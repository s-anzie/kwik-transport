package presentation

import (
	"encoding/hex"
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

	dpm := &DataPresentationManagerImpl{
		streamBuffers:       make(map[uint64]*StreamBufferImpl),
		windowManager:       windowManager,
		backpressureManager: backpressureManager,
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

	return dpm
}

// SetLogger sets the logger for DPM
func (dpm *DataPresentationManagerImpl) SetLogger(l stream.StreamLogger) { dpm.logger = l }

// CreateStreamBuffer creates a new stream buffer
func (dpm *DataPresentationManagerImpl) CreateStreamBuffer(streamID uint64, metadata interface{}) error {
	if streamID == 0 {
		return fmt.Errorf("invalid stream ID: 0")
	}

	dpm.buffersMutex.Lock()
	defer dpm.buffersMutex.Unlock()

	// Check if stream already exists
	if _, exists := dpm.streamBuffers[streamID]; exists {
		return fmt.Errorf("stream buffer %d already exists", streamID)
	}

	// Convert metadata to the expected type
	var streamMetadata *StreamMetadata
	if metadata != nil {
		if sm, ok := metadata.(*StreamMetadata); ok {
			streamMetadata = sm
		}
	}

	// Allocate window space for the new stream
	bufferSize := dpm.config.DefaultStreamBufferSize
	if streamMetadata != nil && streamMetadata.MaxBufferSize > 0 {
		bufferSize = streamMetadata.MaxBufferSize
		if bufferSize > dpm.config.MaxStreamBufferSize {
			bufferSize = dpm.config.MaxStreamBufferSize
		}
	}

	// Ensure buffer size doesn't exceed available window space
	windowStatus := dpm.windowManager.GetWindowStatus()
	if bufferSize > windowStatus.AvailableSize {
		// If no space is available, return an error
		if windowStatus.AvailableSize == 0 {
			return fmt.Errorf("no window space available for new stream")
		}
		// Adjust buffer size to fit available window space
		bufferSize = windowStatus.AvailableSize
	}

	err := dpm.windowManager.AllocateWindow(streamID, bufferSize)
	if err != nil {
		return fmt.Errorf("failed to allocate window space: %w", err)
	}

	// Create stream buffer configuration
	bufferConfig := &StreamBufferConfig{
		StreamID:      streamID,
		BufferSize:    bufferSize,
		Priority:      StreamPriorityNormal,
		GapTimeout:    dpm.config.GapTimeout,
		EnableMetrics: dpm.config.EnableDetailedMetrics,
	}

	if streamMetadata != nil {
		bufferConfig.Priority = streamMetadata.Priority
	}

	// Configure buffer with logger from DPM if available
	if dpm.logger != nil {
		bufferConfig.EnableDebugLogging = dpm.config.EnableDebugLogging
		bufferConfig.Logger = dpm.logger
	}

	// Create the stream buffer
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

	dpm.streamBuffers[streamID] = buffer

	// Update statistics (buffersMutex already held)
	dpm.updateGlobalStatsLocked()

	return nil
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

// ReadFromStreamWithTimeout reads data from a stream with a timeout
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

	// Shutdown components
	dpm.windowManager.Shutdown()
	dpm.backpressureManager.Shutdown()

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
	hexStr := hex.EncodeToString(data)
	if len(hexStr) > 64 { // 32 bytes = 64 hex chars
		return hexStr[:64] + "..."
	}
	return hexStr
}

// WriteToStream writes data to a specific stream (internal method for aggregators)
func (dpm *DataPresentationManagerImpl) WriteToStream(streamID uint64, data []byte, offset uint64, metadata interface{}) error {
	startTime := time.Now()

	// Log the data being written
	if dpm.logger != nil {
		dpm.logger.Debug("DPM WriteToStream - Writing data", 
			"stream", streamID,
			"offset", offset,
			"data_len", len(data),
			"data_hex", bytesToHex(data),
		)
	}

	// Check if system is running
	dpm.mutex.RLock()
	running := dpm.running
	dpm.mutex.RUnlock()

	if !running {
		dpm.incrementErrorCount()
		return fmt.Errorf("data presentation manager is not running")
	}

	// Get the stream buffer
	streamBuffer, err := dpm.GetStreamBuffer(streamID)
	if err != nil {
		dpm.incrementErrorCount()
		return err
	}

	// Convert metadata to the expected type
	var dataMetadata *DataMetadata
	if metadata != nil {
		if dm, ok := metadata.(*DataMetadata); ok {
			dataMetadata = dm
		} else {
			// Create default metadata if conversion fails
			dataMetadata = &DataMetadata{
				Offset:    offset,
				Length:    uint64(len(data)),
				Timestamp: time.Now(),
			}
		}
	}

	// Write data to the buffer
	var writeErr error
	if dataMetadata != nil {
		writeErr = streamBuffer.WriteWithMetadata(data, offset, dataMetadata)
	} else {
		writeErr = streamBuffer.Write(data, offset)
	}
	if dpm.config.EnableDebugLogging {
		if dpm.logger != nil {
			dpm.logger.Debug(fmt.Sprintf("DPM WriteToStream stream=%d len=%d offset=%d err=%v", streamID, len(data), offset, writeErr))
		}
	}

	// Update statistics
	if writeErr == nil {
		dpm.updateWriteStats(len(data), time.Since(startTime))
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
		BackpressureActive: dpm.backpressureManager.IsBackpressureActive(streamID),
		WindowAllocation:   dpm.getStreamWindowAllocation(streamID),
		LastActivity:       metadata.LastActivity,
	}, nil
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
