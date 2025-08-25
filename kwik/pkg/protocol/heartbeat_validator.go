package protocol

import (
	"fmt"
	"time"
)

// HeartbeatFrameValidator validates heartbeat frames for integrity and correctness
type HeartbeatFrameValidator interface {
	ValidateHeartbeatFrame(frame *HeartbeatFrame) error
	ValidateHeartbeatResponseFrame(frame *HeartbeatResponseFrame) error
	ValidateSequenceID(sequenceID uint64) error
	ValidateTimestamp(timestamp time.Time) error
	ValidatePathID(pathID string) error
	ValidateFrameSize(frameData []byte, maxSize int) error
}

// HeartbeatFrameValidatorImpl implements HeartbeatFrameValidator
type HeartbeatFrameValidatorImpl struct {
	maxSequenceID     uint64
	maxTimestampSkew  time.Duration
	maxPathIDLength   int
	minPathIDLength   int
}

// HeartbeatValidationConfig contains configuration for heartbeat validation
type HeartbeatValidationConfig struct {
	MaxSequenceID     uint64        // Maximum allowed sequence ID
	MaxTimestampSkew  time.Duration // Maximum allowed timestamp skew from current time
	MaxPathIDLength   int           // Maximum path ID length
	MinPathIDLength   int           // Minimum path ID length
}

// DefaultHeartbeatValidationConfig returns default validation configuration
func DefaultHeartbeatValidationConfig() *HeartbeatValidationConfig {
	return &HeartbeatValidationConfig{
		MaxSequenceID:    ^uint64(0), // Max uint64
		MaxTimestampSkew: 30 * time.Second,
		MaxPathIDLength:  256,
		MinPathIDLength:  1,
	}
}

// NewHeartbeatFrameValidator creates a new heartbeat frame validator
func NewHeartbeatFrameValidator(config *HeartbeatValidationConfig) *HeartbeatFrameValidatorImpl {
	if config == nil {
		config = DefaultHeartbeatValidationConfig()
	}
	
	return &HeartbeatFrameValidatorImpl{
		maxSequenceID:    config.MaxSequenceID,
		maxTimestampSkew: config.MaxTimestampSkew,
		maxPathIDLength:  config.MaxPathIDLength,
		minPathIDLength:  config.MinPathIDLength,
	}
}

// ValidateHeartbeatFrame validates a heartbeat request frame
func (v *HeartbeatFrameValidatorImpl) ValidateHeartbeatFrame(frame *HeartbeatFrame) error {
	if frame == nil {
		return fmt.Errorf("heartbeat frame is nil")
	}
	
	// Validate FrameID
	if frame.FrameID == 0 {
		return fmt.Errorf("heartbeat frame ID cannot be zero")
	}
	
	// Validate SequenceID
	if err := v.ValidateSequenceID(frame.SequenceID); err != nil {
		return fmt.Errorf("invalid sequence ID: %w", err)
	}
	
	// Validate PathID
	if err := v.ValidatePathID(frame.PathID); err != nil {
		return fmt.Errorf("invalid path ID: %w", err)
	}
	
	// Validate PlaneType
	if frame.PlaneType != HeartbeatPlaneControl && frame.PlaneType != HeartbeatPlaneData {
		return fmt.Errorf("invalid plane type: %d", frame.PlaneType)
	}
	
	// Validate StreamID consistency with PlaneType
	if frame.PlaneType == HeartbeatPlaneControl && frame.StreamID != nil {
		return fmt.Errorf("control plane heartbeat should not have stream ID")
	}
	
	if frame.PlaneType == HeartbeatPlaneData && frame.StreamID == nil {
		return fmt.Errorf("data plane heartbeat must have stream ID")
	}
	
	if frame.StreamID != nil && *frame.StreamID == 0 {
		return fmt.Errorf("data plane heartbeat stream ID cannot be zero")
	}
	
	// Validate Timestamp
	if err := v.ValidateTimestamp(frame.Timestamp); err != nil {
		return fmt.Errorf("invalid timestamp: %w", err)
	}
	
	return nil
}

// ValidateHeartbeatResponseFrame validates a heartbeat response frame
func (v *HeartbeatFrameValidatorImpl) ValidateHeartbeatResponseFrame(frame *HeartbeatResponseFrame) error {
	if frame == nil {
		return fmt.Errorf("heartbeat response frame is nil")
	}
	
	// Validate FrameID
	if frame.FrameID == 0 {
		return fmt.Errorf("heartbeat response frame ID cannot be zero")
	}
	
	// Validate RequestSequenceID
	if err := v.ValidateSequenceID(frame.RequestSequenceID); err != nil {
		return fmt.Errorf("invalid request sequence ID: %w", err)
	}
	
	// Validate PathID
	if err := v.ValidatePathID(frame.PathID); err != nil {
		return fmt.Errorf("invalid path ID: %w", err)
	}
	
	// Validate PlaneType
	if frame.PlaneType != HeartbeatPlaneControl && frame.PlaneType != HeartbeatPlaneData {
		return fmt.Errorf("invalid plane type: %d", frame.PlaneType)
	}
	
	// Validate StreamID consistency with PlaneType
	if frame.PlaneType == HeartbeatPlaneControl && frame.StreamID != nil {
		return fmt.Errorf("control plane heartbeat response should not have stream ID")
	}
	
	if frame.PlaneType == HeartbeatPlaneData && frame.StreamID == nil {
		return fmt.Errorf("data plane heartbeat response must have stream ID")
	}
	
	if frame.StreamID != nil && *frame.StreamID == 0 {
		return fmt.Errorf("data plane heartbeat response stream ID cannot be zero")
	}
	
	// Validate Timestamp
	if err := v.ValidateTimestamp(frame.Timestamp); err != nil {
		return fmt.Errorf("invalid timestamp: %w", err)
	}
	
	// Validate RequestTimestamp
	if err := v.ValidateTimestamp(frame.RequestTimestamp); err != nil {
		return fmt.Errorf("invalid request timestamp: %w", err)
	}
	
	// Validate timestamp ordering (response should be after request)
	if frame.Timestamp.Before(frame.RequestTimestamp) {
		return fmt.Errorf("response timestamp (%v) is before request timestamp (%v)", 
			frame.Timestamp, frame.RequestTimestamp)
	}
	
	// Validate ServerLoad (should be between 0.0 and 1.0)
	if frame.ServerLoad < 0.0 || frame.ServerLoad > 1.0 {
		return fmt.Errorf("invalid server load: %f (must be between 0.0 and 1.0)", frame.ServerLoad)
	}
	
	return nil
}

// ValidateSequenceID validates a sequence ID
func (v *HeartbeatFrameValidatorImpl) ValidateSequenceID(sequenceID uint64) error {
	if sequenceID == 0 {
		return fmt.Errorf("sequence ID cannot be zero")
	}
	
	if sequenceID > v.maxSequenceID {
		return fmt.Errorf("sequence ID %d exceeds maximum %d", sequenceID, v.maxSequenceID)
	}
	
	return nil
}

// ValidateTimestamp validates a timestamp
func (v *HeartbeatFrameValidatorImpl) ValidateTimestamp(timestamp time.Time) error {
	if timestamp.IsZero() {
		return fmt.Errorf("timestamp cannot be zero")
	}
	
	now := time.Now()
	skew := timestamp.Sub(now)
	if skew < 0 {
		skew = -skew
	}
	
	if skew > v.maxTimestampSkew {
		return fmt.Errorf("timestamp skew %v exceeds maximum allowed %v", skew, v.maxTimestampSkew)
	}
	
	return nil
}

// ValidatePathID validates a path ID
func (v *HeartbeatFrameValidatorImpl) ValidatePathID(pathID string) error {
	if len(pathID) < v.minPathIDLength {
		return fmt.Errorf("path ID length %d is below minimum %d", len(pathID), v.minPathIDLength)
	}
	
	if len(pathID) > v.maxPathIDLength {
		return fmt.Errorf("path ID length %d exceeds maximum %d", len(pathID), v.maxPathIDLength)
	}
	
	// Check for valid characters (alphanumeric, dash, underscore)
	for i, r := range pathID {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || 
			 (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return fmt.Errorf("invalid character '%c' at position %d in path ID", r, i)
		}
	}
	
	return nil
}

// ValidateFrameSize validates the serialized frame size
func (v *HeartbeatFrameValidatorImpl) ValidateFrameSize(frameData []byte, maxSize int) error {
	if len(frameData) == 0 {
		return fmt.Errorf("frame data is empty")
	}
	
	if len(frameData) > maxSize {
		return fmt.Errorf("frame size %d exceeds maximum %d", len(frameData), maxSize)
	}
	
	return nil
}

// ValidateHeartbeatFrameIntegrity validates frame integrity after serialization/deserialization
func (v *HeartbeatFrameValidatorImpl) ValidateHeartbeatFrameIntegrity(original *HeartbeatFrame, deserialized *HeartbeatFrame) error {
	if original.FrameID != deserialized.FrameID {
		return fmt.Errorf("frame ID mismatch: original %d, deserialized %d", original.FrameID, deserialized.FrameID)
	}
	
	if original.SequenceID != deserialized.SequenceID {
		return fmt.Errorf("sequence ID mismatch: original %d, deserialized %d", original.SequenceID, deserialized.SequenceID)
	}
	
	if original.PathID != deserialized.PathID {
		return fmt.Errorf("path ID mismatch: original %s, deserialized %s", original.PathID, deserialized.PathID)
	}
	
	if original.PlaneType != deserialized.PlaneType {
		return fmt.Errorf("plane type mismatch: original %d, deserialized %d", original.PlaneType, deserialized.PlaneType)
	}
	
	// Check StreamID (handle nil cases)
	if (original.StreamID == nil) != (deserialized.StreamID == nil) {
		return fmt.Errorf("stream ID presence mismatch")
	}
	
	if original.StreamID != nil && *original.StreamID != *deserialized.StreamID {
		return fmt.Errorf("stream ID mismatch: original %d, deserialized %d", *original.StreamID, *deserialized.StreamID)
	}
	
	if original.RTTMeasurement != deserialized.RTTMeasurement {
		return fmt.Errorf("RTT measurement flag mismatch: original %t, deserialized %t", original.RTTMeasurement, deserialized.RTTMeasurement)
	}
	
	// Allow small timestamp differences due to serialization precision
	timeDiff := original.Timestamp.Sub(deserialized.Timestamp)
	if timeDiff < 0 {
		timeDiff = -timeDiff
	}
	if timeDiff > time.Microsecond {
		return fmt.Errorf("timestamp mismatch: original %v, deserialized %v", original.Timestamp, deserialized.Timestamp)
	}
	
	return nil
}

// ValidateHeartbeatResponseFrameIntegrity validates response frame integrity after serialization/deserialization
func (v *HeartbeatFrameValidatorImpl) ValidateHeartbeatResponseFrameIntegrity(original *HeartbeatResponseFrame, deserialized *HeartbeatResponseFrame) error {
	if original.FrameID != deserialized.FrameID {
		return fmt.Errorf("frame ID mismatch: original %d, deserialized %d", original.FrameID, deserialized.FrameID)
	}
	
	if original.RequestSequenceID != deserialized.RequestSequenceID {
		return fmt.Errorf("request sequence ID mismatch: original %d, deserialized %d", original.RequestSequenceID, deserialized.RequestSequenceID)
	}
	
	if original.PathID != deserialized.PathID {
		return fmt.Errorf("path ID mismatch: original %s, deserialized %s", original.PathID, deserialized.PathID)
	}
	
	if original.PlaneType != deserialized.PlaneType {
		return fmt.Errorf("plane type mismatch: original %d, deserialized %d", original.PlaneType, deserialized.PlaneType)
	}
	
	// Check StreamID (handle nil cases)
	if (original.StreamID == nil) != (deserialized.StreamID == nil) {
		return fmt.Errorf("stream ID presence mismatch")
	}
	
	if original.StreamID != nil && *original.StreamID != *deserialized.StreamID {
		return fmt.Errorf("stream ID mismatch: original %d, deserialized %d", *original.StreamID, *deserialized.StreamID)
	}
	
	// Allow small timestamp differences due to serialization precision
	timeDiff := original.Timestamp.Sub(deserialized.Timestamp)
	if timeDiff < 0 {
		timeDiff = -timeDiff
	}
	if timeDiff > time.Microsecond {
		return fmt.Errorf("timestamp mismatch: original %v, deserialized %v", original.Timestamp, deserialized.Timestamp)
	}
	
	requestTimeDiff := original.RequestTimestamp.Sub(deserialized.RequestTimestamp)
	if requestTimeDiff < 0 {
		requestTimeDiff = -requestTimeDiff
	}
	if requestTimeDiff > time.Microsecond {
		return fmt.Errorf("request timestamp mismatch: original %v, deserialized %v", original.RequestTimestamp, deserialized.RequestTimestamp)
	}
	
	// Allow small float differences due to serialization precision
	loadDiff := original.ServerLoad - deserialized.ServerLoad
	if loadDiff < 0 {
		loadDiff = -loadDiff
	}
	if loadDiff > 0.0001 {
		return fmt.Errorf("server load mismatch: original %f, deserialized %f", original.ServerLoad, deserialized.ServerLoad)
	}
	
	return nil
}