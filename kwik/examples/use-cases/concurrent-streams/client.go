// Concurrent Streams Client - Shows multiple streams usage
package main

import (
	"context"
	"fmt"
	"log"
	"sync"

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

	// Launch multiple concurrent streams
	numStreams := 5
	var wg sync.WaitGroup

	for i := 0; i < numStreams; i++ {
		wg.Add(1)
		go func(streamNum int) {
			defer wg.Done()
			sendMessage(session, streamNum)
		}(i)
	}

	wg.Wait()
	fmt.Println("All streams completed")
}

func sendMessage(sess session.Session, streamNum int) {
	// Open stream
	stream, err := sess.OpenStreamSync(context.Background())
	if err != nil {
		log.Printf("Stream %d: Failed to open: %v", streamNum, err)
		return
	}
	defer stream.Close()

	// Send message
	message := fmt.Sprintf("Message from stream %d", streamNum)
	_, err = stream.Write([]byte(message))
	if err != nil {
		log.Printf("Stream %d: Write error: %v", streamNum, err)
		return
	}

	// Read response
	buffer := make([]byte, 1024)
	n, err := stream.Read(buffer)
	if err != nil {
		log.Printf("Stream %d: Read error: %v", streamNum, err)
		return
	}

	fmt.Printf("Stream %d: %s\n", streamNum, string(buffer[:n]))
}
