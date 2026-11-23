package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/modpack"
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

		ModrinthProject      *string `json:"modrinthProject"`
		ModrinthToken        *string `json:"modrinthToken"`
		ModrinthStartVersion *string `json:"modrinthStartVersion"`

		AutoPublish *bool `json:"autoPublish"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid json")
		return
	}
	// Unveränderte Felder überspringen: das UI zeigt die wirksamen Werte
	// (auch aus der .env) und schickte sie früher alle mit. Folge: ein
	// .env-Webhook, der die Prüfung nicht besteht (alte discordapp.com-URL,
	// Test-Attrappe), blockierte das Speichern der Dropbox-Daten komplett —
	// und jeder Speichervorgang kopierte .env-Werte in die DB (überstimmte
	// damit z. B. die MSM_DISCORD_WEBHOOKS-Aufteilung).
	current := s.settings.Values()
	changed := func(ptr *string, field string) (string, bool) {
		if ptr == nil {
			return "", false
		}
		v := strings.TrimSpace(*ptr)
		return v, v != current[field]
	}
	// Phase 1: alle Werte prüfen und Schreibvorgänge sammeln — noch nichts
	// speichern. Eine ungültige Eingabe lässt so nichts halb gespeichert zurück.
	var writes []func()
	if v, ok := changed(req.DiscordWebhook, "discordWebhook"); ok {
		if v != "" && !validWebhook(v) {
			httpError(w, http.StatusBadRequest, "Webhook-URL muss mit https://discord.com/api/webhooks/ beginnen")
			return
		}
		writes = append(writes, func() {
			s.settings.Set(settings.KeyDiscordWebhook, v)
			s.audit(r.Context(), "settings.discord", "webhook geändert (Wert nicht protokolliert)")
		})
	}
	if v, ok := changed(req.ModrinthStartVersion, "modrinthStartVersion"); ok {
		if v != "" && !modpack.ValidVersion(v) {
			httpError(w, http.StatusBadRequest, "Startversion muss die Form 1.0.0 haben")
			return
		}
		writes = append(writes, func() {
			s.settings.Set(settings.KeyModrinthStart, v)
			s.audit(r.Context(), "settings.modrinth", "startversion="+v)
		})
	}
	collect := func(ptr *string, field, key, category, name string) {
		if v, ok := changed(ptr, field); ok {
			writes = append(writes, func() {
				s.settings.Set(key, v)
				s.audit(r.Context(), category, name+" geändert (Wert nicht protokolliert)")
			})
		}
	}
	collect(req.DropboxKey, "dropboxKey", settings.KeyDropboxKey, "settings.dropbox", "app-key")
	collect(req.DropboxSecret, "dropboxSecret", settings.KeyDropboxSecret, "settings.dropbox", "app-secret")
	collect(req.DropboxToken, "dropboxToken", settings.KeyDropboxToken, "settings.dropbox", "refresh-token")
	collect(req.ModrinthProject, "modrinthProject", settings.KeyModrinthProject, "settings.modrinth", "projekt")
	collect(req.ModrinthToken, "modrinthToken", settings.KeyModrinthToken, "settings.modrinth", "token")
	if req.AutoPublish != nil && *req.AutoPublish != s.settings.AutoPublish() {
		on := *req.AutoPublish
		writes = append(writes, func() {
			s.settings.SetAutoPublish(on)
			s.audit(r.Context(), "settings.clientpack", fmt.Sprintf("autoversand=%v", on))
		})
	}

	// Phase 2: erst nach vollständiger Prüfung persistieren.
	for _, write := range writes {
		write()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"view":   s.settings.View(),
		"values": s.settings.Values(),
	})
}

// handleModrinthTest prüft Token + Projekt („Verbindung prüfen").
func (s *Server) handleModrinthTest(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	msg, err := s.modpack.Check(ctx)
	if err != nil {
		s.audit(r.Context(), "settings.modrinth.test", "fehlgeschlagen: "+err.Error())
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.audit(r.Context(), "settings.modrinth.test", "ok")
	writeJSON(w, http.StatusOK, map[string]string{"message": msg})
}

// validWebhook accepts Discord webhook URLs incl. the legacy discordapp.com
// domain and the ptb/canary clients — Discord liefert alle weiterhin aus.
func validWebhook(v string) bool {
	for _, host := range []string{"discord.com", "discordapp.com", "ptb.discord.com", "canary.discord.com"} {
		if strings.HasPrefix(v, "https://"+host+"/api/webhooks/") {
			return true
		}
	}
	return false
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
	// bewusst KEINE Discord-Meldung: das Dashboard kennen die Spieler nicht,
	// der Admin sitzt beim Klick ohnehin davor (Audit + Log reichen)
	s.audit(r.Context(), "system.selfupdate", "tag="+req.Tag)
	s.log.Info("selbst-update angestoßen", "tag", req.Tag)
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
	ev := events.Event{
		Type:     events.TypeRoutineFailed, // "*"-Filter erreicht jeden Webhook; Fehltyp egal für den Test
		Severity: events.SevInfo,
		Title:    "🔔 Testnachricht vom MSM-Dashboard",
		Message:  "Wenn du das liest, funktioniert die Discord-Anbindung. Gesendet: " + time.Now().Format("15:04:05"),
		Time:     time.Now(),
	}
	if s.discordTest == nil {
		s.bus.Publish(ev)
		s.audit(r.Context(), "settings.discord.test", "testnachricht gesendet")
		writeJSON(w, http.StatusOK, map[string]string{"message": "Testnachricht verschickt — im Discord-Channel nachsehen."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	if err := s.discordTest(ctx, ev); err != nil {
		s.audit(r.Context(), "settings.discord.test", "fehlgeschlagen: "+err.Error())
		httpError(w, http.StatusBadGateway, "Discord hat die Testnachricht nicht angenommen — "+err.Error())
		return
	}
	s.audit(r.Context(), "settings.discord.test", "testnachricht zugestellt")
	writeJSON(w, http.StatusOK, map[string]string{"message": "Testnachricht zugestellt — im Discord-Channel nachsehen."})
}
