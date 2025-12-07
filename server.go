package main

import (
	"log"
	"net"
	"sync"
	"time"
)

type Server struct {
	activeConn map[*conn]struct{}
	listeners  map[*net.Listener]struct{}

	mu sync.Mutex
}

func NewServer() *Server {
	return &Server{
		activeConn: make(map[*conn]struct{}),
		listeners:  make(map[*net.Listener]struct{}),
	}
}

func (s *Server) ListenAndServe(addr string) error {
	log.Printf("Starting server on %s", addr)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return s.serve(ln)
}

func (s *Server) serve(ln net.Listener) error {
	s.trackListener(&ln, true)
	defer s.trackListener(&ln, false)

	var delay time.Duration
	for {
		rwc, err := ln.Accept()
		if err != nil {
			if isRecoverable(err) {
				if delay == 0 {
					delay = 5 * time.Millisecond
				} else {
					delay *= 2
				}
				if max := 1 * time.Second; delay > max {
					delay = max
				}
				time.Sleep(delay)
				continue
			}
			return err
		}
		delay = 0
		c := s.newConn(rwc)
		go c.serve()
	}
}

func (s *Server) newConn(rwc net.Conn) *conn {
	return &conn{
		server: s,
		rwc:    rwc,
	}
}

func (s *Server) trackConn(c *conn, add bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if add {
		s.activeConn[c] = struct{}{}
	} else {
		delete(s.activeConn, c)
	}
}

func (s *Server) trackListener(ln *net.Listener, add bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if add {
		s.listeners[ln] = struct{}{}
	} else {
		delete(s.listeners, ln)
	}
}

func ListenAndServe(addr string) error {
	return NewServer().ListenAndServe(addr)
}
