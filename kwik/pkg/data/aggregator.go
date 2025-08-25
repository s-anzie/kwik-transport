package data

import (
	"fmt"
	"sync"
	"time"

	"kwik/internal/utils"
)

// DataLogger interface to avoid circular imports
type DataLogger interface {
	Debug(msg string, keysAndValues ...interface{})
	Info(msg string, keysAndValues ...interface{})
	Warn(msg string, keysAndValues ...interface{})
	Error(msg string, keysAndValues ...interface{})
	Critical(msg string, keysAndValues ...interface{})
}

// DataAggregatorImpl implements the DataAggregator interface
// It handles aggregation of data from multiple paths for client-side sessions
type DataAggregatorImpl struct {
	// Path streams for aggregation
	pathStreams map[string]DataStream
	pathsMutex  sync.RWMutex

	// Aggregated streams
	aggregatedStreams map[uint64]*AggregatedStreamState
	streamsMutex      sync.RWMutex

	// Secondary stream aggregation
	secondaryAggregator *StreamAggregator
	offsetManager       *MultiSourceOffsetManager
	offsetCoordinator   OffsetCoordinator // For offset continuity validation

	// Stream mappings for secondary streams
	streamMappings map[uint64]uint64 // secondaryStreamID -> kwikStreamID
	mappingsMutex  sync.RWMutex

	// Configuration
	config *AggregatorConfig
	logger DataLogger
}

// AggregatorConfig holds configuration for the data aggregator
type AggregatorConfig struct {
	BufferSize        int
	MaxStreams        int
	AggregationDelay  time.Duration
	ReorderTimeout    time.Duration
	ReorderBufferSize int           // Maximum number of out-of-order frames to buffer
	MaxReorderDelay   time.Duration // Maximum time to wait for missing frames
	DuplicateWindow   int           // Number of recent frames to track for duplicate detection
	EnableDuplicateDetection bool   // Whether to enable duplicate detection
}

// AggregatedStreamState represents the state of an aggregated stream
type AggregatedStreamState struct {
	StreamID        uint64
	Buffer          []byte
	Offset          uint64
	LastActivity    time.Time
	PathBuffers     map[string][]byte
	Stats           *AggregationStats
	ReorderBuffer   *ReorderBuffer
	DuplicateTracker *DuplicateTracker
	NextExpectedSeq uint64
	mutex           sync.RWMutex
}

// ReorderBuffer manages out-of-order frame reordering
type ReorderBuffer struct {
	frames       map[uint64]*ReorderFrame // sequence number -> frame
	maxSize      int
	timeout      time.Duration
	nextExpected uint64
	
	// Enhanced timeout management
	timeoutTicker    *time.Ticker
	stopTimeoutChan  chan struct{}
	timeoutCallback  func([]*ReorderFrame) // Callback for timed-out frames
	
	// Buffer memory management
	currentMemoryUsage uint64
	maxMemoryUsage     uint64
	
	// Statistics
	timeoutCount     uint64
	forcedFlushCount uint64
	
	mutex        sync.RWMutex
}

// ReorderFrame represents a frame waiting for reordering in the aggregator
type ReorderFrame struct {
	Data        []byte
	Offset      uint64
	SequenceNum uint64
	PathID      string
	Timestamp   time.Time
	Delivered   bool
}

// DuplicateTracker tracks recently seen frames to detect duplicates
type DuplicateTracker struct {
	recentFrames map[uint64]*FrameSignature // sequence number -> signature
	maxSize      int
	mutex        sync.RWMutex
}

// FrameSignature represents a unique signature for a frame
type FrameSignature struct {
	SequenceNum uint64
	Offset      uint64
	DataHash    uint64    // Simple hash of data content
	PathID      string
	Timestamp   time.Time
}

// DuplicateStats contains statistics about duplicate detection
type DuplicateStats struct {
	TotalFramesProcessed uint64
	DuplicatesDetected   uint64
	DuplicatesByPath     map[string]uint64
	LastDuplicateTime    time.Time
}

// NewDataAggregator creates a new data aggregator
func NewDataAggregator(logger DataLogger) DataAggregator {
	return &DataAggregatorImpl{
		pathStreams:         make(map[string]DataStream),
		aggregatedStreams:   make(map[uint64]*AggregatedStreamState),
		secondaryAggregator: NewStreamAggregator(logger),
		offsetManager:       NewMultiSourceOffsetManager(),
		streamMappings:      make(map[uint64]uint64),
		logger:              logger,
		config: &AggregatorConfig{
			BufferSize:               utils.DefaultReadBufferSize,
			MaxStreams:               100,
			AggregationDelay:         10 * time.Millisecond,
			ReorderTimeout:           100 * time.Millisecond,
			ReorderBufferSize:        64,                     // Buffer up to 64 out-of-order frames
			MaxReorderDelay:          500 * time.Millisecond, // Wait up to 500ms for missing frames
			DuplicateWindow:          1000,                   // Track last 1000 frames for duplicates
			EnableDuplicateDetection: true,                   // Enable duplicate detection by default
		},
	}
}

// AddPath adds a path stream for aggregation
func (da *DataAggregatorImpl) AddPath(pathID string, stream DataStream) error {
	if pathID == "" {
		return utils.NewKwikError(utils.ErrInvalidFrame, "path ID is empty", nil)
	}

	if stream == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "stream is nil", nil)
	}

	da.pathsMutex.Lock()
	defer da.pathsMutex.Unlock()

	da.pathStreams[pathID] = stream
	return nil
}

// RemovePath removes a path stream from aggregation
func (da *DataAggregatorImpl) RemovePath(pathID string) error {
	da.pathsMutex.Lock()
	defer da.pathsMutex.Unlock()

	delete(da.pathStreams, pathID)
	return nil
}

// AggregateData aggregates data from multiple paths for a stream
func (da *DataAggregatorImpl) AggregateData(streamID uint64) ([]byte, error) {
	da.streamsMutex.RLock()
	streamState, exists := da.aggregatedStreams[streamID]
	da.streamsMutex.RUnlock()

	if !exists {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"aggregated stream not found", nil)
	}

	streamState.mutex.RLock()
	defer streamState.mutex.RUnlock()

	// Return copy of current buffer
	data := make([]byte, len(streamState.Buffer))
	copy(data, streamState.Buffer)

	return data, nil
}

// WriteToPath writes data to a specific path
func (da *DataAggregatorImpl) WriteToPath(pathID string, data []byte) error {
	da.pathsMutex.RLock()
	stream, exists := da.pathStreams[pathID]
	da.pathsMutex.RUnlock()

	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}

	_, err := stream.Write(data)
	return err
}

// CreateAggregatedStream creates a new aggregated stream
func (da *DataAggregatorImpl) CreateAggregatedStream(streamID uint64) error {
	da.streamsMutex.Lock()
	defer da.streamsMutex.Unlock()

	if _, exists := da.aggregatedStreams[streamID]; exists {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			"aggregated stream already exists", nil)
	}

	streamState := &AggregatedStreamState{
		StreamID:        streamID,
		Buffer:          make([]byte, 0, da.config.BufferSize),
		Offset:          0,
		LastActivity:    time.Now(),
		PathBuffers:     make(map[string][]byte),
		NextExpectedSeq: 0,
		ReorderBuffer: NewReorderBufferWithCallback(
			da.config.ReorderBufferSize,
			da.config.MaxReorderDelay,
			func(timedOutFrames []*ReorderFrame) {
				// Handle timed-out frames by processing them immediately
				da.handleTimedOutFrames(streamID, timedOutFrames)
			},
		),
		Stats: &AggregationStats{
			StreamID:           streamID,
			TotalBytesReceived: 0,
			BytesPerPath:       make(map[string]uint64),
			FramesReceived:     0,
			FramesPerPath:      make(map[string]uint64),
			LastActivity:       time.Now().UnixNano(),
			AggregationRatio:   0.0,
		},
	}

	da.aggregatedStreams[streamID] = streamState
	return nil
}

// CloseAggregatedStream closes an aggregated stream
func (da *DataAggregatorImpl) CloseAggregatedStream(streamID uint64) error {
	da.streamsMutex.Lock()
	defer da.streamsMutex.Unlock()

	delete(da.aggregatedStreams, streamID)
	return nil
}

// GetAggregationStats returns aggregation statistics for a stream
func (da *DataAggregatorImpl) GetAggregationStats(streamID uint64) (*AggregationStats, error) {
	da.streamsMutex.RLock()
	streamState, exists := da.aggregatedStreams[streamID]
	da.streamsMutex.RUnlock()

	if !exists {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"aggregated stream not found", nil)
	}

	streamState.mutex.RLock()
	defer streamState.mutex.RUnlock()

	// Return copy of stats
	stats := &AggregationStats{
		StreamID:           streamState.Stats.StreamID,
		TotalBytesReceived: streamState.Stats.TotalBytesReceived,
		BytesPerPath:       make(map[string]uint64),
		FramesReceived:     streamState.Stats.FramesReceived,
		FramesPerPath:      make(map[string]uint64),
		LastActivity:       streamState.Stats.LastActivity,
		AggregationRatio:   streamState.Stats.AggregationRatio,
	}

	// Copy maps
	for k, v := range streamState.Stats.BytesPerPath {
		stats.BytesPerPath[k] = v
	}
	for k, v := range streamState.Stats.FramesPerPath {
		stats.FramesPerPath[k] = v
	}

	return stats, nil
}

// AggregateSecondaryData aggregates data from a secondary stream into the appropriate KWIK stream
func (da *DataAggregatorImpl) AggregateSecondaryData(kwikStreamID uint64, secondaryData *DataFrame) error {
	if secondaryData == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "secondary stream data is nil", nil)
	}

	// Validate that the secondary stream is mapped to the KWIK stream
	da.mappingsMutex.RLock()
	mappedKwikStreamID, exists := da.streamMappings[secondaryData.StreamID]
	da.mappingsMutex.RUnlock()

	if !exists {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			fmt.Sprintf("secondary stream %d not mapped to any KWIK stream", secondaryData.StreamID), nil)
	}

	if mappedKwikStreamID != kwikStreamID {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			fmt.Sprintf("secondary stream %d mapped to KWIK stream %d, not %d",
				secondaryData.StreamID, mappedKwikStreamID, kwikStreamID), nil)
	}

	// Validate offset using the offset manager
	if err := da.offsetManager.ValidateOffset(kwikStreamID, secondaryData.Offset, secondaryData.PathID); err != nil {
		return fmt.Errorf("offset validation failed: %w", err)
	}

	// Update offset manager with the new data
	if err := da.offsetManager.UpdateOffset(kwikStreamID, secondaryData.PathID,
		secondaryData.StreamID, secondaryData.Offset, secondaryData.Data); err != nil {
		return fmt.Errorf("offset update failed: %w", err)
	}

	// Register data received with offset coordinator for gap detection
	if da.offsetCoordinator != nil {
		err := da.offsetCoordinator.RegisterDataReceived(kwikStreamID, int64(secondaryData.Offset), len(secondaryData.Data))
		if err != nil {
			// Log error but don't fail - this is for monitoring
			if da.logger != nil {
				da.logger.Debug("Failed to register data with offset coordinator", "error", err)
			}
		}
		
		// Check for gaps and request missing data if needed
		gaps, err := da.offsetCoordinator.ValidateOffsetContinuity(kwikStreamID)
		if err == nil && len(gaps) > 0 {
			// Request missing data for detected gaps
			err = da.offsetCoordinator.RequestMissingData(kwikStreamID, gaps)
			if err != nil && da.logger != nil {
				da.logger.Debug("Failed to request missing data", "streamID", kwikStreamID, "gaps", len(gaps), "error", err)
			}
		}
	}

	// Use the new reordering-capable processing
	_, err := da.ProcessFrameWithReordering(kwikStreamID, secondaryData)
	if err != nil {
		return fmt.Errorf("reordering aggregation failed: %w", err)
	}

	// Also update the secondary aggregator for compatibility
	if da.secondaryAggregator != nil {
		if err := da.secondaryAggregator.AggregateSecondaryData(secondaryData); err != nil {
			// Log error but don't fail - the reordering aggregation is primary
			// TODO: Add proper logging when logger is available
		}
	}

	return nil
}

// SetStreamMapping sets the mapping between a secondary stream and a KWIK stream
func (da *DataAggregatorImpl) SetStreamMapping(kwikStreamID uint64, secondaryStreamID uint64, pathID string) error {
	if kwikStreamID == 0 || secondaryStreamID == 0 {
		return utils.NewKwikError(utils.ErrInvalidFrame, "invalid stream IDs", nil)
	}

	if pathID == "" {
		return utils.NewKwikError(utils.ErrInvalidFrame, "path ID is empty", nil)
	}

	da.mappingsMutex.Lock()
	defer da.mappingsMutex.Unlock()

	// Check if secondary stream is already mapped
	if existingKwikID, exists := da.streamMappings[secondaryStreamID]; exists {
		if existingKwikID != kwikStreamID {
			return utils.NewKwikError(utils.ErrStreamCreationFailed,
				fmt.Sprintf("secondary stream %d already mapped to KWIK stream %d",
					secondaryStreamID, existingKwikID), nil)
		}
		return nil // Mapping already exists and is correct
	}

	// Set the mapping
	da.streamMappings[secondaryStreamID] = kwikStreamID

	// Register the path with the offset manager
	if err := da.offsetManager.RegisterSource(pathID); err != nil {
		delete(da.streamMappings, secondaryStreamID)
		return fmt.Errorf("failed to register source: %w", err)
	}

	// Set the mapping in the secondary aggregator
	if err := da.secondaryAggregator.SetStreamMapping(secondaryStreamID, kwikStreamID, pathID); err != nil {
		delete(da.streamMappings, secondaryStreamID)
		da.offsetManager.UnregisterSource(pathID)
		return fmt.Errorf("failed to set secondary aggregator mapping: %w", err)
	}

	return nil
}

// RemoveStreamMapping removes the mapping for a secondary stream
func (da *DataAggregatorImpl) RemoveStreamMapping(secondaryStreamID uint64) error {
	da.mappingsMutex.Lock()
	_, exists := da.streamMappings[secondaryStreamID]
	if exists {
		delete(da.streamMappings, secondaryStreamID)
	}
	da.mappingsMutex.Unlock()

	if !exists {
		return nil // Mapping doesn't exist, nothing to do
	}

	// Remove mapping from secondary aggregator
	if err := da.secondaryAggregator.RemoveStreamMapping(secondaryStreamID); err != nil {
		return fmt.Errorf("failed to remove secondary aggregator mapping: %w", err)
	}

	return nil
}

// ValidateOffset validates an offset for a KWIK stream from a specific source
func (da *DataAggregatorImpl) ValidateOffset(kwikStreamID uint64, offset uint64, sourcePathID string) error {
	return da.offsetManager.ValidateOffset(kwikStreamID, offset, sourcePathID)
}

// GetNextExpectedOffset returns the next expected offset for a KWIK stream
func (da *DataAggregatorImpl) GetNextExpectedOffset(kwikStreamID uint64) uint64 {
	offset, err := da.offsetManager.GetNextExpectedOffset(kwikStreamID)
	if err != nil {
		return 0
	}
	return offset
}

// GetSecondaryStreamStats returns statistics for secondary stream aggregation
func (da *DataAggregatorImpl) GetSecondaryStreamStats() *SecondaryAggregationStats {
	return da.secondaryAggregator.GetSecondaryStreamStats()
}

// GetOffsetManagerStats returns statistics for the offset manager
func (da *DataAggregatorImpl) GetOffsetManagerStats() *OffsetManagerStats {
	return da.offsetManager.GetOffsetManagerStats()
}

// updateAggregatedStreamFromSecondary updates the main aggregated stream with data from secondary streams
func (da *DataAggregatorImpl) updateAggregatedStreamFromSecondary(kwikStreamID uint64, secondaryData *DataFrame) error {
	da.streamsMutex.RLock()
	streamState, exists := da.aggregatedStreams[kwikStreamID]
	da.streamsMutex.RUnlock()

	if !exists {
		// Create the aggregated stream if it doesn't exist
		if err := da.CreateAggregatedStream(kwikStreamID); err != nil {
			return fmt.Errorf("failed to create aggregated stream: %w", err)
		}

		da.streamsMutex.RLock()
		streamState = da.aggregatedStreams[kwikStreamID]
		da.streamsMutex.RUnlock()
	}

	streamState.mutex.Lock()
	defer streamState.mutex.Unlock()

	// Update statistics
	streamState.Stats.TotalBytesReceived += uint64(len(secondaryData.Data))
	streamState.Stats.FramesReceived++
	streamState.Stats.LastActivity = time.Now().UnixNano()

	// Update per-path statistics
	if streamState.Stats.BytesPerPath == nil {
		streamState.Stats.BytesPerPath = make(map[string]uint64)
	}
	if streamState.Stats.FramesPerPath == nil {
		streamState.Stats.FramesPerPath = make(map[string]uint64)
	}

	streamState.Stats.BytesPerPath[secondaryData.PathID] += uint64(len(secondaryData.Data))
	streamState.Stats.FramesPerPath[secondaryData.PathID]++

	// Update path buffers
	if streamState.PathBuffers == nil {
		streamState.PathBuffers = make(map[string][]byte)
	}
	streamState.PathBuffers[secondaryData.PathID] = append(streamState.PathBuffers[secondaryData.PathID], secondaryData.Data...)

	// Get aggregated data from secondary aggregator and update main buffer
	aggregatedData, err := da.secondaryAggregator.GetAggregatedData(kwikStreamID)
	if err != nil {
		return fmt.Errorf("failed to get aggregated data: %w", err)
	}

	// Update the main buffer with aggregated data
	streamState.Buffer = aggregatedData
	streamState.LastActivity = time.Now()

	// Calculate aggregation ratio
	totalPathBytes := uint64(0)
	for _, bytes := range streamState.Stats.BytesPerPath {
		totalPathBytes += bytes
	}
	if totalPathBytes > 0 {
		streamState.Stats.AggregationRatio = float64(len(streamState.Buffer)) / float64(totalPathBytes)
	}

	return nil
}

// CloseSecondaryStream closes a secondary stream and removes its mapping
func (da *DataAggregatorImpl) CloseSecondaryStream(secondaryStreamID uint64) error {
	// Remove the mapping first
	if err := da.RemoveStreamMapping(secondaryStreamID); err != nil {
		return fmt.Errorf("failed to remove stream mapping: %w", err)
	}

	// Close the secondary stream in the aggregator
	if err := da.secondaryAggregator.CloseSecondaryStream(secondaryStreamID); err != nil {
		return fmt.Errorf("failed to close secondary stream: %w", err)
	}

	return nil
}

// CloseKwikStreamWithSecondaries closes a KWIK stream and all associated secondary streams
func (da *DataAggregatorImpl) CloseKwikStreamWithSecondaries(kwikStreamID uint64) error {
	// Close the KWIK stream in the secondary aggregator (this will close associated secondary streams)
	if err := da.secondaryAggregator.CloseKwikStream(kwikStreamID); err != nil {
		return fmt.Errorf("failed to close KWIK stream in secondary aggregator: %w", err)
	}

	// Close the offset tracking
	if err := da.offsetManager.CloseStream(kwikStreamID); err != nil {
		return fmt.Errorf("failed to close stream in offset manager: %w", err)
	}

	// Close the main aggregated stream
	if err := da.CloseAggregatedStream(kwikStreamID); err != nil {
		return fmt.Errorf("failed to close aggregated stream: %w", err)
	}

	// Remove any mappings for this KWIK stream
	da.mappingsMutex.Lock()
	secondaryStreamsToRemove := make([]uint64, 0)
	for secondaryStreamID, mappedKwikStreamID := range da.streamMappings {
		if mappedKwikStreamID == kwikStreamID {
			secondaryStreamsToRemove = append(secondaryStreamsToRemove, secondaryStreamID)
		}
	}
	for _, secondaryStreamID := range secondaryStreamsToRemove {
		delete(da.streamMappings, secondaryStreamID)
	}
	da.mappingsMutex.Unlock()

	return nil
}

// NewReorderBuffer creates a new reorder buffer
func NewReorderBuffer(maxSize int, timeout time.Duration) *ReorderBuffer {
	rb := &ReorderBuffer{
		frames:           make(map[uint64]*ReorderFrame),
		maxSize:          maxSize,
		timeout:          timeout,
		nextExpected:     0,
		stopTimeoutChan:  make(chan struct{}),
		maxMemoryUsage:   1024 * 1024 * 10, // 10MB default max memory
		currentMemoryUsage: 0,
		timeoutCount:     0,
		forcedFlushCount: 0,
	}
	
	// Start timeout management goroutine
	rb.startTimeoutManager()
	
	return rb
}

// NewReorderBufferWithCallback creates a new reorder buffer with timeout callback
func NewReorderBufferWithCallback(maxSize int, timeout time.Duration, callback func([]*ReorderFrame)) *ReorderBuffer {
	rb := NewReorderBuffer(maxSize, timeout)
	rb.timeoutCallback = callback
	return rb
}

// startTimeoutManager starts the background timeout management
func (rb *ReorderBuffer) startTimeoutManager() {
	// Check for timeouts every 10ms
	rb.timeoutTicker = time.NewTicker(10 * time.Millisecond)
	
	go func() {
		defer rb.timeoutTicker.Stop()
		
		for {
			select {
			case <-rb.timeoutTicker.C:
				rb.processTimeouts()
			case <-rb.stopTimeoutChan:
				return
			}
		}
	}()
}

// processTimeouts processes frames that have timed out
func (rb *ReorderBuffer) processTimeouts() {
	timedOutFrames := rb.GetTimedOutFrames()
	
	if len(timedOutFrames) > 0 {
		rb.mutex.Lock()
		rb.timeoutCount += uint64(len(timedOutFrames))
		rb.mutex.Unlock()
		
		// Call timeout callback if set
		if rb.timeoutCallback != nil {
			rb.timeoutCallback(timedOutFrames)
		}
	}
}

// AddFrame adds a frame to the reorder buffer
func (rb *ReorderBuffer) AddFrame(frame *ReorderFrame) error {
	rb.mutex.Lock()
	defer rb.mutex.Unlock()

	frameSize := uint64(len(frame.Data))

	// Check memory usage before adding frame
	if rb.currentMemoryUsage+frameSize > rb.maxMemoryUsage {
		// Force flush oldest frames to make room
		flushedFrames := rb.forceFlushOldestFrames(frameSize)
		rb.forcedFlushCount += uint64(len(flushedFrames))
		
		// If still not enough room, return error
		if rb.currentMemoryUsage+frameSize > rb.maxMemoryUsage {
			return utils.NewKwikError(utils.ErrInvalidFrame, "insufficient memory for frame", nil)
		}
	}

	// Check if buffer is full by count
	if len(rb.frames) >= rb.maxSize {
		// Force flush oldest frames
		flushedFrames := rb.forceFlushOldestFrames(0)
		rb.forcedFlushCount += uint64(len(flushedFrames))
		
		if len(rb.frames) >= rb.maxSize {
			return utils.NewKwikError(utils.ErrInvalidFrame, "reorder buffer full", nil)
		}
	}

	// Check if frame is duplicate
	if _, exists := rb.frames[frame.SequenceNum]; exists {
		return utils.NewKwikError(utils.ErrInvalidFrame, "duplicate frame", nil)
	}

	// Add frame to buffer
	rb.frames[frame.SequenceNum] = frame
	rb.currentMemoryUsage += frameSize
	return nil
}

// GetOrderedFrames returns frames that can be delivered in order
func (rb *ReorderBuffer) GetOrderedFrames() []*ReorderFrame {
	rb.mutex.Lock()
	defer rb.mutex.Unlock()

	var orderedFrames []*ReorderFrame

	// Collect consecutive frames starting from nextExpected
	for {
		frame, exists := rb.frames[rb.nextExpected]
		if !exists || frame.Delivered {
			break
		}

		orderedFrames = append(orderedFrames, frame)
		frame.Delivered = true
		delete(rb.frames, rb.nextExpected)
		rb.nextExpected++
	}

	return orderedFrames
}

// GetTimedOutFrames returns frames that have exceeded the timeout
func (rb *ReorderBuffer) GetTimedOutFrames() []*ReorderFrame {
	rb.mutex.Lock()
	defer rb.mutex.Unlock()

	now := time.Now()
	var timedOutFrames []*ReorderFrame

	for seqNum, frame := range rb.frames {
		if !frame.Delivered && now.Sub(frame.Timestamp) > rb.timeout {
			timedOutFrames = append(timedOutFrames, frame)
			frame.Delivered = true
			delete(rb.frames, seqNum)
		}
	}

	return timedOutFrames
}

// GetBufferSize returns the current number of buffered frames
func (rb *ReorderBuffer) GetBufferSize() int {
	rb.mutex.RLock()
	defer rb.mutex.RUnlock()
	return len(rb.frames)
}

// Clear removes all frames from the buffer
func (rb *ReorderBuffer) Clear() {
	rb.mutex.Lock()
	defer rb.mutex.Unlock()
	rb.frames = make(map[uint64]*ReorderFrame)
}

// ProcessFrameWithReordering processes a frame with reordering support
func (da *DataAggregatorImpl) ProcessFrameWithReordering(streamID uint64, frame *DataFrame) ([]byte, error) {
	da.streamsMutex.RLock()
	streamState, exists := da.aggregatedStreams[streamID]
	da.streamsMutex.RUnlock()

	if !exists {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"aggregated stream not found", nil)
	}

	streamState.mutex.Lock()
	defer streamState.mutex.Unlock()

	// Create buffered frame
	bufferedFrame := &ReorderFrame{
		Data:        make([]byte, len(frame.Data)),
		Offset:      frame.Offset,
		SequenceNum: frame.SequenceNum,
		PathID:      frame.PathID,
		Timestamp:   frame.Timestamp,
		Delivered:   false,
	}
	copy(bufferedFrame.Data, frame.Data)

	// Add frame to reorder buffer
	if err := streamState.ReorderBuffer.AddFrame(bufferedFrame); err != nil {
		// If buffer is full, force delivery of oldest frames
		timedOutFrames := da.forceDeliveryOldestFrames(streamState)
		if len(timedOutFrames) > 0 {
			// Try adding the frame again after clearing space
			if err := streamState.ReorderBuffer.AddFrame(bufferedFrame); err != nil {
				return nil, fmt.Errorf("failed to add frame after clearing buffer: %w", err)
			}
		} else {
			return nil, fmt.Errorf("failed to add frame to reorder buffer: %w", err)
		}
	}

	// Get frames that can be delivered in order
	orderedFrames := streamState.ReorderBuffer.GetOrderedFrames()

	// Also check for timed-out frames
	timedOutFrames := streamState.ReorderBuffer.GetTimedOutFrames()

	// Combine ordered and timed-out frames
	allFrames := append(orderedFrames, timedOutFrames...)

	// Process all deliverable frames
	var aggregatedData []byte
	for _, deliverableFrame := range allFrames {
		// Update statistics
		streamState.Stats.TotalBytesReceived += uint64(len(deliverableFrame.Data))
		streamState.Stats.FramesReceived++
		streamState.Stats.LastActivity = time.Now().UnixNano()

		// Update per-path statistics
		if streamState.Stats.BytesPerPath == nil {
			streamState.Stats.BytesPerPath = make(map[string]uint64)
		}
		if streamState.Stats.FramesPerPath == nil {
			streamState.Stats.FramesPerPath = make(map[string]uint64)
		}

		streamState.Stats.BytesPerPath[deliverableFrame.PathID] += uint64(len(deliverableFrame.Data))
		streamState.Stats.FramesPerPath[deliverableFrame.PathID]++

		// Append data to aggregated buffer
		aggregatedData = append(aggregatedData, deliverableFrame.Data...)
	}

	// Update main buffer
	if len(aggregatedData) > 0 {
		streamState.Buffer = append(streamState.Buffer, aggregatedData...)
		streamState.LastActivity = time.Now()
	}

	return aggregatedData, nil
}

// forceDeliveryOldestFrames forces delivery of the oldest frames to make space
func (da *DataAggregatorImpl) forceDeliveryOldestFrames(streamState *AggregatedStreamState) []*ReorderFrame {
	// Get all frames and sort by timestamp
	rb := streamState.ReorderBuffer
	rb.mutex.Lock()
	defer rb.mutex.Unlock()

	if len(rb.frames) == 0 {
		return nil
	}

	// Find oldest frame
	var oldestSeq uint64
	var oldestTime time.Time
	first := true

	for seqNum, frame := range rb.frames {
		if first || frame.Timestamp.Before(oldestTime) {
			oldestSeq = seqNum
			oldestTime = frame.Timestamp
			first = false
		}
	}

	// Remove and return oldest frame
	if frame, exists := rb.frames[oldestSeq]; exists {
		delete(rb.frames, oldestSeq)
		frame.Delivered = true
		return []*ReorderFrame{frame}
	}

	return nil
}

// GetReorderBufferStats returns statistics about the reorder buffer
func (da *DataAggregatorImpl) GetReorderBufferStats(streamID uint64) (*ReorderBufferStats, error) {
	da.streamsMutex.RLock()
	streamState, exists := da.aggregatedStreams[streamID]
	da.streamsMutex.RUnlock()

	if !exists {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"aggregated stream not found", nil)
	}

	streamState.mutex.RLock()
	defer streamState.mutex.RUnlock()

	return &ReorderBufferStats{
		StreamID:        streamID,
		BufferedFrames:  streamState.ReorderBuffer.GetBufferSize(),
		MaxBufferSize:   streamState.ReorderBuffer.maxSize,
		NextExpectedSeq: streamState.ReorderBuffer.nextExpected,
		ReorderTimeout:  streamState.ReorderBuffer.timeout,
		LastActivity:    streamState.LastActivity,
	}, nil
}

// ReorderBufferStats contains statistics for reorder buffer
type ReorderBufferStats struct {
	StreamID           uint64
	BufferedFrames     int
	MaxBufferSize      int
	NextExpectedSeq    uint64
	ReorderTimeout     time.Duration
	LastActivity       time.Time
	CurrentMemoryUsage uint64
	MaxMemoryUsage     uint64
	TimeoutCount       uint64
	ForcedFlushCount   uint64
	Timeout            time.Duration
}

// FlushReorderBuffer forces delivery of all buffered frames for a stream
func (da *DataAggregatorImpl) FlushReorderBuffer(streamID uint64) ([]byte, error) {
	da.streamsMutex.RLock()
	streamState, exists := da.aggregatedStreams[streamID]
	da.streamsMutex.RUnlock()

	if !exists {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"aggregated stream not found", nil)
	}

	streamState.mutex.Lock()
	defer streamState.mutex.Unlock()

	// Get all buffered frames
	rb := streamState.ReorderBuffer
	rb.mutex.Lock()
	var allFrames []*ReorderFrame
	for _, frame := range rb.frames {
		if !frame.Delivered {
			allFrames = append(allFrames, frame)
			frame.Delivered = true
		}
	}
	rb.frames = make(map[uint64]*ReorderFrame) // Clear buffer
	rb.mutex.Unlock()

	// Process all frames
	var aggregatedData []byte
	for _, frame := range allFrames {
		// Update statistics
		streamState.Stats.TotalBytesReceived += uint64(len(frame.Data))
		streamState.Stats.FramesReceived++
		streamState.Stats.LastActivity = time.Now().UnixNano()

		// Update per-path statistics
		if streamState.Stats.BytesPerPath == nil {
			streamState.Stats.BytesPerPath = make(map[string]uint64)
		}
		if streamState.Stats.FramesPerPath == nil {
			streamState.Stats.FramesPerPath = make(map[string]uint64)
		}

		streamState.Stats.BytesPerPath[frame.PathID] += uint64(len(frame.Data))
		streamState.Stats.FramesPerPath[frame.PathID]++

		// Append data
		aggregatedData = append(aggregatedData, frame.Data...)
	}

	// Update main buffer
	if len(aggregatedData) > 0 {
		streamState.Buffer = append(streamState.Buffer, aggregatedData...)
		streamState.LastActivity = time.Now()
	}

	return aggregatedData, nil
}
// forceFlushOldestFrames forces delivery of oldest frames to make room
func (rb *ReorderBuffer) forceFlushOldestFrames(requiredSpace uint64) []*ReorderFrame {
	var flushedFrames []*ReorderFrame
	var freedSpace uint64
	
	// Find oldest frames (lowest sequence numbers)
	var seqNums []uint64
	for seqNum := range rb.frames {
		seqNums = append(seqNums, seqNum)
	}
	
	// Sort sequence numbers to flush oldest first
	for i := 0; i < len(seqNums)-1; i++ {
		for j := i + 1; j < len(seqNums); j++ {
			if seqNums[i] > seqNums[j] {
				seqNums[i], seqNums[j] = seqNums[j], seqNums[i]
			}
		}
	}
	
	// Flush frames until we have enough space or buffer is empty
	for _, seqNum := range seqNums {
		if frame, exists := rb.frames[seqNum]; exists {
			frameSize := uint64(len(frame.Data))
			flushedFrames = append(flushedFrames, frame)
			delete(rb.frames, seqNum)
			rb.currentMemoryUsage -= frameSize
			freedSpace += frameSize
			
			// Stop if we've freed enough space
			if requiredSpace > 0 && freedSpace >= requiredSpace {
				break
			}
			
			// Stop if we've flushed half the buffer
			if len(flushedFrames) >= rb.maxSize/2 {
				break
			}
		}
	}
	
	return flushedFrames
}

// FlushAllFrames forces delivery of all buffered frames
func (rb *ReorderBuffer) FlushAllFrames() []*ReorderFrame {
	rb.mutex.Lock()
	defer rb.mutex.Unlock()
	
	var allFrames []*ReorderFrame
	
	// Collect all frames
	for seqNum, frame := range rb.frames {
		allFrames = append(allFrames, frame)
		delete(rb.frames, seqNum)
		rb.currentMemoryUsage -= uint64(len(frame.Data))
	}
	
	rb.forcedFlushCount += uint64(len(allFrames))
	
	// Sort frames by sequence number for ordered delivery
	for i := 0; i < len(allFrames)-1; i++ {
		for j := i + 1; j < len(allFrames); j++ {
			if allFrames[i].SequenceNum > allFrames[j].SequenceNum {
				allFrames[i], allFrames[j] = allFrames[j], allFrames[i]
			}
		}
	}
	
	return allFrames
}

// FlushFramesOlderThan flushes frames older than the specified duration
func (rb *ReorderBuffer) FlushFramesOlderThan(maxAge time.Duration) []*ReorderFrame {
	rb.mutex.Lock()
	defer rb.mutex.Unlock()
	
	now := time.Now()
	cutoffTime := now.Add(-maxAge)
	var flushedFrames []*ReorderFrame
	
	for seqNum, frame := range rb.frames {
		if frame.Timestamp.Before(cutoffTime) {
			flushedFrames = append(flushedFrames, frame)
			delete(rb.frames, seqNum)
			rb.currentMemoryUsage -= uint64(len(frame.Data))
		}
	}
	
	if len(flushedFrames) > 0 {
		rb.forcedFlushCount += uint64(len(flushedFrames))
	}
	
	return flushedFrames
}

// SetMemoryLimit sets the maximum memory usage for the buffer
func (rb *ReorderBuffer) SetMemoryLimit(maxMemory uint64) {
	rb.mutex.Lock()
	defer rb.mutex.Unlock()
	
	rb.maxMemoryUsage = maxMemory
	
	// If current usage exceeds new limit, force flush
	if rb.currentMemoryUsage > maxMemory {
		rb.forceFlushOldestFrames(rb.currentMemoryUsage - maxMemory)
	}
}

// GetBufferStats returns statistics about the reorder buffer
func (rb *ReorderBuffer) GetBufferStats() *ReorderBufferStats {
	rb.mutex.RLock()
	defer rb.mutex.RUnlock()
	
	return &ReorderBufferStats{
		StreamID:           0, // Will be set by caller if needed
		BufferedFrames:     len(rb.frames),
		MaxBufferSize:      rb.maxSize,
		NextExpectedSeq:    rb.nextExpected,
		ReorderTimeout:     rb.timeout,
		LastActivity:       time.Now(), // Will be set by caller if needed
		CurrentMemoryUsage: rb.currentMemoryUsage,
		MaxMemoryUsage:     rb.maxMemoryUsage,
		TimeoutCount:       rb.timeoutCount,
		ForcedFlushCount:   rb.forcedFlushCount,
		Timeout:            rb.timeout,
	}
}



// Close shuts down the reorder buffer and stops timeout management
func (rb *ReorderBuffer) Close() error {
	rb.mutex.Lock()
	defer rb.mutex.Unlock()
	
	// Stop timeout management
	if rb.stopTimeoutChan != nil {
		close(rb.stopTimeoutChan)
		rb.stopTimeoutChan = nil
	}
	
	// Clear all frames
	rb.frames = make(map[uint64]*ReorderFrame)
	rb.currentMemoryUsage = 0
	
	return nil
}

// SetTimeoutCallback sets the callback function for timed-out frames
func (rb *ReorderBuffer) SetTimeoutCallback(callback func([]*ReorderFrame)) {
	rb.mutex.Lock()
	defer rb.mutex.Unlock()
	rb.timeoutCallback = callback
}

// UpdateTimeout updates the timeout duration for the buffer
func (rb *ReorderBuffer) UpdateTimeout(newTimeout time.Duration) {
	rb.mutex.Lock()
	defer rb.mutex.Unlock()
	rb.timeout = newTimeout
}

// GetMemoryUsage returns current memory usage statistics
func (rb *ReorderBuffer) GetMemoryUsage() (current, max uint64) {
	rb.mutex.RLock()
	defer rb.mutex.RUnlock()
	return rb.currentMemoryUsage, rb.maxMemoryUsage
}

// IsMemoryPressure returns true if memory usage is above threshold
func (rb *ReorderBuffer) IsMemoryPressure(threshold float64) bool {
	rb.mutex.RLock()
	defer rb.mutex.RUnlock()
	
	if rb.maxMemoryUsage == 0 {
		return false
	}
	
	usage := float64(rb.currentMemoryUsage) / float64(rb.maxMemoryUsage)
	return usage > threshold
}

// handleTimedOutFrames processes frames that have timed out
func (da *DataAggregatorImpl) handleTimedOutFrames(streamID uint64, timedOutFrames []*ReorderFrame) {
	if len(timedOutFrames) == 0 {
		return
	}

	da.streamsMutex.RLock()
	streamState, exists := da.aggregatedStreams[streamID]
	da.streamsMutex.RUnlock()

	if !exists {
		return
	}

	streamState.mutex.Lock()
	defer streamState.mutex.Unlock()

	// Sort timed-out frames by sequence number for ordered processing
	for i := 0; i < len(timedOutFrames)-1; i++ {
		for j := i + 1; j < len(timedOutFrames); j++ {
			if timedOutFrames[i].SequenceNum > timedOutFrames[j].SequenceNum {
				timedOutFrames[i], timedOutFrames[j] = timedOutFrames[j], timedOutFrames[i]
			}
		}
	}

	// Process timed-out frames as partial data delivery
	for _, frame := range timedOutFrames {
		// Update statistics
		streamState.Stats.TotalBytesReceived += uint64(len(frame.Data))
		streamState.Stats.FramesReceived++
		streamState.Stats.LastActivity = time.Now().UnixNano()

		// Update per-path statistics
		if streamState.Stats.BytesPerPath == nil {
			streamState.Stats.BytesPerPath = make(map[string]uint64)
		}
		if streamState.Stats.FramesPerPath == nil {
			streamState.Stats.FramesPerPath = make(map[string]uint64)
		}

		streamState.Stats.BytesPerPath[frame.PathID] += uint64(len(frame.Data))
		streamState.Stats.FramesPerPath[frame.PathID]++

		// Append data to buffer (partial delivery)
		streamState.Buffer = append(streamState.Buffer, frame.Data...)
		streamState.LastActivity = time.Now()
	}
}

// FlushStreamBuffer forces delivery of all buffered data for a stream
func (da *DataAggregatorImpl) FlushStreamBuffer(streamID uint64) ([]byte, error) {
	da.streamsMutex.RLock()
	streamState, exists := da.aggregatedStreams[streamID]
	da.streamsMutex.RUnlock()

	if !exists {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"aggregated stream not found", nil)
	}

	streamState.mutex.Lock()
	defer streamState.mutex.Unlock()

	// Flush all frames from reorder buffer
	flushedFrames := streamState.ReorderBuffer.FlushAllFrames()

	// Process flushed frames
	var aggregatedData []byte
	for _, frame := range flushedFrames {
		// Update statistics
		streamState.Stats.TotalBytesReceived += uint64(len(frame.Data))
		streamState.Stats.FramesReceived++
		streamState.Stats.LastActivity = time.Now().UnixNano()

		// Update per-path statistics
		if streamState.Stats.BytesPerPath == nil {
			streamState.Stats.BytesPerPath = make(map[string]uint64)
		}
		if streamState.Stats.FramesPerPath == nil {
			streamState.Stats.FramesPerPath = make(map[string]uint64)
		}

		streamState.Stats.BytesPerPath[frame.PathID] += uint64(len(frame.Data))
		streamState.Stats.FramesPerPath[frame.PathID]++

		// Append data to aggregated buffer
		aggregatedData = append(aggregatedData, frame.Data...)
	}

	// Update main buffer
	if len(aggregatedData) > 0 {
		streamState.Buffer = append(streamState.Buffer, aggregatedData...)
		streamState.LastActivity = time.Now()
	}

	return aggregatedData, nil
}

// FlushStreamBufferOlderThan flushes frames older than the specified duration
func (da *DataAggregatorImpl) FlushStreamBufferOlderThan(streamID uint64, maxAge time.Duration) ([]byte, error) {
	da.streamsMutex.RLock()
	streamState, exists := da.aggregatedStreams[streamID]
	da.streamsMutex.RUnlock()

	if !exists {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"aggregated stream not found", nil)
	}

	streamState.mutex.Lock()
	defer streamState.mutex.Unlock()

	// Flush frames older than maxAge
	flushedFrames := streamState.ReorderBuffer.FlushFramesOlderThan(maxAge)

	// Process flushed frames
	var aggregatedData []byte
	for _, frame := range flushedFrames {
		// Update statistics
		streamState.Stats.TotalBytesReceived += uint64(len(frame.Data))
		streamState.Stats.FramesReceived++
		streamState.Stats.LastActivity = time.Now().UnixNano()

		// Update per-path statistics
		if streamState.Stats.BytesPerPath == nil {
			streamState.Stats.BytesPerPath = make(map[string]uint64)
		}
		if streamState.Stats.FramesPerPath == nil {
			streamState.Stats.FramesPerPath = make(map[string]uint64)
		}

		streamState.Stats.BytesPerPath[frame.PathID] += uint64(len(frame.Data))
		streamState.Stats.FramesPerPath[frame.PathID]++

		// Append data to aggregated buffer
		aggregatedData = append(aggregatedData, frame.Data...)
	}

	// Update main buffer
	if len(aggregatedData) > 0 {
		streamState.Buffer = append(streamState.Buffer, aggregatedData...)
		streamState.LastActivity = time.Now()
	}

	return aggregatedData, nil
}

// SetStreamMemoryLimit sets the memory limit for a stream's reorder buffer
func (da *DataAggregatorImpl) SetStreamMemoryLimit(streamID uint64, maxMemory uint64) error {
	da.streamsMutex.RLock()
	streamState, exists := da.aggregatedStreams[streamID]
	da.streamsMutex.RUnlock()

	if !exists {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			"aggregated stream not found", nil)
	}

	streamState.ReorderBuffer.SetMemoryLimit(maxMemory)
	return nil
}

// GetStreamBufferStats returns buffer statistics for a stream
func (da *DataAggregatorImpl) GetStreamBufferStats(streamID uint64) (*ReorderBufferStats, error) {
	da.streamsMutex.RLock()
	streamState, exists := da.aggregatedStreams[streamID]
	da.streamsMutex.RUnlock()

	if !exists {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"aggregated stream not found", nil)
	}

	return streamState.ReorderBuffer.GetBufferStats(), nil
}

// CheckMemoryPressure checks if any stream buffers are under memory pressure
func (da *DataAggregatorImpl) CheckMemoryPressure(threshold float64) map[uint64]bool {
	da.streamsMutex.RLock()
	defer da.streamsMutex.RUnlock()

	pressureMap := make(map[uint64]bool)
	
	for streamID, streamState := range da.aggregatedStreams {
		pressureMap[streamID] = streamState.ReorderBuffer.IsMemoryPressure(threshold)
	}

	return pressureMap
}

// CleanupExpiredStreams removes streams that have been inactive for too long
func (da *DataAggregatorImpl) CleanupExpiredStreams(maxInactivity time.Duration) []uint64 {
	da.streamsMutex.Lock()
	defer da.streamsMutex.Unlock()

	now := time.Now()
	cutoffTime := now.Add(-maxInactivity)
	var expiredStreams []uint64

	for streamID, streamState := range da.aggregatedStreams {
		streamState.mutex.RLock()
		lastActivity := streamState.LastActivity
		streamState.mutex.RUnlock()

		if lastActivity.Before(cutoffTime) {
			expiredStreams = append(expiredStreams, streamID)
		}
	}

	// Remove expired streams
	for _, streamID := range expiredStreams {
		if streamState, exists := da.aggregatedStreams[streamID]; exists {
			// Close the reorder buffer to stop timeout management
			streamState.ReorderBuffer.Close()
			delete(da.aggregatedStreams, streamID)
		}
	}

	return expiredStreams
}

// UpdateStreamTimeout updates the timeout for a stream's reorder buffer
func (da *DataAggregatorImpl) UpdateStreamTimeout(streamID uint64, newTimeout time.Duration) error {
	da.streamsMutex.RLock()
	streamState, exists := da.aggregatedStreams[streamID]
	da.streamsMutex.RUnlock()

	if !exists {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			"aggregated stream not found", nil)
	}

	streamState.ReorderBuffer.UpdateTimeout(newTimeout)
	return nil
}

// StartPeriodicCleanup starts a background goroutine for periodic cleanup
func (da *DataAggregatorImpl) StartPeriodicCleanup(cleanupInterval, maxInactivity time.Duration) {
	go func() {
		ticker := time.NewTicker(cleanupInterval)
		defer ticker.Stop()

		for range ticker.C {
			expiredStreams := da.CleanupExpiredStreams(maxInactivity)
			if len(expiredStreams) > 0 {
				// Log cleanup activity if needed
				_ = expiredStreams
			}
		}
	}()
}

// SetOffsetCoordinator sets the offset coordinator for continuity validation
func (da *DataAggregatorImpl) SetOffsetCoordinator(coordinator OffsetCoordinator) {
	da.offsetCoordinator = coordinator
}

// ValidateOffsetContinuity validates offset continuity using the offset coordinator
func (da *DataAggregatorImpl) ValidateOffsetContinuity(streamID uint64) ([]OffsetGap, error) {
	if da.offsetCoordinator == nil {
		// Fall back to existing offset manager validation
		return nil, nil
	}
	
	return da.offsetCoordinator.ValidateOffsetContinuity(streamID)
}

