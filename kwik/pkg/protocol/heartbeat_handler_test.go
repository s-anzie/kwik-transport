package protocol

import (
	"fmt"
	"testing"
	"time"
)

// Mock implementations for testing
type mockControlHeartbeatSystem struct {
	requestsReceived  []*HeartbeatFrame
	responsesReceived []*HeartbeatResponseFrame
	shouldError       bool
}

func (m *mockControlHeartbeatSystem) HandleControlHeartbeatRequest(request *HeartbeatFrame) error {
	m.requestsReceived = append(m.requestsReceived, request)
	if m.shouldError {
		return fmt.Errorf("mock error")
	}
	return nil
}

func (m *mockControlHeartbeatSystem) HandleControlHeartbeatResponse(response *HeartbeatResponseFrame) error {
	m.responsesReceived = append(m.responsesReceived, response)
	if m.shouldError {
		return fmt.Errorf("mock error")
	}
	return nil
}

type mockDataHeartbeatSystem struct {
	requestsReceived  []*HeartbeatFrame
	responsesReceived []*HeartbeatResponseFrame
	shouldError       bool
}

func (m *mockDataHeartbeatSystem) HandleDataHeartbeatRequest(request *HeartbeatFrame) error {
	m.requestsReceived = append(m.requestsReceived, request)
	if m.shouldError {
		return fmt.Errorf("mock error")
	}
	return nil
}

func (m *mockDataHeartbeatSystem) HandleDataHeartbeatResponse(response *HeartbeatResponseFrame) error {
	m.responsesReceived = append(m.responsesReceived, response)
	if m.shouldError {
		return fmt.Errorf("mock error")
	}
	return nil
}

func TestHeartbeatFrameHandler_HandleHeartbeatFrame(t *testing.T) {
	handler := NewHeartbeatFrameHandler(nil)
	
	// Register mock systems
	mockControl := &mockControlHeartbeatSystem{}
	mockData := &mockDataHeartbeatSystem{}
	handler.RegisterControlHeartbeatSystem(mockControl)
	handler.RegisterDataHeartbeatSystem(mockData)
	
	tests := []struct {
		name    string
		frame   Frame
		pathID  string
		wantErr bool
	}{
		{
			name: "valid control plane heartbeat",
			frame: &HeartbeatFrame{
				FrameID:    123,
				SequenceID: 1,
				PathID:     "path-123",
				PlaneType:  HeartbeatPlaneControl,
				StreamID:   nil,
				Timestamp:  time.Now(),
			},
			pathID:  "path-123",
			wantErr: false,
		},
		{
			name: "valid data plane heartbeat",
			frame: &HeartbeatFrame{
				FrameID:    124,
				SequenceID: 2,
				PathID:     "path-456",
				PlaneType:  HeartbeatPlaneData,
				StreamID:   func() *uint64 { id := uint64(789); return &id }(),
				Timestamp:  time.Now(),
			},
			pathID:  "path-456",
			wantErr: false,
		},
		{
			name: "wrong frame type",
			frame: &ControlFrame{
				FrameID:   123,
				FrameType: FrameTypeData,
				Timestamp: time.Now(),
			},
			pathID:  "path-123",
			wantErr: true,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := handler.HandleHeartbeatFrame(tt.frame, tt.pathID)
			if (err != nil) != tt.wantErr {
				t.Errorf("HandleHeartbeatFrame() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
	
	// Verify that frames were routed correctly
	if len(mockControl.requestsReceived) != 1 {
		t.Errorf("Expected 1 control request, got %d", len(mockControl.requestsReceived))
	}
	
	if len(mockData.requestsReceived) != 1 {
		t.Errorf("Expected 1 data request, got %d", len(mockData.requestsReceived))
	}
}

func TestHeartbeatFrameHandler_HandleHeartbeatResponseFrame(t *testing.T) {
	handler := NewHeartbeatFrameHandler(nil)
	
	// Register mock systems
	mockControl := &mockControlHeartbeatSystem{}
	mockData := &mockDataHeartbeatSystem{}
	handler.RegisterControlHeartbeatSystem(mockControl)
	handler.RegisterDataHeartbeatSystem(mockData)
	
	now := time.Now()
	requestTime := now.Add(-10 * time.Millisecond)
	
	tests := []struct {
		name    string
		frame   Frame
		pathID  string
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
			pathID:  "path-123",
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
			pathID:  "path-456",
			wantErr: false,
		},
		{
			name: "wrong frame type",
			frame: &ControlFrame{
				FrameID:   123,
				FrameType: FrameTypeData,
				Timestamp: time.Now(),
			},
			pathID:  "path-123",
			wantErr: true,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := handler.HandleHeartbeatResponseFrame(tt.frame, tt.pathID)
			if (err != nil) != tt.wantErr {
				t.Errorf("HandleHeartbeatResponseFrame() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
	
	// Verify that responses were routed correctly
	if len(mockControl.responsesReceived) != 1 {
		t.Errorf("Expected 1 control response, got %d", len(mockControl.responsesReceived))
	}
	
	if len(mockData.responsesReceived) != 1 {
		t.Errorf("Expected 1 data response, got %d", len(mockData.responsesReceived))
	}
}

func TestHeartbeatFrameHandler_CreateHeartbeatFrame(t *testing.T) {
	handler := NewHeartbeatFrameHandler(nil)
	
	tests := []struct {
		name           string
		frameID        uint64
		sequenceID     uint64
		pathID         string
		planeType      HeartbeatPlaneType
		streamID       *uint64
		rttMeasurement bool
		wantErr        bool
	}{
		{
			name:           "valid control plane frame",
			frameID:        123,
			sequenceID:     1,
			pathID:         "path-123",
			planeType:      HeartbeatPlaneControl,
			streamID:       nil,
			rttMeasurement: true,
			wantErr:        false,
		},
		{
			name:           "valid data plane frame",
			frameID:        124,
			sequenceID:     2,
			pathID:         "path-456",
			planeType:      HeartbeatPlaneData,
			streamID:       func() *uint64 { id := uint64(789); return &id }(),
			rttMeasurement: false,
			wantErr:        false,
		},
		{
			name:           "invalid - control plane with stream ID",
			frameID:        125,
			sequenceID:     3,
			pathID:         "path-789",
			planeType:      HeartbeatPlaneControl,
			streamID:       func() *uint64 { id := uint64(999); return &id }(),
			rttMeasurement: false,
			wantErr:        true,
		},
		{
			name:           "invalid - data plane without stream ID",
			frameID:        126,
			sequenceID:     4,
			pathID:         "path-abc",
			planeType:      HeartbeatPlaneData,
			streamID:       nil,
			rttMeasurement: false,
			wantErr:        true,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			frame, err := handler.CreateHeartbeatFrame(
				tt.frameID,
				tt.sequenceID,
				tt.pathID,
				tt.planeType,
				tt.streamID,
				tt.rttMeasurement,
			)
			
			if (err != nil) != tt.wantErr {
				t.Errorf("CreateHeartbeatFrame() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			
			if !tt.wantErr && frame == nil {
				t.Error("CreateHeartbeatFrame() returned nil frame without error")
			}
			
			if !tt.wantErr {
				// Verify frame properties
				if frame.FrameID != tt.frameID {
					t.Errorf("Expected FrameID %d, got %d", tt.frameID, frame.FrameID)
				}
				if frame.SequenceID != tt.sequenceID {
					t.Errorf("Expected SequenceID %d, got %d", tt.sequenceID, frame.SequenceID)
				}
				if frame.PathID != tt.pathID {
					t.Errorf("Expected PathID %s, got %s", tt.pathID, frame.PathID)
				}
				if frame.PlaneType != tt.planeType {
					t.Errorf("Expected PlaneType %d, got %d", tt.planeType, frame.PlaneType)
				}
			}
		})
	}
}

func TestHeartbeatFrameHandler_CreateHeartbeatResponseFrame(t *testing.T) {
	handler := NewHeartbeatFrameHandler(nil)
	
	now := time.Now()
	requestTime := now.Add(-10 * time.Millisecond)
	
	tests := []struct {
		name              string
		frameID           uint64
		requestSequenceID uint64
		pathID            string
		planeType         HeartbeatPlaneType
		streamID          *uint64
		requestTimestamp  time.Time
		serverLoad        float64
		wantErr           bool
	}{
		{
			name:              "valid control plane response",
			frameID:           123,
			requestSequenceID: 1,
			pathID:            "path-123",
			planeType:         HeartbeatPlaneControl,
			streamID:          nil,
			requestTimestamp:  requestTime,
			serverLoad:        0.5,
			wantErr:           false,
		},
		{
			name:              "valid data plane response",
			frameID:           124,
			requestSequenceID: 2,
			pathID:            "path-456",
			planeType:         HeartbeatPlaneData,
			streamID:          func() *uint64 { id := uint64(789); return &id }(),
			requestTimestamp:  requestTime,
			serverLoad:        0.8,
			wantErr:           false,
		},
		{
			name:              "invalid server load",
			frameID:           125,
			requestSequenceID: 3,
			pathID:            "path-789",
			planeType:         HeartbeatPlaneControl,
			streamID:          nil,
			requestTimestamp:  requestTime,
			serverLoad:        1.5, // Invalid - too high
			wantErr:           true,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			frame, err := handler.CreateHeartbeatResponseFrame(
				tt.frameID,
				tt.requestSequenceID,
				tt.pathID,
				tt.planeType,
				tt.streamID,
				tt.requestTimestamp,
				tt.serverLoad,
			)
			
			if (err != nil) != tt.wantErr {
				t.Errorf("CreateHeartbeatResponseFrame() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			
			if !tt.wantErr && frame == nil {
				t.Error("CreateHeartbeatResponseFrame() returned nil frame without error")
			}
			
			if !tt.wantErr {
				// Verify frame properties
				if frame.FrameID != tt.frameID {
					t.Errorf("Expected FrameID %d, got %d", tt.frameID, frame.FrameID)
				}
				if frame.RequestSequenceID != tt.requestSequenceID {
					t.Errorf("Expected RequestSequenceID %d, got %d", tt.requestSequenceID, frame.RequestSequenceID)
				}
				if frame.ServerLoad != tt.serverLoad {
					t.Errorf("Expected ServerLoad %f, got %f", tt.serverLoad, frame.ServerLoad)
				}
			}
		})
	}
}

func TestHeartbeatFrameHandler_GetFrameStats(t *testing.T) {
	handler := NewHeartbeatFrameHandler(nil)
	
	// Register mock systems
	mockControl := &mockControlHeartbeatSystem{}
	mockData := &mockDataHeartbeatSystem{}
	handler.RegisterControlHeartbeatSystem(mockControl)
	handler.RegisterDataHeartbeatSystem(mockData)
	
	// Process some frames to generate stats
	controlFrame := &HeartbeatFrame{
		FrameID:    123,
		SequenceID: 1,
		PathID:     "path-123",
		PlaneType:  HeartbeatPlaneControl,
		Timestamp:  time.Now(),
	}
	
	dataFrame := &HeartbeatFrame{
		FrameID:    124,
		SequenceID: 2,
		PathID:     "path-456",
		PlaneType:  HeartbeatPlaneData,
		StreamID:   func() *uint64 { id := uint64(789); return &id }(),
		Timestamp:  time.Now(),
	}
	
	// Process frames
	handler.HandleHeartbeatFrame(controlFrame, "path-123")
	handler.HandleHeartbeatFrame(dataFrame, "path-456")
	
	// Get stats
	stats := handler.GetFrameStats()
	
	// Verify stats
	if stats.TotalHeartbeatsReceived != 2 {
		t.Errorf("Expected 2 total heartbeats, got %d", stats.TotalHeartbeatsReceived)
	}
	
	if stats.ControlPlaneFrames != 1 {
		t.Errorf("Expected 1 control plane frame, got %d", stats.ControlPlaneFrames)
	}
	
	if stats.DataPlaneFrames != 1 {
		t.Errorf("Expected 1 data plane frame, got %d", stats.DataPlaneFrames)
	}
	
	if len(stats.PathStats) != 2 {
		t.Errorf("Expected 2 path stats, got %d", len(stats.PathStats))
	}
	
	// Verify path-specific stats
	path123Stats, exists := stats.PathStats["path-123"]
	if !exists {
		t.Error("Expected stats for path-123")
	} else if path123Stats.HeartbeatsReceived != 1 {
		t.Errorf("Expected 1 heartbeat for path-123, got %d", path123Stats.HeartbeatsReceived)
	}
	
	path456Stats, exists := stats.PathStats["path-456"]
	if !exists {
		t.Error("Expected stats for path-456")
	} else if path456Stats.HeartbeatsReceived != 1 {
		t.Errorf("Expected 1 heartbeat for path-456, got %d", path456Stats.HeartbeatsReceived)
	}
}