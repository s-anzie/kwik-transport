package data

import (
	"container/heap"
	"sort"
	"sync"
	"time"

	"kwik/internal/utils"
	datapb "kwik/proto/data"
)

// FlowSequencer handles efficient flow reconstitution and intelligent sequencing
// for outbound data flows. Implements Requirement 10.5
type FlowSequencer struct {
	// Flow management
	flows      map[uint64]*FlowContext
	flowsMutex sync.RWMutex

	// Sequencing queues
	outboundQueue *SequencingQueue

	// Configuration
	config *SequencerConfig

	// Statistics
	stats      *SequencerStats
	statsMutex sync.RWMutex

	// Background processing
	processingChan chan *SequencingTask
	stopChan       chan struct{}
	wg             sync.WaitGroup
}

// FlowContext maintains context for a data flow
type FlowContext struct {
	FlowID          uint64
	LogicalStreamID uint64
	PathID          string

	// Flow state
	nextSequenceNum uint64
	lastActivity    time.Time
	totalBytes      uint64
	totalFrames     uint64

	// Buffering
	inputBuffer  *FlowBuffer
	outputBuffer *FlowBuffer

	// Sequencing state
	sequencingState *SequencingState

	// Flow control
	windowSize uint64
	windowUsed uint64

	// Priority and QoS
	priority int
	qosClass QoSClass

	mutex sync.RWMutex
}

// FlowBuffer manages buffering for flow data
type FlowBuffer struct {
	data        []byte
	capacity    int
	readOffset  uint64
	writeOffset uint64
	mutex       sync.RWMutex
}

// SequencingState tracks the state of flow sequencing
type SequencingState struct {
	// Sequence tracking
	nextExpectedSeq  uint64
	maxSeqSeen       uint64
	outOfOrderFrames map[uint64]*datapb.DataFrame

	// Reconstitution state
	reconstitutedData      []byte
	reconstitutionComplete bool

	// Gap tracking
	gaps []SequenceGap

	mutex sync.RWMutex
}

// SequenceGap represents a gap in sequence numbers
type SequenceGap struct {
	StartSeq uint64
	EndSeq   uint64
}

// SequencingQueue manages prioritized sequencing of outbound flows
type SequencingQueue struct {
	queue SequencingHeap
	mutex sync.RWMutex
}

// SequencingTask represents a task for flow sequencing
type SequencingTask struct {
	Type     SequencingTaskType
	FlowID   uint64
	Frame    *datapb.DataFrame
	Priority int
	QoS      QoSClass
}

// SequencingTaskType defines types of sequencing tasks
type SequencingTaskType int

const (
	TaskTypeSequenceFrame SequencingTaskType = iota
	TaskTypeReconstituteFlow
	TaskTypeOptimizeSequencing
	TaskTypeFlowCleanup
)

// QoSClass defines Quality of Service classes for flows
type QoSClass int

const (
	QoSClassBestEffort QoSClass = iota
	QoSClassLowLatency
	QoSClassHighThroughput
	QoSClassRealTime
	QoSClassCritical
)

// SequencerConfig contains configuration for flow sequencing
type SequencerConfig struct {
	// Buffer management
	DefaultBufferSize  int
	MaxBufferSize      int
	BufferGrowthFactor float64

	// Sequencing parameters
	MaxOutOfOrderFrames int
	SequencingTimeout   time.Duration
	ReconstitutionDelay time.Duration

	// Performance tuning
	ProcessingWorkers    int
	BatchSize            int
	OptimizationInterval time.Duration

	// Flow control
	DefaultWindowSize uint64
	MaxWindowSize     uint64
	WindowUpdateRatio float64

	// QoS parameters
	EnableQoS         bool
	PriorityLevels    int
	LatencyThresholds map[QoSClass]time.Duration
}

// SequencerStats contains statistics about flow sequencing
type SequencerStats struct {
	TotalFlows           uint64
	ActiveFlows          int
	TotalFramesSequenced uint64
	FramesReordered      uint64
	FlowsReconstituted   uint64
	AverageLatency       time.Duration
	ThroughputMbps       float64
	QoSStats             map[QoSClass]*QoSStats
}

// QoSStats contains QoS-specific statistics
type QoSStats struct {
	FramesProcessed uint64
	AverageLatency  time.Duration
	ThroughputMbps  float64
	DroppedFrames   uint64
}

// NewFlowSequencer creates a new flow sequencer
func NewFlowSequencer(config *SequencerConfig) *FlowSequencer {
	if config == nil {
		config = DefaultSequencerConfig()
	}

	fs := &FlowSequencer{
		flows:          make(map[uint64]*FlowContext),
		outboundQueue:  NewSequencingQueue(),
		config:         config,
		stats:          NewSequencerStats(),
		processingChan: make(chan *SequencingTask, config.BatchSize*10),
		stopChan:       make(chan struct{}),
	}

	// Start processing workers
	for i := 0; i < config.ProcessingWorkers; i++ {
		fs.wg.Add(1)
		go fs.processingWorker()
	}

	// Start optimization routine
	if config.OptimizationInterval > 0 {
		fs.wg.Add(1)
		go fs.optimizationWorker()
	}

	return fs
}

// DefaultSequencerConfig returns default sequencer configuration
func DefaultSequencerConfig() *SequencerConfig {
	return &SequencerConfig{
		DefaultBufferSize:    64 * 1024,   // 64KB
		MaxBufferSize:        1024 * 1024, // 1MB
		BufferGrowthFactor:   1.5,
		MaxOutOfOrderFrames:  100,
		SequencingTimeout:    100 * time.Millisecond,
		ReconstitutionDelay:  50 * time.Millisecond,
		ProcessingWorkers:    4,
		BatchSize:            10,
		OptimizationInterval: 1 * time.Second,
		DefaultWindowSize:    65536,   // 64KB
		MaxWindowSize:        1048576, // 1MB
		WindowUpdateRatio:    0.5,
		EnableQoS:            true,
		PriorityLevels:       5,
		LatencyThresholds: map[QoSClass]time.Duration{
			QoSClassBestEffort:     100 * time.Millisecond,
			QoSClassLowLatency:     10 * time.Millisecond,
			QoSClassHighThroughput: 200 * time.Millisecond,
			QoSClassRealTime:       5 * time.Millisecond,
			QoSClassCritical:       1 * time.Millisecond,
		},
	}
}

// CreateFlow creates a new flow context for sequencing
func (fs *FlowSequencer) CreateFlow(flowID, logicalStreamID uint64, pathID string, qosClass QoSClass) (*FlowContext, error) {
	fs.flowsMutex.Lock()
	defer fs.flowsMutex.Unlock()

	// Check if flow already exists
	if _, exists := fs.flows[flowID]; exists {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"flow already exists", nil)
	}

	// Create flow context
	flowContext := &FlowContext{
		FlowID:          flowID,
		LogicalStreamID: logicalStreamID,
		PathID:          pathID,
		nextSequenceNum: 0,
		lastActivity:    time.Now(),
		totalBytes:      0,
		totalFrames:     0,
		inputBuffer:     NewFlowBuffer(fs.config.DefaultBufferSize),
		outputBuffer:    NewFlowBuffer(fs.config.DefaultBufferSize),
		sequencingState: NewSequencingState(),
		windowSize:      fs.config.DefaultWindowSize,
		windowUsed:      0,
		priority:        fs.calculatePriority(qosClass),
		qosClass:        qosClass,
	}

	fs.flows[flowID] = flowContext

	// Update statistics
	fs.statsMutex.Lock()
	fs.stats.TotalFlows++
	fs.stats.ActiveFlows++
	fs.statsMutex.Unlock()

	return flowContext, nil
}

// SequenceOutboundFrame sequences an outbound frame for transmission
// Implements intelligent sequencing for outbound data flows
func (fs *FlowSequencer) SequenceOutboundFrame(flowID uint64, frame *datapb.DataFrame) error {
	fs.flowsMutex.RLock()
	flowContext, exists := fs.flows[flowID]
	fs.flowsMutex.RUnlock()

	if !exists {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			"flow not found", nil)
	}

	// Assign sequence number synchronously
	flowContext.mutex.Lock()
	sequenceNum := flowContext.nextSequenceNum
	frame.FrameId = sequenceNum
	flowContext.nextSequenceNum++
	flowContext.lastActivity = time.Now()
	flowContext.totalFrames++
	flowContext.totalBytes += uint64(len(frame.Data))
	priority := flowContext.priority
	qosClass := flowContext.qosClass
	flowContext.mutex.Unlock()

	// Create sequencing task
	task := &SequencingTask{
		Type:     TaskTypeSequenceFrame,
		FlowID:   flowID,
		Frame:    frame,
		Priority: priority,
		QoS:      qosClass,
	}

	// Process the sequencing task immediately to ensure frame is queued
	err := fs.processSequencingTask(task)
	if err != nil {
		return err
	}

	// Also try to queue for background processing if channel has space
	select {
	case fs.processingChan <- task:
		// Successfully queued for background processing
	default:
		// Channel is full, but we already processed it synchronously above
	}

	return nil
}

// ReconstituteInboundFlow reconstitutes an inbound flow from received frames
// Implements efficient algorithms for data flow reconstitution
func (fs *FlowSequencer) ReconstituteInboundFlow(flowID uint64, frame *datapb.DataFrame) ([]byte, error) {
	fs.flowsMutex.RLock()
	flowContext, exists := fs.flows[flowID]
	fs.flowsMutex.RUnlock()

	if !exists {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"flow not found", nil)
	}

	// Process frame for reconstitution
	reconstitutedData, err := fs.processInboundFrame(flowContext, frame)
	if err != nil {
		return nil, err
	}

	// Update statistics
	fs.updateReconstitutionStats(flowContext.qosClass, len(reconstitutedData))

	return reconstitutedData, nil
}

// processInboundFrame processes an inbound frame for flow reconstitution
func (fs *FlowSequencer) processInboundFrame(flowContext *FlowContext, frame *datapb.DataFrame) ([]byte, error) {
	flowContext.sequencingState.mutex.Lock()
	defer flowContext.sequencingState.mutex.Unlock()

	sequenceNum := frame.FrameId

	// Check if frame is in order
	if sequenceNum == flowContext.sequencingState.nextExpectedSeq {
		// Frame is in order, add to reconstituted data
		flowContext.sequencingState.reconstitutedData = append(
			flowContext.sequencingState.reconstitutedData, frame.Data...)
		flowContext.sequencingState.nextExpectedSeq++

		// Process any buffered out-of-order frames that are now in order
		fs.processBufferedFrames(flowContext)

		// Return reconstituted data if available
		if len(flowContext.sequencingState.reconstitutedData) > 0 {
			data := make([]byte, len(flowContext.sequencingState.reconstitutedData))
			copy(data, flowContext.sequencingState.reconstitutedData)
			flowContext.sequencingState.reconstitutedData = flowContext.sequencingState.reconstitutedData[:0]
			return data, nil
		}
	} else if sequenceNum > flowContext.sequencingState.nextExpectedSeq {
		// Frame is out of order, buffer it
		if len(flowContext.sequencingState.outOfOrderFrames) < fs.config.MaxOutOfOrderFrames {
			flowContext.sequencingState.outOfOrderFrames[sequenceNum] = frame

			// Update max sequence seen
			if sequenceNum > flowContext.sequencingState.maxSeqSeen {
				flowContext.sequencingState.maxSeqSeen = sequenceNum
			}

			// Add gap if necessary
			fs.addSequenceGap(flowContext, flowContext.sequencingState.nextExpectedSeq, sequenceNum)
		} else {
			// Too many out-of-order frames, drop this one
			fs.statsMutex.Lock()
			if qosStats, exists := fs.stats.QoSStats[flowContext.qosClass]; exists {
				qosStats.DroppedFrames++
			}
			fs.statsMutex.Unlock()
		}
	}
	// Ignore frames with sequence numbers less than expected (duplicates or very old)

	return nil, nil
}

// processBufferedFrames processes buffered out-of-order frames
func (fs *FlowSequencer) processBufferedFrames(flowContext *FlowContext) {
	for {
		expectedSeq := flowContext.sequencingState.nextExpectedSeq
		frame, exists := flowContext.sequencingState.outOfOrderFrames[expectedSeq]
		if !exists {
			break
		}

		// Add frame data to reconstituted data
		flowContext.sequencingState.reconstitutedData = append(
			flowContext.sequencingState.reconstitutedData, frame.Data...)
		flowContext.sequencingState.nextExpectedSeq++

		// Remove from out-of-order buffer
		delete(flowContext.sequencingState.outOfOrderFrames, expectedSeq)

		// Update statistics
		fs.statsMutex.Lock()
		fs.stats.FramesReordered++
		fs.statsMutex.Unlock()
	}
}

// addSequenceGap adds a sequence gap to track missing frames
func (fs *FlowSequencer) addSequenceGap(flowContext *FlowContext, startSeq, endSeq uint64) {
	gap := SequenceGap{
		StartSeq: startSeq,
		EndSeq:   endSeq - 1,
	}

	// Insert gap in sorted order
	inserted := false
	for i, existingGap := range flowContext.sequencingState.gaps {
		if gap.StartSeq < existingGap.StartSeq {
			flowContext.sequencingState.gaps = append(
				flowContext.sequencingState.gaps[:i],
				append([]SequenceGap{gap}, flowContext.sequencingState.gaps[i:]...)...)
			inserted = true
			break
		}
	}

	if !inserted {
		flowContext.sequencingState.gaps = append(flowContext.sequencingState.gaps, gap)
	}
}

// calculatePriority calculates priority based on QoS class
func (fs *FlowSequencer) calculatePriority(qosClass QoSClass) int {
	switch qosClass {
	case QoSClassCritical:
		return 100
	case QoSClassRealTime:
		return 80
	case QoSClassLowLatency:
		return 60
	case QoSClassHighThroughput:
		return 40
	case QoSClassBestEffort:
		return 20
	default:
		return 20
	}
}

// processingWorker runs background processing tasks
func (fs *FlowSequencer) processingWorker() {
	defer fs.wg.Done()

	for {
		select {
		case task := <-fs.processingChan:
			fs.processSequencingTask(task)
		case <-fs.stopChan:
			return
		}
	}
}

// processSequencingTask processes a sequencing task
func (fs *FlowSequencer) processSequencingTask(task *SequencingTask) error {
	switch task.Type {
	case TaskTypeSequenceFrame:
		return fs.processOutboundFrame(task)
	case TaskTypeReconstituteFlow:
		return fs.processFlowReconstitution(task)
	case TaskTypeOptimizeSequencing:
		return fs.optimizeSequencing(task)
	case TaskTypeFlowCleanup:
		return fs.cleanupFlow(task)
	}
	return nil
}

// processOutboundFrame processes an outbound frame for sequencing
func (fs *FlowSequencer) processOutboundFrame(task *SequencingTask) error {
	// Add frame to outbound queue with priority
	fs.outboundQueue.Enqueue(&SequencedFrame{
		Frame:       task.Frame,
		Priority:    task.Priority,
		QoS:         task.QoS,
		EnqueueTime: time.Now(),
	})

	// Update statistics
	fs.statsMutex.Lock()
	fs.stats.TotalFramesSequenced++
	if qosStats, exists := fs.stats.QoSStats[task.QoS]; exists {
		qosStats.FramesProcessed++
	}
	fs.statsMutex.Unlock()

	return nil
}

// processFlowReconstitution processes flow reconstitution
func (fs *FlowSequencer) processFlowReconstitution(task *SequencingTask) error {
	// TODO: Implement advanced flow reconstitution logic
	return nil
}

// optimizeSequencing optimizes the sequencing algorithm
func (fs *FlowSequencer) optimizeSequencing(task *SequencingTask) error {
	// TODO: Implement sequencing optimization
	return nil
}

// cleanupFlow cleans up a completed flow
func (fs *FlowSequencer) cleanupFlow(task *SequencingTask) error {
	fs.flowsMutex.Lock()
	defer fs.flowsMutex.Unlock()

	if _, exists := fs.flows[task.FlowID]; exists {
		delete(fs.flows, task.FlowID)

		fs.statsMutex.Lock()
		fs.stats.ActiveFlows--
		fs.statsMutex.Unlock()
	}

	return nil
}

// optimizationWorker runs periodic optimization
func (fs *FlowSequencer) optimizationWorker() {
	defer fs.wg.Done()

	ticker := time.NewTicker(fs.config.OptimizationInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			fs.performOptimization()
		case <-fs.stopChan:
			return
		}
	}
}

// performOptimization performs periodic optimization
func (fs *FlowSequencer) performOptimization() {
	// Optimize buffer sizes
	fs.optimizeBufferSizes()

	// Clean up inactive flows
	fs.cleanupInactiveFlows()

	// Update QoS statistics
	fs.updateQoSStatistics()
}

// optimizeBufferSizes optimizes buffer sizes based on usage patterns
func (fs *FlowSequencer) optimizeBufferSizes() {
	fs.flowsMutex.RLock()
	defer fs.flowsMutex.RUnlock()

	for _, flowContext := range fs.flows {
		flowContext.mutex.Lock()

		// Check buffer utilization
		inputUtilization := float64(len(flowContext.inputBuffer.data)) / float64(flowContext.inputBuffer.capacity)
		outputUtilization := float64(len(flowContext.outputBuffer.data)) / float64(flowContext.outputBuffer.capacity)

		// Grow buffers if highly utilized
		if inputUtilization > 0.8 && flowContext.inputBuffer.capacity < fs.config.MaxBufferSize {
			newCapacity := int(float64(flowContext.inputBuffer.capacity) * fs.config.BufferGrowthFactor)
			if newCapacity > fs.config.MaxBufferSize {
				newCapacity = fs.config.MaxBufferSize
			}
			flowContext.inputBuffer.capacity = newCapacity
		}

		if outputUtilization > 0.8 && flowContext.outputBuffer.capacity < fs.config.MaxBufferSize {
			newCapacity := int(float64(flowContext.outputBuffer.capacity) * fs.config.BufferGrowthFactor)
			if newCapacity > fs.config.MaxBufferSize {
				newCapacity = fs.config.MaxBufferSize
			}
			flowContext.outputBuffer.capacity = newCapacity
		}

		flowContext.mutex.Unlock()
	}
}

// cleanupInactiveFlows cleans up flows that have been inactive
func (fs *FlowSequencer) cleanupInactiveFlows() {
	fs.flowsMutex.Lock()
	defer fs.flowsMutex.Unlock()

	now := time.Now()
	inactiveThreshold := now.Add(-5 * time.Minute) // 5 minutes of inactivity

	var inactiveFlows []uint64
	for flowID, flowContext := range fs.flows {
		flowContext.mutex.RLock()
		lastActivity := flowContext.lastActivity
		flowContext.mutex.RUnlock()

		if lastActivity.Before(inactiveThreshold) {
			inactiveFlows = append(inactiveFlows, flowID)
		}
	}

	// Clean up inactive flows
	for _, flowID := range inactiveFlows {
		delete(fs.flows, flowID)
		fs.statsMutex.Lock()
		fs.stats.ActiveFlows--
		fs.statsMutex.Unlock()
	}
}

// updateQoSStatistics updates QoS-related statistics
func (fs *FlowSequencer) updateQoSStatistics() {
	// TODO: Implement QoS statistics updates
}

// updateReconstitutionStats updates reconstitution statistics
func (fs *FlowSequencer) updateReconstitutionStats(qosClass QoSClass, dataSize int) {
	fs.statsMutex.Lock()
	defer fs.statsMutex.Unlock()

	fs.stats.FlowsReconstituted++

	if qosStats, exists := fs.stats.QoSStats[qosClass]; exists {
		// Update throughput calculation
		// This is a simplified calculation - in practice, you'd want a more sophisticated approach
		qosStats.ThroughputMbps = float64(dataSize) * 8 / 1000000 // Convert to Mbps
	}
}

// GetSequencerStats returns current sequencer statistics
func (fs *FlowSequencer) GetSequencerStats() *SequencerStats {
	fs.statsMutex.RLock()
	defer fs.statsMutex.RUnlock()

	// Create a copy of the stats
	statsCopy := &SequencerStats{
		TotalFlows:           fs.stats.TotalFlows,
		ActiveFlows:          fs.stats.ActiveFlows,
		TotalFramesSequenced: fs.stats.TotalFramesSequenced,
		FramesReordered:      fs.stats.FramesReordered,
		FlowsReconstituted:   fs.stats.FlowsReconstituted,
		AverageLatency:       fs.stats.AverageLatency,
		ThroughputMbps:       fs.stats.ThroughputMbps,
		QoSStats:             make(map[QoSClass]*QoSStats),
	}

	// Copy QoS stats
	for qosClass, qosStats := range fs.stats.QoSStats {
		statsCopy.QoSStats[qosClass] = &QoSStats{
			FramesProcessed: qosStats.FramesProcessed,
			AverageLatency:  qosStats.AverageLatency,
			ThroughputMbps:  qosStats.ThroughputMbps,
			DroppedFrames:   qosStats.DroppedFrames,
		}
	}

	return statsCopy
}

// Close shuts down the flow sequencer
func (fs *FlowSequencer) Close() error {
	close(fs.stopChan)
	fs.wg.Wait()
	close(fs.processingChan)

	fs.flowsMutex.Lock()
	defer fs.flowsMutex.Unlock()

	// Clear all flows
	fs.flows = make(map[uint64]*FlowContext)

	return nil
}

// NewSequencerStats creates new sequencer statistics
func NewSequencerStats() *SequencerStats {
	stats := &SequencerStats{
		QoSStats: make(map[QoSClass]*QoSStats),
	}

	// Initialize QoS stats for all classes
	for qosClass := QoSClassBestEffort; qosClass <= QoSClassCritical; qosClass++ {
		stats.QoSStats[qosClass] = &QoSStats{
			FramesProcessed: 0,
			AverageLatency:  0,
			ThroughputMbps:  0,
			DroppedFrames:   0,
		}
	}

	return stats
}

// NewFlowBuffer creates a new flow buffer
func NewFlowBuffer(capacity int) *FlowBuffer {
	return &FlowBuffer{
		data:        make([]byte, 0, capacity),
		capacity:    capacity,
		readOffset:  0,
		writeOffset: 0,
	}
}

// Write writes data to the flow buffer
func (fb *FlowBuffer) Write(data []byte) (int, error) {
	fb.mutex.Lock()
	defer fb.mutex.Unlock()

	if len(fb.data)+len(data) > fb.capacity {
		return 0, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"buffer capacity exceeded", nil)
	}

	fb.data = append(fb.data, data...)
	fb.writeOffset += uint64(len(data))

	return len(data), nil
}

// Read reads data from the flow buffer
func (fb *FlowBuffer) Read(buffer []byte) (int, error) {
	fb.mutex.Lock()
	defer fb.mutex.Unlock()

	available := len(fb.data) - int(fb.readOffset)
	if available == 0 {
		return 0, nil
	}

	n := len(buffer)
	if n > available {
		n = available
	}

	copy(buffer[:n], fb.data[fb.readOffset:fb.readOffset+uint64(n)])
	fb.readOffset += uint64(n)

	// Compact buffer if read offset is significant
	if fb.readOffset > uint64(len(fb.data))/2 {
		copy(fb.data, fb.data[fb.readOffset:])
		fb.data = fb.data[:len(fb.data)-int(fb.readOffset)]
		fb.writeOffset -= fb.readOffset
		fb.readOffset = 0
	}

	return n, nil
}

// NewSequencingState creates a new sequencing state
func NewSequencingState() *SequencingState {
	return &SequencingState{
		nextExpectedSeq:        0,
		maxSeqSeen:             0,
		outOfOrderFrames:       make(map[uint64]*datapb.DataFrame),
		reconstitutedData:      make([]byte, 0),
		reconstitutionComplete: false,
		gaps:                   make([]SequenceGap, 0),
	}
}

// SequencedFrame represents a frame in the sequencing queue
type SequencedFrame struct {
	Frame       *datapb.DataFrame
	Priority    int
	QoS         QoSClass
	EnqueueTime time.Time
	index       int // For heap interface
}

// SequencingHeap implements a priority queue for sequenced frames
type SequencingHeap []*SequencedFrame

func (h SequencingHeap) Len() int { return len(h) }

func (h SequencingHeap) Less(i, j int) bool {
	// Higher priority frames come first
	if h[i].Priority != h[j].Priority {
		return h[i].Priority > h[j].Priority
	}
	// If priority is the same, earlier enqueue time comes first
	return h[i].EnqueueTime.Before(h[j].EnqueueTime)
}

func (h SequencingHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *SequencingHeap) Push(x interface{}) {
	n := len(*h)
	frame := x.(*SequencedFrame)
	frame.index = n
	*h = append(*h, frame)
}

func (h *SequencingHeap) Pop() interface{} {
	old := *h
	n := len(old)
	frame := old[n-1]
	old[n-1] = nil
	frame.index = -1
	*h = old[0 : n-1]
	return frame
}

// NewSequencingQueue creates a new sequencing queue
func NewSequencingQueue() *SequencingQueue {
	sq := &SequencingQueue{
		queue: make(SequencingHeap, 0),
	}
	heap.Init(&sq.queue)
	return sq
}

// Enqueue adds a frame to the sequencing queue
func (sq *SequencingQueue) Enqueue(frame *SequencedFrame) {
	sq.mutex.Lock()
	defer sq.mutex.Unlock()
	heap.Push(&sq.queue, frame)
}

// Dequeue removes and returns the highest priority frame
func (sq *SequencingQueue) Dequeue() *SequencedFrame {
	sq.mutex.Lock()
	defer sq.mutex.Unlock()

	if len(sq.queue) == 0 {
		return nil
	}

	return heap.Pop(&sq.queue).(*SequencedFrame)
}

// Peek returns the highest priority frame without removing it
func (sq *SequencingQueue) Peek() *SequencedFrame {
	sq.mutex.RLock()
	defer sq.mutex.RUnlock()

	if len(sq.queue) == 0 {
		return nil
	}

	return sq.queue[0]
}

// Size returns the number of frames in the queue
func (sq *SequencingQueue) Size() int {
	sq.mutex.RLock()
	defer sq.mutex.RUnlock()
	return len(sq.queue)
}

// Clear removes all frames from the queue
func (sq *SequencingQueue) Clear() {
	sq.mutex.Lock()
	defer sq.mutex.Unlock()
	sq.queue = make(SequencingHeap, 0)
	heap.Init(&sq.queue)
}

// GetFramesByQoS returns frames filtered by QoS class
func (sq *SequencingQueue) GetFramesByQoS(qosClass QoSClass) []*SequencedFrame {
	sq.mutex.RLock()
	defer sq.mutex.RUnlock()

	var frames []*SequencedFrame
	for _, frame := range sq.queue {
		if frame.QoS == qosClass {
			frames = append(frames, frame)
		}
	}

	// Sort by priority and enqueue time
	sort.Slice(frames, func(i, j int) bool {
		if frames[i].Priority != frames[j].Priority {
			return frames[i].Priority > frames[j].Priority
		}
		return frames[i].EnqueueTime.Before(frames[j].EnqueueTime)
	})

	return frames
}

// FlowReconstitutionEngine provides advanced flow reconstitution capabilities
type FlowReconstitutionEngine struct {
	sequencer *FlowSequencer
	config    *ReconstitutionConfig
}

// ReconstitutionConfig contains configuration for flow reconstitution
type ReconstitutionConfig struct {
	EnableAdaptiveBuffering  bool
	EnablePredictiveOrdering bool
	EnableLossRecovery       bool
	MaxRecoveryAttempts      int
	RecoveryTimeout          time.Duration
}

// NewFlowReconstitutionEngine creates a new flow reconstitution engine
func NewFlowReconstitutionEngine(sequencer *FlowSequencer, config *ReconstitutionConfig) *FlowReconstitutionEngine {
	if config == nil {
		config = &ReconstitutionConfig{
			EnableAdaptiveBuffering:  true,
			EnablePredictiveOrdering: true,
			EnableLossRecovery:       true,
			MaxRecoveryAttempts:      3,
			RecoveryTimeout:          500 * time.Millisecond,
		}
	}

	return &FlowReconstitutionEngine{
		sequencer: sequencer,
		config:    config,
	}
}

// ReconstituteFlowWithPrediction reconstitutes a flow using predictive ordering
func (fre *FlowReconstitutionEngine) ReconstituteFlowWithPrediction(flowID uint64, frames []*datapb.DataFrame) ([]byte, error) {
	if !fre.config.EnablePredictiveOrdering {
		// Fall back to basic reconstitution
		return fre.basicReconstitution(flowID, frames)
	}

	// Sort frames by predicted optimal order
	predictedOrder := fre.predictOptimalOrder(frames)

	// Reconstitute using predicted order
	var reconstitutedData []byte
	for _, frame := range predictedOrder {
		reconstitutedData = append(reconstitutedData, frame.Data...)
	}

	return reconstitutedData, nil
}

// predictOptimalOrder predicts the optimal order for frame processing
func (fre *FlowReconstitutionEngine) predictOptimalOrder(frames []*datapb.DataFrame) []*datapb.DataFrame {
	// Sort by offset first (basic ordering)
	sort.Slice(frames, func(i, j int) bool {
		return frames[i].Offset < frames[j].Offset
	})

	// TODO: Implement more sophisticated prediction algorithms:
	// 1. Machine learning-based ordering prediction
	// 2. Pattern recognition for common frame sequences
	// 3. Adaptive ordering based on historical performance

	return frames
}

// basicReconstitution performs basic flow reconstitution
func (fre *FlowReconstitutionEngine) basicReconstitution(flowID uint64, frames []*datapb.DataFrame) ([]byte, error) {
	// Sort frames by offset
	sort.Slice(frames, func(i, j int) bool {
		return frames[i].Offset < frames[j].Offset
	})

	// Concatenate frame data
	var reconstitutedData []byte
	for _, frame := range frames {
		reconstitutedData = append(reconstitutedData, frame.Data...)
	}

	return reconstitutedData, nil
}

// String methods for enums

// String returns string representation of QoS class
func (qos QoSClass) String() string {
	switch qos {
	case QoSClassBestEffort:
		return "BEST_EFFORT"
	case QoSClassLowLatency:
		return "LOW_LATENCY"
	case QoSClassHighThroughput:
		return "HIGH_THROUGHPUT"
	case QoSClassRealTime:
		return "REAL_TIME"
	case QoSClassCritical:
		return "CRITICAL"
	default:
		return "UNKNOWN"
	}
}

// String returns string representation of sequencing task type
func (stt SequencingTaskType) String() string {
	switch stt {
	case TaskTypeSequenceFrame:
		return "SEQUENCE_FRAME"
	case TaskTypeReconstituteFlow:
		return "RECONSTITUTE_FLOW"
	case TaskTypeOptimizeSequencing:
		return "OPTIMIZE_SEQUENCING"
	case TaskTypeFlowCleanup:
		return "FLOW_CLEANUP"
	default:
		return "UNKNOWN"
	}
}
