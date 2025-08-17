package presentation

import (
	"sync"
	"testing"
	"time"
)

func TestReceiveWindowManager_BasicOperations(t *testing.T) {
	config := &WindowManagerConfig{
		InitialSize:           1024,
		MaxSize:               2048,
		MinSize:               512,
		BackpressureThreshold: 0.8,
		SlideSize:             64,
		AutoSlideEnabled:      false, // Disable for testing
		MaxStreams:            100,
	}
	
	rwm := NewReceiveWindowManager(config)
	defer rwm.Shutdown()
	
	// Test initial state
	if size := rwm.GetWindowSize(); size != 1024 {
		t.Errorf("Expected window size 1024, got %d", size)
	}
	
	if available := rwm.GetAvailableWindow(); available != 1024 {
		t.Errorf("Expected available window 1024, got %d", available)
	}
	
	if utilization := rwm.GetWindowUtilization(); utilization != 0 {
		t.Errorf("Expected utilization 0, got %f", utilization)
	}
}

func TestReceiveWindowManager_WindowAllocation(t *testing.T) {
	config := &WindowManagerConfig{
		InitialSize:           1024,
		BackpressureThreshold: 0.8,
		AutoSlideEnabled:      false,
		MaxStreams:            100,
	}
	
	rwm := NewReceiveWindowManager(config)
	defer rwm.Shutdown()
	
	// Allocate window space for stream 1
	err := rwm.AllocateWindow(1, 256)
	if err != nil {
		t.Fatalf("Failed to allocate window: %v", err)
	}
	
	// Check available space decreased
	if available := rwm.GetAvailableWindow(); available != 768 {
		t.Errorf("Expected available window 768, got %d", available)
	}
	
	// Check utilization
	expectedUtilization := 256.0 / 1024.0
	if utilization := rwm.GetWindowUtilization(); utilization != expectedUtilization {
		t.Errorf("Expected utilization %f, got %f", expectedUtilization, utilization)
	}
	
	// Allocate more space for the same stream
	err = rwm.AllocateWindow(1, 128)
	if err != nil {
		t.Fatalf("Failed to allocate additional window: %v", err)
	}
	
	// Total allocation for stream 1 should be 384
	status := rwm.GetWindowStatus()
	if allocation, exists := status.StreamAllocations[1]; !exists || allocation != 384 {
		t.Errorf("Expected stream 1 allocation 384, got %d", allocation)
	}
}

func TestReceiveWindowManager_WindowRelease(t *testing.T) {
	rwm := NewReceiveWindowManager(nil)
	defer rwm.Shutdown()
	
	// Allocate and then release
	err := rwm.AllocateWindow(1, 256)
	if err != nil {
		t.Fatalf("Failed to allocate window: %v", err)
	}
	
	err = rwm.ReleaseWindow(1, 128)
	if err != nil {
		t.Fatalf("Failed to release window: %v", err)
	}
	
	// Check remaining allocation
	status := rwm.GetWindowStatus()
	if allocation, exists := status.StreamAllocations[1]; !exists || allocation != 128 {
		t.Errorf("Expected stream 1 allocation 128, got %d", allocation)
	}
	
	// Release all remaining
	err = rwm.ReleaseWindow(1, 128)
	if err != nil {
		t.Fatalf("Failed to release remaining window: %v", err)
	}
	
	// Stream should be removed from allocations
	status = rwm.GetWindowStatus()
	if _, exists := status.StreamAllocations[1]; exists {
		t.Error("Expected stream 1 to be removed from allocations")
	}
}

func TestReceiveWindowManager_BackpressureActivation(t *testing.T) {
	config := &WindowManagerConfig{
		InitialSize:           1000,
		BackpressureThreshold: 0.8, // 800 bytes
		AutoSlideEnabled:      false,
		MaxStreams:            100,
	}
	
	rwm := NewReceiveWindowManager(config)
	defer rwm.Shutdown()
	
	// Set up callback to detect backpressure
	backpressureActivated := false
	rwm.SetWindowFullCallback(func() {
		backpressureActivated = true
	})
	
	// Allocate just under threshold
	err := rwm.AllocateWindow(1, 799)
	if err != nil {
		t.Fatalf("Failed to allocate window: %v", err)
	}
	
	// Should not be full yet
	if rwm.IsWindowFull() {
		t.Error("Window should not be full yet")
	}
	
	// Allocate more to trigger backpressure
	err = rwm.AllocateWindow(2, 100)
	if err != nil {
		t.Fatalf("Failed to allocate window: %v", err)
	}
	
	// Should be full now
	if !rwm.IsWindowFull() {
		t.Error("Window should be full now")
	}
	
	// Give callback time to execute
	time.Sleep(10 * time.Millisecond)
	
	if !backpressureActivated {
		t.Error("Expected backpressure callback to be activated")
	}
}

func TestReceiveWindowManager_WindowSliding(t *testing.T) {
	rwm := NewReceiveWindowManager(nil)
	defer rwm.Shutdown()
	
	// Initial slide position should be 0
	if pos := rwm.GetSlidePosition(); pos != 0 {
		t.Errorf("Expected initial slide position 0, got %d", pos)
	}
	
	// Slide the window
	err := rwm.SlideWindow(256)
	if err != nil {
		t.Fatalf("Failed to slide window: %v", err)
	}
	
	// Check new position
	if pos := rwm.GetSlidePosition(); pos != 256 {
		t.Errorf("Expected slide position 256, got %d", pos)
	}
	
	// Check slide info
	slideInfo := rwm.GetSlideInfo()
	if slideInfo.TotalSlides != 1 {
		t.Errorf("Expected 1 total slide, got %d", slideInfo.TotalSlides)
	}
	
	if slideInfo.TotalBytesSlid != 256 {
		t.Errorf("Expected 256 total bytes slid, got %d", slideInfo.TotalBytesSlid)
	}
}

func TestReceiveWindowManager_SlideToPosition(t *testing.T) {
	rwm := NewReceiveWindowManager(nil)
	defer rwm.Shutdown()
	
	// Slide to specific position
	err := rwm.SlideWindowToPosition(500)
	if err != nil {
		t.Fatalf("Failed to slide to position: %v", err)
	}
	
	if pos := rwm.GetSlidePosition(); pos != 500 {
		t.Errorf("Expected slide position 500, got %d", pos)
	}
	
	// Try to slide backward (should fail)
	err = rwm.SlideWindowToPosition(300)
	if err == nil {
		t.Error("Expected error when sliding backward")
	}
}

func TestReceiveWindowManager_WindowSizeChange(t *testing.T) {
	rwm := NewReceiveWindowManager(nil)
	defer rwm.Shutdown()
	
	// Change window size to a valid size (above minimum)
	newSize := uint64(2 * 1024 * 1024) // 2MB, above the 1MB minimum
	err := rwm.SetWindowSize(newSize)
	if err != nil {
		t.Fatalf("Failed to set window size: %v", err)
	}
	
	if size := rwm.GetWindowSize(); size != newSize {
		t.Errorf("Expected window size %d, got %d", newSize, size)
	}
	
	// Try to set size below minimum (should fail)
	err = rwm.SetWindowSize(100)
	if err == nil {
		t.Error("Expected error when setting size below minimum")
	}
}

func TestReceiveWindowManager_MaxStreamsLimit(t *testing.T) {
	config := &WindowManagerConfig{
		InitialSize:    1024,
		MaxStreams:     2, // Very low limit for testing
		AutoSlideEnabled: false,
	}
	
	rwm := NewReceiveWindowManager(config)
	defer rwm.Shutdown()
	
	// Allocate for first two streams (should succeed)
	err := rwm.AllocateWindow(1, 100)
	if err != nil {
		t.Fatalf("Failed to allocate for stream 1: %v", err)
	}
	
	err = rwm.AllocateWindow(2, 100)
	if err != nil {
		t.Fatalf("Failed to allocate for stream 2: %v", err)
	}
	
	// Third stream should fail
	err = rwm.AllocateWindow(3, 100)
	if err == nil {
		t.Error("Expected error when exceeding max streams")
	}
}

func TestReceiveWindowManager_ConcurrentAccess(t *testing.T) {
	rwm := NewReceiveWindowManager(nil)
	defer rwm.Shutdown()
	
	var wg sync.WaitGroup
	errors := make(chan error, 100)
	
	// Concurrent allocations
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(streamID uint64) {
			defer wg.Done()
			err := rwm.AllocateWindow(streamID, 50)
			if err != nil {
				errors <- err
			}
		}(uint64(i))
	}
	
	// Concurrent releases
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(streamID uint64) {
			defer wg.Done()
			time.Sleep(10 * time.Millisecond) // Let allocations happen first
			err := rwm.ReleaseWindow(streamID, 25)
			if err != nil {
				errors <- err
			}
		}(uint64(i))
	}
	
	// Concurrent slides
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := rwm.SlideWindow(10)
			if err != nil {
				errors <- err
			}
		}()
	}
	
	wg.Wait()
	close(errors)
	
	// Check for errors
	for err := range errors {
		if err != nil {
			t.Errorf("Concurrent operation failed: %v", err)
		}
	}
}

func TestReceiveWindowManager_AdaptiveSliding(t *testing.T) {
	rwm := NewReceiveWindowManager(nil)
	defer rwm.Shutdown()
	
	// Test optimal slide size calculation
	optimalSize := rwm.CalculateOptimalSlideSize()
	if optimalSize == 0 {
		t.Error("Expected non-zero optimal slide size")
	}
	
	// Perform adaptive slide
	err := rwm.PerformAdaptiveSlide()
	if err != nil {
		t.Fatalf("Failed to perform adaptive slide: %v", err)
	}
	
	// Check that slide happened
	if pos := rwm.GetSlidePosition(); pos == 0 {
		t.Error("Expected slide position to change after adaptive slide")
	}
}

func TestReceiveWindowManager_Statistics(t *testing.T) {
	rwm := NewReceiveWindowManager(nil)
	defer rwm.Shutdown()
	
	// Perform some operations to generate statistics
	rwm.AllocateWindow(1, 100)
	rwm.AllocateWindow(2, 200)
	rwm.ReleaseWindow(1, 50)
	rwm.SlideWindow(100)
	
	stats := rwm.GetStats()
	
	if stats.TotalAllocations < 2 {
		t.Errorf("Expected at least 2 allocations, got %d", stats.TotalAllocations)
	}
	
	if stats.TotalReleases < 1 {
		t.Errorf("Expected at least 1 release, got %d", stats.TotalReleases)
	}
	
	if stats.TotalSlides < 1 {
		t.Errorf("Expected at least 1 slide, got %d", stats.TotalSlides)
	}
	
	if stats.BytesSlid < 100 {
		t.Errorf("Expected at least 100 bytes slid, got %d", stats.BytesSlid)
	}
}

func TestReceiveWindowManager_SlidingMetrics(t *testing.T) {
	rwm := NewReceiveWindowManager(nil)
	defer rwm.Shutdown()
	
	// Perform several slides
	for i := 0; i < 5; i++ {
		rwm.SlideWindow(100)
		time.Sleep(1 * time.Millisecond) // Small delay between slides
	}
	
	metrics := rwm.GetSlidingMetrics()
	
	if metrics.TotalSlides != 5 {
		t.Errorf("Expected 5 total slides, got %d", metrics.TotalSlides)
	}
	
	if metrics.TotalBytesSlid != 500 {
		t.Errorf("Expected 500 total bytes slid, got %d", metrics.TotalBytesSlid)
	}
	
	if metrics.AverageSlideSize != 100 {
		t.Errorf("Expected average slide size 100, got %d", metrics.AverageSlideSize)
	}
}

func TestReceiveWindowManager_CallbackExecution(t *testing.T) {
	rwm := NewReceiveWindowManager(nil)
	defer rwm.Shutdown()
	
	// Test window full callback
	fullCallbackExecuted := false
	rwm.SetWindowFullCallback(func() {
		fullCallbackExecuted = true
	})
	
	// Test window available callback
	availableCallbackExecuted := false
	rwm.SetWindowAvailableCallback(func() {
		availableCallbackExecuted = true
	})
	
	// Fill window to trigger full callback
	rwm.AllocateWindow(1, rwm.GetWindowSize())
	
	// Give callback time to execute
	time.Sleep(10 * time.Millisecond)
	
	if !fullCallbackExecuted {
		t.Error("Expected window full callback to be executed")
	}
	
	// Release window to trigger available callback
	rwm.ReleaseWindow(1, rwm.GetWindowSize()/2)
	
	// Give callback time to execute
	time.Sleep(10 * time.Millisecond)
	
	if !availableCallbackExecuted {
		t.Error("Expected window available callback to be executed")
	}
}

func BenchmarkReceiveWindowManager_AllocateWindow(b *testing.B) {
	rwm := NewReceiveWindowManager(nil)
	defer rwm.Shutdown()
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		streamID := uint64(i % 1000) // Cycle through stream IDs
		rwm.AllocateWindow(streamID, 100)
	}
}

func BenchmarkReceiveWindowManager_ReleaseWindow(b *testing.B) {
	rwm := NewReceiveWindowManager(nil)
	defer rwm.Shutdown()
	
	// Pre-allocate windows
	for i := 0; i < 1000; i++ {
		rwm.AllocateWindow(uint64(i), 100)
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		streamID := uint64(i % 1000)
		rwm.ReleaseWindow(streamID, 50)
	}
}

func BenchmarkReceiveWindowManager_SlideWindow(b *testing.B) {
	rwm := NewReceiveWindowManager(nil)
	defer rwm.Shutdown()
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rwm.SlideWindow(64)
	}
}