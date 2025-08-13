package data

import (
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"kwik/pkg/transport"
	datapb "kwik/proto/data"
)

// MockPathManager implements transport.PathManager for testing
type MockPathManager struct {
	paths       map[string]*MockPath
	activePaths map[string]*MockPath
	deadPaths   map[string]*MockPath
	handler     transport.PathStatusNotificationHandler
}

// MockPath implements transport.Path for testing
type MockPath struct {
	id        string
	address   string
	isPrimary bool
	isActive  bool
}

func (mp *MockPath) ID() string                             { return mp.id }
func (mp *MockPath) SetID(id string)                        { mp.id = id }
func (mp *MockPath) Address() string                        { return mp.address }
func (mp *MockPath) IsActive() bool                         { return mp.isActive }
func (mp *MockPath) IsPrimary() bool                        { return mp.isPrimary }
func (mp *MockPath) GetConnection() quic.Connection         { return nil }
func (mp *MockPath) GetControlStream() (quic.Stream, error) { return nil, nil }
func (mp *MockPath) GetDataStreams() []quic.Stream          { return nil }
func (mp *MockPath) Close() error                           { return nil }

// New methods for proper control stream management
func (mp *MockPath) CreateControlStreamAsClient() (quic.Stream, error) { return nil, nil }
func (mp *MockPath) AcceptControlStreamAsServer() (quic.Stream, error) { return nil, nil }

// Secondary stream support methods
func (mp *MockPath) IsSecondaryPath() bool                                    { return false }
func (mp *MockPath) SetSecondaryPath(isSecondary bool)                        {}
func (mp *MockPath) AddSecondaryStream(streamID uint64, stream quic.Stream)   {}
func (mp *MockPath) RemoveSecondaryStream(streamID uint64)                    {}
func (mp *MockPath) GetSecondaryStream(streamID uint64) (quic.Stream, bool)   { return nil, false }
func (mp *MockPath) GetSecondaryStreams() map[uint64]quic.Stream              { return make(map[uint64]quic.Stream) }
func (mp *MockPath) GetSecondaryStreamCount() int                             { return 0 }

func NewMockPathManager() *MockPathManager {
	return &MockPathManager{
		paths:       make(map[string]*MockPath),
		activePaths: make(map[string]*MockPath),
		deadPaths:   make(map[string]*MockPath),
	}
}

func (mpm *MockPathManager) CreatePath(address string) (transport.Path, error) {
	path := &MockPath{
		id:        "path-" + address,
		address:   address,
		isPrimary: len(mpm.activePaths) == 0,
		isActive:  true,
	}
	mpm.paths[path.id] = path
	mpm.activePaths[path.id] = path
	return path, nil
}

func (mpm *MockPathManager) CreatePathFromConnection(conn quic.Connection) (transport.Path, error) {
	return nil, nil
}

func (mpm *MockPathManager) RemovePath(pathID string) error {
	if path, exists := mpm.activePaths[pathID]; exists {
		delete(mpm.activePaths, pathID)
		mpm.deadPaths[pathID] = path
		path.isActive = false
	}
	return nil
}

func (mpm *MockPathManager) GetPath(pathID string) transport.Path {
	if path, exists := mpm.paths[pathID]; exists {
		return path
	}
	return nil
}

func (mpm *MockPathManager) GetActivePaths() []transport.Path {
	paths := make([]transport.Path, 0, len(mpm.activePaths))
	for _, path := range mpm.activePaths {
		paths = append(paths, path)
	}
	return paths
}

func (mpm *MockPathManager) GetDeadPaths() []transport.Path {
	paths := make([]transport.Path, 0, len(mpm.deadPaths))
	for _, path := range mpm.deadPaths {
		paths = append(paths, path)
	}
	return paths
}

func (mpm *MockPathManager) MarkPathDead(pathID string) error {
	if path, exists := mpm.activePaths[pathID]; exists {
		delete(mpm.activePaths, pathID)
		mpm.deadPaths[pathID] = path
		path.isActive = false

		// Notify handler if set
		if mpm.handler != nil {
			mpm.handler.OnPathStatusChanged(pathID, transport.PathStateActive, transport.PathStateDead, nil)
		}
	}
	return nil
}

func (mpm *MockPathManager) GetPathCount() int                                          { return len(mpm.paths) }
func (mpm *MockPathManager) GetActivePathCount() int                                    { return len(mpm.activePaths) }
func (mpm *MockPathManager) GetDeadPathCount() int                                      { return len(mpm.deadPaths) }
func (mpm *MockPathManager) GetPrimaryPath() transport.Path                             { return nil }
func (mpm *MockPathManager) SetPrimaryPath(pathID string) error                         { return nil }
func (mpm *MockPathManager) CleanupDeadPaths() int                                      { return 0 }
func (mpm *MockPathManager) GetPathsByState(state transport.PathState) []transport.Path { return nil }
func (mpm *MockPathManager) ValidatePathHealth() []string                               { return nil }
func (mpm *MockPathManager) Close() error                                               { return nil }
func (mpm *MockPathManager) StartHealthMonitoring() error                               { return nil }
func (mpm *MockPathManager) StopHealthMonitoring() error                                { return nil }
func (mpm *MockPathManager) GetPathHealthMetrics(pathID string) (*transport.PathHealthMetrics, error) {
	return nil, nil
}
func (mpm *MockPathManager) SetHealthCheckInterval(interval time.Duration) {}
func (mpm *MockPathManager) SetFailureThreshold(threshold int)             {}

func (mpm *MockPathManager) SetPathStatusNotificationHandler(handler transport.PathStatusNotificationHandler) {
	mpm.handler = handler
}

func TestDeadPathHandler_PathDeathDetection(t *testing.T) {
	pathManager := NewMockPathManager()
	config := DefaultDeadPathConfig()
	config.CleanupInterval = 0 // Disable background cleanup for test

	dph := NewDeadPathHandler(pathManager, config)
	defer dph.Close()

	// Create a path
	path, err := pathManager.CreatePath("127.0.0.1:8080")
	if err != nil {
		t.Fatalf("Failed to create path: %v", err)
	}
	pathID := path.ID()

	// Initially, path should be usable for new streams
	canUse := dph.CanUsePathForNewStream(pathID)
	isActive := dph.streamRouter.IsPathActive(pathID)
	isDead := dph.streamRouter.IsPathDead(pathID)

	t.Logf("PathID: %s, CanUse: %v, IsActive: %v, IsDead: %v", pathID, canUse, isActive, isDead)

	if !canUse {
		t.Error("New path should be usable for new streams")
	}

	// Mark path as dead
	err = pathManager.MarkPathDead(pathID)
	if err != nil {
		t.Fatalf("Failed to mark path as dead: %v", err)
	}

	// Give some time for notification processing
	time.Sleep(10 * time.Millisecond)

	// Path should no longer be usable for new streams
	if dph.CanUsePathForNewStream(pathID) {
		t.Error("Dead path should not be usable for new streams")
	}

	// Check dead path statistics
	stats := dph.GetDeadPathStats()
	if stats.TotalDeadPaths != 1 {
		t.Errorf("Expected 1 total dead path, got %d", stats.TotalDeadPaths)
	}

	if stats.CurrentDeadPaths != 1 {
		t.Errorf("Expected 1 current dead path, got %d", stats.CurrentDeadPaths)
	}
}

func TestDeadPathHandler_AlternativePathSelection(t *testing.T) {
	pathManager := NewMockPathManager()
	config := DefaultDeadPathConfig()
	config.CleanupInterval = 0 // Disable background cleanup for test

	dph := NewDeadPathHandler(pathManager, config)
	defer dph.Close()

	// Create multiple paths
	path1, err := pathManager.CreatePath("127.0.0.1:8080")
	if err != nil {
		t.Fatalf("Failed to create path1: %v", err)
	}
	pathID1 := path1.ID()

	path2, err := pathManager.CreatePath("127.0.0.1:8081")
	if err != nil {
		t.Fatalf("Failed to create path2: %v", err)
	}
	pathID2 := path2.ID()

	// Mark path1 as active in stream router
	dph.streamRouter.MarkPathActive(pathID1)
	dph.streamRouter.MarkPathActive(pathID2)

	// Mark path1 as dead
	err = pathManager.MarkPathDead(pathID1)
	if err != nil {
		t.Fatalf("Failed to mark path1 as dead: %v", err)
	}

	// Give some time for notification processing
	time.Sleep(10 * time.Millisecond)

	// Get alternative path (should return path2)
	altPath, err := dph.GetAlternativePath(pathID1)
	if err != nil {
		t.Fatalf("Failed to get alternative path: %v", err)
	}

	if altPath != pathID2 {
		t.Errorf("Expected alternative path %s, got %s", pathID2, altPath)
	}

	// Mark path2 as dead as well
	err = pathManager.MarkPathDead(pathID2)
	if err != nil {
		t.Fatalf("Failed to mark path2 as dead: %v", err)
	}

	// Give some time for notification processing
	time.Sleep(10 * time.Millisecond)

	// Should not be able to get alternative path now
	_, err = dph.GetAlternativePath(pathID1)
	if err == nil {
		t.Error("Expected error when no alternative paths available")
	}
}

func TestDeadPathHandler_FrameProcessing(t *testing.T) {
	pathManager := NewMockPathManager()
	config := DefaultDeadPathConfig()
	config.CleanupInterval = 0 // Disable background cleanup for test

	dph := NewDeadPathHandler(pathManager, config)
	defer dph.Close()

	// Create a path
	path, err := pathManager.CreatePath("127.0.0.1:8080")
	if err != nil {
		t.Fatalf("Failed to create path: %v", err)
	}
	pathID := path.ID()

	// Mark path as active in stream router
	dph.streamRouter.MarkPathActive(pathID)

	// Create a frame from active path
	frame := &datapb.DataFrame{
		FrameId:         1,
		LogicalStreamId: 100,
		Offset:          0,
		Data:            []byte("test data"),
		PathId:          pathID,
	}

	// Frame should be processed successfully
	err = dph.ProcessIncomingFrame(frame)
	if err != nil {
		t.Errorf("Frame from active path should be processed successfully: %v", err)
	}

	// Mark path as dead
	err = pathManager.MarkPathDead(pathID)
	if err != nil {
		t.Fatalf("Failed to mark path as dead: %v", err)
	}

	// Give some time for notification processing
	time.Sleep(10 * time.Millisecond)

	// Frame from dead path should be rejected
	err = dph.ProcessIncomingFrame(frame)
	if err == nil {
		t.Error("Frame from dead path should be rejected")
	}

	// Check statistics
	stats := dph.GetDeadPathStats()
	if stats.FramesLost != 1 {
		t.Errorf("Expected 1 frame lost, got %d", stats.FramesLost)
	}

	if stats.BytesLost != uint64(len(frame.Data)) {
		t.Errorf("Expected %d bytes lost, got %d", len(frame.Data), stats.BytesLost)
	}

	if stats.StreamsAffected != 1 {
		t.Errorf("Expected 1 stream affected, got %d", stats.StreamsAffected)
	}
}

func TestDeadPathHandler_DeadPathInfo(t *testing.T) {
	pathManager := NewMockPathManager()
	config := DefaultDeadPathConfig()
	config.CleanupInterval = 0 // Disable background cleanup for test

	dph := NewDeadPathHandler(pathManager, config)
	defer dph.Close()

	// Create a path
	path, err := pathManager.CreatePath("127.0.0.1:8080")
	if err != nil {
		t.Fatalf("Failed to create path: %v", err)
	}
	pathID := path.ID()

	// Mark path as dead
	err = pathManager.MarkPathDead(pathID)
	if err != nil {
		t.Fatalf("Failed to mark path as dead: %v", err)
	}

	// Give some time for notification processing
	time.Sleep(10 * time.Millisecond)

	// Get dead path info
	deadPathInfo, err := dph.GetDeadPathInfo(pathID)
	if err != nil {
		t.Fatalf("Failed to get dead path info: %v", err)
	}

	if deadPathInfo.PathID != pathID {
		t.Errorf("Expected path ID %s, got %s", pathID, deadPathInfo.PathID)
	}

	if deadPathInfo.Reason != "status_change" {
		t.Errorf("Expected reason 'status_change', got %s", deadPathInfo.Reason)
	}

	if deadPathInfo.IsCleanedUp {
		t.Error("Path should not be cleaned up yet")
	}

	// Get all dead paths
	allDeadPaths := dph.GetAllDeadPaths()
	if len(allDeadPaths) != 1 {
		t.Errorf("Expected 1 dead path, got %d", len(allDeadPaths))
	}

	if allDeadPaths[0].PathID != pathID {
		t.Errorf("Expected dead path ID %s, got %s", pathID, allDeadPaths[0].PathID)
	}
}

func TestStreamRouter_PathStateManagement(t *testing.T) {
	router := NewStreamRouter()

	pathID1 := "path1"
	pathID2 := "path2"

	// Initially, paths should not be active or dead
	if router.IsPathActive(pathID1) {
		t.Error("Path should not be active initially")
	}

	if router.IsPathDead(pathID1) {
		t.Error("Path should not be dead initially")
	}

	// Mark path as active
	router.MarkPathActive(pathID1)
	if !router.IsPathActive(pathID1) {
		t.Error("Path should be active after marking as active")
	}

	if router.IsPathDead(pathID1) {
		t.Error("Active path should not be dead")
	}

	// Mark path as dead
	router.MarkPathDead(pathID1)
	if router.IsPathActive(pathID1) {
		t.Error("Dead path should not be active")
	}

	if !router.IsPathDead(pathID1) {
		t.Error("Path should be dead after marking as dead")
	}

	// Mark another path as active
	router.MarkPathActive(pathID2)

	// Get active paths
	activePaths := router.GetActivePaths()
	if len(activePaths) != 1 {
		t.Errorf("Expected 1 active path, got %d", len(activePaths))
	}

	if activePaths[0] != pathID2 {
		t.Errorf("Expected active path %s, got %s", pathID2, activePaths[0])
	}

	// Get dead paths
	deadPaths := router.GetDeadPaths()
	if len(deadPaths) != 1 {
		t.Errorf("Expected 1 dead path, got %d", len(deadPaths))
	}

	if deadPaths[0] != pathID1 {
		t.Errorf("Expected dead path %s, got %s", pathID1, deadPaths[0])
	}
}

func TestDeadPathHandler_PathRecovery(t *testing.T) {
	pathManager := NewMockPathManager()
	config := DefaultDeadPathConfig()
	config.CleanupInterval = 0 // Disable background cleanup for test

	dph := NewDeadPathHandler(pathManager, config)
	defer dph.Close()

	// Create a path
	path, err := pathManager.CreatePath("127.0.0.1:8080")
	if err != nil {
		t.Fatalf("Failed to create path: %v", err)
	}
	pathID := path.ID()

	// Mark path as active initially
	dph.streamRouter.MarkPathActive(pathID)

	// Mark path as dead
	err = pathManager.MarkPathDead(pathID)
	if err != nil {
		t.Fatalf("Failed to mark path as dead: %v", err)
	}

	// Give some time for notification processing
	time.Sleep(10 * time.Millisecond)

	// Path should be dead
	if !dph.streamRouter.IsPathDead(pathID) {
		t.Error("Path should be marked as dead")
	}

	// Simulate path recovery
	dph.OnPathRecovered(pathID, nil)

	// Path should be active again
	if !dph.streamRouter.IsPathActive(pathID) {
		t.Error("Recovered path should be marked as active")
	}

	if dph.streamRouter.IsPathDead(pathID) {
		t.Error("Recovered path should not be marked as dead")
	}

	// Check statistics
	stats := dph.GetDeadPathStats()
	if stats.CurrentDeadPaths != 0 {
		t.Errorf("Expected 0 current dead paths after recovery, got %d", stats.CurrentDeadPaths)
	}
}
