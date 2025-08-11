package stream

import (
	"context"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"kwik/internal/utils"
)

// StreamState represents the state of a KWIK stream
type StreamState int

const (
	StreamStateIdle StreamState = iota
	StreamStateOpen
	StreamStateHalfClosedLocal
	StreamStateHalfClosedRemote
	StreamStateClosed
	StreamStateResetSent
	StreamStateResetReceived
)

// ClientSession interface to avoid circular imports
type ClientSession interface {
	GetContext() context.Context
	RemoveStream(streamID uint64)
	ValidatePathForWriteOperation(pathID string) error
}

// PathRouter interface to avoid circular imports
type PathRouter interface {
	GetDefaultPathForWrite() (Path, error)
	ValidatePathForOperation(pathID string, operation OperationType) error
}

// Path interface to avoid circular imports
type Path interface {
	ID() string
}

// OperationType defines the type of operation being performed
type OperationType int

const (
	OperationTypeRead OperationType = iota
	OperationTypeWrite
	OperationTypeStream
	OperationTypeControl
)

// ClientStream represents a client-side KWIK stream
type ClientStream struct {
	id         uint64
	pathID     string
	session    ClientSession
	created    time.Time
	state      StreamState
	readBuf    []byte
	writeBuf   []byte
	quicStream quic.Stream // Underlying QUIC stream
	mutex      sync.RWMutex
}

// NewClientStream creates a new client stream
func NewClientStream(id uint64, pathID string, session ClientSession) *ClientStream {
	return &ClientStream{
		id:       id,
		pathID:   pathID,
		session:  session,
		created:  time.Now(),
		state:    StreamStateOpen,
		readBuf:  make([]byte, 0, utils.DefaultReadBufferSize),
		writeBuf: make([]byte, 0, utils.DefaultWriteBufferSize),
	}
}

// NewClientStreamWithQuic creates a new client stream with an underlying QUIC stream
func NewClientStreamWithQuic(id uint64, pathID string, session ClientSession, quicStream quic.Stream) *ClientStream {
	return &ClientStream{
		id:         id,
		pathID:     pathID,
		session:    session,
		created:    time.Now(),
		state:      StreamStateOpen,
		quicStream: quicStream, // Store the underlying QUIC stream
	}
}

// Read reads data from the stream (QUIC-compatible)
func (s *ClientStream) Read(p []byte) (int, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	
	// Check stream state for QUIC compatibility
	switch s.state {
	case StreamStateClosed, StreamStateResetSent, StreamStateResetReceived:
		return 0, utils.NewKwikError(utils.ErrStreamClosed, "stream is closed", nil)
	case StreamStateHalfClosedRemote:
		return 0, utils.NewKwikError(utils.ErrStreamClosed, "stream is half-closed for reading", nil)
	}
	
	// Check if we have a QUIC stream to read from
	if s.quicStream == nil {
		return 0, utils.NewKwikError(utils.ErrStreamCreationFailed, "no underlying QUIC stream", nil)
	}
	
	// Read directly from the underlying QUIC stream
	return s.quicStream.Read(p)
}

// Write writes data to the stream (QUIC-compatible)
// Client writes MUST go through primary path only
func (s *ClientStream) Write(p []byte) (int, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	
	// Check stream state for QUIC compatibility
	switch s.state {
	case StreamStateClosed, StreamStateResetSent, StreamStateResetReceived:
		return 0, utils.NewKwikError(utils.ErrStreamClosed, "stream is closed", nil)
	case StreamStateHalfClosedLocal:
		return 0, utils.NewKwikError(utils.ErrStreamClosed, "stream is half-closed for writing", nil)
	}
	
	// Validate input
	if len(p) == 0 {
		return 0, nil // Nothing to write
	}
	
	// Check if single write would exceed reasonable limits (QUIC-like flow control)
	// Allow large writes but prevent extremely large ones that could cause memory issues
	if len(p) > 10*utils.DefaultWriteBufferSize {
		return 0, utils.NewKwikError(utils.ErrStreamCreationFailed, "write too large", nil)
	}
	
	// Check if we have a QUIC stream to write to
	if s.quicStream == nil {
		return 0, utils.NewKwikError(utils.ErrStreamCreationFailed, "no underlying QUIC stream", nil)
	}
	
	// Write directly to the underlying QUIC stream
	return s.quicStream.Write(p)
}

// Close closes the stream (QUIC-compatible)
func (s *ClientStream) Close() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	
	if s.state == StreamStateClosed {
		return nil // Already closed
	}
	
	s.state = StreamStateClosed
	
	// Remove stream from session
	s.session.RemoveStream(s.id)
	
	return nil
}

// StreamID returns the logical stream ID
func (s *ClientStream) StreamID() uint64 {
	return s.id
}

// PathID returns the primary path ID for this stream
func (s *ClientStream) PathID() string {
	return s.pathID
}

// GetState returns the current stream state
func (s *ClientStream) GetState() StreamState {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.state
}

// SetState sets the stream state
func (s *ClientStream) SetState(state StreamState) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.state = state
}

// String returns a string representation of the stream state
func (s StreamState) String() string {
	switch s {
	case StreamStateIdle:
		return "IDLE"
	case StreamStateOpen:
		return "OPEN"
	case StreamStateHalfClosedLocal:
		return "HALF_CLOSED_LOCAL"
	case StreamStateHalfClosedRemote:
		return "HALF_CLOSED_REMOTE"
	case StreamStateClosed:
		return "CLOSED"
	case StreamStateResetSent:
		return "RESET_SENT"
	case StreamStateResetReceived:
		return "RESET_RECEIVED"
	default:
		return "UNKNOWN"
	}
}

// Additional utility methods for QUIC compatibility

// SetDeadline sets the read and write deadlines (QUIC-compatible interface)
// Note: This is a placeholder for future implementation
func (s *ClientStream) SetDeadline(t time.Time) error {
	// TODO: Implement deadline support for QUIC compatibility
	// This would set both read and write deadlines
	return nil
}

// SetReadDeadline sets the read deadline (QUIC-compatible interface)
// Note: This is a placeholder for future implementation
func (s *ClientStream) SetReadDeadline(t time.Time) error {
	// TODO: Implement read deadline support
	return nil
}

// SetWriteDeadline sets the write deadline (QUIC-compatible interface)
// Note: This is a placeholder for future implementation
func (s *ClientStream) SetWriteDeadline(t time.Time) error {
	// TODO: Implement write deadline support
	return nil
}

// Context returns the stream's context (QUIC-compatible interface)
func (s *ClientStream) Context() context.Context {
	return s.session.GetContext()
}

// CancelWrite cancels the write side of the stream (QUIC-compatible)
func (s *ClientStream) CancelWrite(errorCode uint64) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	
	switch s.state {
	case StreamStateOpen:
		s.state = StreamStateHalfClosedLocal
	case StreamStateHalfClosedRemote:
		s.state = StreamStateClosed
	}
	
	// TODO: Send RESET_STREAM frame via control plane
}

// CancelRead cancels the read side of the stream (QUIC-compatible)
func (s *ClientStream) CancelRead(errorCode uint64) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	
	switch s.state {
	case StreamStateOpen:
		s.state = StreamStateHalfClosedRemote
	case StreamStateHalfClosedLocal:
		s.state = StreamStateClosed
	}
	
	// TODO: Send STOP_SENDING frame via control plane
}