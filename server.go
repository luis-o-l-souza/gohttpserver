package main

import (
	"bufio"
	"bytes"
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
	writer := bufio.NewWriter(conn)

	request, bytesRead, err := parseRequestStreaming(reader)

	if err != nil {
		fmt.Fprintf(os.Stderr, "[Conn %d] Error parsing request: %v\n", connId, err)
		sendResponse(writer, 400, "Bad Request", []byte("Invalid HTTP request"), false)
		writer.Flush()
		return
	}

	stats.mu.Lock()
	stats.bytesReceived += int64(bytesRead)
	stats.mu.Unlock()

	fmt.Printf("[Conn %d] %s %s (body: %d bytes)\n", connId, request.Method, request.Path, len(request.Body))

	bytesSent := handleRequest(writer, request, connId)
	writer.Flush()

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

	transferEncoding := strings.ToLower(request.Headers["Transfer-Encoding"])

	if transferEncoding == "chunked" {
		body, bytesRead, err := readChunkedBody(reader)
		totalBytes += bytesRead
		if err != nil {
			return nil, totalBytes, fmt.Errorf("failed to read chunked body: %v", err)
		}

		request.Body = body
		fmt.Printf("Read chunked body: %d bytes total\n", len(body))
	} else if contentLengthStr, exists := request.Headers["Content-Length"]; exists {
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

func readChunkedBody(reader *bufio.Reader) ([]byte, int, error) {
	var body bytes.Buffer

	totalBytesRead := 0

	for {
		sizeLine, err := reader.ReadString('\n')
		fmt.Printf("sizeLine size: %v", sizeLine)
		if err != nil {
			return nil, totalBytesRead, fmt.Errorf("error reading chunk size: %v", err)
		}

		totalBytesRead += len(sizeLine)

		sizeLine = strings.TrimSpace(sizeLine)

		if idx := strings.Index(sizeLine, ";"); idx != -1 {
			sizeLine = sizeLine[:idx]
		}


		chunkSize, err := strconv.ParseInt(sizeLine, 16, 64)
		fmt.Printf("chunkSize size: %v", chunkSize)

		if err != nil {
			return nil, totalBytesRead, fmt.Errorf("invalid chunk size '%s': %v", sizeLine, err)
		}

		fmt.Printf(" Chunk size 0x%s = %d bytes\n", sizeLine, chunkSize)


		if chunkSize == 0 {
			finalLine, err := reader.ReadString('\n')
			if err != nil {
				return nil, totalBytesRead, fmt.Errorf("error reading final CRLF: %v", err)
			}

			totalBytesRead += len(finalLine)
			break
		}

		chunkData := make([]byte, chunkSize)
		n, err := io.ReadFull(reader, chunkData)
		totalBytesRead += n

		if err != nil {
			return nil, totalBytesRead, fmt.Errorf("error reading chunk data: %v", err)
		}

		body.Write(chunkData)

		trailingCRLF := make([]byte, 2)
		n, err = io.ReadFull(reader, trailingCRLF)
		totalBytesRead += n

		if err != nil {
			return nil, totalBytesRead, fmt.Errorf("error reading chunk trailing CRLF: %v", err)
		}

		if string(trailingCRLF) != "\r\n" {
			return nil, totalBytesRead, fmt.Errorf("expected CRLF after chunk, got %v", trailingCRLF)
		}
	}

	return body.Bytes(), totalBytesRead, nil
}

func handleRequest(writer *bufio.Writer, request *CustomHTTPRequest, connId int) int {
	var responseBody []byte
	var statusCode int
	var statusText string
	chunked := false
	switch request.Path {
	case "/":
		statusCode = 200
		statusText = "OK"
		responseBody = []byte("Welcome to the streaming HTTP server!")
	case "/stream":
			statusCode = 200
			statusText = "OK"
			chunked = true
			return sendChunkedResponse(writer, statusCode, statusText, connId)
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

	return sendResponse(writer, statusCode, statusText, responseBody, chunked)
}

func sendResponse(writer *bufio.Writer, statusCode int, statusText string, body []byte, chunked bool) int {
	header := fmt.Sprintf("HTTP/1.1 %d %s\r\n", statusCode, statusText)
	header += "Content-Type: text/plain\r\n"

	if !chunked {
		header += fmt.Sprintf("Content-Length: %d\r\n", len(body))
	} else {
		header += "Transfer-Encoding: chunked\r\n"
	}

	header += "Connection: close\r\n"
	header += "\r\n"

	headerBytes := []byte(header)
	writer.Write(headerBytes)

	totalSent := len(headerBytes)

	if !chunked {
		writer.Write(body)
		totalSent += len(body)
	}

	return totalSent
}

func sendChunkedResponse(writer *bufio.Writer, statusCode int, statusText string, connId int) int {
	header := fmt.Sprintf("HTTP/1.1 %d %s\r\n", statusCode, statusText)
	header += "Content-Type: text/plain\r\n"
	header += "Transfer-Encoding: chunked\r\n"
	header += "Connection: close\r\n"
	header += "\r\n"

	writer.Write([]byte(header))
	totalSent := len(header)

	messages := []string{
		"Chunk 1: starting stream...\n",
		"Chunk 2: Processing data....\n",
		"Chunk 3: Almost done...\n",
		"Chunk 4: Complete!\n",
	}

	for i, msg := range messages {
		fmt.Printf("[Conn %d] Sending chunk %d\n", connId, i+1)

		chunkSize := fmt.Sprintf("%x\r\n", len(msg))
		writer.Write([]byte(chunkSize))
		totalSent += len(chunkSize)


		writer.Write([]byte(msg))
		totalSent += len(msg)

		writer.Write([]byte("\r\n"))
		totalSent += 2

		writer.Flush()

		time.Sleep(1 * time.Second)
	}

	writer.Write([]byte("0\r\n\r\n"))
	totalSent += 5

	fmt.Printf("[Conn %d] Chunked response complete\n", connId)

	return totalSent
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
