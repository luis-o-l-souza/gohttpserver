package main

import (
	"fmt"
	"net"
)

func main() {

	conn, err := net.Dial("tcp", "localhost:8081")
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	request := "POST /echo HTTP/1.1\r\n" +
	"Host: localhost:8081\r\n" +
	"Transfer-Encoding: chunked\r\n" +
	"\r\n" +
	"5\r\n" +
	"Hello\r\n" +
	"7\r\n" +
	", World\r\n" +
	"1\r\n" +
	"!\r\n" +
	"0\r\n" +
	"\r\n"


	fmt.Println("Sending chunked request:")
	fmt.Println(request)

	conn.Write([]byte(request))

	buf := make([]byte, 4096)
	n, _ := conn.Read(buf)
	fmt.Printf("\nReceived response:\n%s\n", string(buf[:n]))
}
