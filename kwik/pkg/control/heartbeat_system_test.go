package control

import (
	"testing"
	"time"

	"kwik/pkg/protocol"
)

// Mock ControlPlane for testing
type mockControlPlane struct {
	sentFrames []protocol.Frame
	pathFrames map[string][]protocol.Frame
}

func newMockControlPlane() *mockControlPlane {
	return &mockControlPlane{
		sentFrames: make([]protocol.Frame, 0),
		pathFrames: make(map[string][]protocol.Frame),
	}
}

func (m *mockControlPlane) SendFrame(pathID string, frame protocol.Frame) error {
	m.sentFrames = append(m.sentFrames, frame)
	if m.pathFrames[pathID] == nil {
		m.pathFrames[pathID] = make([]protocol.Frame, 0)
	}
	m.pathFrames[pathID] = append(m.pathFrames[pathID], frame)
	return nil
}

func (m *mockControlPlane) ReceiveFrame(pathID string) (protocol.Frame, error) {
	return nil, nil
}

func (m *mockControlPlane) HandleAddPathRequest(req *AddPathRequest) error {
	return nil
}

func (m *mockControlPlane) HandleRemovePathRequest(req *RemovePathRequest) error {
	return nil
}

func (m *mockControlPlane) HandleAuthenticationRequest(req *AuthenticationRequest) error {
	return nil
}

func (m *mockControlPlane) HandleRawPacketTransmission(req *RawPacketTransmission) error {
	return nil
}

func (m *mockControlPlane) SendPathStatusNotification(pathID string, status PathStatus) error {
	return nil
}

func TestControlPlaneHeartbeatSystem_StartStopHeartbeats(t *testing.T) {
	mockCP := newMockControlPlane()
	system := NewControlPlaneHeartbeatSystem(mockCP, nil, nil)
	
	sessionID := "test-session"
	pathID := "test-path"
	
	// Test starting heartbeats
	err := system.StartControlHeartbeats(sessionID, pathID)
	if err != nil {
		t.Fatalf("Failed to start control heartbeats: %v", err)
	}
	
	// Verify state was created
	stats := system.GetControlHeartbeatStats(pathID)
	if stats == nil {
		t.Fatal("Expected heartbeat stats to be created")
	}
	
	if stats.SessionID != sessionID {
		t.Errorf("Expected session ID %s, got %s", sessionID, stats.SessionID)
	}
	
	if stats.PathID != pathID {
		t.Errorf("Expected path ID %s, got %s", pathID, stats.PathID)
	}
	
	if !stats.IsActive {
		t.Error("Expected heartbeats to be active")
	}
	
	// Test stopping heartbeats
	err = system.StopControlHeartbeats(sessionID, pathID)
	if err != nil {
		t.Fatalf("Failed to stop control heartbeats: %v", err)
	}
	
	// Verify state was removed
	stats = system.GetControlHeartbeatStats(pathID)
	if stats != nil {
		t.Error("Expected heartbeat stats to be removed")
	}
}

func TestControlPlaneHeartbeatSystem_SendHeartbeat(t *testing.T) {
	mockCP := newMockControlPlane()
	system := NewControlPlaneHeartbeatSystem(mockCP, nil, nil)
	
	sessionID := "test-session"
	pathID := "test-path"
	
	// Start heartbeats
	err := system.StartControlHeartbeats(sessionID, pathID)
	if err != nil {
		t.Fatalf("Failed to start control heartbeats: %v", err)
	}
	
	// Send heartbeat
	err = system.SendControlHeartbeat(pathID)
	if err != nil {
		t.Fatalf("Failed to send control heartbeat: %v", err)
	}
	
	// Verify frame was sent
	if len(mockCP.sentFrames) != 1 {
		t.Fatalf("Expected 1 frame to be sent, got %d", len(mockCP.sentFrames))
	}
	
	frame, ok := mockCP.sentFrames[0].(*protocol.HeartbeatFrame)
	if !ok {
		t.Fatal("Expected HeartbeatFrame to be sent")
	}
	
	if frame.PathID != pathID {
		t.Errorf("Expected path ID %s, got %s", pathID, frame.PathID)
	}
	
	if frame.PlaneType != protocol.HeartbeatPlaneControl {
		t.Errorf("Expected control plane type, got %d", frame.PlaneType)
	}
	
	if frame.StreamID != nil {
		t.Error("Expected control plane heartbeat to have nil stream ID")
	}
	
	if frame.SequenceID != 1 {
		t.Errorf("Expected sequence ID 1, got %d", frame.SequenceID)
	}
	
	// Verify statistics were updated
	stats := system.GetControlHeartbeatStats(pathID)
	if stats.HeartbeatsSent != 1 {
		t.Errorf("Expected 1 heartbeat sent, got %d", stats.HeartbeatsSent)
	}
}

func TestControlPlaneHeartbeatSystem_HandleHeartbeatRequest(t *testing.T) {
	mockCP := newMockControlPlane()
	system := NewControlPlaneHeartbeatSystem(mockCP, nil, nil)
	system.SetServerMode(true) // Set as server to respond to requests
	
	sessionID := "test-session"
	pathID := "test-path"
	
	// Start heartbeats (to create session state)
	err := system.StartControlHeartbeats(sessionID, pathID)
	if err != nil {
		t.Fatalf("Failed to start control heartbeats: %v", err)
	}
	
	// Create heartbeat request
	request := &protocol.HeartbeatFrame{
		FrameID:        123,
		SequenceID:     1,
		PathID:         pathID,
		PlaneType:      protocol.HeartbeatPlaneControl,
		StreamID:       nil,
		Timestamp:      time.Now(),
		RTTMeasurement: true,
	}
	
	// Handle request
	err = system.HandleControlHeartbeatRequest(request)
	if err != nil {
		t.Fatalf("Failed to handle control heartbeat request: %v", err)
	}
	
	// Verify response was sent
	if len(mockCP.sentFrames) != 1 {
		t.Fatalf("Expected 1 response frame to be sent, got %d", len(mockCP.sentFrames))
	}
	
	responseFrame, ok := mockCP.sentFrames[0].(*protocol.HeartbeatResponseFrame)
	if !ok {
		t.Fatal("Expected HeartbeatResponseFrame to be sent")
	}
	
	if responseFrame.RequestSequenceID != request.SequenceID {
		t.Errorf("Expected request sequence ID %d, got %d", request.SequenceID, responseFrame.RequestSequenceID)
	}
	
	if responseFrame.PathID != pathID {
		t.Errorf("Expected path ID %s, got %s", pathID, responseFrame.PathID)
	}
	
	if responseFrame.PlaneType != protocol.HeartbeatPlaneControl {
		t.Errorf("Expected control plane type, got %d", responseFrame.PlaneType)
	}
	
	// Verify statistics were updated
	stats := system.GetControlHeartbeatStats(pathID)
	if stats.HeartbeatsReceived != 1 {
		t.Errorf("Expected 1 heartbeat received, got %d", stats.HeartbeatsReceived)
	}
	
	if stats.ResponsesSent != 1 {
		t.Errorf("Expected 1 response sent, got %d", stats.ResponsesSent)
	}
}

func TestControlPlaneHeartbeatSystem_HandleHeartbeatResponse(t *testing.T) {
	mockCP := newMockControlPlane()
	system := NewControlPlaneHeartbeatSystem(mockCP, nil, nil)
	
	sessionID := "test-session"
	pathID := "test-path"
	
	// Start heartbeats
	err := system.StartControlHeartbeats(sessionID, pathID)
	if err != nil {
		t.Fatalf("Failed to start control heartbeats: %v", err)
	}
	
	// Send heartbeat to create pending request
	err = system.SendControlHeartbeat(pathID)
	if err != nil {
		t.Fatalf("Failed to send control heartbeat: %v", err)
	}
	
	// Get the sent heartbeat frame to create matching response
	sentFrame := mockCP.sentFrames[0].(*protocol.HeartbeatFrame)
	
	// Create heartbeat response
	now := time.Now()
	response := &protocol.HeartbeatResponseFrame{
		FrameID:           456,
		RequestSequenceID: sentFrame.SequenceID,
		PathID:            pathID,
		PlaneType:         protocol.HeartbeatPlaneControl,
		StreamID:          nil,
		Timestamp:         now,
		RequestTimestamp:  sentFrame.Timestamp,
		ServerLoad:        0.5,
	}
	
	// Handle response
	err = system.HandleControlHeartbeatResponse(response)
	if err != nil {
		t.Fatalf("Failed to handle control heartbeat response: %v", err)
	}
	
	// Verify statistics were updated
	stats := system.GetControlHeartbeatStats(pathID)
	if stats.ResponsesReceived != 1 {
		t.Errorf("Expected 1 response received, got %d", stats.ResponsesReceived)
	}
	
	// Verify RTT was calculated (since the original request had RTTMeasurement = true by default)
	if stats.AverageRTT == 0 {
		t.Error("Expected RTT to be calculated and non-zero")
	}
}

func TestControlPlaneHeartbeatSystem_ServerMode(t *testing.T) {
	mockCP := newMockControlPlane()
	system := NewControlPlaneHeartbeatSystem(mockCP, nil, nil)
	
	// Test client mode (default)
	system.SetServerMode(false)
	
	sessionID := "test-session"
	pathID := "test-path"
	
	err := system.StartControlHeartbeats(sessionID, pathID)
	if err != nil {
		t.Fatalf("Failed to start control heartbeats in client mode: %v", err)
	}
	
	// In client mode, heartbeats should be sent automatically
	// (We can't easily test the automatic sending without waiting, but we can test manual sending)
	
	// Test server mode
	system.SetServerMode(true)
	
	// Create heartbeat request from unknown path (server should still respond)
	unknownPathID := "unknown-path"
	request := &protocol.HeartbeatFrame{
		FrameID:    123,
		SequenceID: 1,
		PathID:     unknownPathID,
		PlaneType:  protocol.HeartbeatPlaneControl,
		Timestamp:  time.Now(),
	}
	
	// Handle request (should succeed even for unknown path in server mode)
	err = system.HandleControlHeartbeatRequest(request)
	if err != nil {
		t.Fatalf("Server should handle heartbeat from unknown path: %v", err)
	}
	
	// Verify response was sent
	responseFound := false
	for _, frame := range mockCP.sentFrames {
		if responseFrame, ok := frame.(*protocol.HeartbeatResponseFrame); ok {
			if responseFrame.PathID == unknownPathID {
				responseFound = true
				break
			}
		}
	}
	
	if !responseFound {
		t.Error("Expected server to send response for unknown path heartbeat")
	}
}

func TestControlPlaneHeartbeatSystem_GetAllStats(t *testing.T) {
	mockCP := newMockControlPlane()
	system := NewControlPlaneHeartbeatSystem(mockCP, nil, nil)
	
	// Start heartbeats for multiple sessions
	sessions := []struct {
		sessionID string
		pathID    string
	}{
		{"session-1", "path-1"},
		{"session-2", "path-2"},
		{"session-3", "path-3"},
	}
	
	for _, s := range sessions {
		err := system.StartControlHeartbeats(s.sessionID, s.pathID)
		if err != nil {
			t.Fatalf("Failed to start heartbeats for %s: %v", s.sessionID, err)
		}
	}
	
	// Get all stats
	allStats := system.GetAllControlHeartbeatStats()
	
	if len(allStats) != len(sessions) {
		t.Errorf("Expected %d session stats, got %d", len(sessions), len(allStats))
	}
	
	// Verify each session has stats
	for _, s := range sessions {
		stats, exists := allStats[s.sessionID]
		if !exists {
			t.Errorf("Expected stats for session %s", s.sessionID)
			continue
		}
		
		if stats.SessionID != s.sessionID {
			t.Errorf("Expected session ID %s, got %s", s.sessionID, stats.SessionID)
		}
		
		if stats.PathID != s.pathID {
			t.Errorf("Expected path ID %s, got %s", s.pathID, stats.PathID)
		}
		
		if !stats.IsActive {
			t.Errorf("Expected session %s to be active", s.sessionID)
		}
	}
}

func TestControlPlaneHeartbeatSystem_Shutdown(t *testing.T) {
	mockCP := newMockControlPlane()
	system := NewControlPlaneHeartbeatSystem(mockCP, nil, nil)
	
	// Start some heartbeats
	err := system.StartControlHeartbeats("session-1", "path-1")
	if err != nil {
		t.Fatalf("Failed to start heartbeats: %v", err)
	}
	
	err = system.StartControlHeartbeats("session-2", "path-2")
	if err != nil {
		t.Fatalf("Failed to start heartbeats: %v", err)
	}
	
	// Verify sessions exist
	allStats := system.GetAllControlHeartbeatStats()
	if len(allStats) != 2 {
		t.Fatalf("Expected 2 sessions before shutdown, got %d", len(allStats))
	}
	
	// Shutdown
	err = system.Shutdown()
	if err != nil {
		t.Fatalf("Failed to shutdown: %v", err)
	}
	
	// Verify all sessions were stopped
	allStats = system.GetAllControlHeartbeatStats()
	if len(allStats) != 0 {
		t.Errorf("Expected 0 sessions after shutdown, got %d", len(allStats))
	}
}