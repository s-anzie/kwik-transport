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
		address       = flag.String("address", "localhost:4433", "Primary server address (host:port)")
		secondaryAddr = flag.String("secondary", "", "Secondary server address for multi-path (host:port)")
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
	if *secondaryAddr != "" {
		serverConfig.Server.SecondaryAddress = *secondaryAddr
	}
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

	// Create file transfer server
	fileServer, err := server.NewFileTransferServer(serverConfig)
	if err != nil {
		log.Fatalf("Failed to create file transfer server: %v", err)
	}

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start the server
	fmt.Printf("🚀 Starting KWIK primary file transfer server...\n")
	fmt.Printf("📍 Primary address: %s\n", serverConfig.Server.Address)
	if serverConfig.Server.SecondaryAddress != "" {
		fmt.Printf("🛤️  Secondary address: %s\n", serverConfig.Server.SecondaryAddress)
	}
	fmt.Printf("📁 File directory: %s\n", serverConfig.Server.FileDirectory)
	fmt.Printf("📦 Chunk size: %d bytes\n", serverConfig.Performance.ChunkSize)
	fmt.Printf("🔗 Max concurrent: %d\n", serverConfig.Limits.MaxConcurrent)

	if err := fileServer.Start(); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}

	// Show startup success
	fmt.Printf("✅ Primary server is running and accepting client connections\n")

	if serverConfig.Server.SecondaryAddress != "" {
		fmt.Printf("🔗 Multi-path mode enabled - will establish secondary paths to %s\n", serverConfig.Server.SecondaryAddress)
	} else {
		fmt.Printf("📡 Single-path mode - no secondary server configured\n")
	}

	if *verbose {
		fmt.Printf("💡 Server capabilities:\n")
		fmt.Printf("   - Accept client connections and file requests\n")
		fmt.Printf("   - Coordinate chunk distribution across multiple paths\n")
		fmt.Printf("   - Establish secondary paths for improved performance\n")
		fmt.Printf("   - Handle chunk retry requests from clients\n")
	}

	// Monitor server status
	go monitorServerStatus(fileServer, *verbose)

	// Wait for shutdown signal
	fmt.Printf("⏹️  Press Ctrl+C to stop the server\n")
	<-sigChan

	// Graceful shutdown
	fmt.Printf("\n🛑 Shutting down primary server...\n")
	shutdownStart := time.Now()

	if err := fileServer.Stop(); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}

	shutdownDuration := time.Since(shutdownStart)
	fmt.Printf("✅ Primary server stopped successfully in %v\n", shutdownDuration.Round(time.Millisecond))
}

func showUsage() {
	fmt.Printf(`FTPA Primary Server - Fast Transfer Protocol Application Primary Server

Usage: %s [options]

Options:
  -config <file>        Path to configuration file (YAML)
  -address <addr>       Primary server address (default: localhost:4433)
  -secondary <addr>     Secondary server address for multi-path support
  -files <directory>    Directory containing files to serve (default: ./data)
  -chunk-size <bytes>   Chunk size in bytes (default: 65536)
  -max-concurrent <n>   Maximum concurrent connections (default: 10)
  -max-file-size <bytes> Maximum file size in bytes (default: 1GB)
  -log-level <level>    Log level: debug, info, warn, error (default: info)
  -verbose              Enable verbose output
  -help                 Show this help message

Configuration File Example (YAML):
  server:
    address: "localhost:8080"
    secondary_address: "localhost:8081"  # Optional for multi-path
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
  KWIK_SERVER_ADDRESS      Primary server address
  KWIK_SECONDARY_ADDRESS   Secondary server address
  KWIK_FILE_DIRECTORY      File directory path
  KWIK_CHUNK_SIZE          Chunk size in bytes
  KWIK_MAX_CONCURRENT      Maximum concurrent connections
  KWIK_MAX_FILE_SIZE       Maximum file size in bytes
  KWIK_LOG_LEVEL           Log level

The primary server is the main entry point for clients and coordinates
file transfers using the KWIK protocol. When a secondary server is configured,
it automatically establishes multi-path connections for improved performance.

Examples:
  # Start with default settings
  %s

  # Start with multi-path support
  %s -secondary localhost:8081

  # Start with custom address and file directory
  %s -address 0.0.0.0:8080 -files /var/ftpa/data

  # Start with configuration file
  %s -config /etc/ftpa/primary.yaml

  # Start with verbose output and custom settings
  %s -verbose -chunk-size 131072 -max-concurrent 20
`, os.Args[0], os.Args[0], os.Args[0], os.Args[0], os.Args[0], os.Args[0])
}

func showConfigSummary(config *config.ServerConfiguration) {
	fmt.Printf("\n📋 Configuration Summary:\n")
	fmt.Printf("   Primary Address: %s\n", config.Server.Address)
	if config.Server.SecondaryAddress != "" {
		fmt.Printf("   Secondary Address: %s\n", config.Server.SecondaryAddress)
	}
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

func monitorServerStatus(fileServer *server.FileTransferServer, verbose bool) {
	if !verbose {
		return
	}

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if !fileServer.IsRunning() {
				return
			}

			// Show server status
			activeRequests := fileServer.GetActiveRequests()
			fmt.Printf("📊 Status: %d active requests\n", len(activeRequests))

			// Show active requests details if any
			if len(activeRequests) > 0 && verbose {
				fmt.Printf("   Active requests:\n")
				for requestKey, requestState := range activeRequests {
					elapsed := time.Since(requestState.RequestedAt)
					fmt.Printf("   - %s: %s (%.1fs ago)\n",
						requestKey, requestState.Status.String(), elapsed.Seconds())
				}
			}
		}
	}
}
