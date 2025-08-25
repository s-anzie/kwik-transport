package protocol

import (
	"testing"
	"time"
)

func TestHeartbeatFrameValidator_ValidateHeartbeatFrame(t *testing.T) {
	validator := NewHeartbeatFrameValidator(nil)
	
	tests := []struct {
		name    string
		frame   *HeartbeatFrame
		wantErr bool
	}{
		{
			name: "valid control plane heartbeat",
			frame: &HeartbeatFrame{
				FrameID:        123,
				SequenceID:     1,
				PathID:         "path-123",
				PlaneType:      HeartbeatPlaneControl,
				StreamID:       nil,
				Timestamp:      time.Now(),
				RTTMeasurement: true,
			},
			wantErr: false,
		},
		{
			name: "valid data plane heartbeat",
			frame: &HeartbeatFrame{
				FrameID:        124,
				SequenceID:     2,
				PathID:         "path-456",
				PlaneType:      HeartbeatPlaneData,
				StreamID:       func() *uint64 { id := uint64(789); return &id }(),
				Timestamp:      time.Now(),
				RTTMeasurement: false,
			},
			wantErr: false,
		},
		{
			name: "nil frame",
			frame: nil,
			wantErr: true,
		},
		{
			name: "zero frame ID",
			frame: &HeartbeatFrame{
				FrameID:    0,
				SequenceID: 1,
				PathID:     "path-123",
				PlaneType:  HeartbeatPlaneControl,
				Timestamp:  time.Now(),
			},
			wantErr: true,
		},
		{
			name: "zero sequence ID",
			frame: &HeartbeatFrame{
				FrameID:    123,
				SequenceID: 0,
				PathID:     "path-123",
				PlaneType:  HeartbeatPlaneControl,
				Timestamp:  time.Now(),
			},
			wantErr: true,
		},
		{
			name: "empty path ID",
			frame: &HeartbeatFrame{
				FrameID:    123,
				SequenceID: 1,
				PathID:     "",
				PlaneType:  HeartbeatPlaneControl,
				Timestamp:  time.Now(),
			},
			wantErr: true,
		},
		{
			name: "control plane with stream ID",
			frame: &HeartbeatFrame{
				FrameID:    123,
				SequenceID: 1,
				PathID:     "path-123",
				PlaneType:  HeartbeatPlaneControl,
				StreamID:   func() *uint64 { id := uint64(789); return &id }(),
				Timestamp:  time.Now(),
			},
			wantErr: true,
		},
		{
			name: "data plane without stream ID",
			frame: &HeartbeatFrame{
				FrameID:    123,
				SequenceID: 1,
				PathID:     "path-123",
				PlaneType:  HeartbeatPlaneData,
				StreamID:   nil,
				Timestamp:  time.Now(),
			},
			wantErr: true,
		},
		{
			name: "data plane with zero stream ID",
			frame: &HeartbeatFrame{
				FrameID:    123,
				SequenceID: 1,
				PathID:     "path-123",
				PlaneType:  HeartbeatPlaneData,
				StreamID:   func() *uint64 { id := uint64(0); return &id }(),
				Timestamp:  time.Now(),
			},
			wantErr: true,
		},
		{
			name: "zero timestamp",
			frame: &HeartbeatFrame{
				FrameID:    123,
				SequenceID: 1,
				PathID:     "path-123",
				PlaneType:  HeartbeatPlaneControl,
				Timestamp:  time.Time{},
			},
			wantErr: true,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidateHeartbeatFrame(tt.frame)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateHeartbeatFrame() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestHeartbeatFrameValidator_ValidateHeartbeatResponseFrame(t *testing.T) {
	validator := NewHeartbeatFrameValidator(nil)
	now := time.Now()
	requestTime := now.Add(-10 * time.Millisecond)
	
	tests := []struct {
		name    string
		frame   *HeartbeatResponseFrame
		wantErr bool
	}{
		{
			name: "valid control plane response",
			frame: &HeartbeatResponseFrame{
				FrameID:           123,
				RequestSequenceID: 1,
				PathID:            "path-123",
				PlaneType:         HeartbeatPlaneControl,
				StreamID:          nil,
				Timestamp:         now,
				RequestTimestamp:  requestTime,
				ServerLoad:        0.5,
			},
			wantErr: false,
		},
		{
			name: "valid data plane response",
			frame: &HeartbeatResponseFrame{
				FrameID:           124,
				RequestSequenceID: 2,
				PathID:            "path-456",
				PlaneType:         HeartbeatPlaneData,
				StreamID:          func() *uint64 { id := uint64(789); return &id }(),
				Timestamp:         now,
				RequestTimestamp:  requestTime,
				ServerLoad:        0.8,
			},
			wantErr: false,
		},
		{
			name: "nil frame",
			frame: nil,
			wantErr: true,
		},
		{
			name: "response before request timestamp",
			frame: &HeartbeatResponseFrame{
				FrameID:           123,
				RequestSequenceID: 1,
				PathID:            "path-123",
				PlaneType:         HeartbeatPlaneControl,
				Timestamp:         requestTime,
				RequestTimestamp:  now,
				ServerLoad:        0.5,
			},
			wantErr: true,
		},
		{
			name: "invalid server load (negative)",
			frame: &HeartbeatResponseFrame{
				FrameID:           123,
				RequestSequenceID: 1,
				PathID:            "path-123",
				PlaneType:         HeartbeatPlaneControl,
				Timestamp:         now,
				RequestTimestamp:  requestTime,
				ServerLoad:        -0.1,
			},
			wantErr: true,
		},
		{
			name: "invalid server load (too high)",
			frame: &HeartbeatResponseFrame{
				FrameID:           123,
				RequestSequenceID: 1,
				PathID:            "path-123",
				PlaneType:         HeartbeatPlaneControl,
				Timestamp:         now,
				RequestTimestamp:  requestTime,
				ServerLoad:        1.1,
			},
			wantErr: true,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidateHeartbeatResponseFrame(tt.frame)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateHeartbeatResponseFrame() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestHeartbeatFrameValidator_ValidatePathID(t *testing.T) {
	validator := NewHeartbeatFrameValidator(nil)
	
	tests := []struct {
		name    string
		pathID  string
		wantErr bool
	}{
		{
			name:    "valid path ID",
			pathID:  "path-123",
			wantErr: false,
		},
		{
			name:    "valid path ID with underscores",
			pathID:  "path_123_abc",
			wantErr: false,
		},
		{
			name:    "valid alphanumeric path ID",
			pathID:  "abc123XYZ",
			wantErr: false,
		},
		{
			name:    "empty path ID",
			pathID:  "",
			wantErr: true,
		},
		{
			name:    "path ID with invalid characters",
			pathID:  "path@123",
			wantErr: true,
		},
		{
			name:    "path ID with spaces",
			pathID:  "path 123",
			wantErr: true,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidatePathID(tt.pathID)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePathID() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestHeartbeatFrameValidator_ValidateTimestamp(t *testing.T) {
	validator := NewHeartbeatFrameValidator(nil)
	
	tests := []struct {
		name      string
		timestamp time.Time
		wantErr   bool
	}{
		{
			name:      "current timestamp",
			timestamp: time.Now(),
			wantErr:   false,
		},
		{
			name:      "recent past timestamp",
			timestamp: time.Now().Add(-5 * time.Second),
			wantErr:   false,
		},
		{
			name:      "recent future timestamp",
			timestamp: time.Now().Add(5 * time.Second),
			wantErr:   false,
		},
		{
			name:      "zero timestamp",
			timestamp: time.Time{},
			wantErr:   true,
		},
		{
			name:      "far past timestamp",
			timestamp: time.Now().Add(-1 * time.Minute),
			wantErr:   true,
		},
		{
			name:      "far future timestamp",
			timestamp: time.Now().Add(1 * time.Minute),
			wantErr:   true,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidateTimestamp(tt.timestamp)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateTimestamp() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestHeartbeatFrameIntegrity(t *testing.T) {
	validator := NewHeartbeatFrameValidator(nil)
	
	// Test heartbeat frame serialization/deserialization integrity
	original := &HeartbeatFrame{
		FrameID:        12345,
		SequenceID:     67890,
		PathID:         "test-path-123",
		PlaneType:      HeartbeatPlaneData,
		StreamID:       func() *uint64 { id := uint64(999); return &id }(),
		Timestamp:      time.Now().Truncate(time.Microsecond), // Truncate for serialization precision
		RTTMeasurement: true,
	}
	
	// Serialize
	data, err := original.Serialize()
	if err != nil {
		t.Fatalf("Failed to serialize heartbeat frame: %v", err)
	}
	
	// Deserialize
	deserialized := &HeartbeatFrame{}
	err = deserialized.Deserialize(data)
	if err != nil {
		t.Fatalf("Failed to deserialize heartbeat frame: %v", err)
	}
	
	// Validate integrity
	err = validator.ValidateHeartbeatFrameIntegrity(original, deserialized)
	if err != nil {
		t.Errorf("Heartbeat frame integrity validation failed: %v", err)
	}
}

func TestHeartbeatResponseFrameIntegrity(t *testing.T) {
	validator := NewHeartbeatFrameValidator(nil)
	
	// Test heartbeat response frame serialization/deserialization integrity
	now := time.Now().Truncate(time.Microsecond) // Truncate for serialization precision
	requestTime := now.Add(-10 * time.Millisecond)
	
	original := &HeartbeatResponseFrame{
		FrameID:           54321,
		RequestSequenceID: 98765,
		PathID:            "response-path-456",
		PlaneType:         HeartbeatPlaneControl,
		StreamID:          nil,
		Timestamp:         now,
		RequestTimestamp:  requestTime,
		ServerLoad:        0.75,
	}
	
	// Serialize
	data, err := original.Serialize()
	if err != nil {
		t.Fatalf("Failed to serialize heartbeat response frame: %v", err)
	}
	
	// Deserialize
	deserialized := &HeartbeatResponseFrame{}
	err = deserialized.Deserialize(data)
	if err != nil {
		t.Fatalf("Failed to deserialize heartbeat response frame: %v", err)
	}
	
	// Validate integrity
	err = validator.ValidateHeartbeatResponseFrameIntegrity(original, deserialized)
	if err != nil {
		t.Errorf("Heartbeat response frame integrity validation failed: %v", err)
	}
}