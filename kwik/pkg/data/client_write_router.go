package data

import (
	"kwik/internal/utils"
	"kwik/proto/data"
)

// ClientWriteRouter implements routing logic for client writes
// This ensures requirement 4.3: client writes always go to primary server only
type ClientWriteRouter struct {
	primaryPathID string
	dataPlane     *DataPlaneImpl
}

// WriteRoutingPolicy defines how client writes should be routed
type WriteRoutingPolicy int

const (
	// WriteRoutingPrimaryOnly - all writes go to primary path only (default for clients)
	WriteRoutingPrimaryOnly WriteRoutingPolicy = iota
	// WriteRoutingLoadBalanced - writes are load balanced across paths (for servers)
	WriteRoutingLoadBalanced
	// WriteRoutingCustom - custom routing logic
	WriteRoutingCustom
)

// NewClientWriteRouter creates a new client write router
func NewClientWriteRouter(primaryPathID string, dataPlane *DataPlaneImpl) *ClientWriteRouter {
	return &ClientWriteRouter{
		primaryPathID: primaryPathID,
		dataPlane:     dataPlane,
	}
}

// RouteWrite routes a write operation to the appropriate path
// For clients, this always routes to the primary path only
func (cwr *ClientWriteRouter) RouteWrite(frame *data.DataFrame) (string, error) {
	// Validate that we have a primary path
	if cwr.primaryPathID == "" {
		return "", utils.NewKwikError(utils.ErrPathNotFound, 
			"no primary path configured for client writes", nil)
	}

	// Validate that the primary path exists and is active
	err := cwr.validatePrimaryPath()
	if err != nil {
		return "", err
	}

	// For client sessions, ALL writes must go to primary path only
	// This implements requirement 4.3: client writes always go to primary server
	return cwr.primaryPathID, nil
}

// ValidateWriteOperation validates that a write operation can proceed
func (cwr *ClientWriteRouter) ValidateWriteOperation(frame *data.DataFrame, targetPathID string) error {
	// Ensure the target path is the primary path
	if targetPathID != cwr.primaryPathID {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			"client writes must use primary path only", nil)
	}

	// Validate primary path is available
	err := cwr.validatePrimaryPath()
	if err != nil {
		return err
	}

	// Validate frame
	if frame == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "frame is nil", nil)
	}

	if frame.LogicalStreamId == 0 {
		return utils.NewKwikError(utils.ErrInvalidFrame, "logical stream ID is zero", nil)
	}

	return nil
}

// SendDataToPrimary sends data directly to the primary path
// This is the main method for client write operations
func (cwr *ClientWriteRouter) SendDataToPrimary(frame *data.DataFrame) error {
	// Route the write to get the target path (should always be primary)
	targetPathID, err := cwr.RouteWrite(frame)
	if err != nil {
		return err
	}

	// Validate the write operation
	err = cwr.ValidateWriteOperation(frame, targetPathID)
	if err != nil {
		return err
	}

	// Send data through the data plane to the primary path
	err = cwr.dataPlane.SendData(targetPathID, frame)
	if err != nil {
		return utils.NewKwikError(utils.ErrConnectionLost,
			"failed to send data to primary path", err)
	}

	return nil
}

// UpdatePrimaryPath updates the primary path ID
func (cwr *ClientWriteRouter) UpdatePrimaryPath(newPrimaryPathID string) error {
	if newPrimaryPathID == "" {
		return utils.NewKwikError(utils.ErrInvalidFrame, "primary path ID cannot be empty", nil)
	}

	// Validate that the new primary path exists
	oldPrimaryPathID := cwr.primaryPathID
	cwr.primaryPathID = newPrimaryPathID
	
	err := cwr.validatePrimaryPath()
	if err != nil {
		// Revert to old primary path if validation fails
		cwr.primaryPathID = oldPrimaryPathID
		return utils.NewKwikError(utils.ErrPathNotFound,
			"new primary path is not valid", err)
	}

	return nil
}

// GetPrimaryPath returns the current primary path ID
func (cwr *ClientWriteRouter) GetPrimaryPath() string {
	return cwr.primaryPathID
}

// IsWriteAllowedOnPath checks if writes are allowed on a specific path
// For clients, only the primary path allows writes
func (cwr *ClientWriteRouter) IsWriteAllowedOnPath(pathID string) bool {
	return pathID == cwr.primaryPathID
}

// GetWritePolicy returns the current write routing policy
func (cwr *ClientWriteRouter) GetWritePolicy() WriteRoutingPolicy {
	// Client write router always uses primary-only policy
	return WriteRoutingPrimaryOnly
}

// validatePrimaryPath validates that the primary path exists and is active
func (cwr *ClientWriteRouter) validatePrimaryPath() error {
	if cwr.dataPlane == nil {
		return utils.NewKwikError(utils.ErrConnectionLost, "data plane is nil", nil)
	}

	// Check if primary path is registered in data plane
	cwr.dataPlane.pathsMutex.RLock()
	stream, exists := cwr.dataPlane.pathStreams[cwr.primaryPathID]
	cwr.dataPlane.pathsMutex.RUnlock()

	if !exists {
		return utils.NewPathNotFoundError(cwr.primaryPathID)
	}

	// Check if primary path is active
	if !stream.IsActive() {
		return utils.NewPathDeadError(cwr.primaryPathID)
	}

	return nil
}

// GetWriteStatistics returns statistics about write operations
func (cwr *ClientWriteRouter) GetWriteStatistics() *ClientWriteStats {
	// Get path statistics from data plane
	pathStats, err := cwr.dataPlane.GetPathStats(cwr.primaryPathID)
	if err != nil {
		return &ClientWriteStats{
			PrimaryPathID:    cwr.primaryPathID,
			TotalWrites:      0,
			TotalBytesWritten: 0,
			WriteErrors:      0,
			LastWriteTime:    0,
		}
	}

	return &ClientWriteStats{
		PrimaryPathID:     cwr.primaryPathID,
		TotalWrites:       pathStats.PacketsSent,
		TotalBytesWritten: pathStats.BytesSent,
		WriteErrors:       0, // TODO: Track write errors
		LastWriteTime:     0, // TODO: Track last write time
	}
}

// ClientWriteStats contains statistics for client write operations
type ClientWriteStats struct {
	PrimaryPathID     string
	TotalWrites       uint64
	TotalBytesWritten uint64
	WriteErrors       uint64
	LastWriteTime     int64
}

// ServerWriteRouter implements routing logic for server writes
// This allows servers to write to their own data plane (requirement 4.4, 4.5)
type ServerWriteRouter struct {
	serverPathID string
	dataPlane    *DataPlaneImpl
	policy       WriteRoutingPolicy
}

// NewServerWriteRouter creates a new server write router
func NewServerWriteRouter(serverPathID string, dataPlane *DataPlaneImpl) *ServerWriteRouter {
	return &ServerWriteRouter{
		serverPathID: serverPathID,
		dataPlane:    dataPlane,
		policy:       WriteRoutingLoadBalanced, // Servers can use load balancing
	}
}

// RouteWrite routes a write operation for server sessions
func (swr *ServerWriteRouter) RouteWrite(frame *data.DataFrame) (string, error) {
	switch swr.policy {
	case WriteRoutingPrimaryOnly:
		// Use server's own path
		return swr.serverPathID, nil
	case WriteRoutingLoadBalanced:
		// Use scheduler to select optimal path
		if swr.dataPlane.scheduler != nil {
			return swr.dataPlane.scheduler.GetOptimalPath(frame.LogicalStreamId)
		}
		return swr.serverPathID, nil
	default:
		return swr.serverPathID, nil
	}
}

// SendDataToPath sends data to a specific path for server sessions
func (swr *ServerWriteRouter) SendDataToPath(frame *data.DataFrame, pathID string) error {
	// Validate the write operation
	err := swr.ValidateWriteOperation(frame, pathID)
	if err != nil {
		return err
	}

	// Send data through the data plane
	err = swr.dataPlane.SendData(pathID, frame)
	if err != nil {
		return utils.NewKwikError(utils.ErrConnectionLost,
			"failed to send data to path", err)
	}

	return nil
}

// ValidateWriteOperation validates that a write operation can proceed for servers
func (swr *ServerWriteRouter) ValidateWriteOperation(frame *data.DataFrame, targetPathID string) error {
	// Validate frame
	if frame == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "frame is nil", nil)
	}

	if frame.LogicalStreamId == 0 {
		return utils.NewKwikError(utils.ErrInvalidFrame, "logical stream ID is zero", nil)
	}

	// Validate target path exists
	swr.dataPlane.pathsMutex.RLock()
	stream, exists := swr.dataPlane.pathStreams[targetPathID]
	swr.dataPlane.pathsMutex.RUnlock()

	if !exists {
		return utils.NewPathNotFoundError(targetPathID)
	}

	if !stream.IsActive() {
		return utils.NewPathDeadError(targetPathID)
	}

	return nil
}

// SetWritePolicy sets the write routing policy for server sessions
func (swr *ServerWriteRouter) SetWritePolicy(policy WriteRoutingPolicy) {
	swr.policy = policy
}

// GetWritePolicy returns the current write routing policy
func (swr *ServerWriteRouter) GetWritePolicy() WriteRoutingPolicy {
	return swr.policy
}