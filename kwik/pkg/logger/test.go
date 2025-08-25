package logger

import (
	"context"
	"kwik/internal/utils"

	"sync"
	"time"
)

// Mock logger for testing
type MockLogger struct {
	logs []string
	mu   sync.Mutex
}

func (m *MockLogger) Debug(msg string, keysAndValues ...interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logs = append(m.logs, "DEBUG: "+msg)
}

func (m *MockLogger) Info(msg string, keysAndValues ...interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logs = append(m.logs, "INFO: "+msg)
}

func (m *MockLogger) Warn(msg string, keysAndValues ...interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logs = append(m.logs, "WARN: "+msg)
}

func (m *MockLogger) Error(msg string, keysAndValues ...interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logs = append(m.logs, "ERROR: "+msg)
}

func (m *MockLogger) Critical(msg string, keysAndValues ...interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logs = append(m.logs, "CRITICAL: "+msg)
}

func (m *MockLogger) DebugWithContext(context.Context, string, ...interface{})          {}
func (m *MockLogger) InfoWithContext(context.Context, string, ...interface{})           {}
func (m *MockLogger) WarnWithContext(context.Context, string, ...interface{})           {}
func (m *MockLogger) ErrorWithContext(context.Context, string, ...interface{})          {}
func (m *MockLogger) LogEnhancedError(*utils.EnhancedKwikError, string, ...interface{}) {}
func (m *MockLogger) LogError(error, string, ...interface{})                            {}
func (m *MockLogger) LogKwikError(*utils.KwikError, string, ...interface{})             {}
func (m *MockLogger) LogMetrics(map[string]interface{})                                 {}
func (m *MockLogger) LogPerformance(string, time.Duration, ...interface{})              {}
func (m *MockLogger) SetLevel(level LogLevel)                                           {}
func (m *MockLogger) WithComponent(name string) Logger {
	return nil
}
func (m *MockLogger) WithPath(name string) Logger {
	return nil
}
func (m *MockLogger) WithSession(name string) Logger {
	return nil
}
func (m *MockLogger) WithStream(uint64) Logger {
	return nil
}
