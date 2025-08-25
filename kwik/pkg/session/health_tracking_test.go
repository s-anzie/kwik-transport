package session

import (
	"testing"
	"time"
)

func TestPathHealthTracker_RTTTracking(t *testing.T) {
	tracker := NewPathHealthTracker()
	
	// Test initial state
	if tracker.smoothedRTT != 0 {
		t.Errorf("Expected initial smoothed RTT to be 0, got %v", tracker.smoothedRTT)
	}
	
	// Add RTT samples
	rttSamples := []time.Duration{
		50 * time.Millisecond,
		55 * time.Millisecond,
		48 * time.Millisecond,
		52 * time.Millisecond,
		60 * time.Millisecond,
	}
	
	for _, rtt := range rttSamples {
		tracker.AddRTTSample(rtt)
	}
	
	// Check that smoothed RTT is calculated
	if tracker.smoothedRTT == 0 {
		t.Errorf("Expected smoothed RTT to be calculated, got 0")
	}
	
	// Check that baseline RTT is set
	if tracker.baselineRTT == 0 {
		t.Errorf("Expected baseline RTT to be set, got 0")
	}
	
	// Check RTT variance calculation
	if tracker.rttVariance == 0 {
		t.Errorf("Expected RTT variance to be calculated, got 0")
	}
	
	// Verify samples are stored
	if len(tracker.rttSamples) != len(rttSamples) {
		t.Errorf("Expected %d RTT samples, got %d", len(rttSamples), len(tracker.rttSamples))
	}
	
	metrics := tracker.GetMetrics()
	if metrics.SampleCount != len(rttSamples) {
		t.Errorf("Expected sample count %d, got %d", len(rttSamples), metrics.SampleCount)
	}
}

func TestPathHealthTracker_PacketLossTracking(t *testing.T) {
	tracker := NewPathHealthTracker()
	
	// Test initial state
	if tracker.packetLossRate != 0.0 {
		t.Errorf("Expected initial packet loss rate to be 0.0, got %f", tracker.packetLossRate)
	}
	
	// Add packet loss samples: 8 received, 2 lost = 20% loss
	lossSamples := []bool{
		false, false, false, false, // 4 received
		true,                       // 1 lost
		false, false, false,        // 3 received
		true,                       // 1 lost
		false,                      // 1 received
	}
	
	for _, lost := range lossSamples {
		tracker.AddPacketLossSample(lost)
	}
	
	expectedLossRate := 2.0 / 10.0 // 2 lost out of 10 total
	if tracker.packetLossRate < expectedLossRate-0.01 || tracker.packetLossRate > expectedLossRate+0.01 {
		t.Errorf("Expected packet loss rate ~%f, got %f", expectedLossRate, tracker.packetLossRate)
	}
	
	// Verify samples are stored
	if len(tracker.lossSamples) != len(lossSamples) {
		t.Errorf("Expected %d loss samples, got %d", len(lossSamples), len(tracker.lossSamples))
	}
}

func TestPathHealthTracker_ThroughputTracking(t *testing.T) {
	tracker := NewPathHealthTracker()
	
	// Test initial state
	if tracker.avgThroughput != 0.0 {
		t.Errorf("Expected initial average throughput to be 0.0, got %f", tracker.avgThroughput)
	}
	
	// Add throughput samples
	throughputSamples := []float64{10.5, 12.3, 9.8, 11.2, 10.9}
	
	for _, throughput := range throughputSamples {
		tracker.AddThroughputSample(throughput)
	}
	
	// Calculate expected average
	var sum float64
	for _, t := range throughputSamples {
		sum += t
	}
	expectedAvg := sum / float64(len(throughputSamples))
	
	if tracker.avgThroughput < expectedAvg-0.01 || tracker.avgThroughput > expectedAvg+0.01 {
		t.Errorf("Expected average throughput ~%f, got %f", expectedAvg, tracker.avgThroughput)
	}
	
	// Verify samples are stored
	if len(tracker.throughputSamples) != len(throughputSamples) {
		t.Errorf("Expected %d throughput samples, got %d", len(throughputSamples), len(tracker.throughputSamples))
	}
}

func TestPathHealthTracker_JitterCalculation(t *testing.T) {
	tracker := NewPathHealthTracker()
	
	// Add RTT samples with varying jitter
	rttSamples := []time.Duration{
		50 * time.Millisecond,
		55 * time.Millisecond, // +5ms jitter
		48 * time.Millisecond, // -7ms jitter (7ms absolute)
		52 * time.Millisecond, // +4ms jitter
		60 * time.Millisecond, // +8ms jitter
	}
	
	for _, rtt := range rttSamples {
		tracker.AddRTTSample(rtt)
	}
	
	// Jitter should be calculated
	if tracker.jitterMean == 0 {
		t.Errorf("Expected jitter mean to be calculated, got 0")
	}
	
	// Verify jitter samples are stored (should be n-1 samples)
	expectedJitterSamples := len(rttSamples) - 1
	if len(tracker.jitterSamples) != expectedJitterSamples {
		t.Errorf("Expected %d jitter samples, got %d", expectedJitterSamples, len(tracker.jitterSamples))
	}
}

func TestPathHealthTracker_HealthScoreCalculation(t *testing.T) {
	tracker := NewPathHealthTracker()
	thresholds := DefaultHealthThresholds()
	
	// Test initial health score (should be 100 with no data)
	score := tracker.CalculateHealthScore(thresholds)
	if score != 100 {
		t.Errorf("Expected initial health score 100, got %d", score)
	}
	
	// Add good metrics
	goodRTT := 30 * time.Millisecond // Below warning threshold
	for i := 0; i < 20; i++ {
		tracker.AddRTTSample(goodRTT)
		tracker.AddPacketLossSample(false) // No packet loss
		tracker.AddThroughputSample(15.0)  // Good throughput
	}
	
	score = tracker.CalculateHealthScore(thresholds)
	if score < 90 {
		t.Errorf("Expected high health score with good metrics, got %d", score)
	}
	
	// Add bad metrics
	badRTT := 600 * time.Millisecond // Above critical threshold
	for i := 0; i < 10; i++ {
		tracker.AddRTTSample(badRTT)
		tracker.AddPacketLossSample(true) // High packet loss
		tracker.AddThroughputSample(0.1)  // Low throughput
	}
	
	score = tracker.CalculateHealthScore(thresholds)
	if score > 70 {
		t.Errorf("Expected low health score with bad metrics, got %d", score)
	}
}

func TestPathHealthTracker_HealthTrendDetection(t *testing.T) {
	tracker := NewPathHealthTracker()
	
	// Initial trend should be stable
	if tracker.healthTrend != HealthTrendStable {
		t.Errorf("Expected initial health trend to be stable, got %v", tracker.healthTrend)
	}
	
	// Add baseline samples (good RTT)
	baselineRTT := 50 * time.Millisecond
	for i := 0; i < 15; i++ {
		tracker.AddRTTSample(baselineRTT)
		tracker.AddPacketLossSample(false)
	}
	
	// Should still be stable
	if tracker.healthTrend != HealthTrendStable {
		t.Errorf("Expected health trend to remain stable after baseline, got %v", tracker.healthTrend)
	}
	
	// Add degraded samples (high RTT)
	degradedRTT := 150 * time.Millisecond // 3x baseline
	for i := 0; i < 10; i++ {
		tracker.AddRTTSample(degradedRTT)
		tracker.AddPacketLossSample(true) // Add packet loss
	}
	
	// Should detect degradation
	if tracker.healthTrend != HealthTrendDegrading {
		t.Errorf("Expected health trend to be degrading, got %v", tracker.healthTrend)
	}
	
	// Add recovery samples (back to good RTT and no loss)
	// Need to add enough good samples to bring loss rate down
	for i := 0; i < 50; i++ {
		tracker.AddRTTSample(baselineRTT)
		tracker.AddPacketLossSample(false) // No packet loss to recover
	}
	
	// Should detect recovery or improving trend
	if tracker.healthTrend != HealthTrendRecovering && tracker.healthTrend != HealthTrendImproving {
		t.Errorf("Expected health trend to be recovering or improving, got %v", tracker.healthTrend)
	}
}

func TestPathHealthTracker_PathDegradationDetection(t *testing.T) {
	tracker := NewPathHealthTracker()
	thresholds := DefaultHealthThresholds()
	
	// Initially should not be degraded
	if tracker.IsPathDegraded(thresholds) {
		t.Errorf("Expected path not to be degraded initially")
	}
	
	// Add baseline samples
	baselineRTT := 50 * time.Millisecond
	for i := 0; i < 15; i++ {
		tracker.AddRTTSample(baselineRTT)
		tracker.AddPacketLossSample(false)
	}
	
	// Should still not be degraded
	if tracker.IsPathDegraded(thresholds) {
		t.Errorf("Expected path not to be degraded with good baseline")
	}
	
	// Add high packet loss
	for i := 0; i < 20; i++ {
		tracker.AddRTTSample(baselineRTT)
		tracker.AddPacketLossSample(true) // 100% loss
	}
	
	// Should be degraded due to high packet loss
	if !tracker.IsPathDegraded(thresholds) {
		t.Errorf("Expected path to be degraded with high packet loss")
	}
}

func TestPathHealthTracker_WindowSizeLimit(t *testing.T) {
	tracker := NewPathHealthTracker()
	
	// Add more samples than window size
	windowSize := tracker.windowSize
	for i := 0; i < windowSize+50; i++ {
		tracker.AddRTTSample(time.Duration(i) * time.Millisecond)
		tracker.AddThroughputSample(float64(i))
	}
	
	// Should not exceed window size
	if len(tracker.rttSamples) > windowSize {
		t.Errorf("RTT samples exceeded window size: %d > %d", len(tracker.rttSamples), windowSize)
	}
	
	if len(tracker.throughputSamples) > windowSize {
		t.Errorf("Throughput samples exceeded window size: %d > %d", len(tracker.throughputSamples), windowSize)
	}
	
	// Add more loss samples than loss window size
	lossWindowSize := tracker.lossWindowSize
	for i := 0; i < lossWindowSize+20; i++ {
		tracker.AddPacketLossSample(i%2 == 0) // Alternating loss
	}
	
	// Should not exceed loss window size
	if len(tracker.lossSamples) > lossWindowSize {
		t.Errorf("Loss samples exceeded window size: %d > %d", len(tracker.lossSamples), lossWindowSize)
	}
}

func TestPathHealthTracker_Reset(t *testing.T) {
	tracker := NewPathHealthTracker()
	
	// Add some data
	for i := 0; i < 10; i++ {
		tracker.AddRTTSample(50 * time.Millisecond)
		tracker.AddPacketLossSample(false)
		tracker.AddThroughputSample(10.0)
	}
	
	// Verify data exists
	if len(tracker.rttSamples) == 0 {
		t.Errorf("Expected RTT samples before reset")
	}
	if tracker.smoothedRTT == 0 {
		t.Errorf("Expected smoothed RTT before reset")
	}
	
	// Reset tracker
	tracker.Reset()
	
	// Verify data is cleared
	if len(tracker.rttSamples) != 0 {
		t.Errorf("Expected RTT samples to be cleared after reset, got %d", len(tracker.rttSamples))
	}
	if len(tracker.lossSamples) != 0 {
		t.Errorf("Expected loss samples to be cleared after reset, got %d", len(tracker.lossSamples))
	}
	if len(tracker.throughputSamples) != 0 {
		t.Errorf("Expected throughput samples to be cleared after reset, got %d", len(tracker.throughputSamples))
	}
	if tracker.smoothedRTT != 0 {
		t.Errorf("Expected smoothed RTT to be reset to 0, got %v", tracker.smoothedRTT)
	}
	if tracker.packetLossRate != 0.0 {
		t.Errorf("Expected packet loss rate to be reset to 0.0, got %f", tracker.packetLossRate)
	}
	if tracker.healthTrend != HealthTrendStable {
		t.Errorf("Expected health trend to be reset to stable, got %v", tracker.healthTrend)
	}
}

func TestHealthTrend_String(t *testing.T) {
	testCases := []struct {
		trend    HealthTrend
		expected string
	}{
		{HealthTrendStable, "STABLE"},
		{HealthTrendImproving, "IMPROVING"},
		{HealthTrendDegrading, "DEGRADING"},
		{HealthTrendRecovering, "RECOVERING"},
		{HealthTrend(999), "UNKNOWN"},
	}
	
	for _, tc := range testCases {
		result := tc.trend.String()
		if result != tc.expected {
			t.Errorf("Expected %s, got %s for trend %d", tc.expected, result, int(tc.trend))
		}
	}
}

func TestPathHealthTracker_GetMetrics(t *testing.T) {
	tracker := NewPathHealthTracker()
	
	// Add some sample data
	rtt := 50 * time.Millisecond
	throughput := 10.5
	
	for i := 0; i < 5; i++ {
		tracker.AddRTTSample(rtt)
		tracker.AddPacketLossSample(false)
		tracker.AddThroughputSample(throughput)
	}
	
	metrics := tracker.GetMetrics()
	
	// Verify metrics are populated
	if metrics.SmoothedRTT == 0 {
		t.Errorf("Expected smoothed RTT in metrics, got 0")
	}
	if metrics.PacketLossRate != 0.0 {
		t.Errorf("Expected packet loss rate 0.0 in metrics, got %f", metrics.PacketLossRate)
	}
	if metrics.AverageThroughput != throughput {
		t.Errorf("Expected average throughput %f in metrics, got %f", throughput, metrics.AverageThroughput)
	}
	if metrics.HealthTrend != HealthTrendStable {
		t.Errorf("Expected health trend stable in metrics, got %v", metrics.HealthTrend)
	}
	if metrics.SampleCount != 5 {
		t.Errorf("Expected sample count 5 in metrics, got %d", metrics.SampleCount)
	}
}

func TestPathHealthTracker_RTTScoreCalculation(t *testing.T) {
	tracker := NewPathHealthTracker()
	thresholds := DefaultHealthThresholds()
	
	// Test excellent RTT (below warning threshold)
	tracker.smoothedRTT = 50 * time.Millisecond // Below 100ms warning
	score := tracker.calculateRTTScore(thresholds)
	if score != 100 {
		t.Errorf("Expected RTT score 100 for excellent RTT, got %d", score)
	}
	
	// Test warning RTT (at warning threshold)
	tracker.smoothedRTT = thresholds.RTTWarningThreshold
	score = tracker.calculateRTTScore(thresholds)
	if score != 100 {
		t.Errorf("Expected RTT score 100 at warning threshold, got %d", score)
	}
	
	// Test critical RTT (at critical threshold)
	tracker.smoothedRTT = thresholds.RTTCriticalThreshold
	score = tracker.calculateRTTScore(thresholds)
	if score != 50 {
		t.Errorf("Expected RTT score 50 at critical threshold, got %d", score)
	}
	
	// Test very high RTT (above critical threshold)
	tracker.smoothedRTT = thresholds.RTTCriticalThreshold * 3
	score = tracker.calculateRTTScore(thresholds)
	if score != 0 {
		t.Errorf("Expected RTT score 0 for very high RTT, got %d", score)
	}
}

func TestPathHealthTracker_LossScoreCalculation(t *testing.T) {
	tracker := NewPathHealthTracker()
	thresholds := DefaultHealthThresholds()
	
	// Test no packet loss
	tracker.packetLossRate = 0.0
	score := tracker.calculateLossScore(thresholds)
	if score != 100 {
		t.Errorf("Expected loss score 100 for no loss, got %d", score)
	}
	
	// Test warning level loss
	tracker.packetLossRate = thresholds.PacketLossWarningThreshold
	score = tracker.calculateLossScore(thresholds)
	if score != 100 {
		t.Errorf("Expected loss score 100 at warning threshold, got %d", score)
	}
	
	// Test critical level loss
	tracker.packetLossRate = thresholds.PacketLossCriticalThreshold
	score = tracker.calculateLossScore(thresholds)
	if score != 50 {
		t.Errorf("Expected loss score 50 at critical threshold, got %d", score)
	}
	
	// Test very high loss
	tracker.packetLossRate = 0.25 // 25% loss
	score = tracker.calculateLossScore(thresholds)
	if score != 0 {
		t.Errorf("Expected loss score 0 for very high loss, got %d", score)
	}
}