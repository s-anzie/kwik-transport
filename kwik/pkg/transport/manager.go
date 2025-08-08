package transport

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"kwik/internal/utils"
)

// pathManager implements the PathManager interface
type pathManager struct {
	paths       map[string]*path
	activePaths map[string]*path
	deadPaths   map[string]*path
	mutex       sync.RWMutex

	// Health monitoring system
	healthMonitoringActive bool
	healthCheckInterval    time.Duration
	failureThreshold       int
	notificationHandler    PathStatusNotificationHandler
	healthTicker           *time.Ticker
	healthStopChan         chan struct{}
	healthMutex            sync.RWMutex
}

// NewPathManager creates a new path manager
func NewPathManager() PathManager {
	return &pathManager{
		paths:       make(map[string]*path),
		activePaths: make(map[string]*path),
		deadPaths:   make(map[string]*path),
	}
}

// CreatePath creates a new path to the specified address
func (pm *pathManager) CreatePath(address string) (Path, error) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	// Generate unique path ID
	pathID := generatePathID()

	// Create QUIC connection with timeout
	ctx, cancel := context.WithTimeout(context.Background(), utils.DefaultDialTimeout)
	defer cancel()

	// Establish QUIC connection to the server
	conn, err := quic.DialAddr(ctx, address, generateClientTLSConfig(), nil)
	if err != nil {
		return nil, utils.NewKwikError(utils.ErrConnectionLost,
			fmt.Sprintf("failed to establish QUIC connection to %s", address), err)
	}

	// Create path with actual QUIC connection
	p := &path{
		id:           pathID,
		address:      address,
		isPrimary:    len(pm.activePaths) == 0, // First path is primary
		state:        PathStateActive,
		connection:   conn,
		createdAt:    time.Now(),
		lastActivity: time.Now(),
	}

	// Create connection wrapper with path reference
	p.wrapper = newConnectionWrapper(conn, p)

	// Establish control plane stream automatically
	err = pm.establishControlPlaneStream(p)
	if err != nil {
		// Close connection if control stream establishment fails
		conn.CloseWithError(0, "failed to establish control plane stream")
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"failed to establish control plane stream", err)
	}

	pm.paths[pathID] = p
	pm.activePaths[pathID] = p

	// Return interface directly
	return p, nil
}

// CreatePathFromConnection creates a path from an existing QUIC connection
func (pm *pathManager) CreatePathFromConnection(conn quic.Connection) (Path, error) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	pathID := generatePathID()

	p := &path{
		id:           pathID,
		address:      conn.RemoteAddr().String(),
		isPrimary:    len(pm.activePaths) == 0,
		state:        PathStateActive,
		connection:   conn,
		createdAt:    time.Now(),
		lastActivity: time.Now(),
	}

	// Create connection wrapper with path reference
	p.wrapper = newConnectionWrapper(conn, p)

	pm.paths[pathID] = p
	pm.activePaths[pathID] = p

	return p, nil
}

// RemovePath removes a path
func (pm *pathManager) RemovePath(pathID string) error {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	p, exists := pm.paths[pathID]
	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}

	// Close the path
	p.Close()

	// Move from active to dead
	delete(pm.activePaths, pathID)
	pm.deadPaths[pathID] = p
	p.SetState(PathStateDead)

	return nil
}

// GetPath retrieves a path by ID
func (pm *pathManager) GetPath(pathID string) Path {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	if p, exists := pm.paths[pathID]; exists {
		return p
	}
	return nil
}

// GetActivePaths returns all active paths
func (pm *pathManager) GetActivePaths() []Path {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	paths := make([]Path, 0, len(pm.activePaths))
	for _, p := range pm.activePaths {
		paths = append(paths, p)
	}
	return paths
}

// GetDeadPaths returns all dead paths
func (pm *pathManager) GetDeadPaths() []Path {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	paths := make([]Path, 0, len(pm.deadPaths))
	for _, p := range pm.deadPaths {
		paths = append(paths, p)
	}
	return paths
}

// MarkPathDead marks a path as dead
func (pm *pathManager) MarkPathDead(pathID string) error {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	p, exists := pm.activePaths[pathID]
	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}

	// Move from active to dead
	delete(pm.activePaths, pathID)
	pm.deadPaths[pathID] = p
	p.SetState(PathStateDead)

	return nil
}

// Advanced path control methods

// GetPathCount returns the total number of paths (active + dead)
func (pm *pathManager) GetPathCount() int {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()
	return len(pm.paths)
}

// GetActivePathCount returns the number of active paths
func (pm *pathManager) GetActivePathCount() int {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()
	return len(pm.activePaths)
}

// GetDeadPathCount returns the number of dead paths
func (pm *pathManager) GetDeadPathCount() int {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()
	return len(pm.deadPaths)
}

// GetPrimaryPath returns the primary path
func (pm *pathManager) GetPrimaryPath() Path {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	for _, p := range pm.activePaths {
		if p.IsPrimary() {
			return p
		}
	}
	return nil
}

// SetPrimaryPath sets a path as the primary path
func (pm *pathManager) SetPrimaryPath(pathID string) error {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	newPrimary, exists := pm.activePaths[pathID]
	if !exists {
		return utils.NewPathNotFoundError(pathID)
	}

	// Remove primary flag from current primary path
	for _, p := range pm.activePaths {
		if p.IsPrimary() {
			p.isPrimary = false
		}
	}

	// Set new primary path
	newPrimary.isPrimary = true
	newPrimary.UpdateActivity()

	return nil
}

// CleanupDeadPaths removes dead paths from memory and returns count of cleaned paths
func (pm *pathManager) CleanupDeadPaths() int {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	count := 0
	for pathID, p := range pm.deadPaths {
		// Close the path if not already closed
		p.Close()

		// Remove from all maps
		delete(pm.deadPaths, pathID)
		delete(pm.paths, pathID)
		count++
	}

	return count
}

// GetPathsByState returns all paths in a specific state
func (pm *pathManager) GetPathsByState(state PathState) []Path {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	var paths []Path
	for _, p := range pm.paths {
		if p.GetState() == state {
			paths = append(paths, p)
		}
	}

	return paths
}

// ValidatePathHealth checks all paths and returns list of unhealthy path IDs
func (pm *pathManager) ValidatePathHealth() []string {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	var unhealthyPaths []string
	for pathID, p := range pm.activePaths {
		if !p.IsHealthy() {
			unhealthyPaths = append(unhealthyPaths, pathID)
		}
	}

	return unhealthyPaths
}

// Close closes all paths and cleans up the path manager
func (pm *pathManager) Close() error {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	// Close all active paths
	for _, p := range pm.activePaths {
		p.Close()
	}

	// Close all dead paths
	for _, p := range pm.deadPaths {
		p.Close()
	}

	// Clear all maps
	pm.paths = make(map[string]*path)
	pm.activePaths = make(map[string]*path)
	pm.deadPaths = make(map[string]*path)

	return nil
}

// Additional lifecycle management methods

// AutoCleanupDeadPaths automatically removes dead paths older than the specified duration
func (pm *pathManager) AutoCleanupDeadPaths(maxAge time.Duration) int {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	count := 0
	now := time.Now()

	for pathID, p := range pm.deadPaths {
		if now.Sub(p.GetLastActivity()) > maxAge {
			// Close the path if not already closed
			p.Close()

			// Remove from all maps
			delete(pm.deadPaths, pathID)
			delete(pm.paths, pathID)
			count++
		}
	}

	return count
}

// GetPathStatistics returns detailed statistics about paths
func (pm *pathManager) GetPathStatistics() PathStatistics {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	stats := PathStatistics{
		TotalPaths:  len(pm.paths),
		ActivePaths: len(pm.activePaths),
		DeadPaths:   len(pm.deadPaths),
		StateCount:  make(map[PathState]int),
	}

	// Count paths by state
	for _, p := range pm.paths {
		state := p.GetState()
		stats.StateCount[state]++
	}

	// Find primary path
	for _, p := range pm.activePaths {
		if p.IsPrimary() {
			stats.PrimaryPathID = p.ID()
			break
		}
	}

	return stats
}

// PathStatistics contains detailed statistics about path management
type PathStatistics struct {
	TotalPaths    int
	ActivePaths   int
	DeadPaths     int
	PrimaryPathID string
	StateCount    map[PathState]int
}

// ValidatePathIntegrity performs comprehensive validation of path manager state
func (pm *pathManager) ValidatePathIntegrity() []string {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	var issues []string

	// Check for orphaned paths
	for pathID := range pm.paths {
		if _, inActive := pm.activePaths[pathID]; !inActive {
			if _, inDead := pm.deadPaths[pathID]; !inDead {
				issues = append(issues, fmt.Sprintf("orphaned path: %s", pathID))
			}
		}
	}

	// Check for duplicate primary paths
	primaryCount := 0
	for _, p := range pm.activePaths {
		if p.IsPrimary() {
			primaryCount++
		}
	}

	if primaryCount == 0 && len(pm.activePaths) > 0 {
		issues = append(issues, "no primary path found among active paths")
	} else if primaryCount > 1 {
		issues = append(issues, fmt.Sprintf("multiple primary paths found: %d", primaryCount))
	}

	// Check for inconsistent states
	for pathID, p := range pm.activePaths {
		if p.GetState() != PathStateActive {
			issues = append(issues, fmt.Sprintf("active path %s has non-active state: %s", pathID, p.GetState()))
		}
	}

	for pathID, p := range pm.deadPaths {
		if p.GetState() == PathStateActive {
			issues = append(issues, fmt.Sprintf("dead path %s has active state", pathID))
		}
	}

	return issues
}

// Path failure detection and notification system implementation

// StartHealthMonitoring starts the health monitoring system
func (pm *pathManager) StartHealthMonitoring() error {
	pm.healthMutex.Lock()
	defer pm.healthMutex.Unlock()

	if pm.healthMonitoringActive {
		return nil // Already running
	}

	// Set default values if not configured
	if pm.healthCheckInterval == 0 {
		pm.healthCheckInterval = utils.PathHealthCheckInterval
	}
	if pm.failureThreshold == 0 {
		pm.failureThreshold = 3 // Default failure threshold
	}

	pm.healthMonitoringActive = true
	pm.healthStopChan = make(chan struct{})
	pm.healthTicker = time.NewTicker(pm.healthCheckInterval)

	// Start monitoring goroutine
	go pm.healthMonitoringLoop()

	return nil
}

// StopHealthMonitoring stops the health monitoring system
func (pm *pathManager) StopHealthMonitoring() error {
	pm.healthMutex.Lock()
	defer pm.healthMutex.Unlock()

	if !pm.healthMonitoringActive {
		return nil // Already stopped
	}

	pm.healthMonitoringActive = false

	if pm.healthTicker != nil {
		pm.healthTicker.Stop()
		pm.healthTicker = nil
	}

	if pm.healthStopChan != nil {
		close(pm.healthStopChan)
		pm.healthStopChan = nil
	}

	return nil
}

// SetPathStatusNotificationHandler sets the notification handler
func (pm *pathManager) SetPathStatusNotificationHandler(handler PathStatusNotificationHandler) {
	pm.healthMutex.Lock()
	defer pm.healthMutex.Unlock()
	pm.notificationHandler = handler
}

// GetPathHealthMetrics returns health metrics for a specific path
func (pm *pathManager) GetPathHealthMetrics(pathID string) (*PathHealthMetrics, error) {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	p, exists := pm.paths[pathID]
	if !exists {
		return nil, utils.NewPathNotFoundError(pathID)
	}

	// Calculate health score based on various factors
	healthScore := pm.calculateHealthScore(p)

	// Get connection state
	connectionState := "unknown"
	if p.GetConnection() != nil {
		select {
		case <-p.GetConnection().Context().Done():
			connectionState = "closed"
		default:
			connectionState = "open"
		}
	}

	metrics := &PathHealthMetrics{
		PathID:              pathID,
		LastActivity:        p.GetLastActivity(),
		ErrorCount:          p.GetErrorCount(),
		LastError:           p.GetLastError(),
		ResponseTime:        pm.calculateResponseTime(p),
		PacketLossRate:      pm.calculatePacketLossRate(p),
		ConnectionState:     connectionState,
		HealthScore:         healthScore,
		ConsecutiveFailures: pm.getConsecutiveFailures(p),
	}

	return metrics, nil
}

// SetHealthCheckInterval sets the health check interval
func (pm *pathManager) SetHealthCheckInterval(interval time.Duration) {
	pm.healthMutex.Lock()
	defer pm.healthMutex.Unlock()

	pm.healthCheckInterval = interval

	// Restart ticker if monitoring is active
	if pm.healthMonitoringActive && pm.healthTicker != nil {
		pm.healthTicker.Stop()
		pm.healthTicker = time.NewTicker(interval)
	}
}

// SetFailureThreshold sets the failure threshold
func (pm *pathManager) SetFailureThreshold(threshold int) {
	pm.healthMutex.Lock()
	defer pm.healthMutex.Unlock()
	pm.failureThreshold = threshold
}

// healthMonitoringLoop runs the health monitoring in background
func (pm *pathManager) healthMonitoringLoop() {
	for {
		select {
		case <-pm.healthTicker.C:
			pm.performHealthCheck()
		case <-pm.healthStopChan:
			return
		}
	}
}

// performHealthCheck performs a comprehensive health check on all paths
func (pm *pathManager) performHealthCheck() {
	pm.mutex.RLock()
	activePaths := make([]*path, 0, len(pm.activePaths))
	for _, p := range pm.activePaths {
		activePaths = append(activePaths, p)
	}
	pm.mutex.RUnlock()

	for _, p := range activePaths {
		pm.checkPathHealth(p)
	}
}

// checkPathHealth checks the health of a single path
func (pm *pathManager) checkPathHealth(p *path) {
	pathID := p.ID()
	oldState := p.GetState()

	// Check if path is healthy
	isHealthy := p.IsHealthy()

	// Check for timeout (no activity for too long)
	timeSinceActivity := time.Since(p.GetLastActivity())
	isTimedOut := timeSinceActivity > utils.DeadPathTimeout

	// Check error count threshold
	errorCount := p.GetErrorCount()
	hasExcessiveErrors := errorCount >= pm.failureThreshold

	// Determine if path should be marked as dead
	shouldMarkDead := !isHealthy || isTimedOut || hasExcessiveErrors

	if shouldMarkDead && oldState == PathStateActive {
		// Mark path as dead
		pm.MarkPathDead(pathID)

		// Get metrics for notification
		metrics, _ := pm.GetPathHealthMetrics(pathID)

		// Determine failure reason
		reason := "unknown"
		if !isHealthy {
			reason = "connection_unhealthy"
		} else if isTimedOut {
			reason = "timeout"
		} else if hasExcessiveErrors {
			reason = "excessive_errors"
		}

		// Send notifications
		pm.sendNotifications(pathID, oldState, PathStateDead, reason, metrics)
	}
}

// sendNotifications sends notifications to the registered handler
func (pm *pathManager) sendNotifications(pathID string, oldState, newState PathState, reason string, metrics *PathHealthMetrics) {
	pm.healthMutex.RLock()
	handler := pm.notificationHandler
	pm.healthMutex.RUnlock()

	if handler == nil {
		return // No handler registered
	}

	// Send status change notification
	handler.OnPathStatusChanged(pathID, oldState, newState, metrics)

	// Send specific failure notification
	if newState == PathStateDead {
		handler.OnPathFailureDetected(pathID, reason, metrics)
	} else if oldState == PathStateDead && newState == PathStateActive {
		handler.OnPathRecovered(pathID, metrics)
	}
}

// Helper methods for health metrics calculation

// calculateHealthScore calculates a health score (0.0 to 1.0) for a path
func (pm *pathManager) calculateHealthScore(p *path) float64 {
	if !p.IsHealthy() {
		return 0.0
	}

	// Base score
	score := 1.0

	// Reduce score based on error count
	errorCount := p.GetErrorCount()
	if errorCount > 0 {
		score -= float64(errorCount) * 0.1
		if score < 0.1 {
			score = 0.1
		}
	}

	// Reduce score based on time since last activity
	timeSinceActivity := time.Since(p.GetLastActivity())
	if timeSinceActivity > time.Minute {
		score -= 0.2
	}

	if score < 0.0 {
		score = 0.0
	}

	return score
}

// calculateResponseTime calculates estimated response time for a path
func (pm *pathManager) calculateResponseTime(p *path) time.Duration {
	// TODO: Implement actual response time measurement
	// For now, return a placeholder based on error count
	errorCount := p.GetErrorCount()
	baseTime := 50 * time.Millisecond

	if errorCount > 0 {
		return baseTime + time.Duration(errorCount)*10*time.Millisecond
	}

	return baseTime
}

// calculatePacketLossRate calculates estimated packet loss rate for a path
func (pm *pathManager) calculatePacketLossRate(p *path) float64 {
	// TODO: Implement actual packet loss measurement
	// For now, return a placeholder based on error count
	errorCount := p.GetErrorCount()

	if errorCount == 0 {
		return 0.0
	}

	// Estimate loss rate based on error count
	lossRate := float64(errorCount) * 0.01
	if lossRate > 1.0 {
		lossRate = 1.0
	}

	return lossRate
}

// getConsecutiveFailures gets the number of consecutive failures for a path
func (pm *pathManager) getConsecutiveFailures(p *path) int {
	// TODO: Implement actual consecutive failure tracking
	// For now, return error count as approximation
	return p.GetErrorCount()
}

// establishControlPlaneStream establishes the control plane stream for a path
func (pm *pathManager) establishControlPlaneStream(p *path) error {
	if p.wrapper == nil {
		return utils.NewKwikError(utils.ErrConnectionLost, "path wrapper is nil", nil)
	}
	
	// Create control plane stream using the connection wrapper
	controlStream, err := p.wrapper.CreateControlStream()
	if err != nil {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			"failed to create control plane stream", err)
	}
	
	// TODO: Send initial handshake/authentication message over control stream
	// This would include:
	// 1. Protocol version negotiation
	// 2. Session ID exchange
	// 3. Authentication if required
	// 4. Path capabilities advertisement
	
	// For now, just ensure the stream is established
	_ = controlStream
	
	return nil
}

// generateClientTLSConfig generates a TLS config for client connections
func generateClientTLSConfig() *tls.Config {
	// TODO: Implement proper TLS configuration for production
	// This should include:
	// 1. Proper certificate validation
	// 2. ALPN protocol negotiation
	// 3. Cipher suite configuration
	// 4. Certificate pinning if required
	
	return &tls.Config{
		InsecureSkipVerify: true, // For development only
		NextProtos:         []string{"kwik"}, // ALPN for KWIK protocol
	}
}

// generatePathID generates a unique path identifier
func generatePathID() string {
	bytes := make([]byte, 8)
	rand.Read(bytes)
	return fmt.Sprintf("path-%x", bytes)
}
