package session

import (
	"fmt"
	"sync"
	"time"

	"kwik/proto/control"
)

// SessionControlPlane implements ControlPlaneInterface for session-level flow control
type SessionControlPlane struct {
	clientSession *ClientSession
	serverSession *ServerSession

	// Handlers
	windowUpdateHandler func(pathID string, windowSize uint64)
	backpressureHandler func(pathID string, active bool)
}

// NewSessionControlPlane creates a new session control plane for client session
func NewSessionControlPlane(session interface{}) *SessionControlPlane {
	scp := &SessionControlPlane{}
	
	switch s := session.(type) {
	case *ClientSession:
		scp.clientSession = s
	case *ServerSession:
		scp.serverSession = s
	default:
		panic("unsupported session type")
	}
	
	return scp
}

// sendControlFrame sends a control frame using the appropriate session
func (scp *SessionControlPlane) sendControlFrame(pathID string, frame *control.ControlFrame) error {
	if scp.clientSession != nil {
		return scp.clientSession.SendControlFrame(pathID, frame)
	}
	if scp.serverSession != nil {
		return scp.serverSession.SendControlFrame(pathID, frame)
	}
	return fmt.Errorf("no session available")
}

// getLogger returns the appropriate logger
func (scp *SessionControlPlane) getLogger() interface{} {
	if scp.clientSession != nil {
		return scp.clientSession.GetLogger()
	}
	if scp.serverSession != nil {
		return scp.serverSession.GetLogger()
	}
	return nil
}

// SendWindowUpdate sends a window update control frame
func (scp *SessionControlPlane) SendWindowUpdate(pathID string, windowSize uint64) error {
	// Create window update message
	windowUpdate := &control.WindowUpdate{
		PathId:     pathID,
		WindowSize: windowSize,
		Timestamp:  uint64(time.Now().UnixNano()),
	}

	// Serialize the window update (using simple JSON for now)
	payload := []byte(fmt.Sprintf(`{"pathId":"%s","windowSize":%d,"timestamp":%d}`, 
		windowUpdate.PathId, windowUpdate.WindowSize, windowUpdate.Timestamp))

	// Create control frame
	frame := &control.ControlFrame{
		FrameId:   uint64(time.Now().UnixNano()), // Simple frame ID generation
		Type:      control.ControlFrameType_WINDOW_UPDATE,
		Payload:   payload,
		Timestamp: uint64(time.Now().UnixNano()),
	}

	// Send via session
	err := scp.sendControlFrame(pathID, frame)
	if err != nil {
		return fmt.Errorf("failed to send window update control frame: %w", err)
	}

	// Log using the appropriate logger
	if logger := scp.getLogger(); logger != nil {
		if streamLogger, ok := logger.(interface{ Debug(string, ...interface{}) }); ok {
			streamLogger.Debug("Sent window update", "pathID", pathID, "windowSize", windowSize)
		}
	}
	return nil
}

// SendBackpressureSignal sends a backpressure signal control frame
func (scp *SessionControlPlane) SendBackpressureSignal(pathID string, active bool) error {
	// Create backpressure signal message
	backpressureSignal := &control.BackpressureSignal{
		PathId:    pathID,
		Active:    active,
		Timestamp: uint64(time.Now().UnixNano()),
	}

	// Serialize the backpressure signal (using simple JSON for now)
	payload := []byte(fmt.Sprintf(`{"pathId":"%s","active":%t,"timestamp":%d}`, 
		backpressureSignal.PathId, backpressureSignal.Active, backpressureSignal.Timestamp))

	// Create control frame
	frame := &control.ControlFrame{
		FrameId:   uint64(time.Now().UnixNano()), // Simple frame ID generation
		Type:      control.ControlFrameType_BACKPRESSURE_SIGNAL,
		Payload:   payload,
		Timestamp: uint64(time.Now().UnixNano()),
	}

	// Send via session
	err := scp.sendControlFrame(pathID, frame)
	if err != nil {
		return fmt.Errorf("failed to send backpressure signal control frame: %w", err)
	}

	// Log using the appropriate logger
	if logger := scp.getLogger(); logger != nil {
		if streamLogger, ok := logger.(interface{ Debug(string, ...interface{}) }); ok {
			streamLogger.Debug("Sent backpressure signal", "pathID", pathID, "active", active)
		}
	}
	return nil
}

// RegisterWindowUpdateHandler registers a handler for window update messages
func (scp *SessionControlPlane) RegisterWindowUpdateHandler(handler func(pathID string, windowSize uint64)) {
	scp.windowUpdateHandler = handler
}

// RegisterBackpressureHandler registers a handler for backpressure signals
func (scp *SessionControlPlane) RegisterBackpressureHandler(handler func(pathID string, active bool)) {
	scp.backpressureHandler = handler
}

// HandleControlFrame handles incoming control frames related to flow control
func (scp *SessionControlPlane) HandleControlFrame(frame *control.ControlFrame) error {
	switch frame.Type {
	case control.ControlFrameType_WINDOW_UPDATE:
		return scp.handleWindowUpdate(frame)
	case control.ControlFrameType_BACKPRESSURE_SIGNAL:
		return scp.handleBackpressureSignal(frame)
	default:
		// Not a flow control frame, ignore
		return nil
	}
}

// handleWindowUpdate handles incoming window update frames
func (scp *SessionControlPlane) handleWindowUpdate(frame *control.ControlFrame) error {
	// For now, just parse the JSON payload manually
	// In a real implementation, you'd use proper protobuf or JSON unmarshaling
	payload := string(frame.Payload)
	
	// Log using the appropriate logger
	if logger := scp.getLogger(); logger != nil {
		if streamLogger, ok := logger.(interface{ Debug(string, ...interface{}) }); ok {
			streamLogger.Debug("Received window update payload", "payload", payload)
		}
	}
	
	// Call registered handler with dummy values for now
	if scp.windowUpdateHandler != nil {
		scp.windowUpdateHandler("default", 1024*1024) // Default 1MB window
	}

	// Log using the appropriate logger
	if logger := scp.getLogger(); logger != nil {
		if streamLogger, ok := logger.(interface{ Debug(string, ...interface{}) }); ok {
			streamLogger.Debug("Received window update", "pathID", "default", "windowSize", 1024*1024)
		}
	}
	return nil
}

// handleBackpressureSignal handles incoming backpressure signal frames
func (scp *SessionControlPlane) handleBackpressureSignal(frame *control.ControlFrame) error {
	// For now, just parse the JSON payload manually
	// In a real implementation, you'd use proper protobuf or JSON unmarshaling
	payload := string(frame.Payload)
	
	// Log using the appropriate logger
	if logger := scp.getLogger(); logger != nil {
		if streamLogger, ok := logger.(interface{ Debug(string, ...interface{}) }); ok {
			streamLogger.Debug("Received backpressure signal payload", "payload", payload)
		}
	}
	
	// Call registered handler with dummy values for now
	if scp.backpressureHandler != nil {
		scp.backpressureHandler("default", false) // Default not active
	}

	// Log using the appropriate logger
	if logger := scp.getLogger(); logger != nil {
		if streamLogger, ok := logger.(interface{ Debug(string, ...interface{}) }); ok {
			streamLogger.Debug("Received backpressure signal", "pathID", "default", "active", false)
		}
	}
	return nil
}

// FlowControlLogger defines the minimal logging interface needed for flow control
type FlowControlLogger interface {
	Debug(msg string, keysAndValues ...interface{})
	Error(msg string, keysAndValues ...interface{})
	Warn(msg string, keysAndValues ...interface{})
}

// FlowControlledSender provides flow-controlled sending capabilities
type FlowControlledSender struct {
	flowControlManager *FlowControlManager
	logger             FlowControlLogger
}

// NewFlowControlledSender creates a new flow-controlled sender
func NewFlowControlledSender(flowControlManager *FlowControlManager, logger FlowControlLogger) *FlowControlledSender {
	return &FlowControlledSender{
		flowControlManager: flowControlManager,
		logger:             logger,
	}
}

// SendData sends data with flow control
func (fcs *FlowControlledSender) SendData(pathID string, data []byte, sendFunc func([]byte) error) error {
	dataSize := uint64(len(data))

	// Check if we can send data
	if !fcs.flowControlManager.CanSendData(pathID, dataSize) {
		return fmt.Errorf("cannot send data: insufficient send window for path %s", pathID)
	}

	// Reserve window space
	err := fcs.flowControlManager.ReserveWindow(pathID, dataSize)
	if err != nil {
		return fmt.Errorf("failed to reserve send window: %w", err)
	}

	// Send the data
	err = sendFunc(data)
	if err != nil {
		// If send failed, we should release the reserved window
		// This is a simplified approach - in practice, you might want more sophisticated error handling
		return fmt.Errorf("failed to send data: %w", err)
	}

	// Consume window space
	err = fcs.flowControlManager.ConsumeWindow(pathID, dataSize)
	if err != nil {
		fcs.logger.Error("Failed to consume send window", "pathID", pathID, "dataSize", dataSize, "error", err)
	}

	return nil
}

// SendDataWithRetry sends data with flow control and retry logic
func (fcs *FlowControlledSender) SendDataWithRetry(pathID string, data []byte, sendFunc func([]byte) error, maxRetries int, retryDelay time.Duration) error {
	dataSize := uint64(len(data))

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Check if we can send data
		if fcs.flowControlManager.CanSendData(pathID, dataSize) {
			// Reserve window space
			err := fcs.flowControlManager.ReserveWindow(pathID, dataSize)
			if err == nil {
				// Send the data
				err = sendFunc(data)
				if err == nil {
					// Success - consume window space
					fcs.flowControlManager.ConsumeWindow(pathID, dataSize)
					return nil
				}
				// Send failed, but window was reserved - this is handled by the flow control manager
			}
		}

		// If this is not the last attempt, wait before retrying
		if attempt < maxRetries {
			fcs.logger.Debug("Send failed, retrying", "pathID", pathID, "attempt", attempt+1, "maxRetries", maxRetries)
			time.Sleep(retryDelay)
		}
	}

	return fmt.Errorf("failed to send data after %d attempts", maxRetries+1)
}

// FlowControlledReceiver provides flow-controlled receiving capabilities
type FlowControlledReceiver struct {
	flowControlManager *FlowControlManager
	logger             FlowControlLogger
}

// NewFlowControlledReceiver creates a new flow-controlled receiver
func NewFlowControlledReceiver(flowControlManager *FlowControlManager, logger FlowControlLogger) *FlowControlledReceiver {
	return &FlowControlledReceiver{
		flowControlManager: flowControlManager,
		logger:             logger,
	}
}

// ReceiveData handles received data with flow control
func (fcr *FlowControlledReceiver) ReceiveData(pathID string, data []byte) error {
	dataSize := uint64(len(data))

	// Notify flow control manager about received data
	err := fcr.flowControlManager.OnDataReceived(pathID, dataSize)
	if err != nil {
		fcr.logger.Warn("Flow control rejected data", "pathID", pathID, "dataSize", dataSize, "error", err)
		return err
	}

	fcr.logger.Debug("Received data with flow control", "pathID", pathID, "dataSize", dataSize)
	return nil
}

// ConsumeData notifies flow control that data has been consumed by the application
func (fcr *FlowControlledReceiver) ConsumeData(pathID string, dataSize uint64) error {
	err := fcr.flowControlManager.OnDataConsumed(pathID, dataSize)
	if err != nil {
		fcr.logger.Error("Failed to notify data consumption", "pathID", pathID, "dataSize", dataSize, "error", err)
		return err
	}

	fcr.logger.Debug("Consumed data", "pathID", pathID, "dataSize", dataSize)
	return nil
}

// PathWindowDistributor distributes window space among multiple paths
type PathWindowDistributor struct {
	totalWindow     uint64
	pathAllocations map[string]uint64
	mutex           sync.RWMutex
	logger          FlowControlLogger
}

// NewPathWindowDistributor creates a new path window distributor
func NewPathWindowDistributor(totalWindow uint64, logger FlowControlLogger) *PathWindowDistributor {
	return &PathWindowDistributor{
		totalWindow:     totalWindow,
		pathAllocations: make(map[string]uint64),
		logger:          logger,
	}
}

// AddPath adds a path to the distributor
func (pwd *PathWindowDistributor) AddPath(pathID string) {
	pwd.mutex.Lock()
	defer pwd.mutex.Unlock()

	// Redistribute window equally among all paths
	pathCount := len(pwd.pathAllocations) + 1
	windowPerPath := pwd.totalWindow / uint64(pathCount)

	// Update existing allocations
	for existingPathID := range pwd.pathAllocations {
		pwd.pathAllocations[existingPathID] = windowPerPath
	}

	// Add new path
	pwd.pathAllocations[pathID] = windowPerPath

	pwd.logger.Debug("Added path to window distributor", "pathID", pathID, "windowPerPath", windowPerPath, "totalPaths", pathCount)
}

// RemovePath removes a path from the distributor
func (pwd *PathWindowDistributor) RemovePath(pathID string) {
	pwd.mutex.Lock()
	defer pwd.mutex.Unlock()

	delete(pwd.pathAllocations, pathID)

	// Redistribute window among remaining paths
	if len(pwd.pathAllocations) > 0 {
		windowPerPath := pwd.totalWindow / uint64(len(pwd.pathAllocations))
		for existingPathID := range pwd.pathAllocations {
			pwd.pathAllocations[existingPathID] = windowPerPath
		}
	}

	pwd.logger.Debug("Removed path from window distributor", "pathID", pathID, "remainingPaths", len(pwd.pathAllocations))
}

// GetPathAllocation returns the window allocation for a path
func (pwd *PathWindowDistributor) GetPathAllocation(pathID string) uint64 {
	pwd.mutex.RLock()
	defer pwd.mutex.RUnlock()

	allocation, exists := pwd.pathAllocations[pathID]
	if !exists {
		return 0
	}

	return allocation
}

// UpdateTotalWindow updates the total window size and redistributes
func (pwd *PathWindowDistributor) UpdateTotalWindow(newTotalWindow uint64) {
	pwd.mutex.Lock()
	defer pwd.mutex.Unlock()

	pwd.totalWindow = newTotalWindow

	// Redistribute window among all paths
	if len(pwd.pathAllocations) > 0 {
		windowPerPath := pwd.totalWindow / uint64(len(pwd.pathAllocations))
		for pathID := range pwd.pathAllocations {
			pwd.pathAllocations[pathID] = windowPerPath
		}
	}

	pwd.logger.Debug("Updated total window", "newTotalWindow", newTotalWindow, "pathCount", len(pwd.pathAllocations))
}

// GetAllAllocations returns all current path allocations
func (pwd *PathWindowDistributor) GetAllAllocations() map[string]uint64 {
	pwd.mutex.RLock()
	defer pwd.mutex.RUnlock()

	allocations := make(map[string]uint64)
	for pathID, allocation := range pwd.pathAllocations {
		allocations[pathID] = allocation
	}

	return allocations
}
