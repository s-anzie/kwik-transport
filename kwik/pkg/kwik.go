package kwik

import (
	"context"
	"fmt"
	"sync"
	"time"

	"kwik/internal/utils"
	"kwik/pkg/control"
	"kwik/pkg/data"
	"kwik/pkg/session"
	"kwik/pkg/stream"
	"kwik/pkg/transport"
)

// KWIK represents the main KWIK protocol implementation
// It integrates all components into a cohesive system
type KWIK struct {
	// Core components
	sessionManager    *SessionManager
	pathManager       transport.PathManager
	controlPlane      control.ControlPlane
	dataPlane         data.DataPlane
	streamMultiplexer stream.LogicalStreamManagerInterface

	// Configuration
	config *Config

	// State management
	state  State
	mutex  sync.RWMutex
	ctx    context.Context
	cancel context.CancelFunc

	// Metrics and monitoring
	metrics *SystemMetrics
	logger  Logger
}

// Config holds configuration for the KWIK system
type Config struct {
	// Session configuration
	MaxSessions            int
	SessionIdleTimeout     time.Duration
	SessionCleanupInterval time.Duration

	// Path configuration
	MaxPathsPerSession      int
	PathHealthCheckInterval time.Duration
	PathFailureThreshold    int
	PathRecoveryTimeout     time.Duration

	// Stream configuration
	OptimalStreamsPerReal int
	MaxStreamsPerReal     int
	StreamCleanupInterval time.Duration

	// Performance configuration
	MaxPacketSize         uint32
	ReadBufferSize        int
	WriteBufferSize       int
	AggregationEnabled    bool
	LoadBalancingStrategy string

	// Logging and monitoring
	LogLevel        LogLevel
	MetricsEnabled  bool
	MetricsInterval time.Duration

	// Logging configuration
	Logging *LogConfig
}

// LogConfig holds logging configuration
type LogConfig struct {
	GlobalLevel LogLevel
	Format      string
	Output      string
	Components  map[string]LogLevel
}

// DefaultLogConfig returns default logging configuration
func DefaultLogConfig() *LogConfig {
	return &LogConfig{
		GlobalLevel: LogLevelInfo,
		Format:      "text",
		Output:      "stdout",
		Components:  make(map[string]LogLevel),
	}
}

// DefaultConfig returns default KWIK configuration
func DefaultConfig() *Config {
	return &Config{
		MaxSessions:             1000,
		SessionIdleTimeout:      30 * time.Minute,
		SessionCleanupInterval:  5 * time.Minute,
		MaxPathsPerSession:      utils.MaxPaths,
		PathHealthCheckInterval: utils.PathHealthCheckInterval,
		PathFailureThreshold:    3,
		PathRecoveryTimeout:     30 * time.Second,
		OptimalStreamsPerReal:   utils.OptimalLogicalStreamsPerReal,
		MaxStreamsPerReal:       utils.MaxLogicalStreamsPerReal,
		StreamCleanupInterval:   1 * time.Minute,
		MaxPacketSize:           utils.DefaultMaxPacketSize,
		ReadBufferSize:          utils.DefaultReadBufferSize,
		WriteBufferSize:         utils.DefaultWriteBufferSize,
		AggregationEnabled:      true,
		LoadBalancingStrategy:   "adaptive",
		LogLevel:                LogLevelInfo,
		MetricsEnabled:          true,
		MetricsInterval:         30 * time.Second,
		Logging:                 DefaultLogConfig(),
	}
}

// State represents the current state of the KWIK system
type State int

const (
	StateIdle State = iota
	StateInitializing
	StateActive
	StateShuttingDown
	StateClosed
)

// New creates a new KWIK instance with integrated components
func New(config *Config) (*KWIK, error) {
	if config == nil {
		config = DefaultConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Create enhanced logger with error recovery
	var logLevel LogLevel
	if config.Logging != nil {
		logLevel = config.Logging.GlobalLevel
	} else {
		logLevel = config.LogLevel
	}
	logger := NewEnhancedLogger(logLevel, "KWIK-SYSTEM")

	// Create metrics system
	var metrics *SystemMetrics
	if config.MetricsEnabled {
		metrics = NewSystemMetrics(config.MetricsInterval)
	}

	// Create path manager with integrated health monitoring
	pathManager := transport.NewPathManager()

	// Create control plane with message routing
	controlPlaneConfig := &control.ControlPlaneConfig{
		MaxFrameSize:      4096,
		FrameTimeout:      5 * time.Second,
		HeartbeatInterval: 5 * time.Second,
		MaxRetries:        3,
		EnableCompression: false,
		EnableEncryption:  false,
		BufferSize:        1024,
		ProcessingWorkers: 4,
	}
	controlPlane := control.NewControlPlane(controlPlaneConfig)

	// Create data plane with aggregation support
	dataPlaneConfig := &data.DataPlaneConfig{
		MaxPacketSize:      config.MaxPacketSize,
		AggregationEnabled: config.AggregationEnabled,
		BufferSize:         config.ReadBufferSize,
		ProcessingWorkers:  4,
	}
	dataPlane := data.NewDataPlane(dataPlaneConfig)

	// Create stream multiplexer with optimal ratios
	streamConfig := &stream.LogicalStreamConfig{
		DefaultReadBufferSize:  config.ReadBufferSize,
		DefaultWriteBufferSize: config.WriteBufferSize,
		MaxConcurrentStreams:   config.MaxStreamsPerReal * 100, // Scale based on max streams
		StreamIdleTimeout:      config.SessionIdleTimeout,
		NotificationTimeout:    30 * time.Second,
		NotificationRetries:    3,
	}
	streamMultiplexer := stream.NewLogicalStreamManager(controlPlane, nil, streamConfig)

	// Create session manager
	sessionManager := NewSessionManager(config, logger)

	kwik := &KWIK{
		sessionManager:    sessionManager,
		pathManager:       pathManager,
		controlPlane:      controlPlane,
		dataPlane:         dataPlane,
		streamMultiplexer: streamMultiplexer,
		config:            config,
		state:             StateIdle,
		ctx:               ctx,
		cancel:            cancel,
		metrics:           metrics,
		logger:            logger,
	}

	// Wire components together
	err := kwik.wireComponents()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to wire components: %w", err)
	}

	return kwik, nil
}

// wireComponents integrates all components with proper message routing
func (k *KWIK) wireComponents() error {
	k.logger.Info("Wiring KWIK components together")

	// Connect path manager with control and data planes
	err := k.connectPathManager()
	if err != nil {
		return fmt.Errorf("failed to connect path manager: %w", err)
	}

	// Connect control plane with session management
	err = k.connectControlPlane()
	if err != nil {
		return fmt.Errorf("failed to connect control plane: %w", err)
	}

	// Connect data plane with stream multiplexing
	err = k.connectDataPlane()
	if err != nil {
		return fmt.Errorf("failed to connect data plane: %w", err)
	}

	// Connect stream multiplexer with session management
	err = k.connectStreamMultiplexer()
	if err != nil {
		return fmt.Errorf("failed to connect stream multiplexer: %w", err)
	}

	// Set up cross-component communication
	err = k.setupCrossComponentCommunication()
	if err != nil {
		return fmt.Errorf("failed to setup cross-component communication: %w", err)
	}

	k.logger.Info("All KWIK components wired successfully")
	return nil
}

// connectPathManager integrates path management with control and data planes
func (k *KWIK) connectPathManager() error {
	// Set up path status notification handler for control plane
	pathStatusHandler := &PathStatusHandler{
		controlPlane: k.controlPlane,
		dataPlane:    k.dataPlane,
		logger:       k.logger,
	}

	k.pathManager.SetPathStatusNotificationHandler(pathStatusHandler)

	// Configure health monitoring
	k.pathManager.SetHealthCheckInterval(k.config.PathHealthCheckInterval)
	k.pathManager.SetFailureThreshold(k.config.PathFailureThreshold)

	// Start health monitoring
	return k.pathManager.StartHealthMonitoring()
}

// connectControlPlane integrates control plane with session management
func (k *KWIK) connectControlPlane() error {
	// Set up control message handlers
	controlHandlers := &ControlMessageHandlers{
		sessionManager: k.sessionManager,
		pathManager:    k.pathManager,
		dataPlane:      k.dataPlane,
		logger:         k.logger,
	}

	// Register handlers with control plane
	if controlPlaneWithHandlers, ok := k.controlPlane.(interface {
		SetMessageHandlers(handlers interface{}) error
	}); ok {
		return controlPlaneWithHandlers.SetMessageHandlers(controlHandlers)
	}

	return nil
}

// connectDataPlane integrates data plane with stream multiplexing
func (k *KWIK) connectDataPlane() error {
	// Configure data aggregation
	if k.config.AggregationEnabled {
		err := k.dataPlane.EnableAggregation(0) // Enable for all streams
		if err != nil {
			return fmt.Errorf("failed to enable data aggregation: %w", err)
		}
	}

	// Set up load balancing strategy (placeholder for future implementation)
	// TODO: Implement load balancing strategy configuration

	return nil
}

// connectStreamMultiplexer integrates stream multiplexer with session management
func (k *KWIK) connectStreamMultiplexer() error {
	// Configure optimal ratios
	if multiplexerWithConfig, ok := k.streamMultiplexer.(interface {
		SetOptimalRatio(ratio int) error
		SetMaxRatio(ratio int) error
	}); ok {
		err := multiplexerWithConfig.SetOptimalRatio(k.config.OptimalStreamsPerReal)
		if err != nil {
			return fmt.Errorf("failed to set optimal stream ratio: %w", err)
		}

		err = multiplexerWithConfig.SetMaxRatio(k.config.MaxStreamsPerReal)
		if err != nil {
			return fmt.Errorf("failed to set max stream ratio: %w", err)
		}
	}

	return nil
}

// setupCrossComponentCommunication establishes communication between components
func (k *KWIK) setupCrossComponentCommunication() error {
	// Create message router for inter-component communication
	messageRouter := &MessageRouter{
		controlPlane:      k.controlPlane,
		dataPlane:         k.dataPlane,
		sessionManager:    k.sessionManager,
		pathManager:       k.pathManager,
		streamMultiplexer: k.streamMultiplexer,
		logger:            k.logger,
	}

	// Set up bidirectional communication channels
	err := messageRouter.SetupRouting()
	if err != nil {
		return fmt.Errorf("failed to setup message routing: %w", err)
	}

	return nil
}

// Start initializes and starts the KWIK system
func (k *KWIK) Start() error {
	k.mutex.Lock()
	defer k.mutex.Unlock()

	if k.state != StateIdle {
		return fmt.Errorf("KWIK system is not in idle state")
	}

	k.state = StateInitializing
	k.logger.Info("Starting KWIK system")

	// Start metrics collection if enabled
	if k.metrics != nil {
		k.metrics.Start()
	}

	// Start session manager
	err := k.sessionManager.Start()
	if err != nil {
		k.state = StateIdle
		return fmt.Errorf("failed to start session manager: %w", err)
	}

	// Start background maintenance routines
	go k.runMaintenanceLoop()
	go k.runMetricsCollection()

	k.state = StateActive
	k.logger.Info("KWIK system started successfully")

	return nil
}

// Stop gracefully shuts down the KWIK system
func (k *KWIK) Stop() error {
	k.mutex.Lock()
	defer k.mutex.Unlock()

	if k.state != StateActive {
		return fmt.Errorf("KWIK system is not active")
	}

	k.state = StateShuttingDown
	k.logger.Info("Stopping KWIK system")

	// Cancel context to stop background routines
	k.cancel()

	// Stop session manager
	err := k.sessionManager.Stop()
	if err != nil {
		k.logger.Error("Failed to stop session manager", "error", err)
	}

	// Stop path manager health monitoring
	err = k.pathManager.StopHealthMonitoring()
	if err != nil {
		k.logger.Error("Failed to stop path health monitoring", "error", err)
	}

	// Close all components
	k.pathManager.Close()
	// TODO: Add Close() method to control plane interface
	// k.controlPlane.Close()
	k.dataPlane.Close()

	// Stop metrics collection
	if k.metrics != nil {
		k.metrics.Stop()
	}

	k.state = StateClosed
	k.logger.Info("KWIK system stopped successfully")

	return nil
}

// Dial creates a new client session with optional configuration (QUIC-compatible interface)
func Dial(ctx context.Context, address string, config *Config) (session.Session, error) {
	if config == nil {
		config = DefaultConfig()
	}

	// Create and start KWIK instance automatically
	kwikInstance, err := New(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create KWIK instance: %w", err)
	}

	// Start the system automatically
	err = kwikInstance.Start()
	if err != nil {
		return nil, fmt.Errorf("failed to start KWIK system: %w", err)
	}

	// Create session configuration from KWIK config
	sessionConfig := &session.SessionConfig{
		MaxPaths:              config.MaxPathsPerSession,
		OptimalStreamsPerReal: config.OptimalStreamsPerReal,
		MaxStreamsPerReal:     config.MaxStreamsPerReal,
		IdleTimeout:           config.SessionIdleTimeout,
		MaxPacketSize:         config.MaxPacketSize,
		EnableAggregation:     config.AggregationEnabled,
		EnableMigration:       true,
	}

	// Create client session through session manager
	clientSession, err := kwikInstance.sessionManager.CreateClientSession(ctx, address, sessionConfig)
	if err != nil {
		kwikInstance.Stop() // Clean up on failure
		return nil, fmt.Errorf("failed to create client session: %w", err)
	}

	// Update metrics
	if kwikInstance.metrics != nil {
		kwikInstance.metrics.IncrementSessionsCreated()
	}

	kwikInstance.logger.Info("Client session created", "address", address, "sessionID", clientSession.GetSessionID())

	// Wrap the session to handle cleanup when closed
	wrappedSession := &ClientSessionWrapper{
		Session:      clientSession,
		kwikInstance: kwikInstance,
	}

	return wrappedSession, nil
}

// Listen creates a new server listener with optional configuration (QUIC-compatible interface)
func Listen(address string, config *Config) (session.Listener, error) {
	if config == nil {
		config = DefaultConfig()
	}

	// Create and start KWIK instance automatically
	kwikInstance, err := New(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create KWIK instance: %w", err)
	}

	// Start the system automatically
	err = kwikInstance.Start()
	if err != nil {
		return nil, fmt.Errorf("failed to start KWIK system: %w", err)
	}

	// Create session configuration from KWIK config
	sessionConfig := &session.SessionConfig{
		MaxPaths:              config.MaxPathsPerSession,
		OptimalStreamsPerReal: config.OptimalStreamsPerReal,
		MaxStreamsPerReal:     config.MaxStreamsPerReal,
		IdleTimeout:           config.SessionIdleTimeout,
		MaxPacketSize:         config.MaxPacketSize,
		EnableAggregation:     config.AggregationEnabled,
		EnableMigration:       true,
	}

	// Create server listener through session manager
	listener, err := kwikInstance.sessionManager.CreateListener(address, sessionConfig)
	if err != nil {
		kwikInstance.Stop() // Clean up on failure
		return nil, fmt.Errorf("failed to create listener: %w", err)
	}

	kwikInstance.logger.Info("Server listener created", "address", address)

	// Wrap the listener to handle cleanup when closed
	wrappedListener := &ListenerWrapper{
		Listener:     listener,
		kwikInstance: kwikInstance,
	}

	return wrappedListener, nil
}

// ClientSessionWrapper wraps a session to handle KWIK instance cleanup
type ClientSessionWrapper struct {
	session.Session
	kwikInstance *KWIK
}

// Close closes the session and cleans up the KWIK instance
func (w *ClientSessionWrapper) Close() error {
	err := w.Session.Close()
	w.kwikInstance.Stop() // Clean up KWIK instance
	return err
}

// ListenerWrapper wraps a listener to handle KWIK instance cleanup
type ListenerWrapper struct {
	session.Listener
	kwikInstance *KWIK
}

// Close closes the listener and cleans up the KWIK instance
func (w *ListenerWrapper) Close() error {
	err := w.Listener.Close()
	w.kwikInstance.Stop() // Clean up KWIK instance
	return err
}

// GetMetrics returns current system metrics
func (k *KWIK) GetMetrics() *SystemMetrics {
	k.mutex.RLock()
	defer k.mutex.RUnlock()

	return k.metrics
}

// GetState returns the current state of the KWIK system
func (k *KWIK) GetState() State {
	k.mutex.RLock()
	defer k.mutex.RUnlock()

	return k.state
}

// runMaintenanceLoop runs periodic maintenance tasks
func (k *KWIK) runMaintenanceLoop() {
	ticker := time.NewTicker(k.config.SessionCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			k.performMaintenance()
		case <-k.ctx.Done():
			return
		}
	}
}

// performMaintenance performs periodic maintenance tasks
func (k *KWIK) performMaintenance() {
	// Clean up dead paths
	deadPathsRemoved := k.pathManager.CleanupDeadPaths()
	if deadPathsRemoved > 0 {
		k.logger.Debug("Cleaned up dead paths", "count", deadPathsRemoved)
	}

	// Clean up idle sessions
	idleSessionsRemoved := k.sessionManager.CleanupIdleSessions()
	if idleSessionsRemoved > 0 {
		k.logger.Debug("Cleaned up idle sessions", "count", idleSessionsRemoved)
	}

	// Update metrics
	if k.metrics != nil {
		k.metrics.SetActivePathCount(k.pathManager.GetActivePathCount())
		k.metrics.SetActiveSessionCount(k.sessionManager.GetActiveSessionCount())
	}
}

// runMetricsCollection runs periodic metrics collection
func (k *KWIK) runMetricsCollection() {
	if k.metrics == nil {
		return
	}

	ticker := time.NewTicker(k.config.MetricsInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			k.collectMetrics()
		case <-k.ctx.Done():
			return
		}
	}
}

// collectMetrics collects system metrics
func (k *KWIK) collectMetrics() {
	if k.metrics == nil {
		return
	}

	// Collect path metrics
	activePaths := k.pathManager.GetActivePaths()
	k.metrics.SetActivePathCount(len(activePaths))

	// Collect session metrics
	k.metrics.SetActiveSessionCount(k.sessionManager.GetActiveSessionCount())

	// Collect performance metrics from data plane
	// TODO: Implement data plane statistics collection
	// This will be implemented when the data plane interface is finalized
}

func (s State) String() string {
	switch s {
	case StateIdle:
		return "IDLE"
	case StateInitializing:
		return "INITIALIZING"
	case StateActive:
		return "ACTIVE"
	case StateShuttingDown:
		return "SHUTTING_DOWN"
	case StateClosed:
		return "CLOSED"
	default:
		return "UNKNOWN"
	}
}
