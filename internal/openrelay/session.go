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

var (
	errNotFound     = errors.New("session not found")
	errConflict     = errors.New("code already in use")
	errLimitReached = errors.New("max sessions reached for this project")
	errUnknownPeer  = errors.New("unknown or expired session or peer")
	errIPMismatch   = errors.New("source IP does not match join IP")
)

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
	// joinIP is the IP of the HTTP request that created this peer.
	joinIP   netip.Addr
	addr     netip.AddrPort
	lastSeen time.Time
}

type session struct {
	token        uint32
	passwordHash string
	visibility   visibility
	label        string
	projectID    string
	lastActivity time.Time
	maxPeers     int
	peers        map[string]*peer
}

// SessionStore holds all sessions and their peers in memory.
// Expired sessions and peers are ignored by every operation and never revived;
// Cleanup only frees their memory.
type SessionStore struct {
	mu          sync.RWMutex
	sessions    map[string]*session
	tokens      map[uint32]string
	sessionTTL  time.Duration
	peerTimeout time.Duration
}

// NewSessionStore returns an empty store with the given session and peer inactivity timeouts.
func NewSessionStore(sessionTTL, peerTimeout time.Duration) *SessionStore {
	return &SessionStore{
		sessions:    make(map[string]*session),
		tokens:      make(map[uint32]string),
		sessionTTL:  sessionTTL,
		peerTimeout: peerTimeout,
	}
}

// --- expiry ---

func (ss *SessionStore) sessionExpired(s *session, now time.Time) bool {
	return now.Sub(s.lastActivity) > ss.sessionTTL
}

func (ss *SessionStore) peerExpired(p *peer, now time.Time) bool {
	return now.Sub(p.lastSeen) > ss.peerTimeout
}

func (ss *SessionStore) liveSession(code, projectID string, now time.Time) (*session, bool) {
	s, ok := ss.sessions[code]
	if !ok || s.projectID != projectID || ss.sessionExpired(s, now) {
		return nil, false
	}
	return s, true
}

func (ss *SessionStore) livePeerCount(s *session, now time.Time) int {
	n := 0
	for _, p := range s.peers {
		if !ss.peerExpired(p, now) {
			n++
		}
	}
	return n
}

func (ss *SessionStore) lowestFreeIndex(s *session, now time.Time) (uint32, bool) {
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

func (ss *SessionStore) liveSessionCount(projectID string, now time.Time) int {
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

func (ss *SessionStore) codeTaken(code string, now time.Time) bool {
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

// CreateResult is returned to the client that created a session.
type CreateResult struct {
	Code         string  `json:"code"`
	Password     *string `json:"password"`
	SessionToken uint32  `json:"sessionToken"`
	PeerToken    string  `json:"peerToken"`
	PeerIndex    uint32  `json:"peerIndex"`
}

func (ss *SessionStore) createSession(project *Project, req newSession) (*CreateResult, error) {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	now := time.Now()

	if project.MaxSessions > 0 && ss.liveSessionCount(project.ID, now) >= project.MaxSessions {
		return nil, errLimitReached
	}

	code := req.Code
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
	if req.Password != "" {
		pwHash = hashPassword(req.Password)
	}

	ss.sessions[code] = &session{
		token:        token,
		passwordHash: pwHash,
		visibility:   req.Visibility,
		label:        req.Label,
		projectID:    project.ID,
		lastActivity: now,
		maxPeers:     project.MaxPeersPerSession,
		peers:        map[string]*peer{peerToken: {index: 0, joinIP: req.IP.Unmap(), lastSeen: now}},
	}
	ss.tokens[token] = code

	slog.Info("session created", "project", project.ID, "code", code, "maxPeers", project.MaxPeersPerSession)
	return &CreateResult{Code: code, Password: optional(req.Password), SessionToken: token, PeerToken: peerToken, PeerIndex: 0}, nil
}

// JoinResult is returned to a client that joined a session.
type JoinResult struct {
	SessionToken uint32 `json:"sessionToken"`
	PeerToken    string `json:"peerToken"`
	PeerIndex    uint32 `json:"peerIndex"`
}

func (ss *SessionStore) joinSession(code, password, projectID string, ip netip.Addr) (*JoinResult, error) {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	now := time.Now()
	sess, ok := ss.liveSession(code, projectID, now)
	if !ok {
		return nil, errNotFound
	}
	if sess.passwordHash != "" && hashPassword(password) != sess.passwordHash {
		return nil, errNotFound
	}
	index, ok := ss.lowestFreeIndex(sess, now)
	if !ok {
		return nil, errNotFound
	}

	peerToken, err := newPeerToken(sess)
	if err != nil {
		return nil, err
	}
	sess.peers[peerToken] = &peer{index: index, joinIP: ip.Unmap(), lastSeen: now}
	sess.lastActivity = now

	slog.Info("peer joined", "code", code, "peerIndex", index)
	return &JoinResult{SessionToken: sess.token, PeerToken: peerToken, PeerIndex: index}, nil
}

// PublicSession is a public session as shown in the lobby list.
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
	now := time.Now()
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

// SessionInfo describes a single session, public or private.
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
	now := time.Now()
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

// ForwardResult is the sender's peer index and the addresses a packet must be forwarded to.
type ForwardResult struct {
	SenderIndex uint32
	Targets     []netip.AddrPort
}

func (ss *SessionStore) registerPeer(token uint32, peerToken string, src netip.AddrPort, targetIndex byte) (*ForwardResult, error) {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	now := time.Now()
	code, ok := ss.tokens[token]
	if !ok {
		return nil, errUnknownPeer
	}
	sess, ok := ss.sessions[code]
	if !ok || ss.sessionExpired(sess, now) {
		return nil, errUnknownPeer
	}
	sender, ok := sess.peers[peerToken]
	if !ok || ss.peerExpired(sender, now) {
		return nil, errUnknownPeer
	}
	if src.Addr().Unmap() != sender.joinIP {
		return nil, fmt.Errorf("%w: %s", errIPMismatch, sender.joinIP)
	}

	sender.addr = src
	sender.lastSeen = now
	sess.lastActivity = now

	if targetIndex == keepalive {
		return &ForwardResult{SenderIndex: sender.index}, nil
	}

	var targets []netip.AddrPort
	for otherToken, other := range sess.peers {
		if otherToken == peerToken || !other.addr.IsValid() || ss.peerExpired(other, now) {
			continue
		}
		if targetIndex != broadcast && other.index != uint32(targetIndex) {
			continue
		}
		targets = append(targets, other.addr)
	}

	return &ForwardResult{SenderIndex: sender.index, Targets: targets}, nil
}

// Cleanup removes expired sessions and peers from memory.
func (ss *SessionStore) Cleanup() {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	now := time.Now()
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
