package main

import (
	"fmt"
	"log/slog"
	"net"
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
	sessions := openrelay.NewSessionStore(cfg.SessionTTL, cfg.PeerTimeout)

	conn, err := openrelay.ListenRelay(net.JoinHostPort(cfg.UDPBindHost, strconv.Itoa(cfg.PortUDP)))
	if err != nil {
		fatal("UDP relay failed to start", err)
	}
	go openrelay.ServeRelay(sessions, conn)

	go func() {
		for range time.Tick(cfg.CleanupInterval) {
			sessions.Cleanup()
		}
	}()

	server := openrelay.NewHTTPServer(fmt.Sprintf(":%d", cfg.PortHTTP), sessions, projects, openrelay.HTTPOptions{
		TrustedIPHeader: cfg.TrustedIPHeader,
		RateLimitPerMin: cfg.RateLimitPerMin,
	})
	slog.Info("HTTP signaling listening", "addr", server.Addr)
	err = server.ListenAndServe()
	if err != nil {
		fatal("HTTP server error", err)
	}
}

func fatal(msg string, err error) {
	slog.Error(msg, "err", err)
	os.Exit(1)
}
