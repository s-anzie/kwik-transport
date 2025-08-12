package main

import (
	"fmt"
	"ftpa/internal/config"
	"ftpa/internal/server"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	// Load configuration from file or use defaults
	configPath := ""
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}

	serverConfig, err := config.LoadConfiguration(configPath)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Create file transfer server
	fileServer, err := server.NewFileTransferServer(serverConfig)
	if err != nil {
		log.Fatalf("Failed to create file transfer server: %v", err)
	}

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start the server
	fmt.Printf("Starting KWIK file transfer server...\n")
	if err := fileServer.Start(); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}

	// Wait for shutdown signal
	fmt.Printf("Server is running. Press Ctrl+C to stop.\n")
	<-sigChan

	// Graceful shutdown
	fmt.Printf("\nShutting down server...\n")
	if err := fileServer.Stop(); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}

	fmt.Printf("Server stopped successfully.\n")
}