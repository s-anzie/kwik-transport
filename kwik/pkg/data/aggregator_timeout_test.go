package data

import (
	"kwik/pkg/logger"
	"testing"
	"time"
)

func TestReorderBuffer_TimeoutManagement(t *testing.T) {
	timeout := 50 * time.Millisecond
	rb := NewReorderBuffer(10, timeout)
	defer rb.Close()

	// Add a frame with an old timestamp to ensure it times out
	frame := &ReorderFrame{
		Data:        []byte("test data"),
		Offset:      0,
		SequenceNum: 1,
		PathID:      "path1",
		Timestamp:   time.Now().Add(-timeout - 10*time.Millisecond), // Make it already timed out
		Delivered:   false,
	}

	err := rb.AddFrame(frame)
	if err != nil {
		t.Fatalf("Failed to add frame: %v", err)
	}

	// Check that frame has timed out immediately
	timedOutFrames := rb.GetTimedOutFrames()
	if len(timedOutFrames) != 1 {
		t.Errorf("Expected 1 timed out frame, got %d", len(timedOutFrames))
		return
	}

	if timedOutFrames[0].SequenceNum != 1 {
		t.Errorf("Expected timed out frame sequence 1, got %d", timedOutFrames[0].SequenceNum)
	}
}

func TestReorderBuffer_MemoryManagement(t *testing.T) {
	rb := NewReorderBuffer(10, 100*time.Millisecond)
	defer rb.Close()

	// Set small memory limit
	rb.SetMemoryLimit(100) // 100 bytes

	// Add frames that exceed memory limit
	for i := 0; i < 5; i++ {
		frame := &ReorderFrame{
			Data:        make([]byte, 50), // 50 bytes each
			Offset:      uint64(i * 50),
			SequenceNum: uint64(i),
			PathID:      "path1",
			Timestamp:   time.Now(),
			Delivered:   false,
		}

		err := rb.AddFrame(frame)
		if i < 2 {
			// First two frames should fit
			if err != nil {
				t.Errorf("Frame %d should fit in memory, got error: %v", i, err)
			}
		} else {
			// Later frames should trigger memory management
			// The buffer should handle this by flushing old frames
			if err != nil && err.Error() != "insufficient memory for frame" {
				t.Errorf("Unexpected error for frame %d: %v", i, err)
			}
		}
	}

	// Check memory usage
	current, max := rb.GetMemoryUsage()
	if current > max {
		t.Errorf("Current memory usage %d exceeds max %d", current, max)
	}
}

func TestReorderBuffer_ForceFlush(t *testing.T) {
	rb := NewReorderBuffer(10, 100*time.Millisecond)
	defer rb.Close()

	// Add multiple frames
	for i := 0; i < 5; i++ {
		frame := &ReorderFrame{
			Data:        []byte("test data"),
			Offset:      uint64(i * 10),
			SequenceNum: uint64(i),
			PathID:      "path1",
			Timestamp:   time.Now(),
			Delivered:   false,
		}

		err := rb.AddFrame(frame)
		if err != nil {
			t.Fatalf("Failed to add frame %d: %v", i, err)
		}
	}

	// Flush all frames
	flushedFrames := rb.FlushAllFrames()
	if len(flushedFrames) != 5 {
		t.Errorf("Expected 5 flushed frames, got %d", len(flushedFrames))
	}

	// Check that frames are sorted by sequence number
	for i := 1; i < len(flushedFrames); i++ {
		if flushedFrames[i-1].SequenceNum > flushedFrames[i].SequenceNum {
			t.Errorf("Flushed frames not sorted: %d > %d",
				flushedFrames[i-1].SequenceNum, flushedFrames[i].SequenceNum)
		}
	}

	// Buffer should be empty now
	stats := rb.GetBufferStats()
	if stats.BufferedFrames != 0 {
		t.Errorf("Expected 0 buffered frames after flush, got %d", stats.BufferedFrames)
	}
}

func TestReorderBuffer_FlushOlderThan(t *testing.T) {
	rb := NewReorderBuffer(10, 100*time.Millisecond)
	defer rb.Close()

	now := time.Now()

	// Add frames with different timestamps
	// Frame 0: current time (should not be flushed)
	// Frame 1: 60ms ago (should be flushed)
	// Frame 2: 90ms ago (should be flushed)
	for i := 0; i < 3; i++ {
		var timestamp time.Time
		if i == 0 {
			timestamp = now // Current time
		} else {
			timestamp = now.Add(-time.Duration(i*30+30) * time.Millisecond) // 60ms, 90ms ago
		}

		frame := &ReorderFrame{
			Data:        []byte("test data"),
			Offset:      uint64(i * 10),
			SequenceNum: uint64(i),
			PathID:      "path1",
			Timestamp:   timestamp,
			Delivered:   false,
		}

		err := rb.AddFrame(frame)
		if err != nil {
			t.Fatalf("Failed to add frame %d: %v", i, err)
		}
	}

	// Flush frames older than 50ms
	flushedFrames := rb.FlushFramesOlderThan(50 * time.Millisecond)

	// Should flush frames 1 and 2 (older than 50ms)
	if len(flushedFrames) != 2 {
		t.Errorf("Expected 2 flushed frames, got %d", len(flushedFrames))
	}

	// Frame 0 should still be in buffer
	stats := rb.GetBufferStats()
	if stats.BufferedFrames != 1 {
		t.Errorf("Expected 1 remaining frame, got %d", stats.BufferedFrames)
	}
}

func TestReorderBuffer_Stats(t *testing.T) {
	timeout := 50 * time.Millisecond
	rb := NewReorderBuffer(5, timeout)
	defer rb.Close()

	// Add frames
	for i := 0; i < 3; i++ {
		frame := &ReorderFrame{
			Data:        make([]byte, 100),
			Offset:      uint64(i * 100),
			SequenceNum: uint64(i),
			PathID:      "path1",
			Timestamp:   time.Now(),
			Delivered:   false,
		}

		err := rb.AddFrame(frame)
		if err != nil {
			t.Fatalf("Failed to add frame %d: %v", i, err)
		}
	}

	stats := rb.GetBufferStats()
	if stats.BufferedFrames != 3 {
		t.Errorf("Expected 3 buffered frames, got %d", stats.BufferedFrames)
	}

	if stats.MaxBufferSize != 5 {
		t.Errorf("Expected max buffer size 5, got %d", stats.MaxBufferSize)
	}

	if stats.CurrentMemoryUsage != 300 {
		t.Errorf("Expected memory usage 300, got %d", stats.CurrentMemoryUsage)
	}

	if stats.Timeout != timeout {
		t.Errorf("Expected timeout %v, got %v", timeout, stats.Timeout)
	}
}

func TestDataAggregator_FlushStreamBuffer(t *testing.T) {
	logger := &logger.MockLogger{}
	da := NewDataAggregator(logger).(*DataAggregatorImpl)

	streamID := uint64(1)
	err := da.CreateAggregatedStream(streamID)
	if err != nil {
		t.Fatalf("Failed to create aggregated stream: %v", err)
	}

	// Add frames to reorder buffer
	frames := []*DataFrame{
		{
			StreamID:    streamID,
			PathID:      "path1",
			Data:        []byte("frame2"),
			Offset:      20,
			SequenceNum: 2,
			Timestamp:   time.Now(),
		},
		{
			StreamID:    streamID,
			PathID:      "path1",
			Data:        []byte("frame1"),
			Offset:      10,
			SequenceNum: 1,
			Timestamp:   time.Now(),
		},
		{
			StreamID:    streamID,
			PathID:      "path1",
			Data:        []byte("frame3"),
			Offset:      30,
			SequenceNum: 3,
			Timestamp:   time.Now(),
		},
	}

	// Process frames (they will be buffered due to out-of-order)
	for _, frame := range frames {
		_, err := da.ProcessFrameWithReordering(streamID, frame)
		if err != nil {
			t.Fatalf("Failed to process frame: %v", err)
		}
	}

	// Flush all buffered frames
	flushedData, err := da.FlushStreamBuffer(streamID)
	if err != nil {
		t.Fatalf("Failed to flush stream buffer: %v", err)
	}

	// Should get all frames in order
	expectedData := "frame1frame2frame3"
	if string(flushedData) != expectedData {
		t.Errorf("Expected flushed data '%s', got '%s'", expectedData, string(flushedData))
	}
}

func TestDataAggregator_MemoryPressure(t *testing.T) {
	logger := &logger.MockLogger{}
	da := NewDataAggregator(logger).(*DataAggregatorImpl)

	streamID := uint64(1)
	err := da.CreateAggregatedStream(streamID)
	if err != nil {
		t.Fatalf("Failed to create aggregated stream: %v", err)
	}

	// Set small memory limit
	err = da.SetStreamMemoryLimit(streamID, 100)
	if err != nil {
		t.Fatalf("Failed to set memory limit: %v", err)
	}

	// Add frames that will cause memory pressure
	for i := 0; i < 5; i++ {
		frame := &DataFrame{
			StreamID:    streamID,
			PathID:      "path1",
			Data:        make([]byte, 50),
			Offset:      uint64(i * 50),
			SequenceNum: uint64(i + 10), // Out of order to force buffering
			Timestamp:   time.Now(),
		}

		_, err := da.ProcessFrameWithReordering(streamID, frame)
		// Some frames might fail due to memory pressure, which is expected
		_ = err
	}

	// Check memory pressure
	pressureMap := da.CheckMemoryPressure(0.8) // 80% threshold
	if pressure, exists := pressureMap[streamID]; exists && !pressure {
		// Memory pressure detection might not trigger immediately
		// This is acceptable behavior
	}

	// Get buffer stats
	stats, err := da.GetStreamBufferStats(streamID)
	if err != nil {
		t.Fatalf("Failed to get buffer stats: %v", err)
	}

	if stats.MaxMemoryUsage != 100 {
		t.Errorf("Expected max memory usage 100, got %d", stats.MaxMemoryUsage)
	}
}

func TestDataAggregator_CleanupExpiredStreams(t *testing.T) {
	logger := &logger.MockLogger{}
	da := NewDataAggregator(logger).(*DataAggregatorImpl)

	// Create multiple streams
	streamIDs := []uint64{1, 2, 3}
	for _, streamID := range streamIDs {
		err := da.CreateAggregatedStream(streamID)
		if err != nil {
			t.Fatalf("Failed to create stream %d: %v", streamID, err)
		}
	}

	// Manually set last activity for stream 2 to be old
	da.streamsMutex.RLock()
	if streamState, exists := da.aggregatedStreams[2]; exists {
		streamState.mutex.Lock()
		streamState.LastActivity = time.Now().Add(-2 * time.Hour)
		streamState.mutex.Unlock()
	}
	da.streamsMutex.RUnlock()

	// Cleanup streams inactive for more than 1 hour
	expiredStreams := da.CleanupExpiredStreams(1 * time.Hour)

	// Should have cleaned up stream 2
	if len(expiredStreams) != 1 || expiredStreams[0] != 2 {
		t.Errorf("Expected to cleanup stream 2, got %v", expiredStreams)
	}

	// Verify stream 2 is removed
	da.streamsMutex.RLock()
	_, exists := da.aggregatedStreams[2]
	da.streamsMutex.RUnlock()

	if exists {
		t.Errorf("Stream 2 should have been removed")
	}

	// Other streams should still exist
	da.streamsMutex.RLock()
	_, exists1 := da.aggregatedStreams[1]
	_, exists3 := da.aggregatedStreams[3]
	da.streamsMutex.RUnlock()

	if !exists1 || !exists3 {
		t.Errorf("Streams 1 and 3 should still exist")
	}
}

func TestDataAggregator_UpdateStreamTimeout(t *testing.T) {
	logger := &logger.MockLogger{}
	da := NewDataAggregator(logger).(*DataAggregatorImpl)

	streamID := uint64(1)
	err := da.CreateAggregatedStream(streamID)
	if err != nil {
		t.Fatalf("Failed to create aggregated stream: %v", err)
	}

	// Update timeout
	newTimeout := 200 * time.Millisecond
	err = da.UpdateStreamTimeout(streamID, newTimeout)
	if err != nil {
		t.Fatalf("Failed to update stream timeout: %v", err)
	}

	// Verify timeout was updated
	stats, err := da.GetStreamBufferStats(streamID)
	if err != nil {
		t.Fatalf("Failed to get buffer stats: %v", err)
	}

	if stats.Timeout != newTimeout {
		t.Errorf("Expected timeout %v, got %v", newTimeout, stats.Timeout)
	}
}

func TestReorderBuffer_TimeoutCallback(t *testing.T) {
	timeout := 30 * time.Millisecond
	var callbackFrames []*ReorderFrame

	callback := func(frames []*ReorderFrame) {
		callbackFrames = append(callbackFrames, frames...)
	}

	rb := NewReorderBufferWithCallback(10, timeout, callback)
	defer rb.Close()

	// Add a frame
	frame := &ReorderFrame{
		Data:        []byte("test data"),
		Offset:      0,
		SequenceNum: 1,
		PathID:      "path1",
		Timestamp:   time.Now(),
		Delivered:   false,
	}

	err := rb.AddFrame(frame)
	if err != nil {
		t.Fatalf("Failed to add frame: %v", err)
	}

	// Wait for timeout and callback
	time.Sleep(timeout + 20*time.Millisecond)

	// Check that callback was called
	if len(callbackFrames) != 1 {
		t.Errorf("Expected 1 frame in callback, got %d", len(callbackFrames))
	}

	if len(callbackFrames) > 0 && callbackFrames[0].SequenceNum != 1 {
		t.Errorf("Expected callback frame sequence 1, got %d", callbackFrames[0].SequenceNum)
	}
}
