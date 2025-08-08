package data

import (
	"sync"
	"time"

	"kwik/internal/utils"
)

// DataAggregatorImpl implements the DataAggregator interface
// It handles aggregation of data from multiple paths for client-side sessions
type DataAggregatorImpl struct {
	// Path streams for aggregation
	pathStreams map[string]DataStream
	pathsMutex  sync.RWMutex

	// Aggregated streams
	aggregatedStreams map[uint64]*AggregatedStreamState
	streamsMutex      sync.RWMutex

	// Configuration
	config *AggregatorConfig
}

// AggregatorConfig holds configuration for the data aggregator
type AggregatorConfig struct {
	BufferSize       int
	MaxStreams       int
	AggregationDelay time.Duration
	ReorderTimeout   time.Duration
}

// AggregatedStreamState represents the state of an aggregated stream
type AggregatedStreamState struct {
	StreamID      uint64
	Buffer        []byte
	Offset        uint64
	LastActivity  time.Time
	PathBuffers   map[string][]byte
	Stats         *AggregationStats
	mutex         sync.RWMutex
}

// NewDataAggregator creates a new data aggregator
func NewDataAggregator() DataAggregator {
	return &DataAggregatorImpl{
		pathStreams:       make(map[string]DataStream),
		aggregatedStreams: make(map[uint64]*AggregatedStreamState),
		config: &AggregatorConfig{
			BufferSize:       utils.DefaultReadBufferSize,
			MaxStreams:       100,
			AggregationDelay: 10 * time.Millisecond,
			ReorderTimeout:   100 * time.Millisecond,
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
		StreamID:     streamID,
		Buffer:       make([]byte, 0, da.config.BufferSize),
		Offset:       0,
		LastActivity: time.Now(),
		PathBuffers:  make(map[string][]byte),
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