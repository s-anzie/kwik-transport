package data

import (
	"fmt"
	"sync"

	"kwik/internal/utils"
)

// OffsetManager manages the distinction between KWIK logical offsets and QUIC physical offsets
// Implements Requirements 13.2, 13.4, 13.5: maintains separate KWIK logical and QUIC physical offsets
type OffsetManager struct {
	// KWIK logical offsets per logical stream
	logicalOffsets map[uint64]*LogicalStreamOffsets
	
	// QUIC physical offsets per path and real stream
	physicalOffsets map[string]map[uint64]*PhysicalStreamOffsets
	
	// Mapping between KWIK logical offsets and QUIC physical offsets
	offsetMappings map[string]*OffsetMapping
	
	// Configuration
	config *OffsetManagerConfig
	
	// Synchronization
	mutex sync.RWMutex
}

// LogicalStreamOffsets tracks offsets for a KWIK logical stream
type LogicalStreamOffsets struct {
	LogicalStreamID uint64
	NextWriteOffset uint64 // Next offset for writing data
	NextReadOffset  uint64 // Next expected offset for reading
	MaxOffset       uint64 // Maximum offset seen
	Gaps            []OffsetGap // Gaps in received data
	LastUpdated     int64
}

// PhysicalStreamOffsets tracks offsets for a QUIC physical stream
type PhysicalStreamOffsets struct {
	PathID          string
	RealStreamID    uint64
	NextWriteOffset uint64 // Next QUIC offset for writing
	NextReadOffset  uint64 // Next expected QUIC offset for reading
	MaxOffset       uint64 // Maximum QUIC offset seen
	BytesWritten    uint64 // Total bytes written to this stream
	BytesRead       uint64 // Total bytes read from this stream
	LastUpdated     int64
}

// OffsetMapping maintains the mapping between KWIK logical and QUIC physical offsets
type OffsetMapping struct {
	LogicalStreamID uint64
	PathID          string
	RealStreamID    uint64
	
	// Mapping from KWIK logical offset to QUIC physical offset
	LogicalToPhysical map[uint64]uint64
	
	// Mapping from QUIC physical offset to KWIK logical offset
	PhysicalToLogical map[uint64]uint64
	
	// Fragmentation tracking
	FragmentMappings map[uint64]*FragmentMapping
	
	LastUpdated int64
}

// FragmentMapping tracks how a logical frame is fragmented across physical packets
type FragmentMapping struct {
	LogicalOffset    uint64
	LogicalLength    uint32
	PhysicalFragments []PhysicalFragment
}

// PhysicalFragment represents a fragment of logical data in physical space
type PhysicalFragment struct {
	PhysicalOffset uint64
	PhysicalLength uint32
	FragmentIndex  int
	TotalFragments int
}

// Note: OffsetGap is defined in offset_coordinator.go

// OffsetManagerConfig contains configuration for offset management
type OffsetManagerConfig struct {
	MaxLogicalStreams   int
	MaxPhysicalStreams  int
	MaxMappingEntries   int
	GapTrackingEnabled  bool
	FragmentationEnabled bool
	CleanupInterval     int64 // Nanoseconds
}

// NewOffsetManager creates a new offset manager
func NewOffsetManager(config *OffsetManagerConfig) *OffsetManager {
	if config == nil {
		config = DefaultOffsetManagerConfig()
	}

	return &OffsetManager{
		logicalOffsets:  make(map[uint64]*LogicalStreamOffsets),
		physicalOffsets: make(map[string]map[uint64]*PhysicalStreamOffsets),
		offsetMappings:  make(map[string]*OffsetMapping),
		config:          config,
	}
}

// DefaultOffsetManagerConfig returns default configuration
func DefaultOffsetManagerConfig() *OffsetManagerConfig {
	return &OffsetManagerConfig{
		MaxLogicalStreams:    10000,
		MaxPhysicalStreams:   1000,
		MaxMappingEntries:    100000,
		GapTrackingEnabled:   true,
		FragmentationEnabled: true,
		CleanupInterval:      300000000000, // 5 minutes in nanoseconds
	}
}

// RegisterLogicalStream registers a new logical stream for offset tracking
// Implements Requirement 13.2: maintain separate KWIK logical offsets
func (om *OffsetManager) RegisterLogicalStream(logicalStreamID uint64) error {
	if logicalStreamID == 0 {
		return utils.NewKwikError(utils.ErrInvalidFrame, "logical stream ID cannot be zero", nil)
	}

	om.mutex.Lock()
	defer om.mutex.Unlock()

	// Check if stream already exists
	if _, exists := om.logicalOffsets[logicalStreamID]; exists {
		return utils.NewKwikError(utils.ErrStreamCreationFailed, 
			fmt.Sprintf("logical stream %d already registered", logicalStreamID), nil)
	}

	// Check limits
	if len(om.logicalOffsets) >= om.config.MaxLogicalStreams {
		return utils.NewKwikError(utils.ErrStreamCreationFailed, 
			"maximum number of logical streams reached", nil)
	}

	// Create logical stream offsets
	om.logicalOffsets[logicalStreamID] = &LogicalStreamOffsets{
		LogicalStreamID: logicalStreamID,
		NextWriteOffset: 0,
		NextReadOffset:  0,
		MaxOffset:       0,
		Gaps:            make([]OffsetGap, 0),
		LastUpdated:     utils.GetCurrentTimestamp(),
	}

	return nil
}

// RegisterPhysicalStream registers a new physical stream for offset tracking
// Implements Requirement 13.4: maintain QUIC physical offsets
func (om *OffsetManager) RegisterPhysicalStream(pathID string, realStreamID uint64) error {
	if pathID == "" {
		return utils.NewKwikError(utils.ErrInvalidFrame, "path ID cannot be empty", nil)
	}

	if realStreamID == 0 {
		return utils.NewKwikError(utils.ErrInvalidFrame, "real stream ID cannot be zero", nil)
	}

	om.mutex.Lock()
	defer om.mutex.Unlock()

	// Initialize path map if needed
	if om.physicalOffsets[pathID] == nil {
		om.physicalOffsets[pathID] = make(map[uint64]*PhysicalStreamOffsets)
	}

	// Check if stream already exists
	if _, exists := om.physicalOffsets[pathID][realStreamID]; exists {
		return utils.NewKwikError(utils.ErrStreamCreationFailed, 
			fmt.Sprintf("physical stream %d on path %s already registered", realStreamID, pathID), nil)
	}

	// Check limits
	totalPhysicalStreams := 0
	for _, pathStreams := range om.physicalOffsets {
		totalPhysicalStreams += len(pathStreams)
	}
	if totalPhysicalStreams >= om.config.MaxPhysicalStreams {
		return utils.NewKwikError(utils.ErrStreamCreationFailed, 
			"maximum number of physical streams reached", nil)
	}

	// Create physical stream offsets
	om.physicalOffsets[pathID][realStreamID] = &PhysicalStreamOffsets{
		PathID:          pathID,
		RealStreamID:    realStreamID,
		NextWriteOffset: 0,
		NextReadOffset:  0,
		MaxOffset:       0,
		BytesWritten:    0,
		BytesRead:       0,
		LastUpdated:     utils.GetCurrentTimestamp(),
	}

	return nil
}

// CreateOffsetMapping creates a mapping between logical and physical streams
// Implements Requirement 13.5: create mapping between KWIK logical and QUIC transport offsets
func (om *OffsetManager) CreateOffsetMapping(logicalStreamID uint64, pathID string, realStreamID uint64) error {
	om.mutex.Lock()
	defer om.mutex.Unlock()

	// Verify logical stream exists
	if _, exists := om.logicalOffsets[logicalStreamID]; !exists {
		return utils.NewKwikError(utils.ErrStreamCreationFailed, 
			fmt.Sprintf("logical stream %d not registered", logicalStreamID), nil)
	}

	// Verify physical stream exists
	if om.physicalOffsets[pathID] == nil || om.physicalOffsets[pathID][realStreamID] == nil {
		return utils.NewKwikError(utils.ErrStreamCreationFailed, 
			fmt.Sprintf("physical stream %d on path %s not registered", realStreamID, pathID), nil)
	}

	// Create mapping key
	mappingKey := fmt.Sprintf("%d:%s:%d", logicalStreamID, pathID, realStreamID)

	// Check if mapping already exists
	if _, exists := om.offsetMappings[mappingKey]; exists {
		return utils.NewKwikError(utils.ErrStreamCreationFailed, 
			fmt.Sprintf("offset mapping already exists for %s", mappingKey), nil)
	}

	// Create offset mapping
	om.offsetMappings[mappingKey] = &OffsetMapping{
		LogicalStreamID:   logicalStreamID,
		PathID:            pathID,
		RealStreamID:      realStreamID,
		LogicalToPhysical: make(map[uint64]uint64),
		PhysicalToLogical: make(map[uint64]uint64),
		FragmentMappings:  make(map[uint64]*FragmentMapping),
		LastUpdated:       utils.GetCurrentTimestamp(),
	}

	return nil
}

// MapLogicalToPhysical maps a KWIK logical offset to a QUIC physical offset
// Implements Requirement 13.5: mapping between KWIK logical and QUIC transport offsets
func (om *OffsetManager) MapLogicalToPhysical(logicalStreamID uint64, pathID string, realStreamID uint64, logicalOffset uint64, dataLength uint32) (uint64, error) {
	om.mutex.Lock()
	defer om.mutex.Unlock()

	// Get mapping
	mappingKey := fmt.Sprintf("%d:%s:%d", logicalStreamID, pathID, realStreamID)
	mapping, exists := om.offsetMappings[mappingKey]
	if !exists {
		return 0, utils.NewKwikError(utils.ErrOffsetMismatch, 
			fmt.Sprintf("no offset mapping found for %s", mappingKey), nil)
	}

	// Get physical stream offsets
	physicalOffsets := om.physicalOffsets[pathID][realStreamID]

	// Check if we already have a mapping for this logical offset
	if physicalOffset, exists := mapping.LogicalToPhysical[logicalOffset]; exists {
		return physicalOffset, nil
	}

	// Allocate new physical offset
	physicalOffset := physicalOffsets.NextWriteOffset

	// Create the mapping
	mapping.LogicalToPhysical[logicalOffset] = physicalOffset
	mapping.PhysicalToLogical[physicalOffset] = logicalOffset

	// Update physical stream next write offset
	physicalOffsets.NextWriteOffset += uint64(dataLength)
	physicalOffsets.BytesWritten += uint64(dataLength)
	physicalOffsets.LastUpdated = utils.GetCurrentTimestamp()

	// Update mapping timestamp
	mapping.LastUpdated = utils.GetCurrentTimestamp()

	return physicalOffset, nil
}

// MapPhysicalToLogical maps a QUIC physical offset to a KWIK logical offset
func (om *OffsetManager) MapPhysicalToLogical(logicalStreamID uint64, pathID string, realStreamID uint64, physicalOffset uint64) (uint64, error) {
	om.mutex.RLock()
	defer om.mutex.RUnlock()

	// Get mapping
	mappingKey := fmt.Sprintf("%d:%s:%d", logicalStreamID, pathID, realStreamID)
	mapping, exists := om.offsetMappings[mappingKey]
	if !exists {
		return 0, utils.NewKwikError(utils.ErrOffsetMismatch, 
			fmt.Sprintf("no offset mapping found for %s", mappingKey), nil)
	}

	// Look up logical offset
	logicalOffset, exists := mapping.PhysicalToLogical[physicalOffset]
	if !exists {
		return 0, utils.NewKwikError(utils.ErrOffsetMismatch, 
			fmt.Sprintf("no logical offset mapping found for physical offset %d", physicalOffset), nil)
	}

	return logicalOffset, nil
}

// UpdateLogicalOffset updates the logical offset for a stream
// Implements Requirement 13.2: maintain separate KWIK logical offsets
func (om *OffsetManager) UpdateLogicalOffset(logicalStreamID uint64, offset uint64, dataLength uint32) error {
	om.mutex.Lock()
	defer om.mutex.Unlock()

	logicalOffsets, exists := om.logicalOffsets[logicalStreamID]
	if !exists {
		return utils.NewKwikError(utils.ErrStreamCreationFailed, 
			fmt.Sprintf("logical stream %d not registered", logicalStreamID), nil)
	}

	// Update offsets
	endOffset := offset + uint64(dataLength)
	
	// Update max offset if this extends the stream
	if endOffset > logicalOffsets.MaxOffset {
		logicalOffsets.MaxOffset = endOffset
	}

	// Update next write offset if this is sequential
	if offset == logicalOffsets.NextWriteOffset {
		logicalOffsets.NextWriteOffset = endOffset
	}

	// Handle gap tracking if enabled
	if om.config.GapTrackingEnabled {
		om.updateGaps(logicalOffsets, offset, dataLength)
	}

	logicalOffsets.LastUpdated = utils.GetCurrentTimestamp()

	return nil
}

// UpdatePhysicalOffset updates the physical offset for a stream
// Implements Requirement 13.4: maintain QUIC physical offsets
func (om *OffsetManager) UpdatePhysicalOffset(pathID string, realStreamID uint64, offset uint64, dataLength uint32) error {
	om.mutex.Lock()
	defer om.mutex.Unlock()

	if om.physicalOffsets[pathID] == nil {
		return utils.NewPathNotFoundError(pathID)
	}

	physicalOffsets, exists := om.physicalOffsets[pathID][realStreamID]
	if !exists {
		return utils.NewKwikError(utils.ErrStreamCreationFailed, 
			fmt.Sprintf("physical stream %d on path %s not registered", realStreamID, pathID), nil)
	}

	// Update offsets
	endOffset := offset + uint64(dataLength)
	
	// Update max offset if this extends the stream
	if endOffset > physicalOffsets.MaxOffset {
		physicalOffsets.MaxOffset = endOffset
	}

	// Update next read offset if this is sequential
	if offset == physicalOffsets.NextReadOffset {
		physicalOffsets.NextReadOffset = endOffset
	}

	physicalOffsets.BytesRead += uint64(dataLength)
	physicalOffsets.LastUpdated = utils.GetCurrentTimestamp()

	return nil
}

// GetLogicalOffset returns the current logical offset information for a stream
func (om *OffsetManager) GetLogicalOffset(logicalStreamID uint64) (*LogicalStreamOffsets, error) {
	om.mutex.RLock()
	defer om.mutex.RUnlock()

	offsets, exists := om.logicalOffsets[logicalStreamID]
	if !exists {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed, 
			fmt.Sprintf("logical stream %d not registered", logicalStreamID), nil)
	}

	// Return a copy to prevent external modification
	offsetsCopy := *offsets
	offsetsCopy.Gaps = make([]OffsetGap, len(offsets.Gaps))
	copy(offsetsCopy.Gaps, offsets.Gaps)

	return &offsetsCopy, nil
}

// GetPhysicalOffset returns the current physical offset information for a stream
func (om *OffsetManager) GetPhysicalOffset(pathID string, realStreamID uint64) (*PhysicalStreamOffsets, error) {
	om.mutex.RLock()
	defer om.mutex.RUnlock()

	if om.physicalOffsets[pathID] == nil {
		return nil, utils.NewPathNotFoundError(pathID)
	}

	offsets, exists := om.physicalOffsets[pathID][realStreamID]
	if !exists {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed, 
			fmt.Sprintf("physical stream %d on path %s not registered", realStreamID, pathID), nil)
	}

	// Return a copy to prevent external modification
	offsetsCopy := *offsets
	return &offsetsCopy, nil
}

// ValidateLogicalOffset validates that a logical offset is valid for a stream
func (om *OffsetManager) ValidateLogicalOffset(logicalStreamID uint64, offset uint64) error {
	om.mutex.RLock()
	defer om.mutex.RUnlock()

	logicalOffsets, exists := om.logicalOffsets[logicalStreamID]
	if !exists {
		return utils.NewKwikError(utils.ErrStreamCreationFailed, 
			fmt.Sprintf("logical stream %d not registered", logicalStreamID), nil)
	}

	// Check if offset is beyond maximum allowed
	if offset > logicalOffsets.MaxOffset+1000000 { // Allow some reasonable future offset
		return utils.NewOffsetMismatchError(logicalOffsets.MaxOffset, offset)
	}

	return nil
}

// ValidatePhysicalOffset validates that a physical offset is valid for a stream
func (om *OffsetManager) ValidatePhysicalOffset(pathID string, realStreamID uint64, offset uint64) error {
	om.mutex.RLock()
	defer om.mutex.RUnlock()

	if om.physicalOffsets[pathID] == nil {
		return utils.NewPathNotFoundError(pathID)
	}

	physicalOffsets, exists := om.physicalOffsets[pathID][realStreamID]
	if !exists {
		return utils.NewKwikError(utils.ErrStreamCreationFailed, 
			fmt.Sprintf("physical stream %d on path %s not registered", realStreamID, pathID), nil)
	}

	// Check if offset is beyond maximum allowed
	if offset > physicalOffsets.MaxOffset+1000000 { // Allow some reasonable future offset
		return utils.NewOffsetMismatchError(physicalOffsets.MaxOffset, offset)
	}

	return nil
}

// updateGaps updates the gap tracking for a logical stream
func (om *OffsetManager) updateGaps(logicalOffsets *LogicalStreamOffsets, offset uint64, dataLength uint32) {
	endOffset := offset + uint64(dataLength)

	// If this data fills a gap at the beginning, update next read offset
	if offset == logicalOffsets.NextReadOffset {
		logicalOffsets.NextReadOffset = endOffset
		
		// Check if this fills additional gaps
		for len(logicalOffsets.Gaps) > 0 && logicalOffsets.Gaps[0].Start == int64(logicalOffsets.NextReadOffset) {
			logicalOffsets.NextReadOffset = uint64(logicalOffsets.Gaps[0].End)
			logicalOffsets.Gaps = logicalOffsets.Gaps[1:]
		}
	} else if offset > logicalOffsets.NextReadOffset {
		// This creates or extends a gap
		gap := OffsetGap{
			Start: int64(logicalOffsets.NextReadOffset),
			End:   int64(offset),
			Size:  int64(offset - logicalOffsets.NextReadOffset),
		}
		
		// Insert gap in sorted order
		inserted := false
		for i, existingGap := range logicalOffsets.Gaps {
			if gap.Start < existingGap.Start {
				// Insert before this gap
				logicalOffsets.Gaps = append(logicalOffsets.Gaps[:i], append([]OffsetGap{gap}, logicalOffsets.Gaps[i:]...)...)
				inserted = true
				break
			}
		}
		if !inserted {
			logicalOffsets.Gaps = append(logicalOffsets.Gaps, gap)
		}
	}
}

// CleanupExpiredMappings removes old offset mappings to prevent memory leaks
func (om *OffsetManager) CleanupExpiredMappings(maxAge int64) int {
	om.mutex.Lock()
	defer om.mutex.Unlock()

	currentTime := utils.GetCurrentTimestamp()
	expiredThreshold := currentTime - maxAge

	var expiredMappings []string
	for key, mapping := range om.offsetMappings {
		if mapping.LastUpdated < expiredThreshold {
			expiredMappings = append(expiredMappings, key)
		}
	}

	// Remove expired mappings
	for _, key := range expiredMappings {
		delete(om.offsetMappings, key)
	}

	return len(expiredMappings)
}

// GetOffsetStatistics returns statistics about offset management
func (om *OffsetManager) GetOffsetStatistics() *OffsetStatistics {
	om.mutex.RLock()
	defer om.mutex.RUnlock()

	stats := &OffsetStatistics{
		LogicalStreams:  len(om.logicalOffsets),
		PhysicalStreams: 0,
		OffsetMappings:  len(om.offsetMappings),
		TotalGaps:       0,
	}

	// Count physical streams and gaps
	for _, pathStreams := range om.physicalOffsets {
		stats.PhysicalStreams += len(pathStreams)
	}

	for _, logicalOffsets := range om.logicalOffsets {
		stats.TotalGaps += len(logicalOffsets.Gaps)
	}

	return stats
}

// OffsetStatistics contains statistics about offset management
type OffsetStatistics struct {
	LogicalStreams  int
	PhysicalStreams int
	OffsetMappings  int
	TotalGaps       int
}