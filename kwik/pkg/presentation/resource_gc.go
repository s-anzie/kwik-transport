package presentation

import (
	"fmt"
	"sync"
	"time"
)

// ResourceGarbageCollector manages cleanup of orphaned and unused resources
type ResourceGarbageCollector struct {
	// Components to monitor
	dpm           *DataPresentationManagerImpl
	windowManager *ReceiveWindowManagerImpl
	
	// Configuration
	config *GCConfig
	
	// State tracking
	lastGCRun        time.Time
	totalGCRuns      uint64
	totalResourcesFreed uint64
	
	// Lifecycle
	running     bool
	stopGC      chan struct{}
	gcTicker    *time.Ticker
	gcWg        sync.WaitGroup
	mutex       sync.RWMutex
	
	// Statistics
	stats *GCStats
	statsMutex sync.RWMutex
}

// GCConfig holds configuration for garbage collection
type GCConfig struct {
	// Intervals
	GCInterval          time.Duration `json:"gc_interval"`           // How often to run GC (default: 30s)
	OrphanCheckInterval time.Duration `json:"orphan_check_interval"` // How often to check for orphans (default: 10s)
	
	// Thresholds
	MaxIdleTime         time.Duration `json:"max_idle_time"`         // Max time a resource can be idle (default: 5min)
	MemoryPressureThreshold float64   `json:"memory_pressure_threshold"` // Memory pressure to trigger aggressive GC (default: 0.8)
	
	// Limits
	MaxOrphanedResources int `json:"max_orphaned_resources"` // Max orphaned resources before forced cleanup (default: 100)
	
	// Behavior
	EnableAggressiveGC   bool `json:"enable_aggressive_gc"`   // Enable aggressive GC under memory pressure
	EnableOrphanCleanup  bool `json:"enable_orphan_cleanup"`  // Enable orphaned resource cleanup
	EnableImmediateCleanup bool `json:"enable_immediate_cleanup"` // Enable immediate cleanup on stream close
}

// DefaultGCConfig returns default garbage collection configuration
func DefaultGCConfig() *GCConfig {
	return &GCConfig{
		GCInterval:          30 * time.Second,
		OrphanCheckInterval: 10 * time.Second,
		MaxIdleTime:         5 * time.Minute,
		MemoryPressureThreshold: 0.8,
		MaxOrphanedResources: 100,
		EnableAggressiveGC:   true,
		EnableOrphanCleanup:  true,
		EnableImmediateCleanup: true,
	}
}

// GCStats contains garbage collection statistics
type GCStats struct {
	TotalGCRuns         uint64        `json:"total_gc_runs"`
	LastGCRun           time.Time     `json:"last_gc_run"`
	LastGCDuration      time.Duration `json:"last_gc_duration"`
	TotalResourcesFreed uint64        `json:"total_resources_freed"`
	OrphanedStreams     int           `json:"orphaned_streams"`
	OrphanedAllocations int           `json:"orphaned_allocations"`
	MemoryFreed         uint64        `json:"memory_freed"`
	WindowSpaceFreed    uint64        `json:"window_space_freed"`
	LastUpdate          time.Time     `json:"last_update"`
}

// OrphanedResource represents a resource that may be orphaned
type OrphanedResource struct {
	Type        ResourceType  `json:"type"`
	StreamID    uint64        `json:"stream_id"`
	Size        uint64        `json:"size"`
	LastAccess  time.Time     `json:"last_access"`
	IdleDuration time.Duration `json:"idle_duration"`
}

// ResourceType defines the type of resource
type ResourceType int

const (
	ResourceTypeStreamBuffer ResourceType = iota
	ResourceTypeWindowAllocation
	ResourceTypeReservation
)

func (rt ResourceType) String() string {
	switch rt {
	case ResourceTypeStreamBuffer:
		return "STREAM_BUFFER"
	case ResourceTypeWindowAllocation:
		return "WINDOW_ALLOCATION"
	case ResourceTypeReservation:
		return "RESERVATION"
	default:
		return "UNKNOWN"
	}
}

// NewResourceGarbageCollector creates a new resource garbage collector
func NewResourceGarbageCollector(dpm *DataPresentationManagerImpl, windowManager *ReceiveWindowManagerImpl, config *GCConfig) *ResourceGarbageCollector {
	if config == nil {
		config = DefaultGCConfig()
	}
	
	return &ResourceGarbageCollector{
		dpm:           dpm,
		windowManager: windowManager,
		config:        config,
		lastGCRun:     time.Now(),
		stopGC:        make(chan struct{}),
		stats: &GCStats{
			TotalGCRuns:         0,
			LastGCRun:           time.Time{},
			LastGCDuration:      0,
			TotalResourcesFreed: 0,
			OrphanedStreams:     0,
			OrphanedAllocations: 0,
			MemoryFreed:         0,
			WindowSpaceFreed:    0,
			LastUpdate:          time.Now(),
		},
	}
}

// Start starts the garbage collector
func (gc *ResourceGarbageCollector) Start() error {
	gc.mutex.Lock()
	defer gc.mutex.Unlock()
	
	if gc.running {
		return fmt.Errorf("garbage collector is already running")
	}
	
	gc.running = true
	gc.gcTicker = time.NewTicker(gc.config.GCInterval)
	
	// Start GC worker
	gc.gcWg.Add(1)
	go gc.gcWorker()
	
	return nil
}

// Stop stops the garbage collector
func (gc *ResourceGarbageCollector) Stop() error {
	gc.mutex.Lock()
	defer gc.mutex.Unlock()
	
	if !gc.running {
		return nil
	}
	
	gc.running = false
	
	if gc.gcTicker != nil {
		gc.gcTicker.Stop()
	}
	
	close(gc.stopGC)
	gc.gcWg.Wait()
	
	// Recreate stop channel for potential restart
	gc.stopGC = make(chan struct{})
	
	return nil
}

// ForceGC forces an immediate garbage collection run
func (gc *ResourceGarbageCollector) ForceGC() *GCStats {
	return gc.performGC(true)
}

// GetStats returns current garbage collection statistics
func (gc *ResourceGarbageCollector) GetStats() *GCStats {
	gc.statsMutex.RLock()
	defer gc.statsMutex.RUnlock()
	
	// Return a copy
	return &GCStats{
		TotalGCRuns:         gc.stats.TotalGCRuns,
		LastGCRun:           gc.stats.LastGCRun,
		LastGCDuration:      gc.stats.LastGCDuration,
		TotalResourcesFreed: gc.stats.TotalResourcesFreed,
		OrphanedStreams:     gc.stats.OrphanedStreams,
		OrphanedAllocations: gc.stats.OrphanedAllocations,
		MemoryFreed:         gc.stats.MemoryFreed,
		WindowSpaceFreed:    gc.stats.WindowSpaceFreed,
		LastUpdate:          gc.stats.LastUpdate,
	}
}

// gcWorker runs the garbage collection worker
func (gc *ResourceGarbageCollector) gcWorker() {
	defer gc.gcWg.Done()
	
	for {
		select {
		case <-gc.gcTicker.C:
			gc.performGC(false)
		case <-gc.stopGC:
			return
		}
	}
}

// performGC performs a garbage collection run
func (gc *ResourceGarbageCollector) performGC(forced bool) *GCStats {
	startTime := time.Now()
	
	gc.mutex.RLock()
	running := gc.running
	gc.mutex.RUnlock()
	
	if !running && !forced {
		return gc.GetStats()
	}
	
	// Update statistics
	gc.statsMutex.Lock()
	gc.stats.TotalGCRuns++
	gc.stats.LastGCRun = startTime
	gc.statsMutex.Unlock()
	
	var totalFreed uint64
	var windowSpaceFreed uint64
	var memoryFreed uint64
	
	// Step 1: Find orphaned resources
	orphanedResources := gc.findOrphanedResources()
	
	// Step 2: Clean up orphaned stream buffers
	if gc.config.EnableOrphanCleanup {
		for _, resource := range orphanedResources {
			if resource.Type == ResourceTypeStreamBuffer {
				err := gc.cleanupOrphanedStreamBuffer(resource.StreamID)
				if err == nil {
					totalFreed++
					memoryFreed += resource.Size
				}
			}
		}
	}
	
	// Step 3: Clean up orphaned window allocations
	orphanedAllocations := gc.findOrphanedWindowAllocations()
	for _, streamID := range orphanedAllocations {
		windowStatus := gc.windowManager.GetWindowStatus()
		if allocation, exists := windowStatus.StreamAllocations[streamID]; exists {
			err := gc.windowManager.ReleaseWindow(streamID, allocation)
			if err == nil {
				totalFreed++
				windowSpaceFreed += allocation
			}
		}
	}
	
	// Step 4: Aggressive cleanup under memory pressure
	if gc.config.EnableAggressiveGC {
		memoryUsage := gc.getMemoryUsage()
		if memoryUsage > gc.config.MemoryPressureThreshold {
			additionalFreed := gc.performAggressiveCleanup()
			totalFreed += additionalFreed
		}
	}
	
	// Step 5: Clean up expired reservations (handled by reservation manager)
	if gc.dpm.reservationManager != nil {
		gc.dpm.reservationManager.performCleanup()
	}
	
	// Update final statistics
	duration := time.Since(startTime)
	
	gc.statsMutex.Lock()
	gc.stats.LastGCDuration = duration
	gc.stats.TotalResourcesFreed += totalFreed
	gc.stats.OrphanedStreams = len(orphanedResources)
	gc.stats.OrphanedAllocations = len(orphanedAllocations)
	gc.stats.MemoryFreed += memoryFreed
	gc.stats.WindowSpaceFreed += windowSpaceFreed
	gc.stats.LastUpdate = time.Now()
	gc.statsMutex.Unlock()
	
	gc.lastGCRun = startTime
	gc.totalGCRuns++
	gc.totalResourcesFreed += totalFreed
	
	return gc.GetStats()
}

// findOrphanedResources finds resources that may be orphaned
func (gc *ResourceGarbageCollector) findOrphanedResources() []OrphanedResource {
	var orphaned []OrphanedResource
	now := time.Now()
	
	// Check stream buffers
	gc.dpm.buffersMutex.RLock()
	for streamID, buffer := range gc.dpm.streamBuffers {
		metadata := buffer.GetStreamMetadata()
		if metadata != nil {
			idleDuration := now.Sub(metadata.LastActivity)
			if idleDuration > gc.config.MaxIdleTime {
				usage := buffer.GetBufferUsage()
				orphaned = append(orphaned, OrphanedResource{
					Type:         ResourceTypeStreamBuffer,
					StreamID:     streamID,
					Size:         usage.TotalCapacity,
					LastAccess:   metadata.LastActivity,
					IdleDuration: idleDuration,
				})
			}
		}
	}
	gc.dpm.buffersMutex.RUnlock()
	
	return orphaned
}

// findOrphanedWindowAllocations finds window allocations without corresponding stream buffers
func (gc *ResourceGarbageCollector) findOrphanedWindowAllocations() []uint64 {
	var orphaned []uint64
	
	windowStatus := gc.windowManager.GetWindowStatus()
	
	gc.dpm.buffersMutex.RLock()
	for streamID := range windowStatus.StreamAllocations {
		if _, exists := gc.dpm.streamBuffers[streamID]; !exists {
			orphaned = append(orphaned, streamID)
		}
	}
	gc.dpm.buffersMutex.RUnlock()
	
	return orphaned
}

// cleanupOrphanedStreamBuffer cleans up an orphaned stream buffer
func (gc *ResourceGarbageCollector) cleanupOrphanedStreamBuffer(streamID uint64) error {
	return gc.dpm.RemoveStreamBuffer(streamID)
}

// performAggressiveCleanup performs aggressive cleanup under memory pressure
func (gc *ResourceGarbageCollector) performAggressiveCleanup() uint64 {
	var freed uint64
	
	// Force cleanup of all stream buffers
	gc.dpm.buffersMutex.RLock()
	buffers := make([]*StreamBufferImpl, 0, len(gc.dpm.streamBuffers))
	for _, buffer := range gc.dpm.streamBuffers {
		buffers = append(buffers, buffer)
	}
	gc.dpm.buffersMutex.RUnlock()
	
	for _, buffer := range buffers {
		buffer.PerformMaintenanceCleanup()
		freed++
	}
	
	return freed
}

// getMemoryUsage gets current memory usage (simplified implementation)
func (gc *ResourceGarbageCollector) getMemoryUsage() float64 {
	// In a real implementation, this would query actual system memory
	// For now, we'll use window utilization as a proxy
	return gc.windowManager.GetWindowUtilization()
}

// CleanupStreamResources immediately cleans up resources for a specific stream
func (gc *ResourceGarbageCollector) CleanupStreamResources(streamID uint64) error {
	if !gc.config.EnableImmediateCleanup {
		return nil // Immediate cleanup is disabled
	}
	
	// Clean up stream buffer
	err := gc.dpm.RemoveStreamBuffer(streamID)
	if err != nil {
		// Stream might not exist, which is okay
	}
	
	// Clean up window allocation
	windowStatus := gc.windowManager.GetWindowStatus()
	if allocation, exists := windowStatus.StreamAllocations[streamID]; exists {
		err = gc.windowManager.ReleaseWindow(streamID, allocation)
		if err != nil {
			return fmt.Errorf("failed to release window allocation: %w", err)
		}
	}
	
	// Update statistics
	gc.statsMutex.Lock()
	gc.stats.TotalResourcesFreed++
	gc.stats.LastUpdate = time.Now()
	gc.statsMutex.Unlock()
	
	return nil
}

// GetOrphanedResources returns a list of currently orphaned resources
func (gc *ResourceGarbageCollector) GetOrphanedResources() []OrphanedResource {
	return gc.findOrphanedResources()
}

// SetConfig updates the garbage collector configuration
func (gc *ResourceGarbageCollector) SetConfig(config *GCConfig) error {
	if config == nil {
		return fmt.Errorf("config cannot be nil")
	}
	
	gc.mutex.Lock()
	defer gc.mutex.Unlock()
	
	gc.config = config
	
	// Update ticker interval if running
	if gc.running && gc.gcTicker != nil {
		gc.gcTicker.Stop()
		gc.gcTicker = time.NewTicker(config.GCInterval)
	}
	
	return nil
}