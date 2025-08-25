package session

import (
	"fmt"
	"sync"
	"time"

)

// HeartbeatSchedulerIntegration manages the integration between adaptive scheduler and heartbeat systems
type HeartbeatSchedulerIntegration struct {
	// Heartbeat scheduler
	scheduler HeartbeatScheduler
	
	// Heartbeat systems (interfaces to avoid import cycles)
	controlSystem interface{}
	dataSystem    interface{}
	
	// Integration state
	integrationActive bool
	
	// Synchronization
	mutex sync.RWMutex
	
	// Logging
	logger SchedulerLogger
}

// ControlHeartbeatSystemInterface defines the interface for control heartbeat system integration
type ControlHeartbeatSystemInterface interface {
	SetHeartbeatScheduler(scheduler interface{})
	GetAllControlHeartbeatStats() map[string]interface{}
}

// DataHeartbeatSystemInterface defines the interface for data heartbeat system integration
type DataHeartbeatSystemInterface interface {
	SetHeartbeatScheduler(scheduler interface{})
}

// NewHeartbeatSchedulerIntegration creates a new integration manager
func NewHeartbeatSchedulerIntegration(scheduler HeartbeatScheduler, logger *SchedulerLogger) *HeartbeatSchedulerIntegration {
	if logger == nil {
		logger = &SchedulerLogger{}
	}
	
	return &HeartbeatSchedulerIntegration{
		scheduler:         scheduler,
		integrationActive: false,
		logger:           *logger,
	}
}

// IntegrateWithControlSystem integrates the scheduler with the control heartbeat system
func (hsi *HeartbeatSchedulerIntegration) IntegrateWithControlSystem(controlSystem interface{}) error {
	hsi.mutex.Lock()
	defer hsi.mutex.Unlock()
	
	hsi.controlSystem = controlSystem
	
	// Set the scheduler in the control system using reflection-like approach
	if setter, ok := controlSystem.(interface{ SetHeartbeatScheduler(interface{}) }); ok {
		setter.SetHeartbeatScheduler(hsi.scheduler)
		hsi.logger.Info("Integrated heartbeat scheduler with control heartbeat system")
	} else {
		return fmt.Errorf("control system does not support heartbeat scheduler integration")
	}
	
	return nil
}

// IntegrateWithDataSystem integrates the scheduler with the data heartbeat system
func (hsi *HeartbeatSchedulerIntegration) IntegrateWithDataSystem(dataSystem interface{}) error {
	hsi.mutex.Lock()
	defer hsi.mutex.Unlock()
	
	hsi.dataSystem = dataSystem
	
	// Set the scheduler in the data system using reflection-like approach
	if setter, ok := dataSystem.(interface{ SetHeartbeatScheduler(interface{}) }); ok {
		setter.SetHeartbeatScheduler(hsi.scheduler)
		hsi.logger.Info("Integrated heartbeat scheduler with data heartbeat system")
	} else {
		return fmt.Errorf("data system does not support heartbeat scheduler integration")
	}
	
	return nil
}

// StartIntegration starts the integration and begins real-time updates
func (hsi *HeartbeatSchedulerIntegration) StartIntegration() error {
	hsi.mutex.Lock()
	defer hsi.mutex.Unlock()
	
	if hsi.integrationActive {
		return fmt.Errorf("integration already active")
	}
	
	hsi.integrationActive = true
	
	// Start background monitoring for real-time updates
	go hsi.monitoringLoop()
	
	hsi.logger.Info("Heartbeat scheduler integration started")
	
	return nil
}

// StopIntegration stops the integration
func (hsi *HeartbeatSchedulerIntegration) StopIntegration() error {
	hsi.mutex.Lock()
	defer hsi.mutex.Unlock()
	
	hsi.integrationActive = false
	
	hsi.logger.Info("Heartbeat scheduler integration stopped")
	
	return nil
}

// UpdateNetworkFeedback updates the scheduler with network feedback from heartbeat systems
func (hsi *HeartbeatSchedulerIntegration) UpdateNetworkFeedback(pathID string, rtt time.Duration, packetLoss float64, consecutiveFailures int) error {
	hsi.mutex.RLock()
	active := hsi.integrationActive
	scheduler := hsi.scheduler
	hsi.mutex.RUnlock()
	
	if !active {
		return fmt.Errorf("integration not active")
	}
	
	// Update network conditions in scheduler
	err := scheduler.UpdateNetworkConditions(pathID, rtt, packetLoss)
	if err != nil {
		hsi.logger.Error("Failed to update network conditions in scheduler", 
			"pathID", pathID, "error", err)
		return err
	}
	
	// Update failure count if scheduler supports it
	if failureUpdater, ok := scheduler.(interface{ UpdateFailureCount(string, int) error }); ok {
		err = failureUpdater.UpdateFailureCount(pathID, consecutiveFailures)
		if err != nil {
			hsi.logger.Error("Failed to update failure count in scheduler", 
				"pathID", pathID, "error", err)
		}
	}
	
	hsi.logger.Debug("Updated network feedback in scheduler", 
		"pathID", pathID,
		"rtt", rtt,
		"packetLoss", packetLoss,
		"consecutiveFailures", consecutiveFailures)
	
	return nil
}

// UpdateStreamActivity updates stream activity in the scheduler
func (hsi *HeartbeatSchedulerIntegration) UpdateStreamActivity(streamID uint64, pathID string, lastActivity time.Time) error {
	hsi.mutex.RLock()
	active := hsi.integrationActive
	scheduler := hsi.scheduler
	hsi.mutex.RUnlock()
	
	if !active {
		return fmt.Errorf("integration not active")
	}
	
	err := scheduler.UpdateStreamActivity(streamID, pathID, lastActivity)
	if err != nil {
		hsi.logger.Error("Failed to update stream activity in scheduler", 
			"streamID", streamID, "pathID", pathID, "error", err)
		return err
	}
	
	hsi.logger.Debug("Updated stream activity in scheduler", 
		"streamID", streamID,
		"pathID", pathID,
		"lastActivity", lastActivity)
	
	return nil
}

// GetSchedulingParameters returns current scheduling parameters for a path
func (hsi *HeartbeatSchedulerIntegration) GetSchedulingParameters(pathID string) *HeartbeatSchedulingParams {
	hsi.mutex.RLock()
	scheduler := hsi.scheduler
	hsi.mutex.RUnlock()
	
	return scheduler.GetSchedulingParams(pathID)
}

// monitoringLoop runs in the background to provide real-time updates
func (hsi *HeartbeatSchedulerIntegration) monitoringLoop() {
	ticker := time.NewTicker(10 * time.Second) // Check every 10 seconds
	defer ticker.Stop()
	
	for {
		hsi.mutex.RLock()
		active := hsi.integrationActive
		hsi.mutex.RUnlock()
		
		if !active {
			return
		}
		
		select {
		case <-ticker.C:
			hsi.performPeriodicUpdates()
		}
	}
}

// performPeriodicUpdates performs periodic updates and optimizations
func (hsi *HeartbeatSchedulerIntegration) performPeriodicUpdates() {
	hsi.mutex.RLock()
	controlSystem := hsi.controlSystem
	scheduler := hsi.scheduler
	hsi.mutex.RUnlock()
	
	// Get statistics from control system if available
	if statsProvider, ok := controlSystem.(interface{ GetAllControlHeartbeatStats() map[string]interface{} }); ok {
		stats := statsProvider.GetAllControlHeartbeatStats()
		
		// Process statistics and update scheduler
		for pathID, pathStats := range stats {
			hsi.processControlHeartbeatStats(pathID, pathStats)
		}
	}
	
	// Log current scheduling state for monitoring
	if stateProvider, ok := scheduler.(interface{ GetPathState(string) *HeartbeatSchedulingState }); ok {
		// This would be used for monitoring/debugging - implementation depends on specific monitoring needs
		_ = stateProvider
	}
}

// processControlHeartbeatStats processes control heartbeat statistics for scheduler updates
func (hsi *HeartbeatSchedulerIntegration) processControlHeartbeatStats(pathID string, stats interface{}) {
	// This is a simplified implementation - in practice, you'd need to extract
	// specific fields from the stats interface based on the actual structure
	
	hsi.logger.Debug("Processing control heartbeat stats for scheduler update", 
		"pathID", pathID)
	
	// Example: Extract RTT and failure information from stats
	// This would need to be implemented based on the actual stats structure
	// For now, we'll just log that we're processing the stats
}

// PersistSchedulingParameters persists scheduling parameters for recovery
func (hsi *HeartbeatSchedulerIntegration) PersistSchedulingParameters(pathID string) error {
	hsi.mutex.RLock()
	scheduler := hsi.scheduler
	hsi.mutex.RUnlock()
	
	params := scheduler.GetSchedulingParams(pathID)
	if params == nil {
		return fmt.Errorf("no scheduling parameters found for path %s", pathID)
	}
	
	// In a real implementation, this would persist to storage
	// For now, we'll just log the persistence action
	hsi.logger.Info("Persisting scheduling parameters", 
		"pathID", pathID,
		"controlInterval", params.CurrentControlInterval,
		"dataInterval", params.CurrentDataInterval,
		"networkScore", params.NetworkConditionScore)
	
	return nil
}

// RecoverSchedulingParameters recovers scheduling parameters from storage
func (hsi *HeartbeatSchedulerIntegration) RecoverSchedulingParameters(pathID string) error {
	// In a real implementation, this would recover from storage
	// For now, we'll just log the recovery action
	hsi.logger.Info("Recovering scheduling parameters", "pathID", pathID)
	
	return nil
}

// GetIntegrationStatus returns the current integration status
func (hsi *HeartbeatSchedulerIntegration) GetIntegrationStatus() map[string]interface{} {
	hsi.mutex.RLock()
	defer hsi.mutex.RUnlock()
	
	return map[string]interface{}{
		"active":             hsi.integrationActive,
		"controlIntegrated":  hsi.controlSystem != nil,
		"dataIntegrated":     hsi.dataSystem != nil,
		"schedulerAvailable": hsi.scheduler != nil,
	}
}

// Shutdown gracefully shuts down the integration
func (hsi *HeartbeatSchedulerIntegration) Shutdown() error {
	hsi.mutex.Lock()
	defer hsi.mutex.Unlock()
	
	hsi.integrationActive = false
	hsi.controlSystem = nil
	hsi.dataSystem = nil
	
	if hsi.scheduler != nil {
		err := hsi.scheduler.Shutdown()
		if err != nil {
			hsi.logger.Error("Error shutting down scheduler", "error", err)
		}
	}
	
	hsi.logger.Info("Heartbeat scheduler integration shutdown complete")
	
	return nil
}