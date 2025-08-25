package data

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"kwik/internal/utils"
	"kwik/pkg/logger"
)

// OffsetCoordinator manages offset allocation and continuity across multiple paths
type OffsetCoordinator interface {
	// ReserveOffsetRange reserves a range of offsets for a stream
	ReserveOffsetRange(streamID uint64, size int) (startOffset int64, err error)

	// CommitOffsetRange confirms the usage of a reserved offset range
	CommitOffsetRange(streamID uint64, startOffset int64, actualSize int) error

	// ValidateOffsetContinuity checks for gaps in the offset sequence
	ValidateOffsetContinuity(streamID uint64) (gaps []OffsetGap, err error)

	// RequestMissingData requests retransmission of missing data
	RequestMissingData(streamID uint64, gaps []OffsetGap) error

	// RegisterDataReceived registers that data has been received at a specific offset
	RegisterDataReceived(streamID uint64, offset int64, size int) error

	// GetNextExpectedOffset returns the next expected offset for a stream
	GetNextExpectedOffset(streamID uint64) int64

	// GetStreamState returns the current offset state for a stream
	GetStreamState(streamID uint64) (*StreamOffsetState, error)

	// CloseStream cleans up offset tracking for a stream
	CloseStream(streamID uint64) error

	// Start starts the offset coordinator
	Start() error

	// Stop stops the offset coordinator and cleans up resources
	Stop() error

	// GetStats returns current offset coordinator statistics
	GetStats() OffsetCoordinatorStats
}

// OffsetGap represents a gap in the offset sequence
type OffsetGap struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
	Size  int64 `json:"size"`
}

// StreamOffsetState represents the offset state for a stream
type StreamOffsetState struct {
	StreamID        uint64        `json:"stream_id"`
	NextOffset      int64         `json:"next_offset"`
	CommittedOffset int64         `json:"committed_offset"`
	ReservedRanges  []OffsetRange `json:"reserved_ranges"`
	ReceivedRanges  []OffsetRange `json:"received_ranges"`
	Gaps            []OffsetGap   `json:"gaps"`
	LastUpdate      time.Time     `json:"last_update"`

	// Internal synchronization
	mutex sync.RWMutex
}

// OffsetRange represents a range of offsets
type OffsetRange struct {
	Start    int64  `json:"start"`
	End      int64  `json:"end"`
	Reserved bool   `json:"reserved"`
	PathID   string `json:"path_id"`

	// Metadata
	ReservedAt  time.Time `json:"reserved_at"`
	CommittedAt time.Time `json:"committed_at,omitempty"`
}

// OffsetCoordinatorImpl implements the OffsetCoordinator interface
type OffsetCoordinatorImpl struct {
	// Stream states
	streamStates map[uint64]*StreamOffsetState
	statesMutex  sync.RWMutex

	// Configuration
	config *OffsetCoordinatorConfig
	logger logger.Logger

	// Control
	running   bool
	stopChan  chan struct{}
	waitGroup sync.WaitGroup
	mutex     sync.RWMutex

	// Statistics
	stats      OffsetCoordinatorStats
	statsMutex sync.RWMutex

	// Callbacks
	onMissingDataCallback func(streamID uint64, gaps []OffsetGap) error
}

// OffsetCoordinatorConfig holds configuration for the offset coordinator
type OffsetCoordinatorConfig struct {
	MaxStreams           int           `json:"max_streams"`
	GapDetectionInterval time.Duration `json:"gap_detection_interval"`
	MaxGapAge            time.Duration `json:"max_gap_age"`
	CleanupInterval      time.Duration `json:"cleanup_interval"`
	EnableDetailedStats  bool          `json:"enable_detailed_stats"`
	Logger               logger.Logger `json:"-"`
}

// OffsetCoordinatorStats provides statistics about offset coordination
type OffsetCoordinatorStats struct {
	ActiveStreams        int       `json:"active_streams"`
	TotalRangesReserved  uint64    `json:"total_ranges_reserved"`
	TotalRangesCommitted uint64    `json:"total_ranges_committed"`
	TotalGapsDetected    uint64    `json:"total_gaps_detected"`
	TotalGapsResolved    uint64    `json:"total_gaps_resolved"`
	AverageGapSize       float64   `json:"average_gap_size"`
	LastUpdate           time.Time `json:"last_update"`
}

// DefaultOffsetCoordinatorConfig returns default configuration
func DefaultOffsetCoordinatorConfig() *OffsetCoordinatorConfig {
	return &OffsetCoordinatorConfig{
		MaxStreams:           1000,
		GapDetectionInterval: 100 * time.Millisecond,
		MaxGapAge:            5 * time.Second,
		CleanupInterval:      30 * time.Second,
		EnableDetailedStats:  true,
		Logger:               nil,
	}
}

// NewOffsetCoordinator creates a new offset coordinator
func NewOffsetCoordinator(config *OffsetCoordinatorConfig) *OffsetCoordinatorImpl {
	if config == nil {
		config = DefaultOffsetCoordinatorConfig()
	}

	return &OffsetCoordinatorImpl{
		streamStates: make(map[uint64]*StreamOffsetState),
		config:       config,
		logger:       config.Logger,
		stopChan:     make(chan struct{}),
		stats: OffsetCoordinatorStats{
			LastUpdate: time.Now(),
		},
	}
}

// SetMissingDataCallback sets the callback for missing data requests
func (oc *OffsetCoordinatorImpl) SetMissingDataCallback(callback func(streamID uint64, gaps []OffsetGap) error) {
	oc.mutex.Lock()
	defer oc.mutex.Unlock()
	oc.onMissingDataCallback = callback
}

// Start implements OffsetCoordinator.Start
func (oc *OffsetCoordinatorImpl) Start() error {
	oc.mutex.Lock()
	defer oc.mutex.Unlock()

	if oc.running {
		return utils.NewKwikError(utils.ErrInvalidState, "offset coordinator is already running", nil)
	}

	oc.running = true
	oc.stopChan = make(chan struct{})

	// Start gap detection goroutine
	oc.waitGroup.Add(1)
	go oc.gapDetectionLoop()

	// Start cleanup goroutine
	oc.waitGroup.Add(1)
	go oc.cleanupLoop()

	if oc.logger != nil {
		oc.logger.Info("Offset coordinator started")
	}

	return nil
}

// Stop implements OffsetCoordinator.Stop
func (oc *OffsetCoordinatorImpl) Stop() error {
	oc.mutex.Lock()
	if !oc.running {
		oc.mutex.Unlock()
		return nil
	}

	oc.running = false
	close(oc.stopChan)
	oc.mutex.Unlock()

	// Wait for goroutines to finish
	oc.waitGroup.Wait()

	// Clean up all stream states
	oc.statesMutex.Lock()
	oc.streamStates = make(map[uint64]*StreamOffsetState)
	oc.statesMutex.Unlock()

	if oc.logger != nil {
		oc.logger.Info("Offset coordinator stopped")
	}

	return nil
}

// ReserveOffsetRange implements OffsetCoordinator.ReserveOffsetRange
func (oc *OffsetCoordinatorImpl) ReserveOffsetRange(streamID uint64, size int) (startOffset int64, err error) {
	oc.mutex.RLock()
	if !oc.running {
		oc.mutex.RUnlock()
		return 0, utils.NewKwikError(utils.ErrInvalidState, "offset coordinator is not running", nil)
	}
	oc.mutex.RUnlock()

	if size <= 0 {
		return 0, utils.NewKwikError(utils.ErrInvalidFrame, "size must be positive", nil)
	}

	// Get or create stream state
	state := oc.getOrCreateStreamState(streamID)

	state.mutex.Lock()
	defer state.mutex.Unlock()

	// Find the next available offset
	startOffset = state.NextOffset
	endOffset := startOffset + int64(size)

	// Check for conflicts with existing reservations
	for _, reserved := range state.ReservedRanges {
		if reserved.Reserved && oc.rangesOverlap(startOffset, endOffset, reserved.Start, reserved.End) {
			// Find next available position after this reservation
			if reserved.End >= startOffset {
				startOffset = reserved.End
				endOffset = startOffset + int64(size)
			}
		}
	}

	// Create reservation
	reservation := OffsetRange{
		Start:      startOffset,
		End:        endOffset,
		Reserved:   true,
		ReservedAt: time.Now(),
	}

	state.ReservedRanges = append(state.ReservedRanges, reservation)
	state.NextOffset = endOffset
	state.LastUpdate = time.Now()

	// Update statistics
	oc.updateStats(func(stats *OffsetCoordinatorStats) {
		stats.TotalRangesReserved++
	})

	if oc.logger != nil {
		oc.logger.Debug("Reserved offset range",
			"streamID", streamID,
			"startOffset", startOffset,
			"size", size,
			"endOffset", endOffset)
	}

	return startOffset, nil
}

// CommitOffsetRange implements OffsetCoordinator.CommitOffsetRange
func (oc *OffsetCoordinatorImpl) CommitOffsetRange(streamID uint64, startOffset int64, actualSize int) error {
	oc.mutex.RLock()
	if !oc.running {
		oc.mutex.RUnlock()
		return utils.NewKwikError(utils.ErrInvalidState, "offset coordinator is not running", nil)
	}
	oc.mutex.RUnlock()

	state := oc.getStreamState(streamID)
	if state == nil {
		return utils.NewKwikError(utils.ErrStreamNotFound, fmt.Sprintf("stream %d not found", streamID), nil)
	}

	state.mutex.Lock()
	defer state.mutex.Unlock()

	// Find the reservation
	reservationIndex := -1
	for i, reserved := range state.ReservedRanges {
		if reserved.Start == startOffset && reserved.Reserved {
			reservationIndex = i
			break
		}
	}

	if reservationIndex == -1 {
		return utils.NewKwikError(utils.ErrOffsetMismatch,
			fmt.Sprintf("no reservation found for offset %d", startOffset), nil)
	}

	// Update the reservation
	reservation := &state.ReservedRanges[reservationIndex]
	reservation.Reserved = false
	reservation.CommittedAt = time.Now()

	// Adjust end offset based on actual size
	actualEndOffset := startOffset + int64(actualSize)
	reservation.End = actualEndOffset

	// Update committed offset if this extends it
	if actualEndOffset > state.CommittedOffset {
		state.CommittedOffset = actualEndOffset
	}

	state.LastUpdate = time.Now()

	// Update statistics
	oc.updateStats(func(stats *OffsetCoordinatorStats) {
		stats.TotalRangesCommitted++
	})

	if oc.logger != nil {
		oc.logger.Debug("Committed offset range",
			"streamID", streamID,
			"startOffset", startOffset,
			"actualSize", actualSize,
			"endOffset", actualEndOffset)
	}

	return nil
}

// RegisterDataReceived implements OffsetCoordinator.RegisterDataReceived
func (oc *OffsetCoordinatorImpl) RegisterDataReceived(streamID uint64, offset int64, size int) error {
	oc.mutex.RLock()
	if !oc.running {
		oc.mutex.RUnlock()
		return utils.NewKwikError(utils.ErrInvalidState, "offset coordinator is not running", nil)
	}
	oc.mutex.RUnlock()

	state := oc.getOrCreateStreamState(streamID)

	state.mutex.Lock()
	defer state.mutex.Unlock()

	endOffset := offset + int64(size)

	// Add to received ranges
	receivedRange := OffsetRange{
		Start:       offset,
		End:         endOffset,
		Reserved:    false,
		CommittedAt: time.Now(),
	}

	state.ReceivedRanges = append(state.ReceivedRanges, receivedRange)

	// Sort received ranges by start offset
	sort.Slice(state.ReceivedRanges, func(i, j int) bool {
		return state.ReceivedRanges[i].Start < state.ReceivedRanges[j].Start
	})

	// Merge overlapping ranges
	oc.mergeReceivedRanges(state)

	// Update committed offset
	if len(state.ReceivedRanges) > 0 && state.ReceivedRanges[0].Start == 0 {
		// We have contiguous data from the beginning
		state.CommittedOffset = state.ReceivedRanges[0].End
	}

	state.LastUpdate = time.Now()

	if oc.logger != nil {
		oc.logger.Debug("Registered data received",
			"streamID", streamID,
			"offset", offset,
			"size", size,
			"committedOffset", state.CommittedOffset)
	}

	return nil
}

// ValidateOffsetContinuity implements OffsetCoordinator.ValidateOffsetContinuity
func (oc *OffsetCoordinatorImpl) ValidateOffsetContinuity(streamID uint64) (gaps []OffsetGap, err error) {
	state := oc.getStreamState(streamID)
	if state == nil {
		return nil, utils.NewKwikError(utils.ErrStreamNotFound, fmt.Sprintf("stream %d not found", streamID), nil)
	}

	state.mutex.RLock()
	defer state.mutex.RUnlock()

	gaps = oc.detectGaps(state)

	// Update gaps in state
	state.Gaps = gaps

	if oc.logger != nil && len(gaps) > 0 {
		oc.logger.Debug("Detected offset gaps",
			"streamID", streamID,
			"gapCount", len(gaps))
	}

	return gaps, nil
}

// RequestMissingData implements OffsetCoordinator.RequestMissingData
func (oc *OffsetCoordinatorImpl) RequestMissingData(streamID uint64, gaps []OffsetGap) error {
	oc.mutex.RLock()
	callback := oc.onMissingDataCallback
	oc.mutex.RUnlock()

	if callback != nil {
		err := callback(streamID, gaps)
		if err != nil {
			return err
		}

		// Update statistics
		oc.updateStats(func(stats *OffsetCoordinatorStats) {
			stats.TotalGapsDetected += uint64(len(gaps))
		})
	}

	if oc.logger != nil {
		oc.logger.Debug("Requested missing data",
			"streamID", streamID,
			"gapCount", len(gaps))
	}

	return nil
}

// GetNextExpectedOffset implements OffsetCoordinator.GetNextExpectedOffset
func (oc *OffsetCoordinatorImpl) GetNextExpectedOffset(streamID uint64) int64 {
	state := oc.getStreamState(streamID)
	if state == nil {
		return 0
	}

	state.mutex.RLock()
	defer state.mutex.RUnlock()

	return state.NextOffset
}

// GetStreamState implements OffsetCoordinator.GetStreamState
func (oc *OffsetCoordinatorImpl) GetStreamState(streamID uint64) (*StreamOffsetState, error) {
	state := oc.getStreamState(streamID)
	if state == nil {
		return nil, utils.NewKwikError(utils.ErrStreamNotFound, fmt.Sprintf("stream %d not found", streamID), nil)
	}

	state.mutex.RLock()
	defer state.mutex.RUnlock()

	// Return a copy to avoid race conditions
	stateCopy := &StreamOffsetState{
		StreamID:        state.StreamID,
		NextOffset:      state.NextOffset,
		CommittedOffset: state.CommittedOffset,
		ReservedRanges:  make([]OffsetRange, len(state.ReservedRanges)),
		ReceivedRanges:  make([]OffsetRange, len(state.ReceivedRanges)),
		Gaps:            make([]OffsetGap, len(state.Gaps)),
		LastUpdate:      state.LastUpdate,
	}

	copy(stateCopy.ReservedRanges, state.ReservedRanges)
	copy(stateCopy.ReceivedRanges, state.ReceivedRanges)
	copy(stateCopy.Gaps, state.Gaps)

	return stateCopy, nil
}

// CloseStream implements OffsetCoordinator.CloseStream
func (oc *OffsetCoordinatorImpl) CloseStream(streamID uint64) error {
	oc.statesMutex.Lock()
	defer oc.statesMutex.Unlock()

	delete(oc.streamStates, streamID)

	// Update statistics
	oc.updateStats(func(stats *OffsetCoordinatorStats) {
		stats.ActiveStreams--
	})

	if oc.logger != nil {
		oc.logger.Debug("Closed stream offset tracking", "streamID", streamID)
	}

	return nil
}

// Internal helper methods

func (oc *OffsetCoordinatorImpl) getOrCreateStreamState(streamID uint64) *StreamOffsetState {
	oc.statesMutex.Lock()
	defer oc.statesMutex.Unlock()

	state, exists := oc.streamStates[streamID]
	if !exists {
		state = &StreamOffsetState{
			StreamID:        streamID,
			NextOffset:      0,
			CommittedOffset: 0,
			ReservedRanges:  make([]OffsetRange, 0),
			ReceivedRanges:  make([]OffsetRange, 0),
			Gaps:            make([]OffsetGap, 0),
			LastUpdate:      time.Now(),
		}
		oc.streamStates[streamID] = state

		// Update statistics
		oc.updateStats(func(stats *OffsetCoordinatorStats) {
			stats.ActiveStreams++
		})
	}

	return state
}

func (oc *OffsetCoordinatorImpl) getStreamState(streamID uint64) *StreamOffsetState {
	oc.statesMutex.RLock()
	defer oc.statesMutex.RUnlock()

	return oc.streamStates[streamID]
}

func (oc *OffsetCoordinatorImpl) rangesOverlap(start1, end1, start2, end2 int64) bool {
	return start1 < end2 && start2 < end1
}

func (oc *OffsetCoordinatorImpl) mergeReceivedRanges(state *StreamOffsetState) {
	if len(state.ReceivedRanges) <= 1 {
		return
	}

	merged := make([]OffsetRange, 0, len(state.ReceivedRanges))
	current := state.ReceivedRanges[0]

	for i := 1; i < len(state.ReceivedRanges); i++ {
		next := state.ReceivedRanges[i]

		if current.End >= next.Start {
			// Overlapping or adjacent ranges, merge them
			if next.End > current.End {
				current.End = next.End
			}
		} else {
			// Non-overlapping range, add current to merged and move to next
			merged = append(merged, current)
			current = next
		}
	}

	merged = append(merged, current)
	state.ReceivedRanges = merged
}

func (oc *OffsetCoordinatorImpl) detectGaps(state *StreamOffsetState) []OffsetGap {
	if len(state.ReceivedRanges) == 0 {
		return nil
	}

	var gaps []OffsetGap
	expectedOffset := int64(0)

	for _, received := range state.ReceivedRanges {
		if received.Start > expectedOffset {
			// Gap detected
			gap := OffsetGap{
				Start: expectedOffset,
				End:   received.Start,
				Size:  received.Start - expectedOffset,
			}
			gaps = append(gaps, gap)
		}

		if received.End > expectedOffset {
			expectedOffset = received.End
		}
	}

	return gaps
}

func (oc *OffsetCoordinatorImpl) updateStats(updater func(*OffsetCoordinatorStats)) {
	oc.statsMutex.Lock()
	defer oc.statsMutex.Unlock()

	updater(&oc.stats)
	oc.stats.LastUpdate = time.Now()
}

func (oc *OffsetCoordinatorImpl) gapDetectionLoop() {
	defer oc.waitGroup.Done()

	ticker := time.NewTicker(oc.config.GapDetectionInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			oc.performGapDetection()
		case <-oc.stopChan:
			return
		}
	}
}

func (oc *OffsetCoordinatorImpl) performGapDetection() {
	oc.statesMutex.RLock()
	streamIDs := make([]uint64, 0, len(oc.streamStates))
	for streamID := range oc.streamStates {
		streamIDs = append(streamIDs, streamID)
	}
	oc.statesMutex.RUnlock()

	for _, streamID := range streamIDs {
		gaps, err := oc.ValidateOffsetContinuity(streamID)
		if err != nil {
			continue
		}

		if len(gaps) > 0 {
			// Request missing data for detected gaps
			oc.RequestMissingData(streamID, gaps)
		}
	}
}

func (oc *OffsetCoordinatorImpl) cleanupLoop() {
	defer oc.waitGroup.Done()

	ticker := time.NewTicker(oc.config.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			oc.performCleanup()
		case <-oc.stopChan:
			return
		}
	}
}

func (oc *OffsetCoordinatorImpl) performCleanup() {
	now := time.Now()

	oc.statesMutex.Lock()
	defer oc.statesMutex.Unlock()

	for streamID, state := range oc.streamStates {
		state.mutex.Lock()

		// Clean up old reservations
		activeReservations := make([]OffsetRange, 0, len(state.ReservedRanges))
		for _, reservation := range state.ReservedRanges {
			if reservation.Reserved && now.Sub(reservation.ReservedAt) > oc.config.MaxGapAge {
				// Old reservation, remove it
				if oc.logger != nil {
					oc.logger.Warn("Cleaning up old reservation",
						"streamID", streamID,
						"startOffset", reservation.Start,
						"age", now.Sub(reservation.ReservedAt))
				}
			} else {
				activeReservations = append(activeReservations, reservation)
			}
		}
		state.ReservedRanges = activeReservations

		state.mutex.Unlock()
	}
}

// GetStats returns current offset coordinator statistics
func (oc *OffsetCoordinatorImpl) GetStats() OffsetCoordinatorStats {
	oc.statsMutex.RLock()
	defer oc.statsMutex.RUnlock()

	// Return a copy to avoid race conditions
	stats := oc.stats
	stats.LastUpdate = time.Now()
	return stats
}
