package server

import (
	"encoding/json"
	"ftpa/internal/config"
	"ftpa/internal/utils"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Test helper functions

func createTestFile(t *testing.T, dir, filename, content string) {
	filePath := filepath.Join(dir, filename)
	err := os.WriteFile(filePath, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
}

func createTestDir(t *testing.T) string {
	dir, err := os.MkdirTemp("", "filetransfer_test_")
	if err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}
	return dir
}

func cleanupTestDir(t *testing.T, dir string) {
	err := os.RemoveAll(dir)
	if err != nil {
		t.Errorf("Failed to cleanup test directory: %v", err)
	}
}

// Tests

func TestNewFileTransferServer(t *testing.T) {
	testDir := createTestDir(t)
	defer cleanupTestDir(t, testDir)
	
	// Create server configuration
	serverConfig := config.DefaultServerConfiguration()
	serverConfig.Server.FileDirectory = testDir
	serverConfig.Limits.MaxFileSize = 1024 * 1024
	serverConfig.Limits.AllowedExtensions = []string{".txt", ".json"}
	serverConfig.Performance.ChunkSize = 1024
	serverConfig.Limits.MaxConcurrent = 5
	serverConfig.Performance.SecondaryPaths = []string{"secondary1"}
	
	server, err := NewFileTransferServer(serverConfig)
	if err != nil {
		t.Fatalf("Failed to create FileTransferServer: %v", err)
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

func TestNewFileTransferServerInvalidConfig(t *testing.T) {
	// Test nil config
	_, err := NewFileTransferServer(nil)
	if err == nil {
		t.Error("Expected error for nil config")
	}
	
	// Test empty file directory
	serverConfig := config.DefaultServerConfiguration()
	serverConfig.Server.FileDirectory = ""
	_, err = NewFileTransferServer(serverConfig)
	if err == nil {
		t.Error("Expected error for empty file directory")
	}
	
	// Test non-existent directory
	serverConfig2 := config.DefaultServerConfiguration()
	serverConfig2.Server.FileDirectory = "/non/existent/directory"
	_, err = NewFileTransferServer(serverConfig2)
	if err == nil {
		t.Error("Expected error for non-existent directory")
	}
}

func TestValidateFilename(t *testing.T) {
	testDir := createTestDir(t)
	defer cleanupTestDir(t, testDir)
	
	// Create server configuration
	serverConfig := config.DefaultServerConfiguration()
	serverConfig.Server.FileDirectory = testDir
	serverConfig.Limits.AllowedExtensions = []string{".txt"}
	
	server, err := NewFileTransferServer(serverConfig)
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

func TestAnalyzeFile(t *testing.T) {
	testDir := createTestDir(t)
	defer cleanupTestDir(t, testDir)
	
	// Create test file
	testContent := "Hello, World! This is a test file."
	createTestFile(t, testDir, "test.txt", testContent)
	
	// Create server configuration
	serverConfig := config.DefaultServerConfiguration()
	serverConfig.Server.FileDirectory = testDir
	serverConfig.Performance.ChunkSize = 10
	
	server, err := NewFileTransferServer(serverConfig)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()
	
	// Test analyzing existing file
	metadata, err := server.analyzeFile("test.txt")
	if err != nil {
		t.Fatalf("Failed to analyze file: %v", err)
	}
	
	if metadata.Filename != "test.txt" {
		t.Errorf("Expected filename test.txt, got %s", metadata.Filename)
	}
	
	if metadata.Size != int64(len(testContent)) {
		t.Errorf("Expected size %d, got %d", len(testContent), metadata.Size)
	}
	
	if metadata.ChunkSize != 10 {
		t.Errorf("Expected chunk size 10, got %d", metadata.ChunkSize)
	}
	
	expectedChunks := uint32((len(testContent) + 9) / 10) // Ceiling division
	if metadata.TotalChunks != expectedChunks {
		t.Errorf("Expected %d chunks, got %d", expectedChunks, metadata.TotalChunks)
	}
	
	if metadata.Checksum == "" {
		t.Error("Expected checksum to be calculated")
	}
	
	// Test analyzing non-existent file
	_, err = server.analyzeFile("nonexistent.txt")
	if err == nil {
		t.Error("Expected error for non-existent file")
	}
}

func TestHandleFileRequest(t *testing.T) {
	testDir := createTestDir(t)
	defer cleanupTestDir(t, testDir)
	
	// Create test file
	testContent := "Hello, World!"
	createTestFile(t, testDir, "test.txt", testContent)
	
	// Create server configuration
	serverConfig := config.DefaultServerConfiguration()
	serverConfig.Server.FileDirectory = testDir
	serverConfig.Performance.ChunkSize = 5
	serverConfig.Limits.MaxFileSize = 1024
	
	server, err := NewFileTransferServer(serverConfig)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()
	
	// Since the server now manages its own session, we need to mock the session creation
	// For testing purposes, we'll create a mock session and assign it directly
	mockSession := utils.NewMockSession()
	server.session = mockSession
	
	// Create chunk coordinator manually for testing since Start() is not called
	chunkCoordinator, err := NewChunkCoordinator(
		mockSession,
		serverConfig.Performance.SecondaryPaths,
		serverConfig.Server.FileDirectory,
		serverConfig.Performance.ChunkSize,
		serverConfig.Limits.MaxConcurrent,
	)
	if err != nil {
		t.Fatalf("Failed to create chunk coordinator: %v", err)
	}
	server.chunkCoordinator = chunkCoordinator
	defer chunkCoordinator.Close()
	
	// Create mock stream with file request
	stream := &utils.MockStream{}
	stream.SetStreamID(1)
	stream.SetPathID("primary")
	request := map[string]interface{}{
		"type":     "FILE_REQUEST",
		"filename": "test.txt",
	}
	requestData, _ := json.Marshal(request)
	stream.SetData(requestData)
	
	// Store initial data length to separate request from response
	initialData := stream.GetData()
	initialDataLen := len(initialData)
	
	// Handle the request
	server.handleFileRequest(stream, request)
	
	// Check that metadata response was sent
	currentData := stream.GetData()
	if len(currentData) <= initialDataLen {
		t.Error("Expected metadata response to be written")
	}
	
	// Extract only the response data (after the request data)
	responseData := currentData[initialDataLen:]
	
	// Parse response
	var response map[string]interface{}
	err = json.Unmarshal(responseData, &response)
	if err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}
	
	// Check response type
	responseType, ok := response["type"].(string)
	if !ok || responseType != "FILE_METADATA" {
		t.Errorf("Expected FILE_METADATA response, got %v", responseType)
	}
	
	// Check filename in response
	filename, ok := response["filename"].(string)
	if !ok || filename != "test.txt" {
		t.Errorf("Expected filename test.txt, got %v", filename)
	}
	
	// Check that request was added to active requests
	activeRequests := server.GetActiveRequests()
	if len(activeRequests) != 1 {
		t.Errorf("Expected 1 active request, got %d", len(activeRequests))
	}
}

func TestHandleFileRequestErrors(t *testing.T) {
	testDir := createTestDir(t)
	defer cleanupTestDir(t, testDir)
	
	// Create server configuration
	serverConfig := config.DefaultServerConfiguration()
	serverConfig.Server.FileDirectory = testDir
	serverConfig.Limits.MaxFileSize = 10 // Very small limit
	
	server, err := NewFileTransferServer(serverConfig)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()
	
	// Mock the session for testing
	mockSession := utils.NewMockSession()
	server.session = mockSession
	
	// Test missing filename
	stream := &utils.MockStream{}
	stream.SetStreamID(1)
	stream.SetPathID("primary")
	request := map[string]interface{}{
		"type": "FILE_REQUEST",
	}
	
	server.handleFileRequest(stream, request)
	
	writtenData := stream.GetData()
	if len(writtenData) == 0 {
		t.Error("Expected error response to be written")
	}
	
	var response map[string]interface{}
	json.Unmarshal(writtenData, &response)
	
	if response["type"] != "ERROR" {
		t.Error("Expected ERROR response for missing filename")
	}
	
	// Test non-existent file
	stream2 := &utils.MockStream{}
	stream2.SetStreamID(2)
	stream2.SetPathID("primary")
	request2 := map[string]interface{}{
		"type":     "FILE_REQUEST",
		"filename": "nonexistent.txt",
	}
	
	server.handleFileRequest(stream2, request2)
	
	writtenData2 := stream2.GetData()
	if len(writtenData2) == 0 {
		t.Error("Expected error response to be written")
	}
	
	var response2 map[string]interface{}
	json.Unmarshal(writtenData2, &response2)
	
	if response2["type"] != "ERROR" {
		t.Error("Expected ERROR response for non-existent file")
	}
	
	// Test file too large
	createTestFile(t, testDir, "large.txt", "This file is too large for the configured limit")
	
	stream3 := &utils.MockStream{}
	stream3.SetStreamID(3)
	stream3.SetPathID("primary")
	request3 := map[string]interface{}{
		"type":     "FILE_REQUEST",
		"filename": "large.txt",
	}
	
	server.handleFileRequest(stream3, request3)
	
	writtenData3 := stream3.GetData()
	if len(writtenData3) == 0 {
		t.Error("Expected error response to be written")
	}
	
	var response3 map[string]interface{}
	json.Unmarshal(writtenData3, &response3)
	
	if response3["type"] != "ERROR" {
		t.Error("Expected ERROR response for file too large")
	}
	
	errorMsg, ok := response3["error"].(string)
	if !ok || !strings.Contains(errorMsg, "too large") {
		t.Errorf("Expected 'too large' error message, got: %v", errorMsg)
	}
}

func TestGetContentType(t *testing.T) {
	testDir := createTestDir(t)
	defer cleanupTestDir(t, testDir)
	
	// Create server configuration
	serverConfig := config.DefaultServerConfiguration()
	serverConfig.Server.FileDirectory = testDir
	
	server, err := NewFileTransferServer(serverConfig)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()
	
	tests := []struct {
		filename    string
		expectedType string
	}{
		{"test.txt", "text/plain"},
		{"data.json", "application/json"},
		{"document.pdf", "application/pdf"},
		{"image.jpg", "image/jpeg"},
		{"image.png", "image/png"},
		{"video.mp4", "video/mp4"},
		{"unknown.xyz", "application/octet-stream"},
		{"noextension", "application/octet-stream"},
	}
	
	for _, test := range tests {
		contentType := server.getContentType(test.filename)
		if contentType != test.expectedType {
			t.Errorf("For filename %s, expected content type %s, got %s",
				test.filename, test.expectedType, contentType)
		}
	}
}

func TestServerCleanup(t *testing.T) {
	testDir := createTestDir(t)
	defer cleanupTestDir(t, testDir)
	
	// Create server configuration
	serverConfig := config.DefaultServerConfiguration()
	serverConfig.Server.FileDirectory = testDir
	
	server, err := NewFileTransferServer(serverConfig)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	
	// Add some mock active requests
	server.activeRequests["test1"] = &RequestState{
		Filename:    "test1.txt",
		RequestedAt: time.Now().Add(-20 * time.Minute), // Old request
		Status:      RequestStatusCompleted,
	}
	
	server.activeRequests["test2"] = &RequestState{
		Filename:    "test2.txt",
		RequestedAt: time.Now(),
		Status:      RequestStatusTransferring,
	}
	
	// Test cleanup
	server.cleanupStaleRequests()
	
	// Check that old completed request was removed
	if _, exists := server.activeRequests["test1"]; exists {
		t.Error("Expected old completed request to be cleaned up")
	}
	
	// Check that active request remains
	if _, exists := server.activeRequests["test2"]; !exists {
		t.Error("Expected active request to remain")
	}
	
	// Test server close
	err = server.Close()
	if err != nil {
		t.Errorf("Failed to close server: %v", err)
	}
	
	// Check that all requests were cleaned up
	server.requestsMutex.RLock()
	requestCount := len(server.activeRequests)
	server.requestsMutex.RUnlock()
	
	if requestCount != 0 {
		t.Error("Expected all requests to be cleaned up after close")
	}
}