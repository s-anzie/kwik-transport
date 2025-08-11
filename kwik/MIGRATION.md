# Migration Guide: From QUIC to KWIK

This guide demonstrates how to migrate from standard QUIC to KWIK (QUIC With Intelligent Konnections) while maintaining full compatibility with existing QUIC applications.

## Table of Contents

1. [Overview](#overview)
2. [Key Differences](#key-differences)
3. [Basic Migration](#basic-migration)
4. [Advanced Features](#advanced-features)
5. [Performance Considerations](#performance-considerations)
6. [Troubleshooting](#troubleshooting)

## Overview

KWIK provides a drop-in replacement for QUIC with enhanced multi-path capabilities. The API is 100% compatible with QUIC, meaning existing applications can be migrated with minimal code changes while gaining access to advanced features like:

- **Multi-server connectivity**: Connect to multiple servers simultaneously
- **Intelligent path management**: Automatic failover and load balancing
- **Enhanced performance**: Optimized stream multiplexing and data aggregation
- **Custom protocol support**: Raw packet transmission for specialized use cases

## Key Differences

### QUIC vs KWIK Architecture

```
QUIC Architecture:
Client ←→ Single QUIC Connection ←→ Server

KWIK Architecture:
                    ┌─ Primary Server
Client ←→ KWIK ←────┼─ Secondary Server 1
          Session   ├─ Secondary Server 2
                    └─ Secondary Server N
```

### Interface Compatibility

KWIK maintains 100% interface compatibility with QUIC:

| Operation | QUIC | KWIK | Notes |
|-----------|------|------|-------|
| `Dial()` | ✓ | ✓ | Identical signature |
| `Listen()` | ✓ | ✓ | Identical signature |
| `OpenStreamSync()` | ✓ | ✓ | Identical behavior |
| `AcceptStream()` | ✓ | ✓ | Identical behavior |
| `Stream.Read()` | ✓ | ✓ | Identical behavior |
| `Stream.Write()` | ✓ | ✓ | Identical behavior |

## Basic Migration

### 1. Client Migration

#### Before (QUIC):
```go
package main

import (
    "context"
    "crypto/tls"
    "log"
    
    "github.com/quic-go/quic-go"
)

func main() {
    // QUIC client
    conn, err := quic.DialAddr(context.Background(), "server:4433", &tls.Config{
        InsecureSkipVerify: true,
    }, nil)
    if err != nil {
        log.Fatal(err)
    }
    defer conn.CloseWithError(0, "")

    stream, err := conn.OpenStreamSync(context.Background())
    if err != nil {
        log.Fatal(err)
    }
    defer stream.Close()

    // Use stream...
    _, err = stream.Write([]byte("Hello QUIC"))
    if err != nil {
        log.Fatal(err)
    }
}
```

#### After (KWIK):
```go
package main

import (
    "context"
    "log"
    
    kwik "kwik/pkg"
)

func main() {
    // KWIK client (identical interface to QUIC)
    session, err := kwik.Dial(context.Background(), "server:4433", nil)
    if err != nil {
        log.Fatal(err)
    }
    defer session.Close()

    stream, err := session.OpenStreamSync(context.Background())
    if err != nil {
        log.Fatal(err)
    }
    defer stream.Close()

    // Use stream (identical to QUIC)...
    _, err = stream.Write([]byte("Hello KWIK"))
    if err != nil {
        log.Fatal(err)
    }
}
```

### 2. Server Migration

#### Before (QUIC):
```go
package main

import (
    "context"
    "crypto/tls"
    "log"
    
    "github.com/quic-go/quic-go"
)

func main() {
    // QUIC server
    listener, err := quic.ListenAddr("localhost:4433", generateTLSConfig(), nil)
    if err != nil {
        log.Fatal(err)
    }
    defer listener.Close()

    for {
        conn, err := listener.Accept(context.Background())
        if err != nil {
            log.Printf("Failed to accept connection: %v", err)
            continue
        }

        go handleConnection(conn)
    }
}

func handleConnection(conn quic.Connection) {
    defer conn.CloseWithError(0, "")
    
    for {
        stream, err := conn.AcceptStream(context.Background())
        if err != nil {
            return
        }
        
        go handleStream(stream)
    }
}
```

#### After (KWIK):
```go
package main

import (
    "context"
    "log"
    
    kwik "kwik/pkg"
    "kwik/pkg/session"
)

func main() {
    // KWIK server (identical interface to QUIC)
    listener, err := kwik.Listen("localhost:4433", nil)
    if err != nil {
        log.Fatal(err)
    }
    defer listener.Close()

    for {
        session, err := listener.Accept(context.Background())
        if err != nil {
            log.Printf("Failed to accept session: %v", err)
            continue
        }

        go handleSession(session)
    }
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

## Advanced Features

### 1. Multi-Path Management (Server-Side)

KWIK allows servers to dynamically add and remove paths to enhance performance:

```go
func enhancedServerHandler(session session.Session) {
    // Add secondary servers for improved performance
    err := session.AddPath("secondary-server-1:4434")
    if err != nil {
        log.Printf("Failed to add secondary path: %v", err)
    }

    err = session.AddPath("secondary-server-2:4435")
    if err != nil {
        log.Printf("Failed to add secondary path: %v", err)
    }

    // Monitor path status
    activePaths := session.GetActivePaths()
    log.Printf("Active paths: %d", len(activePaths))
    
    for _, path := range activePaths {
        log.Printf("Path %s: %s (Primary: %v)", 
            path.PathID, path.Address, path.IsPrimary)
    }

    // Handle streams normally
    for {
        stream, err := session.AcceptStream(context.Background())
        if err != nil {
            return
        }
        
        go handleStream(stream)
    }
}
```

### 2. Raw Packet Transmission

For custom protocols, KWIK supports raw packet transmission:

```go
func customProtocolHandler(session session.Session) {
    // Send custom protocol data via specific path
    customData := []byte{0x01, 0x02, 0x03, 0x04} // Custom protocol packet
    
    paths := session.GetActivePaths()
    if len(paths) > 1 {
        // Send via secondary path
        err := session.SendRawData(customData, paths[1].PathID)
        if err != nil {
            log.Printf("Failed to send raw data: %v", err)
        }
    }
}
```

### 3. Path Monitoring and Management

```go
func pathMonitoringExample(session session.Session) {
    // Periodically check path status
    ticker := time.NewTicker(10 * time.Second)
    defer ticker.Stop()

    go func() {
        for range ticker.C {
            activePaths := session.GetActivePaths()
            deadPaths := session.GetDeadPaths()
            
            log.Printf("Health check - Active: %d, Dead: %d", 
                len(activePaths), len(deadPaths))
            
            // Remove dead paths
            for _, deadPath := range deadPaths {
                log.Printf("Removing dead path: %s", deadPath.PathID)
                session.RemovePath(deadPath.PathID)
            }
        }
    }()
}
```

## Performance Considerations

### 1. Configuration Optimization

```go
func createOptimizedKWIK() (*kwik.KWIK, error) {
    config := kwik.DefaultConfig()
    
    // Optimize for high-throughput scenarios
    config.MaxPathsPerSession = 8
    config.OptimalStreamsPerReal = 4
    config.MaxStreamsPerReal = 8
    config.AggregationEnabled = true
    config.LoadBalancingStrategy = data.LoadBalancingAdaptive
    
    // Optimize for low-latency scenarios
    config.PathHealthCheckInterval = 1 * time.Second
    config.PathFailureThreshold = 2
    config.PathRecoveryTimeout = 5 * time.Second
    
    return kwik.New(config)
}
```

### 2. Stream Management Best Practices

```go
func efficientStreamUsage(session session.Session) {
    // Use stream pooling for high-frequency operations
    streamPool := make(chan session.Stream, 10)
    
    // Pre-create streams
    for i := 0; i < 10; i++ {
        stream, err := session.OpenStreamSync(context.Background())
        if err != nil {
            log.Printf("Failed to pre-create stream: %v", err)
            continue
        }
        streamPool <- stream
    }
    
    // Use pooled streams
    select {
    case stream := <-streamPool:
        // Use stream
        defer func() {
            // Return to pool if still valid
            if stream != nil {
                streamPool <- stream
            }
        }()
    default:
        // Create new stream if pool is empty
        stream, err := session.OpenStreamSync(context.Background())
        if err != nil {
            log.Printf("Failed to create stream: %v", err)
            return
        }
        defer stream.Close()
    }
}
```

## Troubleshooting

### Common Migration Issues

#### 1. TLS Configuration
KWIK handles TLS configuration internally. Remove explicit TLS configuration when migrating:

```go
// QUIC (remove this)
tlsConfig := &tls.Config{
    InsecureSkipVerify: true,
}

// KWIK (TLS handled internally)
session, err := kwikInstance.Dial(context.Background(), "server:4433")
```

#### 2. Connection vs Session
KWIK uses "Session" instead of "Connection" to reflect multi-path nature:

```go
// QUIC
conn, err := quic.DialAddr(...)

// KWIK
session, err := kwikInstance.Dial(...)
```

#### 3. Error Handling
KWIK provides enhanced error information:

```go
func handleKWIKErrors(err error) {
    if kwikErr, ok := err.(*kwik.Error); ok {
        switch kwikErr.Code {
        case kwik.ErrPathNotFound:
            log.Printf("Path not found: %v", kwikErr)
        case kwik.ErrPathDead:
            log.Printf("Path is dead: %v", kwikErr)
        case kwik.ErrAuthenticationFailed:
            log.Printf("Authentication failed: %v", kwikErr)
        default:
            log.Printf("KWIK error: %v", kwikErr)
        }
    } else {
        log.Printf("General error: %v", err)
    }
}
```

### Performance Debugging

#### 1. Enable Metrics
```go
config := kwik.DefaultConfig()
config.MetricsEnabled = true
config.MetricsInterval = 10 * time.Second

kwikInstance, err := kwik.New(config)
// ...

// Check metrics periodically
metrics := kwikInstance.GetMetrics()
log.Printf("Metrics: %+v", metrics)
```

#### 2. Enable Debug Logging
```go
config := kwik.DefaultConfig()
config.LogLevel = kwik.LogLevelDebug

kwikInstance, err := kwik.New(config)
```

### Migration Checklist

- [ ] Replace QUIC imports with KWIK imports
- [ ] Create KWIK instance instead of direct QUIC calls
- [ ] Update connection handling to session handling
- [ ] Remove explicit TLS configuration
- [ ] Test basic connectivity
- [ ] Test stream operations
- [ ] Verify error handling
- [ ] Test performance under load
- [ ] Configure advanced features (optional)
- [ ] Update monitoring and logging

## Conclusion

Migrating from QUIC to KWIK is straightforward due to the maintained interface compatibility. The main changes involve:

1. Creating a KWIK instance
2. Using sessions instead of connections
3. Optionally leveraging advanced multi-path features

The migration provides immediate benefits in terms of resilience and performance while maintaining full compatibility with existing QUIC-based applications.

For more examples, see the `examples/` directory in this repository.