package filetransfer

import (
	"crypto/md5"
	"crypto/sha256"
	"fmt"
	"time"
)

// FileChunk represents a portion of a file with complete metadata
type FileChunk struct {
	ChunkID      uint32            `json:"chunk_id"`      // Unique identifier for this chunk
	SequenceNum  uint32            `json:"sequence_num"`  // Sequential number in the file (0-based)
	Data         []byte            `json:"data"`          // The actual chunk data
	Checksum     string            `json:"checksum"`      // MD5 or SHA256 checksum of the data
	ChecksumType string            `json:"checksum_type"` // "md5" or "sha256"
	IsLast       bool              `json:"is_last"`       // True if this is the final chunk
	TotalChunks  uint32            `json:"total_chunks"`  // Total number of chunks in the file
	Filename     string            `json:"filename"`      // Name of the source file
	Size         int32             `json:"size"`          // Size of this chunk in bytes
	Offset       int64             `json:"offset"`        // Offset position in the original file
	Timestamp    time.Time         `json:"timestamp"`     // When this chunk was created
	Metadata     map[string]string `json:"metadata"`      // Additional metadata
}

// NewFileChunk creates a new FileChunk with computed checksum
func NewFileChunk(chunkID, sequenceNum uint32, data []byte, filename string, offset int64, totalChunks uint32, isLast bool) *FileChunk {
	chunk := &FileChunk{
		ChunkID:      chunkID,
		SequenceNum:  sequenceNum,
		Data:         data,
		IsLast:       isLast,
		TotalChunks:  totalChunks,
		Filename:     filename,
		Size:         int32(len(data)),
		Offset:       offset,
		Timestamp:    time.Now(),
		ChecksumType: "sha256",
		Metadata:     make(map[string]string),
	}
	
	// Compute SHA256 checksum
	hash := sha256.Sum256(data)
	chunk.Checksum = fmt.Sprintf("%x", hash)
	
	return chunk
}

// ValidateChecksum verifies the integrity of the chunk data
func (fc *FileChunk) ValidateChecksum() error {
	var computedChecksum string
	
	switch fc.ChecksumType {
	case "md5":
		hash := md5.Sum(fc.Data)
		computedChecksum = fmt.Sprintf("%x", hash)
	case "sha256":
		hash := sha256.Sum256(fc.Data)
		computedChecksum = fmt.Sprintf("%x", hash)
	default:
		return fmt.Errorf("unsupported checksum type: %s", fc.ChecksumType)
	}
	
	if computedChecksum != fc.Checksum {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", fc.Checksum, computedChecksum)
	}
	
	return nil
}

// ChunkCommand represents coordination commands between servers
type ChunkCommand struct {
	Command      string            `json:"command"`       // Command type: "SEND_CHUNK", "CHUNK_SENT", "CHUNK_ERROR", "CHUNK_REQUEST"
	ChunkID      uint32            `json:"chunk_id"`      // ID of the chunk this command refers to
	SequenceNum  uint32            `json:"sequence_num"`  // Sequential number of the chunk
	Filename     string            `json:"filename"`      // Target filename
	StartOffset  int64             `json:"start_offset"`  // Starting byte offset in the file
	Size         int32             `json:"size"`          // Size of the chunk in bytes
	Checksum     string            `json:"checksum"`      // Expected checksum of the chunk
	ChecksumType string            `json:"checksum_type"` // Type of checksum used
	Priority     int               `json:"priority"`      // Priority level (higher = more urgent)
	Timeout      time.Duration     `json:"timeout"`       // Timeout for this command
	Metadata     map[string]string `json:"metadata"`      // Additional command metadata
	Timestamp    time.Time         `json:"timestamp"`     // When this command was created
	SenderID     string            `json:"sender_id"`     // ID of the sender (primary/secondary server)
	ReceiverID   string            `json:"receiver_id"`   // ID of the intended receiver
}

// NewChunkCommand creates a new chunk command
func NewChunkCommand(command string, chunkID, sequenceNum uint32, filename string, startOffset int64, size int32) *ChunkCommand {
	return &ChunkCommand{
		Command:      command,
		ChunkID:      chunkID,
		SequenceNum:  sequenceNum,
		Filename:     filename,
		StartOffset:  startOffset,
		Size:         size,
		ChecksumType: "sha256",
		Priority:     0,
		Timeout:      30 * time.Second, // Default 30 second timeout
		Metadata:     make(map[string]string),
		Timestamp:    time.Now(),
	}
}

// Validate checks if the command has all required fields
func (cc *ChunkCommand) Validate() error {
	if cc.Command == "" {
		return fmt.Errorf("command type cannot be empty")
	}
	
	if cc.Filename == "" {
		return fmt.Errorf("filename cannot be empty")
	}
	
	if cc.Size < 0 {
		return fmt.Errorf("chunk size cannot be negative: %d", cc.Size)
	}
	
	if cc.StartOffset < 0 {
		return fmt.Errorf("start offset cannot be negative: %d", cc.StartOffset)
	}
	
	// Validate command types
	validCommands := map[string]bool{
		"SEND_CHUNK":    true,
		"CHUNK_SENT":    true,
		"CHUNK_ERROR":   true,
		"CHUNK_REQUEST": true,
	}
	
	if !validCommands[cc.Command] {
		return fmt.Errorf("invalid command type: %s", cc.Command)
	}
	
	return nil
}

// FileMetadata contains comprehensive information about a file
type FileMetadata struct {
	Filename     string            `json:"filename"`      // Name of the file
	Size         int64             `json:"size"`          // Total size in bytes
	ChunkSize    int32             `json:"chunk_size"`    // Size of each chunk (except possibly the last)
	TotalChunks  uint32            `json:"total_chunks"`  // Total number of chunks
	Checksum     string            `json:"checksum"`      // Checksum of the complete file
	ChecksumType string            `json:"checksum_type"` // Type of checksum used
	CreatedAt    time.Time         `json:"created_at"`    // File creation time
	ModifiedAt   time.Time         `json:"modified_at"`   // File last modification time
	ContentType  string            `json:"content_type"`  // MIME type of the file
	Encoding     string            `json:"encoding"`      // File encoding (e.g., "utf-8", "binary")
	Permissions  string            `json:"permissions"`   // File permissions (e.g., "644")
	Owner        string            `json:"owner"`         // File owner
	Group        string            `json:"group"`         // File group
	Metadata     map[string]string `json:"metadata"`      // Additional file metadata
	Version      string            `json:"version"`       // File version if applicable
	Tags         []string          `json:"tags"`          // File tags for categorization
}

// NewFileMetadata creates a new FileMetadata instance
func NewFileMetadata(filename string, size int64, chunkSize int32) *FileMetadata {
	totalChunks := uint32((size + int64(chunkSize) - 1) / int64(chunkSize)) // Ceiling division
	
	return &FileMetadata{
		Filename:     filename,
		Size:         size,
		ChunkSize:    chunkSize,
		TotalChunks:  totalChunks,
		ChecksumType: "sha256",
		CreatedAt:    time.Now(),
		ModifiedAt:   time.Now(),
		ContentType:  "application/octet-stream", // Default binary type
		Encoding:     "binary",
		Permissions:  "644",
		Metadata:     make(map[string]string),
		Tags:         make([]string, 0),
	}
}

// Validate checks if the metadata is consistent and valid
func (fm *FileMetadata) Validate() error {
	if fm.Filename == "" {
		return fmt.Errorf("filename cannot be empty")
	}
	
	if fm.Size < 0 {
		return fmt.Errorf("file size cannot be negative: %d", fm.Size)
	}
	
	if fm.ChunkSize <= 0 {
		return fmt.Errorf("chunk size must be positive: %d", fm.ChunkSize)
	}
	
	if fm.TotalChunks == 0 && fm.Size > 0 {
		return fmt.Errorf("total chunks cannot be zero for non-empty file")
	}
	
	// Validate that total chunks calculation is correct
	expectedChunks := uint32((fm.Size + int64(fm.ChunkSize) - 1) / int64(fm.ChunkSize))
	if fm.TotalChunks != expectedChunks {
		return fmt.Errorf("total chunks mismatch: expected %d, got %d", expectedChunks, fm.TotalChunks)
	}
	
	// Validate checksum type
	validChecksumTypes := map[string]bool{
		"md5":    true,
		"sha256": true,
	}
	
	if fm.ChecksumType != "" && !validChecksumTypes[fm.ChecksumType] {
		return fmt.Errorf("invalid checksum type: %s", fm.ChecksumType)
	}
	
	return nil
}

// GetChunkOffset calculates the byte offset for a given chunk sequence number
func (fm *FileMetadata) GetChunkOffset(sequenceNum uint32) int64 {
	return int64(sequenceNum) * int64(fm.ChunkSize)
}

// GetChunkSize calculates the actual size of a specific chunk (last chunk may be smaller)
func (fm *FileMetadata) GetChunkSize(sequenceNum uint32) int32 {
	if sequenceNum >= fm.TotalChunks {
		return 0 // Invalid chunk number
	}
	
	// For the last chunk, calculate remaining bytes
	if sequenceNum == fm.TotalChunks-1 {
		remainingBytes := fm.Size - int64(sequenceNum)*int64(fm.ChunkSize)
		return int32(remainingBytes)
	}
	
	return fm.ChunkSize
}

// IsLastChunk checks if the given sequence number represents the last chunk
func (fm *FileMetadata) IsLastChunk(sequenceNum uint32) bool {
	return sequenceNum == fm.TotalChunks-1
}