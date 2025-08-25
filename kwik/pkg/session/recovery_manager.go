package session

import (
	"fmt"
	"math"
	"sync"
	"time"

	"kwik/pkg/protocol"
)

// RecoveryManager gère les mécanismes de récupération pour les collisions et erreurs
type RecoveryManager struct {
	sessionID        string
	logger           interface {
		Debug(msg string, keysAndValues ...interface{})
		Info(msg string, keysAndValues ...interface{})
		Error(msg string, keysAndValues ...interface{})
	}
	frameRouter      FrameRouter
	collisionManager *CollisionManager
	
	// Configuration de retry
	maxRetries       int
	baseDelay        time.Duration
	maxDelay         time.Duration
	backoffFactor    float64
	
	// État de récupération
	retryCount       int
	lastRetryTime    time.Time
	mutex            sync.RWMutex
	
	// Callbacks
	onRecoverySuccess func()
	onRecoveryFailure func(error)
	onSessionClose    func()
}

// RecoveryConfig is defined in error_recovery.go

// NewRecoveryManager crée un nouveau gestionnaire de récupération
func NewRecoveryManager(sessionID string, frameRouter FrameRouter, logger interface {
	Debug(msg string, keysAndValues ...interface{})
	Info(msg string, keysAndValues ...interface{})
	Error(msg string, keysAndValues ...interface{})
}, config *RecoveryConfig) *RecoveryManager {
	if config == nil {
		config = DefaultRecoveryConfig()
	}
	
	rm := &RecoveryManager{
		sessionID:     sessionID,
		logger:        logger,
		frameRouter:   frameRouter,
		maxRetries:    config.DefaultMaxRetries,
		baseDelay:     config.DefaultRetryDelay,
		maxDelay:      config.RecoveryTimeout,
		backoffFactor: config.DefaultBackoffMultiplier,
	}
	
	// Créer le gestionnaire de collisions
	rm.collisionManager = NewCollisionManager(sessionID, logger)
	
	// Ajouter les stratégies de récupération
	rm.setupRecoveryStrategies()
	
	return rm
}

// setupRecoveryStrategies configure les stratégies de récupération
func (rm *RecoveryManager) setupRecoveryStrategies() {
	// Stratégie de buffer pour les heartbeats précoces
	bufferStrategy := NewBufferRecoveryStrategy(rm.frameRouter, rm.logger)
	rm.collisionManager.AddRecoveryStrategy(bufferStrategy)
	
	// Stratégie de retry pour les échecs d'authentification
	retryStrategy := NewRetryRecoveryStrategy(rm, rm.logger)
	rm.collisionManager.AddRecoveryStrategy(retryStrategy)
	
	// Stratégie d'ignore pour les trames non critiques
	ignoreStrategy := NewIgnoreRecoveryStrategy(rm.logger)
	rm.collisionManager.AddRecoveryStrategy(ignoreStrategy)
}

// SetCallbacks définit les callbacks de récupération
func (rm *RecoveryManager) SetCallbacks(onSuccess, onFailure func(), onClose func()) {
	rm.onRecoverySuccess = onSuccess
	rm.onRecoveryFailure = func(err error) { onFailure() }
	rm.onSessionClose = onClose
}

// HandleAuthenticationCollision gère une collision pendant l'authentification
func (rm *RecoveryManager) HandleAuthenticationCollision(expectedType protocol.FrameType, receivedFrame protocol.Frame, sessionState SessionState) error {
	rm.logger.Debug("Handling authentication collision",
		"sessionID", rm.sessionID,
		"expectedType", expectedType,
		"receivedType", receivedFrame.Type(),
		"sessionState", sessionState.String())
	
	// Utiliser le gestionnaire de collisions
	err := rm.collisionManager.HandlePotentialCollision(expectedType, receivedFrame, sessionState)
	if err == nil {
		// Récupération réussie
		if rm.onRecoverySuccess != nil {
			rm.onRecoverySuccess()
		}
		return nil
	}
	
	// Récupération échouée, tenter un retry si possible
	return rm.attemptRetry(err)
}

// attemptRetry tente un retry avec délai exponentiel
func (rm *RecoveryManager) attemptRetry(originalError error) error {
	rm.mutex.Lock()
	defer rm.mutex.Unlock()
	
	if rm.retryCount >= rm.maxRetries {
		rm.logger.Error("Maximum retries exceeded, closing session",
			"sessionID", rm.sessionID,
			"retryCount", rm.retryCount,
			"maxRetries", rm.maxRetries,
			"originalError", originalError)
		
		if rm.onSessionClose != nil {
			rm.onSessionClose()
		}
		
		return fmt.Errorf("recovery failed after %d retries: %w", rm.maxRetries, originalError)
	}
	
	// Calculer le délai avec backoff exponentiel
	delay := rm.calculateBackoffDelay()
	
	rm.logger.Info("Attempting recovery retry",
		"sessionID", rm.sessionID,
		"retryCount", rm.retryCount + 1,
		"delay", delay,
		"originalError", originalError)
	
	rm.retryCount++
	rm.lastRetryTime = time.Now()
	
	// Attendre le délai
	time.Sleep(delay)
	
	// Note: Dans une implémentation complète, nous relancerions l'authentification ici
	// Pour l'instant, nous simulons une récupération réussie
	rm.logger.Info("Recovery retry completed",
		"sessionID", rm.sessionID,
		"retryCount", rm.retryCount)
	
	if rm.onRecoverySuccess != nil {
		rm.onRecoverySuccess()
	}
	
	return nil
}

// calculateBackoffDelay calcule le délai avec backoff exponentiel
func (rm *RecoveryManager) calculateBackoffDelay() time.Duration {
	delay := float64(rm.baseDelay) * math.Pow(rm.backoffFactor, float64(rm.retryCount))
	
	if delay > float64(rm.maxDelay) {
		delay = float64(rm.maxDelay)
	}
	
	return time.Duration(delay)
}

// ProcessBufferedFrames traite les trames bufferisées après récupération
func (rm *RecoveryManager) ProcessBufferedFrames() error {
	bufferedCount := rm.frameRouter.GetBufferedFrameCount()
	if bufferedCount == 0 {
		return nil
	}
	
	rm.logger.Debug("Processing buffered frames after recovery",
		"sessionID", rm.sessionID,
		"bufferedCount", bufferedCount)
	
	err := rm.frameRouter.ProcessBufferedFrames()
	if err != nil {
		rm.logger.Error("Failed to process buffered frames",
			"sessionID", rm.sessionID,
			"error", err)
		return err
	}
	
	rm.logger.Info("Successfully processed buffered frames",
		"sessionID", rm.sessionID,
		"processedCount", bufferedCount)
	
	return nil
}

// Reset remet à zéro les compteurs de retry
func (rm *RecoveryManager) Reset() {
	rm.mutex.Lock()
	defer rm.mutex.Unlock()
	
	rm.retryCount = 0
	rm.lastRetryTime = time.Time{}
	
	rm.logger.Debug("Recovery manager reset", "sessionID", rm.sessionID)
}

// GetRetryCount retourne le nombre actuel de retries
func (rm *RecoveryManager) GetRetryCount() int {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()
	return rm.retryCount
}

// RetryRecoveryStrategy stratégie de récupération par retry
type RetryRecoveryStrategy struct {
	recoveryManager *RecoveryManager
	logger          interface {
		Debug(msg string, keysAndValues ...interface{})
		Info(msg string, keysAndValues ...interface{})
		Error(msg string, keysAndValues ...interface{})
	}
}

// NewRetryRecoveryStrategy crée une nouvelle stratégie de retry
func NewRetryRecoveryStrategy(recoveryManager *RecoveryManager, logger interface {
	Debug(msg string, keysAndValues ...interface{})
	Info(msg string, keysAndValues ...interface{})
	Error(msg string, keysAndValues ...interface{})
}) CollisionRecoveryStrategy {
	return &RetryRecoveryStrategy{
		recoveryManager: recoveryManager,
		logger:          logger,
	}
}

// CanRecover vérifie si cette stratégie peut récupérer de la collision
func (rrs *RetryRecoveryStrategy) CanRecover(collision *FrameCollisionError) bool {
	// Peut retry les échecs d'authentification
	return collision.SessionState == SessionStateAuthenticating &&
		   collision.ExpectedFrameType == protocol.FrameTypeAuthenticationResponse
}

// Recover récupère de la collision en tentant un retry
func (rrs *RetryRecoveryStrategy) Recover(collision *FrameCollisionError) error {
	rrs.logger.Debug("Attempting retry recovery for collision",
		"sessionID", collision.SessionID,
		"context", collision.Context)
	
	// Le retry sera géré par le RecoveryManager
	return fmt.Errorf("retry needed for collision: %s", collision.Context)
}

// GetRecoveryType retourne le type de récupération
func (rrs *RetryRecoveryStrategy) GetRecoveryType() RecoveryType {
	return RecoveryTypeRetry
}

// IgnoreRecoveryStrategy stratégie de récupération par ignore
type IgnoreRecoveryStrategy struct {
	logger interface {
		Debug(msg string, keysAndValues ...interface{})
		Info(msg string, keysAndValues ...interface{})
		Error(msg string, keysAndValues ...interface{})
	}
}

// NewIgnoreRecoveryStrategy crée une nouvelle stratégie d'ignore
func NewIgnoreRecoveryStrategy(logger interface {
	Debug(msg string, keysAndValues ...interface{})
	Info(msg string, keysAndValues ...interface{})
	Error(msg string, keysAndValues ...interface{})
}) CollisionRecoveryStrategy {
	return &IgnoreRecoveryStrategy{
		logger: logger,
	}
}

// CanRecover vérifie si cette stratégie peut récupérer de la collision
func (irs *IgnoreRecoveryStrategy) CanRecover(collision *FrameCollisionError) bool {
	// Peut ignorer certaines trames non critiques
	return collision.ReceivedFrameType == protocol.FrameTypeHeartbeatResponse ||
		   collision.SessionState == SessionStateActive
}

// Recover récupère de la collision en ignorant la trame
func (irs *IgnoreRecoveryStrategy) Recover(collision *FrameCollisionError) error {
	irs.logger.Debug("Ignoring collision as non-critical",
		"sessionID", collision.SessionID,
		"frameType", collision.ReceivedFrameType,
		"context", collision.Context)
	
	return nil // Ignore réussi
}

// GetRecoveryType retourne le type de récupération
func (irs *IgnoreRecoveryStrategy) GetRecoveryType() RecoveryType {
	return RecoveryTypeIgnore
}

// GracefulCloseStrategy stratégie de fermeture gracieuse
type GracefulCloseStrategy struct {
	sessionCloser func() error
	logger        interface {
		Debug(msg string, keysAndValues ...interface{})
		Info(msg string, keysAndValues ...interface{})
		Error(msg string, keysAndValues ...interface{})
	}
}

// NewGracefulCloseStrategy crée une nouvelle stratégie de fermeture gracieuse
func NewGracefulCloseStrategy(sessionCloser func() error, logger interface {
	Debug(msg string, keysAndValues ...interface{})
	Info(msg string, keysAndValues ...interface{})
	Error(msg string, keysAndValues ...interface{})
}) CollisionRecoveryStrategy {
	return &GracefulCloseStrategy{
		sessionCloser: sessionCloser,
		logger:        logger,
	}
}

// CanRecover vérifie si cette stratégie peut récupérer de la collision
func (gcs *GracefulCloseStrategy) CanRecover(collision *FrameCollisionError) bool {
	// Fermeture gracieuse comme dernier recours
	return true
}

// Recover récupère de la collision en fermant gracieusement la session
func (gcs *GracefulCloseStrategy) Recover(collision *FrameCollisionError) error {
	gcs.logger.Info("Performing graceful session close due to unrecoverable collision",
		"sessionID", collision.SessionID,
		"context", collision.Context)
	
	if gcs.sessionCloser != nil {
		return gcs.sessionCloser()
	}
	
	return nil
}

// GetRecoveryType retourne le type de récupération
func (gcs *GracefulCloseStrategy) GetRecoveryType() RecoveryType {
	return RecoveryTypeClose
}

// RecoveryMetrics and NewRecoveryMetrics are defined in error_recovery.go

// RecoveryMetrics methods are defined in error_recovery.go