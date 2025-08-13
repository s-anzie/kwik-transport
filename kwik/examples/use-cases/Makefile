# KWIK Use Cases Examples
# Simple, focused examples demonstrating specific KWIK features

.PHONY: all clean help

# Build all use case examples
all: simple-echo multi-path

# Simple Echo Example (complete client-server pair)
simple-echo: bin
	@echo "Building simple echo example..."
	go build -o bin/echo-server ./simple-echo/server/server.go
	go build -o bin/echo-client ./simple-echo/client.go

# Multi-Path Example (primary server, secondary server, and client)
multi-path: bin
	@echo "Building multi-path example..."
	go build -o bin/multipath-primary-server ./multi-path-demo/primary/server.go
	go build -o bin/multipath-secondary-server ./multi-path-demo/secondary/server.go
	go build -o bin/multipath-client ./multi-path-demo/client.go

# Run Examples - Each use case has both client and server
run-echo-server: simple-echo
	@echo "Starting echo server on localhost:4433..."
	./bin/echo-server

run-echo-client: simple-echo
	@echo "Running echo client..."
	./bin/echo-client

run-multipath-primary-server: multi-path
	@echo "Starting multi-path PRIMARY server on localhost:4433..."
	./bin/multipath-primary-server

run-multipath-secondary-server: multi-path
	@echo "Starting multi-path SECONDARY server on localhost:4434..."
	./bin/multipath-secondary-server

run-multipath-client: multi-path
	@echo "Running multi-path client..."
	./bin/multipath-client

# Legacy target for backward compatibility
run-multipath-server: run-multipath-primary-server

# Clean built binaries
clean:
	@echo "Cleaning use case examples..."
	rm -rf bin/

# Create bin directory
bin:
	mkdir -p bin

# Help
help:
	@echo "KWIK Use Cases Examples"
	@echo "======================"
	@echo ""
	@echo "Build targets (each builds complete client-server pair):"
	@echo "  all                    - Build all examples"
	@echo "  simple-echo           - Build simple echo client/server"
	@echo "  multi-path            - Build multi-path demo client/server"
	@echo "  concurrent            - Build concurrent streams client/server"
	@echo "  raw-packets           - Build raw packets client/server"
	@echo ""
	@echo "Run examples (start server first, then client):"
	@echo "  run-echo-server              - Start echo server"
	@echo "  run-echo-client              - Run echo client"
	@echo "  run-multipath-primary-server - Start multi-path PRIMARY server (port 4433)"
	@echo "  run-multipath-secondary-server - Start multi-path SECONDARY server (port 4434)"
	@echo "  run-multipath-client         - Run multi-path client"
	@echo "  run-concurrent-server        - Start concurrent streams server"
	@echo "  run-concurrent-client        - Run concurrent streams client"
	@echo "  run-rawpackets-server        - Start raw packets server"
	@echo "  run-rawpackets-client        - Run raw packets client"
	@echo ""
	@echo "  clean                 - Remove built binaries"
	@echo "  help                  - Show this help"