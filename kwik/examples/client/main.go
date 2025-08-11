package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	kwik "kwik/pkg"
	"kwik/pkg/session"
)

func main() {
	fmt.Println("KWIK Client Example")
	fmt.Println("==================")
	fmt.Println()

	// Parse command line arguments
	serverAddr := "localhost:4433"
	if len(os.Args) > 1 {
		serverAddr = os.Args[1]
	}

	fmt.Printf("Connecting to server at %s...\n", serverAddr)

	// Create client session (QUIC-compatible interface)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Optional: customize configuration
	config := kwik.DefaultConfig()
	config.LogLevel = kwik.LogLevelInfo
	config.MetricsEnabled = true

	// Dial directly - KWIK instance is managed automatically
	session, err := kwik.Dial(ctx, serverAddr, config)
	if err != nil {
		log.Fatalf("Failed to dial server: %v", err)
	}
	defer session.Close()

	fmt.Println("Connected successfully!")
	fmt.Println()

	// Demonstrate basic stream operations
	err = demonstrateBasicStreams(session)
	if err != nil {
		log.Printf("Basic stream demo failed: %v", err)
	}

	// Demonstrate multi-path capabilities (server-controlled)
	err = demonstrateMultiPath(session)
	if err != nil {
		log.Printf("Multi-path demo failed: %v", err)
	}

	// Interactive mode
	runInteractiveMode(session)
}

// demonstrateBasicStreams shows basic QUIC-compatible stream usage
func demonstrateBasicStreams(sess session.Session) error {
	fmt.Println("=== Basic Stream Operations ===")

	// Open a stream (QUIC-compatible)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := sess.OpenStreamSync(ctx)
	if err != nil {
		return fmt.Errorf("failed to open stream: %w", err)
	}
	defer stream.Close()

	fmt.Printf("Opened stream with ID: %d\n", stream.StreamID())

	// Send data
	message := "Hello from KWIK client!"
	_, err = stream.Write([]byte(message))
	if err != nil {
		return fmt.Errorf("failed to write to stream: %w", err)
	}
	fmt.Printf("Sent: %s\n", message)

	// Read response
	buffer := make([]byte, 1024)
	n, err := stream.Read(buffer)
	if err != nil && err != io.EOF {
		return fmt.Errorf("failed to read from stream: %w", err)
	}

	if n > 0 {
		fmt.Printf("Received: %s\n", string(buffer[:n]))
	}

	fmt.Println("Basic stream operations completed successfully!")
	fmt.Println()
	return nil
}

// demonstrateMultiPath shows multi-path capabilities
func demonstrateMultiPath(sess session.Session) error {
	fmt.Println("=== Multi-Path Capabilities ===")

	// Display current paths
	activePaths := sess.GetActivePaths()
	fmt.Printf("Active paths: %d\n", len(activePaths))
	for _, path := range activePaths {
		fmt.Printf("  - Path %s: %s (Primary: %v, Status: %s)\n",
			path.PathID, path.Address, path.IsPrimary, path.Status)
	}

	allPaths := sess.GetAllPaths()
	fmt.Printf("Total paths in session history: %d\n", len(allPaths))

	deadPaths := sess.GetDeadPaths()
	if len(deadPaths) > 0 {
		fmt.Printf("Dead paths: %d\n", len(deadPaths))
		for _, path := range deadPaths {
			fmt.Printf("  - Dead path %s: %s (Last active: %s)\n",
				path.PathID, path.Address, path.LastActive.Format(time.RFC3339))
		}
	}

	fmt.Println("Multi-path information displayed successfully!")
	fmt.Println()
	return nil
}

// runInteractiveMode provides an interactive client interface
func runInteractiveMode(sess session.Session) {
	fmt.Println("=== Interactive Mode ===")
	fmt.Println("Commands:")
	fmt.Println("  send <message>  - Send message on a new stream")
	fmt.Println("  paths           - Show path information")
	fmt.Println("  metrics         - Show session metrics (if available)")
	fmt.Println("  help            - Show this help")
	fmt.Println("  quit            - Exit")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("kwik> ")
		if !scanner.Scan() {
			break
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, " ", 2)
		command := parts[0]

		switch command {
		case "send":
			if len(parts) < 2 {
				fmt.Println("Usage: send <message>")
				continue
			}
			err := sendMessage(sess, parts[1])
			if err != nil {
				fmt.Printf("Error sending message: %v\n", err)
			}

		case "paths":
			showPathInfo(sess)

		case "metrics":
			showMetrics(sess)

		case "help":
			fmt.Println("Commands:")
			fmt.Println("  send <message>  - Send message on a new stream")
			fmt.Println("  paths           - Show path information")
			fmt.Println("  metrics         - Show session metrics (if available)")
			fmt.Println("  help            - Show this help")
			fmt.Println("  quit            - Exit")

		case "quit", "exit":
			fmt.Println("Goodbye!")
			return

		default:
			fmt.Printf("Unknown command: %s (type 'help' for available commands)\n", command)
		}
	}
}

// sendMessage sends a message on a new stream
func sendMessage(sess session.Session, message string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := sess.OpenStreamSync(ctx)
	if err != nil {
		return fmt.Errorf("failed to open stream: %w", err)
	}
	defer stream.Close()

	_, err = stream.Write([]byte(message))
	if err != nil {
		return fmt.Errorf("failed to write message: %w", err)
	}

	fmt.Printf("Message sent on stream %d (path %s)\n", stream.StreamID(), stream.PathID())

	// Try to read response
	buffer := make([]byte, 1024)
	// Note: SetReadDeadline may not be available in all implementations
	// stream.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := stream.Read(buffer)
	if err != nil && err != io.EOF {
		fmt.Printf("No response received (this is normal): %v\n", err)
		return nil
	}

	if n > 0 {
		fmt.Printf("Response: %s\n", string(buffer[:n]))
	}

	return nil
}

// showPathInfo displays current path information
func showPathInfo(sess session.Session) {
	fmt.Println("\n--- Path Information ---")

	activePaths := sess.GetActivePaths()
	fmt.Printf("Active paths: %d\n", len(activePaths))
	for i, path := range activePaths {
		fmt.Printf("  %d. Path %s\n", i+1, path.PathID)
		fmt.Printf("     Address: %s\n", path.Address)
		fmt.Printf("     Primary: %v\n", path.IsPrimary)
		fmt.Printf("     Status: %s\n", path.Status)
		fmt.Printf("     Created: %s\n", path.CreatedAt.Format(time.RFC3339))
		fmt.Printf("     Last Active: %s\n", path.LastActive.Format(time.RFC3339))
		fmt.Println()
	}

	deadPaths := sess.GetDeadPaths()
	if len(deadPaths) > 0 {
		fmt.Printf("Dead paths: %d\n", len(deadPaths))
		for i, path := range deadPaths {
			fmt.Printf("  %d. Path %s (%s) - Last active: %s\n",
				i+1, path.PathID, path.Address, path.LastActive.Format(time.RFC3339))
		}
		fmt.Println()
	}

	allPaths := sess.GetAllPaths()
	fmt.Printf("Total paths in session history: %d\n", len(allPaths))
	fmt.Println()
}

// showMetrics displays session metrics if available
func showMetrics(sess session.Session) {
	fmt.Println("\n--- Session Metrics ---")
	
	// Try to get metrics from session if it supports it
	if sessionWithMetrics, ok := sess.(interface {
		GetMetrics() interface{}
	}); ok {
		metrics := sessionWithMetrics.GetMetrics()
		fmt.Printf("Session metrics: %+v\n", metrics)
	} else {
		fmt.Println("Session metrics not available")
	}
	
	// Show basic path statistics
	activePaths := sess.GetActivePaths()
	deadPaths := sess.GetDeadPaths()
	allPaths := sess.GetAllPaths()
	
	fmt.Printf("Path Statistics:\n")
	fmt.Printf("  Active paths: %d\n", len(activePaths))
	fmt.Printf("  Dead paths: %d\n", len(deadPaths))
	fmt.Printf("  Total paths: %d\n", len(allPaths))
	
	if len(activePaths) > 0 {
		primaryCount := 0
		for _, path := range activePaths {
			if path.IsPrimary {
				primaryCount++
			}
		}
		fmt.Printf("  Primary paths: %d\n", primaryCount)
	}
	
	fmt.Println()
}