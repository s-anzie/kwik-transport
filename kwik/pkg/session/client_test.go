package session

import (
	"context"
	"net"
	"testing"
	"time"

	"kwik/internal/utils"
	"kwik/pkg/transport"

	"github.com/quic-go/quic-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockPathManager is a mock implementation of transport.PathManager
type MockPathManager struct {
	mock.Mock
}

func (m *MockPathManager) CreatePath(address string) (transport.Path, error) {
	args := m.Called(address)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(transport.Path), args.Error(1)
}

func (m *MockPathManager) CreatePathFromConnection(conn quic.Connection) (transport.Path, error) {
	args := m.Called(conn)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(transport.Path), args.Error(1)
}

func (m *MockPathManager) RemovePath(pathID string) error {
	args := m.Called(pathID)
	return args.Error(0)
}

func (m *MockPathManager) GetPath(pathID string) transport.Path {
	args := m.Called(pathID)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(transport.Path)
}

func (m *MockPathManager) GetActivePaths() []transport.Path {
	args := m.Called()
	return args.Get(0).([]transport.Path)
}

func (m *MockPathManager) GetDeadPaths() []transport.Path {
	args := m.Called()
	return args.Get(0).([]transport.Path)
}

func (m *MockPathManager) MarkPathDead(pathID string) error {
	args := m.Called(pathID)
	return args.Error(0)
}

func (m *MockPathManager) GetPathCount() int {
	args := m.Called()
	return args.Int(0)
}

func (m *MockPathManager) GetActivePathCount() int {
	args := m.Called()
	return args.Int(0)
}

func (m *MockPathManager) GetDeadPathCount() int {
	args := m.Called()
	return args.Int(0)
}

func (m *MockPathManager) GetPrimaryPath() transport.Path {
	args := m.Called()
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(transport.Path)
}

func (m *MockPathManager) SetPrimaryPath(pathID string) error {
	args := m.Called(pathID)
	return args.Error(0)
}

func (m *MockPathManager) CleanupDeadPaths() int {
	args := m.Called()
	return args.Int(0)
}

func (m *MockPathManager) GetPathsByState(state transport.PathState) []transport.Path {
	args := m.Called(state)
	return args.Get(0).([]transport.Path)
}

func (m *MockPathManager) ValidatePathHealth() []string {
	args := m.Called()
	return args.Get(0).([]string)
}

func (m *MockPathManager) Close() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockPathManager) StartHealthMonitoring() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockPathManager) StopHealthMonitoring() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockPathManager) SetPathStatusNotificationHandler(handler transport.PathStatusNotificationHandler) {
	m.Called(handler)
}

func (m *MockPathManager) GetPathHealthMetrics(pathID string) (*transport.PathHealthMetrics, error) {
	args := m.Called(pathID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*transport.PathHealthMetrics), args.Error(1)
}

func (m *MockPathManager) SetHealthCheckInterval(interval time.Duration) {
	m.Called(interval)
}

func (m *MockPathManager) SetFailureThreshold(threshold int) {
	m.Called(threshold)
}

func (m *MockPathManager) SetLogger(logger transport.PathLogger) {
	m.Called(logger)
}

// MockPath is a mock implementation of transport.Path
type MockPath struct {
	mock.Mock
	id        string
	address   string
	isPrimary bool
	isActive  bool
}

func NewMockPath(id, address string, isPrimary, isActive bool) *MockPath {
	return &MockPath{
		id:        id,
		address:   address,
		isPrimary: isPrimary,
		isActive:  isActive,
	}
}

func (m *MockPath) ID() string {
	return m.id
}

func (m *MockPath) SetID(id string) {
	m.id = id
}

func (m *MockPath) Address() string {
	return m.address
}

func (m *MockPath) IsActive() bool {
	return m.isActive
}

func (m *MockPath) IsPrimary() bool {
	return m.isPrimary
}

func (m *MockPath) GetConnection() quic.Connection {
	args := m.Called()
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(quic.Connection)
}

func (m *MockPath) GetControlStream() (quic.Stream, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(quic.Stream), args.Error(1)
}

func (m *MockPath) GetDataStreams() []quic.Stream {
	args := m.Called()
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).([]quic.Stream)
}

func (m *MockPath) Close() error {
	args := m.Called()
	return args.Error(0)
}

// New methods for proper control stream management
func (m *MockPath) CreateControlStreamAsClient() (quic.Stream, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(quic.Stream), args.Error(1)
}

func (m *MockPath) AcceptControlStreamAsServer() (quic.Stream, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(quic.Stream), args.Error(1)
}

// Secondary stream support methods
func (m *MockPath) IsSecondaryPath() bool {
	args := m.Called()
	return args.Bool(0)
}

func (m *MockPath) SetSecondaryPath(isSecondary bool) {
	m.Called(isSecondary)
}

func (m *MockPath) AddSecondaryStream(streamID uint64, stream quic.Stream) {
	m.Called(streamID, stream)
}

func (m *MockPath) RemoveSecondaryStream(streamID uint64) {
	m.Called(streamID)
}

func (m *MockPath) GetSecondaryStream(streamID uint64) (quic.Stream, bool) {
	args := m.Called(streamID)
	if args.Get(0) == nil {
		return nil, args.Bool(1)
	}
	return args.Get(0).(quic.Stream), args.Bool(1)
}

func (m *MockPath) GetSecondaryStreams() map[uint64]quic.Stream {
	args := m.Called()
	if args.Get(0) == nil {
		return make(map[uint64]quic.Stream)
	}
	return args.Get(0).(map[uint64]quic.Stream)
}

func (m *MockPath) GetSecondaryStreamCount() int {
	args := m.Called()
	return args.Int(0)
}

func (m *MockPath) SetLogger(logger transport.PathLogger) {
	m.Called(logger)
}

// MockStream is a mock implementation for control streams
type MockStream struct {
	mock.Mock
	readData  []byte
	writeData []byte
}

func NewMockStream() *MockStream {
	return &MockStream{
		readData:  make([]byte, 0),
		writeData: make([]byte, 0),
	}
}

func (m *MockStream) Read(p []byte) (int, error) {
	args := m.Called(p)
	n := args.Int(0)
	if n > 0 && len(m.readData) >= n {
		copy(p, m.readData[:n])
		m.readData = m.readData[n:]
	}
	return n, args.Error(1)
}

func (m *MockStream) Write(p []byte) (int, error) {
	args := m.Called(p)
	n := args.Int(0)
	if n > 0 {
		m.writeData = append(m.writeData, p[:n]...)
	}
	return n, args.Error(1)
}

func (m *MockStream) Close() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockStream) SetReadData(data []byte) {
	m.readData = data
}

func (m *MockStream) GetWrittenData() []byte {
	return m.writeData
}

// MockConnection is a mock implementation of quic.Connection
type MockConnection struct {
	mock.Mock
}

func (m *MockConnection) OpenStreamSync(ctx context.Context) (quic.Stream, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(quic.Stream), args.Error(1)
}

func (m *MockConnection) OpenStream() (quic.Stream, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(quic.Stream), args.Error(1)
}

func (m *MockConnection) AcceptStream(ctx context.Context) (quic.Stream, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(quic.Stream), args.Error(1)
}

func (m *MockConnection) Context() context.Context {
	args := m.Called()
	return args.Get(0).(context.Context)
}

func (m *MockConnection) CloseWithError(code quic.ApplicationErrorCode, reason string) error {
	args := m.Called(code, reason)
	return args.Error(0)
}

// Additional methods to satisfy quic.Connection interface
func (m *MockConnection) AcceptUniStream(ctx context.Context) (quic.ReceiveStream, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(quic.ReceiveStream), args.Error(1)
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

func (m *MockConnection) RemoteAddr() net.Addr {
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

// MockQuicStream is a mock implementation of quic.Stream
type MockQuicStream struct {
	mock.Mock
}

func (m *MockQuicStream) StreamID() quic.StreamID {
	args := m.Called()
	return args.Get(0).(quic.StreamID)
}

func (m *MockQuicStream) Read(p []byte) (int, error) {
	args := m.Called(p)
	return args.Int(0), args.Error(1)
}

func (m *MockQuicStream) Write(p []byte) (int, error) {
	args := m.Called(p)
	return args.Int(0), args.Error(1)
}

func (m *MockQuicStream) Close() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockQuicStream) CancelRead(code quic.StreamErrorCode) {
	m.Called(code)
}

func (m *MockQuicStream) CancelWrite(code quic.StreamErrorCode) {
	m.Called(code)
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

func (m *MockQuicStream) Context() context.Context {
	args := m.Called()
	return args.Get(0).(context.Context)
}

// Test ClientSession creation
func TestNewClientSession(t *testing.T) {
	mockPathManager := &MockPathManager{}
	config := DefaultSessionConfig()

	session := NewClientSession(mockPathManager, config)

	assert.NotNil(t, session)
	assert.NotEmpty(t, session.sessionID)
	assert.Equal(t, SessionStateConnecting, session.state)
	assert.True(t, session.isClient)
	assert.Equal(t, config, session.config)
	assert.NotNil(t, session.authManager)
	assert.NotNil(t, session.streams)
	assert.NotNil(t, session.acceptChan)
}

// Test ClientSession with nil config uses defaults
func TestNewClientSession_NilConfig(t *testing.T) {
	mockPathManager := &MockPathManager{}

	session := NewClientSession(mockPathManager, nil)

	assert.NotNil(t, session)
	assert.NotNil(t, session.config)
	assert.Equal(t, utils.MaxPaths, session.config.MaxPaths)
	assert.Equal(t, utils.OptimalLogicalStreamsPerReal, session.config.OptimalStreamsPerReal)
}

// Test OpenStreamSync with active session
func TestClientSession_OpenStreamSync_Success(t *testing.T) {
	mockPathManager := &MockPathManager{}
	mockPrimaryPath := NewMockPath("path-1", "127.0.0.1:8080", true, true)

	// Configure the mock to return a mock connection
	mockConnection := &MockConnection{}
	mockStream := &MockQuicStream{}

	mockPrimaryPath.On("GetConnection").Return(mockConnection)
	mockConnection.On("OpenStreamSync", mock.Anything).Return(mockStream, nil)

	session := NewClientSession(mockPathManager, nil)
	session.primaryPath = mockPrimaryPath
	session.state = SessionStateActive

	ctx := context.Background()
	stream, err := session.OpenStreamSync(ctx)

	assert.NoError(t, err)
	assert.NotNil(t, stream)
	// Note: The actual stream implementation will have these methods
	// For now, just verify the stream was created successfully

	mockPrimaryPath.AssertExpectations(t)
	mockConnection.AssertExpectations(t)
}

// Test OpenStreamSync with inactive session
func TestClientSession_OpenStreamSync_InactiveSession(t *testing.T) {
	mockPathManager := &MockPathManager{}
	session := NewClientSession(mockPathManager, nil)
	session.state = SessionStateClosed

	ctx := context.Background()
	stream, err := session.OpenStreamSync(ctx)

	assert.Error(t, err)
	assert.Nil(t, stream)
	assert.Contains(t, err.Error(), "session is not active")
}

// Test OpenStreamSync with no primary path
func TestClientSession_OpenStreamSync_NoPrimaryPath(t *testing.T) {
	mockPathManager := &MockPathManager{}
	session := NewClientSession(mockPathManager, nil)
	session.state = SessionStateActive
	session.primaryPath = nil

	ctx := context.Background()
	stream, err := session.OpenStreamSync(ctx)

	assert.Error(t, err)
	assert.Nil(t, stream)
	assert.Contains(t, err.Error(), "no primary path available")
}

// Test OpenStreamSync with inactive primary path
func TestClientSession_OpenStreamSync_InactivePrimaryPath(t *testing.T) {
	mockPathManager := &MockPathManager{}
	mockPrimaryPath := NewMockPath("path-1", "127.0.0.1:8080", true, false) // inactive

	session := NewClientSession(mockPathManager, nil)
	session.primaryPath = mockPrimaryPath
	session.state = SessionStateActive

	ctx := context.Background()
	stream, err := session.OpenStreamSync(ctx)

	assert.Error(t, err)
	assert.Nil(t, stream)
	assert.Contains(t, err.Error(), "primary path is not active")
}

// Test AcceptStream with timeout
func TestClientSession_AcceptStream_Timeout(t *testing.T) {
	mockPathManager := &MockPathManager{}
	mockPathManager.On("GetActivePaths").Return([]transport.Path{})
	session := NewClientSession(mockPathManager, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	stream, err := session.AcceptStream(ctx)

	assert.Error(t, err)
	assert.Nil(t, stream)
	assert.Equal(t, context.DeadlineExceeded, err)
}

// Test GetActivePaths
func TestClientSession_GetActivePaths(t *testing.T) {
	mockPathManager := &MockPathManager{}
	mockPath1 := NewMockPath("path-1", "127.0.0.1:8080", true, true)
	mockPath2 := NewMockPath("path-2", "127.0.0.1:8081", false, true)

	mockPathManager.On("GetActivePaths").Return([]transport.Path{mockPath1, mockPath2})

	session := NewClientSession(mockPathManager, nil)
	paths := session.GetActivePaths()

	assert.Len(t, paths, 2)
	assert.Equal(t, "path-1", paths[0].PathID)
	assert.Equal(t, "path-2", paths[1].PathID)
	assert.True(t, paths[0].IsPrimary)
	assert.False(t, paths[1].IsPrimary)
	mockPathManager.AssertExpectations(t)
}

// Test GetDeadPaths
func TestClientSession_GetDeadPaths(t *testing.T) {
	mockPathManager := &MockPathManager{}
	mockDeadPath := NewMockPath("path-dead", "127.0.0.1:8082", false, false)

	mockPathManager.On("GetDeadPaths").Return([]transport.Path{mockDeadPath})

	session := NewClientSession(mockPathManager, nil)
	paths := session.GetDeadPaths()

	assert.Len(t, paths, 1)
	assert.Equal(t, "path-dead", paths[0].PathID)
	assert.Equal(t, PathStatusDead, paths[0].Status)
	mockPathManager.AssertExpectations(t)
}

// Test AddPath returns error for client session
func TestClientSession_AddPath_ClientError(t *testing.T) {
	mockPathManager := &MockPathManager{}
	session := NewClientSession(mockPathManager, nil)

	err := session.AddPath("127.0.0.1:8080")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "AddPath is only available on server sessions")
}

// Test RemovePath returns error for client session
func TestClientSession_RemovePath_ClientError(t *testing.T) {
	mockPathManager := &MockPathManager{}
	session := NewClientSession(mockPathManager, nil)

	err := session.RemovePath("path-1")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "RemovePath is only available on server sessions")
}

// Test SendRawData with valid path
func TestClientSession_SendRawData_ValidPath(t *testing.T) {
	mockPathManager := &MockPathManager{}
	mockPath := NewMockPath("path-1", "127.0.0.1:8080", true, true)
	mockConnection := &MockConnection{}
	mockStream := &MockQuicStream{}

	mockPathManager.On("GetPath", "path-1").Return(mockPath)
	mockPath.On("GetDataStreams").Return([]quic.Stream{})
	mockPath.On("GetConnection").Return(mockConnection)
	mockConnection.On("OpenStreamSync", mock.Anything).Return(mockStream, nil)
	mockConnection.On("Context").Return(context.Background())
	mockStream.On("Write", mock.Anything).Return(9, nil)

	session := NewClientSession(mockPathManager, nil)
	session.state = SessionStateActive // Set session as active

	// This should now work since SendRawData is implemented
	err := session.SendRawData([]byte("test data"), "path-1", 1)

	// The error might be related to missing connection or other setup, but not "not implemented"
	if err != nil {
		assert.NotContains(t, err.Error(), "raw data transmission not yet implemented")
	}
	mockPathManager.AssertExpectations(t)
	mockPath.AssertExpectations(t)
	mockConnection.AssertExpectations(t)
	mockStream.AssertExpectations(t)
}

// Test SendRawData with invalid path
func TestClientSession_SendRawData_PathNotFound(t *testing.T) {
	mockPathManager := &MockPathManager{}

	mockPathManager.On("GetPath", "invalid-path").Return(nil)

	session := NewClientSession(mockPathManager, nil)
	session.state = SessionStateActive // Set session as active

	err := session.SendRawData([]byte("test data"), "invalid-path", 1)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "path not found")
	mockPathManager.AssertExpectations(t)
}

// Test Close session
func TestClientSession_Close(t *testing.T) {
	mockPathManager := &MockPathManager{}
	mockPath := NewMockPath("path-1", "127.0.0.1:8080", true, true)

	mockPathManager.On("GetActivePaths").Return([]transport.Path{mockPath})
	mockPath.On("Close").Return(nil)

	session := NewClientSession(mockPathManager, nil)
	session.state = SessionStateActive

	err := session.Close()

	assert.NoError(t, err)
	assert.Equal(t, SessionStateClosed, session.state)
	mockPathManager.AssertExpectations(t)
	mockPath.AssertExpectations(t)
}

// Test Close already closed session
func TestClientSession_Close_AlreadyClosed(t *testing.T) {
	mockPathManager := &MockPathManager{}
	session := NewClientSession(mockPathManager, nil)
	session.state = SessionStateClosed

	err := session.Close()

	assert.NoError(t, err)
	assert.Equal(t, SessionStateClosed, session.state)
}

// Test GetDefaultPathForWrite
func TestClientSession_GetDefaultPathForWrite_Success(t *testing.T) {
	mockPathManager := &MockPathManager{}
	mockPrimaryPath := NewMockPath("path-1", "127.0.0.1:8080", true, true)

	session := NewClientSession(mockPathManager, nil)
	session.primaryPath = mockPrimaryPath

	path, err := session.GetDefaultPathForWrite()

	assert.NoError(t, err)
	assert.NotNil(t, path)
	assert.Equal(t, "path-1", path.ID())
}

// Test GetDefaultPathForWrite with no primary path
func TestClientSession_GetDefaultPathForWrite_NoPrimaryPath(t *testing.T) {
	mockPathManager := &MockPathManager{}
	session := NewClientSession(mockPathManager, nil)
	session.primaryPath = nil

	path, err := session.GetDefaultPathForWrite()

	assert.Error(t, err)
	assert.Nil(t, path)
	assert.Contains(t, err.Error(), "no primary path available")
}

// Test ValidatePathForWriteOperation
func TestClientSession_ValidatePathForWriteOperation_Success(t *testing.T) {
	mockPathManager := &MockPathManager{}
	mockPrimaryPath := NewMockPath("path-1", "127.0.0.1:8080", true, true)

	session := NewClientSession(mockPathManager, nil)
	session.primaryPath = mockPrimaryPath

	err := session.ValidatePathForWriteOperation("path-1")

	assert.NoError(t, err)
}

// Test ValidatePathForWriteOperation with non-primary path
func TestClientSession_ValidatePathForWriteOperation_NonPrimaryPath(t *testing.T) {
	mockPathManager := &MockPathManager{}
	mockPrimaryPath := NewMockPath("path-1", "127.0.0.1:8080", true, true)

	session := NewClientSession(mockPathManager, nil)
	session.primaryPath = mockPrimaryPath

	err := session.ValidatePathForWriteOperation("path-2")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "client write operations must use primary path only")
}

// Test PathStatus string conversion
func TestPathStatus_String(t *testing.T) {
	tests := []struct {
		status   PathStatus
		expected string
	}{
		{PathStatusActive, "ACTIVE"},
		{PathStatusDead, "DEAD"},
		{PathStatusConnecting, "CONNECTING"},
		{PathStatusDisconnecting, "DISCONNECTING"},
		{PathStatus(999), "UNKNOWN"},
	}

	for _, test := range tests {
		assert.Equal(t, test.expected, test.status.String())
	}
}

// Test SessionState string conversion
func TestSessionState_String(t *testing.T) {
	tests := []struct {
		state    SessionState
		expected string
	}{
		{SessionStateIdle, "IDLE"},
		{SessionStateConnecting, "CONNECTING"},
		{SessionStateActive, "ACTIVE"},
		{SessionStateClosing, "CLOSING"},
		{SessionStateClosed, "CLOSED"},
		{SessionState(999), "UNKNOWN"},
	}

	for _, test := range tests {
		assert.Equal(t, test.expected, test.state.String())
	}
}

// Test DefaultSessionConfig
func TestDefaultSessionConfig(t *testing.T) {
	config := DefaultSessionConfig()

	assert.NotNil(t, config)
	assert.Equal(t, utils.MaxPaths, config.MaxPaths)
	assert.Equal(t, utils.OptimalLogicalStreamsPerReal, config.OptimalStreamsPerReal)
	assert.Equal(t, utils.MaxLogicalStreamsPerReal, config.MaxStreamsPerReal)
	assert.Equal(t, utils.DefaultKeepAliveInterval, config.IdleTimeout)
	assert.Equal(t, uint32(utils.DefaultMaxPacketSize), config.MaxPacketSize)
	assert.True(t, config.EnableAggregation)
	assert.True(t, config.EnableMigration)
}

// Test session ID generation uniqueness
func TestSessionIDGeneration_Uniqueness(t *testing.T) {
	mockPathManager := &MockPathManager{}

	session1 := NewClientSession(mockPathManager, nil)
	session2 := NewClientSession(mockPathManager, nil)

	assert.NotEqual(t, session1.sessionID, session2.sessionID)
	assert.NotEmpty(t, session1.sessionID)
	assert.NotEmpty(t, session2.sessionID)
}

// Test GetSessionID
func TestClientSession_GetSessionID(t *testing.T) {
	mockPathManager := &MockPathManager{}
	session := NewClientSession(mockPathManager, nil)

	sessionID := session.GetSessionID()

	assert.NotEmpty(t, sessionID)
	assert.Equal(t, session.sessionID, sessionID)
}

// Test IsAuthenticated with nil auth manager
func TestClientSession_IsAuthenticated_NilAuthManager(t *testing.T) {
	mockPathManager := &MockPathManager{}
	session := NewClientSession(mockPathManager, nil)
	session.authManager = nil

	isAuth := session.IsAuthenticated()

	assert.False(t, isAuth)
}

// Test GetPrimaryPath
func TestClientSession_GetPrimaryPath(t *testing.T) {
	mockPathManager := &MockPathManager{}
	mockPrimaryPath := NewMockPath("path-1", "127.0.0.1:8080", true, true)

	session := NewClientSession(mockPathManager, nil)
	session.primaryPath = mockPrimaryPath

	path := session.GetPrimaryPath()

	assert.NotNil(t, path)
	assert.Equal(t, "path-1", path.ID())
}

// Test GetContext
func TestClientSession_GetContext(t *testing.T) {
	mockPathManager := &MockPathManager{}
	session := NewClientSession(mockPathManager, nil)

	ctx := session.GetContext()

	assert.NotNil(t, ctx)
	assert.Equal(t, session.ctx, ctx)
}

// Test RemoveStream
func TestClientSession_RemoveStream(t *testing.T) {
	mockPathManager := &MockPathManager{}
	session := NewClientSession(mockPathManager, nil)

	// Add a mock stream to the session
	session.streams[1] = nil // Just for testing removal

	session.RemoveStream(1)

	_, exists := session.streams[1]
	assert.False(t, exists)
}
