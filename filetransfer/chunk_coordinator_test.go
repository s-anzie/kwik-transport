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

	// Wait for reassignment to complete
	time.Sleep(100 * time.Millisecond)

	// Verify chunk was reassigned to primary server
	transferState.mutex.RLock()
	assignment := transferState.ChunkAssignments[secondaryChunk]
	transferState.mutex.RUnlock()

	if assignment.AssignedTo != ServerTypePrimary {
		t.Errorf("Expected chunk to be reassigned to primary server, got %s", assignment.AssignedTo.String())
	}
	if assignment.Status != ChunkStatusPending {
		t.Errorf("Expected chunk status to be reset to PENDING, got %s", assignment.Status.String())
	}
	if assignment.RetryCount != 0 {
		t.Errorf("Expected retry count to be reset to 0, got %d", assignment.RetryCount)
	}
}