package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	kwik "kwik/pkg"
	"kwik/pkg/session"
)

type Server struct {
	listener     session.Listener
	sessions     map[string]session.Session
	sessionsMux  sync.RWMutex
	shutdown     chan struct{}
}

func main() {
	fmt.Println("KWIK Server Example")
	fmt.Println("==================")
	fmt.Println()

	// Parse command line arguments
	listenAddr := "localhost:4433"
	if len(os.Args) > 1 {
		listenAddr = os.Args[1]
	}

	// Create server instance
	server, err := NewServer()
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}

	// Start server
	err = server.Start(listenAddr)
	if err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}

	fmt.Printf("Server listening on %s\n", listenAddr)
	fmt.Println("Press Ctrl+C to stop, or type 'help' for interactive commands")
	fmt.Println()

	// Handle graceful shutdown
	go server.handleShutdown()

	// Run interactive mode in a separate goroutine
	go server.runInteractiveMode()

	// Wait for shutdown
	<-server.shutdown
	
	fmt.Println("Shutting down server...")
	server.Stop()
	fmt.Println("Server stopped")
}

func NewServer() (*Server, error) {
	return &Server{
		sessions: make(map[string]session.Session),
		shutdown: make(chan struct{}),
	}, nil
}

func (s *Server) Start(address string) error {
	// Create KWIK listener with server-optimized configuration
	config := kwik.DefaultConfig()
	config.LogLevel = kwik.LogLevelInfo
	config.MetricsEnabled = true
	config.MaxSessions = 100
	config.MaxPathsPerSession = 5

	// Listen directly - KWIK instance is managed automatically
	var err error
	s.listener, err = kwik.Listen(address, config)
	if err != nil {
		return fmt.Errorf("failed to create listener: %w", err)
	}

	// Start accepting connections
	go s.acceptConnections()

	return nil
}

func (s *Server) Stop() {
	// Close listener
	if s.listener != nil {
		s.listener.Close()
	}

	// Close all sessions
	s.sessionsMux.Lock()
	for sessionID, sess := range s.sessions {
		fmt.Printf("Closing session %s\n", sessionID)
		sess.Close()
	}
	s.sessions = make(map[string]session.Session)
	s.sessionsMux.Unlock()

	// KWIK system cleanup is handled automatically by the listener wrapper
}

func (s *Server) acceptConnections() {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		sess, err := s.listener.Accept(ctx)
		cancel()

		if err != nil {
			// Check if we're shutting down
			select {
			case <-s.shutdown:
				return
			default:
				log.Printf("Failed to accept connection: %v", err)
				continue
			}
		}

		// Generate session ID (in real implementation, this would come from the session)
		sessionID := fmt.Sprintf("session-%d", time.Now().UnixNano())
		
		// Store session
		s.sessionsMux.Lock()
		s.sessions[sessionID] = sess
		s.sessionsMux.Unlock()

		fmt.Printf("New client connected: %s\n", sessionID)

		// Handle session in separate goroutine
		go s.handleSession(sessionID, sess)
	}
}

func (s *Server) handleSession(sessionID string, sess session.Session) {
	defer func() {
		// Remove session from map
		s.sessionsMux.Lock()
		delete(s.sessions, sessionID)
		s.sessionsMux.Unlock()
		
		sess.Close()
		fmt.Printf("Session %s closed\n", sessionID)
	}()

	fmt.Printf("Handling session %s\n", sessionID)

	// Accept streams from this session
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		stream, err := sess.AcceptStream(ctx)
		cancel()

		if err != nil {
			if err == io.EOF {
				fmt.Printf("Session %s ended normally\n", sessionID)
				return
			}
			log.Printf("Failed to accept stream from session %s: %v", sessionID, err)
			return
		}

		fmt.Printf("New stream %d from session %s (path %s)\n", 
			stream.StreamID(), sessionID, stream.PathID())

		// Handle stream in separate goroutine
		go s.handleStream(sessionID, stream)
	}
}

func (s *Server) handleStream(sessionID string, stream session.Stream) {
	defer stream.Close()

	// Read data from stream
	buffer := make([]byte, 4096)
	for {
		n, err := stream.Read(buffer)
		if err != nil {
			if err == io.EOF {
				fmt.Printf("Stream %d from session %s closed by client\n", 
					stream.StreamID(), sessionID)
				return
			}
			log.Printf("Error reading from stream %d (session %s): %v", 
				stream.StreamID(), sessionID, err)
			return
		}

		if n > 0 {
			message := string(buffer[:n])
			fmt.Printf("Received from stream %d (session %s): %s\n", 
				stream.StreamID(), sessionID, message)

			// Echo the message back
			response := fmt.Sprintf("Echo: %s", message)
			_, err = stream.Write([]byte(response))
			if err != nil {
				log.Printf("Error writing to stream %d (session %s): %v", 
					stream.StreamID(), sessionID, err)
				return
			}
		}
	}
}

func (s *Server) handleShutdown() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan
	close(s.shutdown)
}

func (s *Server) runInteractiveMode() {
	scanner := bufio.NewScanner(os.Stdin)
	for {
		select {
		case <-s.shutdown:
			return
		default:
			fmt.Print("kwik-server> ")
			if !scanner.Scan() {
				return
			}

			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}

			s.handleCommand(line)
		}
	}
}

func (s *Server) handleCommand(line string) {
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return
	}

	command := parts[0]

	switch command {
	case "help":
		s.showHelp()

	case "sessions":
		s.showSessions()

	case "paths":
		if len(parts) < 2 {
			fmt.Println("Usage: paths <session-id>")
			return
		}
		s.showSessionPaths(parts[1])

	case "addpath":
		if len(parts) < 3 {
			fmt.Println("Usage: addpath <session-id> <server-address>")
			return
		}
		s.addPath(parts[1], parts[2])

	case "removepath":
		if len(parts) < 3 {
			fmt.Println("Usage: removepath <session-id> <path-id>")
			return
		}
		s.removePath(parts[1], parts[2])

	case "sendraw":
		if len(parts) < 4 {
			fmt.Println("Usage: sendraw <session-id> <path-id> <data>")
			return
		}
		data := strings.Join(parts[3:], " ")
		s.sendRawData(parts[1], parts[2], data)

	case "metrics":
		s.showMetrics()

	case "status":
		s.showStatus()

	case "quit", "exit":
		fmt.Println("Shutting down server...")
		close(s.shutdown)

	default:
		fmt.Printf("Unknown command: %s (type 'help' for available commands)\n", command)
	}
}

func (s *Server) showHelp() {
	fmt.Println("\nAvailable commands:")
	fmt.Println("  help                              - Show this help")
	fmt.Println("  sessions                          - List active sessions")
	fmt.Println("  paths <session-id>                - Show paths for a session")
	fmt.Println("  addpath <session-id> <address>    - Add secondary path to session")
	fmt.Println("  removepath <session-id> <path-id> - Remove path from session")
	fmt.Println("  sendraw <session-id> <path-id> <data> - Send raw data via specific path")
	fmt.Println("  metrics                           - Show server metrics")
	fmt.Println("  status                            - Show server status")
	fmt.Println("  quit                              - Shutdown server")
	fmt.Println()
}

func (s *Server) showSessions() {
	s.sessionsMux.RLock()
	defer s.sessionsMux.RUnlock()

	fmt.Printf("\nActive sessions: %d\n", len(s.sessions))
	if len(s.sessions) == 0 {
		fmt.Println("No active sessions")
		return
	}

	for sessionID := range s.sessions {
		fmt.Printf("  - %s\n", sessionID)
	}
	fmt.Println()
}

func (s *Server) showSessionPaths(sessionID string) {
	s.sessionsMux.RLock()
	sess, exists := s.sessions[sessionID]
	s.sessionsMux.RUnlock()

	if !exists {
		fmt.Printf("Session %s not found\n", sessionID)
		return
	}

	fmt.Printf("\nPaths for session %s:\n", sessionID)

	activePaths := sess.GetActivePaths()
	fmt.Printf("Active paths: %d\n", len(activePaths))
	for i, path := range activePaths {
		fmt.Printf("  %d. %s (%s) - Primary: %v, Status: %s\n",
			i+1, path.PathID, path.Address, path.IsPrimary, path.Status)
	}

	deadPaths := sess.GetDeadPaths()
	if len(deadPaths) > 0 {
		fmt.Printf("Dead paths: %d\n", len(deadPaths))
		for i, path := range deadPaths {
			fmt.Printf("  %d. %s (%s) - Last active: %s\n",
				i+1, path.PathID, path.Address, path.LastActive.Format(time.RFC3339))
		}
	}
	fmt.Println()
}

func (s *Server) addPath(sessionID, address string) {
	s.sessionsMux.RLock()
	sess, exists := s.sessions[sessionID]
	s.sessionsMux.RUnlock()

	if !exists {
		fmt.Printf("Session %s not found\n", sessionID)
		return
	}

	fmt.Printf("Adding path %s to session %s...\n", address, sessionID)
	err := sess.AddPath(address)
	if err != nil {
		fmt.Printf("Failed to add path: %v\n", err)
		return
	}

	fmt.Printf("Path addition request sent successfully\n")
}

func (s *Server) removePath(sessionID, pathID string) {
	s.sessionsMux.RLock()
	sess, exists := s.sessions[sessionID]
	s.sessionsMux.RUnlock()

	if !exists {
		fmt.Printf("Session %s not found\n", sessionID)
		return
	}

	fmt.Printf("Removing path %s from session %s...\n", pathID, sessionID)
	err := sess.RemovePath(pathID)
	if err != nil {
		fmt.Printf("Failed to remove path: %v\n", err)
		return
	}

	fmt.Printf("Path removal request sent successfully\n")
}

func (s *Server) sendRawData(sessionID, pathID, data string) {
	s.sessionsMux.RLock()
	sess, exists := s.sessions[sessionID]
	s.sessionsMux.RUnlock()

	if !exists {
		fmt.Printf("Session %s not found\n", sessionID)
		return
	}

	fmt.Printf("Sending raw data to session %s via path %s: %s\n", sessionID, pathID, data)
	err := sess.SendRawData([]byte(data), pathID)
	if err != nil {
		fmt.Printf("Failed to send raw data: %v\n", err)
		return
	}

	fmt.Printf("Raw data sent successfully\n")
}

func (s *Server) showMetrics() {
	fmt.Println("\n--- Server Metrics ---")
	
	// Show KWIK system metrics
	fmt.Println("KWIK system metrics not available (managed internally)")

	// Show session count
	s.sessionsMux.RLock()
	sessionCount := len(s.sessions)
	s.sessionsMux.RUnlock()
	
	fmt.Printf("Active sessions: %d\n", sessionCount)
	
	// Show listener metrics if available
	if s.listener != nil {
		listenerSessionCount := s.listener.GetActiveSessionCount()
		fmt.Printf("Listener active sessions: %d\n", listenerSessionCount)
	}
	
	fmt.Println()
}

func (s *Server) showStatus() {
	fmt.Println("\n--- Server Status ---")
	
	if s.listener != nil {
		fmt.Printf("Listening on: %s\n", s.listener.Addr())
	}
	
	s.sessionsMux.RLock()
	sessionCount := len(s.sessions)
	s.sessionsMux.RUnlock()
	
	fmt.Printf("Active sessions: %d\n", sessionCount)
	
	// Show total path count across all sessions
	totalActivePaths := 0
	totalDeadPaths := 0
	
	s.sessionsMux.RLock()
	for _, sess := range s.sessions {
		totalActivePaths += len(sess.GetActivePaths())
		totalDeadPaths += len(sess.GetDeadPaths())
	}
	s.sessionsMux.RUnlock()
	
	fmt.Printf("Total active paths: %d\n", totalActivePaths)
	fmt.Printf("Total dead paths: %d\n", totalDeadPaths)
	
	fmt.Println()
}