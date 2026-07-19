// Package api exposes the HTTP surface: REST snapshots, SSE live streams,
// the RCON console endpoint and the embedded frontend.
package api

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/auth"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/backup"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/collector"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/dropbox"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mods"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/scheduler"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/settings"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/upgrade"
)

type Server struct {
	collector         *collector.Collector
	docker            collector.DockerClient
	controller        collector.ContainerController
	rcon              collector.RCONClient
	store             collector.Store
	admin             AdminStore
	sched             *scheduler.Scheduler
	authmgr           *auth.Manager
	modmgr            *mods.Manager
	watcher           *mods.Watcher
	restore           *backup.Restore
	mcDataDir         string      // read-only mount of the MC data dir ("" = feature off)
	maintActive       func() bool // Wartungsfenster gerade aktiv?
	dropbox           *dropbox.Client
	upgrader          *upgrade.Orchestrator
	settings          *settings.Store
	mcContainer       string
	fallbackMCVersion string
	managed           map[string]bool // allowlist for container actions
	bus               *events.Bus     // optional; nil bus is a safe no-op
	log               *slog.Logger
	frontend          fs.FS // embedded web/dist, may be nil in tests
}

// Deps bundles the wiring — Phase 2 grew too many constructor params.
type Deps struct {
	Collector         *collector.Collector
	Docker            collector.DockerClient
	Controller        collector.ContainerController
	RCON              collector.RCONClient
	Store             collector.Store
	Admin             AdminStore
	Scheduler         *scheduler.Scheduler
	Auth              *auth.Manager
	ModManager        *mods.Manager
	Watcher           *mods.Watcher
	Restore           *backup.Restore
	MCDataDir         string      // read-only MC data dir for the player list
	MaintActive       func() bool // maintenance.Manager.Active
	Dropbox           *dropbox.Client
	Upgrader          *upgrade.Orchestrator
	Settings          *settings.Store
	MCContainer       string   // name of the minecraft container (mod apply restart)
	FallbackMCVersion string   // used when query has no version yet
	Managed           []string // container names allowed for start/stop/restart
	Bus               *events.Bus
	Frontend          fs.FS
	Log               *slog.Logger
}

func New(d Deps) *Server {
	managed := make(map[string]bool, len(d.Managed))
	for _, name := range d.Managed {
		if name = strings.TrimSpace(name); name != "" {
			managed[name] = true
		}
	}
	return &Server{
		collector:         d.Collector,
		docker:            d.Docker,
		controller:        d.Controller,
		rcon:              d.RCON,
		store:             d.Store,
		admin:             d.Admin,
		sched:             d.Scheduler,
		authmgr:           d.Auth,
		modmgr:            d.ModManager,
		watcher:           d.Watcher,
		restore:           d.Restore,
		mcDataDir:         d.MCDataDir,
		maintActive:       d.MaintActive,
		dropbox:           d.Dropbox,
		upgrader:          d.Upgrader,
		settings:          d.Settings,
		mcContainer:       d.MCContainer,
		fallbackMCVersion: d.FallbackMCVersion,
		managed:           managed,
		bus:               d.Bus,
		log:               d.Log,
		frontend:          d.Frontend,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /api/snapshot", s.handleSnapshot)
	mux.HandleFunc("GET /api/containers", s.handleContainers)
	mux.HandleFunc("GET /api/history", s.handleHistory)
	mux.HandleFunc("GET /api/stream/stats", s.handleStatsStream)
	mux.HandleFunc("GET /api/stream/logs", s.handleLogsStream)
	mux.HandleFunc("POST /api/rcon", s.handleRCON)

	// Phase 2
	mux.HandleFunc("POST /api/containers/{name}/{action}", s.handleContainerAction)
	if s.admin != nil {
		mux.HandleFunc("GET /api/routines", s.handleListRoutines)
		mux.HandleFunc("POST /api/routines", s.handleCreateRoutine)
		mux.HandleFunc("PUT /api/routines/{id}", s.handleUpdateRoutine)
		mux.HandleFunc("DELETE /api/routines/{id}", s.handleDeleteRoutine)
		mux.HandleFunc("POST /api/routines/{id}/run", s.handleRunRoutine)
		mux.HandleFunc("GET /api/routine-runs", s.handleRecentRuns)
		mux.HandleFunc("GET /api/audit", s.handleAudit)
	}
	// Phase 3
	if s.modmgr != nil {
		mux.HandleFunc("GET /api/mods", s.handleModsList)
		mux.HandleFunc("POST /api/mods/check", s.handleModsCheck)
		mux.HandleFunc("POST /api/mods/stage", s.handleModsStage)
		mux.HandleFunc("POST /api/mods/apply", s.handleModsApply)
		mux.HandleFunc("POST /api/mods/rollback", s.handleModsRollback)
		if s.dropbox != nil {
			mux.HandleFunc("POST /api/mods/publish", s.handlePublishClientPack)
		}
	}
	if s.watcher != nil {
		mux.HandleFunc("GET /api/version-watch", s.handleVersionWatch)
		mux.HandleFunc("POST /api/version-watch/check", s.handleVersionWatchCheck)
	}
	// MC-Versions-Upgrade per Klick
	if s.upgrader != nil {
		mux.HandleFunc("POST /api/version-upgrade", s.handleVersionUpgrade)
		mux.HandleFunc("GET /api/version-upgrade/status", s.handleVersionUpgradeStatus)
	}
	// Phase 4.4
	if s.restore != nil {
		mux.HandleFunc("POST /api/backup/restore-player", s.handleRestorePlayer)
	}
	if s.mcDataDir != "" {
		mux.HandleFunc("GET /api/backup/players", s.handleListPlayers)
	}
	// Einstellungen (Integrationen über das Dashboard konfigurieren)
	if s.settings != nil {
		mux.HandleFunc("GET /api/settings", s.handleGetSettings)
		mux.HandleFunc("PUT /api/settings", s.handleSaveSettings)
		mux.HandleFunc("POST /api/settings/discord/test", s.handleTestDiscord)
		mux.HandleFunc("POST /api/settings/reveal", s.handleRevealSetting)
	}
	// Phase 4.6
	if s.admin != nil {
		mux.HandleFunc("GET /api/maintenance", s.handleListWindows)
		mux.HandleFunc("POST /api/maintenance", s.handleCreateWindow)
		mux.HandleFunc("POST /api/maintenance/{id}/end", s.handleEndWindow)
		mux.HandleFunc("DELETE /api/maintenance/{id}", s.handleDeleteWindow)
	}
	if s.authmgr != nil {
		mux.HandleFunc("POST /api/login", s.authmgr.HandleLogin)
		mux.HandleFunc("POST /api/logout", s.authmgr.HandleLogout)
		mux.HandleFunc("GET /api/auth", s.authmgr.HandleStatus)
	}
	if s.frontend != nil {
		mux.Handle("GET /", http.FileServerFS(s.frontend))
	}
	if s.authmgr != nil {
		return s.authmgr.Middleware(mux)
	}
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.collector.Snapshot())
}

func (s *Server) handleContainers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.collector.Containers())
}

// handleHistory serves chart data: /api/history?series=host.cpu&hours=24
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	series := r.URL.Query().Get("series")
	if series == "" {
		httpError(w, http.StatusBadRequest, "missing series parameter")
		return
	}
	hours := 24
	if h := r.URL.Query().Get("hours"); h != "" {
		if _, err := fmt.Sscanf(h, "%d", &hours); err != nil || hours < 1 || hours > 24*30 {
			httpError(w, http.StatusBadRequest, "invalid hours parameter")
			return
		}
	}
	to := time.Now()
	from := to.Add(-time.Duration(hours) * time.Hour)
	samples, err := s.store.QuerySeries(r.Context(), series, from, to)
	if err != nil {
		s.log.Error("history query failed", "series", series, "err", err)
		httpError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, samples)
}

// handleStatsStream is the main SSE endpoint. Event types: container, host,
// mc, wan — plus an initial "snapshot" event so the page paints immediately.
func (s *Server) handleStatsStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	events, cancel := s.collector.Subscribe()
	defer cancel()

	writeSSE(w, "snapshot", s.collector.Snapshot())
	flusher.Flush()

	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		case ev := <-events:
			writeSSE(w, ev.Type, ev.Data)
			flusher.Flush()
		}
	}
}

// handleLogsStream streams docker logs of one container as SSE:
// /api/stream/logs?container=<name-or-id>&tail=200
func (s *Server) handleLogsStream(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("container")
	if name == "" {
		httpError(w, http.StatusBadRequest, "missing container parameter")
		return
	}
	id := ""
	for _, ct := range s.collector.Containers() {
		if ct.Name == name || ct.ID == name || strings.HasPrefix(ct.ID, name) {
			id = ct.ID
			break
		}
	}
	if id == "" {
		httpError(w, http.StatusNotFound, "unknown container")
		return
	}
	tail := 200
	if t := r.URL.Query().Get("tail"); t != "" {
		fmt.Sscanf(t, "%d", &tail)
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	logs, err := s.docker.StreamLogs(r.Context(), id, tail)
	if err != nil {
		s.log.Error("log stream failed", "container", name, "err", err)
		httpError(w, http.StatusBadGateway, "log stream failed")
		return
	}
	defer logs.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")

	buf := make([]byte, 4096)
	var partial string
	for {
		n, err := logs.Read(buf)
		if n > 0 {
			partial += string(buf[:n])
			for {
				line, rest, found := strings.Cut(partial, "\n")
				if !found {
					break
				}
				partial = rest
				writeSSE(w, "log", strings.TrimRight(line, "\r"))
			}
			flusher.Flush()
		}
		if err != nil {
			return
		}
	}
}

type rconRequest struct {
	Command string `json:"command"`
}

func (s *Server) handleRCON(w http.ResponseWriter, r *http.Request) {
	if s.rcon == nil {
		httpError(w, http.StatusServiceUnavailable, "rcon not configured")
		return
	}
	var req rconRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	req.Command = strings.TrimSpace(req.Command)
	if req.Command == "" {
		httpError(w, http.StatusBadRequest, "empty command")
		return
	}
	out, err := s.rcon.Exec(r.Context(), req.Command)
	if err != nil {
		s.audit(r.Context(), "rcon.failed", req.Command)
		s.log.Error("rcon exec failed", "err", err)
		httpError(w, http.StatusBadGateway, "rcon failed: "+err.Error())
		return
	}
	s.audit(r.Context(), "rcon", req.Command)
	writeJSON(w, http.StatusOK, map[string]string{"response": out})
}

func writeSSE(w http.ResponseWriter, event string, data any) {
	payload, err := json.Marshal(data)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
