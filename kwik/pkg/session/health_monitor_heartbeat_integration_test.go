package session

import (
	"context"
	"testing"
	"time"
)

// TestHealthMonitorHeartbeatIntegration tests the integration between health monitor and heartbeat system
func TestHealthMonitorHeartbeatIntegration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	// Create health monitor
	monitor := NewConnectionHealthMonitor(ctx)
	
	sessionID := "test-session"
	pathID := "test-path"
	paths := []string{pathID}
	
	// Start monitoring
	err := monitor.StartMonitoring(sessionID, paths)
	if err != nil {
		t.Fatalf("Failed to start monitoring: %v", err)
	}
	
	// Create heartbeat metrics
	heartbeatMetrics := &HeartbeatHealthMetrics{
		PathID:             pathID,
		SessionID:          sessionID,
		ControlPlaneHealth: 0.9,
		DataPlaneHealth:    0.8,
		OverallHealth:      0.85,
		RTTTrend:          RTTTrendStable,
		FailureRate:       0.1,
		RecoveryRate:      0.9,
		ControlHeartbeatStats: &ControlHeartbeatMetrics{
			HeartbeatsSent:      10,
			HeartbeatsReceived:  9,
			ResponsesReceived:   9,
			ConsecutiveFailures: 1,
			AverageRTT:          50 * time.Millisecond,
			LastHeartbeatSent:   time.Now().Add(-1 * time.Second),
			LastResponseReceived: time.Now().Add(-500 * time.Millisecond),
			CurrentInterval:     30 * time.Second,
			SuccessRate:        0.9,
		},
		DataHeartbeatStats: &DataHeartbeatMetrics{
			ActiveStreams: 2,
			StreamMetrics: map[uint64]*StreamHeartbeatMetrics{
				1: {
					StreamID:            1,
					HeartbeatsSent:      5,
					ResponsesReceived:   5,
					ConsecutiveFailures: 0,
					LastDataActivity:    time.Now().Add(-2 * time.Second),
					LastHeartbeatSent:   time.Now().Add(-1 * time.Second),
					LastResponseReceived: time.Now().Add(-500 * time.Millisecond),
					CurrentInterval:     60 * time.Second,
					StreamActive:        true,
					SuccessRate:        1.0,
				},
				2: {
					StreamID:            2,
					HeartbeatsSent:      3,
					ResponsesReceived:   2,
					ConsecutiveFailures: 1,
					LastDataActivity:    time.Now().Add(-5 * time.Second),
					LastHeartbeatSent:   time.Now().Add(-2 * time.Second),
					LastResponseReceived: time.Now().Add(-3 * time.Second),
					CurrentInterval:     60 * time.Second,
					StreamActive:        false,
					SuccessRate:        0.67,
				},
			},
			TotalHeartbeatsSent:    8,
			TotalResponsesReceived: 7,
			AverageSuccessRate:     0.875,
		},
	}
	
	// Update health metrics from heartbeat data
	err = monitor.UpdateHealthMetricsFromHeartbeat(sessionID, pathID, heartbeatMetrics)
	if err != nil {
		t.Errorf("Failed to update health metrics from heartbeat: %v", err)
	}
	
	// Get integrated health
	integratedHealth := monitor.GetHeartbeatIntegratedHealth(sessionID, pathID)
	if integratedHealth == nil {
		t.Error("Expected integrated health to be available")
	}
	
	// Verify integrated health contains heartbeat data
	if integratedHealth.ControlPlaneHeartbeat == nil {
		t.Error("Expected control plane heartbeat info to be available")
	}
	
	if integratedHealth.DataPlaneHeartbeat == nil {
		t.Error("Expected data plane heartbeat info to be available")
	}
	
	if integratedHealth.HeartbeatHealth <= 0 {
		t.Error("Expected heartbeat health score to be positive")
	}
	
	// Verify control plane heartbeat info
	controlInfo := integratedHealth.ControlPlaneHeartbeat
	if !controlInfo.Active {
		t.Error("Expected control plane heartbeat to be active")
	}
	
	if controlInfo.SuccessRate != 0.9 {
		t.Errorf("Expected control plane success rate to be 0.9, got %f", controlInfo.SuccessRate)
	}
	
	if controlInfo.AverageRTT != 50*time.Millisecond {
		t.Errorf("Expected control plane RTT to be 50ms, got %v", controlInfo.AverageRTT)
	}
	
	// Verify data plane heartbeat info
	dataInfo := integratedHealth.DataPlaneHeartbeat
	if dataInfo.ActiveStreams != 2 {
		t.Errorf("Expected 2 active streams, got %d", dataInfo.ActiveStreams)
	}
	
	if len(dataInfo.StreamHeartbeats) != 2 {
		t.Errorf("Expected 2 stream heartbeats, got %d", len(dataInfo.StreamHeartbeats))
	}
	
	// Verify stream heartbeat info
	stream1Info, exists := dataInfo.StreamHeartbeats[1]
	if !exists {
		t.Error("Expected stream 1 heartbeat info to exist")
	} else {
		if !stream1Info.Active {
			t.Error("Expected stream 1 to be active")
		}
		if stream1Info.SuccessRate != 1.0 {
			t.Errorf("Expected stream 1 success rate to be 1.0, got %f", stream1Info.SuccessRate)
		}
	}
	
	stream2Info, exists := dataInfo.StreamHeartbeats[2]
	if !exists {
		t.Error("Expected stream 2 heartbeat info to exist")
	} else {
		if stream2Info.Active {
			t.Error("Expected stream 2 to be inactive")
		}
		if stream2Info.ConsecutiveFailures != 1 {
			t.Errorf("Expected stream 2 to have 1 consecutive failure, got %d", stream2Info.ConsecutiveFailures)
		}
	}
	
	// Cleanup
	monitor.StopMonitoring(sessionID)
}

// TestHealthMonitorHeartbeatFailureHandler tests heartbeat failure handling
func TestHealthMonitorHeartbeatFailureHandler(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	// Create health monitor
	monitor := NewConnectionHealthMonitor(ctx)
	
	sessionID := "test-session"
	pathID := "test-path"
	paths := []string{pathID}
	
	// Start monitoring
	err := monitor.StartMonitoring(sessionID, paths)
	if err != nil {
		t.Fatalf("Failed to start monitoring: %v", err)
	}
	
	// Register failure handler
	failureReceived := false
	var receivedFailure *HeartbeatFailureInfo
	
	handler := func(sessionID, pathID string, failure *HeartbeatFailureInfo) {
		failureReceived = true
		receivedFailure = failure
	}
	
	err = monitor.RegisterHeartbeatFailureHandler(handler)
	if err != nil {
		t.Errorf("Failed to register heartbeat failure handler: %v", err)
	}
	
	// Create heartbeat metrics with failures
	heartbeatMetrics := &HeartbeatHealthMetrics{
		PathID:             pathID,
		SessionID:          sessionID,
		ControlPlaneHealth: 0.3,
		DataPlaneHealth:    0.5,
		OverallHealth:      0.4,
		RTTTrend:          RTTTrendDegrading,
		FailureRate:       0.7,
		RecoveryRate:      0.3,
		ControlHeartbeatStats: &ControlHeartbeatMetrics{
			HeartbeatsSent:      10,
			HeartbeatsReceived:  3,
			ResponsesReceived:   3,
			ConsecutiveFailures: 7, // Above threshold
			AverageRTT:          200 * time.Millisecond,
			LastHeartbeatSent:   time.Now().Add(-1 * time.Second),
			LastResponseReceived: time.Now().Add(-10 * time.Second),
			CurrentInterval:     30 * time.Second,
			SuccessRate:        0.3,
		},
	}
	
	// Update health metrics (should trigger failure handler)
	err = monitor.UpdateHealthMetricsFromHeartbeat(sessionID, pathID, heartbeatMetrics)
	if err != nil {
		t.Errorf("Failed to update health metrics from heartbeat: %v", err)
	}
	
	// Wait for failure handler to be called
	time.Sleep(100 * time.Millisecond)
	
	// Verify failure handler was called
	if !failureReceived {
		t.Error("Expected heartbeat failure handler to be called")
	}
	
	if receivedFailure == nil {
		t.Error("Expected to receive failure info")
	} else {
		if receivedFailure.FailureType != "CONTROL_PLANE_HEARTBEAT" {
			t.Errorf("Expected failure type to be CONTROL_PLANE_HEARTBEAT, got %s", receivedFailure.FailureType)
		}
		
		if receivedFailure.ConsecutiveFails != 7 {
			t.Errorf("Expected 7 consecutive failures, got %d", receivedFailure.ConsecutiveFails)
		}
		
		if !receivedFailure.RTTDegradation {
			t.Error("Expected RTT degradation to be detected")
		}
	}
	
	// Cleanup
	monitor.StopMonitoring(sessionID)
}

// TestHealthMonitorHeartbeatTrendCalculation tests heartbeat trend calculation
func TestHealthMonitorHeartbeatTrendCalculation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	// Create health monitor
	monitor := NewConnectionHealthMonitor(ctx)
	
	sessionID := "test-session"
	pathID := "test-path"
	paths := []string{pathID}
	
	// Start monitoring
	err := monitor.StartMonitoring(sessionID, paths)
	if err != nil {
		t.Fatalf("Failed to start monitoring: %v", err)
	}
	
	// Test improving trend
	improvingMetrics := &HeartbeatHealthMetrics{
		PathID:             pathID,
		SessionID:          sessionID,
		ControlPlaneHealth: 0.9,
		DataPlaneHealth:    0.9,
		OverallHealth:      0.9,
		RTTTrend:          RTTTrendImproving,
		FailureRate:       0.05,
		RecoveryRate:      0.95,
	}
	
	err = monitor.UpdateHealthMetricsFromHeartbeat(sessionID, pathID, improvingMetrics)
	if err != nil {
		t.Errorf("Failed to update health metrics: %v", err)
	}
	
	integratedHealth := monitor.GetHeartbeatIntegratedHealth(sessionID, pathID)
	if integratedHealth.HeartbeatTrend != HeartbeatHealthTrendImproving {
		t.Errorf("Expected improving trend, got %v", integratedHealth.HeartbeatTrend)
	}
	
	// Test degrading trend
	degradingMetrics := &HeartbeatHealthMetrics{
		PathID:             pathID,
		SessionID:          sessionID,
		ControlPlaneHealth: 0.6,
		DataPlaneHealth:    0.7,
		OverallHealth:      0.65,
		RTTTrend:          RTTTrendDegrading,
		FailureRate:       0.25,
		RecoveryRate:      0.75,
	}
	
	err = monitor.UpdateHealthMetricsFromHeartbeat(sessionID, pathID, degradingMetrics)
	if err != nil {
		t.Errorf("Failed to update health metrics: %v", err)
	}
	
	integratedHealth = monitor.GetHeartbeatIntegratedHealth(sessionID, pathID)
	if integratedHealth.HeartbeatTrend != HeartbeatHealthTrendDegrading {
		t.Errorf("Expected degrading trend, got %v", integratedHealth.HeartbeatTrend)
	}
	
	// Test critical trend
	criticalMetrics := &HeartbeatHealthMetrics{
		PathID:             pathID,
		SessionID:          sessionID,
		ControlPlaneHealth: 0.2,
		DataPlaneHealth:    0.3,
		OverallHealth:      0.25,
		RTTTrend:          RTTTrendDegrading,
		FailureRate:       0.6,
		RecoveryRate:      0.4,
	}
	
	err = monitor.UpdateHealthMetricsFromHeartbeat(sessionID, pathID, criticalMetrics)
	if err != nil {
		t.Errorf("Failed to update health metrics: %v", err)
	}
	
	integratedHealth = monitor.GetHeartbeatIntegratedHealth(sessionID, pathID)
	if integratedHealth.HeartbeatTrend != HeartbeatHealthTrendCritical {
		t.Errorf("Expected critical trend, got %v", integratedHealth.HeartbeatTrend)
	}
	
	// Cleanup
	monitor.StopMonitoring(sessionID)
}

// TestHealthMonitorHeartbeatHealthScore tests heartbeat health score calculation
func TestHealthMonitorHeartbeatHealthScore(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	// Create health monitor
	monitor := NewConnectionHealthMonitor(ctx)
	
	sessionID := "test-session"
	pathID := "test-path"
	paths := []string{pathID}
	
	// Start monitoring
	err := monitor.StartMonitoring(sessionID, paths)
	if err != nil {
		t.Fatalf("Failed to start monitoring: %v", err)
	}
	
	// Test perfect health
	perfectMetrics := &HeartbeatHealthMetrics{
		PathID:             pathID,
		SessionID:          sessionID,
		ControlPlaneHealth: 1.0,
		DataPlaneHealth:    1.0,
		OverallHealth:      1.0,
		RTTTrend:          RTTTrendStable,
		FailureRate:       0.0,
		RecoveryRate:      1.0,
	}
	
	err = monitor.UpdateHealthMetricsFromHeartbeat(sessionID, pathID, perfectMetrics)
	if err != nil {
		t.Errorf("Failed to update health metrics: %v", err)
	}
	
	integratedHealth := monitor.GetHeartbeatIntegratedHealth(sessionID, pathID)
	if integratedHealth.HeartbeatHealth != 1.0 {
		t.Errorf("Expected perfect health score (1.0), got %f", integratedHealth.HeartbeatHealth)
	}
	
	// Test degraded health
	degradedMetrics := &HeartbeatHealthMetrics{
		PathID:             pathID,
		SessionID:          sessionID,
		ControlPlaneHealth: 0.8,
		DataPlaneHealth:    0.7,
		OverallHealth:      0.75,
		RTTTrend:          RTTTrendDegrading,
		FailureRate:       0.2,
		RecoveryRate:      0.8,
	}
	
	err = monitor.UpdateHealthMetricsFromHeartbeat(sessionID, pathID, degradedMetrics)
	if err != nil {
		t.Errorf("Failed to update health metrics: %v", err)
	}
	
	integratedHealth = monitor.GetHeartbeatIntegratedHealth(sessionID, pathID)
	expectedScore := 0.8 * 0.7 * 0.75 * (1.0 - 0.2) // control * data * overall * (1 - failure_rate)
	if integratedHealth.HeartbeatHealth < expectedScore-0.01 || integratedHealth.HeartbeatHealth > expectedScore+0.01 {
		t.Errorf("Expected health score around %f, got %f", expectedScore, integratedHealth.HeartbeatHealth)
	}
	
	// Cleanup
	monitor.StopMonitoring(sessionID)
}

// TestHealthMonitorWithoutHeartbeatData tests health monitor behavior without heartbeat data
func TestHealthMonitorWithoutHeartbeatData(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	// Create health monitor
	monitor := NewConnectionHealthMonitor(ctx)
	
	sessionID := "test-session"
	pathID := "test-path"
	paths := []string{pathID}
	
	// Start monitoring
	err := monitor.StartMonitoring(sessionID, paths)
	if err != nil {
		t.Fatalf("Failed to start monitoring: %v", err)
	}
	
	// Get integrated health without heartbeat data
	integratedHealth := monitor.GetHeartbeatIntegratedHealth(sessionID, pathID)
	if integratedHealth == nil {
		t.Error("Expected integrated health to be available even without heartbeat data")
	}
	
	// Should have default values
	if integratedHealth.HeartbeatHealth != 1.0 {
		t.Errorf("Expected default heartbeat health to be 1.0, got %f", integratedHealth.HeartbeatHealth)
	}
	
	if integratedHealth.HeartbeatTrend != HeartbeatHealthTrendStable {
		t.Errorf("Expected default trend to be stable, got %v", integratedHealth.HeartbeatTrend)
	}
	
	if integratedHealth.ControlPlaneHeartbeat != nil {
		t.Error("Expected no control plane heartbeat info without heartbeat data")
	}
	
	if integratedHealth.DataPlaneHeartbeat != nil {
		t.Error("Expected no data plane heartbeat info without heartbeat data")
	}
	
	// Cleanup
	monitor.StopMonitoring(sessionID)
}