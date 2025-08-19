package filetransfer

import (
	"encoding/json"
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
	
	session := NewMockSession()
	config := &FileTransferServerConfig{
		FileDirectory:     testDir,
		MaxFileSize:       1024 * 1024,
		AllowedExtensions: []string{".txt", ".json"},
		ChunkSize:         1024,
		MaxConcurrent:     5,
		SecondaryPaths:    []string{"secondary1"},
	}
	
	server, err := NewFileTransferServer(session, config)
	if err != nil {
		t.Fatalf("Failed to create FileTransferServer: %v", err)
	}
	
	if server == nil {
		t.Fatal("Server should not be nil")
	}
	
	// Test that server has correct configuration
	if server.fileDirectory != testDir {
		t.Errorf("Expected file directory %s, got %s", testDir, server.fileDirectory)
	}
	
	if server.maxFileSize != 1024*1024 {
		t.Errorf("Expected max file size %d, got %d", 1024*1024, server.maxFileSize)
	}
	
	// Cleanup
	server.Close()
}

func TestNewFileTransferServerInvalidConfig(t *testing.T) {
	session := NewMockSession()
	
	// Test nil session
	_, err := NewFileTransferServer(nil, &FileTransferServerConfig{})
	if err == nil {
		t.Error("Expected error for nil session")
	}
	
	// Test nil config
	_, err = NewFileTransferServer(session, nil)
	if err == nil {
		t.Error("Expected error for nil config")
	}
	
	// Test empty file directory
	_, err = NewFileTransferServer(session, &FileTransferServerConfig{
		FileDirectory: "",
	})
	if err == nil {
		t.Error("Expected error for empty file directory")
	}
	
	// Test non-existent directory
	_, err = NewFileTransferServer(session, &FileTransferServerConfig{
		FileDirectory: "/non/existent/directory",
	})
	if err == nil {
		t.Error("Expected error for non-existent directory")
	}
}

func TestValidateFilename(t *testing.T) {
	testDir := createTestDir(t)
	defer cleanupTestDir(t, testDir)
	
	session := NewMockSession()
	config := &FileTransferServerConfig{
		FileDirectory:     testDir,
		AllowedExtensions: []string{".txt"},
	}
	
	server, err := NewFileTransferServer(session, config)
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
	
	session := NewMockSession()
	config := &FileTransferServerConfig{
		FileDirectory: testDir,
		ChunkSize:     10,
	}
	
	server, err := NewFileTransferServer(session, config)
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
	
	session := NewMockSession()
	config := &FileTransferServerConfig{
		FileDirectory: testDir,
		ChunkSize:     5,
		MaxFileSize:   1024,
	}
	
	server, err := NewFileTransferServer(session, config)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()
	
	// Create mock stream with file request
	stream := &MockStream{
		streamID: 1,
		pathID:   "primary",
	}
	request := map[string]interface{}{
		"type":     "FILE_REQUEST",
		"filename": "test.txt",
	}
	requestData, _ := json.Marshal(request)
	stream.SetData(requestData)
	
	// Store initial data length to separate request from response
	initialDataLen := len(stream.data)
	
	// Handle the request
	server.handleFileRequest(stream, request)
	
	// Check that metadata response was sent
	if len(stream.data) <= initialDataLen {
		t.Error("Expected metadata response to be written")
	}
	
	// Extract only the response data (after the request data)
	responseData := stream.data[initialDataLen:]
	
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
	
	session := NewMockSession()
	config := &FileTransferServerConfig{
		FileDirectory: testDir,
		MaxFileSize:   10, // Very small limit
	}
	
	server, err := NewFileTransferServer(session, config)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()
	
	// Test missing filename
	stream := &MockStream{
		streamID: 1,
		pathID:   "primary",
	}
	request := map[string]interface{}{
		"type": "FILE_REQUEST",
	}
	
	server.handleFileRequest(stream, request)
	
	writtenData := stream.data
	if len(writtenData) == 0 {
		t.Error("Expected error response to be written")
	}
	
	var response map[string]interface{}
	json.Unmarshal(writtenData, &response)
	
	if response["type"] != "ERROR" {
		t.Error("Expected ERROR response for missing filename")
	}
	
	// Test non-existent file
	stream2 := &MockStream{
		streamID: 2,
		pathID:   "primary",
	}
	request2 := map[string]interface{}{
		"type":     "FILE_REQUEST",
		"filename": "nonexistent.txt",
	}
	
	server.handleFileRequest(stream2, request2)
	
	writtenData2 := stream2.data
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
	
	stream3 := &MockStream{
		streamID: 3,
		pathID:   "primary",
	}
	request3 := map[string]interface{}{
		"type":     "FILE_REQUEST",
		"filename": "large.txt",
	}
	
	server.handleFileRequest(stream3, request3)
	
	writtenData3 := stream3.data
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
	
	session := NewMockSession()
	config := &FileTransferServerConfig{
		FileDirectory: testDir,
	}
	
	server, err := NewFileTransferServer(session, config)
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
	
	session := NewMockSession()
	config := &FileTransferServerConfig{
		FileDirectory: testDir,
	}
	
	server, err := NewFileTransferServer(session, config)
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
	if len(server.activeRequests) != 0 {
		t.Error("Expected all requests to be cleaned up after close")
	}
}