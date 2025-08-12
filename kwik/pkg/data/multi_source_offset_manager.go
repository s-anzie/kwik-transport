package data

import (
	"fmt"
	"sync"
	"time"

	"kwik/internal/utils"
)

// MultiSourceOffsetManager manages offsets for KWIK streams that receive data from multiple sources
type MultiSourceOffsetManager struct {
	// Offset state for each KWIK stream
	kwikStreamOffsets map[uint64]*KwikStreamOffsetState
	streamsMutex      sync.RWMutex

	// Source offsets tracking
	sourceOffsets map[string]map[uint64]uint64 // pathID -> secondaryStreamID -> offset
	sourcesMutex  sync.RWMutex

	// Configuration
	config *MultiSourceOffsetManagerConfig

	// Statistics
	stats *internalOffsetManagerStats
}

// KwikStreamOffsetState tracks offset state for a KWIK stream
type KwikStreamOffsetState struct {
	StreamID           uint64
	NextExpectedOffset uint64
	SourceOffsets      map[string]uint64 // sourceID -> currentOffset
	PendingData        map[uint64][]byte // offset -> data (for reordering)
	LastUpdate         time.Time
	ConflictCount      int
	TotalBytesReceived uint64
	mutex              sync.RWMutex
}

// MultiSourceOffsetManagerConfig holds configuration for the multi-source offset manager
type MultiSourceOffsetManagerConfig struct {
	MaxPendingData         int                        // Maximum pending data per stream
	ReorderTimeout         time.Duration              // Timeout for reordering data
	ConflictResolution     ConflictResolutionStrategy // Strategy for resolving offset conflicts
	MaxConflictsPerStream  int                        // Maximum conflicts before action
	CleanupInterval        time.Duration              // Interval for cleanup of inactive streams
	OffsetSyncTimeout      time.Duration              // Timeout for offset synchronization
	MaxSourcesPerStream    int                        // Maximum sources per KWIK stream
}

// ConflictResolutionStrategy defines how to resolve offset conflicts
type ConflictResolutionStrategy int

const (
	ConflictResolutionFirstWins ConflictResolutionStrategy = iota
	ConflictResolutionLastWins
	ConflictResolutionPrimaryWins
	ConflictResolutionReject
)

// OffsetManagerStats is defined in interfaces.go

// internalOffsetManagerStats contains statistics with mutex for internal use
type internalOffsetManagerStats struct {
	ActiveStreams        int
	TotalConflicts       uint64
	ResolvedConflicts    uint64
	PendingDataBytes     uint64
	ReorderingEvents     uint64
	SyncRequests         uint64
	SyncFailures         uint64
	LastUpdate           time.Time
	mutex                sync.RWMutex
}

// OffsetSyncRequest represents a request to synchronize offsets
type OffsetSyncRequest struct {
	KwikStreamID      uint64
	RequestingPathID  string
	CurrentOffset     uint64
	Timestamp         time.Time
}

// OffsetSyncResponse represents a response to offset synchronization
type OffsetSyncResponse struct {
	KwikStreamID    uint64
	ExpectedOffset  uint64
	SyncRequired    bool
	Timestamp       time.Time
}

// OffsetConflict represents an offset conflict
type OffsetConflict struct {
	KwikStreamID uint64
	Offset       uint64
	Sources      []string
	Data         map[string][]byte // sourceID -> data
	Timestamp    time.Time
}

// NewMultiSourceOffsetManager creates a new multi-source offset manager
func NewMultiSourceOffsetManager() *MultiSourceOffsetManager {
	return &MultiSourceOffsetManager{
		kwikStreamOffsets: make(map[uint64]*KwikStreamOffsetState),
		sourceOffsets:     make(map[string]map[uint64]uint64),
		config: &MultiSourceOffsetManagerConfig{
			MaxPendingData:        1000,
			ReorderTimeout:        100 * time.Millisecond,
			ConflictResolution:    ConflictResolutionFirstWins,
			MaxConflictsPerStream: 10,
			CleanupInterval:       30 * time.Second,
			OffsetSyncTimeout:     5 * time.Second,
			MaxSourcesPerStream:   10,
		},
		stats: &internalOffsetManagerStats{
			LastUpdate: time.Now(),
		},
	}
}

// RegisterSource registers a new source for offset tracking
func (msom *MultiSourceOffsetManager) RegisterSource(pathID string) error {
	if pathID == "" {
		return utils.NewKwikError(utils.ErrInvalidFrame, "path ID is empty", nil)
	}

	msom.sourcesMutex.Lock()
	defer msom.sourcesMutex.Unlock()

	if _, exists := msom.sourceOffsets[pathID]; !exists {
		msom.sourceOffsets[pathID] = make(map[uint64]uint64)
	}

	return nil
}

// UnregisterSource unregisters a source from offset tracking
func (msom *MultiSourceOffsetManager) UnregisterSource(pathID string) error {
	msom.sourcesMutex.Lock()
	defer msom.sourcesMutex.Unlock()

	delete(msom.sourceOffsets, pathID)
	return nil
}

// UpdateOffset updates the offset for a source and KWIK stream
func (msom *MultiSourceOffsetManager) UpdateOffset(kwikStreamID uint64, pathID string, secondaryStreamID uint64, offset uint64, data []byte) error {
	if kwikStreamID == 0 || pathID == "" {
		return utils.NewKwikError(utils.ErrInvalidFrame, "invalid parameters", nil)
	}

	// Get or create KWIK stream offset state
	streamState, err := msom.getOrCreateStreamState(kwikStreamID)
	if err != nil {
		return err
	}

	// Update source offset
	if err := msom.updateSourceOffset(pathID, secondaryStreamID, offset); err != nil {
		return err
	}

	// Process the data with offset
	return msom.processOffsetData(streamState, pathID, offset, data)
}

// ValidateOffset validates an offset for a KWIK stream
func (msom *MultiSourceOffsetManager) ValidateOffset(kwikStreamID uint64, offset uint64, sourcePathID string) error {
	msom.streamsMutex.RLock()
	streamState, exists := msom.kwikStreamOffsets[kwikStreamID]
	msom.streamsMutex.RUnlock()

	if !exists {
		return utils.NewKwikError(utils.ErrStreamCreationFailed, "KWIK stream not found", nil)
	}

	streamState.mutex.RLock()
	defer streamState.mutex.RUnlock()

	// Check if offset is valid (not too far in the future)
	if offset > streamState.NextExpectedOffset+uint64(msom.config.MaxPendingData) {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			fmt.Sprintf("offset %d too far ahead of expected %d", offset, streamState.NextExpectedOffset), nil)
	}

	// Check for conflicts with existing data
	if existingData, exists := streamState.PendingData[offset]; exists && len(existingData) > 0 {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			fmt.Sprintf("offset %d already has data", offset), nil)
	}

	return nil
}

// GetNextExpectedOffset returns the next expected offset for a KWIK stream
func (msom *MultiSourceOffsetManager) GetNextExpectedOffset(kwikStreamID uint64) (uint64, error) {
	msom.streamsMutex.RLock()
	streamState, exists := msom.kwikStreamOffsets[kwikStreamID]
	msom.streamsMutex.RUnlock()

	if !exists {
		return 0, utils.NewKwikError(utils.ErrStreamCreationFailed, "KWIK stream not found", nil)
	}

	streamState.mutex.RLock()
	defer streamState.mutex.RUnlock()

	return streamState.NextExpectedOffset, nil
}

// SynchronizeOffsets synchronizes offsets for a KWIK stream across all sources
func (msom *MultiSourceOffsetManager) SynchronizeOffsets(request *OffsetSyncRequest) (*OffsetSyncResponse, error) {
	if request == nil {
		return nil, utils.NewKwikError(utils.ErrInvalidFrame, "sync request is nil", nil)
	}

	msom.streamsMutex.RLock()
	streamState, exists := msom.kwikStreamOffsets[request.KwikStreamID]
	msom.streamsMutex.RUnlock()

	if !exists {
		return &OffsetSyncResponse{
			KwikStreamID:   request.KwikStreamID,
			ExpectedOffset: 0,
			SyncRequired:   true,
			Timestamp:      time.Now(),
		}, nil
	}

	streamState.mutex.RLock()
	expectedOffset := streamState.NextExpectedOffset
	streamState.mutex.RUnlock()

	syncRequired := request.CurrentOffset != expectedOffset

	// Update statistics
	msom.updateSyncStats(syncRequired)

	return &OffsetSyncResponse{
		KwikStreamID:   request.KwikStreamID,
		ExpectedOffset: expectedOffset,
		SyncRequired:   syncRequired,
		Timestamp:      time.Now(),
	}, nil
}

// ResolveOffsetConflict resolves an offset conflict using the configured strategy
func (msom *MultiSourceOffsetManager) ResolveOffsetConflict(conflict *OffsetConflict) ([]byte, error) {
	if conflict == nil {
		return nil, utils.NewKwikError(utils.ErrInvalidFrame, "conflict is nil", nil)
	}

	switch msom.config.ConflictResolution {
	case ConflictResolutionFirstWins:
		return msom.resolveFirstWins(conflict)
	case ConflictResolutionLastWins:
		return msom.resolveLastWins(conflict)
	case ConflictResolutionPrimaryWins:
		return msom.resolvePrimaryWins(conflict)
	case ConflictResolutionReject:
		return nil, utils.NewKwikError(utils.ErrInvalidFrame, "offset conflict rejected", nil)
	default:
		return msom.resolveFirstWins(conflict)
	}
}

// GetPendingData returns pending data for a KWIK stream
func (msom *MultiSourceOffsetManager) GetPendingData(kwikStreamID uint64) (map[uint64][]byte, error) {
	msom.streamsMutex.RLock()
	streamState, exists := msom.kwikStreamOffsets[kwikStreamID]
	msom.streamsMutex.RUnlock()

	if !exists {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed, "KWIK stream not found", nil)
	}

	streamState.mutex.RLock()
	defer streamState.mutex.RUnlock()

	// Return copy of pending data
	pendingData := make(map[uint64][]byte)
	for offset, data := range streamState.PendingData {
		dataCopy := make([]byte, len(data))
		copy(dataCopy, data)
		pendingData[offset] = dataCopy
	}

	return pendingData, nil
}

// GetOffsetManagerStats returns statistics for the offset manager
func (msom *MultiSourceOffsetManager) GetOffsetManagerStats() *OffsetManagerStats {
	msom.stats.mutex.RLock()
	defer msom.stats.mutex.RUnlock()

	// Return copy of stats
	return &OffsetManagerStats{
		ActiveStreams:     msom.stats.ActiveStreams,
		TotalConflicts:    msom.stats.TotalConflicts,
		ResolvedConflicts: msom.stats.ResolvedConflicts,
		PendingDataBytes:  msom.stats.PendingDataBytes,
		ReorderingEvents:  msom.stats.ReorderingEvents,
		SyncRequests:      msom.stats.SyncRequests,
		SyncFailures:      msom.stats.SyncFailures,
		LastUpdate:        msom.stats.LastUpdate,
	}
}

// getOrCreateStreamState gets or creates offset state for a KWIK stream
func (msom *MultiSourceOffsetManager) getOrCreateStreamState(kwikStreamID uint64) (*KwikStreamOffsetState, error) {
	msom.streamsMutex.Lock()
	defer msom.streamsMutex.Unlock()

	if state, exists := msom.kwikStreamOffsets[kwikStreamID]; exists {
		return state, nil
	}

	state := &KwikStreamOffsetState{
		StreamID:           kwikStreamID,
		NextExpectedOffset: 0,
		SourceOffsets:      make(map[string]uint64),
		PendingData:        make(map[uint64][]byte),
		LastUpdate:         time.Now(),
		ConflictCount:      0,
		TotalBytesReceived: 0,
	}

	msom.kwikStreamOffsets[kwikStreamID] = state
	return state, nil
}

// updateSourceOffset updates the offset for a specific source
func (msom *MultiSourceOffsetManager) updateSourceOffset(pathID string, secondaryStreamID uint64, offset uint64) error {
	msom.sourcesMutex.Lock()
	defer msom.sourcesMutex.Unlock()

	if _, exists := msom.sourceOffsets[pathID]; !exists {
		msom.sourceOffsets[pathID] = make(map[uint64]uint64)
	}

	msom.sourceOffsets[pathID][secondaryStreamID] = offset
	return nil
}

// processOffsetData processes data with its offset
func (msom *MultiSourceOffsetManager) processOffsetData(streamState *KwikStreamOffsetState, pathID string, offset uint64, data []byte) error {
	streamState.mutex.Lock()
	defer streamState.mutex.Unlock()

	// Check for conflicts
	if existingData, exists := streamState.PendingData[offset]; exists && len(existingData) > 0 {
		// Handle conflict
		conflict := &OffsetConflict{
			KwikStreamID: streamState.StreamID,
			Offset:       offset,
			Sources:      []string{pathID}, // Add existing source info if available
			Data:         map[string][]byte{pathID: data},
			Timestamp:    time.Now(),
		}

		resolvedData, err := msom.ResolveOffsetConflict(conflict)
		if err != nil {
			streamState.ConflictCount++
			msom.updateConflictStats()
			return err
		}

		data = resolvedData
		msom.updateConflictStats()
	}

	// Store data at offset
	streamState.PendingData[offset] = data
	streamState.SourceOffsets[pathID] = offset
	streamState.TotalBytesReceived += uint64(len(data))
	streamState.LastUpdate = time.Now()

	// Try to advance the expected offset
	msom.advanceExpectedOffset(streamState)

	return nil
}

// advanceExpectedOffset advances the expected offset if data is available
func (msom *MultiSourceOffsetManager) advanceExpectedOffset(streamState *KwikStreamOffsetState) {
	for {
		data, exists := streamState.PendingData[streamState.NextExpectedOffset]
		if !exists || len(data) == 0 {
			break
		}

		// Data is available at expected offset, advance
		streamState.NextExpectedOffset += uint64(len(data))
		delete(streamState.PendingData, streamState.NextExpectedOffset-uint64(len(data)))
		
		// Update reordering stats
		msom.updateReorderingStats()
	}
}

// resolveFirstWins resolves conflict by keeping the first data received
func (msom *MultiSourceOffsetManager) resolveFirstWins(conflict *OffsetConflict) ([]byte, error) {
	// Return the first data in the conflict (implementation depends on how we track order)
	for _, data := range conflict.Data {
		return data, nil
	}
	return nil, utils.NewKwikError(utils.ErrInvalidFrame, "no data in conflict", nil)
}

// resolveLastWins resolves conflict by keeping the last data received
func (msom *MultiSourceOffsetManager) resolveLastWins(conflict *OffsetConflict) ([]byte, error) {
	var lastData []byte
	// In a real implementation, we'd track timestamps to determine "last"
	for _, data := range conflict.Data {
		lastData = data
	}
	if lastData == nil {
		return nil, utils.NewKwikError(utils.ErrInvalidFrame, "no data in conflict", nil)
	}
	return lastData, nil
}

// resolvePrimaryWins resolves conflict by preferring data from primary source
func (msom *MultiSourceOffsetManager) resolvePrimaryWins(conflict *OffsetConflict) ([]byte, error) {
	// Look for primary source (implementation depends on how we identify primary)
	// For now, assume first source is primary
	if len(conflict.Sources) > 0 {
		if data, exists := conflict.Data[conflict.Sources[0]]; exists {
			return data, nil
		}
	}
	return msom.resolveFirstWins(conflict)
}

// updateSyncStats updates synchronization statistics
func (msom *MultiSourceOffsetManager) updateSyncStats(syncRequired bool) {
	msom.stats.mutex.Lock()
	defer msom.stats.mutex.Unlock()

	msom.stats.SyncRequests++
	if syncRequired {
		msom.stats.SyncFailures++
	}
	msom.stats.LastUpdate = time.Now()
}

// updateConflictStats updates conflict statistics
func (msom *MultiSourceOffsetManager) updateConflictStats() {
	msom.stats.mutex.Lock()
	defer msom.stats.mutex.Unlock()

	msom.stats.TotalConflicts++
	msom.stats.ResolvedConflicts++
	msom.stats.LastUpdate = time.Now()
}

// updateReorderingStats updates reordering statistics
func (msom *MultiSourceOffsetManager) updateReorderingStats() {
	msom.stats.mutex.Lock()
	defer msom.stats.mutex.Unlock()

	msom.stats.ReorderingEvents++
	msom.stats.LastUpdate = time.Now()
}

// CloseStream closes offset tracking for a KWIK stream
func (msom *MultiSourceOffsetManager) CloseStream(kwikStreamID uint64) error {
	msom.streamsMutex.Lock()
	defer msom.streamsMutex.Unlock()

	delete(msom.kwikStreamOffsets, kwikStreamID)
	return nil
}

// CleanupInactiveStreams removes inactive streams based on timeout
func (msom *MultiSourceOffsetManager) CleanupInactiveStreams() error {
	msom.streamsMutex.Lock()
	defer msom.streamsMutex.Unlock()

	now := time.Now()
	timeout := msom.config.CleanupInterval

	for streamID, state := range msom.kwikStreamOffsets {
		state.mutex.RLock()
		inactive := now.Sub(state.LastUpdate) > timeout
		state.mutex.RUnlock()

		if inactive {
			delete(msom.kwikStreamOffsets, streamID)
		}
	}

	return nil
}