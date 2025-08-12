package stream

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"kwik/pkg/control"
	"kwik/pkg/protocol"
	"kwik/internal/utils"
)

// LogicalStreamManager manages logical KWIK streams without requiring new QUIC streams
// Implements Requirements 7.1, 7.2 and secondary stream isolation (Requirements 5.4, 5.5)
type LogicalStreamManager struct {
	// Stream management
	streams       map[uint64]*LogicalStreamInfo
	streamCounter uint64
	frameCounter  uint64
	mutex         sync.RWMutex

	// Secondary stream isolation (Requirements 5.4, 5.5)
	isolator           SecondaryStreamIsolator
	mappingManager     *StreamMappingManager
	secondaryMappings  map[uint64][]uint64 // kwikStreamID -> []secondaryStreamID

	// Control plane for notifications
	controlPlane control.ControlPlane

	// Path management
	defaultPathID string
	pathValidator PathValidator

	// Configuration
	config *LogicalStreamConfig

	// Context for cancellation
	ctx    context.Context
	cancel context.CancelFunc
}

// LogicalStreamInfo contains metadata about a logical stream
type LogicalStreamInfo struct {
	ID              uint64
	PathID          string
	State           LogicalStreamState
	CreatedAt       time.Time
	LastActivity    time.Time
	BytesRead       uint64
	BytesWritten    uint64
	RealStreamID    uint64 // ID of the underlying QUIC stream (for multiplexing)
	
	// Stream data management
	readBuffer      []byte
	writeBuffer     []byte
	offset          uint64
	
	// Synchronization
	mutex           sync.RWMutex
}

// LogicalStreamState represents the state of a logical stream
type LogicalStreamState int

const (
	LogicalStreamStateIdle LogicalStreamState = iota
	LogicalStreamStateCreating
	LogicalStreamStateActive
	LogicalStreamStateClosing
	LogicalStreamStateClosed
	LogicalStreamStateError
)

// LogicalStreamConfig contains configuration for logical stream management
type LogicalStreamConfig struct {
	// Buffer sizes
	DefaultReadBufferSize  int
	DefaultWriteBufferSize int
	
	// Stream limits
	MaxConcurrentStreams   int
	StreamIdleTimeout      time.Duration
	
	// Notification settings
	NotificationTimeout    time.Duration
	NotificationRetries    int
}

// PathValidator validates path operations
type PathValidator interface {
	ValidatePathForStreamCreation(pathID string) error
	GetDefaultPathForStreams() (string, error)
}

// NewLogicalStreamManager creates a new logical stream manager
func NewLogicalStreamManager(controlPlane control.ControlPlane, pathValidator PathValidator, config *LogicalStreamConfig) *LogicalStreamManager {
	if config == nil {
		config = DefaultLogicalStreamConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &LogicalStreamManager{
		streams:           make(map[uint64]*LogicalStreamInfo),
		streamCounter:     0,
		secondaryMappings: make(map[uint64][]uint64),
		controlPlane:      controlPlane,
		pathValidator:     pathValidator,
		config:            config,
		ctx:               ctx,
		cancel:            cancel,
	}
}

// NewLogicalStreamManagerWithIsolation creates a new logical stream manager with secondary stream isolation support
// Implements Requirements 5.4, 5.5: secondary stream mapping and logical stream creation from secondary streams
func NewLogicalStreamManagerWithIsolation(
	controlPlane control.ControlPlane,
	pathValidator PathValidator,
	isolator SecondaryStreamIsolator,
	mappingManager *StreamMappingManager,
	config *LogicalStreamConfig,
) *LogicalStreamManager {
	if config == nil {
		config = DefaultLogicalStreamConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &LogicalStreamManager{
		streams:           make(map[uint64]*LogicalStreamInfo),
		streamCounter:     0,
		isolator:          isolator,
		mappingManager:    mappingManager,
		secondaryMappings: make(map[uint64][]uint64),
		controlPlane:      controlPlane,
		pathValidator:     pathValidator,
		config:            config,
		ctx:               ctx,
		cancel:            cancel,
	}
}

// DefaultLogicalStreamConfig returns default configuration
func DefaultLogicalStreamConfig() *LogicalStreamConfig {
	return &LogicalStreamConfig{
		DefaultReadBufferSize:  utils.DefaultReadBufferSize,
		DefaultWriteBufferSize: utils.DefaultWriteBufferSize,
		MaxConcurrentStreams:   1000,
		StreamIdleTimeout:      5 * time.Minute,
		NotificationTimeout:    30 * time.Second,
		NotificationRetries:    3,
	}
}

// CreateLogicalStream creates a new logical stream without requiring a new QUIC stream
// Implements Requirement 7.1: logical stream creation without new QUIC streams
func (lsm *LogicalStreamManager) CreateLogicalStream(pathID string) (*LogicalStreamInfo, error) {
	lsm.mutex.Lock()
	defer lsm.mutex.Unlock()

	// Validate path for stream creation
	if pathID == "" {
		var err error
		pathID, err = lsm.pathValidator.GetDefaultPathForStreams()
		if err != nil {
			return nil, utils.NewKwikError(utils.ErrStreamCreationFailed, 
				"failed to get default path for stream creation", err)
		}
	}

	err := lsm.pathValidator.ValidatePathForStreamCreation(pathID)
	if err != nil {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"path validation failed for stream creation", err)
	}

	// Check concurrent stream limit
	if len(lsm.streams) >= lsm.config.MaxConcurrentStreams {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"maximum concurrent streams limit reached", nil)
	}

	// Generate unique stream ID
	streamID := atomic.AddUint64(&lsm.streamCounter, 1)

	// Create logical stream info
	streamInfo := &LogicalStreamInfo{
		ID:                  streamID,
		PathID:              pathID,
		State:               LogicalStreamStateCreating,
		CreatedAt:           time.Now(),
		LastActivity:        time.Now(),
		BytesRead:           0,
		BytesWritten:        0,
		RealStreamID:        0, // Will be assigned by multiplexer
		readBuffer:          make([]byte, 0, lsm.config.DefaultReadBufferSize),
		writeBuffer:         make([]byte, 0, lsm.config.DefaultWriteBufferSize),
		offset:              0,
	}

	// Store stream info
	lsm.streams[streamID] = streamInfo

	// Send control plane notification for logical stream creation
	// Implements Requirement 7.2: control plane notifications for stream creation
	err = lsm.sendStreamCreateNotification(streamInfo)
	if err != nil {
		// Clean up on notification failure
		delete(lsm.streams, streamID)
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"failed to send stream creation notification", err)
	}

	// Mark stream as active after successful notification
	streamInfo.State = LogicalStreamStateActive

	return streamInfo, nil
}

// sendStreamCreateNotification sends a control plane notification for stream creation
// Implements Requirement 7.2: control plane notifications with identifiers
func (lsm *LogicalStreamManager) sendStreamCreateNotification(streamInfo *LogicalStreamInfo) error {
	// Create stream creation notification
	notification := &control.StreamCreateNotification{
		LogicalStreamID: streamInfo.ID,
		PathID:          streamInfo.PathID,
	}

	// Create control frame
	frame := &protocol.ControlFrame{
		FrameID:   atomic.AddUint64(&lsm.frameCounter, 1), // Use separate counter for frame IDs
		FrameType: protocol.FrameTypeStreamCreateNotification,
		Timestamp: time.Now(),
	}

	// Serialize notification as payload
	payload, err := lsm.serializeNotification(notification)
	if err != nil {
		return fmt.Errorf("failed to serialize stream create notification: %w", err)
	}
	frame.Payload = payload

	// Send notification via control plane with retries
	var lastErr error
	for i := 0; i < lsm.config.NotificationRetries; i++ {
		err = lsm.controlPlane.SendFrame(streamInfo.PathID, frame)

		if err == nil {
			return nil // Success
		}
		lastErr = err

		// Wait before retry (exponential backoff)
		if i < lsm.config.NotificationRetries-1 {
			time.Sleep(time.Duration(i+1) * 100 * time.Millisecond)
		}
	}

	return fmt.Errorf("failed to send stream create notification after %d retries: %w", 
		lsm.config.NotificationRetries, lastErr)
}

// serializeNotification serializes a notification to bytes
func (lsm *LogicalStreamManager) serializeNotification(notification *control.StreamCreateNotification) ([]byte, error) {
	// Simple serialization - in a real implementation, this would use protobuf
	data := fmt.Sprintf("STREAM_CREATE:%d:%s", notification.LogicalStreamID, notification.PathID)
	return []byte(data), nil
}

// GetLogicalStream retrieves a logical stream by ID
func (lsm *LogicalStreamManager) GetLogicalStream(streamID uint64) (*LogicalStreamInfo, error) {
	lsm.mutex.RLock()
	defer lsm.mutex.RUnlock()

	streamInfo, exists := lsm.streams[streamID]
	if !exists {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			fmt.Sprintf("logical stream %d not found", streamID), nil)
	}

	// Update last activity
	streamInfo.mutex.Lock()
	streamInfo.LastActivity = time.Now()
	streamInfo.mutex.Unlock()

	return streamInfo, nil
}

// CloseLogicalStream closes a logical stream
func (lsm *LogicalStreamManager) CloseLogicalStream(streamID uint64) error {
	lsm.mutex.Lock()
	defer lsm.mutex.Unlock()

	streamInfo, exists := lsm.streams[streamID]
	if !exists {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			fmt.Sprintf("logical stream %d not found", streamID), nil)
	}

	streamInfo.mutex.Lock()
	defer streamInfo.mutex.Unlock()

	// Update state
	streamInfo.State = LogicalStreamStateClosing

	// TODO: Send stream close notification via control plane
	// TODO: Clean up any associated resources

	// Mark as closed
	streamInfo.State = LogicalStreamStateClosed

	// Remove from active streams
	delete(lsm.streams, streamID)

	return nil
}

// GetActiveStreams returns all active logical streams
func (lsm *LogicalStreamManager) GetActiveStreams() []*LogicalStreamInfo {
	lsm.mutex.RLock()
	defer lsm.mutex.RUnlock()

	activeStreams := make([]*LogicalStreamInfo, 0, len(lsm.streams))
	for _, streamInfo := range lsm.streams {
		streamInfo.mutex.RLock()
		if streamInfo.State == LogicalStreamStateActive {
			activeStreams = append(activeStreams, streamInfo)
		}
		streamInfo.mutex.RUnlock()
	}

	return activeStreams
}

// GetStreamCount returns the number of active logical streams
func (lsm *LogicalStreamManager) GetStreamCount() int {
	lsm.mutex.RLock()
	defer lsm.mutex.RUnlock()
	return len(lsm.streams)
}

// SetDefaultPath sets the default path for new streams
func (lsm *LogicalStreamManager) SetDefaultPath(pathID string) {
	lsm.mutex.Lock()
	defer lsm.mutex.Unlock()
	lsm.defaultPathID = pathID
}

// GetDefaultPath returns the default path for new streams
func (lsm *LogicalStreamManager) GetDefaultPath() string {
	lsm.mutex.RLock()
	defer lsm.mutex.RUnlock()
	return lsm.defaultPathID
}

// StartCleanupRoutine starts a background routine to clean up idle streams
func (lsm *LogicalStreamManager) StartCleanupRoutine() {
	go func() {
		ticker := time.NewTicker(lsm.config.StreamIdleTimeout / 2)
		defer ticker.Stop()

		for {
			select {
			case <-lsm.ctx.Done():
				return
			case <-ticker.C:
				lsm.cleanupIdleStreams()
			}
		}
	}()
}

// cleanupIdleStreams removes streams that have been idle for too long
func (lsm *LogicalStreamManager) cleanupIdleStreams() {
	lsm.mutex.Lock()
	defer lsm.mutex.Unlock()

	now := time.Now()
	idleThreshold := now.Add(-lsm.config.StreamIdleTimeout)

	for streamID, streamInfo := range lsm.streams {
		streamInfo.mutex.RLock()
		lastActivity := streamInfo.LastActivity
		state := streamInfo.State
		streamInfo.mutex.RUnlock()

		if state == LogicalStreamStateActive && lastActivity.Before(idleThreshold) {
			// Mark stream as closing and remove it
			streamInfo.mutex.Lock()
			streamInfo.State = LogicalStreamStateClosed
			streamInfo.mutex.Unlock()

			delete(lsm.streams, streamID)
		}
	}
}

// CreateLogicalStreamFromSecondary creates a logical KWIK stream from secondary stream data
// Implements Requirement 5.5: logical stream creation from secondary streams
func (lsm *LogicalStreamManager) CreateLogicalStreamFromSecondary(secondaryStreamID uint64, pathID string, metadata *StreamMetadata) (*LogicalStreamInfo, error) {
	if lsm.isolator == nil {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"secondary stream isolation not configured", nil)
	}

	lsm.mutex.Lock()
	defer lsm.mutex.Unlock()

	// Validate that this is a secondary stream
	if !lsm.isolator.IsStreamSecondary(secondaryStreamID) {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"stream is not a secondary stream", nil)
	}

	// Validate path for stream creation
	if pathID == "" {
		var err error
		pathID, err = lsm.pathValidator.GetDefaultPathForStreams()
		if err != nil {
			return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
				"failed to get default path for stream creation", err)
		}
	}

	err := lsm.pathValidator.ValidatePathForStreamCreation(pathID)
	if err != nil {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"path validation failed for stream creation", err)
	}

	// Check concurrent stream limit
	if len(lsm.streams) >= lsm.config.MaxConcurrentStreams {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"maximum concurrent streams limit reached", nil)
	}

	// Generate unique logical stream ID
	streamID := atomic.AddUint64(&lsm.streamCounter, 1)

	// Create logical stream info
	streamInfo := &LogicalStreamInfo{
		ID:           streamID,
		PathID:       pathID,
		State:        LogicalStreamStateCreating,
		CreatedAt:    time.Now(),
		LastActivity: time.Now(),
		BytesRead:    0,
		BytesWritten: 0,
		RealStreamID: 0, // Will be assigned by multiplexer
		readBuffer:   make([]byte, 0, lsm.config.DefaultReadBufferSize),
		writeBuffer:  make([]byte, 0, lsm.config.DefaultWriteBufferSize),
		offset:       0,
	}

	// Store stream info
	lsm.streams[streamID] = streamInfo

	// Create mapping between secondary stream and logical stream
	if err := lsm.addSecondaryMapping(streamID, secondaryStreamID); err != nil {
		delete(lsm.streams, streamID)
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"failed to create secondary stream mapping", err)
	}

	// Send control plane notification for logical stream creation
	err = lsm.sendStreamCreateNotification(streamInfo)
	if err != nil {
		// Clean up on notification failure
		delete(lsm.streams, streamID)
		lsm.removeSecondaryMapping(streamID, secondaryStreamID)
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"failed to send stream creation notification", err)
	}

	// Mark stream as active after successful notification
	streamInfo.State = LogicalStreamStateActive

	return streamInfo, nil
}

// AddSecondaryStreamMapping adds a mapping between a KWIK logical stream and a secondary stream
// Implements Requirement 5.4: secondary stream mapping management
func (lsm *LogicalStreamManager) AddSecondaryStreamMapping(kwikStreamID uint64, secondaryStreamID uint64) error {
	if lsm.isolator == nil {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			"secondary stream isolation not configured", nil)
	}

	lsm.mutex.Lock()
	defer lsm.mutex.Unlock()

	// Validate that the KWIK stream exists
	if _, exists := lsm.streams[kwikStreamID]; !exists {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			fmt.Sprintf("KWIK stream %d not found", kwikStreamID), nil)
	}

	// Validate that this is a secondary stream
	if !lsm.isolator.IsStreamSecondary(secondaryStreamID) {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			"stream is not a secondary stream", nil)
	}

	// Add the mapping
	return lsm.addSecondaryMapping(kwikStreamID, secondaryStreamID)
}

// RemoveSecondaryStreamMapping removes a mapping between a KWIK logical stream and a secondary stream
// Implements Requirement 5.4: secondary stream mapping management
func (lsm *LogicalStreamManager) RemoveSecondaryStreamMapping(kwikStreamID uint64, secondaryStreamID uint64) error {
	if lsm.isolator == nil {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			"secondary stream isolation not configured", nil)
	}

	lsm.mutex.Lock()
	defer lsm.mutex.Unlock()

	return lsm.removeSecondaryMapping(kwikStreamID, secondaryStreamID)
}

// GetSecondaryStreamMappings returns all secondary streams mapped to a KWIK logical stream
// Implements Requirement 5.4: secondary stream mapping queries
func (lsm *LogicalStreamManager) GetSecondaryStreamMappings(kwikStreamID uint64) ([]uint64, error) {
	lsm.mutex.RLock()
	defer lsm.mutex.RUnlock()

	// Validate that the KWIK stream exists
	if _, exists := lsm.streams[kwikStreamID]; !exists {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			fmt.Sprintf("KWIK stream %d not found", kwikStreamID), nil)
	}

	// Return copy of secondary stream IDs
	if secondaryStreams, exists := lsm.secondaryMappings[kwikStreamID]; exists {
		result := make([]uint64, len(secondaryStreams))
		copy(result, secondaryStreams)
		return result, nil
	}

	return []uint64{}, nil
}

// GetAllSecondaryMappings returns all secondary stream mappings
// Provides visibility into the mapping state for monitoring and debugging
func (lsm *LogicalStreamManager) GetAllSecondaryMappings() map[uint64][]uint64 {
	lsm.mutex.RLock()
	defer lsm.mutex.RUnlock()

	// Create a deep copy of the mappings
	result := make(map[uint64][]uint64)
	for kwikStreamID, secondaryStreams := range lsm.secondaryMappings {
		result[kwikStreamID] = make([]uint64, len(secondaryStreams))
		copy(result[kwikStreamID], secondaryStreams)
	}

	return result
}

// IsStreamMappedToSecondary checks if a KWIK logical stream has any secondary stream mappings
// Useful for determining if a stream receives data from secondary sources
func (lsm *LogicalStreamManager) IsStreamMappedToSecondary(kwikStreamID uint64) bool {
	lsm.mutex.RLock()
	defer lsm.mutex.RUnlock()

	secondaryStreams, exists := lsm.secondaryMappings[kwikStreamID]
	return exists && len(secondaryStreams) > 0
}

// addSecondaryMapping adds a secondary stream mapping (internal method)
func (lsm *LogicalStreamManager) addSecondaryMapping(kwikStreamID uint64, secondaryStreamID uint64) error {
	// Check if secondary stream is already mapped to this KWIK stream
	if secondaryStreams, exists := lsm.secondaryMappings[kwikStreamID]; exists {
		for _, existingSecondaryID := range secondaryStreams {
			if existingSecondaryID == secondaryStreamID {
				return utils.NewKwikError(utils.ErrStreamCreationFailed,
					"secondary stream already mapped to this KWIK stream", nil)
			}
		}
		// Add to existing mapping
		lsm.secondaryMappings[kwikStreamID] = append(secondaryStreams, secondaryStreamID)
	} else {
		// Create new mapping
		lsm.secondaryMappings[kwikStreamID] = []uint64{secondaryStreamID}
	}

	// Update mapping manager if available
	if lsm.mappingManager != nil {
		if err := lsm.mappingManager.CreateMapping(secondaryStreamID, kwikStreamID, ""); err != nil {
			// Remove the mapping we just added on error
			lsm.removeSecondaryMapping(kwikStreamID, secondaryStreamID)
			return utils.NewKwikError(utils.ErrStreamCreationFailed,
				"failed to update mapping manager", err)
		}
	}

	return nil
}

// removeSecondaryMapping removes a secondary stream mapping (internal method)
func (lsm *LogicalStreamManager) removeSecondaryMapping(kwikStreamID uint64, secondaryStreamID uint64) error {
	secondaryStreams, exists := lsm.secondaryMappings[kwikStreamID]
	if !exists {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			"no secondary mappings found for KWIK stream", nil)
	}

	// Find and remove the secondary stream ID
	for i, existingSecondaryID := range secondaryStreams {
		if existingSecondaryID == secondaryStreamID {
			// Remove from slice
			lsm.secondaryMappings[kwikStreamID] = append(secondaryStreams[:i], secondaryStreams[i+1:]...)
			
			// Clean up empty mapping
			if len(lsm.secondaryMappings[kwikStreamID]) == 0 {
				delete(lsm.secondaryMappings, kwikStreamID)
			}

			// Update mapping manager if available
			if lsm.mappingManager != nil {
				if err := lsm.mappingManager.RemoveMapping(secondaryStreamID); err != nil {
					// Log error but don't fail the operation
					// The mapping manager error is not critical for the logical stream manager
				}
			}

			return nil
		}
	}

	return utils.NewKwikError(utils.ErrStreamCreationFailed,
		"secondary stream not found in mapping", nil)
}

// CloseLogicalStreamWithSecondaryCleanup closes a logical stream and cleans up secondary mappings
// Extends the base CloseLogicalStream to handle secondary stream cleanup
func (lsm *LogicalStreamManager) CloseLogicalStreamWithSecondaryCleanup(streamID uint64) error {
	lsm.mutex.Lock()
	defer lsm.mutex.Unlock()

	streamInfo, exists := lsm.streams[streamID]
	if !exists {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			fmt.Sprintf("logical stream %d not found", streamID), nil)
	}

	streamInfo.mutex.Lock()
	defer streamInfo.mutex.Unlock()

	// Update state
	streamInfo.State = LogicalStreamStateClosing

	// Clean up secondary mappings
	if secondaryStreams, exists := lsm.secondaryMappings[streamID]; exists {
		for _, secondaryStreamID := range secondaryStreams {
			// Update mapping manager if available
			if lsm.mappingManager != nil {
				lsm.mappingManager.RemoveMapping(secondaryStreamID)
			}
		}
		delete(lsm.secondaryMappings, streamID)
	}

	// Mark as closed
	streamInfo.State = LogicalStreamStateClosed

	// Remove from active streams
	delete(lsm.streams, streamID)

	return nil
}

// Close shuts down the logical stream manager
func (lsm *LogicalStreamManager) Close() error {
	lsm.cancel()

	lsm.mutex.Lock()
	defer lsm.mutex.Unlock()

	// Close all active streams
	for streamID := range lsm.streams {
		// Note: We don't call CloseLogicalStream here to avoid deadlock
		// Just mark them as closed
		if streamInfo, exists := lsm.streams[streamID]; exists {
			streamInfo.mutex.Lock()
			streamInfo.State = LogicalStreamStateClosed
			streamInfo.mutex.Unlock()
		}
	}

	// Clear streams map and secondary mappings
	lsm.streams = make(map[uint64]*LogicalStreamInfo)
	lsm.secondaryMappings = make(map[uint64][]uint64)

	return nil
}

// String returns string representation of logical stream state
func (state LogicalStreamState) String() string {
	switch state {
	case LogicalStreamStateIdle:
		return "IDLE"
	case LogicalStreamStateCreating:
		return "CREATING"
	case LogicalStreamStateActive:
		return "ACTIVE"
	case LogicalStreamStateClosing:
		return "CLOSING"
	case LogicalStreamStateClosed:
		return "CLOSED"
	case LogicalStreamStateError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}