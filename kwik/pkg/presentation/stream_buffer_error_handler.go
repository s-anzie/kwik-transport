package presentation

import (
	"fmt"
	"sync"
	"time"
)

// StreamBufferLogger provides minimal logging for the presentation error handler
type StreamBufferLogger interface {
	Debug(msg string, keysAndValues ...interface{})
	Info(msg string, keysAndValues ...interface{})
	Warn(msg string, keysAndValues ...interface{})
	Error(msg string, keysAndValues ...interface{})
}

// StreamBufferErrorHandler gère les erreurs spécifiques aux StreamBuffers
type StreamBufferErrorHandler struct {
	mu                  sync.RWMutex
	corruptedStreams    map[uint64]time.Time
	recoveryAttempts    map[uint64]int
	maxRecoveryAttempts int
	gapTimeouts         map[uint64]time.Time
	maxGapTimeout       time.Duration
	errorCallbacks      []func(StreamBufferError)

	logger StreamBufferLogger
}

// SetLogger sets the logger for the error handler
func (h *StreamBufferErrorHandler) SetLogger(l StreamBufferLogger) { h.logger = l }

// StreamBufferError représente une erreur de StreamBuffer
type StreamBufferError struct {
	StreamID    uint64
	ErrorType   StreamBufferErrorType
	Message     string
	Timestamp   time.Time
	Recoverable bool
	Context     map[string]interface{}
}

// StreamBufferErrorType définit les types d'erreurs de StreamBuffer
type StreamBufferErrorType int

const (
	StreamBufferErrorBufferOverflow StreamBufferErrorType = iota
	StreamBufferErrorDataCorruption
	StreamBufferErrorPersistentGap
	StreamBufferErrorReadTimeout
	StreamBufferErrorWriteFailure
	StreamBufferErrorMemoryExhaustion
)

func (e StreamBufferErrorType) String() string {
	switch e {
	case StreamBufferErrorBufferOverflow:
		return "BUFFER_OVERFLOW"
	case StreamBufferErrorDataCorruption:
		return "DATA_CORRUPTION"
	case StreamBufferErrorPersistentGap:
		return "PERSISTENT_GAP"
	case StreamBufferErrorReadTimeout:
		return "READ_TIMEOUT"
	case StreamBufferErrorWriteFailure:
		return "WRITE_FAILURE"
	case StreamBufferErrorMemoryExhaustion:
		return "MEMORY_EXHAUSTION"
	default:
		return "UNKNOWN"
	}
}

// NewStreamBufferErrorHandler crée un nouveau gestionnaire d'erreurs
func NewStreamBufferErrorHandler() *StreamBufferErrorHandler {
	return &StreamBufferErrorHandler{
		corruptedStreams:    make(map[uint64]time.Time),
		recoveryAttempts:    make(map[uint64]int),
		maxRecoveryAttempts: 3,
		gapTimeouts:         make(map[uint64]time.Time),
		maxGapTimeout:       100 * time.Millisecond,
		errorCallbacks:      make([]func(StreamBufferError), 0),
	}
}

// HandleBufferOverflow gère les erreurs de débordement de buffer
func (h *StreamBufferErrorHandler) HandleBufferOverflow(streamID uint64, bufferSize, attemptedWrite uint64) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	err := StreamBufferError{
		StreamID:    streamID,
		ErrorType:   StreamBufferErrorBufferOverflow,
		Message:     fmt.Sprintf("Buffer overflow: size=%d, attempted_write=%d", bufferSize, attemptedWrite),
		Timestamp:   time.Now(),
		Recoverable: true,
		Context: map[string]interface{}{
			"buffer_size":     bufferSize,
			"attempted_write": attemptedWrite,
			"overflow_amount": attemptedWrite - bufferSize,
		},
	}

	if h.logger != nil {
		h.logger.Error("STREAM_BUFFER_ERROR", "type", err.ErrorType.String(), "stream", streamID, "message", err.Message)
	}

	// Notifier les callbacks
	h.notifyCallbacks(err)

	// Stratégie de récupération : activer la contre-pression
	return fmt.Errorf("buffer overflow detected for stream %d, backpressure activated", streamID)
}

// HandleDataCorruption gère les erreurs de corruption de données
func (h *StreamBufferErrorHandler) HandleDataCorruption(streamID uint64, offset uint64, expectedChecksum, actualChecksum uint32) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Marquer le stream comme corrompu
	h.corruptedStreams[streamID] = time.Now()

	err := StreamBufferError{
		StreamID:    streamID,
		ErrorType:   StreamBufferErrorDataCorruption,
		Message:     fmt.Sprintf("Data corruption at offset %d: expected_checksum=%x, actual_checksum=%x", offset, expectedChecksum, actualChecksum),
		Timestamp:   time.Now(),
		Recoverable: false,
		Context: map[string]interface{}{
			"offset":            offset,
			"expected_checksum": expectedChecksum,
			"actual_checksum":   actualChecksum,
		},
	}
	if h.logger != nil {
		h.logger.Error("STREAM_BUFFER_ERROR", "type", err.ErrorType.String(), "stream", streamID, "message", err.Message)
	}

	// Notifier les callbacks
	h.notifyCallbacks(err)

	// Isoler le stream corrompu
	return fmt.Errorf("data corruption detected for stream %d, stream isolated", streamID)
}

// HandlePersistentGap gère les gaps persistants
func (h *StreamBufferErrorHandler) HandlePersistentGap(streamID uint64, gapStart, gapEnd uint64) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Vérifier si le gap persiste depuis trop longtemps
	if gapTime, exists := h.gapTimeouts[streamID]; exists {
		if time.Since(gapTime) > h.maxGapTimeout {
			err := StreamBufferError{
				StreamID:    streamID,
				ErrorType:   StreamBufferErrorPersistentGap,
				Message:     fmt.Sprintf("Persistent gap from %d to %d (timeout: %v)", gapStart, gapEnd, time.Since(gapTime)),
				Timestamp:   time.Now(),
				Recoverable: true,
				Context: map[string]interface{}{
					"gap_start":    gapStart,
					"gap_end":      gapEnd,
					"gap_duration": time.Since(gapTime),
					"gap_size":     gapEnd - gapStart,
				},
			}
			if h.logger != nil {
				h.logger.Warn("STREAM_BUFFER_ERROR", "type", err.ErrorType.String(), "stream", streamID, "message", err.Message)
			}

			// Notifier les callbacks
			h.notifyCallbacks(err)

			// Nettoyer le timeout
			delete(h.gapTimeouts, streamID)

			return fmt.Errorf("persistent gap detected for stream %d from %d to %d", streamID, gapStart, gapEnd)
		}
	} else {
		// Premier gap détecté, commencer le timeout
		h.gapTimeouts[streamID] = time.Now()
	}

	return nil
}

// HandleReadTimeout gère les timeouts de lecture
func (h *StreamBufferErrorHandler) HandleReadTimeout(streamID uint64, timeout time.Duration) error {
	err := StreamBufferError{
		StreamID:    streamID,
		ErrorType:   StreamBufferErrorReadTimeout,
		Message:     fmt.Sprintf("Read timeout after %v", timeout),
		Timestamp:   time.Now(),
		Recoverable: true,
		Context: map[string]interface{}{
			"timeout_duration": timeout,
		},
	}
	if h.logger != nil {
		h.logger.Warn("STREAM_BUFFER_ERROR", "type", err.ErrorType.String(), "stream", streamID, "message", err.Message)
	}

	// Notifier les callbacks
	h.notifyCallbacks(err)

	return fmt.Errorf("read timeout for stream %d after %v", streamID, timeout)
}

// IsStreamCorrupted vérifie si un stream est marqué comme corrompu
func (h *StreamBufferErrorHandler) IsStreamCorrupted(streamID uint64) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	_, exists := h.corruptedStreams[streamID]
	return exists
}

// AttemptRecovery tente de récupérer un stream en erreur
func (h *StreamBufferErrorHandler) AttemptRecovery(streamID uint64) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	attempts := h.recoveryAttempts[streamID]
	if attempts >= h.maxRecoveryAttempts {
		return fmt.Errorf("maximum recovery attempts (%d) exceeded for stream %d", h.maxRecoveryAttempts, streamID)
	}

	h.recoveryAttempts[streamID] = attempts + 1

	// Nettoyer les erreurs récupérables
	delete(h.corruptedStreams, streamID)
	delete(h.gapTimeouts, streamID)
	if h.logger != nil {
		h.logger.Info("STREAM_BUFFER_RECOVERY Attempting recovery", "attempt", attempts+1, "max", h.maxRecoveryAttempts, "stream", streamID)
	}

	return nil
}

// CleanupStream nettoie les erreurs pour un stream fermé
func (h *StreamBufferErrorHandler) CleanupStream(streamID uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	delete(h.corruptedStreams, streamID)
	delete(h.recoveryAttempts, streamID)
	delete(h.gapTimeouts, streamID)
	if h.logger != nil {
		h.logger.Info("STREAM_BUFFER_CLEANUP Cleaned up error state", "stream", streamID)
	}
}

// AddErrorCallback ajoute un callback pour les erreurs
func (h *StreamBufferErrorHandler) AddErrorCallback(callback func(StreamBufferError)) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.errorCallbacks = append(h.errorCallbacks, callback)
}

// notifyCallbacks notifie tous les callbacks enregistrés
func (h *StreamBufferErrorHandler) notifyCallbacks(err StreamBufferError) {
	for _, callback := range h.errorCallbacks {
		go func(cb func(StreamBufferError)) {
			defer func() {
				if r := recover(); r != nil {
					if h.logger != nil {
						h.logger.Error("STREAM_BUFFER_ERROR Callback panic", "error", r)
					}
				}
			}()
			cb(err)
		}(callback)
	}
}

// GetErrorStats retourne les statistiques d'erreurs
func (h *StreamBufferErrorHandler) GetErrorStats() map[string]interface{} {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return map[string]interface{}{
		"corrupted_streams_count": len(h.corruptedStreams),
		"recovery_attempts_count": len(h.recoveryAttempts),
		"gap_timeouts_count":      len(h.gapTimeouts),
		"max_recovery_attempts":   h.maxRecoveryAttempts,
		"max_gap_timeout":         h.maxGapTimeout,
		"corrupted_streams":       h.corruptedStreams,
		"recovery_attempts":       h.recoveryAttempts,
		"gap_timeouts":            h.gapTimeouts,
	}
}

// DiagnoseStreamIssue diagnostique les problèmes d'un stream spécifique
func (h *StreamBufferErrorHandler) DiagnoseStreamIssue(streamID uint64) map[string]interface{} {
	h.mu.RLock()
	defer h.mu.RUnlock()

	diagnosis := map[string]interface{}{
		"stream_id": streamID,
		"timestamp": time.Now(),
	}

	if corruptTime, isCorrupted := h.corruptedStreams[streamID]; isCorrupted {
		diagnosis["is_corrupted"] = true
		diagnosis["corruption_time"] = corruptTime
		diagnosis["corruption_duration"] = time.Since(corruptTime)
	} else {
		diagnosis["is_corrupted"] = false
	}

	if attempts, hasAttempts := h.recoveryAttempts[streamID]; hasAttempts {
		diagnosis["recovery_attempts"] = attempts
		diagnosis["max_recovery_attempts"] = h.maxRecoveryAttempts
		diagnosis["recovery_exhausted"] = attempts >= h.maxRecoveryAttempts
	} else {
		diagnosis["recovery_attempts"] = 0
		diagnosis["recovery_exhausted"] = false
	}

	if gapTime, hasGap := h.gapTimeouts[streamID]; hasGap {
		diagnosis["has_persistent_gap"] = true
		diagnosis["gap_start_time"] = gapTime
		diagnosis["gap_duration"] = time.Since(gapTime)
		diagnosis["gap_timeout_exceeded"] = time.Since(gapTime) > h.maxGapTimeout
	} else {
		diagnosis["has_persistent_gap"] = false
	}

	return diagnosis
}
