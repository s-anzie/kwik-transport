package kwik

import (
	"context"
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"kwik/internal/utils"
)

// LogLevel represents the logging level
type LogLevel int

const (
	LogLevelDebug LogLevel = iota
	LogLevelInfo
	LogLevelWarn
	LogLevelError
)

// Logger interface for KWIK system logging
type Logger interface {
	Debug(msg string, keysAndValues ...interface{})
	Info(msg string, keysAndValues ...interface{})
	Warn(msg string, keysAndValues ...interface{})
	Error(msg string, keysAndValues ...interface{})
	SetLevel(level LogLevel)
	
	// Enhanced logging methods
	DebugWithContext(ctx context.Context, msg string, keysAndValues ...interface{})
	InfoWithContext(ctx context.Context, msg string, keysAndValues ...interface{})
	WarnWithContext(ctx context.Context, msg string, keysAndValues ...interface{})
	ErrorWithContext(ctx context.Context, msg string, keysAndValues ...interface{})
	
	// Error-specific logging
	LogError(err error, msg string, keysAndValues ...interface{})
	LogKwikError(err *utils.KwikError, msg string, keysAndValues ...interface{})
	LogEnhancedError(err *utils.EnhancedKwikError, msg string, keysAndValues ...interface{})
	
	// Component-specific logging
	WithComponent(component string) Logger
	WithSession(sessionID string) Logger
	WithPath(pathID string) Logger
	WithStream(streamID uint64) Logger
	
	// Performance logging
	LogPerformance(operation string, duration time.Duration, keysAndValues ...interface{})
	LogMetrics(metrics map[string]interface{})
}

// SimpleLogger is a basic implementation of the Logger interface
type SimpleLogger struct {
	level     LogLevel
	logger    *log.Logger
	component string
	sessionID string
	pathID    string
	streamID  uint64
	mutex     sync.RWMutex
}

// EnhancedLogger provides structured logging with context and error correlation
type EnhancedLogger struct {
	*SimpleLogger
	errorRecoveryManager *utils.ErrorRecoveryManager
	correlationID        string
	traceEnabled         bool
}

// NewLogger creates a new logger with the specified level
func NewLogger(level LogLevel) Logger {
	return &SimpleLogger{
		level:  level,
		logger: log.New(os.Stdout, "[KWIK] ", log.LstdFlags|log.Lmicroseconds),
	}
}

// Debug logs a debug message
func (l *SimpleLogger) Debug(msg string, keysAndValues ...interface{}) {
	if l.level <= LogLevelDebug {
		l.logWithLevel("DEBUG", msg, keysAndValues...)
	}
}

// Info logs an info message
func (l *SimpleLogger) Info(msg string, keysAndValues ...interface{}) {
	if l.level <= LogLevelInfo {
		l.logWithLevel("INFO", msg, keysAndValues...)
	}
}

// Warn logs a warning message
func (l *SimpleLogger) Warn(msg string, keysAndValues ...interface{}) {
	if l.level <= LogLevelWarn {
		l.logWithLevel("WARN", msg, keysAndValues...)
	}
}

// Error logs an error message
func (l *SimpleLogger) Error(msg string, keysAndValues ...interface{}) {
	if l.level <= LogLevelError {
		l.logWithLevel("ERROR", msg, keysAndValues...)
	}
}

// SetLevel sets the logging level
func (l *SimpleLogger) SetLevel(level LogLevel) {
	l.level = level
}



func (l LogLevel) String() string {
	switch l {
	case LogLevelDebug:
		return "DEBUG"
	case LogLevelInfo:
		return "INFO"
	case LogLevelWarn:
		return "WARN"
	case LogLevelError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}
// Enhanced logging methods with context
func (l *SimpleLogger) DebugWithContext(ctx context.Context, msg string, keysAndValues ...interface{}) {
	if l.level <= LogLevelDebug {
		l.logWithContext(ctx, "DEBUG", msg, keysAndValues...)
	}
}

func (l *SimpleLogger) InfoWithContext(ctx context.Context, msg string, keysAndValues ...interface{}) {
	if l.level <= LogLevelInfo {
		l.logWithContext(ctx, "INFO", msg, keysAndValues...)
	}
}

func (l *SimpleLogger) WarnWithContext(ctx context.Context, msg string, keysAndValues ...interface{}) {
	if l.level <= LogLevelWarn {
		l.logWithContext(ctx, "WARN", msg, keysAndValues...)
	}
}

func (l *SimpleLogger) ErrorWithContext(ctx context.Context, msg string, keysAndValues ...interface{}) {
	if l.level <= LogLevelError {
		l.logWithContext(ctx, "ERROR", msg, keysAndValues...)
	}
}

// Error-specific logging methods
func (l *SimpleLogger) LogError(err error, msg string, keysAndValues ...interface{}) {
	if l.level <= LogLevelError {
		kvs := append(keysAndValues, "error", err.Error())
		l.logWithLevel("ERROR", msg, kvs...)
	}
}

func (l *SimpleLogger) LogKwikError(err *utils.KwikError, msg string, keysAndValues ...interface{}) {
	if l.level <= LogLevelError {
		kvs := append(keysAndValues, 
			"errorCode", err.Code,
			"errorMessage", err.Message,
			"error", err.Error())
		if err.Cause != nil {
			kvs = append(kvs, "cause", err.Cause.Error())
		}
		l.logWithLevel("ERROR", msg, kvs...)
	}
}

func (l *SimpleLogger) LogEnhancedError(err *utils.EnhancedKwikError, msg string, keysAndValues ...interface{}) {
	if l.level <= LogLevelError {
		kvs := append(keysAndValues,
			"errorCode", err.Code,
			"errorMessage", err.Message,
			"severity", err.Severity.String(),
			"component", err.Component,
			"timestamp", err.Timestamp.Format(time.RFC3339),
			"recoverable", err.Recoverable,
			"error", err.Error())
		
		if err.Cause != nil {
			kvs = append(kvs, "cause", err.Cause.Error())
		}
		
		// Add context information
		for key, value := range err.Context {
			kvs = append(kvs, fmt.Sprintf("ctx_%s", key), value)
		}
		
		l.logWithLevel("ERROR", msg, kvs...)
	}
}

// Component-specific logging methods
func (l *SimpleLogger) WithComponent(component string) Logger {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	
	newLogger := &SimpleLogger{
		level:     l.level,
		logger:    l.logger,
		component: component,
		sessionID: l.sessionID,
		pathID:    l.pathID,
		streamID:  l.streamID,
	}
	return newLogger
}

func (l *SimpleLogger) WithSession(sessionID string) Logger {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	
	newLogger := &SimpleLogger{
		level:     l.level,
		logger:    l.logger,
		component: l.component,
		sessionID: sessionID,
		pathID:    l.pathID,
		streamID:  l.streamID,
	}
	return newLogger
}

func (l *SimpleLogger) WithPath(pathID string) Logger {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	
	newLogger := &SimpleLogger{
		level:     l.level,
		logger:    l.logger,
		component: l.component,
		sessionID: l.sessionID,
		pathID:    pathID,
		streamID:  l.streamID,
	}
	return newLogger
}

func (l *SimpleLogger) WithStream(streamID uint64) Logger {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	
	newLogger := &SimpleLogger{
		level:     l.level,
		logger:    l.logger,
		component: l.component,
		sessionID: l.sessionID,
		pathID:    l.pathID,
		streamID:  streamID,
	}
	return newLogger
}

// Performance logging
func (l *SimpleLogger) LogPerformance(operation string, duration time.Duration, keysAndValues ...interface{}) {
	if l.level <= LogLevelInfo {
		kvs := append(keysAndValues,
			"operation", operation,
			"duration_ms", duration.Milliseconds(),
			"duration_us", duration.Microseconds())
		l.logWithLevel("PERF", "Performance measurement", kvs...)
	}
}

func (l *SimpleLogger) LogMetrics(metrics map[string]interface{}) {
	if l.level <= LogLevelInfo {
		var kvs []interface{}
		for key, value := range metrics {
			kvs = append(kvs, key, value)
		}
		l.logWithLevel("METRICS", "System metrics", kvs...)
	}
}

// logWithContext logs a message with context information
func (l *SimpleLogger) logWithContext(ctx context.Context, level, msg string, keysAndValues ...interface{}) {
	// Extract context values if available
	var contextKVs []interface{}
	
	// Add trace information if available
	if traceID := ctx.Value("traceID"); traceID != nil {
		contextKVs = append(contextKVs, "traceID", traceID)
	}
	
	if requestID := ctx.Value("requestID"); requestID != nil {
		contextKVs = append(contextKVs, "requestID", requestID)
	}
	
	// Combine context and provided key-value pairs
	allKVs := append(contextKVs, keysAndValues...)
	
	l.logWithLevel(level, msg, allKVs...)
}

// Enhanced logWithLevel that includes component context
func (l *SimpleLogger) logWithLevel(level, msg string, keysAndValues ...interface{}) {
	l.mutex.RLock()
	component := l.component
	sessionID := l.sessionID
	pathID := l.pathID
	streamID := l.streamID
	l.mutex.RUnlock()
	
	// Build context prefix
	var contextParts []string
	if component != "" {
		contextParts = append(contextParts, fmt.Sprintf("comp=%s", component))
	}
	if sessionID != "" {
		contextParts = append(contextParts, fmt.Sprintf("session=%s", sessionID))
	}
	if pathID != "" {
		contextParts = append(contextParts, fmt.Sprintf("path=%s", pathID))
	}
	if streamID != 0 {
		contextParts = append(contextParts, fmt.Sprintf("stream=%d", streamID))
	}
	
	var contextPrefix string
	if len(contextParts) > 0 {
		contextPrefix = fmt.Sprintf("[%s] ", strings.Join(contextParts, ","))
	}
	
	// Add caller information for debug level
	var callerInfo string
	if level == "DEBUG" || level == "ERROR" {
		if pc, file, line, ok := runtime.Caller(3); ok {
			funcName := runtime.FuncForPC(pc).Name()
			// Extract just the function name
			if idx := strings.LastIndex(funcName, "."); idx != -1 {
				funcName = funcName[idx+1:]
			}
			// Extract just the filename
			if idx := strings.LastIndex(file, "/"); idx != -1 {
				file = file[idx+1:]
			}
			callerInfo = fmt.Sprintf(" [%s:%d:%s]", file, line, funcName)
		}
	}
	
	// Format key-value pairs
	var kvPairs string
	if len(keysAndValues) > 0 {
		kvPairs = " "
		for i := 0; i < len(keysAndValues); i += 2 {
			if i+1 < len(keysAndValues) {
				kvPairs += fmt.Sprintf("%v=%v ", keysAndValues[i], keysAndValues[i+1])
			} else {
				kvPairs += fmt.Sprintf("%v=<missing_value> ", keysAndValues[i])
			}
		}
	}
	
	// Log the message with full context
	l.logger.Printf("[%s] %s%s%s%s", level, contextPrefix, msg, kvPairs, callerInfo)
}

// NewEnhancedLogger creates an enhanced logger with error recovery capabilities
func NewEnhancedLogger(level LogLevel, component string) Logger {
	baseLogger := &SimpleLogger{
		level:     level,
		logger:    log.New(os.Stdout, "[KWIK] ", log.LstdFlags|log.Lmicroseconds),
		component: component,
	}
	
	// Create error recovery manager
	errorRecoveryManager := utils.NewErrorRecoveryManager(baseLogger)
	
	// Register default recovery strategies
	defaultStrategies := utils.GetDefaultRecoveryStrategies()
	for errorCode, strategy := range defaultStrategies {
		errorRecoveryManager.RegisterRecoveryStrategy(errorCode, strategy)
	}
	
	return &EnhancedLogger{
		SimpleLogger:         baseLogger,
		errorRecoveryManager: errorRecoveryManager,
		correlationID:        generateCorrelationID(),
		traceEnabled:         true,
	}
}

// Enhanced logger methods
func (e *EnhancedLogger) LogError(err error, msg string, keysAndValues ...interface{}) {
	// Add correlation ID to error logging
	kvs := append(keysAndValues, "correlationID", e.correlationID)
	e.SimpleLogger.LogError(err, msg, kvs...)
	
	// Attempt error recovery if it's a KWIK error
	if kwikErr, ok := err.(*utils.KwikError); ok {
		e.attemptErrorRecovery(kwikErr)
	}
}

func (e *EnhancedLogger) LogKwikError(err *utils.KwikError, msg string, keysAndValues ...interface{}) {
	// Add correlation ID to error logging
	kvs := append(keysAndValues, "correlationID", e.correlationID)
	e.SimpleLogger.LogKwikError(err, msg, kvs...)
	
	// Attempt error recovery
	e.attemptErrorRecovery(err)
}

func (e *EnhancedLogger) LogEnhancedError(err *utils.EnhancedKwikError, msg string, keysAndValues ...interface{}) {
	// Add correlation ID to error logging
	kvs := append(keysAndValues, "correlationID", e.correlationID)
	e.SimpleLogger.LogEnhancedError(err, msg, kvs...)
	
	// Attempt error recovery if the error is recoverable
	if err.Recoverable {
		e.attemptErrorRecovery(err.KwikError)
	}
}

// attemptErrorRecovery attempts to recover from an error using the recovery manager
func (e *EnhancedLogger) attemptErrorRecovery(err *utils.KwikError) {
	// This is a placeholder for error recovery logic
	// In a real implementation, this would coordinate with the recovery manager
	// to attempt recovery strategies
	e.Info("Error recovery attempted", 
		"errorCode", err.Code, 
		"correlationID", e.correlationID)
}

// generateCorrelationID generates a unique correlation ID for error tracking
func generateCorrelationID() string {
	return fmt.Sprintf("kwik-%d", time.Now().UnixNano())
}

// LoggingMiddleware provides logging middleware for operations
type LoggingMiddleware struct {
	logger Logger
}

// NewLoggingMiddleware creates a new logging middleware
func NewLoggingMiddleware(logger Logger) *LoggingMiddleware {
	return &LoggingMiddleware{logger: logger}
}

// WrapOperation wraps an operation with logging and error handling
func (lm *LoggingMiddleware) WrapOperation(operationName string, operation func() error) error {
	start := time.Now()
	
	lm.logger.Debug("Operation started", "operation", operationName)
	
	err := operation()
	duration := time.Since(start)
	
	if err != nil {
		lm.logger.LogError(err, "Operation failed", 
			"operation", operationName, 
			"duration_ms", duration.Milliseconds())
		return err
	}
	
	lm.logger.LogPerformance(operationName, duration)
	lm.logger.Debug("Operation completed successfully", 
		"operation", operationName, 
		"duration_ms", duration.Milliseconds())
	
	return nil
}

// WrapOperationWithContext wraps an operation with context-aware logging
func (lm *LoggingMiddleware) WrapOperationWithContext(ctx context.Context, operationName string, operation func(context.Context) error) error {
	start := time.Now()
	
	lm.logger.DebugWithContext(ctx, "Operation started", "operation", operationName)
	
	err := operation(ctx)
	duration := time.Since(start)
	
	if err != nil {
		lm.logger.ErrorWithContext(ctx, "Operation failed", 
			"operation", operationName, 
			"duration_ms", duration.Milliseconds(),
			"error", err.Error())
		return err
	}
	
	lm.logger.LogPerformance(operationName, duration)
	lm.logger.DebugWithContext(ctx, "Operation completed successfully", 
		"operation", operationName, 
		"duration_ms", duration.Milliseconds())
	
	return nil
}