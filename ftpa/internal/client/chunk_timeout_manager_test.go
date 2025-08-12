package internal

import (
	"sync"
	"testing"
	"time"
)

func TestNewChunkTimeoutManager(t *testing.T) {
	config := &TimeoutConfig{
		DefaultTimeout: 10 * time.Second,
		MaxRetries:     5,
		RetryBackoff:   2 * time.Second,
		CheckInterval:  1 * time.Second,
	}

	manager := NewChunkTimeoutManager(config)
	defer manager.Close()

	if manager.defaultTimeout != config.DefaultTimeout {
		t.Errorf("Expected default timeout %v, got %v", config.DefaultTimeout, manager.defaultTimeout)
	}

	if manager.maxRetries != config.MaxRetries {
		t.Errorf("Expected max retries %d, got %d", config.MaxRetries, manager.maxRetries)
	}

	if manager.retryBackoff != config.RetryBackoff {
		t.Errorf("Expected retry backoff %v, got %v", config.RetryBackoff, manager.retryBackoff)
	}
}

func TestNewChunkTimeoutManagerWithDefaultConfig(t *testing.T) {
	manager := NewChunkTimeoutManager(nil)
	defer manager.Close()

	defaultConfig := DefaultTimeoutConfig()

	if manager.defaultTimeout != defaultConfig.DefaultTimeout {
		t.Errorf("Expected default timeout %v, got %v", defaultConfig.DefaultTimeout, manager.defaultTimeout)
	}

	if manager.maxRetries != defaultConfig.MaxRetries {
		t.Errorf("Expected max retries %d, got %d", defaultConfig.MaxRetries, manager.maxRetries)
	}
}

func TestRegisterChunk(t *testing.T) {
	manager := NewChunkTimeoutManager(nil)
	defer manager.Close()

	filename := "test.txt"
	sequenceNum := uint32(0)
	chunkID := uint32(1)

	manager.RegisterChunk(filename, sequenceNum, chunkID, 0)

	// Check that chunk was registered
	chunkTimeout, err := manager.GetChunkTimeout(filename, sequenceNum)
	if err != nil {
		t.Fatalf("Expected chunk to be registered, got error: %v", err)
	}

	if chunkTimeout.Filename != filename {
		t.Errorf("Expected filename %s, got %s", filename, chunkTimeout.Filename)
	}

	if chunkTimeout.SequenceNum != sequenceNum {
		t.Errorf("Expected sequence num %d, got %d", sequenceNum, chunkTimeout.SequenceNum)
	}

	if chunkTimeout.ChunkID != chunkID {
		t.Errorf("Expected chunk ID %d, got %d", chunkID, chunkTimeout.ChunkID)
	}

	if chunkTimeout.RetryCount != 0 {
		t.Errorf("Expected retry count 0, got %d", chunkTimeout.RetryCount)
	}
}

func TestRegisterChunkWithCustomTimeout(t *testing.T) {
	manager := NewChunkTimeoutManager(nil)
	defer manager.Close()

	filename := "test.txt"
	sequenceNum := uint32(0)
	chunkID := uint32(1)
	customTimeout := 5 * time.Second

	manager.RegisterChunk(filename, sequenceNum, chunkID, customTimeout)

	chunkTimeout, err := manager.GetChunkTimeout(filename, sequenceNum)
	if err != nil {
		t.Fatalf("Expected chunk to be registered, got error: %v", err)
	}

	if chunkTimeout.Timeout != customTimeout {
		t.Errorf("Expected timeout %v, got %v", customTimeout, chunkTimeout.Timeout)
	}
}

func TestUnregisterChunk(t *testing.T) {
	manager := NewChunkTimeoutManager(nil)
	defer manager.Close()

	filename := "test.txt"
	sequenceNum := uint32(0)
	chunkID := uint32(1)

	// Register chunk
	manager.RegisterChunk(filename, sequenceNum, chunkID, 0)

	// Verify it's registered
	_, err := manager.GetChunkTimeout(filename, sequenceNum)
	if err != nil {
		t.Fatalf("Expected chunk to be registered, got error: %v", err)
	}

	// Unregister chunk
	manager.UnregisterChunk(filename, sequenceNum)

	// Verify it's unregistered
	_, err = manager.GetChunkTimeout(filename, sequenceNum)
	if err == nil {
		t.Errorf("Expected chunk to be unregistered")
	}
}

func TestMarkChunkReceived(t *testing.T) {
	manager := NewChunkTimeoutManager(nil)
	defer manager.Close()

	filename := "test.txt"
	sequenceNum := uint32(0)
	chunkID := uint32(1)

	// Register chunk
	manager.RegisterChunk(filename, sequenceNum, chunkID, 0)

	// Mark as received
	manager.MarkChunkReceived(filename, sequenceNum)

	// Verify it's removed from monitoring
	_, err := manager.GetChunkTimeout(filename, sequenceNum)
	if err == nil {
		t.Errorf("Expected chunk to be removed after marking as received")
	}
}

func TestUpdateChunkTimeout(t *testing.T) {
	manager := NewChunkTimeoutManager(nil)
	defer manager.Close()

	filename := "test.txt"
	sequenceNum := uint32(0)
	chunkID := uint32(1)
	newTimeout := 15 * time.Second

	// Register chunk
	manager.RegisterChunk(filename, sequenceNum, chunkID, 0)

	// Update timeout
	err := manager.UpdateChunkTimeout(filename, sequenceNum, newTimeout)
	if err != nil {
		t.Fatalf("Failed to update chunk timeout: %v", err)
	}

	// Verify timeout was updated
	chunkTimeout, err := manager.GetChunkTimeout(filename, sequenceNum)
	if err != nil {
		t.Fatalf("Failed to get chunk timeout: %v", err)
	}

	if chunkTimeout.Timeout != newTimeout {
		t.Errorf("Expected timeout %v, got %v", newTimeout, chunkTimeout.Timeout)
	}
}

func TestGetTimedOutChunks(t *testing.T) {
	config := &TimeoutConfig{
		DefaultTimeout: 50 * time.Millisecond, // Very short timeout for testing
		MaxRetries:     3,
		RetryBackoff:   10 * time.Millisecond,
		CheckInterval:  25 * time.Millisecond,
	}

	manager := NewChunkTimeoutManager(config)
	defer manager.Close()

	filename := "test.txt"
	sequenceNum := uint32(0)
	chunkID := uint32(1)

	// Register chunk
	manager.RegisterChunk(filename, sequenceNum, chunkID, 0)

	// Initially, no chunks should be timed out
	timedOut := manager.GetTimedOutChunks()
	if len(timedOut) != 0 {
		t.Errorf("Expected 0 timed out chunks initially, got %d", len(timedOut))
	}

	// Wait for timeout - need to wait longer than the timeout period
	time.Sleep(75 * time.Millisecond)

	// Now chunk should be timed out
	timedOut = manager.GetTimedOutChunks()
	if len(timedOut) != 1 {
		t.Errorf("Expected 1 timed out chunk, got %d", len(timedOut))
	}

	if len(timedOut) > 0 && timedOut[0].SequenceNum != sequenceNum {
		t.Errorf("Expected timed out chunk sequence %d, got %d", sequenceNum, timedOut[0].SequenceNum)
	}
}

func TestMarkChunkForRetry(t *testing.T) {
	manager := NewChunkTimeoutManager(nil)
	defer manager.Close()

	filename := "test.txt"
	sequenceNum := uint32(0)
	chunkID := uint32(1)

	// Register chunk
	manager.RegisterChunk(filename, sequenceNum, chunkID, 0)

	// Mark for retry
	err := manager.MarkChunkForRetry(filename, sequenceNum)
	if err != nil {
		t.Fatalf("Failed to mark chunk for retry: %v", err)
	}

	// Verify retry count increased
	chunkTimeout, err := manager.GetChunkTimeout(filename, sequenceNum)
	if err != nil {
		t.Fatalf("Failed to get chunk timeout: %v", err)
	}

	if chunkTimeout.RetryCount != 1 {
		t.Errorf("Expected retry count 1, got %d", chunkTimeout.RetryCount)
	}

	if !chunkTimeout.IsTimedOut {
		t.Errorf("Expected chunk to be marked as timed out")
	}

	if chunkTimeout.Priority == 0 {
		t.Errorf("Expected priority to be increased, got %d", chunkTimeout.Priority)
	}
}

func TestExponentialBackoff(t *testing.T) {
	config := &TimeoutConfig{
		DefaultTimeout: 10 * time.Second,
		MaxRetries:     3,
		RetryBackoff:   1 * time.Second,
		CheckInterval:  1 * time.Second,
	}

	manager := NewChunkTimeoutManager(config)
	defer manager.Close()

	filename := "test.txt"
	sequenceNum := uint32(0)
	chunkID := uint32(1)

	// Register chunk
	manager.RegisterChunk(filename, sequenceNum, chunkID, 0)

	// First retry
	err := manager.MarkChunkForRetry(filename, sequenceNum)
	if err != nil {
		t.Fatalf("Failed to mark chunk for retry: %v", err)
	}

	chunkTimeout1, _ := manager.GetChunkTimeout(filename, sequenceNum)
	firstRetryDelay := chunkTimeout1.NextRetryAt.Sub(chunkTimeout1.LastRetryAt)

	// Second retry
	err = manager.MarkChunkForRetry(filename, sequenceNum)
	if err != nil {
		t.Fatalf("Failed to mark chunk for retry: %v", err)
	}

	chunkTimeout2, _ := manager.GetChunkTimeout(filename, sequenceNum)
	secondRetryDelay := chunkTimeout2.NextRetryAt.Sub(chunkTimeout2.LastRetryAt)

	// Second delay should be longer than first (exponential backoff)
	if secondRetryDelay <= firstRetryDelay {
		t.Errorf("Expected exponential backoff: second delay (%v) should be > first delay (%v)", 
			secondRetryDelay, firstRetryDelay)
	}
}

func TestTimeoutCallback(t *testing.T) {
	config := &TimeoutConfig{
		DefaultTimeout: 50 * time.Millisecond,
		MaxRetries:     3,
		RetryBackoff:   10 * time.Millisecond,
		CheckInterval:  25 * time.Millisecond,
	}

	manager := NewChunkTimeoutManager(config)
	defer manager.Close()

	filename := "test.txt"
	sequenceNum := uint32(0)
	chunkID := uint32(1)

	// Set up callback
	var callbackCalled bool
	var callbackMutex sync.Mutex
	callback := func(timeout *ChunkTimeout) error {
		callbackMutex.Lock()
		defer callbackMutex.Unlock()
		callbackCalled = true
		return nil
	}

	manager.SetTimeoutCallback(filename, callback)

	// Register chunk
	manager.RegisterChunk(filename, sequenceNum, chunkID, 0)

	// Wait for timeout and callback
	time.Sleep(100 * time.Millisecond)

	// Check if callback was called
	callbackMutex.Lock()
	called := callbackCalled
	callbackMutex.Unlock()

	if !called {
		t.Errorf("Expected timeout callback to be called")
	}
}

func TestGetActiveTimeouts(t *testing.T) {
	manager := NewChunkTimeoutManager(nil)
	defer manager.Close()

	// Initially no active timeouts
	count := manager.GetActiveTimeouts()
	if count != 0 {
		t.Errorf("Expected 0 active timeouts initially, got %d", count)
	}

	// Register some chunks
	manager.RegisterChunk("file1.txt", 0, 1, 0)
	manager.RegisterChunk("file1.txt", 1, 2, 0)
	manager.RegisterChunk("file2.txt", 0, 3, 0)

	count = manager.GetActiveTimeouts()
	if count != 3 {
		t.Errorf("Expected 3 active timeouts, got %d", count)
	}

	// Test file-specific count
	fileCount := manager.GetActiveTimeoutsForFile("file1.txt")
	if fileCount != 2 {
		t.Errorf("Expected 2 active timeouts for file1.txt, got %d", fileCount)
	}
}

func TestCleanupFile(t *testing.T) {
	manager := NewChunkTimeoutManager(nil)
	defer manager.Close()

	filename := "test.txt"

	// Register chunks and callback
	manager.RegisterChunk(filename, 0, 1, 0)
	manager.RegisterChunk(filename, 1, 2, 0)
	manager.SetTimeoutCallback(filename, func(timeout *ChunkTimeout) error { return nil })

	// Verify chunks are registered
	count := manager.GetActiveTimeoutsForFile(filename)
	if count != 2 {
		t.Errorf("Expected 2 active timeouts, got %d", count)
	}

	// Cleanup file
	manager.CleanupFile(filename)

	// Verify cleanup
	count = manager.GetActiveTimeoutsForFile(filename)
	if count != 0 {
		t.Errorf("Expected 0 active timeouts after cleanup, got %d", count)
	}

	// Verify callback was removed
	_, err := manager.GetChunkTimeout(filename, 0)
	if err == nil {
		t.Errorf("Expected chunk to be removed after cleanup")
	}
}

func TestGetTimeoutStatistics(t *testing.T) {
	manager := NewChunkTimeoutManager(nil)
	defer manager.Close()

	// Register chunks with different retry counts
	manager.RegisterChunk("file1.txt", 0, 1, 0)
	manager.RegisterChunk("file1.txt", 1, 2, 0)
	manager.RegisterChunk("file2.txt", 0, 3, 0)

	// Mark some for retry
	manager.MarkChunkForRetry("file1.txt", 0)
	manager.MarkChunkForRetry("file1.txt", 0) // Second retry
	manager.MarkChunkForRetry("file2.txt", 0)

	stats := manager.GetTimeoutStatistics()

	if stats.TotalActiveTimeouts != 3 {
		t.Errorf("Expected 3 total active timeouts, got %d", stats.TotalActiveTimeouts)
	}

	if stats.FileTimeouts["file1.txt"] != 2 {
		t.Errorf("Expected 2 timeouts for file1.txt, got %d", stats.FileTimeouts["file1.txt"])
	}

	if stats.FileTimeouts["file2.txt"] != 1 {
		t.Errorf("Expected 1 timeout for file2.txt, got %d", stats.FileTimeouts["file2.txt"])
	}

	if stats.TotalRetries != 3 {
		t.Errorf("Expected 3 total retries, got %d", stats.TotalRetries)
	}

	if stats.MaxRetryCount != 2 {
		t.Errorf("Expected max retry count 2, got %d", stats.MaxRetryCount)
	}
}

func TestGetChunksExceedingMaxRetries(t *testing.T) {
	config := &TimeoutConfig{
		DefaultTimeout: 10 * time.Second,
		MaxRetries:     2, // Low max retries for testing
		RetryBackoff:   1 * time.Second,
		CheckInterval:  1 * time.Second,
	}

	manager := NewChunkTimeoutManager(config)
	defer manager.Close()

	filename := "test.txt"
	sequenceNum := uint32(0)
	chunkID := uint32(1)

	// Register chunk
	manager.RegisterChunk(filename, sequenceNum, chunkID, 0)

	// Exceed max retries
	manager.MarkChunkForRetry(filename, sequenceNum) // Retry 1
	manager.MarkChunkForRetry(filename, sequenceNum) // Retry 2
	manager.MarkChunkForRetry(filename, sequenceNum) // Retry 3 (exceeds max)

	exceededChunks := manager.GetChunksExceedingMaxRetries()
	if len(exceededChunks) != 1 {
		t.Errorf("Expected 1 chunk exceeding max retries, got %d", len(exceededChunks))
	}

	if len(exceededChunks) > 0 && exceededChunks[0].SequenceNum != sequenceNum {
		t.Errorf("Expected chunk sequence %d to exceed max retries, got %d", 
			sequenceNum, exceededChunks[0].SequenceNum)
	}
}

func TestIsChunkTimedOut(t *testing.T) {
	config := &TimeoutConfig{
		DefaultTimeout: 50 * time.Millisecond,
		MaxRetries:     3,
		RetryBackoff:   10 * time.Millisecond,
		CheckInterval:  25 * time.Millisecond,
	}

	manager := NewChunkTimeoutManager(config)
	defer manager.Close()

	filename := "test.txt"
	sequenceNum := uint32(0)
	chunkID := uint32(1)

	// Register chunk
	manager.RegisterChunk(filename, sequenceNum, chunkID, 0)

	// Initially should not be timed out
	timedOut, err := manager.IsChunkTimedOut(filename, sequenceNum)
	if err != nil {
		t.Fatalf("Failed to check if chunk is timed out: %v", err)
	}
	if timedOut {
		t.Errorf("Expected chunk not to be timed out initially")
	}

	// Wait for timeout
	time.Sleep(75 * time.Millisecond)

	// Now should be timed out
	timedOut, err = manager.IsChunkTimedOut(filename, sequenceNum)
	if err != nil {
		t.Fatalf("Failed to check if chunk is timed out: %v", err)
	}
	if !timedOut {
		t.Errorf("Expected chunk to be timed out after waiting")
	}
}

func TestSetMaxRetries(t *testing.T) {
	manager := NewChunkTimeoutManager(nil)
	defer manager.Close()

	filename := "test.txt"

	// Register chunks
	manager.RegisterChunk(filename, 0, 1, 0)
	manager.RegisterChunk(filename, 1, 2, 0)

	// Update max retries
	newMaxRetries := 10
	manager.SetMaxRetries(filename, newMaxRetries)

	// Verify max retries were updated
	chunkTimeout1, _ := manager.GetChunkTimeout(filename, 0)
	chunkTimeout2, _ := manager.GetChunkTimeout(filename, 1)

	if chunkTimeout1.MaxRetries != newMaxRetries {
		t.Errorf("Expected max retries %d for chunk 0, got %d", newMaxRetries, chunkTimeout1.MaxRetries)
	}

	if chunkTimeout2.MaxRetries != newMaxRetries {
		t.Errorf("Expected max retries %d for chunk 1, got %d", newMaxRetries, chunkTimeout2.MaxRetries)
	}
}

func TestConcurrentAccess(t *testing.T) {
	manager := NewChunkTimeoutManager(nil)
	defer manager.Close()

	filename := "test.txt"
	numGoroutines := 10
	chunksPerGoroutine := 10

	var wg sync.WaitGroup

	// Concurrent registration
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for j := 0; j < chunksPerGoroutine; j++ {
				sequenceNum := uint32(goroutineID*chunksPerGoroutine + j)
				chunkID := sequenceNum + 1
				manager.RegisterChunk(filename, sequenceNum, chunkID, 0)
			}
		}(i)
	}

	wg.Wait()

	// Verify all chunks were registered
	expectedCount := numGoroutines * chunksPerGoroutine
	actualCount := manager.GetActiveTimeoutsForFile(filename)
	if actualCount != expectedCount {
		t.Errorf("Expected %d active timeouts, got %d", expectedCount, actualCount)
	}

	// Concurrent operations
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for j := 0; j < chunksPerGoroutine/2; j++ {
				sequenceNum := uint32(goroutineID*chunksPerGoroutine + j)
				
				// Mix of operations
				if j%2 == 0 {
					manager.MarkChunkForRetry(filename, sequenceNum)
				} else {
					manager.MarkChunkReceived(filename, sequenceNum)
				}
			}
		}(i)
	}

	wg.Wait()

	// Verify operations completed without race conditions
	stats := manager.GetTimeoutStatistics()
	if stats.TotalActiveTimeouts < 0 {
		t.Errorf("Invalid timeout statistics after concurrent operations")
	}
}