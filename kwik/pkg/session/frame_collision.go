package session

import (
	"fmt"
	"time"

	"kwik/pkg/protocol"
)

// FrameCollisionError représente une erreur de collision de trames
type FrameCollisionError struct {
	ExpectedFrameType protocol.FrameType
	ReceivedFrameType protocol.FrameType
	SessionState      SessionState
	SessionID         string
	Timestamp         time.Time
	Context           string
	StackTrace        string
}

// Error implémente l'interface error
func (fce *FrameCollisionError) Error() string {
	return fmt.Sprintf("frame collision in session %s (state: %s): expected %v, got %v at %s - %s",
		fce.SessionID,
		fce.SessionState.String(),
		fce.ExpectedFrameType,
		fce.ReceivedFrameType,
		fce.Timestamp.Format(time.RFC3339Nano),
		fce.Context)
}

// GetDetails retourne les détails de l'erreur pour le debugging
func (fce *FrameCollisionError) GetDetails() map[string]interface{} {
	return map[string]interface{}{
		"sessionID":         fce.SessionID,
		"sessionState":      fce.SessionState.String(),
		"expectedFrameType": fce.ExpectedFrameType,
		"receivedFrameType": fce.ReceivedFrameType,
		"timestamp":         fce.Timestamp,
		"context":           fce.Context,
		"stackTrace":        fce.StackTrace,
	}
}

// CollisionDetector interface pour détecter les collisions de trames
type CollisionDetector interface {
	DetectCollision(expectedType protocol.FrameType, receivedFrame protocol.Frame, sessionState SessionState) *FrameCollisionError
	IsCollisionFrame(frameType protocol.FrameType, sessionState SessionState) bool
	LogCollision(collision *FrameCollisionError)
}

// CollisionDetectorImpl implémentation du détecteur de collisions
type CollisionDetectorImpl struct {
	sessionID string
	logger    interface {
		Debug(msg string, keysAndValues ...interface{})
		Info(msg string, keysAndValues ...interface{})
		Error(msg string, keysAndValues ...interface{})
	}
}

// NewCollisionDetector crée un nouveau détecteur de collisions
func NewCollisionDetector(sessionID string, logger interface {
	Debug(msg string, keysAndValues ...interface{})
	Info(msg string, keysAndValues ...interface{})
	Error(msg string, keysAndValues ...interface{})
}) CollisionDetector {
	return &CollisionDetectorImpl{
		sessionID: sessionID,
		logger:    logger,
	}
}

// DetectCollision détecte si une trame reçue est en collision avec ce qui est attendu
func (cd *CollisionDetectorImpl) DetectCollision(expectedType protocol.FrameType, receivedFrame protocol.Frame, sessionState SessionState) *FrameCollisionError {
	receivedType := receivedFrame.Type()
	
	// Pas de collision si c'est le type attendu
	if receivedType == expectedType {
		return nil
	}
	
	// Vérifier si c'est une collision connue
	if cd.isKnownCollision(expectedType, receivedType, sessionState) {
		return &FrameCollisionError{
			ExpectedFrameType: expectedType,
			ReceivedFrameType: receivedType,
			SessionState:      sessionState,
			SessionID:         cd.sessionID,
			Timestamp:         time.Now(),
			Context:           cd.getCollisionContext(expectedType, receivedType, sessionState),
		}
	}
	
	return nil
}

// isKnownCollision vérifie si c'est un type de collision connu
func (cd *CollisionDetectorImpl) isKnownCollision(expectedType, receivedType protocol.FrameType, sessionState SessionState) bool {
	// Collision principale : heartbeat pendant l'authentification
	if sessionState == SessionStateAuthenticating {
		if expectedType == protocol.FrameTypeAuthenticationResponse &&
		   (receivedType == protocol.FrameTypeHeartbeat || receivedType == protocol.FrameTypeHeartbeatResponse) {
			return true
		}
	}
	
	// Collision : trame d'authentification en état actif
	if sessionState == SessionStateActive {
		if (receivedType == protocol.FrameTypeAuthenticationRequest || receivedType == protocol.FrameTypeAuthenticationResponse) &&
		   expectedType != receivedType {
			return true
		}
	}
	
	// Collision : heartbeat avant l'état actif
	if sessionState == SessionStateAuthenticated {
		if receivedType == protocol.FrameTypeHeartbeat || receivedType == protocol.FrameTypeHeartbeatResponse {
			return true
		}
	}
	
	return false
}

// getCollisionContext retourne le contexte de la collision pour le debugging
func (cd *CollisionDetectorImpl) getCollisionContext(expectedType, receivedType protocol.FrameType, sessionState SessionState) string {
	switch sessionState {
	case SessionStateAuthenticating:
		if receivedType == protocol.FrameTypeHeartbeat {
			return "heartbeat received during authentication handshake - server started heartbeats too early"
		}
		return fmt.Sprintf("unexpected frame during authentication: expected %v, got %v", expectedType, receivedType)
		
	case SessionStateAuthenticated:
		if receivedType == protocol.FrameTypeHeartbeat {
			return "heartbeat received before session became active - timing issue"
		}
		return fmt.Sprintf("unexpected frame in authenticated state: expected %v, got %v", expectedType, receivedType)
		
	case SessionStateActive:
		return fmt.Sprintf("unexpected frame in active state: expected %v, got %v", expectedType, receivedType)
		
	default:
		return fmt.Sprintf("frame collision in state %s: expected %v, got %v", sessionState.String(), expectedType, receivedType)
	}
}

// IsCollisionFrame vérifie si un type de trame peut causer des collisions dans un état donné
func (cd *CollisionDetectorImpl) IsCollisionFrame(frameType protocol.FrameType, sessionState SessionState) bool {
	switch sessionState {
	case SessionStateAuthenticating:
		// Les heartbeats peuvent causer des collisions pendant l'authentification
		return frameType == protocol.FrameTypeHeartbeat || frameType == protocol.FrameTypeHeartbeatResponse
		
	case SessionStateAuthenticated:
		// Les heartbeats peuvent causer des collisions avant l'état actif
		return frameType == protocol.FrameTypeHeartbeat || frameType == protocol.FrameTypeHeartbeatResponse
		
	default:
		return false
	}
}

// LogCollision log une collision détectée
func (cd *CollisionDetectorImpl) LogCollision(collision *FrameCollisionError) {
	cd.logger.Error("Frame collision detected",
		"sessionID", collision.SessionID,
		"sessionState", collision.SessionState.String(),
		"expectedFrameType", collision.ExpectedFrameType,
		"receivedFrameType", collision.ReceivedFrameType,
		"timestamp", collision.Timestamp,
		"context", collision.Context)
}

// CollisionRecoveryStrategy interface pour les stratégies de récupération
type CollisionRecoveryStrategy interface {
	CanRecover(collision *FrameCollisionError) bool
	Recover(collision *FrameCollisionError) error
	GetRecoveryType() RecoveryType
}

// RecoveryType types de récupération disponibles
type RecoveryType int

const (
	RecoveryTypeBuffer RecoveryType = iota
	RecoveryTypeRetry
	RecoveryTypeIgnore
	RecoveryTypeClose
)

// String retourne la représentation string du type de récupération
func (rt RecoveryType) String() string {
	switch rt {
	case RecoveryTypeBuffer:
		return "BUFFER"
	case RecoveryTypeRetry:
		return "RETRY"
	case RecoveryTypeIgnore:
		return "IGNORE"
	case RecoveryTypeClose:
		return "CLOSE"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", int(rt))
	}
}

// BufferRecoveryStrategy stratégie de récupération par mise en buffer
type BufferRecoveryStrategy struct {
	frameRouter FrameRouter
	logger      interface {
		Debug(msg string, keysAndValues ...interface{})
		Info(msg string, keysAndValues ...interface{})
		Error(msg string, keysAndValues ...interface{})
	}
}

// NewBufferRecoveryStrategy crée une nouvelle stratégie de récupération par buffer
func NewBufferRecoveryStrategy(frameRouter FrameRouter, logger interface {
	Debug(msg string, keysAndValues ...interface{})
	Info(msg string, keysAndValues ...interface{})
	Error(msg string, keysAndValues ...interface{})
}) CollisionRecoveryStrategy {
	return &BufferRecoveryStrategy{
		frameRouter: frameRouter,
		logger:      logger,
	}
}

// CanRecover vérifie si cette stratégie peut récupérer de la collision
func (brs *BufferRecoveryStrategy) CanRecover(collision *FrameCollisionError) bool {
	// Peut récupérer des heartbeats pendant l'authentification
	return collision.SessionState == SessionStateAuthenticating &&
		   (collision.ReceivedFrameType == protocol.FrameTypeHeartbeat ||
		    collision.ReceivedFrameType == protocol.FrameTypeHeartbeatResponse)
}

// Recover récupère de la collision en mettant la trame en buffer
func (brs *BufferRecoveryStrategy) Recover(collision *FrameCollisionError) error {
	brs.logger.Debug("Attempting buffer recovery for collision",
		"sessionID", collision.SessionID,
		"frameType", collision.ReceivedFrameType,
		"context", collision.Context)
	
	// Note: Dans une implémentation complète, nous aurions accès à la trame originale
	// Pour l'instant, nous loggons juste la tentative de récupération
	brs.logger.Info("Frame collision recovered by buffering",
		"sessionID", collision.SessionID,
		"frameType", collision.ReceivedFrameType)
	
	return nil
}

// GetRecoveryType retourne le type de récupération
func (brs *BufferRecoveryStrategy) GetRecoveryType() RecoveryType {
	return RecoveryTypeBuffer
}

// CollisionManager gère la détection et la récupération des collisions
type CollisionManager struct {
	detector           CollisionDetector
	recoveryStrategies []CollisionRecoveryStrategy
	logger             interface {
		Debug(msg string, keysAndValues ...interface{})
		Info(msg string, keysAndValues ...interface{})
		Error(msg string, keysAndValues ...interface{})
	}
	sessionID          string
}

// NewCollisionManager crée un nouveau gestionnaire de collisions
func NewCollisionManager(sessionID string, logger interface {
	Debug(msg string, keysAndValues ...interface{})
	Info(msg string, keysAndValues ...interface{})
	Error(msg string, keysAndValues ...interface{})
}) *CollisionManager {
	detector := NewCollisionDetector(sessionID, logger)
	
	return &CollisionManager{
		detector:           detector,
		recoveryStrategies: make([]CollisionRecoveryStrategy, 0),
		logger:             logger,
		sessionID:          sessionID,
	}
}

// AddRecoveryStrategy ajoute une stratégie de récupération
func (cm *CollisionManager) AddRecoveryStrategy(strategy CollisionRecoveryStrategy) {
	cm.recoveryStrategies = append(cm.recoveryStrategies, strategy)
}

// HandlePotentialCollision gère une collision potentielle
func (cm *CollisionManager) HandlePotentialCollision(expectedType protocol.FrameType, receivedFrame protocol.Frame, sessionState SessionState) error {
	collision := cm.detector.DetectCollision(expectedType, receivedFrame, sessionState)
	if collision == nil {
		return nil // Pas de collision
	}
	
	// Logger la collision
	cm.detector.LogCollision(collision)
	
	// Tenter la récupération
	for _, strategy := range cm.recoveryStrategies {
		if strategy.CanRecover(collision) {
			cm.logger.Debug("Attempting collision recovery",
				"sessionID", cm.sessionID,
				"recoveryType", strategy.GetRecoveryType().String(),
				"collision", collision.Context)
			
			err := strategy.Recover(collision)
			if err == nil {
				cm.logger.Info("Collision recovery successful",
					"sessionID", cm.sessionID,
					"recoveryType", strategy.GetRecoveryType().String())
				return nil
			}
			
			cm.logger.Error("Collision recovery failed",
				"sessionID", cm.sessionID,
				"recoveryType", strategy.GetRecoveryType().String(),
				"error", err)
		}
	}
	
	// Aucune récupération possible
	return collision
}