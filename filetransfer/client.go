package filetransfer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileTransferClient provides the interface for downloading files using multi-path transfer
type FileTransferClient interface {
	DownloadFile(filename string, progressCallback func(progress float64)) error
	GetDownloadProgress(filename string) (*TransferProgress, error)
	CancelDownload(filename string) error
}

// KwikFileTransferClient implements FileTransferClient using KWIK sessions
type KwikFileTransferClient struct {
	session         Session                    // KWIK session interface
	chunkManager    *ChunkManager              // Manages chunk reception and reconstruction
	activeDownloads map[string]*DownloadState  // Active downloads by filename
	downloadsMutex  sync.RWMutex               // Protects activeDownloads map
	outputDir       string                     // Directory to save downloaded files
	chunkTimeout    time.Duration              // Timeout for chunk reception
	maxRetries      int                        // Maximum retry attempts per chunk
	ctx             context.Context            // Context for cancellation
	cancel          context.CancelFunc         // Cancel function
}

// Session interface for KWIK integration (abstracted for testing and adapter pattern)
type Session interface {
	OpenStreamSync(ctx context.Context) (Stream, error)
	AcceptStream(ctx context.Context) (Stream, error)
	SendRawData(data []byte, pathID string) error
	GetActivePaths() []PathInfo
	Close() error
}

// Stream interface for KWIK streams (abstracted for testing and adapter pattern)
type Stream interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Close() error
	StreamID() uint64
	PathID() string
}

// PathInfo contains information about a connection path
type PathInfo struct {
	PathID    string
	Address   string
	IsPrimary bool
	Status    PathStatus
	CreatedAt time.Time
	LastActive time.Time
}

// PathStatus represents the current status of a path
type PathStatus int

const (
	PathStatusActive PathStatus = iota
	PathStatusDead
	PathStatusConnecting
	PathStatusDisconnecting
)

// DownloadState tracks the state of an active download
type DownloadState struct {
	Filename         string                    // Name of the file being downloaded
	RequestedAt      time.Time                 // When the download was requested
	Metadata         *FileMetadata             // File metadata received from server
	ProgressCallback func(progress float64)    // Progress callback function
	CancelCtx        context.Context           // Context for cancelling this download
	CancelFunc       context.CancelFunc        // Cancel function for this download
	Status           DownloadStatus            // Current download status
	ErrorMessage     string                    // Error message if download failed
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

// NewKwikFileTransferClient creates a new file transfer client
func NewKwikFileTransferClient(session Session, outputDir string, chunkTimeout time.Duration, maxRetries int) (*KwikFileTransferClient, error) {
	// Create output directory if it doesn't exist
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory: %w", err)
	}

	// Create temporary directory for chunk storage
	tempDir := filepath.Join(outputDir, ".tmp")
	chunkManager, err := NewChunkManager(tempDir, chunkTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to create chunk manager: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	client := &KwikFileTransferClient{
		session:         session,
		chunkManager:    chunkManager,
		activeDownloads: make(map[string]*DownloadState),
		outputDir:       outputDir,
		chunkTimeout:    chunkTimeout,
		maxRetries:      maxRetries,
		ctx:             ctx,
		cancel:          cancel,
	}

	// Start background goroutines
	go client.handleIncomingChunks()
	go client.handleRetryLogic()

	return client, nil
}

// DownloadFile initiates a file download with progress callback
func (c *KwikFileTransferClient) DownloadFile(filename string, progressCallback func(progress float64)) error {
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
	// Create file request message
	fileRequest := map[string]interface{}{
		"type":     "FILE_REQUEST",
		"filename": filename,
		"timestamp": time.Now().Unix(),
	}

	// Serialize request
	requestData, err := json.Marshal(fileRequest)
	if err != nil {
		c.handleDownloadError(filename, fmt.Errorf("failed to serialize file request: %w", err))
		return
	}

	// Open stream to send request
	stream, err := c.session.OpenStreamSync(downloadState.CancelCtx)
	if err != nil {
		c.handleDownloadError(filename, fmt.Errorf("failed to open stream for file request: %w", err))
		return
	}
	defer stream.Close()

	// Send request
	_, err = stream.Write(requestData)
	if err != nil {
		c.handleDownloadError(filename, fmt.Errorf("failed to send file request: %w", err))
		return
	}

	// Wait for metadata response
	c.waitForMetadataResponse(filename, stream, downloadState)
}

// waitForMetadataResponse waits for the server to send file metadata
func (c *KwikFileTransferClient) waitForMetadataResponse(filename string, stream Stream, downloadState *DownloadState) {
	buffer := make([]byte, 4096)
	
	// Set read timeout
	ctx, cancel := context.WithTimeout(downloadState.CancelCtx, 30*time.Second)
	defer cancel()

	// Read metadata response
	select {
	case <-ctx.Done():
		c.handleDownloadError(filename, fmt.Errorf("timeout waiting for metadata response"))
		return
	default:
		n, err := stream.Read(buffer)
		if err != nil {
			c.handleDownloadError(filename, fmt.Errorf("failed to read metadata response: %w", err))
			return
		}

		// Parse metadata response
		var response map[string]interface{}
		if err := json.Unmarshal(buffer[:n], &response); err != nil {
			c.handleDownloadError(filename, fmt.Errorf("failed to parse metadata response: %w", err))
			return
		}

		// Check response type
		responseType, ok := response["type"].(string)
		if !ok || responseType != "FILE_METADATA" {
			c.handleDownloadError(filename, fmt.Errorf("unexpected response type: %v", responseType))
			return
		}

		// Extract metadata
		metadata, err := c.parseFileMetadata(response)
		if err != nil {
			c.handleDownloadError(filename, fmt.Errorf("failed to parse file metadata: %w", err))
			return
		}

		// Update download state
		c.downloadsMutex.Lock()
		downloadState.Metadata = metadata
		downloadState.Status = DownloadStatusReceiving
		c.downloadsMutex.Unlock()

		// Start transfer in chunk manager
		err = c.chunkManager.StartTransfer(metadata, downloadState.ProgressCallback)
		if err != nil {
			c.handleDownloadError(filename, fmt.Errorf("failed to start transfer: %w", err))
			return
		}
	}
}

// parseFileMetadata parses file metadata from server response
func (c *KwikFileTransferClient) parseFileMetadata(response map[string]interface{}) (*FileMetadata, error) {
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

	metadata := NewFileMetadata(filename, int64(size), int32(chunkSize))
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
func (c *KwikFileTransferClient) handleChunkStream(stream Stream) {
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
		var chunk FileChunk
		if err := json.Unmarshal(buffer[:n], &chunk); err != nil {
			continue // Invalid chunk data
		}

		// Process chunk through chunk manager
		err = c.chunkManager.ReceiveChunkWithValidation(&chunk, c.maxRetries)
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
	ticker := time.NewTicker(c.chunkTimeout / 2) // Check twice per timeout period
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
		shouldRetry, err := c.chunkManager.ShouldRetryChunk(retryRequest.Filename, retryRequest.SequenceNum, c.maxRetries)
		if err != nil || !shouldRetry {
			continue
		}

		// Send retry request to server
		c.sendChunkRetryRequest(retryRequest)
	}
}

// sendChunkRetryRequest sends a retry request for a specific chunk
func (c *KwikFileTransferClient) sendChunkRetryRequest(retryRequest ChunkRetryRequest) {
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
	finalPath := filepath.Join(c.outputDir, filename)
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

func (s PathStatus) String() string {
	switch s {
	case PathStatusActive:
		return "ACTIVE"
	case PathStatusDead:
		return "DEAD"
	case PathStatusConnecting:
		return "CONNECTING"
	case PathStatusDisconnecting:
		return "DISCONNECTING"
	default:
		return "UNKNOWN"
	}
}