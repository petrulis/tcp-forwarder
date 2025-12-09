package main

import (
	"context"
	"errors"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultMaxBytes = 100
)

var (
	ErrServerClosed = errors.New("server closed")
)

type broadcastMsg struct {
	data []byte
	from *conn
}

// Server is a TCP server that forwards raw TCP data between connected clients.
// It supports 1 broadcast (buffered) channel, N per-client channels (buffered),
// drop on backpressure, client specific drop counters, server aborts on slow clients
// and graceful shutdown.
type Server struct {
	Addr string

	ConnMaxBytes uint64

	// Buffer size for the broadcast channel size
	// for absorbing bursts of messages.
	BufferSize int

	// ConnBufferSeize is the per-connection outgoing
	// message buffer size.
	ConnBufferSize int

	listener net.Listener
	conns    map[*conn]struct{}

	// broadcastCh is the buffered channel for broadcasting messages
	// to all connected clients. It is buffered to accomodate some
	// level of burstiness without blocking the sender.
	broadcastCh chan broadcastMsg

	// mu protects access to conns map during broadcasting
	// and connection tracking.
	// It is a RWMutex to allow multiple concurrent readers
	// to access the conns map. Adding/removing connections
	// will acquire the lock exclusively.
	// sync.Map could be used here but it has more overhead
	// due to maintaining a lock-free map which is
	// better optimized for Load scenarios, not Range.
	mu         sync.RWMutex
	inShutdown atomic.Bool
}

func (s *Server) ListenAndServe() error {
	if s.shuttingDown() {
		return ErrServerClosed
	}
	ln, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return err
	}

	return s.Serve(ln)
}

func (s *Server) broadcastLoop() {
	for msg := range s.broadcastCh {
		s.mu.RLock()
		for conn := range s.conns {
			if conn == msg.from { // skip sender
				continue
			}
			conn.send(msg.data)
		}
		s.mu.RUnlock()
	}
}

func (s *Server) broadcast(data []byte, from *conn) {
	select {
	case s.broadcastCh <- broadcastMsg{data: data, from: from}:
	default:
		// The server is overloaded and cannot keep up, probably
		// due to slow client connections. For simplicity, we just drop
		// the message here. In a real-world scenario, we might want
		// to implement more sophisticated backpressure handling.
		log.Printf("Server buffer is full, dropping")
		return
	}
}

// Serve accepts incoming connections and starts a new goroutine
// to handle each connection.
func (s *Server) Serve(ln net.Listener) error {
	s.listener = ln

	s.mu.Lock()
	if s.conns == nil {
		s.conns = make(map[*conn]struct{})
	}
	if s.broadcastCh == nil {
		s.broadcastCh = make(chan broadcastMsg, s.BufferSize)
	}
	s.mu.Unlock()

	go s.broadcastLoop()
	for {
		rwc, err := s.listener.Accept()
		if err != nil {
			if s.shuttingDown() {
				return ErrServerClosed
			}
			if errors.Is(err, net.ErrClosed) {
				return nil // listener closed, exit gracefully
			}
			return err
		}
		c := s.newConn(rwc)
		s.trackConn(c, true)

		go func() {
			c.serve()
			s.trackConn(c, false)
		}()
	}
}

func (s *Server) closeConns() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Here we can check if in-flight operations are done before closing
	// however, for simplicity, we just close all connections.
	for c := range s.conns {
		// In very rare cases this may block until TCP buffers are flushed.
		// Can be slow for slow connections.
		c.rwc.Close()
		delete(s.conns, c)
	}
	return len(s.conns) == 0
}

// Shutdown gracefully shuts down the server by closing the listener and all active connections.
func (s *Server) Shutdown(ctx context.Context) error {
	s.inShutdown.Store(true)

	s.mu.Lock()
	// Close the listener to stop accepting new connections.
	if s.listener != nil {
		s.listener.Close()
	}
	s.mu.Unlock()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if s.closeConns() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Server) shuttingDown() bool {
	return s.inShutdown.Load()
}

// newConn creates a new conn instance associated with the server.
func (s *Server) newConn(rwc net.Conn) *conn {
	if s.ConnMaxBytes == 0 {
		s.ConnMaxBytes = defaultMaxBytes
	}
	return &conn{
		server:     s,
		rwc:        rwc,
		lc:         newLimitedConn(rwc, s.ConnMaxBytes),
		remoteAddr: rwc.RemoteAddr().String(),
		out:        make(chan []byte, s.ConnBufferSize),
	}
}

// trackConn adds or removes a connection from the active connections map.
func (s *Server) trackConn(c *conn, add bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if add {
		s.conns[c] = struct{}{}
	} else {
		delete(s.conns, c)
	}
}

func ListenAndServe(addr string) error {
	srv := Server{Addr: addr}
	return srv.ListenAndServe()
}
