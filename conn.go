package main

import (
	"bufio"
	"io"
	"net"
	"time"
)

type conn struct {
	server *Server
	rwc    net.Conn
}

func (c *conn) serve() {
	c.server.trackConn(c, true)
	defer func() {
		c.rwc.Close()
		c.server.trackConn(c, false)
	}()

	// Here we can implement a request read loop if we wanted to
	// implement protocols that require request parsing.
	// However, the goal is to forward raw TCP data, so we just read and forward bytes.

	// Create a buffered reader for efficiency with small reads and writes from clients.
	// bufio.Scanner would work here too but it won't be as efficient for raw stream forwarding
	// due to its tokenization.
	reader := bufio.NewReader(c.rwc)
	// Hold a reasonable buffer size to not overconsume memory but also
	// to allow decent throughput by minimizing number of syscalls.
	buf := make([]byte, 128)
	for {
		n, err := reader.Read(buf)
		if err != nil {
			if err != io.EOF {
				return
			}
			break
		}

		if n > 0 {
			data := buf[:n]
			c.server.mu.Lock()
			for targetConn := range c.server.conns {
				if targetConn == c {
					continue
				}
				go func(targetConn *conn, data []byte) {
					if err := targetConn.rwc.SetWriteDeadline(time.Time{}); err != nil {
						return
					}
					if _, err := targetConn.rwc.Write(data); err != nil {
						delete(c.server.conns, targetConn)
						targetConn.rwc.Close()
					}
				}(targetConn, append([]byte(nil), data...))
			}
			c.server.mu.Unlock()
		}
	}
}
