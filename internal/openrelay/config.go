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

// Config holds the server settings read from environment variables.
type Config struct {
	PortHTTP        int
	PortUDP         int
	UDPBindHost     string
	SessionTTL      time.Duration
	PeerTimeout     time.Duration
	CleanupInterval time.Duration
	TrustedIPHeader string
	RateLimitPerMin int
}

const defaultCleanupInterval = 5 * time.Minute

// Project is a game registered in projects.yaml, identified by its API key.
type Project struct {
	ID                 string
	APIKey             string
	MaxSessions        int
	MaxPeersPerSession int
}

// LoadConfig reads the server settings from environment variables, using defaults for unset ones.
func LoadConfig() Config {
	cfg := Config{
		PortHTTP:        envInt("PORT_HTTP", 3000),
		PortUDP:         envInt("PORT_UDP", 7777),
		UDPBindHost:     envStr("UDP_BIND_HOST", ""),
		SessionTTL:      envMillis("SESSION_TTL_MS", 300_000),
		PeerTimeout:     envMillis("PEER_TIMEOUT_MS", 10_000),
		CleanupInterval: envMillis("CLEANUP_INTERVAL_MS", 300_000),
		TrustedIPHeader: envStr("TRUSTED_IP_HEADER", ""),
		RateLimitPerMin: envInt("RATE_LIMIT_PER_MIN", 10),
	}
	if cfg.CleanupInterval <= 0 {
		slog.Warn("invalid CLEANUP_INTERVAL_MS — using default", "value", cfg.CleanupInterval, "default", defaultCleanupInterval)
		cfg.CleanupInterval = defaultCleanupInterval
	}
	return cfg
}

// LoadProjects reads projects.yaml and resolves each project's API key from the environment.
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
	for _, entry := range f.Projects {
		if entry.ID == "" {
			return nil, errors.New("project with empty id")
		}
		if ids[entry.ID] {
			return nil, fmt.Errorf("duplicate project id %q", entry.ID)
		}
		ids[entry.ID] = true

		maxPeers := entry.MaxPeersPerSession
		if maxPeers == 0 {
			maxPeers = 16
		}
		if maxPeers < 1 || maxPeers > maxPeersLimit {
			return nil, fmt.Errorf("project %q: maxPeersPerSession %d out of range 1-%d", entry.ID, maxPeers, maxPeersLimit)
		}
		envKey := strings.ToUpper(strings.ReplaceAll(entry.ID, "-", "_")) + "_API_KEY"
		apiKey := getenv(envKey)
		if apiKey == "" {
			return nil, fmt.Errorf("project %q: %s is not set", entry.ID, envKey)
		}
		if other, dup := projects[apiKey]; dup {
			return nil, fmt.Errorf("projects %q and %q share the same API key", other.ID, entry.ID)
		}
		projects[apiKey] = &Project{
			ID:                 entry.ID,
			APIKey:             apiKey,
			MaxSessions:        entry.MaxSessions,
			MaxPeersPerSession: maxPeers,
		}
		slog.Info("project loaded", "project", entry.ID)
	}
	return projects, nil
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envMillis(key string, def int) time.Duration {
	return time.Duration(envInt(key, def)) * time.Millisecond
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
