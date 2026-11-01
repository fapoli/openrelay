package openrelay

import (
	"errors"
	"net/netip"
	"strings"
	"sync"
	"testing"
)

var testIP = netip.MustParseAddr("10.0.0.1")

var testProject = &Project{ID: "proj", MaxPeersPerSession: 4}

func ap(s string) netip.AddrPort { return netip.MustParseAddrPort(s) }

func newTestSessions() *SessionStore {
	return NewSessionStore(300_000, 10_000)
}

func createTestSession(t *testing.T, ss *SessionStore, code string) *CreateResult {
	t.Helper()
	r, err := ss.createSession(testProject, newSession{Code: code, Visibility: private, IP: testIP})
	if err != nil {
		t.Fatalf("createSession: %v", err)
	}
	return r
}

func TestCreateSession_success(t *testing.T) {
	ss := newTestSessions()
	r, err := ss.createSession(testProject, newSession{Code: "AAAAAA", Visibility: private, IP: testIP})
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
	ss := newTestSessions()
	r, err := ss.createSession(testProject, newSession{Visibility: private, IP: testIP})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(r.Code) != 6 {
		t.Errorf("expected 6-char code, got %q", r.Code)
	}
}

func TestCreateSession_conflict(t *testing.T) {
	ss := newTestSessions()
	createTestSession(t, ss, "AAAAAA")
	_, err := ss.createSession(testProject, newSession{Code: "AAAAAA", Visibility: private, IP: testIP})
	if err != errConflict {
		t.Errorf("expected errConflict, got %v", err)
	}
}

func TestJoinSession_success(t *testing.T) {
	ss := newTestSessions()
	created := createTestSession(t, ss, "AAAAAA")
	r, err := ss.joinSession("AAAAAA", "", "proj", testIP)
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
	ss := newTestSessions()
	createTestSession(t, ss, "AAAAAA")
	r1, _ := ss.joinSession("AAAAAA", "", "proj", testIP)
	r2, _ := ss.joinSession("AAAAAA", "", "proj", testIP)
	if r1.PeerIndex != 1 || r2.PeerIndex != 2 {
		t.Errorf("expected indices 1,2 got %d,%d", r1.PeerIndex, r2.PeerIndex)
	}
}

func TestJoinSession_wrongPassword(t *testing.T) {
	ss := newTestSessions()
	ss.createSession(testProject, newSession{Code: "AAAAAA", Password: "correct", Visibility: private, IP: testIP})
	_, err := ss.joinSession("AAAAAA", "wrong", "proj", testIP)
	if err != errNotFound {
		t.Errorf("expected errNotFound, got %v", err)
	}
}

func TestJoinSession_fullSession(t *testing.T) {
	ss := newTestSessions()
	createTestSession(t, ss, "AAAAAA") // host = 1 peer
	ss.joinSession("AAAAAA", "", "proj", testIP)
	ss.joinSession("AAAAAA", "", "proj", testIP)
	ss.joinSession("AAAAAA", "", "proj", testIP) // now at max (4)
	_, err := ss.joinSession("AAAAAA", "", "proj", testIP)
	if err != errNotFound {
		t.Errorf("expected errNotFound when full, got %v", err)
	}
}

func TestJoinSession_wrongProject(t *testing.T) {
	ss := newTestSessions()
	createTestSession(t, ss, "AAAAAA")
	_, err := ss.joinSession("AAAAAA", "", "other-proj", testIP)
	if err != errNotFound {
		t.Errorf("expected errNotFound for wrong project, got %v", err)
	}
}

func TestPublicSessions_filtersPrivate(t *testing.T) {
	ss := newTestSessions()
	ss.createSession(testProject, newSession{Code: "PUBL22", Label: "Open", Visibility: public, IP: testIP})
	ss.createSession(testProject, newSession{Code: "PRIV22", Visibility: private, IP: testIP})

	sessions := ss.publicSessions("proj")
	if len(sessions) != 1 || sessions[0].Code != "PUBL22" {
		t.Errorf("expected only PUBL22, got %+v", sessions)
	}
}

func TestPublicSessions_filtersOtherProjects(t *testing.T) {
	ss := newTestSessions()
	ss.createSession(testProject, newSession{Code: "PUBL22", Visibility: public, IP: testIP})
	ss.createSession(&Project{ID: "other", MaxPeersPerSession: 4}, newSession{Code: "PUBLA2", Visibility: public, IP: testIP})

	sessions := ss.publicSessions("proj")
	if len(sessions) != 1 || sessions[0].Code != "PUBL22" {
		t.Errorf("expected only proj's session, got %+v", sessions)
	}
}

func TestSessionInfo_notFound(t *testing.T) {
	ss := newTestSessions()
	_, err := ss.sessionInfo("XXXXXX", "proj")
	if err != errNotFound {
		t.Errorf("expected errNotFound, got %v", err)
	}
}

func TestCleanup_removesExpired(t *testing.T) {
	ss := newTestSessions()
	ss.sessionTTLMs = -1 // expire immediately (now - lastActivity >= 0 > -1)
	createTestSession(t, ss, "AAAAAA")
	ss.Cleanup()
	if _, err := ss.sessionInfo("AAAAAA", "proj"); err == nil {
		t.Error("expected session to be removed after cleanup")
	}
}

// --- liveness (logical expiry, no Cleanup) ---

func expirePeer(ss *SessionStore, code, peerToken string) {
	ss.sessions[code].peers[peerToken].lastSeen = nowMs() - ss.peerTimeoutMs - 1
}

func expireSession(ss *SessionStore, code string) {
	ss.sessions[code].lastActivity = nowMs() - ss.sessionTTLMs - 1
}

func TestJoinSession_reusesLowestFreeIndex(t *testing.T) {
	ss := newTestSessions()
	createTestSession(t, ss, "AAAAAA")
	ss.joinSession("AAAAAA", "", "proj", testIP)          // 1
	r2, _ := ss.joinSession("AAAAAA", "", "proj", testIP) // 2
	ss.joinSession("AAAAAA", "", "proj", testIP)          // 3
	expirePeer(ss, "AAAAAA", r2.PeerToken)

	r, err := ss.joinSession("AAAAAA", "", "proj", testIP)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.PeerIndex != 2 {
		t.Errorf("expected reused index 2, got %d", r.PeerIndex)
	}
}

func TestJoinSession_expiredPeersDontCountTowardFull(t *testing.T) {
	ss := newTestSessions()
	createTestSession(t, ss, "AAAAAA")
	r1, _ := ss.joinSession("AAAAAA", "", "proj", testIP)
	ss.joinSession("AAAAAA", "", "proj", testIP)
	ss.joinSession("AAAAAA", "", "proj", testIP) // full (4)
	expirePeer(ss, "AAAAAA", r1.PeerToken)

	if _, err := ss.joinSession("AAAAAA", "", "proj", testIP); err != nil {
		t.Errorf("expected join to succeed after a peer expired, got %v", err)
	}
}

func TestJoinSession_expiredSession(t *testing.T) {
	ss := newTestSessions()
	createTestSession(t, ss, "AAAAAA")
	expireSession(ss, "AAAAAA")
	if _, err := ss.joinSession("AAAAAA", "", "proj", testIP); err != errNotFound {
		t.Errorf("expected errNotFound, got %v", err)
	}
}

func TestCreateSession_expiredCodeIsFree(t *testing.T) {
	ss := newTestSessions()
	old := createTestSession(t, ss, "AAAAAA")
	expireSession(ss, "AAAAAA")

	r, err := ss.createSession(testProject, newSession{Code: "AAAAAA", Visibility: private, IP: testIP})
	if err != nil {
		t.Fatalf("expected expired code to be reusable, got %v", err)
	}
	if _, ok := ss.tokens[old.SessionToken]; ok && old.SessionToken != r.SessionToken {
		t.Error("expected old session token to be removed")
	}
}

func TestActiveSessionCount_ignoresExpired(t *testing.T) {
	ss := newTestSessions()
	createTestSession(t, ss, "AAAAAA")
	createTestSession(t, ss, "BBBBBB")
	expireSession(ss, "AAAAAA")
	if n := ss.liveSessionCount("proj", nowMs()); n != 1 {
		t.Errorf("expected 1 active session, got %d", n)
	}
}

func TestPublicSessions_hidesExpired(t *testing.T) {
	ss := newTestSessions()
	ss.createSession(testProject, newSession{Code: "PUBL22", Visibility: public, IP: testIP})
	expireSession(ss, "PUBL22")
	if s := ss.publicSessions("proj"); len(s) != 0 {
		t.Errorf("expected no sessions, got %+v", s)
	}
}

func TestSessionInfo_expired(t *testing.T) {
	ss := newTestSessions()
	createTestSession(t, ss, "AAAAAA")
	expireSession(ss, "AAAAAA")
	if _, err := ss.sessionInfo("AAAAAA", "proj"); err != errNotFound {
		t.Errorf("expected errNotFound, got %v", err)
	}
}

func TestSessionInfo_countsOnlyLivePeers(t *testing.T) {
	ss := newTestSessions()
	createTestSession(t, ss, "AAAAAA")
	r1, _ := ss.joinSession("AAAAAA", "", "proj", testIP)
	expirePeer(ss, "AAAAAA", r1.PeerToken)
	info, _ := ss.sessionInfo("AAAAAA", "proj")
	if info.Peers != 1 {
		t.Errorf("expected 1 live peer, got %d", info.Peers)
	}
}

func TestRegisterPeer_expiredPeerNotTargeted(t *testing.T) {
	ss := newTestSessions()
	host := createTestSession(t, ss, "AAAAAA")
	p1, _ := ss.joinSession("AAAAAA", "", "proj", testIP)
	ss.registerPeer(host.SessionToken, p1.PeerToken, ap("10.0.0.1:2000"), broadcast)
	expirePeer(ss, "AAAAAA", p1.PeerToken)

	r, _ := ss.registerPeer(host.SessionToken, host.PeerToken, ap("10.0.0.1:1000"), broadcast)
	if r == nil || len(r.Targets) != 0 {
		t.Errorf("expected no targets, got %+v", r)
	}
}

func TestRegisterPeer_unicastSkipsExpiredDuplicateIndex(t *testing.T) {
	ss := newTestSessions()
	host := createTestSession(t, ss, "AAAAAA")
	old, _ := ss.joinSession("AAAAAA", "", "proj", testIP)
	ss.registerPeer(host.SessionToken, old.PeerToken, ap("10.0.0.1:2000"), broadcast)
	expirePeer(ss, "AAAAAA", old.PeerToken)
	repl, _ := ss.joinSession("AAAAAA", "", "proj", testIP) // reuses index 1
	ss.registerPeer(host.SessionToken, repl.PeerToken, ap("10.0.0.1:3000"), keepalive)

	r, _ := ss.registerPeer(host.SessionToken, host.PeerToken, ap("10.0.0.1:1000"), 1)
	if r == nil || len(r.Targets) != 1 || r.Targets[0] != ap("10.0.0.1:3000") {
		t.Errorf("expected only the live peer at index 1, got %+v", r)
	}
}

func TestRegisterPeer_expiredSenderNotRevived(t *testing.T) {
	ss := newTestSessions()
	host := createTestSession(t, ss, "AAAAAA")
	expirePeer(ss, "AAAAAA", host.PeerToken)
	before := ss.sessions["AAAAAA"].peers[host.PeerToken].lastSeen

	if r, _ := ss.registerPeer(host.SessionToken, host.PeerToken, ap("10.0.0.1:1000"), broadcast); r != nil {
		t.Errorf("expected packet from expired peer to be dropped, got %+v", r)
	}
	if ss.sessions["AAAAAA"].peers[host.PeerToken].lastSeen != before {
		t.Error("expired peer was refreshed")
	}
}

func TestRegisterPeer_expiredSessionNotRevived(t *testing.T) {
	ss := newTestSessions()
	host := createTestSession(t, ss, "AAAAAA")
	expireSession(ss, "AAAAAA")
	before := ss.sessions["AAAAAA"].lastActivity

	if r, _ := ss.registerPeer(host.SessionToken, host.PeerToken, ap("10.0.0.1:1000"), broadcast); r != nil {
		t.Errorf("expected packet for expired session to be dropped, got %+v", r)
	}
	if ss.sessions["AAAAAA"].lastActivity != before {
		t.Error("expired session was refreshed")
	}
}

func TestRegisterPeer_keepaliveRefreshesWithoutForwarding(t *testing.T) {
	ss := newTestSessions()
	host := createTestSession(t, ss, "AAAAAA")
	p1, _ := ss.joinSession("AAAAAA", "", "proj", testIP)
	ss.registerPeer(host.SessionToken, p1.PeerToken, ap("10.0.0.1:2000"), broadcast)
	ss.sessions["AAAAAA"].peers[host.PeerToken].lastSeen = nowMs() - 5_000

	r, _ := ss.registerPeer(host.SessionToken, host.PeerToken, ap("10.0.0.1:1000"), keepalive)
	if r == nil || len(r.Targets) != 0 {
		t.Errorf("expected keepalive to produce no targets, got %+v", r)
	}
	if nowMs()-ss.sessions["AAAAAA"].peers[host.PeerToken].lastSeen > 1_000 {
		t.Error("keepalive did not refresh lastSeen")
	}
}

func TestCleanup_removesExpiredPeers(t *testing.T) {
	ss := newTestSessions()
	createTestSession(t, ss, "AAAAAA")
	p1, _ := ss.joinSession("AAAAAA", "", "proj", testIP)
	expirePeer(ss, "AAAAAA", p1.PeerToken)
	ss.Cleanup()
	if _, ok := ss.sessions["AAAAAA"].peers[p1.PeerToken]; ok {
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
	ss := newTestSessions()
	sequence(t, &randU32, 42, 42, 43)
	first := createTestSession(t, ss, "AAAAAA")
	second := createTestSession(t, ss, "BBBBBB")
	if first.SessionToken != 42 || second.SessionToken != 43 {
		t.Errorf("expected tokens 42,43 got %d,%d", first.SessionToken, second.SessionToken)
	}
	if ss.tokens[42] != "AAAAAA" {
		t.Errorf("first session lost its token: %q", ss.tokens[42])
	}
}

func TestCreateSession_sessionTokenExhausted(t *testing.T) {
	ss := newTestSessions()
	sequence(t, &randU32, 42)
	createTestSession(t, ss, "AAAAAA")
	if _, err := ss.createSession(testProject, newSession{Code: "BBBBBB", Visibility: private, IP: testIP}); err == nil {
		t.Error("expected error when no unique token can be generated")
	}
}

func TestJoinSession_peerTokenCollisionRetries(t *testing.T) {
	ss := newTestSessions()
	sequence(t, &randPeerToken, "aaaa0000", "aaaa0000", "bbbb0000")
	host := createTestSession(t, ss, "AAAAAA")
	r, err := ss.joinSession("AAAAAA", "", "proj", testIP)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.PeerToken == host.PeerToken {
		t.Fatal("joining peer got the host's token")
	}
	if p := ss.sessions["AAAAAA"].peers[host.PeerToken]; p == nil || p.index != 0 {
		t.Errorf("host peer was overwritten: %+v", p)
	}
}

func TestCreateSession_limitReached(t *testing.T) {
	ss := newTestSessions()
	ss.createSession(&Project{ID: "proj", MaxSessions: 1, MaxPeersPerSession: 4}, newSession{Code: "AAAAAA", Visibility: private, IP: testIP})
	if _, err := ss.createSession(&Project{ID: "proj", MaxSessions: 1, MaxPeersPerSession: 4}, newSession{Code: "BBBBBB", Visibility: private, IP: testIP}); err != errLimitReached {
		t.Errorf("expected errLimitReached, got %v", err)
	}
}

func TestCreateSession_limitHoldsUnderConcurrency(t *testing.T) {
	ss := newTestSessions()
	const limit, attempts = 5, 50
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := ss.createSession(&Project{ID: "proj", MaxSessions: limit, MaxPeersPerSession: 4}, newSession{Visibility: private, IP: testIP}); err == nil {
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
	ss := newTestSessions()
	host := createTestSession(t, ss, "AAAAAA")
	p := ss.sessions["AAAAAA"].peers[host.PeerToken]
	p.lastSeen = nowMs() - 5_000
	before := p.lastSeen

	r, err := ss.registerPeer(host.SessionToken, host.PeerToken, ap("203.0.113.9:1000"), broadcast)
	if r != nil || !errors.Is(err, errIPMismatch) || !strings.Contains(err.Error(), testIP.String()) {
		t.Errorf("expected errIPMismatch naming join IP %v, got %+v, %v", testIP, r, err)
	}
	if p.addr.IsValid() || p.lastSeen != before {
		t.Error("packet from a different IP changed the peer")
	}
}

func TestRegisterPeer_spoofedVictimGetsNoTraffic(t *testing.T) {
	ss := newTestSessions()
	host := createTestSession(t, ss, "AAAAAA")
	p1, _ := ss.joinSession("AAAAAA", "", "proj", testIP)
	// Attacker sends p1's first packet with the victim's IP as source.
	ss.registerPeer(host.SessionToken, p1.PeerToken, ap("198.51.100.7:4000"), keepalive)

	r, _ := ss.registerPeer(host.SessionToken, host.PeerToken, ap("10.0.0.1:1000"), broadcast)
	if r == nil || len(r.Targets) != 0 {
		t.Errorf("expected no traffic towards the spoofed address, got %+v", r)
	}
}

func TestRegisterPeer_acceptsPortChange(t *testing.T) {
	ss := newTestSessions()
	host := createTestSession(t, ss, "AAAAAA")
	ss.registerPeer(host.SessionToken, host.PeerToken, ap("10.0.0.1:1000"), keepalive)
	if r, _ := ss.registerPeer(host.SessionToken, host.PeerToken, ap("10.0.0.1:1001"), keepalive); r == nil {
		t.Fatal("expected packet from same IP with a new port to be accepted")
	}
	if got := ss.sessions["AAAAAA"].peers[host.PeerToken].addr; got != ap("10.0.0.1:1001") {
		t.Errorf("expected address updated to new port, got %v", got)
	}
}

func TestRegisterPeer_acceptsIPv4MappedSource(t *testing.T) {
	ss := newTestSessions()
	host := createTestSession(t, ss, "AAAAAA")
	if r, _ := ss.registerPeer(host.SessionToken, host.PeerToken, ap("[::ffff:10.0.0.1]:1000"), keepalive); r == nil {
		t.Error("expected IPv4-mapped IPv6 source to match the IPv4 join IP")
	}
}
