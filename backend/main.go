// Command calculator runs the decimal-safe calculator HTTP service.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"calculator/api"
)

func main() {
	addr := ":" + port()
	srv := &http.Server{
		Addr:              addr,
		Handler:           api.New(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Drain in-flight requests on SIGTERM/SIGINT instead of cutting connections.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	go func() {
		log.Printf("calculator backend listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-ctx.Done()
	stop() // restore default handling so a second signal force-quits
	log.Println("shutdown signal received, draining connections")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("graceful shutdown failed: %v", err)
	}
	log.Println("calculator backend stopped")
}

func port() string {
	if p := os.Getenv("PORT"); p != "" {
		return p
	}
	return "8080"
}
