package data

import (
	"fmt"
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

	// Secondary stream aggregation
	secondaryAggregator *SecondaryStreamAggregator
	offsetManager       *MultiSourceOffsetManager

	// Stream mappings for secondary streams
	streamMappings map[uint64]uint64 // secondaryStreamID -> kwikStreamID
	mappingsMutex  sync.RWMutex

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
		pathStreams:         make(map[string]DataStream),
		aggregatedStreams:   make(map[uint64]*AggregatedStreamState),
		secondaryAggregator: NewSecondaryStreamAggregator(),
		offsetManager:       NewMultiSourceOffsetManager(),
		streamMappings:      make(map[uint64]uint64),
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
// AggregateSecondaryData aggregates data from a secondary stream into the appropriate KWIK stream
func (da *DataAggregatorImpl) AggregateSecondaryData(kwikStreamID uint64, secondaryData *SecondaryStreamData) error {
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

	// Aggregate the data using the secondary aggregator
	if err := da.secondaryAggregator.AggregateSecondaryData(secondaryData); err != nil {
		return fmt.Errorf("secondary aggregation failed: %w", err)
	}

	// Update the main aggregated stream with the new data
	return da.updateAggregatedStreamFromSecondary(kwikStreamID, secondaryData)
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
func (da *DataAggregatorImpl) updateAggregatedStreamFromSecondary(kwikStreamID uint64, secondaryData *SecondaryStreamData) error {
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