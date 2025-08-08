# KWIK Makefile

.PHONY: proto build test clean

# Generate protobuf code
proto:
	@echo "Generating protobuf code..."
	@mkdir -p proto/control proto/data proto/common
	protoc --go_out=proto/control --go_opt=paths=source_relative proto/control.proto
	protoc --go_out=proto/data --go_opt=paths=source_relative proto/data.proto
	protoc --go_out=proto/common --go_opt=paths=source_relative proto/common.proto

# Build the project
build:
	@echo "Building KWIK..."
	go build -v ./...

# Run tests
test:
	@echo "Running tests..."
	go test -v ./...

# Run tests with coverage
test-coverage:
	@echo "Running tests with coverage..."
	go test -v -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# Clean build artifacts
clean:
	@echo "Cleaning..."
	go clean ./...
	rm -f coverage.out coverage.html

# Format code
fmt:
	@echo "Formatting code..."
	go fmt ./...

# Run linter
lint:
	@echo "Running linter..."
	golangci-lint run

# Install dependencies
deps:
	@echo "Installing dependencies..."
	go mod download
	go mod tidy

# Run examples
run-client:
	@echo "Running client example..."
	go run examples/client/main.go

# Development setup
dev-setup: deps proto build

# Full CI pipeline
ci: fmt lint test build