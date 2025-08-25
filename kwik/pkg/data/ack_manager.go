package data

import (
	"context"
	"sync"
	"time"

	"kwik/internal/utils"
	"kwik/proto/data"
)

// AckManager handles optimal ACK management across multiple paths
// Implements Requirements 11.1, 11.2, 11.4, 11.5
type AckManager interface {
	// Per-path ACK optimization (Requirement 11.1)
	RegisterPath(pathID string, characteristics *PathCharacteristics) error
	UnregisterPath(pathID string) error
	UpdatePathCharacteristics(pathID string, characteristics *PathCharacteristics) error

	// ACK generation and processing (Requirement 11.1, 11.2)
	GenerateAck(pathID string, packetIDs []uint64) (*data.AckFrame, error)
	ProcessReceivedAck(pathID string, ackFrame *data.AckFrame) error

	// Fast loss detection (Requirement 11.3)
	DetectPacketLoss(pathID string) ([]uint64, error)
	TriggerRetransmission(pathID string, packetIDs []uint64) error

	// Congestion control (Requirement 11.4)
	UpdateCongestionWindow(pathID string, ackFrame *data.AckFrame) error
	GetCongestionWindow(pathID string) (uint64, error)

	// Multi-path coordination (Requirement 11.5)
	CoordinateAcks() error
	ScheduleAck(pathID string, priority AckPriority) error

	// Statistics and monitoring
	GetAckStats(pathID string) (*AckStats, error)
	GetGlobalAckStats() (*GlobalAckStats, error)

	// Lifecycle
	Start() error
	Stop() error
	Close() error
}

// PathCharacteristics contains characteristics of a path for ACK optimization
type PathCharacteristics struct {
	PathID          string
	RTT             time.Duration
	Bandwidth       uint64  // bytes per second
	PacketLossRate  float64 // 0.0 to 1.0
	Jitter          time.Duration
	CongestionLevel float64 // 0.0 to 1.0
	IsHighLatency   bool
	IsLowBandwidth  bool
	IsUnreliable    bool
	LastUpdate      time.Time
}

// AckPriority defines the priority level for ACK scheduling
type AckPriority int

const (
	AckPriorityLow AckPriority = iota
	AckPriorityNormal
	AckPriorityHigh
	AckPriorityUrgent
)

// AckStats contains ACK statistics for a specific path
type AckStats struct {
	PathID                   string
	AcksSent                 uint64
	AcksReceived             uint64
	AverageAckDelay          time.Duration
	PacketLossDetected       uint64
	RetransmissionsTriggered uint64
	CongestionWindowSize     uint64
	LastAckTime              time.Time
	AckBatchingRatio         float64
}

// GlobalAckStats contains aggregated ACK statistics across all paths
type GlobalAckStats struct {
	TotalAcksSent        uint64
	TotalAcksReceived    uint64
	TotalPacketLoss      uint64
	TotalRetransmissions uint64
	PathCount            int
	CoordinationEvents   uint64
	LastCoordination     time.Time
}

// AckManagerImpl implements the AckManager interface
type AckManagerImpl struct {
	// Path management
	paths      map[string]*PathAckState
	pathsMutex sync.RWMutex

	// ACK coordination
	coordinator *AckCoordinator
	scheduler   *AckScheduler

	// Loss detection
	lossDetector *LossDetector

	// Retransmission management
	retransmissionManager RetransmissionManager

	// Congestion control
	congestionController *CongestionController

	// Configuration
	config *AckManagerConfig

	// Context and lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Statistics
	globalStats *GlobalAckStats
	statsMutex  sync.RWMutex
}

// AckManagerConfig holds configuration for ACK management
type AckManagerConfig struct {
	// ACK timing configuration
	MaxAckDelay          time.Duration
	MinAckDelay          time.Duration
	AckBatchingThreshold int

	// Loss detection configuration
	LossDetectionTimeout    time.Duration
	FastRetransmitThreshold int

	// Congestion control configuration
	InitialCongestionWindow uint64
	MaxCongestionWindow     uint64
	MinCongestionWindow     uint64
	CongestionWindowGrowth  float64

	// Coordination configuration
	CoordinationInterval time.Duration
	MaxConcurrentAcks    int
	AckPriorityLevels    int

	// Performance tuning
	WorkerCount        int
	BufferSize         int
	StatisticsInterval time.Duration
}

// DefaultAckManagerConfig returns default ACK manager configuration
func DefaultAckManagerConfig() *AckManagerConfig {
	return &AckManagerConfig{
		MaxAckDelay:             100 * time.Millisecond,
		MinAckDelay:             10 * time.Millisecond,
		AckBatchingThreshold:    5,
		LossDetectionTimeout:    500 * time.Millisecond,
		FastRetransmitThreshold: 3,
		InitialCongestionWindow: 10 * 1460, // 10 packets * typical MTU
		MaxCongestionWindow:     1000 * 1460,
		MinCongestionWindow:     2 * 1460,
		CongestionWindowGrowth:  1.5,
		CoordinationInterval:    50 * time.Millisecond,
		MaxConcurrentAcks:       10,
		AckPriorityLevels:       4,
		WorkerCount:             2,
		BufferSize:              1000,
		StatisticsInterval:      1 * time.Second,
	}
}

// PathAckState maintains ACK state for a specific path
type PathAckState struct {
	PathID          string
	Characteristics *PathCharacteristics

	// ACK timing strategy
	AckStrategy     *AckTimingStrategy
	BatchingEnabled bool

	// Packet tracking
	SentPackets     map[uint64]*SentPacketInfo
	ReceivedPackets map[uint64]*ReceivedPacketInfo
	PacketsMutex    sync.RWMutex

	// Loss detection
	LossDetectionState *LossDetectionState

	// Congestion control
	CongestionState *CongestionControlState

	// Statistics
	Stats *AckStats

	// Synchronization
	mutex sync.RWMutex
}

// NewAckManager creates a new ACK manager instance
func NewAckManager(config *AckManagerConfig) *AckManagerImpl {
	if config == nil {
		config = DefaultAckManagerConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())

	am := &AckManagerImpl{
		paths:  make(map[string]*PathAckState),
		config: config,
		ctx:    ctx,
		cancel: cancel,
		globalStats: &GlobalAckStats{
			PathCount: 0,
		},
	}

	// Initialize components
	am.coordinator = NewAckCoordinator(config)
	am.scheduler = NewAckScheduler(config)
	am.lossDetector = NewLossDetector(config)
	am.congestionController = NewCongestionController(config)
	retransmissionConfig := &RetransmissionConfig{
		DefaultTimeout:       config.LossDetectionTimeout,
		CleanupInterval:      30 * time.Second,
		MaxConcurrentRetries: 100,
		EnableDetailedStats:  true,
		Logger:               nil,
	}
	am.retransmissionManager = NewRetransmissionManager(retransmissionConfig)

	return am
}

// RegisterPath registers a new path for ACK management
func (am *AckManagerImpl) RegisterPath(pathID string, characteristics *PathCharacteristics) error {
	if pathID == "" {
		return utils.NewKwikError(utils.ErrInvalidFrame, "path ID cannot be empty", nil)
	}

	if characteristics == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "path characteristics cannot be nil", nil)
	}

	am.pathsMutex.Lock()
	defer am.pathsMutex.Unlock()

	// Check if path already exists
	if _, exists := am.paths[pathID]; exists {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			"path already registered", nil)
	}

	// Create path ACK state
	pathState := &PathAckState{
		PathID:          pathID,
		Characteristics: characteristics,
		SentPackets:     make(map[uint64]*SentPacketInfo),
		ReceivedPackets: make(map[uint64]*ReceivedPacketInfo),
		Stats: &AckStats{
			PathID:               pathID,
			CongestionWindowSize: am.config.InitialCongestionWindow,
		},
	}

	// Initialize ACK timing strategy based on path characteristics
	pathState.AckStrategy = am.createAckTimingStrategy(characteristics)
	pathState.BatchingEnabled = am.shouldEnableBatching(characteristics)

	// Initialize loss detection state
	pathState.LossDetectionState = NewLossDetectionState(pathID, am.config)

	// Initialize congestion control state
	pathState.CongestionState = NewCongestionControlState(pathID, am.config)

	am.paths[pathID] = pathState

	// Register path with all components
	err := am.coordinator.RegisterPath(pathID, characteristics)
	if err != nil {
		delete(am.paths, pathID)
		return err
	}

	err = am.scheduler.RegisterPath(pathID, characteristics)
	if err != nil {
		am.coordinator.UnregisterPath(pathID)
		delete(am.paths, pathID)
		return err
	}

	err = am.lossDetector.RegisterPath(pathID, am.config)
	if err != nil {
		am.coordinator.UnregisterPath(pathID)
		am.scheduler.UnregisterPath(pathID)
		delete(am.paths, pathID)
		return err
	}

	err = am.congestionController.RegisterPath(pathID, am.config)
	if err != nil {
		am.coordinator.UnregisterPath(pathID)
		am.scheduler.UnregisterPath(pathID)
		am.lossDetector.UnregisterPath(pathID)
		delete(am.paths, pathID)
		return err
	}

	err = am.retransmissionManager.RegisterPath(pathID)
	if err != nil {
		am.coordinator.UnregisterPath(pathID)
		am.scheduler.UnregisterPath(pathID)
		am.lossDetector.UnregisterPath(pathID)
		am.congestionController.UnregisterPath(pathID)
		delete(am.paths, pathID)
		return err
	}

	// Update global statistics
	am.statsMutex.Lock()
	am.globalStats.PathCount++
	am.statsMutex.Unlock()

	return nil
}

// UnregisterPath unregisters a path from ACK management
func (am *AckManagerImpl) UnregisterPath(pathID string) error {
	am.pathsMutex.Lock()
	defer am.pathsMutex.Unlock()

	pathState, exists := am.paths[pathID]
	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}

	// Clean up path state
	pathState.mutex.Lock()
	pathState.SentPackets = nil
	pathState.ReceivedPackets = nil
	pathState.mutex.Unlock()

	// Unregister from all components
	am.coordinator.UnregisterPath(pathID)
	am.scheduler.UnregisterPath(pathID)
	am.lossDetector.UnregisterPath(pathID)
	am.congestionController.UnregisterPath(pathID)
	am.retransmissionManager.UnregisterPath(pathID)

	delete(am.paths, pathID)

	// Update global statistics
	am.statsMutex.Lock()
	am.globalStats.PathCount--
	am.statsMutex.Unlock()

	return nil
}

// UpdatePathCharacteristics updates the characteristics of a path
func (am *AckManagerImpl) UpdatePathCharacteristics(pathID string, characteristics *PathCharacteristics) error {
	am.pathsMutex.RLock()
	pathState, exists := am.paths[pathID]
	am.pathsMutex.RUnlock()

	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}

	pathState.mutex.Lock()
	defer pathState.mutex.Unlock()

	// Update characteristics
	pathState.Characteristics = characteristics

	// Update ACK timing strategy
	pathState.AckStrategy = am.createAckTimingStrategy(characteristics)
	pathState.BatchingEnabled = am.shouldEnableBatching(characteristics)

	return nil
}

// createAckTimingStrategy creates an ACK timing strategy based on path characteristics
func (am *AckManagerImpl) createAckTimingStrategy(characteristics *PathCharacteristics) *AckTimingStrategy {
	strategy := &AckTimingStrategy{
		PathID: characteristics.PathID,
	}

	// Adjust timing based on RTT
	if characteristics.RTT > 100*time.Millisecond {
		// High latency path - use longer ACK delays to batch more
		strategy.AckDelay = am.config.MaxAckDelay
		strategy.BatchingThreshold = am.config.AckBatchingThreshold * 2
	} else if characteristics.RTT < 10*time.Millisecond {
		// Low latency path - use shorter ACK delays for responsiveness
		strategy.AckDelay = am.config.MinAckDelay
		strategy.BatchingThreshold = am.config.AckBatchingThreshold / 2
	} else {
		// Normal latency path - use default settings
		strategy.AckDelay = (am.config.MaxAckDelay + am.config.MinAckDelay) / 2
		strategy.BatchingThreshold = am.config.AckBatchingThreshold
	}

	// Adjust for packet loss
	if characteristics.PacketLossRate > 0.01 { // > 1% loss
		// High loss path - send ACKs more frequently
		strategy.AckDelay = strategy.AckDelay / 2
		strategy.BatchingThreshold = strategy.BatchingThreshold / 2
	}

	// Adjust for bandwidth
	if characteristics.IsLowBandwidth {
		// Low bandwidth path - batch more aggressively to reduce overhead
		strategy.BatchingThreshold = strategy.BatchingThreshold * 2
	}

	return strategy
}

// shouldEnableBatching determines if ACK batching should be enabled for a path
func (am *AckManagerImpl) shouldEnableBatching(characteristics *PathCharacteristics) bool {
	// Enable batching for high-latency or low-bandwidth paths
	return characteristics.IsHighLatency || characteristics.IsLowBandwidth
}

// AckTimingStrategy defines the timing strategy for ACKs on a specific path
type AckTimingStrategy struct {
	PathID            string
	AckDelay          time.Duration
	BatchingThreshold int
	LastAckTime       time.Time
	PendingAcks       []uint64
	mutex             sync.Mutex
}

// ReceivedPacketInfo tracks information about received packets for ACK generation
type ReceivedPacketInfo struct {
	PacketID     uint64
	ReceivedTime time.Time
	Size         uint32
	Acknowledged bool
	AckSentTime  time.Time
}

// GenerateAck generates an ACK frame for a specific path
func (am *AckManagerImpl) GenerateAck(pathID string, packetIDs []uint64) (*data.AckFrame, error) {
	am.pathsMutex.RLock()
	pathState, exists := am.paths[pathID]
	am.pathsMutex.RUnlock()

	if !exists {
		return nil, utils.NewPathNotFoundError(pathID)
	}

	pathState.mutex.Lock()
	defer pathState.mutex.Unlock()

	if len(packetIDs) == 0 {
		return nil, utils.NewKwikError(utils.ErrInvalidFrame, "no packet IDs to acknowledge", nil)
	}

	// Create ACK frame
	ackFrame := &data.AckFrame{
		AckId:     generateFrameID(),
		PathId:    pathID,
		Timestamp: uint64(time.Now().UnixNano()),
	}

	// Determine ACK strategy based on path characteristics
	if pathState.BatchingEnabled && len(packetIDs) >= pathState.AckStrategy.BatchingThreshold {
		// Use batched ACK with ranges
		ackFrame.AckRanges = am.createAckRanges(packetIDs)
		ackFrame.LargestAcked = am.findLargestPacketID(packetIDs)
	} else {
		// Use individual packet ACKs
		ackFrame.AckedPacketIds = packetIDs
		ackFrame.LargestAcked = am.findLargestPacketID(packetIDs)
	}

	// Calculate ACK delay based on when the largest packet was received
	if largestPacketInfo, exists := pathState.ReceivedPackets[ackFrame.LargestAcked]; exists {
		ackDelay := time.Since(largestPacketInfo.ReceivedTime)
		ackFrame.AckDelay = uint64(ackDelay.Microseconds())
	}

	// Mark packets as acknowledged
	for _, packetID := range packetIDs {
		if packetInfo, exists := pathState.ReceivedPackets[packetID]; exists {
			packetInfo.Acknowledged = true
			packetInfo.AckSentTime = time.Now()
		}
	}

	// Update statistics
	pathState.Stats.AcksSent++
	pathState.Stats.LastAckTime = time.Now()

	// Update global statistics
	am.statsMutex.Lock()
	am.globalStats.TotalAcksSent++
	am.statsMutex.Unlock()

	return ackFrame, nil
}

// ProcessReceivedAck processes a received ACK frame
func (am *AckManagerImpl) ProcessReceivedAck(pathID string, ackFrame *data.AckFrame) error {
	am.pathsMutex.RLock()
	pathState, exists := am.paths[pathID]
	am.pathsMutex.RUnlock()

	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}

	pathState.mutex.Lock()
	defer pathState.mutex.Unlock()

	now := time.Now()
	ackedPackets := make([]uint64, 0)

	// Process individual ACKed packets
	for _, packetID := range ackFrame.AckedPacketIds {
		if sentInfo, exists := pathState.SentPackets[packetID]; exists && !sentInfo.Acknowledged {
			sentInfo.Acknowledged = true
			sentInfo.AckTime = now
			ackedPackets = append(ackedPackets, packetID)

			// Update RTT estimate
			sentTime := time.Unix(0, sentInfo.SentTime)
			rtt := now.Sub(sentTime)
			am.updateRTTEstimate(pathState, rtt)
		}
	}

	// Process ACK ranges
	for _, ackRange := range ackFrame.AckRanges {
		for packetID := ackRange.Start; packetID <= ackRange.End; packetID++ {
			if sentInfo, exists := pathState.SentPackets[packetID]; exists && !sentInfo.Acknowledged {
				sentInfo.Acknowledged = true
				sentInfo.AckTime = now
				ackedPackets = append(ackedPackets, packetID)

				// Update RTT estimate
				sentTime := time.Unix(0, sentInfo.SentTime)
				rtt := now.Sub(sentTime)
				am.updateRTTEstimate(pathState, rtt)
			}
		}
	}

	// Update congestion control
	if len(ackedPackets) > 0 {
		err := am.congestionController.ProcessAck(pathState.CongestionState, ackFrame, ackedPackets)
		if err != nil {
			return err
		}

		// Update congestion window size in stats
		pathState.Stats.CongestionWindowSize = pathState.CongestionState.CongestionWindow
	}

	// Update statistics
	pathState.Stats.AcksReceived++

	// Update global statistics
	am.statsMutex.Lock()
	am.globalStats.TotalAcksReceived++
	am.statsMutex.Unlock()

	return nil
}

// createAckRanges creates ACK ranges from a list of packet IDs
func (am *AckManagerImpl) createAckRanges(packetIDs []uint64) []*data.AckRange {
	if len(packetIDs) == 0 {
		return nil
	}

	// Sort packet IDs
	sortedIDs := make([]uint64, len(packetIDs))
	copy(sortedIDs, packetIDs)

	// Simple bubble sort for small arrays
	for i := 0; i < len(sortedIDs)-1; i++ {
		for j := 0; j < len(sortedIDs)-i-1; j++ {
			if sortedIDs[j] > sortedIDs[j+1] {
				sortedIDs[j], sortedIDs[j+1] = sortedIDs[j+1], sortedIDs[j]
			}
		}
	}

	ranges := make([]*data.AckRange, 0)
	start := sortedIDs[0]
	end := sortedIDs[0]

	for i := 1; i < len(sortedIDs); i++ {
		if sortedIDs[i] == end+1 {
			// Consecutive packet, extend range
			end = sortedIDs[i]
		} else {
			// Gap found, create range and start new one
			ranges = append(ranges, &data.AckRange{
				Start: start,
				End:   end,
			})
			start = sortedIDs[i]
			end = sortedIDs[i]
		}
	}

	// Add final range
	ranges = append(ranges, &data.AckRange{
		Start: start,
		End:   end,
	})

	return ranges
}

// findLargestPacketID finds the largest packet ID in a list
func (am *AckManagerImpl) findLargestPacketID(packetIDs []uint64) uint64 {
	if len(packetIDs) == 0 {
		return 0
	}

	largest := packetIDs[0]
	for _, id := range packetIDs[1:] {
		if id > largest {
			largest = id
		}
	}

	return largest
}

// updateRTTEstimate updates the RTT estimate for a path
func (am *AckManagerImpl) updateRTTEstimate(pathState *PathAckState, rtt time.Duration) {
	// Simple RTT estimation - in a real implementation, this would use
	// more sophisticated algorithms like RFC 6298
	if pathState.Characteristics.RTT == 0 {
		pathState.Characteristics.RTT = rtt
	} else {
		// Exponential weighted moving average
		alpha := 0.125 // Standard TCP alpha value
		pathState.Characteristics.RTT = time.Duration(
			float64(pathState.Characteristics.RTT)*(1-alpha) + float64(rtt)*alpha,
		)
	}

	pathState.Characteristics.LastUpdate = time.Now()
}

// Start starts the ACK manager
func (am *AckManagerImpl) Start() error {
	// Start coordination worker
	am.wg.Add(1)
	go am.coordinationWorker()

	// Start statistics worker
	am.wg.Add(1)
	go am.statisticsWorker()

	return nil
}

// Stop stops the ACK manager
func (am *AckManagerImpl) Stop() error {
	am.cancel()
	am.wg.Wait()
	return nil
}

// Close closes the ACK manager and releases resources
func (am *AckManagerImpl) Close() error {
	am.cancel()
	am.wg.Wait()

	am.pathsMutex.Lock()
	defer am.pathsMutex.Unlock()

	// Clean up all paths
	for pathID := range am.paths {
		delete(am.paths, pathID)
	}

	return nil
}

// coordinationWorker handles ACK coordination between paths
func (am *AckManagerImpl) coordinationWorker() {
	defer am.wg.Done()

	ticker := time.NewTicker(am.config.CoordinationInterval)
	defer ticker.Stop()

	for {
		select {
		case <-am.ctx.Done():
			return
		case <-ticker.C:
			am.CoordinateAcks()
		}
	}
}

// statisticsWorker updates statistics periodically
func (am *AckManagerImpl) statisticsWorker() {
	defer am.wg.Done()

	ticker := time.NewTicker(am.config.StatisticsInterval)
	defer ticker.Stop()

	for {
		select {
		case <-am.ctx.Done():
			return
		case <-ticker.C:
			am.updateStatistics()
		}
	}
}

// updateStatistics updates ACK statistics
func (am *AckManagerImpl) updateStatistics() {
	am.pathsMutex.RLock()
	defer am.pathsMutex.RUnlock()

	for _, pathState := range am.paths {
		pathState.mutex.RLock()

		// Calculate average ACK delay
		totalDelay := time.Duration(0)
		ackCount := 0

		for _, packetInfo := range pathState.ReceivedPackets {
			if packetInfo.Acknowledged {
				delay := packetInfo.AckSentTime.Sub(packetInfo.ReceivedTime)
				totalDelay += delay
				ackCount++
			}
		}

		if ackCount > 0 {
			pathState.Stats.AverageAckDelay = totalDelay / time.Duration(ackCount)
		}

		// Calculate ACK batching ratio
		if pathState.Stats.AcksSent > 0 {
			totalPacketsAcked := uint64(0)
			for _, packetInfo := range pathState.ReceivedPackets {
				if packetInfo.Acknowledged {
					totalPacketsAcked++
				}
			}

			if totalPacketsAcked > 0 {
				pathState.Stats.AckBatchingRatio = float64(totalPacketsAcked) / float64(pathState.Stats.AcksSent)
			}
		}

		pathState.mutex.RUnlock()
	}
}

// GetAckStats returns ACK statistics for a specific path
func (am *AckManagerImpl) GetAckStats(pathID string) (*AckStats, error) {
	am.pathsMutex.RLock()
	pathState, exists := am.paths[pathID]
	am.pathsMutex.RUnlock()

	if !exists {
		return nil, utils.NewPathNotFoundError(pathID)
	}

	pathState.mutex.RLock()
	defer pathState.mutex.RUnlock()

	// Return a copy of the stats
	return &AckStats{
		PathID:                   pathState.Stats.PathID,
		AcksSent:                 pathState.Stats.AcksSent,
		AcksReceived:             pathState.Stats.AcksReceived,
		AverageAckDelay:          pathState.Stats.AverageAckDelay,
		PacketLossDetected:       pathState.Stats.PacketLossDetected,
		RetransmissionsTriggered: pathState.Stats.RetransmissionsTriggered,
		CongestionWindowSize:     pathState.Stats.CongestionWindowSize,
		LastAckTime:              pathState.Stats.LastAckTime,
		AckBatchingRatio:         pathState.Stats.AckBatchingRatio,
	}, nil
}

// GetGlobalAckStats returns global ACK statistics
func (am *AckManagerImpl) GetGlobalAckStats() (*GlobalAckStats, error) {
	am.statsMutex.RLock()
	defer am.statsMutex.RUnlock()

	// Return a copy of the global stats
	return &GlobalAckStats{
		TotalAcksSent:        am.globalStats.TotalAcksSent,
		TotalAcksReceived:    am.globalStats.TotalAcksReceived,
		TotalPacketLoss:      am.globalStats.TotalPacketLoss,
		TotalRetransmissions: am.globalStats.TotalRetransmissions,
		PathCount:            am.globalStats.PathCount,
		CoordinationEvents:   am.globalStats.CoordinationEvents,
		LastCoordination:     am.globalStats.LastCoordination,
	}, nil
}

// CoordinateAcks performs ACK coordination across all paths
func (am *AckManagerImpl) CoordinateAcks() error {
	// Delegate to coordinator
	return am.coordinator.CoordinateAcks()
}

// ScheduleAck schedules an ACK with the specified priority
func (am *AckManagerImpl) ScheduleAck(pathID string, priority AckPriority) error {
	// Use both coordinator and scheduler for comprehensive ACK management

	// First, coordinate with other paths
	err := am.coordinator.ScheduleAck(pathID, priority)
	if err != nil {
		return err
	}

	// Then, schedule the ACK for transmission
	am.pathsMutex.RLock()
	pathState, exists := am.paths[pathID]
	am.pathsMutex.RUnlock()

	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}

	// Get pending packets to acknowledge
	pathState.mutex.RLock()
	pendingPackets := make([]uint64, 0)
	for packetID, packetInfo := range pathState.ReceivedPackets {
		if !packetInfo.Acknowledged {
			pendingPackets = append(pendingPackets, packetID)
		}
	}
	pathState.mutex.RUnlock()

	if len(pendingPackets) > 0 {
		return am.scheduler.ScheduleAck(pathID, pendingPackets, priority)
	}

	return nil
}

// DetectPacketLoss detects packet loss on a specific path
func (am *AckManagerImpl) DetectPacketLoss(pathID string) ([]uint64, error) {
	am.pathsMutex.RLock()
	pathState, exists := am.paths[pathID]
	am.pathsMutex.RUnlock()

	if !exists {
		return nil, utils.NewPathNotFoundError(pathID)
	}

	// Delegate to loss detector
	return am.lossDetector.DetectLoss(pathState.LossDetectionState)
}

// TriggerRetransmission triggers retransmission for lost packets
func (am *AckManagerImpl) TriggerRetransmission(pathID string, packetIDs []uint64) error {
	am.pathsMutex.RLock()
	pathState, exists := am.paths[pathID]
	am.pathsMutex.RUnlock()

	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}

	// Update statistics
	pathState.mutex.Lock()
	pathState.Stats.PacketLossDetected += uint64(len(packetIDs))
	pathState.Stats.RetransmissionsTriggered += uint64(len(packetIDs))

	// Collect original data for retransmission
	originalData := make(map[uint64][]byte)
	for _, packetID := range packetIDs {
		if sentInfo, exists := pathState.SentPackets[packetID]; exists {
			sentInfo.Retransmitted = true
			// In a real implementation, we would store the original packet data
			// For now, we simulate with empty data
			originalData[packetID] = []byte{}
		}
	}
	pathState.mutex.Unlock()

	// Update global statistics
	am.statsMutex.Lock()
	am.globalStats.TotalPacketLoss += uint64(len(packetIDs))
	am.globalStats.TotalRetransmissions += uint64(len(packetIDs))
	am.statsMutex.Unlock()

	// Determine retransmission type and trigger appropriate mechanism
	lostPackets, err := am.lossDetector.DetectLoss(pathState.LossDetectionState)
	if err != nil {
		return err
	}

	// Check if these are fast retransmit candidates
	fastRetransmitPackets := make([]uint64, 0)
	timeoutPackets := make([]uint64, 0)

	for _, packetID := range packetIDs {
		// Check if packet is in the detected loss list (indicating fast retransmit)
		isFastRetransmit := false
		for _, lostPacketID := range lostPackets {
			if packetID == lostPacketID {
				isFastRetransmit = true
				break
			}
		}

		if isFastRetransmit {
			fastRetransmitPackets = append(fastRetransmitPackets, packetID)
		} else {
			timeoutPackets = append(timeoutPackets, packetID)
		}
	}

	// Trigger fast retransmission
	if len(fastRetransmitPackets) > 0 {
		err = am.retransmissionManager.TriggerFastRetransmission(pathID, fastRetransmitPackets, originalData)
		if err != nil {
			return err
		}
	}

	// Trigger timeout retransmission
	if len(timeoutPackets) > 0 {
		err = am.retransmissionManager.TriggerTimeoutRetransmission(pathID, timeoutPackets, originalData)
		if err != nil {
			return err
		}
	}

	return nil
}

// UpdateCongestionWindow updates the congestion window based on received ACK
func (am *AckManagerImpl) UpdateCongestionWindow(pathID string, ackFrame *data.AckFrame) error {
	am.pathsMutex.RLock()
	pathState, exists := am.paths[pathID]
	am.pathsMutex.RUnlock()

	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}

	// Delegate to congestion controller
	return am.congestionController.UpdateCongestionWindow(pathState.CongestionState, ackFrame)
}

// GetCongestionWindow returns the current congestion window size for a path
func (am *AckManagerImpl) GetCongestionWindow(pathID string) (uint64, error) {
	am.pathsMutex.RLock()
	pathState, exists := am.paths[pathID]
	am.pathsMutex.RUnlock()

	if !exists {
		return 0, utils.NewPathNotFoundError(pathID)
	}

	pathState.mutex.RLock()
	defer pathState.mutex.RUnlock()

	return pathState.CongestionState.CongestionWindow, nil
}

// AddSentPacket adds a sent packet for tracking
func (am *AckManagerImpl) AddSentPacket(pathID string, packetID uint64, size uint32) error {
	am.pathsMutex.RLock()
	pathState, exists := am.paths[pathID]
	am.pathsMutex.RUnlock()

	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}

	pathState.mutex.Lock()
	defer pathState.mutex.Unlock()

	pathState.SentPackets[packetID] = &SentPacketInfo{
		PacketNumber:  packetID,
		SentTime:      time.Now().UnixNano(),
		PacketSize:    size,
		Acknowledged:  false,
		Retransmitted: false,
	}

	return nil
}

// AddReceivedPacket adds a received packet for ACK generation
func (am *AckManagerImpl) AddReceivedPacket(pathID string, packetID uint64, size uint32) error {
	am.pathsMutex.RLock()
	pathState, exists := am.paths[pathID]
	am.pathsMutex.RUnlock()

	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}

	pathState.mutex.Lock()
	defer pathState.mutex.Unlock()

	pathState.ReceivedPackets[packetID] = &ReceivedPacketInfo{
		PacketID:     packetID,
		ReceivedTime: time.Now(),
		Size:         size,
		Acknowledged: false,
	}

	return nil
}

// ProcessScheduledAcks processes ACKs that are ready for transmission
func (am *AckManagerImpl) ProcessScheduledAcks() error {
	readyAcks, err := am.scheduler.ProcessScheduledAcks()
	if err != nil {
		return err
	}

	// Process each ready ACK
	for _, scheduledAck := range readyAcks {
		ackFrame, err := am.GenerateAck(scheduledAck.PathID, scheduledAck.PacketIDs)
		if err != nil {
			continue // Skip this ACK and continue with others
		}

		// In a real implementation, this would send the ACK frame
		// For now, we just mark it as processed
		_ = ackFrame
	}

	return nil
}

// GetCoordinationStats returns coordination statistics
func (am *AckManagerImpl) GetCoordinationStats() (*CoordinationStats, error) {
	coordinationEvents, lastCoordination := am.coordinator.GetCoordinationStats()

	return &CoordinationStats{
		CoordinationEvents: coordinationEvents,
		LastCoordination:   lastCoordination,
		ActivePaths:        len(am.paths),
	}, nil
}

// GetPathInterferenceLevel returns the interference level for a path
func (am *AckManagerImpl) GetPathInterferenceLevel(pathID string) (float64, error) {
	return am.coordinator.GetPathInterferenceLevel(pathID)
}

// CoordinationStats contains ACK coordination statistics
type CoordinationStats struct {
	CoordinationEvents uint64
	LastCoordination   time.Time
	ActivePaths        int
}

// OptimizeAckTiming optimizes ACK timing across all paths
func (am *AckManagerImpl) OptimizeAckTiming() error {
	am.pathsMutex.RLock()
	defer am.pathsMutex.RUnlock()

	// Collect path characteristics for optimization
	pathCharacteristics := make(map[string]*PathCharacteristics)
	for pathID, pathState := range am.paths {
		pathState.mutex.RLock()
		pathCharacteristics[pathID] = pathState.Characteristics
		pathState.mutex.RUnlock()
	}

	// Optimize ACK timing based on global view
	return am.optimizeGlobalAckTiming(pathCharacteristics)
}

// optimizeGlobalAckTiming optimizes ACK timing considering all paths
func (am *AckManagerImpl) optimizeGlobalAckTiming(pathCharacteristics map[string]*PathCharacteristics) error {
	// Find the best performing path
	var bestPath string
	var bestScore float64

	for pathID, characteristics := range pathCharacteristics {
		score := am.calculatePathScore(characteristics)
		if score > bestScore {
			bestScore = score
			bestPath = pathID
		}
	}

	// Adjust ACK timing for all paths relative to the best path
	for pathID, characteristics := range pathCharacteristics {
		if pathID == bestPath {
			continue // Don't adjust the best path
		}

		// Calculate adjustment factor
		pathScore := am.calculatePathScore(characteristics)
		adjustmentFactor := bestScore / pathScore

		// Apply adjustment to path's ACK strategy
		err := am.adjustPathAckTiming(pathID, adjustmentFactor)
		if err != nil {
			continue // Skip this path and continue with others
		}
	}

	return nil
}

// calculatePathScore calculates a performance score for a path
func (am *AckManagerImpl) calculatePathScore(characteristics *PathCharacteristics) float64 {
	score := 1.0

	// Higher bandwidth is better
	if characteristics.Bandwidth > 0 {
		score *= float64(characteristics.Bandwidth) / 1000000.0 // Normalize to Mbps
	}

	// Lower RTT is better
	if characteristics.RTT > 0 {
		score *= 1000.0 / float64(characteristics.RTT.Milliseconds())
	}

	// Lower packet loss is better
	score *= (1.0 - characteristics.PacketLossRate)

	// Lower congestion is better
	score *= (1.0 - characteristics.CongestionLevel)

	// Penalize unreliable paths
	if characteristics.IsUnreliable {
		score *= 0.5
	}

	// Penalize high latency paths
	if characteristics.IsHighLatency {
		score *= 0.7
	}

	// Penalize low bandwidth paths
	if characteristics.IsLowBandwidth {
		score *= 0.8
	}

	return score
}

// adjustPathAckTiming adjusts ACK timing for a specific path
func (am *AckManagerImpl) adjustPathAckTiming(pathID string, adjustmentFactor float64) error {
	am.pathsMutex.RLock()
	pathState, exists := am.paths[pathID]
	am.pathsMutex.RUnlock()

	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}

	pathState.mutex.Lock()
	defer pathState.mutex.Unlock()

	// Adjust ACK delay
	currentDelay := pathState.AckStrategy.AckDelay
	newDelay := time.Duration(float64(currentDelay) / adjustmentFactor)

	// Ensure delay is within bounds
	if newDelay < am.config.MinAckDelay {
		newDelay = am.config.MinAckDelay
	}
	if newDelay > am.config.MaxAckDelay {
		newDelay = am.config.MaxAckDelay
	}

	pathState.AckStrategy.AckDelay = newDelay

	// Adjust batching threshold
	currentThreshold := pathState.AckStrategy.BatchingThreshold
	newThreshold := int(float64(currentThreshold) * adjustmentFactor)

	// Ensure threshold is reasonable
	if newThreshold < 1 {
		newThreshold = 1
	}
	if newThreshold > am.config.AckBatchingThreshold*3 {
		newThreshold = am.config.AckBatchingThreshold * 3
	}

	pathState.AckStrategy.BatchingThreshold = newThreshold

	return nil
}

// BalanceAckLoad balances ACK load across all paths
func (am *AckManagerImpl) BalanceAckLoad() error {
	// Get current ACK rates for all paths
	pathLoads := make(map[string]float64)
	totalLoad := 0.0

	am.pathsMutex.RLock()
	for pathID, pathState := range am.paths {
		pathState.mutex.RLock()
		load := float64(pathState.Stats.AcksSent)
		pathLoads[pathID] = load
		totalLoad += load
		pathState.mutex.RUnlock()
	}
	am.pathsMutex.RUnlock()

	if totalLoad == 0 {
		return nil // No ACKs sent yet
	}

	// Calculate target load per path (equal distribution)
	pathCount := len(pathLoads)
	if pathCount == 0 {
		return nil
	}

	targetLoadPerPath := totalLoad / float64(pathCount)

	// Adjust ACK priorities based on current load
	for pathID, currentLoad := range pathLoads {
		loadRatio := currentLoad / targetLoadPerPath

		// Adjust priority based on load ratio
		var newPriority AckPriority
		if loadRatio > 1.5 {
			// Overloaded path - reduce priority
			newPriority = AckPriorityLow
		} else if loadRatio < 0.5 {
			// Underloaded path - increase priority
			newPriority = AckPriorityHigh
		} else {
			// Balanced path - normal priority
			newPriority = AckPriorityNormal
		}

		// Apply the priority adjustment through the coordinator
		am.coordinator.coordinationMutex.Lock()
		if pathState, exists := am.coordinator.activePaths[pathID]; exists {
			pathState.Priority = newPriority
		}
		am.coordinator.coordinationMutex.Unlock()
	}

	return nil
}
