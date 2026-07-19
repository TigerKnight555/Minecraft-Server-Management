package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/settings"
)

// handleGetSettings returns the redacted overview — Werte verlassen den
// Server nie im Klartext, nur gesetzt/Quelle/Endung.
func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.settings.View())
}

// handleSaveSettings persists changed fields. Konvention: Feld fehlt (null)
// = unverändert; leerer String = löschen (→ .env-Fallback greift wieder).
func (s *Server) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DiscordWebhook *string `json:"discordWebhook"`
		DropboxKey     *string `json:"dropboxKey"`
		DropboxSecret  *string `json:"dropboxSecret"`
		DropboxToken   *string `json:"dropboxToken"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.DiscordWebhook != nil {
		v := strings.TrimSpace(*req.DiscordWebhook)
		if v != "" && !strings.HasPrefix(v, "https://discord.com/api/webhooks/") {
			httpError(w, http.StatusBadRequest, "Webhook-URL muss mit https://discord.com/api/webhooks/ beginnen")
			return
		}
		s.settings.Set(settings.KeyDiscordWebhook, v)
		s.audit(r.Context(), "settings.discord", "webhook geändert (Wert nicht protokolliert)")
	}
	set := func(ptr *string, key, name string) {
		if ptr != nil {
			s.settings.Set(key, *ptr)
			s.audit(r.Context(), "settings.dropbox", name+" geändert (Wert nicht protokolliert)")
		}
	}
	set(req.DropboxKey, settings.KeyDropboxKey, "app-key")
	set(req.DropboxSecret, settings.KeyDropboxSecret, "app-secret")
	set(req.DropboxToken, settings.KeyDropboxToken, "refresh-token")
	writeJSON(w, http.StatusOK, s.settings.View())
}

// handleTestDiscord sends a test embed to the currently effective webhook.
func (s *Server) handleTestDiscord(w http.ResponseWriter, r *http.Request) {
	if len(s.settings.DiscordHooks()) == 0 {
		httpError(w, http.StatusConflict, "kein Webhook konfiguriert")
		return
	}
	s.bus.Publish(events.Event{
		Type:     events.TypeRoutineFailed, // "*"-Filter erreicht jeden Webhook; Fehltyp egal für den Test
		Severity: events.SevInfo,
		Title:    "🔔 Testnachricht vom MSM-Dashboard",
		Message:  "Wenn du das liest, funktioniert die Discord-Anbindung. Gesendet: " + time.Now().Format("15:04:05"),
	})
	s.audit(r.Context(), "settings.discord.test", "testnachricht gesendet")
	writeJSON(w, http.StatusOK, map[string]string{"message": "Testnachricht verschickt — im Discord-Channel nachsehen."})
}
