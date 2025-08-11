package stream

import (
	"context"
	"fmt"
	"sync"
	"time"

	"kwik/internal/utils"
	datapb "kwik/proto/data"
)

// StreamIsolationManager ensures strict isolation between logical streams
// despite sharing real QUIC streams. Implements Requirement 10.1
type StreamIsolationManager struct {
	// Stream isolation tracking
	isolatedStreams map[uint64]*IsolatedStreamContext
	streamMutex     sync.RWMutex

	// Real stream to logical stream mapping
	realToLogicalMap map[uint64]map[uint64]bool // realStreamID -> set of logicalStreamIDs
	realStreamMutex  sync.RWMutex

	// Frame routing and buffering
	frameRouter     *IsolationFrameRouter
	bufferManager   *StreamBufferManager

	// Configuration
	config *IsolationConfig

	// Context for cancellation
	ctx    context.Context
	cancel context.CancelFunc
}

// IsolatedStreamContext maintains isolation context for a logical stream
type IsolatedStreamContext struct {
	LogicalStreamID uint64
	RealStreamID    uint64
	PathID          string

	// Isolation boundaries
	readBuffer      *IsolationBuffer
	writeBuffer     *IsolationBuffer
	
	// Stream state
	state           IsolatedStreamState
	lastActivity    time.Time
	bytesRead       uint64
	bytesWritten    uint64

	// Flow control isolation
	readWindow      uint64
	writeWindow     uint64
	windowMutex     sync.RWMutex

	// Synchronization
	mutex           sync.RWMutex
}

// IsolatedStreamState represents the state of an isolated stream
type IsolatedStreamState int

const (
	IsolatedStreamStateActive IsolatedStreamState = iota
	IsolatedStreamStateReading
	IsolatedStreamStateWriting
	IsolatedStreamStateBlocked
	IsolatedStreamStateClosed
)

// IsolationBuffer provides isolated buffering for stream data
type IsolationBuffer struct {
	data        []byte
	capacity    int
	readOffset  uint64
	writeOffset uint64
	mutex       sync.RWMutex
}

// IsolationConfig contains configuration for stream isolation
type IsolationConfig struct {
	// Buffer sizes per stream
	ReadBufferSize     int
	WriteBufferSize    int
	MaxBufferSize      int

	// Isolation parameters
	MaxStreamsPerReal  int
	IsolationTimeout   time.Duration
	BufferFlushDelay   time.Duration

	// Flow control
	InitialWindowSize  uint64
	MaxWindowSize      uint64
	WindowUpdateRatio  float64
}

// IsolationFrameRouter routes frames to the correct isolated stream
type IsolationFrameRouter struct {
	routingTable map[uint64]uint64 // logicalStreamID -> realStreamID
	mutex        sync.RWMutex
}

// StreamBufferManager manages buffers for isolated streams
type StreamBufferManager struct {
	buffers map[uint64]*IsolationBuffer
	mutex   sync.RWMutex
	config  *IsolationConfig
}

// NewStreamIsolationManager creates a new stream isolation manager
func NewStreamIsolationManager(config *IsolationConfig) *StreamIsolationManager {
	if config == nil {
		config = DefaultIsolationConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &StreamIsolationManager{
		isolatedStreams:  make(map[uint64]*IsolatedStreamContext),
		realToLogicalMap: make(map[uint64]map[uint64]bool),
		frameRouter:      NewIsolationFrameRouter(),
		bufferManager:    NewStreamBufferManager(config),
		config:           config,
		ctx:              ctx,
		cancel:           cancel,
	}
}

// DefaultIsolationConfig returns default isolation configuration
func DefaultIsolationConfig() *IsolationConfig {
	return &IsolationConfig{
		ReadBufferSize:     utils.DefaultReadBufferSize,
		WriteBufferSize:    utils.DefaultWriteBufferSize,
		MaxBufferSize:      utils.DefaultReadBufferSize * 4,
		MaxStreamsPerReal:  4, // Optimal ratio from requirements
		IsolationTimeout:   30 * time.Second,
		BufferFlushDelay:   10 * time.Millisecond,
		InitialWindowSize:  65536, // 64KB
		MaxWindowSize:      1048576, // 1MB
		WindowUpdateRatio:  0.5,
	}
}

// CreateIsolatedStream creates a new isolated stream context
// Implements strict isolation between logical streams
func (sim *StreamIsolationManager) CreateIsolatedStream(logicalStreamID, realStreamID uint64, pathID string) (*IsolatedStreamContext, error) {
	sim.streamMutex.Lock()
	defer sim.streamMutex.Unlock()

	// Check if stream already exists
	if _, exists := sim.isolatedStreams[logicalStreamID]; exists {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"isolated stream already exists", nil)
	}

	// Validate real stream capacity
	err := sim.validateRealStreamCapacity(realStreamID)
	if err != nil {
		return nil, err
	}

	// Create isolation buffers
	readBuffer := sim.bufferManager.CreateBuffer(logicalStreamID, sim.config.ReadBufferSize)
	writeBuffer := sim.bufferManager.CreateBuffer(logicalStreamID+1000000, sim.config.WriteBufferSize) // Offset to avoid collision

	// Create isolated stream context
	isolatedStream := &IsolatedStreamContext{
		LogicalStreamID: logicalStreamID,
		RealStreamID:    realStreamID,
		PathID:          pathID,
		readBuffer:      readBuffer,
		writeBuffer:     writeBuffer,
		state:           IsolatedStreamStateActive,
		lastActivity:    time.Now(),
		bytesRead:       0,
		bytesWritten:    0,
		readWindow:      sim.config.InitialWindowSize,
		writeWindow:     sim.config.InitialWindowSize,
	}

	// Store isolated stream
	sim.isolatedStreams[logicalStreamID] = isolatedStream

	// Update real stream mapping
	sim.updateRealStreamMapping(realStreamID, logicalStreamID, true)

	// Register with frame router
	sim.frameRouter.RegisterStream(logicalStreamID, realStreamID)

	return isolatedStream, nil
}

// validateRealStreamCapacity checks if a real stream can accept another logical stream
func (sim *StreamIsolationManager) validateRealStreamCapacity(realStreamID uint64) error {
	sim.realStreamMutex.RLock()
	defer sim.realStreamMutex.RUnlock()

	logicalStreams, exists := sim.realToLogicalMap[realStreamID]
	if !exists {
		return nil // New real stream, capacity is fine
	}

	if len(logicalStreams) >= sim.config.MaxStreamsPerReal {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			fmt.Sprintf("real stream %d has reached maximum capacity of %d logical streams",
				realStreamID, sim.config.MaxStreamsPerReal), nil)
	}

	return nil
}

// updateRealStreamMapping updates the mapping between real and logical streams
func (sim *StreamIsolationManager) updateRealStreamMapping(realStreamID, logicalStreamID uint64, add bool) {
	sim.realStreamMutex.Lock()
	defer sim.realStreamMutex.Unlock()

	if add {
		if _, exists := sim.realToLogicalMap[realStreamID]; !exists {
			sim.realToLogicalMap[realStreamID] = make(map[uint64]bool)
		}
		sim.realToLogicalMap[realStreamID][logicalStreamID] = true
	} else {
		if logicalStreams, exists := sim.realToLogicalMap[realStreamID]; exists {
			delete(logicalStreams, logicalStreamID)
			if len(logicalStreams) == 0 {
				delete(sim.realToLogicalMap, realStreamID)
			}
		}
	}
}

// ReadFromIsolatedStream reads data from an isolated stream
// Maintains strict isolation by only returning data for the specific logical stream
func (sim *StreamIsolationManager) ReadFromIsolatedStream(logicalStreamID uint64, buffer []byte) (int, error) {
	sim.streamMutex.RLock()
	isolatedStream, exists := sim.isolatedStreams[logicalStreamID]
	sim.streamMutex.RUnlock()

	if !exists {
		return 0, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"isolated stream not found", nil)
	}

	isolatedStream.mutex.Lock()
	defer isolatedStream.mutex.Unlock()

	// Update state
	isolatedStream.state = IsolatedStreamStateReading
	isolatedStream.lastActivity = time.Now()

	// Read from isolated buffer
	n, err := isolatedStream.readBuffer.Read(buffer)
	if err != nil {
		return 0, err
	}

	// Update statistics
	isolatedStream.bytesRead += uint64(n)

	// Update flow control window
	isolatedStream.windowMutex.Lock()
	isolatedStream.readWindow -= uint64(n)
	isolatedStream.windowMutex.Unlock()

	// Check if window update is needed
	if float64(isolatedStream.readWindow) < float64(sim.config.InitialWindowSize)*sim.config.WindowUpdateRatio {
		sim.updateFlowControlWindow(isolatedStream)
	}

	// Reset state
	isolatedStream.state = IsolatedStreamStateActive

	return n, nil
}

// WriteToIsolatedStream writes data to an isolated stream
// Maintains strict isolation by buffering data separately for each logical stream
func (sim *StreamIsolationManager) WriteToIsolatedStream(logicalStreamID uint64, data []byte) (int, error) {
	sim.streamMutex.RLock()
	isolatedStream, exists := sim.isolatedStreams[logicalStreamID]
	sim.streamMutex.RUnlock()

	if !exists {
		return 0, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"isolated stream not found", nil)
	}

	isolatedStream.mutex.Lock()
	defer isolatedStream.mutex.Unlock()

	// Check flow control window
	isolatedStream.windowMutex.RLock()
	availableWindow := isolatedStream.writeWindow
	isolatedStream.windowMutex.RUnlock()

	if uint64(len(data)) > availableWindow {
		isolatedStream.state = IsolatedStreamStateBlocked
		return 0, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"write blocked by flow control", nil)
	}

	// Update state
	isolatedStream.state = IsolatedStreamStateWriting
	isolatedStream.lastActivity = time.Now()

	// Write to isolated buffer
	n, err := isolatedStream.writeBuffer.Write(data)
	if err != nil {
		return 0, err
	}

	// Update statistics
	isolatedStream.bytesWritten += uint64(n)

	// Update flow control window
	isolatedStream.windowMutex.Lock()
	isolatedStream.writeWindow -= uint64(n)
	isolatedStream.windowMutex.Unlock()

	// Reset state
	isolatedStream.state = IsolatedStreamStateActive

	// Schedule buffer flush
	go sim.scheduleBufferFlush(isolatedStream)

	return n, nil
}

// ProcessIncomingFrame processes an incoming frame and routes it to the correct isolated stream
// Maintains stream boundaries by ensuring frames only reach their intended logical stream
func (sim *StreamIsolationManager) ProcessIncomingFrame(frame *datapb.DataFrame) error {
	if frame == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "frame is nil", nil)
	}

	// Route frame to correct isolated stream
	logicalStreamID := frame.LogicalStreamId
	
	sim.streamMutex.RLock()
	isolatedStream, exists := sim.isolatedStreams[logicalStreamID]
	sim.streamMutex.RUnlock()

	if !exists {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			fmt.Sprintf("isolated stream %d not found for incoming frame", logicalStreamID), nil)
	}

	// Verify frame belongs to the correct real stream (isolation check)
	if isolatedStream.RealStreamID != 0 && frame.PathId != isolatedStream.PathID {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			"frame path ID does not match isolated stream path", nil)
	}

	// Write frame data to isolated read buffer
	isolatedStream.mutex.Lock()
	defer isolatedStream.mutex.Unlock()

	_, err := isolatedStream.readBuffer.WriteAtOffset(frame.Data, frame.Offset)
	if err != nil {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			"failed to write frame data to isolated buffer", err)
	}

	// Update activity
	isolatedStream.lastActivity = time.Now()

	return nil
}

// updateFlowControlWindow updates the flow control window for an isolated stream
func (sim *StreamIsolationManager) updateFlowControlWindow(isolatedStream *IsolatedStreamContext) {
	isolatedStream.windowMutex.Lock()
	defer isolatedStream.windowMutex.Unlock()

	// Expand window back to initial size
	isolatedStream.readWindow = sim.config.InitialWindowSize
}

// scheduleBufferFlush schedules a buffer flush for an isolated stream
func (sim *StreamIsolationManager) scheduleBufferFlush(isolatedStream *IsolatedStreamContext) {
	time.Sleep(sim.config.BufferFlushDelay)
	
	// Flush write buffer to real stream
	// This would be implemented to actually send data via the real QUIC stream
	// For now, we just mark the buffer as flushed
	isolatedStream.writeBuffer.Flush()
}

// CloseIsolatedStream closes an isolated stream and cleans up resources
func (sim *StreamIsolationManager) CloseIsolatedStream(logicalStreamID uint64) error {
	sim.streamMutex.Lock()
	defer sim.streamMutex.Unlock()

	isolatedStream, exists := sim.isolatedStreams[logicalStreamID]
	if !exists {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			"isolated stream not found", nil)
	}

	// Update state
	isolatedStream.mutex.Lock()
	isolatedStream.state = IsolatedStreamStateClosed
	isolatedStream.mutex.Unlock()

	// Clean up buffers
	sim.bufferManager.ReleaseBuffer(logicalStreamID)
	sim.bufferManager.ReleaseBuffer(logicalStreamID + 1000000) // Write buffer

	// Update real stream mapping
	sim.updateRealStreamMapping(isolatedStream.RealStreamID, logicalStreamID, false)

	// Unregister from frame router
	sim.frameRouter.UnregisterStream(logicalStreamID)

	// Remove from isolated streams
	delete(sim.isolatedStreams, logicalStreamID)

	return nil
}

// GetIsolationStats returns statistics about stream isolation
func (sim *StreamIsolationManager) GetIsolationStats() *IsolationStats {
	sim.streamMutex.RLock()
	defer sim.streamMutex.RUnlock()

	stats := &IsolationStats{
		TotalIsolatedStreams: len(sim.isolatedStreams),
		ActiveStreams:        0,
		BlockedStreams:       0,
		RealStreamUtilization: make(map[uint64]int),
	}

	// Count active and blocked streams
	for _, isolatedStream := range sim.isolatedStreams {
		isolatedStream.mutex.RLock()
		switch isolatedStream.state {
		case IsolatedStreamStateActive, IsolatedStreamStateReading, IsolatedStreamStateWriting:
			stats.ActiveStreams++
		case IsolatedStreamStateBlocked:
			stats.BlockedStreams++
		}
		isolatedStream.mutex.RUnlock()
	}

	// Calculate real stream utilization
	sim.realStreamMutex.RLock()
	for realStreamID, logicalStreams := range sim.realToLogicalMap {
		stats.RealStreamUtilization[realStreamID] = len(logicalStreams)
	}
	sim.realStreamMutex.RUnlock()

	return stats
}

// IsolationStats contains statistics about stream isolation
type IsolationStats struct {
	TotalIsolatedStreams  int
	ActiveStreams         int
	BlockedStreams        int
	RealStreamUtilization map[uint64]int
}

// Close shuts down the stream isolation manager
func (sim *StreamIsolationManager) Close() error {
	sim.cancel()

	sim.streamMutex.Lock()
	defer sim.streamMutex.Unlock()

	// Close all isolated streams
	for logicalStreamID := range sim.isolatedStreams {
		// Note: We don't call CloseIsolatedStream here to avoid deadlock
		sim.bufferManager.ReleaseBuffer(logicalStreamID)
		sim.bufferManager.ReleaseBuffer(logicalStreamID + 1000000)
	}

	// Clear all maps
	sim.isolatedStreams = make(map[uint64]*IsolatedStreamContext)
	sim.realToLogicalMap = make(map[uint64]map[uint64]bool)

	return nil
}
// NewIsolationFrameRouter creates a new isolation frame router
func NewIsolationFrameRouter() *IsolationFrameRouter {
	return &IsolationFrameRouter{
		routingTable: make(map[uint64]uint64),
	}
}

// RegisterStream registers a logical stream with its real stream
func (ifr *IsolationFrameRouter) RegisterStream(logicalStreamID, realStreamID uint64) {
	ifr.mutex.Lock()
	defer ifr.mutex.Unlock()
	ifr.routingTable[logicalStreamID] = realStreamID
}

// UnregisterStream unregisters a logical stream
func (ifr *IsolationFrameRouter) UnregisterStream(logicalStreamID uint64) {
	ifr.mutex.Lock()
	defer ifr.mutex.Unlock()
	delete(ifr.routingTable, logicalStreamID)
}

// GetRealStreamID returns the real stream ID for a logical stream
func (ifr *IsolationFrameRouter) GetRealStreamID(logicalStreamID uint64) (uint64, bool) {
	ifr.mutex.RLock()
	defer ifr.mutex.RUnlock()
	realStreamID, exists := ifr.routingTable[logicalStreamID]
	return realStreamID, exists
}

// NewStreamBufferManager creates a new stream buffer manager
func NewStreamBufferManager(config *IsolationConfig) *StreamBufferManager {
	return &StreamBufferManager{
		buffers: make(map[uint64]*IsolationBuffer),
		config:  config,
	}
}

// CreateBuffer creates a new isolation buffer
func (sbm *StreamBufferManager) CreateBuffer(bufferID uint64, size int) *IsolationBuffer {
	sbm.mutex.Lock()
	defer sbm.mutex.Unlock()

	buffer := &IsolationBuffer{
		data:        make([]byte, 0, size),
		capacity:    size,
		readOffset:  0,
		writeOffset: 0,
	}

	sbm.buffers[bufferID] = buffer
	return buffer
}

// ReleaseBuffer releases an isolation buffer
func (sbm *StreamBufferManager) ReleaseBuffer(bufferID uint64) {
	sbm.mutex.Lock()
	defer sbm.mutex.Unlock()
	delete(sbm.buffers, bufferID)
}

// Read reads data from the isolation buffer
func (ib *IsolationBuffer) Read(buffer []byte) (int, error) {
	ib.mutex.Lock()
	defer ib.mutex.Unlock()

	available := len(ib.data) - int(ib.readOffset)
	if available == 0 {
		return 0, nil // No data available
	}

	n := len(buffer)
	if n > available {
		n = available
	}

	copy(buffer[:n], ib.data[ib.readOffset:ib.readOffset+uint64(n)])
	ib.readOffset += uint64(n)

	// Compact buffer if read offset is significant
	if ib.readOffset > uint64(len(ib.data))/2 {
		copy(ib.data, ib.data[ib.readOffset:])
		ib.data = ib.data[:len(ib.data)-int(ib.readOffset)]
		ib.writeOffset -= ib.readOffset
		ib.readOffset = 0
	}

	return n, nil
}

// Write writes data to the isolation buffer
func (ib *IsolationBuffer) Write(data []byte) (int, error) {
	ib.mutex.Lock()
	defer ib.mutex.Unlock()

	if len(ib.data)+len(data) > ib.capacity {
		return 0, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"buffer capacity exceeded", nil)
	}

	ib.data = append(ib.data, data...)
	ib.writeOffset += uint64(len(data))

	return len(data), nil
}

// WriteAtOffset writes data at a specific offset in the buffer
func (ib *IsolationBuffer) WriteAtOffset(data []byte, offset uint64) (int, error) {
	ib.mutex.Lock()
	defer ib.mutex.Unlock()

	// Ensure buffer is large enough
	requiredSize := int(offset) + len(data)
	if requiredSize > ib.capacity {
		return 0, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"buffer capacity exceeded", nil)
	}

	// Extend buffer if necessary
	if requiredSize > len(ib.data) {
		newData := make([]byte, requiredSize)
		copy(newData, ib.data)
		ib.data = newData
	}

	// Write data at offset
	copy(ib.data[offset:], data)
	
	// Update write offset if we wrote beyond current end
	if offset+uint64(len(data)) > ib.writeOffset {
		ib.writeOffset = offset + uint64(len(data))
	}

	return len(data), nil
}

// Flush flushes the buffer (placeholder for actual implementation)
func (ib *IsolationBuffer) Flush() error {
	ib.mutex.Lock()
	defer ib.mutex.Unlock()
	
	// In a real implementation, this would flush data to the real QUIC stream
	// For now, we just clear the buffer
	ib.data = ib.data[:0]
	ib.readOffset = 0
	ib.writeOffset = 0
	
	return nil
}

// String returns string representation of isolated stream state
func (state IsolatedStreamState) String() string {
	switch state {
	case IsolatedStreamStateActive:
		return "ACTIVE"
	case IsolatedStreamStateReading:
		return "READING"
	case IsolatedStreamStateWriting:
		return "WRITING"
	case IsolatedStreamStateBlocked:
		return "BLOCKED"
	case IsolatedStreamStateClosed:
		return "CLOSED"
	default:
		return "UNKNOWN"
	}
}