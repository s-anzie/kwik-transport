package stream

import (
	"context"
	"fmt"
	"kwik/internal/utils"
	"kwik/pkg/data"
	"kwik/proto/control"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
)

// ServerSession interface to avoid circular imports
type ServerSession interface {
	GetContext() context.Context
	RemoveStream(streamID uint64)

	// Expose metadata protocol for encapsulation/decapsulation
	GetMetadataProtocol() *MetadataProtocolImpl

	// Logger access for streams
	GetLogger() StreamLogger
	// Aggregator returns the secondary stream aggregator for this session
	Aggregator() *data.StreamAggregator
	// GetServerRole returns the server role (PRIMARY or SECONDARY)
	GetServerRole() control.SessionRole
}

// ServerSession interface to avoid circular imports
type ServerStream struct {
	id         uint64
	pathID     string
	session    ServerSession
	created    time.Time
	state      StreamState
	readBuf    []byte
	writeBuf   []byte
	quicStream quic.Stream // Underlying QUIC stream
	mutex      sync.RWMutex

	// Secondary stream isolation fields
	offset            int    // Current offset for stream data aggregation
	remoteStreamID    uint64 // Remote stream ID for cross-stream references
	isSecondaryStream bool   // Flag to indicate if this is a secondary stream for aggregation

	// Pending segments for retransmission keyed by aggregated offset
	pendingSegments map[uint64]*PendingSegment
	retransmitOnce  sync.Once
	retransmitStop  chan struct{}

	// Packet aggregation buffer (raw frames: metadata-encapsulated)
	packetFrameRaw       [][]byte
	packetBufferBytes    int
	packetFlushScheduled bool
}

// PendingSegment tracks a sent segment awaiting ACK
type PendingSegment struct {
	offset       uint64
	size         int
	encapsulated []byte
	lastSent     time.Time
	retries      int
}

func NewServerStream(streamID uint64, pathID string, session ServerSession, quicStream quic.Stream) *ServerStream {
	return &ServerStream{
		id:         streamID,
		pathID:     pathID,
		session:    session,
		created:    time.Now(),
		state:      StreamStateOpen,
		readBuf:    make([]byte, 0, utils.DefaultReadBufferSize),
		writeBuf:   make([]byte, 0, utils.DefaultWriteBufferSize),
		quicStream: quicStream,
	}
}
func NewSecondaryServerStream(streamID uint64, pathID string, session ServerSession, quicStream quic.Stream) *ServerStream {
	return &ServerStream{
		id:                streamID,
		pathID:            pathID,
		session:           session,
		created:           time.Now(),
		state:             StreamStateOpen,
		readBuf:           make([]byte, 0, utils.DefaultReadBufferSize),
		writeBuf:          make([]byte, 0, utils.DefaultWriteBufferSize),
		isSecondaryStream: true,
		quicStream:        quicStream,
	}
}

// ServerStream methods (implementing Stream interface)
func (s *ServerStream) ID() uint64 {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.id
}

// Read reads data from the stream (QUIC-compatible)
func (s *ServerStream) Read(p []byte) (int, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	// Check if we have a QUIC stream to read from
	if s.quicStream == nil {
		return 0, utils.NewKwikError(utils.ErrStreamCreationFailed, "no underlying QUIC stream", nil)
	}

	// Read directly from the underlying QUIC stream into a temporary buffer
	buf := make([]byte, len(p))
	n, err := s.quicStream.Read(buf)
	if err != nil || n == 0 {
		return n, err
	}

	// Try to decapsulate using metadata protocol
	mp := s.session.GetMetadataProtocol()
	if mp != nil {
		metadata, payload, derr := mp.DecapsulateData(buf[:n])
		if derr == nil && metadata != nil && len(payload) > 0 {
			// Update remoteStreamID based on KWIK Stream ID carried by the client
			s.mutex.RUnlock()
			s.mutex.Lock()
			s.remoteStreamID = metadata.KwikStreamID
			s.mutex.Unlock()
			s.mutex.RLock()
			// Return only the application payload to the server application
			copied := copy(p, payload)
			return copied, nil
		}
	}

	// If not a metadata frame, return raw data
	return 0, fmt.Errorf("no metadata protocol available")
}

// Write writes data to the stream (QUIC-compatible)
func (s *ServerStream) Write(p []byte) (int, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	// Check if we have a QUIC stream to write to
	if s.quicStream == nil {
		return 0, utils.NewKwikError(utils.ErrStreamCreationFailed, "no underlying QUIC stream", nil)
	}

	// Debug: Log the write attempt and conditions
	s.session.GetLogger().Debug(fmt.Sprintf("ServerStream %d Write() - serverRole=%s, remoteStreamID=%d, isSecondaryStream=%t",
		s.id, s.session.GetServerRole().String(), s.remoteStreamID, s.isSecondaryStream))

	// Always encapsulate data with metadata for uniform client-side processing
	return s.writeWithMetadata(p)
}

// writeWithMetadata writes data encapsulated with metadata for both primary and secondary roles
func (s *ServerStream) writeWithMetadata(data []byte) (int, error) {
	// Create metadata protocol instance
	metadataProtocol := NewMetadataProtocol()

	// Encapsulate data with metadata
	currentOffset := uint64(s.offset)
	dataFrame, err := metadataProtocol.EncapsulateData(
		s.remoteStreamID, // Target KWIK stream ID
		s.id,             // Secondary stream ID (placeholder; not serialized in current format)
		currentOffset,
		data,
	)

	if err != nil {
		return 0, utils.NewKwikError(utils.ErrInvalidFrame,
			fmt.Sprintf("failed to encapsulate stream data: %v", err), err)
	}

	// Aggregate frame into packet buffer and schedule flush
	mtu := int(utils.DefaultMaxPacketSize)
	frameLen := len(dataFrame)
	// If adding would exceed MTU and we have content, flush first
	if s.packetBufferBytes+4+frameLen > mtu && s.packetBufferBytes > 0 {
		if err := s.flushPacketLocked(); err != nil {
			return 0, err
		}
	}
	// Append with length-prefix
	s.packetFrameRaw = append(s.packetFrameRaw, dataFrame)
	s.packetBufferBytes += 4 + frameLen
	// Schedule timed flush if not already scheduled
	if !s.packetFlushScheduled {
		s.packetFlushScheduled = true
		go func() {
			time.Sleep(2 * time.Millisecond)
			s.mutex.Lock()
			defer s.mutex.Unlock()
			_ = s.flushPacketLocked()
			s.packetFlushScheduled = false
		}()
	}

	// Update offset for next write
	s.offset += len(data)

	// Log for debugging
	s.session.GetLogger().Info(fmt.Sprintf("[SERVER] Stream %d wrote %d bytes (payload) to KWIK stream %d at offset %d",
		s.id, len(data), s.remoteStreamID, s.offset-len(data)))

	// Pending tracking is done when packet is flushed (in flushPacketLocked)

	// Return the original data length (not encapsulated length)
	return len(data), nil
}

// flushPacketLocked assembles buffered frames into a transport packet and sends it
func (s *ServerStream) flushPacketLocked() error {
	if s.quicStream == nil {
		return utils.NewKwikError(utils.ErrConnectionLost, "no underlying QUIC stream", nil)
	}
	if len(s.packetFrameRaw) == 0 {
		return nil
	}
	// Transport packet format: [PacketID:8][FrameCount:2] + repeated([FrameLen:4][FrameBytes])
	packetID := data.GenerateFrameID()
	header := make([]byte, 10)
	for i := 0; i < 8; i++ {
		header[i] = byte(packetID >> (8 * (7 - i)))
	}
	frameCount := uint16(len(s.packetFrameRaw))
	header[8] = byte(frameCount >> 8)
	header[9] = byte(frameCount)
	body := make([]byte, 0, s.packetBufferBytes)
	for _, fb := range s.packetFrameRaw {
		l := len(fb)
		body = append(body, byte(l>>24), byte(l>>16), byte(l>>8), byte(l))
		body = append(body, fb...)
	}
	raw := append(header, body...)
	if _, err := s.quicStream.Write(raw); err != nil {
		return err
	}
	// Register pending
	if s.pendingSegments == nil {
		s.pendingSegments = make(map[uint64]*PendingSegment)
	}
	s.pendingSegments[packetID] = &PendingSegment{
		offset:       packetID,
		size:         len(raw),
		encapsulated: raw,
		lastSent:     time.Now(),
		retries:      0,
	}
	s.startRetransmitter()
	// Reset buffer
	s.packetFrameRaw = nil
	s.packetBufferBytes = 0
	return nil
}

// startRetransmitter starts a background loop to retransmit unacked segments
func (s *ServerStream) startRetransmitter() {
	s.retransmitOnce.Do(func() {
		s.retransmitStop = make(chan struct{})
		go func() {
			ticker := time.NewTicker(500 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-s.retransmitStop:
					return
				case <-ticker.C:
					s.mutex.Lock()
					for off, seg := range s.pendingSegments {
						if time.Since(seg.lastSent) > 1*time.Second && seg.retries < 3 {
							// Retransmit
							_, _ = s.quicStream.Write(seg.encapsulated)
							seg.lastSent = time.Now()
							seg.retries++
							s.session.GetLogger().Debug(fmt.Sprintf("Retransmitted segment stream=%d offset=%d retries=%d", s.id, off, seg.retries))
						} else if seg.retries >= 3 {
							// Give up
							delete(s.pendingSegments, off)
							s.session.GetLogger().Debug(fmt.Sprintf("Dropping segment stream=%d offset=%d after max retries", s.id, off))
						}
					}
					s.mutex.Unlock()
				}
			}
		}()
	})
}

// handleAck removes a pending segment when acknowledged
func (s *ServerStream) HandleAck(offset uint64, size uint32) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.pendingSegments != nil {
		if _, ok := s.pendingSegments[offset]; ok {
			delete(s.pendingSegments, offset)
			s.session.GetLogger().Debug(fmt.Sprintf("ACK received for stream=%d offset=%d", s.id, offset))
		}
	}
}

// Close closes the stream (QUIC-compatible)
func (s *ServerStream) Close() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.state = StreamStateClosed
	return nil
}

// StreamID returns the logical stream ID
func (s *ServerStream) StreamID() uint64 {
	return s.id
}

// PathID returns the primary path ID for this stream
func (s *ServerStream) PathID() string {
	return s.pathID
}

// SetOffset sets the current offset for stream data aggregation
// This is used by secondary servers to specify where their data should be placed in the KWIK stream
func (s *ServerStream) SetOffset(offset int) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if offset < 0 {
		return utils.NewKwikError(utils.ErrInvalidFrame, "offset cannot be negative", nil)
	}

	s.offset = offset

	// If this is a secondary server stream, notify the aggregation system
	if s.session.GetServerRole() == control.SessionRole_SECONDARY && s.remoteStreamID != 0 {
		// Create secondary stream data for aggregation
		// This will be used when data is written to this stream
		return s.updateAggregationMapping()
	}

	return nil
}

// GetOffset returns the current offset for stream data aggregation
func (s *ServerStream) GetOffset() int {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.offset
}

// SetRemoteStreamID sets the remote stream ID for cross-stream references
// For secondary servers, this represents the KWIK stream ID where data should be aggregated
func (s *ServerStream) SetRemoteStreamID(remoteStreamID uint64) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if remoteStreamID == 0 {
		return utils.NewKwikError(utils.ErrInvalidFrame, "remote stream ID cannot be zero", nil)
	}

	s.remoteStreamID = remoteStreamID

	// If this is a secondary server stream, set up aggregation mapping
	if s.session.GetServerRole() == control.SessionRole_SECONDARY {
		return s.updateAggregationMapping()
	}

	return nil
}

// RemoteStreamID returns the remote stream ID for cross-stream references
func (s *ServerStream) RemoteStreamID() uint64 {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.remoteStreamID
}

// updateAggregationMapping updates the aggregation mapping for secondary streams
func (s *ServerStream) updateAggregationMapping() error {
	// Only for secondary servers with both offset and remote stream ID set
	if s.session.GetServerRole() == control.SessionRole_SECONDARY || s.remoteStreamID == 0 {
		return nil
	}

	// Get the secondary stream aggregator from the session
	aggregator := s.session.Aggregator()
	if aggregator == nil {
		return utils.NewKwikError(utils.ErrStreamCreationFailed, "no secondary aggregator available", nil)
	}

	// Set up the stream mapping in the aggregator
	err := aggregator.SetStreamMapping(s.id, s.remoteStreamID, s.pathID)
	if err != nil {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			fmt.Sprintf("failed to set stream mapping: %v", err), err)
	}

	s.session.GetLogger().Info(fmt.Sprintf("[SECONDARY] Stream %d mapped to KWIK stream %d at offset %d (aggregator configured)",
		s.id, s.remoteStreamID, s.offset))

	return nil
}

// Additional utility methods for QUIC compatibility

// SetDeadline sets the read and write deadlines (QUIC-compatible interface)
// Note: This is a placeholder for future implementation
func (s *ServerStream) SetDeadline(t time.Time) error {
	// TODO: Implement deadline support for QUIC compatibility
	// This would set both read and write deadlines
	return nil
}

// SetReadDeadline sets the read deadline (QUIC-compatible interface)
// Note: This is a placeholder for future implementation
func (s *ServerStream) SetReadDeadline(t time.Time) error {
	// TODO: Implement read deadline support
	return nil
}

// SetWriteDeadline sets the write deadline (QUIC-compatible interface)
// Note: This is a placeholder for future implementation
func (s *ServerStream) SetWriteDeadline(t time.Time) error {
	// TODO: Implement write deadline support
	return nil
}

// Context returns the stream's context (QUIC-compatible interface)
func (s *ServerStream) Context() context.Context {
	return s.session.GetContext()
}

// CancelWrite cancels the write side of the stream (QUIC-compatible)
func (s *ServerStream) CancelWrite(errorCode uint64) {
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
func (s *ServerStream) CancelRead(errorCode uint64) {
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
func (s *ServerStream) PendingSegments() map[uint64]*PendingSegment {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.pendingSegments
}
func (s *ServerStream) DeletePendingSegment(packetID uint64) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.pendingSegments != nil {
		if _, ok := s.pendingSegments[packetID]; ok {
			delete(s.pendingSegments, packetID)
			s.session.GetLogger().Debug(fmt.Sprintf("DEBUG: Deleted pending segment for stream=%d packetID=%d", s.id, packetID))
		}
	}
}
