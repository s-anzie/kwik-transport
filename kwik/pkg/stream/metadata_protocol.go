package stream

import (
	"encoding/binary"
	"fmt"
	"time"
)

// MetadataProtocol handles encapsulation and decapsulation of metadata for secondary streams
type MetadataProtocol interface {
	// Encapsulation des métadonnées
	EncapsulateData(kwikStreamID uint64, offset uint64, data []byte) ([]byte, error)
	DecapsulateData(encapsulatedData []byte) (*StreamMetadata, []byte, error)
	
	// Batch processing des métadonnées
	EncapsulateDataBatch(items []BatchMetadataItem) ([][]byte, error)
	DecapsulateDataBatch(encapsulatedFrames [][]byte) ([]*BatchDecapsulationResult, error)
	
	// Validation des métadonnées
	ValidateMetadata(metadata *StreamMetadata) error
	CreateStreamOpenMetadata(kwikStreamID uint64) (*StreamMetadata, error)
}

// StreamMetadata contains metadata information for secondary stream data
type StreamMetadata struct {
	KwikStreamID uint64      // Target KWIK stream ID
	Offset       uint64      // Offset in the target KWIK stream
	DataLength   uint32      // Length of the data payload
	Timestamp    uint64      // Timestamp when metadata was created
	SourcePathID string      // Source path identifier
	MessageType  MetadataType // Type of metadata message
}

// MetadataType defines the type of metadata message
type MetadataType int

const (
	MetadataTypeData       MetadataType = iota // Regular data frame
	MetadataTypeStreamOpen                     // Stream opening notification
	MetadataTypeStreamClose                    // Stream closing notification
	MetadataTypeOffsetSync                     // Offset synchronization message
)

// BatchMetadataItem represents an item for batch processing
type BatchMetadataItem struct {
	KwikStreamID uint64
	Offset       uint64
	Data         []byte
	SourcePathID string
	MessageType  MetadataType
}

// BatchDecapsulationResult represents the result of batch decapsulation
type BatchDecapsulationResult struct {
	Metadata *StreamMetadata
	Data     []byte
	Error    error
}

// String returns a string representation of the metadata type
func (m MetadataType) String() string {
	switch m {
	case MetadataTypeData:
		return "DATA"
	case MetadataTypeStreamOpen:
		return "STREAM_OPEN"
	case MetadataTypeStreamClose:
		return "STREAM_CLOSE"
	case MetadataTypeOffsetSync:
		return "OFFSET_SYNC"
	default:
		return "UNKNOWN"
	}
}

// MetadataProtocolImpl is the concrete implementation of MetadataProtocol
type MetadataProtocolImpl struct {
	// Configuration
	maxDataLength uint32
	maxPathIDLen  int
}

// NewMetadataProtocol creates a new metadata protocol instance
func NewMetadataProtocol() *MetadataProtocolImpl {
	return &MetadataProtocolImpl{
		maxDataLength: 1024 * 1024, // 1MB max data length
		maxPathIDLen:  255,         // 255 chars max path ID (1 byte length field)
	}
}

// Metadata frame format:
// +------------------+------------------+------------------+------------------+
// |   Magic (4B)     | Version (1B)     | Type (1B)        | Reserved (2B)    |
// +------------------+------------------+------------------+------------------+
// |                    KWIK Stream ID (8B)                                    |
// +---------------------------------------------------------------------------|
// |                         Offset (8B)                                       |
// +---------------------------------------------------------------------------|
// |                    Data Length (4B)                                       |
// +------------------+------------------+------------------+------------------+
// |                      Timestamp (8B)                                       |
// +---------------------------------------------------------------------------|
// | Path ID Len (1B) |                Path ID (variable)                     |
// +------------------+--------------------------------------------------------+
// |                           Data (variable)                                 |
// +---------------------------------------------------------------------------|

const (
	metadataFrameMagic   = 0x4B574B4D // "KWKM" in ASCII
	metadataFrameVersion = 1
	
	// Frame header sizes
	metadataHeaderSize    = 8  // Magic + Version + Type + Reserved
	metadataFixedSize     = 28 // KWIK Stream ID + Offset + Data Length + Timestamp
	metadataPathIDLenSize = 1  // Path ID length field
	
	// Minimum frame size (header + fixed fields + path ID length)
	minMetadataFrameSize = metadataHeaderSize + metadataFixedSize + metadataPathIDLenSize
)

// EncapsulateData encapsulates data with metadata for transmission over secondary streams
func (p *MetadataProtocolImpl) EncapsulateData(kwikStreamID uint64, offset uint64, data []byte) ([]byte, error) {
	if len(data) > int(p.maxDataLength) {
		return nil, &MetadataProtocolError{
			Code:    ErrMetadataDataTooLarge,
			Message: fmt.Sprintf("data length %d exceeds maximum %d", len(data), p.maxDataLength),
		}
	}
	
	metadata := &StreamMetadata{
		KwikStreamID: kwikStreamID,
		Offset:       offset,
		DataLength:   uint32(len(data)),
		Timestamp:    uint64(time.Now().UnixNano()),
		SourcePathID: "", // Will be set by caller if needed
		MessageType:  MetadataTypeData,
	}
	
	return p.encodeFrame(metadata, data)
}

// DecapsulateData extracts metadata and data from an encapsulated frame
func (p *MetadataProtocolImpl) DecapsulateData(encapsulatedData []byte) (*StreamMetadata, []byte, error) {
	if len(encapsulatedData) < minMetadataFrameSize {
		return nil, nil, &MetadataProtocolError{
			Code:    ErrMetadataFrameTooSmall,
			Message: fmt.Sprintf("frame size %d is smaller than minimum %d", len(encapsulatedData), minMetadataFrameSize),
		}
	}
	
	return p.decodeFrame(encapsulatedData)
}

// ValidateMetadata validates a metadata structure
func (p *MetadataProtocolImpl) ValidateMetadata(metadata *StreamMetadata) error {
	if metadata == nil {
		return &MetadataProtocolError{
			Code:    ErrMetadataInvalid,
			Message: "metadata is nil",
		}
	}
	
	if metadata.KwikStreamID == 0 {
		return &MetadataProtocolError{
			Code:    ErrMetadataInvalid,
			Message: "KWIK stream ID cannot be 0",
		}
	}
	
	if metadata.DataLength > p.maxDataLength {
		return &MetadataProtocolError{
			Code:    ErrMetadataDataTooLarge,
			Message: fmt.Sprintf("data length %d exceeds maximum %d", metadata.DataLength, p.maxDataLength),
		}
	}
	
	if len(metadata.SourcePathID) > p.maxPathIDLen {
		return &MetadataProtocolError{
			Code:    ErrMetadataInvalid,
			Message: fmt.Sprintf("path ID length %d exceeds maximum %d", len(metadata.SourcePathID), p.maxPathIDLen),
		}
	}
	
	if metadata.MessageType < MetadataTypeData || metadata.MessageType > MetadataTypeOffsetSync {
		return &MetadataProtocolError{
			Code:    ErrMetadataInvalid,
			Message: fmt.Sprintf("invalid message type %d", metadata.MessageType),
		}
	}
	
	return nil
}

// CreateStreamOpenMetadata creates metadata for a stream opening notification
func (p *MetadataProtocolImpl) CreateStreamOpenMetadata(kwikStreamID uint64) (*StreamMetadata, error) {
	metadata := &StreamMetadata{
		KwikStreamID: kwikStreamID,
		Offset:       0,
		DataLength:   0,
		Timestamp:    uint64(time.Now().UnixNano()),
		SourcePathID: "",
		MessageType:  MetadataTypeStreamOpen,
	}
	
	if err := p.ValidateMetadata(metadata); err != nil {
		return nil, err
	}
	
	return metadata, nil
}

// EncapsulateDataBatch processes multiple metadata items in batch for improved performance
func (p *MetadataProtocolImpl) EncapsulateDataBatch(items []BatchMetadataItem) ([][]byte, error) {
	if len(items) == 0 {
		return nil, &MetadataProtocolError{
			Code:    ErrMetadataInvalid,
			Message: "empty batch items",
		}
	}
	
	results := make([][]byte, len(items))
	timestamp := uint64(time.Now().UnixNano()) // Use same timestamp for batch
	
	for i, item := range items {
		if len(item.Data) > int(p.maxDataLength) {
			return nil, &MetadataProtocolError{
				Code:    ErrMetadataDataTooLarge,
				Message: fmt.Sprintf("data length %d exceeds maximum %d at item %d", len(item.Data), p.maxDataLength, i),
			}
		}
		
		metadata := &StreamMetadata{
			KwikStreamID: item.KwikStreamID,
			Offset:       item.Offset,
			DataLength:   uint32(len(item.Data)),
			Timestamp:    timestamp,
			SourcePathID: item.SourcePathID,
			MessageType:  item.MessageType,
		}
		
		frame, err := p.encodeFrame(metadata, item.Data)
		if err != nil {
			return nil, &MetadataProtocolError{
				Code:    ErrMetadataProtocolViolation,
				Message: fmt.Sprintf("failed to encode frame at item %d: %v", i, err),
			}
		}
		
		results[i] = frame
	}
	
	return results, nil
}

// DecapsulateDataBatch processes multiple encapsulated frames in batch for improved performance
func (p *MetadataProtocolImpl) DecapsulateDataBatch(encapsulatedFrames [][]byte) ([]*BatchDecapsulationResult, error) {
	if len(encapsulatedFrames) == 0 {
		return nil, &MetadataProtocolError{
			Code:    ErrMetadataInvalid,
			Message: "empty encapsulated frames",
		}
	}
	
	results := make([]*BatchDecapsulationResult, len(encapsulatedFrames))
	
	for i, frame := range encapsulatedFrames {
		metadata, data, err := p.decodeFrame(frame)
		results[i] = &BatchDecapsulationResult{
			Metadata: metadata,
			Data:     data,
			Error:    err,
		}
	}
	
	return results, nil
}

// encodeFrame encodes metadata and data into a binary frame
func (p *MetadataProtocolImpl) encodeFrame(metadata *StreamMetadata, data []byte) ([]byte, error) {
	if err := p.ValidateMetadata(metadata); err != nil {
		return nil, err
	}
	
	pathIDBytes := []byte(metadata.SourcePathID)
	frameSize := minMetadataFrameSize + len(pathIDBytes) + len(data)
	
	frame := make([]byte, frameSize)
	offset := 0
	
	// Header: Magic + Version + Type + Reserved
	binary.BigEndian.PutUint32(frame[offset:], metadataFrameMagic)
	offset += 4
	frame[offset] = metadataFrameVersion
	offset++
	frame[offset] = byte(metadata.MessageType)
	offset++
	// Reserved 2 bytes (set to 0)
	binary.BigEndian.PutUint16(frame[offset:], 0)
	offset += 2
	
	// Fixed fields
	binary.BigEndian.PutUint64(frame[offset:], metadata.KwikStreamID)
	offset += 8
	binary.BigEndian.PutUint64(frame[offset:], metadata.Offset)
	offset += 8
	binary.BigEndian.PutUint32(frame[offset:], uint32(len(data)))
	offset += 4
	binary.BigEndian.PutUint64(frame[offset:], metadata.Timestamp)
	offset += 8
	
	// Path ID
	frame[offset] = byte(len(pathIDBytes))
	offset++
	copy(frame[offset:], pathIDBytes)
	offset += len(pathIDBytes)
	
	// Data
	copy(frame[offset:], data)
	
	return frame, nil
}

// decodeFrame decodes a binary frame into metadata and data
func (p *MetadataProtocolImpl) decodeFrame(frame []byte) (*StreamMetadata, []byte, error) {
	if len(frame) < minMetadataFrameSize {
		return nil, nil, &MetadataProtocolError{
			Code:    ErrMetadataFrameTooSmall,
			Message: "frame too small",
		}
	}
	
	offset := 0
	
	// Validate magic
	magic := binary.BigEndian.Uint32(frame[offset:])
	offset += 4
	if magic != metadataFrameMagic {
		return nil, nil, &MetadataProtocolError{
			Code:    ErrMetadataInvalidMagic,
			Message: fmt.Sprintf("invalid magic: expected 0x%08X, got 0x%08X", metadataFrameMagic, magic),
		}
	}
	
	// Validate version
	version := frame[offset]
	offset++
	if version != metadataFrameVersion {
		return nil, nil, &MetadataProtocolError{
			Code:    ErrMetadataUnsupportedVersion,
			Message: fmt.Sprintf("unsupported version: expected %d, got %d", metadataFrameVersion, version),
		}
	}
	
	// Message type
	messageType := MetadataType(frame[offset])
	offset++
	
	// Skip reserved bytes
	offset += 2
	
	// Fixed fields
	kwikStreamID := binary.BigEndian.Uint64(frame[offset:])
	offset += 8
	streamOffset := binary.BigEndian.Uint64(frame[offset:])
	offset += 8
	dataLength := binary.BigEndian.Uint32(frame[offset:])
	offset += 4
	timestamp := binary.BigEndian.Uint64(frame[offset:])
	offset += 8
	
	// Path ID
	pathIDLen := int(frame[offset])
	offset++
	
	if offset+pathIDLen > len(frame) {
		return nil, nil, &MetadataProtocolError{
			Code:    ErrMetadataFrameCorrupted,
			Message: "path ID extends beyond frame",
		}
	}
	
	pathID := string(frame[offset : offset+pathIDLen])
	offset += pathIDLen
	
	// Data
	expectedDataLen := int(dataLength)
	if offset+expectedDataLen > len(frame) {
		return nil, nil, &MetadataProtocolError{
			Code:    ErrMetadataFrameCorrupted,
			Message: "data extends beyond frame",
		}
	}
	
	data := make([]byte, expectedDataLen)
	copy(data, frame[offset:offset+expectedDataLen])
	
	metadata := &StreamMetadata{
		KwikStreamID: kwikStreamID,
		Offset:       streamOffset,
		DataLength:   dataLength,
		Timestamp:    timestamp,
		SourcePathID: pathID,
		MessageType:  messageType,
	}
	
	// Validate the decoded metadata
	if err := p.ValidateMetadata(metadata); err != nil {
		return nil, nil, err
	}
	
	return metadata, data, nil
}

// MetadataProtocolError represents an error in metadata protocol operations
type MetadataProtocolError struct {
	Code    string
	Message string
}

func (e *MetadataProtocolError) Error() string {
	return e.Code + ": " + e.Message
}

// Error codes for metadata protocol operations
const (
	ErrMetadataInvalid            = "KWIK_METADATA_INVALID"
	ErrMetadataDataTooLarge       = "KWIK_METADATA_DATA_TOO_LARGE"
	ErrMetadataFrameTooSmall      = "KWIK_METADATA_FRAME_TOO_SMALL"
	ErrMetadataFrameCorrupted     = "KWIK_METADATA_FRAME_CORRUPTED"
	ErrMetadataInvalidMagic       = "KWIK_METADATA_INVALID_MAGIC"
	ErrMetadataUnsupportedVersion = "KWIK_METADATA_UNSUPPORTED_VERSION"
	ErrMetadataProtocolViolation  = "KWIK_METADATA_PROTOCOL_VIOLATION"
)