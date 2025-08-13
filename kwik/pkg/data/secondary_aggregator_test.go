package data

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test helper functions
func createTestAggregator() *SecondaryStreamAggregator {
	return NewSecondaryStreamAggregator()
}

func createTestSecondaryData(streamID, kwikStreamID, offset uint64, pathID string, data []byte) *SecondaryStreamData {
	return &SecondaryStreamData{
		StreamID:     streamID,
		PathID:       pathID,
		Data:         data,
		Offset:       offset,
		KwikStreamID: kwikStreamID,
		Timestamp:    time.Now(),
		SequenceNum:  offset, // Use offset as sequence number for simplicity
	}
}

// Tests for NewSecondaryStreamAggregator
func TestNewSecondaryStreamAggregator(t *testing.T) {
	aggregator := NewSecondaryStreamAggregator()
	
	assert.NotNil(t, aggregator)
	assert.NotNil(t, aggregator.secondaryStreams)
	assert.NotNil(t, aggregator.streamMappings)
	assert.NotNil(t, aggregator.kwikStreams)
	assert.NotNil(t, aggregator.config)
	assert.NotNil(t, aggregator.stats)
	
	// Check default configuration
	assert.Equal(t, 1000, aggregator.config.MaxSecondaryStreams)
	assert.Equal(t, 100, aggregator.config.MaxPendingData)
	assert.Equal(t, 100*time.Millisecond, aggregator.config.ReorderTimeout)
	assert.Equal(t, 64, aggregator.config.AggregationBatchSize)
	assert.Equal(t, 65536, aggregator.config.BufferSize)
	assert.Equal(t, 30*time.Second, aggregator.config.CleanupInterval)
}

// Tests for AggregateSecondaryData
func TestAggregateSecondaryData(t *testing.T) {
	t.Run("successfully aggregates in-order data", func(t *testing.T) {
		aggregator := createTestAggregator()
		
		// Create test data
		data1 := createTestSecondaryData(1, 100, 0, "path1", []byte("hello"))
		data2 := createTestSecondaryData(1, 100, 5, "path1", []byte("world"))
		
		// Aggregate data in order
		err := aggregator.AggregateSecondaryData(data1)
		assert.NoError(t, err)
		
		err = aggregator.AggregateSecondaryData(data2)
		assert.NoError(t, err)
		
		// Verify aggregated data
		aggregatedData, err := aggregator.GetAggregatedData(100)
		assert.NoError(t, err)
		assert.Equal(t, []byte("helloworld"), aggregatedData)
		
		// Verify statistics
		stats := aggregator.GetSecondaryStreamStats()
		assert.Equal(t, 1, stats.ActiveSecondaryStreams)
		assert.Equal(t, 1, stats.ActiveKwikStreams)
		assert.Equal(t, uint64(10), stats.TotalBytesAggregated)
		assert.Equal(t, uint64(2), stats.TotalDataFrames)
	})
	
	t.Run("handles out-of-order data correctly", func(t *testing.T) {
		aggregator := createTestAggregator()
		
		// Create test data out of order
		data1 := createTestSecondaryData(1, 100, 5, "path1", []byte("world"))
		data2 := createTestSecondaryData(1, 100, 0, "path1", []byte("hello"))
		
		// Aggregate data out of order
		err := aggregator.AggregateSecondaryData(data1)
		assert.NoError(t, err)
		
		// At this point, data should be pending
		aggregatedData, err := aggregator.GetAggregatedData(100)
		assert.NoError(t, err)
		assert.Empty(t, aggregatedData) // No data aggregated yet
		
		// Add the missing data
		err = aggregator.AggregateSecondaryData(data2)
		assert.NoError(t, err)
		
		// Now all data should be aggregated
		aggregatedData, err = aggregator.GetAggregatedData(100)
		assert.NoError(t, err)
		assert.Equal(t, []byte("helloworld"), aggregatedData)
	})
	
	t.Run("handles multiple secondary streams to same KWIK stream", func(t *testing.T) {
		aggregator := createTestAggregator()
		
		// Create data from different secondary streams
		data1 := createTestSecondaryData(1, 100, 0, "path1", []byte("hello"))
		data2 := createTestSecondaryData(2, 100, 5, "path2", []byte("world"))
		data3 := createTestSecondaryData(1, 100, 10, "path1", []byte("!"))
		
		// Aggregate data from multiple streams
		err := aggregator.AggregateSecondaryData(data1)
		assert.NoError(t, err)
		
		err = aggregator.AggregateSecondaryData(data2)
		assert.NoError(t, err)
		
		err = aggregator.AggregateSecondaryData(data3)
		assert.NoError(t, err)
		
		// Verify aggregated data
		aggregatedData, err := aggregator.GetAggregatedData(100)
		assert.NoError(t, err)
		assert.Equal(t, []byte("helloworld!"), aggregatedData)
		
		// Verify statistics
		stats := aggregator.GetSecondaryStreamStats()
		assert.Equal(t, 2, stats.ActiveSecondaryStreams) // Two different secondary streams
		assert.Equal(t, 1, stats.ActiveKwikStreams)
		assert.Equal(t, uint64(11), stats.TotalBytesAggregated)
	})
	
	t.Run("rejects nil data", func(t *testing.T) {
		aggregator := createTestAggregator()
		
		err := aggregator.AggregateSecondaryData(nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "secondary stream data is nil")
	})
	
	t.Run("rejects invalid data", func(t *testing.T) {
		aggregator := createTestAggregator()
		
		// Test invalid stream ID
		invalidData := createTestSecondaryData(0, 100, 0, "path1", []byte("test"))
		err := aggregator.AggregateSecondaryData(invalidData)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid secondary stream ID")
		
		// Test invalid KWIK stream ID
		invalidData = createTestSecondaryData(1, 0, 0, "path1", []byte("test"))
		err = aggregator.AggregateSecondaryData(invalidData)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid KWIK stream ID")
		
		// Test invalid path ID
		invalidData = createTestSecondaryData(1, 100, 0, "", []byte("test"))
		err = aggregator.AggregateSecondaryData(invalidData)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid path ID")
		
		// Test empty data
		invalidData = createTestSecondaryData(1, 100, 0, "path1", []byte{})
		err = aggregator.AggregateSecondaryData(invalidData)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "empty data")
	})
	
	t.Run("handles duplicate data at same offset", func(t *testing.T) {
		aggregator := createTestAggregator()
		
		// Create duplicate data at same offset
		data1 := createTestSecondaryData(1, 100, 0, "path1", []byte("hello"))
		data2 := createTestSecondaryData(1, 100, 0, "path1", []byte("world"))
		
		// First data should succeed
		err := aggregator.AggregateSecondaryData(data1)
		assert.NoError(t, err)
		
		// Duplicate data should fail
		err = aggregator.AggregateSecondaryData(data2)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate data at offset")
	})
}

// Tests for SetStreamMapping
func TestSetStreamMapping(t *testing.T) {
	t.Run("successfully sets new mapping", func(t *testing.T) {
		aggregator := createTestAggregator()
		
		err := aggregator.SetStreamMapping(1, 100, "path1")
		assert.NoError(t, err)
		
		// Verify mapping was set
		aggregator.mappingsMutex.RLock()
		kwikStreamID, exists := aggregator.streamMappings[1]
		aggregator.mappingsMutex.RUnlock()
		
		assert.True(t, exists)
		assert.Equal(t, uint64(100), kwikStreamID)
		
		// Verify secondary stream was created
		aggregator.streamsMutex.RLock()
		_, exists = aggregator.secondaryStreams[1]
		aggregator.streamsMutex.RUnlock()
		
		assert.True(t, exists)
	})
	
	t.Run("allows setting same mapping twice", func(t *testing.T) {
		aggregator := createTestAggregator()
		
		err := aggregator.SetStreamMapping(1, 100, "path1")
		assert.NoError(t, err)
		
		// Setting same mapping again should succeed
		err = aggregator.SetStreamMapping(1, 100, "path1")
		assert.NoError(t, err)
	})
	
	t.Run("rejects conflicting mapping", func(t *testing.T) {
		aggregator := createTestAggregator()
		
		err := aggregator.SetStreamMapping(1, 100, "path1")
		assert.NoError(t, err)
		
		// Try to map same secondary stream to different KWIK stream
		err = aggregator.SetStreamMapping(1, 200, "path1")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "already mapped")
	})
	
	t.Run("rejects invalid stream IDs", func(t *testing.T) {
		aggregator := createTestAggregator()
		
		// Test invalid secondary stream ID
		err := aggregator.SetStreamMapping(0, 100, "path1")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid stream IDs")
		
		// Test invalid KWIK stream ID
		err = aggregator.SetStreamMapping(1, 0, "path1")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid stream IDs")
	})
}

// Tests for RemoveStreamMapping
func TestRemoveStreamMapping(t *testing.T) {
	t.Run("successfully removes existing mapping", func(t *testing.T) {
		aggregator := createTestAggregator()
		
		// Set up mapping first
		err := aggregator.SetStreamMapping(1, 100, "path1")
		require.NoError(t, err)
		
		// Add some data to create KWIK stream state
		data := createTestSecondaryData(1, 100, 0, "path1", []byte("test"))
		err = aggregator.AggregateSecondaryData(data)
		require.NoError(t, err)
		
		// Remove mapping
		err = aggregator.RemoveStreamMapping(1)
		assert.NoError(t, err)
		
		// Verify mapping was removed
		aggregator.mappingsMutex.RLock()
		_, exists := aggregator.streamMappings[1]
		aggregator.mappingsMutex.RUnlock()
		
		assert.False(t, exists)
		
		// Verify secondary stream was removed
		aggregator.streamsMutex.RLock()
		_, exists = aggregator.secondaryStreams[1]
		aggregator.streamsMutex.RUnlock()
		
		assert.False(t, exists)
		
		// Verify secondary stream was removed from KWIK stream state
		aggregator.kwikMutex.RLock()
		kwikState, exists := aggregator.kwikStreams[100]
		aggregator.kwikMutex.RUnlock()
		
		if exists {
			kwikState.mutex.RLock()
			_, streamExists := kwikState.SecondaryStreams[1]
			kwikState.mutex.RUnlock()
			assert.False(t, streamExists)
		}
	})
	
	t.Run("handles non-existent mapping gracefully", func(t *testing.T) {
		aggregator := createTestAggregator()
		
		// Try to remove non-existent mapping
		err := aggregator.RemoveStreamMapping(999)
		assert.NoError(t, err) // Should not error
	})
}

// Tests for GetAggregatedData
func TestGetAggregatedData(t *testing.T) {
	t.Run("returns aggregated data for existing stream", func(t *testing.T) {
		aggregator := createTestAggregator()
		
		// Add some data
		data := createTestSecondaryData(1, 100, 0, "path1", []byte("test data"))
		err := aggregator.AggregateSecondaryData(data)
		require.NoError(t, err)
		
		// Get aggregated data
		aggregatedData, err := aggregator.GetAggregatedData(100)
		assert.NoError(t, err)
		assert.Equal(t, []byte("test data"), aggregatedData)
	})
	
	t.Run("returns error for non-existent stream", func(t *testing.T) {
		aggregator := createTestAggregator()
		
		_, err := aggregator.GetAggregatedData(999)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "KWIK stream not found")
	})
	
	t.Run("returns copy of data", func(t *testing.T) {
		aggregator := createTestAggregator()
		
		// Add some data
		data := createTestSecondaryData(1, 100, 0, "path1", []byte("test data"))
		err := aggregator.AggregateSecondaryData(data)
		require.NoError(t, err)
		
		// Get aggregated data twice
		data1, err := aggregator.GetAggregatedData(100)
		assert.NoError(t, err)
		
		data2, err := aggregator.GetAggregatedData(100)
		assert.NoError(t, err)
		
		// Modify one copy
		data1[0] = 'X'
		
		// Other copy should be unchanged
		assert.NotEqual(t, data1[0], data2[0])
		assert.Equal(t, byte('t'), data2[0])
	})
}

// Tests for GetSecondaryStreamStats
func TestGetSecondaryStreamStats(t *testing.T) {
	t.Run("returns correct statistics", func(t *testing.T) {
		aggregator := createTestAggregator()
		
		// Initially should have zero stats
		stats := aggregator.GetSecondaryStreamStats()
		assert.Equal(t, 0, stats.ActiveSecondaryStreams)
		assert.Equal(t, 0, stats.ActiveKwikStreams)
		assert.Equal(t, uint64(0), stats.TotalBytesAggregated)
		assert.Equal(t, uint64(0), stats.TotalDataFrames)
		
		// Add some data
		data1 := createTestSecondaryData(1, 100, 0, "path1", []byte("hello"))
		data2 := createTestSecondaryData(2, 200, 0, "path2", []byte("world"))
		
		err := aggregator.AggregateSecondaryData(data1)
		require.NoError(t, err)
		
		err = aggregator.AggregateSecondaryData(data2)
		require.NoError(t, err)
		
		// Check updated stats
		stats = aggregator.GetSecondaryStreamStats()
		assert.Equal(t, 2, stats.ActiveSecondaryStreams)
		assert.Equal(t, 2, stats.ActiveKwikStreams)
		assert.Equal(t, uint64(10), stats.TotalBytesAggregated) // 5 + 5
		assert.Equal(t, uint64(2), stats.TotalDataFrames)
		assert.True(t, stats.LastUpdate.After(time.Time{}))
	})
}

// Tests for CloseSecondaryStream
func TestCloseSecondaryStream(t *testing.T) {
	t.Run("successfully closes existing stream", func(t *testing.T) {
		aggregator := createTestAggregator()
		
		// Set up stream first
		err := aggregator.SetStreamMapping(1, 100, "path1")
		require.NoError(t, err)
		
		data := createTestSecondaryData(1, 100, 0, "path1", []byte("test"))
		err = aggregator.AggregateSecondaryData(data)
		require.NoError(t, err)
		
		// Close stream
		err = aggregator.CloseSecondaryStream(1)
		assert.NoError(t, err)
		
		// Verify stream state was updated
		aggregator.streamsMutex.RLock()
		state, exists := aggregator.secondaryStreams[1]
		aggregator.streamsMutex.RUnlock()
		
		if exists {
			state.mutex.RLock()
			assert.Equal(t, SecondaryStreamStateClosed, state.State)
			state.mutex.RUnlock()
		}
		
		// Verify mapping was removed
		aggregator.mappingsMutex.RLock()
		_, exists = aggregator.streamMappings[1]
		aggregator.mappingsMutex.RUnlock()
		
		assert.False(t, exists)
	})
	
	t.Run("handles non-existent stream gracefully", func(t *testing.T) {
		aggregator := createTestAggregator()
		
		err := aggregator.CloseSecondaryStream(999)
		assert.NoError(t, err) // Should not error
	})
}

// Tests for CloseKwikStream
func TestCloseKwikStream(t *testing.T) {
	t.Run("successfully closes KWIK stream and associated secondary streams", func(t *testing.T) {
		aggregator := createTestAggregator()
		
		// Set up multiple secondary streams mapping to same KWIK stream
		err := aggregator.SetStreamMapping(1, 100, "path1")
		require.NoError(t, err)
		
		err = aggregator.SetStreamMapping(2, 100, "path2")
		require.NoError(t, err)
		
		// Add data to create the streams
		data1 := createTestSecondaryData(1, 100, 0, "path1", []byte("test1"))
		data2 := createTestSecondaryData(2, 100, 5, "path2", []byte("test2"))
		
		err = aggregator.AggregateSecondaryData(data1)
		require.NoError(t, err)
		
		err = aggregator.AggregateSecondaryData(data2)
		require.NoError(t, err)
		
		// Close KWIK stream
		err = aggregator.CloseKwikStream(100)
		assert.NoError(t, err)
		
		// Verify KWIK stream was removed
		aggregator.kwikMutex.RLock()
		_, exists := aggregator.kwikStreams[100]
		aggregator.kwikMutex.RUnlock()
		
		assert.False(t, exists)
		
		// Verify secondary streams were closed
		aggregator.mappingsMutex.RLock()
		_, exists1 := aggregator.streamMappings[1]
		_, exists2 := aggregator.streamMappings[2]
		aggregator.mappingsMutex.RUnlock()
		
		assert.False(t, exists1)
		assert.False(t, exists2)
	})
	
	t.Run("handles non-existent KWIK stream gracefully", func(t *testing.T) {
		aggregator := createTestAggregator()
		
		err := aggregator.CloseKwikStream(999)
		assert.NoError(t, err) // Should not error
	})
}

// Tests for offset conflict resolution
func TestOffsetConflictResolution(t *testing.T) {
	t.Run("handles gaps in offset sequence", func(t *testing.T) {
		aggregator := createTestAggregator()
		
		// Create data with gaps
		data1 := createTestSecondaryData(1, 100, 0, "path1", []byte("hello"))
		data2 := createTestSecondaryData(1, 100, 10, "path1", []byte("world")) // Gap from 5-9
		data3 := createTestSecondaryData(1, 100, 5, "path1", []byte("_____"))  // Fill the gap
		
		// Add data with gap
		err := aggregator.AggregateSecondaryData(data1)
		assert.NoError(t, err)
		
		err = aggregator.AggregateSecondaryData(data2)
		assert.NoError(t, err)
		
		// Should only have first chunk aggregated
		aggregatedData, err := aggregator.GetAggregatedData(100)
		assert.NoError(t, err)
		assert.Equal(t, []byte("hello"), aggregatedData)
		
		// Fill the gap
		err = aggregator.AggregateSecondaryData(data3)
		assert.NoError(t, err)
		
		// Now all data should be aggregated
		aggregatedData, err = aggregator.GetAggregatedData(100)
		assert.NoError(t, err)
		assert.Equal(t, []byte("hello_____world"), aggregatedData)
	})
	
	t.Run("handles overlapping offsets from different streams", func(t *testing.T) {
		aggregator := createTestAggregator()
		
		// Create overlapping data from different secondary streams
		data1 := createTestSecondaryData(1, 100, 0, "path1", []byte("hello"))
		data2 := createTestSecondaryData(2, 100, 3, "path2", []byte("world")) // Overlaps at offset 3-4
		
		err := aggregator.AggregateSecondaryData(data1)
		assert.NoError(t, err)
		
		err = aggregator.AggregateSecondaryData(data2)
		assert.NoError(t, err)
		
		// Should aggregate in order
		aggregatedData, err := aggregator.GetAggregatedData(100)
		assert.NoError(t, err)
		assert.Equal(t, []byte("helworld"), aggregatedData) // "hel" + "world"
	})
}

// Tests for performance and limits
func TestPerformanceAndLimits(t *testing.T) {
	t.Run("respects maximum secondary streams limit", func(t *testing.T) {
		// Create aggregator with low limit for testing
		aggregator := NewSecondaryStreamAggregator()
		aggregator.config.MaxSecondaryStreams = 3
		
		// Add streams up to limit
		for i := uint64(1); i <= 3; i++ {
			data := createTestSecondaryData(i, 100, 0, "path1", []byte("test"))
			err := aggregator.AggregateSecondaryData(data)
			assert.NoError(t, err)
		}
		
		// Try to add one more stream (should fail)
		data := createTestSecondaryData(4, 100, 0, "path1", []byte("test"))
		err := aggregator.AggregateSecondaryData(data)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "maximum secondary streams reached")
	})
	
	t.Run("respects maximum pending data limit", func(t *testing.T) {
		aggregator := NewSecondaryStreamAggregator()
		aggregator.config.MaxPendingData = 2
		
		// Add out-of-order data up to limit
		data1 := createTestSecondaryData(1, 100, 10, "path1", []byte("data1"))
		data2 := createTestSecondaryData(1, 100, 20, "path1", []byte("data2"))
		data3 := createTestSecondaryData(1, 100, 30, "path1", []byte("data3"))
		
		err := aggregator.AggregateSecondaryData(data1)
		assert.NoError(t, err)
		
		err = aggregator.AggregateSecondaryData(data2)
		assert.NoError(t, err)
		
		// Third out-of-order data should fail
		err = aggregator.AggregateSecondaryData(data3)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "maximum pending data reached")
	})
	
	t.Run("handles large data efficiently", func(t *testing.T) {
		aggregator := createTestAggregator()
		
		// Create large data chunks
		largeData1 := make([]byte, 10000)
		largeData2 := make([]byte, 10000)
		
		for i := range largeData1 {
			largeData1[i] = byte('A')
			largeData2[i] = byte('B')
		}
		
		data1 := createTestSecondaryData(1, 100, 0, "path1", largeData1)
		data2 := createTestSecondaryData(1, 100, 10000, "path1", largeData2)
		
		start := time.Now()
		
		err := aggregator.AggregateSecondaryData(data1)
		assert.NoError(t, err)
		
		err = aggregator.AggregateSecondaryData(data2)
		assert.NoError(t, err)
		
		duration := time.Since(start)
		
		// Should complete quickly (less than 10ms for this amount of data)
		assert.True(t, duration < 10*time.Millisecond, "Processing should take less than 10ms, took %v", duration)
		
		// Verify data integrity
		aggregatedData, err := aggregator.GetAggregatedData(100)
		assert.NoError(t, err)
		assert.Len(t, aggregatedData, 20000)
		
		// Check first and last bytes
		assert.Equal(t, byte('A'), aggregatedData[0])
		assert.Equal(t, byte('A'), aggregatedData[9999])
		assert.Equal(t, byte('B'), aggregatedData[10000])
		assert.Equal(t, byte('B'), aggregatedData[19999])
	})
}

// Tests for concurrent access (thread safety)
func TestSecondaryAggregator_ConcurrentAccess(t *testing.T) {
	t.Run("concurrent aggregation from multiple goroutines", func(t *testing.T) {
		aggregator := createTestAggregator()
		
		const numGoroutines = 10
		const dataPerGoroutine = 100
		
		done := make(chan bool, numGoroutines)
		
		for i := 0; i < numGoroutines; i++ {
			go func(goroutineID int) {
				defer func() { done <- true }()
				
				for j := 0; j < dataPerGoroutine; j++ {
					streamID := uint64(goroutineID*dataPerGoroutine + j + 1)
					kwikStreamID := uint64(100 + goroutineID) // Different KWIK streams per goroutine
					offset := uint64(j * 10)
					
					data := createTestSecondaryData(streamID, kwikStreamID, offset, "path1", []byte("test"))
					err := aggregator.AggregateSecondaryData(data)
					assert.NoError(t, err)
				}
			}(i)
		}
		
		// Wait for all goroutines to complete
		for i := 0; i < numGoroutines; i++ {
			<-done
		}
		
		// Verify statistics
		stats := aggregator.GetSecondaryStreamStats()
		assert.Equal(t, numGoroutines*dataPerGoroutine, stats.ActiveSecondaryStreams)
		assert.Equal(t, numGoroutines, stats.ActiveKwikStreams)
		assert.Equal(t, uint64(numGoroutines*dataPerGoroutine), stats.TotalDataFrames)
	})
	
	t.Run("concurrent mapping operations", func(t *testing.T) {
		aggregator := createTestAggregator()
		
		const numMappings = 100
		done := make(chan bool, numMappings)
		
		for i := 0; i < numMappings; i++ {
			go func(id int) {
				defer func() { done <- true }()
				
				secondaryStreamID := uint64(id + 1)
				kwikStreamID := uint64(100 + id%10) // Map to 10 different KWIK streams
				pathID := "path1"
				
				err := aggregator.SetStreamMapping(secondaryStreamID, kwikStreamID, pathID)
				assert.NoError(t, err)
			}(i)
		}
		
		// Wait for all goroutines to complete
		for i := 0; i < numMappings; i++ {
			<-done
		}
		
		// Verify all mappings were set
		aggregator.mappingsMutex.RLock()
		assert.Len(t, aggregator.streamMappings, numMappings)
		aggregator.mappingsMutex.RUnlock()
	})
	
	t.Run("concurrent read and write operations", func(t *testing.T) {
		aggregator := createTestAggregator()
		
		// Set up initial data
		for i := uint64(1); i <= 10; i++ {
			data := createTestSecondaryData(i, 100, (i-1)*10, "path1", []byte("test"))
			err := aggregator.AggregateSecondaryData(data)
			require.NoError(t, err)
		}
		
		const numReaders = 5
		const numWriters = 5
		const operationsPerWorker = 50
		
		done := make(chan bool, numReaders+numWriters)
		
		// Start readers
		for i := 0; i < numReaders; i++ {
			go func() {
				defer func() { done <- true }()
				
				for j := 0; j < operationsPerWorker; j++ {
					_, err := aggregator.GetAggregatedData(100)
					assert.NoError(t, err)
					
					stats := aggregator.GetSecondaryStreamStats()
					assert.NotNil(t, stats)
					
					time.Sleep(time.Microsecond) // Small delay to increase chance of race conditions
				}
			}()
		}
		
		// Start writers
		for i := 0; i < numWriters; i++ {
			go func(writerID int) {
				defer func() { done <- true }()
				
				for j := 0; j < operationsPerWorker; j++ {
					streamID := uint64(100 + writerID*operationsPerWorker + j)
					kwikStreamID := uint64(200 + writerID)
					
					data := createTestSecondaryData(streamID, kwikStreamID, uint64(j*10), "path1", []byte("test"))
					err := aggregator.AggregateSecondaryData(data)
					assert.NoError(t, err)
					
					time.Sleep(time.Microsecond) // Small delay to increase chance of race conditions
				}
			}(i)
		}
		
		// Wait for all workers to complete
		for i := 0; i < numReaders+numWriters; i++ {
			<-done
		}
		
		// Verify final state is consistent
		stats := aggregator.GetSecondaryStreamStats()
		assert.True(t, stats.ActiveSecondaryStreams >= 10) // At least the initial 10 streams
		assert.True(t, stats.ActiveKwikStreams >= 1)       // At least the initial KWIK stream
	})
}

// Tests for data integrity and correctness
func TestDataIntegrityAndCorrectness(t *testing.T) {
	t.Run("maintains data integrity with complex reordering", func(t *testing.T) {
		aggregator := createTestAggregator()
		
		// Create a complex sequence of data that arrives out of order
		expectedData := "The quick brown fox jumps over the lazy dog"
		chunks := []struct {
			offset uint64
			data   string
		}{
			{20, "jumps "},      // Middle chunk
			{0, "The "},         // First chunk
			{31, "the "},        // Near end (fixed offset)
			{10, "brown "},      // Early middle
			{4, "quick "},       // Early chunk
			{26, "over "},       // Late middle
			{35, "lazy dog"},    // Last chunk (adjusted offset)
			{16, "fox "},        // Middle chunk
		}
		
		// Add chunks in random order
		for _, chunk := range chunks {
			data := createTestSecondaryData(1, 100, chunk.offset, "path1", []byte(chunk.data))
			err := aggregator.AggregateSecondaryData(data)
			assert.NoError(t, err)
		}
		
		// Verify final aggregated data
		aggregatedData, err := aggregator.GetAggregatedData(100)
		assert.NoError(t, err)
		assert.Equal(t, expectedData, string(aggregatedData))
	})
	
	t.Run("handles interleaved data from multiple secondary streams", func(t *testing.T) {
		aggregator := createTestAggregator()
		
		// Create interleaved data from two secondary streams
		stream1Data := []struct {
			offset uint64
			data   string
		}{
			{0, "A1"},
			{4, "A2"},
			{8, "A3"},
		}
		
		stream2Data := []struct {
			offset uint64
			data   string
		}{
			{2, "B1"},
			{6, "B2"},
			{10, "B3"},
		}
		
		// Add data from both streams
		for _, chunk := range stream1Data {
			data := createTestSecondaryData(1, 100, chunk.offset, "path1", []byte(chunk.data))
			err := aggregator.AggregateSecondaryData(data)
			assert.NoError(t, err)
		}
		
		for _, chunk := range stream2Data {
			data := createTestSecondaryData(2, 100, chunk.offset, "path2", []byte(chunk.data))
			err := aggregator.AggregateSecondaryData(data)
			assert.NoError(t, err)
		}
		
		// Verify final aggregated data
		aggregatedData, err := aggregator.GetAggregatedData(100)
		assert.NoError(t, err)
		assert.Equal(t, "A1B1A2B2A3B3", string(aggregatedData))
	})
	
	t.Run("correctly updates statistics during aggregation", func(t *testing.T) {
		aggregator := createTestAggregator()
		
		// Track statistics changes
		initialStats := aggregator.GetSecondaryStreamStats()
		assert.Equal(t, 0, initialStats.ActiveSecondaryStreams)
		assert.Equal(t, uint64(0), initialStats.TotalBytesAggregated)
		
		// Add first data
		data1 := createTestSecondaryData(1, 100, 0, "path1", []byte("hello"))
		err := aggregator.AggregateSecondaryData(data1)
		assert.NoError(t, err)
		
		stats1 := aggregator.GetSecondaryStreamStats()
		assert.Equal(t, 1, stats1.ActiveSecondaryStreams)
		assert.Equal(t, 1, stats1.ActiveKwikStreams)
		assert.Equal(t, uint64(5), stats1.TotalBytesAggregated)
		assert.Equal(t, uint64(1), stats1.TotalDataFrames)
		
		// Add second data from different stream
		data2 := createTestSecondaryData(2, 200, 0, "path2", []byte("world"))
		err = aggregator.AggregateSecondaryData(data2)
		assert.NoError(t, err)
		
		stats2 := aggregator.GetSecondaryStreamStats()
		assert.Equal(t, 2, stats2.ActiveSecondaryStreams)
		assert.Equal(t, 2, stats2.ActiveKwikStreams)
		assert.Equal(t, uint64(10), stats2.TotalBytesAggregated)
		assert.Equal(t, uint64(2), stats2.TotalDataFrames)
		
		// Verify timestamps are updated
		assert.True(t, stats1.LastUpdate.After(initialStats.LastUpdate))
		assert.True(t, stats2.LastUpdate.After(stats1.LastUpdate))
	})
}