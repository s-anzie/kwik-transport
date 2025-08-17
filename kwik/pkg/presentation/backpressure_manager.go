package presentation

import (
	"fmt"
	"sync"
	"time"
)

// BackpressureManagerImpl implements the BackpressureManager interface
type BackpressureManagerImpl struct {
	// Per-stream backpressure state
	streamBackpressure map[uint64]*StreamBackpressureState
	streamMutex        sync.RWMutex
	
	// Global backpressure state
	globalBackpressure *GlobalBackpressureState
	globalMutex        sync.RWMutex
	
	// Configuration
	config *BackpressureConfig
	
	// Callbacks
	callback BackpressureCallback
	callbackMutex sync.RWMutex
	
	// Statistics
	stats *BackpressureStatsImpl
	statsMutex sync.RWMutex
	
	// Background worker
	workerStop chan struct{}
	workerWg   sync.WaitGroup
}

// StreamBackpressureState tracks backpressure state for a single stream
type StreamBackpressureState struct {
	StreamID    uint64
	Active      bool
	Reason      BackpressureReason
	ActivatedAt time.Time
	Duration    time.Duration
	mutex       sync.RWMutex
}

// GlobalBackpressureState tracks global backpressure state
type GlobalBackpressureState struct {
	Active      bool
	Reason      BackpressureReason
	ActivatedAt time.Time
	Duration    time.Duration
	mutex       sync.RWMutex
}

// BackpressureConfig holds configuration for the backpressure manager
type BackpressureConfig struct {
	MaxStreams              int           `json:"max_streams"`
	GlobalThreshold         float64       `json:"global_threshold"`
	StreamThreshold         float64       `json:"stream_threshold"`
	BackoffInitial          time.Duration `json:"backoff_initial"`
	BackoffMax              time.Duration `json:"backoff_max"`
	BackoffMultiplier       float64       `json:"backoff_multiplier"`
	AutoReleaseEnabled      bool          `json:"auto_release_enabled"`
	AutoReleaseInterval     time.Duration `json:"auto_release_interval"`
	StatsUpdateInterval     time.Duration `json:"stats_update_interval"`
	EnableDetailedLogging   bool          `json:"enable_detailed_logging"`
}

// BackpressureStatsImpl implements detailed backpressure statistics
type BackpressureStatsImpl struct {
	TotalActivations     uint64                       `json:"total_activations"`
	ActiveStreams        int                          `json:"active_streams"`
	GlobalActivations    uint64                       `json:"global_activations"`
	ReasonBreakdown      map[BackpressureReason]uint64 `json:"reason_breakdown"`
	AverageDuration      time.Duration                `json:"average_duration"`
	LastActivation       time.Time                    `json:"last_activation"`
	CurrentlyActive      bool                         `json:"currently_active"`
	
	// Additional detailed stats
	PeakActiveStreams    int                          `json:"peak_active_streams"`
	TotalDeactivations   uint64                       `json:"total_deactivations"`
	ActivationRate       float64                      `json:"activation_rate"`
	DeactivationRate     float64                      `json:"deactivation_rate"`
	LastUpdate           time.Time                    `json:"last_update"`
}

// NewBackpressureManager creates a new backpressure manager
func NewBackpressureManager(config *BackpressureConfig) *BackpressureManagerImpl {
	if config == nil {
		config = &BackpressureConfig{
			MaxStreams:              1000,
			GlobalThreshold:         0.9,
			StreamThreshold:         0.8,
			BackoffInitial:          10 * time.Millisecond,
			BackoffMax:              1 * time.Second,
			BackoffMultiplier:       2.0,
			AutoReleaseEnabled:      true,
			AutoReleaseInterval:     100 * time.Millisecond,
			StatsUpdateInterval:     1 * time.Second,
			EnableDetailedLogging:   false,
		}
	}
	
	bpm := &BackpressureManagerImpl{
		streamBackpressure: make(map[uint64]*StreamBackpressureState),
		globalBackpressure: &GlobalBackpressureState{
			Active: false,
		},
		config: config,
		stats: &BackpressureStatsImpl{
			ReasonBreakdown:      make(map[BackpressureReason]uint64),
			TotalActivations:     0,
			ActiveStreams:        0,
			GlobalActivations:    0,
			AverageDuration:      0,
			CurrentlyActive:      false,
			PeakActiveStreams:    0,
			TotalDeactivations:   0,
			ActivationRate:       0,
			DeactivationRate:     0,
			LastUpdate:           time.Now(),
		},
		workerStop: make(chan struct{}),
	}
	
	// Start background worker if auto-release is enabled
	if config.AutoReleaseEnabled {
		bpm.workerWg.Add(1)
		go bpm.backgroundWorker()
	}
	
	return bpm
}

// ActivateBackpressure activates backpressure for a specific stream
func (bpm *BackpressureManagerImpl) ActivateBackpressure(streamID uint64, reason BackpressureReason) error {
	if streamID == 0 {
		return fmt.Errorf("invalid stream ID: 0")
	}
	
	bpm.streamMutex.Lock()
	defer bpm.streamMutex.Unlock()
	
	// Check stream limit
	if len(bpm.streamBackpressure) >= bpm.config.MaxStreams {
		if _, exists := bpm.streamBackpressure[streamID]; !exists {
			return fmt.Errorf("maximum number of streams (%d) reached", bpm.config.MaxStreams)
		}
	}
	
	// Get or create stream state
	state, exists := bpm.streamBackpressure[streamID]
	if !exists {
		state = &StreamBackpressureState{
			StreamID: streamID,
			Active:   false,
		}
		bpm.streamBackpressure[streamID] = state
	}
	
	state.mutex.Lock()
	wasActive := state.Active
	if !wasActive {
		state.Active = true
		state.Reason = reason
		state.ActivatedAt = time.Now()
		state.Duration = 0
	}
	state.mutex.Unlock()
	
	if !wasActive {
		// Update statistics
		bpm.updateActivationStats(streamID, reason)
		
		// Trigger callback
		bpm.triggerCallback(streamID, true, reason)
		
		// Check if global backpressure should be activated
		shouldActivateGlobal := bpm.shouldActivateGlobalBackpressure()
		if shouldActivateGlobal {
			// Release streamMutex before calling ActivateGlobalBackpressure to avoid deadlock
			bpm.streamMutex.Unlock()
			bpm.ActivateGlobalBackpressure(BackpressureReasonSlowConsumer)
			bpm.streamMutex.Lock()
		}
	}
	
	return nil
}

// DeactivateBackpressure deactivates backpressure for a specific stream
func (bpm *BackpressureManagerImpl) DeactivateBackpressure(streamID uint64) error {
	if streamID == 0 {
		return fmt.Errorf("invalid stream ID: 0")
	}
	
	bpm.streamMutex.Lock()
	defer bpm.streamMutex.Unlock()
	
	state, exists := bpm.streamBackpressure[streamID]
	if !exists {
		return nil // Stream not found, nothing to deactivate
	}
	
	state.mutex.Lock()
	wasActive := state.Active
	reason := state.Reason
	if wasActive {
		state.Active = false
		state.Duration = time.Since(state.ActivatedAt)
	}
	state.mutex.Unlock()
	
	if wasActive {
		// Update statistics
		bpm.updateDeactivationStats(streamID, state.Duration)
		
		// Trigger callback
		bpm.triggerCallback(streamID, false, reason)
		
		// Check if global backpressure should be deactivated (lock already held)
		bpm.checkGlobalBackpressureDeactivationLocked()
	}
	
	return nil
}

// IsBackpressureActive returns true if backpressure is active for the stream
func (bpm *BackpressureManagerImpl) IsBackpressureActive(streamID uint64) bool {
	bpm.streamMutex.RLock()
	defer bpm.streamMutex.RUnlock()
	
	state, exists := bpm.streamBackpressure[streamID]
	if !exists {
		return false
	}
	
	state.mutex.RLock()
	defer state.mutex.RUnlock()
	return state.Active
}

// GetBackpressureReason returns the reason for backpressure activation
func (bpm *BackpressureManagerImpl) GetBackpressureReason(streamID uint64) BackpressureReason {
	bpm.streamMutex.RLock()
	defer bpm.streamMutex.RUnlock()
	
	state, exists := bpm.streamBackpressure[streamID]
	if !exists {
		return BackpressureReasonNone
	}
	
	state.mutex.RLock()
	defer state.mutex.RUnlock()
	
	if !state.Active {
		return BackpressureReasonNone
	}
	
	return state.Reason
}

// ActivateGlobalBackpressure activates global backpressure
func (bpm *BackpressureManagerImpl) ActivateGlobalBackpressure(reason BackpressureReason) error {
	bpm.globalMutex.Lock()
	defer bpm.globalMutex.Unlock()
	
	bpm.globalBackpressure.mutex.Lock()
	wasActive := bpm.globalBackpressure.Active
	if !wasActive {
		bpm.globalBackpressure.Active = true
		bpm.globalBackpressure.Reason = reason
		bpm.globalBackpressure.ActivatedAt = time.Now()
		bpm.globalBackpressure.Duration = 0
	}
	bpm.globalBackpressure.mutex.Unlock()
	
	if !wasActive {
		// Update statistics
		bpm.statsMutex.Lock()
		bpm.stats.GlobalActivations++
		bpm.stats.CurrentlyActive = true
		bpm.stats.LastActivation = time.Now()
		bpm.stats.ReasonBreakdown[reason]++
		bpm.statsMutex.Unlock()
		
		// Trigger callback for all streams
		bpm.triggerGlobalCallback(true, reason)
	}
	
	return nil
}

// DeactivateGlobalBackpressure deactivates global backpressure
func (bpm *BackpressureManagerImpl) DeactivateGlobalBackpressure() error {
	bpm.globalMutex.Lock()
	defer bpm.globalMutex.Unlock()
	
	bpm.globalBackpressure.mutex.Lock()
	wasActive := bpm.globalBackpressure.Active
	reason := bpm.globalBackpressure.Reason
	if wasActive {
		bpm.globalBackpressure.Active = false
		bpm.globalBackpressure.Duration = time.Since(bpm.globalBackpressure.ActivatedAt)
	}
	bpm.globalBackpressure.mutex.Unlock()
	
	if wasActive {
		// Update statistics
		bpm.statsMutex.Lock()
		bpm.stats.CurrentlyActive = false
		bpm.statsMutex.Unlock()
		
		// Trigger callback for all streams
		bpm.triggerGlobalCallback(false, reason)
	}
	
	return nil
}

// deactivateGlobalBackpressureNoCallback deactivates global backpressure without triggering callbacks
// This is used when called from within a locked context to avoid deadlocks
func (bpm *BackpressureManagerImpl) deactivateGlobalBackpressureNoCallback() error {
	bpm.globalMutex.Lock()
	defer bpm.globalMutex.Unlock()
	
	bpm.globalBackpressure.mutex.Lock()
	wasActive := bpm.globalBackpressure.Active
	if wasActive {
		bpm.globalBackpressure.Active = false
		bpm.globalBackpressure.Duration = time.Since(bpm.globalBackpressure.ActivatedAt)
	}
	bpm.globalBackpressure.mutex.Unlock()
	
	if wasActive {
		// Update statistics
		bpm.statsMutex.Lock()
		bpm.stats.CurrentlyActive = false
		bpm.statsMutex.Unlock()
		
		// Note: No callback triggering to avoid deadlocks
	}
	
	return nil
}

// IsGlobalBackpressureActive returns true if global backpressure is active
func (bpm *BackpressureManagerImpl) IsGlobalBackpressureActive() bool {
	bpm.globalMutex.RLock()
	defer bpm.globalMutex.RUnlock()
	
	bpm.globalBackpressure.mutex.RLock()
	defer bpm.globalBackpressure.mutex.RUnlock()
	
	return bpm.globalBackpressure.Active
}

// SetBackpressureCallback sets the callback for backpressure events
func (bpm *BackpressureManagerImpl) SetBackpressureCallback(callback BackpressureCallback) error {
	bpm.callbackMutex.Lock()
	defer bpm.callbackMutex.Unlock()
	
	bpm.callback = callback
	return nil
}

// GetBackpressureStats returns current backpressure statistics
func (bpm *BackpressureManagerImpl) GetBackpressureStats() *BackpressureStats {
	// Count currently active streams first (without holding statsMutex)
	bpm.streamMutex.RLock()
	activeCount := 0
	for _, state := range bpm.streamBackpressure {
		state.mutex.RLock()
		if state.Active {
			activeCount++
		}
		state.mutex.RUnlock()
	}
	bpm.streamMutex.RUnlock()
	
	// Now get stats (without holding streamMutex)
	bpm.statsMutex.RLock()
	defer bpm.statsMutex.RUnlock()
	
	// Create reason breakdown copy
	reasonBreakdown := make(map[BackpressureReason]uint64)
	for reason, count := range bpm.stats.ReasonBreakdown {
		reasonBreakdown[reason] = count
	}
	
	return &BackpressureStats{
		TotalActivations:     bpm.stats.TotalActivations,
		ActiveStreams:        activeCount,
		GlobalActivations:    bpm.stats.GlobalActivations,
		ReasonBreakdown:      reasonBreakdown,
		AverageDuration:      bpm.stats.AverageDuration,
		LastActivation:       bpm.stats.LastActivation,
		CurrentlyActive:      bpm.IsGlobalBackpressureActive(),
	}
}

// GetBackpressureStatus returns current backpressure status
func (bpm *BackpressureManagerImpl) GetBackpressureStatus() *BackpressureStatus {
	bpm.statsMutex.RLock()
	defer bpm.statsMutex.RUnlock()
	
	bpm.globalMutex.RLock()
	globalActive := bpm.globalBackpressure.Active
	globalReason := bpm.globalBackpressure.Reason
	bpm.globalMutex.RUnlock()
	
	bpm.streamMutex.RLock()
	activeCount := 0
	totalStreams := len(bpm.streamBackpressure)
	streamStatus := make(map[uint64]StreamBackpressureInfo)
	
	for streamID, state := range bpm.streamBackpressure {
		state.mutex.RLock()
		if state.Active {
			activeCount++
		}
		
		streamStatus[streamID] = StreamBackpressureInfo{
			StreamID:    streamID,
			Active:      state.Active,
			Reason:      state.Reason,
			Duration:    state.Duration,
			ActivatedAt: state.ActivatedAt,
		}
		state.mutex.RUnlock()
	}
	bpm.streamMutex.RUnlock()
	
	return &BackpressureStatus{
		GlobalActive:   globalActive,
		GlobalReason:   globalReason,
		StreamStatus:   streamStatus,
		ActiveStreams:  activeCount,
		TotalStreams:   totalStreams,
		LastActivation: bpm.stats.LastActivation,
	}
}

// Helper methods

// updateActivationStats updates statistics when backpressure is activated
// Note: This method assumes streamMutex is already held by the caller
func (bpm *BackpressureManagerImpl) updateActivationStats(streamID uint64, reason BackpressureReason) {
	bpm.statsMutex.Lock()
	defer bpm.statsMutex.Unlock()
	
	bpm.stats.TotalActivations++
	bpm.stats.LastActivation = time.Now()
	bpm.stats.ReasonBreakdown[reason]++
	
	// Count active streams (streamMutex is already held by caller)
	activeCount := 0
	for _, state := range bpm.streamBackpressure {
		state.mutex.RLock()
		if state.Active {
			activeCount++
		}
		state.mutex.RUnlock()
	}
	
	bpm.stats.ActiveStreams = activeCount
	if activeCount > bpm.stats.PeakActiveStreams {
		bpm.stats.PeakActiveStreams = activeCount
	}
	
	bpm.stats.LastUpdate = time.Now()
}

// updateDeactivationStats updates statistics when backpressure is deactivated
// Note: This method assumes streamMutex is already held by the caller
func (bpm *BackpressureManagerImpl) updateDeactivationStats(streamID uint64, duration time.Duration) {
	bpm.statsMutex.Lock()
	defer bpm.statsMutex.Unlock()
	
	bpm.stats.TotalDeactivations++
	
	// Update average duration (simple moving average)
	if bpm.stats.TotalDeactivations == 1 {
		bpm.stats.AverageDuration = duration
	} else {
		// Weighted average favoring recent measurements
		alpha := 0.1
		bpm.stats.AverageDuration = time.Duration(
			alpha*float64(duration) + (1-alpha)*float64(bpm.stats.AverageDuration))
	}
	
	// Count active streams (streamMutex is already held by caller)
	activeCount := 0
	for _, state := range bpm.streamBackpressure {
		state.mutex.RLock()
		if state.Active {
			activeCount++
		}
		state.mutex.RUnlock()
	}
	
	bpm.stats.ActiveStreams = activeCount
	bpm.stats.LastUpdate = time.Now()
}

// triggerCallback triggers the backpressure callback for a specific stream
func (bpm *BackpressureManagerImpl) triggerCallback(streamID uint64, active bool, reason BackpressureReason) {
	bpm.callbackMutex.RLock()
	callback := bpm.callback
	bpm.callbackMutex.RUnlock()
	
	if callback != nil {
		// Run callback in goroutine to avoid blocking
		go callback(streamID, active, reason)
	}
}

// triggerGlobalCallback triggers the callback for all streams (global backpressure)
func (bpm *BackpressureManagerImpl) triggerGlobalCallback(active bool, reason BackpressureReason) {
	bpm.callbackMutex.RLock()
	callback := bpm.callback
	bpm.callbackMutex.RUnlock()
	
	if callback != nil {
		// Trigger callback for all streams
		bpm.streamMutex.RLock()
		streamIDs := make([]uint64, 0, len(bpm.streamBackpressure))
		for streamID := range bpm.streamBackpressure {
			streamIDs = append(streamIDs, streamID)
		}
		bpm.streamMutex.RUnlock()
		
		// Run callbacks in goroutines
		for _, streamID := range streamIDs {
			go callback(streamID, active, reason)
		}
	}
}

// triggerGlobalCallbackLocked triggers the callback for all streams (global backpressure)
// Note: This method assumes streamMutex is already held by the caller
func (bpm *BackpressureManagerImpl) triggerGlobalCallbackLocked(active bool, reason BackpressureReason) {
	bpm.callbackMutex.RLock()
	callback := bpm.callback
	bpm.callbackMutex.RUnlock()
	
	if callback != nil {
		// Trigger callback for all streams (streamMutex already held by caller)
		streamIDs := make([]uint64, 0, len(bpm.streamBackpressure))
		for streamID := range bpm.streamBackpressure {
			streamIDs = append(streamIDs, streamID)
		}
		
		// Run callbacks in goroutines
		for _, streamID := range streamIDs {
			go callback(streamID, active, reason)
		}
	}
}

// shouldActivateGlobalBackpressure checks if global backpressure should be activated
// Note: This method assumes streamMutex is already held by the caller
func (bpm *BackpressureManagerImpl) shouldActivateGlobalBackpressure() bool {
	activeCount := 0
	totalCount := len(bpm.streamBackpressure)
	
	for _, state := range bpm.streamBackpressure {
		state.mutex.RLock()
		if state.Active {
			activeCount++
		}
		state.mutex.RUnlock()
	}
	
	if totalCount > 0 {
		ratio := float64(activeCount) / float64(totalCount)
		return ratio >= bpm.config.GlobalThreshold
	}
	
	return false
}

// checkGlobalBackpressureActivation checks if global backpressure should be activated
// Note: This method assumes streamMutex is already held by the caller
func (bpm *BackpressureManagerImpl) checkGlobalBackpressureActivation() {
	if bpm.shouldActivateGlobalBackpressure() {
		bpm.ActivateGlobalBackpressure(BackpressureReasonSlowConsumer)
	}
}

// checkGlobalBackpressureDeactivation checks if global backpressure should be deactivated
func (bpm *BackpressureManagerImpl) checkGlobalBackpressureDeactivation() {
	bpm.streamMutex.RLock()
	activeCount := 0
	totalCount := len(bpm.streamBackpressure)
	
	for _, state := range bpm.streamBackpressure {
		state.mutex.RLock()
		if state.Active {
			activeCount++
		}
		state.mutex.RUnlock()
	}
	bpm.streamMutex.RUnlock()
	
	if totalCount > 0 {
		ratio := float64(activeCount) / float64(totalCount)
		if ratio < bpm.config.GlobalThreshold*0.8 { // Hysteresis to avoid flapping
			bpm.DeactivateGlobalBackpressure()
		}
	} else {
		// No streams left, deactivate global backpressure
		bpm.DeactivateGlobalBackpressure()
	}
}

// checkGlobalBackpressureDeactivationLocked checks if global backpressure should be deactivated
// Note: This method assumes streamMutex is already held by the caller
func (bpm *BackpressureManagerImpl) checkGlobalBackpressureDeactivationLocked() {
	activeCount := 0
	totalCount := len(bpm.streamBackpressure)
	
	for _, state := range bpm.streamBackpressure {
		state.mutex.RLock()
		if state.Active {
			activeCount++
		}
		state.mutex.RUnlock()
	}
	
	if totalCount > 0 {
		ratio := float64(activeCount) / float64(totalCount)
		if ratio < bpm.config.GlobalThreshold*0.8 { // Hysteresis to avoid flapping
			bpm.deactivateGlobalBackpressureNoCallback()
		}
	} else {
		// No streams left, deactivate global backpressure
		bpm.deactivateGlobalBackpressureNoCallback()
	}
}

// backgroundWorker runs background tasks for the backpressure manager
func (bpm *BackpressureManagerImpl) backgroundWorker() {
	defer bpm.workerWg.Done()
	
	ticker := time.NewTicker(bpm.config.AutoReleaseInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			bpm.performBackgroundTasks()
		case <-bpm.workerStop:
			return
		}
	}
}

// performBackgroundTasks performs periodic background maintenance
func (bpm *BackpressureManagerImpl) performBackgroundTasks() {
	// Update statistics rates
	bpm.updateRates()
	
	// Clean up inactive streams
	bpm.cleanupInactiveStreams()
	
	// Check for auto-release conditions
	if bpm.config.AutoReleaseEnabled {
		bpm.checkAutoRelease()
	}
}

// updateRates updates activation and deactivation rates
func (bpm *BackpressureManagerImpl) updateRates() {
	bpm.statsMutex.Lock()
	defer bpm.statsMutex.Unlock()
	
	now := time.Now()
	timeDiff := now.Sub(bpm.stats.LastUpdate)
	
	if timeDiff > 0 {
		// Calculate rates per second
		bpm.stats.ActivationRate = float64(bpm.stats.TotalActivations) / timeDiff.Seconds()
		bpm.stats.DeactivationRate = float64(bpm.stats.TotalDeactivations) / timeDiff.Seconds()
	}
	
	bpm.stats.LastUpdate = now
}

// cleanupInactiveStreams removes streams that are no longer active
func (bpm *BackpressureManagerImpl) cleanupInactiveStreams() {
	bpm.streamMutex.Lock()
	defer bpm.streamMutex.Unlock()
	
	cutoffTime := time.Now().Add(-5 * time.Minute) // Clean up streams inactive for 5 minutes
	
	var streamsToRemove []uint64
	for streamID, state := range bpm.streamBackpressure {
		state.mutex.RLock()
		if !state.Active && state.ActivatedAt.Before(cutoffTime) {
			streamsToRemove = append(streamsToRemove, streamID)
		}
		state.mutex.RUnlock()
	}
	
	for _, streamID := range streamsToRemove {
		delete(bpm.streamBackpressure, streamID)
	}
}

// checkAutoRelease checks if any backpressure can be automatically released
func (bpm *BackpressureManagerImpl) checkAutoRelease() {
	// This is a placeholder for auto-release logic
	// In a real implementation, this might check system conditions
	// and automatically release backpressure when appropriate
}

// Shutdown gracefully shuts down the backpressure manager
func (bpm *BackpressureManagerImpl) Shutdown() error {
	// Stop background worker
	close(bpm.workerStop)
	bpm.workerWg.Wait()
	
	// Deactivate all backpressure
	bpm.DeactivateGlobalBackpressure()
	
	// Get list of stream IDs to deactivate (without holding the lock)
	bpm.streamMutex.Lock()
	streamIDs := make([]uint64, 0, len(bpm.streamBackpressure))
	for streamID := range bpm.streamBackpressure {
		streamIDs = append(streamIDs, streamID)
	}
	bpm.streamMutex.Unlock()
	
	// Deactivate each stream (without holding the main lock)
	for _, streamID := range streamIDs {
		bpm.DeactivateBackpressure(streamID)
	}
	
	// Clear the map
	bpm.streamMutex.Lock()
	bpm.streamBackpressure = make(map[uint64]*StreamBackpressureState)
	bpm.streamMutex.Unlock()
	
	return nil
}

// Advanced global backpressure methods

// GetGlobalBackpressureInfo returns detailed information about global backpressure
func (bpm *BackpressureManagerImpl) GetGlobalBackpressureInfo() *GlobalBackpressureInfo {
	bpm.globalMutex.RLock()
	defer bpm.globalMutex.RUnlock()
	
	bpm.globalBackpressure.mutex.RLock()
	defer bpm.globalBackpressure.mutex.RUnlock()
	
	var duration time.Duration
	if bpm.globalBackpressure.Active {
		duration = time.Since(bpm.globalBackpressure.ActivatedAt)
	} else {
		duration = bpm.globalBackpressure.Duration
	}
	
	// Count affected streams
	bpm.streamMutex.RLock()
	affectedStreams := make([]uint64, 0)
	for streamID, state := range bpm.streamBackpressure {
		state.mutex.RLock()
		if state.Active {
			affectedStreams = append(affectedStreams, streamID)
		}
		state.mutex.RUnlock()
	}
	bpm.streamMutex.RUnlock()
	
	return &GlobalBackpressureInfo{
		Active:          bpm.globalBackpressure.Active,
		Reason:          bpm.globalBackpressure.Reason,
		ActivatedAt:     bpm.globalBackpressure.ActivatedAt,
		Duration:        duration,
		AffectedStreams: affectedStreams,
		StreamCount:     len(affectedStreams),
	}
}

// SetGlobalBackpressureThreshold sets the threshold for global backpressure activation
func (bpm *BackpressureManagerImpl) SetGlobalBackpressureThreshold(threshold float64) error {
	if threshold <= 0 || threshold > 1 {
		return fmt.Errorf("threshold must be between 0 and 1, got %f", threshold)
	}
	
	bpm.config.GlobalThreshold = threshold
	
	// Re-evaluate global backpressure with new threshold
	if bpm.IsGlobalBackpressureActive() {
		bpm.checkGlobalBackpressureDeactivation()
	} else {
		bpm.checkGlobalBackpressureActivation()
	}
	
	return nil
}

// GetGlobalBackpressureThreshold returns the current global backpressure threshold
func (bpm *BackpressureManagerImpl) GetGlobalBackpressureThreshold() float64 {
	return bpm.config.GlobalThreshold
}

// ForceGlobalBackpressure forces global backpressure activation regardless of conditions
func (bpm *BackpressureManagerImpl) ForceGlobalBackpressure(reason BackpressureReason) error {
	return bpm.ActivateGlobalBackpressure(reason)
}

// ForceGlobalBackpressureRelease forces global backpressure deactivation
func (bpm *BackpressureManagerImpl) ForceGlobalBackpressureRelease() error {
	return bpm.DeactivateGlobalBackpressure()
}

// GetGlobalBackpressureHistory returns history of global backpressure events
func (bpm *BackpressureManagerImpl) GetGlobalBackpressureHistory() *GlobalBackpressureHistory {
	bpm.statsMutex.RLock()
	defer bpm.statsMutex.RUnlock()
	
	// This is simplified - a real implementation would maintain a circular buffer
	// of historical events
	return &GlobalBackpressureHistory{
		TotalActivations:    bpm.stats.GlobalActivations,
		LastActivation:      bpm.stats.LastActivation,
		AverageDuration:     bpm.stats.AverageDuration,
		CurrentlyActive:     bpm.IsGlobalBackpressureActive(),
		ReasonBreakdown:     bpm.stats.ReasonBreakdown,
	}
}

// EscalateToGlobalBackpressure escalates stream-level backpressure to global
func (bpm *BackpressureManagerImpl) EscalateToGlobalBackpressure(reason BackpressureReason) error {
	// Check if escalation is warranted
	bpm.streamMutex.RLock()
	activeCount := 0
	totalCount := len(bpm.streamBackpressure)
	
	for _, state := range bpm.streamBackpressure {
		state.mutex.RLock()
		if state.Active {
			activeCount++
		}
		state.mutex.RUnlock()
	}
	bpm.streamMutex.RUnlock()
	
	if totalCount == 0 {
		return fmt.Errorf("no streams to escalate")
	}
	
	ratio := float64(activeCount) / float64(totalCount)
	if ratio < bpm.config.StreamThreshold {
		return fmt.Errorf("escalation threshold not met: %f < %f", ratio, bpm.config.StreamThreshold)
	}
	
	return bpm.ActivateGlobalBackpressure(reason)
}

// GetBackpressureImpact returns the impact assessment of current backpressure
func (bpm *BackpressureManagerImpl) GetBackpressureImpact() *BackpressureImpact {
	bpm.streamMutex.RLock()
	defer bpm.streamMutex.RUnlock()
	
	totalStreams := len(bpm.streamBackpressure)
	activeStreams := 0
	impactedStreams := make(map[BackpressureReason]int)
	
	for _, state := range bpm.streamBackpressure {
		state.mutex.RLock()
		if state.Active {
			activeStreams++
			impactedStreams[state.Reason]++
		}
		state.mutex.RUnlock()
	}
	
	var impactLevel BackpressureImpactLevel
	if totalStreams == 0 {
		impactLevel = BackpressureImpactNone
	} else {
		ratio := float64(activeStreams) / float64(totalStreams)
		switch {
		case ratio == 0:
			impactLevel = BackpressureImpactNone
		case ratio < 0.25:
			impactLevel = BackpressureImpactLow
		case ratio < 0.5:
			impactLevel = BackpressureImpactMedium
		case ratio < 0.75:
			impactLevel = BackpressureImpactHigh
		default:
			impactLevel = BackpressureImpactCritical
		}
	}
	
	return &BackpressureImpact{
		Level:            impactLevel,
		TotalStreams:     totalStreams,
		ActiveStreams:    activeStreams,
		ImpactRatio:      float64(activeStreams) / float64(totalStreams),
		ImpactedByReason: impactedStreams,
		GlobalActive:     bpm.IsGlobalBackpressureActive(),
		Timestamp:        time.Now(),
	}
}