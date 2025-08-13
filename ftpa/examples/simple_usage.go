package main

import (
	"fmt"
	"ftpa/internal/client"
	"ftpa/internal/config"
	"ftpa/internal/server"
	"log"
	"os"
	"path/filepath"
	"time"
)

// This example demonstrates basic usage of the FTPA client and server libraries
func main() {
	fmt.Println("FTPA Simple Usage Example")
	fmt.Println("========================")

	// Example 1: Basic server setup
	fmt.Println("\n1. Setting up a basic server:")
	serverExample()

	// Example 2: Basic client usage
	fmt.Println("\n2. Using the client to download a file:")
	clientExample()

	// Example 3: Multi-path server setup
	fmt.Println("\n3. Setting up multi-path servers:")
	multiPathExample()
}

func serverExample() {
	// Create a basic server configuration
	config := &config.ServerConfiguration{}
	
	// Set basic server settings
	config.Server.Address = "localhost:8080"
	config.Server.FileDirectory = "./data"
	
	// Set performance settings
	config.Performance.ChunkSize = 64 * 1024 // 64KB chunks
	config.Performance.BufferSize = 1024 * 1024 // 1MB buffer
	config.Performance.MaxRetries = 3
	
	// Set limits
	config.Limits.MaxFileSize = 1024 * 1024 * 1024 // 1GB max file size
	config.Limits.MaxConcurrent = 10
	config.Limits.SessionTimeout = "30m"
	
	// Set security settings
	config.Security.RequireAuth = false
	config.Security.DenyTraversal = true
	config.Security.LogLevel = "info"
	
	fmt.Printf("  Server address: %s\n", config.Server.Address)
	fmt.Printf("  File directory: %s\n", config.Server.FileDirectory)
	fmt.Printf("  Chunk size: %d bytes\n", config.Performance.ChunkSize)
	fmt.Printf("  Max file size: %d bytes\n", config.Limits.MaxFileSize)

	// Create server (but don't start it in this example)
	server, err := server.NewFileTransferServer(config)
	if err != nil {
		log.Printf("  Error creating server: %v", err)
		return
	}
	
	fmt.Printf("  ✅ Server created successfully\n")
	
	// In a real application, you would call:
	// server.Start()
	// defer server.Stop()
	
	_ = server // Avoid unused variable warning
}

func clientExample() {
	// Create a basic client configuration
	config := &internal.ClientConfiguration{
		ServerAddress:   "localhost:8080",
		OutputDirectory: "./downloads",
		ChunkTimeout:    30 * time.Second,
		MaxRetries:      3,
		ConnectTimeout:  10 * time.Second,
		TLSInsecure:     false,
	}
	
	fmt.Printf("  Server address: %s\n", config.ServerAddress)
	fmt.Printf("  Output directory: %s\n", config.OutputDirectory)
	fmt.Printf("  Chunk timeout: %v\n", config.ChunkTimeout)
	fmt.Printf("  Max retries: %d\n", config.MaxRetries)

	// Create client
	client, err := internal.NewKwikFileTransferClient(config)
	if err != nil {
		log.Printf("  Error creating client: %v", err)
		return
	}
	defer client.Close()
	
	fmt.Printf("  ✅ Client created successfully\n")

	// In a real application, you would:
	// 1. Connect to the server
	// err = client.Connect()
	// if err != nil { ... }
	// defer client.Disconnect()
	
	// 2. Download a file with progress callback
	// progressCallback := func(progress float64) {
	//     fmt.Printf("    Progress: %.1f%%\n", progress*100)
	// }
	// err = client.DownloadFile("example.txt", progressCallback)
	// if err != nil { ... }
	
	// 3. Monitor download status
	// for {
	//     status, err := client.GetDownloadStatus("example.txt")
	//     if err != nil { break }
	//     if status == internal.DownloadStatusCompleted {
	//         fmt.Println("    ✅ Download completed!")
	//         break
	//     }
	//     time.Sleep(100 * time.Millisecond)
	// }
	
	fmt.Printf("  💡 Use client.Connect(), client.DownloadFile(), and monitor status\n")
}

func multiPathExample() {
	// Primary server configuration
	primaryConfig := &config.ServerConfiguration{}
	primaryConfig.Server.Address = "localhost:8080"
	primaryConfig.Server.SecondaryAddress = "localhost:8081" // Enable multi-path
	primaryConfig.Server.FileDirectory = "./data"
	primaryConfig.Performance.ChunkSize = 64 * 1024
	primaryConfig.Limits.MaxConcurrent = 10
	primaryConfig.Security.LogLevel = "info"
	
	// Secondary server configuration
	secondaryConfig := &config.ServerConfiguration{}
	secondaryConfig.Server.Address = "localhost:8081"
	secondaryConfig.Server.FileDirectory = "./data" // Same files
	secondaryConfig.Performance.ChunkSize = 64 * 1024
	secondaryConfig.Limits.MaxConcurrent = 10
	secondaryConfig.Security.LogLevel = "info"
	
	fmt.Printf("  Primary server: %s\n", primaryConfig.Server.Address)
	fmt.Printf("  Secondary server: %s\n", secondaryConfig.Server.Address)
	fmt.Printf("  Multi-path enabled: %v\n", primaryConfig.Server.SecondaryAddress != "")

	// Create servers (but don't start them in this example)
	primaryServer, err := server.NewFileTransferServer(primaryConfig)
	if err != nil {
		log.Printf("  Error creating primary server: %v", err)
		return
	}
	
	secondaryServer, err := server.NewSecondaryFileTransferServer(secondaryConfig)
	if err != nil {
		log.Printf("  Error creating secondary server: %v", err)
		return
	}
	
	fmt.Printf("  ✅ Primary server created\n")
	fmt.Printf("  ✅ Secondary server created\n")
	
	// In a real application, you would start both servers:
	// go secondaryServer.Start() // Start secondary first
	// time.Sleep(1 * time.Second) // Give it time to start
	// primaryServer.Start() // Then start primary
	// 
	// The primary server will automatically establish secondary paths
	// to the secondary server for improved transfer performance
	
	_ = primaryServer   // Avoid unused variable warnings
	_ = secondaryServer
	
	fmt.Printf("  💡 Start secondary server first, then primary server\n")
	fmt.Printf("  💡 Primary server will automatically establish multi-path connections\n")
}

// Helper function to create example files for testing
func createExampleFiles() error {
	dataDir := "./data"
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return err
	}

	// Create a small text file
	smallFile := filepath.Join(dataDir, "small.txt")
	smallContent := "This is a small example file for testing FTPA transfers.\n"
	if err := os.WriteFile(smallFile, []byte(smallContent), 0644); err != nil {
		return err
	}

	// Create a larger file
	largeFile := filepath.Join(dataDir, "large.txt")
	largeContent := ""
	for i := 0; i < 10000; i++ {
		largeContent += fmt.Sprintf("Line %d: This is a larger file for testing multi-chunk transfers.\n", i)
	}
	if err := os.WriteFile(largeFile, []byte(largeContent), 0644); err != nil {
		return err
	}

	fmt.Printf("Created example files:\n")
	fmt.Printf("  - %s (%d bytes)\n", smallFile, len(smallContent))
	fmt.Printf("  - %s (%d bytes)\n", largeFile, len(largeContent))

	return nil
}

func init() {
	// Create example files when the program starts
	if err := createExampleFiles(); err != nil {
		log.Printf("Warning: Could not create example files: %v", err)
	}
}