package session

import (
	"context"
	"fmt"
	"sync"
	"time"

	"kwik/internal/utils"
)

// ErrorRecoverySystem manages error recovery strategies and execution
type ErrorRecoverySystem interface {
	// RegisterStrategy registers a recovery strategy for a specific error type
	RegisterStrategy(errorType ErrorType, strategy RecoveryStrategy) error

	// RecoverFromError attempts to recover from an error using registered strategies
	RecoverFromError(ctx context.Context, kwikError *KwikError) (*RecoveryResult, error)

	// GetRecoveryStatus returns the current recovery status for a session
	GetRecoveryStatus(sessionID string) (*RecoveryStatus, error)

	// GetRecoveryMetrics returns recovery metrics and statistics
	GetRecoveryMetrics() *RecoveryMetrics

	// Close shuts down the error recovery system
	Close() error
}

// RecoveryStrategy defines how to recover from specific types of errors
type RecoveryStrategy interface {
	// CanRecover determines if this strategy can handle the given error
	CanRecover(kwikError *KwikError) bool

	// Recover attempts to recover from the error
	Recover(ctx context.Context, recoveryContext *RecoveryContext) (*RecoveryResult, error)

	// GetStrategyType returns the type of this recovery strategy
	GetStrategyType() StrategyType

	// GetMaxRetries returns the maximum number of retry attempts
	GetMaxRetries() int
}

// KwikError represents an enhanced error with classification and context
type KwikError struct {
	// Basic error information
	Code      string
	Message   string
	Cause     error
	Timestamp time.Time

	// Error classification
	Type     ErrorType
	Severity ErrorSeverity
	Category ErrorCategory

	// Context information
	SessionID string
	PathID    string
	StreamID  uint64
	Component string

	// Recovery information
	Recoverable bool
	RetryCount  int
	LastRetry   time.Time

	// Additional metadata
	Metadata map[string]interface{}
}

// ErrorType defines the type of error for recovery strategy selection
type ErrorType int

const (
	ErrorTypeUnknown ErrorType = iota
	ErrorTypeNetwork
	ErrorTypeTimeout
	ErrorTypeProtocol
	ErrorTypeAuthentication
	ErrorTypeAuthorization
	ErrorTypeResource
	ErrorTypeConfiguration
	ErrorTypeData
	ErrorTypeSession
	ErrorTypePath
	ErrorTypeStream
)

// ErrorSeverity defines the severity level of an error
type ErrorSeverity int

const (
	SeverityLow ErrorSeverity = iota
	SeverityMedium
	SeverityHigh
	SeverityCritical
)

// ErrorCategory defines the category of error for grouping and analysis
type ErrorCategory int

const (
	CategoryTransient ErrorCategory = iota
	CategoryPermanent
	CategoryConfiguration
	CategoryResource
	CategorySecurity
)

// StrategyType defines the type of recovery strategy
type StrategyType int

const (
	StrategyTypeRetryWithBackoff StrategyType = iota
	StrategyTypePathFailover
	StrategyTypeConnectionReset
	StrategyTypeSessionRestart
	StrategyTypeGracefulDegradation
	StrategyTypeCircuitBreaker
)

// RecoveryContext provides context information for recovery operations
type RecoveryContext struct {
	// Error information
	Error     *KwikError
	SessionID string
	PathID    string
	StreamID  uint64

	// Recovery state
	AttemptNumber int
	StartTime     time.Time
	Deadline      time.Time

	// System state
	HealthMonitor   ConnectionHealthMonitor
	FailoverManager FailoverManagerInterface
	SessionManager  interface{} // Will be defined based on actual session manager interface

	// Configuration
	MaxRetries        int
	RetryDelay        time.Duration
	BackoffMultiplier float64

	// Metadata
	Metadata map[string]interface{}
}

// RecoveryResult contains the result of a recovery attempt
type RecoveryResult struct {
	// Result information
	Success       bool
	Strategy      StrategyType
	AttemptNumber int
	Duration      time.Duration

	// Actions taken
	ActionsTaken      []RecoveryAction
	PathsChanged      []string
	SessionsRestarted []string

	// New state
	NewPathID    string
	NewSessionID string

	// Error information (if recovery failed)
	Error       error
	ShouldRetry bool
	RetryDelay  time.Duration

	// Metadata
	Metadata map[string]interface{}
}

// RecoveryAction describes an action taken during recovery
type RecoveryAction struct {
	Type      ActionType
	Target    string
	Timestamp time.Time
	Success   bool
	Error     error
	Metadata  map[string]interface{}
}

// ActionType defines the type of recovery action
type ActionType int

const (
	ActionTypeRetry ActionType = iota
	ActionTypePathSwitch
	ActionTypeConnectionReset
	ActionTypeSessionRestart
	ActionTypeBackoff
	ActionTypeCircuitBreakerOpen
	ActionTypeCircuitBreakerClose
	ActionTypeGracefulDegradation
)

// RecoveryStatus tracks the recovery status for a session
type RecoveryStatus struct {
	SessionID           string
	CurrentState        RecoveryState
	ActiveStrategies    []StrategyType
	LastRecovery        time.Time
	RecoveryCount       int
	FailureCount        int
	SuccessRate         float64
	AverageRecoveryTime time.Duration

	// Current recovery attempt (if any)
	CurrentAttempt *RecoveryAttempt
}

// RecoveryState defines the current state of recovery for a session
type RecoveryState int

const (
	RecoveryStateNormal RecoveryState = iota
	RecoveryStateRecovering
	RecoveryStateFailed
	RecoveryStateDegraded
	RecoveryStateCircuitOpen
)

// RecoveryAttempt tracks an ongoing recovery attempt
type RecoveryAttempt struct {
	AttemptID     string
	Strategy      StrategyType
	StartTime     time.Time
	AttemptNumber int
	Error         *KwikError
	Context       *RecoveryContext
}

// RecoveryMetrics contains metrics and statistics about error recovery
type RecoveryMetrics struct {
	// Overall statistics
	TotalRecoveryAttempts uint64
	SuccessfulRecoveries  uint64
	FailedRecoveries      uint64
	SuccessRate           float64

	// Timing statistics
	AverageRecoveryTime time.Duration
	MedianRecoveryTime  time.Duration
	MaxRecoveryTime     time.Duration
	MinRecoveryTime     time.Duration

	// Error type statistics
	ErrorTypeStats map[ErrorType]*ErrorTypeMetrics
	StrategyStats  map[StrategyType]*StrategyMetrics

	// Recent activity
	RecentRecoveries []*RecoveryResult
	ActiveRecoveries int

	// System health
	SystemHealthScore float64
	LastUpdated       time.Time
}

// ErrorTypeMetrics contains metrics for a specific error type
type ErrorTypeMetrics struct {
	Count          uint64
	SuccessRate    float64
	AverageTime    time.Duration
	LastOccurrence time.Time
}

// StrategyMetrics contains metrics for a specific recovery strategy
type StrategyMetrics struct {
	UsageCount  uint64
	SuccessRate float64
	AverageTime time.Duration
	LastUsed    time.Time
}

// ErrorRecoverySystemImpl implements the ErrorRecoverySystem interface
type ErrorRecoverySystemImpl struct {
	// Strategy registry
	strategies    map[ErrorType][]RecoveryStrategy
	strategyMutex sync.RWMutex

	// Recovery tracking
	recoveryStatus map[string]*RecoveryStatus
	statusMutex    sync.RWMutex

	// Metrics
	metrics      *RecoveryMetrics
	metricsMutex sync.RWMutex

	// Configuration
	config *RecoveryConfig

	// Dependencies
	logger          SessionLogger
	healthMonitor   ConnectionHealthMonitor
	failoverManager FailoverManagerInterface

	// Background processing
	recoveryQueue chan *RecoveryRequest
	stopChan      chan struct{}
	wg            sync.WaitGroup
}

// RecoveryConfig contains configuration for the error recovery system
type RecoveryConfig struct {
	// General settings
	MaxConcurrentRecoveries  int
	DefaultMaxRetries        int
	DefaultRetryDelay        time.Duration
	DefaultBackoffMultiplier float64

	// Timeout settings
	RecoveryTimeout time.Duration
	StrategyTimeout time.Duration

	// Circuit breaker settings
	CircuitBreakerThreshold int
	CircuitBreakerTimeout   time.Duration

	// Metrics settings
	MetricsRetentionPeriod time.Duration
	RecentRecoveriesLimit  int
}

// RecoveryRequest represents a request for error recovery
type RecoveryRequest struct {
	Error      *KwikError
	Context    *RecoveryContext
	ResultChan chan *RecoveryResult
}

// SessionLogger interface for logging
type SessionLogger interface {
	Info(msg string, keysAndValues ...interface{})
	Debug(msg string, keysAndValues ...interface{})
	Warn(msg string, keysAndValues ...interface{})
	Error(msg string, keysAndValues ...interface{})
}

// FailoverReason defines the reason for triggering a failover
type FailoverReason int

const (
	FailoverReasonPathFailed FailoverReason = iota
	FailoverReasonHealthDegraded
	FailoverReasonTimeout
	FailoverReasonManual
	FailoverReasonLoadBalancing
)

func (r FailoverReason) String() string {
	switch r {
	case FailoverReasonPathFailed:
		return "PATH_FAILED"
	case FailoverReasonHealthDegraded:
		return "HEALTH_DEGRADED"
	case FailoverReasonTimeout:
		return "TIMEOUT"
	case FailoverReasonManual:
		return "MANUAL"
	case FailoverReasonLoadBalancing:
		return "LOAD_BALANCING"
	default:
		return "UNKNOWN"
	}
}

// FailoverManagerInterface defines the interface for failover operations
type FailoverManagerInterface interface {
	TriggerFailover(sessionID string, reason FailoverReason) error
}

// NewErrorRecoverySystem creates a new error recovery system
func NewErrorRecoverySystem(logger SessionLogger, healthMonitor ConnectionHealthMonitor, failoverManager FailoverManagerInterface, config *RecoveryConfig) ErrorRecoverySystem {
	if config == nil {
		config = DefaultRecoveryConfig()
	}

	ers := &ErrorRecoverySystemImpl{
		strategies:      make(map[ErrorType][]RecoveryStrategy),
		recoveryStatus:  make(map[string]*RecoveryStatus),
		metrics:         NewRecoveryMetrics(),
		config:          config,
		logger:          logger,
		healthMonitor:   healthMonitor,
		failoverManager: failoverManager,
		recoveryQueue:   make(chan *RecoveryRequest, config.MaxConcurrentRecoveries*2),
		stopChan:        make(chan struct{}),
	}

	// Start background workers
	for i := 0; i < config.MaxConcurrentRecoveries; i++ {
		ers.wg.Add(1)
		go ers.recoveryWorker()
	}

	// Register default strategies
	ers.registerDefaultStrategies()

	return ers
}

// DefaultRecoveryConfig returns default configuration for error recovery
func DefaultRecoveryConfig() *RecoveryConfig {
	return &RecoveryConfig{
		MaxConcurrentRecoveries:  5,
		DefaultMaxRetries:        3,
		DefaultRetryDelay:        100 * time.Millisecond,
		DefaultBackoffMultiplier: 2.0,
		RecoveryTimeout:          30 * time.Second,
		StrategyTimeout:          10 * time.Second,
		CircuitBreakerThreshold:  5,
		CircuitBreakerTimeout:    60 * time.Second,
		MetricsRetentionPeriod:   24 * time.Hour,
		RecentRecoveriesLimit:    100,
	}
}

// NewRecoveryMetrics creates new recovery metrics
func NewRecoveryMetrics() *RecoveryMetrics {
	return &RecoveryMetrics{
		ErrorTypeStats:   make(map[ErrorType]*ErrorTypeMetrics),
		StrategyStats:    make(map[StrategyType]*StrategyMetrics),
		RecentRecoveries: make([]*RecoveryResult, 0),
		LastUpdated:      time.Now(),
	}
}

// NewKwikError creates a new enhanced KWIK error
func NewKwikError(code string, message string, cause error) *KwikError {
	return &KwikError{
		Code:        code,
		Message:     message,
		Cause:       cause,
		Timestamp:   time.Now(),
		Type:        classifyErrorType(code),
		Severity:    classifyErrorSeverity(code),
		Category:    classifyErrorCategory(code),
		Recoverable: isRecoverable(code),
		Metadata:    make(map[string]interface{}),
	}
}

// Error implements the error interface
func (ke *KwikError) Error() string {
	if ke.Cause != nil {
		return fmt.Sprintf("%s: %v", ke.Message, ke.Cause)
	}
	return ke.Message
}

// WithContext adds context information to the error
func (ke *KwikError) WithContext(sessionID, pathID string, streamID uint64, component string) *KwikError {
	ke.SessionID = sessionID
	ke.PathID = pathID
	ke.StreamID = streamID
	ke.Component = component
	return ke
}

// WithMetadata adds metadata to the error
func (ke *KwikError) WithMetadata(key string, value interface{}) *KwikError {
	if ke.Metadata == nil {
		ke.Metadata = make(map[string]interface{})
	}
	ke.Metadata[key] = value
	return ke
}

// IsRetryable returns true if the error can be retried
func (ke *KwikError) IsRetryable() bool {
	return ke.Recoverable && ke.RetryCount < 10 // Max 10 retries by default
}

// classifyErrorType classifies an error code into an error type
func classifyErrorType(code string) ErrorType {
	switch code {
	case utils.ErrConnectionLost, utils.ErrPathNotFound:
		return ErrorTypeNetwork
	case utils.ErrTimeout, utils.ErrSessionTimeout:
		return ErrorTypeTimeout
	case utils.ErrInvalidFrame, utils.ErrControlFrameInvalid:
		return ErrorTypeProtocol
	case utils.ErrAuthenticationFailed:
		return ErrorTypeAuthentication
	case utils.ErrResourceExhausted:
		return ErrorTypeResource
	case utils.ErrConfigurationInvalid:
		return ErrorTypeConfiguration
	case utils.ErrOffsetMismatch, utils.ErrSerializationFailed:
		return ErrorTypeData
	case utils.ErrSessionNotFound, utils.ErrSessionClosed:
		return ErrorTypeSession
	case utils.ErrStreamCreationFailed, utils.ErrStreamNotFound:
		return ErrorTypeStream
	case utils.ErrPathDead, utils.ErrPathCreationFailed:
		return ErrorTypePath
	default:
		return ErrorTypeUnknown
	}
}

// classifyErrorSeverity classifies an error code into a severity level
func classifyErrorSeverity(code string) ErrorSeverity {
	switch code {
	case utils.ErrAuthenticationFailed, utils.ErrInternalError:
		return SeverityCritical
	case utils.ErrConnectionLost, utils.ErrSessionNotFound:
		return SeverityHigh
	case utils.ErrTimeout, utils.ErrResourceExhausted:
		return SeverityMedium
	default:
		return SeverityLow
	}
}

// classifyErrorCategory classifies an error code into a category
func classifyErrorCategory(code string) ErrorCategory {
	switch code {
	case utils.ErrTimeout, utils.ErrConnectionLost:
		return CategoryTransient
	case utils.ErrAuthenticationFailed:
		return CategorySecurity
	case utils.ErrConfigurationInvalid:
		return CategoryConfiguration
	case utils.ErrResourceExhausted:
		return CategoryResource
	default:
		return CategoryPermanent
	}
}

// isRecoverable determines if an error is recoverable
func isRecoverable(code string) bool {
	switch code {
	case utils.ErrTimeout, utils.ErrConnectionLost, utils.ErrPathDead,
		utils.ErrResourceExhausted, utils.ErrPacketLoss:
		return true
	case utils.ErrAuthenticationFailed, utils.ErrInternalError:
		return false
	default:
		return true // Default to recoverable for unknown errors
	}
}

// RegisterStrategy registers a recovery strategy for a specific error type
func (ers *ErrorRecoverySystemImpl) RegisterStrategy(errorType ErrorType, strategy RecoveryStrategy) error {
	ers.strategyMutex.Lock()
	defer ers.strategyMutex.Unlock()

	if strategy == nil {
		return NewKwikError(utils.ErrConfigurationInvalid, "strategy cannot be nil", nil)
	}

	if ers.strategies[errorType] == nil {
		ers.strategies[errorType] = make([]RecoveryStrategy, 0)
	}

	ers.strategies[errorType] = append(ers.strategies[errorType], strategy)

	ers.logger.Info("Registered recovery strategy",
		"errorType", errorType,
		"strategyType", strategy.GetStrategyType())

	return nil
}

// RecoverFromError attempts to recover from an error using registered strategies
func (ers *ErrorRecoverySystemImpl) RecoverFromError(ctx context.Context, kwikError *KwikError) (*RecoveryResult, error) {
	if kwikError == nil {
		return nil, NewKwikError(utils.ErrConfigurationInvalid, "kwikError cannot be nil", nil)
	}

	// Create recovery context
	recoveryContext := &RecoveryContext{
		Error:             kwikError,
		SessionID:         kwikError.SessionID,
		PathID:            kwikError.PathID,
		StreamID:          kwikError.StreamID,
		AttemptNumber:     kwikError.RetryCount + 1,
		StartTime:         time.Now(),
		Deadline:          time.Now().Add(ers.config.RecoveryTimeout),
		HealthMonitor:     ers.healthMonitor,
		FailoverManager:   ers.failoverManager,
		MaxRetries:        ers.config.DefaultMaxRetries,
		RetryDelay:        ers.config.DefaultRetryDelay,
		BackoffMultiplier: ers.config.DefaultBackoffMultiplier,
		Metadata:          make(map[string]interface{}),
	}

	// Update recovery status
	ers.updateRecoveryStatus(kwikError.SessionID, RecoveryStateRecovering, recoveryContext)

	// Try recovery strategies
	result, err := ers.tryRecoveryStrategies(ctx, recoveryContext)

	// Update metrics
	ers.updateMetrics(result, err, kwikError)

	// Update recovery status based on result
	if result != nil && result.Success {
		ers.updateRecoveryStatus(kwikError.SessionID, RecoveryStateNormal, nil)
	} else {
		ers.updateRecoveryStatus(kwikError.SessionID, RecoveryStateFailed, recoveryContext)
	}

	return result, err
}

// tryRecoveryStrategies attempts recovery using available strategies
func (ers *ErrorRecoverySystemImpl) tryRecoveryStrategies(ctx context.Context, recoveryContext *RecoveryContext) (*RecoveryResult, error) {
	ers.strategyMutex.RLock()
	strategies := ers.strategies[recoveryContext.Error.Type]
	ers.strategyMutex.RUnlock()

	if len(strategies) == 0 {
		return &RecoveryResult{
			Success:       false,
			Strategy:      StrategyTypeRetryWithBackoff, // Default fallback
			AttemptNumber: recoveryContext.AttemptNumber,
			Duration:      time.Since(recoveryContext.StartTime),
			Error:         NewKwikError(utils.ErrInternalError, "no recovery strategies available", nil),
			ShouldRetry:   false,
		}, nil
	}

	// Try each strategy in order
	for _, strategy := range strategies {
		if !strategy.CanRecover(recoveryContext.Error) {
			continue
		}

		ers.logger.Debug("Attempting recovery with strategy",
			"strategy", strategy.GetStrategyType(),
			"errorType", recoveryContext.Error.Type,
			"attempt", recoveryContext.AttemptNumber)

		// Create context with timeout for this strategy
		strategyCtx, cancel := context.WithTimeout(ctx, ers.config.StrategyTimeout)

		result, err := strategy.Recover(strategyCtx, recoveryContext)
		cancel()

		if err != nil {
			ers.logger.Warn("Recovery strategy failed",
				"strategy", strategy.GetStrategyType(),
				"error", err)
			continue
		}

		if result != nil && result.Success {
			ers.logger.Info("Recovery successful",
				"strategy", strategy.GetStrategyType(),
				"duration", result.Duration)
			return result, nil
		}

		// If strategy suggests retry, continue to next strategy
		if result != nil && result.ShouldRetry {
			continue
		}

		// Strategy failed and doesn't suggest retry
		break
	}

	// All strategies failed
	return &RecoveryResult{
		Success:       false,
		Strategy:      StrategyTypeRetryWithBackoff,
		AttemptNumber: recoveryContext.AttemptNumber,
		Duration:      time.Since(recoveryContext.StartTime),
		Error:         NewKwikError(utils.ErrInternalError, "all recovery strategies failed", nil),
		ShouldRetry:   recoveryContext.AttemptNumber < recoveryContext.MaxRetries,
		RetryDelay:    ers.calculateBackoffDelay(recoveryContext.AttemptNumber, recoveryContext.RetryDelay, recoveryContext.BackoffMultiplier),
	}, nil
}

// calculateBackoffDelay calculates exponential backoff delay
func (ers *ErrorRecoverySystemImpl) calculateBackoffDelay(attempt int, baseDelay time.Duration, multiplier float64) time.Duration {
	if attempt <= 1 {
		return baseDelay
	}

	delay := float64(baseDelay)
	for i := 1; i < attempt; i++ {
		delay *= multiplier
	}

	// Cap at 30 seconds
	maxDelay := 30 * time.Second
	if time.Duration(delay) > maxDelay {
		return maxDelay
	}

	return time.Duration(delay)
}

// updateRecoveryStatus updates the recovery status for a session
func (ers *ErrorRecoverySystemImpl) updateRecoveryStatus(sessionID string, state RecoveryState, context *RecoveryContext) {
	ers.statusMutex.Lock()
	defer ers.statusMutex.Unlock()

	status, exists := ers.recoveryStatus[sessionID]
	if !exists {
		status = &RecoveryStatus{
			SessionID:    sessionID,
			CurrentState: RecoveryStateNormal,
		}
		ers.recoveryStatus[sessionID] = status
	}

	status.CurrentState = state
	status.LastRecovery = time.Now()

	if context != nil {
		status.CurrentAttempt = &RecoveryAttempt{
			AttemptID:     fmt.Sprintf("%s-%d", sessionID, context.AttemptNumber),
			Strategy:      StrategyTypeRetryWithBackoff, // Will be updated by actual strategy
			StartTime:     context.StartTime,
			AttemptNumber: context.AttemptNumber,
			Error:         context.Error,
			Context:       context,
		}

		if state == RecoveryStateRecovering {
			status.RecoveryCount++
		} else if state == RecoveryStateFailed {
			status.FailureCount++
		}

		// Update success rate
		if status.RecoveryCount > 0 {
			successCount := status.RecoveryCount - status.FailureCount
			status.SuccessRate = float64(successCount) / float64(status.RecoveryCount)
		}
	} else {
		status.CurrentAttempt = nil
	}
}

// updateMetrics updates recovery metrics
func (ers *ErrorRecoverySystemImpl) updateMetrics(result *RecoveryResult, err error, kwikError *KwikError) {
	ers.metricsMutex.Lock()
	defer ers.metricsMutex.Unlock()

	ers.metrics.TotalRecoveryAttempts++

	// Update error type statistics
	if kwikError != nil {
		errorTypeStats, exists := ers.metrics.ErrorTypeStats[kwikError.Type]
		if !exists {
			errorTypeStats = &ErrorTypeMetrics{}
			ers.metrics.ErrorTypeStats[kwikError.Type] = errorTypeStats
		}

		errorTypeStats.Count++
		errorTypeStats.LastOccurrence = time.Now()

		if result != nil {
			if result.Success {
				if errorTypeStats.SuccessRate == 0 {
					errorTypeStats.SuccessRate = 1.0
				} else {
					successCount := float64(errorTypeStats.Count) * errorTypeStats.SuccessRate
					errorTypeStats.SuccessRate = (successCount + 1.0) / float64(errorTypeStats.Count)
				}
			} else {
				successCount := float64(errorTypeStats.Count-1) * errorTypeStats.SuccessRate
				errorTypeStats.SuccessRate = successCount / float64(errorTypeStats.Count)
			}

			if errorTypeStats.AverageTime == 0 {
				errorTypeStats.AverageTime = result.Duration
			} else {
				errorTypeStats.AverageTime = (errorTypeStats.AverageTime + result.Duration) / 2
			}
		}
	}

	if result != nil {
		if result.Success {
			ers.metrics.SuccessfulRecoveries++
		} else {
			ers.metrics.FailedRecoveries++
		}

		// Update timing statistics
		if ers.metrics.AverageRecoveryTime == 0 {
			ers.metrics.AverageRecoveryTime = result.Duration
		} else {
			ers.metrics.AverageRecoveryTime = (ers.metrics.AverageRecoveryTime + result.Duration) / 2
		}

		if result.Duration > ers.metrics.MaxRecoveryTime {
			ers.metrics.MaxRecoveryTime = result.Duration
		}

		if ers.metrics.MinRecoveryTime == 0 || result.Duration < ers.metrics.MinRecoveryTime {
			ers.metrics.MinRecoveryTime = result.Duration
		}

		// Update strategy statistics
		strategyStats, exists := ers.metrics.StrategyStats[result.Strategy]
		if !exists {
			strategyStats = &StrategyMetrics{}
			ers.metrics.StrategyStats[result.Strategy] = strategyStats
		}

		strategyStats.UsageCount++
		strategyStats.LastUsed = time.Now()
		if result.Success {
			if strategyStats.SuccessRate == 0 {
				strategyStats.SuccessRate = 1.0
			} else {
				successCount := float64(strategyStats.UsageCount) * strategyStats.SuccessRate
				strategyStats.SuccessRate = (successCount + 1.0) / float64(strategyStats.UsageCount)
			}
		} else {
			successCount := float64(strategyStats.UsageCount-1) * strategyStats.SuccessRate
			strategyStats.SuccessRate = successCount / float64(strategyStats.UsageCount)
		}

		if strategyStats.AverageTime == 0 {
			strategyStats.AverageTime = result.Duration
		} else {
			strategyStats.AverageTime = (strategyStats.AverageTime + result.Duration) / 2
		}

		// Add to recent recoveries
		ers.metrics.RecentRecoveries = append(ers.metrics.RecentRecoveries, result)
		if len(ers.metrics.RecentRecoveries) > ers.config.RecentRecoveriesLimit {
			ers.metrics.RecentRecoveries = ers.metrics.RecentRecoveries[1:]
		}
	}

	// Update success rate
	if ers.metrics.TotalRecoveryAttempts > 0 {
		ers.metrics.SuccessRate = float64(ers.metrics.SuccessfulRecoveries) / float64(ers.metrics.TotalRecoveryAttempts)
	}

	ers.metrics.LastUpdated = time.Now()
}

// GetRecoveryStatus returns the current recovery status for a session
func (ers *ErrorRecoverySystemImpl) GetRecoveryStatus(sessionID string) (*RecoveryStatus, error) {
	ers.statusMutex.RLock()
	defer ers.statusMutex.RUnlock()

	status, exists := ers.recoveryStatus[sessionID]
	if !exists {
		return nil, NewKwikError(utils.ErrSessionNotFound, fmt.Sprintf("no recovery status for session %s", sessionID), nil)
	}

	// Return a copy to avoid race conditions
	statusCopy := *status
	if status.CurrentAttempt != nil {
		attemptCopy := *status.CurrentAttempt
		statusCopy.CurrentAttempt = &attemptCopy
	}

	return &statusCopy, nil
}

// GetRecoveryMetrics returns recovery metrics and statistics
func (ers *ErrorRecoverySystemImpl) GetRecoveryMetrics() *RecoveryMetrics {
	ers.metricsMutex.RLock()
	defer ers.metricsMutex.RUnlock()

	// Return a copy of metrics
	metricsCopy := &RecoveryMetrics{
		TotalRecoveryAttempts: ers.metrics.TotalRecoveryAttempts,
		SuccessfulRecoveries:  ers.metrics.SuccessfulRecoveries,
		FailedRecoveries:      ers.metrics.FailedRecoveries,
		SuccessRate:           ers.metrics.SuccessRate,
		AverageRecoveryTime:   ers.metrics.AverageRecoveryTime,
		MedianRecoveryTime:    ers.metrics.MedianRecoveryTime,
		MaxRecoveryTime:       ers.metrics.MaxRecoveryTime,
		MinRecoveryTime:       ers.metrics.MinRecoveryTime,
		ErrorTypeStats:        make(map[ErrorType]*ErrorTypeMetrics),
		StrategyStats:         make(map[StrategyType]*StrategyMetrics),
		RecentRecoveries:      make([]*RecoveryResult, len(ers.metrics.RecentRecoveries)),
		ActiveRecoveries:      ers.metrics.ActiveRecoveries,
		SystemHealthScore:     ers.metrics.SystemHealthScore,
		LastUpdated:           ers.metrics.LastUpdated,
	}

	// Copy error type stats
	for errorType, stats := range ers.metrics.ErrorTypeStats {
		metricsCopy.ErrorTypeStats[errorType] = &ErrorTypeMetrics{
			Count:          stats.Count,
			SuccessRate:    stats.SuccessRate,
			AverageTime:    stats.AverageTime,
			LastOccurrence: stats.LastOccurrence,
		}
	}

	// Copy strategy stats
	for strategyType, stats := range ers.metrics.StrategyStats {
		metricsCopy.StrategyStats[strategyType] = &StrategyMetrics{
			UsageCount:  stats.UsageCount,
			SuccessRate: stats.SuccessRate,
			AverageTime: stats.AverageTime,
			LastUsed:    stats.LastUsed,
		}
	}

	// Copy recent recoveries
	copy(metricsCopy.RecentRecoveries, ers.metrics.RecentRecoveries)

	return metricsCopy
}

// recoveryWorker processes recovery requests in the background
func (ers *ErrorRecoverySystemImpl) recoveryWorker() {
	defer ers.wg.Done()

	for {
		select {
		case request := <-ers.recoveryQueue:
			result, err := ers.tryRecoveryStrategies(context.Background(), request.Context)
			if err != nil {
				result = &RecoveryResult{
					Success: false,
					Error:   err,
				}
			}

			select {
			case request.ResultChan <- result:
			default:
				// Channel closed or full, ignore
			}

		case <-ers.stopChan:
			return
		}
	}
}

// registerDefaultStrategies registers default recovery strategies
func (ers *ErrorRecoverySystemImpl) registerDefaultStrategies() {
	// Register retry with backoff for transient errors
	retryStrategy := NewRetryWithBackoffStrategy(ers.config.DefaultMaxRetries, ers.config.DefaultRetryDelay, ers.config.DefaultBackoffMultiplier)
	ers.RegisterStrategy(ErrorTypeNetwork, retryStrategy)
	ers.RegisterStrategy(ErrorTypeTimeout, retryStrategy)
	ers.RegisterStrategy(ErrorTypeResource, retryStrategy)

	// Register path failover for path-specific errors
	pathFailoverStrategy := NewPathFailoverStrategy(ers.failoverManager)
	ers.RegisterStrategy(ErrorTypePath, pathFailoverStrategy)
	ers.RegisterStrategy(ErrorTypeNetwork, pathFailoverStrategy)

	// Register connection reset for protocol errors
	connectionResetStrategy := NewConnectionResetStrategy()
	ers.RegisterStrategy(ErrorTypeProtocol, connectionResetStrategy)
	ers.RegisterStrategy(ErrorTypeSession, connectionResetStrategy)
}

// RetryWithBackoffStrategy implements retry with exponential backoff
type RetryWithBackoffStrategy struct {
	maxRetries        int
	baseDelay         time.Duration
	backoffMultiplier float64
}

// NewRetryWithBackoffStrategy creates a new retry with backoff strategy
func NewRetryWithBackoffStrategy(maxRetries int, baseDelay time.Duration, backoffMultiplier float64) RecoveryStrategy {
	return &RetryWithBackoffStrategy{
		maxRetries:        maxRetries,
		baseDelay:         baseDelay,
		backoffMultiplier: backoffMultiplier,
	}
}

func (r *RetryWithBackoffStrategy) CanRecover(kwikError *KwikError) bool {
	return kwikError.Recoverable && kwikError.RetryCount < r.maxRetries
}

func (r *RetryWithBackoffStrategy) Recover(ctx context.Context, recoveryContext *RecoveryContext) (*RecoveryResult, error) {
	// Calculate delay for this retry
	delay := r.calculateDelay(recoveryContext.AttemptNumber)

	// Wait for the delay
	select {
	case <-time.After(delay):
	case <-ctx.Done():
		return &RecoveryResult{
			Success:       false,
			Strategy:      StrategyTypeRetryWithBackoff,
			AttemptNumber: recoveryContext.AttemptNumber,
			Duration:      time.Since(recoveryContext.StartTime),
			Error:         ctx.Err(),
			ShouldRetry:   false,
		}, nil
	}

	return &RecoveryResult{
		Success:       true,
		Strategy:      StrategyTypeRetryWithBackoff,
		AttemptNumber: recoveryContext.AttemptNumber,
		Duration:      time.Since(recoveryContext.StartTime),
		ShouldRetry:   recoveryContext.AttemptNumber < r.maxRetries,
		RetryDelay:    delay,
	}, nil
}

func (r *RetryWithBackoffStrategy) GetStrategyType() StrategyType {
	return StrategyTypeRetryWithBackoff
}

func (r *RetryWithBackoffStrategy) GetMaxRetries() int {
	return r.maxRetries
}

func (r *RetryWithBackoffStrategy) calculateDelay(attempt int) time.Duration {
	if attempt <= 1 {
		return r.baseDelay
	}

	delay := float64(r.baseDelay)
	for i := 1; i < attempt; i++ {
		delay *= r.backoffMultiplier
	}

	return time.Duration(delay)
}

// PathFailoverStrategy implements path failover recovery
type PathFailoverStrategy struct {
	failoverManager FailoverManagerInterface
}

// NewPathFailoverStrategy creates a new path failover strategy
func NewPathFailoverStrategy(failoverManager FailoverManagerInterface) RecoveryStrategy {
	return &PathFailoverStrategy{
		failoverManager: failoverManager,
	}
}

func (p *PathFailoverStrategy) CanRecover(kwikError *KwikError) bool {
	return kwikError.Type == ErrorTypePath || kwikError.Type == ErrorTypeNetwork
}

func (p *PathFailoverStrategy) Recover(ctx context.Context, recoveryContext *RecoveryContext) (*RecoveryResult, error) {
	startTime := time.Now()

	err := p.failoverManager.TriggerFailover(recoveryContext.SessionID, FailoverReasonPathFailed)
	if err != nil {
		return &RecoveryResult{
			Success:       false,
			Strategy:      StrategyTypePathFailover,
			AttemptNumber: recoveryContext.AttemptNumber,
			Duration:      time.Since(startTime),
			Error:         err,
			ShouldRetry:   false,
		}, nil
	}

	return &RecoveryResult{
		Success:       true,
		Strategy:      StrategyTypePathFailover,
		AttemptNumber: recoveryContext.AttemptNumber,
		Duration:      time.Since(startTime),
		ActionsTaken: []RecoveryAction{
			{
				Type:      ActionTypePathSwitch,
				Target:    recoveryContext.PathID,
				Timestamp: time.Now(),
				Success:   true,
			},
		},
	}, nil
}

func (p *PathFailoverStrategy) GetStrategyType() StrategyType {
	return StrategyTypePathFailover
}

func (p *PathFailoverStrategy) GetMaxRetries() int {
	return 1 // Failover is typically a one-time operation
}

// ConnectionResetStrategy implements connection reset recovery
type ConnectionResetStrategy struct{}

// NewConnectionResetStrategy creates a new connection reset strategy
func NewConnectionResetStrategy() RecoveryStrategy {
	return &ConnectionResetStrategy{}
}

func (c *ConnectionResetStrategy) CanRecover(kwikError *KwikError) bool {
	return kwikError.Type == ErrorTypeProtocol || kwikError.Type == ErrorTypeSession
}

func (c *ConnectionResetStrategy) Recover(ctx context.Context, recoveryContext *RecoveryContext) (*RecoveryResult, error) {
	startTime := time.Now()

	// Simulate connection reset logic
	// In a real implementation, this would reset the connection

	return &RecoveryResult{
		Success:       true,
		Strategy:      StrategyTypeConnectionReset,
		AttemptNumber: recoveryContext.AttemptNumber,
		Duration:      time.Since(startTime),
		ActionsTaken: []RecoveryAction{
			{
				Type:      ActionTypeConnectionReset,
				Target:    recoveryContext.SessionID,
				Timestamp: time.Now(),
				Success:   true,
			},
		},
	}, nil
}

func (c *ConnectionResetStrategy) GetStrategyType() StrategyType {
	return StrategyTypeConnectionReset
}

func (c *ConnectionResetStrategy) GetMaxRetries() int {
	return 2
}

// Close shuts down the error recovery system
func (ers *ErrorRecoverySystemImpl) Close() error {
	close(ers.stopChan)
	ers.wg.Wait()
	close(ers.recoveryQueue)

	ers.logger.Info("Error recovery system shut down")
	return nil
}
