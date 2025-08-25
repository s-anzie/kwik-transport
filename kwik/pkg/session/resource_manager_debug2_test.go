package session

import (
	"fmt"
	"testing"
)

func TestResourceManager_ConcurrentDebug(t *testing.T) {
	rm := NewResourceManager(DefaultResourceConfig())
	err := rm.Start()
	if err != nil {
		t.Fatalf("Failed to start resource manager: %v", err)
	}
	defer rm.Stop()

	sessionID := "test-session"
	numStreams := 10

	fmt.Printf("Initial status: %+v\n", rm.CheckResourceAvailability(sessionID))

	// Allocate multiple streams sequentially for debugging
	for i := 0; i < numStreams; i++ {
		streamID := uint64(i + 1)
		fmt.Printf("Allocating stream %d...\n", streamID)
		
		_, err := rm.AllocateStreamResources(sessionID, streamID)
		if err != nil {
			t.Fatalf("Failed to allocate stream %d: %v", streamID, err)
		}
		
		status := rm.CheckResourceAvailability(sessionID)
		fmt.Printf("After allocating stream %d: %+v\n", streamID, status)
	}

	// Check final status
	finalStatus := rm.CheckResourceAvailability(sessionID)
	fmt.Printf("Final status: %+v\n", finalStatus)
	
	if finalStatus.AvailableStreams != 90 {
		t.Errorf("Expected 90 available streams, got %d", finalStatus.AvailableStreams)
	}
}