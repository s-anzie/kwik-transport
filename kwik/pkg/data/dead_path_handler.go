package data

import (
	"fmt"
	"sync"
	"time"

	"kwik/internal/utils"
	"kwik/pkg/transport"
	datapb "kwik/proto/data"
)

// DeadPathHandler manages dead path detection and handling for data flow management
// Implements Requirement 10.3: logic to stop using dead paths for new streams
type DeadPathHandler struct {
	// Path management
	pathManager transport.PathManager

	// Dead path tracking
	deadPaths      map[string]*DeadPathInfo
	deadPathsMutex sync.RWMutex

	// Stream routing prevention
	streamRouter *StreamRouter

	// Configuration
	config *DeadPathConfig

	// Background processing
	cleanupTicker *time.Ticker
	stopChan      chan struct{}
	wg            sync.WaitGroup

	// Statistics
	stats      *DeadPathStats
	statsMutex sync.RWMutex
}

// DeadPathInfo contains information about a dead path
type DeadPathInfo struct {
	PathID          string
	DeadSince       time.Time
	Reason          string
	LastError       error
	StreamsAffected []uint64
	BytesLost       uint64
	FramesLost      uint64
	CleanupAttempts int
	IsCleanedUp     bool
}

// StreamRouter prevents routing to dead paths
type StreamRouter struct {
	deadPaths   map[string]bool
	activePaths map[string]bool
	mutex       sync.RWMutex
}

// DeadPathConfig contains configuration for dead path handling
type DeadPathConfig struct {
	// Detection parameters
	DeadPathTimeout        time.Duration
	MaxConsecutiveFailures int
	HealthCheckInterval    time.Duration

	// Cleanup parameters
	CleanupInterval      time.Duration
	MaxCleanupRetries    int
	ResourceCleanupDelay time.Duration

	// Stream handling
	PreventNewStreams      bool
	MigrateActiveStreams   bool
	StreamMigrationTimeout time.Duration

	// Notification settings
	NotifyOnDeadPath    bool
	NotificationRetries int
}

// DeadPathStats contains statistics about dead path handling
type DeadPathStats struct {
	TotalDeadPaths           uint64
	CurrentDeadPaths         int
	StreamsAffected          uint64
	BytesLost                uint64
	FramesLost               uint64
	CleanupOperations        uint64
	PreventedStreamCreations uint64
	MigratedStreams          uint64
}

// NewDeadPathHandler creates a new dead path handler
func NewDeadPathHandler(pathManager transport.PathManager, config *DeadPathConfig) *DeadPathHandler {
	if config == nil {
		config = DefaultDeadPathConfig()
	}

	dph := &DeadPathHandler{
		pathManager:  pathManager,
		deadPaths:    make(map[string]*DeadPathInfo),
		streamRouter: NewStreamRouter(),
		config:       config,
		stopChan:     make(chan struct{}),
		stats:        &DeadPathStats{},
	}

	// Set up path status notification handler
	pathManager.SetPathStatusNotificationHandler(dph)

	// Mark existing active paths as active in the stream router
	activePaths := pathManager.GetActivePaths()
	for _, path := range activePaths {
		dph.streamRouter.MarkPathActive(path.ID())
	}

	// Start background cleanup if configured
	if config.CleanupInterval > 0 {
		dph.startCleanupRoutine()
	}

	return dph
}

// DefaultDeadPathConfig returns default configuration
func DefaultDeadPathConfig() *DeadPathConfig {
	return &DeadPathConfig{
		DeadPathTimeout:        30 * time.Second,
		MaxConsecutiveFailures: 3,
		HealthCheckInterval:    10 * time.Second,
		CleanupInterval:        60 * time.Second,
		MaxCleanupRetries:      3,
		ResourceCleanupDelay:   5 * time.Second,
		PreventNewStreams:      true,
		MigrateActiveStreams:   true,
		StreamMigrationTimeout: 30 * time.Second,
		NotifyOnDeadPath:       true,
		NotificationRetries:    3,
	}
}

// OnPathStatusChanged handles path status change notifications
// Implements transport.PathStatusNotificationHandler interface
func (dph *DeadPathHandler) OnPathStatusChanged(pathID string, oldStatus, newStatus transport.PathState, metrics *transport.PathHealthMetrics) {
	if newStatus == transport.PathStateDead {
		dph.handlePathDeath(pathID, "status_change", metrics)
	} else if oldStatus == transport.PathStateDead && newStatus == transport.PathStateActive {
		dph.handlePathRecovery(pathID, metrics)
	}
}

// OnPathFailureDetected handles path failure detection notifications
func (dph *DeadPathHandler) OnPathFailureDetected(pathID string, reason string, metrics *transport.PathHealthMetrics) {
	dph.handlePathDeath(pathID, reason, metrics)
}

// OnPathRecovered handles path recovery notifications
func (dph *DeadPathHandler) OnPathRecovered(pathID string, metrics *transport.PathHealthMetrics) {
	dph.handlePathRecovery(pathID, metrics)
}

// handlePathDeath handles when a path is detected as dead
func (dph *DeadPathHandler) handlePathDeath(pathID string, reason string, metrics *transport.PathHealthMetrics) {
	dph.deadPathsMutex.Lock()
	defer dph.deadPathsMutex.Unlock()

	// Check if already marked as dead
	if _, exists := dph.deadPaths[pathID]; exists {
		return
	}

	// Create dead path info
	deadPathInfo := &DeadPathInfo{
		PathID:          pathID,
		DeadSince:       time.Now(),
		Reason:          reason,
		StreamsAffected: make([]uint64, 0),
		BytesLost:       0,
		FramesLost:      0,
		CleanupAttempts: 0,
		IsCleanedUp:     false,
	}

	if metrics != nil && metrics.LastError != nil {
		deadPathInfo.LastError = metrics.LastError
	}

	dph.deadPaths[pathID] = deadPathInfo

	// Update stream router to prevent new streams
	dph.streamRouter.MarkPathDead(pathID)

	// Update statistics
	dph.statsMutex.Lock()
	dph.stats.TotalDeadPaths++
	dph.stats.CurrentDeadPaths++
	dph.statsMutex.Unlock()

	// Handle affected streams
	if dph.config.MigrateActiveStreams {
		go dph.migrateStreamsFromDeadPath(pathID)
	}

	// Schedule cleanup
	go dph.schedulePathCleanup(pathID)
}

// handlePathRecovery handles when a dead path recovers
func (dph *DeadPathHandler) handlePathRecovery(pathID string, metrics *transport.PathHealthMetrics) {
	dph.deadPathsMutex.Lock()
	defer dph.deadPathsMutex.Unlock()

	// Remove from dead paths
	if _, exists := dph.deadPaths[pathID]; exists {
		delete(dph.deadPaths, pathID)

		// Update stream router to allow new streams
		dph.streamRouter.MarkPathActive(pathID)

		// Update statistics
		dph.statsMutex.Lock()
		dph.stats.CurrentDeadPaths--
		dph.statsMutex.Unlock()
	}
}

// CanUsePathForNewStream checks if a path can be used for new streams
// Implements Requirement 10.3: stop using dead paths for new streams
func (dph *DeadPathHandler) CanUsePathForNewStream(pathID string) bool {
	if !dph.config.PreventNewStreams {
		return true // Feature disabled
	}

	// If path is explicitly marked as dead, don't use it
	if dph.streamRouter.IsPathDead(pathID) {
		return false
	}

	// If path is explicitly marked as active, use it
	if dph.streamRouter.IsPathActive(pathID) {
		return true
	}

	// For paths not explicitly marked, check if they exist in path manager and are active
	path := dph.pathManager.GetPath(pathID)
	if path != nil && path.IsActive() {
		// Auto-mark as active for future checks
		dph.streamRouter.MarkPathActive(pathID)
		return true
	}

	return false
}

// GetAlternativePath returns an alternative active path for stream creation
func (dph *DeadPathHandler) GetAlternativePath(excludePathID string) (string, error) {
	activePaths := dph.pathManager.GetActivePaths()

	for _, path := range activePaths {
		pathID := path.ID()
		if pathID != excludePathID && dph.streamRouter.IsPathActive(pathID) {
			return pathID, nil
		}
	}

	return "", utils.NewKwikError(utils.ErrConnectionLost,
		"no alternative active paths available", nil)
}

// ProcessIncomingFrame processes incoming frames and handles dead path scenarios
func (dph *DeadPathHandler) ProcessIncomingFrame(frame *datapb.DataFrame) error {
	pathID := frame.PathId

	// Check if frame is from a dead path
	if dph.streamRouter.IsPathDead(pathID) {
		// Update statistics for frames from dead paths
		dph.statsMutex.Lock()
		dph.stats.FramesLost++
		dph.stats.BytesLost += uint64(len(frame.Data))
		dph.statsMutex.Unlock()

		// Record affected stream
		dph.recordAffectedStream(pathID, frame.LogicalStreamId)

		return utils.NewKwikError(utils.ErrConnectionLost,
			fmt.Sprintf("received frame from dead path %s", pathID), nil)
	}

	return nil // Frame is from active path, allow processing
}

// recordAffectedStream records a stream affected by a dead path
func (dph *DeadPathHandler) recordAffectedStream(pathID string, streamID uint64) {
	dph.deadPathsMutex.Lock()
	defer dph.deadPathsMutex.Unlock()

	if deadPathInfo, exists := dph.deadPaths[pathID]; exists {
		// Check if stream is already recorded
		for _, affectedStreamID := range deadPathInfo.StreamsAffected {
			if affectedStreamID == streamID {
				return // Already recorded
			}
		}

		// Add to affected streams
		deadPathInfo.StreamsAffected = append(deadPathInfo.StreamsAffected, streamID)

		// Update statistics
		dph.statsMutex.Lock()
		dph.stats.StreamsAffected++
		dph.statsMutex.Unlock()
	}
}

// migrateStreamsFromDeadPath migrates active streams from a dead path
func (dph *DeadPathHandler) migrateStreamsFromDeadPath(pathID string) {
	if !dph.config.MigrateActiveStreams {
		return
	}

	// Get alternative path
	alternativePath, err := dph.GetAlternativePath(pathID)
	if err != nil {
		// No alternative path available, streams will be lost
		return
	}

	dph.deadPathsMutex.RLock()
	deadPathInfo, exists := dph.deadPaths[pathID]
	dph.deadPathsMutex.RUnlock()

	if !exists {
		return
	}

	// Migrate affected streams
	for _, streamID := range deadPathInfo.StreamsAffected {
		err := dph.migrateStream(streamID, pathID, alternativePath)
		if err == nil {
			dph.statsMutex.Lock()
			dph.stats.MigratedStreams++
			dph.statsMutex.Unlock()
		}
	}
}

// migrateStream migrates a single stream from dead path to alternative path
func (dph *DeadPathHandler) migrateStream(streamID uint64, fromPathID, toPathID string) error {
	// TODO: Implement actual stream migration logic
	// This would involve:
	// 1. Creating new stream on alternative path
	// 2. Transferring stream state and buffered data
	// 3. Updating stream routing tables
	// 4. Notifying applications of stream migration

	// For now, just log the migration attempt
	return nil
}

// schedulePathCleanup schedules cleanup of resources for a dead path
func (dph *DeadPathHandler) schedulePathCleanup(pathID string) {
	// Wait for cleanup delay
	time.Sleep(dph.config.ResourceCleanupDelay)

	// Perform cleanup
	dph.cleanupDeadPath(pathID)
}

// cleanupDeadPath cleans up resources associated with a dead path
func (dph *DeadPathHandler) cleanupDeadPath(pathID string) {
	dph.deadPathsMutex.Lock()
	deadPathInfo, exists := dph.deadPaths[pathID]
	if !exists {
		dph.deadPathsMutex.Unlock()
		return
	}

	// Increment cleanup attempts
	deadPathInfo.CleanupAttempts++
	maxRetries := dph.config.MaxCleanupRetries
	dph.deadPathsMutex.Unlock()

	// Check if max retries exceeded
	if deadPathInfo.CleanupAttempts > maxRetries {
		return
	}

	// Perform cleanup operations
	err := dph.performPathCleanup(pathID)
	if err != nil {
		// Retry cleanup later
		go func() {
			time.Sleep(dph.config.CleanupInterval)
			dph.cleanupDeadPath(pathID)
		}()
		return
	}

	// Mark as cleaned up
	dph.deadPathsMutex.Lock()
	if deadPathInfo, exists := dph.deadPaths[pathID]; exists {
		deadPathInfo.IsCleanedUp = true
	}
	dph.deadPathsMutex.Unlock()

	// Update statistics
	dph.statsMutex.Lock()
	dph.stats.CleanupOperations++
	dph.statsMutex.Unlock()
}

// performPathCleanup performs the actual cleanup operations
func (dph *DeadPathHandler) performPathCleanup(pathID string) error {
	// Remove path from path manager
	err := dph.pathManager.RemovePath(pathID)
	if err != nil {
		return err
	}

	// Clean up any remaining resources
	// TODO: Implement additional cleanup operations:
	// 1. Close any remaining streams
	// 2. Clean up buffered data
	// 3. Update routing tables
	// 4. Notify dependent components

	return nil
}

// startCleanupRoutine starts the background cleanup routine
func (dph *DeadPathHandler) startCleanupRoutine() {
	dph.cleanupTicker = time.NewTicker(dph.config.CleanupInterval)

	dph.wg.Add(1)
	go func() {
		defer dph.wg.Done()

		for {
			select {
			case <-dph.cleanupTicker.C:
				dph.performPeriodicCleanup()
			case <-dph.stopChan:
				return
			}
		}
	}()
}

// performPeriodicCleanup performs periodic cleanup of dead paths
func (dph *DeadPathHandler) performPeriodicCleanup() {
	dph.deadPathsMutex.RLock()
	deadPaths := make([]string, 0, len(dph.deadPaths))
	for pathID, deadPathInfo := range dph.deadPaths {
		if !deadPathInfo.IsCleanedUp {
			deadPaths = append(deadPaths, pathID)
		}
	}
	dph.deadPathsMutex.RUnlock()

	// Clean up each dead path
	for _, pathID := range deadPaths {
		dph.cleanupDeadPath(pathID)
	}

	// Remove old cleaned up paths
	dph.removeOldCleanedUpPaths()
}

// removeOldCleanedUpPaths removes old cleaned up paths from memory
func (dph *DeadPathHandler) removeOldCleanedUpPaths() {
	dph.deadPathsMutex.Lock()
	defer dph.deadPathsMutex.Unlock()

	now := time.Now()
	maxAge := 24 * time.Hour // Keep cleaned up paths for 24 hours for debugging

	for pathID, deadPathInfo := range dph.deadPaths {
		if deadPathInfo.IsCleanedUp && now.Sub(deadPathInfo.DeadSince) > maxAge {
			delete(dph.deadPaths, pathID)
		}
	}
}

// GetDeadPathStats returns current dead path statistics
func (dph *DeadPathHandler) GetDeadPathStats() *DeadPathStats {
	dph.statsMutex.RLock()
	defer dph.statsMutex.RUnlock()

	// Return a copy
	return &DeadPathStats{
		TotalDeadPaths:           dph.stats.TotalDeadPaths,
		CurrentDeadPaths:         dph.stats.CurrentDeadPaths,
		StreamsAffected:          dph.stats.StreamsAffected,
		BytesLost:                dph.stats.BytesLost,
		FramesLost:               dph.stats.FramesLost,
		CleanupOperations:        dph.stats.CleanupOperations,
		PreventedStreamCreations: dph.stats.PreventedStreamCreations,
		MigratedStreams:          dph.stats.MigratedStreams,
	}
}

// GetDeadPathInfo returns information about a specific dead path
func (dph *DeadPathHandler) GetDeadPathInfo(pathID string) (*DeadPathInfo, error) {
	dph.deadPathsMutex.RLock()
	defer dph.deadPathsMutex.RUnlock()

	deadPathInfo, exists := dph.deadPaths[pathID]
	if !exists {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			fmt.Sprintf("dead path %s not found", pathID), nil)
	}

	// Return a copy
	infoCopy := *deadPathInfo
	infoCopy.StreamsAffected = make([]uint64, len(deadPathInfo.StreamsAffected))
	copy(infoCopy.StreamsAffected, deadPathInfo.StreamsAffected)

	return &infoCopy, nil
}

// GetAllDeadPaths returns information about all dead paths
func (dph *DeadPathHandler) GetAllDeadPaths() []*DeadPathInfo {
	dph.deadPathsMutex.RLock()
	defer dph.deadPathsMutex.RUnlock()

	deadPaths := make([]*DeadPathInfo, 0, len(dph.deadPaths))
	for _, deadPathInfo := range dph.deadPaths {
		// Create copy
		infoCopy := *deadPathInfo
		infoCopy.StreamsAffected = make([]uint64, len(deadPathInfo.StreamsAffected))
		copy(infoCopy.StreamsAffected, deadPathInfo.StreamsAffected)

		deadPaths = append(deadPaths, &infoCopy)
	}

	return deadPaths
}

// Close shuts down the dead path handler
func (dph *DeadPathHandler) Close() error {
	// Stop cleanup routine
	if dph.cleanupTicker != nil {
		dph.cleanupTicker.Stop()
	}

	close(dph.stopChan)
	dph.wg.Wait()

	// Clean up remaining dead paths
	dph.deadPathsMutex.Lock()
	for pathID := range dph.deadPaths {
		dph.performPathCleanup(pathID)
	}
	dph.deadPaths = make(map[string]*DeadPathInfo)
	dph.deadPathsMutex.Unlock()

	return nil
}

// NewStreamRouter creates a new stream router
func NewStreamRouter() *StreamRouter {
	return &StreamRouter{
		deadPaths:   make(map[string]bool),
		activePaths: make(map[string]bool),
	}
}

// MarkPathDead marks a path as dead in the router
func (sr *StreamRouter) MarkPathDead(pathID string) {
	sr.mutex.Lock()
	defer sr.mutex.Unlock()

	sr.deadPaths[pathID] = true
	delete(sr.activePaths, pathID)
}

// MarkPathActive marks a path as active in the router
func (sr *StreamRouter) MarkPathActive(pathID string) {
	sr.mutex.Lock()
	defer sr.mutex.Unlock()

	sr.activePaths[pathID] = true
	delete(sr.deadPaths, pathID)
}

// IsPathDead checks if a path is marked as dead
func (sr *StreamRouter) IsPathDead(pathID string) bool {
	sr.mutex.RLock()
	defer sr.mutex.RUnlock()

	return sr.deadPaths[pathID]
}

// IsPathActive checks if a path is marked as active
func (sr *StreamRouter) IsPathActive(pathID string) bool {
	sr.mutex.RLock()
	defer sr.mutex.RUnlock()

	// Path is active if it's explicitly marked as active and not dead
	return sr.activePaths[pathID] && !sr.deadPaths[pathID]
}

// GetActivePaths returns all active path IDs
func (sr *StreamRouter) GetActivePaths() []string {
	sr.mutex.RLock()
	defer sr.mutex.RUnlock()

	activePaths := make([]string, 0, len(sr.activePaths))
	for pathID := range sr.activePaths {
		if !sr.deadPaths[pathID] {
			activePaths = append(activePaths, pathID)
		}
	}

	return activePaths
}

// GetDeadPaths returns all dead path IDs
func (sr *StreamRouter) GetDeadPaths() []string {
	sr.mutex.RLock()
	defer sr.mutex.RUnlock()

	deadPaths := make([]string, 0, len(sr.deadPaths))
	for pathID := range sr.deadPaths {
		deadPaths = append(deadPaths, pathID)
	}

	return deadPaths
}
