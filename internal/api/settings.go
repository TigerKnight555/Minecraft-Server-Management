package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/settings"
)

// handleGetSettings returns status AND plaintext values — bewusste
// Nutzer-Entscheidung: konfigurierte Werte sind im Dashboard immer sichtbar
// (LAN-only, hinter Login). Der Status liefert weiterhin die Quelle.
func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"view":   s.settings.View(),
		"values": s.settings.Values(),
	})
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
	writeJSON(w, http.StatusOK, map[string]any{
		"view":   s.settings.View(),
		"values": s.settings.Values(),
	})
}

// handleSelfUpdateStatus returns current/latest MSM version; ?check=1
// erzwingt einen frischen GitHub-Abruf (sonst letzter Stand).
func (s *Server) handleSelfUpdateStatus(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("check") == "1" {
		st, err := s.selfupdate.Check(r.Context())
		if err != nil {
			writeJSON(w, http.StatusOK, st) // Fehler steht im Status, UI zeigt ihn
			return
		}
		writeJSON(w, http.StatusOK, st)
		return
	}
	writeJSON(w, http.StatusOK, s.selfupdate.Status())
}

// handleSelfUpdateApply signals the host helper to update MSM to the tag.
func (s *Server) handleSelfUpdateApply(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tag string `json:"tag"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := s.selfupdate.Apply(req.Tag); err != nil {
		httpError(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r.Context(), "system.selfupdate", "tag="+req.Tag)
	s.bus.Publish(events.Event{
		Type: events.TypeMSMUpdate, Severity: events.SevInfo,
		Title:   "🔄 Dashboard-Update läuft",
		Message: "MSM wird auf " + req.Tag + " aktualisiert und ist gleich kurz nicht erreichbar. Der Minecraft-Server läuft normal weiter.",
	})
	writeJSON(w, http.StatusAccepted, map[string]string{
		"message": "Update auf " + req.Tag + " angestoßen — das Dashboard ist während des Neubaus (~1–2 min) kurz weg und meldet sich mit der neuen Version zurück.",
	})
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
