package stream

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"kwik/pkg/control"

	"github.com/stretchr/testify/mock"
)

// Benchmark tests for stream multiplexing and data aggregation
// These tests verify Requirements 7.*, 8.*, 10.*

// BenchmarkLogicalStreamCreation tests the performance of creating logical streams
func BenchmarkLogicalStreamCreation(b *testing.B) {
	mockControlPlane := &MockControlPlane{}
	mockPathValidator := &MockPathValidator{}

	// Set up mocks for successful operations
	mockPathValidator.On("ValidatePathForStreamCreation", mock.AnythingOfType("string")).Return(nil)
	mockControlPlane.On("SendFrame", mock.AnythingOfType("string"), mock.AnythingOfType("*protocol.ControlFrame")).Return(nil)

	lsm := NewLogicalStreamManager(mockControlPlane, mockPathValidator, nil)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := lsm.CreateLogicalStream("bench-path")
		if err != nil {
			b.Fatalf("Failed to create logical stream: %v", err)
		}
	}
}

// BenchmarkLogicalStreamCreationConcurrent tests concurrent stream creation performance
func BenchmarkLogicalStreamCreationConcurrent(b *testing.B) {
	mockControlPlane := &MockControlPlane{}
	mockPathValidator := &MockPathValidator{}

	// Set up mocks for successful operations
	mockPathValidator.On("ValidatePathForStreamCreation", mock.AnythingOfType("string")).Return(nil)
	mockControlPlane.On("SendFrame", mock.AnythingOfType("string"), mock.AnythingOfType("*protocol.ControlFrame")).Return(nil)

	lsm := NewLogicalStreamManager(mockControlPlane, mockPathValidator, nil)

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := lsm.CreateLogicalStream("bench-path")
			if err != nil {
				b.Fatalf("Failed to create logical stream: %v", err)
			}
		}
	})
}

// BenchmarkStreamMultiplexingRatios tests different logical-to-real stream ratios
func BenchmarkStreamMultiplexingRatios(b *testing.B) {
	ratios := []int{1, 2, 3, 4, 5, 8, 10}

	for _, ratio := range ratios {
		b.Run(fmt.Sprintf("Ratio_%d_to_1", ratio), func(b *testing.B) {
			benchmarkStreamRatio(b, ratio)
		})
	}
}

func benchmarkStreamRatio(b *testing.B, logicalToRealRatio int) {
	mockControlPlane := &MockControlPlane{}
	mockPathValidator := &MockPathValidator{}

	// Set up mocks
	mockPathValidator.On("ValidatePathForStreamCreation", mock.AnythingOfType("string")).Return(nil)
	mockControlPlane.On("SendFrame", mock.AnythingOfType("string"), mock.AnythingOfType("*protocol.ControlFrame")).Return(nil)

	config := DefaultLogicalStreamConfig()
	config.MaxConcurrentStreams = logicalToRealRatio * 1000 // Allow enough streams

	lsm := NewLogicalStreamManager(mockControlPlane, mockPathValidator, config)

	// Pre-create streams to simulate the ratio
	streams := make([]*LogicalStreamInfo, logicalToRealRatio)
	for i := 0; i < logicalToRealRatio; i++ {
		stream, err := lsm.CreateLogicalStream("bench-path")
		if err != nil {
			b.Fatalf("Failed to create logical stream: %v", err)
		}
		streams[i] = stream
	}

	b.ResetTimer()
	b.ReportAllocs()

	// Benchmark stream operations
	for i := 0; i < b.N; i++ {
		streamIndex := i % logicalToRealRatio
		stream := streams[streamIndex]

		// Simulate stream operations
		_, err := lsm.GetLogicalStream(stream.ID)
		if err != nil {
			b.Fatalf("Failed to get logical stream: %v", err)
		}
	}
}

// BenchmarkStreamLookup tests the performance of stream lookup operations
func BenchmarkStreamLookup(b *testing.B) {
	streamCounts := []int{10, 100, 1000, 10000}

	for _, count := range streamCounts {
		b.Run(fmt.Sprintf("Streams_%d", count), func(b *testing.B) {
			benchmarkStreamLookup(b, count)
		})
	}
}

func benchmarkStreamLookup(b *testing.B, streamCount int) {
	mockControlPlane := &MockControlPlane{}
	mockPathValidator := &MockPathValidator{}

	// Set up mocks
	mockPathValidator.On("ValidatePathForStreamCreation", mock.AnythingOfType("string")).Return(nil)
	mockControlPlane.On("SendFrame", mock.AnythingOfType("string"), mock.AnythingOfType("*protocol.ControlFrame")).Return(nil)

	config := DefaultLogicalStreamConfig()
	config.MaxConcurrentStreams = streamCount

	lsm := NewLogicalStreamManager(mockControlPlane, mockPathValidator, config)

	// Pre-create streams
	streamIDs := make([]uint64, streamCount)
	for i := 0; i < streamCount; i++ {
		stream, err := lsm.CreateLogicalStream("bench-path")
		if err != nil {
			b.Fatalf("Failed to create logical stream: %v", err)
		}
		streamIDs[i] = stream.ID
	}

	b.ResetTimer()
	b.ReportAllocs()

	// Benchmark lookups
	for i := 0; i < b.N; i++ {
		streamID := streamIDs[i%streamCount]
		_, err := lsm.GetLogicalStream(streamID)
		if err != nil {
			b.Fatalf("Failed to get logical stream: %v", err)
		}
	}
}

// BenchmarkStreamCleanup tests the performance of stream cleanup operations
func BenchmarkStreamCleanup(b *testing.B) {
	mockControlPlane := &MockControlPlane{}
	mockPathValidator := &MockPathValidator{}

	// Set up mocks
	mockPathValidator.On("ValidatePathForStreamCreation", mock.AnythingOfType("string")).Return(nil)
	mockControlPlane.On("SendFrame", mock.AnythingOfType("string"), mock.AnythingOfType("*protocol.ControlFrame")).Return(nil)

	config := DefaultLogicalStreamConfig()
	config.StreamIdleTimeout = 1 * time.Millisecond // Very short timeout for benchmarking

	lsm := NewLogicalStreamManager(mockControlPlane, mockPathValidator, config)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Create stream
		stream, err := lsm.CreateLogicalStream("bench-path")
		if err != nil {
			b.Fatalf("Failed to create logical stream: %v", err)
		}

		// Wait for it to become idle
		time.Sleep(2 * time.Millisecond)

		// Trigger cleanup
		lsm.cleanupIdleStreams()

		// Verify cleanup
		_, err = lsm.GetLogicalStream(stream.ID)
		if err == nil {
			b.Fatalf("Stream should have been cleaned up")
		}
	}
}

// BenchmarkConcurrentStreamOperations tests concurrent stream operations
func BenchmarkConcurrentStreamOperations(b *testing.B) {
	mockControlPlane := &MockControlPlane{}
	mockPathValidator := &MockPathValidator{}

	// Set up mocks
	mockPathValidator.On("ValidatePathForStreamCreation", mock.AnythingOfType("string")).Return(nil)
	mockControlPlane.On("SendFrame", mock.AnythingOfType("string"), mock.AnythingOfType("*protocol.ControlFrame")).Return(nil)

	config := DefaultLogicalStreamConfig()
	config.MaxConcurrentStreams = 10000

	lsm := NewLogicalStreamManager(mockControlPlane, mockPathValidator, config)

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			// Create stream
			stream, err := lsm.CreateLogicalStream("bench-path")
			if err != nil {
				b.Fatalf("Failed to create logical stream: %v", err)
			}

			// Lookup stream
			_, err = lsm.GetLogicalStream(stream.ID)
			if err != nil {
				b.Fatalf("Failed to get logical stream: %v", err)
			}

			// Close stream
			err = lsm.CloseLogicalStream(stream.ID)
			if err != nil {
				b.Fatalf("Failed to close logical stream: %v", err)
			}
		}
	})
}

// BenchmarkNotificationSerialization tests the performance of notification serialization
func BenchmarkNotificationSerialization(b *testing.B) {
	mockControlPlane := &MockControlPlane{}
	mockPathValidator := &MockPathValidator{}

	lsm := NewLogicalStreamManager(mockControlPlane, mockPathValidator, nil)

	notification := &control.StreamCreateNotification{
		LogicalStreamID: 12345,
		PathID:          "benchmark-path-id",
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := lsm.serializeNotification(notification)
		if err != nil {
			b.Fatalf("Failed to serialize notification: %v", err)
		}
	}
}

// BenchmarkStreamManagerMemoryUsage tests memory usage patterns
func BenchmarkStreamManagerMemoryUsage(b *testing.B) {
	streamCounts := []int{100, 1000, 10000}

	for _, count := range streamCounts {
		b.Run(fmt.Sprintf("Streams_%d", count), func(b *testing.B) {
			benchmarkMemoryUsage(b, count)
		})
	}
}

func benchmarkMemoryUsage(b *testing.B, streamCount int) {
	mockControlPlane := &MockControlPlane{}
	mockPathValidator := &MockPathValidator{}

	// Set up mocks
	mockPathValidator.On("ValidatePathForStreamCreation", mock.AnythingOfType("string")).Return(nil)
	mockControlPlane.On("SendFrame", mock.AnythingOfType("string"), mock.AnythingOfType("*protocol.ControlFrame")).Return(nil)

	config := DefaultLogicalStreamConfig()
	config.MaxConcurrentStreams = streamCount

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		lsm := NewLogicalStreamManager(mockControlPlane, mockPathValidator, config)

		// Create streams
		for j := 0; j < streamCount; j++ {
			_, err := lsm.CreateLogicalStream("bench-path")
			if err != nil {
				b.Fatalf("Failed to create logical stream: %v", err)
			}
		}

		// Clean up
		lsm.Close()
	}
}

// Load test for sustained stream operations
func BenchmarkSustainedStreamLoad(b *testing.B) {
	mockControlPlane := &MockControlPlane{}
	mockPathValidator := &MockPathValidator{}

	// Set up mocks
	mockPathValidator.On("ValidatePathForStreamCreation", mock.AnythingOfType("string")).Return(nil)
	mockControlPlane.On("SendFrame", mock.AnythingOfType("string"), mock.AnythingOfType("*protocol.ControlFrame")).Return(nil)

	config := DefaultLogicalStreamConfig()
	config.MaxConcurrentStreams = 1000

	lsm := NewLogicalStreamManager(mockControlPlane, mockPathValidator, config)

	// Pre-create some streams to simulate ongoing load
	const baseStreams = 100
	for i := 0; i < baseStreams; i++ {
		_, err := lsm.CreateLogicalStream("bench-path")
		if err != nil {
			b.Fatalf("Failed to create base stream: %v", err)
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	// Simulate sustained load with mixed operations
	for i := 0; i < b.N; i++ {
		switch i % 4 {
		case 0: // Create stream
			_, err := lsm.CreateLogicalStream("bench-path")
			if err != nil {
				// Ignore max streams errors in load test
				if err.Error() != "maximum concurrent streams limit reached" {
					b.Fatalf("Failed to create logical stream: %v", err)
				}
			}
		case 1: // Lookup stream
			activeStreams := lsm.GetActiveStreams()
			if len(activeStreams) > 0 {
				_, err := lsm.GetLogicalStream(activeStreams[0].ID)
				if err != nil {
					b.Fatalf("Failed to get logical stream: %v", err)
				}
			}
		case 2: // Get stream count
			_ = lsm.GetStreamCount()
		case 3: // Close a stream
			activeStreams := lsm.GetActiveStreams()
			if len(activeStreams) > baseStreams {
				err := lsm.CloseLogicalStream(activeStreams[len(activeStreams)-1].ID)
				if err != nil {
					b.Fatalf("Failed to close logical stream: %v", err)
				}
			}
		}
	}
}

// Benchmark for testing optimal stream ratios under load
func BenchmarkOptimalStreamRatios(b *testing.B) {
	// Test the optimal 3-4 logical streams per real stream ratio
	optimalRatios := []int{3, 4}

	for _, ratio := range optimalRatios {
		b.Run(fmt.Sprintf("OptimalRatio_%d", ratio), func(b *testing.B) {
			benchmarkOptimalRatio(b, ratio)
		})
	}
}

func benchmarkOptimalRatio(b *testing.B, ratio int) {
	mockControlPlane := &MockControlPlane{}
	mockPathValidator := &MockPathValidator{}

	// Set up mocks
	mockPathValidator.On("ValidatePathForStreamCreation", mock.AnythingOfType("string")).Return(nil)
	mockControlPlane.On("SendFrame", mock.AnythingOfType("string"), mock.AnythingOfType("*protocol.ControlFrame")).Return(nil)

	config := DefaultLogicalStreamConfig()
	config.MaxConcurrentStreams = ratio * 100 // Allow enough streams

	lsm := NewLogicalStreamManager(mockControlPlane, mockPathValidator, config)

	// Simulate real streams by creating groups of logical streams
	const realStreams = 10
	streamGroups := make([][]*LogicalStreamInfo, realStreams)

	// Create logical streams in groups (simulating real stream multiplexing)
	for i := 0; i < realStreams; i++ {
		streamGroups[i] = make([]*LogicalStreamInfo, ratio)
		for j := 0; j < ratio; j++ {
			stream, err := lsm.CreateLogicalStream(fmt.Sprintf("real-stream-%d", i))
			if err != nil {
				b.Fatalf("Failed to create logical stream: %v", err)
			}
			streamGroups[i][j] = stream
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	// Benchmark operations across the multiplexed streams
	for i := 0; i < b.N; i++ {
		realStreamIndex := i % realStreams
		logicalStreamIndex := i % ratio

		stream := streamGroups[realStreamIndex][logicalStreamIndex]

		// Simulate stream operations
		_, err := lsm.GetLogicalStream(stream.ID)
		if err != nil {
			b.Fatalf("Failed to get logical stream: %v", err)
		}
	}
}

// Benchmark for testing stream operations under different concurrency levels
func BenchmarkStreamConcurrencyLevels(b *testing.B) {
	concurrencyLevels := []int{1, 2, 4, 8, 16, 32}

	for _, level := range concurrencyLevels {
		b.Run(fmt.Sprintf("Concurrency_%d", level), func(b *testing.B) {
			benchmarkConcurrencyLevel(b, level)
		})
	}
}

func benchmarkConcurrencyLevel(b *testing.B, concurrency int) {
	mockControlPlane := &MockControlPlane{}
	mockPathValidator := &MockPathValidator{}

	// Set up mocks
	mockPathValidator.On("ValidatePathForStreamCreation", mock.AnythingOfType("string")).Return(nil)
	mockControlPlane.On("SendFrame", mock.AnythingOfType("string"), mock.AnythingOfType("*protocol.ControlFrame")).Return(nil)

	config := DefaultLogicalStreamConfig()
	config.MaxConcurrentStreams = 10000

	lsm := NewLogicalStreamManager(mockControlPlane, mockPathValidator, config)

	b.ResetTimer()
	b.ReportAllocs()

	// Use a channel to control concurrency
	semaphore := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i := 0; i < b.N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			// Acquire semaphore
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			// Perform stream operations
			stream, err := lsm.CreateLogicalStream("bench-path")
			if err != nil {
				b.Errorf("Failed to create logical stream: %v", err)
				return
			}

			_, err = lsm.GetLogicalStream(stream.ID)
			if err != nil {
				b.Errorf("Failed to get logical stream: %v", err)
				return
			}

			err = lsm.CloseLogicalStream(stream.ID)
			if err != nil {
				b.Errorf("Failed to close logical stream: %v", err)
				return
			}
		}()
	}

	wg.Wait()
}

// Benchmark for testing stream manager scalability
func BenchmarkStreamManagerScalability(b *testing.B) {
	scales := []int{10, 100, 1000, 5000}

	for _, scale := range scales {
		b.Run(fmt.Sprintf("Scale_%d", scale), func(b *testing.B) {
			benchmarkScalability(b, scale)
		})
	}
}

func benchmarkScalability(b *testing.B, scale int) {
	mockControlPlane := &MockControlPlane{}
	mockPathValidator := &MockPathValidator{}

	// Set up mocks
	mockPathValidator.On("ValidatePathForStreamCreation", mock.AnythingOfType("string")).Return(nil)
	mockControlPlane.On("SendFrame", mock.AnythingOfType("string"), mock.AnythingOfType("*protocol.ControlFrame")).Return(nil)

	config := DefaultLogicalStreamConfig()
	config.MaxConcurrentStreams = scale

	lsm := NewLogicalStreamManager(mockControlPlane, mockPathValidator, config)

	// Pre-populate with streams at the target scale
	streamIDs := make([]uint64, scale)
	for i := 0; i < scale; i++ {
		stream, err := lsm.CreateLogicalStream("bench-path")
		if err != nil {
			b.Fatalf("Failed to create logical stream: %v", err)
		}
		streamIDs[i] = stream.ID
	}

	b.ResetTimer()
	b.ReportAllocs()

	// Benchmark operations at scale
	for i := 0; i < b.N; i++ {
		streamID := streamIDs[i%scale]

		// Mix of operations
		switch i % 3 {
		case 0:
			_, err := lsm.GetLogicalStream(streamID)
			if err != nil {
				b.Fatalf("Failed to get logical stream: %v", err)
			}
		case 1:
			_ = lsm.GetActiveStreams()
		case 2:
			_ = lsm.GetStreamCount()
		}
	}
}
