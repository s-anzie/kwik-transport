package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"ftpa/internal/config"
	"ftpa/internal/types"
	"io"
	kwik "kwik/pkg"
	"kwik/pkg/session"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileTransferServer manages file transfer requests and coordinates chunk distribution
type FileTransferServer struct {
	config           *config.ServerConfiguration // Server configuration
	session          session.Session             // KWIK session for communication
	listener         session.Listener            // KWIK listener for accepting connections
	chunkCoordinator *ChunkCoordinator           // Coordinates chunk distribution
	activeRequests   map[string]*RequestState    // Active file requests by client address
	requestsMutex    sync.RWMutex                // Protects activeRequests map
	ctx              context.Context             // Context for cancellation
	cancel           context.CancelFunc          // Cancel function
	isRunning        bool                        // Whether the server is running
	runningMutex     sync.RWMutex                // Protects isRunning flag
}

// RequestState tracks the state of a file transfer request
type RequestState struct {
	ClientAddress string              // Address of the requesting client
	Filename      string              // Requested filename
	RequestedAt   time.Time           // When the request was made
	Metadata      *types.FileMetadata // File metadata
	TransferState *TransferState      // Transfer state from coordinator
	Status        RequestStatus       // Current request status
	ErrorMessage  string              // Error message if request failed
	stream        session.Stream      // KWIK stream for this request
}

// RequestStatus represents the current status of a file request
type RequestStatus int

const (
	RequestStatusReceived     RequestStatus = iota // Request received, processing
	RequestStatusPreparing                         // Preparing file for transfer
	RequestStatusTransferring                      // Actively transferring chunks
	RequestStatusCompleted                         // Transfer completed successfully
	RequestStatusFailed                            // Request failed
	RequestStatusCancelled                         // Request was cancelled
)

// NewFileTransferServer creates a new file transfer server with the given configuration
func NewFileTransferServer(serverConfig *config.ServerConfiguration) (*FileTransferServer, error) {
	// Validate configuration
	if serverConfig == nil {
		return nil, fmt.Errorf("server configuration cannot be nil")
	}

	// Validate configuration
	if err := serverConfig.Validate(); err != nil {
		return nil, fmt.Errorf("invalid server configuration: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	server := &FileTransferServer{
		config:         serverConfig,
		activeRequests: make(map[string]*RequestState),
		ctx:            ctx,
		cancel:         cancel,
		isRunning:      false,
	}

	return server, nil
}

// Start starts the file transfer server
func (fts *FileTransferServer) Start() error {
	fts.runningMutex.Lock()
	defer fts.runningMutex.Unlock()

	if fts.isRunning {
		return fmt.Errorf("server is already running")
	}

	// Create KWIK server listener
	err := fts.createKwikServer()
	if err != nil {
		return fmt.Errorf("failed to create KWIK server: %w", err)
	}

	fts.isRunning = true

	// Start background goroutines
	go fts.monitorRequests()

	fmt.Printf("File transfer server started on %s\n", fts.config.Server.Address)
	return nil
}

// Stop stops the file transfer server
func (fts *FileTransferServer) Stop() error {
	fts.runningMutex.Lock()
	defer fts.runningMutex.Unlock()

	if !fts.isRunning {
		return nil // Already stopped
	}

	// Cancel context to stop all goroutines
	fts.cancel()

	// Close chunk coordinator
	if fts.chunkCoordinator != nil {
		fts.chunkCoordinator.Close()
		fts.chunkCoordinator = nil
	}

	// Close KWIK listener
	if fts.listener != nil {
		fts.listener.Close()
		fts.listener = nil
	}

	// Close KWIK session
	if fts.session != nil {
		fts.session.Close()
		fts.session = nil
	}

	// Clean up all active requests
	fts.requestsMutex.Lock()
	fts.activeRequests = make(map[string]*RequestState)
	fts.requestsMutex.Unlock()

	fts.isRunning = false

	fmt.Printf("File transfer server stopped\n")
	return nil
}

// IsRunning returns whether the server is currently running
func (fts *FileTransferServer) IsRunning() bool {
	fts.runningMutex.RLock()
	defer fts.runningMutex.RUnlock()
	return fts.isRunning
}

// createKwikServer creates and configures a KWIK server listener
func (fts *FileTransferServer) createKwikServer() error {
	// Configure KWIK for multi-path support
	kwikConfig := kwik.DefaultConfig()
	kwikConfig.MaxPathsPerSession = 10 // Allow multiple paths per session

	// Create KWIK listener
	listener, err := kwik.Listen(fts.config.Server.Address, kwikConfig)
	if err != nil {
		return fmt.Errorf("failed to create KWIK listener: %w", err)
	}

	// Store listener for cleanup
	fts.listener = listener

	// Start accepting connections in background
	go fts.acceptConnections(listener)

	// Return nil as we handle sessions in acceptConnections
	// This is different from the original design but follows KWIK patterns
	return nil
}

// acceptConnections accepts incoming KWIK connections
func (fts *FileTransferServer) acceptConnections(listener session.Listener) {
	fmt.Printf("DEBUG: Primary server accepting connections on %s\n", fts.config.Server.Address)

	for {
		select {
		case <-fts.ctx.Done():
			fmt.Printf("DEBUG: Primary server stopping connection acceptance\n")
			return
		default:
			// Accept incoming session
			clientSession, err := listener.Accept(fts.ctx)
			if err != nil {
				if fts.ctx.Err() != nil {
					return // Context cancelled, shutting down
				}
				fmt.Printf("Accept error: %v\n", err)
				continue
			}

			fmt.Printf("DEBUG: Primary server accepted client session with %d paths\n", len(clientSession.GetActivePaths()))

			// Handle client session in goroutine
			go fts.handleClientSession(clientSession)
		}
	}
}

// handleClientSession handles a single client session
func (fts *FileTransferServer) handleClientSession(clientSession session.Session) {
	defer clientSession.Close()

	clientID := fmt.Sprintf("client_%s", clientSession.GetActivePaths()[0].PathID)
	fmt.Printf("DEBUG: Primary server handling client session %s\n", clientID)

	// Setup multi-path if secondary address is configured
	if fts.config.Server.SecondaryAddress != "" {
		fts.setupMultiPath(clientSession, clientID)
	}

	// Assign session to server for this client
	fts.session = clientSession

	// Create chunk coordinator for this client session
	chunkCoordinator, err := NewChunkCoordinator(
		clientSession,
		fts.config.Performance.SecondaryPaths,
		fts.config.Server.FileDirectory,
		fts.config.Performance.ChunkSize,
		fts.config.Limits.MaxConcurrent,
	)
	if err != nil {
		fmt.Printf("❌ [%s] Failed to create chunk coordinator: %v\n", clientID, err)
		return
	}
	defer chunkCoordinator.Close()

	// Assign chunk coordinator to server for this client
	fts.chunkCoordinator = chunkCoordinator

	fmt.Printf("✅ [%s] Chunk coordinator created successfully\n", clientID)

	// Handle streams from this client
	for {
		select {
		case <-fts.ctx.Done():
			return
		default:
			stream, err := clientSession.AcceptStream(fts.ctx)
			if err != nil {
				if fts.ctx.Err() != nil {
					return
				}
				fmt.Printf("Stream error for client %s: %v\n", clientID, err)
				return
			}

			fmt.Printf("DEBUG: Primary server accepted stream %d from client %s\n", stream.StreamID(), clientID)
			go fts.handleRequestStream(stream)
		}
	}
}

// setupMultiPath sets up multi-path connection for a client
func (fts *FileTransferServer) setupMultiPath(clientSession session.Session, clientID string) {
	if fts.config.Server.SecondaryAddress == "" {
		return
	}

	fmt.Printf("🛤️  [%s] Setting up secondary path to %s...\n", clientID, fts.config.Server.SecondaryAddress)

	// Give the client a moment to set up its control frame handler
	time.Sleep(500 * time.Millisecond)

	err := clientSession.AddPath(fts.config.Server.SecondaryAddress)
	if err != nil {
		fmt.Printf("❌ [%s] Failed to add secondary path: %v\n", clientID, err)
		return
	}

	fmt.Printf("✅ [%s] AddPath request sent successfully\n", clientID)

	// Wait for secondary path to be established
	time.Sleep(2 * time.Second)

	// Update config with path ID if available
	if serverSession, ok := clientSession.(*session.ServerSession); ok {
		pathID := serverSession.GetPendingPathID(fts.config.Server.SecondaryAddress)
		if pathID != "" {
			fts.config.Performance.SecondaryPaths = []string{pathID}
			fmt.Printf("✅ [%s] Secondary path ID obtained: %s\n", clientID, pathID)
		}
	}
}

// handleIncomingRequests processes incoming file transfer requests
func (fts *FileTransferServer) handleIncomingRequests() {
	fmt.Printf("DEBUG: Server starting to handle incoming requests\n")
	for {
		select {
		case <-fts.ctx.Done():
			fmt.Printf("DEBUG: Server handleIncomingRequests stopping (context done)\n")
			return
		default:
			// Accept incoming streams for file requests
			stream, err := fts.session.AcceptStream(fts.ctx)
			if err != nil {
				// Check if it's a context cancellation or connection lost error
				if fts.ctx.Err() != nil {
					fmt.Printf("DEBUG: Server context cancelled, stopping request handler\n")
					return
				}
				// For connection errors, wait a bit longer before retrying
				time.Sleep(1 * time.Second)
				continue
			}

			fmt.Printf("DEBUG: Server accepted incoming stream %d, handling request\n", stream.StreamID())
			// Handle request stream in goroutine
			go fts.handleRequestStream(stream)
		}
	}
}

// handleRequestStream processes a file request from a single stream
func (fts *FileTransferServer) handleRequestStream(stream session.Stream) {
	defer stream.Close()

	// Read request data
	buffer := make([]byte, 4096)
	n, err := stream.Read(buffer)
	if err != nil {
		return
	}

	// Parse request
	var request map[string]interface{}
	if err := json.Unmarshal(buffer[:n], &request); err != nil {
		fts.sendErrorResponse(stream, "Invalid request format")
		return
	}

	// Check request type
	requestType, ok := request["type"].(string)
	if !ok {
		fts.sendErrorResponse(stream, "Missing request type")
		return
	}

	switch requestType {
	case "FILE_REQUEST":
		fts.handleFileRequest(stream, request)
	case "CHUNK_RETRY":
		fts.handleChunkRetryRequest(stream, request)
	default:
		fts.sendErrorResponse(stream, fmt.Sprintf("Unknown request type: %s", requestType))
	}
}

// handleFileRequest processes a file download request
func (fts *FileTransferServer) handleFileRequest(stream session.Stream, request map[string]interface{}) {
	// Extract filename
	filename, ok := request["filename"].(string)
	if !ok {
		fts.sendErrorResponse(stream, "Missing or invalid filename")
		return
	}

	// Validate filename
	if err := fts.validateFilename(filename); err != nil {
		fts.sendErrorResponse(stream, fmt.Sprintf("Invalid filename: %v", err))
		return
	}

	// Get client address (from stream path info)
	clientAddress := fts.getClientAddress(stream)

	// Check if request already exists for this client
	requestKey := fmt.Sprintf("%s:%s", clientAddress, filename)
	fts.requestsMutex.RLock()
	if _, exists := fts.activeRequests[requestKey]; exists {
		fts.requestsMutex.RUnlock()
		fts.sendErrorResponse(stream, "Request already in progress")
		return
	}
	fts.requestsMutex.RUnlock()

	// Analyze file and create metadata
	metadata, err := fts.analyzeFile(filename)
	if err != nil {
		fts.sendErrorResponse(stream, fmt.Sprintf("Failed to analyze file: %v", err))
		return
	}

	// Validate file size
	if metadata.Size > fts.config.Limits.MaxFileSize {
		fts.sendErrorResponse(stream, fmt.Sprintf("File too large: %d bytes (max: %d)", metadata.Size, fts.config.Limits.MaxFileSize))
		return
	}

	// Create request state
	requestState := &RequestState{
		ClientAddress: clientAddress,
		Filename:      filename,
		RequestedAt:   time.Now(),
		Metadata:      metadata,
		Status:        RequestStatusReceived,
		stream:        stream,
	}

	// Add to active requests
	fts.requestsMutex.Lock()
	fts.activeRequests[requestKey] = requestState
	fts.requestsMutex.Unlock()

	// Send metadata response
	err = fts.sendMetadataResponse(stream, metadata)
	if err != nil {
		fts.handleRequestError(requestKey, fmt.Errorf("failed to send metadata: %w", err))
		return
	}

	// Start file transfer
	fts.startFileTransfer(requestKey, requestState)
}

// validateFilename validates the requested filename
func (fts *FileTransferServer) validateFilename(filename string) error {
	// Check for empty filename
	if filename == "" {
		return fmt.Errorf("filename cannot be empty")
	}

	// Check for path traversal attempts
	if filepath.IsAbs(filename) {
		return fmt.Errorf("absolute paths not allowed")
	}

	cleanPath := filepath.Clean(filename)
	if cleanPath != filename || cleanPath == "." || cleanPath == ".." {
		return fmt.Errorf("invalid path: %s", filename)
	}

	// Check for directory traversal
	if filepath.Dir(cleanPath) != "." {
		return fmt.Errorf("subdirectories not allowed: %s", filename)
	}

	// Check file extension if restrictions are configured
	if !fts.config.IsFileAllowed(filename) {
		return fmt.Errorf("file extension not allowed: %s", filepath.Ext(filename))
	}

	return nil
}

// analyzeFile analyzes a file and creates metadata
func (fts *FileTransferServer) analyzeFile(filename string) (*types.FileMetadata, error) {
	// Construct full file path
	filePath := filepath.Join(fts.config.Server.FileDirectory, filename)

	// Check if file exists and get info
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("file not found: %s", filename)
		}
		return nil, fmt.Errorf("failed to access file: %w", err)
	}

	// Check if it's a regular file
	if !fileInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", filename)
	}

	// Create metadata with configured chunk size
	chunkSize := fts.config.Performance.ChunkSize
	metadata := types.NewFileMetadata(filename, fileInfo.Size(), chunkSize)
	metadata.CreatedAt = fileInfo.ModTime()
	metadata.ModifiedAt = fileInfo.ModTime()

	// Calculate file checksum
	checksum, err := fts.calculateFileChecksum(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate file checksum: %w", err)
	}
	metadata.Checksum = checksum

	// Set additional metadata
	metadata.ContentType = fts.getContentType(filename)
	metadata.Permissions = fileInfo.Mode().String()

	// Validate metadata
	if err := metadata.Validate(); err != nil {
		return nil, fmt.Errorf("invalid metadata: %w", err)
	}

	return metadata, nil
}

// calculateFileChecksum calculates SHA256 checksum of the entire file
func (fts *FileTransferServer) calculateFileChecksum(filePath string) (string, error) {
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

// getContentType determines the content type based on file extension
func (fts *FileTransferServer) getContentType(filename string) string {
	ext := filepath.Ext(filename)

	// Basic content type mapping
	contentTypes := map[string]string{
		".txt":  "text/plain",
		".json": "application/json",
		".xml":  "application/xml",
		".pdf":  "application/pdf",
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".png":  "image/png",
		".gif":  "image/gif",
		".mp4":  "video/mp4",
		".mp3":  "audio/mpeg",
		".zip":  "application/zip",
		".tar":  "application/x-tar",
		".gz":   "application/gzip",
	}

	if contentType, exists := contentTypes[ext]; exists {
		return contentType
	}

	return "application/octet-stream" // Default binary type
}

// getClientAddress extracts client address from stream
func (fts *FileTransferServer) getClientAddress(stream session.Stream) string {
	// For now, use stream ID as a unique identifier
	// In a real implementation, this would extract the actual client address
	return fmt.Sprintf("client_%d", stream.StreamID())
}

// sendMetadataResponse sends file metadata to the client
func (fts *FileTransferServer) sendMetadataResponse(stream session.Stream, metadata *types.FileMetadata) error {
	response := map[string]interface{}{
		"type":         "FILE_METADATA",
		"filename":     metadata.Filename,
		"size":         metadata.Size,
		"chunk_size":   metadata.ChunkSize,
		"total_chunks": metadata.TotalChunks,
		"checksum":     metadata.Checksum,
		"content_type": metadata.ContentType,
		"created_at":   metadata.CreatedAt.Unix(),
		"modified_at":  metadata.ModifiedAt.Unix(),
	}

	responseData, err := json.Marshal(response)
	if err != nil {
		return err
	}

	_, err = stream.Write(responseData)
	return err
}

// sendErrorResponse sends an error response to the client
func (fts *FileTransferServer) sendErrorResponse(stream session.Stream, errorMessage string) {
	response := map[string]interface{}{
		"type":  "ERROR",
		"error": errorMessage,
	}

	responseData, _ := json.Marshal(response)
	stream.Write(responseData)
}

// startFileTransfer initiates the file transfer using the chunk coordinator
func (fts *FileTransferServer) startFileTransfer(requestKey string, requestState *RequestState) {
	// Update request status
	fts.requestsMutex.Lock()
	requestState.Status = RequestStatusPreparing
	fts.requestsMutex.Unlock()

	// Start transfer with chunk coordinator
	transferState, err := fts.chunkCoordinator.StartFileTransfer(
		requestState.stream,
		requestState.Filename,
		requestState.ClientAddress,
	)
	if err != nil {
		fts.handleRequestError(requestKey, fmt.Errorf("failed to start transfer: %w", err))
		return
	}

	// Update request state
	fts.requestsMutex.Lock()
	requestState.TransferState = transferState
	requestState.Status = RequestStatusTransferring
	fts.requestsMutex.Unlock()

	// Monitor transfer progress
	go fts.monitorTransferProgress(requestKey, requestState)
}

// handleChunkRetryRequest processes a chunk retry request from client
func (fts *FileTransferServer) handleChunkRetryRequest(stream session.Stream, request map[string]interface{}) {
	// Extract request parameters
	filename, ok := request["filename"].(string)
	if !ok {
		fts.sendErrorResponse(stream, "Missing filename")
		return
	}

	chunkID, ok := request["chunk_id"].(float64)
	if !ok {
		fts.sendErrorResponse(stream, "Missing chunk_id")
		return
	}

	sequenceNum, ok := request["sequence_num"].(float64)
	if !ok {
		fts.sendErrorResponse(stream, "Missing sequence_num")
		return
	}

	// Get client address
	clientAddress := fts.getClientAddress(stream)
	requestKey := fmt.Sprintf("%s:%s", clientAddress, filename)

	// Find active request
	fts.requestsMutex.RLock()
	requestState, exists := fts.activeRequests[requestKey]
	fts.requestsMutex.RUnlock()

	if !exists {
		fts.sendErrorResponse(stream, "No active transfer found")
		return
	}

	// Handle retry through chunk coordinator
	// This would typically involve re-sending the specific chunk
	// For now, we'll log the retry request
	fmt.Printf("Retry requested for file %s, chunk %d (sequence %d) from client %s\n",
		filename, uint32(chunkID), uint32(sequenceNum), requestState.ClientAddress)

	// Send acknowledgment
	response := map[string]interface{}{
		"type":         "RETRY_ACK",
		"filename":     filename,
		"chunk_id":     uint32(chunkID),
		"sequence_num": uint32(sequenceNum),
	}

	responseData, _ := json.Marshal(response)
	stream.Write(responseData)
}

// monitorTransferProgress monitors the progress of a file transfer
func (fts *FileTransferServer) monitorTransferProgress(requestKey string, requestState *RequestState) {
	ticker := time.NewTicker(5 * time.Second) // Check every 5 seconds
	defer ticker.Stop()

	for {
		select {
		case <-fts.ctx.Done():
			return
		case <-ticker.C:
			// Check transfer status
			if requestState.TransferState == nil {
				continue
			}

			requestState.TransferState.mutex.RLock()
			status := requestState.TransferState.Status
			totalChunks := requestState.TransferState.Metadata.TotalChunks
			completedChunks := len(requestState.TransferState.CompletedChunks)
			requestState.TransferState.mutex.RUnlock()

			// Update request status based on transfer status
			fts.requestsMutex.Lock()
			switch status {
			case TransferStatusCompleted:
				requestState.Status = RequestStatusCompleted
				fts.requestsMutex.Unlock()

				// Clean up completed request after a delay
				go func() {
					time.Sleep(5 * time.Minute)
					fts.cleanupRequest(requestKey)
				}()
				return

			case TransferStatusFailed:
				requestState.Status = RequestStatusFailed
				requestState.ErrorMessage = "Transfer failed"
				fts.requestsMutex.Unlock()
				return

			case TransferStatusCancelled:
				requestState.Status = RequestStatusCancelled
				fts.requestsMutex.Unlock()
				return

			default:
				// Still transferring, log progress
				progress := float64(completedChunks) / float64(totalChunks) * 100
				fmt.Printf("Transfer progress for %s: %.1f%% (%d/%d chunks)\n",
					requestState.Filename, progress, completedChunks, totalChunks)
			}
			fts.requestsMutex.Unlock()
		}
	}
}

// monitorRequests monitors all active requests and handles cleanup
func (fts *FileTransferServer) monitorRequests() {
	ticker := time.NewTicker(60 * time.Second) // Check every minute
	defer ticker.Stop()

	for {
		select {
		case <-fts.ctx.Done():
			return
		case <-ticker.C:
			fts.cleanupStaleRequests()
		}
	}
}

// cleanupStaleRequests removes stale or completed requests
func (fts *FileTransferServer) cleanupStaleRequests() {
	fts.requestsMutex.Lock()
	defer fts.requestsMutex.Unlock()

	now := time.Now()
	for requestKey, requestState := range fts.activeRequests {
		// Remove requests that are too old or completed
		shouldRemove := false

		switch requestState.Status {
		case RequestStatusCompleted, RequestStatusFailed, RequestStatusCancelled:
			// Remove completed/failed requests after 10 minutes
			if now.Sub(requestState.RequestedAt) > 10*time.Minute {
				shouldRemove = true
			}
		case RequestStatusReceived, RequestStatusPreparing:
			// Remove stuck requests after 5 minutes
			if now.Sub(requestState.RequestedAt) > 5*time.Minute {
				shouldRemove = true
			}
		case RequestStatusTransferring:
			// Remove stalled transfers after 30 minutes
			if now.Sub(requestState.RequestedAt) > 30*time.Minute {
				shouldRemove = true
			}
		}

		if shouldRemove {
			delete(fts.activeRequests, requestKey)
		}
	}
}

// handleRequestError handles errors during request processing
func (fts *FileTransferServer) handleRequestError(requestKey string, err error) {
	fts.requestsMutex.Lock()
	defer fts.requestsMutex.Unlock()

	requestState, exists := fts.activeRequests[requestKey]
	if !exists {
		return
	}

	requestState.Status = RequestStatusFailed
	requestState.ErrorMessage = err.Error()
}

// cleanupRequest removes a request from active requests
func (fts *FileTransferServer) cleanupRequest(requestKey string) {
	fts.requestsMutex.Lock()
	defer fts.requestsMutex.Unlock()

	delete(fts.activeRequests, requestKey)
}

// GetActiveRequests returns information about active requests
func (fts *FileTransferServer) GetActiveRequests() map[string]*RequestState {
	fts.requestsMutex.RLock()
	defer fts.requestsMutex.RUnlock()

	// Create a copy to avoid race conditions
	requests := make(map[string]*RequestState)
	for key, state := range fts.activeRequests {
		requests[key] = state
	}

	return requests
}

// GetRequestStatus returns the status of a specific request
func (fts *FileTransferServer) GetRequestStatus(clientAddress, filename string) (*RequestState, error) {
	requestKey := fmt.Sprintf("%s:%s", clientAddress, filename)

	fts.requestsMutex.RLock()
	defer fts.requestsMutex.RUnlock()

	requestState, exists := fts.activeRequests[requestKey]
	if !exists {
		return nil, fmt.Errorf("no request found for client %s, file %s", clientAddress, filename)
	}

	return requestState, nil
}

// Close closes the file transfer server and cleans up resources
// This method is kept for backward compatibility and calls Stop()
func (fts *FileTransferServer) Close() error {
	return fts.Stop()
}

// String method for RequestStatus
func (rs RequestStatus) String() string {
	switch rs {
	case RequestStatusReceived:
		return "RECEIVED"
	case RequestStatusPreparing:
		return "PREPARING"
	case RequestStatusTransferring:
		return "TRANSFERRING"
	case RequestStatusCompleted:
		return "COMPLETED"
	case RequestStatusFailed:
		return "FAILED"
	case RequestStatusCancelled:
		return "CANCELLED"
	default:
		return "UNKNOWN"
	}
}
