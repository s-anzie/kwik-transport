// Raw Packets Server - Demonstrates custom protocol support
package main

import (
	"context"
	"fmt"
	"io"
	"log"

	kwik "kwik/pkg"
	"kwik/pkg/session"
)

func main() {
	listener, err := kwik.Listen("localhost:4433", nil)
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()

	log.Println("Raw packets server listening on localhost:4433")

	for {
		session, err := listener.Accept(context.Background())
		if err != nil {
			continue
		}

		go handleRawPacketSession(session)
	}
}

func handleRawPacketSession(sess session.Session) {
	defer sess.Close()

	// Handle regular streams
	go func() {
		for {
			stream, err := sess.AcceptStream(context.Background())
			if err != nil {
				return
			}
			go echoStream(stream)
		}
	}()

	// Demonstrate raw packet sending
	paths := sess.GetActivePaths()
	if len(paths) > 0 {
		// Send custom protocol data
		customData := []byte{0x01, 0x02, 0x03, 0x04, 0xFF}
		err := sess.SendRawData(customData, paths[0].PathID)
		if err != nil {
			log.Printf("Raw data send failed: %v", err)
		} else {
			log.Printf("Sent raw packet: %x", customData)
		}
	}

	// Keep session alive
	select {}
}

func echoStream(stream session.Stream) {
	defer stream.Close()

	buffer := make([]byte, 1024)
	n, err := stream.Read(buffer)
	if err != nil && err != io.EOF {
		return
	}

	response := fmt.Sprintf("Echo + Raw packet demo: %s", string(buffer[:n]))
	stream.Write([]byte(response))
}