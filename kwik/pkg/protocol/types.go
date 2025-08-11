package protocol

import (
	"fmt"
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
	FrameType FrameType
	Payload   []byte
	Timestamp time.Time
}

// Type returns the frame type (implements Frame interface)
func (cf *ControlFrame) Type() FrameType {
	return cf.FrameType
}

// Serialize serializes the control frame to bytes (implements Frame interface)
func (cf *ControlFrame) Serialize() ([]byte, error) {
	// Simple serialization - in a real implementation, this would use protobuf
	// Format: [FrameID:8][FrameType:1][PayloadLen:4][Payload:N][Timestamp:8]
	result := make([]byte, 0, 21+len(cf.Payload))
	
	// FrameID (8 bytes)
	for i := 0; i < 8; i++ {
		result = append(result, byte(cf.FrameID>>(8*(7-i))))
	}
	
	// FrameType (1 byte)
	result = append(result, byte(cf.FrameType))
	
	// PayloadLen (4 bytes)
	payloadLen := uint32(len(cf.Payload))
	for i := 0; i < 4; i++ {
		result = append(result, byte(payloadLen>>(8*(3-i))))
	}
	
	// Payload
	result = append(result, cf.Payload...)
	
	// Timestamp (8 bytes - Unix nano)
	timestamp := uint64(cf.Timestamp.UnixNano())
	for i := 0; i < 8; i++ {
		result = append(result, byte(timestamp>>(8*(7-i))))
	}
	
	return result, nil
}

// Deserialize deserializes bytes into the control frame (implements Frame interface)
func (cf *ControlFrame) Deserialize(data []byte) error {
	if len(data) < 21 { // Minimum size: 8+1+4+0+8
		return fmt.Errorf("control frame data too short: %d bytes", len(data))
	}
	
	// FrameID (8 bytes)
	cf.FrameID = 0
	for i := 0; i < 8; i++ {
		cf.FrameID = (cf.FrameID << 8) | uint64(data[i])
	}
	
	// FrameType (1 byte)
	cf.FrameType = FrameType(data[8])
	
	// PayloadLen (4 bytes)
	payloadLen := uint32(0)
	for i := 0; i < 4; i++ {
		payloadLen = (payloadLen << 8) | uint32(data[9+i])
	}
	
	// Check if we have enough data for payload
	if len(data) < int(13+payloadLen+8) {
		return fmt.Errorf("control frame data incomplete: expected %d bytes, got %d", 13+payloadLen+8, len(data))
	}
	
	// Payload
	cf.Payload = make([]byte, payloadLen)
	copy(cf.Payload, data[13:13+payloadLen])
	
	// Timestamp (8 bytes)
	timestamp := uint64(0)
	timestampStart := int(13 + payloadLen)
	for i := 0; i < 8; i++ {
		timestamp = (timestamp << 8) | uint64(data[timestampStart+i])
	}
	cf.Timestamp = time.Unix(0, int64(timestamp))
	
	return nil
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

// Type returns the frame type (implements Frame interface)
func (df *DataFrame) Type() FrameType {
	return FrameTypeData
}

// Serialize serializes the data frame to bytes (implements Frame interface)
func (df *DataFrame) Serialize() ([]byte, error) {
	// Format: [FrameID:8][LogicalStreamID:8][Offset:8][DataLen:4][Data:N][Fin:1][Timestamp:8][PathIDLen:2][PathID:N]
	pathIDBytes := []byte(df.PathID)
	result := make([]byte, 0, 39+len(df.Data)+len(pathIDBytes))
	
	// FrameID (8 bytes)
	for i := 0; i < 8; i++ {
		result = append(result, byte(df.FrameID>>(8*(7-i))))
	}
	
	// LogicalStreamID (8 bytes)
	for i := 0; i < 8; i++ {
		result = append(result, byte(df.LogicalStreamID>>(8*(7-i))))
	}
	
	// Offset (8 bytes)
	for i := 0; i < 8; i++ {
		result = append(result, byte(df.Offset>>(8*(7-i))))
	}
	
	// DataLen (4 bytes)
	dataLen := uint32(len(df.Data))
	for i := 0; i < 4; i++ {
		result = append(result, byte(dataLen>>(8*(3-i))))
	}
	
	// Data
	result = append(result, df.Data...)
	
	// Fin (1 byte)
	if df.Fin {
		result = append(result, 1)
	} else {
		result = append(result, 0)
	}
	
	// Timestamp (8 bytes - Unix nano)
	timestamp := uint64(df.Timestamp.UnixNano())
	for i := 0; i < 8; i++ {
		result = append(result, byte(timestamp>>(8*(7-i))))
	}
	
	// PathIDLen (2 bytes)
	pathIDLen := uint16(len(pathIDBytes))
	result = append(result, byte(pathIDLen>>8))
	result = append(result, byte(pathIDLen))
	
	// PathID
	result = append(result, pathIDBytes...)
	
	return result, nil
}

// Deserialize deserializes bytes into the data frame (implements Frame interface)
func (df *DataFrame) Deserialize(data []byte) error {
	if len(data) < 37 { // Minimum size: 8+8+8+4+0+1+8+2+0
		return fmt.Errorf("data frame data too short: %d bytes", len(data))
	}
	
	// FrameID (8 bytes)
	df.FrameID = 0
	for i := 0; i < 8; i++ {
		df.FrameID = (df.FrameID << 8) | uint64(data[i])
	}
	
	// LogicalStreamID (8 bytes)
	df.LogicalStreamID = 0
	for i := 0; i < 8; i++ {
		df.LogicalStreamID = (df.LogicalStreamID << 8) | uint64(data[8+i])
	}
	
	// Offset (8 bytes)
	df.Offset = 0
	for i := 0; i < 8; i++ {
		df.Offset = (df.Offset << 8) | uint64(data[16+i])
	}
	
	// DataLen (4 bytes)
	dataLen := uint32(0)
	for i := 0; i < 4; i++ {
		dataLen = (dataLen << 8) | uint32(data[24+i])
	}
	
	// Check if we have enough data for the payload
	if len(data) < int(28+dataLen+1+8+2) {
		return fmt.Errorf("data frame incomplete: expected at least %d bytes, got %d", 28+dataLen+1+8+2, len(data))
	}
	
	// Data
	df.Data = make([]byte, dataLen)
	copy(df.Data, data[28:28+dataLen])
	
	// Fin (1 byte)
	finPos := int(28 + dataLen)
	df.Fin = data[finPos] == 1
	
	// Timestamp (8 bytes)
	timestamp := uint64(0)
	timestampStart := finPos + 1
	for i := 0; i < 8; i++ {
		timestamp = (timestamp << 8) | uint64(data[timestampStart+i])
	}
	df.Timestamp = time.Unix(0, int64(timestamp))
	
	// PathIDLen (2 bytes)
	pathIDLenStart := timestampStart + 8
	if len(data) < pathIDLenStart+2 {
		return fmt.Errorf("data frame incomplete: missing PathID length")
	}
	pathIDLen := uint16(data[pathIDLenStart])<<8 | uint16(data[pathIDLenStart+1])
	
	// PathID
	pathIDStart := pathIDLenStart + 2
	if len(data) < pathIDStart+int(pathIDLen) {
		return fmt.Errorf("data frame incomplete: missing PathID data")
	}
	df.PathID = string(data[pathIDStart : pathIDStart+int(pathIDLen)])
	
	return nil
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