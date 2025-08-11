package data

import (
	"context"
	"fmt"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
	"kwik/internal/utils"
	"kwik/proto/data"
)

// DataPlaneImpl implements the DataPlane interface
// It manages data plane operations for application data transmission
type DataPlaneImpl struct {
	// Path management
	pathStreams map[string]DataStream // Data streams per path
	pathsMutex  sync.RWMutex

	// Stream management
	logicalStreams map[uint64]*LogicalStreamState
	streamsMutex   sync.RWMutex

	// Data aggregation (for client-side multi-path)
	aggregator DataAggregator
	scheduler  DataScheduler

	// Flow control
	flowController FlowController

	// Frame processing
	frameProcessor FrameProcessor

	// Write routing (for client-side primary path enforcement)
	clientWriteRouter *ClientWriteRouter
	serverWriteRouter *ServerWriteRouter
	isClientSession   bool

	// Context and lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Configuration
	config *DataPlaneConfig

	// Statistics
	stats      *DataPlaneStats
	statsMutex sync.RWMutex
}

// DataPlaneConfig holds configuration for the data plane
type DataPlaneConfig struct {
	MaxFrameSize          uint32
	MaxPacketSize         uint32
	BufferSize            int
	AggregationEnabled    bool
	FlowControlEnabled    bool
	CompressionEnabled    bool
	ProcessingWorkers     int
	AckTimeout            time.Duration
	RetransmissionTimeout time.Duration
	MaxRetransmissions    int
}

// DefaultDataPlaneConfig returns default data plane configuration
func DefaultDataPlaneConfig() *DataPlaneConfig {
	return &DataPlaneConfig{
		MaxFrameSize:          utils.DefaultMaxPacketSize - utils.KwikHeaderSize,
		MaxPacketSize:         utils.DefaultMaxPacketSize,
		BufferSize:            utils.DefaultReadBufferSize,
		AggregationEnabled:    true,
		FlowControlEnabled:    true,
		CompressionEnabled:    false,
		ProcessingWorkers:     2,
		AckTimeout:            100 * time.Millisecond,
		RetransmissionTimeout: 500 * time.Millisecond,
		MaxRetransmissions:    3,
	}
}

// LogicalStreamState represents the state of a logical KWIK stream
type LogicalStreamState struct {
	StreamID      uint64
	PathID        string
	State         data.StreamStateType
	BytesSent     uint64
	BytesReceived uint64
	MaxStreamData uint64
	FinSent       bool
	FinReceived   bool
	LastActivity  time.Time
	Buffer        []byte
	Offset        uint64
	mutex         sync.RWMutex
}

// DataPlaneStats contains statistics for the data plane
type DataPlaneStats struct {
	TotalBytesSent      uint64
	TotalBytesReceived  uint64
	TotalFramesSent     uint64
	TotalFramesReceived uint64
	PathStats           map[string]*data.PathDataStats
	StartTime           time.Time
}

// NewDataPlane creates a new data plane instance
func NewDataPlane(config *DataPlaneConfig) *DataPlaneImpl {
	if config == nil {
		config = DefaultDataPlaneConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())

	dp := &DataPlaneImpl{
		pathStreams:    make(map[string]DataStream),
		logicalStreams: make(map[uint64]*LogicalStreamState),
		ctx:            ctx,
		cancel:         cancel,
		config:         config,
		stats: &DataPlaneStats{
			PathStats: make(map[string]*data.PathDataStats),
			StartTime: time.Now(),
		},
	}

	// Initialize components
	if config.AggregationEnabled {
		dp.aggregator = NewDataAggregator()
	}

	dp.scheduler = NewDataScheduler()

	if config.FlowControlEnabled {
		dp.flowController = NewFlowController()
	}

	dp.frameProcessor = NewFrameProcessor()

	// Mark as server session (default)
	dp.isClientSession = false

	// Start processing workers
	for i := 0; i < config.ProcessingWorkers; i++ {
		dp.wg.Add(1)
		go dp.dataProcessingWorker(i)
	}

	return dp
}

// NewClientDataPlane creates a new data plane instance optimized for client-side aggregation
func NewClientDataPlane(config *DataPlaneConfig) *DataPlaneImpl {
	if config == nil {
		config = DefaultDataPlaneConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())

	dp := &DataPlaneImpl{
		pathStreams:    make(map[string]DataStream),
		logicalStreams: make(map[uint64]*LogicalStreamState),
		ctx:            ctx,
		cancel:         cancel,
		config:         config,
		stats: &DataPlaneStats{
			PathStats: make(map[string]*data.PathDataStats),
			StartTime: time.Now(),
		},
	}

	// Initialize components with client-specific aggregator
	if config.AggregationEnabled {
		// Use client-specific aggregator for multi-path data aggregation
		dp.aggregator = NewClientDataAggregator()
	}

	dp.scheduler = NewDataScheduler()

	if config.FlowControlEnabled {
		dp.flowController = NewFlowController()
	}

	dp.frameProcessor = NewFrameProcessor()

	// Mark as client session for proper write routing
	dp.isClientSession = true

	// Start processing workers
	for i := 0; i < config.ProcessingWorkers; i++ {
		dp.wg.Add(1)
		go dp.dataProcessingWorker(i)
	}

	return dp
}

// InitializeClientWriteRouter initializes the client write router with primary path
func (dp *DataPlaneImpl) InitializeClientWriteRouter(primaryPathID string) error {
	if !dp.isClientSession {
		return utils.NewKwikError(utils.ErrInvalidFrame, "write router can only be initialized for client sessions", nil)
	}

	if primaryPathID == "" {
		return utils.NewKwikError(utils.ErrInvalidFrame, "primary path ID cannot be empty", nil)
	}

	dp.clientWriteRouter = NewClientWriteRouter(primaryPathID, dp)
	return nil
}

// InitializeServerWriteRouter initializes the server write router
func (dp *DataPlaneImpl) InitializeServerWriteRouter(serverPathID string) error {
	if dp.isClientSession {
		return utils.NewKwikError(utils.ErrInvalidFrame, "server write router cannot be initialized for client sessions", nil)
	}

	if serverPathID == "" {
		return utils.NewKwikError(utils.ErrInvalidFrame, "server path ID cannot be empty", nil)
	}

	dp.serverWriteRouter = NewServerWriteRouter(serverPathID, dp)
	return nil
}

// SendDataWithRouting sends data using appropriate routing logic
func (dp *DataPlaneImpl) SendDataWithRouting(frame *data.DataFrame) error {
	if dp.isClientSession {
		// For client sessions, enforce primary-only routing
		if dp.clientWriteRouter == nil {
			return utils.NewKwikError(utils.ErrInvalidFrame, "client write router not initialized", nil)
		}
		return dp.clientWriteRouter.SendDataToPrimary(frame)
	} else {
		// For server sessions, use server routing logic
		if dp.serverWriteRouter == nil {
			return utils.NewKwikError(utils.ErrInvalidFrame, "server write router not initialized", nil)
		}

		// Route the write to get target path
		targetPathID, err := dp.serverWriteRouter.RouteWrite(frame)
		if err != nil {
			return err
		}

		return dp.serverWriteRouter.SendDataToPath(frame, targetPathID)
	}
}

// ValidateWriteOperation validates a write operation based on session type
func (dp *DataPlaneImpl) ValidateWriteOperation(frame *data.DataFrame, pathID string) error {
	if dp.isClientSession {
		// For client sessions, validate using client write router
		if dp.clientWriteRouter == nil {
			return utils.NewKwikError(utils.ErrInvalidFrame, "client write router not initialized", nil)
		}
		return dp.clientWriteRouter.ValidateWriteOperation(frame, pathID)
	} else {
		// For server sessions, validate using server write router
		if dp.serverWriteRouter == nil {
			return utils.NewKwikError(utils.ErrInvalidFrame, "server write router not initialized", nil)
		}
		return dp.serverWriteRouter.ValidateWriteOperation(frame, pathID)
	}
}

// IsWriteAllowedOnPath checks if writes are allowed on a specific path
func (dp *DataPlaneImpl) IsWriteAllowedOnPath(pathID string) bool {
	if dp.isClientSession {
		if dp.clientWriteRouter == nil {
			return false
		}
		return dp.clientWriteRouter.IsWriteAllowedOnPath(pathID)
	} else {
		// For server sessions, writes are generally allowed on any active path
		dp.pathsMutex.RLock()
		stream, exists := dp.pathStreams[pathID]
		dp.pathsMutex.RUnlock()

		return exists && stream.IsActive()
	}
}

// GetPrimaryPathForWrites returns the primary path for write operations
func (dp *DataPlaneImpl) GetPrimaryPathForWrites() (string, error) {
	if dp.isClientSession {
		if dp.clientWriteRouter == nil {
			return "", utils.NewKwikError(utils.ErrInvalidFrame, "client write router not initialized", nil)
		}
		return dp.clientWriteRouter.GetPrimaryPath(), nil
	} else {
		if dp.serverWriteRouter == nil {
			return "", utils.NewKwikError(utils.ErrInvalidFrame, "server write router not initialized", nil)
		}
		// For servers, return the server's own path
		return dp.serverWriteRouter.serverPathID, nil
	}
}

// UpdatePrimaryPath updates the primary path for client sessions
func (dp *DataPlaneImpl) UpdatePrimaryPath(newPrimaryPathID string) error {
	if !dp.isClientSession {
		return utils.NewKwikError(utils.ErrInvalidFrame, "primary path can only be updated for client sessions", nil)
	}

	if dp.clientWriteRouter == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "client write router not initialized", nil)
	}

	return dp.clientWriteRouter.UpdatePrimaryPath(newPrimaryPathID)
}

// SendData sends a data frame to a specific path
func (dp *DataPlaneImpl) SendData(pathID string, frame *data.DataFrame) error {
	// Validate inputs
	if pathID == "" {
		return utils.NewKwikError(utils.ErrInvalidFrame, "path ID is empty", nil)
	}

	if frame == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "data frame is nil", nil)
	}

	// Get data stream for path
	dp.pathsMutex.RLock()
	stream, exists := dp.pathStreams[pathID]
	dp.pathsMutex.RUnlock()

	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}

	if !stream.IsActive() {
		return utils.NewPathDeadError(pathID)
	}

	// Validate frame
	if dp.frameProcessor != nil {
		err := dp.frameProcessor.ValidateFrame(frame)
		if err != nil {
			return utils.NewKwikError(utils.ErrInvalidFrame,
				"frame validation failed", err)
		}
	}

	// Check flow control
	if dp.flowController != nil {
		if dp.flowController.IsStreamBlocked(frame.LogicalStreamId) {
			return utils.NewKwikError(utils.ErrStreamCreationFailed,
				"stream is flow control blocked", nil)
		}

		if dp.flowController.IsConnectionBlocked(pathID) {
			return utils.NewKwikError(utils.ErrConnectionLost,
				"connection is flow control blocked", nil)
		}
	}

	// Process outgoing frame
	if dp.frameProcessor != nil {
		err := dp.frameProcessor.ProcessOutgoingFrame(frame)
		if err != nil {
			return utils.NewKwikError(utils.ErrInvalidFrame,
				"outgoing frame processing failed", err)
		}
	}

	// Set frame metadata
	frame.PathId = pathID
	frame.Timestamp = uint64(time.Now().UnixNano())
	if frame.FrameId == 0 {
		frame.FrameId = generateFrameID()
	}

	// Serialize frame
	frameData, err := proto.Marshal(frame)
	if err != nil {
		return utils.NewKwikError(utils.ErrSerializationFailed,
			"failed to serialize data frame", err)
	}

	// Check frame size
	if len(frameData) > int(dp.config.MaxFrameSize) {
		return utils.NewKwikError(utils.ErrPacketTooLarge,
			fmt.Sprintf("frame size %d exceeds maximum %d", len(frameData), dp.config.MaxFrameSize), nil)
	}

	// Send frame
	_, err = stream.Write(frameData)
	if err != nil {
		return utils.NewKwikError(utils.ErrConnectionLost,
			fmt.Sprintf("failed to send data frame to path %s", pathID), err)
	}

	// Update statistics
	dp.updateSendStats(pathID, frame)

	// Update flow control
	if dp.flowController != nil {
		dp.flowController.ConsumeStreamWindow(frame.LogicalStreamId, uint64(len(frame.Data)))
		dp.flowController.ConsumeConnectionWindow(pathID, uint64(len(frameData)))
	}

	return nil
}

// ReceiveData receives a data frame from a specific path
func (dp *DataPlaneImpl) ReceiveData(pathID string) (*data.DataFrame, error) {
	// Get data stream for path
	dp.pathsMutex.RLock()
	stream, exists := dp.pathStreams[pathID]
	dp.pathsMutex.RUnlock()

	if !exists {
		return nil, utils.NewPathNotFoundError(pathID)
	}

	if !stream.IsActive() {
		return nil, utils.NewPathDeadError(pathID)
	}

	// Read frame data
	buffer := make([]byte, dp.config.BufferSize)
	n, err := stream.Read(buffer)
	if err != nil {
		return nil, utils.NewKwikError(utils.ErrConnectionLost,
			fmt.Sprintf("failed to read data frame from path %s", pathID), err)
	}

	if n == 0 {
		return nil, utils.NewKwikError(utils.ErrInvalidFrame,
			"received empty data frame", nil)
	}

	// Deserialize frame
	var frame data.DataFrame
	err = proto.Unmarshal(buffer[:n], &frame)
	if err != nil {
		return nil, utils.NewKwikError(utils.ErrDeserializationFailed,
			"failed to deserialize data frame", err)
	}

	// Validate frame
	if dp.frameProcessor != nil {
		err = dp.frameProcessor.ValidateFrame(&frame)
		if err != nil {
			return nil, utils.NewKwikError(utils.ErrInvalidFrame,
				"received frame validation failed", err)
		}
	}

	// Process incoming frame
	if dp.frameProcessor != nil {
		err = dp.frameProcessor.ProcessIncomingFrame(&frame)
		if err != nil {
			return nil, utils.NewKwikError(utils.ErrInvalidFrame,
				"incoming frame processing failed", err)
		}
	}

	// Update statistics
	dp.updateReceiveStats(pathID, &frame)

	// Update flow control
	if dp.flowController != nil {
		dp.flowController.ExpandStreamWindow(frame.LogicalStreamId, uint64(len(frame.Data)))
		dp.flowController.ExpandConnectionWindow(pathID, uint64(n))
	}

	return &frame, nil
}

// CreateLogicalStream creates a new logical stream
func (dp *DataPlaneImpl) CreateLogicalStream(streamID uint64, pathID string) error {
	dp.streamsMutex.Lock()
	defer dp.streamsMutex.Unlock()

	// Check if stream already exists
	if _, exists := dp.logicalStreams[streamID]; exists {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			fmt.Sprintf("logical stream %d already exists", streamID), nil)
	}

	// Validate path exists
	dp.pathsMutex.RLock()
	_, pathExists := dp.pathStreams[pathID]
	dp.pathsMutex.RUnlock()

	if !pathExists {
		return utils.NewPathNotFoundError(pathID)
	}

	// Create logical stream state
	streamState := &LogicalStreamState{
		StreamID:      streamID,
		PathID:        pathID,
		State:         data.StreamStateType_DATA_STREAM_IDLE,
		BytesSent:     0,
		BytesReceived: 0,
		MaxStreamData: 65536, // Default window size
		FinSent:       false,
		FinReceived:   false,
		LastActivity:  time.Now(),
		Buffer:        make([]byte, 0, dp.config.BufferSize),
		Offset:        0,
	}

	dp.logicalStreams[streamID] = streamState

	// Initialize flow control for stream
	if dp.flowController != nil {
		dp.flowController.UpdateStreamWindow(streamID, streamState.MaxStreamData)
	}

	return nil
}

// CloseLogicalStream closes a logical stream
func (dp *DataPlaneImpl) CloseLogicalStream(streamID uint64) error {
	dp.streamsMutex.Lock()
	defer dp.streamsMutex.Unlock()

	streamState, exists := dp.logicalStreams[streamID]
	if !exists {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			fmt.Sprintf("logical stream %d not found", streamID), nil)
	}

	// Update stream state
	streamState.mutex.Lock()
	streamState.State = data.StreamStateType_DATA_STREAM_CLOSED
	streamState.LastActivity = time.Now()
	streamState.mutex.Unlock()

	// Remove from active streams
	delete(dp.logicalStreams, streamID)

	return nil
}

// GetStreamState returns the state of a logical stream
func (dp *DataPlaneImpl) GetStreamState(streamID uint64) (*data.StreamState, error) {
	dp.streamsMutex.RLock()
	streamState, exists := dp.logicalStreams[streamID]
	dp.streamsMutex.RUnlock()

	if !exists {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			fmt.Sprintf("logical stream %d not found", streamID), nil)
	}

	streamState.mutex.RLock()
	defer streamState.mutex.RUnlock()

	return &data.StreamState{
		LogicalStreamId:       streamState.StreamID,
		State:                 streamState.State,
		BytesSent:             streamState.BytesSent,
		BytesReceived:         streamState.BytesReceived,
		MaxStreamDataSent:     streamState.MaxStreamData,
		MaxStreamDataReceived: streamState.MaxStreamData,
		FinSent:               streamState.FinSent,
		FinReceived:           streamState.FinReceived,
	}, nil
}

// UpdateStreamFlowControl updates flow control for a stream
func (dp *DataPlaneImpl) UpdateStreamFlowControl(streamID uint64, maxData uint64) error {
	if dp.flowController == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "flow control not enabled", nil)
	}

	return dp.flowController.UpdateStreamWindow(streamID, maxData)
}

// UpdateConnectionFlowControl updates flow control for a connection
func (dp *DataPlaneImpl) UpdateConnectionFlowControl(pathID string, maxData uint64) error {
	if dp.flowController == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "flow control not enabled", nil)
	}

	return dp.flowController.UpdateConnectionWindow(pathID, maxData)
}

// SendRawPacket sends raw packet data to a specific path without interpretation
// This implements requirement 9.4: raw packets reach target server data planes without interpretation
// and requirement 9.5: maintains raw packet data integrity without modification
func (dp *DataPlaneImpl) SendRawPacket(pathID string, rawData []byte, protocolHint string, preserveOrder bool) error {
	// Validate inputs
	if pathID == "" {
		return utils.NewKwikError(utils.ErrInvalidFrame, "path ID is empty", nil)
	}

	if len(rawData) == 0 {
		return utils.NewKwikError(utils.ErrInvalidFrame, "raw packet data is empty", nil)
	}

	// Get data stream for path
	dp.pathsMutex.RLock()
	stream, exists := dp.pathStreams[pathID]
	dp.pathsMutex.RUnlock()

	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}

	if !stream.IsActive() {
		return utils.NewPathDeadError(pathID)
	}

	// Check raw packet size limits
	if len(rawData) > int(dp.config.MaxPacketSize) {
		return utils.NewKwikError(utils.ErrPacketTooLarge,
			fmt.Sprintf("raw packet size %d exceeds maximum %d", len(rawData), dp.config.MaxPacketSize), nil)
	}

	// Create a copy of raw data to ensure integrity preservation
	// This prevents any external modification of the data after this point
	rawDataCopy := make([]byte, len(rawData))
	copy(rawDataCopy, rawData)

	// Calculate integrity checksum for raw data validation
	checksum := calculateDataChecksum(rawDataCopy)

	// Create raw packet frame that preserves data integrity
	rawFrame := &RawPacketDataFrame{
		Data:          rawDataCopy, // Use the protected copy
		PathID:        pathID,
		ProtocolHint:  protocolHint,
		PreserveOrder: preserveOrder,
		Timestamp:     time.Now(),
		FrameID:       generateFrameID(),
		Checksum:      checksum, // Add integrity checksum
	}

	// Serialize raw packet frame
	frameData, err := rawFrame.Serialize()
	if err != nil {
		return utils.NewKwikError(utils.ErrSerializationFailed,
			"failed to serialize raw packet frame", err)
	}

	// Verify serialization integrity by deserializing and comparing
	verifyFrame := &RawPacketDataFrame{}
	err = verifyFrame.Deserialize(frameData)
	if err != nil {
		return utils.NewKwikError(utils.ErrSerializationFailed,
			"raw packet frame integrity verification failed during serialization", err)
	}

	// Verify data integrity after serialization/deserialization
	if !verifyRawDataIntegrity(rawDataCopy, verifyFrame.Data, checksum, verifyFrame.Checksum) {
		return utils.NewKwikError(utils.ErrSerializationFailed,
			"raw packet data integrity compromised during serialization", nil)
	}

	// Send raw packet directly to data plane stream without any interpretation
	// This ensures the raw packet reaches the target server's data plane unchanged
	_, err = stream.Write(frameData)
	if err != nil {
		return utils.NewKwikError(utils.ErrConnectionLost,
			fmt.Sprintf("failed to send raw packet to path %s", pathID), err)
	}

	// Update statistics for raw packet transmission
	dp.updateRawPacketSendStats(pathID, len(rawData))

	return nil
}

// ReceiveRawPacket receives and processes raw packet data from a path
// This ensures raw packets are handled without interpretation
func (dp *DataPlaneImpl) ReceiveRawPacket(pathID string) (*RawPacketDataFrame, error) {
	// Get data stream for path
	dp.pathsMutex.RLock()
	stream, exists := dp.pathStreams[pathID]
	dp.pathsMutex.RUnlock()

	if !exists {
		return nil, utils.NewPathNotFoundError(pathID)
	}

	if !stream.IsActive() {
		return nil, utils.NewPathDeadError(pathID)
	}

	// Read raw packet data
	buffer := make([]byte, dp.config.BufferSize)
	n, err := stream.Read(buffer)
	if err != nil {
		return nil, utils.NewKwikError(utils.ErrConnectionLost,
			fmt.Sprintf("failed to read raw packet from path %s", pathID), err)
	}

	if n == 0 {
		return nil, utils.NewKwikError(utils.ErrInvalidFrame,
			"received empty raw packet", nil)
	}

	// Try to deserialize as raw packet frame first
	rawFrame := &RawPacketDataFrame{}
	err = rawFrame.Deserialize(buffer[:n])
	if err != nil {
		// If deserialization fails, it might be a regular data frame
		// Return nil to indicate this is not a raw packet
		return nil, nil
	}

	// Update statistics for raw packet reception
	dp.updateRawPacketReceiveStats(pathID, len(rawFrame.Data))

	return rawFrame, nil
}

// IsRawPacketFrame checks if the received data is a raw packet frame
func (dp *DataPlaneImpl) IsRawPacketFrame(data []byte) bool {
	if len(data) < 4 {
		return false
	}

	// Try to deserialize as raw packet frame
	rawFrame := &RawPacketDataFrame{}
	err := rawFrame.Deserialize(data)
	return err == nil
}

// SendAck sends an ACK frame
func (dp *DataPlaneImpl) SendAck(pathID string, ackFrame *data.AckFrame) error {
	// Validate inputs
	if pathID == "" {
		return utils.NewKwikError(utils.ErrInvalidFrame, "path ID is empty", nil)
	}

	if ackFrame == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "ACK frame is nil", nil)
	}

	// Get data stream for path
	dp.pathsMutex.RLock()
	stream, exists := dp.pathStreams[pathID]
	dp.pathsMutex.RUnlock()

	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}

	// Set ACK metadata
	ackFrame.PathId = pathID
	ackFrame.Timestamp = uint64(time.Now().UnixNano())
	if ackFrame.AckId == 0 {
		ackFrame.AckId = generateFrameID()
	}

	// Serialize ACK frame
	ackData, err := proto.Marshal(ackFrame)
	if err != nil {
		return utils.NewKwikError(utils.ErrSerializationFailed,
			"failed to serialize ACK frame", err)
	}

	// Send ACK
	_, err = stream.Write(ackData)
	if err != nil {
		return utils.NewKwikError(utils.ErrConnectionLost,
			fmt.Sprintf("failed to send ACK frame to path %s", pathID), err)
	}

	return nil
}

// ProcessAck processes a received ACK frame
func (dp *DataPlaneImpl) ProcessAck(pathID string, ackFrame *data.AckFrame) error {
	// TODO: Implement ACK processing logic
	// This would typically:
	// 1. Update congestion control state
	// 2. Remove acknowledged packets from retransmission queue
	// 3. Update RTT estimates
	// 4. Adjust sending rate

	return nil
}

// EnableAggregation enables data aggregation for a stream
func (dp *DataPlaneImpl) EnableAggregation(streamID uint64) error {
	if dp.aggregator == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "aggregation not enabled", nil)
	}

	return dp.aggregator.CreateAggregatedStream(streamID)
}

// DisableAggregation disables data aggregation for a stream
func (dp *DataPlaneImpl) DisableAggregation(streamID uint64) error {
	if dp.aggregator == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "aggregation not enabled", nil)
	}

	return dp.aggregator.CloseAggregatedStream(streamID)
}

// GetAggregatedData gets aggregated data for a stream
func (dp *DataPlaneImpl) GetAggregatedData(streamID uint64) ([]byte, error) {
	if dp.aggregator == nil {
		return nil, utils.NewKwikError(utils.ErrInvalidFrame, "aggregation not enabled", nil)
	}

	return dp.aggregator.AggregateData(streamID)
}

// GetPathStats returns statistics for a specific path
func (dp *DataPlaneImpl) GetPathStats(pathID string) (*data.PathDataStats, error) {
	dp.statsMutex.RLock()
	defer dp.statsMutex.RUnlock()

	stats, exists := dp.stats.PathStats[pathID]
	if !exists {
		return nil, utils.NewPathNotFoundError(pathID)
	}

	// Return a copy to prevent modification
	return &data.PathDataStats{
		PathId:          stats.PathId,
		BytesSent:       stats.BytesSent,
		BytesReceived:   stats.BytesReceived,
		PacketsSent:     stats.PacketsSent,
		PacketsReceived: stats.PacketsReceived,
		Retransmissions: stats.Retransmissions,
		LossRate:        stats.LossRate,
		RttMs:           stats.RttMs,
	}, nil
}

// GetAggregatedStats returns aggregated statistics across all paths
func (dp *DataPlaneImpl) GetAggregatedStats() (*data.AggregatedDataStats, error) {
	dp.statsMutex.RLock()
	defer dp.statsMutex.RUnlock()

	// Create aggregated stats
	aggregatedStats := &data.AggregatedDataStats{
		TotalBytesSent:       dp.stats.TotalBytesSent,
		TotalBytesReceived:   dp.stats.TotalBytesReceived,
		TotalPacketsSent:     dp.stats.TotalFramesSent,
		TotalPacketsReceived: dp.stats.TotalFramesReceived,
		AggregationTimestamp: uint64(time.Now().UnixNano()),
	}

	// Copy path stats
	pathStats := make([]*data.PathDataStats, 0, len(dp.stats.PathStats))
	for _, stats := range dp.stats.PathStats {
		pathStats = append(pathStats, &data.PathDataStats{
			PathId:          stats.PathId,
			BytesSent:       stats.BytesSent,
			BytesReceived:   stats.BytesReceived,
			PacketsSent:     stats.PacketsSent,
			PacketsReceived: stats.PacketsReceived,
			Retransmissions: stats.Retransmissions,
			LossRate:        stats.LossRate,
			RttMs:           stats.RttMs,
		})
	}
	aggregatedStats.PathStats = pathStats

	return aggregatedStats, nil
}

// RegisterPath registers a data stream for a path
func (dp *DataPlaneImpl) RegisterPath(pathID string, stream DataStream) error {
	if pathID == "" {
		return utils.NewKwikError(utils.ErrInvalidFrame, "path ID is empty", nil)
	}

	if stream == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "stream is nil", nil)
	}

	dp.pathsMutex.Lock()
	defer dp.pathsMutex.Unlock()

	// Check if path already registered
	if _, exists := dp.pathStreams[pathID]; exists {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			fmt.Sprintf("path %s already registered", pathID), nil)
	}

	dp.pathStreams[pathID] = stream

	// Initialize path statistics
	dp.statsMutex.Lock()
	dp.stats.PathStats[pathID] = &data.PathDataStats{
		PathId:          pathID,
		BytesSent:       0,
		BytesReceived:   0,
		PacketsSent:     0,
		PacketsReceived: 0,
		Retransmissions: 0,
		LossRate:        0.0,
		RttMs:           0,
	}
	dp.statsMutex.Unlock()

	// Add path to aggregator if enabled
	if dp.aggregator != nil {
		dp.aggregator.AddPath(pathID, stream)
	}

	// Add path to scheduler
	if dp.scheduler != nil {
		metrics := &PathMetrics{
			PathID:         pathID,
			RTT:            0,
			Bandwidth:      1000000, // Default 1 Mbps
			PacketLoss:     0.0,
			Congestion:     0.0,
			LastUpdate:     time.Now().UnixNano(),
			IsActive:       true,
			QueueDepth:     0,
			ThroughputMbps: 1.0,
		}
		dp.scheduler.AddPath(pathID, metrics)
	}

	return nil
}

// UnregisterPath unregisters a data stream for a path
func (dp *DataPlaneImpl) UnregisterPath(pathID string) error {
	dp.pathsMutex.Lock()
	defer dp.pathsMutex.Unlock()

	stream, exists := dp.pathStreams[pathID]
	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}

	// Close the stream
	stream.Close()

	// Remove from map
	delete(dp.pathStreams, pathID)

	// Remove from aggregator if enabled
	if dp.aggregator != nil {
		dp.aggregator.RemovePath(pathID)
	}

	// Remove from scheduler
	if dp.scheduler != nil {
		dp.scheduler.RemovePath(pathID)
	}

	return nil
}

// Close closes the data plane and all its resources
func (dp *DataPlaneImpl) Close() error {
	dp.cancel()

	// Close all path streams
	dp.pathsMutex.Lock()
	for pathID, stream := range dp.pathStreams {
		stream.Close()
		delete(dp.pathStreams, pathID)
	}
	dp.pathsMutex.Unlock()

	// Close all logical streams
	dp.streamsMutex.Lock()
	for streamID := range dp.logicalStreams {
		delete(dp.logicalStreams, streamID)
	}
	dp.streamsMutex.Unlock()

	// Wait for all workers to finish
	dp.wg.Wait()

	return nil
}

// Helper methods

// updateSendStats updates statistics for sent data
func (dp *DataPlaneImpl) updateSendStats(pathID string, frame *data.DataFrame) {
	dp.statsMutex.Lock()
	defer dp.statsMutex.Unlock()

	// Update global stats
	dp.stats.TotalBytesSent += uint64(len(frame.Data))
	dp.stats.TotalFramesSent++

	// Update path stats
	if pathStats, exists := dp.stats.PathStats[pathID]; exists {
		pathStats.BytesSent += uint64(len(frame.Data))
		pathStats.PacketsSent++
	}

	// Update logical stream stats
	dp.streamsMutex.RLock()
	if streamState, exists := dp.logicalStreams[frame.LogicalStreamId]; exists {
		streamState.mutex.Lock()
		streamState.BytesSent += uint64(len(frame.Data))
		streamState.LastActivity = time.Now()
		streamState.mutex.Unlock()
	}
	dp.streamsMutex.RUnlock()
}

// updateReceiveStats updates statistics for received data
func (dp *DataPlaneImpl) updateReceiveStats(pathID string, frame *data.DataFrame) {
	dp.statsMutex.Lock()
	defer dp.statsMutex.Unlock()

	// Update global stats
	dp.stats.TotalBytesReceived += uint64(len(frame.Data))
	dp.stats.TotalFramesReceived++

	// Update path stats
	if pathStats, exists := dp.stats.PathStats[pathID]; exists {
		pathStats.BytesReceived += uint64(len(frame.Data))
		pathStats.PacketsReceived++
	}

	// Update logical stream stats
	dp.streamsMutex.RLock()
	if streamState, exists := dp.logicalStreams[frame.LogicalStreamId]; exists {
		streamState.mutex.Lock()
		streamState.BytesReceived += uint64(len(frame.Data))
		streamState.LastActivity = time.Now()
		streamState.mutex.Unlock()
	}
	dp.streamsMutex.RUnlock()
}

// dataProcessingWorker processes data in background
func (dp *DataPlaneImpl) dataProcessingWorker(workerID int) {
	defer dp.wg.Done()

	for {
		select {
		case <-dp.ctx.Done():
			return
		default:
			// Worker processes data as needed
			// This is a placeholder for actual data processing queue
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// updateRawPacketSendStats updates statistics for sent raw packets
func (dp *DataPlaneImpl) updateRawPacketSendStats(pathID string, dataSize int) {
	dp.statsMutex.Lock()
	defer dp.statsMutex.Unlock()

	// Update global stats
	dp.stats.TotalBytesSent += uint64(dataSize)
	dp.stats.TotalFramesSent++

	// Update path stats
	if pathStats, exists := dp.stats.PathStats[pathID]; exists {
		pathStats.BytesSent += uint64(dataSize)
		pathStats.PacketsSent++
	}
}

// updateRawPacketReceiveStats updates statistics for received raw packets
func (dp *DataPlaneImpl) updateRawPacketReceiveStats(pathID string, dataSize int) {
	dp.statsMutex.Lock()
	defer dp.statsMutex.Unlock()

	// Update global stats
	dp.stats.TotalBytesReceived += uint64(dataSize)
	dp.stats.TotalFramesReceived++

	// Update path stats
	if pathStats, exists := dp.stats.PathStats[pathID]; exists {
		pathStats.BytesReceived += uint64(dataSize)
		pathStats.PacketsReceived++
	}
}

// calculateDataChecksum calculates a simple checksum for data integrity verification
// This implements requirement 9.5: ensure no modification of raw packet contents
func calculateDataChecksum(data []byte) uint32 {
	var checksum uint32
	for i, b := range data {
		// Simple checksum algorithm that includes position to detect reordering
		checksum ^= uint32(b) << (uint32(i%4) * 8)
		checksum = (checksum << 1) | (checksum >> 31) // Rotate left by 1
	}
	return checksum
}

// verifyRawDataIntegrity verifies that raw packet data has not been modified
// This implements requirement 9.5: ensure no modification or interpretation of raw packet contents
func verifyRawDataIntegrity(originalData, receivedData []byte, originalChecksum, receivedChecksum uint32) bool {
	// Check length first
	if len(originalData) != len(receivedData) {
		return false
	}

	// Check checksum
	if originalChecksum != receivedChecksum {
		return false
	}

	// Byte-by-byte comparison to ensure exact match
	for i := 0; i < len(originalData); i++ {
		if originalData[i] != receivedData[i] {
			return false
		}
	}

	// Verify checksum calculation is consistent
	calculatedChecksum := calculateDataChecksum(receivedData)
	if calculatedChecksum != receivedChecksum {
		return false
	}

	return true
}

// RawPacketDataFrame represents a frame containing raw packet data for data plane transmission
// This frame preserves raw packet data integrity without interpretation
type RawPacketDataFrame struct {
	Data          []byte
	PathID        string
	ProtocolHint  string
	PreserveOrder bool
	Timestamp     time.Time
	FrameID       uint64
	Checksum      uint32 // Integrity checksum for data validation
}

// Serialize serializes the raw packet data frame to bytes
func (rpdf *RawPacketDataFrame) Serialize() ([]byte, error) {
	// Enhanced serialization format that preserves data integrity:
	// [FrameID:8][DataLen:4][Data:N][PathIDLen:2][PathID:N][ProtocolHintLen:2][ProtocolHint:N][PreserveOrder:1][Timestamp:8][Checksum:4]

	pathIDBytes := []byte(rpdf.PathID)
	protocolHintBytes := []byte(rpdf.ProtocolHint)

	result := make([]byte, 0, 8+4+len(rpdf.Data)+2+len(pathIDBytes)+2+len(protocolHintBytes)+1+8+4)

	// FrameID (8 bytes)
	for i := 0; i < 8; i++ {
		result = append(result, byte(rpdf.FrameID>>(8*(7-i))))
	}

	// DataLen (4 bytes)
	dataLen := uint32(len(rpdf.Data))
	for i := 0; i < 4; i++ {
		result = append(result, byte(dataLen>>(8*(3-i))))
	}

	// Data (preserve raw data exactly as-is)
	result = append(result, rpdf.Data...)

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
	if rpdf.PreserveOrder {
		result = append(result, 1)
	} else {
		result = append(result, 0)
	}

	// Timestamp (8 bytes - Unix nano)
	timestamp := uint64(rpdf.Timestamp.UnixNano())
	for i := 0; i < 8; i++ {
		result = append(result, byte(timestamp>>(8*(7-i))))
	}

	// Checksum (4 bytes) - for integrity verification
	for i := 0; i < 4; i++ {
		result = append(result, byte(rpdf.Checksum>>(8*(3-i))))
	}

	return result, nil
}

// Deserialize deserializes bytes into the raw packet data frame
func (rpdf *RawPacketDataFrame) Deserialize(data []byte) error {
	if len(data) < 29 { // Minimum size: 8+4+0+2+0+2+0+1+8+4
		return fmt.Errorf("raw packet data frame too short: %d bytes", len(data))
	}

	offset := 0

	// FrameID (8 bytes)
	rpdf.FrameID = 0
	for i := 0; i < 8; i++ {
		rpdf.FrameID = (rpdf.FrameID << 8) | uint64(data[offset+i])
	}
	offset += 8

	// DataLen (4 bytes)
	dataLen := uint32(0)
	for i := 0; i < 4; i++ {
		dataLen = (dataLen << 8) | uint32(data[offset+i])
	}
	offset += 4

	// Check if we have enough data
	if len(data) < offset+int(dataLen) {
		return fmt.Errorf("raw packet data frame incomplete: missing data")
	}

	// Data (preserve raw data exactly as-is)
	rpdf.Data = make([]byte, dataLen)
	copy(rpdf.Data, data[offset:offset+int(dataLen)])
	offset += int(dataLen)

	// PathIDLen (2 bytes)
	if len(data) < offset+2 {
		return fmt.Errorf("raw packet data frame incomplete: missing PathID length")
	}
	pathIDLen := uint16(data[offset])<<8 | uint16(data[offset+1])
	offset += 2

	// PathID
	if len(data) < offset+int(pathIDLen) {
		return fmt.Errorf("raw packet data frame incomplete: missing PathID data")
	}
	rpdf.PathID = string(data[offset : offset+int(pathIDLen)])
	offset += int(pathIDLen)

	// ProtocolHintLen (2 bytes)
	if len(data) < offset+2 {
		return fmt.Errorf("raw packet data frame incomplete: missing ProtocolHint length")
	}
	protocolHintLen := uint16(data[offset])<<8 | uint16(data[offset+1])
	offset += 2

	// ProtocolHint
	if len(data) < offset+int(protocolHintLen) {
		return fmt.Errorf("raw packet data frame incomplete: missing ProtocolHint data")
	}
	rpdf.ProtocolHint = string(data[offset : offset+int(protocolHintLen)])
	offset += int(protocolHintLen)

	// PreserveOrder (1 byte)
	if len(data) < offset+1 {
		return fmt.Errorf("raw packet data frame incomplete: missing PreserveOrder flag")
	}
	rpdf.PreserveOrder = data[offset] == 1
	offset += 1

	// Timestamp (8 bytes)
	if len(data) < offset+8 {
		return fmt.Errorf("raw packet data frame incomplete: missing Timestamp")
	}
	timestamp := uint64(0)
	for i := 0; i < 8; i++ {
		timestamp = (timestamp << 8) | uint64(data[offset+i])
	}
	rpdf.Timestamp = time.Unix(0, int64(timestamp))
	offset += 8

	// Checksum (4 bytes)
	if len(data) < offset+4 {
		return fmt.Errorf("raw packet data frame incomplete: missing Checksum")
	}
	rpdf.Checksum = 0
	for i := 0; i < 4; i++ {
		rpdf.Checksum = (rpdf.Checksum << 8) | uint32(data[offset+i])
	}

	// Verify data integrity after deserialization
	calculatedChecksum := calculateDataChecksum(rpdf.Data)
	if calculatedChecksum != rpdf.Checksum {
		return fmt.Errorf("raw packet data integrity check failed: checksum mismatch")
	}

	return nil
}

// ValidateIntegrity validates the integrity of the raw packet data
// This implements requirement 9.5: ensure no modification of raw packet contents
func (rpdf *RawPacketDataFrame) ValidateIntegrity() error {
	if len(rpdf.Data) == 0 {
		return fmt.Errorf("raw packet data is empty")
	}

	// Recalculate checksum and compare
	calculatedChecksum := calculateDataChecksum(rpdf.Data)
	if calculatedChecksum != rpdf.Checksum {
		return fmt.Errorf("raw packet data integrity compromised: checksum mismatch")
	}

	return nil
}

// GetRawData returns a copy of the raw data to prevent external modification
// This implements requirement 9.5: ensure no modification of raw packet contents
func (rpdf *RawPacketDataFrame) GetRawData() []byte {
	// Return a copy to prevent external modification
	dataCopy := make([]byte, len(rpdf.Data))
	copy(dataCopy, rpdf.Data)
	return dataCopy
}

// generateFrameID generates a unique frame identifier
func generateFrameID() uint64 {
	return uint64(time.Now().UnixNano())
}
