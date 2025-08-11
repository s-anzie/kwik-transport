package filetransfer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSecondaryFileHandler_Creation(t *testing.T) {
	// Create temporary directory for test files
	tempDir, err := os.MkdirTemp("", "secondary_handler_test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create test file
	testFile := filepath.Join(tempDir, "test.txt")
	testContent := []byte("Hello, World! This is a test file for chunk processing.")
	if err := os.WriteFile(testFile, testContent, 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create mock session
	mockSession := NewMockSession()

	// Create configuration
	config := &SecondaryFileHandlerConfig{
		FileDirectory:   tempDir,
		ChunkSize:       32, // Small chunk size for testing
		PrimaryServerID: "primary",
		ServerID:        "secondary",
	}

	// Create secondary file handler
	handler, err := NewSecondaryFileHandler(mockSession, config)
	if err != nil {
		t.Fatalf("Failed to create secondary file handler: %v", err)
	}
	defer handler.Close()

	// Verify handler was created correctly
	if handler.fileDirectory != tempDir {
		t.Errorf("Expected file directory %s, got %s", tempDir, handler.fileDirectory)
	}

	if handler.chunkSize != 32 {
		t.Errorf("Expected chunk size 32, got %d", handler.chunkSize)
	}

	if handler.serverID != "secondary" {
		t.Errorf("Expected server ID 'secondary', got %s", handler.serverID)
	}

	if handler.primaryServerID != "primary" {
		t.Errorf("Expected primary server ID 'primary', got %s", handler.primaryServerID)
	}

	// Verify raw packet handler is running
	if !handler.IsRawPacketHandlerRunning() {
		t.Error("Expected raw packet handler to be running")
	}
}

func TestSecondaryFileHandler_ReadChunkFromFile(t *testing.T) {
	// Create temporary directory for test files
	tempDir, err := os.MkdirTemp("", "secondary_handler_test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create test file
	testFile := filepath.Join(tempDir, "test.txt")
	testContent := []byte("Hello, World! This is a test file for chunk processing.")
	if err := os.WriteFile(testFile, testContent, 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create mock session
	mockSession := NewMockSession()

	// Create configuration
	config := &SecondaryFileHandlerConfig{
		FileDirectory:   tempDir,
		ChunkSize:       20, // Small chunk size for testing
		PrimaryServerID: "primary",
		ServerID:        "secondary",
	}

	// Create secondary file handler
	handler, err := NewSecondaryFileHandler(mockSession, config)
	if err != nil {
		t.Fatalf("Failed to create secondary file handler: %v", err)
	}
	defer handler.Close()

	// Test reading first chunk
	command := &ChunkCommand{
		Command:     "SEND_CHUNK",
		ChunkID:     0,
		SequenceNum: 0,
		Filename:    "test.txt",
		StartOffset: 0,
		Size:        20,
		Metadata:    make(map[string]string),
	}

	chunk, err := handler.readChunkFromFile(command)
	if err != nil {
		t.Fatalf("Failed to read chunk from file: %v", err)
	}

	// Verify chunk properties
	if chunk.ChunkID != 0 {
		t.Errorf("Expected chunk ID 0, got %d", chunk.ChunkID)
	}

	if chunk.SequenceNum != 0 {
		t.Errorf("Expected sequence number 0, got %d", chunk.SequenceNum)
	}

	if chunk.Filename != "test.txt" {
		t.Errorf("Expected filename 'test.txt', got %s", chunk.Filename)
	}

	if chunk.Size != 20 {
		t.Errorf("Expected chunk size 20, got %d", chunk.Size)
	}

	if chunk.Offset != 0 {
		t.Errorf("Expected offset 0, got %d", chunk.Offset)
	}

	expectedData := testContent[:20]
	if string(chunk.Data) != string(expectedData) {
		t.Errorf("Expected chunk data %q, got %q", string(expectedData), string(chunk.Data))
	}

	// Test reading second chunk
	command2 := &ChunkCommand{
		Command:     "SEND_CHUNK",
		ChunkID:     1,
		SequenceNum: 1,
		Filename:    "test.txt",
		StartOffset: 20,
		Size:        20,
		Metadata:    make(map[string]string),
	}

	chunk2, err := handler.readChunkFromFile(command2)
	if err != nil {
		t.Fatalf("Failed to read second chunk from file: %v", err)
	}

	expectedData2 := testContent[20:40]
	if string(chunk2.Data) != string(expectedData2) {
		t.Errorf("Expected chunk data %q, got %q", string(expectedData2), string(chunk2.Data))
	}
}

func TestSecondaryFileHandler_ValidateFilename(t *testing.T) {
	// Create temporary directory for test files
	tempDir, err := os.MkdirTemp("", "secondary_handler_test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create mock session
	mockSession := NewMockSession()

	// Create configuration
	config := &SecondaryFileHandlerConfig{
		FileDirectory:   tempDir,
		ChunkSize:       32,
		PrimaryServerID: "primary",
		ServerID:        "secondary",
	}

	// Create secondary file handler
	handler, err := NewSecondaryFileHandler(mockSession, config)
	if err != nil {
		t.Fatalf("Failed to create secondary file handler: %v", err)
	}
	defer handler.Close()

	// Test valid filenames
	validFilenames := []string{
		"test.txt",
		"file.json",
		"data.bin",
		"document.pdf",
	}

	for _, filename := range validFilenames {
		if err := handler.validateFilename(filename); err != nil {
			t.Errorf("Expected filename %q to be valid, got error: %v", filename, err)
		}
	}

	// Test invalid filenames
	invalidFilenames := []string{
		"",                    // Empty filename
		"/absolute/path.txt",  // Absolute path
		"../parent.txt",       // Parent directory traversal
		"./current.txt",       // Current directory reference
		"sub/dir/file.txt",    // Subdirectory
	}

	for _, filename := range invalidFilenames {
		if err := handler.validateFilename(filename); err == nil {
			t.Errorf("Expected filename %q to be invalid, but validation passed", filename)
		}
	}
}

func TestSecondaryFileHandler_CommandProcessing(t *testing.T) {
	// Create temporary directory for test files
	tempDir, err := os.MkdirTemp("", "secondary_handler_test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create test file
	testFile := filepath.Join(tempDir, "test.txt")
	testContent := []byte("Hello, World! This is a test file.")
	if err := os.WriteFile(testFile, testContent, 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create mock session
	mockSession := NewMockSession()

	// Create configuration
	config := &SecondaryFileHandlerConfig{
		FileDirectory:   tempDir,
		ChunkSize:       16,
		PrimaryServerID: "primary",
		ServerID:        "secondary",
	}

	// Create secondary file handler
	handler, err := NewSecondaryFileHandler(mockSession, config)
	if err != nil {
		t.Fatalf("Failed to create secondary file handler: %v", err)
	}
	defer handler.Close()

	// Create a chunk command
	command := &ChunkCommand{
		Command:      "SEND_CHUNK",
		ChunkID:      0,
		SequenceNum:  0,
		Filename:     "test.txt",
		StartOffset:  0,
		Size:         16,
		ChecksumType: "sha256",
		Metadata:     make(map[string]string),
	}

	// Create command ID
	commandID := "test.txt_0_0"

	// Create command state
	commandState := &CommandState{
		Command:    command,
		ReceivedAt: time.Now(),
		Status:     CommandStatusReceived,
	}

	// Add to active commands
	handler.commandsMutex.Lock()
	handler.activeCommands[commandID] = commandState
	handler.commandsMutex.Unlock()

	// Process the command
	handler.processChunkCommand(commandID, commandState)

	// Wait a bit for processing to complete
	time.Sleep(100 * time.Millisecond)

	// Verify command was processed
	handler.commandsMutex.RLock()
	finalState := handler.activeCommands[commandID]
	handler.commandsMutex.RUnlock()

	if finalState.Status != CommandStatusCompleted {
		t.Errorf("Expected command status to be COMPLETED, got %s", finalState.Status.String())
	}

	if finalState.ChunkData == nil {
		t.Error("Expected chunk data to be set")
	} else {
		expectedData := testContent[:16]
		if string(finalState.ChunkData.Data) != string(expectedData) {
			t.Errorf("Expected chunk data %q, got %q", string(expectedData), string(finalState.ChunkData.Data))
		}
	}
}

func TestSecondaryFileHandler_RawPacketIntegration(t *testing.T) {
	// Create temporary directory for test files
	tempDir, err := os.MkdirTemp("", "secondary_handler_test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create test file
	testFile := filepath.Join(tempDir, "test.txt")
	testContent := []byte("Hello, World! This is a test file.")
	if err := os.WriteFile(testFile, testContent, 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create mock session
	mockSession := NewMockSession()

	// Create configuration
	config := &SecondaryFileHandlerConfig{
		FileDirectory:   tempDir,
		ChunkSize:       16,
		PrimaryServerID: "primary",
		ServerID:        "secondary",
	}

	// Create secondary file handler
	handler, err := NewSecondaryFileHandler(mockSession, config)
	if err != nil {
		t.Fatalf("Failed to create secondary file handler: %v", err)
	}
	defer handler.Close()

	// Verify raw packet handler is available
	rawPacketHandler := handler.GetRawPacketHandler()
	if rawPacketHandler == nil {
		t.Fatal("Expected raw packet handler to be available")
	}

	if !handler.IsRawPacketHandlerRunning() {
		t.Error("Expected raw packet handler to be running")
	}

	// Test direct chunk command processing
	command := &ChunkCommand{
		Command:      "SEND_CHUNK",
		ChunkID:      0,
		SequenceNum:  0,
		Filename:     "test.txt",
		StartOffset:  0,
		Size:         16,
		ChecksumType: "sha256",
		Metadata:     make(map[string]string),
	}

	// Create mock stream for direct command
	mockStream := &MockStream{
		streamID: 1,
		pathID:   "primary",
		data:     make([]byte, 0),
		closed:   false,
	}

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
	time.Sleep(100 * time.Millisecond)

	// Verify command was processed
	commandID := "test.txt_0_0"
	commandState, err := handler.GetCommandStatus(commandID)
	if err != nil {
		t.Fatalf("Failed to get command status: %v", err)
	}

	if commandState.Status != CommandStatusCompleted {
		t.Errorf("Expected command status to be COMPLETED, got %s", commandState.Status.String())
	}
}

func TestSecondaryFileHandler_Close(t *testing.T) {
	// Create temporary directory for test files
	tempDir, err := os.MkdirTemp("", "secondary_handler_test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create mock session
	mockSession := NewMockSession()

	// Create configuration
	config := &SecondaryFileHandlerConfig{
		FileDirectory:   tempDir,
		ChunkSize:       32,
		PrimaryServerID: "primary",
		ServerID:        "secondary",
	}

	// Create secondary file handler
	handler, err := NewSecondaryFileHandler(mockSession, config)
	if err != nil {
		t.Fatalf("Failed to create secondary file handler: %v", err)
	}

	// Verify raw packet handler is running
	if !handler.IsRawPacketHandlerRunning() {
		t.Error("Expected raw packet handler to be running")
	}

	// Close handler
	err = handler.Close()
	if err != nil {
		t.Errorf("Failed to close handler: %v", err)
	}

	// Verify raw packet handler is stopped
	if handler.IsRawPacketHandlerRunning() {
		t.Error("Expected raw packet handler to be stopped after close")
	}

	// Verify active commands are cleared
	activeCommands := handler.GetActiveCommands()
	if len(activeCommands) != 0 {
		t.Errorf("Expected no active commands after close, got %d", len(activeCommands))
	}
}