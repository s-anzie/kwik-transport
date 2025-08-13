package main

import (
	"flag"
	"fmt"
	"ftpa/internal/config"
	"ftpa/internal/server"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	// Define command line flags
	var (
		configPath    = flag.String("config", "", "Path to configuration file")
		address       = flag.String("address", "localhost:8081", "Server address (host:port)")
		fileDir       = flag.String("files", "./data", "Directory containing files to serve")
		chunkSize     = flag.Int("chunk-size", 64*1024, "Chunk size in bytes (default: 64KB)")
		maxConcurrent = flag.Int("max-concurrent", 10, "Maximum concurrent connections")
		maxFileSize   = flag.Int64("max-file-size", 1024*1024*1024, "Maximum file size in bytes (default: 1GB)")
		logLevel      = flag.String("log-level", "info", "Log level (debug, info, warn, error)")
		verbose       = flag.Bool("verbose", false, "Enable verbose output")
		help          = flag.Bool("help", false, "Show help message")
	)

	flag.Parse()

	// Show help if requested
	if *help {
		showUsage()
		os.Exit(0)
	}

	// Load configuration
	var serverConfig *config.ServerConfiguration
	var err error

	if *configPath != "" {
		// Load from file
		serverConfig, err = config.LoadConfiguration(*configPath)
		if err != nil {
			log.Fatalf("Failed to load configuration from file: %v", err)
		}
		fmt.Printf("Configuration loaded from: %s\n", *configPath)
	} else {
		// Use default configuration
		serverConfig = config.DefaultServerConfiguration()
		fmt.Printf("Using default configuration\n")
	}

	// Override configuration with command line flags
	// Note: We always override with command line values since we can't easily detect if they were set
	serverConfig.Server.Address = *address
	serverConfig.Server.FileDirectory = *fileDir
	serverConfig.Performance.ChunkSize = int32(*chunkSize)
	serverConfig.Limits.MaxConcurrent = *maxConcurrent
	serverConfig.Limits.MaxFileSize = *maxFileSize
	serverConfig.Security.LogLevel = *logLevel

	// Ensure file directory exists
	if err := ensureDirectoryExists(serverConfig.Server.FileDirectory); err != nil {
		log.Fatalf("Failed to create file directory: %v", err)
	}

	// Validate final configuration
	if err := serverConfig.Validate(); err != nil {
		log.Fatalf("Invalid configuration: %v", err)
	}

	// Show configuration summary
	if *verbose {
		showConfigSummary(serverConfig)
	}

	// Create secondary file transfer server
	secondaryServer, err := server.NewSecondaryFileTransferServer(serverConfig)
	if err != nil {
		log.Fatalf("Failed to create secondary server: %v", err)
	}

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start the server
	fmt.Printf("🚀 Starting KWIK secondary file transfer server...\n")
	fmt.Printf("📍 Address: %s\n", serverConfig.Server.Address)
	fmt.Printf("📁 File directory: %s\n", serverConfig.Server.FileDirectory)
	fmt.Printf("📦 Chunk size: %d bytes\n", serverConfig.Performance.ChunkSize)
	fmt.Printf("🔗 Max concurrent: %d\n", serverConfig.Limits.MaxConcurrent)
	
	if err := secondaryServer.Start(); err != nil {
		log.Fatalf("Failed to start secondary server: %v", err)
	}

	// Show startup success
	fmt.Printf("✅ Secondary server is running and ready to handle chunk requests\n")
	fmt.Printf("🛤️  Waiting for secondary path connections from primary servers...\n")
	
	if *verbose {
		fmt.Printf("💡 This server will:\n")
		fmt.Printf("   - Accept secondary path connections from primary servers\n")
		fmt.Printf("   - Process chunk commands received via control plane\n")
		fmt.Printf("   - Send file chunks to clients via data plane\n")
		fmt.Printf("   - Handle raw packet transmissions between servers\n")
	}

	// Monitor server status
	go monitorServerStatus(secondaryServer, *verbose)

	// Wait for shutdown signal
	fmt.Printf("⏹️  Press Ctrl+C to stop the server\n")
	<-sigChan

	// Graceful shutdown
	fmt.Printf("\n🛑 Shutting down secondary server...\n")
	shutdownStart := time.Now()
	
	if err := secondaryServer.Stop(); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}

	shutdownDuration := time.Since(shutdownStart)
	fmt.Printf("✅ Secondary server stopped successfully in %v\n", shutdownDuration.Round(time.Millisecond))
}

func showUsage() {
	fmt.Printf(`FTPA Secondary Server - Fast Transfer Protocol Application Secondary Server

Usage: %s [options]

Options:
  -config <file>        Path to configuration file (YAML)
  -address <addr>       Server address (default: localhost:8081)
  -files <directory>    Directory containing files to serve (default: ./data)
  -chunk-size <bytes>   Chunk size in bytes (default: 65536)
  -max-concurrent <n>   Maximum concurrent connections (default: 10)
  -max-file-size <bytes> Maximum file size in bytes (default: 1GB)
  -log-level <level>    Log level: debug, info, warn, error (default: info)
  -verbose              Enable verbose output
  -help                 Show this help message

Configuration File Example (YAML):
  server:
    address: "localhost:8081"
    file_directory: "./data"
  
  limits:
    max_file_size: 1073741824  # 1GB
    max_concurrent: 10
    allowed_extensions: [".txt", ".pdf", ".zip", ".tar.gz"]
  
  performance:
    chunk_size: 65536  # 64KB
    buffer_size: 1048576  # 1MB
    max_retries: 3
  
  security:
    require_auth: true
    deny_traversal: true
    log_level: "info"

Environment Variables:
  KWIK_SERVER_ADDRESS      Server address
  KWIK_FILE_DIRECTORY      File directory path
  KWIK_CHUNK_SIZE          Chunk size in bytes
  KWIK_MAX_CONCURRENT      Maximum concurrent connections
  KWIK_MAX_FILE_SIZE       Maximum file size in bytes
  KWIK_LOG_LEVEL           Log level

The secondary server works in conjunction with primary servers to provide
multi-path file transfer capabilities using the KWIK protocol. It receives
chunk commands from primary servers and sends file chunks directly to clients.

Examples:
  # Start with default settings
  %s

  # Start with custom address and file directory
  %s -address 0.0.0.0:8081 -files /var/ftpa/data

  # Start with configuration file
  %s -config /etc/ftpa/secondary.yaml

  # Start with verbose output and custom chunk size
  %s -verbose -chunk-size 131072 -files ./shared-files
`, os.Args[0], os.Args[0], os.Args[0], os.Args[0], os.Args[0])
}

func showConfigSummary(config *config.ServerConfiguration) {
	fmt.Printf("\n📋 Configuration Summary:\n")
	fmt.Printf("   Server Address: %s\n", config.Server.Address)
	fmt.Printf("   File Directory: %s\n", config.Server.FileDirectory)
	fmt.Printf("   Chunk Size: %d bytes (%.1f KB)\n", config.Performance.ChunkSize, float64(config.Performance.ChunkSize)/1024)
	fmt.Printf("   Max File Size: %d bytes (%.1f MB)\n", config.Limits.MaxFileSize, float64(config.Limits.MaxFileSize)/(1024*1024))
	fmt.Printf("   Max Concurrent: %d\n", config.Limits.MaxConcurrent)
	fmt.Printf("   Buffer Size: %d bytes (%.1f KB)\n", config.Performance.BufferSize, float64(config.Performance.BufferSize)/1024)
	fmt.Printf("   Max Retries: %d\n", config.Performance.MaxRetries)
	fmt.Printf("   Log Level: %s\n", config.Security.LogLevel)
	
	if len(config.Limits.AllowedExtensions) > 0 {
		fmt.Printf("   Allowed Extensions: %v\n", config.Limits.AllowedExtensions)
	} else {
		fmt.Printf("   Allowed Extensions: All files\n")
	}
	
	fmt.Printf("   Security: Auth=%v, DenyTraversal=%v\n", 
		config.Security.RequireAuth, config.Security.DenyTraversal)
	fmt.Println()
}

func ensureDirectoryExists(dirPath string) error {
	// Check if directory exists
	if info, err := os.Stat(dirPath); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("path exists but is not a directory: %s", dirPath)
		}
		return nil
	}

	// Create directory with proper permissions
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dirPath, err)
	}

	fmt.Printf("📁 Created file directory: %s\n", dirPath)
	return nil
}

func monitorServerStatus(secondaryServer *server.SecondaryFileTransferServer, verbose bool) {
	if !verbose {
		return
	}

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if !secondaryServer.IsRunning() {
				return
			}

			// Show server status
			activeCommands := secondaryServer.GetActiveCommands()
			fmt.Printf("📊 Status: %d active commands", len(activeCommands))
			
			if secondaryServer.IsRawPacketHandlerRunning() {
				fmt.Printf(", raw packet handler running")
			}
			
			fmt.Printf("\n")

			// Show active commands details if any
			if len(activeCommands) > 0 && verbose {
				fmt.Printf("   Active commands:\n")
				for commandID, commandState := range activeCommands {
					elapsed := time.Since(commandState.ReceivedAt)
					fmt.Printf("   - %s: %s (%.1fs ago)\n", 
						commandID, commandState.Status.String(), elapsed.Seconds())
				}
			}
		}
	}
}