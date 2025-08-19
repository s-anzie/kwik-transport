package filetransfer

import (
	"context"
	"testing"
	"time"

	"kwik/pkg/session"
)

func TestNewKwikFileTransferClient(t *testing.T) {
	session := NewMockSession()
	outputDir := "/tmp/test_downloads"
	chunkTimeout := 5 * time.Second
	maxRetries := 3

	client, err := NewKwikFileTransferClient(session, outputDir, chunkTimeout, maxRetries)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	if client == nil {
		t.Fatal("Client should not be nil")
	}

	if client.session != session {
		t.Error("Session not set correctly")
	}

	if client.outputDir != outputDir {
		t.Error("Output directory not set correctly")
	}

	if client.chunkTimeout != chunkTimeout {
		t.Error("Chunk timeout not set correctly")
	}

	if client.maxRetries != maxRetries {
		t.Error("Max retries not set correctly")
	}

	// Clean up
	client.Close()
}

func TestFileTransferClient_DownloadFile(t *testing.T) {
	session := NewMockSession()
	client, err := NewKwikFileTransferClient(session, "/tmp/test_downloads", 5*time.Second, 3)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Test successful download initiation
	var progressCalled bool
	progressCallback := func(progress float64) {
		progressCalled = true
	}

	err = client.DownloadFile("test.txt", progressCallback)
	if err != nil {
		t.Fatalf("DownloadFile should not return error: %v", err)
	}

	// Check that download was added to active downloads
	activeDownloads := client.GetActiveDownloads()
	if len(activeDownloads) != 1 {
		t.Errorf("Expected 1 active download, got %d", len(activeDownloads))
	}

	if activeDownloads[0] != "test.txt" {
		t.Errorf("Expected active download 'test.txt', got '%s'", activeDownloads[0])
	}

	// Test duplicate download prevention
	err = client.DownloadFile("test.txt", progressCallback)
	if err == nil {
		t.Error("DownloadFile should return error for duplicate download")
	}

	// Wait a bit for background processing
	time.Sleep(100 * time.Millisecond)

	// Note: progressCalled would be true if progress callback was invoked during transfer
	_ = progressCalled // Acknowledge the variable is used for testing purposes
}

func TestFileTransferClient_GetDownloadProgress(t *testing.T) {
	session := NewMockSession()
	client, err := NewKwikFileTransferClient(session, "/tmp/test_downloads", 5*time.Second, 3)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Test progress for non-existent download
	_, err = client.GetDownloadProgress("nonexistent.txt")
	if err == nil {
		t.Error("GetDownloadProgress should return error for non-existent download")
	}

	// Start a download
	err = client.DownloadFile("test.txt", nil)
	if err != nil {
		t.Fatalf("Failed to start download: %v", err)
	}

	// Get progress for active download
	progress, err := client.GetDownloadProgress("test.txt")
	if err != nil {
		t.Fatalf("GetDownloadProgress should not return error: %v", err)
	}

	if progress == nil {
		t.Fatal("Progress should not be nil")
	}

	if progress.Filename != "test.txt" {
		t.Errorf("Expected filename 'test.txt', got '%s'", progress.Filename)
	}

	// Since we haven't received metadata yet, these should be zero
	if progress.TotalChunks != 0 {
		t.Errorf("Expected TotalChunks 0, got %d", progress.TotalChunks)
	}

	if progress.ReceivedChunks != 0 {
		t.Errorf("Expected ReceivedChunks 0, got %d", progress.ReceivedChunks)
	}
}

func TestFileTransferClient_CancelDownload(t *testing.T) {
	session := NewMockSession()
	client, err := NewKwikFileTransferClient(session, "/tmp/test_downloads", 5*time.Second, 3)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Test cancel non-existent download
	err = client.CancelDownload("nonexistent.txt")
	if err == nil {
		t.Error("CancelDownload should return error for non-existent download")
	}

	// Start a download
	err = client.DownloadFile("test.txt", nil)
	if err != nil {
		t.Fatalf("Failed to start download: %v", err)
	}

	// Verify download is active
	activeDownloads := client.GetActiveDownloads()
	if len(activeDownloads) != 1 {
		t.Fatalf("Expected 1 active download, got %d", len(activeDownloads))
	}

	// Cancel the download
	err = client.CancelDownload("test.txt")
	if err != nil {
		t.Fatalf("CancelDownload should not return error: %v", err)
	}

	// Verify download was removed
	activeDownloads = client.GetActiveDownloads()
	if len(activeDownloads) != 0 {
		t.Errorf("Expected 0 active downloads after cancel, got %d", len(activeDownloads))
	}

	// Test getting progress after cancel
	_, err = client.GetDownloadProgress("test.txt")
	if err == nil {
		t.Error("GetDownloadProgress should return error after cancel")
	}
}

func TestFileTransferClient_GetDownloadStatus(t *testing.T) {
	session := NewMockSession()
	client, err := NewKwikFileTransferClient(session, "/tmp/test_downloads", 5*time.Second, 3)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Test status for non-existent download
	_, err = client.GetDownloadStatus("nonexistent.txt")
	if err == nil {
		t.Error("GetDownloadStatus should return error for non-existent download")
	}

	// Start a download
	err = client.DownloadFile("test.txt", nil)
	if err != nil {
		t.Fatalf("Failed to start download: %v", err)
	}

	// Get status
	status, err := client.GetDownloadStatus("test.txt")
	if err != nil {
		t.Fatalf("GetDownloadStatus should not return error: %v", err)
	}

	if status != DownloadStatusRequesting {
		t.Errorf("Expected status %v, got %v", DownloadStatusRequesting, status)
	}
}

func TestFileTransferClient_ProgressCallback(t *testing.T) {
	session := NewMockSession()
	client, err := NewKwikFileTransferClient(session, "/tmp/test_downloads", 5*time.Second, 3)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Test with progress callback
	var lastProgress float64
	progressCallback := func(progress float64) {
		lastProgress = progress
	}

	err = client.DownloadFile("test.txt", progressCallback)
	if err != nil {
		t.Fatalf("DownloadFile should not return error: %v", err)
	}

	// Verify callback was stored
	client.downloadsMutex.RLock()
	downloadState, exists := client.activeDownloads["test.txt"]
	client.downloadsMutex.RUnlock()

	if !exists {
		t.Fatal("Download state should exist")
	}

	if downloadState.ProgressCallback == nil {
		t.Error("Progress callback should be stored")
	}

	// Test callback execution (simulate)
	if downloadState.ProgressCallback != nil {
		downloadState.ProgressCallback(0.5)
		if lastProgress != 0.5 {
			t.Errorf("Expected progress 0.5, got %f", lastProgress)
		}
	}
}

func TestFileTransferClient_MultipleDownloads(t *testing.T) {
	session := NewMockSession()
	client, err := NewKwikFileTransferClient(session, "/tmp/test_downloads", 5*time.Second, 3)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Start multiple downloads
	files := []string{"file1.txt", "file2.txt", "file3.txt"}

	for _, filename := range files {
		err = client.DownloadFile(filename, nil)
		if err != nil {
			t.Fatalf("Failed to start download for %s: %v", filename, err)
		}
	}

	// Verify all downloads are active
	activeDownloads := client.GetActiveDownloads()
	if len(activeDownloads) != len(files) {
		t.Errorf("Expected %d active downloads, got %d", len(files), len(activeDownloads))
	}

	// Verify each file is in active downloads
	activeMap := make(map[string]bool)
	for _, filename := range activeDownloads {
		activeMap[filename] = true
	}

	for _, filename := range files {
		if !activeMap[filename] {
			t.Errorf("File %s should be in active downloads", filename)
		}
	}

	// Cancel one download
	err = client.CancelDownload("file2.txt")
	if err != nil {
		t.Fatalf("Failed to cancel download: %v", err)
	}

	// Verify remaining downloads
	activeDownloads = client.GetActiveDownloads()
	if len(activeDownloads) != len(files)-1 {
		t.Errorf("Expected %d active downloads after cancel, got %d", len(files)-1, len(activeDownloads))
	}
}

func TestFileTransferClient_Close(t *testing.T) {
	session := NewMockSession()
	client, err := NewKwikFileTransferClient(session, "/tmp/test_downloads", 5*time.Second, 3)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Start some downloads
	err = client.DownloadFile("file1.txt", nil)
	if err != nil {
		t.Fatalf("Failed to start download: %v", err)
	}

	err = client.DownloadFile("file2.txt", nil)
	if err != nil {
		t.Fatalf("Failed to start download: %v", err)
	}

	// Verify downloads are active
	activeDownloads := client.GetActiveDownloads()
	if len(activeDownloads) != 2 {
		t.Fatalf("Expected 2 active downloads, got %d", len(activeDownloads))
	}

	// Close the client
	err = client.Close()
	if err != nil {
		t.Fatalf("Close should not return error: %v", err)
	}

	// Verify all downloads were cancelled
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
	*MockSession
}

func NewMockKwikSession() *MockKwikSession {
	return &MockKwikSession{
		MockSession: NewMockSession(),
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

func TestCreateFileTransferClientWithKwikSession(t *testing.T) {
	mockKwikSession := NewMockKwikSession()

	client, err := NewKwikFileTransferClient(
		mockKwikSession,
		"/tmp/test_downloads",
		5*time.Second,
		3,
	)

	if err != nil {
		t.Fatalf("CreateFileTransferClientWithKwikSession should not return error: %v", err)
	}

	if client == nil {
		t.Fatal("Client should not be nil")
	}

	// Test that the client works with the KWIK session
	err = client.DownloadFile("test.txt", nil)
	if err != nil {
		t.Fatalf("DownloadFile should not return error: %v", err)
	}

	// Verify download was started
	activeDownloads := client.GetActiveDownloads()
	if len(activeDownloads) != 1 {
		t.Errorf("Expected 1 active download, got %d", len(activeDownloads))
	}

	// Clean up
	client.Close()
}
