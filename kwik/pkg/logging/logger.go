package logging

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// LogLevel represents the severity level of a log message
type LogLevel int

const (
	// SILENT - No logging output
	SILENT LogLevel = iota
	// ERROR - Only error messages
	ERROR
	// WARN - Warning and error messages
	WARN
	// INFO - Informational, warning, and error messages
	INFO
	// DEBUG - All messages including debug information
	DEBUG
	// TRACE - Most verbose level, includes all messages and detailed tracing
	TRACE
)

// String returns the string representation of the log level
func (l LogLevel) String() string {
	switch l {
	case SILENT:
		return "SILENT"
	case ERROR:
		return "ERROR"
	case WARN:
		return "WARN"
	case INFO:
		return "INFO"
	case DEBUG:
		return "DEBUG"
	case TRACE:
		return "TRACE"
	default:
		return "UNKNOWN"
	}
}

// ParseLogLevel parses a string into a LogLevel
func ParseLogLevel(level string) LogLevel {
	switch strings.ToUpper(level) {
	case "SILENT":
		return SILENT
	case "ERROR":
		return ERROR
	case "WARN", "WARNING":
		return WARN
	case "INFO":
		return INFO
	case "DEBUG":
		return DEBUG
	case "TRACE":
		return TRACE
	default:
		return INFO // Default to INFO level
	}
}

// Logger represents a KWIK logger with configurable levels
type Logger struct {
	level      LogLevel
	component  string
	output     io.Writer
	mutex      sync.RWMutex
	timeFormat string
}

// Global logger instance
var globalLogger *Logger
var globalMutex sync.RWMutex

// Config holds logger configuration
type Config struct {
	Level      LogLevel
	Output     io.Writer
	TimeFormat string
}

// DefaultConfig returns default logger configuration
func DefaultConfig() *Config {
	return &Config{
		Level:      INFO,
		Output:     os.Stdout,
		TimeFormat: "2006/01/02 15:04:05.000000",
	}
}

// NewLogger creates a new logger for a specific component
func NewLogger(component string, config *Config) *Logger {
	if config == nil {
		config = DefaultConfig()
	}
	
	return &Logger{
		level:      config.Level,
		component:  component,
		output:     config.Output,
		timeFormat: config.TimeFormat,
	}
}

// InitGlobalLogger initializes the global logger
func InitGlobalLogger(config *Config) {
	globalMutex.Lock()
	defer globalMutex.Unlock()
	
	globalLogger = NewLogger("KWIK-SYSTEM", config)
}

// GetGlobalLogger returns the global logger instance
func GetGlobalLogger() *Logger {
	globalMutex.RLock()
	defer globalMutex.RUnlock()
	
	if globalLogger == nil {
		// Initialize with default config if not already initialized
		globalLogger = NewLogger("KWIK-SYSTEM", DefaultConfig())
	}
	
	return globalLogger
}

// SetLevel sets the logging level
func (l *Logger) SetLevel(level LogLevel) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	l.level = level
}

// GetLevel returns the current logging level
func (l *Logger) GetLevel() LogLevel {
	l.mutex.RLock()
	defer l.mutex.RUnlock()
	return l.level
}

// SetOutput sets the output writer
func (l *Logger) SetOutput(output io.Writer) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	l.output = output
}

// log is the internal logging method
func (l *Logger) log(level LogLevel, format string, args ...interface{}) {
	l.mutex.RLock()
	currentLevel := l.level
	output := l.output
	component := l.component
	timeFormat := l.timeFormat
	l.mutex.RUnlock()
	
	// Check if we should log this message
	if level > currentLevel {
		return
	}
	
	// Format the message
	timestamp := time.Now().Format(timeFormat)
	levelStr := level.String()
	message := fmt.Sprintf(format, args...)
	
	// Create the log line
	logLine := fmt.Sprintf("[KWIK] %s [%s] [comp=%s] %s\n", 
		timestamp, levelStr, component, message)
	
	// Write to output
	fmt.Fprint(output, logLine)
}

// Error logs an error message
func (l *Logger) Error(format string, args ...interface{}) {
	l.log(ERROR, format, args...)
}

// Warn logs a warning message
func (l *Logger) Warn(format string, args ...interface{}) {
	l.log(WARN, format, args...)
}

// Info logs an informational message
func (l *Logger) Info(format string, args ...interface{}) {
	l.log(INFO, format, args...)
}

// Debug logs a debug message
func (l *Logger) Debug(format string, args ...interface{}) {
	l.log(DEBUG, format, args...)
}

// Trace logs a trace message
func (l *Logger) Trace(format string, args ...interface{}) {
	l.log(TRACE, format, args...)
}

// Global logging functions that use the global logger
func Error(format string, args ...interface{}) {
	GetGlobalLogger().Error(format, args...)
}

func Warn(format string, args ...interface{}) {
	GetGlobalLogger().Warn(format, args...)
}

func Info(format string, args ...interface{}) {
	GetGlobalLogger().Info(format, args...)
}

func Debug(format string, args ...interface{}) {
	GetGlobalLogger().Debug(format, args...)
}

func Trace(format string, args ...interface{}) {
	GetGlobalLogger().Trace(format, args...)
}

// SetGlobalLevel sets the global logging level
func SetGlobalLevel(level LogLevel) {
	GetGlobalLogger().SetLevel(level)
}

// GetGlobalLevel returns the global logging level
func GetGlobalLevel() LogLevel {
	return GetGlobalLogger().GetLevel()
}

// Component-specific loggers
var componentLoggers = make(map[string]*Logger)
var componentMutex sync.RWMutex

// GetComponentLogger returns a logger for a specific component
func GetComponentLogger(component string) *Logger {
	componentMutex.RLock()
	logger, exists := componentLoggers[component]
	componentMutex.RUnlock()
	
	if exists {
		return logger
	}
	
	componentMutex.Lock()
	defer componentMutex.Unlock()
	
	// Double-check after acquiring write lock
	if logger, exists := componentLoggers[component]; exists {
		return logger
	}
	
	// Create new component logger with same config as global logger
	globalConfig := &Config{
		Level:      GetGlobalLevel(),
		Output:     os.Stdout,
		TimeFormat: "2006/01/02 15:04:05.000000",
	}
	
	logger = NewLogger(component, globalConfig)
	componentLoggers[component] = logger
	
	return logger
}

// SetComponentLevel sets the logging level for a specific component
func SetComponentLevel(component string, level LogLevel) {
	logger := GetComponentLogger(component)
	logger.SetLevel(level)
}

// DisableStandardLogging disables Go's standard log package output
func DisableStandardLogging() {
	log.SetOutput(io.Discard)
}

// EnableStandardLogging re-enables Go's standard log package output
func EnableStandardLogging() {
	log.SetOutput(os.Stderr)
}