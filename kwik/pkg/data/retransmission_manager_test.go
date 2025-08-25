package data

import (
	"fmt"
	"kwik/pkg/logger"
	"sync"
	"testing"
	"time"
)

func TestRetransmissionManager_Basic(t *testing.T) {
	logger := &logger.MockLogger{}
	config := &RetransmissionConfig{
		DefaultTimeout:       100 * time.Millisecond,
		CleanupInterval:      1 * time.Second,
		MaxConcurrentRetries: 10,
		EnableDetailedStats:  true,
		Logger:               logger,
	}

	rm := NewRetransmissionManager(config)

	// Test start
	err := rm.Start()
	if err != nil {
		t.Fatalf("Failed to start retransmission manager: %v", err)
	}

	// Test double start
	err = rm.Start()
	if err == nil {
		t.Error("Expected error when starting already running manager")
	}

	// Test stop
	err = rm.Stop()
	if err != nil {
		t.Fatalf("Failed to stop retransmission manager: %v", err)
	}

	// Test double stop
	err = rm.Stop()
	if err != nil {
		t.Error("Stop should be idempotent")
	}
}

func TestRetransmissionManager_TrackSegment(t *testing.T) {
	logger := &logger.MockLogger{}
	rm := NewRetransmissionManager(DefaultRetransmissionConfig())
	rm.logger = logger

	err := rm.Start()
	if err != nil {
		t.Fatalf("Failed to start retransmission manager: %v", err)
	}
	defer rm.Stop()

	// Track a segment
	segmentID := "test-segment-1"
	data := []byte("test data")
	pathID := "test-path"
	timeout := 100 * time.Millisecond

	err = rm.TrackSegment(segmentID, data, pathID, timeout)
	if err != nil {
		t.Fatalf("Failed to track segment: %v", err)
	}

	// Check stats
	stats := rm.GetStats()
	if stats.TotalSegments != 1 {
		t.Errorf("Expected 1 total segment, got %d", stats.TotalSegments)
	}
	if stats.ActiveSegments != 1 {
		t.Errorf("Expected 1 active segment, got %d", stats.ActiveSegments)
	}
}

func TestRetransmissionManager_AckSegment(t *testing.T) {
	logger := &logger.MockLogger{}
	rm := NewRetransmissionManager(DefaultRetransmissionConfig())
	rm.logger = logger

	err := rm.Start()
	if err != nil {
		t.Fatalf("Failed to start retransmission manager: %v", err)
	}
	defer rm.Stop()

	// Track a segment
	segmentID := "test-segment-1"
	data := []byte("test data")
	pathID := "test-path"
	timeout := 100 * time.Millisecond

	err = rm.TrackSegment(segmentID, data, pathID, timeout)
	if err != nil {
		t.Fatalf("Failed to track segment: %v", err)
	}

	// Acknowledge the segment
	err = rm.AckSegment(segmentID)
	if err != nil {
		t.Fatalf("Failed to acknowledge segment: %v", err)
	}

	// Check stats
	stats := rm.GetStats()
	if stats.AcknowledgedSegments != 1 {
		t.Errorf("Expected 1 acknowledged segment, got %d", stats.AcknowledgedSegments)
	}
	if stats.ActiveSegments != 0 {
		t.Errorf("Expected 0 active segments, got %d", stats.ActiveSegments)
	}

	// Try to acknowledge non-existent segment
	err = rm.AckSegment("non-existent")
	if err == nil {
		t.Error("Expected error when acknowledging non-existent segment")
	}
}

func TestRetransmissionManager_Retransmission(t *testing.T) {
	logger := &logger.MockLogger{}
	rm := NewRetransmissionManager(DefaultRetransmissionConfig())
	rm.logger = logger

	// Set up callbacks
	retryCount := 0
	maxRetriesCount := 0

	rm.SetRetryCallback(func(segmentID string, data []byte, pathID string) error {
		retryCount++
		return nil
	})

	rm.SetMaxRetriesCallback(func(segmentID string, data []byte, pathID string) error {
		maxRetriesCount++
		return nil
	})

	// Use a strategy with low max attempts for testing
	strategy := NewCustomExponentialBackoffStrategy(10*time.Millisecond, 100*time.Millisecond, 2.0, 2)
	rm.SetBackoffStrategy(strategy)

	err := rm.Start()
	if err != nil {
		t.Fatalf("Failed to start retransmission manager: %v", err)
	}
	defer rm.Stop()

	// Track a segment with short timeout
	segmentID := "test-segment-1"
	data := []byte("test data")
	pathID := "test-path"
	timeout := 10 * time.Millisecond

	err = rm.TrackSegment(segmentID, data, pathID, timeout)
	if err != nil {
		t.Fatalf("Failed to track segment: %v", err)
	}

	// Wait for retransmissions to occur
	time.Sleep(200 * time.Millisecond)

	// Check that retries occurred
	if retryCount == 0 {
		t.Error("Expected at least one retry")
	}

	// Check that max retries was reached
	if maxRetriesCount != 1 {
		t.Errorf("Expected 1 max retries callback, got %d", maxRetriesCount)
	}

	// Check stats
	stats := rm.GetStats()
	if stats.DroppedSegments != 1 {
		t.Errorf("Expected 1 dropped segment, got %d", stats.DroppedSegments)
	}
}

func TestRetransmissionManager_BackoffStrategy(t *testing.T) {
	rm := NewRetransmissionManager(DefaultRetransmissionConfig())

	// Test setting valid strategy
	strategy := NewExponentialBackoffStrategy()
	err := rm.SetBackoffStrategy(strategy)
	if err != nil {
		t.Fatalf("Failed to set backoff strategy: %v", err)
	}

	// Test setting nil strategy
	err = rm.SetBackoffStrategy(nil)
	if err == nil {
		t.Error("Expected error when setting nil backoff strategy")
	}
}

func TestExponentialBackoffStrategy(t *testing.T) {
	strategy := NewExponentialBackoffStrategy()

	baseRTT := 50 * time.Millisecond

	// Test delay calculation
	delay1 := strategy.NextDelay(1, baseRTT)
	delay2 := strategy.NextDelay(2, baseRTT)
	delay3 := strategy.NextDelay(3, baseRTT)

	// Delays should increase exponentially
	if delay2 <= delay1 {
		t.Errorf("Expected delay2 (%v) > delay1 (%v)", delay2, delay1)
	}
	if delay3 <= delay2 {
		t.Errorf("Expected delay3 (%v) > delay2 (%v)", delay3, delay2)
	}

	// Test max attempts
	if strategy.MaxAttempts() != 5 {
		t.Errorf("Expected 5 max attempts, got %d", strategy.MaxAttempts())
	}

	// Test should retry
	if !strategy.ShouldRetry(1, nil) {
		t.Error("Should retry on first attempt")
	}
	if strategy.ShouldRetry(10, nil) {
		t.Error("Should not retry after max attempts")
	}
}

func TestLinearBackoffStrategy(t *testing.T) {
	strategy := NewLinearBackoffStrategy(50*time.Millisecond, 20*time.Millisecond, 200*time.Millisecond, 3)

	baseRTT := 10 * time.Millisecond // Small RTT so linear progression dominates

	// Test delay calculation
	delay1 := strategy.NextDelay(1, baseRTT)
	delay2 := strategy.NextDelay(2, baseRTT)
	delay3 := strategy.NextDelay(3, baseRTT)

	// Delays should increase linearly
	if delay2 <= delay1 {
		t.Errorf("Expected delay2 (%v) > delay1 (%v)", delay2, delay1)
	}

	if delay3 <= delay2 {
		t.Errorf("Expected delay3 (%v) > delay2 (%v)", delay3, delay2)
	}
}

func TestFixedBackoffStrategy(t *testing.T) {
	fixedDelay := 50 * time.Millisecond
	strategy := NewFixedBackoffStrategy(fixedDelay, 3)

	baseRTT := 20 * time.Millisecond

	// All delays should be the same (or based on RTT if larger)
	delay1 := strategy.NextDelay(1, baseRTT)
	delay2 := strategy.NextDelay(2, baseRTT)
	delay3 := strategy.NextDelay(3, baseRTT)

	// Since baseRTT * 1.2 = 24ms < 50ms, should use fixed delay
	if delay1 != fixedDelay {
		t.Errorf("Expected delay1 = %v, got %v", fixedDelay, delay1)
	}
	if delay2 != fixedDelay {
		t.Errorf("Expected delay2 = %v, got %v", fixedDelay, delay2)
	}
	if delay3 != fixedDelay {
		t.Errorf("Expected delay3 = %v, got %v", fixedDelay, delay3)
	}
}

func TestAdaptiveBackoffStrategy(t *testing.T) {
	strategy := NewAdaptiveBackoffStrategy()

	// Test with different RTT values - need to build up history first
	rtt1 := 10 * time.Millisecond
	rtt2 := 50 * time.Millisecond
	rtt3 := 100 * time.Millisecond

	// Build RTT history
	for i := 0; i < 5; i++ {
		strategy.NextDelay(1, rtt1)
	}
	delay1 := strategy.NextDelay(1, rtt1)

	for i := 0; i < 5; i++ {
		strategy.NextDelay(1, rtt3)
	}
	delay3 := strategy.NextDelay(1, rtt3)

	// Delays should adapt to RTT (allow for some variance due to smoothing)
	if delay3 <= delay1 {
		t.Errorf("Expected delay with highest RTT (%v) > delay with lowest RTT (%v)", delay3, delay1)
	}

	// Test exponential increase with attempts
	attempt1 := strategy.NextDelay(1, rtt2)
	attempt2 := strategy.NextDelay(2, rtt2)
	attempt3 := strategy.NextDelay(3, rtt2)

	if attempt2 <= attempt1 {
		t.Errorf("Expected delay for attempt 2 (%v) > attempt 1 (%v)", attempt2, attempt1)
	}
	if attempt3 <= attempt2 {
		t.Errorf("Expected delay for attempt 3 (%v) > attempt 2 (%v)", attempt3, attempt2)
	}
}

func TestRetransmissionManager_Concurrent(t *testing.T) {
	logger := &logger.MockLogger{}
	rm := NewRetransmissionManager(DefaultRetransmissionConfig())
	rm.logger = logger

	err := rm.Start()
	if err != nil {
		t.Fatalf("Failed to start retransmission manager: %v", err)
	}
	defer rm.Stop()

	// Track multiple segments concurrently
	numSegments := 100
	var wg sync.WaitGroup

	for i := 0; i < numSegments; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			segmentID := fmt.Sprintf("segment-%d", id)
			data := []byte(fmt.Sprintf("data-%d", id))
			pathID := fmt.Sprintf("path-%d", id%5) // 5 different paths
			timeout := 100 * time.Millisecond

			err := rm.TrackSegment(segmentID, data, pathID, timeout)
			if err != nil {
				t.Errorf("Failed to track segment %s: %v", segmentID, err)
			}

			// Acknowledge half of the segments
			if id%2 == 0 {
				time.Sleep(10 * time.Millisecond) // Small delay
				err := rm.AckSegment(segmentID)
				if err != nil {
					t.Errorf("Failed to acknowledge segment %s: %v", segmentID, err)
				}
			}
		}(i)
	}

	wg.Wait()

	// Wait a bit for processing
	time.Sleep(50 * time.Millisecond)

	// Check stats
	stats := rm.GetStats()
	if stats.TotalSegments != uint64(numSegments) {
		t.Errorf("Expected %d total segments, got %d", numSegments, stats.TotalSegments)
	}
	if stats.AcknowledgedSegments != uint64(numSegments/2) {
		t.Errorf("Expected %d acknowledged segments, got %d", numSegments/2, stats.AcknowledgedSegments)
	}
}

func TestRetransmissionManager_NotRunning(t *testing.T) {
	rm := NewRetransmissionManager(DefaultRetransmissionConfig())

	// Try to track segment when not running
	err := rm.TrackSegment("test", []byte("data"), "path", time.Second)
	if err == nil {
		t.Error("Expected error when tracking segment on stopped manager")
	}
}

func BenchmarkRetransmissionManager_TrackSegment(b *testing.B) {
	rm := NewRetransmissionManager(DefaultRetransmissionConfig())
	rm.Start()
	defer rm.Stop()

	data := []byte("benchmark data")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		segmentID := fmt.Sprintf("segment-%d", i)
		rm.TrackSegment(segmentID, data, "path", 100*time.Millisecond)
	}
}

func BenchmarkRetransmissionManager_AckSegment(b *testing.B) {
	rm := NewRetransmissionManager(DefaultRetransmissionConfig())
	rm.Start()
	defer rm.Stop()

	data := []byte("benchmark data")

	// Pre-populate segments
	for i := 0; i < b.N; i++ {
		segmentID := fmt.Sprintf("segment-%d", i)
		rm.TrackSegment(segmentID, data, "path", time.Hour) // Long timeout to avoid retransmission
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		segmentID := fmt.Sprintf("segment-%d", i)
		rm.AckSegment(segmentID)
	}
}
