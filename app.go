package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

const (
	peerActiveMs = 10_000
	codeChars    = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	broadcast    = 0xff
)

type visibility string

const (
	public  visibility = "public"
	private visibility = "private"
)

type peer struct {
	index   uint32
	address string
	port    int
	lastSeen int64
}

type session struct {
	token          uint32
	passwordHash   string
	visibility     visibility
	label          string
	projectID      string
	lastActivity   int64
	maxPeers       int
	peers          map[string]*peer
	nextPeerIndex  uint32
}

type App struct {
	mu       sync.RWMutex
	sessions map[string]*session
	tokens   map[uint32]string
	projects map[string]*Project
	config   Config
}

func newApp(cfg Config, projects map[string]*Project) *App {
	return &App{
		sessions: make(map[string]*session),
		tokens:   make(map[uint32]string),
		projects: projects,
		config:   cfg,
	}
}

// --- session management ---

type CreateResult struct {
	Code         string
	Password     string
	SessionToken uint32
	PeerToken    string
	PeerIndex    uint32
}

func (a *App) createSession(projectID string, maxPeers int, code, password, label string, vis visibility) (*CreateResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := nowMs()

	if code == "" {
		var err error
		for i := 0; i < 200; i++ {
			code = randomCode()
			if _, exists := a.sessions[code]; !exists {
				break
			}
			if i == 199 {
				return nil, fmt.Errorf("failed to generate unique code")
			}
			err = nil
			_ = err
		}
	} else if _, exists := a.sessions[code]; exists {
		return nil, errConflict
	}

	token := randomU32()
	peerToken := randomHex4()
	var pwHash string
	if password != "" {
		pwHash = hashPassword(password)
	}

	a.sessions[code] = &session{
		token:         token,
		passwordHash:  pwHash,
		visibility:    vis,
		label:         label,
		projectID:     projectID,
		lastActivity:  now,
		maxPeers:      maxPeers,
		peers:         map[string]*peer{peerToken: {index: 0, lastSeen: now}},
		nextPeerIndex: 1,
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

func (a *App) joinSession(code, password, projectID string) (*JoinResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	s, ok := a.sessions[code]
	if !ok || s.projectID != projectID {
		return nil, errNotFound
	}
	if s.passwordHash != "" && hashPassword(password) != s.passwordHash {
		return nil, errNotFound
	}
	if len(s.peers) >= s.maxPeers {
		return nil, errNotFound
	}

	idx := s.nextPeerIndex
	s.nextPeerIndex++
	pt := randomHex4()
	s.peers[pt] = &peer{index: idx, lastSeen: nowMs()}
	s.lastActivity = nowMs()

	slog.Info("peer joined", "code", code, "peerIndex", idx)
	return &JoinResult{SessionToken: s.token, PeerToken: pt, PeerIndex: idx}, nil
}

func (a *App) activeSessionCount(projectID string) int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	n := 0
	for _, s := range a.sessions {
		if s.projectID == projectID {
			n++
		}
	}
	return n
}

type PublicSession struct {
	Code             string
	Label            string
	Peers            int
	MaxPeers         int
	PasswordRequired bool
}

func (a *App) publicSessions(projectID string) []PublicSession {
	a.mu.RLock()
	defer a.mu.RUnlock()
	now := nowMs()
	var result []PublicSession
	for code, s := range a.sessions {
		if s.projectID != projectID || s.visibility != public {
			continue
		}
		active := 0
		for _, p := range s.peers {
			if now-p.lastSeen < peerActiveMs {
				active++
			}
		}
		result = append(result, PublicSession{
			Code:             code,
			Label:            s.label,
			Peers:            active,
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

func (a *App) sessionInfo(code, projectID string) (*SessionInfo, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	now := nowMs()
	s, ok := a.sessions[code]
	if !ok || s.projectID != projectID {
		return nil, errNotFound
	}
	active := 0
	for _, p := range s.peers {
		if now-p.lastSeen < peerActiveMs {
			active++
		}
	}
	return &SessionInfo{
		Code:             code,
		Label:            s.label,
		Visibility:       s.visibility,
		PasswordRequired: s.passwordHash != "",
		Peers:            active,
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

func (a *App) registerPeer(token uint32, peerToken, address string, port int, targetIndex byte) *ForwardResult {
	a.mu.Lock()
	defer a.mu.Unlock()

	code, ok := a.tokens[token]
	if !ok {
		return nil
	}
	s, ok := a.sessions[code]
	if !ok {
		return nil
	}
	p, ok := s.peers[peerToken]
	if !ok {
		return nil
	}

	now := nowMs()
	p.address = address
	p.port = port
	p.lastSeen = now
	s.lastActivity = now

	var targets []target
	for pt, op := range s.peers {
		if pt == peerToken || op.address == "" {
			continue
		}
		if targetIndex != broadcast && op.index != uint32(targetIndex) {
			continue
		}
		targets = append(targets, target{address: op.address, port: op.port})
	}

	return &ForwardResult{SenderIndex: p.index, Targets: targets}
}

func (a *App) cleanup() {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := nowMs()
	expired := 0
	for code, s := range a.sessions {
		if now-s.lastActivity > a.config.SessionTTLMs {
			delete(a.tokens, s.token)
			delete(a.sessions, code)
			expired++
			continue
		}
		for pt, p := range s.peers {
			if now-p.lastSeen > peerActiveMs {
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
