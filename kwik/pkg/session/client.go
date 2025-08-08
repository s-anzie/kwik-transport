package session

import (
	"context"
	"crypto/rand"
	"fmt"
	"sync"
	"time"

	"kwik/internal/utils"
	"kwik/pkg/stream"
	"kwik/pkg/transport"
	"kwik/proto/control"
	"google.golang.org/protobuf/proto"
)

// ClientSession implements the Session interface for KWIK clients
// It maintains QUIC compatibility while managing multiple paths internally
type ClientSession struct {
	sessionID    string
	pathManager  transport.PathManager
	primaryPath  transport.Path
	isClient     bool
	state        SessionState
	createdAt    time.Time
	
	// Authentication management
	authManager *AuthenticationManager
	
	// Stream management
	nextStreamID uint64
	streams      map[uint64]*stream.ClientStream
	streamsMutex sync.RWMutex
	
	// Channel for accepting incoming streams
	acceptChan chan *stream.ClientStream
	
	// Synchronization
	mutex sync.RWMutex
	ctx   context.Context
	cancel context.CancelFunc
	
	// Configuration
	config *SessionConfig
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
	
	session := &ClientSession{
		sessionID:    sessionID,
		pathManager:  pathManager,
		isClient:     true,
		state:        SessionStateConnecting,
		createdAt:    time.Now(),
		authManager:  NewAuthenticationManager(sessionID, true), // true = isClient
		streams:      make(map[uint64]*stream.ClientStream),
		acceptChan:   make(chan *stream.ClientStream, 100), // Buffered channel
		ctx:          ctx,
		cancel:       cancel,
		config:       config,
	}
	
	return session
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
	
	// Establish primary path with QUIC connection and control plane stream
	primaryPath, err := pathManager.CreatePath(address)
	if err != nil {
		session.Close() // Clean up session if path creation fails
		return nil, utils.NewKwikError(utils.ErrConnectionLost, 
			fmt.Sprintf("failed to create primary path to %s", address), err)
	}
	
	session.primaryPath = primaryPath
	
	// Mark primary path as default for operations (Requirement 2.5)
	err = session.markPrimaryPathAsDefault()
	if err != nil {
		session.Close() // Clean up on failure
		return nil, utils.NewKwikError(utils.ErrConnectionLost,
			"failed to mark primary path as default", err)
	}
	
	// Perform authentication over control plane stream
	err = session.performAuthentication(ctx)
	if err != nil {
		session.Close() // Clean up on authentication failure
		return nil, utils.NewKwikError(utils.ErrAuthenticationFailed,
			"authentication failed during primary path establishment", err)
	}
	
	session.state = SessionStateActive
	
	// Start health monitoring for automatic failure detection
	err = pathManager.StartHealthMonitoring()
	if err != nil {
		session.Close() // Clean up on failure
		return nil, utils.NewKwikError(utils.ErrConnectionLost,
			"failed to start health monitoring", err)
	}
	
	// Set up path status notification handler for the session
	pathManager.SetPathStatusNotificationHandler(session)
	
	// Start session management goroutines
	go session.managePaths()
	go session.handleIncomingStreams()
	
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
	
	// Ensure primary path is available and active (Requirement 2.5)
	if s.primaryPath == nil {
		return nil, utils.NewKwikError(utils.ErrConnectionLost, "no primary path available", nil)
	}
	
	if !s.primaryPath.IsActive() {
		return nil, utils.NewKwikError(utils.ErrPathDead, "primary path is not active", nil)
	}
	
	// Create logical stream on primary path (default path for operations)
	clientStream := stream.NewClientStream(streamID, s.primaryPath.ID(), s)
	
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
func (s *ClientSession) AcceptStream(ctx context.Context) (Stream, error) {
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
func (s *ClientSession) SendRawData(data []byte, pathID string) error {
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
func (s *ClientSession) Close() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	
	if s.state == SessionStateClosed {
		return nil // Already closed
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
	// Start control frame processing loop
	go s.handleControlFrames()
	
	// TODO: Implement incoming stream handling for data plane
	// This would listen on control plane for stream creation notifications
	// and create corresponding ClientStream objects
}

// handleControlFrames processes incoming control frames from the server
func (s *ClientSession) handleControlFrames() {
	// Get control stream from primary path
	controlStream, err := s.primaryPath.GetControlStream()
	if err != nil {
		// TODO: Log error and potentially trigger session failure
		return
	}
	
	// Buffer for reading control frames
	buffer := make([]byte, 4096)
	
	for {
		select {
		case <-s.ctx.Done():
			return // Session is closing
		default:
			// Read control frame from stream
			n, err := controlStream.Read(buffer)
			if err != nil {
				// TODO: Handle read errors (connection lost, etc.)
				continue
			}
			
			if n == 0 {
				continue // No data available
			}
			
			// Parse control frame
			var frame control.ControlFrame
			err = proto.Unmarshal(buffer[:n], &frame)
			if err != nil {
				// TODO: Log invalid frame error
				continue
			}
			
			// Process frame based on type
			s.processControlFrame(&frame)
		}
	}
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
	default:
		// TODO: Log unknown frame type
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
	
	// Attempt to create secondary path
	startTime := time.Now()
	secondaryPath, err := s.pathManager.CreatePath(addPathReq.TargetAddress)
	connectionTime := time.Since(startTime)
	
	if err != nil {
		s.sendAddPathResponse("", false, 
			fmt.Sprintf("failed to create path to %s: %v", addPathReq.TargetAddress, err),
			"CONNECTION_FAILED")
		return
	}
	
	// Perform authentication on secondary path using existing session ID
	err = s.performSecondaryPathAuthentication(context.Background(), secondaryPath)
	if err != nil {
		// Authentication failed, remove the path and send failure response
		s.pathManager.RemovePath(secondaryPath.ID())
		s.sendAddPathResponse("", false, 
			fmt.Sprintf("authentication failed on secondary path: %v", err),
			"AUTHENTICATION_FAILED")
		return
	}
	
	// Integrate secondary path into session aggregate (Requirement 3.7)
	err = s.integrateSecondaryPath(secondaryPath)
	if err != nil {
		// Integration failed, remove the path and send failure response
		s.pathManager.RemovePath(secondaryPath.ID())
		s.sendAddPathResponse("", false, 
			fmt.Sprintf("failed to integrate secondary path: %v", err),
			"INTEGRATION_FAILED")
		return
	}
	
	// Send success response with actual connection time
	s.sendAddPathResponseWithTime(secondaryPath.ID(), true, "", "", connectionTime)
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
		FrameId:      generateFrameID(),
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
		FrameId:      generateFrameID(),
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
	// TODO: Implement in future tasks
}

func (s *ClientSession) handleHeartbeat(frame *control.ControlFrame) {
	// TODO: Implement in future tasks
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
	// Get control stream from primary path
	controlStream, err := s.primaryPath.GetControlStream()
	if err != nil {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			"failed to get control stream for authentication", err)
	}
	
	// Create authentication request
	authFrame, err := s.authManager.CreateAuthenticationRequest()
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
	
	// Verify frame type
	if responseFrame.Type != control.ControlFrameType_AUTHENTICATION_RESPONSE {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			fmt.Sprintf("expected authentication response, got %v", responseFrame.Type), nil)
	}
	
	// Handle authentication response
	err = s.authManager.HandleAuthenticationResponse(&responseFrame)
	if err != nil {
		return utils.NewKwikError(utils.ErrAuthenticationFailed,
			"authentication failed", err)
	}
	
	return nil
}

// GetSessionID returns the session ID (for external access)
func (s *ClientSession) GetSessionID() string {
	return s.sessionID
}

// IsAuthenticated returns whether the session is authenticated
func (s *ClientSession) IsAuthenticated() bool {
	if s.authManager == nil {
		return false
	}
	return s.authManager.IsAuthenticated()
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
}

// GetContext returns the session context
func (s *ClientSession) GetContext() context.Context {
	return s.ctx
}

// performSecondaryPathAuthentication performs authentication on a secondary path using existing session ID
func (s *ClientSession) performSecondaryPathAuthentication(ctx context.Context, secondaryPath transport.Path) error {
	// Get control stream from secondary path
	controlStream, err := secondaryPath.GetControlStream()
	if err != nil {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			"failed to get control stream for secondary path authentication", err)
	}
	
	// Create authentication request using existing session ID
	// This is the key difference from primary path authentication
	authFrame, err := s.authManager.CreateAuthenticationRequest()
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
	
	return nil
}

// integrateSecondaryPath integrates a secondary path into the session aggregate
// This implements requirement 3.7: secondary paths are added to session aggregate and available for traffic distribution
func (s *ClientSession) integrateSecondaryPath(secondaryPath transport.Path) error {
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
	
	// Verify the path is already in the path manager (it should be added by CreatePath)
	managedPath := s.pathManager.GetPath(secondaryPath.ID())
	if managedPath == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "secondary path not found in path manager", nil)
	}
	
	// Verify the path IDs match
	if managedPath.ID() != secondaryPath.ID() {
		return utils.NewKwikError(utils.ErrInvalidFrame, "path ID mismatch during integration", nil)
	}
	
	// At this point, the path is already integrated into the PathManager by CreatePath()
	// The path is now available for:
	// 1. Health monitoring (already started)
	// 2. Data plane operations (through PathManager)
	// 3. Path queries (GetActivePaths, etc.)
	// 4. Future data aggregation (will be implemented in later tasks)
	
	// TODO: In future tasks, we might add:
	// - Data plane stream setup for aggregation
	// - Load balancing configuration
	// - Path-specific metrics initialization
	
	return nil
}

// generateSessionID generates a unique session identifier
func generateSessionID() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return fmt.Sprintf("kwik-session-%x", bytes)
}