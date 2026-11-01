package openrelay

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
)

const maxBodyBytes = 4 << 10

type contextKey int

const (
	projectKey contextKey = iota
	clientIPKey
)

func projectFromCtx(ctx context.Context) *Project {
	return ctx.Value(projectKey).(*Project)
}

func clientIPFromCtx(ctx context.Context) netip.Addr {
	return ctx.Value(clientIPKey).(netip.Addr)
}

var validCode = regexp.MustCompile(`^[A-HJ-NP-Z2-9]{6}$`)

// HTTPOptions are the settings of the HTTP signaling API.
type HTTPOptions struct {
	TrustedIPHeader string
	RateLimitPerMin int
}

// NewHTTPServer returns the HTTP signaling server listening on addr.
func NewHTTPServer(addr string, sessions *SessionStore, projects map[string]*Project, opts HTTPOptions) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           Routes(sessions, projects, opts),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    8 << 10,
	}
}

// Routes returns the handler for the HTTP signaling API.
func Routes(sessions *SessionStore, projects map[string]*Project, opts HTTPOptions) http.Handler {
	r := chi.NewRouter()
	r.Use(useClientIPAsRemoteAddr(opts.TrustedIPHeader))
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(limitBody)

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	r.Group(func(r chi.Router) {
		r.Use(resolveClientIP(opts.TrustedIPHeader))
		limit := rateLimit(opts.RateLimitPerMin)
		r.With(limit, requireAPIKey(projects)).Post("/sessions", handleCreateSession(sessions))
		r.With(requireAPIKey(projects)).Get("/sessions", handleListSessions(sessions))
		r.With(limit, requireAPIKey(projects)).Post("/sessions/{code}/join", handleJoinSession(sessions))
		r.With(limit, requireAPIKey(projects)).Get("/sessions/{code}", handleGetSession(sessions))
	})

	return r
}

func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		next.ServeHTTP(w, r)
	})
}

// useClientIPAsRemoteAddr sets r.RemoteAddr to the IP in trustedHeader when one is configured and present.
func useClientIPAsRemoteAddr(trustedHeader string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if trustedHeader == "" {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if ip, ok := clientIP(r, trustedHeader); ok {
				r = r.WithContext(r.Context())
				r.RemoteAddr = ip.String()
			}
			next.ServeHTTP(w, r)
		})
	}
}

func resolveClientIP(trustedHeader string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip, ok := clientIP(r, trustedHeader)
			if !ok {
				writeErr(w, http.StatusBadRequest, "cannot determine client IP")
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), clientIPKey, ip)))
		})
	}
}

// clientIP returns the TCP peer address of r, or the last entry of trustedHeader when it is set.
func clientIP(r *http.Request, trustedHeader string) (netip.Addr, bool) {
	if trustedHeader != "" {
		v := r.Header.Get(trustedHeader)
		if i := strings.LastIndex(v, ","); i >= 0 {
			v = v[i+1:]
		}
		ip, err := netip.ParseAddr(strings.TrimSpace(v))
		return ip.Unmap(), err == nil
	}
	addrPort, err := netip.ParseAddrPort(r.RemoteAddr)
	return addrPort.Addr().Unmap(), err == nil
}

func rateLimit(perMinute int) func(http.Handler) http.Handler {
	if perMinute <= 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	key := func(r *http.Request) (string, error) {
		return httprate.CanonicalizeIP(clientIPFromCtx(r.Context()).String()), nil
	}
	return httprate.LimitBy(perMinute, time.Minute, key,
		httprate.WithLimitHandler(func(w http.ResponseWriter, r *http.Request) {
			writeErr(w, http.StatusTooManyRequests, "rate limit exceeded")
		}),
	)
}

func requireAPIKey(projects map[string]*Project) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiKey, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok {
				writeErr(w, http.StatusUnauthorized, "missing authorization header")
				return
			}
			project, ok := projects[apiKey]
			if !ok {
				writeErr(w, http.StatusUnauthorized, "invalid API key")
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), projectKey, project)))
		})
	}
}

func handleCreateSession(sessions *SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Code       string `json:"code"`
			Password   string `json:"password"`
			Label      string `json:"label"`
			Visibility string `json:"visibility"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			if isBodyTooLarge(err) {
				writeErr(w, http.StatusRequestEntityTooLarge, "request body too large")
				return
			}
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

		result, err := sessions.createSession(projectFromCtx(r.Context()), newSession{
			Code:       body.Code,
			Password:   body.Password,
			Label:      label,
			Visibility: vis,
			IP:         clientIPFromCtx(r.Context()),
		})
		switch {
		case errors.Is(err, errConflict):
			writeErr(w, http.StatusConflict, err.Error())
		case errors.Is(err, errLimitReached):
			writeErr(w, http.StatusTooManyRequests, err.Error())
		case err != nil:
			writeErr(w, http.StatusInternalServerError, "failed to create session")
		default:
			writeJSON(w, http.StatusCreated, result)
		}
	}
}

func handleJoinSession(sessions *SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); isBodyTooLarge(err) {
			writeErr(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}

		result, err := sessions.joinSession(chi.URLParam(r, "code"), body.Password, projectFromCtx(r.Context()).ID, clientIPFromCtx(r.Context()))
		if err != nil {
			writeErr(w, http.StatusNotFound, "invalid code or password")
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func handleListSessions(sessions *SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions.publicSessions(projectFromCtx(r.Context()).ID)})
	}
}

func handleGetSession(sessions *SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		info, err := sessions.sessionInfo(chi.URLParam(r, "code"), projectFromCtx(r.Context()).ID)
		if err != nil {
			writeErr(w, http.StatusNotFound, "session not found")
			return
		}
		writeJSON(w, http.StatusOK, info)
	}
}

func isBodyTooLarge(err error) bool {
	var e *http.MaxBytesError
	return errors.As(err, &e)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
