// Multi-Path Demo Client - Shows client-side path monitoring
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	kwik "kwik/pkg"
	"kwik/pkg/session"
)

func main() {
	// Connect to server
	session, err := kwik.Dial(context.Background(), "localhost:4433", nil)
	if err != nil {
		log.Fatal(err)
	}
	defer session.Close()

	fmt.Println("[C->]: Connected to [PS]")

	// Start listening for proactive messages from secondary server
	go listenForSecondaryServerMessages(session)

	// Show initial paths
	showPaths(session, "Initial")

	// Send a message
	sendMessage(session, "Message de [C] à [PS], pour demande de ressources!")

	// Wait for server to add secondary path
	fmt.Println("[C->]: Waiting for [PS] to add secondary path...")
	time.Sleep(3 * time.Second)

	// Show updated paths
	showPaths(session, "After server path addition")

	// Send another message (may use different path)
	sendMessage(session, "Second Message à [PS] en provenance de [C]")

	// Wait longer to receive proactive messages from secondary server
	fmt.Println("[C->]: Waiting for proactive messages from [2S]...")
	time.Sleep(15 * time.Second)

	fmt.Println("Multi-path demo completed")
}

func showPaths(sess session.Session, label string) {
	paths := sess.GetActivePaths()
	fmt.Printf("\n%s paths (%d total):\n", label, len(paths))
	for i, path := range paths {
		fmt.Printf("  %d. %s (Primary: %v, Status: %s)\n",
			i+1, path.Address, path.IsPrimary, path.Status)
	}
	fmt.Println()
}

func sendMessage(sess session.Session, message string) {
	stream, err := sess.OpenStreamSync(context.Background())
	if err != nil {
		log.Printf("Failed to open stream: %v", err)
		return
	}
	defer stream.Close()

	// Send message
	_, err = stream.Write([]byte(message))
	if err != nil {
		log.Printf("Failed to send: %v", err)
		return
	}

	// Read response
	buffer := make([]byte, 1024)
	n, err := stream.Read(buffer)
	if err != nil {
		log.Printf("Failed to read: %v", err)
		return
	}

	fmt.Printf("Sent: %s\n", message)
	fmt.Printf("Received: %s\n", string(buffer[:n]))
}
// listenForSecondaryServerMessages listens for proactive messages from secondary server
func listenForSecondaryServerMessages(sess session.Session) {
	fmt.Println("[C->]: Starting listener for [2S] messages")
	
	// Wait a bit for secondary path to be established
	time.Sleep(1 * time.Second)
	
	// Start listeners for all active paths
	for {
		paths := sess.GetActivePaths()
		fmt.Printf("[C->]: Checking %d active paths for incoming streams\n", len(paths))
		
		// Try to accept streams from all paths
		for i, path := range paths {
			fmt.Printf("[C->]: Checking path %d: %s (Primary: %v)\n", i+1, path.Address, path.IsPrimary)
			
			// For secondary paths (non-primary), we need to listen directly on their connections
			if !path.IsPrimary {
				fmt.Printf("[C->]: Starting listener for secondary path: %s\n", path.Address)
				go listenOnSecondaryPath(sess, path)
			}
		}
		
		// Also listen on the main session (for primary path)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		stream, err := sess.AcceptStream(ctx)
		cancel()
		if err != nil {
			fmt.Printf("[C->]: Stream accept error on main session: %v\n", err)
			time.Sleep(1 * time.Second)
			continue
		}
		
		fmt.Printf("[C->]: Accepted incoming stream %d on main session\n", stream.StreamID())
		go handleIncomingSecondaryMessage(stream)
	}
}

// listenOnSecondaryPath listens for streams on a specific secondary path
func listenOnSecondaryPath(sess session.Session, path session.PathInfo) {
	fmt.Printf("[C->]: Starting dedicated listener for secondary path %s\n", path.Address)
	
	// Note: This is a conceptual approach - we need to access the underlying QUIC connection
	// In the current KWIK architecture, we might need to modify the session interface
	// to allow direct access to path connections for AcceptStream operations
	
	// For now, let's try a different approach - use the main session but with better error handling
	for {
		stream, err := sess.AcceptStream(context.Background())
		if err != nil {
			fmt.Printf("[C->]: Failed to accept stream on secondary path %s: %v\n", path.Address, err)
			time.Sleep(2 * time.Second)
			continue
		}
		
		fmt.Printf("[C->]: Accepted stream %d from secondary path %s\n", stream.StreamID(), path.Address)
		go handleIncomingSecondaryMessage(stream)
	}
}

// handleIncomingSecondaryMessage handles a proactive message from secondary server
func handleIncomingSecondaryMessage(stream session.Stream) {
	defer stream.Close()
	
	fmt.Printf("[C->]: Handling incoming message on stream %d\n", stream.StreamID())
	
	// Read the proactive message
	buffer := make([]byte, 1024)
	n, err := stream.Read(buffer)
	if err != nil {
		fmt.Printf("[C->]: Failed to read incoming message: %v\n", err)
		return
	}
	
	message := string(buffer[:n])
	fmt.Printf("🎉 [C->] RECEIVED PROACTIVE MESSAGE FROM [2S]: %s\n", message)
	
	// Optionally send an acknowledgment back
	ack := fmt.Sprintf("ACK: [C] a recu votre message à %s", time.Now().Format("15:04:05"))
	_, err = stream.Write([]byte(ack))
	if err != nil {
		fmt.Printf("[C->]: Failed to send ACK: %v\n", err)
		return
	}
	
	fmt.Printf("[C->]: Sent ACK back to secondary server\n")
}