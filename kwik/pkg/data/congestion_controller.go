package data

import (
	"math"
	"sync"
	"time"

	"kwik/internal/utils"
	"kwik/proto/data"
)

// CongestionController handles per-path congestion control with efficient ACK processing
// Implements Requirement 11.4
type CongestionController struct {
	config *AckManagerConfig
	
	// Congestion control state per path
	pathStates map[string]*CongestionControlState
	mutex      sync.RWMutex
}

// CongestionControlState maintains congestion control state for a path
type CongestionControlState struct {
	PathID           string
	CongestionWindow uint64
	SlowStartThreshold uint64
	BytesInFlight    uint64
	RTT              time.Duration
	RTTVariance      time.Duration
	MinRTT           time.Duration
	
	// Congestion control algorithm state
	Algorithm        CongestionAlgorithm
	SlowStartPhase   bool
	CongestionAvoidancePhase bool
	FastRecoveryPhase bool
	
	// ACK processing state
	LastAckTime      time.Time
	AckCount         uint64
	BytesAcked       uint64
	DuplicateAcks    uint64
	
	// Loss detection integration
	PacketsLost      uint64
	LastLossTime     time.Time
	LossRecoveryTime time.Duration
	
	// Performance metrics
	Throughput       float64 // bytes per second
	PacketLossRate   float64
	AverageRTT       time.Duration
	
	// Configuration
	MaxCongestionWindow uint64
	MinCongestionWindow uint64
	GrowthFactor        float64
	ReductionFactor     float64
	
	mutex sync.RWMutex
}

// CongestionAlgorithm defines the congestion control algorithm
type CongestionAlgorithm int

const (
	CongestionAlgorithmReno CongestionAlgorithm = iota
	CongestionAlgorithmCubic
	CongestionAlgorithmBBR
	CongestionAlgorithmNewReno
)

// NewCongestionController creates a new congestion controller
func NewCongestionController(config *AckManagerConfig) *CongestionController {
	return &CongestionController{
		config:     config,
		pathStates: make(map[string]*CongestionControlState),
	}
}

// NewCongestionControlState creates a new congestion control state for a path
func NewCongestionControlState(pathID string, config *AckManagerConfig) *CongestionControlState {
	return &CongestionControlState{
		PathID:                   pathID,
		CongestionWindow:         config.InitialCongestionWindow,
		SlowStartThreshold:       config.MaxCongestionWindow / 2,
		BytesInFlight:           0,
		Algorithm:               CongestionAlgorithmReno, // Default to Reno
		SlowStartPhase:          true,
		CongestionAvoidancePhase: false,
		FastRecoveryPhase:       false,
		MaxCongestionWindow:     config.MaxCongestionWindow,
		MinCongestionWindow:     config.MinCongestionWindow,
		GrowthFactor:           config.CongestionWindowGrowth,
		ReductionFactor:        0.5, // Standard TCP reduction
		MinRTT:                 time.Duration(math.MaxInt64),
	}
}

// RegisterPath registers a path for congestion control
func (cc *CongestionController) RegisterPath(pathID string, config *AckManagerConfig) error {
	cc.mutex.Lock()
	defer cc.mutex.Unlock()
	
	if _, exists := cc.pathStates[pathID]; exists {
		return utils.NewKwikError(utils.ErrInvalidFrame, "path already registered for congestion control", nil)
	}
	
	cc.pathStates[pathID] = NewCongestionControlState(pathID, config)
	return nil
}

// UnregisterPath unregisters a path from congestion control
func (cc *CongestionController) UnregisterPath(pathID string) error {
	cc.mutex.Lock()
	defer cc.mutex.Unlock()
	
	delete(cc.pathStates, pathID)
	return nil
}

// ProcessAck processes an ACK and updates congestion control state
func (cc *CongestionController) ProcessAck(state *CongestionControlState, ackFrame *data.AckFrame, ackedPackets []uint64) error {
	if state == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "congestion control state is nil", nil)
	}
	
	state.mutex.Lock()
	defer state.mutex.Unlock()
	
	now := time.Now()
	
	// Update RTT if ACK delay is provided
	if ackFrame.AckDelay > 0 {
		ackDelay := time.Duration(ackFrame.AckDelay) * time.Microsecond
		cc.updateRTT(state, ackDelay)
	}
	
	// Calculate bytes acknowledged
	bytesAcked := cc.calculateBytesAcked(ackedPackets)
	
	// Update ACK processing metrics
	state.LastAckTime = now
	state.AckCount++
	state.BytesAcked += bytesAcked
	state.BytesInFlight = cc.calculateBytesInFlight(state, ackedPackets)
	
	// Update congestion window based on current phase
	switch {
	case state.FastRecoveryPhase:
		cc.processFastRecoveryAck(state, bytesAcked)
	case state.SlowStartPhase:
		cc.processSlowStartAck(state, bytesAcked)
	case state.CongestionAvoidancePhase:
		cc.processCongestionAvoidanceAck(state, bytesAcked)
	}
	
	// Update throughput estimate
	cc.updateThroughput(state, bytesAcked, now)
	
	return nil
}

// UpdateCongestionWindow updates the congestion window based on ACK
func (cc *CongestionController) UpdateCongestionWindow(state *CongestionControlState, ackFrame *data.AckFrame) error {
	if state == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "congestion control state is nil", nil)
	}
	
	// This is a simplified version - the full processing is done in ProcessAck
	state.mutex.Lock()
	defer state.mutex.Unlock()
	
	// Ensure congestion window is within bounds
	if state.CongestionWindow > state.MaxCongestionWindow {
		state.CongestionWindow = state.MaxCongestionWindow
	}
	if state.CongestionWindow < state.MinCongestionWindow {
		state.CongestionWindow = state.MinCongestionWindow
	}
	
	return nil
}

// OnPacketLoss handles packet loss events
func (cc *CongestionController) OnPacketLoss(pathID string, lostPackets []uint64) error {
	cc.mutex.RLock()
	state, exists := cc.pathStates[pathID]
	cc.mutex.RUnlock()
	
	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}
	
	state.mutex.Lock()
	defer state.mutex.Unlock()
	
	now := time.Now()
	
	// Update loss statistics
	state.PacketsLost += uint64(len(lostPackets))
	state.LastLossTime = now
	
	// Calculate packet loss rate
	if state.AckCount > 0 {
		state.PacketLossRate = float64(state.PacketsLost) / float64(state.AckCount)
	}
	
	// Handle congestion response based on algorithm
	switch state.Algorithm {
	case CongestionAlgorithmReno:
		cc.handleRenoLoss(state)
	case CongestionAlgorithmCubic:
		cc.handleCubicLoss(state)
	case CongestionAlgorithmBBR:
		cc.handleBBRLoss(state)
	case CongestionAlgorithmNewReno:
		cc.handleNewRenoLoss(state)
	}
	
	return nil
}

// updateRTT updates RTT estimates
func (cc *CongestionController) updateRTT(state *CongestionControlState, sampleRTT time.Duration) {
	if sampleRTT <= 0 {
		return
	}
	
	// Update minimum RTT
	if sampleRTT < state.MinRTT {
		state.MinRTT = sampleRTT
	}
	
	// Update smoothed RTT using exponential weighted moving average
	if state.RTT == 0 {
		// First RTT sample
		state.RTT = sampleRTT
		state.RTTVariance = sampleRTT / 2
	} else {
		// RFC 6298 RTT estimation
		alpha := 0.125
		beta := 0.25
		
		rttDiff := sampleRTT - state.RTT
		state.RTT = time.Duration(float64(state.RTT) + alpha*float64(rttDiff))
		
		if rttDiff < 0 {
			rttDiff = -rttDiff
		}
		state.RTTVariance = time.Duration(float64(state.RTTVariance)*(1-beta) + beta*float64(rttDiff))
	}
	
	// Update average RTT
	state.AverageRTT = state.RTT
}

// calculateBytesAcked calculates the number of bytes acknowledged
func (cc *CongestionController) calculateBytesAcked(ackedPackets []uint64) uint64 {
	// Simplified calculation - in a real implementation, this would
	// look up the actual packet sizes
	return uint64(len(ackedPackets)) * 1460 // Assume typical MTU
}

// calculateBytesInFlight calculates the number of bytes currently in flight
func (cc *CongestionController) calculateBytesInFlight(state *CongestionControlState, ackedPackets []uint64) uint64 {
	// Simplified calculation - reduce by acknowledged bytes
	bytesAcked := cc.calculateBytesAcked(ackedPackets)
	if state.BytesInFlight > bytesAcked {
		return state.BytesInFlight - bytesAcked
	}
	return 0
}

// processSlowStartAck processes ACK during slow start phase
func (cc *CongestionController) processSlowStartAck(state *CongestionControlState, bytesAcked uint64) {
	// In slow start, increase congestion window by bytes acknowledged
	state.CongestionWindow += bytesAcked
	
	// Check if we should exit slow start
	if state.CongestionWindow >= state.SlowStartThreshold {
		state.SlowStartPhase = false
		state.CongestionAvoidancePhase = true
	}
	
	// Ensure we don't exceed maximum
	if state.CongestionWindow > state.MaxCongestionWindow {
		state.CongestionWindow = state.MaxCongestionWindow
		state.SlowStartPhase = false
		state.CongestionAvoidancePhase = true
	}
}

// processCongestionAvoidanceAck processes ACK during congestion avoidance phase
func (cc *CongestionController) processCongestionAvoidanceAck(state *CongestionControlState, bytesAcked uint64) {
	// In congestion avoidance, increase congestion window more conservatively
	// Standard TCP: cwnd += MSS * MSS / cwnd
	mss := uint64(1460) // Maximum segment size
	increase := (mss * mss) / state.CongestionWindow
	if increase == 0 {
		increase = 1 // Ensure some progress
	}
	
	state.CongestionWindow += increase
	
	// Ensure we don't exceed maximum
	if state.CongestionWindow > state.MaxCongestionWindow {
		state.CongestionWindow = state.MaxCongestionWindow
	}
}

// processFastRecoveryAck processes ACK during fast recovery phase
func (cc *CongestionController) processFastRecoveryAck(state *CongestionControlState, bytesAcked uint64) {
	// In fast recovery, inflate congestion window for each ACK
	state.CongestionWindow += bytesAcked
	
	// Check if we can exit fast recovery
	// This would typically be when we receive an ACK for new data
	// For simplicity, we'll exit after a timeout
	if time.Since(state.LastLossTime) > state.RTT*2 {
		state.FastRecoveryPhase = false
		state.CongestionAvoidancePhase = true
		// Set congestion window to slow start threshold
		state.CongestionWindow = state.SlowStartThreshold
	}
}

// handleRenoLoss handles packet loss using TCP Reno algorithm
func (cc *CongestionController) handleRenoLoss(state *CongestionControlState) {
	// Set slow start threshold to half of current congestion window
	state.SlowStartThreshold = state.CongestionWindow / 2
	if state.SlowStartThreshold < state.MinCongestionWindow {
		state.SlowStartThreshold = state.MinCongestionWindow
	}
	
	// Enter fast recovery
	state.FastRecoveryPhase = true
	state.SlowStartPhase = false
	state.CongestionAvoidancePhase = false
	
	// Set congestion window to slow start threshold plus 3 * MSS
	state.CongestionWindow = state.SlowStartThreshold + 3*1460
}

// handleCubicLoss handles packet loss using CUBIC algorithm
func (cc *CongestionController) handleCubicLoss(state *CongestionControlState) {
	// CUBIC loss handling - simplified version
	state.SlowStartThreshold = state.CongestionWindow / 2
	if state.SlowStartThreshold < state.MinCongestionWindow {
		state.SlowStartThreshold = state.MinCongestionWindow
	}
	
	// CUBIC uses a different window reduction factor
	state.CongestionWindow = uint64(float64(state.CongestionWindow) * 0.7) // 30% reduction
	
	state.FastRecoveryPhase = true
	state.SlowStartPhase = false
	state.CongestionAvoidancePhase = false
}

// handleBBRLoss handles packet loss using BBR algorithm
func (cc *CongestionController) handleBBRLoss(state *CongestionControlState) {
	// BBR doesn't react to packet loss the same way as loss-based algorithms
	// It focuses on bandwidth and RTT measurements
	// For simplicity, we'll do minimal adjustment
	state.CongestionWindow = uint64(float64(state.CongestionWindow) * 0.9) // 10% reduction
	
	if state.CongestionWindow < state.MinCongestionWindow {
		state.CongestionWindow = state.MinCongestionWindow
	}
}

// handleNewRenoLoss handles packet loss using TCP New Reno algorithm
func (cc *CongestionController) handleNewRenoLoss(state *CongestionControlState) {
	// New Reno is similar to Reno but with improved fast recovery
	cc.handleRenoLoss(state) // Use same basic logic
}

// updateThroughput updates throughput estimate
func (cc *CongestionController) updateThroughput(state *CongestionControlState, bytesAcked uint64, now time.Time) {
	if state.LastAckTime.IsZero() {
		state.LastAckTime = now
		return
	}
	
	timeDiff := now.Sub(state.LastAckTime)
	if timeDiff > 0 {
		// Calculate instantaneous throughput
		instantThroughput := float64(bytesAcked) / timeDiff.Seconds()
		
		// Update average throughput using exponential weighted moving average
		alpha := 0.1
		if state.Throughput == 0 {
			state.Throughput = instantThroughput
		} else {
			state.Throughput = state.Throughput*(1-alpha) + instantThroughput*alpha
		}
	}
}

// GetCongestionWindow returns the current congestion window size
func (cc *CongestionController) GetCongestionWindow(pathID string) (uint64, error) {
	cc.mutex.RLock()
	state, exists := cc.pathStates[pathID]
	cc.mutex.RUnlock()
	
	if !exists {
		return 0, utils.NewPathNotFoundError(pathID)
	}
	
	state.mutex.RLock()
	defer state.mutex.RUnlock()
	
	return state.CongestionWindow, nil
}

// GetCongestionStats returns congestion control statistics for a path
func (cc *CongestionController) GetCongestionStats(pathID string) (*CongestionStats, error) {
	cc.mutex.RLock()
	state, exists := cc.pathStates[pathID]
	cc.mutex.RUnlock()
	
	if !exists {
		return nil, utils.NewPathNotFoundError(pathID)
	}
	
	state.mutex.RLock()
	defer state.mutex.RUnlock()
	
	return &CongestionStats{
		PathID:               state.PathID,
		CongestionWindow:     state.CongestionWindow,
		SlowStartThreshold:   state.SlowStartThreshold,
		BytesInFlight:       state.BytesInFlight,
		RTT:                 state.RTT,
		MinRTT:              state.MinRTT,
		Throughput:          state.Throughput,
		PacketLossRate:      state.PacketLossRate,
		Algorithm:           state.Algorithm,
		SlowStartPhase:      state.SlowStartPhase,
		CongestionAvoidancePhase: state.CongestionAvoidancePhase,
		FastRecoveryPhase:   state.FastRecoveryPhase,
	}, nil
}

// CongestionStats contains congestion control statistics
type CongestionStats struct {
	PathID                   string
	CongestionWindow         uint64
	SlowStartThreshold       uint64
	BytesInFlight           uint64
	RTT                     time.Duration
	MinRTT                  time.Duration
	Throughput              float64
	PacketLossRate          float64
	Algorithm               CongestionAlgorithm
	SlowStartPhase          bool
	CongestionAvoidancePhase bool
	FastRecoveryPhase       bool
}

// SetAlgorithm sets the congestion control algorithm for a path
func (cc *CongestionController) SetAlgorithm(pathID string, algorithm CongestionAlgorithm) error {
	cc.mutex.RLock()
	state, exists := cc.pathStates[pathID]
	cc.mutex.RUnlock()
	
	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}
	
	state.mutex.Lock()
	defer state.mutex.Unlock()
	
	state.Algorithm = algorithm
	return nil
}

// CanSend checks if we can send more data based on congestion window
func (cc *CongestionController) CanSend(pathID string, dataSize uint64) (bool, error) {
	cc.mutex.RLock()
	state, exists := cc.pathStates[pathID]
	cc.mutex.RUnlock()
	
	if !exists {
		return false, utils.NewPathNotFoundError(pathID)
	}
	
	state.mutex.RLock()
	defer state.mutex.RUnlock()
	
	return state.BytesInFlight+dataSize <= state.CongestionWindow, nil
}