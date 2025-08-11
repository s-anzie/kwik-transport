package filetransfer

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ServerConfiguration represents the complete server configuration
type ServerConfiguration struct {
	Server struct {
		Address          string `yaml:"address" env:"KWIK_SERVER_ADDRESS"`
		FileDirectory    string `yaml:"file_directory" env:"KWIK_FILE_DIRECTORY"`
		SecondaryAddress string `yaml:"secondary_address,omitempty" env:"KWIK_SECONDARY_ADDRESS"`
		TLSCertFile      string `yaml:"tls_cert_file,omitempty" env:"KWIK_TLS_CERT_FILE"`
		TLSKeyFile       string `yaml:"tls_key_file,omitempty" env:"KWIK_TLS_KEY_FILE"`
	} `yaml:"server"`

	Limits struct {
		MaxFileSize       int64    `yaml:"max_file_size" env:"KWIK_MAX_FILE_SIZE"`
		MaxConcurrent     int      `yaml:"max_concurrent" env:"KWIK_MAX_CONCURRENT"`
		AllowedExtensions []string `yaml:"allowed_extensions,omitempty" env:"KWIK_ALLOWED_EXTENSIONS"`
		MaxTransferRate   int64    `yaml:"max_transfer_rate,omitempty" env:"KWIK_MAX_TRANSFER_RATE"`
		SessionTimeout    string   `yaml:"session_timeout" env:"KWIK_SESSION_TIMEOUT"`
	} `yaml:"limits"`

	Performance struct {
		ChunkSize         int32    `yaml:"chunk_size" env:"KWIK_CHUNK_SIZE"`
		SecondaryPaths    []string `yaml:"secondary_paths,omitempty" env:"KWIK_SECONDARY_PATHS"`
		BufferSize        int      `yaml:"buffer_size" env:"KWIK_BUFFER_SIZE"`
		MaxRetries        int      `yaml:"max_retries" env:"KWIK_MAX_RETRIES"`
		RetryDelay        string   `yaml:"retry_delay" env:"KWIK_RETRY_DELAY"`
	} `yaml:"performance"`

	Security struct {
		RequireAuth       bool     `yaml:"require_auth" env:"KWIK_REQUIRE_AUTH"`
		AllowedClients    []string `yaml:"allowed_clients,omitempty" env:"KWIK_ALLOWED_CLIENTS"`
		DenyTraversal     bool     `yaml:"deny_traversal" env:"KWIK_DENY_TRAVERSAL"`
		LogLevel          string   `yaml:"log_level" env:"KWIK_LOG_LEVEL"`
	} `yaml:"security"`
}

// DefaultServerConfiguration returns a secure default configuration
func DefaultServerConfiguration() *ServerConfiguration {
	config := &ServerConfiguration{}
	
	// Server defaults
	config.Server.Address = "localhost:8080"
	config.Server.FileDirectory = "./data"
	
	// Limits defaults (secure)
	config.Limits.MaxFileSize = 1024 * 1024 * 1024 // 1GB
	config.Limits.MaxConcurrent = 10
	config.Limits.AllowedExtensions = []string{".txt", ".pdf", ".zip", ".tar.gz", ".json", ".xml"}
	config.Limits.MaxTransferRate = 0 // No limit by default
	config.Limits.SessionTimeout = "30m"
	
	// Performance defaults
	config.Performance.ChunkSize = 64 * 1024 // 64KB
	config.Performance.BufferSize = 1024 * 1024 // 1MB
	config.Performance.MaxRetries = 3
	config.Performance.RetryDelay = "1s"
	
	// Security defaults (secure by default)
	config.Security.RequireAuth = true
	config.Security.DenyTraversal = true
	config.Security.LogLevel = "info"
	
	return config
}

// LoadConfiguration loads configuration from file and environment variables
func LoadConfiguration(configPath string) (*ServerConfiguration, error) {
	config := DefaultServerConfiguration()
	
	// Load from file if provided
	if configPath != "" {
		if err := loadFromFile(config, configPath); err != nil {
			return nil, fmt.Errorf("failed to load config file: %w", err)
		}
	}
	
	// Override with environment variables
	if err := loadFromEnvironment(config); err != nil {
		return nil, fmt.Errorf("failed to load environment variables: %w", err)
	}
	
	// Validate the final configuration
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("configuration validation failed: %w", err)
	}
	
	return config, nil
}

// loadFromFile loads configuration from a YAML file
func loadFromFile(config *ServerConfiguration, configPath string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}
	
	if err := yaml.Unmarshal(data, config); err != nil {
		return fmt.Errorf("failed to parse YAML config: %w", err)
	}
	
	return nil
}

// loadFromEnvironment loads configuration from environment variables
func loadFromEnvironment(config *ServerConfiguration) error {
	// Server configuration
	if addr := os.Getenv("KWIK_SERVER_ADDRESS"); addr != "" {
		config.Server.Address = addr
	}
	if dir := os.Getenv("KWIK_FILE_DIRECTORY"); dir != "" {
		config.Server.FileDirectory = dir
	}
	if secAddr := os.Getenv("KWIK_SECONDARY_ADDRESS"); secAddr != "" {
		config.Server.SecondaryAddress = secAddr
	}
	if certFile := os.Getenv("KWIK_TLS_CERT_FILE"); certFile != "" {
		config.Server.TLSCertFile = certFile
	}
	if keyFile := os.Getenv("KWIK_TLS_KEY_FILE"); keyFile != "" {
		config.Server.TLSKeyFile = keyFile
	}
	
	// Limits configuration
	if maxSize := os.Getenv("KWIK_MAX_FILE_SIZE"); maxSize != "" {
		if size, err := strconv.ParseInt(maxSize, 10, 64); err == nil {
			config.Limits.MaxFileSize = size
		}
	}
	if maxConcurrent := os.Getenv("KWIK_MAX_CONCURRENT"); maxConcurrent != "" {
		if concurrent, err := strconv.Atoi(maxConcurrent); err == nil {
			config.Limits.MaxConcurrent = concurrent
		}
	}
	if extensions := os.Getenv("KWIK_ALLOWED_EXTENSIONS"); extensions != "" {
		config.Limits.AllowedExtensions = strings.Split(extensions, ",")
	}
	if maxRate := os.Getenv("KWIK_MAX_TRANSFER_RATE"); maxRate != "" {
		if rate, err := strconv.ParseInt(maxRate, 10, 64); err == nil {
			config.Limits.MaxTransferRate = rate
		}
	}
	if timeout := os.Getenv("KWIK_SESSION_TIMEOUT"); timeout != "" {
		config.Limits.SessionTimeout = timeout
	}
	
	// Performance configuration
	if chunkSize := os.Getenv("KWIK_CHUNK_SIZE"); chunkSize != "" {
		if size, err := strconv.ParseInt(chunkSize, 10, 32); err == nil {
			config.Performance.ChunkSize = int32(size)
		}
	}
	if paths := os.Getenv("KWIK_SECONDARY_PATHS"); paths != "" {
		config.Performance.SecondaryPaths = strings.Split(paths, ",")
	}
	if bufferSize := os.Getenv("KWIK_BUFFER_SIZE"); bufferSize != "" {
		if size, err := strconv.Atoi(bufferSize); err == nil {
			config.Performance.BufferSize = size
		}
	}
	if maxRetries := os.Getenv("KWIK_MAX_RETRIES"); maxRetries != "" {
		if retries, err := strconv.Atoi(maxRetries); err == nil {
			config.Performance.MaxRetries = retries
		}
	}
	if retryDelay := os.Getenv("KWIK_RETRY_DELAY"); retryDelay != "" {
		config.Performance.RetryDelay = retryDelay
	}
	
	// Security configuration
	if requireAuth := os.Getenv("KWIK_REQUIRE_AUTH"); requireAuth != "" {
		if auth, err := strconv.ParseBool(requireAuth); err == nil {
			config.Security.RequireAuth = auth
		}
	}
	if clients := os.Getenv("KWIK_ALLOWED_CLIENTS"); clients != "" {
		config.Security.AllowedClients = strings.Split(clients, ",")
	}
	if denyTraversal := os.Getenv("KWIK_DENY_TRAVERSAL"); denyTraversal != "" {
		if deny, err := strconv.ParseBool(denyTraversal); err == nil {
			config.Security.DenyTraversal = deny
		}
	}
	if logLevel := os.Getenv("KWIK_LOG_LEVEL"); logLevel != "" {
		config.Security.LogLevel = logLevel
	}
	
	return nil
}

// Validate validates the configuration and returns an error if invalid
func (c *ServerConfiguration) Validate() error {
	// Validate server configuration
	if c.Server.Address == "" {
		return fmt.Errorf("server address cannot be empty")
	}
	
	if c.Server.FileDirectory == "" {
		return fmt.Errorf("file directory cannot be empty")
	}
	
	// Validate file directory exists and is accessible
	if err := c.validateFileDirectory(); err != nil {
		return err
	}
	
	// Validate TLS configuration if provided
	if err := c.validateTLSConfig(); err != nil {
		return err
	}
	
	// Validate limits
	if c.Limits.MaxFileSize <= 0 {
		return fmt.Errorf("max file size must be positive")
	}
	
	if c.Limits.MaxConcurrent <= 0 {
		return fmt.Errorf("max concurrent connections must be positive")
	}
	
	// Validate session timeout
	if _, err := time.ParseDuration(c.Limits.SessionTimeout); err != nil {
		return fmt.Errorf("invalid session timeout format: %w", err)
	}
	
	// Validate performance settings
	if c.Performance.ChunkSize <= 0 {
		return fmt.Errorf("chunk size must be positive")
	}
	
	if c.Performance.BufferSize <= 0 {
		return fmt.Errorf("buffer size must be positive")
	}
	
	if c.Performance.MaxRetries < 0 {
		return fmt.Errorf("max retries cannot be negative")
	}
	
	// Validate retry delay
	if _, err := time.ParseDuration(c.Performance.RetryDelay); err != nil {
		return fmt.Errorf("invalid retry delay format: %w", err)
	}
	
	// Validate security settings
	validLogLevels := map[string]bool{
		"debug": true, "info": true, "warn": true, "error": true,
	}
	if !validLogLevels[c.Security.LogLevel] {
		return fmt.Errorf("invalid log level: %s (must be debug, info, warn, or error)", c.Security.LogLevel)
	}
	
	// Validate allowed extensions format
	for _, ext := range c.Limits.AllowedExtensions {
		if !strings.HasPrefix(ext, ".") {
			return fmt.Errorf("file extension must start with dot: %s", ext)
		}
	}
	
	return nil
}

// validateFileDirectory validates the file directory configuration
func (c *ServerConfiguration) validateFileDirectory() error {
	// Check if directory exists
	info, err := os.Stat(c.Server.FileDirectory)
	if os.IsNotExist(err) {
		return fmt.Errorf("file directory does not exist: %s", c.Server.FileDirectory)
	}
	if err != nil {
		return fmt.Errorf("cannot access file directory: %w", err)
	}
	
	// Check if it's actually a directory
	if !info.IsDir() {
		return fmt.Errorf("file directory path is not a directory: %s", c.Server.FileDirectory)
	}
	
	// Check if directory is readable
	testFile := filepath.Join(c.Server.FileDirectory, ".kwik-test")
	if file, err := os.Create(testFile); err == nil {
		file.Close()
		os.Remove(testFile)
	} else {
		return fmt.Errorf("file directory is not writable: %s", c.Server.FileDirectory)
	}
	
	return nil
}

// validateTLSConfig validates TLS certificate configuration
func (c *ServerConfiguration) validateTLSConfig() error {
	// If one TLS file is specified, both must be specified
	hasCert := c.Server.TLSCertFile != ""
	hasKey := c.Server.TLSKeyFile != ""
	
	if hasCert != hasKey {
		return fmt.Errorf("both TLS certificate and key files must be specified together")
	}
	
	// If TLS is configured, validate files exist
	if hasCert {
		if _, err := os.Stat(c.Server.TLSCertFile); os.IsNotExist(err) {
			return fmt.Errorf("TLS certificate file does not exist: %s", c.Server.TLSCertFile)
		}
		if _, err := os.Stat(c.Server.TLSKeyFile); os.IsNotExist(err) {
			return fmt.Errorf("TLS key file does not exist: %s", c.Server.TLSKeyFile)
		}
	}
	
	return nil
}

// IsFileAllowed checks if a file extension is allowed
func (c *ServerConfiguration) IsFileAllowed(filename string) bool {
	if len(c.Limits.AllowedExtensions) == 0 {
		return true // No restrictions
	}
	
	ext := strings.ToLower(filepath.Ext(filename))
	for _, allowedExt := range c.Limits.AllowedExtensions {
		if strings.ToLower(allowedExt) == ext {
			return true
		}
	}
	
	return false
}

// GetSessionTimeout returns the parsed session timeout duration
func (c *ServerConfiguration) GetSessionTimeout() time.Duration {
	duration, _ := time.ParseDuration(c.Limits.SessionTimeout)
	return duration
}

// GetRetryDelay returns the parsed retry delay duration
func (c *ServerConfiguration) GetRetryDelay() time.Duration {
	duration, _ := time.ParseDuration(c.Performance.RetryDelay)
	return duration
}

// SaveToFile saves the current configuration to a YAML file
func (c *ServerConfiguration) SaveToFile(configPath string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to marshal configuration: %w", err)
	}
	
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}
	
	return nil
}