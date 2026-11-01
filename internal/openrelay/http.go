package openrelay

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type contextKey int

const projectKey contextKey = iota

func withProject(ctx context.Context, p *Project) context.Context {
	return context.WithValue(ctx, projectKey, p)
}

func projectFromCtx(ctx context.Context) *Project {
	return ctx.Value(projectKey).(*Project)
}

var validCode = regexp.MustCompile(`^[A-HJ-NP-Z2-9]{6}$`)

func Routes(store *SessionStore) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	r.Group(func(r chi.Router) {
		r.Use(auth(store))
		r.Post("/sessions", handleCreateSession(store))
		r.Get("/sessions", handleListSessions(store))
		r.Post("/sessions/{code}/join", handleJoinSession(store))
		r.Get("/sessions/{code}", handleGetSession(store))
	})

	return r
}

func auth(store *SessionStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a := r.Header.Get("Authorization")
			if !strings.HasPrefix(a, "Bearer ") {
				writeErr(w, http.StatusUnauthorized, "missing authorization header")
				return
			}
			project, ok := store.projects[a[7:]]
			if !ok {
				writeErr(w, http.StatusUnauthorized, "invalid API key")
				return
			}
			r = r.WithContext(withProject(r.Context(), project))
			next.ServeHTTP(w, r)
		})
	}
}

func handleCreateSession(store *SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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

		if project.MaxSessions > 0 && store.activeSessionCount(project.ID) >= project.MaxSessions {
			writeErr(w, http.StatusTooManyRequests, "max sessions reached for this project")
			return
		}

		result, err := store.createSession(project.ID, project.MaxPeersPerSession, body.Code, body.Password, label, vis)
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
}

func handleJoinSession(store *SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		project := projectFromCtx(r.Context())
		code := chi.URLParam(r, "code")

		var body struct {
			Password string `json:"password"`
		}
		json.NewDecoder(r.Body).Decode(&body)

		result, err := store.joinSession(code, body.Password, project.ID)
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
}

func handleListSessions(store *SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		project := projectFromCtx(r.Context())
		sessions := store.publicSessions(project.ID)

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
}

func handleGetSession(store *SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		project := projectFromCtx(r.Context())
		code := chi.URLParam(r, "code")

		info, err := store.sessionInfo(code, project.ID)
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
