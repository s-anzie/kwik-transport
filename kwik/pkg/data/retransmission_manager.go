package data

import (
	"fmt"
	"sync"
	"time"
	
	"kwik/internal/utils"
)

// RetransmissionManager manages segment retransmissions with intelligent backoff
type RetransmissionManager interface {
	// TrackSegment registers a segment for retransmission monitoring
	TrackSegment(segmentID string, data []byte, pathID string, timeout time.Duration) error
	
	// AckSegment marks a segment as acknowledged and stops retransmission
	AckSegment(segmentID string) error
	
	// SetBackoffStrategy configures the backoff strategy for retransmissions
	SetBackoffStrategy(strategy BackoffStrategy) error
	
	// GetStats returns current retransmission statistics
	GetStats() RetransmissionStats
	
	// Start begins the retransmission monitoring
	Start() error
	
	// Stop stops the retransmission monitoring and cleans up resources
	Stop() error
	
	// RegisterPath registers a path for retransmission tracking
	RegisterPath(pathID string) error
	
	// UnregisterPath unregisters a path from retransmission tracking
	UnregisterPath(pathID string) error
	
	// TriggerFastRetransmission triggers fast retransmission for specific packets
	TriggerFastRetransmission(pathID string, packetIDs []uint64, originalData map[uint64][]byte) error
	
	// TriggerTimeoutRetransmission triggers timeout-based retransmission
	TriggerTimeoutRetransmission(pathID string, packetIDs []uint64, originalData map[uint64][]byte) error
}

// BackoffStrategy defines how retransmission delays are calculated
type BackoffStrategy interface {
	// NextDelay calculates the delay before the next retransmission attempt
	NextDelay(attempt int, baseRTT time.Duration) time.Duration
	
	// MaxAttempts returns the maximum number of retransmission attempts
	MaxAttempts() int
	
	// ShouldRetry determines if a segment should be retransmitted
	ShouldRetry(attempt int, lastError error) bool
}

// TrackedSegment represents a segment being monitored for retransmission
type TrackedSegment struct {
	ID           string        `json:"id"`
	Data         []byte        `json:"-"` // Don't serialize data for performance
	PathID       string        `json:"path_id"`
	StreamID     uint64        `json:"stream_id"`
	Offset       int64         `json:"offset"`
	SentAt       time.Time     `json:"sent_at"`
	Attempts     int           `json:"attempts"`
	NextRetry    time.Time     `json:"next_retry"`
	MaxAttempts  int           `json:"max_attempts"`
	Acknowledged bool          `json:"acknowledged"`
	BaseRTT      time.Duration `json:"base_rtt"`
	LastError    error         `json:"-"`
	
	// Internal fields
	mutex        sync.RWMutex
	retryTimer   *time.Timer
	onRetry      func(*TrackedSegment) error
	onMaxRetries func(*TrackedSegment) error
}

// RetransmissionStats provides statistics about retransmission activity
type RetransmissionStats struct {
	TotalSegments       uint64        `json:"total_segments"`
	AcknowledgedSegments uint64        `json:"acknowledged_segments"`
	RetransmittedSegments uint64       `json:"retransmitted_segments"`
	DroppedSegments     uint64        `json:"dropped_segments"`
	AverageRTT          time.Duration `json:"average_rtt"`
	AverageRetries      float64       `json:"average_retries"`
	ActiveSegments      int           `json:"active_segments"`
	LastUpdate          time.Time     `json:"last_update"`
}

// RetransmissionManagerImpl implements the RetransmissionManager interface
type RetransmissionManagerImpl struct {
	// Configuration
	strategy BackoffStrategy
	logger   DataLogger
	
	// State management
	segments    map[string]*TrackedSegment
	segmentsMux sync.RWMutex
	
	// Statistics
	stats     RetransmissionStats
	statsMux  sync.RWMutex
	
	// Control
	running   bool
	stopChan  chan struct{}
	waitGroup sync.WaitGroup
	mutex     sync.RWMutex
	
	// Callbacks
	onRetryCallback      func(segmentID string, data []byte, pathID string) error
	onMaxRetriesCallback func(segmentID string, data []byte, pathID string) error
}

// RetransmissionConfig holds configuration for the retransmission manager
type RetransmissionConfig struct {
	DefaultTimeout       time.Duration
	CleanupInterval      time.Duration
	MaxConcurrentRetries int
	EnableDetailedStats  bool
	Logger               DataLogger
}

// DefaultRetransmissionConfig returns default configuration
func DefaultRetransmissionConfig() *RetransmissionConfig {
	return &RetransmissionConfig{
		DefaultTimeout:       5 * time.Second,
		CleanupInterval:      30 * time.Second,
		MaxConcurrentRetries: 100,
		EnableDetailedStats:  true,
		Logger:               nil,
	}
}

// NewRetransmissionManager creates a new retransmission manager
func NewRetransmissionManager(config *RetransmissionConfig) *RetransmissionManagerImpl {
	if config == nil {
		config = DefaultRetransmissionConfig()
	}
	
	return &RetransmissionManagerImpl{
		strategy:  NewExponentialBackoffStrategy(),
		logger:    config.Logger,
		segments:  make(map[string]*TrackedSegment),
		stopChan:  make(chan struct{}),
		stats: RetransmissionStats{
			LastUpdate: time.Now(),
		},
	}
}

// SetRetryCallback sets the callback function for retransmissions
func (rm *RetransmissionManagerImpl) SetRetryCallback(callback func(segmentID string, data []byte, pathID string) error) {
	rm.mutex.Lock()
	defer rm.mutex.Unlock()
	rm.onRetryCallback = callback
}

// SetMaxRetriesCallback sets the callback function for max retries reached
func (rm *RetransmissionManagerImpl) SetMaxRetriesCallback(callback func(segmentID string, data []byte, pathID string) error) {
	rm.mutex.Lock()
	defer rm.mutex.Unlock()
	rm.onMaxRetriesCallback = callback
}

// TrackSegment implements RetransmissionManager.TrackSegment
func (rm *RetransmissionManagerImpl) TrackSegment(segmentID string, data []byte, pathID string, timeout time.Duration) error {
	rm.mutex.RLock()
	if !rm.running {
		rm.mutex.RUnlock()
		return ErrRetransmissionManagerNotRunning
	}
	rm.mutex.RUnlock()
	
	segment := &TrackedSegment{
		ID:          segmentID,
		Data:        make([]byte, len(data)),
		PathID:      pathID,
		SentAt:      time.Now(),
		Attempts:    1,
		MaxAttempts: rm.strategy.MaxAttempts(),
		BaseRTT:     timeout,
		onRetry:     rm.handleRetry,
		onMaxRetries: rm.handleMaxRetries,
	}
	copy(segment.Data, data)
	
	// Calculate next retry time
	segment.NextRetry = time.Now().Add(rm.strategy.NextDelay(1, timeout))
	
	// Set up retry timer
	segment.retryTimer = time.AfterFunc(rm.strategy.NextDelay(1, timeout), func() {
		rm.handleSegmentTimeout(segmentID)
	})
	
	rm.segmentsMux.Lock()
	rm.segments[segmentID] = segment
	rm.segmentsMux.Unlock()
	
	// Update statistics
	rm.updateStats(func(stats *RetransmissionStats) {
		stats.TotalSegments++
		stats.ActiveSegments++
	})
	
	if rm.logger != nil {
		rm.logger.Debug("Tracking segment for retransmission", 
			"segmentID", segmentID, 
			"pathID", pathID,
			"timeout", timeout,
			"nextRetry", segment.NextRetry)
	}
	
	return nil
}

// AckSegment implements RetransmissionManager.AckSegment
func (rm *RetransmissionManagerImpl) AckSegment(segmentID string) error {
	rm.segmentsMux.Lock()
	segment, exists := rm.segments[segmentID]
	if !exists {
		rm.segmentsMux.Unlock()
		return ErrSegmentNotFound
	}
	
	// Mark as acknowledged and stop timer
	segment.mutex.Lock()
	if !segment.Acknowledged {
		segment.Acknowledged = true
		if segment.retryTimer != nil {
			segment.retryTimer.Stop()
		}
	}
	segment.mutex.Unlock()
	
	// Remove from tracking
	delete(rm.segments, segmentID)
	rm.segmentsMux.Unlock()
	
	// Update statistics
	rm.updateStats(func(stats *RetransmissionStats) {
		stats.AcknowledgedSegments++
		stats.ActiveSegments--
		
		// Update average retries
		totalRetries := stats.AcknowledgedSegments + stats.DroppedSegments
		if totalRetries > 0 {
			stats.AverageRetries = float64(stats.RetransmittedSegments) / float64(totalRetries)
		}
	})
	
	if rm.logger != nil {
		rm.logger.Debug("Segment acknowledged", 
			"segmentID", segmentID,
			"attempts", segment.Attempts)
	}
	
	return nil
}

// SetBackoffStrategy implements RetransmissionManager.SetBackoffStrategy
func (rm *RetransmissionManagerImpl) SetBackoffStrategy(strategy BackoffStrategy) error {
	if strategy == nil {
		return ErrInvalidBackoffStrategy
	}
	
	rm.mutex.Lock()
	defer rm.mutex.Unlock()
	
	rm.strategy = strategy
	return nil
}

// GetStats implements RetransmissionManager.GetStats
func (rm *RetransmissionManagerImpl) GetStats() RetransmissionStats {
	rm.statsMux.RLock()
	defer rm.statsMux.RUnlock()
	
	// Return a copy to avoid race conditions
	stats := rm.stats
	stats.LastUpdate = time.Now()
	return stats
}

// Start implements RetransmissionManager.Start
func (rm *RetransmissionManagerImpl) Start() error {
	rm.mutex.Lock()
	defer rm.mutex.Unlock()
	
	if rm.running {
		return ErrRetransmissionManagerAlreadyRunning
	}
	
	rm.running = true
	rm.stopChan = make(chan struct{})
	
	// Start cleanup goroutine
	rm.waitGroup.Add(1)
	go rm.cleanupLoop()
	
	if rm.logger != nil {
		rm.logger.Info("Retransmission manager started")
	}
	
	return nil
}

// Stop implements RetransmissionManager.Stop
func (rm *RetransmissionManagerImpl) Stop() error {
	rm.mutex.Lock()
	if !rm.running {
		rm.mutex.Unlock()
		return nil
	}
	
	rm.running = false
	close(rm.stopChan)
	rm.mutex.Unlock()
	
	// Wait for cleanup goroutine to finish
	rm.waitGroup.Wait()
	
	// Clean up all remaining segments
	rm.segmentsMux.Lock()
	for _, segment := range rm.segments {
		segment.mutex.Lock()
		if segment.retryTimer != nil {
			segment.retryTimer.Stop()
		}
		segment.mutex.Unlock()
	}
	rm.segments = make(map[string]*TrackedSegment)
	rm.segmentsMux.Unlock()
	
	if rm.logger != nil {
		rm.logger.Info("Retransmission manager stopped")
	}
	
	return nil
}

// RegisterPath implements RetransmissionManager.RegisterPath
func (rm *RetransmissionManagerImpl) RegisterPath(pathID string) error {
	// For now, just log the path registration
	if rm.logger != nil {
		rm.logger.Info("Registered path for retransmission tracking", "pathID", pathID)
	}
	return nil
}

// UnregisterPath implements RetransmissionManager.UnregisterPath
func (rm *RetransmissionManagerImpl) UnregisterPath(pathID string) error {
	// Clean up any segments for this path
	rm.segmentsMux.Lock()
	defer rm.segmentsMux.Unlock()
	
	var segmentsToRemove []string
	for segmentID, segment := range rm.segments {
		segment.mutex.RLock()
		if segment.PathID == pathID {
			segmentsToRemove = append(segmentsToRemove, segmentID)
		}
		segment.mutex.RUnlock()
	}
	
	for _, segmentID := range segmentsToRemove {
		if segment := rm.segments[segmentID]; segment != nil {
			segment.mutex.Lock()
			if segment.retryTimer != nil {
				segment.retryTimer.Stop()
			}
			segment.mutex.Unlock()
		}
		delete(rm.segments, segmentID)
	}
	
	if rm.logger != nil {
		rm.logger.Info("Unregistered path from retransmission tracking", 
			"pathID", pathID, 
			"removedSegments", len(segmentsToRemove))
	}
	
	return nil
}

// TriggerFastRetransmission implements RetransmissionManager.TriggerFastRetransmission
func (rm *RetransmissionManagerImpl) TriggerFastRetransmission(pathID string, packetIDs []uint64, originalData map[uint64][]byte) error {
	for _, packetID := range packetIDs {
		segmentID := fmt.Sprintf("%s-%d", pathID, packetID)
		data, exists := originalData[packetID]
		if !exists {
			data = []byte{} // Empty data as fallback
		}
		
		// Track the retransmitted segment with shorter timeout for fast retransmission
		fastTimeout := 50 * time.Millisecond
		err := rm.TrackSegment(segmentID, data, pathID, fastTimeout)
		if err != nil {
			if rm.logger != nil {
				rm.logger.Error("Failed to track fast retransmission segment", 
					"segmentID", segmentID, 
					"error", err)
			}
		}
	}
	
	if rm.logger != nil {
		rm.logger.Info("Triggered fast retransmission", 
			"pathID", pathID, 
			"packetCount", len(packetIDs))
	}
	
	return nil
}

// TriggerTimeoutRetransmission implements RetransmissionManager.TriggerTimeoutRetransmission
func (rm *RetransmissionManagerImpl) TriggerTimeoutRetransmission(pathID string, packetIDs []uint64, originalData map[uint64][]byte) error {
	for _, packetID := range packetIDs {
		segmentID := fmt.Sprintf("%s-%d-timeout", pathID, packetID)
		data, exists := originalData[packetID]
		if !exists {
			data = []byte{} // Empty data as fallback
		}
		
		// Track the retransmitted segment with longer timeout
		timeoutDuration := 1 * time.Second
		err := rm.TrackSegment(segmentID, data, pathID, timeoutDuration)
		if err != nil {
			if rm.logger != nil {
				rm.logger.Error("Failed to track timeout retransmission segment", 
					"segmentID", segmentID, 
					"error", err)
			}
		}
	}
	
	if rm.logger != nil {
		rm.logger.Info("Triggered timeout retransmission", 
			"pathID", pathID, 
			"packetCount", len(packetIDs))
	}
	
	return nil
}

// Internal helper methods

func (rm *RetransmissionManagerImpl) handleSegmentTimeout(segmentID string) {
	rm.segmentsMux.RLock()
	segment, exists := rm.segments[segmentID]
	rm.segmentsMux.RUnlock()
	
	if !exists {
		return // Segment was already acknowledged or removed
	}
	
	segment.mutex.Lock()
	defer segment.mutex.Unlock()
	
	if segment.Acknowledged {
		return // Already acknowledged
	}
	
	segment.Attempts++
	
	// Check if we should retry
	if segment.Attempts <= segment.MaxAttempts && rm.strategy.ShouldRetry(segment.Attempts, segment.LastError) {
		// Schedule next retry
		delay := rm.strategy.NextDelay(segment.Attempts, segment.BaseRTT)
		segment.NextRetry = time.Now().Add(delay)
		segment.retryTimer = time.AfterFunc(delay, func() {
			rm.handleSegmentTimeout(segmentID)
		})
		
		// Trigger retry callback
		if segment.onRetry != nil {
			segment.LastError = segment.onRetry(segment)
		}
		
		// Update statistics
		rm.updateStats(func(stats *RetransmissionStats) {
			stats.RetransmittedSegments++
		})
		
		if rm.logger != nil {
			rm.logger.Debug("Retransmitting segment", 
				"segmentID", segmentID,
				"attempt", segment.Attempts,
				"nextRetry", segment.NextRetry)
		}
	} else {
		// Max retries reached
		if segment.onMaxRetries != nil {
			segment.onMaxRetries(segment)
		}
		
		// Remove from tracking
		rm.segmentsMux.Lock()
		delete(rm.segments, segmentID)
		rm.segmentsMux.Unlock()
		
		// Update statistics
		rm.updateStats(func(stats *RetransmissionStats) {
			stats.DroppedSegments++
			stats.ActiveSegments--
		})
		
		if rm.logger != nil {
			rm.logger.Warn("Segment dropped after max retries", 
				"segmentID", segmentID,
				"attempts", segment.Attempts)
		}
	}
}

func (rm *RetransmissionManagerImpl) handleRetry(segment *TrackedSegment) error {
	rm.mutex.RLock()
	callback := rm.onRetryCallback
	rm.mutex.RUnlock()
	
	if callback != nil {
		return callback(segment.ID, segment.Data, segment.PathID)
	}
	
	return nil
}

func (rm *RetransmissionManagerImpl) handleMaxRetries(segment *TrackedSegment) error {
	rm.mutex.RLock()
	callback := rm.onMaxRetriesCallback
	rm.mutex.RUnlock()
	
	if callback != nil {
		return callback(segment.ID, segment.Data, segment.PathID)
	}
	
	return nil
}

func (rm *RetransmissionManagerImpl) updateStats(updater func(*RetransmissionStats)) {
	rm.statsMux.Lock()
	defer rm.statsMux.Unlock()
	
	updater(&rm.stats)
	rm.stats.LastUpdate = time.Now()
}

func (rm *RetransmissionManagerImpl) cleanupLoop() {
	defer rm.waitGroup.Done()
	
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			rm.performCleanup()
		case <-rm.stopChan:
			return
		}
	}
}

func (rm *RetransmissionManagerImpl) performCleanup() {
	now := time.Now()
	
	rm.segmentsMux.Lock()
	defer rm.segmentsMux.Unlock()
	
	for segmentID, segment := range rm.segments {
		segment.mutex.RLock()
		acknowledged := segment.Acknowledged
		stale := now.Sub(segment.SentAt) > 5*time.Minute
		segment.mutex.RUnlock()
		
		if acknowledged || stale {
			if segment.retryTimer != nil {
				segment.retryTimer.Stop()
			}
			delete(rm.segments, segmentID)
			
			if stale && rm.logger != nil {
				rm.logger.Warn("Cleaning up stale segment", "segmentID", segmentID)
			}
		}
	}
}

// Error definitions
var (
	ErrRetransmissionManagerNotRunning      = utils.NewKwikError(utils.ErrInvalidState, "retransmission manager is not running", nil)
	ErrRetransmissionManagerAlreadyRunning  = utils.NewKwikError(utils.ErrInvalidState, "retransmission manager is already running", nil)
	ErrSegmentNotFound                      = utils.NewKwikError(utils.ErrInvalidFrame, "segment not found", nil)
	ErrInvalidBackoffStrategy               = utils.NewKwikError(utils.ErrInvalidFrame, "invalid backoff strategy", nil)
)