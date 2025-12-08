package main

import (
	"bufio"
	"io"
	"log"
	"net"
	"runtime/debug"
	"sync/atomic"
)

type conn struct {
	server     *Server
	rwc        net.Conn
	remoteAddr string
	out        chan []byte
	dropped    uint64
}

func (c *conn) serve() {
	c.server.trackConn(c, true)
	defer func() {
		c.rwc.Close()
		c.server.trackConn(c, false)
	}()
	// Register recovery callback to avoid crashing the whole
	// server on panics in connection handling.
	defer func() {
		if err := recover(); err != nil {
			log.Printf("panic serving %v: %v\n%s", c.remoteAddr, err, debug.Stack())
		}
	}()
	// Here we can implement a request read loop if we wanted to
	// implement protocols that require request parsing.
	// However, the goal is to forward raw TCP data, so we just read and forward bytes.
	go c.writeLoop()
	// Create a buffered reader for efficiency with small reads and writes from clients.
	// bufio.Scanner would work here too but it won't be as efficient for raw stream forwarding
	// due to its tokenization.
	reader := bufio.NewReader(c.rwc)
	// Hold a reasonable buffer size to not overconsume memory but also
	// to allow decent throughput by minimizing number of syscalls.
	buf := make([]byte, 128)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			// Broadcast the received data to all other connections.
			c.server.broadcast(buf[:n], c)
		}
		if err != nil {
			if err != io.EOF {
				return
			}
			break
		}
	}
}

func (c *conn) writeLoop() {
	for data := range c.out {
		_, err := c.rwc.Write(data)
		if err != nil {
			return
		}
	}
}

func (c *conn) send(data []byte) {
	select {
	case c.out <- data:
	default:
		// The connection is overloaded and cannot keep up, probably
		// due to slow client connection. Let's introduce a simple
		// backpressure mechanism by dropping messages
		// to avoid blocking the server and disconnect the client
		// after too many of them dropped.
		if dropped := atomic.AddUint64(&c.dropped, 1); dropped > 10 {
			c.rwc.Close()
		}
		return
	}
}
