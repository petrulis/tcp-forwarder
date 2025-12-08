package main

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestTCPForwarding(t *testing.T) {
	type input struct {
		// A slice of clients with their messages.
		messages [][]byte
	}
	type expected struct {
		// Expected messages received by each client.
		messages [][]byte
	}
	tests := []struct {
		name     string
		input    input
		expected expected
	}{
		{
			name: "should forward data between two clients",
			input: input{
				messages: [][]byte{
					[]byte("Hello from client 1"),
					[]byte("Hello from client 2"),
				},
			},
			expected: expected{
				messages: [][]byte{
					[]byte("Hello from client 2"),
					[]byte("Hello from client 1"),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serverAddr := ":9001"
			go func() {
				if err := ListenAndServe(serverAddr); err != nil {
					t.Errorf("Server failed: %v", err)
				}
			}()
			// Wait for server to start
			time.Sleep(100 * time.Millisecond)

			var clients []net.Conn
			for range tt.input.messages {
				conn, err := net.Dial("tcp", serverAddr)
				if err != nil {
					t.Fatal(err)
				}
				clients = append(clients, conn)
				defer conn.Close()
			}

			// Channel to collect messages
			type result struct {
				idx int
				msg []byte
				err error
			}
			results := make(chan result, len(clients))

			// Start reading from clients
			for i, client := range clients {
				go func(i int, c net.Conn) {
					buffer := make([]byte, 1024)
					c.SetReadDeadline(time.Now().Add(2 * time.Second))
					n, err := c.Read(buffer)
					results <- result{i, buffer[:n], err}
				}(i, client)
			}

			// Send messages
			for i, msg := range tt.input.messages {
				if _, err := clients[i].Write(msg); err != nil {
					t.Fatal(err)
				}
			}

			// Check received messages
			received := make([][]byte, len(clients))
			for range clients {
				r := <-results
				if r.err != nil {
					t.Fatalf("Client %d read failed: %v", r.idx, r.err)
				}
				received[r.idx] = r.msg
			}

			for i := range tt.expected.messages {
				if string(received[i]) != string(tt.expected.messages[i]) {
					t.Errorf("Client %d expected '%s', got '%s'", i, tt.expected.messages[i], received[i])
				}
			}
		})
	}
}

func TestServer_Shutdown(t *testing.T) {
	srv := NewServer(":0")

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != ErrServerClosed {
			t.Errorf("Server failed: %v", err)
		}
	}()
	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	conn, err := net.Dial("tcp", srv.listener.Addr().String())
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
	_, err = net.Dial("tcp", srv.listener.Addr().String())
	if err == nil {
		t.Fatal("Expected connection failure after shutdown, but succeeded")
	}
}
