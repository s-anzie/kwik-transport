package stream

import (
	"fmt"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
)

// SecondaryStreamHandler manages streams opened by secondary servers
// These streams are isolated from the public client session and handled internally
type SecondaryStreamHandler interface {
	// Gestion des streams secondaires
	HandleSecondaryStream(pathID string, stream quic.Stream) (uint64, error)
	CloseSecondaryStream(pathID string, streamID uint64) error
	
	// Mapping vers streams KWIK
	MapToKwikStream(secondaryStreamID uint64, kwikStreamID uint64, offset uint64) error
	UnmapSecondaryStream(secondaryStreamID uint64) error
	
	// Statistiques
	GetSecondaryStreamStats(pathID string) (*SecondaryStreamStats, error)
	GetActiveMappings() map[uint64]uint64 // secondaryStreamID -> kwikStreamID
}

// SecondaryStreamStats provides statistics about secondary streams for a specific path
type SecondaryStreamStats struct {
	ActiveStreams    int     // Number of currently active secondary streams
	TotalDataBytes   uint64  // Total bytes transferred through secondary streams
	MappedStreams    int     // Number of streams currently mapped to KWIK streams
	UnmappedStreams  int     // Number of streams not yet mapped
	AggregationRatio float64 // Ratio of aggregated data vs raw data
}

// SecondaryStreamConfig contains configuration for secondary stream management
type SecondaryStreamConfig struct {
	MaxStreamsPerPath    int           // Maximum number of secondary streams per path
	StreamTimeout        time.Duration // Timeout for inactive streams
	BufferSize           int           // Buffer size for stream data
	MetadataTimeout      time.Duration // Timeout for metadata operations
	AggregationBatchSize int           // Batch size for aggregation operations
}

// SecondaryStreamInfo contains information about a secondary stream
type SecondaryStreamInfo struct {
	StreamID         uint64                // Unique identifier for the secondary stream
	PathID           string                // Path identifier for the stream
	QuicStream       quic.Stream           // Underlying QUIC stream
	KwikStreamID     uint64                // Target KWIK stream ID (0 if not mapped)
	CurrentOffset    uint64                // Current offset in the target KWIK stream
	State            SecondaryStreamState  // Current state of the stream
	CreatedAt        time.Time             // When the stream was created
	LastActivity     time.Time             // Last activity timestamp
	BytesReceived    uint64                // Total bytes received on this stream
	BytesTransferred uint64                // Total bytes transferred to KWIK stream
}

// SecondaryStreamState represents the state of a secondary stream
type SecondaryStreamState int

const (
	SecondaryStreamStateOpening SecondaryStreamState = iota // Stream is being opened
	SecondaryStreamStateActive                              // Stream is active and processing data
	SecondaryStreamStateClosing                             // Stream is being closed
	SecondaryStreamStateClosed                              // Stream is closed
	SecondaryStreamStateError                               // Stream encountered an error
)

// String returns a string representation of the secondary stream state
func (s SecondaryStreamState) String() string {
	switch s {
	case SecondaryStreamStateOpening:
		return "OPENING"
	case SecondaryStreamStateActive:
		return "ACTIVE"
	case SecondaryStreamStateClosing:
		return "CLOSING"
	case SecondaryStreamStateClosed:
		return "CLOSED"
	case SecondaryStreamStateError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// SecondaryStreamHandlerImpl is the concrete implementation of SecondaryStreamHandler
type SecondaryStreamHandlerImpl struct {
	// Streams secondaires actifs par path
	activeStreams map[string]map[uint64]*SecondaryStreamInfo // pathID -> streamID -> info
	
	// Configuration
	config *SecondaryStreamConfig
	
	// Metrics
	metrics *SecondaryStreamMetrics
	
	// Synchronisation
	mutex sync.RWMutex
	
	// Stream ID counter for generating unique IDs
	nextStreamID uint64
	streamIDMutex sync.Mutex
}

// NewSecondaryStreamHandler creates a new secondary stream handler with the given configuration
func NewSecondaryStreamHandler(config *SecondaryStreamConfig) *SecondaryStreamHandlerImpl {
	if config == nil {
		config = &SecondaryStreamConfig{
			MaxStreamsPerPath:    100,
			StreamTimeout:        30 * time.Second,
			BufferSize:           64 * 1024, // 64KB
			MetadataTimeout:      5 * time.Second,
			AggregationBatchSize: 10,
		}
	}
	
	return &SecondaryStreamHandlerImpl{
		activeStreams: make(map[string]map[uint64]*SecondaryStreamInfo),
		config:        config,
		metrics:       NewSecondaryStreamMetrics(),
		nextStreamID:  1,
	}
}

// HandleSecondaryStream handles a new secondary stream from the specified path
func (h *SecondaryStreamHandlerImpl) HandleSecondaryStream(pathID string, stream quic.Stream) (uint64, error) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	
	// Check if we have too many streams for this path
	if pathStreams, exists := h.activeStreams[pathID]; exists {
		if len(pathStreams) >= h.config.MaxStreamsPerPath {
			return 0, &SecondaryStreamError{
				Code:    ErrSecondaryStreamOverflow,
				Message: "maximum streams per path exceeded",
				PathID:  pathID,
			}
		}
	} else {
		h.activeStreams[pathID] = make(map[uint64]*SecondaryStreamInfo)
	}
	
	// Generate unique stream ID
	streamID := h.generateStreamID()
	
	// Create stream info
	streamInfo := &SecondaryStreamInfo{
		StreamID:         streamID,
		PathID:           pathID,
		QuicStream:       stream,
		KwikStreamID:     0, // Not mapped yet
		CurrentOffset:    0,
		State:            SecondaryStreamStateOpening,
		CreatedAt:        time.Now(),
		LastActivity:     time.Now(),
		BytesReceived:    0,
		BytesTransferred: 0,
	}
	
	// Store the stream
	h.activeStreams[pathID][streamID] = streamInfo
	
	// Transition to active state
	streamInfo.State = SecondaryStreamStateActive
	
	// Record metrics
	h.metrics.RecordStreamCreation(true) // true for secondary stream
	h.updateStreamCountMetrics()
	
	return streamID, nil
}

// CloseSecondaryStream closes a secondary stream
func (h *SecondaryStreamHandlerImpl) CloseSecondaryStream(pathID string, streamID uint64) error {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	
	pathStreams, exists := h.activeStreams[pathID]
	if !exists {
		return &SecondaryStreamError{
			Code:     ErrSecondaryStreamNotFound,
			Message:  "path not found",
			PathID:   pathID,
			StreamID: streamID,
		}
	}
	
	streamInfo, exists := pathStreams[streamID]
	if !exists {
		return &SecondaryStreamError{
			Code:     ErrSecondaryStreamNotFound,
			Message:  "stream not found",
			PathID:   pathID,
			StreamID: streamID,
		}
	}
	
	// Update state
	streamInfo.State = SecondaryStreamStateClosing
	
	// Close the underlying QUIC stream
	if streamInfo.QuicStream != nil {
		streamInfo.QuicStream.Close()
	}
	
	// Update state to closed
	streamInfo.State = SecondaryStreamStateClosed
	
	// Remove from active streams
	delete(pathStreams, streamID)
	
	// Clean up empty path map
	if len(pathStreams) == 0 {
		delete(h.activeStreams, pathID)
	}
	
	// Record metrics
	h.metrics.RecordStreamClosure(true) // true for secondary stream
	h.updateStreamCountMetrics()
	
	return nil
}

// MapToKwikStream maps a secondary stream to a KWIK stream
func (h *SecondaryStreamHandlerImpl) MapToKwikStream(secondaryStreamID uint64, kwikStreamID uint64, offset uint64) error {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	
	// Find the stream
	var streamInfo *SecondaryStreamInfo
	var found bool
	
	for _, pathStreams := range h.activeStreams {
		if info, exists := pathStreams[secondaryStreamID]; exists {
			streamInfo = info
			found = true
			break
		}
	}
	
	if !found {
		return &SecondaryStreamError{
			Code:     ErrSecondaryStreamNotFound,
			Message:  "secondary stream not found",
			StreamID: secondaryStreamID,
		}
	}
	
	// Record mapping latency
	startTime := time.Now()
	
	// Update mapping
	streamInfo.KwikStreamID = kwikStreamID
	streamInfo.CurrentOffset = offset
	streamInfo.LastActivity = time.Now()
	
	// Record metrics
	h.metrics.RecordMappingLatency(time.Since(startTime))
	
	return nil
}

// UnmapSecondaryStream removes the mapping for a secondary stream
func (h *SecondaryStreamHandlerImpl) UnmapSecondaryStream(secondaryStreamID uint64) error {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	
	// Find the stream
	var streamInfo *SecondaryStreamInfo
	var found bool
	
	for _, pathStreams := range h.activeStreams {
		if info, exists := pathStreams[secondaryStreamID]; exists {
			streamInfo = info
			found = true
			break
		}
	}
	
	if !found {
		return &SecondaryStreamError{
			Code:     ErrSecondaryStreamNotFound,
			Message:  "secondary stream not found",
			StreamID: secondaryStreamID,
		}
	}
	
	// Remove mapping
	streamInfo.KwikStreamID = 0
	streamInfo.CurrentOffset = 0
	streamInfo.LastActivity = time.Now()
	
	return nil
}

// GetSecondaryStreamStats returns statistics for secondary streams on the specified path
func (h *SecondaryStreamHandlerImpl) GetSecondaryStreamStats(pathID string) (*SecondaryStreamStats, error) {
	h.mutex.RLock()
	defer h.mutex.RUnlock()
	
	pathStreams, exists := h.activeStreams[pathID]
	if !exists {
		return &SecondaryStreamStats{
			ActiveStreams:    0,
			TotalDataBytes:   0,
			MappedStreams:    0,
			UnmappedStreams:  0,
			AggregationRatio: 0.0,
		}, nil
	}
	
	stats := &SecondaryStreamStats{
		ActiveStreams:   len(pathStreams),
		TotalDataBytes:  0,
		MappedStreams:   0,
		UnmappedStreams: 0,
	}
	
	var totalReceived, totalTransferred uint64
	
	for _, streamInfo := range pathStreams {
		stats.TotalDataBytes += streamInfo.BytesReceived
		totalReceived += streamInfo.BytesReceived
		totalTransferred += streamInfo.BytesTransferred
		
		if streamInfo.KwikStreamID != 0 {
			stats.MappedStreams++
		} else {
			stats.UnmappedStreams++
		}
	}
	
	// Calculate aggregation ratio
	if totalReceived > 0 {
		stats.AggregationRatio = float64(totalTransferred) / float64(totalReceived)
	}
	
	return stats, nil
}

// GetActiveMappings returns a map of active secondary stream to KWIK stream mappings
func (h *SecondaryStreamHandlerImpl) GetActiveMappings() map[uint64]uint64 {
	h.mutex.RLock()
	defer h.mutex.RUnlock()
	
	mappings := make(map[uint64]uint64)
	
	for _, pathStreams := range h.activeStreams {
		for streamID, streamInfo := range pathStreams {
			if streamInfo.KwikStreamID != 0 {
				mappings[streamID] = streamInfo.KwikStreamID
			}
		}
	}
	
	return mappings
}

// generateStreamID generates a unique stream ID
func (h *SecondaryStreamHandlerImpl) generateStreamID() uint64 {
	h.streamIDMutex.Lock()
	defer h.streamIDMutex.Unlock()
	
	id := h.nextStreamID
	h.nextStreamID++
	return id
}

// updateStreamCountMetrics updates the stream count metrics
func (h *SecondaryStreamHandlerImpl) updateStreamCountMetrics() {
	var totalSecondary, totalKwik int
	kwikStreams := make(map[uint64]bool)
	
	for _, pathStreams := range h.activeStreams {
		totalSecondary += len(pathStreams)
		for _, streamInfo := range pathStreams {
			if streamInfo.KwikStreamID != 0 {
				kwikStreams[streamInfo.KwikStreamID] = true
			}
		}
	}
	
	totalKwik = len(kwikStreams)
	h.metrics.UpdateStreamCounts(totalSecondary, totalKwik)
}

// GetMetrics returns the metrics instance for external monitoring
func (h *SecondaryStreamHandlerImpl) GetMetrics() *SecondaryStreamMetrics {
	return h.metrics
}

// RecordDataTransfer records data transfer metrics
func (h *SecondaryStreamHandlerImpl) RecordDataTransfer(streamID uint64, bytes uint64) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	
	// Find the stream and update its metrics
	for _, pathStreams := range h.activeStreams {
		if streamInfo, exists := pathStreams[streamID]; exists {
			streamInfo.BytesReceived += bytes
			streamInfo.LastActivity = time.Now()
			
			// Record throughput metrics
			h.metrics.RecordThroughput(bytes, 1) // 1 frame
			break
		}
	}
}

// SecondaryStreamError represents an error related to secondary stream operations
type SecondaryStreamError struct {
	Code     string
	Message  string
	PathID   string
	StreamID uint64
}

func (e *SecondaryStreamError) Error() string {
	if e.PathID != "" && e.StreamID != 0 {
		return fmt.Sprintf("%s: %s (path: %s, stream: %d)", e.Code, e.Message, e.PathID, e.StreamID)
	} else if e.PathID != "" {
		return fmt.Sprintf("%s: %s (path: %s)", e.Code, e.Message, e.PathID)
	} else if e.StreamID != 0 {
		return fmt.Sprintf("%s: %s (stream: %d)", e.Code, e.Message, e.StreamID)
	}
	return e.Code + ": " + e.Message
}

// Error codes for secondary stream operations
const (
	ErrSecondaryStreamOverflow  = "KWIK_SECONDARY_STREAM_OVERFLOW"
	ErrSecondaryStreamNotFound  = "KWIK_SECONDARY_STREAM_NOT_FOUND"
	ErrSecondaryStreamInvalid   = "KWIK_SECONDARY_STREAM_INVALID"
	ErrSecondaryStreamMapping   = "KWIK_SECONDARY_STREAM_MAPPING"
	ErrSecondaryStreamTimeout   = "KWIK_SECONDARY_STREAM_TIMEOUT"
)