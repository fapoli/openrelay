package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/fapoli/openrelay/internal/openrelay"
)

func main() {
	cfg := openrelay.LoadConfig()
	store := openrelay.NewSessionStore(cfg, openrelay.LoadProjects())

	go openrelay.RunRelay(store, fmt.Sprintf("%s:%d", cfg.UDPBindHost, cfg.PortUDP))

	go func() {
		for range time.Tick(30 * time.Second) {
			store.Cleanup()
		}
	}()

	addr := fmt.Sprintf(":%d", cfg.PortHTTP)
	slog.Info("HTTP signaling listening", "port", cfg.PortHTTP)
	if err := http.ListenAndServe(addr, openrelay.Routes(store)); err != nil {
		slog.Error("HTTP server error", "err", err)
	}
}
