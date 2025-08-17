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
		}
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
	}
	
	// Start auto-slide goroutine if enabled
	if config.AutoSlideEnabled {
		go rwm.autoSlideWorker()
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
	
	// Check stream limit
	rwm.allocationsMutex.RLock()
	if len(rwm.streamAllocations) >= rwm.config.MaxStreams {
		if _, exists := rwm.streamAllocations[streamID]; !exists {
			rwm.allocationsMutex.RUnlock()
			return fmt.Errorf("maximum number of streams (%d) reached", rwm.config.MaxStreams)
		}
	}
	rwm.allocationsMutex.RUnlock()
	
	rwm.sizeMutex.Lock()
	defer rwm.sizeMutex.Unlock()
	
	// Check if we have enough space
	if rwm.usedSize+size > rwm.totalSize {
		// Trigger backpressure
		rwm.activateBackpressure()
		return fmt.Errorf("insufficient window space: requested %d, available %d", 
			size, rwm.totalSize-rwm.usedSize)
	}
	
	// Allocate the space
	rwm.usedSize += size
	
	// Update stream allocation
	rwm.allocationsMutex.Lock()
	if existing, exists := rwm.streamAllocations[streamID]; exists {
		rwm.streamAllocations[streamID] = existing + size
	} else {
		rwm.streamAllocations[streamID] = size
	}
	rwm.allocationsMutex.Unlock()
	
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