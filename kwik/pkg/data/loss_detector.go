package data

import (
	"sync"
	"time"

	"kwik/internal/utils"
)

// LossDetector handles fast loss detection and retransmission triggering
// Implements Requirement 11.3
type LossDetector struct {
	config *AckManagerConfig
	
	// Detection state per path
	pathStates map[string]*LossDetectionState
	mutex      sync.RWMutex
}

// LossDetectionState maintains loss detection state for a path
type LossDetectionState struct {
	PathID                string
	LastPacketSent        uint64
	LastPacketAcked       uint64
	PacketsSentNotAcked   map[uint64]*SentPacketInfo
	DuplicateAckCount     map[uint64]int
	FastRetransmitThreshold int
	LossDetectionTimeout  time.Duration
	ReorderingThreshold   uint64
	TimeThreshold         time.Duration
	
	// Statistics
	PacketsLost           uint64
	FastRetransmits       uint64
	TimeoutRetransmits    uint64
	FalsePositives        uint64
	
	mutex sync.RWMutex
}

// NewLossDetector creates a new loss detector
func NewLossDetector(config *AckManagerConfig) *LossDetector {
	return &LossDetector{
		config:     config,
		pathStates: make(map[string]*LossDetectionState),
	}
}

// NewLossDetectionState creates a new loss detection state for a path
func NewLossDetectionState(pathID string, config *AckManagerConfig) *LossDetectionState {
	return &LossDetectionState{
		PathID:                  pathID,
		LastPacketSent:          0,
		LastPacketAcked:         0,
		PacketsSentNotAcked:     make(map[uint64]*SentPacketInfo),
		DuplicateAckCount:       make(map[uint64]int),
		FastRetransmitThreshold: config.FastRetransmitThreshold,
		LossDetectionTimeout:    config.LossDetectionTimeout,
		ReorderingThreshold:     3, // Standard value
		TimeThreshold:           config.LossDetectionTimeout,
	}
}

// RegisterPath registers a path for loss detection
func (ld *LossDetector) RegisterPath(pathID string, config *AckManagerConfig) error {
	ld.mutex.Lock()
	defer ld.mutex.Unlock()
	
	if _, exists := ld.pathStates[pathID]; exists {
		return utils.NewKwikError(utils.ErrInvalidFrame, "path already registered for loss detection", nil)
	}
	
	ld.pathStates[pathID] = NewLossDetectionState(pathID, config)
	return nil
}

// UnregisterPath unregisters a path from loss detection
func (ld *LossDetector) UnregisterPath(pathID string) error {
	ld.mutex.Lock()
	defer ld.mutex.Unlock()
	
	delete(ld.pathStates, pathID)
	return nil
}

// OnPacketSent records a packet that was sent
func (ld *LossDetector) OnPacketSent(pathID string, packetID uint64, size uint32) error {
	ld.mutex.RLock()
	state, exists := ld.pathStates[pathID]
	ld.mutex.RUnlock()
	
	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}
	
	state.mutex.Lock()
	defer state.mutex.Unlock()
	
	// Record the sent packet
	state.PacketsSentNotAcked[packetID] = &SentPacketInfo{
		PacketNumber:  packetID,
		SentTime:      time.Now().UnixNano(),
		PacketSize:    size,
		Acknowledged:  false,
		Retransmitted: false,
	}
	
	// Update last packet sent
	if packetID > state.LastPacketSent {
		state.LastPacketSent = packetID
	}
	
	return nil
}

// OnPacketAcked records a packet that was acknowledged
func (ld *LossDetector) OnPacketAcked(pathID string, packetID uint64) error {
	ld.mutex.RLock()
	state, exists := ld.pathStates[pathID]
	ld.mutex.RUnlock()
	
	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}
	
	state.mutex.Lock()
	defer state.mutex.Unlock()
	
	// Remove from unacknowledged packets
	if sentInfo, exists := state.PacketsSentNotAcked[packetID]; exists {
		sentInfo.Acknowledged = true
		sentInfo.AckTime = time.Now()
		delete(state.PacketsSentNotAcked, packetID)
	}
	
	// Update last packet acked
	if packetID > state.LastPacketAcked {
		state.LastPacketAcked = packetID
	}
	
	// Reset duplicate ACK count for this packet
	delete(state.DuplicateAckCount, packetID)
	
	return nil
}

// OnDuplicateAck records a duplicate ACK
func (ld *LossDetector) OnDuplicateAck(pathID string, packetID uint64) error {
	ld.mutex.RLock()
	state, exists := ld.pathStates[pathID]
	ld.mutex.RUnlock()
	
	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}
	
	state.mutex.Lock()
	defer state.mutex.Unlock()
	
	// Increment duplicate ACK count
	state.DuplicateAckCount[packetID]++
	
	return nil
}

// DetectLoss detects lost packets using fast retransmit and timeout mechanisms
func (ld *LossDetector) DetectLoss(state *LossDetectionState) ([]uint64, error) {
	if state == nil {
		return nil, utils.NewKwikError(utils.ErrInvalidFrame, "loss detection state is nil", nil)
	}
	
	state.mutex.Lock()
	defer state.mutex.Unlock()
	
	lostPackets := make([]uint64, 0)
	now := time.Now()
	
	// Fast retransmit detection (based on duplicate ACKs)
	fastRetransmitLoss := ld.detectFastRetransmitLoss(state)
	lostPackets = append(lostPackets, fastRetransmitLoss...)
	
	// Timeout-based loss detection
	timeoutLoss := ld.detectTimeoutLoss(state, now)
	lostPackets = append(lostPackets, timeoutLoss...)
	
	// Reordering-based loss detection
	reorderingLoss := ld.detectReorderingLoss(state)
	lostPackets = append(lostPackets, reorderingLoss...)
	
	// Update statistics
	if len(lostPackets) > 0 {
		state.PacketsLost += uint64(len(lostPackets))
		
		// Categorize the type of loss detection
		if len(fastRetransmitLoss) > 0 {
			state.FastRetransmits += uint64(len(fastRetransmitLoss))
		}
		if len(timeoutLoss) > 0 {
			state.TimeoutRetransmits += uint64(len(timeoutLoss))
		}
	}
	
	return ld.removeDuplicates(lostPackets), nil
}

// detectFastRetransmitLoss detects loss based on duplicate ACKs (fast retransmit)
func (ld *LossDetector) detectFastRetransmitLoss(state *LossDetectionState) []uint64 {
	lostPackets := make([]uint64, 0)
	
	// Check for packets that have received enough duplicate ACKs
	for packetID, dupCount := range state.DuplicateAckCount {
		if dupCount >= state.FastRetransmitThreshold {
			// This packet is likely lost
			if _, exists := state.PacketsSentNotAcked[packetID]; exists {
				lostPackets = append(lostPackets, packetID)
				
				// Remove from tracking to avoid duplicate detection
				delete(state.PacketsSentNotAcked, packetID)
				delete(state.DuplicateAckCount, packetID)
			}
		}
	}
	
	return lostPackets
}

// detectTimeoutLoss detects loss based on timeout
func (ld *LossDetector) detectTimeoutLoss(state *LossDetectionState, now time.Time) []uint64 {
	lostPackets := make([]uint64, 0)
	
	// Check for packets that have timed out
	for packetID, sentInfo := range state.PacketsSentNotAcked {
		sentTime := time.Unix(0, sentInfo.SentTime)
		if now.Sub(sentTime) > state.LossDetectionTimeout {
			// This packet has timed out
			lostPackets = append(lostPackets, packetID)
			
			// Remove from tracking to avoid duplicate detection
			delete(state.PacketsSentNotAcked, packetID)
		}
	}
	
	return lostPackets
}

// detectReorderingLoss detects loss based on packet reordering threshold
func (ld *LossDetector) detectReorderingLoss(state *LossDetectionState) []uint64 {
	lostPackets := make([]uint64, 0)
	
	// If we have acknowledged packets beyond the reordering threshold,
	// consider earlier unacknowledged packets as lost
	if state.LastPacketAcked > state.ReorderingThreshold {
		lossThreshold := state.LastPacketAcked - state.ReorderingThreshold
		
		for packetID := range state.PacketsSentNotAcked {
			if packetID < lossThreshold {
				// This packet is likely lost due to reordering
				lostPackets = append(lostPackets, packetID)
				
				// Remove from tracking to avoid duplicate detection
				delete(state.PacketsSentNotAcked, packetID)
			}
		}
	}
	
	return lostPackets
}

// removeDuplicates removes duplicate packet IDs from the lost packets list
func (ld *LossDetector) removeDuplicates(packets []uint64) []uint64 {
	if len(packets) == 0 {
		return packets
	}
	
	seen := make(map[uint64]bool)
	result := make([]uint64, 0)
	
	for _, packetID := range packets {
		if !seen[packetID] {
			seen[packetID] = true
			result = append(result, packetID)
		}
	}
	
	return result
}

// UpdateTimeThreshold updates the time threshold for loss detection
func (ld *LossDetector) UpdateTimeThreshold(pathID string, rtt time.Duration) error {
	ld.mutex.RLock()
	state, exists := ld.pathStates[pathID]
	ld.mutex.RUnlock()
	
	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}
	
	state.mutex.Lock()
	defer state.mutex.Unlock()
	
	// Update time threshold based on RTT (typically 9/8 * RTT)
	state.TimeThreshold = time.Duration(float64(rtt) * 1.125)
	
	// Ensure minimum threshold
	if state.TimeThreshold < 10*time.Millisecond {
		state.TimeThreshold = 10 * time.Millisecond
	}
	
	// Update loss detection timeout
	state.LossDetectionTimeout = state.TimeThreshold * 2
	
	return nil
}

// GetLossStats returns loss detection statistics for a path
func (ld *LossDetector) GetLossStats(pathID string) (*LossDetectionStats, error) {
	ld.mutex.RLock()
	state, exists := ld.pathStates[pathID]
	ld.mutex.RUnlock()
	
	if !exists {
		return nil, utils.NewPathNotFoundError(pathID)
	}
	
	state.mutex.RLock()
	defer state.mutex.RUnlock()
	
	return &LossDetectionStats{
		PathID:             state.PathID,
		PacketsLost:        state.PacketsLost,
		FastRetransmits:    state.FastRetransmits,
		TimeoutRetransmits: state.TimeoutRetransmits,
		FalsePositives:     state.FalsePositives,
		UnackedPackets:     uint64(len(state.PacketsSentNotAcked)),
		TimeThreshold:      state.TimeThreshold,
		ReorderingThreshold: state.ReorderingThreshold,
	}, nil
}

// LossDetectionStats contains loss detection statistics
type LossDetectionStats struct {
	PathID              string
	PacketsLost         uint64
	FastRetransmits     uint64
	TimeoutRetransmits  uint64
	FalsePositives      uint64
	UnackedPackets      uint64
	TimeThreshold       time.Duration
	ReorderingThreshold uint64
}

// IsPacketLost checks if a specific packet is considered lost
func (ld *LossDetector) IsPacketLost(pathID string, packetID uint64) (bool, error) {
	ld.mutex.RLock()
	state, exists := ld.pathStates[pathID]
	ld.mutex.RUnlock()
	
	if !exists {
		return false, utils.NewPathNotFoundError(pathID)
	}
	
	state.mutex.RLock()
	defer state.mutex.RUnlock()
	
	// Check if packet is in unacknowledged list
	sentInfo, exists := state.PacketsSentNotAcked[packetID]
	if !exists {
		// Packet is either acknowledged or already considered lost
		return false, nil
	}
	
	now := time.Now()
	
	// Check timeout condition
	sentTime := time.Unix(0, sentInfo.SentTime)
	if now.Sub(sentTime) > state.LossDetectionTimeout {
		return true, nil
	}
	
	// Check duplicate ACK condition
	if dupCount, exists := state.DuplicateAckCount[packetID]; exists {
		if dupCount >= state.FastRetransmitThreshold {
			return true, nil
		}
	}
	
	// Check reordering condition
	if state.LastPacketAcked > state.ReorderingThreshold {
		lossThreshold := state.LastPacketAcked - state.ReorderingThreshold
		if packetID < lossThreshold {
			return true, nil
		}
	}
	
	return false, nil
}