package session

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"sync"
	"time"

	"kwik/internal/utils"
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

func (d *DefaultSessionLogger) Debug(msg string, keysAndValues ...interface{}) {}
func (d *DefaultSessionLogger) Info(msg string, keysAndValues ...interface{})  {}
func (d *DefaultSessionLogger) Warn(msg string, keysAndValues ...interface{})  {}
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

	// Secondary stream management
	secondaryStreamHandler stream.SecondaryStreamHandler
	streamAggregator       data.DataAggregator
	secondaryAggregator    *data.SecondaryStreamAggregator
	metadataProtocol       *stream.MetadataProtocolImpl

	// Data presentation management
	dataPresentationManager *presentation.DataPresentationManagerImpl

	// Channel for accepting incoming streams
	acceptChan chan *stream.ClientStream

	// Synchronization
	mutex  sync.RWMutex
	ctx    context.Context
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

	// Create data presentation manager
	presentationConfig := presentation.DefaultPresentationConfig()
	dataPresentationManager := presentation.NewDataPresentationManager(presentationConfig)

	session := &ClientSession{
		sessionID:               sessionID,
		pathManager:             pathManager,
		isClient:                true,
		state:                   SessionStateConnecting,
		createdAt:               time.Now(),
		authManager:             NewAuthenticationManager(sessionID, true), // true = isClient
		nextStreamID:            1,                                         // Start stream IDs from 1 (QUIC-compatible)
		streams:                 make(map[uint64]*stream.ClientStream),
		secondaryStreamHandler:  stream.NewSecondaryStreamHandler(nil), // Use default config
		streamAggregator:        data.NewDataAggregator(&DefaultSessionLogger{}),
		secondaryAggregator:     data.NewSecondaryStreamAggregator(&DefaultSessionLogger{}),  // Initialize secondary stream aggregator
		metadataProtocol:        stream.NewMetadataProtocol(),         // Initialize metadata protocol
		dataPresentationManager: dataPresentationManager,              // Initialize data presentation manager
		acceptChan:              make(chan *stream.ClientStream, 100), // Buffered channel
		ctx:                     ctx,
		cancel:                  cancel,
		config:                  config,
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

	// Establish primary path with QUIC connection
	primaryPath, err := pathManager.CreatePath(address)
	if err != nil {
		session.Close() // Clean up session if path creation fails
		return nil, utils.NewKwikError(utils.ErrConnectionLost,
			fmt.Sprintf("failed to create primary path to %s", address), err)
	}

	session.primaryPath = primaryPath

	// CLIENT CREATES THE CONTROL STREAM (OpenStreamSync)
	// This MUST be the very first stream (ID 0)
	_, err = primaryPath.CreateControlStreamAsClient()
	if err != nil {
		fmt.Printf("DEBUG: Client FAILED to create control stream: %v\n", err)
		session.Close() // Clean up on failure
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"failed to create control stream as client", err)
	}

	// IMMEDIATELY send authentication after control stream creation
	// This ensures the server can read the authentication request right away
	err = session.PerformAuthentication(ctx)
	if err != nil {
		fmt.Printf("DEBUG: Client authentication FAILED: %v\n", err)
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

	session.state = SessionStateActive

	// Start the data presentation manager
	err = session.dataPresentationManager.Start()
	if err != nil {
		session.Close() // Clean up on failure
		return nil, utils.NewKwikError(utils.ErrConnectionLost,
			"failed to start data presentation manager", err)
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

	// Start session management goroutines
	go session.managePaths()
	// NOTE: We start handleIncomingStreams AFTER authentication is complete
	// to avoid the control stream being blocked by the Read() in handleControlFrames
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

	// Create actual QUIC stream on primary path
	conn := s.primaryPath.GetConnection()
	if conn == nil {
		return nil, utils.NewKwikError(utils.ErrConnectionLost, "no QUIC connection available", nil)
	}

	quicStream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed, "failed to create QUIC stream", err)
	}

	// Create KWIK stream wrapper with underlying QUIC stream
	clientStream := stream.NewClientStreamWithQuic(streamID, s.primaryPath.ID(), s, quicStream)

	// Create stream buffer in DataPresentationManager for aggregation support
	if s.dataPresentationManager != nil {
		err = s.dataPresentationManager.CreateStreamBuffer(streamID, nil)
		if err != nil {
			// Close the QUIC stream if buffer creation fails
			quicStream.Close()
			return nil, utils.NewKwikError(utils.ErrStreamCreationFailed, "failed to create stream buffer", err)
		}
	}

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
			go s.handleSecondaryStreamOpen(path.ID(), quicStream)
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
func (s *ClientSession) SendRawData(data []byte, pathID string, remoteStreamID ...uint64) error {
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

	// Send raw data directly to the target path's data plane
	// This is simpler than the server approach since client directly accesses paths
	return s.routeRawPacketToDataPlane(data, path, "custom", true)
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
		state := s.state
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

	// Get control stream from primary path
	controlStream, err := s.primaryPath.GetControlStream()
	if err != nil {
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
				fmt.Printf("DEBUG: Client control frame read error: %v\n", err)
				time.Sleep(100 * time.Millisecond) // Avoid tight loop
				continue
			}

			if n == 0 {
				continue // No data available
			}
			// Parse control frame
			var frame control.ControlFrame
			err = proto.Unmarshal(buffer[:n], &frame)
			if err != nil {
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

	// Validate path ID
	if addPathReq.PathId == "" {
		fmt.Printf("DEBUG: Client received AddPathRequest with empty PathId\n")
		s.sendAddPathResponse("", false, "path ID is empty", "INVALID_PATH_ID")
		return
	}

	fmt.Printf("DEBUG: Client received AddPathRequest with PathId: %s, TargetAddress: %s\n",
		addPathReq.PathId, addPathReq.TargetAddress)
	// Attempt to create secondary path
	startTime := time.Now()
	fmt.Printf("DEBUG: Client attempting to create path to %s\n", addPathReq.TargetAddress)
	secondaryPath, err := s.pathManager.CreatePath(addPathReq.TargetAddress)
	connectionTime := time.Since(startTime)

	if err != nil {
		fmt.Printf("DEBUG: Client failed to create path to %s: %v\n", addPathReq.TargetAddress, err)
		s.sendAddPathResponse("", false,
			fmt.Sprintf("failed to create path to %s: %v", addPathReq.TargetAddress, err),
			"CONNECTION_FAILED")
		return
	}
	fmt.Printf("DEBUG: Client successfully created path to %s in %v\n", addPathReq.TargetAddress, connectionTime)

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
	fmt.Printf("DEBUG: Client starting authentication on secondary path %s\n", secondaryPath.ID())
	err = s.performSecondaryPathAuthentication(context.Background(), secondaryPath)
	if err != nil {
		// Authentication failed, remove the path and send failure response
		fmt.Printf("DEBUG: Client authentication failed on secondary path: %v\n", err)
		s.pathManager.RemovePath(secondaryPath.ID())
		s.sendAddPathResponse("", false,
			fmt.Sprintf("authentication failed on secondary path: %v", err),
			"AUTHENTICATION_FAILED")
		return
	}
	fmt.Printf("DEBUG: Client authentication successful on secondary path %s\n", secondaryPath.ID())

	// Integrate secondary path into session aggregate (Requirement 3.7)
	fmt.Printf("DEBUG: Client starting integration of secondary path %s\n", secondaryPath.ID())
	err = s.integrateSecondaryPath(secondaryPath, originalPathID)
	if err != nil {
		// Integration failed, remove the path using the original ID
		fmt.Printf("DEBUG: Client integration failed for secondary path: %v\n", err)
		s.pathManager.RemovePath(originalPathID)
		s.sendAddPathResponse("", false,
			fmt.Sprintf("failed to integrate secondary path: %v", err),
			"INTEGRATION_FAILED")
		return
	}
	fmt.Printf("DEBUG: Client integration successful for secondary path %s\n", secondaryPath.ID())

	// Send success response with actual connection time
	fmt.Printf("DEBUG: Client sending success response for path %s\n", secondaryPath.ID())
	s.sendAddPathResponseWithTime(secondaryPath.ID(), true, "", "", connectionTime)
	fmt.Printf("DEBUG: Client sent AddPathResponse with success=true for path %s\n", secondaryPath.ID())

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
		fmt.Printf("DEBUG: Client target path %s is not active\n", rawPacketReq.TargetPathId)
		return
	}

	// Route raw packet to data plane of target path
	err = s.routeRawPacketToDataPlane(rawPacketReq.Data, targetPath, rawPacketReq.ProtocolHint, rawPacketReq.PreserveOrder)
	if err != nil {
		return
	}

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

// PerformAuthentication performs authentication over the control plane stream
func (s *ClientSession) PerformAuthentication(ctx context.Context) error {
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
			fmt.Printf("DEBUG: Failed to remove stream buffer %d: %v\n", streamID, err)
		}
	}
}

// GetContext returns the session context
func (s *ClientSession) GetContext() context.Context {
	return s.ctx
}

// GetSecondaryStreamAggregator returns the secondary stream aggregator
func (s *ClientSession) GetSecondaryStreamAggregator() *data.SecondaryStreamAggregator {
	return s.secondaryAggregator
}

// GetMetadataProtocol returns the metadata protocol instance
func (s *ClientSession) GetMetadataProtocol() *stream.MetadataProtocolImpl {
	return s.metadataProtocol
}

// GetDataPresentationManager returns the data presentation manager
func (s *ClientSession) GetDataPresentationManager() stream.DataPresentationManager {
	return s.dataPresentationManager
}

// ReadFromPresentationStream reads data from a stream using the presentation manager
func (s *ClientSession) ReadFromPresentationStream(streamID uint64, buffer []byte, timeout time.Duration) (int, error) {
	if s.dataPresentationManager == nil {
		return 0, utils.NewKwikError(utils.ErrStreamCreationFailed, "no data presentation manager available", nil)
	}
	return s.dataPresentationManager.ReadFromStreamWithTimeout(streamID, buffer, timeout)
}

// GetAggregatedDataForStream returns aggregated data from secondary streams for a KWIK stream
func (s *ClientSession) GetAggregatedDataForStream(streamID uint64) ([]byte, error) {
	if s.secondaryAggregator == nil {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed, "no secondary aggregator available", nil)
	}

	// Get aggregated data from the secondary stream aggregator
	return s.secondaryAggregator.GetAggregatedData(streamID)
}

// ConsumeAggregatedDataForStream consumes and removes aggregated data from the aggregator
func (s *ClientSession) ConsumeAggregatedDataForStream(streamID uint64, bytesToConsume int) ([]byte, error) {
	if s.secondaryAggregator == nil {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed, "no secondary aggregator available", nil)
	}

	// Consume aggregated data from the secondary stream aggregator (with forward sliding)
	return s.secondaryAggregator.ConsumeAggregatedData(streamID, bytesToConsume)
}

// DepositPrimaryDataInAggregator deposits primary stream data into the aggregator
func (s *ClientSession) DepositPrimaryDataInAggregator(streamID uint64, data []byte, offset uint64) error {
	if s.secondaryAggregator == nil {
		return utils.NewKwikError(utils.ErrStreamCreationFailed, "no secondary aggregator available", nil)
	}

	// Create SecondaryStreamData structure for primary data
	primaryData := &stream.SecondaryStreamData{
		StreamID:     0,         // Use 0 for primary stream data
		PathID:       "primary", // Mark as primary path
		Data:         data,
		Offset:       offset,
		KwikStreamID: streamID,
		Timestamp:    time.Now(),
		SequenceNum:  0,
	}

	// Deposit primary data into the aggregator
	fmt.Printf("DEBUG: ClientSession depositing %d bytes of primary data for KWIK stream %d at offset %d\n",
		len(data), streamID, offset)

	return s.secondaryAggregator.AggregateSecondaryData(primaryData)
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
	fmt.Printf("DEBUG: Client creating SECONDARY authentication request for session %s\n", s.sessionID)
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
	fmt.Printf("DEBUG: Client found managed path with original ID %s, secondary path has server ID %s\n", managedPath.ID(), secondaryPath.ID())

	// At this point, the path is already integrated into the PathManager by CreatePath()
	// The path is now available for:
	// 1. Health monitoring (already started)
	// 2. Data plane operations (through PathManager)
	// 3. Path queries (GetActivePaths, etc.)
	// 4. Automatic stream acceptance and aggregation (implemented below)

	// Start automatic stream acceptance for this secondary path
	// This runs in background and automatically handles all streams from secondary servers
	fmt.Printf("DEBUG: Client starting automatic stream acceptance for secondary path %s\n", secondaryPath.ID())
	go s.autoAcceptSecondaryStreams(secondaryPath)

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

	fmt.Printf("DEBUG: Client routing raw packet (%d bytes) to data plane of path %s\n", len(data), targetPath.ID())

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

// getSecondaryStreamHandler returns the secondary stream handler (internal method)
func (s *ClientSession) getSecondaryStreamHandler() stream.SecondaryStreamHandler {
	return s.secondaryStreamHandler
}

// getStreamAggregator returns the stream aggregator (internal method)
func (s *ClientSession) getStreamAggregator() data.DataAggregator {
	return s.streamAggregator
}

// handleSecondaryStreamOpen handles a new secondary stream from a secondary server
// This method processes streams internally without exposing them to the public interface
func (s *ClientSession) handleSecondaryStreamOpen(pathID string, quicStream quic.Stream) error {
	fmt.Printf("DEBUG: Client handling secondary stream open from path %s\n", pathID)

	// Handle the secondary stream using the secondary stream handler
	_, err := s.secondaryStreamHandler.HandleSecondaryStream(pathID, quicStream)
	if err != nil {
		fmt.Printf("DEBUG: Client failed to handle secondary stream from path %s: %v\n", pathID, err)
		// Log error and close the stream
		quicStream.Close()
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			fmt.Sprintf("failed to handle secondary stream from path %s", pathID), err)
	}

	fmt.Printf("DEBUG: Client starting secondary stream data processing for path %s\n", pathID)
	// Start processing the secondary stream data in a goroutine
	go s.processSecondaryStreamData(pathID, quicStream)

	return nil
}

// processSecondaryStreamData processes data from a secondary stream
func (s *ClientSession) processSecondaryStreamData(pathID string, quicStream quic.Stream) {
	defer quicStream.Close()
	fmt.Printf("DEBUG: Client starting secondary stream data processing loop for path %s\n", pathID)

	buffer := make([]byte, 4096)
	for {
		select {
		case <-s.ctx.Done():
			fmt.Printf("DEBUG: Client secondary stream processing stopping for path %s (session closing)\n", pathID)
			return // Session is closing
		default:
			// Read data from the secondary stream
			fmt.Printf("DEBUG: Client attempting to read from secondary stream on path %s\n", pathID)
			n, err := quicStream.Read(buffer)
			if err != nil {
				// Stream closed or error occurred
				fmt.Printf("DEBUG: Client secondary stream from path %s closed: %v\n", pathID, err)
				return
			}

			if n == 0 {
				fmt.Printf("DEBUG: Client no data available from secondary stream on path %s\n", pathID)
				continue // No data available
			}

			fmt.Printf("DEBUG: Client received %s bytes from secondary stream on path %d: %s\n", pathID, n, string(buffer[:n]))

			// Process the encapsulated data according to the metadata protocol
			err = s.processEncapsulatedSecondaryData(pathID, buffer[:n])
			if err != nil {
				fmt.Printf("DEBUG: Client error processing secondary stream data from path %s: %v\n", pathID, err)
				// Continue processing other data even if one frame fails
			} else {
				fmt.Printf("DEBUG: Client successfully processed secondary stream data from path %s\n", pathID)
			}
		}
	}
}

// processEncapsulatedSecondaryData decapsulates and aggregates secondary stream data
func (s *ClientSession) processEncapsulatedSecondaryData(pathID string, encapsulatedData []byte) error {
	fmt.Printf("DEBUG: Client processing encapsulated secondary data from path %s (%d bytes)\n", pathID, len(encapsulatedData))

	// Get the metadata protocol instance
	metadataProtocol := s.metadataProtocol
	if metadataProtocol == nil {
		fmt.Printf("DEBUG: Client no metadata protocol available for path %s\n", pathID)
		return utils.NewKwikError(utils.ErrStreamCreationFailed, "no metadata protocol available", nil)
	}

	fmt.Printf("DEBUG: Client attempting to decapsulate data from path %s\n", pathID)
	// Decapsulate the metadata and data
	metadata, data, err := metadataProtocol.DecapsulateData(encapsulatedData)
	if err != nil {
		fmt.Printf("DEBUG: Client failed to decapsulate data from path %s: %v\n", pathID, err)
		return utils.NewKwikError(utils.ErrInvalidFrame,
			fmt.Sprintf("failed to decapsulate secondary stream data: %v", err), err)
	}

	fmt.Printf("DEBUG: Client decapsulated data from path %s: KwikStreamID=%d, Offset=%d, DataLen=%d\n",
		pathID, metadata.KwikStreamID, metadata.Offset, len(data))

	// Create SecondaryStreamData structure for aggregation
	secondaryData := &stream.SecondaryStreamData{
		StreamID:     0, // Will be set by the aggregator based on the path
		PathID:       pathID,
		Data:         data,
		Offset:       metadata.Offset,
		KwikStreamID: metadata.KwikStreamID,
		Timestamp:    time.Now(),
		SequenceNum:  0, // Could be derived from metadata if needed
	}

	// Get the secondary stream aggregator
	aggregator := s.secondaryAggregator
	if aggregator == nil {
		fmt.Printf("DEBUG: Client no secondary aggregator available for path %s\n", pathID)
		return utils.NewKwikError(utils.ErrStreamCreationFailed, "no secondary aggregator available", nil)
	}

	fmt.Printf("DEBUG: Client attempting to aggregate secondary data from path %s\n", pathID)
	// Aggregate the secondary stream data
	err = aggregator.AggregateSecondaryData(secondaryData)
	if err != nil {
		fmt.Printf("DEBUG: Client failed to aggregate secondary data from path %s: %v\n", pathID, err)
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			fmt.Sprintf("failed to aggregate secondary stream data: %v", err), err)
	}

	fmt.Printf("DEBUG: Client successfully aggregated %d bytes from path %s to KWIK stream %d at offset %d\n",
		len(data), pathID, metadata.KwikStreamID, metadata.Offset)

	return nil
}

// autoAcceptSecondaryStreams automatically accepts and processes streams from a secondary path
// This runs in background and handles all streams from secondary servers transparently
func (s *ClientSession) autoAcceptSecondaryStreams(path transport.Path) {
	fmt.Printf("DEBUG: Client starting automatic stream acceptance loop for secondary path %s\n", path.ID())

	conn := path.GetConnection()
	if conn == nil {
		fmt.Printf("DEBUG: Client no QUIC connection available for secondary path %s\n", path.ID())
		return
	}

	for {
		select {
		case <-s.ctx.Done():
			fmt.Printf("DEBUG: Client stopping automatic stream acceptance for secondary path %s (session closing)\n", path.ID())
			return
		default:
			// Accept streams from this secondary path
			fmt.Printf("DEBUG: Client attempting to accept stream from secondary path %s\n", path.ID())
			quicStream, err := conn.AcceptStream(context.Background())
			if err != nil {
				fmt.Printf("DEBUG: Client failed to accept stream from secondary path %s: %v\n", path.ID(), err)
				// If the connection is closed or there's an error, stop this goroutine
				return
			}

			fmt.Printf("DEBUG: Client accepted new stream from secondary path %s\n", path.ID())

			// Handle this secondary stream internally (don't expose to application)
			go s.handleSecondaryStreamOpen(path.ID(), quicStream)
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
