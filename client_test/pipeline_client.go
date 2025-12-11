package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

func main() {
	conn, err := net.Dial("tcp", "localhost:8081")

	if err != nil {
		panic(err)
	}

	defer conn.Close()

	reader := bufio.NewReader(conn)
 // Send multiple requests immediately (pipelined!)
    requests := []string{
        "GET /fast HTTP/1.1\r\nHost: localhost\r\n\r\n",
        "GET /slow HTTP/1.1\r\nHost: localhost\r\n\r\n",
        "GET /fast HTTP/1.1\r\nHost: localhost\r\n\r\n",
        "GET /fast HTTP/1.1\r\nHost: localhost\r\n\r\n",
    }

    fmt.Println("Sending all requests immediately (pipelined)...")
    start := time.Now()

    for i, req := range requests {
        fmt.Printf("Sending request %d\n", i+1)
        conn.Write([]byte(req))
    }

    fmt.Println("All requests sent! Waiting for responses...")

    // Read responses
    for i := 0; i < len(requests); i++ {
        fmt.Printf("\n=== Response %d ===\n", i+1)
        responseStart := time.Now()

        // Read status line
        statusLine, _ := reader.ReadString('\n')
        fmt.Printf("%s",statusLine)
        // Read headers
          contentLength := 0
          for {
              line, _ := reader.ReadString('\n')
              if strings.HasPrefix(line, "Content-Length:") {
                  fmt.Sscanf(line, "Content-Length: %d", &contentLength)
              }
              if line == "\r\n" {
                  break
              }
          }

          // Read body
          if contentLength > 0 {
              body := make([]byte, contentLength)
              io.ReadFull(reader, body)
              fmt.Printf("Body: %s\n", string(body))
          }

          fmt.Printf("Response received in %v\n", time.Since(responseStart))
      }

      fmt.Printf("\nTotal time: %v\n", time.Since(start))

}
