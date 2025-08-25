package session

import (
	"sync"
	"time"
)

// MetricsManager provides comprehensive metrics collection for KWIK sessions
type MetricsManager struct {
	sessionID string
	
	// Connection metrics
	connectionMetrics *ConnectionMetrics
	
	// Path metrics
	pathMetrics map[string]*PathMetricsData
	pathMutex   sync.RWMutex
	
	// Stream metrics
	streamMetrics map[uint64]*StreamMetrics
	streamMutex   sync.RWMutex
	
	// Flow control metrics
	flowControlMetrics *FlowControlMetrics
	
	// Error metrics
	errorMetrics *ErrorMetrics
	
	// Performance metrics
	performanceMetrics *PerformanceMetrics
	
	// Synchronization
	mutex sync.RWMutex
	
	// Configuration
	config *MetricsConfig
}

// ConnectionMetrics tracks connection-level metrics
type ConnectionMetrics struct {
	SessionID         string
	StartTime         time.Time
	LastActivity      time.Time
	TotalConnections  uint64
	ActiveConnections uint64
	FailedConnections uint64
	
	// Data transfer
	BytesSent     uint64
	BytesReceived uint64
	PacketsSent   uint64
	PacketsReceived uint64
	
	// Timing
	AverageRTT    time.Duration
	MinRTT        time.Duration
	MaxRTT        time.Duration
	
	mutex sync.RWMutex
}

// PathMetricsData tracks per-path metrics
type PathMetricsData struct {
	PathID        string
	Address       string
	IsPrimary     bool
	Status        string
	CreatedAt     time.Time
	LastActivity  time.Time
	
	// Health metrics
	HealthScore   float64
	PacketLoss    float64
	RTT           time.Duration
	Jitter        time.Duration
	
	// Data transfer
	BytesSent     uint64
	BytesReceived uint64
	PacketsSent   uint64
	PacketsReceived uint64
	
	// Errors
	ErrorCount    uint64
	TimeoutCount  uint64
	
	mutex sync.RWMutex
}

// StreamMetrics tracks per-stream metrics
type StreamMetrics struct {
	StreamID      uint64
	PathID        string
	CreatedAt     time.Time
	LastActivity  time.Time
	Status        string
	
	// Data transfer
	BytesSent     uint64
	BytesReceived uint64
	
	// Offset tracking
	CurrentOffset uint64
	MaxOffset     uint64
	
	// Performance
	Throughput    float64 // bytes per second
	Latency       time.Duration
	
	mutex sync.RWMutex
}

// FlowControlMetrics tracks flow control performance
type FlowControlMetrics struct {
	// Window management
	SendWindow     uint64
	ReceiveWindow  uint64
	WindowUpdates  uint64
	
	// Backpressure
	BackpressureEvents uint64
	BackpressureDuration time.Duration
	
	// Buffer usage
	BufferUtilization float64
	MaxBufferUsage    uint64
	
	mutex sync.RWMutex
}

// ErrorMetrics tracks error statistics
type ErrorMetrics struct {
	TotalErrors       uint64
	ErrorsByType      map[string]uint64
	ErrorsBySeverity  map[string]uint64
	
	// Recovery metrics
	RecoveryAttempts  uint64
	SuccessfulRecoveries uint64
	FailedRecoveries  uint64
	
	// Recent errors
	RecentErrors      []*ErrorEvent
	
	mutex sync.RWMutex
}

// ErrorEvent represents a single error occurrence
type ErrorEvent struct {
	Timestamp time.Time
	Type      string
	Severity  string
	Message   string
	Component string
	PathID    string
	StreamID  uint64
}

// PerformanceMetrics tracks system performance
type PerformanceMetrics struct {
	// CPU and memory
	CPUUsage    float64
	MemoryUsage uint64
	
	// Throughput
	TotalThroughput   float64
	AverageThroughput float64
	PeakThroughput    float64
	
	// Latency
	AverageLatency time.Duration
	MinLatency     time.Duration
	MaxLatency     time.Duration
	P95Latency     time.Duration
	P99Latency     time.Duration
	
	// Resource utilization
	ActiveStreams     uint64
	ActivePaths       uint64
	BufferUtilization float64
	
	mutex sync.RWMutex
}

// MetricsConfig configures metrics collection
type MetricsConfig struct {
	EnableDetailedMetrics bool
	MetricsInterval       time.Duration
	RetentionPeriod       time.Duration
	MaxRecentErrors       int
	EnablePerformanceMetrics bool
}

// DefaultMetricsConfig returns default metrics configuration
func DefaultMetricsConfig() *MetricsConfig {
	return &MetricsConfig{
		EnableDetailedMetrics:    true,
		MetricsInterval:         time.Second,
		RetentionPeriod:         time.Hour,
		MaxRecentErrors:         100,
		EnablePerformanceMetrics: true,
	}
}

// NewMetricsManager creates a new metrics manager
func NewMetricsManager(sessionID string, config *MetricsConfig) *MetricsManager {
	if config == nil {
		config = DefaultMetricsConfig()
	}
	
	return &MetricsManager{
		sessionID:          sessionID,
		connectionMetrics:  NewConnectionMetrics(sessionID),
		pathMetrics:        make(map[string]*PathMetricsData),
		streamMetrics:      make(map[uint64]*StreamMetrics),
		flowControlMetrics: NewFlowControlMetrics(),
		errorMetrics:       NewErrorMetrics(config.MaxRecentErrors),
		performanceMetrics: NewPerformanceMetrics(),
		config:            config,
	}
}

// NewConnectionMetrics creates new connection metrics
func NewConnectionMetrics(sessionID string) *ConnectionMetrics {
	return &ConnectionMetrics{
		SessionID: sessionID,
		StartTime: time.Now(),
		LastActivity: time.Now(),
	}
}

// NewFlowControlMetrics creates new flow control metrics
func NewFlowControlMetrics() *FlowControlMetrics {
	return &FlowControlMetrics{}
}

// NewErrorMetrics creates new error metrics
func NewErrorMetrics(maxRecentErrors int) *ErrorMetrics {
	return &ErrorMetrics{
		ErrorsByType:     make(map[string]uint64),
		ErrorsBySeverity: make(map[string]uint64),
		RecentErrors:     make([]*ErrorEvent, 0, maxRecentErrors),
	}
}

// NewPerformanceMetrics creates new performance metrics
func NewPerformanceMetrics() *PerformanceMetrics {
	return &PerformanceMetrics{}
}

// RecordPathCreated records a new path creation
func (mm *MetricsManager) RecordPathCreated(pathID, address string, isPrimary bool) {
	mm.pathMutex.Lock()
	defer mm.pathMutex.Unlock()
	
	mm.pathMetrics[pathID] = &PathMetricsData{
		PathID:       pathID,
		Address:      address,
		IsPrimary:    isPrimary,
		Status:       "active",
		CreatedAt:    time.Now(),
		LastActivity: time.Now(),
		HealthScore:  1.0,
	}
	
	mm.connectionMetrics.mutex.Lock()
	mm.connectionMetrics.TotalConnections++
	mm.connectionMetrics.ActiveConnections++
	mm.connectionMetrics.mutex.Unlock()
}

// RecordPathRemoved records path removal
func (mm *MetricsManager) RecordPathRemoved(pathID string) {
	mm.pathMutex.Lock()
	defer mm.pathMutex.Unlock()
	
	if _, exists := mm.pathMetrics[pathID]; exists {
		delete(mm.pathMetrics, pathID)
		
		mm.connectionMetrics.mutex.Lock()
		mm.connectionMetrics.ActiveConnections--
		mm.connectionMetrics.mutex.Unlock()
	}
}

// RecordStreamCreated records a new stream creation
func (mm *MetricsManager) RecordStreamCreated(streamID uint64, pathID string) {
	mm.streamMutex.Lock()
	defer mm.streamMutex.Unlock()
	
	mm.streamMetrics[streamID] = &StreamMetrics{
		StreamID:     streamID,
		PathID:       pathID,
		CreatedAt:    time.Now(),
		LastActivity: time.Now(),
		Status:       "active",
	}
	
	mm.performanceMetrics.mutex.Lock()
	mm.performanceMetrics.ActiveStreams++
	mm.performanceMetrics.mutex.Unlock()
}

// RecordStreamClosed records stream closure
func (mm *MetricsManager) RecordStreamClosed(streamID uint64) {
	mm.streamMutex.Lock()
	defer mm.streamMutex.Unlock()
	
	if _, exists := mm.streamMetrics[streamID]; exists {
		delete(mm.streamMetrics, streamID)
		
		mm.performanceMetrics.mutex.Lock()
		mm.performanceMetrics.ActiveStreams--
		mm.performanceMetrics.mutex.Unlock()
	}
}

// RecordDataSent records data transmission
func (mm *MetricsManager) RecordDataSent(pathID string, streamID uint64, bytes uint64) {
	now := time.Now()
	
	// Update connection metrics
	mm.connectionMetrics.mutex.Lock()
	mm.connectionMetrics.BytesSent += bytes
	mm.connectionMetrics.PacketsSent++
	mm.connectionMetrics.LastActivity = now
	mm.connectionMetrics.mutex.Unlock()
	
	// Update path metrics
	mm.pathMutex.RLock()
	if pathMetrics, exists := mm.pathMetrics[pathID]; exists {
		pathMetrics.mutex.Lock()
		pathMetrics.BytesSent += bytes
		pathMetrics.PacketsSent++
		pathMetrics.LastActivity = now
		pathMetrics.mutex.Unlock()
	}
	mm.pathMutex.RUnlock()
	
	// Update stream metrics
	mm.streamMutex.RLock()
	if streamMetrics, exists := mm.streamMetrics[streamID]; exists {
		streamMetrics.mutex.Lock()
		streamMetrics.BytesSent += bytes
		streamMetrics.LastActivity = now
		streamMetrics.mutex.Unlock()
	}
	mm.streamMutex.RUnlock()
}

// RecordDataReceived records data reception
func (mm *MetricsManager) RecordDataReceived(pathID string, streamID uint64, bytes uint64) {
	now := time.Now()
	
	// Update connection metrics
	mm.connectionMetrics.mutex.Lock()
	mm.connectionMetrics.BytesReceived += bytes
	mm.connectionMetrics.PacketsReceived++
	mm.connectionMetrics.LastActivity = now
	mm.connectionMetrics.mutex.Unlock()
	
	// Update path metrics
	mm.pathMutex.RLock()
	if pathMetrics, exists := mm.pathMetrics[pathID]; exists {
		pathMetrics.mutex.Lock()
		pathMetrics.BytesReceived += bytes
		pathMetrics.PacketsReceived++
		pathMetrics.LastActivity = now
		pathMetrics.mutex.Unlock()
	}
	mm.pathMutex.RUnlock()
	
	// Update stream metrics
	mm.streamMutex.RLock()
	if streamMetrics, exists := mm.streamMetrics[streamID]; exists {
		streamMetrics.mutex.Lock()
		streamMetrics.BytesReceived += bytes
		streamMetrics.LastActivity = now
		streamMetrics.mutex.Unlock()
	}
	mm.streamMutex.RUnlock()
}

// RecordRTT records round-trip time measurement
func (mm *MetricsManager) RecordRTT(pathID string, rtt time.Duration) {
	// Update connection metrics
	mm.connectionMetrics.mutex.Lock()
	if mm.connectionMetrics.AverageRTT == 0 {
		mm.connectionMetrics.AverageRTT = rtt
	} else {
		mm.connectionMetrics.AverageRTT = (mm.connectionMetrics.AverageRTT + rtt) / 2
	}
	
	if mm.connectionMetrics.MinRTT == 0 || rtt < mm.connectionMetrics.MinRTT {
		mm.connectionMetrics.MinRTT = rtt
	}
	if rtt > mm.connectionMetrics.MaxRTT {
		mm.connectionMetrics.MaxRTT = rtt
	}
	mm.connectionMetrics.mutex.Unlock()
	
	// Update path metrics
	mm.pathMutex.RLock()
	if pathMetrics, exists := mm.pathMetrics[pathID]; exists {
		pathMetrics.mutex.Lock()
		pathMetrics.RTT = rtt
		pathMetrics.LastActivity = time.Now()
		pathMetrics.mutex.Unlock()
	}
	mm.pathMutex.RUnlock()
}

// RecordError records an error occurrence
func (mm *MetricsManager) RecordError(errorType, severity, message, component, pathID string, streamID uint64) {
	mm.errorMetrics.mutex.Lock()
	defer mm.errorMetrics.mutex.Unlock()
	
	mm.errorMetrics.TotalErrors++
	mm.errorMetrics.ErrorsByType[errorType]++
	mm.errorMetrics.ErrorsBySeverity[severity]++
	
	// Add to recent errors
	errorEvent := &ErrorEvent{
		Timestamp: time.Now(),
		Type:      errorType,
		Severity:  severity,
		Message:   message,
		Component: component,
		PathID:    pathID,
		StreamID:  streamID,
	}
	
	mm.errorMetrics.RecentErrors = append(mm.errorMetrics.RecentErrors, errorEvent)
	
	// Keep only recent errors
	if len(mm.errorMetrics.RecentErrors) > mm.config.MaxRecentErrors {
		mm.errorMetrics.RecentErrors = mm.errorMetrics.RecentErrors[1:]
	}
}

// RecordFlowControlEvent records flow control events
func (mm *MetricsManager) RecordFlowControlEvent(eventType string, windowSize uint64) {
	mm.flowControlMetrics.mutex.Lock()
	defer mm.flowControlMetrics.mutex.Unlock()
	
	switch eventType {
	case "window_update":
		mm.flowControlMetrics.WindowUpdates++
		mm.flowControlMetrics.SendWindow = windowSize
	case "backpressure_start":
		mm.flowControlMetrics.BackpressureEvents++
	}
}

// GetConnectionMetrics returns current connection metrics
func (mm *MetricsManager) GetConnectionMetrics() *ConnectionMetrics {
	mm.connectionMetrics.mutex.RLock()
	defer mm.connectionMetrics.mutex.RUnlock()
	
	// Return a copy to avoid race conditions
	metrics := *mm.connectionMetrics
	return &metrics
}

// GetPathMetrics returns metrics for a specific path
func (mm *MetricsManager) GetPathMetrics(pathID string) *PathMetricsData {
	mm.pathMutex.RLock()
	defer mm.pathMutex.RUnlock()
	
	if metrics, exists := mm.pathMetrics[pathID]; exists {
		metrics.mutex.RLock()
		defer metrics.mutex.RUnlock()
		
		// Return a copy
		pathMetrics := *metrics
		return &pathMetrics
	}
	
	return nil
}

// GetAllPathMetrics returns metrics for all paths
func (mm *MetricsManager) GetAllPathMetrics() map[string]*PathMetricsData {
	mm.pathMutex.RLock()
	defer mm.pathMutex.RUnlock()
	
	result := make(map[string]*PathMetricsData)
	for pathID, metrics := range mm.pathMetrics {
		metrics.mutex.RLock()
		pathMetrics := *metrics
		metrics.mutex.RUnlock()
		result[pathID] = &pathMetrics
	}
	
	return result
}

// GetStreamMetrics returns metrics for a specific stream
func (mm *MetricsManager) GetStreamMetrics(streamID uint64) *StreamMetrics {
	mm.streamMutex.RLock()
	defer mm.streamMutex.RUnlock()
	
	if metrics, exists := mm.streamMetrics[streamID]; exists {
		metrics.mutex.RLock()
		defer metrics.mutex.RUnlock()
		
		// Return a copy
		streamMetrics := *metrics
		return &streamMetrics
	}
	
	return nil
}

// GetErrorMetrics returns current error metrics
func (mm *MetricsManager) GetErrorMetrics() *ErrorMetrics {
	mm.errorMetrics.mutex.RLock()
	defer mm.errorMetrics.mutex.RUnlock()
	
	// Return a copy
	errorMetrics := &ErrorMetrics{
		TotalErrors:          mm.errorMetrics.TotalErrors,
		ErrorsByType:         make(map[string]uint64),
		ErrorsBySeverity:     make(map[string]uint64),
		RecoveryAttempts:     mm.errorMetrics.RecoveryAttempts,
		SuccessfulRecoveries: mm.errorMetrics.SuccessfulRecoveries,
		FailedRecoveries:     mm.errorMetrics.FailedRecoveries,
		RecentErrors:         make([]*ErrorEvent, len(mm.errorMetrics.RecentErrors)),
	}
	
	// Copy maps
	for k, v := range mm.errorMetrics.ErrorsByType {
		errorMetrics.ErrorsByType[k] = v
	}
	for k, v := range mm.errorMetrics.ErrorsBySeverity {
		errorMetrics.ErrorsBySeverity[k] = v
	}
	
	// Copy recent errors
	copy(errorMetrics.RecentErrors, mm.errorMetrics.RecentErrors)
	
	return errorMetrics
}

// GetPerformanceMetrics returns current performance metrics
func (mm *MetricsManager) GetPerformanceMetrics() *PerformanceMetrics {
	mm.performanceMetrics.mutex.RLock()
	defer mm.performanceMetrics.mutex.RUnlock()
	
	// Return a copy
	perfMetrics := *mm.performanceMetrics
	return &perfMetrics
}

// GetFlowControlMetrics returns current flow control metrics
func (mm *MetricsManager) GetFlowControlMetrics() *FlowControlMetrics {
	mm.flowControlMetrics.mutex.RLock()
	defer mm.flowControlMetrics.mutex.RUnlock()
	
	// Return a copy
	fcMetrics := *mm.flowControlMetrics
	return &fcMetrics
}