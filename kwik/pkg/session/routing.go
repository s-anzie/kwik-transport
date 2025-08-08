package session

import (
	"sync"
	"time"

	"kwik/internal/utils"
	"kwik/pkg/transport"
)

// PathRouter handles routing decisions for KWIK sessions
// It ensures primary path is used as default for operations
type PathRouter struct {
	pathManager transport.PathManager
	primaryPath transport.Path
	mutex       sync.RWMutex
	
	// Routing configuration
	config *RoutingConfig
}

// RoutingConfig holds configuration for path routing
type RoutingConfig struct {
	// Primary path preferences
	PreferPrimaryForWrites bool
	PreferPrimaryForReads  bool
	
	// Failover configuration
	EnableAutomaticFailover bool
	FailoverTimeout         time.Duration
	
	// Load balancing for reads (when not preferring primary)
	EnableReadLoadBalancing bool
	LoadBalancingStrategy   LoadBalancingStrategy
}

// LoadBalancingStrategy defines how to distribute reads across paths
type LoadBalancingStrategy int

const (
	LoadBalancingRoundRobin LoadBalancingStrategy = iota
	LoadBalancingLeastLoaded
	LoadBalancingRandom
)

// DefaultRoutingConfig returns default routing configuration
func DefaultRoutingConfig() *RoutingConfig {
	return &RoutingConfig{
		PreferPrimaryForWrites:  true,  // Always prefer primary for writes
		PreferPrimaryForReads:   false, // Allow read load balancing
		EnableAutomaticFailover: true,
		FailoverTimeout:         5 * time.Second,
		EnableReadLoadBalancing: true,
		LoadBalancingStrategy:   LoadBalancingRoundRobin,
	}
}

// NewPathRouter creates a new path router
func NewPathRouter(pathManager transport.PathManager, config *RoutingConfig) *PathRouter {
	if config == nil {
		config = DefaultRoutingConfig()
	}
	
	router := &PathRouter{
		pathManager: pathManager,
		config:      config,
	}
	
	// Set initial primary path
	router.updatePrimaryPath()
	
	return router
}

// GetPrimaryPath returns the current primary path
func (pr *PathRouter) GetPrimaryPath() transport.Path {
	pr.mutex.RLock()
	defer pr.mutex.RUnlock()
	return pr.primaryPath
}

// SetPrimaryPath sets a new primary path
func (pr *PathRouter) SetPrimaryPath(pathID string) error {
	pr.mutex.Lock()
	defer pr.mutex.Unlock()
	
	// Validate the path exists and is active
	path := pr.pathManager.GetPath(pathID)
	if path == nil {
		return utils.NewPathNotFoundError(pathID)
	}
	
	if !path.IsActive() {
		return utils.NewPathDeadError(pathID)
	}
	
	// Update path manager's primary path designation
	err := pr.pathManager.SetPrimaryPath(pathID)
	if err != nil {
		return err
	}
	
	// Update local reference
	pr.primaryPath = path
	
	return nil
}

// GetDefaultPathForWrite returns the path to use for write operations
// According to KWIK requirements, writes should always go to primary path
func (pr *PathRouter) GetDefaultPathForWrite() (transport.Path, error) {
	pr.mutex.RLock()
	defer pr.mutex.RUnlock()
	
	// Always use primary path for writes
	if pr.primaryPath == nil || !pr.primaryPath.IsActive() {
		// Try to find a new primary path
		pr.mutex.RUnlock()
		err := pr.handlePrimaryPathFailure()
		pr.mutex.RLock()
		
		if err != nil {
			return nil, utils.NewKwikError(utils.ErrConnectionLost,
				"no active primary path available for write operation", err)
		}
	}
	
	return pr.primaryPath, nil
}

// GetDefaultPathForRead returns the path to use for read operations
// Can use primary path or load balance based on configuration
func (pr *PathRouter) GetDefaultPathForRead() (transport.Path, error) {
	pr.mutex.RLock()
	defer pr.mutex.RUnlock()
	
	// If configured to prefer primary for reads, use it
	if pr.config.PreferPrimaryForReads {
		if pr.primaryPath != nil && pr.primaryPath.IsActive() {
			return pr.primaryPath, nil
		}
	}
	
	// If load balancing is enabled, select best path
	if pr.config.EnableReadLoadBalancing {
		return pr.selectPathForRead()
	}
	
	// Fallback to primary path
	if pr.primaryPath == nil || !pr.primaryPath.IsActive() {
		return nil, utils.NewKwikError(utils.ErrConnectionLost,
			"no active path available for read operation", nil)
	}
	
	return pr.primaryPath, nil
}

// GetDefaultPathForStream returns the path to use for new stream creation
// New streams should be created on the primary path by default
func (pr *PathRouter) GetDefaultPathForStream() (transport.Path, error) {
	pr.mutex.RLock()
	defer pr.mutex.RUnlock()
	
	// Always use primary path for new streams
	if pr.primaryPath == nil || !pr.primaryPath.IsActive() {
		// Try to find a new primary path
		pr.mutex.RUnlock()
		err := pr.handlePrimaryPathFailure()
		pr.mutex.RLock()
		
		if err != nil {
			return nil, utils.NewKwikError(utils.ErrConnectionLost,
				"no active primary path available for stream creation", err)
		}
	}
	
	return pr.primaryPath, nil
}

// ValidatePathForOperation checks if a path is suitable for a specific operation
func (pr *PathRouter) ValidatePathForOperation(pathID string, operation OperationType) error {
	path := pr.pathManager.GetPath(pathID)
	if path == nil {
		return utils.NewPathNotFoundError(pathID)
	}
	
	if !path.IsActive() {
		return utils.NewPathDeadError(pathID)
	}
	
	// For write operations, ensure we're using primary path
	if operation == OperationTypeWrite {
		pr.mutex.RLock()
		isPrimary := pr.primaryPath != nil && pr.primaryPath.ID() == pathID
		pr.mutex.RUnlock()
		
		if !isPrimary {
			return utils.NewKwikError(utils.ErrInvalidFrame,
				"write operations must use primary path", nil)
		}
	}
	
	return nil
}

// HandlePathFailure handles failure of a specific path
func (pr *PathRouter) HandlePathFailure(pathID string) error {
	pr.mutex.Lock()
	defer pr.mutex.Unlock()
	
	// Check if the failed path is the primary path
	if pr.primaryPath != nil && pr.primaryPath.ID() == pathID {
		return pr.handlePrimaryPathFailure()
	}
	
	// For non-primary paths, just mark as dead
	return pr.pathManager.MarkPathDead(pathID)
}

// updatePrimaryPath updates the primary path reference from path manager
func (pr *PathRouter) updatePrimaryPath() {
	pr.mutex.Lock()
	defer pr.mutex.Unlock()
	
	pr.primaryPath = pr.pathManager.GetPrimaryPath()
}

// handlePrimaryPathFailure handles failure of the primary path
func (pr *PathRouter) handlePrimaryPathFailure() error {
	if !pr.config.EnableAutomaticFailover {
		return utils.NewKwikError(utils.ErrConnectionLost,
			"primary path failed and automatic failover is disabled", nil)
	}
	
	// Find an alternative active path to promote to primary
	activePaths := pr.pathManager.GetActivePaths()
	
	var newPrimary transport.Path
	for _, path := range activePaths {
		if path.IsActive() && !path.IsPrimary() {
			newPrimary = path
			break
		}
	}
	
	if newPrimary == nil {
		return utils.NewKwikError(utils.ErrConnectionLost,
			"no alternative active path available for primary failover", nil)
	}
	
	// Promote the new path to primary
	err := pr.pathManager.SetPrimaryPath(newPrimary.ID())
	if err != nil {
		return utils.NewKwikError(utils.ErrConnectionLost,
			"failed to promote alternative path to primary", err)
	}
	
	// Update local reference
	pr.primaryPath = newPrimary
	
	return nil
}

// selectPathForRead selects the best path for read operations based on load balancing strategy
func (pr *PathRouter) selectPathForRead() (transport.Path, error) {
	activePaths := pr.pathManager.GetActivePaths()
	
	if len(activePaths) == 0 {
		return nil, utils.NewKwikError(utils.ErrConnectionLost,
			"no active paths available", nil)
	}
	
	switch pr.config.LoadBalancingStrategy {
	case LoadBalancingRoundRobin:
		return pr.selectRoundRobinPath(activePaths)
	case LoadBalancingLeastLoaded:
		return pr.selectLeastLoadedPath(activePaths)
	case LoadBalancingRandom:
		return pr.selectRandomPath(activePaths)
	default:
		// Fallback to first active path
		return activePaths[0], nil
	}
}

// selectRoundRobinPath selects path using round-robin strategy
func (pr *PathRouter) selectRoundRobinPath(paths []transport.Path) (transport.Path, error) {
	// Simple round-robin implementation
	// In a real implementation, this would maintain state for round-robin selection
	if len(paths) > 0 {
		return paths[0], nil
	}
	return nil, utils.NewKwikError(utils.ErrConnectionLost, "no paths available", nil)
}

// selectLeastLoadedPath selects the path with least load
func (pr *PathRouter) selectLeastLoadedPath(paths []transport.Path) (transport.Path, error) {
	// Simple implementation - select path with best health score
	var bestPath transport.Path
	var bestScore float64 = -1
	
	for _, path := range paths {
		metrics, err := pr.pathManager.GetPathHealthMetrics(path.ID())
		if err != nil {
			continue
		}
		
		if metrics.HealthScore > bestScore {
			bestScore = metrics.HealthScore
			bestPath = path
		}
	}
	
	if bestPath == nil {
		return paths[0], nil // Fallback to first path
	}
	
	return bestPath, nil
}

// selectRandomPath selects a random path
func (pr *PathRouter) selectRandomPath(paths []transport.Path) (transport.Path, error) {
	// Simple implementation - return first path
	// In a real implementation, this would use proper randomization
	if len(paths) > 0 {
		return paths[0], nil
	}
	return nil, utils.NewKwikError(utils.ErrConnectionLost, "no paths available", nil)
}

// OperationType defines the type of operation being performed
type OperationType int

const (
	OperationTypeRead OperationType = iota
	OperationTypeWrite
	OperationTypeStream
	OperationTypeControl
)

// PathSelectionResult contains the result of path selection
type PathSelectionResult struct {
	Path   transport.Path
	Reason string
	IsPrimary bool
}

// SelectPathForOperation selects the appropriate path for a specific operation
func (pr *PathRouter) SelectPathForOperation(operation OperationType) (*PathSelectionResult, error) {
	var selectedPath transport.Path
	var err error
	var reason string
	
	switch operation {
	case OperationTypeWrite:
		selectedPath, err = pr.GetDefaultPathForWrite()
		reason = "write operations use primary path"
	case OperationTypeRead:
		selectedPath, err = pr.GetDefaultPathForRead()
		reason = "read operation path selection"
	case OperationTypeStream:
		selectedPath, err = pr.GetDefaultPathForStream()
		reason = "new streams created on primary path"
	case OperationTypeControl:
		selectedPath, err = pr.GetDefaultPathForWrite() // Control messages use primary
		reason = "control messages use primary path"
	default:
		return nil, utils.NewKwikError(utils.ErrInvalidFrame,
			"unknown operation type", nil)
	}
	
	if err != nil {
		return nil, err
	}
	
	isPrimary := false
	pr.mutex.RLock()
	if pr.primaryPath != nil && selectedPath.ID() == pr.primaryPath.ID() {
		isPrimary = true
	}
	pr.mutex.RUnlock()
	
	return &PathSelectionResult{
		Path:      selectedPath,
		Reason:    reason,
		IsPrimary: isPrimary,
	}, nil
}

// GetRoutingStatistics returns statistics about path routing
func (pr *PathRouter) GetRoutingStatistics() *RoutingStatistics {
	pr.mutex.RLock()
	defer pr.mutex.RUnlock()
	
	stats := &RoutingStatistics{
		PrimaryPathID: "",
		TotalPaths:    pr.pathManager.GetPathCount(),
		ActivePaths:   pr.pathManager.GetActivePathCount(),
		DeadPaths:     pr.pathManager.GetDeadPathCount(),
	}
	
	if pr.primaryPath != nil {
		stats.PrimaryPathID = pr.primaryPath.ID()
		stats.PrimaryPathActive = pr.primaryPath.IsActive()
	}
	
	return stats
}

// RoutingStatistics contains statistics about path routing
type RoutingStatistics struct {
	PrimaryPathID     string
	PrimaryPathActive bool
	TotalPaths        int
	ActivePaths       int
	DeadPaths         int
}