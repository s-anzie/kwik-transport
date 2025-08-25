// Simple Echo Client - Demonstrates basic KWIK usage
package main

import (
	"context"
	"fmt"
	"log"

	kwik "kwik/pkg"
)

func main() {
	// Connect to server (identical to QUIC)
	fmt.Println("[APP-CLIENT] Connecting to server...")
	session, err := kwik.Dial(context.Background(), "localhost:4433", nil)
	if err != nil {
		log.Fatal(err)
	}
	defer session.Close()
	fmt.Println("[APP-CLIENT] Connected to server.")
	// Open stream
	stream, err := session.OpenStreamSync(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	defer stream.Close()

	// Send message
	message := "Hello KWIK!"
	_, err = stream.Write([]byte(message))
	if err != nil {
		log.Fatal(err)
	}

	// Read response
	buffer := make([]byte, 1024)
	n, err := stream.Read(buffer)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Sent: %s\n", message)
	fmt.Printf("Received: %s\n", string(buffer[:n]))
}
