package data

import (
	"kwik/pkg/logger"
	"sync"
	"testing"
	"time"
)

func TestOffsetCoordinator_Basic(t *testing.T) {
	logger := &logger.MockLogger{}
	config := &OffsetCoordinatorConfig{
		MaxStreams:           100,
		GapDetectionInterval: 50 * time.Millisecond,
		MaxGapAge:            1 * time.Second,
		CleanupInterval:      500 * time.Millisecond,
		EnableDetailedStats:  true,
		Logger:               logger,
	}

	oc := NewOffsetCoordinator(config)

	// Test start
	err := oc.Start()
	if err != nil {
		t.Fatalf("Failed to start offset coordinator: %v", err)
	}

	// Test double start
	err = oc.Start()
	if err == nil {
		t.Error("Expected error when starting already running coordinator")
	}

	// Test stop
	err = oc.Stop()
	if err != nil {
		t.Fatalf("Failed to stop offset coordinator: %v", err)
	}

	// Test double stop
	err = oc.Stop()
	if err != nil {
		t.Error("Stop should be idempotent")
	}
}

func TestOffsetCoordinator_ReserveOffsetRange(t *testing.T) {
	oc := NewOffsetCoordinator(DefaultOffsetCoordinatorConfig())
	err := oc.Start()
	if err != nil {
		t.Fatalf("Failed to start offset coordinator: %v", err)
	}
	defer oc.Stop()

	streamID := uint64(1)

	// Reserve first range
	offset1, err := oc.ReserveOffsetRange(streamID, 100)
	if err != nil {
		t.Fatalf("Failed to reserve first range: %v", err)
	}
	if offset1 != 0 {
		t.Errorf("Expected first offset to be 0, got %d", offset1)
	}

	// Reserve second range
	offset2, err := oc.ReserveOffsetRange(streamID, 50)
	if err != nil {
		t.Fatalf("Failed to reserve second range: %v", err)
	}
	if offset2 != 100 {
		t.Errorf("Expected second offset to be 100, got %d", offset2)
	}

	// Reserve third range
	offset3, err := oc.ReserveOffsetRange(streamID, 75)
	if err != nil {
		t.Fatalf("Failed to reserve third range: %v", err)
	}
	if offset3 != 150 {
		t.Errorf("Expected third offset to be 150, got %d", offset3)
	}

	// Test invalid size
	_, err = oc.ReserveOffsetRange(streamID, 0)
	if err == nil {
		t.Error("Expected error for zero size reservation")
	}

	_, err = oc.ReserveOffsetRange(streamID, -10)
	if err == nil {
		t.Error("Expected error for negative size reservation")
	}
}

func TestOffsetCoordinator_CommitOffsetRange(t *testing.T) {
	oc := NewOffsetCoordinator(DefaultOffsetCoordinatorConfig())
	err := oc.Start()
	if err != nil {
		t.Fatalf("Failed to start offset coordinator: %v", err)
	}
	defer oc.Stop()

	streamID := uint64(1)

	// Reserve a range
	offset, err := oc.ReserveOffsetRange(streamID, 100)
	if err != nil {
		t.Fatalf("Failed to reserve range: %v", err)
	}

	// Commit the range with actual size
	err = oc.CommitOffsetRange(streamID, offset, 80)
	if err != nil {
		t.Fatalf("Failed to commit range: %v", err)
	}

	// Try to commit non-existent reservation
	err = oc.CommitOffsetRange(streamID, 1000, 50)
	if err == nil {
		t.Error("Expected error when committing non-existent reservation")
	}

	// Try to commit for non-existent stream
	err = oc.CommitOffsetRange(999, 0, 50)
	if err == nil {
		t.Error("Expected error when committing for non-existent stream")
	}
}

func TestOffsetCoordinator_RegisterDataReceived(t *testing.T) {
	oc := NewOffsetCoordinator(DefaultOffsetCoordinatorConfig())
	err := oc.Start()
	if err != nil {
		t.Fatalf("Failed to start offset coordinator: %v", err)
	}
	defer oc.Stop()

	streamID := uint64(1)

	// Register data received at offset 0
	err = oc.RegisterDataReceived(streamID, 0, 50)
	if err != nil {
		t.Fatalf("Failed to register data received: %v", err)
	}

	// Register data received at offset 50 (contiguous)
	err = oc.RegisterDataReceived(streamID, 50, 30)
	if err != nil {
		t.Fatalf("Failed to register contiguous data: %v", err)
	}

	// Register data received at offset 100 (gap)
	err = oc.RegisterDataReceived(streamID, 100, 25)
	if err != nil {
		t.Fatalf("Failed to register data with gap: %v", err)
	}

	// Check stream state
	state, err := oc.GetStreamState(streamID)
	if err != nil {
		t.Fatalf("Failed to get stream state: %v", err)
	}

	if state.CommittedOffset != 80 {
		t.Errorf("Expected committed offset 80, got %d", state.CommittedOffset)
	}

	if len(state.ReceivedRanges) != 2 {
		t.Errorf("Expected 2 received ranges, got %d", len(state.ReceivedRanges))
	}
}

func TestOffsetCoordinator_GapDetection(t *testing.T) {
	oc := NewOffsetCoordinator(DefaultOffsetCoordinatorConfig())
	err := oc.Start()
	if err != nil {
		t.Fatalf("Failed to start offset coordinator: %v", err)
	}
	defer oc.Stop()

	streamID := uint64(1)

	// Register data with gaps
	oc.RegisterDataReceived(streamID, 0, 50)   // 0-50
	oc.RegisterDataReceived(streamID, 100, 30) // 100-130 (gap: 50-100)
	oc.RegisterDataReceived(streamID, 200, 25) // 200-225 (gap: 130-200)

	// Validate continuity and detect gaps
	gaps, err := oc.ValidateOffsetContinuity(streamID)
	if err != nil {
		t.Fatalf("Failed to validate offset continuity: %v", err)
	}

	if len(gaps) != 2 {
		t.Errorf("Expected 2 gaps, got %d", len(gaps))
	}

	// Check first gap
	if gaps[0].Start != 50 || gaps[0].End != 100 {
		t.Errorf("Expected first gap 50-100, got %d-%d", gaps[0].Start, gaps[0].End)
	}

	// Check second gap
	if gaps[1].Start != 130 || gaps[1].End != 200 {
		t.Errorf("Expected second gap 130-200, got %d-%d", gaps[1].Start, gaps[1].End)
	}
}

func TestOffsetCoordinator_MissingDataCallback(t *testing.T) {
	oc := NewOffsetCoordinator(DefaultOffsetCoordinatorConfig())

	// Set up callback
	var callbackCalled bool
	var callbackStreamID uint64
	var callbackGaps []OffsetGap

	oc.SetMissingDataCallback(func(streamID uint64, gaps []OffsetGap) error {
		callbackCalled = true
		callbackStreamID = streamID
		callbackGaps = gaps
		return nil
	})

	err := oc.Start()
	if err != nil {
		t.Fatalf("Failed to start offset coordinator: %v", err)
	}
	defer oc.Stop()

	streamID := uint64(1)

	// Create gaps
	gaps := []OffsetGap{
		{Start: 50, End: 100, Size: 50},
		{Start: 150, End: 200, Size: 50},
	}

	// Request missing data
	err = oc.RequestMissingData(streamID, gaps)
	if err != nil {
		t.Fatalf("Failed to request missing data: %v", err)
	}

	// Check callback was called
	if !callbackCalled {
		t.Error("Expected callback to be called")
	}
	if callbackStreamID != streamID {
		t.Errorf("Expected callback stream ID %d, got %d", streamID, callbackStreamID)
	}
	if len(callbackGaps) != 2 {
		t.Errorf("Expected 2 gaps in callback, got %d", len(callbackGaps))
	}
}

func TestOffsetCoordinator_GetNextExpectedOffset(t *testing.T) {
	oc := NewOffsetCoordinator(DefaultOffsetCoordinatorConfig())
	err := oc.Start()
	if err != nil {
		t.Fatalf("Failed to start offset coordinator: %v", err)
	}
	defer oc.Stop()

	streamID := uint64(1)

	// Initially should be 0
	offset := oc.GetNextExpectedOffset(streamID)
	if offset != 0 {
		t.Errorf("Expected initial offset 0, got %d", offset)
	}

	// Reserve some ranges
	oc.ReserveOffsetRange(streamID, 100)
	offset = oc.GetNextExpectedOffset(streamID)
	if offset != 100 {
		t.Errorf("Expected offset 100 after reservation, got %d", offset)
	}

	oc.ReserveOffsetRange(streamID, 50)
	offset = oc.GetNextExpectedOffset(streamID)
	if offset != 150 {
		t.Errorf("Expected offset 150 after second reservation, got %d", offset)
	}

	// Non-existent stream should return 0
	offset = oc.GetNextExpectedOffset(999)
	if offset != 0 {
		t.Errorf("Expected offset 0 for non-existent stream, got %d", offset)
	}
}

func TestOffsetCoordinator_CloseStream(t *testing.T) {
	oc := NewOffsetCoordinator(DefaultOffsetCoordinatorConfig())
	err := oc.Start()
	if err != nil {
		t.Fatalf("Failed to start offset coordinator: %v", err)
	}
	defer oc.Stop()

	streamID := uint64(1)

	// Create some state for the stream
	oc.ReserveOffsetRange(streamID, 100)
	oc.RegisterDataReceived(streamID, 0, 50)

	// Verify stream exists
	state, err := oc.GetStreamState(streamID)
	if err != nil {
		t.Fatalf("Failed to get stream state: %v", err)
	}
	if state == nil {
		t.Error("Expected stream state to exist")
	}

	// Close the stream
	err = oc.CloseStream(streamID)
	if err != nil {
		t.Fatalf("Failed to close stream: %v", err)
	}

	// Verify stream no longer exists
	state, err = oc.GetStreamState(streamID)
	if err == nil {
		t.Error("Expected error when getting state for closed stream")
	}
}

func TestOffsetCoordinator_Statistics(t *testing.T) {
	oc := NewOffsetCoordinator(DefaultOffsetCoordinatorConfig())
	err := oc.Start()
	if err != nil {
		t.Fatalf("Failed to start offset coordinator: %v", err)
	}
	defer oc.Stop()

	// Initial stats
	stats := oc.GetStats()
	if stats.ActiveStreams != 0 {
		t.Errorf("Expected 0 active streams initially, got %d", stats.ActiveStreams)
	}

	// Create some activity
	streamID1 := uint64(1)
	streamID2 := uint64(2)

	oc.ReserveOffsetRange(streamID1, 100)
	oc.ReserveOffsetRange(streamID2, 50)
	oc.CommitOffsetRange(streamID1, 0, 80)

	// Check updated stats
	stats = oc.GetStats()
	if stats.ActiveStreams != 2 {
		t.Errorf("Expected 2 active streams, got %d", stats.ActiveStreams)
	}
	if stats.TotalRangesReserved != 2 {
		t.Errorf("Expected 2 ranges reserved, got %d", stats.TotalRangesReserved)
	}
	if stats.TotalRangesCommitted != 1 {
		t.Errorf("Expected 1 range committed, got %d", stats.TotalRangesCommitted)
	}
}

func TestOffsetCoordinator_ConcurrentAccess(t *testing.T) {
	oc := NewOffsetCoordinator(DefaultOffsetCoordinatorConfig())
	err := oc.Start()
	if err != nil {
		t.Fatalf("Failed to start offset coordinator: %v", err)
	}
	defer oc.Stop()

	numGoroutines := 10
	numOperationsPerGoroutine := 100
	var wg sync.WaitGroup

	// Concurrent reservations and commits
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()

			streamID := uint64(goroutineID + 1)

			for j := 0; j < numOperationsPerGoroutine; j++ {
				// Reserve range
				offset, err := oc.ReserveOffsetRange(streamID, 10)
				if err != nil {
					t.Errorf("Goroutine %d: Failed to reserve range: %v", goroutineID, err)
					continue
				}

				// Commit range
				err = oc.CommitOffsetRange(streamID, offset, 8)
				if err != nil {
					t.Errorf("Goroutine %d: Failed to commit range: %v", goroutineID, err)
				}

				// Register data received
				err = oc.RegisterDataReceived(streamID, offset, 8)
				if err != nil {
					t.Errorf("Goroutine %d: Failed to register data: %v", goroutineID, err)
				}
			}
		}(i)
	}

	wg.Wait()

	// Check final stats
	stats := oc.GetStats()
	expectedReservations := uint64(numGoroutines * numOperationsPerGoroutine)
	if stats.TotalRangesReserved != expectedReservations {
		t.Errorf("Expected %d reservations, got %d", expectedReservations, stats.TotalRangesReserved)
	}
	if stats.TotalRangesCommitted != expectedReservations {
		t.Errorf("Expected %d commits, got %d", expectedReservations, stats.TotalRangesCommitted)
	}
}

func TestOffsetCoordinator_RangeMerging(t *testing.T) {
	oc := NewOffsetCoordinator(DefaultOffsetCoordinatorConfig())
	err := oc.Start()
	if err != nil {
		t.Fatalf("Failed to start offset coordinator: %v", err)
	}
	defer oc.Stop()

	streamID := uint64(1)

	// Register overlapping/adjacent ranges
	oc.RegisterDataReceived(streamID, 0, 50)   // 0-50
	oc.RegisterDataReceived(streamID, 45, 20)  // 45-65 (overlaps with first)
	oc.RegisterDataReceived(streamID, 65, 10)  // 65-75 (adjacent to merged range)
	oc.RegisterDataReceived(streamID, 100, 25) // 100-125 (separate range)

	state, err := oc.GetStreamState(streamID)
	if err != nil {
		t.Fatalf("Failed to get stream state: %v", err)
	}

	// Should have merged into 2 ranges: 0-75 and 100-125
	if len(state.ReceivedRanges) != 2 {
		t.Errorf("Expected 2 merged ranges, got %d", len(state.ReceivedRanges))
	}

	// Check first merged range
	if state.ReceivedRanges[0].Start != 0 || state.ReceivedRanges[0].End != 75 {
		t.Errorf("Expected first range 0-75, got %d-%d",
			state.ReceivedRanges[0].Start, state.ReceivedRanges[0].End)
	}

	// Check second range
	if state.ReceivedRanges[1].Start != 100 || state.ReceivedRanges[1].End != 125 {
		t.Errorf("Expected second range 100-125, got %d-%d",
			state.ReceivedRanges[1].Start, state.ReceivedRanges[1].End)
	}
}

func TestOffsetCoordinator_NotRunning(t *testing.T) {
	oc := NewOffsetCoordinator(DefaultOffsetCoordinatorConfig())

	// Try operations when not running
	_, err := oc.ReserveOffsetRange(1, 100)
	if err == nil {
		t.Error("Expected error when reserving range on stopped coordinator")
	}

	err = oc.CommitOffsetRange(1, 0, 50)
	if err == nil {
		t.Error("Expected error when committing range on stopped coordinator")
	}

	err = oc.RegisterDataReceived(1, 0, 50)
	if err == nil {
		t.Error("Expected error when registering data on stopped coordinator")
	}
}

func BenchmarkOffsetCoordinator_ReserveOffsetRange(b *testing.B) {
	oc := NewOffsetCoordinator(DefaultOffsetCoordinatorConfig())
	oc.Start()
	defer oc.Stop()

	streamID := uint64(1)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		oc.ReserveOffsetRange(streamID, 100)
	}
}

func BenchmarkOffsetCoordinator_RegisterDataReceived(b *testing.B) {
	oc := NewOffsetCoordinator(DefaultOffsetCoordinatorConfig())
	oc.Start()
	defer oc.Stop()

	streamID := uint64(1)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		oc.RegisterDataReceived(streamID, int64(i*100), 50)
	}
}
