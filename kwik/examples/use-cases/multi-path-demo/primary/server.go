// Multi-Path Demo Primary Server - Shows Secondary Stream Isolation
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"time"

	kwik "kwik/pkg"
	"kwik/pkg/session"
)

func main() {
	fmt.Println("=== PRIMARY SERVER MULTI-PATH DEMO ===")
	
	// 1. Il listen
	fmt.Println("[PRIMARY] 1. Starting listener on localhost:4433...")
	config := kwik.DefaultConfig()
	config.MaxPathsPerSession = 5
	
	listener, err := kwik.Listen("localhost:4433", config)
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()
	fmt.Println("[PRIMARY] ✅ Listening on localhost:4433")

	// 2. Accepte les sessions pour les handle
	for {
		fmt.Println("[PRIMARY] 2. Waiting for session...")
		session, err := listener.Accept(context.Background())
		if err != nil {
			log.Printf("[PRIMARY] Accept error: %v", err)
			continue
		}
		fmt.Println("[PRIMARY] ✅ Session accepted")

		// 3. Handle les sessions
		go handleSession(session)
	}
}

// 3. Handle les sessions en acceptant les stream pour les handle
func handleSession(sess session.Session) {
	defer sess.Close()
	fmt.Println("[PRIMARY] 3. Handling session...")

	// 4. Handle les stream
	for {
		fmt.Println("[PRIMARY] 4. Waiting for stream...")
		stream, err := sess.AcceptStream(context.Background())
		if err != nil {
			if err == io.EOF {
				fmt.Println("[PRIMARY] ✅ Session ended")
				return
			}
			log.Printf("[PRIMARY] Stream error: %v", err)
			return
		}
		fmt.Printf("[PRIMARY] ✅ Stream %d accepted\n", stream.StreamID())

		go handleStream(stream, sess)
	}
}

func handleStream(stream session.Stream, sess session.Session) {
	defer stream.Close()
	fmt.Printf("[PRIMARY] Handling stream %d...\n", stream.StreamID())

	// 5. Il lit un message
	fmt.Printf("[PRIMARY] 5. Reading message from stream %d...\n", stream.StreamID())
	buffer := make([]byte, 1024)
	n, err := stream.Read(buffer)
	if err != nil {
		log.Printf("[PRIMARY] Read error: %v", err)
		return
	}
	message := string(buffer[:n])
	fmt.Printf("[PRIMARY] ✅ Read: %s\n", message)

	// 6. Après avoir lu il ajoute un chemin avec addPath sur la session
	fmt.Printf("[PRIMARY] 6. Adding secondary path after reading message...\n")
	err = sess.AddPath("localhost:4434")
	if err != nil {
		log.Printf("[PRIMARY] Failed to add path: %v", err)
	} else {
		fmt.Println("[PRIMARY] ✅ Secondary path added: localhost:4434")
	}

	// 7. Il écrit une partie du message sur le flux
	fmt.Printf("[PRIMARY] 7. Writing partial response to stream %d...\n", stream.StreamID())
	partialResponse := fmt.Sprintf("Partie 1 de la réponse pour: %s", message)
	stream.SetOffset(0)
	_, err = stream.Write([]byte(partialResponse))
	if err != nil {
		log.Printf("[PRIMARY] Write error: %v", err)
		return
	}
	fmt.Printf("[PRIMARY] ✅ Wrote partial response: %s\n", partialResponse)

	// 8. Il fait un sendRawData pour le path qu'il a demandé d'ajouter
	fmt.Printf("[PRIMARY] 8. Sending raw data to secondary path...\n")
	time.Sleep(500 * time.Millisecond) // Wait for path to be established
	
	if serverSession, ok := sess.(*session.ServerSession); ok {
		pathID := serverSession.GetPendingPathID("localhost:4434")
		if pathID != "" {
			rawMessage := []byte(fmt.Sprintf("Données brutes pour serveur secondaire concernant: %s offset=%d", message, len(partialResponse)))
			err = sess.SendRawData(rawMessage, pathID, stream.RemoteStreamID())
			if err != nil {
				log.Printf("[PRIMARY] SendRawData error: %v", err)
			} else {
				fmt.Printf("[PRIMARY] ✅ Raw data sent to secondary path: %s\n", string(rawMessage))
			}
		} else {
			log.Printf("[PRIMARY] No path ID found for localhost:4434")
		}
	}

	// 9. Puis il lit encore dans le flux
	fmt.Printf("[PRIMARY] 9. Reading second message from stream %d...\n", stream.StreamID())
	n, err = stream.Read(buffer)
	if err != nil {
		log.Printf("[PRIMARY] Second read error: %v", err)
		return
	}
	secondMessage := string(buffer[:n])
	fmt.Printf("[PRIMARY] ✅ Read second message: %s\n", secondMessage)

	// 10. Il écrit dans le flux
	fmt.Printf("[PRIMARY] 10. Writing final response to stream %d...\n", stream.StreamID())
	finalResponse := fmt.Sprintf("Réponse finale pour: %s (avec données agrégées)", secondMessage)
	_, err = stream.Write([]byte(finalResponse))
	if err != nil {
		log.Printf("[PRIMARY] Final write error: %v", err)
		return
	}
	fmt.Printf("[PRIMARY] ✅ Wrote final response: %s\n", finalResponse)

	// 11. Il se ferme
	fmt.Printf("[PRIMARY] 11. Stream %d completed, closing...\n", stream.StreamID())
}
