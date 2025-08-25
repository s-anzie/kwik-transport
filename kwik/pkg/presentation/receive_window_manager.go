package presentation

import (
	"fmt"
	"sync"
	"time"
)

// ReceiveWindowManagerImpl implements the ReceiveWindowManager interface
type ReceiveWindowManagerImpl struct {
	// Window configuration
	totalSize     uint64
	usedSize      uint64
	sizeMutex     sync.RWMutex
	
	// Per-stream allocations
	streamAllocations map[uint64]uint64 // streamID -> allocated bytes
	allocationsMutex  sync.RWMutex
	
	// Window sliding
	slidePosition     uint64
	lastSlideTime     time.Time
	slideMutex        sync.RWMutex
	
	// Configuration
	config            *WindowManagerConfig
	
	// Callbacks
	windowFullCallback      func()
	windowAvailableCallback func()
	callbacksMutex          sync.RWMutex
	
	// Statistics
	stats             *WindowManagerStats
	statsMutex        sync.RWMutex
	
	// State
	isBackpressureActive bool
	backpressureMutex    sync.RWMutex
	
	// Dynamic window management
	dynamicAdjustmentEnabled bool
	lastAdjustmentTime       time.Time
	adjustmentMutex          sync.RWMutex
	memoryMonitor           *MemoryMonitor
	
	// Lifecycle management
	stopDynamicAdjustment chan struct{}
	dynamicWorkerWg       sync.WaitGroup
}

// WindowManagerConfig holds configuration for the window manager
type WindowManagerConfig struct {
	InitialSize           uint64        `json:"initial_size"`
	MaxSize               uint64        `json:"max_size"`
	MinSize               uint64        `json:"min_size"`
	BackpressureThreshold float64       `json:"backpressure_threshold"`
	SlideSize             uint64        `json:"slide_size"`
	AutoSlideEnabled      bool          `json:"auto_slide_enabled"`
	AutoSlideInterval     time.Duration `json:"auto_slide_interval"`
	MaxStreams            int           `json:"max_streams"`
	
	// Dynamic window management configuration
	EnableDynamicSizing   bool          `json:"enable_dynamic_sizing"`
	GrowthFactor          float64       `json:"growth_factor"`          // Factor by which to grow window (default: 1.5)
	ShrinkFactor          float64       `json:"shrink_factor"`          // Factor by which to shrink window (default: 0.8)
	MemoryPressureThreshold float64     `json:"memory_pressure_threshold"` // Memory usage threshold to trigger shrinking (default: 0.85)
	DynamicAdjustInterval time.Duration `json:"dynamic_adjust_interval"`   // Interval for dynamic adjustments (default: 5s)
}

// WindowManagerStats contains statistics for the window manager
type WindowManagerStats struct {
	TotalAllocations   uint64    `json:"total_allocations"`
	TotalReleases      uint64    `json:"total_releases"`
	TotalSlides        uint64    `json:"total_slides"`
	BytesSlid          uint64    `json:"bytes_slid"`
	BackpressureEvents uint64    `json:"backpressure_events"`
	PeakUtilization    float64   `json:"peak_utilization"`
	AverageUtilization float64   `json:"average_utilization"`
	LastUpdate         time.Time `json:"last_update"`
}

// MemoryMonitor tracks system memory usage for dynamic window management
type MemoryMonitor struct {
	totalMemory     uint64
	availableMemory uint64
	usedMemory      uint64
	lastUpdate      time.Time
	mutex           sync.RWMutex
}

// NewMemoryMonitor creates a new memory monitor
func NewMemoryMonitor() *MemoryMonitor {
	return &MemoryMonitor{
		totalMemory:     8 * 1024 * 1024 * 1024, // Default 8GB, would be detected in real implementation
		availableMemory: 6 * 1024 * 1024 * 1024, // Default 6GB available
		usedMemory:      2 * 1024 * 1024 * 1024, // Default 2GB used
		lastUpdate:      time.Now(),
	}
}

// GetMemoryUsage returns current memory usage as a ratio (0.0 to 1.0)
func (mm *MemoryMonitor) GetMemoryUsage() float64 {
	mm.mutex.RLock()
	defer mm.mutex.RUnlock()
	
	if mm.totalMemory == 0 {
		return 0.0
	}
	return float64(mm.usedMemory) / float64(mm.totalMemory)
}

// UpdateMemoryStats updates memory statistics (would use real system calls in production)
func (mm *MemoryMonitor) UpdateMemoryStats() {
	mm.mutex.Lock()
	defer mm.mutex.Unlock()
	
	// In a real implementation, this would query actual system memory
	// For now, we simulate memory pressure based on time
	mm.lastUpdate = time.Now()
	
	// Simulate varying memory pressure
	baseUsage := 0.3 // 30% base usage
	variableUsage := 0.2 * (1 + 0.5*float64(time.Now().Unix()%10)/10) // Variable 20-30%
	mm.usedMemory = uint64(float64(mm.totalMemory) * (baseUsage + variableUsage))
	mm.availableMemory = mm.totalMemory - mm.usedMemory
}

// NewReceiveWindowManager creates a new receive window manager
func NewReceiveWindowManager(config *WindowManagerConfig) *ReceiveWindowManagerImpl {
	if config == nil {
		config = &WindowManagerConfig{
			InitialSize:           DefaultReceiveWindowSize,
			MaxSize:               DefaultReceiveWindowSize * 2,
			MinSize:               DefaultReceiveWindowSize / 4,
			BackpressureThreshold: DefaultBackpressureThreshold,
			SlideSize:             64 * 1024, // 64KB
			AutoSlideEnabled:      true,
			AutoSlideInterval:     100 * time.Millisecond,
			MaxStreams:            1000,
			
			// Dynamic window management defaults
			EnableDynamicSizing:     true,
			GrowthFactor:           1.5,
			ShrinkFactor:           0.8,
			MemoryPressureThreshold: 0.85,
			DynamicAdjustInterval:  5 * time.Second,
		}
	}
	
	// Ensure MaxStreams is set to a reasonable default if not specified
	if config.MaxStreams <= 0 {
		config.MaxStreams = 1000
	}
	
	rwm := &ReceiveWindowManagerImpl{
		totalSize:         config.InitialSize,
		usedSize:          0,
		streamAllocations: make(map[uint64]uint64),
		slidePosition:     0,
		lastSlideTime:     time.Now(),
		config:            config,
		isBackpressureActive: false,
		stats: &WindowManagerStats{
			TotalAllocations:   0,
			TotalReleases:      0,
			TotalSlides:        0,
			BytesSlid:          0,
			BackpressureEvents: 0,
			PeakUtilization:    0,
			AverageUtilization: 0,
			LastUpdate:         time.Now(),
		},
		
		// Dynamic window management
		dynamicAdjustmentEnabled: config.EnableDynamicSizing,
		lastAdjustmentTime:       time.Now(),
		memoryMonitor:           NewMemoryMonitor(),
		stopDynamicAdjustment:   make(chan struct{}),
	}
	
	// Start auto-slide goroutine if enabled
	if config.AutoSlideEnabled {
		go rwm.autoSlideWorker()
	}
	
	// Start dynamic adjustment worker if enabled
	if config.EnableDynamicSizing {
		rwm.dynamicWorkerWg.Add(1)
		go rwm.dynamicAdjustmentWorker()
	}
	
	return rwm
}

// SetWindowSize sets the total window size
func (rwm *ReceiveWindowManagerImpl) SetWindowSize(size uint64) error {
	if size < rwm.config.MinSize {
		return fmt.Errorf("window size %d is below minimum %d", size, rwm.config.MinSize)
	}
	if size > rwm.config.MaxSize {
		return fmt.Errorf("window size %d exceeds maximum %d", size, rwm.config.MaxSize)
	}
	
	rwm.sizeMutex.Lock()
	defer rwm.sizeMutex.Unlock()
	
	// Check if we can shrink without affecting current allocations
	if size < rwm.totalSize && rwm.usedSize > size {
		return fmt.Errorf("cannot shrink window to %d: would exceed used size %d", size, rwm.usedSize)
	}
	
	oldSize := rwm.totalSize
	rwm.totalSize = size
	
	// Update statistics (sizeMutex already held)
	rwm.updateStatsLocked()
	
	// Trigger callbacks if window became available
	if oldSize < size && rwm.isBackpressureActive {
		rwm.checkBackpressureRelease()
	}
	
	return nil
}

// GetWindowSize returns the total window size
func (rwm *ReceiveWindowManagerImpl) GetWindowSize() uint64 {
	rwm.sizeMutex.RLock()
	defer rwm.sizeMutex.RUnlock()
	return rwm.totalSize
}

// GetAvailableWindow returns the available window space
func (rwm *ReceiveWindowManagerImpl) GetAvailableWindow() uint64 {
	rwm.sizeMutex.RLock()
	defer rwm.sizeMutex.RUnlock()
	
	if rwm.usedSize >= rwm.totalSize {
		return 0
	}
	return rwm.totalSize - rwm.usedSize
}

// AllocateWindow allocates window space for a stream
func (rwm *ReceiveWindowManagerImpl) AllocateWindow(streamID uint64, size uint64) error {
	if size == 0 {
		return fmt.Errorf("cannot allocate zero bytes")
	}
	
	// Use atomic allocation to prevent race conditions
	return rwm.AllocateWindowAtomic(streamID, size)
}

// AllocateWindowAtomic atomically allocates window space for a stream
func (rwm *ReceiveWindowManagerImpl) AllocateWindowAtomic(streamID uint64, size uint64) error {
	// Acquire all necessary locks in a consistent order to prevent deadlocks
	// Order: allocationsMutex -> sizeMutex -> statsMutex -> backpressureMutex
	
	rwm.allocationsMutex.Lock()
	defer rwm.allocationsMutex.Unlock()
	
	rwm.sizeMutex.Lock()
	defer rwm.sizeMutex.Unlock()
	
	// Check stream limit while holding the allocation mutex
	if len(rwm.streamAllocations) >= rwm.config.MaxStreams {
		if _, exists := rwm.streamAllocations[streamID]; !exists {
			return fmt.Errorf("maximum number of streams (%d) reached", rwm.config.MaxStreams)
		}
	}
	
	// Check if we have enough space while holding the size mutex
	if rwm.usedSize+size > rwm.totalSize {
		// Trigger backpressure
		rwm.activateBackpressure()
		return fmt.Errorf("insufficient window space: requested %d, available %d", 
			size, rwm.totalSize-rwm.usedSize)
	}
	
	// Allocate the space atomically
	rwm.usedSize += size
	
	// Update stream allocation (already holding allocationsMutex)
	if existing, exists := rwm.streamAllocations[streamID]; exists {
		rwm.streamAllocations[streamID] = existing + size
	} else {
		rwm.streamAllocations[streamID] = size
	}
	
	// Update statistics
	rwm.statsMutex.Lock()
	rwm.stats.TotalAllocations++
	rwm.statsMutex.Unlock()
	
	rwm.updateStatsLocked()
	
	// Check if window became full and trigger callback (check utilization while holding sizeMutex)
	utilization := float64(rwm.usedSize) / float64(rwm.totalSize)
	isWindowFull := utilization >= rwm.config.BackpressureThreshold
	
	if isWindowFull {
		rwm.backpressureMutex.Lock()
		wasActive := rwm.isBackpressureActive
		rwm.isBackpressureActive = true
		rwm.backpressureMutex.Unlock()
		
		if !wasActive {
			// Update statistics
			rwm.statsMutex.Lock()
			rwm.stats.BackpressureEvents++
			rwm.statsMutex.Unlock()
			
			// Trigger callback
			rwm.callbacksMutex.RLock()
			callback := rwm.windowFullCallback
			rwm.callbacksMutex.RUnlock()
			
			if callback != nil {
				go callback() // Run in goroutine to avoid blocking
			}
		}
	}
	
	return nil
}

// TryAllocateWindow attempts to allocate window space without blocking
func (rwm *ReceiveWindowManagerImpl) TryAllocateWindow(streamID uint64, size uint64) error {
	// Use a timeout-based approach since TryLock is not available in older Go versions
	allocDone := make(chan bool, 1)
	sizeDone := make(chan bool, 1)
	
	// Try to acquire allocation mutex with timeout
	go func() {
		rwm.allocationsMutex.Lock()
		allocDone <- true
	}()
	
	select {
	case <-allocDone:
		defer rwm.allocationsMutex.Unlock()
	case <-time.After(10 * time.Millisecond):
		return fmt.Errorf("allocation mutex busy")
	}
	
	// Try to acquire size mutex with timeout
	go func() {
		rwm.sizeMutex.Lock()
		sizeDone <- true
	}()
	
	select {
	case <-sizeDone:
		defer rwm.sizeMutex.Unlock()
	case <-time.After(10 * time.Millisecond):
		return fmt.Errorf("size mutex busy")
	}
	
	// Check stream limit
	if len(rwm.streamAllocations) >= rwm.config.MaxStreams {
		if _, exists := rwm.streamAllocations[streamID]; !exists {
			return fmt.Errorf("maximum number of streams (%d) reached", rwm.config.MaxStreams)
		}
	}
	
	// Check if we have enough space
	if rwm.usedSize+size > rwm.totalSize {
		return fmt.Errorf("insufficient window space: requested %d, available %d", 
			size, rwm.totalSize-rwm.usedSize)
	}
	
	// Allocate the space
	rwm.usedSize += size
	
	// Update stream allocation
	if existing, exists := rwm.streamAllocations[streamID]; exists {
		rwm.streamAllocations[streamID] = existing + size
	} else {
		rwm.streamAllocations[streamID] = size
	}
	
	// Update statistics
	rwm.statsMutex.Lock()
	rwm.stats.TotalAllocations++
	rwm.statsMutex.Unlock()
	
	return nil
}

// CheckResourceAvailability checks if resources are available without allocating
func (rwm *ReceiveWindowManagerImpl) CheckResourceAvailability(streamID uint64, size uint64) error {
	rwm.allocationsMutex.RLock()
	defer rwm.allocationsMutex.RUnlock()
	
	rwm.sizeMutex.RLock()
	defer rwm.sizeMutex.RUnlock()
	
	// Check stream limit
	if len(rwm.streamAllocations) >= rwm.config.MaxStreams {
		if _, exists := rwm.streamAllocations[streamID]; !exists {
			return fmt.Errorf("maximum number of streams (%d) reached", rwm.config.MaxStreams)
		}
	}
	
	// Check if we have enough space
	if rwm.usedSize+size > rwm.totalSize {
		return fmt.Errorf("insufficient window space: requested %d, available %d", 
			size, rwm.totalSize-rwm.usedSize)
	}
	
	return nil
}

// ReleaseWindow releases window space for a stream
func (rwm *ReceiveWindowManagerImpl) ReleaseWindow(streamID uint64, size uint64) error {
	if size == 0 {
		return nil // Nothing to release
	}
	
	rwm.allocationsMutex.Lock()
	currentAllocation, exists := rwm.streamAllocations[streamID]
	if !exists {
		rwm.allocationsMutex.Unlock()
		return fmt.Errorf("no allocation found for stream %d", streamID)
	}
	
	if size > currentAllocation {
		rwm.allocationsMutex.Unlock()
		return fmt.Errorf("cannot release %d bytes: stream %d only has %d bytes allocated", 
			size, streamID, currentAllocation)
	}
	
	// Update stream allocation
	newAllocation := currentAllocation - size
	if newAllocation == 0 {
		delete(rwm.streamAllocations, streamID)
	} else {
		rwm.streamAllocations[streamID] = newAllocation
	}
	rwm.allocationsMutex.Unlock()
	
	// Release the space
	rwm.sizeMutex.Lock()
	if rwm.usedSize >= size {
		rwm.usedSize -= size
	} else {
		// This shouldn't happen, but if it does, clamp to 0
		rwm.usedSize = 0
	}
	rwm.sizeMutex.Unlock()
	
	// Update statistics
	rwm.statsMutex.Lock()
	rwm.stats.TotalReleases++
	rwm.statsMutex.Unlock()
	
	rwm.updateStats()
	
	// Check if backpressure can be released
	rwm.checkBackpressureRelease()
	
	// Trigger window available callback if window became available
	if !rwm.IsWindowFull() {
		rwm.callbacksMutex.RLock()
		callback := rwm.windowAvailableCallback
		rwm.callbacksMutex.RUnlock()
		
		if callback != nil {
			go callback() // Run in goroutine to avoid blocking
		}
	}
	
	return nil
}

// SlideWindow slides the window forward by the specified amount
func (rwm *ReceiveWindowManagerImpl) SlideWindow(bytesConsumed uint64) error {
	if bytesConsumed == 0 {
		return nil
	}
	
	rwm.slideMutex.Lock()
	defer rwm.slideMutex.Unlock()
	
	// Update slide position
	rwm.slidePosition += bytesConsumed
	rwm.lastSlideTime = time.Now()
	
	// Free up window space by reducing used size
	// This makes space available for new allocations without affecting existing stream allocations
	rwm.sizeMutex.Lock()
	wasBackpressureActive := rwm.usedSize >= rwm.totalSize
	if rwm.usedSize >= bytesConsumed {
		rwm.usedSize -= bytesConsumed
	} else {
		rwm.usedSize = 0
	}
	nowBackpressureActive := rwm.usedSize >= rwm.totalSize
	rwm.sizeMutex.Unlock()
	
	// Update statistics
	rwm.statsMutex.Lock()
	rwm.stats.TotalSlides++
	rwm.stats.BytesSlid += bytesConsumed
	rwm.statsMutex.Unlock()
	
	// Check if backpressure status changed
	if wasBackpressureActive && !nowBackpressureActive {
		rwm.checkBackpressureRelease()
	}
	
	// Trigger window available callback if window became available
	if !rwm.IsWindowFull() && rwm.windowAvailableCallback != nil {
		go rwm.windowAvailableCallback()
	}
	
	return nil
}

// GetWindowStatus returns the current window status
func (rwm *ReceiveWindowManagerImpl) GetWindowStatus() *ReceiveWindowStatus {
	rwm.sizeMutex.RLock()
	totalSize := rwm.totalSize
	usedSize := rwm.usedSize
	rwm.sizeMutex.RUnlock()
	
	rwm.allocationsMutex.RLock()
	streamAllocations := make(map[uint64]uint64)
	for streamID, allocation := range rwm.streamAllocations {
		streamAllocations[streamID] = allocation
	}
	rwm.allocationsMutex.RUnlock()
	
	rwm.slideMutex.RLock()
	lastSlideTime := rwm.lastSlideTime
	rwm.slideMutex.RUnlock()
	
	rwm.backpressureMutex.RLock()
	isBackpressureActive := rwm.isBackpressureActive
	rwm.backpressureMutex.RUnlock()
	
	availableSize := totalSize - usedSize
	utilization := float64(usedSize) / float64(totalSize)
	
	return &ReceiveWindowStatus{
		TotalSize:             totalSize,
		UsedSize:              usedSize,
		AvailableSize:         availableSize,
		Utilization:           utilization,
		StreamAllocations:     streamAllocations,
		IsBackpressureActive:  isBackpressureActive,
		LastSlideTime:         lastSlideTime,
	}
}

// IsWindowFull returns true if the window is full (above backpressure threshold)
func (rwm *ReceiveWindowManagerImpl) IsWindowFull() bool {
	utilization := rwm.GetWindowUtilization()
	return utilization >= rwm.config.BackpressureThreshold
}

// GetWindowUtilization returns the current window utilization (0.0 to 1.0)
func (rwm *ReceiveWindowManagerImpl) GetWindowUtilization() float64 {
	rwm.sizeMutex.RLock()
	defer rwm.sizeMutex.RUnlock()
	
	if rwm.totalSize == 0 {
		return 0.0
	}
	
	// Prevent overflow and ensure valid range
	if rwm.usedSize > rwm.totalSize {
		// This shouldn't happen, but if it does, clamp to 100%
		return 1.0
	}
	
	utilization := float64(rwm.usedSize) / float64(rwm.totalSize)
	
	// Ensure utilization is in valid range [0.0, 1.0]
	if utilization < 0.0 {
		return 0.0
	}
	if utilization > 1.0 {
		return 1.0
	}
	
	return utilization
}

// SetWindowFullCallback sets the callback for when the window becomes full
func (rwm *ReceiveWindowManagerImpl) SetWindowFullCallback(callback func()) error {
	rwm.callbacksMutex.Lock()
	defer rwm.callbacksMutex.Unlock()
	
	rwm.windowFullCallback = callback
	return nil
}

// SetWindowAvailableCallback sets the callback for when the window becomes available
func (rwm *ReceiveWindowManagerImpl) SetWindowAvailableCallback(callback func()) error {
	rwm.callbacksMutex.Lock()
	defer rwm.callbacksMutex.Unlock()
	
	rwm.windowAvailableCallback = callback
	return nil
}

// Helper methods

// activateBackpressure activates backpressure and triggers callbacks
func (rwm *ReceiveWindowManagerImpl) activateBackpressure() {
	rwm.backpressureMutex.Lock()
	wasActive := rwm.isBackpressureActive
	rwm.isBackpressureActive = true
	rwm.backpressureMutex.Unlock()
	
	if !wasActive {
		// Update statistics
		rwm.statsMutex.Lock()
		rwm.stats.BackpressureEvents++
		rwm.statsMutex.Unlock()
		
		// Trigger callback
		rwm.callbacksMutex.RLock()
		callback := rwm.windowFullCallback
		rwm.callbacksMutex.RUnlock()
		
		if callback != nil {
			go callback() // Run in goroutine to avoid blocking
		}
	}
}

// checkBackpressureRelease checks if backpressure can be released
func (rwm *ReceiveWindowManagerImpl) checkBackpressureRelease() {
	if !rwm.IsWindowFull() {
		rwm.backpressureMutex.Lock()
		wasActive := rwm.isBackpressureActive
		rwm.isBackpressureActive = false
		rwm.backpressureMutex.Unlock()
		
		if wasActive {
			// Trigger callback
			rwm.callbacksMutex.RLock()
			callback := rwm.windowAvailableCallback
			rwm.callbacksMutex.RUnlock()
			
			if callback != nil {
				go callback() // Run in goroutine to avoid blocking
			}
		}
	}
}

// updateStats updates internal statistics
func (rwm *ReceiveWindowManagerImpl) updateStats() {
	utilization := rwm.GetWindowUtilization()
	
	rwm.statsMutex.Lock()
	defer rwm.statsMutex.Unlock()
	
	// Update peak utilization
	if utilization > rwm.stats.PeakUtilization {
		rwm.stats.PeakUtilization = utilization
	}
}

// updateStatsLocked updates internal statistics when sizeMutex is already held
func (rwm *ReceiveWindowManagerImpl) updateStatsLocked() {
	var utilization float64
	if rwm.totalSize == 0 {
		utilization = 0.0
	} else {
		utilization = float64(rwm.usedSize) / float64(rwm.totalSize)
	}
	
	rwm.statsMutex.Lock()
	defer rwm.statsMutex.Unlock()
	
	// Update peak utilization
	if utilization > rwm.stats.PeakUtilization {
		rwm.stats.PeakUtilization = utilization
	}
	
	// Update average utilization (simple moving average)
	// This is simplified - a real implementation might use a more sophisticated approach
	if rwm.stats.LastUpdate.IsZero() {
		rwm.stats.AverageUtilization = utilization
	} else {
		// Weight recent measurements more heavily
		alpha := 0.1 // Smoothing factor
		rwm.stats.AverageUtilization = alpha*utilization + (1-alpha)*rwm.stats.AverageUtilization
	}
	
	rwm.stats.LastUpdate = time.Now()
}

// autoSlideWorker runs the automatic sliding worker
func (rwm *ReceiveWindowManagerImpl) autoSlideWorker() {
	ticker := time.NewTicker(rwm.config.AutoSlideInterval)
	defer ticker.Stop()
	
	for range ticker.C {
		// Check if we should perform an automatic slide
		rwm.slideMutex.RLock()
		timeSinceLastSlide := time.Since(rwm.lastSlideTime)
		rwm.slideMutex.RUnlock()
		
		// If it's been a while since the last slide and we have some utilization,
		// perform a small slide to keep the window moving
		if timeSinceLastSlide > rwm.config.AutoSlideInterval*2 {
			utilization := rwm.GetWindowUtilization()
			if utilization > 0.1 { // Only slide if we have some data
				slideAmount := rwm.config.SlideSize
				if slideAmount > 0 {
					rwm.SlideWindow(slideAmount)
				}
			}
		}
	}
}

// GetStats returns the current window manager statistics
func (rwm *ReceiveWindowManagerImpl) GetStats() *WindowManagerStats {
	rwm.statsMutex.RLock()
	defer rwm.statsMutex.RUnlock()
	
	// Return a copy to avoid race conditions
	return &WindowManagerStats{
		TotalAllocations:   rwm.stats.TotalAllocations,
		TotalReleases:      rwm.stats.TotalReleases,
		TotalSlides:        rwm.stats.TotalSlides,
		BytesSlid:          rwm.stats.BytesSlid,
		BackpressureEvents: rwm.stats.BackpressureEvents,
		PeakUtilization:    rwm.stats.PeakUtilization,
		AverageUtilization: rwm.stats.AverageUtilization,
		LastUpdate:         rwm.stats.LastUpdate,
	}
}

// Shutdown gracefully shuts down the window manager
func (rwm *ReceiveWindowManagerImpl) Shutdown() error {
	// Stop dynamic adjustment worker
	if rwm.dynamicAdjustmentEnabled {
		close(rwm.stopDynamicAdjustment)
		rwm.dynamicWorkerWg.Wait()
	}
	
	// Clear all allocations
	rwm.allocationsMutex.Lock()
	rwm.streamAllocations = make(map[uint64]uint64)
	rwm.allocationsMutex.Unlock()
	
	// Reset used size
	rwm.sizeMutex.Lock()
	rwm.usedSize = 0
	rwm.sizeMutex.Unlock()
	
	// Deactivate backpressure
	rwm.backpressureMutex.Lock()
	rwm.isBackpressureActive = false
	rwm.backpressureMutex.Unlock()
	
	return nil
}

// Advanced sliding mechanisms

// SlideWindowToPosition slides the window to a specific position
func (rwm *ReceiveWindowManagerImpl) SlideWindowToPosition(position uint64) error {
	rwm.slideMutex.Lock()
	defer rwm.slideMutex.Unlock()
	
	if position < rwm.slidePosition {
		return fmt.Errorf("cannot slide backward: current position %d, requested %d", 
			rwm.slidePosition, position)
	}
	
	bytesToSlide := position - rwm.slidePosition
	if bytesToSlide == 0 {
		return nil // Already at position
	}
	
	// Update slide position
	rwm.slidePosition = position
	rwm.lastSlideTime = time.Now()
	
	// Update statistics
	rwm.statsMutex.Lock()
	rwm.stats.TotalSlides++
	rwm.stats.BytesSlid += bytesToSlide
	rwm.statsMutex.Unlock()
	
	return nil
}

// GetSlidePosition returns the current slide position
func (rwm *ReceiveWindowManagerImpl) GetSlidePosition() uint64 {
	rwm.slideMutex.RLock()
	defer rwm.slideMutex.RUnlock()
	return rwm.slidePosition
}

// GetSlideInfo returns detailed information about sliding
func (rwm *ReceiveWindowManagerImpl) GetSlideInfo() *SlideInfo {
	rwm.slideMutex.RLock()
	defer rwm.slideMutex.RUnlock()
	
	rwm.statsMutex.RLock()
	defer rwm.statsMutex.RUnlock()
	
	return &SlideInfo{
		CurrentPosition:   rwm.slidePosition,
		LastSlideTime:     rwm.lastSlideTime,
		TotalSlides:       rwm.stats.TotalSlides,
		TotalBytesSlid:    rwm.stats.BytesSlid,
		AutoSlideEnabled:  rwm.config.AutoSlideEnabled,
		SlideInterval:     rwm.config.AutoSlideInterval,
		SlideSize:         rwm.config.SlideSize,
	}
}

// SetSlideConfiguration updates the sliding configuration
func (rwm *ReceiveWindowManagerImpl) SetSlideConfiguration(config *SlideConfig) error {
	if config.SlideSize == 0 {
		return fmt.Errorf("slide size cannot be zero")
	}
	if config.AutoSlideInterval <= 0 {
		return fmt.Errorf("auto slide interval must be positive")
	}
	
	// Update configuration
	rwm.config.SlideSize = config.SlideSize
	rwm.config.AutoSlideEnabled = config.AutoSlideEnabled
	rwm.config.AutoSlideInterval = config.AutoSlideInterval
	
	return nil
}

// ForceSlide forces a slide operation regardless of conditions
func (rwm *ReceiveWindowManagerImpl) ForceSlide(bytes uint64) error {
	if bytes == 0 {
		return fmt.Errorf("cannot force slide zero bytes")
	}
	
	return rwm.SlideWindow(bytes)
}

// CalculateOptimalSlideSize calculates the optimal slide size based on current conditions
func (rwm *ReceiveWindowManagerImpl) CalculateOptimalSlideSize() uint64 {
	utilization := rwm.GetWindowUtilization()
	
	// Base slide size on utilization and configuration
	baseSize := rwm.config.SlideSize
	
	if utilization > 0.8 {
		// High utilization - slide more aggressively
		return baseSize * 2
	} else if utilization < 0.2 {
		// Low utilization - slide less
		return baseSize / 2
	}
	
	return baseSize
}

// PerformAdaptiveSlide performs a slide with adaptive sizing
func (rwm *ReceiveWindowManagerImpl) PerformAdaptiveSlide() error {
	optimalSize := rwm.CalculateOptimalSlideSize()
	return rwm.SlideWindow(optimalSize)
}

// GetSlidingMetrics returns metrics about sliding performance
func (rwm *ReceiveWindowManagerImpl) GetSlidingMetrics() *SlidingMetrics {
	rwm.statsMutex.RLock()
	defer rwm.statsMutex.RUnlock()
	
	rwm.slideMutex.RLock()
	defer rwm.slideMutex.RUnlock()
	
	var averageSlideSize uint64
	if rwm.stats.TotalSlides > 0 {
		averageSlideSize = rwm.stats.BytesSlid / rwm.stats.TotalSlides
	}
	
	timeSinceLastSlide := time.Since(rwm.lastSlideTime)
	
	return &SlidingMetrics{
		TotalSlides:        rwm.stats.TotalSlides,
		TotalBytesSlid:     rwm.stats.BytesSlid,
		AverageSlideSize:   averageSlideSize,
		LastSlideTime:      rwm.lastSlideTime,
		TimeSinceLastSlide: timeSinceLastSlide,
		CurrentPosition:    rwm.slidePosition,
		SlidesPerSecond:    rwm.calculateSlidesPerSecond(),
	}
}

// calculateSlidesPerSecond calculates the current slides per second rate
func (rwm *ReceiveWindowManagerImpl) calculateSlidesPerSecond() float64 {
	if rwm.stats.TotalSlides == 0 {
		return 0
	}
	
	// This is simplified - a real implementation would track this over a time window
	timeSinceStart := time.Since(rwm.stats.LastUpdate)
	if timeSinceStart.Seconds() == 0 {
		return 0
	}
	
	return float64(rwm.stats.TotalSlides) / timeSinceStart.Seconds()
}

// Dynamic Window Management Methods

// dynamicAdjustmentWorker runs the dynamic window adjustment worker
func (rwm *ReceiveWindowManagerImpl) dynamicAdjustmentWorker() {
	defer rwm.dynamicWorkerWg.Done()
	
	ticker := time.NewTicker(rwm.config.DynamicAdjustInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			rwm.performDynamicAdjustment()
		case <-rwm.stopDynamicAdjustment:
			return
		}
	}
}

// performDynamicAdjustment performs dynamic window size adjustment based on current conditions
func (rwm *ReceiveWindowManagerImpl) performDynamicAdjustment() {
	// Check if adjustment is enabled without holding locks
	if !rwm.dynamicAdjustmentEnabled {
		return
	}
	
	rwm.adjustmentMutex.Lock()
	defer rwm.adjustmentMutex.Unlock()
	
	// Update memory statistics
	rwm.memoryMonitor.UpdateMemoryStats()
	
	// Get current metrics (avoid nested locking)
	rwm.sizeMutex.RLock()
	currentSize := rwm.totalSize
	usedSize := rwm.usedSize
	rwm.sizeMutex.RUnlock()
	
	// Calculate utilization without calling GetWindowUtilization to avoid nested locks
	var currentUtilization float64
	if currentSize > 0 {
		currentUtilization = float64(usedSize) / float64(currentSize)
	}
	
	memoryUsage := rwm.memoryMonitor.GetMemoryUsage()
	
	// Determine if we should grow or shrink the window
	shouldGrow := rwm.shouldGrowWindow(currentUtilization, memoryUsage)
	shouldShrink := rwm.shouldShrinkWindow(currentUtilization, memoryUsage)
	
	var newSize uint64
	var adjusted bool
	
	if shouldGrow && !shouldShrink {
		// Grow the window
		newSize = uint64(float64(currentSize) * rwm.config.GrowthFactor)
		if newSize > rwm.config.MaxSize {
			newSize = rwm.config.MaxSize
		}
		if newSize != currentSize {
			adjusted = true
		}
	} else if shouldShrink && !shouldGrow {
		// Shrink the window
		newSize = uint64(float64(currentSize) * rwm.config.ShrinkFactor)
		if newSize < rwm.config.MinSize {
			newSize = rwm.config.MinSize
		}
		if newSize != currentSize {
			adjusted = true
		}
	}
	
	// Apply the adjustment if needed
	if adjusted {
		err := rwm.SetWindowSize(newSize)
		if err == nil {
			rwm.lastAdjustmentTime = time.Now()
			
			// Log the adjustment (in a real implementation, this would use proper logging)
			// fmt.Printf("Dynamic window adjustment: %d -> %d (utilization: %.2f, memory: %.2f)\n",
			//	currentSize, newSize, currentUtilization, memoryUsage)
		}
	}
}

// shouldGrowWindow determines if the window should be grown
func (rwm *ReceiveWindowManagerImpl) shouldGrowWindow(utilization, memoryUsage float64) bool {
	// Don't grow if memory pressure is high
	if memoryUsage > rwm.config.MemoryPressureThreshold {
		return false
	}
	
	// Don't grow if we're already at max size
	rwm.sizeMutex.RLock()
	atMaxSize := rwm.totalSize >= rwm.config.MaxSize
	rwm.sizeMutex.RUnlock()
	if atMaxSize {
		return false
	}
	
	// Grow if utilization is high (above 80% of backpressure threshold)
	growThreshold := rwm.config.BackpressureThreshold * 0.8
	if utilization > growThreshold {
		return true
	}
	
	// Grow if we have many active streams and low memory pressure
	rwm.allocationsMutex.RLock()
	activeStreams := len(rwm.streamAllocations)
	rwm.allocationsMutex.RUnlock()
	
	if activeStreams > rwm.config.MaxStreams/2 && memoryUsage < 0.6 {
		return true
	}
	
	return false
}

// shouldShrinkWindow determines if the window should be shrunk
func (rwm *ReceiveWindowManagerImpl) shouldShrinkWindow(utilization, memoryUsage float64) bool {
	// Shrink if memory pressure is very high
	if memoryUsage > rwm.config.MemoryPressureThreshold {
		return true
	}
	
	// Don't shrink if we're already at min size
	rwm.sizeMutex.RLock()
	atMinSize := rwm.totalSize <= rwm.config.MinSize
	rwm.sizeMutex.RUnlock()
	if atMinSize {
		return false
	}
	
	// Shrink if utilization is very low (below 25% of backpressure threshold)
	shrinkThreshold := rwm.config.BackpressureThreshold * 0.25
	if utilization < shrinkThreshold {
		return true
	}
	
	// Shrink if we have few active streams
	rwm.allocationsMutex.RLock()
	activeStreams := len(rwm.streamAllocations)
	rwm.allocationsMutex.RUnlock()
	
	if activeStreams < rwm.config.MaxStreams/4 && utilization < 0.5 {
		return true
	}
	
	return false
}

// GetDynamicWindowStats returns statistics about dynamic window management
func (rwm *ReceiveWindowManagerImpl) GetDynamicWindowStats() *DynamicWindowStats {
	rwm.adjustmentMutex.RLock()
	defer rwm.adjustmentMutex.RUnlock()
	
	rwm.sizeMutex.RLock()
	currentSize := rwm.totalSize
	rwm.sizeMutex.RUnlock()
	
	return &DynamicWindowStats{
		Enabled:              rwm.dynamicAdjustmentEnabled,
		CurrentSize:          currentSize,
		MinSize:              rwm.config.MinSize,
		MaxSize:              rwm.config.MaxSize,
		LastAdjustmentTime:   rwm.lastAdjustmentTime,
		MemoryUsage:          rwm.memoryMonitor.GetMemoryUsage(),
		WindowUtilization:    rwm.GetWindowUtilization(),
		GrowthFactor:         rwm.config.GrowthFactor,
		ShrinkFactor:         rwm.config.ShrinkFactor,
		MemoryPressureThreshold: rwm.config.MemoryPressureThreshold,
	}
}

// SetDynamicWindowConfig updates the dynamic window configuration
func (rwm *ReceiveWindowManagerImpl) SetDynamicWindowConfig(enabled bool, growthFactor, shrinkFactor, memoryThreshold float64) error {
	if growthFactor <= 1.0 {
		return fmt.Errorf("growth factor must be > 1.0")
	}
	if shrinkFactor <= 0.0 || shrinkFactor >= 1.0 {
		return fmt.Errorf("shrink factor must be between 0.0 and 1.0")
	}
	if memoryThreshold <= 0.0 || memoryThreshold > 1.0 {
		return fmt.Errorf("memory threshold must be between 0.0 and 1.0")
	}
	
	rwm.adjustmentMutex.Lock()
	defer rwm.adjustmentMutex.Unlock()
	
	// Update configuration
	rwm.config.EnableDynamicSizing = enabled
	rwm.config.GrowthFactor = growthFactor
	rwm.config.ShrinkFactor = shrinkFactor
	rwm.config.MemoryPressureThreshold = memoryThreshold
	
	// Update enabled state
	wasEnabled := rwm.dynamicAdjustmentEnabled
	rwm.dynamicAdjustmentEnabled = enabled
	
	// Start or stop worker as needed
	if enabled && !wasEnabled {
		// Create new stop channel if needed
		if rwm.stopDynamicAdjustment == nil {
			rwm.stopDynamicAdjustment = make(chan struct{})
		}
		rwm.dynamicWorkerWg.Add(1)
		go rwm.dynamicAdjustmentWorker()
	} else if !enabled && wasEnabled {
		// Worker will stop on next tick when it sees the disabled state
	}
	
	return nil
}

// ForceWindowAdjustment forces an immediate window size adjustment
func (rwm *ReceiveWindowManagerImpl) ForceWindowAdjustment() error {
	if !rwm.dynamicAdjustmentEnabled {
		return fmt.Errorf("dynamic window adjustment is not enabled")
	}
	
	rwm.performDynamicAdjustment()
	return nil
}