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

	// Adjust address for secondary server (typically different port)
	if serverConfig.Server.SecondaryAddress != "" {
		serverConfig.Server.Address = serverConfig.Server.SecondaryAddress
	} else {
		// Default secondary server address
		serverConfig.Server.Address = "localhost:8081"
	}

	// Create secondary file transfer server
	secondaryServer, err := server.NewSecondaryFileTransferServer(serverConfig)
	if err != nil {
		log.Fatalf("Failed to create secondary file transfer server: %v", err)
	}

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start the secondary server
	fmt.Printf("Starting KWIK secondary file transfer server...\n")
	if err := secondaryServer.Start(); err != nil {
		log.Fatalf("Failed to start secondary server: %v", err)
	}

	// Wait for shutdown signal
	fmt.Printf("Secondary server is running. Press Ctrl+C to stop.\n")
	<-sigChan

	// Graceful shutdown
	fmt.Printf("\nShutting down secondary server...\n")
	if err := secondaryServer.Stop(); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}

	fmt.Printf("Secondary server stopped successfully.\n")
}