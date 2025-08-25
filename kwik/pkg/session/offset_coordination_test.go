package session

import (
	"testing"
	"time"

	"kwik/pkg/data"
	"kwik/pkg/logger"
	"kwik/pkg/transport"
)

// TestOffsetCoordinationMultiPath tests offset coordination across multiple paths
func TestOffsetCoordinationMultiPath(t *testing.T) {
	// Create path manager with multiple paths
	pathManager := transport.NewPathManager()

	// Create session config
	config := DefaultSessionConfig()
	config.EnableAggregation = true
	config.EnableMigration = true

	// Create client session
	clientSession := NewClientSession(pathManager, config)

	// Test offset coordinator is initialized
	if clientSession.GetOffsetCoordinator() == nil {
		t.Fatal("OffsetCoordinator should be initialized")
	}

	// Start the offset coordinator
	err := clientSession.startOffsetCoordinator()
	if err != nil {
		t.Fatalf("Failed to start offset coordinator: %v", err)
	}
	defer clientSession.stopOffsetCoordinator()

	// Test offset range reservation
	streamID := uint64(1)
	dataSize := 1024

	startOffset, err := clientSession.reserveOffsetRange(streamID, dataSize)
	if err != nil {
		t.Fatalf("Failed to reserve offset range: %v", err)
	}

	if startOffset < 0 {
		t.Fatalf("Invalid start offset: %d", startOffset)
	}

	// Test offset range commit
	err = clientSession.commitOffsetRange(streamID, startOffset, dataSize)
	if err != nil {
		t.Fatalf("Failed to commit offset range: %v", err)
	}

	// Test gap detection
	gaps, err := clientSession.validateOffsetContinuity(streamID)
	if err != nil {
		t.Fatalf("Failed to validate offset continuity: %v", err)
	}

	// Should have no gaps for contiguous data
	if len(gaps) != 0 {
		t.Fatalf("Expected no gaps, got %d", len(gaps))
	}

	// Test data registration
	err = clientSession.registerDataReceived(streamID, startOffset, dataSize)
	if err != nil {
		t.Fatalf("Failed to register data received: %v", err)
	}

	// Test next expected offset
	nextOffset := clientSession.getNextExpectedOffset(streamID)
	expectedNext := startOffset + int64(dataSize)
	if nextOffset != expectedNext {
		t.Fatalf("Expected next offset %d, got %d", expectedNext, nextOffset)
	}

	// Test statistics
	stats := clientSession.getOffsetCoordinatorStats()
	if stats.TotalRangesReserved == 0 {
		t.Fatal("Expected non-zero ranges reserved")
	}
	if stats.TotalRangesCommitted == 0 {
		t.Fatal("Expected non-zero ranges committed")
	}

	// Clean up
	clientSession.Close()
}

// TestOffsetCoordinationGapDetection tests gap detection and missing data requests
func TestOffsetCoordinationGapDetection(t *testing.T) {
	// Create path manager
	pathManager := transport.NewPathManager()

	// Create session config
	config := DefaultSessionConfig()
	config.EnableAggregation = true

	// Create client session
	clientSession := NewClientSession(pathManager, config)

	// Start the offset coordinator
	err := clientSession.startOffsetCoordinator()
	if err != nil {
		t.Fatalf("Failed to start offset coordinator: %v", err)
	}
	defer clientSession.stopOffsetCoordinator()

	streamID := uint64(2)

	// Register data with gaps (simulate out-of-order arrival)
	// Register data at offset 0-100
	err = clientSession.registerDataReceived(streamID, 0, 100)
	if err != nil {
		t.Fatalf("Failed to register data: %v", err)
	}

	// Register data at offset 200-300 (creating a gap from 100-200)
	err = clientSession.registerDataReceived(streamID, 200, 100)
	if err != nil {
		t.Fatalf("Failed to register data: %v", err)
	}

	// Validate offset continuity should detect the gap
	gaps, err := clientSession.validateOffsetContinuity(streamID)
	if err != nil {
		t.Fatalf("Failed to validate offset continuity: %v", err)
	}

	// Should detect one gap from 100-200
	if len(gaps) != 1 {
		t.Fatalf("Expected 1 gap, got %d", len(gaps))
	}

	gap := gaps[0]
	if gap.Start != 100 || gap.End != 200 || gap.Size != 100 {
		t.Fatalf("Expected gap [100-200, size 100], got [%d-%d, size %d]",
			gap.Start, gap.End, gap.Size)
	}

	// Fill the gap
	err = clientSession.registerDataReceived(streamID, 100, 100)
	if err != nil {
		t.Fatalf("Failed to register gap data: %v", err)
	}

	// Validate continuity again - should have no gaps
	gaps, err = clientSession.validateOffsetContinuity(streamID)
	if err != nil {
		t.Fatalf("Failed to validate offset continuity: %v", err)
	}

	if len(gaps) != 0 {
		t.Fatalf("Expected no gaps after filling, got %d", len(gaps))
	}

	// Clean up
	clientSession.Close()
}

// TestOffsetCoordinationServerSession tests offset coordination in server sessions
func TestOffsetCoordinationServerSession(t *testing.T) {
	// Create path manager
	pathManager := transport.NewPathManager()

	// Create server session
	serverSession := NewServerSession("test-server-session", pathManager, nil, &logger.MockLogger{})

	// Test offset coordinator is initialized
	if serverSession.GetOffsetCoordinator() == nil {
		t.Fatal("Server OffsetCoordinator should be initialized")
	}

	// Start the offset coordinator
	err := serverSession.startOffsetCoordinator()
	if err != nil {
		t.Fatalf("Failed to start offset coordinator: %v", err)
	}
	defer serverSession.stopOffsetCoordinator()

	// Test offset range operations
	streamID := uint64(3)
	dataSize := 512

	startOffset, err := serverSession.reserveOffsetRange(streamID, dataSize)
	if err != nil {
		t.Fatalf("Failed to reserve offset range: %v", err)
	}

	err = serverSession.commitOffsetRange(streamID, startOffset, dataSize)
	if err != nil {
		t.Fatalf("Failed to commit offset range: %v", err)
	}

	// Test gap detection
	gaps, err := serverSession.validateOffsetContinuity(streamID)
	if err != nil {
		t.Fatalf("Failed to validate offset continuity: %v", err)
	}

	if len(gaps) != 0 {
		t.Fatalf("Expected no gaps, got %d", len(gaps))
	}

	// Clean up
	serverSession.Close()
}

// TestDataAggregatorOffsetCoordination tests offset coordinator integration with data aggregator
func TestDataAggregatorOffsetCoordination(t *testing.T) {
	// Create data aggregator
	aggregator := data.NewDataAggregator(&logger.MockLogger{})

	// Create offset coordinator
	config := data.DefaultOffsetCoordinatorConfig()
	config.Logger = &logger.MockLogger{}
	offsetCoordinator := data.NewOffsetCoordinator(config)

	// Start offset coordinator
	err := offsetCoordinator.Start()
	if err != nil {
		t.Fatalf("Failed to start offset coordinator: %v", err)
	}
	defer offsetCoordinator.Stop()

	// Set offset coordinator in aggregator
	aggregator.SetOffsetCoordinator(offsetCoordinator)

	// Test offset continuity validation
	streamID := uint64(4)

	// First, register some data to create the stream
	startOffset, err := offsetCoordinator.ReserveOffsetRange(streamID, 100)
	if err != nil {
		t.Fatalf("Failed to reserve offset range: %v", err)
	}

	err = offsetCoordinator.CommitOffsetRange(streamID, startOffset, 100)
	if err != nil {
		t.Fatalf("Failed to commit offset range: %v", err)
	}

	// Now validate offset continuity
	gaps, err := aggregator.ValidateOffsetContinuity(streamID)
	if err != nil {
		t.Fatalf("Failed to validate offset continuity: %v", err)
	}

	// Should have no gaps for contiguous data
	if len(gaps) != 0 {
		t.Fatalf("Expected no gaps for contiguous data, got %d", len(gaps))
	}
}

// TestOffsetCoordinationConcurrency tests offset coordination under concurrent access
func TestOffsetCoordinationConcurrency(t *testing.T) {
	// Create path manager
	pathManager := transport.NewPathManager()

	// Create session config
	config := DefaultSessionConfig()
	config.EnableAggregation = true

	// Create client session
	clientSession := NewClientSession(pathManager, config)
	defer clientSession.Close()

	// Start the offset coordinator
	err := clientSession.startOffsetCoordinator()
	if err != nil {
		t.Fatalf("Failed to start offset coordinator: %v", err)
	}
	defer clientSession.stopOffsetCoordinator()

	streamID := uint64(5)
	numGoroutines := 10
	dataSize := 100

	// Channel to collect results
	results := make(chan error, numGoroutines)

	// Start multiple goroutines doing offset operations
	for i := 0; i < numGoroutines; i++ {
		go func(goroutineID int) {
			// Reserve offset range
			startOffset, err := clientSession.reserveOffsetRange(streamID, dataSize)
			if err != nil {
				results <- err
				return
			}

			// Simulate some work
			time.Sleep(10 * time.Millisecond)

			// Commit offset range
			err = clientSession.commitOffsetRange(streamID, startOffset, dataSize)
			if err != nil {
				results <- err
				return
			}

			// Register data received
			err = clientSession.registerDataReceived(streamID, startOffset, dataSize)
			results <- err
		}(i)
	}

	// Collect results
	for i := 0; i < numGoroutines; i++ {
		err := <-results
		if err != nil {
			t.Fatalf("Goroutine %d failed: %v", i, err)
		}
	}

	// Validate final state
	stats := clientSession.getOffsetCoordinatorStats()
	if stats.TotalRangesReserved != uint64(numGoroutines) {
		t.Fatalf("Expected %d ranges reserved, got %d", numGoroutines, stats.TotalRangesReserved)
	}
	if stats.TotalRangesCommitted != uint64(numGoroutines) {
		t.Fatalf("Expected %d ranges committed, got %d", numGoroutines, stats.TotalRangesCommitted)
	}
}
