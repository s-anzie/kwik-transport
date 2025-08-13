# FTPA Makefile
# Fast Transfer Protocol Application

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod

# Build parameters
BINARY_DIR=bin
CLIENT_BINARY=$(BINARY_DIR)/ftpa-client
PRIMARY_SERVER_BINARY=$(BINARY_DIR)/ftpa-server-primary
SECONDARY_SERVER_BINARY=$(BINARY_DIR)/ftpa-server-secondary

# Source directories
CLIENT_SOURCE=./cmd/ftpa-client
PRIMARY_SERVER_SOURCE=./cmd/ftpa-server/primary
SECONDARY_SERVER_SOURCE=./cmd/ftpa-server/secondary

# Test parameters
TEST_TIMEOUT=30s
COVERAGE_FILE=coverage.out

# Demo parameters
DATA_DIR=./data
DOWNLOADS_DIR=./downloads
CONFIG_FILE=./config/server.yaml

.PHONY: all build clean test coverage demo help install deps lint format

# Default target
all: clean deps build test

# Build all binaries
build: $(CLIENT_BINARY) $(PRIMARY_SERVER_BINARY) $(SECONDARY_SERVER_BINARY)

# Build client
$(CLIENT_BINARY): $(CLIENT_SOURCE)/*.go
	@echo "Building FTPA client..."
	@mkdir -p $(BINARY_DIR)
	$(GOBUILD) -o $(CLIENT_BINARY) $(CLIENT_SOURCE)
	@echo "✅ Client built: $(CLIENT_BINARY)"

# Build primary server
$(PRIMARY_SERVER_BINARY): $(PRIMARY_SERVER_SOURCE)/*.go
	@echo "Building FTPA primary server..."
	@mkdir -p $(BINARY_DIR)
	$(GOBUILD) -o $(PRIMARY_SERVER_BINARY) $(PRIMARY_SERVER_SOURCE)
	@echo "✅ Primary server built: $(PRIMARY_SERVER_BINARY)"

# Build secondary server
$(SECONDARY_SERVER_BINARY): $(SECONDARY_SERVER_SOURCE)/*.go
	@echo "Building FTPA secondary server..."
	@mkdir -p $(BINARY_DIR)
	$(GOBUILD) -o $(SECONDARY_SERVER_BINARY) $(SECONDARY_SERVER_SOURCE)
	@echo "✅ Secondary server built: $(SECONDARY_SERVER_BINARY)"

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	$(GOCLEAN)
	rm -rf $(BINARY_DIR)
	rm -rf $(DOWNLOADS_DIR)
	rm -f $(COVERAGE_FILE)
	@echo "✅ Clean completed"

# Install dependencies
deps:
	@echo "Installing dependencies..."
	$(GOMOD) tidy
	$(GOMOD) download
	@echo "✅ Dependencies installed"

# Run tests
test:
	@echo "Running tests..."
	$(GOTEST) -timeout $(TEST_TIMEOUT) -v ./...
	@echo "✅ Tests completed"

# Run tests with coverage
coverage:
	@echo "Running tests with coverage..."
	$(GOTEST) -timeout $(TEST_TIMEOUT) -coverprofile=$(COVERAGE_FILE) ./...
	$(GOCMD) tool cover -html=$(COVERAGE_FILE) -o coverage.html
	@echo "✅ Coverage report generated: coverage.html"

# Run integration tests
test-integration:
	@echo "Running integration tests..."
	$(GOTEST) -timeout 60s -tags=integration -v ./...
	@echo "✅ Integration tests completed"

# Run benchmarks
benchmark:
	@echo "Running benchmarks..."
	$(GOTEST) -bench=. -benchmem ./...
	@echo "✅ Benchmarks completed"

# Lint code
lint:
	@echo "Running linter..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "⚠️  golangci-lint not installed. Install with: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
	fi
	@echo "✅ Linting completed"

# Format code
format:
	@echo "Formatting code..."
	$(GOCMD) fmt ./...
	@echo "✅ Code formatted"

# Create test data
test-data:
	@echo "Creating test data..."
	@mkdir -p $(DATA_DIR)
	@echo "Small test file content" > $(DATA_DIR)/small.txt
	@for i in $$(seq 1 1000); do echo "Line $$i: Medium test file content for testing chunk transfers" >> $(DATA_DIR)/medium.txt; done
	@for i in $$(seq 1 10000); do echo "Line $$i: Large test file content for testing multi-path performance" >> $(DATA_DIR)/large.txt; done
	@dd if=/dev/urandom of=$(DATA_DIR)/binary.dat bs=1024 count=512 2>/dev/null
	@echo "✅ Test data created in $(DATA_DIR)/"
	@ls -lh $(DATA_DIR)/

# Run demo
demo: build test-data
	@echo "Running FTPA demo..."
	./demo.sh all

# Run single-path demo
demo-single: build test-data
	@echo "Running single-path demo..."
	./demo.sh single

# Run multi-path demo
demo-multi: build test-data
	@echo "Running multi-path demo..."
	./demo.sh multi

# Install binaries to system
install: build
	@echo "Installing binaries to /usr/local/bin..."
	@sudo cp $(CLIENT_BINARY) /usr/local/bin/
	@sudo cp $(PRIMARY_SERVER_BINARY) /usr/local/bin/
	@sudo cp $(SECONDARY_SERVER_BINARY) /usr/local/bin/
	@echo "✅ Binaries installed"

# Uninstall binaries from system
uninstall:
	@echo "Uninstalling binaries from /usr/local/bin..."
	@sudo rm -f /usr/local/bin/ftpa-client
	@sudo rm -f /usr/local/bin/ftpa-server-primary
	@sudo rm -f /usr/local/bin/ftpa-server-secondary
	@echo "✅ Binaries uninstalled"

# Development build with debug info
build-debug:
	@echo "Building with debug information..."
	@mkdir -p $(BINARY_DIR)
	$(GOBUILD) -gcflags="all=-N -l" -o $(CLIENT_BINARY) $(CLIENT_SOURCE)
	$(GOBUILD) -gcflags="all=-N -l" -o $(PRIMARY_SERVER_BINARY) $(PRIMARY_SERVER_SOURCE)
	$(GOBUILD) -gcflags="all=-N -l" -o $(SECONDARY_SERVER_BINARY) $(SECONDARY_SERVER_SOURCE)
	@echo "✅ Debug builds completed"

# Release build with optimizations
build-release:
	@echo "Building release binaries..."
	@mkdir -p $(BINARY_DIR)
	$(GOBUILD) -ldflags="-s -w" -o $(CLIENT_BINARY) $(CLIENT_SOURCE)
	$(GOBUILD) -ldflags="-s -w" -o $(PRIMARY_SERVER_BINARY) $(PRIMARY_SERVER_SOURCE)
	$(GOBUILD) -ldflags="-s -w" -o $(SECONDARY_SERVER_BINARY) $(SECONDARY_SERVER_SOURCE)
	@echo "✅ Release builds completed"

# Cross-compile for different platforms
build-cross:
	@echo "Cross-compiling for multiple platforms..."
	@mkdir -p $(BINARY_DIR)
	
	# Linux AMD64
	GOOS=linux GOARCH=amd64 $(GOBUILD) -o $(BINARY_DIR)/ftpa-client-linux-amd64 $(CLIENT_SOURCE)
	GOOS=linux GOARCH=amd64 $(GOBUILD) -o $(BINARY_DIR)/ftpa-server-primary-linux-amd64 $(PRIMARY_SERVER_SOURCE)
	GOOS=linux GOARCH=amd64 $(GOBUILD) -o $(BINARY_DIR)/ftpa-server-secondary-linux-amd64 $(SECONDARY_SERVER_SOURCE)
	
	# Windows AMD64
	GOOS=windows GOARCH=amd64 $(GOBUILD) -o $(BINARY_DIR)/ftpa-client-windows-amd64.exe $(CLIENT_SOURCE)
	GOOS=windows GOARCH=amd64 $(GOBUILD) -o $(BINARY_DIR)/ftpa-server-primary-windows-amd64.exe $(PRIMARY_SERVER_SOURCE)
	GOOS=windows GOARCH=amd64 $(GOBUILD) -o $(BINARY_DIR)/ftpa-server-secondary-windows-amd64.exe $(SECONDARY_SERVER_SOURCE)
	
	# macOS AMD64
	GOOS=darwin GOARCH=amd64 $(GOBUILD) -o $(BINARY_DIR)/ftpa-client-darwin-amd64 $(CLIENT_SOURCE)
	GOOS=darwin GOARCH=amd64 $(GOBUILD) -o $(BINARY_DIR)/ftpa-server-primary-darwin-amd64 $(PRIMARY_SERVER_SOURCE)
	GOOS=darwin GOARCH=amd64 $(GOBUILD) -o $(BINARY_DIR)/ftpa-server-secondary-darwin-amd64 $(SECONDARY_SERVER_SOURCE)
	
	# macOS ARM64
	GOOS=darwin GOARCH=arm64 $(GOBUILD) -o $(BINARY_DIR)/ftpa-client-darwin-arm64 $(CLIENT_SOURCE)
	GOOS=darwin GOARCH=arm64 $(GOBUILD) -o $(BINARY_DIR)/ftpa-server-primary-darwin-arm64 $(PRIMARY_SERVER_SOURCE)
	GOOS=darwin GOARCH=arm64 $(GOBUILD) -o $(BINARY_DIR)/ftpa-server-secondary-darwin-arm64 $(SECONDARY_SERVER_SOURCE)
	
	@echo "✅ Cross-compilation completed"
	@ls -la $(BINARY_DIR)/

# Check for security vulnerabilities
security:
	@echo "Checking for security vulnerabilities..."
	@if command -v govulncheck >/dev/null 2>&1; then \
		govulncheck ./...; \
	else \
		echo "⚠️  govulncheck not installed. Install with: go install golang.org/x/vuln/cmd/govulncheck@latest"; \
	fi
	@echo "✅ Security check completed"

# Generate documentation
docs:
	@echo "Generating documentation..."
	@if command -v godoc >/dev/null 2>&1; then \
		echo "📚 Documentation server starting at http://localhost:6060"; \
		godoc -http=:6060; \
	else \
		echo "⚠️  godoc not installed. Install with: go install golang.org/x/tools/cmd/godoc@latest"; \
	fi

# Show project statistics
stats:
	@echo "Project Statistics:"
	@echo "==================="
	@echo "Go files:"
	@find . -name "*.go" -not -path "./vendor/*" | wc -l
	@echo "Lines of code:"
	@find . -name "*.go" -not -path "./vendor/*" -exec wc -l {} + | tail -1
	@echo "Test files:"
	@find . -name "*_test.go" -not -path "./vendor/*" | wc -l
	@echo "Packages:"
	@go list ./... | wc -l

# Show help
help:
	@echo "FTPA Makefile Commands:"
	@echo "======================="
	@echo "Build commands:"
	@echo "  build         - Build all binaries"
	@echo "  build-debug   - Build with debug information"
	@echo "  build-release - Build optimized release binaries"
	@echo "  build-cross   - Cross-compile for multiple platforms"
	@echo ""
	@echo "Test commands:"
	@echo "  test          - Run unit tests"
	@echo "  test-integration - Run integration tests"
	@echo "  coverage      - Run tests with coverage report"
	@echo "  benchmark     - Run benchmarks"
	@echo ""
	@echo "Demo commands:"
	@echo "  demo          - Run complete demo suite"
	@echo "  demo-single   - Run single-path demo"
	@echo "  demo-multi    - Run multi-path demo"
	@echo "  test-data     - Create test data files"
	@echo ""
	@echo "Development commands:"
	@echo "  deps          - Install dependencies"
	@echo "  clean         - Clean build artifacts"
	@echo "  format        - Format code"
	@echo "  lint          - Run linter"
	@echo "  security      - Check for vulnerabilities"
	@echo ""
	@echo "Installation commands:"
	@echo "  install       - Install binaries to system"
	@echo "  uninstall     - Remove binaries from system"
	@echo ""
	@echo "Utility commands:"
	@echo "  docs          - Start documentation server"
	@echo "  stats         - Show project statistics"
	@echo "  help          - Show this help message"