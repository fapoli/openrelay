package openrelay

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testServer() (*SessionStore, http.Handler) {
	sessions := newTestSessions()
	return sessions, Routes(sessions, testProjects, HTTPOptions{})
}

func do(t *testing.T, h http.Handler, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

var authHeader = map[string]string{"Authorization": "Bearer test-key"}

var testProjects = map[string]*Project{
	"test-key": {ID: "proj", APIKey: "test-key", MaxSessions: 20, MaxPeersPerSession: 4},
}

func TestRoutes_createSession_401(t *testing.T) {
	_, h := testServer()
	w := do(t, h, "POST", "/sessions", map[string]any{}, nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestRoutes_createSession_201(t *testing.T) {
	_, h := testServer()
	w := do(t, h, "POST", "/sessions", map[string]any{}, authHeader)
	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d", w.Code)
	}
	var body map[string]any
	json.NewDecoder(w.Body).Decode(&body)
	if body["code"] == nil || body["sessionToken"] == nil || body["peerToken"] == nil {
		t.Errorf("missing fields in response: %v", body)
	}
}

func TestRoutes_createSession_409_conflict(t *testing.T) {
	_, h := testServer()
	do(t, h, "POST", "/sessions", map[string]any{"code": "DUPDUP"}, authHeader)
	w := do(t, h, "POST", "/sessions", map[string]any{"code": "DUPDUP"}, authHeader)
	if w.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d", w.Code)
	}
}

func TestRoutes_joinSession_200(t *testing.T) {
	_, h := testServer()
	do(t, h, "POST", "/sessions", map[string]any{"code": "JNKAB2"}, authHeader)
	w := do(t, h, "POST", "/sessions/JNKAB2/join", map[string]any{}, authHeader)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var body map[string]any
	json.NewDecoder(w.Body).Decode(&body)
	if body["peerIndex"].(float64) != 1 {
		t.Errorf("expected peerIndex 1, got %v", body["peerIndex"])
	}
}

func TestRoutes_joinSession_404_unknownCode(t *testing.T) {
	_, h := testServer()
	w := do(t, h, "POST", "/sessions/XXXXXX/join", map[string]any{}, authHeader)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestRoutes_joinSession_404_wrongPassword(t *testing.T) {
	_, h := testServer()
	do(t, h, "POST", "/sessions", map[string]any{"code": "WRGPWD", "password": "correct"}, authHeader)
	w := do(t, h, "POST", "/sessions/WRGPWD/join", map[string]any{"password": "wrong"}, authHeader)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestRoutes_listSessions_onlyPublic(t *testing.T) {
	_, h := testServer()
	do(t, h, "POST", "/sessions", map[string]any{"code": "PUBLS3", "visibility": "public"}, authHeader)
	do(t, h, "POST", "/sessions", map[string]any{"code": "PRVS22"}, authHeader)

	w := do(t, h, "GET", "/sessions", nil, authHeader)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var body struct {
		Sessions []map[string]any `json:"sessions"`
	}
	json.NewDecoder(w.Body).Decode(&body)
	if len(body.Sessions) != 1 || body.Sessions[0]["code"] != "PUBLS3" {
		t.Errorf("expected only PUBLS3, got %+v", body.Sessions)
	}
}

func TestRoutes_getSession_200(t *testing.T) {
	_, h := testServer()
	do(t, h, "POST", "/sessions", map[string]any{"code": "LBBY22", "visibility": "public", "label": "Lobby"}, authHeader)
	w := do(t, h, "GET", "/sessions/LBBY22", nil, authHeader)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var body map[string]any
	json.NewDecoder(w.Body).Decode(&body)
	if body["code"] != "LBBY22" || body["label"] != "Lobby" {
		t.Errorf("unexpected body: %v", body)
	}
}

func TestRoutes_getSession_404(t *testing.T) {
	_, h := testServer()
	w := do(t, h, "GET", "/sessions/ZZZZZZ", nil, authHeader)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestRoutes_createSession_429_limitReached(t *testing.T) {
	_, h := testServer() // test project allows 20 sessions
	for i := 0; i < 20; i++ {
		if w := do(t, h, "POST", "/sessions", map[string]any{}, authHeader); w.Code != http.StatusCreated {
			t.Fatalf("create %d: expected 201, got %d", i, w.Code)
		}
	}
	if w := do(t, h, "POST", "/sessions", map[string]any{}, authHeader); w.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", w.Code)
	}
}

func TestClientIP(t *testing.T) {
	req := func(remote string, headers map[string]string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		return r
	}
	cases := []struct {
		name    string
		r       *http.Request
		header  string
		want    string
		wantErr bool
	}{
		{"remote addr", req("192.0.2.1:5555", nil), "", "192.0.2.1", false},
		{"remote addr ipv6", req("[2001:db8::1]:5555", nil), "", "2001:db8::1", false},
		{"header ignored when not trusted", req("192.0.2.1:5555", map[string]string{"X-Real-IP": "198.51.100.7"}), "", "192.0.2.1", false},
		{"trusted header", req("10.0.0.9:5555", map[string]string{"X-Real-IP": "198.51.100.7"}), "X-Real-IP", "198.51.100.7", false},
		{"trusted header last entry", req("10.0.0.9:5555", map[string]string{"X-Forwarded-For": "6.6.6.6, 198.51.100.7"}), "X-Forwarded-For", "198.51.100.7", false},
		{"trusted header missing", req("10.0.0.9:5555", nil), "X-Real-IP", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ip, ok := clientIP(c.r, c.header)
			if ok == c.wantErr {
				t.Fatalf("ok=%v, wantErr=%v", ok, c.wantErr)
			}
			if !c.wantErr && ip.String() != c.want {
				t.Errorf("got %v, want %v", ip, c.want)
			}
		})
	}
}

func TestRoutes_joinSession_recordsClientIP(t *testing.T) {
	sessions, h := testServer()
	do(t, h, "POST", "/sessions", map[string]any{"code": "QPCHK2"}, authHeader)
	w := do(t, h, "POST", "/sessions/QPCHK2/join", map[string]any{}, authHeader)
	var body struct {
		PeerToken string `json:"peerToken"`
	}
	json.NewDecoder(w.Body).Decode(&body)
	// httptest requests come from 192.0.2.1.
	if got := sessions.sessions["QPCHK2"].peers[body.PeerToken].joinIP.String(); got != "192.0.2.1" {
		t.Errorf("expected join IP 192.0.2.1, got %s", got)
	}
}

func TestRoutes_trustedHeaderMissing_400(t *testing.T) {
	h := limitedServer(HTTPOptions{TrustedIPHeader: "X-Real-IP"})
	if w := do(t, h, "POST", "/sessions", map[string]any{}, authHeader); w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// --- rate limiting ---

func limitedServer(opts HTTPOptions) http.Handler {
	return Routes(newTestSessions(), testProjects, opts)
}

func doFrom(t *testing.T, h http.Handler, remote, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	req.RemoteAddr = remote
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestRateLimit_429WithRetryAfter(t *testing.T) {
	h := limitedServer(HTTPOptions{RateLimitPerMin: 2})
	for i := 0; i < 2; i++ {
		if w := doFrom(t, h, "192.0.2.1:1", "GET", "/sessions/ZZZZZZ", authHeader); w.Code != http.StatusNotFound {
			t.Fatalf("request %d: expected 404, got %d", i, w.Code)
		}
	}
	w := doFrom(t, h, "192.0.2.1:1", "GET", "/sessions/ZZZZZZ", authHeader)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header")
	}
	if !strings.Contains(w.Body.String(), `"error"`) {
		t.Errorf("expected JSON error body, got %q", w.Body.String())
	}
}

func TestRateLimit_perIP(t *testing.T) {
	h := limitedServer(HTTPOptions{RateLimitPerMin: 1})
	doFrom(t, h, "192.0.2.1:1", "GET", "/sessions/ZZZZZZ", authHeader)
	if w := doFrom(t, h, "192.0.2.1:2", "GET", "/sessions/ZZZZZZ", authHeader); w.Code != http.StatusTooManyRequests {
		t.Errorf("same IP, other port: expected 429, got %d", w.Code)
	}
	if w := doFrom(t, h, "192.0.2.2:1", "GET", "/sessions/ZZZZZZ", authHeader); w.Code == http.StatusTooManyRequests {
		t.Error("different IP should have its own limit")
	}
}

func TestRateLimit_ipv6BucketedBy64(t *testing.T) {
	h := limitedServer(HTTPOptions{RateLimitPerMin: 1})
	doFrom(t, h, "[2001:db8:1:2::1]:1", "GET", "/sessions/ZZZZZZ", authHeader)
	if w := doFrom(t, h, "[2001:db8:1:2::ffff]:1", "GET", "/sessions/ZZZZZZ", authHeader); w.Code != http.StatusTooManyRequests {
		t.Errorf("same /64: expected 429, got %d", w.Code)
	}
	if w := doFrom(t, h, "[2001:db8:1:3::1]:1", "GET", "/sessions/ZZZZZZ", authHeader); w.Code == http.StatusTooManyRequests {
		t.Error("different /64 should have its own limit")
	}
}

func TestRateLimit_listNotLimited(t *testing.T) {
	h := limitedServer(HTTPOptions{RateLimitPerMin: 1})
	for i := 0; i < 5; i++ {
		if w := doFrom(t, h, "192.0.2.1:1", "GET", "/sessions", authHeader); w.Code != http.StatusOK {
			t.Fatalf("list request %d: got %d", i, w.Code)
		}
	}
}

func TestRateLimit_createJoinLookupShareBudget(t *testing.T) {
	h := limitedServer(HTTPOptions{RateLimitPerMin: 2})
	doFrom(t, h, "192.0.2.1:1", "POST", "/sessions", authHeader)
	doFrom(t, h, "192.0.2.1:1", "GET", "/sessions/ZZZZZZ", authHeader)
	if w := doFrom(t, h, "192.0.2.1:1", "POST", "/sessions/ZZZZZZ/join", authHeader); w.Code != http.StatusTooManyRequests {
		t.Errorf("expected join to share the create/lookup budget, got %d", w.Code)
	}
}

func TestRateLimit_countsUnauthenticated(t *testing.T) {
	h := limitedServer(HTTPOptions{RateLimitPerMin: 1})
	doFrom(t, h, "192.0.2.1:1", "POST", "/sessions/ZZZZZZ/join", nil) // 401, still counted
	if w := doFrom(t, h, "192.0.2.1:1", "POST", "/sessions/ZZZZZZ/join", authHeader); w.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", w.Code)
	}
}

func TestRateLimit_trustedHeaderIsKey(t *testing.T) {
	h := limitedServer(HTTPOptions{RateLimitPerMin: 1, TrustedIPHeader: "X-Real-IP"})
	hdr := func(ip string) map[string]string {
		return map[string]string{"Authorization": "Bearer test-key", "X-Real-IP": ip}
	}
	doFrom(t, h, "10.0.0.9:1", "GET", "/sessions/ZZZZZZ", hdr("198.51.100.1"))
	if w := doFrom(t, h, "10.0.0.9:1", "GET", "/sessions/ZZZZZZ", hdr("198.51.100.2")); w.Code == http.StatusTooManyRequests {
		t.Error("clients behind the same proxy should have separate limits")
	}
	if w := doFrom(t, h, "10.0.0.9:1", "GET", "/sessions/ZZZZZZ", hdr("198.51.100.1")); w.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 for repeated client IP, got %d", w.Code)
	}
	if w := doFrom(t, h, "10.0.0.9:1", "GET", "/sessions/ZZZZZZ", authHeader); w.Code != http.StatusBadRequest {
		t.Errorf("missing trusted header: expected 400, got %d", w.Code)
	}
}

func TestHealth_notRateLimited(t *testing.T) {
	h := limitedServer(HTTPOptions{RateLimitPerMin: 1})
	for i := 0; i < 20; i++ {
		if w := doFrom(t, h, "192.0.2.1:1", "GET", "/health", nil); w.Code != http.StatusOK {
			t.Fatalf("health request %d: got %d", i, w.Code)
		}
	}
}

// --- hardening ---

func TestRoutes_bodyTooLarge_413(t *testing.T) {
	_, h := testServer()
	big := `{"label":"` + strings.Repeat("x", maxBodyBytes) + `"}`
	for _, path := range []string{"/sessions", "/sessions/ZZZZZZ/join"} {
		req := httptest.NewRequest("POST", path, strings.NewReader(big))
		req.Header.Set("Authorization", "Bearer test-key")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("%s: expected 413, got %d", path, w.Code)
		}
	}
}

func TestNewHTTPServer_timeouts(t *testing.T) {
	s := NewHTTPServer(":3123", newTestSessions(), testProjects, HTTPOptions{})
	if s.Addr != ":3123" || s.Handler == nil {
		t.Errorf("expected addr :3123 and a handler, got %q, %v", s.Addr, s.Handler)
	}
	if s.ReadHeaderTimeout == 0 || s.ReadTimeout == 0 || s.WriteTimeout == 0 || s.IdleTimeout == 0 || s.MaxHeaderBytes == 0 {
		t.Errorf("expected all timeouts and header limit set, got %+v", s)
	}
}

func TestUseClientIPAsRemoteAddr(t *testing.T) {
	cases := []struct {
		name    string
		trusted string
		header  string
		want    string
	}{
		{"header used when trusted", "X-Real-IP", "198.51.100.7", "198.51.100.7"},
		{"header ignored when not trusted", "", "198.51.100.7", "10.0.0.9:5555"},
		{"missing header leaves address", "X-Real-IP", "", "10.0.0.9:5555"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got string
			h := useClientIPAsRemoteAddr(c.trusted)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.RemoteAddr
			}))
			req := httptest.NewRequest("GET", "/health", nil)
			req.RemoteAddr = "10.0.0.9:5555"
			if c.header != "" {
				req.Header.Set("X-Real-IP", c.header)
			}
			h.ServeHTTP(httptest.NewRecorder(), req)
			if got != c.want {
				t.Errorf("RemoteAddr = %q, want %q", got, c.want)
			}
		})
	}
}

func TestHealth_okWithoutTrustedHeader(t *testing.T) {
	h := limitedServer(HTTPOptions{TrustedIPHeader: "X-Real-IP"})
	if w := doFrom(t, h, "10.0.0.9:1", "GET", "/health", nil); w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}
