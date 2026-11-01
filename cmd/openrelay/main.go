package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/fapoli/openrelay/internal/openrelay"
)

func main() {
	cfg := openrelay.LoadConfig()

	projects, err := openrelay.LoadProjects()
	if err != nil {
		fatal("invalid project configuration", err)
	}
	store := openrelay.NewSessionStore(cfg, projects)

	conn, err := openrelay.ListenRelay(fmt.Sprintf("%s:%d", cfg.UDPBindHost, cfg.PortUDP))
	if err != nil {
		fatal("UDP relay failed to start", err)
	}
	go openrelay.ServeRelay(store, conn)

	// Expiry is enforced on every operation; this sweep only frees memory.
	go func() {
		for range time.Tick(cfg.CleanupInterval()) {
			store.Cleanup()
		}
	}()

	addr := fmt.Sprintf(":%d", cfg.PortHTTP)
	slog.Info("HTTP signaling listening", "port", cfg.PortHTTP)
	if err := http.ListenAndServe(addr, openrelay.Routes(store)); err != nil {
		fatal("HTTP server error", err)
	}
}

func fatal(msg string, err error) {
	slog.Error(msg, "err", err)
	os.Exit(1)
}
