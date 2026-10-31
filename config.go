package main

import (
	"log/slog"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	PortHTTP     int
	PortUDP      int
	UDPBindHost  string
	SessionTTLMs int64
}

type Project struct {
	ID                string
	APIKey            string
	MaxSessions       int
	MaxPeersPerSession int
}

func loadConfig() Config {
	return Config{
		PortHTTP:    envInt("PORT_HTTP", 3000),
		PortUDP:     envInt("PORT_UDP", 7777),
		UDPBindHost: envStr("UDP_BIND_HOST", "0.0.0.0"),
		SessionTTLMs: int64(envInt("SESSION_TTL_MS", 300_000)),
	}
}

func loadProjects() map[string]*Project {
	type projectYAML struct {
		ID                 string `yaml:"id"`
		MaxSessions        int    `yaml:"maxSessions"`
		MaxPeersPerSession int    `yaml:"maxPeersPerSession"`
	}
	type file struct {
		Projects []projectYAML `yaml:"projects"`
	}

	data, err := os.ReadFile("projects.yaml")
	if err != nil {
		slog.Warn("no projects.yaml found — no projects loaded")
		return nil
	}

	var f file
	if err := yaml.Unmarshal(data, &f); err != nil {
		slog.Error("failed to parse projects.yaml", "err", err)
		return nil
	}

	projects := make(map[string]*Project, len(f.Projects))
	for _, p := range f.Projects {
		maxPeers := p.MaxPeersPerSession
		if maxPeers == 0 {
			maxPeers = 16
		}
		envKey := strings.ToUpper(strings.ReplaceAll(p.ID, "-", "_")) + "_API_KEY"
		apiKey := os.Getenv(envKey)
		if apiKey == "" {
			slog.Warn("no API key for project — skipping", "project", p.ID, "env", envKey)
			continue
		}
		projects[apiKey] = &Project{
			ID:                 p.ID,
			APIKey:             apiKey,
			MaxSessions:        p.MaxSessions,
			MaxPeersPerSession: maxPeers,
		}
		slog.Info("project loaded", "project", p.ID)
	}
	return projects
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
