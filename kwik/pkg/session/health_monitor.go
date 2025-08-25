package session

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// ConnectionHealthMonitor monitors the health of connection paths and manages
// automatic failover and recovery mechanisms
type ConnectionHealthMonitor interface {
	// StartMonitoring begins health monitoring for a session with specified paths
	StartMonitoring(sessionID string, paths []string) error
	
	// StopMonitoring stops health monitoring for a session
	StopMonitoring(sessionID string) error
	
	// GetPathHealth returns the current health status of a specific path
	GetPathHealth(sessionID string, pathID string) *PathHealth
	
	// GetAllPathHealth returns health status for all paths in a session
	GetAllPathHealth(sessionID string) map[string]*PathHealth
	
	// SetHealthThresholds configures health monitoring thresholds
	SetHealthThresholds(thresholds HealthThresholds) error
	
	// RegisterFailoverCallback registers a callback for path failover events
	RegisterFailoverCallback(callback FailoverCallback) error
	
	// SetPrimaryPath designates which path is the primary path for a session
	SetPrimaryPath(sessionID string, pathID string) error
	
	// UpdatePathMetrics updates metrics for a specific path
	UpdatePathMetrics(sessionID string, pathID string, metrics PathMetricsUpdate) error
	
	// GetHealthStats returns overall health monitoring statistics
	GetHealthStats() HealthMonitorStats
}

// PathHealth represents the current health status of a connection path
type PathHealth struct {
	PathID        string        `json:"path_id"`
	Status        PathStatus    `json:"status"`
	RTT           time.Duration `json:"rtt"`
	PacketLoss    float64       `json:"packet_loss"`
	LastActivity  time.Time     `json:"last_activity"`
	FailureCount  int           `json:"failure_count"`
	HealthScore   int           `json:"health_score"`   // 0-100 scale
	Throughput    float64       `json:"throughput"`     // Mbps
	Jitter        time.Duration `json:"jitter"`
	LastHeartbeat time.Time     `json:"last_heartbeat"`
	IsPrimary     bool          `json:"is_primary"`     // Whether this is the primary path
	
	// Internal tracking
	metrics       *PathMetrics
	lastUpdate    time.Time
	degradedSince *time.Time
}

// PathMetrics contains detailed metrics for path health calculation
type PathMetrics struct {
	// RTT tracking
	RTTHistory       []time.Duration `json:"rtt_history"`
	RTTMean          time.Duration   `json:"rtt_mean"`
	RTTVariance      time.Duration   `json:"rtt_variance"`
	
	// Packet statistics
	PacketsSent      uint64  `json:"packets_sent"`
	PacketsAcked     uint64  `json:"packets_acked"`
	PacketsLost      uint64  `json:"packets_lost"`
	PacketLossRate   float64 `json:"packet_loss_rate"`
	
	// Throughput tracking
	BytesSent        uint64    `json:"bytes_sent"`
	BytesAcked       uint64    `json:"bytes_acked"`
	ThroughputMbps   float64   `json:"throughput_mbps"`
	ThroughputHistory []float64 `json:"throughput_history"`
	
	// Heartbeat tracking
	HeartbeatsSent     uint64        `json:"heartbeats_sent"`
	HeartbeatsReceived uint64        `json:"heartbeats_received"`
	HeartbeatInterval  time.Duration `json:"heartbeat_interval"`
	ConsecutiveFails   int           `json:"consecutive_fails"`
	
	// Timing
	FirstActivity    time.Time `json:"first_activity"`
	LastActivity     time.Time `json:"last_activity"`
	LastHeartbeat    time.Time `json:"last_heartbeat"`
	LastMetricsUpdate time.Time `json:"last_metrics_update"`
}

// HealthThresholds defines the thresholds for path health assessment
type HealthThresholds struct {
	// RTT thresholds
	RTTWarningThreshold  time.Duration `json:"rtt_warning_threshold"`   // RTT above this triggers warning
	RTTCriticalThreshold time.Duration `json:"rtt_critical_threshold"`  // RTT above this triggers critical
	
	// Packet loss thresholds
	PacketLossWarningThreshold  float64 `json:"packet_loss_warning_threshold"`  // 0.0-1.0
	PacketLossCriticalThreshold float64 `json:"packet_loss_critical_threshold"` // 0.0-1.0
	
	// Health score thresholds
	HealthScoreWarningThreshold  int `json:"health_score_warning_threshold"`  // Below this triggers warning
	HealthScoreCriticalThreshold int `json:"health_score_critical_threshold"` // Below this triggers critical
	
	// Heartbeat thresholds
	HeartbeatTimeoutThreshold    time.Duration `json:"heartbeat_timeout_threshold"`    // Missing heartbeat timeout
	HeartbeatFailureThreshold    int           `json:"heartbeat_failure_threshold"`    // Consecutive failures before degraded
	HeartbeatCriticalThreshold   int           `json:"heartbeat_critical_threshold"`   // Consecutive failures before failed
	
	// Activity thresholds
	InactivityWarningThreshold   time.Duration `json:"inactivity_warning_threshold"`   // No activity warning
	InactivityCriticalThreshold  time.Duration `json:"inactivity_critical_threshold"`  // No activity critical
	
	// Adaptive heartbeat parameters
	MinHeartbeatInterval         time.Duration `json:"min_heartbeat_interval"`         // Minimum heartbeat interval
	MaxHeartbeatInterval         time.Duration `json:"max_heartbeat_interval"`         // Maximum heartbeat interval
	HeartbeatAdaptationFactor    float64       `json:"heartbeat_adaptation_factor"`    // Adaptation speed (0.0-1.0)
}

// PathMetricsUpdate contains metrics updates for a path
type PathMetricsUpdate struct {
	RTT              *time.Duration `json:"rtt,omitempty"`
	PacketSent       bool           `json:"packet_sent,omitempty"`
	PacketAcked      bool           `json:"packet_acked,omitempty"`
	PacketLost       bool           `json:"packet_lost,omitempty"`
	BytesSent        uint64         `json:"bytes_sent,omitempty"`
	BytesAcked       uint64         `json:"bytes_acked,omitempty"`
	HeartbeatSent    bool           `json:"heartbeat_sent,omitempty"`
	HeartbeatReceived bool          `json:"heartbeat_received,omitempty"`
	Activity         bool           `json:"activity,omitempty"`
}

// HealthMonitorStats contains overall health monitoring statistics
type HealthMonitorStats struct {
	ActiveSessions     int                    `json:"active_sessions"`
	TotalPaths         int                    `json:"total_paths"`
	HealthyPaths       int                    `json:"healthy_paths"`
	DegradedPaths      int                    `json:"degraded_paths"`
	FailedPaths        int                    `json:"failed_paths"`
	FailoverEvents     uint64                 `json:"failover_events"`
	RecoveryEvents     uint64                 `json:"recovery_events"`
	AverageHealthScore float64                `json:"average_health_score"`
	MonitoringUptime   time.Duration          `json:"monitoring_uptime"`
	LastUpdate         time.Time              `json:"last_update"`
	
	// Per-session statistics
	SessionStats       map[string]*SessionHealthStats `json:"session_stats"`
}

// SessionHealthStats contains health statistics for a specific session
type SessionHealthStats struct {
	SessionID          string                 `json:"session_id"`
	PathCount          int                    `json:"path_count"`
	ActivePaths        int                    `json:"active_paths"`
	PrimaryPath        string                 `json:"primary_path"`
	FailoverCount      uint64                 `json:"failover_count"`
	LastFailover       *time.Time             `json:"last_failover,omitempty"`
	AverageHealthScore float64                `json:"average_health_score"`
	MonitoringSince    time.Time              `json:"monitoring_since"`
	PathStats          map[string]*PathHealth `json:"path_stats"`
}

// FailoverCallback is called when a path failover occurs
type FailoverCallback func(sessionID string, fromPath string, toPath string, reason FailoverReason)

// FailoverReason is defined in error_recovery.go

// DefaultHealthThresholds returns sensible default health monitoring thresholds
func DefaultHealthThresholds() HealthThresholds {
	return HealthThresholds{
		// RTT thresholds
		RTTWarningThreshold:  100 * time.Millisecond,
		RTTCriticalThreshold: 500 * time.Millisecond,
		
		// Packet loss thresholds (as percentages)
		PacketLossWarningThreshold:  0.01, // 1%
		PacketLossCriticalThreshold: 0.05, // 5%
		
		// Health score thresholds
		HealthScoreWarningThreshold:  70,
		HealthScoreCriticalThreshold: 30,
		
		// Heartbeat thresholds
		HeartbeatTimeoutThreshold:    30 * time.Second,
		HeartbeatFailureThreshold:    3,
		HeartbeatCriticalThreshold:   5,
		
		// Activity thresholds
		InactivityWarningThreshold:   60 * time.Second,
		InactivityCriticalThreshold:  120 * time.Second,
		
		// Adaptive heartbeat parameters
		MinHeartbeatInterval:         5 * time.Second,
		MaxHeartbeatInterval:         60 * time.Second,
		HeartbeatAdaptationFactor:    0.1,
	}
}

// ConnectionHealthMonitorImpl implements the ConnectionHealthMonitor interface
type ConnectionHealthMonitorImpl struct {
	// Configuration
	thresholds HealthThresholds
	callbacks  []FailoverCallback
	
	// State management
	sessions map[string]*sessionHealthState
	stats    HealthMonitorStats
	
	// Synchronization
	mutex sync.RWMutex
	
	// Control
	ctx    context.Context
	cancel context.CancelFunc
	
	// Monitoring
	startTime time.Time
}

// sessionHealthState maintains health monitoring state for a session
type sessionHealthState struct {
	sessionID string
	paths     map[string]*PathHealth
	stats     *SessionHealthStats
	
	// Monitoring control
	ctx    context.Context
	cancel context.CancelFunc
	
	// Synchronization
	mutex sync.RWMutex
	
	// Heartbeat management
	heartbeatTickers map[string]*time.Ticker
	
	// Last update tracking
	lastUpdate time.Time
}

// NewConnectionHealthMonitor creates a new connection health monitor
func NewConnectionHealthMonitor(ctx context.Context) *ConnectionHealthMonitorImpl {
	monitorCtx, cancel := context.WithCancel(ctx)
	
	monitor := &ConnectionHealthMonitorImpl{
		thresholds: DefaultHealthThresholds(),
		callbacks:  make([]FailoverCallback, 0),
		sessions:   make(map[string]*sessionHealthState),
		stats: HealthMonitorStats{
			SessionStats: make(map[string]*SessionHealthStats),
			LastUpdate:   time.Now(),
		},
		ctx:       monitorCtx,
		cancel:    cancel,
		startTime: time.Now(),
	}
	
	// Start background monitoring routine
	go monitor.monitoringRoutine()
	
	return monitor
}

// monitoringRoutine runs the background health monitoring tasks
func (hm *ConnectionHealthMonitorImpl) monitoringRoutine() {
	ticker := time.NewTicker(1 * time.Second) // Update every second
	defer ticker.Stop()
	
	for {
		select {
		case <-hm.ctx.Done():
			return
		case <-ticker.C:
			hm.updateHealthMetrics()
			hm.checkFailoverConditions()
			hm.updateGlobalStats()
		}
	}
}

// updateHealthMetrics updates health metrics for all monitored sessions
func (hm *ConnectionHealthMonitorImpl) updateHealthMetrics() {
	hm.mutex.RLock()
	defer hm.mutex.RUnlock()
	
	for _, session := range hm.sessions {
		session.updatePathHealthScores(hm.thresholds)
	}
}

// checkFailoverConditions checks if any paths need failover
func (hm *ConnectionHealthMonitorImpl) checkFailoverConditions() {
	hm.mutex.RLock()
	defer hm.mutex.RUnlock()
	
	for _, session := range hm.sessions {
		session.checkFailoverConditions(hm.thresholds, hm.callbacks)
	}
}

// updateGlobalStats updates global health monitoring statistics
func (hm *ConnectionHealthMonitorImpl) updateGlobalStats() {
	hm.mutex.Lock()
	defer hm.mutex.Unlock()
	
	hm.stats.ActiveSessions = len(hm.sessions)
	hm.stats.TotalPaths = 0
	hm.stats.HealthyPaths = 0
	hm.stats.DegradedPaths = 0
	hm.stats.FailedPaths = 0
	
	var totalHealthScore float64
	var pathCount int
	
	for sessionID, session := range hm.sessions {
		session.mutex.RLock()
		
		sessionStats := &SessionHealthStats{
			SessionID:       sessionID,
			PathCount:       len(session.paths),
			MonitoringSince: session.stats.MonitoringSince,
			PathStats:       make(map[string]*PathHealth),
		}
		
		activePaths := 0
		var sessionHealthScore float64
		
		for pathID, path := range session.paths {
			hm.stats.TotalPaths++
			pathCount++
			totalHealthScore += float64(path.HealthScore)
			
			// Copy path health for stats
			pathCopy := *path
			sessionStats.PathStats[pathID] = &pathCopy
			
			switch path.Status {
			case PathStatusActive:
				hm.stats.HealthyPaths++
				activePaths++
			case PathStatusDegraded:
				hm.stats.DegradedPaths++
			case PathStatusDead:
				hm.stats.FailedPaths++
			}
			
			sessionHealthScore += float64(path.HealthScore)
		}
		
		sessionStats.ActivePaths = activePaths
		if len(session.paths) > 0 {
			sessionStats.AverageHealthScore = sessionHealthScore / float64(len(session.paths))
		}
		
		hm.stats.SessionStats[sessionID] = sessionStats
		session.mutex.RUnlock()
	}
	
	if pathCount > 0 {
		hm.stats.AverageHealthScore = totalHealthScore / float64(pathCount)
	}
	
	hm.stats.MonitoringUptime = time.Since(hm.startTime)
	hm.stats.LastUpdate = time.Now()
}
// updatePathHealthScores updates health scores for all paths in the session
func (s *sessionHealthState) updatePathHealthScores(thresholds HealthThresholds) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	
	now := time.Now()
	
	for _, path := range s.paths {
		if path.metrics == nil {
			continue
		}
		
		// Calculate health score based on multiple factors
		healthScore := 100
		
		// RTT factor
		if path.RTT > thresholds.RTTCriticalThreshold {
			healthScore -= 40
		} else if path.RTT > thresholds.RTTWarningThreshold {
			healthScore -= 20
		}
		
		// Packet loss factor
		if path.PacketLoss > thresholds.PacketLossCriticalThreshold {
			healthScore -= 30
		} else if path.PacketLoss > thresholds.PacketLossWarningThreshold {
			healthScore -= 15
		}
		
		// Heartbeat factor
		if path.metrics.ConsecutiveFails >= thresholds.HeartbeatCriticalThreshold {
			healthScore -= 50
		} else if path.metrics.ConsecutiveFails >= thresholds.HeartbeatFailureThreshold {
			healthScore -= 25
		}
		
		// Activity factor
		timeSinceActivity := now.Sub(path.LastActivity)
		if timeSinceActivity > thresholds.InactivityCriticalThreshold {
			healthScore -= 30
		} else if timeSinceActivity > thresholds.InactivityWarningThreshold {
			healthScore -= 15
		}
		
		// Ensure health score is within bounds
		if healthScore < 0 {
			healthScore = 0
		}
		
		path.HealthScore = healthScore
		path.lastUpdate = now
		
		// Update path status based on health score
		oldStatus := path.Status
		if healthScore >= thresholds.HealthScoreWarningThreshold {
			path.Status = PathStatusActive
			path.degradedSince = nil
		} else if healthScore >= thresholds.HealthScoreCriticalThreshold {
			if path.Status == PathStatusActive {
				now := time.Now()
				path.degradedSince = &now
			}
			path.Status = PathStatusDegraded
		} else {
			path.Status = PathStatusDead
		}
		
		// Update failure count on status changes
		if oldStatus == PathStatusActive && path.Status != PathStatusActive {
			path.FailureCount++
		}
	}
	
	s.lastUpdate = now
}

// checkFailoverConditions checks if any paths need failover and triggers callbacks
func (s *sessionHealthState) checkFailoverConditions(thresholds HealthThresholds, callbacks []FailoverCallback) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	
	// Find the designated primary path and best alternative
	var primaryPath *PathHealth
	var bestAlternative *PathHealth
	
	// First, find the designated primary path
	for _, path := range s.paths {
		if path.IsPrimary {
			primaryPath = path
			break
		}
	}
	
	// If no primary is designated, fall back to highest health score
	if primaryPath == nil {
		for _, path := range s.paths {
			if path.Status == PathStatusActive {
				if primaryPath == nil || path.HealthScore > primaryPath.HealthScore {
					primaryPath = path
				}
			}
		}
	}
	
	// Find the best alternative (non-primary path with highest health score)
	for _, path := range s.paths {
		if !path.IsPrimary && (path.Status == PathStatusActive || path.Status == PathStatusDegraded) {
			if bestAlternative == nil || path.HealthScore > bestAlternative.HealthScore {
				bestAlternative = path
			}
		}
	}
	
	// Check if failover is needed
	if primaryPath != nil && bestAlternative != nil {
		// Failover if primary is significantly worse than alternative
		healthDifference := bestAlternative.HealthScore - primaryPath.HealthScore
		
		var shouldFailover bool
		var reason FailoverReason
		
		if primaryPath.Status == PathStatusDead {
			shouldFailover = true
			reason = FailoverReasonPathFailed
		} else if primaryPath.HealthScore < thresholds.HealthScoreCriticalThreshold {
			shouldFailover = true
			reason = FailoverReasonHealthDegraded
		} else if primaryPath.Status == PathStatusDegraded && healthDifference > 20 {
			shouldFailover = true
			reason = FailoverReasonHealthDegraded
		} else if time.Since(primaryPath.LastActivity) > thresholds.InactivityCriticalThreshold {
			shouldFailover = true
			reason = FailoverReasonTimeout
		}
		
		if shouldFailover {
			// Trigger failover callbacks
			for _, callback := range callbacks {
				go callback(s.sessionID, primaryPath.PathID, bestAlternative.PathID, reason)
			}
			
			// Update stats
			s.stats.FailoverCount++
			now := time.Now()
			s.stats.LastFailover = &now
		}
	}
}

// newSessionHealthState creates a new session health state
func newSessionHealthState(sessionID string, paths []string) *sessionHealthState {
	ctx, cancel := context.WithCancel(context.Background())
	
	state := &sessionHealthState{
		sessionID: sessionID,
		paths:     make(map[string]*PathHealth),
		stats: &SessionHealthStats{
			SessionID:       sessionID,
			PathCount:       len(paths),
			MonitoringSince: time.Now(),
			PathStats:       make(map[string]*PathHealth),
		},
		ctx:              ctx,
		cancel:           cancel,
		heartbeatTickers: make(map[string]*time.Ticker),
		lastUpdate:       time.Now(),
	}
	
	// Initialize path health for each path
	now := time.Now()
	for _, pathID := range paths {
		pathHealth := &PathHealth{
			PathID:        pathID,
			Status:        PathStatusActive,
			RTT:           0,
			PacketLoss:    0.0,
			LastActivity:  now,
			FailureCount:  0,
			HealthScore:   100,
			Throughput:    0.0,
			Jitter:        0,
			LastHeartbeat: now,
			metrics: &PathMetrics{
				RTTHistory:         make([]time.Duration, 0, 100),
				ThroughputHistory:  make([]float64, 0, 100),
				FirstActivity:      now,
				LastActivity:       now,
				LastHeartbeat:      now,
				LastMetricsUpdate:  now,
				HeartbeatInterval:  30 * time.Second, // Default interval
			},
			lastUpdate: now,
		}
		
		state.paths[pathID] = pathHealth
	}
	
	return state
}

// StartMonitoring begins health monitoring for a session with specified paths
func (hm *ConnectionHealthMonitorImpl) StartMonitoring(sessionID string, paths []string) error {
	hm.mutex.Lock()
	defer hm.mutex.Unlock()
	
	// Check if session is already being monitored
	if _, exists := hm.sessions[sessionID]; exists {
		return nil // Already monitoring
	}
	
	// Create new session health state
	sessionState := newSessionHealthState(sessionID, paths)
	hm.sessions[sessionID] = sessionState
	
	// Start heartbeat monitoring for each path
	for _, pathID := range paths {
		hm.startHeartbeatMonitoring(sessionState, pathID)
	}
	
	return nil
}

// StopMonitoring stops health monitoring for a session
func (hm *ConnectionHealthMonitorImpl) StopMonitoring(sessionID string) error {
	hm.mutex.Lock()
	defer hm.mutex.Unlock()
	
	sessionState, exists := hm.sessions[sessionID]
	if !exists {
		return nil // Not monitoring
	}
	
	// Stop heartbeat monitoring
	sessionState.mutex.Lock()
	for pathID, ticker := range sessionState.heartbeatTickers {
		ticker.Stop()
		delete(sessionState.heartbeatTickers, pathID)
	}
	sessionState.mutex.Unlock()
	
	// Cancel session context
	sessionState.cancel()
	
	// Remove from sessions
	delete(hm.sessions, sessionID)
	delete(hm.stats.SessionStats, sessionID)
	
	return nil
}

// GetPathHealth returns the current health status of a specific path
func (hm *ConnectionHealthMonitorImpl) GetPathHealth(sessionID string, pathID string) *PathHealth {
	hm.mutex.RLock()
	defer hm.mutex.RUnlock()
	
	sessionState, exists := hm.sessions[sessionID]
	if !exists {
		return nil
	}
	
	sessionState.mutex.RLock()
	defer sessionState.mutex.RUnlock()
	
	pathHealth, exists := sessionState.paths[pathID]
	if !exists {
		return nil
	}
	
	// Return a copy to prevent external modification
	healthCopy := *pathHealth
	return &healthCopy
}

// GetAllPathHealth returns health status for all paths in a session
func (hm *ConnectionHealthMonitorImpl) GetAllPathHealth(sessionID string) map[string]*PathHealth {
	hm.mutex.RLock()
	defer hm.mutex.RUnlock()
	
	sessionState, exists := hm.sessions[sessionID]
	if !exists {
		return nil
	}
	
	sessionState.mutex.RLock()
	defer sessionState.mutex.RUnlock()
	
	result := make(map[string]*PathHealth)
	for pathID, pathHealth := range sessionState.paths {
		// Return copies to prevent external modification
		healthCopy := *pathHealth
		result[pathID] = &healthCopy
	}
	
	return result
}

// SetHealthThresholds configures health monitoring thresholds
func (hm *ConnectionHealthMonitorImpl) SetHealthThresholds(thresholds HealthThresholds) error {
	hm.mutex.Lock()
	defer hm.mutex.Unlock()
	
	hm.thresholds = thresholds
	return nil
}

// RegisterFailoverCallback registers a callback for path failover events
func (hm *ConnectionHealthMonitorImpl) RegisterFailoverCallback(callback FailoverCallback) error {
	hm.mutex.Lock()
	defer hm.mutex.Unlock()
	
	hm.callbacks = append(hm.callbacks, callback)
	return nil
}

// SetPrimaryPath designates which path is the primary path for a session
func (hm *ConnectionHealthMonitorImpl) SetPrimaryPath(sessionID string, pathID string) error {
	hm.mutex.RLock()
	sessionState, exists := hm.sessions[sessionID]
	hm.mutex.RUnlock()
	
	if !exists {
		return fmt.Errorf("session %s not found", sessionID)
	}
	
	sessionState.mutex.Lock()
	defer sessionState.mutex.Unlock()
	
	// Clear primary flag from all paths
	for _, path := range sessionState.paths {
		path.IsPrimary = false
	}
	
	// Set the specified path as primary
	if pathHealth, exists := sessionState.paths[pathID]; exists {
		pathHealth.IsPrimary = true
		sessionState.stats.PrimaryPath = pathID
		return nil
	}
	
	return fmt.Errorf("path %s not found in session %s", pathID, sessionID)
}

// UpdatePathMetrics updates metrics for a specific path
func (hm *ConnectionHealthMonitorImpl) UpdatePathMetrics(sessionID string, pathID string, update PathMetricsUpdate) error {
	hm.mutex.RLock()
	sessionState, exists := hm.sessions[sessionID]
	hm.mutex.RUnlock()
	
	if !exists {
		return nil // Session not monitored
	}
	
	sessionState.mutex.Lock()
	defer sessionState.mutex.Unlock()
	
	pathHealth, exists := sessionState.paths[pathID]
	if !exists {
		return nil // Path not found
	}
	
	now := time.Now()
	metrics := pathHealth.metrics
	
	// Update RTT
	if update.RTT != nil {
		pathHealth.RTT = *update.RTT
		metrics.RTTHistory = append(metrics.RTTHistory, *update.RTT)
		
		// Keep only last 100 RTT measurements
		if len(metrics.RTTHistory) > 100 {
			metrics.RTTHistory = metrics.RTTHistory[1:]
		}
		
		// Calculate RTT statistics
		hm.calculateRTTStats(metrics)
	}
	
	// Update packet statistics
	if update.PacketSent {
		metrics.PacketsSent++
	}
	if update.PacketAcked {
		metrics.PacketsAcked++
	}
	if update.PacketLost {
		metrics.PacketsLost++
	}
	
	// Calculate packet loss rate
	if metrics.PacketsSent > 0 {
		pathHealth.PacketLoss = float64(metrics.PacketsLost) / float64(metrics.PacketsSent)
		metrics.PacketLossRate = pathHealth.PacketLoss
	}
	
	// Update throughput
	if update.BytesSent > 0 {
		metrics.BytesSent += update.BytesSent
		// Calculate throughput over last second
		hm.calculateThroughput(pathHealth, metrics, now)
	}
	if update.BytesAcked > 0 {
		metrics.BytesAcked += update.BytesAcked
	}
	
	// Update heartbeat statistics
	if update.HeartbeatSent {
		metrics.HeartbeatsSent++
		pathHealth.LastHeartbeat = now
		metrics.LastHeartbeat = now
	}
	if update.HeartbeatReceived {
		metrics.HeartbeatsReceived++
		metrics.ConsecutiveFails = 0 // Reset failure count on successful heartbeat
	}
	
	// Update activity
	if update.Activity {
		pathHealth.LastActivity = now
		metrics.LastActivity = now
	}
	
	metrics.LastMetricsUpdate = now
	
	return nil
}

// GetHealthStats returns overall health monitoring statistics
func (hm *ConnectionHealthMonitorImpl) GetHealthStats() HealthMonitorStats {
	hm.mutex.RLock()
	defer hm.mutex.RUnlock()
	
	// Return a copy of the stats
	statsCopy := hm.stats
	statsCopy.SessionStats = make(map[string]*SessionHealthStats)
	
	for sessionID, sessionStats := range hm.stats.SessionStats {
		sessionStatsCopy := *sessionStats
		sessionStatsCopy.PathStats = make(map[string]*PathHealth)
		
		for pathID, pathHealth := range sessionStats.PathStats {
			pathHealthCopy := *pathHealth
			sessionStatsCopy.PathStats[pathID] = &pathHealthCopy
		}
		
		statsCopy.SessionStats[sessionID] = &sessionStatsCopy
	}
	
	return statsCopy
}

// startHeartbeatMonitoring starts heartbeat monitoring for a specific path
func (hm *ConnectionHealthMonitorImpl) startHeartbeatMonitoring(sessionState *sessionHealthState, pathID string) {
	sessionState.mutex.Lock()
	defer sessionState.mutex.Unlock()
	
	pathHealth := sessionState.paths[pathID]
	if pathHealth == nil {
		return
	}
	
	// Start with default heartbeat interval
	interval := pathHealth.metrics.HeartbeatInterval
	ticker := time.NewTicker(interval)
	sessionState.heartbeatTickers[pathID] = ticker
	
	go func() {
		defer ticker.Stop()
		
		for {
			select {
			case <-sessionState.ctx.Done():
				return
			case <-ticker.C:
				hm.checkHeartbeatTimeout(sessionState, pathID)
				hm.adaptHeartbeatInterval(sessionState, pathID)
			}
		}
	}()
}

// checkHeartbeatTimeout checks if a heartbeat has timed out
func (hm *ConnectionHealthMonitorImpl) checkHeartbeatTimeout(sessionState *sessionHealthState, pathID string) {
	sessionState.mutex.Lock()
	defer sessionState.mutex.Unlock()
	
	pathHealth := sessionState.paths[pathID]
	if pathHealth == nil {
		return
	}
	
	now := time.Now()
	timeSinceHeartbeat := now.Sub(pathHealth.LastHeartbeat)
	
	if timeSinceHeartbeat > hm.thresholds.HeartbeatTimeoutThreshold {
		pathHealth.metrics.ConsecutiveFails++
		
		// Update jitter calculation based on heartbeat variance
		expectedInterval := pathHealth.metrics.HeartbeatInterval
		actualInterval := timeSinceHeartbeat
		jitter := actualInterval - expectedInterval
		if jitter < 0 {
			jitter = -jitter
		}
		pathHealth.Jitter = jitter
	}
}

// adaptHeartbeatInterval adapts the heartbeat interval based on path conditions
func (hm *ConnectionHealthMonitorImpl) adaptHeartbeatInterval(sessionState *sessionHealthState, pathID string) {
	sessionState.mutex.Lock()
	defer sessionState.mutex.Unlock()
	
	pathHealth := sessionState.paths[pathID]
	if pathHealth == nil {
		return
	}
	
	metrics := pathHealth.metrics
	currentInterval := metrics.HeartbeatInterval
	
	// Adapt based on path health
	var targetInterval time.Duration
	
	if pathHealth.HealthScore >= hm.thresholds.HealthScoreWarningThreshold {
		// Healthy path - can use longer intervals
		targetInterval = hm.thresholds.MaxHeartbeatInterval
	} else if pathHealth.HealthScore >= hm.thresholds.HealthScoreCriticalThreshold {
		// Degraded path - use medium intervals
		targetInterval = (hm.thresholds.MinHeartbeatInterval + hm.thresholds.MaxHeartbeatInterval) / 2
	} else {
		// Unhealthy path - use short intervals for quick detection
		targetInterval = hm.thresholds.MinHeartbeatInterval
	}
	
	// Gradually adapt to target interval
	adaptationFactor := hm.thresholds.HeartbeatAdaptationFactor
	newInterval := time.Duration(float64(currentInterval)*(1-adaptationFactor) + float64(targetInterval)*adaptationFactor)
	
	// Ensure interval is within bounds
	if newInterval < hm.thresholds.MinHeartbeatInterval {
		newInterval = hm.thresholds.MinHeartbeatInterval
	} else if newInterval > hm.thresholds.MaxHeartbeatInterval {
		newInterval = hm.thresholds.MaxHeartbeatInterval
	}
	
	// Update interval if it changed significantly
	if newInterval != currentInterval {
		metrics.HeartbeatInterval = newInterval
		
		// Update ticker if it exists
		if ticker, exists := sessionState.heartbeatTickers[pathID]; exists {
			ticker.Stop()
			newTicker := time.NewTicker(newInterval)
			sessionState.heartbeatTickers[pathID] = newTicker
		}
	}
}

// calculateRTTStats calculates RTT statistics from history
func (hm *ConnectionHealthMonitorImpl) calculateRTTStats(metrics *PathMetrics) {
	if len(metrics.RTTHistory) == 0 {
		return
	}
	
	// Calculate mean
	var sum time.Duration
	for _, rtt := range metrics.RTTHistory {
		sum += rtt
	}
	metrics.RTTMean = sum / time.Duration(len(metrics.RTTHistory))
	
	// Calculate variance
	var varianceSum time.Duration
	for _, rtt := range metrics.RTTHistory {
		diff := rtt - metrics.RTTMean
		if diff < 0 {
			diff = -diff
		}
		varianceSum += diff
	}
	metrics.RTTVariance = varianceSum / time.Duration(len(metrics.RTTHistory))
}

// calculateThroughput calculates throughput based on recent data
func (hm *ConnectionHealthMonitorImpl) calculateThroughput(pathHealth *PathHealth, metrics *PathMetrics, now time.Time) {
	// Simple throughput calculation - could be enhanced with sliding window
	timeSinceLastUpdate := now.Sub(metrics.LastMetricsUpdate)
	if timeSinceLastUpdate > 0 {
		bytesPerSecond := float64(metrics.BytesSent) / timeSinceLastUpdate.Seconds()
		mbps := (bytesPerSecond * 8) / (1024 * 1024) // Convert to Mbps
		
		pathHealth.Throughput = mbps
		metrics.ThroughputMbps = mbps
		
		// Add to history
		metrics.ThroughputHistory = append(metrics.ThroughputHistory, mbps)
		
		// Keep only last 100 measurements
		if len(metrics.ThroughputHistory) > 100 {
			metrics.ThroughputHistory = metrics.ThroughputHistory[1:]
		}
	}
}