package data

import (
	"testing"
	"time"
)

func TestFrameEncapsulator_CreateDataFrame(t *testing.T) {
	encapsulator := NewFrameEncapsulator(nil)

	tests := []struct {
		name            string
		logicalStreamID uint64
		pathID          string
		offset          uint64
		data            []byte
		fin             bool
		expectError     bool
	}{
		{
			name:            "valid frame",
			logicalStreamID: 1,
			pathID:          "path-1",
			offset:          0,
			data:            []byte("test data"),
			fin:             false,
			expectError:     false,
		},
		{
			name:            "frame with fin",
			logicalStreamID: 2,
			pathID:          "path-2",
			offset:          100,
			data:            []byte("final data"),
			fin:             true,
			expectError:     false,
		},
		{
			name:            "zero logical stream ID",
			logicalStreamID: 0,
			pathID:          "path-1",
			offset:          0,
			data:            []byte("test data"),
			fin:             false,
			expectError:     true,
		},
		{
			name:            "empty path ID",
			logicalStreamID: 1,
			pathID:          "",
			offset:          0,
			data:            []byte("test data"),
			fin:             false,
			expectError:     true,
		},
		{
			name:            "empty data",
			logicalStreamID: 1,
			pathID:          "path-1",
			offset:          0,
			data:            []byte{},
			fin:             false,
			expectError:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			frame, err := encapsulator.CreateDataFrame(tt.logicalStreamID, tt.pathID, tt.offset, tt.data, tt.fin)

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

			// Validate frame properties
			if frame.LogicalStreamId != tt.logicalStreamID {
				t.Errorf("expected logical stream ID %d, got %d", tt.logicalStreamID, frame.LogicalStreamId)
			}

			if frame.PathId != tt.pathID {
				t.Errorf("expected path ID %s, got %s", tt.pathID, frame.PathId)
			}

			if frame.Offset != tt.offset {
				t.Errorf("expected offset %d, got %d", tt.offset, frame.Offset)
			}

			if string(frame.Data) != string(tt.data) {
				t.Errorf("expected data %s, got %s", string(tt.data), string(frame.Data))
			}

			if frame.Fin != tt.fin {
				t.Errorf("expected fin %t, got %t", tt.fin, frame.Fin)
			}

			if frame.FrameId == 0 {
				t.Errorf("frame ID should not be zero")
			}

			if frame.DataLength != uint32(len(tt.data)) {
				t.Errorf("expected data length %d, got %d", len(tt.data), frame.DataLength)
			}

			// Check timestamp is set (if enabled)
			if encapsulator.config.EnableTimestamps && frame.Timestamp == 0 {
				t.Errorf("timestamp should be set when enabled")
			}

			// Check checksum is set (if enabled)
			if encapsulator.config.EnableChecksums && frame.Checksum == 0 {
				t.Errorf("checksum should be set when enabled")
			}
		})
	}
}

func TestFrameEncapsulator_EncapsulateDecapsulate(t *testing.T) {
	encapsulator := NewFrameEncapsulator(nil)

	// Create a test frame
	originalFrame, err := encapsulator.CreateDataFrame(1, "path-1", 0, []byte("test data"), false)
	if err != nil {
		t.Fatalf("failed to create test frame: %v", err)
	}

	// Encapsulate the frame
	data, err := encapsulator.EncapsulateFrame(originalFrame)
	if err != nil {
		t.Fatalf("failed to encapsulate frame: %v", err)
	}

	if len(data) == 0 {
		t.Fatalf("encapsulated data should not be empty")
	}

	// Decapsulate the frame
	decapsulatedFrame, err := encapsulator.DecapsulateFrame(data)
	if err != nil {
		t.Fatalf("failed to decapsulate frame: %v", err)
	}

	// Compare frames
	if decapsulatedFrame.FrameId != originalFrame.FrameId {
		t.Errorf("frame ID mismatch: expected %d, got %d", originalFrame.FrameId, decapsulatedFrame.FrameId)
	}

	if decapsulatedFrame.LogicalStreamId != originalFrame.LogicalStreamId {
		t.Errorf("logical stream ID mismatch: expected %d, got %d", originalFrame.LogicalStreamId, decapsulatedFrame.LogicalStreamId)
	}

	if decapsulatedFrame.PathId != originalFrame.PathId {
		t.Errorf("path ID mismatch: expected %s, got %s", originalFrame.PathId, decapsulatedFrame.PathId)
	}

	if string(decapsulatedFrame.Data) != string(originalFrame.Data) {
		t.Errorf("data mismatch: expected %s, got %s", string(originalFrame.Data), string(decapsulatedFrame.Data))
	}
}

func TestFrameEncapsulator_ExtractIdentifiers(t *testing.T) {
	encapsulator := NewFrameEncapsulator(nil)

	frame, err := encapsulator.CreateDataFrame(123, "path-456", 0, []byte("test"), false)
	if err != nil {
		t.Fatalf("failed to create test frame: %v", err)
	}

	// Test extracting logical stream ID
	streamID := encapsulator.ExtractLogicalStreamID(frame)
	if streamID != 123 {
		t.Errorf("expected logical stream ID 123, got %d", streamID)
	}

	// Test extracting path ID
	pathID := encapsulator.ExtractPathID(frame)
	if pathID != "path-456" {
		t.Errorf("expected path ID 'path-456', got '%s'", pathID)
	}

	// Test with nil frame
	streamID = encapsulator.ExtractLogicalStreamID(nil)
	if streamID != 0 {
		t.Errorf("expected 0 for nil frame, got %d", streamID)
	}

	pathID = encapsulator.ExtractPathID(nil)
	if pathID != "" {
		t.Errorf("expected empty string for nil frame, got '%s'", pathID)
	}
}

func TestFrameEncapsulator_FrameIdentifier(t *testing.T) {
	encapsulator := NewFrameEncapsulator(nil)

	// Test creating frame identifier
	identifier := encapsulator.CreateFrameIdentifier(123, "path-456")
	expected := "123:path-456"
	if identifier != expected {
		t.Errorf("expected identifier '%s', got '%s'", expected, identifier)
	}

	// Test parsing frame identifier
	streamID, pathID, err := encapsulator.ParseFrameIdentifier(identifier)
	if err != nil {
		t.Errorf("unexpected error parsing identifier: %v", err)
	}

	if streamID != 123 {
		t.Errorf("expected stream ID 123, got %d", streamID)
	}

	if pathID != "path-456" {
		t.Errorf("expected path ID 'path-456', got '%s'", pathID)
	}

	// Test parsing invalid identifier
	_, _, err = encapsulator.ParseFrameIdentifier("invalid")
	if err == nil {
		t.Errorf("expected error for invalid identifier")
	}
}

func TestFrameEncapsulator_CloneFrame(t *testing.T) {
	encapsulator := NewFrameEncapsulator(nil)

	// Create original frame
	original, err := encapsulator.CreateDataFrame(1, "path-1", 100, []byte("test data"), true)
	if err != nil {
		t.Fatalf("failed to create original frame: %v", err)
	}

	// Clone the frame
	cloned := encapsulator.CloneFrame(original)

	// Verify clone is not nil
	if cloned == nil {
		t.Fatalf("cloned frame should not be nil")
	}

	// Verify all fields are copied
	if cloned.FrameId != original.FrameId {
		t.Errorf("frame ID mismatch: expected %d, got %d", original.FrameId, cloned.FrameId)
	}

	if cloned.LogicalStreamId != original.LogicalStreamId {
		t.Errorf("logical stream ID mismatch: expected %d, got %d", original.LogicalStreamId, cloned.LogicalStreamId)
	}

	if cloned.PathId != original.PathId {
		t.Errorf("path ID mismatch: expected %s, got %s", original.PathId, cloned.PathId)
	}

	if string(cloned.Data) != string(original.Data) {
		t.Errorf("data mismatch: expected %s, got %s", string(original.Data), string(cloned.Data))
	}

	// Verify data is actually copied (not just referenced)
	cloned.Data[0] = 'X'
	if original.Data[0] == 'X' {
		t.Errorf("data should be copied, not referenced")
	}

	// Test cloning nil frame
	nilClone := encapsulator.CloneFrame(nil)
	if nilClone != nil {
		t.Errorf("cloning nil frame should return nil")
	}
}

func TestFrameEncapsulator_UpdateFrameForPath(t *testing.T) {
	encapsulator := NewFrameEncapsulator(nil)

	// Create original frame
	frame, err := encapsulator.CreateDataFrame(1, "path-1", 0, []byte("test"), false)
	if err != nil {
		t.Fatalf("failed to create frame: %v", err)
	}

	originalTimestamp := frame.Timestamp
	originalChecksum := frame.Checksum

	// Wait a bit to ensure timestamp changes
	time.Sleep(1 * time.Millisecond)

	// Update frame for new path
	err = encapsulator.UpdateFrameForPath(frame, "path-2")
	if err != nil {
		t.Errorf("unexpected error updating frame: %v", err)
	}

	// Verify path ID was updated
	if frame.PathId != "path-2" {
		t.Errorf("expected path ID 'path-2', got '%s'", frame.PathId)
	}

	// Verify timestamp was updated (if enabled)
	if encapsulator.config.EnableTimestamps && frame.Timestamp == originalTimestamp {
		t.Errorf("timestamp should have been updated")
	}

	// Verify checksum was updated (if enabled)
	if encapsulator.config.EnableChecksums && frame.Checksum == originalChecksum {
		t.Errorf("checksum should have been updated")
	}

	// Test with nil frame
	err = encapsulator.UpdateFrameForPath(nil, "path-3")
	if err == nil {
		t.Errorf("expected error for nil frame")
	}

	// Test with empty path ID
	err = encapsulator.UpdateFrameForPath(frame, "")
	if err == nil {
		t.Errorf("expected error for empty path ID")
	}
}

func TestFrameEncapsulator_GetFrameSize(t *testing.T) {
	encapsulator := NewFrameEncapsulator(nil)

	// Create frames with different data sizes
	testCases := []struct {
		name     string
		dataSize int
	}{
		{"empty data", 0},
		{"small data", 10},
		{"medium data", 100},
		{"large data", 1000},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			data := make([]byte, tc.dataSize)
			for i := range data {
				data[i] = byte(i % 256)
			}

			frame, err := encapsulator.CreateDataFrame(1, "path-1", 0, data, false)
			if err != nil {
				t.Fatalf("failed to create frame: %v", err)
			}

			size, err := encapsulator.GetFrameSize(frame)
			if err != nil {
				t.Errorf("failed to get frame size: %v", err)
			}

			if size <= 0 {
				t.Errorf("frame size should be positive, got %d", size)
			}

			// Size should be larger than just the data size due to metadata
			if size <= tc.dataSize {
				t.Errorf("frame size %d should be larger than data size %d", size, tc.dataSize)
			}
		})
	}
}

func TestFrameEncapsulator_Configuration(t *testing.T) {
	// Test with checksums disabled
	config := &FrameEncapsulatorConfig{
		EnableChecksums:    false,
		EnableTimestamps:   true,
		MaxFrameSize:       1024,
		MaxDataSize:        512,
		CompressionEnabled: false,
	}

	encapsulator := NewFrameEncapsulator(config)

	frame, err := encapsulator.CreateDataFrame(1, "path-1", 0, []byte("test"), false)
	if err != nil {
		t.Fatalf("failed to create frame: %v", err)
	}

	// Checksum should be 0 when disabled
	if frame.Checksum != 0 {
		t.Errorf("checksum should be 0 when disabled, got %d", frame.Checksum)
	}

	// Timestamp should be set when enabled
	if frame.Timestamp == 0 {
		t.Errorf("timestamp should be set when enabled")
	}

	// Test with timestamps disabled
	config.EnableTimestamps = false
	encapsulator = NewFrameEncapsulator(config)

	frame, err = encapsulator.CreateDataFrame(1, "path-1", 0, []byte("test"), false)
	if err != nil {
		t.Fatalf("failed to create frame: %v", err)
	}

	// Timestamp should be 0 when disabled
	if frame.Timestamp != 0 {
		t.Errorf("timestamp should be 0 when disabled, got %d", frame.Timestamp)
	}
}

func BenchmarkFrameEncapsulator_CreateDataFrame(b *testing.B) {
	encapsulator := NewFrameEncapsulator(nil)
	data := make([]byte, 1024)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := encapsulator.CreateDataFrame(uint64(i), "path-1", uint64(i*1024), data, false)
		if err != nil {
			b.Fatalf("failed to create frame: %v", err)
		}
	}
}

func BenchmarkFrameEncapsulator_EncapsulateDecapsulate(b *testing.B) {
	encapsulator := NewFrameEncapsulator(nil)
	frame, _ := encapsulator.CreateDataFrame(1, "path-1", 0, make([]byte, 1024), false)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		data, err := encapsulator.EncapsulateFrame(frame)
		if err != nil {
			b.Fatalf("failed to encapsulate: %v", err)
		}

		_, err = encapsulator.DecapsulateFrame(data)
		if err != nil {
			b.Fatalf("failed to decapsulate: %v", err)
		}
	}
}