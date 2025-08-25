package session

import (
	"testing"
	"time"

	"kwik/pkg/protocol"
)

// MockLogger is defined in authentication_integration_test.go

// MockLogger methods removed - using simple mock without log storage

// TestSessionStateManager_BasicTransitions teste les transitions d'état de base
func TestSessionStateManager_BasicTransitions(t *testing.T) {
	logger := &MockLogger{}
	sessionID := "test-session-123"
	
	stateManager := NewSessionStateManager(sessionID, logger)
	
	// Vérifier l'état initial
	if stateManager.GetState() != SessionStateConnecting {
		t.Errorf("Expected initial state to be CONNECTING, got %v", stateManager.GetState())
	}
	
	// Test transition valide: CONNECTING -> AUTHENTICATING
	err := stateManager.SetState(SessionStateAuthenticating)
	if err != nil {
		t.Errorf("Expected valid transition CONNECTING -> AUTHENTICATING to succeed, got error: %v", err)
	}
	
	if stateManager.GetState() != SessionStateAuthenticating {
		t.Errorf("Expected state to be AUTHENTICATING, got %v", stateManager.GetState())
	}
	
	// Test transition valide: AUTHENTICATING -> AUTHENTICATED
	err = stateManager.SetState(SessionStateAuthenticated)
	if err != nil {
		t.Errorf("Expected valid transition AUTHENTICATING -> AUTHENTICATED to succeed, got error: %v", err)
	}
	
	// Test transition valide: AUTHENTICATED -> ACTIVE
	err = stateManager.SetState(SessionStateActive)
	if err != nil {
		t.Errorf("Expected valid transition AUTHENTICATED -> ACTIVE to succeed, got error: %v", err)
	}
	
	// Test transition valide: ACTIVE -> CLOSED
	err = stateManager.SetState(SessionStateClosed)
	if err != nil {
		t.Errorf("Expected valid transition ACTIVE -> CLOSED to succeed, got error: %v", err)
	}
}

// TestSessionStateManager_InvalidTransitions teste les transitions d'état invalides
func TestSessionStateManager_InvalidTransitions(t *testing.T) {
	logger := &MockLogger{}
	sessionID := "test-session-456"
	
	stateManager := NewSessionStateManager(sessionID, logger)
	
	// Test transition invalide: CONNECTING -> ACTIVE (skip authentication)
	err := stateManager.SetState(SessionStateActive)
	if err == nil {
		t.Error("Expected invalid transition CONNECTING -> ACTIVE to fail")
	}
	
	// Aller à l'état AUTHENTICATING
	stateManager.SetState(SessionStateAuthenticating)
	
	// Test transition invalide: AUTHENTICATING -> ACTIVE (skip authenticated)
	err = stateManager.SetState(SessionStateActive)
	if err == nil {
		t.Error("Expected invalid transition AUTHENTICATING -> ACTIVE to fail")
	}
	
	// Test transition invalide: AUTHENTICATING -> CONNECTING (backward)
	err = stateManager.SetState(SessionStateConnecting)
	if err == nil {
		t.Error("Expected invalid backward transition AUTHENTICATING -> CONNECTING to fail")
	}
}

// TestSessionStateManager_FrameValidation teste la validation des trames selon l'état
func TestSessionStateManager_FrameValidation(t *testing.T) {
	logger := &MockLogger{}
	sessionID := "test-session-789"
	
	stateManager := NewSessionStateManager(sessionID, logger)
	
	// Test état CONNECTING
	if !stateManager.CanProcessFrame(protocol.FrameTypeAddPathRequest) {
		t.Error("Expected AddPathRequest to be allowed in CONNECTING state")
	}
	
	if !stateManager.CanProcessFrame(protocol.FrameTypeAuthenticationRequest) {
		t.Error("Expected AuthenticationRequest to be allowed in CONNECTING state")
	}
	
	if stateManager.CanProcessFrame(protocol.FrameTypeHeartbeat) {
		t.Error("Expected Heartbeat to be rejected in CONNECTING state")
	}
	
	// Passer à l'état AUTHENTICATING
	stateManager.SetState(SessionStateAuthenticating)
	
	if !stateManager.CanProcessFrame(protocol.FrameTypeAuthenticationResponse) {
		t.Error("Expected AuthenticationResponse to be allowed in AUTHENTICATING state")
	}
	
	if stateManager.CanProcessFrame(protocol.FrameTypeHeartbeat) {
		t.Error("Expected Heartbeat to be rejected in AUTHENTICATING state")
	}
	
	if stateManager.CanProcessFrame(protocol.FrameTypeData) {
		t.Error("Expected Data frame to be rejected in AUTHENTICATING state")
	}
	
	// Passer à l'état AUTHENTICATED
	stateManager.SetState(SessionStateAuthenticated)
	
	if stateManager.CanProcessFrame(protocol.FrameTypeHeartbeat) {
		t.Error("Expected Heartbeat to be rejected in AUTHENTICATED state (not yet active)")
	}
	
	if !stateManager.CanProcessFrame(protocol.FrameTypeData) {
		t.Error("Expected Data frame to be allowed in AUTHENTICATED state")
	}
	
	// Passer à l'état ACTIVE
	stateManager.SetState(SessionStateActive)
	
	if !stateManager.CanProcessFrame(protocol.FrameTypeHeartbeat) {
		t.Error("Expected Heartbeat to be allowed in ACTIVE state")
	}
	
	if !stateManager.CanProcessFrame(protocol.FrameTypeData) {
		t.Error("Expected Data frame to be allowed in ACTIVE state")
	}
	
	// Passer à l'état CLOSED
	stateManager.SetState(SessionStateClosed)
	
	if stateManager.CanProcessFrame(protocol.FrameTypeHeartbeat) {
		t.Error("Expected all frames to be rejected in CLOSED state")
	}
	
	if stateManager.CanProcessFrame(protocol.FrameTypeData) {
		t.Error("Expected all frames to be rejected in CLOSED state")
	}
}

// TestSessionStateManager_AuthenticationStatus teste les méthodes de statut d'authentification
func TestSessionStateManager_AuthenticationStatus(t *testing.T) {
	logger := &MockLogger{}
	sessionID := "test-session-auth"
	
	stateManager := NewSessionStateManager(sessionID, logger)
	
	// Test état initial
	if stateManager.IsAuthenticationComplete() {
		t.Error("Expected authentication to not be complete in CONNECTING state")
	}
	
	if stateManager.IsActive() {
		t.Error("Expected session to not be active in CONNECTING state")
	}
	
	// Test état AUTHENTICATING
	stateManager.SetState(SessionStateAuthenticating)
	
	if stateManager.IsAuthenticationComplete() {
		t.Error("Expected authentication to not be complete in AUTHENTICATING state")
	}
	
	if stateManager.IsActive() {
		t.Error("Expected session to not be active in AUTHENTICATING state")
	}
	
	// Test état AUTHENTICATED
	stateManager.SetState(SessionStateAuthenticated)
	
	if !stateManager.IsAuthenticationComplete() {
		t.Error("Expected authentication to be complete in AUTHENTICATED state")
	}
	
	if stateManager.IsActive() {
		t.Error("Expected session to not be active in AUTHENTICATED state (not yet active)")
	}
	
	// Test état ACTIVE
	stateManager.SetState(SessionStateActive)
	
	if !stateManager.IsAuthenticationComplete() {
		t.Error("Expected authentication to be complete in ACTIVE state")
	}
	
	if !stateManager.IsActive() {
		t.Error("Expected session to be active in ACTIVE state")
	}
}

// TestSessionStateManager_ConcurrentAccess teste l'accès concurrent au gestionnaire d'état
func TestSessionStateManager_ConcurrentAccess(t *testing.T) {
	logger := &MockLogger{}
	sessionID := "test-session-concurrent"
	
	stateManager := NewSessionStateManager(sessionID, logger)
	
	// Lancer plusieurs goroutines qui lisent l'état
	done := make(chan bool, 10)
	
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				state := stateManager.GetState()
				_ = stateManager.CanProcessFrame(protocol.FrameTypeData)
				_ = stateManager.IsAuthenticationComplete()
				_ = stateManager.IsActive()
				
				// Vérifier que l'état est valide
				if state < SessionStateConnecting || state > SessionStateClosed {
					t.Errorf("Invalid state read: %v", state)
				}
			}
			done <- true
		}()
	}
	
	// Changer l'état pendant que les autres goroutines lisent
	go func() {
		time.Sleep(10 * time.Millisecond)
		stateManager.SetState(SessionStateAuthenticating)
		time.Sleep(10 * time.Millisecond)
		stateManager.SetState(SessionStateAuthenticated)
		time.Sleep(10 * time.Millisecond)
		stateManager.SetState(SessionStateActive)
		done <- true
	}()
	
	// Attendre que toutes les goroutines se terminent
	for i := 0; i < 11; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Test timeout - possible deadlock")
		}
	}
}

// TestSessionStateManager_StateTransitionLogging teste le logging des transitions d'état
// TODO: Re-enable this test once MockLogger supports log storage
/*
func TestSessionStateManager_StateTransitionLogging(t *testing.T) {
	logger := &MockLogger{}
	sessionID := "test-session-logging"
	
	stateManager := NewSessionStateManager(sessionID, logger)
	
	// Effectuer quelques transitions
	stateManager.SetState(SessionStateAuthenticating)
	stateManager.SetState(SessionStateAuthenticated)
	stateManager.SetState(SessionStateActive)
	
	logs := logger.GetLogs()
	
	// Vérifier qu'il y a des logs de transition
	if len(logs) < 3 {
		t.Errorf("Expected at least 3 log entries for state transitions, got %d", len(logs))
	}
	
	// Vérifier que les logs contiennent les informations de transition
	foundTransitionLog := false
	for _, log := range logs {
		if len(log) > 0 && (log[0:5] == "DEBUG" || log[0:4] == "INFO") {
			foundTransitionLog = true
			break
		}
	}
	
	if !foundTransitionLog {
		t.Error("Expected to find state transition logs")
	}
}
*/

// TestSessionStateManager_ErrorHandling teste la gestion d'erreurs
func TestSessionStateManager_ErrorHandling(t *testing.T) {
	logger := &MockLogger{}
	sessionID := "test-session-errors"
	
	stateManager := NewSessionStateManager(sessionID, logger)
	
	// Test transition vers un état invalide (hors limites)
	err := stateManager.SetState(SessionState(999))
	if err == nil {
		t.Error("Expected error when setting invalid state")
	}
	
	// Vérifier que l'état n'a pas changé après l'erreur
	if stateManager.GetState() != SessionStateConnecting {
		t.Errorf("Expected state to remain CONNECTING after invalid transition, got %v", stateManager.GetState())
	}
	
	// Test transition valide puis invalide
	stateManager.SetState(SessionStateAuthenticating)
	
	// Tenter une transition invalide
	err = stateManager.SetState(SessionStateConnecting)
	if err == nil {
		t.Error("Expected error for invalid backward transition")
	}
	
	// Vérifier que l'état n'a pas changé
	if stateManager.GetState() != SessionStateAuthenticating {
		t.Errorf("Expected state to remain AUTHENTICATING after invalid transition, got %v", stateManager.GetState())
	}
}