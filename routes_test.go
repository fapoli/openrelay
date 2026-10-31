package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testServer() (*App, http.Handler) {
	app := newTestApp()
	return app, app.routes()
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
