# KWIK Usage Guide

This guide provides detailed information on how to use KWIK (QUIC With Intelligent Konnections) in your applications.

## Table of Contents

1. [Basic Usage](#basic-usage)
2. [Configuration](#configuration)
3. [Session Management](#session-management)
4. [Stream Operations](#stream-operations)
5. [Multi-Path Features](#multi-path-features)
6. [Error Handling](#error-handling)
7. [Performance Tuning](#performance-tuning)
8. [Monitoring and Metrics](#monitoring-and-metrics)

## Basic Usage

### Direct Usage (Recommended)

KWIK provides a simplified API where you can directly call `Dial()` and `Listen()` without managing instances:

```go
import kwik "kwik/pkg"

// Client usage - KWIK instance managed automatically
session, err := kwik.Dial(context.Background(), "server:4433", nil)
if err != nil {
    log.Fatal(err)
}
defer session.Close()

// Server usage - KWIK instance managed automatically  
listener, err := kwik.Listen("localhost:4433", nil)
if err != nil {
    log.Fatal(err)
}
defer listener.Close()
```

### Client Operations

```go
// Connect to server with optional configuration
config := kwik.DefaultConfig()
config.LogLevel = kwik.LogLevelInfo

session, err := kwik.Dial(context.Background(), "server:4433", config)
if err != nil {
    log.Fatal(err)
}
defer session.Close()

// Open stream
stream, err := session.OpenStreamSync(context.Background())
if err != nil {
    log.Fatal(err)
}
defer stream.Close()

// Send data
_, err = stream.Write([]byte("Hello KWIK"))
if err != nil {
    log.Fatal(err)
}

// Read response
buffer := make([]byte, 1024)
n, err := stream.Read(buffer)
if err != nil {
    log.Fatal(err)
}
```

### Server Operations

```go
// Create listener with optional configuration
config := kwik.DefaultConfig()
config.MaxSessions = 100

listener, err := kwik.Listen("localhost:4433", config)
if err != nil {
    log.Fatal(err)
}
defer listener.Close()

// Accept sessions
for {
    session, err := listener.Accept(context.Background())
    if err != nil {
        log.Printf("Accept error: %v", err)
        continue
    }
    
    go handleSession(session)
}

func handleSession(session session.Session) {
    defer session.Close()
    
    for {
        stream, err := session.AcceptStream(context.Background())
        if err != nil {
            return
        }
        
        go handleStream(stream)
    }
}
```

## Configuration

### Default Configuration

```go
config := kwik.DefaultConfig()
// config contains sensible defaults for most use cases
```

### Custom Configuration

```go
config := &kwik.Config{
    // Session settings
    MaxSessions:              100,
    SessionIdleTimeout:       30 * time.Minute,
    SessionCleanupInterval:   5 * time.Minute,
    
    // Path settings
    MaxPathsPerSession:       5,
    PathHealthCheckInterval:  10 * time.Second,
    PathFailureThreshold:     3,
    PathRecoveryTimeout:      30 * time.Second,
    
    // Stream settings
    OptimalStreamsPerReal:    4,
    MaxStreamsPerReal:        8,
    StreamCleanupInterval:    1 * time.Minute,
    
    // Performance settings
    MaxPacketSize:            1400,
    ReadBufferSize:           64 * 1024,
    WriteBufferSize:          64 * 1024,
    AggregationEnabled:       true,
    LoadBalancingStrategy:    data.LoadBalancingAdaptive,
    
    // Monitoring settings
    LogLevel:                 kwik.LogLevelInfo,
    MetricsEnabled:           true,
    MetricsInterval:          30 * time.Second,
}

kwikInstance, err := kwik.New(config)
```

### Configuration Options

| Option | Description | Default |
|--------|-------------|---------|
| `MaxSessions` | Maximum concurrent sessions | 1000 |
| `SessionIdleTimeout` | Session idle timeout | 30 minutes |
| `MaxPathsPerSession` | Maximum paths per session | 8 |
| `PathHealthCheckInterval` | Path health check frequency | 10 seconds |
| `OptimalStreamsPerReal` | Optimal logical streams per real stream | 4 |
| `AggregationEnabled` | Enable data aggregation | true |
| `LogLevel` | Logging level | Info |
| `MetricsEnabled` | Enable metrics collection | true |

## Session Management

### Session Lifecycle

```go
// Client session
session, err := kwikInstance.Dial(ctx, "server:4433")
if err != nil {
    return err
}

// Use session...

// Close session
err = session.Close()
if err != nil {
    log.Printf("Error closing session: %v", err)
}
```

### Session Information

```go
// Get session paths
activePaths := session.GetActivePaths()
deadPaths := session.GetDeadPaths()
allPaths := session.GetAllPaths()

// Display path information
for _, path := range activePaths {
    fmt.Printf("Path %s: %s (Primary: %v, Status: %s)\n",
        path.PathID, path.Address, path.IsPrimary, path.Status)
}
```

## Stream Operations

### Opening Streams

```go
// Synchronous stream opening
stream, err := session.OpenStreamSync(context.Background())
if err != nil {
    return err
}

// Asynchronous stream opening
stream, err := session.OpenStream()
if err != nil {
    return err
}
```

### Stream I/O

```go
// Writing data
data := []byte("Hello, KWIK!")
n, err := stream.Write(data)
if err != nil {
    return err
}

// Reading data
buffer := make([]byte, 1024)
n, err := stream.Read(buffer)
if err != nil && err != io.EOF {
    return err
}

// Stream metadata
streamID := stream.StreamID()
pathID := stream.PathID()
```

### Stream Management

```go
// Set deadlines
stream.SetReadDeadline(time.Now().Add(5 * time.Second))
stream.SetWriteDeadline(time.Now().Add(5 * time.Second))

// Close stream
err = stream.Close()
if err != nil {
    log.Printf("Error closing stream: %v", err)
}
```

## Multi-Path Features

### Adding Paths (Server-Side)

```go
func handleSession(session session.Session) {
    // Add secondary paths for improved performance
    err := session.AddPath("secondary-server-1:4434")
    if err != nil {
        log.Printf("Failed to add path: %v", err)
    }
    
    err = session.AddPath("secondary-server-2:4435")
    if err != nil {
        log.Printf("Failed to add path: %v", err)
    }
    
    // Continue with normal session handling...
}
```

### Path Management

```go
// Remove a path
err := session.RemovePath(pathID)
if err != nil {
    log.Printf("Failed to remove path: %v", err)
}

// Monitor path status
activePaths := session.GetActivePaths()
for _, path := range activePaths {
    if path.Status == session.PathStatusDead {
        log.Printf("Path %s is dead, removing...", path.PathID)
        session.RemovePath(path.PathID)
    }
}
```

### Raw Packet Transmission

```go
// Send custom protocol data via specific path
customData := []byte{0x01, 0x02, 0x03, 0x04}
err := session.SendRawData(customData, pathID)
if err != nil {
    log.Printf("Failed to send raw data: %v", err)
}
```

## Error Handling

### KWIK-Specific Errors

```go
import "kwik/internal/utils"

func handleError(err error) {
    switch err {
    case utils.ErrPathNotFound:
        log.Printf("Path not found: %v", err)
    case utils.ErrPathDead:
        log.Printf("Path is dead: %v", err)
    case utils.ErrInvalidFrame:
        log.Printf("Invalid frame: %v", err)
    case utils.ErrAuthenticationFailed:
        log.Printf("Authentication failed: %v", err)
    default:
        log.Printf("General error: %v", err)
    }
}
```

### Error Recovery

```go
func robustStreamOperation(session session.Session) error {
    maxRetries := 3
    for i := 0; i < maxRetries; i++ {
        stream, err := session.OpenStreamSync(context.Background())
        if err != nil {
            if i == maxRetries-1 {
                return fmt.Errorf("failed to open stream after %d retries: %w", maxRetries, err)
            }
            log.Printf("Retry %d: failed to open stream: %v", i+1, err)
            time.Sleep(time.Duration(i+1) * time.Second)
            continue
        }
        
        // Use stream...
        defer stream.Close()
        return nil
    }
    
    return fmt.Errorf("exhausted retries")
}
```

## Performance Tuning

### High-Throughput Configuration

```go
config := kwik.DefaultConfig()
config.MaxPathsPerSession = 8
config.OptimalStreamsPerReal = 4
config.MaxStreamsPerReal = 8
config.AggregationEnabled = true
config.LoadBalancingStrategy = data.LoadBalancingAdaptive
config.ReadBufferSize = 128 * 1024
config.WriteBufferSize = 128 * 1024
```

### Low-Latency Configuration

```go
config := kwik.DefaultConfig()
config.PathHealthCheckInterval = 1 * time.Second
config.PathFailureThreshold = 2
config.PathRecoveryTimeout = 5 * time.Second
config.OptimalStreamsPerReal = 2
config.MaxStreamsPerReal = 4
```

### Memory-Optimized Configuration

```go
config := kwik.DefaultConfig()
config.MaxSessions = 50
config.MaxPathsPerSession = 3
config.OptimalStreamsPerReal = 3
config.ReadBufferSize = 32 * 1024
config.WriteBufferSize = 32 * 1024
config.SessionCleanupInterval = 1 * time.Minute
```

## Monitoring and Metrics

### Enabling Metrics

```go
config := kwik.DefaultConfig()
config.MetricsEnabled = true
config.MetricsInterval = 10 * time.Second

kwikInstance, err := kwik.New(config)
```

### Accessing Metrics

```go
// Get system metrics
metrics := kwikInstance.GetMetrics()
if metrics != nil {
    log.Printf("Active sessions: %d", metrics.ActiveSessions)
    log.Printf("Active paths: %d", metrics.ActivePaths)
    log.Printf("Total bytes transferred: %d", metrics.TotalBytesTransferred)
}

// Get system state
state := kwikInstance.GetState()
log.Printf("KWIK system state: %s", state)
```

### Custom Monitoring

```go
func monitorKWIK(kwikInstance *kwik.KWIK) {
    ticker := time.NewTicker(30 * time.Second)
    defer ticker.Stop()
    
    for range ticker.C {
        metrics := kwikInstance.GetMetrics()
        if metrics != nil {
            // Log or send metrics to monitoring system
            log.Printf("KWIK Metrics - Sessions: %d, Paths: %d, Bytes: %d",
                metrics.ActiveSessions, metrics.ActivePaths, metrics.TotalBytesTransferred)
        }
    }
}

// Start monitoring in background
go monitorKWIK(kwikInstance)
```

### Logging Configuration

```go
// Set log level
config := kwik.DefaultConfig()
config.LogLevel = kwik.LogLevelDebug  // Verbose logging

// Available log levels:
// - kwik.LogLevelDebug
// - kwik.LogLevelInfo
// - kwik.LogLevelWarn
// - kwik.LogLevelError
```

## Best Practices

### Resource Management

```go
// Always close resources
defer kwikInstance.Stop()
defer session.Close()
defer stream.Close()

// Use context for timeouts
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()

session, err := kwikInstance.Dial(ctx, "server:4433")
```

### Error Handling

```go
// Handle specific errors
if err != nil {
    if errors.Is(err, utils.ErrPathDead) {
        // Handle dead path specifically
        log.Printf("Path is dead, trying alternative")
    } else {
        // Handle general error
        log.Printf("Operation failed: %v", err)
    }
}
```

### Performance

```go
// Reuse streams when possible
var streamPool = make(chan session.Stream, 10)

func getStream(session session.Session) (session.Stream, error) {
    select {
    case stream := <-streamPool:
        return stream, nil
    default:
        return session.OpenStreamSync(context.Background())
    }
}

func returnStream(stream session.Stream) {
    select {
    case streamPool <- stream:
        // Stream returned to pool
    default:
        // Pool full, close stream
        stream.Close()
    }
}
```

This usage guide covers the essential aspects of using KWIK in your applications. For more advanced use cases and examples, refer to the `examples/` directory in the repository.