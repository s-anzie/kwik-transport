package data

import (
	"container/list"
	"sync"
	"time"

	"kwik/internal/utils"
)

// RetransmissionManager handles efficient retransmission triggering mechanisms
// Implements Requirement 11.3
type RetransmissionManager struct {
	config *AckManagerConfig

	// Retransmission queues per path
	pathQueues  map[string]*RetransmissionQueue
	queuesMutex sync.RWMutex

	// Loss detector integration
	lossDetector *LossDetector

	// Retransmission statistics
	totalRetransmissions   uint64
	fastRetransmissions    uint64
	timeoutRetransmissions uint64

	statsMutex sync.RWMutex
}

// RetransmissionQueue manages retransmission queue for a specific path
type RetransmissionQueue struct {
	PathID                 string
	PendingRetransmissions *list.List
	RetransmissionTimer    *time.Timer
	MaxRetransmissions     int
	RetransmissionTimeout  time.Duration

	// Queue statistics
	QueueLength               int
	TotalRetransmitted        uint64
	SuccessfulRetransmissions uint64
	FailedRetransmissions     uint64

	mutex sync.RWMutex
}

// RetransmissionRequest represents a packet that needs to be retransmitted
type RetransmissionRequest struct {
	PacketID            uint64
	OriginalData        []byte
	RetransmissionCount int
	LastAttempt         time.Time
	CreatedTime         time.Time
	Priority            RetransmissionPriority
	Reason              RetransmissionReason
	PathID              string

	// Callback for completion notification
	OnComplete func(success bool, attempts int)
}

// RetransmissionPriority defines the priority of retransmission
type RetransmissionPriority int

const (
	RetransmissionPriorityLow RetransmissionPriority = iota
	RetransmissionPriorityNormal
	RetransmissionPriorityHigh
	RetransmissionPriorityUrgent
)

// RetransmissionReason defines why a packet needs retransmission
type RetransmissionReason int

const (
	RetransmissionReasonTimeout RetransmissionReason = iota
	RetransmissionReasonFastRetransmit
	RetransmissionReasonReordering
	RetransmissionReasonExplicitRequest
)

// NewRetransmissionManager creates a new retransmission manager
func NewRetransmissionManager(config *AckManagerConfig, lossDetector *LossDetector) *RetransmissionManager {
	return &RetransmissionManager{
		config:       config,
		pathQueues:   make(map[string]*RetransmissionQueue),
		lossDetector: lossDetector,
	}
}

// RegisterPath registers a path for retransmission management
func (rm *RetransmissionManager) RegisterPath(pathID string) error {
	rm.queuesMutex.Lock()
	defer rm.queuesMutex.Unlock()

	if _, exists := rm.pathQueues[pathID]; exists {
		return utils.NewKwikError(utils.ErrInvalidFrame, "path already registered for retransmission", nil)
	}

	queue := &RetransmissionQueue{
		PathID:                 pathID,
		PendingRetransmissions: list.New(),
		MaxRetransmissions:     3, // Use default value
		RetransmissionTimeout:  500 * time.Millisecond, // Use default value
		QueueLength:            0,
	}

	rm.pathQueues[pathID] = queue

	// Start retransmission timer for this path
	rm.startRetransmissionTimer(queue)

	return nil
}

// UnregisterPath unregisters a path from retransmission management
func (rm *RetransmissionManager) UnregisterPath(pathID string) error {
	rm.queuesMutex.Lock()
	defer rm.queuesMutex.Unlock()

	queue, exists := rm.pathQueues[pathID]
	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}

	// Stop retransmission timer
	if queue.RetransmissionTimer != nil {
		queue.RetransmissionTimer.Stop()
	}

	// Clear pending retransmissions
	queue.mutex.Lock()
	queue.PendingRetransmissions.Init()
	queue.QueueLength = 0
	queue.mutex.Unlock()

	delete(rm.pathQueues, pathID)

	return nil
}

// TriggerFastRetransmission triggers fast retransmission for lost packets
func (rm *RetransmissionManager) TriggerFastRetransmission(pathID string, lostPackets []uint64, originalData map[uint64][]byte) error {
	rm.queuesMutex.RLock()
	queue, exists := rm.pathQueues[pathID]
	rm.queuesMutex.RUnlock()

	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}

	now := time.Now()

	for _, packetID := range lostPackets {
		data, hasData := originalData[packetID]
		if !hasData {
			continue // Skip packets without data
		}

		request := &RetransmissionRequest{
			PacketID:            packetID,
			OriginalData:        data,
			RetransmissionCount: 0,
			LastAttempt:         time.Time{},
			CreatedTime:         now,
			Priority:            RetransmissionPriorityHigh, // Fast retransmit is high priority
			Reason:              RetransmissionReasonFastRetransmit,
			PathID:              pathID,
		}

		rm.enqueueRetransmission(queue, request)
	}

	// Update statistics
	rm.statsMutex.Lock()
	rm.fastRetransmissions += uint64(len(lostPackets))
	rm.totalRetransmissions += uint64(len(lostPackets))
	rm.statsMutex.Unlock()

	return nil
}

// TriggerTimeoutRetransmission triggers timeout-based retransmission
func (rm *RetransmissionManager) TriggerTimeoutRetransmission(pathID string, timedOutPackets []uint64, originalData map[uint64][]byte) error {
	rm.queuesMutex.RLock()
	queue, exists := rm.pathQueues[pathID]
	rm.queuesMutex.RUnlock()

	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}

	now := time.Now()

	for _, packetID := range timedOutPackets {
		data, hasData := originalData[packetID]
		if !hasData {
			continue // Skip packets without data
		}

		request := &RetransmissionRequest{
			PacketID:            packetID,
			OriginalData:        data,
			RetransmissionCount: 0,
			LastAttempt:         time.Time{},
			CreatedTime:         now,
			Priority:            RetransmissionPriorityNormal, // Timeout retransmit is normal priority
			Reason:              RetransmissionReasonTimeout,
			PathID:              pathID,
		}

		rm.enqueueRetransmission(queue, request)
	}

	// Update statistics
	rm.statsMutex.Lock()
	rm.timeoutRetransmissions += uint64(len(timedOutPackets))
	rm.totalRetransmissions += uint64(len(timedOutPackets))
	rm.statsMutex.Unlock()

	return nil
}

// ProcessRetransmissions processes pending retransmissions for all paths
func (rm *RetransmissionManager) ProcessRetransmissions() error {
	rm.queuesMutex.RLock()
	defer rm.queuesMutex.RUnlock()

	for pathID, queue := range rm.pathQueues {
		err := rm.processPathRetransmissions(pathID, queue)
		if err != nil {
			// Log error but continue with other paths
			continue
		}
	}

	return nil
}

// processPathRetransmissions processes retransmissions for a specific path
func (rm *RetransmissionManager) processPathRetransmissions(pathID string, queue *RetransmissionQueue) error {
	queue.mutex.Lock()
	defer queue.mutex.Unlock()

	now := time.Now()
	processed := 0
	maxProcessed := 10 // Limit processing per iteration

	// Process retransmissions in priority order
	for element := queue.PendingRetransmissions.Front(); element != nil && processed < maxProcessed; {
		request := element.Value.(*RetransmissionRequest)
		next := element.Next()

		// Check if it's time to retransmit
		if rm.shouldRetransmit(request, now) {
			success := rm.executeRetransmission(request)

			if success {
				queue.SuccessfulRetransmissions++

				// Remove from queue if successful
				queue.PendingRetransmissions.Remove(element)
				queue.QueueLength--

				// Notify completion
				if request.OnComplete != nil {
					request.OnComplete(true, request.RetransmissionCount+1)
				}
			} else {
				// Increment retry count
				request.RetransmissionCount++
				request.LastAttempt = now

				// Check if we've exceeded max retransmissions
				if request.RetransmissionCount >= queue.MaxRetransmissions {
					queue.FailedRetransmissions++

					// Remove from queue
					queue.PendingRetransmissions.Remove(element)
					queue.QueueLength--

					// Notify failure
					if request.OnComplete != nil {
						request.OnComplete(false, request.RetransmissionCount)
					}
				}
			}

			processed++
		}

		element = next
	}

	return nil
}

// shouldRetransmit determines if a retransmission request should be processed
func (rm *RetransmissionManager) shouldRetransmit(request *RetransmissionRequest, now time.Time) bool {
	// First attempt
	if request.LastAttempt.IsZero() {
		return true
	}

	// Check if enough time has passed since last attempt
	timeSinceLastAttempt := now.Sub(request.LastAttempt)

	// Calculate backoff based on retry count
	backoffMultiplier := 1 << request.RetransmissionCount // Exponential backoff
	if backoffMultiplier > 8 {
		backoffMultiplier = 8 // Cap at 8x
	}

	requiredInterval := time.Duration(backoffMultiplier) * 500 * time.Millisecond // Use default timeout

	return timeSinceLastAttempt >= requiredInterval
}

// executeRetransmission executes a retransmission request
func (rm *RetransmissionManager) executeRetransmission(request *RetransmissionRequest) bool {
	// In a real implementation, this would send the packet through the data plane
	// For now, we simulate success/failure

	// Simulate some failures for testing
	if request.RetransmissionCount > 2 {
		return false // Simulate failure after multiple attempts
	}

	// Simulate network conditions affecting success rate
	if request.Reason == RetransmissionReasonTimeout {
		// Timeout retransmissions have lower success rate
		return request.RetransmissionCount < 2
	}

	// Fast retransmissions generally have higher success rate
	return true
}

// enqueueRetransmission adds a retransmission request to the queue
func (rm *RetransmissionManager) enqueueRetransmission(queue *RetransmissionQueue, request *RetransmissionRequest) {
	queue.mutex.Lock()
	defer queue.mutex.Unlock()

	// Insert based on priority
	inserted := false
	for element := queue.PendingRetransmissions.Front(); element != nil; element = element.Next() {
		existingRequest := element.Value.(*RetransmissionRequest)

		// Higher priority requests go first
		if request.Priority > existingRequest.Priority {
			queue.PendingRetransmissions.InsertBefore(request, element)
			inserted = true
			break
		}
	}

	// If not inserted, add to the end
	if !inserted {
		queue.PendingRetransmissions.PushBack(request)
	}

	queue.QueueLength++
}

// startRetransmissionTimer starts the retransmission timer for a path
func (rm *RetransmissionManager) startRetransmissionTimer(queue *RetransmissionQueue) {
	queue.RetransmissionTimer = time.AfterFunc(queue.RetransmissionTimeout, func() {
		rm.processPathRetransmissions(queue.PathID, queue)

		// Restart timer
		rm.startRetransmissionTimer(queue)
	})
}

// OnPacketAcknowledged removes acknowledged packets from retransmission queues
func (rm *RetransmissionManager) OnPacketAcknowledged(pathID string, packetID uint64) error {
	rm.queuesMutex.RLock()
	queue, exists := rm.pathQueues[pathID]
	rm.queuesMutex.RUnlock()

	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}

	queue.mutex.Lock()
	defer queue.mutex.Unlock()

	// Remove acknowledged packet from retransmission queue
	for element := queue.PendingRetransmissions.Front(); element != nil; element = element.Next() {
		request := element.Value.(*RetransmissionRequest)

		if request.PacketID == packetID {
			queue.PendingRetransmissions.Remove(element)
			queue.QueueLength--

			// Notify completion
			if request.OnComplete != nil {
				request.OnComplete(true, 0) // Acknowledged without retransmission
			}

			break
		}
	}

	return nil
}

// GetRetransmissionStats returns retransmission statistics
func (rm *RetransmissionManager) GetRetransmissionStats() (*RetransmissionStats, error) {
	rm.statsMutex.RLock()
	defer rm.statsMutex.RUnlock()

	return &RetransmissionStats{
		TotalRetransmissions:   rm.totalRetransmissions,
		FastRetransmissions:    rm.fastRetransmissions,
		TimeoutRetransmissions: rm.timeoutRetransmissions,
		ActivePaths:            len(rm.pathQueues),
	}, nil
}

// GetPathRetransmissionStats returns retransmission statistics for a specific path
func (rm *RetransmissionManager) GetPathRetransmissionStats(pathID string) (*PathRetransmissionStats, error) {
	rm.queuesMutex.RLock()
	queue, exists := rm.pathQueues[pathID]
	rm.queuesMutex.RUnlock()

	if !exists {
		return nil, utils.NewPathNotFoundError(pathID)
	}

	queue.mutex.RLock()
	defer queue.mutex.RUnlock()

	return &PathRetransmissionStats{
		PathID:                    pathID,
		QueueLength:               queue.QueueLength,
		TotalRetransmitted:        queue.TotalRetransmitted,
		SuccessfulRetransmissions: queue.SuccessfulRetransmissions,
		FailedRetransmissions:     queue.FailedRetransmissions,
		MaxRetransmissions:        queue.MaxRetransmissions,
		RetransmissionTimeout:     queue.RetransmissionTimeout,
	}, nil
}

// RetransmissionStats contains global retransmission statistics
type RetransmissionStats struct {
	TotalRetransmissions   uint64
	FastRetransmissions    uint64
	TimeoutRetransmissions uint64
	ActivePaths            int
}

// PathRetransmissionStats contains retransmission statistics for a specific path
type PathRetransmissionStats struct {
	PathID                    string
	QueueLength               int
	TotalRetransmitted        uint64
	SuccessfulRetransmissions uint64
	FailedRetransmissions     uint64
	MaxRetransmissions        int
	RetransmissionTimeout     time.Duration
}

// SetRetransmissionTimeout updates the retransmission timeout for a path
func (rm *RetransmissionManager) SetRetransmissionTimeout(pathID string, timeout time.Duration) error {
	rm.queuesMutex.RLock()
	queue, exists := rm.pathQueues[pathID]
	rm.queuesMutex.RUnlock()

	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}

	queue.mutex.Lock()
	defer queue.mutex.Unlock()

	queue.RetransmissionTimeout = timeout

	return nil
}

// GetQueueLength returns the current retransmission queue length for a path
func (rm *RetransmissionManager) GetQueueLength(pathID string) (int, error) {
	rm.queuesMutex.RLock()
	queue, exists := rm.pathQueues[pathID]
	rm.queuesMutex.RUnlock()

	if !exists {
		return 0, utils.NewPathNotFoundError(pathID)
	}

	queue.mutex.RLock()
	defer queue.mutex.RUnlock()

	return queue.QueueLength, nil
}
