// Multi-Path Demo Client - Shows Secondary Stream Isolation
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"kwik/examples/use-cases/multi-path-demo/types"
	kwik "kwik/pkg"
)

func main() {
	fmt.Println("\n\n[CLIENT] Démarrage du client multi-path demo")

	// 1. Configuration avec logs détaillés
	fmt.Println("[CLIENT] Connexion au serveur primaire localhost:4433...")
	config := kwik.DefaultConfig()

	// Activer les logs de débogage
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
	// Transforme le message initial en une requête de fichier JSON
	req := types.Request{Type: "file", Resource: "sample.txt"}
	payload, err := json.Marshal(req)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("[CLIENT] Envoi de la requête de fichier: %s\n", string(payload))
	_, err = stream.Write(payload)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("[CLIENT] Requête envoyée, attente de la réponse...")

	// 2bis. Lecture de la réponse (avec gestion de l'encapsulation)
	reader := bufio.NewReader(stream)
	
	// Lire les données brutes
	data, err := reader.ReadBytes('\n')
	if err != nil {
		log.Fatalf("[CLIENT] Erreur lecture réponse: %v", err)
	}
	
	// Afficher les premiers octets pour le débogage
	dbgLen := len(data)
	if dbgLen > 100 {
		dbgLen = 100
	}
	fmt.Printf("[CLIENT] Données brutes reçues (%d octets): %x...\n", len(data), data[:dbgLen])
	
	// Essayer de trouver le début du JSON (première occurrence de '{' ou '[')
	jsonStart := 0
	for i, b := range data {
		if b == '{' || b == '[' {
			jsonStart = i
			break
		}
	}
	
	// Extraire et afficher la partie JSON
	jsonData := data[jsonStart:]
	fmt.Printf("[CLIENT] Données JSON extraites (%d octets): %s\n", len(jsonData), string(jsonData))
	
	// Décoder la réponse JSON
	var resp types.Response
	if err := json.Unmarshal(jsonData, &resp); err != nil {
		log.Fatalf("[CLIENT] Réponse invalide: %v (données: %x...)", err, data[:dbgLen])
	}
	if !resp.Success {
		log.Fatalf("[CLIENT] Requête échouée: %s", resp.Error)
	}
	if resp.File == nil {
		log.Fatalf("[CLIENT] Réponse succès sans informations de fichier")
	}
	fmt.Printf("[CLIENT] Réponse OK: name=%s size=%d chunks=%d chunkSize=%d\n", resp.File.Name, resp.File.Size, resp.File.NumChunks, resp.File.ChunkSize)

	// 4. Prépare le fichier de destination
	destName := "received_" + filepath.Base(resp.File.Name)
	out, err := os.Create(destName)
	if err != nil {
		log.Fatalf("[CLIENT] Impossible de créer le fichier destination '%s': %v", destName, err)
	}
	defer out.Close()
	fmt.Printf("[CLIENT] Écriture vers '%s'\n", destName)

	// 5. Boucle de lecture: lire des frames JSON Chunk (terminées par '\n'),
	// décapsuler et écrire au bon offset jusqu'à atteindre la taille du fichier.
	var received int64 = 0
	chunkCount := 0

	log.Println("[DEBUG] Début de la réception des données du fichier")
	log.Printf("[DEBUG] Taille attendue: %d octets, %d chunks annoncés\n", resp.File.Size, resp.File.NumChunks)

	for received < resp.File.Size {
		// Lire une ligne complète
		line, err := reader.ReadBytes('\n')
		if err != nil {
			log.Fatalf("[CLIENT] Erreur lecture frame chunk: %v", err)
		}

		// Afficher les premiers octets pour le débogage
		dbgLen := len(line)
		if dbgLen > 100 {
			dbgLen = 100
		}
		log.Printf("[DEBUG] Chunk brut reçu (%d octets): %x...\n", len(line), line[:dbgLen])

		// Essayer de décoder le JSON
		var ch types.Chunk
		if uerr := json.Unmarshal(line, &ch); uerr != nil {
			log.Printf("[WARN] Erreur décodage chunk JSON: %v (premiers octets: %x...)", uerr, line[:dbgLen])
			continue
		}

		chunkCount++
		log.Printf("[DEBUG] Chunk #%d décodé: id=%d, offset=%d, taille=%d, données=%d octets\n",
			chunkCount, ch.Id, ch.Offset, ch.Size, len(ch.Data))

		// Écrire les données au bon offset
		data := ch.Data
		if len(data) == 0 {
			log.Printf("[WARN] Chunk %d reçu sans données", ch.Id)
			continue
		}

		// Vérifier que l'offset est valide
		offset64 := int64(ch.Offset)
		if offset64 < 0 || offset64 > resp.File.Size {
			log.Printf("[WARN] Offset invalide %d pour le chunk %d (taille fichier: %d)",
				offset64, ch.Id, resp.File.Size)
			continue
		}

		// Écrire les données
		n, werr := out.WriteAt(data, int64(ch.Offset))
		if werr != nil {
			log.Fatalf("[CLIENT] Erreur écriture fichier (chunk %d): %v", ch.Id, werr)
		}

		received += int64(n)
		progress := float64(received) * 100 / float64(resp.File.Size)

		log.Printf("[STATUS] Chunk %d écrit: %d octets à l'offset %d (total: %d/%d, %.1f%%)\n",
			ch.Id, n, ch.Offset, received, resp.File.Size, progress)
	}
	fmt.Println("[CLIENT] Réception terminée avec succès")

	// Envoie un ACK de fin de réception au serveur primaire
	ack := types.Request{Type: "done", Resource: resp.File.Name}
	ackPayload, _ := json.Marshal(ack)
	if _, err := stream.Write(append(ackPayload, '\n')); err != nil {
		log.Printf("[CLIENT] Erreur lors de l'envoi de l'ACK: %v", err)
	} else {
		fmt.Println("[CLIENT] ACK de fin envoyé au serveur primaire")
	}
	fmt.Println("[CLIENT] Fin du client")
}
