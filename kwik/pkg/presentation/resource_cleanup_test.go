package presentation

import (
	"testing"
	"time"
	"sync"
	"fmt"
)

func TestResourceGarbageCollectorBasic(t *testing.T) {
	// Create test components
	config := DefaultPresentationConfig()
	config.ReceiveWindowSize = 10 * 1024 * 1024 // 10MB
	config.DefaultStreamBufferSize = 64 * 1024  // 64KB per stream
	dpm := NewDataPresentationManager(config)
	
	windowConfig := &WindowManagerConfig{
		InitialSize:           10 * 1024 * 1024, // 10MB
		MaxSize:               20 * 1024 * 1024, // 20MB
		MinSize:               5 * 1024 * 1024,  // 5MB
		BackpressureThreshold: 0.8,
		SlideSize:             4096,
		AutoSlideEnabled:      true,
		AutoSlideInterval:     100 * time.Millisecond,
		MaxStreams:            1000,
	}
	windowManager := NewReceiveWindowManager(windowConfig)
	
	// Create garbage collector
	gcConfig := DefaultGCConfig()
	gcConfig.GCInterval = 100 * time.Millisecond // Fast GC for testing
	gcConfig.MaxIdleTime = 200 * time.Millisecond
	
	gc := NewResourceGarbageCollector(dpm, windowManager, gcConfig)
	
	// Start components
	err := dpm.Start()
	if err != nil {
		t.Fatalf("Failed to start DPM: %v", err)
	}
	defer dpm.Shutdown()
	
	err = gc.Start()
	if err != nil {
		t.Fatalf("Failed to start GC: %v", err)
	}
	defer gc.Stop()
	
	// Create some streams
	streamIDs := []uint64{1, 2, 3, 4, 5}
	for _, streamID := range streamIDs {
		metadata := &StreamMetadata{
			StreamID:     streamID,
			Priority:     StreamPriorityNormal,
			LastActivity: time.Now(),
		}
		
		err := dpm.CreateStreamBuffer(streamID, metadata)
		if err != nil {
			t.Fatalf("Failed to create stream %d: %v", streamID, err)
		}
	}
	
	// Verify streams were created
	stats := dpm.GetGlobalStats()
	if stats.ActiveStreams != len(streamIDs) {
		t.Errorf("Expected %d active streams, got %d", len(streamIDs), stats.ActiveStreams)
	}
	
	// Wait for streams to become idle
	time.Sleep(300 * time.Millisecond)
	
	// Force GC run
	gcStats := gc.ForceGC()
	
	// Verify GC ran
	if gcStats.TotalGCRuns == 0 {
		t.Error("Expected GC to run at least once")
	}
	
	// Check if orphaned resources were detected
	orphaned := gc.GetOrphanedResources()
	if len(orphaned) == 0 {
		t.Log("No orphaned resources detected (this may be expected)")
	}
	
	// Clean up specific stream
	err = gc.CleanupStreamResources(streamIDs[0])
	if err != nil {
		t.Errorf("Failed to cleanup stream resources: %v", err)
	}
}

func TestResourceGarbageCollectorConcurrency(t *testing.T) {
	config := DefaultPresentationConfig()
	dpm := NewDataPresentationManager(config)
	
	windowConfig := &WindowManagerConfig{
		InitialSize:           2 * 1024 * 1024,
		MaxSize:               4 * 1024 * 1024,
		MinSize:               1024 * 1024,
		BackpressureThreshold: 0.8,
		SlideSize:             4096,
		AutoSlideEnabled:      true,
		AutoSlideInterval:     50 * time.Millisecond,
		MaxStreams:            1000,
	}
	windowManager := NewReceiveWindowManager(windowConfig)
	
	gcConfig := DefaultGCConfig()
	gcConfig.GCInterval = 50 * time.Millisecond
	gcConfig.MaxIdleTime = 100 * time.Millisecond
	
	gc := NewResourceGarbageCollector(dpm, windowManager, gcConfig)
	
	err := dpm.Start()
	if err != nil {
		t.Fatalf("Failed to start DPM: %v", err)
	}
	defer dpm.Shutdown()
	
	err = gc.Start()
	if err != nil {
		t.Fatalf("Failed to start GC: %v", err)
	}
	defer gc.Stop()
	
	// Concurrent stream creation and cleanup
	const numWorkers = 10
	const streamsPerWorker = 20
	
	var wg sync.WaitGroup
	errors := make(chan error, numWorkers*2)
	
	// Stream creators
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			
			for j := 0; j < streamsPerWorker; j++ {
				streamID := uint64(workerID*streamsPerWorker + j + 1)
				metadata := &StreamMetadata{
					StreamID:     streamID,
					Priority:     StreamPriorityNormal,
					LastActivity: time.Now(),
				}
				
				err := dpm.CreateStreamBuffer(streamID, metadata)
				if err != nil {
					errors <- fmt.Errorf("worker %d failed to create stream %d: %v", workerID, streamID, err)
					return
				}
				
				// Small delay to simulate real usage
				time.Sleep(1 * time.Millisecond)
			}
		}(i)
	}
	
	// Stream cleaners
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			
			// Wait a bit before starting cleanup
			time.Sleep(50 * time.Millisecond)
			
			for j := 0; j < streamsPerWorker/2; j++ {
				streamID := uint64(workerID*streamsPerWorker + j + 1)
				
				err := gc.CleanupStreamResources(streamID)
				if err != nil {
					errors <- fmt.Errorf("worker %d failed to cleanup stream %d: %v", workerID, streamID, err)
					return
				}
				
				time.Sleep(2 * time.Millisecond)
			}
		}(i)
	}
	
	wg.Wait()
	close(errors)
	
	// Check for errors
	for err := range errors {
		t.Error(err)
	}
	
	// Wait for GC to run
	time.Sleep(200 * time.Millisecond)
	
	// Verify final state
	stats := dpm.GetGlobalStats()
	gcStats := gc.GetStats()
	
	t.Logf("Final stats: Active streams: %d, GC runs: %d, Resources freed: %d", 
		stats.ActiveStreams, gcStats.TotalGCRuns, gcStats.TotalResourcesFreed)
	
	if gcStats.TotalGCRuns == 0 {
		t.Error("Expected at least one GC run")
	}
}

func TestResourceMonitorBasic(t *testing.T) {
	config := DefaultPresentationConfig()
	config.ReceiveWindowSize = 10 * 1024 * 1024 // 10MB
	config.DefaultStreamBufferSize = 64 * 1024  // 64KB per stream
	dpm := NewDataPresentationManager(config)
	
	windowConfig := &WindowManagerConfig{
		InitialSize:           10 * 1024 * 1024, // 10MB
		MaxSize:               20 * 1024 * 1024, // 20MB
		MinSize:               5 * 1024 * 1024,  // 5MB
		BackpressureThreshold: 0.8,
		SlideSize:             4096,
		AutoSlideEnabled:      true,
		AutoSlideInterval:     100 * time.Millisecond,
		MaxStreams:            1000,
	}
	windowManager := NewReceiveWindowManager(windowConfig)
	
	memoryPool := NewMemoryPool(1024*1024, 30*time.Second)
	defer memoryPool.Shutdown()
	
	gcConfig := DefaultGCConfig()
	gc := NewResourceGarbageCollector(dpm, windowManager, gcConfig)
	
	// Create resource monitor
	monitorConfig := DefaultResourceMonitorConfig()
	monitorConfig.MonitorInterval = 50 * time.Millisecond
	monitorConfig.MetricsInterval = 25 * time.Millisecond
	
	monitor := NewResourceMonitor(dpm, windowManager, memoryPool, gc, monitorConfig)
	
	// Start components
	err := dpm.Start()
	if err != nil {
		t.Fatalf("Failed to start DPM: %v", err)
	}
	defer dpm.Shutdown()
	
	err = monitor.Start()
	if err != nil {
		t.Fatalf("Failed to start monitor: %v", err)
	}
	defer monitor.Stop()
	
	// Set up alert callback
	alertReceived := make(chan ResourceAlert, 10)
	monitor.SetAlertCallback(func(alert ResourceAlert) {
		alertReceived <- alert
	})
	
	// Create some streams to generate metrics
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
	time.Sleep(200 * time.Millisecond)
	
	// Get metrics
	metrics := monitor.GetMetrics()
	if metrics == nil {
		t.Fatal("Expected metrics to be available")
	}
	
	// Verify basic metrics
	if metrics.ActiveStreams != 10 {
		t.Errorf("Expected 10 active streams, got %d", metrics.ActiveStreams)
	}
	
	if metrics.LastUpdate.IsZero() {
		t.Error("Expected LastUpdate to be set")
	}
	
	// Test threshold violation
	thresholds := monitor.GetThresholds()
	thresholds.MaxActiveStreams = 5 // Set low threshold to trigger alert
	monitor.SetThresholds(thresholds)
	
	// Wait for alert
	select {
	case alert := <-alertReceived:
		if alert.Type != AlertTypeStream {
			t.Errorf("Expected stream alert, got %s", alert.Type.String())
		}
		if alert.Severity != AlertSeverityCritical {
			t.Errorf("Expected critical alert, got %s", alert.Severity.String())
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("Expected to receive an alert within 500ms")
	}
	
	// Get monitoring stats
	monitorStats := monitor.GetMonitoringStats()
	if monitorStats.TotalAlerts == 0 {
		t.Error("Expected at least one alert to be generated")
	}
	
	if !monitorStats.IsRunning {
		t.Error("Expected monitor to be running")
	}
}

func TestResourceMonitorTrendAnalysis(t *testing.T) {
	config := DefaultPresentationConfig()
	dpm := NewDataPresentationManager(config)
	
	windowConfig := &WindowManagerConfig{
		InitialSize:           1024 * 1024,
		MaxSize:               2 * 1024 * 1024,
		MinSize:               512 * 1024,
		BackpressureThreshold: 0.8,
		SlideSize:             4096,
		AutoSlideEnabled:      true,
		AutoSlideInterval:     100 * time.Millisecond,
		MaxStreams:            100,
	}
	windowManager := NewReceiveWindowManager(windowConfig)
	
	memoryPool := NewMemoryPool(1024*1024, 30*time.Second)
	defer memoryPool.Shutdown()
	
	gcConfig := DefaultGCConfig()
	gc := NewResourceGarbageCollector(dpm, windowManager, gcConfig)
	
	monitorConfig := DefaultResourceMonitorConfig()
	monitorConfig.MonitorInterval = 20 * time.Millisecond
	monitorConfig.MetricsInterval = 10 * time.Millisecond
	monitorConfig.EnableTrendAnalysis = true
	
	monitor := NewResourceMonitor(dpm, windowManager, memoryPool, gc, monitorConfig)
	
	err := dpm.Start()
	if err != nil {
		t.Fatalf("Failed to start DPM: %v", err)
	}
	defer dpm.Shutdown()
	
	err = monitor.Start()
	if err != nil {
		t.Fatalf("Failed to start monitor: %v", err)
	}
	defer monitor.Stop()
	
	// Gradually create more streams to create a trend
	for round := 0; round < 6; round++ {
		for i := 0; i < 5; i++ {
			streamID := uint64(round*5 + i + 1)
			metadata := &StreamMetadata{
				StreamID:     streamID,
				Priority:     StreamPriorityNormal,
				LastActivity: time.Now(),
			}
			
			err := dpm.CreateStreamBuffer(streamID, metadata)
			if err != nil {
				t.Fatalf("Failed to create stream %d: %v", streamID, err)
			}
		}
		
		// Wait between rounds to allow metrics collection
		time.Sleep(50 * time.Millisecond)
	}
	
	// Wait for trend analysis
	time.Sleep(200 * time.Millisecond)
	
	// Get final metrics
	metrics := monitor.GetMetrics()
	
	// Verify trend data was collected
	if len(metrics.StreamTrend) < 5 {
		t.Errorf("Expected at least 5 trend measurements, got %d", len(metrics.StreamTrend))
	}
	
	// Verify trend shows increasing pattern
	if len(metrics.StreamTrend) >= 2 {
		first := metrics.StreamTrend[0]
		last := metrics.StreamTrend[len(metrics.StreamTrend)-1]
		if last <= first {
			t.Logf("Stream trend: %v", metrics.StreamTrend)
			t.Error("Expected increasing stream trend")
		}
	}
	
	// Check for trend alerts
	alerts := monitor.GetAlerts()
	trendAlerts := 0
	for _, alert := range alerts {
		if alert.Type == AlertTypeTrend {
			trendAlerts++
		}
	}
	
	t.Logf("Generated %d trend alerts out of %d total alerts", trendAlerts, len(alerts))
}

func TestResourceCleanupIntegration(t *testing.T) {
	config := DefaultPresentationConfig()
	config.EnableMemoryPooling = true
	dpm := NewDataPresentationManager(config)
	
	windowConfig := &WindowManagerConfig{
		InitialSize:           2 * 1024 * 1024,
		MaxSize:               4 * 1024 * 1024,
		MinSize:               1024 * 1024,
		BackpressureThreshold: 0.8,
		SlideSize:             4096,
		AutoSlideEnabled:      true,
		AutoSlideInterval:     50 * time.Millisecond,
		MaxStreams:            1000,
	}
	windowManager := NewReceiveWindowManager(windowConfig)
	
	memoryPool := NewMemoryPool(1024*1024, 5*time.Second)
	defer memoryPool.Shutdown()
	
	gcConfig := DefaultGCConfig()
	gcConfig.GCInterval = 100 * time.Millisecond
	gcConfig.MaxIdleTime = 200 * time.Millisecond
	gcConfig.EnableImmediateCleanup = true
	
	gc := NewResourceGarbageCollector(dpm, windowManager, gcConfig)
	
	monitorConfig := DefaultResourceMonitorConfig()
	monitorConfig.MonitorInterval = 50 * time.Millisecond
	monitorConfig.EnableLeakDetection = true
	
	monitor := NewResourceMonitor(dpm, windowManager, memoryPool, gc, monitorConfig)
	
	// Start all components
	err := dpm.Start()
	if err != nil {
		t.Fatalf("Failed to start DPM: %v", err)
	}
	defer dpm.Shutdown()
	
	err = gc.Start()
	if err != nil {
		t.Fatalf("Failed to start GC: %v", err)
	}
	defer gc.Stop()
	
	err = monitor.Start()
	if err != nil {
		t.Fatalf("Failed to start monitor: %v", err)
	}
	defer monitor.Stop()
	
	// Track resource leaks
	leakDetected := make(chan OrphanedResource, 10)
	monitor.SetResourceLeakCallback(func(resource OrphanedResource) {
		leakDetected <- resource
	})
	
	// Create and immediately close streams to test cleanup
	const numStreams = 50
	
	for i := uint64(1); i <= numStreams; i++ {
		metadata := &StreamMetadata{
			StreamID:     i,
			Priority:     StreamPriorityNormal,
			LastActivity: time.Now(),
		}
		
		err := dpm.CreateStreamBuffer(i, metadata)
		if err != nil {
			t.Fatalf("Failed to create stream %d: %v", i, err)
		}
		
		// Write some data
		data := []byte(fmt.Sprintf("test data for stream %d", i))
		err = dpm.WriteToStream(i, data, 0, nil)
		if err != nil {
			t.Errorf("Failed to write to stream %d: %v", i, err)
		}
		
		// Immediately remove some streams
		if i%3 == 0 {
			err = dpm.RemoveStreamBuffer(i)
			if err != nil {
				t.Errorf("Failed to remove stream %d: %v", i, err)
			}
		}
	}
	
	// Wait for cleanup and monitoring
	time.Sleep(500 * time.Millisecond)
	
	// Check final state
	stats := dpm.GetGlobalStats()
	gcStats := gc.GetStats()
	monitorStats := monitor.GetMonitoringStats()
	
	t.Logf("Final state:")
	t.Logf("  Active streams: %d", stats.ActiveStreams)
	t.Logf("  GC runs: %d", gcStats.TotalGCRuns)
	t.Logf("  Resources freed: %d", gcStats.TotalResourcesFreed)
	t.Logf("  Total alerts: %d", monitorStats.TotalAlerts)
	t.Logf("  Active alerts: %d", monitorStats.ActiveAlerts)
	
	// Verify cleanup occurred
	if gcStats.TotalGCRuns == 0 {
		t.Error("Expected at least one GC run")
	}
	
	// Check for resource leaks (should be minimal with immediate cleanup)
	select {
	case leak := <-leakDetected:
		t.Logf("Resource leak detected: %s stream %d idle for %v", 
			leak.Type.String(), leak.StreamID, leak.IdleDuration)
	case <-time.After(100 * time.Millisecond):
		// No leak detected within timeout - this is good
	}
	
	// Verify memory pool usage
	poolStats := memoryPool.GetStats()
	if poolStats.TotalAllocated == 0 {
		t.Error("Expected some memory pool allocations")
	}
	
	t.Logf("Memory pool stats: Allocated: %d, Released: %d, Utilization: %.2f%%", 
		poolStats.TotalAllocated, poolStats.TotalReleased, poolStats.Utilization*100)
}

func TestResourceMonitorAlertResolution(t *testing.T) {
	config := DefaultPresentationConfig()
	dpm := NewDataPresentationManager(config)
	
	windowConfig := &WindowManagerConfig{
		InitialSize:           1024 * 1024,
		MaxSize:               2 * 1024 * 1024,
		MinSize:               512 * 1024,
		BackpressureThreshold: 0.8,
		SlideSize:             4096,
		AutoSlideEnabled:      true,
		AutoSlideInterval:     100 * time.Millisecond,
		MaxStreams:            100,
	}
	windowManager := NewReceiveWindowManager(windowConfig)
	
	memoryPool := NewMemoryPool(1024*1024, 30*time.Second)
	defer memoryPool.Shutdown()
	
	gcConfig := DefaultGCConfig()
	gc := NewResourceGarbageCollector(dpm, windowManager, gcConfig)
	
	monitorConfig := DefaultResourceMonitorConfig()
	monitorConfig.MonitorInterval = 50 * time.Millisecond
	monitorConfig.AlertInterval = 25 * time.Millisecond
	
	monitor := NewResourceMonitor(dpm, windowManager, memoryPool, gc, monitorConfig)
	
	err := dpm.Start()
	if err != nil {
		t.Fatalf("Failed to start DPM: %v", err)
	}
	defer dpm.Shutdown()
	
	err = monitor.Start()
	if err != nil {
		t.Fatalf("Failed to start monitor: %v", err)
	}
	defer monitor.Stop()
	
	// Set low thresholds to trigger alerts
	thresholds := monitor.GetThresholds()
	thresholds.MaxActiveStreams = 5
	thresholds.WindowWarning = 0.1 // Very low threshold
	monitor.SetThresholds(thresholds)
	
	// Create streams to trigger alerts
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
	
	// Wait for alerts to be generated
	time.Sleep(200 * time.Millisecond)
	
	// Check that alerts were generated
	alerts := monitor.GetActiveAlerts()
	if len(alerts) == 0 {
		t.Error("Expected alerts to be generated")
	}
	
	initialAlertCount := len(alerts)
	t.Logf("Generated %d initial alerts", initialAlertCount)
	
	// Remove streams to resolve alerts
	for i := uint64(6); i <= 10; i++ {
		err := dpm.RemoveStreamBuffer(i)
		if err != nil {
			t.Errorf("Failed to remove stream %d: %v", i, err)
		}
	}
	
	// Raise thresholds to help resolve alerts
	thresholds.MaxActiveStreams = 20
	thresholds.WindowWarning = 0.9
	monitor.SetThresholds(thresholds)
	
	// Wait for alert resolution
	time.Sleep(200 * time.Millisecond)
	
	// Check that some alerts were resolved
	finalAlerts := monitor.GetActiveAlerts()
	allAlerts := monitor.GetAlerts()
	
	t.Logf("Final active alerts: %d, Total alerts: %d", len(finalAlerts), len(allAlerts))
	
	// Verify some alerts were resolved
	resolvedCount := 0
	for _, alert := range allAlerts {
		if alert.Resolved {
			resolvedCount++
		}
	}
	
	if resolvedCount == 0 {
		t.Error("Expected some alerts to be resolved")
	}
	
	t.Logf("Resolved %d alerts", resolvedCount)
}

// Benchmark tests for resource cleanup performance
func BenchmarkResourceCleanup(b *testing.B) {
	config := DefaultPresentationConfig()
	dpm := NewDataPresentationManager(config)
	
	windowConfig := &WindowManagerConfig{
		InitialSize:           10 * 1024 * 1024,
		MaxSize:               20 * 1024 * 1024,
		MinSize:               5 * 1024 * 1024,
		BackpressureThreshold: 0.8,
		SlideSize:             4096,
		AutoSlideEnabled:      false, // Disable for benchmark
		MaxStreams:            10000,
	}
	windowManager := NewReceiveWindowManager(windowConfig)
	
	gcConfig := DefaultGCConfig()
	gcConfig.EnableImmediateCleanup = true
	gc := NewResourceGarbageCollector(dpm, windowManager, gcConfig)
	
	err := dpm.Start()
	if err != nil {
		b.Fatalf("Failed to start DPM: %v", err)
	}
	defer dpm.Shutdown()
	
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		streamID := uint64(i + 1)
		
		// Create stream
		metadata := &StreamMetadata{
			StreamID:     streamID,
			Priority:     StreamPriorityNormal,
			LastActivity: time.Now(),
		}
		
		err := dpm.CreateStreamBuffer(streamID, metadata)
		if err != nil {
			b.Fatalf("Failed to create stream %d: %v", streamID, err)
		}
		
		// Immediately cleanup
		err = gc.CleanupStreamResources(streamID)
		if err != nil {
			b.Fatalf("Failed to cleanup stream %d: %v", streamID, err)
		}
	}
}

func BenchmarkResourceMonitoring(b *testing.B) {
	config := DefaultPresentationConfig()
	dpm := NewDataPresentationManager(config)
	
	windowConfig := &WindowManagerConfig{
		InitialSize:           10 * 1024 * 1024,
		MaxSize:               20 * 1024 * 1024,
		MinSize:               5 * 1024 * 1024,
		BackpressureThreshold: 0.8,
		SlideSize:             4096,
		AutoSlideEnabled:      false,
		MaxStreams:            10000,
	}
	windowManager := NewReceiveWindowManager(windowConfig)
	
	memoryPool := NewMemoryPool(10*1024*1024, 30*time.Second)
	defer memoryPool.Shutdown()
	
	gcConfig := DefaultGCConfig()
	gc := NewResourceGarbageCollector(dpm, windowManager, gcConfig)
	
	monitorConfig := DefaultResourceMonitorConfig()
	monitor := NewResourceMonitor(dpm, windowManager, memoryPool, gc, monitorConfig)
	
	err := dpm.Start()
	if err != nil {
		b.Fatalf("Failed to start DPM: %v", err)
	}
	defer dpm.Shutdown()
	
	// Create some streams for monitoring
	for i := uint64(1); i <= 100; i++ {
		metadata := &StreamMetadata{
			StreamID:     i,
			Priority:     StreamPriorityNormal,
			LastActivity: time.Now(),
		}
		
		err := dpm.CreateStreamBuffer(i, metadata)
		if err != nil {
			b.Fatalf("Failed to create stream %d: %v", i, err)
		}
	}
	
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		// Simulate monitoring operations
		_ = monitor.GetMetrics()
		_ = monitor.GetAlerts()
		_ = monitor.GetMonitoringStats()
	}
}