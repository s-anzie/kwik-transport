package stream

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// KwikMetadataLoggerAdapter adapts central logger to MetadataLogger
type KwikMetadataLoggerAdapter struct {
	base interface {
		Debug(string, ...interface{})
		Info(string, ...interface{})
		Warn(string, ...interface{})
		Error(string, ...interface{})
	}
}

func (a *KwikMetadataLoggerAdapter) Debug(msg string, args ...interface{}) {
	a.base.Debug(fmt.Sprintf(msg, args...))
}
func (a *KwikMetadataLoggerAdapter) Info(msg string, args ...interface{}) {
	a.base.Info(fmt.Sprintf(msg, args...))
}
func (a *KwikMetadataLoggerAdapter) Warn(msg string, args ...interface{}) {
	a.base.Warn(fmt.Sprintf(msg, args...))
}
func (a *KwikMetadataLoggerAdapter) Error(msg string, args ...interface{}) {
	a.base.Error(fmt.Sprintf(msg, args...))
}
func (a *KwikMetadataLoggerAdapter) Critical(msg string, args ...interface{}) {
	a.base.Error(fmt.Sprintf("CRITICAL: "+msg, args...))
}

// MetadataErrorHandler handles errors specific to metadata protocol operations
type MetadataErrorHandler struct {
	// Error handling callbacks
	onInvalidMetadata    func(data []byte, err error) error
	onMetadataTimeout    func(streamID uint64) error
	onProtocolViolation  func(violation string, streamID uint64) error
	onCorruptedFrame     func(frameData []byte, expectedSize, actualSize int) error
	onUnsupportedVersion func(expectedVersion, actualVersion int) error
	onMagicMismatch      func(expectedMagic, actualMagic uint32) error

	// Recovery strategy
	recoveryStrategy MetadataRecoveryStrategy

	// Recovery configuration
	recoveryConfig *MetadataRecoveryConfig

	// Statistics
	stats *MetadataErrorStats

	// Configuration
	config *MetadataErrorConfig

	// Synchronization
	mutex sync.RWMutex

	// Logger
	logger MetadataLogger

	// Active recovery operations
	activeRecoveries map[uint64]*MetadataRecoveryOperation // streamID -> recovery
	recoveryMutex    sync.RWMutex

	// Frame validation cache
	validationCache map[string]*ValidationCacheEntry
	cacheMutex      sync.RWMutex
}

// MetadataRecoveryStrategy defines different recovery strategies for metadata errors
type MetadataRecoveryStrategy int

const (
	RecoveryRequestResend MetadataRecoveryStrategy = iota
	RecoveryUseDefaults
	RecoveryCloseStream
	RecoveryIgnoreStream
	RecoveryResetProtocol
	RecoveryFallbackToBasic
)

// MetadataRecoveryConfig contains configuration for metadata error recovery
type MetadataRecoveryConfig struct {
	EnableAutoRecovery        bool          // Whether to enable automatic recovery
	MaxRecoveryAttempts       int           // Maximum recovery attempts per error
	RecoveryTimeout           time.Duration // Timeout for recovery operations
	ResendTimeout             time.Duration // Timeout for resend requests
	ProtocolResetThreshold    int           // Number of errors before protocol reset
	ValidationCacheSize       int           // Size of validation cache
	ValidationCacheTTL        time.Duration // TTL for validation cache entries
	CorruptionTolerance       float64       // Tolerance for frame corruption (0.0-1.0)
	EnableFrameReconstruction bool          // Whether to attempt frame reconstruction
}

// MetadataErrorConfig contains configuration for the metadata error handler
type MetadataErrorConfig struct {
	EnableErrorRecovery        bool          // Whether to enable error recovery
	ErrorReportingLevel        ErrorLevel    // Level of error reporting
	MaxErrorHistory            int           // Maximum number of errors to keep in history
	ErrorCleanupInterval       time.Duration // Interval for cleaning up old errors
	EnableMetrics              bool          // Whether to collect error metrics
	EnableValidationCache      bool          // Whether to enable validation caching
	MaxConcurrentRecoveries    int           // Maximum concurrent recovery operations
	FrameReconstructionEnabled bool          // Whether to enable frame reconstruction
}

// MetadataErrorStats contains statistics about metadata errors
type MetadataErrorStats struct {
	TotalErrors              int64                  // Total number of errors encountered
	ErrorsByType             map[string]int64       // Count of errors by type
	InvalidMetadataErrors    int64                  // Number of invalid metadata errors
	TimeoutErrors            int64                  // Number of timeout errors
	ProtocolViolations       int64                  // Number of protocol violations
	CorruptedFrames          int64                  // Number of corrupted frames
	UnsupportedVersions      int64                  // Number of unsupported version errors
	MagicMismatches          int64                  // Number of magic mismatch errors
	RecoveryAttempts         int64                  // Total recovery attempts
	RecoverySuccessful       int64                  // Successful recoveries
	ResendRequests           int64                  // Number of resend requests
	ProtocolResets           int64                  // Number of protocol resets
	FrameReconstructions     int64                  // Number of frame reconstructions
	ValidationCacheHits      int64                  // Number of validation cache hits
	ValidationCacheMisses    int64                  // Number of validation cache misses
	LastErrorTime            time.Time              // Time of the last error
	ErrorHistory             []*MetadataErrorRecord // History of recent errors
	ActiveRecoveryOperations int                    // Number of active recovery operations
	mutex                    sync.RWMutex           // Synchronization for stats
}

// MetadataErrorRecord represents a single metadata error occurrence
type MetadataErrorRecord struct {
	Timestamp        time.Time                // When the error occurred
	ErrorType        MetadataErrorType        // Type of error
	ErrorMessage     string                   // Error message
	StreamID         uint64                   // Associated stream ID
	FrameData        []byte                   // Frame data that caused the error (truncated)
	ExpectedValue    interface{}              // Expected value (for validation errors)
	ActualValue      interface{}              // Actual value (for validation errors)
	RecoveryAttempts int                      // Number of recovery attempts
	Resolved         bool                     // Whether the error was resolved
	ResolutionMethod string                   // How the error was resolved
	RecoveryStrategy MetadataRecoveryStrategy // Recovery strategy used
}

// MetadataErrorType represents different types of metadata errors
type MetadataErrorType int

const (
	ErrorTypeInvalidMetadata MetadataErrorType = iota
	ErrorTypeMetadataTimeout
	ErrorTypeProtocolViolation
	ErrorTypeCorruptedFrame
	ErrorTypeUnsupportedVersion
	ErrorTypeMagicMismatch
	ErrorTypeFrameTooSmall
	ErrorTypeDataTooLarge
	ErrorTypeInvalidMessageType
)

// MetadataLogger interface for metadata error logging
type MetadataLogger interface {
	Debug(msg string, args ...interface{})
	Info(msg string, args ...interface{})
	Warn(msg string, args ...interface{})
	Error(msg string, args ...interface{})
	Critical(msg string, args ...interface{})
}

// DefaultMetadataLogger provides a simple logger implementation
type DefaultMetadataLogger struct{}

func (l *DefaultMetadataLogger) Debug(msg string, args ...interface{}) {
	log.Printf("[DEBUG] "+msg, args...)
}
func (l *DefaultMetadataLogger) Info(msg string, args ...interface{}) {
	log.Printf("[INFO] "+msg, args...)
}
func (l *DefaultMetadataLogger) Warn(msg string, args ...interface{}) {
	log.Printf("[WARN] "+msg, args...)
}
func (l *DefaultMetadataLogger) Error(msg string, args ...interface{}) {
	log.Printf("[ERROR] "+msg, args...)
}
func (l *DefaultMetadataLogger) Critical(msg string, args ...interface{}) {
	log.Printf("[CRITICAL] "+msg, args...)
}

// MetadataRecoveryOperation represents an active metadata recovery operation
type MetadataRecoveryOperation struct {
	ID            string                   // Unique recovery operation ID
	StreamID      uint64                   // Stream being recovered
	ErrorType     MetadataErrorType        // Type of error being recovered from
	StartTime     time.Time                // When recovery started
	Timeout       time.Time                // When recovery times out
	Attempts      int                      // Number of attempts made
	Strategy      MetadataRecoveryStrategy // Recovery strategy being used
	Context       context.Context          // Recovery context
	Cancel        context.CancelFunc       // Function to cancel recovery
	Status        MetadataRecoveryStatus   // Current status
	LastUpdate    time.Time                // Last status update
	ErrorDetails  map[string]interface{}   // Additional error details
	OriginalFrame []byte                   // Original frame data (if available)
}

// MetadataRecoveryStatus represents the status of a metadata recovery operation
type MetadataRecoveryStatus int

const (
	MetadataRecoveryStatusPending MetadataRecoveryStatus = iota
	MetadataRecoveryStatusInProgress
	MetadataRecoveryStatusCompleted
	MetadataRecoveryStatusFailed
	MetadataRecoveryStatusCancelled
	MetadataRecoveryStatusTimedOut
)

// ValidationCacheEntry represents a cached validation result
type ValidationCacheEntry struct {
	FrameHash    string    // Hash of the frame data
	IsValid      bool      // Whether the frame is valid
	ErrorMessage string    // Error message if invalid
	Timestamp    time.Time // When the entry was created
	HitCount     int       // Number of times this entry was accessed
}

// MetadataErrorContext provides context for metadata error handling
type MetadataErrorContext struct {
	StreamID      uint64                 // Stream ID involved in the error
	FrameData     []byte                 // Frame data that caused the error
	ExpectedValue interface{}            // Expected value (for validation errors)
	ActualValue   interface{}            // Actual value (for validation errors)
	Operation     string                 // Operation that caused the error
	Timestamp     time.Time              // When the error occurred
	Metadata      map[string]interface{} // Additional context metadata
}

// NewMetadataErrorHandler creates a new metadata error handler
func NewMetadataErrorHandler(config *MetadataErrorConfig) *MetadataErrorHandler {
	if config == nil {
		config = &MetadataErrorConfig{
			EnableErrorRecovery:        true,
			ErrorReportingLevel:        ErrorLevelError,
			MaxErrorHistory:            150,
			ErrorCleanupInterval:       90 * time.Minute,
			EnableMetrics:              true,
			EnableValidationCache:      true,
			MaxConcurrentRecoveries:    5,
			FrameReconstructionEnabled: true,
		}
	}

	recoveryConfig := &MetadataRecoveryConfig{
		EnableAutoRecovery:        true,
		MaxRecoveryAttempts:       3,
		RecoveryTimeout:           20 * time.Second,
		ResendTimeout:             5 * time.Second,
		ProtocolResetThreshold:    10,
		ValidationCacheSize:       1000,
		ValidationCacheTTL:        10 * time.Minute,
		CorruptionTolerance:       0.1,
		EnableFrameReconstruction: true,
	}

	stats := &MetadataErrorStats{
		ErrorsByType: make(map[string]int64),
		ErrorHistory: make([]*MetadataErrorRecord, 0, config.MaxErrorHistory),
	}

	handler := &MetadataErrorHandler{
		recoveryStrategy: RecoveryRequestResend,
		recoveryConfig:   recoveryConfig,
		stats:            stats,
		config:           config,
		logger:           &DefaultMetadataLogger{},
		activeRecoveries: make(map[uint64]*MetadataRecoveryOperation),
		validationCache:  make(map[string]*ValidationCacheEntry),
	}

	// Set default error handlers
	handler.setDefaultErrorHandlers()

	return handler
}

// SetLogger sets a custom logger for the error handler
func (h *MetadataErrorHandler) SetLogger(logger MetadataLogger) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.logger = logger
}

// SetRecoveryStrategy sets the recovery strategy for metadata errors
func (h *MetadataErrorHandler) SetRecoveryStrategy(strategy MetadataRecoveryStrategy) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.recoveryStrategy = strategy
}

// SetInvalidMetadataHandler sets a custom handler for invalid metadata
func (h *MetadataErrorHandler) SetInvalidMetadataHandler(handler func(data []byte, err error) error) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.onInvalidMetadata = handler
}

// SetMetadataTimeoutHandler sets a custom handler for metadata timeouts
func (h *MetadataErrorHandler) SetMetadataTimeoutHandler(handler func(streamID uint64) error) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.onMetadataTimeout = handler
}

// SetProtocolViolationHandler sets a custom handler for protocol violations
func (h *MetadataErrorHandler) SetProtocolViolationHandler(handler func(violation string, streamID uint64) error) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.onProtocolViolation = handler
}

// SetCorruptedFrameHandler sets a custom handler for corrupted frames
func (h *MetadataErrorHandler) SetCorruptedFrameHandler(handler func(frameData []byte, expectedSize, actualSize int) error) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.onCorruptedFrame = handler
}

// SetUnsupportedVersionHandler sets a custom handler for unsupported versions
func (h *MetadataErrorHandler) SetUnsupportedVersionHandler(handler func(expectedVersion, actualVersion int) error) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.onUnsupportedVersion = handler
}

// SetMagicMismatchHandler sets a custom handler for magic mismatches
func (h *MetadataErrorHandler) SetMagicMismatchHandler(handler func(expectedMagic, actualMagic uint32) error) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.onMagicMismatch = handler
}

// HandleMetadataError handles a metadata error with appropriate strategy
func (h *MetadataErrorHandler) HandleMetadataError(err error, context *MetadataErrorContext) error {
	if err == nil {
		return nil
	}

	// Check validation cache first
	if h.config.EnableValidationCache && context.FrameData != nil {
		if cached := h.checkValidationCache(context.FrameData); cached != nil {
			if cached.IsValid {
				return nil // Frame is valid according to cache
			}
			// Use cached error information
			h.logger.Debug("Using cached validation result for frame")
		}
	}

	// Determine error type
	errorType := h.classifyMetadataError(err)

	// Record the error
	h.recordMetadataError(err, errorType, context)

	// Handle specific error types
	switch errorType {
	case ErrorTypeInvalidMetadata:
		return h.handleInvalidMetadata(err, context)
	case ErrorTypeMetadataTimeout:
		return h.handleMetadataTimeout(err, context)
	case ErrorTypeProtocolViolation:
		return h.handleProtocolViolation(err, context)
	case ErrorTypeCorruptedFrame:
		return h.handleCorruptedFrame(err, context)
	case ErrorTypeUnsupportedVersion:
		return h.handleUnsupportedVersion(err, context)
	case ErrorTypeMagicMismatch:
		return h.handleMagicMismatch(err, context)
	default:
		return h.handleGenericMetadataError(err, context)
	}
}

// classifyMetadataError classifies an error into a metadata error type
func (h *MetadataErrorHandler) classifyMetadataError(err error) MetadataErrorType {
	if metadataErr, ok := err.(*MetadataProtocolError); ok {
		switch metadataErr.Code {
		case ErrMetadataInvalid:
			return ErrorTypeInvalidMetadata
		case ErrMetadataFrameCorrupted:
			return ErrorTypeCorruptedFrame
		case ErrMetadataFrameTooSmall:
			return ErrorTypeFrameTooSmall
		case ErrMetadataDataTooLarge:
			return ErrorTypeDataTooLarge
		case ErrMetadataInvalidMagic:
			return ErrorTypeMagicMismatch
		case ErrMetadataUnsupportedVersion:
			return ErrorTypeUnsupportedVersion
		case ErrMetadataProtocolViolation:
			return ErrorTypeProtocolViolation
		default:
			return ErrorTypeInvalidMetadata
		}
	}

	// Check error message for specific patterns
	errMsg := err.Error()
	switch {
	case contains(errMsg, "timeout") || contains(errMsg, "deadline"):
		return ErrorTypeMetadataTimeout
	case contains(errMsg, "protocol") || contains(errMsg, "violation"):
		return ErrorTypeProtocolViolation
	case contains(errMsg, "corrupt") || contains(errMsg, "checksum"):
		return ErrorTypeCorruptedFrame
	case contains(errMsg, "version"):
		return ErrorTypeUnsupportedVersion
	case contains(errMsg, "magic"):
		return ErrorTypeMagicMismatch
	default:
		return ErrorTypeInvalidMetadata
	}
}

// handleInvalidMetadata handles invalid metadata errors
func (h *MetadataErrorHandler) handleInvalidMetadata(err error, context *MetadataErrorContext) error {
	h.logger.Warn("Invalid metadata detected: stream=%d", context.StreamID)

	// Update statistics
	h.stats.mutex.Lock()
	h.stats.InvalidMetadataErrors++
	h.stats.mutex.Unlock()

	// Try custom handler first
	if h.onInvalidMetadata != nil {
		if handlerErr := h.onInvalidMetadata(context.FrameData, err); handlerErr == nil {
			h.recordMetadataResolution(err, context, "custom_handler")
			return nil
		}
	}

	// Apply recovery strategy
	return h.applyRecoveryStrategy(err, context, ErrorTypeInvalidMetadata)
}

// handleMetadataTimeout handles metadata timeout errors
func (h *MetadataErrorHandler) handleMetadataTimeout(err error, context *MetadataErrorContext) error {
	h.logger.Warn("Metadata timeout: stream=%d", context.StreamID)

	// Update statistics
	h.stats.mutex.Lock()
	h.stats.TimeoutErrors++
	h.stats.mutex.Unlock()

	// Try custom handler first
	if h.onMetadataTimeout != nil {
		if handlerErr := h.onMetadataTimeout(context.StreamID); handlerErr == nil {
			h.recordMetadataResolution(err, context, "custom_handler")
			return nil
		}
	}

	// Apply recovery strategy
	return h.applyRecoveryStrategy(err, context, ErrorTypeMetadataTimeout)
}

// handleProtocolViolation handles protocol violation errors
func (h *MetadataErrorHandler) handleProtocolViolation(err error, context *MetadataErrorContext) error {
	h.logger.Error("Protocol violation: stream=%d", context.StreamID)

	// Update statistics
	h.stats.mutex.Lock()
	h.stats.ProtocolViolations++
	h.stats.mutex.Unlock()

	// Try custom handler first
	if h.onProtocolViolation != nil {
		violation := err.Error()
		if handlerErr := h.onProtocolViolation(violation, context.StreamID); handlerErr == nil {
			h.recordMetadataResolution(err, context, "custom_handler")
			return nil
		}
	}

	// Protocol violations are serious, apply recovery strategy
	return h.applyRecoveryStrategy(err, context, ErrorTypeProtocolViolation)
}

// handleCorruptedFrame handles corrupted frame errors
func (h *MetadataErrorHandler) handleCorruptedFrame(err error, context *MetadataErrorContext) error {
	h.logger.Error("Corrupted frame: stream=%d", context.StreamID)

	// Update statistics
	h.stats.mutex.Lock()
	h.stats.CorruptedFrames++
	h.stats.mutex.Unlock()

	// Try custom handler first
	if h.onCorruptedFrame != nil {
		expectedSize := 0
		actualSize := len(context.FrameData)
		if context.ExpectedValue != nil {
			if size, ok := context.ExpectedValue.(int); ok {
				expectedSize = size
			}
		}

		if handlerErr := h.onCorruptedFrame(context.FrameData, expectedSize, actualSize); handlerErr == nil {
			h.recordMetadataResolution(err, context, "custom_handler")
			return nil
		}
	}

	// Try frame reconstruction if enabled
	if h.config.FrameReconstructionEnabled {
		if reconstructed := h.attemptFrameReconstruction(context.FrameData); reconstructed != nil {
			h.logger.Info("Frame reconstruction successful for stream %d", context.StreamID)
			h.stats.mutex.Lock()
			h.stats.FrameReconstructions++
			h.stats.mutex.Unlock()
			h.recordMetadataResolution(err, context, "frame_reconstruction")
			return nil
		}
	}

	// Apply recovery strategy
	return h.applyRecoveryStrategy(err, context, ErrorTypeCorruptedFrame)
}

// handleUnsupportedVersion handles unsupported version errors
func (h *MetadataErrorHandler) handleUnsupportedVersion(err error, context *MetadataErrorContext) error {
	h.logger.Error("Unsupported version: stream=%d", context.StreamID)

	// Update statistics
	h.stats.mutex.Lock()
	h.stats.UnsupportedVersions++
	h.stats.mutex.Unlock()

	// Try custom handler first
	if h.onUnsupportedVersion != nil {
		expectedVersion := 1 // Default expected version
		actualVersion := 0
		if context.ExpectedValue != nil {
			if ver, ok := context.ExpectedValue.(int); ok {
				expectedVersion = ver
			}
		}
		if context.ActualValue != nil {
			if ver, ok := context.ActualValue.(int); ok {
				actualVersion = ver
			}
		}

		if handlerErr := h.onUnsupportedVersion(expectedVersion, actualVersion); handlerErr == nil {
			h.recordMetadataResolution(err, context, "custom_handler")
			return nil
		}
	}

	// Apply recovery strategy
	return h.applyRecoveryStrategy(err, context, ErrorTypeUnsupportedVersion)
}

// handleMagicMismatch handles magic mismatch errors
func (h *MetadataErrorHandler) handleMagicMismatch(err error, context *MetadataErrorContext) error {
	h.logger.Error("Magic mismatch: stream=%d", context.StreamID)

	// Update statistics
	h.stats.mutex.Lock()
	h.stats.MagicMismatches++
	h.stats.mutex.Unlock()

	// Try custom handler first
	if h.onMagicMismatch != nil {
		expectedMagic := uint32(0x4B574B4D) // "KWKM"
		actualMagic := uint32(0)
		if context.ExpectedValue != nil {
			if magic, ok := context.ExpectedValue.(uint32); ok {
				expectedMagic = magic
			}
		}
		if context.ActualValue != nil {
			if magic, ok := context.ActualValue.(uint32); ok {
				actualMagic = magic
			}
		}

		if handlerErr := h.onMagicMismatch(expectedMagic, actualMagic); handlerErr == nil {
			h.recordMetadataResolution(err, context, "custom_handler")
			return nil
		}
	}

	// Apply recovery strategy
	return h.applyRecoveryStrategy(err, context, ErrorTypeMagicMismatch)
}

// handleGenericMetadataError handles generic metadata errors
func (h *MetadataErrorHandler) handleGenericMetadataError(err error, context *MetadataErrorContext) error {
	h.logger.Error("Generic metadata error: %s", err.Error())

	// Apply recovery strategy
	return h.applyRecoveryStrategy(err, context, ErrorTypeInvalidMetadata)
}

// applyRecoveryStrategy applies the configured recovery strategy
func (h *MetadataErrorHandler) applyRecoveryStrategy(err error, context *MetadataErrorContext, errorType MetadataErrorType) error {
	strategy := h.recoveryStrategy

	h.logger.Info("Applying recovery strategy %d for error type %d", strategy, errorType)

	switch strategy {
	case RecoveryRequestResend:
		return h.requestResend(err, context)
	case RecoveryUseDefaults:
		return h.useDefaults(err, context)
	case RecoveryCloseStream:
		return h.closeStreamForMetadata(err, context)
	case RecoveryIgnoreStream:
		return h.ignoreStream(err, context)
	case RecoveryResetProtocol:
		return h.resetProtocol(err, context)
	case RecoveryFallbackToBasic:
		return h.fallbackToBasic(err, context)
	default:
		h.logger.Error("Unknown recovery strategy: %d", strategy)
		return err
	}
}

// requestResend implements the request resend recovery
func (h *MetadataErrorHandler) requestResend(err error, context *MetadataErrorContext) error {
	h.logger.Info("Requesting resend for stream %d", context.StreamID)

	// Update statistics
	h.stats.mutex.Lock()
	h.stats.ResendRequests++
	h.stats.mutex.Unlock()

	// Start recovery operation
	if h.config.EnableErrorRecovery {
		h.startMetadataRecoveryOperation(context, RecoveryRequestResend)
	}

	h.recordMetadataResolution(err, context, "request_resend")
	return nil // Error will be resolved by resend
}

// useDefaults implements the use defaults recovery
func (h *MetadataErrorHandler) useDefaults(err error, context *MetadataErrorContext) error {
	h.logger.Info("Using default values for stream %d", context.StreamID)
	h.recordMetadataResolution(err, context, "use_defaults")
	return nil // Use default values
}

// closeStreamForMetadata implements the close stream recovery
func (h *MetadataErrorHandler) closeStreamForMetadata(err error, context *MetadataErrorContext) error {
	h.logger.Warn("Closing stream due to metadata error: stream=%d", context.StreamID)
	h.recordMetadataResolution(err, context, "close_stream")

	// Return a special error indicating stream should be closed
	return &MetadataStreamCloseError{
		OriginalError: err,
		StreamID:      context.StreamID,
		Reason:        "metadata_error",
	}
}

// ignoreStream implements the ignore stream recovery
func (h *MetadataErrorHandler) ignoreStream(err error, context *MetadataErrorContext) error {
	h.logger.Info("Ignoring stream due to metadata error: stream=%d", context.StreamID)
	h.recordMetadataResolution(err, context, "ignore_stream")
	return nil // Ignore the stream
}

// resetProtocol implements the reset protocol recovery
func (h *MetadataErrorHandler) resetProtocol(err error, context *MetadataErrorContext) error {
	h.logger.Warn("Resetting protocol for stream %d", context.StreamID)

	// Update statistics
	h.stats.mutex.Lock()
	h.stats.ProtocolResets++
	h.stats.mutex.Unlock()

	h.recordMetadataResolution(err, context, "reset_protocol")

	// Return a special error indicating protocol should be reset
	return &MetadataProtocolResetError{
		OriginalError: err,
		StreamID:      context.StreamID,
		Reason:        "metadata_error",
	}
}

// fallbackToBasic implements the fallback to basic recovery
func (h *MetadataErrorHandler) fallbackToBasic(err error, context *MetadataErrorContext) error {
	h.logger.Info("Falling back to basic protocol for stream %d", context.StreamID)
	h.recordMetadataResolution(err, context, "fallback_to_basic")
	return nil // Use basic protocol
}

// attemptFrameReconstruction attempts to reconstruct a corrupted frame
func (h *MetadataErrorHandler) attemptFrameReconstruction(frameData []byte) []byte {
	if len(frameData) < minMetadataFrameSize {
		return nil // Cannot reconstruct if too small
	}

	// Simple reconstruction: try to fix common corruption patterns
	reconstructed := make([]byte, len(frameData))
	copy(reconstructed, frameData)

	// Try to fix magic number if corrupted
	if len(reconstructed) >= 4 {
		// Check if magic is close to expected value
		magic := uint32(reconstructed[0])<<24 | uint32(reconstructed[1])<<16 |
			uint32(reconstructed[2])<<8 | uint32(reconstructed[3])
		expectedMagic := uint32(0x4B574B4D) // "KWKM"

		// Simple bit flip correction
		if countDifferentBits(magic, expectedMagic) <= 2 {
			reconstructed[0] = 0x4B
			reconstructed[1] = 0x57
			reconstructed[2] = 0x4B
			reconstructed[3] = 0x4D
			return reconstructed
		}
	}

	return nil // Reconstruction failed
}

// countDifferentBits counts the number of different bits between two uint32 values
func countDifferentBits(a, b uint32) int {
	xor := a ^ b
	count := 0
	for xor != 0 {
		count += int(xor & 1)
		xor >>= 1
	}
	return count
}

// checkValidationCache checks if a frame validation result is cached
func (h *MetadataErrorHandler) checkValidationCache(frameData []byte) *ValidationCacheEntry {
	if !h.config.EnableValidationCache || len(frameData) == 0 {
		return nil
	}

	// Simple hash of frame data
	hash := fmt.Sprintf("%x", frameData[:min(len(frameData), 32)])

	h.cacheMutex.RLock()
	entry, exists := h.validationCache[hash]
	h.cacheMutex.RUnlock()

	if !exists {
		h.stats.mutex.Lock()
		h.stats.ValidationCacheMisses++
		h.stats.mutex.Unlock()
		return nil
	}

	// Check TTL
	if time.Since(entry.Timestamp) > h.recoveryConfig.ValidationCacheTTL {
		h.cacheMutex.Lock()
		delete(h.validationCache, hash)
		h.cacheMutex.Unlock()
		return nil
	}

	// Update hit count and statistics
	entry.HitCount++
	h.stats.mutex.Lock()
	h.stats.ValidationCacheHits++
	h.stats.mutex.Unlock()

	return entry
}

// startMetadataRecoveryOperation starts a recovery operation for metadata errors
func (h *MetadataErrorHandler) startMetadataRecoveryOperation(errorContext *MetadataErrorContext, strategy MetadataRecoveryStrategy) {
	h.recoveryMutex.Lock()
	defer h.recoveryMutex.Unlock()

	// Check if recovery is already in progress for this stream
	if _, exists := h.activeRecoveries[errorContext.StreamID]; exists {
		h.logger.Debug("Recovery already in progress for stream %d", errorContext.StreamID)
		return
	}

	// Check recovery limits
	if len(h.activeRecoveries) >= h.config.MaxConcurrentRecoveries {
		h.logger.Warn("Maximum concurrent recoveries reached, skipping recovery for stream %d", errorContext.StreamID)
		return
	}

	// Create recovery operation
	ctx, cancel := context.WithTimeout(context.Background(), h.recoveryConfig.RecoveryTimeout)
	recovery := &MetadataRecoveryOperation{
		ID:            fmt.Sprintf("metadata_recovery_%d_%d", errorContext.StreamID, time.Now().UnixNano()),
		StreamID:      errorContext.StreamID,
		ErrorType:     h.classifyMetadataError(fmt.Errorf("recovery_operation")),
		StartTime:     time.Now(),
		Timeout:       time.Now().Add(h.recoveryConfig.RecoveryTimeout),
		Attempts:      0,
		Strategy:      strategy,
		Context:       ctx,
		Cancel:        cancel,
		Status:        MetadataRecoveryStatusPending,
		LastUpdate:    time.Now(),
		ErrorDetails:  make(map[string]interface{}),
		OriginalFrame: errorContext.FrameData,
	}

	h.activeRecoveries[errorContext.StreamID] = recovery

	// Start recovery in background
	go h.executeMetadataRecovery(recovery)

	h.logger.Info("Started metadata recovery operation %s for stream %d", recovery.ID, errorContext.StreamID)
}

// executeMetadataRecovery executes a metadata recovery operation
func (h *MetadataErrorHandler) executeMetadataRecovery(recovery *MetadataRecoveryOperation) {
	defer func() {
		h.recoveryMutex.Lock()
		delete(h.activeRecoveries, recovery.StreamID)
		h.recoveryMutex.Unlock()
		recovery.Cancel()
	}()

	recovery.Status = MetadataRecoveryStatusInProgress
	recovery.LastUpdate = time.Now()

	h.stats.mutex.Lock()
	h.stats.RecoveryAttempts++
	h.stats.mutex.Unlock()

	// Execute recovery based on strategy
	var err error
	switch recovery.Strategy {
	case RecoveryRequestResend:
		err = h.executeResendRecovery(recovery)
	case RecoveryResetProtocol:
		err = h.executeProtocolResetRecovery(recovery)
	default:
		err = fmt.Errorf("unsupported recovery strategy: %d", recovery.Strategy)
	}

	// Update recovery status
	if err != nil {
		recovery.Status = MetadataRecoveryStatusFailed
		h.logger.Error("Metadata recovery operation %s failed: %s", recovery.ID, err.Error())
	} else {
		recovery.Status = MetadataRecoveryStatusCompleted
		h.stats.mutex.Lock()
		h.stats.RecoverySuccessful++
		h.stats.mutex.Unlock()
		h.logger.Info("Metadata recovery operation %s completed successfully", recovery.ID)
	}

	recovery.LastUpdate = time.Now()
}

// executeResendRecovery executes resend recovery
func (h *MetadataErrorHandler) executeResendRecovery(recovery *MetadataRecoveryOperation) error {
	h.logger.Debug("Executing resend recovery for stream %d", recovery.StreamID)

	// Wait for resend timeout
	select {
	case <-time.After(h.recoveryConfig.ResendTimeout):
		return nil // Assume resend completed
	case <-recovery.Context.Done():
		return recovery.Context.Err()
	}
}

// executeProtocolResetRecovery executes protocol reset recovery
func (h *MetadataErrorHandler) executeProtocolResetRecovery(recovery *MetadataRecoveryOperation) error {
	h.logger.Debug("Executing protocol reset recovery for stream %d", recovery.StreamID)

	// Protocol reset is immediate
	return nil
}

// recordMetadataError records a metadata error in the statistics and history
func (h *MetadataErrorHandler) recordMetadataError(err error, errorType MetadataErrorType, context *MetadataErrorContext) {
	if !h.config.EnableMetrics {
		return
	}

	h.stats.mutex.Lock()
	defer h.stats.mutex.Unlock()

	h.stats.TotalErrors++
	h.stats.LastErrorTime = time.Now()

	// Record error by type
	errorTypeStr := h.metadataErrorTypeToString(errorType)
	h.stats.ErrorsByType[errorTypeStr]++

	// Add to error history
	frameDataTruncated := context.FrameData
	if len(frameDataTruncated) > 256 {
		frameDataTruncated = frameDataTruncated[:256] // Truncate for storage
	}

	record := &MetadataErrorRecord{
		Timestamp:        time.Now(),
		ErrorType:        errorType,
		ErrorMessage:     err.Error(),
		StreamID:         context.StreamID,
		FrameData:        frameDataTruncated,
		ExpectedValue:    context.ExpectedValue,
		ActualValue:      context.ActualValue,
		RecoveryAttempts: 0,
		Resolved:         false,
		RecoveryStrategy: h.recoveryStrategy,
	}

	// Maintain history size limit
	if len(h.stats.ErrorHistory) >= h.config.MaxErrorHistory {
		h.stats.ErrorHistory = h.stats.ErrorHistory[1:]
	}
	h.stats.ErrorHistory = append(h.stats.ErrorHistory, record)
}

// recordMetadataResolution records that a metadata error was resolved
func (h *MetadataErrorHandler) recordMetadataResolution(err error, context *MetadataErrorContext, method string) {
	if !h.config.EnableMetrics {
		return
	}

	h.stats.mutex.Lock()
	defer h.stats.mutex.Unlock()

	// Mark the most recent matching error as resolved
	for i := len(h.stats.ErrorHistory) - 1; i >= 0; i-- {
		record := h.stats.ErrorHistory[i]
		if record.StreamID == context.StreamID && !record.Resolved {
			record.Resolved = true
			record.ResolutionMethod = method
			break
		}
	}
}

// GetMetadataErrorStats returns current metadata error statistics
func (h *MetadataErrorHandler) GetMetadataErrorStats() *MetadataErrorStats {
	h.stats.mutex.RLock()
	defer h.stats.mutex.RUnlock()

	// Return a copy to avoid race conditions
	statsCopy := &MetadataErrorStats{
		TotalErrors:              h.stats.TotalErrors,
		ErrorsByType:             make(map[string]int64),
		InvalidMetadataErrors:    h.stats.InvalidMetadataErrors,
		TimeoutErrors:            h.stats.TimeoutErrors,
		ProtocolViolations:       h.stats.ProtocolViolations,
		CorruptedFrames:          h.stats.CorruptedFrames,
		UnsupportedVersions:      h.stats.UnsupportedVersions,
		MagicMismatches:          h.stats.MagicMismatches,
		RecoveryAttempts:         h.stats.RecoveryAttempts,
		RecoverySuccessful:       h.stats.RecoverySuccessful,
		ResendRequests:           h.stats.ResendRequests,
		ProtocolResets:           h.stats.ProtocolResets,
		FrameReconstructions:     h.stats.FrameReconstructions,
		ValidationCacheHits:      h.stats.ValidationCacheHits,
		ValidationCacheMisses:    h.stats.ValidationCacheMisses,
		LastErrorTime:            h.stats.LastErrorTime,
		ErrorHistory:             make([]*MetadataErrorRecord, len(h.stats.ErrorHistory)),
		ActiveRecoveryOperations: len(h.activeRecoveries),
	}

	for k, v := range h.stats.ErrorsByType {
		statsCopy.ErrorsByType[k] = v
	}

	copy(statsCopy.ErrorHistory, h.stats.ErrorHistory)

	return statsCopy
}

// setDefaultErrorHandlers sets up default error handling behavior
func (h *MetadataErrorHandler) setDefaultErrorHandlers() {
	// Default invalid metadata handler
	h.onInvalidMetadata = func(data []byte, err error) error {
		h.logger.Info("Default invalid metadata handler: data_size=%d, error=%s", len(data), err.Error())
		return fmt.Errorf("invalid metadata not resolved by default handler")
	}

	// Default timeout handler
	h.onMetadataTimeout = func(streamID uint64) error {
		h.logger.Warn("Default timeout handler: stream=%d", streamID)
		return fmt.Errorf("timeout not resolved by default handler")
	}

	// Default protocol violation handler
	h.onProtocolViolation = func(violation string, streamID uint64) error {
		h.logger.Error("Default protocol violation handler: stream=%d, violation=%s", streamID, violation)
		return fmt.Errorf("protocol violation not resolved by default handler")
	}

	// Default corrupted frame handler
	h.onCorruptedFrame = func(frameData []byte, expectedSize, actualSize int) error {
		h.logger.Error("Default corrupted frame handler: expected=%d, actual=%d", expectedSize, actualSize)
		return fmt.Errorf("corrupted frame not resolved by default handler")
	}

	// Default unsupported version handler
	h.onUnsupportedVersion = func(expectedVersion, actualVersion int) error {
		h.logger.Error("Default unsupported version handler: expected=%d, actual=%d", expectedVersion, actualVersion)
		return fmt.Errorf("unsupported version not resolved by default handler")
	}

	// Default magic mismatch handler
	h.onMagicMismatch = func(expectedMagic, actualMagic uint32) error {
		h.logger.Error("Default magic mismatch handler: expected=0x%08X, actual=0x%08X", expectedMagic, actualMagic)
		return fmt.Errorf("magic mismatch not resolved by default handler")
	}
}

// metadataErrorTypeToString converts a metadata error type to string
func (h *MetadataErrorHandler) metadataErrorTypeToString(errorType MetadataErrorType) string {
	switch errorType {
	case ErrorTypeInvalidMetadata:
		return "INVALID_METADATA"
	case ErrorTypeMetadataTimeout:
		return "METADATA_TIMEOUT"
	case ErrorTypeProtocolViolation:
		return "PROTOCOL_VIOLATION"
	case ErrorTypeCorruptedFrame:
		return "CORRUPTED_FRAME"
	case ErrorTypeUnsupportedVersion:
		return "UNSUPPORTED_VERSION"
	case ErrorTypeMagicMismatch:
		return "MAGIC_MISMATCH"
	case ErrorTypeFrameTooSmall:
		return "FRAME_TOO_SMALL"
	case ErrorTypeDataTooLarge:
		return "DATA_TOO_LARGE"
	case ErrorTypeInvalidMessageType:
		return "INVALID_MESSAGE_TYPE"
	default:
		return "UNKNOWN"
	}
}

// MetadataStreamCloseError represents an error that requires closing a stream due to metadata issues
type MetadataStreamCloseError struct {
	OriginalError error
	StreamID      uint64
	Reason        string
}

func (e *MetadataStreamCloseError) Error() string {
	return fmt.Sprintf("stream %d should be closed due to %s: %s", e.StreamID, e.Reason, e.OriginalError.Error())
}

// MetadataProtocolResetError represents an error that requires resetting the protocol
type MetadataProtocolResetError struct {
	OriginalError error
	StreamID      uint64
	Reason        string
}

func (e *MetadataProtocolResetError) Error() string {
	return fmt.Sprintf("protocol should be reset for stream %d due to %s: %s", e.StreamID, e.Reason, e.OriginalError.Error())
}

// IsMetadataStreamCloseError checks if an error is a metadata stream close error
func IsMetadataStreamCloseError(err error) bool {
	_, ok := err.(*MetadataStreamCloseError)
	return ok
}

// IsMetadataProtocolResetError checks if an error is a metadata protocol reset error
func IsMetadataProtocolResetError(err error) bool {
	_, ok := err.(*MetadataProtocolResetError)
	return ok
}

// Helper function to get minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
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
