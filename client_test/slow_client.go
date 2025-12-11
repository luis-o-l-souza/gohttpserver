package main

import (
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

    // Send request in small chunks with delays
    chunks := []string{
        "GET",
        " /hel",
        "lo HT",
        "TP/1.1\r\n",
        "Host: local",
        "host:8081\r\n",
        "\r\n",
    }

    for _, chunk := range chunks {
        fmt.Printf("Sending: %q\n", chunk)
        conn.Write([]byte(chunk))
        time.Sleep(500 * time.Millisecond) // Delay between chunks
    }

    // Read response
    buf := make([]byte, 4096)
    n, _ := conn.Read(buf)
    fmt.Printf("\nReceived:\n%s\n", string(buf[:n]))
}
