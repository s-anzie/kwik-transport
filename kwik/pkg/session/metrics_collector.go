package session

import (
	"fmt"
	"runtime"
	"sync"
	"time"
)

// MetricsCollector collects and aggregates metrics from various session components
type MetricsCollector struct {
	metricsManager *MetricsManager
	
	// Component references for metrics collection
	session            interface{} // ClientSession or ServerSession
	healthMonitor      ConnectionHealthMonitor
	flowControlManager *FlowControlManager
	retransmissionMgr  interface{} // RetransmissionManager
	offsetCoordinator  interface{} // OffsetCoordinator
	
	// Collection state
	running    bool
	stopChan   chan struct{}
	wg         sync.WaitGroup
	
	// Configuration
	config *MetricsCollectorConfig
	
	// Synchronization
	mutex sync.RWMutex
}

// MetricsCollectorConfig configures the metrics collector
type MetricsCollectorConfig struct {
	CollectionInterval    time.Duration
	EnableSystemMetrics   bool
	EnableComponentMetrics bool
	EnablePerformanceMetrics bool
	MetricsBufferSize     int
}

// DefaultMetricsCollectorConfig returns default collector configuration
func DefaultMetricsCollectorConfig() *MetricsCollectorConfig {
	return &MetricsCollectorConfig{
		CollectionInterval:      time.Second,
		EnableSystemMetrics:     true,
		EnableComponentMetrics:  true,
		EnablePerformanceMetrics: true,
		MetricsBufferSize:       1000,
	}
}

// NewMetricsCollector creates a new metrics collector
func NewMetricsCollector(metricsManager *MetricsManager, config *MetricsCollectorConfig) *MetricsCollector {
	if config == nil {
		config = DefaultMetricsCollectorConfig()
	}
	
	return &MetricsCollector{
		metricsManager: metricsManager,
		config:         config,
		stopChan:       make(chan struct{}),
	}
}

// SetSession sets the session reference for metrics collection
func (mc *MetricsCollector) SetSession(session interface{}) {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()
	mc.session = session
}

// SetHealthMonitor sets the health monitor reference
func (mc *MetricsCollector) SetHealthMonitor(healthMonitor ConnectionHealthMonitor) {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()
	mc.healthMonitor = healthMonitor
}

// SetFlowControlManager sets the flow control manager reference
func (mc *MetricsCollector) SetFlowControlManager(flowControlManager *FlowControlManager) {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()
	mc.flowControlManager = flowControlManager
}

// SetRetransmissionManager sets the retransmission manager reference
func (mc *MetricsCollector) SetRetransmissionManager(retransmissionMgr interface{}) {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()
	mc.retransmissionMgr = retransmissionMgr
}

// SetOffsetCoordinator sets the offset coordinator reference
func (mc *MetricsCollector) SetOffsetCoordinator(offsetCoordinator interface{}) {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()
	mc.offsetCoordinator = offsetCoordinator
}

// Start begins metrics collection
func (mc *MetricsCollector) Start() error {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()
	
	if mc.running {
		return nil // Already running
	}
	
	mc.running = true
	
	// Start collection goroutines
	if mc.config.EnableSystemMetrics {
		mc.wg.Add(1)
		go mc.collectSystemMetrics()
	}
	
	if mc.config.EnableComponentMetrics {
		mc.wg.Add(1)
		go mc.collectComponentMetrics()
	}
	
	if mc.config.EnablePerformanceMetrics {
		mc.wg.Add(1)
		go mc.collectPerformanceMetrics()
	}
	
	return nil
}

// Stop stops metrics collection
func (mc *MetricsCollector) Stop() error {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()
	
	if !mc.running {
		return nil // Already stopped
	}
	
	mc.running = false
	close(mc.stopChan)
	mc.wg.Wait()
	
	return nil
}

// collectSystemMetrics collects system-level metrics
func (mc *MetricsCollector) collectSystemMetrics() {
	defer mc.wg.Done()
	
	ticker := time.NewTicker(mc.config.CollectionInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			mc.updateSystemMetrics()
		case <-mc.stopChan:
			return
		}
	}
}

// collectComponentMetrics collects metrics from session components
func (mc *MetricsCollector) collectComponentMetrics() {
	defer mc.wg.Done()
	
	ticker := time.NewTicker(mc.config.CollectionInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			mc.updateComponentMetrics()
		case <-mc.stopChan:
			return
		}
	}
}

// collectPerformanceMetrics collects performance metrics
func (mc *MetricsCollector) collectPerformanceMetrics() {
	defer mc.wg.Done()
	
	ticker := time.NewTicker(mc.config.CollectionInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			mc.updatePerformanceMetrics()
		case <-mc.stopChan:
			return
		}
	}
}

// updateSystemMetrics updates system-level metrics
func (mc *MetricsCollector) updateSystemMetrics() {
	if mc.metricsManager == nil {
		return
	}
	
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	
	perfMetrics := mc.metricsManager.GetPerformanceMetrics()
	perfMetrics.mutex.Lock()
	perfMetrics.MemoryUsage = memStats.Alloc
	perfMetrics.mutex.Unlock()
}

// updateComponentMetrics updates metrics from session components
func (mc *MetricsCollector) updateComponentMetrics() {
	mc.mutex.RLock()
	defer mc.mutex.RUnlock()
	
	// Update health monitor metrics
	if mc.healthMonitor != nil {
		mc.updateHealthMetrics()
	}
	
	// Update flow control metrics
	if mc.flowControlManager != nil {
		mc.updateFlowControlMetrics()
	}
	
	// Update retransmission metrics
	if mc.retransmissionMgr != nil {
		mc.updateRetransmissionMetrics()
	}
	
	// Update offset coordinator metrics
	if mc.offsetCoordinator != nil {
		mc.updateOffsetMetrics()
	}
}

// updateHealthMetrics updates health monitoring metrics
func (mc *MetricsCollector) updateHealthMetrics() {
	if mc.metricsManager == nil {
		return
	}
	
	// Get health status for all paths
	// This would need to be implemented based on the actual health monitor interface
	// For now, we'll use placeholder logic
	
	pathMetrics := mc.metricsManager.GetAllPathMetrics()
	for pathID, metrics := range pathMetrics {
		// Update health score based on recent activity
		timeSinceActivity := time.Since(metrics.LastActivity)
		if timeSinceActivity > 30*time.Second {
			metrics.HealthScore = 0.5 // Degraded
		} else if timeSinceActivity > 60*time.Second {
			metrics.HealthScore = 0.0 // Dead
		} else {
			metrics.HealthScore = 1.0 // Healthy
		}
		
		// Update path status
		if metrics.HealthScore == 0.0 {
			metrics.Status = "dead"
		} else if metrics.HealthScore < 1.0 {
			metrics.Status = "degraded"
		} else {
			metrics.Status = "active"
		}
		
		// Record the updated metrics
		mc.metricsManager.pathMutex.Lock()
		if existingMetrics, exists := mc.metricsManager.pathMetrics[pathID]; exists {
			existingMetrics.mutex.Lock()
			existingMetrics.HealthScore = metrics.HealthScore
			existingMetrics.Status = metrics.Status
			existingMetrics.mutex.Unlock()
		}
		mc.metricsManager.pathMutex.Unlock()
	}
}

// updateFlowControlMetrics updates flow control metrics
func (mc *MetricsCollector) updateFlowControlMetrics() {
	if mc.metricsManager == nil {
		return
	}
	
	// Get flow control status
	// This would need to be implemented based on the actual flow control manager interface
	
	fcMetrics := mc.metricsManager.GetFlowControlMetrics()
	
	// Update buffer utilization (placeholder logic)
	fcMetrics.mutex.Lock()
	// This would be calculated based on actual buffer usage
	fcMetrics.BufferUtilization = 0.5 // 50% utilization as example
	fcMetrics.mutex.Unlock()
}

// updateRetransmissionMetrics updates retransmission metrics
func (mc *MetricsCollector) updateRetransmissionMetrics() {
	// Get retransmission statistics
	// This would need to be implemented based on the actual retransmission manager interface
	
	// For now, we'll update error metrics as a placeholder
	// In a real implementation, this would get actual retransmission stats
}

// updateOffsetMetrics updates offset coordinator metrics
func (mc *MetricsCollector) updateOffsetMetrics() {
	if mc.metricsManager == nil {
		return
	}
	
	// Get offset coordination statistics
	// This would need to be implemented based on the actual offset coordinator interface
	
	// Update stream offset metrics
	streamMetrics := mc.metricsManager.streamMetrics
	mc.metricsManager.streamMutex.RLock()
	for streamID, metrics := range streamMetrics {
		metrics.mutex.Lock()
		// Update current offset (placeholder logic)
		// In a real implementation, this would get actual offset from coordinator
		metrics.CurrentOffset += 1024 // Example increment
		metrics.mutex.Unlock()
		
		// Avoid unused variable warning
		_ = streamID
	}
	mc.metricsManager.streamMutex.RUnlock()
}

// updatePerformanceMetrics updates performance metrics
func (mc *MetricsCollector) updatePerformanceMetrics() {
	if mc.metricsManager == nil {
		return
	}
	
	perfMetrics := mc.metricsManager.GetPerformanceMetrics()
	
	perfMetrics.mutex.Lock()
	defer perfMetrics.mutex.Unlock()
	
	// Calculate throughput based on recent data transfer
	connMetrics := mc.metricsManager.GetConnectionMetrics()
	
	// Simple throughput calculation (bytes per second)
	// This is a simplified calculation - in practice, you'd want a sliding window
	if connMetrics.BytesSent > 0 {
		duration := time.Since(connMetrics.StartTime)
		if duration > 0 {
			perfMetrics.TotalThroughput = float64(connMetrics.BytesSent) / duration.Seconds()
		}
	}
	
	// Update active resource counts
	perfMetrics.ActivePaths = uint64(len(mc.metricsManager.pathMetrics))
	perfMetrics.ActiveStreams = uint64(len(mc.metricsManager.streamMetrics))
}

// GetMetricsSummary returns a comprehensive metrics summary
func (mc *MetricsCollector) GetMetricsSummary() *MetricsSummary {
	if mc.metricsManager == nil {
		return &MetricsSummary{
			CollectedAt: time.Now(),
		}
	}
	
	return &MetricsSummary{
		Connection:    mc.metricsManager.GetConnectionMetrics(),
		Paths:         mc.metricsManager.GetAllPathMetrics(),
		FlowControl:   mc.metricsManager.GetFlowControlMetrics(),
		Errors:        mc.metricsManager.GetErrorMetrics(),
		Performance:   mc.metricsManager.GetPerformanceMetrics(),
		CollectedAt:   time.Now(),
	}
}

// MetricsSummary provides a comprehensive view of all metrics
type MetricsSummary struct {
	Connection  *ConnectionMetrics
	Paths       map[string]*PathMetricsData
	FlowControl *FlowControlMetrics
	Errors      *ErrorMetrics
	Performance *PerformanceMetrics
	CollectedAt time.Time
}

// MetricsReporter provides formatted metrics reporting
type MetricsReporter struct {
	collector *MetricsCollector
}

// NewMetricsReporter creates a new metrics reporter
func NewMetricsReporter(collector *MetricsCollector) *MetricsReporter {
	return &MetricsReporter{
		collector: collector,
	}
}

// GenerateReport generates a formatted metrics report
func (mr *MetricsReporter) GenerateReport() string {
	summary := mr.collector.GetMetricsSummary()
	
	report := "KWIK Transport Metrics Report\n"
	report += "============================\n\n"
	
	// Connection metrics
	report += "Connection Metrics:\n"
	report += "- Session ID: " + summary.Connection.SessionID + "\n"
	report += "- Active Connections: " + formatUint64(summary.Connection.ActiveConnections) + "\n"
	report += "- Total Connections: " + formatUint64(summary.Connection.TotalConnections) + "\n"
	report += "- Bytes Sent: " + formatUint64(summary.Connection.BytesSent) + "\n"
	report += "- Bytes Received: " + formatUint64(summary.Connection.BytesReceived) + "\n"
	report += "- Average RTT: " + summary.Connection.AverageRTT.String() + "\n\n"
	
	// Path metrics
	report += "Path Metrics:\n"
	for pathID, pathMetrics := range summary.Paths {
		report += "- Path " + pathID + ":\n"
		report += "  - Status: " + pathMetrics.Status + "\n"
		report += "  - Health Score: " + formatFloat64(pathMetrics.HealthScore) + "\n"
		report += "  - Bytes Sent: " + formatUint64(pathMetrics.BytesSent) + "\n"
		report += "  - RTT: " + pathMetrics.RTT.String() + "\n"
	}
	report += "\n"
	
	// Performance metrics
	report += "Performance Metrics:\n"
	report += "- Active Streams: " + formatUint64(summary.Performance.ActiveStreams) + "\n"
	report += "- Active Paths: " + formatUint64(summary.Performance.ActivePaths) + "\n"
	report += "- Total Throughput: " + formatFloat64(summary.Performance.TotalThroughput) + " bytes/sec\n"
	report += "- Memory Usage: " + formatUint64(summary.Performance.MemoryUsage) + " bytes\n\n"
	
	// Error metrics
	report += "Error Metrics:\n"
	report += "- Total Errors: " + formatUint64(summary.Errors.TotalErrors) + "\n"
	report += "- Recovery Attempts: " + formatUint64(summary.Errors.RecoveryAttempts) + "\n"
	report += "- Successful Recoveries: " + formatUint64(summary.Errors.SuccessfulRecoveries) + "\n\n"
	
	report += "Report generated at: " + summary.CollectedAt.Format(time.RFC3339) + "\n"
	
	return report
}

// Helper functions for formatting
func formatUint64(value uint64) string {
	return fmt.Sprintf("%d", value)
}

func formatFloat64(value float64) string {
	return fmt.Sprintf("%.2f", value)
}

