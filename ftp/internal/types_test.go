package filetransfer

import (
	"crypto/sha256"
	"fmt"
	"testing"
	"time"
)

func TestFileChunk_NewFileChunk(t *testing.T) {
	data := []byte("Hello, World!")
	filename := "test.txt"
	chunkID := uint32(1)
	sequenceNum := uint32(0)
	offset := int64(0)
	totalChunks := uint32(1)
	isLast := true

	chunk := NewFileChunk(chunkID, sequenceNum, data, filename, offset, totalChunks, isLast)

	// Verify basic fields
	if chunk.ChunkID != chunkID {
		t.Errorf("Expected ChunkID %d, got %d", chunkID, chunk.ChunkID)
	}
	if chunk.SequenceNum != sequenceNum {
		t.Errorf("Expected SequenceNum %d, got %d", sequenceNum, chunk.SequenceNum)
	}
	if chunk.Filename != filename {
		t.Errorf("Expected Filename %s, got %s", filename, chunk.Filename)
	}
	if chunk.IsLast != isLast {
		t.Errorf("Expected IsLast %t, got %t", isLast, chunk.IsLast)
	}
	if chunk.TotalChunks != totalChunks {
		t.Errorf("Expected TotalChunks %d, got %d", totalChunks, chunk.TotalChunks)
	}
	if chunk.Size != int32(len(data)) {
		t.Errorf("Expected Size %d, got %d", len(data), chunk.Size)
	}
	if chunk.Offset != offset {
		t.Errorf("Expected Offset %d, got %d", offset, chunk.Offset)
	}

	// Verify checksum
	expectedHash := sha256.Sum256(data)
	expectedChecksum := fmt.Sprintf("%x", expectedHash)
	if chunk.Checksum != expectedChecksum {
		t.Errorf("Expected Checksum %s, got %s", expectedChecksum, chunk.Checksum)
	}
	if chunk.ChecksumType != "sha256" {
		t.Errorf("Expected ChecksumType sha256, got %s", chunk.ChecksumType)
	}

	// Verify metadata map is initialized
	if chunk.Metadata == nil {
		t.Error("Metadata map should be initialized")
	}
}

func TestFileChunk_ValidateChecksum(t *testing.T) {
	data := []byte("Test data for checksum validation")
	chunk := NewFileChunk(1, 0, data, "test.txt", 0, 1, true)

	// Valid checksum should pass
	err := chunk.ValidateChecksum()
	if err != nil {
		t.Errorf("Valid checksum should pass validation: %v", err)
	}

	// Corrupt the checksum
	chunk.Checksum = "invalid_checksum"
	err = chunk.ValidateChecksum()
	if err == nil {
		t.Error("Invalid checksum should fail validation")
	}

	// Test unsupported checksum type
	chunk.ChecksumType = "unsupported"
	err = chunk.ValidateChecksum()
	if err == nil {
		t.Error("Unsupported checksum type should fail validation")
	}
}

func TestChunkCommand_NewChunkCommand(t *testing.T) {
	command := "SEND_CHUNK"
	chunkID := uint32(42)
	sequenceNum := uint32(5)
	filename := "example.txt"
	startOffset := int64(1024)
	size := int32(512)

	cmd := NewChunkCommand(command, chunkID, sequenceNum, filename, startOffset, size)

	if cmd.Command != command {
		t.Errorf("Expected Command %s, got %s", command, cmd.Command)
	}
	if cmd.ChunkID != chunkID {
		t.Errorf("Expected ChunkID %d, got %d", chunkID, cmd.ChunkID)
	}
	if cmd.SequenceNum != sequenceNum {
		t.Errorf("Expected SequenceNum %d, got %d", sequenceNum, cmd.SequenceNum)
	}
	if cmd.Filename != filename {
		t.Errorf("Expected Filename %s, got %s", filename, cmd.Filename)
	}
	if cmd.StartOffset != startOffset {
		t.Errorf("Expected StartOffset %d, got %d", startOffset, cmd.StartOffset)
	}
	if cmd.Size != size {
		t.Errorf("Expected Size %d, got %d", size, cmd.Size)
	}
	if cmd.ChecksumType != "sha256" {
		t.Errorf("Expected ChecksumType sha256, got %s", cmd.ChecksumType)
	}
	if cmd.Timeout != 30*time.Second {
		t.Errorf("Expected Timeout 30s, got %v", cmd.Timeout)
	}
	if cmd.Metadata == nil {
		t.Error("Metadata map should be initialized")
	}
}

func TestChunkCommand_Validate(t *testing.T) {
	// Valid command
	cmd := NewChunkCommand("SEND_CHUNK", 1, 0, "test.txt", 0, 1024)
	err := cmd.Validate()
	if err != nil {
		t.Errorf("Valid command should pass validation: %v", err)
	}

	// Empty command type
	cmd.Command = ""
	err = cmd.Validate()
	if err == nil {
		t.Error("Empty command type should fail validation")
	}

	// Empty filename
	cmd.Command = "SEND_CHUNK"
	cmd.Filename = ""
	err = cmd.Validate()
	if err == nil {
		t.Error("Empty filename should fail validation")
	}

	// Negative size
	cmd.Filename = "test.txt"
	cmd.Size = -1
	err = cmd.Validate()
	if err == nil {
		t.Error("Negative size should fail validation")
	}

	// Negative offset
	cmd.Size = 1024
	cmd.StartOffset = -1
	err = cmd.Validate()
	if err == nil {
		t.Error("Negative offset should fail validation")
	}

	// Invalid command type
	cmd.StartOffset = 0
	cmd.Command = "INVALID_COMMAND"
	err = cmd.Validate()
	if err == nil {
		t.Error("Invalid command type should fail validation")
	}
}

func TestFileMetadata_NewFileMetadata(t *testing.T) {
	filename := "large_file.bin"
	size := int64(10240) // 10KB
	chunkSize := int32(1024) // 1KB chunks
	expectedChunks := uint32(10) // 10240 / 1024 = 10

	metadata := NewFileMetadata(filename, size, chunkSize)

	if metadata.Filename != filename {
		t.Errorf("Expected Filename %s, got %s", filename, metadata.Filename)
	}
	if metadata.Size != size {
		t.Errorf("Expected Size %d, got %d", size, metadata.Size)
	}
	if metadata.ChunkSize != chunkSize {
		t.Errorf("Expected ChunkSize %d, got %d", chunkSize, metadata.ChunkSize)
	}
	if metadata.TotalChunks != expectedChunks {
		t.Errorf("Expected TotalChunks %d, got %d", expectedChunks, metadata.TotalChunks)
	}
	if metadata.ChecksumType != "sha256" {
		t.Errorf("Expected ChecksumType sha256, got %s", metadata.ChecksumType)
	}
	if metadata.ContentType != "application/octet-stream" {
		t.Errorf("Expected ContentType application/octet-stream, got %s", metadata.ContentType)
	}
	if metadata.Metadata == nil {
		t.Error("Metadata map should be initialized")
	}
	if metadata.Tags == nil {
		t.Error("Tags slice should be initialized")
	}
}

func TestFileMetadata_Validate(t *testing.T) {
	// Valid metadata
	metadata := NewFileMetadata("test.txt", 1024, 256)
	err := metadata.Validate()
	if err != nil {
		t.Errorf("Valid metadata should pass validation: %v", err)
	}

	// Empty filename
	metadata.Filename = ""
	err = metadata.Validate()
	if err == nil {
		t.Error("Empty filename should fail validation")
	}

	// Negative size
	metadata.Filename = "test.txt"
	metadata.Size = -1
	err = metadata.Validate()
	if err == nil {
		t.Error("Negative size should fail validation")
	}

	// Zero chunk size
	metadata.Size = 1024
	metadata.ChunkSize = 0
	err = metadata.Validate()
	if err == nil {
		t.Error("Zero chunk size should fail validation")
	}

	// Inconsistent total chunks
	metadata.ChunkSize = 256
	metadata.TotalChunks = 999 // Should be 4 for 1024/256
	err = metadata.Validate()
	if err == nil {
		t.Error("Inconsistent total chunks should fail validation")
	}

	// Invalid checksum type
	metadata.TotalChunks = 4
	metadata.ChecksumType = "invalid"
	err = metadata.Validate()
	if err == nil {
		t.Error("Invalid checksum type should fail validation")
	}
}

func TestFileMetadata_GetChunkOffset(t *testing.T) {
	metadata := NewFileMetadata("test.txt", 1024, 256)

	tests := []struct {
		sequenceNum    uint32
		expectedOffset int64
	}{
		{0, 0},
		{1, 256},
		{2, 512},
		{3, 768},
	}

	for _, test := range tests {
		offset := metadata.GetChunkOffset(test.sequenceNum)
		if offset != test.expectedOffset {
			t.Errorf("For sequence %d, expected offset %d, got %d", 
				test.sequenceNum, test.expectedOffset, offset)
		}
	}
}

func TestFileMetadata_GetChunkSize(t *testing.T) {
	// File size 1000 bytes, chunk size 256 bytes = 4 chunks (256, 256, 256, 232)
	metadata := NewFileMetadata("test.txt", 1000, 256)

	tests := []struct {
		sequenceNum  uint32
		expectedSize int32
	}{
		{0, 256}, // First chunk: full size
		{1, 256}, // Second chunk: full size
		{2, 256}, // Third chunk: full size
		{3, 232}, // Last chunk: remaining bytes (1000 - 3*256 = 232)
		{4, 0},   // Invalid chunk: should return 0
	}

	for _, test := range tests {
		size := metadata.GetChunkSize(test.sequenceNum)
		if size != test.expectedSize {
			t.Errorf("For sequence %d, expected size %d, got %d", 
				test.sequenceNum, test.expectedSize, size)
		}
	}
}

func TestFileMetadata_IsLastChunk(t *testing.T) {
	metadata := NewFileMetadata("test.txt", 1024, 256) // 4 chunks total

	tests := []struct {
		sequenceNum uint32
		isLast      bool
	}{
		{0, false},
		{1, false},
		{2, false},
		{3, true}, // Last chunk (0-indexed, so chunk 3 is the 4th chunk)
		{4, false}, // Invalid chunk
	}

	for _, test := range tests {
		isLast := metadata.IsLastChunk(test.sequenceNum)
		if isLast != test.isLast {
			t.Errorf("For sequence %d, expected isLast %t, got %t", 
				test.sequenceNum, test.isLast, isLast)
		}
	}
}