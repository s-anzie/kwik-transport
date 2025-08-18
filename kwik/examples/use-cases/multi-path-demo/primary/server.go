// Multi-Path Demo Primary Server - Shows Secondary Stream Isolation
package main

import (
	"context"
	"fmt"
	"io"
	kwik "kwik/pkg"
	"kwik/pkg/session"
	"log"
	"time"
)

func main() {
	fmt.Println("\n\n[PRIMARY SERVER] Démarrage du serveur primaire sur localhost:4433")

	config := kwik.DefaultConfig()
	config.MaxPathsPerSession = 5
	config.Logging.GlobalLevel = kwik.LogLevelSilent

	listener, err := kwik.Listen("localhost:4433", config)
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()

	fmt.Println("[PRIMARY SERVER] Serveur en écoute, en attente de connexions...")

	for {
		session, err := listener.Accept(context.Background())
		if err != nil {
			fmt.Printf("[PRIMARY SERVER] Erreur lors de l'acceptation de session: %v\n", err)
			continue
		}
		fmt.Println("[PRIMARY SERVER] Nouvelle session acceptée")
		go handleSession(session)
	}
}

func handleSession(sess session.Session) {
	fmt.Println("[PRIMARY SERVER] Gestion d'une nouvelle session")
	defer func() {
		fmt.Println("[PRIMARY SERVER] Fermeture de la session")
		sess.Close()
	}()

	for {
		stream, err := sess.AcceptStream(context.Background())
		if err != nil {
			if err == io.EOF {
				fmt.Println("[PRIMARY SERVER] Session fermée par le client")
				return
			}
			fmt.Printf("[PRIMARY SERVER] Erreur lors de l'acceptation de stream: %v\n", err)
			return
		}
		fmt.Printf("[PRIMARY SERVER] Nouveau stream accepté (ID: %d)\n", stream.StreamID())
		go handleStream(stream, sess)
	}
}

func handleStream(stream session.Stream, sess session.Session) {
	fmt.Printf("[PRIMARY SERVER] Traitement du stream %d\n", stream.StreamID())
	defer func() {
		fmt.Printf("[PRIMARY SERVER] Fermeture du stream %d\n", stream.StreamID())
		stream.Close()
	}()

	nextoffset := 0

	// Lit le message du client
	fmt.Printf("[PRIMARY SERVER] Lecture du message du client sur stream %d\n", stream.StreamID())
	buffer := make([]byte, 1024)
	n, err := stream.Read(buffer)
	if err != nil {
		fmt.Printf("[PRIMARY SERVER] Erreur lors de la lecture: %v\n", err)
		return
	}

	clientMessage := string(buffer[:n])
	fmt.Printf("[PRIMARY SERVER] Message reçu du client: '%s'\n", clientMessage)

	// Ajoute un chemin secondaire
	fmt.Println("[PRIMARY SERVER] Ajout du chemin secondaire localhost:4434")
	err = sess.AddPath("localhost:4434")
	if err != nil {
		fmt.Printf("[PRIMARY SERVER] Erreur lors de l'ajout du chemin: %v\n", err)
	} else {
		fmt.Println("[PRIMARY SERVER] Chemin secondaire ajouté avec succès")
	}

	// Écrit la réponse primaire
	partialResponse := "salut comment vas tu"
	fmt.Printf("[PRIMARY SERVER] Envoi de la réponse primaire: '%s'\n", partialResponse)
	stream.SetOffset(nextoffset)
	_, err = stream.Write([]byte(partialResponse))
	if err != nil {
		fmt.Printf("[PRIMARY SERVER] Erreur lors de l'écriture: %v\n", err)
		return
	}

	// Envoie des données au serveur secondaire
	fmt.Println("[PRIMARY SERVER] Attente avant envoi au serveur secondaire...")
	time.Sleep(500 * time.Millisecond)

	if serverSession, ok := sess.(*session.ServerSession); ok {
		pathID := serverSession.GetPendingPathID("localhost:4434")
		if pathID != "" {
			primaryResponseLength := len(partialResponse)
			nextoffset += primaryResponseLength
			rawMessage := []byte("Second Message 2")
			fmt.Printf("[PRIMARY SERVER] Envoi de données au serveur secondaire via path %s: '%s'\n", pathID, string(rawMessage))
			fmt.Printf("[PRIMARY SERVER] Stream KWIK cible: %d\n", stream.RemoteStreamID())
			err = sess.SendRawData(rawMessage, pathID, stream.RemoteStreamID())
			if err != nil {
				fmt.Printf("[PRIMARY SERVER] Erreur lors de l'envoi au serveur secondaire: %v\n", err)
			} else {
				fmt.Println("[PRIMARY SERVER] Données envoyées au serveur secondaire avec succès")
			}
		} else {
			fmt.Println("[PRIMARY SERVER] Aucun pathID trouvé pour localhost:4434")
		}
	}

	fmt.Println("[PRIMARY SERVER] Attente finale avant fermeture...")
	time.Sleep(2 * time.Second)
}
