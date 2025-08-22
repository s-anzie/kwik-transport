package data

import (
	"fmt"
	"hash/crc32"
	"sync/atomic"
	"time"

	"kwik/internal/utils"
	datapb "kwik/proto/data"

	"google.golang.org/protobuf/proto"
)

// FrameEncapsulator handles creation and encapsulation of data frames
// Implements Requirements 8.1, 8.2: DataFrame creation with logical stream IDs and path identification
type FrameEncapsulator struct {
	frameIDCounter uint64
	config         *FrameEncapsulatorConfig
}

// FrameEncapsulatorConfig contains configuration for frame encapsulation
type FrameEncapsulatorConfig struct {
	EnableChecksums    bool
	EnableTimestamps   bool
	MaxFrameSize       uint32
	MaxDataSize        uint32
	CompressionEnabled bool
}

// NewFrameEncapsulator creates a new frame encapsulator
func NewFrameEncapsulator(config *FrameEncapsulatorConfig) *FrameEncapsulator {
	if config == nil {
		config = DefaultFrameEncapsulatorConfig()
	}

	return &FrameEncapsulator{
		frameIDCounter: 0,
		config:         config,
	}
}

// DefaultFrameEncapsulatorConfig returns default configuration
func DefaultFrameEncapsulatorConfig() *FrameEncapsulatorConfig {
	return &FrameEncapsulatorConfig{
		EnableChecksums:    true,
		EnableTimestamps:   true,
		MaxFrameSize:       utils.DefaultMaxPacketSize,
		MaxDataSize:        utils.DefaultMaxPacketSize - utils.KwikHeaderSize - utils.ProtobufOverhead,
		CompressionEnabled: false,
	}
}

// CreateDataFrame creates a new data frame with logical stream ID and path identification
// Implements Requirement 8.1: DataFrame creation with logical stream IDs and path identification
func (fe *FrameEncapsulator) CreateDataFrame(logicalStreamID uint64, pathID string, offset uint64, data []byte, fin bool) (*datapb.DataFrame, error) {
	// Validate input parameters
	if logicalStreamID == 0 {
		return nil, utils.NewKwikError(utils.ErrInvalidFrame, "logical stream ID cannot be zero", nil)
	}

	if pathID == "" {
		return nil, utils.NewKwikError(utils.ErrInvalidFrame, "path ID cannot be empty", nil)
	}

	if len(data) > int(fe.config.MaxDataSize) {
		return nil, utils.NewKwikError(utils.ErrPacketTooLarge,
			fmt.Sprintf("data size %d exceeds maximum %d", len(data), fe.config.MaxDataSize), nil)
	}

	// Generate unique frame ID
	frameID := atomic.AddUint64(&fe.frameIDCounter, 1)

	// Create the data frame
	frame := &datapb.DataFrame{
		FrameId:         frameID,
		LogicalStreamId: logicalStreamID,
		Offset:          offset,
		Data:            data,
		Fin:             fin,
		PathId:          pathID,
		DataLength:      uint32(len(data)),
	}

	// Add timestamp if enabled
	if fe.config.EnableTimestamps {
		frame.Timestamp = uint64(time.Now().UnixNano())
	}

	// Compute checksum if enabled
	if fe.config.EnableChecksums {
		frame.Checksum = fe.computeFrameChecksum(frame)
	}

	return frame, nil
}

// EncapsulateFrame encapsulates a data frame for transmission
// Adds necessary metadata and validates the frame
func (fe *FrameEncapsulator) EncapsulateFrame(frame *datapb.DataFrame) ([]byte, error) {
	if frame == nil {
		return nil, utils.NewKwikError(utils.ErrInvalidFrame, "frame is nil", nil)
	}

	// Validate frame
	if err := fe.validateFrame(frame); err != nil {
		return nil, err
	}

	// Update frame metadata
	if fe.config.EnableTimestamps && frame.Timestamp == 0 {
		frame.Timestamp = uint64(time.Now().UnixNano())
	}

	if fe.config.EnableChecksums && frame.Checksum == 0 {
		frame.Checksum = fe.computeFrameChecksum(frame)
	}

	// Serialize the frame
	data, err := proto.Marshal(frame)
	if err != nil {
		return nil, utils.NewKwikError(utils.ErrSerializationFailed,
			"failed to serialize data frame", err)
	}

	// Check serialized size
	if len(data) > int(fe.config.MaxFrameSize) {
		return nil, utils.NewKwikError(utils.ErrPacketTooLarge,
			fmt.Sprintf("serialized frame size %d exceeds maximum %d", len(data), fe.config.MaxFrameSize), nil)
	}

	return data, nil
}

// DecapsulateFrame decapsulates a received frame from bytes
func (fe *FrameEncapsulator) DecapsulateFrame(data []byte) (*datapb.DataFrame, error) {
	if len(data) == 0 {
		return nil, utils.NewKwikError(utils.ErrInvalidFrame, "data is empty", nil)
	}

	if len(data) > int(fe.config.MaxFrameSize) {
		return nil, utils.NewKwikError(utils.ErrPacketTooLarge,
			fmt.Sprintf("data size %d exceeds maximum frame size %d", len(data), fe.config.MaxFrameSize), nil)
	}

	// Deserialize the frame
	var frame datapb.DataFrame
	err := proto.Unmarshal(data, &frame)
	if err != nil {
		return nil, utils.NewKwikError(utils.ErrDeserializationFailed,
			"failed to deserialize data frame", err)
	}

	// Validate the frame
	if err := fe.validateFrame(&frame); err != nil {
		return nil, err
	}

	// Verify checksum if enabled
	if fe.config.EnableChecksums && frame.Checksum != 0 {
		expectedChecksum := fe.computeFrameChecksum(&frame)
		if frame.Checksum != expectedChecksum {
			return nil, utils.NewKwikError(utils.ErrInvalidFrame,
				"frame checksum validation failed", nil)
		}
	}

	return &frame, nil
}

// ExtractLogicalStreamID extracts the logical stream ID from a frame
// Implements Requirement 8.2: frame routing logic based on identifiers
func (fe *FrameEncapsulator) ExtractLogicalStreamID(frame *datapb.DataFrame) uint64 {
	if frame == nil {
		return 0
	}
	return frame.LogicalStreamId
}

// ExtractPathID extracts the path ID from a frame
// Implements Requirement 8.2: frame routing logic based on identifiers
func (fe *FrameEncapsulator) ExtractPathID(frame *datapb.DataFrame) string {
	if frame == nil {
		return ""
	}
	return frame.PathId
}

// CreateFrameIdentifier creates a unique identifier for frame routing
// Combines logical stream ID and path ID for routing decisions
func (fe *FrameEncapsulator) CreateFrameIdentifier(logicalStreamID uint64, pathID string) string {
	return fmt.Sprintf("%d:%s", logicalStreamID, pathID)
}

// ParseFrameIdentifier parses a frame identifier back into components
func (fe *FrameEncapsulator) ParseFrameIdentifier(identifier string) (uint64, string, error) {
	var logicalStreamID uint64
	var pathID string

	n, err := fmt.Sscanf(identifier, "%d:%s", &logicalStreamID, &pathID)
	if err != nil || n != 2 {
		return 0, "", utils.NewKwikError(utils.ErrInvalidFrame,
			"invalid frame identifier format", err)
	}

	return logicalStreamID, pathID, nil
}

// validateFrame validates a data frame
func (fe *FrameEncapsulator) validateFrame(frame *datapb.DataFrame) error {
	if frame == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "frame is nil", nil)
	}

	// Validate frame ID
	if frame.FrameId == 0 {
		return utils.NewKwikError(utils.ErrInvalidFrame, "frame ID cannot be zero", nil)
	}

	// Validate logical stream ID
	if frame.LogicalStreamId == 0 {
		return utils.NewKwikError(utils.ErrInvalidFrame, "logical stream ID cannot be zero", nil)
	}

	// Validate path ID
	if frame.PathId == "" {
		return utils.NewKwikError(utils.ErrInvalidFrame, "path ID cannot be empty", nil)
	}

	// Validate data size
	if len(frame.Data) > int(fe.config.MaxDataSize) {
		return utils.NewKwikError(utils.ErrPacketTooLarge,
			fmt.Sprintf("frame data size %d exceeds maximum %d", len(frame.Data), fe.config.MaxDataSize), nil)
	}

	// Validate data length field matches actual data
	if frame.DataLength != 0 && frame.DataLength != uint32(len(frame.Data)) {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			"data length field does not match actual data size", nil)
	}

	return nil
}

// computeFrameChecksum computes a CRC32 checksum for a data frame
func (fe *FrameEncapsulator) computeFrameChecksum(frame *datapb.DataFrame) uint32 {
	// Create a copy of the frame without the checksum field for computation
	tempFrame := &datapb.DataFrame{
		FrameId:         frame.FrameId,
		LogicalStreamId: frame.LogicalStreamId,
		Offset:          frame.Offset,
		Data:            frame.Data,
		Fin:             frame.Fin,
		Timestamp:       frame.Timestamp,
		PathId:          frame.PathId,
		DataLength:      frame.DataLength,
		// Checksum is intentionally omitted
	}

	// Serialize the frame without checksum
	data, err := proto.Marshal(tempFrame)
	if err != nil {
		return 0 // Return 0 on error
	}

	// Compute CRC32 checksum
	return crc32.ChecksumIEEE(data)
}

// GetFrameSize returns the size of a frame when serialized
func (fe *FrameEncapsulator) GetFrameSize(frame *datapb.DataFrame) (int, error) {
	data, err := proto.Marshal(frame)
	if err != nil {
		return 0, utils.NewKwikError(utils.ErrSerializationFailed,
			"failed to serialize frame for size calculation", err)
	}
	return len(data), nil
}

// CloneFrame creates a deep copy of a data frame
func (fe *FrameEncapsulator) CloneFrame(frame *datapb.DataFrame) *datapb.DataFrame {
	if frame == nil {
		return nil
	}

	// Create a new frame with copied data
	clonedData := make([]byte, len(frame.Data))
	copy(clonedData, frame.Data)

	return &datapb.DataFrame{
		FrameId:         frame.FrameId,
		LogicalStreamId: frame.LogicalStreamId,
		Offset:          frame.Offset,
		Data:            clonedData,
		Fin:             frame.Fin,
		Timestamp:       frame.Timestamp,
		PathId:          frame.PathId,
		DataLength:      frame.DataLength,
		Checksum:        frame.Checksum,
	}
}

// UpdateFrameForPath updates a frame's path ID and recalculates checksum
func (fe *FrameEncapsulator) UpdateFrameForPath(frame *datapb.DataFrame, newPathID string) error {
	if frame == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "frame is nil", nil)
	}

	if newPathID == "" {
		return utils.NewKwikError(utils.ErrInvalidFrame, "new path ID cannot be empty", nil)
	}

	// Update path ID
	frame.PathId = newPathID

	// Update timestamp if enabled
	if fe.config.EnableTimestamps {
		frame.Timestamp = uint64(time.Now().UnixNano())
	}

	// Recalculate checksum if enabled
	if fe.config.EnableChecksums {
		frame.Checksum = fe.computeFrameChecksum(frame)
	}

	return nil
}

// generateFrameID generates a unique frame identifier
func GenerateFrameID() uint64 {
	return uint64(time.Now().UnixNano())
}
