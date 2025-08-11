package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"filetransfer/internal"
)

type CLIConfig struct {
	ConfigFile string
	Verbose    bool
}

func main() {
	cliConfig := &CLIConfig{}
	
	// Parse command line arguments
	flag.StringVar(&cliConfig.ConfigFile, "config", "", "Path to configuration file")
	flag.BoolVar(&cliConfig.Verbose, "verbose", false, "Enable verbose logging")
	
	// Custom usage message
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "KWIK File Transfer Server\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nConfiguration:\n")
		fmt.Fprintf(os.Stderr, "  Configuration can be provided via YAML file or environment variables.\n")
		fmt.Fprintf(os.Stderr, "  Environment variables take precedence over file configuration.\n")
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  %s -config config/server.yaml -verbose\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  KWIK_SERVER_ADDRESS=:9090 KWIK_FILE_DIRECTORY=/path/to/files %s\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\nEnvironment Variables:\n")
		fmt.Fprintf(os.Stderr, "  KWIK_SERVER_ADDRESS      - Server address to listen on\n")
		fmt.Fprintf(os.Stderr, "  KWIK_FILE_DIRECTORY      - Directory containing files to serve\n")
		fmt.Fprintf(os.Stderr, "  KWIK_SECONDARY_ADDRESS   - Secondary address for multi-path\n")
		fmt.Fprintf(os.Stderr, "  KWIK_MAX_FILE_SIZE       - Maximum file size in bytes\n")
		fmt.Fprintf(os.Stderr, "  KWIK_MAX_CONCURRENT      - Maximum concurrent connections\n")
		fmt.Fprintf(os.Stderr, "  KWIK_ALLOWED_EXTENSIONS  - Comma-separated allowed file extensions\n")
	}
	
	flag.Parse()
	
	// Load configuration from file and environment
	config, err := internal.LoadConfiguration(cliConfig.ConfigFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
		flag.Usage()
		os.Exit(1)
	}
	
	// Override log level if verbose is specified
	if cliConfig.Verbose {
		config.Security.LogLevel = "debug"
	}
	
	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	
	// Display configuration
	fmt.Printf("Starting KWIK File Transfer Server...\n")
	fmt.Printf("Address: %s\n", config.Server.Address)
	fmt.Printf("File Directory: %s\n", config.Server.FileDirectory)
	if config.Server.SecondaryAddress != "" {
		fmt.Printf("Secondary Address: %s\n", config.Server.SecondaryAddress)
	}
	fmt.Printf("Max File Size: %d bytes\n", config.Limits.MaxFileSize)
	fmt.Printf("Max Concurrent: %d\n", config.Limits.MaxConcurrent)
	fmt.Printf("Chunk Size: %d bytes\n", config.Performance.ChunkSize)
	fmt.Printf("Log Level: %s\n", config.Security.LogLevel)
	
	if len(config.Limits.AllowedExtensions) > 0 {
		fmt.Printf("Allowed Extensions: %v\n", config.Limits.AllowedExtensions)
	}
	
	// Wait for shutdown signal
	go func() {
		sig := <-sigChan
		fmt.Printf("\nReceived signal %v, shutting down gracefully...\n", sig)
		// TODO: Implement graceful server shutdown
		os.Exit(0)
	}()
	
	// TODO: Initialize and start the actual file transfer server
	fmt.Println("Server started. Press Ctrl+C to stop.")
	
	// Keep the main goroutine alive
	select {}
}

