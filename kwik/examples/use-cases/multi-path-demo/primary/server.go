// Multi-Path Demo Primary Server - Shows Secondary Stream Isolation
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"kwik/examples/use-cases/multi-path-demo/types"
	kwik "kwik/pkg"
	"kwik/pkg/session"
	"log"
	"os"
	"strings"
	"time"
)

func main() {
	fmt.Println("\n\n[PRIMARY SERVER] Démarrage du serveur primaire sur localhost:4433")

	config := kwik.DefaultConfig()
	config.MaxPathsPerSession = 5
	config.LogLevel = kwik.LogLevelDebug
	config.Logging = &kwik.LogConfig{
		GlobalLevel: kwik.LogLevelDebug,
		Components: map[string]kwik.LogLevel{
			"SESSION":   kwik.LogLevelDebug,
			"CONTROL":   kwik.LogLevelDebug,
			"TRANSPORT": kwik.LogLevelDebug,
			"DATA":      kwik.LogLevelDebug,
			"STREAM":    kwik.LogLevelDebug,
			"DPM":       kwik.LogLevelDebug,
		},
	}

	listener, err := kwik.Listen("localhost:4433", config)
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()

	fmt.Println("[PRIMARY SERVER] Serveur en écoute, en attente de connexions...")

	for {
		session, err := listener.Accept(context.Background())
		if err != nil {
			errStr := strings.ToLower(err.Error())
			if strings.Contains(errStr, "timeout") || strings.Contains(errStr, "no recent network activity") {
				fmt.Printf("[PRIMARY SERVER] Timeout accept session, on continue: %v\n", err)
				continue
			}
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

	// Lit le message du client
	fmt.Printf("[PRIMARY SERVER] Lecture du message du client sur stream %d\n", stream.StreamID())
	buffer := make([]byte, 1024)
	n, err := stream.Read(buffer)
	if err != nil {
		fmt.Printf("[PRIMARY SERVER] Erreur lors de la lecture: %v\n", err)
		return
	}

	clientPayload := buffer[:n]
	fmt.Printf("[PRIMARY SERVER] Message reçu du client: '%s'\n", string(clientPayload))

	// Essaie d'interpréter la requête comme une Request JSON
	var req types.Request
	if err := json.Unmarshal(clientPayload, &req); err != nil {
		fmt.Printf("[PRIMARY SERVER] Requête non JSON, fallback texte. Err=%v\n", err)
		return
	}

	// Dispatch en fonction du type
	switch req.Type {
	case "file":
		if err := handleFileRequest(stream, sess, req); err != nil {
			fmt.Printf("[PRIMARY SERVER] Erreur handler fichier: %v\n", err)
		}
	default:
		fmt.Printf("[PRIMARY SERVER] Type de requête inconnu '%s', fallback démo\n", req.Type)
	}

	fmt.Println("[PRIMARY SERVER] Traitement terminé pour ce stream")
}

// handleFileRequest gère l'envoi d'un fichier en chunks:
// - Calcule le nombre de chunks
// - Envoie les chunks pairs (0,2,4,...) sur le stream primaire avec le bon offset
// - Envoie des commandes pour les chunks impairs (1,3,5,...) au serveur secondaire via SendRawData
func handleFileRequest(stream session.Stream, sess session.Session, req types.Request) error {
	// Lecture des stats et du contenu
	info, err := os.Stat(req.Resource)
	if err != nil {
		// Réponse d'échec
		fmt.Printf("[PRIMARY SERVER] Erreur lors de l'ouverture du fichier '%s': %v\n", req.Resource, err)
		resp := types.Response{Success: false, Error: fmt.Sprintf("stat '%s': %v", req.Resource, err)}
		payload, _ := json.Marshal(resp)
		stream.SetOffset(0)
		_, _ = stream.Write(append(payload, '\n'))
		return err
	}
	size := info.Size()
	// Choix taille chunk (démonstration)
	const chunkSize = 1024
	chunks := int((size + chunkSize - 1) / chunkSize)
	fmt.Printf("[PRIMARY SERVER] Fichier %s taille=%d, chunkSize=%d, nbChunks=%d\n", req.Resource, size, chunkSize, chunks)

	// Ouvre le fichier pour des lectures par chunk
	f, readErr := os.Open(req.Resource)
	if readErr != nil {
		// Réponse d'échec
		fmt.Printf("[PRIMARY SERVER] Erreur lors de l'ouverture du fichier '%s': %v\n", req.Resource, readErr)
		resp := types.Response{Success: false, Error: fmt.Sprintf("open '%s': %v", req.Resource, readErr)}
		payload, _ := json.Marshal(resp)
		stream.SetOffset(0)
		_, _ = stream.Write(append(payload, '\n'))
		return readErr
	}
	defer f.Close()

	// Réponse de succès avec méta-données, terminée par un '\n' pour délimiter
	resp := types.Response{
		Success: true,
		File: &types.FileInfo{
			Name:      info.Name(),
			Size:      size,
			NumChunks: chunks,
			ChunkSize: chunkSize,
		},
	}
	respdata, _ := json.Marshal(resp)
	respdata = append(respdata, '\n')
	stream.SetOffset(0)
	if _, err := stream.Write(respdata); err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	headerLen := len(respdata)
	// Position agrégée courante dans le flux après l'entête
	aggPos := int64(headerLen)

	// Ajoute un chemin secondaire
	fmt.Println("[PRIMARY SERVER] Ajout du chemin secondaire localhost:4434")
	err = sess.AddPath("localhost:4434")
	if err != nil {
		fmt.Printf("[PRIMARY SERVER] Erreur lors de l'ajout du chemin secondaire: %v\n", err)
	} else {
		fmt.Println("[PRIMARY SERVER] Chemin secondaire ajouté avec succès")
	}

	// Laisse un court délai pour l'intégration du chemin secondaire côté client
	// afin d'éviter l'envoi de commandes avant que le chemin ne soit prêt (trous d'offset)
	for i := 0; i < 10; i++ {
		if serverSession, ok := sess.(*session.ServerSession); ok {
			if pid := serverSession.GetPendingPathID("localhost:4434"); pid != "" {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Récupère path secondaire si disponible
	var pathID string
	if serverSession, ok := sess.(*session.ServerSession); ok {
		pathID = serverSession.GetPendingPathID("localhost:4434")
	}

	// Itère sur les chunks
	for i := 0; i < chunks; i++ {
		fileOffset := int64(i) * chunkSize
		readSize := int(chunkSize)
		if remaining := int(size - fileOffset); remaining < readSize {
			readSize = remaining
		}
		// Lit le chunk depuis le disque
		buf := make([]byte, readSize)
		n, rerr := f.ReadAt(buf, fileOffset)
		if rerr != nil && rerr != io.EOF && rerr != io.ErrUnexpectedEOF {
			return fmt.Errorf("readAt chunk %d: %w", i, rerr)
		}
		if n == 0 {
			continue
		}
		dataSeg := buf[:n]
		// Encapsulation JSON: types.Chunk puis '\n'
		ch := types.Chunk{Id: i, Offset: int(fileOffset), Size: n, Data: dataSeg}
		payload, jerr := json.Marshal(ch)
		if jerr != nil {
			return fmt.Errorf("marshal chunk %d: %w", i, jerr)
		}
		frame := append(payload, '\n')
		frameLen := len(frame)

		if i%2 == 0 {
			// Pair: envoi direct sur le primaire
			stream.SetOffset(int(aggPos))
			if _, err := stream.Write(frame); err != nil {
				return fmt.Errorf("write chunk %d: %w", i, err)
			}
			fmt.Printf("[PRIMARY SERVER] Chunk pair #%d encapsulé (JSON) envoyé: fileOff=%d size=%d streamOff=%d frameLen=%d\n", i, fileOffset, n, aggPos, frameLen)
			aggPos += int64(frameLen)
		} else {
			// Impair: commande au secondaire
			if pathID == "" {
				fmt.Println("[PRIMARY SERVER] Aucun path secondaire disponible, envoi sur primaire à la place")
				stream.SetOffset(int(aggPos))
				if _, err := stream.Write(frame); err != nil {
					return fmt.Errorf("write chunk %d (fallback): %w", i, err)
				}
				fmt.Printf("[PRIMARY SERVER] Chunk impair #%d encapsulé (JSON) envoyé en fallback sur primaire: fileOff=%d size=%d streamOff=%d frameLen=%d\n", i, fileOffset, n, aggPos, frameLen)
				aggPos += int64(frameLen)
				continue
			}
			cmd := types.Command{
				Resource:     req.Resource,
				ChunkID:      i,
				FileOffset:   fileOffset,
				ReadSize:     n,
				StreamOffset: aggPos,
			}
			payload, mErr := json.Marshal(cmd)
			if mErr != nil {
				return fmt.Errorf("marshal command chunk %d: %w", i, mErr)
			}
			if err := sess.SendRawData(payload, pathID, stream.RemoteStreamID()); err != nil {
				return fmt.Errorf("send command chunk %d: %w", i, err)
			}
			// Réserve la place dans le flux agrégé pour la frame JSON que le secondaire va écrire.
			// La longueur est prédite ici en sérialisant de la même manière.
			fmt.Printf("[PRIMARY SERVER] Commande chunk impair #%d envoyée au secondaire (frameLen prédite=%d) : fileOff=%d size=%d streamOff=%d\n", i, frameLen, fileOffset, n, aggPos)
			aggPos += int64(frameLen)
		}
	}
	return nil
}
