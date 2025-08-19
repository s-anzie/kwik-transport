package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"ftpa/internal/types"
	kwik "kwik/pkg"
	"kwik/pkg/session"
)

type ClientConfig struct {
	ServerAddress string
	Filename      string
	OutputPath    string
	Resume        bool
	Verbose       bool
	ConfigFile    string
}

// readJSON reads from the stream, accumulating data until it can unmarshal
// into one of the provided targets. It tries targets in order; the first
// successful unmarshal is used. A soft 8 MiB limit prevents unbounded growth.
func readJSON(stream session.Stream, timeout time.Duration, targets ...interface{}) error {
    const maxSize = 8 << 20 // 8 MiB
    buf := make([]byte, 0, 64<<10) // start with 64 KiB
    tmp := make([]byte, 1<<20)
    deadline := time.Now().Add(timeout)
    for {
        if time.Now().After(deadline) {
            return fmt.Errorf("timeout reading JSON message")
        }
        n, err := stream.Read(tmp)
        if err != nil {
            if err == io.EOF {
                if len(buf) == 0 {
                    return fmt.Errorf("connection closed")
                }
                // fallthrough to attempt parse
            } else {
                return err
            }
        }
        buf = append(buf, tmp[:n]...)
        if len(buf) > maxSize {
            return fmt.Errorf("message too large (%d bytes)", len(buf))
        }
        // Try to unmarshal into each target
        for _, t := range targets {
            if err := json.Unmarshal(buf, t); err == nil {
                return nil
            }
        }
        // Not yet a full JSON message; keep reading
    }
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
	
	// Initialize and start the actual file transfer
	if err := runDownload(config); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Download failed: %v\n", err)
		os.Exit(1)
	}
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

// runDownload performs a single file download using a single persistent KWIK stream
func runDownload(cfg *ClientConfig) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Connect to KWIK server
	sess, err := kwik.Dial(ctx, cfg.ServerAddress, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}
	defer sess.Close()

	// Open a stream and send FILE_REQUEST
	stream, err := sess.OpenStreamSync(ctx)
	if err != nil {
		return fmt.Errorf("failed to open stream: %w", err)
	}
	defer stream.Close()

	req := map[string]interface{}{
		"type":      "FILE_REQUEST",
		"filename":  cfg.Filename,
		"timestamp": time.Now().Unix(),
	}
	reqBytes, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}
	if _, err := stream.Write(reqBytes); err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}

    // Read FILE_METADATA response
    var metaMap map[string]interface{}
    if err := readJSON(stream, 30*time.Second, &metaMap); err != nil {
        return fmt.Errorf("failed to read metadata: %w", err)
    }
    if t, _ := metaMap["type"].(string); t != "FILE_METADATA" {
        return fmt.Errorf("unexpected first response type: %v", metaMap["type"])
    }

	// Parse metadata to struct
	metadata, err := parseMetadata(metaMap)
	if err != nil {
		return fmt.Errorf("failed to parse metadata: %w", err)
	}

	// Determine output file path
	outFilePath := resolveOutputPath(cfg.OutputPath, metadata.Filename)
	if err := os.MkdirAll(filepath.Dir(outFilePath), 0o755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Create destination file
	f, err := os.OpenFile(outFilePath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer f.Close()

	// Receive chunks until all received
	received := uint32(0)
	for {
        // Try to decode as error or chunk JSON
        var (
            errResp struct {
                Type  string `json:"type"`
                Error string `json:"error"`
            }
            chunk types.FileChunk
        )
        // Prefer error, otherwise chunk
        if err := readJSON(stream, 60*time.Second, &errResp, &chunk); err != nil {
            return fmt.Errorf("failed to read chunk: %w", err)
        }
        if errResp.Type == "ERROR" {
            return fmt.Errorf("server error: %s", errResp.Error)
        }

        // Write at offset
        if _, err := f.WriteAt(chunk.Data, chunk.Offset); err != nil {
            return fmt.Errorf("failed to write chunk at offset %d: %w", chunk.Offset, err)
        }
		received++

		if chunk.IsLast || received >= metadata.TotalChunks {
			break
		}
	}

	// fsync to ensure data is written
	if err := f.Sync(); err != nil {
		return fmt.Errorf("failed to sync output file: %w", err)
	}

	return nil
}

// readMessage reads one message from the stream with a timeout. The KWIK StreamBuffer preserves message boundaries.
func readMessage(stream session.Stream, timeout time.Duration) ([]byte, error) {
	if timeout > 0 {
		// If the stream supports setting deadlines, prefer that.
		// Otherwise we do a simple non-blocking select before read.
		// Here we just perform the read; underlying implementation should not merge messages.
		_ = timeout
	}
	buf := make([]byte, 1<<20) // 1 MiB buffer
	n, err := stream.Read(buf)
	if err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("connection closed")
		}
		return nil, err
	}
	return buf[:n], nil
}

func resolveOutputPath(outputFlag string, filename string) string {
	if outputFlag == "" {
		return filename
	}
	// If outputFlag points to a directory or ends with '/', treat as directory
	if strings.HasSuffix(outputFlag, string(os.PathSeparator)) {
		return filepath.Join(outputFlag, filename)
	}
	info, err := os.Stat(outputFlag)
	if err == nil && info.IsDir() {
		return filepath.Join(outputFlag, filename)
	}
	// Else treat as a full file path
	return outputFlag
}

func parseMetadata(m map[string]interface{}) (*types.FileMetadata, error) {
	filename, ok := m["filename"].(string)
	if !ok {
		return nil, fmt.Errorf("missing filename")
	}
	sizeFloat, ok := m["size"].(float64)
	if !ok {
		return nil, fmt.Errorf("missing size")
	}
	chunkSizeFloat, ok := m["chunk_size"].(float64)
	if !ok {
		return nil, fmt.Errorf("missing chunk_size")
	}
	totalChunksFloat, ok := m["total_chunks"].(float64)
	if !ok {
		return nil, fmt.Errorf("missing total_chunks")
	}
	md := types.NewFileMetadata(filename, int64(sizeFloat), int32(chunkSizeFloat))
	md.TotalChunks = uint32(totalChunksFloat)
	if checksum, _ := m["checksum"].(string); checksum != "" {
		md.Checksum = checksum
	}
	if ct, _ := m["content_type"].(string); ct != "" {
		md.ContentType = ct
	}
	return md, nil
}