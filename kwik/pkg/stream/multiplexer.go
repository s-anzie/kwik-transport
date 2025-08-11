package stream

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"
	"kwik/internal/utils"
)

// KwikStreamMultiplexer manages logical KWIK streams over real QUIC streams
// Implements Requirements 7.3, 7.4, 7.5
type KwikStreamMultiplexer struct {
	// Real QUIC streams management
	realStreams     map[uint64]*RealStreamInfo
	realStreamPool  map[string][]*RealStreamInfo // Pool per path
	realStreamCount uint64
	
	// Logical streams mapping
	logicalStreams  map[uint64]*LogicalStreamMapping
	logicalCounter  uint64
	
	// Configuration
	config *MultiplexerConfig
	
	// Path management
	pathValidator PathValidator
	
	// Synchronization
	mutex sync.RWMutex
	
	// Context for cancellation
	ctx    context.Context
	cancel context.CancelFunc
}

// RealStreamInfo contains information about a real QUIC stream
type RealStreamInfo struct {
	ID               uint64
	QuicStream       quic.Stream
	PathID           string
	LogicalStreams   map[uint64]bool // Set of logical stream IDs using this real stream
	CreatedAt        time.Time
	LastActivity     time.Time
	BytesTransmitted uint64
	
	// Stream state
	mutex  sync.RWMutex
	closed bool
}

// LogicalStreamMapping maps logical streams to real streams
type LogicalStreamMapping struct {
	LogicalStreamID uint64
	RealStreamID    uint64
	PathID          string
	Offset          uint64
	CreatedAt       time.Time
}

// MultiplexerConfig contains configuration for stream multiplexing
type MultiplexerConfig struct {
	// Optimal ratios (Requirements 7.3, 7.4)
	OptimalLogicalPerReal    int           // 3-4 logical streams per real stream
	MaxLogicalPerReal        int           // Maximum before creating new real stream
	MinLogicalPerReal        int           // Minimum to maintain efficiency
	
	// Stream management
	RealStreamIdleTimeout    time.Duration // When to close unused real streams
	RealStreamPoolSize       int           // Max real streams to keep in pool per path
	
	// Performance tuning
	StreamCreationTimeout    time.Duration
	StreamAllocationRetries  int
}

// NewKwikStreamMultiplexer creates a new stream multiplexer
func NewKwikStreamMultiplexer(pathValidator PathValidator, config *MultiplexerConfig) *KwikStreamMultiplexer {
	if config == nil {
		config = DefaultMultiplexerConfig()
	}
	
	ctx, cancel := context.WithCancel(context.Background())
	
	return &KwikStreamMultiplexer{
		realStreams:     make(map[uint64]*RealStreamInfo),
		realStreamPool:  make(map[string][]*RealStreamInfo),
		logicalStreams:  make(map[uint64]*LogicalStreamMapping),
		realStreamCount: 0,
		logicalCounter:  0,
		config:          config,
		pathValidator:   pathValidator,
		ctx:             ctx,
		cancel:          cancel,
	}
}

// DefaultMultiplexerConfig returns default multiplexer configuration
func DefaultMultiplexerConfig() *MultiplexerConfig {
	return &MultiplexerConfig{
		OptimalLogicalPerReal:    4,  // Target 4 logical streams per real stream
		MaxLogicalPerReal:        6,  // Create new real stream after 6 logical streams
		MinLogicalPerReal:        1,  // Keep real stream if it has at least 1 logical stream
		RealStreamIdleTimeout:    2 * time.Minute,
		RealStreamPoolSize:       10, // Keep up to 10 real streams per path
		StreamCreationTimeout:    30 * time.Second,
		StreamAllocationRetries:  3,
	}
}

// CreateLogicalStream creates a new logical stream and assigns it to an optimal real stream
// Implements Requirement 7.3: 3-4 logical streams per real QUIC stream
func (sm *KwikStreamMultiplexer) CreateLogicalStream(pathID string) (*LogicalStream, error) {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()
	
	// Validate path
	err := sm.pathValidator.ValidatePathForStreamCreation(pathID)
	if err != nil {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"path validation failed", err)
	}
	
	// Generate logical stream ID
	logicalStreamID := atomic.AddUint64(&sm.logicalCounter, 1)
	
	// Find or create optimal real stream for this logical stream
	realStreamInfo, err := sm.findOrCreateOptimalRealStream(pathID)
	if err != nil {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			"failed to allocate real stream", err)
	}
	
	// Create logical stream mapping
	mapping := &LogicalStreamMapping{
		LogicalStreamID: logicalStreamID,
		RealStreamID:    realStreamInfo.ID,
		PathID:          pathID,
		Offset:          0,
		CreatedAt:       time.Now(),
	}
	
	// Store mapping
	sm.logicalStreams[logicalStreamID] = mapping
	
	// Add logical stream to real stream's tracking
	realStreamInfo.mutex.Lock()
	realStreamInfo.LogicalStreams[logicalStreamID] = true
	realStreamInfo.LastActivity = time.Now()
	realStreamInfo.mutex.Unlock()
	
	// Create logical stream object
	logicalStream := &LogicalStream{
		ID:           logicalStreamID,
		PathID:       pathID,
		RealStreamID: realStreamInfo.ID,
		Offset:       0,
		Closed:       false,
	}
	
	return logicalStream, nil
}

// findOrCreateOptimalRealStream finds an existing real stream with optimal load or creates a new one
// Implements Requirement 7.4: automatic scaling based on demand
func (sm *KwikStreamMultiplexer) findOrCreateOptimalRealStream(pathID string) (*RealStreamInfo, error) {
	// First, try to find an existing real stream with optimal load
	for _, realStreamInfo := range sm.realStreams {
		if realStreamInfo.PathID != pathID || realStreamInfo.closed {
			continue
		}
		
		realStreamInfo.mutex.RLock()
		logicalCount := len(realStreamInfo.LogicalStreams)
		realStreamInfo.mutex.RUnlock()
		
		// Check if this real stream has optimal load (less than optimal ratio)
		if logicalCount < sm.config.OptimalLogicalPerReal {
			return realStreamInfo, nil
		}
	}
	
	// If no optimal real stream found, try to find one below max capacity
	for _, realStreamInfo := range sm.realStreams {
		if realStreamInfo.PathID != pathID || realStreamInfo.closed {
			continue
		}
		
		realStreamInfo.mutex.RLock()
		logicalCount := len(realStreamInfo.LogicalStreams)
		realStreamInfo.mutex.RUnlock()
		
		// Check if this real stream is below max capacity
		if logicalCount < sm.config.MaxLogicalPerReal {
			return realStreamInfo, nil
		}
	}
	
	// No suitable existing real stream found, create a new one
	return sm.createNewRealStream(pathID)
}

// createNewRealStream creates a new real QUIC stream
// Implements Requirement 7.5: automatic scaling of real QUIC streams
func (sm *KwikStreamMultiplexer) createNewRealStream(pathID string) (*RealStreamInfo, error) {
	// Try to get a real stream from the pool first
	if pooledStream := sm.getFromPool(pathID); pooledStream != nil {
		return pooledStream, nil
	}
	
	// Create new real QUIC stream
	// Note: In a real implementation, this would use the QUIC connection for the path
	// For now, we'll create a placeholder that would be replaced with actual QUIC stream
	realStreamID := atomic.AddUint64(&sm.realStreamCount, 1)
	
	realStreamInfo := &RealStreamInfo{
		ID:               realStreamID,
		QuicStream:       nil, // Would be actual QUIC stream in real implementation
		PathID:           pathID,
		LogicalStreams:   make(map[uint64]bool),
		CreatedAt:        time.Now(),
		LastActivity:     time.Now(),
		BytesTransmitted: 0,
		closed:           false,
	}
	
	// Store real stream info
	sm.realStreams[realStreamID] = realStreamInfo
	
	return realStreamInfo, nil
}

// getFromPool retrieves a real stream from the pool for reuse
func (sm *KwikStreamMultiplexer) getFromPool(pathID string) *RealStreamInfo {
	pool, exists := sm.realStreamPool[pathID]
	if !exists || len(pool) == 0 {
		return nil
	}
	
	// Get the most recently used stream from the pool
	streamInfo := pool[len(pool)-1]
	sm.realStreamPool[pathID] = pool[:len(pool)-1]
	
	// Reset stream for reuse
	streamInfo.mutex.Lock()
	streamInfo.LogicalStreams = make(map[uint64]bool)
	streamInfo.LastActivity = time.Now()
	streamInfo.closed = false
	streamInfo.mutex.Unlock()
	
	// Add back to active streams
	sm.realStreams[streamInfo.ID] = streamInfo
	
	return streamInfo
}

// GetLogicalStream retrieves a logical stream by ID
func (sm *KwikStreamMultiplexer) GetLogicalStream(streamID uint64) (*LogicalStream, error) {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()
	
	mapping, exists := sm.logicalStreams[streamID]
	if !exists {
		return nil, utils.NewKwikError(utils.ErrStreamCreationFailed,
			fmt.Sprintf("logical stream %d not found", streamID), nil)
	}
	
	// Update activity on the real stream
	if realStreamInfo, exists := sm.realStreams[mapping.RealStreamID]; exists {
		realStreamInfo.mutex.Lock()
		realStreamInfo.LastActivity = time.Now()
		realStreamInfo.mutex.Unlock()
	}
	
	logicalStream := &LogicalStream{
		ID:           mapping.LogicalStreamID,
		PathID:       mapping.PathID,
		RealStreamID: mapping.RealStreamID,
		Offset:       mapping.Offset,
		Closed:       false,
	}
	
	return logicalStream, nil
}

// CloseLogicalStream closes a logical stream and optimizes real stream usage
func (sm *KwikStreamMultiplexer) CloseLogicalStream(streamID uint64) error {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()
	
	mapping, exists := sm.logicalStreams[streamID]
	if !exists {
		return utils.NewKwikError(utils.ErrStreamCreationFailed,
			fmt.Sprintf("logical stream %d not found", streamID), nil)
	}
	
	// Remove logical stream mapping
	delete(sm.logicalStreams, streamID)
	
	// Update real stream tracking
	if realStreamInfo, exists := sm.realStreams[mapping.RealStreamID]; exists {
		realStreamInfo.mutex.Lock()
		delete(realStreamInfo.LogicalStreams, streamID)
		logicalCount := len(realStreamInfo.LogicalStreams)
		realStreamInfo.mutex.Unlock()
		
		// If real stream has no more logical streams, consider pooling or closing it
		if logicalCount == 0 {
			sm.handleEmptyRealStream(realStreamInfo)
		}
	}
	
	return nil
}

// handleEmptyRealStream handles a real stream that has no more logical streams
func (sm *KwikStreamMultiplexer) handleEmptyRealStream(realStreamInfo *RealStreamInfo) {
	// Remove from active streams
	delete(sm.realStreams, realStreamInfo.ID)
	
	// Add to pool if pool has space
	pathPool := sm.realStreamPool[realStreamInfo.PathID]
	if len(pathPool) < sm.config.RealStreamPoolSize {
		sm.realStreamPool[realStreamInfo.PathID] = append(pathPool, realStreamInfo)
	} else {
		// Pool is full, close the real stream
		sm.closeRealStream(realStreamInfo)
	}
}

// closeRealStream closes a real QUIC stream
func (sm *KwikStreamMultiplexer) closeRealStream(realStreamInfo *RealStreamInfo) {
	realStreamInfo.mutex.Lock()
	defer realStreamInfo.mutex.Unlock()
	
	if realStreamInfo.closed {
		return
	}
	
	// Close the actual QUIC stream
	if realStreamInfo.QuicStream != nil {
		realStreamInfo.QuicStream.Close()
	}
	
	realStreamInfo.closed = true
}

// GetOptimalRatio returns the optimal ratio of logical to real streams
// Implements interface requirement
func (sm *KwikStreamMultiplexer) GetOptimalRatio() int {
	return sm.config.OptimalLogicalPerReal
}

// GetMultiplexingStats returns statistics about stream multiplexing
func (sm *KwikStreamMultiplexer) GetMultiplexingStats() *MultiplexingStats {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()
	
	stats := &MultiplexingStats{
		ActiveRealStreams:    len(sm.realStreams),
		ActiveLogicalStreams: len(sm.logicalStreams),
		PooledRealStreams:    0,
		OptimalRatio:         sm.config.OptimalLogicalPerReal,
	}
	
	// Count pooled streams
	for _, pool := range sm.realStreamPool {
		stats.PooledRealStreams += len(pool)
	}
	
	// Calculate efficiency metrics
	if stats.ActiveRealStreams > 0 {
		stats.AverageLogicalPerReal = float64(stats.ActiveLogicalStreams) / float64(stats.ActiveRealStreams)
		stats.EfficiencyRatio = stats.AverageLogicalPerReal / float64(sm.config.OptimalLogicalPerReal)
	}
	
	return stats
}

// MultiplexingStats contains statistics about stream multiplexing
type MultiplexingStats struct {
	ActiveRealStreams       int
	ActiveLogicalStreams    int
	PooledRealStreams       int
	OptimalRatio            int
	AverageLogicalPerReal   float64
	EfficiencyRatio         float64
}

// StartCleanupRoutine starts background cleanup of idle real streams
func (sm *KwikStreamMultiplexer) StartCleanupRoutine() {
	go func() {
		ticker := time.NewTicker(sm.config.RealStreamIdleTimeout / 2)
		defer ticker.Stop()
		
		for {
			select {
			case <-sm.ctx.Done():
				return
			case <-ticker.C:
				sm.cleanupIdleRealStreams()
			}
		}
	}()
}

// cleanupIdleRealStreams removes real streams that have been idle for too long
func (sm *KwikStreamMultiplexer) cleanupIdleRealStreams() {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()
	
	now := time.Now()
	idleThreshold := now.Add(-sm.config.RealStreamIdleTimeout)
	
	// Clean up idle real streams
	for streamID, realStreamInfo := range sm.realStreams {
		realStreamInfo.mutex.RLock()
		lastActivity := realStreamInfo.LastActivity
		logicalCount := len(realStreamInfo.LogicalStreams)
		realStreamInfo.mutex.RUnlock()
		
		// Close real streams that are idle and have no logical streams
		if logicalCount == 0 && lastActivity.Before(idleThreshold) {
			delete(sm.realStreams, streamID)
			sm.closeRealStream(realStreamInfo)
		}
	}
	
	// Clean up idle pooled streams
	for pathID, pool := range sm.realStreamPool {
		activePool := make([]*RealStreamInfo, 0, len(pool))
		
		for _, realStreamInfo := range pool {
			realStreamInfo.mutex.RLock()
			lastActivity := realStreamInfo.LastActivity
			realStreamInfo.mutex.RUnlock()
			
			if lastActivity.After(idleThreshold) {
				activePool = append(activePool, realStreamInfo)
			} else {
				sm.closeRealStream(realStreamInfo)
			}
		}
		
		sm.realStreamPool[pathID] = activePool
	}
}

// Close shuts down the stream multiplexer
func (sm *KwikStreamMultiplexer) Close() error {
	sm.cancel()
	
	sm.mutex.Lock()
	defer sm.mutex.Unlock()
	
	// Close all active real streams
	for _, realStreamInfo := range sm.realStreams {
		sm.closeRealStream(realStreamInfo)
	}
	
	// Close all pooled real streams
	for _, pool := range sm.realStreamPool {
		for _, realStreamInfo := range pool {
			sm.closeRealStream(realStreamInfo)
		}
	}
	
	// Clear all maps
	sm.realStreams = make(map[uint64]*RealStreamInfo)
	sm.realStreamPool = make(map[string][]*RealStreamInfo)
	sm.logicalStreams = make(map[uint64]*LogicalStreamMapping)
	
	return nil
}