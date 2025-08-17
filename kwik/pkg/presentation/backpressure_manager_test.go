package presentation

import (
	"sync"
	"testing"
	"time"
)

func TestBackpressureManager_BasicOperations(t *testing.T) {
	config := &BackpressureConfig{
		MaxStreams:              100,
		GlobalThreshold:         0.8,
		StreamThreshold:         0.7,
		BackoffInitial:          10 * time.Millisecond,
		BackoffMax:              1 * time.Second,
		BackoffMultiplier:       2.0,
		AutoReleaseEnabled:      false, // Disable for testing
		AutoReleaseInterval:     100 * time.Millisecond,
		StatsUpdateInterval:     1 * time.Second,
		EnableDetailedLogging:   false,
	}
	
	bpm := NewBackpressureManager(config)
	defer bpm.Shutdown()
	
	// Initially no backpressure should be active
	if bpm.IsBackpressureActive(1) {
		t.Error("Expected no backpressure initially")
	}
	
	if bpm.IsGlobalBackpressureActive() {
		t.Error("Expected no global backpressure initially")
	}
}

func TestBackpressureManager_StreamBackpressure(t *testing.T) {
	t.Log("Creating backpressure manager...")
	config := &BackpressureConfig{
		MaxStreams:              100,
		GlobalThreshold:         0.8,
		StreamThreshold:         0.7,
		BackoffInitial:          10 * time.Millisecond,
		BackoffMax:              1 * time.Second,
		BackoffMultiplier:       2.0,
		AutoReleaseEnabled:      false, // Disable background worker to avoid deadlocks in tests
		AutoReleaseInterval:     100 * time.Millisecond,
		StatsUpdateInterval:     1 * time.Second,
		EnableDetailedLogging:   false,
	}
	bpm := NewBackpressureManager(config)
	defer func() {
		t.Log("Shutting down backpressure manager...")
		bpm.Shutdown()
		t.Log("Shutdown complete")
	}()
	
	t.Log("Activating backpressure for stream 1...")
	// Activate backpressure for stream 1
	err := bpm.ActivateBackpressure(1, BackpressureReasonBufferFull)
	if err != nil {
		t.Fatalf("Failed to activate backpressure: %v", err)
	}
	t.Log("Backpressure activated successfully")
	
	// Check that backpressure is active
	if !bpm.IsBackpressureActive(1) {
		t.Error("Expected backpressure to be active for stream 1")
	}
	
	// Check reason
	if reason := bpm.GetBackpressureReason(1); reason != BackpressureReasonBufferFull {
		t.Errorf("Expected reason BufferFull, got %v", reason)
	}
	
	// Deactivate backpressure
	err = bpm.DeactivateBackpressure(1)
	if err != nil {
		t.Fatalf("Failed to deactivate backpressure: %v", err)
	}
	
	// Check that backpressure is no longer active
	if bpm.IsBackpressureActive(1) {
		t.Error("Expected backpressure to be inactive after deactivation")
	}
	
	// Reason should be None after deactivation
	if reason := bpm.GetBackpressureReason(1); reason != BackpressureReasonNone {
		t.Errorf("Expected reason None after deactivation, got %v", reason)
	}
}

func TestBackpressureManager_GlobalBackpressure(t *testing.T) {
	bpm := NewBackpressureManager(nil)
	defer bpm.Shutdown()
	
	// Activate global backpressure
	err := bpm.ActivateGlobalBackpressure(BackpressureReasonMemoryPressure)
	if err != nil {
		t.Fatalf("Failed to activate global backpressure: %v", err)
	}
	
	// Check that global backpressure is active
	if !bpm.IsGlobalBackpressureActive() {
		t.Error("Expected global backpressure to be active")
	}
	
	// Get global backpressure info
	info := bpm.GetGlobalBackpressureInfo()
	if !info.Active {
		t.Error("Expected global backpressure info to show active")
	}
	
	if info.Reason != BackpressureReasonMemoryPressure {
		t.Errorf("Expected reason MemoryPressure, got %v", info.Reason)
	}
	
	// Deactivate global backpressure
	err = bpm.DeactivateGlobalBackpressure()
	if err != nil {
		t.Fatalf("Failed to deactivate global backpressure: %v", err)
	}
	
	// Check that global backpressure is no longer active
	if bpm.IsGlobalBackpressureActive() {
		t.Error("Expected global backpressure to be inactive after deactivation")
	}
}

func TestBackpressureManager_AutomaticGlobalEscalation(t *testing.T) {
	config := &BackpressureConfig{
		MaxStreams:        10,
		GlobalThreshold:   0.5, // 50% of streams
		AutoReleaseEnabled: false,
	}
	
	bpm := NewBackpressureManager(config)
	defer bpm.Shutdown()
	
	// Activate backpressure for multiple streams to trigger global escalation
	for i := uint64(1); i <= 6; i++ { // 6 out of 10 streams = 60% > 50%
		err := bpm.ActivateBackpressure(i, BackpressureReasonSlowConsumer)
		if err != nil {
			t.Fatalf("Failed to activate backpressure for stream %d: %v", i, err)
		}
	}
	
	// Global backpressure should be automatically activated
	if !bpm.IsGlobalBackpressureActive() {
		t.Error("Expected global backpressure to be automatically activated")
	}
}

func TestBackpressureManager_Callbacks(t *testing.T) {
	config := &BackpressureConfig{
		MaxStreams:              100,
		GlobalThreshold:         0.99, // Very high threshold to prevent automatic global activation
		StreamThreshold:         0.7,
		BackoffInitial:          10 * time.Millisecond,
		BackoffMax:              1 * time.Second,
		BackoffMultiplier:       2.0,
		AutoReleaseEnabled:      false, // Disable background worker to avoid deadlocks in tests
		AutoReleaseInterval:     100 * time.Millisecond,
		StatsUpdateInterval:     1 * time.Second,
		EnableDetailedLogging:   false,
	}
	bpm := NewBackpressureManager(config)
	defer bpm.Shutdown()
	
	// Set up callback
	var callbackEvents []struct {
		streamID uint64
		active   bool
		reason   BackpressureReason
	}
	
	var mu sync.Mutex
	callback := func(streamID uint64, active bool, reason BackpressureReason) {
		mu.Lock()
		defer mu.Unlock()
		callbackEvents = append(callbackEvents, struct {
			streamID uint64
			active   bool
			reason   BackpressureReason
		}{streamID, active, reason})
	}
	
	err := bpm.SetBackpressureCallback(callback)
	if err != nil {
		t.Fatalf("Failed to set callback: %v", err)
	}
	
	// Activate backpressure
	err = bpm.ActivateBackpressure(1, BackpressureReasonBufferFull)
	if err != nil {
		t.Fatalf("Failed to activate backpressure: %v", err)
	}
	
	// Deactivate backpressure
	err = bpm.DeactivateBackpressure(1)
	if err != nil {
		t.Fatalf("Failed to deactivate backpressure: %v", err)
	}
	
	// Give callbacks time to execute
	time.Sleep(10 * time.Millisecond)
	
	// Check callback events
	mu.Lock()
	defer mu.Unlock()
	

	
	if len(callbackEvents) < 2 {
		t.Errorf("Expected at least 2 callback events, got %d", len(callbackEvents))
	}
	
	// Find activation and deactivation events
	var activationEvent *struct {
		streamID uint64
		active   bool
		reason   BackpressureReason
	}
	var deactivationEvent *struct {
		streamID uint64
		active   bool
		reason   BackpressureReason
	}
	
	for _, event := range callbackEvents {
		if event.streamID == 1 {
			if event.active && event.reason == BackpressureReasonBufferFull {
				activationEvent = &event
			} else if !event.active {
				deactivationEvent = &event
			}
		}
	}
	
	// Verify we got the expected activation event
	if activationEvent == nil {
		t.Error("Expected to find activation event for stream 1 with BUFFER_FULL reason")
	}
	
	// Verify we got the expected deactivation event
	if deactivationEvent == nil {
		t.Error("Expected to find deactivation event for stream 1")
	}
}

func TestBackpressureManager_Statistics(t *testing.T) {
	bpm := NewBackpressureManager(nil)
	defer bpm.Shutdown()
	
	// Perform some operations to generate statistics
	bpm.ActivateBackpressure(1, BackpressureReasonBufferFull)
	bpm.ActivateBackpressure(2, BackpressureReasonSlowConsumer)
	bpm.DeactivateBackpressure(1)
	
	stats := bpm.GetBackpressureStats()
	
	if stats.TotalActivations < 2 {
		t.Errorf("Expected at least 2 activations, got %d", stats.TotalActivations)
	}
	
	if stats.ActiveStreams != 1 {
		t.Errorf("Expected 1 active stream, got %d", stats.ActiveStreams)
	}
	
	// Check reason breakdown
	if stats.ReasonBreakdown[BackpressureReasonBufferFull] < 1 {
		t.Error("Expected at least 1 BufferFull reason in breakdown")
	}
	
	if stats.ReasonBreakdown[BackpressureReasonSlowConsumer] < 1 {
		t.Error("Expected at least 1 SlowConsumer reason in breakdown")
	}
}

func TestBackpressureManager_ThresholdConfiguration(t *testing.T) {
	bpm := NewBackpressureManager(nil)
	defer bpm.Shutdown()
	
	// Test setting global threshold
	err := bpm.SetGlobalBackpressureThreshold(0.6)
	if err != nil {
		t.Fatalf("Failed to set global threshold: %v", err)
	}
	
	if threshold := bpm.GetGlobalBackpressureThreshold(); threshold != 0.6 {
		t.Errorf("Expected threshold 0.6, got %f", threshold)
	}
	
	// Test invalid threshold
	err = bpm.SetGlobalBackpressureThreshold(1.5)
	if err == nil {
		t.Error("Expected error for invalid threshold")
	}
}

func TestBackpressureManager_ForceOperations(t *testing.T) {
	bpm := NewBackpressureManager(nil)
	defer bpm.Shutdown()
	
	// Force global backpressure
	err := bpm.ForceGlobalBackpressure(BackpressureReasonMemoryPressure)
	if err != nil {
		t.Fatalf("Failed to force global backpressure: %v", err)
	}
	
	if !bpm.IsGlobalBackpressureActive() {
		t.Error("Expected global backpressure to be active after force")
	}
	
	// Force release
	err = bpm.ForceGlobalBackpressureRelease()
	if err != nil {
		t.Fatalf("Failed to force global backpressure release: %v", err)
	}
	
	if bpm.IsGlobalBackpressureActive() {
		t.Error("Expected global backpressure to be inactive after force release")
	}
}

func TestBackpressureManager_EscalationLogic(t *testing.T) {
	config := &BackpressureConfig{
		MaxStreams:      10,
		StreamThreshold: 0.4, // 40%
		AutoReleaseEnabled: false,
	}
	
	bpm := NewBackpressureManager(config)
	defer bpm.Shutdown()
	
	// First, create entries for all 10 streams by activating and then deactivating them
	for i := uint64(1); i <= 10; i++ {
		bpm.ActivateBackpressure(i, BackpressureReasonSlowConsumer)
		bpm.DeactivateBackpressure(i)
	}
	
	// Now activate backpressure for 3 out of 10 streams (30% < 40%)
	for i := uint64(1); i <= 3; i++ {
		bpm.ActivateBackpressure(i, BackpressureReasonSlowConsumer)
	}
	
	// Check current state before escalation
	stats := bpm.GetBackpressureStats()
	t.Logf("Before escalation: Active streams: %d, Total activations: %d", stats.ActiveStreams, stats.TotalActivations)
	
	// Escalation should fail due to insufficient threshold
	err := bpm.EscalateToGlobalBackpressure(BackpressureReasonSlowConsumer)
	if err == nil {
		t.Error("Expected escalation to fail due to insufficient threshold")
	} else {
		t.Logf("Escalation correctly failed: %v", err)
	}
	
	// Activate one more stream (40% = threshold)
	bpm.ActivateBackpressure(4, BackpressureReasonSlowConsumer)
	
	// Escalation should succeed now
	err = bpm.EscalateToGlobalBackpressure(BackpressureReasonSlowConsumer)
	if err != nil {
		t.Fatalf("Expected escalation to succeed: %v", err)
	}
	
	if !bpm.IsGlobalBackpressureActive() {
		t.Error("Expected global backpressure to be active after escalation")
	}
}

func TestBackpressureManager_ImpactAssessment(t *testing.T) {
	bpm := NewBackpressureManager(nil)
	defer bpm.Shutdown()
	
	// Initially no impact
	impact := bpm.GetBackpressureImpact()
	if impact.Level != BackpressureImpactNone {
		t.Errorf("Expected no impact initially, got %v", impact.Level)
	}
	
	// Activate backpressure for some streams
	for i := uint64(1); i <= 3; i++ {
		bpm.ActivateBackpressure(i, BackpressureReasonBufferFull)
	}
	
	// Should have some impact now
	impact = bpm.GetBackpressureImpact()
	if impact.Level == BackpressureImpactNone {
		t.Error("Expected some impact after activating backpressure")
	}
	
	if impact.ActiveStreams != 3 {
		t.Errorf("Expected 3 active streams, got %d", impact.ActiveStreams)
	}
	
	if impact.ImpactedByReason[BackpressureReasonBufferFull] != 3 {
		t.Errorf("Expected 3 streams impacted by BufferFull, got %d", 
			impact.ImpactedByReason[BackpressureReasonBufferFull])
	}
}

func TestBackpressureManager_GlobalBackpressureHistory(t *testing.T) {
	bpm := NewBackpressureManager(nil)
	defer bpm.Shutdown()
	
	// Activate and deactivate global backpressure
	bpm.ActivateGlobalBackpressure(BackpressureReasonMemoryPressure)
	time.Sleep(10 * time.Millisecond)
	bpm.DeactivateGlobalBackpressure()
	
	// Get history
	history := bpm.GetGlobalBackpressureHistory()
	
	if history.TotalActivations < 1 {
		t.Errorf("Expected at least 1 global activation, got %d", history.TotalActivations)
	}
	
	if history.ReasonBreakdown[BackpressureReasonMemoryPressure] < 1 {
		t.Error("Expected at least 1 MemoryPressure reason in history")
	}
}

func TestBackpressureManager_ConcurrentAccess(t *testing.T) {
	bpm := NewBackpressureManager(nil)
	defer bpm.Shutdown()
	
	var wg sync.WaitGroup
	errors := make(chan error, 100)
	
	// Concurrent activations
	for i := 1; i <= 50; i++ { // Start from 1 to avoid invalid stream ID 0
		wg.Add(1)
		go func(streamID uint64) {
			defer wg.Done()
			err := bpm.ActivateBackpressure(streamID, BackpressureReasonBufferFull)
			if err != nil {
				errors <- err
			}
		}(uint64(i))
	}
	
	// Concurrent deactivations
	for i := 1; i <= 50; i++ { // Start from 1 to avoid invalid stream ID 0
		wg.Add(1)
		go func(streamID uint64) {
			defer wg.Done()
			time.Sleep(5 * time.Millisecond) // Let activations happen first
			err := bpm.DeactivateBackpressure(streamID)
			if err != nil {
				errors <- err
			}
		}(uint64(i))
	}
	
	// Concurrent global operations
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bpm.ActivateGlobalBackpressure(BackpressureReasonMemoryPressure)
			time.Sleep(1 * time.Millisecond)
			bpm.DeactivateGlobalBackpressure()
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

func TestBackpressureManager_MaxStreamsLimit(t *testing.T) {
	config := &BackpressureConfig{
		MaxStreams:         3, // Very low limit for testing
		AutoReleaseEnabled: false,
	}
	
	bpm := NewBackpressureManager(config)
	defer bpm.Shutdown()
	
	// Activate backpressure for max streams
	for i := uint64(1); i <= 3; i++ {
		err := bpm.ActivateBackpressure(i, BackpressureReasonBufferFull)
		if err != nil {
			t.Fatalf("Failed to activate backpressure for stream %d: %v", i, err)
		}
	}
	
	// Fourth stream should fail
	err := bpm.ActivateBackpressure(4, BackpressureReasonBufferFull)
	if err == nil {
		t.Error("Expected error when exceeding max streams")
	}
}

func TestBackpressureManager_InvalidStreamID(t *testing.T) {
	bpm := NewBackpressureManager(nil)
	defer bpm.Shutdown()
	
	// Test with invalid stream ID (0)
	err := bpm.ActivateBackpressure(0, BackpressureReasonBufferFull)
	if err == nil {
		t.Error("Expected error for invalid stream ID 0")
	}
	
	err = bpm.DeactivateBackpressure(0)
	if err == nil {
		t.Error("Expected error for invalid stream ID 0")
	}
}

func TestBackpressureManager_ReasonStringConversion(t *testing.T) {
	reasons := []BackpressureReason{
		BackpressureReasonNone,
		BackpressureReasonWindowFull,
		BackpressureReasonBufferFull,
		BackpressureReasonSlowConsumer,
		BackpressureReasonMemoryPressure,
	}
	
	expectedStrings := []string{
		"NONE",
		"WINDOW_FULL",
		"BUFFER_FULL",
		"SLOW_CONSUMER",
		"MEMORY_PRESSURE",
	}
	
	for i, reason := range reasons {
		if reason.String() != expectedStrings[i] {
			t.Errorf("Expected reason %v to have string %q, got %q", 
				reason, expectedStrings[i], reason.String())
		}
	}
}

func TestBackpressureManager_ImpactLevelStringConversion(t *testing.T) {
	levels := []BackpressureImpactLevel{
		BackpressureImpactNone,
		BackpressureImpactLow,
		BackpressureImpactMedium,
		BackpressureImpactHigh,
		BackpressureImpactCritical,
	}
	
	expectedStrings := []string{
		"NONE",
		"LOW",
		"MEDIUM",
		"HIGH",
		"CRITICAL",
	}
	
	for i, level := range levels {
		if level.String() != expectedStrings[i] {
			t.Errorf("Expected level %v to have string %q, got %q", 
				level, expectedStrings[i], level.String())
		}
	}
}

func BenchmarkBackpressureManager_ActivateBackpressure(b *testing.B) {
	bpm := NewBackpressureManager(nil)
	defer bpm.Shutdown()
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		streamID := uint64(i % 1000) // Cycle through stream IDs
		bpm.ActivateBackpressure(streamID, BackpressureReasonBufferFull)
	}
}

func BenchmarkBackpressureManager_DeactivateBackpressure(b *testing.B) {
	bpm := NewBackpressureManager(nil)
	defer bpm.Shutdown()
	
	// Pre-activate backpressure for many streams
	for i := 0; i < 1000; i++ {
		bpm.ActivateBackpressure(uint64(i), BackpressureReasonBufferFull)
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		streamID := uint64(i % 1000)
		bpm.DeactivateBackpressure(streamID)
	}
}

func BenchmarkBackpressureManager_IsBackpressureActive(b *testing.B) {
	bpm := NewBackpressureManager(nil)
	defer bpm.Shutdown()
	
	// Pre-activate backpressure for some streams
	for i := 0; i < 500; i++ {
		bpm.ActivateBackpressure(uint64(i), BackpressureReasonBufferFull)
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		streamID := uint64(i % 1000)
		bpm.IsBackpressureActive(streamID)
	}
}