package session

import (
	"context"
	"crypto/tls"
	"fmt"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"kwik/internal/utils"
	"kwik/pkg/stream"
	"kwik/pkg/transport"
	"kwik/proto/control"
	"google.golang.org/protobuf/proto"
)

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

	// Stream management
	nextStreamID uint64
	streams      map[uint64]*ServerStream
	streamsMutex sync.RWMutex

	// Channel for accepting incoming streams
	acceptChan chan *ServerStream

	// Synchronization
	mutex  sync.RWMutex
	ctx    context.Context
	cancel context.CancelFunc

	// Configuration
	config *SessionConfig
}

// ServerStream represents a server-side KWIK stream
type ServerStream struct {
	id       uint64
	pathID   string
	session  *ServerSession
	created  time.Time
	state    stream.StreamState
	readBuf  []byte
	writeBuf []byte
	mutex    sync.RWMutex
}

// NewServerSession creates a new KWIK server session
func NewServerSession(sessionID string, pathManager transport.PathManager, config *SessionConfig) *ServerSession {
	if config == nil {
		config = DefaultSessionConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())

	session := &ServerSession{
		sessionID:   sessionID,
		pathManager: pathManager,
		isClient:    false,
		state:       SessionStateActive,
		createdAt:   time.Now(),
		authManager: NewAuthenticationManager(sessionID, false), // false = isServer
		streams:     make(map[uint64]*ServerStream),
		acceptChan:  make(chan *ServerStream, 100),
		ctx:         ctx,
		cancel:      cancel,
		config:      config,
	}

	return session
}

// KwikListener implements the Listener interface for KWIK servers
// It wraps a QUIC listener while providing KWIK-specific functionality
type KwikListener struct {
	quicListener    *quic.Listener
	address         string
	config          *SessionConfig
	activeSessions  map[string]*ServerSession
	sessionsMutex   sync.RWMutex
	ctx             context.Context
	cancel          context.CancelFunc
	closed          bool
	mutex           sync.RWMutex
}

// Listen creates a KWIK listener (QUIC-compatible)
func Listen(address string, config *SessionConfig) (Listener, error) {
	if config == nil {
		config = DefaultSessionConfig()
	}

	// Create QUIC listener
	quicListener, err := quic.ListenAddr(address, generateTLSConfig(), nil)
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

// AcceptWithConfig accepts a new KWIK session with specific configuration
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

	// Generate session ID
	sessionID := generateSessionID()

	// Create server session
	session := NewServerSession(sessionID, pathManager, config)
	session.primaryPath = primaryPath
	
	// Mark primary path as default for operations (Requirement 2.5)
	err = session.markPrimaryPathAsDefault()
	if err != nil {
		session.Close() // Clean up on failure
		return nil, utils.NewKwikError(utils.ErrConnectionLost,
			"failed to mark primary path as default", err)
	}

	// Handle authentication over control plane stream
	err = session.handleAuthentication(ctx)
	if err != nil {
		session.Close() // Clean up on authentication failure
		return nil, utils.NewKwikError(utils.ErrAuthenticationFailed,
			"authentication failed during session establishment", err)
	}

	// Add session to active sessions
	l.sessionsMutex.Lock()
	l.activeSessions[sessionID] = session
	l.sessionsMutex.Unlock()

	// Start session management
	go session.managePaths()
	go session.handleIncomingStreams()

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
	go session.handleIncomingStreams()

	return session, nil
}

// OpenStreamSync opens a new stream synchronously (QUIC-compatible)
func (s *ServerSession) OpenStreamSync(ctx context.Context) (Stream, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.state != SessionStateActive {
		return nil, utils.NewKwikError(utils.ErrConnectionLost, "session is not active", nil)
	}

	// Generate new stream ID
	streamID := s.nextStreamID
	s.nextStreamID++

	// Ensure primary path is available and active (Requirement 2.5)
	if s.primaryPath == nil {
		return nil, utils.NewKwikError(utils.ErrConnectionLost, "no primary path available", nil)
	}
	
	if !s.primaryPath.IsActive() {
		return nil, utils.NewKwikError(utils.ErrPathDead, "primary path is not active", nil)
	}

	// Create logical stream on primary path (default path for operations)
	serverStream := &ServerStream{
		id:       streamID,
		pathID:   s.primaryPath.ID(),
		session:  s,
		created:  time.Now(),
		state:    stream.StreamStateOpen,
		readBuf:  make([]byte, 0, utils.DefaultReadBufferSize),
		writeBuf: make([]byte, 0, utils.DefaultWriteBufferSize),
	}

	s.streamsMutex.Lock()
	s.streams[streamID] = serverStream
	s.streamsMutex.Unlock()

	return serverStream, nil
}

// OpenStream opens a new stream asynchronously (QUIC-compatible)
func (s *ServerSession) OpenStream() (Stream, error) {
	return s.OpenStreamSync(context.Background())
}

// AcceptStream accepts an incoming stream (QUIC-compatible)
func (s *ServerSession) AcceptStream(ctx context.Context) (Stream, error) {
	select {
	case stream := <-s.acceptChan:
		return stream, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.ctx.Done():
		return nil, utils.NewKwikError(utils.ErrConnectionLost, "session closed", nil)
	}
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

	// Create AddPathRequest protobuf message
	addPathReq := &control.AddPathRequest{
		TargetAddress: address,
		SessionId:     s.sessionID,
		Priority:      1, // Default priority for secondary paths
		Metadata:      make(map[string]string),
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

	// TODO: For now, we send the request and return success
	// In a complete implementation, we would:
	// 1. Wait for AddPathResponse with timeout
	// 2. Parse the response and check for success
	// 3. Add the new path to pathManager if successful
	// 4. Return appropriate error if failed
	
	return nil
}

// RemovePath requests removal of a secondary path
func (s *ServerSession) RemovePath(pathID string) error {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if s.state != SessionStateActive {
		return utils.NewKwikError(utils.ErrConnectionLost, "session is not active", nil)
	}

	path := s.pathManager.GetPath(pathID)
	if path == nil {
		return utils.NewPathNotFoundError(pathID)
	}

	if path.IsPrimary() {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			"cannot remove primary path", nil)
	}

	// TODO: Send RemovePathRequest via control plane
	// This should:
	// 1. Create RemovePathRequest protobuf message
	// 2. Send it via control plane stream
	// 3. Wait for RemovePathResponse
	// 4. Remove path from pathManager

	return s.pathManager.RemovePath(pathID)
}

// GetActivePaths returns all active paths
func (s *ServerSession) GetActivePaths() []PathInfo {
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
			CreatedAt:  s.createdAt,
			LastActive: time.Now(),
		}
	}

	return pathInfos
}

// GetDeadPaths returns all dead paths
func (s *ServerSession) GetDeadPaths() []PathInfo {
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
			CreatedAt:  s.createdAt,
			LastActive: time.Now(),
		}
	}

	return pathInfos
}

// GetAllPaths returns all paths (active and dead)
func (s *ServerSession) GetAllPaths() []PathInfo {
	activePaths := s.GetActivePaths()
	deadPaths := s.GetDeadPaths()

	allPaths := make([]PathInfo, len(activePaths)+len(deadPaths))
	copy(allPaths, activePaths)
	copy(allPaths[len(activePaths):], deadPaths)

	return allPaths
}

// SendRawData sends raw data through a specific path
func (s *ServerSession) SendRawData(data []byte, pathID string) error {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	path := s.pathManager.GetPath(pathID)
	if path == nil {
		return utils.NewPathNotFoundError(pathID)
	}

	if !path.IsActive() {
		return utils.NewPathDeadError(pathID)
	}

	// TODO: Implement raw data transmission through control plane
	return utils.NewKwikError(utils.ErrStreamCreationFailed,
		"raw data transmission not yet implemented", nil)
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

// handleAuthentication handles server-side authentication over the control plane stream
func (s *ServerSession) handleAuthentication(ctx context.Context) error {
	// Get control stream from primary path
	controlStream, err := s.primaryPath.GetControlStream()
	if err != nil {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			"failed to get control stream for authentication", err)
	}

	// Read authentication request with timeout
	authCtx, cancel := context.WithTimeout(ctx, utils.DefaultHandshakeTimeout)
	defer cancel()

	requestBuf := make([]byte, 4096)

	// Use a goroutine to handle the read with context cancellation
	type readResult struct {
		n   int
		err error
	}

	readChan := make(chan readResult, 1)
	go func() {
		n, err := controlStream.Read(requestBuf)
		readChan <- readResult{n: n, err: err}
	}()

	var n int
	select {
	case result := <-readChan:
		n, err = result.n, result.err
		if err != nil {
			return utils.NewKwikError(utils.ErrConnectionLost,
				"failed to read authentication request", err)
		}
	case <-authCtx.Done():
		return utils.NewKwikError(utils.ErrAuthenticationFailed,
			"authentication timeout", authCtx.Err())
	}

	// Deserialize authentication request frame
	var requestFrame control.ControlFrame
	err = proto.Unmarshal(requestBuf[:n], &requestFrame)
	if err != nil {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			"failed to deserialize authentication request frame", err)
	}

	// Verify frame type
	if requestFrame.Type != control.ControlFrameType_AUTHENTICATION_REQUEST {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			fmt.Sprintf("expected authentication request, got %v", requestFrame.Type), nil)
	}

	// Handle authentication request and create response
	responseFrame, err := s.authManager.HandleAuthenticationRequest(&requestFrame)
	if err != nil {
		return utils.NewKwikError(utils.ErrAuthenticationFailed,
			"failed to handle authentication request", err)
	}

	// Serialize and send authentication response
	responseData, err := proto.Marshal(responseFrame)
	if err != nil {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			"failed to serialize authentication response", err)
	}

	_, err = controlStream.Write(responseData)
	if err != nil {
		return utils.NewKwikError(utils.ErrConnectionLost,
			"failed to send authentication response", err)
	}

	// Check if authentication was successful
	if !s.authManager.IsAuthenticated() {
		return utils.NewKwikError(utils.ErrAuthenticationFailed,
			"authentication failed", nil)
	}

	return nil
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

// handleIncomingStreams processes incoming streams from clients
func (s *ServerSession) handleIncomingStreams() {
	// TODO: Implement incoming stream handling
}

// ServerStream methods (implementing Stream interface)

// Read reads data from the stream (QUIC-compatible)
func (s *ServerStream) Read(p []byte) (int, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	// Check stream state for QUIC compatibility
	switch s.state {
	case stream.StreamStateClosed, stream.StreamStateResetReceived:
		return 0, utils.NewKwikError(utils.ErrStreamCreationFailed, "stream is closed", nil)
	case stream.StreamStateHalfClosedRemote:
		// Can still read remaining data, but no new data will arrive
		if len(s.readBuf) == 0 {
			return 0, nil // EOF - no more data
		}
	case stream.StreamStateResetSent:
		return 0, utils.NewKwikError(utils.ErrStreamCreationFailed, "stream was reset", nil)
	}

	// Return available data from buffer
	if len(s.readBuf) == 0 {
		return 0, nil // No data available, would block in real implementation
	}

	// Copy data to user buffer
	n := copy(p, s.readBuf)
	s.readBuf = s.readBuf[n:]

	// TODO: Implement actual data reading from aggregated paths
	// This should:
	// 1. Read data from multiple client paths via data plane
	// 2. Aggregate and reorder data based on KWIK logical offsets
	// 3. Return properly ordered data to application
	// 4. Handle flow control and congestion control

	return n, nil
}

// Write writes data to the stream (QUIC-compatible)
func (s *ServerStream) Write(p []byte) (int, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	// Check stream state for QUIC compatibility
	switch s.state {
	case stream.StreamStateClosed, stream.StreamStateResetSent, stream.StreamStateResetReceived:
		return 0, utils.NewKwikError(utils.ErrStreamCreationFailed, "stream is closed", nil)
	case stream.StreamStateHalfClosedLocal:
		return 0, utils.NewKwikError(utils.ErrStreamCreationFailed, "stream is half-closed for writing", nil)
	}

	// Validate input
	if len(p) == 0 {
		return 0, nil // Nothing to write
	}

	// Check if write would exceed buffer limits (QUIC-like flow control)
	if len(s.writeBuf)+len(p) > utils.DefaultWriteBufferSize {
		return 0, utils.NewKwikError(utils.ErrStreamCreationFailed, "write buffer full", nil)
	}

	// TODO: Implement actual data writing to data plane
	// This should:
	// 1. Send data to all client paths via data plane
	// 2. Use proper KWIK logical offsets
	// 3. Handle flow control and congestion control
	// 4. Fragment large writes if necessary

	s.writeBuf = append(s.writeBuf, p...)

	return len(p), nil
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
	
	if s.state == stream.StreamStateOpen {
		s.state = stream.StreamStateHalfClosedLocal
	} else if s.state == stream.StreamStateHalfClosedRemote {
		s.state = stream.StreamStateClosed
	}
	
	// TODO: Send RESET_STREAM frame via control plane
}

// CancelRead cancels the read side of the stream (QUIC-compatible)
func (s *ServerStream) CancelRead(errorCode uint64) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	
	if s.state == stream.StreamStateOpen {
		s.state = stream.StreamStateHalfClosedRemote
	} else if s.state == stream.StreamStateHalfClosedLocal {
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

// generateTLSConfig generates a basic TLS config for testing
func generateTLSConfig() *tls.Config {
	// TODO: Implement proper TLS configuration
	// For now, return a basic config for development
	return &tls.Config{
		InsecureSkipVerify: true,
	}
}
