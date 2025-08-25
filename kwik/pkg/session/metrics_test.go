package session

import (
	"testing"
	"time"
)

func TestMetricsManager_BasicOperations(t *testing.T) {
	sessionID := "test-session-123"
	metricsManager := NewMetricsManager(sessionID, DefaultMetricsConfig())

	// Test path creation
	pathID := "test-path-1"
	address := "127.0.0.1:8080"
	metricsManager.RecordPathCreated(pathID, address, true)

	pathMetrics := metricsManager.GetPathMetrics(pathID)
	if pathMetrics == nil {
		t.Fatal("Expected path metrics to be created")
	}

	if pathMetrics.PathID != pathID {
		t.Errorf("Expected path ID %s, got %s", pathID, pathMetrics.PathID)
	}

	if pathMetrics.Address != address {
		t.Errorf("Expected address %s, got %s", address, pathMetrics.Address)
	}

	if !pathMetrics.IsPrimary {
		t.Error("Expected path to be marked as primary")
	}

	// Test stream creation
	streamID := uint64(1)
	metricsManager.RecordStreamCreated(streamID, pathID)

	streamMetrics := metricsManager.GetStreamMetrics(streamID)
	if streamMetrics == nil {
		t.Fatal("Expected stream metrics to be created")
	}

	if streamMetrics.StreamID != streamID {
		t.Errorf("Expected stream ID %d, got %d", streamID, streamMetrics.StreamID)
	}

	if streamMetrics.PathID != pathID {
		t.Errorf("Expected path ID %s, got %s", pathID, streamMetrics.PathID)
	}

	// Test data transmission recording
	dataSize := uint64(1024)
	metricsManager.RecordDataSent(pathID, streamID, dataSize)

	// Verify connection metrics
	connMetrics := metricsManager.GetConnectionMetrics()
	if connMetrics.BytesSent != dataSize {
		t.Errorf("Expected bytes sent %d, got %d", dataSize, connMetrics.BytesSent)
	}

	if connMetrics.PacketsSent != 1 {
		t.Errorf("Expected packets sent 1, got %d", connMetrics.PacketsSent)
	}

	// Verify path metrics updated
	updatedPathMetrics := metricsManager.GetPathMetrics(pathID)
	if updatedPathMetrics.BytesSent != dataSize {
		t.Errorf("Expected path bytes sent %d, got %d", dataSize, updatedPathMetrics.BytesSent)
	}

	// Verify stream metrics updated
	updatedStreamMetrics := metricsManager.GetStreamMetrics(streamID)
	if updatedStreamMetrics.BytesSent != dataSize {
		t.Errorf("Expected stream bytes sent %d, got %d", dataSize, updatedStreamMetrics.BytesSent)
	}

	// Test RTT recording
	rtt := 50 * time.Millisecond
	metricsManager.RecordRTT(pathID, rtt)

	finalConnMetrics := metricsManager.GetConnectionMetrics()
	if finalConnMetrics.AverageRTT != rtt {
		t.Errorf("Expected average RTT %v, got %v", rtt, finalConnMetrics.AverageRTT)
	}

	// Test error recording
	metricsManager.RecordError("timeout", "medium", "connection timeout", "client", pathID, streamID)

	errorMetrics := metricsManager.GetErrorMetrics()
	if errorMetrics.TotalErrors != 1 {
		t.Errorf("Expected total errors 1, got %d", errorMetrics.TotalErrors)
	}

	if errorMetrics.ErrorsByType["timeout"] != 1 {
		t.Errorf("Expected timeout errors 1, got %d", errorMetrics.ErrorsByType["timeout"])
	}

	if len(errorMetrics.RecentErrors) != 1 {
		t.Errorf("Expected 1 recent error, got %d", len(errorMetrics.RecentErrors))
	}
}

func TestMetricsCollector_BasicOperations(t *testing.T) {
	sessionID := "test-session-456"
	metricsManager := NewMetricsManager(sessionID, DefaultMetricsConfig())

	config := DefaultMetricsCollectorConfig()
	metricsCollector := NewMetricsCollector(metricsManager, config)

	// Add some test data
	metricsManager.RecordPathCreated("test-path", "127.0.0.1:9090", false)
	metricsManager.RecordStreamCreated(1, "test-path")
	metricsManager.RecordDataSent("test-path", 1, 2048)

	// Get metrics summary without starting collection goroutines
	summary := metricsCollector.GetMetricsSummary()
	if summary == nil {
		t.Fatal("Expected metrics summary to be available")
	}

	if summary.Connection == nil {
		t.Fatal("Expected connection metrics in summary")
	}

	if len(summary.Paths) == 0 {
		t.Fatal("Expected path metrics in summary")
	}

	if summary.Performance == nil {
		t.Fatal("Expected performance metrics in summary")
	}
}

func TestMetricsReporter_GenerateReport(t *testing.T) {
	sessionID := "test-session-789"
	metricsManager := NewMetricsManager(sessionID, DefaultMetricsConfig())
	metricsCollector := NewMetricsCollector(metricsManager, DefaultMetricsCollectorConfig())

	// Add some test data
	metricsManager.RecordPathCreated("primary-path", "127.0.0.1:8080", true)
	metricsManager.RecordPathCreated("secondary-path", "127.0.0.1:8081", false)
	metricsManager.RecordStreamCreated(1, "primary-path")
	metricsManager.RecordStreamCreated(2, "secondary-path")
	metricsManager.RecordDataSent("primary-path", 1, 1024)
	metricsManager.RecordDataReceived("secondary-path", 2, 512)
	metricsManager.RecordRTT("primary-path", 25*time.Millisecond)
	metricsManager.RecordError("network", "low", "packet loss detected", "transport", "primary-path", 1)

	reporter := NewMetricsReporter(metricsCollector)
	report := reporter.GenerateReport()

	if report == "" {
		t.Fatal("Expected non-empty metrics report")
	}

	// Check that report contains expected sections
	expectedSections := []string{
		"KWIK Transport Metrics Report",
		"Connection Metrics:",
		"Path Metrics:",
		"Performance Metrics:",
		"Error Metrics:",
	}

	for _, section := range expectedSections {
		if !contains(report, section) {
			t.Errorf("Expected report to contain section: %s", section)
		}
	}

	// Check that report contains our test data
	if !contains(report, sessionID) {
		t.Error("Expected report to contain session ID")
	}

	if !contains(report, "primary-path") {
		t.Error("Expected report to contain primary path")
	}

	if !contains(report, "1024") {
		t.Error("Expected report to contain bytes sent data")
	}
}

// Helper function to check if string contains substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) &&
		(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr ||
			containsSubstring(s, substr)))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
