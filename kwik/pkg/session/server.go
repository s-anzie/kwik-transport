package session

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	kwiктls "kwik/internal/tls"
	"kwik/internal/utils"
	"kwik/pkg/data"
	"kwik/pkg/stream"
	"kwik/pkg/transport"
	"kwik/proto/control"

	"github.com/quic-go/quic-go"
	"google.golang.org/protobuf/proto"
)

// DefaultServerLogger provides a simple logger implementation for server session
type DefaultServerLogger struct{}

func (d *DefaultServerLogger) Debug(msg string, keysAndValues ...interface{}) {}
func (d *DefaultServerLogger) Info(msg string, keysAndValues ...interface{})  {}
func (d *DefaultServerLogger) Warn(msg string, keysAndValues ...interface{})  {}
func (d *DefaultServerLogger) Error(msg string, keysAndValues ...interface{}) {
	log.Printf("[ERROR] %s", msg)
}
func (d *DefaultServerLogger) Critical(msg string, keysAndValues ...interface{}) {
	log.Printf("[CRITICAL] %s", msg)
}

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
	serverRole ServerRole

	// Stream management
	nextStreamID uint64
	streams      map[uint64]*ServerStream
	streamsMutex sync.RWMutex

	// Channel for accepting incoming streams
	acceptChan chan *ServerStream

	// Secondary stream aggregation
	secondaryAggregator *data.StreamAggregator
	metadataProtocol    *stream.MetadataProtocolImpl

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

	// Heartbeat management
	heartbeatSequence   uint64
	lastHeartbeatByPath map[string]time.Time

	// Logger
	logger stream.StreamLogger
}

// SetLogger sets the logger for this server session
func (s *ServerSession) SetLogger(l stream.StreamLogger) { s.logger = l }

// GetLogger returns the logger for this server session
func (s *ServerSession) GetLogger() stream.StreamLogger { return s.logger }

// ServerStream represents a server-side KWIK stream
type ServerStream struct {
	id         uint64
	pathID     string
	session    *ServerSession
	created    time.Time
	state      stream.StreamState
	readBuf    []byte
	writeBuf   []byte
	quicStream quic.Stream // Underlying QUIC stream
	mutex      sync.RWMutex

	// Secondary stream isolation fields
	offset            int    // Current offset for stream data aggregation
	remoteStreamID    uint64 // Remote stream ID for cross-stream references
	isSecondaryStream bool   // Flag to indicate if this is a secondary stream for aggregation

	// Pending segments for retransmission keyed by aggregated offset
	pendingSegments map[uint64]*PendingSegment
	retransmitOnce  sync.Once
	retransmitStop  chan struct{}

	// Packet aggregation buffer (raw frames: metadata-encapsulated)
	packetFrameRaw       [][]byte
	packetBufferBytes    int
	packetFlushScheduled bool
}

// PendingSegment tracks a sent segment awaiting ACK
type PendingSegment struct {
	offset       uint64
	size         int
	encapsulated []byte
	lastSent     time.Time
	retries      int
}

// NewServerSession creates a new KWIK server session
func NewServerSession(sessionID string, pathManager transport.PathManager, config *SessionConfig) *ServerSession {
	if config == nil {
		config = DefaultSessionConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())

	session := &ServerSession{
		sessionID:            sessionID,
		pathManager:          pathManager,
		isClient:             false,
		state:                SessionStateActive,
		createdAt:            time.Now(),
		authManager:          NewAuthenticationManager(sessionID, false), // false = isServer
		serverRole:           ServerRolePrimary,                          // Default to primary role
		streams:              make(map[uint64]*ServerStream),
		acceptChan:           make(chan *ServerStream, 100),
		secondaryAggregator:  data.NewStreamAggregator(&DefaultServerLogger{}), // Initialize secondary stream aggregator
		metadataProtocol:     stream.NewMetadataProtocol(),                     // Initialize metadata protocol
		ctx:                  ctx,
		cancel:               cancel,
		config:               config,
		pendingPathIDs:       make(map[string]string),                        // Initialize pending path IDs map
		addPathResponseChans: make(map[string]chan *control.AddPathResponse), // Initialize response channels
		heartbeatSequence:    0,
		lastHeartbeatByPath:  make(map[string]time.Time),
		logger:               &DefaultServerLogger{},
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
}

// Listen creates a KWIK listener (QUIC-compatible)
func Listen(address string, config *SessionConfig) (Listener, error) {
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

	// SERVER ACCEPTS THE CONTROL STREAM CREATED BY CLIENT (AcceptStream)
	// This is the key fix - server accepts the stream created by client
	_, err = primaryPath.AcceptControlStreamAsServer()
	if err != nil {
		conn.CloseWithError(0, "failed to accept control stream")
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"failed to accept control stream as server", err)
	}

	// Create server session with temporary session ID (will be updated during authentication)
	tempSessionID := generateSessionID()
	session := NewServerSession(tempSessionID, pathManager, config)
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

	// Start control-plane heartbeat loop on server side as well
	go session.startHeartbeat()

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

// AcceptSession accepts a new KWIK session from a listener
func AcceptSession(listener *quic.Listener) (Session, error) {
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
	session := NewServerSession(sessionID, pathManager, nil)
	session.primaryPath = primaryPath

	// Start session management
	go session.managePaths()
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

	if s.state != SessionStateActive {
		return nil, utils.NewKwikError(utils.ErrConnectionLost, "session is not active", nil)
	}

	// Generate new stream ID
	streamID := s.nextStreamID
	s.nextStreamID++

	// Behavior depends on server role
	if s.serverRole == ServerRolePrimary {
		// PRIMARY SERVER: Open stream on public client session (original behavior)
		return s.openPrimaryStream(ctx, streamID)
	} else {
		// SECONDARY SERVER: Open internal stream for aggregation
		s.logger.Info(fmt.Sprintf("[SECONDARY] Opening secondary stream %d for aggregation", streamID))
		return s.openSecondaryStreamInternal(ctx, streamID)
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
	serverStream := &ServerStream{
		id:         streamID,
		pathID:     s.primaryPath.ID(),
		session:    s,
		created:    time.Now(),
		state:      stream.StreamStateOpen,
		readBuf:    make([]byte, 0, utils.DefaultReadBufferSize),
		writeBuf:   make([]byte, 0, utils.DefaultWriteBufferSize),
		quicStream: quicStream, // Add the underlying QUIC stream
	}

	s.streamsMutex.Lock()
	s.streams[streamID] = serverStream
	s.streamsMutex.Unlock()

	return serverStream, nil
}

// openSecondaryStreamInternal opens an internal stream for secondary servers (will be aggregated client-side)
func (s *ServerSession) openSecondaryStreamInternal(ctx context.Context, streamID uint64) (Stream, error) {
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
	serverStream := &ServerStream{
		id:         streamID,
		pathID:     s.primaryPath.ID(),
		session:    s,
		created:    time.Now(),
		state:      stream.StreamStateOpen,
		readBuf:    make([]byte, 0, utils.DefaultReadBufferSize),
		writeBuf:   make([]byte, 0, utils.DefaultWriteBufferSize),
		quicStream: quicStream, // Add the underlying QUIC stream
		// Mark this as a secondary stream for special handling
		isSecondaryStream: true,
	}

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

	serverStream := &ServerStream{
		id:         streamID,
		pathID:     s.primaryPath.ID(),
		session:    s,
		created:    time.Now(),
		state:      stream.StreamStateOpen,
		quicStream: quicStream, // Store the underlying QUIC stream
	}

	// Store in streams map
	s.streamsMutex.Lock()
	s.streams[serverStream.id] = serverStream
	s.streamsMutex.Unlock()

	return serverStream, nil
}

// AddPath requests the client to establish a secondary path
func (s *ServerSession) AddPath(address string) error {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if s.state != SessionStateActive {
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
		FrameId:      generateFrameID(),
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
	return s.secondaryAggregator
}

// GetMetadataProtocol returns the metadata protocol instance
func (s *ServerSession) GetMetadataProtocol() *stream.MetadataProtocolImpl {
	return s.metadataProtocol
}

// SetServerRole sets the server role (PRIMARY or SECONDARY)
func (s *ServerSession) SetServerRole(role ServerRole) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.serverRole = role
	s.logger.Info(fmt.Sprintf("[SERVER] Role set to: %s", role.String()))
}

// GetServerRole returns the current server role
func (s *ServerSession) GetServerRole() ServerRole {
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

	s.logger.Debug(fmt.Sprintf("Server control frame handler started, listening for client responses..."))

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
		_ = controlStream.SetReadDeadline(time.Now().Add(2 * utils.DefaultKeepAliveInterval))
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
		case control.ControlFrameType_DATA_CHUNK_ACK:
			s.handleDataChunkAck(&frame)
		case control.ControlFrameType_PACKET_ACK:
			s.handlePacketAck(&frame)
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

// handleHeartbeat processes Heartbeat frames from the client
func (s *ServerSession) handleHeartbeat(frame *control.ControlFrame) {
	if frame == nil || len(frame.Payload) == 0 {
		return
	}
	var hb control.Heartbeat
	if err := proto.Unmarshal(frame.Payload, &hb); err != nil {
		return
	}
	s.logger.Debug(fmt.Sprintf("Server received HEARTBEAT seq=%d from path=%s", hb.SequenceNumber, frame.SourcePathId))

	// Compute RTT if echo present
	if len(hb.EchoData) == 8 {
		sendTs := int64(binary.BigEndian.Uint64(hb.EchoData))
		rtt := time.Since(time.Unix(0, sendTs))
		s.logger.Debug(fmt.Sprintf("Server computed RTT=%s on path=%s", rtt.String(), frame.SourcePathId))
		return
	}

	// Echo back to allow the client to compute RTT
	// Use the same path when possible
	var controlStream quic.Stream
	if s.primaryPath != nil {
		if cs, err := s.primaryPath.GetControlStream(); err == nil {
			controlStream = cs
		}
	}
	if controlStream == nil {
		return
	}
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
		FrameId:      generateFrameID(),
		Type:         control.ControlFrameType_HEARTBEAT,
		Payload:      payload,
		Timestamp:    uint64(time.Now().UnixNano()),
		SourcePathId: frame.TargetPathId,
		TargetPathId: frame.SourcePathId,
	}
	if data, err := proto.Marshal(resp); err == nil {
		s.logger.Debug(fmt.Sprintf("Server sending HEARTBEAT echo seq=%d to path=%s", hb.SequenceNumber, frame.SourcePathId))
		_, _ = controlStream.Write(data)
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
		srvStream.mutex.RLock()
		match := srvStream.remoteStreamID == ack.KwikStreamId
		srvStream.mutex.RUnlock()
		if match {
			srvStream.handleAck(ack.Offset, ack.Size)
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
		srvStream.mutex.Lock()
		if srvStream.pendingSegments != nil {
			if _, ok := srvStream.pendingSegments[ack.PacketId]; ok {
				delete(srvStream.pendingSegments, ack.PacketId)
				s.logger.Debug(fmt.Sprintf("DEBUG: PACKET_ACK received: packet=%d on path=%s", ack.PacketId, ack.PathId))
			}
		}
		srvStream.mutex.Unlock()
	}
	s.streamsMutex.RUnlock()
}

// RemovePath requests removal of a secondary path with proper cleanup and notification
// Requirements: 6.2 - server can request path removal with graceful shutdown
func (s *ServerSession) RemovePath(pathID string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.state != SessionStateActive {
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
		FrameId:      generateFrameID(),
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
		FrameId:      generateFrameID(),
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
func (s *ServerSession) SendRawData(data []byte, pathID string, remoteStreamID uint64) error {
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
			data,
		)
		if err != nil {
			return utils.NewKwikError(utils.ErrInvalidFrame,
				fmt.Sprintf("failed to encapsulate raw data with metadata: %v", err), err)
		}

		finalData = encapsulatedData
		s.logger.Info(fmt.Sprintf("[PRIMARY] SendRawData encapsulated %d bytes for KWIK stream %d", len(data), remoteStreamID))
	} else {
		// No metadata encapsulation needed
		finalData = data
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
		FrameId:      generateFrameID(),
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
		return utils.NewKwikError(utils.ErrConnectionLost,
			"failed to send raw packet transmission request", err)
	}

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
	if s.state != SessionStateActive {
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
		FrameId:      generateFrameID(),
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

	if s.state == SessionStateClosed {
		return nil
	}

	s.state = SessionStateClosed
	s.cancel()

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

// ServerStream methods (implementing Stream interface)

// Read reads data from the stream (QUIC-compatible)
func (s *ServerStream) Read(p []byte) (int, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	// Check if we have a QUIC stream to read from
	if s.quicStream == nil {
		return 0, utils.NewKwikError(utils.ErrStreamCreationFailed, "no underlying QUIC stream", nil)
	}

	// Read directly from the underlying QUIC stream into a temporary buffer
	buf := make([]byte, len(p))
	n, err := s.quicStream.Read(buf)
	if err != nil || n == 0 {
		return n, err
	}

	// Try to decapsulate using metadata protocol
	mp := s.session.metadataProtocol
	if mp != nil {
		metadata, payload, derr := mp.DecapsulateData(buf[:n])
		if derr == nil && metadata != nil && len(payload) > 0 {
			// Update remoteStreamID based on KWIK Stream ID carried by the client
			s.mutex.RUnlock()
			s.mutex.Lock()
			s.remoteStreamID = metadata.KwikStreamID
			s.mutex.Unlock()
			s.mutex.RLock()
			// Return only the application payload to the server application
			copied := copy(p, payload)
			return copied, nil
		}
	}

	// If not a metadata frame, return raw data
	return 0, fmt.Errorf("no metadata protocol available")
}

// Write writes data to the stream (QUIC-compatible)
func (s *ServerStream) Write(p []byte) (int, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	// Check if we have a QUIC stream to write to
	if s.quicStream == nil {
		return 0, utils.NewKwikError(utils.ErrStreamCreationFailed, "no underlying QUIC stream", nil)
	}

	// Debug: Log the write attempt and conditions
	s.session.logger.Debug(fmt.Sprintf("ServerStream %d Write() - serverRole=%s, remoteStreamID=%d, isSecondaryStream=%t",
		s.id, s.session.serverRole.String(), s.remoteStreamID, s.isSecondaryStream))

	// Always encapsulate data with metadata for uniform client-side processing
	return s.writeWithMetadata(p)
}

// writeWithMetadata writes data encapsulated with metadata for both primary and secondary roles
func (s *ServerStream) writeWithMetadata(data []byte) (int, error) {
	// Create metadata protocol instance
	metadataProtocol := stream.NewMetadataProtocol()

	// Encapsulate data with metadata
	currentOffset := uint64(s.offset)
	encapsulatedData, err := metadataProtocol.EncapsulateData(
		s.remoteStreamID, // Target KWIK stream ID
		s.id,             // Secondary stream ID (placeholder; not serialized in current format)
		currentOffset,
		data,
	)

	if err != nil {
		return 0, utils.NewKwikError(utils.ErrInvalidFrame,
			fmt.Sprintf("failed to encapsulate stream data: %v", err), err)
	}

	// Aggregate frame into packet buffer and schedule flush
	mtu := int(utils.DefaultMaxPacketSize)
	frameLen := len(encapsulatedData)
	// If adding would exceed MTU and we have content, flush first
	if s.packetBufferBytes+4+frameLen > mtu && s.packetBufferBytes > 0 {
		if err := s.flushPacketLocked(); err != nil {
			return 0, err
		}
	}
	// Append with length-prefix
	s.packetFrameRaw = append(s.packetFrameRaw, encapsulatedData)
	s.packetBufferBytes += 4 + frameLen
	// Schedule timed flush if not already scheduled
	if !s.packetFlushScheduled {
		s.packetFlushScheduled = true
		go func() {
			time.Sleep(2 * time.Millisecond)
			s.mutex.Lock()
			defer s.mutex.Unlock()
			_ = s.flushPacketLocked()
			s.packetFlushScheduled = false
		}()
	}

	// Update offset for next write
	s.offset += len(data)

	// Log for debugging
	s.session.logger.Info(fmt.Sprintf("[SERVER] Stream %d wrote %d bytes (payload) to KWIK stream %d at offset %d",
		s.id, len(data), s.remoteStreamID, s.offset-len(data)))

	// Pending tracking is done when packet is flushed (in flushPacketLocked)

	// Return the original data length (not encapsulated length)
	return len(data), nil
}

// flushPacketLocked assembles buffered frames into a transport packet and sends it
func (s *ServerStream) flushPacketLocked() error {
	if s.quicStream == nil {
		return utils.NewKwikError(utils.ErrConnectionLost, "no underlying QUIC stream", nil)
	}
	if len(s.packetFrameRaw) == 0 {
		return nil
	}
	// Transport packet format: [PacketID:8][FrameCount:2] + repeated([FrameLen:4][FrameBytes])
	packetID := generateFrameID()
	header := make([]byte, 10)
	for i := 0; i < 8; i++ {
		header[i] = byte(packetID >> (8 * (7 - i)))
	}
	frameCount := uint16(len(s.packetFrameRaw))
	header[8] = byte(frameCount >> 8)
	header[9] = byte(frameCount)
	body := make([]byte, 0, s.packetBufferBytes)
	for _, fb := range s.packetFrameRaw {
		l := len(fb)
		body = append(body, byte(l>>24), byte(l>>16), byte(l>>8), byte(l))
		body = append(body, fb...)
	}
	raw := append(header, body...)
	if _, err := s.quicStream.Write(raw); err != nil {
		return err
	}
	// Register pending
	if s.pendingSegments == nil {
		s.pendingSegments = make(map[uint64]*PendingSegment)
	}
	s.pendingSegments[packetID] = &PendingSegment{offset: packetID, size: len(raw), encapsulated: raw, lastSent: time.Now(), retries: 0}
	s.startRetransmitter()
	// Reset buffer
	s.packetFrameRaw = nil
	s.packetBufferBytes = 0
	return nil
}

// startRetransmitter starts a background loop to retransmit unacked segments
func (s *ServerStream) startRetransmitter() {
	s.retransmitOnce.Do(func() {
		s.retransmitStop = make(chan struct{})
		go func() {
			ticker := time.NewTicker(500 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-s.retransmitStop:
					return
				case <-ticker.C:
					s.mutex.Lock()
					for off, seg := range s.pendingSegments {
						if time.Since(seg.lastSent) > 1*time.Second && seg.retries < 3 {
							// Retransmit
							_, _ = s.quicStream.Write(seg.encapsulated)
							seg.lastSent = time.Now()
							seg.retries++
							s.session.logger.Debug(fmt.Sprintf("Retransmitted segment stream=%d offset=%d retries=%d", s.id, off, seg.retries))
						} else if seg.retries >= 3 {
							// Give up
							delete(s.pendingSegments, off)
							s.session.logger.Debug(fmt.Sprintf("Dropping segment stream=%d offset=%d after max retries", s.id, off))
						}
					}
					s.mutex.Unlock()
				}
			}
		}()
	})
}

// handleAck removes a pending segment when acknowledged
func (s *ServerStream) handleAck(offset uint64, size uint32) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.pendingSegments != nil {
		if _, ok := s.pendingSegments[offset]; ok {
			delete(s.pendingSegments, offset)
			s.session.logger.Debug(fmt.Sprintf("ACK received for stream=%d offset=%d", s.id, offset))
		}
	}
}

// Close closes the stream (QUIC-compatible)
func (s *ServerStream) Close() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.state = stream.StreamStateClosed
	return nil
}

// StreamID returns the logical stream ID
func (s *ServerStream) StreamID() uint64 {
	return s.id
}

// PathID returns the primary path ID for this stream
func (s *ServerStream) PathID() string {
	return s.pathID
}

// SetOffset sets the current offset for stream data aggregation
// This is used by secondary servers to specify where their data should be placed in the KWIK stream
func (s *ServerStream) SetOffset(offset int) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if offset < 0 {
		return utils.NewKwikError(utils.ErrInvalidFrame, "offset cannot be negative", nil)
	}

	s.offset = offset

	// If this is a secondary server stream, notify the aggregation system
	if s.session.serverRole == ServerRoleSecondary && s.remoteStreamID != 0 {
		// Create secondary stream data for aggregation
		// This will be used when data is written to this stream
		return s.updateAggregationMapping()
	}

	return nil
}

// GetOffset returns the current offset for stream data aggregation
func (s *ServerStream) GetOffset() int {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.offset
}

// SetRemoteStreamID sets the remote stream ID for cross-stream references
// For secondary servers, this represents the KWIK stream ID where data should be aggregated
func (s *ServerStream) SetRemoteStreamID(remoteStreamID uint64) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if remoteStreamID == 0 {
		return utils.NewKwikError(utils.ErrInvalidFrame, "remote stream ID cannot be zero", nil)
	}

	s.remoteStreamID = remoteStreamID

	// If this is a secondary server stream, set up aggregation mapping
	if s.session.serverRole == ServerRoleSecondary {
		return s.updateAggregationMapping()
	}

	return nil
}

// RemoteStreamID returns the remote stream ID for cross-stream references
func (s *ServerStream) RemoteStreamID() uint64 {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.remoteStreamID
}

// updateAggregationMapping updates the aggregation mapping for secondary streams
func (s *ServerStream) updateAggregationMapping() error {
	// Only for secondary servers with both offset and remote stream ID set
	if s.session.serverRole != ServerRoleSecondary || s.remoteStreamID == 0 {
		return nil
	}

	// Get the secondary stream aggregator from the session
	aggregator := s.session.secondaryAggregator
	if aggregator == nil {
		return utils.NewKwikError(utils.ErrStreamCreationFailed, "no secondary aggregator available", nil)
	}

	// Set up the stream mapping in the aggregator
	err := aggregator.SetStreamMapping(s.id, s.remoteStreamID, s.pathID)
	if err != nil {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			fmt.Sprintf("failed to set stream mapping: %v", err), err)
	}

	s.session.logger.Info(fmt.Sprintf("[SECONDARY] Stream %d mapped to KWIK stream %d at offset %d (aggregator configured)",
		s.id, s.remoteStreamID, s.offset))

	return nil
}

// Additional utility methods for QUIC compatibility

// SetDeadline sets the read and write deadlines (QUIC-compatible interface)
// Note: This is a placeholder for future implementation
func (s *ServerStream) SetDeadline(t time.Time) error {
	// TODO: Implement deadline support for QUIC compatibility
	// This would set both read and write deadlines
	return nil
}

// SetReadDeadline sets the read deadline (QUIC-compatible interface)
// Note: This is a placeholder for future implementation
func (s *ServerStream) SetReadDeadline(t time.Time) error {
	// TODO: Implement read deadline support
	return nil
}

// SetWriteDeadline sets the write deadline (QUIC-compatible interface)
// Note: This is a placeholder for future implementation
func (s *ServerStream) SetWriteDeadline(t time.Time) error {
	// TODO: Implement write deadline support
	return nil
}

// Context returns the stream's context (QUIC-compatible interface)
func (s *ServerStream) Context() context.Context {
	return s.session.ctx
}

// CancelWrite cancels the write side of the stream (QUIC-compatible)
func (s *ServerStream) CancelWrite(errorCode uint64) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	switch s.state {
	case stream.StreamStateOpen:
		s.state = stream.StreamStateHalfClosedLocal
	case stream.StreamStateHalfClosedRemote:
		s.state = stream.StreamStateClosed
	}

	// TODO: Send RESET_STREAM frame via control plane
}

// CancelRead cancels the read side of the stream (QUIC-compatible)
func (s *ServerStream) CancelRead(errorCode uint64) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	switch s.state {
	case stream.StreamStateOpen:
		s.state = stream.StreamStateHalfClosedRemote
	case stream.StreamStateHalfClosedLocal:
		s.state = stream.StreamStateClosed
	}

	// TODO: Send STOP_SENDING frame via control plane
}

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

	// Authentication state is now managed by AuthenticationManager.HandleAuthenticationRequest()
	// No need to manually mark as authenticated

	return clientSessionID, nil
}

// openSecondaryStream opens a secondary stream for secondary servers
// This creates a stream on the internal QUIC connection, not the public client session
func (s *ServerSession) openSecondaryStream(ctx context.Context) (stream.SecondaryStream, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.state != SessionStateActive {
		return nil, utils.NewKwikError(utils.ErrConnectionLost, "session is not active", nil)
	}

	// Only secondary servers can use this method
	if s.serverRole != ServerRoleSecondary {
		return nil, utils.NewKwikError(utils.ErrInvalidFrame,
			"openSecondaryStream is only available for secondary servers", nil)
	}

	// Generate new stream ID
	streamID := s.nextStreamID
	s.nextStreamID++

	// Ensure primary path is available and active
	if s.primaryPath == nil {
		return nil, utils.NewKwikError(utils.ErrConnectionLost, "no primary path available", nil)
	}

	if !s.primaryPath.IsActive() {
		return nil, utils.NewKwikError(utils.ErrPathDead, "primary path is not active", nil)
	}

	// Create actual QUIC stream on the internal connection
	conn := s.primaryPath.GetConnection()
	if conn == nil {
		return nil, utils.NewKwikError(utils.ErrConnectionLost, "no QUIC connection available", nil)
	}

	quicStream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed, "failed to create QUIC stream", err)
	}

	// Create secondary stream wrapper
	secondaryStream := stream.NewSecondaryStream(streamID, s.primaryPath.ID(), quicStream, s)

	// Store in streams map (using a wrapper to satisfy the interface)
	serverStream := &ServerStream{
		id:         streamID,
		pathID:     s.primaryPath.ID(),
		session:    s,
		created:    time.Now(),
		state:      stream.StreamStateOpen,
		quicStream: quicStream,
	}

	s.streamsMutex.Lock()
	s.streams[streamID] = serverStream
	s.streamsMutex.Unlock()

	return secondaryStream, nil
}

// startHeartbeat periodically sends heartbeat frames on all active paths' control streams (server side)
func (s *ServerSession) startHeartbeat() {
	s.logger.Debug(fmt.Sprintf("Server starting heartbeat loop (session=%s)", s.sessionID))
	interval := utils.DefaultKeepAliveInterval
	if s.config != nil && s.config.IdleTimeout > 0 {
		interval = s.config.IdleTimeout
	}

	t := time.NewTicker(interval)
	defer t.Stop()

	// Start liveness monitor
	go s.monitorHeartbeatLiveness(3 * interval)

	// Send an initial heartbeat immediately
	s.sendHeartbeatOnAllPaths()

	for {
		// Check session state outside of select, since select cases must be channel operations
		if s.state != SessionStateActive {
			s.logger.Debug(fmt.Sprintf("Server session %s is not active, stopping heartbeat", s.sessionID))
			time.Sleep(1 * time.Second) // Give some time before stopping
			continue
		}
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
			s.sendHeartbeatOnAllPaths()
		}
	}
}

func (s *ServerSession) sendHeartbeatOnAllPaths() {
	activePaths := s.pathManager.GetActivePaths()
	for _, p := range activePaths {
		controlStream, err := p.GetControlStream()
		if err != nil || controlStream == nil {
			continue
		}

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
			continue
		}

		frame := &control.ControlFrame{
			FrameId:      generateFrameID(),
			Type:         control.ControlFrameType_HEARTBEAT,
			Payload:      payload,
			Timestamp:    uint64(time.Now().UnixNano()),
			SourcePathId: p.ID(),
			TargetPathId: "",
		}
		data, err := proto.Marshal(frame)
		if err != nil {
			continue
		}
		s.logger.Debug(fmt.Sprintf("Server sending HEARTBEAT seq=%d on path=%s", seq, p.ID()))
		_, _ = controlStream.Write(data)
	}
}

// monitorHeartbeatLiveness marks paths dead if no heartbeat received for threshold
func (s *ServerSession) monitorHeartbeatLiveness(threshold time.Duration) {
	ticker := time.NewTicker(threshold / 2)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			active := s.pathManager.GetActivePaths()
			for _, p := range active {
				s.mutex.RLock()
				last, ok := s.lastHeartbeatByPath[p.ID()]
				s.mutex.RUnlock()
				if !ok || now.Sub(last) > threshold {
					_ = s.pathManager.MarkPathDead(p.ID())
				}
			}
		}
	}
}
