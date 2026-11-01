package openrelay

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	PortHTTP          int
	PortUDP           int
	UDPBindHost       string
	SessionTTLMs      int64
	PeerTimeoutMs     int64
	CleanupIntervalMs int64
	TrustedIPHeader   string
}

const defaultCleanupIntervalMs = 300_000

type Project struct {
	ID                 string
	APIKey             string
	MaxSessions        int
	MaxPeersPerSession int
}

func LoadConfig() Config {
	return Config{
		PortHTTP:          envInt("PORT_HTTP", 3000),
		PortUDP:           envInt("PORT_UDP", 7777),
		UDPBindHost:       envStr("UDP_BIND_HOST", ""),
		SessionTTLMs:      int64(envInt("SESSION_TTL_MS", 300_000)),
		PeerTimeoutMs:     int64(envInt("PEER_TIMEOUT_MS", 10_000)),
		CleanupIntervalMs: int64(envInt("CLEANUP_INTERVAL_MS", defaultCleanupIntervalMs)),
		TrustedIPHeader:   envStr("TRUSTED_IP_HEADER", ""),
	}
}

// time.Tick returns nil for non-positive durations, which would silently stop the sweep.
func (c Config) CleanupInterval() time.Duration {
	ms := c.CleanupIntervalMs
	if ms <= 0 {
		slog.Warn("invalid CLEANUP_INTERVAL_MS — using default", "value", ms, "default", defaultCleanupIntervalMs)
		ms = defaultCleanupIntervalMs
	}
	return time.Duration(ms) * time.Millisecond
}

func LoadProjects() (map[string]*Project, error) {
	data, err := os.ReadFile("projects.yaml")
	if err != nil {
		return nil, fmt.Errorf("read projects.yaml: %w", err)
	}
	return parseProjects(data, os.Getenv)
}

func parseProjects(data []byte, getenv func(string) string) (map[string]*Project, error) {
	type projectYAML struct {
		ID                 string `yaml:"id"`
		MaxSessions        int    `yaml:"maxSessions"`
		MaxPeersPerSession int    `yaml:"maxPeersPerSession"`
	}
	type file struct {
		Projects []projectYAML `yaml:"projects"`
	}

	var f file
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse projects.yaml: %w", err)
	}
	if len(f.Projects) == 0 {
		return nil, errors.New("projects.yaml defines no projects")
	}

	projects := make(map[string]*Project, len(f.Projects))
	ids := make(map[string]bool, len(f.Projects))
	for _, p := range f.Projects {
		if p.ID == "" {
			return nil, errors.New("project with empty id")
		}
		if ids[p.ID] {
			return nil, fmt.Errorf("duplicate project id %q", p.ID)
		}
		ids[p.ID] = true

		maxPeers := p.MaxPeersPerSession
		if maxPeers == 0 {
			maxPeers = 16
		}
		if maxPeers < 1 || maxPeers > maxPeersLimit {
			return nil, fmt.Errorf("project %q: maxPeersPerSession %d out of range 1-%d", p.ID, maxPeers, maxPeersLimit)
		}
		envKey := strings.ToUpper(strings.ReplaceAll(p.ID, "-", "_")) + "_API_KEY"
		apiKey := getenv(envKey)
		if apiKey == "" {
			return nil, fmt.Errorf("project %q: %s is not set", p.ID, envKey)
		}
		if other, dup := projects[apiKey]; dup {
			return nil, fmt.Errorf("projects %q and %q share the same API key", other.ID, p.ID)
		}
		projects[apiKey] = &Project{
			ID:                 p.ID,
			APIKey:             apiKey,
			MaxSessions:        p.MaxSessions,
			MaxPeersPerSession: maxPeers,
		}
		slog.Info("project loaded", "project", p.ID)
	}
	return projects, nil
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
