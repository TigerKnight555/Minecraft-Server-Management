package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/dropbox"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mods"
)

// handleModsList returns the cached entries of a profile (runs a check first
// if the cache is empty).
func (s *Server) handleModsList(w http.ResponseWriter, r *http.Request) {
	profile := r.URL.Query().Get("profile")
	if profile == "" {
		profile = "server"
	}
	entries := s.modmgr.Entries(profile)
	if entries == nil {
		entries = []mods.Entry{}
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) mcVersion() string {
	if v := s.collector.MCVersion(); v != "" {
		return v
	}
	return s.fallbackMCVersion
}

// handleModsCheck runs a Modrinth update check for one profile.
func (s *Server) handleModsCheck(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Profile string `json:"profile"`
	}
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req)
	if req.Profile == "" {
		req.Profile = "server"
	}
	version := s.mcVersion()
	if version == "" {
		// lieber ehrlich ablehnen als mit leerer Version "alles aktuell" melden
		httpError(w, http.StatusConflict, "Minecraft-Version unbekannt (Server startet noch?) — Check in 1–2 Minuten wiederholen")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	entries, err := s.modmgr.CheckUpdates(ctx, req.Profile, version)
	if err != nil {
		s.log.Error("mod check failed", "profile", req.Profile, "err", err)
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.audit(r.Context(), "mods.check", req.Profile)
	writeJSON(w, http.StatusOK, entries)
}

// handleModsStage downloads selected updates into staging.
func (s *Server) handleModsStage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Profile   string   `json:"profile"`
		Filenames []string `json:"filenames"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Profile == "" {
		req.Profile = "server"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	n, err := s.modmgr.Stage(ctx, req.Profile, req.Filenames)
	if err != nil {
		s.audit(r.Context(), "mods.stage.failed", fmt.Sprintf("profile=%s err=%s", req.Profile, err))
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.audit(r.Context(), "mods.stage", fmt.Sprintf("profile=%s count=%d", req.Profile, n))
	writeJSON(w, http.StatusOK, map[string]int{"staged": n})
}

// handleModsApply swaps staged files in and — for the server profile —
// restarts the Minecraft container so the new mods load.
func (s *Server) handleModsApply(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Profile string `json:"profile"`
		Restart bool   `json:"restart"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Profile == "" {
		req.Profile = "server"
	}
	label, n, err := s.modmgr.ApplyStaged(req.Profile)
	if err != nil {
		s.audit(r.Context(), "mods.apply.failed", fmt.Sprintf("profile=%s err=%s", req.Profile, err))
		httpError(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r.Context(), "mods.apply", fmt.Sprintf("profile=%s count=%d backup=%s", req.Profile, n, label))
	s.bus.Publish(events.Event{
		Type: events.TypeModsApplied, Severity: events.SevSuccess,
		Title: "🔧 Server-Mods aktualisiert",
		Fields: []events.Field{
			{Name: "Profil", Value: req.Profile},
			{Name: "Anzahl", Value: fmt.Sprint(n)},
			{Name: "Backup", Value: label},
		},
	})

	restarted := false
	if req.Restart && req.Profile == "server" && s.controller != nil {
		for _, ct := range s.collector.Containers() {
			if s.managed[ct.Name] && ct.Name == s.mcContainer {
				if err := s.controller.RestartContainer(r.Context(), ct.ID); err != nil {
					s.audit(r.Context(), "container.restart.failed", "container="+ct.Name+" err="+err.Error())
					httpError(w, http.StatusBadGateway, "Mods eingesetzt, aber Neustart fehlgeschlagen: "+err.Error())
					return
				}
				s.audit(r.Context(), "container.restart", "container="+ct.Name+" (mod apply)")
				restarted = true
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"applied": n, "backup": label, "restarted": restarted})
}

// handleModsRollback restores the newest backup set.
func (s *Server) handleModsRollback(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Profile string `json:"profile"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Profile == "" {
		req.Profile = "server"
	}
	n, err := s.modmgr.Rollback(req.Profile)
	if err != nil {
		s.audit(r.Context(), "mods.rollback.failed", fmt.Sprintf("profile=%s err=%s", req.Profile, err))
		httpError(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r.Context(), "mods.rollback", fmt.Sprintf("profile=%s restored=%d", req.Profile, n))
	s.bus.Publish(events.Event{
		Type: events.TypeModsRollback, Severity: events.SevWarn,
		Title: "↩️ Mod-Update zurückgenommen",
		Fields: []events.Field{
			{Name: "Profil", Value: req.Profile},
			{Name: "Wiederhergestellt", Value: fmt.Sprint(n)},
		},
	})
	writeJSON(w, http.StatusOK, map[string]int{"restored": n})
}

// handlePublishClientPack zips the client profile, uploads it to Dropbox
// and posts the shared link to Discord (Phase 4.8). Läuft asynchron — der
// Upload kann bei großen Paketen Minuten dauern; Ergebnis kommt als Event.
func (s *Server) handlePublishClientPack(w http.ResponseWriter, r *http.Request) {
	var clientDirs map[string]string
	for _, p := range s.modmgr.Profiles() {
		if p.Name == "client" {
			clientDirs = p.Dirs
		}
	}
	if clientDirs == nil {
		httpError(w, http.StatusServiceUnavailable, "kein Client-Profil konfiguriert")
		return
	}
	s.audit(r.Context(), "mods.publish", "client-pack upload gestartet")
	go s.publishClientPack(clientDirs)
	writeJSON(w, http.StatusAccepted, map[string]string{
		"message": "Upload gestartet — das Ergebnis kommt als Discord-Meldung (und ins Audit-Log).",
	})
}

func (s *Server) publishClientPack(dirs map[string]string) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	name := "/MSM/mc-clientpack-" + time.Now().Format("2006-01-02") + ".zip"

	pr, pw := io.Pipe()
	files := 0
	go func() {
		n, err := dropbox.ZipDirs(pw, dirs)
		files = n
		pw.CloseWithError(err)
	}()
	fail := func(err error) {
		s.log.Error("client-pack publish failed", "err", err)
		s.audit(ctx, "mods.publish.failed", err.Error())
		s.bus.Publish(events.Event{
			Type: events.TypeClientPack, Severity: events.SevError,
			Title: "⚠️ Mod-Paket-Upload fehlgeschlagen", Message: "Info für den Admin — Details unten.",
			Fields: []events.Field{{Name: "Details", Value: err.Error()}},
		})
	}
	if err := s.dropbox.Upload(ctx, name, pr); err != nil {
		fail(err)
		return
	}
	link, err := s.dropbox.ShareLink(ctx, name)
	if err != nil {
		fail(err)
		return
	}
	s.audit(ctx, "mods.publish.ok", name)
	s.bus.Publish(events.Event{
		Type: events.TypeClientPack, Severity: events.SevSuccess,
		Title:   "📦 Neues Mod-Paket zum Download!",
		Message: "Download: " + link + "\nZIP in den bestehenden .minecraft-Ordner entpacken — Karten, Wegpunkte und Configs bleiben erhalten.",
		Fields: []events.Field{
			{Name: "Dateien", Value: fmt.Sprint(files)},
			{Name: "Stand", Value: time.Now().Format("02.01.2006")},
		},
	})
}

// handleVersionWatch returns the last readiness check.
func (s *Server) handleVersionWatch(w http.ResponseWriter, r *http.Request) {
	last := s.watcher.Last()
	if last == nil {
		writeJSON(w, http.StatusOK, map[string]any{"checked": nil})
		return
	}
	writeJSON(w, http.StatusOK, last)
}

// handleVersionWatchCheck triggers a readiness check now.
func (s *Server) handleVersionWatchCheck(w http.ResponseWriter, r *http.Request) {
	version := s.mcVersion()
	if version == "" {
		httpError(w, http.StatusConflict, "Minecraft-Version unbekannt (Server startet noch?) — Check in 1–2 Minuten wiederholen")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	status, err := s.watcher.Check(ctx, version)
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.watcher.SetLast(status)
	s.audit(r.Context(), "version-watch.check", status.LatestVersion)
	writeJSON(w, http.StatusOK, status)
}
