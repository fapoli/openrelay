package openrelay

import (
	"errors"
	"net/netip"
	"sync"
	"testing"
)

var testIP = netip.MustParseAddr("10.0.0.1")

func ap(s string) netip.AddrPort { return netip.MustParseAddrPort(s) }

func newTestApp() *SessionStore {
	return NewSessionStore(Config{SessionTTLMs: 300_000, PeerTimeoutMs: 10_000}, map[string]*Project{
		"test-key": {ID: "proj", APIKey: "test-key", MaxSessions: 20, MaxPeersPerSession: 4},
	})
}

func createTestSession(t *testing.T, a *SessionStore, code string) *CreateResult {
	t.Helper()
	r, err := a.createSession("proj", 0, 4, code, "", "", private, testIP)
	if err != nil {
		t.Fatalf("createSession: %v", err)
	}
	return r
}

func TestCreateSession_success(t *testing.T) {
	a := newTestApp()
	r, err := a.createSession("proj", 0, 4, "AAAAAA", "", "", private, testIP)
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
	r, err := a.createSession("proj", 0, 4, "", "", "", private, testIP)
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
	_, err := a.createSession("proj", 0, 4, "AAAAAA", "", "", private, testIP)
	if err != errConflict {
		t.Errorf("expected errConflict, got %v", err)
	}
}

func TestJoinSession_success(t *testing.T) {
	a := newTestApp()
	created := createTestSession(t, a, "AAAAAA")
	r, err := a.joinSession("AAAAAA", "", "proj", testIP)
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

func TestJoinSession_sequentialIndices(t *testing.T) {
	a := newTestApp()
	createTestSession(t, a, "AAAAAA")
	r1, _ := a.joinSession("AAAAAA", "", "proj", testIP)
	r2, _ := a.joinSession("AAAAAA", "", "proj", testIP)
	if r1.PeerIndex != 1 || r2.PeerIndex != 2 {
		t.Errorf("expected indices 1,2 got %d,%d", r1.PeerIndex, r2.PeerIndex)
	}
}

func TestJoinSession_wrongPassword(t *testing.T) {
	a := newTestApp()
	a.createSession("proj", 0, 4, "AAAAAA", "correct", "", private, testIP)
	_, err := a.joinSession("AAAAAA", "wrong", "proj", testIP)
	if err != errNotFound {
		t.Errorf("expected errNotFound, got %v", err)
	}
}

func TestJoinSession_fullSession(t *testing.T) {
	a := newTestApp()
	createTestSession(t, a, "AAAAAA") // host = 1 peer
	a.joinSession("AAAAAA", "", "proj", testIP)
	a.joinSession("AAAAAA", "", "proj", testIP)
	a.joinSession("AAAAAA", "", "proj", testIP) // now at max (4)
	_, err := a.joinSession("AAAAAA", "", "proj", testIP)
	if err != errNotFound {
		t.Errorf("expected errNotFound when full, got %v", err)
	}
}

func TestJoinSession_wrongProject(t *testing.T) {
	a := newTestApp()
	createTestSession(t, a, "AAAAAA")
	_, err := a.joinSession("AAAAAA", "", "other-proj", testIP)
	if err != errNotFound {
		t.Errorf("expected errNotFound for wrong project, got %v", err)
	}
}

func TestPublicSessions_filtersPrivate(t *testing.T) {
	a := newTestApp()
	a.createSession("proj", 0, 4, "PUBL22", "", "Open", public, testIP)
	a.createSession("proj", 0, 4, "PRIV22", "", "", private, testIP)

	sessions := a.publicSessions("proj")
	if len(sessions) != 1 || sessions[0].Code != "PUBL22" {
		t.Errorf("expected only PUBL22, got %+v", sessions)
	}
}

func TestPublicSessions_filtersOtherProjects(t *testing.T) {
	a := newTestApp()
	a.createSession("proj", 0, 4, "PUBL22", "", "", public, testIP)
	a.createSession("other", 0, 4, "PUBLA2", "", "", public, testIP)

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
	a.Cleanup()
	if _, err := a.sessionInfo("AAAAAA", "proj"); err == nil {
		t.Error("expected session to be removed after cleanup")
	}
}

// --- liveness (logical expiry, no Cleanup) ---

func expirePeer(a *SessionStore, code, peerToken string) {
	a.sessions[code].peers[peerToken].lastSeen = nowMs() - a.config.PeerTimeoutMs - 1
}

func expireSession(a *SessionStore, code string) {
	a.sessions[code].lastActivity = nowMs() - a.config.SessionTTLMs - 1
}

func TestJoinSession_reusesLowestFreeIndex(t *testing.T) {
	a := newTestApp()
	createTestSession(t, a, "AAAAAA")
	a.joinSession("AAAAAA", "", "proj", testIP)          // 1
	r2, _ := a.joinSession("AAAAAA", "", "proj", testIP) // 2
	a.joinSession("AAAAAA", "", "proj", testIP)          // 3
	expirePeer(a, "AAAAAA", r2.PeerToken)

	r, err := a.joinSession("AAAAAA", "", "proj", testIP)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.PeerIndex != 2 {
		t.Errorf("expected reused index 2, got %d", r.PeerIndex)
	}
}

func TestJoinSession_expiredPeersDontCountTowardFull(t *testing.T) {
	a := newTestApp()
	createTestSession(t, a, "AAAAAA")
	r1, _ := a.joinSession("AAAAAA", "", "proj", testIP)
	a.joinSession("AAAAAA", "", "proj", testIP)
	a.joinSession("AAAAAA", "", "proj", testIP) // full (4)
	expirePeer(a, "AAAAAA", r1.PeerToken)

	if _, err := a.joinSession("AAAAAA", "", "proj", testIP); err != nil {
		t.Errorf("expected join to succeed after a peer expired, got %v", err)
	}
}

func TestJoinSession_expiredSession(t *testing.T) {
	a := newTestApp()
	createTestSession(t, a, "AAAAAA")
	expireSession(a, "AAAAAA")
	if _, err := a.joinSession("AAAAAA", "", "proj", testIP); err != errNotFound {
		t.Errorf("expected errNotFound, got %v", err)
	}
}

func TestCreateSession_expiredCodeIsFree(t *testing.T) {
	a := newTestApp()
	old := createTestSession(t, a, "AAAAAA")
	expireSession(a, "AAAAAA")

	r, err := a.createSession("proj", 0, 4, "AAAAAA", "", "", private, testIP)
	if err != nil {
		t.Fatalf("expected expired code to be reusable, got %v", err)
	}
	if _, ok := a.tokens[old.SessionToken]; ok && old.SessionToken != r.SessionToken {
		t.Error("expected old session token to be removed")
	}
}

func TestActiveSessionCount_ignoresExpired(t *testing.T) {
	a := newTestApp()
	createTestSession(t, a, "AAAAAA")
	createTestSession(t, a, "BBBBBB")
	expireSession(a, "AAAAAA")
	if n := a.liveSessionCount("proj", nowMs()); n != 1 {
		t.Errorf("expected 1 active session, got %d", n)
	}
}

func TestPublicSessions_hidesExpired(t *testing.T) {
	a := newTestApp()
	a.createSession("proj", 0, 4, "PUBL22", "", "", public, testIP)
	expireSession(a, "PUBL22")
	if s := a.publicSessions("proj"); len(s) != 0 {
		t.Errorf("expected no sessions, got %+v", s)
	}
}

func TestSessionInfo_expired(t *testing.T) {
	a := newTestApp()
	createTestSession(t, a, "AAAAAA")
	expireSession(a, "AAAAAA")
	if _, err := a.sessionInfo("AAAAAA", "proj"); err != errNotFound {
		t.Errorf("expected errNotFound, got %v", err)
	}
}

func TestSessionInfo_countsOnlyLivePeers(t *testing.T) {
	a := newTestApp()
	createTestSession(t, a, "AAAAAA")
	r1, _ := a.joinSession("AAAAAA", "", "proj", testIP)
	expirePeer(a, "AAAAAA", r1.PeerToken)
	info, _ := a.sessionInfo("AAAAAA", "proj")
	if info.Peers != 1 {
		t.Errorf("expected 1 live peer, got %d", info.Peers)
	}
}

func TestRegisterPeer_expiredPeerNotTargeted(t *testing.T) {
	a := newTestApp()
	host := createTestSession(t, a, "AAAAAA")
	p1, _ := a.joinSession("AAAAAA", "", "proj", testIP)
	a.registerPeer(host.SessionToken, p1.PeerToken, ap("10.0.0.1:2000"), broadcast)
	expirePeer(a, "AAAAAA", p1.PeerToken)

	r, _ := a.registerPeer(host.SessionToken, host.PeerToken, ap("10.0.0.1:1000"), broadcast)
	if r == nil || len(r.Targets) != 0 {
		t.Errorf("expected no targets, got %+v", r)
	}
}

func TestRegisterPeer_unicastSkipsExpiredDuplicateIndex(t *testing.T) {
	a := newTestApp()
	host := createTestSession(t, a, "AAAAAA")
	old, _ := a.joinSession("AAAAAA", "", "proj", testIP)
	a.registerPeer(host.SessionToken, old.PeerToken, ap("10.0.0.1:2000"), broadcast)
	expirePeer(a, "AAAAAA", old.PeerToken)
	repl, _ := a.joinSession("AAAAAA", "", "proj", testIP) // reuses index 1
	a.registerPeer(host.SessionToken, repl.PeerToken, ap("10.0.0.1:3000"), keepalive)

	r, _ := a.registerPeer(host.SessionToken, host.PeerToken, ap("10.0.0.1:1000"), 1)
	if r == nil || len(r.Targets) != 1 || r.Targets[0] != ap("10.0.0.1:3000") {
		t.Errorf("expected only the live peer at index 1, got %+v", r)
	}
}

func TestRegisterPeer_expiredSenderNotRevived(t *testing.T) {
	a := newTestApp()
	host := createTestSession(t, a, "AAAAAA")
	expirePeer(a, "AAAAAA", host.PeerToken)
	before := a.sessions["AAAAAA"].peers[host.PeerToken].lastSeen

	if r, _ := a.registerPeer(host.SessionToken, host.PeerToken, ap("10.0.0.1:1000"), broadcast); r != nil {
		t.Errorf("expected packet from expired peer to be dropped, got %+v", r)
	}
	if a.sessions["AAAAAA"].peers[host.PeerToken].lastSeen != before {
		t.Error("expired peer was refreshed")
	}
}

func TestRegisterPeer_expiredSessionNotRevived(t *testing.T) {
	a := newTestApp()
	host := createTestSession(t, a, "AAAAAA")
	expireSession(a, "AAAAAA")
	before := a.sessions["AAAAAA"].lastActivity

	if r, _ := a.registerPeer(host.SessionToken, host.PeerToken, ap("10.0.0.1:1000"), broadcast); r != nil {
		t.Errorf("expected packet for expired session to be dropped, got %+v", r)
	}
	if a.sessions["AAAAAA"].lastActivity != before {
		t.Error("expired session was refreshed")
	}
}

func TestRegisterPeer_keepaliveRefreshesWithoutForwarding(t *testing.T) {
	a := newTestApp()
	host := createTestSession(t, a, "AAAAAA")
	p1, _ := a.joinSession("AAAAAA", "", "proj", testIP)
	a.registerPeer(host.SessionToken, p1.PeerToken, ap("10.0.0.1:2000"), broadcast)
	a.sessions["AAAAAA"].peers[host.PeerToken].lastSeen = nowMs() - 5_000

	r, _ := a.registerPeer(host.SessionToken, host.PeerToken, ap("10.0.0.1:1000"), keepalive)
	if r == nil || len(r.Targets) != 0 {
		t.Errorf("expected keepalive to produce no targets, got %+v", r)
	}
	if nowMs()-a.sessions["AAAAAA"].peers[host.PeerToken].lastSeen > 1_000 {
		t.Error("keepalive did not refresh lastSeen")
	}
}

func TestCleanup_removesExpiredPeers(t *testing.T) {
	a := newTestApp()
	createTestSession(t, a, "AAAAAA")
	p1, _ := a.joinSession("AAAAAA", "", "proj", testIP)
	expirePeer(a, "AAAAAA", p1.PeerToken)
	a.Cleanup()
	if _, ok := a.sessions["AAAAAA"].peers[p1.PeerToken]; ok {
		t.Error("expected expired peer to be removed by cleanup")
	}
}

// --- uniqueness and limits ---

func sequence[T any](t *testing.T, target *func() T, vals ...T) {
	orig := *target
	t.Cleanup(func() { *target = orig })
	i := 0
	*target = func() T {
		v := vals[min(i, len(vals)-1)]
		i++
		return v
	}
}

func TestCreateSession_sessionTokenCollisionRetries(t *testing.T) {
	a := newTestApp()
	sequence(t, &randU32, 42, 42, 43)
	first := createTestSession(t, a, "AAAAAA")
	second := createTestSession(t, a, "BBBBBB")
	if first.SessionToken != 42 || second.SessionToken != 43 {
		t.Errorf("expected tokens 42,43 got %d,%d", first.SessionToken, second.SessionToken)
	}
	if a.tokens[42] != "AAAAAA" {
		t.Errorf("first session lost its token: %q", a.tokens[42])
	}
}

func TestCreateSession_sessionTokenExhausted(t *testing.T) {
	a := newTestApp()
	sequence(t, &randU32, 42)
	createTestSession(t, a, "AAAAAA")
	if _, err := a.createSession("proj", 0, 4, "BBBBBB", "", "", private, testIP); err == nil {
		t.Error("expected error when no unique token can be generated")
	}
}

func TestJoinSession_peerTokenCollisionRetries(t *testing.T) {
	a := newTestApp()
	sequence(t, &randPeerToken, "aaaa0000", "aaaa0000", "bbbb0000")
	host := createTestSession(t, a, "AAAAAA")
	r, err := a.joinSession("AAAAAA", "", "proj", testIP)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.PeerToken == host.PeerToken {
		t.Fatal("joining peer got the host's token")
	}
	if p := a.sessions["AAAAAA"].peers[host.PeerToken]; p == nil || p.index != 0 {
		t.Errorf("host peer was overwritten: %+v", p)
	}
}

func TestCreateSession_limitReached(t *testing.T) {
	a := newTestApp()
	a.createSession("proj", 1, 4, "AAAAAA", "", "", private, testIP)
	if _, err := a.createSession("proj", 1, 4, "BBBBBB", "", "", private, testIP); err != errLimitReached {
		t.Errorf("expected errLimitReached, got %v", err)
	}
}

func TestCreateSession_limitHoldsUnderConcurrency(t *testing.T) {
	a := newTestApp()
	const limit, attempts = 5, 50
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := a.createSession("proj", limit, 4, "", "", "", private, testIP); err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if ok != limit {
		t.Errorf("expected exactly %d sessions, got %d", limit, ok)
	}
}

// --- source IP binding ---

func TestRegisterPeer_rejectsOtherIP(t *testing.T) {
	a := newTestApp()
	host := createTestSession(t, a, "AAAAAA")
	p := a.sessions["AAAAAA"].peers[host.PeerToken]
	p.lastSeen = nowMs() - 5_000
	before := p.lastSeen

	r, err := a.registerPeer(host.SessionToken, host.PeerToken, ap("203.0.113.9:1000"), broadcast)
	var mismatch *ipMismatchError
	if r != nil || !errors.As(err, &mismatch) || mismatch.joinIP != testIP {
		t.Errorf("expected ipMismatchError with join IP %v, got %+v, %v", testIP, r, err)
	}
	if p.addr.IsValid() || p.lastSeen != before {
		t.Error("packet from a different IP changed the peer")
	}
}

func TestRegisterPeer_spoofedVictimGetsNoTraffic(t *testing.T) {
	a := newTestApp()
	host := createTestSession(t, a, "AAAAAA")
	p1, _ := a.joinSession("AAAAAA", "", "proj", testIP)
	// Attacker sends p1's first packet with the victim's IP as source.
	a.registerPeer(host.SessionToken, p1.PeerToken, ap("198.51.100.7:4000"), keepalive)

	r, _ := a.registerPeer(host.SessionToken, host.PeerToken, ap("10.0.0.1:1000"), broadcast)
	if r == nil || len(r.Targets) != 0 {
		t.Errorf("expected no traffic towards the spoofed address, got %+v", r)
	}
}

func TestRegisterPeer_acceptsPortChange(t *testing.T) {
	a := newTestApp()
	host := createTestSession(t, a, "AAAAAA")
	a.registerPeer(host.SessionToken, host.PeerToken, ap("10.0.0.1:1000"), keepalive)
	if r, _ := a.registerPeer(host.SessionToken, host.PeerToken, ap("10.0.0.1:1001"), keepalive); r == nil {
		t.Fatal("expected packet from same IP with a new port to be accepted")
	}
	if got := a.sessions["AAAAAA"].peers[host.PeerToken].addr; got != ap("10.0.0.1:1001") {
		t.Errorf("expected address updated to new port, got %v", got)
	}
}

func TestRegisterPeer_acceptsIPv4MappedSource(t *testing.T) {
	a := newTestApp()
	host := createTestSession(t, a, "AAAAAA")
	if r, _ := a.registerPeer(host.SessionToken, host.PeerToken, ap("[::ffff:10.0.0.1]:1000"), keepalive); r == nil {
		t.Error("expected IPv4-mapped IPv6 source to match the IPv4 join IP")
	}
}
