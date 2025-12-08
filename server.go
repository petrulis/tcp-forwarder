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

var (
	ErrServerClosed = errors.New("server closed")
)

type Server struct {
	addr string

	listener net.Listener
	conns    map[*conn]struct{}

	mu         sync.Mutex
	inShutdown atomic.Bool
}

func NewServer(addr string) *Server {
	return &Server{
		addr:  addr,
		conns: make(map[*conn]struct{}),
	}
}

func (s *Server) ListenAndServe() error {
	if s.shuttingDown() {
		return ErrServerClosed
	}
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}

	s.listener = ln

	return s.run()
}

// run accepts incoming connections and starts a new goroutine
// to handle each connection. If a recoverable error occurs during Accept(),
// it retries with exponential backoff.
func (s *Server) run() error {
	for {
		rwc, err := s.listener.Accept()
		if err != nil {
			if s.shuttingDown() {
				return ErrServerClosed
			}
			log.Printf("failed to accept connection: %v", err)
			continue
		}
		c := s.newConn(rwc)
		go c.serve()
	}
}

func (s *Server) closeConns() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Here we can check if in-flight operations are done before closing
	// however, for simplicity, we just close all connections.
	for c := range s.conns {
		// This may block until TCP buffers are flushed.
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
	return &conn{
		server: s,
		rwc:    rwc,
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
	return NewServer(addr).ListenAndServe()
}
