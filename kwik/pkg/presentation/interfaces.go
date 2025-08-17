package presentation

import (
	"fmt"
	"strings"
	"time"
)

// DataPresentationManager is the central manager that coordinates data presentation
type DataPresentationManager interface {
	// Stream management
	CreateStreamBuffer(streamID uint64, metadata interface{}) error
	RemoveStreamBuffer(streamID uint64) error
	GetStreamBuffer(streamID uint64) (StreamBuffer, error)
	
	// Data reading
	ReadFromStream(streamID uint64, buffer []byte) (int, error)
	ReadFromStreamWithTimeout(streamID uint64, buffer []byte, timeout time.Duration) (int, error)
	
	// Data writing
	WriteToStream(streamID uint64, data []byte, offset uint64, metadata interface{}) error
	
	// Receive window management
	GetReceiveWindowStatus() *ReceiveWindowStatus
	SetReceiveWindowSize(size uint64) error
	
	// Backpressure management
	IsBackpressureActive(streamID uint64) bool
	GetBackpressureStatus() *BackpressureStatus
	
	// Statistics and monitoring
	GetStreamStats(streamID uint64) *StreamStats
	GetGlobalStats() *GlobalPresentationStats
	
	// Lifecycle
	Start() error
	Stop() error
	Shutdown() error
}

// StreamBuffer represents a dedicated buffer for a single data stream
type StreamBuffer interface {
	// Data writing (from aggregators)
	Write(data []byte, offset uint64) error
	WriteWithMetadata(data []byte, offset uint64, metadata *DataMetadata) error
	
	// Data reading (to application)
	Read(buffer []byte) (int, error)
	ReadContiguous(buffer []byte) (int, error)
	
	// Read position management
	GetReadPosition() uint64
	SetReadPosition(position uint64) error
	AdvanceReadPosition(bytes int) error
	
	// Buffer state
	GetAvailableBytes() int
	GetContiguousBytes() int
	HasGaps() bool
	GetNextGapPosition() (uint64, bool)
	
	// Metadata
	GetStreamMetadata() *StreamMetadata
	UpdateStreamMetadata(metadata *StreamMetadata) error
	
	// Flow control
	GetBufferUsage() *BufferUsage
	IsBackpressureNeeded() bool
	
	// Cleanup
	Cleanup() error
	Close() error
}

// ReceiveWindowManager manages the global receive window with per-stream allocation
type ReceiveWindowManager interface {
	// Window configuration
	SetWindowSize(size uint64) error
	GetWindowSize() uint64
	GetAvailableWindow() uint64
	
	// Window allocation management
	AllocateWindow(streamID uint64, size uint64) error
	ReleaseWindow(streamID uint64, size uint64) error
	
	// Window sliding
	SlideWindow(bytesConsumed uint64) error
	
	// Window state
	GetWindowStatus() *ReceiveWindowStatus
	IsWindowFull() bool
	GetWindowUtilization() float64
	
	// Notifications
	SetWindowFullCallback(callback func()) error
	SetWindowAvailableCallback(callback func()) error
}

// BackpressureManager handles flow control and backpressure mechanisms
type BackpressureManager interface {
	// Backpressure activation/deactivation
	ActivateBackpressure(streamID uint64, reason BackpressureReason) error
	DeactivateBackpressure(streamID uint64) error
	
	// Backpressure state
	IsBackpressureActive(streamID uint64) bool
	GetBackpressureReason(streamID uint64) BackpressureReason
	
	// Global backpressure
	ActivateGlobalBackpressure(reason BackpressureReason) error
	DeactivateGlobalBackpressure() error
	IsGlobalBackpressureActive() bool
	
	// Notifications
	SetBackpressureCallback(callback BackpressureCallback) error
	
	// Metrics
	GetBackpressureStats() *BackpressureStats
}

// StreamMetadata contains metadata information for a stream
type StreamMetadata struct {
	StreamID      uint64                 `json:"stream_id"`
	StreamType    StreamType             `json:"stream_type"`
	Priority      StreamPriority         `json:"priority"`
	MaxBufferSize uint64                 `json:"max_buffer_size"`
	CreatedAt     time.Time              `json:"created_at"`
	LastActivity  time.Time              `json:"last_activity"`
	Properties    map[string]interface{} `json:"properties"`
}

// DataMetadata contains metadata for individual data chunks
type DataMetadata struct {
	Offset     uint64                 `json:"offset"`
	Length     uint64                 `json:"length"`
	Timestamp  time.Time              `json:"timestamp"`
	SourcePath string                 `json:"source_path"`
	Checksum   uint32                 `json:"checksum"`
	Flags      DataFlags              `json:"flags"`
	Properties map[string]interface{} `json:"properties"`
}

// ReceiveWindowStatus represents the current state of the receive window
type ReceiveWindowStatus struct {
	TotalSize             uint64            `json:"total_size"`
	UsedSize              uint64            `json:"used_size"`
	AvailableSize         uint64            `json:"available_size"`
	Utilization           float64           `json:"utilization"`
	StreamAllocations     map[uint64]uint64 `json:"stream_allocations"` // streamID -> allocated bytes
	IsBackpressureActive  bool              `json:"is_backpressure_active"`
	LastSlideTime         time.Time         `json:"last_slide_time"`
}

// BufferUsage represents the usage statistics of a stream buffer
type BufferUsage struct {
	TotalCapacity     uint64  `json:"total_capacity"`
	UsedCapacity      uint64  `json:"used_capacity"`
	AvailableCapacity uint64  `json:"available_capacity"`
	ContiguousBytes   uint64  `json:"contiguous_bytes"`
	GapCount          int     `json:"gap_count"`
	LargestGapSize    uint64  `json:"largest_gap_size"`
	ReadPosition      uint64  `json:"read_position"`
	WritePosition     uint64  `json:"write_position"`
	Utilization       float64 `json:"utilization"`
}

// BackpressureStatus represents the current backpressure state
type BackpressureStatus struct {
	GlobalActive   bool                             `json:"global_active"`
	GlobalReason   BackpressureReason               `json:"global_reason"`
	StreamStatus   map[uint64]StreamBackpressureInfo `json:"stream_status"`
	ActiveStreams  int                              `json:"active_streams"`
	TotalStreams   int                              `json:"total_streams"`
	LastActivation time.Time                        `json:"last_activation"`
}

// StreamBackpressureInfo contains backpressure information for a specific stream
type StreamBackpressureInfo struct {
	StreamID    uint64             `json:"stream_id"`
	Active      bool               `json:"active"`
	Reason      BackpressureReason `json:"reason"`
	Duration    time.Duration      `json:"duration"`
	ActivatedAt time.Time          `json:"activated_at"`
}

// StreamStats contains statistics for a specific stream
type StreamStats struct {
	StreamID         uint64        `json:"stream_id"`
	BytesWritten     uint64        `json:"bytes_written"`
	BytesRead        uint64        `json:"bytes_read"`
	WriteOperations  uint64        `json:"write_operations"`
	ReadOperations   uint64        `json:"read_operations"`
	GapEvents        uint64        `json:"gap_events"`
	BackpressureEvents uint64      `json:"backpressure_events"`
	AverageLatency   time.Duration `json:"average_latency"`
	LastActivity     time.Time     `json:"last_activity"`
}

// GlobalPresentationStats contains global statistics for the presentation system
type GlobalPresentationStats struct {
	ActiveStreams        int           `json:"active_streams"`
	TotalBytesWritten    uint64        `json:"total_bytes_written"`
	TotalBytesRead       uint64        `json:"total_bytes_read"`
	WindowUtilization    float64       `json:"window_utilization"`
	BackpressureEvents   uint64        `json:"backpressure_events"`
	AverageLatency       time.Duration `json:"average_latency"`
	LastUpdate           time.Time     `json:"last_update"`
	TotalWriteOperations uint64        `json:"total_write_operations"`
	TotalReadOperations  uint64        `json:"total_read_operations"`
	ErrorCount           uint64        `json:"error_count"`
	TimeoutCount         uint64        `json:"timeout_count"`
}

// BackpressureStats contains statistics about backpressure events
type BackpressureStats struct {
	TotalActivations     uint64                       `json:"total_activations"`
	ActiveStreams        int                          `json:"active_streams"`
	GlobalActivations    uint64                       `json:"global_activations"`
	ReasonBreakdown      map[BackpressureReason]uint64 `json:"reason_breakdown"`
	AverageDuration      time.Duration                `json:"average_duration"`
	LastActivation       time.Time                    `json:"last_activation"`
	CurrentlyActive      bool                         `json:"currently_active"`
}

// Enumerations

// StreamType defines the type of stream
type StreamType int

const (
	StreamTypeData     StreamType = iota // Regular data stream
	StreamTypeControl                    // Control stream
	StreamTypeMetadata                   // Metadata stream
)

func (st StreamType) String() string {
	switch st {
	case StreamTypeData:
		return "DATA"
	case StreamTypeControl:
		return "CONTROL"
	case StreamTypeMetadata:
		return "METADATA"
	default:
		return "UNKNOWN"
	}
}

// StreamPriority defines the priority level of a stream
type StreamPriority int

const (
	StreamPriorityLow      StreamPriority = iota // Low priority
	StreamPriorityNormal                         // Normal priority
	StreamPriorityHigh                           // High priority
	StreamPriorityCritical                       // Critical priority
)

func (sp StreamPriority) String() string {
	switch sp {
	case StreamPriorityLow:
		return "LOW"
	case StreamPriorityNormal:
		return "NORMAL"
	case StreamPriorityHigh:
		return "HIGH"
	case StreamPriorityCritical:
		return "CRITICAL"
	default:
		return "UNKNOWN"
	}
}

// DataFlags defines flags for data chunks
type DataFlags uint32

const (
	DataFlagNone        DataFlags = 0      // No special flags
	DataFlagEndOfStream DataFlags = 1 << 0 // End of stream marker
	DataFlagUrgent      DataFlags = 1 << 1 // Urgent data
	DataFlagCompressed  DataFlags = 1 << 2 // Compressed data
)

func (df DataFlags) String() string {
	var flags []string
	if df&DataFlagEndOfStream != 0 {
		flags = append(flags, "END_OF_STREAM")
	}
	if df&DataFlagUrgent != 0 {
		flags = append(flags, "URGENT")
	}
	if df&DataFlagCompressed != 0 {
		flags = append(flags, "COMPRESSED")
	}
	if len(flags) == 0 {
		return "NONE"
	}
	return fmt.Sprintf("[%s]", strings.Join(flags, ","))
}

// BackpressureReason defines the reason for backpressure activation
type BackpressureReason int

const (
	BackpressureReasonNone           BackpressureReason = iota // No backpressure
	BackpressureReasonWindowFull                               // Receive window is full
	BackpressureReasonBufferFull                               // Stream buffer is full
	BackpressureReasonSlowConsumer                             // Consumer is too slow
	BackpressureReasonMemoryPressure                           // System memory pressure
)

func (br BackpressureReason) String() string {
	switch br {
	case BackpressureReasonNone:
		return "NONE"
	case BackpressureReasonWindowFull:
		return "WINDOW_FULL"
	case BackpressureReasonBufferFull:
		return "BUFFER_FULL"
	case BackpressureReasonSlowConsumer:
		return "SLOW_CONSUMER"
	case BackpressureReasonMemoryPressure:
		return "MEMORY_PRESSURE"
	default:
		return "UNKNOWN"
	}
}

// BackpressureCallback is called when backpressure state changes
type BackpressureCallback func(streamID uint64, active bool, reason BackpressureReason)