package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const (
	gracePeriod = 10 * time.Second
)

func main() {
	srv := Server{
		Addr:           ":9000",
		ConnMaxBytes:   100,
		BufferSize:     64,
		ConnBufferSize: 64,
	}
	// Channel for OS signals
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		log.Printf("TCP Forwarder starting on %s\n", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != ErrServerClosed {
			log.Fatalf("Failed to start: %v\n", err)
		}
	}()

	// Wait for signal
	<-stop
	log.Println("Shutdown signal received")

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), gracePeriod)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Graceful shutdown failed: %v", err)
	}

	log.Println("Server gracefully stopped")
}
