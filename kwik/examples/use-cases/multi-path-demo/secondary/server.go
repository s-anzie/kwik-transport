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

	log.Println("[2S->]: listening on localhost:4434")
	log.Println("[2S->]: Waiting for connections...")

	for {
		session, err := listener.Accept(context.Background())
		if err != nil {
			log.Printf("[2S->]: Session Accept error: %v", err)
			continue
		}

		log.Printf("[2S->] New connection accepted from [C]")
		go handleSecondaryPathSession(session)
	}
}

func handleSecondaryPathSession(sess session.Session) {
	defer sess.Close()

	log.Printf("[2S->]: handling session with %d paths", len(sess.GetActivePaths()))

	// Create a channel for triggering proactive responses
	proactiveDataChan := make(chan string, 10)

	// Start a goroutine to send proactive data responses only when triggered
	go sendProactiveDataResponses(sess, proactiveDataChan)

	// Handle all incoming streams (both regular and raw message streams)
	for {
		stream, err := sess.AcceptStream(context.Background())
		if err != nil {
			if err == io.EOF {
				log.Println("[2S->]: session ended")
				return
			}
			log.Printf("[2S->]: stream error: %v", err)
			return
		}

		log.Printf("[2S->]: received stream %d", stream.StreamID())
		go handleSecondaryStream(stream, sess, proactiveDataChan)
	}
}

func handleSecondaryStream(stream session.Stream, sess session.Session, proactiveDataChan chan string) {
	defer stream.Close()

	buffer := make([]byte, 1024)
	n, err := stream.Read(buffer)
	if err != nil {
		log.Printf("[2S->]: Stream read error: %v", err)
		return
	}

	message := string(buffer[:n])
	log.Printf("[2S->]: received message: %s", message)

	// Process the received message and generate a proactive response
	processedResponse := processRawMessage([]byte(message))

	// Trigger proactive response by sending data to the channel
	select {
	case proactiveDataChan <- processedResponse:
		log.Printf("[2S->]: triggered Response: %s", processedResponse)
	default:
		log.Printf("[2S->] proactive channel full, skipping response")
	}

	// Show path information from secondary server perspective
	paths := sess.GetActivePaths()
	response := fmt.Sprintf("Message de [2S] à [C] avec %d chemin en reponse à ': %s'", len(paths), message)

	_, err = stream.Write([]byte(response))
	if err != nil {
		log.Printf("[2S->] Stream write error: %v", err)
		return
	}

	log.Printf("[2S->] sent response: %s", response)
}

// processRawMessage processes the raw message and returns a response
func processRawMessage(messageData []byte) string {
	message := string(messageData)
	fmt.Printf("[2S->]: Processing message: %s\n", message)

	// Create a response based on the received message
	response := fmt.Sprintf("[2S->]: response to: %s [processed at %s]",
		message, time.Now().Format("15:04:05"))

	return response
}

// sendProactiveDataResponses sends proactive data responses to the client only when triggered
func sendProactiveDataResponses(sess session.Session, proactiveDataChan chan string) {
	fmt.Println("[2S->]: Starting proactive data response sender")
	fmt.Println("[2S->]: Will only send messages when there's data to process")

	counter := 1
	for {
		select {
		case responseData := <-proactiveDataChan:
			fmt.Printf("[2S->]: Received data to send proactively: %s\n", responseData)

			// Debug: Check session state and paths
			paths := sess.GetActivePaths()
			fmt.Printf("[2S->]: Has %d active paths\n", len(paths))
			for i, path := range paths {
				fmt.Printf("[2S->]: Path %d: ID=%s, Address=%s, Primary=%v\n", i+1, path.PathID, path.Address, path.IsPrimary)
			}

			// Create a new data stream to send proactive message
			stream, err := sess.OpenStreamSync(context.Background())
			if err != nil {
				fmt.Printf("[2S->]: Failed to open proactive stream: %v\n", err)
				continue
			}

			fmt.Printf("[2S->]: Successfully created stream %d for proactive response\n", stream.StreamID())

			// Send the processed response data
			_, err = stream.Write([]byte(responseData))
			if err != nil {
				fmt.Printf("[2S->]: Failed to send proactive response: %v\n", err)
				stream.Close()
				continue
			}

			fmt.Printf("[2S->]: Sent proactive response #%d: %s\n", counter, responseData)
			stream.Close()
			counter++

		default:
			// Check if we should continue running
			time.Sleep(100 * time.Millisecond)
		}
	}
}
