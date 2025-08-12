# FTPA Architecture

## Overview

FTPA (File Transfer Protocol over KWIK) provides a high-performance file transfer system using the KWIK transport protocol with multi-path support.

## New Simplified Architecture

### Core Principles

1. **Self-Managed Sessions**: Both primary and secondary servers manage their own KWIK sessions internally
2. **Simple Interface**: Applications use just `Start()` and `Stop()` methods
3. **Configuration-Driven**: All settings are managed through `config.ServerConfiguration`
4. **Consistent Naming**: Both servers are now properly named as "servers" not "handlers"

### Server Components

#### 1. FileTransferServer (Primary Server)

The main file transfer server that handles client requests and coordinates multi-path transfers.

**Key Features:**
- Manages its own KWIK session
- Handles file requests from clients
- Coordinates chunk distribution with secondary servers
- Provides bandwidth-based optimization
- Supports dynamic rebalancing

**Usage:**
```go
// Create server
serverConfig := config.DefaultServerConfiguration()
serverConfig.Server.Address = "localhost:8080"
serverConfig.Server.FileDirectory = "./data"

server, err := server.NewFileTransferServer(serverConfig)
if err != nil {
    log.Fatal(err)
}

// Start server
if err := server.Start(); err != nil {
    log.Fatal(err)
}

// Stop server
if err := server.Stop(); err != nil {
    log.Printf("Error: %v", err)
}
```

#### 2. SecondaryFileTransferServer (Secondary Server)

A full-featured secondary server that handles chunk requests from the primary server.

**Key Features:**
- Manages its own KWIK session
- Processes chunk commands from primary server
- Supports raw packet integration
- Provides file validation and security
- Handles command state management

**Usage:**
```go
// Create secondary server
serverConfig := config.DefaultServerConfiguration()
serverConfig.Server.Address = "localhost:8081"
serverConfig.Server.FileDirectory = "./data"

secondaryServer, err := server.NewSecondaryFileTransferServer(serverConfig)
if err != nil {
    log.Fatal(err)
}

// Start secondary server
if err := secondaryServer.Start(); err != nil {
    log.Fatal(err)
}

// Stop secondary server
if err := secondaryServer.Stop(); err != nil {
    log.Printf("Error: %v", err)
}
```

### Configuration

All servers use the same `config.ServerConfiguration` structure:

```go
type ServerConfiguration struct {
    Server struct {
        Address          string // Server listening address
        FileDirectory    string // Directory containing files to serve
        SecondaryAddress string // Address of secondary server
        TLSCertFile      string // TLS certificate file (optional)
        TLSKeyFile       string // TLS key file (optional)
    }
    
    Limits struct {
        MaxFileSize       int64    // Maximum file size allowed
        MaxConcurrent     int      // Maximum concurrent transfers
        AllowedExtensions []string // Allowed file extensions
        SessionTimeout    string   // Session timeout duration
    }
    
    Performance struct {
        ChunkSize         int32    // Size of each chunk
        SecondaryPaths    []string // Secondary server path IDs
        BufferSize        int      // Buffer size for operations
        MaxRetries        int      // Maximum retry attempts
        RetryDelay        string   // Delay between retries
    }
    
    Security struct {
        RequireAuth       bool     // Whether authentication is required
        AllowedClients    []string // List of allowed client addresses
        DenyTraversal     bool     // Deny path traversal attempts
        LogLevel          string   // Logging level
    }
}
```

### Multi-Path Support

The system supports multi-path transfers where:

1. **Primary Server** receives file requests from clients
2. **Chunk Coordinator** distributes chunks between primary and secondary servers
3. **Secondary Servers** handle assigned chunks independently
4. **Raw Packet Integration** enables efficient communication between servers
5. **Dynamic Rebalancing** optimizes performance based on bandwidth metrics

### Command Line Tools

#### Primary Server
```bash
./kwik-file-server [config.yaml]
```

#### Secondary Server
```bash
./kwik-secondary-server [config.yaml]
```

### Testing

The architecture includes comprehensive test coverage:

- **Unit Tests**: Test individual components in isolation
- **Integration Tests**: Test server interactions and multi-path scenarios
- **Mock Framework**: Provides realistic testing without actual KWIK sessions

### Migration from Old Architecture

The new architecture maintains backward compatibility while providing:

1. **Simplified API**: No need to manage KWIK sessions externally
2. **Better Separation**: Clear distinction between primary and secondary servers
3. **Improved Testing**: Better mock support and test isolation
4. **Enhanced Configuration**: Centralized configuration management

### Future Enhancements

1. **Actual KWIK Integration**: Replace placeholder KWIK session creation with real implementation
2. **Load Balancing**: Add support for multiple secondary servers with load balancing
3. **Monitoring**: Add metrics collection and monitoring endpoints
4. **Authentication**: Implement client authentication and authorization
5. **Encryption**: Add end-to-end encryption for sensitive file transfers

## Example Usage

See `ftpa/examples/simple_usage.go` for a complete example of running both primary and secondary servers together.