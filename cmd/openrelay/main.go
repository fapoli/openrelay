package main

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
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

	conn, err := openrelay.ListenRelay(net.JoinHostPort(cfg.UDPBindHost, strconv.Itoa(cfg.PortUDP)))
	if err != nil {
		fatal("UDP relay failed to start", err)
	}
	go openrelay.ServeRelay(store, conn)

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
