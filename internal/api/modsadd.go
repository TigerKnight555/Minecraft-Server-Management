package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mods"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/storage"
)

// needVersion writes 409 when the MC version is unknown (Server startet).
func (s *Server) needVersion(w http.ResponseWriter) (string, bool) {
	v := s.mcVersion()
	if v == "" {
		httpError(w, http.StatusConflict, "Minecraft-Version unbekannt (Server startet noch?) — in 1–2 Minuten erneut versuchen")
		return "", false
	}
	return v, true
}

func (s *Server) handleModsSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if n := len([]rune(q)); n < 2 || n > 100 {
		httpError(w, http.StatusBadRequest, "Suchbegriff: 2–100 Zeichen")
		return
	}
	version, ok := s.needVersion(w)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	hits, err := s.modmgr.Search(ctx, q, version)
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"mcVersion": version, "hits": hits})
}

func (s *Server) handleModsPlan(w http.ResponseWriter, r *http.Request) {
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	if project == "" || len(project) > 64 {
		httpError(w, http.StatusBadRequest, "project fehlt")
		return
	}
	version, ok := s.needVersion(w)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	plan, err := s.modmgr.Plan(ctx, project, version)
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (s *Server) handleModsAdd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProjectID string   `json:"projectId"`
		Profiles  []string `json:"profiles"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.ProjectID == "" {
		httpError(w, http.StatusBadRequest, "projectId und profiles angeben")
		return
	}
	version, ok := s.needVersion(w)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	res, err := s.modmgr.AddMods(ctx, req.ProjectID, version, req.Profiles)
	if err != nil {
		s.audit(r.Context(), "mods.add.failed", fmt.Sprintf("project=%s err=%s", req.ProjectID, err))
		httpError(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r.Context(), "mods.add", fmt.Sprintf("project=%s added=%v", req.ProjectID, res.Added))
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleModsUpload(w http.ResponseWriter, r *http.Request) {
	// Streamen statt ParseMultipartForm: keine Temp-Dateien im (read-only)
	// Root-FS, kein 100-MB-Puffer im RAM (mem_limit). Die kleinen Felder
	// profile/category müssen VOR der Datei kommen (stream.js uploadMod).
	r.Body = http.MaxBytesReader(w, r.Body, mods.MaxUploadBytes+(1<<20))
	mr, err := r.MultipartReader()
	if err != nil {
		httpError(w, http.StatusBadRequest, "Upload ungültig (multipart/form-data erwartet)")
		return
	}
	var profiles []string
	category := ""
	var up mods.UploadFile
	filename := ""
	for up.Path == "" {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			httpError(w, http.StatusBadRequest, "Upload zu groß oder ungültig (max. 100 MB)")
			return
		}
		switch part.FormName() {
		case "profile", "category":
			b, err := io.ReadAll(io.LimitReader(part, 256))
			if err != nil {
				part.Close()
				httpError(w, http.StatusBadRequest, "Upload zu groß oder ungültig (max. 100 MB)")
				return
			}
			if part.FormName() == "profile" {
				profiles = append(profiles, strings.TrimSpace(string(b)))
			} else {
				category = strings.TrimSpace(string(b))
			}
		case "file":
			if len(profiles) == 0 {
				part.Close()
				httpError(w, http.StatusBadRequest, "Datei und mindestens ein Profil angeben (Profil vor der Datei senden)")
				return
			}
			filename = part.FileName()
			up, err = s.modmgr.SpoolUpload(profiles[0], part)
			if err != nil {
				part.Close()
				s.audit(r.Context(), "mods.upload.failed", fmt.Sprintf("profiles=%v file=%s err=%s", profiles, filename, err))
				httpError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		part.Close()
	}
	if up.Path == "" {
		httpError(w, http.StatusBadRequest, "Datei und mindestens ein Profil angeben")
		return
	}
	defer os.Remove(up.Path)
	if category == "" {
		category = "mods"
	}
	e, err := s.modmgr.StageUploads(r.Context(), profiles, category, filename, up)
	if err != nil {
		s.audit(r.Context(), "mods.upload.failed", fmt.Sprintf("profiles=%v file=%s err=%s", profiles, filename, err))
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r.Context(), "mods.upload", fmt.Sprintf("profiles=%v file=%s managed=%v", profiles, filename, e.Managed))
	writeJSON(w, http.StatusOK, e)
}

func (s *Server) handleModsRemoveInfo(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	info, err := s.modmgr.RemovalInfo(ctx, r.URL.Query().Get("profile"), r.URL.Query().Get("file"))
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

type profileFileReq struct {
	Profile  string `json:"profile"`
	Filename string `json:"filename"`
}

func decodeProfileFile(w http.ResponseWriter, r *http.Request) (profileFileReq, bool) {
	var req profileFileReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid json")
		return req, false
	}
	return req, true
}

func (s *Server) handleModsRemove(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeProfileFile(w, r)
	if !ok {
		return
	}
	if err := s.modmgr.StageRemove(req.Profile, req.Filename); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r.Context(), "mods.remove", fmt.Sprintf("profile=%s file=%s", req.Profile, req.Filename))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleModsDiscard(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeProfileFile(w, r)
	if !ok {
		return
	}
	if err := s.modmgr.Discard(req.Profile, req.Filename); err != nil {
		httpError(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r.Context(), "mods.discard", fmt.Sprintf("profile=%s file=%s", req.Profile, req.Filename))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleModsApplyInfo(w http.ResponseWriter, r *http.Request) {
	if s.sched == nil {
		writeJSON(w, http.StatusOK, map[string]any{"routine": ""})
		return
	}
	name, at, ok, err := s.sched.NextApply(r.Context(), time.Now())
	if err != nil || !ok {
		writeJSON(w, http.StatusOK, map[string]any{"routine": ""})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"routine": name, "next": at})
}

// startAnnouncedApply runs „Anwenden + Neustart" über den Scheduler: 5 min
// Vorwarnung, save-all, Stop → Tausch → Start, Watchdog. Vorher startete
// der Button den Server sofort ohne Warnung neu.
func (s *Server) startAnnouncedApply(w http.ResponseWriter, r *http.Request) {
	if !s.modmgr.HasStaged(mods.ProfileServer) {
		httpError(w, http.StatusConflict, "für den Server ist nichts vorgemerkt")
		return
	}
	if !s.applyBusy.CompareAndSwap(false, true) {
		httpError(w, http.StatusConflict, "Einspielen läuft bereits")
		return
	}
	s.audit(r.Context(), "mods.apply", "profile=server (angekündigter Neustart, 5 min)")
	go func() {
		defer s.applyBusy.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		s.sched.RunAdhoc(ctx, storage.Routine{
			Name: "Mod-Änderungen einspielen", Kind: "announce-restart", Payload: s.mcContainer,
			WarnMinutes: 5, ApplyStaged: true, WatchdogMinutes: 10,
		})
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{
		"scheduled": true,
		"message":   "Server startet in 5 Minuten neu (Spieler werden im Spiel gewarnt) — danach sind die Änderungen aktiv.",
	})
}
