package server

import (
	"encoding/json"
	"ftpa/internal/config"
	"ftpa/internal/types"
	"ftpa/internal/utils"
	"testing"
	"time"
)

func TestNewSecondaryFileTransferServer(t *testing.T) {
	testDir := createTestDir(t)
	defer cleanupTestDir(t, testDir)
	
	// Create server configuration
	serverConfig := config.DefaultServerConfiguration()
	serverConfig.Server.FileDirectory = testDir
	serverConfig.Server.Address = "localhost:8081"
	serverConfig.Limits.MaxFileSize = 1024 * 1024
	serverConfig.Limits.AllowedExtensions = []string{".txt", ".json"}
	serverConfig.Performance.ChunkSize = 1024
	serverConfig.Limits.MaxConcurrent = 5
	
	server, err := NewSecondaryFileTransferServer(serverConfig)
	if err != nil {
		t.Fatalf("Failed to create SecondaryFileTransferServer: %v", err)
	}
	
	if server == nil {
		t.Fatal("Server should not be nil")
	}
	
	// Test that server has correct configuration
	if server.config.Server.FileDirectory != testDir {
		t.Errorf("Expected file directory %s, got %s", testDir, server.config.Server.FileDirectory)
	}
	
	if server.config.Limits.MaxFileSize != 1024*1024 {
		t.Errorf("Expected max file size %d, got %d", 1024*1024, server.config.Limits.MaxFileSize)
	}
	
	// Test that server is not running initially
	if server.IsRunning() {
		t.Error("Expected server to not be running initially")
	}
	
	// Cleanup
	server.Close()
}

func TestNewSecondaryFileTransferServerInvalidConfig(t *testing.T) {
	// Test nil config
	_, err := NewSecondaryFileTransferServer(nil)
	if err == nil {
		t.Error("Expected error for nil config")
	}
	
	// Test empty file directory
	serverConfig := config.DefaultServerConfiguration()
	serverConfig.Server.FileDirectory = ""
	_, err = NewSecondaryFileTransferServer(serverConfig)
	if err == nil {
		t.Error("Expected error for empty file directory")
	}
	
	// Test non-existent directory
	serverConfig2 := config.DefaultServerConfiguration()
	serverConfig2.Server.FileDirectory = "/non/existent/directory"
	_, err = NewSecondaryFileTransferServer(serverConfig2)
	if err == nil {
		t.Error("Expected error for non-existent directory")
	}
}

func TestSecondaryServerValidateFilename(t *testing.T) {
	testDir := createTestDir(t)
	defer cleanupTestDir(t, testDir)
	
	// Create server configuration
	serverConfig := config.DefaultServerConfiguration()
	serverConfig.Server.FileDirectory = testDir
	serverConfig.Limits.AllowedExtensions = []string{".txt"}
	
	server, err := NewSecondaryFileTransferServer(serverConfig)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()
	
	// Test valid filename
	err = server.validateFilename("test.txt")
	if err != nil {
		t.Errorf("Expected valid filename to pass: %v", err)
	}
	
	// Test empty filename
	err = server.validateFilename("")
	if err == nil {
		t.Error("Expected error for empty filename")
	}
	
	// Test absolute path
	err = server.validateFilename("/etc/passwd")
	if err == nil {
		t.Error("Expected error for absolute path")
	}
	
	// Test path traversal
	err = server.validateFilename("../test.txt")
	if err == nil {
		t.Error("Expected error for path traversal")
	}
	
	// Test subdirectory
	err = server.validateFilename("subdir/test.txt")
	if err == nil {
		t.Error("Expected error for subdirectory")
	}
	
	// Test invalid extension
	err = server.validateFilename("test.json")
	if err == nil {
		t.Error("Expected error for invalid extension")
	}
}

func TestSecondaryServerReadChunkFromFile(t *testing.T) {
	testDir := createTestDir(t)
	defer cleanupTestDir(t, testDir)
	
	// Create test file
	testContent := "Hello, World! This is a test file for chunk reading."
	createTestFile(t, testDir, "test.txt", testContent)
	
	// Create server configuration
	serverConfig := config.DefaultServerConfiguration()
	serverConfig.Server.FileDirectory = testDir
	serverConfig.Performance.ChunkSize = 10
	
	server, err := NewSecondaryFileTransferServer(serverConfig)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()
	
	// Create chunk command
	command := types.NewChunkCommand("SEND_CHUNK", 0, 0, "test.txt", 0, 10)
	
	// Test reading chunk from file
	chunk, err := server.readChunkFromFile(command)
	if err != nil {
		t.Fatalf("Failed to read chunk from file: %v", err)
	}
	
	if chunk.ChunkID != 0 {
		t.Errorf("Expected chunk ID 0, got %d", chunk.ChunkID)
	}
	
	if chunk.SequenceNum != 0 {
		t.Errorf("Expected sequence number 0, got %d", chunk.SequenceNum)
	}
	
	if string(chunk.Data) != testContent[:10] {
		t.Errorf("Expected chunk data '%s', got '%s'", testContent[:10], string(chunk.Data))
	}
	
	if chunk.Filename != "test.txt" {
		t.Errorf("Expected filename test.txt, got %s", chunk.Filename)
	}
	
	// Test reading non-existent file
	command2 := types.NewChunkCommand("SEND_CHUNK", 0, 0, "nonexistent.txt", 0, 10)
	_, err = server.readChunkFromFile(command2)
	if err == nil {
		t.Error("Expected error for non-existent file")
	}
}

func TestSecondaryServerHandleSendChunkCommand(t *testing.T) {
	testDir := createTestDir(t)
	defer cleanupTestDir(t, testDir)
	
	// Create test file
	testContent := "Hello, World!"
	createTestFile(t, testDir, "test.txt", testContent)
	
	// Create server configuration
	serverConfig := config.DefaultServerConfiguration()
	serverConfig.Server.FileDirectory = testDir
	serverConfig.Performance.ChunkSize = 5
	
	server, err := NewSecondaryFileTransferServer(serverConfig)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()
	
	// Mock the session for testing
	mockSession := utils.NewMockSession()
	server.session = mockSession
	
	// Create mock stream with chunk command
	stream := &utils.MockStream{}
	stream.SetStreamID(1)
	stream.SetPathID("primary")
	
	// Create chunk command
	command := types.NewChunkCommand("SEND_CHUNK", 0, 0, "test.txt", 0, 5)
	command.Metadata["client_address"] = "client1"
	
	// Handle the command
	server.handleSendChunkCommand(stream, command)
	
	// Wait a bit for processing
	time.Sleep(100 * time.Millisecond)
	
	// Check that command was added to active commands
	activeCommands := server.GetActiveCommands()
	if len(activeCommands) != 1 {
		t.Errorf("Expected 1 active command, got %d", len(activeCommands))
	}
	
	// Check that acknowledgment was sent
	writtenData := stream.GetData()
	if len(writtenData) == 0 {
		t.Error("Expected acknowledgment to be written")
	}
	
	var response map[string]interface{}
	json.Unmarshal(writtenData, &response)
	
	if response["type"] != "COMMAND_ACK" {
		t.Error("Expected COMMAND_ACK response")
	}
}

func TestSecondaryServerCommandProcessing(t *testing.T) {
	testDir := createTestDir(t)
	defer cleanupTestDir(t, testDir)
	
	// Create test file
	testContent := "Hello, World!"
	createTestFile(t, testDir, "test.txt", testContent)
	
	// Create server configuration
	serverConfig := config.DefaultServerConfiguration()
	serverConfig.Server.FileDirectory = testDir
	serverConfig.Performance.ChunkSize = 5
	
	server, err := NewSecondaryFileTransferServer(serverConfig)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()
	
	// Mock the session for testing
	mockSession := utils.NewMockSession()
	server.session = mockSession
	
	// Create chunk command
	command := types.NewChunkCommand("SEND_CHUNK", 0, 0, "test.txt", 0, 5)
	command.Metadata["client_address"] = "client1"
	
	// Create command state
	commandID := "test.txt_0_0"
	commandState := &CommandState{
		Command:    command,
		ReceivedAt: time.Now(),
		Status:     CommandStatusReceived,
	}
	
	// Add to active commands
	server.commandsMutex.Lock()
	server.activeCommands[commandID] = commandState
	server.commandsMutex.Unlock()
	
	// Process the chunk command
	server.processChunkCommand(commandID, commandState)
	
	// Wait for processing to complete
	time.Sleep(200 * time.Millisecond)
	
	// Check command status
	server.commandsMutex.RLock()
	finalState := server.activeCommands[commandID]
	server.commandsMutex.RUnlock()
	
	if finalState.Status != CommandStatusCompleted {
		t.Errorf("Expected command status COMPLETED, got %s", finalState.Status.String())
	}
	
	if finalState.ChunkData == nil {
		t.Error("Expected chunk data to be set")
	}
	
	if string(finalState.ChunkData.Data) != testContent[:5] {
		t.Errorf("Expected chunk data '%s', got '%s'", testContent[:5], string(finalState.ChunkData.Data))
	}
}

func TestSecondaryServerCommandErrors(t *testing.T) {
	testDir := createTestDir(t)
	defer cleanupTestDir(t, testDir)
	
	// Create server configuration
	serverConfig := config.DefaultServerConfiguration()
	serverConfig.Server.FileDirectory = testDir
	serverConfig.Performance.ChunkSize = 5
	
	server, err := NewSecondaryFileTransferServer(serverConfig)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()
	
	// Mock the session for testing
	mockSession := utils.NewMockSession()
	server.session = mockSession
	
	// Create mock stream with invalid command
	stream := &utils.MockStream{}
	stream.SetStreamID(1)
	stream.SetPathID("primary")
	
	// Test invalid command type
	command := types.NewChunkCommand("INVALID_COMMAND", 0, 0, "test.txt", 0, 5)
	server.handleSendChunkCommand(stream, command)
	
	// Check that error response was sent
	writtenData := stream.GetData()
	if len(writtenData) == 0 {
		t.Error("Expected error response to be written")
	}
	
	// Test non-existent file
	stream2 := &utils.MockStream{}
	stream2.SetStreamID(2)
	stream2.SetPathID("primary")
	
	command2 := types.NewChunkCommand("SEND_CHUNK", 0, 0, "nonexistent.txt", 0, 5)
	
	// Create command state
	commandID := "nonexistent.txt_0_0"
	commandState := &CommandState{
		Command:    command2,
		ReceivedAt: time.Now(),
		Status:     CommandStatusReceived,
	}
	
	// Add to active commands
	server.commandsMutex.Lock()
	server.activeCommands[commandID] = commandState
	server.commandsMutex.Unlock()
	
	// Process the chunk command (should fail)
	server.processChunkCommand(commandID, commandState)
	
	// Wait for processing to complete
	time.Sleep(100 * time.Millisecond)
	
	// Check command status
	server.commandsMutex.RLock()
	finalState := server.activeCommands[commandID]
	server.commandsMutex.RUnlock()
	
	if finalState.Status != CommandStatusFailed {
		t.Errorf("Expected command status FAILED, got %s", finalState.Status.String())
	}
	
	if finalState.ErrorMessage == "" {
		t.Error("Expected error message to be set")
	}
}

func TestSecondaryServerCleanup(t *testing.T) {
	testDir := createTestDir(t)
	defer cleanupTestDir(t, testDir)
	
	// Create server configuration
	serverConfig := config.DefaultServerConfiguration()
	serverConfig.Server.FileDirectory = testDir
	
	server, err := NewSecondaryFileTransferServer(serverConfig)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	
	// Add some mock active commands
	server.activeCommands["test1"] = &CommandState{
		Command: &types.ChunkCommand{
			Command:  "SEND_CHUNK",
			Filename: "test1.txt",
		},
		ReceivedAt: time.Now().Add(-20 * time.Minute), // Old command
		Status:     CommandStatusCompleted,
	}
	
	server.activeCommands["test2"] = &CommandState{
		Command: &types.ChunkCommand{
			Command:  "SEND_CHUNK",
			Filename: "test2.txt",
		},
		ReceivedAt: time.Now(),
		Status:     CommandStatusSending,
	}
	
	// Test cleanup
	server.cleanupStaleCommands()
	
	// Check that old completed command was removed
	if _, exists := server.activeCommands["test1"]; exists {
		t.Error("Expected old completed command to be cleaned up")
	}
	
	// Check that active command remains
	if _, exists := server.activeCommands["test2"]; !exists {
		t.Error("Expected active command to remain")
	}
	
	// Test server close
	err = server.Close()
	if err != nil {
		t.Errorf("Failed to close server: %v", err)
	}
	
	// Check that all commands were cleaned up
	server.commandsMutex.RLock()
	commandCount := len(server.activeCommands)
	server.commandsMutex.RUnlock()
	
	if commandCount != 0 {
		t.Error("Expected all commands to be cleaned up after close")
	}
}

func TestSecondaryServerRawPacketIntegration(t *testing.T) {
	testDir := createTestDir(t)
	defer cleanupTestDir(t, testDir)
	
	// Create test file
	testContent := "Hello, World!"
	createTestFile(t, testDir, "test.txt", testContent)
	
	// Create server configuration
	serverConfig := config.DefaultServerConfiguration()
	serverConfig.Server.FileDirectory = testDir
	serverConfig.Performance.ChunkSize = 5
	
	server, err := NewSecondaryFileTransferServer(serverConfig)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()
	
	// Mock the session for testing
	mockSession := utils.NewMockSession()
	server.session = mockSession
	
	// Create raw packet handler manually for testing
	rawPacketHandler := NewRawPacketHandler(server, mockSession)
	server.rawPacketHandler = rawPacketHandler
	
	// Test that raw packet handler was created
	if server.GetRawPacketHandler() == nil {
		t.Error("Expected raw packet handler to be created")
	}
	
	// Start raw packet handler
	err = rawPacketHandler.Start()
	if err != nil {
		t.Fatalf("Failed to start raw packet handler: %v", err)
	}
	defer rawPacketHandler.Stop()
	
	// Test that raw packet handler is running
	if !server.IsRawPacketHandlerRunning() {
		t.Error("Expected raw packet handler to be running")
	}
	
	// Create chunk command
	command := types.NewChunkCommand("SEND_CHUNK", 0, 0, "test.txt", 0, 5)
	
	// Create mock stream for direct command
	mockStream := &utils.MockStream{}
	mockStream.SetStreamID(1)
	mockStream.SetPathID("primary")
	mockStream.SetData(make([]byte, 0))
	mockStream.SetClosed(false)
	
	// Serialize command
	commandData, err := json.Marshal(command)
	if err != nil {
		t.Fatalf("Failed to serialize command: %v", err)
	}
	
	// Set command data in mock stream
	mockStream.SetData(commandData)
	
	// Process direct chunk command
	rawPacketHandler.handleDirectChunkCommand(command, mockStream)
	
	// Wait a bit for processing
	time.Sleep(200 * time.Millisecond)
	
	// Verify command was processed - check both server and raw packet handler
	activeCommands := server.GetActiveCommands()
	rawPacketCommands := len(rawPacketHandler.activeCommands)
	
	if len(activeCommands) == 0 && rawPacketCommands == 0 {
		t.Error("Expected command to be processed in either server or raw packet handler")
	}
}

func TestCommandStatusString(t *testing.T) {
	tests := []struct {
		status   CommandStatus
		expected string
	}{
		{CommandStatusReceived, "RECEIVED"},
		{CommandStatusReading, "READING"},
		{CommandStatusSending, "SENDING"},
		{CommandStatusCompleted, "COMPLETED"},
		{CommandStatusFailed, "FAILED"},
		{CommandStatus(999), "UNKNOWN"},
	}
	
	for _, test := range tests {
		result := test.status.String()
		if result != test.expected {
			t.Errorf("Expected %s, got %s", test.expected, result)
		}
	}
}