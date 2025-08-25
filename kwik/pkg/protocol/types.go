package protocol

import (
	"fmt"
	"time"
	"unsafe"
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
	FrameTypeHeartbeat
	FrameTypeHeartbeatResponse
	
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

// HeartbeatPlaneType represents the plane type for heartbeat frames
type HeartbeatPlaneType int

const (
	HeartbeatPlaneControl HeartbeatPlaneType = iota
	HeartbeatPlaneData
)

// HeartbeatFrame represents a heartbeat request frame
type HeartbeatFrame struct {
	FrameID        uint64
	SequenceID     uint64
	PathID         string
	PlaneType      HeartbeatPlaneType
	StreamID       *uint64 // Optional, for data plane heartbeats
	Timestamp      time.Time
	RTTMeasurement bool // Whether this heartbeat measures RTT
}

// Type returns the frame type (implements Frame interface)
func (hf *HeartbeatFrame) Type() FrameType {
	return FrameTypeHeartbeat
}

// Serialize serializes the heartbeat frame to bytes (implements Frame interface)
func (hf *HeartbeatFrame) Serialize() ([]byte, error) {
	// Format: [FrameID:8][SequenceID:8][PlaneType:1][StreamID:9][RTTMeasurement:1][Timestamp:8][PathIDLen:2][PathID:N]
	pathIDBytes := []byte(hf.PathID)
	result := make([]byte, 0, 37+len(pathIDBytes))
	
	// FrameID (8 bytes)
	for i := 0; i < 8; i++ {
		result = append(result, byte(hf.FrameID>>(8*(7-i))))
	}
	
	// SequenceID (8 bytes)
	for i := 0; i < 8; i++ {
		result = append(result, byte(hf.SequenceID>>(8*(7-i))))
	}
	
	// PlaneType (1 byte)
	result = append(result, byte(hf.PlaneType))
	
	// StreamID (9 bytes: 1 byte for presence + 8 bytes for value)
	if hf.StreamID != nil {
		result = append(result, 1) // Present
		for i := 0; i < 8; i++ {
			result = append(result, byte(*hf.StreamID>>(8*(7-i))))
		}
	} else {
		result = append(result, 0) // Not present
		for i := 0; i < 8; i++ {
			result = append(result, 0)
		}
	}
	
	// RTTMeasurement (1 byte)
	if hf.RTTMeasurement {
		result = append(result, 1)
	} else {
		result = append(result, 0)
	}
	
	// Timestamp (8 bytes - Unix nano)
	timestamp := uint64(hf.Timestamp.UnixNano())
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

// Deserialize deserializes bytes into the heartbeat frame (implements Frame interface)
func (hf *HeartbeatFrame) Deserialize(data []byte) error {
	if len(data) < 35 { // Minimum size without PathID
		return fmt.Errorf("heartbeat frame data too short: %d bytes", len(data))
	}
	
	// FrameID (8 bytes)
	hf.FrameID = 0
	for i := 0; i < 8; i++ {
		hf.FrameID = (hf.FrameID << 8) | uint64(data[i])
	}
	
	// SequenceID (8 bytes)
	hf.SequenceID = 0
	for i := 0; i < 8; i++ {
		hf.SequenceID = (hf.SequenceID << 8) | uint64(data[8+i])
	}
	
	// PlaneType (1 byte)
	hf.PlaneType = HeartbeatPlaneType(data[16])
	
	// StreamID (9 bytes: 1 byte for presence + 8 bytes for value)
	if data[17] == 1 { // Present
		streamID := uint64(0)
		for i := 0; i < 8; i++ {
			streamID = (streamID << 8) | uint64(data[18+i])
		}
		hf.StreamID = &streamID
	} else {
		hf.StreamID = nil
	}
	
	// RTTMeasurement (1 byte)
	hf.RTTMeasurement = data[26] == 1
	
	// Timestamp (8 bytes)
	timestamp := uint64(0)
	for i := 0; i < 8; i++ {
		timestamp = (timestamp << 8) | uint64(data[27+i])
	}
	hf.Timestamp = time.Unix(0, int64(timestamp))
	
	// PathIDLen (2 bytes)
	if len(data) < 37 {
		return fmt.Errorf("heartbeat frame incomplete: missing PathID length")
	}
	pathIDLen := uint16(data[35])<<8 | uint16(data[36])
	
	// PathID
	pathIDStart := 37
	if len(data) < pathIDStart+int(pathIDLen) {
		return fmt.Errorf("heartbeat frame incomplete: missing PathID data")
	}
	hf.PathID = string(data[pathIDStart : pathIDStart+int(pathIDLen)])
	
	return nil
}

// HeartbeatResponseFrame represents a heartbeat response frame
type HeartbeatResponseFrame struct {
	FrameID           uint64
	RequestSequenceID uint64 // Matches the request
	PathID            string
	PlaneType         HeartbeatPlaneType
	StreamID          *uint64 // Optional, for data plane heartbeats
	Timestamp         time.Time
	RequestTimestamp  time.Time // From original request for RTT calculation
	ServerLoad        float64   // Optional server load indicator
}

// Type returns the frame type (implements Frame interface)
func (hrf *HeartbeatResponseFrame) Type() FrameType {
	return FrameTypeHeartbeatResponse
}

// Serialize serializes the heartbeat response frame to bytes (implements Frame interface)
func (hrf *HeartbeatResponseFrame) Serialize() ([]byte, error) {
	// Format: [FrameID:8][RequestSequenceID:8][PlaneType:1][StreamID:9][Timestamp:8][RequestTimestamp:8][ServerLoad:8][PathIDLen:2][PathID:N]
	pathIDBytes := []byte(hrf.PathID)
	result := make([]byte, 0, 52+len(pathIDBytes))
	
	// FrameID (8 bytes)
	for i := 0; i < 8; i++ {
		result = append(result, byte(hrf.FrameID>>(8*(7-i))))
	}
	
	// RequestSequenceID (8 bytes)
	for i := 0; i < 8; i++ {
		result = append(result, byte(hrf.RequestSequenceID>>(8*(7-i))))
	}
	
	// PlaneType (1 byte)
	result = append(result, byte(hrf.PlaneType))
	
	// StreamID (9 bytes: 1 byte for presence + 8 bytes for value)
	if hrf.StreamID != nil {
		result = append(result, 1) // Present
		for i := 0; i < 8; i++ {
			result = append(result, byte(*hrf.StreamID>>(8*(7-i))))
		}
	} else {
		result = append(result, 0) // Not present
		for i := 0; i < 8; i++ {
			result = append(result, 0)
		}
	}
	
	// Timestamp (8 bytes - Unix nano)
	timestamp := uint64(hrf.Timestamp.UnixNano())
	for i := 0; i < 8; i++ {
		result = append(result, byte(timestamp>>(8*(7-i))))
	}
	
	// RequestTimestamp (8 bytes - Unix nano)
	requestTimestamp := uint64(hrf.RequestTimestamp.UnixNano())
	for i := 0; i < 8; i++ {
		result = append(result, byte(requestTimestamp>>(8*(7-i))))
	}
	
	// ServerLoad (8 bytes - float64 as uint64)
	serverLoadBits := *(*uint64)(unsafe.Pointer(&hrf.ServerLoad))
	for i := 0; i < 8; i++ {
		result = append(result, byte(serverLoadBits>>(8*(7-i))))
	}
	
	// PathIDLen (2 bytes)
	pathIDLen := uint16(len(pathIDBytes))
	result = append(result, byte(pathIDLen>>8))
	result = append(result, byte(pathIDLen))
	
	// PathID
	result = append(result, pathIDBytes...)
	
	return result, nil
}

// Deserialize deserializes bytes into the heartbeat response frame (implements Frame interface)
func (hrf *HeartbeatResponseFrame) Deserialize(data []byte) error {
	if len(data) < 50 { // Minimum size without PathID
		return fmt.Errorf("heartbeat response frame data too short: %d bytes", len(data))
	}
	
	// FrameID (8 bytes)
	hrf.FrameID = 0
	for i := 0; i < 8; i++ {
		hrf.FrameID = (hrf.FrameID << 8) | uint64(data[i])
	}
	
	// RequestSequenceID (8 bytes)
	hrf.RequestSequenceID = 0
	for i := 0; i < 8; i++ {
		hrf.RequestSequenceID = (hrf.RequestSequenceID << 8) | uint64(data[8+i])
	}
	
	// PlaneType (1 byte)
	hrf.PlaneType = HeartbeatPlaneType(data[16])
	
	// StreamID (9 bytes: 1 byte for presence + 8 bytes for value)
	if data[17] == 1 { // Present
		streamID := uint64(0)
		for i := 0; i < 8; i++ {
			streamID = (streamID << 8) | uint64(data[18+i])
		}
		hrf.StreamID = &streamID
	} else {
		hrf.StreamID = nil
	}
	
	// Timestamp (8 bytes)
	timestamp := uint64(0)
	for i := 0; i < 8; i++ {
		timestamp = (timestamp << 8) | uint64(data[26+i])
	}
	hrf.Timestamp = time.Unix(0, int64(timestamp))
	
	// RequestTimestamp (8 bytes)
	requestTimestamp := uint64(0)
	for i := 0; i < 8; i++ {
		requestTimestamp = (requestTimestamp << 8) | uint64(data[34+i])
	}
	hrf.RequestTimestamp = time.Unix(0, int64(requestTimestamp))
	
	// ServerLoad (8 bytes - uint64 as float64)
	serverLoadBits := uint64(0)
	for i := 0; i < 8; i++ {
		serverLoadBits = (serverLoadBits << 8) | uint64(data[42+i])
	}
	hrf.ServerLoad = *(*float64)(unsafe.Pointer(&serverLoadBits))
	
	// PathIDLen (2 bytes)
	if len(data) < 52 {
		return fmt.Errorf("heartbeat response frame incomplete: missing PathID length")
	}
	pathIDLen := uint16(data[50])<<8 | uint16(data[51])
	
	// PathID
	pathIDStart := 52
	if len(data) < pathIDStart+int(pathIDLen) {
		return fmt.Errorf("heartbeat response frame incomplete: missing PathID data")
	}
	hrf.PathID = string(data[pathIDStart : pathIDStart+int(pathIDLen)])
	
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