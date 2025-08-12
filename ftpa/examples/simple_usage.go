package main

import (
	"fmt"
	"ftpa/internal/config"
	"ftpa/internal/server"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

func main() {
	fmt.Println("🚀 FTPA - Simple Usage Example")
	fmt.Println("This example shows how to use the new simplified server architecture")
	fmt.Println()

	// Create configuration for primary server
	primaryConfig := config.DefaultServerConfiguration()
	primaryConfig.Server.Address = "localhost:8080"
	primaryConfig.Server.FileDirectory = "./data"
	primaryConfig.Server.SecondaryAddress = "localhost:8081"
	primaryConfig.Performance.SecondaryPaths = []string{"secondary1"}
	
	// Create configuration for secondary server
	secondaryConfig := config.DefaultServerConfiguration()
	secondaryConfig.Server.Address = "localhost:8081"
	secondaryConfig.Server.FileDirectory = "./data" // Same directory for this example
	
	// Create both servers
	fmt.Println("📦 Creating file transfer servers...")
	
	primaryServer, err := server.NewFileTransferServer(primaryConfig)
	if err != nil {
		log.Fatalf("Failed to create primary server: %v", err)
	}
	
	secondaryServer, err := server.NewSecondaryFileTransferServer(secondaryConfig)
	if err != nil {
		log.Fatalf("Failed to create secondary server: %v", err)
	}
	
	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	
	// Start both servers concurrently
	var wg sync.WaitGroup
	
	// Start secondary server first
	wg.Add(1)
	go func() {
		defer wg.Done()
		fmt.Println("🔧 Starting secondary file transfer server...")
		if err := secondaryServer.Start(); err != nil {
			log.Printf("Failed to start secondary server: %v", err)
			return
		}
		fmt.Println("✅ Secondary server started successfully")
	}()
	
	// Wait a moment for secondary server to start
	time.Sleep(1 * time.Second)
	
	// Start primary server
	wg.Add(1)
	go func() {
		defer wg.Done()
		fmt.Println("🔧 Starting primary file transfer server...")
		if err := primaryServer.Start(); err != nil {
			log.Printf("Failed to start primary server: %v", err)
			return
		}
		fmt.Println("✅ Primary server started successfully")
	}()
	
	// Wait for both servers to start
	time.Sleep(2 * time.Second)
	
	// Show server status
	fmt.Println()
	fmt.Println("📊 Server Status:")
	fmt.Printf("   Primary Server:   %s (Running: %v)\n", 
		primaryConfig.Server.Address, primaryServer.IsRunning())
	fmt.Printf("   Secondary Server: %s (Running: %v)\n", 
		secondaryConfig.Server.Address, secondaryServer.IsRunning())
	fmt.Println()
	
	// Show usage instructions
	fmt.Println("💡 Usage Instructions:")
	fmt.Println("   1. Place files in the './data' directory")
	fmt.Println("   2. Use a KWIK client to connect to localhost:8080")
	fmt.Println("   3. Request files - they will be served using multi-path")
	fmt.Println("   4. The primary server will coordinate with the secondary server")
	fmt.Println("   5. Press Ctrl+C to stop both servers")
	fmt.Println()
	
	// Wait for shutdown signal
	fmt.Println("🏃 Servers are running. Press Ctrl+C to stop.")
	<-sigChan
	
	// Graceful shutdown
	fmt.Println("\n🛑 Shutting down servers...")
	
	// Stop primary server first
	if err := primaryServer.Stop(); err != nil {
		log.Printf("Error stopping primary server: %v", err)
	} else {
		fmt.Println("✅ Primary server stopped")
	}
	
	// Stop secondary server
	if err := secondaryServer.Stop(); err != nil {
		log.Printf("Error stopping secondary server: %v", err)
	} else {
		fmt.Println("✅ Secondary server stopped")
	}
	
	// Wait for goroutines to finish
	wg.Wait()
	
	fmt.Println("🎉 All servers stopped successfully!")
}