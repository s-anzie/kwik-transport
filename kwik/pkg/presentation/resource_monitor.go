package presentation

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// ResourceMonitor provides comprehensive monitoring and alerting for resource usage
type ResourceMonitor struct {
	// Components to monitor
	dpm           *DataPresentationManagerImpl
	windowManager *ReceiveWindowManagerImpl
	memoryPool    *MemoryPool
	gcManager     *ResourceGarbageCollector
	
	// Configuration
	config *ResourceMonitorConfig
	
	// Monitoring state
	metrics        *ResourceMetrics
	metricsMutex   sync.RWMutex
	alerts         []ResourceAlert
	alertsMutex    sync.RWMutex
	
	// Thresholds and limits
	thresholds     *ResourceThresholds
	thresholdsMutex sync.RWMutex
	
	// Control
	ctx            context.Context
	cancel         context.CancelFunc
	workerWg       sync.WaitGroup
	running        bool
	runningMutex   sync.RWMutex
	
	// Callbacks
	onAlert        func(alert ResourceAlert)
	onThreshold    func(metric string, value float64, threshold float64)
	onResourceLeak func(resource OrphanedResource)
	
	// Statistics
	totalAlerts    uint64
	lastMonitorRun time.Time
}

// ResourceMonitorConfig contains configuration for resource monitoring
type ResourceMonitorConfig struct {
	// Monitoring intervals
	MonitorInterval     time.Duration `json:"monitor_interval"`      // How often to check resources
	AlertInterval       time.Duration `json:"alert_interval"`       // How often to check for alerts
	MetricsInterval     time.Duration `json:"metrics_interval"`     // How often to update metrics
	
	// Alert configuration
	MaxAlerts           int           `json:"max_alerts"`            // Maximum alerts to keep in memory
	AlertRetention      time.Duration `json:"alert_retention"`      // How long to keep alerts
	EnableEmailAlerts   bool          `json:"enable_email_alerts"`  // Enable email notifications
	EnableSlackAlerts   bool          `json:"enable_slack_alerts"`  // Enable Slack notifications
	
	// Monitoring behavior
	EnableLeakDetection bool          `json:"enable_leak_detection"` // Enable memory leak detection
	EnableTrendAnalysis bool          `json:"enable_trend_analysis"` // Enable trend analysis
	EnablePredictive    bool          `json:"enable_predictive"`     // Enable predictive alerts
	
	// Performance
	MaxConcurrentChecks int           `json:"max_concurrent_checks"` // Max concurrent monitoring checks
	CheckTimeout        time.Duration `json:"check_timeout"`         // Timeout for individual checks
}

// DefaultResourceMonitorConfig returns default monitoring configuration
func DefaultResourceMonitorConfig() *ResourceMonitorConfig {
	return &ResourceMonitorConfig{
		MonitorInterval:     10 * time.Second,
		AlertInterval:       30 * time.Second,
		MetricsInterval:     5 * time.Second,
		MaxAlerts:           1000,
		AlertRetention:      24 * time.Hour,
		EnableEmailAlerts:   false,
		EnableSlackAlerts:   false,
		EnableLeakDetection: true,
		EnableTrendAnalysis: true,
		EnablePredictive:    false,
		MaxConcurrentChecks: 10,
		CheckTimeout:        5 * time.Second,
	}
}

// ResourceMetrics contains comprehensive resource usage metrics
type ResourceMetrics struct {
	// Memory metrics
	TotalMemoryUsed     uint64    `json:"total_memory_used"`
	MemoryUtilization   float64   `json:"memory_utilization"`
	PeakMemoryUsage     uint64    `json:"peak_memory_usage"`
	MemoryGrowthRate    float64   `json:"memory_growth_rate"`
	
	// Stream metrics
	ActiveStreams       int       `json:"active_streams"`
	OrphanedStreams     int       `json:"orphaned_streams"`
	StreamCreationRate  float64   `json:"stream_creation_rate"`
	StreamCleanupRate   float64   `json:"stream_cleanup_rate"`
	
	// Window metrics
	WindowUtilization   float64   `json:"window_utilization"`
	WindowFragmentation float64   `json:"window_fragmentation"`
	WindowWaste         uint64    `json:"window_waste"`
	
	// Buffer metrics
	BufferPoolUsage     float64   `json:"buffer_pool_usage"`
	BufferHitRate       float64   `json:"buffer_hit_rate"`
	BufferMissRate      float64   `json:"buffer_miss_rate"`
	
	// GC metrics
	GCFrequency         float64   `json:"gc_frequency"`
	GCEfficiency        float64   `json:"gc_efficiency"`
	ResourcesFreedRate  float64   `json:"resources_freed_rate"`
	
	// Performance metrics
	AverageLatency      time.Duration `json:"average_latency"`
	ThroughputMBps      float64   `json:"throughput_mbps"`
	ErrorRate           float64   `json:"error_rate"`
	
	// Trend data (last 10 measurements)
	MemoryTrend         []float64 `json:"memory_trend"`
	StreamTrend         []int     `json:"stream_trend"`
	LatencyTrend        []time.Duration `json:"latency_trend"`
	
	// Timestamps
	LastUpdate          time.Time `json:"last_update"`
	MeasurementPeriod   time.Duration `json:"measurement_period"`
}

// ResourceThresholds defines thresholds for resource monitoring
type ResourceThresholds struct {
	// Memory thresholds
	MemoryWarning       float64 `json:"memory_warning"`        // 0.7 = 70%
	MemoryCritical      float64 `json:"memory_critical"`       // 0.9 = 90%
	MemoryGrowthWarning float64 `json:"memory_growth_warning"` // MB/s growth rate
	
	// Stream thresholds
	MaxActiveStreams    int     `json:"max_active_streams"`    // Maximum allowed active streams
	OrphanedWarning     int     `json:"orphaned_warning"`      // Number of orphaned streams
	StreamRateWarning   float64 `json:"stream_rate_warning"`   // Streams/second creation rate
	
	// Window thresholds
	WindowWarning       float64 `json:"window_warning"`        // 0.8 = 80%
	WindowCritical      float64 `json:"window_critical"`       // 0.95 = 95%
	FragmentationWarning float64 `json:"fragmentation_warning"` // 0.3 = 30%
	
	// Performance thresholds
	LatencyWarning      time.Duration `json:"latency_warning"`   // Maximum acceptable latency
	ErrorRateWarning    float64 `json:"error_rate_warning"`    // 0.05 = 5%
	ThroughputWarning   float64 `json:"throughput_warning"`    // Minimum MB/s
	
	// GC thresholds
	GCFrequencyWarning  float64 `json:"gc_frequency_warning"`  // GC runs per minute
	GCEfficiencyWarning float64 `json:"gc_efficiency_warning"` // 0.5 = 50%
}

// DefaultResourceThresholds returns default resource thresholds
func DefaultResourceThresholds() *ResourceThresholds {
	return &ResourceThresholds{
		MemoryWarning:       0.7,
		MemoryCritical:      0.9,
		MemoryGrowthWarning: 10.0, // 10 MB/s
		MaxActiveStreams:    1000,
		OrphanedWarning:     50,
		StreamRateWarning:   100.0, // 100 streams/s
		WindowWarning:       0.8,
		WindowCritical:      0.95,
		FragmentationWarning: 0.3,
		LatencyWarning:      100 * time.Millisecond,
		ErrorRateWarning:    0.05,
		ThroughputWarning:   1.0, // 1 MB/s
		GCFrequencyWarning:  10.0, // 10 GC runs per minute
		GCEfficiencyWarning: 0.5,
	}
}

// ResourceAlert represents a resource monitoring alert
type ResourceAlert struct {
	ID          string           `json:"id"`
	Type        AlertType        `json:"type"`
	Severity    AlertSeverity    `json:"severity"`
	Resource    string           `json:"resource"`
	Message     string           `json:"message"`
	Value       float64          `json:"value"`
	Threshold   float64          `json:"threshold"`
	Timestamp   time.Time        `json:"timestamp"`
	Resolved    bool             `json:"resolved"`
	ResolvedAt  *time.Time       `json:"resolved_at,omitempty"`
	Duration    time.Duration    `json:"duration"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// Use AlertType from metrics.go - no need to redefine

// AlertSeverity defines the severity of an alert
type AlertSeverity int

const (
	AlertSeverityInfo AlertSeverity = iota
	AlertSeverityWarning
	AlertSeverityCritical
	AlertSeverityEmergency
)

func (as AlertSeverity) String() string {
	switch as {
	case AlertSeverityInfo:
		return "INFO"
	case AlertSeverityWarning:
		return "WARNING"
	case AlertSeverityCritical:
		return "CRITICAL"
	case AlertSeverityEmergency:
		return "EMERGENCY"
	default:
		return "UNKNOWN"
	}
}

// NewResourceMonitor creates a new resource monitor
func NewResourceMonitor(dpm *DataPresentationManagerImpl, windowManager *ReceiveWindowManagerImpl, 
	memoryPool *MemoryPool, gcManager *ResourceGarbageCollector, config *ResourceMonitorConfig) *ResourceMonitor {
	
	if config == nil {
		config = DefaultResourceMonitorConfig()
	}
	
	ctx, cancel := context.WithCancel(context.Background())
	
	return &ResourceMonitor{
		dpm:           dpm,
		windowManager: windowManager,
		memoryPool:    memoryPool,
		gcManager:     gcManager,
		config:        config,
		metrics:       &ResourceMetrics{
			MemoryTrend:  make([]float64, 0, 10),
			StreamTrend:  make([]int, 0, 10),
			LatencyTrend: make([]time.Duration, 0, 10),
			LastUpdate:   time.Now(),
		},
		alerts:        make([]ResourceAlert, 0),
		thresholds:    DefaultResourceThresholds(),
		ctx:           ctx,
		cancel:        cancel,
		running:       false,
		lastMonitorRun: time.Now(),
	}
}

// Start starts the resource monitor
func (rm *ResourceMonitor) Start() error {
	rm.runningMutex.Lock()
	defer rm.runningMutex.Unlock()
	
	if rm.running {
		return fmt.Errorf("resource monitor is already running")
	}
	
	rm.running = true
	
	// Start monitoring workers
	rm.workerWg.Add(3)
	go rm.metricsWorker()
	go rm.alertWorker()
	go rm.monitorWorker()
	
	return nil
}

// Stop stops the resource monitor
func (rm *ResourceMonitor) Stop() error {
	rm.runningMutex.Lock()
	defer rm.runningMutex.Unlock()
	
	if !rm.running {
		return nil
	}
	
	rm.running = false
	rm.cancel()
	rm.workerWg.Wait()
	
	return nil
}

// GetMetrics returns current resource metrics
func (rm *ResourceMonitor) GetMetrics() *ResourceMetrics {
	rm.metricsMutex.RLock()
	defer rm.metricsMutex.RUnlock()
	
	// Return a deep copy
	metrics := *rm.metrics
	metrics.MemoryTrend = make([]float64, len(rm.metrics.MemoryTrend))
	copy(metrics.MemoryTrend, rm.metrics.MemoryTrend)
	metrics.StreamTrend = make([]int, len(rm.metrics.StreamTrend))
	copy(metrics.StreamTrend, rm.metrics.StreamTrend)
	metrics.LatencyTrend = make([]time.Duration, len(rm.metrics.LatencyTrend))
	copy(metrics.LatencyTrend, rm.metrics.LatencyTrend)
	
	return &metrics
}

// GetAlerts returns current alerts
func (rm *ResourceMonitor) GetAlerts() []ResourceAlert {
	rm.alertsMutex.RLock()
	defer rm.alertsMutex.RUnlock()
	
	// Return a copy
	alerts := make([]ResourceAlert, len(rm.alerts))
	copy(alerts, rm.alerts)
	return alerts
}

// GetActiveAlerts returns only unresolved alerts
func (rm *ResourceMonitor) GetActiveAlerts() []ResourceAlert {
	rm.alertsMutex.RLock()
	defer rm.alertsMutex.RUnlock()
	
	var activeAlerts []ResourceAlert
	for _, alert := range rm.alerts {
		if !alert.Resolved {
			activeAlerts = append(activeAlerts, alert)
		}
	}
	return activeAlerts
}

// SetThresholds updates the monitoring thresholds
func (rm *ResourceMonitor) SetThresholds(thresholds *ResourceThresholds) {
	rm.thresholdsMutex.Lock()
	defer rm.thresholdsMutex.Unlock()
	rm.thresholds = thresholds
}

// GetThresholds returns current thresholds
func (rm *ResourceMonitor) GetThresholds() *ResourceThresholds {
	rm.thresholdsMutex.RLock()
	defer rm.thresholdsMutex.RUnlock()
	
	// Return a copy
	thresholds := *rm.thresholds
	return &thresholds
}

// SetAlertCallback sets the callback for alerts
func (rm *ResourceMonitor) SetAlertCallback(callback func(alert ResourceAlert)) {
	rm.onAlert = callback
}

// SetThresholdCallback sets the callback for threshold violations
func (rm *ResourceMonitor) SetThresholdCallback(callback func(metric string, value float64, threshold float64)) {
	rm.onThreshold = callback
}

// SetResourceLeakCallback sets the callback for resource leaks
func (rm *ResourceMonitor) SetResourceLeakCallback(callback func(resource OrphanedResource)) {
	rm.onResourceLeak = callback
}

// metricsWorker collects and updates resource metrics
func (rm *ResourceMonitor) metricsWorker() {
	defer rm.workerWg.Done()
	
	ticker := time.NewTicker(rm.config.MetricsInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			rm.collectMetrics()
		case <-rm.ctx.Done():
			return
		}
	}
}

// alertWorker processes alerts and notifications
func (rm *ResourceMonitor) alertWorker() {
	defer rm.workerWg.Done()
	
	ticker := time.NewTicker(rm.config.AlertInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			rm.processAlerts()
		case <-rm.ctx.Done():
			return
		}
	}
}

// monitorWorker performs resource monitoring checks
func (rm *ResourceMonitor) monitorWorker() {
	defer rm.workerWg.Done()
	
	ticker := time.NewTicker(rm.config.MonitorInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			rm.performMonitoring()
		case <-rm.ctx.Done():
			return
		}
	}
}

// collectMetrics collects current resource metrics
func (rm *ResourceMonitor) collectMetrics() {
	startTime := time.Now()
	
	// Collect metrics from various components
	globalStats := rm.dpm.GetDetailedGlobalStats()
	windowStatus := rm.windowManager.GetWindowStatus()
	memoryStats := rm.memoryPool.GetStats()
	gcStats := rm.gcManager.GetStats()
	
	// Calculate derived metrics
	memoryUtilization := memoryStats.Utilization // Use existing utilization field
	windowUtilization := float64(windowStatus.UsedSize) / float64(windowStatus.TotalSize)
	
	// Calculate rates (per second)
	elapsed := time.Since(rm.lastMonitorRun).Seconds()
	if elapsed == 0 {
		elapsed = 1 // Avoid division by zero
	}
	
	rm.metricsMutex.Lock()
	defer rm.metricsMutex.Unlock()
	
	// Update basic metrics
	rm.metrics.TotalMemoryUsed = uint64(memoryStats.UsedBlocks) // Use available field
	rm.metrics.MemoryUtilization = memoryUtilization
	rm.metrics.ActiveStreams = globalStats.ActiveStreams
	rm.metrics.OrphanedStreams = gcStats.OrphanedStreams
	rm.metrics.WindowUtilization = windowUtilization
	rm.metrics.BufferPoolUsage = memoryStats.Utilization
	rm.metrics.AverageLatency = globalStats.AverageLatency
	rm.metrics.ErrorRate = float64(globalStats.ErrorCount) / float64(globalStats.TotalReadOperations + globalStats.TotalWriteOperations)
	
	// Update peak values
	currentMemoryUsage := uint64(memoryStats.UsedBlocks)
	if currentMemoryUsage > rm.metrics.PeakMemoryUsage {
		rm.metrics.PeakMemoryUsage = currentMemoryUsage
	}
	
	// Calculate growth rates
	if len(rm.metrics.MemoryTrend) > 0 {
		lastMemory := rm.metrics.MemoryTrend[len(rm.metrics.MemoryTrend)-1]
		rm.metrics.MemoryGrowthRate = (memoryUtilization - lastMemory) / elapsed
	}
	
	// Update trend data (keep last 10 measurements)
	rm.updateTrend(&rm.metrics.MemoryTrend, memoryUtilization)
	rm.updateStreamTrend(&rm.metrics.StreamTrend, globalStats.ActiveStreams)
	rm.updateLatencyTrend(&rm.metrics.LatencyTrend, globalStats.AverageLatency)
	
	// Calculate GC metrics
	if gcStats.TotalGCRuns > 0 {
		rm.metrics.GCFrequency = float64(gcStats.TotalGCRuns) / time.Since(gcStats.LastGCRun).Minutes()
		rm.metrics.GCEfficiency = float64(gcStats.TotalResourcesFreed) / float64(gcStats.TotalGCRuns)
	}
	
	// Calculate throughput
	totalBytes := globalStats.TotalBytesRead + globalStats.TotalBytesWritten
	rm.metrics.ThroughputMBps = float64(totalBytes) / (1024 * 1024) / elapsed
	
	rm.metrics.LastUpdate = time.Now()
	rm.metrics.MeasurementPeriod = time.Since(startTime)
	rm.lastMonitorRun = startTime
}

// updateTrend updates a float64 trend slice
func (rm *ResourceMonitor) updateTrend(trend *[]float64, value float64) {
	*trend = append(*trend, value)
	if len(*trend) > 10 {
		*trend = (*trend)[1:]
	}
}

// updateStreamTrend updates an int trend slice
func (rm *ResourceMonitor) updateStreamTrend(trend *[]int, value int) {
	*trend = append(*trend, value)
	if len(*trend) > 10 {
		*trend = (*trend)[1:]
	}
}

// updateLatencyTrend updates a duration trend slice
func (rm *ResourceMonitor) updateLatencyTrend(trend *[]time.Duration, value time.Duration) {
	*trend = append(*trend, value)
	if len(*trend) > 10 {
		*trend = (*trend)[1:]
	}
}

// processAlerts processes and manages alerts
func (rm *ResourceMonitor) processAlerts() {
	// Clean up old resolved alerts
	rm.cleanupOldAlerts()
	
	// Check for alert resolution
	rm.checkAlertResolution()
}

// performMonitoring performs comprehensive resource monitoring
func (rm *ResourceMonitor) performMonitoring() {
	metrics := rm.GetMetrics()
	thresholds := rm.GetThresholds()
	
	// Check memory thresholds
	rm.checkMemoryThresholds(metrics, thresholds)
	
	// Check stream thresholds
	rm.checkStreamThresholds(metrics, thresholds)
	
	// Check window thresholds
	rm.checkWindowThresholds(metrics, thresholds)
	
	// Check performance thresholds
	rm.checkPerformanceThresholds(metrics, thresholds)
	
	// Check GC thresholds
	rm.checkGCThresholds(metrics, thresholds)
	
	// Check for resource leaks
	if rm.config.EnableLeakDetection {
		rm.checkResourceLeaks()
	}
	
	// Perform trend analysis
	if rm.config.EnableTrendAnalysis {
		rm.performTrendAnalysis(metrics)
	}
}

// checkMemoryThresholds checks memory-related thresholds
func (rm *ResourceMonitor) checkMemoryThresholds(metrics *ResourceMetrics, thresholds *ResourceThresholds) {
	// Memory utilization check
	if metrics.MemoryUtilization > thresholds.MemoryCritical {
		rm.createAlert(AlertTypeMemory, AlertSeverityCritical, "memory_utilization",
			fmt.Sprintf("Memory utilization critical: %.1f%% > %.1f%%", 
				metrics.MemoryUtilization*100, thresholds.MemoryCritical*100),
			metrics.MemoryUtilization, thresholds.MemoryCritical)
	} else if metrics.MemoryUtilization > thresholds.MemoryWarning {
		rm.createAlert(AlertTypeMemory, AlertSeverityWarning, "memory_utilization",
			fmt.Sprintf("Memory utilization warning: %.1f%% > %.1f%%", 
				metrics.MemoryUtilization*100, thresholds.MemoryWarning*100),
			metrics.MemoryUtilization, thresholds.MemoryWarning)
	}
	
	// Memory growth rate check
	if metrics.MemoryGrowthRate > thresholds.MemoryGrowthWarning {
		rm.createAlert(AlertTypeMemory, AlertSeverityWarning, "memory_growth",
			fmt.Sprintf("Memory growth rate warning: %.2f MB/s > %.2f MB/s", 
				metrics.MemoryGrowthRate, thresholds.MemoryGrowthWarning),
			metrics.MemoryGrowthRate, thresholds.MemoryGrowthWarning)
	}
}

// checkStreamThresholds checks stream-related thresholds
func (rm *ResourceMonitor) checkStreamThresholds(metrics *ResourceMetrics, thresholds *ResourceThresholds) {
	// Active streams check
	if metrics.ActiveStreams > thresholds.MaxActiveStreams {
		rm.createAlert(AlertTypeStream, AlertSeverityCritical, "active_streams",
			fmt.Sprintf("Too many active streams: %d > %d", 
				metrics.ActiveStreams, thresholds.MaxActiveStreams),
			float64(metrics.ActiveStreams), float64(thresholds.MaxActiveStreams))
	}
	
	// Orphaned streams check
	if metrics.OrphanedStreams > thresholds.OrphanedWarning {
		rm.createAlert(AlertTypeStream, AlertSeverityWarning, "orphaned_streams",
			fmt.Sprintf("Too many orphaned streams: %d > %d", 
				metrics.OrphanedStreams, thresholds.OrphanedWarning),
			float64(metrics.OrphanedStreams), float64(thresholds.OrphanedWarning))
	}
	
	// Stream creation rate check
	if metrics.StreamCreationRate > thresholds.StreamRateWarning {
		rm.createAlert(AlertTypeStream, AlertSeverityWarning, "stream_creation_rate",
			fmt.Sprintf("High stream creation rate: %.1f/s > %.1f/s", 
				metrics.StreamCreationRate, thresholds.StreamRateWarning),
			metrics.StreamCreationRate, thresholds.StreamRateWarning)
	}
}

// checkWindowThresholds checks window-related thresholds
func (rm *ResourceMonitor) checkWindowThresholds(metrics *ResourceMetrics, thresholds *ResourceThresholds) {
	// Window utilization check
	if metrics.WindowUtilization > thresholds.WindowCritical {
		rm.createAlert(AlertTypeWindow, AlertSeverityCritical, "window_utilization",
			fmt.Sprintf("Window utilization critical: %.1f%% > %.1f%%", 
				metrics.WindowUtilization*100, thresholds.WindowCritical*100),
			metrics.WindowUtilization, thresholds.WindowCritical)
	} else if metrics.WindowUtilization > thresholds.WindowWarning {
		rm.createAlert(AlertTypeWindow, AlertSeverityWarning, "window_utilization",
			fmt.Sprintf("Window utilization warning: %.1f%% > %.1f%%", 
				metrics.WindowUtilization*100, thresholds.WindowWarning*100),
			metrics.WindowUtilization, thresholds.WindowWarning)
	}
	
	// Window fragmentation check
	if metrics.WindowFragmentation > thresholds.FragmentationWarning {
		rm.createAlert(AlertTypeWindow, AlertSeverityWarning, "window_fragmentation",
			fmt.Sprintf("High window fragmentation: %.1f%% > %.1f%%", 
				metrics.WindowFragmentation*100, thresholds.FragmentationWarning*100),
			metrics.WindowFragmentation, thresholds.FragmentationWarning)
	}
}

// checkPerformanceThresholds checks performance-related thresholds
func (rm *ResourceMonitor) checkPerformanceThresholds(metrics *ResourceMetrics, thresholds *ResourceThresholds) {
	// Latency check
	if metrics.AverageLatency > thresholds.LatencyWarning {
		rm.createAlert(AlertTypePerformance, AlertSeverityWarning, "latency",
			fmt.Sprintf("High latency: %v > %v", 
				metrics.AverageLatency, thresholds.LatencyWarning),
			float64(metrics.AverageLatency.Nanoseconds()), float64(thresholds.LatencyWarning.Nanoseconds()))
	}
	
	// Error rate check
	if metrics.ErrorRate > thresholds.ErrorRateWarning {
		rm.createAlert(AlertTypePerformance, AlertSeverityWarning, "error_rate",
			fmt.Sprintf("High error rate: %.2f%% > %.2f%%", 
				metrics.ErrorRate*100, thresholds.ErrorRateWarning*100),
			metrics.ErrorRate, thresholds.ErrorRateWarning)
	}
	
	// Throughput check
	if metrics.ThroughputMBps < thresholds.ThroughputWarning {
		rm.createAlert(AlertTypePerformance, AlertSeverityWarning, "throughput",
			fmt.Sprintf("Low throughput: %.2f MB/s < %.2f MB/s", 
				metrics.ThroughputMBps, thresholds.ThroughputWarning),
			metrics.ThroughputMBps, thresholds.ThroughputWarning)
	}
}

// checkGCThresholds checks garbage collection thresholds
func (rm *ResourceMonitor) checkGCThresholds(metrics *ResourceMetrics, thresholds *ResourceThresholds) {
	// GC frequency check
	if metrics.GCFrequency > thresholds.GCFrequencyWarning {
		rm.createAlert(AlertTypeGC, AlertSeverityWarning, "gc_frequency",
			fmt.Sprintf("High GC frequency: %.1f/min > %.1f/min", 
				metrics.GCFrequency, thresholds.GCFrequencyWarning),
			metrics.GCFrequency, thresholds.GCFrequencyWarning)
	}
	
	// GC efficiency check
	if metrics.GCEfficiency < thresholds.GCEfficiencyWarning {
		rm.createAlert(AlertTypeGC, AlertSeverityWarning, "gc_efficiency",
			fmt.Sprintf("Low GC efficiency: %.1f%% < %.1f%%", 
				metrics.GCEfficiency*100, thresholds.GCEfficiencyWarning*100),
			metrics.GCEfficiency, thresholds.GCEfficiencyWarning)
	}
}

// checkResourceLeaks checks for potential resource leaks
func (rm *ResourceMonitor) checkResourceLeaks() {
	if rm.gcManager == nil {
		return
	}
	
	orphanedResources := rm.gcManager.GetOrphanedResources()
	for _, resource := range orphanedResources {
		if resource.IdleDuration > 10*time.Minute { // Configurable threshold
			rm.createAlert(AlertTypeLeak, AlertSeverityWarning, "resource_leak",
				fmt.Sprintf("Potential resource leak: %s stream %d idle for %v", 
					resource.Type.String(), resource.StreamID, resource.IdleDuration),
				float64(resource.IdleDuration.Minutes()), 10.0)
			
			if rm.onResourceLeak != nil {
				rm.onResourceLeak(resource)
			}
		}
	}
}

// performTrendAnalysis analyzes trends in resource usage
func (rm *ResourceMonitor) performTrendAnalysis(metrics *ResourceMetrics) {
	// Analyze memory trend
	if len(metrics.MemoryTrend) >= 5 {
		trend := rm.calculateTrend(metrics.MemoryTrend)
		if trend > 0.1 { // 10% increase trend
			rm.createAlert(AlertTypeTrend, AlertSeverityInfo, "memory_trend",
				fmt.Sprintf("Increasing memory usage trend detected: %.2f%%/measurement", trend*100),
				trend, 0.1)
		}
	}
	
	// Analyze latency trend
	if len(metrics.LatencyTrend) >= 5 {
		latencyValues := make([]float64, len(metrics.LatencyTrend))
		for i, d := range metrics.LatencyTrend {
			latencyValues[i] = float64(d.Nanoseconds())
		}
		trend := rm.calculateTrend(latencyValues)
		if trend > 0.2 { // 20% increase trend
			rm.createAlert(AlertTypeTrend, AlertSeverityInfo, "latency_trend",
				fmt.Sprintf("Increasing latency trend detected: %.2f%%/measurement", trend*100),
				trend, 0.2)
		}
	}
}

// calculateTrend calculates the trend (slope) of a series of values
func (rm *ResourceMonitor) calculateTrend(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}
	
	n := float64(len(values))
	sumX := n * (n - 1) / 2 // Sum of indices 0, 1, 2, ..., n-1
	sumY := 0.0
	sumXY := 0.0
	sumX2 := (n - 1) * n * (2*n - 1) / 6 // Sum of squares of indices
	
	for i, y := range values {
		sumY += y
		sumXY += float64(i) * y
	}
	
	// Calculate slope using least squares
	slope := (n*sumXY - sumX*sumY) / (n*sumX2 - sumX*sumX)
	
	// Normalize by average value to get percentage change
	avgY := sumY / n
	if avgY != 0 {
		return slope / avgY
	}
	return 0
}

// createAlert creates a new alert
func (rm *ResourceMonitor) createAlert(alertType AlertType, severity AlertSeverity, resource, message string, value, threshold float64) {
	alert := ResourceAlert{
		ID:        fmt.Sprintf("%s_%s_%d", alertType.String(), resource, time.Now().Unix()),
		Type:      alertType,
		Severity:  severity,
		Resource:  resource,
		Message:   message,
		Value:     value,
		Threshold: threshold,
		Timestamp: time.Now(),
		Resolved:  false,
		Metadata:  make(map[string]interface{}),
	}
	
	rm.alertsMutex.Lock()
	// Check if similar alert already exists
	exists := false
	for i, existingAlert := range rm.alerts {
		if existingAlert.Type == alertType && existingAlert.Resource == resource && !existingAlert.Resolved {
			// Update existing alert
			rm.alerts[i].Value = value
			rm.alerts[i].Timestamp = time.Now()
			rm.alerts[i].Message = message
			exists = true
			break
		}
	}
	
	if !exists {
		rm.alerts = append(rm.alerts, alert)
		rm.totalAlerts++
		
		// Limit alert history
		if len(rm.alerts) > rm.config.MaxAlerts {
			rm.alerts = rm.alerts[1:]
		}
	}
	rm.alertsMutex.Unlock()
	
	// Trigger callback
	if !exists && rm.onAlert != nil {
		rm.onAlert(alert)
	}
	
	// Trigger threshold callback
	if rm.onThreshold != nil {
		rm.onThreshold(resource, value, threshold)
	}
}

// checkAlertResolution checks if alerts should be resolved
func (rm *ResourceMonitor) checkAlertResolution() {
	metrics := rm.GetMetrics()
	thresholds := rm.GetThresholds()
	
	rm.alertsMutex.Lock()
	defer rm.alertsMutex.Unlock()
	
	for i := range rm.alerts {
		if rm.alerts[i].Resolved {
			continue
		}
		
		resolved := false
		switch rm.alerts[i].Type {
		case AlertTypeMemory:
			if rm.alerts[i].Resource == "memory_utilization" {
				resolved = metrics.MemoryUtilization < thresholds.MemoryWarning
			}
		case AlertTypeWindow:
			if rm.alerts[i].Resource == "window_utilization" {
				resolved = metrics.WindowUtilization < thresholds.WindowWarning
			}
		case AlertTypePerformance:
			if rm.alerts[i].Resource == "latency" {
				resolved = metrics.AverageLatency < thresholds.LatencyWarning
			}
		}
		
		if resolved {
			now := time.Now()
			rm.alerts[i].Resolved = true
			rm.alerts[i].ResolvedAt = &now
			rm.alerts[i].Duration = now.Sub(rm.alerts[i].Timestamp)
		}
	}
}

// cleanupOldAlerts removes old resolved alerts
func (rm *ResourceMonitor) cleanupOldAlerts() {
	rm.alertsMutex.Lock()
	defer rm.alertsMutex.Unlock()
	
	cutoff := time.Now().Add(-rm.config.AlertRetention)
	var activeAlerts []ResourceAlert
	
	for _, alert := range rm.alerts {
		if !alert.Resolved || alert.Timestamp.After(cutoff) {
			activeAlerts = append(activeAlerts, alert)
		}
	}
	
	rm.alerts = activeAlerts
}

// GetMonitoringStats returns monitoring statistics
func (rm *ResourceMonitor) GetMonitoringStats() *MonitoringStats {
	rm.alertsMutex.RLock()
	activeAlerts := 0
	for _, alert := range rm.alerts {
		if !alert.Resolved {
			activeAlerts++
		}
	}
	rm.alertsMutex.RUnlock()
	
	return &MonitoringStats{
		TotalAlerts:    rm.totalAlerts,
		ActiveAlerts:   uint64(activeAlerts),
		LastMonitorRun: rm.lastMonitorRun,
		IsRunning:      rm.isRunning(),
		Uptime:         time.Since(rm.lastMonitorRun),
	}
}

// isRunning returns whether the monitor is currently running
func (rm *ResourceMonitor) isRunning() bool {
	rm.runningMutex.RLock()
	defer rm.runningMutex.RUnlock()
	return rm.running
}

// MonitoringStats contains statistics about the monitoring system itself
type MonitoringStats struct {
	TotalAlerts    uint64        `json:"total_alerts"`
	ActiveAlerts   uint64        `json:"active_alerts"`
	LastMonitorRun time.Time     `json:"last_monitor_run"`
	IsRunning      bool          `json:"is_running"`
	Uptime         time.Duration `json:"uptime"`
}

// DynamicPoolStats is defined in types.go