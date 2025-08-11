package filetransfer

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ChunkCoordinator manages the distribution of file chunks between primary and secondary servers
type ChunkCoordinator struct {
	primarySession   Session                    // Primary server session
	secondaryPaths   []string                   // Secondary server path IDs
	chunkSize        int32                      // Size of each chunk in bytes
	activeTransfers  map[string]*TransferState  // Active transfers by filename
	transfersMutex   sync.RWMutex               // Protects activeTransfers map
	fileDirectory    string                     // Directory containing files to serve
	maxConcurrent    int                        // Maximum concurrent transfers
	ctx              context.Context            // Context for cancellation
	cancel           context.CancelFunc         // Cancel function
}

// TransferState tracks the state of an active file transfer
type TransferState struct {
	Filename         string                    // Name of the file being transferred
	Metadata         *FileMetadata             // File metadata
	PrimaryChunks    []uint32                  // Chunk sequence numbers assigned to primary server
	SecondaryChunks  []uint32                  // Chunk sequence numbers assigned to secondary server
	CompletedChunks  map[uint32]bool           // Chunks that have been sent successfully
	FailedChunks     map[uint32]int            // Chunks that failed with retry count
	StartTime        time.Time                 // When the transfer started
	LastActivity     time.Time                 // Last activity timestamp
	ClientAddress    string                    // Address of the requesting client
	Status           TransferStatus            // Current transfer status
	ChunkAssignments map[uint32]ChunkAssignment // Assignment details for each chunk
	mutex            sync.RWMutex              // Protects this transfer state
}

// ChunkAssignment tracks which server is responsible for a chunk
type ChunkAssignment struct {
	ChunkID      uint32        // Chunk identifier
	SequenceNum  uint32        // Sequential number in file
	AssignedTo   ServerType    // Which server is assigned this chunk
	Status       ChunkStatus   // Current status of this chunk
	AssignedAt   time.Time     // When the chunk was assigned
	SentAt       time.Time     // When the chunk was sent (if applicable)
	RetryCount   int           // Number of retry attempts
	LastError    string        // Last error message if any
}

// ServerType represents which server is handling a chunk
type ServerType int

const (
	ServerTypePrimary ServerType = iota
	ServerTypeSecondary
)

// ChunkStatus represents the current status of a chunk
type ChunkStatus int

const (
	ChunkStatusPending ChunkStatus = iota // Assigned but not yet sent
	ChunkStatusSending                    // Currently being sent
	ChunkStatusSent                       // Successfully sent
	ChunkStatusFailed                     // Failed to send
	ChunkStatusRetrying                   // Being retried
)

// TransferStatus represents the overall status of a file transfer
type TransferStatus int

const (
	TransferStatusPreparing TransferStatus = iota // Analyzing file and creating chunks
	TransferStatusActive                          // Actively sending chunks
	TransferStatusCompleted                       // All chunks sent successfully
	TransferStatusFailed                          // Transfer failed
	TransferStatusCancelled                       // Transfer was cancelled
)

// DistributionStrategy defines how chunks are distributed between servers
type DistributionStrategy int

const (
	DistributionStrategyAlternating DistributionStrategy = iota // Alternate between servers
	DistributionStrategyBandwidth                               // Based on bandwidth measurements
	DistributionStrategyRoundRobin                              // Round-robin distribution
	DistributionStrategyPrimaryFirst                            // Primary server gets priority
)

// NewChunkCoordinator creates a new chunk coordinator
func NewChunkCoordinator(primarySession Session, secondaryPaths []string, fileDirectory string, chunkSize int32, maxConcurrent int) (*ChunkCoordinator, error) {
	// Validate parameters
	if primarySession == nil {
		return nil, fmt.Errorf("primary session cannot be nil")
	}
	
	if chunkSize <= 0 {
		return nil, fmt.Errorf("chunk size must be positive: %d", chunkSize)
	}
	
	if maxConcurrent <= 0 {
		maxConcurrent = 10 // Default to 10 concurrent transfers
	}
	
	// Verify file directory exists
	if _, err := os.Stat(fileDirectory); os.IsNotExist(err) {
		return nil, fmt.Errorf("file directory does not exist: %s", fileDirectory)
	}
	
	ctx, cancel := context.WithCancel(context.Background())
	
	coordinator := &ChunkCoordinator{
		primarySession:  primarySession,
		secondaryPaths:  secondaryPaths,
		chunkSize:       chunkSize,
		activeTransfers: make(map[string]*TransferState),
		fileDirectory:   fileDirectory,
		maxConcurrent:   maxConcurrent,
		ctx:             ctx,
		cancel:          cancel,
	}
	
	// Start background monitoring
	go coordinator.monitorTransfers()
	go coordinator.handleSecondaryResponses()
	
	return coordinator, nil
}

// StartFileTransfer initiates a file transfer with chunk distribution
func (cc *ChunkCoordinator) StartFileTransfer(filename string, clientAddress string) (*TransferState, error) {
	cc.transfersMutex.Lock()
	defer cc.transfersMutex.Unlock()
	
	// Check if transfer is already active
	if _, exists := cc.activeTransfers[filename]; exists {
		return nil, fmt.Errorf("transfer for file %s is already active", filename)
	}
	
	// Check concurrent transfer limit
	if len(cc.activeTransfers) >= cc.maxConcurrent {
		return nil, fmt.Errorf("maximum concurrent transfers reached: %d", cc.maxConcurrent)
	}
	
	// Analyze file and create metadata
	metadata, err := cc.analyzeFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to analyze file: %w", err)
	}
	
	// Create transfer state
	transferState := &TransferState{
		Filename:         filename,
		Metadata:         metadata,
		PrimaryChunks:    make([]uint32, 0),
		SecondaryChunks:  make([]uint32, 0),
		CompletedChunks:  make(map[uint32]bool),
		FailedChunks:     make(map[uint32]int),
		StartTime:        time.Now(),
		LastActivity:     time.Now(),
		ClientAddress:    clientAddress,
		Status:           TransferStatusPreparing,
		ChunkAssignments: make(map[uint32]ChunkAssignment),
	}
	
	// Distribute chunks between servers
	err = cc.distributeChunks(transferState, DistributionStrategyAlternating)
	if err != nil {
		return nil, fmt.Errorf("failed to distribute chunks: %w", err)
	}
	
	// Add to active transfers
	cc.activeTransfers[filename] = transferState
	
	// Start sending chunks
	transferState.Status = TransferStatusActive
	go cc.sendPrimaryChunks(transferState)
	go cc.coordinateSecondaryChunks(transferState)
	
	return transferState, nil
}

// analyzeFile analyzes a file and creates metadata
func (cc *ChunkCoordinator) analyzeFile(filename string) (*FileMetadata, error) {
	// Construct full file path
	filePath := filepath.Join(cc.fileDirectory, filename)
	
	// Check if file exists and get info
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("file not found: %w", err)
	}
	
	// Create metadata
	metadata := NewFileMetadata(filename, fileInfo.Size(), cc.chunkSize)
	metadata.CreatedAt = fileInfo.ModTime()
	metadata.ModifiedAt = fileInfo.ModTime()
	
	// Calculate file checksum
	checksum, err := cc.calculateFileChecksum(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate file checksum: %w", err)
	}
	metadata.Checksum = checksum
	
	// Validate metadata
	if err := metadata.Validate(); err != nil {
		return nil, fmt.Errorf("invalid metadata: %w", err)
	}
	
	return metadata, nil
}

// calculateFileChecksum calculates SHA256 checksum of the entire file
func (cc *ChunkCoordinator) calculateFileChecksum(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

// distributeChunks distributes chunks between primary and secondary servers
func (cc *ChunkCoordinator) distributeChunks(transferState *TransferState, strategy DistributionStrategy) error {
	totalChunks := transferState.Metadata.TotalChunks
	
	switch strategy {
	case DistributionStrategyAlternating:
		return cc.distributeAlternating(transferState, totalChunks)
	case DistributionStrategyBandwidth:
		return cc.distributeBandwidthBased(transferState, totalChunks)
	case DistributionStrategyRoundRobin:
		return cc.distributeRoundRobin(transferState, totalChunks)
	case DistributionStrategyPrimaryFirst:
		return cc.distributePrimaryFirst(transferState, totalChunks)
	default:
		return cc.distributeAlternating(transferState, totalChunks)
	}
}

// distributeAlternating distributes chunks alternating between primary and secondary servers
func (cc *ChunkCoordinator) distributeAlternating(transferState *TransferState, totalChunks uint32) error {
	for i := uint32(0); i < totalChunks; i++ {
		assignment := ChunkAssignment{
			ChunkID:     i, // Using sequence number as chunk ID for simplicity
			SequenceNum: i,
			Status:      ChunkStatusPending,
			AssignedAt:  time.Now(),
			RetryCount:  0,
		}
		
		// Alternate assignment: odd chunks to primary, even chunks to secondary
		if i%2 == 0 {
			assignment.AssignedTo = ServerTypePrimary
			transferState.PrimaryChunks = append(transferState.PrimaryChunks, i)
		} else {
			assignment.AssignedTo = ServerTypeSecondary
			transferState.SecondaryChunks = append(transferState.SecondaryChunks, i)
		}
		
		transferState.ChunkAssignments[i] = assignment
	}
	
	return nil
}

// distributeBandwidthBased distributes chunks based on measured bandwidth (placeholder for future implementation)
func (cc *ChunkCoordinator) distributeBandwidthBased(transferState *TransferState, totalChunks uint32) error {
	// For now, fall back to alternating distribution
	// TODO: Implement bandwidth measurement and intelligent distribution
	return cc.distributeAlternating(transferState, totalChunks)
}

// distributeRoundRobin distributes chunks in round-robin fashion
func (cc *ChunkCoordinator) distributeRoundRobin(transferState *TransferState, totalChunks uint32) error {
	// With primary + secondary servers, round-robin is same as alternating
	return cc.distributeAlternating(transferState, totalChunks)
}

// distributePrimaryFirst gives priority to primary server
func (cc *ChunkCoordinator) distributePrimaryFirst(transferState *TransferState, totalChunks uint32) error {
	// Give 70% of chunks to primary, 30% to secondary
	primaryCount := uint32(float64(totalChunks) * 0.7)
	
	for i := uint32(0); i < totalChunks; i++ {
		assignment := ChunkAssignment{
			ChunkID:     i,
			SequenceNum: i,
			Status:      ChunkStatusPending,
			AssignedAt:  time.Now(),
			RetryCount:  0,
		}
		
		if i < primaryCount {
			assignment.AssignedTo = ServerTypePrimary
			transferState.PrimaryChunks = append(transferState.PrimaryChunks, i)
		} else {
			assignment.AssignedTo = ServerTypeSecondary
			transferState.SecondaryChunks = append(transferState.SecondaryChunks, i)
		}
		
		transferState.ChunkAssignments[i] = assignment
	}
	
	return nil
}

// sendPrimaryChunks sends chunks assigned to the primary server
func (cc *ChunkCoordinator) sendPrimaryChunks(transferState *TransferState) {
	for _, sequenceNum := range transferState.PrimaryChunks {
		select {
		case <-cc.ctx.Done():
			return
		default:
			err := cc.sendChunk(transferState, sequenceNum, ServerTypePrimary)
			if err != nil {
				cc.handleChunkError(transferState, sequenceNum, err)
			}
		}
	}
}

// coordinateSecondaryChunks coordinates chunk sending with secondary servers
func (cc *ChunkCoordinator) coordinateSecondaryChunks(transferState *TransferState) {
	for _, sequenceNum := range transferState.SecondaryChunks {
		select {
		case <-cc.ctx.Done():
			return
		default:
			err := cc.sendChunkCommand(transferState, sequenceNum)
			if err != nil {
				cc.handleChunkError(transferState, sequenceNum, err)
			}
		}
	}
}

// sendChunk sends a chunk directly from the primary server
func (cc *ChunkCoordinator) sendChunk(transferState *TransferState, sequenceNum uint32, serverType ServerType) error {
	// Update chunk status
	transferState.mutex.Lock()
	assignment := transferState.ChunkAssignments[sequenceNum]
	assignment.Status = ChunkStatusSending
	transferState.ChunkAssignments[sequenceNum] = assignment
	transferState.mutex.Unlock()
	
	// Read chunk data from file
	chunk, err := cc.readChunkFromFile(transferState.Metadata, sequenceNum)
	if err != nil {
		return fmt.Errorf("failed to read chunk %d: %w", sequenceNum, err)
	}
	
	// Send chunk via stream
	err = cc.sendChunkViaStream(chunk)
	if err != nil {
		return fmt.Errorf("failed to send chunk %d: %w", sequenceNum, err)
	}
	
	// Update chunk status
	transferState.mutex.Lock()
	assignment = transferState.ChunkAssignments[sequenceNum]
	assignment.Status = ChunkStatusSent
	assignment.SentAt = time.Now()
	transferState.ChunkAssignments[sequenceNum] = assignment
	transferState.CompletedChunks[sequenceNum] = true
	transferState.LastActivity = time.Now()
	transferState.mutex.Unlock()
	
	return nil
}

// sendChunkCommand sends a command to secondary server to send a specific chunk
func (cc *ChunkCoordinator) sendChunkCommand(transferState *TransferState, sequenceNum uint32) error {
	// Create chunk command
	metadata := transferState.Metadata
	startOffset := metadata.GetChunkOffset(sequenceNum)
	chunkSize := metadata.GetChunkSize(sequenceNum)
	
	command := NewChunkCommand("SEND_CHUNK", sequenceNum, sequenceNum, metadata.Filename, startOffset, chunkSize)
	command.SenderID = "primary"
	command.ReceiverID = "secondary"
	command.Metadata["client_address"] = transferState.ClientAddress
	command.Metadata["total_chunks"] = fmt.Sprintf("%d", metadata.TotalChunks)
	command.Metadata["file_checksum"] = metadata.Checksum
	
	// Serialize command
	commandData, err := json.Marshal(command)
	if err != nil {
		return fmt.Errorf("failed to serialize chunk command: %w", err)
	}
	
	// Send command to secondary server via raw data
	if len(cc.secondaryPaths) == 0 {
		return fmt.Errorf("no secondary paths available")
	}
	
	// Use first available secondary path
	pathID := cc.secondaryPaths[0]
	err = cc.primarySession.SendRawData(commandData, pathID)
	if err != nil {
		return fmt.Errorf("failed to send chunk command: %w", err)
	}
	
	// Update chunk status
	transferState.mutex.Lock()
	assignment := transferState.ChunkAssignments[sequenceNum]
	assignment.Status = ChunkStatusSending
	transferState.ChunkAssignments[sequenceNum] = assignment
	transferState.mutex.Unlock()
	
	return nil
}

// readChunkFromFile reads a specific chunk from the file
func (cc *ChunkCoordinator) readChunkFromFile(metadata *FileMetadata, sequenceNum uint32) (*FileChunk, error) {
	filePath := filepath.Join(cc.fileDirectory, metadata.Filename)
	
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	
	// Calculate chunk parameters
	offset := metadata.GetChunkOffset(sequenceNum)
	chunkSize := metadata.GetChunkSize(sequenceNum)
	isLast := metadata.IsLastChunk(sequenceNum)
	
	// Seek to chunk position
	_, err = file.Seek(offset, 0)
	if err != nil {
		return nil, err
	}
	
	// Read chunk data
	data := make([]byte, chunkSize)
	n, err := file.Read(data)
	if err != nil && err != io.EOF {
		return nil, err
	}
	
	// Trim data to actual bytes read
	data = data[:n]
	
	// Create chunk
	chunk := NewFileChunk(sequenceNum, sequenceNum, data, metadata.Filename, offset, metadata.TotalChunks, isLast)
	
	return chunk, nil
}

// sendChunkViaStream sends a chunk to the client via a stream
func (cc *ChunkCoordinator) sendChunkViaStream(chunk *FileChunk) error {
	// Open a new stream to send the chunk
	stream, err := cc.primarySession.OpenStreamSync(cc.ctx)
	if err != nil {
		return err
	}
	defer stream.Close()
	
	// Serialize chunk
	chunkData, err := json.Marshal(chunk)
	if err != nil {
		return err
	}
	
	// Send chunk data
	_, err = stream.Write(chunkData)
	return err
}

// handleChunkError handles errors during chunk sending
func (cc *ChunkCoordinator) handleChunkError(transferState *TransferState, sequenceNum uint32, err error) {
	transferState.mutex.Lock()
	defer transferState.mutex.Unlock()
	
	// Update chunk status
	assignment := transferState.ChunkAssignments[sequenceNum]
	assignment.Status = ChunkStatusFailed
	assignment.LastError = err.Error()
	assignment.RetryCount++
	transferState.ChunkAssignments[sequenceNum] = assignment
	
	// Track failed chunk
	transferState.FailedChunks[sequenceNum] = assignment.RetryCount
	transferState.LastActivity = time.Now()
}

// monitorTransfers monitors active transfers and handles cleanup
func (cc *ChunkCoordinator) monitorTransfers() {
	ticker := time.NewTicker(30 * time.Second) // Check every 30 seconds
	defer ticker.Stop()
	
	for {
		select {
		case <-cc.ctx.Done():
			return
		case <-ticker.C:
			cc.checkTransferStatus()
		}
	}
}

// checkTransferStatus checks the status of all active transfers
func (cc *ChunkCoordinator) checkTransferStatus() {
	cc.transfersMutex.Lock()
	defer cc.transfersMutex.Unlock()
	
	for filename, transferState := range cc.activeTransfers {
		transferState.mutex.RLock()
		
		// Check if transfer is complete
		if len(transferState.CompletedChunks) == int(transferState.Metadata.TotalChunks) {
			transferState.mutex.RUnlock()
			transferState.mutex.Lock()
			transferState.Status = TransferStatusCompleted
			transferState.mutex.Unlock()
			
			// Clean up completed transfer after a delay
			go func(fname string) {
				time.Sleep(5 * time.Minute)
				cc.CleanupTransfer(fname)
			}(filename)
			continue
		}
		
		// Check for stalled transfers
		if time.Since(transferState.LastActivity) > 5*time.Minute {
			transferState.mutex.RUnlock()
			transferState.mutex.Lock()
			transferState.Status = TransferStatusFailed
			transferState.mutex.Unlock()
			continue
		}
		
		transferState.mutex.RUnlock()
	}
}

// GetTransferState returns the current state of a transfer
func (cc *ChunkCoordinator) GetTransferState(filename string) (*TransferState, error) {
	cc.transfersMutex.RLock()
	defer cc.transfersMutex.RUnlock()
	
	transferState, exists := cc.activeTransfers[filename]
	if !exists {
		return nil, fmt.Errorf("no active transfer found for file: %s", filename)
	}
	
	return transferState, nil
}

// CleanupTransfer removes a transfer from active transfers
func (cc *ChunkCoordinator) CleanupTransfer(filename string) error {
	cc.transfersMutex.Lock()
	defer cc.transfersMutex.Unlock()
	
	delete(cc.activeTransfers, filename)
	return nil
}

// GetActiveTransfers returns a list of currently active transfers
func (cc *ChunkCoordinator) GetActiveTransfers() []string {
	cc.transfersMutex.RLock()
	defer cc.transfersMutex.RUnlock()
	
	filenames := make([]string, 0, len(cc.activeTransfers))
	for filename := range cc.activeTransfers {
		filenames = append(filenames, filename)
	}
	
	return filenames
}

// Close closes the chunk coordinator and cleans up resources
func (cc *ChunkCoordinator) Close() error {
	cc.cancel()
	
	// Clean up all active transfers
	cc.transfersMutex.Lock()
	cc.activeTransfers = make(map[string]*TransferState)
	cc.transfersMutex.Unlock()
	
	return nil
}

// String methods for enums
func (st ServerType) String() string {
	switch st {
	case ServerTypePrimary:
		return "PRIMARY"
	case ServerTypeSecondary:
		return "SECONDARY"
	default:
		return "UNKNOWN"
	}
}

func (cs ChunkStatus) String() string {
	switch cs {
	case ChunkStatusPending:
		return "PENDING"
	case ChunkStatusSending:
		return "SENDING"
	case ChunkStatusSent:
		return "SENT"
	case ChunkStatusFailed:
		return "FAILED"
	case ChunkStatusRetrying:
		return "RETRYING"
	default:
		return "UNKNOWN"
	}
}

func (ts TransferStatus) String() string {
	switch ts {
	case TransferStatusPreparing:
		return "PREPARING"
	case TransferStatusActive:
		return "ACTIVE"
	case TransferStatusCompleted:
		return "COMPLETED"
	case TransferStatusFailed:
		return "FAILED"
	case TransferStatusCancelled:
		return "CANCELLED"
	default:
		return "UNKNOWN"
	}
}

func (ds DistributionStrategy) String() string {
	switch ds {
	case DistributionStrategyAlternating:
		return "ALTERNATING"
	case DistributionStrategyBandwidth:
		return "BANDWIDTH"
	case DistributionStrategyRoundRobin:
		return "ROUND_ROBIN"
	case DistributionStrategyPrimaryFirst:
		return "PRIMARY_FIRST"
	default:
		return "UNKNOWN"
	}
}

// handleSecondaryResponses handles responses from secondary servers
func (cc *ChunkCoordinator) handleSecondaryResponses() {
	for {
		select {
		case <-cc.ctx.Done():
			return
		default:
			// Accept incoming streams from secondary servers
			stream, err := cc.primarySession.AcceptStream(cc.ctx)
			if err != nil {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			
			// Handle secondary response in goroutine
			go cc.processSecondaryResponse(stream)
		}
	}
}

// processSecondaryResponse processes a response from a secondary server
func (cc *ChunkCoordinator) processSecondaryResponse(stream Stream) {
	defer stream.Close()
	
	buffer := make([]byte, 4096)
	n, err := stream.Read(buffer)
	if err != nil {
		return
	}
	
	// Parse the response as a ChunkCommand
	var response ChunkCommand
	if err := json.Unmarshal(buffer[:n], &response); err != nil {
		return
	}
	
	// Validate the response
	if err := response.Validate(); err != nil {
		return
	}
	
	// Process based on command type
	switch response.Command {
	case "CHUNK_SENT":
		cc.handleChunkSentResponse(&response)
	case "CHUNK_ERROR":
		cc.handleChunkErrorResponse(&response)
	default:
		// Unknown command type, ignore
	}
}

// handleChunkSentResponse handles successful chunk sending notification from secondary server
func (cc *ChunkCoordinator) handleChunkSentResponse(response *ChunkCommand) {
	cc.transfersMutex.RLock()
	transferState, exists := cc.activeTransfers[response.Filename]
	cc.transfersMutex.RUnlock()
	
	if !exists {
		return // Transfer no longer active
	}
	
	transferState.mutex.Lock()
	defer transferState.mutex.Unlock()
	
	// Update chunk assignment status
	assignment, exists := transferState.ChunkAssignments[response.SequenceNum]
	if !exists {
		return // Chunk assignment not found
	}
	
	// Verify this chunk was assigned to secondary server
	if assignment.AssignedTo != ServerTypeSecondary {
		return // Chunk was not assigned to secondary server
	}
	
	// Update chunk status to sent
	assignment.Status = ChunkStatusSent
	assignment.SentAt = time.Now()
	if response.Checksum != "" {
		assignment.LastError = "" // Clear any previous error
	}
	transferState.ChunkAssignments[response.SequenceNum] = assignment
	
	// Mark chunk as completed
	transferState.CompletedChunks[response.SequenceNum] = true
	transferState.LastActivity = time.Now()
	
	// Remove from failed chunks if it was there
	delete(transferState.FailedChunks, response.SequenceNum)
}

// handleChunkErrorResponse handles error notification from secondary server
func (cc *ChunkCoordinator) handleChunkErrorResponse(response *ChunkCommand) {
	cc.transfersMutex.RLock()
	transferState, exists := cc.activeTransfers[response.Filename]
	cc.transfersMutex.RUnlock()
	
	if !exists {
		return // Transfer no longer active
	}
	
	transferState.mutex.Lock()
	defer transferState.mutex.Unlock()
	
	// Update chunk assignment status
	assignment, exists := transferState.ChunkAssignments[response.SequenceNum]
	if !exists {
		return // Chunk assignment not found
	}
	
	// Verify this chunk was assigned to secondary server
	if assignment.AssignedTo != ServerTypeSecondary {
		return // Chunk was not assigned to secondary server
	}
	
	// Update chunk status to failed
	assignment.Status = ChunkStatusFailed
	assignment.RetryCount++
	if errorMsg, exists := response.Metadata["error"]; exists {
		assignment.LastError = errorMsg
	} else {
		assignment.LastError = "Secondary server reported chunk error"
	}
	transferState.ChunkAssignments[response.SequenceNum] = assignment
	
	// Track failed chunk
	transferState.FailedChunks[response.SequenceNum] = assignment.RetryCount
	transferState.LastActivity = time.Now()
	
	// Attempt retry if under retry limit
	maxRetries := 3 // Default max retries
	if assignment.RetryCount < maxRetries {
		go cc.retrySecondaryChunk(transferState, response.SequenceNum)
	}
}

// retrySecondaryChunk retries sending a chunk command to secondary server
func (cc *ChunkCoordinator) retrySecondaryChunk(transferState *TransferState, sequenceNum uint32) {
	// Wait a bit before retrying
	time.Sleep(time.Duration(transferState.ChunkAssignments[sequenceNum].RetryCount) * time.Second)
	
	// Check if context is still valid
	select {
	case <-cc.ctx.Done():
		return
	default:
	}
	
	// Update chunk status to retrying
	transferState.mutex.Lock()
	assignment := transferState.ChunkAssignments[sequenceNum]
	assignment.Status = ChunkStatusRetrying
	transferState.ChunkAssignments[sequenceNum] = assignment
	transferState.mutex.Unlock()
	
	// Retry sending the chunk command
	err := cc.sendChunkCommand(transferState, sequenceNum)
	if err != nil {
		cc.handleChunkError(transferState, sequenceNum, err)
	}
}

// GetSecondaryServerStatus returns status information about secondary server coordination
func (cc *ChunkCoordinator) GetSecondaryServerStatus() map[string]interface{} {
	status := make(map[string]interface{})
	
	status["secondary_paths"] = cc.secondaryPaths
	status["secondary_paths_count"] = len(cc.secondaryPaths)
	
	// Get active paths from session
	activePaths := cc.primarySession.GetActivePaths()
	secondaryActivePaths := make([]string, 0)
	
	for _, path := range activePaths {
		if !path.IsPrimary {
			secondaryActivePaths = append(secondaryActivePaths, path.PathID)
		}
	}
	
	status["active_secondary_paths"] = secondaryActivePaths
	status["active_secondary_paths_count"] = len(secondaryActivePaths)
	
	return status
}

// ReassignFailedChunks reassigns failed chunks from secondary to primary server
func (cc *ChunkCoordinator) ReassignFailedChunks(filename string, maxRetries int) error {
	cc.transfersMutex.RLock()
	transferState, exists := cc.activeTransfers[filename]
	cc.transfersMutex.RUnlock()
	
	if !exists {
		return fmt.Errorf("no active transfer found for file: %s", filename)
	}
	
	transferState.mutex.Lock()
	defer transferState.mutex.Unlock()
	
	reassignedCount := 0
	
	// Find chunks that have failed too many times on secondary server
	for sequenceNum, assignment := range transferState.ChunkAssignments {
		if assignment.AssignedTo == ServerTypeSecondary && 
		   assignment.Status == ChunkStatusFailed && 
		   assignment.RetryCount >= maxRetries {
			
			// Reassign to primary server
			assignment.AssignedTo = ServerTypePrimary
			assignment.Status = ChunkStatusPending
			assignment.RetryCount = 0
			assignment.LastError = ""
			transferState.ChunkAssignments[sequenceNum] = assignment
			
			// Move from secondary to primary chunks list
			transferState.PrimaryChunks = append(transferState.PrimaryChunks, sequenceNum)
			
			// Remove from secondary chunks list
			for i, chunkSeq := range transferState.SecondaryChunks {
				if chunkSeq == sequenceNum {
					transferState.SecondaryChunks = append(transferState.SecondaryChunks[:i], transferState.SecondaryChunks[i+1:]...)
					break
				}
			}
			
			// Remove from failed chunks
			delete(transferState.FailedChunks, sequenceNum)
			
			reassignedCount++
			
			// Send chunk via primary server
			go func(seqNum uint32) {
				err := cc.sendChunk(transferState, seqNum, ServerTypePrimary)
				if err != nil {
					cc.handleChunkError(transferState, seqNum, err)
				}
			}(sequenceNum)
		}
	}
	
	if reassignedCount > 0 {
		transferState.LastActivity = time.Now()
	}
	
	return nil
}