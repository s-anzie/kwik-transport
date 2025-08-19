package filetransfer

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"kwik/pkg/session"
)

// RawPacketMessage represents a message sent via raw packet transmission
type RawPacketMessage struct {
	Type           string                 `json:"type"`
	SourceServerID string                 `json:"source_server_id"`
	TargetServerID string                 `json:"target_server_id,omitempty"`
	ProtocolHint   string                 `json:"protocol_hint"`
	Data           json.RawMessage        `json:"data"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
	Timestamp      time.Time              `json:"timestamp"`
}

// RawPacketHandler handles raw packet transmissions for the secondary file handler
type RawPacketHandler struct {
	secondaryHandler *SecondaryFileHandler
	session          session.Session
	ctx              context.Context
	cancel           context.CancelFunc
	isRunning        bool
	mutex            sync.RWMutex
}

// NewRawPacketHandler creates a new raw packet handler for the secondary file handler
func NewRawPacketHandler(secondaryHandler *SecondaryFileHandler, kwikSession session.Session) *RawPacketHandler {
	ctx, cancel := context.WithCancel(context.Background())

	return &RawPacketHandler{
		secondaryHandler: secondaryHandler,
		session:          kwikSession,
		ctx:              ctx,
		cancel:           cancel,
		isRunning:        false,
	}
}

// Start starts the raw packet handler
func (rph *RawPacketHandler) Start() error {
	rph.mutex.Lock()
	defer rph.mutex.Unlock()

	if rph.isRunning {
		return fmt.Errorf("raw packet handler is already running")
	}

	rph.isRunning = true

	// Start the raw packet processing goroutine
	go rph.processRawPackets()

	return nil
}

// Stop stops the raw packet handler
func (rph *RawPacketHandler) Stop() error {
	rph.mutex.Lock()
	defer rph.mutex.Unlock()

	if !rph.isRunning {
		return nil
	}

	rph.cancel()
	rph.isRunning = false

	return nil
}

// processRawPackets processes incoming raw packet transmissions
func (rph *RawPacketHandler) processRawPackets() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("Raw packet handler panic: %v\n", r)
		}
	}()

	fmt.Println("DEBUG: Raw packet handler started, listening for chunk commands...")

	for {
		select {
		case <-rph.ctx.Done():
			fmt.Println("DEBUG: Raw packet handler stopping")
			return
		default:
			// Accept incoming streams that might contain raw packet data
			stream, err := rph.session.AcceptStream(rph.ctx)
			if err != nil {
				time.Sleep(100 * time.Millisecond)
				continue
			}

			// Handle the stream in a goroutine
			go rph.handleRawPacketStream(stream)
		}
	}
}

// handleRawPacketStream handles a stream that might contain raw packet data
func (rph *RawPacketHandler) handleRawPacketStream(stream session.Stream) {
	defer stream.Close()

	// Read data from stream
	buffer := make([]byte, 4096)
	n, err := stream.Read(buffer)
	if err != nil {
		return
	}

	// Try to parse as raw packet message first
	var rawPacketMsg RawPacketMessage
	if err := json.Unmarshal(buffer[:n], &rawPacketMsg); err == nil {
		// This is a raw packet message
		if rawPacketMsg.Type == "RAW_PACKET_TRANSMISSION" {
			rph.handleRawPacketMessage(&rawPacketMsg)
			return
		}
	}

	// Try to parse as direct chunk command (fallback)
	var chunkCommand ChunkCommand
	if err := json.Unmarshal(buffer[:n], &chunkCommand); err == nil {
		rph.handleDirectChunkCommand(&chunkCommand, stream)
		return
	}

	// Unknown data format, ignore
	fmt.Printf("DEBUG: Received unknown data format in raw packet stream\n")
}

// handleRawPacketMessage handles a raw packet message
func (rph *RawPacketHandler) handleRawPacketMessage(rawPacketMsg *RawPacketMessage) {
	fmt.Printf("DEBUG: Received raw packet transmission from %s, protocol hint: %s\n",
		rawPacketMsg.SourceServerID, rawPacketMsg.ProtocolHint)

	// Try to parse the raw data as a chunk command
	var chunkCommand ChunkCommand
	if err := json.Unmarshal(rawPacketMsg.Data, &chunkCommand); err != nil {
		fmt.Printf("Failed to parse chunk command from raw packet data: %v\n", err)
		return
	}

	// Validate the chunk command
	if err := chunkCommand.Validate(); err != nil {
		fmt.Printf("Invalid chunk command in raw packet: %v\n", err)
		return
	}

	// Process the chunk command
	rph.processChunkCommandFromRawPacket(&chunkCommand, rawPacketMsg)
}

// handleDirectChunkCommand handles a direct chunk command (not wrapped in raw packet)
func (rph *RawPacketHandler) handleDirectChunkCommand(command *ChunkCommand, stream session.Stream) {
	// Validate the chunk command
	if err := command.Validate(); err != nil {
		rph.sendErrorResponse(stream, fmt.Sprintf("Invalid chunk command: %v", err))
		return
	}

	// Process the chunk command based on type
	switch command.Command {
	case "SEND_CHUNK":
		rph.processDirectSendChunkCommand(command, stream)
	default:
		rph.sendErrorResponse(stream, fmt.Sprintf("Unknown command type: %s", command.Command))
	}
}

// processChunkCommandFromRawPacket processes a chunk command received via raw packet
func (rph *RawPacketHandler) processChunkCommandFromRawPacket(command *ChunkCommand, rawPacketMsg *RawPacketMessage) {
	switch command.Command {
	case "SEND_CHUNK":
		rph.processRawPacketSendChunkCommand(command, rawPacketMsg)
	default:
		fmt.Printf("Unknown chunk command type in raw packet: %s\n", command.Command)
	}
}

// processRawPacketSendChunkCommand processes a SEND_CHUNK command received via raw packet
func (rph *RawPacketHandler) processRawPacketSendChunkCommand(command *ChunkCommand, rawPacketMsg *RawPacketMessage) {
	// Create command ID for tracking
	commandID := fmt.Sprintf("%s_%d_%d", command.Filename, command.ChunkID, command.SequenceNum)

	fmt.Printf("DEBUG: Processing SEND_CHUNK command from raw packet: %s\n", commandID)

	// Check if command is already being processed
	if _, exists := rph.secondaryHandler.activeCommands[commandID]; exists {
		fmt.Printf("Command already in progress: %s\n", commandID)
		rph.sendRawPacketErrorResponse(command, rawPacketMsg, "Command already in progress")
		return
	}

	// Create command state
	commandState := &CommandState{
		Command:    command,
		ReceivedAt: time.Now(),
		Status:     CommandStatusReceived,
	}

	// Add to active commands
	rph.secondaryHandler.commandsMutex.Lock()
	rph.secondaryHandler.activeCommands[commandID] = commandState
	rph.secondaryHandler.commandsMutex.Unlock()

	// Send acknowledgment via raw packet response
	rph.sendRawPacketAckResponse(command, rawPacketMsg)

	// Process the chunk command
	go rph.processRawPacketChunkCommand(commandID, commandState, rawPacketMsg)
}

// processDirectSendChunkCommand processes a SEND_CHUNK command received directly
func (rph *RawPacketHandler) processDirectSendChunkCommand(command *ChunkCommand, stream session.Stream) {
	// Create command ID for tracking
	commandID := fmt.Sprintf("%s_%d_%d", command.Filename, command.ChunkID, command.SequenceNum)

	fmt.Printf("DEBUG: Processing direct SEND_CHUNK command: %s\n", commandID)

	// Check if command is already being processed
	if _, exists := rph.secondaryHandler.activeCommands[commandID]; exists {
		rph.sendErrorResponse(stream, "Command already in progress")
		return
	}

	// Create command state
	commandState := &CommandState{
		Command:    command,
		ReceivedAt: time.Now(),
		Status:     CommandStatusReceived,
	}

	// Add to active commands
	rph.secondaryHandler.commandsMutex.Lock()
	rph.secondaryHandler.activeCommands[commandID] = commandState
	rph.secondaryHandler.commandsMutex.Unlock()

	// Send acknowledgment
	err := rph.sendDirectAckResponse(stream, command)
	if err != nil {
		rph.handleCommandError(commandID, fmt.Errorf("failed to send acknowledgment: %w", err))
		return
	}

	// Process the chunk command
	go rph.processDirectChunkCommand(commandID, commandState)
}

// processRawPacketChunkCommand processes a chunk command received via raw packet
func (rph *RawPacketHandler) processRawPacketChunkCommand(commandID string, commandState *CommandState, rawPacketMsg *RawPacketMessage) {
	command := commandState.Command

	// Update status to reading
	rph.secondaryHandler.commandsMutex.Lock()
	commandState.Status = CommandStatusReading
	rph.secondaryHandler.commandsMutex.Unlock()

	// Read chunk from file
	chunk, err := rph.secondaryHandler.readChunkFromFile(command)
	if err != nil {
		rph.handleCommandError(commandID, fmt.Errorf("failed to read chunk: %w", err))
		rph.sendRawPacketErrorResponse(command, rawPacketMsg, err.Error())
		return
	}

	// Store chunk data
	rph.secondaryHandler.commandsMutex.Lock()
	commandState.ChunkData = chunk
	commandState.Status = CommandStatusSending
	rph.secondaryHandler.commandsMutex.Unlock()

	// Send chunk to client
	err = rph.secondaryHandler.sendChunkToClient(chunk, command)
	if err != nil {
		rph.handleCommandError(commandID, fmt.Errorf("failed to send chunk: %w", err))
		rph.sendRawPacketErrorResponse(command, rawPacketMsg, err.Error())
		return
	}

	// Update status to completed
	rph.secondaryHandler.commandsMutex.Lock()
	commandState.Status = CommandStatusCompleted
	commandState.SentAt = time.Now()
	rph.secondaryHandler.commandsMutex.Unlock()

	// Send success response via raw packet
	rph.sendRawPacketSuccessResponse(command, rawPacketMsg)

	// Clean up command after a delay
	go func() {
		time.Sleep(5 * time.Minute)
		rph.secondaryHandler.cleanupCommand(commandID)
	}()
}

// processDirectChunkCommand processes a chunk command received directly
func (rph *RawPacketHandler) processDirectChunkCommand(commandID string, commandState *CommandState) {
	command := commandState.Command

	// Update status to reading
	rph.secondaryHandler.commandsMutex.Lock()
	commandState.Status = CommandStatusReading
	rph.secondaryHandler.commandsMutex.Unlock()

	// Read chunk from file
	chunk, err := rph.secondaryHandler.readChunkFromFile(command)
	if err != nil {
		rph.handleCommandError(commandID, fmt.Errorf("failed to read chunk: %w", err))
		return
	}

	// Store chunk data
	rph.secondaryHandler.commandsMutex.Lock()
	commandState.ChunkData = chunk
	commandState.Status = CommandStatusSending
	rph.secondaryHandler.commandsMutex.Unlock()

	// Send chunk to client
	err = rph.secondaryHandler.sendChunkToClient(chunk, command)
	if err != nil {
		rph.handleCommandError(commandID, fmt.Errorf("failed to send chunk: %w", err))
		return
	}

	// Update status to completed
	rph.secondaryHandler.commandsMutex.Lock()
	commandState.Status = CommandStatusCompleted
	commandState.SentAt = time.Now()
	rph.secondaryHandler.commandsMutex.Unlock()

	// Notify primary server of completion
	rph.secondaryHandler.notifyPrimaryServer(command, true, "")

	// Clean up command after a delay
	go func() {
		time.Sleep(5 * time.Minute)
		rph.secondaryHandler.cleanupCommand(commandID)
	}()
}

// sendRawPacketAckResponse sends an acknowledgment response via raw packet
func (rph *RawPacketHandler) sendRawPacketAckResponse(command *ChunkCommand, rawPacketMsg *RawPacketMessage) {
	response := map[string]interface{}{
		"type":         "COMMAND_ACK",
		"command":      command.Command,
		"chunk_id":     command.ChunkID,
		"sequence_num": command.SequenceNum,
		"filename":     command.Filename,
		"server_id":    rph.secondaryHandler.serverID,
	}

	responseData, err := json.Marshal(response)
	if err != nil {
		fmt.Printf("Failed to serialize ack response: %v\n", err)
		return
	}

	// Send response back to source server
	rph.sendRawPacketResponse(responseData, rawPacketMsg.SourceServerID)
}

// sendRawPacketSuccessResponse sends a success response via raw packet
func (rph *RawPacketHandler) sendRawPacketSuccessResponse(command *ChunkCommand, rawPacketMsg *RawPacketMessage) {
	responseCommand := NewChunkCommand("CHUNK_SENT", command.ChunkID, command.SequenceNum, command.Filename, command.StartOffset, command.Size)
	responseCommand.Checksum = command.Checksum
	responseCommand.ChecksumType = command.ChecksumType
	responseCommand.SenderID = rph.secondaryHandler.serverID
	responseCommand.ReceiverID = rawPacketMsg.SourceServerID

	// Copy relevant metadata
	if clientAddress, exists := command.Metadata["client_address"]; exists {
		responseCommand.Metadata["client_address"] = clientAddress
	}

	responseData, err := json.Marshal(responseCommand)
	if err != nil {
		fmt.Printf("Failed to serialize success response: %v\n", err)
		return
	}

	// Send response back to source server
	rph.sendRawPacketResponse(responseData, rawPacketMsg.SourceServerID)
}

// sendRawPacketErrorResponse sends an error response via raw packet
func (rph *RawPacketHandler) sendRawPacketErrorResponse(command *ChunkCommand, rawPacketMsg *RawPacketMessage, errorMessage string) {
	responseCommand := NewChunkCommand("CHUNK_ERROR", command.ChunkID, command.SequenceNum, command.Filename, command.StartOffset, command.Size)
	responseCommand.SenderID = rph.secondaryHandler.serverID
	responseCommand.ReceiverID = rawPacketMsg.SourceServerID
	responseCommand.Metadata["error"] = errorMessage

	// Copy relevant metadata
	if clientAddress, exists := command.Metadata["client_address"]; exists {
		responseCommand.Metadata["client_address"] = clientAddress
	}

	responseData, err := json.Marshal(responseCommand)
	if err != nil {
		fmt.Printf("Failed to serialize error response: %v\n", err)
		return
	}

	// Send response back to source server
	rph.sendRawPacketResponse(responseData, rawPacketMsg.SourceServerID)
}

// sendRawPacketResponse sends a response via raw packet
func (rph *RawPacketHandler) sendRawPacketResponse(responseData []byte, targetServerID string) {
	// Get active paths to send response
	activePaths := rph.session.GetActivePaths()
	if len(activePaths) == 0 {
		fmt.Printf("No active paths available to send raw packet response\n")
		return
	}

	// Use the first available path
	pathID := activePaths[0].PathID
	err := rph.session.SendRawData(responseData, pathID)
	if err != nil {
		fmt.Printf("Failed to send raw packet response: %v\n", err)
	}
}

// sendDirectAckResponse sends an acknowledgment response directly via stream
func (rph *RawPacketHandler) sendDirectAckResponse(stream session.Stream, command *ChunkCommand) error {
	response := map[string]interface{}{
		"type":         "COMMAND_ACK",
		"command":      command.Command,
		"chunk_id":     command.ChunkID,
		"sequence_num": command.SequenceNum,
		"filename":     command.Filename,
		"server_id":    rph.secondaryHandler.serverID,
	}

	responseData, err := json.Marshal(response)
	if err != nil {
		return err
	}

	_, err = stream.Write(responseData)
	return err
}

// sendErrorResponse sends an error response directly via stream
func (rph *RawPacketHandler) sendErrorResponse(stream session.Stream, errorMessage string) {
	response := map[string]interface{}{
		"type":      "ERROR",
		"error":     errorMessage,
		"server_id": rph.secondaryHandler.serverID,
	}

	responseData, _ := json.Marshal(response)
	stream.Write(responseData)
}

// handleCommandError handles errors during command processing
func (rph *RawPacketHandler) handleCommandError(commandID string, err error) {
	rph.secondaryHandler.commandsMutex.Lock()
	defer rph.secondaryHandler.commandsMutex.Unlock()

	commandState, exists := rph.secondaryHandler.activeCommands[commandID]
	if !exists {
		return
	}

	commandState.Status = CommandStatusFailed
	commandState.ErrorMessage = err.Error()
}

// IsRunning returns whether the raw packet handler is currently running
func (rph *RawPacketHandler) IsRunning() bool {
	rph.mutex.RLock()
	defer rph.mutex.RUnlock()
	return rph.isRunning
}
