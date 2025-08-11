// Simple Echo Server - Demonstrates basic KWIK server
package main

import (
	"context"
	"io"
	"log"

	kwik "kwik/pkg"
	"kwik/pkg/session"
)

func main() {
	// Listen for connections (identical to QUIC)
	listener, err := kwik.Listen("localhost:4433", nil)
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()

	log.Println("Echo server listening on localhost:4433")

	for {
		// Accept session
		session, err := listener.Accept(context.Background())
		if err != nil {
			log.Printf("Accept error: %v", err)
			continue
		}

		go handleSession(session)
	}
}

func handleSession(sess session.Session) {
	defer sess.Close()

	for {
		// Accept stream
		stream, err := sess.AcceptStream(context.Background())
		if err != nil {
			if err == io.EOF {
				return
			}
			log.Printf("Stream error: %v", err)
			return
		}

		go echoStream(stream)
	}
}

func echoStream(stream session.Stream) {
	defer stream.Close()

	buffer := make([]byte, 1024)
	n, err := stream.Read(buffer)
	if err != nil {
		log.Printf("Read error: %v", err)
		return
	}

	// Echo back
	_, err = stream.Write(buffer[:n])
	if err != nil {
		log.Printf("Write error: %v", err)
	}
}
