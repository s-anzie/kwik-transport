package kwik

import (
	"context"
	"fmt"
	"sync"
	"time"

	"kwik/pkg/session"
)

// SessionManager manages KWIK sessions and provides session lifecycle management
type SessionManager struct {
	// Session storage
	clientSessions map[string]*session.ClientSession
	serverSessions map[string]*session.ServerSession
	listeners      map[string]session.Listener

	// Configuration
	config *Config
	logger Logger

	// Synchronization
	mutex sync.RWMutex

	// State
	active bool
}

// NewSessionManager creates a new session manager
func NewSessionManager(config *Config, logger Logger) *SessionManager {
	return &SessionManager{
		clientSessions: make(map[string]*session.ClientSession),
		serverSessions: make(map[string]*session.ServerSession),
		listeners:      make(map[string]session.Listener),
		config:         config,
		logger:         logger,
		active:         false,
	}
}

// Start starts the session manager
func (sm *SessionManager) Start() error {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	if sm.active {
		return fmt.Errorf("session manager is already active")
	}

	sm.active = true
	sm.logger.Info("Session manager started")

	return nil
}

// Stop stops the session manager and closes all sessions
func (sm *SessionManager) Stop() error {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	if !sm.active {
		return fmt.Errorf("session manager is not active")
	}

	// Close all client sessions
	for sessionID, clientSession := range sm.clientSessions {
		err := clientSession.Close()
		if err != nil {
			sm.logger.Error("Failed to close client session", "sessionID", sessionID, "error", err)
		}
	}

	// Close all server sessions
	for sessionID, serverSession := range sm.serverSessions {
		err := serverSession.Close()
		if err != nil {
			sm.logger.Error("Failed to close server session", "sessionID", sessionID, "error", err)
		}
	}

	// Close all listeners
	for address, listener := range sm.listeners {
		err := listener.Close()
		if err != nil {
			sm.logger.Error("Failed to close listener", "address", address, "error", err)
		}
	}

	// Clear all maps
	sm.clientSessions = make(map[string]*session.ClientSession)
	sm.serverSessions = make(map[string]*session.ServerSession)
	sm.listeners = make(map[string]session.Listener)

	sm.active = false
	sm.logger.Info("Session manager stopped")

	return nil
}

// CreateClientSession creates a new client session
func (sm *SessionManager) CreateClientSession(ctx context.Context, address string, config *session.SessionConfig) (*session.ClientSession, error) {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	if !sm.active {
		return nil, fmt.Errorf("session manager is not active")
	}

	// Check session limit
	if len(sm.clientSessions) >= sm.config.MaxSessions {
		return nil, fmt.Errorf("maximum number of client sessions reached")
	}

	// Use the centralized Dial method to avoid duplication

	// Create session config for Dial
	sessionConfigForDial := config
	if sessionConfigForDial == nil {
		sessionConfigForDial = session.DefaultSessionConfig()
	}

	// Use session.Dial which handles everything properly
	dialedSession, err := session.Dial(ctx, address, sessionConfigForDial)
	if err != nil {
		return nil, fmt.Errorf("failed to dial session: %w", err)
	}

	// Convert to ClientSession (should be safe since Dial returns ClientSession)
	clientSession, ok := dialedSession.(*session.ClientSession)
	if !ok {
		dialedSession.Close()
		return nil, fmt.Errorf("session.Dial returned unexpected session type")
	}

	// Store session
	sessionID := clientSession.GetSessionID()
	sm.clientSessions[sessionID] = clientSession

	// Set up session cleanup on close
	go sm.cleanupClientSessionOnClose(sessionID, clientSession)

	sm.logger.Info("Client session created", "sessionID", sessionID, "address", address)

	return clientSession, nil
}

// CreateListener creates a new server listener
func (sm *SessionManager) CreateListener(address string, config *session.SessionConfig) (session.Listener, error) {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	if !sm.active {
		return nil, fmt.Errorf("session manager is not active")
	}

	// Check if listener already exists for this address
	if _, exists := sm.listeners[address]; exists {
		return nil, fmt.Errorf("listener already exists for address %s", address)
	}

	// Create listener
	listener, err := session.Listen(address, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create listener: %w", err)
	}

	// Store listener
	sm.listeners[address] = listener

	// Set up listener cleanup on close
	go sm.cleanupListenerOnClose(address, listener)

	sm.logger.Info("Listener created", "address", address)

	return listener, nil
}

// GetActiveSessionCount returns the number of active sessions
func (sm *SessionManager) GetActiveSessionCount() int {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()

	return len(sm.clientSessions) + len(sm.serverSessions)
}

// CleanupIdleSessions removes idle sessions that have exceeded the timeout
func (sm *SessionManager) CleanupIdleSessions() int {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	if !sm.active {
		return 0
	}

	cleanedUp := 0
	now := time.Now()

	// Check client sessions for idle timeout
	for sessionID, clientSession := range sm.clientSessions {
		if sm.isSessionIdle(clientSession, now) {
			err := clientSession.Close()
			if err != nil {
				sm.logger.Error("Failed to close idle client session", "sessionID", sessionID, "error", err)
			} else {
				delete(sm.clientSessions, sessionID)
				cleanedUp++
				sm.logger.Debug("Cleaned up idle client session", "sessionID", sessionID)
			}
		}
	}

	// Check server sessions for idle timeout
	for sessionID, serverSession := range sm.serverSessions {
		if sm.isSessionIdle(serverSession, now) {
			err := serverSession.Close()
			if err != nil {
				sm.logger.Error("Failed to close idle server session", "sessionID", sessionID, "error", err)
			} else {
				delete(sm.serverSessions, sessionID)
				cleanedUp++
				sm.logger.Debug("Cleaned up idle server session", "sessionID", sessionID)
			}
		}
	}

	return cleanedUp
}

// authenticateClientSession performs authentication for a client session
func (sm *SessionManager) authenticateClientSession(ctx context.Context, clientSession *session.ClientSession) error {
	// For demo purposes, we'll assume authentication is handled by the underlying QUIC connection
	// In a production environment, this would implement proper authentication protocols

	// Check if already authenticated
	if clientSession.IsAuthenticated() {
		return nil
	}

	// Give the session a moment to establish the QUIC connection
	time.Sleep(100 * time.Millisecond)

	// Check if the session has active paths (indicates successful QUIC handshake)
	activePaths := clientSession.GetActivePaths()
	if len(activePaths) > 0 && clientSession.IsAuthenticated() {
		// If we have active paths and authentication is marked, consider it successful
		return nil
	}

	// Wait a bit longer for the connection to establish
	authTimeout := time.NewTimer(5 * time.Second) // Reduced timeout
	defer authTimeout.Stop()

	authTicker := time.NewTicker(100 * time.Millisecond)
	defer authTicker.Stop()

	for {
		select {
		case <-authTimeout.C:
			return fmt.Errorf("authentication timeout")
		case <-authTicker.C:
			// Check if authentication is now complete
			if clientSession.IsAuthenticated() {
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// isSessionIdle checks if a session is idle and should be cleaned up
func (sm *SessionManager) isSessionIdle(sess interface{}, now time.Time) bool {
	// Try to get last activity time from session
	if sessionWithActivity, ok := sess.(interface {
		GetLastActivity() time.Time
	}); ok {
		lastActivity := sessionWithActivity.GetLastActivity()
		return now.Sub(lastActivity) > sm.config.SessionIdleTimeout
	}

	// If we can't determine last activity, don't clean up
	return false
}

// cleanupClientSessionOnClose removes a client session when it closes
func (sm *SessionManager) cleanupClientSessionOnClose(sessionID string, clientSession *session.ClientSession) {
	// Wait for session context to be done (session closed)
	ctx := clientSession.GetContext()
	<-ctx.Done()

	// Remove from active sessions
	sm.mutex.Lock()
	delete(sm.clientSessions, sessionID)
	sm.mutex.Unlock()

	sm.logger.Debug("Client session cleaned up after close", "sessionID", sessionID)
}

// cleanupListenerOnClose removes a listener when it closes
func (sm *SessionManager) cleanupListenerOnClose(address string, listener session.Listener) {
	// This is a placeholder - in a real implementation, we would need a way
	// to detect when the listener closes. For now, we'll rely on explicit cleanup
	// during Stop()
}

// RegisterServerSession registers a server session (called by listeners)
func (sm *SessionManager) RegisterServerSession(serverSession *session.ServerSession) error {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	if !sm.active {
		return fmt.Errorf("session manager is not active")
	}

	// Check session limit
	if len(sm.serverSessions) >= sm.config.MaxSessions {
		return fmt.Errorf("maximum number of server sessions reached")
	}

	// Get session ID
	sessionID := serverSession.GetSessionID()

	// Store session
	sm.serverSessions[sessionID] = serverSession

	// Set up session cleanup on close
	go sm.cleanupServerSessionOnClose(sessionID, serverSession)

	sm.logger.Info("Server session registered", "sessionID", sessionID)

	return nil
}

// cleanupServerSessionOnClose removes a server session when it closes
func (sm *SessionManager) cleanupServerSessionOnClose(sessionID string, serverSession *session.ServerSession) {
	// For now, we'll use a simple approach - check periodically if session is still active
	// In a real implementation, we would need a proper context or notification mechanism
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Check if session is still in the map (simple way to detect if it's been closed)
			sm.mutex.RLock()
			_, exists := sm.serverSessions[sessionID]
			sm.mutex.RUnlock()

			if !exists {
				// Session was already removed, cleanup complete
				return
			}

			// TODO: Add proper session state checking here
			// For now, we rely on explicit cleanup during Stop()
		}
	}
}
