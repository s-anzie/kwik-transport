package presentation

import (
	"testing"
	"time"
)

func TestResourceGarbageCollector(t *testing.T) {
	config := DefaultPresentationConfig()
	config.ReceiveWindowSize = 10 * 1024 * 1024 // 10MB window
	config.DefaultStreamBufferSize = 1024 * 1024 // 1MB per stream
	
	dpm := NewDataPresentationManager(config)
	defer dpm.Shutdown()
	
	err := dpm.Start()
	if err != nil {
		t.Fatalf("Failed to start DPM: %v", err)
	}
	
	// Create some streams
	for i := 1; i <= 5; i++ {
		metadata := &StreamMetadata{
			StreamID:     uint64(i),
			Priority:     StreamPriorityNormal,
			CreatedAt:    time.Now(),
			LastActivity: time.Now(),
		}
		
		err := dpm.CreateStreamBuffer(uint64(i), metadata)
		if err != nil {
			t.Fatalf("Failed to create stream %d: %v", i, err)
		}
	}
	
	// Verify streams were created
	stats := dpm.GetGlobalStats()
	if stats.ActiveStreams != 5 {
		t.Errorf("Expected 5 active streams, got %d", stats.ActiveStreams)
	}
	
	// Test force GC
	gcStats := dpm.garbageCollector.ForceGC()
	if gcStats.TotalGCRuns == 0 {
		t.Error("Expected at least one GC run")
	}
	
	t.Logf("GC Stats: %+v", gcStats)
}

func TestOrphanedResourceDetection(t *testing.T) {
	config := DefaultPresentationConfig()
	dpm := NewDataPresentationManager(config)
	defer dpm.Shutdown()
	
	dpm.Start()
	
	// Create a stream
	metadata := &StreamMetadata{
		StreamID:     1,
		Priority:     StreamPriorityNormal,
		CreatedAt:    time.Now(),
		LastActivity: time.Now().Add(-10 * time.Minute), // Old activity
	}
	
	err := dpm.CreateStreamBuffer(1, metadata)
	if err != nil {
		t.Fatalf("Failed to create stream: %v", err)
	}
	
	// Check for orphaned resources
	orphaned := dpm.garbageCollector.GetOrphanedResources()
	
	// Should find the old stream as orphaned
	if len(orphaned) == 0 {
		t.Error("Expected to find orphaned resources")
	}
	
	found := false
	for _, resource := range orphaned {
		if resource.StreamID == 1 && resource.Type == ResourceTypeStreamBuffer {
			found = true
			if resource.IdleDuration < 5*time.Minute {
				t.Errorf("Expected idle duration > 5min, got %v", resource.IdleDuration)
			}
			break
		}
	}
	
	if !found {
		t.Error("Expected to find stream 1 as orphaned")
	}
}

func TestImmediateResourceCleanup(t *testing.T) {
	config := DefaultPresentationConfig()
	dpm := NewDataPresentationManager(config)
	defer dpm.Shutdown()
	
	dpm.Start()
	
	// Create a stream
	metadata := &StreamMetadata{
		StreamID:  1,
		Priority:  StreamPriorityNormal,
		CreatedAt: time.Now(),
	}
	
	err := dpm.CreateStreamBuffer(1, metadata)
	if err != nil {
		t.Fatalf("Failed to create stream: %v", err)
	}
	
	// Verify stream exists
	_, err = dpm.GetStreamBuffer(1)
	if err != nil {
		t.Fatalf("Stream should exist: %v", err)
	}
	
	// Remove stream (should trigger immediate cleanup)
	err = dpm.RemoveStreamBuffer(1)
	if err != nil {
		t.Fatalf("Failed to remove stream: %v", err)
	}
	
	// Verify stream is gone
	_, err = dpm.GetStreamBuffer(1)
	if err == nil {
		t.Error("Stream should have been removed")
	}
	
	// Check GC stats
	gcStats := dpm.garbageCollector.GetStats()
	if gcStats.TotalResourcesFreed == 0 {
		t.Error("Expected some resources to be freed")
	}
}

func TestGarbageCollectorConfiguration(t *testing.T) {
	config := DefaultPresentationConfig()
	dpm := NewDataPresentationManager(config)
	defer dpm.Shutdown()
	
	// Test default GC config
	gcStats := dpm.garbageCollector.GetStats()
	if gcStats == nil {
		t.Fatal("GC stats should not be nil")
	}
	
	// Test custom GC config
	customConfig := &GCConfig{
		GCInterval:              10 * time.Second,
		OrphanCheckInterval:     5 * time.Second,
		MaxIdleTime:             2 * time.Minute,
		MemoryPressureThreshold: 0.9,
		MaxOrphanedResources:    50,
		EnableAggressiveGC:      false,
		EnableOrphanCleanup:     false,
		EnableImmediateCleanup:  false,
	}
	
	err := dpm.garbageCollector.SetConfig(customConfig)
	if err != nil {
		t.Fatalf("Failed to set GC config: %v", err)
	}
	
	// Test invalid config
	err = dpm.garbageCollector.SetConfig(nil)
	if err == nil {
		t.Error("Expected error with nil config")
	}
}

func TestOrphanedWindowAllocationCleanup(t *testing.T) {
	config := &WindowManagerConfig{
		InitialSize:           10 * 1024 * 1024, // 10MB
		MaxSize:               20 * 1024 * 1024, // 20MB
		MinSize:               5 * 1024 * 1024,  // 5MB
		BackpressureThreshold: 0.8,
		MaxStreams:            1000,
		EnableDynamicSizing:   false,
	}
	
	rwm := NewReceiveWindowManager(config)
	defer rwm.Shutdown()
	
	// Manually create an orphaned allocation (simulate a bug or crash)
	err := rwm.AllocateWindowAtomic(999, 1024*1024) // 1MB
	if err != nil {
		t.Fatalf("Failed to allocate window: %v", err)
	}
	
	// Create DPM without the corresponding stream buffer
	dpmConfig := DefaultPresentationConfig()
	dpm := &DataPresentationManagerImpl{
		streamBuffers:       make(map[uint64]*StreamBufferImpl),
		windowManager:       rwm,
		config:              dpmConfig,
	}
	
	// Create GC
	gc := NewResourceGarbageCollector(dpm, rwm, DefaultGCConfig())
	
	// Find orphaned allocations
	orphanedAllocations := gc.findOrphanedWindowAllocations()
	if len(orphanedAllocations) == 0 {
		t.Error("Expected to find orphaned window allocation")
	}
	
	found := false
	for _, streamID := range orphanedAllocations {
		if streamID == 999 {
			found = true
			break
		}
	}
	
	if !found {
		t.Error("Expected to find stream 999 as orphaned allocation")
	}
	
	// Test cleanup
	gcStats := gc.ForceGC()
	if gcStats.WindowSpaceFreed == 0 {
		t.Error("Expected some window space to be freed")
	}
	
	// Verify allocation was cleaned up
	windowStatus := rwm.GetWindowStatus()
	if _, exists := windowStatus.StreamAllocations[999]; exists {
		t.Error("Orphaned allocation should have been cleaned up")
	}
}

func BenchmarkGarbageCollection(b *testing.B) {
	config := DefaultPresentationConfig()
	config.ReceiveWindowSize = 50 * 1024 * 1024 // 50MB window
	
	dpm := NewDataPresentationManager(config)
	defer dpm.Shutdown()
	
	dpm.Start()
	
	// Create some streams to clean up
	for i := 1; i <= 10; i++ {
		metadata := &StreamMetadata{
			StreamID:  uint64(i),
			Priority:  StreamPriorityNormal,
			CreatedAt: time.Now(),
		}
		dpm.CreateStreamBuffer(uint64(i), metadata)
	}
	
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		dpm.garbageCollector.ForceGC()
	}
}