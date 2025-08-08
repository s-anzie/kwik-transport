package data

import (
	"hash/crc32"

	"google.golang.org/protobuf/proto"
	"kwik/internal/utils"
	datapb "kwik/proto/data"
)

// FrameProcessorImpl implements the FrameProcessor interface
// It handles processing of individual data frames
type FrameProcessorImpl struct {
	config *FrameProcessorConfig
}

// FrameProcessorConfig holds configuration for frame processing
type FrameProcessorConfig struct {
	ValidateChecksums bool
	ComputeChecksums  bool
	MaxFrameSize      uint32
	MaxDataSize       uint32
}

// NewFrameProcessor creates a new frame processor
func NewFrameProcessor() FrameProcessor {
	return &FrameProcessorImpl{
		config: &FrameProcessorConfig{
			ValidateChecksums: true,
			ComputeChecksums:  true,
			MaxFrameSize:      utils.DefaultMaxPacketSize,
			MaxDataSize:       utils.DefaultMaxPacketSize - utils.KwikHeaderSize - utils.ProtobufOverhead,
		},
	}
}

// ProcessIncomingFrame processes an incoming data frame
func (fp *FrameProcessorImpl) ProcessIncomingFrame(frame *datapb.DataFrame) error {
	if frame == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "frame is nil", nil)
	}

	// Validate frame first
	err := fp.ValidateFrame(frame)
	if err != nil {
		return err
	}

	// Validate checksum if enabled
	if fp.config.ValidateChecksums && frame.Checksum != 0 {
		expectedChecksum := fp.computeFrameChecksum(frame)
		if frame.Checksum != expectedChecksum {
			return utils.NewKwikError(utils.ErrInvalidFrame,
				"frame checksum validation failed", nil)
		}
	}

	// Additional processing for incoming frames
	// This could include:
	// - Decompression
	// - Decryption
	// - Reordering logic
	// - Duplicate detection

	return nil
}

// ProcessOutgoingFrame processes an outgoing data frame
func (fp *FrameProcessorImpl) ProcessOutgoingFrame(frame *datapb.DataFrame) error {
	if frame == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "frame is nil", nil)
	}

	// Validate frame first
	err := fp.ValidateFrame(frame)
	if err != nil {
		return err
	}

	// Set data length
	frame.DataLength = uint32(len(frame.Data))

	// Compute checksum if enabled
	if fp.config.ComputeChecksums {
		frame.Checksum = fp.computeFrameChecksum(frame)
	}

	// Additional processing for outgoing frames
	// This could include:
	// - Compression
	// - Encryption
	// - Fragmentation
	// - Sequence numbering

	return nil
}

// ValidateFrame validates a data frame
func (fp *FrameProcessorImpl) ValidateFrame(frame *datapb.DataFrame) error {
	if frame == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "frame is nil", nil)
	}

	// Validate frame ID
	if frame.FrameId == 0 {
		return utils.NewKwikError(utils.ErrInvalidFrame, "frame ID is zero", nil)
	}

	// Validate logical stream ID
	if frame.LogicalStreamId == 0 {
		return utils.NewKwikError(utils.ErrInvalidFrame, "logical stream ID is zero", nil)
	}

	// Validate data size
	if len(frame.Data) > int(fp.config.MaxDataSize) {
		return utils.NewKwikError(utils.ErrPacketTooLarge,
			"frame data size exceeds maximum", nil)
	}

	// Validate data length field matches actual data
	if frame.DataLength != 0 && frame.DataLength != uint32(len(frame.Data)) {
		return utils.NewKwikError(utils.ErrInvalidFrame,
			"data length field does not match actual data size", nil)
	}

	// Validate path ID is not empty
	if frame.PathId == "" {
		return utils.NewKwikError(utils.ErrInvalidFrame, "path ID is empty", nil)
	}

	// Validate timestamp
	if frame.Timestamp == 0 {
		return utils.NewKwikError(utils.ErrInvalidFrame, "timestamp is zero", nil)
	}

	return nil
}

// SerializeFrame serializes a data frame to bytes
func (fp *FrameProcessorImpl) SerializeFrame(frame *datapb.DataFrame) ([]byte, error) {
	if frame == nil {
		return nil, utils.NewKwikError(utils.ErrInvalidFrame, "frame is nil", nil)
	}

	// Validate frame before serialization
	err := fp.ValidateFrame(frame)
	if err != nil {
		return nil, err
	}

	// Serialize using protobuf
	data, err := proto.Marshal(frame)
	if err != nil {
		return nil, utils.NewKwikError(utils.ErrSerializationFailed,
			"failed to serialize data frame", err)
	}

	// Check serialized size
	if len(data) > int(fp.config.MaxFrameSize) {
		return nil, utils.NewKwikError(utils.ErrPacketTooLarge,
			"serialized frame size exceeds maximum", nil)
	}

	return data, nil
}

// DeserializeFrame deserializes bytes into a data frame
func (fp *FrameProcessorImpl) DeserializeFrame(data []byte) (*datapb.DataFrame, error) {
	if len(data) == 0 {
		return nil, utils.NewKwikError(utils.ErrInvalidFrame, "data is empty", nil)
	}

	if len(data) > int(fp.config.MaxFrameSize) {
		return nil, utils.NewKwikError(utils.ErrPacketTooLarge,
			"data size exceeds maximum frame size", nil)
	}

	// Deserialize using protobuf
	var frame datapb.DataFrame
	err := proto.Unmarshal(data, &frame)
	if err != nil {
		return nil, utils.NewKwikError(utils.ErrDeserializationFailed,
			"failed to deserialize data frame", err)
	}

	// Validate deserialized frame
	err = fp.ValidateFrame(&frame)
	if err != nil {
		return nil, err
	}

	return &frame, nil
}

// computeFrameChecksum computes a checksum for a data frame
func (fp *FrameProcessorImpl) computeFrameChecksum(frame *datapb.DataFrame) uint32 {
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
