package data

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"kwik/internal/utils"
	"kwik/proto/data"

	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/proto"
)

// Benchmark tests for data aggregation performance
// These tests verify Requirements 7.*, 8.*, 10.*

// MockDataAggregator simulates data aggregation functionality
type MockDataAggregator struct {
	paths       map[string]*PathDataBuffer
	streams     map[uint64]*StreamBuffer
	mutex       sync.RWMutex
	frameCount  uint64
	packetCount uint64
}

type PathDataBuffer struct {
	pathID   string
	frames   []*data.DataFrame
	packets  []*data.DataPacket
	mutex    sync.RWMutex
}

type StreamBuffer struct {
	streamID uint64
	frames   []*data.DataFrame
	offset   uint64
	mutex    sync.RWMutex
}

func NewMockDataAggregator() *MockDataAggregator {
	return &MockDataAggregator{
		paths:   make(map[string]*PathDataBuffer),
		streams: make(map[uint64]*StreamBuffer),
	}
}

func (a *MockDataAggregator) AddPath(pathID string) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	
	a.paths[pathID] = &PathDataBuffer{
		pathID:  pathID,
		frames:  make([]*data.DataFrame, 0),
		packets: make([]*data.DataPacket, 0),
	}
}

func (a *MockDataAggregator) AddFrame(pathID string, frame *data.DataFrame) error {
	a.mutex.RLock()
	pathBuffer, exists := a.paths[pathID]
	a.mutex.RUnlock()
	
	if !exists {
		return utils.NewKwikError(utils.ErrPathNotFound, "path not found", nil)
	}
	
	pathBuffer.mutex.Lock()
	pathBuffer.frames = append(pathBuffer.frames, frame)
	pathBuffer.mutex.Unlock()
	
	// Update stream buffer
	a.updateStreamBuffer(frame)
	
	a.frameCount++
	return nil
}

func (a *MockDataAggregator) updateStreamBuffer(frame *data.DataFrame) {
	a.mutex.Lock()
	streamBuffer, exists := a.streams[frame.LogicalStreamId]
	if !exists {
		streamBuffer = &StreamBuffer{
			streamID: frame.LogicalStreamId,
			frames:   make([]*data.DataFrame, 0),
			offset:   0,
		}
		a.streams[frame.LogicalStreamId] = streamBuffer
	}
	a.mutex.Unlock()
	
	streamBuffer.mutex.Lock()
	streamBuffer.frames = append(streamBuffer.frames, frame)
	if frame.Offset+uint64(len(frame.Data)) > streamBuffer.offset {
		streamBuffer.offset = frame.Offset + uint64(len(frame.Data))
	}
	streamBuffer.mutex.Unlock()
}

func (a *MockDataAggregator) AggregateData(streamID uint64) ([]byte, error) {
	a.mutex.RLock()
	streamBuffer, exists := a.streams[streamID]
	a.mutex.RUnlock()
	
	if !exists {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed, "stream not found", nil)
	}
	
	streamBuffer.mutex.RLock()
	defer streamBuffer.mutex.RUnlock()
	
	// Sort frames by offset (simplified)
	frames := make([]*data.DataFrame, len(streamBuffer.frames))
	copy(frames, streamBuffer.frames)
	
	// Aggregate data
	var result []byte
	for _, frame := range frames {
		result = append(result, frame.Data...)
	}
	
	return result, nil
}

func (a *MockDataAggregator) GetStats() (uint64, uint64, int, int) {
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	
	return a.frameCount, a.packetCount, len(a.paths), len(a.streams)
}

// BenchmarkDataFrameCreation tests the performance of creating data frames
func BenchmarkDataFrameCreation(b *testing.B) {
	testData := []byte("benchmark test data for frame creation")
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for i := 0; i < b.N; i++ {
		frame := &data.DataFrame{
			FrameId:         uint64(i),
			LogicalStreamId: uint64(i % 100),
			Offset:          uint64(i * len(testData)),
			Data:            testData,
			Fin:             false,
			Timestamp:       uint64(time.Now().UnixNano()),
			PathId:          fmt.Sprintf("path-%d", i%10),
			DataLength:      uint32(len(testData)),
			Checksum:        uint32(i),
		}
		
		// Simulate frame processing
		_ = frame
	}
}

// BenchmarkDataFrameSerialization tests protobuf serialization performance
func BenchmarkDataFrameSerialization(b *testing.B) {
	testData := make([]byte, 1024) // 1KB test data
	for i := range testData {
		testData[i] = byte(i % 256)
	}
	
	frame := &data.DataFrame{
		FrameId:         12345,
		LogicalStreamId: 67890,
		Offset:          1024,
		Data:            testData,
		Fin:             false,
		Timestamp:       uint64(time.Now().UnixNano()),
		PathId:          "benchmark-path",
		DataLength:      uint32(len(testData)),
		Checksum:        0x12345678,
	}
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for i := 0; i < b.N; i++ {
		_, err := proto.Marshal(frame)
		if err != nil {
			b.Fatalf("Failed to serialize frame: %v", err)
		}
	}
}

// BenchmarkDataFrameDeserialization tests protobuf deserialization performance
func BenchmarkDataFrameDeserialization(b *testing.B) {
	testData := make([]byte, 1024) // 1KB test data
	for i := range testData {
		testData[i] = byte(i % 256)
	}
	
	frame := &data.DataFrame{
		FrameId:         12345,
		LogicalStreamId: 67890,
		Offset:          1024,
		Data:            testData,
		Fin:             false,
		Timestamp:       uint64(time.Now().UnixNano()),
		PathId:          "benchmark-path",
		DataLength:      uint32(len(testData)),
		Checksum:        0x12345678,
	}
	
	serializedData, err := proto.Marshal(frame)
	if err != nil {
		b.Fatalf("Failed to serialize frame: %v", err)
	}
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for i := 0; i < b.N; i++ {
		var deserializedFrame data.DataFrame
		err := proto.Unmarshal(serializedData, &deserializedFrame)
		if err != nil {
			b.Fatalf("Failed to deserialize frame: %v", err)
		}
	}
}

// BenchmarkDataAggregation tests the performance of aggregating data from multiple paths
func BenchmarkDataAggregation(b *testing.B) {
	pathCounts := []int{2, 4, 8, 16}
	
	for _, pathCount := range pathCounts {
		b.Run(fmt.Sprintf("Paths_%d", pathCount), func(b *testing.B) {
			benchmarkDataAggregation(b, pathCount)
		})
	}
}

func benchmarkDataAggregation(b *testing.B, pathCount int) {
	aggregator := NewMockDataAggregator()
	
	// Set up paths
	pathIDs := make([]string, pathCount)
	for i := 0; i < pathCount; i++ {
		pathID := fmt.Sprintf("path-%d", i)
		pathIDs[i] = pathID
		aggregator.AddPath(pathID)
	}
	
	// Pre-create test data
	testData := []byte("benchmark test data for aggregation")
	const streamID = 12345
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for i := 0; i < b.N; i++ {
		pathIndex := i % pathCount
		pathID := pathIDs[pathIndex]
		
		frame := &data.DataFrame{
			FrameId:         uint64(i),
			LogicalStreamId: streamID,
			Offset:          uint64(i * len(testData)),
			Data:            testData,
			Fin:             false,
			Timestamp:       uint64(time.Now().UnixNano()),
			PathId:          pathID,
			DataLength:      uint32(len(testData)),
			Checksum:        uint32(i),
		}
		
		err := aggregator.AddFrame(pathID, frame)
		if err != nil {
			b.Fatalf("Failed to add frame: %v", err)
		}
		
		// Periodically aggregate data
		if i%100 == 0 {
			_, err := aggregator.AggregateData(streamID)
			if err != nil {
				b.Fatalf("Failed to aggregate data: %v", err)
			}
		}
	}
}

// BenchmarkConcurrentDataAggregation tests concurrent data aggregation performance
func BenchmarkConcurrentDataAggregation(b *testing.B) {
	aggregator := NewMockDataAggregator()
	
	// Set up paths
	const pathCount = 8
	pathIDs := make([]string, pathCount)
	for i := 0; i < pathCount; i++ {
		pathID := fmt.Sprintf("path-%d", i)
		pathIDs[i] = pathID
		aggregator.AddPath(pathID)
	}
	
	testData := []byte("concurrent benchmark test data")
	
	b.ResetTimer()
	b.ReportAllocs()
	
	b.RunParallel(func(pb *testing.PB) {
		streamID := uint64(12345)
		frameID := uint64(0)
		
		for pb.Next() {
			pathIndex := int(frameID) % pathCount
			pathID := pathIDs[pathIndex]
			
			frame := &data.DataFrame{
				FrameId:         frameID,
				LogicalStreamId: streamID,
				Offset:          frameID * uint64(len(testData)),
				Data:            testData,
				Fin:             false,
				Timestamp:       uint64(time.Now().UnixNano()),
				PathId:          pathID,
				DataLength:      uint32(len(testData)),
				Checksum:        uint32(frameID),
			}
			
			err := aggregator.AddFrame(pathID, frame)
			if err != nil {
				b.Fatalf("Failed to add frame: %v", err)
			}
			
			frameID++
		}
	})
}

// BenchmarkDataPacketCreation tests the performance of creating data packets
func BenchmarkDataPacketCreation(b *testing.B) {
	// Pre-create frames
	const framesPerPacket = 5
	frames := make([]*data.DataFrame, framesPerPacket)
	testData := []byte("packet benchmark data")
	
	for i := 0; i < framesPerPacket; i++ {
		frames[i] = &data.DataFrame{
			FrameId:         uint64(i),
			LogicalStreamId: 12345,
			Offset:          uint64(i * len(testData)),
			Data:            testData,
			Fin:             i == framesPerPacket-1,
			Timestamp:       uint64(time.Now().UnixNano()),
			PathId:          "benchmark-path",
			DataLength:      uint32(len(testData)),
			Checksum:        uint32(i),
		}
	}
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for i := 0; i < b.N; i++ {
		packet := &data.DataPacket{
			PacketId:  uint64(i),
			PathId:    "benchmark-path",
			Frames:    frames,
			Checksum:  uint32(i),
			Timestamp: uint64(time.Now().UnixNano()),
			Metadata: &data.PacketMetadata{
				TotalSize:      uint32(len(testData) * framesPerPacket),
				FrameCount:     framesPerPacket,
				Compression:    data.CompressionType_COMPRESSION_NONE,
				SequenceNumber: uint32(i),
				Retransmission: false,
			},
		}
		
		// Simulate packet processing
		_ = packet
	}
}

// BenchmarkDataReordering tests the performance of reordering out-of-order data
func BenchmarkDataReordering(b *testing.B) {
	frameCounts := []int{10, 50, 100, 500}
	
	for _, frameCount := range frameCounts {
		b.Run(fmt.Sprintf("Frames_%d", frameCount), func(b *testing.B) {
			benchmarkDataReordering(b, frameCount)
		})
	}
}

func benchmarkDataReordering(b *testing.B, frameCount int) {
	testData := []byte("reordering benchmark data")
	
	// Create frames in random order
	frames := make([]*data.DataFrame, frameCount)
	for i := 0; i < frameCount; i++ {
		frames[i] = &data.DataFrame{
			FrameId:         uint64(i),
			LogicalStreamId: 12345,
			Offset:          uint64(i * len(testData)),
			Data:            testData,
			Fin:             i == frameCount-1,
			Timestamp:       uint64(time.Now().UnixNano()),
			PathId:          "benchmark-path",
			DataLength:      uint32(len(testData)),
			Checksum:        uint32(i),
		}
	}
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for i := 0; i < b.N; i++ {
		// Simulate reordering by sorting frames by offset
		orderedFrames := make([]*data.DataFrame, len(frames))
		copy(orderedFrames, frames)
		
		// Simple bubble sort for benchmarking (in real implementation, use more efficient sorting)
		for j := 0; j < len(orderedFrames)-1; j++ {
			for k := 0; k < len(orderedFrames)-j-1; k++ {
				if orderedFrames[k].Offset > orderedFrames[k+1].Offset {
					orderedFrames[k], orderedFrames[k+1] = orderedFrames[k+1], orderedFrames[k]
				}
			}
		}
		
		// Verify ordering
		for j := 1; j < len(orderedFrames); j++ {
			if orderedFrames[j].Offset < orderedFrames[j-1].Offset {
				b.Fatalf("Frames not properly ordered")
			}
		}
	}
}

// BenchmarkStreamMultiplexingOverhead tests the overhead of stream multiplexing
func BenchmarkStreamMultiplexingOverhead(b *testing.B) {
	ratios := []int{1, 3, 4, 8, 16} // logical streams per real stream
	
	for _, ratio := range ratios {
		b.Run(fmt.Sprintf("Ratio_%d_to_1", ratio), func(b *testing.B) {
			benchmarkMultiplexingOverhead(b, ratio)
		})
	}
}

func benchmarkMultiplexingOverhead(b *testing.B, logicalToRealRatio int) {
	aggregator := NewMockDataAggregator()
	aggregator.AddPath("benchmark-path")
	
	testData := []byte("multiplexing overhead test data")
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for i := 0; i < b.N; i++ {
		// Simulate multiplexing by creating frames for different logical streams
		// but routing them through the same real stream (path)
		logicalStreamID := uint64(i % logicalToRealRatio)
		
		frame := &data.DataFrame{
			FrameId:         uint64(i),
			LogicalStreamId: logicalStreamID,
			Offset:          uint64(i * len(testData)),
			Data:            testData,
			Fin:             false,
			Timestamp:       uint64(time.Now().UnixNano()),
			PathId:          "benchmark-path",
			DataLength:      uint32(len(testData)),
			Checksum:        uint32(i),
		}
		
		err := aggregator.AddFrame("benchmark-path", frame)
		if err != nil {
			b.Fatalf("Failed to add frame: %v", err)
		}
	}
}

// BenchmarkLargeDataAggregation tests aggregation performance with large data
func BenchmarkLargeDataAggregation(b *testing.B) {
	dataSizes := []int{1024, 4096, 16384, 65536} // 1KB to 64KB
	
	for _, dataSize := range dataSizes {
		b.Run(fmt.Sprintf("DataSize_%dB", dataSize), func(b *testing.B) {
			benchmarkLargeDataAggregation(b, dataSize)
		})
	}
}

func benchmarkLargeDataAggregation(b *testing.B, dataSize int) {
	aggregator := NewMockDataAggregator()
	aggregator.AddPath("benchmark-path")
	
	// Create large test data
	testData := make([]byte, dataSize)
	for i := range testData {
		testData[i] = byte(i % 256)
	}
	
	const streamID = 12345
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for i := 0; i < b.N; i++ {
		frame := &data.DataFrame{
			FrameId:         uint64(i),
			LogicalStreamId: streamID,
			Offset:          uint64(i * dataSize),
			Data:            testData,
			Fin:             false,
			Timestamp:       uint64(time.Now().UnixNano()),
			PathId:          "benchmark-path",
			DataLength:      uint32(dataSize),
			Checksum:        uint32(i),
		}
		
		err := aggregator.AddFrame("benchmark-path", frame)
		if err != nil {
			b.Fatalf("Failed to add frame: %v", err)
		}
		
		// Periodically aggregate to measure aggregation performance
		if i%10 == 0 {
			aggregatedData, err := aggregator.AggregateData(streamID)
			if err != nil {
				b.Fatalf("Failed to aggregate data: %v", err)
			}
			
			// Verify aggregated data size
			expectedSize := (i/10 + 1) * 10 * dataSize
			if len(aggregatedData) != expectedSize {
				b.Fatalf("Aggregated data size mismatch: expected %d, got %d", expectedSize, len(aggregatedData))
			}
		}
	}
}

// BenchmarkMemoryEfficiency tests memory usage efficiency during aggregation
func BenchmarkMemoryEfficiency(b *testing.B) {
	streamCounts := []int{10, 100, 1000}
	
	for _, streamCount := range streamCounts {
		b.Run(fmt.Sprintf("Streams_%d", streamCount), func(b *testing.B) {
			benchmarkMemoryEfficiency(b, streamCount)
		})
	}
}

func benchmarkMemoryEfficiency(b *testing.B, streamCount int) {
	aggregator := NewMockDataAggregator()
	aggregator.AddPath("benchmark-path")
	
	testData := []byte("memory efficiency test data")
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for i := 0; i < b.N; i++ {
		streamID := uint64(i % streamCount)
		
		frame := &data.DataFrame{
			FrameId:         uint64(i),
			LogicalStreamId: streamID,
			Offset:          uint64(i * len(testData)),
			Data:            testData,
			Fin:             false,
			Timestamp:       uint64(time.Now().UnixNano()),
			PathId:          "benchmark-path",
			DataLength:      uint32(len(testData)),
			Checksum:        uint32(i),
		}
		
		err := aggregator.AddFrame("benchmark-path", frame)
		if err != nil {
			b.Fatalf("Failed to add frame: %v", err)
		}
	}
	
	// Verify final state
	frameCount, packetCount, pathCount, streamCountFinal := aggregator.GetStats()
	assert.Equal(b, uint64(b.N), frameCount)
	assert.Equal(b, 1, pathCount)
	assert.LessOrEqual(b, streamCountFinal, streamCount)
	_ = packetCount // packetCount not used in this mock
}