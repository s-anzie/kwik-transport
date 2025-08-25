package session

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// FailoverManager manages automatic path failover and recovery
type FailoverManager struct {
	// Configuration
	config FailoverConfig

	// Dependencies
	healthMonitor ConnectionHealthMonitor

	// State management
	sessions map[string]*sessionFailoverState

	// Callbacks
	callbacks []FailoverEventCallback

	// Statistics
	stats FailoverStats

	// Synchronization
	mutex sync.RWMutex

	// Control
	ctx    context.Context
	cancel context.CancelFunc
}

// sessionFailoverState tracks failover state for a session
type sessionFailoverState struct {
	sessionID   string
	primaryPath string
	backupPaths []string
	allPaths    map[string]*pathFailoverInfo

	// Failover history
	failoverHistory []FailoverEvent

	// Recovery tracking
	recoveryAttempts map[string]*recoveryAttempt

	// Synchronization
	mutex sync.RWMutex
}

// pathFailoverInfo contains failover-specific information for a path
type pathFailoverInfo struct {
	pathID           string
	isPrimary        bool
	isBackup         bool
	isAvailable      bool
	lastFailover     *time.Time
	failoverCount    int
	recoveryAttempts int
	lastRecovery     *time.Time

	// Failover criteria
	healthScore      int
	consecutiveFails int
	lastHealthCheck  time.Time
}

// recoveryAttempt tracks recovery attempts for a failed path
type recoveryAttempt struct {
	pathID        string
	startTime     time.Time
	lastAttempt   time.Time
	attemptCount  int
	nextAttempt   time.Time
	backoffFactor float64
}

// FailoverConfig contains configuration for failover management
type FailoverConfig struct {
	// Failover thresholds
	HealthScoreThreshold      int           `json:"health_score_threshold"`      // Below this triggers failover
	ConsecutiveFailsThreshold int           `json:"consecutive_fails_threshold"` // Consecutive failures before failover
	FailoverCooldown          time.Duration `json:"failover_cooldown"`           // Minimum time between failovers

	// Recovery settings
	RecoveryCheckInterval   time.Duration `json:"recovery_check_interval"`   // How often to check for recovery
	RecoveryHealthThreshold int           `json:"recovery_health_threshold"` // Health score needed for recovery
	RecoveryStabilityPeriod time.Duration `json:"recovery_stability_period"` // How long path must be stable
	MaxRecoveryAttempts     int           `json:"max_recovery_attempts"`     // Max recovery attempts per path
	RecoveryBackoffBase     time.Duration `json:"recovery_backoff_base"`     // Base backoff for recovery attempts
	RecoveryBackoffMax      time.Duration `json:"recovery_backoff_max"`      // Max backoff for recovery attempts

	// Path selection
	PreferLowerLatency     bool    `json:"prefer_lower_latency"`     // Prefer paths with lower latency
	LoadBalancingEnabled   bool    `json:"load_balancing_enabled"`   // Enable load balancing
	LoadBalancingThreshold float64 `json:"load_balancing_threshold"` // Health difference for load balancing
}

// FailoverEvent represents a failover event
type FailoverEvent struct {
	SessionID   string         `json:"session_id"`
	FromPath    string         `json:"from_path"`
	ToPath      string         `json:"to_path"`
	Reason      FailoverReason `json:"reason"`
	Timestamp   time.Time      `json:"timestamp"`
	HealthScore int            `json:"health_score"`
	Duration    time.Duration  `json:"duration"` // Time taken to complete failover
}

// FailoverStats contains failover statistics
type FailoverStats struct {
	TotalFailovers       uint64        `json:"total_failovers"`
	SuccessfulFailovers  uint64        `json:"successful_failovers"`
	FailedFailovers      uint64        `json:"failed_failovers"`
	RecoveryAttempts     uint64        `json:"recovery_attempts"`
	SuccessfulRecoveries uint64        `json:"successful_recoveries"`
	AverageFailoverTime  time.Duration `json:"average_failover_time"`
	LastFailover         *time.Time    `json:"last_failover,omitempty"`

	// Per-session stats
	SessionStats map[string]*SessionFailoverStats `json:"session_stats"`
}

// SessionFailoverStats contains failover statistics for a specific session
type SessionFailoverStats struct {
	SessionID            string          `json:"session_id"`
	PrimaryPath          string          `json:"primary_path"`
	BackupPaths          []string        `json:"backup_paths"`
	FailoverCount        uint64          `json:"failover_count"`
	LastFailover         *time.Time      `json:"last_failover,omitempty"`
	RecoveryCount        uint64          `json:"recovery_count"`
	AverageHealthScore   float64         `json:"average_health_score"`
	RecentFailoverEvents []FailoverEvent `json:"recent_failover_events"`
}

// FailoverEventCallback is called when a failover event occurs
type FailoverEventCallback func(event FailoverEvent) error

// DefaultFailoverConfig returns default failover configuration
func DefaultFailoverConfig() FailoverConfig {
	return FailoverConfig{
		HealthScoreThreshold:      30,
		ConsecutiveFailsThreshold: 3,
		FailoverCooldown:          5 * time.Second,

		RecoveryCheckInterval:   30 * time.Second,
		RecoveryHealthThreshold: 70,
		RecoveryStabilityPeriod: 60 * time.Second,
		MaxRecoveryAttempts:     5,
		RecoveryBackoffBase:     30 * time.Second,
		RecoveryBackoffMax:      10 * time.Minute,

		PreferLowerLatency:     true,
		LoadBalancingEnabled:   false,
		LoadBalancingThreshold: 20.0,
	}
}

// NewFailoverManager creates a new failover manager
func NewFailoverManager(ctx context.Context, config FailoverConfig, healthMonitor ConnectionHealthMonitor) *FailoverManager {
	managerCtx, cancel := context.WithCancel(ctx)

	fm := &FailoverManager{
		config:        config,
		healthMonitor: healthMonitor,
		sessions:      make(map[string]*sessionFailoverState),
		callbacks:     make([]FailoverEventCallback, 0),
		stats: FailoverStats{
			SessionStats: make(map[string]*SessionFailoverStats),
		},
		ctx:    managerCtx,
		cancel: cancel,
	}

	// Register with health monitor for failover callbacks
	healthMonitor.RegisterFailoverCallback(fm.onHealthMonitorFailover)

	// Start background recovery routine
	go fm.recoveryRoutine()

	return fm
}

// RegisterSession registers a session for failover management
func (fm *FailoverManager) RegisterSession(sessionID string, primaryPath string, backupPaths []string) error {
	fm.mutex.Lock()
	defer fm.mutex.Unlock()

	// Create session failover state
	allPaths := make(map[string]*pathFailoverInfo)

	// Add primary path
	allPaths[primaryPath] = &pathFailoverInfo{
		pathID:          primaryPath,
		isPrimary:       true,
		isBackup:        false,
		isAvailable:     true,
		healthScore:     100,
		lastHealthCheck: time.Now(),
	}

	// Add backup paths
	for _, pathID := range backupPaths {
		allPaths[pathID] = &pathFailoverInfo{
			pathID:          pathID,
			isPrimary:       false,
			isBackup:        true,
			isAvailable:     true,
			healthScore:     100,
			lastHealthCheck: time.Now(),
		}
	}

	sessionState := &sessionFailoverState{
		sessionID:        sessionID,
		primaryPath:      primaryPath,
		backupPaths:      backupPaths,
		allPaths:         allPaths,
		failoverHistory:  make([]FailoverEvent, 0),
		recoveryAttempts: make(map[string]*recoveryAttempt),
	}

	fm.sessions[sessionID] = sessionState

	// Initialize session stats
	fm.stats.SessionStats[sessionID] = &SessionFailoverStats{
		SessionID:            sessionID,
		PrimaryPath:          primaryPath,
		BackupPaths:          backupPaths,
		RecentFailoverEvents: make([]FailoverEvent, 0),
	}

	// Set the primary path in the health monitor
	if fm.healthMonitor != nil {
		err := fm.healthMonitor.SetPrimaryPath(sessionID, primaryPath)
		if err != nil {
			// Log the error but don't fail registration
			// The health monitor might not have the session yet
		}
	}

	return nil
}

// UnregisterSession unregisters a session from failover management
func (fm *FailoverManager) UnregisterSession(sessionID string) error {
	fm.mutex.Lock()
	defer fm.mutex.Unlock()

	delete(fm.sessions, sessionID)
	delete(fm.stats.SessionStats, sessionID)

	return nil
}

// RegisterFailoverCallback registers a callback for failover events
func (fm *FailoverManager) RegisterFailoverCallback(callback FailoverEventCallback) error {
	fm.mutex.Lock()
	defer fm.mutex.Unlock()

	fm.callbacks = append(fm.callbacks, callback)
	return nil
}

// TriggerFailover manually triggers a failover for a session
func (fm *FailoverManager) TriggerFailover(sessionID string, reason FailoverReason) error {
	fm.mutex.RLock()
	sessionState, exists := fm.sessions[sessionID]
	fm.mutex.RUnlock()

	if !exists {
		return fmt.Errorf("session %s not registered for failover", sessionID)
	}

	return fm.performFailover(sessionState, reason)
}

// GetFailoverStats returns current failover statistics
func (fm *FailoverManager) GetFailoverStats() FailoverStats {
	fm.mutex.RLock()
	defer fm.mutex.RUnlock()

	// Return a copy of the stats
	statsCopy := fm.stats
	statsCopy.SessionStats = make(map[string]*SessionFailoverStats)

	for sessionID, sessionStats := range fm.stats.SessionStats {
		sessionStatsCopy := *sessionStats
		sessionStatsCopy.BackupPaths = make([]string, len(sessionStats.BackupPaths))
		copy(sessionStatsCopy.BackupPaths, sessionStats.BackupPaths)

		sessionStatsCopy.RecentFailoverEvents = make([]FailoverEvent, len(sessionStats.RecentFailoverEvents))
		copy(sessionStatsCopy.RecentFailoverEvents, sessionStats.RecentFailoverEvents)

		statsCopy.SessionStats[sessionID] = &sessionStatsCopy
	}

	return statsCopy
}

// onHealthMonitorFailover handles failover events from the health monitor
func (fm *FailoverManager) onHealthMonitorFailover(sessionID string, fromPath string, toPath string, reason FailoverReason) {
	fm.mutex.RLock()
	sessionState, exists := fm.sessions[sessionID]
	fm.mutex.RUnlock()

	if !exists {
		return
	}

	// Update path health information
	fm.updatePathHealth(sessionState, fromPath, toPath)

	// The health monitor has already determined that failover is needed,
	// so we should perform it directly rather than re-checking conditions
	fm.performFailoverToPath(sessionState, fromPath, toPath, reason)
}

// shouldPerformFailover determines if a failover should be performed
func (fm *FailoverManager) shouldPerformFailover(sessionState *sessionFailoverState, fromPath string, reason FailoverReason) bool {
	sessionState.mutex.RLock()
	defer sessionState.mutex.RUnlock()

	pathInfo, exists := sessionState.allPaths[fromPath]
	if !exists || !pathInfo.isPrimary {
		return false // Only failover from primary path
	}

	// Check cooldown period
	if pathInfo.lastFailover != nil {
		if time.Since(*pathInfo.lastFailover) < fm.config.FailoverCooldown {
			return false // Still in cooldown
		}
	}

	// Check if there are available backup paths
	availableBackups := fm.getAvailableBackupPaths(sessionState)
	if len(availableBackups) == 0 {
		return false // No backup paths available
	}

	// Check failover conditions based on reason
	switch reason {
	case FailoverReasonHealthDegraded:
		return pathInfo.healthScore < fm.config.HealthScoreThreshold
	case FailoverReasonPathFailed:
		return pathInfo.consecutiveFails >= fm.config.ConsecutiveFailsThreshold
	case FailoverReasonTimeout:
		return true // Always failover on timeout
	case FailoverReasonManual:
		return true // Always allow manual failover
	case FailoverReasonLoadBalancing:
		return fm.config.LoadBalancingEnabled
	default:
		return false
	}
}

// performFailoverToPath performs failover to a specific path
func (fm *FailoverManager) performFailoverToPath(sessionState *sessionFailoverState, fromPath string, toPath string, reason FailoverReason) error {
	sessionState.mutex.Lock()
	defer sessionState.mutex.Unlock()

	startTime := time.Now()

	// Validate that the target path exists and is available
	toPathInfo, exists := sessionState.allPaths[toPath]
	if !exists || !toPathInfo.isAvailable {
		fm.stats.FailedFailovers++
		return fmt.Errorf("target path %s not available for session %s", toPath, sessionState.sessionID)
	}

	oldPrimary := sessionState.primaryPath

	// Update path roles
	if oldPrimaryInfo, exists := sessionState.allPaths[oldPrimary]; exists {
		oldPrimaryInfo.isPrimary = false
		oldPrimaryInfo.isBackup = true
		now := time.Now()
		oldPrimaryInfo.lastFailover = &now
		oldPrimaryInfo.failoverCount++
	}

	toPathInfo.isPrimary = true
	toPathInfo.isBackup = false

	// Update session state
	sessionState.primaryPath = toPath

	// Remove new primary from backup paths and add old primary
	newBackupPaths := make([]string, 0, len(sessionState.backupPaths))
	for _, pathID := range sessionState.backupPaths {
		if pathID != toPath {
			newBackupPaths = append(newBackupPaths, pathID)
		}
	}
	newBackupPaths = append(newBackupPaths, oldPrimary)
	sessionState.backupPaths = newBackupPaths

	// Create failover event
	event := FailoverEvent{
		SessionID:   sessionState.sessionID,
		FromPath:    fromPath,
		ToPath:      toPath,
		Reason:      reason,
		Timestamp:   startTime,
		HealthScore: sessionState.allPaths[fromPath].healthScore,
		Duration:    time.Since(startTime),
	}

	// Add to history
	sessionState.failoverHistory = append(sessionState.failoverHistory, event)

	// Keep only last 100 events
	if len(sessionState.failoverHistory) > 100 {
		sessionState.failoverHistory = sessionState.failoverHistory[1:]
	}

	// Update statistics
	fm.stats.TotalFailovers++
	fm.stats.SuccessfulFailovers++
	now := time.Now()
	fm.stats.LastFailover = &now

	// Update average failover time
	if fm.stats.TotalFailovers > 0 {
		totalTime := time.Duration(fm.stats.TotalFailovers-1)*fm.stats.AverageFailoverTime + event.Duration
		fm.stats.AverageFailoverTime = totalTime / time.Duration(fm.stats.TotalFailovers)
	} else {
		fm.stats.AverageFailoverTime = event.Duration
	}

	// Update session stats
	if sessionStats, exists := fm.stats.SessionStats[sessionState.sessionID]; exists {
		sessionStats.PrimaryPath = toPath
		sessionStats.BackupPaths = newBackupPaths
		sessionStats.FailoverCount++
		sessionStats.LastFailover = &now

		// Add to recent events
		sessionStats.RecentFailoverEvents = append(sessionStats.RecentFailoverEvents, event)
		if len(sessionStats.RecentFailoverEvents) > 10 {
			sessionStats.RecentFailoverEvents = sessionStats.RecentFailoverEvents[1:]
		}
	}

	// Notify callbacks
	for _, callback := range fm.callbacks {
		go func(cb FailoverEventCallback, evt FailoverEvent) {
			cb(evt)
		}(callback, event)
	}

	return nil
}

// performFailover performs the actual failover operation
func (fm *FailoverManager) performFailover(sessionState *sessionFailoverState, reason FailoverReason) error {
	sessionState.mutex.Lock()
	defer sessionState.mutex.Unlock()

	startTime := time.Now()

	// Find the best backup path
	bestBackup := fm.selectBestBackupPath(sessionState)
	if bestBackup == "" {
		fm.stats.FailedFailovers++
		return fmt.Errorf("no suitable backup path available for session %s", sessionState.sessionID)
	}

	oldPrimary := sessionState.primaryPath

	// Update path roles
	if oldPrimaryInfo, exists := sessionState.allPaths[oldPrimary]; exists {
		oldPrimaryInfo.isPrimary = false
		oldPrimaryInfo.isBackup = true
		now := time.Now()
		oldPrimaryInfo.lastFailover = &now
		oldPrimaryInfo.failoverCount++
	}

	if newPrimaryInfo, exists := sessionState.allPaths[bestBackup]; exists {
		newPrimaryInfo.isPrimary = true
		newPrimaryInfo.isBackup = false
	}

	// Update session state
	sessionState.primaryPath = bestBackup

	// Remove old primary from backup paths and add it
	newBackupPaths := make([]string, 0, len(sessionState.backupPaths))
	for _, pathID := range sessionState.backupPaths {
		if pathID != bestBackup {
			newBackupPaths = append(newBackupPaths, pathID)
		}
	}
	newBackupPaths = append(newBackupPaths, oldPrimary)
	sessionState.backupPaths = newBackupPaths

	// Create failover event
	event := FailoverEvent{
		SessionID:   sessionState.sessionID,
		FromPath:    oldPrimary,
		ToPath:      bestBackup,
		Reason:      reason,
		Timestamp:   startTime,
		HealthScore: sessionState.allPaths[oldPrimary].healthScore,
		Duration:    time.Since(startTime),
	}

	// Add to history
	sessionState.failoverHistory = append(sessionState.failoverHistory, event)

	// Keep only last 100 events
	if len(sessionState.failoverHistory) > 100 {
		sessionState.failoverHistory = sessionState.failoverHistory[1:]
	}

	// Update statistics
	fm.stats.TotalFailovers++
	fm.stats.SuccessfulFailovers++
	now := time.Now()
	fm.stats.LastFailover = &now

	// Update average failover time
	if fm.stats.TotalFailovers > 0 {
		totalTime := time.Duration(fm.stats.TotalFailovers-1)*fm.stats.AverageFailoverTime + event.Duration
		fm.stats.AverageFailoverTime = totalTime / time.Duration(fm.stats.TotalFailovers)
	} else {
		fm.stats.AverageFailoverTime = event.Duration
	}

	// Update session stats
	if sessionStats, exists := fm.stats.SessionStats[sessionState.sessionID]; exists {
		sessionStats.PrimaryPath = bestBackup
		sessionStats.BackupPaths = newBackupPaths
		sessionStats.FailoverCount++
		sessionStats.LastFailover = &now

		// Add to recent events
		sessionStats.RecentFailoverEvents = append(sessionStats.RecentFailoverEvents, event)
		if len(sessionStats.RecentFailoverEvents) > 10 {
			sessionStats.RecentFailoverEvents = sessionStats.RecentFailoverEvents[1:]
		}
	}

	// Notify callbacks
	for _, callback := range fm.callbacks {
		go func(cb FailoverEventCallback, evt FailoverEvent) {
			cb(evt)
		}(callback, event)
	}

	return nil
}

// selectBestBackupPath selects the best available backup path
func (fm *FailoverManager) selectBestBackupPath(sessionState *sessionFailoverState) string {
	availableBackups := fm.getAvailableBackupPaths(sessionState)
	if len(availableBackups) == 0 {
		return ""
	}

	// If only one backup, use it
	if len(availableBackups) == 1 {
		return availableBackups[0]
	}

	// Select based on configuration preferences
	bestPath := availableBackups[0]
	bestScore := fm.calculatePathScore(sessionState, bestPath)

	for _, pathID := range availableBackups[1:] {
		score := fm.calculatePathScore(sessionState, pathID)
		if score > bestScore {
			bestPath = pathID
			bestScore = score
		}
	}

	return bestPath
}

// calculatePathScore calculates a score for path selection
func (fm *FailoverManager) calculatePathScore(sessionState *sessionFailoverState, pathID string) float64 {
	pathInfo, exists := sessionState.allPaths[pathID]
	if !exists {
		return 0.0
	}

	score := float64(pathInfo.healthScore)

	// Penalize paths with recent failures
	if pathInfo.lastFailover != nil {
		timeSinceFailover := time.Since(*pathInfo.lastFailover)
		if timeSinceFailover < time.Hour {
			penalty := (time.Hour - timeSinceFailover).Seconds() / time.Hour.Seconds() * 20
			score -= penalty
		}
	}

	// Penalize paths with high failover count
	score -= float64(pathInfo.failoverCount) * 5

	// Get additional metrics from health monitor if available
	if fm.healthMonitor != nil {
		if pathHealth := fm.healthMonitor.GetPathHealth(sessionState.sessionID, pathID); pathHealth != nil {
			// Prefer lower RTT if configured
			if fm.config.PreferLowerLatency && pathHealth.RTT > 0 {
				rttPenalty := float64(pathHealth.RTT.Milliseconds()) / 100.0
				score -= rttPenalty
			}

			// Prefer higher throughput
			score += pathHealth.Throughput / 10.0

			// Penalize packet loss
			score -= pathHealth.PacketLoss * 50
		}
	}

	return score
}

// getAvailableBackupPaths returns a list of available backup paths
func (fm *FailoverManager) getAvailableBackupPaths(sessionState *sessionFailoverState) []string {
	available := make([]string, 0)

	for pathID, pathInfo := range sessionState.allPaths {
		if pathInfo.isBackup && pathInfo.isAvailable &&
			pathInfo.healthScore >= fm.config.RecoveryHealthThreshold {
			available = append(available, pathID)
		}
	}

	return available
}

// updatePathHealth updates path health information from health monitor
func (fm *FailoverManager) updatePathHealth(sessionState *sessionFailoverState, fromPath, toPath string) {
	sessionState.mutex.Lock()
	defer sessionState.mutex.Unlock()

	// Update health information for both paths
	for _, pathID := range []string{fromPath, toPath} {
		if pathInfo, exists := sessionState.allPaths[pathID]; exists {
			if fm.healthMonitor != nil {
				if pathHealth := fm.healthMonitor.GetPathHealth(sessionState.sessionID, pathID); pathHealth != nil {
					pathInfo.healthScore = pathHealth.HealthScore
					pathInfo.consecutiveFails = pathHealth.FailureCount
					pathInfo.lastHealthCheck = time.Now()
					pathInfo.isAvailable = pathHealth.Status == PathStatusActive || pathHealth.Status == PathStatusDegraded
				}
			}
		}
	}
}

// recoveryRoutine runs the background recovery process
func (fm *FailoverManager) recoveryRoutine() {
	ticker := time.NewTicker(fm.config.RecoveryCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-fm.ctx.Done():
			return
		case <-ticker.C:
			fm.checkPathRecovery()
		}
	}
}

// checkPathRecovery checks for path recovery opportunities
func (fm *FailoverManager) checkPathRecovery() {
	fm.mutex.RLock()
	sessions := make([]*sessionFailoverState, 0, len(fm.sessions))
	for _, session := range fm.sessions {
		sessions = append(sessions, session)
	}
	fm.mutex.RUnlock()

	for _, sessionState := range sessions {
		fm.attemptPathRecovery(sessionState)
	}
}

// attemptPathRecovery attempts to recover failed paths and check for better path options
func (fm *FailoverManager) attemptPathRecovery(sessionState *sessionFailoverState) {
	sessionState.mutex.Lock()
	defer sessionState.mutex.Unlock()

	now := time.Now()

	// First, check for failed path recovery
	for pathID, pathInfo := range sessionState.allPaths {
		if pathInfo.isPrimary || pathInfo.isAvailable {
			continue // Skip primary and already available paths
		}

		// Check if we should attempt recovery
		attempt, exists := sessionState.recoveryAttempts[pathID]
		if !exists {
			// First recovery attempt
			attempt = &recoveryAttempt{
				pathID:        pathID,
				startTime:     now,
				lastAttempt:   now,
				attemptCount:  0,
				nextAttempt:   now,
				backoffFactor: 1.0,
			}
			sessionState.recoveryAttempts[pathID] = attempt
		}

		// Check if it's time for next attempt
		if now.Before(attempt.nextAttempt) {
			continue
		}

		// Check if we've exceeded max attempts
		if attempt.attemptCount >= fm.config.MaxRecoveryAttempts {
			continue
		}

		// Attempt recovery
		if fm.attemptSinglePathRecovery(sessionState, pathID, attempt) {
			// Recovery successful
			pathInfo.isAvailable = true
			pathInfo.recoveryAttempts = attempt.attemptCount
			now := time.Now()
			pathInfo.lastRecovery = &now

			// Remove from recovery attempts
			delete(sessionState.recoveryAttempts, pathID)

			// Update statistics
			fm.stats.SuccessfulRecoveries++

			if sessionStats, exists := fm.stats.SessionStats[sessionState.sessionID]; exists {
				sessionStats.RecoveryCount++
			}
		} else {
			// Recovery failed, update backoff
			attempt.attemptCount++
			attempt.lastAttempt = now

			// Calculate next attempt time with exponential backoff
			backoffDuration := time.Duration(float64(fm.config.RecoveryBackoffBase) * attempt.backoffFactor)
			if backoffDuration > fm.config.RecoveryBackoffMax {
				backoffDuration = fm.config.RecoveryBackoffMax
			}

			attempt.nextAttempt = now.Add(backoffDuration)
			attempt.backoffFactor *= 2.0 // Exponential backoff

			fm.stats.RecoveryAttempts++
		}
	}

	// Second, check for path preference recovery (switching back to a better path)
	fm.checkPathPreferenceRecovery(sessionState, now)
}

// checkPathPreferenceRecovery checks if we should switch back to a better path
func (fm *FailoverManager) checkPathPreferenceRecovery(sessionState *sessionFailoverState, now time.Time) {
	// Find current primary path
	var currentPrimary *pathFailoverInfo
	var currentPrimaryID string
	for pathID, pathInfo := range sessionState.allPaths {
		if pathInfo.isPrimary {
			currentPrimary = pathInfo
			currentPrimaryID = pathID
			break
		}
	}

	if currentPrimary == nil {
		return // No primary path found
	}

	// Get current primary health
	var currentPrimaryHealth *PathHealth
	if fm.healthMonitor != nil {
		currentPrimaryHealth = fm.healthMonitor.GetPathHealth(sessionState.sessionID, currentPrimaryID)
	}

	// Check if any backup path is significantly better than current primary
	for pathID, pathInfo := range sessionState.allPaths {
		if pathInfo.isPrimary || !pathInfo.isAvailable {
			continue // Skip primary and unavailable paths
		}

		// Get backup path health
		var backupPathHealth *PathHealth
		if fm.healthMonitor != nil {
			backupPathHealth = fm.healthMonitor.GetPathHealth(sessionState.sessionID, pathID)
		}

		if backupPathHealth == nil || currentPrimaryHealth == nil {
			continue
		}

		// Check if backup path is significantly better or if it's a previously failed path that has recovered
		healthDifference := backupPathHealth.HealthScore - currentPrimaryHealth.HealthScore

		// Consider recovery if:
		// 1. Backup path health is significantly better (>20 points), OR
		// 2. Backup path has recovered to good health (>=recovery threshold) and had a recent failover
		// 3. Enough time has passed since last failover (cooldown)
		hasRecentFailover := pathInfo.lastFailover != nil && now.Sub(*pathInfo.lastFailover) < 5*time.Minute
		shouldAttemptRecovery := (healthDifference > 20) ||
			(backupPathHealth.HealthScore >= fm.config.RecoveryHealthThreshold && hasRecentFailover)

		if shouldAttemptRecovery &&
			backupPathHealth.HealthScore >= fm.config.RecoveryHealthThreshold &&
			(pathInfo.lastFailover == nil || now.Sub(*pathInfo.lastFailover) > fm.config.FailoverCooldown) {

			// Attempt recovery by switching back to the better path
			fm.stats.RecoveryAttempts++

			// Perform the recovery failover
			err := fm.performFailoverToPath(sessionState, currentPrimaryID, pathID, FailoverReasonHealthDegraded)
			if err == nil {
				// Recovery successful
				fm.stats.SuccessfulRecoveries++

				if sessionStats, exists := fm.stats.SessionStats[sessionState.sessionID]; exists {
					sessionStats.RecoveryCount++
				}

				return // Only recover one path at a time
			}
		}
	}
}

// attemptSinglePathRecovery attempts to recover a single path
func (fm *FailoverManager) attemptSinglePathRecovery(sessionState *sessionFailoverState, pathID string, attempt *recoveryAttempt) bool {
	// Check current health from health monitor
	if fm.healthMonitor != nil {
		pathHealth := fm.healthMonitor.GetPathHealth(sessionState.sessionID, pathID)
		if pathHealth == nil {
			return false
		}

		// Check if path meets recovery criteria
		if pathHealth.HealthScore < fm.config.RecoveryHealthThreshold {
			return false
		}

		if pathHealth.Status != PathStatusActive && pathHealth.Status != PathStatusDegraded {
			return false
		}

		// Check stability period
		if time.Since(pathHealth.LastActivity) > fm.config.RecoveryStabilityPeriod {
			return false
		}

		// Path appears to be recovered
		return true
	}

	return false
}

// Shutdown gracefully shuts down the failover manager
func (fm *FailoverManager) Shutdown() error {
	fm.cancel()
	return nil
}
