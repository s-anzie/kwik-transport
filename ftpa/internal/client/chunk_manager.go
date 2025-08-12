package internal

import (
	"crypto/sha256"
	"fmt"
	"ftpa/internal/types"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// ChunkManager handles the temporary storage, validation, and reconstruction of file chunks
type ChunkManager struct {
	mu              sync.RWMutex
	tempDir         string                    // Directory for temporary chunk storage
	activeTransfers map[string]*ChunkTransferState // Active file transfers by filename
	chunkTimeout    time.Duration             // Timeout for chunk reception
}

// ChunkTransferState tracks the state of an ongoing file transfer from client perspective
type ChunkTransferState struct {
	Metadata         *types.FileMetadata         // File metadata
	ReceivedChunks   map[uint32]*types.FileChunk // Chunks received so far (by sequence number)
	MissingChunks    map[uint32]bool       // Chunks that are still missing
	ChunkTimestamps  map[uint32]time.Time  // When each chunk was last requested
	RetryCount       map[uint32]int        // Number of retry attempts per chunk
	CorruptedChunks  map[uint32]int        // Chunks that failed validation (sequence -> failure count)
	LastActivity     time.Time             // Last time a chunk was received
	StartTime        time.Time             // When the transfer started
	TempFilePath     string                // Path to temporary file for reconstruction
	IsComplete       bool                  // Whether all chunks have been received
	FinalChecksum    string                // Final file checksum for validation
	IntegrityPassed  bool                  // Whether final integrity validation passed
	ValidationErrors []ValidationError     // List of validation errors encountered
	ProgressCallback func(progress float64) // Progress callback function
}

// NewChunkManager creates a new ChunkManager instance
func NewChunkManager(tempDir string, chunkTimeout time.Duration) (*ChunkManager, error) {
	// Create temp directory if it doesn't exist
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	return &ChunkManager{
		tempDir:         tempDir,
		activeTransfers: make(map[string]*ChunkTransferState),
		chunkTimeout:    chunkTimeout,
	}, nil
}

// StartTransfer initializes a new file transfer
func (cm *ChunkManager) StartTransfer(metadata *types.FileMetadata, progressCallback func(progress float64)) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Validate metadata
	if err := metadata.Validate(); err != nil {
		return fmt.Errorf("invalid metadata: %w", err)
	}

	// Check if transfer already exists
	if _, exists := cm.activeTransfers[metadata.Filename]; exists {
		return fmt.Errorf("transfer for file %s already in progress", metadata.Filename)
	}

	// Create temporary file path
	tempFilePath := filepath.Join(cm.tempDir, fmt.Sprintf("%s.tmp", metadata.Filename))

	// Initialize missing chunks map
	missingChunks := make(map[uint32]bool)
	for i := uint32(0); i < metadata.TotalChunks; i++ {
		missingChunks[i] = true
	}

	// Create transfer state
	state := &ChunkTransferState{
		Metadata:         metadata,
		ReceivedChunks:   make(map[uint32]*types.FileChunk),
		MissingChunks:    missingChunks,
		ChunkTimestamps:  make(map[uint32]time.Time),
		RetryCount:       make(map[uint32]int),
		CorruptedChunks:  make(map[uint32]int),
		LastActivity:     time.Now(),
		StartTime:        time.Now(),
		TempFilePath:     tempFilePath,
		IsComplete:       false,
		IntegrityPassed:  false,
		ValidationErrors: make([]ValidationError, 0),
		ProgressCallback: progressCallback,
	}

	cm.activeTransfers[metadata.Filename] = state

	return nil
}

// ReceiveChunk processes a received chunk and stores it temporarily
func (cm *ChunkManager) ReceiveChunk(chunk *types.FileChunk) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Find the transfer state
	state, exists := cm.activeTransfers[chunk.Filename]
	if !exists {
		return fmt.Errorf("no active transfer found for file: %s", chunk.Filename)
	}

	// Validate chunk checksum
	if err := chunk.ValidateChecksum(); err != nil {
		return fmt.Errorf("chunk validation failed: %w", err)
	}

	// Check if chunk is within expected range
	if chunk.SequenceNum >= state.Metadata.TotalChunks {
		return fmt.Errorf("chunk sequence number %d exceeds total chunks %d", 
			chunk.SequenceNum, state.Metadata.TotalChunks)
	}

	// Check if chunk was already received
	if _, alreadyReceived := state.ReceivedChunks[chunk.SequenceNum]; alreadyReceived {
		// Chunk already received, ignore duplicate
		return nil
	}

	// Store the chunk
	state.ReceivedChunks[chunk.SequenceNum] = chunk
	delete(state.MissingChunks, chunk.SequenceNum)
	state.LastActivity = time.Now()

	// Update progress
	progress := float64(len(state.ReceivedChunks)) / float64(state.Metadata.TotalChunks)
	if state.ProgressCallback != nil {
		state.ProgressCallback(progress)
	}

	// Check if all chunks are received
	if len(state.MissingChunks) == 0 {
		return cm.reconstructFile(state)
	}

	return nil
}

// reconstructFile assembles all chunks into the final file
func (cm *ChunkManager) reconstructFile(state *ChunkTransferState) error {
	// Create temporary file for reconstruction
	tempFile, err := os.Create(state.TempFilePath)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer tempFile.Close()

	// Sort chunks by sequence number
	sequences := make([]uint32, 0, len(state.ReceivedChunks))
	for seq := range state.ReceivedChunks {
		sequences = append(sequences, seq)
	}
	sort.Slice(sequences, func(i, j int) bool {
		return sequences[i] < sequences[j]
	})

	// Write chunks in order and compute final checksum
	hasher := sha256.New()
	var totalBytesWritten int64

	for _, seq := range sequences {
		chunk := state.ReceivedChunks[seq]
		
		// Verify chunk is at expected position
		expectedOffset := state.Metadata.GetChunkOffset(seq)
		if expectedOffset != totalBytesWritten {
			return fmt.Errorf("chunk %d at wrong position: expected %d, got %d", 
				seq, expectedOffset, totalBytesWritten)
		}

		// Write chunk data
		n, err := tempFile.Write(chunk.Data)
		if err != nil {
			return fmt.Errorf("failed to write chunk %d: %w", seq, err)
		}

		// Update hash and byte count
		hasher.Write(chunk.Data)
		totalBytesWritten += int64(n)
	}

	// Verify total file size
	if totalBytesWritten != state.Metadata.Size {
		return fmt.Errorf("reconstructed file size mismatch: expected %d, got %d", 
			state.Metadata.Size, totalBytesWritten)
	}

	// Compute and store final checksum
	finalChecksum := fmt.Sprintf("%x", hasher.Sum(nil))
	state.FinalChecksum = finalChecksum

	// Validate against expected checksum if provided
	if state.Metadata.Checksum != "" && state.Metadata.Checksum != finalChecksum {
		return fmt.Errorf("final file checksum mismatch: expected %s, got %s", 
			state.Metadata.Checksum, finalChecksum)
	}

	state.IsComplete = true

	// Final progress callback
	if state.ProgressCallback != nil {
		state.ProgressCallback(1.0)
	}

	return nil
}

// GetMissingChunks returns a list of chunk sequence numbers that are still missing
func (cm *ChunkManager) GetMissingChunks(filename string) ([]uint32, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	state, exists := cm.activeTransfers[filename]
	if !exists {
		return nil, fmt.Errorf("no active transfer found for file: %s", filename)
	}

	missing := make([]uint32, 0, len(state.MissingChunks))
	for seq := range state.MissingChunks {
		missing = append(missing, seq)
	}

	sort.Slice(missing, func(i, j int) bool {
		return missing[i] < missing[j]
	})

	return missing, nil
}

// GetTransferProgress returns the current progress of a file transfer
func (cm *ChunkManager) GetTransferProgress(filename string) (*TransferProgress, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	state, exists := cm.activeTransfers[filename]
	if !exists {
		return nil, fmt.Errorf("no active transfer found for file: %s", filename)
	}

	receivedChunks := len(state.ReceivedChunks)
	totalChunks := int(state.Metadata.TotalChunks)
	
	// Calculate received bytes
	var receivedBytes int64
	for _, chunk := range state.ReceivedChunks {
		receivedBytes += int64(chunk.Size)
	}

	// Calculate transfer speed
	elapsed := time.Since(state.StartTime)
	speed := float64(receivedBytes) / elapsed.Seconds()

	// Calculate ETA
	var eta time.Duration
	if speed > 0 {
		remainingBytes := state.Metadata.Size - receivedBytes
		eta = time.Duration(float64(remainingBytes)/speed) * time.Second
	}

	return &TransferProgress{
		Filename:       filename,
		TotalChunks:    totalChunks,
		ReceivedChunks: receivedChunks,
		TotalBytes:     state.Metadata.Size,
		ReceivedBytes:  receivedBytes,
		Speed:          speed,
		ETA:            eta,
	}, nil
}

// IsTransferComplete checks if a file transfer is complete
func (cm *ChunkManager) IsTransferComplete(filename string) (bool, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	state, exists := cm.activeTransfers[filename]
	if !exists {
		return false, fmt.Errorf("no active transfer found for file: %s", filename)
	}

	return state.IsComplete, nil
}

// GetReconstructedFilePath returns the path to the reconstructed file
func (cm *ChunkManager) GetReconstructedFilePath(filename string) (string, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	state, exists := cm.activeTransfers[filename]
	if !exists {
		return "", fmt.Errorf("no active transfer found for file: %s", filename)
	}

	if !state.IsComplete {
		return "", fmt.Errorf("transfer for file %s is not complete", filename)
	}

	return state.TempFilePath, nil
}

// GetFinalChecksum returns the checksum of the reconstructed file
func (cm *ChunkManager) GetFinalChecksum(filename string) (string, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	state, exists := cm.activeTransfers[filename]
	if !exists {
		return "", fmt.Errorf("no active transfer found for file: %s", filename)
	}

	if !state.IsComplete {
		return "", fmt.Errorf("transfer for file %s is not complete", filename)
	}

	return state.FinalChecksum, nil
}

// CleanupTransfer removes a completed or failed transfer and cleans up temporary files
func (cm *ChunkManager) CleanupTransfer(filename string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	state, exists := cm.activeTransfers[filename]
	if !exists {
		return fmt.Errorf("no active transfer found for file: %s", filename)
	}

	// Remove temporary file if it exists
	if state.TempFilePath != "" {
		if err := os.Remove(state.TempFilePath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove temp file: %w", err)
		}
	}

	// Remove from active transfers
	delete(cm.activeTransfers, filename)

	return nil
}

// GetTimedOutTransfers returns a list of transfers that have exceeded the timeout
func (cm *ChunkManager) GetTimedOutTransfers() []string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	var timedOut []string
	now := time.Now()

	for filename, state := range cm.activeTransfers {
		if !state.IsComplete && now.Sub(state.LastActivity) > cm.chunkTimeout {
			timedOut = append(timedOut, filename)
		}
	}

	return timedOut
}

// TransferProgress represents the current state of a file transfer
type TransferProgress struct {
	Filename       string        `json:"filename"`
	TotalChunks    int           `json:"total_chunks"`
	ReceivedChunks int           `json:"received_chunks"`
	TotalBytes     int64         `json:"total_bytes"`
	ReceivedBytes  int64         `json:"received_bytes"`
	Speed          float64       `json:"speed"`          // bytes per second
	ETA            time.Duration `json:"eta"`            // estimated time to completion
}

// GetProgressPercentage returns the transfer progress as a percentage (0-100)
func (tp *TransferProgress) GetProgressPercentage() float64 {
	if tp.TotalChunks == 0 {
		return 0
	}
	return (float64(tp.ReceivedChunks) / float64(tp.TotalChunks)) * 100
}

// GetSpeedMBps returns the transfer speed in MB/s
func (tp *TransferProgress) GetSpeedMBps() float64 {
	return tp.Speed / (1024 * 1024)
}



// MarkChunkRequested marks a chunk as requested and updates its timestamp
func (cm *ChunkManager) MarkChunkRequested(filename string, sequenceNum uint32) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	state, exists := cm.activeTransfers[filename]
	if !exists {
		return fmt.Errorf("no active transfer found for file: %s", filename)
	}

	// Only mark if chunk is still missing
	if _, isMissing := state.MissingChunks[sequenceNum]; !isMissing {
		return nil // Chunk already received
	}

	state.ChunkTimestamps[sequenceNum] = time.Now()
	state.RetryCount[sequenceNum]++

	return nil
}

// GetTimedOutChunks returns chunks that have exceeded the timeout and need retransmission
func (cm *ChunkManager) GetTimedOutChunks(filename string) ([]types.ChunkRetryRequest, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	state, exists := cm.activeTransfers[filename]
	if !exists {
		return nil, fmt.Errorf("no active transfer found for file: %s", filename)
	}

	var timedOutChunks []types.ChunkRetryRequest
	now := time.Now()

	for sequenceNum := range state.MissingChunks {
		lastRequest, hasTimestamp := state.ChunkTimestamps[sequenceNum]
		retryCount := state.RetryCount[sequenceNum]

		// If chunk was never requested or has timed out
		if !hasTimestamp || now.Sub(lastRequest) > cm.chunkTimeout {
			// Calculate priority based on how long it's been missing
			priority := int(now.Sub(state.StartTime).Minutes()) + retryCount*10

			timedOutChunks = append(timedOutChunks, types.ChunkRetryRequest{
				Filename:    filename,
				ChunkID:     sequenceNum + 1, // ChunkID is 1-based
				SequenceNum: sequenceNum,
				RetryCount:  retryCount,
				LastRequest: lastRequest,
				Priority:    priority,
			})
		}
	}

	// Sort by priority (highest first)
	sort.Slice(timedOutChunks, func(i, j int) bool {
		return timedOutChunks[i].Priority > timedOutChunks[j].Priority
	})

	return timedOutChunks, nil
}

// GetAllTimedOutChunks returns timed out chunks for all active transfers
func (cm *ChunkManager) GetAllTimedOutChunks() ([]types.ChunkRetryRequest, error) {
	cm.mu.RLock()
	filenames := make([]string, 0, len(cm.activeTransfers))
	for filename := range cm.activeTransfers {
		filenames = append(filenames, filename)
	}
	cm.mu.RUnlock()

	var allTimedOut []types.ChunkRetryRequest

	for _, filename := range filenames {
		timedOut, err := cm.GetTimedOutChunks(filename)
		if err != nil {
			continue // Skip files with errors
		}
		allTimedOut = append(allTimedOut, timedOut...)
	}

	// Sort all timed out chunks by priority
	sort.Slice(allTimedOut, func(i, j int) bool {
		return allTimedOut[i].Priority > allTimedOut[j].Priority
	})

	return allTimedOut, nil
}

// GetChunkRetryCount returns the number of retry attempts for a specific chunk
func (cm *ChunkManager) GetChunkRetryCount(filename string, sequenceNum uint32) (int, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	state, exists := cm.activeTransfers[filename]
	if !exists {
		return 0, fmt.Errorf("no active transfer found for file: %s", filename)
	}

	return state.RetryCount[sequenceNum], nil
}

// ShouldRetryChunk determines if a chunk should be retried based on retry count and policy
func (cm *ChunkManager) ShouldRetryChunk(filename string, sequenceNum uint32, maxRetries int) (bool, error) {
	retryCount, err := cm.GetChunkRetryCount(filename, sequenceNum)
	if err != nil {
		return false, err
	}

	return retryCount < maxRetries, nil
}

// GetMissingChunksWithTimeout returns missing chunks that have exceeded timeout
func (cm *ChunkManager) GetMissingChunksWithTimeout(filename string) ([]uint32, error) {
	timedOutChunks, err := cm.GetTimedOutChunks(filename)
	if err != nil {
		return nil, err
	}

	sequences := make([]uint32, len(timedOutChunks))
	for i, chunk := range timedOutChunks {
		sequences[i] = chunk.SequenceNum
	}

	return sequences, nil
}

// GetTransferStatistics returns detailed statistics about a transfer
func (cm *ChunkManager) GetTransferStatistics(filename string) (*TransferStatistics, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	state, exists := cm.activeTransfers[filename]
	if !exists {
		return nil, fmt.Errorf("no active transfer found for file: %s", filename)
	}

	// Calculate retry statistics
	totalRetries := 0
	maxRetries := 0
	chunksWithRetries := 0

	for _, retryCount := range state.RetryCount {
		totalRetries += retryCount
		if retryCount > maxRetries {
			maxRetries = retryCount
		}
		if retryCount > 0 {
			chunksWithRetries++
		}
	}

	// Calculate timing statistics
	now := time.Now()
	elapsed := now.Sub(state.StartTime)
	timeSinceLastActivity := now.Sub(state.LastActivity)

	return &TransferStatistics{
		Filename:              filename,
		TotalChunks:           int(state.Metadata.TotalChunks),
		ReceivedChunks:        len(state.ReceivedChunks),
		MissingChunks:         len(state.MissingChunks),
		TotalRetries:          totalRetries,
		MaxRetries:            maxRetries,
		ChunksWithRetries:     chunksWithRetries,
		ElapsedTime:           elapsed,
		TimeSinceLastActivity: timeSinceLastActivity,
		IsComplete:            state.IsComplete,
		StartTime:             state.StartTime,
		LastActivity:          state.LastActivity,
	}, nil
}

// TransferStatistics provides detailed statistics about a file transfer
type TransferStatistics struct {
	Filename              string        `json:"filename"`
	TotalChunks           int           `json:"total_chunks"`
	ReceivedChunks        int           `json:"received_chunks"`
	MissingChunks         int           `json:"missing_chunks"`
	TotalRetries          int           `json:"total_retries"`
	MaxRetries            int           `json:"max_retries"`
	ChunksWithRetries     int           `json:"chunks_with_retries"`
	ElapsedTime           time.Duration `json:"elapsed_time"`
	TimeSinceLastActivity time.Duration `json:"time_since_last_activity"`
	IsComplete            bool          `json:"is_complete"`
	StartTime             time.Time     `json:"start_time"`
	LastActivity          time.Time     `json:"last_activity"`
}

// GetRetryRate returns the percentage of chunks that required retries
func (ts *TransferStatistics) GetRetryRate() float64 {
	if ts.TotalChunks == 0 {
		return 0
	}
	return (float64(ts.ChunksWithRetries) / float64(ts.TotalChunks)) * 100
}

// GetAverageRetriesPerChunk returns the average number of retries per chunk
func (ts *TransferStatistics) GetAverageRetriesPerChunk() float64 {
	if ts.TotalChunks == 0 {
		return 0
	}
	return float64(ts.TotalRetries) / float64(ts.TotalChunks)
}

// ValidationError represents an integrity validation error
type ValidationError struct {
	Type        string    `json:"type"`         // Type of validation error
	ChunkID     uint32    `json:"chunk_id"`     // Chunk ID where error occurred (0 for file-level errors)
	SequenceNum uint32    `json:"sequence_num"` // Sequence number of the chunk
	Expected    string    `json:"expected"`     // Expected value
	Actual      string    `json:"actual"`       // Actual value
	Message     string    `json:"message"`      // Human-readable error message
	Timestamp   time.Time `json:"timestamp"`    // When the error occurred
	Retryable   bool      `json:"retryable"`    // Whether this error can be retried
}

// ValidationErrorType constants
const (
	ValidationErrorChunkChecksum = "chunk_checksum"
	ValidationErrorFileChecksum  = "file_checksum"
	ValidationErrorFileSize      = "file_size"
	ValidationErrorChunkSize     = "chunk_size"
	ValidationErrorChunkOrder    = "chunk_order"
	ValidationErrorCorruption    = "corruption"
)

// ReceiveChunkWithValidation processes a received chunk with comprehensive validation
func (cm *ChunkManager) ReceiveChunkWithValidation(chunk *types.FileChunk, maxCorruptionRetries int) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Find the transfer state
	state, exists := cm.activeTransfers[chunk.Filename]
	if !exists {
		return fmt.Errorf("no active transfer found for file: %s", chunk.Filename)
	}

	// Check if chunk is within expected range
	if chunk.SequenceNum >= state.Metadata.TotalChunks {
		validationError := ValidationError{
			Type:        ValidationErrorChunkOrder,
			ChunkID:     chunk.ChunkID,
			SequenceNum: chunk.SequenceNum,
			Expected:    fmt.Sprintf("< %d", state.Metadata.TotalChunks),
			Actual:      fmt.Sprintf("%d", chunk.SequenceNum),
			Message:     fmt.Sprintf("chunk sequence number %d exceeds total chunks %d", chunk.SequenceNum, state.Metadata.TotalChunks),
			Timestamp:   time.Now(),
			Retryable:   false,
		}
		state.ValidationErrors = append(state.ValidationErrors, validationError)
		return fmt.Errorf("chunk validation failed: %s", validationError.Message)
	}

	// Validate expected chunk size
	expectedSize := state.Metadata.GetChunkSize(chunk.SequenceNum)
	if chunk.Size != expectedSize {
		validationError := ValidationError{
			Type:        ValidationErrorChunkSize,
			ChunkID:     chunk.ChunkID,
			SequenceNum: chunk.SequenceNum,
			Expected:    fmt.Sprintf("%d", expectedSize),
			Actual:      fmt.Sprintf("%d", chunk.Size),
			Message:     fmt.Sprintf("chunk %d size mismatch: expected %d, got %d", chunk.SequenceNum, expectedSize, chunk.Size),
			Timestamp:   time.Now(),
			Retryable:   false, // Size mismatch is a critical error
		}
		state.ValidationErrors = append(state.ValidationErrors, validationError)
		state.CorruptedChunks[chunk.SequenceNum]++
		
		return fmt.Errorf("chunk validation failed (critical): %s", validationError.Message)
	}

	// Validate chunk checksum
	if err := chunk.ValidateChecksum(); err != nil {
		validationError := ValidationError{
			Type:        ValidationErrorChunkChecksum,
			ChunkID:     chunk.ChunkID,
			SequenceNum: chunk.SequenceNum,
			Expected:    chunk.Checksum,
			Actual:      "corrupted",
			Message:     fmt.Sprintf("chunk %d checksum validation failed: %v", chunk.SequenceNum, err),
			Timestamp:   time.Now(),
			Retryable:   true,
		}
		state.ValidationErrors = append(state.ValidationErrors, validationError)
		state.CorruptedChunks[chunk.SequenceNum]++
		
		// Check if we should retry this chunk
		if state.CorruptedChunks[chunk.SequenceNum] <= maxCorruptionRetries {
			return fmt.Errorf("chunk validation failed (retryable): %s", validationError.Message)
		} else {
			return fmt.Errorf("chunk validation failed (max retries exceeded): %s", validationError.Message)
		}
	}

	// Check if chunk was already received
	if _, alreadyReceived := state.ReceivedChunks[chunk.SequenceNum]; alreadyReceived {
		// Chunk already received, ignore duplicate
		return nil
	}

	// Store the chunk
	state.ReceivedChunks[chunk.SequenceNum] = chunk
	delete(state.MissingChunks, chunk.SequenceNum)
	state.LastActivity = time.Now()

	// Update progress
	progress := float64(len(state.ReceivedChunks)) / float64(state.Metadata.TotalChunks)
	if state.ProgressCallback != nil {
		state.ProgressCallback(progress)
	}

	// Check if all chunks are received
	if len(state.MissingChunks) == 0 {
		return cm.reconstructFileWithValidation(state)
	}

	return nil
}

// reconstructFileWithValidation assembles all chunks with comprehensive validation
func (cm *ChunkManager) reconstructFileWithValidation(state *ChunkTransferState) error {
	// Create temporary file for reconstruction
	tempFile, err := os.Create(state.TempFilePath)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer tempFile.Close()

	// Sort chunks by sequence number
	sequences := make([]uint32, 0, len(state.ReceivedChunks))
	for seq := range state.ReceivedChunks {
		sequences = append(sequences, seq)
	}
	sort.Slice(sequences, func(i, j int) bool {
		return sequences[i] < sequences[j]
	})

	// Validate we have all expected chunks
	for i := uint32(0); i < state.Metadata.TotalChunks; i++ {
		if _, exists := state.ReceivedChunks[i]; !exists {
			validationError := ValidationError{
				Type:        ValidationErrorChunkOrder,
				ChunkID:     i + 1,
				SequenceNum: i,
				Expected:    "present",
				Actual:      "missing",
				Message:     fmt.Sprintf("chunk %d is missing during reconstruction", i),
				Timestamp:   time.Now(),
				Retryable:   true,
			}
			state.ValidationErrors = append(state.ValidationErrors, validationError)
			return fmt.Errorf("reconstruction failed: %s", validationError.Message)
		}
	}

	// Write chunks in order and compute final checksum
	hasher := sha256.New()
	var totalBytesWritten int64

	for _, seq := range sequences {
		chunk := state.ReceivedChunks[seq]
		
		// Verify chunk is at expected position
		expectedOffset := state.Metadata.GetChunkOffset(seq)
		if expectedOffset != totalBytesWritten {
			validationError := ValidationError{
				Type:        ValidationErrorChunkOrder,
				ChunkID:     chunk.ChunkID,
				SequenceNum: seq,
				Expected:    fmt.Sprintf("%d", expectedOffset),
				Actual:      fmt.Sprintf("%d", totalBytesWritten),
				Message:     fmt.Sprintf("chunk %d at wrong position: expected %d, got %d", seq, expectedOffset, totalBytesWritten),
				Timestamp:   time.Now(),
				Retryable:   false,
			}
			state.ValidationErrors = append(state.ValidationErrors, validationError)
			return fmt.Errorf("reconstruction failed: %s", validationError.Message)
		}

		// Re-validate chunk before writing
		if err := chunk.ValidateChecksum(); err != nil {
			validationError := ValidationError{
				Type:        ValidationErrorCorruption,
				ChunkID:     chunk.ChunkID,
				SequenceNum: seq,
				Expected:    chunk.Checksum,
				Actual:      "corrupted",
				Message:     fmt.Sprintf("chunk %d corrupted during reconstruction: %v", seq, err),
				Timestamp:   time.Now(),
				Retryable:   true,
			}
			state.ValidationErrors = append(state.ValidationErrors, validationError)
			return fmt.Errorf("reconstruction failed: %s", validationError.Message)
		}

		// Write chunk data
		n, err := tempFile.Write(chunk.Data)
		if err != nil {
			return fmt.Errorf("failed to write chunk %d: %w", seq, err)
		}

		// Update hash and byte count
		hasher.Write(chunk.Data)
		totalBytesWritten += int64(n)
	}

	// Verify total file size
	if totalBytesWritten != state.Metadata.Size {
		validationError := ValidationError{
			Type:        ValidationErrorFileSize,
			ChunkID:     0, // File-level error
			SequenceNum: 0,
			Expected:    fmt.Sprintf("%d", state.Metadata.Size),
			Actual:      fmt.Sprintf("%d", totalBytesWritten),
			Message:     fmt.Sprintf("reconstructed file size mismatch: expected %d, got %d", state.Metadata.Size, totalBytesWritten),
			Timestamp:   time.Now(),
			Retryable:   false,
		}
		state.ValidationErrors = append(state.ValidationErrors, validationError)
		return fmt.Errorf("reconstruction failed: %s", validationError.Message)
	}

	// Compute and store final checksum
	finalChecksum := fmt.Sprintf("%x", hasher.Sum(nil))
	state.FinalChecksum = finalChecksum

	// Validate against expected checksum if provided
	if state.Metadata.Checksum != "" && state.Metadata.Checksum != finalChecksum {
		validationError := ValidationError{
			Type:        ValidationErrorFileChecksum,
			ChunkID:     0, // File-level error
			SequenceNum: 0,
			Expected:    state.Metadata.Checksum,
			Actual:      finalChecksum,
			Message:     fmt.Sprintf("final file checksum mismatch: expected %s, got %s", state.Metadata.Checksum, finalChecksum),
			Timestamp:   time.Now(),
			Retryable:   false,
		}
		state.ValidationErrors = append(state.ValidationErrors, validationError)
		return fmt.Errorf("reconstruction failed: %s", validationError.Message)
	}

	state.IsComplete = true
	state.IntegrityPassed = true

	// Final progress callback
	if state.ProgressCallback != nil {
		state.ProgressCallback(1.0)
	}

	return nil
}

// GetValidationErrors returns all validation errors for a transfer
func (cm *ChunkManager) GetValidationErrors(filename string) ([]ValidationError, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	state, exists := cm.activeTransfers[filename]
	if !exists {
		return nil, fmt.Errorf("no active transfer found for file: %s", filename)
	}

	// Return a copy of the validation errors
	errors := make([]ValidationError, len(state.ValidationErrors))
	copy(errors, state.ValidationErrors)

	return errors, nil
}

// GetCorruptedChunks returns chunks that have failed validation
func (cm *ChunkManager) GetCorruptedChunks(filename string) (map[uint32]int, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	state, exists := cm.activeTransfers[filename]
	if !exists {
		return nil, fmt.Errorf("no active transfer found for file: %s", filename)
	}

	// Return a copy of the corrupted chunks map
	corrupted := make(map[uint32]int)
	for seq, count := range state.CorruptedChunks {
		corrupted[seq] = count
	}

	return corrupted, nil
}

// IsIntegrityValidationPassed checks if the transfer passed all integrity validations
func (cm *ChunkManager) IsIntegrityValidationPassed(filename string) (bool, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	state, exists := cm.activeTransfers[filename]
	if !exists {
		return false, fmt.Errorf("no active transfer found for file: %s", filename)
	}

	return state.IntegrityPassed, nil
}

// RetryCorruptedChunk marks a corrupted chunk for retry
func (cm *ChunkManager) RetryCorruptedChunk(filename string, sequenceNum uint32) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	state, exists := cm.activeTransfers[filename]
	if !exists {
		return fmt.Errorf("no active transfer found for file: %s", filename)
	}

	// Remove the chunk from received chunks if it exists
	delete(state.ReceivedChunks, sequenceNum)
	
	// Add it back to missing chunks
	state.MissingChunks[sequenceNum] = true
	
	// Reset corruption count for this chunk
	delete(state.CorruptedChunks, sequenceNum)
	
	// Mark as requested for retry
	state.ChunkTimestamps[sequenceNum] = time.Now()
	state.RetryCount[sequenceNum]++

	return nil
}

// GetIntegrityReport returns a comprehensive integrity report for a transfer
func (cm *ChunkManager) GetIntegrityReport(filename string) (*IntegrityReport, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	state, exists := cm.activeTransfers[filename]
	if !exists {
		return nil, fmt.Errorf("no active transfer found for file: %s", filename)
	}

	// Count validation errors by type
	errorCounts := make(map[string]int)
	retryableErrors := 0
	for _, err := range state.ValidationErrors {
		errorCounts[err.Type]++
		if err.Retryable {
			retryableErrors++
		}
	}

	// Count corrupted chunks
	totalCorruptedChunks := len(state.CorruptedChunks)
	totalCorruptionAttempts := 0
	for _, count := range state.CorruptedChunks {
		totalCorruptionAttempts += count
	}

	return &IntegrityReport{
		Filename:                filename,
		IsComplete:              state.IsComplete,
		IntegrityPassed:         state.IntegrityPassed,
		TotalValidationErrors:   len(state.ValidationErrors),
		RetryableErrors:         retryableErrors,
		ErrorsByType:            errorCounts,
		CorruptedChunks:         totalCorruptedChunks,
		TotalCorruptionAttempts: totalCorruptionAttempts,
		FinalChecksum:           state.FinalChecksum,
		ExpectedChecksum:        state.Metadata.Checksum,
		ValidationErrors:        state.ValidationErrors,
	}, nil
}

// IntegrityReport provides a comprehensive report on transfer integrity
type IntegrityReport struct {
	Filename                string                 `json:"filename"`
	IsComplete              bool                   `json:"is_complete"`
	IntegrityPassed         bool                   `json:"integrity_passed"`
	TotalValidationErrors   int                    `json:"total_validation_errors"`
	RetryableErrors         int                    `json:"retryable_errors"`
	ErrorsByType            map[string]int         `json:"errors_by_type"`
	CorruptedChunks         int                    `json:"corrupted_chunks"`
	TotalCorruptionAttempts int                    `json:"total_corruption_attempts"`
	FinalChecksum           string                 `json:"final_checksum"`
	ExpectedChecksum        string                 `json:"expected_checksum"`
	ValidationErrors        []ValidationError      `json:"validation_errors"`
}

// HasCriticalErrors returns true if there are non-retryable validation errors
func (ir *IntegrityReport) HasCriticalErrors() bool {
	return ir.TotalValidationErrors > ir.RetryableErrors
}

// GetCorruptionRate returns the percentage of chunks that were corrupted
func (ir *IntegrityReport) GetCorruptionRate(totalChunks int) float64 {
	if totalChunks == 0 {
		return 0
	}
	return (float64(ir.CorruptedChunks) / float64(totalChunks)) * 100
}