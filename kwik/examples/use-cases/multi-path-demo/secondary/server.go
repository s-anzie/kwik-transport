// Multi-Path Demo Secondary Server - Shows Secondary Stream Isolation
package main

import (
	"context"
	"io"
	"log"
	"time"
	"fmt"
	kwik "kwik/pkg"
	"kwik/pkg/session"
)

func main() {
	fmt.Println("\n\n[SECONDARY SERVER] Démarrage du serveur secondaire sur localhost:4434")
	
	config := kwik.DefaultConfig()
	config.MaxPathsPerSession = 5
	config.Logging.GlobalLevel = kwik.LogLevelSilent

	listener, err := kwik.Listen("localhost:4434", config)
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()

	fmt.Println("[SECONDARY SERVER] Serveur en écoute, en attente de connexions...")

	for {
		session, err := listener.Accept(context.Background())
		if err != nil {
			fmt.Printf("[SECONDARY SERVER] Erreur lors de l'acceptation de session: %v\n", err)
			continue
		}
		fmt.Println("[SECONDARY SERVER] Nouvelle session acceptée")
		go handleSession(session)
	}
}

func handleSession(sess session.Session) {
	fmt.Println("[SECONDARY SERVER] Gestion d'une nouvelle session")
	defer func() {
		fmt.Println("[SECONDARY SERVER] Fermeture de la session")
		sess.Close()
	}()

	// Configure comme serveur secondaire
	if serverSession, ok := sess.(*session.ServerSession); ok {
		fmt.Println("[SECONDARY SERVER] Configuration du rôle serveur secondaire")
		serverSession.SetServerRole(session.ServerRoleSecondary)
	}

	for {
		stream, err := sess.AcceptStream(context.Background())
		if err != nil {
			if err == io.EOF {
				fmt.Println("[SECONDARY SERVER] Session fermée par le client")
				return
			}
			fmt.Printf("[SECONDARY SERVER] Erreur lors de l'acceptation de stream: %v\n", err)
			return
		}
		fmt.Printf("[SECONDARY SERVER] Nouveau stream accepté (ID: %d)\n", stream.StreamID())
		go handleStream(stream, sess)
	}
}

func handleStream(stream session.Stream, sess session.Session) {
	fmt.Printf("[SECONDARY SERVER] Traitement du stream %d\n", stream.StreamID())
	defer func() {
		fmt.Printf("[SECONDARY SERVER] Fermeture du stream %d\n", stream.StreamID())
		stream.Close()
	}()

	// Lit le message du serveur primaire
	fmt.Printf("[SECONDARY SERVER] Lecture du message du serveur primaire sur stream %d\n", stream.StreamID())
	buffer := make([]byte, 1024)
	n, err := stream.Read(buffer)
	if err != nil {
		fmt.Printf("[SECONDARY SERVER] Erreur lors de la lecture: %v\n", err)
		return
	}
	
	primaryMessage := string(buffer[:n])
	fmt.Printf("[SECONDARY SERVER] Message reçu du serveur primaire: '%s'\n", primaryMessage)

	// Ouvre un nouveau stream secondaire pour la réponse
	offset := 21 // Après "salut comment vas tu"
	fmt.Printf("[SECONDARY SERVER] Ouverture d'un nouveau stream pour la réponse (offset: %d)\n", offset)
	
	newStream, err := sess.OpenStreamSync(context.Background())
	if err != nil {
		fmt.Printf("[SECONDARY SERVER] Erreur lors de l'ouverture du stream: %v\n", err)
		return
	}
	defer func() {
		fmt.Printf("[SECONDARY SERVER] Fermeture du nouveau stream %d\n", newStream.StreamID())
		newStream.Close()
	}()

	// Configure le stream pour l'agrégation
	fmt.Printf("[SECONDARY SERVER] Configuration du stream %d pour l'agrégation\n", newStream.StreamID())
	newStream.SetOffset(offset)
	newStream.SetRemoteStreamID(1) // Stream KWIK cible
	fmt.Printf("[SECONDARY SERVER] Stream configuré: offset=%d, remoteStreamID=1\n", offset)

	// Envoie la réponse
	response := "Salut, ça fait longtemps"
	fmt.Printf("[SECONDARY SERVER] Envoi de la réponse: '%s'\n", response)
	_, err = newStream.Write([]byte(response))
	if err != nil {
		fmt.Printf("[SECONDARY SERVER] Erreur lors de l'écriture: %v\n", err)
		return
	}
	
	fmt.Println("[SECONDARY SERVER] Réponse envoyée avec succès")
	fmt.Println("[SECONDARY SERVER] Attente avant fermeture...")
	time.Sleep(1 * time.Second)
}
