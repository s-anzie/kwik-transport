package presentation

import (
	"sync"
	"testing"
	"time"
)

// TestMultiStreamSeparation tests that multiple streams remain separated
func TestMultiStreamSeparation(t *testing.T) {
	// Create data presentation manager
	config := DefaultPresentationConfig()
	config.ReceiveWindowSize = 8 * 1024 * 1024 // 8MB for testing
	config.CleanupInterval = 0                  // Disable background workers
	config.MetricsUpdateInterval = 0
	
	dpm := NewDataPresentationManager(config)
	defer dpm.Shutdown()
	
	err := dpm.Start()
	if err != nil {
		t.Fatalf("Failed to start data presentation manager: %v", err)
	}
	
	// Create multiple streams with different data
	streamData := map[uint64]string{
		1: "Stream 1: Hello from primary server",
		2: "Stream 2: Data from secondary server A", 
		3: "Stream 3: Information from secondary server B",
		4: "Stream 4: More data from primary server",
	}
	
	// Create stream buffers
	for streamID := range streamData {
		metadata := &StreamMetadata{
			StreamID:   streamID,
			StreamType: StreamTypeData,
			Priority:   StreamPriorityNormal,
			CreatedAt:  time.Now(),
		}
		
		err := dpm.CreateStreamBuffer(streamID, metadata)
		if err != nil {
			t.Fatalf("Failed to create stream buffer %d: %v", streamID, err)
		}
	}
	
	// Write data to each stream
	for streamID, data := range streamData {
		err := dpm.WriteToStream(streamID, []byte(data), 0, nil)
		if err != nil {
			t.Fatalf("Failed to write to stream %d: %v", streamID, err)
		}
	}
	
	// Read from each stream and verify separation
	for streamID, expectedData := range streamData {
		readBuf := make([]byte, 1024)
		n, err := dpm.ReadFromStream(streamID, readBuf)
		if err != nil {
			t.Fatalf("Failed to read from stream %d: %v", streamID, err)
		}
		
		actualData := string(readBuf[:n])
		if actualData != expectedData {
			t.Errorf("Stream %d data mismatch:\nExpected: %q\nActual: %q", 
				streamID, expectedData, actualData)
		}
	}
	
	// Verify that reading from one stream doesn't affect others
	// Read from stream 1 again
	readBuf := make([]byte, 1024)
	n, err := dpm.ReadFromStream(1, readBuf)
	if err == nil && n > 0 {
		t.Error("Expected no more data from stream 1 after first read")
	}
	
	// But stream 2 should still have its data available for re-reading
	// (This tests that streams are truly isolated)
	buffer2, _ := dpm.GetStreamBuffer(2)
	usage := buffer2.GetBufferUsage()
	if usage.ContiguousBytes == 0 {
		t.Error("Stream 2 should still have data available")
	}
}

// TestConcurrentMultiStreamOperations tests concurrent operations on multiple streams
func TestConcurrentMultiStreamOperations(t *testing.T) {
	config := DefaultPresentationConfig()
	config.ReceiveWindowSize = 16 * 1024 * 1024  // 16MB window
	config.DefaultStreamBufferSize = 1024 * 1024 // 1MB per stream
	dpm := NewDataPresentationManager(config)
	defer dpm.Shutdown()
	
	dpm.Start()
	
	const numStreams = 10
	const numOperationsPerStream = 100
	
	var wg sync.WaitGroup
	errors := make(chan error, numStreams*numOperationsPerStream*2)
	
	// Create streams concurrently
	for i := 1; i <= numStreams; i++ {
		wg.Add(1)
		go func(streamID uint64) {
			defer wg.Done()
			
			metadata := &StreamMetadata{
				StreamID:   streamID,
				StreamType: StreamTypeData,
				Priority:   StreamPriorityNormal,
				CreatedAt:  time.Now(),
			}
			
			err := dpm.CreateStreamBuffer(streamID, metadata)
			if err != nil {
				errors <- err
				return
			}
		}(uint64(i))
	}
	
	wg.Wait()
	
	// Concurrent writes to different streams
	for i := 1; i <= numStreams; i++ {
		wg.Add(1)
		go func(streamID uint64) {
			defer wg.Done()
			
			for j := 0; j < numOperationsPerStream; j++ {
				data := []byte("Stream " + string(rune('0'+streamID)) + " data " + string(rune('0'+j)))
				offset := uint64(j * len(data))
				
				err := dpm.WriteToStream(streamID, data, offset, nil)
				if err != nil {
					errors <- err
					return
				}
			}
		}(uint64(i))
	}
	
	// Concurrent reads from different streams
	for i := 1; i <= numStreams; i++ {
		wg.Add(1)
		go func(streamID uint64) {
			defer wg.Done()
			
			time.Sleep(50 * time.Millisecond) // Let some writes happen first
			
			for j := 0; j < numOperationsPerStream/2; j++ {
				readBuf := make([]byte, 1024)
				_, err := dpm.ReadFromStream(streamID, readBuf)
				if err != nil {
					// Some reads might fail if no data is available yet, that's ok
					continue
				}
			}
		}(uint64(i))
	}
	
	wg.Wait()
	close(errors)
	
	// Check for errors
	errorCount := 0
	for err := range errors {
		if err != nil {
			t.Errorf("Concurrent operation failed: %v", err)
			errorCount++
		}
	}
	
	if errorCount > numStreams { // Allow some errors due to timing
		t.Errorf("Too many errors in concurrent operations: %d", errorCount)
	}
}

// TestStreamIsolationWithBackpressure tests stream isolation under backpressure conditions
func TestStreamIsolationWithBackpressure(t *testing.T) {
	config := DefaultPresentationConfig()
	config.DefaultStreamBufferSize = 1024    // Small buffers to trigger backpressure
	config.ReceiveWindowSize = 4096          // Small window
	config.WindowSlideSize = 512             // Must be less than ReceiveWindowSize
	config.BackpressureThreshold = 0.7       // Lower threshold
	
	dpm := NewDataPresentationManager(config)
	defer dpm.Shutdown()
	
	dpm.Start()
	
	// Create multiple streams
	for i := uint64(1); i <= 3; i++ {
		err := dpm.CreateStreamBuffer(i, nil)
		if err != nil {
			t.Fatalf("Failed to create stream buffer %d: %v", i, err)
		}
	}
	
	// Fill stream 1 to trigger backpressure
	largeData := make([]byte, 2048) // Larger than buffer size
	for i := range largeData {
		largeData[i] = byte('A')
	}
	
	err := dpm.WriteToStream(1, largeData, 0, nil)
	if err == nil {
		t.Error("Expected error when writing large data to small buffer")
	}
	
	// Verify stream 1 is under backpressure
	if !dpm.IsBackpressureActive(1) {
		t.Error("Expected stream 1 to be under backpressure")
	}
	
	// Verify other streams are not affected
	smallData := []byte("Small data for stream 2")
	err = dpm.WriteToStream(2, smallData, 0, nil)
	if err != nil {
		t.Errorf("Stream 2 should not be affected by stream 1 backpressure: %v", err)
	}
	
	// Verify we can read from stream 2
	readBuf := make([]byte, 100)
	n, err := dpm.ReadFromStream(2, readBuf)
	if err != nil {
		t.Errorf("Failed to read from stream 2: %v", err)
	}
	
	if string(readBuf[:n]) != string(smallData) {
		t.Errorf("Stream 2 data corrupted: expected %q, got %q", 
			string(smallData), string(readBuf[:n]))
	}
	
	// Verify stream 2 is not under backpressure
	if dpm.IsBackpressureActive(2) {
		t.Error("Stream 2 should not be under backpressure")
	}
}

// TestWindowSharingBetweenStreams tests that streams share the receive window properly
func TestWindowSharingBetweenStreams(t *testing.T) {
	config := DefaultPresentationConfig()
	config.ReceiveWindowSize = 4 * 1024 * 1024      // 4MB window for testing
	config.DefaultStreamBufferSize = 1024 * 1024    // 1MB per stream
	config.WindowSlideSize = 512 * 1024             // 512KB slide size
	
	dpm := NewDataPresentationManager(config)
	defer dpm.Shutdown()
	
	dpm.Start()
	
	// Check initial window status
	status := dpm.GetReceiveWindowStatus()
	initialAvailable := status.AvailableSize
	
	// Create first stream (should allocate window space)
	err := dpm.CreateStreamBuffer(1, nil)
	if err != nil {
		t.Fatalf("Failed to create stream buffer 1: %v", err)
	}
	
	status = dpm.GetReceiveWindowStatus()
	afterFirstStream := status.AvailableSize
	
	if afterFirstStream >= initialAvailable {
		t.Error("Expected available window to decrease after creating first stream")
	}
	
	// Create second stream (should allocate more window space)
	err = dpm.CreateStreamBuffer(2, nil)
	if err != nil {
		t.Fatalf("Failed to create stream buffer 2: %v", err)
	}
	
	status = dpm.GetReceiveWindowStatus()
	afterSecondStream := status.AvailableSize
	
	if afterSecondStream >= afterFirstStream {
		t.Error("Expected available window to decrease after creating second stream")
	}
	
	// Verify both streams have allocations
	if len(status.StreamAllocations) != 2 {
		t.Errorf("Expected 2 stream allocations, got %d", len(status.StreamAllocations))
	}
	
	// Remove first stream (should free window space)
	err = dpm.RemoveStreamBuffer(1)
	if err != nil {
		t.Fatalf("Failed to remove stream buffer 1: %v", err)
	}
	
	status = dpm.GetReceiveWindowStatus()
	afterRemoval := status.AvailableSize
	
	if afterRemoval <= afterSecondStream {
		t.Error("Expected available window to increase after removing stream")
	}
	
	// Verify only one stream allocation remains
	if len(status.StreamAllocations) != 1 {
		t.Errorf("Expected 1 stream allocation after removal, got %d", len(status.StreamAllocations))
	}
}

// TestDataIntegrityAcrossStreams tests that data integrity is maintained across multiple streams
func TestDataIntegrityAcrossStreams(t *testing.T) {
	dpm := NewDataPresentationManager(nil)
	defer dpm.Shutdown()
	
	dpm.Start()
	
	// Create streams with different priorities
	priorities := []StreamPriority{
		StreamPriorityLow,
		StreamPriorityNormal, 
		StreamPriorityHigh,
		StreamPriorityCritical,
	}
	
	for i, priority := range priorities {
		streamID := uint64(i + 1)
		metadata := &StreamMetadata{
			StreamID:   streamID,
			StreamType: StreamTypeData,
			Priority:   priority,
			CreatedAt:  time.Now(),
		}
		
		err := dpm.CreateStreamBuffer(streamID, metadata)
		if err != nil {
			t.Fatalf("Failed to create stream buffer %d: %v", streamID, err)
		}
	}
	
	// Write structured data to each stream
	testData := make(map[uint64][]string)
	for i := uint64(1); i <= 4; i++ {
		testData[i] = []string{
			"Packet 1 for stream " + string(rune('0'+i)),
			"Packet 2 for stream " + string(rune('0'+i)),
			"Packet 3 for stream " + string(rune('0'+i)),
		}
		
		// Write packets with proper offsets
		offset := uint64(0)
		for _, packet := range testData[i] {
			err := dpm.WriteToStream(i, []byte(packet), offset, nil)
			if err != nil {
				t.Fatalf("Failed to write packet to stream %d: %v", i, err)
			}
			offset += uint64(len(packet))
		}
	}
	
	// Read back data and verify integrity
	for streamID, expectedPackets := range testData {
		var receivedData []byte
		
		// Read all data from the stream
		for {
			readBuf := make([]byte, 1024)
			n, err := dpm.ReadFromStream(streamID, readBuf)
			if err != nil || n == 0 {
				break
			}
			receivedData = append(receivedData, readBuf[:n]...)
		}
		
		// Reconstruct expected data
		var expectedData []byte
		for _, packet := range expectedPackets {
			expectedData = append(expectedData, []byte(packet)...)
		}
		
		if string(receivedData) != string(expectedData) {
			t.Errorf("Data integrity failed for stream %d:\nExpected: %q\nReceived: %q",
				streamID, string(expectedData), string(receivedData))
		}
	}
}

// TestStreamLifecycleManagement tests complete stream lifecycle
func TestStreamLifecycleManagement(t *testing.T) {
	config := DefaultPresentationConfig()
	config.ReceiveWindowSize = 8 * 1024 * 1024      // 8MB window to fit 5 streams
	config.DefaultStreamBufferSize = 1024 * 1024    // 1MB per stream
	
	dpm := NewDataPresentationManager(config)
	defer dpm.Shutdown()
	
	dpm.Start()
	
	const numStreams = 5
	
	// Phase 1: Create streams
	for i := uint64(1); i <= numStreams; i++ {
		metadata := &StreamMetadata{
			StreamID:   i,
			StreamType: StreamTypeData,
			Priority:   StreamPriorityNormal,
			CreatedAt:  time.Now(),
		}
		
		err := dpm.CreateStreamBuffer(i, metadata)
		if err != nil {
			t.Fatalf("Failed to create stream %d: %v", i, err)
		}
	}
	
	// Verify all streams exist
	stats := dpm.GetGlobalStats()
	if stats.ActiveStreams != numStreams {
		t.Errorf("Expected %d active streams, got %d", numStreams, stats.ActiveStreams)
	}
	
	// Phase 2: Use streams (write and read data)
	for i := uint64(1); i <= numStreams; i++ {
		data := []byte("Lifecycle test data for stream " + string(rune('0'+i)))
		
		err := dpm.WriteToStream(i, data, 0, nil)
		if err != nil {
			t.Fatalf("Failed to write to stream %d: %v", i, err)
		}
		
		readBuf := make([]byte, 1024)
		n, err := dpm.ReadFromStream(i, readBuf)
		if err != nil {
			t.Fatalf("Failed to read from stream %d: %v", i, err)
		}
		
		if string(readBuf[:n]) != string(data) {
			t.Errorf("Data mismatch for stream %d", i)
		}
	}
	
	// Phase 3: Remove some streams
	streamsToRemove := []uint64{2, 4}
	for _, streamID := range streamsToRemove {
		err := dpm.RemoveStreamBuffer(streamID)
		if err != nil {
			t.Fatalf("Failed to remove stream %d: %v", streamID, err)
		}
	}
	
	// Verify remaining streams
	stats = dpm.GetGlobalStats()
	expectedRemaining := numStreams - len(streamsToRemove)
	if stats.ActiveStreams != expectedRemaining {
		t.Errorf("Expected %d active streams after removal, got %d", 
			expectedRemaining, stats.ActiveStreams)
	}
	
	// Verify removed streams are inaccessible
	for _, streamID := range streamsToRemove {
		_, err := dpm.GetStreamBuffer(streamID)
		if err == nil {
			t.Errorf("Expected error when accessing removed stream %d", streamID)
		}
	}
	
	// Verify remaining streams still work
	remainingStreams := []uint64{1, 3, 5}
	for _, streamID := range remainingStreams {
		buffer, err := dpm.GetStreamBuffer(streamID)
		if err != nil {
			t.Errorf("Failed to access remaining stream %d: %v", streamID, err)
		}
		
		if buffer == nil {
			t.Errorf("Expected non-nil buffer for remaining stream %d", streamID)
		}
	}
}

// TestPerformanceUnderLoad tests system performance with multiple active streams
func TestPerformanceUnderLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}
	
	config := DefaultPresentationConfig()
	config.ReceiveWindowSize = 64 * 1024 * 1024     // 64MB for performance testing
	config.DefaultStreamBufferSize = 1024 * 1024    // 1MB per stream
	
	dpm := NewDataPresentationManager(config)
	defer dpm.Shutdown()
	
	dpm.Start()
	
	const numStreams = 50
	const dataSize = 1024
	const operationsPerStream = 100
	
	// Create streams
	for i := uint64(1); i <= numStreams; i++ {
		err := dpm.CreateStreamBuffer(i, nil)
		if err != nil {
			t.Fatalf("Failed to create stream %d: %v", i, err)
		}
	}
	
	startTime := time.Now()
	
	var wg sync.WaitGroup
	
	// Concurrent load test
	for i := uint64(1); i <= numStreams; i++ {
		wg.Add(1)
		go func(streamID uint64) {
			defer wg.Done()
			
			data := make([]byte, dataSize)
			for j := range data {
				data[j] = byte('A' + (j % 26))
			}
			
			// Write operations
			for j := 0; j < operationsPerStream; j++ {
				offset := uint64(j * dataSize)
				err := dpm.WriteToStream(streamID, data, offset, nil)
				if err != nil {
					t.Errorf("Write failed for stream %d operation %d: %v", streamID, j, err)
					return
				}
			}
			
			// Read operations
			readBuf := make([]byte, dataSize)
			for j := 0; j < operationsPerStream; j++ {
				_, err := dpm.ReadFromStream(streamID, readBuf)
				if err != nil {
					// Some reads might fail if data isn't available yet
					continue
				}
			}
		}(i)
	}
	
	wg.Wait()
	
	duration := time.Since(startTime)
	totalOperations := numStreams * operationsPerStream * 2 // writes + reads
	operationsPerSecond := float64(totalOperations) / duration.Seconds()
	
	t.Logf("Performance test completed:")
	t.Logf("  Streams: %d", numStreams)
	t.Logf("  Operations per stream: %d", operationsPerStream*2)
	t.Logf("  Total operations: %d", totalOperations)
	t.Logf("  Duration: %v", duration)
	t.Logf("  Operations per second: %.2f", operationsPerSecond)
	
	// Basic performance expectations
	if operationsPerSecond < 1000 {
		t.Errorf("Performance too low: %.2f ops/sec (expected > 1000)", operationsPerSecond)
	}
	
	// Verify system state after load test
	stats := dpm.GetGlobalStats()
	if stats.ActiveStreams != numStreams {
		t.Errorf("Expected %d active streams after load test, got %d", 
			numStreams, stats.ActiveStreams)
	}
	
	if stats.ErrorCount > uint64(totalOperations/10) { // Allow up to 10% errors
		t.Errorf("Too many errors during load test: %d", stats.ErrorCount)
	}
}