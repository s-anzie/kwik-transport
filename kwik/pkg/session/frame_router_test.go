package session

import (
	"testing"
	"time"

	"kwik/pkg/protocol"
)

// MockFrame implémentation mock d'une trame pour les tests
type MockFrame struct {
	frameType protocol.FrameType
	data      []byte
}

func (mf *MockFrame) Type() protocol.FrameType {
	return mf.frameType
}

func (mf *MockFrame) Serialize() ([]byte, error) {
	return mf.data, nil
}

func (mf *MockFrame) Deserialize(data []byte) error {
	mf.data = data
	return nil
}

// NewMockFrame crée une nouvelle trame mock
func NewMockFrame(frameType protocol.FrameType) *MockFrame {
	return &MockFrame{
		frameType: frameType,
		data:      []byte("mock-data"),
	}
}

// TestFrameRouter_BasicRouting teste le routage de base des trames
func TestFrameRouter_BasicRouting(t *testing.T) {
	logger := &MockLogger{}
	sessionID := "test-session-router"
	
	router := NewFrameRouter(sessionID, logger)
	
	// Compteurs pour vérifier que les handlers sont appelés
	authHandlerCalled := 0
	heartbeatHandlerCalled := 0
	dataHandlerCalled := 0
	controlHandlerCalled := 0
	
	// Configurer les handlers
	routerImpl := router.(*FrameRouterImpl)
	routerImpl.SetAuthHandler(func(frame protocol.Frame) error {
		authHandlerCalled++
		return nil
	})
	routerImpl.SetHeartbeatHandler(func(frame protocol.Frame) error {
		heartbeatHandlerCalled++
		return nil
	})
	routerImpl.SetDataHandler(func(frame protocol.Frame) error {
		dataHandlerCalled++
		return nil
	})
	routerImpl.SetControlHandler(func(frame protocol.Frame) error {
		controlHandlerCalled++
		return nil
	})
	
	// Test routage en état ACTIVE (tous les handlers doivent être appelés)
	authFrame := NewMockFrame(protocol.FrameTypeAuthenticationRequest)
	err := router.RouteFrame(authFrame, SessionStateActive)
	if err != nil {
		t.Errorf("Expected auth frame routing to succeed, got error: %v", err)
	}
	if authHandlerCalled != 1 {
		t.Errorf("Expected auth handler to be called once, got %d", authHandlerCalled)
	}
	
	heartbeatFrame := NewMockFrame(protocol.FrameTypeHeartbeat)
	err = router.RouteFrame(heartbeatFrame, SessionStateActive)
	if err != nil {
		t.Errorf("Expected heartbeat frame routing to succeed, got error: %v", err)
	}
	if heartbeatHandlerCalled != 1 {
		t.Errorf("Expected heartbeat handler to be called once, got %d", heartbeatHandlerCalled)
	}
	
	dataFrame := NewMockFrame(protocol.FrameTypeData)
	err = router.RouteFrame(dataFrame, SessionStateActive)
	if err != nil {
		t.Errorf("Expected data frame routing to succeed, got error: %v", err)
	}
	if dataHandlerCalled != 1 {
		t.Errorf("Expected data handler to be called once, got %d", dataHandlerCalled)
	}
	
	controlFrame := NewMockFrame(protocol.FrameTypeAddPathRequest)
	err = router.RouteFrame(controlFrame, SessionStateActive)
	if err != nil {
		t.Errorf("Expected control frame routing to succeed, got error: %v", err)
	}
	if controlHandlerCalled != 1 {
		t.Errorf("Expected control handler to be called once, got %d", controlHandlerCalled)
	}
}

// TestFrameRouter_AuthenticatingState teste le routage pendant l'authentification
func TestFrameRouter_AuthenticatingState(t *testing.T) {
	logger := &MockLogger{}
	sessionID := "test-session-auth-routing"
	
	router := NewFrameRouter(sessionID, logger)
	
	authHandlerCalled := 0
	routerImpl := router.(*FrameRouterImpl)
	routerImpl.SetAuthHandler(func(frame protocol.Frame) error {
		authHandlerCalled++
		return nil
	})
	
	// Test: trame d'authentification doit être traitée
	authFrame := NewMockFrame(protocol.FrameTypeAuthenticationResponse)
	err := router.RouteFrame(authFrame, SessionStateAuthenticating)
	if err != nil {
		t.Errorf("Expected auth frame to be processed in AUTHENTICATING state, got error: %v", err)
	}
	if authHandlerCalled != 1 {
		t.Errorf("Expected auth handler to be called once, got %d", authHandlerCalled)
	}
	
	// Test: heartbeat doit être bufferisé
	heartbeatFrame := NewMockFrame(protocol.FrameTypeHeartbeat)
	err = router.RouteFrame(heartbeatFrame, SessionStateAuthenticating)
	if err != nil {
		t.Errorf("Expected heartbeat frame to be buffered in AUTHENTICATING state, got error: %v", err)
	}
	
	// Vérifier que la trame a été bufferisée
	if router.GetBufferedFrameCount() != 1 {
		t.Errorf("Expected 1 buffered frame, got %d", router.GetBufferedFrameCount())
	}
	
	// Test: trame de données doit être bufferisée
	dataFrame := NewMockFrame(protocol.FrameTypeData)
	err = router.RouteFrame(dataFrame, SessionStateAuthenticating)
	if err != nil {
		t.Errorf("Expected data frame to be buffered in AUTHENTICATING state, got error: %v", err)
	}
	
	if router.GetBufferedFrameCount() != 2 {
		t.Errorf("Expected 2 buffered frames, got %d", router.GetBufferedFrameCount())
	}
}

// TestFrameRouter_BufferManagement teste la gestion du buffer de trames
func TestFrameRouter_BufferManagement(t *testing.T) {
	logger := &MockLogger{}
	sessionID := "test-session-buffer"
	
	router := NewFrameRouter(sessionID, logger)
	
	// Ajouter plusieurs trames au buffer
	frames := []*MockFrame{
		NewMockFrame(protocol.FrameTypeHeartbeat),
		NewMockFrame(protocol.FrameTypeData),
		NewMockFrame(protocol.FrameTypeAck),
	}
	
	for _, frame := range frames {
		err := router.RouteFrame(frame, SessionStateAuthenticating)
		if err != nil {
			t.Errorf("Expected frame to be buffered, got error: %v", err)
		}
	}
	
	// Vérifier le nombre de trames bufferisées
	if router.GetBufferedFrameCount() != len(frames) {
		t.Errorf("Expected %d buffered frames, got %d", len(frames), router.GetBufferedFrameCount())
	}
	
	// Configurer les handlers pour compter les trames traitées
	processedFrames := 0
	routerImpl := router.(*FrameRouterImpl)
	routerImpl.SetHeartbeatHandler(func(frame protocol.Frame) error {
		processedFrames++
		return nil
	})
	routerImpl.SetDataHandler(func(frame protocol.Frame) error {
		processedFrames++
		return nil
	})
	
	// Traiter les trames bufferisées
	err := router.ProcessBufferedFrames()
	if err != nil {
		t.Errorf("Expected buffered frames processing to succeed, got error: %v", err)
	}
	
	// Vérifier que toutes les trames ont été traitées
	if processedFrames != len(frames) {
		t.Errorf("Expected %d frames to be processed, got %d", len(frames), processedFrames)
	}
	
	// Vérifier que le buffer est vide après traitement
	if router.GetBufferedFrameCount() != 0 {
		t.Errorf("Expected buffer to be empty after processing, got %d frames", router.GetBufferedFrameCount())
	}
}

// TestFrameRouter_StateTransitions teste le routage lors des transitions d'état
func TestFrameRouter_StateTransitions(t *testing.T) {
	logger := &MockLogger{}
	sessionID := "test-session-transitions"
	
	router := NewFrameRouter(sessionID, logger)
	
	// Test état CONNECTING
	authFrame := NewMockFrame(protocol.FrameTypeAuthenticationRequest)
	err := router.RouteFrame(authFrame, SessionStateConnecting)
	if err != nil {
		t.Errorf("Expected auth request to be allowed in CONNECTING state, got error: %v", err)
	}
	
	heartbeatFrame := NewMockFrame(protocol.FrameTypeHeartbeat)
	err = router.RouteFrame(heartbeatFrame, SessionStateConnecting)
	if err != nil {
		t.Errorf("Expected heartbeat to be buffered in CONNECTING state, got error: %v", err)
	}
	
	// Test état AUTHENTICATED
	err = router.RouteFrame(heartbeatFrame, SessionStateAuthenticated)
	if err != nil {
		t.Errorf("Expected heartbeat to be buffered in AUTHENTICATED state, got error: %v", err)
	}
	
	dataFrame := NewMockFrame(protocol.FrameTypeData)
	err = router.RouteFrame(dataFrame, SessionStateAuthenticated)
	if err != nil {
		t.Errorf("Expected data frame to be processed in AUTHENTICATED state, got error: %v", err)
	}
	
	// Test état CLOSED
	err = router.RouteFrame(authFrame, SessionStateClosed)
	if err == nil {
		t.Error("Expected frames to be rejected in CLOSED state")
	}
	
	err = router.RouteFrame(dataFrame, SessionStateClosed)
	if err == nil {
		t.Error("Expected frames to be rejected in CLOSED state")
	}
}

// TestFrameRouter_BufferOverflow teste le comportement en cas de débordement du buffer
func TestFrameRouter_BufferOverflow(t *testing.T) {
	logger := &MockLogger{}
	sessionID := "test-session-overflow"
	
	router := NewFrameRouter(sessionID, logger)
	
	// Remplir le buffer jusqu'à sa capacité maximale
	// Le buffer par défaut a une taille de 100
	for i := 0; i < 100; i++ {
		frame := NewMockFrame(protocol.FrameTypeHeartbeat)
		err := router.RouteFrame(frame, SessionStateAuthenticating)
		if err != nil {
			t.Errorf("Expected frame %d to be buffered, got error: %v", i, err)
		}
	}
	
	// Vérifier que le buffer est plein
	if router.GetBufferedFrameCount() != 100 {
		t.Errorf("Expected buffer to be full (100 frames), got %d", router.GetBufferedFrameCount())
	}
	
	// Tenter d'ajouter une trame supplémentaire (devrait échouer)
	overflowFrame := NewMockFrame(protocol.FrameTypeHeartbeat)
	err := router.RouteFrame(overflowFrame, SessionStateAuthenticating)
	if err == nil {
		t.Error("Expected buffer overflow to cause an error")
	}
	
	// Vérifier que le buffer n'a pas dépassé sa capacité
	if router.GetBufferedFrameCount() > 100 {
		t.Errorf("Expected buffer to not exceed capacity, got %d frames", router.GetBufferedFrameCount())
	}
}

// TestFrameRouter_ClearBuffer teste la fonction de vidage du buffer
func TestFrameRouter_ClearBuffer(t *testing.T) {
	logger := &MockLogger{}
	sessionID := "test-session-clear"
	
	router := NewFrameRouter(sessionID, logger)
	
	// Ajouter quelques trames au buffer
	for i := 0; i < 5; i++ {
		frame := NewMockFrame(protocol.FrameTypeHeartbeat)
		router.RouteFrame(frame, SessionStateAuthenticating)
	}
	
	// Vérifier que le buffer contient des trames
	if router.GetBufferedFrameCount() != 5 {
		t.Errorf("Expected 5 buffered frames, got %d", router.GetBufferedFrameCount())
	}
	
	// Vider le buffer
	err := router.ClearBuffer()
	if err != nil {
		t.Errorf("Expected buffer clear to succeed, got error: %v", err)
	}
	
	// Vérifier que le buffer est vide
	if router.GetBufferedFrameCount() != 0 {
		t.Errorf("Expected buffer to be empty after clear, got %d frames", router.GetBufferedFrameCount())
	}
}

// TestFrameBuffer_ConcurrentAccess teste l'accès concurrent au buffer de trames
func TestFrameBuffer_ConcurrentAccess(t *testing.T) {
	buffer := NewFrameBuffer(50)
	
	// Lancer plusieurs goroutines qui ajoutent des trames
	done := make(chan bool, 10)
	
	for i := 0; i < 5; i++ {
		go func(id int) {
			for j := 0; j < 5; j++ {
				frame := NewMockFrame(protocol.FrameTypeHeartbeat)
				buffer.Add(frame)
			}
			done <- true
		}(i)
	}
	
	// Lancer des goroutines qui lisent le buffer
	for i := 0; i < 5; i++ {
		go func() {
			for j := 0; j < 10; j++ {
				buffer.Count()
				buffer.GetAll() // Vide le buffer
			}
			done <- true
		}()
	}
	
	// Attendre que toutes les goroutines se terminent
	for i := 0; i < 10; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Test timeout - possible deadlock in frame buffer")
		}
	}
	
	// Le test réussit s'il n'y a pas de deadlock ou de race condition
}