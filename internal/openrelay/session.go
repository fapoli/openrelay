package openrelay

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"
	"time"
)

const (
	codeChars     = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	keepalive     = 0xfe
	broadcast     = 0xff
	maxPeersLimit = keepalive
)

var errNotFound = errors.New("session not found")
var errConflict = errors.New("code already in use")
var errLimitReached = errors.New("max sessions reached for this project")
var errUnknownPeer = errors.New("unknown or expired session or peer")
var errIPMismatch = errors.New("source IP does not match join IP")

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
	index uint32
	// From the HTTP request (TCP, so not spoofable); UDP is only accepted from this IP.
	joinIP   netip.Addr
	addr     netip.AddrPort
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

// Expiry is logical: every operation ignores expired entries and never revives them.
// Cleanup only frees their memory.
type SessionStore struct {
	mu            sync.RWMutex
	sessions      map[string]*session
	tokens        map[uint32]string
	sessionTTLMs  int64
	peerTimeoutMs int64
}

func NewSessionStore(sessionTTLMs, peerTimeoutMs int64) *SessionStore {
	return &SessionStore{
		sessions:      make(map[string]*session),
		tokens:        make(map[uint32]string),
		sessionTTLMs:  sessionTTLMs,
		peerTimeoutMs: peerTimeoutMs,
	}
}

// --- expiry ---

func (ss *SessionStore) sessionExpired(s *session, now int64) bool {
	return now-s.lastActivity > ss.sessionTTLMs
}

func (ss *SessionStore) peerExpired(p *peer, now int64) bool {
	return now-p.lastSeen > ss.peerTimeoutMs
}

func (ss *SessionStore) liveSession(code, projectID string, now int64) (*session, bool) {
	s, ok := ss.sessions[code]
	if !ok || s.projectID != projectID || ss.sessionExpired(s, now) {
		return nil, false
	}
	return s, true
}

func (ss *SessionStore) livePeerCount(s *session, now int64) int {
	n := 0
	for _, p := range s.peers {
		if !ss.peerExpired(p, now) {
			n++
		}
	}
	return n
}

func (ss *SessionStore) lowestFreeIndex(s *session, now int64) (uint32, bool) {
	var used [maxPeersLimit]bool
	for _, p := range s.peers {
		if !ss.peerExpired(p, now) && p.index < maxPeersLimit {
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

func (ss *SessionStore) liveSessionCount(projectID string, now int64) int {
	n := 0
	for _, s := range ss.sessions {
		if s.projectID == projectID && !ss.sessionExpired(s, now) {
			n++
		}
	}
	return n
}

func (ss *SessionStore) newSessionToken() (uint32, error) {
	for i := 0; i < maxTokenAttempts; i++ {
		t := randU32()
		if _, taken := ss.tokens[t]; !taken {
			return t, nil
		}
	}
	return 0, fmt.Errorf("failed to generate unique session token")
}

func newPeerToken(s *session) (string, error) {
	for i := 0; i < maxTokenAttempts; i++ {
		t := randPeerToken()
		if _, taken := s.peers[t]; !taken {
			return t, nil
		}
	}
	return "", fmt.Errorf("failed to generate unique peer token")
}

func (ss *SessionStore) codeTaken(code string, now int64) bool {
	s, ok := ss.sessions[code]
	if !ok {
		return false
	}
	if ss.sessionExpired(s, now) {
		delete(ss.tokens, s.token)
		delete(ss.sessions, code)
		return false
	}
	return true
}

// --- session management ---

type newSession struct {
	Code       string
	Password   string
	Label      string
	Visibility visibility
	IP         netip.Addr
}

type CreateResult struct {
	Code         string  `json:"code"`
	Password     *string `json:"password"`
	SessionToken uint32  `json:"sessionToken"`
	PeerToken    string  `json:"peerToken"`
	PeerIndex    uint32  `json:"peerIndex"`
}

// The limit is checked under the same lock as the insert so concurrent creates cannot exceed it.
func (ss *SessionStore) createSession(p *Project, ns newSession) (*CreateResult, error) {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	now := nowMs()

	if p.MaxSessions > 0 && ss.liveSessionCount(p.ID, now) >= p.MaxSessions {
		return nil, errLimitReached
	}

	code := ns.Code
	if code == "" {
		found := false
		for i := 0; i < 200; i++ {
			code = randomCode()
			if !ss.codeTaken(code, now) {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("failed to generate unique code")
		}
	} else if ss.codeTaken(code, now) {
		return nil, errConflict
	}

	token, err := ss.newSessionToken()
	if err != nil {
		return nil, err
	}
	peerToken := randPeerToken()
	var pwHash string
	if ns.Password != "" {
		pwHash = hashPassword(ns.Password)
	}

	ss.sessions[code] = &session{
		token:        token,
		passwordHash: pwHash,
		visibility:   ns.Visibility,
		label:        ns.Label,
		projectID:    p.ID,
		lastActivity: now,
		maxPeers:     p.MaxPeersPerSession,
		peers:        map[string]*peer{peerToken: {index: 0, joinIP: ns.IP.Unmap(), lastSeen: now}},
	}
	ss.tokens[token] = code

	slog.Info("session created", "project", p.ID, "code", code, "maxPeers", p.MaxPeersPerSession)
	return &CreateResult{Code: code, Password: optional(ns.Password), SessionToken: token, PeerToken: peerToken, PeerIndex: 0}, nil
}

type JoinResult struct {
	SessionToken uint32 `json:"sessionToken"`
	PeerToken    string `json:"peerToken"`
	PeerIndex    uint32 `json:"peerIndex"`
}

func (ss *SessionStore) joinSession(code, password, projectID string, ip netip.Addr) (*JoinResult, error) {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	now := nowMs()
	s, ok := ss.liveSession(code, projectID, now)
	if !ok {
		return nil, errNotFound
	}
	if s.passwordHash != "" && hashPassword(password) != s.passwordHash {
		return nil, errNotFound
	}
	idx, ok := ss.lowestFreeIndex(s, now)
	if !ok {
		return nil, errNotFound
	}

	pt, err := newPeerToken(s)
	if err != nil {
		return nil, err
	}
	s.peers[pt] = &peer{index: idx, joinIP: ip.Unmap(), lastSeen: now}
	s.lastActivity = now

	slog.Info("peer joined", "code", code, "peerIndex", idx)
	return &JoinResult{SessionToken: s.token, PeerToken: pt, PeerIndex: idx}, nil
}

type PublicSession struct {
	Code             string `json:"code"`
	Label            string `json:"label"`
	Peers            int    `json:"peers"`
	MaxPeers         int    `json:"maxPeers"`
	PasswordRequired bool   `json:"passwordRequired"`
}

func (ss *SessionStore) publicSessions(projectID string) []PublicSession {
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	now := nowMs()
	result := []PublicSession{}
	for code, s := range ss.sessions {
		if s.projectID != projectID || s.visibility != public || ss.sessionExpired(s, now) {
			continue
		}
		result = append(result, PublicSession{
			Code:             code,
			Label:            s.label,
			Peers:            ss.livePeerCount(s, now),
			MaxPeers:         s.maxPeers,
			PasswordRequired: s.passwordHash != "",
		})
	}
	return result
}

type SessionInfo struct {
	Code             string     `json:"code"`
	Label            *string    `json:"label"`
	Visibility       visibility `json:"visibility"`
	PasswordRequired bool       `json:"passwordRequired"`
	Peers            int        `json:"peers"`
}

func (ss *SessionStore) sessionInfo(code, projectID string) (*SessionInfo, error) {
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	now := nowMs()
	s, ok := ss.liveSession(code, projectID, now)
	if !ok {
		return nil, errNotFound
	}
	return &SessionInfo{
		Code:             code,
		Label:            optional(s.label),
		Visibility:       s.visibility,
		PasswordRequired: s.passwordHash != "",
		Peers:            ss.livePeerCount(s, now),
	}, nil
}

type ForwardResult struct {
	SenderIndex uint32
	Targets     []netip.AddrPort
}

func (ss *SessionStore) registerPeer(token uint32, peerToken string, src netip.AddrPort, targetIndex byte) (*ForwardResult, error) {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	now := nowMs()
	code, ok := ss.tokens[token]
	if !ok {
		return nil, errUnknownPeer
	}
	s, ok := ss.sessions[code]
	if !ok || ss.sessionExpired(s, now) {
		return nil, errUnknownPeer
	}
	p, ok := s.peers[peerToken]
	if !ok || ss.peerExpired(p, now) {
		return nil, errUnknownPeer
	}
	if src.Addr().Unmap() != p.joinIP {
		return nil, fmt.Errorf("%w %s", errIPMismatch, p.joinIP)
	}

	p.addr = src
	p.lastSeen = now
	s.lastActivity = now

	if targetIndex == keepalive {
		return &ForwardResult{SenderIndex: p.index}, nil
	}

	var targets []netip.AddrPort
	for pt, op := range s.peers {
		if pt == peerToken || !op.addr.IsValid() || ss.peerExpired(op, now) {
			continue
		}
		if targetIndex != broadcast && op.index != uint32(targetIndex) {
			continue
		}
		targets = append(targets, op.addr)
	}

	return &ForwardResult{SenderIndex: p.index, Targets: targets}, nil
}

func (ss *SessionStore) Cleanup() {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	now := nowMs()
	expired := 0
	for code, s := range ss.sessions {
		if ss.sessionExpired(s, now) {
			delete(ss.tokens, s.token)
			delete(ss.sessions, code)
			expired++
			continue
		}
		for pt, p := range s.peers {
			if ss.peerExpired(p, now) {
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

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func hashPassword(pw string) string {
	h := sha256.Sum256([]byte(pw))
	return hex.EncodeToString(h[:])
}
