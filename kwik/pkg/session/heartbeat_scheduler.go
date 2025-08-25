package session

import (
	"fmt"
	"math"
	"sync"
	"time"

	"kwik/pkg/protocol"
)
// SchedulerLogger provides a simple logger implementation
type SchedulerLogger struct{}

func (d *SchedulerLogger) Debug(msg string, keysAndValues ...interface{}) {}
func (d *SchedulerLogger) Info(msg string, keysAndValues ...interface{})  {}
func (d *SchedulerLogger) Warn(msg string, keysAndValues ...interface{})  {}
func (d *SchedulerLogger) Error(msg string, keysAndValues ...interface{}) {}

// HeartbeatScheduler manages adaptive heartbeat interval calculation
type HeartbeatScheduler interface {
	// Calculate optimal heartbeat interval
	CalculateHeartbeatInterval(pathID string, planeType protocol.HeartbeatPlaneType, streamID *uint64) time.Duration
	
	// Update network conditions
	UpdateNetworkConditions(pathID string, rtt time.Duration, packetLoss float64) error
	
	// Update stream activity
	UpdateStreamActivity(streamID uint64, pathID string, lastActivity time.Time) error
	
	// Update failure count for a path
	UpdateFailureCount(pathID string, consecutiveFailures int) error
	
	// Get current scheduling parameters
	GetSchedulingParams(pathID string) *HeartbeatSchedulingParams
	
	// Set scheduling configuration
	SetSchedulingConfig(config *HeartbeatSchedulingConfig) error
	
	// Shutdown the scheduler
	Shutdown() error
}

// HeartbeatSchedulingParams contains current scheduling parameters for a path
type HeartbeatSchedulingParams struct {
	PathID                    string
	BaseInterval              time.Duration
	CurrentControlInterval    time.Duration
	CurrentDataInterval       time.Duration
	AdaptationFactor          float64
	NetworkConditionScore     float64
	RecentFailureCount        int
	LastAdaptation           time.Time
}

// HeartbeatSchedulingConfig contains configuration for adaptive scheduling
type HeartbeatSchedulingConfig struct {
	// Base intervals
	MinControlInterval        time.Duration
	MaxControlInterval        time.Duration
	MinDataInterval          time.Duration
	MaxDataInterval          time.Duration
	
	// Adaptation parameters
	RTTMultiplier            float64
	FailureAdaptationFactor  float64
	RecoveryAdaptationFactor float64
	ActivityThreshold        time.Duration
	
	// Network condition thresholds
	GoodNetworkThreshold     float64
	PoorNetworkThreshold     float64
}

// HeartbeatSchedulingState tracks adaptation history and current state
type HeartbeatSchedulingState struct {
	PathID                string
	CurrentControlInterval time.Duration
	CurrentDataInterval   time.Duration
	NextControlHeartbeat  time.Time
	NextDataHeartbeat     map[uint64]time.Time // Per stream
	AdaptationHistory     []HeartbeatAdaptation
	LastAdaptation 		time.Time
	// Network metrics
	NetworkMetrics        NetworkMetrics
	NetworkConditionScore float64
	
	// Stream activity tracking
	StreamActivity        map[uint64]time.Time
	
	// Failure tracking
	RecentFailures        []time.Time
	ConsecutiveFailures   int
	
	// Synchronization
	mutex sync.RWMutex
}

// HeartbeatAdaptation records an adaptation event
type HeartbeatAdaptation struct {
	Timestamp       time.Time
	OldInterval     time.Duration
	NewInterval     time.Duration
	Reason          string
	NetworkMetrics  NetworkMetrics
}

// NetworkMetrics contains network performance metrics
type NetworkMetrics struct {
	RTT         time.Duration
	PacketLoss  float64
	Jitter      time.Duration
	Bandwidth   uint64
	Congestion  float64
}

// RTTTrend indicates the trend of RTT measurements
type RTTTrend int

const (
	RTTTrendStable RTTTrend = iota
	RTTTrendImproving
	RTTTrendDegrading
)

// HeartbeatSchedulerImpl implements HeartbeatScheduler
type HeartbeatSchedulerImpl struct {
	// Configuration
	config *HeartbeatSchedulingConfig
	
	// State management per path
	pathStates map[string]*HeartbeatSchedulingState
	
	// Synchronization
	mutex sync.RWMutex
	
	// Logging
	logger *SchedulerLogger
}

// DefaultHeartbeatSchedulingConfig returns default configuration
func DefaultHeartbeatSchedulingConfig() *HeartbeatSchedulingConfig {
	return &HeartbeatSchedulingConfig{
		// Base intervals
		MinControlInterval:        5 * time.Second,
		MaxControlInterval:        120 * time.Second,
		MinDataInterval:          10 * time.Second,
		MaxDataInterval:          300 * time.Second,
		
		// Adaptation parameters
		RTTMultiplier:            3.0,
		FailureAdaptationFactor:  0.5,  // Reduce interval by 50% on failures
		RecoveryAdaptationFactor: 1.2,  // Increase interval by 20% on recovery
		ActivityThreshold:        30 * time.Second,
		
		// Network condition thresholds
		GoodNetworkThreshold:     0.8,  // 80% score is considered good
		PoorNetworkThreshold:     0.3,  // 30% score is considered poor
	}
}

// NewHeartbeatScheduler creates a new heartbeat scheduler
func NewHeartbeatScheduler(config *HeartbeatSchedulingConfig, logger *SchedulerLogger) *HeartbeatSchedulerImpl {
	if config == nil {
		config = DefaultHeartbeatSchedulingConfig()
	}
	
	if logger == nil {
		logger = &SchedulerLogger{}
	}
	
	return &HeartbeatSchedulerImpl{
		config:     config,
		pathStates: make(map[string]*HeartbeatSchedulingState),
		logger:     logger,
	}
}


// CalculateHeartbeatInterval calculates optimal heartbeat interval for a path and plane type
func (s *HeartbeatSchedulerImpl) CalculateHeartbeatInterval(pathID string, planeType protocol.HeartbeatPlaneType, streamID *uint64) time.Duration {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	
	// Get or create path state
	pathState, exists := s.pathStates[pathID]
	if !exists {
		pathState = s.createPathState(pathID)
		s.pathStates[pathID] = pathState
	}
	
	pathState.mutex.Lock()
	defer pathState.mutex.Unlock()
	
	var baseInterval, minInterval, maxInterval time.Duration
	
	// Set base intervals based on plane type
	switch planeType {
	case protocol.HeartbeatPlaneControl:
		baseInterval = (s.config.MinControlInterval + s.config.MaxControlInterval) / 2
		minInterval = s.config.MinControlInterval
		maxInterval = s.config.MaxControlInterval
	case protocol.HeartbeatPlaneData:
		baseInterval = (s.config.MinDataInterval + s.config.MaxDataInterval) / 2
		minInterval = s.config.MinDataInterval
		maxInterval = s.config.MaxDataInterval
		
		// For data plane, consider stream activity
		if streamID != nil {
			if lastActivity, exists := pathState.StreamActivity[*streamID]; exists {
				timeSinceActivity := time.Since(lastActivity)
				if timeSinceActivity < s.config.ActivityThreshold {
					// Stream is active, use longer intervals
					baseInterval = time.Duration(float64(baseInterval) * 1.5)
				}
			}
		}
	default:
		s.logger.Warn("Unknown plane type for heartbeat scheduling", "planeType", planeType)
		return 30 * time.Second // Default fallback
	}
	
	// Apply network condition adaptations
	adaptedInterval := s.adaptForNetworkConditions(baseInterval, pathState)
	
	// Apply failure-based adaptations
	adaptedInterval = s.adaptForFailures(adaptedInterval, pathState)
	
	// Apply RTT-based adaptations
	adaptedInterval = s.adaptForRTT(adaptedInterval, pathState)
	
	// Ensure interval is within bounds
	if adaptedInterval < minInterval {
		adaptedInterval = minInterval
	} else if adaptedInterval > maxInterval {
		adaptedInterval = maxInterval
	}
	
	// Update current intervals in state
	switch planeType {
	case protocol.HeartbeatPlaneControl:
		if pathState.CurrentControlInterval != adaptedInterval {
			s.recordAdaptation(pathState, pathState.CurrentControlInterval, adaptedInterval, "network_and_failure_adaptation")
			pathState.CurrentControlInterval = adaptedInterval
		}
	case protocol.HeartbeatPlaneData:
		if pathState.CurrentDataInterval != adaptedInterval {
			s.recordAdaptation(pathState, pathState.CurrentDataInterval, adaptedInterval, "network_and_failure_adaptation")
			pathState.CurrentDataInterval = adaptedInterval
		}
	}
	
	s.logger.Debug("Calculated heartbeat interval", 
		"pathID", pathID,
		"planeType", planeType,
		"streamID", streamID,
		"interval", adaptedInterval,
		"networkScore", pathState.NetworkConditionScore,
		"failures", pathState.ConsecutiveFailures)
	
	return adaptedInterval
}

// UpdateNetworkConditions updates network condition metrics for a path
func (s *HeartbeatSchedulerImpl) UpdateNetworkConditions(pathID string, rtt time.Duration, packetLoss float64) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	
	// Get or create path state
	pathState, exists := s.pathStates[pathID]
	if !exists {
		pathState = s.createPathState(pathID)
		s.pathStates[pathID] = pathState
	}
	
	pathState.mutex.Lock()
	defer pathState.mutex.Unlock()
	
	// Update network metrics
	pathState.NetworkMetrics.RTT = rtt
	pathState.NetworkMetrics.PacketLoss = packetLoss
	
	// Calculate network condition score (0.0 to 1.0)
	pathState.NetworkConditionScore = s.calculateNetworkScore(rtt, packetLoss)
	
	s.logger.Debug("Updated network conditions", 
		"pathID", pathID,
		"rtt", rtt,
		"packetLoss", packetLoss,
		"networkScore", pathState.NetworkConditionScore)
	
	return nil
}

// UpdateStreamActivity updates stream activity timestamp for data plane optimization
func (s *HeartbeatSchedulerImpl) UpdateStreamActivity(streamID uint64, pathID string, lastActivity time.Time) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	
	// Get or create path state
	pathState, exists := s.pathStates[pathID]
	if !exists {
		pathState = s.createPathState(pathID)
		s.pathStates[pathID] = pathState
	}
	
	pathState.mutex.Lock()
	defer pathState.mutex.Unlock()
	
	// Update stream activity
	pathState.StreamActivity[streamID] = lastActivity
	
	s.logger.Debug("Updated stream activity", 
		"streamID", streamID,
		"pathID", pathID,
		"lastActivity", lastActivity)
	
	return nil
}

// GetSchedulingParams returns current scheduling parameters for a path
func (s *HeartbeatSchedulerImpl) GetSchedulingParams(pathID string) *HeartbeatSchedulingParams {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	
	pathState, exists := s.pathStates[pathID]
	if !exists {
		return nil
	}
	
	pathState.mutex.RLock()
	defer pathState.mutex.RUnlock()
	
	baseInterval := (s.config.MinControlInterval + s.config.MaxControlInterval) / 2
	
	return &HeartbeatSchedulingParams{
		PathID:                    pathID,
		BaseInterval:              baseInterval,
		CurrentControlInterval:    pathState.CurrentControlInterval,
		CurrentDataInterval:       pathState.CurrentDataInterval,
		AdaptationFactor:          s.calculateAdaptationFactor(pathState),
		NetworkConditionScore:     pathState.NetworkConditionScore,
		RecentFailureCount:        len(pathState.RecentFailures),
		LastAdaptation:           s.getLastAdaptationTime(pathState),
	}
}

// SetSchedulingConfig updates the scheduling configuration
func (s *HeartbeatSchedulerImpl) SetSchedulingConfig(config *HeartbeatSchedulingConfig) error {
	if config == nil {
		return fmt.Errorf("config cannot be nil")
	}
	
	s.mutex.Lock()
	defer s.mutex.Unlock()
	
	s.config = config
	
	s.logger.Info("Updated scheduling configuration", 
		"minControlInterval", config.MinControlInterval,
		"maxControlInterval", config.MaxControlInterval,
		"minDataInterval", config.MinDataInterval,
		"maxDataInterval", config.MaxDataInterval)
	
	return nil
}

// Shutdown gracefully shuts down the scheduler
func (s *HeartbeatSchedulerImpl) Shutdown() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	
	// Clear all path states
	s.pathStates = make(map[string]*HeartbeatSchedulingState)
	
	s.logger.Info("Adaptive heartbeat scheduler shutdown complete")
	
	return nil
}

// createPathState creates a new path state with default values
func (s *HeartbeatSchedulerImpl) createPathState(pathID string) *HeartbeatSchedulingState {
	now := time.Now()
	
	return &HeartbeatSchedulingState{
		PathID:                pathID,
		CurrentControlInterval: (s.config.MinControlInterval + s.config.MaxControlInterval) / 2,
		CurrentDataInterval:   (s.config.MinDataInterval + s.config.MaxDataInterval) / 2,
		NextControlHeartbeat:  now,
		NextDataHeartbeat:     make(map[uint64]time.Time),
		AdaptationHistory:     make([]HeartbeatAdaptation, 0, 100),
		NetworkMetrics:        NetworkMetrics{},
		NetworkConditionScore: 1.0, // Start with perfect score
		StreamActivity:        make(map[uint64]time.Time),
		RecentFailures:        make([]time.Time, 0, 50),
		ConsecutiveFailures:   0,
	}
}

// adaptForNetworkConditions adapts interval based on network conditions
func (s *HeartbeatSchedulerImpl) adaptForNetworkConditions(baseInterval time.Duration, pathState *HeartbeatSchedulingState) time.Duration {
	score := pathState.NetworkConditionScore
	
	if score >= s.config.GoodNetworkThreshold {
		// Good network conditions - use longer intervals
		return time.Duration(float64(baseInterval) * (1.0 + (score-s.config.GoodNetworkThreshold)*2.0))
	} else if score <= s.config.PoorNetworkThreshold {
		// Poor network conditions - use shorter intervals for faster detection
		return time.Duration(float64(baseInterval) * (0.5 + score*0.5))
	}
	
	// Medium network conditions - use base interval with slight adjustment
	return time.Duration(float64(baseInterval) * (0.8 + score*0.4))
}

// adaptForFailures adapts interval based on recent failures
func (s *HeartbeatSchedulerImpl) adaptForFailures(baseInterval time.Duration, pathState *HeartbeatSchedulingState) time.Duration {
	if pathState.ConsecutiveFailures == 0 {
		return baseInterval
	}
	
	// Reduce interval based on consecutive failures
	failureFactor := math.Pow(s.config.FailureAdaptationFactor, float64(pathState.ConsecutiveFailures))
	return time.Duration(float64(baseInterval) * failureFactor)
}

// adaptForRTT adapts interval based on RTT measurements
func (s *HeartbeatSchedulerImpl) adaptForRTT(baseInterval time.Duration, pathState *HeartbeatSchedulingState) time.Duration {
	rtt := pathState.NetworkMetrics.RTT
	if rtt == 0 {
		return baseInterval
	}
	
	// Ensure interval is at least RTT * multiplier
	minRTTInterval := time.Duration(float64(rtt) * s.config.RTTMultiplier)
	
	if baseInterval < minRTTInterval {
		return minRTTInterval
	}
	
	return baseInterval
}

// calculateNetworkScore calculates a network condition score from 0.0 to 1.0
func (s *HeartbeatSchedulerImpl) calculateNetworkScore(rtt time.Duration, packetLoss float64) float64 {
	// RTT score (0.0 to 1.0, where lower RTT is better)
	rttScore := 1.0
	if rtt > 0 {
		// Normalize RTT: 0-50ms = 1.0, 50-200ms = 0.5-1.0, >200ms = 0.0-0.5
		rttMs := float64(rtt.Milliseconds())
		if rttMs <= 50 {
			rttScore = 1.0
		} else if rttMs <= 200 {
			rttScore = 1.0 - (rttMs-50)/150*0.5
		} else {
			rttScore = 0.5 * math.Exp(-(rttMs-200)/200)
		}
	}
	
	// Packet loss score (0.0 to 1.0, where lower loss is better)
	lossScore := 1.0 - math.Min(packetLoss, 1.0)
	
	// Combined score (weighted average)
	return (rttScore*0.7 + lossScore*0.3)
}

// calculateAdaptationFactor calculates current adaptation factor
func (s *HeartbeatSchedulerImpl) calculateAdaptationFactor(pathState *HeartbeatSchedulingState) float64 {
	// Base adaptation factor
	factor := 1.0
	
	// Adjust based on network conditions
	if pathState.NetworkConditionScore < s.config.PoorNetworkThreshold {
		factor *= 0.5 // More aggressive adaptation for poor conditions
	} else if pathState.NetworkConditionScore > s.config.GoodNetworkThreshold {
		factor *= 1.5 // Less aggressive adaptation for good conditions
	}
	
	// Adjust based on recent failures
	if len(pathState.RecentFailures) > 0 {
		factor *= (1.0 - float64(len(pathState.RecentFailures))*0.1)
	}
	
	return math.Max(0.1, math.Min(2.0, factor))
}

// getLastAdaptationTime returns the timestamp of the last adaptation
func (s *HeartbeatSchedulerImpl) getLastAdaptationTime(pathState *HeartbeatSchedulingState) time.Time {
	if len(pathState.AdaptationHistory) == 0 {
		return time.Time{}
	}
	
	return pathState.AdaptationHistory[len(pathState.AdaptationHistory)-1].Timestamp
}

// recordAdaptation records an adaptation event in the history
func (s *HeartbeatSchedulerImpl) recordAdaptation(pathState *HeartbeatSchedulingState, oldInterval, newInterval time.Duration, reason string) {
	adaptation := HeartbeatAdaptation{
		Timestamp:      time.Now(),
		OldInterval:    oldInterval,
		NewInterval:    newInterval,
		Reason:         reason,
		NetworkMetrics: pathState.NetworkMetrics,
	}
	
	pathState.AdaptationHistory = append(pathState.AdaptationHistory, adaptation)
	
	// Keep only last 100 adaptations
	if len(pathState.AdaptationHistory) > 100 {
		pathState.AdaptationHistory = pathState.AdaptationHistory[1:]
	}
	
	pathState.LastAdaptation = adaptation.Timestamp
}

// UpdateFailureCount updates the failure count for a path (used by heartbeat systems)
func (s *HeartbeatSchedulerImpl) UpdateFailureCount(pathID string, consecutiveFailures int) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	
	pathState, exists := s.pathStates[pathID]
	if !exists {
		pathState = s.createPathState(pathID)
		s.pathStates[pathID] = pathState
	}
	
	pathState.mutex.Lock()
	defer pathState.mutex.Unlock()
	
	pathState.ConsecutiveFailures = consecutiveFailures
	
	// Add to recent failures if this is a new failure
	if consecutiveFailures > 0 {
		now := time.Now()
		pathState.RecentFailures = append(pathState.RecentFailures, now)
		
		// Keep only failures from last 5 minutes
		cutoff := now.Add(-5 * time.Minute)
		var recentFailures []time.Time
		for _, failureTime := range pathState.RecentFailures {
			if failureTime.After(cutoff) {
				recentFailures = append(recentFailures, failureTime)
			}
		}
		pathState.RecentFailures = recentFailures
	}
	
	return nil
}

// GetPathState returns a copy of the path state for debugging/monitoring
func (s *HeartbeatSchedulerImpl) GetPathState(pathID string) *HeartbeatSchedulingState {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	
	pathState, exists := s.pathStates[pathID]
	if !exists {
		return nil
	}
	
	pathState.mutex.RLock()
	defer pathState.mutex.RUnlock()
	
	// Return a copy to avoid race conditions
	stateCopy := &HeartbeatSchedulingState{
		PathID:                pathState.PathID,
		CurrentControlInterval: pathState.CurrentControlInterval,
		CurrentDataInterval:   pathState.CurrentDataInterval,
		NextControlHeartbeat:  pathState.NextControlHeartbeat,
		NextDataHeartbeat:     make(map[uint64]time.Time),
		AdaptationHistory:     make([]HeartbeatAdaptation, len(pathState.AdaptationHistory)),
		NetworkMetrics:        pathState.NetworkMetrics,
		NetworkConditionScore: pathState.NetworkConditionScore,
		StreamActivity:        make(map[uint64]time.Time),
		RecentFailures:        make([]time.Time, len(pathState.RecentFailures)),
		ConsecutiveFailures:   pathState.ConsecutiveFailures,
	}
	
	// Copy maps and slices
	for k, v := range pathState.NextDataHeartbeat {
		stateCopy.NextDataHeartbeat[k] = v
	}
	for k, v := range pathState.StreamActivity {
		stateCopy.StreamActivity[k] = v
	}
	copy(stateCopy.AdaptationHistory, pathState.AdaptationHistory)
	copy(stateCopy.RecentFailures, pathState.RecentFailures)
	
	return stateCopy
}