package session

import (
	"fmt"
	"sync"

	"kwik/pkg/protocol"
)

// FrameRouter interface pour router les trames selon l'état de session
type FrameRouter interface {
	RouteFrame(frame protocol.Frame, sessionState SessionState) error
	BufferFrame(frame protocol.Frame) error
	ProcessBufferedFrames() error
	GetBufferedFrameCount() int
	ClearBuffer() error
}

// FrameBuffer gère la mise en buffer des trames
type FrameBuffer struct {
	frames  []protocol.Frame
	mutex   sync.RWMutex
	maxSize int
}

// NewFrameBuffer crée un nouveau buffer de trames
func NewFrameBuffer(maxSize int) *FrameBuffer {
	return &FrameBuffer{
		frames:  make([]protocol.Frame, 0),
		maxSize: maxSize,
	}
}

// Add ajoute une trame au buffer
func (fb *FrameBuffer) Add(frame protocol.Frame) error {
	fb.mutex.Lock()
	defer fb.mutex.Unlock()
	
	if len(fb.frames) >= fb.maxSize {
		return fmt.Errorf("frame buffer full: %d frames", fb.maxSize)
	}
	
	fb.frames = append(fb.frames, frame)
	return nil
}

// GetAll retourne toutes les trames bufferisées et vide le buffer
func (fb *FrameBuffer) GetAll() []protocol.Frame {
	fb.mutex.Lock()
	defer fb.mutex.Unlock()
	
	frames := make([]protocol.Frame, len(fb.frames))
	copy(frames, fb.frames)
	fb.frames = fb.frames[:0] // Vider le buffer
	
	return frames
}

// Count retourne le nombre de trames dans le buffer
func (fb *FrameBuffer) Count() int {
	fb.mutex.RLock()
	defer fb.mutex.RUnlock()
	return len(fb.frames)
}

// Clear vide le buffer
func (fb *FrameBuffer) Clear() {
	fb.mutex.Lock()
	defer fb.mutex.Unlock()
	fb.frames = fb.frames[:0]
}

// FrameRouterImpl implémentation du routeur de trames
type FrameRouterImpl struct {
	sessionID    string
	frameBuffer  *FrameBuffer
	logger       interface {
		Debug(msg string, keysAndValues ...interface{})
		Info(msg string, keysAndValues ...interface{})
		Error(msg string, keysAndValues ...interface{})
	}
	
	// Handlers pour différents types de trames
	authHandler      func(frame protocol.Frame) error
	heartbeatHandler func(frame protocol.Frame) error
	dataHandler      func(frame protocol.Frame) error
	controlHandler   func(frame protocol.Frame) error
}

// NewFrameRouter crée un nouveau routeur de trames
func NewFrameRouter(sessionID string, logger interface {
	Debug(msg string, keysAndValues ...interface{})
	Info(msg string, keysAndValues ...interface{})
	Error(msg string, keysAndValues ...interface{})
}) FrameRouter {
	return &FrameRouterImpl{
		sessionID:   sessionID,
		frameBuffer: NewFrameBuffer(100), // Buffer max de 100 trames
		logger:      logger,
	}
}

// SetAuthHandler définit le handler pour les trames d'authentification
func (fr *FrameRouterImpl) SetAuthHandler(handler func(frame protocol.Frame) error) {
	fr.authHandler = handler
}

// SetHeartbeatHandler définit le handler pour les trames heartbeat
func (fr *FrameRouterImpl) SetHeartbeatHandler(handler func(frame protocol.Frame) error) {
	fr.heartbeatHandler = handler
}

// SetDataHandler définit le handler pour les trames de données
func (fr *FrameRouterImpl) SetDataHandler(handler func(frame protocol.Frame) error) {
	fr.dataHandler = handler
}

// SetControlHandler définit le handler pour les trames de contrôle
func (fr *FrameRouterImpl) SetControlHandler(handler func(frame protocol.Frame) error) {
	fr.controlHandler = handler
}

// RouteFrame route une trame selon l'état de session
func (fr *FrameRouterImpl) RouteFrame(frame protocol.Frame, sessionState SessionState) error {
	frameType := frame.Type()
	
	fr.logger.Debug("Routing frame", 
		"sessionID", fr.sessionID,
		"frameType", frameType,
		"sessionState", sessionState.String())
	
	switch sessionState {
	case SessionStateConnecting:
		return fr.routeConnectingFrame(frame)
		
	case SessionStateAuthenticating:
		return fr.routeAuthenticatingFrame(frame)
		
	case SessionStateAuthenticated:
		return fr.routeAuthenticatedFrame(frame)
		
	case SessionStateActive:
		return fr.routeActiveFrame(frame)
		
	case SessionStateClosed:
		return fmt.Errorf("session is closed, cannot process frame type %v", frameType)
		
	default:
		return fmt.Errorf("unknown session state: %v", sessionState)
	}
}

// routeConnectingFrame route les trames pendant la connexion
func (fr *FrameRouterImpl) routeConnectingFrame(frame protocol.Frame) error {
	frameType := frame.Type()
	
	switch frameType {
	case protocol.FrameTypeAddPathRequest,
		 protocol.FrameTypeAddPathResponse,
		 protocol.FrameTypeAuthenticationRequest:
		// Ces trames sont acceptées pendant la connexion
		return fr.processControlFrame(frame)
		
	default:
		// Autres trames sont bufferisées
		fr.logger.Debug("Buffering frame during connecting state",
			"frameType", frameType,
			"sessionID", fr.sessionID)
		return fr.BufferFrame(frame)
	}
}

// routeAuthenticatingFrame route les trames pendant l'authentification
func (fr *FrameRouterImpl) routeAuthenticatingFrame(frame protocol.Frame) error {
	frameType := frame.Type()
	
	switch frameType {
	case protocol.FrameTypeAuthenticationRequest,
		 protocol.FrameTypeAuthenticationResponse:
		// Seules les trames d'authentification sont acceptées
		return fr.processAuthFrame(frame)
		
	case protocol.FrameTypeHeartbeat,
		 protocol.FrameTypeHeartbeatResponse:
		// Les heartbeats sont bufferisés pendant l'authentification
		fr.logger.Debug("Buffering heartbeat frame during authentication",
			"frameType", frameType,
			"sessionID", fr.sessionID)
		return fr.BufferFrame(frame)
		
	default:
		// Autres trames sont bufferisées
		fr.logger.Debug("Buffering frame during authentication",
			"frameType", frameType,
			"sessionID", fr.sessionID)
		return fr.BufferFrame(frame)
	}
}

// routeAuthenticatedFrame route les trames après authentification
func (fr *FrameRouterImpl) routeAuthenticatedFrame(frame protocol.Frame) error {
	frameType := frame.Type()
	
	switch frameType {
	case protocol.FrameTypeHeartbeat,
		 protocol.FrameTypeHeartbeatResponse:
		// Les heartbeats ne sont pas encore acceptés (pas encore actif)
		fr.logger.Debug("Buffering heartbeat frame in authenticated state",
			"frameType", frameType,
			"sessionID", fr.sessionID)
		return fr.BufferFrame(frame)
		
	default:
		// Autres trames sont traitées normalement
		return fr.routeActiveFrame(frame)
	}
}

// routeActiveFrame route les trames pendant l'état actif
func (fr *FrameRouterImpl) routeActiveFrame(frame protocol.Frame) error {
	frameType := frame.Type()
	
	switch frameType {
	case protocol.FrameTypeAuthenticationRequest,
		 protocol.FrameTypeAuthenticationResponse:
		return fr.processAuthFrame(frame)
		
	case protocol.FrameTypeHeartbeat,
		 protocol.FrameTypeHeartbeatResponse:
		return fr.processHeartbeatFrame(frame)
		
	case protocol.FrameTypeData,
		 protocol.FrameTypeAck:
		return fr.processDataFrame(frame)
		
	default:
		return fr.processControlFrame(frame)
	}
}

// processAuthFrame traite les trames d'authentification
func (fr *FrameRouterImpl) processAuthFrame(frame protocol.Frame) error {
	if fr.authHandler != nil {
		return fr.authHandler(frame)
	}
	fr.logger.Debug("No auth handler configured, ignoring frame",
		"frameType", frame.Type(),
		"sessionID", fr.sessionID)
	return nil
}

// processHeartbeatFrame traite les trames heartbeat
func (fr *FrameRouterImpl) processHeartbeatFrame(frame protocol.Frame) error {
	if fr.heartbeatHandler != nil {
		return fr.heartbeatHandler(frame)
	}
	fr.logger.Debug("No heartbeat handler configured, ignoring frame",
		"frameType", frame.Type(),
		"sessionID", fr.sessionID)
	return nil
}

// processDataFrame traite les trames de données
func (fr *FrameRouterImpl) processDataFrame(frame protocol.Frame) error {
	if fr.dataHandler != nil {
		return fr.dataHandler(frame)
	}
	fr.logger.Debug("No data handler configured, ignoring frame",
		"frameType", frame.Type(),
		"sessionID", fr.sessionID)
	return nil
}

// processControlFrame traite les trames de contrôle
func (fr *FrameRouterImpl) processControlFrame(frame protocol.Frame) error {
	if fr.controlHandler != nil {
		return fr.controlHandler(frame)
	}
	fr.logger.Debug("No control handler configured, ignoring frame",
		"frameType", frame.Type(),
		"sessionID", fr.sessionID)
	return nil
}

// BufferFrame met une trame en buffer
func (fr *FrameRouterImpl) BufferFrame(frame protocol.Frame) error {
	err := fr.frameBuffer.Add(frame)
	if err != nil {
		fr.logger.Error("Failed to buffer frame",
			"error", err,
			"frameType", frame.Type(),
			"sessionID", fr.sessionID)
		return err
	}
	
	fr.logger.Debug("Frame buffered",
		"frameType", frame.Type(),
		"bufferSize", fr.frameBuffer.Count(),
		"sessionID", fr.sessionID)
	
	return nil
}

// ProcessBufferedFrames traite toutes les trames bufferisées
func (fr *FrameRouterImpl) ProcessBufferedFrames() error {
	bufferedFrames := fr.frameBuffer.GetAll()
	
	if len(bufferedFrames) == 0 {
		return nil
	}
	
	fr.logger.Debug("Processing buffered frames",
		"count", len(bufferedFrames),
		"sessionID", fr.sessionID)
	
	for _, frame := range bufferedFrames {
		// Traiter les trames bufferisées comme si elles arrivaient en état actif
		err := fr.routeActiveFrame(frame)
		if err != nil {
			fr.logger.Error("Failed to process buffered frame",
				"error", err,
				"frameType", frame.Type(),
				"sessionID", fr.sessionID)
			// Continuer avec les autres trames même si une échoue
		}
	}
	
	return nil
}

// GetBufferedFrameCount retourne le nombre de trames bufferisées
func (fr *FrameRouterImpl) GetBufferedFrameCount() int {
	return fr.frameBuffer.Count()
}

// ClearBuffer vide le buffer de trames
func (fr *FrameRouterImpl) ClearBuffer() error {
	fr.frameBuffer.Clear()
	fr.logger.Debug("Frame buffer cleared", "sessionID", fr.sessionID)
	return nil
}