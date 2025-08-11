package filetransfer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestChunkCoordinatorSecondaryCoordination(t *testing.T) {
	// Create temporary directory for test files
	tempDir, err := os.MkdirTemp("", "chunk_coordinator_test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a test file
	testFile := filepath.Join(tempDir, "test.txt")
	testData := []byte("Hello, World! This is a test file for chunk coordination.")
	err = os.WriteFile(testFile, testData, 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create mock session with secondary paths
	mockSession := &MockSession{
		activePaths: []PathInfo{
			{PathID: "primary", IsPrimary: true, Status: PathStatusActive},
			{PathID: "secondary1", IsPrimary: false, Status: PathStatusActive},
		},
	}

	// Create chunk coordinator
	coordinator, err := NewChunkCoordinator(
		mockSession,
		[]string{"secondary1"},
		tempDir,
		20, // Small chunk size for testing
		5,
	)
	if err != nil {
		t.Fatalf("Failed to create chunk coordinator: %v", err)
	}
	defer coordinator.Close()

	// Start file transfer
	transferState, err := coordinator.StartFileTransfer("test.txt", "client1")
	if err != nil {
		t.Fatalf("Failed to start file transfer: %v", err)
	}

	// Wait a bit for chunks to be distributed
	time.Sleep(100 * time.Millisecond)

	// Verify chunks were distributed between primary and secondary
	if len(transferState.PrimaryChunks) == 0 {
		t.Error("No chunks assigned to primary server")
	}
	if len(transferState.SecondaryChunks) == 0 {
		t.Error("No chunks assigned to secondary server")
	}

	// Verify commands were sent to secondary server
	if len(mockSession.rawDataSent) == 0 {
		t.Error("No commands sent to secondary server")
	}

	// Parse the first command sent to secondary
	var command ChunkCommand
	err = json.Unmarshal(mockSession.rawDataSent[0].Data, &command)
	if err != nil {
		t.Fatalf("Failed to parse chunk command: %v", err)
	}

	if command.Command != "SEND_CHUNK" {
		t.Errorf("Expected SEND_CHUNK command, got %s", command.Command)
	}
	if command.Filename != "test.txt" {
		t.Errorf("Expected filename test.txt, got %s", command.Filename)
	}
}

func TestSecondaryServerResponseHandling(t *testing.T) {
	// Create temporary directory for test files
	tempDir, err := os.MkdirTemp("", "chunk_coordinator_test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a test file
	testFile := filepath.Join(tempDir, "test.txt")
	testData := []byte("Hello, World!")
	err = os.WriteFile(testFile, testData, 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create mock session
	mockSession := &MockSession{
		activePaths: []PathInfo{
			{PathID: "primary", IsPrimary: true, Status: PathStatusActive},
			{PathID: "secondary1", IsPrimary: false, Status: PathStatusActive},
		},
	}

	// Create chunk coordinator
	coordinator, err := NewChunkCoordinator(
		mockSession,
		[]string{"secondary1"},
		tempDir,
		10,
		5,
	)
	if err != nil {
		t.Fatalf("Failed to create chunk coordinator: %v", err)
	}
	defer coordinator.Close()

	// Start file transfer
	transferState, err := coordinator.StartFileTransfer("test.txt", "client1")
	if err != nil {
		t.Fatalf("Failed to start file transfer: %v", err)
	}

	// Wait for initial distribution
	time.Sleep(100 * time.Millisecond)

	// Find a chunk assigned to secondary server
	var secondaryChunk uint32
	found := false
	for chunkID, assignment := range transferState.ChunkAssignments {
		if assignment.AssignedTo == ServerTypeSecondary {
			secondaryChunk = chunkID
			found = true
			break
		}
	}

	if !found {
		t.Fatal("No chunks assigned to secondary server")
	}

	// Simulate successful response from secondary server
	successResponse := ChunkCommand{
		Command:     "CHUNK_SENT",
		ChunkID:     secondaryChunk,
		SequenceNum: secondaryChunk,
		Filename:    "test.txt",
		Checksum:    "test_checksum",
		Timestamp:   time.Now(),
	}

	// Test the response handling directly
	coordinator.handleChunkSentResponse(&successResponse)

	// Verify chunk was marked as completed
	transferState.mutex.RLock()
	assignment := transferState.ChunkAssignments[secondaryChunk]
	completed := transferState.CompletedChunks[secondaryChunk]
	transferState.mutex.RUnlock()

	if assignment.Status != ChunkStatusSent {
		t.Errorf("Expected chunk status SENT, got %s", assignment.Status.String())
	}
	if !completed {
		t.Error("Chunk should be marked as completed")
	}
}

func TestSecondaryServerErrorHandling(t *testing.T) {
	// Create temporary directory for test files
	tempDir, err := os.MkdirTemp("", "chunk_coordinator_test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a test file
	testFile := filepath.Join(tempDir, "test.txt")
	testData := []byte("Hello, World!")
	err = os.WriteFile(testFile, testData, 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create mock session
	mockSession := &MockSession{
		activePaths: []PathInfo{
			{PathID: "primary", IsPrimary: true, Status: PathStatusActive},
			{PathID: "secondary1", IsPrimary: false, Status: PathStatusActive},
		},
	}

	// Create chunk coordinator
	coordinator, err := NewChunkCoordinator(
		mockSession,
		[]string{"secondary1"},
		tempDir,
		10,
		5,
	)
	if err != nil {
		t.Fatalf("Failed to create chunk coordinator: %v", err)
	}
	defer coordinator.Close()

	// Start file transfer
	transferState, err := coordinator.StartFileTransfer("test.txt", "client1")
	if err != nil {
		t.Fatalf("Failed to start file transfer: %v", err)
	}

	// Wait for initial distribution
	time.Sleep(100 * time.Millisecond)

	// Find a chunk assigned to secondary server
	var secondaryChunk uint32
	found := false
	for chunkID, assignment := range transferState.ChunkAssignments {
		if assignment.AssignedTo == ServerTypeSecondary {
			secondaryChunk = chunkID
			found = true
			break
		}
	}

	if !found {
		t.Fatal("No chunks assigned to secondary server")
	}

	// Simulate error response from secondary server
	errorResponse := ChunkCommand{
		Command:     "CHUNK_ERROR",
		ChunkID:     secondaryChunk,
		SequenceNum: secondaryChunk,
		Filename:    "test.txt",
		Metadata:    map[string]string{"error": "Failed to read chunk"},
		Timestamp:   time.Now(),
	}

	// Test the error handling directly
	coordinator.handleChunkErrorResponse(&errorResponse)

	// Verify chunk was marked as failed
	transferState.mutex.RLock()
	assignment := transferState.ChunkAssignments[secondaryChunk]
	failedCount := transferState.FailedChunks[secondaryChunk]
	transferState.mutex.RUnlock()

	if assignment.Status != ChunkStatusFailed {
		t.Errorf("Expected chunk status FAILED, got %s", assignment.Status.String())
	}
	if failedCount == 0 {
		t.Error("Chunk should be marked as failed")
	}
	if assignment.LastError == "" {
		t.Error("Error message should be set")
	}
}

func TestGetSecondaryServerStatus(t *testing.T) {
	// Create temporary directory for test files
	tempDir, err := os.MkdirTemp("", "chunk_coordinator_test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create mock session with multiple secondary paths
	mockSession := &MockSession{
		activePaths: []PathInfo{
			{PathID: "primary", IsPrimary: true, Status: PathStatusActive},
			{PathID: "secondary1", IsPrimary: false, Status: PathStatusActive},
			{PathID: "secondary2", IsPrimary: false, Status: PathStatusActive},
		},
	}

	// Create chunk coordinator
	coordinator, err := NewChunkCoordinator(
		mockSession,
		[]string{"secondary1", "secondary2"},
		tempDir,
		1024,
		5,
	)
	if err != nil {
		t.Fatalf("Failed to create chunk coordinator: %v", err)
	}
	defer coordinator.Close()

	// Get secondary server status
	status := coordinator.GetSecondaryServerStatus()

	// Verify status information
	secondaryPaths, ok := status["secondary_paths"].([]string)
	if !ok {
		t.Error("secondary_paths should be a string slice")
	}
	if len(secondaryPaths) != 2 {
		t.Errorf("Expected 2 secondary paths, got %d", len(secondaryPaths))
	}

	pathsCount, ok := status["secondary_paths_count"].(int)
	if !ok {
		t.Error("secondary_paths_count should be an int")
	}
	if pathsCount != 2 {
		t.Errorf("Expected secondary_paths_count to be 2, got %d", pathsCount)
	}

	activeSecondaryPaths, ok := status["active_secondary_paths"].([]string)
	if !ok {
		t.Error("active_secondary_paths should be a string slice")
	}
	if len(activeSecondaryPaths) != 2 {
		t.Errorf("Expected 2 active secondary paths, got %d", len(activeSecondaryPaths))
	}
}

func TestReassignFailedChunks(t *testing.T) {
	// Create temporary directory for test files
	tempDir, err := os.MkdirTemp("", "chunk_coordinator_test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a test file
	testFile := filepath.Join(tempDir, "test.txt")
	testData := []byte("Hello, World! This is a test file.")
	err = os.WriteFile(testFile, testData, 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create mock session
	mockSession := &MockSession{
		activePaths: []PathInfo{
			{PathID: "primary", IsPrimary: true, Status: PathStatusActive},
			{PathID: "secondary1", IsPrimary: false, Status: PathStatusActive},
		},
	}

	// Create chunk coordinator
	coordinator, err := NewChunkCoordinator(
		mockSession,
		[]string{"secondary1"},
		tempDir,
		10,
		5,
	)
	if err != nil {
		t.Fatalf("Failed to create chunk coordinator: %v", err)
	}
	defer coordinator.Close()

	// Start file transfer
	transferState, err := coordinator.StartFileTransfer("test.txt", "client1")
	if err != nil {
		t.Fatalf("Failed to start file transfer: %v", err)
	}

	// Wait for initial distribution
	time.Sleep(100 * time.Millisecond)

	// Find a chunk assigned to secondary server and mark it as failed
	var secondaryChunk uint32
	found := false
	transferState.mutex.Lock()
	for chunkID, assignment := range transferState.ChunkAssignments {
		if assignment.AssignedTo == ServerTypeSecondary {
			secondaryChunk = chunkID
			found = true
			// Mark as failed with high retry count
			assignment.Status = ChunkStatusFailed
			assignment.RetryCount = 5
			assignment.LastError = "Simulated failure"
			transferState.ChunkAssignments[chunkID] = assignment
			transferState.FailedChunks[chunkID] = 5
			break
		}
	}
	transferState.mutex.Unlock()

	if !found {
		t.Fatal("No chunks assigned to secondary server")
	}

	// Reassign failed chunks
	err = coordinator.ReassignFailedChunks("test.txt", 3)
	if err != nil {
		t.Fatalf("Failed to reassign chunks: %v", err)
	}

	// Check immediately after reassignment (before goroutine completes)
	transferState.mutex.RLock()
	assignment := transferState.ChunkAssignments[secondaryChunk]
	transferState.mutex.RUnlock()

	if assignment.AssignedTo != ServerTypePrimary {
		t.Errorf("Expected chunk to be reassigned to primary server, got %s", assignment.AssignedTo.String())
	}
	if assignment.RetryCount != 0 {
		t.Errorf("Expected retry count to be reset to 0, got %d", assignment.RetryCount)
	}

	// Wait for the chunk to be processed and verify it was sent successfully
	time.Sleep(100 * time.Millisecond)

	transferState.mutex.RLock()
	finalAssignment := transferState.ChunkAssignments[secondaryChunk]
	completed := transferState.CompletedChunks[secondaryChunk]
	transferState.mutex.RUnlock()

	// After processing, the chunk should be marked as sent and completed
	if finalAssignment.Status != ChunkStatusSent {
		t.Errorf("Expected final chunk status to be SENT, got %s", finalAssignment.Status.String())
	}
	if !completed {
		t.Error("Chunk should be marked as completed after reassignment and processing")
	}
}

func TestBandwidthBasedDistribution(t *testing.T) {
	// Create temporary directory for test files
	tempDir, err := os.MkdirTemp("", "chunk_coordinator_test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a test file with multiple chunks
	testFile := filepath.Join(tempDir, "large_test.txt")
	testData := make([]byte, 1000) // 1000 bytes
	for i := range testData {
		testData[i] = byte(i % 256)
	}
	err = os.WriteFile(testFile, testData, 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create mock session with secondary paths
	mockSession := &MockSession{
		activePaths: []PathInfo{
			{PathID: "primary", IsPrimary: true, Status: PathStatusActive},
			{PathID: "secondary1", IsPrimary: false, Status: PathStatusActive},
		},
	}

	// Create chunk coordinator
	coordinator, err := NewChunkCoordinator(
		mockSession,
		[]string{"secondary1"},
		tempDir,
		100, // 100 byte chunks = 10 chunks total
		5,
	)
	if err != nil {
		t.Fatalf("Failed to create chunk coordinator: %v", err)
	}
	defer coordinator.Close()

	// Simulate different bandwidth metrics
	coordinator.updatePathMetrics("primary", 100, 100*time.Millisecond, true)     // 1000 bytes/sec
	coordinator.updatePathMetrics("secondary1", 100, 50*time.Millisecond, true)   // 2000 bytes/sec

	// Create transfer state for bandwidth-based distribution
	metadata := NewFileMetadata("large_test.txt", 1000, 100)
	transferState := &TransferState{
		Filename:         "large_test.txt",
		Metadata:         metadata,
		PrimaryChunks:    make([]uint32, 0),
		SecondaryChunks:  make([]uint32, 0),
		CompletedChunks:  make(map[uint32]bool),
		FailedChunks:     make(map[uint32]int),
		StartTime:        time.Now(),
		LastActivity:     time.Now(),
		ClientAddress:    "client1",
		Status:           TransferStatusPreparing,
		ChunkAssignments: make(map[uint32]ChunkAssignment),
	}

	// Test bandwidth-based distribution
	err = coordinator.distributeBandwidthBased(transferState, metadata.TotalChunks)
	if err != nil {
		t.Fatalf("Failed to distribute chunks: %v", err)
	}

	// Secondary server has 2x bandwidth, so should get more chunks
	primaryCount := len(transferState.PrimaryChunks)
	secondaryCount := len(transferState.SecondaryChunks)

	if secondaryCount <= primaryCount {
		t.Errorf("Expected secondary server to get more chunks due to higher bandwidth. Primary: %d, Secondary: %d", primaryCount, secondaryCount)
	}

	// Verify total chunks are distributed
	if primaryCount+secondaryCount != int(metadata.TotalChunks) {
		t.Errorf("Expected total chunks %d, got %d", metadata.TotalChunks, primaryCount+secondaryCount)
	}
}

func TestPathMetricsTracking(t *testing.T) {
	// Create temporary directory for test files
	tempDir, err := os.MkdirTemp("", "chunk_coordinator_test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create mock session
	mockSession := &MockSession{
		activePaths: []PathInfo{
			{PathID: "primary", IsPrimary: true, Status: PathStatusActive},
			{PathID: "secondary1", IsPrimary: false, Status: PathStatusActive},
		},
	}

	// Create chunk coordinator
	coordinator, err := NewChunkCoordinator(
		mockSession,
		[]string{"secondary1"},
		tempDir,
		1024,
		5,
	)
	if err != nil {
		t.Fatalf("Failed to create chunk coordinator: %v", err)
	}
	defer coordinator.Close()

	// Test initial metrics
	metrics := coordinator.GetPathMetrics()
	if len(metrics) != 2 {
		t.Errorf("Expected 2 path metrics, got %d", len(metrics))
	}

	// Test updating metrics
	coordinator.updatePathMetrics("primary", 1024, 100*time.Millisecond, true)
	coordinator.updatePathMetrics("primary", 1024, 200*time.Millisecond, true)

	// Get updated metrics
	updatedMetrics := coordinator.GetPathMetrics()
	primaryMetrics := updatedMetrics["primary"]

	if primaryMetrics.TotalChunksSent != 2 {
		t.Errorf("Expected 2 chunks sent, got %d", primaryMetrics.TotalChunksSent)
	}
	if primaryMetrics.TotalBytesSent != 2048 {
		t.Errorf("Expected 2048 bytes sent, got %d", primaryMetrics.TotalBytesSent)
	}
	if primaryMetrics.AverageBandwidth <= 0 {
		t.Error("Expected positive average bandwidth")
	}
	if primaryMetrics.SuccessRate != 1.0 {
		t.Errorf("Expected success rate 1.0, got %f", primaryMetrics.SuccessRate)
	}

	// Test failure tracking
	coordinator.updatePathMetrics("primary", 1024, 100*time.Millisecond, false)
	failureMetrics := coordinator.GetPathMetrics()
	primaryFailureMetrics := failureMetrics["primary"]

	if primaryFailureMetrics.FailureCount != 1 {
		t.Errorf("Expected 1 failure, got %d", primaryFailureMetrics.FailureCount)
	}
	if primaryFailureMetrics.SuccessRate >= 1.0 {
		t.Errorf("Expected success rate < 1.0 after failure, got %f", primaryFailureMetrics.SuccessRate)
	}
}

func TestDynamicRebalancing(t *testing.T) {
	// Create temporary directory for test files
	tempDir, err := os.MkdirTemp("", "chunk_coordinator_test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create mock session
	mockSession := &MockSession{
		activePaths: []PathInfo{
			{PathID: "primary", IsPrimary: true, Status: PathStatusActive},
			{PathID: "secondary1", IsPrimary: false, Status: PathStatusActive},
		},
	}

	// Create chunk coordinator with short rebalance interval for testing
	coordinator, err := NewChunkCoordinator(
		mockSession,
		[]string{"secondary1"},
		tempDir,
		100,
		5,
	)
	if err != nil {
		t.Fatalf("Failed to create chunk coordinator: %v", err)
	}
	defer coordinator.Close()

	// Set short rebalance interval for testing
	coordinator.SetRebalanceInterval(100 * time.Millisecond)

	// Create a transfer state with unbalanced distribution
	metadata := NewFileMetadata("test.txt", 1000, 100)
	transferState := &TransferState{
		Filename:         "test.txt",
		Metadata:         metadata,
		PrimaryChunks:    []uint32{0, 1, 2, 3, 4, 5, 6, 7}, // 8 chunks on primary
		SecondaryChunks:  []uint32{8, 9},                    // 2 chunks on secondary
		CompletedChunks:  make(map[uint32]bool),
		FailedChunks:     make(map[uint32]int),
		StartTime:        time.Now(),
		LastActivity:     time.Now(),
		ClientAddress:    "client1",
		Status:           TransferStatusActive,
		ChunkAssignments: make(map[uint32]ChunkAssignment),
	}

	// Initialize chunk assignments
	for i := uint32(0); i < 10; i++ {
		assignment := ChunkAssignment{
			ChunkID:     i,
			SequenceNum: i,
			Status:      ChunkStatusPending,
			AssignedAt:  time.Now(),
			RetryCount:  0,
		}
		if i < 8 {
			assignment.AssignedTo = ServerTypePrimary
		} else {
			assignment.AssignedTo = ServerTypeSecondary
		}
		transferState.ChunkAssignments[i] = assignment
	}

	// Add to active transfers
	coordinator.transfersMutex.Lock()
	coordinator.activeTransfers["test.txt"] = transferState
	coordinator.transfersMutex.Unlock()

	// Set up bandwidth metrics that favor secondary server
	coordinator.updatePathMetrics("primary", 100, 200*time.Millisecond, true)     // 500 bytes/sec
	coordinator.updatePathMetrics("secondary1", 100, 50*time.Millisecond, true)   // 2000 bytes/sec

	// Wait for rebalancing to occur
	time.Sleep(200 * time.Millisecond)

	// Check if rebalancing occurred
	transferState.mutex.RLock()
	primaryCount := len(transferState.PrimaryChunks)
	secondaryCount := len(transferState.SecondaryChunks)
	transferState.mutex.RUnlock()

	// Secondary should have gotten more chunks due to higher bandwidth
	if secondaryCount <= 2 {
		t.Errorf("Expected rebalancing to move chunks to secondary server. Primary: %d, Secondary: %d", primaryCount, secondaryCount)
	}
}

func TestOptimizationConfiguration(t *testing.T) {
	// Create temporary directory for test files
	tempDir, err := os.MkdirTemp("", "chunk_coordinator_test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create mock session
	mockSession := &MockSession{
		activePaths: []PathInfo{
			{PathID: "primary", IsPrimary: true, Status: PathStatusActive},
		},
	}

	// Create chunk coordinator
	coordinator, err := NewChunkCoordinator(
		mockSession,
		[]string{},
		tempDir,
		1024,
		5,
	)
	if err != nil {
		t.Fatalf("Failed to create chunk coordinator: %v", err)
	}
	defer coordinator.Close()

	// Test initial optimization settings
	if !coordinator.IsOptimizationEnabled() {
		t.Error("Expected optimization to be enabled by default")
	}

	// Test disabling optimization
	coordinator.SetOptimizationEnabled(false)
	if coordinator.IsOptimizationEnabled() {
		t.Error("Expected optimization to be disabled")
	}

	// Test rebalance interval configuration
	newInterval := 60 * time.Second
	coordinator.SetRebalanceInterval(newInterval)
	if coordinator.GetRebalanceInterval() != newInterval {
		t.Errorf("Expected rebalance interval %v, got %v", newInterval, coordinator.GetRebalanceInterval())
	}

	// Test optimization status
	status := coordinator.GetOptimizationStatus()
	if status["optimization_enabled"] != false {
		t.Error("Expected optimization_enabled to be false in status")
	}
	if status["rebalance_interval"] != newInterval.String() {
		t.Errorf("Expected rebalance_interval %s in status, got %v", newInterval.String(), status["rebalance_interval"])
	}
}

func TestBandwidthCalculation(t *testing.T) {
	// Create temporary directory for test files
	tempDir, err := os.MkdirTemp("", "chunk_coordinator_test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create mock session
	mockSession := &MockSession{
		activePaths: []PathInfo{
			{PathID: "primary", IsPrimary: true, Status: PathStatusActive},
			{PathID: "secondary1", IsPrimary: false, Status: PathStatusActive},
			{PathID: "secondary2", IsPrimary: false, Status: PathStatusActive},
		},
	}

	// Create chunk coordinator
	coordinator, err := NewChunkCoordinator(
		mockSession,
		[]string{"secondary1", "secondary2"},
		tempDir,
		1024,
		5,
	)
	if err != nil {
		t.Fatalf("Failed to create chunk coordinator: %v", err)
	}
	defer coordinator.Close()

	// Test initial bandwidth (should be 0)
	primaryBW := coordinator.getPrimaryPathBandwidth()
	secondaryBW := coordinator.getSecondaryPathBandwidth()

	if primaryBW != 0 {
		t.Errorf("Expected initial primary bandwidth 0, got %f", primaryBW)
	}
	if secondaryBW != 0 {
		t.Errorf("Expected initial secondary bandwidth 0, got %f", secondaryBW)
	}

	// Add some metrics
	coordinator.updatePathMetrics("primary", 1024, 100*time.Millisecond, true)    // 10240 bytes/sec
	coordinator.updatePathMetrics("secondary1", 1024, 200*time.Millisecond, true) // 5120 bytes/sec
	coordinator.updatePathMetrics("secondary2", 1024, 50*time.Millisecond, true)  // 20480 bytes/sec

	// Test bandwidth calculations
	primaryBW = coordinator.getPrimaryPathBandwidth()
	secondaryBW = coordinator.getSecondaryPathBandwidth()

	if primaryBW <= 0 {
		t.Errorf("Expected positive primary bandwidth, got %f", primaryBW)
	}
	if secondaryBW <= 0 {
		t.Errorf("Expected positive secondary bandwidth, got %f", secondaryBW)
	}

	// Secondary should have higher average bandwidth (average of 5120 and 20480)
	expectedSecondaryBW := (5120.0 + 20480.0) / 2.0
	if abs(int(secondaryBW-expectedSecondaryBW)) > 100 { // Allow some tolerance
		t.Errorf("Expected secondary bandwidth around %f, got %f", expectedSecondaryBW, secondaryBW)
	}
}

func TestChunkReassignment(t *testing.T) {
	// Create temporary directory for test files
	tempDir, err := os.MkdirTemp("", "chunk_coordinator_test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a test file
	testFile := filepath.Join(tempDir, "test.txt")
	testData := []byte("Hello, World! This is a test file for chunk reassignment.")
	err = os.WriteFile(testFile, testData, 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create mock session
	mockSession := &MockSession{
		activePaths: []PathInfo{
			{PathID: "primary", IsPrimary: true, Status: PathStatusActive},
			{PathID: "secondary1", IsPrimary: false, Status: PathStatusActive},
		},
	}

	// Create chunk coordinator
	coordinator, err := NewChunkCoordinator(
		mockSession,
		[]string{"secondary1"},
		tempDir,
		20,
		5,
	)
	if err != nil {
		t.Fatalf("Failed to create chunk coordinator: %v", err)
	}
	defer coordinator.Close()

	// Start file transfer
	transferState, err := coordinator.StartFileTransfer("test.txt", "client1")
	if err != nil {
		t.Fatalf("Failed to start file transfer: %v", err)
	}

	// Wait for initial distribution
	time.Sleep(100 * time.Millisecond)

	// Find chunks assigned to secondary and mark some as failed
	failedChunks := make([]uint32, 0)
	transferState.mutex.Lock()
	for chunkID, assignment := range transferState.ChunkAssignments {
		if assignment.AssignedTo == ServerTypeSecondary && len(failedChunks) < 2 {
			// Mark as failed with high retry count
			assignment.Status = ChunkStatusFailed
			assignment.RetryCount = 5
			assignment.LastError = "Simulated failure"
			transferState.ChunkAssignments[chunkID] = assignment
			transferState.FailedChunks[chunkID] = 5
			failedChunks = append(failedChunks, chunkID)
		}
	}
	transferState.mutex.Unlock()

	if len(failedChunks) == 0 {
		t.Fatal("No chunks were assigned to secondary server for testing")
	}

	// Test reassignment
	err = coordinator.ReassignFailedChunks("test.txt", 3)
	if err != nil {
		t.Fatalf("Failed to reassign chunks: %v", err)
	}

	// Verify chunks were reassigned to primary
	transferState.mutex.RLock()
	for _, chunkID := range failedChunks {
		assignment := transferState.ChunkAssignments[chunkID]
		if assignment.AssignedTo != ServerTypePrimary {
			t.Errorf("Expected chunk %d to be reassigned to primary, got %s", chunkID, assignment.AssignedTo.String())
		}
		if assignment.RetryCount != 0 {
			t.Errorf("Expected retry count to be reset for chunk %d, got %d", chunkID, assignment.RetryCount)
		}
	}
	transferState.mutex.RUnlock()

	// Wait for chunks to be processed
	time.Sleep(200 * time.Millisecond)

	// Verify chunks were successfully sent
	transferState.mutex.RLock()
	for _, chunkID := range failedChunks {
		if !transferState.CompletedChunks[chunkID] {
			t.Errorf("Expected chunk %d to be completed after reassignment", chunkID)
		}
	}
	transferState.mutex.RUnlock()
}