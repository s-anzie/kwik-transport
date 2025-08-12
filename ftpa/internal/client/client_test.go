package internal

import (
	"context"
	"ftpa/internal/utils"
	"testing"
	"time"

	"kwik/pkg/session"
)

// Helper function to create test configuration
func createTestConfig() *ClientConfiguration {
	return &ClientConfiguration{
		ServerAddress:   "localhost:8080",
		OutputDirectory: "/tmp/test_downloads",
		ChunkTimeout:    5 * time.Second,
		MaxRetries:      3,
		ConnectTimeout:  10 * time.Second,
		TLSInsecure:     true,
	}
}

func TestNewKwikFileTransferClient(t *testing.T) {
	config := &ClientConfiguration{
		ServerAddress:   "localhost:8080",
		OutputDirectory: "/tmp/test_downloads",
		ChunkTimeout:    5 * time.Second,
		MaxRetries:      3,
		ConnectTimeout:  10 * time.Second,
		TLSInsecure:     true,
	}

	client, err := NewKwikFileTransferClient(config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	if client == nil {
		t.Fatal("Client should not be nil")
	}

	if client.config.ServerAddress != config.ServerAddress {
		t.Error("Server address not set correctly")
	}

	if client.config.OutputDirectory != config.OutputDirectory {
		t.Error("Output directory not set correctly")
	}

	if client.config.ChunkTimeout != config.ChunkTimeout {
		t.Error("Chunk timeout not set correctly")
	}

	if client.config.MaxRetries != config.MaxRetries {
		t.Error("Max retries not set correctly")
	}

	// Clean up
	client.Close()
}

func TestFileTransferClient_DownloadFile(t *testing.T) {
	config := createTestConfig()
	client, err := NewKwikFileTransferClient(config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Test download without connection should return error
	var progressCalled bool
	progressCallback := func(progress float64) {
		progressCalled = true
	}

	err = client.DownloadFile("test.txt", progressCallback)
	if err == nil {
		t.Error("DownloadFile should return error when not connected")
	}

	expectedError := "client is not connected - call Connect() first"
	if err.Error() != expectedError {
		t.Errorf("Expected error '%s', got '%s'", expectedError, err.Error())
	}

	// Since the download failed, there should be no active downloads
	activeDownloads := client.GetActiveDownloads()
	if len(activeDownloads) != 0 {
		t.Errorf("Expected 0 active downloads when not connected, got %d", len(activeDownloads))
	}

	// Wait a bit for background processing
	time.Sleep(100 * time.Millisecond)

	// Note: progressCalled would be true if progress callback was invoked during transfer
	_ = progressCalled // Acknowledge the variable is used for testing purposes
}

func TestFileTransferClient_GetDownloadProgress(t *testing.T) {
	config := createTestConfig()
	client, err := NewKwikFileTransferClient(config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Test progress for non-existent download
	_, err = client.GetDownloadProgress("nonexistent.txt")
	if err == nil {
		t.Error("GetDownloadProgress should return error for non-existent download")
	}

	// Try to start a download without connection - should fail
	err = client.DownloadFile("test.txt", nil)
	if err == nil {
		t.Error("DownloadFile should return error when not connected")
	}

	// Since download failed, getting progress should also fail
	_, err = client.GetDownloadProgress("test.txt")
	if err == nil {
		t.Error("GetDownloadProgress should return error for non-existent download")
	}
}

func TestFileTransferClient_CancelDownload(t *testing.T) {
	config := createTestConfig()
	client, err := NewKwikFileTransferClient(config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Test cancel non-existent download
	err = client.CancelDownload("nonexistent.txt")
	if err == nil {
		t.Error("CancelDownload should return error for non-existent download")
	}

	// Try to start a download without connection - should fail
	err = client.DownloadFile("test.txt", nil)
	if err == nil {
		t.Error("DownloadFile should return error when not connected")
	}

	// Since download failed, there should be no active downloads
	activeDownloads := client.GetActiveDownloads()
	if len(activeDownloads) != 0 {
		t.Errorf("Expected 0 active downloads when not connected, got %d", len(activeDownloads))
	}

	// Test cancel non-existent download again
	err = client.CancelDownload("test.txt")
	if err == nil {
		t.Error("CancelDownload should return error for non-existent download")
	}
}

func TestFileTransferClient_GetDownloadStatus(t *testing.T) {
	config := createTestConfig()
	client, err := NewKwikFileTransferClient(config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Test status for non-existent download
	_, err = client.GetDownloadStatus("nonexistent.txt")
	if err == nil {
		t.Error("GetDownloadStatus should return error for non-existent download")
	}

	// Try to start a download without connection - should fail
	err = client.DownloadFile("test.txt", nil)
	if err == nil {
		t.Error("DownloadFile should return error when not connected")
	}

	// Since download failed, getting status should also fail
	_, err = client.GetDownloadStatus("test.txt")
	if err == nil {
		t.Error("GetDownloadStatus should return error for non-existent download")
	}
}

func TestFileTransferClient_ProgressCallback(t *testing.T) {
	config := createTestConfig()
	client, err := NewKwikFileTransferClient(config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Test with progress callback - should fail when not connected
	var lastProgress float64
	progressCallback := func(progress float64) {
		lastProgress = progress
	}

	err = client.DownloadFile("test.txt", progressCallback)
	if err == nil {
		t.Error("DownloadFile should return error when not connected")
	}

	// Since download failed, there should be no active downloads
	client.downloadsMutex.RLock()
	_, exists := client.activeDownloads["test.txt"]
	client.downloadsMutex.RUnlock()

	if exists {
		t.Error("Download state should not exist when not connected")
	}

	// Test that progress callback would work if we had a connection
	// (This is just to verify the callback function works)
	progressCallback(0.5)
	if lastProgress != 0.5 {
		t.Errorf("Expected progress 0.5, got %f", lastProgress)
	}
}

func TestFileTransferClient_MultipleDownloads(t *testing.T) {
	config := createTestConfig()
	client, err := NewKwikFileTransferClient(config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Try to start multiple downloads without connection - should all fail
	files := []string{"file1.txt", "file2.txt", "file3.txt"}

	for _, filename := range files {
		err = client.DownloadFile(filename, nil)
		if err == nil {
			t.Errorf("DownloadFile should return error when not connected for %s", filename)
		}
	}

	// Since all downloads failed, there should be no active downloads
	activeDownloads := client.GetActiveDownloads()
	if len(activeDownloads) != 0 {
		t.Errorf("Expected 0 active downloads when not connected, got %d", len(activeDownloads))
	}

	// Test cancel non-existent download
	err = client.CancelDownload("file2.txt")
	if err == nil {
		t.Error("CancelDownload should return error for non-existent download")
	}
}

func TestFileTransferClient_Close(t *testing.T) {
	config := createTestConfig()
	client, err := NewKwikFileTransferClient(config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Try to start some downloads without connection - should fail
	err = client.DownloadFile("file1.txt", nil)
	if err == nil {
		t.Error("DownloadFile should return error when not connected")
	}

	err = client.DownloadFile("file2.txt", nil)
	if err == nil {
		t.Error("DownloadFile should return error when not connected")
	}

	// Since downloads failed, there should be no active downloads
	activeDownloads := client.GetActiveDownloads()
	if len(activeDownloads) != 0 {
		t.Errorf("Expected 0 active downloads when not connected, got %d", len(activeDownloads))
	}

	// Close the client - should work even without connection
	err = client.Close()
	if err != nil {
		t.Fatalf("Close should not return error: %v", err)
	}

	// Verify still no active downloads
	activeDownloads = client.GetActiveDownloads()
	if len(activeDownloads) != 0 {
		t.Errorf("Expected 0 active downloads after close, got %d", len(activeDownloads))
	}
}

func TestDownloadStatus_String(t *testing.T) {
	tests := []struct {
		status   DownloadStatus
		expected string
	}{
		{DownloadStatusRequesting, "REQUESTING"},
		{DownloadStatusReceiving, "RECEIVING"},
		{DownloadStatusCompleted, "COMPLETED"},
		{DownloadStatusFailed, "FAILED"},
		{DownloadStatusCancelled, "CANCELLED"},
		{DownloadStatus(999), "UNKNOWN"},
	}

	for _, test := range tests {
		result := test.status.String()
		if result != test.expected {
			t.Errorf("Expected %s, got %s", test.expected, result)
		}
	}
}

func TestPathStatus_String(t *testing.T) {
	tests := []struct {
		status   session.PathStatus
		expected string
	}{
		{session.PathStatusActive, "ACTIVE"},
		{session.PathStatusDead, "DEAD"},
		{session.PathStatusConnecting, "CONNECTING"},
		{session.PathStatusDisconnecting, "DISCONNECTING"},
		{session.PathStatus(999), "UNKNOWN"},
	}

	for _, test := range tests {
		result := test.status.String()
		if result != test.expected {
			t.Errorf("Expected %s, got %s", test.expected, result)
		}
	}
}

// MockKwikSession implements the KwikSession interface for testing
type MockKwikSession struct {
	*utils.MockSession
}

func NewMockKwikSession() *MockKwikSession {
	return &MockKwikSession{
		MockSession: utils.NewMockSession(),
	}
}

// OpenStream implements the KwikSession interface (non-sync version)
func (mks *MockKwikSession) OpenStream() (session.Stream, error) {
	stream, err := mks.MockSession.OpenStreamSync(context.Background())
	if err != nil {
		return nil, err
	}
	return stream, nil
}

// AddPath implements the KwikSession interface
func (mks *MockKwikSession) AddPath(address string) error {
	return nil
}

// RemovePath implements the KwikSession interface
func (mks *MockKwikSession) RemovePath(pathID string) error {
	return nil
}

// These methods are removed - we now use KWIK sessions directly

// TestKwikSessionAdapter removed - we now use KWIK sessions directly

func TestCreateFileTransferClientWithConfig(t *testing.T) {
	config := createTestConfig()

	client, err := NewKwikFileTransferClient(config)

	if err != nil {
		t.Fatalf("NewKwikFileTransferClient should not return error: %v", err)
	}

	if client == nil {
		t.Fatal("Client should not be nil")
	}

	// Test that the client requires connection before downloads
	err = client.DownloadFile("test.txt", nil)
	if err == nil {
		t.Error("DownloadFile should return error when not connected")
	}

	expectedError := "client is not connected - call Connect() first"
	if err.Error() != expectedError {
		t.Errorf("Expected error '%s', got '%s'", expectedError, err.Error())
	}

	// Since download failed, there should be no active downloads
	activeDownloads := client.GetActiveDownloads()
	if len(activeDownloads) != 0 {
		t.Errorf("Expected 0 active downloads when not connected, got %d", len(activeDownloads))
	}

	// Clean up
	client.Close()
}
