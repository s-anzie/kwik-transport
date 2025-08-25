package control

import (
	"context"
	"fmt"
	"sync"
	"time"

	"kwik/pkg/protocol"
)

// HeartbeatScheduler interface for adaptive scheduling
type HeartbeatScheduler interface {
	CalculateHeartbeatInterval(pathID string, planeType protocol.HeartbeatPlaneType, streamID *uint64) time.Duration
	UpdateNetworkConditions(pathID string, rtt time.Duration, packetLoss float64) error
	UpdateFailureCount(pathID string, consecutiveFailures int) error
}

// ControlPlaneHeartbeatSystem manages heartbeats on the control plane
type ControlPlaneHeartbeatSystem interface {
	// Start control plane heartbeats for a session
	StartControlHeartbeats(sessionID string, pathID string) error
	
	// Stop control plane heartbeats
	StopControlHeartbeats(sessionID string, pathID string) error
	
	// Send a control heartbeat request
	SendControlHeartbeat(pathID string) error
	
	// Handle received control heartbeat request
	HandleControlHeartbeatRequest(request *protocol.HeartbeatFrame) error
	
	// Handle received control heartbeat response
	HandleControlHeartbeatResponse(response *protocol.HeartbeatResponseFrame) error
	
	// Get control heartbeat statistics
	GetControlHeartbeatStats(pathID string) *ControlHeartbeatStats
	
	// Get all control heartbeat statistics
	GetAllControlHeartbeatStats() map[string]*ControlHeartbeatStats
	
	// Set server mode (for response generation)
	SetServerMode(isServer bool)
	
	// Shutdown the heartbeat system
	Shutdown() error
}

// ControlHeartbeatStats contains statistics for control plane heartbeats
type ControlHeartbeatStats struct {
	PathID              string
	SessionID           string
	HeartbeatsSent      uint64
	HeartbeatsReceived  uint64
	ResponsesReceived   uint64
	ResponsesSent       uint64
	ConsecutiveFailures int
	AverageRTT          time.Duration
	LastHeartbeatSent   time.Time
	LastHeartbeatReceived time.Time
	LastResponseReceived time.Time
	LastResponseSent    time.Time
	CurrentInterval     time.Duration
	IsActive            bool
}

// ControlHeartbeatState tracks heartbeat state for a control plane session
type ControlHeartbeatState struct {
	SessionID           string
	PathID              string
	Active              bool
	NextSequenceID      uint64
	PendingRequests     map[uint64]*PendingControlHeartbeatRequest
	LastSent            time.Time
	LastReceived        time.Time
	ConsecutiveFailures int
	Statistics          *ControlHeartbeatStats
	
	// Timing
	HeartbeatInterval   time.Duration
	HeartbeatTimer      *time.Timer
	
	// Synchronization
	mutex sync.RWMutex
}

// PendingControlHeartbeatRequest tracks pending heartbeat requests
type PendingControlHeartbeatRequest struct {
	SequenceID      uint64
	SentAt          time.Time
	Timeout         time.Time
	RTTMeasurement  bool
	RetryCount      int
	MaxRetries      int
}

// ControlPlaneHeartbeatSystemImpl implements ControlPlaneHeartbeatSystem
type ControlPlaneHeartbeatSystemImpl struct {
	// Configuration
	config *ControlHeartbeatConfig
	
	// State management
	sessions map[string]*ControlHeartbeatState // sessionID -> state
	paths    map[string]string                 // pathID -> sessionID
	
	// Control plane integration
	controlPlane ControlPlane
	
	// Heartbeat scheduling
	scheduler HeartbeatScheduler
	
	// Frame generation
	frameIDGenerator uint64
	
	// Server mode
	isServer bool
	
	// Context and lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	
	// Synchronization
	mutex sync.RWMutex
	
	// Logging
	logger Logger
}

// ControlHeartbeatConfig contains configuration for control plane heartbeats
type ControlHeartbeatConfig struct {
	DefaultInterval     time.Duration
	MinInterval         time.Duration
	MaxInterval         time.Duration
	TimeoutMultiplier   float64
	MaxRetries          int
	RTTMeasurementRatio float64 // Ratio of heartbeats that measure RTT
}

// Logger interface for control heartbeat system logging
type Logger interface {
	Debug(msg string, keysAndValues ...interface{})
	Info(msg string, keysAndValues ...interface{})
	Warn(msg string, keysAndValues ...interface{})
	Error(msg string, keysAndValues ...interface{})
}



// DefaultControlHeartbeatConfig returns default configuration
func DefaultControlHeartbeatConfig() *ControlHeartbeatConfig {
	return &ControlHeartbeatConfig{
		DefaultInterval:     30 * time.Second,
		MinInterval:         5 * time.Second,
		MaxInterval:         120 * time.Second,
		TimeoutMultiplier:   3.0,
		MaxRetries:          3,
		RTTMeasurementRatio: 0.1, // 10% of heartbeats measure RTT
	}
}

// NewControlPlaneHeartbeatSystem creates a new control plane heartbeat system
func NewControlPlaneHeartbeatSystem(
	controlPlane ControlPlane,
	config *ControlHeartbeatConfig,
	logger Logger,
) *ControlPlaneHeartbeatSystemImpl {
	if config == nil {
		config = DefaultControlHeartbeatConfig()
	}
	
	if logger == nil {
		logger = &DefaultControlLogger{}
	}
	
	ctx, cancel := context.WithCancel(context.Background())
	
	// Scheduler will be set later to avoid import cycles
	var scheduler HeartbeatScheduler
	
	return &ControlPlaneHeartbeatSystemImpl{
		config:           config,
		sessions:         make(map[string]*ControlHeartbeatState),
		paths:            make(map[string]string),
		controlPlane:     controlPlane,
		scheduler:        scheduler,
		frameIDGenerator: 1,
		isServer:         false,
		ctx:              ctx,
		cancel:           cancel,
		logger:           logger,
	}
}

// DefaultControlLogger provides a simple logger implementation
type DefaultControlLogger struct{}

func (d *DefaultControlLogger) Debug(msg string, keysAndValues ...interface{}) {}
func (d *DefaultControlLogger) Info(msg string, keysAndValues ...interface{})  {}
func (d *DefaultControlLogger) Warn(msg string, keysAndValues ...interface{})  {}
func (d *DefaultControlLogger) Error(msg string, keysAndValues ...interface{}) {}

// SetServerMode sets whether this system is running in server mode
func (c *ControlPlaneHeartbeatSystemImpl) SetServerMode(isServer bool) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	
	c.isServer = isServer
	c.logger.Info("Control heartbeat system mode set", "isServer", isServer)
}

// SetHeartbeatScheduler sets the heartbeat scheduler
func (c *ControlPlaneHeartbeatSystemImpl) SetHeartbeatScheduler(scheduler HeartbeatScheduler) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	
	c.scheduler = scheduler
	c.logger.Info("Heartbeat scheduler set for control heartbeat system")
}

// StartControlHeartbeats starts control plane heartbeats for a session
func (c *ControlPlaneHeartbeatSystemImpl) StartControlHeartbeats(sessionID string, pathID string) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	
	// Check if session already exists
	if _, exists := c.sessions[sessionID]; exists {
		return fmt.Errorf("control heartbeats already started for session %s", sessionID)
	}
	
	// Create heartbeat state
	state := &ControlHeartbeatState{
		SessionID:           sessionID,
		PathID:              pathID,
		Active:              true,
		NextSequenceID:      1,
		PendingRequests:     make(map[uint64]*PendingControlHeartbeatRequest),
		HeartbeatInterval:   c.config.DefaultInterval,
		Statistics: &ControlHeartbeatStats{
			PathID:          pathID,
			SessionID:       sessionID,
			CurrentInterval: c.config.DefaultInterval,
			IsActive:        true,
		},
	}
	
	c.sessions[sessionID] = state
	c.paths[pathID] = sessionID
	
	// Start heartbeat routine for client mode only
	if !c.isServer {
		c.wg.Add(1)
		go c.controlHeartbeatRoutine(sessionID, state)
	}
	
	c.logger.Info("Control heartbeats started", 
		"sessionID", sessionID, 
		"pathID", pathID,
		"isServer", c.isServer)
	
	return nil
}

// StopControlHeartbeats stops control plane heartbeats
func (c *ControlPlaneHeartbeatSystemImpl) StopControlHeartbeats(sessionID string, pathID string) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	
	state, exists := c.sessions[sessionID]
	if !exists {
		return fmt.Errorf("no control heartbeats found for session %s", sessionID)
	}
	
	// Stop the heartbeat routine
	state.mutex.Lock()
	state.Active = false
	if state.HeartbeatTimer != nil {
		state.HeartbeatTimer.Stop()
	}
	state.mutex.Unlock()
	
	// Remove from maps
	delete(c.sessions, sessionID)
	delete(c.paths, pathID)
	
	c.logger.Info("Control heartbeats stopped", "sessionID", sessionID, "pathID", pathID)
	
	return nil
}

// SendControlHeartbeat sends a control heartbeat request
func (c *ControlPlaneHeartbeatSystemImpl) SendControlHeartbeat(pathID string) error {
	c.mutex.RLock()
	sessionID, exists := c.paths[pathID]
	c.mutex.RUnlock()
	
	if !exists {
		return fmt.Errorf("no session found for path %s", pathID)
	}
	
	c.mutex.RLock()
	state, exists := c.sessions[sessionID]
	c.mutex.RUnlock()
	
	if !exists {
		return fmt.Errorf("no heartbeat state found for session %s", sessionID)
	}
	
	state.mutex.Lock()
	defer state.mutex.Unlock()
	
	if !state.Active {
		return fmt.Errorf("heartbeats not active for session %s", sessionID)
	}
	
	// Generate sequence ID
	sequenceID := state.NextSequenceID
	state.NextSequenceID++
	
	// Determine if this heartbeat should measure RTT
	rttMeasurement := (float64(sequenceID) * c.config.RTTMeasurementRatio) >= 1.0
	
	// Create heartbeat frame
	frameID := c.generateFrameID()
	heartbeatFrame := &protocol.HeartbeatFrame{
		FrameID:        frameID,
		SequenceID:     sequenceID,
		PathID:         pathID,
		PlaneType:      protocol.HeartbeatPlaneControl,
		StreamID:       nil, // Control plane doesn't use stream ID
		Timestamp:      time.Now(),
		RTTMeasurement: rttMeasurement,
	}
	
	// Send frame via control plane
	err := c.controlPlane.SendFrame(pathID, heartbeatFrame)
	if err != nil {
		c.logger.Error("Failed to send control heartbeat", 
			"sessionID", sessionID,
			"pathID", pathID,
			"sequenceID", sequenceID,
			"error", err)
		return fmt.Errorf("failed to send control heartbeat: %w", err)
	}
	
	// Track pending request
	now := time.Now()
	timeout := now.Add(time.Duration(float64(state.HeartbeatInterval) * c.config.TimeoutMultiplier))
	
	state.PendingRequests[sequenceID] = &PendingControlHeartbeatRequest{
		SequenceID:     sequenceID,
		SentAt:         now,
		Timeout:        timeout,
		RTTMeasurement: rttMeasurement,
		RetryCount:     0,
		MaxRetries:     c.config.MaxRetries,
	}
	
	// Update statistics
	state.LastSent = now
	state.Statistics.HeartbeatsSent++
	state.Statistics.LastHeartbeatSent = now
	
	c.logger.Debug("Control heartbeat sent", 
		"sessionID", sessionID,
		"pathID", pathID,
		"sequenceID", sequenceID,
		"rttMeasurement", rttMeasurement)
	
	return nil
}

// HandleControlHeartbeatRequest handles received control heartbeat requests
func (c *ControlPlaneHeartbeatSystemImpl) HandleControlHeartbeatRequest(request *protocol.HeartbeatFrame) error {
	if request.PlaneType != protocol.HeartbeatPlaneControl {
		return fmt.Errorf("expected control plane heartbeat, got %d", request.PlaneType)
	}
	
	// Find session for this path
	c.mutex.RLock()
	sessionID, exists := c.paths[request.PathID]
	c.mutex.RUnlock()
	
	if !exists {
		// If we're a server, we might receive heartbeats before the session is fully established
		// In this case, we should still respond but log a warning
		if c.isServer {
			c.logger.Warn("Received control heartbeat for unknown path, responding anyway", 
				"pathID", request.PathID,
				"sequenceID", request.SequenceID)
			return c.sendControlHeartbeatResponse(request, "unknown-session")
		}
		return fmt.Errorf("no session found for path %s", request.PathID)
	}
	
	c.mutex.RLock()
	state, exists := c.sessions[sessionID]
	c.mutex.RUnlock()
	
	if exists {
		// Update statistics
		state.mutex.Lock()
		now := time.Now()
		state.LastReceived = now
		state.Statistics.HeartbeatsReceived++
		state.Statistics.LastHeartbeatReceived = now
		state.mutex.Unlock()
	}
	
	// Send response (servers always respond to heartbeat requests)
	if c.isServer {
		err := c.sendControlHeartbeatResponse(request, sessionID)
		if err != nil {
			c.logger.Error("Failed to send control heartbeat response", 
				"sessionID", sessionID,
				"pathID", request.PathID,
				"sequenceID", request.SequenceID,
				"error", err)
			return fmt.Errorf("failed to send control heartbeat response: %w", err)
		}
	}
	
	c.logger.Debug("Control heartbeat request handled", 
		"sessionID", sessionID,
		"pathID", request.PathID,
		"sequenceID", request.SequenceID,
		"isServer", c.isServer)
	
	return nil
}

// HandleControlHeartbeatResponse handles received control heartbeat responses
func (c *ControlPlaneHeartbeatSystemImpl) HandleControlHeartbeatResponse(response *protocol.HeartbeatResponseFrame) error {
	if response.PlaneType != protocol.HeartbeatPlaneControl {
		return fmt.Errorf("expected control plane heartbeat response, got %d", response.PlaneType)
	}
	
	// Find session for this path
	c.mutex.RLock()
	sessionID, exists := c.paths[response.PathID]
	c.mutex.RUnlock()
	
	if !exists {
		return fmt.Errorf("no session found for path %s", response.PathID)
	}
	
	c.mutex.RLock()
	state, exists := c.sessions[sessionID]
	c.mutex.RUnlock()
	
	if !exists {
		return fmt.Errorf("no heartbeat state found for session %s", sessionID)
	}
	
	state.mutex.Lock()
	defer state.mutex.Unlock()
	
	// Find and remove pending request
	pendingRequest, exists := state.PendingRequests[response.RequestSequenceID]
	if !exists {
		c.logger.Warn("Received response for unknown heartbeat request", 
			"sessionID", sessionID,
			"pathID", response.PathID,
			"requestSequenceID", response.RequestSequenceID)
		return nil // Not an error, might be a late response
	}
	
	delete(state.PendingRequests, response.RequestSequenceID)
	
	// Calculate RTT if this was an RTT measurement
	now := time.Now()
	var rtt time.Duration
	if pendingRequest.RTTMeasurement {
		rtt = now.Sub(pendingRequest.SentAt)
		
		// Update average RTT (simple moving average)
		if state.Statistics.AverageRTT == 0 {
			state.Statistics.AverageRTT = rtt
		} else {
			// Exponential moving average with alpha = 0.125
			state.Statistics.AverageRTT = time.Duration(
				float64(state.Statistics.AverageRTT)*0.875 + float64(rtt)*0.125,
			)
		}
		
		// Update adaptive scheduler with network conditions
		if c.scheduler != nil {
			// Estimate packet loss based on consecutive failures (simplified)
			packetLoss := float64(state.ConsecutiveFailures) * 0.01
			if packetLoss > 1.0 {
				packetLoss = 1.0
			}
			
			err := c.scheduler.UpdateNetworkConditions(response.PathID, rtt, packetLoss)
			if err != nil {
				c.logger.Warn("Failed to update network conditions in scheduler", 
					"pathID", response.PathID, "error", err)
			}
			
			// Update failure count in scheduler
			err = c.scheduler.UpdateFailureCount(response.PathID, state.ConsecutiveFailures)
			if err != nil {
				c.logger.Warn("Failed to update failure count in scheduler", 
					"pathID", response.PathID, "error", err)
			}
		}
	}
	
	// Reset consecutive failures
	state.ConsecutiveFailures = 0
	
	// Update statistics
	state.LastReceived = now
	state.Statistics.ResponsesReceived++
	state.Statistics.LastResponseReceived = now
	
	c.logger.Debug("Control heartbeat response handled", 
		"sessionID", sessionID,
		"pathID", response.PathID,
		"requestSequenceID", response.RequestSequenceID,
		"rtt", rtt,
		"serverLoad", response.ServerLoad)
	
	return nil
}

// sendControlHeartbeatResponse sends a heartbeat response
func (c *ControlPlaneHeartbeatSystemImpl) sendControlHeartbeatResponse(request *protocol.HeartbeatFrame, sessionID string) error {
	// Generate response frame
	frameID := c.generateFrameID()
	responseFrame := &protocol.HeartbeatResponseFrame{
		FrameID:           frameID,
		RequestSequenceID: request.SequenceID,
		PathID:            request.PathID,
		PlaneType:         protocol.HeartbeatPlaneControl,
		StreamID:          nil, // Control plane doesn't use stream ID
		Timestamp:         time.Now(),
		RequestTimestamp:  request.Timestamp,
		ServerLoad:        c.calculateServerLoad(),
	}
	
	// Send response via control plane
	err := c.controlPlane.SendFrame(request.PathID, responseFrame)
	if err != nil {
		return fmt.Errorf("failed to send control heartbeat response: %w", err)
	}
	
	// Update statistics if we have state for this session
	if sessionID != "unknown-session" {
		c.mutex.RLock()
		state, exists := c.sessions[sessionID]
		c.mutex.RUnlock()
		
		if exists {
			state.mutex.Lock()
			state.Statistics.ResponsesSent++
			state.Statistics.LastResponseSent = time.Now()
			state.mutex.Unlock()
		}
	}
	
	c.logger.Debug("Control heartbeat response sent", 
		"sessionID", sessionID,
		"pathID", request.PathID,
		"requestSequenceID", request.SequenceID,
		"serverLoad", responseFrame.ServerLoad)
	
	return nil
}

// calculateServerLoad calculates current server load (simplified implementation)
func (c *ControlPlaneHeartbeatSystemImpl) calculateServerLoad() float64 {
	// Simple implementation: base load on number of active sessions
	c.mutex.RLock()
	activeSessionCount := len(c.sessions)
	c.mutex.RUnlock()
	
	// Normalize to 0.0-1.0 range (assuming max 100 sessions for full load)
	load := float64(activeSessionCount) / 100.0
	if load > 1.0 {
		load = 1.0
	}
	
	return load
}

// controlHeartbeatRoutine runs the heartbeat loop for a session (client mode only)
func (c *ControlPlaneHeartbeatSystemImpl) controlHeartbeatRoutine(sessionID string, state *ControlHeartbeatState) {
	defer c.wg.Done()
	
	state.mutex.Lock()
	state.HeartbeatTimer = time.NewTimer(state.HeartbeatInterval)
	state.mutex.Unlock()
	
	defer func() {
		state.mutex.Lock()
		if state.HeartbeatTimer != nil {
			state.HeartbeatTimer.Stop()
		}
		state.mutex.Unlock()
	}()
	
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-state.HeartbeatTimer.C:
			state.mutex.RLock()
			active := state.Active
			pathID := state.PathID
			state.mutex.RUnlock()
			
			if !active {
				return
			}
			
			// Send heartbeat
			err := c.SendControlHeartbeat(pathID)
			if err != nil {
				c.logger.Error("Failed to send scheduled control heartbeat", 
					"sessionID", sessionID,
					"pathID", pathID,
					"error", err)
				
				// Increment failure count
				state.mutex.Lock()
				state.ConsecutiveFailures++
				consecutiveFailures := state.ConsecutiveFailures
				state.mutex.Unlock()
				
				// Update scheduler with failure count
				if c.scheduler != nil {
					err := c.scheduler.UpdateFailureCount(pathID, consecutiveFailures)
					if err != nil {
						c.logger.Warn("Failed to update failure count in scheduler after send failure", 
							"pathID", pathID, "error", err)
					}
				}
			}
			
			// Check for timeouts
			c.checkControlHeartbeatTimeouts(sessionID, state)
			
			// Calculate adaptive interval and schedule next heartbeat
			state.mutex.Lock()
			if state.Active && state.HeartbeatTimer != nil {
				// Get adaptive interval from scheduler
				var nextInterval time.Duration
				if c.scheduler != nil {
					nextInterval = c.scheduler.CalculateHeartbeatInterval(pathID, protocol.HeartbeatPlaneControl, nil)
				} else {
					nextInterval = state.HeartbeatInterval
				}
				
				// Update current interval if it changed
				if nextInterval != state.HeartbeatInterval {
					c.logger.Debug("Adaptive interval update", 
						"pathID", pathID,
						"oldInterval", state.HeartbeatInterval,
						"newInterval", nextInterval)
					
					state.HeartbeatInterval = nextInterval
					state.Statistics.CurrentInterval = nextInterval
				}
				
				state.HeartbeatTimer.Reset(nextInterval)
			}
			state.mutex.Unlock()
		}
	}
}

// checkControlHeartbeatTimeouts checks for and handles heartbeat timeouts
func (c *ControlPlaneHeartbeatSystemImpl) checkControlHeartbeatTimeouts(sessionID string, state *ControlHeartbeatState) {
	state.mutex.Lock()
	defer state.mutex.Unlock()
	
	now := time.Now()
	var timedOutRequests []uint64
	
	// Find timed out requests
	for sequenceID, request := range state.PendingRequests {
		if now.After(request.Timeout) {
			timedOutRequests = append(timedOutRequests, sequenceID)
		}
	}
	
	// Handle timeouts
	for _, sequenceID := range timedOutRequests {
		delete(state.PendingRequests, sequenceID)
		
		state.ConsecutiveFailures++
		
		c.logger.Warn("Control heartbeat timeout", 
			"sessionID", sessionID,
			"pathID", state.PathID,
			"sequenceID", sequenceID,
			"consecutiveFailures", state.ConsecutiveFailures)
		
		// Update scheduler with failure count
		if c.scheduler != nil {
			err := c.scheduler.UpdateFailureCount(state.PathID, state.ConsecutiveFailures)
			if err != nil {
				c.logger.Warn("Failed to update failure count in scheduler after timeout", 
					"pathID", state.PathID, "error", err)
			}
		}
		
		// TODO: Trigger error recovery if consecutive failures exceed threshold
	}
}

// GetControlHeartbeatStats returns heartbeat statistics for a path
func (c *ControlPlaneHeartbeatSystemImpl) GetControlHeartbeatStats(pathID string) *ControlHeartbeatStats {
	c.mutex.RLock()
	sessionID, exists := c.paths[pathID]
	c.mutex.RUnlock()
	
	if !exists {
		return nil
	}
	
	c.mutex.RLock()
	state, exists := c.sessions[sessionID]
	c.mutex.RUnlock()
	
	if !exists {
		return nil
	}
	
	state.mutex.RLock()
	defer state.mutex.RUnlock()
	
	// Return a copy of the statistics
	return &ControlHeartbeatStats{
		PathID:               state.Statistics.PathID,
		SessionID:            state.Statistics.SessionID,
		HeartbeatsSent:       state.Statistics.HeartbeatsSent,
		HeartbeatsReceived:   state.Statistics.HeartbeatsReceived,
		ResponsesReceived:    state.Statistics.ResponsesReceived,
		ResponsesSent:        state.Statistics.ResponsesSent,
		ConsecutiveFailures:  state.ConsecutiveFailures,
		AverageRTT:           state.Statistics.AverageRTT,
		LastHeartbeatSent:    state.Statistics.LastHeartbeatSent,
		LastHeartbeatReceived: state.Statistics.LastHeartbeatReceived,
		LastResponseReceived: state.Statistics.LastResponseReceived,
		LastResponseSent:     state.Statistics.LastResponseSent,
		CurrentInterval:      state.Statistics.CurrentInterval,
		IsActive:             state.Statistics.IsActive,
	}
}

// GetAllControlHeartbeatStats returns heartbeat statistics for all sessions
func (c *ControlPlaneHeartbeatSystemImpl) GetAllControlHeartbeatStats() map[string]*ControlHeartbeatStats {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	
	stats := make(map[string]*ControlHeartbeatStats)
	
	for sessionID, state := range c.sessions {
		state.mutex.RLock()
		stats[sessionID] = &ControlHeartbeatStats{
			PathID:               state.Statistics.PathID,
			SessionID:            state.Statistics.SessionID,
			HeartbeatsSent:       state.Statistics.HeartbeatsSent,
			HeartbeatsReceived:   state.Statistics.HeartbeatsReceived,
			ResponsesReceived:    state.Statistics.ResponsesReceived,
			ResponsesSent:        state.Statistics.ResponsesSent,
			ConsecutiveFailures:  state.ConsecutiveFailures,
			AverageRTT:           state.Statistics.AverageRTT,
			LastHeartbeatSent:    state.Statistics.LastHeartbeatSent,
			LastHeartbeatReceived: state.Statistics.LastHeartbeatReceived,
			LastResponseReceived: state.Statistics.LastResponseReceived,
			LastResponseSent:     state.Statistics.LastResponseSent,
			CurrentInterval:      state.Statistics.CurrentInterval,
			IsActive:             state.Statistics.IsActive,
		}
		state.mutex.RUnlock()
	}
	
	return stats
}

// generateFrameID generates a unique frame ID
func (c *ControlPlaneHeartbeatSystemImpl) generateFrameID() uint64 {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	
	frameID := c.frameIDGenerator
	c.frameIDGenerator++
	return frameID
}

// Shutdown gracefully shuts down the control heartbeat system
func (c *ControlPlaneHeartbeatSystemImpl) Shutdown() error {
	c.cancel()
	
	// Stop all sessions
	c.mutex.Lock()
	sessionIDs := make([]string, 0, len(c.sessions))
	for sessionID := range c.sessions {
		sessionIDs = append(sessionIDs, sessionID)
	}
	c.mutex.Unlock()
	
	for _, sessionID := range sessionIDs {
		c.mutex.RLock()
		state := c.sessions[sessionID]
		c.mutex.RUnlock()
		
		if state != nil {
			c.StopControlHeartbeats(sessionID, state.PathID)
		}
	}
	
	// Wait for all routines to finish
	c.wg.Wait()
	
	c.logger.Info("Control heartbeat system shutdown complete")
	
	return nil
}