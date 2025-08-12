package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"ftpa/internal/types"
	"os"
	"path/filepath"
	"sync"
	"time"

	kwik "kwik/pkg"
	"kwik/pkg/session"
)

// FileTransferClient provides the interface for downloading files using multi-path transfer
type FileTransferClient interface {
	DownloadFile(filename string, progressCallback func(progress float64)) error
	GetDownloadProgress(filename string) (*TransferProgress, error)
	CancelDownload(filename string) error
}

// KwikFileTransferClient implements FileTransferClient using KWIK sessions
type KwikFileTransferClient struct {
	config          *ClientConfiguration      // Client configuration
	session         session.Session           // KWIK session interface
	chunkManager    *ChunkManager             // Manages chunk reception and reconstruction
	activeDownloads map[string]*DownloadState // Active downloads by filename
	downloadsMutex  sync.RWMutex              // Protects activeDownloads map
	ctx             context.Context           // Context for cancellation
	cancel          context.CancelFunc        // Cancel function
	isConnected     bool                      // Whether client is connected
	connectedMutex  sync.RWMutex              // Protects isConnected flag
}

// ClientConfiguration contains configuration for the file transfer client
type ClientConfiguration struct {
	ServerAddress   string        // Address of the primary server
	OutputDirectory string        // Directory to save downloaded files
	ChunkTimeout    time.Duration // Timeout for chunk reception
	MaxRetries      int           // Maximum retry attempts per chunk
	ConnectTimeout  time.Duration // Timeout for initial connection
	TLSInsecure     bool          // Skip TLS verification (for testing)
}

// DownloadState tracks the state of an active download
type DownloadState struct {
	Filename         string                 // Name of the file being downloaded
	RequestedAt      time.Time              // When the download was requested
	StartTime        time.Time              // When the download started
	EndTime          time.Time              // When the download ended
	Metadata         *types.FileMetadata    // File metadata received from server
	ProgressCallback func(progress float64) // Progress callback function
	CancelCtx        context.Context        // Context for cancelling this download
	CancelFunc       context.CancelFunc     // Cancel function for this download
	Status           DownloadStatus         // Current download status
	ErrorMessage     string                 // Error message if download failed
}

// DownloadStatus represents the current status of a download
type DownloadStatus int

const (
	DownloadStatusRequesting DownloadStatus = iota // Requesting file from server
	DownloadStatusReceiving                        // Receiving chunks
	DownloadStatusCompleted                        // Download completed successfully
	DownloadStatusFailed                           // Download failed
	DownloadStatusCancelled                        // Download was cancelled
)

// DownloadProgress represents the progress of a file download
type DownloadProgress struct {
	Filename       string         `json:"filename"`        // Name of the file being downloaded
	Status         DownloadStatus `json:"status"`          // Current download status
	TotalChunks    uint32         `json:"total_chunks"`    // Total number of chunks
	ReceivedChunks uint32         `json:"received_chunks"` // Number of chunks received
	TotalBytes     int64          `json:"total_bytes"`     // Total file size in bytes
	ReceivedBytes  int64          `json:"received_bytes"`  // Number of bytes received
	StartTime      time.Time      `json:"start_time"`      // When the download started
	LastUpdate     time.Time      `json:"last_update"`     // Last progress update
	ErrorMessage   string         `json:"error_message"`   // Error message if failed
}

// DefaultClientConfiguration returns a default client configuration
func DefaultClientConfiguration() *ClientConfiguration {
	return &ClientConfiguration{
		ServerAddress:   "localhost:8080",
		OutputDirectory: "./downloads",
		ChunkTimeout:    30 * time.Second,
		MaxRetries:      3,
		ConnectTimeout:  10 * time.Second,
		TLSInsecure:     false,
	}
}

// NewKwikFileTransferClient creates a new file transfer client with the given configuration
func NewKwikFileTransferClient(clientConfig *ClientConfiguration) (*KwikFileTransferClient, error) {
	// Validate configuration
	if clientConfig == nil {
		return nil, fmt.Errorf("client configuration cannot be nil")
	}

	if clientConfig.ServerAddress == "" {
		return nil, fmt.Errorf("server address cannot be empty")
	}

	if clientConfig.OutputDirectory == "" {
		return nil, fmt.Errorf("output directory cannot be empty")
	}

	// Set defaults
	if clientConfig.ChunkTimeout <= 0 {
		clientConfig.ChunkTimeout = 30 * time.Second
	}

	if clientConfig.MaxRetries <= 0 {
		clientConfig.MaxRetries = 3
	}

	if clientConfig.ConnectTimeout <= 0 {
		clientConfig.ConnectTimeout = 10 * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())

	client := &KwikFileTransferClient{
		config:          clientConfig,
		activeDownloads: make(map[string]*DownloadState),
		ctx:             ctx,
		cancel:          cancel,
		isConnected:     false,
	}

	return client, nil
}

// Connect establishes a connection to the file transfer server
func (c *KwikFileTransferClient) Connect() error {
	c.connectedMutex.Lock()
	defer c.connectedMutex.Unlock()

	if c.isConnected {
		return fmt.Errorf("client is already connected")
	}

	// Create output directory if it doesn't exist
	if err := os.MkdirAll(c.config.OutputDirectory, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Create KWIK session
	kwikSession, err := c.createKwikSession()
	if err != nil {
		return fmt.Errorf("failed to create KWIK session: %w", err)
	}

	c.session = kwikSession

	// Create temporary directory for chunk storage
	tempDir := filepath.Join(c.config.OutputDirectory, ".tmp")
	chunkManager, err := NewChunkManager(tempDir, c.config.ChunkTimeout)
	if err != nil {
		return fmt.Errorf("failed to create chunk manager: %w", err)
	}

	c.chunkManager = chunkManager
	c.isConnected = true

	// Start background goroutines
	go c.handleIncomingChunks()
	go c.handleRetryLogic()

	fmt.Printf("File transfer client connected to %s\n", c.config.ServerAddress)
	return nil
}

// Disconnect closes the connection to the file transfer server
func (c *KwikFileTransferClient) Disconnect() error {
	c.connectedMutex.Lock()
	defer c.connectedMutex.Unlock()

	if !c.isConnected {
		return nil // Already disconnected
	}

	// Cancel context to stop all goroutines
	c.cancel()

	// Cancel all active downloads
	c.downloadsMutex.Lock()
	for filename, downloadState := range c.activeDownloads {
		downloadState.CancelFunc()
		if c.chunkManager != nil {
			c.chunkManager.CleanupTransfer(filename)
		}
	}
	c.activeDownloads = make(map[string]*DownloadState)
	c.downloadsMutex.Unlock()

	// Close chunk manager (cleanup all transfers)
	if c.chunkManager != nil {
		// Clean up all active transfers
		for filename := range c.activeDownloads {
			c.chunkManager.CleanupTransfer(filename)
		}
		c.chunkManager = nil
	}

	// Close KWIK session
	if c.session != nil {
		c.session.Close()
		c.session = nil
	}

	c.isConnected = false

	fmt.Printf("File transfer client disconnected\n")
	return nil
}

// IsConnected returns whether the client is currently connected
func (c *KwikFileTransferClient) IsConnected() bool {
	c.connectedMutex.RLock()
	defer c.connectedMutex.RUnlock()
	return c.isConnected
}

// createKwikSession creates and configures a KWIK client session
func (c *KwikFileTransferClient) createKwikSession() (session.Session, error) {
	// Configure KWIK for multi-path support
	kwikConfig := kwik.DefaultConfig()
	kwikConfig.MaxPathsPerSession = 10 // Allow multiple paths per session

	// Create connection context with timeout
	ctx, cancel := context.WithTimeout(c.ctx, c.config.ConnectTimeout)
	defer cancel()

	// Dial server using KWIK
	kwikSession, err := kwik.Dial(ctx, c.config.ServerAddress, kwikConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to dial server: %w", err)
	}

	fmt.Printf("DEBUG: Client connected to %s with %d paths\n", c.config.ServerAddress, len(kwikSession.GetActivePaths()))

	return kwikSession, nil
}

// DownloadFile initiates a file download with progress callback
func (c *KwikFileTransferClient) DownloadFile(filename string, progressCallback func(progress float64)) error {
	// Check if client is connected
	if !c.IsConnected() {
		return fmt.Errorf("client is not connected - call Connect() first")
	}

	c.downloadsMutex.Lock()
	defer c.downloadsMutex.Unlock()

	// Check if download is already in progress
	if _, exists := c.activeDownloads[filename]; exists {
		return fmt.Errorf("download for file %s is already in progress", filename)
	}

	// Create download context
	downloadCtx, downloadCancel := context.WithCancel(c.ctx)

	// Create download state
	downloadState := &DownloadState{
		Filename:         filename,
		RequestedAt:      time.Now(),
		ProgressCallback: progressCallback,
		CancelCtx:        downloadCtx,
		CancelFunc:       downloadCancel,
		Status:           DownloadStatusRequesting,
	}

	c.activeDownloads[filename] = downloadState

	// Send file request to server
	go c.requestFile(filename, downloadState)

	return nil
}

// GetDownloadProgress returns the current progress of a file download
func (c *KwikFileTransferClient) GetDownloadProgress(filename string) (*TransferProgress, error) {
	c.downloadsMutex.RLock()
	downloadState, exists := c.activeDownloads[filename]
	c.downloadsMutex.RUnlock()

	if !exists {
		return nil, fmt.Errorf("no active download found for file: %s", filename)
	}

	// If we haven't received metadata yet, return basic progress
	if downloadState.Metadata == nil {
		return &TransferProgress{
			Filename:       filename,
			TotalChunks:    0,
			ReceivedChunks: 0,
			TotalBytes:     0,
			ReceivedBytes:  0,
			Speed:          0,
			ETA:            0,
		}, nil
	}

	// Get progress from chunk manager
	return c.chunkManager.GetTransferProgress(filename)
}

// CancelDownload cancels an active download
func (c *KwikFileTransferClient) CancelDownload(filename string) error {
	c.downloadsMutex.Lock()
	defer c.downloadsMutex.Unlock()

	downloadState, exists := c.activeDownloads[filename]
	if !exists {
		return fmt.Errorf("no active download found for file: %s", filename)
	}

	// Cancel the download context
	downloadState.CancelFunc()
	downloadState.Status = DownloadStatusCancelled

	// Clean up chunk manager state
	c.chunkManager.CleanupTransfer(filename)

	// Remove from active downloads
	delete(c.activeDownloads, filename)

	return nil
}

// requestFile sends a file request to the server
func (c *KwikFileTransferClient) requestFile(filename string, downloadState *DownloadState) {
	fmt.Printf("DEBUG: Starting file request for %s\n", filename)

	// Create file request message
	fileRequest := map[string]interface{}{
		"type":      "FILE_REQUEST",
		"filename":  filename,
		"timestamp": time.Now().Unix(),
	}

	// Serialize request
	requestData, err := json.Marshal(fileRequest)
	if err != nil {
		fmt.Printf("DEBUG: Failed to serialize file request for %s: %v\n", filename, err)
		c.handleDownloadError(filename, fmt.Errorf("failed to serialize file request: %w", err))
		return
	}

	fmt.Printf("DEBUG: Opening stream for file request %s\n", filename)
	// Open stream to send request
	stream, err := c.session.OpenStreamSync(downloadState.CancelCtx)
	if err != nil {
		fmt.Printf("DEBUG: Failed to open stream for %s: %v\n", filename, err)
		c.handleDownloadError(filename, fmt.Errorf("failed to open stream for file request: %w", err))
		return
	}
	defer stream.Close()

	fmt.Printf("DEBUG: Sending file request for %s (%d bytes)\n", filename, len(requestData))
	// Send request
	_, err = stream.Write(requestData)
	if err != nil {
		fmt.Printf("DEBUG: Failed to send file request for %s: %v\n", filename, err)
		c.handleDownloadError(filename, fmt.Errorf("failed to send file request: %w", err))
		return
	}

	fmt.Printf("DEBUG: File request sent for %s, waiting for metadata response\n", filename)
	// Wait for metadata response
	c.waitForMetadataResponse(filename, stream, downloadState)
}

// waitForMetadataResponse waits for the server to send file metadata
func (c *KwikFileTransferClient) waitForMetadataResponse(filename string, stream session.Stream, downloadState *DownloadState) {
	fmt.Printf("DEBUG: Waiting for metadata response for %s\n", filename)
	buffer := make([]byte, 4096)

	// Set read timeout
	ctx, cancel := context.WithTimeout(downloadState.CancelCtx, 30*time.Second)
	defer cancel()

	// Read metadata response
	select {
	case <-ctx.Done():
		fmt.Printf("DEBUG: Timeout waiting for metadata response for %s\n", filename)
		c.handleDownloadError(filename, fmt.Errorf("timeout waiting for metadata response"))
		return
	default:
		fmt.Printf("DEBUG: Reading metadata response for %s\n", filename)
		n, err := stream.Read(buffer)
		if err != nil {
			fmt.Printf("DEBUG: Failed to read metadata response for %s: %v\n", filename, err)
			c.handleDownloadError(filename, fmt.Errorf("failed to read metadata response: %w", err))
			return
		}

		fmt.Printf("DEBUG: Received %d bytes of metadata response for %s\n", n, filename)
		fmt.Printf("DEBUG: Raw response: %s\n", string(buffer[:n]))

		// Parse metadata response
		var response map[string]interface{}
		if err := json.Unmarshal(buffer[:n], &response); err != nil {
			fmt.Printf("DEBUG: Failed to parse metadata response for %s: %v\n", filename, err)
			c.handleDownloadError(filename, fmt.Errorf("failed to parse metadata response: %w", err))
			return
		}

		fmt.Printf("DEBUG: Parsed metadata response for %s: %+v\n", filename, response)

		// Check response type
		responseType, ok := response["type"].(string)
		if !ok || responseType != "FILE_METADATA" {
			fmt.Printf("DEBUG: Unexpected response type for %s: %v\n", filename, responseType)
			c.handleDownloadError(filename, fmt.Errorf("unexpected response type: %v", responseType))
			return
		}

		fmt.Printf("DEBUG: Valid FILE_METADATA response received for %s\n", filename)

		// Extract metadata
		metadata, err := c.parseFileMetadata(response)
		if err != nil {
			fmt.Printf("DEBUG: Failed to parse file metadata for %s: %v\n", filename, err)
			c.handleDownloadError(filename, fmt.Errorf("failed to parse file metadata: %w", err))
			return
		}

		fmt.Printf("DEBUG: Parsed metadata for %s: %+v\n", filename, metadata)

		// Update download state
		c.downloadsMutex.Lock()
		downloadState.Metadata = metadata
		downloadState.Status = DownloadStatusReceiving
		c.downloadsMutex.Unlock()

		fmt.Printf("DEBUG: Starting transfer in chunk manager for %s\n", filename)
		// Start transfer in chunk manager
		err = c.chunkManager.StartTransfer(metadata, downloadState.ProgressCallback)
		if err != nil {
			fmt.Printf("DEBUG: Failed to start transfer for %s: %v\n", filename, err)
			c.handleDownloadError(filename, fmt.Errorf("failed to start transfer: %w", err))
			return
		}

		fmt.Printf("DEBUG: Transfer started successfully for %s\n", filename)
	}
}

// parseFileMetadata parses file metadata from server response
func (c *KwikFileTransferClient) parseFileMetadata(response map[string]interface{}) (*types.FileMetadata, error) {
	filename, ok := response["filename"].(string)
	if !ok {
		return nil, fmt.Errorf("missing or invalid filename")
	}

	size, ok := response["size"].(float64)
	if !ok {
		return nil, fmt.Errorf("missing or invalid file size")
	}

	chunkSize, ok := response["chunk_size"].(float64)
	if !ok {
		return nil, fmt.Errorf("missing or invalid chunk size")
	}

	totalChunks, ok := response["total_chunks"].(float64)
	if !ok {
		return nil, fmt.Errorf("missing or invalid total chunks")
	}

	checksum, _ := response["checksum"].(string) // Optional

	metadata := types.NewFileMetadata(filename, int64(size), int32(chunkSize))
	metadata.TotalChunks = uint32(totalChunks)
	metadata.Checksum = checksum

	return metadata, nil
}

// handleIncomingChunks processes incoming file chunks from the server
func (c *KwikFileTransferClient) handleIncomingChunks() {
	for {
		select {
		case <-c.ctx.Done():
			return
		default:
			// Accept incoming streams
			stream, err := c.session.AcceptStream(c.ctx)
			if err != nil {
				time.Sleep(100 * time.Millisecond)
				continue
			}

			// Handle chunk stream in goroutine
			go c.handleChunkStream(stream)
		}
	}
}

// handleChunkStream processes chunks from a single stream
func (c *KwikFileTransferClient) handleChunkStream(stream session.Stream) {
	defer stream.Close()

	buffer := make([]byte, 128*1024) // 128KB buffer for chunk data

	for {
		// Read chunk data
		n, err := stream.Read(buffer)
		if err != nil {
			return // Stream closed or error
		}

		if n == 0 {
			continue
		}

		// Parse chunk
		var chunk types.FileChunk
		if err := json.Unmarshal(buffer[:n], &chunk); err != nil {
			continue // Invalid chunk data
		}

		// Process chunk through chunk manager
		err = c.chunkManager.ReceiveChunkWithValidation(&chunk, c.config.MaxRetries)
		if err != nil {
			// Handle chunk reception error
			c.handleChunkError(chunk.Filename, chunk.SequenceNum, err)
			continue
		}

		// Check if transfer is complete
		complete, err := c.chunkManager.IsTransferComplete(chunk.Filename)
		if err == nil && complete {
			c.handleTransferComplete(chunk.Filename)
		}
	}
}

// handleRetryLogic manages chunk retry requests
func (c *KwikFileTransferClient) handleRetryLogic() {
	ticker := time.NewTicker(c.config.ChunkTimeout / 2) // Check twice per timeout period
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.processRetryRequests()
		}
	}
}

// processRetryRequests processes timed-out chunks and requests retries
func (c *KwikFileTransferClient) processRetryRequests() {
	// Get all timed-out chunks
	timedOutChunks, err := c.chunkManager.GetAllTimedOutChunks()
	if err != nil {
		return
	}

	for _, retryRequest := range timedOutChunks {
		// Check if we should retry this chunk
		shouldRetry, err := c.chunkManager.ShouldRetryChunk(retryRequest.Filename, retryRequest.SequenceNum, c.config.MaxRetries)
		if err != nil || !shouldRetry {
			continue
		}

		// Send retry request to server
		c.sendChunkRetryRequest(retryRequest)
	}
}

// sendChunkRetryRequest sends a retry request for a specific chunk
func (c *KwikFileTransferClient) sendChunkRetryRequest(retryRequest types.ChunkRetryRequest) {
	// Create retry request message
	request := map[string]interface{}{
		"type":         "CHUNK_RETRY",
		"filename":     retryRequest.Filename,
		"chunk_id":     retryRequest.ChunkID,
		"sequence_num": retryRequest.SequenceNum,
		"retry_count":  retryRequest.RetryCount,
		"timestamp":    time.Now().Unix(),
	}

	// Serialize request
	requestData, err := json.Marshal(request)
	if err != nil {
		return
	}

	// Send via raw data to primary path (control channel)
	activePaths := c.session.GetActivePaths()
	if len(activePaths) == 0 {
		return
	}

	// Find primary path
	var primaryPathID string
	for _, path := range activePaths {
		if path.IsPrimary {
			primaryPathID = path.PathID
			break
		}
	}

	if primaryPathID != "" {
		c.session.SendRawData(requestData, primaryPathID)

		// Mark chunk as requested
		c.chunkManager.MarkChunkRequested(retryRequest.Filename, retryRequest.SequenceNum)
	}
}

// handleDownloadError handles errors during download
func (c *KwikFileTransferClient) handleDownloadError(filename string, err error) {
	c.downloadsMutex.Lock()
	defer c.downloadsMutex.Unlock()

	downloadState, exists := c.activeDownloads[filename]
	if !exists {
		return
	}

	downloadState.Status = DownloadStatusFailed
	downloadState.ErrorMessage = err.Error()

	// Clean up chunk manager state
	c.chunkManager.CleanupTransfer(filename)

	// Remove from active downloads
	delete(c.activeDownloads, filename)
}

// handleChunkError handles errors during chunk processing
func (c *KwikFileTransferClient) handleChunkError(filename string, sequenceNum uint32, err error) {
	// For now, just log the error
	// In a production system, this could trigger specific retry logic
	// or notify the application layer
}

// handleTransferComplete handles successful transfer completion
func (c *KwikFileTransferClient) handleTransferComplete(filename string) {
	c.downloadsMutex.Lock()
	defer c.downloadsMutex.Unlock()

	downloadState, exists := c.activeDownloads[filename]
	if !exists {
		return
	}

	// Get reconstructed file path
	tempFilePath, err := c.chunkManager.GetReconstructedFilePath(filename)
	if err != nil {
		c.handleDownloadError(filename, fmt.Errorf("failed to get reconstructed file: %w", err))
		return
	}

	// Move file to final destination
	finalPath := filepath.Join(c.config.OutputDirectory, filename)
	err = os.Rename(tempFilePath, finalPath)
	if err != nil {
		c.handleDownloadError(filename, fmt.Errorf("failed to move file to final destination: %w", err))
		return
	}

	// Update download state
	downloadState.Status = DownloadStatusCompleted

	// Final progress callback
	if downloadState.ProgressCallback != nil {
		downloadState.ProgressCallback(1.0)
	}

	// Clean up chunk manager state
	c.chunkManager.CleanupTransfer(filename)

	// Remove from active downloads
	delete(c.activeDownloads, filename)
}

// Close closes the file transfer client and cleans up resources
func (c *KwikFileTransferClient) Close() error {
	c.cancel()

	// Cancel all active downloads
	c.downloadsMutex.Lock()
	for filename, downloadState := range c.activeDownloads {
		downloadState.CancelFunc()
		c.chunkManager.CleanupTransfer(filename)
	}
	c.activeDownloads = make(map[string]*DownloadState)
	c.downloadsMutex.Unlock()

	return nil
}

// GetActiveDownloads returns a list of currently active downloads
func (c *KwikFileTransferClient) GetActiveDownloads() []string {
	c.downloadsMutex.RLock()
	defer c.downloadsMutex.RUnlock()

	filenames := make([]string, 0, len(c.activeDownloads))
	for filename := range c.activeDownloads {
		filenames = append(filenames, filename)
	}

	return filenames
}

// GetDownloadStatus returns the status of a specific download
func (c *KwikFileTransferClient) GetDownloadStatus(filename string) (DownloadStatus, error) {
	c.downloadsMutex.RLock()
	defer c.downloadsMutex.RUnlock()

	downloadState, exists := c.activeDownloads[filename]
	if !exists {
		return DownloadStatusFailed, fmt.Errorf("no download found for file: %s", filename)
	}

	return downloadState.Status, nil
}

// String methods for enums
func (s DownloadStatus) String() string {
	switch s {
	case DownloadStatusRequesting:
		return "REQUESTING"
	case DownloadStatusReceiving:
		return "RECEIVING"
	case DownloadStatusCompleted:
		return "COMPLETED"
	case DownloadStatusFailed:
		return "FAILED"
	case DownloadStatusCancelled:
		return "CANCELLED"
	default:
		return "UNKNOWN"
	}
}

// PathStatus String method is now provided by session.PathStatus
