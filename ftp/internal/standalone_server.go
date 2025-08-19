package filetransfer

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	kwik "kwik/pkg"
	"kwik/pkg/session"
)

// StandaloneFileTransferServer is a standalone file transfer server with its own KWIK session
type StandaloneFileTransferServer struct {
	address       string
	listener      session.Listener
	fileDirectory string
	config        *FileTransferServerConfig
	activeClients map[string]*ClientHandler
	clientsMutex  sync.RWMutex
	ctx           context.Context
	cancel        context.CancelFunc
	isRunning     bool
	runningMutex  sync.RWMutex
}

// ClientHandler handles a single client connection
type ClientHandler struct {
	clientID    string
	session     session.Session
	fileServer  *FileTransferServer
	coordinator *ChunkCoordinator
	ctx         context.Context
	cancel      context.CancelFunc
}

// NewStandaloneFileTransferServer creates a new standalone file transfer server
func NewStandaloneFileTransferServer(address string, config *FileTransferServerConfig) (*StandaloneFileTransferServer, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	ctx, cancel := context.WithCancel(context.Background())

	server := &StandaloneFileTransferServer{
		address:       address,
		fileDirectory: config.FileDirectory,
		config:        config,
		activeClients: make(map[string]*ClientHandler),
		ctx:           ctx,
		cancel:        cancel,
		isRunning:     false,
	}

	return server, nil
}

// Start starts the standalone file transfer server
func (s *StandaloneFileTransferServer) Start() error {
	s.runningMutex.Lock()
	defer s.runningMutex.Unlock()

	if s.isRunning {
		return fmt.Errorf("server is already running")
	}

	// Create KWIK listener
	listener, err := kwik.Listen(s.address, nil)
	if err != nil {
		return fmt.Errorf("failed to create KWIK listener: %w", err)
	}

	s.listener = listener
	s.isRunning = true

	fmt.Printf("🚀 Standalone File Transfer Server started on %s\n", s.address)
	fmt.Printf("📁 Serving files from: %s\n", s.fileDirectory)

	// Start accepting connections
	go s.acceptConnections()

	return nil
}

// Stop stops the standalone file transfer server
func (s *StandaloneFileTransferServer) Stop() error {
	s.runningMutex.Lock()
	defer s.runningMutex.Unlock()

	if !s.isRunning {
		return nil
	}

	fmt.Printf("🛑 Stopping Standalone File Transfer Server...\n")

	// Cancel context to stop all operations
	s.cancel()

	// Close all active clients
	s.clientsMutex.Lock()
	for clientID, handler := range s.activeClients {
		fmt.Printf("🔌 Closing client %s\n", clientID)
		handler.cleanup()
	}
	s.activeClients = make(map[string]*ClientHandler)
	s.clientsMutex.Unlock()

	// Close listener
	if s.listener != nil {
		s.listener.Close()
	}

	s.isRunning = false
	fmt.Printf("✅ Standalone File Transfer Server stopped\n")

	return nil
}

// IsRunning returns whether the server is currently running
func (s *StandaloneFileTransferServer) IsRunning() bool {
	s.runningMutex.RLock()
	defer s.runningMutex.RUnlock()
	return s.isRunning
}

// GetActiveClients returns the number of active clients
func (s *StandaloneFileTransferServer) GetActiveClients() int {
	s.clientsMutex.RLock()
	defer s.clientsMutex.RUnlock()
	return len(s.activeClients)
}

// acceptConnections accepts incoming client connections
func (s *StandaloneFileTransferServer) acceptConnections() {
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
			// Accept incoming connection
			clientSession, err := s.listener.Accept(s.ctx)
			if err != nil {
				if s.ctx.Err() != nil {
					return // Context cancelled, shutting down
				}
				log.Printf("❌ Failed to accept connection: %v", err)
				continue
			}

			// Generate client ID
			clientID := fmt.Sprintf("client_%d", time.Now().UnixNano())
			fmt.Printf("🔗 New client connected: %s\n", clientID)

			// Handle client in goroutine
			go s.handleClient(clientID, clientSession)
		}
	}
}

// handleClient handles a single client connection
func (s *StandaloneFileTransferServer) handleClient(clientID string, clientSession session.Session) {
	defer clientSession.Close()

	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()

	fmt.Printf("👤 [%s] Client session started with %d paths\n", clientID, len(clientSession.GetActivePaths()))

	// Show initial paths
	s.showPaths(clientSession, clientID, "Initial connection")

	// Setup multi-path if secondary address is configured
	if s.config.SecondaryAddress != "" {
		s.setupMultiPath(clientSession, clientID)
	}

	// Create chunk coordinator
	coordinator, err := NewChunkCoordinator(
		clientSession,
		s.config.SecondaryPaths,
		s.fileDirectory,
		s.config.ChunkSize,
		s.config.MaxConcurrent,
	)
	if err != nil {
		log.Printf("❌ [%s] Failed to create chunk coordinator: %v", clientID, err)
		return
	}
	defer coordinator.Close()

	// Create file transfer server for this client
	fileServer, err := NewFileTransferServer(clientSession, s.config)
	if err != nil {
		log.Printf("❌ [%s] Failed to create file transfer server: %v", clientID, err)
		return
	}
	defer fileServer.Close()

	// Create client handler
	handler := &ClientHandler{
		clientID:    clientID,
		session:     clientSession,
		fileServer:  fileServer,
		coordinator: coordinator,
		ctx:         ctx,
		cancel:      cancel,
	}

	// Add to active clients
	s.clientsMutex.Lock()
	s.activeClients[clientID] = handler
	s.clientsMutex.Unlock()

	// Remove from active clients when done
	defer func() {
		s.clientsMutex.Lock()
		delete(s.activeClients, clientID)
		s.clientsMutex.Unlock()
		fmt.Printf("🔌 [%s] Client disconnected\n", clientID)
	}()

	fmt.Printf("✅ [%s] File transfer server ready for requests\n", clientID)

	// Keep session alive and monitor
	s.monitorClient(handler)
}

// setupMultiPath sets up multi-path connection for a client
func (s *StandaloneFileTransferServer) setupMultiPath(clientSession session.Session, clientID string) {
	if s.config.SecondaryAddress == "" {
		return
	}

	fmt.Printf("🛤️  [%s] Setting up secondary path to %s...\n", clientID, s.config.SecondaryAddress)

	// Give the client a moment to set up its control frame handler
	time.Sleep(500 * time.Millisecond)

	err := clientSession.AddPath(s.config.SecondaryAddress)
	if err != nil {
		log.Printf("❌ [%s] Failed to add secondary path: %v", clientID, err)
		return
	}

	fmt.Printf("✅ [%s] AddPath request sent successfully\n", clientID)

	// Wait for secondary path to be established
	time.Sleep(2 * time.Second)
	s.showPaths(clientSession, clientID, "After secondary path addition")

	// Update config with path ID if available
	if serverSession, ok := clientSession.(*session.ServerSession); ok {
		pathID := serverSession.GetPendingPathID(s.config.SecondaryAddress)
		if pathID != "" {
			s.config.SecondaryPaths = []string{pathID}
			fmt.Printf("✅ [%s] Secondary path ID obtained: %s\n", clientID, pathID)
		}
	}
}

// monitorClient monitors a client connection
func (s *StandaloneFileTransferServer) monitorClient(handler *ClientHandler) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-handler.ctx.Done():
			return
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			// Check if session is still active
			paths := handler.session.GetActivePaths()
			if len(paths) == 0 {
				fmt.Printf("🔌 [%s] No active paths, closing session\n", handler.clientID)
				return
			}

			fmt.Printf("💓 [%s] Session heartbeat - %d active paths\n", handler.clientID, len(paths))

			// Show active requests
			if handler.fileServer != nil {
				requests := handler.fileServer.GetActiveRequests()
				if len(requests) > 0 {
					fmt.Printf("📊 [%s] Active file requests: %d\n", handler.clientID, len(requests))
					for requestKey, request := range requests {
						elapsed := time.Since(request.RequestedAt)
						fmt.Printf("   🔄 %s: %s (elapsed: %v)\n",
							requestKey, request.Status, elapsed.Round(time.Second))
					}
				}
			}
		case <-time.After(10 * time.Minute):
			// Timeout after 10 minutes of inactivity
			fmt.Printf("⏰ [%s] Session timeout, closing\n", handler.clientID)
			return
		}
	}
}

// showPaths displays information about active connection paths
func (s *StandaloneFileTransferServer) showPaths(sess session.Session, clientID, label string) {
	paths := sess.GetActivePaths()
	fmt.Printf("🛤️  [%s] %s paths (%d total):\n", clientID, label, len(paths))
	for i, path := range paths {
		fmt.Printf("   %d. %s (Primary: %v, Status: %s)\n",
			i+1, path.Address, path.IsPrimary, path.Status)
	}
	fmt.Println()
}

// cleanup cleans up client handler resources
func (h *ClientHandler) cleanup() {
	h.cancel()
	if h.fileServer != nil {
		h.fileServer.Close()
	}
	if h.coordinator != nil {
		h.coordinator.Close()
	}
}
