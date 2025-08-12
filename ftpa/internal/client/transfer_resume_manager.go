package internal

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"ftpa/internal/types"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// TransferResumeManager handles saving and resuming transfer state
type TransferResumeManager struct {
	mu           sync.RWMutex
	stateDir     string                           // Directory to store transfer state
	activeStates map[string]*TransferResumeState // Active transfer states by filename
	config       *ResumeConfig                   // Configuration for resume behavior
}

// TransferResumeState contains the state needed to resume a transfer
type TransferResumeState struct {
	Filename        string                    `json:"filename"`
	FileSize        int64                     `json:"file_size"`
	ChunkSize       int32                     `json:"chunk_size"`
	TotalChunks     uint32                    `json:"total_chunks"`
	FileChecksum    string                    `json:"file_checksum"`
	ChecksumType    string                    `json:"checksum_type"`
	ReceivedChunks  map[uint32]*ChunkInfo     `json:"received_chunks"`  // Chunks successfully received
	MissingChunks   []uint32                  `json:"missing_chunks"`   // Chunks still needed
	PartialChunks   map[uint32]*PartialChunk  `json:"partial_chunks"`   // Partially received chunks
	TransferID      string                    `json:"transfer_id"`      // Unique transfer identifier
	StartTime       time.Time                 `json:"start_time"`       // When transfer started
	LastSaveTime    time.Time                 `json:"last_save_time"`   // When state was last saved
	ResumeCount     int                       `json:"resume_count"`     // Number of times resumed
	TempFilePath    string                    `json:"temp_file_path"`   // Path to temporary file
	Metadata        map[string]string         `json:"metadata"`         // Additional metadata
	Version         int                       `json:"version"`          // State format version
}

// ChunkInfo contains information about a received chunk
type ChunkInfo struct {
	SequenceNum uint32    `json:"sequence_num"`
	ChunkID     uint32    `json:"chunk_id"`
	Size        int32     `json:"size"`
	Offset      int64     `json:"offset"`
	Checksum    string    `json:"checksum"`
	ReceivedAt  time.Time `json:"received_at"`
	Validated   bool      `json:"validated"`
}

// PartialChunk represents a chunk that was partially received
type PartialChunk struct {
	SequenceNum    uint32    `json:"sequence_num"`
	ChunkID        uint32    `json:"chunk_id"`
	ExpectedSize   int32     `json:"expected_size"`
	ReceivedBytes  int32     `json:"received_bytes"`
	Data           []byte    `json:"data"`           // Partial data received
	LastUpdate     time.Time `json:"last_update"`
	RetryCount     int       `json:"retry_count"`
	IsCorrupted    bool      `json:"is_corrupted"`
}

// ResumeConfig contains configuration for transfer resumption
type ResumeConfig struct {
	StateDir              string        `json:"state_dir"`               // Directory for state files
	SaveInterval          time.Duration `json:"save_interval"`           // How often to save state
	MaxResumeAttempts     int           `json:"max_resume_attempts"`     // Max resume attempts
	StateRetentionTime    time.Duration `json:"state_retention_time"`    // How long to keep state files
	EnablePartialChunks   bool          `json:"enable_partial_chunks"`   // Whether to save partial chunks
	ValidateOnResume      bool          `json:"validate_on_resume"`      // Whether to validate chunks on resume
	CompressStateFiles    bool          `json:"compress_state_files"`    // Whether to compress state files
	AutoCleanup           bool          `json:"auto_cleanup"`            // Whether to auto-cleanup old states
	CleanupInterval       time.Duration `json:"cleanup_interval"`        // How often to cleanup old states
}

// DefaultResumeConfig returns a default resume configuration
func DefaultResumeConfig() *ResumeConfig {
	return &ResumeConfig{
		StateDir:              ".transfer_state",
		SaveInterval:          30 * time.Second,
		MaxResumeAttempts:     5,
		StateRetentionTime:    24 * time.Hour,
		EnablePartialChunks:   true,
		ValidateOnResume:      true,
		CompressStateFiles:    false,
		AutoCleanup:           true,
		CleanupInterval:       1 * time.Hour,
	}
}

// NewTransferResumeManager creates a new transfer resume manager
func NewTransferResumeManager(config *ResumeConfig) (*TransferResumeManager, error) {
	if config == nil {
		config = DefaultResumeConfig()
	}

	// Create state directory if it doesn't exist
	if err := os.MkdirAll(config.StateDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create state directory: %w", err)
	}

	manager := &TransferResumeManager{
		stateDir:     config.StateDir,
		activeStates: make(map[string]*TransferResumeState),
		config:       config,
	}

	// Start cleanup goroutine if auto-cleanup is enabled
	if config.AutoCleanup {
		go manager.cleanupLoop()
	}

	return manager, nil
}

// StartTransfer initializes a new transfer state or resumes an existing one
func (trm *TransferResumeManager) StartTransfer(metadata *types.FileMetadata, transferID string) (*TransferResumeState, bool, error) {
	trm.mu.Lock()
	defer trm.mu.Unlock()

	// Check if we have an existing state to resume
	existingState, err := trm.loadTransferState(metadata.Filename)
	if err == nil && existingState != nil {
		// Validate that the file hasn't changed
		if trm.canResumeTransfer(existingState, metadata) {
			existingState.ResumeCount++
			existingState.LastSaveTime = time.Now()
			trm.activeStates[metadata.Filename] = existingState
			
			// Save updated state
			trm.saveTransferStateInternal(existingState)
			
			return existingState, true, nil // true indicates resume
		} else {
			// File has changed, remove old state and start fresh
			trm.removeTransferStateFile(metadata.Filename)
		}
	}

	// Create new transfer state
	state := &TransferResumeState{
		Filename:       metadata.Filename,
		FileSize:       metadata.Size,
		ChunkSize:      metadata.ChunkSize,
		TotalChunks:    metadata.TotalChunks,
		FileChecksum:   metadata.Checksum,
		ChecksumType:   metadata.ChecksumType,
		ReceivedChunks: make(map[uint32]*ChunkInfo),
		MissingChunks:  make([]uint32, 0, metadata.TotalChunks),
		PartialChunks:  make(map[uint32]*PartialChunk),
		TransferID:     transferID,
		StartTime:      time.Now(),
		LastSaveTime:   time.Now(),
		ResumeCount:    0,
		TempFilePath:   filepath.Join(trm.config.StateDir, fmt.Sprintf("%s.tmp", metadata.Filename)),
		Metadata:       make(map[string]string),
		Version:        1,
	}

	// Initialize missing chunks list
	for i := uint32(0); i < metadata.TotalChunks; i++ {
		state.MissingChunks = append(state.MissingChunks, i)
	}

	trm.activeStates[metadata.Filename] = state
	
	// Save initial state
	err = trm.saveTransferStateInternal(state)
	if err != nil {
		return nil, false, fmt.Errorf("failed to save initial transfer state: %w", err)
	}

	return state, false, nil // false indicates new transfer
}

// canResumeTransfer checks if a transfer can be resumed
func (trm *TransferResumeManager) canResumeTransfer(state *TransferResumeState, metadata *types.FileMetadata) bool {
	// Check if file size matches
	if state.FileSize != metadata.Size {
		return false
	}

	// Check if chunk size matches
	if state.ChunkSize != metadata.ChunkSize {
		return false
	}

	// Check if total chunks matches
	if state.TotalChunks != metadata.TotalChunks {
		return false
	}

	// Check if file checksum matches (if available)
	if state.FileChecksum != "" && metadata.Checksum != "" && state.FileChecksum != metadata.Checksum {
		return false
	}

	// Check if we haven't exceeded max resume attempts
	if state.ResumeCount >= trm.config.MaxResumeAttempts {
		return false
	}

	// Check if state is not too old
	if time.Since(state.StartTime) > trm.config.StateRetentionTime {
		return false
	}

	return true
}

// RecordChunkReceived records that a chunk has been successfully received
func (trm *TransferResumeManager) RecordChunkReceived(filename string, chunk *types.FileChunk) error {
	trm.mu.Lock()
	defer trm.mu.Unlock()

	state, exists := trm.activeStates[filename]
	if !exists {
		return fmt.Errorf("no active transfer state for file: %s", filename)
	}

	// Create chunk info
	chunkInfo := &ChunkInfo{
		SequenceNum: chunk.SequenceNum,
		ChunkID:     chunk.ChunkID,
		Size:        chunk.Size,
		Offset:      chunk.Offset,
		Checksum:    chunk.Checksum,
		ReceivedAt:  time.Now(),
		Validated:   true, // Assume chunk was validated before calling this
	}

	// Add to received chunks
	state.ReceivedChunks[chunk.SequenceNum] = chunkInfo

	// Remove from missing chunks
	state.MissingChunks = trm.removeFromSlice(state.MissingChunks, chunk.SequenceNum)

	// Remove from partial chunks if it was there
	delete(state.PartialChunks, chunk.SequenceNum)

	state.LastSaveTime = time.Now()

	// Save state periodically
	if time.Since(state.LastSaveTime) > trm.config.SaveInterval {
		return trm.saveTransferStateInternal(state)
	}

	return nil
}

// RecordPartialChunk records a partially received chunk
func (trm *TransferResumeManager) RecordPartialChunk(filename string, sequenceNum uint32, data []byte, expectedSize int32) error {
	if !trm.config.EnablePartialChunks {
		return nil // Partial chunks disabled
	}

	trm.mu.Lock()
	defer trm.mu.Unlock()

	state, exists := trm.activeStates[filename]
	if !exists {
		return fmt.Errorf("no active transfer state for file: %s", filename)
	}

	// Create or update partial chunk
	partialChunk := &PartialChunk{
		SequenceNum:   sequenceNum,
		ChunkID:       sequenceNum + 1, // Assuming ChunkID is SequenceNum + 1
		ExpectedSize:  expectedSize,
		ReceivedBytes: int32(len(data)),
		Data:          make([]byte, len(data)),
		LastUpdate:    time.Now(),
		RetryCount:    0,
		IsCorrupted:   false,
	}

	copy(partialChunk.Data, data)

	// If we already have a partial chunk, update retry count
	if existing, exists := state.PartialChunks[sequenceNum]; exists {
		partialChunk.RetryCount = existing.RetryCount + 1
	}

	state.PartialChunks[sequenceNum] = partialChunk
	state.LastSaveTime = time.Now()

	return nil
}

// GetMissingChunks returns the list of chunks still needed for a transfer
func (trm *TransferResumeManager) GetMissingChunks(filename string) ([]uint32, error) {
	trm.mu.RLock()
	defer trm.mu.RUnlock()

	state, exists := trm.activeStates[filename]
	if !exists {
		return nil, fmt.Errorf("no active transfer state for file: %s", filename)
	}

	// Return a copy of missing chunks
	missing := make([]uint32, len(state.MissingChunks))
	copy(missing, state.MissingChunks)

	return missing, nil
}

// GetReceivedChunks returns the list of chunks already received
func (trm *TransferResumeManager) GetReceivedChunks(filename string) (map[uint32]*ChunkInfo, error) {
	trm.mu.RLock()
	defer trm.mu.RUnlock()

	state, exists := trm.activeStates[filename]
	if !exists {
		return nil, fmt.Errorf("no active transfer state for file: %s", filename)
	}

	// Return a copy of received chunks
	received := make(map[uint32]*ChunkInfo)
	for seq, info := range state.ReceivedChunks {
		received[seq] = &ChunkInfo{
			SequenceNum: info.SequenceNum,
			ChunkID:     info.ChunkID,
			Size:        info.Size,
			Offset:      info.Offset,
			Checksum:    info.Checksum,
			ReceivedAt:  info.ReceivedAt,
			Validated:   info.Validated,
		}
	}

	return received, nil
}

// GetPartialChunks returns the list of partially received chunks
func (trm *TransferResumeManager) GetPartialChunks(filename string) (map[uint32]*PartialChunk, error) {
	trm.mu.RLock()
	defer trm.mu.RUnlock()

	state, exists := trm.activeStates[filename]
	if !exists {
		return nil, fmt.Errorf("no active transfer state for file: %s", filename)
	}

	// Return a copy of partial chunks
	partial := make(map[uint32]*PartialChunk)
	for seq, chunk := range state.PartialChunks {
		partial[seq] = &PartialChunk{
			SequenceNum:   chunk.SequenceNum,
			ChunkID:       chunk.ChunkID,
			ExpectedSize:  chunk.ExpectedSize,
			ReceivedBytes: chunk.ReceivedBytes,
			Data:          make([]byte, len(chunk.Data)),
			LastUpdate:    chunk.LastUpdate,
			RetryCount:    chunk.RetryCount,
			IsCorrupted:   chunk.IsCorrupted,
		}
		copy(partial[seq].Data, chunk.Data)
	}

	return partial, nil
}

// GetTransferProgress returns the current progress of a transfer
func (trm *TransferResumeManager) GetTransferProgress(filename string) (*ResumeTransferProgress, error) {
	trm.mu.RLock()
	defer trm.mu.RUnlock()

	state, exists := trm.activeStates[filename]
	if !exists {
		return nil, fmt.Errorf("no active transfer state for file: %s", filename)
	}

	receivedChunks := len(state.ReceivedChunks)
	totalChunks := int(state.TotalChunks)
	missingChunks := len(state.MissingChunks)
	partialChunks := len(state.PartialChunks)

	// Calculate received bytes
	var receivedBytes int64
	for _, chunkInfo := range state.ReceivedChunks {
		receivedBytes += int64(chunkInfo.Size)
	}

	// Add partial chunk bytes
	for _, partialChunk := range state.PartialChunks {
		receivedBytes += int64(partialChunk.ReceivedBytes)
	}

	progress := &ResumeTransferProgress{
		Filename:       filename,
		TotalChunks:    totalChunks,
		ReceivedChunks: receivedChunks,
		MissingChunks:  missingChunks,
		PartialChunks:  partialChunks,
		TotalBytes:     state.FileSize,
		ReceivedBytes:  receivedBytes,
		ResumeCount:    state.ResumeCount,
		StartTime:      state.StartTime,
		LastSaveTime:   state.LastSaveTime,
	}

	return progress, nil
}

// ResumeTransferProgress contains progress information for a resumable transfer
type ResumeTransferProgress struct {
	Filename       string    `json:"filename"`
	TotalChunks    int       `json:"total_chunks"`
	ReceivedChunks int       `json:"received_chunks"`
	MissingChunks  int       `json:"missing_chunks"`
	PartialChunks  int       `json:"partial_chunks"`
	TotalBytes     int64     `json:"total_bytes"`
	ReceivedBytes  int64     `json:"received_bytes"`
	ResumeCount    int       `json:"resume_count"`
	StartTime      time.Time `json:"start_time"`
	LastSaveTime   time.Time `json:"last_save_time"`
}

// GetProgressPercentage returns the transfer progress as a percentage
func (rtp *ResumeTransferProgress) GetProgressPercentage() float64 {
	if rtp.TotalChunks == 0 {
		return 0
	}
	return (float64(rtp.ReceivedChunks) / float64(rtp.TotalChunks)) * 100
}

// GetBytesProgressPercentage returns the bytes progress as a percentage
func (rtp *ResumeTransferProgress) GetBytesProgressPercentage() float64 {
	if rtp.TotalBytes == 0 {
		return 0
	}
	return (float64(rtp.ReceivedBytes) / float64(rtp.TotalBytes)) * 100
}

// SaveTransferState manually saves the current transfer state
func (trm *TransferResumeManager) SaveTransferState(filename string) error {
	trm.mu.Lock()
	defer trm.mu.Unlock()

	state, exists := trm.activeStates[filename]
	if !exists {
		return fmt.Errorf("no active transfer state for file: %s", filename)
	}

	return trm.saveTransferStateInternal(state)
}

// saveTransferStateInternal saves transfer state to disk (internal method)
func (trm *TransferResumeManager) saveTransferStateInternal(state *TransferResumeState) error {
	stateFile := trm.getStateFilePath(state.Filename)

	// Create state directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(stateFile), 0755); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}

	// Serialize state to JSON
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal transfer state: %w", err)
	}

	// Write to temporary file first, then rename (atomic operation)
	tempFile := stateFile + ".tmp"
	if err := os.WriteFile(tempFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write state file: %w", err)
	}

	if err := os.Rename(tempFile, stateFile); err != nil {
		os.Remove(tempFile) // Clean up temp file on error
		return fmt.Errorf("failed to rename state file: %w", err)
	}

	state.LastSaveTime = time.Now()
	return nil
}

// loadTransferState loads transfer state from disk
func (trm *TransferResumeManager) loadTransferState(filename string) (*TransferResumeState, error) {
	stateFile := trm.getStateFilePath(filename)

	// Check if state file exists
	if _, err := os.Stat(stateFile); os.IsNotExist(err) {
		return nil, nil // No state file exists
	}

	// Read state file
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}

	// Deserialize state
	var state TransferResumeState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal transfer state: %w", err)
	}

	// Validate state version
	if state.Version != 1 {
		return nil, fmt.Errorf("unsupported state version: %d", state.Version)
	}

	return &state, nil
}

// CompleteTransfer marks a transfer as complete and cleans up state
func (trm *TransferResumeManager) CompleteTransfer(filename string) error {
	trm.mu.Lock()
	defer trm.mu.Unlock()

	// Remove from active states
	delete(trm.activeStates, filename)

	// Remove state file
	return trm.removeTransferStateFile(filename)
}

// CancelTransfer cancels a transfer and optionally removes state
func (trm *TransferResumeManager) CancelTransfer(filename string, removeState bool) error {
	trm.mu.Lock()
	defer trm.mu.Unlock()

	// Remove from active states
	delete(trm.activeStates, filename)

	if removeState {
		return trm.removeTransferStateFile(filename)
	}

	return nil
}

// removeTransferStateFile removes the state file for a transfer
func (trm *TransferResumeManager) removeTransferStateFile(filename string) error {
	stateFile := trm.getStateFilePath(filename)
	if err := os.Remove(stateFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove state file: %w", err)
	}
	return nil
}

// getStateFilePath returns the file path for storing transfer state
func (trm *TransferResumeManager) getStateFilePath(filename string) string {
	// Create a safe filename by hashing the original filename
	hash := sha256.Sum256([]byte(filename))
	safeFilename := fmt.Sprintf("%x.json", hash[:8]) // Use first 8 bytes of hash
	return filepath.Join(trm.stateDir, safeFilename)
}

// removeFromSlice removes a value from a uint32 slice
func (trm *TransferResumeManager) removeFromSlice(slice []uint32, value uint32) []uint32 {
	for i, v := range slice {
		if v == value {
			return append(slice[:i], slice[i+1:]...)
		}
	}
	return slice
}

// ListResumableTransfers returns a list of transfers that can be resumed
func (trm *TransferResumeManager) ListResumableTransfers() ([]string, error) {
	files, err := os.ReadDir(trm.stateDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read state directory: %w", err)
	}

	var resumable []string
	for _, file := range files {
		if !file.IsDir() && filepath.Ext(file.Name()) == ".json" {
			// Try to load the state to verify it's valid
			stateFile := filepath.Join(trm.stateDir, file.Name())
			data, err := os.ReadFile(stateFile)
			if err != nil {
				continue
			}

			var state TransferResumeState
			if err := json.Unmarshal(data, &state); err != nil {
				continue
			}

			// Check if state is not too old
			if time.Since(state.StartTime) <= trm.config.StateRetentionTime {
				resumable = append(resumable, state.Filename)
			}
		}
	}

	return resumable, nil
}

// CleanupOldStates removes old transfer state files
func (trm *TransferResumeManager) CleanupOldStates() error {
	files, err := os.ReadDir(trm.stateDir)
	if err != nil {
		return fmt.Errorf("failed to read state directory: %w", err)
	}

	for _, file := range files {
		if !file.IsDir() && filepath.Ext(file.Name()) == ".json" {
			stateFile := filepath.Join(trm.stateDir, file.Name())
			
			// Check file modification time
			info, err := file.Info()
			if err != nil {
				continue
			}

			if time.Since(info.ModTime()) > trm.config.StateRetentionTime {
				os.Remove(stateFile) // Ignore errors for cleanup
			}
		}
	}

	return nil
}

// cleanupLoop runs periodic cleanup of old state files
func (trm *TransferResumeManager) cleanupLoop() {
	ticker := time.NewTicker(trm.config.CleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		trm.CleanupOldStates() // Ignore errors in background cleanup
	}
}

// ValidateResumedChunks validates chunks when resuming a transfer
func (trm *TransferResumeManager) ValidateResumedChunks(filename string, chunkValidator func(chunkInfo *ChunkInfo) bool) error {
	if !trm.config.ValidateOnResume {
		return nil
	}

	trm.mu.Lock()
	defer trm.mu.Unlock()

	state, exists := trm.activeStates[filename]
	if !exists {
		return fmt.Errorf("no active transfer state for file: %s", filename)
	}

	var invalidChunks []uint32

	// Validate received chunks
	for seq, chunkInfo := range state.ReceivedChunks {
		if !chunkValidator(chunkInfo) {
			invalidChunks = append(invalidChunks, seq)
		}
	}

	// Move invalid chunks back to missing
	for _, seq := range invalidChunks {
		delete(state.ReceivedChunks, seq)
		state.MissingChunks = append(state.MissingChunks, seq)
	}

	// Save updated state if there were invalid chunks
	if len(invalidChunks) > 0 {
		return trm.saveTransferStateInternal(state)
	}

	return nil
}

// GetTransferStatistics returns statistics about all transfers
func (trm *TransferResumeManager) GetTransferStatistics() *ResumeStatistics {
	trm.mu.RLock()
	defer trm.mu.RUnlock()

	stats := &ResumeStatistics{
		ActiveTransfers:   len(trm.activeStates),
		TotalTransfers:    0,
		CompletedTransfers: 0,
		ResumedTransfers:  0,
		TransfersByStatus: make(map[string]int),
	}

	for _, state := range trm.activeStates {
		stats.TotalTransfers++
		if state.ResumeCount > 0 {
			stats.ResumedTransfers++
		}

		// Calculate status
		progress := float64(len(state.ReceivedChunks)) / float64(state.TotalChunks)
		if progress == 1.0 {
			stats.TransfersByStatus["completed"]++
		} else if progress > 0.5 {
			stats.TransfersByStatus["in_progress"]++
		} else {
			stats.TransfersByStatus["starting"]++
		}
	}

	return stats
}

// ResumeStatistics contains statistics about transfer resumption
type ResumeStatistics struct {
	ActiveTransfers    int            `json:"active_transfers"`
	TotalTransfers     int            `json:"total_transfers"`
	CompletedTransfers int            `json:"completed_transfers"`
	ResumedTransfers   int            `json:"resumed_transfers"`
	TransfersByStatus  map[string]int `json:"transfers_by_status"`
}

// Close closes the transfer resume manager and cleans up resources
func (trm *TransferResumeManager) Close() error {
	trm.mu.Lock()
	defer trm.mu.Unlock()

	// Save all active states
	for _, state := range trm.activeStates {
		trm.saveTransferStateInternal(state) // Ignore errors during shutdown
	}

	// Clear active states
	trm.activeStates = make(map[string]*TransferResumeState)

	return nil
}