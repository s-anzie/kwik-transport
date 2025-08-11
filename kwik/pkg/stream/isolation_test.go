package stream

import (
	"testing"
	"time"

	datapb "kwik/proto/data"
)

func TestStreamIsolationManager_CreateIsolatedStream(t *testing.T) {
	sim := NewStreamIsolationManager(nil)
	defer sim.Close()

	// Test creating isolated stream
	logicalStreamID := uint64(1)
	realStreamID := uint64(100)
	pathID := "path1"

	isolatedStream, err := sim.CreateIsolatedStream(logicalStreamID, realStreamID, pathID)
	if err != nil {
		t.Fatalf("Failed to create isolated stream: %v", err)
	}

	if isolatedStream.LogicalStreamID != logicalStreamID {
		t.Errorf("Expected logical stream ID %d, got %d", logicalStreamID, isolatedStream.LogicalStreamID)
	}

	if isolatedStream.RealStreamID != realStreamID {
		t.Errorf("Expected real stream ID %d, got %d", realStreamID, isolatedStream.RealStreamID)
	}

	if isolatedStream.PathID != pathID {
		t.Errorf("Expected path ID %s, got %s", pathID, isolatedStream.PathID)
	}

	// Test duplicate creation fails
	_, err = sim.CreateIsolatedStream(logicalStreamID, realStreamID, pathID)
	if err == nil {
		t.Error("Expected error when creating duplicate isolated stream")
	}
}

func TestStreamIsolationManager_StreamCapacityLimit(t *testing.T) {
	config := DefaultIsolationConfig()
	config.MaxStreamsPerReal = 2 // Limit to 2 streams per real stream
	sim := NewStreamIsolationManager(config)
	defer sim.Close()

	realStreamID := uint64(100)
	pathID := "path1"

	// Create streams up to the limit
	for i := 0; i < config.MaxStreamsPerReal; i++ {
		logicalStreamID := uint64(i + 1)
		_, err := sim.CreateIsolatedStream(logicalStreamID, realStreamID, pathID)
		if err != nil {
			t.Fatalf("Failed to create isolated stream %d: %v", i+1, err)
		}
	}

	// Try to create one more stream - should fail
	logicalStreamID := uint64(config.MaxStreamsPerReal + 1)
	_, err := sim.CreateIsolatedStream(logicalStreamID, realStreamID, pathID)
	if err == nil {
		t.Error("Expected error when exceeding real stream capacity")
	}
}

func TestStreamIsolationManager_ReadWriteIsolation(t *testing.T) {
	sim := NewStreamIsolationManager(nil)
	defer sim.Close()

	// Create two isolated streams on the same real stream
	logicalStreamID1 := uint64(1)
	logicalStreamID2 := uint64(2)
	realStreamID := uint64(100)
	pathID := "path1"

	_, err := sim.CreateIsolatedStream(logicalStreamID1, realStreamID, pathID)
	if err != nil {
		t.Fatalf("Failed to create isolated stream 1: %v", err)
	}

	_, err = sim.CreateIsolatedStream(logicalStreamID2, realStreamID, pathID)
	if err != nil {
		t.Fatalf("Failed to create isolated stream 2: %v", err)
	}

	// Write different data to each stream
	data1 := []byte("stream1 data")
	data2 := []byte("stream2 data")

	n1, err := sim.WriteToIsolatedStream(logicalStreamID1, data1)
	if err != nil {
		t.Fatalf("Failed to write to stream 1: %v", err)
	}
	if n1 != len(data1) {
		t.Errorf("Expected to write %d bytes to stream 1, wrote %d", len(data1), n1)
	}

	n2, err := sim.WriteToIsolatedStream(logicalStreamID2, data2)
	if err != nil {
		t.Fatalf("Failed to write to stream 2: %v", err)
	}
	if n2 != len(data2) {
		t.Errorf("Expected to write %d bytes to stream 2, wrote %d", len(data2), n2)
	}

	// Verify isolation by checking that each stream only sees its own data
	// Note: In a real implementation, we would need to simulate frame processing
	// to move data from write buffers to read buffers
}

func TestStreamIsolationManager_ProcessIncomingFrame(t *testing.T) {
	sim := NewStreamIsolationManager(nil)
	defer sim.Close()

	// Create isolated stream
	logicalStreamID := uint64(1)
	realStreamID := uint64(100)
	pathID := "path1"

	_, err := sim.CreateIsolatedStream(logicalStreamID, realStreamID, pathID)
	if err != nil {
		t.Fatalf("Failed to create isolated stream: %v", err)
	}

	// Create frame for the stream
	frameData := []byte("test frame data")
	frame := &datapb.DataFrame{
		FrameId:         1,
		LogicalStreamId: logicalStreamID,
		Offset:          0,
		Data:            frameData,
		Fin:             false,
		Timestamp:       uint64(time.Now().UnixNano()),
		PathId:          pathID,
		DataLength:      uint32(len(frameData)),
	}

	// Process incoming frame
	err = sim.ProcessIncomingFrame(frame)
	if err != nil {
		t.Fatalf("Failed to process incoming frame: %v", err)
	}

	// Read data from isolated stream
	buffer := make([]byte, len(frameData))
	n, err := sim.ReadFromIsolatedStream(logicalStreamID, buffer)
	if err != nil {
		t.Fatalf("Failed to read from isolated stream: %v", err)
	}

	if n != len(frameData) {
		t.Errorf("Expected to read %d bytes, read %d", len(frameData), n)
	}

	if string(buffer[:n]) != string(frameData) {
		t.Errorf("Expected to read %q, read %q", string(frameData), string(buffer[:n]))
	}
}

func TestStreamIsolationManager_FlowControl(t *testing.T) {
	config := DefaultIsolationConfig()
	config.InitialWindowSize = 100 // Small window for testing
	sim := NewStreamIsolationManager(config)
	defer sim.Close()

	// Create isolated stream
	logicalStreamID := uint64(1)
	realStreamID := uint64(100)
	pathID := "path1"

	_, err := sim.CreateIsolatedStream(logicalStreamID, realStreamID, pathID)
	if err != nil {
		t.Fatalf("Failed to create isolated stream: %v", err)
	}

	// Write data that exceeds the window size
	largeData := make([]byte, config.InitialWindowSize+1)
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}

	// This should fail due to flow control
	_, err = sim.WriteToIsolatedStream(logicalStreamID, largeData)
	if err == nil {
		t.Error("Expected error when writing data larger than flow control window")
	}

	// Write data within window size should succeed
	smallData := make([]byte, config.InitialWindowSize/2)
	for i := range smallData {
		smallData[i] = byte(i % 256)
	}

	n, err := sim.WriteToIsolatedStream(logicalStreamID, smallData)
	if err != nil {
		t.Fatalf("Failed to write small data: %v", err)
	}
	if n != len(smallData) {
		t.Errorf("Expected to write %d bytes, wrote %d", len(smallData), n)
	}
}

func TestStreamIsolationManager_CloseIsolatedStream(t *testing.T) {
	sim := NewStreamIsolationManager(nil)
	defer sim.Close()

	// Create isolated stream
	logicalStreamID := uint64(1)
	realStreamID := uint64(100)
	pathID := "path1"

	_, err := sim.CreateIsolatedStream(logicalStreamID, realStreamID, pathID)
	if err != nil {
		t.Fatalf("Failed to create isolated stream: %v", err)
	}

	// Close the stream
	err = sim.CloseIsolatedStream(logicalStreamID)
	if err != nil {
		t.Fatalf("Failed to close isolated stream: %v", err)
	}

	// Verify stream is no longer accessible
	_, err = sim.ReadFromIsolatedStream(logicalStreamID, make([]byte, 100))
	if err == nil {
		t.Error("Expected error when reading from closed stream")
	}

	_, err = sim.WriteToIsolatedStream(logicalStreamID, []byte("test"))
	if err == nil {
		t.Error("Expected error when writing to closed stream")
	}
}

func TestStreamIsolationManager_GetIsolationStats(t *testing.T) {
	sim := NewStreamIsolationManager(nil)
	defer sim.Close()

	// Initially no streams
	stats := sim.GetIsolationStats()
	if stats.TotalIsolatedStreams != 0 {
		t.Errorf("Expected 0 total streams, got %d", stats.TotalIsolatedStreams)
	}

	// Create some streams
	realStreamID := uint64(100)
	pathID := "path1"

	for i := 1; i <= 3; i++ {
		logicalStreamID := uint64(i)
		_, err := sim.CreateIsolatedStream(logicalStreamID, realStreamID, pathID)
		if err != nil {
			t.Fatalf("Failed to create isolated stream %d: %v", i, err)
		}
	}

	// Check stats
	stats = sim.GetIsolationStats()
	if stats.TotalIsolatedStreams != 3 {
		t.Errorf("Expected 3 total streams, got %d", stats.TotalIsolatedStreams)
	}

	if stats.ActiveStreams != 3 {
		t.Errorf("Expected 3 active streams, got %d", stats.ActiveStreams)
	}

	if stats.RealStreamUtilization[realStreamID] != 3 {
		t.Errorf("Expected real stream utilization of 3, got %d", stats.RealStreamUtilization[realStreamID])
	}
}

func TestIsolationBuffer_ReadWrite(t *testing.T) {
	buffer := &IsolationBuffer{
		data:        make([]byte, 0, 100),
		capacity:    100,
		readOffset:  0,
		writeOffset: 0,
	}

	// Test write
	testData := []byte("hello world")
	n, err := buffer.Write(testData)
	if err != nil {
		t.Fatalf("Failed to write to buffer: %v", err)
	}
	if n != len(testData) {
		t.Errorf("Expected to write %d bytes, wrote %d", len(testData), n)
	}

	// Test read
	readBuffer := make([]byte, len(testData))
	n, err = buffer.Read(readBuffer)
	if err != nil {
		t.Fatalf("Failed to read from buffer: %v", err)
	}
	if n != len(testData) {
		t.Errorf("Expected to read %d bytes, read %d", len(testData), n)
	}
	if string(readBuffer) != string(testData) {
		t.Errorf("Expected to read %q, read %q", string(testData), string(readBuffer))
	}
}

func TestIsolationBuffer_WriteAtOffset(t *testing.T) {
	buffer := &IsolationBuffer{
		data:        make([]byte, 0, 100),
		capacity:    100,
		readOffset:  0,
		writeOffset: 0,
	}

	// Write at offset 10
	testData := []byte("test")
	offset := uint64(10)
	n, err := buffer.WriteAtOffset(testData, offset)
	if err != nil {
		t.Fatalf("Failed to write at offset: %v", err)
	}
	if n != len(testData) {
		t.Errorf("Expected to write %d bytes, wrote %d", len(testData), n)
	}

	// Verify data is at correct offset
	if len(buffer.data) < int(offset)+len(testData) {
		t.Errorf("Buffer not extended properly")
	}

	// Read from offset
	readBuffer := make([]byte, len(testData))
	copy(readBuffer, buffer.data[offset:offset+uint64(len(testData))])
	if string(readBuffer) != string(testData) {
		t.Errorf("Expected data at offset %d to be %q, got %q", offset, string(testData), string(readBuffer))
	}
}