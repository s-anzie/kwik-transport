package session

import (
	"fmt"
	"sync"
	"time"

	"kwik/internal/utils"
)

// ResourceManager manages stream resources and prevents window space exhaustion
type ResourceManager struct {
	// Configuration
	config *ResourceConfig
	
	// Resource tracking
	allocatedResources map[string]*SessionResources // sessionID -> resources
	resourcesMutex     sync.RWMutex
	
	// Statistics
	stats      *ResourceStats
	statsMutex sync.RWMutex
	
	// Background cleanup
	cleanupTicker *time.Ticker
	stopCleanup   chan struct{}
	cleanupWg     sync.WaitGroup
	
	// State
	running bool
	mutex   sync.RWMutex
}

// ResourceConfig holds configuration for resource management
type ResourceConfig struct {
	// Stream limits
	MaxConcurrentStreams int `json:"max_concurrent_streams"`
	DefaultBufferSize    int `json:"default_buffer_size"`
	MaxBufferSize        int `json:"max_buffer_size"`
	
	// Window management
	DefaultWindowSize    int64 `json:"default_window_size"`
	MaxWindowSize        int64 `json:"max_window_size"`
	WindowGrowthFactor   float64 `json:"window_growth_factor"`
	
	// Memory limits
	MaxMemoryUsage       int64 `json:"max_memory_usage"`
	MemoryPressureThreshold float64 `json:"memory_pressure_threshold"`
	
	// Cleanup
	CleanupInterval      time.Duration `json:"cleanup_interval"`
	ResourceTimeout      time.Duration `json:"resource_timeout"`
	
	// Priority management
	EnablePriorityAllocation bool `json:"enable_priority_allocation"`
	HighPriorityReservedPct  float64 `json:"high_priority_reserved_pct"`
}

// SessionResources tracks resources for a session
type SessionResources struct {
	SessionID     string
	StreamCount   int
	TotalMemory   int64
	WindowSize    int64
	LastActivity  time.Time
	StreamBuffers map[uint64]*StreamResourceInfo
	mutex         sync.RWMutex
}

// StreamResourceInfo tracks resources for individual streams
type StreamResourceInfo struct {
	StreamID     uint64
	BufferSize   int
	WindowSize   int
	Priority     ResourcePriority
	AllocatedAt  time.Time
	LastActivity time.Time
}

// ResourcePriority defines resource allocation priority
type ResourcePriority int

const (
	PriorityLow ResourcePriority = iota
	PriorityNormal
	PriorityHigh
	PriorityCritical
)

// ResourceStats tracks resource usage statistics
type ResourceStats struct {
	ActiveSessions       int
	TotalStreams         int
	TotalMemoryUsed      int64
	AllocationFailures   int
	GCRuns              int
	LastGCTime          time.Time
	AverageStreamLife   time.Duration
	PeakMemoryUsage     int64
	PeakStreamCount     int
}

// StreamResources represents allocated resources for a stream
type StreamResources struct {
	BufferSize    int
	WindowSize    int
	MemoryLimit   int
	Priority      ResourcePriority
}

// ResourceStatus represents current resource availability
type ResourceStatus struct {
	AvailableStreams int
	MemoryUsage      int64
	BufferPoolSize   int
	WindowSpaceLeft  int
}

// NewResourceManager creates a new resource manager
func NewResourceManager(config *ResourceConfig) *ResourceManager {
	if config == nil {
		config = DefaultResourceConfig()
	}
	
	rm := &ResourceManager{
		config:             config,
		allocatedResources: make(map[string]*SessionResources),
		stats: &ResourceStats{
			ActiveSessions:    0,
			TotalStreams:      0,
			TotalMemoryUsed:   0,
			AllocationFailures: 0,
			GCRuns:           0,
			LastGCTime:       time.Now(),
			AverageStreamLife: 0,
			PeakMemoryUsage:  0,
			PeakStreamCount:  0,
		},
		stopCleanup: make(chan struct{}),
		running:     false,
	}
	
	return rm
}

// DefaultResourceConfig returns default resource configuration
func DefaultResourceConfig() *ResourceConfig {
	return &ResourceConfig{
		MaxConcurrentStreams:     100,
		DefaultBufferSize:        1024 * 1024, // 1MB
		MaxBufferSize:           16 * 1024 * 1024, // 16MB
		DefaultWindowSize:       16 * 1024 * 1024, // 16MB (increased from 4MB)
		MaxWindowSize:           64 * 1024 * 1024, // 64MB
		WindowGrowthFactor:      1.5,
		MaxMemoryUsage:          256 * 1024 * 1024, // 256MB
		MemoryPressureThreshold: 0.8,
		CleanupInterval:         30 * time.Second,
		ResourceTimeout:         5 * time.Minute,
		EnablePriorityAllocation: true,
		HighPriorityReservedPct: 0.2, // 20% reserved for high priority
	}
}

// Start starts the resource manager
func (rm *ResourceManager) Start() error {
	rm.mutex.Lock()
	defer rm.mutex.Unlock()
	
	if rm.running {
		return nil
	}
	
	// Start cleanup goroutine only if cleanup interval is configured
	if rm.config.CleanupInterval > 0 {
		rm.cleanupTicker = time.NewTicker(rm.config.CleanupInterval)
		rm.cleanupWg.Add(1)
		go rm.cleanupWorker()
	}
	
	rm.running = true
	return nil
}

// Stop stops the resource manager
func (rm *ResourceManager) Stop() error {
	rm.mutex.Lock()
	defer rm.mutex.Unlock()
	
	if !rm.running {
		return nil
	}
	
	// Stop cleanup
	close(rm.stopCleanup)
	if rm.cleanupTicker != nil {
		rm.cleanupTicker.Stop()
	}
	rm.cleanupWg.Wait()
	
	rm.running = false
	return nil
}

// AllocateStreamResources allocates resources for a new stream
func (rm *ResourceManager) AllocateStreamResources(sessionID string, streamID uint64) (*StreamResources, error) {
	rm.resourcesMutex.Lock()
	defer rm.resourcesMutex.Unlock()
	
	// Get or create session resources
	sessionRes, exists := rm.allocatedResources[sessionID]
	if !exists {
		sessionRes = &SessionResources{
			SessionID:     sessionID,
			StreamCount:   0,
			TotalMemory:   0,
			WindowSize:    rm.config.DefaultWindowSize,
			LastActivity:  time.Now(),
			StreamBuffers: make(map[uint64]*StreamResourceInfo),
		}
		rm.allocatedResources[sessionID] = sessionRes
	}
	
	sessionRes.mutex.Lock()
	defer sessionRes.mutex.Unlock()
	
	// Check if stream already exists
	if _, exists := sessionRes.StreamBuffers[streamID]; exists {
		return nil, utils.NewKwikError(utils.ErrStreamLimitExceeded, 
			fmt.Sprintf("stream %d already has allocated resources", streamID), nil)
	}
	
	// Check stream limit
	if sessionRes.StreamCount >= rm.config.MaxConcurrentStreams {
		return nil, utils.NewKwikError(utils.ErrStreamLimitExceeded, 
			fmt.Sprintf("session %s has reached maximum concurrent streams (%d)", 
				sessionID, rm.config.MaxConcurrentStreams), nil)
	}
	
	// Use simple buffer size calculation
	bufferSize := rm.config.DefaultBufferSize
	windowSize := bufferSize
	
	// Check memory limits (simplified)
	newMemoryUsage := sessionRes.TotalMemory + int64(bufferSize)
	if newMemoryUsage > rm.config.MaxMemoryUsage {
		return nil, utils.NewKwikError(utils.ErrResourceExhausted, 
			fmt.Sprintf("insufficient memory: requested %d, available %d", 
				bufferSize, rm.config.MaxMemoryUsage-sessionRes.TotalMemory), nil)
	}
	
	// Allocate resources
	streamInfo := &StreamResourceInfo{
		StreamID:     streamID,
		BufferSize:   bufferSize,
		WindowSize:   windowSize,
		Priority:     PriorityNormal,
		AllocatedAt:  time.Now(),
		LastActivity: time.Now(),
	}
	
	sessionRes.StreamBuffers[streamID] = streamInfo
	sessionRes.StreamCount++
	sessionRes.TotalMemory += int64(bufferSize)
	sessionRes.LastActivity = time.Now()
	
	// Update simple statistics
	rm.statsMutex.Lock()
	rm.stats.TotalStreams++
	rm.stats.TotalMemoryUsed += int64(bufferSize)
	rm.statsMutex.Unlock()
	
	return &StreamResources{
		BufferSize:  bufferSize,
		WindowSize:  windowSize,
		MemoryLimit: bufferSize,
		Priority:    PriorityNormal,
	}, nil
}

// ReleaseStreamResources releases resources for a stream
func (rm *ResourceManager) ReleaseStreamResources(sessionID string, streamID uint64) error {
	rm.resourcesMutex.Lock()
	defer rm.resourcesMutex.Unlock()
	
	sessionRes, exists := rm.allocatedResources[sessionID]
	if !exists {
		return nil // Session doesn't exist, nothing to release
	}
	
	sessionRes.mutex.Lock()
	defer sessionRes.mutex.Unlock()
	
	streamInfo, exists := sessionRes.StreamBuffers[streamID]
	if !exists {
		return nil // Stream doesn't exist, nothing to release
	}
	
	// Release resources
	delete(sessionRes.StreamBuffers, streamID)
	sessionRes.StreamCount--
	sessionRes.TotalMemory -= int64(streamInfo.BufferSize)
	sessionRes.LastActivity = time.Now()
	
	// Remove session if no streams left
	if sessionRes.StreamCount == 0 {
		delete(rm.allocatedResources, sessionID)
	}
	
	// Update simple statistics
	rm.statsMutex.Lock()
	rm.stats.TotalStreams--
	rm.stats.TotalMemoryUsed -= int64(streamInfo.BufferSize)
	rm.statsMutex.Unlock()
	
	return nil
}

// CheckResourceAvailability checks current resource availability
func (rm *ResourceManager) CheckResourceAvailability(sessionID string) ResourceStatus {
	rm.resourcesMutex.RLock()
	defer rm.resourcesMutex.RUnlock()
	
	sessionRes, exists := rm.allocatedResources[sessionID]
	if !exists {
		return ResourceStatus{
			AvailableStreams: rm.config.MaxConcurrentStreams,
			MemoryUsage:      0,
			BufferPoolSize:   0,
			WindowSpaceLeft:  int(rm.config.DefaultWindowSize),
		}
	}
	
	sessionRes.mutex.RLock()
	streamCount := sessionRes.StreamCount
	totalMemory := sessionRes.TotalMemory
	windowSize := sessionRes.WindowSize
	sessionRes.mutex.RUnlock()
	
	availableStreams := rm.config.MaxConcurrentStreams - streamCount
	windowSpaceLeft := int(windowSize - totalMemory)
	
	return ResourceStatus{
		AvailableStreams: availableStreams,
		MemoryUsage:      totalMemory,
		BufferPoolSize:   streamCount,
		WindowSpaceLeft:  windowSpaceLeft,
	}
}

// SetResourceLimits updates resource limits
func (rm *ResourceManager) SetResourceLimits(limits ResourceConfig) error {
	rm.mutex.Lock()
	defer rm.mutex.Unlock()
	
	// Validate limits
	if limits.MaxConcurrentStreams <= 0 {
		return utils.NewKwikError(utils.ErrConfigurationInvalid, 
			"max concurrent streams must be positive", nil)
	}
	if limits.DefaultBufferSize <= 0 {
		return utils.NewKwikError(utils.ErrConfigurationInvalid, 
			"default buffer size must be positive", nil)
	}
	if limits.MaxMemoryUsage <= 0 {
		return utils.NewKwikError(utils.ErrConfigurationInvalid, 
			"max memory usage must be positive", nil)
	}
	
	rm.config = &limits
	return nil
}

// GetResourceStats returns current resource statistics
func (rm *ResourceManager) GetResourceStats(sessionID string) ResourceStats {
	rm.statsMutex.RLock()
	defer rm.statsMutex.RUnlock()
	
	// Return a copy to prevent external modification
	return *rm.stats
}

// Helper methods

// calculateOptimalBufferSize calculates optimal buffer size based on current conditions
func (rm *ResourceManager) calculateOptimalBufferSize(sessionRes *SessionResources) int {
	baseSize := rm.config.DefaultBufferSize
	
	// Adjust based on memory pressure
	memoryPressure := float64(sessionRes.TotalMemory) / float64(rm.config.MaxMemoryUsage)
	if memoryPressure > rm.config.MemoryPressureThreshold {
		// Reduce buffer size under memory pressure
		return int(float64(baseSize) * (1.0 - memoryPressure))
	}
	
	// Adjust based on stream count
	if sessionRes.StreamCount > rm.config.MaxConcurrentStreams/2 {
		// Reduce buffer size when many streams are active
		return baseSize / 2
	}
	
	return baseSize
}

// performGarbageCollection performs resource garbage collection
func (rm *ResourceManager) performGarbageCollection() {
	now := time.Now()
	
	for sessionID, sessionRes := range rm.allocatedResources {
		sessionRes.mutex.Lock()
		
		// Check for inactive streams
		for streamID, streamInfo := range sessionRes.StreamBuffers {
			if now.Sub(streamInfo.LastActivity) > rm.config.ResourceTimeout {
				// Remove inactive stream
				delete(sessionRes.StreamBuffers, streamID)
				sessionRes.StreamCount--
				sessionRes.TotalMemory -= int64(streamInfo.BufferSize)
			}
		}
		
		// Remove empty sessions
		if sessionRes.StreamCount == 0 {
			sessionRes.mutex.Unlock()
			delete(rm.allocatedResources, sessionID)
		} else {
			sessionRes.mutex.Unlock()
		}
	}
	
	// Update statistics
	rm.statsMutex.Lock()
	rm.stats.GCRuns++
	rm.stats.LastGCTime = now
	rm.statsMutex.Unlock()
}

// cleanupWorker performs periodic cleanup
func (rm *ResourceManager) cleanupWorker() {
	defer rm.cleanupWg.Done()
	
	for {
		select {
		case <-rm.cleanupTicker.C:
			rm.performGarbageCollection()
		case <-rm.stopCleanup:
			return
		}
	}
}

// updateStatsAfterAllocation updates statistics after resource allocation
func (rm *ResourceManager) updateStatsAfterAllocation() {
	rm.statsMutex.Lock()
	defer rm.statsMutex.Unlock()
	
	// Recalculate totals
	totalStreams := 0
	totalMemory := int64(0)
	activeSessions := len(rm.allocatedResources)
	
	for _, sessionRes := range rm.allocatedResources {
		sessionRes.mutex.RLock()
		totalStreams += sessionRes.StreamCount
		totalMemory += sessionRes.TotalMemory
		sessionRes.mutex.RUnlock()
	}
	
	rm.stats.ActiveSessions = activeSessions
	rm.stats.TotalStreams = totalStreams
	rm.stats.TotalMemoryUsed = totalMemory
	
	// Update peaks
	if totalMemory > rm.stats.PeakMemoryUsage {
		rm.stats.PeakMemoryUsage = totalMemory
	}
	if totalStreams > rm.stats.PeakStreamCount {
		rm.stats.PeakStreamCount = totalStreams
	}
}

// updateStatsAfterRelease updates statistics after resource release
func (rm *ResourceManager) updateStatsAfterRelease() {
	rm.statsMutex.Lock()
	defer rm.statsMutex.Unlock()
	
	// Recalculate totals
	totalStreams := 0
	totalMemory := int64(0)
	activeSessions := len(rm.allocatedResources)
	
	for _, sessionRes := range rm.allocatedResources {
		sessionRes.mutex.RLock()
		totalStreams += sessionRes.StreamCount
		totalMemory += sessionRes.TotalMemory
		sessionRes.mutex.RUnlock()
	}
	
	rm.stats.ActiveSessions = activeSessions
	rm.stats.TotalStreams = totalStreams
	rm.stats.TotalMemoryUsed = totalMemory
}

// incrementAllocationFailures increments allocation failure count
func (rm *ResourceManager) incrementAllocationFailures() {
	rm.statsMutex.Lock()
	rm.stats.AllocationFailures++
	rm.statsMutex.Unlock()
}

// GetSessionResourceInfo returns resource information for a session
func (rm *ResourceManager) GetSessionResourceInfo(sessionID string) (*SessionResources, bool) {
	rm.resourcesMutex.RLock()
	defer rm.resourcesMutex.RUnlock()
	
	sessionRes, exists := rm.allocatedResources[sessionID]
	if !exists {
		return nil, false
	}
	
	// Return a copy to prevent external modification
	sessionRes.mutex.RLock()
	defer sessionRes.mutex.RUnlock()
	
	copy := &SessionResources{
		SessionID:     sessionRes.SessionID,
		StreamCount:   sessionRes.StreamCount,
		TotalMemory:   sessionRes.TotalMemory,
		WindowSize:    sessionRes.WindowSize,
		LastActivity:  sessionRes.LastActivity,
		StreamBuffers: make(map[uint64]*StreamResourceInfo),
	}
	
	for streamID, streamInfo := range sessionRes.StreamBuffers {
		copy.StreamBuffers[streamID] = &StreamResourceInfo{
			StreamID:     streamInfo.StreamID,
			BufferSize:   streamInfo.BufferSize,
			WindowSize:   streamInfo.WindowSize,
			Priority:     streamInfo.Priority,
			AllocatedAt:  streamInfo.AllocatedAt,
			LastActivity: streamInfo.LastActivity,
		}
	}
	
	return copy, true
}

// UpdateStreamActivity updates the last activity time for a stream
func (rm *ResourceManager) UpdateStreamActivity(sessionID string, streamID uint64) {
	rm.resourcesMutex.RLock()
	sessionRes, exists := rm.allocatedResources[sessionID]
	rm.resourcesMutex.RUnlock()
	
	if !exists {
		return
	}
	
	sessionRes.mutex.Lock()
	defer sessionRes.mutex.Unlock()
	
	if streamInfo, exists := sessionRes.StreamBuffers[streamID]; exists {
		streamInfo.LastActivity = time.Now()
		sessionRes.LastActivity = time.Now()
	}
}

// ExpandSessionWindow expands the window size for a session
func (rm *ResourceManager) ExpandSessionWindow(sessionID string, additionalSize int64) error {
	rm.resourcesMutex.Lock()
	defer rm.resourcesMutex.Unlock()
	
	sessionRes, exists := rm.allocatedResources[sessionID]
	if !exists {
		return utils.NewKwikError(utils.ErrSessionNotFound, 
			fmt.Sprintf("session %s not found", sessionID), nil)
	}
	
	sessionRes.mutex.Lock()
	defer sessionRes.mutex.Unlock()
	
	newWindowSize := sessionRes.WindowSize + additionalSize
	if newWindowSize > rm.config.MaxWindowSize {
		return utils.NewKwikError(utils.ErrResourceExhausted, 
			fmt.Sprintf("cannot expand window beyond maximum size %d", rm.config.MaxWindowSize), nil)
	}
	
	sessionRes.WindowSize = newWindowSize
	return nil
}