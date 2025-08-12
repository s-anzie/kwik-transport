package server

import (
	"context"
	"fmt"
	"ftpa/internal/types"
	"sync"
	"time"
)

// ServerFallbackManager handles fallback logic between primary and secondary servers
type ServerFallbackManager struct {
	mu                    sync.RWMutex
	primaryServer         ServerConnection    // Primary server connection
	secondaryServers      []ServerConnection  // List of secondary servers
	activeServer          ServerConnection    // Currently active server
	serverHealth          map[string]*ServerHealth // Health status of each server
	fallbackConfig        *FallbackConfig     // Configuration for fallback behavior
	healthCheckInterval   time.Duration       // How often to check server health
	ctx                   context.Context     // Context for cancellation
	cancel                context.CancelFunc  // Cancel function
	healthCheckTicker     *time.Ticker        // Ticker for health checks
	fallbackCallbacks     []FallbackCallback  // Callbacks for fallback events
	currentServerIndex    int                 // Index of current server in fallback chain
	lastFallbackTime      time.Time           // When the last fallback occurred
	fallbackHistory       []FallbackEvent     // History of fallback events
}

// ServerConnection represents a connection to a file transfer server
type ServerConnection interface {
	GetServerID() string
	GetServerAddress() string
	IsHealthy() bool
	GetLastActivity() time.Time
	SendChunkRequest(request *types.ChunkRetryRequest) error
	GetConnectionStatus() ConnectionStatus
	Close() error
}

// ServerHealth tracks the health status of a server
type ServerHealth struct {
	ServerID          string        `json:"server_id"`
	Address           string        `json:"address"`
	IsHealthy         bool          `json:"is_healthy"`
	LastHealthCheck   time.Time     `json:"last_health_check"`
	LastActivity      time.Time     `json:"last_activity"`
	ConsecutiveFailures int         `json:"consecutive_failures"`
	ResponseTime      time.Duration `json:"response_time"`
	ErrorCount        int           `json:"error_count"`
	Status            ServerStatus  `json:"status"`
}

// ServerStatus represents the current status of a server
type ServerStatus int

const (
	ServerStatusHealthy ServerStatus = iota
	ServerStatusDegraded
	ServerStatusUnhealthy
	ServerStatusUnreachable
	ServerStatusMaintenance
)

// ConnectionStatus represents the status of a connection to a server
type ConnectionStatus int

const (
	ConnectionStatusConnected ConnectionStatus = iota
	ConnectionStatusConnecting
	ConnectionStatusDisconnected
	ConnectionStatusError
	ConnectionStatusTimeout
)

// FallbackConfig contains configuration for fallback behavior
type FallbackConfig struct {
	MaxConsecutiveFailures int           `json:"max_consecutive_failures"` // Max failures before fallback
	HealthCheckInterval    time.Duration `json:"health_check_interval"`    // How often to check health
	FallbackTimeout        time.Duration `json:"fallback_timeout"`         // Timeout for fallback operations
	RetryInterval          time.Duration `json:"retry_interval"`           // Interval between retry attempts
	MaxFallbackAttempts    int           `json:"max_fallback_attempts"`    // Max fallback attempts before giving up
	PreferPrimary          bool          `json:"prefer_primary"`           // Whether to prefer primary server
	AutoRecovery           bool          `json:"auto_recovery"`            // Whether to automatically recover to primary
	RecoveryCheckInterval  time.Duration `json:"recovery_check_interval"`  // How often to check for recovery
}

// FallbackCallback is called when a fallback event occurs
type FallbackCallback func(event *FallbackEvent) error

// FallbackEvent represents a fallback event
type FallbackEvent struct {
	EventType     FallbackEventType `json:"event_type"`
	FromServer    string            `json:"from_server"`
	ToServer      string            `json:"to_server"`
	Reason        string            `json:"reason"`
	Timestamp     time.Time         `json:"timestamp"`
	Success       bool              `json:"success"`
	ErrorMessage  string            `json:"error_message,omitempty"`
	ResponseTime  time.Duration     `json:"response_time"`
}

// FallbackEventType represents the type of fallback event
type FallbackEventType int

const (
	FallbackEventServerFailure FallbackEventType = iota
	FallbackEventServerRecovery
	FallbackEventManualSwitch
	FallbackEventHealthCheck
	FallbackEventTimeout
)

// DefaultFallbackConfig returns a default fallback configuration
func DefaultFallbackConfig() *FallbackConfig {
	return &FallbackConfig{
		MaxConsecutiveFailures: 3,
		HealthCheckInterval:    30 * time.Second,
		FallbackTimeout:        10 * time.Second,
		RetryInterval:          5 * time.Second,
		MaxFallbackAttempts:    5,
		PreferPrimary:          true,
		AutoRecovery:           true,
		RecoveryCheckInterval:  60 * time.Second,
	}
}

// NewServerFallbackManager creates a new server fallback manager
func NewServerFallbackManager(primary ServerConnection, secondaries []ServerConnection, config *FallbackConfig) *ServerFallbackManager {
	if config == nil {
		config = DefaultFallbackConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())

	manager := &ServerFallbackManager{
		primaryServer:         primary,
		secondaryServers:      secondaries,
		activeServer:          primary, // Start with primary
		serverHealth:          make(map[string]*ServerHealth),
		fallbackConfig:        config,
		healthCheckInterval:   config.HealthCheckInterval,
		ctx:                   ctx,
		cancel:                cancel,
		healthCheckTicker:     time.NewTicker(config.HealthCheckInterval),
		fallbackCallbacks:     make([]FallbackCallback, 0),
		currentServerIndex:    0, // 0 = primary, 1+ = secondary servers
		lastFallbackTime:      time.Now(),
		fallbackHistory:       make([]FallbackEvent, 0),
	}

	// Initialize server health tracking
	manager.initializeServerHealth()

	// Start health checking goroutine
	go manager.healthChecker()

	return manager
}

// initializeServerHealth initializes health tracking for all servers
func (sfm *ServerFallbackManager) initializeServerHealth() {
	// Initialize primary server health
	sfm.serverHealth[sfm.primaryServer.GetServerID()] = &ServerHealth{
		ServerID:            sfm.primaryServer.GetServerID(),
		Address:             sfm.primaryServer.GetServerAddress(),
		IsHealthy:           true,
		LastHealthCheck:     time.Now(),
		LastActivity:        time.Now(),
		ConsecutiveFailures: 0,
		ResponseTime:        0,
		ErrorCount:          0,
		Status:              ServerStatusHealthy,
	}

	// Initialize secondary servers health
	for _, server := range sfm.secondaryServers {
		sfm.serverHealth[server.GetServerID()] = &ServerHealth{
			ServerID:            server.GetServerID(),
			Address:             server.GetServerAddress(),
			IsHealthy:           true,
			LastHealthCheck:     time.Now(),
			LastActivity:        time.Now(),
			ConsecutiveFailures: 0,
			ResponseTime:        0,
			ErrorCount:          0,
			Status:              ServerStatusHealthy,
		}
	}
}

// GetActiveServer returns the currently active server
func (sfm *ServerFallbackManager) GetActiveServer() ServerConnection {
	sfm.mu.RLock()
	defer sfm.mu.RUnlock()
	return sfm.activeServer
}

// SendChunkRequest sends a chunk request through the active server with fallback
func (sfm *ServerFallbackManager) SendChunkRequest(request *types.ChunkRetryRequest) error {
	sfm.mu.Lock()
	defer sfm.mu.Unlock()

	startTime := time.Now()
	var lastError error

	// Try current active server first
	err := sfm.activeServer.SendChunkRequest(request)
	if err == nil {
		// Success - update health
		sfm.updateServerHealth(sfm.activeServer.GetServerID(), true, time.Since(startTime), nil)
		return nil
	}

	// Active server failed - record failure and try fallback
	sfm.updateServerHealth(sfm.activeServer.GetServerID(), false, time.Since(startTime), err)
	lastError = err

	// Check if we should trigger fallback
	if sfm.shouldTriggerFallback(sfm.activeServer.GetServerID()) {
		fallbackErr := sfm.performFallback(fmt.Sprintf("chunk request failed: %v", err))
		if fallbackErr != nil {
			return fmt.Errorf("fallback failed: %v, original error: %v", fallbackErr, err)
		}

		// Retry with new active server
		err = sfm.activeServer.SendChunkRequest(request)
		if err == nil {
			sfm.updateServerHealth(sfm.activeServer.GetServerID(), true, time.Since(startTime), nil)
			return nil
		}

		sfm.updateServerHealth(sfm.activeServer.GetServerID(), false, time.Since(startTime), err)
		lastError = err
	}

	return fmt.Errorf("all servers failed, last error: %v", lastError)
}

// shouldTriggerFallback determines if fallback should be triggered for a server
func (sfm *ServerFallbackManager) shouldTriggerFallback(serverID string) bool {
	health, exists := sfm.serverHealth[serverID]
	if !exists {
		return true // Unknown server, trigger fallback
	}

	return health.ConsecutiveFailures >= sfm.fallbackConfig.MaxConsecutiveFailures
}

// performFallback performs fallback to the next available server
func (sfm *ServerFallbackManager) performFallback(reason string) error {
	fromServer := sfm.activeServer.GetServerID()
	
	// Find next healthy server
	nextServer, nextIndex, err := sfm.findNextHealthyServer()
	if err != nil {
		event := &FallbackEvent{
			EventType:    FallbackEventServerFailure,
			FromServer:   fromServer,
			ToServer:     "",
			Reason:       reason,
			Timestamp:    time.Now(),
			Success:      false,
			ErrorMessage: err.Error(),
			ResponseTime: 0,
		}
		sfm.recordFallbackEvent(event)
		return err
	}

	// Perform the switch
	startTime := time.Now()
	sfm.activeServer = nextServer
	sfm.currentServerIndex = nextIndex
	sfm.lastFallbackTime = time.Now()

	// Record successful fallback event
	event := &FallbackEvent{
		EventType:    FallbackEventServerFailure,
		FromServer:   fromServer,
		ToServer:     nextServer.GetServerID(),
		Reason:       reason,
		Timestamp:    time.Now(),
		Success:      true,
		ResponseTime: time.Since(startTime),
	}
	sfm.recordFallbackEvent(event)

	// Notify callbacks
	sfm.notifyFallbackCallbacks(event)

	return nil
}

// findNextHealthyServer finds the next healthy server in the fallback chain
func (sfm *ServerFallbackManager) findNextHealthyServer() (ServerConnection, int, error) {
	allServers := []ServerConnection{sfm.primaryServer}
	allServers = append(allServers, sfm.secondaryServers...)

	// Try servers starting from the next one in the chain
	for i := 1; i <= len(allServers); i++ {
		nextIndex := (sfm.currentServerIndex + i) % len(allServers)
		server := allServers[nextIndex]
		
		if sfm.isServerHealthy(server.GetServerID()) {
			return server, nextIndex, nil
		}
	}

	return nil, -1, fmt.Errorf("no healthy servers available")
}

// isServerHealthy checks if a server is considered healthy
func (sfm *ServerFallbackManager) isServerHealthy(serverID string) bool {
	health, exists := sfm.serverHealth[serverID]
	if !exists {
		return false
	}

	return health.IsHealthy && health.Status == ServerStatusHealthy
}

// updateServerHealth updates the health status of a server
func (sfm *ServerFallbackManager) updateServerHealth(serverID string, success bool, responseTime time.Duration, err error) {
	health, exists := sfm.serverHealth[serverID]
	if !exists {
		return
	}

	health.LastHealthCheck = time.Now()
	health.ResponseTime = responseTime

	if success {
		health.ConsecutiveFailures = 0
		health.IsHealthy = true
		health.Status = ServerStatusHealthy
		health.LastActivity = time.Now()
	} else {
		health.ConsecutiveFailures++
		health.ErrorCount++
		
		if health.ConsecutiveFailures >= sfm.fallbackConfig.MaxConsecutiveFailures {
			health.IsHealthy = false
			health.Status = ServerStatusUnhealthy
		} else {
			health.Status = ServerStatusDegraded
		}
	}
}

// healthChecker runs periodic health checks on all servers
func (sfm *ServerFallbackManager) healthChecker() {
	for {
		select {
		case <-sfm.ctx.Done():
			return
		case <-sfm.healthCheckTicker.C:
			sfm.performHealthChecks()
			if sfm.fallbackConfig.AutoRecovery {
				sfm.checkForRecovery()
			}
		}
	}
}

// performHealthChecks performs health checks on all servers
func (sfm *ServerFallbackManager) performHealthChecks() {
	sfm.mu.Lock()
	defer sfm.mu.Unlock()

	allServers := []ServerConnection{sfm.primaryServer}
	allServers = append(allServers, sfm.secondaryServers...)

	for _, server := range allServers {
		startTime := time.Now()
		isHealthy := server.IsHealthy()
		responseTime := time.Since(startTime)

		// Update server health based on the health check result
		health, exists := sfm.serverHealth[server.GetServerID()]
		if exists {
			health.LastHealthCheck = time.Now()
			health.ResponseTime = responseTime
			
			if isHealthy {
				health.ConsecutiveFailures = 0
				health.IsHealthy = true
				health.Status = ServerStatusHealthy
				health.LastActivity = time.Now()
			} else {
				health.ConsecutiveFailures++
				health.IsHealthy = false
				health.Status = ServerStatusUnhealthy
			}
		}

		// Record health check event
		event := &FallbackEvent{
			EventType:    FallbackEventHealthCheck,
			FromServer:   server.GetServerID(),
			ToServer:     server.GetServerID(),
			Reason:       "periodic health check",
			Timestamp:    time.Now(),
			Success:      isHealthy,
			ResponseTime: responseTime,
		}
		
		if !isHealthy {
			event.ErrorMessage = "server unhealthy"
		}
		
		sfm.recordFallbackEvent(event)
	}
}

// checkForRecovery checks if we can recover to a preferred server
func (sfm *ServerFallbackManager) checkForRecovery() {
	if !sfm.fallbackConfig.PreferPrimary {
		return
	}

	// If we're not on primary and primary is healthy, switch back
	if sfm.currentServerIndex != 0 && sfm.isServerHealthy(sfm.primaryServer.GetServerID()) {
		// Check if enough time has passed since last fallback
		if time.Since(sfm.lastFallbackTime) < sfm.fallbackConfig.RecoveryCheckInterval {
			return
		}

		fromServer := sfm.activeServer.GetServerID()
		sfm.activeServer = sfm.primaryServer
		sfm.currentServerIndex = 0
		sfm.lastFallbackTime = time.Now()

		// Record recovery event
		event := &FallbackEvent{
			EventType:   FallbackEventServerRecovery,
			FromServer:  fromServer,
			ToServer:    sfm.primaryServer.GetServerID(),
			Reason:      "automatic recovery to primary server",
			Timestamp:   time.Now(),
			Success:     true,
			ResponseTime: 0,
		}
		sfm.recordFallbackEvent(event)
		sfm.notifyFallbackCallbacks(event)
	}
}

// ManualSwitchToServer manually switches to a specific server
func (sfm *ServerFallbackManager) ManualSwitchToServer(serverID string) error {
	sfm.mu.Lock()
	defer sfm.mu.Unlock()

	var targetServer ServerConnection
	var targetIndex int

	// Find the target server
	if sfm.primaryServer.GetServerID() == serverID {
		targetServer = sfm.primaryServer
		targetIndex = 0
	} else {
		found := false
		for i, server := range sfm.secondaryServers {
			if server.GetServerID() == serverID {
				targetServer = server
				targetIndex = i + 1
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("server %s not found", serverID)
		}
	}

	// Check if target server is healthy
	if !sfm.isServerHealthy(serverID) {
		return fmt.Errorf("target server %s is not healthy", serverID)
	}

	fromServer := sfm.activeServer.GetServerID()
	sfm.activeServer = targetServer
	sfm.currentServerIndex = targetIndex
	sfm.lastFallbackTime = time.Now()

	// Record manual switch event
	event := &FallbackEvent{
		EventType:   FallbackEventManualSwitch,
		FromServer:  fromServer,
		ToServer:    serverID,
		Reason:      "manual server switch",
		Timestamp:   time.Now(),
		Success:     true,
		ResponseTime: 0,
	}
	sfm.recordFallbackEvent(event)
	sfm.notifyFallbackCallbacks(event)

	return nil
}

// AddFallbackCallback adds a callback for fallback events
func (sfm *ServerFallbackManager) AddFallbackCallback(callback FallbackCallback) {
	sfm.mu.Lock()
	defer sfm.mu.Unlock()
	sfm.fallbackCallbacks = append(sfm.fallbackCallbacks, callback)
}

// notifyFallbackCallbacks notifies all registered callbacks
func (sfm *ServerFallbackManager) notifyFallbackCallbacks(event *FallbackEvent) {
	for _, callback := range sfm.fallbackCallbacks {
		go func(cb FallbackCallback, evt *FallbackEvent) {
			cb(evt)
		}(callback, event)
	}
}

// recordFallbackEvent records a fallback event in history
func (sfm *ServerFallbackManager) recordFallbackEvent(event *FallbackEvent) {
	// Keep only last 100 events to prevent memory growth
	if len(sfm.fallbackHistory) >= 100 {
		sfm.fallbackHistory = sfm.fallbackHistory[1:]
	}
	sfm.fallbackHistory = append(sfm.fallbackHistory, *event)
}

// GetServerHealth returns the health status of all servers
func (sfm *ServerFallbackManager) GetServerHealth() map[string]*ServerHealth {
	sfm.mu.RLock()
	defer sfm.mu.RUnlock()

	// Return a copy to avoid race conditions
	healthCopy := make(map[string]*ServerHealth)
	for id, health := range sfm.serverHealth {
		healthCopy[id] = &ServerHealth{
			ServerID:            health.ServerID,
			Address:             health.Address,
			IsHealthy:           health.IsHealthy,
			LastHealthCheck:     health.LastHealthCheck,
			LastActivity:        health.LastActivity,
			ConsecutiveFailures: health.ConsecutiveFailures,
			ResponseTime:        health.ResponseTime,
			ErrorCount:          health.ErrorCount,
			Status:              health.Status,
		}
	}

	return healthCopy
}

// GetFallbackHistory returns the history of fallback events
func (sfm *ServerFallbackManager) GetFallbackHistory() []FallbackEvent {
	sfm.mu.RLock()
	defer sfm.mu.RUnlock()

	// Return a copy
	history := make([]FallbackEvent, len(sfm.fallbackHistory))
	copy(history, sfm.fallbackHistory)
	return history
}

// GetFallbackStatistics returns statistics about fallback operations
func (sfm *ServerFallbackManager) GetFallbackStatistics() *FallbackStatistics {
	sfm.mu.RLock()
	defer sfm.mu.RUnlock()

	stats := &FallbackStatistics{
		TotalFallbacks:    0,
		SuccessfulFallbacks: 0,
		FailedFallbacks:   0,
		CurrentServer:     sfm.activeServer.GetServerID(),
		LastFallbackTime:  sfm.lastFallbackTime,
		ServerHealthCount: make(map[ServerStatus]int),
	}

	// Count fallback events
	for _, event := range sfm.fallbackHistory {
		if event.EventType == FallbackEventServerFailure || event.EventType == FallbackEventManualSwitch {
			stats.TotalFallbacks++
			if event.Success {
				stats.SuccessfulFallbacks++
			} else {
				stats.FailedFallbacks++
			}
		}
	}

	// Count server health statuses
	for _, health := range sfm.serverHealth {
		stats.ServerHealthCount[health.Status]++
	}

	return stats
}

// FallbackStatistics contains statistics about fallback operations
type FallbackStatistics struct {
	TotalFallbacks      int                    `json:"total_fallbacks"`
	SuccessfulFallbacks int                    `json:"successful_fallbacks"`
	FailedFallbacks     int                    `json:"failed_fallbacks"`
	CurrentServer       string                 `json:"current_server"`
	LastFallbackTime    time.Time              `json:"last_fallback_time"`
	ServerHealthCount   map[ServerStatus]int   `json:"server_health_count"`
}

// ForceHealthCheck forces an immediate health check on all servers
func (sfm *ServerFallbackManager) ForceHealthCheck() {
	sfm.performHealthChecks()
}

// Close stops the fallback manager and cleans up resources
func (sfm *ServerFallbackManager) Close() error {
	sfm.cancel()
	sfm.healthCheckTicker.Stop()

	sfm.mu.Lock()
	defer sfm.mu.Unlock()

	// Clear callbacks and history
	sfm.fallbackCallbacks = nil
	sfm.fallbackHistory = nil

	return nil
}

// String methods for enums
func (s ServerStatus) String() string {
	switch s {
	case ServerStatusHealthy:
		return "HEALTHY"
	case ServerStatusDegraded:
		return "DEGRADED"
	case ServerStatusUnhealthy:
		return "UNHEALTHY"
	case ServerStatusUnreachable:
		return "UNREACHABLE"
	case ServerStatusMaintenance:
		return "MAINTENANCE"
	default:
		return "UNKNOWN"
	}
}

func (c ConnectionStatus) String() string {
	switch c {
	case ConnectionStatusConnected:
		return "CONNECTED"
	case ConnectionStatusConnecting:
		return "CONNECTING"
	case ConnectionStatusDisconnected:
		return "DISCONNECTED"
	case ConnectionStatusError:
		return "ERROR"
	case ConnectionStatusTimeout:
		return "TIMEOUT"
	default:
		return "UNKNOWN"
	}
}

func (f FallbackEventType) String() string {
	switch f {
	case FallbackEventServerFailure:
		return "SERVER_FAILURE"
	case FallbackEventServerRecovery:
		return "SERVER_RECOVERY"
	case FallbackEventManualSwitch:
		return "MANUAL_SWITCH"
	case FallbackEventHealthCheck:
		return "HEALTH_CHECK"
	case FallbackEventTimeout:
		return "TIMEOUT"
	default:
		return "UNKNOWN"
	}
}