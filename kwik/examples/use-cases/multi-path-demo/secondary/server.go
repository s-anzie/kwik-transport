// Multi-Path Demo Secondary Server - Shows Secondary Stream Isolation
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
	fmt.Println("=== SECONDARY SERVER MULTI-PATH DEMO ===")

	// 1. Il listen pour accepter les sessions
	fmt.Println("[SECONDARY] 1. Starting listener on localhost:4434...")
	config := kwik.DefaultConfig()
	config.MaxPathsPerSession = 5

	listener, err := kwik.Listen("localhost:4434", config)
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()
	fmt.Println("[SECONDARY] ✅ Listening on localhost:4434")

	// 2. Accepte les sessions et les handle
	for {
		fmt.Println("[SECONDARY] 2. Waiting for session...")
		session, err := listener.Accept(context.Background())
		if err != nil {
			log.Printf("[SECONDARY] Accept error: %v", err)
			continue
		}
		fmt.Println("[SECONDARY] ✅ Session accepted")

		// 3. Handle les sessions
		go handleSession(session)
	}
}

// 3. Handle les sessions et accepte les stream
func handleSession(sess session.Session) {
	defer sess.Close()
	fmt.Println("[SECONDARY] 3. Handling session...")

	// Vérifier que c'est bien un serveur secondaire
	if serverSession, ok := sess.(*session.ServerSession); ok {
		fmt.Printf("[SECONDARY] Server role: %s\n", serverSession.GetServerRole().String())
		serverSession.SetServerRole(session.ServerRoleSecondary)
		fmt.Printf("[SECONDARY] Server role set to: %s\n", serverSession.GetServerRole().String())
	}

	// 4. Accepte les stream et les handle
	for {
		fmt.Println("[SECONDARY] 4. Waiting for stream...")
		stream, err := sess.AcceptStream(context.Background())
		if err != nil {
			if err == io.EOF {
				fmt.Println("[SECONDARY] ✅ Session ended")
				return
			}
			log.Printf("[SECONDARY] Stream error: %v", err)
			return
		}
		fmt.Printf("[SECONDARY] ✅ Stream %d accepted\n", stream.StreamID())

		go handleStream(stream, sess)
	}
}

func handleStream(stream session.Stream, sess session.Session) {
	defer stream.Close()
	fmt.Printf("[SECONDARY] Handling stream %d...\n", stream.StreamID())

	// 5. Il lit dans les stream
	fmt.Printf("[SECONDARY] 5. Reading from stream %d...\n", stream.StreamID())
	buffer := make([]byte, 1024)
	n, err := stream.Read(buffer)
	if err != nil {
		log.Printf("[SECONDARY] Read error: %v", err)
		return
	}
	message := string(buffer[:n])
	fmt.Printf("[SECONDARY] ✅ Read: %s\n", message)

	// 6. Quand il lit il répond dans le même stream ou ouvre un autre stream et écrit dedans sa réponse
	fmt.Printf("[SECONDARY] 6. Deciding response method for stream %d...\n", stream.StreamID())
	offset := len(message) //normalement on doit recuperer l'offset dans le message car il sera formaté
	// Option A: Répondre dans le même stream
	stream.SetOffset(offset)
	
	fmt.Printf("[SECONDARY] 6a. Responding in same stream %d...\n", stream.StreamID())
	response := fmt.Sprintf("Réponse du serveur secondaire dans le même flux pour: %s", message)
	_, err = stream.Write([]byte(response))
	if err != nil {
		log.Printf("[SECONDARY] Write error: %v", err)
		return
	}
	fmt.Printf("[SECONDARY] ✅ Response sent in same stream: %s\n", response)

	// 7. Il attend un peu et se termine
	fmt.Printf("[SECONDARY] 7. Waiting before stream %d completion...\n", stream.StreamID())
	time.Sleep(1 * time.Second)
	fmt.Printf("[SECONDARY] ✅ Stream %d completed\n", stream.StreamID())
}
