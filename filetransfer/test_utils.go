package filetransfer

import (
	"context"
	"errors"
	"sync"
	"time"
)

// MockSession implements the Session interface for testing
type MockSession struct {
	streams       []MockStream
	streamIndex   int
	rawDataSent   []RawDataMessage
	activePaths   []PathInfo
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
		activePaths: []PathInfo{
			{
				PathID:    "primary",
				Address:   "127.0.0.1:8080",
				IsPrimary: true,
				Status:    PathStatusActive,
				CreatedAt: time.Now(),
			},
			{
				PathID:    "secondary",
				Address:   "127.0.0.1:8081",
				IsPrimary: false,
				Status:    PathStatusActive,
				CreatedAt: time.Now(),
			},
		},
	}
}

func (ms *MockSession) OpenStreamSync(ctx context.Context) (Stream, error) {
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

	ms.streams = append(ms.streams, *stream)
	return stream, nil
}

func (ms *MockSession) AcceptStream(ctx context.Context) (Stream, error) {
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

func (ms *MockSession) SendRawData(data []byte, pathID string) error {
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

func (ms *MockSession) GetActivePaths() []PathInfo {
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
