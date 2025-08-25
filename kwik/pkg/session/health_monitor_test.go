package session

import (
	"context"
	"testing"
	"time"
)

func TestConnectionHealthMonitor_StartStopMonitoring(t *testing.T) {
	ctx := context.Background()
	monitor := NewConnectionHealthMonitor(ctx)
	defer monitor.cancel()

	sessionID := "test-session-1"
	paths := []string{"path1", "path2", "path3"}

	// Test starting monitoring
	err := monitor.StartMonitoring(sessionID, paths)
	if err != nil {
		t.Fatalf("Failed to start monitoring: %v", err)
	}

	// Wait for monitoring routine to update stats
	time.Sleep(1100 * time.Millisecond)

	// Verify session is being monitored
	stats := monitor.GetHealthStats()
	if stats.ActiveSessions != 1 {
		t.Errorf("Expected 1 active session, got %d", stats.ActiveSessions)
	}

	if stats.TotalPaths != len(paths) {
		t.Errorf("Expected %d total paths, got %d", len(paths), stats.TotalPaths)
	}

	// Test getting path health
	for _, pathID := range paths {
		pathHealth := monitor.GetPathHealth(sessionID, pathID)
		if pathHealth == nil {
			t.Errorf("Path health not found for path %s", pathID)
			continue
		}

		if pathHealth.PathID != pathID {
			t.Errorf("Expected path ID %s, got %s", pathID, pathHealth.PathID)
		}

		if pathHealth.Status != PathStatusActive {
			t.Errorf("Expected path status Active, got %v", pathHealth.Status)
		}

		if pathHealth.HealthScore != 100 {
			t.Errorf("Expected initial health score 100, got %d", pathHealth.HealthScore)
		}
	}

	// Test getting all path health
	allPaths := monitor.GetAllPathHealth(sessionID)
	if len(allPaths) != len(paths) {
		t.Errorf("Expected %d paths in GetAllPathHealth, got %d", len(paths), len(allPaths))
	}

	// Test stopping monitoring
	err = monitor.StopMonitoring(sessionID)
	if err != nil {
		t.Fatalf("Failed to stop monitoring: %v", err)
	}

	// Wait for monitoring routine to process the stop
	time.Sleep(1100 * time.Millisecond)

	// Verify session is no longer monitored
	stats = monitor.GetHealthStats()
	if stats.ActiveSessions != 0 {
		t.Errorf("Expected 0 active sessions after stop, got %d", stats.ActiveSessions)
	}

	// Verify path health returns nil after stopping
	pathHealth := monitor.GetPathHealth(sessionID, paths[0])
	if pathHealth != nil {
		t.Errorf("Expected nil path health after stopping monitoring, got %v", pathHealth)
	}
}

func TestConnectionHealthMonitor_UpdatePathMetrics(t *testing.T) {
	ctx := context.Background()
	monitor := NewConnectionHealthMonitor(ctx)
	defer monitor.cancel()

	sessionID := "test-session-2"
	pathID := "test-path"

	err := monitor.StartMonitoring(sessionID, []string{pathID})
	if err != nil {
		t.Fatalf("Failed to start monitoring: %v", err)
	}
	defer monitor.StopMonitoring(sessionID)

	// Test RTT update
	rtt := 50 * time.Millisecond
	err = monitor.UpdatePathMetrics(sessionID, pathID, PathMetricsUpdate{
		RTT: &rtt,
	})
	if err != nil {
		t.Fatalf("Failed to update RTT: %v", err)
	}

	pathHealth := monitor.GetPathHealth(sessionID, pathID)
	if pathHealth.RTT != rtt {
		t.Errorf("Expected RTT %v, got %v", rtt, pathHealth.RTT)
	}

	// Test packet statistics update
	err = monitor.UpdatePathMetrics(sessionID, pathID, PathMetricsUpdate{
		PacketSent: true,
		BytesSent:  1024,
	})
	if err != nil {
		t.Fatalf("Failed to update packet stats: %v", err)
	}

	err = monitor.UpdatePathMetrics(sessionID, pathID, PathMetricsUpdate{
		PacketAcked: true,
		BytesAcked:  1024,
	})
	if err != nil {
		t.Fatalf("Failed to update ack stats: %v", err)
	}

	// Test packet loss - send 2 more packets, lose 1
	err = monitor.UpdatePathMetrics(sessionID, pathID, PathMetricsUpdate{
		PacketSent: true,
	})
	if err != nil {
		t.Fatalf("Failed to update packet sent: %v", err)
	}

	err = monitor.UpdatePathMetrics(sessionID, pathID, PathMetricsUpdate{
		PacketSent: true,
	})
	if err != nil {
		t.Fatalf("Failed to update packet sent: %v", err)
	}

	err = monitor.UpdatePathMetrics(sessionID, pathID, PathMetricsUpdate{
		PacketLost: true,
	})
	if err != nil {
		t.Fatalf("Failed to update packet lost: %v", err)
	}

	pathHealth = monitor.GetPathHealth(sessionID, pathID)
	expectedLossRate := 1.0 / 3.0 // 1 lost out of 3 sent
	if pathHealth.PacketLoss < expectedLossRate-0.01 || pathHealth.PacketLoss > expectedLossRate+0.01 {
		t.Errorf("Expected packet loss rate ~%f, got %f", expectedLossRate, pathHealth.PacketLoss)
	}

	// Test heartbeat updates
	err = monitor.UpdatePathMetrics(sessionID, pathID, PathMetricsUpdate{
		HeartbeatSent: true,
	})
	if err != nil {
		t.Fatalf("Failed to update heartbeat sent: %v", err)
	}

	err = monitor.UpdatePathMetrics(sessionID, pathID, PathMetricsUpdate{
		HeartbeatReceived: true,
	})
	if err != nil {
		t.Fatalf("Failed to update heartbeat received: %v", err)
	}

	// Test activity update
	beforeActivity := pathHealth.LastActivity
	time.Sleep(10 * time.Millisecond) // Ensure time difference

	err = monitor.UpdatePathMetrics(sessionID, pathID, PathMetricsUpdate{
		Activity: true,
	})
	if err != nil {
		t.Fatalf("Failed to update activity: %v", err)
	}

	pathHealth = monitor.GetPathHealth(sessionID, pathID)
	if !pathHealth.LastActivity.After(beforeActivity) {
		t.Errorf("Expected LastActivity to be updated")
	}
}

func TestHealthThresholds_Defaults(t *testing.T) {
	thresholds := DefaultHealthThresholds()

	// Test that defaults are reasonable
	if thresholds.RTTWarningThreshold <= 0 {
		t.Errorf("RTT warning threshold should be positive, got %v", thresholds.RTTWarningThreshold)
	}

	if thresholds.RTTCriticalThreshold <= thresholds.RTTWarningThreshold {
		t.Errorf("RTT critical threshold should be greater than warning threshold")
	}

	if thresholds.PacketLossWarningThreshold <= 0 || thresholds.PacketLossWarningThreshold >= 1 {
		t.Errorf("Packet loss warning threshold should be between 0 and 1, got %f", thresholds.PacketLossWarningThreshold)
	}

	if thresholds.PacketLossCriticalThreshold <= thresholds.PacketLossWarningThreshold {
		t.Errorf("Packet loss critical threshold should be greater than warning threshold")
	}

	if thresholds.HealthScoreWarningThreshold <= thresholds.HealthScoreCriticalThreshold {
		t.Errorf("Health score warning threshold should be greater than critical threshold")
	}

	if thresholds.MinHeartbeatInterval <= 0 {
		t.Errorf("Min heartbeat interval should be positive, got %v", thresholds.MinHeartbeatInterval)
	}

	if thresholds.MaxHeartbeatInterval <= thresholds.MinHeartbeatInterval {
		t.Errorf("Max heartbeat interval should be greater than min interval")
	}
}

func TestConnectionHealthMonitor_SetHealthThresholds(t *testing.T) {
	ctx := context.Background()
	monitor := NewConnectionHealthMonitor(ctx)
	defer monitor.cancel()

	customThresholds := HealthThresholds{
		RTTWarningThreshold:          200 * time.Millisecond,
		RTTCriticalThreshold:         1000 * time.Millisecond,
		PacketLossWarningThreshold:   0.02,
		PacketLossCriticalThreshold:  0.10,
		HealthScoreWarningThreshold:  80,
		HealthScoreCriticalThreshold: 40,
		HeartbeatTimeoutThreshold:    60 * time.Second,
		HeartbeatFailureThreshold:    5,
		HeartbeatCriticalThreshold:   10,
		InactivityWarningThreshold:   120 * time.Second,
		InactivityCriticalThreshold:  300 * time.Second,
		MinHeartbeatInterval:         10 * time.Second,
		MaxHeartbeatInterval:         120 * time.Second,
		HeartbeatAdaptationFactor:    0.2,
	}

	err := monitor.SetHealthThresholds(customThresholds)
	if err != nil {
		t.Fatalf("Failed to set health thresholds: %v", err)
	}

	// Verify thresholds were set by checking internal state
	monitor.mutex.RLock()
	if monitor.thresholds.RTTWarningThreshold != customThresholds.RTTWarningThreshold {
		t.Errorf("RTT warning threshold not set correctly")
	}
	if monitor.thresholds.PacketLossWarningThreshold != customThresholds.PacketLossWarningThreshold {
		t.Errorf("Packet loss warning threshold not set correctly")
	}
	monitor.mutex.RUnlock()
}

func TestConnectionHealthMonitor_FailoverCallback(t *testing.T) {
	ctx := context.Background()
	monitor := NewConnectionHealthMonitor(ctx)
	defer monitor.cancel()

	callback := func(sessionID, fromPath, toPath string, reason FailoverReason) {
		// Callback implementation for testing
		// Variables are used within this closure
		_ = sessionID
		_ = fromPath
		_ = toPath
		_ = reason
	}

	err := monitor.RegisterFailoverCallback(callback)
	if err != nil {
		t.Fatalf("Failed to register failover callback: %v", err)
	}

	// Verify callback was registered
	monitor.mutex.RLock()
	if len(monitor.callbacks) != 1 {
		t.Errorf("Expected 1 callback registered, got %d", len(monitor.callbacks))
	}
	monitor.mutex.RUnlock()

	// Note: Testing actual callback invocation would require simulating
	// failover conditions, which is covered in integration tests
}

func TestPathHealth_StatusTransitions(t *testing.T) {
	ctx := context.Background()
	monitor := NewConnectionHealthMonitor(ctx)
	defer monitor.cancel()

	sessionID := "test-session-status"
	pathID := "test-path-status"

	err := monitor.StartMonitoring(sessionID, []string{pathID})
	if err != nil {
		t.Fatalf("Failed to start monitoring: %v", err)
	}
	defer monitor.StopMonitoring(sessionID)

	// Initial status should be Active
	pathHealth := monitor.GetPathHealth(sessionID, pathID)
	if pathHealth.Status != PathStatusActive {
		t.Errorf("Expected initial status Active, got %v", pathHealth.Status)
	}

	// Simulate high RTT to degrade health
	highRTT := 600 * time.Millisecond // Above critical threshold
	err = monitor.UpdatePathMetrics(sessionID, pathID, PathMetricsUpdate{
		RTT: &highRTT,
	})
	if err != nil {
		t.Fatalf("Failed to update RTT: %v", err)
	}

	// Wait for health update
	time.Sleep(1100 * time.Millisecond) // Slightly more than monitoring interval

	pathHealth = monitor.GetPathHealth(sessionID, pathID)
	if pathHealth.HealthScore >= 70 { // Should be degraded due to high RTT
		t.Errorf("Expected health score to be degraded due to high RTT, got %d", pathHealth.HealthScore)
	}
}

func TestFailoverReason_String(t *testing.T) {
	testCases := []struct {
		reason   FailoverReason
		expected string
	}{
		{FailoverReasonHealthDegraded, "HEALTH_DEGRADED"},
		{FailoverReasonPathFailed, "PATH_FAILED"},
		{FailoverReasonTimeout, "TIMEOUT"},
		{FailoverReasonManual, "MANUAL"},
		{FailoverReasonLoadBalancing, "LOAD_BALANCING"},
		{FailoverReason(999), "UNKNOWN"},
	}

	for _, tc := range testCases {
		result := tc.reason.String()
		if result != tc.expected {
			t.Errorf("Expected %s, got %s for reason %d", tc.expected, result, int(tc.reason))
		}
	}
}

func TestConnectionHealthMonitor_GetHealthStats(t *testing.T) {
	ctx := context.Background()
	monitor := NewConnectionHealthMonitor(ctx)
	defer monitor.cancel()

	// Test empty stats
	stats := monitor.GetHealthStats()
	if stats.ActiveSessions != 0 {
		t.Errorf("Expected 0 active sessions initially, got %d", stats.ActiveSessions)
	}

	// Add some sessions
	sessionID1 := "session1"
	sessionID2 := "session2"
	paths1 := []string{"path1", "path2"}
	paths2 := []string{"path3", "path4", "path5"}

	err := monitor.StartMonitoring(sessionID1, paths1)
	if err != nil {
		t.Fatalf("Failed to start monitoring session1: %v", err)
	}

	err = monitor.StartMonitoring(sessionID2, paths2)
	if err != nil {
		t.Fatalf("Failed to start monitoring session2: %v", err)
	}

	// Wait for stats update
	time.Sleep(1100 * time.Millisecond)

	stats = monitor.GetHealthStats()
	if stats.ActiveSessions != 2 {
		t.Errorf("Expected 2 active sessions, got %d", stats.ActiveSessions)
	}

	expectedTotalPaths := len(paths1) + len(paths2)
	if stats.TotalPaths != expectedTotalPaths {
		t.Errorf("Expected %d total paths, got %d", expectedTotalPaths, stats.TotalPaths)
	}

	// Check session stats
	if len(stats.SessionStats) != 2 {
		t.Errorf("Expected 2 session stats, got %d", len(stats.SessionStats))
	}

	sessionStats1, exists := stats.SessionStats[sessionID1]
	if !exists {
		t.Errorf("Session stats not found for %s", sessionID1)
	} else {
		if sessionStats1.PathCount != len(paths1) {
			t.Errorf("Expected %d paths for session1, got %d", len(paths1), sessionStats1.PathCount)
		}
	}

	// Clean up
	monitor.StopMonitoring(sessionID1)
	monitor.StopMonitoring(sessionID2)
}
