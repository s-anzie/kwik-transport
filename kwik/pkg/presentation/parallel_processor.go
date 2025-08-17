package presentation

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// ParallelProcessor manages parallel processing of presentation tasks
type ParallelProcessor struct {
	// Configuration
	numWorkers      int
	batchSize       int
	queueSize       int
	
	// Worker management
	workers         []*Worker
	taskQueue       chan Task
	resultQueue     chan TaskResult
	
	// Lifecycle management
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	started         int32
	
	// Statistics
	stats           ParallelProcessorStats
	statsMutex      sync.RWMutex
	
	// Performance monitoring
	lastStatsUpdate time.Time
	throughputHist  []float64
	latencyHist     []time.Duration
}

// Task represents a unit of work for parallel processing
type Task struct {
	ID          uint64
	Type        TaskType
	StreamID    uint64
	Data        []byte
	Offset      uint64
	Metadata    interface{}
	Priority    TaskPriority
	SubmittedAt time.Time
	Callback    TaskCallback
}

// TaskResult represents the result of a processed task
type TaskResult struct {
	TaskID      uint64
	Success     bool
	Error       error
	Data        []byte
	ProcessedAt time.Time
	Duration    time.Duration
}

// TaskType defines the type of task to be processed
type TaskType int

const (
	TaskTypeWrite TaskType = iota
	TaskTypeRead
	TaskTypeCleanup
	TaskTypeGapDetection
	TaskTypeWindowSlide
	TaskTypeBackpressureCheck
)

// TaskPriority defines the priority level of a task
type TaskPriority int

const (
	TaskPriorityLow TaskPriority = iota
	TaskPriorityNormal
	TaskPriorityHigh
	TaskPriorityCritical
)

// TaskCallback is called when a task is completed
type TaskCallback func(result TaskResult)

// Worker represents a worker goroutine
type Worker struct {
	id          int
	processor   *ParallelProcessor
	taskQueue   <-chan Task
	resultQueue chan<- TaskResult
	
	// Statistics
	tasksProcessed int64
	totalDuration  time.Duration
	lastActivity   time.Time
}

// ParallelProcessorStats contains statistics about parallel processing
type ParallelProcessorStats struct {
	// Task statistics
	TotalTasks      int64
	CompletedTasks  int64
	FailedTasks     int64
	QueuedTasks     int64
	
	// Performance metrics
	TasksPerSecond  float64
	AverageLatency  time.Duration
	P95Latency      time.Duration
	P99Latency      time.Duration
	
	// Worker statistics
	ActiveWorkers   int
	IdleWorkers     int
	WorkerUtilization float64
	
	// Queue statistics
	QueueUtilization float64
	MaxQueueSize     int
	
	LastUpdate      time.Time
}

// NewParallelProcessor creates a new parallel processor
func NewParallelProcessor(numWorkers, batchSize, queueSize int) *ParallelProcessor {
	ctx, cancel := context.WithCancel(context.Background())
	
	pp := &ParallelProcessor{
		numWorkers:      numWorkers,
		batchSize:       batchSize,
		queueSize:       queueSize,
		taskQueue:       make(chan Task, queueSize),
		resultQueue:     make(chan TaskResult, queueSize),
		ctx:             ctx,
		cancel:          cancel,
		lastStatsUpdate: time.Now(),
		throughputHist:  make([]float64, 0, 100),
		latencyHist:     make([]time.Duration, 0, 1000),
	}
	
	// Create workers
	pp.workers = make([]*Worker, numWorkers)
	for i := 0; i < numWorkers; i++ {
		pp.workers[i] = &Worker{
			id:          i,
			processor:   pp,
			taskQueue:   pp.taskQueue,
			resultQueue: pp.resultQueue,
			lastActivity: time.Now(),
		}
	}
	
	return pp
}

// Start starts the parallel processor
func (pp *ParallelProcessor) Start() error {
	if !atomic.CompareAndSwapInt32(&pp.started, 0, 1) {
		return nil // Already started
	}
	
	// Start workers
	for _, worker := range pp.workers {
		pp.wg.Add(1)
		go worker.run()
	}
	
	// Start result processor
	pp.wg.Add(1)
	go pp.processResults()
	
	// Start statistics updater
	pp.wg.Add(1)
	go pp.updateStats()
	
	return nil
}

// Stop stops the parallel processor
func (pp *ParallelProcessor) Stop() error {
	if !atomic.CompareAndSwapInt32(&pp.started, 1, 0) {
		return nil // Already stopped
	}
	
	pp.cancel()
	close(pp.taskQueue)
	pp.wg.Wait()
	close(pp.resultQueue)
	
	return nil
}

// SubmitTask submits a task for parallel processing
func (pp *ParallelProcessor) SubmitTask(task Task) error {
	if atomic.LoadInt32(&pp.started) == 0 {
		return ErrSystemShutdown
	}
	
	task.SubmittedAt = time.Now()
	
	select {
	case pp.taskQueue <- task:
		atomic.AddInt64(&pp.stats.TotalTasks, 1)
		atomic.AddInt64(&pp.stats.QueuedTasks, 1)
		return nil
	case <-pp.ctx.Done():
		return ErrSystemShutdown
	default:
		return ErrBufferOverflow // Queue is full
	}
}

// SubmitBatch submits multiple tasks as a batch
func (pp *ParallelProcessor) SubmitBatch(tasks []Task) error {
	if atomic.LoadInt32(&pp.started) == 0 {
		return ErrSystemShutdown
	}
	
	for i := range tasks {
		tasks[i].SubmittedAt = time.Now()
	}
	
	// Try to submit all tasks
	for _, task := range tasks {
		select {
		case pp.taskQueue <- task:
			atomic.AddInt64(&pp.stats.TotalTasks, 1)
			atomic.AddInt64(&pp.stats.QueuedTasks, 1)
		case <-pp.ctx.Done():
			return ErrSystemShutdown
		default:
			return ErrBufferOverflow // Queue is full
		}
	}
	
	return nil
}

// GetStats returns current processor statistics
func (pp *ParallelProcessor) GetStats() ParallelProcessorStats {
	pp.statsMutex.RLock()
	defer pp.statsMutex.RUnlock()
	
	stats := pp.stats
	stats.QueuedTasks = int64(len(pp.taskQueue))
	stats.QueueUtilization = float64(len(pp.taskQueue)) / float64(pp.queueSize)
	
	// Count active/idle workers
	activeWorkers := 0
	for _, worker := range pp.workers {
		if time.Since(worker.lastActivity) < time.Second {
			activeWorkers++
		}
	}
	stats.ActiveWorkers = activeWorkers
	stats.IdleWorkers = pp.numWorkers - activeWorkers
	stats.WorkerUtilization = float64(activeWorkers) / float64(pp.numWorkers)
	
	return stats
}

// run is the main worker loop
func (w *Worker) run() {
	defer w.processor.wg.Done()
	
	for {
		select {
		case task, ok := <-w.taskQueue:
			if !ok {
				return // Channel closed
			}
			
			w.lastActivity = time.Now()
			result := w.processTask(task)
			
			select {
			case w.resultQueue <- result:
				atomic.AddInt64(&w.tasksProcessed, 1)
				w.totalDuration += result.Duration
			case <-w.processor.ctx.Done():
				return
			}
			
		case <-w.processor.ctx.Done():
			return
		}
	}
}

// processTask processes a single task
func (w *Worker) processTask(task Task) TaskResult {
	startTime := time.Now()
	
	result := TaskResult{
		TaskID:      task.ID,
		Success:     true,
		ProcessedAt: startTime,
	}
	
	// Process based on task type
	switch task.Type {
	case TaskTypeWrite:
		result.Error = w.processWriteTask(task)
	case TaskTypeRead:
		result.Data, result.Error = w.processReadTask(task)
	case TaskTypeCleanup:
		result.Error = w.processCleanupTask(task)
	case TaskTypeGapDetection:
		result.Error = w.processGapDetectionTask(task)
	case TaskTypeWindowSlide:
		result.Error = w.processWindowSlideTask(task)
	case TaskTypeBackpressureCheck:
		result.Error = w.processBackpressureCheckTask(task)
	default:
		result.Error = ErrInvalidData
	}
	
	result.Duration = time.Since(startTime)
	result.Success = result.Error == nil
	
	return result
}

// processWriteTask processes a write task
func (w *Worker) processWriteTask(task Task) error {
	// This would integrate with the actual stream buffer
	// For now, simulate processing time
	time.Sleep(time.Microsecond * time.Duration(len(task.Data)/100))
	return nil
}

// processReadTask processes a read task
func (w *Worker) processReadTask(task Task) ([]byte, error) {
	// This would integrate with the actual stream buffer
	// For now, simulate processing time and return dummy data
	time.Sleep(time.Microsecond * 10)
	return make([]byte, 1024), nil
}

// processCleanupTask processes a cleanup task
func (w *Worker) processCleanupTask(task Task) error {
	// This would integrate with the actual cleanup logic
	time.Sleep(time.Microsecond * 50)
	return nil
}

// processGapDetectionTask processes a gap detection task
func (w *Worker) processGapDetectionTask(task Task) error {
	// This would integrate with the actual gap detection logic
	time.Sleep(time.Microsecond * 20)
	return nil
}

// processWindowSlideTask processes a window slide task
func (w *Worker) processWindowSlideTask(task Task) error {
	// This would integrate with the actual window sliding logic
	time.Sleep(time.Microsecond * 30)
	return nil
}

// processBackpressureCheckTask processes a backpressure check task
func (w *Worker) processBackpressureCheckTask(task Task) error {
	// This would integrate with the actual backpressure checking logic
	time.Sleep(time.Microsecond * 5)
	return nil
}

// processResults processes task results
func (pp *ParallelProcessor) processResults() {
	defer pp.wg.Done()
	
	for {
		select {
		case result, ok := <-pp.resultQueue:
			if !ok {
				return // Channel closed
			}
			
			// Update statistics
			atomic.AddInt64(&pp.stats.QueuedTasks, -1)
			if result.Success {
				atomic.AddInt64(&pp.stats.CompletedTasks, 1)
			} else {
				atomic.AddInt64(&pp.stats.FailedTasks, 1)
			}
			
			// Update latency histogram
			pp.statsMutex.Lock()
			if len(pp.latencyHist) < cap(pp.latencyHist) {
				pp.latencyHist = append(pp.latencyHist, result.Duration)
			} else {
				// Circular buffer
				pp.latencyHist[int(result.TaskID)%len(pp.latencyHist)] = result.Duration
			}
			pp.statsMutex.Unlock()
			
			// Call callback if provided
			// Note: We would need to get the callback from the original task
			// This is a simplified implementation
			
		case <-pp.ctx.Done():
			return
		}
	}
}

// updateStats periodically updates performance statistics
func (pp *ParallelProcessor) updateStats() {
	defer pp.wg.Done()
	
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	
	var lastCompleted int64
	var lastUpdate time.Time = time.Now()
	
	for {
		select {
		case <-ticker.C:
			now := time.Now()
			duration := now.Sub(lastUpdate)
			
			pp.statsMutex.Lock()
			
			// Calculate throughput
			currentCompleted := atomic.LoadInt64(&pp.stats.CompletedTasks)
			tasksInPeriod := currentCompleted - lastCompleted
			pp.stats.TasksPerSecond = float64(tasksInPeriod) / duration.Seconds()
			
			// Update throughput history
			if len(pp.throughputHist) < cap(pp.throughputHist) {
				pp.throughputHist = append(pp.throughputHist, pp.stats.TasksPerSecond)
			} else {
				// Circular buffer
				copy(pp.throughputHist, pp.throughputHist[1:])
				pp.throughputHist[len(pp.throughputHist)-1] = pp.stats.TasksPerSecond
			}
			
			// Calculate latency percentiles
			if len(pp.latencyHist) > 0 {
				pp.stats.AverageLatency = pp.calculateAverageLatency()
				pp.stats.P95Latency = pp.calculatePercentileLatency(0.95)
				pp.stats.P99Latency = pp.calculatePercentileLatency(0.99)
			}
			
			pp.stats.LastUpdate = now
			
			pp.statsMutex.Unlock()
			
			lastCompleted = currentCompleted
			lastUpdate = now
			
		case <-pp.ctx.Done():
			return
		}
	}
}

// calculateAverageLatency calculates the average latency from the histogram
func (pp *ParallelProcessor) calculateAverageLatency() time.Duration {
	if len(pp.latencyHist) == 0 {
		return 0
	}
	
	var total time.Duration
	for _, latency := range pp.latencyHist {
		total += latency
	}
	
	return total / time.Duration(len(pp.latencyHist))
}

// calculatePercentileLatency calculates the specified percentile latency
func (pp *ParallelProcessor) calculatePercentileLatency(percentile float64) time.Duration {
	if len(pp.latencyHist) == 0 {
		return 0
	}
	
	// Simple percentile calculation (not perfectly accurate but good enough)
	// In production, you'd want to use a proper percentile calculation
	sortedLatencies := make([]time.Duration, len(pp.latencyHist))
	copy(sortedLatencies, pp.latencyHist)
	
	// Simple bubble sort for small arrays
	for i := 0; i < len(sortedLatencies); i++ {
		for j := 0; j < len(sortedLatencies)-1-i; j++ {
			if sortedLatencies[j] > sortedLatencies[j+1] {
				sortedLatencies[j], sortedLatencies[j+1] = sortedLatencies[j+1], sortedLatencies[j]
			}
		}
	}
	
	index := int(float64(len(sortedLatencies)) * percentile)
	if index >= len(sortedLatencies) {
		index = len(sortedLatencies) - 1
	}
	
	return sortedLatencies[index]
}

// BatchProcessor handles batch processing of similar tasks
type BatchProcessor struct {
	processor   *ParallelProcessor
	batchSize   int
	flushInterval time.Duration
	
	// Batching state
	batches     map[TaskType][]Task
	batchMutex  sync.Mutex
	lastFlush   time.Time
	
	// Lifecycle
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
}

// NewBatchProcessor creates a new batch processor
func NewBatchProcessor(processor *ParallelProcessor, batchSize int, flushInterval time.Duration) *BatchProcessor {
	ctx, cancel := context.WithCancel(context.Background())
	
	return &BatchProcessor{
		processor:     processor,
		batchSize:     batchSize,
		flushInterval: flushInterval,
		batches:       make(map[TaskType][]Task),
		lastFlush:     time.Now(),
		ctx:           ctx,
		cancel:        cancel,
	}
}

// Start starts the batch processor
func (bp *BatchProcessor) Start() error {
	bp.wg.Add(1)
	go bp.flushRoutine()
	return nil
}

// Stop stops the batch processor
func (bp *BatchProcessor) Stop() error {
	bp.cancel()
	bp.wg.Wait()
	
	// Flush remaining batches
	bp.flushAllBatches()
	
	return nil
}

// AddTask adds a task to the appropriate batch
func (bp *BatchProcessor) AddTask(task Task) error {
	bp.batchMutex.Lock()
	defer bp.batchMutex.Unlock()
	
	if bp.batches[task.Type] == nil {
		bp.batches[task.Type] = make([]Task, 0, bp.batchSize)
	}
	
	bp.batches[task.Type] = append(bp.batches[task.Type], task)
	
	// Flush if batch is full
	if len(bp.batches[task.Type]) >= bp.batchSize {
		return bp.flushBatch(task.Type)
	}
	
	return nil
}

// flushRoutine periodically flushes batches
func (bp *BatchProcessor) flushRoutine() {
	defer bp.wg.Done()
	
	ticker := time.NewTicker(bp.flushInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			bp.flushAllBatches()
		case <-bp.ctx.Done():
			return
		}
	}
}

// flushBatch flushes a specific batch type
func (bp *BatchProcessor) flushBatch(taskType TaskType) error {
	batch := bp.batches[taskType]
	if len(batch) == 0 {
		return nil
	}
	
	// Submit batch to processor
	err := bp.processor.SubmitBatch(batch)
	if err != nil {
		return err
	}
	
	// Clear the batch
	bp.batches[taskType] = bp.batches[taskType][:0]
	
	return nil
}

// flushAllBatches flushes all pending batches
func (bp *BatchProcessor) flushAllBatches() {
	bp.batchMutex.Lock()
	defer bp.batchMutex.Unlock()
	
	for taskType := range bp.batches {
		bp.flushBatch(taskType)
	}
	
	bp.lastFlush = time.Now()
}

// OptimizedGapDetector provides optimized gap detection algorithms
type OptimizedGapDetector struct {
	// Gap tracking using interval tree or similar efficient structure
	gaps        []GapInterval
	gapsMutex   sync.RWMutex
	
	// Performance optimization
	lastScanPos uint64
	scanCache   map[uint64]bool
}

// GapInterval represents a gap in the data
type GapInterval struct {
	Start    uint64
	End      uint64
	Duration time.Duration
}

// NewOptimizedGapDetector creates a new optimized gap detector
func NewOptimizedGapDetector() *OptimizedGapDetector {
	return &OptimizedGapDetector{
		gaps:      make([]GapInterval, 0),
		scanCache: make(map[uint64]bool),
	}
}

// DetectGaps efficiently detects gaps in the given range
func (ogd *OptimizedGapDetector) DetectGaps(start, end uint64, dataMap map[uint64][]byte) []GapInterval {
	ogd.gapsMutex.Lock()
	defer ogd.gapsMutex.Unlock()
	
	gaps := make([]GapInterval, 0)
	currentPos := start
	
	for currentPos < end {
		if _, exists := dataMap[currentPos]; !exists {
			// Found start of gap
			gapStart := currentPos
			
			// Find end of gap
			for currentPos < end {
				if _, exists := dataMap[currentPos]; exists {
					break
				}
				currentPos++
			}
			
			gaps = append(gaps, GapInterval{
				Start:    gapStart,
				End:      currentPos,
				Duration: time.Since(time.Now()), // This would be tracked properly
			})
		} else {
			currentPos++
		}
	}
	
	return gaps
}

// OptimizedWindowSlider provides optimized window sliding algorithms
type OptimizedWindowSlider struct {
	windowSize    uint64
	slideSize     uint64
	currentPos    uint64
	
	// Optimization state
	lastSlideTime time.Time
	slideHistory  []SlideEvent
	
	mutex         sync.RWMutex
}

// SlideEvent represents a window slide event
type SlideEvent struct {
	Position  uint64
	Size      uint64
	Timestamp time.Time
	Duration  time.Duration
}

// NewOptimizedWindowSlider creates a new optimized window slider
func NewOptimizedWindowSlider(windowSize, slideSize uint64) *OptimizedWindowSlider {
	return &OptimizedWindowSlider{
		windowSize:    windowSize,
		slideSize:     slideSize,
		slideHistory:  make([]SlideEvent, 0, 100),
		lastSlideTime: time.Now(),
	}
}

// SlideWindow efficiently slides the window
func (ows *OptimizedWindowSlider) SlideWindow(targetPos uint64) SlideEvent {
	ows.mutex.Lock()
	defer ows.mutex.Unlock()
	
	startTime := time.Now()
	
	// Calculate optimal slide size
	slideSize := ows.calculateOptimalSlideSize(targetPos)
	
	// Perform the slide
	ows.currentPos = targetPos
	
	event := SlideEvent{
		Position:  ows.currentPos,
		Size:      slideSize,
		Timestamp: startTime,
		Duration:  time.Since(startTime),
	}
	
	// Update history
	if len(ows.slideHistory) < cap(ows.slideHistory) {
		ows.slideHistory = append(ows.slideHistory, event)
	} else {
		// Circular buffer
		copy(ows.slideHistory, ows.slideHistory[1:])
		ows.slideHistory[len(ows.slideHistory)-1] = event
	}
	
	ows.lastSlideTime = startTime
	
	return event
}

// calculateOptimalSlideSize calculates the optimal slide size based on history and target
func (ows *OptimizedWindowSlider) calculateOptimalSlideSize(targetPos uint64) uint64 {
	if targetPos <= ows.currentPos {
		return 0
	}
	
	distance := targetPos - ows.currentPos
	
	// Use configured slide size, but optimize based on distance
	if distance < ows.slideSize {
		return distance
	}
	
	// For large distances, use multiple of slide size
	return ((distance + ows.slideSize - 1) / ows.slideSize) * ows.slideSize
}