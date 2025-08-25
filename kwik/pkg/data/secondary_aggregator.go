package data

import (
	"fmt"
	"runtime"
	"sync"
	"time"

	"kwik/internal/utils"
	"kwik/pkg/logger"
)

// StreamAggregator handles aggregation of data from secondary streams into KWIK streams
type StreamAggregator struct {
	// Secondary stream data by stream ID
	secondaryStreams map[uint64]*SecondaryStreamState
	streamsMutex     sync.RWMutex

	// Mapping from secondary streams to KWIK streams
	streamMappings map[uint64]uint64 // secondaryStreamID -> kwikStreamID
	mappingsMutex  sync.RWMutex

	// KWIK stream aggregation state (kept for backward compatibility)
	kwikStreams map[uint64]*KwikStreamAggregationState
	kwikMutex   sync.RWMutex

	// Configuration
	config *SecondaryAggregatorConfig

	// Statistics (internal structure with mutex)
	stats *internalSecondaryAggregationStats

	// Data presentation manager integration
	presentationManager DataPresentationManagerInterface
	presentationMutex   sync.RWMutex

	// Parallel processing
	workerPool    *AggregationWorkerPool
	batchChannel  chan []*DataFrame
	resultChannel chan *AggregationResult

	// Logger
	logger logger.Logger
}

// DataPresentationManagerInterface defines the interface for presentation manager integration
type DataPresentationManagerInterface interface {
	WriteToStream(streamID uint64, data []byte, offset uint64, metadata interface{}) error
	CreateStreamBuffer(streamID uint64, metadata interface{}) error
	IsBackpressureActive(streamID uint64) bool
}

// SecondaryLogger interface for logging in secondary aggregator
type SecondaryLogger interface {
	Debug(msg string, args ...interface{})
	Info(msg string, args ...interface{})
	Warn(msg string, args ...interface{})
	Error(msg string, args ...interface{})
	Critical(msg string, args ...interface{})
}

// DataFrame is defined in interfaces.go

// SecondaryStreamState tracks the state of a secondary stream
type SecondaryStreamState struct {
	StreamID        uint64
	PathID          string
	KwikStreamID    uint64
	CurrentOffset   uint64
	LastActivity    time.Time
	BytesReceived   uint64
	BytesAggregated uint64
	State           SecondaryStreamStateType
	PendingData     map[uint64]*DataFrame // offset -> data
	mutex           sync.RWMutex
}

// KwikStreamAggregationState tracks aggregation state for a KWIK stream
type KwikStreamAggregationState struct {
	StreamID             uint64
	NextExpectedOffset   uint64
	SecondaryStreams     map[uint64]bool       // Set of secondary stream IDs
	PendingData          map[uint64]*DataFrame // offset -> data
	AggregatedBuffer     []byte
	LastActivity         time.Time
	TotalBytesAggregated uint64
	mutex                sync.RWMutex
}

// SecondaryStreamStateType represents the state of a secondary stream
type SecondaryStreamStateType int

const (
	SecondaryStreamStateOpening SecondaryStreamStateType = iota
	SecondaryStreamStateActive
	SecondaryStreamStateClosing
	SecondaryStreamStateClosed
	SecondaryStreamStateError
)

// SecondaryAggregatorConfig holds configuration for the secondary aggregator
type SecondaryAggregatorConfig struct {
	MaxSecondaryStreams      int
	MaxPendingData           int           // Maximum pending data per stream
	ReorderTimeout           time.Duration // Timeout for reordering data
	AggregationBatchSize     int           // Batch size for aggregation
	BufferSize               int           // Buffer size for aggregated data
	CleanupInterval          time.Duration // Interval for cleanup of inactive streams
	ParallelWorkers          int           // Number of parallel workers for aggregation
	EnableParallelProcessing bool          // Enable parallel processing
}

// SecondaryAggregationStats is defined in interfaces.go

// internalSecondaryAggregationStats contains statistics with mutex for internal use
type internalSecondaryAggregationStats struct {
	ActiveSecondaryStreams int
	ActiveKwikStreams      int
	TotalBytesAggregated   uint64
	TotalDataFrames        uint64
	AggregationLatency     time.Duration
	ReorderingEvents       uint64
	DroppedFrames          uint64
	LastUpdate             time.Time
	mutex                  sync.RWMutex
}

// AggregationWorkerPool manages parallel workers for aggregation
type AggregationWorkerPool struct {
	workers       int
	workChannel   chan *AggregationWork
	resultChannel chan *AggregationResult
	stopChannel   chan struct{}
	wg            sync.WaitGroup
	running       bool
	mutex         sync.RWMutex
}

// AggregationWork represents work to be done by a worker
type AggregationWork struct {
	Data      []*DataFrame
	BatchID   uint64
	Timestamp time.Time
}

// AggregationResult represents the result of aggregation work
type AggregationResult struct {
	BatchID        uint64
	ProcessedData  []*ProcessedSecondaryData
	Error          error
	ProcessingTime time.Duration
}

// ProcessedSecondaryData represents processed secondary stream data
type ProcessedSecondaryData struct {
	StreamID     uint64
	KwikStreamID uint64
	Data         []byte
	Offset       uint64
	PathID       string
	Processed    bool
}

// NewStreamAggregator creates a new secondary stream aggregator
func NewStreamAggregator(logger logger.Logger) *StreamAggregator {
	config := &SecondaryAggregatorConfig{
		MaxSecondaryStreams:      1000,
		MaxPendingData:           100,
		ReorderTimeout:           100 * time.Millisecond,
		AggregationBatchSize:     64,
		BufferSize:               65536,
		CleanupInterval:          30 * time.Second,
		ParallelWorkers:          runtime.NumCPU(),
		EnableParallelProcessing: true,
	}

	ssa := &StreamAggregator{
		secondaryStreams: make(map[uint64]*SecondaryStreamState),
		streamMappings:   make(map[uint64]uint64),
		kwikStreams:      make(map[uint64]*KwikStreamAggregationState),
		config:           config,
		stats: &internalSecondaryAggregationStats{
			LastUpdate: time.Now(),
		},
		// metrics removed to avoid import cycle
		batchChannel:  make(chan []*DataFrame, 100),
		resultChannel: make(chan *AggregationResult, 100),
		logger:        logger,
	}

	// Initialize worker pool if parallel processing is enabled
	if config.EnableParallelProcessing {
		ssa.workerPool = NewAggregationWorkerPool(config.ParallelWorkers)
		ssa.workerPool.Start()
	}

	return ssa
}

// AggregateSecondaryData aggregates data from a secondary stream into the appropriate KWIK stream
func (ssa *StreamAggregator) AggregateSecondaryData(data *DataFrame) error {
	if data == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "secondary stream data is nil", nil)
	}

	// Validate the data
	if err := ssa.validateSecondaryData(data); err != nil {
		return err
	}

	// Get or create secondary stream state
	secondaryState, err := ssa.getOrCreateSecondaryStream(data.StreamID, data.PathID, data.KwikStreamID)
	if err != nil {
		return err
	}

	// Get or create KWIK stream aggregation state
	kwikState, err := ssa.getOrCreateKwikStream(data.KwikStreamID)
	if err != nil {
		return err
	}

	// Add data to secondary stream
	if err := ssa.addDataToSecondaryStream(secondaryState, data); err != nil {
		return err
	}

	// Aggregate data into KWIK stream
	if err := ssa.aggregateIntoKwikStream(kwikState, data); err != nil {
		return err
	}

	// Update statistics
	ssa.updateAggregationStats(data)

	// Record metrics (removed to avoid import cycle)

	return nil
}

// SetStreamMapping sets the mapping between a secondary stream and a KWIK stream
func (ssa *StreamAggregator) SetStreamMapping(secondaryStreamID, kwikStreamID uint64, pathID string) error {
	if secondaryStreamID == 0 || kwikStreamID == 0 {
		return utils.NewKwikError(utils.ErrInvalidFrame, "invalid stream IDs", nil)
	}

	ssa.mappingsMutex.Lock()
	defer ssa.mappingsMutex.Unlock()

	// Check if mapping already exists
	if existingKwikID, exists := ssa.streamMappings[secondaryStreamID]; exists {
		if existingKwikID != kwikStreamID {
			return utils.NewKwikError(utils.ErrStreamCreationFailed,
				fmt.Sprintf("secondary stream %d already mapped to KWIK stream %d",
					secondaryStreamID, existingKwikID), nil)
		}
		return nil // Mapping already exists and is correct
	}

	ssa.streamMappings[secondaryStreamID] = kwikStreamID

	// Create or update secondary stream state
	_, err := ssa.getOrCreateSecondaryStream(secondaryStreamID, pathID, kwikStreamID)
	if err != nil {
		// Remove mapping if stream creation failed
		delete(ssa.streamMappings, secondaryStreamID)
		return err
	}

	return nil
}

// RemoveStreamMapping removes the mapping for a secondary stream
func (ssa *StreamAggregator) RemoveStreamMapping(secondaryStreamID uint64) error {
	ssa.mappingsMutex.Lock()
	kwikStreamID, exists := ssa.streamMappings[secondaryStreamID]
	if exists {
		delete(ssa.streamMappings, secondaryStreamID)
	}
	ssa.mappingsMutex.Unlock()

	if !exists {
		return nil // Mapping doesn't exist, nothing to do
	}

	// Remove secondary stream state
	ssa.streamsMutex.Lock()
	delete(ssa.secondaryStreams, secondaryStreamID)
	ssa.streamsMutex.Unlock()

	// Update KWIK stream state
	ssa.kwikMutex.Lock()
	if kwikState, exists := ssa.kwikStreams[kwikStreamID]; exists {
		kwikState.mutex.Lock()
		delete(kwikState.SecondaryStreams, secondaryStreamID)
		kwikState.mutex.Unlock()
	}
	ssa.kwikMutex.Unlock()

	return nil
}

// GetAggregatedData returns the aggregated data for a KWIK stream
func (ssa *StreamAggregator) GetAggregatedData(kwikStreamID uint64) ([]byte, error) {
	if ssa.logger != nil {
		ssa.logger.Debug("StreamAggregator.GetAggregatedData called", kwikStreamID)
	}

	ssa.kwikMutex.RLock()
	kwikState, exists := ssa.kwikStreams[kwikStreamID]
	ssa.kwikMutex.RUnlock()

	if !exists {
		if ssa.logger != nil {
			ssa.logger.Debug("StreamAggregator.GetAggregatedData: KWIK stream not found", kwikStreamID)
		}
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"KWIK stream not found", nil)
	}

	kwikState.mutex.RLock()
	defer kwikState.mutex.RUnlock()

	if ssa.logger != nil {
		ssa.logger.Debug("StreamAggregator.GetAggregatedData: buffer length", kwikStreamID, len(kwikState.AggregatedBuffer))
	}

	// Return only contiguous data (up to first gap)
	contiguousLength := 0
	for i, b := range kwikState.AggregatedBuffer {
		if b == 0 {
			// Found a gap, check if it's a real gap or just trailing zeros
			// Look ahead to see if there's more data after this zero
			hasDataAfter := false
			for j := i + 1; j < len(kwikState.AggregatedBuffer); j++ {
				if kwikState.AggregatedBuffer[j] != 0 {
					hasDataAfter = true
					break
				}
			}
			if hasDataAfter {
				// This is a gap in the middle, stop here
				if ssa.logger != nil {
					ssa.logger.Debug("StreamAggregator.GetAggregatedData: found gap at position", i)
				}
				break
			}
		}
		contiguousLength = i + 1
	}

	if ssa.logger != nil {
		ssa.logger.Debug("StreamAggregator.GetAggregatedData: returning contiguous bytes", contiguousLength, kwikStreamID)
	}

	// Return copy of contiguous data only
	data := make([]byte, contiguousLength)
	copy(data, kwikState.AggregatedBuffer[:contiguousLength])

	return data, nil
}

// ConsumeAggregatedData returns and removes the specified amount of data from the aggregated buffer
// This implements the "coulissage vers l'avant" (forward sliding) mechanism
func (ssa *StreamAggregator) ConsumeAggregatedData(kwikStreamID uint64, bytesToConsume int) ([]byte, error) {
	if ssa.logger != nil {
		ssa.logger.Debug("StreamAggregator.ConsumeAggregatedData called", kwikStreamID, bytesToConsume)
	}

	ssa.kwikMutex.RLock()
	kwikState, exists := ssa.kwikStreams[kwikStreamID]
	ssa.kwikMutex.RUnlock()

	if !exists {
		if ssa.logger != nil {
			ssa.logger.Debug("StreamAggregator.ConsumeAggregatedData: KWIK stream not found", kwikStreamID)
		}
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"KWIK stream not found", nil)
	}

	kwikState.mutex.Lock()
	defer kwikState.mutex.Unlock()

	// Check if we have enough contiguous data to consume
	contiguousLength := 0
	for i, b := range kwikState.AggregatedBuffer {
		if b == 0 {
			// Found a gap, check if it's a real gap or just trailing zeros
			hasDataAfter := false
			for j := i + 1; j < len(kwikState.AggregatedBuffer); j++ {
				if kwikState.AggregatedBuffer[j] != 0 {
					hasDataAfter = true
					break
				}
			}
			if hasDataAfter {
				// This is a gap in the middle, stop here
				break
			}
		}
		contiguousLength = i + 1
	}

	if bytesToConsume > contiguousLength {
		if ssa.logger != nil {
			ssa.logger.Debug("StreamAggregator.ConsumeAggregatedData: not enough contiguous data", bytesToConsume, contiguousLength)
		}
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"not enough contiguous data to consume", nil)
	}

	// Extract the data to return
	data := make([]byte, bytesToConsume)
	copy(data, kwikState.AggregatedBuffer[:bytesToConsume])

	// Slide the remaining data forward (coulissage vers l'avant)
	remainingLength := len(kwikState.AggregatedBuffer) - bytesToConsume
	if remainingLength > 0 {
		// Move remaining data to the beginning of the buffer
		copy(kwikState.AggregatedBuffer[:remainingLength], kwikState.AggregatedBuffer[bytesToConsume:])
		// Clear the end of the buffer
		for i := remainingLength; i < len(kwikState.AggregatedBuffer); i++ {
			kwikState.AggregatedBuffer[i] = 0
		}
		// Resize the buffer to remove the consumed data
		kwikState.AggregatedBuffer = kwikState.AggregatedBuffer[:remainingLength]
	} else {
		// All data consumed, reset buffer
		kwikState.AggregatedBuffer = kwikState.AggregatedBuffer[:0]
	}

	if ssa.logger != nil {
		ssa.logger.Debug("StreamAggregator.ConsumeAggregatedData: consumed bytes, remaining length", bytesToConsume, len(kwikState.AggregatedBuffer))
	}

	return data, nil
}

// GetSecondaryStreamStats returns statistics for secondary stream aggregation
func (ssa *StreamAggregator) GetSecondaryStreamStats() *SecondaryAggregationStats {
	// Return copy of stats (no mutex needed since we're returning a copy)
	return &SecondaryAggregationStats{
		ActiveSecondaryStreams: ssa.stats.ActiveSecondaryStreams,
		ActiveKwikStreams:      ssa.stats.ActiveKwikStreams,
		TotalBytesAggregated:   ssa.stats.TotalBytesAggregated,
		TotalDataFrames:        ssa.stats.TotalDataFrames,
		AggregationLatency:     ssa.stats.AggregationLatency,
		ReorderingEvents:       ssa.stats.ReorderingEvents,
		DroppedFrames:          ssa.stats.DroppedFrames,
		LastUpdate:             ssa.stats.LastUpdate,
	}
}

// validateSecondaryData validates secondary stream data
func (ssa *StreamAggregator) validateSecondaryData(data *DataFrame) error {
	if data.StreamID == 0 {
		return utils.NewKwikError(utils.ErrInvalidFrame, "invalid secondary stream ID: cannot be 0", nil)
	}
	if data.KwikStreamID == 0 {
		return utils.NewKwikError(utils.ErrInvalidFrame, "invalid KWIK stream ID: cannot be 0", nil)
	}
	if data.PathID == "" {
		return utils.NewKwikError(utils.ErrInvalidFrame, "invalid path ID", nil)
	}
	if len(data.Data) == 0 {
		return utils.NewKwikError(utils.ErrInvalidFrame, "empty data", nil)
	}
	return nil
}

// getOrCreateSecondaryStream gets or creates a secondary stream state
func (ssa *StreamAggregator) getOrCreateSecondaryStream(streamID uint64, pathID string, kwikStreamID uint64) (*SecondaryStreamState, error) {
	ssa.streamsMutex.Lock()
	defer ssa.streamsMutex.Unlock()

	if state, exists := ssa.secondaryStreams[streamID]; exists {
		return state, nil
	}

	// Check limits
	if len(ssa.secondaryStreams) >= ssa.config.MaxSecondaryStreams {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"maximum secondary streams reached", nil)
	}

	state := &SecondaryStreamState{
		StreamID:        streamID,
		PathID:          pathID,
		KwikStreamID:    kwikStreamID,
		CurrentOffset:   0,
		LastActivity:    time.Now(),
		BytesReceived:   0,
		BytesAggregated: 0,
		State:           SecondaryStreamStateActive,
		PendingData:     make(map[uint64]*DataFrame),
	}

	ssa.secondaryStreams[streamID] = state
	return state, nil
}

// getOrCreateKwikStream gets or creates a KWIK stream aggregation state
func (ssa *StreamAggregator) getOrCreateKwikStream(streamID uint64) (*KwikStreamAggregationState, error) {
	ssa.kwikMutex.Lock()
	defer ssa.kwikMutex.Unlock()

	if state, exists := ssa.kwikStreams[streamID]; exists {
		return state, nil
	}

	state := &KwikStreamAggregationState{
		StreamID:             streamID,
		NextExpectedOffset:   0,
		SecondaryStreams:     make(map[uint64]bool),
		PendingData:          make(map[uint64]*DataFrame),
		AggregatedBuffer:     make([]byte, 0, ssa.config.BufferSize),
		LastActivity:         time.Now(),
		TotalBytesAggregated: 0,
	}

	ssa.kwikStreams[streamID] = state
	return state, nil
}

// addDataToSecondaryStream adds data to a secondary stream
func (ssa *StreamAggregator) addDataToSecondaryStream(state *SecondaryStreamState, data *DataFrame) error {
	state.mutex.Lock()
	defer state.mutex.Unlock()

	// Check if we already have data at this offset
	if _, exists := state.PendingData[data.Offset]; exists {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			fmt.Sprintf("duplicate data at offset %d", data.Offset), nil)
	}

	// Check pending data limits
	if len(state.PendingData) >= ssa.config.MaxPendingData {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			"maximum pending data reached for secondary stream", nil)
	}

	state.PendingData[data.Offset] = data
	state.BytesReceived += uint64(len(data.Data))
	state.LastActivity = time.Now()

	return nil
}

// aggregateIntoKwikStream aggregates data into a KWIK stream
func (ssa *StreamAggregator) aggregateIntoKwikStream(kwikState *KwikStreamAggregationState, data *DataFrame) error {
	kwikState.mutex.Lock()
	defer kwikState.mutex.Unlock()

	// Add secondary stream to the set
	kwikState.SecondaryStreams[data.StreamID] = true

	// Handle data placement based on offset
	// Only place data if it's within a reasonable range of current buffer or extends it directly
	maxReasonableOffset := uint64(len(kwikState.AggregatedBuffer)) + 1024 // Allow reasonable gaps

	if data.Offset <= maxReasonableOffset {
		// Data can be placed now
		endOffset := data.Offset + uint64(len(data.Data))

		// Extend buffer if necessary
		if endOffset > uint64(len(kwikState.AggregatedBuffer)) {
			newSize := int(endOffset)
			// Extend the buffer with zero bytes
			extension := make([]byte, newSize-len(kwikState.AggregatedBuffer))
			kwikState.AggregatedBuffer = append(kwikState.AggregatedBuffer, extension...)
		}

		// Copy data at the correct offset
		copy(kwikState.AggregatedBuffer[data.Offset:], data.Data)

		// Update next expected offset if this data extends beyond current position
		if endOffset > kwikState.NextExpectedOffset {
			kwikState.NextExpectedOffset = endOffset
		}

		kwikState.TotalBytesAggregated += uint64(len(data.Data))
		kwikState.LastActivity = time.Now()

		// Check if we can aggregate any pending data
		ssa.aggregatePendingData(kwikState)
	} else {
		// Data is too far out of order, store for later
		if len(kwikState.PendingData) >= ssa.config.MaxPendingData {
			return utils.NewKwikError(utils.ErrStreamCreationFailed,
				"maximum pending data reached for KWIK stream", nil)
		}
		kwikState.PendingData[data.Offset] = data
	}

	return nil
}

// aggregatePendingData aggregates any pending data that can now be placed
func (ssa *StreamAggregator) aggregatePendingData(kwikState *KwikStreamAggregationState) {
	// Keep processing until no more changes are made
	changed := true
	for changed {
		changed = false

		// Process all pending data that can now be placed
		for offset, data := range kwikState.PendingData {
			// Check if this data can be placed (either within current buffer or extends it reasonably)
			maxReasonableOffset := kwikState.NextExpectedOffset + 1024 // Allow some reasonable gap
			if offset <= maxReasonableOffset {
				// This data can now be placed
				endOffset := offset + uint64(len(data.Data))

				// Extend buffer if necessary
				if endOffset > uint64(len(kwikState.AggregatedBuffer)) {
					newSize := int(endOffset)
					// Extend the buffer
					extension := make([]byte, newSize-len(kwikState.AggregatedBuffer))
					kwikState.AggregatedBuffer = append(kwikState.AggregatedBuffer, extension...)
				}

				// Copy data at the correct offset
				copy(kwikState.AggregatedBuffer[offset:], data.Data)

				// Update next expected offset if this data extends beyond current position
				if endOffset > kwikState.NextExpectedOffset {
					kwikState.NextExpectedOffset = endOffset
				}

				kwikState.TotalBytesAggregated += uint64(len(data.Data))

				// Remove from pending
				delete(kwikState.PendingData, offset)
				changed = true
			}
		}
	}
}

// updateAggregationStats updates aggregation statistics
func (ssa *StreamAggregator) updateAggregationStats(data *DataFrame) {
	ssa.stats.mutex.Lock()
	defer ssa.stats.mutex.Unlock()

	ssa.stats.TotalDataFrames++
	ssa.stats.TotalBytesAggregated += uint64(len(data.Data))
	ssa.stats.LastUpdate = time.Now()

	// Update active stream counts
	ssa.streamsMutex.RLock()
	ssa.stats.ActiveSecondaryStreams = len(ssa.secondaryStreams)
	ssa.streamsMutex.RUnlock()

	ssa.kwikMutex.RLock()
	ssa.stats.ActiveKwikStreams = len(ssa.kwikStreams)
	ssa.kwikMutex.RUnlock()
}

// CloseSecondaryStream closes a secondary stream
func (ssa *StreamAggregator) CloseSecondaryStream(streamID uint64) error {
	ssa.streamsMutex.Lock()
	state, exists := ssa.secondaryStreams[streamID]
	if exists {
		state.mutex.Lock()
		state.State = SecondaryStreamStateClosed
		state.mutex.Unlock()
	}
	ssa.streamsMutex.Unlock()

	if !exists {
		return nil // Stream doesn't exist, nothing to do
	}

	// Remove the mapping
	return ssa.RemoveStreamMapping(streamID)
}

// CloseKwikStream closes a KWIK stream and all associated secondary streams
func (ssa *StreamAggregator) CloseKwikStream(streamID uint64) error {
	ssa.kwikMutex.Lock()
	kwikState, exists := ssa.kwikStreams[streamID]
	if exists {
		delete(ssa.kwikStreams, streamID)
	}
	ssa.kwikMutex.Unlock()

	if !exists {
		return nil // Stream doesn't exist, nothing to do
	}

	// Close all associated secondary streams
	kwikState.mutex.RLock()
	secondaryStreamIDs := make([]uint64, 0, len(kwikState.SecondaryStreams))
	for streamID := range kwikState.SecondaryStreams {
		secondaryStreamIDs = append(secondaryStreamIDs, streamID)
	}
	kwikState.mutex.RUnlock()

	for _, secondaryStreamID := range secondaryStreamIDs {
		ssa.CloseSecondaryStream(secondaryStreamID)
	}

	return nil
}

// NewAggregationWorkerPool creates a new worker pool for parallel aggregation
func NewAggregationWorkerPool(workers int) *AggregationWorkerPool {
	if workers <= 0 {
		workers = runtime.NumCPU()
	}

	return &AggregationWorkerPool{
		workers:       workers,
		workChannel:   make(chan *AggregationWork, workers*2),
		resultChannel: make(chan *AggregationResult, workers*2),
		stopChannel:   make(chan struct{}),
	}
}

// Start starts the worker pool
func (pool *AggregationWorkerPool) Start() {
	pool.mutex.Lock()
	defer pool.mutex.Unlock()

	if pool.running {
		return
	}

	pool.running = true

	for i := 0; i < pool.workers; i++ {
		pool.wg.Add(1)
		go pool.worker(i)
	}
}

// Stop stops the worker pool
func (pool *AggregationWorkerPool) Stop() {
	pool.mutex.Lock()
	defer pool.mutex.Unlock()

	if !pool.running {
		return
	}

	pool.running = false
	close(pool.stopChannel)
	pool.wg.Wait()
}

// SubmitWork submits work to the worker pool
func (pool *AggregationWorkerPool) SubmitWork(work *AggregationWork) error {
	pool.mutex.RLock()
	defer pool.mutex.RUnlock()

	if !pool.running {
		return fmt.Errorf("worker pool is not running")
	}

	select {
	case pool.workChannel <- work:
		return nil
	default:
		return fmt.Errorf("worker pool is full")
	}
}

// GetResult gets a result from the worker pool
func (pool *AggregationWorkerPool) GetResult() (*AggregationResult, bool) {
	select {
	case result := <-pool.resultChannel:
		return result, true
	default:
		return nil, false
	}
}

// worker is the worker goroutine that processes aggregation work
func (pool *AggregationWorkerPool) worker(_ int) {
	defer pool.wg.Done()

	for {
		select {
		case work := <-pool.workChannel:
			startTime := time.Now()
			result := pool.processWork(work)
			result.ProcessingTime = time.Since(startTime)

			select {
			case pool.resultChannel <- result:
			case <-pool.stopChannel:
				return
			}

		case <-pool.stopChannel:
			return
		}
	}
}

// processWork processes a batch of aggregation work
func (pool *AggregationWorkerPool) processWork(work *AggregationWork) *AggregationResult {
	result := &AggregationResult{
		BatchID:       work.BatchID,
		ProcessedData: make([]*ProcessedSecondaryData, 0, len(work.Data)),
	}

	for _, data := range work.Data {
		processed := &ProcessedSecondaryData{
			StreamID:     data.StreamID,
			KwikStreamID: data.KwikStreamID,
			Data:         make([]byte, len(data.Data)),
			Offset:       data.Offset,
			PathID:       data.PathID,
			Processed:    true,
		}

		// Copy data to avoid race conditions
		copy(processed.Data, data.Data)

		result.ProcessedData = append(result.ProcessedData, processed)
	}

	return result
}

// AggregateSecondaryDataBatch processes multiple secondary stream data items in parallel
func (ssa *StreamAggregator) AggregateSecondaryDataBatch(dataItems []*DataFrame) error {
	if len(dataItems) == 0 {
		return nil
	}

	// If parallel processing is disabled or batch is small, process sequentially
	if !ssa.config.EnableParallelProcessing || len(dataItems) < ssa.config.AggregationBatchSize/2 {
		for _, data := range dataItems {
			if err := ssa.AggregateSecondaryData(data); err != nil {
				return err
			}
		}
		return nil
	}

	// Process in parallel using worker pool
	batchID := uint64(time.Now().UnixNano())
	work := &AggregationWork{
		Data:      dataItems,
		BatchID:   batchID,
		Timestamp: time.Now(),
	}

	if err := ssa.workerPool.SubmitWork(work); err != nil {
		// Fallback to sequential processing if worker pool is full
		for _, data := range dataItems {
			if err := ssa.AggregateSecondaryData(data); err != nil {
				return err
			}
		}
		return nil
	}

	// Wait for result and process it
	// Note: In a real implementation, this might be handled asynchronously
	// For now, we'll wait for the result to maintain consistency
	for {
		if result, ok := ssa.workerPool.GetResult(); ok && result.BatchID == batchID {
			if result.Error != nil {
				return result.Error
			}

			// Process the results sequentially to maintain order
			for _, processed := range result.ProcessedData {
				data := &DataFrame{
					StreamID:     processed.StreamID,
					PathID:       processed.PathID,
					Data:         processed.Data,
					Offset:       processed.Offset,
					KwikStreamID: processed.KwikStreamID,
					Timestamp:    work.Timestamp,
				}

				if err := ssa.AggregateSecondaryData(data); err != nil {
					return err
				}
			}

			// Update latency statistics
			ssa.stats.mutex.Lock()
			ssa.stats.AggregationLatency = result.ProcessingTime
			ssa.stats.mutex.Unlock()

			break
		}

		// Small delay to avoid busy waiting
		time.Sleep(time.Microsecond * 100)
	}

	return nil
}

// GetMetrics returns the metrics instance for external monitoring
// Removed to avoid import cycle - metrics should be accessed through other means

// Shutdown gracefully shuts down the aggregator
func (ssa *StreamAggregator) Shutdown() error {
	if ssa.workerPool != nil {
		ssa.workerPool.Stop()
	}

	// Close channels
	if ssa.batchChannel != nil {
		close(ssa.batchChannel)
	}
	if ssa.resultChannel != nil {
		close(ssa.resultChannel)
	}

	return nil
}

// SetPresentationManager sets the data presentation manager for integration
func (ssa *StreamAggregator) SetPresentationManager(manager DataPresentationManagerInterface) {
	ssa.presentationMutex.Lock()
	defer ssa.presentationMutex.Unlock()
	ssa.presentationManager = manager
}

// GetPresentationManager returns the current presentation manager
func (ssa *StreamAggregator) GetPresentationManager() DataPresentationManagerInterface {
	ssa.presentationMutex.RLock()
	defer ssa.presentationMutex.RUnlock()
	return ssa.presentationManager
}

// AggregateSecondaryDataWithPresentation aggregates data using the presentation manager
func (ssa *StreamAggregator) AggregateDataFrames(frame *DataFrame) error {
	if frame == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "secondary stream data is nil", nil)
	}

	ssa.logger.Debug(fmt.Sprintf("[AGGREGATOR] Processing frame: streamID=%d, offset=%d, size=%d",
		frame.StreamID, frame.Offset, len(frame.Data)))

	// Validate the data
	if err := ssa.validateSecondaryData(frame); err != nil {
		ssa.logger.Debug(fmt.Sprintf("[AGGREGATOR] Validation failed for stream %d: %v", frame.StreamID, err))
		return err
	}

	// Check if presentation manager is available
	ssa.presentationMutex.RLock()
	presentationManager := ssa.presentationManager
	ssa.presentationMutex.RUnlock()

	if presentationManager == nil {
		ssa.logger.Debug("[AGGREGATOR] Warning: No presentation manager set")
	} else {
		ssa.logger.Debug(fmt.Sprintf("[AGGREGATOR] Found presentation manager for stream %d", frame.StreamID))
	}

	if presentationManager != nil {
		// Use presentation manager for new architecture
		return ssa.aggregateDataFrame(frame, presentationManager)
	}
	return fmt.Errorf("presentation manager not set, cannot aggregate data frame")
}

// aggregateDataFrame aggregates data using the presentation manager
func (ssa *StreamAggregator) aggregateDataFrame(frame *DataFrame, manager DataPresentationManagerInterface) error {
	// Log frame metadata at the start
	ssa.logger.Debug("Starting frame aggregation",
		"streamID", frame.StreamID,
		"kwikStreamID", frame.KwikStreamID,
		"offset", frame.Offset,
		"dataLength", len(frame.Data),
		"path", frame.PathID)
	if frame == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "frame cannot be nil", nil)
	}

	// Log detailed information about the frame
	ssa.logger.Debug(fmt.Sprintf("[AGGREGATOR] Starting aggregation for stream %d (KWIK stream: %d), data length: %d, offset: %d, path: %s",
		frame.StreamID, frame.KwikStreamID, len(frame.Data), frame.Offset, frame.PathID))

	// Validate input data
	if len(frame.Data) == 0 {
		ssa.logger.Warn("Empty data frame received, skipping",
			"streamID", frame.StreamID,
			"kwikStreamID", frame.KwikStreamID,
			"offset", frame.Offset)
		return utils.NewKwikError(utils.ErrInvalidFrame, "empty data frame", nil)
	}

	// Log data samples for debugging
	sampleSize := 16
	headerSample := ""
	trailerSample := ""

	// Get header and trailer samples
	if len(frame.Data) >= sampleSize {
		headerSample = fmt.Sprintf("% x", frame.Data[:sampleSize])
		if len(frame.Data) > sampleSize*2 {
			trailerSample = fmt.Sprintf("% x", frame.Data[len(frame.Data)-sampleSize:])
			headerSample += "..." + trailerSample
		} else {
			headerSample = fmt.Sprintf("% x", frame.Data)
		}
	} else {
		headerSample = fmt.Sprintf("% x", frame.Data)
	}

	// Check for JSON data
	isJSON := false
	if len(frame.Data) > 1 {
		firstChar := frame.Data[0]
		lastChar := frame.Data[len(frame.Data)-1]
		isJSON = (firstChar == '{' && lastChar == '}') || (firstChar == '[' && lastChar == ']')
	}

	ssa.logger.Debug("Data frame details",
		"streamID", frame.StreamID,
		"kwikStreamID", frame.KwikStreamID,
		"offset", frame.Offset,
		"dataLength", len(frame.Data),
		"dataSample", headerSample,
		"isJSON", isJSON,
		"path", frame.PathID)

	// Check if backpressure is active for the target stream
	if manager.IsBackpressureActive(frame.KwikStreamID) {
		return utils.NewBackpressureError(frame.KwikStreamID, "stream is under backpressure")
	}

	// Create metadata for the data with validation info
	metadata := &DataFrameMetadata{
		Offset:     frame.Offset,
		Length:     uint64(len(frame.Data)),
		Timestamp:  frame.Timestamp,
		SourcePath: frame.PathID,
		Flags:      SecondaryDataFlagNone,
		Properties: map[string]interface{}{
			"isJSON":     isJSON,
			"dataLength": len(frame.Data),
		},
	}

	// Log before writing to presentation manager
	writeStart := time.Now()
	ssa.logger.Debug("Writing to presentation manager",
		"streamID", frame.StreamID,
		"kwikStreamID", frame.KwikStreamID,
		"offset", frame.Offset,
		"dataLength", len(frame.Data),
		"isJSON", isJSON)

	// Write data to the presentation manager with timeout
	errCh := make(chan error, 1)
	go func() {
		errCh <- manager.WriteToStream(frame.KwikStreamID, frame.Data, frame.Offset, metadata)
	}()

	var err error
	var writeDuration time.Duration

	select {
	case err = <-errCh:
		writeDuration = time.Since(writeStart)
		// Operation completed
	case <-time.After(10 * time.Second):
		writeDuration = time.Since(writeStart)
		errMsg := "timeout writing to presentation manager"
		ssa.logger.Error(errMsg,
			"streamID", frame.StreamID,
			"kwikStreamID", frame.KwikStreamID,
			"offset", frame.Offset,
			"duration", writeDuration.String())
		return utils.NewTimeoutError("write to presentation manager", 10*time.Second)
	}

	if err != nil {
		errMsg := fmt.Sprintf("failed to write to presentation manager: %v", err)
		ssa.logger.Error(errMsg,
			"streamID", frame.StreamID,
			"kwikStreamID", frame.KwikStreamID,
			"offset", frame.Offset,
			"error", err,
			"duration", writeDuration.String())
		return utils.NewKwikError(utils.ErrStreamCreationFailed, errMsg, err)
	}

	// Log successful write
	ssa.logger.Debug("Successfully wrote to presentation manager",
		"streamID", frame.StreamID,
		"kwikStreamID", frame.KwikStreamID,
		"offset", frame.Offset,
		"dataLength", len(frame.Data),
		"duration", writeDuration.String())

	// Update statistics
	ssa.updateAggregationStats(frame)

	// Update secondary stream state with timeout
	stateCh := make(chan *SecondaryStreamState, 1)
	errCh2 := make(chan error, 1)
	go func() {
		state, err2 := ssa.getOrCreateSecondaryStream(frame.StreamID, frame.PathID, frame.KwikStreamID)
		if err2 != nil {
			errCh2 <- err2
		} else {
			stateCh <- state
			errCh2 <- nil
		}
	}()

	select {
	case err = <-errCh2:
		if err != nil {
			return err
		}
	case <-time.After(5 * time.Second):
		errMsg := "timeout updating secondary stream state"
		ssa.logger.Error(errMsg,
			"streamID", frame.StreamID,
			"kwikStreamID", frame.KwikStreamID)
		return utils.NewTimeoutError("update secondary stream state", 5*time.Second)
	}

	secondaryState := <-stateCh

	secondaryState.mutex.Lock()
	defer secondaryState.mutex.Unlock()

	secondaryState.BytesAggregated += uint64(len(frame.Data))
	secondaryState.LastActivity = time.Now()

	ssa.logger.Debug("Successfully aggregated data",
		"streamID", frame.StreamID,
		"kwikStreamID", frame.KwikStreamID,
		"offset", frame.Offset,
		"totalBytesAggregated", secondaryState.BytesAggregated)

	return nil
}

// GetAggregatedDataFromPresentation reads data from the presentation manager
func (ssa *StreamAggregator) GetAggregatedDataFromPresentation(kwikStreamID uint64) ([]byte, error) {
	ssa.presentationMutex.RLock()
	presentationManager := ssa.presentationManager
	ssa.presentationMutex.RUnlock()

	if presentationManager != nil {
		// This would require extending the interface to support reading
		// For now, we'll fall back to legacy method
		return ssa.GetAggregatedData(kwikStreamID)
	}

	return ssa.GetAggregatedData(kwikStreamID)
}

// ConsumeAggregatedDataFromPresentation consumes data from the presentation manager
func (ssa *StreamAggregator) ConsumeAggregatedDataFromPresentation(kwikStreamID uint64, bytesToConsume int) ([]byte, error) {
	ssa.presentationMutex.RLock()
	presentationManager := ssa.presentationManager
	ssa.presentationMutex.RUnlock()

	if presentationManager != nil {
		// This would require extending the interface to support consumption
		// For now, we'll fall back to legacy method
		return ssa.ConsumeAggregatedData(kwikStreamID, bytesToConsume)
	}

	return ssa.ConsumeAggregatedData(kwikStreamID, bytesToConsume)
}

// SecondaryDataMetadata represents metadata for secondary stream data chunks
type DataFrameMetadata struct {
	Offset     uint64                 `json:"offset"`
	Length     uint64                 `json:"length"`
	Timestamp  time.Time              `json:"timestamp"`
	SourcePath string                 `json:"source_path"`
	Checksum   uint32                 `json:"checksum"`
	Flags      SecondaryDataFlags     `json:"flags"`
	Properties map[string]interface{} `json:"properties"`
}

// SecondaryDataFlags defines flags for secondary stream data chunks
type SecondaryDataFlags uint32

const (
	SecondaryDataFlagNone        SecondaryDataFlags = 0      // No special flags
	SecondaryDataFlagEndOfStream SecondaryDataFlags = 1 << 0 // End of stream marker
	SecondaryDataFlagUrgent      SecondaryDataFlags = 1 << 1 // Urgent data
	SecondaryDataFlagCompressed  SecondaryDataFlags = 1 << 2 // Compressed data
)
