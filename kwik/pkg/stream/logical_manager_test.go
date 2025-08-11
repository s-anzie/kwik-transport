package stream

import (
	"testing"
	"time"

	"kwik/internal/utils"
	"kwik/pkg/control"
	"kwik/pkg/protocol"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockControlPlane is a mock implementation of control.ControlPlane
type MockControlPlane struct {
	mock.Mock
}

func (m *MockControlPlane) SendFrame(pathID string, frame protocol.Frame) error {
	args := m.Called(pathID, frame)
	return args.Error(0)
}

func (m *MockControlPlane) ReceiveFrame(pathID string) (protocol.Frame, error) {
	args := m.Called(pathID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(protocol.Frame), args.Error(1)
}

func (m *MockControlPlane) HandleAddPathRequest(request *control.AddPathRequest) error {
	args := m.Called(request)
	return args.Error(0)
}

func (m *MockControlPlane) HandleRemovePathRequest(req *control.RemovePathRequest) error {
	args := m.Called(req)
	return args.Error(0)
}

func (m *MockControlPlane) HandleAuthenticationRequest(req *control.AuthenticationRequest) error {
	args := m.Called(req)
	return args.Error(0)
}

func (m *MockControlPlane) HandleRawPacketTransmission(req *control.RawPacketTransmission) error {
	args := m.Called(req)
	return args.Error(0)
}

func (m *MockControlPlane) SendPathStatusNotification(pathID string, status control.PathStatus) error {
	args := m.Called(pathID, status)
	return args.Error(0)
}

func (m *MockControlPlane) Close() error {
	args := m.Called()
	return args.Error(0)
}

// MockPathValidator is a mock implementation of PathValidator
type MockPathValidator struct {
	mock.Mock
}

func (m *MockPathValidator) ValidatePathForStreamCreation(pathID string) error {
	args := m.Called(pathID)
	return args.Error(0)
}

func (m *MockPathValidator) GetDefaultPathForStreams() (string, error) {
	args := m.Called()
	return args.String(0), args.Error(1)
}

// Test LogicalStreamManager creation
func TestNewLogicalStreamManager(t *testing.T) {
	mockControlPlane := &MockControlPlane{}
	mockPathValidator := &MockPathValidator{}
	config := DefaultLogicalStreamConfig()

	lsm := NewLogicalStreamManager(mockControlPlane, mockPathValidator, config)

	assert.NotNil(t, lsm)
	assert.NotNil(t, lsm.streams)
	assert.Equal(t, config, lsm.config)
	assert.Equal(t, mockControlPlane, lsm.controlPlane)
	assert.Equal(t, mockPathValidator, lsm.pathValidator)
	assert.NotNil(t, lsm.ctx)
	assert.NotNil(t, lsm.cancel)
}

// Test LogicalStreamManager with nil config uses defaults
func TestNewLogicalStreamManager_NilConfig(t *testing.T) {
	mockControlPlane := &MockControlPlane{}
	mockPathValidator := &MockPathValidator{}

	lsm := NewLogicalStreamManager(mockControlPlane, mockPathValidator, nil)

	assert.NotNil(t, lsm)
	assert.NotNil(t, lsm.config)
	assert.Equal(t, utils.DefaultReadBufferSize, lsm.config.DefaultReadBufferSize)
	assert.Equal(t, utils.DefaultWriteBufferSize, lsm.config.DefaultWriteBufferSize)
	assert.Equal(t, 1000, lsm.config.MaxConcurrentStreams)
}

// Test CreateLogicalStream with valid path
func TestLogicalStreamManager_CreateLogicalStream_Success(t *testing.T) {
	mockControlPlane := &MockControlPlane{}
	mockPathValidator := &MockPathValidator{}
	
	mockPathValidator.On("ValidatePathForStreamCreation", "path-1").Return(nil)
	mockControlPlane.On("SendFrame", "path-1", mock.AnythingOfType("*protocol.ControlFrame")).Return(nil)
	
	lsm := NewLogicalStreamManager(mockControlPlane, mockPathValidator, nil)

	streamInfo, err := lsm.CreateLogicalStream("path-1")

	assert.NoError(t, err)
	assert.NotNil(t, streamInfo)
	assert.Equal(t, uint64(1), streamInfo.ID)
	assert.Equal(t, "path-1", streamInfo.PathID)
	assert.Equal(t, LogicalStreamStateActive, streamInfo.State)
	assert.NotZero(t, streamInfo.CreatedAt)
	assert.NotZero(t, streamInfo.LastActivity)
	
	mockPathValidator.AssertExpectations(t)
	mockControlPlane.AssertExpectations(t)
}

// Test CreateLogicalStream with empty path uses default
func TestLogicalStreamManager_CreateLogicalStream_EmptyPathUsesDefault(t *testing.T) {
	mockControlPlane := &MockControlPlane{}
	mockPathValidator := &MockPathValidator{}
	
	mockPathValidator.On("GetDefaultPathForStreams").Return("default-path", nil)
	mockPathValidator.On("ValidatePathForStreamCreation", "default-path").Return(nil)
	mockControlPlane.On("SendFrame", "default-path", mock.AnythingOfType("*protocol.ControlFrame")).Return(nil)
	
	lsm := NewLogicalStreamManager(mockControlPlane, mockPathValidator, nil)

	streamInfo, err := lsm.CreateLogicalStream("")

	assert.NoError(t, err)
	assert.NotNil(t, streamInfo)
	assert.Equal(t, "default-path", streamInfo.PathID)
	
	mockPathValidator.AssertExpectations(t)
	mockControlPlane.AssertExpectations(t)
}

// Test CreateLogicalStream with default path error
func TestLogicalStreamManager_CreateLogicalStream_DefaultPathError(t *testing.T) {
	mockControlPlane := &MockControlPlane{}
	mockPathValidator := &MockPathValidator{}
	
	mockPathValidator.On("GetDefaultPathForStreams").Return("", utils.NewKwikError(utils.ErrConnectionLost, "no default path", nil))
	
	lsm := NewLogicalStreamManager(mockControlPlane, mockPathValidator, nil)

	streamInfo, err := lsm.CreateLogicalStream("")

	assert.Error(t, err)
	assert.Nil(t, streamInfo)
	assert.Contains(t, err.Error(), "failed to get default path for stream creation")
	
	mockPathValidator.AssertExpectations(t)
}

// Test CreateLogicalStream with path validation error
func TestLogicalStreamManager_CreateLogicalStream_PathValidationError(t *testing.T) {
	mockControlPlane := &MockControlPlane{}
	mockPathValidator := &MockPathValidator{}
	
	mockPathValidator.On("ValidatePathForStreamCreation", "invalid-path").Return(utils.NewKwikError(utils.ErrPathDead, "path is dead", nil))
	
	lsm := NewLogicalStreamManager(mockControlPlane, mockPathValidator, nil)

	streamInfo, err := lsm.CreateLogicalStream("invalid-path")

	assert.Error(t, err)
	assert.Nil(t, streamInfo)
	assert.Contains(t, err.Error(), "path validation failed for stream creation")
	
	mockPathValidator.AssertExpectations(t)
}

// Test CreateLogicalStream with max streams limit
func TestLogicalStreamManager_CreateLogicalStream_MaxStreamsLimit(t *testing.T) {
	mockControlPlane := &MockControlPlane{}
	mockPathValidator := &MockPathValidator{}
	config := DefaultLogicalStreamConfig()
	config.MaxConcurrentStreams = 1
	
	mockPathValidator.On("ValidatePathForStreamCreation", "path-1").Return(nil)
	mockControlPlane.On("SendFrame", "path-1", mock.AnythingOfType("*protocol.ControlFrame")).Return(nil)
	
	lsm := NewLogicalStreamManager(mockControlPlane, mockPathValidator, config)

	// Create first stream (should succeed)
	streamInfo1, err1 := lsm.CreateLogicalStream("path-1")
	assert.NoError(t, err1)
	assert.NotNil(t, streamInfo1)

	// Create second stream (should fail due to limit)
	streamInfo2, err2 := lsm.CreateLogicalStream("path-1")
	assert.Error(t, err2)
	assert.Nil(t, streamInfo2)
	assert.Contains(t, err2.Error(), "maximum concurrent streams limit reached")
	
	mockPathValidator.AssertExpectations(t)
	mockControlPlane.AssertExpectations(t)
}

// Test CreateLogicalStream with notification failure
func TestLogicalStreamManager_CreateLogicalStream_NotificationFailure(t *testing.T) {
	mockControlPlane := &MockControlPlane{}
	mockPathValidator := &MockPathValidator{}
	config := DefaultLogicalStreamConfig()
	config.NotificationRetries = 1 // Reduce retries for faster test
	
	mockPathValidator.On("ValidatePathForStreamCreation", "path-1").Return(nil)
	mockControlPlane.On("SendFrame", "path-1", mock.AnythingOfType("*protocol.ControlFrame")).Return(utils.NewKwikError(utils.ErrConnectionLost, "send failed", nil))
	
	lsm := NewLogicalStreamManager(mockControlPlane, mockPathValidator, config)

	streamInfo, err := lsm.CreateLogicalStream("path-1")

	assert.Error(t, err)
	assert.Nil(t, streamInfo)
	assert.Contains(t, err.Error(), "failed to send stream creation notification")
	
	// Verify stream was cleaned up
	assert.Equal(t, 0, lsm.GetStreamCount())
	
	mockPathValidator.AssertExpectations(t)
	mockControlPlane.AssertExpectations(t)
}

// Test GetLogicalStream with existing stream
func TestLogicalStreamManager_GetLogicalStream_Success(t *testing.T) {
	mockControlPlane := &MockControlPlane{}
	mockPathValidator := &MockPathValidator{}
	
	mockPathValidator.On("ValidatePathForStreamCreation", "path-1").Return(nil)
	mockControlPlane.On("SendFrame", "path-1", mock.AnythingOfType("*protocol.ControlFrame")).Return(nil)
	
	lsm := NewLogicalStreamManager(mockControlPlane, mockPathValidator, nil)

	// Create a stream first
	createdStream, err := lsm.CreateLogicalStream("path-1")
	require.NoError(t, err)

	// Get the stream
	retrievedStream, err := lsm.GetLogicalStream(createdStream.ID)

	assert.NoError(t, err)
	assert.NotNil(t, retrievedStream)
	assert.Equal(t, createdStream.ID, retrievedStream.ID)
	assert.Equal(t, createdStream.PathID, retrievedStream.PathID)
	
	mockPathValidator.AssertExpectations(t)
	mockControlPlane.AssertExpectations(t)
}

// Test GetLogicalStream with non-existent stream
func TestLogicalStreamManager_GetLogicalStream_NotFound(t *testing.T) {
	mockControlPlane := &MockControlPlane{}
	mockPathValidator := &MockPathValidator{}
	
	lsm := NewLogicalStreamManager(mockControlPlane, mockPathValidator, nil)

	streamInfo, err := lsm.GetLogicalStream(999)

	assert.Error(t, err)
	assert.Nil(t, streamInfo)
	assert.Contains(t, err.Error(), "logical stream 999 not found")
}

// Test CloseLogicalStream
func TestLogicalStreamManager_CloseLogicalStream_Success(t *testing.T) {
	mockControlPlane := &MockControlPlane{}
	mockPathValidator := &MockPathValidator{}
	
	mockPathValidator.On("ValidatePathForStreamCreation", "path-1").Return(nil)
	mockControlPlane.On("SendFrame", "path-1", mock.AnythingOfType("*protocol.ControlFrame")).Return(nil)
	
	lsm := NewLogicalStreamManager(mockControlPlane, mockPathValidator, nil)

	// Create a stream first
	createdStream, err := lsm.CreateLogicalStream("path-1")
	require.NoError(t, err)

	// Close the stream
	err = lsm.CloseLogicalStream(createdStream.ID)

	assert.NoError(t, err)
	
	// Verify stream is removed
	_, err = lsm.GetLogicalStream(createdStream.ID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	
	mockPathValidator.AssertExpectations(t)
	mockControlPlane.AssertExpectations(t)
}

// Test CloseLogicalStream with non-existent stream
func TestLogicalStreamManager_CloseLogicalStream_NotFound(t *testing.T) {
	mockControlPlane := &MockControlPlane{}
	mockPathValidator := &MockPathValidator{}
	
	lsm := NewLogicalStreamManager(mockControlPlane, mockPathValidator, nil)

	err := lsm.CloseLogicalStream(999)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "logical stream 999 not found")
}

// Test GetActiveStreams
func TestLogicalStreamManager_GetActiveStreams(t *testing.T) {
	mockControlPlane := &MockControlPlane{}
	mockPathValidator := &MockPathValidator{}
	
	mockPathValidator.On("ValidatePathForStreamCreation", "path-1").Return(nil)
	mockControlPlane.On("SendFrame", "path-1", mock.AnythingOfType("*protocol.ControlFrame")).Return(nil)
	
	lsm := NewLogicalStreamManager(mockControlPlane, mockPathValidator, nil)

	// Create multiple streams
	stream1, err1 := lsm.CreateLogicalStream("path-1")
	require.NoError(t, err1)
	
	stream2, err2 := lsm.CreateLogicalStream("path-1")
	require.NoError(t, err2)

	activeStreams := lsm.GetActiveStreams()

	assert.Len(t, activeStreams, 2)
	
	// Verify both streams are in the active list
	streamIDs := make(map[uint64]bool)
	for _, stream := range activeStreams {
		streamIDs[stream.ID] = true
	}
	assert.True(t, streamIDs[stream1.ID])
	assert.True(t, streamIDs[stream2.ID])
	
	mockPathValidator.AssertExpectations(t)
	mockControlPlane.AssertExpectations(t)
}

// Test GetStreamCount
func TestLogicalStreamManager_GetStreamCount(t *testing.T) {
	mockControlPlane := &MockControlPlane{}
	mockPathValidator := &MockPathValidator{}
	
	mockPathValidator.On("ValidatePathForStreamCreation", "path-1").Return(nil)
	mockControlPlane.On("SendFrame", "path-1", mock.AnythingOfType("*protocol.ControlFrame")).Return(nil)
	
	lsm := NewLogicalStreamManager(mockControlPlane, mockPathValidator, nil)

	// Initially should be 0
	assert.Equal(t, 0, lsm.GetStreamCount())

	// Create a stream
	_, err := lsm.CreateLogicalStream("path-1")
	require.NoError(t, err)

	// Should be 1
	assert.Equal(t, 1, lsm.GetStreamCount())
	
	mockPathValidator.AssertExpectations(t)
	mockControlPlane.AssertExpectations(t)
}

// Test SetDefaultPath and GetDefaultPath
func TestLogicalStreamManager_DefaultPath(t *testing.T) {
	mockControlPlane := &MockControlPlane{}
	mockPathValidator := &MockPathValidator{}
	
	lsm := NewLogicalStreamManager(mockControlPlane, mockPathValidator, nil)

	// Initially should be empty
	assert.Equal(t, "", lsm.GetDefaultPath())

	// Set default path
	lsm.SetDefaultPath("default-path")

	// Should return the set path
	assert.Equal(t, "default-path", lsm.GetDefaultPath())
}

// Test Close
func TestLogicalStreamManager_Close(t *testing.T) {
	mockControlPlane := &MockControlPlane{}
	mockPathValidator := &MockPathValidator{}
	
	mockPathValidator.On("ValidatePathForStreamCreation", "path-1").Return(nil)
	mockControlPlane.On("SendFrame", "path-1", mock.AnythingOfType("*protocol.ControlFrame")).Return(nil)
	
	lsm := NewLogicalStreamManager(mockControlPlane, mockPathValidator, nil)

	// Create some streams
	_, err1 := lsm.CreateLogicalStream("path-1")
	require.NoError(t, err1)
	
	_, err2 := lsm.CreateLogicalStream("path-1")
	require.NoError(t, err2)

	// Verify streams exist
	assert.Equal(t, 2, lsm.GetStreamCount())

	// Close the manager
	err := lsm.Close()

	assert.NoError(t, err)
	assert.Equal(t, 0, lsm.GetStreamCount())
	
	mockPathValidator.AssertExpectations(t)
	mockControlPlane.AssertExpectations(t)
}

// Test LogicalStreamState string conversion
func TestLogicalStreamState_String(t *testing.T) {
	tests := []struct {
		state    LogicalStreamState
		expected string
	}{
		{LogicalStreamStateIdle, "IDLE"},
		{LogicalStreamStateCreating, "CREATING"},
		{LogicalStreamStateActive, "ACTIVE"},
		{LogicalStreamStateClosing, "CLOSING"},
		{LogicalStreamStateClosed, "CLOSED"},
		{LogicalStreamStateError, "ERROR"},
		{LogicalStreamState(999), "UNKNOWN"},
	}

	for _, test := range tests {
		assert.Equal(t, test.expected, test.state.String())
	}
}

// Test DefaultLogicalStreamConfig
func TestDefaultLogicalStreamConfig(t *testing.T) {
	config := DefaultLogicalStreamConfig()

	assert.NotNil(t, config)
	assert.Equal(t, utils.DefaultReadBufferSize, config.DefaultReadBufferSize)
	assert.Equal(t, utils.DefaultWriteBufferSize, config.DefaultWriteBufferSize)
	assert.Equal(t, 1000, config.MaxConcurrentStreams)
	assert.Equal(t, 5*time.Minute, config.StreamIdleTimeout)
	assert.Equal(t, 30*time.Second, config.NotificationTimeout)
	assert.Equal(t, 3, config.NotificationRetries)
}

// Test stream ID generation uniqueness
func TestLogicalStreamManager_StreamIDUniqueness(t *testing.T) {
	mockControlPlane := &MockControlPlane{}
	mockPathValidator := &MockPathValidator{}
	
	mockPathValidator.On("ValidatePathForStreamCreation", "path-1").Return(nil)
	mockControlPlane.On("SendFrame", "path-1", mock.AnythingOfType("*protocol.ControlFrame")).Return(nil)
	
	lsm := NewLogicalStreamManager(mockControlPlane, mockPathValidator, nil)

	// Create multiple streams
	stream1, err1 := lsm.CreateLogicalStream("path-1")
	require.NoError(t, err1)
	
	stream2, err2 := lsm.CreateLogicalStream("path-1")
	require.NoError(t, err2)
	
	stream3, err3 := lsm.CreateLogicalStream("path-1")
	require.NoError(t, err3)

	// Verify all stream IDs are unique
	assert.NotEqual(t, stream1.ID, stream2.ID)
	assert.NotEqual(t, stream1.ID, stream3.ID)
	assert.NotEqual(t, stream2.ID, stream3.ID)
	
	// Verify IDs are sequential
	assert.Equal(t, uint64(1), stream1.ID)
	assert.Equal(t, uint64(2), stream2.ID)
	assert.Equal(t, uint64(3), stream3.ID)
	
	mockPathValidator.AssertExpectations(t)
	mockControlPlane.AssertExpectations(t)
}

// Test cleanup of idle streams (integration test for cleanup routine)
func TestLogicalStreamManager_CleanupIdleStreams(t *testing.T) {
	mockControlPlane := &MockControlPlane{}
	mockPathValidator := &MockPathValidator{}
	config := DefaultLogicalStreamConfig()
	config.StreamIdleTimeout = 100 * time.Millisecond // Very short timeout for testing
	
	mockPathValidator.On("ValidatePathForStreamCreation", "path-1").Return(nil)
	mockControlPlane.On("SendFrame", "path-1", mock.AnythingOfType("*protocol.ControlFrame")).Return(nil)
	
	lsm := NewLogicalStreamManager(mockControlPlane, mockPathValidator, config)

	// Create a stream
	stream, err := lsm.CreateLogicalStream("path-1")
	require.NoError(t, err)

	// Verify stream exists
	assert.Equal(t, 1, lsm.GetStreamCount())

	// Start cleanup routine
	lsm.StartCleanupRoutine()

	// Wait for cleanup to occur (timeout + some buffer)
	time.Sleep(200 * time.Millisecond)

	// Stream should be cleaned up due to idle timeout
	assert.Equal(t, 0, lsm.GetStreamCount())
	
	// Verify stream is no longer accessible
	_, err = lsm.GetLogicalStream(stream.ID)
	assert.Error(t, err)
	
	// Close to stop cleanup routine
	lsm.Close()
	
	mockPathValidator.AssertExpectations(t)
	mockControlPlane.AssertExpectations(t)
}

// Test notification serialization
func TestLogicalStreamManager_NotificationSerialization(t *testing.T) {
	mockControlPlane := &MockControlPlane{}
	mockPathValidator := &MockPathValidator{}
	
	lsm := NewLogicalStreamManager(mockControlPlane, mockPathValidator, nil)

	notification := &control.StreamCreateNotification{
		LogicalStreamID: 123,
		PathID:          "test-path",
	}

	data, err := lsm.serializeNotification(notification)

	assert.NoError(t, err)
	assert.NotEmpty(t, data)
	assert.Contains(t, string(data), "STREAM_CREATE:123:test-path")
}

// Test concurrent stream creation (race condition test)
func TestLogicalStreamManager_ConcurrentStreamCreation(t *testing.T) {
	mockControlPlane := &MockControlPlane{}
	mockPathValidator := &MockPathValidator{}
	
	mockPathValidator.On("ValidatePathForStreamCreation", "path-1").Return(nil)
	mockControlPlane.On("SendFrame", "path-1", mock.AnythingOfType("*protocol.ControlFrame")).Return(nil)
	
	lsm := NewLogicalStreamManager(mockControlPlane, mockPathValidator, nil)

	const numGoroutines = 10
	streamChan := make(chan *LogicalStreamInfo, numGoroutines)
	errorChan := make(chan error, numGoroutines)

	// Create streams concurrently
	for i := 0; i < numGoroutines; i++ {
		go func() {
			stream, err := lsm.CreateLogicalStream("path-1")
			if err != nil {
				errorChan <- err
			} else {
				streamChan <- stream
			}
		}()
	}

	// Collect results
	var streams []*LogicalStreamInfo
	var errors []error

	for i := 0; i < numGoroutines; i++ {
		select {
		case stream := <-streamChan:
			streams = append(streams, stream)
		case err := <-errorChan:
			errors = append(errors, err)
		case <-time.After(5 * time.Second):
			t.Fatal("Timeout waiting for concurrent stream creation")
		}
	}

	// All should succeed
	assert.Len(t, streams, numGoroutines)
	assert.Len(t, errors, 0)

	// All stream IDs should be unique
	streamIDs := make(map[uint64]bool)
	for _, stream := range streams {
		assert.False(t, streamIDs[stream.ID], "Duplicate stream ID: %d", stream.ID)
		streamIDs[stream.ID] = true
	}

	assert.Equal(t, numGoroutines, lsm.GetStreamCount())
	
	mockPathValidator.AssertExpectations(t)
	mockControlPlane.AssertExpectations(t)
}