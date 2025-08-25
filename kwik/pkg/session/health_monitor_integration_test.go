package session

import (
	"kwik/pkg/logger"
	"kwik/pkg/transport"
	"testing"
	"time"
)

// TestHealthMonitorIntegrationClientSession tests health monitor integration in client sessions
func TestHealthMonitorIntegrationClientSession(t *testing.T) {
	// Create path manager
	pathManager := transport.NewPathManager()

	// Create client session
	clientSession := NewClientSession(pathManager, nil)
	defer clientSession.Close()

	// Verify health monitor is initialized
	if clientSession.GetHealthMonitor() == nil {
		t.Fatal("Health monitor should be initialized in client session")
	}

	// Verify heartbeat manager is initialized
	if clientSession.heartbeatManager == nil {
		t.Fatal("Heartbeat manager should be initialized in client session")
	}

	// Test health monitor methods
	sessionID := clientSession.sessionID

	// Test with a mock path ID for health monitoring
	testPathID := "test-path-1"

	// Start health monitoring for the path
	err := clientSession.healthMonitor.StartMonitoring(sessionID, []string{testPathID})
	if err != nil {
		t.Fatalf("Failed to start path monitoring: %v", err)
	}

	// Test path metrics update
	metrics := PathMetricsUpdate{
		RTT:      &[]time.Duration{15 * time.Millisecond}[0],
		Activity: true,
	}

	clientSession.healthMonitor.UpdatePathMetrics(sessionID, testPathID, metrics)

	// Wait a bit for processing
	time.Sleep(100 * time.Millisecond)

	// Get path health
	pathHealth := clientSession.healthMonitor.GetPathHealth(sessionID, testPathID)
	if pathHealth == nil {
		t.Fatal("Path health should be available")
	}

	if pathHealth.Status == PathStatusDead {
		t.Error("Path health status should not be dead after metrics update")
	}

	// Test session-level health methods
	sessionPathHealth := clientSession.GetPathHealthStatus(testPathID)
	if sessionPathHealth == nil {
		t.Error("Session should provide path health status")
	}

	// Test health-aware methods
	isHealthy := clientSession.IsPathHealthy(testPathID)
	t.Logf("Path %s is healthy: %v", testPathID, isHealthy)

	// Test best path selection
	bestPath := clientSession.GetBestAvailablePath()
	t.Logf("Best available path: %v", bestPath)

	// Test health metrics update
	rtt := 25 * time.Millisecond
	clientSession.UpdatePathHealthMetrics(testPathID, &rtt, true, 1024)
}

// TestHealthMonitorIntegrationServerSession tests health monitor integration in server sessions
func TestHealthMonitorIntegrationServerSession(t *testing.T) {
	// Create path manager
	pathManager := transport.NewPathManager()

	// Create server session
	serverSession := NewServerSession("test-server-session", pathManager, nil, &logger.MockLogger{})
	defer serverSession.Close()

	// Verify health monitor is initialized
	if serverSession.GetHealthMonitor() == nil {
		t.Fatal("Health monitor should be initialized in server session")
	}

	// Verify heartbeat manager is initialized
	if serverSession.heartbeatManager == nil {
		t.Fatal("Heartbeat manager should be initialized in server session")
	}

	// Test health monitor methods
	sessionID := serverSession.sessionID

	// Test with a mock path ID for health monitoring
	testPathID := "test-path-2"

	// Start health monitoring for the path
	err := serverSession.healthMonitor.StartMonitoring(sessionID, []string{testPathID})
	if err != nil {
		t.Fatalf("Failed to start path monitoring: %v", err)
	}

	// Test path metrics update
	metrics := PathMetricsUpdate{
		RTT:      &[]time.Duration{20 * time.Millisecond}[0],
		Activity: true,
	}

	serverSession.healthMonitor.UpdatePathMetrics(sessionID, testPathID, metrics)

	// Wait a bit for processing
	time.Sleep(100 * time.Millisecond)

	// Get path health
	pathHealth := serverSession.healthMonitor.GetPathHealth(sessionID, testPathID)
	if pathHealth == nil {
		t.Fatal("Path health should be available")
	}

	if pathHealth.Status == PathStatusDead {
		t.Error("Path health status should not be dead after metrics update")
	}

	// Test session-level health methods
	sessionPathHealth := serverSession.GetPathHealthStatus(testPathID)
	if sessionPathHealth == nil {
		t.Error("Session should provide path health status")
	}

	// Test health-aware methods
	isHealthy := serverSession.IsPathHealthy(testPathID)
	t.Logf("Path %s is healthy: %v", testPathID, isHealthy)

	// Test best path selection
	bestPath := serverSession.GetBestAvailablePath()
	t.Logf("Best available path: %v", bestPath)

	// Test health metrics update
	rtt := 35 * time.Millisecond
	serverSession.UpdatePathHealthMetrics(testPathID, &rtt, true, 2048)
}

// TestHealthMonitorPathFailover tests path failover integration
func TestHealthMonitorPathFailover(t *testing.T) {
	// Create path manager
	pathManager := transport.NewPathManager()

	// Create client session
	clientSession := NewClientSession(pathManager, nil)
	defer clientSession.Close()

	sessionID := clientSession.sessionID

	// Test with multiple paths
	primaryPathID := "primary-path"
	backupPathID := "backup-path"

	// Start health monitoring for both paths
	err := clientSession.healthMonitor.StartMonitoring(sessionID, []string{primaryPathID, backupPathID})
	if err != nil {
		t.Fatalf("Failed to start path monitoring: %v", err)
	}

	// Simulate good health for backup path
	goodRTT := 20 * time.Millisecond
	goodMetrics := PathMetricsUpdate{
		RTT:         &goodRTT,
		PacketAcked: true,
		Activity:    true,
	}

	clientSession.healthMonitor.UpdatePathMetrics(sessionID, backupPathID, goodMetrics)

	// Simulate poor health for primary path
	badRTT := 500 * time.Millisecond
	badMetrics := PathMetricsUpdate{
		RTT:        &badRTT,
		PacketLost: true,
		Activity:   false,
	}

	for i := 0; i < 10; i++ {
		clientSession.healthMonitor.UpdatePathMetrics(sessionID, primaryPathID, badMetrics)
	}

	// Wait for health monitor to process updates
	time.Sleep(200 * time.Millisecond)

	// Check path health status
	primaryHealth := clientSession.GetPathHealthStatus(primaryPathID)
	backupHealth := clientSession.GetPathHealthStatus(backupPathID)

	if primaryHealth != nil {
		t.Logf("Primary path health score: %d", primaryHealth.HealthScore)
	}

	if backupHealth != nil {
		t.Logf("Backup path health score: %d", backupHealth.HealthScore)
	}

	// Test health-aware routing decisions
	primaryHealthy := clientSession.IsPathHealthy(primaryPathID)
	backupHealthy := clientSession.IsPathHealthy(backupPathID)

	t.Logf("Primary path healthy: %v, Backup path healthy: %v", primaryHealthy, backupHealthy)

	// Test best path selection
	bestPath := clientSession.GetBestAvailablePath()
	t.Logf("Best available path: %v", bestPath)
}

// TestHealthMonitorAdaptiveHeartbeats tests adaptive heartbeat integration
func TestHealthMonitorAdaptiveHeartbeats(t *testing.T) {
	// Create path manager
	pathManager := transport.NewPathManager()

	// Create client session
	clientSession := NewClientSession(pathManager, nil)
	defer clientSession.Close()

	sessionID := clientSession.sessionID

	// Test with a path
	testPathID := "adaptive-path"

	// Start health monitoring
	err := clientSession.healthMonitor.StartMonitoring(sessionID, []string{testPathID})
	if err != nil {
		t.Fatalf("Failed to start path monitoring: %v", err)
	}

	// Start heartbeat management
	clientSession.startHeartbeatManagement()

	// Simulate varying path conditions
	conditions := []struct {
		rtt     time.Duration
		success bool
		label   string
	}{
		{15 * time.Millisecond, true, "good"},
		{50 * time.Millisecond, true, "moderate"},
		{200 * time.Millisecond, false, "poor"},
		{10 * time.Millisecond, true, "excellent"},
	}

	for _, condition := range conditions {
		metrics := PathMetricsUpdate{
			RTT:      &condition.rtt,
			Activity: true,
		}

		if condition.success {
			metrics.PacketAcked = true
		} else {
			metrics.PacketLost = true
		}

		clientSession.healthMonitor.UpdatePathMetrics(sessionID, testPathID, metrics)

		// Update heartbeat manager with path health
		if clientSession.heartbeatManager != nil {
			pathHealth := clientSession.GetPathHealthStatus(testPathID)
			if pathHealth != nil {
				err = clientSession.heartbeatManager.UpdatePathHealth(sessionID, testPathID, condition.rtt, !condition.success, int(pathHealth.HealthScore))
				if err != nil {
					t.Logf("Failed to update heartbeat manager: %v", err)
				}
			}
		}

		// Wait for adaptation
		time.Sleep(100 * time.Millisecond)

		// Check heartbeat stats
		if clientSession.heartbeatManager != nil {
			stats := clientSession.heartbeatManager.GetHeartbeatStats(sessionID, testPathID)
			if stats != nil {
				t.Logf("Condition %s: RTT=%v, Interval=%v, Fails=%d",
					condition.label, condition.rtt, stats.CurrentInterval, stats.ConsecutiveFails)
			}
		}
	}
}
