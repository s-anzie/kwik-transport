package stream

import (
	"sync"
	"time"
)

// SecondaryStreamMetrics provides specialized metrics for secondary stream isolation
type SecondaryStreamMetrics struct {
	// Aggregation metrics
	aggregationLatency    *LatencyMetrics
	aggregationThroughput *ThroughputMetrics
	aggregationOverhead   *OverheadMetrics
	
	// Stream metrics
	streamMetrics *StreamCountMetrics
	
	// Efficiency metrics
	efficiencyMetrics *AggregationEfficiencyMetrics
	
	// Error metrics
	errorMetrics *SecondaryStreamErrorMetrics
	
	// Synchronization
	mutex sync.RWMutex
}

// LatencyMetrics tracks latency-related metrics
type LatencyMetrics struct {
	AggregationLatency    time.Duration // Current aggregation latency
	MetadataLatency       time.Duration // Metadata processing latency
	MappingLatency        time.Duration // Stream mapping latency
	AvgAggregationLatency time.Duration // Average aggregation latency
	MaxAggregationLatency time.Duration // Maximum aggregation latency
	MinAggregationLatency time.Duration // Minimum aggregation latency
	LatencySamples        uint64        // Number of latency samples
	mutex                 sync.RWMutex
}

// ThroughputMetrics tracks throughput-related metrics
type ThroughputMetrics struct {
	BytesPerSecond          uint64    // Current bytes per second
	FramesPerSecond         uint64    // Current frames per second
	AggregatedBytesPerSec   uint64    // Aggregated bytes per second
	PeakBytesPerSecond      uint64    // Peak bytes per second
	AvgBytesPerSecond       uint64    // Average bytes per second
	TotalBytesProcessed     uint64    // Total bytes processed
	TotalFramesProcessed    uint64    // Total frames processed
	LastMeasurement         time.Time // Last measurement timestamp
	mutex                   sync.RWMutex
}

// OverheadMetrics tracks overhead introduced by secondary stream processing
type OverheadMetrics struct {
	MetadataOverheadBytes   uint64        // Bytes of metadata overhead
	MetadataOverheadPercent float64       // Percentage of metadata overhead
	ProcessingOverheadTime  time.Duration // Processing overhead time
	MemoryOverheadBytes     uint64        // Memory overhead in bytes
	CPUOverheadPercent      float64       // CPU overhead percentage
	NetworkOverheadBytes    uint64        // Network overhead in bytes
	mutex                   sync.RWMutex
}

// StreamCountMetrics tracks stream-related counts
type StreamCountMetrics struct {
	ActiveSecondaryStreams  int       // Currently active secondary streams
	ActiveKwikStreams       int       // Currently active KWIK streams
	TotalSecondaryStreams   uint64    // Total secondary streams created
	TotalKwikStreams        uint64    // Total KWIK streams created
	ClosedSecondaryStreams  uint64    // Total secondary streams closed
	ClosedKwikStreams       uint64    // Total KWIK streams closed
	PeakSecondaryStreams    int       // Peak number of secondary streams
	PeakKwikStreams         int       // Peak number of KWIK streams
	LastUpdate              time.Time // Last update timestamp
	mutex                   sync.RWMutex
}

// AggregationEfficiencyMetrics tracks efficiency of aggregation process
type AggregationEfficiencyMetrics struct {
	AggregationRatio        float64   // Ratio of aggregated to raw data
	CompressionRatio        float64   // Compression ratio achieved
	ReorderingEfficiency    float64   // Efficiency of reordering operations
	BatchProcessingRatio    float64   // Ratio of batch to individual processing
	ParallelProcessingRatio float64   // Ratio of parallel to sequential processing
	MemoryUtilization       float64   // Memory utilization efficiency
	CPUUtilization          float64   // CPU utilization efficiency
	LastCalculation         time.Time // Last calculation timestamp
	mutex                   sync.RWMutex
}

// SecondaryStreamErrorMetrics tracks error-related metrics
type SecondaryStreamErrorMetrics struct {
	MappingErrors           uint64    // Number of mapping errors
	AggregationErrors       uint64    // Number of aggregation errors
	MetadataErrors          uint64    // Number of metadata errors
	TimeoutErrors           uint64    // Number of timeout errors
	ProtocolViolations      uint64    // Number of protocol violations
	DataCorruptionErrors    uint64    // Number of data corruption errors
	RecoveryAttempts        uint64    // Number of recovery attempts
	SuccessfulRecoveries    uint64    // Number of successful recoveries
	LastError               time.Time // Timestamp of last error
	mutex                   sync.RWMutex
}

// NewSecondaryStreamMetrics creates a new secondary stream metrics instance
func NewSecondaryStreamMetrics() *SecondaryStreamMetrics {
	now := time.Now()
	
	return &SecondaryStreamMetrics{
		aggregationLatency: &LatencyMetrics{
			MinAggregationLatency: time.Hour, // Initialize to high value
		},
		aggregationThroughput: &ThroughputMetrics{
			LastMeasurement: now,
		},
		aggregationOverhead: &OverheadMetrics{},
		streamMetrics: &StreamCountMetrics{
			LastUpdate: now,
		},
		efficiencyMetrics: &AggregationEfficiencyMetrics{
			LastCalculation: now,
		},
		errorMetrics: &SecondaryStreamErrorMetrics{},
	}
}

// RecordAggregationLatency records aggregation latency
func (m *SecondaryStreamMetrics) RecordAggregationLatency(latency time.Duration) {
	m.aggregationLatency.mutex.Lock()
	defer m.aggregationLatency.mutex.Unlock()
	
	m.aggregationLatency.AggregationLatency = latency
	m.aggregationLatency.LatencySamples++
	
	// Update min/max
	if latency > m.aggregationLatency.MaxAggregationLatency {
		m.aggregationLatency.MaxAggregationLatency = latency
	}
	if latency < m.aggregationLatency.MinAggregationLatency {
		m.aggregationLatency.MinAggregationLatency = latency
	}
	
	// Update average (simple moving average)
	if m.aggregationLatency.LatencySamples == 1 {
		m.aggregationLatency.AvgAggregationLatency = latency
	} else {
		// Exponential moving average with alpha = 0.1
		alpha := 0.1
		m.aggregationLatency.AvgAggregationLatency = time.Duration(
			float64(m.aggregationLatency.AvgAggregationLatency)*(1-alpha) + 
			float64(latency)*alpha,
		)
	}
}

// RecordMetadataLatency records metadata processing latency
func (m *SecondaryStreamMetrics) RecordMetadataLatency(latency time.Duration) {
	m.aggregationLatency.mutex.Lock()
	defer m.aggregationLatency.mutex.Unlock()
	
	m.aggregationLatency.MetadataLatency = latency
}

// RecordMappingLatency records stream mapping latency
func (m *SecondaryStreamMetrics) RecordMappingLatency(latency time.Duration) {
	m.aggregationLatency.mutex.Lock()
	defer m.aggregationLatency.mutex.Unlock()
	
	m.aggregationLatency.MappingLatency = latency
}

// RecordThroughput records throughput metrics
func (m *SecondaryStreamMetrics) RecordThroughput(bytes, frames uint64) {
	m.aggregationThroughput.mutex.Lock()
	defer m.aggregationThroughput.mutex.Unlock()
	
	now := time.Now()
	elapsed := now.Sub(m.aggregationThroughput.LastMeasurement)
	
	if elapsed > 0 {
		m.aggregationThroughput.BytesPerSecond = uint64(float64(bytes) / elapsed.Seconds())
		m.aggregationThroughput.FramesPerSecond = uint64(float64(frames) / elapsed.Seconds())
		
		// Update peak
		if m.aggregationThroughput.BytesPerSecond > m.aggregationThroughput.PeakBytesPerSecond {
			m.aggregationThroughput.PeakBytesPerSecond = m.aggregationThroughput.BytesPerSecond
		}
		
		// Update totals
		m.aggregationThroughput.TotalBytesProcessed += bytes
		m.aggregationThroughput.TotalFramesProcessed += frames
		
		// Update average (simple moving average)
		if m.aggregationThroughput.TotalFramesProcessed == frames {
			m.aggregationThroughput.AvgBytesPerSecond = m.aggregationThroughput.BytesPerSecond
		} else {
			alpha := 0.1
			m.aggregationThroughput.AvgBytesPerSecond = uint64(
				float64(m.aggregationThroughput.AvgBytesPerSecond)*(1-alpha) + 
				float64(m.aggregationThroughput.BytesPerSecond)*alpha,
			)
		}
	}
	
	m.aggregationThroughput.LastMeasurement = now
}

// RecordOverhead records overhead metrics
func (m *SecondaryStreamMetrics) RecordOverhead(metadataBytes, memoryBytes, networkBytes uint64, processingTime time.Duration, cpuPercent float64) {
	m.aggregationOverhead.mutex.Lock()
	defer m.aggregationOverhead.mutex.Unlock()
	
	m.aggregationOverhead.MetadataOverheadBytes = metadataBytes
	m.aggregationOverhead.ProcessingOverheadTime = processingTime
	m.aggregationOverhead.MemoryOverheadBytes = memoryBytes
	m.aggregationOverhead.CPUOverheadPercent = cpuPercent
	m.aggregationOverhead.NetworkOverheadBytes = networkBytes
	
	// Calculate metadata overhead percentage
	totalBytes := m.aggregationThroughput.TotalBytesProcessed
	if totalBytes > 0 {
		m.aggregationOverhead.MetadataOverheadPercent = float64(metadataBytes) / float64(totalBytes) * 100
	}
}

// UpdateStreamCounts updates stream count metrics
func (m *SecondaryStreamMetrics) UpdateStreamCounts(activeSecondary, activeKwik int) {
	m.streamMetrics.mutex.Lock()
	defer m.streamMetrics.mutex.Unlock()
	
	m.streamMetrics.ActiveSecondaryStreams = activeSecondary
	m.streamMetrics.ActiveKwikStreams = activeKwik
	m.streamMetrics.LastUpdate = time.Now()
	
	// Update peaks
	if activeSecondary > m.streamMetrics.PeakSecondaryStreams {
		m.streamMetrics.PeakSecondaryStreams = activeSecondary
	}
	if activeKwik > m.streamMetrics.PeakKwikStreams {
		m.streamMetrics.PeakKwikStreams = activeKwik
	}
}

// RecordStreamCreation records stream creation
func (m *SecondaryStreamMetrics) RecordStreamCreation(isSecondary bool) {
	m.streamMetrics.mutex.Lock()
	defer m.streamMetrics.mutex.Unlock()
	
	if isSecondary {
		m.streamMetrics.TotalSecondaryStreams++
	} else {
		m.streamMetrics.TotalKwikStreams++
	}
}

// RecordStreamClosure records stream closure
func (m *SecondaryStreamMetrics) RecordStreamClosure(isSecondary bool) {
	m.streamMetrics.mutex.Lock()
	defer m.streamMetrics.mutex.Unlock()
	
	if isSecondary {
		m.streamMetrics.ClosedSecondaryStreams++
	} else {
		m.streamMetrics.ClosedKwikStreams++
	}
}

// UpdateEfficiencyMetrics updates efficiency metrics
func (m *SecondaryStreamMetrics) UpdateEfficiencyMetrics(aggregationRatio, compressionRatio, reorderingEfficiency, batchRatio, parallelRatio, memoryUtil, cpuUtil float64) {
	m.efficiencyMetrics.mutex.Lock()
	defer m.efficiencyMetrics.mutex.Unlock()
	
	m.efficiencyMetrics.AggregationRatio = aggregationRatio
	m.efficiencyMetrics.CompressionRatio = compressionRatio
	m.efficiencyMetrics.ReorderingEfficiency = reorderingEfficiency
	m.efficiencyMetrics.BatchProcessingRatio = batchRatio
	m.efficiencyMetrics.ParallelProcessingRatio = parallelRatio
	m.efficiencyMetrics.MemoryUtilization = memoryUtil
	m.efficiencyMetrics.CPUUtilization = cpuUtil
	m.efficiencyMetrics.LastCalculation = time.Now()
}

// RecordError records an error occurrence
func (m *SecondaryStreamMetrics) RecordError(errorType SecondaryStreamErrorType) {
	m.errorMetrics.mutex.Lock()
	defer m.errorMetrics.mutex.Unlock()
	
	switch errorType {
	case SecondaryErrorTypeMappingError:
		m.errorMetrics.MappingErrors++
	case SecondaryErrorTypeAggregationError:
		m.errorMetrics.AggregationErrors++
	case SecondaryErrorTypeMetadataError:
		m.errorMetrics.MetadataErrors++
	case SecondaryErrorTypeTimeoutError:
		m.errorMetrics.TimeoutErrors++
	case SecondaryErrorTypeProtocolViolation:
		m.errorMetrics.ProtocolViolations++
	case SecondaryErrorTypeDataCorruption:
		m.errorMetrics.DataCorruptionErrors++
	}
	
	m.errorMetrics.LastError = time.Now()
}

// RecordRecoveryAttempt records a recovery attempt
func (m *SecondaryStreamMetrics) RecordRecoveryAttempt(successful bool) {
	m.errorMetrics.mutex.Lock()
	defer m.errorMetrics.mutex.Unlock()
	
	m.errorMetrics.RecoveryAttempts++
	if successful {
		m.errorMetrics.SuccessfulRecoveries++
	}
}

// SecondaryStreamErrorType represents types of errors that can occur
type SecondaryStreamErrorType int

const (
	SecondaryErrorTypeMappingError SecondaryStreamErrorType = iota
	SecondaryErrorTypeAggregationError
	SecondaryErrorTypeMetadataError
	SecondaryErrorTypeTimeoutError
	SecondaryErrorTypeProtocolViolation
	SecondaryErrorTypeDataCorruption
)

// GetLatencyMetrics returns a copy of latency metrics
func (m *SecondaryStreamMetrics) GetLatencyMetrics() LatencyMetrics {
	m.aggregationLatency.mutex.RLock()
	defer m.aggregationLatency.mutex.RUnlock()
	
	return *m.aggregationLatency
}

// GetThroughputMetrics returns a copy of throughput metrics
func (m *SecondaryStreamMetrics) GetThroughputMetrics() ThroughputMetrics {
	m.aggregationThroughput.mutex.RLock()
	defer m.aggregationThroughput.mutex.RUnlock()
	
	return *m.aggregationThroughput
}

// GetOverheadMetrics returns a copy of overhead metrics
func (m *SecondaryStreamMetrics) GetOverheadMetrics() OverheadMetrics {
	m.aggregationOverhead.mutex.RLock()
	defer m.aggregationOverhead.mutex.RUnlock()
	
	return *m.aggregationOverhead
}

// GetStreamMetrics returns a copy of stream count metrics
func (m *SecondaryStreamMetrics) GetStreamMetrics() StreamCountMetrics {
	m.streamMetrics.mutex.RLock()
	defer m.streamMetrics.mutex.RUnlock()
	
	return *m.streamMetrics
}

// GetEfficiencyMetrics returns a copy of efficiency metrics
func (m *SecondaryStreamMetrics) GetEfficiencyMetrics() AggregationEfficiencyMetrics {
	m.efficiencyMetrics.mutex.RLock()
	defer m.efficiencyMetrics.mutex.RUnlock()
	
	return *m.efficiencyMetrics
}

// GetErrorMetrics returns a copy of error metrics
func (m *SecondaryStreamMetrics) GetErrorMetrics() SecondaryStreamErrorMetrics {
	m.errorMetrics.mutex.RLock()
	defer m.errorMetrics.mutex.RUnlock()
	
	return *m.errorMetrics
}

// GetSummary returns a summary of all metrics
func (m *SecondaryStreamMetrics) GetSummary() *SecondaryStreamMetricsSummary {
	return &SecondaryStreamMetricsSummary{
		Latency:    m.GetLatencyMetrics(),
		Throughput: m.GetThroughputMetrics(),
		Overhead:   m.GetOverheadMetrics(),
		Streams:    m.GetStreamMetrics(),
		Efficiency: m.GetEfficiencyMetrics(),
		Errors:     m.GetErrorMetrics(),
		Timestamp:  time.Now(),
	}
}

// SecondaryStreamMetricsSummary provides a complete summary of all metrics
type SecondaryStreamMetricsSummary struct {
	Latency    LatencyMetrics                `json:"latency"`
	Throughput ThroughputMetrics             `json:"throughput"`
	Overhead   OverheadMetrics               `json:"overhead"`
	Streams    StreamCountMetrics            `json:"streams"`
	Efficiency AggregationEfficiencyMetrics  `json:"efficiency"`
	Errors     SecondaryStreamErrorMetrics   `json:"errors"`
	Timestamp  time.Time                     `json:"timestamp"`
}

// Reset resets all metrics to their initial state
func (m *SecondaryStreamMetrics) Reset() {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	
	now := time.Now()
	
	// Reset all metrics
	m.aggregationLatency = &LatencyMetrics{
		MinAggregationLatency: time.Hour,
	}
	m.aggregationThroughput = &ThroughputMetrics{
		LastMeasurement: now,
	}
	m.aggregationOverhead = &OverheadMetrics{}
	m.streamMetrics = &StreamCountMetrics{
		LastUpdate: now,
	}
	m.efficiencyMetrics = &AggregationEfficiencyMetrics{
		LastCalculation: now,
	}
	m.errorMetrics = &SecondaryStreamErrorMetrics{}
}