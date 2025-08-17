// Multi-Path Demo Primary Server - Shows Secondary Stream Isolation
package main

import (
	"context"
	"io"
	"log"
	"time"

	kwik "kwik/pkg"
	"kwik/pkg/session"
)

func main() {
	config := kwik.DefaultConfig()
	config.MaxPathsPerSession = 5
	
	listener, err := kwik.Listen("localhost:4433", config)
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()

	for {
		session, err := listener.Accept(context.Background())
		if err != nil {
			continue
		}
		go handleSession(session)
	}
}

func handleSession(sess session.Session) {
	defer sess.Close()

	for {
		stream, err := sess.AcceptStream(context.Background())
		if err != nil {
			if err == io.EOF {
				return
			}
			return
		}
		go handleStream(stream, sess)
	}
}

func handleStream(stream session.Stream, sess session.Session) {
	defer stream.Close()
	nextoffset := 0

	// Lit le message du client
	buffer := make([]byte, 1024)
	_, err := stream.Read(buffer)
	if err != nil {
		return
	}

	// Ajoute un chemin secondaire
	sess.AddPath("localhost:4434")

	// Écrit la réponse primaire
	partialResponse := "salut comment vas tu"
	stream.SetOffset(nextoffset)
	stream.Write([]byte(partialResponse))

	// Envoie des données au serveur secondaire
	time.Sleep(500 * time.Millisecond)
	
	if serverSession, ok := sess.(*session.ServerSession); ok {
		pathID := serverSession.GetPendingPathID("localhost:4434")
		if pathID != "" {
			primaryResponseLength := len(partialResponse)
			nextoffset += primaryResponseLength
			rawMessage := []byte("Second Message 2")
			sess.SendRawData(rawMessage, pathID, stream.RemoteStreamID())
		}
	}

	time.Sleep(2 * time.Second)
}
