package presentation

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// MetricsCollector collects and manages presentation layer metrics
type MetricsCollector struct {
	// Configuration
	config              *MetricsConfig
	
	// Metrics storage
	globalMetrics       *GlobalMetrics
	streamMetrics       map[uint64]*StreamMetrics
	performanceMetrics  *PerformanceMetrics
	systemHealthMetrics *SystemHealthMetrics
	
	// Synchronization
	mutex               sync.RWMutex
	
	// Collection state
	started             int32
	ctx                 context.Context
	cancel              context.CancelFunc
	wg                  sync.WaitGroup
	
	// Event channels
	eventChan           chan MetricEvent
	alertChan           chan Alert
	
	// Callbacks
	alertCallback       AlertCallback
	reportCallback      ReportCallback
	
	// History and aggregation
	historyBuffer       *MetricsHistory
	aggregator          *MetricsAggregator
}

// MetricsConfig contains configuration for metrics collection
type MetricsConfig struct {
	// Collection intervals
	CollectionInterval    time.Duration `json:"collection_interval"`
	AggregationInterval   time.Duration `json:"aggregation_interval"`
	ReportInterval        time.Duration `json:"report_interval"`
	
	// Storage configuration
	HistoryRetention      time.Duration `json:"history_retention"`
	MaxHistoryEntries     int           `json:"max_history_entries"`
	
	// Alert configuration
	EnableAlerts          bool          `json:"enable_alerts"`
	AlertThresholds       AlertThresholds `json:"alert_thresholds"`
	
	// Performance monitoring
	EnablePerformanceMetrics bool       `json:"enable_performance_metrics"`
	LatencyBuckets          []time.Duration `json:"latency_buckets"`
	ThroughputWindow        time.Duration `json:"throughput_window"`
	
	// Export configuration
	EnableExport          bool          `json:"enable_export"`
	ExportFormat          ExportFormat  `json:"export_format"`
	ExportPath            string        `json:"export_path"`
}

// GlobalMetrics contains system-wide metrics
type GlobalMetrics struct {
	// Basic counters
	TotalStreams          int64     `json:"total_streams"`
	ActiveStreams         int64     `json:"active_streams"`
	TotalBytesWritten     int64     `json:"total_bytes_written"`
	TotalBytesRead        int64     `json:"total_bytes_read"`
	TotalOperations       int64     `json:"total_operations"`
	
	// Error counters
	TotalErrors           int64     `json:"total_errors"`
	TimeoutErrors         int64     `json:"timeout_errors"`
	BackpressureEvents    int64     `json:"backpressure_events"`
	
	// Performance metrics
	AverageLatency        time.Duration `json:"average_latency"`
	ThroughputBytesPerSec int64     `json:"throughput_bytes_per_sec"`
	OperationsPerSec      int64     `json:"operations_per_sec"`
	
	// Resource utilization
	WindowUtilization     float64   `json:"window_utilization"`
	MemoryUtilization     float64   `json:"memory_utilization"`
	CPUUtilization        float64   `json:"cpu_utilization"`
	
	// Timestamps
	StartTime             time.Time `json:"start_time"`
	LastUpdate            time.Time `json:"last_update"`
	Uptime                time.Duration `json:"uptime"`
}

// StreamMetrics contains per-stream metrics
type StreamMetrics struct {
	StreamID              uint64    `json:"stream_id"`
	
	// Basic counters
	BytesWritten          int64     `json:"bytes_written"`
	BytesRead             int64     `json:"bytes_read"`
	WriteOperations       int64     `json:"write_operations"`
	ReadOperations        int64     `json:"read_operations"`
	
	// Error counters
	WriteErrors           int64     `json:"write_errors"`
	ReadErrors            int64     `json:"read_errors"`
	TimeoutCount          int64     `json:"timeout_count"`
	
	// Buffer metrics
	BufferUtilization     float64   `json:"buffer_utilization"`
	MaxBufferUtilization  float64   `json:"max_buffer_utilization"`
	GapCount              int64     `json:"gap_count"`
	MaxGapSize            uint64    `json:"max_gap_size"`
	
	// Backpressure metrics
	BackpressureActive    bool      `json:"backpressure_active"`
	BackpressureCount     int64     `json:"backpressure_count"`
	BackpressureDuration  time.Duration `json:"backpressure_duration"`
	
	// Performance metrics
	AverageWriteLatency   time.Duration `json:"average_write_latency"`
	AverageReadLatency    time.Duration `json:"average_read_latency"`
	P95WriteLatency       time.Duration `json:"p95_write_latency"`
	P99WriteLatency       time.Duration `json:"p99_write_latency"`
	
	// Timestamps
	CreatedAt             time.Time `json:"created_at"`
	LastActivity          time.Time `json:"last_activity"`
	LastUpdate            time.Time `json:"last_update"`
}

// MetricEvent represents a metrics event
type MetricEvent struct {
	Type        MetricEventType `json:"type"`
	StreamID    uint64         `json:"stream_id,omitempty"`
	Value       interface{}    `json:"value"`
	Timestamp   time.Time      `json:"timestamp"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// MetricEventType defines the type of metric event
type MetricEventType int

const (
	MetricEventStreamCreated MetricEventType = iota
	MetricEventStreamRemoved
	MetricEventBytesWritten
	MetricEventBytesRead
	MetricEventError
	MetricEventTimeout
	MetricEventBackpressureActivated
	MetricEventBackpressureDeactivated
	MetricEventLatencyMeasured
	MetricEventThroughputMeasured
)

// Alert represents a system alert
type Alert struct {
	ID          string        `json:"id"`
	Level       AlertLevel    `json:"level"`
	Type        AlertType     `json:"type"`
	Message     string        `json:"message"`
	StreamID    *uint64       `json:"stream_id,omitempty"`
	Value       interface{}   `json:"value"`
	Threshold   interface{}   `json:"threshold"`
	Timestamp   time.Time     `json:"timestamp"`
	Resolved    bool          `json:"resolved"`
	ResolvedAt  *time.Time    `json:"resolved_at,omitempty"`
}

// AlertLevel defines the severity level of an alert
type AlertLevel int

const (
	AlertLevelInfo AlertLevel = iota
	AlertLevelWarning
	AlertLevelError
	AlertLevelCritical
)

func (al AlertLevel) String() string {
	switch al {
	case AlertLevelInfo:
		return "INFO"
	case AlertLevelWarning:
		return "WARNING"
	case AlertLevelError:
		return "ERROR"
	case AlertLevelCritical:
		return "CRITICAL"
	default:
		return "UNKNOWN"
	}
}

// AlertType defines the type of alert
type AlertType int

const (
	AlertTypeHighLatency AlertType = iota
	AlertTypeLowThroughput
	AlertTypeHighErrorRate
	AlertTypeMemoryPressure
	AlertTypeBackpressureCascade
	AlertTypeSystemOverload
	AlertTypeStreamStalled
)

// AlertThresholds contains thresholds for generating alerts
type AlertThresholds struct {
	MaxLatency           time.Duration `json:"max_latency"`
	MinThroughput        int64         `json:"min_throughput"`
	MaxErrorRate         float64       `json:"max_error_rate"`
	MaxMemoryUtilization float64       `json:"max_memory_utilization"`
	MaxBackpressureRatio float64       `json:"max_backpressure_ratio"`
	MaxCPUUtilization    float64       `json:"max_cpu_utilization"`
}

// AlertCallback is called when an alert is generated
type AlertCallback func(alert Alert)

// ReportCallback is called when a metrics report is generated
type ReportCallback func(report MetricsReport)

// ExportFormat defines the format for metrics export
type ExportFormat int

const (
	ExportFormatJSON ExportFormat = iota
	ExportFormatPrometheus
	ExportFormatInfluxDB
	ExportFormatCSV
)

// MetricsHistory manages historical metrics data
type MetricsHistory struct {
	entries     []MetricsSnapshot
	maxEntries  int
	retention   time.Duration
	mutex       sync.RWMutex
}

// MetricsSnapshot represents a point-in-time snapshot of metrics
type MetricsSnapshot struct {
	Timestamp       time.Time      `json:"timestamp"`
	GlobalMetrics   GlobalMetrics  `json:"global_metrics"`
	StreamMetrics   map[uint64]*StreamMetrics `json:"stream_metrics"`
	SystemHealth    SystemHealthMetrics `json:"system_health"`
}

// MetricsAggregator aggregates metrics over time windows
type MetricsAggregator struct {
	windows     map[time.Duration]*AggregationWindow
	mutex       sync.RWMutex
}

// AggregationWindow represents metrics aggregated over a time window
type AggregationWindow struct {
	Duration    time.Duration `json:"duration"`
	StartTime   time.Time     `json:"start_time"`
	EndTime     time.Time     `json:"end_time"`
	
	// Aggregated values
	TotalBytes      int64         `json:"total_bytes"`
	TotalOps        int64         `json:"total_ops"`
	TotalErrors     int64         `json:"total_errors"`
	AvgLatency      time.Duration `json:"avg_latency"`
	MaxLatency      time.Duration `json:"max_latency"`
	MinLatency      time.Duration `json:"min_latency"`
	Throughput      float64       `json:"throughput"`
	ErrorRate       float64       `json:"error_rate"`
}

// MetricsReport contains a comprehensive metrics report
type MetricsReport struct {
	GeneratedAt     time.Time              `json:"generated_at"`
	ReportPeriod    time.Duration          `json:"report_period"`
	GlobalMetrics   GlobalMetrics          `json:"global_metrics"`
	StreamMetrics   map[uint64]*StreamMetrics `json:"stream_metrics"`
	SystemHealth    SystemHealthMetrics    `json:"system_health"`
	Alerts          []Alert                `json:"alerts"`
	Aggregations    map[string]*AggregationWindow `json:"aggregations"`
	Recommendations []string               `json:"recommendations"`
}

// NewMetricsCollector creates a new metrics collector
func NewMetricsCollector(config *MetricsConfig) *MetricsCollector {
	ctx, cancel := context.WithCancel(context.Background())
	
	mc := &MetricsCollector{
		config:              config,
		globalMetrics:       &GlobalMetrics{StartTime: time.Now()},
		streamMetrics:       make(map[uint64]*StreamMetrics),
		performanceMetrics:  &PerformanceMetrics{},
		systemHealthMetrics: &SystemHealthMetrics{},
		ctx:                 ctx,
		cancel:              cancel,
		eventChan:           make(chan MetricEvent, 1000),
		alertChan:           make(chan Alert, 100),
		historyBuffer:       NewMetricsHistory(config.MaxHistoryEntries, config.HistoryRetention),
		aggregator:          NewMetricsAggregator(),
	}
	
	return mc
}

// Start starts the metrics collector
func (mc *MetricsCollector) Start() error {
	if !atomic.CompareAndSwapInt32(&mc.started, 0, 1) {
		return nil // Already started
	}
	
	// Start collection routine
	mc.wg.Add(1)
	go mc.collectionRoutine()
	
	// Start aggregation routine
	mc.wg.Add(1)
	go mc.aggregationRoutine()
	
	// Start alert processing routine
	mc.wg.Add(1)
	go mc.alertRoutine()
	
	// Start report generation routine
	mc.wg.Add(1)
	go mc.reportRoutine()
	
	return nil
}

// Stop stops the metrics collector
func (mc *MetricsCollector) Stop() error {
	if !atomic.CompareAndSwapInt32(&mc.started, 1, 0) {
		return nil // Already stopped
	}
	
	mc.cancel()
	close(mc.eventChan)
	mc.wg.Wait()
	close(mc.alertChan)
	
	return nil
}

// RecordEvent records a metric event
func (mc *MetricsCollector) RecordEvent(event MetricEvent) {
	if atomic.LoadInt32(&mc.started) == 0 {
		return
	}
	
	event.Timestamp = time.Now()
	
	select {
	case mc.eventChan <- event:
	default:
		// Channel full, drop event (could log this)
	}
}

// GetGlobalMetrics returns current global metrics
func (mc *MetricsCollector) GetGlobalMetrics() GlobalMetrics {
	mc.mutex.RLock()
	defer mc.mutex.RUnlock()
	
	metrics := *mc.globalMetrics
	metrics.Uptime = time.Since(metrics.StartTime)
	metrics.LastUpdate = time.Now()
	
	return metrics
}

// GetStreamMetrics returns metrics for a specific stream
func (mc *MetricsCollector) GetStreamMetrics(streamID uint64) (*StreamMetrics, bool) {
	mc.mutex.RLock()
	defer mc.mutex.RUnlock()
	
	metrics, exists := mc.streamMetrics[streamID]
	if !exists {
		return nil, false
	}
	
	// Return a copy
	metricsCopy := *metrics
	return &metricsCopy, true
}

// GetAllStreamMetrics returns metrics for all streams
func (mc *MetricsCollector) GetAllStreamMetrics() map[uint64]*StreamMetrics {
	mc.mutex.RLock()
	defer mc.mutex.RUnlock()
	
	result := make(map[uint64]*StreamMetrics)
	for id, metrics := range mc.streamMetrics {
		metricsCopy := *metrics
		result[id] = &metricsCopy
	}
	
	return result
}

// SetAlertCallback sets the callback for alerts
func (mc *MetricsCollector) SetAlertCallback(callback AlertCallback) {
	mc.alertCallback = callback
}

// SetReportCallback sets the callback for reports
func (mc *MetricsCollector) SetReportCallback(callback ReportCallback) {
	mc.reportCallback = callback
}

// collectionRoutine collects metrics periodically
func (mc *MetricsCollector) collectionRoutine() {
	defer mc.wg.Done()
	
	ticker := time.NewTicker(mc.config.CollectionInterval)
	defer ticker.Stop()
	
	for {
		select {
		case event := <-mc.eventChan:
			mc.processEvent(event)
			
		case <-ticker.C:
			mc.collectSystemMetrics()
			mc.updateHealthMetrics()
			mc.checkAlertConditions()
			
		case <-mc.ctx.Done():
			return
		}
	}
}

// processEvent processes a single metric event
func (mc *MetricsCollector) processEvent(event MetricEvent) {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()
	
	switch event.Type {
	case MetricEventStreamCreated:
		mc.handleStreamCreated(event)
	case MetricEventStreamRemoved:
		mc.handleStreamRemoved(event)
	case MetricEventBytesWritten:
		mc.handleBytesWritten(event)
	case MetricEventBytesRead:
		mc.handleBytesRead(event)
	case MetricEventError:
		mc.handleError(event)
	case MetricEventTimeout:
		mc.handleTimeout(event)
	case MetricEventBackpressureActivated:
		mc.handleBackpressureActivated(event)
	case MetricEventBackpressureDeactivated:
		mc.handleBackpressureDeactivated(event)
	case MetricEventLatencyMeasured:
		mc.handleLatencyMeasured(event)
	case MetricEventThroughputMeasured:
		mc.handleThroughputMeasured(event)
	}
}

// handleStreamCreated handles stream creation events
func (mc *MetricsCollector) handleStreamCreated(event MetricEvent) {
	streamID := event.StreamID
	
	mc.streamMetrics[streamID] = &StreamMetrics{
		StreamID:  streamID,
		CreatedAt: event.Timestamp,
		LastUpdate: event.Timestamp,
	}
	
	atomic.AddInt64(&mc.globalMetrics.TotalStreams, 1)
	atomic.AddInt64(&mc.globalMetrics.ActiveStreams, 1)
}

// handleStreamRemoved handles stream removal events
func (mc *MetricsCollector) handleStreamRemoved(event MetricEvent) {
	streamID := event.StreamID
	
	delete(mc.streamMetrics, streamID)
	atomic.AddInt64(&mc.globalMetrics.ActiveStreams, -1)
}

// handleBytesWritten handles bytes written events
func (mc *MetricsCollector) handleBytesWritten(event MetricEvent) {
	bytes := event.Value.(int64)
	streamID := event.StreamID
	
	atomic.AddInt64(&mc.globalMetrics.TotalBytesWritten, bytes)
	atomic.AddInt64(&mc.globalMetrics.TotalOperations, 1)
	
	if metrics, exists := mc.streamMetrics[streamID]; exists {
		atomic.AddInt64(&metrics.BytesWritten, bytes)
		atomic.AddInt64(&metrics.WriteOperations, 1)
		metrics.LastActivity = event.Timestamp
		metrics.LastUpdate = event.Timestamp
	}
}

// handleBytesRead handles bytes read events
func (mc *MetricsCollector) handleBytesRead(event MetricEvent) {
	bytes := event.Value.(int64)
	streamID := event.StreamID
	
	atomic.AddInt64(&mc.globalMetrics.TotalBytesRead, bytes)
	atomic.AddInt64(&mc.globalMetrics.TotalOperations, 1)
	
	if metrics, exists := mc.streamMetrics[streamID]; exists {
		atomic.AddInt64(&metrics.BytesRead, bytes)
		atomic.AddInt64(&metrics.ReadOperations, 1)
		metrics.LastActivity = event.Timestamp
		metrics.LastUpdate = event.Timestamp
	}
}

// handleError handles error events
func (mc *MetricsCollector) handleError(event MetricEvent) {
	streamID := event.StreamID
	
	atomic.AddInt64(&mc.globalMetrics.TotalErrors, 1)
	
	if metrics, exists := mc.streamMetrics[streamID]; exists {
		// Determine if it's a read or write error based on metadata
		if errorType, ok := event.Metadata["error_type"]; ok {
			switch errorType {
			case "write":
				atomic.AddInt64(&metrics.WriteErrors, 1)
			case "read":
				atomic.AddInt64(&metrics.ReadErrors, 1)
			}
		}
		metrics.LastUpdate = event.Timestamp
	}
}

// handleTimeout handles timeout events
func (mc *MetricsCollector) handleTimeout(event MetricEvent) {
	streamID := event.StreamID
	
	atomic.AddInt64(&mc.globalMetrics.TimeoutErrors, 1)
	
	if metrics, exists := mc.streamMetrics[streamID]; exists {
		atomic.AddInt64(&metrics.TimeoutCount, 1)
		metrics.LastUpdate = event.Timestamp
	}
}

// handleBackpressureActivated handles backpressure activation events
func (mc *MetricsCollector) handleBackpressureActivated(event MetricEvent) {
	streamID := event.StreamID
	
	atomic.AddInt64(&mc.globalMetrics.BackpressureEvents, 1)
	
	if metrics, exists := mc.streamMetrics[streamID]; exists {
		metrics.BackpressureActive = true
		atomic.AddInt64(&metrics.BackpressureCount, 1)
		metrics.LastUpdate = event.Timestamp
	}
}

// handleBackpressureDeactivated handles backpressure deactivation events
func (mc *MetricsCollector) handleBackpressureDeactivated(event MetricEvent) {
	streamID := event.StreamID
	
	if metrics, exists := mc.streamMetrics[streamID]; exists {
		metrics.BackpressureActive = false
		
		// Update backpressure duration if provided
		if duration, ok := event.Metadata["duration"]; ok {
			if d, ok := duration.(time.Duration); ok {
				metrics.BackpressureDuration += d
			}
		}
		
		metrics.LastUpdate = event.Timestamp
	}
}

// handleLatencyMeasured handles latency measurement events
func (mc *MetricsCollector) handleLatencyMeasured(event MetricEvent) {
	latency := event.Value.(time.Duration)
	streamID := event.StreamID
	
	// Update global average latency (simplified)
	mc.globalMetrics.AverageLatency = (mc.globalMetrics.AverageLatency + latency) / 2
	
	if metrics, exists := mc.streamMetrics[streamID]; exists {
		// Determine if it's read or write latency
		if opType, ok := event.Metadata["operation_type"]; ok {
			switch opType {
			case "write":
				metrics.AverageWriteLatency = (metrics.AverageWriteLatency + latency) / 2
			case "read":
				metrics.AverageReadLatency = (metrics.AverageReadLatency + latency) / 2
			}
		}
		metrics.LastUpdate = event.Timestamp
	}
}

// handleThroughputMeasured handles throughput measurement events
func (mc *MetricsCollector) handleThroughputMeasured(event MetricEvent) {
	throughput := event.Value.(int64)
	
	atomic.StoreInt64(&mc.globalMetrics.ThroughputBytesPerSec, throughput)
}

// collectSystemMetrics collects system-level metrics
func (mc *MetricsCollector) collectSystemMetrics() {
	// This would integrate with actual system monitoring
	// For now, we'll simulate some values
	
	mc.mutex.Lock()
	defer mc.mutex.Unlock()
	
	// Update operations per second
	now := time.Now()
	if !mc.globalMetrics.LastUpdate.IsZero() {
		duration := now.Sub(mc.globalMetrics.LastUpdate)
		if duration > 0 {
			totalOps := atomic.LoadInt64(&mc.globalMetrics.TotalOperations)
			mc.globalMetrics.OperationsPerSec = int64(float64(totalOps) / duration.Seconds())
		}
	}
	
	mc.globalMetrics.LastUpdate = now
}

// updateHealthMetrics updates system health metrics
func (mc *MetricsCollector) updateHealthMetrics() {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()
	
	// Calculate health score based on various factors
	healthScore := 100
	
	// Reduce score based on error rate
	totalOps := atomic.LoadInt64(&mc.globalMetrics.TotalOperations)
	totalErrors := atomic.LoadInt64(&mc.globalMetrics.TotalErrors)
	if totalOps > 0 {
		errorRate := float64(totalErrors) / float64(totalOps)
		if errorRate > 0.1 {
			healthScore -= int(errorRate * 50)
		}
	}
	
	// Reduce score based on backpressure events
	backpressureEvents := atomic.LoadInt64(&mc.globalMetrics.BackpressureEvents)
	if backpressureEvents > 10 {
		healthScore -= int(backpressureEvents / 10)
	}
	
	// Ensure health score is within bounds
	if healthScore < 0 {
		healthScore = 0
	}
	
	mc.systemHealthMetrics.HealthScore = healthScore
	mc.systemHealthMetrics.LastHealthCheck = time.Now()
}

// checkAlertConditions checks for alert conditions
func (mc *MetricsCollector) checkAlertConditions() {
	if !mc.config.EnableAlerts {
		return
	}
	
	mc.mutex.RLock()
	defer mc.mutex.RUnlock()
	
	// Check global latency
	if mc.globalMetrics.AverageLatency > mc.config.AlertThresholds.MaxLatency {
		alert := Alert{
			ID:        fmt.Sprintf("high_latency_%d", time.Now().Unix()),
			Level:     AlertLevelWarning,
			Type:      AlertTypeHighLatency,
			Message:   fmt.Sprintf("High average latency: %v", mc.globalMetrics.AverageLatency),
			Value:     mc.globalMetrics.AverageLatency,
			Threshold: mc.config.AlertThresholds.MaxLatency,
			Timestamp: time.Now(),
		}
		mc.sendAlert(alert)
	}
	
	// Check throughput
	if mc.globalMetrics.ThroughputBytesPerSec < mc.config.AlertThresholds.MinThroughput {
		alert := Alert{
			ID:        fmt.Sprintf("low_throughput_%d", time.Now().Unix()),
			Level:     AlertLevelWarning,
			Type:      AlertTypeLowThroughput,
			Message:   fmt.Sprintf("Low throughput: %d bytes/sec", mc.globalMetrics.ThroughputBytesPerSec),
			Value:     mc.globalMetrics.ThroughputBytesPerSec,
			Threshold: mc.config.AlertThresholds.MinThroughput,
			Timestamp: time.Now(),
		}
		mc.sendAlert(alert)
	}
	
	// Check error rate
	totalOps := atomic.LoadInt64(&mc.globalMetrics.TotalOperations)
	totalErrors := atomic.LoadInt64(&mc.globalMetrics.TotalErrors)
	if totalOps > 0 {
		errorRate := float64(totalErrors) / float64(totalOps)
		if errorRate > mc.config.AlertThresholds.MaxErrorRate {
			alert := Alert{
				ID:        fmt.Sprintf("high_error_rate_%d", time.Now().Unix()),
				Level:     AlertLevelError,
				Type:      AlertTypeHighErrorRate,
				Message:   fmt.Sprintf("High error rate: %.2f%%", errorRate*100),
				Value:     errorRate,
				Threshold: mc.config.AlertThresholds.MaxErrorRate,
				Timestamp: time.Now(),
			}
			mc.sendAlert(alert)
		}
	}
}

// sendAlert sends an alert
func (mc *MetricsCollector) sendAlert(alert Alert) {
	select {
	case mc.alertChan <- alert:
	default:
		// Alert channel full, could log this
	}
}

// aggregationRoutine performs metrics aggregation
func (mc *MetricsCollector) aggregationRoutine() {
	defer mc.wg.Done()
	
	ticker := time.NewTicker(mc.config.AggregationInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			mc.performAggregation()
			
		case <-mc.ctx.Done():
			return
		}
	}
}

// performAggregation performs metrics aggregation
func (mc *MetricsCollector) performAggregation() {
	// Take a snapshot
	snapshot := MetricsSnapshot{
		Timestamp:     time.Now(),
		GlobalMetrics: mc.GetGlobalMetrics(),
		StreamMetrics: mc.GetAllStreamMetrics(),
		SystemHealth:  *mc.systemHealthMetrics,
	}
	
	// Add to history
	mc.historyBuffer.AddSnapshot(snapshot)
	
	// Update aggregations
	mc.aggregator.UpdateAggregations(snapshot)
}

// alertRoutine processes alerts
func (mc *MetricsCollector) alertRoutine() {
	defer mc.wg.Done()
	
	for {
		select {
		case alert := <-mc.alertChan:
			if mc.alertCallback != nil {
				mc.alertCallback(alert)
			}
			
		case <-mc.ctx.Done():
			return
		}
	}
}

// reportRoutine generates periodic reports
func (mc *MetricsCollector) reportRoutine() {
	defer mc.wg.Done()
	
	ticker := time.NewTicker(mc.config.ReportInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			report := mc.generateReport()
			if mc.reportCallback != nil {
				mc.reportCallback(report)
			}
			
		case <-mc.ctx.Done():
			return
		}
	}
}

// generateReport generates a comprehensive metrics report
func (mc *MetricsCollector) generateReport() MetricsReport {
	return MetricsReport{
		GeneratedAt:   time.Now(),
		ReportPeriod:  mc.config.ReportInterval,
		GlobalMetrics: mc.GetGlobalMetrics(),
		StreamMetrics: mc.GetAllStreamMetrics(),
		SystemHealth:  *mc.systemHealthMetrics,
		Alerts:        []Alert{}, // Would collect recent alerts
		Aggregations:  mc.aggregator.GetAggregations(),
		Recommendations: mc.generateRecommendations(),
	}
}

// generateRecommendations generates performance recommendations
func (mc *MetricsCollector) generateRecommendations() []string {
	recommendations := make([]string, 0)
	
	globalMetrics := mc.GetGlobalMetrics()
	
	// Check for high error rate
	totalOps := atomic.LoadInt64(&globalMetrics.TotalOperations)
	totalErrors := atomic.LoadInt64(&globalMetrics.TotalErrors)
	if totalOps > 0 {
		errorRate := float64(totalErrors) / float64(totalOps)
		if errorRate > 0.05 {
			recommendations = append(recommendations, "Consider investigating high error rate and implementing additional error handling")
		}
	}
	
	// Check for high latency
	if globalMetrics.AverageLatency > 100*time.Millisecond {
		recommendations = append(recommendations, "High latency detected - consider optimizing buffer sizes or increasing parallel workers")
	}
	
	// Check for low throughput
	if globalMetrics.ThroughputBytesPerSec < 1024*1024 { // Less than 1MB/s
		recommendations = append(recommendations, "Low throughput detected - consider increasing batch sizes or optimizing data paths")
	}
	
	// Check for high memory utilization
	if globalMetrics.MemoryUtilization > 0.8 {
		recommendations = append(recommendations, "High memory utilization - consider implementing more aggressive cleanup or increasing memory limits")
	}
	
	return recommendations
}

// NewMetricsHistory creates a new metrics history buffer
func NewMetricsHistory(maxEntries int, retention time.Duration) *MetricsHistory {
	return &MetricsHistory{
		entries:    make([]MetricsSnapshot, 0, maxEntries),
		maxEntries: maxEntries,
		retention:  retention,
	}
}

// AddSnapshot adds a snapshot to the history
func (mh *MetricsHistory) AddSnapshot(snapshot MetricsSnapshot) {
	mh.mutex.Lock()
	defer mh.mutex.Unlock()
	
	// Add new snapshot
	if len(mh.entries) < mh.maxEntries {
		mh.entries = append(mh.entries, snapshot)
	} else {
		// Circular buffer
		copy(mh.entries, mh.entries[1:])
		mh.entries[len(mh.entries)-1] = snapshot
	}
	
	// Clean up old entries
	mh.cleanupOldEntries()
}

// cleanupOldEntries removes entries older than the retention period
func (mh *MetricsHistory) cleanupOldEntries() {
	cutoff := time.Now().Add(-mh.retention)
	
	// Find first entry to keep
	keepIndex := 0
	for i, entry := range mh.entries {
		if entry.Timestamp.After(cutoff) {
			keepIndex = i
			break
		}
	}
	
	// Remove old entries
	if keepIndex > 0 {
		mh.entries = mh.entries[keepIndex:]
	}
}

// NewMetricsAggregator creates a new metrics aggregator
func NewMetricsAggregator() *MetricsAggregator {
	return &MetricsAggregator{
		windows: make(map[time.Duration]*AggregationWindow),
	}
}

// UpdateAggregations updates aggregation windows with new snapshot
func (ma *MetricsAggregator) UpdateAggregations(snapshot MetricsSnapshot) {
	ma.mutex.Lock()
	defer ma.mutex.Unlock()
	
	// Define aggregation windows
	windows := []time.Duration{
		time.Minute,
		5 * time.Minute,
		15 * time.Minute,
		time.Hour,
		24 * time.Hour,
	}
	
	for _, duration := range windows {
		if ma.windows[duration] == nil {
			ma.windows[duration] = &AggregationWindow{
				Duration:  duration,
				StartTime: snapshot.Timestamp.Truncate(duration),
			}
		}
		
		window := ma.windows[duration]
		
		// Check if we need to start a new window
		if snapshot.Timestamp.Sub(window.StartTime) >= duration {
			// Start new window
			window.StartTime = snapshot.Timestamp.Truncate(duration)
			window.EndTime = window.StartTime.Add(duration)
			// Reset aggregated values
			window.TotalBytes = 0
			window.TotalOps = 0
			window.TotalErrors = 0
		}
		
		// Update aggregated values
		window.TotalBytes += snapshot.GlobalMetrics.TotalBytesWritten + snapshot.GlobalMetrics.TotalBytesRead
		window.TotalOps += snapshot.GlobalMetrics.TotalOperations
		window.TotalErrors += snapshot.GlobalMetrics.TotalErrors
		
		// Update latency (simplified)
		if window.AvgLatency == 0 {
			window.AvgLatency = snapshot.GlobalMetrics.AverageLatency
		} else {
			window.AvgLatency = (window.AvgLatency + snapshot.GlobalMetrics.AverageLatency) / 2
		}
		
		if snapshot.GlobalMetrics.AverageLatency > window.MaxLatency {
			window.MaxLatency = snapshot.GlobalMetrics.AverageLatency
		}
		
		if window.MinLatency == 0 || snapshot.GlobalMetrics.AverageLatency < window.MinLatency {
			window.MinLatency = snapshot.GlobalMetrics.AverageLatency
		}
		
		// Calculate throughput and error rate
		if window.EndTime.Sub(window.StartTime) > 0 {
			window.Throughput = float64(window.TotalBytes) / window.EndTime.Sub(window.StartTime).Seconds()
		}
		
		if window.TotalOps > 0 {
			window.ErrorRate = float64(window.TotalErrors) / float64(window.TotalOps)
		}
	}
}

// GetAggregations returns all aggregation windows
func (ma *MetricsAggregator) GetAggregations() map[string]*AggregationWindow {
	ma.mutex.RLock()
	defer ma.mutex.RUnlock()
	
	result := make(map[string]*AggregationWindow)
	for duration, window := range ma.windows {
		windowCopy := *window
		result[duration.String()] = &windowCopy
	}
	
	return result
}

// DefaultMetricsConfig returns a default metrics configuration
func DefaultMetricsConfig() *MetricsConfig {
	return &MetricsConfig{
		CollectionInterval:    time.Second,
		AggregationInterval:   10 * time.Second,
		ReportInterval:        time.Minute,
		HistoryRetention:      24 * time.Hour,
		MaxHistoryEntries:     1000,
		EnableAlerts:          true,
		AlertThresholds: AlertThresholds{
			MaxLatency:           100 * time.Millisecond,
			MinThroughput:        1024 * 1024, // 1MB/s
			MaxErrorRate:         0.05,        // 5%
			MaxMemoryUtilization: 0.8,         // 80%
			MaxBackpressureRatio: 0.3,         // 30%
			MaxCPUUtilization:    0.8,         // 80%
		},
		EnablePerformanceMetrics: true,
		LatencyBuckets: []time.Duration{
			time.Millisecond,
			5 * time.Millisecond,
			10 * time.Millisecond,
			50 * time.Millisecond,
			100 * time.Millisecond,
			500 * time.Millisecond,
			time.Second,
		},
		ThroughputWindow: 10 * time.Second,
		EnableExport:     false,
		ExportFormat:     ExportFormatJSON,
		ExportPath:       "/tmp/kwik_metrics.json",
	}
}

// ExportMetrics exports metrics in the specified format
func (mc *MetricsCollector) ExportMetrics(format ExportFormat, path string) error {
	report := mc.generateReport()
	
	switch format {
	case ExportFormatJSON:
		return mc.exportJSON(report, path)
	case ExportFormatPrometheus:
		return mc.exportPrometheus(report, path)
	case ExportFormatInfluxDB:
		return mc.exportInfluxDB(report, path)
	case ExportFormatCSV:
		return mc.exportCSV(report, path)
	default:
		return fmt.Errorf("unsupported export format: %v", format)
	}
}

// exportJSON exports metrics as JSON
func (mc *MetricsCollector) exportJSON(report MetricsReport, path string) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	
	// In a real implementation, you would write to file
	_ = data
	_ = path
	
	return nil
}

// exportPrometheus exports metrics in Prometheus format
func (mc *MetricsCollector) exportPrometheus(report MetricsReport, path string) error {
	// Implementation would generate Prometheus format
	return nil
}

// exportInfluxDB exports metrics in InfluxDB line protocol format
func (mc *MetricsCollector) exportInfluxDB(report MetricsReport, path string) error {
	// Implementation would generate InfluxDB format
	return nil
}

// exportCSV exports metrics as CSV
func (mc *MetricsCollector) exportCSV(report MetricsReport, path string) error {
	// Implementation would generate CSV format
	return nil
}