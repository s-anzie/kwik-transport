package kwik

import (
	"kwik/pkg/control"
	"kwik/pkg/data"
	"kwik/pkg/logger"
	"kwik/pkg/stream"
	"kwik/pkg/transport"
)

// PathStatusHandler handles path status notifications
type PathStatusHandler struct {
	controlPlane control.ControlPlane
	dataPlane    data.DataPlane
	logger       logger.Logger
}

// OnPathStatusChanged handles path status change notifications
func (h *PathStatusHandler) OnPathStatusChanged(pathID string, oldStatus, newStatus transport.PathState, metrics *transport.PathHealthMetrics) {
	h.logger.Info("Path status changed", "pathID", pathID, "oldStatus", oldStatus, "newStatus", newStatus)
}

// OnPathFailureDetected handles path failure notifications
func (h *PathStatusHandler) OnPathFailureDetected(pathID string, reason string, metrics *transport.PathHealthMetrics) {
	h.logger.Warn("Path failure detected", "pathID", pathID, "reason", reason)
}

// OnPathRecovered handles path recovery notifications
func (h *PathStatusHandler) OnPathRecovered(pathID string, metrics *transport.PathHealthMetrics) {
	h.logger.Info("Path recovered", "pathID", pathID)
}

// ControlMessageHandlers handles control plane messages
type ControlMessageHandlers struct {
	sessionManager *SessionManager
	pathManager    transport.PathManager
	dataPlane      data.DataPlane
	logger         logger.Logger
}

// MessageRouter handles inter-component communication
type MessageRouter struct {
	controlPlane      control.ControlPlane
	dataPlane         data.DataPlane
	sessionManager    *SessionManager
	pathManager       transport.PathManager
	streamMultiplexer stream.LogicalStreamManagerInterface
	logger            logger.Logger
}

// SetupRouting sets up message routing between components
func (mr *MessageRouter) SetupRouting() error {
	mr.logger.Info("Setting up message routing between components")
	// TODO: Implement actual routing logic
	return nil
}
