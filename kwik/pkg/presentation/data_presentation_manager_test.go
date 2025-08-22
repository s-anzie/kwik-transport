package presentation

import (
	"sync"
	"testing"
	"time"
)

func TestDataPresentationManager_BasicOperations(t *testing.T) {
	config := DefaultPresentationConfig()
	config.CleanupInterval = 0       // Disable cleanup worker for testing
	config.MetricsUpdateInterval = 0 // Disable metrics worker for testing

	dpm := NewDataPresentationManager(config)
	defer dpm.Shutdown()

	err := dpm.Start()
	if err != nil {
		t.Fatalf("Failed to start data presentation manager: %v", err)
	}

	// Test creating a stream buffer
	metadata := &StreamMetadata{
		StreamID:   1,
		StreamType: StreamTypeData,
		Priority:   StreamPriorityNormal,
		CreatedAt:  time.Now(),
	}

	err = dpm.CreateStreamBuffer(1, metadata)
	if err != nil {
		t.Fatalf("Failed to create stream buffer: %v", err)
	}

	// Test getting the stream buffer
	buffer, err := dpm.GetStreamBuffer(1)
	if err != nil {
		t.Fatalf("Failed to get stream buffer: %v", err)
	}

	if buffer == nil {
		t.Error("Expected non-nil stream buffer")
	}

	// Test removing the stream buffer
	err = dpm.RemoveStreamBuffer(1)
	if err != nil {
		t.Fatalf("Failed to remove stream buffer: %v", err)
	}

	// Getting the buffer should fail now
	_, err = dpm.GetStreamBuffer(1)
	if err == nil {
		t.Error("Expected error when getting removed stream buffer")
	}
}

func TestDataPresentationManager_StreamDataFlow(t *testing.T) {
	dpm := NewDataPresentationManager(nil)
	defer dpm.Shutdown()

	dpm.Start()

	// Create stream buffer
	metadata := &StreamMetadata{
		StreamID:   1,
		StreamType: StreamTypeData,
		Priority:   StreamPriorityNormal,
		CreatedAt:  time.Now(),
	}

	err := dpm.CreateStreamBuffer(1, metadata)
	if err != nil {
		t.Fatalf("Failed to create stream buffer: %v", err)
	}

	// Write data to stream
	data := []byte("Hello, World!")
	dataMetadata := &DataMetadata{
		Offset:    0,
		Length:    uint64(len(data)),
		Timestamp: time.Now(),
	}

	err = dpm.WriteToStream(1, data, 0, dataMetadata)
	if err != nil {
		t.Fatalf("Failed to write to stream: %v", err)
	}

	// Read data from stream
	readBuf := make([]byte, 100)
	n, err := dpm.ReadFromStream(1, readBuf)
	if err != nil {
		t.Fatalf("Failed to read from stream: %v", err)
	}

	if n != len(data) {
		t.Errorf("Expected to read %d bytes, got %d", len(data), n)
	}

	if string(readBuf[:n]) != string(data) {
		t.Errorf("Expected to read %q, got %q", string(data), string(readBuf[:n]))
	}
}

func TestDataPresentationManager_MultipleStreams(t *testing.T) {
	dpm := NewDataPresentationManager(nil)
	defer dpm.Shutdown()

	dpm.Start()

	// Create multiple stream buffers
	for i := uint64(1); i <= 3; i++ {
		metadata := &StreamMetadata{
			StreamID:   i,
			StreamType: StreamTypeData,
			Priority:   StreamPriorityNormal,
			CreatedAt:  time.Now(),
		}

		err := dpm.CreateStreamBuffer(i, metadata)
		if err != nil {
			t.Fatalf("Failed to create stream buffer %d: %v", i, err)
		}
	}

	// Write different data to each stream
	for i := uint64(1); i <= 3; i++ {
		data := []byte("Stream " + string(rune('0'+i)) + " data")
		err := dpm.WriteToStream(i, data, 0, nil)
		if err != nil {
			t.Fatalf("Failed to write to stream %d: %v", i, err)
		}
	}

	// Read from each stream and verify separation
	for i := uint64(1); i <= 3; i++ {
		readBuf := make([]byte, 100)
		n, err := dpm.ReadFromStream(i, readBuf)
		if err != nil {
			t.Fatalf("Failed to read from stream %d: %v", i, err)
		}

		expected := "Stream " + string(rune('0'+i)) + " data"
		if string(readBuf[:n]) != expected {
			t.Errorf("Stream %d: expected %q, got %q", i, expected, string(readBuf[:n]))
		}
	}
}

func TestDataPresentationManager_BackpressureIntegration(t *testing.T) {
	config := DefaultPresentationConfig()
	config.DefaultStreamBufferSize = 50 // Small buffer to trigger backpressure

	dpm := NewDataPresentationManager(config)
	defer dpm.Shutdown()

	dpm.Start()

	// Create stream buffer
	err := dpm.CreateStreamBuffer(1, nil)
	if err != nil {
		t.Fatalf("Failed to create stream buffer: %v", err)
	}

	// Fill buffer to trigger backpressure
	largeData := make([]byte, 100) // Larger than buffer size
	for i := range largeData {
		largeData[i] = byte('A' + (i % 26))
	}

	err = dpm.WriteToStream(1, largeData, 0, nil)
	if err == nil {
		t.Error("Expected error when writing data larger than buffer")
	}

	// Check that backpressure is active
	if !dpm.IsBackpressureActive(1) {
		t.Error("Expected backpressure to be active after buffer overflow")
	}
}

func TestDataPresentationManager_WindowManagement(t *testing.T) {
	config := DefaultPresentationConfig()
	config.ReceiveWindowSize = 1024
	config.WindowSlideSize = 256         // Must be less than ReceiveWindowSize
	config.DefaultStreamBufferSize = 512 // Must fit within ReceiveWindowSize

	dpm := NewDataPresentationManager(config)
	defer dpm.Shutdown()

	dpm.Start()

	// Check initial window status
	status := dpm.GetReceiveWindowStatus()
	if status.TotalSize != 1024 {
		t.Errorf("Expected window size 1024, got %d", status.TotalSize)
	}

	if status.UsedSize != 0 {
		t.Errorf("Expected used size 0, got %d", status.UsedSize)
	}

	// Create stream buffer (should allocate window space)
	err := dpm.CreateStreamBuffer(1, nil)
	if err != nil {
		t.Fatalf("Failed to create stream buffer: %v", err)
	}

	// Check that window space was allocated
	status = dpm.GetReceiveWindowStatus()
	if status.UsedSize == 0 {
		t.Error("Expected window space to be allocated after creating stream")
	}

	// Test changing window size
	err = dpm.SetReceiveWindowSize(2048)
	if err != nil {
		t.Fatalf("Failed to set window size: %v", err)
	}

	status = dpm.GetReceiveWindowStatus()
	if status.TotalSize != 2048 {
		t.Errorf("Expected window size 2048, got %d", status.TotalSize)
	}
}

func TestDataPresentationManager_ReadWithTimeout(t *testing.T) {
	dpm := NewDataPresentationManager(nil)
	defer dpm.Shutdown()

	dpm.Start()

	// Create stream buffer
	err := dpm.CreateStreamBuffer(1, nil)
	if err != nil {
		t.Fatalf("Failed to create stream buffer: %v", err)
	}

	// Try to read from empty stream (should return 0 bytes, not timeout)
	readBuf := make([]byte, 100)
	dpm.SetStreamTimeout(1, 50*time.Millisecond) // Set short timeout for testing
	n, err := dpm.ReadFromStream(1, readBuf)
	if err != nil {
		t.Errorf("Unexpected error when reading from empty stream: %v", err)
	}
	if n != 0 {
		t.Errorf("Expected 0 bytes from empty stream, got %d", n)
	}

	// Write data and then read it
	data := []byte("Delayed data")
	err = dpm.WriteToStream(1, data, 0, nil)
	if err != nil {
		t.Fatalf("Failed to write data: %v", err)
	}
	dpm.SetStreamTimeout(1, 100*time.Millisecond) // Set short timeout for testing
	n, err = dpm.ReadFromStream(1, readBuf)
	if err != nil {
		t.Fatalf("Failed to read with timeout: %v", err)
	}

	expected := "Delayed data"
	if string(readBuf[:n]) != expected {
		t.Errorf("Expected %q, got %q", expected, string(readBuf[:n]))
	}
}

func TestDataPresentationManager_BatchOperations(t *testing.T) {
	dpm := NewDataPresentationManager(nil)
	defer dpm.Shutdown()

	dpm.Start()

	// Create stream buffers
	for i := uint64(1); i <= 3; i++ {
		err := dpm.CreateStreamBuffer(i, nil)
		if err != nil {
			t.Fatalf("Failed to create stream buffer %d: %v", i, err)
		}
	}

	// Prepare batch writes
	writes := []StreamWrite{
		{StreamID: 1, Data: []byte("Data for stream 1"), Offset: 0},
		{StreamID: 2, Data: []byte("Data for stream 2"), Offset: 0},
		{StreamID: 3, Data: []byte("Data for stream 3"), Offset: 0},
	}

	err := dpm.WriteToStreamBatch(writes)
	if err != nil {
		t.Fatalf("Failed to write batch: %v", err)
	}

	// Verify data was written to each stream
	for i := uint64(1); i <= 3; i++ {
		readBuf := make([]byte, 100)
		n, err := dpm.ReadFromStream(i, readBuf)
		if err != nil {
			t.Fatalf("Failed to read from stream %d: %v", i, err)
		}

		expected := "Data for stream " + string(rune('0'+i))
		if string(readBuf[:n]) != expected {
			t.Errorf("Stream %d: expected %q, got %q", i, expected, string(readBuf[:n]))
		}
	}
}

func TestDataPresentationManager_RoutingOperations(t *testing.T) {
	dpm := NewDataPresentationManager(nil)
	defer dpm.Shutdown()

	dpm.Start()

	// Test routing data to non-existent stream (should create it)
	routingInfo := &DataRoutingInfo{
		StreamID:   1,
		Data:       []byte("Routed data"),
		Offset:     0,
		Priority:   StreamPriorityNormal,
		SourcePath: "test-path",
		Flags:      DataFlagNone,
	}

	err := dpm.RouteDataToStream(routingInfo)
	if err != nil {
		t.Fatalf("Failed to route data to stream: %v", err)
	}

	// Verify stream was created and data was written
	readBuf := make([]byte, 100)
	n, err := dpm.ReadFromStream(1, readBuf)
	if err != nil {
		t.Fatalf("Failed to read from routed stream: %v", err)
	}

	if string(readBuf[:n]) != "Routed data" {
		t.Errorf("Expected 'Routed data', got %q", string(readBuf[:n]))
	}

	// Test batch routing
	routingInfos := []*DataRoutingInfo{
		{StreamID: 2, Data: []byte("Batch data 1"), Offset: 0, Priority: StreamPriorityNormal},
		{StreamID: 3, Data: []byte("Batch data 2"), Offset: 0, Priority: StreamPriorityNormal},
	}

	err = dpm.RouteDataBatch(routingInfos)
	if err != nil {
		t.Fatalf("Failed to route data batch: %v", err)
	}

	// Verify batch routing worked
	for i := uint64(2); i <= 3; i++ {
		readBuf := make([]byte, 100)
		n, err := dpm.ReadFromStream(i, readBuf)
		if err != nil {
			t.Fatalf("Failed to read from batch routed stream %d: %v", i, err)
		}

		expected := "Batch data " + string(rune('0'+i-1))
		if string(readBuf[:n]) != expected {
			t.Errorf("Stream %d: expected %q, got %q", i, expected, string(readBuf[:n]))
		}
	}
}

func TestDataPresentationManager_Statistics(t *testing.T) {
	dpm := NewDataPresentationManager(nil)
	defer dpm.Shutdown()

	dpm.Start()

	// Create stream and perform operations
	err := dpm.CreateStreamBuffer(1, nil)
	if err != nil {
		t.Fatalf("Failed to create stream buffer: %v", err)
	}

	data := []byte("Test data")
	err = dpm.WriteToStream(1, data, 0, nil)
	if err != nil {
		t.Fatalf("Failed to write to stream: %v", err)
	}

	readBuf := make([]byte, 100)
	_, err = dpm.ReadFromStream(1, readBuf)
	if err != nil {
		t.Fatalf("Failed to read from stream: %v", err)
	}

	// Check global statistics
	stats := dpm.GetGlobalStats()
	if stats.ActiveStreams != 1 {
		t.Errorf("Expected 1 active stream, got %d", stats.ActiveStreams)
	}

	if stats.TotalBytesWritten < uint64(len(data)) {
		t.Errorf("Expected at least %d bytes written, got %d", len(data), stats.TotalBytesWritten)
	}

	if stats.TotalBytesRead < uint64(len(data)) {
		t.Errorf("Expected at least %d bytes read, got %d", len(data), stats.TotalBytesRead)
	}

	// Check stream-specific statistics
	streamStats := dpm.GetStreamStats(1)
	if streamStats == nil {
		t.Error("Expected non-nil stream statistics")
	} else {
		if streamStats.BytesWritten < uint64(len(data)) {
			t.Errorf("Expected at least %d bytes written to stream, got %d",
				len(data), streamStats.BytesWritten)
		}
	}
}

func TestDataPresentationManager_ConcurrentAccess(t *testing.T) {
	config := DefaultPresentationConfig()
	config.ReceiveWindowSize = 16 * 1024 * 1024  // 16MB window
	config.DefaultStreamBufferSize = 1024 * 1024 // 1MB per stream
	dpm := NewDataPresentationManager(config)
	defer dpm.Shutdown()

	dpm.Start()

	var wg sync.WaitGroup
	errors := make(chan error, 100)

	// Concurrent stream creation
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(streamID uint64) {
			defer wg.Done()
			err := dpm.CreateStreamBuffer(streamID, nil)
			if err != nil {
				errors <- err
			}
		}(uint64(i + 1))
	}

	// Concurrent writes
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(streamID uint64) {
			defer wg.Done()
			time.Sleep(10 * time.Millisecond) // Let stream creation happen first
			data := []byte("Concurrent data")
			err := dpm.WriteToStream(streamID, data, 0, nil)
			if err != nil {
				errors <- err
			}
		}(uint64(i + 1))
	}

	// Concurrent reads
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(streamID uint64) {
			defer wg.Done()
			time.Sleep(20 * time.Millisecond) // Let writes happen first
			readBuf := make([]byte, 100)
			_, err := dpm.ReadFromStream(streamID, readBuf)
			if err != nil {
				errors <- err
			}
		}(uint64(i + 1))
	}

	wg.Wait()
	close(errors)

	// Check for errors
	for err := range errors {
		if err != nil {
			t.Errorf("Concurrent operation failed: %v", err)
		}
	}
}

func TestDataPresentationManager_RoutingMetrics(t *testing.T) {
	dpm := NewDataPresentationManager(nil)
	defer dpm.Shutdown()

	dpm.Start()

	// Create streams and write data
	for i := uint64(1); i <= 3; i++ {
		err := dpm.CreateStreamBuffer(i, nil)
		if err != nil {
			t.Fatalf("Failed to create stream buffer %d: %v", i, err)
		}

		data := []byte("Test data")
		err = dpm.WriteToStream(i, data, 0, nil)
		if err != nil {
			t.Fatalf("Failed to write to stream %d: %v", i, err)
		}
	}

	// Get routing metrics
	metrics := dpm.GetRoutingMetrics()

	if metrics.TotalStreams != 3 {
		t.Errorf("Expected 3 total streams, got %d", metrics.TotalStreams)
	}

	if metrics.TotalThroughput == 0 {
		t.Error("Expected non-zero total throughput")
	}

	if metrics.WindowUtilization < 0 || metrics.WindowUtilization > 1 {
		t.Errorf("Expected window utilization between 0 and 1, got %f", metrics.WindowUtilization)
	}
}

func TestDataPresentationManager_OptimizeRouting(t *testing.T) {
	config := DefaultPresentationConfig()
	config.ReceiveWindowSize = 16 * 1024 * 1024  // 16MB window
	config.DefaultStreamBufferSize = 1024 * 1024 // 1MB per stream
	dpm := NewDataPresentationManager(config)
	defer dpm.Shutdown()

	dpm.Start()

	// Create some streams
	for i := uint64(1); i <= 5; i++ {
		metadata := &StreamMetadata{
			StreamID: i,
			Priority: StreamPriorityNormal,
		}
		if i == 1 {
			metadata.Priority = StreamPriorityCritical
		}

		err := dpm.CreateStreamBuffer(i, metadata)
		if err != nil {
			t.Fatalf("Failed to create stream buffer %d: %v", i, err)
		}
	}

	// Test routing optimization
	err := dpm.OptimizeRouting()
	if err != nil {
		t.Fatalf("Failed to optimize routing: %v", err)
	}

	// Optimization should complete without error
	// Specific behavior depends on system state, so we just verify it doesn't crash
}

func TestDataPresentationManager_InvalidOperations(t *testing.T) {
	dpm := NewDataPresentationManager(nil)
	defer dpm.Shutdown()

	dpm.Start()

	// Test creating stream with invalid ID
	err := dpm.CreateStreamBuffer(0, nil)
	if err == nil {
		t.Error("Expected error when creating stream with ID 0")
	}

	// Test writing to non-existent stream
	err = dpm.WriteToStream(999, []byte("test"), 0, nil)
	if err == nil {
		t.Error("Expected error when writing to non-existent stream")
	}

	// Test reading from non-existent stream
	readBuf := make([]byte, 100)
	_, err = dpm.ReadFromStream(999, readBuf)
	if err == nil {
		t.Error("Expected error when reading from non-existent stream")
	}

	// Test getting non-existent stream buffer
	_, err = dpm.GetStreamBuffer(999)
	if err == nil {
		t.Error("Expected error when getting non-existent stream buffer")
	}
}

func BenchmarkDataPresentationManager_WriteToStream(b *testing.B) {
	dpm := NewDataPresentationManager(nil)
	defer dpm.Shutdown()

	dpm.Start()

	// Create stream buffer
	dpm.CreateStreamBuffer(1, nil)

	data := []byte("Benchmark data")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dpm.WriteToStream(1, data, uint64(i*len(data)), nil)
	}
}

func BenchmarkDataPresentationManager_ReadFromStream(b *testing.B) {
	dpm := NewDataPresentationManager(nil)
	defer dpm.Shutdown()

	dpm.Start()

	// Create stream buffer and pre-populate with data
	dpm.CreateStreamBuffer(1, nil)

	data := []byte("Benchmark data")
	for i := 0; i < 1000; i++ {
		dpm.WriteToStream(1, data, uint64(i*len(data)), nil)
	}

	readBuf := make([]byte, len(data))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dpm.ReadFromStream(1, readBuf)
	}
}

func BenchmarkDataPresentationManager_RouteDataBatch(b *testing.B) {
	dpm := NewDataPresentationManager(nil)
	defer dpm.Shutdown()

	dpm.Start()

	// Prepare batch routing data
	routingInfos := make([]*DataRoutingInfo, 10)
	for i := 0; i < 10; i++ {
		routingInfos[i] = &DataRoutingInfo{
			StreamID: uint64(i + 1),
			Data:     []byte("Batch benchmark data"),
			Offset:   0,
			Priority: StreamPriorityNormal,
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dpm.RouteDataBatch(routingInfos)
	}
}
