package stream

import (
	"fmt"
	"sync"
	"time"
)

// StreamMappingManager manages the mapping between secondary streams and KWIK streams
type StreamMappingManager struct {
	// Mapping secondaire -> KWIK
	secondaryToKwik map[uint64]uint64 // secondaryStreamID -> kwikStreamID
	
	// Mapping KWIK -> secondaires
	kwikToSecondaries map[uint64][]uint64 // kwikStreamID -> []secondaryStreamID
	
	// Métadonnées des mappings
	mappingMetadata map[uint64]*MappingMetadata // secondaryStreamID -> metadata
	
	// Configuration
	config *MappingManagerConfig
	
	// Synchronisation
	mutex sync.RWMutex
	
	// Cleanup management
	cleanupTicker *time.Ticker
	stopCleanup   chan struct{}
}

// MappingMetadata contains metadata about a stream mapping
type MappingMetadata struct {
	SecondaryStreamID uint64    // Secondary stream ID
	KwikStreamID      uint64    // Target KWIK stream ID
	PathID            string    // Source path identifier
	CreatedAt         time.Time // When the mapping was created
	LastActivity      time.Time // Last activity timestamp
	BytesTransferred  uint64    // Total bytes transferred through this mapping
	CurrentOffset     uint64    // Current offset in the KWIK stream
}

// MappingManagerConfig contains configuration for the mapping manager
type MappingManagerConfig struct {
	MaxMappings       int           // Maximum number of concurrent mappings
	CleanupInterval   time.Duration // Interval for cleanup operations
	MappingTimeout    time.Duration // Timeout for inactive mappings
	EnableAutoCleanup bool          // Whether to enable automatic cleanup
}

// NewStreamMappingManager creates a new stream mapping manager
func NewStreamMappingManager(config *MappingManagerConfig) *StreamMappingManager {
	if config == nil {
		config = &MappingManagerConfig{
			MaxMappings:       1000,
			CleanupInterval:   30 * time.Second,
			MappingTimeout:    5 * time.Minute,
			EnableAutoCleanup: true,
		}
	}
	
	manager := &StreamMappingManager{
		secondaryToKwik:   make(map[uint64]uint64),
		kwikToSecondaries: make(map[uint64][]uint64),
		mappingMetadata:   make(map[uint64]*MappingMetadata),
		config:            config,
		stopCleanup:       make(chan struct{}),
	}
	
	// Start cleanup routine if enabled
	if config.EnableAutoCleanup {
		manager.startCleanupRoutine()
	}
	
	return manager
}

// CreateMapping creates a new mapping between a secondary stream and a KWIK stream
func (m *StreamMappingManager) CreateMapping(secondaryStreamID, kwikStreamID uint64, pathID string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	
	// Check if we've reached the maximum number of mappings
	if len(m.secondaryToKwik) >= m.config.MaxMappings {
		return &MappingError{
			Code:              ErrMappingLimitExceeded,
			Message:           "maximum number of mappings exceeded",
			SecondaryStreamID: secondaryStreamID,
			KwikStreamID:      kwikStreamID,
		}
	}
	
	// Check if secondary stream is already mapped
	if existingKwikID, exists := m.secondaryToKwik[secondaryStreamID]; exists {
		return &MappingError{
			Code:              ErrMappingAlreadyExists,
			Message:           fmt.Sprintf("secondary stream %d already mapped to KWIK stream %d", secondaryStreamID, existingKwikID),
			SecondaryStreamID: secondaryStreamID,
			KwikStreamID:      kwikStreamID,
		}
	}
	
	// Create the mapping
	m.secondaryToKwik[secondaryStreamID] = kwikStreamID
	
	// Add to reverse mapping
	if secondaries, exists := m.kwikToSecondaries[kwikStreamID]; exists {
		m.kwikToSecondaries[kwikStreamID] = append(secondaries, secondaryStreamID)
	} else {
		m.kwikToSecondaries[kwikStreamID] = []uint64{secondaryStreamID}
	}
	
	// Create metadata
	now := time.Now()
	m.mappingMetadata[secondaryStreamID] = &MappingMetadata{
		SecondaryStreamID: secondaryStreamID,
		KwikStreamID:      kwikStreamID,
		PathID:            pathID,
		CreatedAt:         now,
		LastActivity:      now,
		BytesTransferred:  0,
		CurrentOffset:     0,
	}
	
	return nil
}

// UpdateMapping updates an existing mapping with new information
func (m *StreamMappingManager) UpdateMapping(secondaryStreamID uint64, bytesTransferred uint64, currentOffset uint64) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	
	metadata, exists := m.mappingMetadata[secondaryStreamID]
	if !exists {
		return &MappingError{
			Code:              ErrMappingNotFound,
			Message:           "mapping not found",
			SecondaryStreamID: secondaryStreamID,
		}
	}
	
	// Update metadata
	metadata.LastActivity = time.Now()
	metadata.BytesTransferred += bytesTransferred
	metadata.CurrentOffset = currentOffset
	
	return nil
}

// RemoveMapping removes a mapping between a secondary stream and a KWIK stream
func (m *StreamMappingManager) RemoveMapping(secondaryStreamID uint64) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	
	// Get the KWIK stream ID
	kwikStreamID, exists := m.secondaryToKwik[secondaryStreamID]
	if !exists {
		return &MappingError{
			Code:              ErrMappingNotFound,
			Message:           "mapping not found",
			SecondaryStreamID: secondaryStreamID,
		}
	}
	
	// Remove from primary mapping
	delete(m.secondaryToKwik, secondaryStreamID)
	
	// Remove from reverse mapping
	if secondaries, exists := m.kwikToSecondaries[kwikStreamID]; exists {
		// Find and remove the secondary stream ID
		for i, id := range secondaries {
			if id == secondaryStreamID {
				m.kwikToSecondaries[kwikStreamID] = append(secondaries[:i], secondaries[i+1:]...)
				break
			}
		}
		
		// Clean up empty slice
		if len(m.kwikToSecondaries[kwikStreamID]) == 0 {
			delete(m.kwikToSecondaries, kwikStreamID)
		}
	}
	
	// Remove metadata
	delete(m.mappingMetadata, secondaryStreamID)
	
	return nil
}

// GetKwikStreamID returns the KWIK stream ID for a given secondary stream ID
func (m *StreamMappingManager) GetKwikStreamID(secondaryStreamID uint64) (uint64, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	
	kwikStreamID, exists := m.secondaryToKwik[secondaryStreamID]
	if !exists {
		return 0, &MappingError{
			Code:              ErrMappingNotFound,
			Message:           "mapping not found",
			SecondaryStreamID: secondaryStreamID,
		}
	}
	
	return kwikStreamID, nil
}

// GetSecondaryStreamIDs returns all secondary stream IDs mapped to a given KWIK stream ID
func (m *StreamMappingManager) GetSecondaryStreamIDs(kwikStreamID uint64) ([]uint64, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	
	secondaries, exists := m.kwikToSecondaries[kwikStreamID]
	if !exists {
		return nil, &MappingError{
			Code:         ErrMappingNotFound,
			Message:      "no secondary streams found for KWIK stream",
			KwikStreamID: kwikStreamID,
		}
	}
	
	// Return a copy to avoid race conditions
	result := make([]uint64, len(secondaries))
	copy(result, secondaries)
	return result, nil
}

// GetMappingMetadata returns metadata for a given secondary stream mapping
func (m *StreamMappingManager) GetMappingMetadata(secondaryStreamID uint64) (*MappingMetadata, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	
	metadata, exists := m.mappingMetadata[secondaryStreamID]
	if !exists {
		return nil, &MappingError{
			Code:              ErrMappingNotFound,
			Message:           "mapping metadata not found",
			SecondaryStreamID: secondaryStreamID,
		}
	}
	
	// Return a copy to avoid race conditions
	metadataCopy := *metadata
	return &metadataCopy, nil
}

// GetAllMappings returns all current mappings
func (m *StreamMappingManager) GetAllMappings() map[uint64]uint64 {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	
	// Return a copy to avoid race conditions
	mappings := make(map[uint64]uint64)
	for secondary, kwik := range m.secondaryToKwik {
		mappings[secondary] = kwik
	}
	return mappings
}

// GetMappingStats returns statistics about the current mappings
func (m *StreamMappingManager) GetMappingStats() *MappingStats {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	
	stats := &MappingStats{
		TotalMappings:     len(m.secondaryToKwik),
		UniqueKwikStreams: len(m.kwikToSecondaries),
		TotalBytesTransferred: 0,
	}
	
	// Calculate total bytes transferred and find oldest/newest mappings
	var oldestTime, newestTime time.Time
	first := true
	
	for _, metadata := range m.mappingMetadata {
		stats.TotalBytesTransferred += metadata.BytesTransferred
		
		if first {
			oldestTime = metadata.CreatedAt
			newestTime = metadata.CreatedAt
			first = false
		} else {
			if metadata.CreatedAt.Before(oldestTime) {
				oldestTime = metadata.CreatedAt
			}
			if metadata.CreatedAt.After(newestTime) {
				newestTime = metadata.CreatedAt
			}
		}
	}
	
	if !first {
		stats.OldestMappingAge = time.Since(oldestTime)
		stats.NewestMappingAge = time.Since(newestTime)
	}
	
	return stats
}

// CleanupInactiveMappings removes mappings that have been inactive for too long
func (m *StreamMappingManager) CleanupInactiveMappings() int {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	
	now := time.Now()
	var toRemove []uint64
	
	// Find inactive mappings
	for secondaryStreamID, metadata := range m.mappingMetadata {
		if now.Sub(metadata.LastActivity) > m.config.MappingTimeout {
			toRemove = append(toRemove, secondaryStreamID)
		}
	}
	
	// Remove inactive mappings
	for _, secondaryStreamID := range toRemove {
		kwikStreamID := m.secondaryToKwik[secondaryStreamID]
		
		// Remove from primary mapping
		delete(m.secondaryToKwik, secondaryStreamID)
		
		// Remove from reverse mapping
		if secondaries, exists := m.kwikToSecondaries[kwikStreamID]; exists {
			for i, id := range secondaries {
				if id == secondaryStreamID {
					m.kwikToSecondaries[kwikStreamID] = append(secondaries[:i], secondaries[i+1:]...)
					break
				}
			}
			
			if len(m.kwikToSecondaries[kwikStreamID]) == 0 {
				delete(m.kwikToSecondaries, kwikStreamID)
			}
		}
		
		// Remove metadata
		delete(m.mappingMetadata, secondaryStreamID)
	}
	
	return len(toRemove)
}

// Close stops the mapping manager and cleans up resources
func (m *StreamMappingManager) Close() error {
	if m.cleanupTicker != nil {
		m.cleanupTicker.Stop()
		close(m.stopCleanup)
	}
	
	m.mutex.Lock()
	defer m.mutex.Unlock()
	
	// Clear all mappings
	m.secondaryToKwik = make(map[uint64]uint64)
	m.kwikToSecondaries = make(map[uint64][]uint64)
	m.mappingMetadata = make(map[uint64]*MappingMetadata)
	
	return nil
}

// startCleanupRoutine starts the automatic cleanup routine
func (m *StreamMappingManager) startCleanupRoutine() {
	m.cleanupTicker = time.NewTicker(m.config.CleanupInterval)
	
	go func() {
		for {
			select {
			case <-m.cleanupTicker.C:
				m.CleanupInactiveMappings()
			case <-m.stopCleanup:
				return
			}
		}
	}()
}

// MappingStats contains statistics about stream mappings
type MappingStats struct {
	TotalMappings         int           // Total number of active mappings
	UniqueKwikStreams     int           // Number of unique KWIK streams being mapped to
	TotalBytesTransferred uint64        // Total bytes transferred through all mappings
	OldestMappingAge      time.Duration // Age of the oldest mapping
	NewestMappingAge      time.Duration // Age of the newest mapping
}

// MappingError represents an error in mapping operations
type MappingError struct {
	Code              string
	Message           string
	SecondaryStreamID uint64
	KwikStreamID      uint64
}

func (e *MappingError) Error() string {
	if e.SecondaryStreamID != 0 && e.KwikStreamID != 0 {
		return fmt.Sprintf("%s: %s (secondary: %d, kwik: %d)", e.Code, e.Message, e.SecondaryStreamID, e.KwikStreamID)
	} else if e.SecondaryStreamID != 0 {
		return fmt.Sprintf("%s: %s (secondary: %d)", e.Code, e.Message, e.SecondaryStreamID)
	} else if e.KwikStreamID != 0 {
		return fmt.Sprintf("%s: %s (kwik: %d)", e.Code, e.Message, e.KwikStreamID)
	}
	return e.Code + ": " + e.Message
}

// Error codes for mapping operations
const (
	ErrMappingNotFound       = "KWIK_MAPPING_NOT_FOUND"
	ErrMappingAlreadyExists  = "KWIK_MAPPING_ALREADY_EXISTS"
	ErrMappingLimitExceeded  = "KWIK_MAPPING_LIMIT_EXCEEDED"
	ErrMappingInvalid        = "KWIK_MAPPING_INVALID"
	ErrMappingConflict       = "KWIK_MAPPING_CONFLICT"
	ErrMappingTimeout        = "KWIK_MAPPING_TIMEOUT"
)