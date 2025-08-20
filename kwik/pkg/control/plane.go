package control

import (
	"context"
	"crypto/rand"
	"fmt"
	"sync"
	"time"

	"kwik/internal/utils"
	"kwik/pkg/protocol"
	"kwik/proto/control"

	"github.com/quic-go/quic-go"
	"google.golang.org/protobuf/proto"
)

// ControlPlaneImpl implements the ControlPlane interface
// It manages control plane operations including frame routing and command handling
type ControlPlaneImpl struct {
	// Path management
	pathStreams map[string]quic.Stream // Control streams per path
	pathsMutex  sync.RWMutex

	// Frame processing
	frameHandlers map[control.ControlFrameType]CommandHandler
	handlersMutex sync.RWMutex

	// Secondary stream support
	secondaryStreamHandlers map[string]SecondaryStreamNotificationHandler // pathID -> handler
	secondaryHandlersMutex  sync.RWMutex

	// Notification management
	notificationSenders map[string]NotificationSender
	sendersMutex        sync.RWMutex

	// Context and lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Configuration
	config *ControlPlaneConfig
}

// ControlPlaneConfig holds configuration for the control plane
type ControlPlaneConfig struct {
	MaxFrameSize      uint32
	FrameTimeout      time.Duration
	HeartbeatInterval time.Duration
	MaxRetries        int
	EnableCompression bool
	EnableEncryption  bool
	BufferSize        int
	ProcessingWorkers int
}

// DefaultControlPlaneConfig returns default control plane configuration
func DefaultControlPlaneConfig() *ControlPlaneConfig {
	return &ControlPlaneConfig{
		MaxFrameSize:      utils.DefaultMaxPacketSize,
		FrameTimeout:      utils.DefaultHandshakeTimeout,
		HeartbeatInterval: utils.DefaultKeepAliveInterval,
		MaxRetries:        3,
		EnableCompression: false,
		EnableEncryption:  false,
		BufferSize:        4096,
		ProcessingWorkers: 2,
	}
}

// NewControlPlane creates a new control plane instance
func NewControlPlane(config *ControlPlaneConfig) *ControlPlaneImpl {
	if config == nil {
		config = DefaultControlPlaneConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())

	cp := &ControlPlaneImpl{
		pathStreams:             make(map[string]quic.Stream),
		frameHandlers:           make(map[control.ControlFrameType]CommandHandler),
		secondaryStreamHandlers: make(map[string]SecondaryStreamNotificationHandler),
		notificationSenders:     make(map[string]NotificationSender),
		ctx:                     ctx,
		cancel:                  cancel,
		config:                  config,
	}

	// Register default frame handlers
	cp.registerDefaultHandlers()

	// Start processing workers
	for i := 0; i < config.ProcessingWorkers; i++ {
		cp.wg.Add(1)
		go cp.frameProcessingWorker(i)
	}

	return cp
}

// SendFrame sends a control frame to a specific path
func (cp *ControlPlaneImpl) SendFrame(pathID string, frame protocol.Frame) error {
	cp.pathsMutex.RLock()
	stream, exists := cp.pathStreams[pathID]
	cp.pathsMutex.RUnlock()

	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}

	// Serialize frame
	data, err := frame.Serialize()
	if err != nil {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			"failed to serialize control frame", err)
	}

	// Check frame size limits
	if len(data) > int(cp.config.MaxFrameSize) {
		return utils.NewKwikError(utils.ErrPacketTooLarge,
			fmt.Sprintf("frame size %d exceeds maximum %d", len(data), cp.config.MaxFrameSize), nil)
	}

	// Send frame with timeout
	ctx, cancel := context.WithTimeout(cp.ctx, cp.config.FrameTimeout)
	defer cancel()

	// Use a goroutine to handle the write with context cancellation
	type writeResult struct {
		n   int
		err error
	}

	writeChan := make(chan writeResult, 1)
	go func() {
		n, err := stream.Write(data)
		writeChan <- writeResult{n: n, err: err}
	}()

	select {
	case result := <-writeChan:
		if result.err != nil {
			return utils.NewKwikError(utils.ErrConnectionLost,
				fmt.Sprintf("failed to send control frame to path %s", pathID), result.err)
		}
		return nil
	case <-ctx.Done():
		return utils.NewKwikError(utils.ErrConnectionLost,
			"control frame send timeout", ctx.Err())
	}
}

// ReceiveFrame receives a control frame from a specific path
func (cp *ControlPlaneImpl) ReceiveFrame(pathID string) (protocol.Frame, error) {
	cp.pathsMutex.RLock()
	stream, exists := cp.pathStreams[pathID]
	cp.pathsMutex.RUnlock()

	if !exists {
		return nil, utils.NewPathNotFoundError(pathID)
	}

	// Read frame data
	buffer := make([]byte, cp.config.BufferSize)
	n, err := stream.Read(buffer)
	if err != nil {
		return nil, utils.NewKwikError(utils.ErrConnectionLost,
			fmt.Sprintf("failed to read control frame from path %s", pathID), err)
	}

	if n == 0 {
		return nil, utils.NewKwikError(utils.ErrInvalidFrame,
			"received empty control frame", nil)
	}

	// Deserialize control frame
	var controlFrame control.ControlFrame
	err = proto.Unmarshal(buffer[:n], &controlFrame)
	if err != nil {
		return nil, utils.NewKwikError(utils.ErrInvalidFrame,
			"failed to deserialize control frame", err)
	}

	// Convert to protocol.Frame
	frame := &ProtocolControlFrame{
		FrameID:      controlFrame.FrameId,
		FrameType:    convertControlFrameType(controlFrame.Type),
		Payload:      controlFrame.Payload,
		Timestamp:    time.Unix(0, int64(controlFrame.Timestamp)),
		SourcePathID: controlFrame.SourcePathId,
		TargetPathID: controlFrame.TargetPathId,
	}

	return frame, nil
}

// HandleAddPathRequest handles AddPathRequest control frames
func (cp *ControlPlaneImpl) HandleAddPathRequest(req *AddPathRequest) error {
	// Validate request
	if req == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "AddPathRequest is nil", nil)
	}

	if req.TargetAddress == "" {
		return utils.NewKwikError(utils.ErrInvalidFrame, "target address is empty", nil)
	}

	if req.SessionID == "" {
		return utils.NewKwikError(utils.ErrInvalidFrame, "session ID is empty", nil)
	}

	// TODO: Implement actual path creation logic
	// This would typically:
	// 1. Validate the target address
	// 2. Check if path already exists
	// 3. Initiate connection to target address
	// 4. Perform authentication
	// 5. Add path to path manager

	return utils.NewKwikError(utils.ErrStreamCreationFailed,
		"AddPathRequest handling not fully implemented", nil)
}

// HandleRemovePathRequest handles RemovePathRequest control frames
func (cp *ControlPlaneImpl) HandleRemovePathRequest(req *RemovePathRequest) error {
	// Validate request
	if req == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "RemovePathRequest is nil", nil)
	}

	if req.PathID == "" {
		return utils.NewKwikError(utils.ErrInvalidFrame, "path ID is empty", nil)
	}

	// TODO: Implement actual path removal logic
	// This would typically:
	// 1. Validate the path exists
	// 2. Check if path can be removed (not primary)
	// 3. Gracefully close streams on the path
	// 4. Remove path from path manager
	// 5. Send confirmation response

	return utils.NewKwikError(utils.ErrStreamCreationFailed,
		"RemovePathRequest handling not fully implemented", nil)
}

// HandleAuthenticationRequest handles AuthenticationRequest control frames
func (cp *ControlPlaneImpl) HandleAuthenticationRequest(req *AuthenticationRequest) error {
	// Validate request
	if req == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "AuthenticationRequest is nil", nil)
	}

	if req.SessionID == "" {
		return utils.NewKwikError(utils.ErrInvalidFrame, "session ID is empty", nil)
	}

	// TODO: Implement actual authentication logic
	// This would typically:
	// 1. Validate credentials
	// 2. Check session ID validity
	// 3. Verify client version compatibility
	// 4. Enable supported features
	// 5. Send authentication response

	return utils.NewKwikError(utils.ErrAuthenticationFailed,
		"AuthenticationRequest handling not fully implemented", nil)
}

// HandleRawPacketTransmission handles RawPacketTransmission control frames
func (cp *ControlPlaneImpl) HandleRawPacketTransmission(req *RawPacketTransmission) error {
	// Validate request
	if req == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "RawPacketTransmission is nil", nil)
	}

	if len(req.Data) == 0 {
		return utils.NewKwikError(utils.ErrInvalidFrame, "raw packet data is empty", nil)
	}

	if req.TargetPathID == "" {
		return utils.NewKwikError(utils.ErrInvalidFrame, "target path ID is empty", nil)
	}

	// Check if target path exists and is active
	cp.pathsMutex.RLock()
	targetStream, exists := cp.pathStreams[req.TargetPathID]
	cp.pathsMutex.RUnlock()

	if !exists {
		return utils.NewPathNotFoundError(req.TargetPathID)
	}

	// Create raw packet transmission notification for client
	notification := &control.RawPacketTransmission{
		Data:           req.Data,
		TargetPathId:   req.TargetPathID,
		SourceServerId: req.SourceServerID,
		ProtocolHint:   req.ProtocolHint,
		PreserveOrder:  req.PreserveOrder,
	}

	// Serialize notification
	payload, err := proto.Marshal(notification)
	if err != nil {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			"failed to serialize raw packet transmission", err)
	}

	// Create control frame
	frame := &control.ControlFrame{
		FrameId:      generateFrameID(),
		Type:         control.ControlFrameType_RAW_PACKET_TRANSMISSION,
		Payload:      payload,
		Timestamp:    uint64(time.Now().UnixNano()),
		SourcePathId: req.SourceServerID, // Source server ID
		TargetPathId: req.TargetPathID,   // Target path for transmission
	}

	// Serialize and send control frame to client
	frameData, err := proto.Marshal(frame)
	if err != nil {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			"failed to serialize raw packet control frame", err)
	}

	// Send to target path's control stream
	_, err = targetStream.Write(frameData)
	if err != nil {
		return utils.NewKwikError(utils.ErrConnectionLost,
			fmt.Sprintf("failed to send raw packet transmission to path %s", req.TargetPathID), err)
	}

	return nil
}

// SendPathStatusNotification sends a path status notification
func (cp *ControlPlaneImpl) SendPathStatusNotification(pathID string, status PathStatus) error {
	// Validate inputs
	if pathID == "" {
		return utils.NewKwikError(utils.ErrInvalidFrame, "path ID is empty", nil)
	}

	// Create path status notification
	notification := &control.PathStatusNotification{
		PathId:    pathID,
		Status:    convertToControlPathStatus(status),
		Reason:    "status change detected",
		Timestamp: uint64(time.Now().UnixNano()),
		Metrics: &control.PathMetrics{
			LastActivity: uint64(time.Now().UnixNano()),
		},
	}

	// Serialize notification
	payload, err := proto.Marshal(notification)
	if err != nil {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			"failed to serialize path status notification", err)
	}

	// Create control frame
	frame := &control.ControlFrame{
		FrameId:      generateFrameID(),
		Type:         control.ControlFrameType_PATH_STATUS_NOTIFICATION,
		Payload:      payload,
		Timestamp:    uint64(time.Now().UnixNano()),
		SourcePathId: pathID,
		TargetPathId: "", // Broadcast to all paths
	}

	// Convert to protocol frame
	protocolFrame := &ProtocolControlFrame{
		FrameID:      frame.FrameId,
		FrameType:    protocol.FrameTypePathStatusNotification,
		Payload:      frame.Payload,
		Timestamp:    time.Unix(0, int64(frame.Timestamp)),
		SourcePathID: frame.SourcePathId,
		TargetPathID: frame.TargetPathId,
	}

	// Send to all paths (broadcast)
	cp.pathsMutex.RLock()
	defer cp.pathsMutex.RUnlock()

	var lastError error
	for currentPathID := range cp.pathStreams {
		if currentPathID != pathID { // Don't send to the path that changed status
			err := cp.SendFrame(currentPathID, protocolFrame)
			if err != nil {
				lastError = err
				// Continue sending to other paths even if one fails
			}
		}
	}

	return lastError
}

// RegisterSecondaryStreamHandler registers a handler for secondary stream notifications on a specific path
func (cp *ControlPlaneImpl) RegisterSecondaryStreamHandler(pathID string, handler SecondaryStreamNotificationHandler) error {
	if pathID == "" {
		return utils.NewKwikError(utils.ErrInvalidFrame, "path ID is empty", nil)
	}

	if handler == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "handler is nil", nil)
	}

	cp.secondaryHandlersMutex.Lock()
	defer cp.secondaryHandlersMutex.Unlock()

	cp.secondaryStreamHandlers[pathID] = handler
	return nil
}

// UnregisterSecondaryStreamHandler unregisters a secondary stream handler for a path
func (cp *ControlPlaneImpl) UnregisterSecondaryStreamHandler(pathID string) {
	cp.secondaryHandlersMutex.Lock()
	defer cp.secondaryHandlersMutex.Unlock()

	delete(cp.secondaryStreamHandlers, pathID)
}

// SendSecondaryStreamMappingUpdate sends a secondary stream mapping update notification
func (cp *ControlPlaneImpl) SendSecondaryStreamMappingUpdate(pathID string, mapping *SecondaryStreamMapping) error {
	if pathID == "" {
		return utils.NewKwikError(utils.ErrInvalidFrame, "path ID is empty", nil)
	}

	if mapping == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "mapping is nil", nil)
	}

	// For now, just notify the handler directly (simplified implementation)
	cp.secondaryHandlersMutex.RLock()
	handler, exists := cp.secondaryStreamHandlers[pathID]
	cp.secondaryHandlersMutex.RUnlock()

	if exists && handler != nil {
		return handler.OnSecondaryStreamMappingUpdate(pathID, mapping)
	}

	// If no handler is registered, this is not an error - just log and continue
	return nil
}

// SendOffsetSyncRequest sends an offset synchronization request
func (cp *ControlPlaneImpl) SendOffsetSyncRequest(pathID string, request *OffsetSyncRequest) error {
	if pathID == "" {
		return utils.NewKwikError(utils.ErrInvalidFrame, "path ID is empty", nil)
	}

	if request == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "request is nil", nil)
	}

	// For now, just notify the handler directly (simplified implementation)
	cp.secondaryHandlersMutex.RLock()
	handler, exists := cp.secondaryStreamHandlers[pathID]
	cp.secondaryHandlersMutex.RUnlock()

	if exists && handler != nil {
		return handler.OnOffsetSyncRequest(pathID, request)
	}

	// If no handler is registered, this is not an error - just log and continue
	return nil
}

// SendOffsetSyncResponse sends an offset synchronization response
func (cp *ControlPlaneImpl) SendOffsetSyncResponse(pathID string, response *OffsetSyncResponse) error {
	if pathID == "" {
		return utils.NewKwikError(utils.ErrInvalidFrame, "path ID is empty", nil)
	}

	if response == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "response is nil", nil)
	}

	// For now, just notify the handler directly (simplified implementation)
	cp.secondaryHandlersMutex.RLock()
	handler, exists := cp.secondaryStreamHandlers[pathID]
	cp.secondaryHandlersMutex.RUnlock()

	if exists && handler != nil {
		return handler.OnOffsetSyncResponse(pathID, response)
	}

	// If no handler is registered, this is not an error - just log and continue
	return nil
}

// RegisterPath registers a control stream for a path
func (cp *ControlPlaneImpl) RegisterPath(pathID string, stream quic.Stream) error {
	if pathID == "" {
		return utils.NewKwikError(utils.ErrInvalidFrame, "path ID is empty", nil)
	}

	if stream == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "stream is nil", nil)
	}

	cp.pathsMutex.Lock()
	defer cp.pathsMutex.Unlock()

	// Check if path already registered
	if _, exists := cp.pathStreams[pathID]; exists {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			fmt.Sprintf("path %s already registered", pathID), nil)
	}

	cp.pathStreams[pathID] = stream

	// Start frame processing for this path
	cp.wg.Add(1)
	go cp.pathFrameProcessor(pathID, stream)

	return nil
}

// UnregisterPath unregisters a control stream for a path
func (cp *ControlPlaneImpl) UnregisterPath(pathID string) error {
	cp.pathsMutex.Lock()
	defer cp.pathsMutex.Unlock()

	stream, exists := cp.pathStreams[pathID]
	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}

	// Close the stream
	stream.Close()

	// Remove from map
	delete(cp.pathStreams, pathID)

	return nil
}

// RegisterFrameHandler registers a handler for a specific frame type
func (cp *ControlPlaneImpl) RegisterFrameHandler(frameType control.ControlFrameType, handler CommandHandler) {
	cp.handlersMutex.Lock()
	defer cp.handlersMutex.Unlock()
	cp.frameHandlers[frameType] = handler
}

// Close closes the control plane and all its resources
func (cp *ControlPlaneImpl) Close() error {
	cp.cancel()

	// Close all path streams
	cp.pathsMutex.Lock()
	for pathID, stream := range cp.pathStreams {
		stream.Close()
		delete(cp.pathStreams, pathID)
	}
	cp.pathsMutex.Unlock()

	// Wait for all workers to finish
	cp.wg.Wait()

	return nil
}

// frameProcessingWorker processes frames in background
func (cp *ControlPlaneImpl) frameProcessingWorker(workerID int) {
	defer cp.wg.Done()

	for {
		select {
		case <-cp.ctx.Done():
			return
		default:
			// Worker processes frames as they arrive
			// This is a placeholder for actual frame processing queue
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// pathFrameProcessor processes frames for a specific path
func (cp *ControlPlaneImpl) pathFrameProcessor(pathID string, stream quic.Stream) {
	defer cp.wg.Done()

	buffer := make([]byte, cp.config.BufferSize)

	for {
		select {
		case <-cp.ctx.Done():
			return
		default:
			// Read frame from stream
			n, err := stream.Read(buffer)
			if err != nil {
				// TODO: Log error and potentially trigger path failure
				return
			}

			if n == 0 {
				continue
			}

			// Process the frame
			cp.processRawFrame(pathID, buffer[:n])
		}
	}
}

// processRawFrame processes a raw frame received from a path
func (cp *ControlPlaneImpl) processRawFrame(pathID string, data []byte) {
	// Deserialize control frame
	var controlFrame control.ControlFrame
	err := proto.Unmarshal(data, &controlFrame)
	if err != nil {
		// TODO: Log invalid frame error
		return
	}

	// Get handler for frame type
	cp.handlersMutex.RLock()
	handler, exists := cp.frameHandlers[controlFrame.Type]
	cp.handlersMutex.RUnlock()

	if !exists {
		// TODO: Log unknown frame type
		return
	}

	// Convert to protocol frame
	protocolFrame := &ProtocolControlFrame{
		FrameID:      controlFrame.FrameId,
		FrameType:    convertControlFrameType(controlFrame.Type),
		Payload:      controlFrame.Payload,
		Timestamp:    time.Unix(0, int64(controlFrame.Timestamp)),
		SourcePathID: controlFrame.SourcePathId,
		TargetPathID: controlFrame.TargetPathId,
	}

	// Handle frame
	err = handler.HandleCommand(protocolFrame)
	if err != nil {
		// TODO: Log handler error
	}
}

// ProtocolControlFrame implements protocol.Frame for control frames
type ProtocolControlFrame struct {
	FrameID      uint64
	FrameType    protocol.FrameType
	Payload      []byte
	Timestamp    time.Time
	SourcePathID string
	TargetPathID string
}

// Type returns the frame type
func (f *ProtocolControlFrame) Type() protocol.FrameType {
	return f.FrameType
}

// Serialize serializes the frame to bytes
func (f *ProtocolControlFrame) Serialize() ([]byte, error) {
	// Convert to protobuf control frame
	controlFrame := &control.ControlFrame{
		FrameId:      f.FrameID,
		Type:         convertToControlFrameType(f.FrameType),
		Payload:      f.Payload,
		Timestamp:    uint64(f.Timestamp.UnixNano()),
		SourcePathId: f.SourcePathID,
		TargetPathId: f.TargetPathID,
	}

	return proto.Marshal(controlFrame)
}

// Deserialize deserializes bytes into the frame
func (f *ProtocolControlFrame) Deserialize(data []byte) error {
	var controlFrame control.ControlFrame
	err := proto.Unmarshal(data, &controlFrame)
	if err != nil {
		return err
	}

	f.FrameID = controlFrame.FrameId
	f.FrameType = convertControlFrameType(controlFrame.Type)
	f.Payload = controlFrame.Payload
	f.Timestamp = time.Unix(0, int64(controlFrame.Timestamp))
	f.SourcePathID = controlFrame.SourcePathId
	f.TargetPathID = controlFrame.TargetPathId

	return nil
}

// Utility functions for type conversion

// convertControlFrameType converts protobuf control frame type to protocol frame type
func convertControlFrameType(pbType control.ControlFrameType) protocol.FrameType {
	switch pbType {
	case control.ControlFrameType_ADD_PATH_REQUEST:
		return protocol.FrameTypeAddPathRequest
	case control.ControlFrameType_ADD_PATH_RESPONSE:
		return protocol.FrameTypeAddPathResponse
	case control.ControlFrameType_REMOVE_PATH_REQUEST:
		return protocol.FrameTypeRemovePathRequest
	case control.ControlFrameType_REMOVE_PATH_RESPONSE:
		return protocol.FrameTypeRemovePathResponse
	case control.ControlFrameType_PATH_STATUS_NOTIFICATION:
		return protocol.FrameTypePathStatusNotification
	case control.ControlFrameType_AUTHENTICATION_REQUEST:
		return protocol.FrameTypeAuthenticationRequest
	case control.ControlFrameType_AUTHENTICATION_RESPONSE:
		return protocol.FrameTypeAuthenticationResponse
	case control.ControlFrameType_STREAM_CREATE_NOTIFICATION:
		return protocol.FrameTypeStreamCreateNotification
	case control.ControlFrameType_RAW_PACKET_TRANSMISSION:
		return protocol.FrameTypeRawPacketTransmission
	case control.ControlFrameType_HEARTBEAT:
		return protocol.FrameTypeAddPathRequest // placeholder, protocol does not define heartbeat
	case control.ControlFrameType_SESSION_CLOSE:
		return protocol.FrameTypeRemovePathRequest // placeholder
	default:
		return protocol.FrameTypeAddPathRequest // Default fallback
	}
}

// convertToControlFrameType converts protocol frame type to protobuf control frame type
func convertToControlFrameType(protoType protocol.FrameType) control.ControlFrameType {
	switch protoType {
	case protocol.FrameTypeAddPathRequest:
		return control.ControlFrameType_ADD_PATH_REQUEST
	case protocol.FrameTypeAddPathResponse:
		return control.ControlFrameType_ADD_PATH_RESPONSE
	case protocol.FrameTypeRemovePathRequest:
		return control.ControlFrameType_REMOVE_PATH_REQUEST
	case protocol.FrameTypeRemovePathResponse:
		return control.ControlFrameType_REMOVE_PATH_RESPONSE
	case protocol.FrameTypePathStatusNotification:
		return control.ControlFrameType_PATH_STATUS_NOTIFICATION
	case protocol.FrameTypeAuthenticationRequest:
		return control.ControlFrameType_AUTHENTICATION_REQUEST
	case protocol.FrameTypeAuthenticationResponse:
		return control.ControlFrameType_AUTHENTICATION_RESPONSE
	case protocol.FrameTypeStreamCreateNotification:
		return control.ControlFrameType_STREAM_CREATE_NOTIFICATION
	case protocol.FrameTypeRawPacketTransmission:
		return control.ControlFrameType_RAW_PACKET_TRANSMISSION
	default:
		return control.ControlFrameType_ADD_PATH_REQUEST // Default fallback
	}
}

// convertToControlPathStatus converts PathStatus to protobuf PathStatus
func convertToControlPathStatus(status PathStatus) control.PathStatus {
	switch status {
	case PathStatusActive:
		return control.PathStatus_CONTROL_PATH_ACTIVE
	case PathStatusDead:
		return control.PathStatus_CONTROL_PATH_DEAD
	case PathStatusConnecting:
		return control.PathStatus_CONTROL_PATH_CONNECTING
	case PathStatusDisconnecting:
		return control.PathStatus_CONTROL_PATH_DISCONNECTING
	default:
		return control.PathStatus_CONTROL_PATH_DEAD // Default fallback
	}
}

// registerDefaultHandlers registers default frame handlers
func (cp *ControlPlaneImpl) registerDefaultHandlers() {
	// Register raw packet transmission handler
	rawPacketHandler := &DefaultRawPacketHandler{controlPlane: cp}
	cp.RegisterFrameHandler(control.ControlFrameType_RAW_PACKET_TRANSMISSION, rawPacketHandler)

	// TODO: Register other default handlers as they are implemented
	// - AddPathRequest handler
	// - RemovePathRequest handler
	// - AuthenticationRequest handler
	// - PathStatusNotification handler
}

// DefaultRawPacketHandler handles raw packet transmission frames
type DefaultRawPacketHandler struct {
	controlPlane *ControlPlaneImpl
}

// HandleCommand handles raw packet transmission commands
func (h *DefaultRawPacketHandler) HandleCommand(frame protocol.Frame) error {
	// For now, just log that we received a raw packet transmission
	// In a full implementation, this would process the raw packet data
	return nil
}

// convertToControlMappingOperation converts MappingOperation to a simple integer representation
// This is a simplified implementation until the protobuf definitions are updated
func convertToControlMappingOperation(operation MappingOperation) int32 {
	switch operation {
	case MappingOperationCreate:
		return 0 // MAPPING_CREATE
	case MappingOperationUpdate:
		return 1 // MAPPING_UPDATE
	case MappingOperationDelete:
		return 2 // MAPPING_DELETE
	default:
		return 0 // Default fallback
	}
}

// generateFrameID generates a unique frame identifier
func generateFrameID() uint64 {
	// Use timestamp + random bytes for better uniqueness
	timestamp := uint64(time.Now().UnixNano())

	// Add some randomness to avoid collisions in high-frequency scenarios
	randomBytes := make([]byte, 4)
	rand.Read(randomBytes)

	// Combine timestamp with random data
	randomPart := uint64(randomBytes[0])<<24 | uint64(randomBytes[1])<<16 |
		uint64(randomBytes[2])<<8 | uint64(randomBytes[3])

	// Use upper bits for timestamp, lower bits for randomness
	return (timestamp & 0xFFFFFFFFFFFF0000) | (randomPart & 0x000000000000FFFF)
}
