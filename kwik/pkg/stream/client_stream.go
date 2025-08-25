package stream

import (
	"context"
	"fmt"
	"sync"
	"time"

	"kwik/internal/utils"
	"kwik/pkg/data"

	"github.com/quic-go/quic-go"
)

// ClientSession interface to avoid circular imports
type ClientSession interface {
	GetContext() context.Context
	RemoveStream(streamID uint64)
	ValidatePathForWriteOperation(pathID string) error
	GetAggregatedDataForStream(streamID uint64) ([]byte, error)
	ConsumeAggregatedDataForStream(streamID uint64, bytesToConsume int) ([]byte, error)
	AggregateData(streamID uint64, data []byte, offset uint64) error
	HandlePrimaryPathDataPacket(pathID string, quicStream quic.Stream)
	// New methods for data presentation manager integration
	GetDataPresentationManager() DataPresentationManager

	// Expose metadata protocol for encapsulation/decapsulation
	GetMetadataProtocol() *MetadataProtocolImpl

	// Expose offset coordinator for offset management
	GetOffsetCoordinator() data.OffsetCoordinator

	// Logger access for streams
	GetLogger() StreamLogger
}

// DataPresentationManager interface to avoid circular imports
type DataPresentationManager interface {
	ReadFromStream(streamID uint64, buffer []byte) (int, error)
	SetStreamTimeout(streamID uint64, timeout time.Duration) error
	WriteToStream(streamID uint64, data []byte, offset uint64, metadata interface{}) error
	CreateStreamBuffer(streamID uint64, metadata interface{}) error
	IsBackpressureActive(streamID uint64) bool
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

	// Secondary stream isolation fields
	offset         int    // Current offset for stream data aggregation
	remoteStreamID uint64 // Remote stream ID for cross-stream references

	// Application stream flag: true if created via OpenStreamSync/OpenStream
	isAppStream bool

	// Logger
	logger StreamLogger

	// Internal primary ingestion state
	primaryWriteOffset uint64
}

// StreamLogger provides minimal logging for streams
// Kept local to avoid import cycles with the root kwik package
type StreamLogger interface {
	Debug(msg string, keysAndValues ...interface{})
	Info(msg string, keysAndValues ...interface{})
	Warn(msg string, keysAndValues ...interface{})
	Error(msg string, keysAndValues ...interface{})
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
		logger:   session.GetLogger(),
	}
}

// NewClientStreamWithQuic creates a new client stream with an underlying QUIC stream
func NewClientStreamWithQuic(id uint64, pathID string, session ClientSession, quicStream quic.Stream) *ClientStream {
	cs := &ClientStream{
		id:          id,
		pathID:      pathID,
		session:     session,
		created:     time.Now(),
		state:       StreamStateOpen,
		quicStream:  quicStream, // Store the underlying QUIC stream
		isAppStream: true,
		logger:      session.GetLogger(),
	}
	return cs
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

	// Always use DataPresentationManager; streams must be presented only here
	if dpm := s.session.GetDataPresentationManager(); dpm != nil {
		err := dpm.SetStreamTimeout(s.id, 3*time.Second)
		if err != nil {
			return 0, utils.NewKwikError(utils.ErrStreamReadTimeout, "failed to set stream timeout", err)
		}
		return dpm.ReadFromStream(s.id, p)
	}

	// If DPM is unavailable, return no data (design decision: presentation-only)
	return 0, utils.NewKwikError(utils.ErrStreamCreationFailed, "no data presentation manager available", nil)
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

	// Reserve offset range for coordinated writing
	var reservedOffset int64 = -1
	if oc := s.session.GetOffsetCoordinator(); oc != nil {
		var err error
		reservedOffset, err = oc.ReserveOffsetRange(s.id, len(p))
		if err != nil {
			s.logger.Warn("Failed to reserve offset range", "streamID", s.id, "size", len(p), "error", err)
			// Continue with current offset if reservation fails
			reservedOffset = int64(s.offset)
		}
	} else {
		// Use current offset if no coordinator available
		reservedOffset = int64(s.offset)
	}

	// Try to encapsulate data with metadata carrying the KWIK Stream ID and reserved offset
	if mp := s.session.GetMetadataProtocol(); mp != nil {
		encapsulated, err := mp.EncapsulateData(
			uint64(s.id), // KWIK Stream ID (source stream ID)
			uint64(s.id), // Secondary stream ID (not serialized; placeholder)
			uint64(reservedOffset),
			p,
		)
		if err == nil {
			// Write encapsulated frame
			if _, werr := s.quicStream.Write(encapsulated); werr != nil {
				// If write fails, we should not commit the offset range
				return 0, werr
			}
			
			// Commit the offset range after successful write
			if oc := s.session.GetOffsetCoordinator(); oc != nil {
				commitErr := oc.CommitOffsetRange(s.id, reservedOffset, len(p))
				if commitErr != nil {
					s.logger.Warn("Failed to commit offset range", "streamID", s.id, "offset", reservedOffset, "size", len(p), "error", commitErr)
				}
			}
			
			// Update local offset to match the reserved offset
			s.offset = int(reservedOffset) + len(p)
			return len(p), nil
		}
		// If encapsulation fails, fall back to raw write
	}
	return 0, fmt.Errorf("metadata protocol not found")
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

// SetOffset sets the current offset for stream data aggregation
// This is used by the client to track where data should be placed in aggregated streams
func (s *ClientStream) SetOffset(offset int) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if offset < 0 {
		return &StreamError{
			Code:    "KWIK_INVALID_OFFSET",
			Message: "offset cannot be negative",
		}
	}

	s.offset = offset

	// If this stream is part of secondary stream aggregation, update the aggregation system
	if s.remoteStreamID != 0 {
		return s.updateAggregationOffset()
	}

	return nil
}

// GetOffset returns the current offset for stream data aggregation
func (s *ClientStream) GetOffset() int {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.offset
}

// SetRemoteStreamID sets the remote stream ID for cross-stream references
// For client streams, this represents the server-side stream ID for mapping
func (s *ClientStream) SetRemoteStreamID(remoteStreamID uint64) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if remoteStreamID == 0 {
		return &StreamError{
			Code:    "KWIK_INVALID_REMOTE_STREAM_ID",
			Message: "remote stream ID cannot be zero",
		}
	}

	s.remoteStreamID = remoteStreamID

	// Set up aggregation mapping if offset is also set
	if s.offset >= 0 {
		return s.updateAggregationOffset()
	}

	return nil
}

// RemoteStreamID returns the remote stream ID for cross-stream references
func (s *ClientStream) RemoteStreamID() uint64 {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.remoteStreamID
}

// updateAggregationOffset updates the aggregation system with current offset information
func (s *ClientStream) updateAggregationOffset() error {
	// This would integrate with the secondary stream aggregation system
	// For now, we'll log the mapping for debugging since we need access to the session
	// to get the aggregator, but ClientStream doesn't have direct access to ClientSession

	s.logger.Info(fmt.Sprintf("[CLIENT] Stream %d mapped to remote stream %d at offset %d (ready for aggregation)",
		s.id, s.remoteStreamID, s.offset))

	// Note: In a full implementation, this would:
	// 1. Get the aggregator from the session via s.session.getSecondaryStreamAggregator()
	// 2. Call aggregator.SetStreamMapping(s.id, s.remoteStreamID, s.pathID)
	// 3. Update offset information for proper data placement
	//
	// However, since ClientStream doesn't have direct access to ClientSession,
	// this mapping will be handled when data is actually received and processed

	return nil
}

// StreamError represents an error in stream operations
type StreamError struct {
	Code    string
	Message string
}

func (e *StreamError) Error() string {
	return e.Code + ": " + e.Message
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
