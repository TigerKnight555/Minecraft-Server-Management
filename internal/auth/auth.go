// Package auth implements the single-admin login: Argon2id password
// verification, in-memory sessions with secure cookies, a login rate limit
// and CSRF protection via strict origin checks (the SPA sends same-origin
// requests only; cookies are SameSite=Strict).
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

const (
	sessionCookie   = "msm_session"
	sessionLifetime = 12 * time.Hour
	maxLoginTries   = 5               // per window
	loginWindow     = 15 * time.Minute
)

// VerifyPassword checks a password against an encoded Argon2id hash in the
// standard format: $argon2id$v=19$m=65536,t=3,p=2$<salt-b64>$<hash-b64>
func VerifyPassword(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, fmt.Errorf("unsupported hash format")
	}
	var mem uint32
	var iter uint32
	var par uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &iter, &par); err != nil {
		return false, fmt.Errorf("bad hash params: %w", err)
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("bad salt: %w", err)
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("bad hash: %w", err)
	}
	got := argon2.IDKey([]byte(password), salt, iter, mem, par, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// HashPassword produces an encoded Argon2id hash (used by the -hash-password
// CLI helper so the admin can generate MSM_ADMIN_PASSWORD_HASH).
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	const (
		mem  uint32 = 64 * 1024
		iter uint32 = 3
		par  uint8  = 2
	)
	hash := argon2.IDKey([]byte(password), salt, iter, mem, par, 32)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		mem, iter, par,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash)), nil
}

type session struct {
	expires time.Time
}

// Manager holds sessions and guards login attempts.
type Manager struct {
	passwordHash string
	log          *slog.Logger

	mu       sync.Mutex
	sessions map[string]session
	tries    []time.Time // login attempts inside the window (all IPs — LAN-only setup)
}

func NewManager(passwordHash string, log *slog.Logger) *Manager {
	return &Manager{
		passwordHash: passwordHash,
		log:          log,
		sessions:     make(map[string]session),
	}
}

// Enabled reports whether login is configured. Without a hash MSM runs
// open (mock/dev mode) and the middleware passes everything through.
func (m *Manager) Enabled() bool { return m.passwordHash != "" }

func (m *Manager) allowAttempt() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := time.Now().Add(-loginWindow)
	kept := m.tries[:0]
	for _, t := range m.tries {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	m.tries = kept
	if len(m.tries) >= maxLoginTries {
		return false
	}
	m.tries = append(m.tries, time.Now())
	return true
}

func (m *Manager) createSession() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	m.mu.Lock()
	m.sessions[token] = session{expires: time.Now().Add(sessionLifetime)}
	// opportunistic cleanup
	for t, s := range m.sessions {
		if s.expires.Before(time.Now()) {
			delete(m.sessions, t)
		}
	}
	m.mu.Unlock()
	return token, nil
}

func (m *Manager) validSession(token string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[token]
	if !ok || s.expires.Before(time.Now()) {
		delete(m.sessions, token)
		return false
	}
	return true
}

func (m *Manager) dropSession(token string) {
	m.mu.Lock()
	delete(m.sessions, token)
	m.mu.Unlock()
}

// HandleLogin is POST /api/login with {"password":"..."}.
func (m *Manager) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if !m.Enabled() {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	if !m.allowAttempt() {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "zu viele Versuche, später erneut"})
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	ok, err := VerifyPassword(body.Password, m.passwordHash)
	if err != nil {
		m.log.Error("password verify failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "verify failed"})
		return
	}
	if !ok {
		m.log.Warn("failed login attempt", "remote", r.RemoteAddr)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "falsches Passwort"})
		return
	}
	token, err := m.createSession()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "session failed"})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   r.TLS != nil,
		MaxAge:   int(sessionLifetime.Seconds()),
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// HandleLogout is POST /api/logout.
func (m *Manager) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		m.dropSession(c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// HandleStatus is GET /api/auth — tells the SPA whether login is needed.
func (m *Manager) HandleStatus(w http.ResponseWriter, r *http.Request) {
	authed := !m.Enabled()
	if c, err := r.Cookie(sessionCookie); err == nil && m.validSession(c.Value) {
		authed = true
	}
	writeJSON(w, http.StatusOK, map[string]bool{"required": m.Enabled(), "authenticated": authed})
}

// Middleware protects everything except the login/status endpoints and the
// static frontend. Mutating requests additionally need a same-origin check
// (CSRF defence in depth on top of SameSite=Strict).
func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		open := path == "/api/login" || path == "/api/auth" || path == "/healthz" ||
			!strings.HasPrefix(path, "/api/")
		if !open && m.Enabled() {
			c, err := r.Cookie(sessionCookie)
			if err != nil || !m.validSession(c.Value) {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "nicht angemeldet"})
				return
			}
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if !sameOrigin(r) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "cross-origin verboten"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // non-browser client (curl, tests)
	}
	return strings.TrimPrefix(strings.TrimPrefix(origin, "http://"), "https://") == r.Host
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
