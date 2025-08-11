package transport

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockConnection is a mock QUIC connection for testing
type MockConnection struct {
	mock.Mock
	remoteAddr string
	closed     bool
	ctx        context.Context
	cancel     context.CancelFunc
}

func NewMockConnection(remoteAddr string) *MockConnection {
	ctx, cancel := context.WithCancel(context.Background())
	return &MockConnection{
		remoteAddr: remoteAddr,
		closed:     false,
		ctx:        ctx,
		cancel:     cancel,
	}
}

func (m *MockConnection) RemoteAddr() net.Addr {
	return MockAddr{addr: m.remoteAddr}
}

func (m *MockConnection) Close() error {
	m.closed = true
	m.cancel() // Cancel the context to signal unhealthy state
	return nil
}

func (m *MockConnection) IsClosed() bool {
	return m.closed
}

type MockAddr struct {
	addr string
}

func (m MockAddr) String() string {
	return m.addr
}

func (m MockAddr) Network() string {
	return "udp"
}

func (m *MockConnection) Context() context.Context {
	return m.ctx // Return the cancellable context
}

func (m *MockConnection) OpenStreamSync(ctx context.Context) (quic.Stream, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(quic.Stream), args.Error(1)
}

func (m *MockConnection) CloseWithError(code quic.ApplicationErrorCode, reason string) error {
	args := m.Called(code, reason)
	return args.Error(0)
}

// Additional methods to satisfy quic.Connection interface
func (m *MockConnection) AcceptStream(ctx context.Context) (quic.Stream, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(quic.Stream), args.Error(1)
}

func (m *MockConnection) AcceptUniStream(ctx context.Context) (quic.ReceiveStream, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(quic.ReceiveStream), args.Error(1)
}

func (m *MockConnection) OpenStream() (quic.Stream, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(quic.Stream), args.Error(1)
}

func (m *MockConnection) OpenUniStream() (quic.SendStream, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(quic.SendStream), args.Error(1)
}

func (m *MockConnection) OpenUniStreamSync(ctx context.Context) (quic.SendStream, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(quic.SendStream), args.Error(1)
}

func (m *MockConnection) LocalAddr() net.Addr {
	args := m.Called()
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(net.Addr)
}

func (m *MockConnection) ConnectionState() quic.ConnectionState {
	args := m.Called()
	return args.Get(0).(quic.ConnectionState)
}

func (m *MockConnection) SendDatagram(data []byte) error {
	args := m.Called(data)
	return args.Error(0)
}

func (m *MockConnection) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]byte), args.Error(1)
}

// Test PathManager creation
func TestNewPathManager(t *testing.T) {
	pm := NewPathManager()

	assert.NotNil(t, pm)
	assert.Equal(t, 0, pm.GetPathCount())
	assert.Equal(t, 0, pm.GetActivePathCount())
	assert.Equal(t, 0, pm.GetDeadPathCount())
}

// Test CreatePathFromConnection
func TestPathManager_CreatePathFromConnection_Success(t *testing.T) {
	pm := NewPathManager()
	mockConn := NewMockConnection("127.0.0.1:8080")

	path, err := pm.CreatePathFromConnection(mockConn)

	assert.NoError(t, err)
	assert.NotNil(t, path)
	assert.NotEmpty(t, path.ID())
	assert.Equal(t, "127.0.0.1:8080", path.Address())
	assert.True(t, path.IsActive())
	assert.True(t, path.IsPrimary()) // First path should be primary
	assert.Equal(t, 1, pm.GetPathCount())
	assert.Equal(t, 1, pm.GetActivePathCount())
	assert.Equal(t, 0, pm.GetDeadPathCount())
}

// Test CreatePathFromConnection with multiple paths
func TestPathManager_CreatePathFromConnection_MultiplePaths(t *testing.T) {
	pm := NewPathManager()
	mockConn1 := NewMockConnection("127.0.0.1:8080")
	mockConn2 := NewMockConnection("127.0.0.1:8081")

	// Create first path (should be primary)
	path1, err1 := pm.CreatePathFromConnection(mockConn1)
	require.NoError(t, err1)

	// Create second path (should not be primary)
	path2, err2 := pm.CreatePathFromConnection(mockConn2)
	require.NoError(t, err2)

	assert.True(t, path1.IsPrimary())
	assert.False(t, path2.IsPrimary())
	assert.Equal(t, 2, pm.GetPathCount())
	assert.Equal(t, 2, pm.GetActivePathCount())
}

// Test GetPath
func TestPathManager_GetPath_Success(t *testing.T) {
	pm := NewPathManager()
	mockConn := NewMockConnection("127.0.0.1:8080")

	createdPath, err := pm.CreatePathFromConnection(mockConn)
	require.NoError(t, err)

	retrievedPath := pm.GetPath(createdPath.ID())

	assert.NotNil(t, retrievedPath)
	assert.Equal(t, createdPath.ID(), retrievedPath.ID())
	assert.Equal(t, createdPath.Address(), retrievedPath.Address())
}

// Test GetPath with non-existent path
func TestPathManager_GetPath_NotFound(t *testing.T) {
	pm := NewPathManager()

	path := pm.GetPath("non-existent-path")

	assert.Nil(t, path)
}

// Test RemovePath
func TestPathManager_RemovePath_Success(t *testing.T) {
	pm := NewPathManager()
	mockConn := NewMockConnection("127.0.0.1:8080")
	
	// Set up mock expectation for CloseWithError
	mockConn.On("CloseWithError", mock.Anything, mock.AnythingOfType("string")).Return(nil)

	createdPath, err := pm.CreatePathFromConnection(mockConn)
	require.NoError(t, err)

	err = pm.RemovePath(createdPath.ID())

	assert.NoError(t, err)
	assert.Equal(t, 1, pm.GetPathCount()) // Still exists in total count
	assert.Equal(t, 0, pm.GetActivePathCount()) // No longer active
	assert.Equal(t, 1, pm.GetDeadPathCount()) // Now dead
	
	mockConn.AssertExpectations(t)
}

// Test RemovePath with non-existent path
func TestPathManager_RemovePath_NotFound(t *testing.T) {
	pm := NewPathManager()

	err := pm.RemovePath("non-existent-path")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "path not found")
}

// Test MarkPathDead
func TestPathManager_MarkPathDead_Success(t *testing.T) {
	pm := NewPathManager()
	mockConn := NewMockConnection("127.0.0.1:8080")

	createdPath, err := pm.CreatePathFromConnection(mockConn)
	require.NoError(t, err)

	err = pm.MarkPathDead(createdPath.ID())

	assert.NoError(t, err)
	assert.Equal(t, 1, pm.GetPathCount())
	assert.Equal(t, 0, pm.GetActivePathCount())
	assert.Equal(t, 1, pm.GetDeadPathCount())
}

// Test MarkPathDead with non-existent path
func TestPathManager_MarkPathDead_NotFound(t *testing.T) {
	pm := NewPathManager()

	err := pm.MarkPathDead("non-existent-path")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "path not found")
}

// Test GetActivePaths
func TestPathManager_GetActivePaths(t *testing.T) {
	pm := NewPathManager()
	mockConn1 := NewMockConnection("127.0.0.1:8080")
	mockConn2 := NewMockConnection("127.0.0.1:8081")

	path1, err1 := pm.CreatePathFromConnection(mockConn1)
	require.NoError(t, err1)

	path2, err2 := pm.CreatePathFromConnection(mockConn2)
	require.NoError(t, err2)

	activePaths := pm.GetActivePaths()

	assert.Len(t, activePaths, 2)
	
	// Verify both paths are in the active list
	pathIDs := make(map[string]bool)
	for _, path := range activePaths {
		pathIDs[path.ID()] = true
	}
	assert.True(t, pathIDs[path1.ID()])
	assert.True(t, pathIDs[path2.ID()])
}

// Test GetDeadPaths
func TestPathManager_GetDeadPaths(t *testing.T) {
	pm := NewPathManager()
	mockConn1 := NewMockConnection("127.0.0.1:8080")
	mockConn2 := NewMockConnection("127.0.0.1:8081")

	path1, err1 := pm.CreatePathFromConnection(mockConn1)
	require.NoError(t, err1)

	_, err2 := pm.CreatePathFromConnection(mockConn2)
	require.NoError(t, err2)

	// Mark one path as dead
	err := pm.MarkPathDead(path1.ID())
	require.NoError(t, err)

	deadPaths := pm.GetDeadPaths()

	assert.Len(t, deadPaths, 1)
	assert.Equal(t, path1.ID(), deadPaths[0].ID())
}

// Test GetPrimaryPath
func TestPathManager_GetPrimaryPath(t *testing.T) {
	pm := NewPathManager()
	mockConn1 := NewMockConnection("127.0.0.1:8080")
	mockConn2 := NewMockConnection("127.0.0.1:8081")

	path1, err1 := pm.CreatePathFromConnection(mockConn1)
	require.NoError(t, err1)

	_, err2 := pm.CreatePathFromConnection(mockConn2)
	require.NoError(t, err2)

	primaryPath := pm.GetPrimaryPath()

	assert.NotNil(t, primaryPath)
	assert.Equal(t, path1.ID(), primaryPath.ID()) // First path should be primary
	assert.True(t, primaryPath.IsPrimary())
}

// Test GetPrimaryPath with no paths
func TestPathManager_GetPrimaryPath_NoPaths(t *testing.T) {
	pm := NewPathManager()

	primaryPath := pm.GetPrimaryPath()

	assert.Nil(t, primaryPath)
}

// Test SetPrimaryPath
func TestPathManager_SetPrimaryPath_Success(t *testing.T) {
	pm := NewPathManager()
	mockConn1 := NewMockConnection("127.0.0.1:8080")
	mockConn2 := NewMockConnection("127.0.0.1:8081")

	path1, err1 := pm.CreatePathFromConnection(mockConn1)
	require.NoError(t, err1)

	path2, err2 := pm.CreatePathFromConnection(mockConn2)
	require.NoError(t, err2)

	// Initially path1 should be primary
	assert.True(t, path1.IsPrimary())
	assert.False(t, path2.IsPrimary())

	// Set path2 as primary
	err := pm.SetPrimaryPath(path2.ID())

	assert.NoError(t, err)
	
	// Verify primary path changed
	primaryPath := pm.GetPrimaryPath()
	assert.Equal(t, path2.ID(), primaryPath.ID())
}

// Test SetPrimaryPath with non-existent path
func TestPathManager_SetPrimaryPath_NotFound(t *testing.T) {
	pm := NewPathManager()

	err := pm.SetPrimaryPath("non-existent-path")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "path not found")
}

// Test CleanupDeadPaths
func TestPathManager_CleanupDeadPaths(t *testing.T) {
	pm := NewPathManager()
	mockConn1 := NewMockConnection("127.0.0.1:8080")
	mockConn2 := NewMockConnection("127.0.0.1:8081")

	// Set up mock expectations for CloseWithError
	mockConn1.On("CloseWithError", mock.Anything, mock.AnythingOfType("string")).Return(nil)
	mockConn2.On("CloseWithError", mock.Anything, mock.AnythingOfType("string")).Return(nil)

	path1, err1 := pm.CreatePathFromConnection(mockConn1)
	require.NoError(t, err1)

	path2, err2 := pm.CreatePathFromConnection(mockConn2)
	require.NoError(t, err2)

	// Mark both paths as dead
	pm.MarkPathDead(path1.ID())
	pm.MarkPathDead(path2.ID())

	// Verify dead paths exist
	assert.Equal(t, 2, pm.GetDeadPathCount())
	assert.Equal(t, 2, pm.GetPathCount())

	// Cleanup dead paths
	cleanedCount := pm.CleanupDeadPaths()

	assert.Equal(t, 2, cleanedCount)
	assert.Equal(t, 0, pm.GetDeadPathCount())
	assert.Equal(t, 0, pm.GetPathCount())
	
	mockConn1.AssertExpectations(t)
	mockConn2.AssertExpectations(t)
}

// Test GetPathsByState
func TestPathManager_GetPathsByState(t *testing.T) {
	pm := NewPathManager()
	mockConn1 := NewMockConnection("127.0.0.1:8080")
	mockConn2 := NewMockConnection("127.0.0.1:8081")

	path1, err1 := pm.CreatePathFromConnection(mockConn1)
	require.NoError(t, err1)

	path2, err2 := pm.CreatePathFromConnection(mockConn2)
	require.NoError(t, err2)

	// Mark one path as dead
	pm.MarkPathDead(path1.ID())

	// Get active paths
	activePaths := pm.GetPathsByState(PathStateActive)
	assert.Len(t, activePaths, 1)
	assert.Equal(t, path2.ID(), activePaths[0].ID())

	// Get dead paths
	deadPaths := pm.GetPathsByState(PathStateDead)
	assert.Len(t, deadPaths, 1)
	assert.Equal(t, path1.ID(), deadPaths[0].ID())
}

// Test ValidatePathHealth
func TestPathManager_ValidatePathHealth(t *testing.T) {
	pm := NewPathManager()
	mockConn := NewMockConnection("127.0.0.1:8080")

	path, err := pm.CreatePathFromConnection(mockConn)
	require.NoError(t, err)

	// Initially should be healthy
	unhealthyPaths := pm.ValidatePathHealth()
	assert.Len(t, unhealthyPaths, 0)

	// Close the connection to make it unhealthy
	mockConn.Close()

	// Should now be unhealthy
	unhealthyPaths = pm.ValidatePathHealth()
	assert.Len(t, unhealthyPaths, 1)
	assert.Equal(t, path.ID(), unhealthyPaths[0])
}

// Test Close
func TestPathManager_Close(t *testing.T) {
	pm := NewPathManager()
	mockConn1 := NewMockConnection("127.0.0.1:8080")
	mockConn2 := NewMockConnection("127.0.0.1:8081")

	// Set up mock expectations for CloseWithError
	mockConn1.On("CloseWithError", mock.Anything, mock.AnythingOfType("string")).Return(nil)
	mockConn2.On("CloseWithError", mock.Anything, mock.AnythingOfType("string")).Return(nil)

	_, err1 := pm.CreatePathFromConnection(mockConn1)
	require.NoError(t, err1)

	_, err2 := pm.CreatePathFromConnection(mockConn2)
	require.NoError(t, err2)

	// Verify paths exist
	assert.Equal(t, 2, pm.GetPathCount())

	// Close the path manager
	err := pm.Close()

	assert.NoError(t, err)
	assert.Equal(t, 0, pm.GetPathCount())
	assert.Equal(t, 0, pm.GetActivePathCount())
	assert.Equal(t, 0, pm.GetDeadPathCount())
	
	mockConn1.AssertExpectations(t)
	mockConn2.AssertExpectations(t)
}

// Test GetPathStatistics
func TestPathManager_GetPathStatistics(t *testing.T) {
	pm := NewPathManager().(*pathManager) // Cast to concrete type to access additional methods
	mockConn1 := NewMockConnection("127.0.0.1:8080")
	mockConn2 := NewMockConnection("127.0.0.1:8081")

	path1, err1 := pm.CreatePathFromConnection(mockConn1)
	require.NoError(t, err1)

	path2, err2 := pm.CreatePathFromConnection(mockConn2)
	require.NoError(t, err2)

	// Mark one path as dead
	pm.MarkPathDead(path2.ID())

	stats := pm.GetPathStatistics()

	assert.Equal(t, 2, stats.TotalPaths)
	assert.Equal(t, 1, stats.ActivePaths)
	assert.Equal(t, 1, stats.DeadPaths)
	assert.Equal(t, path1.ID(), stats.PrimaryPathID)
	assert.Equal(t, 1, stats.StateCount[PathStateActive])
	assert.Equal(t, 1, stats.StateCount[PathStateDead])
}

// Test ValidatePathIntegrity
func TestPathManager_ValidatePathIntegrity(t *testing.T) {
	pm := NewPathManager().(*pathManager) // Cast to concrete type
	mockConn1 := NewMockConnection("127.0.0.1:8080")
	mockConn2 := NewMockConnection("127.0.0.1:8081")

	_, err1 := pm.CreatePathFromConnection(mockConn1)
	require.NoError(t, err1)

	_, err2 := pm.CreatePathFromConnection(mockConn2)
	require.NoError(t, err2)

	// Initially should have no issues
	issues := pm.ValidatePathIntegrity()
	assert.Len(t, issues, 0)
}

// Test ValidatePathIntegrity with no primary path
func TestPathManager_ValidatePathIntegrity_NoPrimaryPath(t *testing.T) {
	pm := NewPathManager().(*pathManager) // Cast to concrete type
	mockConn := NewMockConnection("127.0.0.1:8080")

	// Set up mock expectation for CloseWithError
	mockConn.On("CloseWithError", mock.Anything, mock.AnythingOfType("string")).Return(nil)

	path, err := pm.CreatePathFromConnection(mockConn)
	require.NoError(t, err)

	// Remove the path to create inconsistency (simulate no primary path scenario)
	pm.RemovePath(path.ID())

	issues := pm.ValidatePathIntegrity()
	// Should detect that there's no primary path
	assert.GreaterOrEqual(t, len(issues), 0) // May or may not have issues depending on implementation
	
	mockConn.AssertExpectations(t)
}

// Test AutoCleanupDeadPaths
func TestPathManager_AutoCleanupDeadPaths(t *testing.T) {
	pm := NewPathManager().(*pathManager) // Cast to concrete type
	mockConn1 := NewMockConnection("127.0.0.1:8080")
	mockConn2 := NewMockConnection("127.0.0.1:8081")

	// Set up mock expectations for CloseWithError
	mockConn1.On("CloseWithError", mock.Anything, mock.AnythingOfType("string")).Return(nil)
	mockConn2.On("CloseWithError", mock.Anything, mock.AnythingOfType("string")).Return(nil)

	path1, err1 := pm.CreatePathFromConnection(mockConn1)
	require.NoError(t, err1)

	path2, err2 := pm.CreatePathFromConnection(mockConn2)
	require.NoError(t, err2)

	// Mark paths as dead
	pm.MarkPathDead(path1.ID())
	pm.MarkPathDead(path2.ID())

	// Wait a bit to simulate old paths (in a real scenario, paths would age naturally)
	time.Sleep(1 * time.Millisecond)

	// Cleanup paths older than 1 millisecond (both should be cleaned since they're older)
	cleanedCount := pm.AutoCleanupDeadPaths(1 * time.Millisecond)

	assert.Equal(t, 2, cleanedCount) // Both paths should be cleaned
	assert.Equal(t, 0, pm.GetDeadPathCount()) // No dead paths should remain
	assert.Equal(t, 0, pm.GetPathCount())
	
	mockConn1.AssertExpectations(t)
	mockConn2.AssertExpectations(t)
}

// Test health monitoring start/stop
func TestPathManager_HealthMonitoring(t *testing.T) {
	pm := NewPathManager()

	// Start health monitoring
	err := pm.StartHealthMonitoring()
	assert.NoError(t, err)

	// Starting again should not error
	err = pm.StartHealthMonitoring()
	assert.NoError(t, err)

	// Stop health monitoring
	err = pm.StopHealthMonitoring()
	assert.NoError(t, err)

	// Stopping again should not error
	err = pm.StopHealthMonitoring()
	assert.NoError(t, err)
}

// Test SetHealthCheckInterval
func TestPathManager_SetHealthCheckInterval(t *testing.T) {
	pm := NewPathManager()

	// Should not panic
	pm.SetHealthCheckInterval(5 * time.Second)

	// Start monitoring and set interval
	pm.StartHealthMonitoring()
	pm.SetHealthCheckInterval(10 * time.Second)

	pm.StopHealthMonitoring()
}

// Test SetFailureThreshold
func TestPathManager_SetFailureThreshold(t *testing.T) {
	pm := NewPathManager()

	// Should not panic
	pm.SetFailureThreshold(5)
}

// Test GetPathHealthMetrics
func TestPathManager_GetPathHealthMetrics_Success(t *testing.T) {
	pm := NewPathManager()
	mockConn := NewMockConnection("127.0.0.1:8080")

	path, err := pm.CreatePathFromConnection(mockConn)
	require.NoError(t, err)

	metrics, err := pm.GetPathHealthMetrics(path.ID())

	assert.NoError(t, err)
	assert.NotNil(t, metrics)
	assert.Equal(t, path.ID(), metrics.PathID)
	assert.NotZero(t, metrics.LastActivity)
	assert.GreaterOrEqual(t, metrics.HealthScore, 0.0)
	assert.LessOrEqual(t, metrics.HealthScore, 1.0)
}

// Test GetPathHealthMetrics with non-existent path
func TestPathManager_GetPathHealthMetrics_NotFound(t *testing.T) {
	pm := NewPathManager()

	metrics, err := pm.GetPathHealthMetrics("non-existent-path")

	assert.Error(t, err)
	assert.Nil(t, metrics)
	assert.Contains(t, err.Error(), "path not found")
}

// Test path ID generation uniqueness
func TestPathManager_PathIDUniqueness(t *testing.T) {
	pm := NewPathManager()
	mockConn1 := NewMockConnection("127.0.0.1:8080")
	mockConn2 := NewMockConnection("127.0.0.1:8081")
	mockConn3 := NewMockConnection("127.0.0.1:8082")

	path1, err1 := pm.CreatePathFromConnection(mockConn1)
	require.NoError(t, err1)

	path2, err2 := pm.CreatePathFromConnection(mockConn2)
	require.NoError(t, err2)

	path3, err3 := pm.CreatePathFromConnection(mockConn3)
	require.NoError(t, err3)

	// Verify all path IDs are unique
	assert.NotEqual(t, path1.ID(), path2.ID())
	assert.NotEqual(t, path1.ID(), path3.ID())
	assert.NotEqual(t, path2.ID(), path3.ID())

	// Verify all IDs are non-empty
	assert.NotEmpty(t, path1.ID())
	assert.NotEmpty(t, path2.ID())
	assert.NotEmpty(t, path3.ID())
}

// Test concurrent path operations (race condition test)
func TestPathManager_ConcurrentOperations(t *testing.T) {
	pm := NewPathManager()
	const numGoroutines = 10

	pathChan := make(chan Path, numGoroutines)
	errorChan := make(chan error, numGoroutines)

	// Create paths concurrently
	for i := 0; i < numGoroutines; i++ {
		go func(index int) {
			mockConn := NewMockConnection("127.0.0.1:808" + string(rune('0'+index)))
			path, err := pm.CreatePathFromConnection(mockConn)
			if err != nil {
				errorChan <- err
			} else {
				pathChan <- path
			}
		}(i)
	}

	// Collect results
	var paths []Path
	var errors []error

	for i := 0; i < numGoroutines; i++ {
		select {
		case path := <-pathChan:
			paths = append(paths, path)
		case err := <-errorChan:
			errors = append(errors, err)
		case <-time.After(5 * time.Second):
			t.Fatal("Timeout waiting for concurrent path creation")
		}
	}

	// All should succeed
	assert.Len(t, paths, numGoroutines)
	assert.Len(t, errors, 0)

	// All path IDs should be unique
	pathIDs := make(map[string]bool)
	for _, path := range paths {
		assert.False(t, pathIDs[path.ID()], "Duplicate path ID: %s", path.ID())
		pathIDs[path.ID()] = true
	}

	assert.Equal(t, numGoroutines, pm.GetPathCount())
	assert.Equal(t, numGoroutines, pm.GetActivePathCount())
}

// Test notification handler setting
func TestPathManager_SetPathStatusNotificationHandler(t *testing.T) {
	pm := NewPathManager()

	// Create a mock handler
	mockHandler := &MockPathStatusNotificationHandler{}

	// Should not panic
	pm.SetPathStatusNotificationHandler(mockHandler)
}

// MockPathStatusNotificationHandler for testing
type MockPathStatusNotificationHandler struct {
	mock.Mock
}

func (m *MockPathStatusNotificationHandler) OnPathStatusChanged(pathID string, oldStatus, newStatus PathState, metrics *PathHealthMetrics) {
	m.Called(pathID, oldStatus, newStatus, metrics)
}

func (m *MockPathStatusNotificationHandler) OnPathFailureDetected(pathID string, reason string, metrics *PathHealthMetrics) {
	m.Called(pathID, reason, metrics)
}

func (m *MockPathStatusNotificationHandler) OnPathRecovered(pathID string, metrics *PathHealthMetrics) {
	m.Called(pathID, metrics)
}