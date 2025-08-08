package stream

import (
	"context"
	"sync"
	"time"

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
	id       uint64
	pathID   string
	session  ClientSession
	created  time.Time
	state    StreamState
	readBuf  []byte
	writeBuf []byte
	mutex    sync.RWMutex
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

// Read reads data from the stream (QUIC-compatible)
func (s *ClientStream) Read(p []byte) (int, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	
	// Check stream state for QUIC compatibility
	switch s.state {
	case StreamStateClosed, StreamStateResetReceived:
		return 0, utils.NewKwikError(utils.ErrStreamCreationFailed, "stream is closed", nil)
	case StreamStateHalfClosedRemote:
		// Can still read remaining data, but no new data will arrive
		if len(s.readBuf) == 0 {
			return 0, nil // EOF - no more data
		}
	case StreamStateResetSent:
		return 0, utils.NewKwikError(utils.ErrStreamCreationFailed, "stream was reset", nil)
	}
	
	// Return available data from buffer
	if len(s.readBuf) == 0 {
		return 0, nil // No data available, would block in real implementation
	}
	
	// Copy data to user buffer
	n := copy(p, s.readBuf)
	s.readBuf = s.readBuf[n:]
	
	// TODO: Implement actual data reading from aggregated paths
	// This should:
	// 1. Read data from multiple paths via data plane
	// 2. Aggregate and reorder data based on KWIK logical offsets
	// 3. Return properly ordered data to application
	// 4. Handle flow control and congestion control
	
	return n, nil
}

// Write writes data to the stream (QUIC-compatible)
// Client writes MUST go through primary path only
func (s *ClientStream) Write(p []byte) (int, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	
	// Check stream state for QUIC compatibility
	switch s.state {
	case StreamStateClosed, StreamStateResetSent, StreamStateResetReceived:
		return 0, utils.NewKwikError(utils.ErrStreamCreationFailed, "stream is closed", nil)
	case StreamStateHalfClosedLocal:
		return 0, utils.NewKwikError(utils.ErrStreamCreationFailed, "stream is half-closed for writing", nil)
	}
	
	// Validate input
	if len(p) == 0 {
		return 0, nil // Nothing to write
	}
	
	// Check if write would exceed buffer limits (QUIC-like flow control)
	if len(s.writeBuf)+len(p) > utils.DefaultWriteBufferSize {
		return 0, utils.NewKwikError(utils.ErrStreamCreationFailed, "write buffer full", nil)
	}
	
	// Ensure write operation uses primary path (Requirement 4.3)
	// Client writes must always go to primary server via primary path
	err := s.session.ValidatePathForWriteOperation(s.pathID)
	if err != nil {
		return 0, utils.NewKwikError(utils.ErrInvalidFrame,
			"write operation validation failed", err)
	}
	
	// TODO: Implement actual data writing to primary path data plane
	// This should:
	// 1. Send data only to primary path via data plane
	// 2. Use proper KWIK logical offsets
	// 3. Handle flow control and congestion control
	// 4. Fragment large writes if necessary
	
	s.writeBuf = append(s.writeBuf, p...)
	
	return len(p), nil
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