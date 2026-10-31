package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

func main() {
	cfg := loadConfig()
	projects := loadProjects()
	app := newApp(cfg, projects)

	go app.runRelay(fmt.Sprintf("%s:%d", cfg.UDPBindHost, cfg.PortUDP))

	go func() {
		for range time.Tick(30 * time.Second) {
			app.cleanup()
		}
	}()

	addr := fmt.Sprintf(":%d", cfg.PortHTTP)
	slog.Info("HTTP signaling listening", "port", cfg.PortHTTP)
	if err := http.ListenAndServe(addr, app.routes()); err != nil {
		slog.Error("HTTP server error", "err", err)
	}
}
