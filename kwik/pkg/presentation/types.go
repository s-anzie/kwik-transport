package presentation

import (
	"errors"
	"fmt"
	"time"
)

// Error definitions for the presentation package
var (
	ErrBufferClosed    = errors.New("buffer is closed")
	ErrBufferOverflow  = errors.New("buffer overflow")
	ErrInvalidOffset   = errors.New("invalid offset")
	ErrStreamNotFound  = errors.New("stream not found")
	ErrWindowExhausted = errors.New("receive window exhausted")
	ErrBackpressure    = errors.New("backpressure active")
	ErrTimeout         = errors.New("operation timeout")
	ErrInvalidData     = errors.New("invalid data")
	ErrGapTimeout      = errors.New("gap resolution timeout")
	ErrSystemShutdown  = errors.New("system is shutting down")
)

// Constants for the presentation package
const (
	DefaultReadTimeoutShort = 100 * time.Millisecond
)

// PresentationConfig holds configuration for the data presentation system
type PresentationConfig struct {
	// Receive window configuration
	ReceiveWindowSize     uint64  `json:"receive_window_size"`    // Total receive window size (default: 4MB)
	BackpressureThreshold float64 `json:"backpressure_threshold"` // Threshold for backpressure activation (default: 0.8)
	WindowSlideSize       uint64  `json:"window_slide_size"`      // Size of window slide operations (default: 64KB)

	// Stream buffer configuration
	DefaultStreamBufferSize     uint64        `json:"default_stream_buffer_size"`    // Default buffer size per stream (default: 1MB)
	MaxStreamBufferSize         uint64        `json:"max_stream_buffer_size"`        // Maximum buffer size per stream (default: 2MB)
	StreamBackpressureThreshold float64       `json:"stream_backpressure_threshold"` // Per-stream backpressure threshold (default: 0.75)
	GapTimeout                  time.Duration `json:"gap_timeout"`                   // Timeout for gap resolution (default: 100ms)

	// Performance configuration
	ParallelWorkers     int           `json:"parallel_workers"`      // Number of parallel workers
	BatchProcessingSize int           `json:"batch_processing_size"` // Batch size for processing
	CleanupInterval     time.Duration `json:"cleanup_interval"`      // Interval for automatic cleanup

	// Memory management
	EnableMemoryPooling bool   `json:"enable_memory_pooling"` // Enable memory pooling
	PoolBlockSize       uint64 `json:"pool_block_size"`       // Size of memory pool blocks
	MaxPoolSize         uint64 `json:"max_pool_size"`         // Maximum pool size

	// Monitoring and debugging
	EnableDetailedMetrics bool          `json:"enable_detailed_metrics"` // Enable detailed metrics collection
	MetricsUpdateInterval time.Duration `json:"metrics_update_interval"` // Metrics update interval
	EnableDebugLogging    bool          `json:"enable_debug_logging"`    // Enable debug logging
}

// DefaultPresentationConfig returns the default configuration
func DefaultPresentationConfig() *PresentationConfig {
	return &PresentationConfig{
		// Receive window defaults (4MB total)
		ReceiveWindowSize:     4 * 1024 * 1024, // 4MB
		BackpressureThreshold: 0.8,             // 80%
		WindowSlideSize:       64 * 1024,       // 64KB

		// Stream buffer defaults (1MB per stream)
		DefaultStreamBufferSize:     1024 * 1024,     // 1MB
		MaxStreamBufferSize:         2 * 1024 * 1024, // 2MB
		StreamBackpressureThreshold: 0.75,            // 75%
		GapTimeout:                  100 * time.Millisecond,

		// Performance defaults
		ParallelWorkers:     4,
		BatchProcessingSize: 32,
		CleanupInterval:     30 * time.Second,

		// Memory management defaults
		EnableMemoryPooling: true,
		PoolBlockSize:       4096,             // 4KB blocks
		MaxPoolSize:         16 * 1024 * 1024, // 16MB pool

		// Monitoring defaults
		EnableDetailedMetrics: true,
		MetricsUpdateInterval: 1 * time.Second,
		EnableDebugLogging:    false,
	}
}

// Validate validates the configuration and returns an error if invalid
func (c *PresentationConfig) Validate() error {
	if c.ReceiveWindowSize == 0 {
		return fmt.Errorf("receive window size cannot be zero")
	}
	if c.BackpressureThreshold <= 0 || c.BackpressureThreshold > 1 {
		return fmt.Errorf("backpressure threshold must be between 0 and 1")
	}
	if c.WindowSlideSize == 0 || c.WindowSlideSize > c.ReceiveWindowSize {
		return fmt.Errorf("window slide size must be positive and less than receive window size")
	}
	if c.DefaultStreamBufferSize == 0 {
		return fmt.Errorf("default stream buffer size cannot be zero")
	}
	if c.MaxStreamBufferSize < c.DefaultStreamBufferSize {
		return fmt.Errorf("max stream buffer size must be >= default stream buffer size")
	}
	if c.StreamBackpressureThreshold <= 0 || c.StreamBackpressureThreshold > 1 {
		return fmt.Errorf("stream backpressure threshold must be between 0 and 1")
	}
	if c.GapTimeout <= 0 {
		return fmt.Errorf("gap timeout must be positive")
	}
	if c.ParallelWorkers <= 0 {
		return fmt.Errorf("parallel workers must be positive")
	}
	if c.BatchProcessingSize <= 0 {
		return fmt.Errorf("batch processing size must be positive")
	}
	return nil
}

// StreamBufferConfig holds configuration specific to a stream buffer
type StreamBufferConfig struct {
	StreamID              uint64         `json:"stream_id"`
	BufferSize            uint64         `json:"buffer_size"`
	Priority              StreamPriority `json:"priority"`
	GapTimeout            time.Duration  `json:"gap_timeout"`
	EnableMetrics         bool           `json:"enable_metrics"`
	EnableImmediateCleanup bool          `json:"enable_immediate_cleanup"`
}

// MemoryPoolStats contains statistics about memory pool usage
type MemoryPoolStats struct {
	TotalBlocks     int       `json:"total_blocks"`
	UsedBlocks      int       `json:"used_blocks"`
	AvailableBlocks int       `json:"available_blocks"`
	Utilization     float64   `json:"utilization"`
	TotalAllocated  uint64    `json:"total_allocated"`
	TotalReleased   uint64    `json:"total_released"`
	LastUpdate      time.Time `json:"last_update"`
}

// PerformanceMetrics contains performance-related metrics
type PerformanceMetrics struct {
	// Throughput metrics
	BytesPerSecond      uint64 `json:"bytes_per_second"`
	OperationsPerSecond uint64 `json:"operations_per_second"`

	// Latency metrics
	AverageLatency time.Duration `json:"average_latency"`
	P95Latency     time.Duration `json:"p95_latency"`
	P99Latency     time.Duration `json:"p99_latency"`

	// Error metrics
	ErrorRate   float64 `json:"error_rate"`
	TimeoutRate float64 `json:"timeout_rate"`

	// Resource utilization
	CPUUtilization    float64 `json:"cpu_utilization"`
	MemoryUtilization float64 `json:"memory_utilization"`

	LastUpdate time.Time `json:"last_update"`
}

// DetailedStreamStats contains detailed statistics for a stream
type DetailedStreamStats struct {
	// Basic stats (embedded from StreamStats)
	StreamStats

	// Detailed metrics
	WriteLatencyHistogram map[time.Duration]uint64 `json:"write_latency_histogram"`
	ReadLatencyHistogram  map[time.Duration]uint64 `json:"read_latency_histogram"`
	GapSizeHistogram      map[uint64]uint64        `json:"gap_size_histogram"`

	// Buffer metrics
	BufferUtilizationHistory []float64 `json:"buffer_utilization_history"`
	MaxBufferUtilization     float64   `json:"max_buffer_utilization"`
	MinBufferUtilization     float64   `json:"min_buffer_utilization"`

	// Backpressure metrics
	BackpressureDuration  time.Duration `json:"backpressure_duration"`
	BackpressureFrequency float64       `json:"backpressure_frequency"`

	// Data quality metrics
	OutOfOrderPackets uint64 `json:"out_of_order_packets"`
	DuplicatePackets  uint64 `json:"duplicate_packets"`
	CorruptedPackets  uint64 `json:"corrupted_packets"`
}

// SystemHealthMetrics contains overall system health indicators
type SystemHealthMetrics struct {
	// Overall health score (0-100)
	HealthScore int `json:"health_score"`

	// Component health
	WindowManagerHealth int `json:"window_manager_health"`
	BufferManagerHealth int `json:"buffer_manager_health"`
	BackpressureHealth  int `json:"backpressure_health"`

	// Resource health
	MemoryHealth int `json:"memory_health"`
	CPUHealth    int `json:"cpu_health"`

	// Operational metrics
	UptimeSeconds   uint64    `json:"uptime_seconds"`
	LastHealthCheck time.Time `json:"last_health_check"`

	// Alerts and warnings
	ActiveAlerts []string `json:"active_alerts"`
	WarningCount int      `json:"warning_count"`
	ErrorCount   int      `json:"error_count"`
}

// ConfigurationUpdate represents a configuration change request
type ConfigurationUpdate struct {
	// What to update
	UpdateType ConfigUpdateType `json:"update_type"`

	// New values
	ReceiveWindowSize     *uint64        `json:"receive_window_size,omitempty"`
	BackpressureThreshold *float64       `json:"backpressure_threshold,omitempty"`
	StreamBufferSize      *uint64        `json:"stream_buffer_size,omitempty"`
	GapTimeout            *time.Duration `json:"gap_timeout,omitempty"`

	// Target (for stream-specific updates)
	TargetStreamID *uint64 `json:"target_stream_id,omitempty"`

	// Metadata
	RequestedBy string    `json:"requested_by"`
	RequestedAt time.Time `json:"requested_at"`
	Reason      string    `json:"reason"`
}

// ConfigUpdateType defines the type of configuration update
type ConfigUpdateType int

const (
	ConfigUpdateGlobal       ConfigUpdateType = iota // Global configuration update
	ConfigUpdateStream                               // Stream-specific configuration update
	ConfigUpdateWindow                               // Window-specific configuration update
	ConfigUpdateBackpressure                         // Backpressure-specific configuration update
)

func (cut ConfigUpdateType) String() string {
	switch cut {
	case ConfigUpdateGlobal:
		return "GLOBAL"
	case ConfigUpdateStream:
		return "STREAM"
	case ConfigUpdateWindow:
		return "WINDOW"
	case ConfigUpdateBackpressure:
		return "BACKPRESSURE"
	default:
		return "UNKNOWN"
	}
}

// DiagnosticInfo contains diagnostic information for troubleshooting
type DiagnosticInfo struct {
	// System state
	SystemState         string   `json:"system_state"`
	ActiveStreams       []uint64 `json:"active_streams"`
	BackpressureStreams []uint64 `json:"backpressure_streams"`

	// Resource usage
	MemoryUsage uint64            `json:"memory_usage"`
	WindowUsage uint64            `json:"window_usage"`
	BufferUsage map[uint64]uint64 `json:"buffer_usage"` // streamID -> usage

	// Recent events
	RecentErrors       []DiagnosticEvent `json:"recent_errors"`
	RecentWarnings     []DiagnosticEvent `json:"recent_warnings"`
	RecentBackpressure []DiagnosticEvent `json:"recent_backpressure"`

	// Performance indicators
	PerformanceMetrics PerformanceMetrics `json:"performance_metrics"`

	// Configuration
	CurrentConfig *PresentationConfig `json:"current_config"`

	GeneratedAt time.Time `json:"generated_at"`
}

// DiagnosticEvent represents a diagnostic event (error, warning, etc.)
type DiagnosticEvent struct {
	Timestamp time.Time              `json:"timestamp"`
	Level     string                 `json:"level"`     // ERROR, WARNING, INFO
	Component string                 `json:"component"` // Which component generated the event
	Message   string                 `json:"message"`
	StreamID  *uint64                `json:"stream_id,omitempty"`
	Details   map[string]interface{} `json:"details,omitempty"`
}

// Constants for default values and limits
const (
	// Size constants
	DefaultReceiveWindowSize = 4 * 1024 * 1024  // 4MB
	DefaultStreamBufferSize  = 1024 * 1024      // 1MB
	MaxStreamBufferSize      = 16 * 1024 * 1024 // 16MB
	MinStreamBufferSize      = 64 * 1024        // 64KB

	// Threshold constants
	DefaultBackpressureThreshold       = 0.8  // 80%
	DefaultStreamBackpressureThreshold = 0.75 // 75%
	MinBackpressureThreshold           = 0.1  // 10%
	MaxBackpressureThreshold           = 0.95 // 95%

	// Timeout constants
	DefaultGapTimeout  = 100 * time.Millisecond
	MinGapTimeout      = 10 * time.Millisecond
	MaxGapTimeout      = 10 * time.Second
	DefaultReadTimeout = 5 * time.Second

	// Performance constants
	DefaultParallelWorkers = 4
	DefaultBatchSize       = 32
	DefaultCleanupInterval = 30 * time.Second
	DefaultMetricsInterval = 1 * time.Second

	// Memory pool constants
	DefaultPoolBlockSize = 4096             // 4KB
	DefaultMaxPoolSize   = 16 * 1024 * 1024 // 16MB

	// Health check constants
	HealthCheckInterval = 10 * time.Second
	HealthScoreMax      = 100
	HealthScoreMin      = 0
)

// Additional types for advanced gap management and buffer operations

// GapInfo contains information about a gap in the buffer
type GapInfo struct {
	StartOffset uint64        `json:"start_offset"`
	EndOffset   uint64        `json:"end_offset"`
	Size        uint64        `json:"size"`
	Duration    time.Duration `json:"duration"` // How long this gap has existed
}

// DataRangeChunk represents a chunk of data or gap in a range query
type DataRangeChunk struct {
	StartOffset uint64 `json:"start_offset"`
	EndOffset   uint64 `json:"end_offset"`
	Data        []byte `json:"data,omitempty"` // nil for gaps
	IsGap       bool   `json:"is_gap"`
}

// ContiguousRange represents a contiguous range of data in the buffer
type ContiguousRange struct {
	StartOffset uint64 `json:"start_offset"`
	EndOffset   uint64 `json:"end_offset"`
	Size        uint64 `json:"size"`
}

// ReadPositionInfo contains detailed information about the read position
type ReadPositionInfo struct {
	CurrentPosition  uint64    `json:"current_position"`
	WritePosition    uint64    `json:"write_position"`
	AvailableBytes   uint64    `json:"available_bytes"`
	NextDataPosition uint64    `json:"next_data_position"`
	HasGapAtPosition bool      `json:"has_gap_at_position"`
	ContiguousBytes  uint64    `json:"contiguous_bytes"`
	LastUpdate       time.Time `json:"last_update"`
}

// CleanupStats contains statistics about cleanup operations
type CleanupStats struct {
	TotalChunks        int       `json:"total_chunks"`
	CleanableChunks    int       `json:"cleanable_chunks"`
	CleanableBytes     uint64    `json:"cleanable_bytes"`
	UsedBytes          uint64    `json:"used_bytes"`
	TotalBytes         uint64    `json:"total_bytes"`
	ReadPosition       uint64    `json:"read_position"`
	WritePosition      uint64    `json:"write_position"`
	FragmentationRatio float64   `json:"fragmentation_ratio"`
	LastCleanup        time.Time `json:"last_cleanup"`
}

// Additional types for window sliding mechanisms

// SlideInfo contains information about window sliding
type SlideInfo struct {
	CurrentPosition  uint64        `json:"current_position"`
	LastSlideTime    time.Time     `json:"last_slide_time"`
	TotalSlides      uint64        `json:"total_slides"`
	TotalBytesSlid   uint64        `json:"total_bytes_slid"`
	AutoSlideEnabled bool          `json:"auto_slide_enabled"`
	SlideInterval    time.Duration `json:"slide_interval"`
	SlideSize        uint64        `json:"slide_size"`
}

// SlideConfig contains configuration for sliding operations
type SlideConfig struct {
	SlideSize         uint64        `json:"slide_size"`
	AutoSlideEnabled  bool          `json:"auto_slide_enabled"`
	AutoSlideInterval time.Duration `json:"auto_slide_interval"`
}

// SlidingMetrics contains metrics about sliding performance
type SlidingMetrics struct {
	TotalSlides        uint64        `json:"total_slides"`
	TotalBytesSlid     uint64        `json:"total_bytes_slid"`
	AverageSlideSize   uint64        `json:"average_slide_size"`
	LastSlideTime      time.Time     `json:"last_slide_time"`
	TimeSinceLastSlide time.Duration `json:"time_since_last_slide"`
	CurrentPosition    uint64        `json:"current_position"`
	SlidesPerSecond    float64       `json:"slides_per_second"`
}

// Additional types for advanced backpressure management

// GlobalBackpressureInfo contains detailed information about global backpressure
type GlobalBackpressureInfo struct {
	Active          bool               `json:"active"`
	Reason          BackpressureReason `json:"reason"`
	ActivatedAt     time.Time          `json:"activated_at"`
	Duration        time.Duration      `json:"duration"`
	AffectedStreams []uint64           `json:"affected_streams"`
	StreamCount     int                `json:"stream_count"`
}

// GlobalBackpressureHistory contains historical information about global backpressure
type GlobalBackpressureHistory struct {
	TotalActivations uint64                        `json:"total_activations"`
	LastActivation   time.Time                     `json:"last_activation"`
	AverageDuration  time.Duration                 `json:"average_duration"`
	CurrentlyActive  bool                          `json:"currently_active"`
	ReasonBreakdown  map[BackpressureReason]uint64 `json:"reason_breakdown"`
}

// BackpressureImpact represents the impact assessment of current backpressure
type BackpressureImpact struct {
	Level            BackpressureImpactLevel    `json:"level"`
	TotalStreams     int                        `json:"total_streams"`
	ActiveStreams    int                        `json:"active_streams"`
	ImpactRatio      float64                    `json:"impact_ratio"`
	ImpactedByReason map[BackpressureReason]int `json:"impacted_by_reason"`
	GlobalActive     bool                       `json:"global_active"`
	Timestamp        time.Time                  `json:"timestamp"`
}

// BackpressureImpactLevel defines the severity level of backpressure impact
type BackpressureImpactLevel int

const (
	BackpressureImpactNone     BackpressureImpactLevel = iota // No impact
	BackpressureImpactLow                                     // Low impact (< 25% streams)
	BackpressureImpactMedium                                  // Medium impact (25-50% streams)
	BackpressureImpactHigh                                    // High impact (50-75% streams)
	BackpressureImpactCritical                                // Critical impact (> 75% streams)
)

func (bil BackpressureImpactLevel) String() string {
	switch bil {
	case BackpressureImpactNone:
		return "NONE"
	case BackpressureImpactLow:
		return "LOW"
	case BackpressureImpactMedium:
		return "MEDIUM"
	case BackpressureImpactHigh:
		return "HIGH"
	case BackpressureImpactCritical:
		return "CRITICAL"
	default:
		return "UNKNOWN"
	}
}

// Additional types for advanced routing and data writing

// StreamWrite represents a write operation to a stream
type StreamWrite struct {
	StreamID uint64        `json:"stream_id"`
	Data     []byte        `json:"data"`
	Offset   uint64        `json:"offset"`
	Metadata *DataMetadata `json:"metadata,omitempty"`
}

// DataRoutingInfo contains information for routing data to streams
type DataRoutingInfo struct {
	StreamID   uint64         `json:"stream_id"`
	Data       []byte         `json:"data"`
	Offset     uint64         `json:"offset"`
	Priority   StreamPriority `json:"priority"`
	SourcePath string         `json:"source_path"`
	Flags      DataFlags      `json:"flags"`
}

// StreamRoutingInfo contains routing information for a stream
type StreamRoutingInfo struct {
	StreamID           uint64         `json:"stream_id"`
	BufferUtilization  float64        `json:"buffer_utilization"`
	Priority           StreamPriority `json:"priority"`
	BackpressureActive bool           `json:"backpressure_active"`
	WindowAllocation   uint64         `json:"window_allocation"`
	LastActivity       time.Time      `json:"last_activity"`
}

// RoutingMetrics contains metrics about data routing performance
type RoutingMetrics struct {
	TotalStreams       int           `json:"total_streams"`
	ActiveStreams      int           `json:"active_streams"`
	TotalThroughput    uint64        `json:"total_throughput"`
	AverageLatency     time.Duration `json:"average_latency"`
	WindowUtilization  float64       `json:"window_utilization"`
	BackpressureEvents uint64        `json:"backpressure_events"`
	ErrorRate          float64       `json:"error_rate"`
	LastUpdate         time.Time     `json:"last_update"`
}
