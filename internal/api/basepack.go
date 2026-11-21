package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/basepack"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mods"
)

func (s *Server) basepackBlocked(w http.ResponseWriter) bool {
	if s.upgradeActive != nil && s.upgradeActive() {
		httpError(w, http.StatusConflict, "Ein Minecraft-Update läuft gerade — Basis-Modpack danach ändern")
		return true
	}
	return false
}

// handleBasepackStatus: checkError gesetzt = Update-Prüfung fehlgeschlagen
// (dann ist "available" unbekannt, nicht "kein Update").
func (s *Server) handleBasepackStatus(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"state": nil, "available": nil}
	if st, ok := s.basepack.State(); ok {
		out["state"] = st
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		v, err := s.basepack.Available(ctx, s.mcVersion())
		if err != nil {
			s.log.Warn("basepack update check failed", "err", err)
			out["checkError"] = "Update-Prüfung bei Modrinth fehlgeschlagen"
		} else if v != nil {
			out["available"] = map[string]string{"versionId": v.ID, "versionNumber": v.VersionNumber}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleBasepackSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < 2 || len(q) > 100 {
		httpError(w, http.StatusBadRequest, "Suchbegriff zu kurz")
		return
	}
	mc := s.mcVersion()
	hits, err := s.basepack.Search(r.Context(), q, mc)
	if err != nil {
		s.log.Error("basepack search failed", "err", err)
		httpError(w, http.StatusBadGateway, "Modrinth-Suche fehlgeschlagen")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hits": hits, "mcVersion": mc})
}

func (s *Server) handleBasepackPreview(w http.ResponseWriter, r *http.Request) {
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	if project == "" || len(project) > 64 {
		httpError(w, http.StatusBadRequest, "project fehlt")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	pv, err := s.basepack.Preview(ctx, project, s.mcVersion())
	if err != nil {
		s.log.Error("basepack preview failed", "project", project, "err", err)
		httpError(w, http.StatusBadGateway, "Vorschau fehlgeschlagen: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, pv)
}

func (s *Server) handleBasepackApply(w http.ResponseWriter, r *http.Request) {
	if s.basepackBlocked(w) {
		return
	}
	var req struct {
		ProjectID string          `json:"projectId"`
		VersionID string          `json:"versionId"`
		Configs   map[string]bool `json:"configs"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil || req.ProjectID == "" || len(req.ProjectID) > 64 || len(req.VersionID) > 64 {
		httpError(w, http.StatusBadRequest, "projectId fehlt")
		return
	}
	// bewusst nicht an r.Context() gebunden: ein Abbruch mitten im Anwenden
	// ließe Pack und Zustand halb geschrieben zurück
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	summary, err := s.basepack.Apply(ctx, req.ProjectID, req.VersionID, s.mcVersion(), req.Configs)
	if err != nil {
		s.log.Error("basepack apply failed", "project", req.ProjectID, "err", err)
		s.audit(r.Context(), "basepack.apply.failed", req.ProjectID)
		httpError(w, http.StatusBadGateway, "Anwenden fehlgeschlagen: "+err.Error())
		return
	}
	s.audit(r.Context(), "basepack.apply", summary)
	writeJSON(w, http.StatusOK, map[string]string{"message": summary + " — wird beim nächsten Neustart an die Spieler verteilt."})
}

func (s *Server) handleBasepackToggle(w http.ResponseWriter, r *http.Request) {
	if s.basepackBlocked(w) {
		return
	}
	var req struct {
		Key string `json:"key"`
		On  bool   `json:"on"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.Key == "" {
		httpError(w, http.StatusBadRequest, "key fehlt")
		return
	}
	if _, ok := s.basepack.State(); !ok {
		httpError(w, http.StatusBadRequest, "kein Basis-Modpack eingerichtet")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := s.basepack.Toggle(ctx, req.Key, req.On); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r.Context(), "basepack.toggle", req.Key)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleBasepackConfigs(w http.ResponseWriter, r *http.Request) {
	if s.basepackBlocked(w) {
		return
	}
	var req struct {
		Configs map[string]bool `json:"configs"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid json")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := s.basepack.SetConfigs(ctx, req.Configs); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r.Context(), "basepack.configs", "auswahl geändert")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleBasepackRemove(w http.ResponseWriter, r *http.Request) {
	if s.basepackBlocked(w) {
		return
	}
	if err := s.basepack.Remove(r.Context()); err != nil {
		s.log.Error("basepack remove failed", "err", err)
		httpError(w, http.StatusBadGateway, "Entfernen fehlgeschlagen: "+err.Error())
		return
	}
	s.audit(r.Context(), "basepack.remove", "")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// decorateClient marks pack files and appends disabled pack mods.
func (s *Server) decorateClient(entries []mods.Entry) []mods.Entry {
	st, ok := s.basepack.State()
	if !ok {
		return entries
	}
	for i := range entries {
		if s.basepack.Pinned(entries[i].Filename) {
			entries[i].Source = "basepack"
		}
	}
	for _, f := range st.Files {
		if st.IsDisabled(basepack.Key(f)) {
			entries = append(entries, mods.Entry{Profile: mods.ProfileClient, Category: f.Category, Filename: f.Filename,
				Name: f.Name, ProjectID: f.ProjectID, Version: f.Version, Managed: f.ProjectID != "", Source: "basepack", Disabled: true})
		}
	}
	return entries
}
