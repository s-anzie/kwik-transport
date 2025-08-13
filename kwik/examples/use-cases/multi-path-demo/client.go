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

	// 4. Boucle de lecture et d'écriture avec comptage des messages
	fmt.Println("[CLIENT] 4. Starting continuous read/write loop...")
	buffer := make([]byte, 1024)
	messagesReceived := 0
	messagesSent := 1 // On a déjà envoyé le premier message
	maxMessages := 4  // Limite pour éviter une boucle infinie

	for messagesReceived < maxMessages {
		fmt.Printf("[CLIENT] 4.%d. Reading from stream (attempt %d)...\n", messagesReceived+1, messagesReceived+1)

		// Lecture avec timeout pour éviter de bloquer indéfiniment
		n, err := stream.Read(buffer)
		if err != nil {
			fmt.Printf("[CLIENT] Read error or timeout: %v\n", err)
			// Si on a une erreur de lecture, on essaie d'envoyer un nouveau message
			if messagesReceived > 0 {
				break // On a reçu au moins un message, on peut terminer
			}
			time.Sleep(500 * time.Millisecond)
			continue
		}

		// Message reçu avec succès
		messagesReceived++
		response := string(buffer[:n])
		fmt.Printf("[CLIENT] ✅ Read message %d: %s\n", messagesReceived, response)

		// Analyser le message pour voir s'il vient du serveur secondaire
		if contains(response, "serveur secondaire") {
			fmt.Printf("[CLIENT] 📨 Detected secondary server response in message %d\n", messagesReceived)
		}

		// Envoyer un nouveau message si on n'a pas atteint la limite
		if messagesSent < maxMessages {
			messagesSent++
			newMessage := fmt.Sprintf("Message client #%d (réponse à: %s)", messagesSent, truncateString(response, 30))
			fmt.Printf("[CLIENT] 5.%d. Sending response message %d...\n", messagesSent-1, messagesSent)
			_, err = stream.Write([]byte(newMessage))
			if err != nil {
				fmt.Printf("[CLIENT] Write error: %v\n", err)
				break
			}
			fmt.Printf("[CLIENT] ✅ Sent message %d: %s\n", messagesSent, newMessage)
		}

		// Petite pause entre les messages
		time.Sleep(500 * time.Millisecond)
	}

	fmt.Printf("[CLIENT] 6. Loop completed - Received: %d messages, Sent: %d messages\n", messagesReceived, messagesSent)

	// 7. Il attend un peu et se termine
	fmt.Println("[CLIENT] 7. Waiting before termination...")
	time.Sleep(2 * time.Second)
	fmt.Println("[CLIENT] ✅ Demo completed")
}

// contains vérifie si une chaîne contient une sous-chaîne (insensible à la casse)
func contains(text, substr string) bool {
	return strings.Contains(strings.ToLower(text), strings.ToLower(substr))
}

// truncateString tronque une chaîne à la longueur spécifiée
func truncateString(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	return text[:maxLen] + "..."
}
