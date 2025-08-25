package data

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestOffsetTracker_BasicOperations(t *testing.T) {
	tracker := NewOffsetTracker(64)

	// Test marking offsets as received
	if !tracker.MarkReceived(0) {
		t.Errorf("Expected offset 0 to be marked as new")
	}

	if tracker.MarkReceived(0) {
		t.Errorf("Expected offset 0 to be marked as duplicate")
	}

	// Test checking received status
	if !tracker.IsReceived(0) {
		t.Errorf("Expected offset 0 to be marked as received")
	}

	if tracker.IsReceived(1) {
		t.Errorf("Expected offset 1 to not be marked as received")
	}
}

func TestOffsetTracker_WindowSliding(t *testing.T) {
	tracker := NewOffsetTracker(64)

	// Fill the window
	for i := uint64(0); i < 64; i++ {
		tracker.MarkReceived(i)
	}

	// Add offset beyond window to trigger sliding
	tracker.MarkReceived(100)

	// Old offsets should no longer be tracked
	if tracker.IsReceived(0) {
		t.Errorf("Expected offset 0 to be outside window after sliding")
	}

	// New offset should be tracked
	if !tracker.IsReceived(100) {
		t.Errorf("Expected offset 100 to be tracked after sliding")
	}
}

func TestOffsetTracker_GapDetection(t *testing.T) {
	tracker := NewOffsetTracker(64)

	// Mark some offsets as received with gaps
	tracker.MarkReceived(0)
	tracker.MarkReceived(1)
	// Gap: 2, 3
	tracker.MarkReceived(4)
	tracker.MarkReceived(5)
	// Gap: 6
	tracker.MarkReceived(7)

	gaps := tracker.GetGaps(8)

	expectedGaps := []PerformanceOffsetRange{
		{Start: 2, End: 3},
		{Start: 6, End: 6},
	}

	if len(gaps) != len(expectedGaps) {
		t.Errorf("Expected %d gaps, got %d", len(expectedGaps), len(gaps))
	}

	for i, gap := range gaps {
		if i < len(expectedGaps) {
			if gap.Start != expectedGaps[i].Start || gap.End != expectedGaps[i].End {
				t.Errorf("Gap %d: expected [%d-%d], got [%d-%d]",
					i, expectedGaps[i].Start, expectedGaps[i].End, gap.Start, gap.End)
			}
		}
	}
}

func TestMemoryPool_BasicOperations(t *testing.T) {
	pool := NewMemoryPool()

	// Test getting buffers of different sizes
	buf1 := pool.Get(100)
	if len(buf1) != 100 {
		t.Errorf("Expected buffer length 100, got %d", len(buf1))
	}

	buf2 := pool.Get(200)
	if len(buf2) != 200 {
		t.Errorf("Expected buffer length 200, got %d", len(buf2))
	}

	// Test returning buffers
	pool.Put(buf1)
	pool.Put(buf2)

	// Test reusing buffers
	buf3 := pool.Get(100)
	if cap(buf3) < 100 {
		t.Errorf("Expected buffer capacity >= 100, got %d", cap(buf3))
	}
}

func TestMemoryPool_PowerOf2Sizing(t *testing.T) {
	pool := NewMemoryPool()

	// Test that buffers are allocated in power-of-2 sizes
	buf := pool.Get(100)
	expectedCap := 128 // Next power of 2 after 100
	if cap(buf) != expectedCap {
		t.Errorf("Expected buffer capacity %d, got %d", expectedCap, cap(buf))
	}

	pool.Put(buf)
}

func TestBatchProcessor_BasicProcessing(t *testing.T) {
	var processedBatches []int
	var mutex sync.Mutex

	processor := func(frames []*DataFrame) error {
		mutex.Lock()
		processedBatches = append(processedBatches, len(frames))
		mutex.Unlock()
		return nil
	}

	bp := NewBatchProcessor(3, 50*time.Millisecond, processor)
	defer bp.Close()

	// Add frames to trigger batch processing
	for i := 0; i < 7; i++ {
		frame := &DataFrame{
			StreamID:    1,
			PathID:      "path1",
			Data:        []byte(fmt.Sprintf("frame%d", i)),
			SequenceNum: uint64(i),
			Timestamp:   time.Now(),
		}
		err := bp.AddFrame(frame)
		if err != nil {
			t.Fatalf("Failed to add frame %d: %v", i, err)
		}
	}

	// Wait for processing
	time.Sleep(100 * time.Millisecond)

	mutex.Lock()
	defer mutex.Unlock()

	// Should have processed at least 1 batch
	if len(processedBatches) < 1 {
		t.Errorf("Expected at least 1 batch, got %d", len(processedBatches))
	}

	// Total frames processed should be 7
	totalFrames := 0
	for _, batchSize := range processedBatches {
		totalFrames += batchSize
	}
	
	if totalFrames != 7 {
		t.Errorf("Expected 7 total frames processed, got %d", totalFrames)
	}

	t.Logf("Processed %d batches with sizes: %v", len(processedBatches), processedBatches)
}

func TestBatchProcessor_TimeoutProcessing(t *testing.T) {
	var processedBatches []int
	var mutex sync.Mutex

	processor := func(frames []*DataFrame) error {
		mutex.Lock()
		processedBatches = append(processedBatches, len(frames))
		mutex.Unlock()
		return nil
	}

	bp := NewBatchProcessor(10, 30*time.Millisecond, processor)
	defer bp.Close()

	// Add only 2 frames (less than batch size)
	for i := 0; i < 2; i++ {
		frame := &DataFrame{
			StreamID:    1,
			PathID:      "path1",
			Data:        []byte(fmt.Sprintf("frame%d", i)),
			SequenceNum: uint64(i),
			Timestamp:   time.Now(),
		}
		err := bp.AddFrame(frame)
		if err != nil {
			t.Fatalf("Failed to add frame %d: %v", i, err)
		}
	}

	// Wait for timeout processing
	time.Sleep(50 * time.Millisecond)

	mutex.Lock()
	defer mutex.Unlock()

	// Should have processed 1 batch due to timeout
	if len(processedBatches) != 1 {
		t.Errorf("Expected 1 batch due to timeout, got %d", len(processedBatches))
	}

	if len(processedBatches) > 0 && processedBatches[0] != 2 {
		t.Errorf("Expected batch size 2, got %d", processedBatches[0])
	}
}

func TestPerformanceOptimizedAggregator_BatchProcessing(t *testing.T) {
	logger := &MockLogger{}
	poa := NewPerformanceOptimizedAggregator(logger)
	defer poa.Close()

	streamID := uint64(1)
	err := poa.CreateAggregatedStream(streamID)
	if err != nil {
		t.Fatalf("Failed to create aggregated stream: %v", err)
	}

	// Create multiple frames for batch processing
	frames := make([]*DataFrame, 5)
	for i := 0; i < 5; i++ {
		frames[i] = &DataFrame{
			StreamID:    streamID,
			PathID:      "path1",
			Data:        []byte(fmt.Sprintf("frame%d", i)),
			Offset:      uint64(i * 10),
			SequenceNum: uint64(i),
			Timestamp:   time.Now(),
		}
	}

	// Process frames as a batch
	err = poa.ProcessFrameBatch(frames)
	if err != nil {
		t.Fatalf("Failed to process frame batch: %v", err)
	}

	// Check metrics
	metrics := poa.GetMetrics()
	if metrics.TotalFramesProcessed != 5 {
		t.Errorf("Expected 5 frames processed, got %d", metrics.TotalFramesProcessed)
	}

	if metrics.BatchesProcessed != 1 {
		t.Errorf("Expected 1 batch processed, got %d", metrics.BatchesProcessed)
	}
}

func TestPerformanceOptimizedAggregator_MemoryPool(t *testing.T) {
	logger := &MockLogger{}
	poa := NewPerformanceOptimizedAggregator(logger)
	defer poa.Close()

	// Test memory pool operations
	buf1 := poa.GetOptimizedBuffer(100)
	if len(buf1) != 100 {
		t.Errorf("Expected buffer length 100, got %d", len(buf1))
	}

	buf2 := poa.GetOptimizedBuffer(200)
	if len(buf2) != 200 {
		t.Errorf("Expected buffer length 200, got %d", len(buf2))
	}

	// Return buffers
	poa.PutOptimizedBuffer(buf1)
	poa.PutOptimizedBuffer(buf2)

	// Check metrics
	metrics := poa.GetMetrics()
	if metrics.MemoryAllocations != 2 {
		t.Errorf("Expected 2 memory allocations, got %d", metrics.MemoryAllocations)
	}

	if metrics.MemoryDeallocations != 2 {
		t.Errorf("Expected 2 memory deallocations, got %d", metrics.MemoryDeallocations)
	}
}

// Benchmark tests for performance optimization

func BenchmarkOffsetTracker_MarkReceived(b *testing.B) {
	tracker := NewOffsetTracker(1024)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tracker.MarkReceived(uint64(i % 1024))
	}
}

func BenchmarkOffsetTracker_IsReceived(b *testing.B) {
	tracker := NewOffsetTracker(1024)

	// Pre-populate tracker
	for i := uint64(0); i < 1024; i++ {
		tracker.MarkReceived(i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tracker.IsReceived(uint64(i % 1024))
	}
}

func BenchmarkMemoryPool_GetPut(b *testing.B) {
	pool := NewMemoryPool()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf := pool.Get(1024)
		pool.Put(buf)
	}
}

func BenchmarkMemoryPool_vs_MakeSlice(b *testing.B) {
	b.Run("MemoryPool", func(b *testing.B) {
		pool := NewMemoryPool()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			buf := pool.Get(1024)
			pool.Put(buf)
		}
	})

	b.Run("MakeSlice", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = make([]byte, 1024)
		}
	})
}

func BenchmarkAggregator_SingleFrame_vs_Batch(b *testing.B) {
	logger := &MockLogger{}

	b.Run("SingleFrame", func(b *testing.B) {
		da := NewDataAggregator(logger).(*DataAggregatorImpl)
		streamID := uint64(1)
		da.CreateAggregatedStream(streamID)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			frame := &DataFrame{
				StreamID:    streamID,
				PathID:      "path1",
				Data:        make([]byte, 100),
				Offset:      uint64(i * 100),
				SequenceNum: uint64(i),
				Timestamp:   time.Now(),
			}
			da.ProcessFrameWithReordering(streamID, frame)
		}
	})

	b.Run("BatchProcessing", func(b *testing.B) {
		poa := NewPerformanceOptimizedAggregator(logger)
		defer poa.Close()
		streamID := uint64(1)
		poa.CreateAggregatedStream(streamID)

		batchSize := 10

		b.ResetTimer()
		for i := 0; i < b.N; i += batchSize {
			// Prepare batch
			actualBatchSize := batchSize
			if i+batchSize > b.N {
				actualBatchSize = b.N - i
			}
			
			currentBatch := make([]*DataFrame, actualBatchSize)
			for j := 0; j < actualBatchSize; j++ {
				currentBatch[j] = &DataFrame{
					StreamID:    streamID,
					PathID:      "path1",
					Data:        make([]byte, 100),
					Offset:      uint64((i + j) * 100),
					SequenceNum: uint64(i + j),
					Timestamp:   time.Now(),
				}
			}
			poa.ProcessFrameBatch(currentBatch)
		}
	})
}

func BenchmarkAggregator_ConcurrentProcessing(b *testing.B) {
	logger := &MockLogger{}
	poa := NewPerformanceOptimizedAggregator(logger)
	defer poa.Close()

	streamID := uint64(1)
	poa.CreateAggregatedStream(streamID)

	numWorkers := runtime.NumCPU()
	framesPerWorker := b.N / numWorkers

	b.ResetTimer()

	var wg sync.WaitGroup
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < framesPerWorker; i++ {
				frame := &DataFrame{
					StreamID:    streamID,
					PathID:      fmt.Sprintf("path%d", workerID),
					Data:        make([]byte, 100),
					Offset:      uint64((workerID*framesPerWorker + i) * 100),
					SequenceNum: uint64(workerID*framesPerWorker + i),
					Timestamp:   time.Now(),
				}
				poa.ProcessFrameWithReordering(streamID, frame)
			}
		}(w)
	}

	wg.Wait()
}

func BenchmarkAggregator_LargeFrames(b *testing.B) {
	logger := &MockLogger{}
	poa := NewPerformanceOptimizedAggregator(logger)
	defer poa.Close()

	streamID := uint64(1)
	poa.CreateAggregatedStream(streamID)

	frameSizes := []int{1024, 4096, 16384, 65536} // 1KB, 4KB, 16KB, 64KB

	for _, size := range frameSizes {
		b.Run(fmt.Sprintf("FrameSize_%dB", size), func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				frame := &DataFrame{
					StreamID:    streamID,
					PathID:      "path1",
					Data:        make([]byte, size),
					Offset:      uint64(i * size),
					SequenceNum: uint64(i),
					Timestamp:   time.Now(),
				}
				poa.ProcessFrameWithReordering(streamID, frame)
			}
		})
	}
}

func BenchmarkAggregator_MemoryUsage(b *testing.B) {
	logger := &MockLogger{}
	poa := NewPerformanceOptimizedAggregator(logger)
	defer poa.Close()

	streamID := uint64(1)
	poa.CreateAggregatedStream(streamID)

	// Measure memory usage
	var m1, m2 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m1)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		frame := &DataFrame{
			StreamID:    streamID,
			PathID:      "path1",
			Data:        make([]byte, 1024),
			Offset:      uint64(i * 1024),
			SequenceNum: uint64(i),
			Timestamp:   time.Now(),
		}
		poa.ProcessFrameWithReordering(streamID, frame)
	}

	runtime.GC()
	runtime.ReadMemStats(&m2)

	b.ReportMetric(float64(m2.Alloc-m1.Alloc)/float64(b.N), "bytes/op")
	b.ReportMetric(float64(m2.Mallocs-m1.Mallocs)/float64(b.N), "allocs/op")
}

// Performance regression tests

func TestPerformanceRegression_ProcessingLatency(t *testing.T) {
	logger := &MockLogger{}
	poa := NewPerformanceOptimizedAggregator(logger)
	defer poa.Close()

	streamID := uint64(1)
	poa.CreateAggregatedStream(streamID)

	// Process frames and measure latency
	const numFrames = 1000
	start := time.Now()

	for i := 0; i < numFrames; i++ {
		frame := &DataFrame{
			StreamID:    streamID,
			PathID:      "path1",
			Data:        make([]byte, 1024),
			Offset:      uint64(i * 1024),
			SequenceNum: uint64(i),
			Timestamp:   time.Now(),
		}
		_, err := poa.ProcessFrameWithReordering(streamID, frame)
		if err != nil {
			t.Fatalf("Failed to process frame %d: %v", i, err)
		}
	}

	totalTime := time.Since(start)
	avgLatency := totalTime / numFrames

	// Assert reasonable performance (adjust threshold as needed)
	maxExpectedLatency := 100 * time.Microsecond
	if avgLatency > maxExpectedLatency {
		t.Errorf("Average processing latency %v exceeds threshold %v", avgLatency, maxExpectedLatency)
	}

	t.Logf("Processed %d frames in %v (avg: %v per frame)", numFrames, totalTime, avgLatency)
}

func TestPerformanceRegression_MemoryEfficiency(t *testing.T) {
	logger := &MockLogger{}
	poa := NewPerformanceOptimizedAggregator(logger)
	defer poa.Close()

	streamID := uint64(1)
	poa.CreateAggregatedStream(streamID)

	// Measure memory before processing
	var m1, m2 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m1)

	// Process frames
	const numFrames = 1000
	for i := 0; i < numFrames; i++ {
		frame := &DataFrame{
			StreamID:    streamID,
			PathID:      "path1",
			Data:        make([]byte, 1024),
			Offset:      uint64(i * 1024),
			SequenceNum: uint64(i),
			Timestamp:   time.Now(),
		}
		poa.ProcessFrameWithReordering(streamID, frame)
	}

	runtime.GC()
	runtime.ReadMemStats(&m2)

	// Calculate memory usage
	memoryUsed := m2.Alloc - m1.Alloc
	memoryPerFrame := memoryUsed / numFrames

	// Assert reasonable memory usage (adjust threshold as needed)
	maxExpectedMemoryPerFrame := uint64(2048) // 2KB per frame
	if memoryPerFrame > maxExpectedMemoryPerFrame {
		t.Errorf("Memory usage per frame %d bytes exceeds threshold %d bytes", 
			memoryPerFrame, maxExpectedMemoryPerFrame)
	}

	t.Logf("Memory usage: %d bytes total, %d bytes per frame", memoryUsed, memoryPerFrame)
}

// Helper function for testing
func createTestFrames(streamID uint64, count int, dataSize int) []*DataFrame {
	frames := make([]*DataFrame, count)
	for i := 0; i < count; i++ {
		frames[i] = &DataFrame{
			StreamID:    streamID,
			PathID:      "path1",
			Data:        make([]byte, dataSize),
			Offset:      uint64(i * dataSize),
			SequenceNum: uint64(i),
			Timestamp:   time.Now(),
		}
	}
	return frames
}