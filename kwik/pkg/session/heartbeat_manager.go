package session

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// HeartbeatManager manages adaptive heartbeat intervals and failure detection
type HeartbeatManager struct {
	// Configuration
	minInterval       time.Duration
	maxInterval       time.Duration
	adaptationFactor  float64
	timeoutMultiplier float64
	
	// State tracking
	sessions map[string]*sessionHeartbeatState
	
	// Synchronization
	mutex sync.RWMutex
	
	// Control
	ctx    context.Context
	cancel context.CancelFunc
}

// sessionHeartbeatState tracks heartbeat state for a session
type sessionHeartbeatState struct {
	sessionID string
	paths     map[string]*pathHeartbeatState
	
	// Synchronization
	mutex sync.RWMutex
}

// pathHeartbeatState tracks heartbeat state for a specific path
type pathHeartbeatState struct {
	pathID            string
	currentInterval   time.Duration
	targetInterval    time.Duration
	lastSent          time.Time
	lastReceived      time.Time
	consecutiveFails  int
	totalSent         uint64
	totalReceived     uint64
	responseTimes     []time.Duration
	avgResponseTime   time.Duration
	
	// Adaptive parameters
	rttHistory        []time.Duration
	lossHistory       []bool
	healthScore       int
	
	// Authentication state
	isAuthenticated   bool
	authCheckCallback AuthenticationCheckCallback
	
	// Control
	ticker            *time.Ticker
	stopChan          chan struct{}
	
	// Callbacks
	sendCallback      HeartbeatSendCallback
	timeoutCallback   HeartbeatTimeoutCallback
	
	// Synchronization
	mutex sync.RWMutex
}

// HeartbeatSendCallback is called when a heartbeat should be sent
type HeartbeatSendCallback func(sessionID, pathID string) error

// HeartbeatTimeoutCallback is called when a heartbeat times out
type HeartbeatTimeoutCallback func(sessionID, pathID string, consecutiveFails int)

// AuthenticationCheckCallback is called to check if a path is authenticated
type AuthenticationCheckCallback func(sessionID, pathID string) bool

// HeartbeatConfig contains configuration for heartbeat management
type HeartbeatConfig struct {
	MinInterval       time.Duration `json:"min_interval"`
	MaxInterval       time.Duration `json:"max_interval"`
	AdaptationFactor  float64       `json:"adaptation_factor"`
	TimeoutMultiplier float64       `json:"timeout_multiplier"`
}

// HeartbeatStats contains heartbeat statistics for a path
type HeartbeatStats struct {
	PathID            string        `json:"path_id"`
	CurrentInterval   time.Duration `json:"current_interval"`
	TargetInterval    time.Duration `json:"target_interval"`
	LastSent          time.Time     `json:"last_sent"`
	LastReceived      time.Time     `json:"last_received"`
	ConsecutiveFails  int           `json:"consecutive_fails"`
	TotalSent         uint64        `json:"total_sent"`
	TotalReceived     uint64        `json:"total_received"`
	SuccessRate       float64       `json:"success_rate"`
	AvgResponseTime   time.Duration `json:"avg_response_time"`
	IsActive          bool          `json:"is_active"`
}

// DefaultHeartbeatConfig returns default heartbeat configuration
func DefaultHeartbeatConfig() HeartbeatConfig {
	return HeartbeatConfig{
		MinInterval:       5 * time.Second,
		MaxInterval:       60 * time.Second,
		AdaptationFactor:  0.1,
		TimeoutMultiplier: 3.0,
	}
}

// NewHeartbeatManager creates a new heartbeat manager
func NewHeartbeatManager(ctx context.Context, config HeartbeatConfig) *HeartbeatManager {
	managerCtx, cancel := context.WithCancel(ctx)
	
	return &HeartbeatManager{
		minInterval:       config.MinInterval,
		maxInterval:       config.MaxInterval,
		adaptationFactor:  config.AdaptationFactor,
		timeoutMultiplier: config.TimeoutMultiplier,
		sessions:          make(map[string]*sessionHeartbeatState),
		ctx:               managerCtx,
		cancel:            cancel,
	}
}

// StartHeartbeat starts heartbeat monitoring for a path
func (hm *HeartbeatManager) StartHeartbeat(sessionID, pathID string, 
	sendCallback HeartbeatSendCallback, timeoutCallback HeartbeatTimeoutCallback) error {
	return hm.StartHeartbeatWithAuth(sessionID, pathID, sendCallback, timeoutCallback, nil)
}

// StartHeartbeatWithAuth starts heartbeat monitoring for a path with authentication awareness
func (hm *HeartbeatManager) StartHeartbeatWithAuth(sessionID, pathID string, 
	sendCallback HeartbeatSendCallback, timeoutCallback HeartbeatTimeoutCallback, 
	authCheckCallback AuthenticationCheckCallback) error {
	
	hm.mutex.Lock()
	defer hm.mutex.Unlock()
	
	// Get or create session state
	sessionState, exists := hm.sessions[sessionID]
	if !exists {
		sessionState = &sessionHeartbeatState{
			sessionID: sessionID,
			paths:     make(map[string]*pathHeartbeatState),
		}
		hm.sessions[sessionID] = sessionState
	}
	
	sessionState.mutex.Lock()
	defer sessionState.mutex.Unlock()
	
	// Check if path already exists
	if _, exists := sessionState.paths[pathID]; exists {
		return nil // Already monitoring
	}
	
	// Create path heartbeat state
	initialInterval := (hm.minInterval + hm.maxInterval) / 2 // Start with middle interval
	now := time.Now()
	pathState := &pathHeartbeatState{
		pathID:            pathID,
		currentInterval:   initialInterval,
		targetInterval:    initialInterval,
		lastReceived:      now, // Initialize to current time to avoid immediate timeout
		responseTimes:     make([]time.Duration, 0, 100),
		rttHistory:        make([]time.Duration, 0, 50),
		lossHistory:       make([]bool, 0, 50),
		healthScore:       100,
		isAuthenticated:   authCheckCallback == nil, // If no auth callback, assume authenticated
		authCheckCallback: authCheckCallback,
		stopChan:          make(chan struct{}),
		sendCallback:      sendCallback,
		timeoutCallback:   timeoutCallback,
	}
	
	sessionState.paths[pathID] = pathState
	
	// Start heartbeat routine
	go hm.heartbeatRoutine(sessionID, pathID, pathState)
	
	return nil
}

// StopHeartbeat stops heartbeat monitoring for a path
func (hm *HeartbeatManager) StopHeartbeat(sessionID, pathID string) error {
	hm.mutex.Lock()
	defer hm.mutex.Unlock()
	
	sessionState, exists := hm.sessions[sessionID]
	if !exists {
		return nil
	}
	
	sessionState.mutex.Lock()
	defer sessionState.mutex.Unlock()
	
	pathState, exists := sessionState.paths[pathID]
	if !exists {
		return nil
	}
	
	// Stop the heartbeat routine
	close(pathState.stopChan)
	if pathState.ticker != nil {
		pathState.ticker.Stop()
	}
	
	// Remove from session
	delete(sessionState.paths, pathID)
	
	// Remove session if no paths left
	if len(sessionState.paths) == 0 {
		delete(hm.sessions, sessionID)
	}
	
	return nil
}

// StopSession stops heartbeat monitoring for all paths in a session
func (hm *HeartbeatManager) StopSession(sessionID string) error {
	hm.mutex.Lock()
	sessionState, exists := hm.sessions[sessionID]
	if !exists {
		hm.mutex.Unlock()
		return nil
	}
	
	sessionState.mutex.Lock()
	pathStates := make([]*pathHeartbeatState, 0, len(sessionState.paths))
	for _, pathState := range sessionState.paths {
		pathStates = append(pathStates, pathState)
	}
	
	// Clear the paths map
	sessionState.paths = make(map[string]*pathHeartbeatState)
	sessionState.mutex.Unlock()
	
	// Remove session from manager
	delete(hm.sessions, sessionID)
	hm.mutex.Unlock()
	
	// Stop all path heartbeat routines
	for _, pathState := range pathStates {
		close(pathState.stopChan)
		if pathState.ticker != nil {
			pathState.ticker.Stop()
		}
	}
	
	return nil
}

// OnHeartbeatReceived should be called when a heartbeat response is received
func (hm *HeartbeatManager) OnHeartbeatReceived(sessionID, pathID string) error {
	hm.mutex.RLock()
	sessionState, exists := hm.sessions[sessionID]
	hm.mutex.RUnlock()
	
	if !exists {
		return nil
	}
	
	sessionState.mutex.RLock()
	pathState, exists := sessionState.paths[pathID]
	sessionState.mutex.RUnlock()
	
	if !exists {
		return nil
	}
	
	pathState.mutex.Lock()
	defer pathState.mutex.Unlock()
	
	now := time.Now()
	pathState.lastReceived = now
	pathState.totalReceived++
	pathState.consecutiveFails = 0
	
	// Calculate response time if we have a recent send time
	if !pathState.lastSent.IsZero() && pathState.lastSent.Before(now) {
		responseTime := now.Sub(pathState.lastSent)
		pathState.responseTimes = append(pathState.responseTimes, responseTime)
		
		// Keep only last 100 response times
		if len(pathState.responseTimes) > 100 {
			pathState.responseTimes = pathState.responseTimes[1:]
		}
		
		// Calculate average response time
		hm.calculateAverageResponseTime(pathState)
	}
	
	// Trigger interval adaptation
	hm.adaptHeartbeatInterval(pathState)
	
	return nil
}

// UpdatePathHealth updates path health information for heartbeat adaptation
func (hm *HeartbeatManager) UpdatePathHealth(sessionID, pathID string, rtt time.Duration, 
	packetLoss bool, healthScore int) error {
	
	hm.mutex.RLock()
	sessionState, exists := hm.sessions[sessionID]
	hm.mutex.RUnlock()
	
	if !exists {
		return nil
	}
	
	sessionState.mutex.RLock()
	pathState, exists := sessionState.paths[pathID]
	sessionState.mutex.RUnlock()
	
	if !exists {
		return nil
	}
	
	pathState.mutex.Lock()
	defer pathState.mutex.Unlock()
	
	// Update RTT history
	if rtt > 0 {
		pathState.rttHistory = append(pathState.rttHistory, rtt)
		if len(pathState.rttHistory) > 50 {
			pathState.rttHistory = pathState.rttHistory[1:]
		}
	}
	
	// Update loss history
	pathState.lossHistory = append(pathState.lossHistory, packetLoss)
	if len(pathState.lossHistory) > 50 {
		pathState.lossHistory = pathState.lossHistory[1:]
	}
	
	// Update health score
	pathState.healthScore = healthScore
	
	// Trigger interval adaptation
	hm.adaptHeartbeatInterval(pathState)
	
	return nil
}

// GetHeartbeatStats returns heartbeat statistics for a path
func (hm *HeartbeatManager) GetHeartbeatStats(sessionID, pathID string) *HeartbeatStats {
	hm.mutex.RLock()
	sessionState, exists := hm.sessions[sessionID]
	hm.mutex.RUnlock()
	
	if !exists {
		return nil
	}
	
	sessionState.mutex.RLock()
	pathState, exists := sessionState.paths[pathID]
	sessionState.mutex.RUnlock()
	
	if !exists {
		return nil
	}
	
	pathState.mutex.RLock()
	defer pathState.mutex.RUnlock()
	
	var successRate float64
	if pathState.totalSent > 0 {
		successRate = float64(pathState.totalReceived) / float64(pathState.totalSent)
	}
	
	return &HeartbeatStats{
		PathID:           pathID,
		CurrentInterval:  pathState.currentInterval,
		TargetInterval:   pathState.targetInterval,
		LastSent:         pathState.lastSent,
		LastReceived:     pathState.lastReceived,
		ConsecutiveFails: pathState.consecutiveFails,
		TotalSent:        pathState.totalSent,
		TotalReceived:    pathState.totalReceived,
		SuccessRate:      successRate,
		AvgResponseTime:  pathState.avgResponseTime,
		IsActive:         pathState.ticker != nil,
	}
}

// GetAllHeartbeatStats returns heartbeat statistics for all paths in a session
func (hm *HeartbeatManager) GetAllHeartbeatStats(sessionID string) map[string]*HeartbeatStats {
	hm.mutex.RLock()
	sessionState, exists := hm.sessions[sessionID]
	hm.mutex.RUnlock()
	
	if !exists {
		return nil
	}
	
	sessionState.mutex.RLock()
	defer sessionState.mutex.RUnlock()
	
	stats := make(map[string]*HeartbeatStats)
	for pathID := range sessionState.paths {
		if pathStats := hm.GetHeartbeatStats(sessionID, pathID); pathStats != nil {
			stats[pathID] = pathStats
		}
	}
	
	return stats
}

// heartbeatRoutine runs the heartbeat loop for a specific path
func (hm *HeartbeatManager) heartbeatRoutine(sessionID, pathID string, pathState *pathHeartbeatState) {
	pathState.mutex.Lock()
	pathState.ticker = time.NewTicker(pathState.currentInterval)
	pathState.mutex.Unlock()
	
	defer func() {
		pathState.mutex.Lock()
		if pathState.ticker != nil {
			pathState.ticker.Stop()
			pathState.ticker = nil
		}
		pathState.mutex.Unlock()
	}()
	
	for {
		select {
		case <-hm.ctx.Done():
			return
		case <-pathState.stopChan:
			return
		case <-pathState.ticker.C:
			hm.sendHeartbeat(sessionID, pathID, pathState)
			hm.checkHeartbeatTimeout(sessionID, pathID, pathState)
			hm.updateHeartbeatTicker(pathState)
		}
	}
}

// sendHeartbeat sends a heartbeat for a path
func (hm *HeartbeatManager) sendHeartbeat(sessionID, pathID string, pathState *pathHeartbeatState) {
	pathState.mutex.Lock()
	
	// Check authentication status before sending heartbeat
	authCheckCallback := pathState.authCheckCallback
	isAuthenticated := pathState.isAuthenticated
	
	// Update authentication status if callback is available
	if authCheckCallback != nil {
		isAuthenticated = authCheckCallback(sessionID, pathID)
		pathState.isAuthenticated = isAuthenticated
	}
	
	// Only send heartbeat if path is authenticated
	if !isAuthenticated {
		pathState.mutex.Unlock()
		return // Skip heartbeat if not authenticated
	}
	
	now := time.Now()
	pathState.lastSent = now
	pathState.totalSent++
	sendCallback := pathState.sendCallback
	pathState.mutex.Unlock()
	
	// Call the send callback synchronously to avoid race conditions
	if sendCallback != nil {
		sendCallback(sessionID, pathID)
	}
}

// checkHeartbeatTimeout checks for heartbeat timeouts
func (hm *HeartbeatManager) checkHeartbeatTimeout(sessionID, pathID string, pathState *pathHeartbeatState) {
	pathState.mutex.Lock()
	defer pathState.mutex.Unlock()
	
	if pathState.lastSent.IsZero() {
		return
	}
	
	timeout := time.Duration(float64(pathState.currentInterval) * hm.timeoutMultiplier)
	timeSinceLastReceived := time.Since(pathState.lastReceived)
	
	// Check if we haven't received a response within the timeout period
	if pathState.lastReceived.Before(pathState.lastSent) && time.Since(pathState.lastSent) > timeout {
		pathState.consecutiveFails++
		timeoutCallback := pathState.timeoutCallback
		consecutiveFails := pathState.consecutiveFails
		
		// Adapt interval based on failure
		hm.adaptHeartbeatInterval(pathState)
		
		// Call timeout callback synchronously
		if timeoutCallback != nil {
			timeoutCallback(sessionID, pathID, consecutiveFails)
		}
	} else if timeSinceLastReceived > timeout*2 {
		// Haven't received any heartbeat in a long time
		pathState.consecutiveFails++
		timeoutCallback := pathState.timeoutCallback
		consecutiveFails := pathState.consecutiveFails
		
		if timeoutCallback != nil {
			timeoutCallback(sessionID, pathID, consecutiveFails)
		}
	}
}

// adaptHeartbeatInterval adapts the heartbeat interval based on path conditions
func (hm *HeartbeatManager) adaptHeartbeatInterval(pathState *pathHeartbeatState) {
	// Calculate target interval based on path health
	var targetInterval time.Duration
	
	if pathState.healthScore >= 80 {
		// Healthy path - use longer intervals
		targetInterval = hm.maxInterval
	} else if pathState.healthScore >= 50 {
		// Moderately healthy - use medium intervals
		targetInterval = (hm.minInterval + hm.maxInterval) / 2
	} else {
		// Unhealthy path - use short intervals for quick detection
		targetInterval = hm.minInterval
	}
	
	// Adjust based on consecutive failures
	if pathState.consecutiveFails > 0 {
		// Increase frequency when failures occur
		failureFactor := 1.0 / (1.0 + float64(pathState.consecutiveFails)*0.2)
		targetInterval = time.Duration(float64(targetInterval) * failureFactor)
	}
	
	// Adjust based on RTT if available
	if len(pathState.rttHistory) > 0 {
		avgRTT := hm.calculateAverageRTT(pathState)
		// Use at least 3x RTT as minimum interval to avoid overwhelming
		minRTTInterval := avgRTT * 3
		if targetInterval < minRTTInterval && minRTTInterval < hm.maxInterval {
			targetInterval = minRTTInterval
		}
	}
	
	// Ensure target is within bounds
	if targetInterval < hm.minInterval {
		targetInterval = hm.minInterval
	} else if targetInterval > hm.maxInterval {
		targetInterval = hm.maxInterval
	}
	
	pathState.targetInterval = targetInterval
	
	// Gradually adapt current interval towards target
	currentInterval := pathState.currentInterval
	diff := targetInterval - currentInterval
	adaptation := time.Duration(float64(diff) * hm.adaptationFactor)
	pathState.currentInterval = currentInterval + adaptation
	
	// Ensure current interval is within bounds
	if pathState.currentInterval < hm.minInterval {
		pathState.currentInterval = hm.minInterval
	} else if pathState.currentInterval > hm.maxInterval {
		pathState.currentInterval = hm.maxInterval
	}
}

// updateHeartbeatTicker updates the ticker with the new interval if it changed significantly
func (hm *HeartbeatManager) updateHeartbeatTicker(pathState *pathHeartbeatState) {
	pathState.mutex.Lock()
	defer pathState.mutex.Unlock()
	
	if pathState.ticker == nil {
		return
	}
	
	// Update ticker if interval changed by more than 10%
	currentTickerInterval := pathState.currentInterval
	if pathState.ticker != nil {
		// Calculate percentage change
		oldInterval := currentTickerInterval
		newInterval := pathState.currentInterval
		
		if oldInterval > 0 {
			percentChange := float64(newInterval-oldInterval) / float64(oldInterval)
			if percentChange < 0 {
				percentChange = -percentChange
			}
			
			// Update if change is significant (>10%)
			if percentChange > 0.1 {
				pathState.ticker.Stop()
				pathState.ticker = time.NewTicker(newInterval)
			}
		}
	}
}

// calculateAverageResponseTime calculates the average response time
func (hm *HeartbeatManager) calculateAverageResponseTime(pathState *pathHeartbeatState) {
	if len(pathState.responseTimes) == 0 {
		pathState.avgResponseTime = 0
		return
	}
	
	var sum time.Duration
	for _, rt := range pathState.responseTimes {
		sum += rt
	}
	pathState.avgResponseTime = sum / time.Duration(len(pathState.responseTimes))
}

// calculateAverageRTT calculates the average RTT from history
func (hm *HeartbeatManager) calculateAverageRTT(pathState *pathHeartbeatState) time.Duration {
	if len(pathState.rttHistory) == 0 {
		return 0
	}
	
	var sum time.Duration
	for _, rtt := range pathState.rttHistory {
		sum += rtt
	}
	return sum / time.Duration(len(pathState.rttHistory))
}

// SetPathAuthenticated marks a path as authenticated, enabling heartbeat sending
func (hm *HeartbeatManager) SetPathAuthenticated(sessionID, pathID string, authenticated bool) error {
	hm.mutex.RLock()
	sessionState, exists := hm.sessions[sessionID]
	hm.mutex.RUnlock()
	
	if !exists {
		return fmt.Errorf("session %s not found", sessionID)
	}
	
	sessionState.mutex.RLock()
	pathState, exists := sessionState.paths[pathID]
	sessionState.mutex.RUnlock()
	
	if !exists {
		return fmt.Errorf("path %s not found in session %s", pathID, sessionID)
	}
	
	pathState.mutex.Lock()
	pathState.isAuthenticated = authenticated
	pathState.mutex.Unlock()
	
	return nil
}

// IsPathAuthenticated checks if a path is authenticated
func (hm *HeartbeatManager) IsPathAuthenticated(sessionID, pathID string) bool {
	hm.mutex.RLock()
	sessionState, exists := hm.sessions[sessionID]
	hm.mutex.RUnlock()
	
	if !exists {
		return false
	}
	
	sessionState.mutex.RLock()
	pathState, exists := sessionState.paths[pathID]
	sessionState.mutex.RUnlock()
	
	if !exists {
		return false
	}
	
	pathState.mutex.RLock()
	authenticated := pathState.isAuthenticated
	pathState.mutex.RUnlock()
	
	return authenticated
}

// Shutdown gracefully shuts down the heartbeat manager
func (hm *HeartbeatManager) Shutdown() error {
	hm.cancel()
	
	hm.mutex.Lock()
	sessionIDs := make([]string, 0, len(hm.sessions))
	for sessionID := range hm.sessions {
		sessionIDs = append(sessionIDs, sessionID)
	}
	hm.mutex.Unlock()
	
	// Stop all sessions
	for _, sessionID := range sessionIDs {
		hm.StopSession(sessionID)
	}
	
	return nil
}