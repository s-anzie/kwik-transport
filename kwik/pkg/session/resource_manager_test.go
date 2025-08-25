package session

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResourceManager_BasicAllocation(t *testing.T) {
	rm := NewResourceManager(DefaultResourceConfig())
	err := rm.Start()
	require.NoError(t, err)
	defer rm.Stop()

	// Test basic allocation
	sessionID := "test-session"
	streamID := uint64(1)

	resources, err := rm.AllocateStreamResources(sessionID, streamID)
	assert.NoError(t, err)
	assert.NotNil(t, resources)
	assert.Greater(t, resources.BufferSize, 0)

	// Test resource status
	status := rm.CheckResourceAvailability(sessionID)
	assert.Equal(t, 99, status.AvailableStreams) // 100 - 1 allocated

	// Test release
	err = rm.ReleaseStreamResources(sessionID, streamID)
	assert.NoError(t, err)

	// Check status after release
	status = rm.CheckResourceAvailability(sessionID)
	assert.Equal(t, 100, status.AvailableStreams) // Back to full capacity
}

func TestResourceManager_ConcurrentAllocation(t *testing.T) {
	rm := NewResourceManager(DefaultResourceConfig())
	err := rm.Start()
	require.NoError(t, err)
	defer rm.Stop()

	sessionID := "test-session"
	numStreams := 10

	// Allocate multiple streams concurrently
	results := make(chan error, numStreams)
	for i := 0; i < numStreams; i++ {
		go func(streamID uint64) {
			_, err := rm.AllocateStreamResources(sessionID, streamID)
			results <- err
		}(uint64(i + 1))
	}

	// Collect results
	var errors []error
	for i := 0; i < numStreams; i++ {
		select {
		case err := <-results:
			if err != nil {
				errors = append(errors, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Timeout waiting for resource allocation")
		}
	}

	// All allocations should succeed
	assert.Len(t, errors, 0, "No errors should occur during concurrent allocation")

	// Check final status
	status := rm.CheckResourceAvailability(sessionID)
	assert.Equal(t, 90, status.AvailableStreams) // 100 - 10 allocated
}

func TestResourceManager_ExceedLimits(t *testing.T) {
	config := DefaultResourceConfig()
	config.MaxConcurrentStreams = 5 // Small limit for testing
	
	rm := NewResourceManager(config)
	err := rm.Start()
	require.NoError(t, err)
	defer rm.Stop()

	sessionID := "test-session"

	// Allocate up to the limit
	for i := 0; i < 5; i++ {
		_, err := rm.AllocateStreamResources(sessionID, uint64(i+1))
		assert.NoError(t, err)
	}

	// Try to exceed the limit
	_, err = rm.AllocateStreamResources(sessionID, uint64(6))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "maximum concurrent streams")
}