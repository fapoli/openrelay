package openrelay

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

const (
	codeChars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	keepalive = 0xfe
	broadcast = 0xff
	// maxPeersLimit keeps every peer index below the reserved keepalive/broadcast values.
	maxPeersLimit = keepalive
)

var errNotFound = errors.New("session not found")
var errConflict = errors.New("code already in use")
var errLimitReached = errors.New("max sessions reached for this project")

// Random sources, replaceable in tests.
var (
	randU32       = randomU32
	randPeerToken = randomHex4
)

const maxTokenAttempts = 100

type visibility string

const (
	public  visibility = "public"
	private visibility = "private"
)

type peer struct {
	index    uint32
	address  string
	port     int
	lastSeen int64
}

type session struct {
	token        uint32
	passwordHash string
	visibility   visibility
	label        string
	projectID    string
	lastActivity int64
	maxPeers     int
	peers        map[string]*peer
}

// SessionStore keeps all sessions in memory.
//
// Expiry is logical: a session or peer whose timestamp is older than its timeout is
// treated as gone by every operation from that moment on, and is never revived.
// Cleanup only reclaims the memory of already-expired entries.
type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*session
	tokens   map[uint32]string
	projects map[string]*Project
	config   Config
}

func NewSessionStore(cfg Config, projects map[string]*Project) *SessionStore {
	return &SessionStore{
		sessions: make(map[string]*session),
		tokens:   make(map[uint32]string),
		projects: projects,
		config:   cfg,
	}
}

// --- expiry ---

func (a *SessionStore) sessionExpired(s *session, now int64) bool {
	return now-s.lastActivity > a.config.SessionTTLMs
}

func (a *SessionStore) peerExpired(p *peer, now int64) bool {
	return now-p.lastSeen > a.config.PeerTimeoutMs
}

// liveSession returns the session for code if it exists, belongs to projectID and has not expired.
func (a *SessionStore) liveSession(code, projectID string, now int64) (*session, bool) {
	s, ok := a.sessions[code]
	if !ok || s.projectID != projectID || a.sessionExpired(s, now) {
		return nil, false
	}
	return s, true
}

func (a *SessionStore) livePeerCount(s *session, now int64) int {
	n := 0
	for _, p := range s.peers {
		if !a.peerExpired(p, now) {
			n++
		}
	}
	return n
}

// lowestFreeIndex returns the lowest index in [0, maxPeers) not held by a live peer.
func (a *SessionStore) lowestFreeIndex(s *session, now int64) (uint32, bool) {
	var used [maxPeersLimit]bool
	for _, p := range s.peers {
		if !a.peerExpired(p, now) && p.index < maxPeersLimit {
			used[p.index] = true
		}
	}
	for i := 0; i < s.maxPeers && i < maxPeersLimit; i++ {
		if !used[i] {
			return uint32(i), true
		}
	}
	return 0, false
}

// liveSessionCount returns the number of unexpired sessions of a project. Caller holds the lock.
func (a *SessionStore) liveSessionCount(projectID string, now int64) int {
	n := 0
	for _, s := range a.sessions {
		if s.projectID == projectID && !a.sessionExpired(s, now) {
			n++
		}
	}
	return n
}

// newSessionToken returns a token not used by any session still in memory.
func (a *SessionStore) newSessionToken() (uint32, error) {
	for i := 0; i < maxTokenAttempts; i++ {
		t := randU32()
		if _, taken := a.tokens[t]; !taken {
			return t, nil
		}
	}
	return 0, fmt.Errorf("failed to generate unique session token")
}

// newPeerToken returns a peer token not used by any peer of s still in memory.
func newPeerToken(s *session) (string, error) {
	for i := 0; i < maxTokenAttempts; i++ {
		t := randPeerToken()
		if _, taken := s.peers[t]; !taken {
			return t, nil
		}
	}
	return "", fmt.Errorf("failed to generate unique peer token")
}

// codeTaken reports whether code is held by a live session. An expired session holding
// the code is removed so the code can be reused.
func (a *SessionStore) codeTaken(code string, now int64) bool {
	s, ok := a.sessions[code]
	if !ok {
		return false
	}
	if a.sessionExpired(s, now) {
		delete(a.tokens, s.token)
		delete(a.sessions, code)
		return false
	}
	return true
}

// --- session management ---

type CreateResult struct {
	Code         string
	Password     string
	SessionToken uint32
	PeerToken    string
	PeerIndex    uint32
}

// createSession creates a session. maxSessions <= 0 means unlimited; the limit is
// checked under the same lock as the insert so concurrent creates cannot exceed it.
func (a *SessionStore) createSession(projectID string, maxSessions, maxPeers int, code, password, label string, vis visibility) (*CreateResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := nowMs()

	if maxSessions > 0 && a.liveSessionCount(projectID, now) >= maxSessions {
		return nil, errLimitReached
	}

	if code == "" {
		found := false
		for i := 0; i < 200; i++ {
			code = randomCode()
			if !a.codeTaken(code, now) {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("failed to generate unique code")
		}
	} else if a.codeTaken(code, now) {
		return nil, errConflict
	}

	token, err := a.newSessionToken()
	if err != nil {
		return nil, err
	}
	// The session is new, so any peer token is unique within it.
	peerToken := randPeerToken()
	var pwHash string
	if password != "" {
		pwHash = hashPassword(password)
	}

	a.sessions[code] = &session{
		token:        token,
		passwordHash: pwHash,
		visibility:   vis,
		label:        label,
		projectID:    projectID,
		lastActivity: now,
		maxPeers:     maxPeers,
		peers:        map[string]*peer{peerToken: {index: 0, lastSeen: now}},
	}
	a.tokens[token] = code

	slog.Info("session created", "project", projectID, "code", code, "maxPeers", maxPeers)
	return &CreateResult{Code: code, Password: password, SessionToken: token, PeerToken: peerToken, PeerIndex: 0}, nil
}

type JoinResult struct {
	SessionToken uint32
	PeerToken    string
	PeerIndex    uint32
}

func (a *SessionStore) joinSession(code, password, projectID string) (*JoinResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := nowMs()
	s, ok := a.liveSession(code, projectID, now)
	if !ok {
		return nil, errNotFound
	}
	if s.passwordHash != "" && hashPassword(password) != s.passwordHash {
		return nil, errNotFound
	}
	idx, ok := a.lowestFreeIndex(s, now)
	if !ok {
		return nil, errNotFound
	}

	pt, err := newPeerToken(s)
	if err != nil {
		return nil, err
	}
	s.peers[pt] = &peer{index: idx, lastSeen: now}
	s.lastActivity = now

	slog.Info("peer joined", "code", code, "peerIndex", idx)
	return &JoinResult{SessionToken: s.token, PeerToken: pt, PeerIndex: idx}, nil
}

type PublicSession struct {
	Code             string
	Label            string
	Peers            int
	MaxPeers         int
	PasswordRequired bool
}

func (a *SessionStore) publicSessions(projectID string) []PublicSession {
	a.mu.RLock()
	defer a.mu.RUnlock()
	now := nowMs()
	var result []PublicSession
	for code, s := range a.sessions {
		if s.projectID != projectID || s.visibility != public || a.sessionExpired(s, now) {
			continue
		}
		result = append(result, PublicSession{
			Code:             code,
			Label:            s.label,
			Peers:            a.livePeerCount(s, now),
			MaxPeers:         s.maxPeers,
			PasswordRequired: s.passwordHash != "",
		})
	}
	return result
}

type SessionInfo struct {
	Code             string
	Label            string
	Visibility       visibility
	PasswordRequired bool
	Peers            int
}

func (a *SessionStore) sessionInfo(code, projectID string) (*SessionInfo, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	now := nowMs()
	s, ok := a.liveSession(code, projectID, now)
	if !ok {
		return nil, errNotFound
	}
	return &SessionInfo{
		Code:             code,
		Label:            s.label,
		Visibility:       s.visibility,
		PasswordRequired: s.passwordHash != "",
		Peers:            a.livePeerCount(s, now),
	}, nil
}

type ForwardResult struct {
	SenderIndex uint32
	Targets     []target
}

type target struct {
	address string
	port    int
}

// registerPeer refreshes the sending peer and returns where its packet should go.
// It returns nil when the session or peer is unknown or expired; expired entries are
// not refreshed, so a late packet cannot revive them. A keepalive refreshes the peer
// and returns no targets.
func (a *SessionStore) registerPeer(token uint32, peerToken, address string, port int, targetIndex byte) *ForwardResult {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := nowMs()
	code, ok := a.tokens[token]
	if !ok {
		return nil
	}
	s, ok := a.sessions[code]
	if !ok || a.sessionExpired(s, now) {
		return nil
	}
	p, ok := s.peers[peerToken]
	if !ok || a.peerExpired(p, now) {
		return nil
	}

	p.address = address
	p.port = port
	p.lastSeen = now
	s.lastActivity = now

	if targetIndex == keepalive {
		return &ForwardResult{SenderIndex: p.index}
	}

	var targets []target
	for pt, op := range s.peers {
		if pt == peerToken || op.address == "" || a.peerExpired(op, now) {
			continue
		}
		if targetIndex != broadcast && op.index != uint32(targetIndex) {
			continue
		}
		targets = append(targets, target{address: op.address, port: op.port})
	}

	return &ForwardResult{SenderIndex: p.index, Targets: targets}
}

// Cleanup reclaims memory held by expired sessions and peers. It does not decide
// expiry; every other operation already ignores expired entries.
func (a *SessionStore) Cleanup() {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := nowMs()
	expired := 0
	for code, s := range a.sessions {
		if a.sessionExpired(s, now) {
			delete(a.tokens, s.token)
			delete(a.sessions, code)
			expired++
			continue
		}
		for pt, p := range s.peers {
			if a.peerExpired(p, now) {
				delete(s.peers, pt)
			}
		}
	}
	if expired > 0 {
		slog.Info("expired sessions cleaned up", "count", expired)
	}
}

// --- helpers ---

func nowMs() int64 {
	return time.Now().UnixMilli()
}

func randomCode() string {
	b := make([]byte, 6)
	rand.Read(b)
	out := make([]byte, 6)
	for i, v := range b {
		out[i] = codeChars[int(v)%len(codeChars)]
	}
	return string(out)
}

func randomU32() uint32 {
	b := make([]byte, 4)
	rand.Read(b)
	return binary.BigEndian.Uint32(b)
}

func randomHex4() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func hashPassword(pw string) string {
	h := sha256.Sum256([]byte(pw))
	return hex.EncodeToString(h[:])
}
