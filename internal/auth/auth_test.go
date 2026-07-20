package auth

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestHashAndVerify(t *testing.T) {
	hash, err := HashPassword("geheim123")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("unexpected hash format: %s", hash)
	}
	ok, err := VerifyPassword("geheim123", hash)
	if err != nil || !ok {
		t.Errorf("correct password rejected: ok=%v err=%v", ok, err)
	}
	ok, err = VerifyPassword("falsch", hash)
	if err != nil || ok {
		t.Errorf("wrong password accepted: ok=%v err=%v", ok, err)
	}
}

func TestVerifyRejectsGarbage(t *testing.T) {
	if _, err := VerifyPassword("x", "not-a-hash"); err == nil {
		t.Error("expected error for malformed hash")
	}
}

func login(t *testing.T, m *Manager, password string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"password":"`+password+`"}`))
	m.HandleLogin(rec, req)
	return rec
}

func TestLoginFlow(t *testing.T) {
	hash, _ := HashPassword("geheim123")
	m := NewManager(hash, testLogger())

	rec := login(t, m, "falsch")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: status = %d", rec.Code)
	}

	rec = login(t, m, "geheim123")
	if rec.Code != http.StatusOK {
		t.Fatalf("correct password: status = %d, body %s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != sessionCookie {
		t.Fatalf("expected session cookie, got %v", cookies)
	}
	if !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Error("cookie must be HttpOnly + SameSite=Strict")
	}

	// middleware accepts the session
	var reached bool
	h := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	req := httptest.NewRequest("GET", "/api/snapshot", nil)
	req.AddCookie(cookies[0])
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !reached {
		t.Error("valid session was rejected")
	}

	// without cookie: 401
	reached = false
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest("GET", "/api/snapshot", nil))
	if reached || rec2.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated request passed: reached=%v status=%d", reached, rec2.Code)
	}
}

func TestLoginRateLimit(t *testing.T) {
	hash, _ := HashPassword("geheim123")
	m := NewManager(hash, testLogger())
	for i := 0; i < maxLoginTries; i++ {
		login(t, m, "falsch")
	}
	rec := login(t, m, "geheim123") // correct, but window exhausted
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", rec.Code)
	}
}

func TestMiddlewareCrossOriginBlocked(t *testing.T) {
	m := NewManager("", testLogger()) // auth disabled — CSRF check still applies
	h := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest("POST", "/api/rcon", nil)
	req.Host = "msm.local:8080"
	req.Header.Set("Origin", "http://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-origin POST: status = %d, want 403", rec.Code)
	}

	req = httptest.NewRequest("POST", "/api/rcon", nil)
	req.Host = "msm.local:8080"
	req.Header.Set("Origin", "http://msm.local:8080")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusForbidden {
		t.Error("same-origin POST was blocked")
	}
}

func TestOpenPathsWithoutSession(t *testing.T) {
	hash, _ := HashPassword("pw")
	m := NewManager(hash, testLogger())
	h := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	for _, path := range []string{"/api/auth", "/healthz", "/", "/assets/app.js"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("open path %s blocked: %d", path, rec.Code)
		}
	}
}
