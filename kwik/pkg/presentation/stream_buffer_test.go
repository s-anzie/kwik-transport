package presentation

import (
	"testing"
	"time"
)

func TestStreamBuffer_BasicOperations(t *testing.T) {
	// Create a stream buffer
	metadata := &StreamMetadata{
		StreamID:   1,
		StreamType: StreamTypeData,
		Priority:   StreamPriorityNormal,
		CreatedAt:  time.Now(),
	}
	
	config := &StreamBufferConfig{
		StreamID:      1,
		BufferSize:    1024,
		Priority:      StreamPriorityNormal,
		GapTimeout:    100 * time.Millisecond,
		EnableMetrics: true,
	}
	
	buffer := NewStreamBuffer(1, metadata, config)
	defer buffer.Close()
	
	// Test writing data
	data := []byte("Hello, World!")
	err := buffer.Write(data, 0)
	if err != nil {
		t.Fatalf("Failed to write data: %v", err)
	}
	
	// Test reading data
	readBuf := make([]byte, 100)
	n, err := buffer.Read(readBuf)
	if err != nil {
		t.Fatalf("Failed to read data: %v", err)
	}
	
	if n != len(data) {
		t.Errorf("Expected to read %d bytes, got %d", len(data), n)
	}
	
	if string(readBuf[:n]) != string(data) {
		t.Errorf("Expected to read %q, got %q", string(data), string(readBuf[:n]))
	}
}

func TestStreamBuffer_NonSequentialWrites(t *testing.T) {
	buffer := NewStreamBuffer(1, nil, nil)
	defer buffer.Close()
	
	// Write data out of order
	data1 := []byte("World!")
	data2 := []byte("Hello, ")
	
	// Write second part first
	err := buffer.Write(data1, 7)
	if err != nil {
		t.Fatalf("Failed to write data1: %v", err)
	}
	
	// Write first part
	err = buffer.Write(data2, 0)
	if err != nil {
		t.Fatalf("Failed to write data2: %v", err)
	}
	
	// Read should return all contiguous data
	readBuf := make([]byte, 100)
	n, err := buffer.Read(readBuf)
	if err != nil {
		t.Fatalf("Failed to read data: %v", err)
	}
	
	expected := "Hello, World!"
	if n != len(expected) {
		t.Errorf("Expected to read %d bytes, got %d", len(expected), n)
	}
	
	if string(readBuf[:n]) != expected {
		t.Errorf("Expected to read %q, got %q", expected, string(readBuf[:n]))
	}
	
	// Second read should return no data since all contiguous data was consumed
	n, err = buffer.Read(readBuf)
	if err != nil {
		t.Fatalf("Failed to read second time: %v", err)
	}
	
	if n != 0 {
		t.Errorf("Expected to read 0 bytes on second read, got %d", n)
	}
}

func TestStreamBuffer_GapDetection(t *testing.T) {
	buffer := NewStreamBuffer(1, nil, nil)
	defer buffer.Close()
	
	// Write data with gaps
	data1 := []byte("Hello")
	data2 := []byte("World")
	
	err := buffer.Write(data1, 0)
	if err != nil {
		t.Fatalf("Failed to write data1: %v", err)
	}
	
	// Write with a gap (offset 10 instead of 5)
	err = buffer.Write(data2, 10)
	if err != nil {
		t.Fatalf("Failed to write data2: %v", err)
	}
	
	// Check gap detection
	if !buffer.HasGaps() {
		t.Error("Expected buffer to have gaps")
	}
	
	gapPos, hasGap := buffer.GetNextGapPosition()
	if !hasGap {
		t.Error("Expected to find a gap")
	}
	
	if gapPos != 5 {
		t.Errorf("Expected gap at position 5, got %d", gapPos)
	}
	
	// Read should only return contiguous data
	readBuf := make([]byte, 100)
	n, err := buffer.Read(readBuf)
	if err != nil {
		t.Fatalf("Failed to read data: %v", err)
	}
	
	if n != len(data1) {
		t.Errorf("Expected to read %d bytes, got %d", len(data1), n)
	}
	
	if string(readBuf[:n]) != string(data1) {
		t.Errorf("Expected to read %q, got %q", string(data1), string(readBuf[:n]))
	}
}

func TestStreamBuffer_FillGap(t *testing.T) {
	buffer := NewStreamBuffer(1, nil, nil)
	defer buffer.Close()
	
	// Create a gap scenario
	data1 := []byte("Hello")
	data2 := []byte("World")
	gapData := []byte(" ")
	
	err := buffer.Write(data1, 0)
	if err != nil {
		t.Fatalf("Failed to write data1: %v", err)
	}
	
	err = buffer.Write(data2, 6)
	if err != nil {
		t.Fatalf("Failed to write data2: %v", err)
	}
	
	// Fill the gap
	err = buffer.FillGap(5, gapData)
	if err != nil {
		t.Fatalf("Failed to fill gap: %v", err)
	}
	
	// Now we should be able to read all data contiguously
	readBuf := make([]byte, 100)
	n, err := buffer.Read(readBuf)
	if err != nil {
		t.Fatalf("Failed to read data: %v", err)
	}
	
	expected := "Hello World"
	if n != len(expected) {
		t.Errorf("Expected to read %d bytes, got %d", len(expected), n)
	}
	
	if string(readBuf[:n]) != expected {
		t.Errorf("Expected to read %q, got %q", expected, string(readBuf[:n]))
	}
}

func TestStreamBuffer_BufferUsage(t *testing.T) {
	config := &StreamBufferConfig{
		StreamID:   1,
		BufferSize: 100,
	}
	
	buffer := NewStreamBuffer(1, nil, config)
	defer buffer.Close()
	
	// Check initial usage
	usage := buffer.GetBufferUsage()
	if usage.TotalCapacity != 100 {
		t.Errorf("Expected total capacity 100, got %d", usage.TotalCapacity)
	}
	
	if usage.UsedCapacity != 0 {
		t.Errorf("Expected used capacity 0, got %d", usage.UsedCapacity)
	}
	
	if usage.Utilization != 0 {
		t.Errorf("Expected utilization 0, got %f", usage.Utilization)
	}
	
	// Write some data
	data := []byte("Hello, World!")
	err := buffer.Write(data, 0)
	if err != nil {
		t.Fatalf("Failed to write data: %v", err)
	}
	
	// Check usage after write
	usage = buffer.GetBufferUsage()
	if usage.UsedCapacity != uint64(len(data)) {
		t.Errorf("Expected used capacity %d, got %d", len(data), usage.UsedCapacity)
	}
	
	expectedUtilization := float64(len(data)) / 100.0
	if usage.Utilization != expectedUtilization {
		t.Errorf("Expected utilization %f, got %f", expectedUtilization, usage.Utilization)
	}
}

func TestStreamBuffer_BackpressureDetection(t *testing.T) {
	config := &StreamBufferConfig{
		StreamID:   1,
		BufferSize: 10, // Small buffer to trigger backpressure
	}
	
	buffer := NewStreamBuffer(1, nil, config)
	defer buffer.Close()
	
	// Initially no backpressure needed
	if buffer.IsBackpressureNeeded() {
		t.Error("Expected no backpressure needed initially")
	}
	
	// Fill buffer to trigger backpressure
	data := []byte("1234567890") // Exactly buffer size
	err := buffer.Write(data, 0)
	if err != nil {
		t.Fatalf("Failed to write data: %v", err)
	}
	
	// Should need backpressure now
	if !buffer.IsBackpressureNeeded() {
		t.Error("Expected backpressure needed after filling buffer")
	}
}

func TestStreamBuffer_ReadPosition(t *testing.T) {
	buffer := NewStreamBuffer(1, nil, nil)
	defer buffer.Close()
	
	// Initial read position should be 0
	if pos := buffer.GetReadPosition(); pos != 0 {
		t.Errorf("Expected initial read position 0, got %d", pos)
	}
	
	// Write and read some data
	data := []byte("Hello, World!")
	err := buffer.Write(data, 0)
	if err != nil {
		t.Fatalf("Failed to write data: %v", err)
	}
	
	readBuf := make([]byte, 5)
	n, err := buffer.Read(readBuf)
	if err != nil {
		t.Fatalf("Failed to read data: %v", err)
	}
	
	// Read position should advance
	if pos := buffer.GetReadPosition(); pos != uint64(n) {
		t.Errorf("Expected read position %d, got %d", n, pos)
	}
	
	// Test manual position setting
	err = buffer.SetReadPosition(10)
	if err != nil {
		t.Fatalf("Failed to set read position: %v", err)
	}
	
	if pos := buffer.GetReadPosition(); pos != 10 {
		t.Errorf("Expected read position 10, got %d", pos)
	}
}

func TestStreamBuffer_Cleanup(t *testing.T) {
	buffer := NewStreamBuffer(1, nil, nil)
	defer buffer.Close()
	
	// Write some data
	data := []byte("Hello, World!")
	err := buffer.Write(data, 0)
	if err != nil {
		t.Fatalf("Failed to write data: %v", err)
	}
	
	// Read part of the data
	readBuf := make([]byte, 5)
	_, err = buffer.Read(readBuf)
	if err != nil {
		t.Fatalf("Failed to read data: %v", err)
	}
	
	// Get cleanup stats before cleanup
	statsBefore := buffer.GetCleanupStats()
	
	// Perform cleanup
	err = buffer.Cleanup()
	if err != nil {
		t.Fatalf("Failed to cleanup: %v", err)
	}
	
	// Get cleanup stats after cleanup
	statsAfter := buffer.GetCleanupStats()
	
	// Should have fewer cleanable chunks after cleanup
	if statsAfter.CleanableChunks >= statsBefore.CleanableChunks {
		t.Error("Expected cleanup to reduce cleanable chunks")
	}
}

func TestStreamBuffer_ContiguousRanges(t *testing.T) {
	buffer := NewStreamBuffer(1, nil, nil)
	defer buffer.Close()
	
	// Write data with gaps
	data1 := []byte("Hello")
	data2 := []byte("World")
	data3 := []byte("!")
	
	err := buffer.Write(data1, 0)
	if err != nil {
		t.Fatalf("Failed to write data1: %v", err)
	}
	
	err = buffer.Write(data2, 10)
	if err != nil {
		t.Fatalf("Failed to write data2: %v", err)
	}
	
	err = buffer.Write(data3, 20)
	if err != nil {
		t.Fatalf("Failed to write data3: %v", err)
	}
	
	// Get contiguous ranges
	ranges := buffer.GetContiguousRanges()
	
	// Should have 3 separate ranges due to gaps
	if len(ranges) != 3 {
		t.Errorf("Expected 3 contiguous ranges, got %d", len(ranges))
	}
	
	// Check first range
	if ranges[0].StartOffset != 0 || ranges[0].EndOffset != 5 {
		t.Errorf("Expected first range [0,5], got [%d,%d]", ranges[0].StartOffset, ranges[0].EndOffset)
	}
	
	// Check second range
	if ranges[1].StartOffset != 10 || ranges[1].EndOffset != 15 {
		t.Errorf("Expected second range [10,15], got [%d,%d]", ranges[1].StartOffset, ranges[1].EndOffset)
	}
	
	// Check third range
	if ranges[2].StartOffset != 20 || ranges[2].EndOffset != 21 {
		t.Errorf("Expected third range [20,21], got [%d,%d]", ranges[2].StartOffset, ranges[2].EndOffset)
	}
}

func TestStreamBuffer_WaitForContiguousData(t *testing.T) {
	buffer := NewStreamBuffer(1, nil, nil)
	defer buffer.Close()
	
	// Start a goroutine to write data after a delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		data := []byte("Hello, World!")
		buffer.Write(data, 0)
	}()
	
	// Wait for contiguous data
	n, err := buffer.WaitForContiguousData(10, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("Failed to wait for contiguous data: %v", err)
	}
	
	if n < 10 {
		t.Errorf("Expected at least 10 contiguous bytes, got %d", n)
	}
}

func TestStreamBuffer_WaitForContiguousDataTimeout(t *testing.T) {
	buffer := NewStreamBuffer(1, nil, nil)
	defer buffer.Close()
	
	// Wait for data that never comes
	_, err := buffer.WaitForContiguousData(10, 50*time.Millisecond)
	if err == nil {
		t.Error("Expected timeout error")
	}
}

func TestStreamBuffer_Statistics(t *testing.T) {
	buffer := NewStreamBuffer(1, nil, nil)
	defer buffer.Close()
	
	// Write and read some data to generate statistics
	data := []byte("Hello, World!")
	err := buffer.Write(data, 0)
	if err != nil {
		t.Fatalf("Failed to write data: %v", err)
	}
	
	readBuf := make([]byte, len(data))
	_, err = buffer.Read(readBuf)
	if err != nil {
		t.Fatalf("Failed to read data: %v", err)
	}
	
	// Get detailed statistics
	stats := buffer.GetDetailedStats()
	
	if stats.BytesWritten != uint64(len(data)) {
		t.Errorf("Expected bytes written %d, got %d", len(data), stats.BytesWritten)
	}
	
	if stats.BytesRead != uint64(len(data)) {
		t.Errorf("Expected bytes read %d, got %d", len(data), stats.BytesRead)
	}
	
	if stats.WriteOperations != 1 {
		t.Errorf("Expected 1 write operation, got %d", stats.WriteOperations)
	}
	
	if stats.ReadOperations != 1 {
		t.Errorf("Expected 1 read operation, got %d", stats.ReadOperations)
	}
}

func BenchmarkStreamBuffer_Write(b *testing.B) {
	buffer := NewStreamBuffer(1, nil, nil)
	defer buffer.Close()
	
	data := []byte("Hello, World!")
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buffer.Write(data, uint64(i*len(data)))
	}
}

func BenchmarkStreamBuffer_Read(b *testing.B) {
	buffer := NewStreamBuffer(1, nil, nil)
	defer buffer.Close()
	
	// Pre-populate buffer
	data := []byte("Hello, World!")
	for i := 0; i < 1000; i++ {
		buffer.Write(data, uint64(i*len(data)))
	}
	
	readBuf := make([]byte, len(data))
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buffer.Read(readBuf)
	}
}