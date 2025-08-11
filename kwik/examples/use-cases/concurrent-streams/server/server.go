// Concurrent Streams Server - Handles multiple streams simultaneously
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"sync/atomic"

	kwik "kwik/pkg"
	"kwik/pkg/session"
)

var streamCounter int64

func main() {
	listener, err := kwik.Listen("localhost:4433", nil)
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()

	log.Println("Concurrent streams server listening on localhost:4433")

	for {
		session, err := listener.Accept(context.Background())
		if err != nil {
			log.Printf("Accept error: %v", err)
			continue
		}

		go handleConcurrentSession(session)
	}
}

func handleConcurrentSession(sess session.Session) {
	defer sess.Close()

	log.Println("Client connected for concurrent streams demo")

	for {
		stream, err := sess.AcceptStream(context.Background())
		if err != nil {
			if err == io.EOF {
				log.Println("Client disconnected")
				return
			}
			log.Printf("Stream error: %v", err)
			return
		}

		// Handle each stream concurrently
		go handleConcurrentStream(stream)
	}
}

func handleConcurrentStream(stream session.Stream) {
	defer stream.Close()

	streamNum := atomic.AddInt64(&streamCounter, 1)
	log.Printf("Handling stream #%d (ID: %d)", streamNum, stream.StreamID())

	buffer := make([]byte, 1024)
	n, err := stream.Read(buffer)
	if err != nil {
		log.Printf("Stream #%d read error: %v", streamNum, err)
		return
	}

	message := string(buffer[:n])
	response := fmt.Sprintf("Server processed stream #%d: %s", streamNum, message)

	_, err = stream.Write([]byte(response))
	if err != nil {
		log.Printf("Stream #%d write error: %v", streamNum, err)
		return
	}

	log.Printf("Stream #%d completed", streamNum)
}