package utils

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"kwik/pkg/session"
)

// MockSession implements the session.Session interface for testing
type MockSession struct {
	streams       []MockStream
	streamIndex   int
	rawDataSent   []RawDataMessage
	activePaths   []session.PathInfo
	closed        bool
	openStreamErr error
	acceptErr     error
	sendRawErr    error
	mutex         sync.Mutex
}

type RawDataMessage struct {
	Data   []byte
	PathID string
}

type MockStream struct {
	data     []byte
	readPos  int
	closed   bool
	streamID uint64
	pathID   string
	writeErr error
	readErr  error
	mutex    sync.Mutex
}

func NewMockSession() *MockSession {
	return &MockSession{
		streams: make([]MockStream, 0),
		activePaths: []session.PathInfo{
			{
				PathID:    "primary",
				Address:   "127.0.0.1:8080",
				IsPrimary: true,
				Status:    session.PathStatusActive,
				CreatedAt: time.Now(),
			},
			{
				PathID:    "secondary",
				Address:   "127.0.0.1:8081",
				IsPrimary: false,
				Status:    session.PathStatusActive,
				CreatedAt: time.Now(),
			},
		},
	}
}

func (ms *MockSession) OpenStreamSync(ctx context.Context) (session.Stream, error) {
	ms.mutex.Lock()
	defer ms.mutex.Unlock()

	if ms.openStreamErr != nil {
		return nil, ms.openStreamErr
	}

	stream := &MockStream{
		streamID: uint64(len(ms.streams)),
		pathID:   "primary",
		data:     make([]byte, 0),
		closed:   false,
	}

	// Store a copy without the mutex to avoid copying lock value
	streamCopy := MockStream{
		streamID: stream.streamID,
		pathID:   stream.pathID,
		data:     make([]byte, 0),
		closed:   false,
	}
	ms.streams = append(ms.streams, streamCopy)
	return stream, nil
}

func (ms *MockSession) AcceptStream(ctx context.Context) (session.Stream, error) {
	ms.mutex.Lock()
	defer ms.mutex.Unlock()

	if ms.acceptErr != nil {
		return nil, ms.acceptErr
	}

	// Simulate waiting for incoming stream
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(10 * time.Millisecond):
		// Return a mock stream with chunk data
		stream := &MockStream{
			streamID: uint64(len(ms.streams) + 100),
			pathID:   "secondary",
			data:     make([]byte, 0),
			closed:   false,
		}
		return stream, nil
	}
}

func (ms *MockSession) SendRawData(data []byte, pathID string, remoteStreamID uint64) error {
	ms.mutex.Lock()
	defer ms.mutex.Unlock()

	if ms.sendRawErr != nil {
		return ms.sendRawErr
	}

	ms.rawDataSent = append(ms.rawDataSent, RawDataMessage{
		Data:   data,
		PathID: pathID,
	})
	return nil
}

func (ms *MockSession) GetActivePaths() []session.PathInfo {
	ms.mutex.Lock()
	defer ms.mutex.Unlock()

	return ms.activePaths
}

func (ms *MockSession) Close() error {
	ms.mutex.Lock()
	defer ms.mutex.Unlock()

	ms.closed = true
	return nil
}

// AddPath implements the session.Session interface
func (ms *MockSession) AddPath(address string) error {
	ms.mutex.Lock()
	defer ms.mutex.Unlock()

	// Add a new path to the mock session
	newPath := session.PathInfo{
		PathID:    fmt.Sprintf("path-%d", len(ms.activePaths)),
		Address:   address,
		IsPrimary: false,
		Status:    session.PathStatusActive,
		CreatedAt: time.Now(),
	}
	ms.activePaths = append(ms.activePaths, newPath)
	return nil
}

// GetAllPaths implements the session.Session interface
func (ms *MockSession) GetAllPaths() []session.PathInfo {
	ms.mutex.Lock()
	defer ms.mutex.Unlock()
	return ms.activePaths
}

// RemovePath implements the session.Session interface
func (ms *MockSession) RemovePath(pathID string) error {
	ms.mutex.Lock()
	defer ms.mutex.Unlock()
	
	// Remove path with matching ID
	for i, path := range ms.activePaths {
		if path.PathID == pathID {
			ms.activePaths = append(ms.activePaths[:i], ms.activePaths[i+1:]...)
			break
		}
	}
	return nil
}

// GetDeadPaths implements the session.Session interface
func (ms *MockSession) GetDeadPaths() []session.PathInfo {
	ms.mutex.Lock()
	defer ms.mutex.Unlock()
	
	// Return empty slice for mock - no dead paths in tests
	return []session.PathInfo{}
}

// OpenStream implements the session.Session interface (non-sync version)
func (ms *MockSession) OpenStream() (session.Stream, error) {
	// Just call the sync version with a background context
	return ms.OpenStreamSync(context.Background())
}

func (ms *MockStream) Read(p []byte) (int, error) {
	ms.mutex.Lock()
	defer ms.mutex.Unlock()

	if ms.readErr != nil {
		return 0, ms.readErr
	}

	if ms.closed {
		return 0, errors.New("stream closed")
	}

	if ms.readPos >= len(ms.data) {
		// Simulate waiting for data
		time.Sleep(10 * time.Millisecond)
		return 0, nil
	}

	n := copy(p, ms.data[ms.readPos:])
	ms.readPos += n
	return n, nil
}

func (ms *MockStream) Write(p []byte) (int, error) {
	ms.mutex.Lock()
	defer ms.mutex.Unlock()

	if ms.writeErr != nil {
		return 0, ms.writeErr
	}

	if ms.closed {
		return 0, errors.New("stream closed")
	}

	ms.data = append(ms.data, p...)
	return len(p), nil
}

func (ms *MockStream) Close() error {
	ms.mutex.Lock()
	defer ms.mutex.Unlock()

	ms.closed = true
	return nil
}

func (ms *MockStream) StreamID() uint64 {
	return ms.streamID
}

func (ms *MockStream) PathID() string {
	return ms.pathID
}

// SetData sets the data that will be returned by Read operations
func (ms *MockStream) SetData(data []byte) {
	ms.mutex.Lock()
	defer ms.mutex.Unlock()

	ms.data = data
	ms.readPos = 0
}

// GetData returns the current data in the stream
func (ms *MockStream) GetData() []byte {
	ms.mutex.Lock()
	defer ms.mutex.Unlock()
	return ms.data
}

// SetStreamID sets the stream ID for the mock stream
func (ms *MockStream) SetStreamID(id uint64) {
	ms.mutex.Lock()
	defer ms.mutex.Unlock()
	ms.streamID = id
}

// SetPathID sets the path ID for the mock stream
func (ms *MockStream) SetPathID(pathID string) {
	ms.mutex.Lock()
	defer ms.mutex.Unlock()
	ms.pathID = pathID
}

// SetClosed sets the closed state for the mock stream
func (ms *MockStream) SetClosed(closed bool) {
	ms.mutex.Lock()
	defer ms.mutex.Unlock()
	ms.closed = closed
}

// IsClosed returns whether the stream is closed
func (ms *MockStream) IsClosed() bool {
	ms.mutex.Lock()
	defer ms.mutex.Unlock()
	return ms.closed
}

// SetWriteError sets an error to be returned by Write operations
func (ms *MockStream) SetWriteError(err error) {
	ms.mutex.Lock()
	defer ms.mutex.Unlock()
	ms.writeErr = err
}

// SetReadError sets an error to be returned by Read operations
func (ms *MockStream) SetReadError(err error) {
	ms.mutex.Lock()
	defer ms.mutex.Unlock()
	ms.readErr = err
}

// Public methods to access unexported fields for testing

// SetActivePaths sets the active paths for the mock session
func (ms *MockSession) SetActivePaths(paths []session.PathInfo) {
	ms.mutex.Lock()
	defer ms.mutex.Unlock()
	ms.activePaths = paths
}

// GetRawDataSent returns the raw data messages sent through the session
func (ms *MockSession) GetRawDataSent() []RawDataMessage {
	ms.mutex.Lock()
	defer ms.mutex.Unlock()
	return ms.rawDataSent
}

// ClearRawDataSent clears the raw data sent history
func (ms *MockSession) ClearRawDataSent() {
	ms.mutex.Lock()
	defer ms.mutex.Unlock()
	ms.rawDataSent = make([]RawDataMessage, 0)
}

// SetOpenStreamError sets an error to be returned by OpenStreamSync
func (ms *MockSession) SetOpenStreamError(err error) {
	ms.mutex.Lock()
	defer ms.mutex.Unlock()
	ms.openStreamErr = err
}

// SetAcceptError sets an error to be returned by AcceptStream
func (ms *MockSession) SetAcceptError(err error) {
	ms.mutex.Lock()
	defer ms.mutex.Unlock()
	ms.acceptErr = err
}

// SetSendRawError sets an error to be returned by SendRawData
func (ms *MockSession) SetSendRawError(err error) {
	ms.mutex.Lock()
	defer ms.mutex.Unlock()
	ms.sendRawErr = err
}

func (s *MockStream) GetOffset () int {
	return 0;
}

func (s *MockStream) RemoteStreamID () uint64 {
	return 0;
}

func (s *MockStream) SetOffset (int) error { return nil }

func (s *MockStream) SetRemoteStreamID (uint64) error { return nil }