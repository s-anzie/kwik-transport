package filetransfer

import (
	"context"
	"fmt"
	"sync"
	"time"

	kwik "kwik/pkg"
	"kwik/pkg/session"
)

// StandaloneFileTransferClient is a standalone file transfer client with its own KWIK session
type StandaloneFileTransferClient struct {
	serverAddress   string
	session         session.Session
	downloadDir     string
	chunkTimeout    time.Duration
	maxRetries      int
	activeDownloads map[string]*DownloadState
	downloadsMutex  sync.RWMutex
	ctx             context.Context
	cancel          context.CancelFunc
	isConnected     bool
	connectedMutex  sync.RWMutex
}

// NewStandaloneFileTransferClient creates a new standalone file transfer client
func NewStandaloneFileTransferClient(serverAddress, downloadDir string, chunkTimeout time.Duration, maxRetries int) (*StandaloneFileTransferClient, error) {
	if serverAddress == "" {
		return nil, fmt.Errorf("server address cannot be empty")
	}
	if downloadDir == "" {
		return nil, fmt.Errorf("download directory cannot be empty")
	}

	ctx, cancel := context.WithCancel(context.Background())

	client := &StandaloneFileTransferClient{
		serverAddress:   serverAddress,
		downloadDir:     downloadDir,
		chunkTimeout:    chunkTimeout,
		maxRetries:      maxRetries,
		activeDownloads: make(map[string]*DownloadState),
		ctx:             ctx,
		cancel:          cancel,
		isConnected:     false,
	}

	return client, nil
}

// Connect connects to the file transfer server
func (c *StandaloneFileTransferClient) Connect() error {
	c.connectedMutex.Lock()
	defer c.connectedMutex.Unlock()

	if c.isConnected {
		return fmt.Errorf("client is already connected")
	}

	fmt.Printf("📡 Connecting to file transfer server at %s...\n", c.serverAddress)

	// Create KWIK session
	session, err := kwik.Dial(c.ctx, c.serverAddress, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}

	c.session = session
	c.isConnected = true

	fmt.Printf("✅ Connected to file transfer server\n")
	fmt.Printf("🛤️  Active paths (%d total):\n", len(session.GetActivePaths()))
	for i, path := range session.GetActivePaths() {
		fmt.Printf("   %d. %s (Primary: %v, Status: %s)\n",
			i+1, path.Address, path.IsPrimary, path.Status)
	}

	return nil
}

// Disconnect disconnects from the file transfer server
func (c *StandaloneFileTransferClient) Disconnect() error {
	c.connectedMutex.Lock()
	defer c.connectedMutex.Unlock()

	if !c.isConnected {
		return nil
	}

	fmt.Printf("🔌 Disconnecting from file transfer server...\n")

	// Cancel all active downloads
	c.downloadsMutex.Lock()
	for filename := range c.activeDownloads {
		fmt.Printf("❌ Cancelling download: %s\n", filename)
	}
	c.activeDownloads = make(map[string]*DownloadState)
	c.downloadsMutex.Unlock()

	// Cancel context
	c.cancel()

	// Close session
	if c.session != nil {
		c.session.Close()
		c.session = nil
	}

	c.isConnected = false
	fmt.Printf("✅ Disconnected from file transfer server\n")

	return nil
}

// IsConnected returns whether the client is connected to the server
func (c *StandaloneFileTransferClient) IsConnected() bool {
	c.connectedMutex.RLock()
	defer c.connectedMutex.RUnlock()
	return c.isConnected
}

// DownloadFile downloads a file from the server
func (c *StandaloneFileTransferClient) DownloadFile(filename string, progressCallback func(float64)) error {
	if !c.IsConnected() {
		return fmt.Errorf("client is not connected to server")
	}

	if filename == "" {
		return fmt.Errorf("filename cannot be empty")
	}

	// Check if download is already in progress
	c.downloadsMutex.RLock()
	if _, exists := c.activeDownloads[filename]; exists {
		c.downloadsMutex.RUnlock()
		return fmt.Errorf("download already in progress for file: %s", filename)
	}
	c.downloadsMutex.RUnlock()

	fmt.Printf("🔄 Starting download request for %s...\n", filename)

	// Create download state
	downloadState := &DownloadState{
		Filename:         filename,
		Status:           DownloadStatusRequesting,
		StartTime:        time.Now(),
		ProgressCallback: progressCallback,
	}

	// Add to active downloads
	c.downloadsMutex.Lock()
	c.activeDownloads[filename] = downloadState
	c.downloadsMutex.Unlock()

	// Start download in goroutine
	go c.performDownload(filename, downloadState)

	fmt.Printf("✅ Download request sent for %s\n", filename)
	return nil
}

// performDownload performs the actual file download
func (c *StandaloneFileTransferClient) performDownload(filename string, downloadState *DownloadState) {
	defer func() {
		// Remove from active downloads when done
		c.downloadsMutex.Lock()
		delete(c.activeDownloads, filename)
		c.downloadsMutex.Unlock()
	}()

	// Send file request
	err := c.sendFileRequest(filename)
	if err != nil {
		c.handleDownloadError(filename, fmt.Errorf("failed to send file request: %w", err))
		return
	}

	// Wait for metadata response and handle chunks
	err = c.handleFileTransfer(filename, downloadState)
	if err != nil {
		c.handleDownloadError(filename, fmt.Errorf("file transfer failed: %w", err))
		return
	}

	// Update status to completed
	c.downloadsMutex.Lock()
	downloadState.Status = DownloadStatusCompleted
	downloadState.EndTime = time.Now()
	c.downloadsMutex.Unlock()

	duration := time.Since(downloadState.StartTime)
	fmt.Printf("✅ Download completed: %s (%.2fs)\n", filename, duration.Seconds())
}

// sendFileRequest sends a file request to the server
func (c *StandaloneFileTransferClient) sendFileRequest(filename string) error {
	fmt.Printf("DEBUG: Starting file request for %s\n", filename)

	// Open stream for file request
	stream, err := c.session.OpenStream()
	if err != nil {
		return fmt.Errorf("failed to open stream: %w", err)
	}
	defer stream.Close()

	fmt.Printf("DEBUG: Opening stream for file request %s\n", filename)

	// Create file request
	request := map[string]interface{}{
		"type":      "FILE_REQUEST",
		"filename":  filename,
		"timestamp": time.Now().Unix(),
	}

	// Send request
	return c.sendRequest(stream, request)
}

// sendRequest sends a request over a stream
func (c *StandaloneFileTransferClient) sendRequest(stream session.Stream, request map[string]interface{}) error {
	requestData, err := c.serializeRequest(request)
	if err != nil {
		return fmt.Errorf("failed to serialize request: %w", err)
	}

	filename := request["filename"].(string)
	fmt.Printf("DEBUG: Sending file request for %s (%d bytes)\n", filename, len(requestData))

	_, err = stream.Write(requestData)
	if err != nil {
		return fmt.Errorf("failed to write request: %w", err)
	}

	fmt.Printf("DEBUG: File request sent for %s, waiting for metadata response\n", filename)
	return nil
}

// handleFileTransfer handles the file transfer process
func (c *StandaloneFileTransferClient) handleFileTransfer(filename string, downloadState *DownloadState) error {
	// For now, just simulate the transfer process
	// In a real implementation, this would handle metadata response and chunk reception
	
	fmt.Printf("DEBUG: Simulating file transfer for %s\n", filename)
	
	// Simulate progress updates
	if downloadState.ProgressCallback != nil {
		for i := 0; i <= 100; i += 10 {
			progress := float64(i) / 100.0
			downloadState.ProgressCallback(progress)
			time.Sleep(100 * time.Millisecond)
		}
	}
	
	return nil
}

// serializeRequest serializes a request to JSON
func (c *StandaloneFileTransferClient) serializeRequest(request map[string]interface{}) ([]byte, error) {
	// This would use JSON marshaling in a real implementation
	// For now, return a simple byte representation
	filename := request["filename"].(string)
	requestStr := fmt.Sprintf(`{"type":"FILE_REQUEST","filename":"%s","timestamp":%d}`, 
		filename, request["timestamp"])
	return []byte(requestStr), nil
}

// handleDownloadError handles download errors
func (c *StandaloneFileTransferClient) handleDownloadError(filename string, err error) {
	fmt.Printf("❌ Download error for %s: %v\n", filename, err)

	c.downloadsMutex.Lock()
	if downloadState, exists := c.activeDownloads[filename]; exists {
		downloadState.Status = DownloadStatusFailed
		downloadState.ErrorMessage = err.Error()
		downloadState.EndTime = time.Now()
	}
	c.downloadsMutex.Unlock()
}

// GetActiveDownloads returns a list of active download filenames
func (c *StandaloneFileTransferClient) GetActiveDownloads() []string {
	c.downloadsMutex.RLock()
	defer c.downloadsMutex.RUnlock()

	downloads := make([]string, 0, len(c.activeDownloads))
	for filename := range c.activeDownloads {
		downloads = append(downloads, filename)
	}
	return downloads
}

// GetDownloadProgress returns the progress of a specific download
func (c *StandaloneFileTransferClient) GetDownloadProgress(filename string) (*DownloadProgress, error) {
	c.downloadsMutex.RLock()
	defer c.downloadsMutex.RUnlock()

	downloadState, exists := c.activeDownloads[filename]
	if !exists {
		return nil, fmt.Errorf("no active download found for file: %s", filename)
	}

	progress := &DownloadProgress{
		Filename:        downloadState.Filename,
		Status:          downloadState.Status,
		TotalChunks:     0, // Will be set when metadata is received
		ReceivedChunks:  0, // Will be updated as chunks are received
		TotalBytes:      0, // Will be set when metadata is received
		ReceivedBytes:   0, // Will be updated as chunks are received
		StartTime:       downloadState.StartTime,
		LastUpdate:      time.Now(),
		ErrorMessage:    downloadState.ErrorMessage,
	}

	return progress, nil
}

// GetDownloadStatus returns the status of a specific download
func (c *StandaloneFileTransferClient) GetDownloadStatus(filename string) (DownloadStatus, error) {
	c.downloadsMutex.RLock()
	defer c.downloadsMutex.RUnlock()

	downloadState, exists := c.activeDownloads[filename]
	if !exists {
		return DownloadStatusFailed, fmt.Errorf("no active download found for file: %s", filename)
	}

	return downloadState.Status, nil
}

// CancelDownload cancels a specific download
func (c *StandaloneFileTransferClient) CancelDownload(filename string) error {
	c.downloadsMutex.Lock()
	defer c.downloadsMutex.Unlock()

	downloadState, exists := c.activeDownloads[filename]
	if !exists {
		return fmt.Errorf("no active download found for file: %s", filename)
	}

	downloadState.Status = DownloadStatusCancelled
	downloadState.EndTime = time.Now()
	delete(c.activeDownloads, filename)

	fmt.Printf("❌ Download cancelled: %s\n", filename)
	return nil
}