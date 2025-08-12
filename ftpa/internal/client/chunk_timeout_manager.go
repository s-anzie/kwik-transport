package internal

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// ChunkTimeoutManager handles timeout detection and retry logic for chunks
type ChunkTimeoutManager struct {
	mu                sync.RWMutex
	chunkTimeouts     map[string]map[uint32]*ChunkTimeout // filename -> sequence -> timeout info
	defaultTimeout    time.Duration                       // Default timeout for chunks
	maxRetries        int                                 // Maximum retry attempts per chunk
	retryBackoff      time.Duration                       // Backoff time between retries
	timeoutCallbacks  map[string]TimeoutCallback          // Callbacks for timeout events
	ctx               context.Context                     // Context for cancellation
	cancel            context.CancelFunc                  // Cancel function
	ticker            *time.Ticker                        // Timer for checking timeouts
}

// ChunkTimeout tracks timeout information for a specific chunk
type ChunkTimeout struct {
	Filename      string        // File being transferred
	SequenceNum   uint32        // Chunk sequence number
	ChunkID       uint32        // Chunk ID
	RequestedAt   time.Time     // When the chunk was first requested
	LastRetryAt   time.Time     // When the last retry was attempted
	RetryCount    int           // Number of retry attempts
	Timeout       time.Duration // Timeout duration for this chunk
	IsTimedOut    bool          // Whether this chunk has timed out
	NextRetryAt   time.Time     // When the next retry should be attempted
	Priority      int           // Priority level (higher = more urgent)
	MaxRetries    int           // Maximum retries for this specific chunk
}

// TimeoutCallback is called when a chunk times out
type TimeoutCallback func(timeout *ChunkTimeout) error

// TimeoutConfig contains configuration for chunk timeouts
type TimeoutConfig struct {
	DefaultTimeout time.Duration // Default timeout for chunks
	MaxRetries     int           // Maximum retry attempts
	RetryBackoff   time.Duration // Backoff time between retries
	CheckInterval  time.Duration // How often to check for timeouts
}

// DefaultTimeoutConfig returns a default timeout configuration
func DefaultTimeoutConfig() *TimeoutConfig {
	return &TimeoutConfig{
		DefaultTimeout: 30 * time.Second,
		MaxRetries:     3,
		RetryBackoff:   5 * time.Second,
		CheckInterval:  5 * time.Second,
	}
}

// NewChunkTimeoutManager creates a new chunk timeout manager
func NewChunkTimeoutManager(config *TimeoutConfig) *ChunkTimeoutManager {
	if config == nil {
		config = DefaultTimeoutConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())

	manager := &ChunkTimeoutManager{
		chunkTimeouts:    make(map[string]map[uint32]*ChunkTimeout),
		defaultTimeout:   config.DefaultTimeout,
		maxRetries:       config.MaxRetries,
		retryBackoff:     config.RetryBackoff,
		timeoutCallbacks: make(map[string]TimeoutCallback),
		ctx:              ctx,
		cancel:           cancel,
		ticker:           time.NewTicker(config.CheckInterval),
	}

	// Start timeout checking goroutine
	go manager.timeoutChecker()

	return manager
}

// RegisterChunk registers a chunk for timeout monitoring
func (tm *ChunkTimeoutManager) RegisterChunk(filename string, sequenceNum, chunkID uint32, customTimeout time.Duration) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	// Initialize file map if needed
	if tm.chunkTimeouts[filename] == nil {
		tm.chunkTimeouts[filename] = make(map[uint32]*ChunkTimeout)
	}

	timeout := tm.defaultTimeout
	if customTimeout > 0 {
		timeout = customTimeout
	}

	now := time.Now()
	chunkTimeout := &ChunkTimeout{
		Filename:      filename,
		SequenceNum:   sequenceNum,
		ChunkID:       chunkID,
		RequestedAt:   now,
		LastRetryAt:   now,
		RetryCount:    0,
		Timeout:       timeout,
		IsTimedOut:    false,
		NextRetryAt:   now.Add(timeout),
		Priority:      0,
		MaxRetries:    tm.maxRetries,
	}

	tm.chunkTimeouts[filename][sequenceNum] = chunkTimeout
}

// UnregisterChunk removes a chunk from timeout monitoring (when received)
func (tm *ChunkTimeoutManager) UnregisterChunk(filename string, sequenceNum uint32) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if fileTimeouts, exists := tm.chunkTimeouts[filename]; exists {
		delete(fileTimeouts, sequenceNum)
		
		// Clean up empty file map
		if len(fileTimeouts) == 0 {
			delete(tm.chunkTimeouts, filename)
		}
	}
}

// MarkChunkReceived marks a chunk as received and removes it from timeout monitoring
func (tm *ChunkTimeoutManager) MarkChunkReceived(filename string, sequenceNum uint32) {
	tm.UnregisterChunk(filename, sequenceNum)
}

// UpdateChunkTimeout updates the timeout for a specific chunk
func (tm *ChunkTimeoutManager) UpdateChunkTimeout(filename string, sequenceNum uint32, newTimeout time.Duration) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	fileTimeouts, exists := tm.chunkTimeouts[filename]
	if !exists {
		return fmt.Errorf("no timeouts registered for file: %s", filename)
	}

	chunkTimeout, exists := fileTimeouts[sequenceNum]
	if !exists {
		return fmt.Errorf("no timeout registered for chunk %d in file %s", sequenceNum, filename)
	}

	chunkTimeout.Timeout = newTimeout
	chunkTimeout.NextRetryAt = time.Now().Add(newTimeout)

	return nil
}

// GetTimedOutChunks returns all chunks that have timed out and need retry
func (tm *ChunkTimeoutManager) GetTimedOutChunks() []*ChunkTimeout {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	var timedOut []*ChunkTimeout
	now := time.Now()

	for _, fileTimeouts := range tm.chunkTimeouts {
		for _, chunkTimeout := range fileTimeouts {
			if now.After(chunkTimeout.NextRetryAt) && chunkTimeout.RetryCount < chunkTimeout.MaxRetries {
				timedOut = append(timedOut, chunkTimeout)
			}
		}
	}

	return timedOut
}

// GetTimedOutChunksForFile returns timed out chunks for a specific file
func (tm *ChunkTimeoutManager) GetTimedOutChunksForFile(filename string) []*ChunkTimeout {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	var timedOut []*ChunkTimeout
	now := time.Now()

	fileTimeouts, exists := tm.chunkTimeouts[filename]
	if !exists {
		return timedOut
	}

	for _, chunkTimeout := range fileTimeouts {
		if now.After(chunkTimeout.NextRetryAt) && chunkTimeout.RetryCount < chunkTimeout.MaxRetries {
			timedOut = append(timedOut, chunkTimeout)
		}
	}

	return timedOut
}

// MarkChunkForRetry marks a chunk for retry and updates its timeout
func (tm *ChunkTimeoutManager) MarkChunkForRetry(filename string, sequenceNum uint32) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	fileTimeouts, exists := tm.chunkTimeouts[filename]
	if !exists {
		return fmt.Errorf("no timeouts registered for file: %s", filename)
	}

	chunkTimeout, exists := fileTimeouts[sequenceNum]
	if !exists {
		return fmt.Errorf("no timeout registered for chunk %d in file %s", sequenceNum, filename)
	}

	now := time.Now()
	chunkTimeout.RetryCount++
	chunkTimeout.LastRetryAt = now
	chunkTimeout.IsTimedOut = true
	
	// Calculate next retry time with exponential backoff
	backoffMultiplier := time.Duration(1 << chunkTimeout.RetryCount) // 2^retryCount
	nextRetryDelay := tm.retryBackoff * backoffMultiplier
	if nextRetryDelay > chunkTimeout.Timeout {
		nextRetryDelay = chunkTimeout.Timeout
	}
	
	chunkTimeout.NextRetryAt = now.Add(nextRetryDelay)
	
	// Increase priority based on retry count and age
	age := now.Sub(chunkTimeout.RequestedAt)
	chunkTimeout.Priority = chunkTimeout.RetryCount*10 + int(age.Minutes())

	return nil
}

// GetChunkTimeout returns timeout information for a specific chunk
func (tm *ChunkTimeoutManager) GetChunkTimeout(filename string, sequenceNum uint32) (*ChunkTimeout, error) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	fileTimeouts, exists := tm.chunkTimeouts[filename]
	if !exists {
		return nil, fmt.Errorf("no timeouts registered for file: %s", filename)
	}

	chunkTimeout, exists := fileTimeouts[sequenceNum]
	if !exists {
		return nil, fmt.Errorf("no timeout registered for chunk %d in file %s", sequenceNum, filename)
	}

	// Return a copy to avoid race conditions
	timeoutCopy := *chunkTimeout
	return &timeoutCopy, nil
}

// SetTimeoutCallback sets a callback function for timeout events
func (tm *ChunkTimeoutManager) SetTimeoutCallback(filename string, callback TimeoutCallback) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	tm.timeoutCallbacks[filename] = callback
}

// RemoveTimeoutCallback removes the timeout callback for a file
func (tm *ChunkTimeoutManager) RemoveTimeoutCallback(filename string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	delete(tm.timeoutCallbacks, filename)
}

// GetActiveTimeouts returns the number of active timeouts being monitored
func (tm *ChunkTimeoutManager) GetActiveTimeouts() int {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	count := 0
	for _, fileTimeouts := range tm.chunkTimeouts {
		count += len(fileTimeouts)
	}

	return count
}

// GetActiveTimeoutsForFile returns the number of active timeouts for a specific file
func (tm *ChunkTimeoutManager) GetActiveTimeoutsForFile(filename string) int {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	if fileTimeouts, exists := tm.chunkTimeouts[filename]; exists {
		return len(fileTimeouts)
	}

	return 0
}

// CleanupFile removes all timeout monitoring for a specific file
func (tm *ChunkTimeoutManager) CleanupFile(filename string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	delete(tm.chunkTimeouts, filename)
	delete(tm.timeoutCallbacks, filename)
}

// timeoutChecker runs in a goroutine to check for timed out chunks
func (tm *ChunkTimeoutManager) timeoutChecker() {
	for {
		select {
		case <-tm.ctx.Done():
			return
		case <-tm.ticker.C:
			tm.processTimeouts()
		}
	}
}

// processTimeouts processes all timed out chunks and triggers callbacks
func (tm *ChunkTimeoutManager) processTimeouts() {
	timedOutChunks := tm.GetTimedOutChunks()

	for _, chunkTimeout := range timedOutChunks {
		// Mark chunk for retry
		tm.MarkChunkForRetry(chunkTimeout.Filename, chunkTimeout.SequenceNum)

		// Call timeout callback if registered
		tm.mu.RLock()
		callback, exists := tm.timeoutCallbacks[chunkTimeout.Filename]
		tm.mu.RUnlock()

		if exists && callback != nil {
			go func(timeout *ChunkTimeout) {
				callback(timeout)
			}(chunkTimeout)
		}
	}
}

// GetTimeoutStatistics returns statistics about timeouts
func (tm *ChunkTimeoutManager) GetTimeoutStatistics() *TimeoutStatistics {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	stats := &TimeoutStatistics{
		TotalActiveTimeouts: 0,
		FileTimeouts:        make(map[string]int),
		TotalRetries:        0,
		MaxRetryCount:       0,
	}

	now := time.Now()

	for filename, fileTimeouts := range tm.chunkTimeouts {
		stats.FileTimeouts[filename] = len(fileTimeouts)
		stats.TotalActiveTimeouts += len(fileTimeouts)

		for _, chunkTimeout := range fileTimeouts {
			stats.TotalRetries += chunkTimeout.RetryCount
			if chunkTimeout.RetryCount > stats.MaxRetryCount {
				stats.MaxRetryCount = chunkTimeout.RetryCount
			}

			if now.After(chunkTimeout.NextRetryAt) {
				stats.TimedOutChunks++
			}
		}
	}

	return stats
}

// TimeoutStatistics contains statistics about chunk timeouts
type TimeoutStatistics struct {
	TotalActiveTimeouts int            `json:"total_active_timeouts"`
	FileTimeouts        map[string]int `json:"file_timeouts"`
	TotalRetries        int            `json:"total_retries"`
	MaxRetryCount       int            `json:"max_retry_count"`
	TimedOutChunks      int            `json:"timed_out_chunks"`
}

// Close stops the timeout manager and cleans up resources
func (tm *ChunkTimeoutManager) Close() error {
	tm.cancel()
	tm.ticker.Stop()

	tm.mu.Lock()
	defer tm.mu.Unlock()

	// Clear all timeouts
	tm.chunkTimeouts = make(map[string]map[uint32]*ChunkTimeout)
	tm.timeoutCallbacks = make(map[string]TimeoutCallback)

	return nil
}

// IsChunkTimedOut checks if a specific chunk has timed out
func (tm *ChunkTimeoutManager) IsChunkTimedOut(filename string, sequenceNum uint32) (bool, error) {
	chunkTimeout, err := tm.GetChunkTimeout(filename, sequenceNum)
	if err != nil {
		return false, err
	}

	return time.Now().After(chunkTimeout.NextRetryAt), nil
}

// GetNextRetryTime returns when the next retry should be attempted for a chunk
func (tm *ChunkTimeoutManager) GetNextRetryTime(filename string, sequenceNum uint32) (time.Time, error) {
	chunkTimeout, err := tm.GetChunkTimeout(filename, sequenceNum)
	if err != nil {
		return time.Time{}, err
	}

	return chunkTimeout.NextRetryAt, nil
}

// SetMaxRetries updates the maximum retry count for all chunks in a file
func (tm *ChunkTimeoutManager) SetMaxRetries(filename string, maxRetries int) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if fileTimeouts, exists := tm.chunkTimeouts[filename]; exists {
		for _, chunkTimeout := range fileTimeouts {
			chunkTimeout.MaxRetries = maxRetries
		}
	}
}

// GetChunksExceedingMaxRetries returns chunks that have exceeded maximum retry attempts
func (tm *ChunkTimeoutManager) GetChunksExceedingMaxRetries() []*ChunkTimeout {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	var exceededChunks []*ChunkTimeout

	for _, fileTimeouts := range tm.chunkTimeouts {
		for _, chunkTimeout := range fileTimeouts {
			if chunkTimeout.RetryCount >= chunkTimeout.MaxRetries {
				exceededChunks = append(exceededChunks, chunkTimeout)
			}
		}
	}

	return exceededChunks
}