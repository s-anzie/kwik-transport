package session

import (
	"context"
	"sync"
	"testing"
	"time"

	"kwik/pkg/protocol"
)

// MockLogger implémente l'interface Logger pour les tests
type MockLogger struct{}

func (ml *MockLogger) Debug(msg string, keysAndValues ...interface{}) {}
func (ml *MockLogger) Info(msg string, keysAndValues ...interface{})  {}
func (ml *MockLogger) Error(msg string, keysAndValues ...interface{}) {}

// MockAuthenticationManager mock du gestionnaire d'authentification pour les tests
type MockAuthenticationManager struct {
	sessionID      string
	isAuthenticated bool
	authDelay      time.Duration
	mutex          sync.RWMutex
}

func NewMockAuthenticationManager(sessionID string) *MockAuthenticationManager {
	return &MockAuthenticationManager{
		sessionID:       sessionID,
		isAuthenticated: false,
		authDelay:       50 * time.Millisecond, // Délai simulé pour l'authentification
	}
}

func (mam *MockAuthenticationManager) IsAuthenticated() bool {
	mam.mutex.RLock()
	defer mam.mutex.RUnlock()
	return mam.isAuthenticated
}

func (mam *MockAuthenticationManager) SetAuthenticated(authenticated bool) {
	mam.mutex.Lock()
	defer mam.mutex.Unlock()
	mam.isAuthenticated = authenticated
}

func (mam *MockAuthenticationManager) SimulateAuthentication() {
	// Simuler un délai d'authentification
	time.Sleep(mam.authDelay)
	mam.SetAuthenticated(true)
}

// MockSession session mock pour les tests d'intégration
type MockSession struct {
	sessionID        string
	stateManager     SessionStateManager
	frameRouter      FrameRouter
	recoveryManager  *RecoveryManager
	authManager      *MockAuthenticationManager
	logger           interface {
		Debug(msg string, keysAndValues ...interface{})
		Info(msg string, keysAndValues ...interface{})
		Error(msg string, keysAndValues ...interface{})
	}
	
	// Canaux pour la synchronisation des tests
	authCompleteCh   chan bool
	heartbeatStartCh chan bool
	healthStartCh    chan bool
	
	// Compteurs pour vérifier les appels
	heartbeatStarted bool
	healthStarted    bool
	mutex            sync.RWMutex
}

func NewMockSession(sessionID string, logger interface {
	Debug(msg string, keysAndValues ...interface{})
	Info(msg string, keysAndValues ...interface{})
	Error(msg string, keysAndValues ...interface{})
}) *MockSession {
	stateManager := NewSessionStateManager(sessionID, logger)
	frameRouter := NewFrameRouter(sessionID, logger)
	recoveryManager := NewRecoveryManager(sessionID, frameRouter, logger, nil)
	authManager := NewMockAuthenticationManager(sessionID)
	
	return &MockSession{
		sessionID:        sessionID,
		stateManager:     stateManager,
		frameRouter:      frameRouter,
		recoveryManager:  recoveryManager,
		authManager:      authManager,
		logger:           logger,
		authCompleteCh:   make(chan bool, 1),
		heartbeatStartCh: make(chan bool, 1),
		healthStartCh:    make(chan bool, 1),
	}
}

func (ms *MockSession) StartAuthentication(ctx context.Context) error {
	// Entrer en état d'authentification
	err := ms.stateManager.SetState(SessionStateAuthenticating)
	if err != nil {
		return err
	}
	
	// Simuler l'authentification en arrière-plan
	go func() {
		ms.authManager.SimulateAuthentication()
		
		// Changer l'état vers authentifié
		ms.stateManager.SetState(SessionStateAuthenticated)
		
		// Notifier que l'authentification est terminée
		select {
		case ms.authCompleteCh <- true:
		default:
		}
	}()
	
	return nil
}

func (ms *MockSession) StartPostAuthServices() error {
	// Attendre que l'authentification soit terminée
	if !ms.stateManager.IsAuthenticationComplete() {
		return nil // Pas encore prêt
	}
	
	// Changer vers l'état actif
	err := ms.stateManager.SetState(SessionStateActive)
	if err != nil {
		return err
	}
	
	// Démarrer les services post-authentification
	ms.startHealthMonitoring()
	ms.startHeartbeatManagement()
	
	return nil
}

func (ms *MockSession) startHealthMonitoring() {
	ms.mutex.Lock()
	defer ms.mutex.Unlock()
	
	if !ms.stateManager.IsAuthenticationComplete() {
		ms.logger.Debug("Cannot start health monitoring: authentication not complete")
		return
	}
	
	ms.healthStarted = true
	ms.logger.Debug("Health monitoring started")
	
	select {
	case ms.healthStartCh <- true:
	default:
	}
}

func (ms *MockSession) startHeartbeatManagement() {
	ms.mutex.Lock()
	defer ms.mutex.Unlock()
	
	if !ms.stateManager.IsActive() {
		ms.logger.Debug("Cannot start heartbeat management: session not active")
		return
	}
	
	ms.heartbeatStarted = true
	ms.logger.Debug("Heartbeat management started")
	
	select {
	case ms.heartbeatStartCh <- true:
	default:
	}
}

func (ms *MockSession) IsHeartbeatStarted() bool {
	ms.mutex.RLock()
	defer ms.mutex.RUnlock()
	return ms.heartbeatStarted
}

func (ms *MockSession) IsHealthStarted() bool {
	ms.mutex.RLock()
	defer ms.mutex.RUnlock()
	return ms.healthStarted
}

func (ms *MockSession) GetState() SessionState {
	return ms.stateManager.GetState()
}

func (ms *MockSession) ProcessFrame(frame protocol.Frame) error {
	return ms.frameRouter.RouteFrame(frame, ms.stateManager.GetState())
}

// TestAuthenticationFlow_Complete teste le flux complet d'authentification
func TestAuthenticationFlow_Complete(t *testing.T) {
	logger := &MockLogger{}
	sessionID := "test-auth-flow-complete"
	
	session := NewMockSession(sessionID, logger)
	
	// Vérifier l'état initial
	if session.GetState() != SessionStateConnecting {
		t.Errorf("Expected initial state to be CONNECTING, got %v", session.GetState())
	}
	
	// Démarrer l'authentification
	ctx := context.Background()
	err := session.StartAuthentication(ctx)
	if err != nil {
		t.Fatalf("Failed to start authentication: %v", err)
	}
	
	// Vérifier que l'état est passé à AUTHENTICATING
	if session.GetState() != SessionStateAuthenticating {
		t.Errorf("Expected state to be AUTHENTICATING, got %v", session.GetState())
	}
	
	// Attendre que l'authentification se termine
	select {
	case <-session.authCompleteCh:
		// Authentification terminée
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Authentication timeout")
	}
	
	// Vérifier que l'état est passé à AUTHENTICATED
	if session.GetState() != SessionStateAuthenticated {
		t.Errorf("Expected state to be AUTHENTICATED, got %v", session.GetState())
	}
	
	// Démarrer les services post-authentification
	err = session.StartPostAuthServices()
	if err != nil {
		t.Fatalf("Failed to start post-auth services: %v", err)
	}
	
	// Vérifier que l'état est passé à ACTIVE
	if session.GetState() != SessionStateActive {
		t.Errorf("Expected state to be ACTIVE, got %v", session.GetState())
	}
	
	// Attendre que les services démarrent
	select {
	case <-session.healthStartCh:
		// Health monitoring démarré
	case <-time.After(100 * time.Millisecond):
		t.Error("Health monitoring did not start")
	}
	
	select {
	case <-session.heartbeatStartCh:
		// Heartbeat management démarré
	case <-time.After(100 * time.Millisecond):
		t.Error("Heartbeat management did not start")
	}
	
	// Vérifier que les services sont démarrés
	if !session.IsHealthStarted() {
		t.Error("Expected health monitoring to be started")
	}
	
	if !session.IsHeartbeatStarted() {
		t.Error("Expected heartbeat management to be started")
	}
}

// TestAuthenticationFlow_HeartbeatBuffering teste la mise en buffer des heartbeats pendant l'authentification
func TestAuthenticationFlow_HeartbeatBuffering(t *testing.T) {
	logger := &MockLogger{}
	sessionID := "test-auth-heartbeat-buffer"
	
	session := NewMockSession(sessionID, logger)
	
	// Démarrer l'authentification
	ctx := context.Background()
	session.StartAuthentication(ctx)
	
	// Vérifier que nous sommes en état d'authentification
	if session.GetState() != SessionStateAuthenticating {
		t.Fatalf("Expected AUTHENTICATING state, got %v", session.GetState())
	}
	
	// Envoyer des trames heartbeat pendant l'authentification (elles doivent être bufferisées)
	heartbeatFrames := []*MockFrame{
		NewMockFrame(protocol.FrameTypeHeartbeat),
		NewMockFrame(protocol.FrameTypeHeartbeat),
		NewMockFrame(protocol.FrameTypeHeartbeatResponse),
	}
	
	for i, frame := range heartbeatFrames {
		err := session.ProcessFrame(frame)
		if err != nil {
			t.Errorf("Expected heartbeat frame %d to be buffered, got error: %v", i, err)
		}
	}
	
	// Vérifier que les trames sont bufferisées
	bufferedCount := session.frameRouter.GetBufferedFrameCount()
	if bufferedCount != len(heartbeatFrames) {
		t.Errorf("Expected %d buffered frames, got %d", len(heartbeatFrames), bufferedCount)
	}
	
	// Attendre que l'authentification se termine
	select {
	case <-session.authCompleteCh:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Authentication timeout")
	}
	
	// Démarrer les services post-authentification
	session.StartPostAuthServices()
	
	// Configurer un handler pour compter les trames traitées
	processedFrames := 0
	routerImpl := session.frameRouter.(*FrameRouterImpl)
	routerImpl.SetHeartbeatHandler(func(frame protocol.Frame) error {
		processedFrames++
		return nil
	})
	
	// Traiter les trames bufferisées
	err := session.frameRouter.ProcessBufferedFrames()
	if err != nil {
		t.Errorf("Failed to process buffered frames: %v", err)
	}
	
	// Vérifier que toutes les trames ont été traitées
	if processedFrames != len(heartbeatFrames) {
		t.Errorf("Expected %d frames to be processed, got %d", len(heartbeatFrames), processedFrames)
	}
	
	// Vérifier que le buffer est vide
	if session.frameRouter.GetBufferedFrameCount() != 0 {
		t.Errorf("Expected buffer to be empty after processing, got %d frames", 
			session.frameRouter.GetBufferedFrameCount())
	}
}

// TestAuthenticationFlow_FrameCollisionDetection teste la détection des collisions de trames
func TestAuthenticationFlow_FrameCollisionDetection(t *testing.T) {
	logger := &MockLogger{}
	sessionID := "test-auth-collision"
	
	session := NewMockSession(sessionID, logger)
	
	// Démarrer l'authentification
	ctx := context.Background()
	session.StartAuthentication(ctx)
	
	// Créer un détecteur de collisions
	collisionDetector := NewCollisionDetector(sessionID, logger)
	
	// Simuler une collision : heartbeat reçu pendant l'authentification
	// quand on attend une réponse d'authentification
	heartbeatFrame := NewMockFrame(protocol.FrameTypeHeartbeat)
	collision := collisionDetector.DetectCollision(
		protocol.FrameTypeAuthenticationResponse,
		heartbeatFrame,
		SessionStateAuthenticating,
	)
	
	// Vérifier qu'une collision est détectée
	if collision == nil {
		t.Error("Expected collision to be detected for heartbeat during authentication")
	}
	
	if collision != nil {
		if collision.ExpectedFrameType != protocol.FrameTypeAuthenticationResponse {
			t.Errorf("Expected collision expected type to be AuthenticationResponse, got %v", 
				collision.ExpectedFrameType)
		}
		
		if collision.ReceivedFrameType != protocol.FrameTypeHeartbeat {
			t.Errorf("Expected collision received type to be Heartbeat, got %v", 
				collision.ReceivedFrameType)
		}
		
		if collision.SessionState != SessionStateAuthenticating {
			t.Errorf("Expected collision session state to be AUTHENTICATING, got %v", 
				collision.SessionState)
		}
	}
	
	// Tester qu'il n'y a pas de collision pour une trame valide
	authFrame := NewMockFrame(protocol.FrameTypeAuthenticationResponse)
	noCollision := collisionDetector.DetectCollision(
		protocol.FrameTypeAuthenticationResponse,
		authFrame,
		SessionStateAuthenticating,
	)
	
	if noCollision != nil {
		t.Error("Expected no collision for valid authentication response")
	}
}

// TestAuthenticationFlow_RecoveryMechanism teste les mécanismes de récupération
func TestAuthenticationFlow_RecoveryMechanism(t *testing.T) {
	logger := &MockLogger{}
	sessionID := "test-auth-recovery"
	
	session := NewMockSession(sessionID, logger)
	
	// Démarrer l'authentification
	ctx := context.Background()
	session.StartAuthentication(ctx)
	
	// Simuler une collision et tenter la récupération
	heartbeatFrame := NewMockFrame(protocol.FrameTypeHeartbeat)
	
	err := session.recoveryManager.HandleAuthenticationCollision(
		protocol.FrameTypeAuthenticationResponse,
		heartbeatFrame,
		SessionStateAuthenticating,
	)
	
	// La récupération devrait réussir (buffer strategy)
	if err != nil {
		t.Errorf("Expected collision recovery to succeed, got error: %v", err)
	}
	
	// Vérifier que la trame a été bufferisée (récupération par buffer)
	if session.frameRouter.GetBufferedFrameCount() == 0 {
		t.Error("Expected frame to be buffered as part of recovery")
	}
}

// TestAuthenticationFlow_ConcurrentOperations teste les opérations concurrentes pendant l'authentification
func TestAuthenticationFlow_ConcurrentOperations(t *testing.T) {
	logger := &MockLogger{}
	sessionID := "test-auth-concurrent"
	
	session := NewMockSession(sessionID, logger)
	
	// Démarrer l'authentification
	ctx := context.Background()
	session.StartAuthentication(ctx)
	
	// Lancer plusieurs goroutines qui envoient des trames pendant l'authentification
	var wg sync.WaitGroup
	framesSent := 0
	
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			
			for j := 0; j < 10; j++ {
				frame := NewMockFrame(protocol.FrameTypeHeartbeat)
				err := session.ProcessFrame(frame)
				if err == nil {
					framesSent++
				}
				time.Sleep(1 * time.Millisecond)
			}
		}(i)
	}
	
	// Lancer une goroutine qui vérifie l'état de session
	wg.Add(1)
	go func() {
		defer wg.Done()
		
		for i := 0; i < 20; i++ {
			state := session.GetState()
			_ = session.stateManager.IsAuthenticationComplete()
			_ = session.stateManager.IsActive()
			
			// Vérifier que l'état est valide
			if state < SessionStateConnecting || state > SessionStateClosed {
				t.Errorf("Invalid state during concurrent operations: %v", state)
			}
			
			time.Sleep(2 * time.Millisecond)
		}
	}()
	
	// Attendre que toutes les goroutines se terminent
	wg.Wait()
	
	// Attendre que l'authentification se termine
	select {
	case <-session.authCompleteCh:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("Authentication timeout during concurrent operations")
	}
	
	// Vérifier que des trames ont été bufferisées
	if session.frameRouter.GetBufferedFrameCount() == 0 {
		t.Error("Expected some frames to be buffered during concurrent operations")
	}
}

// TestAuthenticationFlow_StateTransitionTiming teste le timing des transitions d'état
func TestAuthenticationFlow_StateTransitionTiming(t *testing.T) {
	logger := &MockLogger{}
	sessionID := "test-auth-timing"
	
	session := NewMockSession(sessionID, logger)
	
	// Enregistrer les temps de transition
	startTime := time.Now()
	var authTime, activeTime time.Time
	
	// Démarrer l'authentification
	ctx := context.Background()
	session.StartAuthentication(ctx)
	
	// Surveiller les transitions d'état
	go func() {
		for {
			state := session.GetState()
			switch state {
			case SessionStateAuthenticated:
				if authTime.IsZero() {
					authTime = time.Now()
				}
			case SessionStateActive:
				if activeTime.IsZero() {
					activeTime = time.Now()
				}
				return
			}
			time.Sleep(1 * time.Millisecond)
		}
	}()
	
	// Attendre que l'authentification se termine
	select {
	case <-session.authCompleteCh:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Authentication timeout")
	}
	
	// Démarrer les services post-authentification
	session.StartPostAuthServices()
	
	// Attendre que l'état devienne actif
	for i := 0; i < 100 && session.GetState() != SessionStateActive; i++ {
		time.Sleep(1 * time.Millisecond)
	}
	
	// Vérifier le timing des transitions
	if authTime.IsZero() {
		t.Error("Authentication state transition was not recorded")
	}
	
	if activeTime.IsZero() {
		t.Error("Active state transition was not recorded")
	}
	
	// Vérifier que les transitions se sont faites dans l'ordre correct
	if !authTime.After(startTime) {
		t.Error("Authentication should happen after start")
	}
	
	if !activeTime.After(authTime) {
		t.Error("Active state should come after authentication")
	}
	
	// Vérifier que l'authentification ne prend pas trop de temps
	authDuration := authTime.Sub(startTime)
	if authDuration > 150*time.Millisecond {
		t.Errorf("Authentication took too long: %v", authDuration)
	}
}