package api

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/settings"
)

func settingsServer(t *testing.T) (*Server, map[string]string) {
	t.Helper()
	db := map[string]string{}
	store := settings.New(func(k string) string { return db[k] }, func(k, v string) { db[k] = v })
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	return New(Deps{Settings: store, Log: log}), db
}

func putSettings(srv *Server, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("PUT", "/api/settings", strings.NewReader(body)))
	return rec
}

// Regression (2026-11-08): das UI schickt alle Felder mit, auch den
// .env-Webhook. Bestand der die Prüfung nicht (alte discordapp.com-URL,
// Test-Attrappe), ließen sich die Dropbox-Daten gar nicht speichern.
func TestSaveSettingsIgnoresUnchangedEnvWebhook(t *testing.T) {
	t.Setenv("MSM_DISCORD_WEBHOOK_URL", "http://webhook-sink:8000/hook")
	srv, db := settingsServer(t)

	rec := putSettings(srv, `{"discordWebhook":"http://webhook-sink:8000/hook","dropboxKey":"k","dropboxSecret":"s","dropboxToken":"r"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if db[settings.KeyDropboxKey] != "k" || db[settings.KeyDropboxSecret] != "s" || db[settings.KeyDropboxToken] != "r" {
		t.Errorf("dropbox nicht gespeichert: %v", db)
	}
	// .env-Wert darf nicht in die DB kopiert werden (überstimmte sonst
	// dauerhaft die .env, inkl. MSM_DISCORD_WEBHOOKS-Aufteilung)
	if _, ok := db[settings.KeyDiscordWebhook]; ok {
		t.Errorf("unveränderter .env-Webhook in DB kopiert: %q", db[settings.KeyDiscordWebhook])
	}
}

func TestSaveSettingsWebhookValidation(t *testing.T) {
	srv, db := settingsServer(t)
	for url, want := range map[string]int{
		"https://discord.com/api/webhooks/1/x":        http.StatusOK,
		"https://discordapp.com/api/webhooks/1/y":     http.StatusOK,
		"https://canary.discord.com/api/webhooks/1/z": http.StatusOK,
		"https://evil.example/api/webhooks/1/x":       http.StatusBadRequest,
		"http://discord.com/api/webhooks/1/x":         http.StatusBadRequest,
	} {
		rec := putSettings(srv, `{"discordWebhook":"`+url+`"}`)
		if rec.Code != want {
			t.Errorf("%s: status = %d, want %d", url, rec.Code, want)
		}
		if want == http.StatusOK && db[settings.KeyDiscordWebhook] != url {
			t.Errorf("%s: nicht gespeichert (%q)", url, db[settings.KeyDiscordWebhook])
		}
	}
}

func TestSaveModrinthSettings(t *testing.T) {
	srv, db := settingsServer(t)
	rec := putSettings(srv, `{"modrinthProject":" mein-pack ","modrinthToken":"mrp_x","modrinthStartVersion":"1.2.0"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if db[settings.KeyModrinthProject] != "mein-pack" || db[settings.KeyModrinthToken] != "mrp_x" || db[settings.KeyModrinthStart] != "1.2.0" {
		t.Errorf("db = %v", db)
	}
	if rec := putSettings(srv, `{"modrinthStartVersion":"v1"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("ungültige Startversion: status = %d", rec.Code)
	}
}
