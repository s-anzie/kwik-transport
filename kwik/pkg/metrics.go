package kwik

import (
	"sync"
	"time"
)

// SystemMetrics holds system-wide metrics for KWIK
type SystemMetrics struct {
	// Session metrics
	ActiveSessions       int64
	SessionsCreated      int64
	SessionsClosed       int64
	
	// Path metrics
	ActivePaths          int64
	PathsCreated         int64
	PathsRemoved         int64
	
	// Data metrics
	TotalBytesTransferred uint64
	TotalFramesProcessed  uint64
	
	// Performance metrics
	AverageLatency       time.Duration
	ThroughputBps        uint64
	
	// Error metrics
	TotalErrors          int64
	AuthenticationErrors int64
	ConnectionErrors     int64
	
	// Internal state
	startTime            time.Time
	lastUpdate           time.Time
	mutex                sync.RWMutex
	active               bool
}

// NewSystemMetrics creates a new system metrics instance
func NewSystemMetrics(updateInterval time.Duration) *SystemMetrics {
	return &SystemMetrics{
		startTime:  time.Now(),
		lastUpdate: time.Now(),
		active:     false,
	}
}

// Start starts metrics collection
func (sm *SystemMetrics) Start() {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()
	
	sm.active = true
	sm.startTime = time.Now()
	sm.lastUpdate = time.Now()
}

// Stop stops metrics collection
func (sm *SystemMetrics) Stop() {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()
	
	sm.active = false
}

// IncrementSessionsCreated increments the sessions created counter
func (sm *SystemMetrics) IncrementSessionsCreated() {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()
	
	sm.SessionsCreated++
}

// SetActiveSessionCount sets the active session count
func (sm *SystemMetrics) SetActiveSessionCount(count int) {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()
	
	sm.ActiveSessions = int64(count)
}

// SetActivePathCount sets the active path count
func (sm *SystemMetrics) SetActivePathCount(count int) {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()
	
	sm.ActivePaths = int64(count)
}

// SetTotalBytesTransferred sets the total bytes transferred
func (sm *SystemMetrics) SetTotalBytesTransferred(bytes uint64) {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()
	
	sm.TotalBytesTransferred = bytes
}

// SetTotalFramesProcessed sets the total frames processed
func (sm *SystemMetrics) SetTotalFramesProcessed(frames uint64) {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()
	
	sm.TotalFramesProcessed = frames
}

// GetSnapshot returns a snapshot of current metrics
func (sm *SystemMetrics) GetSnapshot() *SystemMetrics {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()
	
	// Create a copy of the metrics
	snapshot := &SystemMetrics{
		ActiveSessions:        sm.ActiveSessions,
		SessionsCreated:       sm.SessionsCreated,
		SessionsClosed:        sm.SessionsClosed,
		ActivePaths:           sm.ActivePaths,
		PathsCreated:          sm.PathsCreated,
		PathsRemoved:          sm.PathsRemoved,
		TotalBytesTransferred: sm.TotalBytesTransferred,
		TotalFramesProcessed:  sm.TotalFramesProcessed,
		AverageLatency:        sm.AverageLatency,
		ThroughputBps:         sm.ThroughputBps,
		TotalErrors:           sm.TotalErrors,
		AuthenticationErrors:  sm.AuthenticationErrors,
		ConnectionErrors:      sm.ConnectionErrors,
		startTime:             sm.startTime,
		lastUpdate:            sm.lastUpdate,
		active:                sm.active,
	}
	
	return snapshot
}