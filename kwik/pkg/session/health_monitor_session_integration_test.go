package session

import (
	"kwik/pkg/logger"
	"kwik/pkg/transport"
	"testing"
	"time"
)

// TestHealthMonitorSessionIntegration tests the integration of health monitor into session management
func TestHealthMonitorSessionIntegration(t *testing.T) {
	t.Run("ClientSessionHealthIntegration", func(t *testing.T) {
		testClientSessionHealthIntegration(t)
	})
	t.Run("ServerSessionHealthIntegration", func(t *testing.T) {
		testServerSessionHealthIntegration(t)
	})
	t.Run("HealthMonitorInitialization", func(t *testing.T) {
		testHealthMonitorInitialization(t)
	})
	t.Run("HealthMetricsUpdate", func(t *testing.T) {
		testHealthMetricsUpdate(t)
	})
}

func testClientSessionHealthIntegration(t *testing.T) {
	// Create path manager
	pathManager := transport.NewPathManager()

	// Create client session
	config := DefaultSessionConfig()
	clientSession := NewClientSession(pathManager, config)
	defer clientSession.Close()

	// Verify health monitor is initialized
	healthMonitor := clientSession.GetHealthMonitor()
	if healthMonitor == nil {
		t.Fatal("Health monitor not initialized in client session")
	}

	// Verify health monitor methods are available
	if clientSession.GetPathHealthStatus("nonexistent") != nil {
		t.Error("Expected nil for nonexistent path health status")
	}

	// Test IsPathHealthy with nonexistent path
	if clientSession.IsPathHealthy("nonexistent") {
		t.Error("Expected false for nonexistent path health check")
	}

	// Test GetBestAvailablePath with no paths
	bestPath := clientSession.GetBestAvailablePath()
	if bestPath != nil {
		t.Error("Expected nil for best path when no paths available")
	}

	// Test UpdatePathHealthMetrics (should not crash)
	rtt := 50 * time.Millisecond
	clientSession.UpdatePathHealthMetrics("test_path", &rtt, true, 1024)
}

func testServerSessionHealthIntegration(t *testing.T) {
	// Create path manager
	pathManager := transport.NewPathManager()

	// Create server session
	config := DefaultSessionConfig()
	serverSession := NewServerSession("test-session", pathManager, config, &logger.MockLogger{})
	defer serverSession.Close()

	// Verify health monitor is initialized
	healthMonitor := serverSession.GetHealthMonitor()
	if healthMonitor == nil {
		t.Fatal("Health monitor not initialized in server session")
	}

	// Verify health monitor methods are available
	if serverSession.GetPathHealthStatus("nonexistent") != nil {
		t.Error("Expected nil for nonexistent path health status")
	}

	// Test IsPathHealthy with nonexistent path
	if serverSession.IsPathHealthy("nonexistent") {
		t.Error("Expected false for nonexistent path health check")
	}

	// Test GetBestAvailablePath with no paths
	bestPath := serverSession.GetBestAvailablePath()
	if bestPath != nil {
		t.Error("Expected nil for best path when no paths available")
	}

	// Test UpdatePathHealthMetrics (should not crash)
	rtt := 30 * time.Millisecond
	serverSession.UpdatePathHealthMetrics("test_path", &rtt, true, 2048)
}

func testHealthMonitorInitialization(t *testing.T) {
	// Test client session health monitor initialization
	pathManager := transport.NewPathManager()
	config := DefaultSessionConfig()
	clientSession := NewClientSession(pathManager, config)
	defer clientSession.Close()

	// Verify health monitor is initialized
	healthMonitor := clientSession.GetHealthMonitor()
	if healthMonitor == nil {
		t.Fatal("Health monitor not initialized in client session")
	}

	// Test server session health monitor initialization
	serverSession := NewServerSession("test-session", pathManager, config, &logger.MockLogger{})
	defer serverSession.Close()

	// Verify health monitor is initialized
	serverHealthMonitor := serverSession.GetHealthMonitor()
	if serverHealthMonitor == nil {
		t.Fatal("Health monitor not initialized in server session")
	}

	// Verify both sessions have different health monitor instances
	if healthMonitor == serverHealthMonitor {
		t.Error("Expected different health monitor instances for client and server sessions")
	}
}

func testHealthMetricsUpdate(t *testing.T) {
	// Create client session
	pathManager := transport.NewPathManager()
	config := DefaultSessionConfig()
	clientSession := NewClientSession(pathManager, config)
	defer clientSession.Close()

	// Test UpdatePathHealthMetrics method
	rtt := 50 * time.Millisecond
	clientSession.UpdatePathHealthMetrics("test_path", &rtt, true, 1024)

	// Test with nil RTT
	clientSession.UpdatePathHealthMetrics("test_path", nil, false, 512)

	// Test with zero bytes
	clientSession.UpdatePathHealthMetrics("test_path", &rtt, true, 0)

	// Create server session and test the same
	serverSession := NewServerSession("test-session", pathManager, config, &logger.MockLogger{})
	defer serverSession.Close()

	// Test UpdatePathHealthMetrics method
	serverSession.UpdatePathHealthMetrics("server_test_path", &rtt, true, 2048)

	// Test with nil RTT
	serverSession.UpdatePathHealthMetrics("server_test_path", nil, false, 1024)

	// Test with zero bytes
	serverSession.UpdatePathHealthMetrics("server_test_path", &rtt, true, 0)
}
