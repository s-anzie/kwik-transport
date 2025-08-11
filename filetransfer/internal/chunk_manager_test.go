package filetransfer

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewChunkManager(t *testing.T) {
	tempDir := filepath.Join(os.TempDir(), "chunk_manager_test")
	defer os.RemoveAll(tempDir)

	timeout := 30 * time.Second
	cm, err := NewChunkManager(tempDir, timeout)
	if err != nil {
		t.Fatalf("Failed to create ChunkManager: %v", err)
	}

	if cm.tempDir != tempDir {
		t.Errorf("Expected tempDir %s, got %s", tempDir, cm.tempDir)
	}
	if cm.chunkTimeout != timeout {
		t.Errorf("Expected timeout %v, got %v", timeout, cm.chunkTimeout)
	}
	if cm.activeTransfers == nil {
		t.Error("activeTransfers map should be initialized")
	}

	// Verify temp directory was created
	if _, err := os.Stat(tempDir); os.IsNotExist(err) {
		t.Error("Temp directory should have been created")
	}
}

func TestChunkManager_StartTransfer(t *testing.T) {
	tempDir := filepath.Join(os.TempDir(), "chunk_manager_start_test")
	defer os.RemoveAll(tempDir)

	cm, err := NewChunkManager(tempDir, 30*time.Second)
	if err != nil {
		t.Fatalf("Failed to create ChunkManager: %v", err)
	}

	metadata := NewFileMetadata("test.txt", 1024, 256)
	progressCallback := func(progress float64) {
		// Progress callback for testing
	}

	// Start transfer
	err = cm.StartTransfer(metadata, progressCallback)
	if err != nil {
		t.Fatalf("Failed to start transfer: %v", err)
	}

	// Verify transfer state was created
	cm.mu.RLock()
	state, exists := cm.activeTransfers["test.txt"]
	cm.mu.RUnlock()

	if !exists {
		t.Fatal("Transfer state should exist")
	}
	if state.Metadata.Filename != "test.txt" {
		t.Errorf("Expected filename test.txt, got %s", state.Metadata.Filename)
	}
	if len(state.MissingChunks) != 4 { // 1024/256 = 4 chunks
		t.Errorf("Expected 4 missing chunks, got %d", len(state.MissingChunks))
	}
	if state.IsComplete {
		t.Error("Transfer should not be complete initially")
	}

	// Try to start same transfer again (should fail)
	err = cm.StartTransfer(metadata, progressCallback)
	if err == nil {
		t.Error("Starting duplicate transfer should fail")
	}
}

func TestChunkManager_ReceiveChunk(t *testing.T) {
	tempDir := filepath.Join(os.TempDir(), "chunk_manager_receive_test")
	defer os.RemoveAll(tempDir)

	cm, err := NewChunkManager(tempDir, 30*time.Second)
	if err != nil {
		t.Fatalf("Failed to create ChunkManager: %v", err)
	}

	// Start transfer
	metadata := NewFileMetadata("test.txt", 512, 256) // 2 chunks
	progressValues := []float64{}
	progressCallback := func(progress float64) {
		progressValues = append(progressValues, progress)
	}

	err = cm.StartTransfer(metadata, progressCallback)
	if err != nil {
		t.Fatalf("Failed to start transfer: %v", err)
	}

	// Create and receive first chunk
	data1 := make([]byte, 256)
	for i := range data1 {
		data1[i] = byte(i % 256)
	}
	chunk1 := NewFileChunk(1, 0, data1, "test.txt", 0, 2, false)

	err = cm.ReceiveChunk(chunk1)
	if err != nil {
		t.Fatalf("Failed to receive chunk 1: %v", err)
	}

	// Verify progress callback was called
	if len(progressValues) == 0 {
		t.Error("Progress callback should have been called")
	}
	if progressValues[len(progressValues)-1] != 0.5 { // 1 of 2 chunks
		t.Errorf("Expected progress 0.5, got %f", progressValues[len(progressValues)-1])
	}

	// Create and receive second chunk
	data2 := make([]byte, 256)
	for i := range data2 {
		data2[i] = byte((i + 256) % 256)
	}
	chunk2 := NewFileChunk(2, 1, data2, "test.txt", 256, 2, true)

	err = cm.ReceiveChunk(chunk2)
	if err != nil {
		t.Fatalf("Failed to receive chunk 2: %v", err)
	}

	// Verify transfer is complete
	isComplete, err := cm.IsTransferComplete("test.txt")
	if err != nil {
		t.Fatalf("Failed to check transfer completion: %v", err)
	}
	if !isComplete {
		t.Error("Transfer should be complete")
	}

	// Verify final progress is 1.0
	if progressValues[len(progressValues)-1] != 1.0 {
		t.Errorf("Expected final progress 1.0, got %f", progressValues[len(progressValues)-1])
	}
}

func TestChunkManager_ReceiveChunk_InvalidChecksum(t *testing.T) {
	tempDir := filepath.Join(os.TempDir(), "chunk_manager_invalid_test")
	defer os.RemoveAll(tempDir)

	cm, err := NewChunkManager(tempDir, 30*time.Second)
	if err != nil {
		t.Fatalf("Failed to create ChunkManager: %v", err)
	}

	metadata := NewFileMetadata("test.txt", 256, 256)
	err = cm.StartTransfer(metadata, nil)
	if err != nil {
		t.Fatalf("Failed to start transfer: %v", err)
	}

	// Create chunk with invalid checksum
	data := []byte("test data")
	chunk := NewFileChunk(1, 0, data, "test.txt", 0, 1, true)
	chunk.Checksum = "invalid_checksum" // Corrupt the checksum

	err = cm.ReceiveChunk(chunk)
	if err == nil {
		t.Error("Receiving chunk with invalid checksum should fail")
	}
}

func TestChunkManager_GetMissingChunks(t *testing.T) {
	tempDir := filepath.Join(os.TempDir(), "chunk_manager_missing_test")
	defer os.RemoveAll(tempDir)

	cm, err := NewChunkManager(tempDir, 30*time.Second)
	if err != nil {
		t.Fatalf("Failed to create ChunkManager: %v", err)
	}

	metadata := NewFileMetadata("test.txt", 1024, 256) // 4 chunks
	err = cm.StartTransfer(metadata, nil)
	if err != nil {
		t.Fatalf("Failed to start transfer: %v", err)
	}

	// Initially all chunks should be missing
	missing, err := cm.GetMissingChunks("test.txt")
	if err != nil {
		t.Fatalf("Failed to get missing chunks: %v", err)
	}
	if len(missing) != 4 {
		t.Errorf("Expected 4 missing chunks, got %d", len(missing))
	}

	// Receive chunk 1
	data := make([]byte, 256)
	chunk := NewFileChunk(1, 1, data, "test.txt", 256, 4, false)
	err = cm.ReceiveChunk(chunk)
	if err != nil {
		t.Fatalf("Failed to receive chunk: %v", err)
	}

	// Now should have 3 missing chunks
	missing, err = cm.GetMissingChunks("test.txt")
	if err != nil {
		t.Fatalf("Failed to get missing chunks: %v", err)
	}
	if len(missing) != 3 {
		t.Errorf("Expected 3 missing chunks, got %d", len(missing))
	}

	// Verify chunk 1 is not in missing list
	for _, seq := range missing {
		if seq == 1 {
			t.Error("Chunk 1 should not be in missing list")
		}
	}
}

func TestChunkManager_GetTransferProgress(t *testing.T) {
	tempDir := filepath.Join(os.TempDir(), "chunk_manager_progress_test")
	defer os.RemoveAll(tempDir)

	cm, err := NewChunkManager(tempDir, 30*time.Second)
	if err != nil {
		t.Fatalf("Failed to create ChunkManager: %v", err)
	}

	metadata := NewFileMetadata("test.txt", 1024, 256) // 4 chunks
	err = cm.StartTransfer(metadata, nil)
	if err != nil {
		t.Fatalf("Failed to start transfer: %v", err)
	}

	// Get initial progress
	progress, err := cm.GetTransferProgress("test.txt")
	if err != nil {
		t.Fatalf("Failed to get transfer progress: %v", err)
	}

	if progress.Filename != "test.txt" {
		t.Errorf("Expected filename test.txt, got %s", progress.Filename)
	}
	if progress.TotalChunks != 4 {
		t.Errorf("Expected 4 total chunks, got %d", progress.TotalChunks)
	}
	if progress.ReceivedChunks != 0 {
		t.Errorf("Expected 0 received chunks, got %d", progress.ReceivedChunks)
	}
	if progress.TotalBytes != 1024 {
		t.Errorf("Expected 1024 total bytes, got %d", progress.TotalBytes)
	}
	if progress.ReceivedBytes != 0 {
		t.Errorf("Expected 0 received bytes, got %d", progress.ReceivedBytes)
	}

	// Receive a chunk
	data := make([]byte, 256)
	chunk := NewFileChunk(1, 0, data, "test.txt", 0, 4, false)
	err = cm.ReceiveChunk(chunk)
	if err != nil {
		t.Fatalf("Failed to receive chunk: %v", err)
	}

	// Get updated progress
	progress, err = cm.GetTransferProgress("test.txt")
	if err != nil {
		t.Fatalf("Failed to get transfer progress: %v", err)
	}

	if progress.ReceivedChunks != 1 {
		t.Errorf("Expected 1 received chunk, got %d", progress.ReceivedChunks)
	}
	if progress.ReceivedBytes != 256 {
		t.Errorf("Expected 256 received bytes, got %d", progress.ReceivedBytes)
	}
	if progress.Speed <= 0 {
		t.Error("Transfer speed should be positive")
	}
}

func TestChunkManager_FileReconstruction(t *testing.T) {
	tempDir := filepath.Join(os.TempDir(), "chunk_manager_reconstruction_test")
	defer os.RemoveAll(tempDir)

	cm, err := NewChunkManager(tempDir, 30*time.Second)
	if err != nil {
		t.Fatalf("Failed to create ChunkManager: %v", err)
	}

	// Create test data
	originalData := []byte("Hello, World! This is a test file for chunk reconstruction.")
	chunkSize := int32(20)
	metadata := NewFileMetadata("test.txt", int64(len(originalData)), chunkSize)

	// Calculate expected checksum
	expectedChecksum := fmt.Sprintf("%x", sha256.Sum256(originalData))
	metadata.Checksum = expectedChecksum

	err = cm.StartTransfer(metadata, nil)
	if err != nil {
		t.Fatalf("Failed to start transfer: %v", err)
	}

	// Send chunks in random order to test reconstruction
	chunkOrder := []int{2, 0, 1} // Send chunks out of order
	for _, i := range chunkOrder {
		start := i * int(chunkSize)
		end := start + int(chunkSize)
		if end > len(originalData) {
			end = len(originalData)
		}

		chunkData := originalData[start:end]
		isLast := (i == int(metadata.TotalChunks)-1)
		
		chunk := NewFileChunk(uint32(i+1), uint32(i), chunkData, "test.txt", 
			int64(start), metadata.TotalChunks, isLast)

		err = cm.ReceiveChunk(chunk)
		if err != nil {
			t.Fatalf("Failed to receive chunk %d: %v", i, err)
		}
	}

	// Verify transfer is complete
	isComplete, err := cm.IsTransferComplete("test.txt")
	if err != nil {
		t.Fatalf("Failed to check completion: %v", err)
	}
	if !isComplete {
		t.Error("Transfer should be complete")
	}

	// Get reconstructed file path
	filePath, err := cm.GetReconstructedFilePath("test.txt")
	if err != nil {
		t.Fatalf("Failed to get reconstructed file path: %v", err)
	}

	// Read reconstructed file
	reconstructedData, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("Failed to read reconstructed file: %v", err)
	}

	// Verify data matches original
	if string(reconstructedData) != string(originalData) {
		t.Errorf("Reconstructed data doesn't match original.\nExpected: %s\nGot: %s", 
			string(originalData), string(reconstructedData))
	}

	// Verify checksum
	finalChecksum, err := cm.GetFinalChecksum("test.txt")
	if err != nil {
		t.Fatalf("Failed to get final checksum: %v", err)
	}
	if finalChecksum != expectedChecksum {
		t.Errorf("Final checksum mismatch. Expected: %s, Got: %s", 
			expectedChecksum, finalChecksum)
	}
}

func TestChunkManager_CleanupTransfer(t *testing.T) {
	tempDir := filepath.Join(os.TempDir(), "chunk_manager_cleanup_test")
	defer os.RemoveAll(tempDir)

	cm, err := NewChunkManager(tempDir, 30*time.Second)
	if err != nil {
		t.Fatalf("Failed to create ChunkManager: %v", err)
	}

	metadata := NewFileMetadata("test.txt", 256, 256)
	err = cm.StartTransfer(metadata, nil)
	if err != nil {
		t.Fatalf("Failed to start transfer: %v", err)
	}

	// Receive chunk to create temp file
	data := make([]byte, 256)
	chunk := NewFileChunk(1, 0, data, "test.txt", 0, 1, true)
	err = cm.ReceiveChunk(chunk)
	if err != nil {
		t.Fatalf("Failed to receive chunk: %v", err)
	}

	// Get temp file path before cleanup
	tempFilePath, err := cm.GetReconstructedFilePath("test.txt")
	if err != nil {
		t.Fatalf("Failed to get temp file path: %v", err)
	}

	// Verify temp file exists
	if _, err := os.Stat(tempFilePath); os.IsNotExist(err) {
		t.Error("Temp file should exist before cleanup")
	}

	// Cleanup transfer
	err = cm.CleanupTransfer("test.txt")
	if err != nil {
		t.Fatalf("Failed to cleanup transfer: %v", err)
	}

	// Verify temp file is removed
	if _, err := os.Stat(tempFilePath); !os.IsNotExist(err) {
		t.Error("Temp file should be removed after cleanup")
	}

	// Verify transfer is removed from active transfers
	cm.mu.RLock()
	_, exists := cm.activeTransfers["test.txt"]
	cm.mu.RUnlock()

	if exists {
		t.Error("Transfer should be removed from active transfers")
	}
}

func TestChunkManager_GetTimedOutTransfers(t *testing.T) {
	tempDir := filepath.Join(os.TempDir(), "chunk_manager_timeout_test")
	defer os.RemoveAll(tempDir)

	shortTimeout := 100 * time.Millisecond
	cm, err := NewChunkManager(tempDir, shortTimeout)
	if err != nil {
		t.Fatalf("Failed to create ChunkManager: %v", err)
	}

	metadata := NewFileMetadata("test.txt", 256, 256)
	err = cm.StartTransfer(metadata, nil)
	if err != nil {
		t.Fatalf("Failed to start transfer: %v", err)
	}

	// Initially no timeouts
	timedOut := cm.GetTimedOutTransfers()
	if len(timedOut) != 0 {
		t.Errorf("Expected 0 timed out transfers, got %d", len(timedOut))
	}

	// Wait for timeout
	time.Sleep(shortTimeout + 50*time.Millisecond)

	// Now should have timed out transfer
	timedOut = cm.GetTimedOutTransfers()
	if len(timedOut) != 1 {
		t.Errorf("Expected 1 timed out transfer, got %d", len(timedOut))
	}
	if timedOut[0] != "test.txt" {
		t.Errorf("Expected timed out transfer 'test.txt', got '%s'", timedOut[0])
	}
}

func TestTransferProgress_GetProgressPercentage(t *testing.T) {
	progress := &TransferProgress{
		TotalChunks:    10,
		ReceivedChunks: 3,
	}

	percentage := progress.GetProgressPercentage()
	expected := 30.0
	if percentage != expected {
		t.Errorf("Expected progress percentage %f, got %f", expected, percentage)
	}

	// Test with zero total chunks
	progress.TotalChunks = 0
	percentage = progress.GetProgressPercentage()
	if percentage != 0 {
		t.Errorf("Expected progress percentage 0 for zero total chunks, got %f", percentage)
	}
}

func TestTransferProgress_GetSpeedMBps(t *testing.T) {
	progress := &TransferProgress{
		Speed: 2 * 1024 * 1024, // 2 MB/s in bytes/s
	}

	speedMBps := progress.GetSpeedMBps()
	expected := 2.0
	if speedMBps != expected {
		t.Errorf("Expected speed %f MB/s, got %f MB/s", expected, speedMBps)
	}
}

// Tests for missing chunk detection functionality (subtask 2.2)

func TestChunkManager_MarkChunkRequested(t *testing.T) {
	tempDir := filepath.Join(os.TempDir(), "chunk_manager_mark_test")
	defer os.RemoveAll(tempDir)

	cm, err := NewChunkManager(tempDir, 30*time.Second)
	if err != nil {
		t.Fatalf("Failed to create ChunkManager: %v", err)
	}

	metadata := NewFileMetadata("test.txt", 1024, 256) // 4 chunks
	err = cm.StartTransfer(metadata, nil)
	if err != nil {
		t.Fatalf("Failed to start transfer: %v", err)
	}

	// Mark chunk 0 as requested
	err = cm.MarkChunkRequested("test.txt", 0)
	if err != nil {
		t.Fatalf("Failed to mark chunk as requested: %v", err)
	}

	// Verify retry count increased
	retryCount, err := cm.GetChunkRetryCount("test.txt", 0)
	if err != nil {
		t.Fatalf("Failed to get retry count: %v", err)
	}
	if retryCount != 1 {
		t.Errorf("Expected retry count 1, got %d", retryCount)
	}

	// Mark same chunk again
	err = cm.MarkChunkRequested("test.txt", 0)
	if err != nil {
		t.Fatalf("Failed to mark chunk as requested again: %v", err)
	}

	// Verify retry count increased again
	retryCount, err = cm.GetChunkRetryCount("test.txt", 0)
	if err != nil {
		t.Fatalf("Failed to get retry count: %v", err)
	}
	if retryCount != 2 {
		t.Errorf("Expected retry count 2, got %d", retryCount)
	}
}

func TestChunkManager_GetTimedOutChunks(t *testing.T) {
	tempDir := filepath.Join(os.TempDir(), "chunk_manager_timedout_test")
	defer os.RemoveAll(tempDir)

	shortTimeout := 100 * time.Millisecond
	cm, err := NewChunkManager(tempDir, shortTimeout)
	if err != nil {
		t.Fatalf("Failed to create ChunkManager: %v", err)
	}

	metadata := NewFileMetadata("test.txt", 1024, 256) // 4 chunks
	err = cm.StartTransfer(metadata, nil)
	if err != nil {
		t.Fatalf("Failed to start transfer: %v", err)
	}

	// Initially, all chunks should be timed out (never requested)
	timedOut, err := cm.GetTimedOutChunks("test.txt")
	if err != nil {
		t.Fatalf("Failed to get timed out chunks: %v", err)
	}
	if len(timedOut) != 4 {
		t.Errorf("Expected 4 timed out chunks, got %d", len(timedOut))
	}

	// Mark chunk 0 as requested
	err = cm.MarkChunkRequested("test.txt", 0)
	if err != nil {
		t.Fatalf("Failed to mark chunk as requested: %v", err)
	}

	// Immediately after marking, chunk 0 should not be timed out
	timedOut, err = cm.GetTimedOutChunks("test.txt")
	if err != nil {
		t.Fatalf("Failed to get timed out chunks: %v", err)
	}
	if len(timedOut) != 3 {
		t.Errorf("Expected 3 timed out chunks, got %d", len(timedOut))
	}

	// Wait for timeout
	time.Sleep(shortTimeout + 50*time.Millisecond)

	// Now chunk 0 should be timed out again
	timedOut, err = cm.GetTimedOutChunks("test.txt")
	if err != nil {
		t.Fatalf("Failed to get timed out chunks: %v", err)
	}
	if len(timedOut) != 4 {
		t.Errorf("Expected 4 timed out chunks after timeout, got %d", len(timedOut))
	}

	// Verify priority ordering (chunks with more retries should have higher priority)
	if len(timedOut) > 1 && timedOut[0].RetryCount < timedOut[1].RetryCount {
		t.Error("Chunks should be ordered by priority (retry count)")
	}
}

func TestChunkManager_GetAllTimedOutChunks(t *testing.T) {
	tempDir := filepath.Join(os.TempDir(), "chunk_manager_all_timedout_test")
	defer os.RemoveAll(tempDir)

	shortTimeout := 100 * time.Millisecond
	cm, err := NewChunkManager(tempDir, shortTimeout)
	if err != nil {
		t.Fatalf("Failed to create ChunkManager: %v", err)
	}

	// Start two transfers
	metadata1 := NewFileMetadata("test1.txt", 512, 256) // 2 chunks
	err = cm.StartTransfer(metadata1, nil)
	if err != nil {
		t.Fatalf("Failed to start transfer 1: %v", err)
	}

	metadata2 := NewFileMetadata("test2.txt", 768, 256) // 3 chunks
	err = cm.StartTransfer(metadata2, nil)
	if err != nil {
		t.Fatalf("Failed to start transfer 2: %v", err)
	}

	// Get all timed out chunks
	allTimedOut, err := cm.GetAllTimedOutChunks()
	if err != nil {
		t.Fatalf("Failed to get all timed out chunks: %v", err)
	}

	// Should have 2 + 3 = 5 timed out chunks
	if len(allTimedOut) != 5 {
		t.Errorf("Expected 5 timed out chunks, got %d", len(allTimedOut))
	}

	// Verify chunks from both files are included
	files := make(map[string]bool)
	for _, chunk := range allTimedOut {
		files[chunk.Filename] = true
	}
	if len(files) != 2 {
		t.Errorf("Expected chunks from 2 files, got %d", len(files))
	}
}

func TestChunkManager_ShouldRetryChunk(t *testing.T) {
	tempDir := filepath.Join(os.TempDir(), "chunk_manager_should_retry_test")
	defer os.RemoveAll(tempDir)

	cm, err := NewChunkManager(tempDir, 30*time.Second)
	if err != nil {
		t.Fatalf("Failed to create ChunkManager: %v", err)
	}

	metadata := NewFileMetadata("test.txt", 256, 256) // 1 chunk
	err = cm.StartTransfer(metadata, nil)
	if err != nil {
		t.Fatalf("Failed to start transfer: %v", err)
	}

	maxRetries := 3

	// Initially should retry (0 attempts)
	shouldRetry, err := cm.ShouldRetryChunk("test.txt", 0, maxRetries)
	if err != nil {
		t.Fatalf("Failed to check should retry: %v", err)
	}
	if !shouldRetry {
		t.Error("Should retry chunk with 0 attempts")
	}

	// Mark chunk as requested multiple times
	for i := 0; i < maxRetries; i++ {
		err = cm.MarkChunkRequested("test.txt", 0)
		if err != nil {
			t.Fatalf("Failed to mark chunk as requested: %v", err)
		}
	}

	// Now should not retry (reached max retries)
	shouldRetry, err = cm.ShouldRetryChunk("test.txt", 0, maxRetries)
	if err != nil {
		t.Fatalf("Failed to check should retry: %v", err)
	}
	if shouldRetry {
		t.Error("Should not retry chunk after max retries")
	}
}

func TestChunkManager_GetMissingChunksWithTimeout(t *testing.T) {
	tempDir := filepath.Join(os.TempDir(), "chunk_manager_missing_timeout_test")
	defer os.RemoveAll(tempDir)

	shortTimeout := 100 * time.Millisecond
	cm, err := NewChunkManager(tempDir, shortTimeout)
	if err != nil {
		t.Fatalf("Failed to create ChunkManager: %v", err)
	}

	metadata := NewFileMetadata("test.txt", 1024, 256) // 4 chunks
	err = cm.StartTransfer(metadata, nil)
	if err != nil {
		t.Fatalf("Failed to start transfer: %v", err)
	}

	// Mark chunks 0 and 1 as requested
	err = cm.MarkChunkRequested("test.txt", 0)
	if err != nil {
		t.Fatalf("Failed to mark chunk 0 as requested: %v", err)
	}
	err = cm.MarkChunkRequested("test.txt", 1)
	if err != nil {
		t.Fatalf("Failed to mark chunk 1 as requested: %v", err)
	}

	// Immediately, only chunks 2 and 3 should be timed out
	timedOutSeqs, err := cm.GetMissingChunksWithTimeout("test.txt")
	if err != nil {
		t.Fatalf("Failed to get missing chunks with timeout: %v", err)
	}
	if len(timedOutSeqs) != 2 {
		t.Errorf("Expected 2 timed out sequences, got %d", len(timedOutSeqs))
	}

	// Wait for timeout
	time.Sleep(shortTimeout + 50*time.Millisecond)

	// Now all 4 chunks should be timed out
	timedOutSeqs, err = cm.GetMissingChunksWithTimeout("test.txt")
	if err != nil {
		t.Fatalf("Failed to get missing chunks with timeout: %v", err)
	}
	if len(timedOutSeqs) != 4 {
		t.Errorf("Expected 4 timed out sequences after timeout, got %d", len(timedOutSeqs))
	}
}

func TestChunkManager_GetTransferStatistics(t *testing.T) {
	tempDir := filepath.Join(os.TempDir(), "chunk_manager_stats_test")
	defer os.RemoveAll(tempDir)

	cm, err := NewChunkManager(tempDir, 30*time.Second)
	if err != nil {
		t.Fatalf("Failed to create ChunkManager: %v", err)
	}

	metadata := NewFileMetadata("test.txt", 1024, 256) // 4 chunks
	err = cm.StartTransfer(metadata, nil)
	if err != nil {
		t.Fatalf("Failed to start transfer: %v", err)
	}

	// Mark some chunks as requested with retries
	err = cm.MarkChunkRequested("test.txt", 0) // 1 retry
	if err != nil {
		t.Fatalf("Failed to mark chunk 0: %v", err)
	}
	err = cm.MarkChunkRequested("test.txt", 0) // 2 retries
	if err != nil {
		t.Fatalf("Failed to mark chunk 0 again: %v", err)
	}
	err = cm.MarkChunkRequested("test.txt", 1) // 1 retry
	if err != nil {
		t.Fatalf("Failed to mark chunk 1: %v", err)
	}

	// Receive one chunk
	data := make([]byte, 256)
	chunk := NewFileChunk(1, 0, data, "test.txt", 0, 4, false)
	err = cm.ReceiveChunk(chunk)
	if err != nil {
		t.Fatalf("Failed to receive chunk: %v", err)
	}

	// Get statistics
	stats, err := cm.GetTransferStatistics("test.txt")
	if err != nil {
		t.Fatalf("Failed to get transfer statistics: %v", err)
	}

	// Verify statistics
	if stats.Filename != "test.txt" {
		t.Errorf("Expected filename test.txt, got %s", stats.Filename)
	}
	if stats.TotalChunks != 4 {
		t.Errorf("Expected 4 total chunks, got %d", stats.TotalChunks)
	}
	if stats.ReceivedChunks != 1 {
		t.Errorf("Expected 1 received chunk, got %d", stats.ReceivedChunks)
	}
	if stats.MissingChunks != 3 {
		t.Errorf("Expected 3 missing chunks, got %d", stats.MissingChunks)
	}
	if stats.TotalRetries != 3 { // 2 for chunk 0, 1 for chunk 1
		t.Errorf("Expected 3 total retries, got %d", stats.TotalRetries)
	}
	if stats.MaxRetries != 2 { // chunk 0 has 2 retries
		t.Errorf("Expected 2 max retries, got %d", stats.MaxRetries)
	}
	if stats.ChunksWithRetries != 2 { // chunks 0 and 1 have retries
		t.Errorf("Expected 2 chunks with retries, got %d", stats.ChunksWithRetries)
	}
	if stats.IsComplete {
		t.Error("Transfer should not be complete")
	}
}

func TestTransferStatistics_GetRetryRate(t *testing.T) {
	stats := &TransferStatistics{
		TotalChunks:       10,
		ChunksWithRetries: 3,
	}

	retryRate := stats.GetRetryRate()
	expected := 30.0
	if retryRate != expected {
		t.Errorf("Expected retry rate %f%%, got %f%%", expected, retryRate)
	}

	// Test with zero total chunks
	stats.TotalChunks = 0
	retryRate = stats.GetRetryRate()
	if retryRate != 0 {
		t.Errorf("Expected retry rate 0%% for zero total chunks, got %f%%", retryRate)
	}
}

func TestTransferStatistics_GetAverageRetriesPerChunk(t *testing.T) {
	stats := &TransferStatistics{
		TotalChunks:  10,
		TotalRetries: 25,
	}

	avgRetries := stats.GetAverageRetriesPerChunk()
	expected := 2.5
	if avgRetries != expected {
		t.Errorf("Expected average retries %f, got %f", expected, avgRetries)
	}

	// Test with zero total chunks
	stats.TotalChunks = 0
	avgRetries = stats.GetAverageRetriesPerChunk()
	if avgRetries != 0 {
		t.Errorf("Expected average retries 0 for zero total chunks, got %f", avgRetries)
	}
}

func TestChunkManager_MarkChunkRequested_ChunkAlreadyReceived(t *testing.T) {
	tempDir := filepath.Join(os.TempDir(), "chunk_manager_mark_received_test")
	defer os.RemoveAll(tempDir)

	cm, err := NewChunkManager(tempDir, 30*time.Second)
	if err != nil {
		t.Fatalf("Failed to create ChunkManager: %v", err)
	}

	metadata := NewFileMetadata("test.txt", 256, 256) // 1 chunk
	err = cm.StartTransfer(metadata, nil)
	if err != nil {
		t.Fatalf("Failed to start transfer: %v", err)
	}

	// Receive the chunk
	data := make([]byte, 256)
	chunk := NewFileChunk(1, 0, data, "test.txt", 0, 1, true)
	err = cm.ReceiveChunk(chunk)
	if err != nil {
		t.Fatalf("Failed to receive chunk: %v", err)
	}

	// Try to mark the received chunk as requested (should be ignored)
	err = cm.MarkChunkRequested("test.txt", 0)
	if err != nil {
		t.Fatalf("Failed to mark received chunk as requested: %v", err)
	}

	// Retry count should still be 0 since chunk was already received
	retryCount, err := cm.GetChunkRetryCount("test.txt", 0)
	if err != nil {
		t.Fatalf("Failed to get retry count: %v", err)
	}
	if retryCount != 0 {
		t.Errorf("Expected retry count 0 for received chunk, got %d", retryCount)
	}
}

// Tests for complete integrity validation functionality (subtask 2.3)

func TestChunkManager_ReceiveChunkWithValidation(t *testing.T) {
	tempDir := filepath.Join(os.TempDir(), "chunk_manager_validation_test")
	defer os.RemoveAll(tempDir)

	cm, err := NewChunkManager(tempDir, 30*time.Second)
	if err != nil {
		t.Fatalf("Failed to create ChunkManager: %v", err)
	}

	metadata := NewFileMetadata("test.txt", 512, 256) // 2 chunks
	err = cm.StartTransfer(metadata, nil)
	if err != nil {
		t.Fatalf("Failed to start transfer: %v", err)
	}

	// Create valid chunks
	data1 := make([]byte, 256)
	for i := range data1 {
		data1[i] = byte(i % 256)
	}
	chunk1 := NewFileChunk(1, 0, data1, "test.txt", 0, 2, false)

	data2 := make([]byte, 256)
	for i := range data2 {
		data2[i] = byte((i + 256) % 256)
	}
	chunk2 := NewFileChunk(2, 1, data2, "test.txt", 256, 2, true)

	// Receive chunks with validation
	maxRetries := 3
	err = cm.ReceiveChunkWithValidation(chunk1, maxRetries)
	if err != nil {
		t.Fatalf("Failed to receive valid chunk 1: %v", err)
	}

	err = cm.ReceiveChunkWithValidation(chunk2, maxRetries)
	if err != nil {
		t.Fatalf("Failed to receive valid chunk 2: %v", err)
	}

	// Verify transfer completed with integrity validation
	isComplete, err := cm.IsTransferComplete("test.txt")
	if err != nil {
		t.Fatalf("Failed to check completion: %v", err)
	}
	if !isComplete {
		t.Error("Transfer should be complete")
	}

	integrityPassed, err := cm.IsIntegrityValidationPassed("test.txt")
	if err != nil {
		t.Fatalf("Failed to check integrity: %v", err)
	}
	if !integrityPassed {
		t.Error("Integrity validation should have passed")
	}
}

func TestChunkManager_ReceiveChunkWithValidation_InvalidSize(t *testing.T) {
	tempDir := filepath.Join(os.TempDir(), "chunk_manager_invalid_size_test")
	defer os.RemoveAll(tempDir)

	cm, err := NewChunkManager(tempDir, 30*time.Second)
	if err != nil {
		t.Fatalf("Failed to create ChunkManager: %v", err)
	}

	metadata := NewFileMetadata("test.txt", 256, 256) // 1 chunk of 256 bytes
	err = cm.StartTransfer(metadata, nil)
	if err != nil {
		t.Fatalf("Failed to start transfer: %v", err)
	}

	// Create chunk with wrong size
	data := make([]byte, 128) // Should be 256 bytes
	chunk := NewFileChunk(1, 0, data, "test.txt", 0, 1, true)

	// Try to receive chunk with validation
	maxRetries := 3
	err = cm.ReceiveChunkWithValidation(chunk, maxRetries)
	if err == nil {
		t.Error("Should fail validation for wrong chunk size")
	}

	// Verify validation error was recorded
	validationErrors, err := cm.GetValidationErrors("test.txt")
	if err != nil {
		t.Fatalf("Failed to get validation errors: %v", err)
	}
	if len(validationErrors) == 0 {
		t.Error("Should have validation errors")
	}
	if validationErrors[0].Type != ValidationErrorChunkSize {
		t.Errorf("Expected chunk size error, got %s", validationErrors[0].Type)
	}
}

func TestChunkManager_ReceiveChunkWithValidation_CorruptedChecksum(t *testing.T) {
	tempDir := filepath.Join(os.TempDir(), "chunk_manager_corrupted_test")
	defer os.RemoveAll(tempDir)

	cm, err := NewChunkManager(tempDir, 30*time.Second)
	if err != nil {
		t.Fatalf("Failed to create ChunkManager: %v", err)
	}

	metadata := NewFileMetadata("test.txt", 256, 256)
	err = cm.StartTransfer(metadata, nil)
	if err != nil {
		t.Fatalf("Failed to start transfer: %v", err)
	}

	// Create chunk with corrupted checksum
	data := make([]byte, 256)
	chunk := NewFileChunk(1, 0, data, "test.txt", 0, 1, true)
	chunk.Checksum = "invalid_checksum" // Corrupt the checksum

	// Try to receive corrupted chunk multiple times
	maxRetries := 2
	for i := 0; i < maxRetries+1; i++ {
		err = cm.ReceiveChunkWithValidation(chunk, maxRetries)
		if err == nil {
			t.Error("Should fail validation for corrupted checksum")
		}
	}

	// Verify corrupted chunk was tracked
	corruptedChunks, err := cm.GetCorruptedChunks("test.txt")
	if err != nil {
		t.Fatalf("Failed to get corrupted chunks: %v", err)
	}
	if len(corruptedChunks) == 0 {
		t.Error("Should have corrupted chunks")
	}
	if corruptedChunks[0] != maxRetries+1 {
		t.Errorf("Expected %d corruption attempts, got %d", maxRetries+1, corruptedChunks[0])
	}
}

func TestChunkManager_GetValidationErrors(t *testing.T) {
	tempDir := filepath.Join(os.TempDir(), "chunk_manager_validation_errors_test")
	defer os.RemoveAll(tempDir)

	cm, err := NewChunkManager(tempDir, 30*time.Second)
	if err != nil {
		t.Fatalf("Failed to create ChunkManager: %v", err)
	}

	metadata := NewFileMetadata("test.txt", 256, 256)
	err = cm.StartTransfer(metadata, nil)
	if err != nil {
		t.Fatalf("Failed to start transfer: %v", err)
	}

	// Initially no validation errors
	errors, err := cm.GetValidationErrors("test.txt")
	if err != nil {
		t.Fatalf("Failed to get validation errors: %v", err)
	}
	if len(errors) != 0 {
		t.Errorf("Expected 0 validation errors, got %d", len(errors))
	}

	// Create chunk with wrong size to trigger validation error
	data := make([]byte, 128) // Wrong size
	chunk := NewFileChunk(1, 0, data, "test.txt", 0, 1, true)

	err = cm.ReceiveChunkWithValidation(chunk, 3)
	if err == nil {
		t.Error("Should fail validation")
	}

	// Now should have validation errors
	errors, err = cm.GetValidationErrors("test.txt")
	if err != nil {
		t.Fatalf("Failed to get validation errors: %v", err)
	}
	if len(errors) == 0 {
		t.Error("Should have validation errors")
	}

	// Verify error details
	if errors[0].Type != ValidationErrorChunkSize {
		t.Errorf("Expected chunk size error, got %s", errors[0].Type)
	}
	if errors[0].Retryable {
		t.Error("Chunk size error should not be retryable (critical error)")
	}
}

func TestChunkManager_RetryCorruptedChunk(t *testing.T) {
	tempDir := filepath.Join(os.TempDir(), "chunk_manager_retry_corrupted_test")
	defer os.RemoveAll(tempDir)

	cm, err := NewChunkManager(tempDir, 30*time.Second)
	if err != nil {
		t.Fatalf("Failed to create ChunkManager: %v", err)
	}

	metadata := NewFileMetadata("test.txt", 512, 256) // 2 chunks
	err = cm.StartTransfer(metadata, nil)
	if err != nil {
		t.Fatalf("Failed to start transfer: %v", err)
	}

	// Receive first chunk successfully
	data1 := make([]byte, 256)
	chunk1 := NewFileChunk(1, 0, data1, "test.txt", 0, 2, false)
	err = cm.ReceiveChunk(chunk1)
	if err != nil {
		t.Fatalf("Failed to receive chunk 1: %v", err)
	}

	// Verify chunk 0 is received
	missing, err := cm.GetMissingChunks("test.txt")
	if err != nil {
		t.Fatalf("Failed to get missing chunks: %v", err)
	}
	if len(missing) != 1 || missing[0] != 1 {
		t.Errorf("Expected missing chunk 1, got %v", missing)
	}

	// Retry the received chunk (simulate corruption detection)
	err = cm.RetryCorruptedChunk("test.txt", 0)
	if err != nil {
		t.Fatalf("Failed to retry corrupted chunk: %v", err)
	}

	// Verify chunk 0 is now missing again
	missing, err = cm.GetMissingChunks("test.txt")
	if err != nil {
		t.Fatalf("Failed to get missing chunks: %v", err)
	}
	if len(missing) != 2 {
		t.Errorf("Expected 2 missing chunks after retry, got %d", len(missing))
	}

	// Verify retry count increased
	retryCount, err := cm.GetChunkRetryCount("test.txt", 0)
	if err != nil {
		t.Fatalf("Failed to get retry count: %v", err)
	}
	if retryCount != 1 {
		t.Errorf("Expected retry count 1, got %d", retryCount)
	}
}

func TestChunkManager_GetIntegrityReport(t *testing.T) {
	tempDir := filepath.Join(os.TempDir(), "chunk_manager_integrity_report_test")
	defer os.RemoveAll(tempDir)

	cm, err := NewChunkManager(tempDir, 30*time.Second)
	if err != nil {
		t.Fatalf("Failed to create ChunkManager: %v", err)
	}

	metadata := NewFileMetadata("test.txt", 512, 256) // 2 chunks
	metadata.Checksum = "expected_checksum"
	err = cm.StartTransfer(metadata, nil)
	if err != nil {
		t.Fatalf("Failed to start transfer: %v", err)
	}

	// Create chunk with wrong size to trigger validation error
	data := make([]byte, 128) // Wrong size
	chunk := NewFileChunk(1, 0, data, "test.txt", 0, 2, false)

	err = cm.ReceiveChunkWithValidation(chunk, 3)
	if err == nil {
		t.Error("Should fail validation")
	}

	// Get integrity report
	report, err := cm.GetIntegrityReport("test.txt")
	if err != nil {
		t.Fatalf("Failed to get integrity report: %v", err)
	}

	// Verify report details
	if report.Filename != "test.txt" {
		t.Errorf("Expected filename test.txt, got %s", report.Filename)
	}
	if report.IsComplete {
		t.Error("Transfer should not be complete")
	}
	if report.IntegrityPassed {
		t.Error("Integrity should not have passed")
	}
	if report.TotalValidationErrors == 0 {
		t.Error("Should have validation errors")
	}
	if report.ExpectedChecksum != "expected_checksum" {
		t.Errorf("Expected checksum expected_checksum, got %s", report.ExpectedChecksum)
	}
	if !report.HasCriticalErrors() {
		t.Error("Should have critical errors")
	}
}

func TestChunkManager_ReconstructFileWithValidation_Success(t *testing.T) {
	tempDir := filepath.Join(os.TempDir(), "chunk_manager_reconstruct_validation_test")
	defer os.RemoveAll(tempDir)

	cm, err := NewChunkManager(tempDir, 30*time.Second)
	if err != nil {
		t.Fatalf("Failed to create ChunkManager: %v", err)
	}

	// Create test data
	originalData := []byte("Hello, World! This is a test file for validation.")
	chunkSize := int32(20)
	metadata := NewFileMetadata("test.txt", int64(len(originalData)), chunkSize)

	// Calculate and set expected checksum
	expectedChecksum := fmt.Sprintf("%x", sha256.Sum256(originalData))
	metadata.Checksum = expectedChecksum

	err = cm.StartTransfer(metadata, nil)
	if err != nil {
		t.Fatalf("Failed to start transfer: %v", err)
	}

	// Send all chunks with validation
	maxRetries := 3
	for i := 0; i < int(metadata.TotalChunks); i++ {
		start := i * int(chunkSize)
		end := start + int(chunkSize)
		if end > len(originalData) {
			end = len(originalData)
		}

		chunkData := originalData[start:end]
		isLast := (i == int(metadata.TotalChunks)-1)
		
		chunk := NewFileChunk(uint32(i+1), uint32(i), chunkData, "test.txt", 
			int64(start), metadata.TotalChunks, isLast)

		err = cm.ReceiveChunkWithValidation(chunk, maxRetries)
		if err != nil {
			t.Fatalf("Failed to receive chunk %d with validation: %v", i, err)
		}
	}

	// Verify transfer completed successfully
	isComplete, err := cm.IsTransferComplete("test.txt")
	if err != nil {
		t.Fatalf("Failed to check completion: %v", err)
	}
	if !isComplete {
		t.Error("Transfer should be complete")
	}

	// Verify integrity validation passed
	integrityPassed, err := cm.IsIntegrityValidationPassed("test.txt")
	if err != nil {
		t.Fatalf("Failed to check integrity: %v", err)
	}
	if !integrityPassed {
		t.Error("Integrity validation should have passed")
	}

	// Verify final checksum matches
	finalChecksum, err := cm.GetFinalChecksum("test.txt")
	if err != nil {
		t.Fatalf("Failed to get final checksum: %v", err)
	}
	if finalChecksum != expectedChecksum {
		t.Errorf("Final checksum mismatch. Expected: %s, Got: %s", 
			expectedChecksum, finalChecksum)
	}

	// Verify no validation errors
	validationErrors, err := cm.GetValidationErrors("test.txt")
	if err != nil {
		t.Fatalf("Failed to get validation errors: %v", err)
	}
	if len(validationErrors) != 0 {
		t.Errorf("Expected 0 validation errors, got %d", len(validationErrors))
	}
}

func TestIntegrityReport_GetCorruptionRate(t *testing.T) {
	report := &IntegrityReport{
		CorruptedChunks: 3,
	}

	totalChunks := 10
	corruptionRate := report.GetCorruptionRate(totalChunks)
	expected := 30.0
	if corruptionRate != expected {
		t.Errorf("Expected corruption rate %f%%, got %f%%", expected, corruptionRate)
	}

	// Test with zero total chunks
	corruptionRate = report.GetCorruptionRate(0)
	if corruptionRate != 0 {
		t.Errorf("Expected corruption rate 0%% for zero total chunks, got %f%%", corruptionRate)
	}
}

func TestIntegrityReport_HasCriticalErrors(t *testing.T) {
	// Report with only retryable errors
	report := &IntegrityReport{
		TotalValidationErrors: 5,
		RetryableErrors:       5,
	}
	if report.HasCriticalErrors() {
		t.Error("Should not have critical errors when all errors are retryable")
	}

	// Report with some non-retryable errors
	report.RetryableErrors = 3
	if !report.HasCriticalErrors() {
		t.Error("Should have critical errors when some errors are non-retryable")
	}
}