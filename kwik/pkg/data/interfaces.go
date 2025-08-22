package data

import (
	"time"

	"kwik/proto/data"
)

// DataPlane manages data plane operations for application data transmission
type DataPlane interface {
	// Core data transmission
	SendData(pathID string, frame *data.DataFrame) error
	ReceiveData(pathID string) (*data.DataFrame, error)

	// Stream management
	CreateLogicalStream(streamID uint64, pathID string) error
	CloseLogicalStream(streamID uint64) error
	GetStreamState(streamID uint64) (*data.StreamState, error)

	// Flow control
	UpdateStreamFlowControl(streamID uint64, maxData uint64) error
	UpdateConnectionFlowControl(pathID string, maxData uint64) error

	// ACK management
	SendAck(pathID string, ackFrame *data.AckFrame) error
	ProcessAck(pathID string, ackFrame *data.AckFrame) error

	// Aggregation (for client-side multi-path data aggregation)
	EnableAggregation(streamID uint64) error
	DisableAggregation(streamID uint64) error
	GetAggregatedData(streamID uint64) ([]byte, error)

	// Statistics and monitoring
	GetPathStats(pathID string) (*data.PathDataStats, error)
	GetAggregatedStats() (*data.AggregatedDataStats, error)

	// Lifecycle
	RegisterPath(pathID string, stream DataStream) error
	UnregisterPath(pathID string) error
	Close() error
}

// DataStream represents a data stream for a specific path
type DataStream interface {
	Read(buffer []byte) (int, error)
	Write(data []byte) (int, error)
	Close() error
	PathID() string
	IsActive() bool
}

// DataAggregator handles aggregation of data from multiple paths
type DataAggregator interface {
	// Aggregation control
	AddPath(pathID string, stream DataStream) error
	RemovePath(pathID string) error

	// Data aggregation
	AggregateData(streamID uint64) ([]byte, error)
	WriteToPath(pathID string, data []byte) error

	// Stream management
	CreateAggregatedStream(streamID uint64) error
	CloseAggregatedStream(streamID uint64) error

	// Secondary stream aggregation (new methods)
	AggregateSecondaryData(kwikStreamID uint64, secondaryData *DataFrame) error
	SetStreamMapping(kwikStreamID uint64, secondaryStreamID uint64, pathID string) error
	RemoveStreamMapping(secondaryStreamID uint64) error

	// Offset validation and management
	ValidateOffset(kwikStreamID uint64, offset uint64, sourcePathID string) error
	GetNextExpectedOffset(kwikStreamID uint64) uint64

	// Statistics
	GetAggregationStats(streamID uint64) (*AggregationStats, error)
	GetSecondaryStreamStats() *SecondaryAggregationStats
	GetOffsetManagerStats() *OffsetManagerStats
}

// DataScheduler handles scheduling of data transmission across paths
type DataScheduler interface {
	// Scheduling
	ScheduleFrame(frame *data.DataFrame, availablePaths []string) (string, error)
	UpdatePathMetrics(pathID string, metrics *PathMetrics) error

	// Path management
	AddPath(pathID string, metrics *PathMetrics) error
	RemovePath(pathID string) error
	GetOptimalPath(streamID uint64) (string, error)

	// Load balancing
	EnableLoadBalancing(enable bool)
	SetLoadBalancingStrategy(strategy LoadBalancingStrategy)
}

// AggregationStats contains statistics for data aggregation
type AggregationStats struct {
	StreamID           uint64
	TotalBytesReceived uint64
	BytesPerPath       map[string]uint64
	FramesReceived     uint64
	FramesPerPath      map[string]uint64
	LastActivity       int64
	AggregationRatio   float64
}

// SecondaryStreamData represents data from a secondary stream (forward declaration)
// Full definition is in secondary_aggregator.go
type DataFrame struct {
	StreamID     uint64
	PathID       string
	Data         []byte
	Offset       uint64
	KwikStreamID uint64
	Timestamp    time.Time
	SequenceNum  uint64
}

// SecondaryAggregationStats contains statistics for secondary stream aggregation (forward declaration)
// Full definition is in secondary_aggregator.go
type SecondaryAggregationStats struct {
	ActiveSecondaryStreams int
	ActiveKwikStreams      int
	TotalBytesAggregated   uint64
	TotalDataFrames        uint64
	AggregationLatency     time.Duration
	ReorderingEvents       uint64
	DroppedFrames          uint64
	LastUpdate             time.Time
}

// OffsetManagerStats contains statistics for offset management (forward declaration)
// Full definition is in multi_source_offset_manager.go
type OffsetManagerStats struct {
	ActiveStreams     int
	TotalConflicts    uint64
	ResolvedConflicts uint64
	PendingDataBytes  uint64
	ReorderingEvents  uint64
	SyncRequests      uint64
	SyncFailures      uint64
	LastUpdate        time.Time
}

// PathMetrics contains performance metrics for a path
type PathMetrics struct {
	PathID         string
	RTT            int64   // Round-trip time in microseconds
	Bandwidth      uint64  // Estimated bandwidth in bytes/second
	PacketLoss     float64 // Packet loss rate (0.0 to 1.0)
	Congestion     float64 // Congestion level (0.0 to 1.0)
	LastUpdate     int64   // Timestamp of last update
	IsActive       bool
	QueueDepth     int
	ThroughputMbps float64
}

// LoadBalancingStrategy defines how data is distributed across paths
type LoadBalancingStrategy int

const (
	LoadBalancingRoundRobin LoadBalancingStrategy = iota
	LoadBalancingWeighted
	LoadBalancingLatencyBased
	LoadBalancingBandwidthBased
	LoadBalancingAdaptive
)

// FrameProcessor handles processing of individual data frames
type FrameProcessor interface {
	ProcessIncomingFrame(frame *data.DataFrame) error
	ProcessOutgoingFrame(frame *data.DataFrame) error
	ValidateFrame(frame *data.DataFrame) error
	SerializeFrame(frame *data.DataFrame) ([]byte, error)
	DeserializeFrame(data []byte) (*data.DataFrame, error)
}

// FlowController manages flow control for streams and connections
type FlowController interface {
	// Stream-level flow control
	UpdateStreamWindow(streamID uint64, windowSize uint64) error
	GetStreamWindow(streamID uint64) (uint64, error)
	IsStreamBlocked(streamID uint64) bool

	// Connection-level flow control
	UpdateConnectionWindow(pathID string, windowSize uint64) error
	GetConnectionWindow(pathID string) (uint64, error)
	IsConnectionBlocked(pathID string) bool

	// Window management
	ConsumeStreamWindow(streamID uint64, bytes uint64) error
	ConsumeConnectionWindow(pathID string, bytes uint64) error
	ExpandStreamWindow(streamID uint64, bytes uint64) error
	ExpandConnectionWindow(pathID string, bytes uint64) error
}
