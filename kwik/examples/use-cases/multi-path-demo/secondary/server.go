// Multi-Path Demo Secondary Server - Shows Secondary Stream Isolation
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

	listener, err := kwik.Listen("localhost:4434", config)
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

	// Configure comme serveur secondaire
	if serverSession, ok := sess.(*session.ServerSession); ok {
		serverSession.SetServerRole(session.ServerRoleSecondary)
	}

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

	// Lit le message du serveur primaire
	buffer := make([]byte, 1024)
	stream.Read(buffer)

	// Ouvre un nouveau stream secondaire pour la réponse
	offset := 21 // Après "salut comment vas tu"
	
	newStream, err := sess.OpenStreamSync(context.Background())
	if err != nil {
		return
	}
	defer newStream.Close()

	// Configure le stream pour l'agrégation
	newStream.SetOffset(offset)
	newStream.SetRemoteStreamID(1) // Stream KWIK cible

	// Envoie la réponse
	response := "Salut, ça fait longtemps"
	newStream.Write([]byte(response))

	time.Sleep(1 * time.Second)
}
