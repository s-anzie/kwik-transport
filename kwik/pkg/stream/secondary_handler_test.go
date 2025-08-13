package stream

import (
	"context"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockQuicStream is a mock implementation of quic.Stream for testing
type MockQuicStream struct {
	mock.Mock
	closed bool
}

func (m *MockQuicStream) Read(p []byte) (n int, err error) {
	args := m.Called(p)
	return args.Int(0), args.Error(1)
}

func (m *MockQuicStream) Write(p []byte) (n int, err error) {
	args := m.Called(p)
	return args.Int(0), args.Error(1)
}

func (m *MockQuicStream) Close() error {
	m.closed = true
	args := m.Called()
	return args.Error(0)
}

func (m *MockQuicStream) CloseWithError(code quic.StreamErrorCode, msg string) error {
	m.closed = true
	args := m.Called(code, msg)
	return args.Error(0)
}

func (m *MockQuicStream) CancelRead(code quic.StreamErrorCode) {
	m.Called(code)
}

func (m *MockQuicStream) CancelWrite(code quic.StreamErrorCode) {
	m.Called(code)
}

func (m *MockQuicStream) Context() context.Context {
	args := m.Called()
	if ctx := args.Get(0); ctx != nil {
		return ctx.(context.Context)
	}
	return context.Background()
}

func (m *MockQuicStream) StreamID() quic.StreamID {
	args := m.Called()
	return args.Get(0).(quic.StreamID)
}

func (m *MockQuicStream) SetReadDeadline(t time.Time) error {
	args := m.Called(t)
	return args.Error(0)
}

func (m *MockQuicStream) SetWriteDeadline(t time.Time) error {
	args := m.Called(t)
	return args.Error(0)
}

func (m *MockQuicStream) SetDeadline(t time.Time) error {
	args := m.Called(t)
	return args.Error(0)
}

// Test helper functions
func createTestHandler() *SecondaryStreamHandlerImpl {
	config := &SecondaryStreamConfig{
		MaxStreamsPerPath:    5,
		StreamTimeout:        10 * time.Second,
		BufferSize:           1024,
		MetadataTimeout:      2 * time.Second,
		AggregationBatchSize: 5,
	}
	return NewSecondaryStreamHandler(config)
}

func createMockStream() *MockQuicStream {
	stream := &MockQuicStream{}
	stream.On("Close").Return(nil)
	return stream
}

// Tests for NewSecondaryStreamHandler
func TestNewSecondaryStreamHandler(t *testing.T) {
	t.Run("with custom config", func(t *testing.T) {
		config := &SecondaryStreamConfig{
			MaxStreamsPerPath:    10,
			StreamTimeout:        5 * time.Second,
			BufferSize:           2048,
			MetadataTimeout:      1 * time.Second,
			AggregationBatchSize: 3,
		}

		handler := NewSecondaryStreamHandler(config)

		assert.NotNil(t, handler)
		assert.Equal(t, config, handler.config)
		assert.NotNil(t, handler.activeStreams)
		assert.Equal(t, uint64(1), handler.nextStreamID)
	})

	t.Run("with nil config uses defaults", func(t *testing.T) {
		handler := NewSecondaryStreamHandler(nil)

		assert.NotNil(t, handler)
		assert.NotNil(t, handler.config)
		assert.Equal(t, 100, handler.config.MaxStreamsPerPath)
		assert.Equal(t, 30*time.Second, handler.config.StreamTimeout)
		assert.Equal(t, 64*1024, handler.config.BufferSize)
		assert.Equal(t, 5*time.Second, handler.config.MetadataTimeout)
		assert.Equal(t, 10, handler.config.AggregationBatchSize)
	})
}

// Tests for HandleSecondaryStream
func TestHandleSecondaryStream(t *testing.T) {
	t.Run("successfully handles new stream", func(t *testing.T) {
		handler := createTestHandler()
		stream := createMockStream()
		pathID := "path1"

		_, err := handler.HandleSecondaryStream(pathID, stream)

		assert.NoError(t, err)

		// Verify stream was added
		handler.mutex.RLock()
		pathStreams, exists := handler.activeStreams[pathID]
		handler.mutex.RUnlock()

		assert.True(t, exists)
		assert.Len(t, pathStreams, 1)

		// Get the stream info
		var streamInfo *SecondaryStreamInfo
		for _, info := range pathStreams {
			streamInfo = info
			break
		}

		assert.NotNil(t, streamInfo)
		assert.Equal(t, pathID, streamInfo.PathID)
		assert.Equal(t, stream, streamInfo.QuicStream)
		assert.Equal(t, SecondaryStreamStateActive, streamInfo.State)
		assert.Equal(t, uint64(0), streamInfo.KwikStreamID) // Not mapped yet
		assert.Equal(t, uint64(0), streamInfo.CurrentOffset)
		assert.Equal(t, uint64(0), streamInfo.BytesReceived)
		assert.Equal(t, uint64(0), streamInfo.BytesTransferred)
	})

	t.Run("handles multiple streams on same path", func(t *testing.T) {
		handler := createTestHandler()
		pathID := "path1"

		// Add multiple streams
		for i := 0; i < 3; i++ {
			stream := createMockStream()
			_, err := handler.HandleSecondaryStream(pathID, stream)
			assert.NoError(t, err)
		}

		// Verify all streams were added
		handler.mutex.RLock()
		pathStreams := handler.activeStreams[pathID]
		handler.mutex.RUnlock()

		assert.Len(t, pathStreams, 3)
	})

	t.Run("handles streams on different paths", func(t *testing.T) {
		handler := createTestHandler()

		// Add streams to different paths
		paths := []string{"path1", "path2", "path3"}
		for _, pathID := range paths {
			stream := createMockStream()
			_, err := handler.HandleSecondaryStream(pathID, stream)
			assert.NoError(t, err)
		}

		// Verify streams were added to correct paths
		handler.mutex.RLock()
		assert.Len(t, handler.activeStreams, 3)
		for _, pathID := range paths {
			pathStreams, exists := handler.activeStreams[pathID]
			assert.True(t, exists)
			assert.Len(t, pathStreams, 1)
		}
		handler.mutex.RUnlock()
	})

	t.Run("rejects stream when max streams per path exceeded", func(t *testing.T) {
		handler := createTestHandler()
		pathID := "path1"

		// Add streams up to the limit
		for i := 0; i < handler.config.MaxStreamsPerPath; i++ {
			stream := createMockStream()
			_, err := handler.HandleSecondaryStream(pathID, stream)
			assert.NoError(t, err)
		}

		// Try to add one more stream (should fail)
		stream := createMockStream()
		_, err := handler.HandleSecondaryStream(pathID, stream)

		assert.Error(t, err)
		streamErr, ok := err.(*SecondaryStreamError)
		assert.True(t, ok, "Expected SecondaryStreamError")
		assert.Equal(t, ErrSecondaryStreamOverflow, streamErr.Code)
		assert.Equal(t, pathID, streamErr.PathID)
	})

	t.Run("generates unique stream IDs", func(t *testing.T) {
		// Create handler with higher limit for this test
		config := &SecondaryStreamConfig{
			MaxStreamsPerPath:    15,
			StreamTimeout:        10 * time.Second,
			BufferSize:           1024,
			MetadataTimeout:      2 * time.Second,
			AggregationBatchSize: 5,
		}
		handler := NewSecondaryStreamHandler(config)
		pathID := "path1"
		streamIDs := make(map[uint64]bool)

		// Add multiple streams and collect their IDs
		for i := 0; i < 10; i++ {
			stream := createMockStream()
			_, err := handler.HandleSecondaryStream(pathID, stream)
			assert.NoError(t, err)
		}

		// Verify all IDs are unique
		handler.mutex.RLock()
		pathStreams := handler.activeStreams[pathID]
		for streamID := range pathStreams {
			assert.False(t, streamIDs[streamID], "Stream ID %d is not unique", streamID)
			streamIDs[streamID] = true
		}
		handler.mutex.RUnlock()

		assert.Len(t, streamIDs, 10)
	})
}

// Tests for CloseSecondaryStream
func TestCloseSecondaryStream(t *testing.T) {
	t.Run("successfully closes existing stream", func(t *testing.T) {
		handler := createTestHandler()
		stream := createMockStream()
		pathID := "path1"

		// Add a stream first
		_, err := handler.HandleSecondaryStream(pathID, stream)
		require.NoError(t, err)

		// Get the stream ID
		handler.mutex.RLock()
		var streamID uint64
		for id := range handler.activeStreams[pathID] {
			streamID = id
			break
		}
		handler.mutex.RUnlock()

		// Close the stream
		err = handler.CloseSecondaryStream(pathID, streamID)
		assert.NoError(t, err)

		// Verify stream was removed
		handler.mutex.RLock()
		pathStreams, exists := handler.activeStreams[pathID]
		handler.mutex.RUnlock()

		assert.False(t, exists) // Path should be removed when empty
		assert.Nil(t, pathStreams)

		// Verify underlying QUIC stream was closed
		assert.True(t, stream.closed)
	})

	t.Run("closes stream but keeps path with other streams", func(t *testing.T) {
		handler := createTestHandler()
		pathID := "path1"

		// Add multiple streams
		streams := make([]*MockQuicStream, 3)
		streamIDs := make([]uint64, 3)

		for i := 0; i < 3; i++ {
			streams[i] = createMockStream()
			_, err := handler.HandleSecondaryStream(pathID, streams[i])
			require.NoError(t, err)
		}

		// Get stream IDs and map them to streams
		handler.mutex.RLock()
		streamToID := make(map[*MockQuicStream]uint64)
		i := 0
		for id, streamInfo := range handler.activeStreams[pathID] {
			streamIDs[i] = id
			// Find which mock stream this corresponds to
			for _, stream := range streams {
				if streamInfo.QuicStream == stream {
					streamToID[stream] = id
					break
				}
			}
			i++
		}
		handler.mutex.RUnlock()

		// Close one stream
		err := handler.CloseSecondaryStream(pathID, streamIDs[0])
		assert.NoError(t, err)

		// Verify path still exists with remaining streams
		handler.mutex.RLock()
		pathStreams, exists := handler.activeStreams[pathID]
		handler.mutex.RUnlock()

		assert.True(t, exists)
		assert.Len(t, pathStreams, 2)

		// Verify correct stream was closed - find which stream corresponds to streamIDs[0]
		var closedStream *MockQuicStream
		for stream, id := range streamToID {
			if id == streamIDs[0] {
				closedStream = stream
				break
			}
		}
		
		require.NotNil(t, closedStream, "Should have found the closed stream")
		assert.True(t, closedStream.closed)
		
		// Verify other streams are not closed
		for _, stream := range streams {
			if stream != closedStream {
				assert.False(t, stream.closed)
			}
		}
	})

	t.Run("returns error for non-existent path", func(t *testing.T) {
		handler := createTestHandler()

		err := handler.CloseSecondaryStream("nonexistent", 123)

		assert.Error(t, err)
		streamErr, ok := err.(*SecondaryStreamError)
		assert.True(t, ok, "Expected SecondaryStreamError")
		assert.Equal(t, ErrSecondaryStreamNotFound, streamErr.Code)
		assert.Equal(t, "nonexistent", streamErr.PathID)
		assert.Equal(t, uint64(123), streamErr.StreamID)
	})

	t.Run("returns error for non-existent stream", func(t *testing.T) {
		handler := createTestHandler()
		stream := createMockStream()
		pathID := "path1"

		// Add a stream first
		_, err := handler.HandleSecondaryStream(pathID, stream)
		require.NoError(t, err)

		// Try to close non-existent stream
		err = handler.CloseSecondaryStream(pathID, 999)

		assert.Error(t, err)
		streamErr, ok := err.(*SecondaryStreamError)
		assert.True(t, ok, "Expected SecondaryStreamError")
		assert.Equal(t, ErrSecondaryStreamNotFound, streamErr.Code)
		assert.Equal(t, pathID, streamErr.PathID)
		assert.Equal(t, uint64(999), streamErr.StreamID)
	})
}

// Tests for MapToKwikStream
func TestMapToKwikStream(t *testing.T) {
	t.Run("successfully maps stream to KWIK stream", func(t *testing.T) {
		handler := createTestHandler()
		stream := createMockStream()
		pathID := "path1"

		// Add a stream first
		_, err := handler.HandleSecondaryStream(pathID, stream)
		require.NoError(t, err)

		// Get the stream ID
		handler.mutex.RLock()
		var streamID uint64
		for id := range handler.activeStreams[pathID] {
			streamID = id
			break
		}
		handler.mutex.RUnlock()

		// Map to KWIK stream
		kwikStreamID := uint64(100)
		offset := uint64(1024)
		err = handler.MapToKwikStream(streamID, kwikStreamID, offset)
		assert.NoError(t, err)

		// Verify mapping was set
		handler.mutex.RLock()
		streamInfo := handler.activeStreams[pathID][streamID]
		handler.mutex.RUnlock()

		assert.Equal(t, kwikStreamID, streamInfo.KwikStreamID)
		assert.Equal(t, offset, streamInfo.CurrentOffset)
		assert.True(t, streamInfo.LastActivity.After(streamInfo.CreatedAt))
	})

	t.Run("updates existing mapping", func(t *testing.T) {
		handler := createTestHandler()
		stream := createMockStream()
		pathID := "path1"

		// Add and map a stream
		_, err := handler.HandleSecondaryStream(pathID, stream)
		require.NoError(t, err)

		handler.mutex.RLock()
		var streamID uint64
		for id := range handler.activeStreams[pathID] {
			streamID = id
			break
		}
		handler.mutex.RUnlock()

		// Initial mapping
		err = handler.MapToKwikStream(streamID, 100, 1024)
		require.NoError(t, err)

		// Update mapping
		newKwikStreamID := uint64(200)
		newOffset := uint64(2048)
		err = handler.MapToKwikStream(streamID, newKwikStreamID, newOffset)
		assert.NoError(t, err)

		// Verify updated mapping
		handler.mutex.RLock()
		streamInfo := handler.activeStreams[pathID][streamID]
		handler.mutex.RUnlock()

		assert.Equal(t, newKwikStreamID, streamInfo.KwikStreamID)
		assert.Equal(t, newOffset, streamInfo.CurrentOffset)
	})

	t.Run("returns error for non-existent stream", func(t *testing.T) {
		handler := createTestHandler()

		err := handler.MapToKwikStream(999, 100, 1024)

		assert.Error(t, err)
		streamErr, ok := err.(*SecondaryStreamError)
		assert.True(t, ok, "Expected SecondaryStreamError")
		assert.Equal(t, ErrSecondaryStreamNotFound, streamErr.Code)
		assert.Equal(t, uint64(999), streamErr.StreamID)
	})
}

// Tests for UnmapSecondaryStream
func TestUnmapSecondaryStream(t *testing.T) {
	t.Run("successfully unmaps stream", func(t *testing.T) {
		handler := createTestHandler()
		stream := createMockStream()
		pathID := "path1"

		// Add and map a stream
		_, err := handler.HandleSecondaryStream(pathID, stream)
		require.NoError(t, err)

		handler.mutex.RLock()
		var streamID uint64
		for id := range handler.activeStreams[pathID] {
			streamID = id
			break
		}
		handler.mutex.RUnlock()

		err = handler.MapToKwikStream(streamID, 100, 1024)
		require.NoError(t, err)

		// Unmap the stream
		err = handler.UnmapSecondaryStream(streamID)
		assert.NoError(t, err)

		// Verify mapping was removed
		handler.mutex.RLock()
		streamInfo := handler.activeStreams[pathID][streamID]
		handler.mutex.RUnlock()

		assert.Equal(t, uint64(0), streamInfo.KwikStreamID)
		assert.Equal(t, uint64(0), streamInfo.CurrentOffset)
		assert.True(t, streamInfo.LastActivity.After(streamInfo.CreatedAt))
	})

	t.Run("returns error for non-existent stream", func(t *testing.T) {
		handler := createTestHandler()

		err := handler.UnmapSecondaryStream(999)

		assert.Error(t, err)
		streamErr, ok := err.(*SecondaryStreamError)
		assert.True(t, ok, "Expected SecondaryStreamError")
		assert.Equal(t, ErrSecondaryStreamNotFound, streamErr.Code)
		assert.Equal(t, uint64(999), streamErr.StreamID)
	})
}

// Tests for GetSecondaryStreamStats
func TestGetSecondaryStreamStats(t *testing.T) {
	t.Run("returns empty stats for non-existent path", func(t *testing.T) {
		handler := createTestHandler()

		stats, err := handler.GetSecondaryStreamStats("nonexistent")

		assert.NoError(t, err)
		assert.NotNil(t, stats)
		assert.Equal(t, 0, stats.ActiveStreams)
		assert.Equal(t, uint64(0), stats.TotalDataBytes)
		assert.Equal(t, 0, stats.MappedStreams)
		assert.Equal(t, 0, stats.UnmappedStreams)
		assert.Equal(t, 0.0, stats.AggregationRatio)
	})

	t.Run("returns correct stats for path with streams", func(t *testing.T) {
		handler := createTestHandler()
		pathID := "path1"

		// Add multiple streams with different states
		streams := make([]*MockQuicStream, 4)
		streamIDs := make([]uint64, 4)

		for i := 0; i < 4; i++ {
			streams[i] = createMockStream()
			_, err := handler.HandleSecondaryStream(pathID, streams[i])
			require.NoError(t, err)
		}

		// Get stream IDs
		handler.mutex.RLock()
		i := 0
		for id := range handler.activeStreams[pathID] {
			streamIDs[i] = id
			i++
		}
		handler.mutex.RUnlock()

		// Map some streams and set data
		handler.mutex.Lock()
		handler.activeStreams[pathID][streamIDs[0]].KwikStreamID = 100
		handler.activeStreams[pathID][streamIDs[0]].BytesReceived = 1000
		handler.activeStreams[pathID][streamIDs[0]].BytesTransferred = 800

		handler.activeStreams[pathID][streamIDs[1]].KwikStreamID = 200
		handler.activeStreams[pathID][streamIDs[1]].BytesReceived = 2000
		handler.activeStreams[pathID][streamIDs[1]].BytesTransferred = 1500

		// Leave streamIDs[2] and streamIDs[3] unmapped
		handler.activeStreams[pathID][streamIDs[2]].BytesReceived = 500
		handler.activeStreams[pathID][streamIDs[2]].BytesTransferred = 300

		handler.activeStreams[pathID][streamIDs[3]].BytesReceived = 1500
		handler.activeStreams[pathID][streamIDs[3]].BytesTransferred = 900
		handler.mutex.Unlock()

		// Get stats
		stats, err := handler.GetSecondaryStreamStats(pathID)

		assert.NoError(t, err)
		assert.Equal(t, 4, stats.ActiveStreams)
		assert.Equal(t, uint64(5000), stats.TotalDataBytes) // 1000+2000+500+1500
		assert.Equal(t, 2, stats.MappedStreams)
		assert.Equal(t, 2, stats.UnmappedStreams)
		assert.InDelta(t, 0.7, stats.AggregationRatio, 0.01) // 3500/5000 = 0.7
	})

	t.Run("calculates aggregation ratio correctly", func(t *testing.T) {
		handler := createTestHandler()
		pathID := "path1"

		// Add one stream
		stream := createMockStream()
		_, err := handler.HandleSecondaryStream(pathID, stream)
		require.NoError(t, err)

		// Get stream ID
		handler.mutex.RLock()
		var streamID uint64
		for id := range handler.activeStreams[pathID] {
			streamID = id
			break
		}
		handler.mutex.RUnlock()

		// Set data with perfect aggregation ratio
		handler.mutex.Lock()
		handler.activeStreams[pathID][streamID].BytesReceived = 1000
		handler.activeStreams[pathID][streamID].BytesTransferred = 1000
		handler.mutex.Unlock()

		stats, err := handler.GetSecondaryStreamStats(pathID)

		assert.NoError(t, err)
		assert.Equal(t, 1.0, stats.AggregationRatio)
	})

	t.Run("handles zero bytes received", func(t *testing.T) {
		handler := createTestHandler()
		pathID := "path1"

		// Add stream with no data
		stream := createMockStream()
		_, err := handler.HandleSecondaryStream(pathID, stream)
		require.NoError(t, err)

		stats, err := handler.GetSecondaryStreamStats(pathID)

		assert.NoError(t, err)
		assert.Equal(t, 1, stats.ActiveStreams)
		assert.Equal(t, uint64(0), stats.TotalDataBytes)
		assert.Equal(t, 0.0, stats.AggregationRatio)
	})
}

// Tests for GetActiveMappings
func TestGetActiveMappings(t *testing.T) {
	t.Run("returns empty map when no streams", func(t *testing.T) {
		handler := createTestHandler()

		mappings := handler.GetActiveMappings()

		assert.NotNil(t, mappings)
		assert.Len(t, mappings, 0)
	})

	t.Run("returns empty map when no mapped streams", func(t *testing.T) {
		handler := createTestHandler()
		pathID := "path1"

		// Add unmapped streams
		for i := 0; i < 3; i++ {
			stream := createMockStream()
			_, err := handler.HandleSecondaryStream(pathID, stream)
			require.NoError(t, err)
		}

		mappings := handler.GetActiveMappings()

		assert.NotNil(t, mappings)
		assert.Len(t, mappings, 0)
	})

	t.Run("returns correct mappings for mapped streams", func(t *testing.T) {
		handler := createTestHandler()
		pathID := "path1"

		// Add streams
		streams := make([]*MockQuicStream, 4)
		streamIDs := make([]uint64, 4)

		for i := 0; i < 4; i++ {
			streams[i] = createMockStream()
			_, err := handler.HandleSecondaryStream(pathID, streams[i])
			require.NoError(t, err)
		}

		// Get stream IDs
		handler.mutex.RLock()
		i := 0
		for id := range handler.activeStreams[pathID] {
			streamIDs[i] = id
			i++
		}
		handler.mutex.RUnlock()

		// Map some streams
		err := handler.MapToKwikStream(streamIDs[0], 100, 0)
		require.NoError(t, err)
		err = handler.MapToKwikStream(streamIDs[2], 200, 0)
		require.NoError(t, err)
		// Leave streamIDs[1] and streamIDs[3] unmapped

		mappings := handler.GetActiveMappings()

		assert.Len(t, mappings, 2)
		assert.Equal(t, uint64(100), mappings[streamIDs[0]])
		assert.Equal(t, uint64(200), mappings[streamIDs[2]])
		assert.NotContains(t, mappings, streamIDs[1])
		assert.NotContains(t, mappings, streamIDs[3])
	})

	t.Run("returns mappings from multiple paths", func(t *testing.T) {
		handler := createTestHandler()

		// Add streams to different paths
		paths := []string{"path1", "path2"}
		streamIDs := make([]uint64, 2)

		for i, pathID := range paths {
			stream := createMockStream()
			_, err := handler.HandleSecondaryStream(pathID, stream)
			require.NoError(t, err)

			// Get stream ID
			handler.mutex.RLock()
			for id := range handler.activeStreams[pathID] {
				streamIDs[i] = id
				break
			}
			handler.mutex.RUnlock()

			// Map stream
			err = handler.MapToKwikStream(streamIDs[i], uint64(100+i*100), 0)
			require.NoError(t, err)
		}

		mappings := handler.GetActiveMappings()

		assert.Len(t, mappings, 2)
		assert.Equal(t, uint64(100), mappings[streamIDs[0]])
		assert.Equal(t, uint64(200), mappings[streamIDs[1]])
	})
}

// Tests for SecondaryStreamState
func TestSecondaryStreamState_String(t *testing.T) {
	tests := []struct {
		state    SecondaryStreamState
		expected string
	}{
		{SecondaryStreamStateOpening, "OPENING"},
		{SecondaryStreamStateActive, "ACTIVE"},
		{SecondaryStreamStateClosing, "CLOSING"},
		{SecondaryStreamStateClosed, "CLOSED"},
		{SecondaryStreamStateError, "ERROR"},
		{SecondaryStreamState(999), "UNKNOWN"},
	}

	for _, test := range tests {
		t.Run(test.expected, func(t *testing.T) {
			assert.Equal(t, test.expected, test.state.String())
		})
	}
}

// Tests for SecondaryStreamError
func TestSecondaryStreamError_Error(t *testing.T) {
	t.Run("with path and stream ID", func(t *testing.T) {
		err := &SecondaryStreamError{
			Code:     "TEST_ERROR",
			Message:  "test message",
			PathID:   "path1",
			StreamID: 123,
		}

		errorStr := err.Error()
		assert.Contains(t, errorStr, "TEST_ERROR")
		assert.Contains(t, errorStr, "test message")
		assert.Contains(t, errorStr, "path1")
		assert.Contains(t, errorStr, "123")
	})

	t.Run("with path only", func(t *testing.T) {
		err := &SecondaryStreamError{
			Code:    "TEST_ERROR",
			Message: "test message",
			PathID:  "path1",
		}

		errorStr := err.Error()
		assert.Contains(t, errorStr, "TEST_ERROR")
		assert.Contains(t, errorStr, "test message")
		assert.Contains(t, errorStr, "path1")
		assert.NotContains(t, errorStr, "stream:")
	})

	t.Run("with stream ID only", func(t *testing.T) {
		err := &SecondaryStreamError{
			Code:     "TEST_ERROR",
			Message:  "test message",
			StreamID: 123,
		}

		errorStr := err.Error()
		assert.Contains(t, errorStr, "TEST_ERROR")
		assert.Contains(t, errorStr, "test message")
		assert.Contains(t, errorStr, "123")
		assert.NotContains(t, errorStr, "path:")
	})

	t.Run("with neither path nor stream ID", func(t *testing.T) {
		err := &SecondaryStreamError{
			Code:    "TEST_ERROR",
			Message: "test message",
		}

		errorStr := err.Error()
		assert.Equal(t, "TEST_ERROR: test message", errorStr)
	})
}

// Tests for concurrent access (thread safety)
func TestSecondaryStreamHandler_ConcurrentAccess(t *testing.T) {
	t.Run("concurrent stream handling", func(t *testing.T) {
		// Create handler with higher limit for concurrent test
		config := &SecondaryStreamConfig{
			MaxStreamsPerPath:    100, // Increased limit for concurrent test
			StreamTimeout:        10 * time.Second,
			BufferSize:           1024,
			MetadataTimeout:      2 * time.Second,
			AggregationBatchSize: 5,
		}
		handler := NewSecondaryStreamHandler(config)
		pathID := "path1"

		// Run concurrent operations
		const numGoroutines = 10
		const streamsPerGoroutine = 5

		done := make(chan bool, numGoroutines)

		for i := 0; i < numGoroutines; i++ {
			go func() {
				defer func() { done <- true }()

				for j := 0; j < streamsPerGoroutine; j++ {
					stream := createMockStream()
					_, err := handler.HandleSecondaryStream(pathID, stream)
					assert.NoError(t, err)
				}
			}()
		}

		// Wait for all goroutines to complete
		for i := 0; i < numGoroutines; i++ {
			<-done
		}

		// Verify all streams were added
		handler.mutex.RLock()
		pathStreams := handler.activeStreams[pathID]
		handler.mutex.RUnlock()

		assert.Len(t, pathStreams, numGoroutines*streamsPerGoroutine)
	})

	t.Run("concurrent mapping operations", func(t *testing.T) {
		// Create handler with higher limit for concurrent test
		config := &SecondaryStreamConfig{
			MaxStreamsPerPath:    50, // Increased limit for concurrent test
			StreamTimeout:        10 * time.Second,
			BufferSize:           1024,
			MetadataTimeout:      2 * time.Second,
			AggregationBatchSize: 5,
		}
		handler := NewSecondaryStreamHandler(config)
		pathID := "path1"

		// Add streams first
		const numStreams = 20
		streamIDs := make([]uint64, numStreams)

		for i := 0; i < numStreams; i++ {
			stream := createMockStream()
			_, err := handler.HandleSecondaryStream(pathID, stream)
			require.NoError(t, err)
		}

		// Get stream IDs
		handler.mutex.RLock()
		i := 0
		for id := range handler.activeStreams[pathID] {
			streamIDs[i] = id
			i++
		}
		handler.mutex.RUnlock()

		// Run concurrent mapping operations
		done := make(chan bool, numStreams)

		for i := 0; i < numStreams; i++ {
			go func(streamID uint64, kwikStreamID uint64) {
				defer func() { done <- true }()

				err := handler.MapToKwikStream(streamID, kwikStreamID, 0)
				assert.NoError(t, err)
			}(streamIDs[i], uint64(100+i))
		}

		// Wait for all goroutines to complete
		for i := 0; i < numStreams; i++ {
			<-done
		}

		// Verify all mappings were set
		mappings := handler.GetActiveMappings()
		assert.Len(t, mappings, numStreams)
	})
}

// Tests for stream isolation (ensuring streams don't appear in public interface)
func TestSecondaryStreamHandler_StreamIsolation(t *testing.T) {
	t.Run("secondary streams are isolated from public interface", func(t *testing.T) {
		handler := createTestHandler()
		pathID := "path1"

		// Add secondary streams
		for i := 0; i < 5; i++ {
			stream := createMockStream()
			_, err := handler.HandleSecondaryStream(pathID, stream)
			require.NoError(t, err)
		}

		// Verify streams are managed internally
		handler.mutex.RLock()
		pathStreams := handler.activeStreams[pathID]
		handler.mutex.RUnlock()

		assert.Len(t, pathStreams, 5)

		// Verify each stream has correct isolation properties
		for _, streamInfo := range pathStreams {
			assert.Equal(t, pathID, streamInfo.PathID)
			assert.Equal(t, SecondaryStreamStateActive, streamInfo.State)
			assert.NotNil(t, streamInfo.QuicStream)
			assert.Equal(t, uint64(0), streamInfo.KwikStreamID) // Not mapped initially
		}
	})

	t.Run("stream statistics reflect isolation", func(t *testing.T) {
		handler := createTestHandler()
		pathID := "path1"

		// Add streams with different mapping states
		for i := 0; i < 3; i++ {
			stream := createMockStream()
			_, err := handler.HandleSecondaryStream(pathID, stream)
			require.NoError(t, err)
		}

		// Map only some streams
		handler.mutex.RLock()
		streamIDs := make([]uint64, 0, 3)
		for id := range handler.activeStreams[pathID] {
			streamIDs = append(streamIDs, id)
		}
		handler.mutex.RUnlock()

		// Map first stream only
		err := handler.MapToKwikStream(streamIDs[0], 100, 0)
		require.NoError(t, err)

		// Get stats
		stats, err := handler.GetSecondaryStreamStats(pathID)
		require.NoError(t, err)

		// Verify isolation is reflected in stats
		assert.Equal(t, 3, stats.ActiveStreams)
		assert.Equal(t, 1, stats.MappedStreams)
		assert.Equal(t, 2, stats.UnmappedStreams)

		// Verify only mapped streams appear in active mappings
		mappings := handler.GetActiveMappings()
		assert.Len(t, mappings, 1)
		assert.Equal(t, uint64(100), mappings[streamIDs[0]])
	})
}
