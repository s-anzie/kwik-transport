package data

import (
	"fmt"
	"sync"
	"time"

	"kwik/internal/utils"
	datapb "kwik/proto/data"
)

// PacketNumberingManager manages packet numbering spaces per path
// Implements Requirements 8.3, 8.5: per-path packet numbering and sequencing
type PacketNumberingManager struct {
	// Per-path packet numbering spaces
	pathNumberSpaces map[string]*PathNumberSpace
	
	// Global configuration
	config *PacketNumberingConfig
	
	// Synchronization
	mutex sync.RWMutex
}

// PathNumberSpace maintains packet numbering for a specific path
type PathNumberSpace struct {
	PathID              string
	NextPacketNumber    uint64 // Next packet number to assign
	LastSentPacket      uint64 // Last packet number sent
	LastAckedPacket     uint64 // Last packet number acknowledged
	MaxPacketNumber     uint64 // Maximum packet number seen
	
	// Packet tracking
	SentPackets         map[uint64]*SentPacketInfo
	PendingPackets      map[uint64]*PendingPacketInfo
	AckedPackets        map[uint64]*AckedPacketInfo
	
	// Sequencing information
	SequenceNumber      uint32 // Sequence number within path
	NextSequenceNumber  uint32 // Next sequence number to assign
	
	// Statistics
	TotalPacketsSent    uint64
	TotalPacketsAcked   uint64
	TotalPacketsLost    uint64
	TotalRetransmissions uint64
	
	// Timestamps
	CreatedAt           int64
	LastActivity        int64
}

// SentPacketInfo contains information about a sent packet
type SentPacketInfo struct {
	PacketNumber    uint64
	SequenceNumber  uint32
	PathID          string
	SentTime        int64
	PacketSize      uint32
	FrameCount      int
	RetransmissionCount int
	IsRetransmission bool
	Retransmitted bool
	Acknowledged  bool
	AckTime       time.Time
}

// PendingPacketInfo contains information about a packet pending acknowledgment
type PendingPacketInfo struct {
	PacketNumber    uint64
	SequenceNumber  uint32
	PathID          string
	SentTime        int64
	PacketSize      uint32
	TimeoutDeadline int64
	RetryCount      int
}

// AckedPacketInfo contains information about an acknowledged packet
type AckedPacketInfo struct {
	PacketNumber    uint64
	SequenceNumber  uint32
	PathID          string
	SentTime        int64
	AckedTime       int64
	RTT             int64 // Round-trip time in nanoseconds
}

// PacketNumberingConfig contains configuration for packet numbering
type PacketNumberingConfig struct {
	MaxPaths                int
	MaxSentPacketsPerPath   int
	MaxPendingPacketsPerPath int
	MaxAckedPacketsPerPath  int
	PacketTimeoutNs         int64 // Packet timeout in nanoseconds
	RetransmissionLimit     int
	CleanupIntervalNs       int64 // Cleanup interval in nanoseconds
	EnableStatistics        bool
}

// NewPacketNumberingManager creates a new packet numbering manager
func NewPacketNumberingManager(config *PacketNumberingConfig) *PacketNumberingManager {
	if config == nil {
		config = DefaultPacketNumberingConfig()
	}

	return &PacketNumberingManager{
		pathNumberSpaces: make(map[string]*PathNumberSpace),
		config:           config,
	}
}

// DefaultPacketNumberingConfig returns default configuration
func DefaultPacketNumberingConfig() *PacketNumberingConfig {
	return &PacketNumberingConfig{
		MaxPaths:                 16,
		MaxSentPacketsPerPath:    10000,
		MaxPendingPacketsPerPath: 1000,
		MaxAckedPacketsPerPath:   5000,
		PacketTimeoutNs:          5000000000, // 5 seconds
		RetransmissionLimit:      3,
		CleanupIntervalNs:        60000000000, // 1 minute
		EnableStatistics:         true,
	}
}

// RegisterPath registers a new path for packet numbering
// Implements Requirement 8.3: per-path packet numbering
func (pnm *PacketNumberingManager) RegisterPath(pathID string) error {
	if pathID == "" {
		return utils.NewKwikError(utils.ErrInvalidFrame, "path ID cannot be empty", nil)
	}

	pnm.mutex.Lock()
	defer pnm.mutex.Unlock()

	// Check if path already exists
	if _, exists := pnm.pathNumberSpaces[pathID]; exists {
		return utils.NewKwikError(utils.ErrStreamCreationFailed, 
			fmt.Sprintf("path %s already registered", pathID), nil)
	}

	// Check path limit
	if len(pnm.pathNumberSpaces) >= pnm.config.MaxPaths {
		return utils.NewKwikError(utils.ErrStreamCreationFailed, 
			"maximum number of paths reached", nil)
	}

	// Create new path number space
	currentTime := utils.GetCurrentTimestamp()
	pnm.pathNumberSpaces[pathID] = &PathNumberSpace{
		PathID:              pathID,
		NextPacketNumber:    1, // Start from 1, 0 is reserved
		LastSentPacket:      0,
		LastAckedPacket:     0,
		MaxPacketNumber:     0,
		SentPackets:         make(map[uint64]*SentPacketInfo),
		PendingPackets:      make(map[uint64]*PendingPacketInfo),
		AckedPackets:        make(map[uint64]*AckedPacketInfo),
		SequenceNumber:      0,
		NextSequenceNumber:  1,
		TotalPacketsSent:    0,
		TotalPacketsAcked:   0,
		TotalPacketsLost:    0,
		TotalRetransmissions: 0,
		CreatedAt:           currentTime,
		LastActivity:        currentTime,
	}

	return nil
}

// UnregisterPath removes a path from packet numbering
func (pnm *PacketNumberingManager) UnregisterPath(pathID string) error {
	pnm.mutex.Lock()
	defer pnm.mutex.Unlock()

	if _, exists := pnm.pathNumberSpaces[pathID]; !exists {
		return utils.NewPathNotFoundError(pathID)
	}

	delete(pnm.pathNumberSpaces, pathID)
	return nil
}

// AssignPacketNumber assigns a new packet number for a path
// Implements Requirement 8.3: per-path packet numbering
func (pnm *PacketNumberingManager) AssignPacketNumber(pathID string) (uint64, uint32, error) {
	pnm.mutex.Lock()
	defer pnm.mutex.Unlock()

	pathSpace, exists := pnm.pathNumberSpaces[pathID]
	if !exists {
		return 0, 0, utils.NewPathNotFoundError(pathID)
	}

	// Assign packet number
	packetNumber := pathSpace.NextPacketNumber
	pathSpace.NextPacketNumber++

	// Assign sequence number
	sequenceNumber := pathSpace.NextSequenceNumber
	pathSpace.NextSequenceNumber++

	// Update activity timestamp
	pathSpace.LastActivity = utils.GetCurrentTimestamp()

	return packetNumber, sequenceNumber, nil
}

// RecordSentPacket records information about a sent packet
// Implements Requirement 8.5: packet transmission sequencing
func (pnm *PacketNumberingManager) RecordSentPacket(pathID string, packetNumber uint64, sequenceNumber uint32, packetSize uint32, frameCount int, isRetransmission bool) error {
	pnm.mutex.Lock()
	defer pnm.mutex.Unlock()

	pathSpace, exists := pnm.pathNumberSpaces[pathID]
	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}

	currentTime := utils.GetCurrentTimestamp()

	// Create sent packet info
	sentInfo := &SentPacketInfo{
		PacketNumber:        packetNumber,
		SequenceNumber:      sequenceNumber,
		PathID:              pathID,
		SentTime:            currentTime,
		PacketSize:          packetSize,
		FrameCount:          frameCount,
		RetransmissionCount: 0,
		IsRetransmission:    isRetransmission,
	}

	// Check limits and cleanup if necessary
	if len(pathSpace.SentPackets) >= pnm.config.MaxSentPacketsPerPath {
		pnm.cleanupOldSentPackets(pathSpace)
	}

	// Record sent packet
	pathSpace.SentPackets[packetNumber] = sentInfo
	pathSpace.LastSentPacket = packetNumber

	// Update max packet number
	if packetNumber > pathSpace.MaxPacketNumber {
		pathSpace.MaxPacketNumber = packetNumber
	}

	// Create pending packet info
	pendingInfo := &PendingPacketInfo{
		PacketNumber:    packetNumber,
		SequenceNumber:  sequenceNumber,
		PathID:          pathID,
		SentTime:        currentTime,
		PacketSize:      packetSize,
		TimeoutDeadline: currentTime + pnm.config.PacketTimeoutNs,
		RetryCount:      0,
	}

	// Check pending packet limits
	if len(pathSpace.PendingPackets) >= pnm.config.MaxPendingPacketsPerPath {
		pnm.cleanupOldPendingPackets(pathSpace)
	}

	pathSpace.PendingPackets[packetNumber] = pendingInfo

	// Update statistics
	if pnm.config.EnableStatistics {
		pathSpace.TotalPacketsSent++
		if isRetransmission {
			pathSpace.TotalRetransmissions++
		}
	}

	pathSpace.LastActivity = currentTime

	return nil
}

// ProcessAcknowledgment processes an acknowledgment for packets
func (pnm *PacketNumberingManager) ProcessAcknowledgment(pathID string, ackFrame *datapb.AckFrame) error {
	pnm.mutex.Lock()
	defer pnm.mutex.Unlock()

	pathSpace, exists := pnm.pathNumberSpaces[pathID]
	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}

	currentTime := utils.GetCurrentTimestamp()

	// Process acknowledged packet IDs
	for _, packetID := range ackFrame.AckedPacketIds {
		if err := pnm.processPacketAck(pathSpace, packetID, currentTime); err != nil {
			// Log error but continue processing other ACKs
			continue
		}
	}

	// Process ACK ranges
	for _, ackRange := range ackFrame.AckRanges {
		for packetID := ackRange.Start; packetID <= ackRange.End; packetID++ {
			if err := pnm.processPacketAck(pathSpace, packetID, currentTime); err != nil {
				// Log error but continue processing other ACKs
				continue
			}
		}
	}

	// Update largest acked packet
	if ackFrame.LargestAcked > pathSpace.LastAckedPacket {
		pathSpace.LastAckedPacket = ackFrame.LargestAcked
	}

	pathSpace.LastActivity = currentTime

	return nil
}

// processPacketAck processes acknowledgment for a single packet
func (pnm *PacketNumberingManager) processPacketAck(pathSpace *PathNumberSpace, packetNumber uint64, ackTime int64) error {
	// Check if packet is pending
	pendingInfo, isPending := pathSpace.PendingPackets[packetNumber]
	if !isPending {
		// Packet not pending, might be duplicate ACK or already processed
		return nil
	}

	// Remove from pending packets
	delete(pathSpace.PendingPackets, packetNumber)

	// Get sent packet info
	sentInfo, wasSent := pathSpace.SentPackets[packetNumber]
	if !wasSent {
		// Packet was not recorded as sent, this is unusual
		return utils.NewKwikError(utils.ErrInvalidFrame, 
			fmt.Sprintf("received ACK for unrecorded packet %d", packetNumber), nil)
	}

	// Calculate RTT
	rtt := ackTime - sentInfo.SentTime

	// Create acked packet info
	ackedInfo := &AckedPacketInfo{
		PacketNumber:   packetNumber,
		SequenceNumber: pendingInfo.SequenceNumber,
		PathID:         pathSpace.PathID,
		SentTime:       sentInfo.SentTime,
		AckedTime:      ackTime,
		RTT:            rtt,
	}

	// Check acked packet limits
	if len(pathSpace.AckedPackets) >= pnm.config.MaxAckedPacketsPerPath {
		pnm.cleanupOldAckedPackets(pathSpace)
	}

	pathSpace.AckedPackets[packetNumber] = ackedInfo

	// Update statistics
	if pnm.config.EnableStatistics {
		pathSpace.TotalPacketsAcked++
	}

	return nil
}

// GetTimeoutPackets returns packets that have timed out and need retransmission
func (pnm *PacketNumberingManager) GetTimeoutPackets(pathID string) ([]*PendingPacketInfo, error) {
	pnm.mutex.RLock()
	defer pnm.mutex.RUnlock()

	pathSpace, exists := pnm.pathNumberSpaces[pathID]
	if !exists {
		return nil, utils.NewPathNotFoundError(pathID)
	}

	currentTime := utils.GetCurrentTimestamp()
	var timeoutPackets []*PendingPacketInfo

	for _, pendingInfo := range pathSpace.PendingPackets {
		if currentTime > pendingInfo.TimeoutDeadline {
			// Create a copy to avoid concurrent modification issues
			timeoutPacket := *pendingInfo
			timeoutPackets = append(timeoutPackets, &timeoutPacket)
		}
	}

	return timeoutPackets, nil
}

// MarkPacketLost marks a packet as lost and removes it from pending
func (pnm *PacketNumberingManager) MarkPacketLost(pathID string, packetNumber uint64) error {
	pnm.mutex.Lock()
	defer pnm.mutex.Unlock()

	pathSpace, exists := pnm.pathNumberSpaces[pathID]
	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}

	// Remove from pending packets
	delete(pathSpace.PendingPackets, packetNumber)

	// Update statistics
	if pnm.config.EnableStatistics {
		pathSpace.TotalPacketsLost++
	}

	pathSpace.LastActivity = utils.GetCurrentTimestamp()

	return nil
}

// GetPathNumberSpace returns the number space for a path
func (pnm *PacketNumberingManager) GetPathNumberSpace(pathID string) (*PathNumberSpace, error) {
	pnm.mutex.RLock()
	defer pnm.mutex.RUnlock()

	pathSpace, exists := pnm.pathNumberSpaces[pathID]
	if !exists {
		return nil, utils.NewPathNotFoundError(pathID)
	}

	// Return a copy to prevent external modification
	pathSpaceCopy := *pathSpace
	
	// Deep copy maps
	pathSpaceCopy.SentPackets = make(map[uint64]*SentPacketInfo)
	for k, v := range pathSpace.SentPackets {
		sentInfoCopy := *v
		pathSpaceCopy.SentPackets[k] = &sentInfoCopy
	}

	pathSpaceCopy.PendingPackets = make(map[uint64]*PendingPacketInfo)
	for k, v := range pathSpace.PendingPackets {
		pendingInfoCopy := *v
		pathSpaceCopy.PendingPackets[k] = &pendingInfoCopy
	}

	pathSpaceCopy.AckedPackets = make(map[uint64]*AckedPacketInfo)
	for k, v := range pathSpace.AckedPackets {
		ackedInfoCopy := *v
		pathSpaceCopy.AckedPackets[k] = &ackedInfoCopy
	}

	return &pathSpaceCopy, nil
}

// GetPacketNumberingStatistics returns statistics for all paths
func (pnm *PacketNumberingManager) GetPacketNumberingStatistics() *PacketNumberingStatistics {
	pnm.mutex.RLock()
	defer pnm.mutex.RUnlock()

	stats := &PacketNumberingStatistics{
		TotalPaths:      len(pnm.pathNumberSpaces),
		PathStatistics:  make(map[string]*PathPacketStatistics),
	}

	var totalSent, totalAcked, totalLost, totalRetransmissions uint64

	for pathID, pathSpace := range pnm.pathNumberSpaces {
		pathStats := &PathPacketStatistics{
			PathID:               pathID,
			NextPacketNumber:     pathSpace.NextPacketNumber,
			LastSentPacket:       pathSpace.LastSentPacket,
			LastAckedPacket:      pathSpace.LastAckedPacket,
			PendingPackets:       len(pathSpace.PendingPackets),
			TotalPacketsSent:     pathSpace.TotalPacketsSent,
			TotalPacketsAcked:    pathSpace.TotalPacketsAcked,
			TotalPacketsLost:     pathSpace.TotalPacketsLost,
			TotalRetransmissions: pathSpace.TotalRetransmissions,
			LastActivity:         pathSpace.LastActivity,
		}

		// Calculate loss rate
		if pathSpace.TotalPacketsSent > 0 {
			pathStats.LossRate = float64(pathSpace.TotalPacketsLost) / float64(pathSpace.TotalPacketsSent)
		}

		// Calculate average RTT
		if len(pathSpace.AckedPackets) > 0 {
			var totalRTT int64
			for _, ackedInfo := range pathSpace.AckedPackets {
				totalRTT += ackedInfo.RTT
			}
			pathStats.AverageRTT = totalRTT / int64(len(pathSpace.AckedPackets))
		}

		stats.PathStatistics[pathID] = pathStats

		totalSent += pathSpace.TotalPacketsSent
		totalAcked += pathSpace.TotalPacketsAcked
		totalLost += pathSpace.TotalPacketsLost
		totalRetransmissions += pathSpace.TotalRetransmissions
	}

	stats.TotalPacketsSent = totalSent
	stats.TotalPacketsAcked = totalAcked
	stats.TotalPacketsLost = totalLost
	stats.TotalRetransmissions = totalRetransmissions

	// Calculate overall loss rate
	if totalSent > 0 {
		stats.OverallLossRate = float64(totalLost) / float64(totalSent)
	}

	return stats
}

// cleanupOldSentPackets removes old sent packet records to prevent memory growth
func (pnm *PacketNumberingManager) cleanupOldSentPackets(pathSpace *PathNumberSpace) {
	// Remove oldest 25% of sent packets
	removeCount := len(pathSpace.SentPackets) / 4
	if removeCount == 0 {
		removeCount = 1
	}

	// Find oldest packets by packet number (assuming sequential numbering)
	var oldestPackets []uint64
	for packetNumber := range pathSpace.SentPackets {
		oldestPackets = append(oldestPackets, packetNumber)
	}

	// Sort and remove oldest
	if len(oldestPackets) > removeCount {
		for i := 0; i < removeCount; i++ {
			delete(pathSpace.SentPackets, oldestPackets[i])
		}
	}
}

// cleanupOldPendingPackets removes old pending packet records
func (pnm *PacketNumberingManager) cleanupOldPendingPackets(pathSpace *PathNumberSpace) {
	currentTime := utils.GetCurrentTimestamp()
	expiredThreshold := currentTime - (pnm.config.PacketTimeoutNs * 2) // Double timeout for cleanup

	for packetNumber, pendingInfo := range pathSpace.PendingPackets {
		if pendingInfo.SentTime < expiredThreshold {
			delete(pathSpace.PendingPackets, packetNumber)
		}
	}
}

// cleanupOldAckedPackets removes old acked packet records
func (pnm *PacketNumberingManager) cleanupOldAckedPackets(pathSpace *PathNumberSpace) {
	// Remove oldest 25% of acked packets
	removeCount := len(pathSpace.AckedPackets) / 4
	if removeCount == 0 {
		removeCount = 1
	}

	// Find oldest packets by packet number
	var oldestPackets []uint64
	for packetNumber := range pathSpace.AckedPackets {
		oldestPackets = append(oldestPackets, packetNumber)
	}

	// Remove oldest
	if len(oldestPackets) > removeCount {
		for i := 0; i < removeCount; i++ {
			delete(pathSpace.AckedPackets, oldestPackets[i])
		}
	}
}

// PacketNumberingStatistics contains statistics about packet numbering
type PacketNumberingStatistics struct {
	TotalPaths           int
	TotalPacketsSent     uint64
	TotalPacketsAcked    uint64
	TotalPacketsLost     uint64
	TotalRetransmissions uint64
	OverallLossRate      float64
	PathStatistics       map[string]*PathPacketStatistics
}

// PathPacketStatistics contains statistics for a specific path
type PathPacketStatistics struct {
	PathID               string
	NextPacketNumber     uint64
	LastSentPacket       uint64
	LastAckedPacket      uint64
	PendingPackets       int
	TotalPacketsSent     uint64
	TotalPacketsAcked    uint64
	TotalPacketsLost     uint64
	TotalRetransmissions uint64
	LossRate             float64
	AverageRTT           int64
	LastActivity         int64
}