# KWIK Use Cases Examples

This directory contains focused, dedicated examples that demonstrate specific KWIK features. Each use case is **completely independent** with its own client and server pair.

## 📁 Available Examples

### 1. Simple Echo (`simple-echo/`)
**Purpose**: Basic KWIK client-server communication

**Files**:
- `client.go` - Connects, sends message, receives echo
- `server.go` - Accepts connections, echoes messages back

**Usage**:
```bash
# Terminal 1: Start server
make run-echo-server

# Terminal 2: Run client
make run-echo-client
```

**Demonstrates**:
- Basic KWIK connection establishment
- Stream creation and data transmission
- QUIC-compatible API usage

---

### 2. Multi-Path Demo (`multi-path-demo/`)
**Purpose**: Server-side path management and client monitoring

**Files**:
- `server.go` - Demonstrates adding secondary paths
- `client.go` - Monitors path changes and shows multi-path benefits

**Usage**:
```bash
# Terminal 1: Start server
make run-multipath-server

# Terminal 2: Run client
make run-multipath-client
```

**Demonstrates**:
- Server-controlled path addition (`AddPath`)
- Client-side path monitoring
- Multi-path session management

---

### 3. Concurrent Streams (`concurrent-streams/`)
**Purpose**: Multiple simultaneous streams

**Files**:
- `server.go` - Handles multiple streams concurrently
- `client.go` - Opens multiple streams simultaneously

**Usage**:
```bash
# Terminal 1: Start server
make run-concurrent-server

# Terminal 2: Run client
make run-concurrent-client
```

**Demonstrates**:
- Concurrent stream handling
- Stream multiplexing
- Parallel data transmission

---

### 4. Raw Packets (`raw-packets/`)
**Purpose**: Custom protocol support

**Files**:
- `server.go` - Sends raw packet data
- `client.go` - Receives and processes custom protocol data

**Usage**:
```bash
# Terminal 1: Start server
make run-rawpackets-server

# Terminal 2: Run client
make run-rawpackets-client
```

**Demonstrates**:
- Raw packet transmission (`SendRawData`)
- Custom protocol integration
- Advanced KWIK features

## 🚀 Quick Start

### Build All Examples
```bash
make all
```

### Run a Complete Demo
```bash
# Terminal 1: Start echo server
make run-echo-server

# Terminal 2: Run echo client  
make run-echo-client
```

### Clean Up
```bash
make clean
```

## 📖 Learning Path

1. **Start with Simple Echo** - Learn basic KWIK usage
2. **Try Concurrent Streams** - Understand stream multiplexing
3. **Explore Multi-Path** - See advanced path management
4. **Experiment with Raw Packets** - Custom protocol integration

## 🔧 Customization

Each example is self-contained and can be easily modified:

- **Change addresses**: Edit the `localhost:4433` addresses
- **Add features**: Extend examples with additional functionality
- **Combine examples**: Mix features from different use cases
- **Create new examples**: Use these as templates

## 💡 Tips

- **Server First**: Always start the server before running clients
- **Port Conflicts**: Use different ports if running multiple servers
- **Logs**: Check console output for connection status and errors
- **Experimentation**: Modify the code to explore different behaviors

## 🔗 Related Documentation

- [Main Examples](../README.md) - Interactive client/server examples
- [Migration Guide](../../MIGRATION.md) - Migrating from QUIC
- [Usage Guide](../../docs/USAGE.md) - Detailed API documentation