// Multi-Path Demo Secondary Server - Handles secondary path connections
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
	// Configure for multi-path secondary server
	config := kwik.DefaultConfig()
	config.MaxPathsPerSession = 5

	listener, err := kwik.Listen("localhost:4434", config)
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()

	log.Println("Multi-path SECONDARY server listening on localhost:4434")
	log.Println("Waiting for secondary path connections from clients...")

	for {
		session, err := listener.Accept(context.Background())
		if err != nil {
			log.Printf("Accept error: %v", err)
			continue
		}

		log.Printf("Secondary path connection accepted from client")
		go handleSecondaryPathSession(session)
	}
}

func handleSecondaryPathSession(sess session.Session) {
	defer sess.Close()

	log.Printf("Secondary server handling session with %d paths", len(sess.GetActivePaths()))

	// Create a channel for triggering proactive responses
	proactiveDataChan := make(chan string, 10)

	// Start a goroutine to send proactive data responses only when triggered
	go sendProactiveDataResponses(sess, proactiveDataChan)

	// Handle all incoming streams (both regular and raw message streams)
	for {
		stream, err := sess.AcceptStream(context.Background())
		if err != nil {
			if err == io.EOF {
				log.Println("Secondary path session ended")
				return
			}
			log.Printf("Secondary server stream error: %v", err)
			return
		}

		log.Printf("Secondary server received stream %d", stream.StreamID())
		go handleSecondaryStream(stream, sess, proactiveDataChan)
	}
}

func handleSecondaryStream(stream session.Stream, sess session.Session, proactiveDataChan chan string) {
	defer stream.Close()

	buffer := make([]byte, 1024)
	n, err := stream.Read(buffer)
	if err != nil {
		log.Printf("Secondary server read error: %v", err)
		return
	}

	message := string(buffer[:n])
	log.Printf("Secondary server received: %s", message)

	// Process the received message and generate a proactive response
	processedResponse := processRawMessage([]byte(message))

	// Trigger proactive response by sending data to the channel
	select {
	case proactiveDataChan <- processedResponse:
		log.Printf("Secondary server triggered proactive response: %s", processedResponse)
	default:
		log.Printf("Secondary server proactive channel full, skipping response")
	}

	// Show path information from secondary server perspective
	paths := sess.GetActivePaths()
	response := fmt.Sprintf("Secondary server echo from %d paths: %s", len(paths), message)

	_, err = stream.Write([]byte(response))
	if err != nil {
		log.Printf("Secondary server write error: %v", err)
		return
	}

	log.Printf("Secondary server sent response: %s", response)
}

// processRawMessage processes the raw message and returns a response
func processRawMessage(messageData []byte) string {
	message := string(messageData)
	fmt.Printf("DEBUG: Secondary server processing message: %s\n", message)

	// Create a response based on the received message
	response := fmt.Sprintf("Secondary server response to: %s [processed at %s]",
		message, time.Now().Format("15:04:05"))

	return response
}

// sendProactiveDataResponses sends proactive data responses to the client only when triggered
func sendProactiveDataResponses(sess session.Session, proactiveDataChan chan string) {
	fmt.Println("DEBUG: Secondary server starting proactive data response sender")
	fmt.Println("DEBUG: Secondary server will only send messages when there's data to process")

	counter := 1
	for {
		select {
		case responseData := <-proactiveDataChan:
			fmt.Printf("DEBUG: Secondary server received data to send proactively: %s\n", responseData)

			// Debug: Check session state and paths
			paths := sess.GetActivePaths()
			fmt.Printf("DEBUG: Secondary server has %d active paths\n", len(paths))
			for i, path := range paths {
				fmt.Printf("DEBUG: Path %d: ID=%s, Address=%s, Primary=%v\n", i+1, path.PathID, path.Address, path.IsPrimary)
			}

			// Create a new data stream to send proactive message
			stream, err := sess.OpenStreamSync(context.Background())
			if err != nil {
				fmt.Printf("DEBUG: Secondary server failed to open proactive stream: %v\n", err)
				continue
			}

			fmt.Printf("DEBUG: Secondary server successfully created stream %d for proactive response\n", stream.StreamID())

			// Send the processed response data
			_, err = stream.Write([]byte(responseData))
			if err != nil {
				fmt.Printf("DEBUG: Secondary server failed to send proactive response: %v\n", err)
				stream.Close()
				continue
			}

			fmt.Printf("DEBUG: Secondary server sent proactive response #%d: %s\n", counter, responseData)
			stream.Close()
			counter++

		default:
			// Check if we should continue running
			time.Sleep(100 * time.Millisecond)
		}
	}
}
