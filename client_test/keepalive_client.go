package main

import (
	"bufio"
	"fmt"
	"net"
	"time"
)

func main() {
	conn, err := net.Dial("tcp", "localhost:8081")

	if err != nil {
		panic(err)
	}

	defer conn.Close()

	reader := bufio.NewReader(conn)

	for i := 1; i <= 5; i++ {
		fmt.Printf("\n======= Sending request %d ======\n", i)

		request := fmt.Sprintf("GET /counter HTTP/1.1\r\nHost: localhost\r\n\r\n")
		conn.Write([]byte(request))

		statusLine, _ := reader.ReadString('\n')
		fmt.Printf("Status: %s", statusLine)

		for {
			line, _ := reader.ReadString('\n')
			if line == "\r\n" {
				break
			}

			fmt.Printf("Header: %s", line)
		}

		body, _ := reader.ReadString('\n')
		fmt.Printf("Body: %s", body)


		if i < 5 {
			time.Sleep(1 * time.Second)
		}
	}

	fmt.Println("\n==== All requests sent, waiting for timeout ===")
	time.Sleep(6 * time.Second)
}
