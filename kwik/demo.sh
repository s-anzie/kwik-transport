#!/bin/bash

# KWIK Demo Script
# This script demonstrates KWIK functionality by running server and client examples

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Function to print colored output
print_status() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Function to check if command exists
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

# Function to cleanup background processes
cleanup() {
    print_status "Cleaning up..."
    if [ ! -z "$SERVER_PID" ]; then
        kill $SERVER_PID 2>/dev/null || true
        print_status "Server stopped"
    fi
    exit 0
}

# Set up signal handlers
trap cleanup SIGINT SIGTERM

print_status "KWIK Demo Script"
print_status "================"
echo

# Check prerequisites
print_status "Checking prerequisites..."

if ! command_exists go; then
    print_error "Go is not installed. Please install Go 1.19 or later."
    exit 1
fi

GO_VERSION=$(go version | cut -d' ' -f3 | sed 's/go//')
print_success "Go version: $GO_VERSION"

# Check if we're in the right directory
if [ ! -f "go.mod" ]; then
    print_error "Please run this script from the kwik directory (where go.mod is located)"
    exit 1
fi

# Build the examples
print_status "Building examples..."
make build-examples
if [ $? -eq 0 ]; then
    print_success "Examples built successfully"
else
    print_error "Failed to build examples"
    exit 1
fi

# Start server in background
print_status "Starting KWIK server on localhost:4433..."
./bin/kwik-server &
SERVER_PID=$!

# Wait a moment for server to start
sleep 2

# Check if server is still running
if ! kill -0 $SERVER_PID 2>/dev/null; then
    print_error "Server failed to start"
    exit 1
fi

print_success "Server started (PID: $SERVER_PID)"

# Function to run client demo
run_client_demo() {
    print_status "Running client demo..."
    echo
    print_status "The client will:"
    print_status "1. Connect to the server"
    print_status "2. Demonstrate basic stream operations"
    print_status "3. Show multi-path capabilities"
    print_status "4. Enter interactive mode"
    echo
    print_status "In interactive mode, you can try these commands:"
    print_status "  send <message>  - Send a message to the server"
    print_status "  paths           - Show path information"
    print_status "  metrics         - Show session metrics"
    print_status "  help            - Show help"
    print_status "  quit            - Exit client"
    echo
    print_warning "Press Enter to start the client..."
    read

    ./bin/kwik-client
}

# Function to run automated demo
run_automated_demo() {
    print_status "Running automated demo..."
    
    # Create a simple test script for the client
    cat > /tmp/kwik_demo_commands.txt << EOF
send Hello from automated demo!
paths
metrics
send This is a second message
paths
quit
EOF

    print_status "Sending automated commands to client..."
    ./bin/kwik-client < /tmp/kwik_demo_commands.txt
    
    rm -f /tmp/kwik_demo_commands.txt
    print_success "Automated demo completed"
}

# Main menu
while true; do
    echo
    print_status "KWIK Demo Options:"
    echo "1. Interactive client demo"
    echo "2. Automated demo"
    echo "3. Show server logs"
    echo "4. Exit"
    echo
    read -p "Choose an option (1-4): " choice

    case $choice in
        1)
            run_client_demo
            ;;
        2)
            run_automated_demo
            ;;
        3)
            print_status "Server is running in background (PID: $SERVER_PID)"
            print_status "Check server output in the terminal where you started this script"
            print_status "Or use 'ps aux | grep kwik-server' to verify it's running"
            ;;
        4)
            cleanup
            ;;
        *)
            print_warning "Invalid option. Please choose 1-4."
            ;;
    esac
done