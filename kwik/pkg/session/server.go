package session

import (
	"context"
	"encoding/binary"
	"fmt"
	"strings"
	"sync"
	"time"

	kwiктls "kwik/internal/tls"
	"kwik/internal/utils"
	controlpkg "kwik/pkg/control"
	"kwik/pkg/data"
	"kwik/pkg/logger"
	"kwik/pkg/protocol"
	"kwik/pkg/stream"
	"kwik/pkg/transport"
	"kwik/proto/control"

	"github.com/quic-go/quic-go"
	"google.golang.org/protobuf/proto"
)

// ServerRole defines the role of a server in the KWIK architecture
type ServerRole int

const (
	ServerRolePrimary ServerRole = iota
	ServerRoleSecondary
)

// String returns a string representation of the server role
func (r ServerRole) String() string {
	switch r {
	case ServerRolePrimary:
		return "PRIMARY"
	case ServerRoleSecondary:
		return "SECONDARY"
	default:
		return "UNKNOWN"
	}
}

// ServerSession implements the Session interface for KWIK servers
// It provides full path management capabilities including AddPath/RemovePath
type ServerSession struct {
	sessionID   string
	pathManager transport.PathManager
	primaryPath transport.Path
	isClient    bool
	state       SessionState
	createdAt   time.Time

	// Authentication management
	authManager *AuthenticationManager

	// Server role for stream isolation
	serverRole control.SessionRole

	// Stream management
	nextStreamID uint64
	streams      map[uint64]*stream.ServerStream
	streamsMutex sync.RWMutex

	// Channel for accepting incoming streams
	acceptChan chan *stream.ServerStream

	// Secondary stream aggregation
	aggregator       *data.StreamAggregator
	metadataProtocol *stream.MetadataProtocolImpl

	// Offset coordination management
	offsetCoordinator data.OffsetCoordinator

	// Health monitoring
	healthMonitor    ConnectionHealthMonitor
	heartbeatManager *HeartbeatManager

	// Control plane heartbeat system
	controlHeartbeatSystem controlpkg.ControlPlaneHeartbeatSystem

	// State management
	stateManager SessionStateManager

	// Flow control management
	flowControlManager *FlowControlManager
	controlPlane       *SessionControlPlane

	// Synchronization
	mutex  sync.RWMutex
	ctx    context.Context
	cancel context.CancelFunc

	// Configuration
	config *SessionConfig

	// Path ID mapping for synchronization with client
	pendingPathIDs map[string]string // address -> pathID mapping for pending paths
	pathIDsMutex   sync.RWMutex

	// Response channels for AddPath synchronization
	addPathResponseChans  map[string]chan *control.AddPathResponse // pathID -> response channel
	responseChannelsMutex sync.RWMutex

	// Heartbeat management (now handled by health monitor)
	heartbeatSequence uint64

	// Logger
	logger stream.StreamLogger

	// Metrics management
	metricsManager   *MetricsManager
	metricsCollector *MetricsCollector
}

// SetLogger sets the logger for this server session
func (s *ServerSession) SetLogger(l stream.StreamLogger) { s.logger = l }

// GetLogger returns the logger for this server session
func (s *ServerSession) GetLogger() stream.StreamLogger { return s.logger }

// GetMetrics returns the current session metrics
func (s *ServerSession) GetMetrics() *MetricsSummary {
	if s.metricsCollector == nil {
		return nil
	}
	return s.metricsCollector.GetMetricsSummary()
}

// GetMetricsReport returns a formatted metrics report
func (s *ServerSession) GetMetricsReport() string {
	if s.metricsCollector == nil {
		return "Metrics collection not enabled"
	}
	reporter := NewMetricsReporter(s.metricsCollector)
	return reporter.GenerateReport()
}
func (s *ServerSession) Aggregator() *data.StreamAggregator {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.aggregator
}

// ServerStream represents a server-side KWIK stream

// LoggerAdapter adapts stream.StreamLogger to data.SecondaryLogger
type LoggerAdapter struct {
	stream.StreamLogger
}

func (l *LoggerAdapter) Critical(msg string, keysAndValues ...interface{}) {
	l.StreamLogger.Error(msg, keysAndValues...)
}

// NewServerSession creates a new KWIK server session
// If logger is nil, a new DefaultServerLogger will be used
func NewServerSession(sessionID string, pathManager transport.PathManager, config *SessionConfig, log logger.Logger) *ServerSession {
	if config == nil {
		config = DefaultSessionConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Create offset coordinator
	offsetCoordinatorConfig := data.DefaultOffsetCoordinatorConfig()
	offsetCoordinatorConfig.Logger = logger.NewEnhancedLogger(logger.LogLevelDebug, "OffsetCoordinator")
	offsetCoordinator := data.NewOffsetCoordinator(offsetCoordinatorConfig)

	// Create health monitor
	healthMonitor := NewConnectionHealthMonitor(ctx)

	// Create heartbeat manager
	heartbeatConfig := DefaultHeartbeatConfig()
	heartbeatManager := NewHeartbeatManager(ctx, heartbeatConfig)

	// Create control plane heartbeat system (server mode)
	controlHeartbeatConfig := controlpkg.DefaultControlHeartbeatConfig()
	controlHeartbeatSystem := controlpkg.NewControlPlaneHeartbeatSystem(
		nil, // Will be set later when control plane is available
		controlHeartbeatConfig,
		log,
	)
	controlHeartbeatSystem.SetServerMode(true) // Server mode

	// Create metrics manager
	metricsManager := NewMetricsManager(sessionID, DefaultMetricsConfig())
	metricsCollector := NewMetricsCollector(metricsManager, DefaultMetricsCollectorConfig())
	session := &ServerSession{
		sessionID:              sessionID,
		pathManager:            pathManager,
		isClient:               false,
		state:                  SessionStateConnecting, // Commencer en état de connexion
		createdAt:              time.Now(),
		authManager:            NewAuthenticationManager(sessionID, false), // false = isServer
		serverRole:             control.SessionRole_PRIMARY,                // Default to primary role
		streams:                make(map[uint64]*stream.ServerStream),
		acceptChan:             make(chan *stream.ServerStream, 100),
		aggregator:             data.NewStreamAggregator(log),          // Initialize secondary stream aggregator
		metadataProtocol:       stream.NewMetadataProtocol(),           // Initialize metadata protocol
		offsetCoordinator:      offsetCoordinator,                      // Initialize offset coordinator
		healthMonitor:          healthMonitor,                          // Initialize health monitor
		heartbeatManager:       heartbeatManager,                       // Initialize heartbeat manager
		controlHeartbeatSystem: controlHeartbeatSystem,                 // Initialize control heartbeat system
		stateManager:           NewSessionStateManager(sessionID, log), // Initialize state manager
		ctx:                    ctx,
		cancel:                 cancel,
		config:                 config,
		pendingPathIDs:         make(map[string]string),                        // Initialize pending path IDs map
		addPathResponseChans:   make(map[string]chan *control.AddPathResponse), // Initialize response channels
		heartbeatSequence:      0,
		logger:                 log,
		metricsManager:         metricsManager,
		metricsCollector:       metricsCollector,
	}

	return session
}

// KwikListener implements the Listener interface for KWIK servers
// It wraps a QUIC listener while providing KWIK-specific functionality
type KwikListener struct {
	quicListener   *quic.Listener
	address        string
	config         *SessionConfig
	activeSessions map[string]*ServerSession
	sessionsMutex  sync.RWMutex
	ctx            context.Context
	cancel         context.CancelFunc
	closed         bool
	mutex          sync.RWMutex
	logger         logger.Logger
}

// Listen creates a KWIK listener (QUIC-compatible)
func Listen(address string, config *SessionConfig) (Listener, error) {
	listenerLogger := logger.NewEnhancedLogger(logger.LogLevelDebug, "KwikListener")
	return ListenWithLogger(address, config, listenerLogger)
}

// ListenWithLogger creates a KWIK listener (QUIC-compatible) with a custom logger
func ListenWithLogger(address string, config *SessionConfig, log logger.Logger) (Listener, error) {
	if config == nil {
		config = DefaultSessionConfig()
	}

	// Create QUIC listener
	tlsConfig, err := kwiктls.GenerateTLSConfig()
	if err != nil {
		return nil, utils.NewKwikError(utils.ErrConnectionLost,
			fmt.Sprintf("failed to generate TLS config: %v", err), err)
	}
	quicListener, err := quic.ListenAddr(address, tlsConfig, nil)
	if err != nil {
		return nil, utils.NewKwikError(utils.ErrConnectionLost,
			fmt.Sprintf("failed to listen on %s", address), err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	listener := &KwikListener{
		quicListener:   quicListener,
		address:        address,
		config:         config,
		activeSessions: make(map[string]*ServerSession),
		ctx:            ctx,
		cancel:         cancel,
		closed:         false,
		logger:         log,
	}

	return listener, nil
}

// Accept accepts a new KWIK session (QUIC-compatible)
func (l *KwikListener) Accept(ctx context.Context) (Session, error) {
	return l.AcceptWithConfig(ctx, l.config)
}

// AcceptWithConfig accepts a new KWIC session with specific configuration
func (l *KwikListener) AcceptWithConfig(ctx context.Context, config *SessionConfig) (Session, error) {
	l.mutex.RLock()
	if l.closed {
		l.mutex.RUnlock()
		return nil, utils.NewKwikError(utils.ErrConnectionLost, "listener is closed", nil)
	}
	l.mutex.RUnlock()

	if config == nil {
		config = l.config
	}

	// Accept QUIC connection
	conn, err := l.quicListener.Accept(ctx)
	if err != nil {
		return nil, utils.NewKwikError(utils.ErrConnectionLost,
			"failed to accept connection", err)
	}

	// Create path manager and primary path
	pathManager := transport.NewPathManager()
	primaryPath, err := pathManager.CreatePathFromConnection(conn)
	if err != nil {
		conn.CloseWithError(0, "failed to create primary path")
		return nil, utils.NewKwikError(utils.ErrConnectionLost,
			"failed to create primary path", err)
	}

	// Create server session with temporary session ID (will be updated during authentication)
	tempSessionID := generateSessionID()
	log := l.logger
	if log == nil {
		log = logger.NewEnhancedLogger(logger.LogLevelDebug, "ServerSession")
	}
	session := NewServerSession(tempSessionID, pathManager, config, log)

	// SERVER ACCEPTS THE CONTROL STREAM CREATED BY CLIENT (AcceptStream)
	// This is the key fix - server accepts the stream created by client
	session.logger.Debug("Server attempting to accept control stream...")
	controlStream, err := primaryPath.AcceptControlStreamAsServer()
	if err != nil {
		session.logger.Debug(fmt.Sprintf("Server FAILED to accept control stream: %v", err))
		conn.CloseWithError(0, "failed to accept control stream")
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"failed to accept control stream as server", err)
	}
	session.logger.Debug(fmt.Sprintf("Server successfully accepted control stream (ID=%d)", controlStream.StreamID()))
	session.primaryPath = primaryPath

	// Mark primary path as default for operations (Requirement 2.5)
	err = session.markPrimaryPathAsDefault()
	if err != nil {
		session.Close() // Clean up on failure
		return nil, utils.NewKwikError(utils.ErrConnectionLost,
			"failed to mark primary path as default", err)
	}

	// Handle authentication over control plane stream and get client's session ID
	clientSessionID, err := session.handleAuthenticationWithSessionID(ctx)
	if err != nil {
		session.Close() // Clean up on authentication failure
		return nil, utils.NewKwikError(utils.ErrAuthenticationFailed,
			"authentication failed during session establishment", err)
	}

	// Update session ID to match client's session ID
	session.sessionID = clientSessionID
	if session.authManager != nil {
		session.authManager.sessionID = clientSessionID
	}

	// Add session to active sessions using the client's session ID
	l.sessionsMutex.Lock()
	l.activeSessions[clientSessionID] = session
	l.sessionsMutex.Unlock()

	// Start session management
	go session.managePaths()

	// Start control frame handler to process AddPathResponse and other control messages
	go session.handleControlFrames()

	// Set up and start offset coordinator
	session.setupOffsetCoordinatorCallbacks()
	err = session.startOffsetCoordinator()
	if err != nil {
		session.Close()
		return nil, utils.NewKwikError(utils.ErrInvalidState, "failed to start offset coordinator", err)
	}

	// Attendre que l'authentification soit terminée avant de continuer
	for !session.stateManager.IsAuthenticationComplete() {
		time.Sleep(10 * time.Millisecond)
	}

	// Changer vers l'état actif
	if err := session.stateManager.SetState(SessionStateActive); err != nil {
		session.logger.Error("Failed to set active state", "error", err)
		session.Close()
		return nil, utils.NewKwikError(utils.ErrInvalidState, "failed to set active state", err)
	}
	session.logger.Debug("Server session state set to ACTIVE", "sessionID", session.sessionID)

	// Start control plane heartbeats now that session is active (server mode - will respond to requests)
	if session.controlHeartbeatSystem != nil && session.primaryPath != nil {
		err := session.controlHeartbeatSystem.StartControlHeartbeats(session.sessionID, session.primaryPath.ID())
		if err != nil {
			session.logger.Error("Failed to start control heartbeats", "error", err)
			// Don't fail session creation for heartbeat issues, just log the error
		} else {
			session.logger.Debug("Control heartbeats started", "sessionID", session.sessionID, "pathID", session.primaryPath.ID())
		}
	}

	// Start the health monitor after authentication is complete
	err = session.startHealthMonitor()
	if err != nil {
		session.logger.Error("Failed to start health monitor after authentication", "error", err)
		session.Close()
		return nil, utils.NewKwikError(utils.ErrInvalidState, "failed to start health monitor", err)
	}

	// Start heartbeat management for existing paths
	session.startHeartbeatManagement()

	// Heartbeat management is now handled by the health monitor

	// Set up flow control manager
	session.setupFlowControlManager()

	// Start flow control manager
	err = session.startFlowControlManager()
	if err != nil {
		session.Close()
		return nil, utils.NewKwikError(utils.ErrInvalidState, "failed to start flow control manager", err)
	}

	// Note: Stream handling is now on-demand via AcceptStream() calls

	// Clean up session when it closes
	go l.cleanupSession(session)

	return session, nil
}

// Close closes the listener and all active sessions
func (l *KwikListener) Close() error {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	if l.closed {
		return nil // Already closed
	}

	l.closed = true
	l.cancel()

	// Close all active sessions
	l.sessionsMutex.Lock()
	for _, session := range l.activeSessions {
		session.Close()
	}
	l.sessionsMutex.Unlock()

	// Close underlying QUIC listener
	return l.quicListener.Close()
}

// Addr returns the listener's network address
func (l *KwikListener) Addr() string {
	return l.address
}

// SetSessionConfig updates the default session configuration
func (l *KwikListener) SetSessionConfig(config *SessionConfig) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	l.config = config
}

// GetActiveSessionCount returns the number of active sessions
func (l *KwikListener) GetActiveSessionCount() int {
	l.sessionsMutex.RLock()
	defer l.sessionsMutex.RUnlock()
	return len(l.activeSessions)
}

// cleanupSession removes a session from active sessions when it closes
func (l *KwikListener) cleanupSession(session *ServerSession) {
	// Wait for session to close
	<-session.ctx.Done()

	// Remove from active sessions
	l.sessionsMutex.Lock()
	delete(l.activeSessions, session.sessionID)
	l.sessionsMutex.Unlock()
}

// AcceptSession accepts a new KWIK session from a listener with a default logger
func AcceptSession(listener *quic.Listener) (Session, error) {
	listenerLogger := logger.NewEnhancedLogger(logger.LogLevelDebug, "KwikListener")
	return AcceptSessionWithLogger(listener, listenerLogger)
}

// AcceptSessionWithLogger accepts a new KWIK session from a listener with a custom logger
func AcceptSessionWithLogger(listener *quic.Listener, log logger.Logger) (Session, error) {
	// Accept QUIC connection
	conn, err := listener.Accept(context.Background())
	if err != nil {
		return nil, utils.NewKwikError(utils.ErrConnectionLost,
			"failed to accept connection", err)
	}

	// Create path manager and primary path
	pathManager := transport.NewPathManager()
	primaryPath, err := pathManager.CreatePathFromConnection(conn)
	if err != nil {
		conn.CloseWithError(0, "failed to create primary path")
		return nil, utils.NewKwikError(utils.ErrConnectionLost,
			"failed to create primary path", err)
	}

	// Generate session ID
	sessionID := generateSessionID()

	// Create server session
	if log == nil {
		log = logger.NewEnhancedLogger(logger.LogLevelDebug, "[ServerSession]")
	}
	session := NewServerSession(sessionID, pathManager, nil, log)
	session.primaryPath = primaryPath

	// Start session management
	go session.managePaths()

	// Set up and start offset coordinator
	session.setupOffsetCoordinatorCallbacks()
	err = session.startOffsetCoordinator()
	if err != nil {
		session.Close()
		return nil, utils.NewKwikError(utils.ErrInvalidState, "failed to start offset coordinator", err)
	}

	// Note: Stream handling is now on-demand via AcceptStream() calls

	return session, nil
}

// OpenStreamSync opens a new stream synchronously (QUIC-compatible)
// Behavior depends on server role:
// - PRIMARY servers: open streams on the public client session
// - SECONDARY servers: open internal streams that will be aggregated client-side
func (s *ServerSession) OpenStreamSync(ctx context.Context) (Stream, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.stateManager != nil && !s.stateManager.IsActive() {
		return nil, utils.NewKwikError(utils.ErrConnectionLost, "session is not active", nil)
	}

	// Generate new stream ID
	streamID := s.nextStreamID
	s.nextStreamID++

	// Behavior depends on server role
	if s.serverRole == control.SessionRole_PRIMARY {
		// PRIMARY SERVER: Open stream on public client session (original behavior)
		return s.openPrimaryStream(ctx, streamID)
	} else {
		// SECONDARY SERVER: Open internal stream for aggregation
		s.logger.Info(fmt.Sprintf("[SECONDARY] Opening secondary stream %d for aggregation", streamID))
		return s.openSecondaryStream(ctx, streamID)
	}
}

// openPrimaryStream opens a stream on the public client session (for primary servers)
func (s *ServerSession) openPrimaryStream(ctx context.Context, streamID uint64) (Stream, error) {
	// Ensure primary path is available and active (Requirement 2.5)
	if s.primaryPath == nil {
		return nil, utils.NewKwikError(utils.ErrConnectionLost, "no primary path available", nil)
	}

	// Check if primary path is suitable for stream operations (dead path detection)
	err := s.checkPathForOperation(s.primaryPath.ID(), "OpenStreamSync")
	if err != nil {
		return nil, err
	}

	if !s.primaryPath.IsActive() {
		// Send dead path notification for primary path failure
		s.sendDeadPathNotification(s.primaryPath.ID(), "stream_creation_failed_primary_path_dead")
		return nil, utils.NewKwikError(utils.ErrPathDead, "primary path is not active", nil)
	}

	// Create actual QUIC stream on primary path
	conn := s.primaryPath.GetConnection()
	if conn == nil {
		return nil, utils.NewKwikError(utils.ErrConnectionLost, "no QUIC connection available", nil)
	}

	quicStream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed, "failed to create QUIC stream", err)
	}

	// Create logical stream on primary path (default path for operations)
	serverStream := stream.NewServerStream(streamID, s.primaryPath.ID(), s, quicStream)

	s.streamsMutex.Lock()
	s.streams[streamID] = serverStream
	s.streamsMutex.Unlock()

	return serverStream, nil
}

// GetContext returns the context associated with this session
func (s *ServerSession) GetContext() context.Context {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.ctx
}
func (s *ServerSession) RemoveStream(streamID uint64) {
	s.streamsMutex.Lock()
	defer s.streamsMutex.Unlock()
	if stream, exists := s.streams[streamID]; exists {
		stream.Close()              // Close the stream
		delete(s.streams, streamID) // Remove from map
		s.logger.Info(fmt.Sprintf("[SECONDARY] Stream %d removed from session", streamID))
	} else {
		s.logger.Warn(fmt.Sprintf("[SECONDARY] Attempted to remove non-existent stream %d", streamID))
	}
}

// openSecondaryStream opens an internal stream for secondary servers (will be aggregated client-side)
func (s *ServerSession) openSecondaryStream(ctx context.Context, streamID uint64) (Stream, error) {
	// For secondary servers, we create a stream that will be handled internally
	// and aggregated on the client side according to the secondary stream isolation architecture

	// Ensure primary path is available (secondary servers still use the primary path for communication)
	if s.primaryPath == nil {
		return nil, utils.NewKwikError(utils.ErrConnectionLost, "no primary path available", nil)
	}

	// Create actual QUIC stream on primary path (this will be detected as secondary by client)
	conn := s.primaryPath.GetConnection()
	if conn == nil {
		return nil, utils.NewKwikError(utils.ErrConnectionLost, "no QUIC connection available", nil)
	}

	quicStream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed, "failed to create secondary QUIC stream", err)
	}

	s.logger.Info(fmt.Sprintf("[SECONDARY] Created QUIC stream for secondary stream %d", streamID))

	// Create secondary stream wrapper that will handle metadata encapsulation
	serverStream := stream.NewSecondaryServerStream(streamID, s.primaryPath.ID(), s, quicStream)

	s.streamsMutex.Lock()
	s.streams[streamID] = serverStream
	s.streamsMutex.Unlock()

	s.logger.Info(fmt.Sprintf("[SECONDARY] Secondary stream %d created and ready for aggregation", streamID))
	return serverStream, nil
}

// OpenStream opens a new stream asynchronously (QUIC-compatible)
func (s *ServerSession) OpenStream() (Stream, error) {
	return s.OpenStreamSync(context.Background())
}

// AcceptStream accepts an incoming stream (QUIC-compatible)
// This method blocks until a stream is available or context is cancelled
func (s *ServerSession) AcceptStream(ctx context.Context) (Stream, error) {
	// Get the QUIC connection from primary path
	if s.primaryPath == nil {
		return nil, utils.NewKwikError(utils.ErrConnectionLost, "no primary path available", nil)
	}

	conn := s.primaryPath.GetConnection()
	if conn == nil {
		return nil, utils.NewKwikError(utils.ErrConnectionLost, "no QUIC connection available", nil)
	}

	// Accept incoming QUIC stream from client (this blocks until a stream arrives)
	quicStream, err := conn.AcceptStream(ctx)
	if err != nil {
		return nil, utils.NewKwikError(utils.ErrConnectionLost, "failed to accept stream", err)
	}

	// Create KWIK ServerStream wrapper
	s.mutex.Lock()
	streamID := s.nextStreamID
	s.nextStreamID++
	s.mutex.Unlock()
	serverStream := stream.NewServerStream(streamID, s.primaryPath.ID(), s, quicStream)

	// Store in streams map
	s.streamsMutex.Lock()
	s.streams[serverStream.ID()] = serverStream
	s.streamsMutex.Unlock()

	return serverStream, nil
}

// AddPath requests the client to establish a secondary path
func (s *ServerSession) AddPath(address string) error {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if s.stateManager != nil && !s.stateManager.IsActive() {
		return utils.NewKwikError(utils.ErrConnectionLost, "session is not active", nil)
	}

	// Validate input
	if address == "" {
		return utils.NewKwikError(utils.ErrInvalidFrame, "target address cannot be empty", nil)
	}

	// Get control stream from primary path
	controlStream, err := s.primaryPath.GetControlStream()
	if err != nil {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			"failed to get control stream for AddPath request", err)
	}

	// Generate unique path ID that both server and client will use
	pathID := generatePathID()

	// Create AddPathRequest protobuf message
	addPathReq := &control.AddPathRequest{
		TargetAddress: address,
		SessionId:     s.sessionID,
		Priority:      1, // Default priority for secondary paths
		Metadata:      make(map[string]string),
		PathId:        pathID, // Server-generated path ID for synchronization
	}

	// Add metadata for tracking
	addPathReq.Metadata["request_time"] = fmt.Sprintf("%d", time.Now().UnixNano())
	addPathReq.Metadata["server_id"] = s.sessionID

	// Serialize the AddPathRequest
	payload, err := proto.Marshal(addPathReq)
	if err != nil {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			"failed to serialize AddPathRequest", err)
	}

	// Create control frame
	frame := &control.ControlFrame{
		FrameId:      data.GenerateFrameID(),
		Type:         control.ControlFrameType_ADD_PATH_REQUEST,
		Payload:      payload,
		Timestamp:    uint64(time.Now().UnixNano()),
		SourcePathId: s.primaryPath.ID(),
		TargetPathId: "", // Will be filled by client
	}

	// Serialize and send control frame
	frameData, err := proto.Marshal(frame)
	if err != nil {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			"failed to serialize AddPath control frame", err)
	}

	_, err = controlStream.Write(frameData)
	if err != nil {
		return utils.NewKwikError(utils.ErrConnectionLost,
			"failed to send AddPath request", err)
	}

	// Store the generated path ID for later reference
	s.pathIDsMutex.Lock()
	s.pendingPathIDs[address] = pathID
	s.pathIDsMutex.Unlock()

	// Create a virtual path entry for the server to track the client's secondary path
	// This allows SendRawData to find the path ID even though the actual connection is on the client
	err = s.createVirtualPath(pathID, address)
	if err != nil {
		// Clean up pending path ID if virtual path creation fails
		s.pathIDsMutex.Lock()
		delete(s.pendingPathIDs, address)
		s.pathIDsMutex.Unlock()
		return utils.NewKwikError(utils.ErrConnectionLost,
			"failed to create virtual path for secondary server", err)
	}

	// Wait for AddPathResponse from client with timeout (synchronous)
	response, err := s.waitForAddPathResponse(pathID, 10*time.Second)
	if err != nil {
		// Clean up pending path ID on failure
		s.pathIDsMutex.Lock()
		delete(s.pendingPathIDs, address)
		s.pathIDsMutex.Unlock()
		return utils.NewKwikError(utils.ErrConnectionLost,
			fmt.Sprintf("failed to receive AddPathResponse for %s", address), err)
	}

	// Check if the client successfully created the secondary path
	if !response.Success {
		// Clean up pending path ID on failure
		s.pathIDsMutex.Lock()
		delete(s.pendingPathIDs, address)
		s.pathIDsMutex.Unlock()
		return utils.NewKwikError(utils.ErrConnectionLost,
			fmt.Sprintf("client failed to create secondary path to %s: %s", address, response.ErrorMessage), nil)
	}

	// Success! The client has confirmed the secondary path is active
	// The path ID remains in pendingPathIDs and can now be used for SendRawData
	return nil
}

// createVirtualPath creates a virtual path entry for tracking client's secondary paths
func (s *ServerSession) createVirtualPath(pathID string, address string) error {
	// Create a virtual path that represents the client's secondary path
	// This is needed so the server can track path IDs for SendRawData operations
	// even though the actual QUIC connection exists on the client side

	// We need to create a path entry in the path manager so that SendRawData can find it
	// Since we can't create an actual QUIC connection to the secondary server from the server side,
	// we'll create a virtual path that exists only for tracking purposes

	// For now, we'll use a workaround by directly adding the path to the path manager
	// In a full implementation, this would be a special "virtual" path type

	// For now, we'll just return success since the path ID is already stored in pendingPathIDs
	// and SendRawData has been modified to handle pending paths specially
	// In a full implementation, this would create a proper virtual path entry

	return nil
}

// GetPendingPathID retrieves the path ID for a pending path by address
func (s *ServerSession) GetPendingPathID(address string) string {
	s.pathIDsMutex.RLock()
	defer s.pathIDsMutex.RUnlock()

	return s.pendingPathIDs[address]
}

// GetStreamAggregator returns the secondary stream aggregator
func (s *ServerSession) GetStreamAggregator() *data.StreamAggregator {
	return s.aggregator
}

// GetMetadataProtocol returns the metadata protocol instance
func (s *ServerSession) GetMetadataProtocol() *stream.MetadataProtocolImpl {
	return s.metadataProtocol
}

// SetServerRole sets the server role (PRIMARY or SECONDARY)
func (s *ServerSession) SetServerRole(role control.SessionRole) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.serverRole = role
	s.logger.Info(fmt.Sprintf("[SERVER] Role set to: %s", role.String()))
}

// GetServerRole returns the current server role
func (s *ServerSession) GetServerRole() control.SessionRole {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.serverRole
}

// waitForAddPathResponse waits for AddPathResponse from client with timeout
func (s *ServerSession) waitForAddPathResponse(pathID string, timeout time.Duration) (*control.AddPathResponse, error) {
	// Create a channel to receive the response for this specific pathID
	responseChan := make(chan *control.AddPathResponse, 1)

	// Register the response channel for this pathID
	s.responseChannelsMutex.Lock()
	s.addPathResponseChans[pathID] = responseChan
	s.responseChannelsMutex.Unlock()

	// Ensure cleanup of the channel when we're done
	defer func() {
		s.responseChannelsMutex.Lock()
		delete(s.addPathResponseChans, pathID)
		close(responseChan)
		s.responseChannelsMutex.Unlock()
	}()

	// Wait for response or timeout
	select {
	case response := <-responseChan:
		if response != nil {
			return response, nil
		}
		return nil, utils.NewKwikError(utils.ErrConnectionLost, "received nil AddPathResponse", nil)
	case <-time.After(timeout):
		return nil, utils.NewKwikError(utils.ErrConnectionLost,
			fmt.Sprintf("timeout waiting for AddPathResponse for path %s", pathID), nil)
	}
}

// handleControlFrames handles incoming control frames from the client
func (s *ServerSession) handleControlFrames() {
	defer func() {
		if r := recover(); r != nil {
			// Log panic and continue
			s.logger.Debug(fmt.Sprintf("Server control frame handler panic: %v", r))
		}
	}()

	// Get control stream from primary path
	controlStream, err := s.primaryPath.GetControlStream()
	if err != nil {
		s.logger.Debug(fmt.Sprintf("Server failed to get control stream for frame handling: %v", err))
		return
	}

	s.logger.Debug(fmt.Sprintf("Server control frame handler started, listening for client responses... (stream=%d)", controlStream.StreamID()))

	for {
		// Check if session is still active
		select {
		case <-s.ctx.Done():
			s.logger.Debug(fmt.Sprintf("Server control frame handler stopping (session closed)"))
			return
		default:
		}

		// Read control frame from client
		buffer := make([]byte, 4096)
		_ = controlStream.SetReadDeadline(time.Now().Add(5 * utils.DefaultKeepAliveInterval))
		n, err := controlStream.Read(buffer)
		if err != nil {
			// Ignore timeouts
			if strings.Contains(strings.ToLower(err.Error()), "timeout") || strings.Contains(strings.ToLower(err.Error()), "no recent network activity") {
				time.Sleep(5 * time.Millisecond)
				continue
			}
			s.logger.Debug(fmt.Sprintf("Server control frame read error: %v", err))
			return
		}

		s.logger.Debug(fmt.Sprintf("Server received control frame (%d bytes)", n))

		// Parse control frame
		var frame control.ControlFrame
		err = proto.Unmarshal(buffer[:n], &frame)
		if err != nil {
			s.logger.Debug(fmt.Sprintf("Server failed to parse control frame: %v", err))
			continue
		}

		s.logger.Debug(fmt.Sprintf("Server processing control frame type: %s", frame.Type))

		// Handle different control frame types
		switch frame.Type {
		case control.ControlFrameType_ADD_PATH_RESPONSE:
			s.handleAddPathResponse(&frame)
		case control.ControlFrameType_PATH_STATUS_NOTIFICATION:
			s.handlePathStatusNotification(&frame)
		case control.ControlFrameType_HEARTBEAT:
			s.handleHeartbeat(&frame)
		case control.ControlFrameType_HEARTBEAT_RESPONSE:
			s.handleHeartbeatResponse(&frame)
		case control.ControlFrameType_DATA_CHUNK_ACK:
			s.handleDataChunkAck(&frame)
		case control.ControlFrameType_PACKET_ACK:
			s.handlePacketAck(&frame)
		case control.ControlFrameType_WINDOW_UPDATE:
			s.handleWindowUpdate(&frame)
		case control.ControlFrameType_BACKPRESSURE_SIGNAL:
			s.handleBackpressureSignal(&frame)
		default:
			s.logger.Debug(fmt.Sprintf("Server ignoring unknown control frame type: %s", frame.Type))
		}
	}
}

// handleAddPathResponse processes AddPathResponse frames from client
func (s *ServerSession) handleAddPathResponse(frame *control.ControlFrame) {
	// Parse AddPathResponse payload
	var response control.AddPathResponse
	err := proto.Unmarshal(frame.Payload, &response)
	if err != nil {
		s.logger.Debug(fmt.Sprintf("Server failed to parse AddPathResponse: %v", err))
		return
	}

	s.logger.Debug(fmt.Sprintf("Server received AddPathResponse for path %s, success: %t", response.PathId, response.Success))

	// Find the response channel for this pathID
	s.responseChannelsMutex.RLock()
	responseChan, exists := s.addPathResponseChans[response.PathId]
	s.responseChannelsMutex.RUnlock()

	if !exists {
		s.logger.Debug(fmt.Sprintf("Server no response channel found for path %s", response.PathId))
		return
	}

	// Send response to waiting AddPath method
	select {
	case responseChan <- &response:
		s.logger.Debug(fmt.Sprintf("Server routed AddPathResponse to waiting channel for path %s", response.PathId))
	default:
		s.logger.Debug(fmt.Sprintf("Server response channel full or closed for path %s", response.PathId))
	}
}

// handleHeartbeat processes Heartbeat frames from the client using the new heartbeat system
func (s *ServerSession) handleHeartbeat(frame *control.ControlFrame) {
	if frame == nil || len(frame.Payload) == 0 {
		s.logger.Debug("Server received empty heartbeat frame")
		return
	}

	s.logger.Debug(fmt.Sprintf("Server received HEARTBEAT frame (%d bytes)", len(frame.Payload)))

	// Deserialize the binary heartbeat frame from the payload
	heartbeatFrame := &protocol.HeartbeatFrame{}
	err := heartbeatFrame.Deserialize(frame.Payload)
	if err != nil {
		s.logger.Debug(fmt.Sprintf("Server failed to deserialize heartbeat frame: %v", err))
		return
	}

	s.logger.Debug(fmt.Sprintf("Server received HEARTBEAT seq=%d from path=%s", heartbeatFrame.SequenceID, heartbeatFrame.PathID))

	// Pass to the new heartbeat system for processing
	if s.controlHeartbeatSystem != nil {
		err := s.controlHeartbeatSystem.HandleControlHeartbeatRequest(heartbeatFrame)
		if err != nil {
			s.logger.Debug(fmt.Sprintf("Server heartbeat system failed to handle request: %v", err))
		} else {
			s.logger.Debug(fmt.Sprintf("Server successfully processed heartbeat seq=%d", heartbeatFrame.SequenceID))
		}
	} else {
		s.logger.Debug("Server heartbeat system not available")
	}
}

// handleHeartbeatResponse processes HeartbeatResponse frames from the client
func (s *ServerSession) handleHeartbeatResponse(frame *control.ControlFrame) {
	if frame == nil || len(frame.Payload) == 0 {
		s.logger.Debug("Server received empty heartbeat response frame")
		return
	}

	s.logger.Debug(fmt.Sprintf("Server received HEARTBEAT_RESPONSE frame (%d bytes)", len(frame.Payload)))

	// Deserialize the binary heartbeat response frame from the payload
	heartbeatResponseFrame := &protocol.HeartbeatResponseFrame{}
	err := heartbeatResponseFrame.Deserialize(frame.Payload)
	if err != nil {
		s.logger.Debug(fmt.Sprintf("Server failed to deserialize heartbeat response frame: %v", err))
		return
	}

	s.logger.Debug(fmt.Sprintf("Server received HEARTBEAT_RESPONSE seq=%d from path=%s", heartbeatResponseFrame.RequestSequenceID, heartbeatResponseFrame.PathID))

	// Pass to the heartbeat system for processing (servers don't typically process responses, but we log it)
	if s.controlHeartbeatSystem != nil {
		err := s.controlHeartbeatSystem.HandleControlHeartbeatResponse(heartbeatResponseFrame)
		if err != nil {
			s.logger.Debug(fmt.Sprintf("Server heartbeat system failed to handle response: %v", err))
		} else {
			s.logger.Debug(fmt.Sprintf("Server successfully processed heartbeat response seq=%d", heartbeatResponseFrame.RequestSequenceID))
		}
	} else {
		s.logger.Debug("Server heartbeat system not available for response processing")
	}
}

// handlePathStatusNotification processes PathStatusNotification frames from client
func (s *ServerSession) handlePathStatusNotification(frame *control.ControlFrame) {
	// Parse PathStatusNotification payload
	var notification control.PathStatusNotification
	err := proto.Unmarshal(frame.Payload, &notification)
	if err != nil {
		s.logger.Debug(fmt.Sprintf("Server failed to parse PathStatusNotification: %v", err))
		return
	}

	s.logger.Debug(fmt.Sprintf("Server received PathStatusNotification for path %s, status: %s",
		notification.PathId, notification.Status.String()))

	// TODO: Handle path status changes
	// This could update internal path state, trigger notifications, etc.
}

// handleDataChunkAck processes DataChunkAck frames from client
func (s *ServerSession) handleDataChunkAck(frame *control.ControlFrame) {
	var ack control.DataChunkAck
	if err := proto.Unmarshal(frame.Payload, &ack); err != nil {
		return
	}
	s.logger.Debug(fmt.Sprintf("Server received DataChunkAck: path=%s stream=%d offset=%d size=%d",
		ack.PathId, ack.KwikStreamId, ack.Offset, ack.Size))
	// Route ACK to the corresponding ServerStream by matching remoteStreamID
	s.streamsMutex.RLock()
	for _, srvStream := range s.streams {
		match := srvStream.RemoteStreamID() == ack.KwikStreamId
		if match {
			srvStream.HandleAck(ack.Offset, ack.Size)
		}
	}
	s.streamsMutex.RUnlock()
}

// handlePacketAck processes PacketAck frames from client
func (s *ServerSession) handlePacketAck(frame *control.ControlFrame) {
	var ack control.PacketAck
	if err := proto.Unmarshal(frame.Payload, &ack); err != nil {
		return
	}
	// Remove pending packet by packet_id in all streams
	s.streamsMutex.RLock()
	for _, srvStream := range s.streams {
		segments := srvStream.PendingSegments()
		if segments != nil {
			if _, ok := segments[ack.PacketId]; ok {
				srvStream.DeletePendingSegment(ack.PacketId)
			}
		}
	}
	s.streamsMutex.RUnlock()
}

// RemovePath requests removal of a secondary path with proper cleanup and notification
// Requirements: 6.2 - server can request path removal with graceful shutdown
func (s *ServerSession) RemovePath(pathID string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.stateManager != nil && !s.stateManager.IsActive() {
		return utils.NewKwikError(utils.ErrConnectionLost, "session is not active", nil)
	}

	// Validate path exists
	path := s.pathManager.GetPath(pathID)
	if path == nil {
		return utils.NewPathNotFoundError(pathID)
	}

	// Cannot remove primary path
	if path.IsPrimary() {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			"cannot remove primary path", nil)
	}

	// Get control stream from primary path to send removal request
	controlStream, err := s.primaryPath.GetControlStream()
	if err != nil {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			"failed to get control stream for RemovePath request", err)
	}

	// Create RemovePathRequest protobuf message
	removePathReq := &control.RemovePathRequest{
		PathId:    pathID,
		Reason:    "server_requested_removal",
		Graceful:  true,                                              // Request graceful shutdown
		TimeoutMs: uint32(utils.GracefulCloseTimeout.Milliseconds()), // Timeout for graceful close
	}

	// Serialize the RemovePathRequest
	payload, err := proto.Marshal(removePathReq)
	if err != nil {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			"failed to serialize RemovePathRequest", err)
	}

	// Create control frame
	frame := &control.ControlFrame{
		FrameId:      data.GenerateFrameID(),
		Type:         control.ControlFrameType_REMOVE_PATH_REQUEST,
		Payload:      payload,
		Timestamp:    uint64(time.Now().UnixNano()),
		SourcePathId: s.primaryPath.ID(),
		TargetPathId: pathID,
	}

	// Serialize and send control frame
	frameData, err := proto.Marshal(frame)
	if err != nil {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			"failed to serialize RemovePath control frame", err)
	}

	_, err = controlStream.Write(frameData)
	if err != nil {
		return utils.NewKwikError(utils.ErrConnectionLost,
			"failed to send RemovePath request", err)
	}

	// Perform graceful path shutdown with traffic redistribution
	err = s.performGracefulPathShutdown(pathID)
	if err != nil {
		// Log the error but continue with removal
		// In production, this should be logged properly
		_ = err
	}

	// Remove path from path manager
	err = s.pathManager.RemovePath(pathID)
	if err != nil {
		return utils.NewKwikError(utils.ErrConnectionLost,
			"failed to remove path from path manager", err)
	}

	// Send path status notification to inform about removal
	err = s.sendPathStatusNotification(pathID, control.PathStatus_CONTROL_PATH_DEAD, "path_removed_by_server")
	if err != nil {
		// Log the error but don't fail the removal
		// In production, this should be logged properly
		_ = err
	}

	return nil
}

// performGracefulPathShutdown handles graceful shutdown with traffic redistribution
func (s *ServerSession) performGracefulPathShutdown(pathID string) error {
	// Get the path to be removed
	path := s.pathManager.GetPath(pathID)
	if path == nil {
		return utils.NewPathNotFoundError(pathID)
	}

	// TODO: Implement traffic redistribution logic
	// This should:
	// 1. Identify all streams currently using this path
	// 2. Migrate active streams to other available paths
	// 3. Wait for in-flight data to be acknowledged
	// 4. Close data streams gracefully
	// 5. Close control stream last

	// For now, we'll perform basic cleanup
	// Get data streams from the path
	dataStreams := path.GetDataStreams()

	// Close data streams gracefully
	for _, stream := range dataStreams {
		if stream != nil {
			// TODO: Migrate any active logical streams to other paths
			// For now, just close the stream
			stream.Close()
		}
	}

	// Wait a brief moment for graceful closure
	time.Sleep(100 * time.Millisecond)

	return nil
}

// sendPathStatusNotification sends a path status notification via control plane
func (s *ServerSession) sendPathStatusNotification(pathID string, status control.PathStatus, reason string) error {
	// Get control stream from primary path
	controlStream, err := s.primaryPath.GetControlStream()
	if err != nil {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			"failed to get control stream for path status notification", err)
	}

	// Create path metrics (basic implementation)
	metrics := &control.PathMetrics{
		RttMs:          50,      // Placeholder - should be actual RTT
		BandwidthBps:   1000000, // Placeholder - should be actual bandwidth
		PacketLossRate: 0.0,     // Placeholder - should be actual loss rate
		BytesSent:      0,       // Placeholder - should be actual bytes sent
		BytesReceived:  0,       // Placeholder - should be actual bytes received
		LastActivity:   uint64(time.Now().UnixNano()),
	}

	// Create PathStatusNotification protobuf message
	statusNotification := &control.PathStatusNotification{
		PathId:    pathID,
		Status:    status,
		Reason:    reason,
		Timestamp: uint64(time.Now().UnixNano()),
		Metrics:   metrics,
	}

	// Serialize the PathStatusNotification
	payload, err := proto.Marshal(statusNotification)
	if err != nil {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			"failed to serialize PathStatusNotification", err)
	}

	// Create control frame
	frame := &control.ControlFrame{
		FrameId:      data.GenerateFrameID(),
		Type:         control.ControlFrameType_PATH_STATUS_NOTIFICATION,
		Payload:      payload,
		Timestamp:    uint64(time.Now().UnixNano()),
		SourcePathId: s.primaryPath.ID(),
		TargetPathId: "", // Broadcast notification
	}

	// Serialize and send control frame
	frameData, err := proto.Marshal(frame)
	if err != nil {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			"failed to serialize path status notification control frame", err)
	}

	_, err = controlStream.Write(frameData)
	if err != nil {
		return utils.NewKwikError(utils.ErrConnectionLost,
			"failed to send path status notification", err)
	}

	return nil
}

// GetActivePaths returns all active paths with proper PathInfo structures
// Requirements: 6.4 - server can query active paths
func (s *ServerSession) GetActivePaths() []PathInfo {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	activePaths := s.pathManager.GetActivePaths()
	pathInfos := make([]PathInfo, len(activePaths))

	for i, path := range activePaths {
		pathInfos[i] = s.createPathInfo(path, PathStatusActive)
	}

	return pathInfos
}

// GetDeadPaths returns all dead paths with proper PathInfo structures
// Requirements: 6.5 - server can query dead paths
func (s *ServerSession) GetDeadPaths() []PathInfo {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	deadPaths := s.pathManager.GetDeadPaths()
	pathInfos := make([]PathInfo, len(deadPaths))

	for i, path := range deadPaths {
		pathInfos[i] = s.createPathInfo(path, PathStatusDead)
	}

	return pathInfos
}

// GetSessionRole returns the role of this session (PRIMARY or SECONDARY)
func (s *ServerSession) GetSessionRole() control.SessionRole {
	if s.authManager != nil {
		return s.authManager.GetSessionRole()
	}
	return control.SessionRole_PRIMARY // Default to PRIMARY if no auth manager
}

// IsPrimarySession returns true if this is a primary session
func (s *ServerSession) IsPrimarySession() bool {
	if s.authManager != nil {
		return s.authManager.IsPrimarySession()
	}
	return true // Default to primary if no auth manager
}

// IsSecondarySession returns true if this is a secondary session
func (s *ServerSession) IsSecondarySession() bool {
	if s.authManager != nil {
		return s.authManager.IsSecondarySession()
	}
	return false // Default to not secondary if no auth manager
}

// GetAllPaths returns all paths (active and dead) with proper PathInfo structures
// Requirements: 6.6 - server can query all paths (complete history)
func (s *ServerSession) GetAllPaths() []PathInfo {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	// Get all paths from path manager (both active and dead)
	activePaths := s.pathManager.GetActivePaths()
	deadPaths := s.pathManager.GetDeadPaths()

	allPaths := make([]PathInfo, 0, len(activePaths)+len(deadPaths))

	// Add active paths
	for _, path := range activePaths {
		pathInfo := s.createPathInfo(path, PathStatusActive)
		allPaths = append(allPaths, pathInfo)
	}

	// Add dead paths
	for _, path := range deadPaths {
		pathInfo := s.createPathInfo(path, PathStatusDead)
		allPaths = append(allPaths, pathInfo)
	}

	return allPaths
}

// createPathInfo creates a PathInfo structure from a transport.Path
// This helper method ensures consistent PathInfo creation with current path states
func (s *ServerSession) createPathInfo(path transport.Path, status PathStatus) PathInfo {
	// Default values
	createdAt := s.createdAt
	lastActive := time.Now()
	actualStatus := status

	// Try to get more detailed information if the path supports it
	// We need to check if the path has additional methods beyond the interface
	if pathWithDetails, ok := path.(interface {
		GetCreatedAt() time.Time
		GetLastActivity() time.Time
		GetState() transport.PathState
	}); ok {
		createdAt = pathWithDetails.GetCreatedAt()
		lastActive = pathWithDetails.GetLastActivity()

		// Map transport.PathState to session.PathStatus
		switch pathWithDetails.GetState() {
		case transport.PathStateActive:
			actualStatus = PathStatusActive
		case transport.PathStateDead:
			actualStatus = PathStatusDead
		case transport.PathStateConnecting:
			actualStatus = PathStatusConnecting
		case transport.PathStateDisconnecting:
			actualStatus = PathStatusDisconnecting
		default:
			// Keep the provided status as fallback
			actualStatus = status
		}
	}

	return PathInfo{
		PathID:     path.ID(),
		Address:    path.Address(),
		IsPrimary:  path.IsPrimary(),
		Status:     actualStatus,
		CreatedAt:  createdAt,
		LastActive: lastActive,
	}
}

// SendRawData sends raw data through a specific path with dead path detection
// Requirements: 6.3 - detect operations targeting dead paths and send notifications
func (s *ServerSession) SendRawData(rawData []byte, pathID string, remoteStreamID uint64) error {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if s.stateManager != nil && !s.stateManager.IsActive() {
		return utils.NewKwikError(utils.ErrConnectionLost, "session is not active", nil)
	}

	// Validate inputs
	if len(rawData) == 0 {
		return utils.NewKwikError(utils.ErrInvalidFrame, "raw data cannot be empty", nil)
	}

	if pathID == "" {
		return utils.NewKwikError(utils.ErrInvalidFrame, "target path ID cannot be empty", nil)
	}

	// Check path health before proceeding
	if !s.IsPathHealthy(pathID) {
		s.logger.Warn("Attempting to send data on unhealthy path", "pathID", pathID)

		// Try to find a better path if the requested path is unhealthy
		betterPath := s.GetBestAvailablePath()
		if betterPath != nil && betterPath.ID() != pathID {
			s.logger.Info("Routing data to healthier path", "originalPath", pathID, "newPath", betterPath.ID())
			pathID = betterPath.ID()
		}
	}

	// Record start time for RTT measurement
	startTime := time.Now()

	// First check if target path exists in path manager
	targetPath := s.pathManager.GetPath(pathID)
	if targetPath == nil {
		// Check if this is a pending path ID (for client-side secondary paths)
		// pendingPathIDs is a map of address -> pathID, so we need to check the values
		s.pathIDsMutex.RLock()
		found := false
		s.logger.Debug(fmt.Sprintf("SendRawData checking for pathID %s in pendingPathIDs", pathID))
		for address, pendingID := range s.pendingPathIDs {
			s.logger.Debug(fmt.Sprintf("pendingPathIDs[%s] = %s", address, pendingID))
			if pendingID == pathID {
				found = true
				s.logger.Debug(fmt.Sprintf("Found matching pathID %s for address %s", pathID, address))
				break
			}
		}
		s.pathIDsMutex.RUnlock()

		if !found {
			s.logger.Debug(fmt.Sprintf("PathID %s not found in pendingPathIDs, returning error", pathID))
			return utils.NewPathNotFoundError(pathID)
		}

		s.logger.Debug(fmt.Sprintf("PathID %s found in pendingPathIDs, proceeding with SendRawData", pathID))

		// This is a pending path (client-side secondary path)
		// We can proceed with sending the raw data via control plane
		// The client will route it to the appropriate secondary server
	} else {
		// This is a regular path in the path manager
		if !targetPath.IsActive() {
			// Send dead path notification when operation fails due to dead path
			notifyErr := s.sendDeadPathNotification(pathID, "raw_data_operation_failed")
			if notifyErr != nil {
				// Log the notification error but don't override the main error
				_ = notifyErr
			}
			return utils.NewPathDeadError(pathID)
		}
	}

	// Get control stream from primary path to send raw packet transmission command
	controlStream, err := s.primaryPath.GetControlStream()
	if err != nil {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			"failed to get control stream for raw packet transmission", err)
	}

	// If remoteStreamID is provided, encapsulate data with metadata for secondary stream aggregation
	var finalData []byte
	if remoteStreamID != 0 {
		// Encapsulate data with metadata for secondary stream aggregation
		metadataProtocol := s.metadataProtocol
		if metadataProtocol == nil {
			return utils.NewKwikError(utils.ErrStreamCreationFailed, "no metadata protocol available", nil)
		}

		// Use offset 0 as default since we don't have offset information in SendRawData
		// In a real scenario, the offset would be provided as an additional parameter
		encapsulatedData, err := metadataProtocol.EncapsulateData(
			remoteStreamID, // Target KWIK stream ID
			1,              // Secondary stream ID (placeholder, should be actual stream ID)
			0,              // Default offset (could be enhanced to accept offset parameter)
			rawData,
		)
		if err != nil {
			return utils.NewKwikError(utils.ErrInvalidFrame,
				fmt.Sprintf("failed to encapsulate raw data with metadata: %v", err), err)
		}

		finalData = encapsulatedData
		s.logger.Info(fmt.Sprintf("[PRIMARY] SendRawData encapsulated %d bytes for KWIK stream %d", len(rawData), remoteStreamID))
	} else {
		// No metadata encapsulation needed
		finalData = rawData
	}

	// Create RawPacketTransmission protobuf message
	rawPacketReq := &control.RawPacketTransmission{
		Data:           finalData,
		TargetPathId:   pathID,
		SourceServerId: s.sessionID,
		ProtocolHint:   "custom", // Default hint for custom protocols
		PreserveOrder:  true,     // Preserve packet order by default
	}

	// Serialize the RawPacketTransmission
	payload, err := proto.Marshal(rawPacketReq)
	if err != nil {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			"failed to serialize RawPacketTransmission", err)
	}

	// Create control frame
	frame := &control.ControlFrame{
		FrameId:      data.GenerateFrameID(),
		Type:         control.ControlFrameType_RAW_PACKET_TRANSMISSION,
		Payload:      payload,
		Timestamp:    uint64(time.Now().UnixNano()),
		SourcePathId: s.primaryPath.ID(),
		TargetPathId: pathID,
	}

	// Serialize and send control frame
	frameData, err := proto.Marshal(frame)
	if err != nil {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			"failed to serialize raw packet control frame", err)
	}

	_, err = controlStream.Write(frameData)
	if err != nil {
		// If write fails, check if it's due to dead path and send notification
		if pathErr := s.checkPathForOperation(pathID, "SendRawData"); pathErr != nil {
			s.sendDeadPathNotification(pathID, "raw_data_transmission_failed")
		}

		// Update health metrics for failed operation
		rtt := time.Since(startTime)
		s.UpdatePathHealthMetrics(pathID, &rtt, false, uint64(len(finalData)))

		return utils.NewKwikError(utils.ErrConnectionLost,
			"failed to send raw packet transmission request", err)
	}

	// Update health metrics for successful operation
	rtt := time.Since(startTime)
	s.UpdatePathHealthMetrics(pathID, &rtt, true, uint64(len(finalData)))

	return nil
}

// checkPathForOperation checks if a path is suitable for an operation
// Requirements: 6.3 - detect operations targeting dead paths
func (s *ServerSession) checkPathForOperation(pathID string, operationName string) error {
	// Get the path
	path := s.pathManager.GetPath(pathID)
	if path == nil {
		// Send dead path notification for non-existent path
		s.sendDeadPathNotification(pathID, fmt.Sprintf("%s_operation_failed_path_not_found", operationName))
		return utils.NewPathNotFoundError(pathID)
	}

	// Check if path is active
	if !path.IsActive() {
		// Send dead path notification for inactive path
		s.sendDeadPathNotification(pathID, fmt.Sprintf("%s_operation_failed_path_dead", operationName))
		return utils.NewPathDeadError(pathID)
	}

	// Check if path has a healthy connection
	if pathWithHealth, ok := path.(interface{ IsHealthy() bool }); ok {
		if !pathWithHealth.IsHealthy() {
			// Send dead path notification for unhealthy path
			s.sendDeadPathNotification(pathID, fmt.Sprintf("%s_operation_failed_path_unhealthy", operationName))
			return utils.NewKwikError(utils.ErrPathDead, "path is unhealthy", nil)
		}
	}

	return nil
}

// sendDeadPathNotification sends a dead path notification frame when operations fail
// Requirements: 6.3 - send dead path notification frame when operations fail
func (s *ServerSession) sendDeadPathNotification(pathID string, reason string) error {
	// Don't send notification if session is not active
	if s.stateManager != nil && !s.stateManager.IsActive() {
		return nil
	}

	// Don't send notification if primary path is not available
	if s.primaryPath == nil || !s.primaryPath.IsActive() {
		return nil
	}

	// Get control stream from primary path
	controlStream, err := s.primaryPath.GetControlStream()
	if err != nil {
		// Can't send notification if control stream is not available
		return nil
	}

	// Create path metrics for the dead path (minimal information)
	metrics := &control.PathMetrics{
		RttMs:          0,   // No RTT for dead path
		BandwidthBps:   0,   // No bandwidth for dead path
		PacketLossRate: 1.0, // 100% loss for dead path
		BytesSent:      0,   // No bytes sent
		BytesReceived:  0,   // No bytes received
		LastActivity:   uint64(time.Now().UnixNano()),
	}

	// Create PathStatusNotification protobuf message for dead path
	statusNotification := &control.PathStatusNotification{
		PathId:    pathID,
		Status:    control.PathStatus_CONTROL_PATH_DEAD,
		Reason:    reason,
		Timestamp: uint64(time.Now().UnixNano()),
		Metrics:   metrics,
	}

	// Serialize the PathStatusNotification
	payload, err := proto.Marshal(statusNotification)
	if err != nil {
		return err
	}

	// Create control frame
	frame := &control.ControlFrame{
		FrameId:      data.GenerateFrameID(),
		Type:         control.ControlFrameType_PATH_STATUS_NOTIFICATION,
		Payload:      payload,
		Timestamp:    uint64(time.Now().UnixNano()),
		SourcePathId: s.primaryPath.ID(),
		TargetPathId: "", // Broadcast notification
	}

	// Serialize and send control frame
	frameData, err := proto.Marshal(frame)
	if err != nil {
		return err
	}

	// Send the notification (ignore errors since this is best-effort)
	_, _ = controlStream.Write(frameData)

	return nil
}

// Close closes the session and all its resources
func (s *ServerSession) Close() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.stateManager != nil && s.stateManager.GetState() == SessionStateClosed {
		return nil
	}

	if s.stateManager != nil {
		s.stateManager.SetState(SessionStateClosed)
	}
	s.state = SessionStateClosed
	s.cancel()

	// Stop offset coordinator
	err := s.stopOffsetCoordinator()
	if err != nil {
		s.logger.Debug(fmt.Sprintf("Failed to stop offset coordinator: %v", err))
	}

	// Stop health monitor
	err = s.stopHealthMonitor()
	if err != nil {
		s.logger.Debug(fmt.Sprintf("Failed to stop health monitor: %v", err))
	}

	// Stop control heartbeats
	if s.controlHeartbeatSystem != nil && s.primaryPath != nil {
		err = s.controlHeartbeatSystem.StopControlHeartbeats(s.sessionID, s.primaryPath.ID())
		if err != nil {
			s.logger.Debug(fmt.Sprintf("Failed to stop control heartbeats: %v", err))
		}
	}

	// Stop all heartbeats
	s.stopAllHeartbeats()

	// Close all streams
	s.streamsMutex.Lock()
	for _, stream := range s.streams {
		stream.Close()
	}
	s.streamsMutex.Unlock()

	// Close all paths
	activePaths := s.pathManager.GetActivePaths()
	for _, path := range activePaths {
		path.Close()
	}

	close(s.acceptChan)

	return nil
}

// managePaths handles path management in background
func (s *ServerSession) managePaths() {
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
func (s *ServerSession) checkPathHealth() {
	activePaths := s.pathManager.GetActivePaths()

	for _, path := range activePaths {
		if !path.IsActive() {
			s.pathManager.MarkPathDead(path.ID())
		}
	}
}

// GetSessionID returns the session ID (for external access)
func (s *ServerSession) GetSessionID() string {
	return s.sessionID
}

// IsAuthenticated returns whether the session is authenticated
func (s *ServerSession) IsAuthenticated() bool {
	if s.authManager == nil {
		return false
	}
	return s.authManager.IsAuthenticated()
}

// Note: handleIncomingStreams method removed - streams are now handled on-demand via AcceptStream()

// markPrimaryPathAsDefault marks the primary path as the default for operations
// This implements requirement 2.5: primary path serves as default for stream operations
func (s *ServerSession) markPrimaryPathAsDefault() error {
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
// This implements requirement 4.3: server writes go through their own data plane
func (s *ServerSession) GetDefaultPathForWrite() (transport.Path, error) {
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

// GetPrimaryPath returns the primary path for this session
func (s *ServerSession) GetPrimaryPath() transport.Path {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.primaryPath
}

// handleAuthenticationWithSessionID handles server-side authentication and returns client's session ID
func (s *ServerSession) handleAuthenticationWithSessionID(ctx context.Context) (string, error) {
	// Entrer en état d'authentification
	if err := s.stateManager.SetState(SessionStateAuthenticating); err != nil {
		return "", utils.NewKwikError(utils.ErrInvalidState,
			"failed to set authenticating state", err)
	}

	// Get control stream from primary path
	controlStream, err := s.primaryPath.GetControlStream()
	if err != nil {
		s.logger.Debug(fmt.Sprintf("Server failed to get control stream: %v", err))
		return "", utils.NewKwikError(utils.ErrStreamCreationFailed,
			"failed to get control stream for authentication", err)
	}

	// Read authentication request from client with timeout
	buffer := make([]byte, 4096)

	// Use a goroutine to handle the read with context cancellation
	type readResult struct {
		n   int
		err error
	}

	readChan := make(chan readResult, 1)
	go func() {
		n, err := controlStream.Read(buffer)
		readChan <- readResult{n: n, err: err}
	}()

	// Wait for read with timeout
	authCtx, cancel := context.WithTimeout(ctx, utils.DefaultHandshakeTimeout)
	defer cancel()

	var n int
	select {
	case result := <-readChan:
		n, err = result.n, result.err
		if err != nil {
			return "", utils.NewKwikError(utils.ErrAuthenticationFailed,
				"failed to read authentication request", err)
		}
	case <-authCtx.Done():
		return "", utils.NewKwikError(utils.ErrAuthenticationFailed,
			"authentication timeout", authCtx.Err())
	}

	// Parse control frame
	var frame control.ControlFrame
	err = proto.Unmarshal(buffer[:n], &frame)
	if err != nil {
		return "", utils.NewKwikError(utils.ErrInvalidFrame,
			"failed to parse authentication control frame", err)
	}

	// Verify it's an authentication request
	if frame.Type != control.ControlFrameType_AUTHENTICATION_REQUEST {
		return "", utils.NewKwikError(utils.ErrAuthenticationFailed,
			"expected authentication request frame", nil)
	}

	// Parse authentication request
	var authReq control.AuthenticationRequest
	err = proto.Unmarshal(frame.Payload, &authReq)
	if err != nil {
		return "", utils.NewKwikError(utils.ErrInvalidFrame,
			"failed to parse authentication request", err)
	}

	// Debug: Log the received authentication request details
	roleStr := "UNKNOWN"
	switch authReq.Role {
	case control.SessionRole_PRIMARY:
		roleStr = "PRIMARY"
	case control.SessionRole_SECONDARY:
		roleStr = "SECONDARY"
	}
	s.logger.Debug(fmt.Sprintf("Server received authentication request with role: %s, session ID: %s", roleStr, authReq.SessionId))

	// Use AuthenticationManager to handle the authentication request with role support
	responseFrame, err := s.authManager.HandleAuthenticationRequest(&frame)
	if err != nil {
		return "", utils.NewKwikError(utils.ErrAuthenticationFailed,
			"authentication failed", err)
	}

	// Extract client's session ID from the authentication request
	clientSessionID := authReq.SessionId
	if clientSessionID == "" {
		return "", utils.NewKwikError(utils.ErrAuthenticationFailed,
			"client session ID is empty", nil)
	}

	// The role is now stored in the AuthenticationManager and can be accessed via GetSessionRole()
	// Log the role information for debugging
	roleStr = "UNKNOWN"
	if s.authManager.GetSessionRole() == control.SessionRole_PRIMARY {
		roleStr = "PRIMARY"
	} else if s.authManager.GetSessionRole() == control.SessionRole_SECONDARY {
		roleStr = "SECONDARY"
	}
	s.logger.Debug(fmt.Sprintf("Server authenticated client with role: %s, session ID: %s", roleStr, clientSessionID))

	// Serialize and send response
	frameData, err := proto.Marshal(responseFrame)
	if err != nil {
		return "", utils.NewKwikError(utils.ErrInvalidFrame,
			"failed to serialize authentication response frame", err)
	}

	_, err = controlStream.Write(frameData)
	if err != nil {
		return "", utils.NewKwikError(utils.ErrConnectionLost,
			"failed to send authentication response", err)
	}

	// Attendre un délai de sécurité pour s'assurer que le client reçoit la réponse
	// avant que les heartbeats commencent
	time.Sleep(200 * time.Millisecond)

	// Changer l'état vers authentifié
	if err := s.stateManager.SetState(SessionStateAuthenticated); err != nil {
		return "", utils.NewKwikError(utils.ErrInvalidState,
			"failed to set authenticated state", err)
	}

	// Authentication state is now managed by AuthenticationManager.HandleAuthenticationRequest()
	// Mark primary path as authenticated
	if s.primaryPath != nil {
		s.authManager.MarkPrimaryPathAuthenticated(s.primaryPath.ID())

		// Also mark the path as authenticated in the heartbeat manager
		if s.heartbeatManager != nil {
			s.heartbeatManager.SetPathAuthenticated(clientSessionID, s.primaryPath.ID(), true)
		}
	}

	return clientSessionID, nil
}

// Old heartbeat functions removed - now using health monitor system

// GetOffsetCoordinator returns the offset coordinator
func (s *ServerSession) GetOffsetCoordinator() data.OffsetCoordinator {
	return s.offsetCoordinator
}

// setupOffsetCoordinatorCallbacks configures the offset coordinator callbacks
func (s *ServerSession) setupOffsetCoordinatorCallbacks() {
	if s.offsetCoordinator == nil {
		return
	}

	// Cast to implementation type to access callback methods
	if ocImpl, ok := s.offsetCoordinator.(*data.OffsetCoordinatorImpl); ok {
		// Set missing data callback - called when gaps are detected
		ocImpl.SetMissingDataCallback(func(streamID uint64, gaps []data.OffsetGap) error {
			s.logger.Debug(fmt.Sprintf("Server requesting missing data for stream %d, gaps: %d", streamID, len(gaps)))
			// Server can request missing data from clients or trigger retransmission
			// For now, just log the gaps
			for _, gap := range gaps {
				s.logger.Debug(fmt.Sprintf("Gap detected: start=%d, end=%d, size=%d", gap.Start, gap.End, gap.Size))
			}
			return nil
		})
	}
}

// startOffsetCoordinator starts the offset coordinator
func (s *ServerSession) startOffsetCoordinator() error {
	if s.offsetCoordinator == nil {
		return utils.NewKwikError(utils.ErrInvalidState, "offset coordinator not initialized", nil)
	}

	err := s.offsetCoordinator.Start()
	if err != nil {
		return utils.NewKwikError(utils.ErrInvalidState, "failed to start offset coordinator", err)
	}

	s.logger.Debug("Server offset coordinator started")
	return nil
}

// stopOffsetCoordinator stops the offset coordinator
func (s *ServerSession) stopOffsetCoordinator() error {
	if s.offsetCoordinator == nil {
		return nil
	}

	return s.offsetCoordinator.Stop()
}

// reserveOffsetRange reserves an offset range for stream writing
func (s *ServerSession) reserveOffsetRange(streamID uint64, size int) (int64, error) {
	if s.offsetCoordinator == nil {
		return 0, utils.NewKwikError(utils.ErrInvalidState, "offset coordinator not available", nil)
	}

	return s.offsetCoordinator.ReserveOffsetRange(streamID, size)
}

// commitOffsetRange commits an offset range after successful writing
func (s *ServerSession) commitOffsetRange(streamID uint64, startOffset int64, actualSize int) error {
	if s.offsetCoordinator == nil {
		return utils.NewKwikError(utils.ErrInvalidState, "offset coordinator not available", nil)
	}

	return s.offsetCoordinator.CommitOffsetRange(streamID, startOffset, actualSize)
}

// validateOffsetContinuity checks for gaps in the offset sequence
func (s *ServerSession) validateOffsetContinuity(streamID uint64) ([]data.OffsetGap, error) {
	if s.offsetCoordinator == nil {
		return nil, utils.NewKwikError(utils.ErrInvalidState, "offset coordinator not available", nil)
	}

	return s.offsetCoordinator.ValidateOffsetContinuity(streamID)
}

// registerDataReceived registers that data has been received at a specific offset
func (s *ServerSession) registerDataReceived(streamID uint64, offset int64, size int) error {
	if s.offsetCoordinator == nil {
		return utils.NewKwikError(utils.ErrInvalidState, "offset coordinator not available", nil)
	}

	return s.offsetCoordinator.RegisterDataReceived(streamID, offset, size)
}

// getNextExpectedOffset returns the next expected offset for a stream
func (s *ServerSession) getNextExpectedOffset(streamID uint64) int64 {
	if s.offsetCoordinator == nil {
		return 0
	}

	return s.offsetCoordinator.GetNextExpectedOffset(streamID)
}

// getOffsetCoordinatorStats returns current offset coordination statistics
func (s *ServerSession) getOffsetCoordinatorStats() data.OffsetCoordinatorStats {
	if s.offsetCoordinator == nil {
		return data.OffsetCoordinatorStats{}
	}

	return s.offsetCoordinator.GetStats()
}

// startHealthMonitor starts the health monitor
func (s *ServerSession) startHealthMonitor() error {
	if s.healthMonitor == nil {
		return utils.NewKwikError(utils.ErrInvalidState, "health monitor not initialized", nil)
	}

	// Vérifier que l'authentification est terminée avant de démarrer le health monitoring
	if s.stateManager != nil && !s.stateManager.IsAuthenticationComplete() {
		return utils.NewKwikError(utils.ErrInvalidState,
			"cannot start health monitor: authentication not complete", nil)
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
func (s *ServerSession) stopHealthMonitor() error {
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
func (s *ServerSession) GetHealthMonitor() ConnectionHealthMonitor {
	return s.healthMonitor
}

// GetPathHealthStatus returns the health status of a specific path
func (s *ServerSession) GetPathHealthStatus(pathID string) *PathHealth {
	if s.healthMonitor == nil {
		return nil
	}
	return s.healthMonitor.GetPathHealth(s.sessionID, pathID)
}

// IsPathHealthy checks if a path is healthy enough for operations
func (s *ServerSession) IsPathHealthy(pathID string) bool {
	pathHealth := s.GetPathHealthStatus(pathID)
	if pathHealth == nil {
		return false
	}

	// Consider path healthy if status is Active and health score is above warning threshold
	return pathHealth.Status == PathStatusActive && pathHealth.HealthScore >= 70
}

// GetBestAvailablePath returns the healthiest available path for operations
func (s *ServerSession) GetBestAvailablePath() transport.Path {
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
func (s *ServerSession) UpdatePathHealthMetrics(pathID string, rtt *time.Duration, success bool, bytesTransferred uint64) {
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
func (s *ServerSession) startHeartbeatManagement() {
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
func (s *ServerSession) startHeartbeatForPath(pathID string) {
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
func (s *ServerSession) stopHeartbeatForPath(pathID string) {
	if s.heartbeatManager == nil {
		return
	}

	err := s.heartbeatManager.StopHeartbeat(s.sessionID, pathID)
	if err != nil {
		s.logger.Error("Failed to stop heartbeat for path", "pathID", pathID, "error", err)
	}
}

// stopAllHeartbeats stops heartbeat monitoring for all paths
func (s *ServerSession) stopAllHeartbeats() {
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
func (s *ServerSession) onHeartbeatReceived(pathID string, rtt time.Duration) {
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

// setupFlowControlManager sets up the flow control manager for the server session
func (s *ServerSession) setupFlowControlManager() {
	// Create a simple receive window manager for flow control
	receiveWindowManager := NewSimpleReceiveWindowManager(1024 * 1024) // 1MB initial window

	// Create control plane interface directly with session
	s.controlPlane = NewSessionControlPlane(s)

	// Set control plane in heartbeat system now that it's available
	if s.controlHeartbeatSystem != nil {
		controlPlaneAdapter := &SessionControlPlaneAdapter{session: s}
		s.controlHeartbeatSystem.SetControlPlane(controlPlaneAdapter)
	}

	// Create flow control manager
	flowControlConfig := DefaultFlowControlConfig()
	s.flowControlManager = NewFlowControlManager(
		s.sessionID,
		false, // isClient = false for server
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
func (s *ServerSession) SendControlFrame(pathID string, frame *control.ControlFrame) error {
	// Get the path - for server, we typically use the primary path
	path := s.primaryPath
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
func (s *ServerSession) startFlowControlManager() error {
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
func (s *ServerSession) stopFlowControlManager() error {
	if s.flowControlManager == nil {
		return nil
	}

	return s.flowControlManager.Stop()
}

// handleWindowUpdate handles incoming window update frames
func (s *ServerSession) handleWindowUpdate(frame *control.ControlFrame) {
	if s.controlPlane != nil {
		err := s.controlPlane.HandleControlFrame(frame)
		if err != nil {
			s.logger.Error("Failed to handle window update", "error", err)
		}
	}
}

// handleBackpressureSignal handles incoming backpressure signal frames
func (s *ServerSession) handleBackpressureSignal(frame *control.ControlFrame) {
	if s.controlPlane != nil {
		err := s.controlPlane.HandleControlFrame(frame)
		if err != nil {
			s.logger.Error("Failed to handle backpressure signal", "error", err)
		}
	}
}

// sendHeartbeatPacket sends an actual heartbeat packet through the specified path
func (s *ServerSession) sendHeartbeatPacket(sessionID, pathID string) error {
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
	s.logger.Debug(fmt.Sprintf("Server sending HEARTBEAT seq=%d on path=%s (stream=%d, %d bytes)", seq, pathID, controlStream.StreamID(), len(frameData)))
	n, err := controlStream.Write(frameData)
	if err != nil {
		s.logger.Debug(fmt.Sprintf("Server heartbeat write error: %v", err))
		return fmt.Errorf("failed to write heartbeat: %v", err)
	} else {
		s.logger.Debug(fmt.Sprintf("Server heartbeat write success: %d bytes", n))
	}

	return nil
}

// SessionControlPlaneAdapter adapts session control frame sending for heartbeat system
// (This is a duplicate of the one in client.go - in a real implementation, this would be in a shared file)
type SessionControlPlaneAdapter struct {
	session interface{}
}

// SendFrame implements the ControlPlane interface for heartbeat system
func (adapter *SessionControlPlaneAdapter) SendFrame(pathID string, frame protocol.Frame) error {
	var controlFrame *control.ControlFrame

	// Handle different frame types
	switch f := frame.(type) {
	case *protocol.HeartbeatFrame:
		// Create a control frame from the heartbeat frame
		controlFrame = &control.ControlFrame{
			FrameId:   f.FrameID,
			Type:      control.ControlFrameType_HEARTBEAT,
			Payload:   adapter.serializeHeartbeatFrame(f),
			Timestamp: uint64(f.Timestamp.UnixNano()),
		}
	case *protocol.HeartbeatResponseFrame:
		// Create a control frame from the heartbeat response frame
		controlFrame = &control.ControlFrame{
			FrameId:   f.FrameID,
			Type:      control.ControlFrameType_HEARTBEAT_RESPONSE,
			Payload:   adapter.serializeHeartbeatResponseFrame(f),
			Timestamp: uint64(f.Timestamp.UnixNano()),
		}
	default:
		return fmt.Errorf("unsupported frame type for heartbeat system: %T", frame)
	}

	// Send via the appropriate session
	switch s := adapter.session.(type) {
	case *ClientSession:
		return s.SendControlFrame(pathID, controlFrame)
	case *ServerSession:
		return s.SendControlFrame(pathID, controlFrame)
	default:
		return fmt.Errorf("unsupported session type")
	}
}

// serializeHeartbeatFrame serializes a heartbeat frame to bytes using binary format
func (adapter *SessionControlPlaneAdapter) serializeHeartbeatFrame(frame *protocol.HeartbeatFrame) []byte {
	// Use the protocol's binary serialization
	data, err := frame.Serialize()
	if err != nil {
		// Fallback to empty payload on error
		return []byte{}
	}
	return data
}

// serializeHeartbeatResponseFrame serializes a heartbeat response frame to bytes using binary format
func (adapter *SessionControlPlaneAdapter) serializeHeartbeatResponseFrame(frame *protocol.HeartbeatResponseFrame) []byte {
	// Use the protocol's binary serialization
	data, err := frame.Serialize()
	if err != nil {
		// Fallback to empty payload on error
		return []byte{}
	}
	return data
}

// Stub implementations for other ControlPlane interface methods (not used by heartbeat system)
func (adapter *SessionControlPlaneAdapter) ReceiveFrame(pathID string) (protocol.Frame, error) {
	return nil, fmt.Errorf("ReceiveFrame not implemented in adapter")
}

func (adapter *SessionControlPlaneAdapter) HandleAddPathRequest(req *controlpkg.AddPathRequest) error {
	return fmt.Errorf("HandleAddPathRequest not implemented in adapter")
}

func (adapter *SessionControlPlaneAdapter) HandleRemovePathRequest(req *controlpkg.RemovePathRequest) error {
	return fmt.Errorf("HandleRemovePathRequest not implemented in adapter")
}

func (adapter *SessionControlPlaneAdapter) HandleAuthenticationRequest(req *controlpkg.AuthenticationRequest) error {
	return fmt.Errorf("HandleAuthenticationRequest not implemented in adapter")
}

func (adapter *SessionControlPlaneAdapter) HandleRawPacketTransmission(req *controlpkg.RawPacketTransmission) error {
	return fmt.Errorf("HandleRawPacketTransmission not implemented in adapter")
}

func (adapter *SessionControlPlaneAdapter) SendPathStatusNotification(pathID string, status controlpkg.PathStatus) error {
	return fmt.Errorf("SendPathStatusNotification not implemented in adapter")
}
