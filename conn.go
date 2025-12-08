package main

import (
	"bufio"
	"errors"
	"io"
	"log"
	"net"
	"runtime/debug"
	"sync"
	"sync/atomic"
)

const (
	maxBytes = 100
	limitMsg = "\nYou've reached the 100-byte limit. Goodbye!\n"
)

var (
	errUploadLimitExceeded   = errors.New("upload limit exceeded")
	errDownloadLimitExceeded = errors.New("download limit exceeded")
)

// limitedConn wraps a net.Conn and enforces strict
// upload and download byte limits.
type limitedConn struct {
	rwc io.ReadWriteCloser

	// mutexes protects access to uploaded and downloaded counters.
	// It should be more efficient to use atomics for
	// simple counts since mutex has more overhead,
	// but for simplicity we use a mutex here.
	// Using atomics should increase the throughput of
	// of the server under high load.
	uploadMu   sync.Mutex
	uploaded   uint64
	downloadMu sync.Mutex
	downloaded uint64
}

func newLimitedConn(rwc io.ReadWriteCloser) *limitedConn {
	return &limitedConn{
		rwc: rwc,
	}
}

func (lc *limitedConn) Read(b []byte) (int, error) {
	lc.uploadMu.Lock()
	defer lc.uploadMu.Unlock()

	if lc.uploaded >= maxBytes {
		return 0, errUploadLimitExceeded
	}

	remaining := maxBytes - lc.uploaded
	if uint64(len(b)) > remaining {
		b = b[:remaining]
	}

	n, err := lc.rwc.Read(b)
	if n > 0 {
		lc.uploaded += uint64(n)
		if lc.uploaded >= maxBytes {
			return n, errUploadLimitExceeded
		}
	}
	return n, err
}

func (lc *limitedConn) Write(b []byte) (int, error) {
	lc.downloadMu.Lock()
	defer lc.downloadMu.Unlock()

	if lc.downloaded >= maxBytes {
		return 0, errDownloadLimitExceeded
	}

	remaining := maxBytes - lc.downloaded
	if uint64(len(b)) > remaining {
		b = b[:remaining]
	}

	n, err := lc.rwc.Write(b)
	if n > 0 {
		lc.downloaded += uint64(n)
		if lc.downloaded >= maxBytes {
			return n, errDownloadLimitExceeded
		}
	}
	return n, err
}

func (lc *limitedConn) Close() error {
	return lc.rwc.Close()
}

type conn struct {
	server     *Server
	rwc        net.Conn
	lc         *limitedConn
	remoteAddr string
	out        chan []byte
	dropped    uint64
}

func (c *conn) serve() {
	defer c.rwc.Close()
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
	reader := bufio.NewReader(c.lc)
	// Hold a reasonable buffer size to not overconsume memory but also
	// to allow decent throughput by minimizing number of syscalls.
	buf := make([]byte, 128)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			// Copy data and broadcast to all other connections.
			c.server.broadcast(append([]byte(nil), buf[:n]...), c)
		}
		if err != nil {
			if err == errUploadLimitExceeded {
				log.Printf("Upload limit reached for %s", c.remoteAddr)
				c.sendLimitNoticeAndClose()
				return
			}
			if err != io.EOF {
				return
			}
			break
		}
	}
}

func (c *conn) writeLoop() {
	for data := range c.out {
		_, err := c.lc.Write(data)
		if err == errDownloadLimitExceeded {
			log.Printf("Download limit reached for %s", c.remoteAddr)
			c.sendLimitNoticeAndClose()
			return
		}
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

func (c *conn) sendLimitNoticeAndClose() {
	if _, err := c.rwc.Write([]byte(limitMsg)); err != nil {
		log.Printf("Failed to send limit notice to %s: %v", c.remoteAddr, err)
	}
	c.rwc.Close()
}
