package data

import (
	"sync"
	"time"

	"kwik/internal/utils"
	"kwik/proto/data"
)

// DataSchedulerImpl implements the DataScheduler interface
// It handles scheduling of data transmission across multiple paths
type DataSchedulerImpl struct {
	// Path metrics for scheduling decisions
	pathMetrics map[string]*PathMetrics
	metricsMutex sync.RWMutex

	// Scheduling configuration
	config *SchedulerConfig

	// Load balancing
	loadBalancingEnabled bool
	strategy             LoadBalancingStrategy
	roundRobinIndex      int
}

// SchedulerConfig holds configuration for the data scheduler
type SchedulerConfig struct {
	DefaultBandwidth   uint64
	DefaultRTT         int64
	MetricsUpdateInterval time.Duration
	PathSelectionTimeout  time.Duration
}

// NewDataScheduler creates a new data scheduler
func NewDataScheduler() DataScheduler {
	return &DataSchedulerImpl{
		pathMetrics: make(map[string]*PathMetrics),
		config: &SchedulerConfig{
			DefaultBandwidth:      1000000, // 1 Mbps
			DefaultRTT:            50000,   // 50ms in microseconds
			MetricsUpdateInterval: 1 * time.Second,
			PathSelectionTimeout:  100 * time.Millisecond,
		},
		loadBalancingEnabled: true,
		strategy:             LoadBalancingAdaptive,
		roundRobinIndex:      0,
	}
}

// ScheduleFrame schedules a frame for transmission on the optimal path
func (ds *DataSchedulerImpl) ScheduleFrame(frame *data.DataFrame, availablePaths []string) (string, error) {
	if len(availablePaths) == 0 {
		return "", utils.NewKwikError(utils.ErrPathNotFound, "no available paths", nil)
	}

	if len(availablePaths) == 1 {
		return availablePaths[0], nil
	}

	if !ds.loadBalancingEnabled {
		return availablePaths[0], nil
	}

	// Select path based on strategy
	switch ds.strategy {
	case LoadBalancingRoundRobin:
		return ds.selectRoundRobin(availablePaths), nil
	case LoadBalancingWeighted:
		return ds.selectWeighted(availablePaths), nil
	case LoadBalancingLatencyBased:
		return ds.selectLatencyBased(availablePaths), nil
	case LoadBalancingBandwidthBased:
		return ds.selectBandwidthBased(availablePaths), nil
	case LoadBalancingAdaptive:
		return ds.selectAdaptive(availablePaths), nil
	default:
		return availablePaths[0], nil
	}
}

// UpdatePathMetrics updates metrics for a path
func (ds *DataSchedulerImpl) UpdatePathMetrics(pathID string, metrics *PathMetrics) error {
	if pathID == "" {
		return utils.NewKwikError(utils.ErrInvalidFrame, "path ID is empty", nil)
	}

	if metrics == nil {
		return utils.NewKwikError(utils.ErrInvalidFrame, "metrics is nil", nil)
	}

	ds.metricsMutex.Lock()
	defer ds.metricsMutex.Unlock()

	// Update metrics
	ds.pathMetrics[pathID] = &PathMetrics{
		PathID:         metrics.PathID,
		RTT:            metrics.RTT,
		Bandwidth:      metrics.Bandwidth,
		PacketLoss:     metrics.PacketLoss,
		Congestion:     metrics.Congestion,
		LastUpdate:     time.Now().UnixNano(),
		IsActive:       metrics.IsActive,
		QueueDepth:     metrics.QueueDepth,
		ThroughputMbps: metrics.ThroughputMbps,
	}

	return nil
}

// AddPath adds a path to the scheduler
func (ds *DataSchedulerImpl) AddPath(pathID string, metrics *PathMetrics) error {
	if pathID == "" {
		return utils.NewKwikError(utils.ErrInvalidFrame, "path ID is empty", nil)
	}

	ds.metricsMutex.Lock()
	defer ds.metricsMutex.Unlock()

	if metrics == nil {
		// Use default metrics
		metrics = &PathMetrics{
			PathID:         pathID,
			RTT:            ds.config.DefaultRTT,
			Bandwidth:      ds.config.DefaultBandwidth,
			PacketLoss:     0.0,
			Congestion:     0.0,
			LastUpdate:     time.Now().UnixNano(),
			IsActive:       true,
			QueueDepth:     0,
			ThroughputMbps: float64(ds.config.DefaultBandwidth) / 1000000.0,
		}
	}

	ds.pathMetrics[pathID] = metrics
	return nil
}

// RemovePath removes a path from the scheduler
func (ds *DataSchedulerImpl) RemovePath(pathID string) error {
	ds.metricsMutex.Lock()
	defer ds.metricsMutex.Unlock()

	delete(ds.pathMetrics, pathID)
	return nil
}

// GetOptimalPath returns the optimal path for a stream
func (ds *DataSchedulerImpl) GetOptimalPath(streamID uint64) (string, error) {
	ds.metricsMutex.RLock()
	defer ds.metricsMutex.RUnlock()

	if len(ds.pathMetrics) == 0 {
		return "", utils.NewKwikError(utils.ErrPathNotFound, "no paths available", nil)
	}

	// Get all active paths
	activePaths := make([]string, 0, len(ds.pathMetrics))
	for pathID, metrics := range ds.pathMetrics {
		if metrics.IsActive {
			activePaths = append(activePaths, pathID)
		}
	}

	if len(activePaths) == 0 {
		return "", utils.NewKwikError(utils.ErrPathDead, "no active paths", nil)
	}

	// Use adaptive selection for optimal path
	return ds.selectAdaptive(activePaths), nil
}

// EnableLoadBalancing enables or disables load balancing
func (ds *DataSchedulerImpl) EnableLoadBalancing(enable bool) {
	ds.loadBalancingEnabled = enable
}

// SetLoadBalancingStrategy sets the load balancing strategy
func (ds *DataSchedulerImpl) SetLoadBalancingStrategy(strategy LoadBalancingStrategy) {
	ds.strategy = strategy
}

// Path selection algorithms

// selectRoundRobin selects paths in round-robin fashion
func (ds *DataSchedulerImpl) selectRoundRobin(availablePaths []string) string {
	if len(availablePaths) == 0 {
		return ""
	}

	selectedPath := availablePaths[ds.roundRobinIndex%len(availablePaths)]
	ds.roundRobinIndex++
	return selectedPath
}

// selectWeighted selects paths based on bandwidth weights
func (ds *DataSchedulerImpl) selectWeighted(availablePaths []string) string {
	if len(availablePaths) == 0 {
		return ""
	}

	ds.metricsMutex.RLock()
	defer ds.metricsMutex.RUnlock()

	// Calculate total bandwidth
	totalBandwidth := uint64(0)
	for _, pathID := range availablePaths {
		if metrics, exists := ds.pathMetrics[pathID]; exists {
			totalBandwidth += metrics.Bandwidth
		}
	}

	if totalBandwidth == 0 {
		return availablePaths[0]
	}

	// Select based on bandwidth proportion
	// This is a simplified implementation
	bestPath := availablePaths[0]
	bestBandwidth := uint64(0)

	for _, pathID := range availablePaths {
		if metrics, exists := ds.pathMetrics[pathID]; exists {
			if metrics.Bandwidth > bestBandwidth {
				bestBandwidth = metrics.Bandwidth
				bestPath = pathID
			}
		}
	}

	return bestPath
}

// selectLatencyBased selects the path with lowest latency
func (ds *DataSchedulerImpl) selectLatencyBased(availablePaths []string) string {
	if len(availablePaths) == 0 {
		return ""
	}

	ds.metricsMutex.RLock()
	defer ds.metricsMutex.RUnlock()

	bestPath := availablePaths[0]
	bestRTT := int64(999999999) // Very high initial value

	for _, pathID := range availablePaths {
		if metrics, exists := ds.pathMetrics[pathID]; exists {
			if metrics.RTT < bestRTT {
				bestRTT = metrics.RTT
				bestPath = pathID
			}
		}
	}

	return bestPath
}

// selectBandwidthBased selects the path with highest bandwidth
func (ds *DataSchedulerImpl) selectBandwidthBased(availablePaths []string) string {
	if len(availablePaths) == 0 {
		return ""
	}

	ds.metricsMutex.RLock()
	defer ds.metricsMutex.RUnlock()

	bestPath := availablePaths[0]
	bestBandwidth := uint64(0)

	for _, pathID := range availablePaths {
		if metrics, exists := ds.pathMetrics[pathID]; exists {
			if metrics.Bandwidth > bestBandwidth {
				bestBandwidth = metrics.Bandwidth
				bestPath = pathID
			}
		}
	}

	return bestPath
}

// selectAdaptive selects path using adaptive algorithm considering multiple factors
func (ds *DataSchedulerImpl) selectAdaptive(availablePaths []string) string {
	if len(availablePaths) == 0 {
		return ""
	}

	ds.metricsMutex.RLock()
	defer ds.metricsMutex.RUnlock()

	bestPath := availablePaths[0]
	bestScore := float64(-1)

	for _, pathID := range availablePaths {
		if metrics, exists := ds.pathMetrics[pathID]; exists {
			// Calculate composite score
			// Higher bandwidth is better, lower RTT is better, lower loss is better
			bandwidthScore := float64(metrics.Bandwidth) / 1000000.0 // Convert to Mbps
			rttScore := 1000.0 / (float64(metrics.RTT)/1000.0 + 1.0) // Lower RTT = higher score
			lossScore := 1.0 - metrics.PacketLoss                    // Lower loss = higher score
			congestionScore := 1.0 - metrics.Congestion             // Lower congestion = higher score

			// Weighted composite score
			score := (bandwidthScore * 0.4) + (rttScore * 0.3) + (lossScore * 0.2) + (congestionScore * 0.1)

			if score > bestScore {
				bestScore = score
				bestPath = pathID
			}
		}
	}

	return bestPath
}