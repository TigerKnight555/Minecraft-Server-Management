// Package settings makes the integration credentials (Discord-Webhook,
// Dropbox) über das Dashboard konfigurierbar. Werte liegen in SQLite
// (app_state) und gewinnen gegen die .env — die bleibt als Fallback für
// bestehende Setups. Änderungen wirken sofort: Notifier und Dropbox-Client
// lesen über Provider-Funktionen bei jeder Nutzung den aktuellen Stand.
package settings

import (
	"os"
	"strings"
	"sync"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/dropbox"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/notify"
)

// Keys im app_state (Werte sind Geheimnisse — nie im Klartext ausliefern!)
const (
	KeyDiscordWebhook = "settings.discord.webhook"
	KeyDropboxKey     = "settings.dropbox.key"
	KeyDropboxSecret  = "settings.dropbox.secret"
	KeyDropboxToken   = "settings.dropbox.token"
)

// Store liest/schreibt Einstellungen. get/set kommen aus SQLite (app_state);
// ohne Persistenz (Tests/Mock) genügt eine Map über die Closures.
type Store struct {
	mu  sync.Mutex
	get func(key string) string
	set func(key, value string)
}

func New(get func(key string) string, set func(key, value string)) *Store {
	return &Store{get: get, set: set}
}

func (s *Store) value(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.TrimSpace(s.get(key))
}

// Set persists one setting ("" löscht den Wert → .env-Fallback greift wieder).
func (s *Store) Set(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.set(key, strings.TrimSpace(value))
}

// DiscordHooks returns the effective webhook list: DB-Wert (eine URL, alle
// Events) gewinnt; sonst .env (MSM_DISCORD_WEBHOOKS-JSON vor Einzel-URL).
func (s *Store) DiscordHooks() []notify.Webhook {
	if url := s.value(KeyDiscordWebhook); url != "" {
		return []notify.Webhook{{Name: "dashboard", URL: url, Events: []string{"*"}}}
	}
	hooks, err := notify.ParseWebhooks(os.Getenv("MSM_DISCORD_WEBHOOKS"), os.Getenv("MSM_DISCORD_WEBHOOK_URL"))
	if err != nil {
		return nil // kaputte .env-Konfiguration wurde beim Start schon gemeldet
	}
	return hooks
}

// DropboxConfig returns the effective credentials (je Feld: DB vor .env).
func (s *Store) DropboxConfig() dropbox.Config {
	pick := func(key, env string) string {
		if v := s.value(key); v != "" {
			return v
		}
		return os.Getenv(env)
	}
	return dropbox.Config{
		AppKey:       pick(KeyDropboxKey, "MSM_DROPBOX_APP_KEY"),
		AppSecret:    pick(KeyDropboxSecret, "MSM_DROPBOX_APP_SECRET"),
		RefreshToken: pick(KeyDropboxToken, "MSM_DROPBOX_REFRESH_TOKEN"),
	}
}

// Masked liefert die UI-Ansicht: nie den Wert, nur gesetzt/Quelle/Endung.
type Masked struct {
	Set    bool   `json:"set"`
	Source string `json:"source,omitempty"` // "dashboard" | "env"
	Hint   string `json:"hint,omitempty"`   // letzte 4 Zeichen
}

func (s *Store) masked(key, env string) Masked {
	if v := s.value(key); v != "" {
		return Masked{Set: true, Source: "dashboard", Hint: tail(v)}
	}
	if v := strings.TrimSpace(os.Getenv(env)); v != "" {
		return Masked{Set: true, Source: "env", Hint: tail(v)}
	}
	return Masked{}
}

// View is the redacted settings overview for the dashboard.
type View struct {
	DiscordWebhook Masked `json:"discordWebhook"`
	DropboxKey     Masked `json:"dropboxKey"`
	DropboxSecret  Masked `json:"dropboxSecret"`
	DropboxToken   Masked `json:"dropboxToken"`
	DropboxReady   bool   `json:"dropboxReady"`
}

func (s *Store) View() View {
	return View{
		DiscordWebhook: s.masked(KeyDiscordWebhook, "MSM_DISCORD_WEBHOOK_URL"),
		DropboxKey:     s.masked(KeyDropboxKey, "MSM_DROPBOX_APP_KEY"),
		DropboxSecret:  s.masked(KeyDropboxSecret, "MSM_DROPBOX_APP_SECRET"),
		DropboxToken:   s.masked(KeyDropboxToken, "MSM_DROPBOX_REFRESH_TOKEN"),
		DropboxReady:   s.DropboxConfig().Complete(),
	}
}

// Reveal returns the effective plaintext value of one field (DB vor .env) —
// nur für den bewussten „Anzeigen"-Klick im Dashboard; jeder Abruf wird vom
// API-Handler auditiert. Unbekanntes Feld -> "".
func (s *Store) Reveal(field string) string {
	switch field {
	case "discordWebhook":
		if v := s.value(KeyDiscordWebhook); v != "" {
			return v
		}
		return strings.TrimSpace(os.Getenv("MSM_DISCORD_WEBHOOK_URL"))
	case "dropboxKey":
		return s.DropboxConfig().AppKey
	case "dropboxSecret":
		return s.DropboxConfig().AppSecret
	case "dropboxToken":
		return s.DropboxConfig().RefreshToken
	}
	return ""
}

func tail(v string) string {
	if len(v) <= 4 {
		return "····"
	}
	return "…" + v[len(v)-4:]
}
