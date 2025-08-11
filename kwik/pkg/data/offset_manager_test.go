package data

import (
	"testing"
)

func TestOffsetManager_RegisterLogicalStream(t *testing.T) {
	manager := NewOffsetManager(nil)

	tests := []struct {
		name            string
		logicalStreamID uint64
		expectError     bool
	}{
		{
			name:            "valid stream registration",
			logicalStreamID: 1,
			expectError:     false,
		},
		{
			name:            "another valid stream",
			logicalStreamID: 2,
			expectError:     false,
		},
		{
			name:            "zero stream ID",
			logicalStreamID: 0,
			expectError:     true,
		},
		{
			name:            "duplicate stream ID",
			logicalStreamID: 1, // Already registered above
			expectError:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := manager.RegisterLogicalStream(tt.logicalStreamID)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			// Verify stream was registered
			offsets, err := manager.GetLogicalOffset(tt.logicalStreamID)
			if err != nil {
				t.Errorf("failed to get logical offset: %v", err)
				return
			}

			if offsets.LogicalStreamID != tt.logicalStreamID {
				t.Errorf("expected stream ID %d, got %d", tt.logicalStreamID, offsets.LogicalStreamID)
			}

			if offsets.NextWriteOffset != 0 {
				t.Errorf("expected initial write offset 0, got %d", offsets.NextWriteOffset)
			}

			if offsets.NextReadOffset != 0 {
				t.Errorf("expected initial read offset 0, got %d", offsets.NextReadOffset)
			}
		})
	}
}

func TestOffsetManager_RegisterPhysicalStream(t *testing.T) {
	manager := NewOffsetManager(nil)

	tests := []struct {
		name         string
		pathID       string
		realStreamID uint64
		expectError  bool
	}{
		{
			name:         "valid stream registration",
			pathID:       "path-1",
			realStreamID: 1,
			expectError:  false,
		},
		{
			name:         "another stream on same path",
			pathID:       "path-1",
			realStreamID: 2,
			expectError:  false,
		},
		{
			name:         "stream on different path",
			pathID:       "path-2",
			realStreamID: 1,
			expectError:  false,
		},
		{
			name:         "empty path ID",
			pathID:       "",
			realStreamID: 1,
			expectError:  true,
		},
		{
			name:         "zero stream ID",
			pathID:       "path-1",
			realStreamID: 0,
			expectError:  true,
		},
		{
			name:         "duplicate stream",
			pathID:       "path-1",
			realStreamID: 1, // Already registered above
			expectError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := manager.RegisterPhysicalStream(tt.pathID, tt.realStreamID)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			// Verify stream was registered
			offsets, err := manager.GetPhysicalOffset(tt.pathID, tt.realStreamID)
			if err != nil {
				t.Errorf("failed to get physical offset: %v", err)
				return
			}

			if offsets.PathID != tt.pathID {
				t.Errorf("expected path ID %s, got %s", tt.pathID, offsets.PathID)
			}

			if offsets.RealStreamID != tt.realStreamID {
				t.Errorf("expected stream ID %d, got %d", tt.realStreamID, offsets.RealStreamID)
			}

			if offsets.NextWriteOffset != 0 {
				t.Errorf("expected initial write offset 0, got %d", offsets.NextWriteOffset)
			}
		})
	}
}

func TestOffsetManager_CreateOffsetMapping(t *testing.T) {
	manager := NewOffsetManager(nil)

	// Register required streams first
	logicalStreamID := uint64(1)
	pathID := "path-1"
	realStreamID := uint64(1)

	err := manager.RegisterLogicalStream(logicalStreamID)
	if err != nil {
		t.Fatalf("failed to register logical stream: %v", err)
	}

	err = manager.RegisterPhysicalStream(pathID, realStreamID)
	if err != nil {
		t.Fatalf("failed to register physical stream: %v", err)
	}

	tests := []struct {
		name            string
		logicalStreamID uint64
		pathID          string
		realStreamID    uint64
		expectError     bool
	}{
		{
			name:            "valid mapping creation",
			logicalStreamID: logicalStreamID,
			pathID:          pathID,
			realStreamID:    realStreamID,
			expectError:     false,
		},
		{
			name:            "unregistered logical stream",
			logicalStreamID: 999,
			pathID:          pathID,
			realStreamID:    realStreamID,
			expectError:     true,
		},
		{
			name:            "unregistered physical stream",
			logicalStreamID: logicalStreamID,
			pathID:          "unknown-path",
			realStreamID:    realStreamID,
			expectError:     true,
		},
		{
			name:            "duplicate mapping",
			logicalStreamID: logicalStreamID,
			pathID:          pathID,
			realStreamID:    realStreamID,
			expectError:     true, // Already created above
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := manager.CreateOffsetMapping(tt.logicalStreamID, tt.pathID, tt.realStreamID)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestOffsetManager_MapLogicalToPhysical(t *testing.T) {
	manager := NewOffsetManager(nil)

	// Setup streams and mapping
	logicalStreamID := uint64(1)
	pathID := "path-1"
	realStreamID := uint64(1)

	manager.RegisterLogicalStream(logicalStreamID)
	manager.RegisterPhysicalStream(pathID, realStreamID)
	manager.CreateOffsetMapping(logicalStreamID, pathID, realStreamID)

	tests := []struct {
		name             string
		logicalOffset    uint64
		dataLength       uint32
		expectError      bool
		expectedPhysical uint64
	}{
		{
			name:             "first mapping",
			logicalOffset:    0,
			dataLength:       100,
			expectError:      false,
			expectedPhysical: 0,
		},
		{
			name:             "second mapping",
			logicalOffset:    100,
			dataLength:       200,
			expectError:      false,
			expectedPhysical: 100, // Should be sequential
		},
		{
			name:             "duplicate mapping",
			logicalOffset:    0, // Already mapped above
			dataLength:       100,
			expectError:      false,
			expectedPhysical: 0, // Should return existing mapping
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			physicalOffset, err := manager.MapLogicalToPhysical(
				logicalStreamID, pathID, realStreamID, tt.logicalOffset, tt.dataLength)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if physicalOffset != tt.expectedPhysical {
				t.Errorf("expected physical offset %d, got %d", tt.expectedPhysical, physicalOffset)
			}

			// Verify reverse mapping works
			mappedLogical, err := manager.MapPhysicalToLogical(
				logicalStreamID, pathID, realStreamID, physicalOffset)
			if err != nil {
				t.Errorf("failed to map physical to logical: %v", err)
				return
			}

			if mappedLogical != tt.logicalOffset {
				t.Errorf("reverse mapping failed: expected %d, got %d", tt.logicalOffset, mappedLogical)
			}
		})
	}
}

func TestOffsetManager_UpdateLogicalOffset(t *testing.T) {
	manager := NewOffsetManager(nil)

	logicalStreamID := uint64(1)
	err := manager.RegisterLogicalStream(logicalStreamID)
	if err != nil {
		t.Fatalf("failed to register logical stream: %v", err)
	}

	tests := []struct {
		name        string
		offset      uint64
		dataLength  uint32
		expectError bool
	}{
		{
			name:        "first update",
			offset:      0,
			dataLength:  100,
			expectError: false,
		},
		{
			name:        "sequential update",
			offset:      100,
			dataLength:  200,
			expectError: false,
		},
		{
			name:        "gap creating update",
			offset:      400, // Creates gap from 300-400
			dataLength:  100,
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := manager.UpdateLogicalOffset(logicalStreamID, tt.offset, tt.dataLength)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			// Verify offset was updated
			offsets, err := manager.GetLogicalOffset(logicalStreamID)
			if err != nil {
				t.Errorf("failed to get logical offset: %v", err)
				return
			}

			expectedMaxOffset := tt.offset + uint64(tt.dataLength)
			if offsets.MaxOffset < expectedMaxOffset {
				t.Errorf("expected max offset >= %d, got %d", expectedMaxOffset, offsets.MaxOffset)
			}
		})
	}

	// Verify gap tracking worked
	offsets, err := manager.GetLogicalOffset(logicalStreamID)
	if err != nil {
		t.Fatalf("failed to get final logical offset: %v", err)
	}

	// Should have a gap from 300-400
	if len(offsets.Gaps) == 0 {
		t.Errorf("expected gaps to be tracked")
	}
}

func TestOffsetManager_UpdatePhysicalOffset(t *testing.T) {
	manager := NewOffsetManager(nil)

	pathID := "path-1"
	realStreamID := uint64(1)
	err := manager.RegisterPhysicalStream(pathID, realStreamID)
	if err != nil {
		t.Fatalf("failed to register physical stream: %v", err)
	}

	tests := []struct {
		name        string
		offset      uint64
		dataLength  uint32
		expectError bool
	}{
		{
			name:        "first update",
			offset:      0,
			dataLength:  100,
			expectError: false,
		},
		{
			name:        "sequential update",
			offset:      100,
			dataLength:  200,
			expectError: false,
		},
		{
			name:        "non-sequential update",
			offset:      400,
			dataLength:  100,
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := manager.UpdatePhysicalOffset(pathID, realStreamID, tt.offset, tt.dataLength)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			// Verify offset was updated
			offsets, err := manager.GetPhysicalOffset(pathID, realStreamID)
			if err != nil {
				t.Errorf("failed to get physical offset: %v", err)
				return
			}

			expectedMaxOffset := tt.offset + uint64(tt.dataLength)
			if offsets.MaxOffset < expectedMaxOffset {
				t.Errorf("expected max offset >= %d, got %d", expectedMaxOffset, offsets.MaxOffset)
			}

			expectedBytesRead := offsets.BytesRead
			if expectedBytesRead < uint64(tt.dataLength) {
				t.Errorf("bytes read should have increased by at least %d", tt.dataLength)
			}
		})
	}
}

func TestOffsetManager_ValidateOffsets(t *testing.T) {
	manager := NewOffsetManager(nil)

	// Setup streams
	logicalStreamID := uint64(1)
	pathID := "path-1"
	realStreamID := uint64(1)

	manager.RegisterLogicalStream(logicalStreamID)
	manager.RegisterPhysicalStream(pathID, realStreamID)

	// Update offsets to establish some baseline
	manager.UpdateLogicalOffset(logicalStreamID, 0, 1000)
	manager.UpdatePhysicalOffset(pathID, realStreamID, 0, 1000)

	tests := []struct {
		name        string
		testFunc    func() error
		expectError bool
	}{
		{
			name: "valid logical offset",
			testFunc: func() error {
				return manager.ValidateLogicalOffset(logicalStreamID, 500)
			},
			expectError: false,
		},
		{
			name: "valid physical offset",
			testFunc: func() error {
				return manager.ValidatePhysicalOffset(pathID, realStreamID, 500)
			},
			expectError: false,
		},
		{
			name: "invalid logical offset - too far ahead",
			testFunc: func() error {
				return manager.ValidateLogicalOffset(logicalStreamID, 2000000) // Way beyond max
			},
			expectError: true,
		},
		{
			name: "invalid physical offset - too far ahead",
			testFunc: func() error {
				return manager.ValidatePhysicalOffset(pathID, realStreamID, 2000000) // Way beyond max
			},
			expectError: true,
		},
		{
			name: "unregistered logical stream",
			testFunc: func() error {
				return manager.ValidateLogicalOffset(999, 500)
			},
			expectError: true,
		},
		{
			name: "unregistered physical stream",
			testFunc: func() error {
				return manager.ValidatePhysicalOffset("unknown-path", realStreamID, 500)
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.testFunc()

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestOffsetManager_GetOffsetStatistics(t *testing.T) {
	manager := NewOffsetManager(nil)

	// Register some streams
	manager.RegisterLogicalStream(1)
	manager.RegisterLogicalStream(2)
	manager.RegisterPhysicalStream("path-1", 1)
	manager.RegisterPhysicalStream("path-1", 2)
	manager.RegisterPhysicalStream("path-2", 1)

	// Create some mappings
	manager.CreateOffsetMapping(1, "path-1", 1)
	manager.CreateOffsetMapping(2, "path-1", 2)

	// Create some gaps
	manager.UpdateLogicalOffset(1, 0, 100)
	manager.UpdateLogicalOffset(1, 200, 100) // Creates gap from 100-200

	stats := manager.GetOffsetStatistics()

	if stats.LogicalStreams != 2 {
		t.Errorf("expected 2 logical streams, got %d", stats.LogicalStreams)
	}

	if stats.PhysicalStreams != 3 {
		t.Errorf("expected 3 physical streams, got %d", stats.PhysicalStreams)
	}

	if stats.OffsetMappings != 2 {
		t.Errorf("expected 2 offset mappings, got %d", stats.OffsetMappings)
	}

	if stats.TotalGaps == 0 {
		t.Errorf("expected some gaps to be tracked")
	}
}

func TestOffsetManager_CleanupExpiredMappings(t *testing.T) {
	manager := NewOffsetManager(nil)

	// Setup streams and mappings
	manager.RegisterLogicalStream(1)
	manager.RegisterPhysicalStream("path-1", 1)
	manager.CreateOffsetMapping(1, "path-1", 1)

	// Get initial mapping count
	initialStats := manager.GetOffsetStatistics()
	initialMappings := initialStats.OffsetMappings

	if initialMappings == 0 {
		t.Fatalf("expected at least one mapping to be created")
	}

	// Cleanup with very short max age (should remove all mappings)
	removed := manager.CleanupExpiredMappings(1) // 1 nanosecond max age

	if removed != initialMappings {
		t.Errorf("expected to remove %d mappings, removed %d", initialMappings, removed)
	}

	// Verify mappings were removed
	finalStats := manager.GetOffsetStatistics()
	if finalStats.OffsetMappings != 0 {
		t.Errorf("expected 0 mappings after cleanup, got %d", finalStats.OffsetMappings)
	}
}

func BenchmarkOffsetManager_MapLogicalToPhysical(b *testing.B) {
	manager := NewOffsetManager(nil)

	// Setup
	logicalStreamID := uint64(1)
	pathID := "path-1"
	realStreamID := uint64(1)

	manager.RegisterLogicalStream(logicalStreamID)
	manager.RegisterPhysicalStream(pathID, realStreamID)
	manager.CreateOffsetMapping(logicalStreamID, pathID, realStreamID)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := manager.MapLogicalToPhysical(
			logicalStreamID, pathID, realStreamID, uint64(i*100), 100)
		if err != nil {
			b.Fatalf("unexpected error: %v", err)
		}
	}
}

func BenchmarkOffsetManager_UpdateLogicalOffset(b *testing.B) {
	manager := NewOffsetManager(nil)

	logicalStreamID := uint64(1)
	manager.RegisterLogicalStream(logicalStreamID)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := manager.UpdateLogicalOffset(logicalStreamID, uint64(i*100), 100)
		if err != nil {
			b.Fatalf("unexpected error: %v", err)
		}
	}
}
