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
	fmt.Println("\n\n[CLIENT] Démarrage du client multi-path demo")

	// 1. Il dial avec logs silencieux
	fmt.Println("[CLIENT] Connexion au serveur primaire localhost:4433...")
	config := kwik.DefaultConfig()
	config.Logging.GlobalLevel = kwik.LogLevelDebug
	session, err := kwik.Dial(context.Background(), "localhost:4433", config)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		fmt.Println("[CLIENT] Fermeture de la session")
		session.Close()
	}()

	fmt.Println("[CLIENT] Connexion établie avec succès")

	// 2. Il envoie un message
	fmt.Println("[CLIENT] Ouverture d'un stream...")
	stream, err := session.OpenStreamSync(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		fmt.Printf("[CLIENT] Fermeture du stream %d\n", stream.StreamID())
		stream.Close()
	}()

	fmt.Printf("[CLIENT] Stream %d ouvert avec succès\n", stream.StreamID())
	fmt.Printf("[CLIENT] Stream KWIK source: %d\n", stream.StreamID())
	message1 := "bonjour"
	fmt.Printf("[CLIENT] Envoi du message: '%s'\n", message1)
	_, err = stream.Write([]byte(message1))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("[CLIENT] Message envoyé avec succès")

	// 3. Il attend d'avoir 2 chemins au moins
	fmt.Println("[CLIENT] Attente de l'établissement de chemins multiples...")
	pathCount := 0
	for {
		paths := session.GetActivePaths()
		newPathCount := len(paths)
		if newPathCount != pathCount {
			fmt.Printf("[CLIENT] Nombre de chemins actifs: %d\n", newPathCount)
			pathCount = newPathCount
		}
		if len(paths) >= 2 {
			fmt.Println("[CLIENT] Chemins multiples établis!")
			break
		}
		time.Sleep(1 * time.Second)
	}

	// 4. Boucle de lecture et d'écriture avec comptage des messages
	fmt.Println("[CLIENT] Début de la lecture des réponses...")
	buffer := make([]byte, 1024)
	messagesReceived := 0
	maxMessages := 2 // Attendre 2 messages: primaire + secondaire

	for messagesReceived < maxMessages {
		fmt.Printf("[CLIENT] Tentative de lecture %d/%d...\n", messagesReceived+1, maxMessages)

		// Lecture avec timeout pour éviter de bloquer indéfiniment
		n, err := stream.Read(buffer)
		if err != nil {
			fmt.Printf("[CLIENT] Erreur de lecture ou pas de données: %v\n", err)
			time.Sleep(500 * time.Millisecond)
			continue
		}

		if n == 0 {
			// Ignorer les lectures à 0 octet (aucune donnée disponible pour l'instant)
			fmt.Printf("[CLIENT] Lecture à 0 octet, continuer...\n")
			break
		}

		// Message reçu avec succès
		messagesReceived++
		response := string(buffer[:n])
		fmt.Printf("[CLIENT] Message %d reçu: '%s'\n", messagesReceived, response)

		// Petite pause entre les messages
		time.Sleep(500 * time.Millisecond)
	}

	// 5. Il attend un peu et se termine
	fmt.Println("[CLIENT] Tous les messages reçus, attente finale...")
	time.Sleep(2 * time.Second)
	fmt.Println("[CLIENT] Fin du client")
}
