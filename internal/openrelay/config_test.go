package openrelay

import (
	"testing"
	"time"
)

func TestParseProjects_valid(t *testing.T) {
	data := []byte(`
projects:
  - id: max
    maxPeersPerSession: 254
  - id: default
`)
	env := map[string]string{"MAX_API_KEY": "k1", "DEFAULT_API_KEY": "k2"}
	projects, err := parseProjects(data, func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p := projects["k1"]; p == nil || p.MaxPeersPerSession != 254 {
		t.Errorf("expected project with 254 peers, got %+v", p)
	}
	if p := projects["k2"]; p == nil || p.MaxPeersPerSession != 16 {
		t.Errorf("expected default of 16 peers, got %+v", p)
	}
}

func TestParseProjects_errors(t *testing.T) {
	env := map[string]string{"A_API_KEY": "k", "B_API_KEY": "k", "C_API_KEY": "other"}
	getenv := func(k string) string { return env[k] }
	cases := map[string]string{
		"invalid yaml":    "projects: [",
		"no projects":     "projects: []",
		"empty id":        "projects:\n  - maxSessions: 1",
		"duplicate id":    "projects:\n  - id: c\n  - id: c",
		"too many peers":  "projects:\n  - id: c\n    maxPeersPerSession: 255",
		"negative peers":  "projects:\n  - id: c\n    maxPeersPerSession: -1",
		"missing api key": "projects:\n  - id: missing",
		"shared api key":  "projects:\n  - id: a\n  - id: b",
	}
	for name, yml := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseProjects([]byte(yml), getenv); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestLoadConfig_peerTimeout(t *testing.T) {
	if c := LoadConfig(); c.PeerTimeout != 10*time.Second {
		t.Errorf("expected default 10s, got %v", c.PeerTimeout)
	}
	t.Setenv("PEER_TIMEOUT_MS", "2500")
	if c := LoadConfig(); c.PeerTimeout != 2500*time.Millisecond {
		t.Errorf("expected 2.5s, got %v", c.PeerTimeout)
	}
}

func TestLoadConfig_cleanupInterval(t *testing.T) {
	if d := LoadConfig().CleanupInterval; d != 5*time.Minute {
		t.Errorf("expected default 5m, got %v", d)
	}
	t.Setenv("CLEANUP_INTERVAL_MS", "60000")
	if d := LoadConfig().CleanupInterval; d != time.Minute {
		t.Errorf("expected 1m, got %v", d)
	}
}

func TestLoadConfig_nonPositiveCleanupIntervalFallsBackToDefault(t *testing.T) {
	for _, v := range []string{"0", "-1"} {
		t.Setenv("CLEANUP_INTERVAL_MS", v)
		if d := LoadConfig().CleanupInterval; d != 5*time.Minute {
			t.Errorf("CLEANUP_INTERVAL_MS=%s: expected default 5m, got %v", v, d)
		}
	}
}
