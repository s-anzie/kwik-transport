package stream

import (
	"fmt"
	"sync"
	"time"

	"kwik/internal/utils"
	"kwik/pkg/protocol"
)

// FrameRouter routes frames based on logical stream identifiers
// Implements Requirements 8.1, 8.2
type FrameRouter struct {
	// Stream routing table
	streamRoutes map[uint64]*StreamRoute

	// Frame handlers
	frameHandlers map[protocol.FrameType]FrameHandler

	// Statistics
	stats *FrameRoutingStats

	// Synchronization
	mutex sync.RWMutex

	// Configuration
	config *FrameRouterConfig
}

// StreamRoute contains routing information for a logical stream
type StreamRoute struct {
	LogicalStreamID uint64
	PathID          string
	RealStreamID    uint64
	Handler         LogicalStreamHandler
	CreatedAt       time.Time
	LastActivity    time.Time
	FrameCount      uint64
}

// LogicalStreamHandler handles frames for a specific logical stream
type LogicalStreamHandler interface {
	HandleFrame(frame protocol.Frame) error
	GetStreamID() uint64
	IsActive() bool
}

// FrameHandler handles specific frame types
type FrameHandler interface {
	HandleFrame(frame protocol.Frame, route *StreamRoute) error
	CanHandle(frameType protocol.FrameType) bool
}

// FrameRouterConfig contains configuration for frame routing
type FrameRouterConfig struct {
	// Routing settings
	MaxRoutes    int
	RouteTimeout time.Duration

	// Performance settings
	FrameBufferSize int
	BatchProcessing bool
	BatchSize       int

	// Statistics settings
	EnableStats         bool
	StatsUpdateInterval time.Duration
}

// FrameRoutingStats contains statistics about frame routing
type FrameRoutingStats struct {
	TotalFramesRouted  uint64
	FramesPerSecond    float64
	ActiveRoutes       int
	RoutingErrors      uint64
	AverageRoutingTime time.Duration

	// Per-frame-type statistics
	FrameTypeStats map[protocol.FrameType]*FrameTypeStats

	// Synchronization
	mutex sync.RWMutex
}

// FrameTypeStats contains statistics for a specific frame type
type FrameTypeStats struct {
	Count         uint64
	Errors        uint64
	AverageSize   float64
	LastProcessed time.Time
}

// NewFrameRouter creates a new frame router
func NewFrameRouter(config *FrameRouterConfig) *FrameRouter {
	if config == nil {
		config = DefaultFrameRouterConfig()
	}

	router := &FrameRouter{
		streamRoutes:  make(map[uint64]*StreamRoute),
		frameHandlers: make(map[protocol.FrameType]FrameHandler),
		config:        config,
	}

	if config.EnableStats {
		router.stats = &FrameRoutingStats{
			FrameTypeStats: make(map[protocol.FrameType]*FrameTypeStats),
		}
	}

	// Register default frame handlers
	router.registerDefaultHandlers()

	return router
}

// DefaultFrameRouterConfig returns default configuration
func DefaultFrameRouterConfig() *FrameRouterConfig {
	return &FrameRouterConfig{
		MaxRoutes:           10000,
		RouteTimeout:        5 * time.Minute,
		FrameBufferSize:     1000,
		BatchProcessing:     true,
		BatchSize:           10,
		EnableStats:         true,
		StatsUpdateInterval: 1 * time.Second,
	}
}

// RegisterStreamRoute registers a routing entry for a logical stream
// Implements Requirement 8.1: frame routing based on logical stream identifiers
func (fr *FrameRouter) RegisterStreamRoute(logicalStreamID uint64, pathID string, realStreamID uint64, handler LogicalStreamHandler) error {
	fr.mutex.Lock()
	defer fr.mutex.Unlock()

	// Check if route already exists
	if _, exists := fr.streamRoutes[logicalStreamID]; exists {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			fmt.Sprintf("route for logical stream %d already exists", logicalStreamID), nil)
	}

	// Check route limit
	if len(fr.streamRoutes) >= fr.config.MaxRoutes {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			"maximum number of routes reached", nil)
	}

	// Create new route
	route := &StreamRoute{
		LogicalStreamID: logicalStreamID,
		PathID:          pathID,
		RealStreamID:    realStreamID,
		Handler:         handler,
		CreatedAt:       time.Now(),
		LastActivity:    time.Now(),
		FrameCount:      0,
	}

	fr.streamRoutes[logicalStreamID] = route

	// Update statistics
	if fr.stats != nil {
		fr.stats.mutex.Lock()
		fr.stats.ActiveRoutes = len(fr.streamRoutes)
		fr.stats.mutex.Unlock()
	}

	return nil
}

// UnregisterStreamRoute removes a routing entry for a logical stream
func (fr *FrameRouter) UnregisterStreamRoute(logicalStreamID uint64) error {
	fr.mutex.Lock()
	defer fr.mutex.Unlock()

	if _, exists := fr.streamRoutes[logicalStreamID]; !exists {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			fmt.Sprintf("route for logical stream %d not found", logicalStreamID), nil)
	}

	delete(fr.streamRoutes, logicalStreamID)

	// Update statistics
	if fr.stats != nil {
		fr.stats.mutex.Lock()
		fr.stats.ActiveRoutes = len(fr.streamRoutes)
		fr.stats.mutex.Unlock()
	}

	return nil
}

// RouteFrame routes a frame to the appropriate logical stream handler
// Implements Requirement 8.2: routing logic based on logical stream identifiers in frames
func (fr *FrameRouter) RouteFrame(frame protocol.Frame) error {
	startTime := time.Now()

	// Extract logical stream ID from frame
	logicalStreamID, err := fr.extractLogicalStreamID(frame)
	if err != nil {
		fr.updateErrorStats(frame.Type())
		return utils.NewKwikError(utils.ErrInvalidFrame,
			"failed to extract logical stream ID from frame", err)
	}

	// Find route for logical stream
	fr.mutex.RLock()
	route, exists := fr.streamRoutes[logicalStreamID]
	fr.mutex.RUnlock()

	if !exists {
		fr.updateErrorStats(frame.Type())
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			fmt.Sprintf("no route found for logical stream %d", logicalStreamID), nil)
	}

	// Update route activity
	route.LastActivity = time.Now()
	route.FrameCount++

	// Route frame to appropriate handler
	err = fr.routeFrameToHandler(frame, route)
	if err != nil {
		fr.updateErrorStats(frame.Type())
		return utils.NewKwikError(utils.ErrInvalidFrame,
			"failed to route frame to handler", err)
	}

	// Update statistics
	fr.updateRoutingStats(frame, time.Since(startTime))

	return nil
}

// extractLogicalStreamID extracts the logical stream ID from a frame
func (fr *FrameRouter) extractLogicalStreamID(frame protocol.Frame) (uint64, error) {
	switch f := frame.(type) {
	case *protocol.DataFrame:
		return f.LogicalStreamID, nil
	case *protocol.ControlFrame:
		// For control frames, we might need to parse the payload to get stream ID
		// For now, return 0 (control frames are typically not stream-specific)
		return 0, nil
	default:
		return 0, fmt.Errorf("unsupported frame type: %T", frame)
	}
}

// routeFrameToHandler routes a frame to the appropriate handler
func (fr *FrameRouter) routeFrameToHandler(frame protocol.Frame, route *StreamRoute) error {
	// First try the stream-specific handler
	if route.Handler != nil && route.Handler.IsActive() {
		return route.Handler.HandleFrame(frame)
	}

	// Fall back to frame type handler
	if handler, exists := fr.frameHandlers[frame.Type()]; exists {
		return handler.HandleFrame(frame, route)
	}

	return fmt.Errorf("no handler found for frame type %v", frame.Type())
}

// RegisterFrameHandler registers a handler for a specific frame type
func (fr *FrameRouter) RegisterFrameHandler(frameType protocol.FrameType, handler FrameHandler) {
	fr.mutex.Lock()
	defer fr.mutex.Unlock()

	fr.frameHandlers[frameType] = handler
}

// registerDefaultHandlers registers default frame handlers
func (fr *FrameRouter) registerDefaultHandlers() {
	// Register default data frame handler
	fr.RegisterFrameHandler(protocol.FrameTypeData, &DefaultDataFrameHandler{})

	// Register default control frame handlers
	fr.RegisterFrameHandler(protocol.FrameTypeStreamCreateNotification, &DefaultControlFrameHandler{})
}

// BatchRouteFrames routes multiple frames in a batch for better performance
func (fr *FrameRouter) BatchRouteFrames(frames []protocol.Frame) []error {
	if !fr.config.BatchProcessing {
		// Process frames individually
		errors := make([]error, len(frames))
		for i, frame := range frames {
			errors[i] = fr.RouteFrame(frame)
		}
		return errors
	}

	// Process frames in batches
	errors := make([]error, len(frames))
	batchSize := fr.config.BatchSize

	for i := 0; i < len(frames); i += batchSize {
		end := i + batchSize
		if end > len(frames) {
			end = len(frames)
		}

		// Process batch
		for j := i; j < end; j++ {
			errors[j] = fr.RouteFrame(frames[j])
		}
	}

	return errors
}

// GetStreamRoute returns the route for a logical stream
func (fr *FrameRouter) GetStreamRoute(logicalStreamID uint64) (*StreamRoute, error) {
	fr.mutex.RLock()
	defer fr.mutex.RUnlock()

	route, exists := fr.streamRoutes[logicalStreamID]
	if !exists {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			fmt.Sprintf("route for logical stream %d not found", logicalStreamID), nil)
	}

	return route, nil
}

// GetActiveRoutes returns all active stream routes
func (fr *FrameRouter) GetActiveRoutes() []*StreamRoute {
	fr.mutex.RLock()
	defer fr.mutex.RUnlock()

	routes := make([]*StreamRoute, 0, len(fr.streamRoutes))
	for _, route := range fr.streamRoutes {
		if route.Handler == nil || route.Handler.IsActive() {
			routes = append(routes, route)
		}
	}

	return routes
}

// CleanupExpiredRoutes removes routes that have been inactive for too long
func (fr *FrameRouter) CleanupExpiredRoutes() int {
	fr.mutex.Lock()
	defer fr.mutex.Unlock()

	now := time.Now()
	expiredThreshold := now.Add(-fr.config.RouteTimeout)

	var expiredRoutes []uint64
	for streamID, route := range fr.streamRoutes {
		if route.LastActivity.Before(expiredThreshold) {
			expiredRoutes = append(expiredRoutes, streamID)
		}
	}

	// Remove expired routes
	for _, streamID := range expiredRoutes {
		delete(fr.streamRoutes, streamID)
	}

	// Update statistics
	if fr.stats != nil {
		fr.stats.mutex.Lock()
		fr.stats.ActiveRoutes = len(fr.streamRoutes)
		fr.stats.mutex.Unlock()
	}

	return len(expiredRoutes)
}

// GetRoutingStats returns current routing statistics
func (fr *FrameRouter) GetRoutingStats() *FrameRoutingStats {
	if fr.stats == nil {
		return nil
	}

	fr.stats.mutex.RLock()
	defer fr.stats.mutex.RUnlock()

	// Create a copy of the stats
	statsCopy := &FrameRoutingStats{
		TotalFramesRouted:  fr.stats.TotalFramesRouted,
		FramesPerSecond:    fr.stats.FramesPerSecond,
		ActiveRoutes:       fr.stats.ActiveRoutes,
		RoutingErrors:      fr.stats.RoutingErrors,
		AverageRoutingTime: fr.stats.AverageRoutingTime,
		FrameTypeStats:     make(map[protocol.FrameType]*FrameTypeStats),
	}

	// Copy frame type stats
	for frameType, stats := range fr.stats.FrameTypeStats {
		statsCopy.FrameTypeStats[frameType] = &FrameTypeStats{
			Count:         stats.Count,
			Errors:        stats.Errors,
			AverageSize:   stats.AverageSize,
			LastProcessed: stats.LastProcessed,
		}
	}

	return statsCopy
}

// updateRoutingStats updates routing statistics
func (fr *FrameRouter) updateRoutingStats(frame protocol.Frame, routingTime time.Duration) {
	if fr.stats == nil {
		return
	}

	fr.stats.mutex.Lock()
	defer fr.stats.mutex.Unlock()

	fr.stats.TotalFramesRouted++

	// Update average routing time
	if fr.stats.AverageRoutingTime == 0 {
		fr.stats.AverageRoutingTime = routingTime
	} else {
		// Simple moving average
		fr.stats.AverageRoutingTime = (fr.stats.AverageRoutingTime + routingTime) / 2
	}

	// Update frame type stats
	frameType := frame.Type()
	if _, exists := fr.stats.FrameTypeStats[frameType]; !exists {
		fr.stats.FrameTypeStats[frameType] = &FrameTypeStats{}
	}

	typeStats := fr.stats.FrameTypeStats[frameType]
	typeStats.Count++
	typeStats.LastProcessed = time.Now()

	// Calculate frame size if possible
	if serialized, err := frame.Serialize(); err == nil {
		if typeStats.AverageSize == 0 {
			typeStats.AverageSize = float64(len(serialized))
		} else {
			typeStats.AverageSize = (typeStats.AverageSize + float64(len(serialized))) / 2
		}
	}
}

// updateErrorStats updates error statistics
func (fr *FrameRouter) updateErrorStats(frameType protocol.FrameType) {
	if fr.stats == nil {
		return
	}

	fr.stats.mutex.Lock()
	defer fr.stats.mutex.Unlock()

	fr.stats.RoutingErrors++

	if _, exists := fr.stats.FrameTypeStats[frameType]; !exists {
		fr.stats.FrameTypeStats[frameType] = &FrameTypeStats{}
	}

	fr.stats.FrameTypeStats[frameType].Errors++
}

// Default frame handlers

// DefaultDataFrameHandler handles data frames
type DefaultDataFrameHandler struct{}

func (h *DefaultDataFrameHandler) HandleFrame(frame protocol.Frame, route *StreamRoute) error {
	dataFrame, ok := frame.(*protocol.DataFrame)
	if !ok {
		return fmt.Errorf("expected DataFrame, got %T", frame)
	}

	// Basic data frame handling - in a real implementation, this would
	// forward the frame to the appropriate stream handler
	_ = dataFrame // Use the frame to avoid unused variable error

	return nil
}

func (h *DefaultDataFrameHandler) CanHandle(frameType protocol.FrameType) bool {
	return frameType == protocol.FrameTypeData
}

// DefaultControlFrameHandler handles control frames
type DefaultControlFrameHandler struct{}

func (h *DefaultControlFrameHandler) HandleFrame(frame protocol.Frame, route *StreamRoute) error {
	controlFrame, ok := frame.(*protocol.ControlFrame)
	if !ok {
		return fmt.Errorf("expected ControlFrame, got %T", frame)
	}

	// Basic control frame handling
	_ = controlFrame // Use the frame to avoid unused variable error

	return nil
}

func (h *DefaultControlFrameHandler) CanHandle(frameType protocol.FrameType) bool {
	return frameType == protocol.FrameTypeStreamCreateNotification ||
		frameType == protocol.FrameTypeAddPathRequest ||
		frameType == protocol.FrameTypeAddPathResponse
}
