package data

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"kwik/internal/utils"
)

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

// AggregationErrorHandler handles errors specific to secondary stream aggregation
type AggregationErrorHandler struct {
	// Error handling callbacks
	onOffsetConflict     func(kwikStreamID uint64, offset uint64, sources []string) error
	onDataCorruption     func(kwikStreamID uint64, corruptedData []byte) error
	onAggregationTimeout func(kwikStreamID uint64, pendingOffsets []uint64) error
	onBufferOverflow     func(kwikStreamID uint64, bufferSize, maxSize int) error
	onReorderingFailure  func(kwikStreamID uint64, expectedOffset, receivedOffset uint64) error
	
	// Fallback strategy
	fallbackStrategy AggregationFallbackStrategy
	
	// Recovery mechanisms
	recoveryConfig *AggregationRecoveryConfig
	
	// Statistics
	stats *AggregationErrorStats
	
	// Configuration
	config *AggregationErrorConfig
	
	// Synchronization
	mutex sync.RWMutex
	
	// Logger
	logger AggregationLogger
	
	// Active recovery operations
	activeRecoveries map[uint64]*RecoveryOperation // kwikStreamID -> recovery
	recoveryMutex    sync.RWMutex
}

// AggregationFallbackStrategy defines different fallback strategies for aggregation errors
type AggregationFallbackStrategy int

const (
	FallbackIgnoreCorrupted AggregationFallbackStrategy = iota
	FallbackRequestRetransmission
	FallbackCloseStream
	FallbackIsolateSource
	FallbackUseLastKnownGood
	FallbackResetStream
)

// AggregationRecoveryConfig contains configuration for error recovery
type AggregationRecoveryConfig struct {
	EnableAutoRecovery       bool          // Whether to enable automatic recovery
	MaxRecoveryAttempts      int           // Maximum recovery attempts per error
	RecoveryTimeout          time.Duration // Timeout for recovery operations
	BufferRecoveryThreshold  float64       // Buffer usage threshold to trigger recovery (0.0-1.0)
	OffsetToleranceWindow    uint64        // Tolerance window for offset conflicts
	CorruptionDetectionLevel int           // Level of corruption detection (0=none, 3=strict)
	RetransmissionTimeout    time.Duration // Timeout for retransmission requests
	IsolationDuration        time.Duration // Duration to isolate problematic sources
}

// AggregationErrorConfig contains configuration for the error handler
type AggregationErrorConfig struct {
	EnableErrorRecovery     bool          // Whether to enable error recovery
	ErrorReportingLevel     ErrorLevel    // Level of error reporting
	MaxErrorHistory         int           // Maximum number of errors to keep in history
	ErrorCleanupInterval    time.Duration // Interval for cleaning up old errors
	EnableMetrics           bool          // Whether to collect error metrics
	EnableCorruptionCheck   bool          // Whether to enable data corruption checks
	MaxPendingRecoveries    int           // Maximum concurrent recovery operations
	RecoveryQueueSize       int           // Size of recovery operation queue
}

// AggregationErrorStats contains statistics about aggregation errors
type AggregationErrorStats struct {
	TotalErrors              int64                         // Total number of errors encountered
	ErrorsByType             map[string]int64              // Count of errors by type
	OffsetConflicts          int64                         // Number of offset conflicts
	DataCorruptions          int64                         // Number of data corruption events
	AggregationTimeouts      int64                         // Number of aggregation timeouts
	BufferOverflows          int64                         // Number of buffer overflows
	ReorderingFailures       int64                         // Number of reordering failures
	RecoveryAttempts         int64                         // Total recovery attempts
	RecoverySuccessful       int64                         // Successful recoveries
	RetransmissionRequests   int64                         // Number of retransmission requests
	SourceIsolations         int64                         // Number of source isolations
	StreamResets             int64                         // Number of stream resets
	LastErrorTime            time.Time                     // Time of the last error
	ErrorHistory             []*AggregationErrorRecord     // History of recent errors
	ActiveRecoveryOperations int                           // Number of active recovery operations
	mutex                    sync.RWMutex                  // Synchronization for stats
}

// AggregationErrorRecord represents a single aggregation error occurrence
type AggregationErrorRecord struct {
	Timestamp         time.Time                   // When the error occurred
	ErrorType         AggregationErrorType        // Type of error
	ErrorMessage      string                      // Error message
	KwikStreamID      uint64                      // Associated KWIK stream ID
	SecondaryStreamID uint64                      // Associated secondary stream ID (if applicable)
	PathID            string                      // Associated path ID
	Offset            uint64                      // Offset where error occurred
	DataSize          int                         // Size of data involved in error
	RecoveryAttempts  int                         // Number of recovery attempts
	Resolved          bool                        // Whether the error was resolved
	ResolutionMethod  string                      // How the error was resolved
	FallbackUsed      AggregationFallbackStrategy // Fallback strategy used
}

// AggregationErrorType represents different types of aggregation errors
type AggregationErrorType int

const (
	ErrorTypeOffsetConflict AggregationErrorType = iota
	ErrorTypeDataCorruption
	ErrorTypeAggregationTimeout
	ErrorTypeBufferOverflow
	ErrorTypeReorderingFailure
	ErrorTypeInvalidData
	ErrorTypeStreamClosed
	ErrorTypeSourceUnavailable
)

// AggregationLogger interface for aggregation error logging
type AggregationLogger interface {
	Debug(msg string, args ...interface{})
	Info(msg string, args ...interface{})
	Warn(msg string, args ...interface{})
	Error(msg string, args ...interface{})
	Critical(msg string, args ...interface{})
}

// DefaultAggregationLogger provides a simple logger implementation
type DefaultAggregationLogger struct{}

func (l *DefaultAggregationLogger) Debug(msg string, args ...interface{})    { log.Printf("[DEBUG] "+msg, args...) }
func (l *DefaultAggregationLogger) Info(msg string, args ...interface{})     { log.Printf("[INFO] "+msg, args...) }
func (l *DefaultAggregationLogger) Warn(msg string, args ...interface{})     { log.Printf("[WARN] "+msg, args...) }
func (l *DefaultAggregationLogger) Error(msg string, args ...interface{})    { log.Printf("[ERROR] "+msg, args...) }
func (l *DefaultAggregationLogger) Critical(msg string, args ...interface{}) { log.Printf("[CRITICAL] "+msg, args...) }

// RecoveryOperation represents an active recovery operation
type RecoveryOperation struct {
	ID               string                      // Unique recovery operation ID
	KwikStreamID     uint64                      // Stream being recovered
	ErrorType        AggregationErrorType        // Type of error being recovered from
	StartTime        time.Time                   // When recovery started
	Timeout          time.Time                   // When recovery times out
	Attempts         int                         // Number of attempts made
	Strategy         AggregationFallbackStrategy // Recovery strategy being used
	Context          context.Context             // Recovery context
	Cancel           context.CancelFunc          // Function to cancel recovery
	Status           RecoveryStatus              // Current status
	LastUpdate       time.Time                   // Last status update
	ErrorDetails     map[string]interface{}      // Additional error details
}

// RecoveryStatus represents the status of a recovery operation
type RecoveryStatus int

const (
	RecoveryStatusPending RecoveryStatus = iota
	RecoveryStatusInProgress
	RecoveryStatusCompleted
	RecoveryStatusFailed
	RecoveryStatusCancelled
	RecoveryStatusTimedOut
)

// AggregationErrorContext provides context for error handling
type AggregationErrorContext struct {
	KwikStreamID      uint64                 // KWIK stream ID involved in the error
	SecondaryStreamID uint64                 // Secondary stream ID (if applicable)
	PathID            string                 // Path ID where the error occurred
	Offset            uint64                 // Offset where error occurred
	DataSize          int                    // Size of data involved
	ExpectedOffset    uint64                 // Expected offset (for reordering errors)
	Sources           []string               // List of sources involved
	Timestamp         time.Time              // When the error occurred
	Metadata          map[string]interface{} // Additional context metadata
}

// NewAggregationErrorHandler creates a new aggregation error handler
func NewAggregationErrorHandler(config *AggregationErrorConfig) *AggregationErrorHandler {
	if config == nil {
		config = &AggregationErrorConfig{
			EnableErrorRecovery:   true,
			ErrorReportingLevel:   ErrorLevelError,
			MaxErrorHistory:       200,
			ErrorCleanupInterval:  2 * time.Hour,
			EnableMetrics:         true,
			EnableCorruptionCheck: true,
			MaxPendingRecoveries:  10,
			RecoveryQueueSize:     50,
		}
	}
	
	recoveryConfig := &AggregationRecoveryConfig{
		EnableAutoRecovery:       true,
		MaxRecoveryAttempts:      3,
		RecoveryTimeout:          30 * time.Second,
		BufferRecoveryThreshold:  0.8,
		OffsetToleranceWindow:    1024,
		CorruptionDetectionLevel: 2,
		RetransmissionTimeout:    10 * time.Second,
		IsolationDuration:        5 * time.Minute,
	}
	
	stats := &AggregationErrorStats{
		ErrorsByType: make(map[string]int64),
		ErrorHistory: make([]*AggregationErrorRecord, 0, config.MaxErrorHistory),
	}
	
	handler := &AggregationErrorHandler{
		fallbackStrategy:  FallbackRequestRetransmission,
		recoveryConfig:    recoveryConfig,
		stats:             stats,
		config:            config,
		logger:            &DefaultAggregationLogger{},
		activeRecoveries:  make(map[uint64]*RecoveryOperation),
	}
	
	// Set default error handlers
	handler.setDefaultErrorHandlers()
	
	return handler
}

// SetLogger sets a custom logger for the error handler
func (h *AggregationErrorHandler) SetLogger(logger AggregationLogger) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.logger = logger
}

// SetFallbackStrategy sets the fallback strategy for aggregation errors
func (h *AggregationErrorHandler) SetFallbackStrategy(strategy AggregationFallbackStrategy) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.fallbackStrategy = strategy
}

// SetOffsetConflictHandler sets a custom handler for offset conflicts
func (h *AggregationErrorHandler) SetOffsetConflictHandler(handler func(kwikStreamID uint64, offset uint64, sources []string) error) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.onOffsetConflict = handler
}

// SetDataCorruptionHandler sets a custom handler for data corruption
func (h *AggregationErrorHandler) SetDataCorruptionHandler(handler func(kwikStreamID uint64, corruptedData []byte) error) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.onDataCorruption = handler
}

// SetAggregationTimeoutHandler sets a custom handler for aggregation timeouts
func (h *AggregationErrorHandler) SetAggregationTimeoutHandler(handler func(kwikStreamID uint64, pendingOffsets []uint64) error) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.onAggregationTimeout = handler
}

// SetBufferOverflowHandler sets a custom handler for buffer overflows
func (h *AggregationErrorHandler) SetBufferOverflowHandler(handler func(kwikStreamID uint64, bufferSize, maxSize int) error) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.onBufferOverflow = handler
}

// SetReorderingFailureHandler sets a custom handler for reordering failures
func (h *AggregationErrorHandler) SetReorderingFailureHandler(handler func(kwikStreamID uint64, expectedOffset, receivedOffset uint64) error) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.onReorderingFailure = handler
}

// HandleAggregationError handles an aggregation error with appropriate strategy
func (h *AggregationErrorHandler) HandleAggregationError(err error, context *AggregationErrorContext) error {
	if err == nil {
		return nil
	}
	
	// Determine error type
	errorType := h.classifyError(err)
	
	// Record the error
	h.recordError(err, errorType, context)
	
	// Handle specific error types
	switch errorType {
	case ErrorTypeOffsetConflict:
		return h.handleOffsetConflict(err, context)
	case ErrorTypeDataCorruption:
		return h.handleDataCorruption(err, context)
	case ErrorTypeAggregationTimeout:
		return h.handleAggregationTimeout(err, context)
	case ErrorTypeBufferOverflow:
		return h.handleBufferOverflow(err, context)
	case ErrorTypeReorderingFailure:
		return h.handleReorderingFailure(err, context)
	default:
		return h.handleGenericAggregationError(err, context)
	}
}

// classifyError classifies an error into an aggregation error type
func (h *AggregationErrorHandler) classifyError(err error) AggregationErrorType {
	if kwikErr, ok := err.(*utils.KwikError); ok {
		switch kwikErr.Code {
		case utils.ErrInvalidFrame:
			if kwikErr.Message == "duplicate data at offset" {
				return ErrorTypeOffsetConflict
			}
			return ErrorTypeInvalidData
		case utils.ErrStreamCreationFailed:
			if kwikErr.Message == "maximum pending data reached" {
				return ErrorTypeBufferOverflow
			}
			return ErrorTypeStreamClosed
		default:
			return ErrorTypeInvalidData
		}
	}
	
	// Check error message for specific patterns
	errMsg := err.Error()
	switch {
	case contains(errMsg, "offset conflict") || contains(errMsg, "duplicate data"):
		return ErrorTypeOffsetConflict
	case contains(errMsg, "corruption") || contains(errMsg, "checksum"):
		return ErrorTypeDataCorruption
	case contains(errMsg, "timeout") || contains(errMsg, "deadline"):
		return ErrorTypeAggregationTimeout
	case contains(errMsg, "buffer") || contains(errMsg, "overflow"):
		return ErrorTypeBufferOverflow
	case contains(errMsg, "reorder") || contains(errMsg, "sequence"):
		return ErrorTypeReorderingFailure
	default:
		return ErrorTypeInvalidData
	}
}

// handleOffsetConflict handles offset conflict errors
func (h *AggregationErrorHandler) handleOffsetConflict(err error, context *AggregationErrorContext) error {
	h.logger.Warn("Offset conflict detected: stream=%d, offset=%d", context.KwikStreamID, context.Offset)
	
	// Update statistics
	h.stats.mutex.Lock()
	h.stats.OffsetConflicts++
	h.stats.mutex.Unlock()
	
	// Try custom handler first
	if h.onOffsetConflict != nil {
		if handlerErr := h.onOffsetConflict(context.KwikStreamID, context.Offset, context.Sources); handlerErr == nil {
			h.recordResolution(err, context, "custom_handler")
			return nil
		}
	}
	
	// Apply fallback strategy
	return h.applyFallbackStrategy(err, context, ErrorTypeOffsetConflict)
}

// handleDataCorruption handles data corruption errors
func (h *AggregationErrorHandler) handleDataCorruption(err error, context *AggregationErrorContext) error {
	h.logger.Error("Data corruption detected: stream=%d, offset=%d", context.KwikStreamID, context.Offset)
	
	// Update statistics
	h.stats.mutex.Lock()
	h.stats.DataCorruptions++
	h.stats.mutex.Unlock()
	
	// Try custom handler first
	if h.onDataCorruption != nil {
		// Extract corrupted data from context if available
		var corruptedData []byte
		if context.Metadata != nil {
			if data, ok := context.Metadata["corrupted_data"].([]byte); ok {
				corruptedData = data
			}
		}
		
		if handlerErr := h.onDataCorruption(context.KwikStreamID, corruptedData); handlerErr == nil {
			h.recordResolution(err, context, "custom_handler")
			return nil
		}
	}
	
	// Data corruption is serious, apply fallback strategy
	return h.applyFallbackStrategy(err, context, ErrorTypeDataCorruption)
}

// handleAggregationTimeout handles aggregation timeout errors
func (h *AggregationErrorHandler) handleAggregationTimeout(err error, context *AggregationErrorContext) error {
	h.logger.Warn("Aggregation timeout: stream=%d", context.KwikStreamID)
	
	// Update statistics
	h.stats.mutex.Lock()
	h.stats.AggregationTimeouts++
	h.stats.mutex.Unlock()
	
	// Try custom handler first
	if h.onAggregationTimeout != nil {
		// Extract pending offsets from context if available
		var pendingOffsets []uint64
		if context.Metadata != nil {
			if offsets, ok := context.Metadata["pending_offsets"].([]uint64); ok {
				pendingOffsets = offsets
			}
		}
		
		if handlerErr := h.onAggregationTimeout(context.KwikStreamID, pendingOffsets); handlerErr == nil {
			h.recordResolution(err, context, "custom_handler")
			return nil
		}
	}
	
	// Apply fallback strategy
	return h.applyFallbackStrategy(err, context, ErrorTypeAggregationTimeout)
}

// handleBufferOverflow handles buffer overflow errors
func (h *AggregationErrorHandler) handleBufferOverflow(err error, context *AggregationErrorContext) error {
	h.logger.Error("Buffer overflow: stream=%d", context.KwikStreamID)
	
	// Update statistics
	h.stats.mutex.Lock()
	h.stats.BufferOverflows++
	h.stats.mutex.Unlock()
	
	// Try custom handler first
	if h.onBufferOverflow != nil {
		// Extract buffer sizes from context if available
		bufferSize := context.DataSize
		maxSize := bufferSize
		if context.Metadata != nil {
			if size, ok := context.Metadata["max_buffer_size"].(int); ok {
				maxSize = size
			}
		}
		
		if handlerErr := h.onBufferOverflow(context.KwikStreamID, bufferSize, maxSize); handlerErr == nil {
			h.recordResolution(err, context, "custom_handler")
			return nil
		}
	}
	
	// Buffer overflow requires immediate action
	return h.applyFallbackStrategy(err, context, ErrorTypeBufferOverflow)
}

// handleReorderingFailure handles reordering failure errors
func (h *AggregationErrorHandler) handleReorderingFailure(err error, context *AggregationErrorContext) error {
	h.logger.Warn("Reordering failure: stream=%d, expected=%d, received=%d", 
		context.KwikStreamID, context.ExpectedOffset, context.Offset)
	
	// Update statistics
	h.stats.mutex.Lock()
	h.stats.ReorderingFailures++
	h.stats.mutex.Unlock()
	
	// Try custom handler first
	if h.onReorderingFailure != nil {
		if handlerErr := h.onReorderingFailure(context.KwikStreamID, context.ExpectedOffset, context.Offset); handlerErr == nil {
			h.recordResolution(err, context, "custom_handler")
			return nil
		}
	}
	
	// Apply fallback strategy
	return h.applyFallbackStrategy(err, context, ErrorTypeReorderingFailure)
}

// handleGenericAggregationError handles generic aggregation errors
func (h *AggregationErrorHandler) handleGenericAggregationError(err error, context *AggregationErrorContext) error {
	h.logger.Error("Generic aggregation error: %s", err.Error())
	
	// Apply fallback strategy
	return h.applyFallbackStrategy(err, context, ErrorTypeInvalidData)
}

// applyFallbackStrategy applies the configured fallback strategy
func (h *AggregationErrorHandler) applyFallbackStrategy(err error, context *AggregationErrorContext, errorType AggregationErrorType) error {
	strategy := h.fallbackStrategy
	
	h.logger.Info("Applying fallback strategy %d for error type %d", strategy, errorType)
	
	switch strategy {
	case FallbackIgnoreCorrupted:
		return h.ignoreCorruptedData(err, context)
	case FallbackRequestRetransmission:
		return h.requestRetransmission(err, context)
	case FallbackCloseStream:
		return h.closeStream(err, context)
	case FallbackIsolateSource:
		return h.isolateSource(err, context)
	case FallbackUseLastKnownGood:
		return h.useLastKnownGood(err, context)
	case FallbackResetStream:
		return h.resetStream(err, context)
	default:
		h.logger.Error("Unknown fallback strategy: %d", strategy)
		return err
	}
}

// ignoreCorruptedData implements the ignore corrupted data fallback
func (h *AggregationErrorHandler) ignoreCorruptedData(err error, context *AggregationErrorContext) error {
	h.logger.Info("Ignoring corrupted data: stream=%d, offset=%d", context.KwikStreamID, context.Offset)
	h.recordResolution(err, context, "ignore_corrupted")
	return nil // Ignore the error
}

// requestRetransmission implements the request retransmission fallback
func (h *AggregationErrorHandler) requestRetransmission(err error, context *AggregationErrorContext) error {
	h.logger.Info("Requesting retransmission: stream=%d, offset=%d", context.KwikStreamID, context.Offset)
	
	// Update statistics
	h.stats.mutex.Lock()
	h.stats.RetransmissionRequests++
	h.stats.mutex.Unlock()
	
	// Start recovery operation
	if h.config.EnableErrorRecovery {
		h.startRecoveryOperation(context, FallbackRequestRetransmission)
	}
	
	h.recordResolution(err, context, "request_retransmission")
	return nil // Error will be resolved by retransmission
}

// closeStream implements the close stream fallback
func (h *AggregationErrorHandler) closeStream(err error, context *AggregationErrorContext) error {
	h.logger.Warn("Closing stream due to error: stream=%d", context.KwikStreamID)
	h.recordResolution(err, context, "close_stream")
	
	// Return a special error indicating stream should be closed
	return &StreamCloseError{
		OriginalError: err,
		StreamID:      context.KwikStreamID,
		Reason:        "aggregation_error",
	}
}

// isolateSource implements the isolate source fallback
func (h *AggregationErrorHandler) isolateSource(err error, context *AggregationErrorContext) error {
	h.logger.Warn("Isolating source: stream=%d, path=%s", context.KwikStreamID, context.PathID)
	
	// Update statistics
	h.stats.mutex.Lock()
	h.stats.SourceIsolations++
	h.stats.mutex.Unlock()
	
	// Start recovery operation
	if h.config.EnableErrorRecovery {
		h.startRecoveryOperation(context, FallbackIsolateSource)
	}
	
	h.recordResolution(err, context, "isolate_source")
	return nil // Source will be isolated
}

// useLastKnownGood implements the use last known good fallback
func (h *AggregationErrorHandler) useLastKnownGood(err error, context *AggregationErrorContext) error {
	h.logger.Info("Using last known good data: stream=%d", context.KwikStreamID)
	h.recordResolution(err, context, "use_last_known_good")
	return nil // Use last known good data
}

// resetStream implements the reset stream fallback
func (h *AggregationErrorHandler) resetStream(err error, context *AggregationErrorContext) error {
	h.logger.Warn("Resetting stream: stream=%d", context.KwikStreamID)
	
	// Update statistics
	h.stats.mutex.Lock()
	h.stats.StreamResets++
	h.stats.mutex.Unlock()
	
	h.recordResolution(err, context, "reset_stream")
	
	// Return a special error indicating stream should be reset
	return &StreamResetError{
		OriginalError: err,
		StreamID:      context.KwikStreamID,
		Reason:        "aggregation_error",
	}
}

// startRecoveryOperation starts a recovery operation for the given context
func (h *AggregationErrorHandler) startRecoveryOperation(errorContext *AggregationErrorContext, strategy AggregationFallbackStrategy) {
	h.recoveryMutex.Lock()
	defer h.recoveryMutex.Unlock()
	
	// Check if recovery is already in progress for this stream
	if _, exists := h.activeRecoveries[errorContext.KwikStreamID]; exists {
		h.logger.Debug("Recovery already in progress for stream %d", errorContext.KwikStreamID)
		return
	}
	
	// Check recovery limits
	if len(h.activeRecoveries) >= h.config.MaxPendingRecoveries {
		h.logger.Warn("Maximum pending recoveries reached, skipping recovery for stream %d", errorContext.KwikStreamID)
		return
	}
	
	// Create recovery operation
	ctx, cancel := context.WithTimeout(context.Background(), h.recoveryConfig.RecoveryTimeout)
	recovery := &RecoveryOperation{
		ID:           fmt.Sprintf("recovery_%d_%d", errorContext.KwikStreamID, time.Now().UnixNano()),
		KwikStreamID: errorContext.KwikStreamID,
		ErrorType:    h.classifyError(fmt.Errorf("recovery_operation")),
		StartTime:    time.Now(),
		Timeout:      time.Now().Add(h.recoveryConfig.RecoveryTimeout),
		Attempts:     0,
		Strategy:     strategy,
		Context:      ctx,
		Cancel:       cancel,
		Status:       RecoveryStatusPending,
		LastUpdate:   time.Now(),
		ErrorDetails: make(map[string]interface{}),
	}
	
	h.activeRecoveries[errorContext.KwikStreamID] = recovery
	
	// Start recovery in background
	go h.executeRecovery(recovery)
	
	h.logger.Info("Started recovery operation %s for stream %d", recovery.ID, errorContext.KwikStreamID)
}

// executeRecovery executes a recovery operation
func (h *AggregationErrorHandler) executeRecovery(recovery *RecoveryOperation) {
	defer func() {
		h.recoveryMutex.Lock()
		delete(h.activeRecoveries, recovery.KwikStreamID)
		h.recoveryMutex.Unlock()
		recovery.Cancel()
	}()
	
	recovery.Status = RecoveryStatusInProgress
	recovery.LastUpdate = time.Now()
	
	h.stats.mutex.Lock()
	h.stats.RecoveryAttempts++
	h.stats.mutex.Unlock()
	
	// Execute recovery based on strategy
	var err error
	switch recovery.Strategy {
	case FallbackRequestRetransmission:
		err = h.executeRetransmissionRecovery(recovery)
	case FallbackIsolateSource:
		err = h.executeSourceIsolationRecovery(recovery)
	default:
		err = fmt.Errorf("unsupported recovery strategy: %d", recovery.Strategy)
	}
	
	// Update recovery status
	if err != nil {
		recovery.Status = RecoveryStatusFailed
		h.logger.Error("Recovery operation %s failed: %s", recovery.ID, err.Error())
	} else {
		recovery.Status = RecoveryStatusCompleted
		h.stats.mutex.Lock()
		h.stats.RecoverySuccessful++
		h.stats.mutex.Unlock()
		h.logger.Info("Recovery operation %s completed successfully", recovery.ID)
	}
	
	recovery.LastUpdate = time.Now()
}

// executeRetransmissionRecovery executes retransmission recovery
func (h *AggregationErrorHandler) executeRetransmissionRecovery(recovery *RecoveryOperation) error {
	h.logger.Debug("Executing retransmission recovery for stream %d", recovery.KwikStreamID)
	
	// Wait for retransmission timeout
	select {
	case <-time.After(h.recoveryConfig.RetransmissionTimeout):
		return nil // Assume retransmission completed
	case <-recovery.Context.Done():
		return recovery.Context.Err()
	}
}

// executeSourceIsolationRecovery executes source isolation recovery
func (h *AggregationErrorHandler) executeSourceIsolationRecovery(recovery *RecoveryOperation) error {
	h.logger.Debug("Executing source isolation recovery for stream %d", recovery.KwikStreamID)
	
	// Wait for isolation duration
	select {
	case <-time.After(h.recoveryConfig.IsolationDuration):
		return nil // Assume isolation completed
	case <-recovery.Context.Done():
		return recovery.Context.Err()
	}
}

// recordError records an error in the statistics and history
func (h *AggregationErrorHandler) recordError(err error, errorType AggregationErrorType, context *AggregationErrorContext) {
	if !h.config.EnableMetrics {
		return
	}
	
	h.stats.mutex.Lock()
	defer h.stats.mutex.Unlock()
	
	h.stats.TotalErrors++
	h.stats.LastErrorTime = time.Now()
	
	// Record error by type
	errorTypeStr := h.errorTypeToString(errorType)
	h.stats.ErrorsByType[errorTypeStr]++
	
	// Add to error history
	record := &AggregationErrorRecord{
		Timestamp:         time.Now(),
		ErrorType:         errorType,
		ErrorMessage:      err.Error(),
		KwikStreamID:      context.KwikStreamID,
		SecondaryStreamID: context.SecondaryStreamID,
		PathID:            context.PathID,
		Offset:            context.Offset,
		DataSize:          context.DataSize,
		RecoveryAttempts:  0,
		Resolved:          false,
		FallbackUsed:      h.fallbackStrategy,
	}
	
	// Maintain history size limit
	if len(h.stats.ErrorHistory) >= h.config.MaxErrorHistory {
		h.stats.ErrorHistory = h.stats.ErrorHistory[1:]
	}
	h.stats.ErrorHistory = append(h.stats.ErrorHistory, record)
}

// recordResolution records that an error was resolved
func (h *AggregationErrorHandler) recordResolution(err error, context *AggregationErrorContext, method string) {
	if !h.config.EnableMetrics {
		return
	}
	
	h.stats.mutex.Lock()
	defer h.stats.mutex.Unlock()
	
	// Mark the most recent matching error as resolved
	for i := len(h.stats.ErrorHistory) - 1; i >= 0; i-- {
		record := h.stats.ErrorHistory[i]
		if record.KwikStreamID == context.KwikStreamID &&
			record.Offset == context.Offset &&
			!record.Resolved {
			record.Resolved = true
			record.ResolutionMethod = method
			break
		}
	}
}

// GetErrorStats returns current error statistics
func (h *AggregationErrorHandler) GetErrorStats() *AggregationErrorStats {
	h.stats.mutex.RLock()
	defer h.stats.mutex.RUnlock()
	
	// Return a copy to avoid race conditions
	statsCopy := &AggregationErrorStats{
		TotalErrors:              h.stats.TotalErrors,
		ErrorsByType:             make(map[string]int64),
		OffsetConflicts:          h.stats.OffsetConflicts,
		DataCorruptions:          h.stats.DataCorruptions,
		AggregationTimeouts:      h.stats.AggregationTimeouts,
		BufferOverflows:          h.stats.BufferOverflows,
		ReorderingFailures:       h.stats.ReorderingFailures,
		RecoveryAttempts:         h.stats.RecoveryAttempts,
		RecoverySuccessful:       h.stats.RecoverySuccessful,
		RetransmissionRequests:   h.stats.RetransmissionRequests,
		SourceIsolations:         h.stats.SourceIsolations,
		StreamResets:             h.stats.StreamResets,
		LastErrorTime:            h.stats.LastErrorTime,
		ErrorHistory:             make([]*AggregationErrorRecord, len(h.stats.ErrorHistory)),
		ActiveRecoveryOperations: len(h.activeRecoveries),
	}
	
	for k, v := range h.stats.ErrorsByType {
		statsCopy.ErrorsByType[k] = v
	}
	
	copy(statsCopy.ErrorHistory, h.stats.ErrorHistory)
	
	return statsCopy
}

// setDefaultErrorHandlers sets up default error handling behavior
func (h *AggregationErrorHandler) setDefaultErrorHandlers() {
	// Default offset conflict handler
	h.onOffsetConflict = func(kwikStreamID uint64, offset uint64, sources []string) error {
		h.logger.Info("Default offset conflict handler: stream=%d, offset=%d, sources=%v", 
			kwikStreamID, offset, sources)
		return fmt.Errorf("offset conflict not resolved by default handler")
	}
	
	// Default data corruption handler
	h.onDataCorruption = func(kwikStreamID uint64, corruptedData []byte) error {
		h.logger.Error("Default data corruption handler: stream=%d, data_size=%d", 
			kwikStreamID, len(corruptedData))
		return fmt.Errorf("data corruption not resolved by default handler")
	}
	
	// Default aggregation timeout handler
	h.onAggregationTimeout = func(kwikStreamID uint64, pendingOffsets []uint64) error {
		h.logger.Warn("Default aggregation timeout handler: stream=%d, pending=%v", 
			kwikStreamID, pendingOffsets)
		return fmt.Errorf("aggregation timeout not resolved by default handler")
	}
	
	// Default buffer overflow handler
	h.onBufferOverflow = func(kwikStreamID uint64, bufferSize, maxSize int) error {
		h.logger.Error("Default buffer overflow handler: stream=%d, size=%d, max=%d", 
			kwikStreamID, bufferSize, maxSize)
		return fmt.Errorf("buffer overflow not resolved by default handler")
	}
	
	// Default reordering failure handler
	h.onReorderingFailure = func(kwikStreamID uint64, expectedOffset, receivedOffset uint64) error {
		h.logger.Warn("Default reordering failure handler: stream=%d, expected=%d, received=%d", 
			kwikStreamID, expectedOffset, receivedOffset)
		return fmt.Errorf("reordering failure not resolved by default handler")
	}
}

// errorTypeToString converts an error type to string
func (h *AggregationErrorHandler) errorTypeToString(errorType AggregationErrorType) string {
	switch errorType {
	case ErrorTypeOffsetConflict:
		return "OFFSET_CONFLICT"
	case ErrorTypeDataCorruption:
		return "DATA_CORRUPTION"
	case ErrorTypeAggregationTimeout:
		return "AGGREGATION_TIMEOUT"
	case ErrorTypeBufferOverflow:
		return "BUFFER_OVERFLOW"
	case ErrorTypeReorderingFailure:
		return "REORDERING_FAILURE"
	case ErrorTypeInvalidData:
		return "INVALID_DATA"
	case ErrorTypeStreamClosed:
		return "STREAM_CLOSED"
	case ErrorTypeSourceUnavailable:
		return "SOURCE_UNAVAILABLE"
	default:
		return "UNKNOWN"
	}
}

// StreamCloseError represents an error that requires closing a stream
type StreamCloseError struct {
	OriginalError error
	StreamID      uint64
	Reason        string
}

func (e *StreamCloseError) Error() string {
	return fmt.Sprintf("stream %d should be closed due to %s: %s", e.StreamID, e.Reason, e.OriginalError.Error())
}

// StreamResetError represents an error that requires resetting a stream
type StreamResetError struct {
	OriginalError error
	StreamID      uint64
	Reason        string
}

func (e *StreamResetError) Error() string {
	return fmt.Sprintf("stream %d should be reset due to %s: %s", e.StreamID, e.Reason, e.OriginalError.Error())
}

// IsStreamCloseError checks if an error is a stream close error
func IsStreamCloseError(err error) bool {
	_, ok := err.(*StreamCloseError)
	return ok
}

// IsStreamResetError checks if an error is a stream reset error
func IsStreamResetError(err error) bool {
	_, ok := err.(*StreamResetError)
	return ok
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || (len(s) > len(substr) && 
		(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || 
		 func() bool {
			 for i := 1; i <= len(s)-len(substr); i++ {
				 if s[i:i+len(substr)] == substr {
					 return true
				 }
			 }
			 return false
		 }())))
}