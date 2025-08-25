package data

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
	UpdateStreamActivity(streamID uint64, pathID string, lastActivity time.Time) error
	UpdateFailureCount(pathID string, consecutiveFailures int) error
}

// DataPlaneHeartbeatSystem manages heartbeats on the data plane
type DataPlaneHeartbeatSystem interface {
	// Start data plane heartbeats for a stream
	StartDataHeartbeats(streamID uint64, pathID string) error

	// Stop data plane heartbeats for a stream
	StopDataHeartbeats(streamID uint64, pathID string) error

	// Send a data heartbeat request
	SendDataHeartbeat(streamID uint64, pathID string) error

	// Handle received data heartbeat request
	HandleDataHeartbeatRequest(request *protocol.HeartbeatFrame) error

	// Handle received data heartbeat response
	HandleDataHeartbeatResponse(response *protocol.HeartbeatResponseFrame) error

	// Update stream activity (resets heartbeat timer)
	UpdateStreamActivity(streamID uint64, pathID string) error

	// Get data heartbeat statistics
	GetDataHeartbeatStats(streamID uint64, pathID string) *DataHeartbeatStats

	// Set server mode
	SetServerMode(isServer bool)

	// Shutdown the system
	Shutdown() error
}

// DataHeartbeatStats contains statistics for data plane heartbeats
type DataHeartbeatStats struct {
	StreamID             uint64
	PathID               string
	HeartbeatsSent       uint64
	HeartbeatsReceived   uint64
	ResponsesReceived    uint64
	ResponsesSent        uint64
	ConsecutiveFailures  int
	LastDataActivity     time.Time
	LastHeartbeatSent    time.Time
	LastResponseReceived time.Time
	CurrentInterval      time.Duration
	StreamActive         bool
}

// DataPlaneHeartbeatSystemImpl implements DataPlaneHeartbeatSystem
type DataPlaneHeartbeatSystemImpl struct {
	// Configuration
	config *DataHeartbeatConfig

	// State management
	streams map[uint64]*DataHeartbeatState // streamID -> state

	// Data plane integration
	dataPlane DataPlane

	// Heartbeat scheduling
	scheduler HeartbeatScheduler

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

// DataHeartbeatState tracks heartbeat state for a data stream
type DataHeartbeatState struct {
	StreamID            uint64
	PathID              string
	Active              bool
	NextSequenceID      uint64
	PendingRequests     map[uint64]*PendingDataHeartbeatRequest
	LastDataActivity    time.Time
	LastSent            time.Time
	LastReceived        time.Time
	ConsecutiveFailures int
	Statistics          *DataHeartbeatStats

	// Timing
	HeartbeatInterval time.Duration
	ActivityThreshold time.Duration
	HeartbeatTimer    *time.Timer

	// Synchronization
	mutex sync.RWMutex
}

// PendingDataHeartbeatRequest tracks pending data heartbeat requests
type PendingDataHeartbeatRequest struct {
	SequenceID uint64
	SentAt     time.Time
	Timeout    time.Time
	RetryCount int
	MaxRetries int
}

// DataHeartbeatConfig contains configuration for data plane heartbeats
type DataHeartbeatConfig struct {
	DefaultInterval   time.Duration
	MinInterval       time.Duration
	MaxInterval       time.Duration
	ActivityThreshold time.Duration // Time without data activity before sending heartbeat
	TimeoutMultiplier float64
	MaxRetries        int
}

// Logger interface for data heartbeat system logging
type Logger interface {
	Debug(msg string, keysAndValues ...interface{})
	Info(msg string, keysAndValues ...interface{})
	Warn(msg string, keysAndValues ...interface{})
	Error(msg string, keysAndValues ...interface{})
}

// DefaultDataHeartbeatConfig returns default configuration
func DefaultDataHeartbeatConfig() *DataHeartbeatConfig {
	return &DataHeartbeatConfig{
		DefaultInterval:   60 * time.Second,
		MinInterval:       10 * time.Second,
		MaxInterval:       300 * time.Second,
		ActivityThreshold: 30 * time.Second, // Send heartbeat if no data activity for 30s
		TimeoutMultiplier: 3.0,
		MaxRetries:        3,
	}
}

// NewDataPlaneHeartbeatSystem creates a new data plane heartbeat system
func NewDataPlaneHeartbeatSystem(
	dataPlane DataPlane,
	config *DataHeartbeatConfig,
	logger Logger,
) *DataPlaneHeartbeatSystemImpl {
	if config == nil {
		config = DefaultDataHeartbeatConfig()
	}

	if logger == nil {
		logger = &DefaultDataLogger{}
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &DataPlaneHeartbeatSystemImpl{
		config:    config,
		streams:   make(map[uint64]*DataHeartbeatState),
		dataPlane: dataPlane,
		isServer:  false,
		ctx:       ctx,
		cancel:    cancel,
		logger:    logger,
	}
}

// DefaultDataLogger provides a simple logger implementation
type DefaultDataLogger struct{}

func (d *DefaultDataLogger) Debug(msg string, keysAndValues ...interface{}) {}
func (d *DefaultDataLogger) Info(msg string, keysAndValues ...interface{})  {}
func (d *DefaultDataLogger) Warn(msg string, keysAndValues ...interface{})  {}
func (d *DefaultDataLogger) Error(msg string, keysAndValues ...interface{}) {}

// SetServerMode sets whether this system is running in server mode
func (d *DataPlaneHeartbeatSystemImpl) SetServerMode(isServer bool) {
	d.mutex.Lock()
	defer d.mutex.Unlock()

	d.isServer = isServer
	d.logger.Info("Data heartbeat system mode set", "isServer", isServer)
}

// SetHeartbeatScheduler sets the heartbeat scheduler
func (d *DataPlaneHeartbeatSystemImpl) SetHeartbeatScheduler(scheduler HeartbeatScheduler) {
	d.mutex.Lock()
	defer d.mutex.Unlock()

	d.scheduler = scheduler
	d.logger.Info("Heartbeat scheduler set for data heartbeat system")
}

// StartDataHeartbeats starts data plane heartbeats for a stream
func (d *DataPlaneHeartbeatSystemImpl) StartDataHeartbeats(streamID uint64, pathID string) error {
	d.mutex.Lock()
	defer d.mutex.Unlock()

	// Check if stream already exists
	if _, exists := d.streams[streamID]; exists {
		return fmt.Errorf("data heartbeats already started for stream %d", streamID)
	}

	// Create heartbeat state
	state := &DataHeartbeatState{
		StreamID:          streamID,
		PathID:            pathID,
		Active:            true,
		NextSequenceID:    1,
		PendingRequests:   make(map[uint64]*PendingDataHeartbeatRequest),
		LastDataActivity:  time.Now(),
		HeartbeatInterval: d.config.DefaultInterval,
		ActivityThreshold: d.config.ActivityThreshold,
		Statistics: &DataHeartbeatStats{
			StreamID:        streamID,
			PathID:          pathID,
			CurrentInterval: d.config.DefaultInterval,
			StreamActive:    true,
		},
	}

	d.streams[streamID] = state

	// Start heartbeat routine for client mode only
	if !d.isServer {
		d.wg.Add(1)
		go d.dataHeartbeatRoutine(streamID, state)
	}

	d.logger.Info("Data heartbeats started",
		"streamID", streamID,
		"pathID", pathID,
		"isServer", d.isServer)

	return nil
}

// StopDataHeartbeats stops data plane heartbeats for a stream
func (d *DataPlaneHeartbeatSystemImpl) StopDataHeartbeats(streamID uint64, pathID string) error {
	d.mutex.Lock()
	defer d.mutex.Unlock()

	state, exists := d.streams[streamID]
	if !exists {
		return fmt.Errorf("no data heartbeats found for stream %d", streamID)
	}

	// Stop the heartbeat routine
	state.mutex.Lock()
	state.Active = false
	if state.HeartbeatTimer != nil {
		state.HeartbeatTimer.Stop()
	}
	state.mutex.Unlock()

	// Remove from map
	delete(d.streams, streamID)

	d.logger.Info("Data heartbeats stopped", "streamID", streamID, "pathID", pathID)

	return nil
}

// SendDataHeartbeat sends a data heartbeat request
func (d *DataPlaneHeartbeatSystemImpl) SendDataHeartbeat(streamID uint64, pathID string) error {
	d.mutex.RLock()
	state, exists := d.streams[streamID]
	d.mutex.RUnlock()

	if !exists {
		return fmt.Errorf("no heartbeat state found for stream %d", streamID)
	}

	state.mutex.Lock()
	defer state.mutex.Unlock()

	if !state.Active {
		return fmt.Errorf("heartbeats not active for stream %d", streamID)
	}

	// Generate sequence ID
	sequenceID := state.NextSequenceID
	state.NextSequenceID++

	// Create heartbeat frame
	frameID := d.generateFrameID()

	// Send frame via data plane (simplified - would need actual data plane integration)
	// For now, just log that we would send it
	d.logger.Debug("Data heartbeat would be sent",
		"streamID", streamID,
		"pathID", pathID,
		"sequenceID", sequenceID,
		"frameID", frameID)

	// Track pending request
	now := time.Now()
	timeout := now.Add(time.Duration(float64(state.HeartbeatInterval) * d.config.TimeoutMultiplier))

	state.PendingRequests[sequenceID] = &PendingDataHeartbeatRequest{
		SequenceID: sequenceID,
		SentAt:     now,
		Timeout:    timeout,
		RetryCount: 0,
		MaxRetries: d.config.MaxRetries,
	}

	// Update statistics
	state.LastSent = now
	state.Statistics.HeartbeatsSent++
	state.Statistics.LastHeartbeatSent = now

	return nil
}

// HandleDataHeartbeatRequest handles received data heartbeat requests
func (d *DataPlaneHeartbeatSystemImpl) HandleDataHeartbeatRequest(request *protocol.HeartbeatFrame) error {
	if request.PlaneType != protocol.HeartbeatPlaneData {
		return fmt.Errorf("expected data plane heartbeat, got %d", request.PlaneType)
	}

	if request.StreamID == nil {
		return fmt.Errorf("data plane heartbeat must have stream ID")
	}

	streamID := *request.StreamID

	// Find stream state
	d.mutex.RLock()
	state, exists := d.streams[streamID]
	d.mutex.RUnlock()

	if exists {
		// Update statistics
		state.mutex.Lock()
		now := time.Now()
		state.LastReceived = now
		state.Statistics.HeartbeatsReceived++
		state.Statistics.LastDataActivity = now // Heartbeat counts as activity
		state.mutex.Unlock()
	}

	// Send response (servers always respond to heartbeat requests)
	if d.isServer {
		err := d.sendDataHeartbeatResponse(request)
		if err != nil {
			d.logger.Error("Failed to send data heartbeat response",
				"streamID", streamID,
				"pathID", request.PathID,
				"sequenceID", request.SequenceID,
				"error", err)
			return fmt.Errorf("failed to send data heartbeat response: %w", err)
		}
	}

	d.logger.Debug("Data heartbeat request handled",
		"streamID", streamID,
		"pathID", request.PathID,
		"sequenceID", request.SequenceID,
		"isServer", d.isServer)

	return nil
}

// HandleDataHeartbeatResponse handles received data heartbeat responses
func (d *DataPlaneHeartbeatSystemImpl) HandleDataHeartbeatResponse(response *protocol.HeartbeatResponseFrame) error {
	if response.PlaneType != protocol.HeartbeatPlaneData {
		return fmt.Errorf("expected data plane heartbeat response, got %d", response.PlaneType)
	}

	if response.StreamID == nil {
		return fmt.Errorf("data plane heartbeat response must have stream ID")
	}

	streamID := *response.StreamID

	// Find stream state
	d.mutex.RLock()
	state, exists := d.streams[streamID]
	d.mutex.RUnlock()

	if !exists {
		return fmt.Errorf("no heartbeat state found for stream %d", streamID)
	}

	state.mutex.Lock()
	defer state.mutex.Unlock()

	// Find and remove pending request
	_, exists = state.PendingRequests[response.RequestSequenceID]
	if !exists {
		d.logger.Warn("Received response for unknown heartbeat request",
			"streamID", streamID,
			"pathID", response.PathID,
			"requestSequenceID", response.RequestSequenceID)
		return nil // Not an error, might be a late response
	}

	delete(state.PendingRequests, response.RequestSequenceID)

	// Reset consecutive failures
	state.ConsecutiveFailures = 0

	// Update statistics
	now := time.Now()
	state.LastReceived = now
	state.Statistics.ResponsesReceived++
	state.Statistics.LastResponseReceived = now

	d.logger.Debug("Data heartbeat response handled",
		"streamID", streamID,
		"pathID", response.PathID,
		"requestSequenceID", response.RequestSequenceID,
		"serverLoad", response.ServerLoad)

	return nil
}

// UpdateStreamActivity updates stream activity timestamp
func (d *DataPlaneHeartbeatSystemImpl) UpdateStreamActivity(streamID uint64, pathID string) error {
	d.mutex.RLock()
	state, exists := d.streams[streamID]
	d.mutex.RUnlock()

	if !exists {
		return nil // Stream not being monitored, not an error
	}

	state.mutex.Lock()
	defer state.mutex.Unlock()

	now := time.Now()
	state.LastDataActivity = now
	state.Statistics.LastDataActivity = now

	// Update scheduler with stream activity
	if d.scheduler != nil {
		err := d.scheduler.UpdateStreamActivity(streamID, pathID, now)
		if err != nil {
			d.logger.Warn("Failed to update stream activity in scheduler",
				"streamID", streamID, "pathID", pathID, "error", err)
		}
	}

	return nil
}

// GetDataHeartbeatStats returns heartbeat statistics for a stream
func (d *DataPlaneHeartbeatSystemImpl) GetDataHeartbeatStats(streamID uint64, pathID string) *DataHeartbeatStats {
	d.mutex.RLock()
	state, exists := d.streams[streamID]
	d.mutex.RUnlock()

	if !exists {
		return nil
	}

	state.mutex.RLock()
	defer state.mutex.RUnlock()

	// Return a copy of the statistics
	return &DataHeartbeatStats{
		StreamID:             state.Statistics.StreamID,
		PathID:               state.Statistics.PathID,
		HeartbeatsSent:       state.Statistics.HeartbeatsSent,
		HeartbeatsReceived:   state.Statistics.HeartbeatsReceived,
		ResponsesReceived:    state.Statistics.ResponsesReceived,
		ResponsesSent:        state.Statistics.ResponsesSent,
		ConsecutiveFailures:  state.ConsecutiveFailures,
		LastDataActivity:     state.Statistics.LastDataActivity,
		LastHeartbeatSent:    state.Statistics.LastHeartbeatSent,
		LastResponseReceived: state.Statistics.LastResponseReceived,
		CurrentInterval:      state.Statistics.CurrentInterval,
		StreamActive:         state.Statistics.StreamActive,
	}
}

// sendDataHeartbeatResponse sends a heartbeat response
func (d *DataPlaneHeartbeatSystemImpl) sendDataHeartbeatResponse(request *protocol.HeartbeatFrame) error {
	// Generate response frame
	frameID := d.generateFrameID()
	responseFrame := &protocol.HeartbeatResponseFrame{
		FrameID:           frameID,
		RequestSequenceID: request.SequenceID,
		PathID:            request.PathID,
		PlaneType:         protocol.HeartbeatPlaneData,
		StreamID:          request.StreamID,
		Timestamp:         time.Now(),
		RequestTimestamp:  request.Timestamp,
		ServerLoad:        0.5, // Simplified server load
	}

	// Send response via data plane (simplified - would need actual data plane integration)
	// For now, just log that we would send it
	d.logger.Debug("Data heartbeat response would be sent",
		"streamID", *request.StreamID,
		"pathID", request.PathID,
		"requestSequenceID", request.SequenceID,
		"serverLoad", responseFrame.ServerLoad)

	// Update statistics if we have state for this stream
	if request.StreamID != nil {
		d.mutex.RLock()
		state, exists := d.streams[*request.StreamID]
		d.mutex.RUnlock()

		if exists {
			state.mutex.Lock()
			state.Statistics.ResponsesSent++
			state.Statistics.LastResponseReceived = time.Now()
			state.mutex.Unlock()
		}
	}
	//TODO: Implementent the real working importantly
	return nil
}

// dataHeartbeatRoutine runs the heartbeat loop for a stream (client mode only)
func (d *DataPlaneHeartbeatSystemImpl) dataHeartbeatRoutine(streamID uint64, state *DataHeartbeatState) {
	defer d.wg.Done()

	state.mutex.Lock()
	state.HeartbeatTimer = time.NewTimer(state.ActivityThreshold)
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
		case <-d.ctx.Done():
			return
		case <-state.HeartbeatTimer.C:
			state.mutex.RLock()
			active := state.Active
			pathID := state.PathID
			lastActivity := state.LastDataActivity
			activityThreshold := state.ActivityThreshold
			state.mutex.RUnlock()

			if !active {
				return
			}

			// Check if we need to send a heartbeat (no recent data activity)
			if time.Since(lastActivity) >= activityThreshold {
				// Send heartbeat
				err := d.SendDataHeartbeat(streamID, pathID)
				if err != nil {
					d.logger.Error("Failed to send scheduled data heartbeat",
						"streamID", streamID,
						"pathID", pathID,
						"error", err)

					// Increment failure count
					state.mutex.Lock()
					state.ConsecutiveFailures++
					consecutiveFailures := state.ConsecutiveFailures
					state.mutex.Unlock()

					// Update scheduler with failure count
					if d.scheduler != nil {
						err := d.scheduler.UpdateFailureCount(pathID, consecutiveFailures)
						if err != nil {
							d.logger.Warn("Failed to update failure count in scheduler after send failure",
								"streamID", streamID, "pathID", pathID, "error", err)
						}
					}
				}
			}

			// Check for timeouts
			d.checkDataHeartbeatTimeouts(streamID, state)

			// Calculate adaptive interval and schedule next check
			state.mutex.Lock()
			if state.Active && state.HeartbeatTimer != nil {
				// Get adaptive interval from scheduler for data plane
				var nextInterval time.Duration
				if d.scheduler != nil {
					nextInterval = d.scheduler.CalculateHeartbeatInterval(pathID, protocol.HeartbeatPlaneData, &streamID)

					// For data plane, we use the interval as activity threshold
					if nextInterval != state.ActivityThreshold {
						d.logger.Debug("Adaptive data interval update",
							"streamID", streamID,
							"pathID", pathID,
							"oldThreshold", state.ActivityThreshold,
							"newThreshold", nextInterval)

						state.ActivityThreshold = nextInterval
						state.Statistics.CurrentInterval = nextInterval
					}
				} else {
					nextInterval = state.ActivityThreshold
				}

				state.HeartbeatTimer.Reset(nextInterval)
			}
			state.mutex.Unlock()
		}
	}
}

// checkDataHeartbeatTimeouts checks for and handles heartbeat timeouts
func (d *DataPlaneHeartbeatSystemImpl) checkDataHeartbeatTimeouts(streamID uint64, state *DataHeartbeatState) {
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

		d.logger.Warn("Data heartbeat timeout",
			"streamID", streamID,
			"pathID", state.PathID,
			"sequenceID", sequenceID,
			"consecutiveFailures", state.ConsecutiveFailures)

		// Update scheduler with failure count
		if d.scheduler != nil {
			err := d.scheduler.UpdateFailureCount(state.PathID, state.ConsecutiveFailures)
			if err != nil {
				d.logger.Warn("Failed to update failure count in scheduler after timeout",
					"streamID", streamID, "pathID", state.PathID, "error", err)
			}
		}
	}
}

// generateFrameID generates a unique frame ID (simplified)
func (d *DataPlaneHeartbeatSystemImpl) generateFrameID() uint64 {
	return uint64(time.Now().UnixNano())
}

// Shutdown gracefully shuts down the data heartbeat system
func (d *DataPlaneHeartbeatSystemImpl) Shutdown() error {
	d.cancel()

	// Stop all streams
	d.mutex.Lock()
	streamIDs := make([]uint64, 0, len(d.streams))
	for streamID := range d.streams {
		streamIDs = append(streamIDs, streamID)
	}
	d.mutex.Unlock()

	for _, streamID := range streamIDs {
		d.mutex.RLock()
		state := d.streams[streamID]
		d.mutex.RUnlock()

		if state != nil {
			d.StopDataHeartbeats(streamID, state.PathID)
		}
	}

	// Wait for all routines to finish
	d.wg.Wait()

	d.logger.Info("Data heartbeat system shutdown complete")

	return nil
}
