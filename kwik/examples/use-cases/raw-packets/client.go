// Raw Packets Client - Demonstrates receiving custom protocol data
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	kwik "kwik/pkg"
)

func main() {
	// Connect to server
	session, err := kwik.Dial(context.Background(), "localhost:4433", nil)
	if err != nil {
		log.Fatal(err)
	}
	defer session.Close()

	fmt.Println("Connected to raw packets server")

	// Send a regular message to trigger raw packet response
	stream, err := session.OpenStreamSync(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	defer stream.Close()

	// Send trigger message
	message := "Please send raw packet"
	_, err = stream.Write([]byte(message))
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Sent: %s\n", message)

	// Read response
	buffer := make([]byte, 1024)
	n, err := stream.Read(buffer)
	if err != nil {
		log.Printf("Read error: %v", err)
	} else {
		fmt.Printf("Received: %s\n", string(buffer[:n]))
	}

	// Wait to potentially receive raw packets
	fmt.Println("Waiting for raw packets...")
	time.Sleep(2 * time.Second)

	// Show path information (raw packets are sent via paths)
	paths := session.GetActivePaths()
	fmt.Printf("Session has %d active paths for raw packet transmission\n", len(paths))
	for _, path := range paths {
		fmt.Printf("  - Path: %s (Primary: %v)\n", path.Address, path.IsPrimary)
	}

	fmt.Println("Raw packets demo completed")
}