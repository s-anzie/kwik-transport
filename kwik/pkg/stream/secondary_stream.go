package stream

import (
	"sync"
	"time"

	"github.com/quic-go/quic-go"
)

// SecondaryStream represents a stream interface for secondary servers
// These streams are handled internally and not exposed to the public client session
type SecondaryStream interface {
	// Basic stream operations
	Write([]byte) (int, error)
	Close() error

	// KWIK stream mapping operations
	GetKwikStreamID() uint64
	SetKwikStreamMapping(kwikStreamID uint64, offset uint64) error

	// Stream information
	GetStreamID() uint64
	GetPathID() string
	GetCurrentOffset() uint64
}

// SecondaryStreamImpl implements the SecondaryStream interface
type SecondaryStreamImpl struct {
	// Stream identification
	streamID      uint64
	pathID        string
	kwikStreamID  uint64
	currentOffset uint64

	// Underlying QUIC stream
	quicStream quic.Stream

	// Metadata protocol for encapsulation
	metadataProtocol MetadataProtocol

	// Session reference for internal operations
	session interface{} // Reference to the server session

	// Stream state
	created time.Time
	state   StreamState

	// Synchronization
	mutex sync.RWMutex
}

// NewSecondaryStream creates a new secondary stream
func NewSecondaryStream(streamID uint64, pathID string, quicStream quic.Stream, session interface{}) *SecondaryStreamImpl {
	return &SecondaryStreamImpl{
		streamID:         streamID,
		pathID:           pathID,
		kwikStreamID:     0, // Not mapped initially
		currentOffset:    0,
		quicStream:       quicStream,
		metadataProtocol: NewMetadataProtocol(), // Create default metadata protocol
		session:          session,
		created:          time.Now(),
		state:            StreamStateOpen,
	}
}

// Write writes data to the secondary stream with metadata encapsulation
func (s *SecondaryStreamImpl) Write(data []byte) (int, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.state != StreamStateOpen {
		return 0, &SecondaryStreamError{
			Code:     ErrSecondaryStreamInvalid,
			Message:  "stream is not open for writing",
			StreamID: s.streamID,
		}
	}

	if s.quicStream == nil {
		return 0, &SecondaryStreamError{
			Code:     ErrSecondaryStreamInvalid,
			Message:  "no underlying QUIC stream",
			StreamID: s.streamID,
		}
	}

	// If the stream is mapped to a KWIK stream, encapsulate the data with metadata
	if s.kwikStreamID != 0 && s.metadataProtocol != nil {
		encapsulatedData, err := s.metadataProtocol.EncapsulateData(s.kwikStreamID, s.currentOffset, data)
		if err != nil {
			return 0, &SecondaryStreamError{
				Code:     ErrSecondaryStreamMapping,
				Message:  "failed to encapsulate data with metadata",
				StreamID: s.streamID,
			}
		}

		// Write encapsulated data to the underlying QUIC stream
		_, err = s.quicStream.Write(encapsulatedData)
		if err != nil {
			return 0, err
		}

		// Update offset based on original data length (not encapsulated length)
		s.currentOffset += uint64(len(data))

		// Return the original data length, not the encapsulated length
		return len(data), nil
	}

	// If not mapped, write data directly (this shouldn't happen in normal operation)
	n, err := s.quicStream.Write(data)
	if err != nil {
		return n, err
	}

	// Update offset
	s.currentOffset += uint64(n)

	return n, nil
}

// Close closes the secondary stream
func (s *SecondaryStreamImpl) Close() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.state == StreamStateClosed {
		return nil // Already closed
	}

	s.state = StreamStateClosed

	if s.quicStream != nil {
		return s.quicStream.Close()
	}

	return nil
}

// GetKwikStreamID returns the KWIK stream ID this secondary stream is mapped to
func (s *SecondaryStreamImpl) GetKwikStreamID() uint64 {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.kwikStreamID
}

// SetKwikStreamMapping sets the mapping between this secondary stream and a KWIK stream
func (s *SecondaryStreamImpl) SetKwikStreamMapping(kwikStreamID uint64, offset uint64) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if kwikStreamID == 0 {
		return &SecondaryStreamError{
			Code:     ErrSecondaryStreamMapping,
			Message:  "invalid KWIK stream ID",
			StreamID: s.streamID,
		}
	}

	s.kwikStreamID = kwikStreamID
	s.currentOffset = offset

	return nil
}

// GetStreamID returns the secondary stream ID
func (s *SecondaryStreamImpl) GetStreamID() uint64 {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.streamID
}

// GetPathID returns the path ID for this secondary stream
func (s *SecondaryStreamImpl) GetPathID() string {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.pathID
}

// GetCurrentOffset returns the current offset in the KWIK stream
func (s *SecondaryStreamImpl) GetCurrentOffset() uint64 {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.currentOffset
}

// SetMetadataProtocol sets the metadata protocol for this secondary stream
func (s *SecondaryStreamImpl) SetMetadataProtocol(protocol MetadataProtocol) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.metadataProtocol = protocol
}

// GetMetadataProtocol returns the metadata protocol for this secondary stream
func (s *SecondaryStreamImpl) GetMetadataProtocol() MetadataProtocol {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.metadataProtocol
}

// IsOpen returns whether the secondary stream is open
func (s *SecondaryStreamImpl) IsOpen() bool {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.state == StreamStateOpen
}

// GetState returns the current state of the secondary stream
func (s *SecondaryStreamImpl) GetState() StreamState {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.state
}

// GetCreatedTime returns when the secondary stream was created
func (s *SecondaryStreamImpl) GetCreatedTime() time.Time {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.created
}

// UpdateOffset updates the current offset (used internally by the aggregation system)
func (s *SecondaryStreamImpl) UpdateOffset(newOffset uint64) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.currentOffset = newOffset
}

// GetUnderlyingStream returns the underlying QUIC stream (for internal use)
func (s *SecondaryStreamImpl) GetUnderlyingStream() quic.Stream {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.quicStream
}
