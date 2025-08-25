package data

import (
	"container/heap"
	"fmt"
	"sort"
	"sync"
	"time"

	"kwik/internal/utils"
	datapb "kwik/proto/data"
)

// DataReorderingManager handles reordering of out-of-order data from multiple paths
// and reassembly using KWIK logical offsets. Implements Requirements 10.2, 10.4
type DataReorderingManager struct {
	// Stream reordering contexts
	streamContexts map[uint64]*ReorderingContext
	contextMutex   sync.RWMutex

	// Offset manager for logical offset handling
	offsetManager *OffsetManager

	// Configuration
	config *ReorderingConfig

	// Statistics
	stats *ReorderingStats

	// Background processing
	processingQueue chan *ReorderingTask
	stopChan        chan struct{}
	wg              sync.WaitGroup
}

// ReorderingContext maintains reordering state for a logical stream
type ReorderingContext struct {
	LogicalStreamID uint64
	
	// Frame buffer for out-of-order frames
	frameBuffer *FrameBuffer
	
	// Reassembly state
	reassemblyState *ReassemblyState
	
	// Path tracking
	pathContributions map[string]*ReorderingPathContribution
	
	// Flow control
	windowSize      uint64
	windowUsed      uint64
	
	// Timing
	lastActivity    time.Time
	createdAt       time.Time
	
	// Synchronization
	mutex           sync.RWMutex
}

// FrameBuffer manages buffering of out-of-order frames
type FrameBuffer struct {
	frames          []*BufferedFrame
	maxSize         int
	currentSize     int
	nextExpectedOffset uint64
	mutex           sync.RWMutex
}

// BufferedFrame represents a frame waiting for reordering
type BufferedFrame struct {
	Frame       *datapb.DataFrame
	ReceivedAt  time.Time
	PathID      string
	Priority    int
	
	// Heap interface implementation
	index       int
}

// ReassemblyState tracks the state of data reassembly
type ReassemblyState struct {
	// Reassembled data buffer
	reassembledData []byte
	
	// Offset tracking
	nextOffset      uint64
	maxOffset       uint64
	
	// Gap tracking
	gaps            []OffsetGap
	
	// Completion tracking
	finalFrameReceived bool
	totalExpectedBytes uint64
	
	mutex           sync.RWMutex
}

// ReorderingPathContribution tracks contribution from each path for reordering
type ReorderingPathContribution struct {
	PathID          string
	BytesReceived   uint64
	FramesReceived  uint64
	LastFrameTime   time.Time
	AverageLatency  time.Duration
	OutOfOrderCount uint64
}

// ReorderingConfig contains configuration for data reordering
type ReorderingConfig struct {
	// Buffer limits
	MaxFrameBufferSize    int
	MaxReassemblyBuffer   int
	MaxConcurrentStreams  int
	
	// Timing parameters
	ReorderingTimeout     time.Duration
	ReassemblyTimeout     time.Duration
	MaxWaitTime           time.Duration
	
	// Processing parameters
	ProcessingWorkers     int
	BatchSize             int
	
	// Gap handling
	MaxGaps               int
	GapFillTimeout        time.Duration
}

// ReorderingTask represents a task for background processing
type ReorderingTask struct {
	Type            ReorderingTaskType
	LogicalStreamID uint64
	Frame           *datapb.DataFrame
	Context         *ReorderingContext
}

// ReorderingTaskType defines types of reordering tasks
type ReorderingTaskType int

const (
	TaskTypeProcessFrame ReorderingTaskType = iota
	TaskTypeReassemble
	TaskTypeCleanup
	TaskTypeGapFill
)

// ReorderingStats contains statistics about reordering operations
type ReorderingStats struct {
	TotalFramesProcessed   uint64
	FramesReordered        uint64
	FramesDropped          uint64
	ReassemblyOperations   uint64
	AverageReorderingTime  time.Duration
	PathContributions      map[string]*PathContribution
	
	mutex                  sync.RWMutex
}

// NewDataReorderingManager creates a new data reordering manager
func NewDataReorderingManager(offsetManager *OffsetManager, config *ReorderingConfig) *DataReorderingManager {
	if config == nil {
		config = DefaultReorderingConfig()
	}

	drm := &DataReorderingManager{
		streamContexts:  make(map[uint64]*ReorderingContext),
		offsetManager:   offsetManager,
		config:          config,
		stats:           NewReorderingStats(),
		processingQueue: make(chan *ReorderingTask, config.BatchSize*10),
		stopChan:        make(chan struct{}),
	}

	// Start background workers
	for i := 0; i < config.ProcessingWorkers; i++ {
		drm.wg.Add(1)
		go drm.processingWorker()
	}

	return drm
}

// DefaultReorderingConfig returns default reordering configuration
func DefaultReorderingConfig() *ReorderingConfig {
	return &ReorderingConfig{
		MaxFrameBufferSize:   1000,
		MaxReassemblyBuffer:  1024 * 1024, // 1MB
		MaxConcurrentStreams: 1000,
		ReorderingTimeout:    100 * time.Millisecond,
		ReassemblyTimeout:    500 * time.Millisecond,
		MaxWaitTime:          1 * time.Second,
		ProcessingWorkers:    4,
		BatchSize:            10,
		MaxGaps:              100,
		GapFillTimeout:       200 * time.Millisecond,
	}
}

// ProcessIncomingFrame processes an incoming frame for reordering
// Implements Requirement 10.2: reordering logic for out-of-order data from multiple paths
func (drm *DataReorderingManager) ProcessIncomingFrame(frame *datapb.DataFrame) error {
	if frame == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "frame is nil", nil)
	}

	startTime := time.Now()
	logicalStreamID := frame.LogicalStreamId

	// Get or create reordering context
	context, err := drm.getOrCreateContext(logicalStreamID)
	if err != nil {
		return err
	}

	// Update path contribution statistics
	drm.updatePathContribution(frame.PathId, frame)

	// Check if frame is in order
	if drm.isFrameInOrder(context, frame) {
		// Process in-order frame immediately
		err = drm.processInOrderFrame(context, frame)
		if err != nil {
			return err
		}
		
		// Try to process any buffered frames that are now in order
		drm.processBufferedFrames(context)
	} else {
		// Buffer out-of-order frame
		err = drm.bufferOutOfOrderFrame(context, frame)
		if err != nil {
			return err
		}
		
		// Schedule reordering task
		task := &ReorderingTask{
			Type:            TaskTypeProcessFrame,
			LogicalStreamID: logicalStreamID,
			Frame:           frame,
			Context:         context,
		}
		
		select {
		case drm.processingQueue <- task:
		default:
			// Queue is full, process synchronously
			drm.processReorderingTask(task)
		}
	}

	// Update statistics
	drm.updateProcessingStats(time.Since(startTime))

	return nil
}

// getOrCreateContext gets or creates a reordering context for a stream
func (drm *DataReorderingManager) getOrCreateContext(logicalStreamID uint64) (*ReorderingContext, error) {
	drm.contextMutex.RLock()
	context, exists := drm.streamContexts[logicalStreamID]
	drm.contextMutex.RUnlock()

	if exists {
		return context, nil
	}

	// Create new context
	drm.contextMutex.Lock()
	defer drm.contextMutex.Unlock()

	// Double-check after acquiring write lock
	if context, exists := drm.streamContexts[logicalStreamID]; exists {
		return context, nil
	}

	// Check limits
	if len(drm.streamContexts) >= drm.config.MaxConcurrentStreams {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"maximum concurrent streams limit reached", nil)
	}

	// Create new context
	context = &ReorderingContext{
		LogicalStreamID: logicalStreamID,
		frameBuffer:     NewFrameBuffer(drm.config.MaxFrameBufferSize),
		reassemblyState: NewReassemblyState(drm.config.MaxReassemblyBuffer),
		pathContributions: make(map[string]*ReorderingPathContribution),
		windowSize:      65536, // 64KB initial window
		windowUsed:      0,
		lastActivity:    time.Now(),
		createdAt:       time.Now(),
	}

	drm.streamContexts[logicalStreamID] = context

	return context, nil
}

// isFrameInOrder checks if a frame is in the expected order
func (drm *DataReorderingManager) isFrameInOrder(context *ReorderingContext, frame *datapb.DataFrame) bool {
	context.reassemblyState.mutex.RLock()
	defer context.reassemblyState.mutex.RUnlock()

	return frame.Offset == context.reassemblyState.nextOffset
}

// processInOrderFrame processes a frame that is in the expected order
func (drm *DataReorderingManager) processInOrderFrame(context *ReorderingContext, frame *datapb.DataFrame) error {
	context.reassemblyState.mutex.Lock()
	defer context.reassemblyState.mutex.Unlock()

	// Append data to reassembled buffer
	context.reassemblyState.reassembledData = append(context.reassemblyState.reassembledData, frame.Data...)
	
	// Update offset tracking
	context.reassemblyState.nextOffset = frame.Offset + uint64(len(frame.Data))
	if context.reassemblyState.nextOffset > context.reassemblyState.maxOffset {
		context.reassemblyState.maxOffset = context.reassemblyState.nextOffset
	}

	// Check if this is the final frame
	if frame.Fin {
		context.reassemblyState.finalFrameReceived = true
		context.reassemblyState.totalExpectedBytes = context.reassemblyState.nextOffset
	}

	// Update activity timestamp
	context.lastActivity = time.Now()

	return nil
}

// bufferOutOfOrderFrame buffers a frame that is out of order
func (drm *DataReorderingManager) bufferOutOfOrderFrame(context *ReorderingContext, frame *datapb.DataFrame) error {
	bufferedFrame := &BufferedFrame{
		Frame:      frame,
		ReceivedAt: time.Now(),
		PathID:     frame.PathId,
		Priority:   drm.calculateFramePriority(frame),
	}

	return context.frameBuffer.AddFrame(bufferedFrame)
}

// processBufferedFrames processes buffered frames that are now in order
func (drm *DataReorderingManager) processBufferedFrames(context *ReorderingContext) {
	for {
		frame := context.frameBuffer.GetNextInOrderFrame(context.reassemblyState.nextOffset)
		if frame == nil {
			break
		}

		err := drm.processInOrderFrame(context, frame.Frame)
		if err != nil {
			// Log error but continue processing
			continue
		}

		// Update statistics
		drm.stats.mutex.Lock()
		drm.stats.FramesReordered++
		drm.stats.mutex.Unlock()
	}
}

// calculateFramePriority calculates priority for frame processing
func (drm *DataReorderingManager) calculateFramePriority(frame *datapb.DataFrame) int {
	priority := 0

	// Higher priority for frames that fill gaps
	if frame.Fin {
		priority += 100 // Final frames have highest priority
	}

	// Priority based on data size (larger frames get higher priority)
	priority += len(frame.Data) / 100

	return priority
}

// ReassembleStreamData reassembles complete data for a stream
// Implements Requirement 10.4: reassembly system using KWIK logical offsets
func (drm *DataReorderingManager) ReassembleStreamData(logicalStreamID uint64) ([]byte, error) {
	drm.contextMutex.RLock()
	context, exists := drm.streamContexts[logicalStreamID]
	drm.contextMutex.RUnlock()

	if !exists {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			fmt.Sprintf("no reordering context for stream %d", logicalStreamID), nil)
	}

	// Try to process any remaining buffered frames first
	drm.processBufferedFrames(context)

	context.reassemblyState.mutex.RLock()
	defer context.reassemblyState.mutex.RUnlock()

	// Check if reassembly is complete
	if !context.reassemblyState.finalFrameReceived {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"stream reassembly not complete", nil)
	}

	// Check for gaps
	if len(context.reassemblyState.gaps) > 0 {
		return nil, utils.NewKwikError(utils.ErrOffsetMismatch,
			fmt.Sprintf("stream has %d gaps, cannot reassemble", len(context.reassemblyState.gaps)), nil)
	}

	// Return copy of reassembled data
	data := make([]byte, len(context.reassemblyState.reassembledData))
	copy(data, context.reassemblyState.reassembledData)

	// Update statistics
	drm.stats.mutex.Lock()
	drm.stats.ReassemblyOperations++
	drm.stats.mutex.Unlock()

	return data, nil
}

// GetPartialStreamData returns partial reassembled data for a stream
func (drm *DataReorderingManager) GetPartialStreamData(logicalStreamID uint64) ([]byte, error) {
	drm.contextMutex.RLock()
	context, exists := drm.streamContexts[logicalStreamID]
	drm.contextMutex.RUnlock()

	if !exists {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			fmt.Sprintf("no reordering context for stream %d", logicalStreamID), nil)
	}

	context.reassemblyState.mutex.RLock()
	defer context.reassemblyState.mutex.RUnlock()

	// Return copy of current reassembled data
	data := make([]byte, len(context.reassemblyState.reassembledData))
	copy(data, context.reassemblyState.reassembledData)

	return data, nil
}

// updatePathContribution updates statistics for path contribution
func (drm *DataReorderingManager) updatePathContribution(pathID string, frame *datapb.DataFrame) {
	drm.stats.mutex.Lock()
	defer drm.stats.mutex.Unlock()

	if drm.stats.PathContributions == nil {
		drm.stats.PathContributions = make(map[string]*PathContribution)
	}

	contribution, exists := drm.stats.PathContributions[pathID]
	if !exists {
		contribution = &PathContribution{
			PathID:           pathID,
			BytesReceived:    0,
			FramesReceived:   0,
			LastFrameTime:    time.Now(),
			LastFrameOffset:  0,
			IsActive:         true,
		}
		drm.stats.PathContributions[pathID] = contribution
	}

	contribution.BytesReceived += uint64(len(frame.Data))
	contribution.FramesReceived++
	contribution.LastFrameTime = time.Now()
	contribution.LastFrameOffset = frame.Offset
}

// updateProcessingStats updates processing statistics
func (drm *DataReorderingManager) updateProcessingStats(processingTime time.Duration) {
	drm.stats.mutex.Lock()
	defer drm.stats.mutex.Unlock()

	drm.stats.TotalFramesProcessed++

	if drm.stats.AverageReorderingTime == 0 {
		drm.stats.AverageReorderingTime = processingTime
	} else {
		drm.stats.AverageReorderingTime = (drm.stats.AverageReorderingTime + processingTime) / 2
	}
}

// processingWorker runs background processing tasks
func (drm *DataReorderingManager) processingWorker() {
	defer drm.wg.Done()

	for {
		select {
		case task := <-drm.processingQueue:
			drm.processReorderingTask(task)
		case <-drm.stopChan:
			return
		}
	}
}

// processReorderingTask processes a reordering task
func (drm *DataReorderingManager) processReorderingTask(task *ReorderingTask) {
	switch task.Type {
	case TaskTypeProcessFrame:
		drm.processBufferedFrames(task.Context)
	case TaskTypeReassemble:
		// Trigger reassembly for the stream
		drm.processBufferedFrames(task.Context)
	case TaskTypeCleanup:
		drm.cleanupExpiredContexts()
	case TaskTypeGapFill:
		drm.attemptGapFilling(task.Context)
	}
}

// attemptGapFilling attempts to fill gaps using buffered frames
func (drm *DataReorderingManager) attemptGapFilling(context *ReorderingContext) {
	context.reassemblyState.mutex.Lock()
	defer context.reassemblyState.mutex.Unlock()

	// Try to fill gaps with buffered frames
	for i, gap := range context.reassemblyState.gaps {
		frame := context.frameBuffer.GetFrameForOffset(uint64(gap.Start))
		if frame != nil {
			// Found a frame that can fill this gap
			if frame.Frame.Offset == uint64(gap.Start) {
				// Process the frame
				context.reassemblyState.reassembledData = append(context.reassemblyState.reassembledData, frame.Frame.Data...)
				
				// Update gap
				newStartOffset := gap.Start + int64(len(frame.Frame.Data))
				if newStartOffset >= gap.End {
					// Gap is completely filled, remove it
					context.reassemblyState.gaps = append(context.reassemblyState.gaps[:i], context.reassemblyState.gaps[i+1:]...)
				} else {
					// Gap is partially filled, update it
					context.reassemblyState.gaps[i].Start = newStartOffset
				}
				
				// Remove frame from buffer
				context.frameBuffer.RemoveFrame(frame)
			}
		}
	}
}

// cleanupExpiredContexts removes expired reordering contexts
func (drm *DataReorderingManager) cleanupExpiredContexts() {
	drm.contextMutex.Lock()
	defer drm.contextMutex.Unlock()

	now := time.Now()
	expiredThreshold := now.Add(-drm.config.ReassemblyTimeout)

	var expiredStreams []uint64
	for streamID, context := range drm.streamContexts {
		if context.lastActivity.Before(expiredThreshold) {
			expiredStreams = append(expiredStreams, streamID)
		}
	}

	// Remove expired contexts
	for _, streamID := range expiredStreams {
		delete(drm.streamContexts, streamID)
	}
}

// GetReorderingStats returns current reordering statistics
func (drm *DataReorderingManager) GetReorderingStats() *ReorderingStats {
	drm.stats.mutex.RLock()
	defer drm.stats.mutex.RUnlock()

	// Create a copy of the stats
	statsCopy := &ReorderingStats{
		TotalFramesProcessed:  drm.stats.TotalFramesProcessed,
		FramesReordered:       drm.stats.FramesReordered,
		FramesDropped:         drm.stats.FramesDropped,
		ReassemblyOperations:  drm.stats.ReassemblyOperations,
		AverageReorderingTime: drm.stats.AverageReorderingTime,
		PathContributions:     make(map[string]*PathContribution),
	}

	// Copy path contributions
	for pathID, contribution := range drm.stats.PathContributions {
		statsCopy.PathContributions[pathID] = &PathContribution{
			PathID:           contribution.PathID,
			BytesReceived:    contribution.BytesReceived,
			FramesReceived:   contribution.FramesReceived,
			LastFrameTime:    contribution.LastFrameTime,
			LastFrameOffset:  contribution.LastFrameOffset,
			IsActive:         contribution.IsActive,
		}
	}

	return statsCopy
}

// Close shuts down the data reordering manager
func (drm *DataReorderingManager) Close() error {
	close(drm.stopChan)
	drm.wg.Wait()
	close(drm.processingQueue)

	drm.contextMutex.Lock()
	defer drm.contextMutex.Unlock()

	// Clear all contexts
	drm.streamContexts = make(map[uint64]*ReorderingContext)

	return nil
}

// NewReorderingStats creates new reordering statistics
func NewReorderingStats() *ReorderingStats {
	return &ReorderingStats{
		PathContributions: make(map[string]*PathContribution),
	}
}
// NewFrameBuffer creates a new frame buffer
func NewFrameBuffer(maxSize int) *FrameBuffer {
	return &FrameBuffer{
		frames:             make([]*BufferedFrame, 0),
		maxSize:            maxSize,
		currentSize:        0,
		nextExpectedOffset: 0,
	}
}

// AddFrame adds a frame to the buffer
func (fb *FrameBuffer) AddFrame(frame *BufferedFrame) error {
	fb.mutex.Lock()
	defer fb.mutex.Unlock()

	if fb.currentSize >= fb.maxSize {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			"frame buffer is full", nil)
	}

	// Insert frame in sorted order by offset
	inserted := false
	for i, existingFrame := range fb.frames {
		if frame.Frame.Offset < existingFrame.Frame.Offset {
			// Insert before this frame
			fb.frames = append(fb.frames[:i], append([]*BufferedFrame{frame}, fb.frames[i:]...)...)
			inserted = true
			break
		}
	}

	if !inserted {
		fb.frames = append(fb.frames, frame)
	}

	fb.currentSize++
	return nil
}

// GetNextInOrderFrame returns the next frame that is in order
func (fb *FrameBuffer) GetNextInOrderFrame(expectedOffset uint64) *BufferedFrame {
	fb.mutex.Lock()
	defer fb.mutex.Unlock()

	for i, frame := range fb.frames {
		if frame.Frame.Offset == expectedOffset {
			// Remove frame from buffer
			fb.frames = append(fb.frames[:i], fb.frames[i+1:]...)
			fb.currentSize--
			return frame
		}
		if frame.Frame.Offset > expectedOffset {
			// No frame with the expected offset
			break
		}
	}

	return nil
}

// GetFrameForOffset returns a frame with the specified offset
func (fb *FrameBuffer) GetFrameForOffset(offset uint64) *BufferedFrame {
	fb.mutex.RLock()
	defer fb.mutex.RUnlock()

	for _, frame := range fb.frames {
		if frame.Frame.Offset == offset {
			return frame
		}
	}

	return nil
}

// RemoveFrame removes a frame from the buffer
func (fb *FrameBuffer) RemoveFrame(targetFrame *BufferedFrame) bool {
	fb.mutex.Lock()
	defer fb.mutex.Unlock()

	for i, frame := range fb.frames {
		if frame == targetFrame {
			fb.frames = append(fb.frames[:i], fb.frames[i+1:]...)
			fb.currentSize--
			return true
		}
	}

	return false
}

// GetBufferedFrameCount returns the number of buffered frames
func (fb *FrameBuffer) GetBufferedFrameCount() int {
	fb.mutex.RLock()
	defer fb.mutex.RUnlock()
	return fb.currentSize
}

// NewReassemblyState creates a new reassembly state
func NewReassemblyState(maxBufferSize int) *ReassemblyState {
	return &ReassemblyState{
		reassembledData:    make([]byte, 0, maxBufferSize),
		nextOffset:         0,
		maxOffset:          0,
		gaps:               make([]OffsetGap, 0),
		finalFrameReceived: false,
		totalExpectedBytes: 0,
	}
}

// AddGap adds a gap to the reassembly state
func (rs *ReassemblyState) AddGap(startOffset, endOffset uint64) {
	rs.mutex.Lock()
	defer rs.mutex.Unlock()

	gap := OffsetGap{
		Start: int64(startOffset),
		End:   int64(endOffset),
		Size:  int64(endOffset - startOffset),
	}

	// Insert gap in sorted order
	inserted := false
	for i, existingGap := range rs.gaps {
		if gap.Start < existingGap.Start {
			rs.gaps = append(rs.gaps[:i], append([]OffsetGap{gap}, rs.gaps[i:]...)...)
			inserted = true
			break
		}
	}

	if !inserted {
		rs.gaps = append(rs.gaps, gap)
	}
}

// FillGap attempts to fill a gap with data
func (rs *ReassemblyState) FillGap(offset uint64, data []byte) bool {
	rs.mutex.Lock()
	defer rs.mutex.Unlock()

	offsetInt := int64(offset)
	endOffsetInt := int64(offset + uint64(len(data)))

	for i, gap := range rs.gaps {
		if offsetInt >= gap.Start && offsetInt < gap.End {
			// This data can fill part of the gap
			
			if offsetInt == gap.Start && endOffsetInt >= gap.End {
				// Gap is completely filled
				rs.gaps = append(rs.gaps[:i], rs.gaps[i+1:]...)
			} else if offsetInt == gap.Start {
				// Gap is partially filled from the start
				rs.gaps[i].Start = endOffsetInt
				rs.gaps[i].Size = rs.gaps[i].End - rs.gaps[i].Start
			} else if endOffsetInt >= gap.End {
				// Gap is partially filled from the end
				rs.gaps[i].End = offsetInt
				rs.gaps[i].Size = rs.gaps[i].End - rs.gaps[i].Start
			} else {
				// Gap is split into two gaps
				newGap := OffsetGap{
					Start: endOffsetInt,
					End:   gap.End,
					Size:  gap.End - endOffsetInt,
				}
				rs.gaps[i].End = offsetInt
				rs.gaps[i].Size = rs.gaps[i].End - rs.gaps[i].Start
				rs.gaps = append(rs.gaps[:i+1], append([]OffsetGap{newGap}, rs.gaps[i+1:]...)...)
			}
			
			return true
		}
	}

	return false
}

// HasGaps returns true if there are gaps in the reassembly
func (rs *ReassemblyState) HasGaps() bool {
	rs.mutex.RLock()
	defer rs.mutex.RUnlock()
	return len(rs.gaps) > 0
}

// GetGapCount returns the number of gaps
func (rs *ReassemblyState) GetGapCount() int {
	rs.mutex.RLock()
	defer rs.mutex.RUnlock()
	return len(rs.gaps)
}

// IsComplete returns true if reassembly is complete
func (rs *ReassemblyState) IsComplete() bool {
	rs.mutex.RLock()
	defer rs.mutex.RUnlock()
	return rs.finalFrameReceived && len(rs.gaps) == 0
}

// GetProgress returns the reassembly progress as a percentage
func (rs *ReassemblyState) GetProgress() float64 {
	rs.mutex.RLock()
	defer rs.mutex.RUnlock()

	if rs.totalExpectedBytes == 0 {
		return 0.0
	}

	return float64(len(rs.reassembledData)) / float64(rs.totalExpectedBytes) * 100.0
}

// Heap interface implementation for BufferedFrame priority queue

// FramePriorityQueue implements a priority queue for buffered frames
type FramePriorityQueue []*BufferedFrame

func (pq FramePriorityQueue) Len() int { return len(pq) }

func (pq FramePriorityQueue) Less(i, j int) bool {
	// Higher priority frames come first
	if pq[i].Priority != pq[j].Priority {
		return pq[i].Priority > pq[j].Priority
	}
	// If priority is the same, earlier offsets come first
	return pq[i].Frame.Offset < pq[j].Frame.Offset
}

func (pq FramePriorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}

func (pq *FramePriorityQueue) Push(x interface{}) {
	n := len(*pq)
	frame := x.(*BufferedFrame)
	frame.index = n
	*pq = append(*pq, frame)
}

func (pq *FramePriorityQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	frame := old[n-1]
	old[n-1] = nil
	frame.index = -1
	*pq = old[0 : n-1]
	return frame
}

// PriorityFrameBuffer implements a priority-based frame buffer
type PriorityFrameBuffer struct {
	queue   FramePriorityQueue
	maxSize int
	mutex   sync.RWMutex
}

// NewPriorityFrameBuffer creates a new priority frame buffer
func NewPriorityFrameBuffer(maxSize int) *PriorityFrameBuffer {
	pfb := &PriorityFrameBuffer{
		queue:   make(FramePriorityQueue, 0),
		maxSize: maxSize,
	}
	heap.Init(&pfb.queue)
	return pfb
}

// AddFrame adds a frame to the priority buffer
func (pfb *PriorityFrameBuffer) AddFrame(frame *BufferedFrame) error {
	pfb.mutex.Lock()
	defer pfb.mutex.Unlock()

	if len(pfb.queue) >= pfb.maxSize {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			"priority frame buffer is full", nil)
	}

	heap.Push(&pfb.queue, frame)
	return nil
}

// GetHighestPriorityFrame returns the frame with the highest priority
func (pfb *PriorityFrameBuffer) GetHighestPriorityFrame() *BufferedFrame {
	pfb.mutex.Lock()
	defer pfb.mutex.Unlock()

	if len(pfb.queue) == 0 {
		return nil
	}

	return heap.Pop(&pfb.queue).(*BufferedFrame)
}

// GetFrameCount returns the number of frames in the buffer
func (pfb *PriorityFrameBuffer) GetFrameCount() int {
	pfb.mutex.RLock()
	defer pfb.mutex.RUnlock()
	return len(pfb.queue)
}

// Clear removes all frames from the buffer
func (pfb *PriorityFrameBuffer) Clear() {
	pfb.mutex.Lock()
	defer pfb.mutex.Unlock()
	pfb.queue = make(FramePriorityQueue, 0)
	heap.Init(&pfb.queue)
}

// StreamReassembler provides high-level reassembly operations
type StreamReassembler struct {
	reorderingManager *DataReorderingManager
	config            *ReassemblerConfig
}

// ReassemblerConfig contains configuration for stream reassembly
type ReassemblerConfig struct {
	EnablePriorityQueue bool
	MaxConcurrentReassemblies int
	ReassemblyWorkers int
}

// NewStreamReassembler creates a new stream reassembler
func NewStreamReassembler(reorderingManager *DataReorderingManager, config *ReassemblerConfig) *StreamReassembler {
	if config == nil {
		config = &ReassemblerConfig{
			EnablePriorityQueue:       true,
			MaxConcurrentReassemblies: 100,
			ReassemblyWorkers:         2,
		}
	}

	return &StreamReassembler{
		reorderingManager: reorderingManager,
		config:            config,
	}
}

// ReassembleStream reassembles a complete stream
func (sr *StreamReassembler) ReassembleStream(logicalStreamID uint64) ([]byte, error) {
	return sr.reorderingManager.ReassembleStreamData(logicalStreamID)
}

// GetStreamProgress returns the reassembly progress for a stream
func (sr *StreamReassembler) GetStreamProgress(logicalStreamID uint64) (float64, error) {
	context, exists := sr.reorderingManager.streamContexts[logicalStreamID]
	if !exists {
		return 0.0, utils.NewKwikError(utils.ErrStreamCreationFailed,
			fmt.Sprintf("no reordering context for stream %d", logicalStreamID), nil)
	}

	return context.reassemblyState.GetProgress(), nil
}

// GetActiveStreams returns the list of streams being reassembled
func (sr *StreamReassembler) GetActiveStreams() []uint64 {
	sr.reorderingManager.contextMutex.RLock()
	defer sr.reorderingManager.contextMutex.RUnlock()

	streams := make([]uint64, 0, len(sr.reorderingManager.streamContexts))
	for streamID := range sr.reorderingManager.streamContexts {
		streams = append(streams, streamID)
	}

	sort.Slice(streams, func(i, j int) bool {
		return streams[i] < streams[j]
	})

	return streams
}