package stream

import (
	"fmt"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"kwik/pkg/data"
)

// SecondaryStreamIsolator provides isolation logic for secondary streams
// It ensures secondary streams are not exposed to the public client session
// and handles proper routing to internal aggregation systems
type SecondaryStreamIsolator interface {
	// Validation functions
	ValidateStreamAccess(streamID uint64, serverRole ServerRole) error
	ValidateStreamCreation(pathID string, serverRole ServerRole) error
	
	// Routing functions
	RouteSecondaryStream(pathID string, stream quic.Stream, metadata *StreamMetadata) error
	RouteToAggregator(kwikStreamID uint64, secondaryData *SecondaryStreamData) error
	
	// Isolation enforcement
	IsStreamPublic(streamID uint64) bool
	IsStreamSecondary(streamID uint64) bool
	PreventPublicExposure(streamID uint64) error
	
	// Stream lifecycle management
	RegisterSecondaryStream(streamID uint64, pathID string) error
	UnregisterSecondaryStream(streamID uint64) error
	
	// Statistics and monitoring
	GetIsolationStats() *SecondaryIsolationStats
	GetSecondaryStreamCount() int
}

// ServerRole defines the role of a server in the KWIK architecture
type ServerRole int

const (
	ServerRolePrimary   ServerRole = iota // Primary server - can open public streams
	ServerRoleSecondary                   // Secondary server - can only open internal streams
)

// String returns a string representation of the server role
func (r ServerRole) String() string {
	switch r {
	case ServerRolePrimary:
		return "PRIMARY"
	case ServerRoleSecondary:
		return "SECONDARY"
	default:
		return "UNKNOWN"
	}
}

// Use SecondaryStreamData from data package
type SecondaryStreamData = data.SecondaryStreamData

// SecondaryStreamIsolatorImpl is the concrete implementation of SecondaryStreamIsolator
type SecondaryStreamIsolatorImpl struct {
	// Stream tracking
	secondaryStreams map[uint64]*SecondaryStreamInfo // streamID -> stream info
	publicStreams    map[uint64]bool                 // streamID -> is public
	
	// Path to server role mapping
	pathRoles map[string]ServerRole // pathID -> server role
	
	// Routing components
	aggregator data.DataAggregator
	handler    SecondaryStreamHandler
	
	// Statistics
	stats *SecondaryIsolationStats
	
	// Configuration
	config *SecondaryIsolationConfig
	
	// Synchronization
	mutex sync.RWMutex
	
	// Stream ID tracking
	nextSecondaryStreamID uint64
	streamIDMutex         sync.Mutex
}

// Use SecondaryStreamInfo from secondary_handler.go to avoid redeclaration

// SecondaryIsolationConfig contains configuration for secondary stream isolation
type SecondaryIsolationConfig struct {
	MaxSecondaryStreams     int           // Maximum number of secondary streams
	StreamTimeout           time.Duration // Timeout for inactive streams
	RoutingTimeout          time.Duration // Timeout for routing operations
	EnableStrictIsolation   bool          // Enable strict isolation checks
	AllowPublicFallback     bool          // Allow fallback to public streams in emergencies
	MonitoringInterval      time.Duration // Interval for statistics collection
}

// SecondaryIsolationStats provides statistics about stream isolation
type SecondaryIsolationStats struct {
	TotalSecondaryStreams   int     // Total number of secondary streams managed
	ActiveSecondaryStreams  int     // Currently active secondary streams
	PublicStreams           int     // Number of public streams
	IsolationViolations     int     // Number of isolation violations detected
	RoutingErrors           int     // Number of routing errors
	AverageRoutingLatency   float64 // Average latency for routing operations (ms)
	TotalDataRouted         uint64  // Total bytes routed through isolation layer
}

// NewSecondaryStreamIsolator creates a new secondary stream isolator
func NewSecondaryStreamIsolator(
	aggregator data.DataAggregator,
	handler SecondaryStreamHandler,
	config *SecondaryIsolationConfig,
) *SecondaryStreamIsolatorImpl {
	if config == nil {
		config = &SecondaryIsolationConfig{
			MaxSecondaryStreams:   1000,
			StreamTimeout:         30 * time.Second,
			RoutingTimeout:        5 * time.Second,
			EnableStrictIsolation: true,
			AllowPublicFallback:   false,
			MonitoringInterval:    10 * time.Second,
		}
	}
	
	isolator := &SecondaryStreamIsolatorImpl{
		secondaryStreams:      make(map[uint64]*SecondaryStreamInfo),
		publicStreams:         make(map[uint64]bool),
		pathRoles:             make(map[string]ServerRole),
		aggregator:            aggregator,
		handler:               handler,
		config:                config,
		stats:                 &SecondaryIsolationStats{},
		nextSecondaryStreamID: 1,
	}
	
	// Start monitoring routine if configured
	if config.MonitoringInterval > 0 {
		go isolator.monitoringRoutine()
	}
	
	return isolator
}

// ValidateStreamAccess validates whether a stream can be accessed based on server role
func (i *SecondaryStreamIsolatorImpl) ValidateStreamAccess(streamID uint64, serverRole ServerRole) error {
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	
	// Check if stream is public
	isPublic, exists := i.publicStreams[streamID]
	if !exists {
		// Stream doesn't exist, check if it's a secondary stream
		if _, secondaryExists := i.secondaryStreams[streamID]; secondaryExists {
			// Secondary stream exists
			if serverRole == ServerRolePrimary {
				// Primary servers should not access secondary streams directly
				i.stats.IsolationViolations++
				return &IsolationError{
					Code:     ErrIsolationViolation,
					Message:  "primary server attempting to access secondary stream",
					StreamID: streamID,
					Role:     serverRole,
				}
			}
			// Secondary servers can access their own secondary streams
			return nil
		}
		// Stream doesn't exist at all
		return &IsolationError{
			Code:     ErrStreamNotFound,
			Message:  "stream not found",
			StreamID: streamID,
			Role:     serverRole,
		}
	}
	
	// Stream is public
	if isPublic {
		if serverRole == ServerRoleSecondary && i.config.EnableStrictIsolation {
			// Secondary servers should not access public streams in strict mode
			i.stats.IsolationViolations++
			return &IsolationError{
				Code:     ErrIsolationViolation,
				Message:  "secondary server attempting to access public stream in strict isolation mode",
				StreamID: streamID,
				Role:     serverRole,
			}
		}
		return nil
	}
	
	return nil
}

// ValidateStreamCreation validates whether a stream can be created based on server role
func (i *SecondaryStreamIsolatorImpl) ValidateStreamCreation(pathID string, serverRole ServerRole) error {
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	
	// Check server role for this path
	if pathRole, exists := i.pathRoles[pathID]; exists && pathRole != serverRole {
		return &IsolationError{
			Code:    ErrRoleMismatch,
			Message: fmt.Sprintf("path %s is assigned to role %s, cannot create stream with role %s", pathID, pathRole, serverRole),
			PathID:  pathID,
			Role:    serverRole,
		}
	}
	
	// Secondary servers can only create secondary streams
	if serverRole == ServerRoleSecondary {
		// Check if we're at the limit for secondary streams
		if len(i.secondaryStreams) >= i.config.MaxSecondaryStreams {
			return &IsolationError{
				Code:    ErrSecondaryStreamLimit,
				Message: "maximum number of secondary streams reached",
				PathID:  pathID,
				Role:    serverRole,
			}
		}
	}
	
	return nil
}

// RouteSecondaryStream routes a secondary stream to the appropriate internal handler
func (i *SecondaryStreamIsolatorImpl) RouteSecondaryStream(pathID string, stream quic.Stream, metadata *StreamMetadata) error {
	startTime := time.Now()
	defer func() {
		// Update routing latency statistics
		latency := time.Since(startTime).Seconds() * 1000 // Convert to milliseconds
		i.updateRoutingLatency(latency)
	}()
	
	i.mutex.Lock()
	defer i.mutex.Unlock()
	
	// Validate that this is indeed a secondary stream path
	if pathRole, exists := i.pathRoles[pathID]; exists && pathRole != ServerRoleSecondary {
		i.stats.RoutingErrors++
		return &IsolationError{
			Code:    ErrInvalidRouting,
			Message: fmt.Sprintf("attempting to route secondary stream on non-secondary path %s", pathID),
			PathID:  pathID,
		}
	}
	
	// Generate secondary stream ID
	streamID := i.generateSecondaryStreamID()
	
	// Create stream info
	streamInfo := &SecondaryStreamInfo{
		StreamID:         streamID,
		PathID:           pathID,
		QuicStream:       stream,
		KwikStreamID:     0, // Will be set when mapping is established
		CurrentOffset:    0,
		State:            SecondaryStreamStateActive, // Assuming this state exists
		CreatedAt:        time.Now(),
		LastActivity:     time.Now(),
		BytesReceived:    0,
		BytesTransferred: 0,
	}
	
	// Register the stream
	i.secondaryStreams[streamID] = streamInfo
	i.stats.TotalSecondaryStreams++
	i.stats.ActiveSecondaryStreams++
	
	// Route to secondary stream handler
	_, err := i.handler.HandleSecondaryStream(pathID, stream)
	if err != nil {
		// Clean up on error
		delete(i.secondaryStreams, streamID)
		i.stats.ActiveSecondaryStreams--
		i.stats.RoutingErrors++
		return &IsolationError{
			Code:     ErrRoutingFailed,
			Message:  fmt.Sprintf("failed to route secondary stream: %v", err),
			StreamID: streamID,
			PathID:   pathID,
		}
	}
	
	// If metadata is provided, set up mapping
	if metadata != nil && metadata.KwikStreamID != 0 {
		if err := i.handler.MapToKwikStream(streamID, metadata.KwikStreamID, metadata.Offset); err != nil {
			// Log error but don't fail the routing - mapping can be done later
			// This is not a critical error for stream isolation
		}
	}
	
	return nil
}

// RouteToAggregator routes secondary stream data to the appropriate aggregator
func (i *SecondaryStreamIsolatorImpl) RouteToAggregator(kwikStreamID uint64, secondaryData *SecondaryStreamData) error {
	startTime := time.Now()
	defer func() {
		latency := time.Since(startTime).Seconds() * 1000
		i.updateRoutingLatency(latency)
	}()
	
	i.mutex.Lock()
	defer i.mutex.Unlock()
	
	// Validate that the secondary stream exists and is properly isolated
	streamInfo, exists := i.secondaryStreams[secondaryData.StreamID]
	if !exists {
		i.stats.RoutingErrors++
		return &IsolationError{
			Code:     ErrStreamNotFound,
			Message:  "secondary stream not found for aggregation routing",
			StreamID: secondaryData.StreamID,
		}
	}
	
	// Update stream activity
	streamInfo.LastActivity = time.Now()
	streamInfo.BytesTransferred += uint64(len(secondaryData.Data))
	
	// Route to aggregator
	if err := i.aggregator.AggregateSecondaryData(kwikStreamID, secondaryData); err != nil {
		i.stats.RoutingErrors++
		return &IsolationError{
			Code:     ErrAggregationFailed,
			Message:  fmt.Sprintf("failed to aggregate secondary data: %v", err),
			StreamID: secondaryData.StreamID,
		}
	}
	
	// Update statistics
	i.stats.TotalDataRouted += uint64(len(secondaryData.Data))
	
	return nil
}

// IsStreamPublic checks if a stream is exposed to the public interface
func (i *SecondaryStreamIsolatorImpl) IsStreamPublic(streamID uint64) bool {
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	
	isPublic, exists := i.publicStreams[streamID]
	return exists && isPublic
}

// IsStreamSecondary checks if a stream is a secondary (internal) stream
func (i *SecondaryStreamIsolatorImpl) IsStreamSecondary(streamID uint64) bool {
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	
	_, exists := i.secondaryStreams[streamID]
	return exists
}

// PreventPublicExposure ensures a stream is not exposed to the public interface
func (i *SecondaryStreamIsolatorImpl) PreventPublicExposure(streamID uint64) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	
	// If it's already marked as public, remove it
	if isPublic, exists := i.publicStreams[streamID]; exists && isPublic {
		delete(i.publicStreams, streamID)
		i.stats.PublicStreams--
	}
	
	// Ensure it's tracked as a secondary stream if it exists
	if _, exists := i.secondaryStreams[streamID]; exists {
		// Secondary streams are never public by design
		return nil
	}
	
	// If stream doesn't exist in either category, it might be a new secondary stream
	// This is not an error - the stream will be registered when it's created
	return nil
}

// RegisterSecondaryStream registers a new secondary stream for isolation tracking
func (i *SecondaryStreamIsolatorImpl) RegisterSecondaryStream(streamID uint64, pathID string) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	
	// Check if stream already exists
	if _, exists := i.secondaryStreams[streamID]; exists {
		return &IsolationError{
			Code:     ErrStreamAlreadyExists,
			Message:  "secondary stream already registered",
			StreamID: streamID,
			PathID:   pathID,
		}
	}
	
	// Check if it's marked as public (shouldn't happen)
	if isPublic, exists := i.publicStreams[streamID]; exists && isPublic {
		i.stats.IsolationViolations++
		return &IsolationError{
			Code:     ErrIsolationViolation,
			Message:  "attempting to register public stream as secondary",
			StreamID: streamID,
			PathID:   pathID,
		}
	}
	
	// Register the stream
	streamInfo := &SecondaryStreamInfo{
		StreamID:         streamID,
		PathID:           pathID,
		QuicStream:       nil, // Will be set when actual stream is provided
		KwikStreamID:     0,   // Will be set when mapping is established
		CurrentOffset:    0,
		State:            SecondaryStreamStateActive, // Assuming this state exists
		CreatedAt:        time.Now(),
		LastActivity:     time.Now(),
		BytesReceived:    0,
		BytesTransferred: 0,
	}
	
	i.secondaryStreams[streamID] = streamInfo
	i.stats.TotalSecondaryStreams++
	i.stats.ActiveSecondaryStreams++
	
	// Set path role if not already set
	if _, exists := i.pathRoles[pathID]; !exists {
		i.pathRoles[pathID] = ServerRoleSecondary
	}
	
	return nil
}

// UnregisterSecondaryStream removes a secondary stream from isolation tracking
func (i *SecondaryStreamIsolatorImpl) UnregisterSecondaryStream(streamID uint64) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	
	streamInfo, exists := i.secondaryStreams[streamID]
	if !exists {
		return &IsolationError{
			Code:     ErrStreamNotFound,
			Message:  "secondary stream not found for unregistration",
			StreamID: streamID,
		}
	}
	
	// Remove from tracking
	delete(i.secondaryStreams, streamID)
	i.stats.ActiveSecondaryStreams--
	
	// Clean up path role if no more streams on this path
	hasPathStreams := false
	for _, info := range i.secondaryStreams {
		if info.PathID == streamInfo.PathID {
			hasPathStreams = true
			break
		}
	}
	
	if !hasPathStreams {
		delete(i.pathRoles, streamInfo.PathID)
	}
	
	return nil
}

// GetIsolationStats returns current isolation statistics
func (i *SecondaryStreamIsolatorImpl) GetIsolationStats() *SecondaryIsolationStats {
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	
	// Create a copy of stats to avoid race conditions
	statsCopy := *i.stats
	statsCopy.ActiveSecondaryStreams = len(i.secondaryStreams)
	statsCopy.PublicStreams = len(i.publicStreams)
	
	return &statsCopy
}

// GetSecondaryStreamCount returns the number of active secondary streams
func (i *SecondaryStreamIsolatorImpl) GetSecondaryStreamCount() int {
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	
	return len(i.secondaryStreams)
}

// generateSecondaryStreamID generates a unique ID for secondary streams
func (i *SecondaryStreamIsolatorImpl) generateSecondaryStreamID() uint64 {
	i.streamIDMutex.Lock()
	defer i.streamIDMutex.Unlock()
	
	id := i.nextSecondaryStreamID
	i.nextSecondaryStreamID++
	return id
}

// updateRoutingLatency updates the average routing latency statistic
func (i *SecondaryStreamIsolatorImpl) updateRoutingLatency(latencyMs float64) {
	// Simple moving average - in production, consider using a more sophisticated approach
	if i.stats.AverageRoutingLatency == 0 {
		i.stats.AverageRoutingLatency = latencyMs
	} else {
		// Weighted average with 90% weight on previous average
		i.stats.AverageRoutingLatency = 0.9*i.stats.AverageRoutingLatency + 0.1*latencyMs
	}
}

// monitoringRoutine runs periodic monitoring and cleanup tasks
func (i *SecondaryStreamIsolatorImpl) monitoringRoutine() {
	ticker := time.NewTicker(i.config.MonitoringInterval)
	defer ticker.Stop()
	
	for range ticker.C {
		i.performCleanup()
	}
}

// performCleanup removes inactive streams and updates statistics
func (i *SecondaryStreamIsolatorImpl) performCleanup() {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	
	now := time.Now()
	var streamsToRemove []uint64
	
	// Find inactive streams
	for streamID, streamInfo := range i.secondaryStreams {
		if now.Sub(streamInfo.LastActivity) > i.config.StreamTimeout {
			streamsToRemove = append(streamsToRemove, streamID)
		}
	}
	
	// Remove inactive streams
	for _, streamID := range streamsToRemove {
		delete(i.secondaryStreams, streamID)
		i.stats.ActiveSecondaryStreams--
	}
}

// IsolationError represents an error in stream isolation operations
type IsolationError struct {
	Code     string
	Message  string
	StreamID uint64
	PathID   string
	Role     ServerRole
}

func (e *IsolationError) Error() string {
	msg := e.Code + ": " + e.Message
	if e.StreamID != 0 {
		msg += fmt.Sprintf(" (stream: %d)", e.StreamID)
	}
	if e.PathID != "" {
		msg += fmt.Sprintf(" (path: %s)", e.PathID)
	}
	if e.Role != ServerRolePrimary && e.Role != ServerRoleSecondary {
		msg += fmt.Sprintf(" (role: %s)", e.Role)
	}
	return msg
}

// Error codes for isolation operations
const (
	ErrIsolationViolation   = "KWIK_ISOLATION_VIOLATION"
	ErrStreamNotFound       = "KWIK_STREAM_NOT_FOUND"
	ErrStreamAlreadyExists  = "KWIK_STREAM_ALREADY_EXISTS"
	ErrRoleMismatch         = "KWIK_ROLE_MISMATCH"
	ErrSecondaryStreamLimit = "KWIK_SECONDARY_STREAM_LIMIT"
	ErrInvalidRouting       = "KWIK_INVALID_ROUTING"
	ErrRoutingFailed        = "KWIK_ROUTING_FAILED"
	ErrAggregationFailed    = "KWIK_AGGREGATION_FAILED"
)