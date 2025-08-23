package utils

import (
	"errors"
	"fmt"
	"time"
)

// KWIK error codes - Core errors
const (
	ErrPathNotFound          = "KWIK_PATH_NOT_FOUND"
	ErrPathDead              = "KWIK_PATH_DEAD"
	ErrInvalidFrame          = "KWIK_INVALID_FRAME"
	ErrAuthenticationFailed  = "KWIK_AUTH_FAILED"
	ErrStreamCreationFailed  = "KWIK_STREAM_CREATE_FAILED"
	ErrStreamReadTimeout     = "KWIK_STREAM_READ_TIMEOUT"
	ErrPacketTooLarge        = "KWIK_PACKET_TOO_LARGE"
	ErrOffsetMismatch        = "KWIK_OFFSET_MISMATCH"
	ErrSerializationFailed   = "KWIK_SERIALIZATION_FAILED"
	ErrDeserializationFailed = "KWIK_DESERIALIZATION_FAILED"
	ErrConnectionLost        = "KWIK_CONNECTION_LOST"
)

// KWIK error codes - Session management
const (
	ErrSessionNotFound      = "KWIK_SESSION_NOT_FOUND"
	ErrSessionClosed        = "KWIK_SESSION_CLOSED"
	ErrSessionLimitExceeded = "KWIK_SESSION_LIMIT_EXCEEDED"
	ErrSessionTimeout       = "KWIK_SESSION_TIMEOUT"
	ErrSessionStateInvalid  = "KWIK_SESSION_STATE_INVALID"
)

// KWIK error codes - Path management
const (
	ErrPathLimitExceeded     = "KWIK_PATH_LIMIT_EXCEEDED"
	ErrPathAlreadyExists     = "KWIK_PATH_ALREADY_EXISTS"
	ErrPathCreationFailed    = "KWIK_PATH_CREATION_FAILED"
	ErrPathHealthCheckFailed = "KWIK_PATH_HEALTH_CHECK_FAILED"
	ErrPrimaryPathRequired   = "KWIK_PRIMARY_PATH_REQUIRED"
)

// KWIK error codes - Stream management
const (
	ErrStreamNotFound        = "KWIK_STREAM_NOT_FOUND"
	ErrStreamClosed          = "KWIK_STREAM_CLOSED"
	ErrStreamLimitExceeded   = "KWIK_STREAM_LIMIT_EXCEEDED"
	ErrStreamMultiplexFailed = "KWIK_STREAM_MULTIPLEX_FAILED"
	ErrStreamIsolationFailed = "KWIK_STREAM_ISOLATION_FAILED"
)

// KWIK error codes - Data plane
const (
	ErrDataAggregationFailed = "KWIK_DATA_AGGREGATION_FAILED"
	ErrDataReorderingFailed  = "KWIK_DATA_REORDERING_FAILED"
	ErrFlowControlViolation  = "KWIK_FLOW_CONTROL_VIOLATION"
	ErrCongestionControl     = "KWIK_CONGESTION_CONTROL"
	ErrPacketLoss            = "KWIK_PACKET_LOSS"
	ErrBackpressure          = "KWIK_BACKPRESSURE"
	ErrTimeout               = "KWIK_TIMEOUT"
)

// KWIK error codes - Control plane
const (
	ErrControlFrameInvalid    = "KWIK_CONTROL_FRAME_INVALID"
	ErrControlTimeout         = "KWIK_CONTROL_TIMEOUT"
	ErrControlHandshakeFailed = "KWIK_CONTROL_HANDSHAKE_FAILED"
	ErrControlMessageRouting  = "KWIK_CONTROL_MESSAGE_ROUTING"
)

// KWIK error codes - System level
const (
	ErrSystemNotInitialized = "KWIK_SYSTEM_NOT_INITIALIZED"
	ErrSystemShuttingDown   = "KWIK_SYSTEM_SHUTTING_DOWN"
	ErrResourceExhausted    = "KWIK_RESOURCE_EXHAUSTED"
	ErrConfigurationInvalid = "KWIK_CONFIGURATION_INVALID"
	ErrInternalError        = "KWIK_INTERNAL_ERROR"
)

// KwikError represents a KWIK-specific error
type KwikError struct {
	Code    string
	Message string
	Cause   error
}

func (e *KwikError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s (caused by: %v)", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *KwikError) Unwrap() error {
	return e.Cause
}

// NewKwikError creates a new KWIK error
func NewKwikError(code, message string, cause error) *KwikError {
	return &KwikError{
		Code:    code,
		Message: message,
		Cause:   cause,
	}
}

// Common error constructors
func NewPathNotFoundError(pathID string) error {
	return NewKwikError(ErrPathNotFound, fmt.Sprintf("path %s not found", pathID), nil)
}

func NewPathDeadError(pathID string) error {
	return NewKwikError(ErrPathDead, fmt.Sprintf("path %s is dead", pathID), nil)
}

func NewInvalidFrameError(reason string) error {
	return NewKwikError(ErrInvalidFrame, reason, nil)
}

func NewAuthenticationFailedError(reason string) error {
	return NewKwikError(ErrAuthenticationFailed, reason, nil)
}

func NewStreamCreationFailedError(streamID uint64, cause error) error {
	return NewKwikError(ErrStreamCreationFailed,
		fmt.Sprintf("failed to create stream %d", streamID), cause)
}

func NewPacketTooLargeError(size, maxSize uint32) error {
	return NewKwikError(ErrPacketTooLarge,
		fmt.Sprintf("packet size %d exceeds maximum %d", size, maxSize), nil)
}

func NewOffsetMismatchError(expected, actual uint64) error {
	return NewKwikError(ErrOffsetMismatch,
		fmt.Sprintf("offset mismatch: expected %d, got %d", expected, actual), nil)
}

// IsKwikError checks if an error is a KWIK error with specific code
func IsKwikError(err error, code string) bool {
	var kwikErr *KwikError
	if errors.As(err, &kwikErr) {
		return kwikErr.Code == code
	}
	return false
}

// Enhanced error constructors for session management
func NewSessionNotFoundError(sessionID string) error {
	return NewKwikError(ErrSessionNotFound, fmt.Sprintf("session %s not found", sessionID), nil)
}

func NewSessionClosedError(sessionID string) error {
	return NewKwikError(ErrSessionClosed, fmt.Sprintf("session %s is closed", sessionID), nil)
}

func NewSessionLimitExceededError(current, max int) error {
	return NewKwikError(ErrSessionLimitExceeded,
		fmt.Sprintf("session limit exceeded: %d/%d", current, max), nil)
}

func NewSessionTimeoutError(sessionID string, timeout time.Duration) error {
	return NewKwikError(ErrSessionTimeout,
		fmt.Sprintf("session %s timed out after %v", sessionID, timeout), nil)
}

// Enhanced error constructors for path management
func NewPathLimitExceededError(current, max int) error {
	return NewKwikError(ErrPathLimitExceeded,
		fmt.Sprintf("path limit exceeded: %d/%d", current, max), nil)
}

func NewPathAlreadyExistsError(pathID string) error {
	return NewKwikError(ErrPathAlreadyExists, fmt.Sprintf("path %s already exists", pathID), nil)
}

func NewPathCreationFailedError(address string, cause error) error {
	return NewKwikError(ErrPathCreationFailed,
		fmt.Sprintf("failed to create path to %s", address), cause)
}

func NewPathHealthCheckFailedError(pathID string, cause error) error {
	return NewKwikError(ErrPathHealthCheckFailed,
		fmt.Sprintf("health check failed for path %s", pathID), cause)
}

func NewPrimaryPathRequiredError() error {
	return NewKwikError(ErrPrimaryPathRequired, "primary path is required for this operation", nil)
}

// Enhanced error constructors for stream management
func NewStreamNotFoundError(streamID uint64) error {
	return NewKwikError(ErrStreamNotFound, fmt.Sprintf("stream %d not found", streamID), nil)
}

func NewStreamClosedError(streamID uint64) error {
	return NewKwikError(ErrStreamClosed, fmt.Sprintf("stream %d is closed", streamID), nil)
}

func NewStreamLimitExceededError(current, max int) error {
	return NewKwikError(ErrStreamLimitExceeded,
		fmt.Sprintf("stream limit exceeded: %d/%d", current, max), nil)
}

func NewStreamMultiplexFailedError(streamID uint64, cause error) error {
	return NewKwikError(ErrStreamMultiplexFailed,
		fmt.Sprintf("failed to multiplex stream %d", streamID), cause)
}

// Enhanced error constructors for data plane
func NewDataAggregationFailedError(streamID uint64, cause error) error {
	return NewKwikError(ErrDataAggregationFailed, fmt.Sprintf("data aggregation failed for stream %d", streamID), cause)
}

func NewFlowControlViolationError(streamID uint64, attempted, available uint64) error {
	return NewKwikError(ErrFlowControlViolation, 
		fmt.Sprintf("flow control violation on stream %d: attempted to write %d bytes, %d available", 
			streamID, attempted, available), nil)
}

func NewCongestionControlError(pathID string, reason string) error {
	return NewKwikError(ErrCongestionControl, 
		fmt.Sprintf("congestion control triggered on path %s: %s", pathID, reason), nil)
}

func NewBackpressureError(streamID uint64, reason string) error {
	return NewKwikError(ErrBackpressure, 
		fmt.Sprintf("backpressure on stream %d: %s", streamID, reason), nil)
}

func NewTimeoutError(operation string, timeout time.Duration) error {
	return NewKwikError(ErrTimeout, 
		fmt.Sprintf("operation '%s' timed out after %v", operation, timeout), nil)
}

// Enhanced error constructors for control plane
func NewControlFrameInvalidError(frameType string, reason string) error {
	return NewKwikError(ErrControlFrameInvalid,
		fmt.Sprintf("invalid %s frame: %s", frameType, reason), nil)
}

func NewControlTimeoutError(operation string, timeout time.Duration) error {
	return NewKwikError(ErrControlTimeout,
		fmt.Sprintf("control operation %s timed out after %v", operation, timeout), nil)
}

func NewControlHandshakeFailedError(reason string, cause error) error {
	return NewKwikError(ErrControlHandshakeFailed,
		fmt.Sprintf("control handshake failed: %s", reason), cause)
}

// Enhanced error constructors for system level
func NewSystemNotInitializedError(component string) error {
	return NewKwikError(ErrSystemNotInitialized,
		fmt.Sprintf("system component %s is not initialized", component), nil)
}

func NewSystemShuttingDownError() error {
	return NewKwikError(ErrSystemShuttingDown, "system is shutting down", nil)
}

func NewResourceExhaustedError(resource string) error {
	return NewKwikError(ErrResourceExhausted,
		fmt.Sprintf("resource exhausted: %s", resource), nil)
}

func NewConfigurationInvalidError(field string, value interface{}) error {
	return NewKwikError(ErrConfigurationInvalid,
		fmt.Sprintf("invalid configuration for %s: %v", field, value), nil)
}

func NewInternalError(component string, cause error) error {
	return NewKwikError(ErrInternalError,
		fmt.Sprintf("internal error in %s", component), cause)
}

// Error severity levels
type ErrorSeverity int

const (
	SeverityInfo ErrorSeverity = iota
	SeverityWarning
	SeverityError
	SeverityCritical
)

// EnhancedKwikError extends KwikError with additional context
type EnhancedKwikError struct {
	*KwikError
	Severity    ErrorSeverity
	Component   string
	Timestamp   time.Time
	Context     map[string]interface{}
	Recoverable bool
}

// NewEnhancedKwikError creates an enhanced KWIK error with additional context
func NewEnhancedKwikError(code, message string, cause error, severity ErrorSeverity, component string, recoverable bool) *EnhancedKwikError {
	return &EnhancedKwikError{
		KwikError:   NewKwikError(code, message, cause),
		Severity:    severity,
		Component:   component,
		Timestamp:   time.Now(),
		Context:     make(map[string]interface{}),
		Recoverable: recoverable,
	}
}

// AddContext adds contextual information to the error
func (e *EnhancedKwikError) AddContext(key string, value interface{}) {
	if e.Context == nil {
		e.Context = make(map[string]interface{})
	}
	e.Context[key] = value
}

// Error implements the error interface with enhanced formatting
func (e *EnhancedKwikError) Error() string {
	base := e.KwikError.Error()
	return fmt.Sprintf("[%s] %s [%s] %s",
		e.Severity.String(), e.Component, e.Timestamp.Format(time.RFC3339), base)
}

func (s ErrorSeverity) String() string {
	switch s {
	case SeverityInfo:
		return "INFO"
	case SeverityWarning:
		return "WARN"
	case SeverityError:
		return "ERROR"
	case SeverityCritical:
		return "CRITICAL"
	default:
		return "UNKNOWN"
	}
}

// Error recovery strategies
type RecoveryStrategy int

const (
	RecoveryRetry RecoveryStrategy = iota
	RecoveryFallback
	RecoveryFailover
	RecoveryGracefulDegradation
	RecoveryAbort
)

// RecoveryAction represents an action to take when recovering from an error
type RecoveryAction struct {
	Strategy    RecoveryStrategy
	MaxRetries  int
	RetryDelay  time.Duration
	Fallback    func() error
	Description string
}

// ErrorRecoveryManager manages error recovery strategies
type ErrorRecoveryManager struct {
	strategies map[string]*RecoveryAction
	logger     interface {
		Error(msg string, keysAndValues ...interface{})
		Warn(msg string, keysAndValues ...interface{})
		Info(msg string, keysAndValues ...interface{})
	}
}

// NewErrorRecoveryManager creates a new error recovery manager
func NewErrorRecoveryManager(logger interface {
	Error(msg string, keysAndValues ...interface{})
	Warn(msg string, keysAndValues ...interface{})
	Info(msg string, keysAndValues ...interface{})
}) *ErrorRecoveryManager {
	return &ErrorRecoveryManager{
		strategies: make(map[string]*RecoveryAction),
		logger:     logger,
	}
}

// RegisterRecoveryStrategy registers a recovery strategy for a specific error code
func (rm *ErrorRecoveryManager) RegisterRecoveryStrategy(errorCode string, action *RecoveryAction) {
	rm.strategies[errorCode] = action
}

// RecoverFromError attempts to recover from an error using registered strategies
func (rm *ErrorRecoveryManager) RecoverFromError(err error, operation func() error) error {
	var kwikErr *KwikError
	if !errors.As(err, &kwikErr) {
		// Not a KWIK error, can't recover
		return err
	}

	strategy, exists := rm.strategies[kwikErr.Code]
	if !exists {
		// No recovery strategy registered
		return err
	}

	rm.logger.Info("Attempting error recovery",
		"errorCode", kwikErr.Code,
		"strategy", strategy.Strategy.String(),
		"description", strategy.Description)

	switch strategy.Strategy {
	case RecoveryRetry:
		return rm.retryOperation(operation, strategy, kwikErr)
	case RecoveryFallback:
		return rm.fallbackOperation(strategy, kwikErr)
	case RecoveryFailover:
		return rm.failoverOperation(strategy, kwikErr)
	case RecoveryGracefulDegradation:
		return rm.gracefulDegradation(strategy, kwikErr)
	case RecoveryAbort:
		return err // No recovery, return original error
	default:
		return err
	}
}

// retryOperation implements retry recovery strategy
func (rm *ErrorRecoveryManager) retryOperation(operation func() error, strategy *RecoveryAction, originalErr *KwikError) error {
	for attempt := 1; attempt <= strategy.MaxRetries; attempt++ {
		if attempt > 1 {
			time.Sleep(strategy.RetryDelay)
		}

		rm.logger.Info("Retrying operation",
			"attempt", attempt,
			"maxRetries", strategy.MaxRetries,
			"originalError", originalErr.Code)

		err := operation()
		if err == nil {
			rm.logger.Info("Operation succeeded after retry", "attempt", attempt)
			return nil
		}

		// Check if it's the same type of error
		var retryErr *KwikError
		if errors.As(err, &retryErr) && retryErr.Code == originalErr.Code {
			continue // Same error, try again
		}

		// Different error, return it
		return err
	}

	rm.logger.Error("Operation failed after all retries",
		"maxRetries", strategy.MaxRetries,
		"originalError", originalErr.Code)
	return originalErr
}

// fallbackOperation implements fallback recovery strategy
func (rm *ErrorRecoveryManager) fallbackOperation(strategy *RecoveryAction, originalErr *KwikError) error {
	if strategy.Fallback == nil {
		return originalErr
	}

	rm.logger.Warn("Executing fallback operation", "originalError", originalErr.Code)

	err := strategy.Fallback()
	if err != nil {
		rm.logger.Error("Fallback operation failed", "error", err)
		return originalErr // Return original error if fallback fails
	}

	rm.logger.Info("Fallback operation succeeded")
	return nil
}

// failoverOperation implements failover recovery strategy
func (rm *ErrorRecoveryManager) failoverOperation(strategy *RecoveryAction, originalErr *KwikError) error {
	// Failover logic would be implemented here
	// This is a placeholder for path failover, session failover, etc.
	rm.logger.Warn("Failover recovery not yet implemented", "originalError", originalErr.Code)
	return originalErr
}

// gracefulDegradation implements graceful degradation recovery strategy
func (rm *ErrorRecoveryManager) gracefulDegradation(strategy *RecoveryAction, originalErr *KwikError) error {
	// Graceful degradation logic would be implemented here
	// This could involve reducing functionality while maintaining basic operation
	rm.logger.Warn("Graceful degradation recovery not yet implemented", "originalError", originalErr.Code)
	return originalErr
}

func (s RecoveryStrategy) String() string {
	switch s {
	case RecoveryRetry:
		return "RETRY"
	case RecoveryFallback:
		return "FALLBACK"
	case RecoveryFailover:
		return "FAILOVER"
	case RecoveryGracefulDegradation:
		return "GRACEFUL_DEGRADATION"
	case RecoveryAbort:
		return "ABORT"
	default:
		return "UNKNOWN"
	}
}

// Predefined recovery strategies for common KWIK errors
func GetDefaultRecoveryStrategies() map[string]*RecoveryAction {
	return map[string]*RecoveryAction{
		ErrConnectionLost: {
			Strategy:    RecoveryRetry,
			MaxRetries:  3,
			RetryDelay:  time.Second * 2,
			Description: "Retry connection establishment",
		},
		ErrPathDead: {
			Strategy:    RecoveryFailover,
			MaxRetries:  1,
			RetryDelay:  0,
			Description: "Failover to alternative path",
		},
		ErrAuthenticationFailed: {
			Strategy:    RecoveryRetry,
			MaxRetries:  2,
			RetryDelay:  time.Second * 1,
			Description: "Retry authentication",
		},
		ErrStreamCreationFailed: {
			Strategy:    RecoveryRetry,
			MaxRetries:  3,
			RetryDelay:  time.Millisecond * 500,
			Description: "Retry stream creation",
		},
		ErrControlTimeout: {
			Strategy:    RecoveryRetry,
			MaxRetries:  2,
			RetryDelay:  time.Second * 1,
			Description: "Retry control operation with timeout",
		},
		ErrResourceExhausted: {
			Strategy:    RecoveryGracefulDegradation,
			MaxRetries:  0,
			RetryDelay:  0,
			Description: "Reduce resource usage gracefully",
		},
	}
}
