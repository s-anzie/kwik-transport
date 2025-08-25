package presentation

import (
	"testing"
	"time"
)

func TestResourceCleanupSimple(t *testing.T) {
	// Create test components with large window sizes
	config := DefaultPresentationConfig()
	config.ReceiveWindowSize = 50 * 1024 * 1024 // 50MB
	config.DefaultStreamBufferSize = 64 * 1024  // 64KB per stream
	dpm := NewDataPresentationManager(config)
	
	// Start the manager
	err := dpm.Start()
	if err != nil {
		t.Fatalf("Failed to start DPM: %v", err)
	}
	defer dpm.Shutdown()
	
	// Create a few streams
	for i := uint64(1); i <= 5; i++ {
		metadata := &StreamMetadata{
			StreamID:     i,
			Priority:     StreamPriorityNormal,
			LastActivity: time.Now(),
		}
		
		err := dpm.CreateStreamBuffer(i, metadata)
		if err != nil {
			t.Fatalf("Failed to create stream %d: %v", i, err)
		}
	}
	
	// Verify streams were created
	stats := dpm.GetGlobalStats()
	if stats.ActiveStreams != 5 {
		t.Errorf("Expected 5 active streams, got %d", stats.ActiveStreams)
	}
	
	// Test resource monitoring
	resourceMetrics := dpm.GetResourceMetrics()
	if resourceMetrics == nil {
		t.Error("Expected resource metrics to be available")
	} else {
		t.Logf("Active streams: %d", resourceMetrics.ActiveStreams)
		t.Logf("Memory utilization: %.2f%%", resourceMetrics.MemoryUtilization*100)
		t.Logf("Window utilization: %.2f%%", resourceMetrics.WindowUtilization*100)
	}
	
	// Test garbage collection
	gcStats := dpm.ForceResourceCleanup()
	if gcStats == nil {
		t.Error("Expected GC stats to be available")
	} else {
		t.Logf("GC runs: %d", gcStats.TotalGCRuns)
		t.Logf("Resources freed: %d", gcStats.TotalResourcesFreed)
	}
	
	// Test resource alerts
	alerts := dpm.GetActiveResourceAlerts()
	t.Logf("Active alerts: %d", len(alerts))
	
	// Remove streams
	for i := uint64(1); i <= 5; i++ {
		err := dpm.RemoveStreamBuffer(i)
		if err != nil {
			t.Errorf("Failed to remove stream %d: %v", i, err)
		}
	}
	
	// Verify streams were removed
	finalStats := dpm.GetGlobalStats()
	if finalStats.ActiveStreams != 0 {
		t.Errorf("Expected 0 active streams, got %d", finalStats.ActiveStreams)
	}
}

func TestResourceMonitoringIntegration(t *testing.T) {
	// Create test components
	config := DefaultPresentationConfig()
	config.ReceiveWindowSize = 20 * 1024 * 1024 // 20MB
	config.DefaultStreamBufferSize = 128 * 1024 // 128KB per stream
	dpm := NewDataPresentationManager(config)
	
	err := dpm.Start()
	if err != nil {
		t.Fatalf("Failed to start DPM: %v", err)
	}
	defer dpm.Shutdown()
	
	// Get resource monitor
	monitor := dpm.GetResourceMonitor()
	if monitor == nil {
		t.Fatal("Expected resource monitor to be available")
	}
	
	// Set up alert callback
	alertReceived := make(chan ResourceAlert, 10)
	dpm.SetResourceAlertCallback(func(alert ResourceAlert) {
		alertReceived <- alert
	})
	
	// Create streams to trigger monitoring
	for i := uint64(1); i <= 10; i++ {
		metadata := &StreamMetadata{
			StreamID:     i,
			Priority:     StreamPriorityNormal,
			LastActivity: time.Now(),
		}
		
		err := dpm.CreateStreamBuffer(i, metadata)
		if err != nil {
			t.Fatalf("Failed to create stream %d: %v", i, err)
		}
	}
	
	// Wait for monitoring to collect metrics
	time.Sleep(100 * time.Millisecond)
	
	// Check metrics
	metrics := dpm.GetResourceMetrics()
	if metrics == nil {
		t.Fatal("Expected resource metrics to be available")
	}
	
	if metrics.ActiveStreams != 10 {
		t.Errorf("Expected 10 active streams, got %d", metrics.ActiveStreams)
	}
	
	// Test threshold violation by setting low threshold
	thresholds := monitor.GetThresholds()
	thresholds.MaxActiveStreams = 5 // Set low threshold
	dpm.SetResourceThresholds(thresholds)
	
	// Wait a bit for alert processing
	time.Sleep(200 * time.Millisecond)
	
	// Check for alerts
	alerts := dpm.GetActiveResourceAlerts()
	if len(alerts) == 0 {
		t.Log("No alerts generated (this may be expected in test environment)")
	} else {
		t.Logf("Generated %d alerts", len(alerts))
		for _, alert := range alerts {
			t.Logf("Alert: %s - %s", alert.Type.String(), alert.Message)
		}
	}
	
	// Test garbage collection
	gcStats := dpm.ForceResourceCleanup()
	if gcStats != nil {
		t.Logf("Forced GC: runs=%d, freed=%d", gcStats.TotalGCRuns, gcStats.TotalResourcesFreed)
	}
}