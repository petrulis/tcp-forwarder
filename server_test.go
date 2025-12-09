package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestServer(t *testing.T) {
	const (
		numClients   = 100
		numMessages  = 5
		idleDeadline = 2 * time.Second
	)
	// Generate input dynamically
	input := make([][]string, numClients)
	for i := 0; i < numClients; i++ {
		msgs := make([]string, numMessages)
		for j := 0; j < numMessages; j++ {
			msgs[j] = fmt.Sprintf("c%dm%d", i, j)
		}
		input[i] = msgs
	}

	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("Failed to start listener: %v", err)
	}
	defer ln.Close()
	srv := Server{
		BufferSize:     512,
		ConnMaxBytes:   numClients * numMessages * 32,
		ConnBufferSize: 512,
	}

	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Errorf("Server failed: %v", err)
		}
	}()

	clients := make([]net.Conn, numClients)
	for i := 0; i < numClients; i++ {
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		clients[i] = c
		defer c.Close()
	}

	type result struct {
		idx  int
		data string
		err  error
	}
	results := make(chan result, numClients)
	for i, c := range clients {
		go func(i int, conn net.Conn) {
			conn.SetReadDeadline(time.Now().Add(idleDeadline))
			buf, err := io.ReadAll(conn)
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				results <- result{i, string(buf), nil}
				return
			}
			if err == io.EOF {
				err = nil
			}
			results <- result{i, string(buf), err}
		}(i, c)
	}

	for i, msgs := range input {
		clients[i].SetWriteDeadline(time.Now().Add(idleDeadline))
		for _, msg := range msgs {
			// Just spawn in a goroutine
			go func(i int, msg string) {
				if _, err := clients[i].Write([]byte(msg)); err != nil {
					panic(err) // keep it simple
				}
			}(i, msg)
		}
	}

	for i := 0; i < numClients; i++ {
		r := <-results
		if r.err != nil {
			t.Fatalf("Client %d read failed: %v", r.idx, r.err)
		}
		// Check that all messages from other clients exist somewhere in the received string
		for j := 0; j < numClients; j++ {
			if j == r.idx {
				continue
			}
			for _, msg := range input[j] {
				if !strings.Contains(r.data, msg) {
					t.Errorf("Client %d missing message: %s", r.idx, msg)
				}
			}
		}
	}
}

func TestServer_ByteLimit(t *testing.T) {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("Failed to start listener: %v", err)
	}
	defer ln.Close()

	srv := Server{
		ConnMaxBytes:   100,
		BufferSize:     64,
		ConnBufferSize: 64,
	}

	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Errorf("Server failed: %v", err)
		}
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Start reading to capture the server's goodbye message.
	done := make(chan string, 1)
	go func() {
		buf, err := io.ReadAll(conn)
		if err != nil && err != io.EOF {
			t.Errorf("Failed to read response: %v", err)
		}
		done <- string(buf)
	}()

	data := make([]byte, 100)
	for i := range data {
		data[i] = 'b'
	}

	if _, err := conn.Write(data); err != nil {
		t.Fatalf("Failed to write data: %v", err)
	}

	select {
	case response := <-done:
		expectedMsg := "You've reached the 100-byte limit. Goodbye!"
		if !strings.Contains(response, expectedMsg) {
			t.Errorf("Expected goodbye message containing %q, got %q", expectedMsg, response)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for goodbye message")
	}
}

func TestServer_Shutdown(t *testing.T) {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("Failed to start listener: %v", err)
	}
	defer ln.Close()

	var srv Server
	go srv.Serve(ln)

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Shutdown the server
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown failed: %v", err)
	}

	// Attempt to connect after shutdown
	_, err = net.Dial("tcp", ln.Addr().String())
	if err == nil {
		t.Fatal("Expected connection failure after shutdown, but succeeded")
	}
}
