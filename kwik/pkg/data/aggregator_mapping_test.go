package data

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test helper functions
func createTestDataAggregator() *DataAggregatorImpl {
	return NewDataAggregator().(*DataAggregatorImpl)
}

func createTestSecondaryStreamData(streamID, kwikStreamID, offset uint64, pathID string, data []byte) *SecondaryStreamData {
	return &SecondaryStreamData{
		StreamID:     streamID,
		PathID:       pathID,
		Data:         data,
		Offset:       offset,
		KwikStreamID: kwikStreamID,
		Timestamp:    time.Now(),
		SequenceNum:  offset,
	}
}

// Tests for basic mapping functionality in DataAggregatorImpl
func TestDataAggregator_BasicMappingFunctionality(t *testing.T) {
	t.Run("successfully sets new mapping", func(t *testing.T) {
		aggregator := createTestDataAggregator()
		
		err := aggregator.SetStreamMapping(100, 1, "path1")
		assert.NoError(t, err)
		
		// Verify mapping was set
		aggregator.mappingsMutex.RLock()
		kwikStreamID, exists := aggregator.streamMappings[1]
		aggregator.mappingsMutex.RUnlock()
		
		assert.True(t, exists)
		assert.Equal(t, uint64(100), kwikStreamID)
	})
	
	t.Run("allows setting same mapping twice", func(t *testing.T) {
		aggregator := createTestDataAggregator()
		
		err := aggregator.SetStreamMapping(100, 1, "path1")
		assert.NoError(t, err)
		
		// Setting same mapping again should succeed
		err = aggregator.SetStreamMapping(100, 1, "path1")
		assert.NoError(t, err)
	})
	
	t.Run("rejects conflicting mapping", func(t *testing.T) {
		aggregator := createTestDataAggregator()
		
		err := aggregator.SetStreamMapping(100, 1, "path1")
		assert.NoError(t, err)
		
		// Try to map same secondary stream to different KWIK stream
		err = aggregator.SetStreamMapping(200, 1, "path1")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "already mapped")
	})
	
	t.Run("rejects invalid stream IDs", func(t *testing.T) {
		aggregator := createTestDataAggregator()
		
		// Test invalid KWIK stream ID
		err := aggregator.SetStreamMapping(0, 1, "path1")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid stream IDs")
		
		// Test invalid secondary stream ID
		err = aggregator.SetStreamMapping(100, 0, "path1")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid stream IDs")
	})
	
	t.Run("rejects empty path ID", func(t *testing.T) {
		aggregator := createTestDataAggregator()
		
		err := aggregator.SetStreamMapping(100, 1, "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "path ID is empty")
	})
}

// Tests for RemoveStreamMapping
func TestDataAggregator_RemoveStreamMapping(t *testing.T) {
	t.Run("successfully removes existing mapping", func(t *testing.T) {
		aggregator := createTestDataAggregator()
		
		// Set up mapping first
		err := aggregator.SetStreamMapping(100, 1, "path1")
		require.NoError(t, err)
		
		// Remove mapping
		err = aggregator.RemoveStreamMapping(1)
		assert.NoError(t, err)
		
		// Verify mapping was removed
		aggregator.mappingsMutex.RLock()
		_, exists := aggregator.streamMappings[1]
		aggregator.mappingsMutex.RUnlock()
		
		assert.False(t, exists)
	})
	
	t.Run("handles non-existent mapping gracefully", func(t *testing.T) {
		aggregator := createTestDataAggregator()
		
		// Try to remove non-existent mapping
		err := aggregator.RemoveStreamMapping(999)
		assert.NoError(t, err) // Should not error
	})
}

// Tests for mapping validation
func TestDataAggregator_MappingValidation(t *testing.T) {
	t.Run("validates stream mapping consistency", func(t *testing.T) {
		aggregator := createTestDataAggregator()
		
		// Set up mapping
		err := aggregator.SetStreamMapping(100, 1, "path1")
		require.NoError(t, err)
		
		// Try to aggregate data with incorrect KWIK stream ID
		data := createTestSecondaryStreamData(1, 200, 5, "path1", []byte("test2"))
		err = aggregator.AggregateSecondaryData(200, data)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "mapped to KWIK stream")
	})
	
	t.Run("handles mapping conflicts correctly", func(t *testing.T) {
		aggregator := createTestDataAggregator()
		
		// Set up initial mapping
		err := aggregator.SetStreamMapping(100, 1, "path1")
		require.NoError(t, err)
		
		// Try to create conflicting mapping
		err = aggregator.SetStreamMapping(200, 1, "path2")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "already mapped")
		
		// Verify original mapping is still intact
		aggregator.mappingsMutex.RLock()
		kwikStreamID, exists := aggregator.streamMappings[1]
		aggregator.mappingsMutex.RUnlock()
		
		assert.True(t, exists)
		assert.Equal(t, uint64(100), kwikStreamID)
	})
}

// Tests for mapping lifecycle management
func TestDataAggregator_MappingLifecycle(t *testing.T) {
	t.Run("manages mapping lifecycle correctly", func(t *testing.T) {
		aggregator := createTestDataAggregator()
		
		// Phase 1: Create mapping
		err := aggregator.SetStreamMapping(100, 1, "path1")
		require.NoError(t, err)
		
		// Verify mapping exists
		aggregator.mappingsMutex.RLock()
		_, exists := aggregator.streamMappings[1]
		aggregator.mappingsMutex.RUnlock()
		assert.True(t, exists)
		
		// Phase 2: Remove mapping
		err = aggregator.RemoveStreamMapping(1)
		assert.NoError(t, err)
		
		// Verify mapping is gone
		aggregator.mappingsMutex.RLock()
		_, exists = aggregator.streamMappings[1]
		aggregator.mappingsMutex.RUnlock()
		assert.False(t, exists)
		
		// Phase 3: Try to use removed mapping (should fail)
		data := createTestSecondaryStreamData(1, 100, 5, "path1", []byte("world"))
		err = aggregator.AggregateSecondaryData(100, data)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not mapped")
	})
	
	t.Run("handles mapping recreation after removal", func(t *testing.T) {
		aggregator := createTestDataAggregator()
		
		// Create mapping
		err := aggregator.SetStreamMapping(100, 1, "path1")
		require.NoError(t, err)
		
		// Remove mapping
		err = aggregator.RemoveStreamMapping(1)
		require.NoError(t, err)
		
		// Recreate mapping (should work)
		err = aggregator.SetStreamMapping(200, 1, "path2")
		assert.NoError(t, err)
		
		// Verify new mapping
		aggregator.mappingsMutex.RLock()
		kwikStreamID, exists := aggregator.streamMappings[1]
		aggregator.mappingsMutex.RUnlock()
		
		assert.True(t, exists)
		assert.Equal(t, uint64(200), kwikStreamID)
	})
	
	t.Run("handles KWIK stream closure with multiple mappings", func(t *testing.T) {
		aggregator := createTestDataAggregator()
		
		// Create multiple mappings to same KWIK stream
		err := aggregator.SetStreamMapping(100, 1, "path1")
		require.NoError(t, err)
		
		err = aggregator.SetStreamMapping(100, 2, "path2")
		require.NoError(t, err)
		
		err = aggregator.SetStreamMapping(100, 3, "path3")
		require.NoError(t, err)
		
		// Create mapping to different KWIK stream
		err = aggregator.SetStreamMapping(200, 4, "path4")
		require.NoError(t, err)
		
		// Close KWIK stream 100 with secondaries
		err = aggregator.CloseKwikStreamWithSecondaries(100)
		assert.NoError(t, err)
		
		// Verify mappings to KWIK stream 100 were removed
		aggregator.mappingsMutex.RLock()
		_, exists1 := aggregator.streamMappings[1]
		_, exists2 := aggregator.streamMappings[2]
		_, exists3 := aggregator.streamMappings[3]
		_, exists4 := aggregator.streamMappings[4]
		aggregator.mappingsMutex.RUnlock()
		
		assert.False(t, exists1)
		assert.False(t, exists2)
		assert.False(t, exists3)
		assert.True(t, exists4) // Should still exist
	})
}

// Tests for concurrent mapping operations
func TestDataAggregator_ConcurrentMappingOperations(t *testing.T) {
	t.Run("concurrent mapping creation", func(t *testing.T) {
		aggregator := createTestDataAggregator()
		
		const numGoroutines = 10
		const mappingsPerGoroutine = 10
		
		done := make(chan bool, numGoroutines)
		
		for i := 0; i < numGoroutines; i++ {
			go func(goroutineID int) {
				defer func() { done <- true }()
				
				for j := 0; j < mappingsPerGoroutine; j++ {
					kwikStreamID := uint64(100 + goroutineID)
					secondaryStreamID := uint64(goroutineID*mappingsPerGoroutine + j + 1)
					pathID := "path1"
					
					err := aggregator.SetStreamMapping(kwikStreamID, secondaryStreamID, pathID)
					assert.NoError(t, err)
				}
			}(i)
		}
		
		// Wait for all goroutines to complete
		for i := 0; i < numGoroutines; i++ {
			<-done
		}
		
		// Verify all mappings were set
		aggregator.mappingsMutex.RLock()
		assert.Len(t, aggregator.streamMappings, numGoroutines*mappingsPerGoroutine)
		aggregator.mappingsMutex.RUnlock()
	})
	
	t.Run("concurrent mapping removal", func(t *testing.T) {
		aggregator := createTestDataAggregator()
		
		// Set up mappings first
		const numMappings = 100
		for i := uint64(1); i <= numMappings; i++ {
			err := aggregator.SetStreamMapping(100, i, "path1")
			require.NoError(t, err)
		}
		
		const numGoroutines = 10
		done := make(chan bool, numGoroutines)
		
		for i := 0; i < numGoroutines; i++ {
			go func(goroutineID int) {
				defer func() { done <- true }()
				
				// Each goroutine removes a subset of mappings
				start := goroutineID * (numMappings / numGoroutines) + 1
				end := start + (numMappings / numGoroutines)
				
				for j := start; j < end; j++ {
					err := aggregator.RemoveStreamMapping(uint64(j))
					assert.NoError(t, err)
				}
			}(i)
		}
		
		// Wait for all goroutines to complete
		for i := 0; i < numGoroutines; i++ {
			<-done
		}
		
		// Verify all mappings were removed
		aggregator.mappingsMutex.RLock()
		assert.Len(t, aggregator.streamMappings, 0)
		aggregator.mappingsMutex.RUnlock()
	})
}

// Tests for edge cases and boundary conditions
func TestDataAggregator_MappingEdgeCases(t *testing.T) {
	t.Run("handles maximum stream IDs", func(t *testing.T) {
		aggregator := createTestDataAggregator()
		
		maxUint64 := uint64(^uint64(0))
		
		// Test with maximum values
		err := aggregator.SetStreamMapping(maxUint64, maxUint64-1, "path1")
		assert.NoError(t, err)
		
		// Verify mapping was set
		aggregator.mappingsMutex.RLock()
		kwikStreamID, exists := aggregator.streamMappings[maxUint64-1]
		aggregator.mappingsMutex.RUnlock()
		
		assert.True(t, exists)
		assert.Equal(t, maxUint64, kwikStreamID)
	})
	
	t.Run("handles special path IDs", func(t *testing.T) {
		aggregator := createTestDataAggregator()
		
		specialPaths := []string{
			"path-with-dashes",
			"path_with_underscores",
			"path.with.dots",
			"path/with/slashes",
			"path with spaces",
			"UPPERCASE_PATH",
			"MiXeD_cAsE_pAtH",
			"path123with456numbers",
		}
		
		for i, pathID := range specialPaths {
			t.Run(pathID, func(t *testing.T) {
				kwikStreamID := uint64(100 + i)
				secondaryStreamID := uint64(i + 1)
				
				err := aggregator.SetStreamMapping(kwikStreamID, secondaryStreamID, pathID)
				assert.NoError(t, err)
				
				// Verify mapping was set
				aggregator.mappingsMutex.RLock()
				mappedKwikStreamID, exists := aggregator.streamMappings[secondaryStreamID]
				aggregator.mappingsMutex.RUnlock()
				
				assert.True(t, exists)
				assert.Equal(t, kwikStreamID, mappedKwikStreamID)
			})
		}
	})
	
	t.Run("handles rapid mapping creation and removal", func(t *testing.T) {
		aggregator := createTestDataAggregator()
		
		// Rapidly create and remove mappings
		for i := 0; i < 1000; i++ {
			kwikStreamID := uint64(100)
			secondaryStreamID := uint64(i + 1)
			pathID := "path1"
			
			// Create mapping
			err := aggregator.SetStreamMapping(kwikStreamID, secondaryStreamID, pathID)
			assert.NoError(t, err)
			
			// Immediately remove mapping
			err = aggregator.RemoveStreamMapping(secondaryStreamID)
			assert.NoError(t, err)
		}
		
		// Verify no mappings remain
		aggregator.mappingsMutex.RLock()
		assert.Len(t, aggregator.streamMappings, 0)
		aggregator.mappingsMutex.RUnlock()
	})
}