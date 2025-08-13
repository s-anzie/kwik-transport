// Multi-Path Demo Client - Shows Secondary Stream Isolation
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	kwik "kwik/pkg"
)

func main() {
	fmt.Println("=== CLIENT MULTI-PATH DEMO ===")

	// 1. Il dial
	fmt.Println("[CLIENT] 1. Dialing primary server...")
	session, err := kwik.Dial(context.Background(), "localhost:4433", nil)
	if err != nil {
		log.Fatal(err)
	}
	defer session.Close()
	fmt.Println("[CLIENT] ✅ Connected to primary server")

	// 2. Il envoie un message
	fmt.Println("[CLIENT] 2. Opening stream and sending first message...")
	stream, err := session.OpenStreamSync(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	defer stream.Close()

	message1 := "Premier message du client"
	_, err = stream.Write([]byte(message1))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("[CLIENT] ✅ Sent: %s\n", message1)

	// 3. Il attend d'avoir 2 chemins au moins
	fmt.Println("[CLIENT] 3. Waiting for at least 2 paths...")
	for {
		paths := session.GetActivePaths()
		fmt.Printf("[CLIENT] Current paths: %d\n", len(paths))
		if len(paths) >= 2 {
			fmt.Println("[CLIENT] ✅ At least 2 paths available")
			break
		}
		time.Sleep(1 * time.Second)
	}

	// 4. Il lit dans son flux
	fmt.Println("[CLIENT] 4. Reading from stream...")
	buffer := make([]byte, 1024)
	n, err := stream.Read(buffer)
	if err != nil {
		log.Fatal(err)
	}
	response1 := string(buffer[:n])
	fmt.Printf("[CLIENT] ✅ Read: %s\n", response1)

	// 5. Il écrit un autre message
	fmt.Println("[CLIENT] 5. Writing second message...")
	message2 := "Deuxième message du client"
	_, err = stream.Write([]byte(message2))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("[CLIENT] ✅ Sent: %s\n", message2)

	// 6. Il lit dans son flux
	fmt.Println("[CLIENT] 6. Reading second response...")
	n, err = stream.Read(buffer)
	if err != nil {
		log.Fatal(err)
	}
	response2 := string(buffer[:n])
	fmt.Printf("[CLIENT] ✅ Read: %s\n", response2)

	// 7. Il attend un peu et se termine
	fmt.Println("[CLIENT] 7. Waiting before termination...")
	time.Sleep(2 * time.Second)
	fmt.Println("[CLIENT] ✅ Demo completed")
}
