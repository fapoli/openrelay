package main

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

var validCode = regexp.MustCompile(`^[A-HJ-NP-Z2-9]{6}$`)

func (a *App) routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	r.Group(func(r chi.Router) {
		r.Use(a.auth)
		r.Post("/sessions", a.handleCreateSession)
		r.Get("/sessions", a.handleListSessions)
		r.Post("/sessions/{code}/join", a.handleJoinSession)
		r.Get("/sessions/{code}", a.handleGetSession)
	})

	return r
}

func (a *App) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			writeErr(w, http.StatusUnauthorized, "missing authorization header")
			return
		}
		project, ok := a.projects[auth[7:]]
		if !ok {
			writeErr(w, http.StatusUnauthorized, "invalid API key")
			return
		}
		r = r.WithContext(withProject(r.Context(), project))
		next.ServeHTTP(w, r)
	})
}

func (a *App) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	project := projectFromCtx(r.Context())

	var body struct {
		Code       string `json:"code"`
		Password   string `json:"password"`
		Label      string `json:"label"`
		Visibility string `json:"visibility"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if body.Code != "" && !validCode.MatchString(body.Code) {
		writeErr(w, http.StatusUnprocessableEntity, "invalid code")
		return
	}
	if body.Password != "" && len(body.Password) < 6 {
		writeErr(w, http.StatusUnprocessableEntity, "password must be at least 6 characters")
		return
	}

	vis := private
	if body.Visibility == "public" {
		vis = public
	} else if body.Visibility != "" && body.Visibility != "private" {
		writeErr(w, http.StatusUnprocessableEntity, "invalid visibility")
		return
	}

	label := strings.TrimSpace(body.Label)
	if len(label) > 64 {
		writeErr(w, http.StatusUnprocessableEntity, "label too long")
		return
	}

	if project.MaxSessions > 0 && a.activeSessionCount(project.ID) >= project.MaxSessions {
		writeErr(w, http.StatusTooManyRequests, "max sessions reached for this project")
		return
	}

	result, err := a.createSession(project.ID, project.MaxPeersPerSession, body.Code, body.Password, label, vis)
	if err == errConflict {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to create session")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"code":         result.Code,
		"password":     nullableString(result.Password),
		"sessionToken": result.SessionToken,
		"peerToken":    result.PeerToken,
		"peerIndex":    result.PeerIndex,
	})
}

func (a *App) handleJoinSession(w http.ResponseWriter, r *http.Request) {
	project := projectFromCtx(r.Context())
	code := chi.URLParam(r, "code")

	var body struct {
		Password string `json:"password"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	result, err := a.joinSession(code, body.Password, project.ID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "invalid code or password")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"sessionToken": result.SessionToken,
		"peerToken":    result.PeerToken,
		"peerIndex":    result.PeerIndex,
	})
}

func (a *App) handleListSessions(w http.ResponseWriter, r *http.Request) {
	project := projectFromCtx(r.Context())
	sessions := a.publicSessions(project.ID)

	type item struct {
		Code             string `json:"code"`
		Label            string `json:"label"`
		Peers            int    `json:"peers"`
		MaxPeers         int    `json:"maxPeers"`
		PasswordRequired bool   `json:"passwordRequired"`
	}
	items := make([]item, len(sessions))
	for i, s := range sessions {
		items[i] = item{Code: s.Code, Label: s.Label, Peers: s.Peers, MaxPeers: s.MaxPeers, PasswordRequired: s.PasswordRequired}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": items})
}

func (a *App) handleGetSession(w http.ResponseWriter, r *http.Request) {
	project := projectFromCtx(r.Context())
	code := chi.URLParam(r, "code")

	info, err := a.sessionInfo(code, project.ID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "session not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"code":             info.Code,
		"label":            nullableString(info.Label),
		"visibility":       info.Visibility,
		"passwordRequired": info.PasswordRequired,
		"peers":            info.Peers,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
