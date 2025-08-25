package session

import (
	"testing"
	"time"

	"kwik/internal/utils"
	"kwik/pkg/data"
	"kwik/pkg/transport"
)

// TestRetransmissionManagerIntegration tests that the retransmission manager is properly integrated into the session
func TestRetransmissionManagerIntegration(t *testing.T) {
	// Create a mock path manager
	pathManager := transport.NewPathManager()
	
	// Create session config
	config := DefaultSessionConfig()
	
	// Create client session
	session := NewClientSession(pathManager, config)
	defer session.Close()

	// Verify retransmission manager is initialized
	if session.retransmissionManager == nil {
		t.Fatal("Retransmission manager should be initialized")
	}

	// Test that we can get retransmission stats
	stats := session.getRetransmissionStats()
	if stats.TotalSegments != 0 {
		t.Errorf("Expected 0 total segments initially, got %d", stats.TotalSegments)
	}

	// Test starting the retransmission manager
	err := session.startRetransmissionManager()
	if err != nil {
		t.Fatalf("Failed to start retransmission manager: %v", err)
	}

	// Test stopping the retransmission manager
	err = session.stopRetransmissionManager()
	if err != nil {
		t.Fatalf("Failed to stop retransmission manager: %v", err)
	}
}

// TestRetransmissionCallbacks tests that retransmission callbacks are properly set up
func TestRetransmissionCallbacks(t *testing.T) {
	// Create a mock path manager
	pathManager := transport.NewPathManager()
	
	// Create session config
	config := DefaultSessionConfig()
	
	// Create client session
	session := NewClientSession(pathManager, config)
	defer session.Close()

	// Start the retransmission manager
	err := session.startRetransmissionManager()
	if err != nil {
		t.Fatalf("Failed to start retransmission manager: %v", err)
	}

	// Test tracking a segment (this will test the callback setup indirectly)
	segmentID := "test-segment-1"
	data := []byte("test data")
	pathID := "test-path"
	timeout := 1 * time.Second

	err = session.trackSegmentForRetransmission(segmentID, data, pathID, timeout)
	if err != nil {
		t.Fatalf("Failed to track segment: %v", err)
	}

	// Verify the segment is being tracked
	stats := session.getRetransmissionStats()
	if stats.TotalSegments != 1 {
		t.Errorf("Expected 1 total segment, got %d", stats.TotalSegments)
	}
	if stats.ActiveSegments != 1 {
		t.Errorf("Expected 1 active segment, got %d", stats.ActiveSegments)
	}

	// Test acknowledging the segment
	err = session.acknowledgeSegment(segmentID)
	if err != nil {
		t.Fatalf("Failed to acknowledge segment: %v", err)
	}

	// Give some time for the acknowledgment to be processed
	time.Sleep(10 * time.Millisecond)

	// Verify the segment is no longer active
	stats = session.getRetransmissionStats()
	if stats.ActiveSegments != 0 {
		t.Errorf("Expected 0 active segments after acknowledgment, got %d", stats.ActiveSegments)
	}
	if stats.AcknowledgedSegments != 1 {
		t.Errorf("Expected 1 acknowledged segment, got %d", stats.AcknowledgedSegments)
	}
}

// TestRetransmissionManagerLifecycle tests the complete lifecycle of the retransmission manager
func TestRetransmissionManagerLifecycle(t *testing.T) {
	// Create a mock path manager
	pathManager := transport.NewPathManager()
	
	// Create session config
	config := DefaultSessionConfig()
	
	// Create client session
	session := NewClientSession(pathManager, config)

	// Verify initial state
	if session.retransmissionManager == nil {
		t.Fatal("Retransmission manager should be initialized")
	}

	// Test starting
	err := session.startRetransmissionManager()
	if err != nil {
		t.Fatalf("Failed to start retransmission manager: %v", err)
	}

	// Test that we can track segments after starting
	err = session.trackSegmentForRetransmission("test-1", []byte("data"), "path-1", time.Second)
	if err != nil {
		t.Fatalf("Failed to track segment after starting: %v", err)
	}

	// Test closing the session (should stop retransmission manager)
	err = session.Close()
	if err != nil {
		t.Fatalf("Failed to close session: %v", err)
	}

	// Test that tracking fails after closing
	err = session.trackSegmentForRetransmission("test-2", []byte("data"), "path-1", time.Second)
	if err == nil {
		t.Error("Expected error when tracking segment after session close")
	}
}

// MockRetransmissionManager for testing
type MockRetransmissionManager struct {
	started   bool
	stopped   bool
	segments  map[string]bool
	callbacks struct {
		retry      func(string, []byte, string) error
		maxRetries func(string, []byte, string) error
	}
}

func NewMockRetransmissionManager() *MockRetransmissionManager {
	return &MockRetransmissionManager{
		segments: make(map[string]bool),
	}
}

func (m *MockRetransmissionManager) TrackSegment(segmentID string, data []byte, pathID string, timeout time.Duration) error {
	if !m.started {
		return utils.NewKwikError(utils.ErrInvalidState, "retransmission manager is not running", nil)
	}
	m.segments[segmentID] = true
	return nil
}

func (m *MockRetransmissionManager) AckSegment(segmentID string) error {
	if !m.started {
		return utils.NewKwikError(utils.ErrInvalidState, "retransmission manager is not running", nil)
	}
	delete(m.segments, segmentID)
	return nil
}

func (m *MockRetransmissionManager) SetBackoffStrategy(strategy data.BackoffStrategy) error {
	return nil
}

func (m *MockRetransmissionManager) GetStats() data.RetransmissionStats {
	return data.RetransmissionStats{
		TotalSegments:  uint64(len(m.segments)),
		ActiveSegments: len(m.segments),
		LastUpdate:     time.Now(),
	}
}

func (m *MockRetransmissionManager) Start() error {
	m.started = true
	return nil
}

func (m *MockRetransmissionManager) Stop() error {
	m.stopped = true
	m.started = false
	return nil
}

func (m *MockRetransmissionManager) RegisterPath(pathID string) error {
	return nil
}

func (m *MockRetransmissionManager) UnregisterPath(pathID string) error {
	return nil
}

func (m *MockRetransmissionManager) TriggerFastRetransmission(pathID string, packetIDs []uint64, originalData map[uint64][]byte) error {
	return nil
}

func (m *MockRetransmissionManager) TriggerTimeoutRetransmission(pathID string, packetIDs []uint64, originalData map[uint64][]byte) error {
	return nil
}

func (m *MockRetransmissionManager) SetRetryCallback(callback func(string, []byte, string) error) {
	m.callbacks.retry = callback
}

func (m *MockRetransmissionManager) SetMaxRetriesCallback(callback func(string, []byte, string) error) {
	m.callbacks.maxRetries = callback
}