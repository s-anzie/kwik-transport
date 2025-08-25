package presentation

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"kwik/pkg/stream"
)

// DataPresentationManagerImpl implements the DataPresentationManager interface
type DataPresentationManagerImpl struct {
	// Stream buffers management
	streamBuffers map[uint64]*StreamBufferImpl
	buffersMutex  sync.RWMutex

	// Window and backpressure managers
	windowManager       *ReceiveWindowManagerImpl
	backpressureManager *BackpressureManagerImpl

	// Resource reservation system
	reservationManager *ResourceReservationManager

	// Garbage collection system
	garbageCollector *ResourceGarbageCollector
	
	// Resource monitoring system
	resourceMonitor *ResourceMonitor
	
	// Resource configuration manager
	resourceConfigManager *ResourceConfigManager

	// Configuration
	config *PresentationConfig

	// Statistics
	globalStats *GlobalPresentationStatsImpl
	statsMutex  sync.RWMutex

	// Background workers
	workerStop chan struct{}
	workerWg   sync.WaitGroup

	// State
	running bool
	mutex   sync.RWMutex

	// Logger
	logger stream.StreamLogger
}

// GlobalPresentationStatsImpl implements detailed global statistics
type GlobalPresentationStatsImpl struct {
	ActiveStreams      int           `json:"active_streams"`
	TotalBytesWritten  uint64        `json:"total_bytes_written"`
	TotalBytesRead     uint64        `json:"total_bytes_read"`
	WindowUtilization  float64       `json:"window_utilization"`
	BackpressureEvents uint64        `json:"backpressure_events"`
	AverageLatency     time.Duration `json:"average_latency"`
	LastUpdate         time.Time     `json:"last_update"`

	// Additional detailed stats
	TotalWriteOperations uint64        `json:"total_write_operations"`
	TotalReadOperations  uint64        `json:"total_read_operations"`
	ErrorCount           uint64        `json:"error_count"`
	TimeoutCount         uint64        `json:"timeout_count"`
	PeakActiveStreams    int           `json:"peak_active_streams"`
	AverageStreamLife    time.Duration `json:"average_stream_life"`
}

// NewDataPresentationManager creates a new data presentation manager
func NewDataPresentationManager(config *PresentationConfig) *DataPresentationManagerImpl {
	if config == nil {
		config = DefaultPresentationConfig()
	}

	// Validate configuration
	if err := config.Validate(); err != nil {
		panic(fmt.Sprintf("invalid presentation config: %v", err))
	}

	// Create window manager
	windowConfig := &WindowManagerConfig{
		InitialSize:           config.ReceiveWindowSize,
		MaxSize:               config.ReceiveWindowSize * 2,
		MinSize:               config.ReceiveWindowSize / 4,
		BackpressureThreshold: config.BackpressureThreshold,
		SlideSize:             config.WindowSlideSize,
		AutoSlideEnabled:      true,
		AutoSlideInterval:     100 * time.Millisecond,
		MaxStreams:            1000,
	}
	windowManager := NewReceiveWindowManager(windowConfig)

	// Create backpressure manager
	backpressureConfig := &BackpressureConfig{
		MaxStreams:            1000,
		GlobalThreshold:       config.BackpressureThreshold,
		StreamThreshold:       config.StreamBackpressureThreshold,
		BackoffInitial:        10 * time.Millisecond,
		BackoffMax:            1 * time.Second,
		BackoffMultiplier:     2.0,
		AutoReleaseEnabled:    true,
		AutoReleaseInterval:   100 * time.Millisecond,
		StatsUpdateInterval:   config.MetricsUpdateInterval,
		EnableDetailedLogging: config.EnableDebugLogging,
	}
	backpressureManager := NewBackpressureManager(backpressureConfig)

	// Create resource reservation manager
	reservationManager := NewResourceReservationManager(
		5*time.Second, // 5 second reservation timeout
		100,           // Max 100 concurrent reservations
	)

	dpm := &DataPresentationManagerImpl{
		streamBuffers:       make(map[uint64]*StreamBufferImpl),
		windowManager:       windowManager,
		backpressureManager: backpressureManager,
		reservationManager:  reservationManager,
		config:              config,
		globalStats: &GlobalPresentationStatsImpl{
			ActiveStreams:        0,
			TotalBytesWritten:    0,
			TotalBytesRead:       0,
			WindowUtilization:    0,
			BackpressureEvents:   0,
			AverageLatency:       0,
			LastUpdate:           time.Now(),
			TotalWriteOperations: 0,
			TotalReadOperations:  0,
			ErrorCount:           0,
			TimeoutCount:         0,
			PeakActiveStreams:    0,
			AverageStreamLife:    0,
		},
		workerStop: make(chan struct{}),
		running:    false,
		logger:     nil,
	}

	// Set up backpressure callback
	backpressureManager.SetBackpressureCallback(dpm.handleBackpressureEvent)

	// Create garbage collector
	gcConfig := DefaultGCConfig()
	gcConfig.EnableImmediateCleanup = config.EnableMemoryPooling // Use memory pooling config as proxy
	dpm.garbageCollector = NewResourceGarbageCollector(dpm, windowManager, gcConfig)
	
	// Create resource monitor
	memoryPool := GetGlobalMemoryManager().pool
	monitorConfig := DefaultResourceMonitorConfig()
	monitorConfig.EnableLeakDetection = true
	monitorConfig.EnableTrendAnalysis = config.EnableDetailedMetrics
	dpm.resourceMonitor = NewResourceMonitor(dpm, windowManager, memoryPool, dpm.garbageCollector, monitorConfig)
	
	// Create resource configuration manager
	dpm.resourceConfigManager = NewResourceConfigManager()

	return dpm
}

// SetLogger sets the logger for DPM
func (dpm *DataPresentationManagerImpl) SetLogger(l stream.StreamLogger) { dpm.logger = l }

// CreateStreamBuffer creates a new stream buffer
func (dpm *DataPresentationManagerImpl) CreateStreamBuffer(streamID uint64, metadata interface{}) error {
	if streamID == 0 {
		return fmt.Errorf("invalid stream ID: 0")
	}

	// Convert metadata to the expected type
	var streamMetadata *StreamMetadata
	if metadata != nil {
		if sm, ok := metadata.(*StreamMetadata); ok {
			streamMetadata = sm
		}
	}

	// Use atomic stream creation to prevent race conditions
	return dpm.createStreamBufferAtomic(streamID, streamMetadata)
}

// createStreamBufferAtomic atomically creates a stream buffer with resource reservation
func (dpm *DataPresentationManagerImpl) createStreamBufferAtomic(streamID uint64, streamMetadata *StreamMetadata) error {
	// Step 1: Reserve resources first
	bufferSize := dpm.calculateAdaptiveBufferSize(streamMetadata)
	
	priority := StreamPriorityNormal
	if streamMetadata != nil {
		priority = streamMetadata.Priority
	}
	
	reservation, err := dpm.reservationManager.ReserveResources(streamID, bufferSize, bufferSize, priority)
	if err != nil {
		return fmt.Errorf("failed to reserve resources: %w", err)
	}
	
	// Ensure we release the reservation if something goes wrong
	defer func() {
		if err != nil {
			dpm.reservationManager.ReleaseReservation(reservation.ReservationID)
		}
	}()
	
	// Step 2: Check and allocate resources atomically
	dpm.buffersMutex.Lock()
	defer dpm.buffersMutex.Unlock()

	// Check if stream already exists
	if _, exists := dpm.streamBuffers[streamID]; exists {
		return fmt.Errorf("stream buffer %d already exists", streamID)
	}

	// Try dynamic window expansion if needed
	windowStatus := dpm.windowManager.GetWindowStatus()
	reservedWindow, _ := dpm.reservationManager.GetReservedResources()
	effectiveAvailable := windowStatus.AvailableSize
	
	// Account for reserved resources
	if effectiveAvailable > reservedWindow {
		effectiveAvailable -= reservedWindow
	} else {
		effectiveAvailable = 0
	}
	
	if bufferSize > effectiveAvailable {
		// Try to trigger dynamic window expansion if enabled
		if dynamicStats := dpm.windowManager.GetDynamicWindowStats(); dynamicStats.Enabled {
			// Force a window adjustment to try to accommodate the new stream
			dpm.windowManager.ForceWindowAdjustment()
			
			// Re-check available space after adjustment
			windowStatus = dpm.windowManager.GetWindowStatus()
			effectiveAvailable = windowStatus.AvailableSize
			if effectiveAvailable > reservedWindow {
				effectiveAvailable -= reservedWindow
			} else {
				effectiveAvailable = 0
			}
		}
		
		if bufferSize > effectiveAvailable {
			// If still no space is available, return an error
			if effectiveAvailable == 0 {
				return fmt.Errorf("no window space available for new stream")
			}
			// Adjust buffer size to fit available window space
			bufferSize = effectiveAvailable
		}
	}

	// Step 3: Allocate window space atomically
	err = dpm.windowManager.AllocateWindowAtomic(streamID, bufferSize)
	if err != nil {
		return fmt.Errorf("failed to allocate window space: %w", err)
	}

	// Step 4: Create stream buffer configuration
	bufferConfig := &StreamBufferConfig{
		StreamID:      streamID,
		BufferSize:    bufferSize,
		Priority:      priority,
		GapTimeout:    dpm.config.GapTimeout,
		EnableMetrics: dpm.config.EnableDetailedMetrics,
	}

	// Configure buffer with logger from DPM if available
	if dpm.logger != nil {
		bufferConfig.EnableDebugLogging = dpm.config.EnableDebugLogging
		bufferConfig.Logger = dpm.logger
	}

	// Step 5: Create the stream buffer
	buffer := NewStreamBuffer(streamID, streamMetadata, bufferConfig)

	// Set backpressure callback
	buffer.SetBackpressureCallback(func(streamID uint64, needed bool) {
		if dpm.logger != nil {
			dpm.logger.Debug("StreamBuffer backpressure event", 
				"stream", streamID, 
				"needed", needed)
		}
		
		if needed {
			dpm.backpressureManager.ActivateBackpressure(streamID, BackpressureReasonBufferFull)
		} else {
			dpm.backpressureManager.DeactivateBackpressure(streamID)
		}
	})

	// Step 6: Add to stream buffers map
	dpm.streamBuffers[streamID] = buffer

	// Step 7: Commit the reservation (resources are now allocated)
	err = dpm.reservationManager.CommitReservation(reservation.ReservationID)
	if err != nil {
		// If commit fails, clean up the allocated resources
		delete(dpm.streamBuffers, streamID)
		dpm.windowManager.ReleaseWindow(streamID, bufferSize)
		return fmt.Errorf("failed to commit resource reservation: %w", err)
	}

	// Update statistics (buffersMutex already held)
	dpm.updateGlobalStatsLocked()

	return nil
}

// CreateStreamBufferWithReservation creates a stream buffer using an existing reservation
func (dpm *DataPresentationManagerImpl) CreateStreamBufferWithReservation(reservationID string) error {
	// This method would be used when a reservation was made separately
	// For now, we'll keep the main CreateStreamBuffer method as the primary interface
	return fmt.Errorf("not implemented: use CreateStreamBuffer instead")
}

// RemoveStreamBuffer removes a stream buffer
func (dpm *DataPresentationManagerImpl) RemoveStreamBuffer(streamID uint64) error {
	dpm.buffersMutex.Lock()
	defer dpm.buffersMutex.Unlock()

	buffer, exists := dpm.streamBuffers[streamID]
	if !exists {
		return nil // Stream doesn't exist, nothing to remove
	}

	// Get the actual allocated size from window manager
	windowStatus := dpm.windowManager.GetWindowStatus()
	allocatedSize, hasAllocation := windowStatus.StreamAllocations[streamID]

	// Close the buffer
	buffer.Close()

	// Remove from map
	delete(dpm.streamBuffers, streamID)

	// Release window space using the actual allocated amount
	if hasAllocation && allocatedSize > 0 {
		err := dpm.windowManager.ReleaseWindow(streamID, allocatedSize)
		if err != nil {
			// Log the error but don't fail the removal
			// This prevents cascading failures
		}
	}

	// Deactivate backpressure for this stream
	dpm.backpressureManager.DeactivateBackpressure(streamID)

	// Note: Don't call garbageCollector.CleanupStreamResources here to avoid circular dependency
	// The GC will clean up resources in its next run

	// Update statistics (buffersMutex already held)
	dpm.updateGlobalStatsLocked()

	return nil
}

// GetStreamBuffer returns a stream buffer by ID
func (dpm *DataPresentationManagerImpl) GetStreamBuffer(streamID uint64) (StreamBuffer, error) {
	dpm.buffersMutex.RLock()
	defer dpm.buffersMutex.RUnlock()

	buffer, exists := dpm.streamBuffers[streamID]
	if !exists {
		return nil, fmt.Errorf("stream buffer %d not found", streamID)
	}

	return buffer, nil
}

// SetStreamTimeout sets the read timeout for a specific stream
func (dpm *DataPresentationManagerImpl) SetStreamTimeout(streamID uint64, timeout time.Duration) error {
	// Check if system is running
	dpm.mutex.RLock()
	running := dpm.running
	dpm.mutex.RUnlock()

	if !running {
		return fmt.Errorf("data presentation manager is not running")
	}

	// Get the stream buffer
	dpm.buffersMutex.RLock()
	buffer, exists := dpm.streamBuffers[streamID]
	dpm.buffersMutex.RUnlock()

	if !exists {
		return fmt.Errorf("stream buffer %d not found", streamID)
	}

	// Update the stream's timeout configuration
	buffer.config.ReadTimeout = timeout

	if dpm.config.EnableDebugLogging && dpm.logger != nil {
		dpm.logger.Debug(fmt.Sprintf("Set stream %d read timeout to %v", streamID, timeout))
	}

	return nil
}

// ReadFromStream reads data from a stream with a timeout
func (dpm *DataPresentationManagerImpl) ReadFromStream(streamID uint64, buffer []byte) (int, error) {
	timeout := dpm.config.ReadTimeout
	startTime := time.Now()

	if dpm.logger != nil {
		dpm.logger.Debug("DPM ReadFromStream - Starting read", 
			"stream", streamID,
			"buffer_size", len(buffer),
		)
	}

	// Check if system is running
	dpm.mutex.RLock()
	running := dpm.running
	dpm.mutex.RUnlock()

	if !running {
		dpm.incrementErrorCount()
		return 0, fmt.Errorf("data presentation manager is not running")
	}

	// Get the stream buffer
	streamBuffer, err := dpm.GetStreamBuffer(streamID)
	if err != nil {
		dpm.incrementErrorCount()
		return 0, err
	}

	// Note: We don't check backpressure for reads - reads should always be allowed
	// to help relieve backpressure by consuming data

	// Wait for readiness (contiguous data available) or timeout
	if sbImpl, ok := streamBuffer.(*StreamBufferImpl); ok {
		if !sbImpl.WaitReady(timeout) {
			// timeout
			dpm.updateReadStats(0, time.Since(startTime))
			return 0, nil
		}
	}

	// Now perform a contiguous read
	n, err := streamBuffer.Read(buffer)

	// Log the read result
	if dpm.logger != nil {
		dpm.logger.Debug("DPM ReadFromStream - Read completed",
			"stream", streamID,
			"bytes_read", n,
			"error", err,
		)
		if n > 0 {
			dpm.logger.Debug("DPM ReadFromStream - Read data",
				"stream", streamID,
				"data_hex", bytesToHex(buffer[:n]),
			)
		}
	}

	// Update statistics
	dpm.updateReadStats(n, time.Since(startTime))

	// Slide window if data was consumed
	if n > 0 {
		dpm.windowManager.SlideWindow(uint64(n))
	}

	if dpm.config.EnableDebugLogging {
		if dpm.logger != nil {
			dpm.logger.Debug(fmt.Sprintf("DPM ReadFromStream stream=%d n=%d err=%v", streamID, n, err))
		}
	}

	return n, err
}

// GetReceiveWindowStatus returns the current receive window status
func (dpm *DataPresentationManagerImpl) GetReceiveWindowStatus() *ReceiveWindowStatus {
	return dpm.windowManager.GetWindowStatus()
}

// SetReceiveWindowSize sets the receive window size
func (dpm *DataPresentationManagerImpl) SetReceiveWindowSize(size uint64) error {
	return dpm.windowManager.SetWindowSize(size)
}

// IsBackpressureActive returns true if backpressure is active for the stream
func (dpm *DataPresentationManagerImpl) IsBackpressureActive(streamID uint64) bool {
	return dpm.backpressureManager.IsBackpressureActive(streamID)
}

// GetBackpressureStatus returns the current backpressure status
func (dpm *DataPresentationManagerImpl) GetBackpressureStatus() *BackpressureStatus {
	return dpm.backpressureManager.GetBackpressureStatus()
}

// GetStreamStats returns statistics for a specific stream
func (dpm *DataPresentationManagerImpl) GetStreamStats(streamID uint64) *StreamStats {
	dpm.buffersMutex.RLock()
	defer dpm.buffersMutex.RUnlock()

	buffer, exists := dpm.streamBuffers[streamID]
	if !exists {
		return nil
	}

	detailedStats := buffer.GetDetailedStats()
	return &detailedStats.StreamStats
}

// GetGlobalStats returns global presentation statistics
func (dpm *DataPresentationManagerImpl) GetGlobalStats() *GlobalPresentationStats {
	dpm.statsMutex.RLock()
	defer dpm.statsMutex.RUnlock()

	return &GlobalPresentationStats{
		ActiveStreams:        dpm.globalStats.ActiveStreams,
		TotalBytesWritten:    dpm.globalStats.TotalBytesWritten,
		TotalBytesRead:       dpm.globalStats.TotalBytesRead,
		WindowUtilization:    dpm.windowManager.GetWindowUtilization(),
		BackpressureEvents:   dpm.globalStats.BackpressureEvents,
		AverageLatency:       dpm.globalStats.AverageLatency,
		LastUpdate:           dpm.globalStats.LastUpdate,
		TotalWriteOperations: dpm.globalStats.TotalWriteOperations,
		TotalReadOperations:  dpm.globalStats.TotalReadOperations,
		ErrorCount:           dpm.globalStats.ErrorCount,
		TimeoutCount:         dpm.globalStats.TimeoutCount,
	}
}

// Start starts the data presentation manager
func (dpm *DataPresentationManagerImpl) Start() error {
	dpm.mutex.Lock()
	defer dpm.mutex.Unlock()

	if dpm.running {
		return fmt.Errorf("data presentation manager is already running")
	}

	dpm.running = true

	// Start background workers
	if dpm.config.CleanupInterval > 0 {
		dpm.workerWg.Add(1)
		go dpm.cleanupWorker()
	}

	if dpm.config.MetricsUpdateInterval > 0 {
		dpm.workerWg.Add(1)
		go dpm.metricsWorker()
	}

	// Start garbage collector
	if dpm.garbageCollector != nil {
		err := dpm.garbageCollector.Start()
		if err != nil {
			return fmt.Errorf("failed to start garbage collector: %w", err)
		}
	}
	
	// Start resource monitor
	if dpm.resourceMonitor != nil {
		err := dpm.resourceMonitor.Start()
		if err != nil {
			return fmt.Errorf("failed to start resource monitor: %w", err)
		}
	}

	return nil
}

// Stop stops the data presentation manager
func (dpm *DataPresentationManagerImpl) Stop() error {
	dpm.mutex.Lock()
	defer dpm.mutex.Unlock()

	if !dpm.running {
		return nil
	}

	dpm.running = false

	// Stop background workers (only if not already stopped)
	select {
	case <-dpm.workerStop:
		// Already stopped
	default:
		close(dpm.workerStop)
	}
	dpm.workerWg.Wait()

	// Recreate the channel for potential restart
	dpm.workerStop = make(chan struct{})

	return nil
}

// Shutdown gracefully shuts down the data presentation manager
func (dpm *DataPresentationManagerImpl) Shutdown() error {
	// Stop the manager
	dpm.Stop()

	// Get list of stream IDs to remove (without holding the lock)
	dpm.buffersMutex.RLock()
	streamIDs := make([]uint64, 0, len(dpm.streamBuffers))
	for streamID := range dpm.streamBuffers {
		streamIDs = append(streamIDs, streamID)
	}
	dpm.buffersMutex.RUnlock()

	// Close all stream buffers (without holding the main lock)
	for _, streamID := range streamIDs {
		dpm.RemoveStreamBuffer(streamID)
	}

	// Stop garbage collector
	if dpm.garbageCollector != nil {
		dpm.garbageCollector.Stop()
	}
	
	// Stop resource monitor
	if dpm.resourceMonitor != nil {
		dpm.resourceMonitor.Stop()
	}

	// Shutdown components
	dpm.windowManager.Shutdown()
	dpm.backpressureManager.Shutdown()
	dpm.reservationManager.Shutdown()

	return nil
}

// Helper methods

// handleBackpressureEvent handles backpressure events from the backpressure manager
func (dpm *DataPresentationManagerImpl) handleBackpressureEvent(streamID uint64, active bool, reason BackpressureReason) {
	dpm.statsMutex.Lock()
	if active {
		dpm.globalStats.BackpressureEvents++
	}
	dpm.statsMutex.Unlock()

	// Log if debug logging is enabled
	if dpm.config.EnableDebugLogging {
		if active {
			if dpm.logger != nil {
				dpm.logger.Debug(fmt.Sprintf("Backpressure activated for stream %d: %s", streamID, reason.String()))
			}
		} else {
			if dpm.logger != nil {
				dpm.logger.Debug(fmt.Sprintf("Backpressure deactivated for stream %d", streamID))
			}
		}
	}
}

// updateGlobalStats updates global statistics
func (dpm *DataPresentationManagerImpl) updateGlobalStats() {
	// Always acquire buffersMutex first to maintain consistent lock ordering
	dpm.buffersMutex.RLock()
	activeStreams := len(dpm.streamBuffers)

	var totalBytesWritten, totalBytesRead uint64
	var totalWriteOps, totalReadOps uint64

	for _, buffer := range dpm.streamBuffers {
		stats := buffer.GetDetailedStats()
		totalBytesWritten += stats.BytesWritten
		totalBytesRead += stats.BytesRead
		totalWriteOps += stats.WriteOperations
		totalReadOps += stats.ReadOperations
	}
	dpm.buffersMutex.RUnlock()

	// Now acquire statsMutex after releasing buffersMutex
	dpm.statsMutex.Lock()
	dpm.globalStats.ActiveStreams = activeStreams
	dpm.globalStats.TotalBytesWritten = totalBytesWritten
	dpm.globalStats.TotalBytesRead = totalBytesRead
	dpm.globalStats.TotalWriteOperations = totalWriteOps
	dpm.globalStats.TotalReadOperations = totalReadOps
	dpm.globalStats.WindowUtilization = dpm.windowManager.GetWindowUtilization()
	dpm.globalStats.LastUpdate = time.Now()

	if activeStreams > dpm.globalStats.PeakActiveStreams {
		dpm.globalStats.PeakActiveStreams = activeStreams
	}
	dpm.statsMutex.Unlock()
}

// updateGlobalStatsLocked updates global statistics when buffersMutex is already held
func (dpm *DataPresentationManagerImpl) updateGlobalStatsLocked() {
	dpm.statsMutex.Lock()
	defer dpm.statsMutex.Unlock()

	// buffersMutex is already held by caller
	activeStreams := len(dpm.streamBuffers)

	var totalBytesWritten, totalBytesRead uint64
	var totalWriteOps, totalReadOps uint64

	for _, buffer := range dpm.streamBuffers {
		stats := buffer.GetDetailedStats()
		totalBytesWritten += stats.BytesWritten
		totalBytesRead += stats.BytesRead
		totalWriteOps += stats.WriteOperations
		totalReadOps += stats.ReadOperations
	}

	dpm.globalStats.ActiveStreams = activeStreams
	dpm.globalStats.TotalBytesWritten = totalBytesWritten
	dpm.globalStats.TotalBytesRead = totalBytesRead
	dpm.globalStats.TotalWriteOperations = totalWriteOps
	dpm.globalStats.TotalReadOperations = totalReadOps
	dpm.globalStats.WindowUtilization = dpm.windowManager.GetWindowUtilization()
	dpm.globalStats.LastUpdate = time.Now()

	if activeStreams > dpm.globalStats.PeakActiveStreams {
		dpm.globalStats.PeakActiveStreams = activeStreams
	}
}

// updateReadStats updates read-related statistics
func (dpm *DataPresentationManagerImpl) updateReadStats(bytesRead int, latency time.Duration) {
	dpm.statsMutex.Lock()
	defer dpm.statsMutex.Unlock()

	dpm.globalStats.TotalReadOperations++
	dpm.globalStats.TotalBytesRead += uint64(bytesRead)

	// Update average latency (simple moving average)
	if dpm.globalStats.TotalReadOperations == 1 {
		dpm.globalStats.AverageLatency = latency
	} else {
		alpha := 0.1 // Smoothing factor
		dpm.globalStats.AverageLatency = time.Duration(
			alpha*float64(latency) + (1-alpha)*float64(dpm.globalStats.AverageLatency))
	}
}

// updateWriteStats updates write-related statistics
func (dpm *DataPresentationManagerImpl) updateWriteStats(bytesWritten int, latency time.Duration) {
	dpm.statsMutex.Lock()
	defer dpm.statsMutex.Unlock()

	dpm.globalStats.TotalWriteOperations++
	dpm.globalStats.TotalBytesWritten += uint64(bytesWritten)

	// Update average latency (simple moving average)
	if dpm.globalStats.TotalWriteOperations == 1 {
		dpm.globalStats.AverageLatency = latency
	} else {
		alpha := 0.1 // Smoothing factor
		dpm.globalStats.AverageLatency = time.Duration(
			alpha*float64(latency) + (1-alpha)*float64(dpm.globalStats.AverageLatency))
	}
}

// incrementErrorCount increments the error count
func (dpm *DataPresentationManagerImpl) incrementErrorCount() {
	dpm.statsMutex.Lock()
	dpm.globalStats.ErrorCount++
	dpm.statsMutex.Unlock()
}

// incrementTimeoutCount increments the timeout count
func (dpm *DataPresentationManagerImpl) incrementTimeoutCount() {
	dpm.statsMutex.Lock()
	dpm.globalStats.TimeoutCount++
	dpm.statsMutex.Unlock()
}

// cleanupWorker performs periodic cleanup tasks
func (dpm *DataPresentationManagerImpl) cleanupWorker() {
	defer dpm.workerWg.Done()

	ticker := time.NewTicker(dpm.config.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			dpm.performCleanup()
		case <-dpm.workerStop:
			return
		}
	}
}

// metricsWorker updates metrics periodically
func (dpm *DataPresentationManagerImpl) metricsWorker() {
	defer dpm.workerWg.Done()

	ticker := time.NewTicker(dpm.config.MetricsUpdateInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			dpm.updateGlobalStats()
		case <-dpm.workerStop:
			return
		}
	}
}

// performCleanup performs cleanup tasks on all stream buffers
func (dpm *DataPresentationManagerImpl) performCleanup() {
	dpm.buffersMutex.RLock()
	buffers := make([]*StreamBufferImpl, 0, len(dpm.streamBuffers))
	for _, buffer := range dpm.streamBuffers {
		buffers = append(buffers, buffer)
	}
	dpm.buffersMutex.RUnlock()

	// Perform cleanup on each buffer
	for _, buffer := range buffers {
		buffer.PerformMaintenanceCleanup()
	}

	// Update global statistics after cleanup
	dpm.updateGlobalStats()
}

// bytesToHex returns a hex string representation of the data (first 32 bytes if longer)
func bytesToHex(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	hexStr := hex.EncodeToString(data)
	if len(hexStr) > 32 { // Limit to first 16 bytes (32 hex chars)
		hexStr = hexStr[:32] + "..."
	}
	return hexStr
}

// isJSON checks if the given byte slice contains valid JSON data
func isJSON(data []byte) bool {
	if len(data) == 0 {
		return false
	}

	// Quick check for JSON-like start/end characters
	firstChar := data[0]
	lastChar := data[len(data)-1]
	
	// Check for JSON object or array
	if (firstChar == '{' && lastChar == '}') || 
	   (firstChar == '[' && lastChar == ']') {
		// Try to unmarshal to validate JSON structure
		var js map[string]interface{}
		return json.Unmarshal(data, &js) == nil
	}
	
	return false
}

// WriteToStream writes data to a specific stream (internal method for aggregators)
func (dpm *DataPresentationManagerImpl) WriteToStream(streamID uint64, data []byte, offset uint64, metadata interface{}) error {
	_ = time.Now() // Will be used in future statistics

	// Validate input data
	if len(data) == 0 {
		err := fmt.Errorf("empty data provided for stream %d at offset %d", streamID, offset)
		if dpm.logger != nil {
			dpm.logger.Error("DPM WriteToStream - Validation failed", 
				"error", err.Error(),
				"stream", streamID,
				"offset", offset)
		}
		dpm.incrementErrorCount()
		return err
	}

	// Log the data being written with more context
	if dpm.logger != nil {
		// Create a sample of the data (first and last 16 bytes if available)
		sampleSize := 16
		sample := ""
		if len(data) <= sampleSize*2 {
			sample = fmt.Sprintf("% x", data)
		} else {
			sample = fmt.Sprintf("% x...% x", 
				data[:sampleSize], 
				data[len(data)-sampleSize:])
		}

		dpm.logger.Debug("DPM WriteToStream - Writing data", 
			"stream", streamID,
			"offset", offset,
			"data_len", len(data),
			"data_sample", sample,
			"is_json", isJSON(data),
		)
	}

	// Check if system is running
	dpm.mutex.RLock()
	running := dpm.running
	dpm.mutex.RUnlock()

	if !running {
		err := fmt.Errorf("data presentation manager is not running")
		if dpm.logger != nil {
			dpm.logger.Error("DPM WriteToStream - Manager not running",
				"stream", streamID,
				"offset", offset,
				"error", err.Error())
		}
		dpm.incrementErrorCount()
		return err
	}

	// Get the stream buffer
	streamBuffer, err := dpm.GetStreamBuffer(streamID)
	if err != nil {
		if dpm.logger != nil {
			dpm.logger.Error("DPM WriteToStream - Failed to get stream buffer",
				"stream", streamID,
				"offset", offset,
				"error", err.Error())
		}
		dpm.incrementErrorCount()
		return fmt.Errorf("failed to get stream buffer: %w", err)
	}

	// Convert metadata to the expected type with validation
	var dataMetadata *DataMetadata
	if metadata != nil {
		if dm, ok := metadata.(*DataMetadata); ok {
			dataMetadata = dm
			// Validate metadata
			if dataMetadata.Length != uint64(len(data)) {
				dpm.logger.Warn("DPM WriteToStream - Metadata length mismatch",
					"stream", streamID,
					"metadata_length", dataMetadata.Length,
					"actual_length", len(data))
			}
		} else {
			// Create default metadata with additional context
			dataMetadata = &DataMetadata{
				Offset:    offset,
				Length:    uint64(len(data)),
				Timestamp: time.Now(),
				Properties: map[string]interface{}{
					"metadata_type": fmt.Sprintf("%T", metadata),
				},
			}
			dpm.logger.Debug("DPM WriteToStream - Created default metadata",
				"stream", streamID,
				"offset", offset,
				"metadata_type", fmt.Sprintf("%T", metadata))
		}
	}

	// Write data to the buffer with metadata if available
	var writeErr error
	writeStart := time.Now()

	if dataMetadata != nil {
		writeErr = streamBuffer.WriteWithMetadata(data, offset, dataMetadata)
	} else {
		writeErr = streamBuffer.Write(data, offset)
	}

	duration := time.Since(writeStart)

	// Log the result of the write operation
	if dpm.logger != nil {
		logLevel := "Debug"
		if writeErr != nil {
			logLevel = "Error"
		}

		logFields := []interface{}{
			"stream", streamID,
			"offset", offset,
			"data_len", len(data),
			"duration", duration.String(),
		}

		if writeErr != nil {
			logFields = append(logFields, "error", writeErr.Error())
		}

		switch logLevel {
		case "Error":
			dpm.logger.Error("DPM WriteToStream - Write failed", logFields...)
		default:
			dpm.logger.Debug("DPM WriteToStream - Write successful", logFields...)
		}
	}

	// Update statistics
	if writeErr == nil {
		dpm.updateWriteStats(len(data), duration)
	} else {
		dpm.incrementErrorCount()
	}

	return writeErr
}

// GetDetailedGlobalStats returns detailed global statistics
func (dpm *DataPresentationManagerImpl) GetDetailedGlobalStats() *GlobalPresentationStatsImpl {
	dpm.statsMutex.RLock()
	defer dpm.statsMutex.RUnlock()

	// Return a copy to avoid race conditions
	return &GlobalPresentationStatsImpl{
		ActiveStreams:        dpm.globalStats.ActiveStreams,
		TotalBytesWritten:    dpm.globalStats.TotalBytesWritten,
		TotalBytesRead:       dpm.globalStats.TotalBytesRead,
		WindowUtilization:    dpm.globalStats.WindowUtilization,
		BackpressureEvents:   dpm.globalStats.BackpressureEvents,
		AverageLatency:       dpm.globalStats.AverageLatency,
		LastUpdate:           dpm.globalStats.LastUpdate,
		TotalWriteOperations: dpm.globalStats.TotalWriteOperations,
		TotalReadOperations:  dpm.globalStats.TotalReadOperations,
		ErrorCount:           dpm.globalStats.ErrorCount,
		TimeoutCount:         dpm.globalStats.TimeoutCount,
		PeakActiveStreams:    dpm.globalStats.PeakActiveStreams,
		AverageStreamLife:    dpm.globalStats.AverageStreamLife,
	}
}

// GetResourceMonitor returns the resource monitor instance
func (dpm *DataPresentationManagerImpl) GetResourceMonitor() *ResourceMonitor {
	return dpm.resourceMonitor
}

// GetGarbageCollector returns the garbage collector instance
func (dpm *DataPresentationManagerImpl) GetGarbageCollector() *ResourceGarbageCollector {
	return dpm.garbageCollector
}

// GetResourceMetrics returns current resource monitoring metrics
func (dpm *DataPresentationManagerImpl) GetResourceMetrics() *ResourceMetrics {
	if dpm.resourceMonitor != nil {
		return dpm.resourceMonitor.GetMetrics()
	}
	return nil
}

// GetResourceAlerts returns current resource monitoring alerts
func (dpm *DataPresentationManagerImpl) GetResourceAlerts() []ResourceAlert {
	if dpm.resourceMonitor != nil {
		return dpm.resourceMonitor.GetAlerts()
	}
	return nil
}

// GetActiveResourceAlerts returns only unresolved resource alerts
func (dpm *DataPresentationManagerImpl) GetActiveResourceAlerts() []ResourceAlert {
	if dpm.resourceMonitor != nil {
		return dpm.resourceMonitor.GetActiveAlerts()
	}
	return nil
}

// SetResourceThresholds updates resource monitoring thresholds
func (dpm *DataPresentationManagerImpl) SetResourceThresholds(thresholds *ResourceThresholds) {
	if dpm.resourceMonitor != nil {
		dpm.resourceMonitor.SetThresholds(thresholds)
	}
}

// ForceResourceCleanup forces an immediate resource cleanup
func (dpm *DataPresentationManagerImpl) ForceResourceCleanup() *GCStats {
	if dpm.garbageCollector != nil {
		return dpm.garbageCollector.ForceGC()
	}
	return nil
}

// SetResourceAlertCallback sets a callback for resource alerts
func (dpm *DataPresentationManagerImpl) SetResourceAlertCallback(callback func(alert ResourceAlert)) {
	if dpm.resourceMonitor != nil {
		dpm.resourceMonitor.SetAlertCallback(callback)
	}
}

// GetResourceConfigManager returns the resource configuration manager
func (dpm *DataPresentationManagerImpl) GetResourceConfigManager() *ResourceConfigManager {
	return dpm.resourceConfigManager
}

// GetResourceManagementConfig returns the current resource management configuration
func (dpm *DataPresentationManagerImpl) GetResourceManagementConfig() *ResourceManagementConfig {
	if dpm.resourceConfigManager != nil {
		return dpm.resourceConfigManager.GetConfig()
	}
	return nil
}

// SetResourceManagementConfig sets the resource management configuration
func (dpm *DataPresentationManagerImpl) SetResourceManagementConfig(config *ResourceManagementConfig) error {
	if dpm.resourceConfigManager != nil {
		return dpm.resourceConfigManager.SetConfig(config)
	}
	return fmt.Errorf("resource configuration manager not available")
}

// ApplyResourceProfile applies a predefined resource profile
func (dpm *DataPresentationManagerImpl) ApplyResourceProfile(profileName string) error {
	if dpm.resourceConfigManager != nil {
		return dpm.resourceConfigManager.ApplyProfile(profileName)
	}
	return fmt.Errorf("resource configuration manager not available")
}

// GetAvailableResourceProfiles returns available resource profiles
func (dpm *DataPresentationManagerImpl) GetAvailableResourceProfiles() []string {
	if dpm.resourceConfigManager != nil {
		return dpm.resourceConfigManager.GetAvailableProfiles()
	}
	return nil
}

// GetResourceProfile returns a specific resource profile
func (dpm *DataPresentationManagerImpl) GetResourceProfile(name string) (*ResourceProfile, error) {
	if dpm.resourceConfigManager != nil {
		return dpm.resourceConfigManager.GetProfile(name)
	}
	return nil, fmt.Errorf("resource configuration manager not available")
}

// AddCustomResourceProfile adds a custom resource profile
func (dpm *DataPresentationManagerImpl) AddCustomResourceProfile(name string, profile *ResourceProfile) error {
	if dpm.resourceConfigManager != nil {
		return dpm.resourceConfigManager.AddCustomProfile(name, profile)
	}
	return fmt.Errorf("resource configuration manager not available")
}

// GetResourcePolicy returns the current resource policy
func (dpm *DataPresentationManagerImpl) GetResourcePolicy() *ResourcePolicy {
	if dpm.resourceConfigManager != nil {
		return dpm.resourceConfigManager.GetPolicy()
	}
	return nil
}

// SetResourcePolicy sets the resource policy
func (dpm *DataPresentationManagerImpl) SetResourcePolicy(policy *ResourcePolicy) {
	if dpm.resourceConfigManager != nil {
		dpm.resourceConfigManager.SetPolicy(policy)
	}
}

// Advanced routing and data writing methods

// WriteToStreamBatch writes multiple data chunks to streams in a batch
func (dpm *DataPresentationManagerImpl) WriteToStreamBatch(writes []StreamWrite) error {
	if len(writes) == 0 {
		return nil
	}

	// Group writes by stream for efficiency
	streamWrites := make(map[uint64][]StreamWrite)
	for _, write := range writes {
		streamWrites[write.StreamID] = append(streamWrites[write.StreamID], write)
	}

	// Process each stream's writes
	var firstError error
	for streamID, writes := range streamWrites {
		for _, write := range writes {
			err := dpm.WriteToStream(streamID, write.Data, write.Offset, write.Metadata)
			if err != nil && firstError == nil {
				firstError = err
			}
		}
	}

	return firstError
}

// RouteDataToStream routes data from aggregators to the appropriate stream
func (dpm *DataPresentationManagerImpl) RouteDataToStream(routingInfo *DataRoutingInfo) error {
	// Validate routing info
	if routingInfo.StreamID == 0 {
		return fmt.Errorf("invalid stream ID: 0")
	}
	if len(routingInfo.Data) == 0 {
		return fmt.Errorf("no data to route")
	}

	// Check if stream exists, create if needed
	_, err := dpm.GetStreamBuffer(routingInfo.StreamID)
	if err != nil {
		// Stream doesn't exist, create it
		metadata := &StreamMetadata{
			StreamID:     routingInfo.StreamID,
			StreamType:   StreamTypeData,
			Priority:     routingInfo.Priority,
			CreatedAt:    time.Now(),
			LastActivity: time.Now(),
		}

		err = dpm.CreateStreamBuffer(routingInfo.StreamID, metadata)
		if err != nil {
			return fmt.Errorf("failed to create stream buffer: %w", err)
		}
	}

	// Create data metadata
	dataMetadata := &DataMetadata{
		Offset:     routingInfo.Offset,
		Length:     uint64(len(routingInfo.Data)),
		Timestamp:  time.Now(),
		SourcePath: routingInfo.SourcePath,
		Flags:      routingInfo.Flags,
	}

	// Write data to stream
	return dpm.WriteToStream(routingInfo.StreamID, routingInfo.Data, routingInfo.Offset, dataMetadata)
}

// RouteDataBatch routes multiple data items to their respective streams
func (dpm *DataPresentationManagerImpl) RouteDataBatch(routingInfos []*DataRoutingInfo) error {
	if len(routingInfos) == 0 {
		return nil
	}

	// Process in parallel if enabled and batch is large enough
	if dpm.config.ParallelWorkers > 1 && len(routingInfos) >= dpm.config.BatchProcessingSize {
		return dpm.routeDataBatchParallel(routingInfos)
	}

	// Sequential processing
	var firstError error
	for _, routingInfo := range routingInfos {
		err := dpm.RouteDataToStream(routingInfo)
		if err != nil && firstError == nil {
			firstError = err
		}
	}

	return firstError
}

// routeDataBatchParallel processes routing in parallel
func (dpm *DataPresentationManagerImpl) routeDataBatchParallel(routingInfos []*DataRoutingInfo) error {
	// Create worker pool
	workers := dpm.config.ParallelWorkers
	workChan := make(chan *DataRoutingInfo, len(routingInfos))
	errorChan := make(chan error, len(routingInfos))

	// Start workers
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for routingInfo := range workChan {
				err := dpm.RouteDataToStream(routingInfo)
				errorChan <- err
			}
		}()
	}

	// Send work
	for _, routingInfo := range routingInfos {
		workChan <- routingInfo
	}
	close(workChan)

	// Wait for completion
	wg.Wait()
	close(errorChan)

	// Collect errors
	var firstError error
	for err := range errorChan {
		if err != nil && firstError == nil {
			firstError = err
		}
	}

	return firstError
}

// GetStreamRoutingInfo returns routing information for a stream
func (dpm *DataPresentationManagerImpl) GetStreamRoutingInfo(streamID uint64) (*StreamRoutingInfo, error) {
	buffer, err := dpm.GetStreamBuffer(streamID)
	if err != nil {
		return nil, err
	}

	metadata := buffer.GetStreamMetadata()
	usage := buffer.GetBufferUsage()

	return &StreamRoutingInfo{
		StreamID:           streamID,
		BufferUtilization:  usage.Utilization,
		Priority:           metadata.Priority,
		BackpressureActive: dpm.IsBackpressureActive(streamID),
		WindowAllocation:   dpm.windowManager.GetWindowStatus().StreamAllocations[streamID],
		LastActivity:       metadata.LastActivity,
	}, nil
}

// calculateAdaptiveBufferSize calculates an adaptive buffer size based on current system conditions
func (dpm *DataPresentationManagerImpl) calculateAdaptiveBufferSize(metadata *StreamMetadata) uint64 {
	// Start with default size
	baseSize := dpm.config.DefaultStreamBufferSize
	
	// Adjust based on stream metadata if available
	if metadata != nil {
		if metadata.MaxBufferSize > 0 {
			baseSize = metadata.MaxBufferSize
			if baseSize > dpm.config.MaxStreamBufferSize {
				baseSize = dpm.config.MaxStreamBufferSize
			}
		}
		
		// Adjust based on stream priority
		switch metadata.Priority {
		case StreamPriorityCritical:
			baseSize = uint64(float64(baseSize) * 1.5) // 50% larger for critical streams
		case StreamPriorityHigh:
			baseSize = uint64(float64(baseSize) * 1.2) // 20% larger for high priority
		case StreamPriorityLow:
			baseSize = uint64(float64(baseSize) * 0.8) // 20% smaller for low priority
		}
	}
	
	// Adjust based on current system load
	dpm.buffersMutex.RLock()
	activeStreams := len(dpm.streamBuffers)
	dpm.buffersMutex.RUnlock()
	
	// If we have many active streams, reduce buffer size to accommodate more streams
	if activeStreams > 50 {
		reductionFactor := 1.0 - (float64(activeStreams-50) * 0.01) // Reduce by 1% per stream over 50
		if reductionFactor < 0.5 {
			reductionFactor = 0.5 // Don't reduce below 50%
		}
		baseSize = uint64(float64(baseSize) * reductionFactor)
	}
	
	// Adjust based on window utilization
	windowStatus := dpm.windowManager.GetWindowStatus()
	if windowStatus.Utilization > 0.8 {
		// High utilization - reduce buffer size
		baseSize = uint64(float64(baseSize) * 0.7)
	} else if windowStatus.Utilization < 0.3 {
		// Low utilization - can afford larger buffers
		baseSize = uint64(float64(baseSize) * 1.3)
	}
	
	// Ensure we stay within bounds
	if baseSize < MinStreamBufferSize {
		baseSize = MinStreamBufferSize
	}
	if baseSize > dpm.config.MaxStreamBufferSize {
		baseSize = dpm.config.MaxStreamBufferSize
	}
	
	return baseSize
}       

// getStreamWindowAllocation returns the window allocation for a stream
func (dpm *DataPresentationManagerImpl) getStreamWindowAllocation(streamID uint64) uint64 {
	status := dpm.windowManager.GetWindowStatus()
	if allocation, exists := status.StreamAllocations[streamID]; exists {
		return allocation
	}
	return 0
}

// OptimizeRouting optimizes routing based on current system state
func (dpm *DataPresentationManagerImpl) OptimizeRouting() error {
	// Get current system state
	windowStatus := dpm.windowManager.GetWindowStatus()
	backpressureStatus := dpm.backpressureManager.GetBackpressureStats()

	// If system is under high pressure, prioritize critical streams
	if windowStatus.Utilization > 0.8 || backpressureStatus.ActiveStreams > 0 {
		return dpm.prioritizeStreams()
	}

	// If system is underutilized, balance load
	if windowStatus.Utilization < 0.3 {
		return dpm.balanceStreamLoad()
	}

	return nil
}

// prioritizeStreams prioritizes critical streams during high load
func (dpm *DataPresentationManagerImpl) prioritizeStreams() error {
	dpm.buffersMutex.RLock()
	defer dpm.buffersMutex.RUnlock()

	// Find critical streams and ensure they have adequate resources
	for streamID, buffer := range dpm.streamBuffers {
		metadata := buffer.GetStreamMetadata()
		if metadata.Priority == StreamPriorityCritical {
			// Ensure critical streams have maximum buffer allocation
			usage := buffer.GetBufferUsage()
			if usage.Utilization > 0.7 {
				// Try to allocate more window space
				additionalSpace := dpm.config.DefaultStreamBufferSize / 4
				dpm.windowManager.AllocateWindow(streamID, additionalSpace)
			}
		}
	}

	return nil
}

// balanceStreamLoad balances load across streams when system is underutilized
func (dpm *DataPresentationManagerImpl) balanceStreamLoad() error {
	// This is a placeholder for load balancing logic
	// In a real implementation, this might redistribute window allocations
	// or adjust buffer sizes based on usage patterns
	return nil
}

// GetRoutingMetrics returns metrics about data routing performance
func (dpm *DataPresentationManagerImpl) GetRoutingMetrics() *RoutingMetrics {
	dpm.statsMutex.RLock()
	defer dpm.statsMutex.RUnlock()

	dpm.buffersMutex.RLock()
	defer dpm.buffersMutex.RUnlock()

	totalStreams := len(dpm.streamBuffers)
	activeStreams := 0
	totalThroughput := uint64(0)

	for _, buffer := range dpm.streamBuffers {
		stats := buffer.GetDetailedStats()
		if stats.LastActivity.After(time.Now().Add(-1 * time.Minute)) {
			activeStreams++
		}
		totalThroughput += stats.BytesWritten
	}

	return &RoutingMetrics{
		TotalStreams:       totalStreams,
		ActiveStreams:      activeStreams,
		TotalThroughput:    totalThroughput,
		AverageLatency:     dpm.globalStats.AverageLatency,
		WindowUtilization:  dpm.windowManager.GetWindowUtilization(),
		BackpressureEvents: dpm.globalStats.BackpressureEvents,
		ErrorRate:          float64(dpm.globalStats.ErrorCount) / float64(dpm.globalStats.TotalWriteOperations),
		LastUpdate:         time.Now(),
	}
}
