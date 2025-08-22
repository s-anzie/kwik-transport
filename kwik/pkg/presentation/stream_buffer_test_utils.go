package presentation

import (
	"testing"
	"time"
)

// BenchmarkLogger is a no-op logger for benchmarks
type BenchmarkLogger struct {
	b *testing.B
}

// Debug logs a debug message (no-op for benchmarks)
func (l *BenchmarkLogger) Debug(msg string, keysAndValues ...interface{}) {
	// No-op for benchmarks
}

// Info logs an info message (no-op for benchmarks)
func (l *BenchmarkLogger) Info(msg string, keysAndValues ...interface{}) {
	// No-op for benchmarks
}

// Warn logs a warning message (no-op for benchmarks)
func (l *BenchmarkLogger) Warn(msg string, keysAndValues ...interface{}) {
	// No-op for benchmarks
}

// Error logs an error message (no-op for benchmarks)
func (l *BenchmarkLogger) Error(msg string, keysAndValues ...interface{}) {
	// No-op for benchmarks
}

// TestLogger is a simple logger for testing
type TestLogger struct {
	t *testing.T
}

// Debug logs a debug message
func (l *TestLogger) Debug(msg string, keysAndValues ...interface{}) {
	l.t.Helper()
	l.t.Logf("DEBUG: %s %v", msg, keysAndValues)
}

// Info logs an info message
func (l *TestLogger) Info(msg string, keysAndValues ...interface{}) {
	l.t.Helper()
	l.t.Logf("INFO: %s %v", msg, keysAndValues)
}

// Warn logs a warning message
func (l *TestLogger) Warn(msg string, keysAndValues ...interface{}) {
	l.t.Helper()
	l.t.Logf("WARN: %s %v", msg, keysAndValues)
}

// Error logs an error message
func (l *TestLogger) Error(msg string, keysAndValues ...interface{}) {
	l.t.Helper()
	l.t.Logf("ERROR: %s %v", msg, keysAndValues)
}

// NewBenchmarkStreamBuffer creates a new StreamBuffer for benchmarks
func NewBenchmarkStreamBuffer(b *testing.B, streamID uint64) *StreamBufferImpl {
	// Create a no-op logger for benchmarks
	logger := &BenchmarkLogger{b: b}
	
	config := &StreamBufferConfig{
		StreamID:         streamID,
		BufferSize:       1024 * 1024, // 1MB default
		Priority:         StreamPriorityNormal,
		GapTimeout:       100 * time.Millisecond,
		EnableMetrics:    false, // Disable metrics for benchmarks
		EnableDebugLogging: false, // Disable debug logging for benchmarks
		Logger:           logger,
	}

	metadata := &StreamMetadata{
		StreamID:   streamID,
		StreamType: StreamTypeData,
		Priority:   StreamPriorityNormal,
		CreatedAt:  time.Now(),
	}

	return NewStreamBuffer(streamID, metadata, config)
}

// NewTestStreamBuffer creates a new StreamBuffer with test configuration
func NewTestStreamBuffer(t *testing.T, streamID uint64) *StreamBufferImpl {
	config := &StreamBufferConfig{
		StreamID:          streamID,
		BufferSize:        1024,
		Priority:         StreamPriorityNormal,
		GapTimeout:       100 * time.Millisecond,
		EnableMetrics:    true,
		EnableDebugLogging: true,
		Logger:           &TestLogger{t: t},
	}

	metadata := &StreamMetadata{
		StreamID:   streamID,
		StreamType: StreamTypeData,
		Priority:   StreamPriorityNormal,
		CreatedAt:  time.Now(),
	}

	return NewStreamBuffer(streamID, metadata, config)
}
