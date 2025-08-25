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
	
	// Integration with new heartbeat system
	integrationLayer HeartbeatIntegrationLayer
	
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
	firstUnackedSent  time.Time // Time when we first sent a heartbeat without receiving a response
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

// SetIntegrationLayer sets the heartbeat integration layer for event synchronization
func (hm *HeartbeatManager) SetIntegrationLayer(integrationLayer HeartbeatIntegrationLayer) {
	hm.mutex.Lock()
	defer hm.mutex.Unlock()
	hm.integrationLayer = integrationLayer
}

// SyncHeartbeatEvent synchronizes heartbeat events with the integration layer
func (hm *HeartbeatManager) SyncHeartbeatEvent(event *HeartbeatEvent) error {
	if hm.integrationLayer == nil {
		return nil // No integration layer configured
	}
	
	return hm.integrationLayer.SyncHeartbeatEvent(event)
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
	pathState := &pathHeartbeatState{
		pathID:            pathID,
		currentInterval:   initialInterval,
		targetInterval:    initialInterval,
		lastReceived:      time.Time{}, // Initialize to zero time so timeout detection works properly
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
	
	// Reset first unacked sent time since we received a response
	pathState.firstUnackedSent = time.Time{}
	
	var rtt *time.Duration
	
	// Calculate response time if we have a recent send time
	if !pathState.lastSent.IsZero() && pathState.lastSent.Before(now) {
		responseTime := now.Sub(pathState.lastSent)
		pathState.responseTimes = append(pathState.responseTimes, responseTime)
		rtt = &responseTime
		
		// Keep only last 100 response times
		if len(pathState.responseTimes) > 100 {
			pathState.responseTimes = pathState.responseTimes[1:]
		}
		
		// Calculate average response time
		hm.calculateAverageResponseTime(pathState)
	}
	
	// Sync event with integration layer
	if hm.integrationLayer != nil {
		event := &HeartbeatEvent{
			Type:      HeartbeatEventResponseReceived,
			PathID:    pathID,
			SessionID: sessionID,
			PlaneType: HeartbeatPlaneControl, // Assume control plane for existing manager
			Timestamp: now,
			RTT:       rtt,
			Success:   true,
		}
		hm.integrationLayer.SyncHeartbeatEvent(event)
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
	
	// Create a separate ticker for timeout checks (check more frequently than heartbeat interval)
	timeoutCheckInterval := pathState.currentInterval / 4
	if timeoutCheckInterval < 10*time.Millisecond {
		timeoutCheckInterval = 10 * time.Millisecond
	}
	timeoutTicker := time.NewTicker(timeoutCheckInterval)
	
	defer func() {
		pathState.mutex.Lock()
		if pathState.ticker != nil {
			pathState.ticker.Stop()
			pathState.ticker = nil
		}
		pathState.mutex.Unlock()
		timeoutTicker.Stop()
	}()
	
	for {
		select {
		case <-hm.ctx.Done():
			return
		case <-pathState.stopChan:
			return
		case <-pathState.ticker.C:
			hm.sendHeartbeat(sessionID, pathID, pathState)
			hm.updateHeartbeatTicker(pathState)
		case <-timeoutTicker.C:
			hm.checkHeartbeatTimeout(sessionID, pathID, pathState)
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
	
	// Track first unacknowledged heartbeat for timeout calculation
	if pathState.firstUnackedSent.IsZero() || 
		(!pathState.lastReceived.IsZero() && pathState.lastReceived.After(pathState.firstUnackedSent)) {
		// Either this is the first heartbeat, or we received a response to previous heartbeats
		pathState.firstUnackedSent = now
	}
	
	sendCallback := pathState.sendCallback
	pathState.mutex.Unlock()
	
	// Sync event with integration layer before sending
	if hm.integrationLayer != nil {
		event := &HeartbeatEvent{
			Type:      HeartbeatEventSent,
			PathID:    pathID,
			SessionID: sessionID,
			PlaneType: HeartbeatPlaneControl, // Assume control plane for existing manager
			Timestamp: now,
			Success:   true,
		}
		hm.integrationLayer.SyncHeartbeatEvent(event)
	}
	
	// Call the send callback synchronously to avoid race conditions
	if sendCallback != nil {
		err := sendCallback(sessionID, pathID)
		
		// Sync failure event if send failed
		if err != nil {
			// Update failure count
			pathState.mutex.Lock()
			pathState.consecutiveFails++
			pathState.mutex.Unlock()
			
			// Sync failure event with integration layer
			if hm.integrationLayer != nil {
				failureEvent := &HeartbeatEvent{
					Type:         HeartbeatEventFailure,
					PathID:       pathID,
					SessionID:    sessionID,
					PlaneType:    HeartbeatPlaneControl,
					Timestamp:    time.Now(),
					Success:      false,
					ErrorCode:    "SEND_ERROR",
					ErrorMessage: err.Error(),
				}
				hm.integrationLayer.SyncHeartbeatEvent(failureEvent)
			}
		}
	}
}

// checkHeartbeatTimeout checks for heartbeat timeouts
func (hm *HeartbeatManager) checkHeartbeatTimeout(sessionID, pathID string, pathState *pathHeartbeatState) {
	pathState.mutex.Lock()
	defer pathState.mutex.Unlock()
	
	if pathState.firstUnackedSent.IsZero() {
		return // No unacknowledged heartbeats
	}
	
	timeout := time.Duration(float64(pathState.currentInterval) * hm.timeoutMultiplier)
	timeSinceFirstUnacked := time.Since(pathState.firstUnackedSent)
	
	shouldTimeout := timeSinceFirstUnacked > timeout
	
	if shouldTimeout {
		pathState.consecutiveFails++
		timeoutCallback := pathState.timeoutCallback
		consecutiveFails := pathState.consecutiveFails
		
		// Reset first unacked time to avoid repeated timeouts
		pathState.firstUnackedSent = time.Time{}
		
		// Sync timeout event with integration layer
		if hm.integrationLayer != nil {
			timeoutEvent := &HeartbeatEvent{
				Type:         HeartbeatEventTimeout,
				PathID:       pathID,
				SessionID:    sessionID,
				PlaneType:    HeartbeatPlaneControl,
				Timestamp:    time.Now(),
				Success:      false,
				ErrorCode:    "TIMEOUT",
				ErrorMessage: fmt.Sprintf("heartbeat timeout after %v", timeout),
				Metadata: map[string]interface{}{
					"consecutive_failures": consecutiveFails,
					"timeout_duration":     timeout,
				},
			}
			hm.integrationLayer.SyncHeartbeatEvent(timeoutEvent)
		}
		
		// Adapt interval based on failure
		hm.adaptHeartbeatInterval(pathState)
		
		// Call timeout callback synchronously
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

// GetIntegratedHeartbeatStats returns heartbeat statistics integrated with new system metrics
func (hm *HeartbeatManager) GetIntegratedHeartbeatStats(sessionID, pathID string) *HeartbeatStats {
	// Get base stats from existing system
	stats := hm.GetHeartbeatStats(sessionID, pathID)
	if stats == nil {
		return nil
	}
	
	// Enhance with integration layer metrics if available
	if hm.integrationLayer != nil {
		integrationStats := hm.integrationLayer.GetIntegrationStats()
		if integrationStats != nil {
			// Add integration-specific information to metadata or extend stats
			// This could include event processing metrics, integration errors, etc.
		}
	}
	
	return stats
}

// SyncWithNewHeartbeatSystem synchronizes the existing heartbeat manager with new heartbeat systems
func (hm *HeartbeatManager) SyncWithNewHeartbeatSystem(controlSystem interface{}, dataSystem interface{}) error {
	// This method can be used to coordinate between the existing heartbeat manager
	// and the new control/data plane heartbeat systems
	
	// For now, we rely on the integration layer for coordination
	// Future implementations could add direct coordination logic here
	
	return nil
}

// Shutdown gracefully shuts down the heartbeat manager
func (hm *HeartbeatManager) Shutdown() error {
	hm.cancel()
	
	// Shutdown integration layer if available
	if hm.integrationLayer != nil {
		if err := hm.integrationLayer.Shutdown(); err != nil {
			// Log error but continue shutdown
		}
	}
	
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