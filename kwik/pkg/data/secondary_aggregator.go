package data

import (
	"fmt"
	"sync"
	"time"

	"kwik/internal/utils"
)

// SecondaryStreamAggregator handles aggregation of data from secondary streams into KWIK streams
type SecondaryStreamAggregator struct {
	// Secondary stream data by stream ID
	secondaryStreams map[uint64]*SecondaryStreamState
	streamsMutex     sync.RWMutex

	// Mapping from secondary streams to KWIK streams
	streamMappings map[uint64]uint64 // secondaryStreamID -> kwikStreamID
	mappingsMutex  sync.RWMutex

	// KWIK stream aggregation state
	kwikStreams map[uint64]*KwikStreamAggregationState
	kwikMutex   sync.RWMutex

	// Configuration
	config *SecondaryAggregatorConfig

	// Statistics (internal structure with mutex)
	stats *internalSecondaryAggregationStats
}

// SecondaryStreamData is defined in interfaces.go

// SecondaryStreamState tracks the state of a secondary stream
type SecondaryStreamState struct {
	StreamID         uint64
	PathID           string
	KwikStreamID     uint64
	CurrentOffset    uint64
	LastActivity     time.Time
	BytesReceived    uint64
	BytesAggregated  uint64
	State            SecondaryStreamStateType
	PendingData      map[uint64]*SecondaryStreamData // offset -> data
	mutex            sync.RWMutex
}

// KwikStreamAggregationState tracks aggregation state for a KWIK stream
type KwikStreamAggregationState struct {
	StreamID          uint64
	NextExpectedOffset uint64
	SecondaryStreams  map[uint64]bool // Set of secondary stream IDs
	PendingData       map[uint64]*SecondaryStreamData // offset -> data
	AggregatedBuffer  []byte
	LastActivity      time.Time
	TotalBytesAggregated uint64
	mutex             sync.RWMutex
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
	MaxSecondaryStreams  int
	MaxPendingData       int           // Maximum pending data per stream
	ReorderTimeout       time.Duration // Timeout for reordering data
	AggregationBatchSize int           // Batch size for aggregation
	BufferSize           int           // Buffer size for aggregated data
	CleanupInterval      time.Duration // Interval for cleanup of inactive streams
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

// NewSecondaryStreamAggregator creates a new secondary stream aggregator
func NewSecondaryStreamAggregator() *SecondaryStreamAggregator {
	return &SecondaryStreamAggregator{
		secondaryStreams: make(map[uint64]*SecondaryStreamState),
		streamMappings:   make(map[uint64]uint64),
		kwikStreams:      make(map[uint64]*KwikStreamAggregationState),
		config: &SecondaryAggregatorConfig{
			MaxSecondaryStreams:  1000,
			MaxPendingData:       100,
			ReorderTimeout:       100 * time.Millisecond,
			AggregationBatchSize: 64,
			BufferSize:           65536,
			CleanupInterval:      30 * time.Second,
		},
		stats: &internalSecondaryAggregationStats{
			LastUpdate: time.Now(),
		},
	}
}

// AggregateSecondaryData aggregates data from a secondary stream into the appropriate KWIK stream
func (ssa *SecondaryStreamAggregator) AggregateSecondaryData(data *SecondaryStreamData) error {
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

	return nil
}

// SetStreamMapping sets the mapping between a secondary stream and a KWIK stream
func (ssa *SecondaryStreamAggregator) SetStreamMapping(secondaryStreamID, kwikStreamID uint64, pathID string) error {
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
func (ssa *SecondaryStreamAggregator) RemoveStreamMapping(secondaryStreamID uint64) error {
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
func (ssa *SecondaryStreamAggregator) GetAggregatedData(kwikStreamID uint64) ([]byte, error) {
	ssa.kwikMutex.RLock()
	kwikState, exists := ssa.kwikStreams[kwikStreamID]
	ssa.kwikMutex.RUnlock()

	if !exists {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"KWIK stream not found", nil)
	}

	kwikState.mutex.RLock()
	defer kwikState.mutex.RUnlock()

	// Return copy of aggregated buffer
	data := make([]byte, len(kwikState.AggregatedBuffer))
	copy(data, kwikState.AggregatedBuffer)

	return data, nil
}

// GetSecondaryStreamStats returns statistics for secondary stream aggregation
func (ssa *SecondaryStreamAggregator) GetSecondaryStreamStats() *SecondaryAggregationStats {
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
func (ssa *SecondaryStreamAggregator) validateSecondaryData(data *SecondaryStreamData) error {
	if data.StreamID == 0 {
		return utils.NewKwikError(utils.ErrInvalidFrame, "invalid secondary stream ID", nil)
	}
	if data.KwikStreamID == 0 {
		return utils.NewKwikError(utils.ErrInvalidFrame, "invalid KWIK stream ID", nil)
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
func (ssa *SecondaryStreamAggregator) getOrCreateSecondaryStream(streamID uint64, pathID string, kwikStreamID uint64) (*SecondaryStreamState, error) {
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
		PendingData:     make(map[uint64]*SecondaryStreamData),
	}

	ssa.secondaryStreams[streamID] = state
	return state, nil
}

// getOrCreateKwikStream gets or creates a KWIK stream aggregation state
func (ssa *SecondaryStreamAggregator) getOrCreateKwikStream(streamID uint64) (*KwikStreamAggregationState, error) {
	ssa.kwikMutex.Lock()
	defer ssa.kwikMutex.Unlock()

	if state, exists := ssa.kwikStreams[streamID]; exists {
		return state, nil
	}

	state := &KwikStreamAggregationState{
		StreamID:             streamID,
		NextExpectedOffset:   0,
		SecondaryStreams:     make(map[uint64]bool),
		PendingData:          make(map[uint64]*SecondaryStreamData),
		AggregatedBuffer:     make([]byte, 0, ssa.config.BufferSize),
		LastActivity:         time.Now(),
		TotalBytesAggregated: 0,
	}

	ssa.kwikStreams[streamID] = state
	return state, nil
}

// addDataToSecondaryStream adds data to a secondary stream
func (ssa *SecondaryStreamAggregator) addDataToSecondaryStream(state *SecondaryStreamState, data *SecondaryStreamData) error {
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
func (ssa *SecondaryStreamAggregator) aggregateIntoKwikStream(kwikState *KwikStreamAggregationState, data *SecondaryStreamData) error {
	kwikState.mutex.Lock()
	defer kwikState.mutex.Unlock()

	// Add secondary stream to the set
	kwikState.SecondaryStreams[data.StreamID] = true

	// Check if this data can be immediately aggregated
	if data.Offset == kwikState.NextExpectedOffset {
		// Data is in order, aggregate immediately
		kwikState.AggregatedBuffer = append(kwikState.AggregatedBuffer, data.Data...)
		kwikState.NextExpectedOffset += uint64(len(data.Data))
		kwikState.TotalBytesAggregated += uint64(len(data.Data))
		kwikState.LastActivity = time.Now()

		// Check if we can aggregate any pending data
		ssa.aggregatePendingData(kwikState)
	} else {
		// Data is out of order, store for later
		if len(kwikState.PendingData) >= ssa.config.MaxPendingData {
			return utils.NewKwikError(utils.ErrStreamCreationFailed,
				"maximum pending data reached for KWIK stream", nil)
		}
		kwikState.PendingData[data.Offset] = data
	}

	return nil
}

// aggregatePendingData aggregates any pending data that is now in order
func (ssa *SecondaryStreamAggregator) aggregatePendingData(kwikState *KwikStreamAggregationState) {
	for {
		data, exists := kwikState.PendingData[kwikState.NextExpectedOffset]
		if !exists {
			break
		}

		// Aggregate this data
		kwikState.AggregatedBuffer = append(kwikState.AggregatedBuffer, data.Data...)
		kwikState.NextExpectedOffset += uint64(len(data.Data))
		kwikState.TotalBytesAggregated += uint64(len(data.Data))
		
		// Remove from pending
		delete(kwikState.PendingData, data.Offset)
	}
}

// updateAggregationStats updates aggregation statistics
func (ssa *SecondaryStreamAggregator) updateAggregationStats(data *SecondaryStreamData) {
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
func (ssa *SecondaryStreamAggregator) CloseSecondaryStream(streamID uint64) error {
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
func (ssa *SecondaryStreamAggregator) CloseKwikStream(streamID uint64) error {
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