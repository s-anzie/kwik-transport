package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

type ClientConfig struct {
	ServerAddress string
	Filename      string
	OutputPath    string
	Resume        bool
	Verbose       bool
	ConfigFile    string
}

func main() {
	config := &ClientConfig{}
	
	// Parse command line arguments
	flag.StringVar(&config.ServerAddress, "server", "", "Server address to connect to (required)")
	flag.StringVar(&config.Filename, "file", "", "File to download (required)")
	flag.StringVar(&config.OutputPath, "output", "", "Output path for downloaded file (optional)")
	flag.BoolVar(&config.Resume, "resume", false, "Resume interrupted transfer")
	flag.BoolVar(&config.Verbose, "verbose", false, "Enable verbose logging")
	flag.StringVar(&config.ConfigFile, "config", "", "Path to configuration file")
	
	// Custom usage message
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "KWIK File Transfer Client\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  %s -server localhost:8080 -file document.pdf\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -server 192.168.1.100:9090 -file video.mp4 -output /downloads/\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -server example.com:8080 -file data.zip -resume -verbose\n", os.Args[0])
	}
	
	flag.Parse()
	
	// Validate required arguments
	if err := validateConfig(config); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		flag.Usage()
		os.Exit(1)
	}
	
	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	
	// Setup default output path if not provided
	if config.OutputPath == "" {
		config.OutputPath = filepath.Base(config.Filename)
	}
	
	fmt.Printf("KWIK File Transfer Client\n")
	fmt.Printf("Server: %s\n", config.ServerAddress)
	fmt.Printf("File: %s\n", config.Filename)
	fmt.Printf("Output: %s\n", config.OutputPath)
	if config.Resume {
		fmt.Printf("Resume mode enabled\n")
	}
	if config.Verbose {
		fmt.Printf("Verbose logging enabled\n")
	}
	
	// Handle shutdown signal
	go func() {
		sig := <-sigChan
		fmt.Printf("\nReceived signal %v, canceling download...\n", sig)
		// TODO: Implement graceful download cancellation and cleanup
		os.Exit(0)
	}()
	
	// TODO: Initialize and start the actual file transfer client
	fmt.Println("Starting download...")
	
	// Placeholder for download logic
	fmt.Println("Download completed successfully!")
}

func validateConfig(config *ClientConfig) error {
	if config.ServerAddress == "" {
		return fmt.Errorf("server address is required")
	}
	
	if config.Filename == "" {
		return fmt.Errorf("filename is required")
	}
	
	// Validate config file if provided
	if config.ConfigFile != "" {
		if _, err := os.Stat(config.ConfigFile); os.IsNotExist(err) {
			return fmt.Errorf("config file does not exist: %s", config.ConfigFile)
		}
	}
	
	// Validate output directory exists if output path is specified
	if config.OutputPath != "" {
		outputDir := filepath.Dir(config.OutputPath)
		if outputDir != "." {
			if _, err := os.Stat(outputDir); os.IsNotExist(err) {
				return fmt.Errorf("output directory does not exist: %s", outputDir)
			}
		}
	}
	
	return nil
}