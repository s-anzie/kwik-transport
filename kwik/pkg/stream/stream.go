package stream

import (
	"context"
	"fmt"
	"sync"
	"time"

	"kwik/internal/utils"

	"github.com/quic-go/quic-go"
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
	GetAggregatedDataForStream(streamID uint64) ([]byte, error)
	ConsumeAggregatedDataForStream(streamID uint64, bytesToConsume int) ([]byte, error)
	DepositPrimaryDataInAggregator(streamID uint64, data []byte, offset uint64) error

	// New methods for data presentation manager integration
	GetDataPresentationManager() DataPresentationManager
	ReadFromPresentationStream(streamID uint64, buffer []byte, timeout time.Duration) (int, error)

	// Expose metadata protocol for encapsulation/decapsulation
	GetMetadataProtocol() *MetadataProtocolImpl
}

// DataPresentationManager interface to avoid circular imports
type DataPresentationManager interface {
	ReadFromStreamWithTimeout(streamID uint64, buffer []byte, timeout time.Duration) (int, error)
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

	// Always use DataPresentationManager; streams must be presented only here
	if dpm := s.session.GetDataPresentationManager(); dpm != nil {
		return s.session.ReadFromPresentationStream(s.id, p, 5*time.Second)
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

	// Try to encapsulate data with metadata carrying the KWIK Stream ID and current offset
	if mp := s.session.GetMetadataProtocol(); mp != nil {
		encapsulated, err := mp.EncapsulateData(
			uint64(s.id), // KWIK Stream ID (source stream ID)
			uint64(s.id), // Secondary stream ID (not serialized; placeholder)
			uint64(s.offset),
			p,
		)
		if err == nil {
			// Write encapsulated frame
			if _, werr := s.quicStream.Write(encapsulated); werr != nil {
				return 0, werr
			}
			// Update offset by application payload length
			s.offset += len(p)
			return len(p), nil
		}
		// If encapsulation fails, fall back to raw write
	}

	// Fallback: Write directly to the underlying QUIC stream
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

	fmt.Printf("[CLIENT] Stream %d mapped to remote stream %d at offset %d (ready for aggregation)\n",
		s.id, s.remoteStreamID, s.offset)

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

// readAggregatedData reads aggregated data from secondary streams for this KWIK stream
func (s *ClientStream) readAggregatedData() ([]byte, error) {
	// Use the session interface to get aggregated data for this stream
	return s.session.GetAggregatedDataForStream(s.id)
}

// readWithGapHandling reads data with proper gap handling and timeout
// This implements the requirement: "l'application ne peut lire que si les données contigues suivantes sont disponibles
// et elle ne peut lire que si les données totales disponibles font au moins la taille d'un paquet de donnée et sans gap"
func (s *ClientStream) readWithGapHandling(p []byte) (int, error) {
	const readTimeout = 5 * time.Second         // Timeout for gap filling
	const retryInterval = 50 * time.Millisecond // Interval between gap checks
	const minPacketSize = 1                     // Minimum packet size to read

	startTime := time.Now()

	// Track our current read position in the stream
	if s.offset < 0 {
		s.offset = 0 // Initialize offset if not set
	}

	for {
		// Build a unified view of all available data (primary + secondary)
		unifiedBuffer, err := s.buildUnifiedBuffer()
		if err != nil {
			fmt.Printf("DEBUG: ClientStream %d failed to build unified buffer: %v\n", s.id, err)
		}

		// Check for contiguous data starting from our current read position
		contiguousLength := s.findContiguousDataLength(unifiedBuffer, s.offset)

		fmt.Printf("DEBUG: ClientStream %d at offset %d: unified buffer length=%d, contiguous length=%d\n",
			s.id, s.offset, len(unifiedBuffer), contiguousLength)

		// Check if we have enough contiguous data to read (at least minPacketSize)
		if contiguousLength >= minPacketSize {
			// We have enough contiguous data to read
			maxRead := contiguousLength
			if maxRead > len(p) {
				maxRead = len(p) // Don't exceed the provided buffer size
			}

			// Copy the contiguous data
			n := copy(p, unifiedBuffer[s.offset:s.offset+maxRead])
			s.offset += n // Update our read position

			fmt.Printf("DEBUG: ClientStream %d read %d bytes of contiguous data at offset %d\n",
				s.id, n, s.offset-n)
			return n, nil
		}

		// No sufficient contiguous data available, check if we've exceeded timeout
		if time.Since(startTime) > readTimeout {
			fmt.Printf("DEBUG: ClientStream %d read timeout after %v - insufficient contiguous data at offset %d (need %d, have %d)\n",
				s.id, time.Since(startTime), s.offset, minPacketSize, contiguousLength)
			return 0, utils.NewKwikError(utils.ErrStreamClosed, "read timeout: insufficient contiguous data", nil)
		}

		// Wait before retrying
		time.Sleep(retryInterval)

		// Check if the session context is cancelled
		select {
		case <-s.session.GetContext().Done():
			return 0, s.session.GetContext().Err()
		default:
			// Continue trying
		}
	}
}

// buildUnifiedBuffer builds a unified buffer combining primary and secondary stream data
func (s *ClientStream) buildUnifiedBuffer() ([]byte, error) {
	// Get aggregated data from secondary streams (this includes data from all secondary servers)
	aggregatedData, err := s.readAggregatedData()
	if err != nil {
		aggregatedData = []byte{} // Use empty slice if no aggregated data
	}

	// Try to read any available data from the primary QUIC stream without blocking
	primaryData := s.readAvailablePrimaryData()

	// Create a unified buffer that combines both data sources
	// The aggregated data already has correct offset positioning
	// Primary data starts at offset 0

	maxLength := len(aggregatedData)
	if len(primaryData) > maxLength {
		maxLength = len(primaryData)
	}

	if maxLength == 0 {
		return []byte{}, nil
	}

	// Create unified buffer
	unifiedBuffer := make([]byte, maxLength)

	// First, copy aggregated data (it already has correct positioning)
	if len(aggregatedData) > 0 {
		copy(unifiedBuffer, aggregatedData)
		fmt.Printf("DEBUG: ClientStream %d unified buffer: copied %d bytes from aggregated data\n", s.id, len(aggregatedData))
	}

	// Then overlay primary data at offset 0 (primary data always starts at beginning)
	if len(primaryData) > 0 {
		copy(unifiedBuffer[:len(primaryData)], primaryData)
		fmt.Printf("DEBUG: ClientStream %d unified buffer: overlaid %d bytes from primary data at offset 0\n", s.id, len(primaryData))
	}

	fmt.Printf("DEBUG: ClientStream %d unified buffer: total length %d (primary: %d, aggregated: %d)\n",
		s.id, len(unifiedBuffer), len(primaryData), len(aggregatedData))

	return unifiedBuffer, nil
}

// findContiguousDataLength finds the length of contiguous data starting from the given offset
// Returns 0 if there's a gap at the starting offset
func (s *ClientStream) findContiguousDataLength(buffer []byte, startOffset int) int {
	if startOffset >= len(buffer) {
		return 0 // No data available at this offset
	}

	// Check for contiguous non-zero data starting from startOffset
	contiguousLength := 0
	for i := startOffset; i < len(buffer); i++ {
		if buffer[i] == 0 {
			// Found a zero byte - check if it's a gap or just trailing zeros
			hasDataAfter := false
			for j := i + 1; j < len(buffer); j++ {
				if buffer[j] != 0 {
					hasDataAfter = true
					break
				}
			}
			if hasDataAfter {
				// This is a gap in the middle, stop here
				break
			}
			// This is trailing zeros, continue
		}
		contiguousLength++
	}

	fmt.Printf("DEBUG: ClientStream %d found %d bytes of contiguous data starting at offset %d\n",
		s.id, contiguousLength, startOffset)

	return contiguousLength
}

// readAvailablePrimaryData reads any immediately available data from the primary QUIC stream
func (s *ClientStream) readAvailablePrimaryData() []byte {
	// Use a very short deadline to avoid blocking
	s.quicStream.SetReadDeadline(time.Now().Add(1 * time.Millisecond))
	defer s.quicStream.SetReadDeadline(time.Time{}) // Clear deadline

	buffer := make([]byte, 4096) // Large enough buffer for available data
	n, err := s.quicStream.Read(buffer)
	if err != nil || n == 0 {
		return []byte{} // No data available
	}

	fmt.Printf("DEBUG: ClientStream %d read %d bytes from primary QUIC stream\n", s.id, n)
	return buffer[:n]
}

// depositPrimaryDataInAggregator reads data from the primary QUIC stream and deposits it in the aggregator
func (s *ClientStream) depositPrimaryDataInAggregator() {
	// Try to read any available data from the primary QUIC stream
	s.quicStream.SetReadDeadline(time.Now().Add(1 * time.Millisecond))
	defer s.quicStream.SetReadDeadline(time.Time{}) // Clear deadline

	buffer := make([]byte, 4096)
	n, err := s.quicStream.Read(buffer)
	if err != nil || n == 0 {
		return // No data available
	}

	primaryData := buffer[:n]
	fmt.Printf("DEBUG: ClientStream %d depositing %d bytes from primary stream into aggregator\n", s.id, n)

	// Deposit primary data into the aggregator via the session
	// We need to calculate the correct offset - for now, we'll use the current offset
	currentOffset := uint64(s.offset)
	if s.offset < 0 {
		currentOffset = 0
	}

	err = s.session.DepositPrimaryDataInAggregator(s.id, primaryData, currentOffset)
	if err != nil {
		fmt.Printf("DEBUG: ClientStream %d failed to deposit primary data in aggregator: %v\n", s.id, err)
		return
	}

	// Update our offset to reflect the data we just deposited
	s.offset += len(primaryData)
	fmt.Printf("DEBUG: ClientStream %d successfully deposited %d bytes at offset %d, new offset: %d\n",
		s.id, len(primaryData), currentOffset, s.offset)
}
