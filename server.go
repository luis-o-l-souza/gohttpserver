package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type CustomHTTPRequest struct {
	Method  string
	Path    string
	Version string
	Headers map[string]string
	Body    []byte
}

type ServerStats struct {
	mu              sync.Mutex
	activeConns     int
	totalConns      int
	requestsHandled int
	bytesReceived   int64
	bytesSent       int64
}

var stats = &ServerStats{}

func main() {
	listener, err := net.Listen("tcp", ":8081")

	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to bind to port: %v\n", err)
		os.Exit(1)
	}

	defer listener.Close()

	fmt.Println("Server listening on :8081")

	go reportStats()

	for {
		conn, err := listener.Accept()

		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to accept connection: %v\n", err)
			continue
		}

		go handleConnection(conn)
	}
}

func handleConnection(conn net.Conn) {
	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(30 * time.Second))

	stats.mu.Lock()
	stats.activeConns++
	stats.totalConns++
	connId := stats.totalConns
	stats.mu.Unlock()

	defer func() {
		stats.mu.Lock()
		stats.activeConns--
		stats.mu.Unlock()
	}()

	fmt.Printf("[Conn %d] New connection from %s\n", connId, conn.RemoteAddr())
	reader := bufio.NewReader(conn)

	request, bytesRead, err := parseRequestStreaming(reader)

	if err != nil {
		fmt.Fprintf(os.Stderr, "[Conn %d] Error parsing request: %v\n", connId, err)
		sendResponse(conn, 400, "Bad Request", []byte("Invalid HTTP request"))
		return
	}

	stats.mu.Lock()
	stats.bytesReceived += int64(bytesRead)
	stats.mu.Unlock()

	fmt.Printf("[Conn %d] %s %s (body: %d bytes)\n", connId, request.Method, request.Path, len(request.Body))

	bytesSent := handleRequest(conn, request, connId)

	stats.mu.Lock()
	stats.requestsHandled++
	stats.bytesSent += int64(bytesSent)
	stats.mu.Unlock()

	fmt.Printf("[Conn %d] Request completed\n", connId)
}

func parseRequestStreaming(reader *bufio.Reader) (*CustomHTTPRequest, int, error) {
	totalBytes := 0

	requestLine, err := reader.ReadString('\n')
	if err != nil {
		return nil, 0, fmt.Errorf("failed to read request line: %v", err)
	}

	totalBytes += len(requestLine)

	requestLine = strings.TrimSpace(requestLine)

	parts := strings.Split(requestLine, " ")
	if len(parts) != 3 {
		return nil, totalBytes, fmt.Errorf("Invalid request line: %s", requestLine)
	}

	request := &CustomHTTPRequest{
		Method:  parts[0],
		Path:    parts[1],
		Version: parts[2],
		Headers: make(map[string]string),
	}

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, totalBytes, fmt.Errorf("failed to read header: %v", err)
		}
		totalBytes += len(line)

		line = strings.TrimSpace(line)

		if line == "" {
			break
		}

		colonIdx := strings.Index(line, ": ")
		if colonIdx == -1 {
			colonIdx = strings.Index(line, ":")

			if colonIdx == -1 {
				continue
			}
			request.Headers[line[:colonIdx]] = strings.TrimSpace(line[colonIdx+1:])
		} else {
			request.Headers[line[:colonIdx]] = line[colonIdx+2:]
		}
	}

	if contentLengthStr, exists := request.Headers["Content-Length"]; exists {
		contentLength, err := strconv.Atoi(contentLengthStr)
		if err != nil {
			return nil, totalBytes, fmt.Errorf("invalid content length: %v", err)
		}

		if contentLength > 0 {
			request.Body = make([]byte, contentLength)
			n, err := io.ReadFull(reader, request.Body)

			totalBytes += n

			if err != nil {
				return nil, totalBytes, fmt.Errorf("failed to read body: %v", err)
			}
		}
	}

	return request, totalBytes, nil
}

func handleRequest(conn net.Conn, request *CustomHTTPRequest, connId int) int {
	var responseBody []byte
	var statusCode int
	var statusText string

	switch request.Path {
	case "/":
		statusCode = 200
		statusText = "OK"
		responseBody = []byte("Welcome to the streaming HTTP server!")

	case "/echo":
		statusCode = 200
		statusText = "OK"
		if len(request.Body) > 0 {
			responseBody = []byte(fmt.Sprintf("Received %d bytes:\n%s", len(request.Body), string(request.Body)))
		} else {
			responseBody = []byte("No body received")
		}
	case "/large":
		statusCode = 200
		statusText = "OK"
		size := 100000
		responseBody = make([]byte, size)
		for i := range responseBody {
			responseBody[i] = byte('A' + (i % 26))
		}
	case "/stats":
		stats.mu.Lock()
		body := fmt.Sprintf("Active connection: %d\nTotal Connections: %d\nRequests handled: %d\nBytes Received: %d\nBytes Sent: %d", stats.activeConns, stats.totalConns, stats.requestsHandled, stats.bytesReceived, stats.bytesSent)
		stats.mu.Unlock()
		statusCode = 200
		statusText = "OK"
		responseBody = []byte(body)

	case "/slow":
		fmt.Printf("[Conn %d] Simulating slow request (5s)...\n", connId)
		time.Sleep(5 * time.Second)
		statusCode = 200
		statusText = "OK"
		responseBody = []byte("This response took 5 seconds")
	default:
		statusCode = 400
		statusText = "Not Found"
		responseBody = []byte("The requested path was not found")
	}

	return sendResponse(conn, statusCode, statusText, responseBody)
}

func sendResponse(conn net.Conn, statusCode int, statusText string, body []byte) int {
	header := fmt.Sprintf("HTTP/1.1 %d %s\r\n", statusCode, statusText)
	header += "Content-Type: text/plain\r\n"
	header += fmt.Sprintf("Content-Length: %d\r\n", len(body))
	header += "\r\n"

	headerBytes := []byte(header)
	conn.Write(headerBytes)

	conn.Write(body)

	return len(headerBytes) + len(body)
}

func reportStats() {
	ticker := time.NewTicker(10 * time.Second)

	defer ticker.Stop()

	for range ticker.C {
		stats.mu.Lock()
		fmt.Printf("\n=== Server Stats === \n")
		fmt.Printf("Active connections: %d\n", stats.activeConns)
		fmt.Printf("Total connections: %d\n", stats.totalConns)
		fmt.Printf("Requests handled: %d\n", stats.requestsHandled)
		fmt.Printf("Bytes received: %d (%.2f KB)\n", stats.bytesReceived, float64(stats.bytesReceived)/1024.0)
		fmt.Printf("Bytes sent: %d (%.2f KB)\n", stats.bytesSent, float64(stats.bytesSent)/1024.0)
		fmt.Printf("=============\n\n")
		stats.mu.Unlock()
	}
}
