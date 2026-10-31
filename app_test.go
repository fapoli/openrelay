package main

import (
	"testing"
)

func newTestApp() *App {
	return newApp(Config{SessionTTLMs: 300_000}, map[string]*Project{
		"test-key": {ID: "proj", APIKey: "test-key", MaxSessions: 20, MaxPeersPerSession: 4},
	})
}

func createTestSession(t *testing.T, a *App, code string) *CreateResult {
	t.Helper()
	r, err := a.createSession("proj", 4, code, "", "", private)
	if err != nil {
		t.Fatalf("createSession: %v", err)
	}
	return r
}

func TestCreateSession_success(t *testing.T) {
	a := newTestApp()
	r, err := a.createSession("proj", 4, "AAAAAA", "", "", private)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Code != "AAAAAA" {
		t.Errorf("got code %q, want AAAAAA", r.Code)
	}
	if r.PeerIndex != 0 {
		t.Errorf("host peer index should be 0, got %d", r.PeerIndex)
	}
}

func TestCreateSession_autoGeneratesCode(t *testing.T) {
	a := newTestApp()
	r, err := a.createSession("proj", 4, "", "", "", private)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(r.Code) != 6 {
		t.Errorf("expected 6-char code, got %q", r.Code)
	}
}

func TestCreateSession_conflict(t *testing.T) {
	a := newTestApp()
	createTestSession(t, a, "AAAAAA")
	_, err := a.createSession("proj", 4, "AAAAAA", "", "", private)
	if err != errConflict {
		t.Errorf("expected errConflict, got %v", err)
	}
}

func TestJoinSession_success(t *testing.T) {
	a := newTestApp()
	created := createTestSession(t, a, "AAAAAA")
	r, err := a.joinSession("AAAAAA", "", "proj")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.SessionToken != created.SessionToken {
		t.Error("session token mismatch")
	}
	if r.PeerIndex != 1 {
		t.Errorf("expected peer index 1, got %d", r.PeerIndex)
	}
}

func TestJoinSession_monotonicIndex(t *testing.T) {
	a := newTestApp()
	createTestSession(t, a, "AAAAAA")
	r1, _ := a.joinSession("AAAAAA", "", "proj")
	r2, _ := a.joinSession("AAAAAA", "", "proj")
	if r1.PeerIndex != 1 || r2.PeerIndex != 2 {
		t.Errorf("expected indices 1,2 got %d,%d", r1.PeerIndex, r2.PeerIndex)
	}
}

func TestJoinSession_wrongPassword(t *testing.T) {
	a := newTestApp()
	a.createSession("proj", 4, "AAAAAA", "correct", "", private)
	_, err := a.joinSession("AAAAAA", "wrong", "proj")
	if err != errNotFound {
		t.Errorf("expected errNotFound, got %v", err)
	}
}

func TestJoinSession_fullSession(t *testing.T) {
	a := newTestApp()
	createTestSession(t, a, "AAAAAA") // host = 1 peer
	a.joinSession("AAAAAA", "", "proj")
	a.joinSession("AAAAAA", "", "proj")
	a.joinSession("AAAAAA", "", "proj") // now at max (4)
	_, err := a.joinSession("AAAAAA", "", "proj")
	if err != errNotFound {
		t.Errorf("expected errNotFound when full, got %v", err)
	}
}

func TestJoinSession_wrongProject(t *testing.T) {
	a := newTestApp()
	createTestSession(t, a, "AAAAAA")
	_, err := a.joinSession("AAAAAA", "", "other-proj")
	if err != errNotFound {
		t.Errorf("expected errNotFound for wrong project, got %v", err)
	}
}

func TestPublicSessions_filtersPrivate(t *testing.T) {
	a := newTestApp()
	a.createSession("proj", 4, "PUBL22", "", "Open", public)
	a.createSession("proj", 4, "PRIV22", "", "", private)

	sessions := a.publicSessions("proj")
	if len(sessions) != 1 || sessions[0].Code != "PUBL22" {
		t.Errorf("expected only PUBL22, got %+v", sessions)
	}
}

func TestPublicSessions_filtersOtherProjects(t *testing.T) {
	a := newTestApp()
	a.createSession("proj", 4, "PUBL22", "", "", public)
	a.createSession("other", 4, "PUBLA2", "", "", public)

	sessions := a.publicSessions("proj")
	if len(sessions) != 1 || sessions[0].Code != "PUBL22" {
		t.Errorf("expected only proj's session, got %+v", sessions)
	}
}

func TestSessionInfo_notFound(t *testing.T) {
	a := newTestApp()
	_, err := a.sessionInfo("XXXXXX", "proj")
	if err != errNotFound {
		t.Errorf("expected errNotFound, got %v", err)
	}
}

func TestCleanup_removesExpired(t *testing.T) {
	a := newTestApp()
	a.config.SessionTTLMs = -1 // expire immediately (now - lastActivity >= 0 > -1)
	createTestSession(t, a, "AAAAAA")
	a.cleanup()
	if _, err := a.sessionInfo("AAAAAA", "proj"); err == nil {
		t.Error("expected session to be removed after cleanup")
	}
}
