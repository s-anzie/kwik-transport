// Multi-Path Demo Secondary Server - Shows Secondary Stream Isolation
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"kwik/examples/use-cases/multi-path-demo/types"
	kwik "kwik/pkg"
	"kwik/pkg/logger"
	"kwik/pkg/session"
	"log"
	"os"
	"strings"
	"time"
)

func main() {
	fmt.Println("\n\n[SECONDARY SERVER] Démarrage du serveur secondaire sur localhost:4434")

	config := kwik.DefaultConfig()
	config.MaxPathsPerSession = 5
	config.LogLevel = logger.LogLevelDebug
	config.Logging = &kwik.LogConfig{
		GlobalLevel: logger.LogLevelDebug,
		Components: map[string]logger.LogLevel{
			"SESSION":   logger.LogLevelDebug,
			"CONTROL":   logger.LogLevelDebug,
			"TRANSPORT": logger.LogLevelDebug,
			"DATA":      logger.LogLevelDebug,
			"STREAM":    logger.LogLevelDebug,
			"DPM":       logger.LogLevelDebug,
		},
	}

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

	for {
		stream, err := sess.AcceptStream(context.Background())
		if err != nil {
			if err == io.EOF {
				fmt.Println("[SECONDARY SERVER] Session fermée par le client")
				return
			}
			// Tolère les timeouts/inactivité et continue la boucle d'acceptation
			errStr := strings.ToLower(err.Error())
			if strings.Contains(errStr, "timeout") || strings.Contains(errStr, "no recent network activity") {
				fmt.Printf("[SECONDARY SERVER] Timeout accept stream, on continue: %v\n", err)
				continue
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

	payload := buffer[:n]
	fmt.Printf("[SECONDARY SERVER] Message reçu du serveur primaire: '%s'\n", string(payload))

	// Tente d'interpréter comme une Command
	var cmd types.Command
	if err := json.Unmarshal(payload, &cmd); err == nil && cmd.Resource != "" && cmd.ReadSize > 0 {
		fmt.Printf("[SECONDARY SERVER] Commande reçue: chunkID=%d resource=%s fileOff=%d size=%d streamOff=%d\n", cmd.ChunkID, cmd.Resource, cmd.FileOffset, cmd.ReadSize, cmd.StreamOffset)

		// Ouvre un nouveau stream pour la réponse et configure l'agrégation
		newStream, err := sess.OpenStreamSync(context.Background())
		if err != nil {
			fmt.Printf("[SECONDARY SERVER] Erreur lors de l'ouverture du stream: %v\n", err)
			return
		}
		defer func() {
			fmt.Printf("[SECONDARY SERVER] Fermeture du nouveau stream %d\n", newStream.StreamID())
			newStream.Close()
		}()

		newStream.SetOffset(int(cmd.StreamOffset))
		newStream.SetRemoteStreamID(stream.RemoteStreamID())
		fmt.Printf("[SECONDARY SERVER] Stream configuré: offset=%d, remoteStreamID=%d\n", cmd.StreamOffset, stream.RemoteStreamID())

		// Lecture du segment à partir du fichier (ReadAt par chunk)
		f, rErr := os.Open(cmd.Resource)
		if rErr != nil {
			fmt.Printf("[SECONDARY SERVER] Erreur ouverture fichier '%s': %v\n", cmd.Resource, rErr)
			return
		}
		defer f.Close()

		buf := make([]byte, cmd.ReadSize)
		n, rErr := f.ReadAt(buf, cmd.FileOffset)
		if rErr != nil && rErr != io.EOF && rErr != io.ErrUnexpectedEOF {
			fmt.Printf("[SECONDARY SERVER] Erreur ReadAt: %v\n", rErr)
			return
		}
		if n == 0 {
			fmt.Printf("[SECONDARY SERVER] Aucun octet lu pour chunkID=%d\n", cmd.ChunkID)
			return
		}
		segment := buf[:n]

		// Encapsule et envoie le chunk en JSON (types.Chunk) terminé par '\n'
		ch := types.Chunk{Id: cmd.ChunkID, Offset: int(cmd.FileOffset), Size: len(segment), Data: segment}
		payload, jerr := json.Marshal(ch)
		if jerr != nil {
			fmt.Printf("[SECONDARY SERVER] Erreur marshal chunk: %v\n", jerr)
			return
		}
		frame := append(payload, '\n')
		if _, err := newStream.Write(frame); err != nil {
			fmt.Printf("[SECONDARY SERVER] Erreur lors de l'écriture du chunk: %v\n", err)
			return
		}
		fmt.Printf("[SECONDARY SERVER] Chunk envoyé: chunkID=%d, bytes=%d (frameLen=%d)\n", cmd.ChunkID, len(segment), len(frame))
		fmt.Println("[SECONDARY SERVER] Attente avant fermeture...")
		time.Sleep(500 * time.Millisecond)
		return
	}
}
