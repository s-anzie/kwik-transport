package data

import (
	"testing"
	"time"

	datapb "kwik/proto/data"
)

func TestDataReorderingManager_ProcessInOrderFrames(t *testing.T) {
	offsetManager := NewOffsetManager(nil)
	drm := NewDataReorderingManager(offsetManager, nil)
	defer drm.Close()

	logicalStreamID := uint64(1)

	// Register logical stream
	err := offsetManager.RegisterLogicalStream(logicalStreamID)
	if err != nil {
		t.Fatalf("Failed to register logical stream: %v", err)
	}

	// Create in-order frames
	frames := []*datapb.DataFrame{
		{
			FrameId:         1,
			LogicalStreamId: logicalStreamID,
			Offset:          0,
			Data:            []byte("Hello "),
			Fin:             false,
			Timestamp:       uint64(time.Now().UnixNano()),
			PathId:          "path1",
			DataLength:      6,
		},
		{
			FrameId:         2,
			LogicalStreamId: logicalStreamID,
			Offset:          6,
			Data:            []byte("World!"),
			Fin:             true,
			Timestamp:       uint64(time.Now().UnixNano()),
			PathId:          "path1",
			DataLength:      6,
		},
	}

	// Process frames in order
	for _, frame := range frames {
		err := drm.ProcessIncomingFrame(frame)
		if err != nil {
			t.Fatalf("Failed to process frame: %v", err)
		}
	}

	// Reassemble data
	data, err := drm.ReassembleStreamData(logicalStreamID)
	if err != nil {
		t.Fatalf("Failed to reassemble stream data: %v", err)
	}

	expected := "Hello World!"
	if string(data) != expected {
		t.Errorf("Expected reassembled data %q, got %q", expected, string(data))
	}
}

func TestDataReorderingManager_ProcessOutOfOrderFrames(t *testing.T) {
	offsetManager := NewOffsetManager(nil)
	drm := NewDataReorderingManager(offsetManager, nil)
	defer drm.Close()

	logicalStreamID := uint64(1)

	// Register logical stream
	err := offsetManager.RegisterLogicalStream(logicalStreamID)
	if err != nil {
		t.Fatalf("Failed to register logical stream: %v", err)
	}

	// Create out-of-order frames
	frames := []*datapb.DataFrame{
		{
			FrameId:         2,
			LogicalStreamId: logicalStreamID,
			Offset:          6,
			Data:            []byte("World!"),
			Fin:             true,
			Timestamp:       uint64(time.Now().UnixNano()),
			PathId:          "path2",
			DataLength:      6,
		},
		{
			FrameId:         1,
			LogicalStreamId: logicalStreamID,
			Offset:          0,
			Data:            []byte("Hello "),
			Fin:             false,
			Timestamp:       uint64(time.Now().UnixNano()),
			PathId:          "path1",
			DataLength:      6,
		},
	}

	// Process frames out of order
	for _, frame := range frames {
		err := drm.ProcessIncomingFrame(frame)
		if err != nil {
			t.Fatalf("Failed to process frame: %v", err)
		}
	}

	// Give some time for background processing
	time.Sleep(100 * time.Millisecond)

	// Reassemble data
	data, err := drm.ReassembleStreamData(logicalStreamID)
	if err != nil {
		t.Fatalf("Failed to reassemble stream data: %v", err)
	}

	expected := "Hello World!"
	if string(data) != expected {
		t.Errorf("Expected reassembled data %q, got %q", expected, string(data))
	}

	// Check statistics
	stats := drm.GetReorderingStats()
	if stats.TotalFramesProcessed != 2 {
		t.Errorf("Expected 2 frames processed, got %d", stats.TotalFramesProcessed)
	}

	if stats.FramesReordered == 0 {
		t.Error("Expected some frames to be reordered")
	}
}

func TestDataReorderingManager_MultiplePathsReordering(t *testing.T) {
	offsetManager := NewOffsetManager(nil)
	drm := NewDataReorderingManager(offsetManager, nil)
	defer drm.Close()

	logicalStreamID := uint64(1)

	// Register logical stream
	err := offsetManager.RegisterLogicalStream(logicalStreamID)
	if err != nil {
		t.Fatalf("Failed to register logical stream: %v", err)
	}

	// Create frames from multiple paths arriving out of order
	frames := []*datapb.DataFrame{
		{
			FrameId:         3,
			LogicalStreamId: logicalStreamID,
			Offset:          11,
			Data:            []byte(" from"),
			Fin:             false,
			Timestamp:       uint64(time.Now().UnixNano()),
			PathId:          "path3",
			DataLength:      5,
		},
		{
			FrameId:         1,
			LogicalStreamId: logicalStreamID,
			Offset:          0,
			Data:            []byte("Hello"),
			Fin:             false,
			Timestamp:       uint64(time.Now().UnixNano()),
			PathId:          "path1",
			DataLength:      5,
		},
		{
			FrameId:         4,
			LogicalStreamId: logicalStreamID,
			Offset:          16,
			Data:            []byte(" multiple paths!"),
			Fin:             true,
			Timestamp:       uint64(time.Now().UnixNano()),
			PathId:          "path4",
			DataLength:      16,
		},
		{
			FrameId:         2,
			LogicalStreamId: logicalStreamID,
			Offset:          5,
			Data:            []byte(" World"),
			Fin:             false,
			Timestamp:       uint64(time.Now().UnixNano()),
			PathId:          "path2",
			DataLength:      6,
		},
	}

	// Process frames in random order
	for _, frame := range frames {
		err := drm.ProcessIncomingFrame(frame)
		if err != nil {
			t.Fatalf("Failed to process frame: %v", err)
		}
	}

	// Give time for background processing and retry until complete
	maxRetries := 10
	var data []byte
	var reassembleErr error
	
	for i := 0; i < maxRetries; i++ {
		time.Sleep(50 * time.Millisecond)
		
		// Get debug info about the stream state
		context, exists := drm.streamContexts[logicalStreamID]
		if exists {
			context.reassemblyState.mutex.RLock()
			finalReceived := context.reassemblyState.finalFrameReceived
			gapCount := len(context.reassemblyState.gaps)
			nextOffset := context.reassemblyState.nextOffset
			dataLen := len(context.reassemblyState.reassembledData)
			context.reassemblyState.mutex.RUnlock()
			
			bufferedCount := context.frameBuffer.GetBufferedFrameCount()
			
			t.Logf("Retry %d: finalReceived=%v, gaps=%d, nextOffset=%d, dataLen=%d, buffered=%d", 
				i+1, finalReceived, gapCount, nextOffset, dataLen, bufferedCount)
		}
		
		data, reassembleErr = drm.ReassembleStreamData(logicalStreamID)
		if reassembleErr == nil {
			break
		}
		// If it's not complete yet, continue waiting
		if i == maxRetries-1 {
			t.Fatalf("Failed to reassemble stream data after %d retries: %v", maxRetries, reassembleErr)
		}
	}

	expected := "Hello World from multiple paths!"
	if string(data) != expected {
		t.Errorf("Expected reassembled data %q, got %q", expected, string(data))
	}

	// Check path contributions
	stats := drm.GetReorderingStats()
	if len(stats.PathContributions) != 4 {
		t.Errorf("Expected contributions from 4 paths, got %d", len(stats.PathContributions))
	}

	// Verify each path contributed
	expectedPaths := []string{"path1", "path2", "path3", "path4"}
	for _, pathID := range expectedPaths {
		if contribution, exists := stats.PathContributions[pathID]; !exists {
			t.Errorf("Expected contribution from %s", pathID)
		} else if contribution.FramesReceived != 1 {
			t.Errorf("Expected 1 frame from %s, got %d", pathID, contribution.FramesReceived)
		}
	}
}

func TestFrameBuffer_AddAndRetrieveFrames(t *testing.T) {
	buffer := NewFrameBuffer(10)

	// Create test frames with different offsets
	frames := []*BufferedFrame{
		{
			Frame: &datapb.DataFrame{
				Offset: 10,
				Data:   []byte("frame2"),
			},
			ReceivedAt: time.Now(),
			PathID:     "path1",
			Priority:   1,
		},
		{
			Frame: &datapb.DataFrame{
				Offset: 0,
				Data:   []byte("frame1"),
			},
			ReceivedAt: time.Now(),
			PathID:     "path1",
			Priority:   2,
		},
		{
			Frame: &datapb.DataFrame{
				Offset: 20,
				Data:   []byte("frame3"),
			},
			ReceivedAt: time.Now(),
			PathID:     "path1",
			Priority:   1,
		},
	}

	// Add frames to buffer
	for _, frame := range frames {
		err := buffer.AddFrame(frame)
		if err != nil {
			t.Fatalf("Failed to add frame: %v", err)
		}
	}

	// Retrieve frames in order
	frame1 := buffer.GetNextInOrderFrame(0)
	if frame1 == nil {
		t.Fatal("Expected to get frame with offset 0")
	}
	if string(frame1.Frame.Data) != "frame1" {
		t.Errorf("Expected frame1, got %s", string(frame1.Frame.Data))
	}

	frame2 := buffer.GetNextInOrderFrame(10)
	if frame2 == nil {
		t.Fatal("Expected to get frame with offset 10")
	}
	if string(frame2.Frame.Data) != "frame2" {
		t.Errorf("Expected frame2, got %s", string(frame2.Frame.Data))
	}

	// Try to get frame that doesn't exist
	frame4 := buffer.GetNextInOrderFrame(5)
	if frame4 != nil {
		t.Error("Expected nil for non-existent frame")
	}

	// Check remaining frame count
	if buffer.GetBufferedFrameCount() != 1 {
		t.Errorf("Expected 1 remaining frame, got %d", buffer.GetBufferedFrameCount())
	}
}

func TestReassemblyState_GapHandling(t *testing.T) {
	state := NewReassemblyState(1024)

	// Add some gaps
	state.AddGap(10, 20)
	state.AddGap(30, 40)
	state.AddGap(5, 8)

	if state.GetGapCount() != 3 {
		t.Errorf("Expected 3 gaps, got %d", state.GetGapCount())
	}

	// Fill a gap completely
	filled := state.FillGap(10, make([]byte, 10))
	if !filled {
		t.Error("Expected gap to be filled")
	}

	if state.GetGapCount() != 2 {
		t.Errorf("Expected 2 gaps after filling one, got %d", state.GetGapCount())
	}

	// Fill a gap partially
	filled = state.FillGap(30, make([]byte, 5))
	if !filled {
		t.Error("Expected gap to be partially filled")
	}

	if state.GetGapCount() != 2 {
		t.Errorf("Expected 2 gaps after partial fill, got %d", state.GetGapCount())
	}

	// Split a gap
	filled = state.FillGap(6, make([]byte, 1))
	if !filled {
		t.Error("Expected gap to be split")
	}

	if state.GetGapCount() != 3 {
		t.Errorf("Expected 3 gaps after splitting, got %d", state.GetGapCount())
	}
}

func TestPriorityFrameBuffer_PriorityOrdering(t *testing.T) {
	buffer := NewPriorityFrameBuffer(10)

	// Create frames with different priorities
	frames := []*BufferedFrame{
		{
			Frame: &datapb.DataFrame{
				Offset: 10,
				Data:   []byte("low priority"),
			},
			Priority: 1,
		},
		{
			Frame: &datapb.DataFrame{
				Offset: 0,
				Data:   []byte("high priority"),
			},
			Priority: 10,
		},
		{
			Frame: &datapb.DataFrame{
				Offset: 5,
				Data:   []byte("medium priority"),
			},
			Priority: 5,
		},
	}

	// Add frames to buffer
	for _, frame := range frames {
		err := buffer.AddFrame(frame)
		if err != nil {
			t.Fatalf("Failed to add frame: %v", err)
		}
	}

	// Retrieve frames in priority order
	frame1 := buffer.GetHighestPriorityFrame()
	if frame1 == nil {
		t.Fatal("Expected to get highest priority frame")
	}
	if string(frame1.Frame.Data) != "high priority" {
		t.Errorf("Expected high priority frame, got %s", string(frame1.Frame.Data))
	}

	frame2 := buffer.GetHighestPriorityFrame()
	if frame2 == nil {
		t.Fatal("Expected to get medium priority frame")
	}
	if string(frame2.Frame.Data) != "medium priority" {
		t.Errorf("Expected medium priority frame, got %s", string(frame2.Frame.Data))
	}

	frame3 := buffer.GetHighestPriorityFrame()
	if frame3 == nil {
		t.Fatal("Expected to get low priority frame")
	}
	if string(frame3.Frame.Data) != "low priority" {
		t.Errorf("Expected low priority frame, got %s", string(frame3.Frame.Data))
	}

	// Buffer should be empty now
	if buffer.GetFrameCount() != 0 {
		t.Errorf("Expected empty buffer, got %d frames", buffer.GetFrameCount())
	}
}

func TestStreamReassembler_GetStreamProgress(t *testing.T) {
	offsetManager := NewOffsetManager(nil)
	drm := NewDataReorderingManager(offsetManager, nil)
	defer drm.Close()

	reassembler := NewStreamReassembler(drm, nil)
	logicalStreamID := uint64(1)

	// Register logical stream
	err := offsetManager.RegisterLogicalStream(logicalStreamID)
	if err != nil {
		t.Fatalf("Failed to register logical stream: %v", err)
	}

	// Process partial data
	frame := &datapb.DataFrame{
		FrameId:         1,
		LogicalStreamId: logicalStreamID,
		Offset:          0,
		Data:            []byte("Hello"),
		Fin:             false,
		Timestamp:       uint64(time.Now().UnixNano()),
		PathId:          "path1",
		DataLength:      5,
	}

	err = drm.ProcessIncomingFrame(frame)
	if err != nil {
		t.Fatalf("Failed to process frame: %v", err)
	}

	// Process final frame to set total expected bytes
	finalFrame := &datapb.DataFrame{
		FrameId:         2,
		LogicalStreamId: logicalStreamID,
		Offset:          5,
		Data:            []byte(" World!"),
		Fin:             true,
		Timestamp:       uint64(time.Now().UnixNano()),
		PathId:          "path1",
		DataLength:      7,
	}

	err = drm.ProcessIncomingFrame(finalFrame)
	if err != nil {
		t.Fatalf("Failed to process final frame: %v", err)
	}

	// Check progress
	progress, err := reassembler.GetStreamProgress(logicalStreamID)
	if err != nil {
		t.Fatalf("Failed to get stream progress: %v", err)
	}

	if progress != 100.0 {
		t.Errorf("Expected 100%% progress, got %.2f%%", progress)
	}

	// Check active streams
	activeStreams := reassembler.GetActiveStreams()
	if len(activeStreams) != 1 {
		t.Errorf("Expected 1 active stream, got %d", len(activeStreams))
	}

	if activeStreams[0] != logicalStreamID {
		t.Errorf("Expected stream ID %d, got %d", logicalStreamID, activeStreams[0])
	}
}