package server

import (
	"context"
	"encoding/json"
	"fmt"
	"ftpa/internal/config"
	"ftpa/internal/types"
	"os"
	"path/filepath"
	"sync"
	"time"

	kwik "kwik/pkg"
	"kwik/pkg/session"
)

// SecondaryFileTransferServer is a secondary file transfer server that handles chunk requests
type SecondaryFileTransferServer struct {
	config           *config.ServerConfiguration // Server configuration
	session          session.Session             // KWIK session for communication
	listener         session.Listener            // KWIK listener for accepting connections
	activeCommands   map[string]*CommandState    // Active commands by command ID
	commandsMutex    sync.RWMutex                // Protects activeCommands map
	ctx              context.Context             // Context for cancellation
	cancel           context.CancelFunc          // Cancel function
	isRunning        bool                        // Whether the server is running
	runningMutex     sync.RWMutex                // Protects isRunning flag
	rawPacketHandler *RawPacketHandler           // Handler for raw packet transmissions
}

// CommandState tracks the state of a chunk command
type CommandState struct {
	Command      *types.ChunkCommand // The original command
	ReceivedAt   time.Time           // When the command was received
	Status       CommandStatus       // Current command status
	ErrorMessage string              // Error message if command failed
	ChunkData    *types.FileChunk    // The chunk data if successfully read
	SentAt       time.Time           // When the chunk was sent (if applicable)
}

// CommandStatus represents the current status of a command
type CommandStatus int

const (
	CommandStatusReceived  CommandStatus = iota // Command received, processing
	CommandStatusReading                        // Reading chunk from file
	CommandStatusSending                        // Sending chunk to client
	CommandStatusCompleted                      // Command completed successfully
	CommandStatusFailed                         // Command failed
)

// NewSecondaryFileTransferServer creates a new secondary file transfer server
func NewSecondaryFileTransferServer(serverConfig *config.ServerConfiguration) (*SecondaryFileTransferServer, error) {
	// Validate configuration
	if serverConfig == nil {
		return nil, fmt.Errorf("server configuration cannot be nil")
	}

	// Validate configuration
	if err := serverConfig.Validate(); err != nil {
		return nil, fmt.Errorf("invalid server configuration: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	server := &SecondaryFileTransferServer{
		config:         serverConfig,
		activeCommands: make(map[string]*CommandState),
		ctx:            ctx,
		cancel:         cancel,
		isRunning:      false,
	}

	return server, nil
}

// Start starts the secondary file transfer server
func (sfts *SecondaryFileTransferServer) Start() error {
	sfts.runningMutex.Lock()
	defer sfts.runningMutex.Unlock()

	if sfts.isRunning {
		return fmt.Errorf("secondary server is already running")
	}

	// Create KWIK server listener
	err := sfts.createKwikServer()
	if err != nil {
		return fmt.Errorf("failed to create KWIK server: %w", err)
	}

	sfts.isRunning = true

	// Start background goroutines
	go sfts.monitorCommands()

	fmt.Printf("Secondary file transfer server started on %s\n", sfts.config.Server.Address)
	return nil
}

// Stop stops the secondary file transfer server
func (sfts *SecondaryFileTransferServer) Stop() error {
	sfts.runningMutex.Lock()
	defer sfts.runningMutex.Unlock()

	if !sfts.isRunning {
		return nil // Already stopped
	}

	// Cancel context to stop all goroutines
	sfts.cancel()

	// Stop raw packet handler
	if sfts.rawPacketHandler != nil {
		sfts.rawPacketHandler.Stop()
		sfts.rawPacketHandler = nil
	}

	// Close KWIK listener
	if sfts.listener != nil {
		sfts.listener.Close()
		sfts.listener = nil
	}

	// Close KWIK session
	if sfts.session != nil {
		sfts.session.Close()
		sfts.session = nil
	}

	// Clean up all active commands
	sfts.commandsMutex.Lock()
	sfts.activeCommands = make(map[string]*CommandState)
	sfts.commandsMutex.Unlock()

	sfts.isRunning = false

	fmt.Printf("Secondary file transfer server stopped\n")
	return nil
}

// IsRunning returns whether the server is currently running
func (sfts *SecondaryFileTransferServer) IsRunning() bool {
	sfts.runningMutex.RLock()
	defer sfts.runningMutex.RUnlock()
	return sfts.isRunning
}

// createKwikServer creates and configures a KWIK server listener
func (sfts *SecondaryFileTransferServer) createKwikServer() error {
	// Configure KWIK for multi-path support
	kwikConfig := kwik.DefaultConfig()
	kwikConfig.MaxPathsPerSession = 10 // Allow multiple paths per session

	// Create KWIK listener
	listener, err := kwik.Listen(sfts.config.Server.Address, kwikConfig)
	if err != nil {
		return fmt.Errorf("failed to create KWIK listener: %w", err)
	}

	// Store listener for cleanup
	sfts.listener = listener

	// Start accepting connections in background
	go sfts.acceptConnections(listener)

	// Return nil as we handle sessions in acceptConnections
	// This follows KWIK patterns for secondary servers
	return nil
}

// acceptConnections accepts incoming KWIK connections from primary servers
func (sfts *SecondaryFileTransferServer) acceptConnections(listener session.Listener) {
	fmt.Printf("DEBUG: Secondary server accepting connections on %s\n", sfts.config.Server.Address)
	fmt.Printf("DEBUG: Waiting for secondary path connections from primary servers...\n")

	for {
		select {
		case <-sfts.ctx.Done():
			fmt.Printf("DEBUG: Secondary server stopping connection acceptance\n")
			return
		default:
			// Accept incoming session (secondary path connection)
			primarySession, err := listener.Accept(sfts.ctx)
			if err != nil {
				if sfts.ctx.Err() != nil {
					return // Context cancelled, shutting down
				}
				fmt.Printf("Accept error: %v\n", err)
				continue
			}

			fmt.Printf("DEBUG: Secondary server accepted connection from primary server with %d paths\n", len(primarySession.GetActivePaths()))

			// Handle primary server session in goroutine
			go sfts.handlePrimaryServerSession(primarySession)
		}
	}
}

// handlePrimaryServerSession handles a session from a primary server
func (sfts *SecondaryFileTransferServer) handlePrimaryServerSession(primarySession session.Session) {
	defer primarySession.Close()

	serverID := fmt.Sprintf("primary_%d", time.Now().UnixNano())
	fmt.Printf("DEBUG: Secondary server handling session from primary server %s\n", serverID)

	// Assign session to server for this primary server
	sfts.session = primarySession

	// Create and initialize raw packet handler for this session
	sfts.rawPacketHandler = NewRawPacketHandler(sfts, primarySession)

	// Start raw packet handler
	if err := sfts.rawPacketHandler.Start(); err != nil {
		fmt.Printf("❌ Failed to start raw packet handler: %v\n", err)
		return
	}
	defer sfts.rawPacketHandler.Stop()

	fmt.Printf("✅ Secondary server ready to handle commands from primary server %s\n", serverID)

	// Handle streams from primary server (chunk commands)
	for {
		select {
		case <-sfts.ctx.Done():
			return
		default:
			stream, err := primarySession.AcceptStream(sfts.ctx)
			if err != nil {
				if sfts.ctx.Err() != nil {
					return
				}
				fmt.Printf("Stream error from primary server %s: %v\n", serverID, err)
				return
			}

			fmt.Printf("DEBUG: Secondary server accepted stream %d from primary server %s\n", stream.StreamID(), serverID)
			go sfts.handleCommandStream(stream)
		}
	}
}

// handleIncomingCommands processes incoming chunk commands from the primary server
func (sfts *SecondaryFileTransferServer) handleIncomingCommands() {
	fmt.Printf("DEBUG: Secondary server starting to handle incoming commands\n")
	for {
		select {
		case <-sfts.ctx.Done():
			fmt.Printf("DEBUG: Secondary server handleIncomingCommands stopping (context done)\n")
			return
		default:
			// Accept incoming streams for chunk commands
			stream, err := sfts.session.AcceptStream(sfts.ctx)
			if err != nil {
				// Check if it's a context cancellation or connection lost error
				if sfts.ctx.Err() != nil {
					fmt.Printf("DEBUG: Secondary server context cancelled, stopping command handler\n")
					return
				}
				// For connection errors, wait a bit longer before retrying
				time.Sleep(1 * time.Second)
				continue
			}

			fmt.Printf("DEBUG: Secondary server accepted incoming stream %d, handling command\n", stream.StreamID())
			// Handle command stream in goroutine
			go sfts.handleCommandStream(stream)
		}
	}
}

// handleCommandStream processes a chunk command from a single stream
func (sfts *SecondaryFileTransferServer) handleCommandStream(stream session.Stream) {
	defer stream.Close()

	// Read command data
	buffer := make([]byte, 4096)
	n, err := stream.Read(buffer)
	if err != nil {
		return
	}

	// Parse command
	var command types.ChunkCommand
	if err := json.Unmarshal(buffer[:n], &command); err != nil {
		sfts.sendErrorResponse(stream, "Invalid command format")
		return
	}

	// Validate command
	if err := command.Validate(); err != nil {
		sfts.sendErrorResponse(stream, fmt.Sprintf("Invalid command: %v", err))
		return
	}

	// Process command based on type
	switch command.Command {
	case "SEND_CHUNK":
		sfts.handleSendChunkCommand(stream, &command)
	default:
		sfts.sendErrorResponse(stream, fmt.Sprintf("Unknown command type: %s", command.Command))
	}
}

// handleSendChunkCommand processes a SEND_CHUNK command
func (sfts *SecondaryFileTransferServer) handleSendChunkCommand(stream session.Stream, command *types.ChunkCommand) {
	// Create command ID for tracking
	commandID := fmt.Sprintf("%s_%d_%d", command.Filename, command.ChunkID, command.SequenceNum)

	// Check if command is already being processed
	sfts.commandsMutex.RLock()
	if _, exists := sfts.activeCommands[commandID]; exists {
		sfts.commandsMutex.RUnlock()
		sfts.sendErrorResponse(stream, "Command already in progress")
		return
	}
	sfts.commandsMutex.RUnlock()

	// Create command state
	commandState := &CommandState{
		Command:    command,
		ReceivedAt: time.Now(),
		Status:     CommandStatusReceived,
	}

	// Add to active commands
	sfts.commandsMutex.Lock()
	sfts.activeCommands[commandID] = commandState
	sfts.commandsMutex.Unlock()

	// Send acknowledgment
	err := sfts.sendCommandAck(stream, command)
	if err != nil {
		sfts.handleCommandError(commandID, fmt.Errorf("failed to send acknowledgment: %w", err))
		return
	}

	// Process the chunk command
	sfts.processChunkCommand(commandID, commandState)
}

// processChunkCommand processes a chunk command by reading and sending the chunk
func (sfts *SecondaryFileTransferServer) processChunkCommand(commandID string, commandState *CommandState) {
	command := commandState.Command

	// Update status to reading
	sfts.commandsMutex.Lock()
	commandState.Status = CommandStatusReading
	sfts.commandsMutex.Unlock()

	// Read chunk from file
	chunk, err := sfts.readChunkFromFile(command)
	if err != nil {
		sfts.handleCommandError(commandID, fmt.Errorf("failed to read chunk: %w", err))
		return
	}

	// Store chunk data
	sfts.commandsMutex.Lock()
	commandState.ChunkData = chunk
	commandState.Status = CommandStatusSending
	sfts.commandsMutex.Unlock()

	// Send chunk to client
	err = sfts.sendChunkToClient(chunk, command)
	if err != nil {
		sfts.handleCommandError(commandID, fmt.Errorf("failed to send chunk: %w", err))
		return
	}

	// Update status to completed
	sfts.commandsMutex.Lock()
	commandState.Status = CommandStatusCompleted
	commandState.SentAt = time.Now()
	sfts.commandsMutex.Unlock()

	// Notify primary server of completion
	sfts.notifyPrimaryServer(command, true, "")

	// Clean up command after a delay
	go func() {
		time.Sleep(5 * time.Minute)
		sfts.cleanupCommand(commandID)
	}()
}

// readChunkFromFile reads a specific chunk from the file
func (sfts *SecondaryFileTransferServer) readChunkFromFile(command *types.ChunkCommand) (*types.FileChunk, error) {
	// Validate filename
	if err := sfts.validateFilename(command.Filename); err != nil {
		return nil, fmt.Errorf("invalid filename: %w", err)
	}

	// Construct full file path
	filePath := filepath.Join(sfts.config.Server.FileDirectory, command.Filename)

	// Check if file exists
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("file not found: %s", command.Filename)
		}
		return nil, fmt.Errorf("failed to access file: %w", err)
	}

	// Check if it's a regular file
	if !fileInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", command.Filename)
	}

	// Open file
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// Seek to chunk position
	_, err = file.Seek(command.StartOffset, 0)
	if err != nil {
		return nil, err
	}

	// Read chunk data
	data := make([]byte, command.Size)
	n, err := file.Read(data)
	if err != nil {
		return nil, err
	}

	// Trim data to actual bytes read
	data = data[:n]

	// Calculate total chunks and determine if this is the last chunk
	chunkSize := sfts.config.Performance.ChunkSize
	totalChunks := uint32((fileInfo.Size() + int64(chunkSize) - 1) / int64(chunkSize))
	isLast := command.SequenceNum == totalChunks-1

	// Create chunk
	chunk := types.NewFileChunk(
		command.ChunkID,
		command.SequenceNum,
		data,
		command.Filename,
		command.StartOffset,
		totalChunks,
		isLast,
	)

	// Validate checksum if provided
	if command.Checksum != "" {
		if err := chunk.ValidateChecksum(); err != nil {
			return nil, fmt.Errorf("chunk checksum validation failed: %w", err)
		}

		// Compare with expected checksum
		if chunk.Checksum != command.Checksum {
			return nil, fmt.Errorf("chunk checksum mismatch: expected %s, got %s", command.Checksum, chunk.Checksum)
		}
	}

	return chunk, nil
}

// sendChunkToClient sends a chunk to the client via a stream
func (sfts *SecondaryFileTransferServer) sendChunkToClient(chunk *types.FileChunk, command *types.ChunkCommand) error {
	// Open a new stream to send the chunk
	stream, err := sfts.session.OpenStreamSync(sfts.ctx)
	if err != nil {
		return err
	}
	defer stream.Close()

	// Add client address metadata if available
	if clientAddress, exists := command.Metadata["client_address"]; exists {
		chunk.Metadata["client_address"] = clientAddress
	}

	// Add command metadata
	chunk.Metadata["command_id"] = fmt.Sprintf("%d", command.ChunkID)
	chunk.Metadata["sender_id"] = "secondary" // TODO: Use configurable server ID

	// Serialize chunk
	chunkData, err := json.Marshal(chunk)
	if err != nil {
		return err
	}

	// Send chunk data
	_, err = stream.Write(chunkData)
	return err
}

// notifyPrimaryServer notifies the primary server about command completion
func (sfts *SecondaryFileTransferServer) notifyPrimaryServer(command *types.ChunkCommand, success bool, errorMessage string) {
	// Create response command
	var responseCommand *types.ChunkCommand
	if success {
		responseCommand = types.NewChunkCommand("CHUNK_SENT", command.ChunkID, command.SequenceNum, command.Filename, command.StartOffset, command.Size)
		responseCommand.Checksum = command.Checksum
		responseCommand.ChecksumType = command.ChecksumType
	} else {
		responseCommand = types.NewChunkCommand("CHUNK_ERROR", command.ChunkID, command.SequenceNum, command.Filename, command.StartOffset, command.Size)
		responseCommand.Metadata["error"] = errorMessage
	}

	// Set sender and receiver IDs
	responseCommand.SenderID = "secondary" // TODO: Use configurable server ID
	responseCommand.ReceiverID = "primary" // TODO: Use configurable primary server ID

	// Copy relevant metadata
	if clientAddress, exists := command.Metadata["client_address"]; exists {
		responseCommand.Metadata["client_address"] = clientAddress
	}

	// Serialize response
	responseData, err := json.Marshal(responseCommand)
	if err != nil {
		fmt.Printf("Failed to serialize response command: %v\n", err)
		return
	}

	// NOTE: Secondary server cannot SendRawData; skipping notification for now.
	fmt.Printf("Secondary notify suppressed: %s\n", string(responseData))
}

// validateFilename validates the requested filename
func (sfts *SecondaryFileTransferServer) validateFilename(filename string) error {
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
	if !sfts.config.IsFileAllowed(filename) {
		return fmt.Errorf("file extension not allowed: %s", filepath.Ext(filename))
	}

	return nil
}

// sendCommandAck sends an acknowledgment for a received command
func (sfts *SecondaryFileTransferServer) sendCommandAck(stream session.Stream, command *types.ChunkCommand) error {
	response := map[string]interface{}{
		"type":         "COMMAND_ACK",
		"command":      command.Command,
		"chunk_id":     command.ChunkID,
		"sequence_num": command.SequenceNum,
		"filename":     command.Filename,
		"server_id":    "secondary", // TODO: Use configurable server ID
	}

	responseData, err := json.Marshal(response)
	if err != nil {
		return err
	}

	_, err = stream.Write(responseData)
	return err
}

// sendErrorResponse sends an error response to the client
func (sfts *SecondaryFileTransferServer) sendErrorResponse(stream session.Stream, errorMessage string) {
	response := map[string]interface{}{
		"type":      "ERROR",
		"error":     errorMessage,
		"server_id": "secondary", // TODO: Use configurable server ID
	}

	responseData, _ := json.Marshal(response)
	stream.Write(responseData)
}

// handleCommandError handles errors during command processing
func (sfts *SecondaryFileTransferServer) handleCommandError(commandID string, err error) {
	sfts.commandsMutex.Lock()
	defer sfts.commandsMutex.Unlock()

	commandState, exists := sfts.activeCommands[commandID]
	if !exists {
		return
	}

	commandState.Status = CommandStatusFailed
	commandState.ErrorMessage = err.Error()

	// Notify primary server of error
	go sfts.notifyPrimaryServer(commandState.Command, false, err.Error())
}

// monitorCommands monitors active commands and handles cleanup
func (sfts *SecondaryFileTransferServer) monitorCommands() {
	ticker := time.NewTicker(60 * time.Second) // Check every minute
	defer ticker.Stop()

	for {
		select {
		case <-sfts.ctx.Done():
			return
		case <-ticker.C:
			sfts.cleanupStaleCommands()
		}
	}
}

// cleanupStaleCommands removes stale or completed commands
func (sfts *SecondaryFileTransferServer) cleanupStaleCommands() {
	sfts.commandsMutex.Lock()
	defer sfts.commandsMutex.Unlock()

	now := time.Now()
	for commandID, commandState := range sfts.activeCommands {
		// Remove commands that are too old or completed
		shouldRemove := false

		switch commandState.Status {
		case CommandStatusCompleted, CommandStatusFailed:
			// Remove completed/failed commands after 10 minutes
			if now.Sub(commandState.ReceivedAt) > 10*time.Minute {
				shouldRemove = true
			}
		case CommandStatusReceived, CommandStatusReading:
			// Remove stuck commands after 5 minutes
			if now.Sub(commandState.ReceivedAt) > 5*time.Minute {
				shouldRemove = true
			}
		case CommandStatusSending:
			// Remove stalled sends after 2 minutes
			if now.Sub(commandState.ReceivedAt) > 2*time.Minute {
				shouldRemove = true
			}
		}

		if shouldRemove {
			delete(sfts.activeCommands, commandID)
		}
	}
}

// cleanupCommand removes a command from active commands
func (sfts *SecondaryFileTransferServer) cleanupCommand(commandID string) {
	sfts.commandsMutex.Lock()
	defer sfts.commandsMutex.Unlock()

	delete(sfts.activeCommands, commandID)
}

// GetActiveCommands returns information about active commands
func (sfts *SecondaryFileTransferServer) GetActiveCommands() map[string]*CommandState {
	sfts.commandsMutex.RLock()
	defer sfts.commandsMutex.RUnlock()

	// Create a copy to avoid race conditions
	commands := make(map[string]*CommandState)
	for key, state := range sfts.activeCommands {
		commands[key] = state
	}

	return commands
}

// GetCommandStatus returns the status of a specific command
func (sfts *SecondaryFileTransferServer) GetCommandStatus(commandID string) (*CommandState, error) {
	sfts.commandsMutex.RLock()
	defer sfts.commandsMutex.RUnlock()

	commandState, exists := sfts.activeCommands[commandID]
	if !exists {
		return nil, fmt.Errorf("no command found with ID: %s", commandID)
	}

	return commandState, nil
}

// GetRawPacketHandler returns the raw packet handler instance
func (sfts *SecondaryFileTransferServer) GetRawPacketHandler() *RawPacketHandler {
	return sfts.rawPacketHandler
}

// IsRawPacketHandlerRunning returns whether the raw packet handler is running
func (sfts *SecondaryFileTransferServer) IsRawPacketHandlerRunning() bool {
	if sfts.rawPacketHandler == nil {
		return false
	}
	return sfts.rawPacketHandler.IsRunning()
}

// Close closes the secondary file transfer server and cleans up resources
// This method is kept for backward compatibility and calls Stop()
func (sfts *SecondaryFileTransferServer) Close() error {
	return sfts.Stop()
}

// String method for CommandStatus
func (cs CommandStatus) String() string {
	switch cs {
	case CommandStatusReceived:
		return "RECEIVED"
	case CommandStatusReading:
		return "READING"
	case CommandStatusSending:
		return "SENDING"
	case CommandStatusCompleted:
		return "COMPLETED"
	case CommandStatusFailed:
		return "FAILED"
	default:
		return "UNKNOWN"
	}
}
