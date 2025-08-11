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

// SecondaryFileHandler handles chunk commands from the primary server
type SecondaryFileHandler struct {
	session           Session                    // KWIK session for communication
	fileDirectory     string                     // Directory containing files to serve
	chunkSize         int32                      // Size of each chunk in bytes
	activeCommands    map[string]*CommandState   // Active commands by command ID
	commandsMutex     sync.RWMutex               // Protects activeCommands map
	ctx               context.Context            // Context for cancellation
	cancel            context.CancelFunc         // Cancel function
	primaryServerID   string                     // ID of the primary server
	serverID          string                     // ID of this secondary server
	rawPacketHandler  *RawPacketHandler          // Handler for raw packet transmissions
}

// CommandState tracks the state of a chunk command
type CommandState struct {
	Command       *ChunkCommand     // The original command
	ReceivedAt    time.Time         // When the command was received
	Status        CommandStatus     // Current command status
	ErrorMessage  string            // Error message if command failed
	ChunkData     *FileChunk        // The chunk data if successfully read
	SentAt        time.Time         // When the chunk was sent (if applicable)
}

// CommandStatus represents the current status of a command
type CommandStatus int

const (
	CommandStatusReceived CommandStatus = iota // Command received, processing
	CommandStatusReading                       // Reading chunk from file
	CommandStatusSending                       // Sending chunk to client
	CommandStatusCompleted                     // Command completed successfully
	CommandStatusFailed                        // Command failed
)

// SecondaryFileHandlerConfig contains configuration for the secondary file handler
type SecondaryFileHandlerConfig struct {
	FileDirectory   string // Directory containing files to serve
	ChunkSize       int32  // Size of each chunk
	PrimaryServerID string // ID of the primary server
	ServerID        string // ID of this secondary server
}

// NewSecondaryFileHandler creates a new secondary file handler
func NewSecondaryFileHandler(session Session, config *SecondaryFileHandlerConfig) (*SecondaryFileHandler, error) {
	// Validate parameters
	if session == nil {
		return nil, fmt.Errorf("session cannot be nil")
	}

	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	if config.FileDirectory == "" {
		return nil, fmt.Errorf("file directory cannot be empty")
	}

	// Verify file directory exists
	if _, err := os.Stat(config.FileDirectory); os.IsNotExist(err) {
		return nil, fmt.Errorf("file directory does not exist: %s", config.FileDirectory)
	}

	// Set defaults
	if config.ChunkSize <= 0 {
		config.ChunkSize = 64 * 1024 // Default 64KB chunks
	}

	if config.ServerID == "" {
		config.ServerID = "secondary"
	}

	if config.PrimaryServerID == "" {
		config.PrimaryServerID = "primary"
	}

	ctx, cancel := context.WithCancel(context.Background())

	handler := &SecondaryFileHandler{
		session:         session,
		fileDirectory:   config.FileDirectory,
		chunkSize:       config.ChunkSize,
		activeCommands:  make(map[string]*CommandState),
		ctx:             ctx,
		cancel:          cancel,
		primaryServerID: config.PrimaryServerID,
		serverID:        config.ServerID,
	}

	// Create and initialize raw packet handler
	handler.rawPacketHandler = NewRawPacketHandler(handler, session)

	// Start background goroutines
	go handler.handleIncomingCommands()
	go handler.monitorCommands()

	// Start raw packet handler
	if err := handler.rawPacketHandler.Start(); err != nil {
		return nil, fmt.Errorf("failed to start raw packet handler: %w", err)
	}

	return handler, nil
}

// handleIncomingCommands processes incoming chunk commands from the primary server
func (sfh *SecondaryFileHandler) handleIncomingCommands() {
	for {
		select {
		case <-sfh.ctx.Done():
			return
		default:
			// Accept incoming streams for chunk commands
			stream, err := sfh.session.AcceptStream(sfh.ctx)
			if err != nil {
				time.Sleep(100 * time.Millisecond)
				continue
			}

			// Handle command stream in goroutine
			go sfh.handleCommandStream(stream)
		}
	}
}

// handleCommandStream processes a chunk command from a single stream
func (sfh *SecondaryFileHandler) handleCommandStream(stream Stream) {
	defer stream.Close()

	// Read command data
	buffer := make([]byte, 4096)
	n, err := stream.Read(buffer)
	if err != nil {
		return
	}

	// Parse command
	var command ChunkCommand
	if err := json.Unmarshal(buffer[:n], &command); err != nil {
		sfh.sendErrorResponse(stream, "Invalid command format")
		return
	}

	// Validate command
	if err := command.Validate(); err != nil {
		sfh.sendErrorResponse(stream, fmt.Sprintf("Invalid command: %v", err))
		return
	}

	// Process command based on type
	switch command.Command {
	case "SEND_CHUNK":
		sfh.handleSendChunkCommand(stream, &command)
	default:
		sfh.sendErrorResponse(stream, fmt.Sprintf("Unknown command type: %s", command.Command))
	}
}

// handleSendChunkCommand processes a SEND_CHUNK command
func (sfh *SecondaryFileHandler) handleSendChunkCommand(stream Stream, command *ChunkCommand) {
	// Create command ID for tracking
	commandID := fmt.Sprintf("%s_%d_%d", command.Filename, command.ChunkID, command.SequenceNum)

	// Check if command is already being processed
	sfh.commandsMutex.RLock()
	if _, exists := sfh.activeCommands[commandID]; exists {
		sfh.commandsMutex.RUnlock()
		sfh.sendErrorResponse(stream, "Command already in progress")
		return
	}
	sfh.commandsMutex.RUnlock()

	// Create command state
	commandState := &CommandState{
		Command:    command,
		ReceivedAt: time.Now(),
		Status:     CommandStatusReceived,
	}

	// Add to active commands
	sfh.commandsMutex.Lock()
	sfh.activeCommands[commandID] = commandState
	sfh.commandsMutex.Unlock()

	// Send acknowledgment
	err := sfh.sendCommandAck(stream, command)
	if err != nil {
		sfh.handleCommandError(commandID, fmt.Errorf("failed to send acknowledgment: %w", err))
		return
	}

	// Process the chunk command
	sfh.processChunkCommand(commandID, commandState)
}

// processChunkCommand processes a chunk command by reading and sending the chunk
func (sfh *SecondaryFileHandler) processChunkCommand(commandID string, commandState *CommandState) {
	command := commandState.Command

	// Update status to reading
	sfh.commandsMutex.Lock()
	commandState.Status = CommandStatusReading
	sfh.commandsMutex.Unlock()

	// Read chunk from file
	chunk, err := sfh.readChunkFromFile(command)
	if err != nil {
		sfh.handleCommandError(commandID, fmt.Errorf("failed to read chunk: %w", err))
		return
	}

	// Store chunk data
	sfh.commandsMutex.Lock()
	commandState.ChunkData = chunk
	commandState.Status = CommandStatusSending
	sfh.commandsMutex.Unlock()

	// Send chunk to client
	err = sfh.sendChunkToClient(chunk, command)
	if err != nil {
		sfh.handleCommandError(commandID, fmt.Errorf("failed to send chunk: %w", err))
		return
	}

	// Update status to completed
	sfh.commandsMutex.Lock()
	commandState.Status = CommandStatusCompleted
	commandState.SentAt = time.Now()
	sfh.commandsMutex.Unlock()

	// Notify primary server of completion
	sfh.notifyPrimaryServer(command, true, "")

	// Clean up command after a delay
	go func() {
		time.Sleep(5 * time.Minute)
		sfh.cleanupCommand(commandID)
	}()
}

// readChunkFromFile reads a specific chunk from the file
func (sfh *SecondaryFileHandler) readChunkFromFile(command *ChunkCommand) (*FileChunk, error) {
	// Validate filename
	if err := sfh.validateFilename(command.Filename); err != nil {
		return nil, fmt.Errorf("invalid filename: %w", err)
	}

	// Construct full file path
	filePath := filepath.Join(sfh.fileDirectory, command.Filename)

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
	totalChunks := uint32((fileInfo.Size() + int64(sfh.chunkSize) - 1) / int64(sfh.chunkSize))
	isLast := command.SequenceNum == totalChunks-1

	// Create chunk
	chunk := NewFileChunk(
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
func (sfh *SecondaryFileHandler) sendChunkToClient(chunk *FileChunk, command *ChunkCommand) error {
	// Open a new stream to send the chunk
	stream, err := sfh.session.OpenStreamSync(sfh.ctx)
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
	chunk.Metadata["sender_id"] = sfh.serverID

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
func (sfh *SecondaryFileHandler) notifyPrimaryServer(command *ChunkCommand, success bool, errorMessage string) {
	// Create response command
	var responseCommand *ChunkCommand
	if success {
		responseCommand = NewChunkCommand("CHUNK_SENT", command.ChunkID, command.SequenceNum, command.Filename, command.StartOffset, command.Size)
		responseCommand.Checksum = command.Checksum
		responseCommand.ChecksumType = command.ChecksumType
	} else {
		responseCommand = NewChunkCommand("CHUNK_ERROR", command.ChunkID, command.SequenceNum, command.Filename, command.StartOffset, command.Size)
		responseCommand.Metadata["error"] = errorMessage
	}

	// Set sender and receiver IDs
	responseCommand.SenderID = sfh.serverID
	responseCommand.ReceiverID = sfh.primaryServerID

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

	// Send response to primary server via raw data
	// Note: In a real implementation, we would need to determine the correct path ID
	// For now, we'll use the first available path
	activePaths := sfh.session.GetActivePaths()
	if len(activePaths) == 0 {
		fmt.Printf("No active paths available to send response to primary server\n")
		return
	}

	pathID := activePaths[0].PathID
	err = sfh.session.SendRawData(responseData, pathID)
	if err != nil {
		fmt.Printf("Failed to send response to primary server: %v\n", err)
	}
}

// validateFilename validates the requested filename
func (sfh *SecondaryFileHandler) validateFilename(filename string) error {
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

	return nil
}

// sendCommandAck sends an acknowledgment for a received command
func (sfh *SecondaryFileHandler) sendCommandAck(stream Stream, command *ChunkCommand) error {
	response := map[string]interface{}{
		"type":         "COMMAND_ACK",
		"command":      command.Command,
		"chunk_id":     command.ChunkID,
		"sequence_num": command.SequenceNum,
		"filename":     command.Filename,
		"server_id":    sfh.serverID,
	}

	responseData, err := json.Marshal(response)
	if err != nil {
		return err
	}

	_, err = stream.Write(responseData)
	return err
}

// sendErrorResponse sends an error response to the client
func (sfh *SecondaryFileHandler) sendErrorResponse(stream Stream, errorMessage string) {
	response := map[string]interface{}{
		"type":      "ERROR",
		"error":     errorMessage,
		"server_id": sfh.serverID,
	}

	responseData, _ := json.Marshal(response)
	stream.Write(responseData)
}

// handleCommandError handles errors during command processing
func (sfh *SecondaryFileHandler) handleCommandError(commandID string, err error) {
	sfh.commandsMutex.Lock()
	defer sfh.commandsMutex.Unlock()

	commandState, exists := sfh.activeCommands[commandID]
	if !exists {
		return
	}

	commandState.Status = CommandStatusFailed
	commandState.ErrorMessage = err.Error()

	// Notify primary server of error
	go sfh.notifyPrimaryServer(commandState.Command, false, err.Error())
}

// monitorCommands monitors active commands and handles cleanup
func (sfh *SecondaryFileHandler) monitorCommands() {
	ticker := time.NewTicker(60 * time.Second) // Check every minute
	defer ticker.Stop()

	for {
		select {
		case <-sfh.ctx.Done():
			return
		case <-ticker.C:
			sfh.cleanupStaleCommands()
		}
	}
}

// cleanupStaleCommands removes stale or completed commands
func (sfh *SecondaryFileHandler) cleanupStaleCommands() {
	sfh.commandsMutex.Lock()
	defer sfh.commandsMutex.Unlock()

	now := time.Now()
	for commandID, commandState := range sfh.activeCommands {
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
			delete(sfh.activeCommands, commandID)
		}
	}
}

// cleanupCommand removes a command from active commands
func (sfh *SecondaryFileHandler) cleanupCommand(commandID string) {
	sfh.commandsMutex.Lock()
	defer sfh.commandsMutex.Unlock()

	delete(sfh.activeCommands, commandID)
}

// GetActiveCommands returns information about active commands
func (sfh *SecondaryFileHandler) GetActiveCommands() map[string]*CommandState {
	sfh.commandsMutex.RLock()
	defer sfh.commandsMutex.RUnlock()

	// Create a copy to avoid race conditions
	commands := make(map[string]*CommandState)
	for key, state := range sfh.activeCommands {
		commands[key] = state
	}

	return commands
}

// GetCommandStatus returns the status of a specific command
func (sfh *SecondaryFileHandler) GetCommandStatus(commandID string) (*CommandState, error) {
	sfh.commandsMutex.RLock()
	defer sfh.commandsMutex.RUnlock()

	commandState, exists := sfh.activeCommands[commandID]
	if !exists {
		return nil, fmt.Errorf("no command found with ID: %s", commandID)
	}

	return commandState, nil
}

// GetRawPacketHandler returns the raw packet handler instance
func (sfh *SecondaryFileHandler) GetRawPacketHandler() *RawPacketHandler {
	return sfh.rawPacketHandler
}

// IsRawPacketHandlerRunning returns whether the raw packet handler is running
func (sfh *SecondaryFileHandler) IsRawPacketHandlerRunning() bool {
	if sfh.rawPacketHandler == nil {
		return false
	}
	return sfh.rawPacketHandler.IsRunning()
}

// Close closes the secondary file handler and cleans up resources
func (sfh *SecondaryFileHandler) Close() error {
	sfh.cancel()

	// Stop raw packet handler
	if sfh.rawPacketHandler != nil {
		sfh.rawPacketHandler.Stop()
	}

	// Clean up all active commands
	sfh.commandsMutex.Lock()
	sfh.activeCommands = make(map[string]*CommandState)
	sfh.commandsMutex.Unlock()

	return nil
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