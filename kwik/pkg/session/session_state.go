package session

import (
	"fmt"
	"sync"

	"kwik/pkg/protocol"
)

// SessionState and constants are defined in interfaces.go

// SessionStateManager interface pour gérer les états de session
type SessionStateManager interface {
	GetState() SessionState
	SetState(state SessionState) error
	CanProcessFrame(frameType protocol.FrameType) bool
	IsAuthenticationComplete() bool
	IsActive() bool
}

// SessionStateManagerImpl implémentation du gestionnaire d'état de session
type SessionStateManagerImpl struct {
	currentState SessionState
	mutex        sync.RWMutex
	sessionID    string
	logger       interface {
		Debug(msg string, keysAndValues ...interface{})
		Info(msg string, keysAndValues ...interface{})
		Error(msg string, keysAndValues ...interface{})
	}
	
	// Canal pour notifier les changements d'état
	stateChangeCh chan SessionState
}

// NewSessionStateManager crée un nouveau gestionnaire d'état de session
func NewSessionStateManager(sessionID string, logger interface {
	Debug(msg string, keysAndValues ...interface{})
	Info(msg string, keysAndValues ...interface{})
	Error(msg string, keysAndValues ...interface{})
}) SessionStateManager {
	return &SessionStateManagerImpl{
		currentState:  SessionStateConnecting,
		sessionID:     sessionID,
		logger:        logger,
		stateChangeCh: make(chan SessionState, 10),
	}
}

// GetState retourne l'état actuel de la session
func (sm *SessionStateManagerImpl) GetState() SessionState {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()
	return sm.currentState
}

// SetState change l'état de la session avec validation
func (sm *SessionStateManagerImpl) SetState(newState SessionState) error {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()
	
	// Valider la transition d'état
	if !sm.isValidTransition(sm.currentState, newState) {
		return fmt.Errorf("invalid state transition from %s to %s for session %s", 
			sm.currentState, newState, sm.sessionID)
	}
	
	oldState := sm.currentState
	sm.currentState = newState
	
	sm.logger.Debug("Session state transition", 
		"sessionID", sm.sessionID,
		"from", oldState.String(),
		"to", newState.String())
	
	// Notifier le changement d'état
	select {
	case sm.stateChangeCh <- newState:
	default:
		// Canal plein, ignorer la notification
	}
	
	return nil
}

// isValidTransition vérifie si une transition d'état est valide
func (sm *SessionStateManagerImpl) isValidTransition(from, to SessionState) bool {
	validTransitions := map[SessionState][]SessionState{
		SessionStateConnecting: {
			SessionStateAuthenticating,
			SessionStateClosed,
		},
		SessionStateAuthenticating: {
			SessionStateAuthenticated,
			SessionStateClosed,
		},
		SessionStateAuthenticated: {
			SessionStateActive,
			SessionStateClosed,
		},
		SessionStateActive: {
			SessionStateClosed,
		},
		SessionStateClosed: {
			// Aucune transition depuis l'état fermé
		},
	}
	
	allowedStates, exists := validTransitions[from]
	if !exists {
		return false
	}
	
	for _, allowedState := range allowedStates {
		if allowedState == to {
			return true
		}
	}
	
	return false
}

// CanProcessFrame détermine si une trame peut être traitée selon l'état actuel
func (sm *SessionStateManagerImpl) CanProcessFrame(frameType protocol.FrameType) bool {
	state := sm.GetState()
	
	switch state {
	case SessionStateConnecting:
		// Seules les trames de connexion et d'authentification sont acceptées
		return frameType == protocol.FrameTypeAddPathRequest ||
			   frameType == protocol.FrameTypeAddPathResponse ||
			   frameType == protocol.FrameTypeAuthenticationRequest
			   
	case SessionStateAuthenticating:
		// Seules les trames d'authentification sont acceptées
		return frameType == protocol.FrameTypeAuthenticationRequest ||
			   frameType == protocol.FrameTypeAuthenticationResponse
			   
	case SessionStateAuthenticated:
		// Toutes les trames sauf heartbeat (pas encore actif)
		return frameType != protocol.FrameTypeHeartbeat &&
			   frameType != protocol.FrameTypeHeartbeatResponse
		
	case SessionStateActive:
		// Toutes les trames sont acceptées
		return true
		
	case SessionStateClosed:
		// Aucune trame n'est acceptée
		return false
		
	default:
		return false
	}
}

// IsAuthenticationComplete vérifie si l'authentification est terminée
func (sm *SessionStateManagerImpl) IsAuthenticationComplete() bool {
	state := sm.GetState()
	return state == SessionStateAuthenticated || state == SessionStateActive
}

// IsActive vérifie si la session est active
func (sm *SessionStateManagerImpl) IsActive() bool {
	return sm.GetState() == SessionStateActive
}

// GetStateChangeChan retourne le canal de notification des changements d'état
func (sm *SessionStateManagerImpl) GetStateChangeChan() <-chan SessionState {
	return sm.stateChangeCh
}

// Close ferme le gestionnaire d'état
func (sm *SessionStateManagerImpl) Close() {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()
	
	if sm.stateChangeCh != nil {
		close(sm.stateChangeCh)
		sm.stateChangeCh = nil
	}
}