// Multi-Path Demo Client - Shows Secondary Stream Isolation
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	kwik "kwik/pkg"
	"strings"
)

func main() {
	// 1. Il dial
	session, err := kwik.Dial(context.Background(), "localhost:4433", nil)
	if err != nil {
		log.Fatal(err)
	}
	defer session.Close()

	// 2. Il envoie un message
	stream, err := session.OpenStreamSync(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	defer stream.Close()

	message1 := "bonjour"
	_, err = stream.Write([]byte(message1))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s\n", message1)

	// 3. Il attend d'avoir 2 chemins au moins
	for {
		paths := session.GetActivePaths()
		if len(paths) >= 2 {
			break
		}
		time.Sleep(1 * time.Second)
	}

	// 4. Boucle de lecture et d'écriture avec comptage des messages
	buffer := make([]byte, 1024)
	messagesReceived := 0
	maxMessages := 2  // Attendre 2 messages: primaire + secondaire

	for messagesReceived < maxMessages {
		// Lecture avec timeout pour éviter de bloquer indéfiniment
		n, err := stream.Read(buffer)
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}

		// Message reçu avec succès
		messagesReceived++
		response := string(buffer[:n])
		fmt.Printf("%s\n", response)

		// Petite pause entre les messages
		time.Sleep(500 * time.Millisecond)
	}

	// 5. Il attend un peu et se termine
	time.Sleep(2 * time.Second)
}

// contains vérifie si une chaîne contient une sous-chaîne (insensible à la casse)
func contains(text, substr string) bool {
	return strings.Contains(strings.ToLower(text), strings.ToLower(substr))
}
