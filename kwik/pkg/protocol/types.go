package protocol

import (
	"time"
)

// FrameType represents different types of KWIK frames
type FrameType uint8

const (
	// Control plane frame types
	FrameTypeAddPathRequest FrameType = iota
	FrameTypeAddPathResponse
	FrameTypeRemovePathRequest
	FrameTypeRemovePathResponse
	FrameTypePathStatusNotification
	FrameTypeAuthenticationRequest
	FrameTypeAuthenticationResponse
	FrameTypeStreamCreateNotification
	FrameTypeRawPacketTransmission
	
	// Data plane frame types
	FrameTypeData
	FrameTypeAck
)

// Frame represents a generic KWIK frame
type Frame interface {
	Type() FrameType
	Serialize() ([]byte, error)
	Deserialize(data []byte) error
}

// ControlFrame represents a control plane frame
type ControlFrame struct {
	FrameID   uint64
	Type      FrameType
	Payload   []byte
	Timestamp time.Time
}

// DataFrame represents a data plane frame
type DataFrame struct {
	FrameID         uint64
	LogicalStreamID uint64
	Offset          uint64
	Data            []byte
	Fin             bool
	Timestamp       time.Time
	PathID          string
}

// Packet represents a KWIK packet containing multiple frames
type Packet struct {
	PacketID uint64
	PathID   string
	Frames   []Frame
	Checksum uint32
}

// Serializer handles protobuf serialization/deserialization
type Serializer interface {
	SerializeFrame(frame Frame) ([]byte, error)
	DeserializeFrame(data []byte) (Frame, error)
	SerializePacket(packet *Packet) ([]byte, error)
	DeserializePacket(data []byte) (*Packet, error)
}