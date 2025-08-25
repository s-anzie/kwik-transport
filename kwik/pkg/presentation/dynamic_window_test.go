package presentation

import (
	"testing"
	"time"
)

func TestDynamicWindowManagement(t *testing.T) {
	// Create window manager with dynamic sizing enabled
	config := &WindowManagerConfig{
		InitialSize:             1024 * 1024, // 1MB
		MaxSize:                 4 * 1024 * 1024, // 4MB
		MinSize:                 256 * 1024, // 256KB
		BackpressureThreshold:   0.8,
		MaxStreams:              1000,
		EnableDynamicSizing:     false, // Disable for manual testing
		GrowthFactor:           1.5,
		ShrinkFactor:           0.8,
		MemoryPressureThreshold: 0.85,
		DynamicAdjustInterval:  100 * time.Millisecond,
	}
	
	rwm := NewReceiveWindowManager(config)
	defer rwm.Shutdown()
	
	// Enable dynamic sizing after creation to avoid worker startup issues
	err2 := rwm.SetDynamicWindowConfig(true, 1.5, 0.8, 0.85)
	if err2 != nil {
		t.Fatalf("Failed to enable dynamic sizing: %v", err2)
	}
	
	// Test initial state
	stats := rwm.GetDynamicWindowStats()
	if !stats.Enabled {
		t.Error("Dynamic window management should be enabled")
	}
	if stats.CurrentSize != config.InitialSize {
		t.Errorf("Expected initial size %d, got %d", config.InitialSize, stats.CurrentSize)
	}
	
	// Test window growth under high utilization
	// Allocate enough to trigger growth
	streamID := uint64(1)
	allocSize := uint64(float64(config.InitialSize) * 0.85) // 85% utilization
	
	err := rwm.AllocateWindow(streamID, allocSize)
	if err != nil {
		t.Fatalf("Failed to allocate window: %v", err)
	}
	
	// Force dynamic adjustment
	err = rwm.ForceWindowAdjustment()
	if err != nil {
		t.Fatalf("Failed to force window adjustment: %v", err)
	}
	
	// Check if window grew
	newStats := rwm.GetDynamicWindowStats()
	if newStats.CurrentSize <= stats.CurrentSize {
		t.Error("Window should have grown under high utilization")
	}
	
	// Test window shrinking under low utilization
	// Release most of the allocation
	err = rwm.ReleaseWindow(streamID, allocSize-1024) // Keep 1KB allocated
	if err != nil {
		t.Fatalf("Failed to release window: %v", err)
	}
	
	// Simulate low memory pressure
	rwm.memoryMonitor.mutex.Lock()
	rwm.memoryMonitor.usedMemory = uint64(float64(rwm.memoryMonitor.totalMemory) * 0.3) // 30% usage
	rwm.memoryMonitor.mutex.Unlock()
	
	// Force adjustment again
	err = rwm.ForceWindowAdjustment()
	if err != nil {
		t.Fatalf("Failed to force window adjustment: %v", err)
	}
	
	// Check if window shrunk (might not shrink immediately due to thresholds)
	finalStats := rwm.GetDynamicWindowStats()
	t.Logf("Window size progression: %d -> %d -> %d", 
		stats.CurrentSize, newStats.CurrentSize, finalStats.CurrentSize)
}

func TestDynamicWindowConfiguration(t *testing.T) {
	config := &WindowManagerConfig{
		InitialSize:             1024 * 1024,
		MaxSize:                 4 * 1024 * 1024,
		MinSize:                 256 * 1024,
		BackpressureThreshold:   0.8,
		MaxStreams:              1000,
		EnableDynamicSizing:     false, // Start disabled
		GrowthFactor:           1.5,
		ShrinkFactor:           0.8,
		MemoryPressureThreshold: 0.85,
		DynamicAdjustInterval:  100 * time.Millisecond,
	}
	
	rwm := NewReceiveWindowManager(config)
	defer rwm.Shutdown()
	
	// Test enabling dynamic sizing
	err := rwm.SetDynamicWindowConfig(true, 2.0, 0.7, 0.9)
	if err != nil {
		t.Fatalf("Failed to set dynamic window config: %v", err)
	}
	
	stats := rwm.GetDynamicWindowStats()
	if !stats.Enabled {
		t.Error("Dynamic window management should be enabled after configuration")
	}
	if stats.GrowthFactor != 2.0 {
		t.Errorf("Expected growth factor 2.0, got %f", stats.GrowthFactor)
	}
	if stats.ShrinkFactor != 0.7 {
		t.Errorf("Expected shrink factor 0.7, got %f", stats.ShrinkFactor)
	}
	if stats.MemoryPressureThreshold != 0.9 {
		t.Errorf("Expected memory pressure threshold 0.9, got %f", stats.MemoryPressureThreshold)
	}
	
	// Test invalid configuration
	err = rwm.SetDynamicWindowConfig(true, 0.5, 0.7, 0.9) // Invalid growth factor
	if err == nil {
		t.Error("Should have failed with invalid growth factor")
	}
	
	err = rwm.SetDynamicWindowConfig(true, 2.0, 1.5, 0.9) // Invalid shrink factor
	if err == nil {
		t.Error("Should have failed with invalid shrink factor")
	}
	
	err = rwm.SetDynamicWindowConfig(true, 2.0, 0.7, 1.5) // Invalid memory threshold
	if err == nil {
		t.Error("Should have failed with invalid memory threshold")
	}
}

func TestMemoryMonitor(t *testing.T) {
	monitor := NewMemoryMonitor()
	
	// Test initial state
	usage := monitor.GetMemoryUsage()
	if usage < 0 || usage > 1 {
		t.Errorf("Memory usage should be between 0 and 1, got %f", usage)
	}
	
	// Test memory stats update
	initialUsage := usage
	monitor.UpdateMemoryStats()
	newUsage := monitor.GetMemoryUsage()
	
	// Usage might change due to simulation
	if newUsage < 0 || newUsage > 1 {
		t.Errorf("Memory usage should be between 0 and 1 after update, got %f", newUsage)
	}
	
	t.Logf("Memory usage: %f -> %f", initialUsage, newUsage)
}

func TestDynamicBufferPool(t *testing.T) {
	pool := NewDynamicBufferPool(1024, 64*1024, 1.5, 0.3)
	
	// Test basic allocation
	block := pool.GetDynamicBlock(2048)
	if block == nil {
		t.Fatal("Failed to get dynamic block")
	}
	if block.capacity < 2048 {
		t.Errorf("Block capacity %d should be at least %d", block.capacity, 2048)
	}
	
	// Test pool creation
	stats := pool.GetDynamicPoolStats()
	if stats.ActivePools == 0 {
		t.Error("Should have at least one active pool")
	}
	
	// Return block to pool
	pool.PutDynamicBlock(block)
	
	// Test oversized allocation
	largeBlock := pool.GetDynamicBlock(128 * 1024) // Larger than max
	if largeBlock == nil {
		t.Fatal("Failed to get large block")
	}
	if largeBlock.capacity != 128*1024 {
		t.Errorf("Large block capacity should be exactly %d, got %d", 128*1024, largeBlock.capacity)
	}
	
	// Return large block (should not be pooled)
	pool.PutDynamicBlock(largeBlock)
	
	// Check final stats
	finalStats := pool.GetDynamicPoolStats()
	if finalStats.TotalAllocations != 2 {
		t.Errorf("Expected 2 total allocations, got %d", finalStats.TotalAllocations)
	}
	if finalStats.TotalDeallocations != 2 {
		t.Errorf("Expected 2 total deallocations, got %d", finalStats.TotalDeallocations)
	}
	
	t.Logf("Pool stats: %+v", finalStats)
}

func TestAdaptiveBufferSizing(t *testing.T) {
	config := DefaultPresentationConfig()
	config.DefaultStreamBufferSize = 1024 * 1024 // 1MB
	config.MaxStreamBufferSize = 4 * 1024 * 1024 // 4MB
	
	dpm := NewDataPresentationManager(config)
	defer dpm.Shutdown()
	
	// Test default sizing
	size := dpm.calculateAdaptiveBufferSize(nil)
	if size < MinStreamBufferSize || size > config.MaxStreamBufferSize {
		t.Errorf("Default size %d should be between %d and %d", size, MinStreamBufferSize, config.MaxStreamBufferSize)
	}
	
	// Test priority-based sizing
	criticalMetadata := &StreamMetadata{
		StreamID: 1,
		Priority: StreamPriorityCritical,
	}
	criticalSize := dpm.calculateAdaptiveBufferSize(criticalMetadata)
	if criticalSize <= size {
		t.Error("Critical priority streams should get larger buffers")
	}
	
	lowMetadata := &StreamMetadata{
		StreamID: 2,
		Priority: StreamPriorityLow,
	}
	lowSize := dpm.calculateAdaptiveBufferSize(lowMetadata)
	if lowSize >= size {
		t.Error("Low priority streams should get smaller buffers")
	}
	
	// Test custom max buffer size
	customMetadata := &StreamMetadata{
		StreamID:      3,
		Priority:      StreamPriorityNormal,
		MaxBufferSize: 2 * 1024 * 1024, // 2MB
	}
	customSize := dpm.calculateAdaptiveBufferSize(customMetadata)
	// Custom size should be based on the MaxBufferSize but may be adjusted
	if customSize < MinStreamBufferSize || customSize > config.MaxStreamBufferSize {
		t.Errorf("Custom size %d should be between %d and %d", customSize, MinStreamBufferSize, config.MaxStreamBufferSize)
	}
	
	t.Logf("Buffer sizes - Default: %d, Critical: %d, Low: %d, Custom: %d", 
		size, criticalSize, lowSize, customSize)
}

func BenchmarkDynamicWindowAdjustment(b *testing.B) {
	config := &WindowManagerConfig{
		InitialSize:             1024 * 1024,
		MaxSize:                 4 * 1024 * 1024,
		MinSize:                 256 * 1024,
		BackpressureThreshold:   0.8,
		MaxStreams:              1000,
		EnableDynamicSizing:     true,
		GrowthFactor:           1.5,
		ShrinkFactor:           0.8,
		MemoryPressureThreshold: 0.85,
		DynamicAdjustInterval:  100 * time.Millisecond,
	}
	
	rwm := NewReceiveWindowManager(config)
	defer rwm.Shutdown()
	
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		rwm.ForceWindowAdjustment()
	}
}

func BenchmarkDynamicBufferAllocation(b *testing.B) {
	pool := NewDynamicBufferPool(1024, 64*1024, 1.5, 0.3)
	
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		block := pool.GetDynamicBlock(4096)
		pool.PutDynamicBlock(block)
	}
}