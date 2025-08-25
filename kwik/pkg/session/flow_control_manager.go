package session

import (
	"context"
	"fmt"
	"sync"
	"time"

	"kwik/pkg/presentation"
)

// FlowControlManager manages flow control for KWIK sessions
type FlowControlManager struct {
	// Session information
	sessionID string
	isClient  bool
	
	// Window management
	receiveWindowManager presentation.ReceiveWindowManager
	sendWindowManager    *SendWindowManager
	
	// Path-based window allocation
	pathWindows      map[string]*PathWindowState
	pathWindowsMutex sync.RWMutex
	
	// Control plane communication
	controlPlane ControlPlaneInterface
	
	// Configuration
	config *FlowControlConfig
	
	// Statistics
	stats      *FlowControlStats
	statsMutex sync.RWMutex
	
	// Control
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	
	// Callbacks
	windowUpdateCallback    func(pathID string, windowSize uint64)
	backpressureCallback    func(pathID string, active bool)
	windowFullCallback      func()
	windowAvailableCallback func()
	
	// Logger
	logger SessionLogger
}

// SendWindowManager manages send window for outgoing data
type SendWindowManager struct {
	// Total send window size
	totalWindow     uint64
	availableWindow uint64
	windowMutex     sync.RWMutex
	
	// Per-path send windows
	pathWindows      map[string]*PathSendWindow
	pathWindowsMutex sync.RWMutex
	
	// Pending data tracking
	pendingData      map[string]uint64 // pathID -> pending bytes
	pendingDataMutex sync.RWMutex
	
	// Configuration
	config *SendWindowConfig
}

// PathWindowState tracks window state for a specific path
type PathWindowState struct {
	PathID           string
	AllocatedWindow  uint64
	UsedWindow       uint64
	LastUpdate       time.Time
	IsBackpressured  bool
	WindowUpdates    uint64
	TotalDataSent    uint64
	TotalDataReceived uint64
	mutex            sync.RWMutex
}

// PathSendWindow tracks send window for a specific path
type PathSendWindow struct {
	PathID          string
	WindowSize      uint64
	UsedWindow      uint64
	LastAck         time.Time
	PendingBytes    uint64
	RTT             time.Duration
	WindowUpdates   uint64
	mutex           sync.RWMutex
}

// FlowControlConfig contains configuration for flow control
type FlowControlConfig struct {
	// Receive window configuration
	InitialReceiveWindow    uint64        `json:"initial_receive_window"`
	MaxReceiveWindow        uint64        `json:"max_receive_window"`
	MinReceiveWindow        uint64        `json:"min_receive_window"`
	
	// Send window configuration
	InitialSendWindow       uint64        `json:"initial_send_window"`
	MaxSendWindow           uint64        `json:"max_send_window"`
	MinSendWindow           uint64        `json:"min_send_window"`
	
	// Window update thresholds
	WindowUpdateThreshold   float64       `json:"window_update_threshold"`   // Send update when window drops below this ratio
	WindowRefreshInterval   time.Duration `json:"window_refresh_interval"`   // Periodic window refresh interval
	
	// Path-based allocation
	EnablePathWindows       bool          `json:"enable_path_windows"`
	PathWindowAllocation    string        `json:"path_window_allocation"`    // "equal", "proportional", "adaptive"
	MinPathWindow           uint64        `json:"min_path_window"`
	
	// Backpressure configuration
	BackpressureThreshold   float64       `json:"backpressure_threshold"`
	BackpressureTimeout     time.Duration `json:"backpressure_timeout"`
	
	// Performance tuning
	EnableWindowScaling     bool          `json:"enable_window_scaling"`
	WindowScalingFactor     float64       `json:"window_scaling_factor"`
	AdaptiveWindowEnabled   bool          `json:"adaptive_window_enabled"`
}

// SendWindowConfig contains configuration for send window management
type SendWindowConfig struct {
	InitialWindow     uint64        `json:"initial_window"`
	MaxWindow         uint64        `json:"max_window"`
	MinWindow         uint64        `json:"min_window"`
	GrowthFactor      float64       `json:"growth_factor"`
	ShrinkFactor      float64       `json:"shrink_factor"`
	RTTThreshold      time.Duration `json:"rtt_threshold"`
	UpdateInterval    time.Duration `json:"update_interval"`
}

// FlowControlStats contains flow control statistics
type FlowControlStats struct {
	// Window statistics
	TotalWindowUpdates     uint64    `json:"total_window_updates"`
	WindowUpdatesSent      uint64    `json:"window_updates_sent"`
	WindowUpdatesReceived  uint64    `json:"window_updates_received"`
	
	// Backpressure statistics
	BackpressureEvents     uint64    `json:"backpressure_events"`
	BackpressureDuration   time.Duration `json:"backpressure_duration"`
	
	// Data flow statistics
	TotalDataSent          uint64    `json:"total_data_sent"`
	TotalDataReceived      uint64    `json:"total_data_received"`
	DataBlocked            uint64    `json:"data_blocked"`
	
	// Path statistics
	ActivePaths            int       `json:"active_paths"`
	PathWindowUpdates      map[string]uint64 `json:"path_window_updates"`
	
	// Performance metrics
	AverageWindowUtilization float64 `json:"average_window_utilization"`
	PeakWindowUtilization    float64 `json:"peak_window_utilization"`
	
	LastUpdate             time.Time `json:"last_update"`
}

// ControlPlaneInterface defines the interface for control plane communication
type ControlPlaneInterface interface {
	SendWindowUpdate(pathID string, windowSize uint64) error
	SendBackpressureSignal(pathID string, active bool) error
	RegisterWindowUpdateHandler(handler func(pathID string, windowSize uint64))
	RegisterBackpressureHandler(handler func(pathID string, active bool))
}



// DefaultFlowControlConfig returns default flow control configuration
func DefaultFlowControlConfig() *FlowControlConfig {
	return &FlowControlConfig{
		InitialReceiveWindow:    1024 * 1024,      // 1MB
		MaxReceiveWindow:        16 * 1024 * 1024, // 16MB
		MinReceiveWindow:        64 * 1024,        // 64KB
		InitialSendWindow:       1024 * 1024,      // 1MB
		MaxSendWindow:           16 * 1024 * 1024, // 16MB
		MinSendWindow:           64 * 1024,        // 64KB
		WindowUpdateThreshold:   0.5,              // Send update when 50% consumed
		WindowRefreshInterval:   5 * time.Second,
		EnablePathWindows:       true,
		PathWindowAllocation:    "adaptive",
		MinPathWindow:           32 * 1024,        // 32KB
		BackpressureThreshold:   0.9,              // 90% utilization
		BackpressureTimeout:     30 * time.Second,
		EnableWindowScaling:     true,
		WindowScalingFactor:     1.5,
		AdaptiveWindowEnabled:   true,
	}
}

// DefaultSendWindowConfig returns default send window configuration
func DefaultSendWindowConfig() *SendWindowConfig {
	return &SendWindowConfig{
		InitialWindow:  1024 * 1024,      // 1MB
		MaxWindow:      16 * 1024 * 1024, // 16MB
		MinWindow:      64 * 1024,        // 64KB
		GrowthFactor:   1.5,
		ShrinkFactor:   0.8,
		RTTThreshold:   100 * time.Millisecond,
		UpdateInterval: 1 * time.Second,
	}
}

// NewFlowControlManager creates a new flow control manager
func NewFlowControlManager(sessionID string, isClient bool, receiveWindowManager presentation.ReceiveWindowManager, 
	controlPlane ControlPlaneInterface, config *FlowControlConfig, logger SessionLogger) *FlowControlManager {
	
	if config == nil {
		config = DefaultFlowControlConfig()
	}
	
	ctx, cancel := context.WithCancel(context.Background())
	
	fcm := &FlowControlManager{
		sessionID:            sessionID,
		isClient:             isClient,
		receiveWindowManager: receiveWindowManager,
		sendWindowManager:    NewSendWindowManager(DefaultSendWindowConfig()),
		pathWindows:          make(map[string]*PathWindowState),
		controlPlane:         controlPlane,
		config:               config,
		stats: &FlowControlStats{
			PathWindowUpdates: make(map[string]uint64),
			LastUpdate:        time.Now(),
		},
		ctx:    ctx,
		cancel: cancel,
		logger: logger,
	}
	
	// Set up receive window callbacks
	if receiveWindowManager != nil {
		receiveWindowManager.SetWindowFullCallback(fcm.onReceiveWindowFull)
		receiveWindowManager.SetWindowAvailableCallback(fcm.onReceiveWindowAvailable)
	}
	
	// Register control plane handlers
	if controlPlane != nil {
		controlPlane.RegisterWindowUpdateHandler(fcm.handleWindowUpdate)
		controlPlane.RegisterBackpressureHandler(fcm.handleBackpressureSignal)
	}
	
	return fcm
}

// NewSendWindowManager creates a new send window manager
func NewSendWindowManager(config *SendWindowConfig) *SendWindowManager {
	return &SendWindowManager{
		totalWindow:   config.InitialWindow,
		availableWindow: config.InitialWindow,
		pathWindows:   make(map[string]*PathSendWindow),
		pendingData:   make(map[string]uint64),
		config:        config,
	}
}

// Start starts the flow control manager
func (fcm *FlowControlManager) Start() error {
	// Start periodic window refresh
	fcm.wg.Add(1)
	go fcm.windowRefreshWorker()
	
	// Start adaptive window management if enabled
	if fcm.config.AdaptiveWindowEnabled {
		fcm.wg.Add(1)
		go fcm.adaptiveWindowWorker()
	}
	
	// Start send window management
	fcm.wg.Add(1)
	go fcm.sendWindowWorker()
	
	return nil
}

// Stop stops the flow control manager
func (fcm *FlowControlManager) Stop() error {
	fcm.cancel()
	fcm.wg.Wait()
	return nil
}

// AddPath adds a new path to flow control management
func (fcm *FlowControlManager) AddPath(pathID string) error {
	fcm.pathWindowsMutex.Lock()
	defer fcm.pathWindowsMutex.Unlock()
	
	if _, exists := fcm.pathWindows[pathID]; exists {
		return nil // Path already exists
	}
	
	// Calculate initial window allocation for this path
	initialWindow := fcm.calculateInitialPathWindow()
	
	pathWindow := &PathWindowState{
		PathID:          pathID,
		AllocatedWindow: initialWindow,
		UsedWindow:      0,
		LastUpdate:      time.Now(),
		IsBackpressured: false,
	}
	
	fcm.pathWindows[pathID] = pathWindow
	
	// Add to send window manager
	fcm.sendWindowManager.AddPath(pathID, initialWindow)
	
	// Update statistics
	fcm.statsMutex.Lock()
	fcm.stats.ActivePaths = len(fcm.pathWindows)
	fcm.stats.PathWindowUpdates[pathID] = 0
	fcm.statsMutex.Unlock()
	
	// Send initial window update to peer
	if fcm.controlPlane != nil {
		fcm.controlPlane.SendWindowUpdate(pathID, initialWindow)
	}
	
	fcm.logger.Debug("Added path to flow control", "pathID", pathID, "initialWindow", initialWindow)
	
	return nil
}

// RemovePath removes a path from flow control management
func (fcm *FlowControlManager) RemovePath(pathID string) error {
	fcm.pathWindowsMutex.Lock()
	defer fcm.pathWindowsMutex.Unlock()
	
	delete(fcm.pathWindows, pathID)
	
	// Remove from send window manager
	fcm.sendWindowManager.RemovePath(pathID)
	
	// Update statistics
	fcm.statsMutex.Lock()
	fcm.stats.ActivePaths = len(fcm.pathWindows)
	delete(fcm.stats.PathWindowUpdates, pathID)
	fcm.statsMutex.Unlock()
	
	fcm.logger.Debug("Removed path from flow control", "pathID", pathID)
	
	return nil
}

// CanSendData checks if data can be sent on a specific path
func (fcm *FlowControlManager) CanSendData(pathID string, dataSize uint64) bool {
	return fcm.sendWindowManager.CanSendData(pathID, dataSize)
}

// ReserveWindow reserves window space for sending data
func (fcm *FlowControlManager) ReserveWindow(pathID string, dataSize uint64) error {
	return fcm.sendWindowManager.ReserveWindow(pathID, dataSize)
}

// ConsumeWindow consumes window space when data is sent
func (fcm *FlowControlManager) ConsumeWindow(pathID string, dataSize uint64) error {
	err := fcm.sendWindowManager.ConsumeWindow(pathID, dataSize)
	if err != nil {
		return err
	}
	
	// Update path statistics
	fcm.pathWindowsMutex.RLock()
	pathWindow, exists := fcm.pathWindows[pathID]
	fcm.pathWindowsMutex.RUnlock()
	
	if exists {
		pathWindow.mutex.Lock()
		pathWindow.TotalDataSent += dataSize
		pathWindow.mutex.Unlock()
	}
	
	// Update global statistics
	fcm.statsMutex.Lock()
	fcm.stats.TotalDataSent += dataSize
	fcm.statsMutex.Unlock()
	
	return nil
}

// OnDataReceived should be called when data is received
func (fcm *FlowControlManager) OnDataReceived(pathID string, dataSize uint64) error {
	// Allocate receive window space
	if fcm.receiveWindowManager != nil {
		// Use stream ID 0 for session-level flow control
		err := fcm.receiveWindowManager.AllocateWindow(0, dataSize)
		if err != nil {
			// Window is full, trigger backpressure
			fcm.triggerBackpressure(pathID, true)
			return err
		}
	}
	
	// Update path statistics
	fcm.pathWindowsMutex.RLock()
	pathWindow, exists := fcm.pathWindows[pathID]
	fcm.pathWindowsMutex.RUnlock()
	
	if exists {
		pathWindow.mutex.Lock()
		pathWindow.TotalDataReceived += dataSize
		pathWindow.UsedWindow += dataSize
		pathWindow.LastUpdate = time.Now()
		pathWindow.mutex.Unlock()
		
		// Check if window update is needed
		fcm.checkWindowUpdate(pathID, pathWindow)
	}
	
	// Update global statistics
	fcm.statsMutex.Lock()
	fcm.stats.TotalDataReceived += dataSize
	fcm.statsMutex.Unlock()
	
	return nil
}

// OnDataConsumed should be called when received data is consumed by the application
func (fcm *FlowControlManager) OnDataConsumed(pathID string, dataSize uint64) error {
	// Release receive window space
	if fcm.receiveWindowManager != nil {
		fcm.receiveWindowManager.ReleaseWindow(0, dataSize)
		fcm.receiveWindowManager.SlideWindow(dataSize)
	}
	
	// Update path window
	fcm.pathWindowsMutex.RLock()
	pathWindow, exists := fcm.pathWindows[pathID]
	fcm.pathWindowsMutex.RUnlock()
	
	if exists {
		pathWindow.mutex.Lock()
		if pathWindow.UsedWindow >= dataSize {
			pathWindow.UsedWindow -= dataSize
		} else {
			pathWindow.UsedWindow = 0
		}
		pathWindow.LastUpdate = time.Now()
		pathWindow.mutex.Unlock()
		
		// Check if window update is needed
		fcm.checkWindowUpdate(pathID, pathWindow)
		
		// Check if backpressure can be released
		if pathWindow.IsBackpressured {
			fcm.checkBackpressureRelease(pathID, pathWindow)
		}
	}
	
	return nil
}

// calculateInitialPathWindow calculates the initial window size for a new path
func (fcm *FlowControlManager) calculateInitialPathWindow() uint64 {
	if !fcm.config.EnablePathWindows {
		return fcm.config.InitialReceiveWindow
	}
	
	fcm.pathWindowsMutex.RLock()
	pathCount := len(fcm.pathWindows)
	fcm.pathWindowsMutex.RUnlock()
	
	switch fcm.config.PathWindowAllocation {
	case "equal":
		// Divide window equally among all paths
		if pathCount == 0 {
			return fcm.config.InitialReceiveWindow
		}
		equalShare := fcm.config.InitialReceiveWindow / uint64(pathCount+1)
		if equalShare < fcm.config.MinPathWindow {
			return fcm.config.MinPathWindow
		}
		return equalShare
		
	case "proportional":
		// Allocate based on path performance (simplified)
		return fcm.config.InitialReceiveWindow / 2
		
	case "adaptive":
		// Start with a base allocation and adapt based on usage
		baseWindow := fcm.config.InitialReceiveWindow / 4
		if baseWindow < fcm.config.MinPathWindow {
			return fcm.config.MinPathWindow
		}
		return baseWindow
		
	default:
		return fcm.config.InitialReceiveWindow
	}
}

// checkWindowUpdate checks if a window update should be sent
func (fcm *FlowControlManager) checkWindowUpdate(pathID string, pathWindow *PathWindowState) {
	pathWindow.mutex.RLock()
	allocatedWindow := pathWindow.AllocatedWindow
	usedWindow := pathWindow.UsedWindow
	pathWindow.mutex.RUnlock()
	
	if allocatedWindow == 0 {
		return
	}
	
	// Calculate window utilization
	utilization := float64(usedWindow) / float64(allocatedWindow)
	
	// Send window update if utilization exceeds threshold
	if utilization >= fcm.config.WindowUpdateThreshold {
		newWindow := fcm.calculateNewWindowSize(pathID, pathWindow)
		fcm.sendWindowUpdate(pathID, newWindow)
	}
}

// calculateNewWindowSize calculates the new window size for a path
func (fcm *FlowControlManager) calculateNewWindowSize(pathID string, pathWindow *PathWindowState) uint64 {
	pathWindow.mutex.RLock()
	currentWindow := pathWindow.AllocatedWindow
	usedWindow := pathWindow.UsedWindow
	pathWindow.mutex.RUnlock()
	
	if !fcm.config.EnableWindowScaling {
		return currentWindow
	}
	
	// Calculate new window size based on utilization and performance
	utilization := float64(usedWindow) / float64(currentWindow)
	
	var newWindow uint64
	if utilization > 0.8 {
		// High utilization - increase window
		newWindow = uint64(float64(currentWindow) * fcm.config.WindowScalingFactor)
	} else if utilization < 0.3 {
		// Low utilization - decrease window
		newWindow = uint64(float64(currentWindow) / fcm.config.WindowScalingFactor)
	} else {
		// Moderate utilization - keep current window
		newWindow = currentWindow
	}
	
	// Ensure window is within bounds
	if newWindow > fcm.config.MaxReceiveWindow {
		newWindow = fcm.config.MaxReceiveWindow
	}
	if newWindow < fcm.config.MinReceiveWindow {
		newWindow = fcm.config.MinReceiveWindow
	}
	
	return newWindow
}

// sendWindowUpdate sends a window update to the peer
func (fcm *FlowControlManager) sendWindowUpdate(pathID string, windowSize uint64) {
	// Update path window state
	fcm.pathWindowsMutex.RLock()
	pathWindow, exists := fcm.pathWindows[pathID]
	fcm.pathWindowsMutex.RUnlock()
	
	if exists {
		pathWindow.mutex.Lock()
		pathWindow.AllocatedWindow = windowSize
		pathWindow.WindowUpdates++
		pathWindow.LastUpdate = time.Now()
		pathWindow.mutex.Unlock()
	}
	
	// Send window update via control plane
	if fcm.controlPlane != nil {
		err := fcm.controlPlane.SendWindowUpdate(pathID, windowSize)
		if err != nil {
			fcm.logger.Error("Failed to send window update", "pathID", pathID, "windowSize", windowSize, "error", err)
			return
		}
	}
	
	// Update statistics
	fcm.statsMutex.Lock()
	fcm.stats.WindowUpdatesSent++
	fcm.stats.TotalWindowUpdates++
	if fcm.stats.PathWindowUpdates == nil {
		fcm.stats.PathWindowUpdates = make(map[string]uint64)
	}
	fcm.stats.PathWindowUpdates[pathID]++
	fcm.statsMutex.Unlock()
	
	// Trigger callback
	if fcm.windowUpdateCallback != nil {
		fcm.windowUpdateCallback(pathID, windowSize)
	}
	
	fcm.logger.Debug("Sent window update", "pathID", pathID, "windowSize", windowSize)
}

// handleWindowUpdate handles incoming window updates from peer
func (fcm *FlowControlManager) handleWindowUpdate(pathID string, windowSize uint64) {
	// Update send window for this path
	fcm.sendWindowManager.UpdateWindow(pathID, windowSize)
	
	// Update statistics
	fcm.statsMutex.Lock()
	fcm.stats.WindowUpdatesReceived++
	fcm.stats.TotalWindowUpdates++
	fcm.statsMutex.Unlock()
	
	fcm.logger.Debug("Received window update", "pathID", pathID, "windowSize", windowSize)
}

// triggerBackpressure triggers backpressure for a path
func (fcm *FlowControlManager) triggerBackpressure(pathID string, active bool) {
	fcm.pathWindowsMutex.RLock()
	pathWindow, exists := fcm.pathWindows[pathID]
	fcm.pathWindowsMutex.RUnlock()
	
	if exists {
		pathWindow.mutex.Lock()
		wasBackpressured := pathWindow.IsBackpressured
		pathWindow.IsBackpressured = active
		pathWindow.mutex.Unlock()
		
		if active && !wasBackpressured {
			// Backpressure activated
			fcm.statsMutex.Lock()
			fcm.stats.BackpressureEvents++
			fcm.statsMutex.Unlock()
		}
	}
	
	// Send backpressure signal via control plane
	if fcm.controlPlane != nil {
		fcm.controlPlane.SendBackpressureSignal(pathID, active)
	}
	
	// Trigger callback
	if fcm.backpressureCallback != nil {
		fcm.backpressureCallback(pathID, active)
	}
	
	fcm.logger.Debug("Triggered backpressure", "pathID", pathID, "active", active)
}

// handleBackpressureSignal handles incoming backpressure signals from peer
func (fcm *FlowControlManager) handleBackpressureSignal(pathID string, active bool) {
	// Update send window manager
	fcm.sendWindowManager.SetBackpressure(pathID, active)
	
	fcm.logger.Debug("Received backpressure signal", "pathID", pathID, "active", active)
}

// checkBackpressureRelease checks if backpressure can be released
func (fcm *FlowControlManager) checkBackpressureRelease(pathID string, pathWindow *PathWindowState) {
	pathWindow.mutex.RLock()
	allocatedWindow := pathWindow.AllocatedWindow
	usedWindow := pathWindow.UsedWindow
	isBackpressured := pathWindow.IsBackpressured
	pathWindow.mutex.RUnlock()
	
	if !isBackpressured {
		return
	}
	
	// Release backpressure if utilization drops below threshold
	utilization := float64(usedWindow) / float64(allocatedWindow)
	if utilization < fcm.config.BackpressureThreshold * 0.7 { // Hysteresis
		fcm.triggerBackpressure(pathID, false)
	}
}

// onReceiveWindowFull is called when the receive window becomes full
func (fcm *FlowControlManager) onReceiveWindowFull() {
	fcm.logger.Warn("Receive window is full - triggering backpressure")
	
	// Trigger backpressure on all paths
	fcm.pathWindowsMutex.RLock()
	pathIDs := make([]string, 0, len(fcm.pathWindows))
	for pathID := range fcm.pathWindows {
		pathIDs = append(pathIDs, pathID)
	}
	fcm.pathWindowsMutex.RUnlock()
	
	for _, pathID := range pathIDs {
		fcm.triggerBackpressure(pathID, true)
	}
	
	// Trigger callback
	if fcm.windowFullCallback != nil {
		fcm.windowFullCallback()
	}
}

// onReceiveWindowAvailable is called when the receive window becomes available
func (fcm *FlowControlManager) onReceiveWindowAvailable() {
	fcm.logger.Debug("Receive window is available - releasing backpressure")
	
	// Release backpressure on all paths
	fcm.pathWindowsMutex.RLock()
	pathIDs := make([]string, 0, len(fcm.pathWindows))
	for pathID := range fcm.pathWindows {
		pathIDs = append(pathIDs, pathID)
	}
	fcm.pathWindowsMutex.RUnlock()
	
	for _, pathID := range pathIDs {
		fcm.triggerBackpressure(pathID, false)
	}
	
	// Trigger callback
	if fcm.windowAvailableCallback != nil {
		fcm.windowAvailableCallback()
	}
}

// windowRefreshWorker periodically refreshes window information
func (fcm *FlowControlManager) windowRefreshWorker() {
	defer fcm.wg.Done()
	
	ticker := time.NewTicker(fcm.config.WindowRefreshInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			fcm.refreshWindows()
		case <-fcm.ctx.Done():
			return
		}
	}
}

// refreshWindows refreshes window information for all paths
func (fcm *FlowControlManager) refreshWindows() {
	fcm.pathWindowsMutex.RLock()
	pathIDs := make([]string, 0, len(fcm.pathWindows))
	for pathID := range fcm.pathWindows {
		pathIDs = append(pathIDs, pathID)
	}
	fcm.pathWindowsMutex.RUnlock()
	
	for _, pathID := range pathIDs {
		fcm.pathWindowsMutex.RLock()
		pathWindow := fcm.pathWindows[pathID]
		fcm.pathWindowsMutex.RUnlock()
		
		if pathWindow != nil {
			pathWindow.mutex.RLock()
			windowSize := pathWindow.AllocatedWindow
			pathWindow.mutex.RUnlock()
			
			// Send periodic window refresh
			fcm.sendWindowUpdate(pathID, windowSize)
		}
	}
}

// adaptiveWindowWorker performs adaptive window management
func (fcm *FlowControlManager) adaptiveWindowWorker() {
	defer fcm.wg.Done()
	
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			fcm.performAdaptiveWindowAdjustment()
		case <-fcm.ctx.Done():
			return
		}
	}
}

// performAdaptiveWindowAdjustment performs adaptive window size adjustments
func (fcm *FlowControlManager) performAdaptiveWindowAdjustment() {
	fcm.pathWindowsMutex.RLock()
	defer fcm.pathWindowsMutex.RUnlock()
	
	for pathID, pathWindow := range fcm.pathWindows {
		pathWindow.mutex.RLock()
		allocatedWindow := pathWindow.AllocatedWindow
		usedWindow := pathWindow.UsedWindow
		totalDataSent := pathWindow.TotalDataSent
		totalDataReceived := pathWindow.TotalDataReceived
		lastUpdate := pathWindow.LastUpdate
		pathWindow.mutex.RUnlock()
		
		// Calculate metrics for adaptive adjustment
		utilization := float64(usedWindow) / float64(allocatedWindow)
		timeSinceUpdate := time.Since(lastUpdate)
		
		// Determine if window should be adjusted
		var newWindow uint64
		shouldAdjust := false
		
		if utilization > 0.8 && timeSinceUpdate < time.Minute {
			// High utilization and recent activity - increase window
			newWindow = uint64(float64(allocatedWindow) * 1.2)
			shouldAdjust = true
		} else if utilization < 0.2 && timeSinceUpdate > 5*time.Minute {
			// Low utilization and no recent activity - decrease window
			newWindow = uint64(float64(allocatedWindow) * 0.8)
			shouldAdjust = true
		}
		
		if shouldAdjust {
			// Ensure window is within bounds
			if newWindow > fcm.config.MaxReceiveWindow {
				newWindow = fcm.config.MaxReceiveWindow
			}
			if newWindow < fcm.config.MinReceiveWindow {
				newWindow = fcm.config.MinReceiveWindow
			}
			
			if newWindow != allocatedWindow {
				fcm.sendWindowUpdate(pathID, newWindow)
				fcm.logger.Debug("Adaptive window adjustment", 
					"pathID", pathID, 
					"oldWindow", allocatedWindow, 
					"newWindow", newWindow,
					"utilization", utilization,
					"dataSent", totalDataSent,
					"dataReceived", totalDataReceived)
			}
		}
	}
}

// sendWindowWorker manages send window operations
func (fcm *FlowControlManager) sendWindowWorker() {
	defer fcm.wg.Done()
	
	ticker := time.NewTicker(fcm.sendWindowManager.config.UpdateInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			fcm.sendWindowManager.UpdateWindows()
		case <-fcm.ctx.Done():
			return
		}
	}
}

// GetFlowControlStats returns current flow control statistics
func (fcm *FlowControlManager) GetFlowControlStats() *FlowControlStats {
	fcm.statsMutex.RLock()
	defer fcm.statsMutex.RUnlock()
	
	// Create a copy to avoid race conditions
	stats := &FlowControlStats{
		TotalWindowUpdates:       fcm.stats.TotalWindowUpdates,
		WindowUpdatesSent:        fcm.stats.WindowUpdatesSent,
		WindowUpdatesReceived:    fcm.stats.WindowUpdatesReceived,
		BackpressureEvents:       fcm.stats.BackpressureEvents,
		BackpressureDuration:     fcm.stats.BackpressureDuration,
		TotalDataSent:            fcm.stats.TotalDataSent,
		TotalDataReceived:        fcm.stats.TotalDataReceived,
		DataBlocked:              fcm.stats.DataBlocked,
		ActivePaths:              fcm.stats.ActivePaths,
		PathWindowUpdates:        make(map[string]uint64),
		AverageWindowUtilization: fcm.stats.AverageWindowUtilization,
		PeakWindowUtilization:    fcm.stats.PeakWindowUtilization,
		LastUpdate:               fcm.stats.LastUpdate,
	}
	
	// Copy path window updates
	for pathID, count := range fcm.stats.PathWindowUpdates {
		stats.PathWindowUpdates[pathID] = count
	}
	
	return stats
}

// SetWindowUpdateCallback sets the callback for window updates
func (fcm *FlowControlManager) SetWindowUpdateCallback(callback func(pathID string, windowSize uint64)) {
	fcm.windowUpdateCallback = callback
}

// SetBackpressureCallback sets the callback for backpressure events
func (fcm *FlowControlManager) SetBackpressureCallback(callback func(pathID string, active bool)) {
	fcm.backpressureCallback = callback
}

// SetWindowFullCallback sets the callback for when window becomes full
func (fcm *FlowControlManager) SetWindowFullCallback(callback func()) {
	fcm.windowFullCallback = callback
}

// SetWindowAvailableCallback sets the callback for when window becomes available
func (fcm *FlowControlManager) SetWindowAvailableCallback(callback func()) {
	fcm.windowAvailableCallback = callback
}
// Send Window Manager Methods

// AddPath adds a path to send window management
func (swm *SendWindowManager) AddPath(pathID string, initialWindow uint64) {
	swm.pathWindowsMutex.Lock()
	defer swm.pathWindowsMutex.Unlock()
	
	pathWindow := &PathSendWindow{
		PathID:     pathID,
		WindowSize: initialWindow,
		UsedWindow: 0,
		LastAck:    time.Now(),
		RTT:        100 * time.Millisecond, // Default RTT
	}
	
	swm.pathWindows[pathID] = pathWindow
	
	swm.pendingDataMutex.Lock()
	swm.pendingData[pathID] = 0
	swm.pendingDataMutex.Unlock()
}

// RemovePath removes a path from send window management
func (swm *SendWindowManager) RemovePath(pathID string) {
	swm.pathWindowsMutex.Lock()
	delete(swm.pathWindows, pathID)
	swm.pathWindowsMutex.Unlock()
	
	swm.pendingDataMutex.Lock()
	delete(swm.pendingData, pathID)
	swm.pendingDataMutex.Unlock()
}

// CanSendData checks if data can be sent on a path
func (swm *SendWindowManager) CanSendData(pathID string, dataSize uint64) bool {
	swm.pathWindowsMutex.RLock()
	pathWindow, exists := swm.pathWindows[pathID]
	swm.pathWindowsMutex.RUnlock()
	
	if !exists {
		return false
	}
	
	pathWindow.mutex.RLock()
	available := pathWindow.WindowSize - pathWindow.UsedWindow
	pathWindow.mutex.RUnlock()
	
	return available >= dataSize
}

// ReserveWindow reserves window space for sending data
func (swm *SendWindowManager) ReserveWindow(pathID string, dataSize uint64) error {
	swm.pathWindowsMutex.RLock()
	pathWindow, exists := swm.pathWindows[pathID]
	swm.pathWindowsMutex.RUnlock()
	
	if !exists {
		return fmt.Errorf("path %s not found in send window manager", pathID)
	}
	
	pathWindow.mutex.Lock()
	defer pathWindow.mutex.Unlock()
	
	available := pathWindow.WindowSize - pathWindow.UsedWindow
	if available < dataSize {
		return fmt.Errorf("insufficient send window: available %d, requested %d", available, dataSize)
	}
	
	pathWindow.UsedWindow += dataSize
	pathWindow.PendingBytes += dataSize
	
	return nil
}

// ConsumeWindow consumes window space when data is sent
func (swm *SendWindowManager) ConsumeWindow(pathID string, dataSize uint64) error {
	swm.pathWindowsMutex.RLock()
	pathWindow, exists := swm.pathWindows[pathID]
	swm.pathWindowsMutex.RUnlock()
	
	if !exists {
		return fmt.Errorf("path %s not found in send window manager", pathID)
	}
	
	pathWindow.mutex.Lock()
	defer pathWindow.mutex.Unlock()
	
	if pathWindow.PendingBytes >= dataSize {
		pathWindow.PendingBytes -= dataSize
	} else {
		pathWindow.PendingBytes = 0
	}
	
	return nil
}

// UpdateWindow updates the send window size for a path
func (swm *SendWindowManager) UpdateWindow(pathID string, windowSize uint64) {
	swm.pathWindowsMutex.RLock()
	pathWindow, exists := swm.pathWindows[pathID]
	swm.pathWindowsMutex.RUnlock()
	
	if !exists {
		return
	}
	
	pathWindow.mutex.Lock()
	defer pathWindow.mutex.Unlock()
	
	pathWindow.WindowSize = windowSize
	pathWindow.WindowUpdates++
	pathWindow.LastAck = time.Now()
	
	// Ensure window bounds
	if windowSize > swm.config.MaxWindow {
		pathWindow.WindowSize = swm.config.MaxWindow
	}
	if windowSize < swm.config.MinWindow {
		pathWindow.WindowSize = swm.config.MinWindow
	}
}

// SetBackpressure sets backpressure status for a path
func (swm *SendWindowManager) SetBackpressure(pathID string, active bool) {
	swm.pathWindowsMutex.RLock()
	pathWindow, exists := swm.pathWindows[pathID]
	swm.pathWindowsMutex.RUnlock()
	
	if !exists {
		return
	}
	
	pathWindow.mutex.Lock()
	defer pathWindow.mutex.Unlock()
	
	if active {
		// Reduce window size under backpressure
		pathWindow.WindowSize = uint64(float64(pathWindow.WindowSize) * swm.config.ShrinkFactor)
		if pathWindow.WindowSize < swm.config.MinWindow {
			pathWindow.WindowSize = swm.config.MinWindow
		}
	}
}

// UpdateWindows performs periodic window updates
func (swm *SendWindowManager) UpdateWindows() {
	swm.pathWindowsMutex.RLock()
	defer swm.pathWindowsMutex.RUnlock()
	
	for _, pathWindow := range swm.pathWindows {
		pathWindow.mutex.Lock()
		
		// Adaptive window sizing based on RTT and utilization
		timeSinceAck := time.Since(pathWindow.LastAck)
		utilization := float64(pathWindow.UsedWindow) / float64(pathWindow.WindowSize)
		
		var newWindowSize uint64
		if timeSinceAck < swm.config.RTTThreshold && utilization > 0.8 {
			// Good performance and high utilization - grow window
			newWindowSize = uint64(float64(pathWindow.WindowSize) * swm.config.GrowthFactor)
		} else if timeSinceAck > swm.config.RTTThreshold*2 || utilization < 0.2 {
			// Poor performance or low utilization - shrink window
			newWindowSize = uint64(float64(pathWindow.WindowSize) * swm.config.ShrinkFactor)
		} else {
			// Stable performance - keep current window
			newWindowSize = pathWindow.WindowSize
		}
		
		// Apply bounds
		if newWindowSize > swm.config.MaxWindow {
			newWindowSize = swm.config.MaxWindow
		}
		if newWindowSize < swm.config.MinWindow {
			newWindowSize = swm.config.MinWindow
		}
		
		pathWindow.WindowSize = newWindowSize
		pathWindow.mutex.Unlock()
	}
}

// GetSendWindowStats returns send window statistics
func (swm *SendWindowManager) GetSendWindowStats() map[string]*PathSendWindow {
	swm.pathWindowsMutex.RLock()
	defer swm.pathWindowsMutex.RUnlock()
	
	stats := make(map[string]*PathSendWindow)
	for pathID, pathWindow := range swm.pathWindows {
		pathWindow.mutex.RLock()
		stats[pathID] = &PathSendWindow{
			PathID:        pathWindow.PathID,
			WindowSize:    pathWindow.WindowSize,
			UsedWindow:    pathWindow.UsedWindow,
			LastAck:       pathWindow.LastAck,
			PendingBytes:  pathWindow.PendingBytes,
			RTT:           pathWindow.RTT,
			WindowUpdates: pathWindow.WindowUpdates,
		}
		pathWindow.mutex.RUnlock()
	}
	
	return stats
}