package presentation

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// ConfigManager manages runtime configuration for the presentation layer
type ConfigManager struct {
	// Current configuration
	config      *PresentationConfig
	configMutex sync.RWMutex

	// Configuration history
	history      []ConfigurationUpdate
	historyMutex sync.RWMutex

	// Change notifications
	subscribers []ConfigChangeSubscriber
	subsMutex   sync.RWMutex

	// Validation
	validator ConfigValidator

	// Persistence
	persistor ConfigPersistor

	// Runtime state
	started bool
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// ConfigChangeSubscriber receives notifications about configuration changes
type ConfigChangeSubscriber interface {
	OnConfigChanged(oldConfig, newConfig *PresentationConfig, update ConfigurationUpdate) error
}

// ConfigValidator validates configuration changes
type ConfigValidator interface {
	ValidateConfig(config *PresentationConfig) error
	ValidateUpdate(current *PresentationConfig, update ConfigurationUpdate) error
}

// ConfigPersistor handles configuration persistence
type ConfigPersistor interface {
	SaveConfig(config *PresentationConfig) error
	LoadConfig() (*PresentationConfig, error)
	SaveHistory(history []ConfigurationUpdate) error
	LoadHistory() ([]ConfigurationUpdate, error)
}

// AdminInterface provides administrative operations for the presentation layer
type AdminInterface struct {
	configManager    *ConfigManager
	metricsCollector *MetricsCollector
	dpm              *DataPresentationManagerImpl

	// Command processing
	commandQueue  chan AdminCommand
	responseQueue chan AdminResponse

	// Authentication and authorization
	authProvider AuthProvider

	// Lifecycle
	started bool
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// AdminCommand represents an administrative command
type AdminCommand struct {
	ID          string                 `json:"id"`
	Type        AdminCommandType       `json:"type"`
	Parameters  map[string]interface{} `json:"parameters"`
	RequestedBy string                 `json:"requested_by"`
	RequestedAt time.Time              `json:"requested_at"`
	Timeout     time.Duration          `json:"timeout"`
}

// AdminResponse represents the response to an administrative command
type AdminResponse struct {
	CommandID  string                 `json:"command_id"`
	Success    bool                   `json:"success"`
	Result     interface{}            `json:"result,omitempty"`
	Error      string                 `json:"error,omitempty"`
	ExecutedAt time.Time              `json:"executed_at"`
	Duration   time.Duration          `json:"duration"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
}

// AdminCommandType defines the type of administrative command
type AdminCommandType int

const (
	AdminCommandGetConfig AdminCommandType = iota
	AdminCommandUpdateConfig
	AdminCommandGetMetrics
	AdminCommandGetStreamInfo
	AdminCommandCreateStream
	AdminCommandRemoveStream
	AdminCommandGetSystemHealth
	AdminCommandRunDiagnostics
	AdminCommandExportMetrics
	AdminCommandImportConfig
	AdminCommandResetMetrics
	AdminCommandShutdownSystem
	AdminCommandRestartSystem
	AdminCommandGetLogs
	AdminCommandSetLogLevel
	AdminCommandRunMaintenance
	AdminCommandBackupConfig
	AdminCommandRestoreConfig
)

// AuthProvider handles authentication and authorization
type AuthProvider interface {
	Authenticate(credentials map[string]interface{}) (AuthContext, error)
	Authorize(ctx AuthContext, command AdminCommandType) error
}

// AuthContext contains authentication context
type AuthContext struct {
	UserID      string                 `json:"user_id"`
	Roles       []string               `json:"roles"`
	Permissions []string               `json:"permissions"`
	ExpiresAt   time.Time              `json:"expires_at"`
	Metadata    map[string]interface{} `json:"metadata"`
}

// DiagnosticRunner performs system diagnostics
type DiagnosticRunner struct {
	dpm              *DataPresentationManagerImpl
	metricsCollector *MetricsCollector
	configManager    *ConfigManager
}

// DiagnosticResult contains the result of a diagnostic check
type DiagnosticResult struct {
	CheckName string                 `json:"check_name"`
	Status    DiagnosticStatus       `json:"status"`
	Message   string                 `json:"message"`
	Details   map[string]interface{} `json:"details"`
	Timestamp time.Time              `json:"timestamp"`
	Duration  time.Duration          `json:"duration"`
}

// DiagnosticStatus represents the status of a diagnostic check
type DiagnosticStatus int

const (
	DiagnosticStatusPass DiagnosticStatus = iota
	DiagnosticStatusWarn
	DiagnosticStatusFail
	DiagnosticStatusError
)

func (ds DiagnosticStatus) String() string {
	switch ds {
	case DiagnosticStatusPass:
		return "PASS"
	case DiagnosticStatusWarn:
		return "WARN"
	case DiagnosticStatusFail:
		return "FAIL"
	case DiagnosticStatusError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// NewConfigManager creates a new configuration manager
func NewConfigManager(config *PresentationConfig, validator ConfigValidator, persistor ConfigPersistor) *ConfigManager {
	ctx, cancel := context.WithCancel(context.Background())

	return &ConfigManager{
		config:      config,
		history:     make([]ConfigurationUpdate, 0),
		subscribers: make([]ConfigChangeSubscriber, 0),
		validator:   validator,
		persistor:   persistor,
		ctx:         ctx,
		cancel:      cancel,
	}
}

// Start starts the configuration manager
func (cm *ConfigManager) Start() error {
	cm.configMutex.Lock()
	defer cm.configMutex.Unlock()

	if cm.started {
		return nil
	}

	// Load configuration from persistence if available
	if cm.persistor != nil {
		if loadedConfig, err := cm.persistor.LoadConfig(); err == nil {
			cm.config = loadedConfig
		}

		if loadedHistory, err := cm.persistor.LoadHistory(); err == nil {
			cm.history = loadedHistory
		}
	}

	cm.started = true
	return nil
}

// Stop stops the configuration manager
func (cm *ConfigManager) Stop() error {
	cm.configMutex.Lock()
	defer cm.configMutex.Unlock()

	if !cm.started {
		return nil
	}

	cm.cancel()
	cm.wg.Wait()

	// Save configuration and history
	if cm.persistor != nil {
		cm.persistor.SaveConfig(cm.config)
		cm.persistor.SaveHistory(cm.history)
	}

	cm.started = false
	return nil
}

// GetConfig returns the current configuration
func (cm *ConfigManager) GetConfig() *PresentationConfig {
	cm.configMutex.RLock()
	defer cm.configMutex.RUnlock()

	// Return a copy to prevent external modification
	configCopy := *cm.config
	return &configCopy
}

// UpdateConfig updates the configuration
func (cm *ConfigManager) UpdateConfig(update ConfigurationUpdate) error {
	cm.configMutex.Lock()
	defer cm.configMutex.Unlock()

	oldConfig := *cm.config

	// Validate the update
	if cm.validator != nil {
		if err := cm.validator.ValidateUpdate(cm.config, update); err != nil {
			return fmt.Errorf("configuration update validation failed: %w", err)
		}
	}

	// Apply the update
	newConfig, err := cm.applyUpdate(cm.config, update)
	if err != nil {
		return fmt.Errorf("failed to apply configuration update: %w", err)
	}

	// Validate the new configuration
	if cm.validator != nil {
		if err := cm.validator.ValidateConfig(newConfig); err != nil {
			return fmt.Errorf("new configuration validation failed: %w", err)
		}
	}

	// Update the configuration
	cm.config = newConfig

	// Add to history
	update.RequestedAt = time.Now()
	cm.historyMutex.Lock()
	cm.history = append(cm.history, update)
	cm.historyMutex.Unlock()

	// Notify subscribers
	cm.notifySubscribers(&oldConfig, newConfig, update)

	// Persist if available
	if cm.persistor != nil {
		cm.persistor.SaveConfig(cm.config)
		cm.persistor.SaveHistory(cm.history)
	}

	return nil
}

// applyUpdate applies a configuration update
func (cm *ConfigManager) applyUpdate(current *PresentationConfig, update ConfigurationUpdate) (*PresentationConfig, error) {
	newConfig := *current

	switch update.UpdateType {
	case ConfigUpdateGlobal:
		if update.ReceiveWindowSize != nil {
			newConfig.ReceiveWindowSize = *update.ReceiveWindowSize
		}
		if update.BackpressureThreshold != nil {
			newConfig.BackpressureThreshold = *update.BackpressureThreshold
		}
		if update.StreamBufferSize != nil {
			newConfig.DefaultStreamBufferSize = *update.StreamBufferSize
		}
		if update.GapTimeout != nil {
			newConfig.GapTimeout = *update.GapTimeout
		}

	case ConfigUpdateStream:
		// Stream-specific updates would be handled here
		// This would require extending the configuration structure

	case ConfigUpdateWindow:
		if update.ReceiveWindowSize != nil {
			newConfig.ReceiveWindowSize = *update.ReceiveWindowSize
		}

	case ConfigUpdateBackpressure:
		if update.BackpressureThreshold != nil {
			newConfig.BackpressureThreshold = *update.BackpressureThreshold
		}

	default:
		return nil, fmt.Errorf("unknown update type: %v", update.UpdateType)
	}

	return &newConfig, nil
}

// Subscribe adds a configuration change subscriber
func (cm *ConfigManager) Subscribe(subscriber ConfigChangeSubscriber) {
	cm.subsMutex.Lock()
	defer cm.subsMutex.Unlock()

	cm.subscribers = append(cm.subscribers, subscriber)
}

// Unsubscribe removes a configuration change subscriber
func (cm *ConfigManager) Unsubscribe(subscriber ConfigChangeSubscriber) {
	cm.subsMutex.Lock()
	defer cm.subsMutex.Unlock()

	for i, sub := range cm.subscribers {
		if sub == subscriber {
			cm.subscribers = append(cm.subscribers[:i], cm.subscribers[i+1:]...)
			break
		}
	}
}

// notifySubscribers notifies all subscribers of configuration changes
func (cm *ConfigManager) notifySubscribers(oldConfig, newConfig *PresentationConfig, update ConfigurationUpdate) {
	cm.subsMutex.RLock()
	defer cm.subsMutex.RUnlock()

	for _, subscriber := range cm.subscribers {
		go func(sub ConfigChangeSubscriber) {
			if err := sub.OnConfigChanged(oldConfig, newConfig, update); err != nil {
				// Log error (in a real implementation)
				_ = err
			}
		}(subscriber)
	}
}

// GetHistory returns the configuration change history
func (cm *ConfigManager) GetHistory() []ConfigurationUpdate {
	cm.historyMutex.RLock()
	defer cm.historyMutex.RUnlock()

	// Return a copy
	history := make([]ConfigurationUpdate, len(cm.history))
	copy(history, cm.history)
	return history
}

// NewAdminInterface creates a new administrative interface
func NewAdminInterface(configManager *ConfigManager, metricsCollector *MetricsCollector, dpm *DataPresentationManagerImpl, authProvider AuthProvider) *AdminInterface {
	ctx, cancel := context.WithCancel(context.Background())

	return &AdminInterface{
		configManager:    configManager,
		metricsCollector: metricsCollector,
		dpm:              dpm,
		commandQueue:     make(chan AdminCommand, 100),
		responseQueue:    make(chan AdminResponse, 100),
		authProvider:     authProvider,
		ctx:              ctx,
		cancel:           cancel,
	}
}

// Start starts the administrative interface
func (ai *AdminInterface) Start() error {
	if ai.started {
		return nil
	}

	// Start command processor
	ai.wg.Add(1)
	go ai.commandProcessor()

	ai.started = true
	return nil
}

// Stop stops the administrative interface
func (ai *AdminInterface) Stop() error {
	if !ai.started {
		return nil
	}

	ai.cancel()
	close(ai.commandQueue)
	ai.wg.Wait()
	close(ai.responseQueue)

	ai.started = false
	return nil
}

// ExecuteCommand executes an administrative command
func (ai *AdminInterface) ExecuteCommand(command AdminCommand, authCtx AuthContext) (AdminResponse, error) {
	// Authorize the command
	if ai.authProvider != nil {
		if err := ai.authProvider.Authorize(authCtx, command.Type); err != nil {
			return AdminResponse{
				CommandID:  command.ID,
				Success:    false,
				Error:      fmt.Sprintf("authorization failed: %v", err),
				ExecutedAt: time.Now(),
			}, err
		}
	}

	// Set timeout if not specified
	if command.Timeout == 0 {
		command.Timeout = 30 * time.Second
	}

	// Submit command
	select {
	case ai.commandQueue <- command:
	case <-time.After(command.Timeout):
		return AdminResponse{
			CommandID:  command.ID,
			Success:    false,
			Error:      "command submission timeout",
			ExecutedAt: time.Now(),
		}, ErrTimeout
	}

	// Wait for response
	timeout := time.NewTimer(command.Timeout)
	defer timeout.Stop()

	for {
		select {
		case response := <-ai.responseQueue:
			if response.CommandID == command.ID {
				return response, nil
			}
			// Not our response, put it back (simplified)

		case <-timeout.C:
			return AdminResponse{
				CommandID:  command.ID,
				Success:    false,
				Error:      "command execution timeout",
				ExecutedAt: time.Now(),
			}, ErrTimeout

		case <-ai.ctx.Done():
			return AdminResponse{
				CommandID:  command.ID,
				Success:    false,
				Error:      "system shutdown",
				ExecutedAt: time.Now(),
			}, ErrSystemShutdown
		}
	}
}

// commandProcessor processes administrative commands
func (ai *AdminInterface) commandProcessor() {
	defer ai.wg.Done()

	for {
		select {
		case command := <-ai.commandQueue:
			response := ai.processCommand(command)

			select {
			case ai.responseQueue <- response:
			case <-ai.ctx.Done():
				return
			}

		case <-ai.ctx.Done():
			return
		}
	}
}

// processCommand processes a single administrative command
func (ai *AdminInterface) processCommand(command AdminCommand) AdminResponse {
	startTime := time.Now()

	response := AdminResponse{
		CommandID:  command.ID,
		ExecutedAt: startTime,
	}

	switch command.Type {
	case AdminCommandGetConfig:
		response.Result = ai.configManager.GetConfig()
		response.Success = true

	case AdminCommandUpdateConfig:
		err := ai.handleUpdateConfig(command)
		if err != nil {
			response.Error = err.Error()
		} else {
			response.Success = true
		}

	case AdminCommandGetMetrics:
		response.Result = ai.metricsCollector.GetGlobalMetrics()
		response.Success = true

	case AdminCommandGetStreamInfo:
		result, err := ai.handleGetStreamInfo(command)
		if err != nil {
			response.Error = err.Error()
		} else {
			response.Result = result
			response.Success = true
		}

	case AdminCommandCreateStream:
		err := ai.handleCreateStream(command)
		if err != nil {
			response.Error = err.Error()
		} else {
			response.Success = true
		}

	case AdminCommandRemoveStream:
		err := ai.handleRemoveStream(command)
		if err != nil {
			response.Error = err.Error()
		} else {
			response.Success = true
		}

	case AdminCommandGetSystemHealth:
		response.Result = ai.getSystemHealth()
		response.Success = true

	case AdminCommandRunDiagnostics:
		result, err := ai.runDiagnostics()
		if err != nil {
			response.Error = err.Error()
		} else {
			response.Result = result
			response.Success = true
		}

	case AdminCommandExportMetrics:
		err := ai.handleExportMetrics(command)
		if err != nil {
			response.Error = err.Error()
		} else {
			response.Success = true
		}

	default:
		response.Error = fmt.Sprintf("unknown command type: %v", command.Type)
	}

	response.Duration = time.Since(startTime)
	return response
}

// handleUpdateConfig handles configuration update commands
func (ai *AdminInterface) handleUpdateConfig(command AdminCommand) error {
	// Extract update parameters
	update := ConfigurationUpdate{
		UpdateType:  ConfigUpdateGlobal,
		RequestedBy: command.RequestedBy,
		RequestedAt: command.RequestedAt,
		Reason:      "Admin command",
	}

	if windowSize, ok := command.Parameters["receive_window_size"]; ok {
		if size, ok := windowSize.(float64); ok {
			sizeUint := uint64(size)
			update.ReceiveWindowSize = &sizeUint
		}
	}

	if threshold, ok := command.Parameters["backpressure_threshold"]; ok {
		if thresh, ok := threshold.(float64); ok {
			update.BackpressureThreshold = &thresh
		}
	}

	return ai.configManager.UpdateConfig(update)
}

// handleGetStreamInfo handles stream information requests
func (ai *AdminInterface) handleGetStreamInfo(command AdminCommand) (interface{}, error) {
	if streamIDParam, ok := command.Parameters["stream_id"]; ok {
		if streamIDFloat, ok := streamIDParam.(float64); ok {
			streamID := uint64(streamIDFloat)

			// Get stream metrics
			metrics, exists := ai.metricsCollector.GetStreamMetrics(streamID)
			if !exists {
				return nil, fmt.Errorf("stream %d not found", streamID)
			}

			// Get stream buffer info
			buffer, err := ai.dpm.GetStreamBuffer(streamID)
			if err != nil {
				return nil, fmt.Errorf("failed to get stream buffer: %w", err)
			}

			return map[string]interface{}{
				"metrics":  metrics,
				"has_gaps": buffer.HasGaps(),
			}, nil
		}
	}

	// Return all streams
	return ai.metricsCollector.GetAllStreamMetrics(), nil
}

// handleCreateStream handles stream creation commands
func (ai *AdminInterface) handleCreateStream(command AdminCommand) error {
	streamIDParam, ok := command.Parameters["stream_id"]
	if !ok {
		return fmt.Errorf("stream_id parameter required")
	}

	streamIDFloat, ok := streamIDParam.(float64)
	if !ok {
		return fmt.Errorf("invalid stream_id type")
	}

	streamID := uint64(streamIDFloat)

	// Create stream metadata
	metadata := &StreamMetadata{
		StreamID:   streamID,
		StreamType: StreamTypeData,
		Priority:   StreamPriorityNormal,
		CreatedAt:  time.Now(),
	}

	return ai.dpm.CreateStreamBuffer(streamID, metadata)
}

// handleRemoveStream handles stream removal commands
func (ai *AdminInterface) handleRemoveStream(command AdminCommand) error {
	streamIDParam, ok := command.Parameters["stream_id"]
	if !ok {
		return fmt.Errorf("stream_id parameter required")
	}

	streamIDFloat, ok := streamIDParam.(float64)
	if !ok {
		return fmt.Errorf("invalid stream_id type")
	}

	streamID := uint64(streamIDFloat)
	return ai.dpm.RemoveStreamBuffer(streamID)
}

// getSystemHealth returns system health information
func (ai *AdminInterface) getSystemHealth() SystemHealthMetrics {
	globalMetrics := ai.metricsCollector.GetGlobalMetrics()

	// Calculate health score
	healthScore := 100

	// Reduce score based on error rate
	if globalMetrics.TotalOperations > 0 {
		errorRate := float64(globalMetrics.TotalErrors) / float64(globalMetrics.TotalOperations)
		if errorRate > 0.1 {
			healthScore -= int(errorRate * 50)
		}
	}

	// Reduce score based on high latency
	if globalMetrics.AverageLatency > 100*time.Millisecond {
		healthScore -= 20
	}

	// Reduce score based on memory utilization
	if globalMetrics.MemoryUtilization > 0.8 {
		healthScore -= 15
	}

	if healthScore < 0 {
		healthScore = 0
	}

	return SystemHealthMetrics{
		HealthScore:         healthScore,
		WindowManagerHealth: 100, // Would be calculated based on actual metrics
		BufferManagerHealth: 100, // Would be calculated based on actual metrics
		BackpressureHealth:  100, // Would be calculated based on actual metrics
		MemoryHealth:        int((1.0 - globalMetrics.MemoryUtilization) * 100),
		CPUHealth:           int((1.0 - globalMetrics.CPUUtilization) * 100),
		UptimeSeconds:       uint64(globalMetrics.Uptime.Seconds()),
		LastHealthCheck:     time.Now(),
		ActiveAlerts:        []string{}, // Would be populated from actual alerts
		WarningCount:        0,          // Would be calculated from actual warnings
		ErrorCount:          int(globalMetrics.TotalErrors),
	}
}

// handleExportMetrics handles metrics export commands
func (ai *AdminInterface) handleExportMetrics(command AdminCommand) error {
	formatParam, ok := command.Parameters["format"]
	if !ok {
		return fmt.Errorf("format parameter required")
	}

	pathParam, ok := command.Parameters["path"]
	if !ok {
		return fmt.Errorf("path parameter required")
	}

	formatStr, ok := formatParam.(string)
	if !ok {
		return fmt.Errorf("invalid format type")
	}

	pathStr, ok := pathParam.(string)
	if !ok {
		return fmt.Errorf("invalid path type")
	}

	var format ExportFormat
	switch formatStr {
	case "json":
		format = ExportFormatJSON
	case "prometheus":
		format = ExportFormatPrometheus
	case "influxdb":
		format = ExportFormatInfluxDB
	case "csv":
		format = ExportFormatCSV
	default:
		return fmt.Errorf("unsupported format: %s", formatStr)
	}

	return ai.metricsCollector.ExportMetrics(format, pathStr)
}

// runDiagnostics runs system diagnostics
func (ai *AdminInterface) runDiagnostics() ([]DiagnosticResult, error) {
	runner := &DiagnosticRunner{
		dpm:              ai.dpm,
		metricsCollector: ai.metricsCollector,
		configManager:    ai.configManager,
	}

	return runner.RunDiagnostics()
}

// RunDiagnostics runs comprehensive system diagnostics
func (dr *DiagnosticRunner) RunDiagnostics() ([]DiagnosticResult, error) {
	results := make([]DiagnosticResult, 0)

	// Check configuration validity
	results = append(results, dr.checkConfiguration())

	// Check system health
	results = append(results, dr.checkSystemHealth())

	// Check stream health
	results = append(results, dr.checkStreamHealth())

	// Check memory usage
	results = append(results, dr.checkMemoryUsage())

	// Check performance metrics
	results = append(results, dr.checkPerformanceMetrics())

	// Check backpressure status
	results = append(results, dr.checkBackpressureStatus())

	return results, nil
}

// checkConfiguration checks configuration validity
func (dr *DiagnosticRunner) checkConfiguration() DiagnosticResult {
	startTime := time.Now()

	config := dr.configManager.GetConfig()
	err := config.Validate()

	result := DiagnosticResult{
		CheckName: "Configuration Validity",
		Timestamp: startTime,
		Duration:  time.Since(startTime),
	}

	if err != nil {
		result.Status = DiagnosticStatusFail
		result.Message = fmt.Sprintf("Configuration validation failed: %v", err)
	} else {
		result.Status = DiagnosticStatusPass
		result.Message = "Configuration is valid"
	}

	result.Details = map[string]interface{}{
		"receive_window_size":    config.ReceiveWindowSize,
		"backpressure_threshold": config.BackpressureThreshold,
		"default_buffer_size":    config.DefaultStreamBufferSize,
		"parallel_workers":       config.ParallelWorkers,
	}

	return result
}

// checkSystemHealth checks overall system health
func (dr *DiagnosticRunner) checkSystemHealth() DiagnosticResult {
	startTime := time.Now()

	globalMetrics := dr.metricsCollector.GetGlobalMetrics()

	result := DiagnosticResult{
		CheckName: "System Health",
		Timestamp: startTime,
		Duration:  time.Since(startTime),
	}

	// Calculate health score
	healthScore := 100
	issues := make([]string, 0)

	// Check error rate
	if globalMetrics.TotalOperations > 0 {
		errorRate := float64(globalMetrics.TotalErrors) / float64(globalMetrics.TotalOperations)
		if errorRate > 0.1 {
			healthScore -= 30
			issues = append(issues, fmt.Sprintf("High error rate: %.2f%%", errorRate*100))
		}
	}

	// Check latency
	if globalMetrics.AverageLatency > 100*time.Millisecond {
		healthScore -= 20
		issues = append(issues, fmt.Sprintf("High latency: %v", globalMetrics.AverageLatency))
	}

	// Check memory utilization
	if globalMetrics.MemoryUtilization > 0.8 {
		healthScore -= 25
		issues = append(issues, fmt.Sprintf("High memory utilization: %.2f%%", globalMetrics.MemoryUtilization*100))
	}

	if healthScore >= 80 {
		result.Status = DiagnosticStatusPass
		result.Message = "System health is good"
	} else if healthScore >= 60 {
		result.Status = DiagnosticStatusWarn
		result.Message = "System health has some issues"
	} else {
		result.Status = DiagnosticStatusFail
		result.Message = "System health is poor"
	}

	result.Details = map[string]interface{}{
		"health_score":       healthScore,
		"issues":             issues,
		"active_streams":     globalMetrics.ActiveStreams,
		"total_operations":   globalMetrics.TotalOperations,
		"total_errors":       globalMetrics.TotalErrors,
		"average_latency":    globalMetrics.AverageLatency.String(),
		"memory_utilization": globalMetrics.MemoryUtilization,
	}

	return result
}

// checkStreamHealth checks the health of individual streams
func (dr *DiagnosticRunner) checkStreamHealth() DiagnosticResult {
	startTime := time.Now()

	streamMetrics := dr.metricsCollector.GetAllStreamMetrics()

	result := DiagnosticResult{
		CheckName: "Stream Health",
		Timestamp: startTime,
		Duration:  time.Since(startTime),
	}

	healthyStreams := 0
	unhealthyStreams := 0
	issues := make([]string, 0)

	for streamID, metrics := range streamMetrics {
		// Check for stalled streams
		if time.Since(metrics.LastActivity) > 5*time.Minute {
			unhealthyStreams++
			issues = append(issues, fmt.Sprintf("Stream %d appears stalled (last activity: %v)", streamID, metrics.LastActivity))
			continue
		}

		// Check error rate
		totalOps := metrics.WriteOperations + metrics.ReadOperations
		totalErrors := metrics.WriteErrors + metrics.ReadErrors
		if totalOps > 0 {
			errorRate := float64(totalErrors) / float64(totalOps)
			if errorRate > 0.2 {
				unhealthyStreams++
				issues = append(issues, fmt.Sprintf("Stream %d has high error rate: %.2f%%", streamID, errorRate*100))
				continue
			}
		}

		healthyStreams++
	}

	if unhealthyStreams == 0 {
		result.Status = DiagnosticStatusPass
		result.Message = "All streams are healthy"
	} else if unhealthyStreams < len(streamMetrics)/2 {
		result.Status = DiagnosticStatusWarn
		result.Message = fmt.Sprintf("%d streams have issues", unhealthyStreams)
	} else {
		result.Status = DiagnosticStatusFail
		result.Message = fmt.Sprintf("Many streams (%d) have issues", unhealthyStreams)
	}

	result.Details = map[string]interface{}{
		"total_streams":     len(streamMetrics),
		"healthy_streams":   healthyStreams,
		"unhealthy_streams": unhealthyStreams,
		"issues":            issues,
	}

	return result
}

// checkMemoryUsage checks memory usage patterns
func (dr *DiagnosticRunner) checkMemoryUsage() DiagnosticResult {
	startTime := time.Now()

	globalMetrics := dr.metricsCollector.GetGlobalMetrics()

	result := DiagnosticResult{
		CheckName: "Memory Usage",
		Timestamp: startTime,
		Duration:  time.Since(startTime),
	}

	if globalMetrics.MemoryUtilization < 0.7 {
		result.Status = DiagnosticStatusPass
		result.Message = "Memory usage is normal"
	} else if globalMetrics.MemoryUtilization < 0.85 {
		result.Status = DiagnosticStatusWarn
		result.Message = "Memory usage is elevated"
	} else {
		result.Status = DiagnosticStatusFail
		result.Message = "Memory usage is critical"
	}

	result.Details = map[string]interface{}{
		"memory_utilization": globalMetrics.MemoryUtilization,
		"window_utilization": globalMetrics.WindowUtilization,
	}

	return result
}

// checkPerformanceMetrics checks performance metrics
func (dr *DiagnosticRunner) checkPerformanceMetrics() DiagnosticResult {
	startTime := time.Now()

	globalMetrics := dr.metricsCollector.GetGlobalMetrics()

	result := DiagnosticResult{
		CheckName: "Performance Metrics",
		Timestamp: startTime,
		Duration:  time.Since(startTime),
	}

	issues := make([]string, 0)

	// Check throughput
	if globalMetrics.ThroughputBytesPerSec < 1024*1024 { // Less than 1MB/s
		issues = append(issues, fmt.Sprintf("Low throughput: %d bytes/sec", globalMetrics.ThroughputBytesPerSec))
	}

	// Check latency
	if globalMetrics.AverageLatency > 100*time.Millisecond {
		issues = append(issues, fmt.Sprintf("High latency: %v", globalMetrics.AverageLatency))
	}

	// Check operations per second
	if globalMetrics.OperationsPerSec < 100 {
		issues = append(issues, fmt.Sprintf("Low operations per second: %d", globalMetrics.OperationsPerSec))
	}

	if len(issues) == 0 {
		result.Status = DiagnosticStatusPass
		result.Message = "Performance metrics are good"
	} else if len(issues) <= 1 {
		result.Status = DiagnosticStatusWarn
		result.Message = "Some performance issues detected"
	} else {
		result.Status = DiagnosticStatusFail
		result.Message = "Multiple performance issues detected"
	}

	result.Details = map[string]interface{}{
		"throughput_bytes_per_sec": globalMetrics.ThroughputBytesPerSec,
		"average_latency":          globalMetrics.AverageLatency.String(),
		"operations_per_sec":       globalMetrics.OperationsPerSec,
		"issues":                   issues,
	}

	return result
}

// checkBackpressureStatus checks backpressure status
func (dr *DiagnosticRunner) checkBackpressureStatus() DiagnosticResult {
	startTime := time.Now()

	globalMetrics := dr.metricsCollector.GetGlobalMetrics()
	streamMetrics := dr.metricsCollector.GetAllStreamMetrics()

	result := DiagnosticResult{
		CheckName: "Backpressure Status",
		Timestamp: startTime,
		Duration:  time.Since(startTime),
	}

	activeBackpressureStreams := 0
	for _, metrics := range streamMetrics {
		if metrics.BackpressureActive {
			activeBackpressureStreams++
		}
	}

	backpressureRatio := 0.0
	if len(streamMetrics) > 0 {
		backpressureRatio = float64(activeBackpressureStreams) / float64(len(streamMetrics))
	}

	if backpressureRatio == 0 {
		result.Status = DiagnosticStatusPass
		result.Message = "No backpressure detected"
	} else if backpressureRatio < 0.3 {
		result.Status = DiagnosticStatusWarn
		result.Message = fmt.Sprintf("Some streams (%d) under backpressure", activeBackpressureStreams)
	} else {
		result.Status = DiagnosticStatusFail
		result.Message = fmt.Sprintf("Many streams (%d) under backpressure", activeBackpressureStreams)
	}

	result.Details = map[string]interface{}{
		"total_streams":               len(streamMetrics),
		"active_backpressure_streams": activeBackpressureStreams,
		"backpressure_ratio":          backpressureRatio,
		"total_backpressure_events":   globalMetrics.BackpressureEvents,
	}

	return result
}

// DefaultConfigValidator provides default configuration validation
type DefaultConfigValidator struct{}

// ValidateConfig validates a configuration
func (dcv *DefaultConfigValidator) ValidateConfig(config *PresentationConfig) error {
	return config.Validate()
}

// ValidateUpdate validates a configuration update
func (dcv *DefaultConfigValidator) ValidateUpdate(current *PresentationConfig, update ConfigurationUpdate) error {
	// Validate individual fields
	if update.ReceiveWindowSize != nil {
		if *update.ReceiveWindowSize == 0 {
			return fmt.Errorf("receive window size cannot be zero")
		}
		if *update.ReceiveWindowSize > 1024*1024*1024 { // 1GB limit
			return fmt.Errorf("receive window size too large: %d", *update.ReceiveWindowSize)
		}
	}

	if update.BackpressureThreshold != nil {
		if *update.BackpressureThreshold <= 0 || *update.BackpressureThreshold > 1 {
			return fmt.Errorf("backpressure threshold must be between 0 and 1")
		}
	}

	if update.StreamBufferSize != nil {
		if *update.StreamBufferSize == 0 {
			return fmt.Errorf("stream buffer size cannot be zero")
		}
		if *update.StreamBufferSize > 100*1024*1024 { // 100MB limit
			return fmt.Errorf("stream buffer size too large: %d", *update.StreamBufferSize)
		}
	}

	if update.GapTimeout != nil {
		if *update.GapTimeout <= 0 {
			return fmt.Errorf("gap timeout must be positive")
		}
		if *update.GapTimeout > 60*time.Second {
			return fmt.Errorf("gap timeout too large: %v", *update.GapTimeout)
		}
	}

	return nil
}

// FileConfigPersistor provides file-based configuration persistence
type FileConfigPersistor struct {
	configPath  string
	historyPath string
}

// NewFileConfigPersistor creates a new file-based config persistor
func NewFileConfigPersistor(configPath, historyPath string) *FileConfigPersistor {
	return &FileConfigPersistor{
		configPath:  configPath,
		historyPath: historyPath,
	}
}

// SaveConfig saves configuration to file
func (fcp *FileConfigPersistor) SaveConfig(config *PresentationConfig) error {
	// In a real implementation, this would write to file
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	// Write to file (simplified)
	_ = data

	return nil
}

// LoadConfig loads configuration from file
func (fcp *FileConfigPersistor) LoadConfig() (*PresentationConfig, error) {
	// In a real implementation, this would read from file
	// For now, return default config
	return DefaultPresentationConfig(), nil
}

// SaveHistory saves configuration history to file
func (fcp *FileConfigPersistor) SaveHistory(history []ConfigurationUpdate) error {
	// In a real implementation, this would write to file
	data, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return err
	}

	// Write to file (simplified)
	_ = data

	return nil
}

// LoadHistory loads configuration history from file
func (fcp *FileConfigPersistor) LoadHistory() ([]ConfigurationUpdate, error) {
	// In a real implementation, this would read from file
	// For now, return empty history
	return make([]ConfigurationUpdate, 0), nil
}

// SimpleAuthProvider provides simple authentication and authorization
type SimpleAuthProvider struct {
	users map[string]AuthContext
}

// NewSimpleAuthProvider creates a new simple auth provider
func NewSimpleAuthProvider() *SimpleAuthProvider {
	return &SimpleAuthProvider{
		users: map[string]AuthContext{
			"admin": {
				UserID:      "admin",
				Roles:       []string{"admin"},
				Permissions: []string{"*"},
				ExpiresAt:   time.Now().Add(24 * time.Hour),
			},
			"operator": {
				UserID:      "operator",
				Roles:       []string{"operator"},
				Permissions: []string{"read", "metrics", "diagnostics"},
				ExpiresAt:   time.Now().Add(8 * time.Hour),
			},
		},
	}
}

// Authenticate authenticates a user
func (sap *SimpleAuthProvider) Authenticate(credentials map[string]interface{}) (AuthContext, error) {
	username, ok := credentials["username"].(string)
	if !ok {
		return AuthContext{}, fmt.Errorf("username required")
	}

	password, ok := credentials["password"].(string)
	if !ok {
		return AuthContext{}, fmt.Errorf("password required")
	}

	// Simple password check (in production, use proper hashing)
	if password != "password" {
		return AuthContext{}, fmt.Errorf("invalid credentials")
	}

	authCtx, exists := sap.users[username]
	if !exists {
		return AuthContext{}, fmt.Errorf("user not found")
	}

	if time.Now().After(authCtx.ExpiresAt) {
		return AuthContext{}, fmt.Errorf("credentials expired")
	}

	return authCtx, nil
}

// Authorize authorizes a command
func (sap *SimpleAuthProvider) Authorize(ctx AuthContext, command AdminCommandType) error {
	// Check if user has admin role (can do everything)
	for _, role := range ctx.Roles {
		if role == "admin" {
			return nil
		}
	}

	// Check specific permissions
	for _, permission := range ctx.Permissions {
		if permission == "*" {
			return nil
		}

		// Map commands to required permissions
		switch command {
		case AdminCommandGetConfig, AdminCommandGetMetrics, AdminCommandGetStreamInfo, AdminCommandGetSystemHealth:
			if permission == "read" {
				return nil
			}
		case AdminCommandRunDiagnostics:
			if permission == "diagnostics" {
				return nil
			}
		case AdminCommandExportMetrics:
			if permission == "metrics" {
				return nil
			}
		}
	}

	return fmt.Errorf("insufficient permissions for command: %v", command)
}

// RESTHandler provides REST API endpoints for administration
type RESTHandler struct {
	adminInterface *AdminInterface
	authProvider   AuthProvider
}

// NewRESTHandler creates a new REST handler
func NewRESTHandler(adminInterface *AdminInterface, authProvider AuthProvider) *RESTHandler {
	return &RESTHandler{
		adminInterface: adminInterface,
		authProvider:   authProvider,
	}
}

// HandleRequest handles HTTP requests (simplified interface)
func (rh *RESTHandler) HandleRequest(method, path string, body io.Reader, headers map[string]string) (int, []byte, error) {
	// Extract authentication
	_, ok := headers["Authorization"]
	if !ok {
		return 401, []byte(`{"error": "authentication required"}`), nil
	}

	// Simple token extraction (in production, use proper JWT or similar)
	credentials := map[string]interface{}{
		"username": "admin", // Would extract from token
		"password": "password",
	}

	authCtx, err := rh.authProvider.Authenticate(credentials)
	if err != nil {
		return 401, []byte(fmt.Sprintf(`{"error": "authentication failed: %v"}`, err)), nil
	}

	// Route request
	switch {
	case method == "GET" && path == "/api/config":
		return rh.handleGetConfig(authCtx)
	case method == "PUT" && path == "/api/config":
		return rh.handleUpdateConfig(authCtx, body)
	case method == "GET" && path == "/api/metrics":
		return rh.handleGetMetrics(authCtx)
	case method == "GET" && path == "/api/health":
		return rh.handleGetHealth(authCtx)
	case method == "POST" && path == "/api/diagnostics":
		return rh.handleRunDiagnostics(authCtx)
	default:
		return 404, []byte(`{"error": "endpoint not found"}`), nil
	}
}

// handleGetConfig handles GET /api/config
func (rh *RESTHandler) handleGetConfig(authCtx AuthContext) (int, []byte, error) {
	command := AdminCommand{
		ID:          fmt.Sprintf("get_config_%d", time.Now().Unix()),
		Type:        AdminCommandGetConfig,
		RequestedBy: authCtx.UserID,
		RequestedAt: time.Now(),
	}

	response, err := rh.adminInterface.ExecuteCommand(command, authCtx)
	if err != nil {
		return 500, []byte(fmt.Sprintf(`{"error": "%v"}`, err)), nil
	}

	if !response.Success {
		return 400, []byte(fmt.Sprintf(`{"error": "%s"}`, response.Error)), nil
	}

	data, err := json.Marshal(response.Result)
	if err != nil {
		return 500, []byte(fmt.Sprintf(`{"error": "serialization failed: %v"}`, err)), nil
	}

	return 200, data, nil
}

// handleUpdateConfig handles PUT /api/config
func (rh *RESTHandler) handleUpdateConfig(authCtx AuthContext, body io.Reader) (int, []byte, error) {
	// Parse request body
	var params map[string]interface{}
	if err := json.NewDecoder(body).Decode(&params); err != nil {
		return 400, []byte(fmt.Sprintf(`{"error": "invalid JSON: %v"}`, err)), nil
	}

	command := AdminCommand{
		ID:          fmt.Sprintf("update_config_%d", time.Now().Unix()),
		Type:        AdminCommandUpdateConfig,
		Parameters:  params,
		RequestedBy: authCtx.UserID,
		RequestedAt: time.Now(),
	}

	response, err := rh.adminInterface.ExecuteCommand(command, authCtx)
	if err != nil {
		return 500, []byte(fmt.Sprintf(`{"error": "%v"}`, err)), nil
	}

	if !response.Success {
		return 400, []byte(fmt.Sprintf(`{"error": "%s"}`, response.Error)), nil
	}

	return 200, []byte(`{"success": true}`), nil
}

// handleGetMetrics handles GET /api/metrics
func (rh *RESTHandler) handleGetMetrics(authCtx AuthContext) (int, []byte, error) {
	command := AdminCommand{
		ID:          fmt.Sprintf("get_metrics_%d", time.Now().Unix()),
		Type:        AdminCommandGetMetrics,
		RequestedBy: authCtx.UserID,
		RequestedAt: time.Now(),
	}

	response, err := rh.adminInterface.ExecuteCommand(command, authCtx)
	if err != nil {
		return 500, []byte(fmt.Sprintf(`{"error": "%v"}`, err)), nil
	}

	if !response.Success {
		return 400, []byte(fmt.Sprintf(`{"error": "%s"}`, response.Error)), nil
	}

	data, err := json.Marshal(response.Result)
	if err != nil {
		return 500, []byte(fmt.Sprintf(`{"error": "serialization failed: %v"}`, err)), nil
	}

	return 200, data, nil
}

// handleGetHealth handles GET /api/health
func (rh *RESTHandler) handleGetHealth(authCtx AuthContext) (int, []byte, error) {
	command := AdminCommand{
		ID:          fmt.Sprintf("get_health_%d", time.Now().Unix()),
		Type:        AdminCommandGetSystemHealth,
		RequestedBy: authCtx.UserID,
		RequestedAt: time.Now(),
	}

	response, err := rh.adminInterface.ExecuteCommand(command, authCtx)
	if err != nil {
		return 500, []byte(fmt.Sprintf(`{"error": "%v"}`, err)), nil
	}

	if !response.Success {
		return 400, []byte(fmt.Sprintf(`{"error": "%s"}`, response.Error)), nil
	}

	data, err := json.Marshal(response.Result)
	if err != nil {
		return 500, []byte(fmt.Sprintf(`{"error": "serialization failed: %v"}`, err)), nil
	}

	return 200, data, nil
}

// handleRunDiagnostics handles POST /api/diagnostics
func (rh *RESTHandler) handleRunDiagnostics(authCtx AuthContext) (int, []byte, error) {
	command := AdminCommand{
		ID:          fmt.Sprintf("run_diagnostics_%d", time.Now().Unix()),
		Type:        AdminCommandRunDiagnostics,
		RequestedBy: authCtx.UserID,
		RequestedAt: time.Now(),
		Timeout:     60 * time.Second, // Diagnostics can take longer
	}

	response, err := rh.adminInterface.ExecuteCommand(command, authCtx)
	if err != nil {
		return 500, []byte(fmt.Sprintf(`{"error": "%v"}`, err)), nil
	}

	if !response.Success {
		return 400, []byte(fmt.Sprintf(`{"error": "%s"}`, response.Error)), nil
	}

	data, err := json.Marshal(response.Result)
	if err != nil {
		return 500, []byte(fmt.Sprintf(`{"error": "serialization failed: %v"}`, err)), nil
	}

	return 200, data, nil
}
