package session

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"kwik/internal/utils"
	controlpkg "kwik/pkg/control"
	"kwik/pkg/data"
	"kwik/pkg/presentation"
	"kwik/pkg/stream"
	"kwik/pkg/transport"
	"kwik/proto/control"

	"github.com/quic-go/quic-go"
	"google.golang.org/protobuf/proto"
)

// DefaultSessionLogger provides a simple logger implementation for session
type DefaultSessionLogger struct{}

func (d *DefaultSessionLogger) Debug(msg string, keysAndValues ...interface{}) {
	log.Printf("[DEBUG] %s %v", msg, keysAndValues)
}
func (d *DefaultSessionLogger) Info(msg string, keysAndValues ...interface{}) {}
func (d *DefaultSessionLogger) Warn(msg string, keysAndValues ...interface{}) {}
func (d *DefaultSessionLogger) Error(msg string, keysAndValues ...interface{}) {
	log.Printf("[ERROR] %s", msg)
}
func (d *DefaultSessionLogger) Critical(msg string, keysAndValues ...interface{}) {
	log.Printf("[CRITICAL] %s", msg)
}

// ClientSession implements the Session interface for KWIK clients
// It maintains QUIC compatibility while managing multiple paths internally
type ClientSession struct {
	sessionID   string
	pathManager transport.PathManager
	primaryPath transport.Path
	isClient    bool
	state       SessionState
	createdAt   time.Time

	// Authentication management
	authManager *AuthenticationManager

	// Stream management
	nextStreamID uint64
	streams      map[uint64]*stream.ClientStream
	streamsMutex sync.RWMutex

	// Track application streams to avoid internal ingestion conflicts
	appStreams map[uint64]bool

	// Secondary stream management
	secondaryStreamHandler stream.SecondaryStreamHandler
	streamAggregator       data.DataAggregator
	aggregator             *data.StreamAggregator
	metadataProtocol       *stream.MetadataProtocolImpl

	// Data presentation management
	dataPresentationManager *presentation.DataPresentationManagerImpl

	// Retransmission management
	retransmissionManager data.RetransmissionManager

	// Offset coordination management
	offsetCoordinator data.OffsetCoordinator

	// Health monitoring
	healthMonitor    ConnectionHealthMonitor
	heartbeatManager *HeartbeatManager

	// Control plane heartbeat system
	controlHeartbeatSystem controlpkg.ControlPlaneHeartbeatSystem

	// State management
	stateManager SessionStateManager

	// Resource management
	resourceManager *ResourceManager

	// Flow control management
	flowControlManager *FlowControlManager
	controlPlane       *SessionControlPlane

	// Channel for accepting incoming streams
	acceptChan chan *stream.ClientStream

	// Synchronization
	mutex  sync.RWMutex
	ctx    context.Context
	cancel context.CancelFunc

	// Configuration
	config *SessionConfig

	// Heartbeat management (now handled by health monitor)
	heartbeatSequence uint64

	// Logger for this session
	logger stream.StreamLogger

	// Metrics management
	metricsManager   *MetricsManager
	metricsCollector *MetricsCollector
}

// SessionConfig holds configuration for a KWIK session
type SessionConfig struct {
	MaxPaths              int
	OptimalStreamsPerReal int
	MaxStreamsPerReal     int
	IdleTimeout           time.Duration
	MaxPacketSize         uint32
	EnableAggregation     bool
	EnableMigration       bool
}

// DefaultSessionConfig returns default session configuration
func DefaultSessionConfig() *SessionConfig {
	return &SessionConfig{
		MaxPaths:              utils.MaxPaths,
		OptimalStreamsPerReal: utils.OptimalLogicalStreamsPerReal,
		MaxStreamsPerReal:     utils.MaxLogicalStreamsPerReal,
		IdleTimeout:           utils.DefaultKeepAliveInterval,
		MaxPacketSize:         utils.DefaultMaxPacketSize,
		EnableAggregation:     true,
		EnableMigration:       true,
	}
}

// NewClientSession creates a new KWIK client session
func NewClientSession(pathManager transport.PathManager, config *SessionConfig) *ClientSession {
	if config == nil {
		config = DefaultSessionConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Generate unique session ID
	sessionID := generateSessionID()

	// Create data presentation manager
	presentationConfig := presentation.DefaultPresentationConfig()
	presentationConfig.EnableDebugLogging = true
	dataPresentationManager := presentation.NewDataPresentationManager(presentationConfig)

	// Create retransmission manager
	retransmissionConfig := data.DefaultRetransmissionConfig()
	retransmissionConfig.Logger = &DefaultSessionLogger{}
	retransmissionManager := data.NewRetransmissionManager(retransmissionConfig)

	// Create offset coordinator
	offsetCoordinatorConfig := data.DefaultOffsetCoordinatorConfig()
	offsetCoordinatorConfig.Logger = &DefaultSessionLogger{}
	offsetCoordinator := data.NewOffsetCoordinator(offsetCoordinatorConfig)

	// Create health monitor
	healthMonitor := NewConnectionHealthMonitor(ctx)

	// Create heartbeat manager
	heartbeatConfig := DefaultHeartbeatConfig()
	heartbeatManager := NewHeartbeatManager(ctx, heartbeatConfig)

	// Create control plane heartbeat system (client mode)
	controlHeartbeatConfig := controlpkg.DefaultControlHeartbeatConfig()
	controlHeartbeatSystem := controlpkg.NewControlPlaneHeartbeatSystem(
		nil, // Will be set later when control plane is available
		controlHeartbeatConfig,
		&DefaultSessionLogger{},
	)
	controlHeartbeatSystem.SetServerMode(false) // Client mode

	// Create resource manager
	resourceManager := NewResourceManager(DefaultResourceConfig())

	// Create metrics manager
	metricsManager := NewMetricsManager(sessionID, DefaultMetricsConfig())
	metricsCollector := NewMetricsCollector(metricsManager, DefaultMetricsCollectorConfig())

	session := &ClientSession{
		sessionID:               sessionID,
		pathManager:             pathManager,
		isClient:                true,
		state:                   SessionStateConnecting,
		createdAt:               time.Now(),
		authManager:             NewAuthenticationManager(sessionID, true), // true = isClient
		nextStreamID:            1,                                         // Start stream IDs from 1 (QUIC-compatible)
		streams:                 make(map[uint64]*stream.ClientStream),
		appStreams:              make(map[uint64]bool),
		secondaryStreamHandler:  stream.NewSecondaryStreamHandler(nil), // Use default config
		streamAggregator:        data.NewDataAggregator(&DefaultSessionLogger{}),
		aggregator:              data.NewStreamAggregator(&DefaultSessionLogger{}),          // Initialize secondary stream aggregator
		metadataProtocol:        stream.NewMetadataProtocol(),                               // Initialize metadata protocol
		dataPresentationManager: dataPresentationManager,                                    // Initialize data presentation manager
		retransmissionManager:   retransmissionManager,                                      // Initialize retransmission manager
		offsetCoordinator:       offsetCoordinator,                                          // Initialize offset coordinator
		healthMonitor:           healthMonitor,                                              // Initialize health monitor
		heartbeatManager:        heartbeatManager,                                           // Initialize heartbeat manager
		controlHeartbeatSystem:  controlHeartbeatSystem,                                     // Initialize control heartbeat system
		stateManager:            NewSessionStateManager(sessionID, &DefaultSessionLogger{}), // Initialize state manager
		resourceManager:         resourceManager,                                            // Initialize resource manager
		acceptChan:              make(chan *stream.ClientStream, 100),                       // Buffered channel
		ctx:                     ctx,
		cancel:                  cancel,
		config:                  config,
		heartbeatSequence:       0,
		logger:                  &DefaultSessionLogger{},
		metricsManager:          metricsManager,
		metricsCollector:        metricsCollector,
	}

	// Wire the secondary aggregator to the presentation manager so reads use the same buffer path
	session.aggregator.SetPresentationManager(dataPresentationManager)

	// Propagate logger to sub-components that support it
	session.authManager.SetLogger(session.logger)
	session.dataPresentationManager.SetLogger(session.logger)

	// Set up retransmission manager callbacks
	session.setupRetransmissionCallbacks()

	// Set up offset coordinator callbacks
	session.setupOffsetCoordinatorCallbacks()

	// Set up flow control manager
	session.setupFlowControlManager()

	// Set up metrics collector with component references
	metricsCollector.SetSession(session)
	metricsCollector.SetHealthMonitor(healthMonitor)
	metricsCollector.SetRetransmissionManager(retransmissionManager)
	metricsCollector.SetOffsetCoordinator(offsetCoordinator)

	return session
}

// GetLogger returns the stream-compatible logger for this session
func (s *ClientSession) GetLogger() stream.StreamLogger {
	return s.logger
}

// GetSessionLogger returns the logger as SessionLogger interface for flow control
func (s *ClientSession) GetSessionLogger() SessionLogger {
	return s.logger
}

// GetMetrics returns the current session metrics
func (s *ClientSession) GetMetrics() *MetricsSummary {
	if s.metricsCollector == nil {
		return nil
	}
	return s.metricsCollector.GetMetricsSummary()
}

// GetMetricsReport returns a formatted metrics report
func (s *ClientSession) GetMetricsReport() string {
	if s.metricsCollector == nil {
		return "Metrics collection not enabled"
	}
	reporter := NewMetricsReporter(s.metricsCollector)
	return reporter.GenerateReport()
}

// SetLogger sets the stream-compatible logger for this session
func (s *ClientSession) SetLogger(l stream.StreamLogger) {
	s.logger = l
}

// Dial establishes a connection to the primary server (QUIC-compatible)
func Dial(ctx context.Context, address string, config *SessionConfig) (Session, error) {
	if config == nil {
		config = DefaultSessionConfig()
	}

	// Create path manager
	pathManager := transport.NewPathManager()

	// Create client session
	session := NewClientSession(pathManager, config)

	// Establish primary path with QUIC connection
	primaryPath, err := pathManager.CreatePath(address)
	if err != nil {
		session.Close() // Clean up session if path creation fails
		return nil, utils.NewKwikError(utils.ErrConnectionLost,
			fmt.Sprintf("failed to create primary path to %s", address), err)
	}

	session.primaryPath = primaryPath

	// Record primary path creation in metrics
	if session.metricsManager != nil {
		session.metricsManager.RecordPathCreated(primaryPath.ID(), address, true)
	}

	// CLIENT CREATES THE CONTROL STREAM (OpenStreamSync)
	// This MUST be the very first stream (ID 0)
	session.logger.Debug("Client attempting to create control stream...")
	controlStream, err := primaryPath.CreateControlStreamAsClient()
	if err != nil {
		session.logger.Debug(fmt.Sprintf("Client FAILED to create control stream: %v", err))
		session.Close() // Clean up on failure
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"failed to create control stream as client", err)
	}
	session.logger.Debug(fmt.Sprintf("Client successfully created control stream (ID=%d)", controlStream.StreamID()))

	// IMMEDIATELY send authentication after control stream creation
	// This ensures the server can read the authentication request right away
	err = session.performAuthentication(ctx)
	if err != nil {
		session.logger.Debug(fmt.Sprintf("Client authentication FAILED: %v", err))
		session.Close() // Clean up on authentication failure
		return nil, utils.NewKwikError(utils.ErrAuthenticationFailed,
			"authentication failed during primary path establishment", err)
	}

	// Mark primary path as default for operations (Requirement 2.5)
	err = session.markPrimaryPathAsDefault()
	if err != nil {
		session.Close() // Clean up on failure
		return nil, utils.NewKwikError(utils.ErrConnectionLost,
			"failed to mark primary path as default", err)
	}

	// Changer vers l'état actif après authentification réussie
	if err := session.stateManager.SetState(SessionStateActive); err != nil {
		session.Close() // Clean up on failure
		return nil, utils.NewKwikError(utils.ErrInvalidState,
			"failed to set active state", err)
	}
	session.state = SessionStateActive // Maintenir la compatibilité avec l'ancien champ

	// Start control plane heartbeats now that session is active
	if session.controlHeartbeatSystem != nil && session.primaryPath != nil {
		err := session.controlHeartbeatSystem.StartControlHeartbeats(session.sessionID, session.primaryPath.ID())
		if err != nil {
			session.logger.Error("Failed to start control heartbeats", "error", err)
			// Don't fail session creation for heartbeat issues, just log the error
		} else {
			session.logger.Debug("Control heartbeats started", "sessionID", session.sessionID, "pathID", session.primaryPath.ID())
		}
	}

	// Start the data presentation manager
	err = session.dataPresentationManager.Start()
	if err != nil {
		session.Close() // Clean up on failure
		return nil, utils.NewKwikError(utils.ErrConnectionLost,
			"failed to start data presentation manager", err)
	}

	// Start the retransmission manager
	err = session.startRetransmissionManager()
	if err != nil {
		session.Close() // Clean up on failure
		return nil, utils.NewKwikError(utils.ErrConnectionLost,
			"failed to start retransmission manager", err)
	}

	// Start the resource manager
	err = session.resourceManager.Start()
	if err != nil {
		session.Close() // Clean up on failure
		return nil, utils.NewKwikError(utils.ErrConnectionLost,
			"failed to start resource manager", err)
	}

	// Start heartbeat management for existing paths (APRÈS authentification complète)
	session.startHeartbeatManagement()

	// Start the health monitor (APRÈS authentification complète)
	err = session.startHealthMonitor()
	if err != nil {
		session.Close() // Clean up on failure
		return nil, utils.NewKwikError(utils.ErrConnectionLost,
			"failed to start health monitor", err)
	}

	// Start the offset coordinator
	err = session.startOffsetCoordinator()
	if err != nil {
		session.Close() // Clean up on failure
		return nil, utils.NewKwikError(utils.ErrConnectionLost,
			"failed to start offset coordinator", err)
	}

	// Start the flow control manager
	err = session.startFlowControlManager()
	if err != nil {
		session.Close() // Clean up on failure
		return nil, utils.NewKwikError(utils.ErrConnectionLost,
			"failed to start flow control manager", err)
	}

	// Start health monitoring for automatic failure detection
	err = pathManager.StartHealthMonitoring()
	if err != nil {
		session.Close() // Clean up on failure
		return nil, utils.NewKwikError(utils.ErrConnectionLost,
			"failed to start health monitoring", err)
	}

	// Set up path status notification handler for the session
	pathManager.SetPathStatusNotificationHandler(session)

	// Start metrics collection
	err = session.metricsCollector.Start()
	if err != nil {
		session.Close() // Clean up on failure
		return nil, utils.NewKwikError(utils.ErrConnectionLost,
			"failed to start metrics collection", err)
	}

	// Start session management goroutines
	go session.managePaths()
	// NOTE: We start handleIncomingStreams AFTER authentication is complete
	// to avoid the control stream being blocked by the Read() in handleControlFrames
	go session.handleIncomingStreams()

	// Heartbeat management is now handled by the health monitor

	return session, nil
}

// OpenStreamSync opens a new stream synchronously (QUIC-compatible)
func (s *ClientSession) OpenStreamSync(ctx context.Context) (Stream, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.state != SessionStateActive {
		return nil, utils.NewKwikError(utils.ErrConnectionLost, "session is not active", nil)
	}

	// Generate new stream ID
	streamID := s.nextStreamID
	s.nextStreamID++

	// Check resource availability and allocate resources
	if s.resourceManager != nil {
		_, err := s.resourceManager.AllocateStreamResources(s.sessionID, streamID)
		if err != nil {
			return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
				fmt.Sprintf("failed to allocate resources for stream %d", streamID), err)
		}
	}

	// Ensure primary path is available and active (Requirement 2.5)
	if s.primaryPath == nil {
		// Release allocated resources on failure
		if s.resourceManager != nil {
			s.resourceManager.ReleaseStreamResources(s.sessionID, streamID)
		}
		return nil, utils.NewKwikError(utils.ErrConnectionLost, "no primary path available", nil)
	}

	if !s.primaryPath.IsActive() {
		// Release allocated resources on failure
		if s.resourceManager != nil {
			s.resourceManager.ReleaseStreamResources(s.sessionID, streamID)
		}
		return nil, utils.NewKwikError(utils.ErrPathDead, "primary path is not active", nil)
	}

	// Create actual QUIC stream on primary path
	s.logger.Debug("OpenStreamSync: Getting connection from primary path", "pathID", s.primaryPath.ID())
	conn := s.primaryPath.GetConnection()
	if conn == nil {
		// Release allocated resources on failure
		if s.resourceManager != nil {
			s.resourceManager.ReleaseStreamResources(s.sessionID, streamID)
		}
		return nil, utils.NewKwikError(utils.ErrConnectionLost, "no QUIC connection available", nil)
	}

	s.logger.Debug("OpenStreamSync: About to call conn.OpenStreamSync", "pathID", s.primaryPath.ID())
	quicStream, err := conn.OpenStreamSync(ctx)
	s.logger.Debug("OpenStreamSync: conn.OpenStreamSync returned", "pathID", s.primaryPath.ID(), "error", err)
	if err != nil {
		// Release allocated resources on failure
		if s.resourceManager != nil {
			s.resourceManager.ReleaseStreamResources(s.sessionID, streamID)
		}
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed, "failed to create QUIC stream", err)
	}

	// Create KWIK stream wrapper with underlying QUIC stream
	clientStream := stream.NewClientStreamWithQuic(streamID, s.primaryPath.ID(), s, quicStream)

	// Record stream creation in metrics
	if s.metricsManager != nil {
		s.metricsManager.RecordStreamCreated(streamID, s.primaryPath.ID())
	}

	// Create stream buffer in DataPresentationManager for aggregation support
	if s.dataPresentationManager != nil {
		err = s.dataPresentationManager.CreateStreamBuffer(streamID, nil)
		if err != nil {
			// Close the QUIC stream if buffer creation fails
			quicStream.Close()
			// Release allocated resources on failure
			if s.resourceManager != nil {
				s.resourceManager.ReleaseStreamResources(s.sessionID, streamID)
			}
			return nil, utils.NewKwikError(utils.ErrStreamCreationFailed, "failed to create stream buffer", err)
		}
	}

	// Mark as application stream to avoid internal ingestion stealing
	s.streamsMutex.Lock()
	s.appStreams[streamID] = true
	s.streamsMutex.Unlock()

	// Start ingestion of primary path data for this stream (decapsulation -> DPM) only for non-app streams
	go func(id uint64, qs quic.Stream, pid string) {
		s.streamsMutex.RLock()
		isApp := s.appStreams[id]
		s.streamsMutex.RUnlock()
		if !isApp {
			s.startPrimaryIngestion(id, qs, pid)
		}
	}(streamID, quicStream, s.primaryPath.ID())

	s.streamsMutex.Lock()
	s.streams[streamID] = clientStream
	s.streamsMutex.Unlock()

	return clientStream, nil
}

// OpenStream opens a new stream asynchronously (QUIC-compatible)
func (s *ClientSession) OpenStream() (Stream, error) {
	return s.OpenStreamSync(context.Background())
}

// AcceptStream accepts an incoming stream (QUIC-compatible)
// Only returns streams from the primary server, secondary streams are handled internally
func (s *ClientSession) AcceptStream(ctx context.Context) (Stream, error) {
	// First check if there are any streams in the accept channel
	select {
	case stream := <-s.acceptChan:
		return stream, nil
	default:
		// No streams in channel, try to accept from QUIC connections
	}

	// Get all active paths and try to accept streams from their connections
	activePaths := s.pathManager.GetActivePaths()

	for _, path := range activePaths {
		conn := path.GetConnection()
		if conn == nil {
			continue
		}

		// Try to accept a stream from this connection with a short timeout
		streamCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		quicStream, err := conn.AcceptStream(streamCtx)
		cancel()

		if err != nil {
			continue // Try next path
		}

		// Check if this is a secondary path - if so, handle internally
		if !path.IsPrimary() {
			// Handle secondary stream internally, don't expose to public interface
			go s.handleSecondaryStream(path.ID(), quicStream)
			continue // Don't return this stream to the application
		}

		// Create KWIK ClientStream wrapper for primary path streams only
		s.mutex.Lock()
		streamID := s.nextStreamID
		s.nextStreamID++
		s.mutex.Unlock()

		clientStream := stream.NewClientStreamWithQuic(streamID, path.ID(), s, quicStream)

		// Store in streams map
		s.streamsMutex.Lock()
		s.streams[clientStream.StreamID()] = clientStream
		s.streamsMutex.Unlock()

		return clientStream, nil
	}

	// If no streams available, wait with the original logic
	select {
	case stream := <-s.acceptChan:
		return stream, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.ctx.Done():
		return nil, utils.NewKwikError(utils.ErrConnectionLost, "session closed", nil)
	}
}

// AddPath adds a secondary path (server-side control plane method)
func (s *ClientSession) AddPath(address string) error {
	return utils.NewKwikError(utils.ErrInvalidFrame,
		"AddPath is only available on server sessions", nil)
}

// RemovePath removes a path (server-side control plane method)
func (s *ClientSession) RemovePath(pathID string) error {
	return utils.NewKwikError(utils.ErrInvalidFrame,
		"RemovePath is only available on server sessions", nil)
}

// GetActivePaths returns all active paths
func (s *ClientSession) GetActivePaths() []PathInfo {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	activePaths := s.pathManager.GetActivePaths()
	pathInfos := make([]PathInfo, len(activePaths))

	for i, path := range activePaths {
		pathInfos[i] = PathInfo{
			PathID:     path.ID(),
			Address:    path.Address(),
			IsPrimary:  path.IsPrimary(),
			Status:     PathStatusActive,
			CreatedAt:  s.createdAt, // TODO: Get actual path creation time
			LastActive: time.Now(),  // TODO: Get actual last activity time
		}
	}

	return pathInfos
}

// GetDeadPaths returns all dead paths
func (s *ClientSession) GetDeadPaths() []PathInfo {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	deadPaths := s.pathManager.GetDeadPaths()
	pathInfos := make([]PathInfo, len(deadPaths))

	for i, path := range deadPaths {
		pathInfos[i] = PathInfo{
			PathID:     path.ID(),
			Address:    path.Address(),
			IsPrimary:  path.IsPrimary(),
			Status:     PathStatusDead,
			CreatedAt:  s.createdAt, // TODO: Get actual path creation time
			LastActive: time.Now(),  // TODO: Get actual last activity time
		}
	}

	return pathInfos
}

// GetAllPaths returns all paths (active and dead)
func (s *ClientSession) GetAllPaths() []PathInfo {
	activePaths := s.GetActivePaths()
	deadPaths := s.GetDeadPaths()

	allPaths := make([]PathInfo, len(activePaths)+len(deadPaths))
	copy(allPaths, activePaths)
	copy(allPaths[len(activePaths):], deadPaths)

	return allPaths
}

// SendRawData sends raw data through a specific path
func (s *ClientSession) SendRawData(data []byte, pathID string, remoteStreamID uint64) error {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if s.state != SessionStateActive {
		return utils.NewKwikError(utils.ErrConnectionLost, "session is not active", nil)
	}

	// Validate inputs
	if len(data) == 0 {
		return utils.NewKwikError(utils.ErrInvalidFrame, "raw data cannot be empty", nil)
	}

	if pathID == "" {
		return utils.NewKwikError(utils.ErrInvalidFrame, "target path ID cannot be empty", nil)
	}

	path := s.pathManager.GetPath(pathID)
	if path == nil {
		return utils.NewPathNotFoundError(pathID)
	}

	if !path.IsActive() {
		return utils.NewPathDeadError(pathID)
	}

	// Check path health before sending data
	if !s.IsPathHealthy(pathID) {
		s.logger.Warn("Attempting to send data on unhealthy path", "pathID", pathID)

		// Try to find a better path if the requested path is unhealthy
		betterPath := s.GetBestAvailablePath()
		if betterPath != nil && betterPath.ID() != pathID {
			s.logger.Info("Routing data to healthier path", "originalPath", pathID, "newPath", betterPath.ID())
			path = betterPath
			pathID = betterPath.ID()
		}
	}

	// Record start time for RTT measurement
	startTime := time.Now()

	// Send raw data directly to the target path's data plane with retransmission tracking
	// This is simpler than the server approach since client directly accesses paths
	err := s.routeRawPacketToDataPlaneWithRetransmission(data, path, "custom", true, true)

	// Update health metrics based on operation result
	rtt := time.Since(startTime)
	s.UpdatePathHealthMetrics(pathID, &rtt, err == nil, uint64(len(data)))

	// Record data transmission in metrics
	if s.metricsManager != nil {
		if err == nil {
			s.metricsManager.RecordDataSent(pathID, remoteStreamID, uint64(len(data)))
			s.metricsManager.RecordRTT(pathID, rtt)
		} else {
			s.metricsManager.RecordError("transmission_failed", "medium", err.Error(), "client_session", pathID, remoteStreamID)
		}
	}

	return err
}

// Close closes the session and all its resources
func (s *ClientSession) Close() error {
	s.mutex.Lock()

	if s.state == SessionStateClosed {
		s.mutex.Unlock()
		return nil // Already closed
	}

	s.state = SessionStateClosed
	s.cancel()

	// Close all streams
	s.streamsMutex.Lock()

	// Create a copy of streams to avoid modification during iteration
	streamsToClose := make([]*stream.ClientStream, 0, len(s.streams))
	for _, stream := range s.streams {
		streamsToClose = append(streamsToClose, stream)
	}

	// Clear the streams map first to avoid RemoveStream calls during Close
	s.streams = make(map[uint64]*stream.ClientStream)
	s.streamsMutex.Unlock()

	// Close streams without holding the mutex
	for _, stream := range streamsToClose {
		stream.Close()
	}

	// Get active paths while holding the mutex
	activePaths := s.pathManager.GetActivePaths()

	// Release the mutex before closing paths to avoid deadlock
	s.mutex.Unlock()

	// Close all paths (without holding session mutex)
	for _, path := range activePaths {
		path.Close()
	}

	// Stop retransmission manager
	err := s.stopRetransmissionManager()
	if err != nil {
		s.logger.Warn("Failed to stop retransmission manager", "error", err)
	}

	// Stop offset coordinator
	err = s.stopOffsetCoordinator()
	if err != nil {
		s.logger.Warn("Failed to stop offset coordinator", "error", err)
	}

	// Stop health monitor
	err = s.stopHealthMonitor()
	if err != nil {
		s.logger.Warn("Failed to stop health monitor", "error", err)
	}

	// Stop control heartbeats
	if s.controlHeartbeatSystem != nil && s.primaryPath != nil {
		err = s.controlHeartbeatSystem.StopControlHeartbeats(s.sessionID, s.primaryPath.ID())
		if err != nil {
			s.logger.Warn("Failed to stop control heartbeats", "error", err)
		}
	}

	// Stop resource manager
	if s.resourceManager != nil {
		err = s.resourceManager.Stop()
		if err != nil {
			s.logger.Warn("Failed to stop resource manager", "error", err)
		}
	}

	// Stop all heartbeats
	s.stopAllHeartbeats()

	// Stop flow control manager
	err = s.stopFlowControlManager()
	if err != nil {
		s.logger.Warn("Failed to stop flow control manager", "error", err)
	}

	// Stop metrics collection
	if s.metricsCollector != nil {
		err = s.metricsCollector.Stop()
		if err != nil {
			s.logger.Warn("Failed to stop metrics collector", "error", err)
		}
	}

	// Close accept channel (safe to do without mutex since session is marked closed)
	close(s.acceptChan)

	return nil
}

// managePaths handles path management in background
func (s *ClientSession) managePaths() {
	ticker := time.NewTicker(utils.PathHealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.checkPathHealth()
		case <-s.ctx.Done():
			return
		}
	}
}

// checkPathHealth monitors path health and marks dead paths
func (s *ClientSession) checkPathHealth() {
	activePaths := s.pathManager.GetActivePaths()

	for _, path := range activePaths {
		if !path.IsActive() {
			s.pathManager.MarkPathDead(path.ID())
		}
	}
}

// handleIncomingStreams processes incoming streams from peers
func (s *ClientSession) handleIncomingStreams() {
	// Wait for session to be active before starting control frame processing
	// This ensures authentication is complete before we start reading control frames
	for {
		s.mutex.RLock()
		var state SessionState
		if s.stateManager != nil {
			state = s.stateManager.GetState()
		} else {
			state = s.state
		}
		s.mutex.RUnlock()

		if state == SessionStateActive {
			break
		}

		if state == SessionStateClosed {
			return // Session was closed before becoming active
		}

		time.Sleep(10 * time.Millisecond) // Small delay to avoid busy waiting
	}

	// Start control frame processing loop
	go s.handleControlFrames()

	// TODO: Implement incoming stream handling for data plane
	// This would listen on control plane for stream creation notifications
	// and create corresponding ClientStream objects
}

// handleControlFrames processes incoming control frames from the server
func (s *ClientSession) handleControlFrames() {
	// Backward-compat: delegate to per-path reader on primary
	s.startControlReaderForPath(s.primaryPath)
}

// processControlFrame processes a single control frame
func (s *ClientSession) processControlFrame(frame *control.ControlFrame) {
	switch frame.Type {
	case control.ControlFrameType_ADD_PATH_REQUEST:
		s.handleAddPathRequest(frame)
	case control.ControlFrameType_REMOVE_PATH_REQUEST:
		s.handleRemovePathRequest(frame)
	case control.ControlFrameType_PATH_STATUS_NOTIFICATION:
		s.handlePathStatusNotification(frame)
	case control.ControlFrameType_STREAM_CREATE_NOTIFICATION:
		s.handleStreamCreateNotification(frame)
	case control.ControlFrameType_RAW_PACKET_TRANSMISSION:
		s.handleRawPacketTransmission(frame)
	case control.ControlFrameType_HEARTBEAT:
		s.handleHeartbeat(frame)
	case control.ControlFrameType_SESSION_CLOSE:
		s.handleSessionClose(frame)
	case control.ControlFrameType_WINDOW_UPDATE:
		s.handleWindowUpdate(frame)
	case control.ControlFrameType_BACKPRESSURE_SIGNAL:
		s.handleBackpressureSignal(frame)
	default:
		// TODO: Log unknown frame type
	}
}

// startControlReaderForPath starts a control-plane reader loop for the given path
func (s *ClientSession) startControlReaderForPath(path transport.Path) {
	if path == nil {
		return
	}
	controlStream, err := path.GetControlStream()
	if err != nil || controlStream == nil {
		return
	}
	buffer := make([]byte, 4096)
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
			// Set a generous read deadline to avoid idle timeouts
			_ = controlStream.SetReadDeadline(time.Now().Add(5 * utils.DefaultKeepAliveInterval))
			n, err := controlStream.Read(buffer)
			if err != nil {
				// Ignore timeouts and continue; they are expected when idle
				if strings.Contains(strings.ToLower(err.Error()), "timeout") {
					time.Sleep(5 * time.Millisecond)
					continue
				}
				s.logger.Debug(fmt.Sprintf("Client control frame read error on path %s: %v", path.ID(), err))
				time.Sleep(100 * time.Millisecond)
				continue
			}
			if n == 0 {
				continue
			}
			var frame control.ControlFrame
			if err := proto.Unmarshal(buffer[:n], &frame); err != nil {
				continue
			}
			s.processControlFrame(&frame)
		}
	}
}

// handleAddPathRequest processes AddPathRequest frames from server
func (s *ClientSession) handleAddPathRequest(frame *control.ControlFrame) {
	// Deserialize AddPathRequest
	var addPathReq control.AddPathRequest
	err := proto.Unmarshal(frame.Payload, &addPathReq)
	if err != nil {
		s.sendAddPathResponse("", false, "failed to deserialize AddPathRequest", "DESERIALIZATION_ERROR")
		return
	}

	// Validate session ID matches
	if addPathReq.SessionId != s.sessionID {
		s.sendAddPathResponse("", false, "session ID mismatch", "SESSION_MISMATCH")
		return
	}

	// Validate target address
	if addPathReq.TargetAddress == "" {
		s.sendAddPathResponse("", false, "target address is empty", "INVALID_ADDRESS")
		return
	}

	// Validate path ID
	if addPathReq.PathId == "" {
		s.logger.Debug("Client received AddPathRequest with empty PathId")
		s.sendAddPathResponse("", false, "path ID is empty", "INVALID_PATH_ID")
		return
	}

	s.logger.Debug(fmt.Sprintf("Client received AddPathRequest with PathId: %s, TargetAddress: %s",
		addPathReq.PathId, addPathReq.TargetAddress))
	// Attempt to create secondary path
	startTime := time.Now()
	s.logger.Debug(fmt.Sprintf("Client attempting to create path to %s", addPathReq.TargetAddress))
	secondaryPath, err := s.pathManager.CreatePath(addPathReq.TargetAddress)
	connectionTime := time.Since(startTime)

	if err != nil {
		s.logger.Debug(fmt.Sprintf("Client failed to create path to %s: %v", addPathReq.TargetAddress, err))
		s.sendAddPathResponse("", false,
			fmt.Sprintf("failed to create path to %s: %v", addPathReq.TargetAddress, err),
			"CONNECTION_FAILED")
		return
	}
	s.logger.Debug(fmt.Sprintf("Client successfully created path to %s in %v", addPathReq.TargetAddress, connectionTime))

	// Store the original path ID for path manager operations
	originalPathID := secondaryPath.ID()

	// IMPORTANT: Use the server-provided path ID for the path object
	secondaryPath.SetID(addPathReq.PathId)

	// CLIENT CREATES THE CONTROL STREAM FOR SECONDARY PATH (OpenStreamSync)
	// This ensures proper communication with the secondary server
	_, err = secondaryPath.CreateControlStreamAsClient()
	if err != nil {
		s.pathManager.RemovePath(secondaryPath.ID())
		s.sendAddPathResponse("", false,
			fmt.Sprintf("failed to create control stream for secondary path: %v", err),
			"CONTROL_STREAM_FAILED")
		return
	}

	// Perform authentication on secondary path using existing session ID
	s.logger.Debug(fmt.Sprintf("Client starting authentication on secondary path %s", secondaryPath.ID()))
	err = s.performSecondaryPathAuthentication(context.Background(), secondaryPath)
	if err != nil {
		// Authentication failed, remove the path and send failure response
		s.logger.Debug(fmt.Sprintf("Client authentication failed on secondary path: %v", err))
		s.pathManager.RemovePath(secondaryPath.ID())
		s.sendAddPathResponse("", false,
			fmt.Sprintf("authentication failed on secondary path: %v", err),
			"AUTHENTICATION_FAILED")
		return
	}
	s.logger.Debug(fmt.Sprintf("Client authentication successful on secondary path %s", secondaryPath.ID()))

	// Integrate secondary path into session aggregate (Requirement 3.7)
	s.logger.Debug(fmt.Sprintf("Client starting integration of secondary path %s", secondaryPath.ID()))
	err = s.integrateSecondaryPath(secondaryPath, originalPathID)
	if err != nil {
		// Integration failed, remove the path using the original ID
		s.logger.Debug(fmt.Sprintf("Client integration failed for secondary path: %v", err))
		s.pathManager.RemovePath(originalPathID)
		s.sendAddPathResponse("", false,
			fmt.Sprintf("failed to integrate secondary path: %v", err),
			"INTEGRATION_FAILED")
		return
	}
	s.logger.Debug(fmt.Sprintf("Client integration successful for secondary path %s", secondaryPath.ID()))

	// Send success response with actual connection time
	s.logger.Debug(fmt.Sprintf("Client sending success response for path %s", secondaryPath.ID()))
	s.sendAddPathResponseWithTime(secondaryPath.ID(), true, "", "", connectionTime)
	s.logger.Debug(fmt.Sprintf("Client sent AddPathResponse with success=true for path %s", secondaryPath.ID()))

}

// sendAddPathResponse sends an AddPathResponse back to the server
func (s *ClientSession) sendAddPathResponse(pathID string, success bool, errorMessage, errorCode string) {
	// Validate inputs
	if !success && errorCode == "" {
		errorCode = "UNKNOWN_ERROR"
	}
	if !success && errorMessage == "" {
		errorMessage = "unspecified error occurred"
	}

	// Get control stream
	controlStream, err := s.primaryPath.GetControlStream()
	if err != nil {
		// TODO: Log error - cannot send response
		return
	}

	// Create AddPathResponse
	addPathResp := &control.AddPathResponse{
		Success:      success,
		PathId:       pathID,
		ErrorMessage: errorMessage,
		ErrorCode:    errorCode,
	}

	// If successful, include connection time (placeholder for basic response)
	if success {
		addPathResp.ConnectionTimeMs = 0 // Will be set properly in sendAddPathResponseWithTime
	}

	// Serialize response
	payload, err := proto.Marshal(addPathResp)
	if err != nil {
		// TODO: Log serialization error
		return
	}

	// Create control frame
	frame := &control.ControlFrame{
		FrameId:      data.GenerateFrameID(),
		Type:         control.ControlFrameType_ADD_PATH_RESPONSE,
		Payload:      payload,
		Timestamp:    uint64(time.Now().UnixNano()),
		SourcePathId: s.primaryPath.ID(),
		TargetPathId: "", // Server will handle routing
	}

	// Serialize and send frame
	frameData, err := proto.Marshal(frame)
	if err != nil {
		// TODO: Log serialization error
		return
	}

	_, err = controlStream.Write(frameData)
	if err != nil {
		// TODO: Log transmission error
	}
}

// sendAddPathResponseWithTime sends an AddPathResponse with actual connection time
func (s *ClientSession) sendAddPathResponseWithTime(pathID string, success bool, errorMessage, errorCode string, connectionTime time.Duration) {
	// Get control stream
	controlStream, err := s.primaryPath.GetControlStream()
	if err != nil {
		// TODO: Log error - cannot send response
		return
	}

	// Create AddPathResponse
	addPathResp := &control.AddPathResponse{
		Success:      success,
		PathId:       pathID,
		ErrorMessage: errorMessage,
		ErrorCode:    errorCode,
	}

	// Include actual connection time
	if success {
		addPathResp.ConnectionTimeMs = uint64(connectionTime.Milliseconds())
	}

	// Serialize response
	payload, err := proto.Marshal(addPathResp)
	if err != nil {
		// TODO: Log serialization error
		return
	}

	// Create control frame
	frame := &control.ControlFrame{
		FrameId:      data.GenerateFrameID(),
		Type:         control.ControlFrameType_ADD_PATH_RESPONSE,
		Payload:      payload,
		Timestamp:    uint64(time.Now().UnixNano()),
		SourcePathId: s.primaryPath.ID(),
		TargetPathId: "", // Server will handle routing
	}

	// Serialize and send frame
	frameData, err := proto.Marshal(frame)
	if err != nil {
		// TODO: Log serialization error
		return
	}

	_, err = controlStream.Write(frameData)
	if err != nil {
		// TODO: Log transmission error
	}
}

// Placeholder handlers for other control frame types
func (s *ClientSession) handleRemovePathRequest(frame *control.ControlFrame) {
	// TODO: Implement in future tasks
}

func (s *ClientSession) handlePathStatusNotification(frame *control.ControlFrame) {
	// TODO: Implement in future tasks
}

func (s *ClientSession) handleStreamCreateNotification(frame *control.ControlFrame) {
	// TODO: Implement in future tasks
}

func (s *ClientSession) handleRawPacketTransmission(frame *control.ControlFrame) {
	// Deserialize RawPacketTransmission
	var rawPacketReq control.RawPacketTransmission
	err := proto.Unmarshal(frame.Payload, &rawPacketReq)
	if err != nil {
		return
	}

	// Validate raw packet data
	if len(rawPacketReq.Data) == 0 {
		return
	}

	// Validate target path ID
	if rawPacketReq.TargetPathId == "" {
		return
	}

	// Get target path from path manager
	// Note: The path manager uses original path IDs as keys, but the path objects have server-provided IDs
	// We need to search through all paths to find the one with the matching server-provided ID
	var targetPath transport.Path
	activePaths := s.pathManager.GetActivePaths()

	for _, path := range activePaths {
		if path.ID() == rawPacketReq.TargetPathId {
			targetPath = path
			break
		}
	}

	if targetPath == nil {
		return
	}

	// Verify target path is active
	if !targetPath.IsActive() {
		s.logger.Debug(fmt.Sprintf("Client target path %s is not active", rawPacketReq.TargetPathId))
		return
	}

	// Route raw packet to data plane of target path
	err = s.routeRawPacketToDataPlane(rawPacketReq.Data, targetPath, rawPacketReq.ProtocolHint, rawPacketReq.PreserveOrder)
	if err != nil {
		return
	}

}

func (s *ClientSession) handleHeartbeat(frame *control.ControlFrame) {
	if frame == nil || len(frame.Payload) == 0 {
		return
	}

	var hb control.Heartbeat
	if err := proto.Unmarshal(frame.Payload, &hb); err != nil {
		return
	}

	// Record last heartbeat time per path for visibility/health
	pathID := frame.SourcePathId
	if pathID == "" {
		pathID = s.primaryPath.ID()
	}
	// Heartbeat tracking now handled by health monitor

	s.logger.Debug(fmt.Sprintf("Client received HEARTBEAT seq=%d from path=%s", hb.SequenceNumber, pathID))

	// If echo data present (8 bytes timestamp), compute RTT
	if len(hb.EchoData) == 8 {
		sendTs := int64(binary.BigEndian.Uint64(hb.EchoData))
		rtt := time.Since(time.Unix(0, sendTs))
		s.logger.Debug(fmt.Sprintf("Client computed RTT=%s on path=%s", rtt.String(), pathID))
		return
	}

	// If no echo data, respond with an echo to allow peer to compute RTT
	if controlStream, err := s.primaryPath.GetControlStream(); err == nil && controlStream != nil {
		// Echo back with EchoData = received timestamp
		echo := &control.Heartbeat{
			SequenceNumber: hb.SequenceNumber,
			Timestamp:      uint64(time.Now().UnixNano()),
			EchoData:       make([]byte, 8),
		}
		binary.BigEndian.PutUint64(echo.EchoData, hb.Timestamp)
		payload, err := proto.Marshal(echo)
		if err != nil {
			return
		}
		resp := &control.ControlFrame{
			FrameId:      data.GenerateFrameID(),
			Type:         control.ControlFrameType_HEARTBEAT,
			Payload:      payload,
			Timestamp:    uint64(time.Now().UnixNano()),
			SourcePathId: pathID,
			TargetPathId: "",
		}
		if data, err := proto.Marshal(resp); err == nil {
			s.logger.Debug(fmt.Sprintf("Client sending HEARTBEAT echo seq=%d to path=%s", hb.SequenceNumber, pathID))
			_, _ = controlStream.Write(data)
		}
	}
}

func (s *ClientSession) handleSessionClose(frame *control.ControlFrame) {
	// TODO: Implement in future tasks
}

// PathStatusNotificationHandler implementation for ClientSession

// OnPathStatusChanged handles path status change notifications
func (s *ClientSession) OnPathStatusChanged(pathID string, oldStatus, newStatus transport.PathState, metrics *transport.PathHealthMetrics) {
	// TODO: Implement path status change handling
	// This could include:
	// 1. Logging the status change
	// 2. Updating internal state
	// 3. Triggering failover if primary path fails
	// 4. Notifying application layer if needed
}

// OnPathFailureDetected handles path failure notifications
func (s *ClientSession) OnPathFailureDetected(pathID string, reason string, metrics *transport.PathHealthMetrics) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	// Check if the failed path is the primary path
	if s.primaryPath != nil && s.primaryPath.ID() == pathID {
		// TODO: Implement primary path failover logic
		// This should:
		// 1. Find an alternative active path
		// 2. Promote it to primary if available
		// 3. Update routing logic for new streams
		// 4. Potentially trigger connection to new primary server
	}

	// TODO: Log the failure for debugging
	// TODO: Notify application layer if configured
}

// OnPathRecovered handles path recovery notifications
func (s *ClientSession) OnPathRecovered(pathID string, metrics *transport.PathHealthMetrics) {
	// TODO: Implement path recovery handling
	// This could include:
	// 1. Logging the recovery
	// 2. Re-enabling the path for use
	// 3. Rebalancing traffic if needed
	// 4. Notifying application layer
}

// performAuthentication performs authentication over the control plane stream
func (s *ClientSession) performAuthentication(ctx context.Context) error {
	// Entrer en état d'authentification
	if err := s.stateManager.SetState(SessionStateAuthenticating); err != nil {
		return utils.NewKwikError(utils.ErrInvalidState,
			"failed to set authenticating state", err)
	}

	// Get control stream from primary path
	controlStream, err := s.primaryPath.GetControlStream()
	if err != nil {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			"failed to get control stream for authentication", err)
	}

	// Create authentication request with PRIMARY role
	authFrame, err := s.authManager.CreateAuthenticationRequest(control.SessionRole_PRIMARY)
	if err != nil {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			"failed to create authentication request", err)
	}

	// Serialize and send authentication request
	frameData, err := proto.Marshal(authFrame)
	if err != nil {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			"failed to serialize authentication frame", err)
	}

	_, err = controlStream.Write(frameData)
	if err != nil {
		return utils.NewKwikError(utils.ErrConnectionLost,
			"failed to send authentication request", err)
	}

	// Read authentication response with timeout
	responseCtx, cancel := context.WithTimeout(ctx, utils.DefaultHandshakeTimeout)
	defer cancel()

	responseBuf := make([]byte, 4096)

	// Use a goroutine to handle the read with context cancellation
	type readResult struct {
		n   int
		err error
	}

	readChan := make(chan readResult, 1)
	go func() {
		n, err := controlStream.Read(responseBuf)
		readChan <- readResult{n: n, err: err}
	}()

	var n int
	select {
	case result := <-readChan:
		n, err = result.n, result.err
		if err != nil {
			return utils.NewKwikError(utils.ErrConnectionLost,
				"failed to read authentication response", err)
		}
	case <-responseCtx.Done():
		return utils.NewKwikError(utils.ErrAuthenticationFailed,
			"authentication timeout", responseCtx.Err())
	}

	// Deserialize authentication response frame
	var responseFrame control.ControlFrame
	err = proto.Unmarshal(responseBuf[:n], &responseFrame)
	if err != nil {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			"failed to deserialize authentication response frame", err)
	}

	// Verify frame type - pendant l'authentification, seules les réponses d'auth sont acceptées
	if responseFrame.Type != control.ControlFrameType_AUTHENTICATION_RESPONSE {
		// Si c'est un heartbeat pendant l'authentification, c'est une collision
		if responseFrame.Type == control.ControlFrameType_HEARTBEAT {
			return utils.NewKwikError(utils.ErrInvalidFrame,
				"KWIK_INVALID_FRAME: expected authentication response, got HEARTBEAT", nil)
		}
		return utils.NewKwikError(utils.ErrInvalidFrame,
			fmt.Sprintf("expected authentication response, got %v", responseFrame.Type), nil)
	}

	// Handle authentication response
	err = s.authManager.HandleAuthenticationResponse(&responseFrame)
	if err != nil {
		return utils.NewKwikError(utils.ErrAuthenticationFailed,
			"authentication failed", err)
	}

	// Changer l'état vers authentifié
	if err := s.stateManager.SetState(SessionStateAuthenticated); err != nil {
		return utils.NewKwikError(utils.ErrInvalidState,
			"failed to set authenticated state", err)
	}

	// Mark primary path as authenticated
	if s.primaryPath != nil {
		s.authManager.MarkPrimaryPathAuthenticated(s.primaryPath.ID())

		// Also mark the path as authenticated in the heartbeat manager
		if s.heartbeatManager != nil {
			s.heartbeatManager.SetPathAuthenticated(s.sessionID, s.primaryPath.ID(), true)
		}
	}

	return nil
}

// GetSessionID returns the session ID (for external access)
func (s *ClientSession) GetSessionID() string {
	return s.sessionID
}

// GetState returns the current session state
func (s *ClientSession) GetState() SessionState {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.state
}

// IsAuthenticated returns whether the session is authenticated
func (s *ClientSession) IsAuthenticated() bool {
	if s.authManager == nil {
		return false
	}
	return s.authManager.IsAuthenticated()
}

// MarkAuthenticated marks the session as authenticated (for demo/testing purposes)
func (s *ClientSession) MarkAuthenticated() {
	if s.authManager != nil {
		s.authManager.MarkAuthenticated()
	}
}

// SetState sets the session state
func (s *ClientSession) SetState(state SessionState) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.state = state
}

// SetPrimaryPath sets the primary path for the session
func (s *ClientSession) SetPrimaryPath(primaryPath transport.Path) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.primaryPath = primaryPath
	return nil
}

// StartSessionManagement starts the session management goroutines
func (s *ClientSession) StartSessionManagement() {
	go s.managePaths()
	go s.handleIncomingStreams()
}

// markPrimaryPathAsDefault marks the primary path as the default for operations
// This implements requirement 2.5: primary path serves as default for stream operations
func (s *ClientSession) markPrimaryPathAsDefault() error {
	if s.primaryPath == nil {
		return utils.NewKwikError(utils.ErrConnectionLost, "no primary path available", nil)
	}

	// Ensure the primary path is marked as primary in the path manager
	err := s.pathManager.SetPrimaryPath(s.primaryPath.ID())
	if err != nil {
		return utils.NewKwikError(utils.ErrConnectionLost,
			"failed to set primary path in path manager", err)
	}

	return nil
}

// GetDefaultPathForWrite returns the primary path for write operations
// This implements requirement 4.3: client writes always go to primary server
func (s *ClientSession) GetDefaultPathForWrite() (transport.Path, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if s.primaryPath == nil {
		return nil, utils.NewKwikError(utils.ErrConnectionLost, "no primary path available", nil)
	}

	if !s.primaryPath.IsActive() {
		return nil, utils.NewKwikError(utils.ErrPathDead, "primary path is not active", nil)
	}

	return s.primaryPath, nil
}

// ValidatePathForWriteOperation validates that a path can be used for write operations
// This ensures requirement 4.3: client writes only go to primary path
func (s *ClientSession) ValidatePathForWriteOperation(pathID string) error {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if s.primaryPath == nil {
		return utils.NewKwikError(utils.ErrConnectionLost, "no primary path available", nil)
	}

	// For client sessions, only primary path is allowed for writes
	if pathID != s.primaryPath.ID() {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			"client write operations must use primary path only", nil)
	}

	if !s.primaryPath.IsActive() {
		return utils.NewKwikError(utils.ErrPathDead, "primary path is not active", nil)
	}

	return nil
}

// GetPrimaryPath returns the primary path for this session
func (s *ClientSession) GetPrimaryPath() transport.Path {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.primaryPath
}

// RemoveStream removes a stream from the session
func (s *ClientSession) RemoveStream(streamID uint64) {
	s.streamsMutex.Lock()
	defer s.streamsMutex.Unlock()

	delete(s.streams, streamID)

	// Clean up stream buffer in DataPresentationManager
	if s.dataPresentationManager != nil {
		err := s.dataPresentationManager.RemoveStreamBuffer(streamID)
		if err != nil {
			// Log error but don't fail the removal
			s.logger.Debug(fmt.Sprintf("Failed to remove stream buffer %d: %v", streamID, err))
		}
	}

	// Release resources in ResourceManager
	if s.resourceManager != nil {
		err := s.resourceManager.ReleaseStreamResources(s.sessionID, streamID)
		if err != nil {
			// Log error but don't fail the removal
			s.logger.Debug(fmt.Sprintf("Failed to release resources for stream %d: %v", streamID, err))
		}
	}
}

// GetContext returns the session context
func (s *ClientSession) GetContext() context.Context {
	return s.ctx
}

// GetStreamAggregator returns the secondary stream aggregator
func (s *ClientSession) GetStreamAggregator() *data.StreamAggregator {
	return s.aggregator
}

// GetMetadataProtocol returns the metadata protocol instance
func (s *ClientSession) GetMetadataProtocol() *stream.MetadataProtocolImpl {
	return s.metadataProtocol
}

// GetDataPresentationManager returns the data presentation manager
func (s *ClientSession) GetDataPresentationManager() stream.DataPresentationManager {
	return s.dataPresentationManager
}

// GetOffsetCoordinator returns the offset coordinator
func (s *ClientSession) GetOffsetCoordinator() data.OffsetCoordinator {
	return s.offsetCoordinator
}

// GetAggregatedDataForStream returns aggregated data from secondary streams for a KWIK stream
func (s *ClientSession) GetAggregatedDataForStream(streamID uint64) ([]byte, error) {
	if s.aggregator == nil {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed, "no secondary aggregator available", nil)
	}

	// Get aggregated data from the secondary stream aggregator
	return s.aggregator.GetAggregatedData(streamID)
}

// ConsumeAggregatedDataForStream consumes and removes aggregated data from the aggregator
func (s *ClientSession) ConsumeAggregatedDataForStream(streamID uint64, bytesToConsume int) ([]byte, error) {
	if s.aggregator == nil {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed, "no secondary aggregator available", nil)
	}

	// Consume aggregated data from the secondary stream aggregator (with forward sliding)
	return s.aggregator.ConsumeAggregatedData(streamID, bytesToConsume)
}

// AggregatePrimaryData deposits primary stream data into the aggregator
func (s *ClientSession) AggregateData(streamID uint64, frame []byte, offset uint64) error {
	if s.aggregator == nil {
		return utils.NewKwikError(utils.ErrStreamCreationFailed, "no secondary aggregator available", nil)
	}

	// Create SecondaryStreamData structure for primary data
	primaryData := &data.DataFrame{
		StreamID:     streamID,  // Use streamID to pass validation
		PathID:       "primary", // Mark as primary path
		Data:         frame,
		Offset:       offset,
		KwikStreamID: streamID,
		Timestamp:    time.Now(),
		SequenceNum:  0,
	}

	// Deposit primary data into the aggregator
	s.logger.Debug(fmt.Sprintf("ClientSession depositing %d bytes of primary data for KWIK stream %d at offset %d",
		len(frame), streamID, offset))

	return s.aggregator.AggregateDataFrames(primaryData)
}

// performSecondaryPathAuthentication performs authentication on a secondary path using existing session ID
func (s *ClientSession) performSecondaryPathAuthentication(ctx context.Context, secondaryPath transport.Path) error {
	// Get control stream from secondary path
	controlStream, err := secondaryPath.GetControlStream()
	if err != nil {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			"failed to get control stream for secondary path authentication", err)
	}

	// Create authentication request using existing session ID with SECONDARY role
	// This is the key difference from primary path authentication
	s.logger.Debug(fmt.Sprintf("Client creating SECONDARY authentication request for session %s", s.sessionID))
	authFrame, err := s.authManager.CreateAuthenticationRequest(control.SessionRole_SECONDARY)
	if err != nil {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			"failed to create authentication request for secondary path", err)
	}

	// Serialize and send authentication request
	frameData, err := proto.Marshal(authFrame)
	if err != nil {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			"failed to serialize authentication frame for secondary path", err)
	}

	_, err = controlStream.Write(frameData)
	if err != nil {
		return utils.NewKwikError(utils.ErrConnectionLost,
			"failed to send authentication request on secondary path", err)
	}

	// Read authentication response with timeout
	responseCtx, cancel := context.WithTimeout(ctx, utils.DefaultHandshakeTimeout)
	defer cancel()

	responseBuf := make([]byte, 4096)

	// Use a goroutine to handle the read with context cancellation
	type readResult struct {
		n   int
		err error
	}

	readChan := make(chan readResult, 1)
	go func() {
		n, err := controlStream.Read(responseBuf)
		readChan <- readResult{n: n, err: err}
	}()

	var n int
	select {
	case result := <-readChan:
		n, err = result.n, result.err
		if err != nil {
			return utils.NewKwikError(utils.ErrConnectionLost,
				"failed to read authentication response from secondary path", err)
		}
	case <-responseCtx.Done():
		return utils.NewKwikError(utils.ErrAuthenticationFailed,
			"secondary path authentication timeout", responseCtx.Err())
	}

	// Deserialize authentication response frame
	var responseFrame control.ControlFrame
	err = proto.Unmarshal(responseBuf[:n], &responseFrame)
	if err != nil {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			"failed to deserialize authentication response frame from secondary path", err)
	}

	// Verify frame type
	if responseFrame.Type != control.ControlFrameType_AUTHENTICATION_RESPONSE {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			fmt.Sprintf("expected authentication response from secondary path, got %v", responseFrame.Type), nil)
	}

	// Handle authentication response
	// Note: We don't update the authManager state since this is a secondary path
	// We just validate that the authentication succeeded
	var authResp control.AuthenticationResponse
	err = proto.Unmarshal(responseFrame.Payload, &authResp)
	if err != nil {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			"failed to deserialize authentication response payload", err)
	}

	// Check authentication result
	if !authResp.Success {
		return utils.NewKwikError(utils.ErrAuthenticationFailed,
			fmt.Sprintf("secondary path authentication failed: %s", authResp.ErrorMessage), nil)
	}

	// Validate session ID matches
	if authResp.SessionId != s.sessionID {
		return utils.NewKwikError(utils.ErrAuthenticationFailed,
			"session ID mismatch in secondary path authentication response", nil)
	}

	// Mark secondary path as authenticated
	s.authManager.SetPathAuthenticated(secondaryPath.ID(), true)

	// Also mark the path as authenticated in the heartbeat manager
	if s.heartbeatManager != nil {
		s.heartbeatManager.SetPathAuthenticated(s.sessionID, secondaryPath.ID(), true)
	}

	// Add path to flow control manager
	if s.flowControlManager != nil {
		err := s.flowControlManager.AddPath(secondaryPath.ID())
		if err != nil {
			s.logger.Warn("Failed to add secondary path to flow control", "pathID", secondaryPath.ID(), "error", err)
		}
	}

	return nil
}

// integrateSecondaryPath integrates a secondary path into the session aggregate
// This implements requirement 3.7: secondary paths are added to session aggregate and available for traffic distribution
func (s *ClientSession) integrateSecondaryPath(secondaryPath transport.Path, originalPathID string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	// Validate that the path is not nil
	if secondaryPath == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "secondary path is nil", nil)
	}

	// Validate that the path is active
	if !secondaryPath.IsActive() {
		return utils.NewKwikError(utils.ErrPathDead, "secondary path is not active", nil)
	}

	// Validate that the path is not primary (secondary paths should not be primary)
	if secondaryPath.IsPrimary() {
		return utils.NewKwikError(utils.ErrInvalidFrame, "secondary path cannot be primary", nil)
	}

	// Verify the path is already in the path manager
	// First try with the server-provided ID (current path ID)
	managedPath := s.pathManager.GetPath(secondaryPath.ID())
	if managedPath == nil {
		// If not found with server ID, try with original ID
		managedPath = s.pathManager.GetPath(originalPathID)
		if managedPath == nil {
			return utils.NewKwikError(utils.ErrInvalidFrame, "secondary path not found in path manager", nil)
		}
	}

	// The path is found in the path manager - integration is successful
	s.logger.Debug(fmt.Sprintf("Client found managed path with original ID %s, secondary path has server ID %s", managedPath.ID(), secondaryPath.ID()))

	// At this point, the path is already integrated into the PathManager by CreatePath()
	// The path is now available for:
	// 1. Health monitoring (already started)
	// 2. Data plane operations (through PathManager)
	// 3. Path queries (GetActivePaths, etc.)
	// 4. Automatic stream acceptance and aggregation (implemented below)

	// Start automatic stream acceptance for this secondary path
	// This runs in background and automatically handles all streams from secondary servers
	s.logger.Debug(fmt.Sprintf("Client starting automatic stream acceptance for secondary path %s", secondaryPath.ID()))
	go s.autoAcceptSecondaryStreams(secondaryPath)

	// Start control-plane reader for this secondary path
	s.logger.Debug(fmt.Sprintf("Client starting control-frame reader for secondary path %s", secondaryPath.ID()))
	go s.startControlReaderForPath(secondaryPath)

	// Register the new path with the retransmission manager
	if s.retransmissionManager != nil {
		err := s.retransmissionManager.RegisterPath(secondaryPath.ID())
		if err != nil {
			s.logger.Warn("Failed to register secondary path with retransmission manager", "pathID", secondaryPath.ID(), "error", err)
		}
	}

	return nil
}

// routeRawPacketToDataPlane routes raw packet data to the data plane of the specified target path
// This implements requirement 9.2 and 9.3: client receives raw packet commands and routes them to data plane
func (s *ClientSession) routeRawPacketToDataPlane(data []byte, targetPath transport.Path, protocolHint string, preserveOrder bool) error {
	// Validate inputs
	if len(data) == 0 {
		return utils.NewKwikError(utils.ErrInvalidFrame, "raw packet data is empty", nil)
	}

	if targetPath == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "target path is nil", nil)
	}

	// Verify target path is active
	if !targetPath.IsActive() {
		return utils.NewKwikError(utils.ErrPathDead,
			fmt.Sprintf("target path %s is not active", targetPath.ID()), nil)
	}

	// Get or create a data plane stream from target path
	// First, try to get existing data streams
	dataStreams := targetPath.GetDataStreams()
	var dataStream quic.Stream
	var err error

	if len(dataStreams) > 0 {
		// Use the first available data stream
		dataStream = dataStreams[0]
	} else {
		// No existing data streams, create a new one
		// We need to access the connection wrapper to create a data stream
		connection := targetPath.GetConnection()
		if connection == nil {
			return utils.NewKwikError(utils.ErrConnectionLost,
				fmt.Sprintf("no connection available for path %s", targetPath.ID()), nil)
		}

		// Create a new data stream
		dataStream, err = connection.OpenStreamSync(connection.Context())
		if err != nil {
			return utils.NewKwikError(utils.ErrStreamCreationFailed,
				fmt.Sprintf("failed to create data stream for path %s", targetPath.ID()), err)
		}
	}

	s.logger.Debug(fmt.Sprintf("Client routing raw packet (%d bytes) to data plane of path %s", len(data), targetPath.ID()))

	// Write raw packet data directly to the data plane stream
	// This ensures the raw packet reaches the target server's data plane without interpretation
	_, err = dataStream.Write(data)
	if err != nil {
		return utils.NewKwikError(utils.ErrConnectionLost,
			fmt.Sprintf("failed to write raw packet to data plane of path %s", targetPath.ID()), err)
	}

	return nil
}

// RawPacketFrame represents a frame containing raw packet data for data plane transmission
type RawPacketFrame struct {
	Data          []byte
	PathID        string
	ProtocolHint  string
	PreserveOrder bool
	Timestamp     time.Time
}

// Serialize serializes the raw packet frame to bytes
func (rpf *RawPacketFrame) Serialize() ([]byte, error) {
	// Simple serialization format:
	// [DataLen:4][Data:N][PathIDLen:2][PathID:N][ProtocolHintLen:2][ProtocolHint:N][PreserveOrder:1][Timestamp:8]

	pathIDBytes := []byte(rpf.PathID)
	protocolHintBytes := []byte(rpf.ProtocolHint)

	result := make([]byte, 0, 4+len(rpf.Data)+2+len(pathIDBytes)+2+len(protocolHintBytes)+1+8)

	// DataLen (4 bytes)
	dataLen := uint32(len(rpf.Data))
	for i := 0; i < 4; i++ {
		result = append(result, byte(dataLen>>(8*(3-i))))
	}

	// Data
	result = append(result, rpf.Data...)

	// PathIDLen (2 bytes)
	pathIDLen := uint16(len(pathIDBytes))
	result = append(result, byte(pathIDLen>>8))
	result = append(result, byte(pathIDLen))

	// PathID
	result = append(result, pathIDBytes...)

	// ProtocolHintLen (2 bytes)
	protocolHintLen := uint16(len(protocolHintBytes))
	result = append(result, byte(protocolHintLen>>8))
	result = append(result, byte(protocolHintLen))

	// ProtocolHint
	result = append(result, protocolHintBytes...)

	// PreserveOrder (1 byte)
	if rpf.PreserveOrder {
		result = append(result, 1)
	} else {
		result = append(result, 0)
	}

	// Timestamp (8 bytes - Unix nano)
	timestamp := uint64(rpf.Timestamp.UnixNano())
	for i := 0; i < 8; i++ {
		result = append(result, byte(timestamp>>(8*(7-i))))
	}

	return result, nil
}

// Deserialize deserializes bytes into the raw packet frame
func (rpf *RawPacketFrame) Deserialize(data []byte) error {
	if len(data) < 17 { // Minimum size: 4+0+2+0+2+0+1+8
		return fmt.Errorf("raw packet frame data too short: %d bytes", len(data))
	}

	offset := 0

	// DataLen (4 bytes)
	dataLen := uint32(0)
	for i := 0; i < 4; i++ {
		dataLen = (dataLen << 8) | uint32(data[offset+i])
	}
	offset += 4

	// Check if we have enough data
	if len(data) < offset+int(dataLen) {
		return fmt.Errorf("raw packet frame incomplete: missing data")
	}

	// Data
	rpf.Data = make([]byte, dataLen)
	copy(rpf.Data, data[offset:offset+int(dataLen)])
	offset += int(dataLen)

	// PathIDLen (2 bytes)
	if len(data) < offset+2 {
		return fmt.Errorf("raw packet frame incomplete: missing PathID length")
	}
	pathIDLen := uint16(data[offset])<<8 | uint16(data[offset+1])
	offset += 2

	// PathID
	if len(data) < offset+int(pathIDLen) {
		return fmt.Errorf("raw packet frame incomplete: missing PathID data")
	}
	rpf.PathID = string(data[offset : offset+int(pathIDLen)])
	offset += int(pathIDLen)

	// ProtocolHintLen (2 bytes)
	if len(data) < offset+2 {
		return fmt.Errorf("raw packet frame incomplete: missing ProtocolHint length")
	}
	protocolHintLen := uint16(data[offset])<<8 | uint16(data[offset+1])
	offset += 2

	// ProtocolHint
	if len(data) < offset+int(protocolHintLen) {
		return fmt.Errorf("raw packet frame incomplete: missing ProtocolHint data")
	}
	rpf.ProtocolHint = string(data[offset : offset+int(protocolHintLen)])
	offset += int(protocolHintLen)

	// PreserveOrder (1 byte)
	if len(data) < offset+1 {
		return fmt.Errorf("raw packet frame incomplete: missing PreserveOrder flag")
	}
	rpf.PreserveOrder = data[offset] == 1
	offset += 1

	// Timestamp (8 bytes)
	if len(data) < offset+8 {
		return fmt.Errorf("raw packet frame incomplete: missing Timestamp")
	}
	timestamp := uint64(0)
	for i := 0; i < 8; i++ {
		timestamp = (timestamp << 8) | uint64(data[offset+i])
	}
	rpf.Timestamp = time.Unix(0, int64(timestamp))

	return nil
}

// handleSecondaryStreamO handles a new secondary stream from a secondary server
// This method processes streams internally without exposing them to the public interface
func (s *ClientSession) handleSecondaryStream(pathID string, quicStream quic.Stream) error {
	s.logger.Debug(fmt.Sprintf("Client handling secondary stream open from path %s", pathID))

	// Handle the secondary stream using the secondary stream handler
	_, err := s.secondaryStreamHandler.HandleSecondaryStream(pathID, quicStream)
	if err != nil {
		s.logger.Debug(fmt.Sprintf("Client failed to handle secondary stream from path %s: %v", pathID, err))
		// Log error and close the stream
		quicStream.Close()
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			fmt.Sprintf("failed to handle secondary stream from path %s", pathID), err)
	}

	s.logger.Debug(fmt.Sprintf("Client starting secondary stream data processing for path %s", pathID))
	// Start processing the secondary stream data in a goroutine
	go s.handleSecondaryPathDataPacket(pathID, quicStream)

	return nil
}

// handlePrimaryDataPacket processes data from a secondary stream
func (s *ClientSession) HandlePrimaryPathDataPacket(pathID string, quicStream quic.Stream) {
	s.logger.Debug(fmt.Sprintf("Client starting primary stream data processing loop for path %s", pathID))

	buffer := make([]byte, 4096)
	for {
		select {
		case <-s.ctx.Done():
			s.logger.Debug(fmt.Sprintf("Client primary stream processing stopping for path %s (session closing)", pathID))
			return // Session is closing
		default:
			// Read data from the secondary stream
			s.logger.Debug(fmt.Sprintf("Client attempting to read from primary stream on path %s", pathID))
			n, err := quicStream.Read(buffer)
			if err != nil {
				// Stream closed or error occurred
				s.logger.Debug(fmt.Sprintf("Client primary stream from path %s closed: %v", pathID, err))
				return
			}

			if n == 0 {
				s.logger.Debug(fmt.Sprintf("Client no data available from primary stream on path %s", pathID))
				// Add a small delay to prevent busy-waiting when no data is available
				time.Sleep(10 * time.Millisecond)
				continue // No data available
			}

			s.logger.Debug(fmt.Sprintf("Client received %d bytes from primary stream on path %s:", n, pathID))

			// Process the encapsulated data according to the metadata protocol
			err = s.processDataPacket(pathID, buffer[:n])
			if err != nil {
				s.logger.Debug(fmt.Sprintf("Client error processing primary stream data from path %s: %v", pathID, err))
				// Continue processing other data even if one frame fails
			} else {
				s.logger.Debug(fmt.Sprintf("Client successfully processed primary stream data from path %s", pathID))
			}
		}
	}
}

// handleSecondaryPathDataPacket processes data from a secondary stream
func (s *ClientSession) handleSecondaryPathDataPacket(pathID string, quicStream quic.Stream) {
	defer quicStream.Close()
	s.logger.Debug(fmt.Sprintf("Client starting secondary stream data processing loop for path %s", pathID))

	buffer := make([]byte, 4096)
	for {
		select {
		case <-s.ctx.Done():
			s.logger.Debug(fmt.Sprintf("Client secondary stream processing stopping for path %s (session closing)", pathID))
			return // Session is closing
		default:
			// Read data from the secondary stream
			s.logger.Debug(fmt.Sprintf("Client attempting to read from secondary stream on path %s", pathID))
			n, err := quicStream.Read(buffer)
			if err != nil {
				// Stream closed or error occurred
				s.logger.Debug(fmt.Sprintf("Client secondary stream from path %s closed: %v", pathID, err))
				return
			}

			if n == 0 {
				s.logger.Debug(fmt.Sprintf("Client no data available from secondary stream on path %s", pathID))
				// Add a small delay to prevent busy-waiting when no data is available
				time.Sleep(10 * time.Millisecond)
				continue // No data available
			}

			s.logger.Debug(fmt.Sprintf("Client received %d bytes from secondary stream on path %s:", n, pathID))

			// Process the encapsulated data according to the metadata protocol
			err = s.processDataPacket(pathID, buffer[:n])
			if err != nil {
				s.logger.Debug(fmt.Sprintf("Client error processing secondary stream data from path %s: %v", pathID, err))
				// Continue processing other data even if one frame fails
			} else {
				s.logger.Debug(fmt.Sprintf("Client successfully processed secondary stream data from path %s", pathID))
			}
		}
	}
}

// processDataPacket decapsulates and aggregates secondary stream data
func (s *ClientSession) processDataPacket(pathID string, encapsulatedData []byte) error {
	s.logger.Debug(fmt.Sprintf("[SECONDARY] Processing encapsulated data from path %s (%d bytes)", pathID, len(encapsulatedData)))
	if len(encapsulatedData) > 16 {
		s.logger.Debug(fmt.Sprintf("[SECONDARY] First 16 bytes: %x...", encapsulatedData[:16]))
	}

	// Parse transport packet [PacketID:8][FrameCount:2] + frames
	if len(encapsulatedData) < 10 {
		return nil
	}
	packetID := uint64(0)
	for i := 0; i < 8; i++ {
		packetID = (packetID << 8) | uint64(encapsulatedData[i])
	}
	frameCount := int(uint16(encapsulatedData[8])<<8 | uint16(encapsulatedData[9]))
	off := 10
	metadataProtocol := s.metadataProtocol
	if metadataProtocol == nil {
		return utils.NewKwikError(utils.ErrStreamCreationFailed, "no metadata protocol available", nil)
	}
	for f := 0; f < frameCount; f++ {
		if off+4 > len(encapsulatedData) {
			break
		}
		flen := int(uint32(encapsulatedData[off])<<24 | uint32(encapsulatedData[off+1])<<16 | uint32(encapsulatedData[off+2])<<8 | uint32(encapsulatedData[off+3]))
		off += 4
		if off+flen > len(encapsulatedData) {
			break
		}
		inner := encapsulatedData[off : off+flen]
		off += flen
		// Parse frame metadata
		metadata, frame, err := metadataProtocol.DecapsulateData(inner)
		if err != nil {
			debugLen := 16
			if len(inner) < debugLen {
				debugLen = len(inner)
			}
			s.logger.Debug(fmt.Sprintf("[SECONDARY] Failed to decapsulate frame: %v (data: %x...)", err, inner[:debugLen]))
			return utils.NewKwikError(utils.ErrInvalidFrame, fmt.Sprintf("failed to decapsulate secondary stream data: %v", err), err)
		}
		s.logger.Debug(fmt.Sprintf("[SECONDARY] Decapsulated frame: streamID=%d, kwikStreamID=%d, offset=%d, size=%d",
			metadata.SecondaryStreamID, metadata.KwikStreamID, metadata.Offset, len(frame)))
		secondaryStreamID := metadata.SecondaryStreamID
		if secondaryStreamID == 0 {
			secondaryStreamID = metadata.KwikStreamID
		}

		dataFrame := &data.DataFrame{
			StreamID:     secondaryStreamID,
			PathID:       pathID,
			Data:         frame,
			Offset:       metadata.Offset,
			KwikStreamID: metadata.KwikStreamID,
			Timestamp:    time.Now(),
			SequenceNum:  0,
		}

		aggregator := s.aggregator
		if aggregator == nil {
			return utils.NewKwikError(utils.ErrStreamCreationFailed, "no secondary aggregator available", nil)
		}
		if err = aggregator.AggregateDataFrames(dataFrame); err != nil {
			s.logger.Debug(fmt.Sprintf("[SECONDARY] Failed to aggregate data: %v (streamID=%d, kwikStreamID=%d, offset=%d, size=%d)",
				err, secondaryStreamID, metadata.KwikStreamID, metadata.Offset, len(frame)))
			return utils.NewKwikError(utils.ErrStreamCreationFailed, fmt.Sprintf("failed to aggregate secondary stream data: %v", err), err)
		}
		s.logger.Debug(fmt.Sprintf("[SECONDARY] Successfully aggregated data: streamID=%d, kwikStreamID=%d, offset=%d, size=%d",
			secondaryStreamID, metadata.KwikStreamID, metadata.Offset, len(frame)))
	}
	// Ack after all frames processed
	active := s.pathManager.GetActivePaths()
	for _, p := range active {
		if p.ID() == pathID {
			if ctrl, e := p.GetControlStream(); e == nil && ctrl != nil {
				ack := &control.PacketAck{PacketId: packetID, PathId: pathID, Timestamp: uint64(time.Now().UnixNano())}
				if bytes, e2 := proto.Marshal(ack); e2 == nil {
					frame := &control.ControlFrame{FrameId: data.GenerateFrameID(), Type: control.ControlFrameType_PACKET_ACK, Payload: bytes, Timestamp: uint64(time.Now().UnixNano()), SourcePathId: pathID}
					if out, e3 := proto.Marshal(frame); e3 == nil {
						_, _ = ctrl.Write(out)
					}
				}
			}

			// Acknowledge segments for retransmission tracking
			segmentID := fmt.Sprintf("%s-%d", pathID, packetID)
			ackErr := s.acknowledgeSegment(segmentID)
			if ackErr != nil {
				s.logger.Debug("Failed to acknowledge segment", "segmentID", segmentID, "error", ackErr)
			}
			break
		}
	}
	return nil
}

// autoAcceptSecondaryStreams automatically accepts and processes streams from a secondary path
// This runs in background and handles all streams from secondary servers transparently
func (s *ClientSession) autoAcceptSecondaryStreams(path transport.Path) {
	s.logger.Debug(fmt.Sprintf("Client starting automatic stream acceptance loop for secondary path %s", path.ID()))

	conn := path.GetConnection()
	if conn == nil {
		s.logger.Debug(fmt.Sprintf("Client no QUIC connection available for secondary path %s", path.ID()))
		return
	}

	for {
		select {
		case <-s.ctx.Done():
			s.logger.Debug(fmt.Sprintf("Client stopping automatic stream acceptance for secondary path %s (session closing)", path.ID()))
			return
		default:
			// Accept streams from this secondary path
			s.logger.Debug(fmt.Sprintf("Client attempting to accept stream from secondary path %s", path.ID()))
			quicStream, err := conn.AcceptStream(context.Background())
			if err != nil {
				errStr := strings.ToLower(err.Error())
				if strings.Contains(errStr, "timeout") || strings.Contains(errStr, "no recent network activity") {
					s.logger.Debug(fmt.Sprintf("Client accept on secondary path %s timeout, continue", path.ID()))
					time.Sleep(10 * time.Millisecond)
					continue
				}
				s.logger.Debug(fmt.Sprintf("Client failed to accept stream from secondary path %s: %v", path.ID(), err))
				// For fatal errors, exit loop
				return
			}

			s.logger.Debug(fmt.Sprintf("Client accepted new stream from secondary path %s", path.ID()))

			// Handle this secondary stream internally (don't expose to application)
			go s.handleSecondaryStream(path.ID(), quicStream)
		}
	}
}

// startPrimaryIngestion reads metadata-encapsulated frames from the primary QUIC stream
// and deposits the decoded payload into the DataPresentationManager via the aggregator
func (s *ClientSession) startPrimaryIngestion(streamID uint64, quicStream quic.Stream, pathID string) {
	buffer := make([]byte, 65536)
	for {
		n, err := quicStream.Read(buffer)
		if err != nil || n == 0 {
			return
		}

		// Parse transport packet: [PacketID:8][FrameCount:2] + repeated([FrameLen:4][FrameBytes])
		if n < 10 {
			continue
		}
		packetID := uint64(0)
		for i := 0; i < 8; i++ {
			packetID = (packetID << 8) | uint64(buffer[i])
		}
		frameCount := int(uint16(buffer[8])<<8 | uint16(buffer[9]))
		off := 10
		for f := 0; f < frameCount; f++ {
			if off+4 > n {
				break
			}
			flen := int(uint32(buffer[off])<<24 | uint32(buffer[off+1])<<16 | uint32(buffer[off+2])<<8 | uint32(buffer[off+3]))
			off += 4
			if off+flen > n {
				break
			}
			payloadBytes := buffer[off : off+flen]
			off += flen

			mp := s.metadataProtocol
			if mp == nil {
				continue
			}
			metadata, payload, derr := mp.DecapsulateData(payloadBytes)
			if derr != nil || metadata == nil || len(payload) == 0 {
				continue
			}

			secondaryData := &data.DataFrame{StreamID: streamID, PathID: pathID, Data: payload, Offset: metadata.Offset, KwikStreamID: metadata.KwikStreamID, Timestamp: time.Now(), SequenceNum: 0}
			if s.aggregator != nil {
				_ = s.aggregator.AggregateDataFrames(secondaryData)
			}
		}

		// Send PacketAck over control plane
		if s.primaryPath != nil {
			if ctrl, e := s.primaryPath.GetControlStream(); e == nil && ctrl != nil {
				ack := &control.PacketAck{PacketId: packetID, PathId: pathID, Timestamp: uint64(time.Now().UnixNano())}
				if bytes, e2 := proto.Marshal(ack); e2 == nil {
					frame := &control.ControlFrame{FrameId: data.GenerateFrameID(), Type: control.ControlFrameType_PACKET_ACK, Payload: bytes, Timestamp: uint64(time.Now().UnixNano()), SourcePathId: pathID}
					if out, e3 := proto.Marshal(frame); e3 == nil {
						_, _ = ctrl.Write(out)
					}
				}
			}
		}

		// Acknowledge segments for retransmission tracking
		// Generate segment ID based on packet ID and path ID
		segmentID := fmt.Sprintf("%s-%d", pathID, packetID)
		ackErr := s.acknowledgeSegment(segmentID)
		if ackErr != nil {
			s.logger.Debug("Failed to acknowledge segment", "segmentID", segmentID, "error", ackErr)
		}
	}
}

// generateSessionID generates a unique session identifier
func generateSessionID() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return fmt.Sprintf("kwik-session-%x", bytes)
}

// generatePathID generates a unique path identifier
func generatePathID() string {
	bytes := make([]byte, 8)
	rand.Read(bytes)
	return fmt.Sprintf("path-%x", bytes)
}

// Old startHeartbeat function removed - now using health monitor system

// Old sendHeartbeatOnAllPaths function removed - now using health monitor system

// Old monitorHeartbeatLiveness function removed - now using health monitor system

// setupRetransmissionCallbacks configures the retransmission manager callbacks
func (s *ClientSession) setupRetransmissionCallbacks() {
	if s.retransmissionManager == nil {
		return
	}

	// Cast to implementation type to access callback methods
	if rmImpl, ok := s.retransmissionManager.(*data.RetransmissionManagerImpl); ok {
		// Set retry callback - called when a segment needs to be retransmitted
		rmImpl.SetRetryCallback(func(segmentID string, data []byte, pathID string) error {
			s.logger.Debug("Retransmitting segment", "segmentID", segmentID, "pathID", pathID, "dataSize", len(data))

			// Find the path to retransmit on
			path := s.pathManager.GetPath(pathID)
			if path == nil || !path.IsActive() {
				// Path is dead, try to find an alternative active path
				activePaths := s.pathManager.GetActivePaths()
				if len(activePaths) == 0 {
					return utils.NewKwikError(utils.ErrPathDead, "no active paths available for retransmission", nil)
				}
				path = activePaths[0] // Use first available active path
			}

			// Retransmit the data on the selected path
			return s.routeRawPacketToDataPlane(data, path, "retransmission", true)
		})

		// Set max retries callback - called when a segment has exceeded max retry attempts
		rmImpl.SetMaxRetriesCallback(func(segmentID string, data []byte, pathID string) error {
			s.logger.Warn("Segment dropped after max retries", "segmentID", segmentID, "pathID", pathID, "dataSize", len(data))

			// Could trigger path health degradation or failover here
			// For now, just log the failure
			return nil
		})
	}
}

// startRetransmissionManager starts the retransmission manager
func (s *ClientSession) startRetransmissionManager() error {
	if s.retransmissionManager == nil {
		return utils.NewKwikError(utils.ErrInvalidState, "retransmission manager not initialized", nil)
	}

	err := s.retransmissionManager.Start()
	if err != nil {
		return utils.NewKwikError(utils.ErrInvalidState, "failed to start retransmission manager", err)
	}

	// Register all current paths with the retransmission manager
	activePaths := s.pathManager.GetActivePaths()
	for _, path := range activePaths {
		err = s.retransmissionManager.RegisterPath(path.ID())
		if err != nil {
			s.logger.Warn("Failed to register path with retransmission manager", "pathID", path.ID(), "error", err)
		}
	}

	s.logger.Info("Retransmission manager started successfully")
	return nil
}

// stopRetransmissionManager stops the retransmission manager
func (s *ClientSession) stopRetransmissionManager() error {
	if s.retransmissionManager == nil {
		return nil
	}

	return s.retransmissionManager.Stop()
}

// trackSegmentForRetransmission tracks a data segment for retransmission
func (s *ClientSession) trackSegmentForRetransmission(segmentID string, data []byte, pathID string, timeout time.Duration) error {
	if s.retransmissionManager == nil {
		return utils.NewKwikError(utils.ErrInvalidState, "retransmission manager not available", nil)
	}

	return s.retransmissionManager.TrackSegment(segmentID, data, pathID, timeout)
}

// acknowledgeSegment acknowledges a successfully transmitted segment
func (s *ClientSession) acknowledgeSegment(segmentID string) error {
	if s.retransmissionManager == nil {
		return utils.NewKwikError(utils.ErrInvalidState, "retransmission manager not available", nil)
	}

	return s.retransmissionManager.AckSegment(segmentID)
}

// getRetransmissionStats returns current retransmission statistics
func (s *ClientSession) getRetransmissionStats() data.RetransmissionStats {
	if s.retransmissionManager == nil {
		return data.RetransmissionStats{}
	}

	return s.retransmissionManager.GetStats()
}

// startHealthMonitor starts the health monitor
func (s *ClientSession) startHealthMonitor() error {
	if s.healthMonitor == nil {
		return utils.NewKwikError(utils.ErrInvalidState, "health monitor not initialized", nil)
	}

	// Vérifier que l'authentification est terminée avant de démarrer le health monitoring
	if s.stateManager != nil && !s.stateManager.IsAuthenticationComplete() {
		return utils.NewKwikError(utils.ErrInvalidState,
			"cannot start health monitor: authentication not complete", nil)
	}

	// Ensure session is authenticated before starting health monitoring (compatibilité)
	if !s.IsAuthenticated() {
		return utils.NewKwikError(utils.ErrInvalidState, "cannot start health monitor before session authentication is complete", nil)
	}

	// Start monitoring for the session with active paths
	activePaths := s.pathManager.GetActivePaths()
	pathIDs := make([]string, len(activePaths))
	for i, path := range activePaths {
		pathIDs[i] = path.ID()
	}

	err := s.healthMonitor.StartMonitoring(s.sessionID, pathIDs)
	if err != nil {
		return utils.NewKwikError(utils.ErrInvalidState, "failed to start health monitor", err)
	}

	s.logger.Debug("Health monitor started for session", "sessionID", s.sessionID,
		"sessionState", s.stateManager.GetState().String())
	return nil
}

// stopHealthMonitor stops the health monitor
func (s *ClientSession) stopHealthMonitor() error {
	if s.healthMonitor == nil {
		return nil
	}

	err := s.healthMonitor.StopMonitoring(s.sessionID)
	if err != nil {
		s.logger.Error("Failed to stop health monitor", "error", err)
		return err
	}

	s.logger.Debug("Health monitor stopped for session", "sessionID", s.sessionID)
	return nil
}

// GetHealthMonitor returns the health monitor for external access
func (s *ClientSession) GetHealthMonitor() ConnectionHealthMonitor {
	return s.healthMonitor
}

// GetPathHealthStatus returns the health status of a specific path
func (s *ClientSession) GetPathHealthStatus(pathID string) *PathHealth {
	if s.healthMonitor == nil {
		return nil
	}
	return s.healthMonitor.GetPathHealth(s.sessionID, pathID)
}

// IsPathHealthy checks if a path is healthy enough for operations
func (s *ClientSession) IsPathHealthy(pathID string) bool {
	pathHealth := s.GetPathHealthStatus(pathID)
	if pathHealth == nil {
		return false
	}

	// Consider path healthy if status is Active and health score is above warning threshold
	return pathHealth.Status == PathStatusActive && pathHealth.HealthScore >= 70
}

// GetBestAvailablePath returns the healthiest available path for operations
func (s *ClientSession) GetBestAvailablePath() transport.Path {
	activePaths := s.pathManager.GetActivePaths()
	if len(activePaths) == 0 {
		return nil
	}

	var bestPath transport.Path
	var bestScore int = -1

	for _, path := range activePaths {
		if s.IsPathHealthy(path.ID()) {
			pathHealth := s.GetPathHealthStatus(path.ID())
			if pathHealth != nil && pathHealth.HealthScore > bestScore {
				bestScore = pathHealth.HealthScore
				bestPath = path
			}
		}
	}

	// If no healthy path found, return primary path as fallback
	if bestPath == nil && s.primaryPath != nil && s.primaryPath.IsActive() {
		return s.primaryPath
	}

	return bestPath
}

// UpdatePathHealthMetrics updates health metrics for a path based on operation results
func (s *ClientSession) UpdatePathHealthMetrics(pathID string, rtt *time.Duration, success bool, bytesTransferred uint64) {
	if s.healthMonitor == nil {
		return
	}

	update := PathMetricsUpdate{
		Activity: true,
	}

	if rtt != nil {
		update.RTT = rtt
	}

	if success {
		update.PacketAcked = true
		update.BytesAcked = bytesTransferred
	} else {
		update.PacketLost = true
	}

	update.BytesSent = bytesTransferred

	s.healthMonitor.UpdatePathMetrics(s.sessionID, pathID, update)
}

// startHeartbeatManagement starts heartbeat management for all paths
func (s *ClientSession) startHeartbeatManagement() {
	if s.heartbeatManager == nil {
		s.logger.Warn("Heartbeat manager not initialized")
		return
	}

	// Vérifier que l'authentification est terminée et que la session est active
	if s.stateManager != nil {
		if !s.stateManager.IsAuthenticationComplete() {
			s.logger.Debug("Cannot start heartbeat management: authentication not complete",
				"sessionID", s.sessionID,
				"sessionState", s.stateManager.GetState().String())
			return
		}

		if !s.stateManager.IsActive() {
			s.logger.Debug("Cannot start heartbeat management: session not active",
				"sessionID", s.sessionID,
				"sessionState", s.stateManager.GetState().String())
			return
		}
	}

	s.logger.Debug("Starting heartbeat management for session",
		"sessionID", s.sessionID,
		"sessionState", s.stateManager.GetState().String())

	// Start heartbeats for all active paths
	activePaths := s.pathManager.GetActivePaths()
	for _, path := range activePaths {
		s.startHeartbeatForPath(path.ID())
	}
}

// startHeartbeatForPath starts heartbeat monitoring for a specific path
func (s *ClientSession) startHeartbeatForPath(pathID string) {
	if s.heartbeatManager == nil {
		return
	}

	// Define heartbeat send callback
	sendCallback := func(sessionID, pathID string) error {
		// Send heartbeat through the path
		s.logger.Debug("Sending heartbeat", "sessionID", sessionID, "pathID", pathID)

		// Send actual heartbeat packet using control frame
		err := s.sendHeartbeatPacket(sessionID, pathID)
		if err != nil {
			s.logger.Debug("Failed to send heartbeat packet", "sessionID", sessionID, "pathID", pathID, "error", err)
			return err
		}

		// Update health monitor that we sent a heartbeat
		if s.healthMonitor != nil {
			s.healthMonitor.UpdatePathMetrics(sessionID, pathID, PathMetricsUpdate{
				HeartbeatSent: true,
				Activity:      true,
			})
		}
		return nil
	}

	// Define heartbeat timeout callback
	timeoutCallback := func(sessionID, pathID string, timeoutCount int) {
		s.logger.Warn("Heartbeat timeout detected", "sessionID", sessionID, "pathID", pathID, "timeoutCount", timeoutCount)

		// Update health monitor with timeout information
		if s.healthMonitor != nil {
			s.healthMonitor.UpdatePathMetrics(sessionID, pathID, PathMetricsUpdate{
				PacketLost: true,
				Activity:   false,
			})
		}
	}

	// Define authentication check callback
	authCheckCallback := func(sessionID, pathID string) bool {
		// Check if the path is authenticated
		if s.authManager == nil {
			return false
		}
		return s.authManager.IsPathAuthenticated(pathID)
	}

	// Start heartbeat monitoring with authentication awareness
	err := s.heartbeatManager.StartHeartbeatWithAuth(s.sessionID, pathID, sendCallback, timeoutCallback, authCheckCallback)
	if err != nil {
		s.logger.Error("Failed to start heartbeat for path", "pathID", pathID, "error", err)
	}
}

// stopHeartbeatForPath stops heartbeat monitoring for a specific path
func (s *ClientSession) stopHeartbeatForPath(pathID string) {
	if s.heartbeatManager == nil {
		return
	}

	err := s.heartbeatManager.StopHeartbeat(s.sessionID, pathID)
	if err != nil {
		s.logger.Error("Failed to stop heartbeat for path", "pathID", pathID, "error", err)
	}
}

// stopAllHeartbeats stops heartbeat monitoring for all paths
func (s *ClientSession) stopAllHeartbeats() {
	if s.heartbeatManager == nil {
		return
	}

	err := s.heartbeatManager.StopSession(s.sessionID)
	if err != nil {
		s.logger.Error("Failed to stop heartbeats for session", "error", err)
	}

	// Shutdown the heartbeat manager
	err = s.heartbeatManager.Shutdown()
	if err != nil {
		s.logger.Error("Failed to shutdown heartbeat manager", "error", err)
	}
}

// onHeartbeatReceived should be called when a heartbeat response is received
func (s *ClientSession) onHeartbeatReceived(pathID string, rtt time.Duration) {
	if s.heartbeatManager == nil {
		return
	}

	// Notify heartbeat manager
	err := s.heartbeatManager.OnHeartbeatReceived(s.sessionID, pathID)
	if err != nil {
		s.logger.Error("Failed to process heartbeat response", "pathID", pathID, "error", err)
		return
	}

	// Update health monitor with RTT information
	if s.healthMonitor != nil {
		s.healthMonitor.UpdatePathMetrics(s.sessionID, pathID, PathMetricsUpdate{
			RTT:               &rtt,
			HeartbeatReceived: true,
			Activity:          true,
		})
	}

	// Update heartbeat manager with path health
	if s.healthMonitor != nil {
		pathHealth := s.healthMonitor.GetPathHealth(s.sessionID, pathID)
		if pathHealth != nil {
			s.heartbeatManager.UpdatePathHealth(s.sessionID, pathID, rtt, false, int(pathHealth.HealthScore))
		}
	}
}

// Enhanced routeRawPacketToDataPlane with retransmission tracking
func (s *ClientSession) routeRawPacketToDataPlaneWithRetransmission(data []byte, targetPath transport.Path, protocolHint string, preserveOrder bool, trackRetransmission bool) error {
	// First, route the packet normally
	err := s.routeRawPacketToDataPlane(data, targetPath, protocolHint, preserveOrder)
	if err != nil {
		return err
	}

	// If retransmission tracking is enabled, track this segment
	if trackRetransmission && s.retransmissionManager != nil {
		segmentID := fmt.Sprintf("%s-%d-%d", targetPath.ID(), time.Now().UnixNano(), len(data))
		timeout := 5 * time.Second // Default timeout, could be made configurable

		err = s.trackSegmentForRetransmission(segmentID, data, targetPath.ID(), timeout)
		if err != nil {
			s.logger.Warn("Failed to track segment for retransmission", "segmentID", segmentID, "error", err)
			// Don't fail the send operation if tracking fails
		}
	}

	return nil
}

// setupOffsetCoordinatorCallbacks configures the offset coordinator callbacks
func (s *ClientSession) setupOffsetCoordinatorCallbacks() {
	if s.offsetCoordinator == nil {
		return
	}

	// Cast to implementation type to access callback methods
	if ocImpl, ok := s.offsetCoordinator.(*data.OffsetCoordinatorImpl); ok {
		// Set missing data callback - called when gaps are detected
		ocImpl.SetMissingDataCallback(func(streamID uint64, gaps []data.OffsetGap) error {
			s.logger.Debug("Requesting missing data for gaps", "streamID", streamID, "gaps", len(gaps))

			// Request retransmission of missing data through the retransmission manager
			if s.retransmissionManager != nil {
				for _, gap := range gaps {
					// Generate segment IDs for the missing data ranges
					segmentID := fmt.Sprintf("gap-%d-%d-%d", streamID, gap.Start, gap.End)

					// Create placeholder data for the gap (will be replaced by actual retransmitted data)
					gapData := make([]byte, gap.Size)

					// Track the gap for retransmission with a shorter timeout since it's missing data
					err := s.retransmissionManager.TrackSegment(segmentID, gapData, "gap-fill", 1*time.Second)
					if err != nil {
						s.logger.Warn("Failed to track gap for retransmission", "segmentID", segmentID, "error", err)
					}
				}
			}

			return nil
		})
	}
}

// startOffsetCoordinator starts the offset coordinator
func (s *ClientSession) startOffsetCoordinator() error {
	if s.offsetCoordinator == nil {
		return utils.NewKwikError(utils.ErrInvalidState, "offset coordinator not initialized", nil)
	}

	err := s.offsetCoordinator.Start()
	if err != nil {
		return utils.NewKwikError(utils.ErrInvalidState, "failed to start offset coordinator", err)
	}

	s.logger.Info("Offset coordinator started successfully")
	return nil
}

// stopOffsetCoordinator stops the offset coordinator
func (s *ClientSession) stopOffsetCoordinator() error {
	if s.offsetCoordinator == nil {
		return nil
	}

	return s.offsetCoordinator.Stop()
}

// reserveOffsetRange reserves an offset range for stream writing
func (s *ClientSession) reserveOffsetRange(streamID uint64, size int) (int64, error) {
	if s.offsetCoordinator == nil {
		return 0, utils.NewKwikError(utils.ErrInvalidState, "offset coordinator not available", nil)
	}

	return s.offsetCoordinator.ReserveOffsetRange(streamID, size)
}

// commitOffsetRange commits an offset range after successful writing
func (s *ClientSession) commitOffsetRange(streamID uint64, startOffset int64, actualSize int) error {
	if s.offsetCoordinator == nil {
		return utils.NewKwikError(utils.ErrInvalidState, "offset coordinator not available", nil)
	}

	return s.offsetCoordinator.CommitOffsetRange(streamID, startOffset, actualSize)
}

// validateOffsetContinuity checks for gaps in the offset sequence
func (s *ClientSession) validateOffsetContinuity(streamID uint64) ([]data.OffsetGap, error) {
	if s.offsetCoordinator == nil {
		return nil, utils.NewKwikError(utils.ErrInvalidState, "offset coordinator not available", nil)
	}

	return s.offsetCoordinator.ValidateOffsetContinuity(streamID)
}

// registerDataReceived registers that data has been received at a specific offset
func (s *ClientSession) registerDataReceived(streamID uint64, offset int64, size int) error {
	if s.offsetCoordinator == nil {
		return utils.NewKwikError(utils.ErrInvalidState, "offset coordinator not available", nil)
	}

	return s.offsetCoordinator.RegisterDataReceived(streamID, offset, size)
}

// getNextExpectedOffset returns the next expected offset for a stream
func (s *ClientSession) getNextExpectedOffset(streamID uint64) int64 {
	if s.offsetCoordinator == nil {
		return 0
	}

	return s.offsetCoordinator.GetNextExpectedOffset(streamID)
}

// getOffsetCoordinatorStats returns current offset coordination statistics
func (s *ClientSession) getOffsetCoordinatorStats() data.OffsetCoordinatorStats {
	if s.offsetCoordinator == nil {
		return data.OffsetCoordinatorStats{}
	}

	return s.offsetCoordinator.GetStats()
}

// setupFlowControlManager sets up the flow control manager for the session
func (s *ClientSession) setupFlowControlManager() {
	// Create a simple receive window manager for flow control
	receiveWindowManager := NewSimpleReceiveWindowManager(1024 * 1024) // 1MB initial window

	// Create control plane interface directly with session
	s.controlPlane = NewSessionControlPlane(s)

	// Create flow control manager
	flowControlConfig := DefaultFlowControlConfig()
	s.flowControlManager = NewFlowControlManager(
		s.sessionID,
		true, // isClient
		receiveWindowManager,
		s.controlPlane,
		flowControlConfig,
		s.logger,
	)

	// Set up flow control callbacks
	s.flowControlManager.SetWindowUpdateCallback(func(pathID string, windowSize uint64) {
		s.logger.Debug("Window update sent", "pathID", pathID, "windowSize", windowSize)
	})

	s.flowControlManager.SetBackpressureCallback(func(pathID string, active bool) {
		s.logger.Debug("Backpressure signal", "pathID", pathID, "active", active)

		// Update health monitor with backpressure information
		if s.healthMonitor != nil {
			s.healthMonitor.UpdatePathMetrics(s.sessionID, pathID, PathMetricsUpdate{
				Activity: !active,
			})
		}
	})

	s.flowControlManager.SetWindowFullCallback(func() {
		s.logger.Warn("Receive window is full - applying backpressure")
	})

	s.flowControlManager.SetWindowAvailableCallback(func() {
		s.logger.Debug("Receive window is available - releasing backpressure")
	})
}

// SendControlFrame sends a control frame via the specified path
func (s *ClientSession) SendControlFrame(pathID string, frame *control.ControlFrame) error {
	// Get the path
	path := s.pathManager.GetPath(pathID)
	if path == nil {
		// If specific path not found, use primary path
		path = s.primaryPath
	}

	if path == nil {
		return fmt.Errorf("no path available to send control frame")
	}

	// Get control stream
	controlStream, err := path.GetControlStream()
	if err != nil {
		return fmt.Errorf("failed to get control stream: %w", err)
	}

	// Serialize and send frame
	frameData, err := proto.Marshal(frame)
	if err != nil {
		return fmt.Errorf("failed to serialize control frame: %w", err)
	}

	_, err = controlStream.Write(frameData)
	if err != nil {
		return fmt.Errorf("failed to send control frame: %w", err)
	}

	return nil
}

// startFlowControlManager starts the flow control manager
func (s *ClientSession) startFlowControlManager() error {
	if s.flowControlManager == nil {
		return nil // Flow control not initialized
	}

	// Add primary path to flow control
	if s.primaryPath != nil {
		err := s.flowControlManager.AddPath(s.primaryPath.ID())
		if err != nil {
			return fmt.Errorf("failed to add primary path to flow control: %w", err)
		}
	}

	// Start flow control manager
	err := s.flowControlManager.Start()
	if err != nil {
		return fmt.Errorf("failed to start flow control manager: %w", err)
	}

	return nil
}

// stopFlowControlManager stops the flow control manager
func (s *ClientSession) stopFlowControlManager() error {
	if s.flowControlManager == nil {
		return nil
	}

	return s.flowControlManager.Stop()
}

// handleWindowUpdate handles incoming window update frames
func (s *ClientSession) handleWindowUpdate(frame *control.ControlFrame) {
	if s.controlPlane != nil {
		err := s.controlPlane.HandleControlFrame(frame)
		if err != nil {
			s.logger.Error("Failed to handle window update", "error", err)
		}
	}
}

// handleBackpressureSignal handles incoming backpressure signal frames
func (s *ClientSession) handleBackpressureSignal(frame *control.ControlFrame) {
	if s.controlPlane != nil {
		err := s.controlPlane.HandleControlFrame(frame)
		if err != nil {
			s.logger.Error("Failed to handle backpressure signal", "error", err)
		}
	}
}

// sendHeartbeatPacket sends an actual heartbeat packet through the specified path
func (s *ClientSession) sendHeartbeatPacket(sessionID, pathID string) error {
	// Get the path from path manager
	activePaths := s.pathManager.GetActivePaths()
	var targetPath transport.Path
	for _, p := range activePaths {
		if p.ID() == pathID {
			targetPath = p
			break
		}
	}

	if targetPath == nil {
		return fmt.Errorf("path %s not found", pathID)
	}

	// Get control stream
	controlStream, err := targetPath.GetControlStream()
	if err != nil || controlStream == nil {
		return fmt.Errorf("failed to get control stream for path %s: %v", pathID, err)
	}

	// Build heartbeat payload
	s.mutex.Lock()
	s.heartbeatSequence++
	seq := s.heartbeatSequence
	s.mutex.Unlock()

	now := time.Now().UnixNano()
	hb := &control.Heartbeat{
		SequenceNumber: seq,
		Timestamp:      uint64(now),
		EchoData:       make([]byte, 8),
	}
	binary.BigEndian.PutUint64(hb.EchoData, uint64(now))
	payload, err := proto.Marshal(hb)
	if err != nil {
		return fmt.Errorf("failed to marshal heartbeat: %v", err)
	}

	frame := &control.ControlFrame{
		FrameId:      data.GenerateFrameID(),
		Type:         control.ControlFrameType_HEARTBEAT,
		Payload:      payload,
		Timestamp:    uint64(time.Now().UnixNano()),
		SourcePathId: pathID,
		TargetPathId: "",
	}

	frameData, err := proto.Marshal(frame)
	if err != nil {
		return fmt.Errorf("failed to marshal control frame: %v", err)
	}

	// Send the heartbeat packet with no write deadline to avoid timeouts
	_ = controlStream.SetWriteDeadline(time.Time{}) // No deadline for heartbeat writes
	s.logger.Debug(fmt.Sprintf("Client sending HEARTBEAT seq=%d on path=%s (stream=%d, %d bytes)", seq, pathID, controlStream.StreamID(), len(frameData)))
	n, err := controlStream.Write(frameData)
	if err != nil {
		s.logger.Debug(fmt.Sprintf("Client heartbeat write error: %v", err))
		return fmt.Errorf("failed to write heartbeat: %v", err)
	} else {
		s.logger.Debug(fmt.Sprintf("Client heartbeat write success: %d bytes", n))
	}

	return nil
}
