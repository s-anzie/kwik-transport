package filetransfer

import (
	"context"
	"time"
)

// KwikSession represents the interface that KWIK sessions must implement
// This matches the kwik/pkg/session.Session interface
type KwikSession interface {
	OpenStreamSync(ctx context.Context) (KwikStream, error)
	OpenStream() (KwikStream, error)
	AcceptStream(ctx context.Context) (KwikStream, error)
	AddPath(address string) error
	RemovePath(pathID string) error
	GetActivePaths() []KwikPathInfo
	GetDeadPaths() []KwikPathInfo
	GetAllPaths() []KwikPathInfo
	SendRawData(data []byte, pathID string) error
	Close() error
}

// KwikStream represents the interface that KWIK streams must implement
// This matches the kwik/pkg/session.Stream interface
type KwikStream interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Close() error
	StreamID() uint64
	PathID() string
}

// KwikPathInfo represents path information from KWIK sessions
// This matches the kwik/pkg/session.PathInfo structure
type KwikPathInfo struct {
	PathID     string
	Address    string
	IsPrimary  bool
	Status     KwikPathStatus
	CreatedAt  time.Time
	LastActive time.Time
}

// KwikPathStatus represents the status of a KWIK path
// This matches the kwik/pkg/session.PathStatus enum
type KwikPathStatus int

const (
	KwikPathStatusActive KwikPathStatus = iota
	KwikPathStatusDead
	KwikPathStatusConnecting
	KwikPathStatusDisconnecting
)

// KwikSessionAdapter adapts a KWIK session to our FileTransfer Session interface
type KwikSessionAdapter struct {
	kwikSession KwikSession
}

// NewKwikSessionAdapter creates a new adapter for KWIK sessions
func NewKwikSessionAdapter(kwikSession KwikSession) *KwikSessionAdapter {
	return &KwikSessionAdapter{
		kwikSession: kwikSession,
	}
}

// OpenStreamSync opens a new stream synchronously
func (ksa *KwikSessionAdapter) OpenStreamSync(ctx context.Context) (Stream, error) {
	kwikStream, err := ksa.kwikSession.OpenStreamSync(ctx)
	if err != nil {
		return nil, err
	}
	
	return &KwikStreamAdapter{kwikStream: kwikStream}, nil
}

// AcceptStream accepts an incoming stream
func (ksa *KwikSessionAdapter) AcceptStream(ctx context.Context) (Stream, error) {
	kwikStream, err := ksa.kwikSession.AcceptStream(ctx)
	if err != nil {
		return nil, err
	}
	
	return &KwikStreamAdapter{kwikStream: kwikStream}, nil
}

// SendRawData sends raw data on the specified path
func (ksa *KwikSessionAdapter) SendRawData(data []byte, pathID string) error {
	return ksa.kwikSession.SendRawData(data, pathID)
}

// GetActivePaths returns information about active paths
func (ksa *KwikSessionAdapter) GetActivePaths() []PathInfo {
	kwikPaths := ksa.kwikSession.GetActivePaths()
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

// Close closes the KWIK session
func (ksa *KwikSessionAdapter) Close() error {
	return ksa.kwikSession.Close()
}

// KwikStreamAdapter adapts a KWIK stream to our FileTransfer Stream interface
type KwikStreamAdapter struct {
	kwikStream KwikStream
}

// Read reads data from the stream
func (ksa *KwikStreamAdapter) Read(p []byte) (int, error) {
	return ksa.kwikStream.Read(p)
}

// Write writes data to the stream
func (ksa *KwikStreamAdapter) Write(p []byte) (int, error) {
	return ksa.kwikStream.Write(p)
}

// Close closes the stream
func (ksa *KwikStreamAdapter) Close() error {
	return ksa.kwikStream.Close()
}

// StreamID returns the stream ID
func (ksa *KwikStreamAdapter) StreamID() uint64 {
	return ksa.kwikStream.StreamID()
}

// PathID returns the path ID this stream is using
func (ksa *KwikStreamAdapter) PathID() string {
	return ksa.kwikStream.PathID()
}

// CreateFileTransferClientWithKwikSession creates a FileTransferClient using a KWIK session
func CreateFileTransferClientWithKwikSession(kwikSession KwikSession, outputDir string, chunkTimeout time.Duration, maxRetries int) (*KwikFileTransferClient, error) {
	adapter := NewKwikSessionAdapter(kwikSession)
	return NewKwikFileTransferClient(adapter, outputDir, chunkTimeout, maxRetries)
}

// FileTransferClientFactory creates file transfer clients with KWIK sessions
type FileTransferClientFactory struct {
	outputDir    string
	chunkTimeout time.Duration
	maxRetries   int
}

// NewFileTransferClientFactory creates a new factory for file transfer clients
func NewFileTransferClientFactory(outputDir string, chunkTimeout time.Duration, maxRetries int) *FileTransferClientFactory {
	return &FileTransferClientFactory{
		outputDir:    outputDir,
		chunkTimeout: chunkTimeout,
		maxRetries:   maxRetries,
	}
}

// CreateClient creates a new file transfer client with the given KWIK session
func (f *FileTransferClientFactory) CreateClient(kwikSession KwikSession) (FileTransferClient, error) {
	return CreateFileTransferClientWithKwikSession(kwikSession, f.outputDir, f.chunkTimeout, f.maxRetries)
}

// FileTransferClientManager manages multiple file transfer clients
type FileTransferClientManager struct {
	factory *FileTransferClientFactory
	clients map[string]FileTransferClient // clients by server address
}

// NewFileTransferClientManager creates a new client manager
func NewFileTransferClientManager(outputDir string, chunkTimeout time.Duration, maxRetries int) *FileTransferClientManager {
	return &FileTransferClientManager{
		factory: NewFileTransferClientFactory(outputDir, chunkTimeout, maxRetries),
		clients: make(map[string]FileTransferClient),
	}
}

// AddClient adds a client for the given address
func (m *FileTransferClientManager) AddClient(address string, kwikSession KwikSession) (FileTransferClient, error) {
	// Check if client already exists
	if client, exists := m.clients[address]; exists {
		return client, nil
	}
	
	// Create new client
	client, err := m.factory.CreateClient(kwikSession)
	if err != nil {
		return nil, err
	}
	
	// Store client
	m.clients[address] = client
	
	return client, nil
}

// GetClient gets an existing client for the given address
func (m *FileTransferClientManager) GetClient(address string) (FileTransferClient, bool) {
	client, exists := m.clients[address]
	return client, exists
}

// CloseClient closes and removes a client for the given address
func (m *FileTransferClientManager) CloseClient(address string) error {
	client, exists := m.clients[address]
	if !exists {
		return nil // Client doesn't exist, nothing to close
	}
	
	// Close client
	if kwikClient, ok := client.(*KwikFileTransferClient); ok {
		kwikClient.Close()
	}
	
	// Remove from map
	delete(m.clients, address)
	
	return nil
}

// CloseAllClients closes all managed clients
func (m *FileTransferClientManager) CloseAllClients() error {
	for address := range m.clients {
		m.CloseClient(address)
	}
	return nil
}

// GetActiveClients returns a list of addresses for active clients
func (m *FileTransferClientManager) GetActiveClients() []string {
	addresses := make([]string, 0, len(m.clients))
	for address := range m.clients {
		addresses = append(addresses, address)
	}
	return addresses
}