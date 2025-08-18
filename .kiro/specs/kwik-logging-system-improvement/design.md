# Design Document

## Overview

Cette conception améliore le système de logging KWIK existant en ajoutant la configuration externe, en éliminant les logs directs, et en permettant un contrôle granulaire par composant. Le design s'appuie sur le logger centralisé existant (`logger.go`) tout en ajoutant des capacités de configuration et de gestion avancées.

## Architecture

### Configuration System

```
Config Sources (Priority Order):
1. Environment Variables (KWIK_LOG_LEVEL, KWIK_LOG_FORMAT, etc.)
2. Configuration File (kwik-log.yaml)
3. Programmatic Configuration (via Config struct)
4. Default Configuration
```

### Logger Hierarchy

```
GlobalLoggerManager
├── ComponentLogger (session)
├── ComponentLogger (transport) 
├── ComponentLogger (stream)
├── ComponentLogger (data)
├── ComponentLogger (control)
└── ComponentLogger (presentation)
```

### Configuration Structure

```yaml
# kwik-log.yaml
logging:
  level: "WARN"              # Global level: DEBUG, INFO, WARN, ERROR, SILENT
  format: "text"             # Format: text, json, structured
  destination: "stdout"      # Destination: stdout, stderr, file
  file:
    path: "/var/log/kwik.log"
    maxSize: "100MB"
    maxBackups: 5
    maxAge: 30
  components:
    session: "INFO"
    transport: "WARN" 
    stream: "DEBUG"
    data: "ERROR"
    control: "INFO"
    presentation: "WARN"
```

## Components and Interfaces

### Enhanced Logger Interface

```go
type ConfigurableLogger interface {
    Logger // Existing interface
    
    // Configuration methods
    LoadConfig(configPath string) error
    LoadConfigFromEnv() error
    SetComponentLevel(component string, level LogLevel) error
    GetComponentLevel(component string) LogLevel
    
    // Output management
    SetOutput(destination LogDestination) error
    SetFormat(format LogFormat) error
    
    // Runtime control
    ReloadConfig() error
    GetCurrentConfig() *LogConfig
}

type LogConfig struct {
    GlobalLevel    LogLevel
    ComponentLevels map[string]LogLevel
    Format         LogFormat
    Destination    LogDestination
    FileConfig     *FileLogConfig
}

type LogDestination int
const (
    DestinationStdout LogDestination = iota
    DestinationStderr
    DestinationFile
    DestinationSyslog
)

type LogFormat int
const (
    FormatText LogFormat = iota
    FormatJSON
    FormatStructured
)
```

### Component Logger Factory

```go
type LoggerFactory interface {
    CreateComponentLogger(component string) Logger
    UpdateComponentLevel(component string, level LogLevel) error
    GetGlobalLogger() ConfigurableLogger
}

type ComponentLoggerManager struct {
    globalLogger ConfigurableLogger
    componentLoggers map[string]Logger
    config *LogConfig
    mutex sync.RWMutex
}
```

### Configuration Loader

```go
type ConfigLoader interface {
    LoadFromFile(path string) (*LogConfig, error)
    LoadFromEnv() (*LogConfig, error)
    LoadDefaults() *LogConfig
    MergeConfigs(configs ...*LogConfig) *LogConfig
}
```

## Data Models

### Log Entry Structure

```go
type LogEntry struct {
    Timestamp   time.Time
    Level       LogLevel
    Component   string
    Message     string
    Fields      map[string]interface{}
    Context     map[string]interface{}
    Caller      *CallerInfo
    TraceID     string
    SessionID   string
    StreamID    uint64
}

type CallerInfo struct {
    File     string
    Line     int
    Function string
}
```

### Configuration Models

```go
type FileLogConfig struct {
    Path       string
    MaxSize    int64  // in bytes
    MaxBackups int
    MaxAge     int    // in days
    Compress   bool
}

type ComponentConfig struct {
    Name  string
    Level LogLevel
}
```

## Error Handling

### Configuration Errors

1. **Invalid Configuration**: Log warning and use defaults
2. **File Access Errors**: Fallback to stdout with warning
3. **Permission Errors**: Attempt stderr, then stdout
4. **Invalid Log Levels**: Use closest valid level with warning

### Runtime Errors

1. **Log Destination Unavailable**: Automatic fallback chain
2. **Disk Space Issues**: Implement log rotation and cleanup
3. **Format Errors**: Fallback to simple text format

### Error Recovery Strategy

```go
type LoggerErrorHandler struct {
    fallbackDestinations []LogDestination
    retryAttempts       int
    retryDelay          time.Duration
}
```

## Testing Strategy

### Unit Tests

1. **Configuration Loading Tests**
   - Valid YAML parsing
   - Environment variable parsing
   - Invalid configuration handling
   - Configuration merging logic

2. **Logger Factory Tests**
   - Component logger creation
   - Level inheritance
   - Dynamic level updates

3. **Output Format Tests**
   - Text format validation
   - JSON format validation
   - Structured format validation

### Integration Tests

1. **End-to-End Logging Tests**
   - Configuration file to log output
   - Environment variable configuration
   - Component-specific logging levels

2. **Performance Tests**
   - Logging overhead measurement
   - High-volume logging scenarios
   - Memory usage under load

3. **Error Handling Tests**
   - Configuration error scenarios
   - File system error scenarios
   - Network logging failure scenarios

### Migration Tests

1. **Backward Compatibility Tests**
   - Existing code continues to work
   - Default behavior unchanged
   - API compatibility maintained

2. **Direct Log Replacement Tests**
   - All fmt.Printf calls replaced
   - All log.Printf calls replaced
   - Equivalent output verification

## Implementation Plan

### Phase 1: Configuration System
- Implement LogConfig structure
- Create ConfigLoader with file and environment support
- Add configuration validation and error handling

### Phase 2: Enhanced Logger
- Extend existing Logger interface
- Implement ConfigurableLogger
- Add output destination and format support

### Phase 3: Component Integration
- Create LoggerFactory and ComponentLoggerManager
- Integrate with existing KWIK components
- Replace direct logging calls

### Phase 4: Advanced Features
- Implement file rotation
- Add structured logging formats
- Performance optimization

### Migration Strategy

1. **Gradual Replacement**: Replace direct logs component by component
2. **Backward Compatibility**: Maintain existing Logger interface
3. **Default Behavior**: Ensure no breaking changes for existing users
4. **Testing**: Comprehensive testing at each phase

## Performance Considerations

### Optimization Strategies

1. **Lazy Logger Creation**: Create component loggers only when needed
2. **Level Checking**: Fast level checks before expensive operations
3. **Buffer Management**: Efficient buffer reuse for formatting
4. **Async Logging**: Optional asynchronous logging for high-throughput scenarios

### Memory Management

1. **Logger Pooling**: Reuse logger instances where possible
2. **Buffer Pooling**: Reuse formatting buffers
3. **Garbage Collection**: Minimize allocations in hot paths

### Benchmarking Targets

- Logging overhead < 1% of total CPU time
- Memory allocation < 100 bytes per log entry
- Configuration reload < 10ms
- Component logger creation < 1ms