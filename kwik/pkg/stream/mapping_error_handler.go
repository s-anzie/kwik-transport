package stream

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// KwikLoggerAdapter adapts the central kwik.Logger to this package's Logger interface
type KwikLoggerAdapter struct {
	base interface {
		Debug(string, ...interface{})
		Info(string, ...interface{})
		Warn(string, ...interface{})
		Error(string, ...interface{})
	}
}

func (a *KwikLoggerAdapter) Debug(msg string, args ...interface{}) {
	a.base.Debug(fmt.Sprintf(msg, args...))
}
func (a *KwikLoggerAdapter) Info(msg string, args ...interface{}) {
	a.base.Info(fmt.Sprintf(msg, args...))
}
func (a *KwikLoggerAdapter) Warn(msg string, args ...interface{}) {
	a.base.Warn(fmt.Sprintf(msg, args...))
}
func (a *KwikLoggerAdapter) Error(msg string, args ...interface{}) {
	a.base.Error(fmt.Sprintf(msg, args...))
}
func (a *KwikLoggerAdapter) Critical(msg string, args ...interface{}) {
	// Map Critical to Error level for now
	a.base.Error(fmt.Sprintf("CRITICAL: "+msg, args...))
}

// MappingErrorHandler handles errors specific to stream mapping operations
type MappingErrorHandler struct {
	// Error handling callbacks
	onMappingConflict      func(secondaryStreamID, kwikStreamID uint64, pathID string) error
	onInvalidMapping       func(secondaryStreamID uint64, reason string) error
	onMappingTimeout       func(secondaryStreamID uint64) error
	onMappingLimitExceeded func(currentCount, maxCount int) error

	// Retry policy
	retryPolicy *MappingRetryPolicy

	// Statistics
	stats *MappingErrorStats

	// Configuration
	config *MappingErrorConfig

	// Synchronization
	mutex sync.RWMutex

	// Logger
	logger Logger
}

// MappingRetryPolicy defines the retry behavior for mapping operations
type MappingRetryPolicy struct {
	MaxRetries      int             // Maximum number of retry attempts
	BackoffStrategy BackoffStrategy // Backoff strategy for retries
	TimeoutPerRetry time.Duration   // Timeout for each retry attempt
	RetryableErrors []string        // List of error codes that should be retried
	MaxBackoffTime  time.Duration   // Maximum backoff time between retries
	JitterEnabled   bool            // Whether to add jitter to backoff times
}

// BackoffStrategy defines different backoff strategies for retries
type BackoffStrategy int

const (
	BackoffLinear BackoffStrategy = iota
	BackoffExponential
	BackoffFixed
	BackoffCustom
)

// MappingErrorConfig contains configuration for the error handler
type MappingErrorConfig struct {
	EnableRetries        bool          // Whether to enable automatic retries
	EnableErrorRecovery  bool          // Whether to enable error recovery mechanisms
	ErrorReportingLevel  ErrorLevel    // Level of error reporting
	MaxErrorHistory      int           // Maximum number of errors to keep in history
	ErrorCleanupInterval time.Duration // Interval for cleaning up old errors
	EnableMetrics        bool          // Whether to collect error metrics
}

// ErrorLevel defines the level of error reporting
type ErrorLevel int

const (
	ErrorLevelNone ErrorLevel = iota
	ErrorLevelCritical
	ErrorLevelError
	ErrorLevelWarning
	ErrorLevelInfo
	ErrorLevelDebug
)

// MappingErrorStats contains statistics about mapping errors
type MappingErrorStats struct {
	TotalErrors        int64                 // Total number of errors encountered
	ErrorsByType       map[string]int64      // Count of errors by type
	RetriesAttempted   int64                 // Total number of retry attempts
	RetriesSuccessful  int64                 // Number of successful retries
	RecoveryAttempts   int64                 // Number of recovery attempts
	RecoverySuccessful int64                 // Number of successful recoveries
	LastErrorTime      time.Time             // Time of the last error
	ErrorHistory       []*MappingErrorRecord // History of recent errors
	mutex              sync.RWMutex          // Synchronization for stats
}

// MappingErrorRecord represents a single error occurrence
type MappingErrorRecord struct {
	Timestamp         time.Time // When the error occurred
	ErrorCode         string    // Error code
	ErrorMessage      string    // Error message
	SecondaryStreamID uint64    // Associated secondary stream ID
	KwikStreamID      uint64    // Associated KWIK stream ID
	PathID            string    // Associated path ID
	RetryAttempts     int       // Number of retry attempts for this error
	Resolved          bool      // Whether the error was resolved
	ResolutionMethod  string    // How the error was resolved
}

// Logger interface for error logging
type Logger interface {
	Debug(msg string, args ...interface{})
	Info(msg string, args ...interface{})
	Warn(msg string, args ...interface{})
	Error(msg string, args ...interface{})
	Critical(msg string, args ...interface{})
}

// DefaultLogger provides a simple logger implementation
type DefaultLogger struct{}

func (l *DefaultLogger) Debug(msg string, args ...interface{}) { log.Printf("[DEBUG] "+msg, args...) }
func (l *DefaultLogger) Info(msg string, args ...interface{})  { log.Printf("[INFO] "+msg, args...) }
func (l *DefaultLogger) Warn(msg string, args ...interface{})  { log.Printf("[WARN] "+msg, args...) }
func (l *DefaultLogger) Error(msg string, args ...interface{}) { log.Printf("[ERROR] "+msg, args...) }
func (l *DefaultLogger) Critical(msg string, args ...interface{}) {
	log.Printf("[CRITICAL] "+msg, args...)
}

// NewMappingErrorHandler creates a new mapping error handler
func NewMappingErrorHandler(config *MappingErrorConfig) *MappingErrorHandler {
	if config == nil {
		config = &MappingErrorConfig{
			EnableRetries:        true,
			EnableErrorRecovery:  true,
			ErrorReportingLevel:  ErrorLevelError,
			MaxErrorHistory:      100,
			ErrorCleanupInterval: 1 * time.Hour,
			EnableMetrics:        true,
		}
	}

	retryPolicy := &MappingRetryPolicy{
		MaxRetries:      3,
		BackoffStrategy: BackoffExponential,
		TimeoutPerRetry: 5 * time.Second,
		RetryableErrors: []string{
			ErrMappingTimeout,
			ErrMappingConflict,
		},
		MaxBackoffTime: 30 * time.Second,
		JitterEnabled:  true,
	}

	stats := &MappingErrorStats{
		ErrorsByType: make(map[string]int64),
		ErrorHistory: make([]*MappingErrorRecord, 0, config.MaxErrorHistory),
	}

	handler := &MappingErrorHandler{
		retryPolicy: retryPolicy,
		stats:       stats,
		config:      config,
		logger:      &DefaultLogger{},
	}

	// Set default error handlers
	handler.setDefaultErrorHandlers()

	return handler
}

// SetLogger sets a custom logger for the error handler
func (h *MappingErrorHandler) SetLogger(logger Logger) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.logger = logger
}

// SetRetryPolicy sets a custom retry policy
func (h *MappingErrorHandler) SetRetryPolicy(policy *MappingRetryPolicy) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.retryPolicy = policy
}

// SetMappingConflictHandler sets a custom handler for mapping conflicts
func (h *MappingErrorHandler) SetMappingConflictHandler(handler func(secondaryStreamID, kwikStreamID uint64, pathID string) error) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.onMappingConflict = handler
}

// SetInvalidMappingHandler sets a custom handler for invalid mappings
func (h *MappingErrorHandler) SetInvalidMappingHandler(handler func(secondaryStreamID uint64, reason string) error) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.onInvalidMapping = handler
}

// SetMappingTimeoutHandler sets a custom handler for mapping timeouts
func (h *MappingErrorHandler) SetMappingTimeoutHandler(handler func(secondaryStreamID uint64) error) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.onMappingTimeout = handler
}

// SetMappingLimitExceededHandler sets a custom handler for mapping limit exceeded
func (h *MappingErrorHandler) SetMappingLimitExceededHandler(handler func(currentCount, maxCount int) error) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.onMappingLimitExceeded = handler
}

// HandleMappingError handles a mapping error with appropriate strategy
func (h *MappingErrorHandler) HandleMappingError(err error, context *MappingErrorContext) error {
	if err == nil {
		return nil
	}

	// Record the error
	h.recordError(err, context)

	// Check if this is a MappingError
	mappingErr, ok := err.(*MappingError)
	if !ok {
		// Generic error handling
		return h.handleGenericError(err, context)
	}

	// Handle specific mapping error types
	switch mappingErr.Code {
	case ErrMappingConflict:
		return h.handleMappingConflict(mappingErr, context)
	case ErrMappingInvalid:
		return h.handleInvalidMapping(mappingErr, context)
	case ErrMappingTimeout:
		return h.handleMappingTimeout(mappingErr, context)
	case ErrMappingLimitExceeded:
		return h.handleMappingLimitExceeded(mappingErr, context)
	case ErrMappingNotFound:
		return h.handleMappingNotFound(mappingErr, context)
	case ErrMappingAlreadyExists:
		return h.handleMappingAlreadyExists(mappingErr, context)
	default:
		return h.handleGenericError(err, context)
	}
}

// MappingErrorContext provides context for error handling
type MappingErrorContext struct {
	SecondaryStreamID uint64                 // Secondary stream ID involved in the error
	KwikStreamID      uint64                 // KWIK stream ID involved in the error
	PathID            string                 // Path ID where the error occurred
	Operation         string                 // Operation that caused the error
	Timestamp         time.Time              // When the error occurred
	RetryCount        int                    // Number of retries already attempted
	Metadata          map[string]interface{} // Additional context metadata
}

// handleMappingConflict handles mapping conflict errors
func (h *MappingErrorHandler) handleMappingConflict(err *MappingError, context *MappingErrorContext) error {
	h.logger.Warn("Mapping conflict detected: %s", err.Message)

	// Try custom handler first
	if h.onMappingConflict != nil {
		if handlerErr := h.onMappingConflict(err.SecondaryStreamID, err.KwikStreamID, context.PathID); handlerErr == nil {
			h.recordResolution(err, context, "custom_handler")
			return nil
		}
	}

	// Default conflict resolution: retry with backoff
	if h.config.EnableRetries && context.RetryCount < h.retryPolicy.MaxRetries {
		return h.retryOperation(err, context)
	}

	// If retries exhausted, log and return error
	h.logger.Error("Mapping conflict could not be resolved after %d retries", context.RetryCount)
	return err
}

// handleInvalidMapping handles invalid mapping errors
func (h *MappingErrorHandler) handleInvalidMapping(err *MappingError, context *MappingErrorContext) error {
	h.logger.Error("Invalid mapping detected: %s", err.Message)

	// Try custom handler first
	if h.onInvalidMapping != nil {
		if handlerErr := h.onInvalidMapping(err.SecondaryStreamID, err.Message); handlerErr == nil {
			h.recordResolution(err, context, "custom_handler")
			return nil
		}
	}

	// Invalid mappings are generally not retryable
	h.logger.Error("Invalid mapping cannot be recovered: %s", err.Message)
	return err
}

// handleMappingTimeout handles mapping timeout errors
func (h *MappingErrorHandler) handleMappingTimeout(err *MappingError, context *MappingErrorContext) error {
	h.logger.Warn("Mapping timeout detected: %s", err.Message)

	// Try custom handler first
	if h.onMappingTimeout != nil {
		if handlerErr := h.onMappingTimeout(err.SecondaryStreamID); handlerErr == nil {
			h.recordResolution(err, context, "custom_handler")
			return nil
		}
	}

	// Timeouts are retryable
	if h.config.EnableRetries && context.RetryCount < h.retryPolicy.MaxRetries {
		return h.retryOperation(err, context)
	}

	h.logger.Error("Mapping timeout could not be resolved after %d retries", context.RetryCount)
	return err
}

// handleMappingLimitExceeded handles mapping limit exceeded errors
func (h *MappingErrorHandler) handleMappingLimitExceeded(err *MappingError, context *MappingErrorContext) error {
	h.logger.Warn("Mapping limit exceeded: %s", err.Message)

	// Try custom handler first
	if h.onMappingLimitExceeded != nil {
		// Extract current and max counts from context if available
		currentCount := 0
		maxCount := 0
		if context.Metadata != nil {
			if c, ok := context.Metadata["current_count"].(int); ok {
				currentCount = c
			}
			if m, ok := context.Metadata["max_count"].(int); ok {
				maxCount = m
			}
		}

		if handlerErr := h.onMappingLimitExceeded(currentCount, maxCount); handlerErr == nil {
			h.recordResolution(err, context, "custom_handler")
			return nil
		}
	}

	// Limit exceeded errors are generally not retryable without cleanup
	h.logger.Error("Mapping limit exceeded and cannot be resolved automatically")
	return err
}

// handleMappingNotFound handles mapping not found errors
func (h *MappingErrorHandler) handleMappingNotFound(err *MappingError, context *MappingErrorContext) error {
	h.logger.Debug("Mapping not found: %s", err.Message)

	// Not found errors are usually not critical and not retryable
	return err
}

// handleMappingAlreadyExists handles mapping already exists errors
func (h *MappingErrorHandler) handleMappingAlreadyExists(err *MappingError, context *MappingErrorContext) error {
	h.logger.Debug("Mapping already exists: %s", err.Message)

	// Already exists errors might be recoverable by updating instead of creating
	if h.config.EnableErrorRecovery {
		h.logger.Info("Attempting to recover from 'already exists' error by updating mapping")
		h.recordResolution(err, context, "update_instead_of_create")
		return nil // Indicate that the caller should try updating instead
	}

	return err
}

// handleGenericError handles non-mapping-specific errors
func (h *MappingErrorHandler) handleGenericError(err error, context *MappingErrorContext) error {
	h.logger.Error("Generic mapping error: %s", err.Error())

	// Generic errors might be retryable depending on configuration
	if h.config.EnableRetries && context.RetryCount < h.retryPolicy.MaxRetries {
		return h.retryOperation(err, context)
	}

	return err
}

// retryOperation implements the retry logic with backoff
func (h *MappingErrorHandler) retryOperation(err error, context *MappingErrorContext) error {
	if !h.shouldRetry(err) {
		return err
	}

	// Calculate backoff time
	backoffTime := h.calculateBackoff(context.RetryCount)

	h.logger.Info("Retrying mapping operation after %v (attempt %d/%d)",
		backoffTime, context.RetryCount+1, h.retryPolicy.MaxRetries)

	// Update statistics
	h.stats.mutex.Lock()
	h.stats.RetriesAttempted++
	h.stats.mutex.Unlock()

	// Wait for backoff period
	time.Sleep(backoffTime)

	// Return a special error indicating retry should be attempted
	return &RetryableError{
		OriginalError: err,
		RetryCount:    context.RetryCount + 1,
		BackoffTime:   backoffTime,
	}
}

// shouldRetry determines if an error should be retried
func (h *MappingErrorHandler) shouldRetry(err error) bool {
	if !h.config.EnableRetries {
		return false
	}

	// Check if error type is in retryable list
	var errorCode string
	if mappingErr, ok := err.(*MappingError); ok {
		errorCode = mappingErr.Code
	} else {
		errorCode = err.Error()
	}

	for _, retryableError := range h.retryPolicy.RetryableErrors {
		if errorCode == retryableError {
			return true
		}
	}

	return false
}

// calculateBackoff calculates the backoff time for a retry attempt
func (h *MappingErrorHandler) calculateBackoff(retryCount int) time.Duration {
	var backoff time.Duration

	switch h.retryPolicy.BackoffStrategy {
	case BackoffLinear:
		backoff = time.Duration(retryCount+1) * h.retryPolicy.TimeoutPerRetry
	case BackoffExponential:
		backoff = time.Duration(1<<uint(retryCount)) * h.retryPolicy.TimeoutPerRetry
	case BackoffFixed:
		backoff = h.retryPolicy.TimeoutPerRetry
	default:
		backoff = h.retryPolicy.TimeoutPerRetry
	}

	// Apply maximum backoff limit
	if backoff > h.retryPolicy.MaxBackoffTime {
		backoff = h.retryPolicy.MaxBackoffTime
	}

	// Add jitter if enabled
	if h.retryPolicy.JitterEnabled {
		jitter := time.Duration(float64(backoff) * 0.1 * (2.0*float64(time.Now().UnixNano()%1000)/1000.0 - 1.0))
		backoff += jitter
	}

	return backoff
}

// recordError records an error in the statistics and history
func (h *MappingErrorHandler) recordError(err error, context *MappingErrorContext) {
	if !h.config.EnableMetrics {
		return
	}

	h.stats.mutex.Lock()
	defer h.stats.mutex.Unlock()

	h.stats.TotalErrors++
	h.stats.LastErrorTime = time.Now()

	// Record error by type
	var errorCode string
	if mappingErr, ok := err.(*MappingError); ok {
		errorCode = mappingErr.Code
	} else {
		errorCode = "GENERIC_ERROR"
	}
	h.stats.ErrorsByType[errorCode]++

	// Add to error history
	record := &MappingErrorRecord{
		Timestamp:         time.Now(),
		ErrorCode:         errorCode,
		ErrorMessage:      err.Error(),
		SecondaryStreamID: context.SecondaryStreamID,
		KwikStreamID:      context.KwikStreamID,
		PathID:            context.PathID,
		RetryAttempts:     context.RetryCount,
		Resolved:          false,
	}

	// Maintain history size limit
	if len(h.stats.ErrorHistory) >= h.config.MaxErrorHistory {
		h.stats.ErrorHistory = h.stats.ErrorHistory[1:]
	}
	h.stats.ErrorHistory = append(h.stats.ErrorHistory, record)
}

// recordResolution records that an error was resolved
func (h *MappingErrorHandler) recordResolution(err error, context *MappingErrorContext, method string) {
	if !h.config.EnableMetrics {
		return
	}

	h.stats.mutex.Lock()
	defer h.stats.mutex.Unlock()

	h.stats.RecoverySuccessful++

	// Mark the most recent matching error as resolved
	for i := len(h.stats.ErrorHistory) - 1; i >= 0; i-- {
		record := h.stats.ErrorHistory[i]
		if record.SecondaryStreamID == context.SecondaryStreamID &&
			record.KwikStreamID == context.KwikStreamID &&
			!record.Resolved {
			record.Resolved = true
			record.ResolutionMethod = method
			break
		}
	}
}

// GetErrorStats returns current error statistics
func (h *MappingErrorHandler) GetErrorStats() *MappingErrorStats {
	h.stats.mutex.RLock()
	defer h.stats.mutex.RUnlock()

	// Return a copy to avoid race conditions
	statsCopy := &MappingErrorStats{
		TotalErrors:        h.stats.TotalErrors,
		ErrorsByType:       make(map[string]int64),
		RetriesAttempted:   h.stats.RetriesAttempted,
		RetriesSuccessful:  h.stats.RetriesSuccessful,
		RecoveryAttempts:   h.stats.RecoveryAttempts,
		RecoverySuccessful: h.stats.RecoverySuccessful,
		LastErrorTime:      h.stats.LastErrorTime,
		ErrorHistory:       make([]*MappingErrorRecord, len(h.stats.ErrorHistory)),
	}

	for k, v := range h.stats.ErrorsByType {
		statsCopy.ErrorsByType[k] = v
	}

	copy(statsCopy.ErrorHistory, h.stats.ErrorHistory)

	return statsCopy
}

// setDefaultErrorHandlers sets up default error handling behavior
func (h *MappingErrorHandler) setDefaultErrorHandlers() {
	// Default mapping conflict handler: log and allow retry
	h.onMappingConflict = func(secondaryStreamID, kwikStreamID uint64, pathID string) error {
		h.logger.Info("Default conflict handler: secondary=%d, kwik=%d, path=%s",
			secondaryStreamID, kwikStreamID, pathID)
		return fmt.Errorf("conflict not resolved by default handler")
	}

	// Default invalid mapping handler: log error
	h.onInvalidMapping = func(secondaryStreamID uint64, reason string) error {
		h.logger.Error("Default invalid mapping handler: secondary=%d, reason=%s",
			secondaryStreamID, reason)
		return fmt.Errorf("invalid mapping not resolved by default handler")
	}

	// Default timeout handler: log and allow retry
	h.onMappingTimeout = func(secondaryStreamID uint64) error {
		h.logger.Warn("Default timeout handler: secondary=%d", secondaryStreamID)
		return fmt.Errorf("timeout not resolved by default handler")
	}

	// Default limit exceeded handler: log error
	h.onMappingLimitExceeded = func(currentCount, maxCount int) error {
		h.logger.Error("Default limit exceeded handler: current=%d, max=%d",
			currentCount, maxCount)
		return fmt.Errorf("limit exceeded not resolved by default handler")
	}
}

// RetryableError represents an error that should be retried
type RetryableError struct {
	OriginalError error
	RetryCount    int
	BackoffTime   time.Duration
}

func (e *RetryableError) Error() string {
	return fmt.Sprintf("retryable error (attempt %d): %s", e.RetryCount, e.OriginalError.Error())
}

// IsRetryableError checks if an error is a retryable error
func IsRetryableError(err error) bool {
	_, ok := err.(*RetryableError)
	return ok
}

// GetRetryCount extracts the retry count from a retryable error
func GetRetryCount(err error) int {
	if retryErr, ok := err.(*RetryableError); ok {
		return retryErr.RetryCount
	}
	return 0
}
