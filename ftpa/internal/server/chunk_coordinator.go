package server

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
	"ftpa/internal/types"
	"kwik/pkg/session"
)

// ChunkCoordinator manages the distribution of file chunks between primary and secondary servers
type ChunkCoordinator struct {
	primarySession   session.Session            // Primary server session
	secondaryPaths   []string                   // Secondary server path IDs
	chunkSize        int32                      // Size of each chunk in bytes
	activeTransfers  map[string]*TransferState  // Active transfers by filename
	transfersMutex   sync.RWMutex               // Protects activeTransfers map
	fileDirectory    string                     // Directory containing files to serve
	maxConcurrent    int                        // Maximum concurrent transfers
	ctx              context.Context            // Context for cancellation
	cancel           context.CancelFunc         // Cancel function
	
	// Dynamic optimization fields
	pathMetrics      map[string]*PathMetrics    // Performance metrics per path
	metricsMutex     sync.RWMutex               // Protects pathMetrics map
	optimizationEnabled bool                    // Whether dynamic optimization is enabled
	rebalanceInterval   time.Duration           // How often to rebalance chunk distribution
}

// TransferState tracks the state of an active file transfer
type TransferState struct {
	Filename         string                    // Name of the file being transferred
	Metadata         *types.FileMetadata             // File metadata
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

// PathMetrics tracks performance metrics for a specific path
type PathMetrics struct {
	PathID           string        // Path identifier
	ServerType       ServerType    // Which server this path belongs to
	TotalChunksSent  uint64        // Total number of chunks sent via this path
	TotalBytesSent   uint64        // Total bytes sent via this path
	TotalSendTime    time.Duration // Total time spent sending chunks
	AverageBandwidth float64       // Average bandwidth in bytes/second
	LastBandwidth    float64       // Most recent bandwidth measurement
	SuccessRate      float64       // Success rate (0.0 to 1.0)
	AverageLatency   time.Duration // Average latency for chunk sending
	LastActivity     time.Time     // Last time this path was used
	FailureCount     uint64        // Number of failed chunk sends
	mutex            sync.RWMutex  // Protects this metrics data
}

// BandwidthSample represents a single bandwidth measurement
type BandwidthSample struct {
	Timestamp time.Time
	Bandwidth float64 // bytes/second
	ChunkSize int32
	Duration  time.Duration
}

// NewChunkCoordinator creates a new chunk coordinator
func NewChunkCoordinator(primarySession session.Session, secondaryPaths []string, fileDirectory string, chunkSize int32, maxConcurrent int) (*ChunkCoordinator, error) {
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
		primarySession:      primarySession,
		secondaryPaths:      secondaryPaths,
		chunkSize:           chunkSize,
		activeTransfers:     make(map[string]*TransferState),
		fileDirectory:       fileDirectory,
		maxConcurrent:       maxConcurrent,
		ctx:                 ctx,
		cancel:              cancel,
		pathMetrics:         make(map[string]*PathMetrics),
		optimizationEnabled: true,
		rebalanceInterval:   30 * time.Second, // Rebalance every 30 seconds
	}
	
	// Initialize path metrics for all available paths
	coordinator.initializePathMetrics()
	
	// Start background monitoring
	go coordinator.monitorTransfers()
	go coordinator.handleSecondaryResponses()
	go coordinator.monitorPathPerformance()
	
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
	
	fmt.Printf("DEBUG: Starting chunk distribution for %s:\n", filename)
	fmt.Printf("  📦 Total chunks: %d\n", transferState.Metadata.TotalChunks)
	fmt.Printf("  🏢 Primary chunks: %v\n", transferState.PrimaryChunks)
	fmt.Printf("  🏢 Secondary chunks: %v\n", transferState.SecondaryChunks)
	
	go cc.sendPrimaryChunks(transferState)
	go cc.coordinateSecondaryChunks(transferState)
	
	return transferState, nil
}

// analyzeFile analyzes a file and creates metadata
func (cc *ChunkCoordinator) analyzeFile(filename string) (*types.FileMetadata, error) {
	// Construct full file path
	filePath := filepath.Join(cc.fileDirectory, filename)
	
	// Check if file exists and get info
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("file not found: %w", err)
	}
	
	// Create metadata
	metadata := types.NewFileMetadata(filename, fileInfo.Size(), cc.chunkSize)
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

// distributeBandwidthBased distributes chunks based on measured bandwidth
func (cc *ChunkCoordinator) distributeBandwidthBased(transferState *TransferState, totalChunks uint32) error {
	if !cc.optimizationEnabled {
		return cc.distributeAlternating(transferState, totalChunks)
	}
	
	// Get current bandwidth metrics for primary and secondary paths
	primaryBandwidth := cc.getPrimaryPathBandwidth()
	secondaryBandwidth := cc.getSecondaryPathBandwidth()
	
	// If we don't have enough metrics yet, fall back to alternating
	if primaryBandwidth <= 0 && secondaryBandwidth <= 0 {
		return cc.distributeAlternating(transferState, totalChunks)
	}
	
	// Calculate total bandwidth and distribution ratio
	totalBandwidth := primaryBandwidth + secondaryBandwidth
	if totalBandwidth <= 0 {
		return cc.distributeAlternating(transferState, totalChunks)
	}
	
	// Calculate how many chunks each server should get based on bandwidth
	primaryRatio := primaryBandwidth / totalBandwidth
	
	primaryChunkCount := uint32(float64(totalChunks) * primaryRatio)
	secondaryChunkCount := totalChunks - primaryChunkCount
	
	// Ensure at least one chunk per server if both are available
	if primaryChunkCount == 0 && primaryBandwidth > 0 {
		primaryChunkCount = 1
		secondaryChunkCount = totalChunks - 1
	}
	if secondaryChunkCount == 0 && secondaryBandwidth > 0 {
		secondaryChunkCount = 1
		primaryChunkCount = totalChunks - 1
	}
	
	// Distribute chunks based on calculated counts
	primaryAssigned := uint32(0)
	secondaryAssigned := uint32(0)
	
	for i := uint32(0); i < totalChunks; i++ {
		assignment := ChunkAssignment{
			ChunkID:     i,
			SequenceNum: i,
			Status:      ChunkStatusPending,
			AssignedAt:  time.Now(),
			RetryCount:  0,
		}
		
		// Assign to primary if we haven't reached the limit, or if secondary is full
		if primaryAssigned < primaryChunkCount && (secondaryAssigned >= secondaryChunkCount || primaryBandwidth > secondaryBandwidth) {
			assignment.AssignedTo = ServerTypePrimary
			transferState.PrimaryChunks = append(transferState.PrimaryChunks, i)
			primaryAssigned++
		} else {
			assignment.AssignedTo = ServerTypeSecondary
			transferState.SecondaryChunks = append(transferState.SecondaryChunks, i)
			secondaryAssigned++
		}
		
		transferState.ChunkAssignments[i] = assignment
	}
	
	return nil
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
	startTime := time.Now()
	
	// Update chunk status
	transferState.mutex.Lock()
	assignment := transferState.ChunkAssignments[sequenceNum]
	assignment.Status = ChunkStatusSending
	transferState.ChunkAssignments[sequenceNum] = assignment
	transferState.mutex.Unlock()
	
	// Read chunk data from file
	chunk, err := cc.readChunkFromFile(transferState.Metadata, sequenceNum)
	if err != nil {
		// Update metrics for failure
		cc.updatePrimaryPathMetrics(0, time.Since(startTime), false)
		return fmt.Errorf("failed to read chunk %d: %w", sequenceNum, err)
	}
	
	// Send chunk via stream
	err = cc.sendChunkViaStream(chunk)
	sendDuration := time.Since(startTime)
	
	if err != nil {
		// Update metrics for failure
		cc.updatePrimaryPathMetrics(int32(len(chunk.Data)), sendDuration, false)
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
	
	// Update metrics for success
	cc.updatePrimaryPathMetrics(int32(len(chunk.Data)), sendDuration, true)
	
	return nil
}

// sendChunkCommand sends a command to secondary server to send a specific chunk
func (cc *ChunkCoordinator) sendChunkCommand(transferState *TransferState, sequenceNum uint32) error {
	// Create chunk command
	metadata := transferState.Metadata
	startOffset := metadata.GetChunkOffset(sequenceNum)
	chunkSize := metadata.GetChunkSize(sequenceNum)
	
	command := types.NewChunkCommand("SEND_CHUNK", sequenceNum, sequenceNum, metadata.Filename, startOffset, chunkSize)
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
func (cc *ChunkCoordinator) readChunkFromFile(metadata *types.FileMetadata, sequenceNum uint32) (*types.FileChunk, error) {
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
	chunk := types.NewFileChunk(sequenceNum, sequenceNum, data, metadata.Filename, offset, metadata.TotalChunks, isLast)
	
	return chunk, nil
}

// sendChunkViaStream sends a chunk to the client via a stream
func (cc *ChunkCoordinator) sendChunkViaStream(chunk *types.FileChunk) error {
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
func (cc *ChunkCoordinator) processSecondaryResponse(stream session.Stream) {
	defer stream.Close()
	
	buffer := make([]byte, 4096)
	n, err := stream.Read(buffer)
	if err != nil {
		return
	}
	
	// Parse the response as a ChunkCommand
	var response types.ChunkCommand
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
func (cc *ChunkCoordinator) handleChunkSentResponse(response *types.ChunkCommand) {
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
	
	// Calculate send duration for metrics
	sendDuration := time.Since(assignment.AssignedAt)
	chunkSize := int32(0)
	if response.Size > 0 {
		chunkSize = response.Size
	}
	
	// Update secondary path metrics
	cc.updateSecondaryPathMetrics(chunkSize, sendDuration, true)
	
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
func (cc *ChunkCoordinator) handleChunkErrorResponse(response *types.ChunkCommand) {
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
// initializePathMetrics initializes metrics for all available paths
func (cc *ChunkCoordinator) initializePathMetrics() {
	cc.metricsMutex.Lock()
	defer cc.metricsMutex.Unlock()
	
	// Initialize metrics for primary path
	primaryPaths := cc.primarySession.GetActivePaths()
	for _, path := range primaryPaths {
		if path.IsPrimary {
			cc.pathMetrics[path.PathID] = &PathMetrics{
				PathID:           path.PathID,
				ServerType:       ServerTypePrimary,
				TotalChunksSent:  0,
				TotalBytesSent:   0,
				TotalSendTime:    0,
				AverageBandwidth: 0,
				LastBandwidth:    0,
				SuccessRate:      1.0, // Start with optimistic success rate
				AverageLatency:   0,
				LastActivity:     time.Now(),
				FailureCount:     0,
			}
		}
	}
	
	// Initialize metrics for secondary paths
	for _, pathID := range cc.secondaryPaths {
		cc.pathMetrics[pathID] = &PathMetrics{
			PathID:           pathID,
			ServerType:       ServerTypeSecondary,
			TotalChunksSent:  0,
			TotalBytesSent:   0,
			TotalSendTime:    0,
			AverageBandwidth: 0,
			LastBandwidth:    0,
			SuccessRate:      1.0, // Start with optimistic success rate
			AverageLatency:   0,
			LastActivity:     time.Now(),
			FailureCount:     0,
		}
	}
}

// getPrimaryPathBandwidth returns the average bandwidth for primary paths
func (cc *ChunkCoordinator) getPrimaryPathBandwidth() float64 {
	cc.metricsMutex.RLock()
	defer cc.metricsMutex.RUnlock()
	
	totalBandwidth := 0.0
	pathCount := 0
	
	for _, metrics := range cc.pathMetrics {
		if metrics.ServerType == ServerTypePrimary && metrics.AverageBandwidth > 0 {
			totalBandwidth += metrics.AverageBandwidth
			pathCount++
		}
	}
	
	if pathCount == 0 {
		return 0
	}
	
	return totalBandwidth / float64(pathCount)
}

// getSecondaryPathBandwidth returns the average bandwidth for secondary paths
func (cc *ChunkCoordinator) getSecondaryPathBandwidth() float64 {
	cc.metricsMutex.RLock()
	defer cc.metricsMutex.RUnlock()
	
	totalBandwidth := 0.0
	pathCount := 0
	
	for _, metrics := range cc.pathMetrics {
		if metrics.ServerType == ServerTypeSecondary && metrics.AverageBandwidth > 0 {
			totalBandwidth += metrics.AverageBandwidth
			pathCount++
		}
	}
	
	if pathCount == 0 {
		return 0
	}
	
	return totalBandwidth / float64(pathCount)
}

// updatePrimaryPathMetrics updates performance metrics for primary paths
func (cc *ChunkCoordinator) updatePrimaryPathMetrics(chunkSize int32, sendDuration time.Duration, success bool) {
	// Find primary path ID from active paths
	activePaths := cc.primarySession.GetActivePaths()
	for _, path := range activePaths {
		if path.IsPrimary {
			cc.updatePathMetrics(path.PathID, chunkSize, sendDuration, success)
			break
		}
	}
}

// updatePathMetrics updates performance metrics for a specific path
func (cc *ChunkCoordinator) updatePathMetrics(pathID string, chunkSize int32, sendDuration time.Duration, success bool) {
	cc.metricsMutex.Lock()
	defer cc.metricsMutex.Unlock()
	
	metrics, exists := cc.pathMetrics[pathID]
	if !exists {
		return // Path not found
	}
	
	metrics.mutex.Lock()
	defer metrics.mutex.Unlock()
	
	// Update basic counters
	if success {
		metrics.TotalChunksSent++
		metrics.TotalBytesSent += uint64(chunkSize)
		metrics.TotalSendTime += sendDuration
		metrics.LastActivity = time.Now()
		
		// Calculate bandwidth for this chunk
		if sendDuration > 0 {
			chunkBandwidth := float64(chunkSize) / sendDuration.Seconds()
			metrics.LastBandwidth = chunkBandwidth
			
			// Update average bandwidth using exponential moving average
			alpha := 0.3 // Smoothing factor
			if metrics.AverageBandwidth == 0 {
				metrics.AverageBandwidth = chunkBandwidth
			} else {
				metrics.AverageBandwidth = alpha*chunkBandwidth + (1-alpha)*metrics.AverageBandwidth
			}
		}
		
		// Update average latency
		if metrics.TotalChunksSent > 0 {
			metrics.AverageLatency = metrics.TotalSendTime / time.Duration(metrics.TotalChunksSent)
		}
	} else {
		metrics.FailureCount++
	}
	
	// Update success rate
	totalAttempts := metrics.TotalChunksSent + metrics.FailureCount
	if totalAttempts > 0 {
		metrics.SuccessRate = float64(metrics.TotalChunksSent) / float64(totalAttempts)
	}
}

// monitorPathPerformance monitors path performance and triggers rebalancing
func (cc *ChunkCoordinator) monitorPathPerformance() {
	ticker := time.NewTicker(cc.rebalanceInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-cc.ctx.Done():
			return
		case <-ticker.C:
			if cc.optimizationEnabled {
				cc.rebalanceActiveTransfers()
			}
		}
	}
}

// rebalanceActiveTransfers rebalances chunk distribution for active transfers
func (cc *ChunkCoordinator) rebalanceActiveTransfers() {
	cc.transfersMutex.RLock()
	activeTransfers := make([]*TransferState, 0, len(cc.activeTransfers))
	for _, transfer := range cc.activeTransfers {
		if transfer.Status == TransferStatusActive {
			activeTransfers = append(activeTransfers, transfer)
		}
	}
	cc.transfersMutex.RUnlock()
	
	// Check each active transfer for rebalancing opportunities
	for _, transfer := range activeTransfers {
		cc.rebalanceTransfer(transfer)
	}
}

// rebalanceTransfer rebalances chunk distribution for a specific transfer
func (cc *ChunkCoordinator) rebalanceTransfer(transferState *TransferState) {
	transferState.mutex.Lock()
	defer transferState.mutex.Unlock()
	
	// Get current bandwidth metrics
	primaryBandwidth := cc.getPrimaryPathBandwidth()
	secondaryBandwidth := cc.getSecondaryPathBandwidth()
	
	if primaryBandwidth <= 0 && secondaryBandwidth <= 0 {
		return // No metrics available
	}
	
	// Calculate current distribution
	primaryPending := 0
	secondaryPending := 0
	
	for _, assignment := range transferState.ChunkAssignments {
		if assignment.Status == ChunkStatusPending || assignment.Status == ChunkStatusRetrying {
			if assignment.AssignedTo == ServerTypePrimary {
				primaryPending++
			} else {
				secondaryPending++
			}
		}
	}
	
	totalPending := primaryPending + secondaryPending
	if totalPending == 0 {
		return // No pending chunks to rebalance
	}
	
	// Calculate optimal distribution based on current bandwidth
	totalBandwidth := primaryBandwidth + secondaryBandwidth
	if totalBandwidth <= 0 {
		return
	}
	
	optimalPrimaryRatio := primaryBandwidth / totalBandwidth
	optimalPrimaryCount := int(float64(totalPending) * optimalPrimaryRatio)
	
	// Check if rebalancing is needed (threshold of 20% difference)
	currentPrimaryRatio := float64(primaryPending) / float64(totalPending)
	if absFloat(currentPrimaryRatio-optimalPrimaryRatio) < 0.2 {
		return // Current distribution is close enough to optimal
	}
	
	// Perform rebalancing by reassigning pending chunks
	chunksToReassign := absInt(primaryPending - optimalPrimaryCount)
	if chunksToReassign == 0 {
		return
	}
	
	reassignedCount := 0
	targetServer := ServerTypePrimary
	sourceServer := ServerTypeSecondary
	
	if primaryPending > optimalPrimaryCount {
		// Too many chunks on primary, move some to secondary
		targetServer = ServerTypeSecondary
		sourceServer = ServerTypePrimary
	}
	
	// Find chunks to reassign
	for chunkID, assignment := range transferState.ChunkAssignments {
		if reassignedCount >= chunksToReassign {
			break
		}
		
		if assignment.AssignedTo == sourceServer && 
		   (assignment.Status == ChunkStatusPending || assignment.Status == ChunkStatusRetrying) {
			
			// Reassign this chunk
			assignment.AssignedTo = targetServer
			assignment.Status = ChunkStatusPending
			assignment.RetryCount = 0
			assignment.LastError = ""
			transferState.ChunkAssignments[chunkID] = assignment
			
			// Update chunk lists
			if targetServer == ServerTypePrimary {
				// Move from secondary to primary
				transferState.PrimaryChunks = append(transferState.PrimaryChunks, chunkID)
				cc.removeFromSlice(&transferState.SecondaryChunks, chunkID)
			} else {
				// Move from primary to secondary
				transferState.SecondaryChunks = append(transferState.SecondaryChunks, chunkID)
				cc.removeFromSlice(&transferState.PrimaryChunks, chunkID)
			}
			
			reassignedCount++
		}
	}
	
	// Trigger sending of reassigned chunks
	if reassignedCount > 0 {
		transferState.LastActivity = time.Now()
		
		// Start sending reassigned chunks
		if targetServer == ServerTypePrimary {
			go cc.sendReassignedPrimaryChunks(transferState, reassignedCount)
		} else {
			go cc.sendReassignedSecondaryChunks(transferState, reassignedCount)
		}
	}
}

// sendReassignedPrimaryChunks sends chunks that were reassigned to primary server
func (cc *ChunkCoordinator) sendReassignedPrimaryChunks(transferState *TransferState, maxChunks int) {
	sent := 0
	for _, sequenceNum := range transferState.PrimaryChunks {
		if sent >= maxChunks {
			break
		}
		
		transferState.mutex.RLock()
		assignment := transferState.ChunkAssignments[sequenceNum]
		transferState.mutex.RUnlock()
		
		if assignment.Status == ChunkStatusPending && assignment.AssignedTo == ServerTypePrimary {
			select {
			case <-cc.ctx.Done():
				return
			default:
				err := cc.sendChunk(transferState, sequenceNum, ServerTypePrimary)
				if err != nil {
					cc.handleChunkError(transferState, sequenceNum, err)
				}
				sent++
			}
		}
	}
}

// sendReassignedSecondaryChunks sends chunks that were reassigned to secondary server
func (cc *ChunkCoordinator) sendReassignedSecondaryChunks(transferState *TransferState, maxChunks int) {
	sent := 0
	for _, sequenceNum := range transferState.SecondaryChunks {
		if sent >= maxChunks {
			break
		}
		
		transferState.mutex.RLock()
		assignment := transferState.ChunkAssignments[sequenceNum]
		transferState.mutex.RUnlock()
		
		if assignment.Status == ChunkStatusPending && assignment.AssignedTo == ServerTypeSecondary {
			select {
			case <-cc.ctx.Done():
				return
			default:
				err := cc.sendChunkCommand(transferState, sequenceNum)
				if err != nil {
					cc.handleChunkError(transferState, sequenceNum, err)
				}
				sent++
			}
		}
	}
}

// removeFromSlice removes a value from a uint32 slice
func (cc *ChunkCoordinator) removeFromSlice(slice *[]uint32, value uint32) {
	for i, v := range *slice {
		if v == value {
			*slice = append((*slice)[:i], (*slice)[i+1:]...)
			break
		}
	}
}

// abs returns the absolute value of an integer
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// GetPathMetrics returns current performance metrics for all paths
func (cc *ChunkCoordinator) GetPathMetrics() map[string]*PathMetrics {
	cc.metricsMutex.RLock()
	defer cc.metricsMutex.RUnlock()
	
	// Create a copy of metrics to avoid race conditions
	result := make(map[string]*PathMetrics)
	for pathID, metrics := range cc.pathMetrics {
		metrics.mutex.RLock()
		result[pathID] = &PathMetrics{
			PathID:           metrics.PathID,
			ServerType:       metrics.ServerType,
			TotalChunksSent:  metrics.TotalChunksSent,
			TotalBytesSent:   metrics.TotalBytesSent,
			TotalSendTime:    metrics.TotalSendTime,
			AverageBandwidth: metrics.AverageBandwidth,
			LastBandwidth:    metrics.LastBandwidth,
			SuccessRate:      metrics.SuccessRate,
			AverageLatency:   metrics.AverageLatency,
			LastActivity:     metrics.LastActivity,
			FailureCount:     metrics.FailureCount,
		}
		metrics.mutex.RUnlock()
	}
	
	return result
}

// SetOptimizationEnabled enables or disables dynamic optimization
func (cc *ChunkCoordinator) SetOptimizationEnabled(enabled bool) {
	cc.optimizationEnabled = enabled
}

// IsOptimizationEnabled returns whether dynamic optimization is enabled
func (cc *ChunkCoordinator) IsOptimizationEnabled() bool {
	return cc.optimizationEnabled
}

// SetRebalanceInterval sets the interval for rebalancing chunk distribution
func (cc *ChunkCoordinator) SetRebalanceInterval(interval time.Duration) {
	cc.rebalanceInterval = interval
}

// GetRebalanceInterval returns the current rebalance interval
func (cc *ChunkCoordinator) GetRebalanceInterval() time.Duration {
	return cc.rebalanceInterval
}

// GetOptimizationStatus returns detailed status of the optimization system
func (cc *ChunkCoordinator) GetOptimizationStatus() map[string]any {
	status := make(map[string]any)
	
	status["optimization_enabled"] = cc.optimizationEnabled
	status["rebalance_interval"] = cc.rebalanceInterval.String()
	
	// Get path metrics summary
	pathMetrics := cc.GetPathMetrics()
	pathSummary := make(map[string]any)
	
	for pathID, metrics := range pathMetrics {
		pathSummary[pathID] = map[string]any{
			"server_type":        metrics.ServerType.String(),
			"total_chunks_sent":  metrics.TotalChunksSent,
			"total_bytes_sent":   metrics.TotalBytesSent,
			"average_bandwidth":  metrics.AverageBandwidth,
			"last_bandwidth":     metrics.LastBandwidth,
			"success_rate":       metrics.SuccessRate,
			"average_latency":    metrics.AverageLatency.String(),
			"last_activity":      metrics.LastActivity.Format(time.RFC3339),
			"failure_count":      metrics.FailureCount,
		}
	}
	
	status["path_metrics"] = pathSummary
	status["primary_bandwidth"] = cc.getPrimaryPathBandwidth()
	status["secondary_bandwidth"] = cc.getSecondaryPathBandwidth()
	
	return status
}
// updateSecondaryPathMetrics updates performance metrics for secondary paths
func (cc *ChunkCoordinator) updateSecondaryPathMetrics(chunkSize int32, sendDuration time.Duration, success bool) {
	// Update metrics for all secondary paths (since we don't know which specific path was used)
	for _, pathID := range cc.secondaryPaths {
		cc.updatePathMetrics(pathID, chunkSize, sendDuration, success)
	}
}

// absFloat returns the absolute value of a float64
func absFloat(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// absInt returns the absolute value of an integer
func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}