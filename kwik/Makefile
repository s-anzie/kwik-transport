# KWIK - QUIC With Intelligent Konnections
# Makefile for building and running examples

.PHONY: all build test clean examples server client help

# Default target
all: build

# Build the project
build:
	@echo "Building KWIK..."
	go build ./pkg/...
	go build ./internal/...

# Run tests
test:
	@echo "Running tests..."
	go test ./...

# Run tests with verbose output
test-verbose:
	@echo "Running tests with verbose output..."
	go test -v ./...

# Run integration tests
test-integration:
	@echo "Running integration tests..."
	go test -tags=integration ./...

# Run benchmarks
benchmark:
	@echo "Running benchmarks..."
	go test -bench=. ./...

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	go clean ./...

# Build examples
examples: build-examples

build-examples:
	@echo "Building examples..."
	go build -o bin/kwik-server ./examples/server
	go build -o bin/kwik-client ./examples/client

# Build test examples
build-test-examples:
	@echo "Building test examples..."
	go build -o bin/basic-connectivity-test ./examples/tests/basic_connectivity_test.go
	go build -o bin/multipath-test ./examples/tests/multipath_test.go
	go build -o bin/performance-test ./examples/tests/performance_test.go

# Build use case examples
build-use-cases:
	@echo "Building use case examples..."
	cd examples/use-cases && make all

# Run test examples
test-basic: build-test-examples
	@echo "Running basic connectivity test..."
	./bin/basic-connectivity-test

test-multipath: build-test-examples
	@echo "Running multi-path test..."
	./bin/multipath-test

test-performance: build-test-examples
	@echo "Running performance test..."
	./bin/performance-test

# Run all test examples
test-examples: build-test-examples
	@echo "Running all KWIK test examples..."
	@echo "================================="
	@echo ""
	@echo "1. Basic Connectivity Test"
	@echo "--------------------------"
	./bin/basic-connectivity-test || echo "❌ Basic connectivity test failed"
	@echo ""
	@echo "2. Multi-Path Test"
	@echo "------------------"
	./bin/multipath-test || echo "❌ Multi-path test failed"
	@echo ""
	@echo "3. Performance Test"
	@echo "-------------------"
	./bin/performance-test || echo "❌ Performance test failed"
	@echo ""
	@echo "All test examples completed!"

# Run server example
server: build-examples
	@echo "Starting KWIK server on localhost:4433..."
	./bin/kwik-server

# Run client example
client: build-examples
	@echo "Starting KWIK client connecting to localhost:4433..."
	./bin/kwik-client

# Run client with custom server address
client-addr: build-examples
	@echo "Starting KWIK client connecting to $(ADDR)..."
	./bin/kwik-client $(ADDR)

# Run server with custom address
server-addr: build-examples
	@echo "Starting KWIK server on $(ADDR)..."
	./bin/kwik-server $(ADDR)

# Format code
fmt:
	@echo "Formatting code..."
	go fmt ./...

# Lint code
lint:
	@echo "Linting code..."
	golangci-lint run

# Generate protobuf code
proto:
	@echo "Generating protobuf code..."
	protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		proto/common/common.proto \
		proto/control/control.proto \
		proto/data/data.proto

# Install dependencies
deps:
	@echo "Installing dependencies..."
	go mod download
	go mod tidy

# Update dependencies
update-deps:
	@echo "Updating dependencies..."
	go get -u ./...
	go mod tidy

# Create binary directory
bin:
	mkdir -p bin

# Install tools
install-tools:
	@echo "Installing development tools..."
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# Development setup
dev-setup: install-tools deps
	@echo "Development environment setup complete!"

# Demo: Run server and client in parallel (requires tmux)
demo:
	@echo "Starting KWIK demo..."
	@echo "This will start server and client in separate tmux panes"
	tmux new-session -d -s kwik-demo 'make server'
	tmux split-window -h 'sleep 2 && make client'
	tmux attach-session -t kwik-demo

# Kill demo session
kill-demo:
	tmux kill-session -t kwik-demo 2>/dev/null || true

# Show help
help:
	@echo "KWIK - QUIC With Intelligent Konnections"
	@echo "========================================"
	@echo ""
	@echo "Available targets:"
	@echo "  build          - Build the project"
	@echo "  test           - Run tests"
	@echo "  test-verbose   - Run tests with verbose output"
	@echo "  test-integration - Run integration tests"
	@echo "  benchmark      - Run benchmarks"
	@echo "  clean          - Clean build artifacts"
	@echo "  examples       - Build example applications"
	@echo "  build-test-examples - Build test example applications"
	@echo "  build-use-cases - Build focused use case examples"
	@echo "  test-examples  - Run all test examples"
	@echo "  test-basic     - Run basic connectivity test"
	@echo "  test-multipath - Run multi-path test"
	@echo "  test-performance - Run performance test"
	@echo "  server         - Run server example on localhost:4433"
	@echo "  client         - Run client example connecting to localhost:4433"
	@echo "  server-addr    - Run server on custom address (use ADDR=host:port)"
	@echo "  client-addr    - Run client connecting to custom address (use ADDR=host:port)"
	@echo "  fmt            - Format code"
	@echo "  lint           - Lint code"
	@echo "  proto          - Generate protobuf code"
	@echo "  deps           - Install dependencies"
	@echo "  update-deps    - Update dependencies"
	@echo "  install-tools  - Install development tools"
	@echo "  dev-setup      - Set up development environment"
	@echo "  demo           - Run interactive demo (requires tmux)"
	@echo "  kill-demo      - Kill demo session"
	@echo "  help           - Show this help"
	@echo ""
	@echo "Examples:"
	@echo "  make server                    # Start server on localhost:4433"
	@echo "  make client                    # Connect client to localhost:4433"
	@echo "  make server-addr ADDR=:8080    # Start server on port 8080"
	@echo "  make client-addr ADDR=server:8080  # Connect to server:8080"
	@echo "  make demo                      # Interactive demo with tmux"