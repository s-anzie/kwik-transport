package session

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// HeartbeatIntegrationLayer provides integration between the new heartbeat system
// and existing components (HeartbeatManager, ConnectionHealthMonitor, ErrorRecovery)
type HeartbeatIntegrationLayer interface {
	// InitializeIntegration initializes integration with existing components
	InitializeIntegration(
		heartbeatManager *HeartbeatManager,
		healthMonitor ConnectionHealthMonitor,
		errorRecovery ErrorRecoverySystem,
	) error

	// SyncHeartbeatEvent synchronizes heartbeat events with existing systems
	SyncHeartbeatEvent(event *HeartbeatEvent) error

	// UpdateHealthMetrics updates health metrics from heartbeat data
	UpdateHealthMetrics(pathID string, metrics *HeartbeatHealthMetrics) error

	// TriggerHeartbeatFailureRecovery triggers error recovery on heartbeat failures
	TriggerHeartbeatFailureRecovery(pathID string, failure *HeartbeatFailure) error

	// GetIntegrationStats returns integration layer statistics
	GetIntegrationStats() *HeartbeatIntegrationStats

	// Shutdown gracefully shuts down the integration layer
	Shutdown() error
}

// HeartbeatEvent represents a heartbeat-related event for synchronization
type HeartbeatEvent struct {
	Type         HeartbeatEventType `json:"type"`
	PathID       string             `json:"path_id"`
	SessionID    string             `json:"session_id"`
	StreamID     *uint64            `json:"stream_id,omitempty"`
	PlaneType    HeartbeatPlaneType `json:"plane_type"`
	SequenceID   uint64             `json:"sequence_id"`
	Timestamp    time.Time          `json:"timestamp"`
	RTT          *time.Duration     `json:"rtt,omitempty"`
	Success      bool               `json:"success"`
	ErrorCode    string             `json:"error_code,omitempty"`
	ErrorMessage string             `json:"error_message,omitempty"`

	// Additional context
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// HeartbeatEventType defines the type of heartbeat event
type HeartbeatEventType int

const (
	HeartbeatEventSent HeartbeatEventType = iota
	HeartbeatEventReceived
	HeartbeatEventResponseSent
	HeartbeatEventResponseReceived
	HeartbeatEventTimeout
	HeartbeatEventFailure
	HeartbeatEventRecovery
)

// String returns string representation of HeartbeatEventType
func (t HeartbeatEventType) String() string {
	switch t {
	case HeartbeatEventSent:
		return "SENT"
	case HeartbeatEventReceived:
		return "RECEIVED"
	case HeartbeatEventResponseSent:
		return "RESPONSE_SENT"
	case HeartbeatEventResponseReceived:
		return "RESPONSE_RECEIVED"
	case HeartbeatEventTimeout:
		return "TIMEOUT"
	case HeartbeatEventFailure:
		return "FAILURE"
	case HeartbeatEventRecovery:
		return "RECOVERY"
	default:
		return "UNKNOWN"
	}
}

// HeartbeatPlaneType defines whether heartbeat is for control or data plane
type HeartbeatPlaneType int

const (
	HeartbeatPlaneControl HeartbeatPlaneType = iota
	HeartbeatPlaneData
)

// String returns string representation of HeartbeatPlaneType
func (t HeartbeatPlaneType) String() string {
	switch t {
	case HeartbeatPlaneControl:
		return "CONTROL"
	case HeartbeatPlaneData:
		return "DATA"
	default:
		return "UNKNOWN"
	}
}

// HeartbeatHealthMetrics contains health metrics derived from heartbeat data
type HeartbeatHealthMetrics struct {
	PathID             string    `json:"path_id"`
	SessionID          string    `json:"session_id"`
	ControlPlaneHealth float64   `json:"control_plane_health"` // 0.0 to 1.0
	DataPlaneHealth    float64   `json:"data_plane_health"`    // 0.0 to 1.0
	OverallHealth      float64   `json:"overall_health"`       // 0.0 to 1.0
	RTTTrend           RTTTrend  `json:"rtt_trend"`
	FailureRate        float64   `json:"failure_rate"`  // 0.0 to 1.0
	RecoveryRate       float64   `json:"recovery_rate"` // 0.0 to 1.0
	LastUpdate         time.Time `json:"last_update"`

	// Detailed metrics
	ControlHeartbeatStats *ControlHeartbeatMetrics `json:"control_heartbeat_stats,omitempty"`
	DataHeartbeatStats    *DataHeartbeatMetrics    `json:"data_heartbeat_stats,omitempty"`
}

// String returns string representation of RTTTrend
func (t RTTTrend) String() string {
	switch t {
	case RTTTrendStable:
		return "STABLE"
	case RTTTrendImproving:
		return "IMPROVING"
	case RTTTrendDegrading:
		return "DEGRADING"
	default:
		return "UNKNOWN"
	}
}

// ControlHeartbeatMetrics contains control plane heartbeat metrics
type ControlHeartbeatMetrics struct {
	HeartbeatsSent       uint64        `json:"heartbeats_sent"`
	HeartbeatsReceived   uint64        `json:"heartbeats_received"`
	ResponsesReceived    uint64        `json:"responses_received"`
	ConsecutiveFailures  int           `json:"consecutive_failures"`
	AverageRTT           time.Duration `json:"average_rtt"`
	LastHeartbeatSent    time.Time     `json:"last_heartbeat_sent"`
	LastResponseReceived time.Time     `json:"last_response_received"`
	CurrentInterval      time.Duration `json:"current_interval"`
	SuccessRate          float64       `json:"success_rate"`
}

// DataHeartbeatMetrics contains data plane heartbeat metrics
type DataHeartbeatMetrics struct {
	ActiveStreams          int                                `json:"active_streams"`
	StreamMetrics          map[uint64]*StreamHeartbeatMetrics `json:"stream_metrics"`
	TotalHeartbeatsSent    uint64                             `json:"total_heartbeats_sent"`
	TotalResponsesReceived uint64                             `json:"total_responses_received"`
	AverageSuccessRate     float64                            `json:"average_success_rate"`
}

// StreamHeartbeatMetrics contains heartbeat metrics for a specific stream
type StreamHeartbeatMetrics struct {
	StreamID             uint64        `json:"stream_id"`
	HeartbeatsSent       uint64        `json:"heartbeats_sent"`
	ResponsesReceived    uint64        `json:"responses_received"`
	ConsecutiveFailures  int           `json:"consecutive_failures"`
	LastDataActivity     time.Time     `json:"last_data_activity"`
	LastHeartbeatSent    time.Time     `json:"last_heartbeat_sent"`
	LastResponseReceived time.Time     `json:"last_response_received"`
	CurrentInterval      time.Duration `json:"current_interval"`
	StreamActive         bool          `json:"stream_active"`
	SuccessRate          float64       `json:"success_rate"`
}

// HeartbeatFailure represents a heartbeat failure for error recovery
type HeartbeatFailure struct {
	Type         HeartbeatFailureType `json:"type"`
	PathID       string               `json:"path_id"`
	SessionID    string               `json:"session_id"`
	StreamID     *uint64              `json:"stream_id,omitempty"`
	PlaneType    HeartbeatPlaneType   `json:"plane_type"`
	SequenceID   uint64               `json:"sequence_id"`
	FailureCount int                  `json:"failure_count"`
	LastSuccess  time.Time            `json:"last_success"`
	FailureTime  time.Time            `json:"failure_time"`
	ErrorMessage string               `json:"error_message"`
	Recoverable  bool                 `json:"recoverable"`

	// Recovery context
	RecoveryAttempts int                    `json:"recovery_attempts"`
	Metadata         map[string]interface{} `json:"metadata,omitempty"`
}

// HeartbeatFailureType defines the type of heartbeat failure
type HeartbeatFailureType int

const (
	HeartbeatFailureTimeout HeartbeatFailureType = iota
	HeartbeatFailureSendError
	HeartbeatFailureInvalidResponse
	HeartbeatFailureSequenceMismatch
	HeartbeatFailurePathDead
)

// String returns string representation of HeartbeatFailureType
func (t HeartbeatFailureType) String() string {
	switch t {
	case HeartbeatFailureTimeout:
		return "TIMEOUT"
	case HeartbeatFailureSendError:
		return "SEND_ERROR"
	case HeartbeatFailureInvalidResponse:
		return "INVALID_RESPONSE"
	case HeartbeatFailureSequenceMismatch:
		return "SEQUENCE_MISMATCH"
	case HeartbeatFailurePathDead:
		return "PATH_DEAD"
	default:
		return "UNKNOWN"
	}
}

// HeartbeatIntegrationStats contains statistics about the integration layer
type HeartbeatIntegrationStats struct {
	EventsProcessed     uint64                        `json:"events_processed"`
	EventsByType        map[HeartbeatEventType]uint64 `json:"events_by_type"`
	HealthUpdates       uint64                        `json:"health_updates"`
	RecoveryTriggers    uint64                        `json:"recovery_triggers"`
	IntegrationErrors   uint64                        `json:"integration_errors"`
	LastEventProcessed  time.Time                     `json:"last_event_processed"`
	LastHealthUpdate    time.Time                     `json:"last_health_update"`
	LastRecoveryTrigger time.Time                     `json:"last_recovery_trigger"`

	// Component integration status
	HeartbeatManagerIntegrated bool `json:"heartbeat_manager_integrated"`
	HealthMonitorIntegrated    bool `json:"health_monitor_integrated"`
	ErrorRecoveryIntegrated    bool `json:"error_recovery_integrated"`

	// Performance metrics
	AverageEventProcessingTime time.Duration `json:"average_event_processing_time"`
	MaxEventProcessingTime     time.Duration `json:"max_event_processing_time"`
}

// HeartbeatIntegrationLayerImpl implements the HeartbeatIntegrationLayer interface
type HeartbeatIntegrationLayerImpl struct {
	// Component references
	heartbeatManager *HeartbeatManager
	healthMonitor    ConnectionHealthMonitor
	errorRecovery    ErrorRecoverySystem

	// Event processing
	eventQueue   chan *HeartbeatEvent
	eventWorkers int

	// Health metrics tracking
	healthMetrics map[string]*HeartbeatHealthMetrics
	healthMutex   sync.RWMutex

	// Statistics
	stats      *HeartbeatIntegrationStats
	statsMutex sync.RWMutex

	// Control
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Configuration
	config *HeartbeatIntegrationConfig
}

// HeartbeatIntegrationConfig contains configuration for the integration layer
type HeartbeatIntegrationConfig struct {
	EventQueueSize       int           `json:"event_queue_size"`
	EventWorkers         int           `json:"event_workers"`
	HealthUpdateInterval time.Duration `json:"health_update_interval"`
	MetricsRetention     time.Duration `json:"metrics_retention"`
	EnableEventLogging   bool          `json:"enable_event_logging"`
}

// DefaultHeartbeatIntegrationConfig returns default configuration
func DefaultHeartbeatIntegrationConfig() *HeartbeatIntegrationConfig {
	return &HeartbeatIntegrationConfig{
		EventQueueSize:       1000,
		EventWorkers:         3,
		HealthUpdateInterval: 5 * time.Second,
		MetricsRetention:     24 * time.Hour,
		EnableEventLogging:   false,
	}
}

// NewHeartbeatIntegrationLayer creates a new heartbeat integration layer
func NewHeartbeatIntegrationLayer(ctx context.Context, config *HeartbeatIntegrationConfig) HeartbeatIntegrationLayer {
	if config == nil {
		config = DefaultHeartbeatIntegrationConfig()
	}

	integrationCtx, cancel := context.WithCancel(ctx)

	layer := &HeartbeatIntegrationLayerImpl{
		eventQueue:    make(chan *HeartbeatEvent, config.EventQueueSize),
		eventWorkers:  config.EventWorkers,
		healthMetrics: make(map[string]*HeartbeatHealthMetrics),
		stats: &HeartbeatIntegrationStats{
			EventsByType: make(map[HeartbeatEventType]uint64),
		},
		ctx:    integrationCtx,
		cancel: cancel,
		config: config,
	}

	// Start event processing workers
	for i := 0; i < config.EventWorkers; i++ {
		layer.wg.Add(1)
		go layer.eventWorker()
	}

	// Start health metrics updater
	layer.wg.Add(1)
	go layer.healthMetricsUpdater()

	return layer
}

// InitializeIntegration initializes integration with existing components
func (hil *HeartbeatIntegrationLayerImpl) InitializeIntegration(
	heartbeatManager *HeartbeatManager,
	healthMonitor ConnectionHealthMonitor,
	errorRecovery ErrorRecoverySystem,
) error {
	hil.statsMutex.Lock()
	defer hil.statsMutex.Unlock()

	// Store component references
	hil.heartbeatManager = heartbeatManager
	hil.healthMonitor = healthMonitor
	hil.errorRecovery = errorRecovery

	// Update integration status
	hil.stats.HeartbeatManagerIntegrated = (heartbeatManager != nil)
	hil.stats.HealthMonitorIntegrated = (healthMonitor != nil)
	hil.stats.ErrorRecoveryIntegrated = (errorRecovery != nil)

	return nil
}

// SyncHeartbeatEvent synchronizes heartbeat events with existing systems
func (hil *HeartbeatIntegrationLayerImpl) SyncHeartbeatEvent(event *HeartbeatEvent) error {
	if event == nil {
		return fmt.Errorf("heartbeat event cannot be nil")
	}

	// Add timestamp if not set
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	// Queue event for processing
	select {
	case hil.eventQueue <- event:
		return nil
	case <-hil.ctx.Done():
		return fmt.Errorf("integration layer is shutting down")
	default:
		// Queue is full, increment error counter and drop event
		hil.statsMutex.Lock()
		hil.stats.IntegrationErrors++
		hil.statsMutex.Unlock()
		return fmt.Errorf("event queue is full, dropping event")
	}
}

// UpdateHealthMetrics updates health metrics from heartbeat data
func (hil *HeartbeatIntegrationLayerImpl) UpdateHealthMetrics(pathID string, metrics *HeartbeatHealthMetrics) error {
	if metrics == nil {
		return fmt.Errorf("health metrics cannot be nil")
	}

	hil.healthMutex.Lock()
	defer hil.healthMutex.Unlock()

	// Store metrics
	metrics.LastUpdate = time.Now()
	hil.healthMetrics[pathID] = metrics

	// Update statistics
	hil.statsMutex.Lock()
	hil.stats.HealthUpdates++
	hil.stats.LastHealthUpdate = time.Now()
	hil.statsMutex.Unlock()

	// Forward to health monitor if available
	if hil.healthMonitor != nil {
		// Convert heartbeat health metrics to path metrics update
		pathUpdate := hil.convertToPathMetricsUpdate(metrics)
		if err := hil.healthMonitor.UpdatePathMetrics(metrics.SessionID, pathID, pathUpdate); err != nil {
			hil.statsMutex.Lock()
			hil.stats.IntegrationErrors++
			hil.statsMutex.Unlock()
			return fmt.Errorf("failed to update health monitor: %w", err)
		}
	}

	return nil
}

// TriggerHeartbeatFailureRecovery triggers error recovery on heartbeat failures
func (hil *HeartbeatIntegrationLayerImpl) TriggerHeartbeatFailureRecovery(pathID string, failure *HeartbeatFailure) error {
	if failure == nil {
		return fmt.Errorf("heartbeat failure cannot be nil")
	}

	// Update statistics
	hil.statsMutex.Lock()
	hil.stats.RecoveryTriggers++
	hil.stats.LastRecoveryTrigger = time.Now()
	hil.statsMutex.Unlock()

	// Trigger error recovery if available
	if hil.errorRecovery != nil {
		// Convert heartbeat failure to KwikError
		kwikError := hil.convertToKwikError(failure)

		// Attempt recovery
		_, err := hil.errorRecovery.RecoverFromError(hil.ctx, kwikError)
		if err != nil {
			hil.statsMutex.Lock()
			hil.stats.IntegrationErrors++
			hil.statsMutex.Unlock()
			return fmt.Errorf("failed to trigger error recovery: %w", err)
		}
	}

	return nil
}

// GetIntegrationStats returns integration layer statistics
func (hil *HeartbeatIntegrationLayerImpl) GetIntegrationStats() *HeartbeatIntegrationStats {
	hil.statsMutex.RLock()
	defer hil.statsMutex.RUnlock()

	// Return a copy of the stats
	statsCopy := &HeartbeatIntegrationStats{
		EventsProcessed:            hil.stats.EventsProcessed,
		EventsByType:               make(map[HeartbeatEventType]uint64),
		HealthUpdates:              hil.stats.HealthUpdates,
		RecoveryTriggers:           hil.stats.RecoveryTriggers,
		IntegrationErrors:          hil.stats.IntegrationErrors,
		LastEventProcessed:         hil.stats.LastEventProcessed,
		LastHealthUpdate:           hil.stats.LastHealthUpdate,
		LastRecoveryTrigger:        hil.stats.LastRecoveryTrigger,
		HeartbeatManagerIntegrated: hil.stats.HeartbeatManagerIntegrated,
		HealthMonitorIntegrated:    hil.stats.HealthMonitorIntegrated,
		ErrorRecoveryIntegrated:    hil.stats.ErrorRecoveryIntegrated,
		AverageEventProcessingTime: hil.stats.AverageEventProcessingTime,
		MaxEventProcessingTime:     hil.stats.MaxEventProcessingTime,
	}

	// Copy events by type
	for eventType, count := range hil.stats.EventsByType {
		statsCopy.EventsByType[eventType] = count
	}

	return statsCopy
}

// Shutdown gracefully shuts down the integration layer
func (hil *HeartbeatIntegrationLayerImpl) Shutdown() error {
	// Cancel context to stop workers
	hil.cancel()

	// Wait for all workers to finish
	hil.wg.Wait()

	// Close event queue
	close(hil.eventQueue)

	return nil
}

// eventWorker processes heartbeat events from the queue
func (hil *HeartbeatIntegrationLayerImpl) eventWorker() {
	defer hil.wg.Done()

	for {
		select {
		case event := <-hil.eventQueue:
			if event != nil {
				hil.processHeartbeatEvent(event)
			}
		case <-hil.ctx.Done():
			return
		}
	}
}

// processHeartbeatEvent processes a single heartbeat event
func (hil *HeartbeatIntegrationLayerImpl) processHeartbeatEvent(event *HeartbeatEvent) {
	startTime := time.Now()

	// Update statistics
	hil.statsMutex.Lock()
	hil.stats.EventsProcessed++
	hil.stats.EventsByType[event.Type]++
	hil.stats.LastEventProcessed = time.Now()
	hil.statsMutex.Unlock()

	// Process event based on type
	switch event.Type {
	case HeartbeatEventSent:
		hil.processHeartbeatSent(event)
	case HeartbeatEventReceived:
		hil.processHeartbeatReceived(event)
	case HeartbeatEventResponseSent:
		hil.processHeartbeatResponseSent(event)
	case HeartbeatEventResponseReceived:
		hil.processHeartbeatResponseReceived(event)
	case HeartbeatEventTimeout:
		hil.processHeartbeatTimeout(event)
	case HeartbeatEventFailure:
		hil.processHeartbeatFailure(event)
	case HeartbeatEventRecovery:
		hil.processHeartbeatRecovery(event)
	}

	// Update processing time statistics
	processingTime := time.Since(startTime)
	hil.statsMutex.Lock()
	if hil.stats.AverageEventProcessingTime == 0 {
		hil.stats.AverageEventProcessingTime = processingTime
	} else {
		hil.stats.AverageEventProcessingTime = (hil.stats.AverageEventProcessingTime + processingTime) / 2
	}
	if processingTime > hil.stats.MaxEventProcessingTime {
		hil.stats.MaxEventProcessingTime = processingTime
	}
	hil.statsMutex.Unlock()
}

// processHeartbeatSent processes a heartbeat sent event
func (hil *HeartbeatIntegrationLayerImpl) processHeartbeatSent(event *HeartbeatEvent) {
	// Notify heartbeat manager if available
	if hil.heartbeatManager != nil {
		// The heartbeat manager doesn't have a direct "sent" notification,
		// but we can use this to track timing for RTT calculation
	}
}

// processHeartbeatReceived processes a heartbeat received event
func (hil *HeartbeatIntegrationLayerImpl) processHeartbeatReceived(event *HeartbeatEvent) {
	// Notify heartbeat manager if available
	if hil.heartbeatManager != nil {
		hil.heartbeatManager.OnHeartbeatReceived(event.SessionID, event.PathID)
	}

	// Update health monitor activity
	if hil.healthMonitor != nil {
		update := PathMetricsUpdate{
			HeartbeatReceived: true,
			Activity:          true,
		}
		hil.healthMonitor.UpdatePathMetrics(event.SessionID, event.PathID, update)
	}
}

// processHeartbeatResponseSent processes a heartbeat response sent event
func (hil *HeartbeatIntegrationLayerImpl) processHeartbeatResponseSent(event *HeartbeatEvent) {
	// Update health monitor activity
	if hil.healthMonitor != nil {
		update := PathMetricsUpdate{
			Activity: true,
		}
		hil.healthMonitor.UpdatePathMetrics(event.SessionID, event.PathID, update)
	}
}

// processHeartbeatResponseReceived processes a heartbeat response received event
func (hil *HeartbeatIntegrationLayerImpl) processHeartbeatResponseReceived(event *HeartbeatEvent) {
	// Notify heartbeat manager
	if hil.heartbeatManager != nil {
		hil.heartbeatManager.OnHeartbeatReceived(event.SessionID, event.PathID)
	}

	// Update health monitor with RTT if available
	if hil.healthMonitor != nil {
		update := PathMetricsUpdate{
			HeartbeatReceived: true,
			Activity:          true,
		}
		if event.RTT != nil {
			update.RTT = event.RTT
		}
		hil.healthMonitor.UpdatePathMetrics(event.SessionID, event.PathID, update)
	}
}

// processHeartbeatTimeout processes a heartbeat timeout event
func (hil *HeartbeatIntegrationLayerImpl) processHeartbeatTimeout(event *HeartbeatEvent) {
	// Create heartbeat failure for recovery
	failure := &HeartbeatFailure{
		Type:         HeartbeatFailureTimeout,
		PathID:       event.PathID,
		SessionID:    event.SessionID,
		StreamID:     event.StreamID,
		PlaneType:    event.PlaneType,
		SequenceID:   event.SequenceID,
		FailureTime:  event.Timestamp,
		ErrorMessage: "heartbeat timeout",
		Recoverable:  true,
		Metadata:     event.Metadata,
	}

	// Trigger recovery
	hil.TriggerHeartbeatFailureRecovery(event.PathID, failure)
}

// processHeartbeatFailure processes a heartbeat failure event
func (hil *HeartbeatIntegrationLayerImpl) processHeartbeatFailure(event *HeartbeatEvent) {
	// Create heartbeat failure for recovery
	failure := &HeartbeatFailure{
		Type:         HeartbeatFailureSendError, // Default, could be refined based on error code
		PathID:       event.PathID,
		SessionID:    event.SessionID,
		StreamID:     event.StreamID,
		PlaneType:    event.PlaneType,
		SequenceID:   event.SequenceID,
		FailureTime:  event.Timestamp,
		ErrorMessage: event.ErrorMessage,
		Recoverable:  true,
		Metadata:     event.Metadata,
	}

	// Refine failure type based on error code
	switch event.ErrorCode {
	case "TIMEOUT":
		failure.Type = HeartbeatFailureTimeout
	case "INVALID_RESPONSE":
		failure.Type = HeartbeatFailureInvalidResponse
	case "SEQUENCE_MISMATCH":
		failure.Type = HeartbeatFailureSequenceMismatch
	case "PATH_DEAD":
		failure.Type = HeartbeatFailurePathDead
		failure.Recoverable = false
	}

	// Trigger recovery
	hil.TriggerHeartbeatFailureRecovery(event.PathID, failure)
}

// processHeartbeatRecovery processes a heartbeat recovery event
func (hil *HeartbeatIntegrationLayerImpl) processHeartbeatRecovery(event *HeartbeatEvent) {
	// Update health monitor with recovery
	if hil.healthMonitor != nil {
		update := PathMetricsUpdate{
			Activity: true,
		}
		hil.healthMonitor.UpdatePathMetrics(event.SessionID, event.PathID, update)
	}
}

// healthMetricsUpdater periodically updates health metrics
func (hil *HeartbeatIntegrationLayerImpl) healthMetricsUpdater() {
	defer hil.wg.Done()

	ticker := time.NewTicker(hil.config.HealthUpdateInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			hil.updateHealthMetrics()
		case <-hil.ctx.Done():
			return
		}
	}
}

// updateHealthMetrics updates health metrics for all paths
func (hil *HeartbeatIntegrationLayerImpl) updateHealthMetrics() {
	hil.healthMutex.RLock()
	defer hil.healthMutex.RUnlock()

	// Clean up old metrics
	cutoff := time.Now().Add(-hil.config.MetricsRetention)
	for pathID, metrics := range hil.healthMetrics {
		if metrics.LastUpdate.Before(cutoff) {
			delete(hil.healthMetrics, pathID)
		}
	}
}

// convertToPathMetricsUpdate converts heartbeat health metrics to path metrics update
func (hil *HeartbeatIntegrationLayerImpl) convertToPathMetricsUpdate(metrics *HeartbeatHealthMetrics) PathMetricsUpdate {
	update := PathMetricsUpdate{
		Activity: true,
	}

	// Add RTT if available from control plane
	if metrics.ControlHeartbeatStats != nil && metrics.ControlHeartbeatStats.AverageRTT > 0 {
		update.RTT = &metrics.ControlHeartbeatStats.AverageRTT
	}

	// Add heartbeat activity
	if metrics.ControlHeartbeatStats != nil {
		if metrics.ControlHeartbeatStats.HeartbeatsSent > 0 {
			update.HeartbeatSent = true
		}
		if metrics.ControlHeartbeatStats.ResponsesReceived > 0 {
			update.HeartbeatReceived = true
		}
	}

	return update
}

// convertToKwikError converts heartbeat failure to KwikError for error recovery
func (hil *HeartbeatIntegrationLayerImpl) convertToKwikError(failure *HeartbeatFailure) *KwikError {
	var errorType ErrorType
	var errorCode string

	switch failure.Type {
	case HeartbeatFailureTimeout:
		errorType = ErrorTypeTimeout
		errorCode = "HEARTBEAT_TIMEOUT"
	case HeartbeatFailureSendError:
		errorType = ErrorTypeNetwork
		errorCode = "HEARTBEAT_SEND_ERROR"
	case HeartbeatFailureInvalidResponse:
		errorType = ErrorTypeProtocol
		errorCode = "HEARTBEAT_INVALID_RESPONSE"
	case HeartbeatFailureSequenceMismatch:
		errorType = ErrorTypeProtocol
		errorCode = "HEARTBEAT_SEQUENCE_MISMATCH"
	case HeartbeatFailurePathDead:
		errorType = ErrorTypePath
		errorCode = "HEARTBEAT_PATH_DEAD"
	default:
		errorType = ErrorTypeUnknown
		errorCode = "HEARTBEAT_UNKNOWN_ERROR"
	}

	kwikError := &KwikError{
		Code:        errorCode,
		Message:     failure.ErrorMessage,
		Timestamp:   failure.FailureTime,
		Type:        errorType,
		Severity:    SeverityMedium, // Default severity for heartbeat failures
		Category:    CategoryTransient,
		SessionID:   failure.SessionID,
		PathID:      failure.PathID,
		StreamID:    0, // Default to 0 if not set
		Component:   "heartbeat",
		Recoverable: failure.Recoverable,
		RetryCount:  failure.RecoveryAttempts,
		LastRetry:   failure.FailureTime,
		Metadata:    failure.Metadata,
	}

	if failure.StreamID != nil {
		kwikError.StreamID = *failure.StreamID
	}

	return kwikError
}
