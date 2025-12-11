package main

import (
	"bufio"
	"bytes"
	"context"
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
	SequenceNum int
}

type CustomHTTPResponse struct {
	StatusCode int
	StatusText string
	Headers map[string]string
	Body []byte
	SequenceNum int
}

type ServerStats struct {
	mu              sync.Mutex
	activeConns     int
	totalConns      int
	requestsHandled int
	bytesReceived   int64
	bytesSent       int64
	keepAliveConns int
	keepAliveRequests int64
	pipelinedRequests int64
}

var stats = &ServerStats{}

const (
	keepAliveTimeout = 5 * time.Second
	maxKeepAliveRequests = 100
	requestTimeout = 30 * time.Second
	maxPipelineDepth = 10
)

func main() {
	listener, err := net.Listen("tcp", ":8081")

	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to bind to port: %v\n", err)
		os.Exit(1)
	}

	defer listener.Close()

	fmt.Println("Server listening on :8081")
	fmt.Printf("Keep-alive timeout: %v\n", keepAliveTimeout)
	fmt.Printf("Max requests per connection: %d\n", maxKeepAliveRequests)
	fmt.Printf("HTTP Pipelining enabled (max depth %d)\n", maxPipelineDepth)

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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	requestChan := make(chan *CustomHTTPRequest, maxPipelineDepth)
	responseChan := make(chan *CustomHTTPResponse, maxPipelineDepth)

	errChan := make(chan error, 3)

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	go readRequests(ctx, reader, requestChan, errChan, connId, conn)

	go processRequests(ctx, requestChan, responseChan, errChan, connId)

	go writeResponses(ctx, writer, responseChan, errChan, connId)

	err := <-errChan

	cancel()

	close(requestChan)
	close(responseChan)

	if err != nil {
		if err != io.EOF {
			fmt.Printf("[Conn %d] Connection error: %v\n", connId, err)
		} else {
			fmt.Printf("[Conn %d] Client closed connection\n", connId)
		}
	}

	fmt.Printf("[Conn %d] Connection closed\n", connId)
	/** keepAlive := true
	requestCount := 0


	for keepAlive && requestCount < maxKeepAliveRequests {
		requestCount++
		if requestCount > 1 {
			conn.SetReadDeadline(time.Now().Add(keepAliveTimeout))
		} else {
			conn.SetReadDeadline(time.Now().Add(requestTimeout))
		}

		request, bytesRead, err := parseRequestStreaming(reader)

		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				if requestCount > 1 {
					fmt.Printf("[Conn %d] Keep-alive timeout after %d requests\n", connId, requestCount - 1)
				} else {
					fmt.Printf("[Conn %d] timeout on first request\n", connId)
				}
			} else {
				fmt.Fprintf(os.Stderr, "[Conn %d] Error parsing request: %v\n", connId, err)
				if requestCount == 1 {
					sendResponse(writer, 400, "Bad Request", []byte("Invalid HTTP request"), false)
					writer.Flush()
				}
			}
			break
		}
		stats.mu.Lock()
		stats.bytesReceived += int64(bytesRead)
		if requestCount > 1 {
			stats.keepAliveRequests++
		}
		stats.mu.Unlock()

		fmt.Printf("[Conn %d-%d] %s %s\n", connId, requestCount, request.Method, request.Path)

		connectionHeader := strings.ToLower(request.Headers["Connection"])
		clientWantsClose := connectionHeader == "close"

		shouldKeepAlive := !clientWantsClose &&
		requestCount < maxKeepAliveRequests &&
		request.Version == "HTTP/1.1"

		bytesSent := handleRequest(writer, request, connId, requestCount, shouldKeepAlive)
		writer.Flush()

		stats.mu.Lock()
		stats.requestsHandled++
		stats.bytesSent += int64(bytesSent)
		stats.mu.Unlock()


		keepAlive = shouldKeepAlive

		if !keepAlive {
			if clientWantsClose {
				fmt.Printf("[Conn %d] Client requested connection close\n", connId)
			} else if requestCount >= maxKeepAliveRequests {
				fmt.Printf("[Conn %d] Max requests reached\n", connId)
			}

			break
		}

		if requestCount == 1 && keepAlive {
			stats.mu.Lock()
			stats.keepAliveConns++
			stats.mu.Unlock()
		}
	}

	fmt.Printf("[Conn %d] Connection closed after %d requests\n", connId, requestCount)
	**/
}

func readRequests(ctx context.Context, reader *bufio.Reader, requestChan chan<- *CustomHTTPRequest, errChan chan<- error, connId int, conn net.Conn) {
	sequenceNum := 0
	requestCount := 0

	for {
		select {
			case <- ctx.Done():
				return
			default:
		}

		if requestCount > 0 {
			conn.SetReadDeadline(time.Now().Add(keepAliveTimeout))
		} else {
			conn.SetReadDeadline(time.Now().Add(requestTimeout))
		}

		request, bytesRead, err := parseRequestStreaming(reader)

		if err != nil {

			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				if requestCount > 0 {
					fmt.Printf("[Conn %d] Keep-alive timeout after %d requests\n", connId, requestCount)
					errChan <- io.EOF
					return
				}
			}
			errChan <- err
			return
		}

		sequenceNum++
		requestCount++

		request.SequenceNum = sequenceNum

		stats.mu.Lock()
		stats.bytesReceived += int64(bytesRead)

		if requestCount > 1 {
			stats.pipelinedRequests++
		}

		stats.mu.Unlock()

		fmt.Printf("[Conn %d-%d] Received: %s %s\n", connId, sequenceNum, request.Method, request.Path)

		select {
			case requestChan <- request:

			case <- ctx.Done():
			return
		}

		if strings.ToLower(request.Headers["Connection"]) == "close" {
			fmt.Printf("[Conn %d] Client requested close\n", connId)

			time.Sleep(100 * time.Millisecond)
			errChan <- io.EOF
			return
		}

		if requestCount >= maxKeepAliveRequests {
			fmt.Printf("[Conn %d] Max requests reached\n", connId)
			errChan <- io.EOF
			return
		}
	}
}
func processRequests(ctx context.Context, requestChan <-chan *CustomHTTPRequest, responseChan chan<- *CustomHTTPResponse, errChan chan <- error, connId int) {
	for {
		select {
			case <-ctx.Done():
				return
			case request, ok := <-requestChan:
			if !ok {
				return
			}

			fmt.Printf("[Conn %d-%d] Processing: %s %s\n", connId, request.SequenceNum, request.Method, request.Path)

			response := handleRequestPipeline(request, connId)

			fmt.Printf("[Conn %d-%d] Completed: %s %s (%d)\n", connId, request.SequenceNum, request.Method, request.Path, response.StatusCode)

			select {
				case responseChan <- response:

				case <- ctx.Done():
					return
			}
		}
	}

}

func writeResponses(ctx context.Context, writer *bufio.Writer, responseChan <- chan *CustomHTTPResponse, errChan chan <-error, connId int) {
	for {
		select {
			case <- ctx.Done():
				return

			case response, ok := <-responseChan:
				if !ok {
					return
				}

				fmt.Printf("[Conn %d-%d] Sending response\n", connId, response.SequenceNum)

				bytesSent := writeResponse(writer, response)
				writer.Flush()

				stats.mu.Lock()
				stats.requestsHandled++
				stats.bytesSent += int64(bytesSent)
				stats.mu.Unlock()
		}
	}
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

func handleRequestPipeline(request *CustomHTTPRequest, connId int) *CustomHTTPResponse {
	var body []byte
	var statusCode int
	var statusText string

	switch request.Path {
    case "/":
        statusCode = 200
        statusText = "OK"
        body = []byte("Welcome to the pipelined HTTP server!")

    case "/fast":
        statusCode = 200
        statusText = "OK"
        body = []byte("Fast response!")

    case "/slow":
        // Simulate slow processing
        time.Sleep(3 * time.Second)
        statusCode = 200
        statusText = "OK"
        body = []byte("Slow response (took 3 seconds)")

    case "/echo":
        statusCode = 200
        statusText = "OK"
        if len(request.Body) > 0 {
            body = []byte(fmt.Sprintf("Received %d bytes:\n%s", len(request.Body), string(request.Body)))
        } else {
            body = []byte("No body received")
        }

    case "/stats":
        stats.mu.Lock()
        bodyStr := fmt.Sprintf("Active connections: %d\n"+
            "Total connections: %d\n"+
            "Requests handled: %d\n"+
            "Keep-alive connections: %d\n"+
            "Keep-alive requests: %d\n"+
            "Pipelined requests: %d\n"+
            "Bytes received: %d (%.2f KB)\n"+
            "Bytes sent: %d (%.2f KB)\n",
            stats.activeConns, stats.totalConns, stats.requestsHandled,
            stats.keepAliveConns, stats.keepAliveRequests, stats.pipelinedRequests,
            stats.bytesReceived, float64(stats.bytesReceived)/1024.0,
            stats.bytesSent, float64(stats.bytesSent)/1024.0)
        stats.mu.Unlock()
        statusCode = 200
        statusText = "OK"
        body = []byte(bodyStr)

    default:
        statusCode = 404
        statusText = "Not Found"
        body = []byte("The requested path was not found")
    }

    response := &CustomHTTPResponse{
    StatusCode: statusCode,
    StatusText: statusText,
    Headers: make(map[string]string),
    Body: body,
    SequenceNum: request.SequenceNum,
    }

    response.Headers["Content-Type"] = "text/plain"
    response.Headers["Content-Length"] = strconv.Itoa(len(body))
    response.Headers["Connection"] = "keep-alive"

    return response
}

func writeResponse(writer *bufio.Writer, response *CustomHTTPResponse) int {
	statusLine := fmt.Sprintf("HTTP/1.1 %d %s\r\n", response.StatusCode, response.StatusText)

	writer.WriteString(statusLine)

	totalSent := len(statusLine)

	for key, value := range response.Headers {
		header := fmt.Sprintf("%s: %s\r\n", key, value)
		writer.WriteString(header)
		totalSent += len(header)
	}


	writer.WriteString("\r\n")
	totalSent += 2

	writer.Write(response.Body)
	totalSent += len(response.Body)

	return totalSent
}

func handleRequest(writer *bufio.Writer, request *CustomHTTPRequest, connId int, requestNum int, keepAlive bool) int {
	var responseBody []byte
	var statusCode int
	var statusText string
	switch request.Path {
	case "/":
		statusCode = 200
		statusText = "OK"
		responseBody = []byte(fmt.Sprintf("Welcome to the streaming HTTP server! Connection will %s after this request.\n", map[bool]string{true: "stay open", false: "close"}[keepAlive]))
	case "/stream":
		statusCode = 200
		statusText = "OK"
		return sendChunkedResponse(writer, statusCode, statusText, connId, keepAlive)
	case "/counter":
		statusCode = 200
		statusText = "OK"
		responseBody = []byte(fmt.Sprintf("This is request #%d on this connection\n", requestNum))
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
		body := fmt.Sprintf("Active connection: %d\n" +
			"Total Connections: %d\n" +
			"Requests handled: %d\n" +
			"Bytes Received: %d\n" +
			"Bytes Sent: %d" +
			"Keep-alive connections: %d\n" +
			"Keep-alive requests: %d\n", stats.activeConns, stats.totalConns, stats.requestsHandled, stats.bytesReceived, stats.bytesSent, stats.keepAliveConns, stats.keepAliveRequests)
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

	return sendResponse(writer, statusCode, statusText, responseBody, keepAlive)
}

func sendResponse(writer *bufio.Writer, statusCode int, statusText string, body []byte, keepAlive bool) int {
	header := fmt.Sprintf("HTTP/1.1 %d %s\r\n", statusCode, statusText)
	header += "Content-Type: text/plain\r\n"
	header += fmt.Sprintf("Content-Length: %d\r\n", len(body))

	if keepAlive {
		header += "Connection: keep-alive\r\n"
		header += fmt.Sprintf("Keep-Alive: timeout=%d, max=%d\r\n", int(keepAliveTimeout.Seconds()), maxKeepAliveRequests)
	} else {
		header += "Connection: close\r\n"
	}
	header += "\r\n"

	headerBytes := []byte(header)
	writer.Write(headerBytes)
	writer.Write(body)

	return len(headerBytes) + len(body)
}

func sendChunkedResponse(writer *bufio.Writer, statusCode int, statusText string, connId int, keepAlive bool) int {
	header := fmt.Sprintf("HTTP/1.1 %d %s\r\n", statusCode, statusText)
	header += "Content-Type: text/plain\r\n"
	header += "Transfer-Encoding: chunked\r\n"

	if keepAlive {
		header += "Connection: keep-alive\r\n"
	} else {
		header += "Connection: close\r\n"
	}

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
		fmt.Printf("Keep-alive connections: %d\n", stats.keepAliveConns)
		fmt.Printf("Keep-alive requests: %d\n", stats.keepAliveRequests)
	  	fmt.Printf("Pipelined requests: %d\n", stats.pipelinedRequests)
		fmt.Printf("Bytes received: %d (%.2f KB)\n", stats.bytesReceived, float64(stats.bytesReceived)/1024.0)
		fmt.Printf("Bytes sent: %d (%.2f KB)\n", stats.bytesSent, float64(stats.bytesSent)/1024.0)

		if stats.totalConns > 0 {
			avgRequestsPerConn := float64(stats.requestsHandled) / float64(stats.totalConns)
			fmt.Printf("Avg requests per connection: %.2f\n", avgRequestsPerConn)
		}


		fmt.Printf("=============\n\n")
		stats.mu.Unlock()
	}
}
