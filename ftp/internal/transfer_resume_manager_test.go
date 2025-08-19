package filetransfer

import (
	"os"
	"testing"
	"time"
)

func TestNewTransferResumeManager(t *testing.T) {
	tempDir := t.TempDir()
	config := &ResumeConfig{
		StateDir:              tempDir,
		SaveInterval:          10 * time.Second,
		MaxResumeAttempts:     3,
		StateRetentionTime:    1 * time.Hour,
		EnablePartialChunks:   true,
		ValidateOnResume:      true,
		CompressStateFiles:    false,
		AutoCleanup:           false, // Disable for testing
		CleanupInterval:       30 * time.Minute,
	}

	manager, err := NewTransferResumeManager(config)
	if err != nil {
		t.Fatalf("Failed to create transfer resume manager: %v", err)
	}
	defer manager.Close()

	if manager.stateDir != tempDir {
		t.Errorf("Expected state dir %s, got %s", tempDir, manager.stateDir)
	}

	if manager.config.MaxResumeAttempts != 3 {
		t.Errorf("Expected max resume attempts 3, got %d", manager.config.MaxResumeAttempts)
	}
}

func TestNewTransferResumeManagerWithDefaultConfig(t *testing.T) {
	manager, err := NewTransferResumeManager(nil)
	if err != nil {
		t.Fatalf("Failed to create transfer resume manager with default config: %v", err)
	}
	defer manager.Close()

	defaultConfig := DefaultResumeConfig()
	if manager.config.MaxResumeAttempts != defaultConfig.MaxResumeAttempts {
		t.Errorf("Expected default max resume attempts %d, got %d", 
			defaultConfig.MaxResumeAttempts, manager.config.MaxResumeAttempts)
	}
}

func TestStartTransferNewTransfer(t *testing.T) {
	tempDir := t.TempDir()
	config := &ResumeConfig{
		StateDir:              tempDir,
		SaveInterval:          10 * time.Second,
		MaxResumeAttempts:     3,
		StateRetentionTime:    1 * time.Hour,
		EnablePartialChunks:   true,
		ValidateOnResume:      true,
		CompressStateFiles:    false,
		AutoCleanup:           false,
		CleanupInterval:       30 * time.Minute,
	}

	manager, err := NewTransferResumeManager(config)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}
	defer manager.Close()

	metadata := NewFileMetadata("test.txt", 1024, 256)
	metadata.Checksum = "abc123"

	state, isResume, err := manager.StartTransfer(metadata, "transfer-1")
	if err != nil {
		t.Fatalf("Failed to start transfer: %v", err)
	}

	if isResume {
		t.Errorf("Expected new transfer, got resume")
	}

	if state.Filename != "test.txt" {
		t.Errorf("Expected filename test.txt, got %s", state.Filename)
	}

	if state.FileSize != 1024 {
		t.Errorf("Expected file size 1024, got %d", state.FileSize)
	}

	if state.TotalChunks != 4 {
		t.Errorf("Expected 4 total chunks, got %d", state.TotalChunks)
	}

	if len(state.MissingChunks) != 4 {
		t.Errorf("Expected 4 missing chunks, got %d", len(state.MissingChunks))
	}

	if state.ResumeCount != 0 {
		t.Errorf("Expected resume count 0, got %d", state.ResumeCount)
	}
}

func TestStartTransferResumeExisting(t *testing.T) {
	tempDir := t.TempDir()
	config := &ResumeConfig{
		StateDir:              tempDir,
		SaveInterval:          10 * time.Second,
		MaxResumeAttempts:     3,
		StateRetentionTime:    1 * time.Hour,
		EnablePartialChunks:   true,
		ValidateOnResume:      true,
		CompressStateFiles:    false,
		AutoCleanup:           false,
		CleanupInterval:       30 * time.Minute,
	}

	manager, err := NewTransferResumeManager(config)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}
	defer manager.Close()

	metadata := NewFileMetadata("test.txt", 1024, 256)
	metadata.Checksum = "abc123"

	// Start initial transfer
	_, isResume1, err := manager.StartTransfer(metadata, "transfer-1")
	if err != nil {
		t.Fatalf("Failed to start initial transfer: %v", err)
	}

	if isResume1 {
		t.Errorf("Expected new transfer for initial start")
	}

	// Simulate receiving some chunks
	chunk1 := NewFileChunk(1, 0, []byte("test data 1"), "test.txt", 0, 4, false)
	err = manager.RecordChunkReceived("test.txt", chunk1)
	if err != nil {
		t.Fatalf("Failed to record chunk: %v", err)
	}

	// Save state
	err = manager.SaveTransferState("test.txt")
	if err != nil {
		t.Fatalf("Failed to save state: %v", err)
	}

	// Create new manager instance (simulating restart)
	manager2, err := NewTransferResumeManager(config)
	if err != nil {
		t.Fatalf("Failed to create second manager: %v", err)
	}
	defer manager2.Close()

	// Try to start same transfer - should resume
	state2, isResume2, err := manager2.StartTransfer(metadata, "transfer-2")
	if err != nil {
		t.Fatalf("Failed to resume transfer: %v", err)
	}

	if !isResume2 {
		t.Errorf("Expected resume transfer")
	}

	if state2.ResumeCount != 1 {
		t.Errorf("Expected resume count 1, got %d", state2.ResumeCount)
	}

	if len(state2.ReceivedChunks) != 1 {
		t.Errorf("Expected 1 received chunk, got %d", len(state2.ReceivedChunks))
	}

	if len(state2.MissingChunks) != 3 {
		t.Errorf("Expected 3 missing chunks, got %d", len(state2.MissingChunks))
	}
}

func TestRecordChunkReceived(t *testing.T) {
	tempDir := t.TempDir()
	config := &ResumeConfig{
		StateDir:              tempDir,
		SaveInterval:          10 * time.Second,
		MaxResumeAttempts:     3,
		StateRetentionTime:    1 * time.Hour,
		EnablePartialChunks:   true,
		ValidateOnResume:      true,
		CompressStateFiles:    false,
		AutoCleanup:           false,
		CleanupInterval:       30 * time.Minute,
	}

	manager, err := NewTransferResumeManager(config)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}
	defer manager.Close()

	metadata := NewFileMetadata("test.txt", 1024, 256)
	_, _, err = manager.StartTransfer(metadata, "transfer-1")
	if err != nil {
		t.Fatalf("Failed to start transfer: %v", err)
	}

	// Record chunk received
	chunk := NewFileChunk(1, 0, []byte("test data"), "test.txt", 0, 4, false)
	err = manager.RecordChunkReceived("test.txt", chunk)
	if err != nil {
		t.Fatalf("Failed to record chunk: %v", err)
	}

	// Check that chunk was recorded
	received, err := manager.GetReceivedChunks("test.txt")
	if err != nil {
		t.Fatalf("Failed to get received chunks: %v", err)
	}

	if len(received) != 1 {
		t.Errorf("Expected 1 received chunk, got %d", len(received))
	}

	chunkInfo, exists := received[0]
	if !exists {
		t.Errorf("Expected chunk 0 to be received")
	} else {
		if chunkInfo.SequenceNum != 0 {
			t.Errorf("Expected sequence num 0, got %d", chunkInfo.SequenceNum)
		}
		if chunkInfo.Size != int32(len("test data")) {
			t.Errorf("Expected size %d, got %d", len("test data"), chunkInfo.Size)
		}
	}

	// Check missing chunks updated
	missing, err := manager.GetMissingChunks("test.txt")
	if err != nil {
		t.Fatalf("Failed to get missing chunks: %v", err)
	}

	if len(missing) != 3 {
		t.Errorf("Expected 3 missing chunks, got %d", len(missing))
	}

	// Verify chunk 0 is not in missing list
	for _, seq := range missing {
		if seq == 0 {
			t.Errorf("Chunk 0 should not be in missing list")
		}
	}
}

func TestRecordPartialChunk(t *testing.T) {
	tempDir := t.TempDir()
	config := &ResumeConfig{
		StateDir:              tempDir,
		SaveInterval:          10 * time.Second,
		MaxResumeAttempts:     3,
		StateRetentionTime:    1 * time.Hour,
		EnablePartialChunks:   true,
		ValidateOnResume:      true,
		CompressStateFiles:    false,
		AutoCleanup:           false,
		CleanupInterval:       30 * time.Minute,
	}

	manager, err := NewTransferResumeManager(config)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}
	defer manager.Close()

	metadata := NewFileMetadata("test.txt", 1024, 256)
	_, _, err = manager.StartTransfer(metadata, "transfer-1")
	if err != nil {
		t.Fatalf("Failed to start transfer: %v", err)
	}

	// Record partial chunk
	partialData := []byte("partial")
	err = manager.RecordPartialChunk("test.txt", 0, partialData, 256)
	if err != nil {
		t.Fatalf("Failed to record partial chunk: %v", err)
	}

	// Get partial chunks
	partial, err := manager.GetPartialChunks("test.txt")
	if err != nil {
		t.Fatalf("Failed to get partial chunks: %v", err)
	}

	if len(partial) != 1 {
		t.Errorf("Expected 1 partial chunk, got %d", len(partial))
	}

	partialChunk, exists := partial[0]
	if !exists {
		t.Errorf("Expected partial chunk 0 to exist")
	} else {
		if partialChunk.SequenceNum != 0 {
			t.Errorf("Expected sequence num 0, got %d", partialChunk.SequenceNum)
		}
		if partialChunk.ExpectedSize != 256 {
			t.Errorf("Expected expected size 256, got %d", partialChunk.ExpectedSize)
		}
		if partialChunk.ReceivedBytes != int32(len(partialData)) {
			t.Errorf("Expected received bytes %d, got %d", len(partialData), partialChunk.ReceivedBytes)
		}
		if string(partialChunk.Data) != string(partialData) {
			t.Errorf("Expected data %s, got %s", string(partialData), string(partialChunk.Data))
		}
	}
}

func TestRecordPartialChunkDisabled(t *testing.T) {
	tempDir := t.TempDir()
	config := &ResumeConfig{
		StateDir:              tempDir,
		SaveInterval:          10 * time.Second,
		MaxResumeAttempts:     3,
		StateRetentionTime:    1 * time.Hour,
		EnablePartialChunks:   false, // Disabled
		ValidateOnResume:      true,
		CompressStateFiles:    false,
		AutoCleanup:           false,
		CleanupInterval:       30 * time.Minute,
	}

	manager, err := NewTransferResumeManager(config)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}
	defer manager.Close()

	metadata := NewFileMetadata("test.txt", 1024, 256)
	_, _, err = manager.StartTransfer(metadata, "transfer-1")
	if err != nil {
		t.Fatalf("Failed to start transfer: %v", err)
	}

	// Try to record partial chunk - should be ignored
	partialData := []byte("partial")
	err = manager.RecordPartialChunk("test.txt", 0, partialData, 256)
	if err != nil {
		t.Fatalf("Failed to record partial chunk: %v", err)
	}

	// Get partial chunks - should be empty
	partial, err := manager.GetPartialChunks("test.txt")
	if err != nil {
		t.Fatalf("Failed to get partial chunks: %v", err)
	}

	if len(partial) != 0 {
		t.Errorf("Expected 0 partial chunks when disabled, got %d", len(partial))
	}
}

func TestGetTransferProgress(t *testing.T) {
	tempDir := t.TempDir()
	config := &ResumeConfig{
		StateDir:              tempDir,
		SaveInterval:          10 * time.Second,
		MaxResumeAttempts:     3,
		StateRetentionTime:    1 * time.Hour,
		EnablePartialChunks:   true,
		ValidateOnResume:      true,
		CompressStateFiles:    false,
		AutoCleanup:           false,
		CleanupInterval:       30 * time.Minute,
	}

	manager, err := NewTransferResumeManager(config)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}
	defer manager.Close()

	metadata := NewFileMetadata("test.txt", 1024, 256)
	_, _, err = manager.StartTransfer(metadata, "transfer-1")
	if err != nil {
		t.Fatalf("Failed to start transfer: %v", err)
	}

	// Record some chunks
	chunk1 := NewFileChunk(1, 0, []byte("data1"), "test.txt", 0, 4, false)
	chunk2 := NewFileChunk(2, 1, []byte("data2"), "test.txt", 256, 4, false)
	
	manager.RecordChunkReceived("test.txt", chunk1)
	manager.RecordChunkReceived("test.txt", chunk2)

	// Record partial chunk
	manager.RecordPartialChunk("test.txt", 2, []byte("part"), 256)

	// Get progress
	progress, err := manager.GetTransferProgress("test.txt")
	if err != nil {
		t.Fatalf("Failed to get transfer progress: %v", err)
	}

	if progress.Filename != "test.txt" {
		t.Errorf("Expected filename test.txt, got %s", progress.Filename)
	}

	if progress.TotalChunks != 4 {
		t.Errorf("Expected 4 total chunks, got %d", progress.TotalChunks)
	}

	if progress.ReceivedChunks != 2 {
		t.Errorf("Expected 2 received chunks, got %d", progress.ReceivedChunks)
	}

	if progress.MissingChunks != 2 {
		t.Errorf("Expected 2 missing chunks, got %d", progress.MissingChunks)
	}

	if progress.PartialChunks != 1 {
		t.Errorf("Expected 1 partial chunk, got %d", progress.PartialChunks)
	}

	expectedBytes := int64(len("data1") + len("data2") + len("part"))
	if progress.ReceivedBytes != expectedBytes {
		t.Errorf("Expected %d received bytes, got %d", expectedBytes, progress.ReceivedBytes)
	}

	// Test progress percentage
	expectedPercentage := (2.0 / 4.0) * 100
	if progress.GetProgressPercentage() != expectedPercentage {
		t.Errorf("Expected progress percentage %.2f, got %.2f", 
			expectedPercentage, progress.GetProgressPercentage())
	}
}

func TestCompleteTransfer(t *testing.T) {
	tempDir := t.TempDir()
	config := &ResumeConfig{
		StateDir:              tempDir,
		SaveInterval:          10 * time.Second,
		MaxResumeAttempts:     3,
		StateRetentionTime:    1 * time.Hour,
		EnablePartialChunks:   true,
		ValidateOnResume:      true,
		CompressStateFiles:    false,
		AutoCleanup:           false,
		CleanupInterval:       30 * time.Minute,
	}

	manager, err := NewTransferResumeManager(config)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}
	defer manager.Close()

	metadata := NewFileMetadata("test.txt", 1024, 256)
	_, _, err = manager.StartTransfer(metadata, "transfer-1")
	if err != nil {
		t.Fatalf("Failed to start transfer: %v", err)
	}

	// Save state to create file
	err = manager.SaveTransferState("test.txt")
	if err != nil {
		t.Fatalf("Failed to save state: %v", err)
	}

	// Verify state file exists
	stateFile := manager.getStateFilePath("test.txt")
	if _, err := os.Stat(stateFile); os.IsNotExist(err) {
		t.Errorf("State file should exist before completion")
	}

	// Complete transfer
	err = manager.CompleteTransfer("test.txt")
	if err != nil {
		t.Fatalf("Failed to complete transfer: %v", err)
	}

	// Verify state file is removed
	if _, err := os.Stat(stateFile); !os.IsNotExist(err) {
		t.Errorf("State file should be removed after completion")
	}

	// Verify transfer is removed from active states
	_, err = manager.GetTransferProgress("test.txt")
	if err == nil {
		t.Errorf("Transfer should be removed from active states")
	}
}

func TestCancelTransfer(t *testing.T) {
	tempDir := t.TempDir()
	config := &ResumeConfig{
		StateDir:              tempDir,
		SaveInterval:          10 * time.Second,
		MaxResumeAttempts:     3,
		StateRetentionTime:    1 * time.Hour,
		EnablePartialChunks:   true,
		ValidateOnResume:      true,
		CompressStateFiles:    false,
		AutoCleanup:           false,
		CleanupInterval:       30 * time.Minute,
	}

	manager, err := NewTransferResumeManager(config)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}
	defer manager.Close()

	metadata := NewFileMetadata("test.txt", 1024, 256)
	_, _, err = manager.StartTransfer(metadata, "transfer-1")
	if err != nil {
		t.Fatalf("Failed to start transfer: %v", err)
	}

	// Save state to create file
	err = manager.SaveTransferState("test.txt")
	if err != nil {
		t.Fatalf("Failed to save state: %v", err)
	}

	stateFile := manager.getStateFilePath("test.txt")

	// Cancel without removing state
	err = manager.CancelTransfer("test.txt", false)
	if err != nil {
		t.Fatalf("Failed to cancel transfer: %v", err)
	}

	// State file should still exist
	if _, err := os.Stat(stateFile); os.IsNotExist(err) {
		t.Errorf("State file should exist when removeState=false")
	}

	// Start another transfer and cancel with state removal
	_, _, err = manager.StartTransfer(metadata, "transfer-2")
	if err != nil {
		t.Fatalf("Failed to start second transfer: %v", err)
	}

	err = manager.CancelTransfer("test.txt", true)
	if err != nil {
		t.Fatalf("Failed to cancel transfer with state removal: %v", err)
	}

	// State file should be removed
	if _, err := os.Stat(stateFile); !os.IsNotExist(err) {
		t.Errorf("State file should be removed when removeState=true")
	}
}

func TestCannotResumeChangedFile(t *testing.T) {
	tempDir := t.TempDir()
	config := &ResumeConfig{
		StateDir:              tempDir,
		SaveInterval:          10 * time.Second,
		MaxResumeAttempts:     3,
		StateRetentionTime:    1 * time.Hour,
		EnablePartialChunks:   true,
		ValidateOnResume:      true,
		CompressStateFiles:    false,
		AutoCleanup:           false,
		CleanupInterval:       30 * time.Minute,
	}

	manager, err := NewTransferResumeManager(config)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}
	defer manager.Close()

	// Start initial transfer
	metadata1 := NewFileMetadata("test.txt", 1024, 256)
	metadata1.Checksum = "abc123"
	
	_, _, err = manager.StartTransfer(metadata1, "transfer-1")
	if err != nil {
		t.Fatalf("Failed to start initial transfer: %v", err)
	}

	err = manager.SaveTransferState("test.txt")
	if err != nil {
		t.Fatalf("Failed to save state: %v", err)
	}

	// Create new manager and try to resume with different file
	manager2, err := NewTransferResumeManager(config)
	if err != nil {
		t.Fatalf("Failed to create second manager: %v", err)
	}
	defer manager2.Close()

	// Different file size - should not resume
	metadata2 := NewFileMetadata("test.txt", 2048, 256) // Different size
	metadata2.Checksum = "abc123"
	
	_, isResume, err := manager2.StartTransfer(metadata2, "transfer-2")
	if err != nil {
		t.Fatalf("Failed to start transfer: %v", err)
	}

	if isResume {
		t.Errorf("Should not resume transfer when file size changed")
	}
}

func TestCannotResumeExceededMaxAttempts(t *testing.T) {
	tempDir := t.TempDir()
	config := &ResumeConfig{
		StateDir:              tempDir,
		SaveInterval:          10 * time.Second,
		MaxResumeAttempts:     2, // Low limit for testing
		StateRetentionTime:    1 * time.Hour,
		EnablePartialChunks:   true,
		ValidateOnResume:      true,
		CompressStateFiles:    false,
		AutoCleanup:           false,
		CleanupInterval:       30 * time.Minute,
	}

	manager, err := NewTransferResumeManager(config)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}
	defer manager.Close()

	metadata := NewFileMetadata("test.txt", 1024, 256)
	
	// Start and resume multiple times to exceed limit
	_, _, err = manager.StartTransfer(metadata, "transfer-1")
	if err != nil {
		t.Fatalf("Failed to start transfer: %v", err)
	}

	// First resume
	_, isResume1, err := manager.StartTransfer(metadata, "transfer-2")
	if err != nil {
		t.Fatalf("Failed to resume first time: %v", err)
	}
	if !isResume1 {
		t.Errorf("Expected first resume to succeed")
	}

	// Second resume
	_, isResume2, err := manager.StartTransfer(metadata, "transfer-3")
	if err != nil {
		t.Fatalf("Failed to resume second time: %v", err)
	}
	if !isResume2 {
		t.Errorf("Expected second resume to succeed")
	}

	// Third resume should fail (exceeds max attempts)
	_, isResume3, err := manager.StartTransfer(metadata, "transfer-4")
	if err != nil {
		t.Fatalf("Failed to start transfer after max resumes: %v", err)
	}
	if isResume3 {
		t.Errorf("Should not resume after exceeding max attempts")
	}
}

func TestListResumableTransfers(t *testing.T) {
	tempDir := t.TempDir()
	config := &ResumeConfig{
		StateDir:              tempDir,
		SaveInterval:          10 * time.Second,
		MaxResumeAttempts:     3,
		StateRetentionTime:    1 * time.Hour,
		EnablePartialChunks:   true,
		ValidateOnResume:      true,
		CompressStateFiles:    false,
		AutoCleanup:           false,
		CleanupInterval:       30 * time.Minute,
	}

	manager, err := NewTransferResumeManager(config)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}
	defer manager.Close()

	// Start multiple transfers
	metadata1 := NewFileMetadata("file1.txt", 1024, 256)
	metadata2 := NewFileMetadata("file2.txt", 2048, 512)
	
	_, _, err = manager.StartTransfer(metadata1, "transfer-1")
	if err != nil {
		t.Fatalf("Failed to start transfer 1: %v", err)
	}

	_, _, err = manager.StartTransfer(metadata2, "transfer-2")
	if err != nil {
		t.Fatalf("Failed to start transfer 2: %v", err)
	}

	// Save states
	manager.SaveTransferState("file1.txt")
	manager.SaveTransferState("file2.txt")

	// List resumable transfers
	resumable, err := manager.ListResumableTransfers()
	if err != nil {
		t.Fatalf("Failed to list resumable transfers: %v", err)
	}

	if len(resumable) != 2 {
		t.Errorf("Expected 2 resumable transfers, got %d", len(resumable))
	}

	// Check that both files are in the list
	found1, found2 := false, false
	for _, filename := range resumable {
		if filename == "file1.txt" {
			found1 = true
		}
		if filename == "file2.txt" {
			found2 = true
		}
	}

	if !found1 {
		t.Errorf("file1.txt not found in resumable transfers")
	}
	if !found2 {
		t.Errorf("file2.txt not found in resumable transfers")
	}
}

func TestGetTransferStatistics(t *testing.T) {
	tempDir := t.TempDir()
	config := &ResumeConfig{
		StateDir:              tempDir,
		SaveInterval:          10 * time.Second,
		MaxResumeAttempts:     3,
		StateRetentionTime:    1 * time.Hour,
		EnablePartialChunks:   true,
		ValidateOnResume:      true,
		CompressStateFiles:    false,
		AutoCleanup:           false,
		CleanupInterval:       30 * time.Minute,
	}

	manager, err := NewTransferResumeManager(config)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}
	defer manager.Close()

	// Start transfers
	metadata1 := NewFileMetadata("file1.txt", 1024, 256)
	metadata2 := NewFileMetadata("file2.txt", 2048, 512)
	
	_, _, err = manager.StartTransfer(metadata1, "transfer-1")
	if err != nil {
		t.Fatalf("Failed to start transfer 1: %v", err)
	}

	// Resume transfer 1
	_, _, err = manager.StartTransfer(metadata1, "transfer-1-resume")
	if err != nil {
		t.Fatalf("Failed to resume transfer 1: %v", err)
	}

	_, _, err = manager.StartTransfer(metadata2, "transfer-2")
	if err != nil {
		t.Fatalf("Failed to start transfer 2: %v", err)
	}

	// Get statistics
	stats := manager.GetTransferStatistics()

	if stats.ActiveTransfers != 2 {
		t.Errorf("Expected 2 active transfers, got %d", stats.ActiveTransfers)
	}

	if stats.TotalTransfers != 2 {
		t.Errorf("Expected 2 total transfers, got %d", stats.TotalTransfers)
	}

	if stats.ResumedTransfers != 1 {
		t.Errorf("Expected 1 resumed transfer, got %d", stats.ResumedTransfers)
	}

	if stats.TransfersByStatus["starting"] != 2 {
		t.Errorf("Expected 2 starting transfers, got %d", stats.TransfersByStatus["starting"])
	}
}

func TestValidateResumedChunks(t *testing.T) {
	tempDir := t.TempDir()
	config := &ResumeConfig{
		StateDir:              tempDir,
		SaveInterval:          10 * time.Second,
		MaxResumeAttempts:     3,
		StateRetentionTime:    1 * time.Hour,
		EnablePartialChunks:   true,
		ValidateOnResume:      true,
		CompressStateFiles:    false,
		AutoCleanup:           false,
		CleanupInterval:       30 * time.Minute,
	}

	manager, err := NewTransferResumeManager(config)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}
	defer manager.Close()

	metadata := NewFileMetadata("test.txt", 1024, 256)
	_, _, err = manager.StartTransfer(metadata, "transfer-1")
	if err != nil {
		t.Fatalf("Failed to start transfer: %v", err)
	}

	// Record some chunks
	chunk1 := NewFileChunk(1, 0, []byte("valid"), "test.txt", 0, 4, false)
	chunk2 := NewFileChunk(2, 1, []byte("invalid"), "test.txt", 256, 4, false)
	
	manager.RecordChunkReceived("test.txt", chunk1)
	manager.RecordChunkReceived("test.txt", chunk2)

	// Validator that marks chunk 1 as invalid
	validator := func(chunkInfo *ChunkInfo) bool {
		return chunkInfo.SequenceNum != 1 // Chunk 1 is invalid
	}

	// Validate chunks
	err = manager.ValidateResumedChunks("test.txt", validator)
	if err != nil {
		t.Fatalf("Failed to validate resumed chunks: %v", err)
	}

	// Check that invalid chunk was moved back to missing
	received, err := manager.GetReceivedChunks("test.txt")
	if err != nil {
		t.Fatalf("Failed to get received chunks: %v", err)
	}

	if len(received) != 1 {
		t.Errorf("Expected 1 received chunk after validation, got %d", len(received))
	}

	if _, exists := received[1]; exists {
		t.Errorf("Invalid chunk 1 should have been removed from received chunks")
	}

	missing, err := manager.GetMissingChunks("test.txt")
	if err != nil {
		t.Fatalf("Failed to get missing chunks: %v", err)
	}

	// Should have 3 missing chunks (1 moved back + 2 original)
	if len(missing) != 3 {
		t.Errorf("Expected 3 missing chunks after validation, got %d", len(missing))
	}
}

func TestResumeTransferProgressMethods(t *testing.T) {
	progress := &ResumeTransferProgress{
		Filename:       "test.txt",
		TotalChunks:    10,
		ReceivedChunks: 3,
		MissingChunks:  7,
		PartialChunks:  0,
		TotalBytes:     1000,
		ReceivedBytes:  300,
		ResumeCount:    1,
		StartTime:      time.Now(),
		LastSaveTime:   time.Now(),
	}

	// Test progress percentage
	expectedPercentage := 30.0 // 3/10 * 100
	if progress.GetProgressPercentage() != expectedPercentage {
		t.Errorf("Expected progress percentage %.2f, got %.2f", 
			expectedPercentage, progress.GetProgressPercentage())
	}

	// Test bytes progress percentage
	expectedBytesPercentage := 30.0 // 300/1000 * 100
	if progress.GetBytesProgressPercentage() != expectedBytesPercentage {
		t.Errorf("Expected bytes progress percentage %.2f, got %.2f", 
			expectedBytesPercentage, progress.GetBytesProgressPercentage())
	}

	// Test with zero values
	zeroProgress := &ResumeTransferProgress{
		TotalChunks: 0,
		TotalBytes:  0,
	}

	if zeroProgress.GetProgressPercentage() != 0 {
		t.Errorf("Expected 0 progress percentage for zero chunks")
	}

	if zeroProgress.GetBytesProgressPercentage() != 0 {
		t.Errorf("Expected 0 bytes progress percentage for zero bytes")
	}
}