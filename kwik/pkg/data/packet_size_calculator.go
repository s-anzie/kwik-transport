package data

import (
	"fmt"
	"sync"

	"kwik/internal/utils"
	datapb "kwik/proto/data"
)

// PacketSizeCalculator handles packet size calculation and management
// Implements Requirements 13.1, 13.3: respects QUIC transport limits and prevents KWIK/QUIC offset confusion
type PacketSizeCalculator struct {
	// Path-specific configurations
	pathConfigs map[string]*PathSizeConfig
	
	// Global configuration
	globalConfig *GlobalSizeConfig
	
	// Synchronization
	mutex sync.RWMutex
}

// PathSizeConfig contains size configuration for a specific path
type PathSizeConfig struct {
	PathID           string
	QuicMaxPacketSize uint32 // Maximum packet size QUIC can transport on this path
	MTU              uint32 // Path MTU
	HeaderOverhead   uint32 // Total header overhead (QUIC + UDP + IP)
	KwikOverhead     uint32 // KWIK protocol overhead
	ProtobufOverhead uint32 // Protobuf serialization overhead
	EffectiveMaxSize uint32 // Calculated effective maximum size for KWIK data
	LastUpdated      int64  // Timestamp of last update
}

// GlobalSizeConfig contains global size configuration
type GlobalSizeConfig struct {
	DefaultQuicMaxSize uint32 // Default QUIC maximum packet size
	DefaultMTU         uint32 // Default MTU
	MinPacketSize      uint32 // Minimum allowed packet size
	MaxPacketSize      uint32 // Maximum allowed packet size
	SafetyMargin       uint32 // Safety margin to prevent edge cases
	FragmentThreshold  uint32 // Threshold for automatic fragmentation
}

// PacketSizeInfo contains calculated size information for a packet
type PacketSizeInfo struct {
	PathID              string
	RequestedSize       uint32
	CalculatedSize      uint32
	EffectiveDataSize   uint32
	HeaderSize          uint32
	OverheadSize        uint32
	WillFitInQuic       bool
	RequiresFragmentation bool
	FragmentCount       int
	RecommendedMaxSize  uint32
}

// NewPacketSizeCalculator creates a new packet size calculator
func NewPacketSizeCalculator(config *GlobalSizeConfig) *PacketSizeCalculator {
	if config == nil {
		config = DefaultGlobalSizeConfig()
	}

	return &PacketSizeCalculator{
		pathConfigs:  make(map[string]*PathSizeConfig),
		globalConfig: config,
	}
}

// DefaultGlobalSizeConfig returns default global size configuration
func DefaultGlobalSizeConfig() *GlobalSizeConfig {
	return &GlobalSizeConfig{
		DefaultQuicMaxSize: utils.DefaultMaxPacketSize,
		DefaultMTU:         1500, // Standard Ethernet MTU
		MinPacketSize:      64,   // Minimum viable packet size
		MaxPacketSize:      9000, // Jumbo frame size
		SafetyMargin:       32,   // Safety margin for edge cases
		FragmentThreshold:  1024, // Auto-fragment packets larger than this
	}
}

// RegisterPath registers size configuration for a specific path
// Implements Requirement 13.1: calculate packet sizes per path relative to QUIC transport limits
func (psc *PacketSizeCalculator) RegisterPath(pathID string, quicMaxSize, mtu uint32) error {
	if pathID == "" {
		return utils.NewKwikError(utils.ErrInvalidFrame, "path ID cannot be empty", nil)
	}

	if quicMaxSize == 0 {
		quicMaxSize = psc.globalConfig.DefaultQuicMaxSize
	}

	if mtu == 0 {
		mtu = psc.globalConfig.DefaultMTU
	}

	// Calculate overheads
	headerOverhead := psc.calculateHeaderOverhead(mtu)
	kwikOverhead := uint32(utils.KwikHeaderSize)
	protobufOverhead := uint32(utils.ProtobufOverhead)

	// Calculate effective maximum size
	effectiveMaxSize := psc.calculateEffectiveMaxSize(quicMaxSize, headerOverhead, kwikOverhead, protobufOverhead)

	config := &PathSizeConfig{
		PathID:           pathID,
		QuicMaxPacketSize: quicMaxSize,
		MTU:              mtu,
		HeaderOverhead:   headerOverhead,
		KwikOverhead:     kwikOverhead,
		ProtobufOverhead: protobufOverhead,
		EffectiveMaxSize: effectiveMaxSize,
		LastUpdated:      utils.GetCurrentTimestamp(),
	}

	psc.mutex.Lock()
	defer psc.mutex.Unlock()

	psc.pathConfigs[pathID] = config

	return nil
}

// UnregisterPath removes size configuration for a path
func (psc *PacketSizeCalculator) UnregisterPath(pathID string) error {
	psc.mutex.Lock()
	defer psc.mutex.Unlock()

	if _, exists := psc.pathConfigs[pathID]; !exists {
		return utils.NewPathNotFoundError(pathID)
	}

	delete(psc.pathConfigs, pathID)
	return nil
}

// CalculatePacketSize calculates the size information for a packet
// Implements Requirement 13.3: ensure packets respect QUIC limits
func (psc *PacketSizeCalculator) CalculatePacketSize(pathID string, frames []*datapb.DataFrame) (*PacketSizeInfo, error) {
	psc.mutex.RLock()
	pathConfig, exists := psc.pathConfigs[pathID]
	psc.mutex.RUnlock()

	if !exists {
		return nil, utils.NewPathNotFoundError(pathID)
	}

	// Calculate total frame size
	totalFrameSize := uint32(0)
	for _, frame := range frames {
		frameSize, err := psc.calculateFrameSize(frame)
		if err != nil {
			return nil, utils.NewKwikError(utils.ErrInvalidFrame, 
				"failed to calculate frame size", err)
		}
		totalFrameSize += frameSize
	}

	// Calculate packet overhead
	packetOverhead := psc.calculatePacketOverhead(pathConfig)
	
	// Calculate total packet size
	totalPacketSize := totalFrameSize + packetOverhead
	
	// Determine if packet fits in QUIC limits
	willFitInQuic := totalPacketSize <= pathConfig.QuicMaxPacketSize
	
	// Determine if fragmentation is required
	requiresFragmentation := totalPacketSize > pathConfig.EffectiveMaxSize
	
	// Calculate fragment count if needed
	fragmentCount := 1
	if requiresFragmentation {
		fragmentCount = int((totalPacketSize + pathConfig.EffectiveMaxSize - 1) / pathConfig.EffectiveMaxSize)
	}

	info := &PacketSizeInfo{
		PathID:                pathID,
		RequestedSize:         totalFrameSize,
		CalculatedSize:        totalPacketSize,
		EffectiveDataSize:     totalFrameSize,
		HeaderSize:            pathConfig.HeaderOverhead,
		OverheadSize:          packetOverhead,
		WillFitInQuic:         willFitInQuic,
		RequiresFragmentation: requiresFragmentation,
		FragmentCount:         fragmentCount,
		RecommendedMaxSize:    pathConfig.EffectiveMaxSize,
	}

	return info, nil
}

// CalculateMaxDataSize calculates the maximum data size that can fit in a packet for a path
// Implements Requirement 13.1: calculate sizes relative to QUIC transport limits
func (psc *PacketSizeCalculator) CalculateMaxDataSize(pathID string) (uint32, error) {
	psc.mutex.RLock()
	pathConfig, exists := psc.pathConfigs[pathID]
	psc.mutex.RUnlock()

	if !exists {
		return 0, utils.NewPathNotFoundError(pathID)
	}

	return pathConfig.EffectiveMaxSize, nil
}

// ValidatePacketSize validates that a packet size is acceptable for a path
// Implements Requirement 13.3: prevent packets from exceeding QUIC limits
func (psc *PacketSizeCalculator) ValidatePacketSize(pathID string, packetSize uint32) error {
	psc.mutex.RLock()
	pathConfig, exists := psc.pathConfigs[pathID]
	psc.mutex.RUnlock()

	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}

	if packetSize > pathConfig.QuicMaxPacketSize {
		return utils.NewPacketTooLargeError(packetSize, pathConfig.QuicMaxPacketSize)
	}

	if packetSize < psc.globalConfig.MinPacketSize {
		return utils.NewKwikError(utils.ErrPacketTooLarge, 
			fmt.Sprintf("packet size %d is below minimum %d", packetSize, psc.globalConfig.MinPacketSize), nil)
	}

	return nil
}

// GetPathSizeConfig returns the size configuration for a path
func (psc *PacketSizeCalculator) GetPathSizeConfig(pathID string) (*PathSizeConfig, error) {
	psc.mutex.RLock()
	defer psc.mutex.RUnlock()

	config, exists := psc.pathConfigs[pathID]
	if !exists {
		return nil, utils.NewPathNotFoundError(pathID)
	}

	// Return a copy to prevent external modification
	configCopy := *config
	return &configCopy, nil
}

// UpdatePathMTU updates the MTU for a path and recalculates sizes
func (psc *PacketSizeCalculator) UpdatePathMTU(pathID string, newMTU uint32) error {
	psc.mutex.Lock()
	defer psc.mutex.Unlock()

	config, exists := psc.pathConfigs[pathID]
	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}

	// Update MTU
	config.MTU = newMTU
	
	// Recalculate dependent values
	config.HeaderOverhead = psc.calculateHeaderOverhead(newMTU)
	config.EffectiveMaxSize = psc.calculateEffectiveMaxSize(
		config.QuicMaxPacketSize,
		config.HeaderOverhead,
		config.KwikOverhead,
		config.ProtobufOverhead,
	)
	config.LastUpdated = utils.GetCurrentTimestamp()

	return nil
}

// GetOptimalFragmentSize calculates the optimal fragment size for a path
func (psc *PacketSizeCalculator) GetOptimalFragmentSize(pathID string) (uint32, error) {
	psc.mutex.RLock()
	pathConfig, exists := psc.pathConfigs[pathID]
	psc.mutex.RUnlock()

	if !exists {
		return 0, utils.NewPathNotFoundError(pathID)
	}

	// Use 80% of effective max size to allow for some overhead variation
	optimalSize := uint32(float64(pathConfig.EffectiveMaxSize) * 0.8)
	
	// Ensure it's not below minimum
	if optimalSize < psc.globalConfig.MinPacketSize {
		optimalSize = psc.globalConfig.MinPacketSize
	}

	return optimalSize, nil
}

// calculateHeaderOverhead calculates the total header overhead for a given MTU
func (psc *PacketSizeCalculator) calculateHeaderOverhead(mtu uint32) uint32 {
	// Standard overhead: IP header (20-40 bytes) + UDP header (8 bytes) + QUIC header (variable, ~20-50 bytes)
	// We use conservative estimates
	ipHeaderSize := uint32(40)  // IPv6 header size (larger than IPv4)
	udpHeaderSize := uint32(8)  // UDP header size
	quicHeaderSize := uint32(50) // Conservative QUIC header estimate
	
	return ipHeaderSize + udpHeaderSize + quicHeaderSize
}

// calculateEffectiveMaxSize calculates the effective maximum size for KWIK data
func (psc *PacketSizeCalculator) calculateEffectiveMaxSize(quicMaxSize, headerOverhead, kwikOverhead, protobufOverhead uint32) uint32 {
	totalOverhead := headerOverhead + kwikOverhead + protobufOverhead + psc.globalConfig.SafetyMargin
	
	if quicMaxSize <= totalOverhead {
		return psc.globalConfig.MinPacketSize
	}
	
	return quicMaxSize - totalOverhead
}

// calculateFrameSize calculates the serialized size of a data frame
func (psc *PacketSizeCalculator) calculateFrameSize(frame *datapb.DataFrame) (uint32, error) {
	// Estimate protobuf serialized size
	// This is an approximation - in practice, you might want to actually serialize
	baseSize := uint32(64) // Base protobuf overhead for frame metadata
	dataSize := uint32(len(frame.Data))
	pathIDSize := uint32(len(frame.PathId))
	
	return baseSize + dataSize + pathIDSize, nil
}

// calculatePacketOverhead calculates the overhead for a packet container
func (psc *PacketSizeCalculator) calculatePacketOverhead(config *PathSizeConfig) uint32 {
	return config.KwikOverhead + config.ProtobufOverhead
}

// GetAllPathConfigs returns all registered path configurations
func (psc *PacketSizeCalculator) GetAllPathConfigs() map[string]*PathSizeConfig {
	psc.mutex.RLock()
	defer psc.mutex.RUnlock()

	configs := make(map[string]*PathSizeConfig)
	for pathID, config := range psc.pathConfigs {
		configCopy := *config
		configs[pathID] = &configCopy
	}

	return configs
}

// GetSizeStatistics returns statistics about packet sizes across all paths
func (psc *PacketSizeCalculator) GetSizeStatistics() *SizeStatistics {
	psc.mutex.RLock()
	defer psc.mutex.RUnlock()

	stats := &SizeStatistics{
		TotalPaths:     len(psc.pathConfigs),
		PathStatistics: make(map[string]*PathSizeStatistics),
	}

	var totalEffectiveSize uint64
	var minEffectiveSize, maxEffectiveSize uint32 = ^uint32(0), 0

	for pathID, config := range psc.pathConfigs {
		pathStats := &PathSizeStatistics{
			PathID:           pathID,
			QuicMaxSize:      config.QuicMaxPacketSize,
			EffectiveMaxSize: config.EffectiveMaxSize,
			MTU:              config.MTU,
			TotalOverhead:    config.HeaderOverhead + config.KwikOverhead + config.ProtobufOverhead,
			LastUpdated:      config.LastUpdated,
		}

		stats.PathStatistics[pathID] = pathStats
		
		totalEffectiveSize += uint64(config.EffectiveMaxSize)
		if config.EffectiveMaxSize < minEffectiveSize {
			minEffectiveSize = config.EffectiveMaxSize
		}
		if config.EffectiveMaxSize > maxEffectiveSize {
			maxEffectiveSize = config.EffectiveMaxSize
		}
	}

	if len(psc.pathConfigs) > 0 {
		stats.AverageEffectiveSize = uint32(totalEffectiveSize / uint64(len(psc.pathConfigs)))
		stats.MinEffectiveSize = minEffectiveSize
		stats.MaxEffectiveSize = maxEffectiveSize
	}

	return stats
}

// SizeStatistics contains statistics about packet sizes
type SizeStatistics struct {
	TotalPaths           int
	AverageEffectiveSize uint32
	MinEffectiveSize     uint32
	MaxEffectiveSize     uint32
	PathStatistics       map[string]*PathSizeStatistics
}

// PathSizeStatistics contains statistics for a specific path
type PathSizeStatistics struct {
	PathID           string
	QuicMaxSize      uint32
	EffectiveMaxSize uint32
	MTU              uint32
	TotalOverhead    uint32
	LastUpdated      int64
}

// Helper function to get current timestamp (would be implemented in utils)
func getCurrentTimestamp() int64 {
	return utils.GetCurrentTimestamp()
}