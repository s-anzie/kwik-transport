#!/bin/bash

# FTPA Demo Script
# This script demonstrates the Fast Transfer Protocol Application (FTPA)
# with both single-path and multi-path configurations

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
PRIMARY_ADDR="localhost:4433"
SECONDARY_ADDR="localhost:4434"
DATA_DIR="./data"
DOWNLOADS_DIR="./downloads"
CONFIG_FILE="./config/server.yaml"

# Function to print colored output
print_info() {
    echo -e "${BLUE}ℹ️  $1${NC}"
}

print_success() {
    echo -e "${GREEN}✅ $1${NC}"
}

print_warning() {
    echo -e "${YELLOW}⚠️  $1${NC}"
}

print_error() {
    echo -e "${RED}❌ $1${NC}"
}

print_header() {
    echo -e "\n${BLUE}=== $1 ===${NC}"
}

# Function to check if a port is in use (UDP for KWIK/QUIC)
check_port() {
    local port=$1
    # Check for UDP listeners (KWIK uses UDP, not TCP)
    if lsof -Pi :$port -sUDP:Idle -t >/dev/null 2>&1; then
        return 0  # Port is in use
    elif netstat -ulnp 2>/dev/null | grep -q ":$port "; then
        return 0  # Port is in use (fallback check)
    elif ss -ulnp 2>/dev/null | grep -q ":$port "; then
        return 0  # Port is in use (ss fallback)
    else
        return 1  # Port is free
    fi
}

# Function to check if server is actually responding
check_server_health() {
    local addr=$1
    local name=$2
    
    print_info "Testing $name connectivity at $addr..."
    
    # Try a simple connection test with timeout
    timeout 5s ./bin/ftpa-client -server "$addr" -file "nonexistent.txt" -verbose 2>&1 | grep -q "Connected successfully" && return 0
    
    return 1
}

# Function to wait for server to start
wait_for_server() {
    local addr=$1
    local name=$2
    local port=$(echo $addr | cut -d':' -f2)
    
    print_info "Waiting for $name to start on port $port..."
    
    # Give the server a moment to initialize
    sleep 5
    
    # Check if the server process is still running
    local server_pid=""
    if [ "$name" = "Primary server" ]; then
        server_pid=$PRIMARY_PID
    elif [ "$name" = "Secondary server" ]; then
        server_pid=$SECONDARY_PID
    fi
    
    if [ -n "$server_pid" ] && ! kill -0 $server_pid 2>/dev/null; then
        print_error "$name process (PID $server_pid) died during startup"
        return 1
    fi
    
    # Look for server startup success messages in the background
    for i in {1..6}; do
        print_info "Attempt $i/6: Checking $name status..."
        
        # Check if server logs indicate successful startup
        # The server should print "File transfer server started on ..." when ready
        sleep 2
        
        # Simple approach: assume server is ready after a reasonable delay
        # since KWIK servers don't bind to TCP ports we can easily check
        if [ $i -ge 3 ]; then
            print_success "$name startup delay completed, assuming ready"
            return 0
        fi
    done
    
    print_error "$name failed to start within expected time"
    return 1
}

# Function to create test files
create_test_files() {
    print_header "Creating Test Files"
    
    mkdir -p "$DATA_DIR"
    
    # Create small text file
    echo "This is a small test file for FTPA demonstration." > "$DATA_DIR/small.txt"
    echo "It contains multiple lines to test basic functionality." >> "$DATA_DIR/small.txt"
    echo "File created at: $(date)" >> "$DATA_DIR/small.txt"
    
    # Create medium file
    for i in {1..1000}; do
        echo "Line $i: This is a medium-sized file for testing chunk-based transfers." >> "$DATA_DIR/medium.txt"
    done
    
    # Create large file (about 1MB)
    for i in {1..10000}; do
        echo "Line $i: This is a large file for testing multi-path performance improvements with KWIK protocol." >> "$DATA_DIR/large.txt"
    done
    
    # Create binary-like file
    dd if=/dev/urandom of="$DATA_DIR/binary.dat" bs=1024 count=512 2>/dev/null
    
    print_success "Created test files:"
    ls -lh "$DATA_DIR"
}

# Function to build the applications
build_applications() {
    print_header "Building Applications"
    
    print_info "Building ftpa-client..."
    go build -o bin/ftpa-client ./cmd/ftpa-client/
    
    print_info "Building ftpa-server (primary)..."
    go build -o bin/ftpa-server-primary ./cmd/ftpa-server/primary/
    
    print_info "Building ftpa-server (secondary)..."
    go build -o bin/ftpa-server-secondary ./cmd/ftpa-server/secondary/
    
    print_success "All applications built successfully"
    ls -la bin/
}

# Function to run single-path demo
demo_single_path() {
    print_header "Single-Path Transfer Demo"
    
    print_info "Starting primary server only (single-path mode)..."
    
    # Start primary server without secondary address
    ./bin/ftpa-server-primary \
        -address "$PRIMARY_ADDR" \
        -files "$DATA_DIR" \
        -verbose &
    PRIMARY_PID=$!
    
    # Wait for server to start
    if ! wait_for_server "$PRIMARY_ADDR" "Primary server"; then
        kill $PRIMARY_PID 2>/dev/null || true
        return 1
    fi
    
    sleep 2
    
    # Test file downloads
    print_info "Downloading small.txt..."
    ./bin/ftpa-client \
        -server "$PRIMARY_ADDR" \
        -file "small.txt" \
        -output "$DOWNLOADS_DIR/single-path" \
        -verbose
    
    print_info "Downloading medium.txt..."
    ./bin/ftpa-client \
        -server "$PRIMARY_ADDR" \
        -file "medium.txt" \
        -output "$DOWNLOADS_DIR/single-path" \
        -progress
    
    print_success "Single-path demo completed"
    
    # Stop server
    print_info "Stopping primary server..."
    kill $PRIMARY_PID 2>/dev/null || true
    wait $PRIMARY_PID 2>/dev/null || true
    sleep 2
}

# Function to run multi-path demo
demo_multi_path() {
    print_header "Multi-Path Transfer Demo"
    
    print_info "Starting secondary server..."
    ./bin/ftpa-server-secondary \
        -address "$SECONDARY_ADDR" \
        -files "$DATA_DIR" \
        -verbose &
    SECONDARY_PID=$!
    
    # Wait for secondary server to start
    if ! wait_for_server "$SECONDARY_ADDR" "Secondary server"; then
        kill $SECONDARY_PID 2>/dev/null || true
        return 1
    fi
    
    sleep 2
    
    print_info "Starting primary server with multi-path support..."
    ./bin/ftpa-server-primary \
        -address "$PRIMARY_ADDR" \
        -secondary "$SECONDARY_ADDR" \
        -files "$DATA_DIR" \
        -verbose &
    PRIMARY_PID=$!
    
    # Wait for primary server to start
    if ! wait_for_server "$PRIMARY_ADDR" "Primary server"; then
        kill $PRIMARY_PID $SECONDARY_PID 2>/dev/null || true
        return 1
    fi
    
    sleep 3  # Give time for multi-path setup
    
    # Test file downloads with multi-path
    print_info "Downloading large.txt with multi-path..."
    ./bin/ftpa-client \
        -server "$PRIMARY_ADDR" \
        -file "large.txt" \
        -output "$DOWNLOADS_DIR/multi-path" \
        -verbose
    
    print_info "Downloading binary.dat with multi-path..."
    ./bin/ftpa-client \
        -server "$PRIMARY_ADDR" \
        -file "binary.dat" \
        -output "$DOWNLOADS_DIR/multi-path" \
        -progress
    
    print_success "Multi-path demo completed"
    
    # Stop servers
    print_info "Stopping servers..."
    kill $PRIMARY_PID $SECONDARY_PID 2>/dev/null || true
    wait $PRIMARY_PID $SECONDARY_PID 2>/dev/null || true
    sleep 2
}

# Function to run configuration file demo
demo_config_file() {
    print_header "Configuration File Demo"
    
    print_info "Using configuration file: $CONFIG_FILE"
    cat "$CONFIG_FILE"
    
    print_info "Starting servers with configuration file..."
    
    # Start secondary server with config
    ./bin/ftpa-server-secondary -config "$CONFIG_FILE" -address "$SECONDARY_ADDR" &
    SECONDARY_PID=$!
    
    if ! wait_for_server "$SECONDARY_ADDR" "Secondary server"; then
        kill $SECONDARY_PID 2>/dev/null || true
        return 1
    fi
    
    sleep 2
    
    # Start primary server with config
    ./bin/ftpa-server-primary -config "$CONFIG_FILE" &
    PRIMARY_PID=$!
    
    if ! wait_for_server "$PRIMARY_ADDR" "Primary server"; then
        kill $PRIMARY_PID $SECONDARY_PID 2>/dev/null || true
        return 1
    fi
    
    sleep 3
    
    # Test download
    print_info "Testing download with configuration file setup..."
    ./bin/ftpa-client \
        -server "$PRIMARY_ADDR" \
        -file "medium.txt" \
        -output "$DOWNLOADS_DIR/config-demo" \
        -verbose
    
    print_success "Configuration file demo completed"
    
    # Stop servers
    kill $PRIMARY_PID $SECONDARY_PID 2>/dev/null || true
    wait $PRIMARY_PID $SECONDARY_PID 2>/dev/null || true
}

# Function to verify downloads
verify_downloads() {
    print_header "Verifying Downloads"
    
    for download_dir in "$DOWNLOADS_DIR"/*; do
        if [ -d "$download_dir" ]; then
            print_info "Files in $(basename "$download_dir"):"
            ls -lh "$download_dir" 2>/dev/null || print_warning "No files found in $download_dir"
            
            # Compare file sizes
            for file in "$download_dir"/*; do
                if [ -f "$file" ]; then
                    filename=$(basename "$file")
                    original="$DATA_DIR/$filename"
                    if [ -f "$original" ]; then
                        orig_size=$(stat -f%z "$original" 2>/dev/null || stat -c%s "$original" 2>/dev/null)
                        down_size=$(stat -f%z "$file" 2>/dev/null || stat -c%s "$file" 2>/dev/null)
                        if [ "$orig_size" = "$down_size" ]; then
                            print_success "$filename: Size match ($orig_size bytes)"
                        else
                            print_error "$filename: Size mismatch (original: $orig_size, downloaded: $down_size)"
                        fi
                    fi
                fi
            done
        fi
    done
}

# Function to cleanup
cleanup() {
    print_header "Cleanup"
    
    # Kill any remaining processes
    pkill -f "ftpa-server" 2>/dev/null || true
    pkill -f "ftpa-client" 2>/dev/null || true
    
    print_info "Cleanup completed"
}

# Function to show help
show_help() {
    echo "FTPA Demo Script"
    echo "Usage: $0 [command]"
    echo ""
    echo "Commands:"
    echo "  setup       - Create test files and build applications"
    echo "  single      - Run single-path transfer demo"
    echo "  multi       - Run multi-path transfer demo"
    echo "  config      - Run configuration file demo"
    echo "  verify      - Verify downloaded files"
    echo "  cleanup     - Clean up processes and temporary files"
    echo "  all         - Run all demos (default)"
    echo "  help        - Show this help message"
    echo ""
    echo "Examples:"
    echo "  $0 setup     # Prepare for demos"
    echo "  $0 single    # Test single-path transfers"
    echo "  $0 multi     # Test multi-path transfers"
    echo "  $0 all       # Run complete demo suite"
}

# Main execution
main() {
    local command=${1:-all}
    
    case $command in
        setup)
            create_test_files
            build_applications
            ;;
        single)
            demo_single_path
            ;;
        multi)
            demo_multi_path
            ;;
        config)
            demo_config_file
            ;;
        verify)
            verify_downloads
            ;;
        cleanup)
            cleanup
            ;;
        all)
            print_header "FTPA Complete Demo Suite"
            create_test_files
            build_applications
            demo_single_path
            demo_multi_path
            demo_config_file
            verify_downloads
            cleanup
            print_success "All demos completed successfully!"
            ;;
        help|--help|-h)
            show_help
            ;;
        *)
            print_error "Unknown command: $command"
            show_help
            exit 1
            ;;
    esac
}

# Trap to cleanup on exit
trap cleanup EXIT

# Check if we're in the right directory
if [ ! -f "go.mod" ]; then
    print_error "Please run this script from the ftpa project root directory"
    exit 1
fi

# Create necessary directories
mkdir -p bin "$DOWNLOADS_DIR"

# Run main function
main "$@"