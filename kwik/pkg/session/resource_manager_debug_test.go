package session

import (
	"fmt"
	"testing"
)

func TestResourceManager_Debug(t *testing.T) {
	fmt.Println("Creating resource manager...")
	config := DefaultResourceConfig()
	config.CleanupInterval = 0 // Disable cleanup to avoid goroutines
	
	rm := NewResourceManager(config)
	fmt.Println("Resource manager created")
	
	fmt.Println("Starting resource manager...")
	err := rm.Start()
	if err != nil {
		t.Fatalf("Failed to start resource manager: %v", err)
	}
	fmt.Println("Resource manager started")
	
	defer func() {
		fmt.Println("Stopping resource manager...")
		rm.Stop()
		fmt.Println("Resource manager stopped")
	}()

	sessionID := "test-session"
	streamID := uint64(1)

	fmt.Println("Allocating stream resources...")
	resources, err := rm.AllocateStreamResources(sessionID, streamID)
	if err != nil {
		t.Fatalf("Failed to allocate resources: %v", err)
	}
	fmt.Printf("Resources allocated: %+v\n", resources)

	fmt.Println("Checking resource availability...")
	status := rm.CheckResourceAvailability(sessionID)
	fmt.Printf("Resource status: %+v\n", status)

	fmt.Println("Releasing stream resources...")
	err = rm.ReleaseStreamResources(sessionID, streamID)
	if err != nil {
		t.Fatalf("Failed to release resources: %v", err)
	}
	fmt.Println("Resources released")

	fmt.Println("Test completed successfully")
}