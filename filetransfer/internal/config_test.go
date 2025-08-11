package filetransfer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultServerConfiguration(t *testing.T) {
	config := DefaultServerConfiguration()
	
	// Test server defaults
	if config.Server.Address != "localhost:8080" {
		t.Errorf("Expected default address 'localhost:8080', got '%s'", config.Server.Address)
	}
	
	if config.Server.FileDirectory != "./data" {
		t.Errorf("Expected default file directory './data', got '%s'", config.Server.FileDirectory)
	}
	
	// Test limits defaults
	if config.Limits.MaxFileSize != 1024*1024*1024 {
		t.Errorf("Expected default max file size 1GB, got %d", config.Limits.MaxFileSize)
	}
	
	if config.Limits.MaxConcurrent != 10 {
		t.Errorf("Expected default max concurrent 10, got %d", config.Limits.MaxConcurrent)
	}
	
	// Test performance defaults
	if config.Performance.ChunkSize != 64*1024 {
		t.Errorf("Expected default chunk size 64KB, got %d", config.Performance.ChunkSize)
	}
	
	// Test security defaults
	if !config.Security.RequireAuth {
		t.Error("Expected default require auth to be true")
	}
	
	if !config.Security.DenyTraversal {
		t.Error("Expected default deny traversal to be true")
	}
	
	if config.Security.LogLevel != "info" {
		t.Errorf("Expected default log level 'info', got '%s'", config.Security.LogLevel)
	}
}

func TestLoadConfigurationFromFile(t *testing.T) {
	// Create a temporary config file
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "test-config.yaml")
	
	configContent := `
server:
  address: "test:9090"
  file_directory: "/tmp/test"
  secondary_address: "test:9091"

limits:
  max_file_size: 2147483648
  max_concurrent: 20
  allowed_extensions:
    - ".test"
    - ".example"
  session_timeout: "60m"

performance:
  chunk_size: 32768
  buffer_size: 2097152
  max_retries: 5
  retry_delay: "2s"

security:
  require_auth: false
  deny_traversal: false
  log_level: "debug"
`
	
	if err := os.WriteFile(configFile, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to create test config file: %v", err)
	}
	
	// Create the test directory
	testDir := "/tmp/test"
	if err := os.MkdirAll(testDir, 0755); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}
	defer os.RemoveAll(testDir)
	
	config, err := LoadConfiguration(configFile)
	if err != nil {
		t.Fatalf("Failed to load configuration: %v", err)
	}
	
	// Verify loaded values
	if config.Server.Address != "test:9090" {
		t.Errorf("Expected address 'test:9090', got '%s'", config.Server.Address)
	}
	
	if config.Server.FileDirectory != "/tmp/test" {
		t.Errorf("Expected file directory '/tmp/test', got '%s'", config.Server.FileDirectory)
	}
	
	if config.Server.SecondaryAddress != "test:9091" {
		t.Errorf("Expected secondary address 'test:9091', got '%s'", config.Server.SecondaryAddress)
	}
	
	if config.Limits.MaxFileSize != 2147483648 {
		t.Errorf("Expected max file size 2GB, got %d", config.Limits.MaxFileSize)
	}
	
	if config.Performance.ChunkSize != 32768 {
		t.Errorf("Expected chunk size 32KB, got %d", config.Performance.ChunkSize)
	}
	
	if config.Security.RequireAuth {
		t.Error("Expected require auth to be false")
	}
	
	if config.Security.LogLevel != "debug" {
		t.Errorf("Expected log level 'debug', got '%s'", config.Security.LogLevel)
	}
}

func TestLoadConfigurationFromEnvironment(t *testing.T) {
	// Set environment variables
	envVars := map[string]string{
		"KWIK_SERVER_ADDRESS":      "env:8888",
		"KWIK_FILE_DIRECTORY":      "/tmp/env-test",
		"KWIK_SECONDARY_ADDRESS":   "env:8889",
		"KWIK_MAX_FILE_SIZE":       "5368709120", // 5GB
		"KWIK_MAX_CONCURRENT":      "25",
		"KWIK_ALLOWED_EXTENSIONS":  ".env,.test",
		"KWIK_CHUNK_SIZE":          "131072", // 128KB
		"KWIK_REQUIRE_AUTH":        "false",
		"KWIK_LOG_LEVEL":           "warn",
	}
	
	// Set environment variables
	for key, value := range envVars {
		if err := os.Setenv(key, value); err != nil {
			t.Fatalf("Failed to set environment variable %s: %v", key, err)
		}
	}
	
	// Clean up environment variables after test
	defer func() {
		for key := range envVars {
			os.Unsetenv(key)
		}
	}()
	
	// Create the test directory
	testDir := "/tmp/env-test"
	if err := os.MkdirAll(testDir, 0755); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}
	defer os.RemoveAll(testDir)
	
	config, err := LoadConfiguration("")
	if err != nil {
		t.Fatalf("Failed to load configuration from environment: %v", err)
	}
	
	// Verify environment values override defaults
	if config.Server.Address != "env:8888" {
		t.Errorf("Expected address 'env:8888', got '%s'", config.Server.Address)
	}
	
	if config.Server.FileDirectory != "/tmp/env-test" {
		t.Errorf("Expected file directory '/tmp/env-test', got '%s'", config.Server.FileDirectory)
	}
	
	if config.Limits.MaxFileSize != 5368709120 {
		t.Errorf("Expected max file size 5GB, got %d", config.Limits.MaxFileSize)
	}
	
	if config.Limits.MaxConcurrent != 25 {
		t.Errorf("Expected max concurrent 25, got %d", config.Limits.MaxConcurrent)
	}
	
	expectedExtensions := []string{".env", ".test"}
	if len(config.Limits.AllowedExtensions) != len(expectedExtensions) {
		t.Errorf("Expected %d extensions, got %d", len(expectedExtensions), len(config.Limits.AllowedExtensions))
	}
	
	if config.Performance.ChunkSize != 131072 {
		t.Errorf("Expected chunk size 128KB, got %d", config.Performance.ChunkSize)
	}
	
	if config.Security.RequireAuth {
		t.Error("Expected require auth to be false from environment")
	}
	
	if config.Security.LogLevel != "warn" {
		t.Errorf("Expected log level 'warn', got '%s'", config.Security.LogLevel)
	}
}

func TestConfigurationValidation(t *testing.T) {
	tests := []struct {
		name        string
		modifyFunc  func(*ServerConfiguration)
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid configuration",
			modifyFunc: func(c *ServerConfiguration) {
				// Create a valid test directory
				testDir := "/tmp/valid-test"
				os.MkdirAll(testDir, 0755)
				c.Server.FileDirectory = testDir
			},
			expectError: false,
		},
		{
			name: "empty server address",
			modifyFunc: func(c *ServerConfiguration) {
				c.Server.Address = ""
			},
			expectError: true,
			errorMsg:    "server address cannot be empty",
		},
		{
			name: "empty file directory",
			modifyFunc: func(c *ServerConfiguration) {
				c.Server.FileDirectory = ""
			},
			expectError: true,
			errorMsg:    "file directory cannot be empty",
		},
		{
			name: "non-existent file directory",
			modifyFunc: func(c *ServerConfiguration) {
				c.Server.FileDirectory = "/non/existent/directory"
			},
			expectError: true,
			errorMsg:    "file directory does not exist",
		},
		{
			name: "negative max file size",
			modifyFunc: func(c *ServerConfiguration) {
				testDir := "/tmp/negative-test"
				os.MkdirAll(testDir, 0755)
				c.Server.FileDirectory = testDir
				c.Limits.MaxFileSize = -1
			},
			expectError: true,
			errorMsg:    "max file size must be positive",
		},
		{
			name: "zero max concurrent",
			modifyFunc: func(c *ServerConfiguration) {
				testDir := "/tmp/zero-test"
				os.MkdirAll(testDir, 0755)
				c.Server.FileDirectory = testDir
				c.Limits.MaxConcurrent = 0
			},
			expectError: true,
			errorMsg:    "max concurrent connections must be positive",
		},
		{
			name: "invalid session timeout",
			modifyFunc: func(c *ServerConfiguration) {
				testDir := "/tmp/timeout-test"
				os.MkdirAll(testDir, 0755)
				c.Server.FileDirectory = testDir
				c.Limits.SessionTimeout = "invalid"
			},
			expectError: true,
			errorMsg:    "invalid session timeout format",
		},
		{
			name: "invalid log level",
			modifyFunc: func(c *ServerConfiguration) {
				testDir := "/tmp/log-test"
				os.MkdirAll(testDir, 0755)
				c.Server.FileDirectory = testDir
				c.Security.LogLevel = "invalid"
			},
			expectError: true,
			errorMsg:    "invalid log level",
		},
		{
			name: "invalid file extension format",
			modifyFunc: func(c *ServerConfiguration) {
				testDir := "/tmp/ext-test"
				os.MkdirAll(testDir, 0755)
				c.Server.FileDirectory = testDir
				c.Limits.AllowedExtensions = []string{"txt", ".pdf"} // Missing dot
			},
			expectError: true,
			errorMsg:    "file extension must start with dot",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := DefaultServerConfiguration()
			tt.modifyFunc(config)
			
			err := config.Validate()
			
			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error containing '%s', but got no error", tt.errorMsg)
				} else if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("Expected error containing '%s', got '%s'", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error, got: %v", err)
				}
			}
		})
	}
}

func TestIsFileAllowed(t *testing.T) {
	config := DefaultServerConfiguration()
	config.Limits.AllowedExtensions = []string{".txt", ".pdf", ".json"}
	
	tests := []struct {
		filename string
		expected bool
	}{
		{"test.txt", true},
		{"document.pdf", true},
		{"data.json", true},
		{"script.sh", false},
		{"image.png", false},
		{"test.TXT", true}, // Case insensitive
		{"document.PDF", true},
		{"noextension", false},
	}
	
	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			result := config.IsFileAllowed(tt.filename)
			if result != tt.expected {
				t.Errorf("IsFileAllowed(%s) = %v, expected %v", tt.filename, result, tt.expected)
			}
		})
	}
	
	// Test with no restrictions (empty allowed extensions)
	config.Limits.AllowedExtensions = []string{}
	if !config.IsFileAllowed("any.file") {
		t.Error("Expected all files to be allowed when no extensions are specified")
	}
}

func TestGetSessionTimeout(t *testing.T) {
	config := DefaultServerConfiguration()
	config.Limits.SessionTimeout = "45m"
	
	timeout := config.GetSessionTimeout()
	expected := 45 * time.Minute
	
	if timeout != expected {
		t.Errorf("Expected timeout %v, got %v", expected, timeout)
	}
}

func TestGetRetryDelay(t *testing.T) {
	config := DefaultServerConfiguration()
	config.Performance.RetryDelay = "5s"
	
	delay := config.GetRetryDelay()
	expected := 5 * time.Second
	
	if delay != expected {
		t.Errorf("Expected delay %v, got %v", expected, delay)
	}
}

func TestSaveToFile(t *testing.T) {
	config := DefaultServerConfiguration()
	
	// Create a temporary directory for the test
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "test-save.yaml")
	
	// Save configuration to file
	if err := config.SaveToFile(configFile); err != nil {
		t.Fatalf("Failed to save configuration: %v", err)
	}
	
	// Verify file was created
	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		t.Error("Configuration file was not created")
	}
	
	// Load the saved configuration and verify it matches
	loadedConfig, err := LoadConfiguration(configFile)
	if err != nil {
		t.Fatalf("Failed to load saved configuration: %v", err)
	}
	
	// Compare key values
	if loadedConfig.Server.Address != config.Server.Address {
		t.Errorf("Saved/loaded address mismatch: %s != %s", loadedConfig.Server.Address, config.Server.Address)
	}
	
	if loadedConfig.Limits.MaxFileSize != config.Limits.MaxFileSize {
		t.Errorf("Saved/loaded max file size mismatch: %d != %d", loadedConfig.Limits.MaxFileSize, config.Limits.MaxFileSize)
	}
}