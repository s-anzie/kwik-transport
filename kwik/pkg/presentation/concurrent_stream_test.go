package presentation

import (
	"sync"
	"testing"
	"time"
)

func TestConcurrentStreamCreation(t *testing.T) {
	config := DefaultPresentationConfig()
	config.ReceiveWindowSize = 100 * 1024 * 1024 // 100MB window
	config.DefaultStreamBufferSize = 1024 * 1024  // 1MB per stream
	
	dpm := NewDataPresentationManager(config)
	defer dpm.Shutdown()
	
	err := dpm.Start()
	if err != nil {
		t.Fatalf("Failed to start DPM: %v", err)
	}
	
	// Test concurrent creation of multiple streams
	numStreams := 50
	numWorkers := 10
	
	var wg sync.WaitGroup
	errors := make(chan error, numStreams)
	streamIDs := make(chan uint64, numStreams)
	
	// Generate stream IDs
	go func() {
		for i := 1; i <= numStreams; i++ {
			streamIDs <- uint64(i)
		}
		close(streamIDs)
	}()
	
	// Start workers
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			
			for streamID := range streamIDs {
				metadata := &StreamMetadata{
					StreamID:  streamID,
					Priority:  StreamPriorityNormal,
					CreatedAt: time.Now(),
				}
				
				err := dpm.CreateStreamBuffer(streamID, metadata)
				if err != nil {
					errors <- err
				} else {
					errors <- nil
				}
			}
		}(i)
	}
	
	wg.Wait()
	close(errors)
	
	// Check results
	successCount := 0
	errorCount := 0
	
	for err := range errors {
		if err != nil {
			errorCount++
			t.Logf("Stream creation error: %v", err)
		} else {
			successCount++
		}
	}
	
	t.Logf("Concurrent stream creation results: %d success, %d errors", successCount, errorCount)
	
	// We should have created at least some streams successfully
	if successCount == 0 {
		t.Error("No streams were created successfully")
	}
	
	// Check that we don't have more streams than expected
	stats := dpm.GetGlobalStats()
	if stats.ActiveStreams > numStreams {
		t.Errorf("More active streams (%d) than expected (%d)", stats.ActiveStreams, numStreams)
	}
}

func TestResourceReservationSystem(t *testing.T) {
	rrm := NewResourceReservationManager(1*time.Second, 10)
	defer rrm.Shutdown()
	
	// Test basic reservation
	reservation, err := rrm.ReserveResources(1, 1024, 1024, StreamPriorityNormal)
	if err != nil {
		t.Fatalf("Failed to reserve resources: %v", err)
	}
	
	if reservation.StreamID != 1 {
		t.Errorf("Expected stream ID 1, got %d", reservation.StreamID)
	}
	if reservation.WindowSize != 1024 {
		t.Errorf("Expected window size 1024, got %d", reservation.WindowSize)
	}
	
	// Check reserved resources
	windowSpace, streams := rrm.GetReservedResources()
	if windowSpace != 1024 {
		t.Errorf("Expected reserved window space 1024, got %d", windowSpace)
	}
	if streams != 1 {
		t.Errorf("Expected 1 reserved stream, got %d", streams)
	}
	
	// Test reservation commit
	err = rrm.CommitReservation(reservation.ReservationID)
	if err != nil {
		t.Fatalf("Failed to commit reservation: %v", err)
	}
	
	// Check that resources are no longer reserved
	windowSpace, streams = rrm.GetReservedResources()
	if windowSpace != 0 {
		t.Errorf("Expected no reserved window space after commit, got %d", windowSpace)
	}
	if streams != 0 {
		t.Errorf("Expected no reserved streams after commit, got %d", streams)
	}
}

func TestResourceReservationExpiration(t *testing.T) {
	rrm := NewResourceReservationManager(100*time.Millisecond, 10)
	defer rrm.Shutdown()
	
	// Create a reservation
	reservation, err := rrm.ReserveResources(1, 1024, 1024, StreamPriorityNormal)
	if err != nil {
		t.Fatalf("Failed to reserve resources: %v", err)
	}
	
	// Wait for expiration
	time.Sleep(200 * time.Millisecond)
	
	// Try to commit expired reservation
	err = rrm.CommitReservation(reservation.ReservationID)
	if err == nil {
		t.Error("Expected error when committing expired reservation")
	}
	
	// Check that resources are cleaned up
	windowSpace, streams := rrm.GetReservedResources()
	if windowSpace != 0 {
		t.Errorf("Expected no reserved window space after expiration, got %d", windowSpace)
	}
	if streams != 0 {
		t.Errorf("Expected no reserved streams after expiration, got %d", streams)
	}
}

func TestAtomicWindowAllocation(t *testing.T) {
	config := &WindowManagerConfig{
		InitialSize:           1024 * 1024, // 1MB
		MaxSize:               4 * 1024 * 1024, // 4MB
		MinSize:               256 * 1024, // 256KB
		BackpressureThreshold: 0.8,
		MaxStreams:            100,
		EnableDynamicSizing:   false,
	}
	
	rwm := NewReceiveWindowManager(config)
	defer rwm.Shutdown()
	
	// Test concurrent allocations
	numWorkers := 20
	allocSize := uint64(50 * 1024) // 50KB per allocation
	
	var wg sync.WaitGroup
	errors := make(chan error, numWorkers)
	
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(streamID int) {
			defer wg.Done()
			
			err := rwm.AllocateWindowAtomic(uint64(streamID), allocSize)
			errors <- err
		}(i + 1)
	}
	
	wg.Wait()
	close(errors)
	
	// Check results
	successCount := 0
	errorCount := 0
	
	for err := range errors {
		if err != nil {
			errorCount++
		} else {
			successCount++
		}
	}
	
	t.Logf("Atomic allocation results: %d success, %d errors", successCount, errorCount)
	
	// Verify total allocated space doesn't exceed window size
	status := rwm.GetWindowStatus()
	if status.UsedSize > status.TotalSize {
		t.Errorf("Used size (%d) exceeds total size (%d)", status.UsedSize, status.TotalSize)
	}
	
	// Verify number of allocated streams matches successful allocations
	if len(status.StreamAllocations) != successCount {
		t.Errorf("Expected %d allocated streams, got %d", successCount, len(status.StreamAllocations))
	}
}

func TestResourceAvailabilityCheck(t *testing.T) {
	config := &WindowManagerConfig{
		InitialSize:           1024 * 1024, // 1MB
		MaxSize:               4 * 1024 * 1024, // 4MB
		MinSize:               256 * 1024, // 256KB
		BackpressureThreshold: 0.8,
		MaxStreams:            10,
		EnableDynamicSizing:   false,
	}
	
	rwm := NewReceiveWindowManager(config)
	defer rwm.Shutdown()
	
	// Test availability check without allocation
	err := rwm.CheckResourceAvailability(1, 512*1024) // 512KB
	if err != nil {
		t.Errorf("Expected resources to be available: %v", err)
	}
	
	// Allocate most of the window
	err = rwm.AllocateWindowAtomic(1, 900*1024) // 900KB
	if err != nil {
		t.Fatalf("Failed to allocate window space: %v", err)
	}
	
	// Check availability for large allocation
	err = rwm.CheckResourceAvailability(2, 200*1024) // 200KB (should fail)
	if err == nil {
		t.Error("Expected insufficient space error")
	}
	
	// Check availability for small allocation
	err = rwm.CheckResourceAvailability(2, 100*1024) // 100KB (should succeed)
	if err != nil {
		t.Errorf("Expected small allocation to be available: %v", err)
	}
}

func TestConcurrentStreamRemoval(t *testing.T) {
	config := DefaultPresentationConfig()
	config.ReceiveWindowSize = 5 * 1024 * 1024 // 5MB window
	config.DefaultStreamBufferSize = 512 * 1024 // 512KB per stream
	
	dpm := NewDataPresentationManager(config)
	defer dpm.Shutdown()
	
	err := dpm.Start()
	if err != nil {
		t.Fatalf("Failed to start DPM: %v", err)
	}
	
	// Create some streams first
	numStreams := 10
	for i := 1; i <= numStreams; i++ {
		metadata := &StreamMetadata{
			StreamID:  uint64(i),
			Priority:  StreamPriorityNormal,
			CreatedAt: time.Now(),
		}
		
		err := dpm.CreateStreamBuffer(uint64(i), metadata)
		if err != nil {
			t.Fatalf("Failed to create stream %d: %v", i, err)
		}
	}
	
	// Concurrently remove streams
	var wg sync.WaitGroup
	errors := make(chan error, numStreams)
	
	for i := 1; i <= numStreams; i++ {
		wg.Add(1)
		go func(streamID int) {
			defer wg.Done()
			
			err := dpm.RemoveStreamBuffer(uint64(streamID))
			errors <- err
		}(i)
	}
	
	wg.Wait()
	close(errors)
	
	// Check results
	errorCount := 0
	for err := range errors {
		if err != nil {
			errorCount++
			t.Logf("Stream removal error: %v", err)
		}
	}
	
	if errorCount > 0 {
		t.Errorf("Expected no errors during stream removal, got %d", errorCount)
	}
	
	// Verify all streams are removed
	stats := dpm.GetGlobalStats()
	if stats.ActiveStreams != 0 {
		t.Errorf("Expected 0 active streams after removal, got %d", stats.ActiveStreams)
	}
}

func BenchmarkConcurrentStreamCreation(b *testing.B) {
	config := DefaultPresentationConfig()
	config.ReceiveWindowSize = 100 * 1024 * 1024 // 100MB window
	config.DefaultStreamBufferSize = 1024 * 1024  // 1MB per stream
	
	dpm := NewDataPresentationManager(config)
	defer dpm.Shutdown()
	
	dpm.Start()
	
	b.ResetTimer()
	
	b.RunParallel(func(pb *testing.PB) {
		streamID := uint64(1)
		for pb.Next() {
			metadata := &StreamMetadata{
				StreamID:  streamID,
				Priority:  StreamPriorityNormal,
				CreatedAt: time.Now(),
			}
			
			// Create and immediately remove to avoid running out of space
			dpm.CreateStreamBuffer(streamID, metadata)
			dpm.RemoveStreamBuffer(streamID)
			
			streamID++
		}
	})
}

func BenchmarkAtomicWindowAllocation(b *testing.B) {
	config := &WindowManagerConfig{
		InitialSize:           100 * 1024 * 1024, // 100MB
		MaxSize:               200 * 1024 * 1024, // 200MB
		MinSize:               50 * 1024 * 1024,  // 50MB
		BackpressureThreshold: 0.8,
		MaxStreams:            10000,
		EnableDynamicSizing:   false,
	}
	
	rwm := NewReceiveWindowManager(config)
	defer rwm.Shutdown()
	
	b.ResetTimer()
	
	b.RunParallel(func(pb *testing.PB) {
		streamID := uint64(1)
		for pb.Next() {
			// Allocate and immediately release to avoid running out of space
			rwm.AllocateWindowAtomic(streamID, 1024)
			rwm.ReleaseWindow(streamID, 1024)
			streamID++
		}
	})
}