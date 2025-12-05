package main

import (
	"fmt"
	"net"
	"os"
	"strings"
)

type HTTPRequest struct {
	Method  string
	Path    string
	Version string
	Headers map[string]string
	Body    string
}

func main() {
	// Creates a listener on port 8081
	listener, err := net.Listen("tcp", ":8081")

	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to bind to port: %v\n", err)
		os.Exit(1)
	}

	// ensures the listener is closed when the function exits
	defer listener.Close()

	fmt.Println("Server listening on :8081")

	for {
		// this is a blocking call, it will await until a new connection happens
		conn, err := listener.Accept()

		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to accept connection: %v\n", err)
			continue
		}

		handleConnection(conn)
	}
}

func handleConnection(conn net.Conn) {
	defer conn.Close()

	buffer := make([]byte, 4096)
	n, err := conn.Read(buffer)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading: %v\n", err)
		return
	}

	request, err := parseRequest(string(buffer[:n]))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing request: %v\n", err)
		sendResponse(conn, 400, "Bad Request", "Invalid HTTP request")
		return
	}

	fmt.Printf("Method: %s\n", request.Method)
	fmt.Printf("Path: %s\n", request.Path)
	fmt.Printf("Version: %s\n", request.Version)
	fmt.Printf("Headers: %v\n", request.Headers)
	fmt.Println("---")

	handleRequest(conn, request)
}

func parseRequest(rawRequest string) (*HTTPRequest, error) {
	// Split by \r\n to get lines
	lines := strings.Split(rawRequest, "\r\n")
	if len(lines) < 1 {
		return nil, fmt.Errorf("empty request")
	}

	// Parse the request line (first line)
	// Format: METHOD PATH VERSION
	requestLine := strings.Split(lines[0], " ")
	if len(requestLine) != 3 {
		return nil, fmt.Errorf("invalid request line")
	}

	request := &HTTPRequest{
		Method:  requestLine[0],
		Path:    requestLine[1],
		Version: requestLine[2],
		Headers: make(map[string]string),
	}

	// Parse headers
	i := 1
	for i < len(lines) && lines[i] != "" {
		// Header format: "Key: Value"
		parts := strings.SplitN(lines[i], ": ", 2)
		if len(parts) == 2 {
			request.Headers[parts[0]] = parts[1]
		}
		i++
	}

	// Body starts after the blank line
	if i < len(lines)-1 {
		request.Body = strings.Join(lines[i+1:], "\r\n")
	}

	return request, nil
}

func handleRequest(conn net.Conn, request *HTTPRequest) {
	// Simple routing based on path
	switch request.Path {
	case "/":
		sendResponse(conn, 200, "OK", "Welcome to the home page!")
	case "/hello":
		sendResponse(conn, 200, "OK", "Hello, World!")
	case "/about":
		sendResponse(conn, 200, "OK", "This is a custom HTTP server built from scratch!")
	default:
		sendResponse(conn, 404, "Not Found", "The requested path was not found")
	}
}

func sendResponse(conn net.Conn, statusCode int, statusText string, body string) {
	// Build the response
	response := fmt.Sprintf("HTTP/1.1 %d %s\r\n", statusCode, statusText)
	response += "Content-Type: text/plain\r\n"
	response += fmt.Sprintf("Content-Length: %d\r\n", len(body))
	response += "\r\n"
	response += body

	conn.Write([]byte(response))
}
