package data

import (
	"sort"
	"sync"
	"time"

	"kwik/internal/utils"
	"kwik/proto/data"
)

// ClientDataAggregator implements client-side data aggregation from multiple paths
// This implements requirements 4.6 and 5.1 for aggregating data from multiple server data planes
type ClientDataAggregator struct {
	// Stream aggregation state
	aggregatedStreams map[uint64]*ClientAggregatedStream
	streamsMutex      sync.RWMutex

	// Path data streams
	pathStreams map[string]DataStream
	pathsMutex  sync.RWMutex

	// Configuration
	config *ClientAggregatorConfig

	// Statistics
	stats      *ClientAggregationStats
	statsMutex sync.RWMutex
}

// ClientAggregatorConfig holds configuration for client-side aggregation
type ClientAggregatorConfig struct {
	ReorderBufferSize   int
	ReorderTimeout      time.Duration
	MaxOutOfOrderFrames int
	AggregationEnabled  bool
	OrderingEnabled     bool
	DuplicateDetection  bool
	BufferFlushInterval time.Duration
}

// ClientAggregatedStream represents an aggregated stream on the client side
type ClientAggregatedStream struct {
	StreamID          uint64
	ExpectedOffset    uint64
	ReorderBuffer     map[uint64]*data.DataFrame // Offset -> Frame
	OrderedBuffer     []byte
	PathContributions map[string]*PathContribution
	LastActivity      time.Time
	IsComplete        bool
	mutex             sync.RWMutex
}

// PathContribution tracks contribution from each path
type PathContribution struct {
	PathID          string
	BytesReceived   uint64
	FramesReceived  uint64
	LastFrameTime   time.Time
	LastFrameOffset uint64
	IsActive        bool
}

// ClientAggregationStats contains statistics for client-side aggregation
type ClientAggregationStats struct {
	TotalStreamsAggregated  uint64
	TotalBytesAggregated    uint64
	TotalFramesAggregated   uint64
	PathContributions       map[string]*PathContribution
	AverageAggregationRatio float64
	ReorderingEvents        uint64
	DuplicateFrames         uint64
	OutOfOrderFrames        uint64
}

// NewClientDataAggregator creates a new client-side data aggregator
func NewClientDataAggregator() DataAggregator {
	return &ClientDataAggregator{
		aggregatedStreams: make(map[uint64]*ClientAggregatedStream),
		pathStreams:       make(map[string]DataStream),
		config: &ClientAggregatorConfig{
			ReorderBufferSize:   1024,
			ReorderTimeout:      100 * time.Millisecond,
			MaxOutOfOrderFrames: 100,
			AggregationEnabled:  true,
			OrderingEnabled:     true,
			DuplicateDetection:  true,
			BufferFlushInterval: 50 * time.Millisecond,
		},
		stats: &ClientAggregationStats{
			PathContributions: make(map[string]*PathContribution),
		},
	}
}

// AddPath adds a path for data aggregation
func (ca *ClientDataAggregator) AddPath(pathID string, stream DataStream) error {
	if pathID == "" {
		return utils.NewKwikError(utils.ErrInvalidFrame, "path ID is empty", nil)
	}

	if stream == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "stream is nil", nil)
	}

	ca.pathsMutex.Lock()
	defer ca.pathsMutex.Unlock()

	ca.pathStreams[pathID] = stream

	// Initialize path contribution tracking
	ca.statsMutex.Lock()
	ca.stats.PathContributions[pathID] = &PathContribution{
		PathID:          pathID,
		BytesReceived:   0,
		FramesReceived:  0,
		LastFrameTime:   time.Now(),
		LastFrameOffset: 0,
		IsActive:        true,
	}
	ca.statsMutex.Unlock()

	return nil
}

// RemovePath removes a path from aggregation
func (ca *ClientDataAggregator) RemovePath(pathID string) error {
	ca.pathsMutex.Lock()
	defer ca.pathsMutex.Unlock()

	delete(ca.pathStreams, pathID)

	// Mark path contribution as inactive
	ca.statsMutex.Lock()
	if contrib, exists := ca.stats.PathContributions[pathID]; exists {
		contrib.IsActive = false
	}
	ca.statsMutex.Unlock()

	return nil
}

// CreateAggregatedStream creates a new aggregated stream for client-side aggregation
func (ca *ClientDataAggregator) CreateAggregatedStream(streamID uint64) error {
	ca.streamsMutex.Lock()
	defer ca.streamsMutex.Unlock()

	if _, exists := ca.aggregatedStreams[streamID]; exists {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			"aggregated stream already exists", nil)
	}

	aggregatedStream := &ClientAggregatedStream{
		StreamID:          streamID,
		ExpectedOffset:    0,
		ReorderBuffer:     make(map[uint64]*data.DataFrame),
		OrderedBuffer:     make([]byte, 0, ca.config.ReorderBufferSize),
		PathContributions: make(map[string]*PathContribution),
		LastActivity:      time.Now(),
		IsComplete:        false,
	}

	ca.aggregatedStreams[streamID] = aggregatedStream

	ca.statsMutex.Lock()
	ca.stats.TotalStreamsAggregated++
	ca.statsMutex.Unlock()

	return nil
}

// CloseAggregatedStream closes an aggregated stream
func (ca *ClientDataAggregator) CloseAggregatedStream(streamID uint64) error {
	ca.streamsMutex.Lock()
	defer ca.streamsMutex.Unlock()

	aggregatedStream, exists := ca.aggregatedStreams[streamID]
	if !exists {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			"aggregated stream not found", nil)
	}

	aggregatedStream.mutex.Lock()
	aggregatedStream.IsComplete = true
	aggregatedStream.mutex.Unlock()

	delete(ca.aggregatedStreams, streamID)

	return nil
}

// AggregateDataFromPaths aggregates data from multiple paths for a specific stream
// This implements requirement 4.6: client reads aggregated data from multiple server data planes
func (ca *ClientDataAggregator) AggregateDataFromPaths(streamID uint64) ([]byte, error) {
	ca.streamsMutex.RLock()
	aggregatedStream, exists := ca.aggregatedStreams[streamID]
	ca.streamsMutex.RUnlock()

	if !exists {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"aggregated stream not found", nil)
	}

	// Collect data from all paths for this stream
	ca.pathsMutex.RLock()
	pathStreams := make(map[string]DataStream)
	for pathID, stream := range ca.pathStreams {
		pathStreams[pathID] = stream
	}
	ca.pathsMutex.RUnlock()

	// Read data from each path and aggregate
	var aggregatedFrames []*data.DataFrame
	for pathID, stream := range pathStreams {
		frames, err := ca.readFramesFromPath(pathID, stream, streamID)
		if err != nil {
			// Log error but continue with other paths
			continue
		}
		aggregatedFrames = append(aggregatedFrames, frames...)
	}

	// Process and order the aggregated frames
	orderedData, err := ca.processAggregatedFrames(aggregatedStream, aggregatedFrames)
	if err != nil {
		return nil, err
	}

	return orderedData, nil
}

// AggregateData aggregates data from multiple paths for a stream (DataAggregator interface)
func (ca *ClientDataAggregator) AggregateData(streamID uint64) ([]byte, error) {
	return ca.AggregateDataFromPaths(streamID)
}

// WriteToPath writes data to a specific path (DataAggregator interface)
func (ca *ClientDataAggregator) WriteToPath(pathID string, data []byte) error {
	ca.pathsMutex.RLock()
	stream, exists := ca.pathStreams[pathID]
	ca.pathsMutex.RUnlock()

	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}

	_, err := stream.Write(data)
	return err
}

// readFramesFromPath reads data frames from a specific path for a stream
func (ca *ClientDataAggregator) readFramesFromPath(pathID string, stream DataStream, streamID uint64) ([]*data.DataFrame, error) {
	var frames []*data.DataFrame

	// Read available data from the path
	buffer := make([]byte, ca.config.ReorderBufferSize)
	n, err := stream.Read(buffer)
	if err != nil {
		return nil, err
	}

	if n == 0 {
		return frames, nil // No data available
	}

	// Parse frames from the data
	// This is a simplified implementation - in reality, we'd need proper frame parsing
	frameProcessor := NewFrameProcessor()
	frame, err := frameProcessor.DeserializeFrame(buffer[:n])
	if err != nil {
		return nil, err
	}

	// Only include frames for the requested stream
	if frame.LogicalStreamId == streamID {
		frames = append(frames, frame)

		// Update path contribution statistics
		ca.updatePathContribution(pathID, frame)
	}

	return frames, nil
}

// processAggregatedFrames processes and orders aggregated frames from multiple paths
// This implements requirement 5.1: proper ordering and reassembly of aggregated data
func (ca *ClientDataAggregator) processAggregatedFrames(aggregatedStream *ClientAggregatedStream, frames []*data.DataFrame) ([]byte, error) {
	aggregatedStream.mutex.Lock()
	defer aggregatedStream.mutex.Unlock()

	// Add frames to reorder buffer
	for _, frame := range frames {
		// Check for duplicates if enabled
		if ca.config.DuplicateDetection {
			if _, exists := aggregatedStream.ReorderBuffer[frame.Offset]; exists {
				ca.statsMutex.Lock()
				ca.stats.DuplicateFrames++
				ca.statsMutex.Unlock()
				continue // Skip duplicate frame
			}
		}

		aggregatedStream.ReorderBuffer[frame.Offset] = frame

		// Check if frame is out of order
		if frame.Offset != aggregatedStream.ExpectedOffset {
			ca.statsMutex.Lock()
			ca.stats.OutOfOrderFrames++
			ca.statsMutex.Unlock()
		}
	}

	// Process frames in order
	var orderedData []byte
	if ca.config.OrderingEnabled {
		orderedData = ca.extractOrderedData(aggregatedStream)
	} else {
		// If ordering is disabled, just concatenate all available data
		orderedData = ca.extractAllData(aggregatedStream)
	}

	aggregatedStream.LastActivity = time.Now()

	// Update statistics
	ca.statsMutex.Lock()
	ca.stats.TotalBytesAggregated += uint64(len(orderedData))
	ca.stats.TotalFramesAggregated += uint64(len(frames))
	ca.statsMutex.Unlock()

	return orderedData, nil
}

// extractOrderedData extracts data in proper order from the reorder buffer
func (ca *ClientDataAggregator) extractOrderedData(aggregatedStream *ClientAggregatedStream) []byte {
	var orderedData []byte

	// Extract consecutive frames starting from expected offset
	for {
		frame, exists := aggregatedStream.ReorderBuffer[aggregatedStream.ExpectedOffset]
		if !exists {
			break // No frame at expected offset
		}

		// Add frame data to ordered buffer
		orderedData = append(orderedData, frame.Data...)

		// Update expected offset
		aggregatedStream.ExpectedOffset += uint64(len(frame.Data))

		// Remove processed frame from reorder buffer
		delete(aggregatedStream.ReorderBuffer, frame.Offset)

		// Check if this is the final frame
		if frame.Fin {
			aggregatedStream.IsComplete = true
			break
		}
	}

	return orderedData
}

// extractAllData extracts all available data without ordering
func (ca *ClientDataAggregator) extractAllData(aggregatedStream *ClientAggregatedStream) []byte {
	var allData []byte

	// Sort frames by offset
	var offsets []uint64
	for offset := range aggregatedStream.ReorderBuffer {
		offsets = append(offsets, offset)
	}
	sort.Slice(offsets, func(i, j int) bool {
		return offsets[i] < offsets[j]
	})

	// Extract data in sorted order
	for _, offset := range offsets {
		frame := aggregatedStream.ReorderBuffer[offset]
		allData = append(allData, frame.Data...)
		delete(aggregatedStream.ReorderBuffer, offset)

		if frame.Fin {
			aggregatedStream.IsComplete = true
		}
	}

	return allData
}

// updatePathContribution updates statistics for a path's contribution
func (ca *ClientDataAggregator) updatePathContribution(pathID string, frame *data.DataFrame) {
	ca.statsMutex.Lock()
	defer ca.statsMutex.Unlock()

	contrib, exists := ca.stats.PathContributions[pathID]
	if !exists {
		contrib = &PathContribution{
			PathID:          pathID,
			BytesReceived:   0,
			FramesReceived:  0,
			LastFrameTime:   time.Now(),
			LastFrameOffset: 0,
			IsActive:        true,
		}
		ca.stats.PathContributions[pathID] = contrib
	}

	contrib.BytesReceived += uint64(len(frame.Data))
	contrib.FramesReceived++
	contrib.LastFrameTime = time.Now()
	contrib.LastFrameOffset = frame.Offset
}

// GetAggregationStats returns aggregation statistics
func (ca *ClientDataAggregator) GetAggregationStats(streamID uint64) (*AggregationStats, error) {
	ca.statsMutex.RLock()
	defer ca.statsMutex.RUnlock()

	// Calculate aggregation ratio
	totalBytes := uint64(0)
	pathBytes := make(map[string]uint64)
	pathFrames := make(map[string]uint64)

	for pathID, contrib := range ca.stats.PathContributions {
		totalBytes += contrib.BytesReceived
		pathBytes[pathID] = contrib.BytesReceived
		pathFrames[pathID] = contrib.FramesReceived
	}

	aggregationRatio := 0.0
	if totalBytes > 0 && len(ca.stats.PathContributions) > 1 {
		// Calculate how evenly distributed the data is across paths
		avgBytesPerPath := float64(totalBytes) / float64(len(ca.stats.PathContributions))
		variance := 0.0
		for _, contrib := range ca.stats.PathContributions {
			diff := float64(contrib.BytesReceived) - avgBytesPerPath
			variance += diff * diff
		}
		variance /= float64(len(ca.stats.PathContributions))

		// Higher variance means less even distribution (lower aggregation efficiency)
		aggregationRatio = 1.0 - (variance / (avgBytesPerPath * avgBytesPerPath))
		if aggregationRatio < 0 {
			aggregationRatio = 0
		}
	}

	return &AggregationStats{
		StreamID:           streamID,
		TotalBytesReceived: totalBytes,
		BytesPerPath:       pathBytes,
		FramesReceived:     ca.stats.TotalFramesAggregated,
		FramesPerPath:      pathFrames,
		LastActivity:       time.Now().UnixNano(),
		AggregationRatio:   aggregationRatio,
	}, nil
}

// IsStreamComplete returns whether an aggregated stream is complete
func (ca *ClientDataAggregator) IsStreamComplete(streamID uint64) bool {
	ca.streamsMutex.RLock()
	defer ca.streamsMutex.RUnlock()

	aggregatedStream, exists := ca.aggregatedStreams[streamID]
	if !exists {
		return false
	}

	aggregatedStream.mutex.RLock()
	defer aggregatedStream.mutex.RUnlock()

	return aggregatedStream.IsComplete
}

// GetReorderBufferSize returns the current size of the reorder buffer for a stream
func (ca *ClientDataAggregator) GetReorderBufferSize(streamID uint64) int {
	ca.streamsMutex.RLock()
	defer ca.streamsMutex.RUnlock()

	aggregatedStream, exists := ca.aggregatedStreams[streamID]
	if !exists {
		return 0
	}

	aggregatedStream.mutex.RLock()
	defer aggregatedStream.mutex.RUnlock()

	return len(aggregatedStream.ReorderBuffer)
}

// FlushReorderBuffer flushes the reorder buffer for a stream after timeout
func (ca *ClientDataAggregator) FlushReorderBuffer(streamID uint64) ([]byte, error) {
	ca.streamsMutex.RLock()
	aggregatedStream, exists := ca.aggregatedStreams[streamID]
	ca.streamsMutex.RUnlock()

	if !exists {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"aggregated stream not found", nil)
	}

	aggregatedStream.mutex.Lock()
	defer aggregatedStream.mutex.Unlock()

	// Check if timeout has occurred
	if time.Since(aggregatedStream.LastActivity) < ca.config.ReorderTimeout {
		return nil, nil // No timeout yet
	}

	// Force flush all buffered data
	flushedData := ca.extractAllData(aggregatedStream)

	ca.statsMutex.Lock()
	ca.stats.ReorderingEvents++
	ca.statsMutex.Unlock()

	return flushedData, nil
} // AggregateSecondaryData aggregates data from a secondary stream (stub implementation for interface compliance)
func (ca *ClientDataAggregator) AggregateSecondaryData(kwikStreamID uint64, secondaryData *DataFrame) error {
	// ClientDataAggregator doesn't support secondary streams, return error
	return utils.NewKwikError(utils.ErrInvalidFrame, "secondary stream aggregation not supported by ClientDataAggregator", nil)
}

// SetStreamMapping sets the mapping between a secondary stream and a KWIK stream (stub implementation)
func (ca *ClientDataAggregator) SetStreamMapping(kwikStreamID uint64, secondaryStreamID uint64, pathID string) error {
	// ClientDataAggregator doesn't support secondary streams, return error
	return utils.NewKwikError(utils.ErrInvalidFrame, "stream mapping not supported by ClientDataAggregator", nil)
}

// RemoveStreamMapping removes the mapping for a secondary stream (stub implementation)
func (ca *ClientDataAggregator) RemoveStreamMapping(secondaryStreamID uint64) error {
	// ClientDataAggregator doesn't support secondary streams, return error
	return utils.NewKwikError(utils.ErrInvalidFrame, "stream mapping not supported by ClientDataAggregator", nil)
}

// ValidateOffset validates an offset for a KWIK stream from a specific source (stub implementation)
func (ca *ClientDataAggregator) ValidateOffset(kwikStreamID uint64, offset uint64, sourcePathID string) error {
	// ClientDataAggregator doesn't support offset validation, return success
	return nil
}

// GetNextExpectedOffset returns the next expected offset for a KWIK stream (stub implementation)
func (ca *ClientDataAggregator) GetNextExpectedOffset(kwikStreamID uint64) uint64 {
	ca.streamsMutex.RLock()
	defer ca.streamsMutex.RUnlock()

	aggregatedStream, exists := ca.aggregatedStreams[kwikStreamID]
	if !exists {
		return 0
	}

	aggregatedStream.mutex.RLock()
	defer aggregatedStream.mutex.RUnlock()

	return aggregatedStream.ExpectedOffset
}

// GetSecondaryStreamStats returns statistics for secondary stream aggregation (stub implementation)
func (ca *ClientDataAggregator) GetSecondaryStreamStats() *SecondaryAggregationStats {
	// ClientDataAggregator doesn't support secondary streams, return empty stats
	return &SecondaryAggregationStats{
		ActiveSecondaryStreams: 0,
		ActiveKwikStreams:      0,
		TotalBytesAggregated:   0,
		TotalDataFrames:        0,
		AggregationLatency:     0,
		ReorderingEvents:       0,
		DroppedFrames:          0,
		LastUpdate:             time.Now(),
	}
}

// GetOffsetManagerStats returns statistics for the offset manager (stub implementation)
func (ca *ClientDataAggregator) GetOffsetManagerStats() *OffsetManagerStats {
	// ClientDataAggregator doesn't support offset management, return empty stats
	return &OffsetManagerStats{
		ActiveStreams:     0,
		TotalConflicts:    0,
		ResolvedConflicts: 0,
		PendingDataBytes:  0,
		ReorderingEvents:  0,
		SyncRequests:      0,
		SyncFailures:      0,
		LastUpdate:        time.Now(),
	}
}
