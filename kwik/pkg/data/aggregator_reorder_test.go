package data

import (
	"kwik/pkg/logger"
	"testing"
	"time"
)

func TestReorderBuffer_AddFrame(t *testing.T) {
	rb := NewReorderBuffer(5, 100*time.Millisecond)

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

	if rb.GetBufferSize() != 1 {
		t.Errorf("Expected buffer size 1, got %d", rb.GetBufferSize())
	}
}

func TestReorderBuffer_DuplicateFrame(t *testing.T) {
	rb := NewReorderBuffer(5, 100*time.Millisecond)

	frame := &ReorderFrame{
		Data:        []byte("test data"),
		Offset:      0,
		SequenceNum: 1,
		PathID:      "path1",
		Timestamp:   time.Now(),
		Delivered:   false,
	}

	// Add frame first time
	err := rb.AddFrame(frame)
	if err != nil {
		t.Fatalf("Failed to add frame: %v", err)
	}

	// Try to add same frame again
	err = rb.AddFrame(frame)
	if err == nil {
		t.Errorf("Expected error for duplicate frame, got nil")
	}
}

func TestReorderBuffer_BufferFull(t *testing.T) {
	rb := NewReorderBuffer(2, 100*time.Millisecond) // Small buffer

	// Fill buffer
	for i := 0; i < 2; i++ {
		frame := &ReorderFrame{
			Data:        []byte("test data"),
			Offset:      uint64(i),
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

	// Try to add one more frame - should succeed due to automatic flushing
	frame := &ReorderFrame{
		Data:        []byte("overflow data"),
		Offset:      2,
		SequenceNum: 2,
		PathID:      "path1",
		Timestamp:   time.Now(),
		Delivered:   false,
	}

	err := rb.AddFrame(frame)
	if err != nil {
		t.Errorf("Expected frame to be added with automatic flushing, got error: %v", err)
	}

	// Check that some frames were flushed to make room
	stats := rb.GetBufferStats()
	if stats.ForcedFlushCount == 0 {
		t.Errorf("Expected some frames to be force flushed, got count: %d", stats.ForcedFlushCount)
	}
}

func TestReorderBuffer_GetOrderedFrames(t *testing.T) {
	rb := NewReorderBuffer(10, 100*time.Millisecond)

	// Add frames out of order
	frames := []*ReorderFrame{
		{Data: []byte("frame2"), SequenceNum: 2, Timestamp: time.Now()},
		{Data: []byte("frame0"), SequenceNum: 0, Timestamp: time.Now()},
		{Data: []byte("frame1"), SequenceNum: 1, Timestamp: time.Now()},
		{Data: []byte("frame4"), SequenceNum: 4, Timestamp: time.Now()},
	}

	for _, frame := range frames {
		err := rb.AddFrame(frame)
		if err != nil {
			t.Fatalf("Failed to add frame %d: %v", frame.SequenceNum, err)
		}
	}

	// Get ordered frames
	orderedFrames := rb.GetOrderedFrames()

	// Should get frames 0, 1, 2 in order (frame 4 should remain buffered)
	expectedSeqs := []uint64{0, 1, 2}
	if len(orderedFrames) != len(expectedSeqs) {
		t.Errorf("Expected %d ordered frames, got %d", len(expectedSeqs), len(orderedFrames))
	}

	for i, frame := range orderedFrames {
		if frame.SequenceNum != expectedSeqs[i] {
			t.Errorf("Expected sequence %d at position %d, got %d", expectedSeqs[i], i, frame.SequenceNum)
		}
	}

	// Frame 4 should still be in buffer
	if rb.GetBufferSize() != 1 {
		t.Errorf("Expected 1 frame remaining in buffer, got %d", rb.GetBufferSize())
	}
}

func TestReorderBuffer_GetTimedOutFrames(t *testing.T) {
	rb := NewReorderBuffer(10, 50*time.Millisecond) // Short timeout

	// Add frame with old timestamp
	oldFrame := &ReorderFrame{
		Data:        []byte("old frame"),
		SequenceNum: 5,
		Timestamp:   time.Now().Add(-100 * time.Millisecond), // Old timestamp
	}

	// Add frame with recent timestamp
	recentFrame := &ReorderFrame{
		Data:        []byte("recent frame"),
		SequenceNum: 6,
		Timestamp:   time.Now(),
	}

	err := rb.AddFrame(oldFrame)
	if err != nil {
		t.Fatalf("Failed to add old frame: %v", err)
	}

	err = rb.AddFrame(recentFrame)
	if err != nil {
		t.Fatalf("Failed to add recent frame: %v", err)
	}

	// Get timed-out frames
	timedOutFrames := rb.GetTimedOutFrames()

	// Should get the old frame
	if len(timedOutFrames) != 1 {
		t.Errorf("Expected 1 timed-out frame, got %d", len(timedOutFrames))
	}

	if len(timedOutFrames) > 0 && timedOutFrames[0].SequenceNum != 5 {
		t.Errorf("Expected timed-out frame sequence 5, got %d", timedOutFrames[0].SequenceNum)
	}

	// Recent frame should still be in buffer
	if rb.GetBufferSize() != 1 {
		t.Errorf("Expected 1 frame remaining in buffer, got %d", rb.GetBufferSize())
	}
}

func TestDataAggregator_ProcessFrameWithReordering(t *testing.T) {
	logger := &logger.MockLogger{}
	da := NewDataAggregator(logger).(*DataAggregatorImpl)

	streamID := uint64(1)
	err := da.CreateAggregatedStream(streamID)
	if err != nil {
		t.Fatalf("Failed to create aggregated stream: %v", err)
	}

	// Create frames out of order
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
			Data:        []byte("frame0"),
			Offset:      0,
			SequenceNum: 0,
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
	}

	// Process frames
	var totalDelivered []byte
	for _, frame := range frames {
		delivered, err := da.ProcessFrameWithReordering(streamID, frame)
		if err != nil {
			t.Fatalf("Failed to process frame %d: %v", frame.SequenceNum, err)
		}
		totalDelivered = append(totalDelivered, delivered...)
	}

	// Should have delivered frames 0, 1, 2 in order
	expectedData := "frame0frame1frame2"
	if string(totalDelivered) != expectedData {
		t.Errorf("Expected delivered data '%s', got '%s'", expectedData, string(totalDelivered))
	}

	// Check aggregated stream buffer
	aggregatedData, err := da.AggregateData(streamID)
	if err != nil {
		t.Fatalf("Failed to get aggregated data: %v", err)
	}

	if string(aggregatedData) != expectedData {
		t.Errorf("Expected aggregated data '%s', got '%s'", expectedData, string(aggregatedData))
	}
}

func TestDataAggregator_ProcessFrameWithReordering_OutOfOrder(t *testing.T) {
	logger := &logger.MockLogger{}
	da := NewDataAggregator(logger).(*DataAggregatorImpl)

	streamID := uint64(1)
	err := da.CreateAggregatedStream(streamID)
	if err != nil {
		t.Fatalf("Failed to create aggregated stream: %v", err)
	}

	// Process frames with gaps
	frames := []*DataFrame{
		{
			StreamID:    streamID,
			PathID:      "path1",
			Data:        []byte("frame0"),
			Offset:      0,
			SequenceNum: 0,
			Timestamp:   time.Now(),
		},
		{
			StreamID:    streamID,
			PathID:      "path1",
			Data:        []byte("frame2"), // Skip frame 1
			Offset:      20,
			SequenceNum: 2,
			Timestamp:   time.Now(),
		},
		{
			StreamID:    streamID,
			PathID:      "path1",
			Data:        []byte("frame4"), // Skip frame 3
			Offset:      40,
			SequenceNum: 4,
			Timestamp:   time.Now(),
		},
	}

	// Process first frame - should be delivered immediately
	delivered, err := da.ProcessFrameWithReordering(streamID, frames[0])
	if err != nil {
		t.Fatalf("Failed to process frame 0: %v", err)
	}
	if string(delivered) != "frame0" {
		t.Errorf("Expected 'frame0', got '%s'", string(delivered))
	}

	// Process frame 2 - should be buffered (waiting for frame 1)
	delivered, err = da.ProcessFrameWithReordering(streamID, frames[1])
	if err != nil {
		t.Fatalf("Failed to process frame 2: %v", err)
	}
	if len(delivered) != 0 {
		t.Errorf("Expected no delivery for out-of-order frame 2, got '%s'", string(delivered))
	}

	// Process frame 4 - should be buffered (waiting for frame 3)
	delivered, err = da.ProcessFrameWithReordering(streamID, frames[2])
	if err != nil {
		t.Fatalf("Failed to process frame 4: %v", err)
	}
	if len(delivered) != 0 {
		t.Errorf("Expected no delivery for out-of-order frame 4, got '%s'", string(delivered))
	}

	// Check reorder buffer stats
	stats, err := da.GetReorderBufferStats(streamID)
	if err != nil {
		t.Fatalf("Failed to get reorder buffer stats: %v", err)
	}

	if stats.BufferedFrames != 2 {
		t.Errorf("Expected 2 buffered frames, got %d", stats.BufferedFrames)
	}

	if stats.NextExpectedSeq != 1 {
		t.Errorf("Expected next expected sequence 1, got %d", stats.NextExpectedSeq)
	}
}

func TestDataAggregator_FlushReorderBuffer(t *testing.T) {
	logger := &logger.MockLogger{}
	da := NewDataAggregator(logger).(*DataAggregatorImpl)

	streamID := uint64(1)
	err := da.CreateAggregatedStream(streamID)
	if err != nil {
		t.Fatalf("Failed to create aggregated stream: %v", err)
	}

	// Add some out-of-order frames
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
			Data:        []byte("frame4"),
			Offset:      40,
			SequenceNum: 4,
			Timestamp:   time.Now(),
		},
	}

	for _, frame := range frames {
		_, err := da.ProcessFrameWithReordering(streamID, frame)
		if err != nil {
			t.Fatalf("Failed to process frame %d: %v", frame.SequenceNum, err)
		}
	}

	// Flush the buffer
	flushedData, err := da.FlushReorderBuffer(streamID)
	if err != nil {
		t.Fatalf("Failed to flush reorder buffer: %v", err)
	}

	// Should get both frames
	expectedData := "frame2frame4"
	if string(flushedData) != expectedData {
		t.Errorf("Expected flushed data '%s', got '%s'", expectedData, string(flushedData))
	}

	// Buffer should be empty now
	stats, err := da.GetReorderBufferStats(streamID)
	if err != nil {
		t.Fatalf("Failed to get reorder buffer stats: %v", err)
	}

	if stats.BufferedFrames != 0 {
		t.Errorf("Expected 0 buffered frames after flush, got %d", stats.BufferedFrames)
	}
}

func TestDataAggregator_ReorderTimeout(t *testing.T) {
	logger := &logger.MockLogger{}
	da := NewDataAggregator(logger).(*DataAggregatorImpl)

	// Configure short timeout for testing
	da.config.MaxReorderDelay = 50 * time.Millisecond

	streamID := uint64(1)
	err := da.CreateAggregatedStream(streamID)
	if err != nil {
		t.Fatalf("Failed to create aggregated stream: %v", err)
	}

	// Add frame with old timestamp
	oldFrame := &DataFrame{
		StreamID:    streamID,
		PathID:      "path1",
		Data:        []byte("old frame"),
		Offset:      10,
		SequenceNum: 1,
		Timestamp:   time.Now().Add(-100 * time.Millisecond), // Old timestamp
	}

	// Process the old frame
	delivered, err := da.ProcessFrameWithReordering(streamID, oldFrame)
	if err != nil {
		t.Fatalf("Failed to process old frame: %v", err)
	}

	// Should be delivered due to timeout
	if string(delivered) != "old frame" {
		t.Errorf("Expected 'old frame' to be delivered due to timeout, got '%s'", string(delivered))
	}
}

func TestDataAggregator_MultiPath_Reordering(t *testing.T) {
	logger := &logger.MockLogger{}
	da := NewDataAggregator(logger).(*DataAggregatorImpl)

	streamID := uint64(1)
	err := da.CreateAggregatedStream(streamID)
	if err != nil {
		t.Fatalf("Failed to create aggregated stream: %v", err)
	}

	// Create frames from different paths
	frames := []*DataFrame{
		{
			StreamID:    streamID,
			PathID:      "path1",
			Data:        []byte("path1-frame1"),
			Offset:      10,
			SequenceNum: 1,
			Timestamp:   time.Now(),
		},
		{
			StreamID:    streamID,
			PathID:      "path2",
			Data:        []byte("path2-frame0"),
			Offset:      0,
			SequenceNum: 0,
			Timestamp:   time.Now(),
		},
		{
			StreamID:    streamID,
			PathID:      "path1",
			Data:        []byte("path1-frame3"),
			Offset:      30,
			SequenceNum: 3,
			Timestamp:   time.Now(),
		},
		{
			StreamID:    streamID,
			PathID:      "path2",
			Data:        []byte("path2-frame2"),
			Offset:      20,
			SequenceNum: 2,
			Timestamp:   time.Now(),
		},
	}

	// Process frames
	var totalDelivered []byte
	for _, frame := range frames {
		delivered, err := da.ProcessFrameWithReordering(streamID, frame)
		if err != nil {
			t.Fatalf("Failed to process frame %d from %s: %v", frame.SequenceNum, frame.PathID, err)
		}
		totalDelivered = append(totalDelivered, delivered...)
	}

	// Should deliver frames in sequence order regardless of path
	expectedData := "path2-frame0path1-frame1path2-frame2path1-frame3"
	if string(totalDelivered) != expectedData {
		t.Errorf("Expected delivered data '%s', got '%s'", expectedData, string(totalDelivered))
	}

	// Check statistics
	stats, err := da.GetAggregationStats(streamID)
	if err != nil {
		t.Fatalf("Failed to get aggregation stats: %v", err)
	}

	if stats.FramesReceived != 4 {
		t.Errorf("Expected 4 frames received, got %d", stats.FramesReceived)
	}

	if stats.BytesPerPath["path1"] == 0 || stats.BytesPerPath["path2"] == 0 {
		t.Errorf("Expected bytes from both paths, got %v", stats.BytesPerPath)
	}
}
