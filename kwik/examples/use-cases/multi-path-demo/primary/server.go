// Multi-Path Demo Server - Shows server-side path management
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
	// Configure for multi-path
	config := kwik.DefaultConfig()
	config.MaxPathsPerSession = 5

	listener, err := kwik.Listen("localhost:4433", config)
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()

	log.Println("Multi-path server listening on localhost:4433")

	for {
		session, err := listener.Accept(context.Background())
		if err != nil {
			log.Printf("Accept error: %v", err)
			continue
		}

		go handleMultiPathSession(session)
	}
}

func handleMultiPathSession(sess session.Session) {
	defer sess.Close()

	log.Printf("[PS->]: [C] connected with %d paths", len(sess.GetActivePaths()))

	// Add secondary path after connection (with small delay to ensure client is ready)
	log.Println("[PS->]: Add secondary path to localhost:4434...")

	// Give the client a moment to set up its control frame handler
	time.Sleep(500 * time.Millisecond)

	err := sess.AddPath("localhost:4434")
	if err != nil {
		log.Printf("ERROR: Failed to add path: %v", err)
	} else {
		log.Println("[PS->]: AddPath request sent successfully to [C]")
	}

	// Wait for secondary path to be established on client side
	time.Sleep(2 * time.Second)
	
	log.Println("[PS->]: Sending raw data to [2S]...")
	
	// Use the stored path ID from AddPath request
	// The server generated this ID and sent it to the client, so it should know it
	secondaryAddress := "localhost:4434"
	
	// Get the path ID that was generated during AddPath
	if serverSession, ok := sess.(*session.ServerSession); ok {
		pathID := serverSession.GetPendingPathID(secondaryAddress)
		if pathID != "" {
			rawMessage := []byte("Message en direction de [2S] par SendRawData! depuis [PS]")
			// log.Printf("DEBUG: Sending raw data to secondary path %s (address: %s)", pathID, secondaryAddress)
			err = sess.SendRawData(rawMessage, pathID)
			if err != nil {
				log.Printf("[PS->]: Failed to send raw data: %v", err)
			} else {
				log.Println("[PS->]: Raw data sent successfully using SendRawData API!")
			}
		} else {
			log.Printf("[PS->]: No pending path ID found for address %s", secondaryAddress)
		}
	} else {
		log.Println("[PS->]: Could not cast session to ServerSession")
	}

	// Handle streams
	for {
		stream, err := sess.AcceptStream(context.Background())
		if err != nil {
			if err == io.EOF {
				return
			}
			log.Printf("Stream error: %v", err)
			return
		}

		go handleStream(stream, sess)
	}
}

func handleStream(stream session.Stream, sess session.Session) {
	defer stream.Close()

	buffer := make([]byte, 1024)
	n, err := stream.Read(buffer)
	if err != nil {
		return
	}

	// Show path information
	paths := sess.GetActivePaths()
	response := fmt.Sprintf("Reponse du [PS] au [C] avec %d chemins pour: %s", len(paths), string(buffer[:n]))

	stream.Write([]byte(response))
}
