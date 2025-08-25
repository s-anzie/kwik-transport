package data

import (
	"kwik/pkg/logger"
	"sync"
	"time"
)

// BatchProcessor handles batch processing of multiple data frames
type BatchProcessor struct {
	batchSize    int
	batchTimeout time.Duration
	frameQueue   chan *DataFrame
	batchQueue   chan []*DataFrame
	stopChan     chan struct{}
	wg           sync.WaitGroup
	processor    func([]*DataFrame) error
}

// NewBatchProcessor creates a new batch processor
func NewBatchProcessor(batchSize int, batchTimeout time.Duration, processor func([]*DataFrame) error) *BatchProcessor {
	bp := &BatchProcessor{
		batchSize:    batchSize,
		batchTimeout: batchTimeout,
		frameQueue:   make(chan *DataFrame, batchSize*2),
		batchQueue:   make(chan []*DataFrame, 10),
		stopChan:     make(chan struct{}),
		processor:    processor,
	}

	bp.wg.Add(2)
	go bp.batchCollector()
	go bp.batchWorker()

	return bp
}

// AddFrame adds a frame to the batch processor
func (bp *BatchProcessor) AddFrame(frame *DataFrame) error {
	select {
	case bp.frameQueue <- frame:
		return nil
	case <-bp.stopChan:
		return nil
	default:
		// Queue is full, process synchronously
		return bp.processor([]*DataFrame{frame})
	}
}

// batchCollector collects frames into batches
func (bp *BatchProcessor) batchCollector() {
	defer bp.wg.Done()

	var batch []*DataFrame
	timer := time.NewTimer(bp.batchTimeout)
	defer timer.Stop()

	for {
		select {
		case frame := <-bp.frameQueue:
			batch = append(batch, frame)

			if len(batch) >= bp.batchSize {
				// Send full batch
				select {
				case bp.batchQueue <- batch:
					batch = make([]*DataFrame, 0, bp.batchSize)
					timer.Reset(bp.batchTimeout)
				case <-bp.stopChan:
					return
				}
			}

		case <-timer.C:
			if len(batch) > 0 {
				// Send partial batch on timeout
				select {
				case bp.batchQueue <- batch:
					batch = make([]*DataFrame, 0, bp.batchSize)
				case <-bp.stopChan:
					return
				}
			}
			timer.Reset(bp.batchTimeout)

		case <-bp.stopChan:
			// Send remaining batch if any
			if len(batch) > 0 {
				select {
				case bp.batchQueue <- batch:
				default:
				}
			}
			return
		}
	}
}

// batchWorker processes batches
func (bp *BatchProcessor) batchWorker() {
	defer bp.wg.Done()

	for {
		select {
		case batch := <-bp.batchQueue:
			if err := bp.processor(batch); err != nil {
				// Log error but continue processing
				_ = err
			}
		case <-bp.stopChan:
			return
		}
	}
}

// Close shuts down the batch processor
func (bp *BatchProcessor) Close() error {
	close(bp.stopChan)
	bp.wg.Wait()
	close(bp.frameQueue)
	close(bp.batchQueue)
	return nil
}

// OffsetTracker provides efficient offset tracking using a bitmap
type OffsetTracker struct {
	bitmap       []uint64 // Bitmap for tracking received offsets
	baseOffset   uint64   // Base offset for the bitmap
	windowSize   uint64   // Size of the tracking window
	nextExpected uint64   // Next expected offset
	mutex        sync.RWMutex
}

// NewOffsetTracker creates a new offset tracker
func NewOffsetTracker(windowSize uint64) *OffsetTracker {
	bitmapSize := (windowSize + 63) / 64 // Round up to nearest 64-bit word
	return &OffsetTracker{
		bitmap:       make([]uint64, bitmapSize),
		baseOffset:   0,
		windowSize:   windowSize,
		nextExpected: 0,
	}
}

// MarkReceived marks an offset as received
func (ot *OffsetTracker) MarkReceived(offset uint64) bool {
	ot.mutex.Lock()
	defer ot.mutex.Unlock()

	// Check if offset is within tracking window
	if offset < ot.baseOffset || offset >= ot.baseOffset+ot.windowSize {
		// Offset is outside window, might need to slide window
		if offset >= ot.baseOffset+ot.windowSize {
			ot.slideWindow(offset)
		} else {
			// Offset is too old, ignore
			return false
		}
	}

	// Calculate bitmap position
	relativeOffset := offset - ot.baseOffset
	wordIndex := relativeOffset / 64
	bitIndex := relativeOffset % 64

	// Check if already marked
	if ot.bitmap[wordIndex]&(1<<bitIndex) != 0 {
		return false // Already received
	}

	// Mark as received
	ot.bitmap[wordIndex] |= 1 << bitIndex
	return true
}

// IsReceived checks if an offset has been received
func (ot *OffsetTracker) IsReceived(offset uint64) bool {
	ot.mutex.RLock()
	defer ot.mutex.RUnlock()

	if offset < ot.baseOffset || offset >= ot.baseOffset+ot.windowSize {
		return false
	}

	relativeOffset := offset - ot.baseOffset
	wordIndex := relativeOffset / 64
	bitIndex := relativeOffset % 64

	return ot.bitmap[wordIndex]&(1<<bitIndex) != 0
}

// GetNextExpected returns the next expected offset
func (ot *OffsetTracker) GetNextExpected() uint64 {
	ot.mutex.RLock()
	defer ot.mutex.RUnlock()
	return ot.nextExpected
}

// UpdateNextExpected updates the next expected offset
func (ot *OffsetTracker) UpdateNextExpected(offset uint64) {
	ot.mutex.Lock()
	defer ot.mutex.Unlock()

	if offset > ot.nextExpected {
		ot.nextExpected = offset
	}
}

// slideWindow slides the tracking window forward
func (ot *OffsetTracker) slideWindow(newOffset uint64) {
	// Calculate how much to slide
	slideAmount := newOffset - ot.baseOffset - ot.windowSize/2
	if slideAmount <= 0 {
		return
	}

	// Slide the bitmap
	slideWords := slideAmount / 64
	slideBits := slideAmount % 64

	if slideWords >= uint64(len(ot.bitmap)) {
		// Complete slide, clear all
		for i := range ot.bitmap {
			ot.bitmap[i] = 0
		}
	} else {
		// Partial slide
		for i := 0; i < len(ot.bitmap)-int(slideWords); i++ {
			ot.bitmap[i] = ot.bitmap[i+int(slideWords)]
		}
		// Clear the end
		for i := len(ot.bitmap) - int(slideWords); i < len(ot.bitmap); i++ {
			ot.bitmap[i] = 0
		}

		// Handle bit-level sliding if needed
		if slideBits > 0 {
			carry := uint64(0)
			for i := range ot.bitmap {
				newCarry := ot.bitmap[i] << (64 - slideBits)
				ot.bitmap[i] = (ot.bitmap[i] >> slideBits) | carry
				carry = newCarry
			}
		}
	}

	ot.baseOffset += slideAmount
}

// GetGaps returns a list of missing offset ranges
func (ot *OffsetTracker) GetGaps(maxOffset uint64) []PerformanceOffsetRange {
	ot.mutex.RLock()
	defer ot.mutex.RUnlock()

	var gaps []PerformanceOffsetRange
	currentGapStart := uint64(0)
	inGap := false

	endOffset := maxOffset
	if endOffset > ot.baseOffset+ot.windowSize {
		endOffset = ot.baseOffset + ot.windowSize
	}

	for offset := ot.baseOffset; offset < endOffset; offset++ {
		received := ot.IsReceived(offset)

		if !received && !inGap {
			// Start of a gap
			currentGapStart = offset
			inGap = true
		} else if received && inGap {
			// End of a gap
			gaps = append(gaps, PerformanceOffsetRange{
				Start: currentGapStart,
				End:   offset - 1,
			})
			inGap = false
		}
	}

	// Handle gap that extends to the end
	if inGap {
		gaps = append(gaps, PerformanceOffsetRange{
			Start: currentGapStart,
			End:   endOffset - 1,
		})
	}

	return gaps
}

// PerformanceOffsetRange represents a range of offsets for performance tracking
type PerformanceOffsetRange struct {
	Start uint64
	End   uint64
}

// MemoryPool provides efficient memory allocation for frame data
type MemoryPool struct {
	pools map[int]*sync.Pool // Size -> pool
	mutex sync.RWMutex
}

// NewMemoryPool creates a new memory pool
func NewMemoryPool() *MemoryPool {
	return &MemoryPool{
		pools: make(map[int]*sync.Pool),
	}
}

// Get gets a buffer of the specified size
func (mp *MemoryPool) Get(size int) []byte {
	// Round up to nearest power of 2 for better pooling
	poolSize := nextPowerOf2(size)

	mp.mutex.RLock()
	pool, exists := mp.pools[poolSize]
	mp.mutex.RUnlock()

	if !exists {
		mp.mutex.Lock()
		// Double-check after acquiring write lock
		if pool, exists = mp.pools[poolSize]; !exists {
			pool = &sync.Pool{
				New: func() interface{} {
					return make([]byte, poolSize)
				},
			}
			mp.pools[poolSize] = pool
		}
		mp.mutex.Unlock()
	}

	buf := pool.Get().([]byte)
	return buf[:size] // Return slice of requested size
}

// Put returns a buffer to the pool
func (mp *MemoryPool) Put(buf []byte) {
	if cap(buf) == 0 {
		return
	}

	poolSize := cap(buf)

	mp.mutex.RLock()
	pool, exists := mp.pools[poolSize]
	mp.mutex.RUnlock()

	if exists {
		// Reset the slice to full capacity before returning to pool
		buf = buf[:cap(buf)]
		pool.Put(buf)
	}
}

// nextPowerOf2 returns the next power of 2 greater than or equal to n
func nextPowerOf2(n int) int {
	if n <= 0 {
		return 1
	}

	// Handle powers of 2
	if n&(n-1) == 0 {
		return n
	}

	// Find next power of 2
	power := 1
	for power < n {
		power <<= 1
	}
	return power
}

// PerformanceOptimizedAggregator extends DataAggregatorImpl with performance optimizations
type PerformanceOptimizedAggregator struct {
	*DataAggregatorImpl

	// Performance optimizations
	batchProcessor *BatchProcessor
	offsetTracker  *OffsetTracker
	memoryPool     *MemoryPool

	// Performance metrics
	metrics *AggregatorMetrics
}

// AggregatorMetrics tracks performance metrics
type AggregatorMetrics struct {
	TotalFramesProcessed  uint64
	BatchesProcessed      uint64
	MemoryAllocations     uint64
	MemoryDeallocations   uint64
	AverageProcessingTime time.Duration
	PeakMemoryUsage       uint64

	mutex sync.RWMutex
}

// NewPerformanceOptimizedAggregator creates a performance-optimized aggregator
func NewPerformanceOptimizedAggregator(logger logger.Logger) *PerformanceOptimizedAggregator {
	baseAggregator := NewDataAggregator(logger).(*DataAggregatorImpl)

	poa := &PerformanceOptimizedAggregator{
		DataAggregatorImpl: baseAggregator,
		offsetTracker:      NewOffsetTracker(1024), // Track 1024 offsets
		memoryPool:         NewMemoryPool(),
		metrics:            &AggregatorMetrics{},
	}

	// Create batch processor
	poa.batchProcessor = NewBatchProcessor(
		10,                 // Batch size
		5*time.Millisecond, // Batch timeout
		poa.processBatch,   // Batch processor function
	)

	return poa
}

// ProcessFrameBatch processes multiple frames efficiently
func (poa *PerformanceOptimizedAggregator) ProcessFrameBatch(frames []*DataFrame) error {
	startTime := time.Now()
	defer func() {
		poa.updateMetrics(len(frames), time.Since(startTime))
	}()

	return poa.processBatch(frames)
}

// processBatch is the internal batch processing function
func (poa *PerformanceOptimizedAggregator) processBatch(frames []*DataFrame) error {
	// Group frames by stream ID for efficient processing
	streamFrames := make(map[uint64][]*DataFrame)

	for _, frame := range frames {
		if frame == nil {
			continue
		}
		streamFrames[frame.StreamID] = append(streamFrames[frame.StreamID], frame)
	}

	// Process each stream's frames
	for streamID, frames := range streamFrames {
		err := poa.processStreamFrames(streamID, frames)
		if err != nil {
			return err
		}
	}

	return nil
}

// processStreamFrames processes frames for a specific stream
func (poa *PerformanceOptimizedAggregator) processStreamFrames(streamID uint64, frames []*DataFrame) error {
	// Sort frames by sequence number for optimal processing
	for i := 0; i < len(frames)-1; i++ {
		for j := i + 1; j < len(frames); j++ {
			if frames[i].SequenceNum > frames[j].SequenceNum {
				frames[i], frames[j] = frames[j], frames[i]
			}
		}
	}

	// Process frames in order
	for _, frame := range frames {
		_, err := poa.DataAggregatorImpl.ProcessFrameWithReordering(streamID, frame)
		if err != nil {
			return err
		}
	}

	return nil
}

// GetOptimizedBuffer gets a buffer from the memory pool
func (poa *PerformanceOptimizedAggregator) GetOptimizedBuffer(size int) []byte {
	poa.metrics.mutex.Lock()
	poa.metrics.MemoryAllocations++
	poa.metrics.mutex.Unlock()

	return poa.memoryPool.Get(size)
}

// PutOptimizedBuffer returns a buffer to the memory pool
func (poa *PerformanceOptimizedAggregator) PutOptimizedBuffer(buf []byte) {
	poa.metrics.mutex.Lock()
	poa.metrics.MemoryDeallocations++
	poa.metrics.mutex.Unlock()

	poa.memoryPool.Put(buf)
}

// updateMetrics updates performance metrics
func (poa *PerformanceOptimizedAggregator) updateMetrics(frameCount int, processingTime time.Duration) {
	poa.metrics.mutex.Lock()
	defer poa.metrics.mutex.Unlock()

	poa.metrics.TotalFramesProcessed += uint64(frameCount)
	poa.metrics.BatchesProcessed++

	// Update average processing time
	if poa.metrics.AverageProcessingTime == 0 {
		poa.metrics.AverageProcessingTime = processingTime
	} else {
		poa.metrics.AverageProcessingTime = (poa.metrics.AverageProcessingTime + processingTime) / 2
	}
}

// GetMetrics returns current performance metrics
func (poa *PerformanceOptimizedAggregator) GetMetrics() *AggregatorMetrics {
	poa.metrics.mutex.RLock()
	defer poa.metrics.mutex.RUnlock()

	return &AggregatorMetrics{
		TotalFramesProcessed:  poa.metrics.TotalFramesProcessed,
		BatchesProcessed:      poa.metrics.BatchesProcessed,
		MemoryAllocations:     poa.metrics.MemoryAllocations,
		MemoryDeallocations:   poa.metrics.MemoryDeallocations,
		AverageProcessingTime: poa.metrics.AverageProcessingTime,
		PeakMemoryUsage:       poa.metrics.PeakMemoryUsage,
	}
}

// Close shuts down the performance-optimized aggregator
func (poa *PerformanceOptimizedAggregator) Close() error {
	if poa.batchProcessor != nil {
		poa.batchProcessor.Close()
	}
	return nil
}
