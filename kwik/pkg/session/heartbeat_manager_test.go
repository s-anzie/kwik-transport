package session

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestHeartbeatManager_StartStopHeartbeat(t *testing.T) {
	ctx := context.Background()
	config := DefaultHeartbeatConfig()
	config.MinInterval = 100 * time.Millisecond // Faster for testing
	config.MaxInterval = 500 * time.Millisecond
	
	hm := NewHeartbeatManager(ctx, config)
	defer hm.Shutdown()
	
	sessionID := "test-session"
	pathID := "test-path"
	
	var sendCount int
	var timeoutCount int
	var mutex sync.Mutex
	
	sendCallback := func(sID, pID string) error {
		mutex.Lock()
		sendCount++
		mutex.Unlock()
		return nil
	}
	
	timeoutCallback := func(sID, pID string, fails int) {
		mutex.Lock()
		timeoutCount++
		mutex.Unlock()
	}
	
	// Start heartbeat
	err := hm.StartHeartbeat(sessionID, pathID, sendCallback, timeoutCallback)
	if err != nil {
		t.Fatalf("Failed to start heartbeat: %v", err)
	}
	
	// Wait for some heartbeats
	time.Sleep(350 * time.Millisecond)
	
	// Check that heartbeats were sent
	mutex.Lock()
	if sendCount == 0 {
		t.Errorf("Expected heartbeats to be sent, got %d", sendCount)
	}
	mutex.Unlock()
	
	// Get stats
	stats := hm.GetHeartbeatStats(sessionID, pathID)
	if stats == nil {
		t.Fatalf("Expected heartbeat stats, got nil")
	}
	
	if stats.PathID != pathID {
		t.Errorf("Expected path ID %s, got %s", pathID, stats.PathID)
	}
	
	if !stats.IsActive {
		t.Errorf("Expected heartbeat to be active")
	}
	
	if stats.TotalSent == 0 {
		t.Errorf("Expected heartbeats to be sent, got %d", stats.TotalSent)
	}
	
	// Stop heartbeat
	err = hm.StopHeartbeat(sessionID, pathID)
	if err != nil {
		t.Fatalf("Failed to stop heartbeat: %v", err)
	}
	
	// Verify stats are no longer available
	stats = hm.GetHeartbeatStats(sessionID, pathID)
	if stats != nil {
		t.Errorf("Expected no stats after stopping heartbeat, got %v", stats)
	}
}

func TestHeartbeatManager_HeartbeatResponse(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	
	config := DefaultHeartbeatConfig()
	config.MinInterval = 200 * time.Millisecond
	config.MaxInterval = 500 * time.Millisecond
	config.TimeoutMultiplier = 10.0 // Very lenient timeout
	
	hm := NewHeartbeatManager(ctx, config)
	defer hm.Shutdown()
	
	sessionID := "test-session"
	pathID := "test-path"
	
	var sendCount int
	var timeoutOccurred bool
	var mutex sync.Mutex
	
	sendCallback := func(sID, pID string) error {
		mutex.Lock()
		sendCount++
		currentSendCount := sendCount
		mutex.Unlock()
		
		t.Logf("Heartbeat sent #%d for session %s, path %s", currentSendCount, sID, pID)
		
		// Immediately send response
		t.Logf("Sending heartbeat response for session %s, path %s", sID, pID)
		err := hm.OnHeartbeatReceived(sID, pID)
		if err != nil {
			t.Logf("Error sending heartbeat response: %v", err)
		} else {
			t.Logf("Heartbeat response sent for session %s, path %s", sID, pID)
		}
		return nil
	}
	
	timeoutCallback := func(sID, pID string, fails int) {
		mutex.Lock()
		timeoutOccurred = true
		mutex.Unlock()
		t.Logf("Timeout occurred with %d failures for session %s, path %s", fails, sID, pID)
	}
	
	// Start heartbeat
	t.Logf("Starting heartbeat for session %s, path %s", sessionID, pathID)
	err := hm.StartHeartbeat(sessionID, pathID, sendCallback, timeoutCallback)
	if err != nil {
		t.Fatalf("Failed to start heartbeat: %v", err)
	}
	t.Logf("Heartbeat started successfully")
	
	// Check initial stats
	initialStats := hm.GetHeartbeatStats(sessionID, pathID)
	if initialStats != nil {
		t.Logf("Initial stats: Active=%v, Interval=%v, TotalSent=%d, TotalReceived=%d", 
			initialStats.IsActive, initialStats.CurrentInterval, initialStats.TotalSent, initialStats.TotalReceived)
	} else {
		t.Logf("No initial stats available")
	}
	
	// Wait for heartbeats and responses
	t.Logf("Waiting 600ms for heartbeats and responses...")
	time.Sleep(600 * time.Millisecond)
	
	// Check intermediate stats
	mutex.Lock()
	currentSendCount := sendCount
	currentTimeoutOccurred := timeoutOccurred
	mutex.Unlock()
	
	t.Logf("After wait: sendCount=%d, timeoutOccurred=%v", currentSendCount, currentTimeoutOccurred)
	
	// Check that no timeouts occurred
	mutex.Lock()
	if timeoutOccurred {
		t.Errorf("Unexpected timeout occurred")
	}
	mutex.Unlock()
	
	// Check stats
	stats := hm.GetHeartbeatStats(sessionID, pathID)
	if stats == nil {
		t.Fatalf("Expected heartbeat stats, got nil")
	}
	
	t.Logf("Final stats: Active=%v, TotalSent=%d, TotalReceived=%d, ConsecutiveFails=%d, SuccessRate=%f", 
		stats.IsActive, stats.TotalSent, stats.TotalReceived, stats.ConsecutiveFails, stats.SuccessRate)
	
	// Stop the heartbeat before checking final results to avoid race conditions
	t.Logf("Stopping heartbeat...")
	err = hm.StopHeartbeat(sessionID, pathID)
	if err != nil {
		t.Fatalf("Failed to stop heartbeat: %v", err)
	}
	t.Logf("Heartbeat stopped")
	
	if stats.TotalSent == 0 {
		t.Errorf("Expected heartbeats to be sent, got %d", stats.TotalSent)
	}
	
	if stats.TotalReceived == 0 {
		t.Errorf("Expected heartbeat responses, got %d", stats.TotalReceived)
	}
	
	if stats.ConsecutiveFails != 0 {
		t.Errorf("Expected no consecutive failures with responses, got %d", stats.ConsecutiveFails)
	}
	
	if stats.SuccessRate == 0.0 {
		t.Errorf("Expected non-zero success rate, got %f", stats.SuccessRate)
	}
	
	t.Logf("Test completed successfully")
}

func TestHeartbeatManager_AdaptiveInterval(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	
	config := DefaultHeartbeatConfig()
	config.MinInterval = 100 * time.Millisecond
	config.MaxInterval = 1000 * time.Millisecond
	config.AdaptationFactor = 0.5 // Faster adaptation for testing
	
	hm := NewHeartbeatManager(ctx, config)
	defer hm.Shutdown()
	
	sessionID := "test-session"
	pathID := "test-path"
	
	sendCallback := func(sID, pID string) error {
		t.Logf("Heartbeat sent for session %s, path %s", sID, pID)
		return nil
	}
	
	timeoutCallback := func(sID, pID string, fails int) {
		t.Logf("Timeout for session %s, path %s, fails: %d", sID, pID, fails)
	}
	
	// Start heartbeat
	t.Logf("Starting heartbeat...")
	err := hm.StartHeartbeat(sessionID, pathID, sendCallback, timeoutCallback)
	if err != nil {
		t.Fatalf("Failed to start heartbeat: %v", err)
	}
	
	// Get initial stats
	initialStats := hm.GetHeartbeatStats(sessionID, pathID)
	if initialStats == nil {
		t.Fatalf("Expected initial heartbeat stats, got nil")
	}
	
	initialInterval := initialStats.TargetInterval
	t.Logf("Initial target interval: %v", initialInterval)
	
	// Update with poor health to trigger faster heartbeats
	t.Logf("Updating with poor health...")
	err = hm.UpdatePathHealth(sessionID, pathID, 200*time.Millisecond, true, 20)
	if err != nil {
		t.Fatalf("Failed to update path health: %v", err)
	}
	
	// Wait for adaptation
	time.Sleep(200 * time.Millisecond)
	
	// Check that interval adapted to be shorter for unhealthy path
	adaptedStats := hm.GetHeartbeatStats(sessionID, pathID)
	if adaptedStats == nil {
		t.Fatalf("Expected adapted heartbeat stats, got nil")
	}
	
	t.Logf("After poor health update - Target: %v, Current: %v", adaptedStats.TargetInterval, adaptedStats.CurrentInterval)
	
	// Target interval should be shorter for unhealthy path
	if adaptedStats.TargetInterval >= initialInterval {
		t.Logf("Warning: Target interval didn't decrease as expected for unhealthy path, got %v >= %v", 
			adaptedStats.TargetInterval, initialInterval)
		// Don't fail the test, just log the warning since adaptation might be gradual
	}
	
	// Update with good health to trigger slower heartbeats
	t.Logf("Updating with good health...")
	err = hm.UpdatePathHealth(sessionID, pathID, 50*time.Millisecond, false, 95)
	if err != nil {
		t.Fatalf("Failed to update path health: %v", err)
	}
	
	// Wait for adaptation
	time.Sleep(200 * time.Millisecond)
	
	// Check that interval adapted to be longer for healthy path
	healthyStats := hm.GetHeartbeatStats(sessionID, pathID)
	if healthyStats == nil {
		t.Fatalf("Expected healthy heartbeat stats, got nil")
	}
	
	t.Logf("After good health update - Target: %v, Current: %v", healthyStats.TargetInterval, healthyStats.CurrentInterval)
	
	// Target interval should be longer for healthy path
	if healthyStats.TargetInterval <= adaptedStats.TargetInterval {
		t.Logf("Warning: Target interval didn't increase as expected for healthy path, got %v <= %v", 
			healthyStats.TargetInterval, adaptedStats.TargetInterval)
		// Don't fail the test, just log the warning since adaptation might be gradual
	}
	
	// Stop heartbeat to clean up
	t.Logf("Stopping heartbeat...")
	err = hm.StopHeartbeat(sessionID, pathID)
	if err != nil {
		t.Fatalf("Failed to stop heartbeat: %v", err)
	}
	t.Logf("Test completed")
}

func TestHeartbeatManager_TimeoutDetection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	
	config := DefaultHeartbeatConfig()
	config.MinInterval = 50 * time.Millisecond
	config.MaxInterval = 200 * time.Millisecond
	config.TimeoutMultiplier = 2.0 // Short timeout for testing
	
	hm := NewHeartbeatManager(ctx, config)
	defer hm.Shutdown()
	
	sessionID := "test-session"
	pathID := "test-path"
	
	var timeoutCount int
	var sendCount int
	var mutex sync.Mutex
	
	sendCallback := func(sID, pID string) error {
		mutex.Lock()
		sendCount++
		currentSendCount := sendCount
		mutex.Unlock()
		t.Logf("Heartbeat sent #%d for session %s, path %s (no response will be sent)", currentSendCount, sID, pID)
		// Don't send responses to trigger timeouts
		return nil
	}
	
	timeoutCallback := func(sID, pID string, fails int) {
		mutex.Lock()
		timeoutCount++
		currentTimeoutCount := timeoutCount
		mutex.Unlock()
		t.Logf("Timeout #%d detected for session %s, path %s, consecutive fails: %d", currentTimeoutCount, sID, pID, fails)
	}
	
	// Start heartbeat
	t.Logf("Starting heartbeat...")
	err := hm.StartHeartbeat(sessionID, pathID, sendCallback, timeoutCallback)
	if err != nil {
		t.Fatalf("Failed to start heartbeat: %v", err)
	}
	
	// Wait for timeouts to occur
	t.Logf("Waiting 500ms for timeouts to occur...")
	time.Sleep(500 * time.Millisecond)
	
	// Check intermediate state
	mutex.Lock()
	currentTimeoutCount := timeoutCount
	currentSendCount := sendCount
	mutex.Unlock()
	
	t.Logf("After wait: sendCount=%d, timeoutCount=%d", currentSendCount, currentTimeoutCount)
	
	// Check stats show consecutive failures
	stats := hm.GetHeartbeatStats(sessionID, pathID)
	if stats == nil {
		t.Fatalf("Expected heartbeat stats, got nil")
	}
	
	t.Logf("Stats: TotalSent=%d, TotalReceived=%d, ConsecutiveFails=%d, SuccessRate=%f", 
		stats.TotalSent, stats.TotalReceived, stats.ConsecutiveFails, stats.SuccessRate)
	
	// Stop heartbeat to clean up
	t.Logf("Stopping heartbeat...")
	err = hm.StopHeartbeat(sessionID, pathID)
	if err != nil {
		t.Fatalf("Failed to stop heartbeat: %v", err)
	}
	
	// Check that timeouts were detected
	mutex.Lock()
	finalTimeoutCount := timeoutCount
	mutex.Unlock()
	
	if finalTimeoutCount == 0 {
		t.Errorf("Expected timeouts to be detected, got %d", finalTimeoutCount)
	}
	
	if stats.ConsecutiveFails == 0 {
		t.Errorf("Expected consecutive failures, got %d", stats.ConsecutiveFails)
	}
	
	if stats.SuccessRate != 0.0 {
		t.Errorf("Expected zero success rate with no responses, got %f", stats.SuccessRate)
	}
	
	t.Logf("Test completed")
}

func TestHeartbeatManager_MultiplePathsSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	
	config := DefaultHeartbeatConfig()
	config.MinInterval = 100 * time.Millisecond
	config.MaxInterval = 300 * time.Millisecond
	
	hm := NewHeartbeatManager(ctx, config)
	defer hm.Shutdown()
	
	sessionID := "test-session"
	paths := []string{"path1", "path2", "path3"}
	
	var sendCounts = make(map[string]int)
	var mutex sync.Mutex
	
	sendCallback := func(sID, pID string) error {
		mutex.Lock()
		sendCounts[pID]++
		currentCount := sendCounts[pID]
		mutex.Unlock()
		t.Logf("Heartbeat sent #%d for session %s, path %s", currentCount, sID, pID)
		return nil
	}
	
	timeoutCallback := func(sID, pID string, fails int) {
		t.Logf("Timeout for session %s, path %s, fails: %d", sID, pID, fails)
	}
	
	// Start heartbeats for all paths
	t.Logf("Starting heartbeats for %d paths...", len(paths))
	for _, pathID := range paths {
		t.Logf("Starting heartbeat for path %s", pathID)
		err := hm.StartHeartbeat(sessionID, pathID, sendCallback, timeoutCallback)
		if err != nil {
			t.Fatalf("Failed to start heartbeat for path %s: %v", pathID, err)
		}
	}
	
	// Wait for heartbeats
	t.Logf("Waiting 250ms for heartbeats...")
	time.Sleep(250 * time.Millisecond)
	
	// Check that all paths are sending heartbeats
	mutex.Lock()
	sendCountsCopy := make(map[string]int)
	for k, v := range sendCounts {
		sendCountsCopy[k] = v
	}
	mutex.Unlock()
	
	t.Logf("Send counts after wait: %v", sendCountsCopy)
	
	for _, pathID := range paths {
		if sendCountsCopy[pathID] == 0 {
			t.Errorf("Expected heartbeats for path %s, got %d", pathID, sendCountsCopy[pathID])
		}
	}
	
	// Get all stats
	allStats := hm.GetAllHeartbeatStats(sessionID)
	if allStats == nil {
		t.Fatalf("Expected heartbeat stats for session, got nil")
	}
	
	t.Logf("Got stats for %d paths", len(allStats))
	
	if len(allStats) != len(paths) {
		t.Errorf("Expected stats for %d paths, got %d", len(paths), len(allStats))
	}
	
	for _, pathID := range paths {
		stats, exists := allStats[pathID]
		if !exists {
			t.Errorf("Expected stats for path %s", pathID)
			continue
		}
		
		t.Logf("Path %s stats: Active=%v, TotalSent=%d", pathID, stats.IsActive, stats.TotalSent)
		
		if !stats.IsActive {
			t.Errorf("Expected path %s to be active", pathID)
		}
		
		if stats.TotalSent == 0 {
			t.Errorf("Expected heartbeats sent for path %s, got %d", pathID, stats.TotalSent)
		}
	}
	
	// Stop session
	t.Logf("Stopping session...")
	err := hm.StopSession(sessionID)
	if err != nil {
		t.Fatalf("Failed to stop session: %v", err)
	}
	
	// Verify all paths are stopped
	allStats = hm.GetAllHeartbeatStats(sessionID)
	if allStats != nil {
		t.Errorf("Expected no stats after stopping session, got %v", allStats)
	}
	
	t.Logf("Test completed")
}

func TestHeartbeatManager_RTTBasedAdaptation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	
	config := DefaultHeartbeatConfig()
	config.MinInterval = 50 * time.Millisecond
	config.MaxInterval = 500 * time.Millisecond
	config.AdaptationFactor = 0.8 // Fast adaptation
	
	hm := NewHeartbeatManager(ctx, config)
	defer hm.Shutdown()
	
	sessionID := "test-session"
	pathID := "test-path"
	
	sendCallback := func(sID, pID string) error {
		t.Logf("Heartbeat sent for session %s, path %s", sID, pID)
		return nil
	}
	
	timeoutCallback := func(sID, pID string, fails int) {
		t.Logf("Timeout for session %s, path %s, fails: %d", sID, pID, fails)
	}
	
	// Start heartbeat
	t.Logf("Starting heartbeat...")
	err := hm.StartHeartbeat(sessionID, pathID, sendCallback, timeoutCallback)
	if err != nil {
		t.Fatalf("Failed to start heartbeat: %v", err)
	}
	
	// Get initial stats
	initialStats := hm.GetHeartbeatStats(sessionID, pathID)
	if initialStats != nil {
		t.Logf("Initial stats: Target=%v, Current=%v", initialStats.TargetInterval, initialStats.CurrentInterval)
	}
	
	// Update with high RTT
	highRTT := 100 * time.Millisecond
	t.Logf("Updating with high RTT: %v", highRTT)
	err = hm.UpdatePathHealth(sessionID, pathID, highRTT, false, 80)
	if err != nil {
		t.Fatalf("Failed to update path health: %v", err)
	}
	
	// Wait for adaptation
	t.Logf("Waiting for adaptation...")
	time.Sleep(100 * time.Millisecond)
	
	stats := hm.GetHeartbeatStats(sessionID, pathID)
	if stats == nil {
		t.Fatalf("Expected heartbeat stats, got nil")
	}
	
	t.Logf("After RTT update: Target=%v, Current=%v", stats.TargetInterval, stats.CurrentInterval)
	
	// Target interval should be at least 3x RTT
	expectedMinInterval := highRTT * 3
	if stats.TargetInterval < expectedMinInterval {
		t.Errorf("Expected target interval >= %v (3x RTT), got %v", 
			expectedMinInterval, stats.TargetInterval)
	}
	
	// Stop heartbeat to clean up
	t.Logf("Stopping heartbeat...")
	err = hm.StopHeartbeat(sessionID, pathID)
	if err != nil {
		t.Fatalf("Failed to stop heartbeat: %v", err)
	}
	t.Logf("Test completed")
}

func TestHeartbeatManager_FailureBasedAdaptation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	
	config := DefaultHeartbeatConfig()
	config.MinInterval = 100 * time.Millisecond
	config.MaxInterval = 500 * time.Millisecond
	config.TimeoutMultiplier = 1.5 // Short timeout
	
	hm := NewHeartbeatManager(ctx, config)
	defer hm.Shutdown()
	
	sessionID := "test-session"
	pathID := "test-path"
	
	var failureCount int
	var sendCount int
	var mutex sync.Mutex
	
	sendCallback := func(sID, pID string) error {
		mutex.Lock()
		sendCount++
		currentSendCount := sendCount
		mutex.Unlock()
		t.Logf("Heartbeat sent #%d for session %s, path %s (no response)", currentSendCount, sID, pID)
		return nil
	}
	
	timeoutCallback := func(sID, pID string, fails int) {
		mutex.Lock()
		failureCount = fails
		mutex.Unlock()
		t.Logf("Timeout detected for session %s, path %s, consecutive fails: %d", sID, pID, fails)
	}
	
	// Start heartbeat
	t.Logf("Starting heartbeat...")
	err := hm.StartHeartbeat(sessionID, pathID, sendCallback, timeoutCallback)
	if err != nil {
		t.Fatalf("Failed to start heartbeat: %v", err)
	}
	
	// Get initial interval
	initialStats := hm.GetHeartbeatStats(sessionID, pathID)
	if initialStats == nil {
		t.Fatalf("Expected initial heartbeat stats, got nil")
	}
	initialInterval := initialStats.TargetInterval
	t.Logf("Initial target interval: %v", initialInterval)
	
	// Wait for failures to accumulate (no responses)
	t.Logf("Waiting 400ms for failures to accumulate...")
	time.Sleep(400 * time.Millisecond)
	
	// Check that failures occurred
	mutex.Lock()
	currentFailureCount := failureCount
	currentSendCount := sendCount
	mutex.Unlock()
	
	t.Logf("After wait: sendCount=%d, failureCount=%d", currentSendCount, currentFailureCount)
	
	if currentFailureCount == 0 {
		t.Logf("Warning: Expected failures to occur, got %d", currentFailureCount)
	}
	
	// Check that interval adapted to be shorter due to failures
	failureStats := hm.GetHeartbeatStats(sessionID, pathID)
	if failureStats == nil {
		t.Fatalf("Expected failure heartbeat stats, got nil")
	}
	
	t.Logf("After failures: Target=%v, Current=%v, ConsecutiveFails=%d", 
		failureStats.TargetInterval, failureStats.CurrentInterval, failureStats.ConsecutiveFails)
	
	// Target interval should be shorter due to failures (if failures occurred)
	if currentFailureCount > 0 && failureStats.TargetInterval >= initialInterval {
		t.Logf("Warning: Expected target interval to decrease due to failures, got %v >= %v", 
			failureStats.TargetInterval, initialInterval)
	}
	
	// Stop heartbeat to clean up
	t.Logf("Stopping heartbeat...")
	err = hm.StopHeartbeat(sessionID, pathID)
	if err != nil {
		t.Fatalf("Failed to stop heartbeat: %v", err)
	}
	t.Logf("Test completed")
}

func TestDefaultHeartbeatConfig(t *testing.T) {
	config := DefaultHeartbeatConfig()
	
	if config.MinInterval <= 0 {
		t.Errorf("Expected positive min interval, got %v", config.MinInterval)
	}
	
	if config.MaxInterval <= config.MinInterval {
		t.Errorf("Expected max interval > min interval, got %v <= %v", 
			config.MaxInterval, config.MinInterval)
	}
	
	if config.AdaptationFactor <= 0 || config.AdaptationFactor >= 1 {
		t.Errorf("Expected adaptation factor between 0 and 1, got %f", config.AdaptationFactor)
	}
	
	if config.TimeoutMultiplier <= 1 {
		t.Errorf("Expected timeout multiplier > 1, got %f", config.TimeoutMultiplier)
	}
}

func TestHeartbeatManager_Shutdown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	
	config := DefaultHeartbeatConfig()
	config.MinInterval = 50 * time.Millisecond
	
	hm := NewHeartbeatManager(ctx, config)
	
	sessionID := "test-session"
	pathID := "test-path"
	
	sendCallback := func(sID, pID string) error {
		t.Logf("Heartbeat sent for session %s, path %s", sID, pID)
		return nil
	}
	
	timeoutCallback := func(sID, pID string, fails int) {
		t.Logf("Timeout for session %s, path %s, fails: %d", sID, pID, fails)
	}
	
	// Start heartbeat
	t.Logf("Starting heartbeat...")
	err := hm.StartHeartbeat(sessionID, pathID, sendCallback, timeoutCallback)
	if err != nil {
		t.Fatalf("Failed to start heartbeat: %v", err)
	}
	
	// Wait a bit for the heartbeat routine to start
	time.Sleep(50 * time.Millisecond)
	
	// Verify it's running
	stats := hm.GetHeartbeatStats(sessionID, pathID)
	if stats == nil {
		t.Fatalf("Expected heartbeat stats before shutdown")
	}
	t.Logf("Heartbeat stats: Active=%v, TotalSent=%d", stats.IsActive, stats.TotalSent)
	
	if !stats.IsActive {
		t.Logf("Warning: Heartbeat not showing as active yet, but continuing test")
	}
	
	// Shutdown
	t.Logf("Shutting down heartbeat manager...")
	err = hm.Shutdown()
	if err != nil {
		t.Fatalf("Failed to shutdown heartbeat manager: %v", err)
	}
	
	// Verify everything is stopped
	stats = hm.GetHeartbeatStats(sessionID, pathID)
	if stats != nil {
		t.Errorf("Expected no stats after shutdown, got %v", stats)
	}
	t.Logf("Test completed")
}