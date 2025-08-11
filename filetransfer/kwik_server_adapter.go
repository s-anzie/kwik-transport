package filetransfer

import (
	"context"
	"fmt"
	"time"
	
	"kwik/pkg/session"
)

// KwikListener represents a KWIK listener interface
// This matches the kwik/pkg/session.Listener interface
type KwikListener interface {
	Accept(ctx context.Context) (KwikServerSession, error)
	Close() error
	Addr() string
}

// KwikServerSession represents the interface that KWIK server sessions must implement
// This matches the kwik/pkg/session.ServerSession interface
type KwikServerSession interface {
	OpenStreamSync(ctx context.Context) (KwikStream, error)
	OpenStream() (KwikStream, error)
	AcceptStream(ctx context.Context) (KwikStream, error)
	AddPath(address string) error
	RemovePath(pathID string) error
	GetActivePaths() []session.PathInfo
	GetDeadPaths() []session.PathInfo
	GetAllPaths() []session.PathInfo
	SendRawData(data []byte, pathID string) error
	Close() error
}

// KwikServerSessionAdapter adapts a KWIK server session to our FileTransfer Session interface
type KwikServerSessionAdapter struct {
	kwikServerSession KwikServerSession
}

// NewKwikServerSessionAdapter creates a new adapter for KWIK server sessions
func NewKwikServerSessionAdapter(kwikServerSession KwikServerSession) *KwikServerSessionAdapter {
	return &KwikServerSessionAdapter{
		kwikServerSession: kwikServerSession,
	}
}

// OpenStreamSync opens a new stream synchronously
func (kssa *KwikServerSessionAdapter) OpenStreamSync(ctx context.Context) (Stream, error) {
	kwikStream, err := kssa.kwikServerSession.OpenStreamSync(ctx)
	if err != nil {
		return nil, err
	}
	
	return &KwikStreamAdapter{kwikStream: kwikStream}, nil
}

// AcceptStream accepts an incoming stream
func (kssa *KwikServerSessionAdapter) AcceptStream(ctx context.Context) (Stream, error) {
	kwikStream, err := kssa.kwikServerSession.AcceptStream(ctx)
	if err != nil {
		return nil, err
	}
	
	return &KwikStreamAdapter{kwikStream: kwikStream}, nil
}

// SendRawData sends raw data on the specified path
func (kssa *KwikServerSessionAdapter) SendRawData(data []byte, pathID string) error {
	return kssa.kwikServerSession.SendRawData(data, pathID)
}

// GetActivePaths returns information about active paths
func (kssa *KwikServerSessionAdapter) GetActivePaths() []PathInfo {
	kwikPaths := kssa.kwikServerSession.GetActivePaths()
	paths := make([]PathInfo, len(kwikPaths))
	
	for i, kwikPath := range kwikPaths {
		paths[i] = PathInfo{
			PathID:     kwikPath.PathID,
			Address:    kwikPath.Address,
			IsPrimary:  kwikPath.IsPrimary,
			Status:     PathStatus(kwikPath.Status),
			CreatedAt:  kwikPath.CreatedAt,
			LastActive: kwikPath.LastActive,
		}
	}
	
	return paths
}

// Close closes the KWIK server session
func (kssa *KwikServerSessionAdapter) Close() error {
	return kssa.kwikServerSession.Close()
}

// CreateFileTransferServerWithKwikSession creates a FileTransferServer using a KWIK server session
func CreateFileTransferServerWithKwikSession(kwikServerSession KwikServerSession, config *FileTransferServerConfig) (*FileTransferServer, error) {
	adapter := NewKwikServerSessionAdapter(kwikServerSession)
	return NewFileTransferServer(adapter, config)
}

// FileTransferServerFactory creates file transfer servers with KWIK server sessions
type FileTransferServerFactory struct {
	config *FileTransferServerConfig
}

// NewFileTransferServerFactory creates a new factory for file transfer servers
func NewFileTransferServerFactory(config *FileTransferServerConfig) *FileTransferServerFactory {
	return &FileTransferServerFactory{
		config: config,
	}
}

// CreateServer creates a new file transfer server with the given KWIK server session
func (f *FileTransferServerFactory) CreateServer(kwikServerSession KwikServerSession) (*FileTransferServer, error) {
	return CreateFileTransferServerWithKwikSession(kwikServerSession, f.config)
}

// FileTransferServerManager manages multiple file transfer servers
type FileTransferServerManager struct {
	factory *FileTransferServerFactory
	servers map[string]*FileTransferServer // servers by session ID
}

// NewFileTransferServerManager creates a new server manager
func NewFileTransferServerManager(config *FileTransferServerConfig) *FileTransferServerManager {
	return &FileTransferServerManager{
		factory: NewFileTransferServerFactory(config),
		servers: make(map[string]*FileTransferServer),
	}
}

// AddServer adds a server for the given session ID
func (m *FileTransferServerManager) AddServer(sessionID string, kwikServerSession KwikServerSession) (*FileTransferServer, error) {
	// Check if server already exists
	if server, exists := m.servers[sessionID]; exists {
		return server, nil
	}
	
	// Create new server
	server, err := m.factory.CreateServer(kwikServerSession)
	if err != nil {
		return nil, err
	}
	
	// Store server
	m.servers[sessionID] = server
	
	return server, nil
}

// GetServer gets an existing server for the given session ID
func (m *FileTransferServerManager) GetServer(sessionID string) (*FileTransferServer, bool) {
	server, exists := m.servers[sessionID]
	return server, exists
}

// CloseServer closes and removes a server for the given session ID
func (m *FileTransferServerManager) CloseServer(sessionID string) error {
	server, exists := m.servers[sessionID]
	if !exists {
		return nil // Server doesn't exist, nothing to close
	}
	
	// Close server
	server.Close()
	
	// Remove from map
	delete(m.servers, sessionID)
	
	return nil
}

// CloseAllServers closes all managed servers
func (m *FileTransferServerManager) CloseAllServers() error {
	for sessionID := range m.servers {
		m.CloseServer(sessionID)
	}
	return nil
}

// GetActiveServers returns a list of session IDs for active servers
func (m *FileTransferServerManager) GetActiveServers() []string {
	sessionIDs := make([]string, 0, len(m.servers))
	for sessionID := range m.servers {
		sessionIDs = append(sessionIDs, sessionID)
	}
	return sessionIDs
}

// FileTransferListener wraps a KWIK listener to automatically create file transfer servers
type FileTransferListener struct {
	kwikListener KwikListener
	serverManager *FileTransferServerManager
	ctx          context.Context
	cancel       context.CancelFunc
}

// NewFileTransferListener creates a new file transfer listener
func NewFileTransferListener(kwikListener KwikListener, config *FileTransferServerConfig) *FileTransferListener {
	ctx, cancel := context.WithCancel(context.Background())
	
	return &FileTransferListener{
		kwikListener:  kwikListener,
		serverManager: NewFileTransferServerManager(config),
		ctx:           ctx,
		cancel:        cancel,
	}
}

// Accept accepts incoming connections and creates file transfer servers
func (ftl *FileTransferListener) Accept(ctx context.Context) (*FileTransferServer, error) {
	// Accept a KWIK session
	kwikSession, err := ftl.kwikListener.Accept(ctx)
	if err != nil {
		return nil, err
	}
	
	// Create a session ID (in a real implementation, this would come from the session)
	sessionID := generateSessionID()
	
	// Create file transfer server for this session
	server, err := ftl.serverManager.AddServer(sessionID, kwikSession)
	if err != nil {
		kwikSession.Close()
		return nil, err
	}
	
	return server, nil
}

// Close closes the file transfer listener
func (ftl *FileTransferListener) Close() error {
	ftl.cancel()
	
	// Close all servers
	ftl.serverManager.CloseAllServers()
	
	// Close underlying listener
	return ftl.kwikListener.Close()
}

// Addr returns the listener's address
func (ftl *FileTransferListener) Addr() string {
	return ftl.kwikListener.Addr()
}

// generateSessionID generates a unique session ID
func generateSessionID() string {
	return fmt.Sprintf("session_%d", time.Now().UnixNano())
}