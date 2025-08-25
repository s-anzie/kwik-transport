package protocol

import (
	"fmt"
	"sync"
	"time"
)

// Logger interface for heartbeat frame handler logging
type Logger interface {
	Debug(msg string, keysAndValues ...interface{})
	Info(msg string, keysAndValues ...interface{})
	Warn(msg string, keysAndValues ...interface{})
	Error(msg string, keysAndValues ...interface{})
}

// HeartbeatFrameHandler handles incoming heartbeat frames and routes them appropriately
type HeartbeatFrameHandler interface {
	// Handle incoming heartbeat frames
	HandleHeartbeatFrame(frame Frame, pathID string) error
	
	// Handle incoming heartbeat response frames
	HandleHeartbeatResponseFrame(frame Frame, pathID string) error
	
	// Register heartbeat systems
	RegisterControlHeartbeatSystem(system ControlPlaneHeartbeatSystem)
	RegisterDataHeartbeatSystem(system DataPlaneHeartbeatSystem)
	
	// Get frame processing statistics
	GetFrameStats() *HeartbeatFrameStats
}

// ControlPlaneHeartbeatSystem interface (forward declaration)
type ControlPlaneHeartbeatSystem interface {
	HandleControlHeartbeatRequest(request *HeartbeatFrame) error
	HandleControlHeartbeatResponse(response *HeartbeatResponseFrame) error
}

// DataPlaneHeartbeatSystem interface (forward declaration)
type DataPlaneHeartbeatSystem interface {
	HandleDataHeartbeatRequest(request *HeartbeatFrame) error
	HandleDataHeartbeatResponse(response *HeartbeatResponseFrame) error
}

// HeartbeatFrameStats contains statistics for heartbeat frame processing
type HeartbeatFrameStats struct {
	TotalHeartbeatsReceived   uint64
	TotalResponsesReceived    uint64
	TotalResponsesSent        uint64
	InvalidFramesReceived     uint64
	ProcessingErrors          uint64
	ControlPlaneFrames        uint64
	DataPlaneFrames           uint64
	LastFrameProcessed        time.Time
	
	// Per-path statistics
	PathStats map[string]*PathHeartbeatStats
	
	// Synchronization
	mutex sync.RWMutex
}

// PathHeartbeatStats contains heartbeat statistics for a specific path
type PathHeartbeatStats struct {
	PathID                    string
	HeartbeatsReceived        uint64
	ResponsesReceived         uint64
	ResponsesSent             uint64
	InvalidFrames             uint64
	ProcessingErrors          uint64
	LastHeartbeatReceived     time.Time
	LastResponseReceived      time.Time
	LastResponseSent          time.Time
}

// HeartbeatFrameHandlerImpl implements HeartbeatFrameHandler
type HeartbeatFrameHandlerImpl struct {
	// Registered systems
	controlSystem ControlPlaneHeartbeatSystem
	dataSystem    DataPlaneHeartbeatSystem
	
	// Frame validation
	validator HeartbeatFrameValidator
	
	// Logging
	logger Logger
	
	// Statistics
	frameStats *HeartbeatFrameStats
	
	// Configuration
	maxFrameSize int
	
	// Synchronization
	mutex sync.RWMutex
}

// HeartbeatFrameHandlerConfig contains configuration for the frame handler
type HeartbeatFrameHandlerConfig struct {
	MaxFrameSize int
	Logger       Logger
	Validator    HeartbeatFrameValidator
}

// DefaultHeartbeatFrameHandlerConfig returns default configuration
func DefaultHeartbeatFrameHandlerConfig() *HeartbeatFrameHandlerConfig {
	return &HeartbeatFrameHandlerConfig{
		MaxFrameSize: 4096,
		Logger:       &DefaultLogger{},
		Validator:    NewHeartbeatFrameValidator(nil),
	}
}

// DefaultLogger provides a simple logger implementation
type DefaultLogger struct{}

func (d *DefaultLogger) Debug(msg string, keysAndValues ...interface{}) {}
func (d *DefaultLogger) Info(msg string, keysAndValues ...interface{})  {}
func (d *DefaultLogger) Warn(msg string, keysAndValues ...interface{})  {}
func (d *DefaultLogger) Error(msg string, keysAndValues ...interface{}) {}

// NewHeartbeatFrameHandler creates a new heartbeat frame handler
func NewHeartbeatFrameHandler(config *HeartbeatFrameHandlerConfig) *HeartbeatFrameHandlerImpl {
	if config == nil {
		config = DefaultHeartbeatFrameHandlerConfig()
	}
	
	return &HeartbeatFrameHandlerImpl{
		validator:    config.Validator,
		logger:       config.Logger,
		maxFrameSize: config.MaxFrameSize,
		frameStats: &HeartbeatFrameStats{
			PathStats: make(map[string]*PathHeartbeatStats),
		},
	}
}

// RegisterControlHeartbeatSystem registers the control plane heartbeat system
func (h *HeartbeatFrameHandlerImpl) RegisterControlHeartbeatSystem(system ControlPlaneHeartbeatSystem) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	
	h.controlSystem = system
	h.logger.Info("Control plane heartbeat system registered")
}

// RegisterDataHeartbeatSystem registers the data plane heartbeat system
func (h *HeartbeatFrameHandlerImpl) RegisterDataHeartbeatSystem(system DataPlaneHeartbeatSystem) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	
	h.dataSystem = system
	h.logger.Info("Data plane heartbeat system registered")
}

// HandleHeartbeatFrame handles incoming heartbeat request frames
func (h *HeartbeatFrameHandlerImpl) HandleHeartbeatFrame(frame Frame, pathID string) error {
	// Validate frame type
	if frame.Type() != FrameTypeHeartbeat {
		return fmt.Errorf("expected heartbeat frame, got %v", frame.Type())
	}
	
	// Cast to heartbeat frame
	heartbeatFrame, ok := frame.(*HeartbeatFrame)
	if !ok {
		h.updateErrorStats(pathID, "invalid frame cast")
		return fmt.Errorf("failed to cast frame to HeartbeatFrame")
	}
	
	// Validate frame
	if err := h.validator.ValidateHeartbeatFrame(heartbeatFrame); err != nil {
		h.updateErrorStats(pathID, "validation failed")
		h.logger.Error("Heartbeat frame validation failed", "pathID", pathID, "error", err)
		return fmt.Errorf("heartbeat frame validation failed: %w", err)
	}
	
	// Update statistics
	h.updateReceiveStats(pathID, heartbeatFrame.PlaneType, "heartbeat")
	
	// Route to appropriate system based on plane type
	var err error
	switch heartbeatFrame.PlaneType {
	case HeartbeatPlaneControl:
		if h.controlSystem == nil {
			err = fmt.Errorf("control plane heartbeat system not registered")
		} else {
			err = h.controlSystem.HandleControlHeartbeatRequest(heartbeatFrame)
		}
		
	case HeartbeatPlaneData:
		if h.dataSystem == nil {
			err = fmt.Errorf("data plane heartbeat system not registered")
		} else {
			err = h.dataSystem.HandleDataHeartbeatRequest(heartbeatFrame)
		}
		
	default:
		err = fmt.Errorf("unknown heartbeat plane type: %d", heartbeatFrame.PlaneType)
	}
	
	if err != nil {
		h.updateErrorStats(pathID, "system processing failed")
		h.logger.Error("Heartbeat frame processing failed", 
			"pathID", pathID, 
			"planeType", heartbeatFrame.PlaneType,
			"sequenceID", heartbeatFrame.SequenceID,
			"error", err)
		return fmt.Errorf("heartbeat frame processing failed: %w", err)
	}
	
	h.logger.Debug("Heartbeat frame processed successfully",
		"pathID", pathID,
		"planeType", heartbeatFrame.PlaneType,
		"sequenceID", heartbeatFrame.SequenceID,
		"streamID", heartbeatFrame.StreamID)
	
	return nil
}

// HandleHeartbeatResponseFrame handles incoming heartbeat response frames
func (h *HeartbeatFrameHandlerImpl) HandleHeartbeatResponseFrame(frame Frame, pathID string) error {
	// Validate frame type
	if frame.Type() != FrameTypeHeartbeatResponse {
		return fmt.Errorf("expected heartbeat response frame, got %v", frame.Type())
	}
	
	// Cast to heartbeat response frame
	responseFrame, ok := frame.(*HeartbeatResponseFrame)
	if !ok {
		h.updateErrorStats(pathID, "invalid response frame cast")
		return fmt.Errorf("failed to cast frame to HeartbeatResponseFrame")
	}
	
	// Validate frame
	if err := h.validator.ValidateHeartbeatResponseFrame(responseFrame); err != nil {
		h.updateErrorStats(pathID, "response validation failed")
		h.logger.Error("Heartbeat response frame validation failed", "pathID", pathID, "error", err)
		return fmt.Errorf("heartbeat response frame validation failed: %w", err)
	}
	
	// Update statistics
	h.updateReceiveStats(pathID, responseFrame.PlaneType, "response")
	
	// Route to appropriate system based on plane type
	var err error
	switch responseFrame.PlaneType {
	case HeartbeatPlaneControl:
		if h.controlSystem == nil {
			err = fmt.Errorf("control plane heartbeat system not registered")
		} else {
			err = h.controlSystem.HandleControlHeartbeatResponse(responseFrame)
		}
		
	case HeartbeatPlaneData:
		if h.dataSystem == nil {
			err = fmt.Errorf("data plane heartbeat system not registered")
		} else {
			err = h.dataSystem.HandleDataHeartbeatResponse(responseFrame)
		}
		
	default:
		err = fmt.Errorf("unknown heartbeat response plane type: %d", responseFrame.PlaneType)
	}
	
	if err != nil {
		h.updateErrorStats(pathID, "response system processing failed")
		h.logger.Error("Heartbeat response frame processing failed", 
			"pathID", pathID, 
			"planeType", responseFrame.PlaneType,
			"requestSequenceID", responseFrame.RequestSequenceID,
			"error", err)
		return fmt.Errorf("heartbeat response frame processing failed: %w", err)
	}
	
	h.logger.Debug("Heartbeat response frame processed successfully",
		"pathID", pathID,
		"planeType", responseFrame.PlaneType,
		"requestSequenceID", responseFrame.RequestSequenceID,
		"streamID", responseFrame.StreamID,
		"serverLoad", responseFrame.ServerLoad)
	
	return nil
}

// GetFrameStats returns current frame processing statistics
func (h *HeartbeatFrameHandlerImpl) GetFrameStats() *HeartbeatFrameStats {
	h.frameStats.mutex.RLock()
	defer h.frameStats.mutex.RUnlock()
	
	// Create a deep copy of the stats
	statsCopy := &HeartbeatFrameStats{
		TotalHeartbeatsReceived: h.frameStats.TotalHeartbeatsReceived,
		TotalResponsesReceived:  h.frameStats.TotalResponsesReceived,
		TotalResponsesSent:      h.frameStats.TotalResponsesSent,
		InvalidFramesReceived:   h.frameStats.InvalidFramesReceived,
		ProcessingErrors:        h.frameStats.ProcessingErrors,
		ControlPlaneFrames:      h.frameStats.ControlPlaneFrames,
		DataPlaneFrames:         h.frameStats.DataPlaneFrames,
		LastFrameProcessed:      h.frameStats.LastFrameProcessed,
		PathStats:               make(map[string]*PathHeartbeatStats),
	}
	
	// Copy path stats
	for pathID, pathStats := range h.frameStats.PathStats {
		statsCopy.PathStats[pathID] = &PathHeartbeatStats{
			PathID:                pathStats.PathID,
			HeartbeatsReceived:    pathStats.HeartbeatsReceived,
			ResponsesReceived:     pathStats.ResponsesReceived,
			ResponsesSent:         pathStats.ResponsesSent,
			InvalidFrames:         pathStats.InvalidFrames,
			ProcessingErrors:      pathStats.ProcessingErrors,
			LastHeartbeatReceived: pathStats.LastHeartbeatReceived,
			LastResponseReceived:  pathStats.LastResponseReceived,
			LastResponseSent:      pathStats.LastResponseSent,
		}
	}
	
	return statsCopy
}

// updateReceiveStats updates statistics for received frames
func (h *HeartbeatFrameHandlerImpl) updateReceiveStats(pathID string, planeType HeartbeatPlaneType, frameType string) {
	h.frameStats.mutex.Lock()
	defer h.frameStats.mutex.Unlock()
	
	now := time.Now()
	h.frameStats.LastFrameProcessed = now
	
	// Update global stats
	switch frameType {
	case "heartbeat":
		h.frameStats.TotalHeartbeatsReceived++
	case "response":
		h.frameStats.TotalResponsesReceived++
	}
	
	// Update plane-specific stats
	switch planeType {
	case HeartbeatPlaneControl:
		h.frameStats.ControlPlaneFrames++
	case HeartbeatPlaneData:
		h.frameStats.DataPlaneFrames++
	}
	
	// Update path-specific stats
	pathStats, exists := h.frameStats.PathStats[pathID]
	if !exists {
		pathStats = &PathHeartbeatStats{
			PathID: pathID,
		}
		h.frameStats.PathStats[pathID] = pathStats
	}
	
	switch frameType {
	case "heartbeat":
		pathStats.HeartbeatsReceived++
		pathStats.LastHeartbeatReceived = now
	case "response":
		pathStats.ResponsesReceived++
		pathStats.LastResponseReceived = now
	}
}

// updateSendStats updates statistics for sent response frames
func (h *HeartbeatFrameHandlerImpl) updateSendStats(pathID string) {
	h.frameStats.mutex.Lock()
	defer h.frameStats.mutex.Unlock()
	
	now := time.Now()
	h.frameStats.TotalResponsesSent++
	
	// Update path-specific stats
	pathStats, exists := h.frameStats.PathStats[pathID]
	if !exists {
		pathStats = &PathHeartbeatStats{
			PathID: pathID,
		}
		h.frameStats.PathStats[pathID] = pathStats
	}
	
	pathStats.ResponsesSent++
	pathStats.LastResponseSent = now
}

// updateErrorStats updates error statistics
func (h *HeartbeatFrameHandlerImpl) updateErrorStats(pathID string, errorType string) {
	h.frameStats.mutex.Lock()
	defer h.frameStats.mutex.Unlock()
	
	h.frameStats.ProcessingErrors++
	
	if errorType == "validation failed" || errorType == "invalid frame cast" || errorType == "invalid response frame cast" {
		h.frameStats.InvalidFramesReceived++
	}
	
	// Update path-specific error stats
	pathStats, exists := h.frameStats.PathStats[pathID]
	if !exists {
		pathStats = &PathHeartbeatStats{
			PathID: pathID,
		}
		h.frameStats.PathStats[pathID] = pathStats
	}
	
	pathStats.ProcessingErrors++
	
	if errorType == "validation failed" || errorType == "invalid frame cast" || errorType == "invalid response frame cast" {
		pathStats.InvalidFrames++
	}
}

// ValidateFrameSize validates the size of a serialized frame
func (h *HeartbeatFrameHandlerImpl) ValidateFrameSize(frameData []byte) error {
	return h.validator.ValidateFrameSize(frameData, h.maxFrameSize)
}

// CreateHeartbeatFrame creates a new heartbeat frame with validation
func (h *HeartbeatFrameHandlerImpl) CreateHeartbeatFrame(
	frameID uint64,
	sequenceID uint64,
	pathID string,
	planeType HeartbeatPlaneType,
	streamID *uint64,
	rttMeasurement bool,
) (*HeartbeatFrame, error) {
	
	frame := &HeartbeatFrame{
		FrameID:        frameID,
		SequenceID:     sequenceID,
		PathID:         pathID,
		PlaneType:      planeType,
		StreamID:       streamID,
		Timestamp:      time.Now(),
		RTTMeasurement: rttMeasurement,
	}
	
	// Validate the created frame
	if err := h.validator.ValidateHeartbeatFrame(frame); err != nil {
		return nil, fmt.Errorf("created heartbeat frame is invalid: %w", err)
	}
	
	return frame, nil
}

// CreateHeartbeatResponseFrame creates a new heartbeat response frame with validation
func (h *HeartbeatFrameHandlerImpl) CreateHeartbeatResponseFrame(
	frameID uint64,
	requestSequenceID uint64,
	pathID string,
	planeType HeartbeatPlaneType,
	streamID *uint64,
	requestTimestamp time.Time,
	serverLoad float64,
) (*HeartbeatResponseFrame, error) {
	
	frame := &HeartbeatResponseFrame{
		FrameID:           frameID,
		RequestSequenceID: requestSequenceID,
		PathID:            pathID,
		PlaneType:         planeType,
		StreamID:          streamID,
		Timestamp:         time.Now(),
		RequestTimestamp:  requestTimestamp,
		ServerLoad:        serverLoad,
	}
	
	// Validate the created frame
	if err := h.validator.ValidateHeartbeatResponseFrame(frame); err != nil {
		return nil, fmt.Errorf("created heartbeat response frame is invalid: %w", err)
	}
	
	return frame, nil
}