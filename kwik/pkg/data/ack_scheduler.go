package data

import (
	"container/heap"
	"sync"
	"time"

	"kwik/internal/utils"
)

// AckScheduler handles intelligent ACK scheduling across multiple active paths
// Implements Requirement 11.5
type AckScheduler struct {
	config *AckManagerConfig
	
	// Scheduling queues
	scheduledAcks *AckPriorityQueue
	queueMutex    sync.Mutex
	
	// Path scheduling state
	pathSchedulingState map[string]*PathSchedulingState
	pathsMutex          sync.RWMutex
	
	// Scheduling metrics
	totalScheduled   uint64
	totalProcessed   uint64
	averageDelay     time.Duration
	lastScheduleTime time.Time
	
	mutex sync.RWMutex
}

// PathSchedulingState tracks scheduling state for a path
type PathSchedulingState struct {
	PathID              string
	NextScheduledTime   time.Time
	AckInterval         time.Duration
	BurstAllowance      int
	CurrentBurst        int
	LastAckTime         time.Time
	SchedulingWeight    float64
	Characteristics     *PathCharacteristics
}

// ScheduledAck represents an ACK that has been scheduled for transmission
type ScheduledAck struct {
	PathID        string
	PacketIDs     []uint64
	Priority      AckPriority
	ScheduledTime time.Time
	CreatedTime   time.Time
	Attempts      int
	Index         int // For heap implementation
}

// AckPriorityQueue implements a priority queue for scheduled ACKs
type AckPriorityQueue []*ScheduledAck

// Implement heap.Interface for AckPriorityQueue
func (pq AckPriorityQueue) Len() int { return len(pq) }

func (pq AckPriorityQueue) Less(i, j int) bool {
	// Higher priority ACKs come first
	if pq[i].Priority != pq[j].Priority {
		return pq[i].Priority > pq[j].Priority
	}
	// For same priority, earlier scheduled time comes first
	return pq[i].ScheduledTime.Before(pq[j].ScheduledTime)
}

func (pq AckPriorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].Index = i
	pq[j].Index = j
}

func (pq *AckPriorityQueue) Push(x interface{}) {
	n := len(*pq)
	item := x.(*ScheduledAck)
	item.Index = n
	*pq = append(*pq, item)
}

func (pq *AckPriorityQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil  // avoid memory leak
	item.Index = -1 // for safety
	*pq = old[0 : n-1]
	return item
}

// NewAckScheduler creates a new ACK scheduler
func NewAckScheduler(config *AckManagerConfig) *AckScheduler {
	scheduler := &AckScheduler{
		config:              config,
		scheduledAcks:       &AckPriorityQueue{},
		pathSchedulingState: make(map[string]*PathSchedulingState),
	}
	
	heap.Init(scheduler.scheduledAcks)
	
	return scheduler
}

// RegisterPath registers a path for ACK scheduling
func (as *AckScheduler) RegisterPath(pathID string, characteristics *PathCharacteristics) error {
	as.pathsMutex.Lock()
	defer as.pathsMutex.Unlock()
	
	if _, exists := as.pathSchedulingState[pathID]; exists {
		return utils.NewKwikError(utils.ErrInvalidFrame, "path already registered for scheduling", nil)
	}
	
	// Calculate scheduling parameters based on path characteristics
	ackInterval := as.calculateAckInterval(characteristics)
	burstAllowance := as.calculateBurstAllowance(characteristics)
	schedulingWeight := as.calculateSchedulingWeight(characteristics)
	
	as.pathSchedulingState[pathID] = &PathSchedulingState{
		PathID:            pathID,
		NextScheduledTime: time.Now(),
		AckInterval:       ackInterval,
		BurstAllowance:    burstAllowance,
		CurrentBurst:      0,
		LastAckTime:       time.Time{},
		SchedulingWeight:  schedulingWeight,
		Characteristics:   characteristics,
	}
	
	return nil
}

// UnregisterPath unregisters a path from ACK scheduling
func (as *AckScheduler) UnregisterPath(pathID string) error {
	as.pathsMutex.Lock()
	defer as.pathsMutex.Unlock()
	
	delete(as.pathSchedulingState, pathID)
	
	// Remove any scheduled ACKs for this path
	as.queueMutex.Lock()
	defer as.queueMutex.Unlock()
	
	// Rebuild the queue without ACKs for this path
	newQueue := make([]*ScheduledAck, 0)
	for _, ack := range *as.scheduledAcks {
		if ack.PathID != pathID {
			newQueue = append(newQueue, ack)
		}
	}
	
	*as.scheduledAcks = newQueue
	heap.Init(as.scheduledAcks)
	
	return nil
}

// ScheduleAck schedules an ACK for transmission
func (as *AckScheduler) ScheduleAck(pathID string, packetIDs []uint64, priority AckPriority) error {
	as.pathsMutex.RLock()
	pathState, exists := as.pathSchedulingState[pathID]
	as.pathsMutex.RUnlock()
	
	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}
	
	now := time.Now()
	
	// Calculate scheduled time based on path state and priority
	scheduledTime := as.calculateScheduledTime(pathState, priority, now)
	
	// Create scheduled ACK
	scheduledAck := &ScheduledAck{
		PathID:        pathID,
		PacketIDs:     packetIDs,
		Priority:      priority,
		ScheduledTime: scheduledTime,
		CreatedTime:   now,
		Attempts:      0,
	}
	
	// Add to priority queue
	as.queueMutex.Lock()
	heap.Push(as.scheduledAcks, scheduledAck)
	as.queueMutex.Unlock()
	
	// Update scheduling metrics
	as.mutex.Lock()
	as.totalScheduled++
	as.lastScheduleTime = now
	as.mutex.Unlock()
	
	return nil
}

// ProcessScheduledAcks processes ACKs that are ready for transmission
func (as *AckScheduler) ProcessScheduledAcks() ([]*ScheduledAck, error) {
	as.queueMutex.Lock()
	defer as.queueMutex.Unlock()
	
	now := time.Now()
	readyAcks := make([]*ScheduledAck, 0)
	
	// Process ACKs that are ready for transmission
	for as.scheduledAcks.Len() > 0 {
		// Peek at the highest priority ACK
		topAck := (*as.scheduledAcks)[0]
		
		// Check if it's time to send this ACK
		if topAck.ScheduledTime.After(now) {
			break // Not ready yet
		}
		
		// Remove from queue
		scheduledAck := heap.Pop(as.scheduledAcks).(*ScheduledAck)
		
		// Check if we can send this ACK based on burst limits
		if as.canSendAck(scheduledAck.PathID, now) {
			readyAcks = append(readyAcks, scheduledAck)
			as.updatePathSchedulingState(scheduledAck.PathID, now)
		} else {
			// Reschedule for later
			scheduledAck.ScheduledTime = now.Add(as.config.MinAckDelay)
			scheduledAck.Attempts++
			heap.Push(as.scheduledAcks, scheduledAck)
		}
		
		// Limit the number of ACKs processed in one batch
		if len(readyAcks) >= as.config.MaxConcurrentAcks {
			break
		}
	}
	
	// Update processing metrics
	as.mutex.Lock()
	as.totalProcessed += uint64(len(readyAcks))
	as.mutex.Unlock()
	
	return readyAcks, nil
}

// calculateAckInterval calculates the ACK interval for a path based on its characteristics
func (as *AckScheduler) calculateAckInterval(characteristics *PathCharacteristics) time.Duration {
	baseInterval := as.config.MinAckDelay
	
	// Adjust based on RTT
	if characteristics.RTT > 0 {
		// Use a fraction of RTT as the base interval
		rttBasedInterval := characteristics.RTT / 4
		if rttBasedInterval > baseInterval {
			baseInterval = rttBasedInterval
		}
	}
	
	// Adjust based on packet loss rate
	if characteristics.PacketLossRate > 0.01 { // > 1% loss
		// Reduce interval for lossy paths to send ACKs more frequently
		baseInterval = time.Duration(float64(baseInterval) * (1.0 - characteristics.PacketLossRate))
	}
	
	// Adjust based on bandwidth
	if characteristics.IsLowBandwidth {
		// Increase interval for low bandwidth paths to reduce overhead
		baseInterval = time.Duration(float64(baseInterval) * 1.5)
	}
	
	// Ensure interval is within bounds
	if baseInterval < as.config.MinAckDelay {
		baseInterval = as.config.MinAckDelay
	}
	if baseInterval > as.config.MaxAckDelay {
		baseInterval = as.config.MaxAckDelay
	}
	
	return baseInterval
}

// calculateBurstAllowance calculates the burst allowance for a path
func (as *AckScheduler) calculateBurstAllowance(characteristics *PathCharacteristics) int {
	baseBurst := 3 // Default burst allowance
	
	// Increase burst for high bandwidth paths
	if !characteristics.IsLowBandwidth && characteristics.Bandwidth > 1000000 { // > 1 Mbps
		baseBurst = 5
	}
	
	// Decrease burst for unreliable paths
	if characteristics.IsUnreliable {
		baseBurst = 2
	}
	
	// Decrease burst for high latency paths to avoid overwhelming
	if characteristics.IsHighLatency {
		baseBurst = max(1, baseBurst-1)
	}
	
	return baseBurst
}

// calculateSchedulingWeight calculates the scheduling weight for a path
func (as *AckScheduler) calculateSchedulingWeight(characteristics *PathCharacteristics) float64 {
	weight := 1.0
	
	// Higher weight for better paths
	if !characteristics.IsHighLatency {
		weight += 0.3
	}
	if !characteristics.IsLowBandwidth {
		weight += 0.3
	}
	if !characteristics.IsUnreliable {
		weight += 0.4
	}
	
	// Adjust based on packet loss rate
	weight *= (1.0 - characteristics.PacketLossRate)
	
	// Ensure weight is positive
	if weight < 0.1 {
		weight = 0.1
	}
	
	return weight
}

// calculateScheduledTime calculates when an ACK should be scheduled for transmission
func (as *AckScheduler) calculateScheduledTime(pathState *PathSchedulingState, priority AckPriority, now time.Time) time.Time {
	baseTime := now
	
	// Adjust based on priority
	switch priority {
	case AckPriorityUrgent:
		// Send immediately
		return baseTime
	case AckPriorityHigh:
		// Send with minimal delay
		return baseTime.Add(as.config.MinAckDelay / 2)
	case AckPriorityNormal:
		// Use path's normal ACK interval
		return baseTime.Add(pathState.AckInterval)
	case AckPriorityLow:
		// Use longer delay
		return baseTime.Add(pathState.AckInterval * 2)
	}
	
	return baseTime.Add(pathState.AckInterval)
}

// canSendAck checks if an ACK can be sent based on burst limits
func (as *AckScheduler) canSendAck(pathID string, now time.Time) bool {
	as.pathsMutex.RLock()
	pathState, exists := as.pathSchedulingState[pathID]
	as.pathsMutex.RUnlock()
	
	if !exists {
		return false
	}
	
	// Check if we're within burst allowance
	if pathState.CurrentBurst >= pathState.BurstAllowance {
		// Check if enough time has passed to reset burst
		if now.Sub(pathState.LastAckTime) >= pathState.AckInterval {
			return true // Burst window has reset
		}
		return false // Still within burst window
	}
	
	return true // Within burst allowance
}

// updatePathSchedulingState updates the scheduling state after sending an ACK
func (as *AckScheduler) updatePathSchedulingState(pathID string, now time.Time) {
	as.pathsMutex.Lock()
	defer as.pathsMutex.Unlock()
	
	pathState, exists := as.pathSchedulingState[pathID]
	if !exists {
		return
	}
	
	// Update burst tracking
	if now.Sub(pathState.LastAckTime) >= pathState.AckInterval {
		// Reset burst counter
		pathState.CurrentBurst = 1
	} else {
		// Increment burst counter
		pathState.CurrentBurst++
	}
	
	pathState.LastAckTime = now
	pathState.NextScheduledTime = now.Add(pathState.AckInterval)
}

// GetSchedulingStats returns scheduling statistics
func (as *AckScheduler) GetSchedulingStats() (uint64, uint64, time.Duration, time.Time) {
	as.mutex.RLock()
	defer as.mutex.RUnlock()
	
	return as.totalScheduled, as.totalProcessed, as.averageDelay, as.lastScheduleTime
}

// GetQueueLength returns the current length of the scheduling queue
func (as *AckScheduler) GetQueueLength() int {
	as.queueMutex.Lock()
	defer as.queueMutex.Unlock()
	
	return as.scheduledAcks.Len()
}

// GetPathSchedulingState returns the scheduling state for a path
func (as *AckScheduler) GetPathSchedulingState(pathID string) (*PathSchedulingState, error) {
	as.pathsMutex.RLock()
	defer as.pathsMutex.RUnlock()
	
	pathState, exists := as.pathSchedulingState[pathID]
	if !exists {
		return nil, utils.NewPathNotFoundError(pathID)
	}
	
	// Return a copy to prevent modification
	return &PathSchedulingState{
		PathID:            pathState.PathID,
		NextScheduledTime: pathState.NextScheduledTime,
		AckInterval:       pathState.AckInterval,
		BurstAllowance:    pathState.BurstAllowance,
		CurrentBurst:      pathState.CurrentBurst,
		LastAckTime:       pathState.LastAckTime,
		SchedulingWeight:  pathState.SchedulingWeight,
		Characteristics:   pathState.Characteristics,
	}, nil
}

// Helper function to get maximum of two integers
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}