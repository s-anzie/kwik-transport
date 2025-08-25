package session

import (
	"math"
	"time"
)

// PathHealthTracker provides advanced health tracking algorithms for connection paths
type PathHealthTracker struct {
	// Configuration
	windowSize       int           // Number of samples to keep in sliding window
	rttSmoothingFactor float64     // Exponential smoothing factor for RTT (0.0-1.0)
	lossWindowSize   int           // Window size for packet loss calculation
	degradationThreshold float64   // Threshold for detecting path degradation
	
	// Tracking state
	rttSamples       []time.Duration
	lossSamples      []bool
	throughputSamples []float64
	jitterSamples    []time.Duration
	
	// Calculated metrics
	smoothedRTT      time.Duration
	rttVariance      time.Duration
	packetLossRate   float64
	avgThroughput    float64
	jitterMean       time.Duration
	healthTrend      HealthTrend
	
	// Degradation detection
	baselineRTT      time.Duration
	baselineLoss     float64
	degradationStart *time.Time
	recoveryStart    *time.Time
}

// HealthTrend indicates the trend in path health
type HealthTrend int

const (
	HealthTrendStable HealthTrend = iota
	HealthTrendImproving
	HealthTrendDegrading
	HealthTrendRecovering
)

func (t HealthTrend) String() string {
	switch t {
	case HealthTrendStable:
		return "STABLE"
	case HealthTrendImproving:
		return "IMPROVING"
	case HealthTrendDegrading:
		return "DEGRADING"
	case HealthTrendRecovering:
		return "RECOVERING"
	default:
		return "UNKNOWN"
	}
}

// NewPathHealthTracker creates a new path health tracker with default configuration
func NewPathHealthTracker() *PathHealthTracker {
	return &PathHealthTracker{
		windowSize:           100,
		rttSmoothingFactor:   0.125, // Standard TCP smoothing factor
		lossWindowSize:       50,
		degradationThreshold: 1.5,   // 50% increase from baseline
		rttSamples:          make([]time.Duration, 0, 100),
		lossSamples:         make([]bool, 0, 50),
		throughputSamples:   make([]float64, 0, 100),
		jitterSamples:       make([]time.Duration, 0, 100),
		healthTrend:         HealthTrendStable,
	}
}

// AddRTTSample adds a new RTT measurement and updates calculated metrics
func (tracker *PathHealthTracker) AddRTTSample(rtt time.Duration) {
	// Add to samples
	tracker.rttSamples = append(tracker.rttSamples, rtt)
	if len(tracker.rttSamples) > tracker.windowSize {
		tracker.rttSamples = tracker.rttSamples[1:]
	}
	
	// Update smoothed RTT using exponential weighted moving average
	if tracker.smoothedRTT == 0 {
		tracker.smoothedRTT = rtt
	} else {
		alpha := tracker.rttSmoothingFactor
		tracker.smoothedRTT = time.Duration(float64(tracker.smoothedRTT)*(1-alpha) + float64(rtt)*alpha)
	}
	
	// Calculate RTT variance
	tracker.calculateRTTVariance()
	
	// Update baseline if this is early in the connection
	if len(tracker.rttSamples) <= 10 && tracker.baselineRTT == 0 {
		tracker.baselineRTT = tracker.smoothedRTT
	}
	
	// Calculate jitter (RTT variation)
	if len(tracker.rttSamples) >= 2 {
		prevRTT := tracker.rttSamples[len(tracker.rttSamples)-2]
		jitter := rtt - prevRTT
		if jitter < 0 {
			jitter = -jitter
		}
		tracker.addJitterSample(jitter)
	}
	
	// Update health trend
	tracker.updateHealthTrend()
}

// AddPacketLossSample adds a packet loss sample (true for lost, false for received)
func (tracker *PathHealthTracker) AddPacketLossSample(lost bool) {
	tracker.lossSamples = append(tracker.lossSamples, lost)
	if len(tracker.lossSamples) > tracker.lossWindowSize {
		tracker.lossSamples = tracker.lossSamples[1:]
	}
	
	// Calculate packet loss rate
	tracker.calculatePacketLossRate()
	
	// Update baseline loss rate
	if len(tracker.lossSamples) >= tracker.lossWindowSize && tracker.baselineLoss == 0 {
		tracker.baselineLoss = tracker.packetLossRate
	}
	
	// Update health trend
	tracker.updateHealthTrend()
}

// AddThroughputSample adds a throughput measurement
func (tracker *PathHealthTracker) AddThroughputSample(throughput float64) {
	tracker.throughputSamples = append(tracker.throughputSamples, throughput)
	if len(tracker.throughputSamples) > tracker.windowSize {
		tracker.throughputSamples = tracker.throughputSamples[1:]
	}
	
	// Calculate average throughput
	tracker.calculateAverageThroughput()
}

// addJitterSample adds a jitter sample and calculates jitter statistics
func (tracker *PathHealthTracker) addJitterSample(jitter time.Duration) {
	tracker.jitterSamples = append(tracker.jitterSamples, jitter)
	if len(tracker.jitterSamples) > tracker.windowSize {
		tracker.jitterSamples = tracker.jitterSamples[1:]
	}
	
	// Calculate mean jitter
	if len(tracker.jitterSamples) > 0 {
		var sum time.Duration
		for _, j := range tracker.jitterSamples {
			sum += j
		}
		tracker.jitterMean = sum / time.Duration(len(tracker.jitterSamples))
	}
}

// calculateRTTVariance calculates the variance in RTT measurements
func (tracker *PathHealthTracker) calculateRTTVariance() {
	if len(tracker.rttSamples) < 2 {
		return
	}
	
	// Calculate mean
	var sum time.Duration
	for _, rtt := range tracker.rttSamples {
		sum += rtt
	}
	mean := sum / time.Duration(len(tracker.rttSamples))
	
	// Calculate variance
	var varianceSum float64
	for _, rtt := range tracker.rttSamples {
		diff := float64(rtt - mean)
		varianceSum += diff * diff
	}
	
	variance := varianceSum / float64(len(tracker.rttSamples))
	tracker.rttVariance = time.Duration(math.Sqrt(variance))
}

// calculatePacketLossRate calculates the current packet loss rate
func (tracker *PathHealthTracker) calculatePacketLossRate() {
	if len(tracker.lossSamples) == 0 {
		tracker.packetLossRate = 0.0
		return
	}
	
	lostCount := 0
	for _, lost := range tracker.lossSamples {
		if lost {
			lostCount++
		}
	}
	
	tracker.packetLossRate = float64(lostCount) / float64(len(tracker.lossSamples))
}

// calculateAverageThroughput calculates the average throughput
func (tracker *PathHealthTracker) calculateAverageThroughput() {
	if len(tracker.throughputSamples) == 0 {
		tracker.avgThroughput = 0.0
		return
	}
	
	var sum float64
	for _, throughput := range tracker.throughputSamples {
		sum += throughput
	}
	
	tracker.avgThroughput = sum / float64(len(tracker.throughputSamples))
}

// updateHealthTrend analyzes recent metrics to determine health trend
func (tracker *PathHealthTracker) updateHealthTrend() {
	if tracker.baselineRTT == 0 || len(tracker.rttSamples) < 10 {
		tracker.healthTrend = HealthTrendStable
		return
	}
	
	// Check for RTT degradation
	rttRatio := float64(tracker.smoothedRTT) / float64(tracker.baselineRTT)
	lossIncrease := tracker.packetLossRate - tracker.baselineLoss
	
	now := time.Now()
	
	// Determine if path is degrading
	isDegrading := rttRatio > tracker.degradationThreshold || lossIncrease > 0.02 // 2% loss increase
	
	// Determine if path is recovering
	isRecovering := rttRatio < 1.1 && lossIncrease < 0.005 // Within 10% of baseline RTT and 0.5% loss
	
	switch tracker.healthTrend {
	case HealthTrendStable:
		if isDegrading {
			tracker.healthTrend = HealthTrendDegrading
			tracker.degradationStart = &now
		} else if isRecovering && tracker.degradationStart != nil {
			tracker.healthTrend = HealthTrendImproving
		}
		
	case HealthTrendDegrading:
		if isRecovering {
			tracker.healthTrend = HealthTrendRecovering
			tracker.recoveryStart = &now
		}
		
	case HealthTrendRecovering:
		if isDegrading {
			tracker.healthTrend = HealthTrendDegrading
			tracker.recoveryStart = nil
		} else if isRecovering && tracker.recoveryStart != nil {
			// If we've been recovering for a while, consider it stable
			if time.Since(*tracker.recoveryStart) > 30*time.Second {
				tracker.healthTrend = HealthTrendStable
				tracker.degradationStart = nil
				tracker.recoveryStart = nil
				// Update baseline to current good values
				tracker.baselineRTT = tracker.smoothedRTT
				tracker.baselineLoss = tracker.packetLossRate
			}
		}
		
	case HealthTrendImproving:
		if isDegrading {
			tracker.healthTrend = HealthTrendDegrading
		} else if isRecovering {
			tracker.healthTrend = HealthTrendStable
			tracker.degradationStart = nil
		}
	}
}

// CalculateHealthScore calculates a comprehensive health score (0-100)
func (tracker *PathHealthTracker) CalculateHealthScore(thresholds HealthThresholds) int {
	if len(tracker.rttSamples) == 0 {
		return 100 // No data yet, assume healthy
	}
	
	score := 100
	
	// RTT component (30% of score)
	rttScore := tracker.calculateRTTScore(thresholds)
	score = int(float64(score) * 0.7 + float64(rttScore) * 0.3)
	
	// Packet loss component (25% of score)
	lossScore := tracker.calculateLossScore(thresholds)
	score = int(float64(score) * 0.75 + float64(lossScore) * 0.25)
	
	// Jitter component (20% of score)
	jitterScore := tracker.calculateJitterScore(thresholds)
	score = int(float64(score) * 0.8 + float64(jitterScore) * 0.2)
	
	// Trend component (15% of score)
	trendScore := tracker.calculateTrendScore()
	score = int(float64(score) * 0.85 + float64(trendScore) * 0.15)
	
	// Throughput component (10% of score)
	throughputScore := tracker.calculateThroughputScore()
	score = int(float64(score) * 0.9 + float64(throughputScore) * 0.1)
	
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	
	return score
}

// calculateRTTScore calculates the RTT component of the health score
func (tracker *PathHealthTracker) calculateRTTScore(thresholds HealthThresholds) int {
	if tracker.smoothedRTT <= thresholds.RTTWarningThreshold {
		return 100
	} else if tracker.smoothedRTT <= thresholds.RTTCriticalThreshold {
		// Linear interpolation between warning and critical
		ratio := float64(tracker.smoothedRTT-thresholds.RTTWarningThreshold) / 
				float64(thresholds.RTTCriticalThreshold-thresholds.RTTWarningThreshold)
		return int(100 - ratio*50) // 100 to 50
	} else {
		// Above critical threshold
		ratio := float64(tracker.smoothedRTT) / float64(thresholds.RTTCriticalThreshold)
		if ratio > 3.0 {
			return 0
		}
		return int(50 - (ratio-1.0)*25) // 50 to 0 for 1x to 3x critical
	}
}

// calculateLossScore calculates the packet loss component of the health score
func (tracker *PathHealthTracker) calculateLossScore(thresholds HealthThresholds) int {
	if tracker.packetLossRate <= thresholds.PacketLossWarningThreshold {
		return 100
	} else if tracker.packetLossRate <= thresholds.PacketLossCriticalThreshold {
		// Linear interpolation between warning and critical
		ratio := (tracker.packetLossRate - thresholds.PacketLossWarningThreshold) /
				(thresholds.PacketLossCriticalThreshold - thresholds.PacketLossWarningThreshold)
		return int(100 - ratio*50) // 100 to 50
	} else {
		// Above critical threshold
		if tracker.packetLossRate > 0.2 { // 20% loss
			return 0
		}
		ratio := tracker.packetLossRate / thresholds.PacketLossCriticalThreshold
		return int(50 - (ratio-1.0)*50) // 50 to 0
	}
}

// calculateJitterScore calculates the jitter component of the health score
func (tracker *PathHealthTracker) calculateJitterScore(thresholds HealthThresholds) int {
	if tracker.jitterMean == 0 {
		return 100
	}
	
	// Jitter threshold as percentage of RTT
	jitterThreshold := thresholds.RTTWarningThreshold / 4 // 25% of warning RTT
	
	if tracker.jitterMean <= jitterThreshold {
		return 100
	} else if tracker.jitterMean <= jitterThreshold*2 {
		ratio := float64(tracker.jitterMean) / float64(jitterThreshold)
		return int(100 - (ratio-1.0)*50) // 100 to 50
	} else {
		return 25 // High jitter
	}
}

// calculateTrendScore calculates the trend component of the health score
func (tracker *PathHealthTracker) calculateTrendScore() int {
	switch tracker.healthTrend {
	case HealthTrendStable:
		return 100
	case HealthTrendImproving:
		return 90
	case HealthTrendRecovering:
		return 70
	case HealthTrendDegrading:
		return 30
	default:
		return 50
	}
}

// calculateThroughputScore calculates the throughput component of the health score
func (tracker *PathHealthTracker) calculateThroughputScore() int {
	if tracker.avgThroughput == 0 {
		return 50 // No throughput data
	}
	
	// Simple throughput scoring - could be enhanced with baseline comparison
	if tracker.avgThroughput > 10.0 { // > 10 Mbps
		return 100
	} else if tracker.avgThroughput > 1.0 { // > 1 Mbps
		return 80
	} else if tracker.avgThroughput > 0.1 { // > 100 Kbps
		return 60
	} else {
		return 30
	}
}

// GetMetrics returns the current calculated metrics
func (tracker *PathHealthTracker) GetMetrics() PathHealthMetrics {
	return PathHealthMetrics{
		SmoothedRTT:      tracker.smoothedRTT,
		RTTVariance:      tracker.rttVariance,
		PacketLossRate:   tracker.packetLossRate,
		AverageThroughput: tracker.avgThroughput,
		JitterMean:       tracker.jitterMean,
		HealthTrend:      tracker.healthTrend,
		BaselineRTT:      tracker.baselineRTT,
		BaselineLoss:     tracker.baselineLoss,
		SampleCount:      len(tracker.rttSamples),
	}
}

// PathHealthMetrics contains calculated health metrics
type PathHealthMetrics struct {
	SmoothedRTT       time.Duration `json:"smoothed_rtt"`
	RTTVariance       time.Duration `json:"rtt_variance"`
	PacketLossRate    float64       `json:"packet_loss_rate"`
	AverageThroughput float64       `json:"average_throughput"`
	JitterMean        time.Duration `json:"jitter_mean"`
	HealthTrend       HealthTrend   `json:"health_trend"`
	BaselineRTT       time.Duration `json:"baseline_rtt"`
	BaselineLoss      float64       `json:"baseline_loss"`
	SampleCount       int           `json:"sample_count"`
}

// IsPathDegraded determines if the path is currently degraded based on metrics
func (tracker *PathHealthTracker) IsPathDegraded(thresholds HealthThresholds) bool {
	// Path is degraded if:
	// 1. Health trend is degrading
	// 2. RTT is significantly above baseline
	// 3. Packet loss is above critical threshold
	// 4. Health score is below warning threshold
	
	if tracker.healthTrend == HealthTrendDegrading {
		return true
	}
	
	if tracker.baselineRTT > 0 {
		rttRatio := float64(tracker.smoothedRTT) / float64(tracker.baselineRTT)
		if rttRatio > tracker.degradationThreshold {
			return true
		}
	}
	
	if tracker.packetLossRate > thresholds.PacketLossCriticalThreshold {
		return true
	}
	
	healthScore := tracker.CalculateHealthScore(thresholds)
	if healthScore < thresholds.HealthScoreWarningThreshold {
		return true
	}
	
	return false
}

// Reset resets the tracker state (useful for path recovery)
func (tracker *PathHealthTracker) Reset() {
	tracker.rttSamples = tracker.rttSamples[:0]
	tracker.lossSamples = tracker.lossSamples[:0]
	tracker.throughputSamples = tracker.throughputSamples[:0]
	tracker.jitterSamples = tracker.jitterSamples[:0]
	
	tracker.smoothedRTT = 0
	tracker.rttVariance = 0
	tracker.packetLossRate = 0.0
	tracker.avgThroughput = 0.0
	tracker.jitterMean = 0
	tracker.healthTrend = HealthTrendStable
	
	tracker.baselineRTT = 0
	tracker.baselineLoss = 0.0
	tracker.degradationStart = nil
	tracker.recoveryStart = nil
}