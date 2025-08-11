package data

import (
	"sync"
	"time"

	"kwik/internal/utils"
)

// AckCoordinator handles coordination between paths to prevent ACK interference
// Implements Requirement 11.5
type AckCoordinator struct {
	config *AckManagerConfig
	
	// Coordination state
	activePaths     map[string]*PathCoordinationState
	coordinationMutex sync.RWMutex
	
	// ACK scheduling
	ackQueue        chan *AckRequest
	priorityQueues  map[AckPriority]chan *AckRequest
	
	// Coordination metrics
	coordinationEvents uint64
	lastCoordination   time.Time
	
	mutex sync.RWMutex
}

// PathCoordinationState tracks coordination state for a path
type PathCoordinationState struct {
	PathID           string
	LastAckTime      time.Time
	PendingAcks      int
	AckRate          float64  // ACKs per second
	InterferenceLevel float64 // 0.0 to 1.0
	Priority         AckPriority
	Characteristics  *PathCharacteristics
}

// AckRequest represents a request to send an ACK
type AckRequest struct {
	PathID    string
	PacketIDs []uint64
	Priority  AckPriority
	Timestamp time.Time
	Callback  func(error)
}

// NewAckCoordinator creates a new ACK coordinator
func NewAckCoordinator(config *AckManagerConfig) *AckCoordinator {
	coordinator := &AckCoordinator{
		config:         config,
		activePaths:    make(map[string]*PathCoordinationState),
		ackQueue:       make(chan *AckRequest, config.BufferSize),
		priorityQueues: make(map[AckPriority]chan *AckRequest),
	}
	
	// Initialize priority queues
	for priority := AckPriorityLow; priority <= AckPriorityUrgent; priority++ {
		coordinator.priorityQueues[priority] = make(chan *AckRequest, config.BufferSize/config.AckPriorityLevels)
	}
	
	return coordinator
}

// RegisterPath registers a path for coordination
func (ac *AckCoordinator) RegisterPath(pathID string, characteristics *PathCharacteristics) error {
	ac.coordinationMutex.Lock()
	defer ac.coordinationMutex.Unlock()
	
	if _, exists := ac.activePaths[pathID]; exists {
		return utils.NewKwikError(utils.ErrInvalidFrame, "path already registered for coordination", nil)
	}
	
	// Determine initial priority based on path characteristics
	priority := ac.determinePriority(characteristics)
	
	ac.activePaths[pathID] = &PathCoordinationState{
		PathID:          pathID,
		LastAckTime:     time.Now(),
		PendingAcks:     0,
		AckRate:         0.0,
		InterferenceLevel: 0.0,
		Priority:        priority,
		Characteristics: characteristics,
	}
	
	return nil
}

// UnregisterPath unregisters a path from coordination
func (ac *AckCoordinator) UnregisterPath(pathID string) error {
	ac.coordinationMutex.Lock()
	defer ac.coordinationMutex.Unlock()
	
	delete(ac.activePaths, pathID)
	return nil
}

// CoordinateAcks performs ACK coordination across all paths
func (ac *AckCoordinator) CoordinateAcks() error {
	ac.coordinationMutex.Lock()
	defer ac.coordinationMutex.Unlock()
	
	now := time.Now()
	
	// Update coordination metrics
	ac.mutex.Lock()
	ac.coordinationEvents++
	ac.lastCoordination = now
	ac.mutex.Unlock()
	
	// Calculate interference levels
	ac.calculateInterferenceLevels()
	
	// Adjust ACK priorities based on interference
	ac.adjustAckPriorities()
	
	// Schedule pending ACKs
	return ac.schedulePendingAcks()
}

// ScheduleAck schedules an ACK with the specified priority
func (ac *AckCoordinator) ScheduleAck(pathID string, priority AckPriority) error {
	ac.coordinationMutex.RLock()
	pathState, exists := ac.activePaths[pathID]
	ac.coordinationMutex.RUnlock()
	
	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}
	
	// Update pending ACK count
	pathState.PendingAcks++
	
	// Create ACK request
	request := &AckRequest{
		PathID:    pathID,
		Priority:  priority,
		Timestamp: time.Now(),
	}
	
	// Add to appropriate priority queue
	select {
	case ac.priorityQueues[priority] <- request:
		return nil
	default:
		// Queue is full, try lower priority queue
		if priority > AckPriorityLow {
			return ac.ScheduleAck(pathID, priority-1)
		}
		return utils.NewKwikError(utils.ErrInvalidFrame, "ACK queue is full", nil)
	}
}

// determinePriority determines the initial priority for a path based on its characteristics
func (ac *AckCoordinator) determinePriority(characteristics *PathCharacteristics) AckPriority {
	// High-priority paths: low latency, high bandwidth, reliable
	if !characteristics.IsHighLatency && !characteristics.IsLowBandwidth && !characteristics.IsUnreliable {
		return AckPriorityHigh
	}
	
	// Low-priority paths: high latency, low bandwidth, or unreliable
	if characteristics.IsHighLatency || characteristics.IsLowBandwidth || characteristics.IsUnreliable {
		return AckPriorityLow
	}
	
	// Normal priority for everything else
	return AckPriorityNormal
}

// calculateInterferenceLevels calculates interference levels between paths
func (ac *AckCoordinator) calculateInterferenceLevels() {
	now := time.Now()
	
	for pathID, pathState := range ac.activePaths {
		// Calculate ACK rate
		timeSinceLastAck := now.Sub(pathState.LastAckTime)
		if timeSinceLastAck > 0 {
			pathState.AckRate = float64(pathState.PendingAcks) / timeSinceLastAck.Seconds()
		}
		
		// Calculate interference level based on:
		// 1. Number of other active paths
		// 2. ACK rates of other paths
		// 3. Path characteristics similarity
		
		interferenceLevel := 0.0
		activePathCount := len(ac.activePaths)
		
		if activePathCount > 1 {
			// Base interference from multiple paths
			interferenceLevel = float64(activePathCount-1) / 10.0
			
			// Additional interference from high-rate paths
			for otherPathID, otherPathState := range ac.activePaths {
				if otherPathID != pathID {
					if otherPathState.AckRate > 10.0 { // More than 10 ACKs per second
						interferenceLevel += 0.1
					}
					
					// Interference from similar characteristics (competing for same resources)
					if ac.pathsHaveSimilarCharacteristics(pathState.Characteristics, otherPathState.Characteristics) {
						interferenceLevel += 0.2
					}
				}
			}
		}
		
		// Cap interference level at 1.0
		if interferenceLevel > 1.0 {
			interferenceLevel = 1.0
		}
		
		pathState.InterferenceLevel = interferenceLevel
	}
}

// pathsHaveSimilarCharacteristics checks if two paths have similar characteristics
func (ac *AckCoordinator) pathsHaveSimilarCharacteristics(char1, char2 *PathCharacteristics) bool {
	// Consider paths similar if they have similar RTT and bandwidth characteristics
	rttSimilar := false
	if char1.RTT > 0 && char2.RTT > 0 {
		rttRatio := float64(char1.RTT) / float64(char2.RTT)
		rttSimilar = rttRatio > 0.5 && rttRatio < 2.0 // Within 2x of each other
	}
	
	bandwidthSimilar := false
	if char1.Bandwidth > 0 && char2.Bandwidth > 0 {
		bandwidthRatio := float64(char1.Bandwidth) / float64(char2.Bandwidth)
		bandwidthSimilar = bandwidthRatio > 0.5 && bandwidthRatio < 2.0
	}
	
	return rttSimilar && bandwidthSimilar
}

// adjustAckPriorities adjusts ACK priorities based on interference levels
func (ac *AckCoordinator) adjustAckPriorities() {
	for _, pathState := range ac.activePaths {
		originalPriority := pathState.Priority
		
		// Reduce priority for high-interference paths
		if pathState.InterferenceLevel > 0.7 {
			if pathState.Priority > AckPriorityLow {
				pathState.Priority--
			}
		} else if pathState.InterferenceLevel < 0.3 {
			// Increase priority for low-interference paths
			if pathState.Priority < AckPriorityUrgent {
				pathState.Priority++
			}
		}
		
		// Special handling for unreliable paths - always keep them at lower priority
		if pathState.Characteristics.IsUnreliable && pathState.Priority > AckPriorityNormal {
			pathState.Priority = AckPriorityNormal
		}
		
		// Log priority changes (in a real implementation, this would use proper logging)
		if pathState.Priority != originalPriority {
			// Priority changed due to interference
		}
	}
}

// schedulePendingAcks schedules pending ACKs based on priorities and coordination
func (ac *AckCoordinator) schedulePendingAcks() error {
	// Process ACKs in priority order
	priorities := []AckPriority{AckPriorityUrgent, AckPriorityHigh, AckPriorityNormal, AckPriorityLow}
	
	processedCount := 0
	maxProcessed := ac.config.MaxConcurrentAcks
	
	for _, priority := range priorities {
		if processedCount >= maxProcessed {
			break
		}
		
		// Process ACKs from this priority queue
		for processedCount < maxProcessed {
			select {
			case request := <-ac.priorityQueues[priority]:
				// Check if we should delay this ACK to reduce interference
				if ac.shouldDelayAck(request) {
					// Put it back in the queue for later processing
					select {
					case ac.priorityQueues[priority] <- request:
					default:
						// Queue is full, drop the request
					}
					continue
				}
				
				// Process the ACK request
				ac.processAckRequest(request)
				processedCount++
				
			default:
				// No more ACKs in this priority queue
				break
			}
		}
	}
	
	return nil
}

// shouldDelayAck determines if an ACK should be delayed to reduce interference
func (ac *AckCoordinator) shouldDelayAck(request *AckRequest) bool {
	pathState, exists := ac.activePaths[request.PathID]
	if !exists {
		return false
	}
	
	// Delay ACKs from high-interference paths
	if pathState.InterferenceLevel > 0.8 {
		// Check if enough time has passed since last ACK
		timeSinceLastAck := time.Since(pathState.LastAckTime)
		minInterval := time.Duration(float64(ac.config.CoordinationInterval) * (1.0 + pathState.InterferenceLevel))
		
		return timeSinceLastAck < minInterval
	}
	
	return false
}

// processAckRequest processes an ACK request
func (ac *AckCoordinator) processAckRequest(request *AckRequest) {
	pathState, exists := ac.activePaths[request.PathID]
	if !exists {
		if request.Callback != nil {
			request.Callback(utils.NewPathNotFoundError(request.PathID))
		}
		return
	}
	
	// Update path state
	pathState.LastAckTime = time.Now()
	pathState.PendingAcks--
	if pathState.PendingAcks < 0 {
		pathState.PendingAcks = 0
	}
	
	// In a real implementation, this would trigger the actual ACK sending
	if request.Callback != nil {
		request.Callback(nil)
	}
}

// GetCoordinationStats returns coordination statistics
func (ac *AckCoordinator) GetCoordinationStats() (uint64, time.Time) {
	ac.mutex.RLock()
	defer ac.mutex.RUnlock()
	
	return ac.coordinationEvents, ac.lastCoordination
}

// GetPathInterferenceLevel returns the interference level for a path
func (ac *AckCoordinator) GetPathInterferenceLevel(pathID string) (float64, error) {
	ac.coordinationMutex.RLock()
	defer ac.coordinationMutex.RUnlock()
	
	pathState, exists := ac.activePaths[pathID]
	if !exists {
		return 0.0, utils.NewPathNotFoundError(pathID)
	}
	
	return pathState.InterferenceLevel, nil
}