package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/backup"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/scheduler"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/storage"
)

// AdminStore is the persistence surface for Phase-2 features.
type AdminStore interface {
	Audit(ctx context.Context, action, detail string) error
	RecentAudit(ctx context.Context, limit int) ([]storage.AuditEntry, error)
	ListRoutines(ctx context.Context) ([]storage.Routine, error)
	GetRoutine(ctx context.Context, id int64) (storage.Routine, error)
	CreateRoutine(ctx context.Context, r storage.Routine) (int64, error)
	UpdateRoutine(ctx context.Context, r storage.Routine) error
	DeleteRoutine(ctx context.Context, id int64) error
	RecentRuns(ctx context.Context, limit int) ([]storage.RoutineRun, error)
	// Soll-Zustand (Phase 4.5): nur explizite Nutzer-Aktionen setzen ihn
	SetDesiredState(ctx context.Context, container, state string) error
}

func (s *Server) audit(ctx context.Context, action, detail string) {
	if s.admin == nil {
		return
	}
	if err := s.admin.Audit(ctx, action, detail); err != nil {
		s.log.Error("audit write failed", "action", action, "err", err)
	}
}

// handleContainerAction is POST /api/containers/{name}/{action}.
// Only allowlisted containers may be controlled (MSM_MANAGED_CONTAINERS).
func (s *Server) handleContainerAction(w http.ResponseWriter, r *http.Request) {
	if s.controller == nil {
		httpError(w, http.StatusServiceUnavailable, "Container-Steuerung nicht konfiguriert")
		return
	}
	name := r.PathValue("name")
	action := r.PathValue("action")

	if !s.managed[name] {
		httpError(w, http.StatusForbidden, "Container nicht in der Verwaltungs-Allowlist (MSM_MANAGED_CONTAINERS)")
		return
	}
	var id string
	for _, c := range s.collector.Containers() {
		if c.Name == name {
			id = c.ID
			break
		}
	}
	if id == "" {
		httpError(w, http.StatusNotFound, "unbekannter Container")
		return
	}

	var err error
	switch action {
	case "start":
		err = s.controller.StartContainer(r.Context(), id)
	case "stop":
		err = s.controller.StopContainer(r.Context(), id)
	case "restart":
		err = s.controller.RestartContainer(r.Context(), id)
	default:
		httpError(w, http.StatusBadRequest, "unbekannte Aktion")
		return
	}
	detail := fmt.Sprintf("container=%s", name)
	if err != nil {
		s.audit(r.Context(), "container."+action+".failed", detail+" err="+err.Error())
		s.log.Error("container action failed", "action", action, "container", name, "err", err)
		httpError(w, http.StatusBadGateway, "Aktion fehlgeschlagen: "+err.Error())
		return
	}
	// Nutzer-Intention persistieren: der Boot-Abgleich stellt genau diesen
	// Zustand nach jedem Host-Reboot wieder her (bewusst gestoppt bleibt aus)
	if s.admin != nil {
		desired := map[string]string{"start": "running", "restart": "running", "stop": "stopped"}[action]
		if err := s.admin.SetDesiredState(r.Context(), name, desired); err != nil {
			s.log.Error("soll-zustand speichern fehlgeschlagen", "container", name, "err", err)
		}
	}
	s.audit(r.Context(), "container."+action, detail)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleListRoutines(w http.ResponseWriter, r *http.Request) {
	routines, err := s.admin.ListRoutines(r.Context())
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if routines == nil {
		routines = []storage.Routine{}
	}
	writeJSON(w, http.StatusOK, routines)
}

func validateRoutine(rt *storage.Routine) string {
	rt.Name = strings.TrimSpace(rt.Name)
	rt.Payload = strings.TrimSpace(rt.Payload)
	if rt.Name == "" {
		return "Name fehlt"
	}
	if err := scheduler.ValidateCron(rt.Cron); err != nil {
		return "ungültiger Cron-Ausdruck: " + err.Error()
	}
	switch rt.Kind {
	case "rcon":
		if rt.Payload == "" {
			return "RCON-Befehl fehlt"
		}
	case "restart", "announce-restart":
		if rt.Payload == "" {
			return "Container-Name fehlt"
		}
		if rt.WarnMinutes < 0 || rt.WarnMinutes > 60 {
			return "Vorwarnzeit muss zwischen 0 und 60 Minuten liegen"
		}
	case "backup", "host-reboot":
		// Payload = Minecraft-Container (wird für Snapshot bzw. Reboot gestoppt)
		if rt.Payload == "" {
			return "Container-Name fehlt (der Minecraft-Container)"
		}
		if rt.WarnMinutes < 0 || rt.WarnMinutes > 60 {
			return "Vorwarnzeit muss zwischen 0 und 60 Minuten liegen"
		}
		if rt.Kind == "host-reboot" && (rt.ApplyStaged || rt.WatchdogMinutes != 0) {
			return "Update-Einspielen und Watchdog gehören zur Backup-Routine, nicht zum Host-Reboot"
		}
	default:
		return "unbekannter Typ (rcon, restart, announce-restart, backup, host-reboot)"
	}
	stage2OK := rt.Kind == "announce-restart" || rt.Kind == "backup" || rt.Kind == "host-reboot"
	if !stage2OK && (rt.SkipIfPlayersOnline || rt.WaitForEmpty || rt.ApplyStaged || rt.WatchdogMinutes != 0) {
		return "Bedingungen, Update-Einspielen und Watchdog gibt es nur bei angekündigtem Neustart, Backup und Host-Reboot"
	}
	if rt.WatchdogMinutes < 0 || rt.WatchdogMinutes > 30 {
		return "Watchdog muss zwischen 0 und 30 Minuten liegen"
	}
	rt.WaitDeadline = strings.TrimSpace(rt.WaitDeadline)
	if rt.WaitDeadline != "" {
		if !rt.WaitForEmpty {
			return "Warte-Frist ohne 'auf leeren Server warten' ergibt keinen Sinn"
		}
		if _, err := time.Parse("15:04", rt.WaitDeadline); err != nil {
			return "Warte-Frist muss HH:MM sein (z. B. 06:00)"
		}
	}
	return ""
}

func (s *Server) handleCreateRoutine(w http.ResponseWriter, r *http.Request) {
	var rt storage.Routine
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&rt); err != nil {
		httpError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if msg := validateRoutine(&rt); msg != "" {
		httpError(w, http.StatusBadRequest, msg)
		return
	}
	id, err := s.admin.CreateRoutine(r.Context(), rt)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rt.ID = id
	s.audit(r.Context(), "routine.create", rt.Name)
	s.reloadScheduler(r.Context())
	writeJSON(w, http.StatusCreated, rt)
}

func (s *Server) handleUpdateRoutine(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var rt storage.Routine
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&rt); err != nil {
		httpError(w, http.StatusBadRequest, "invalid json")
		return
	}
	rt.ID = id
	if msg := validateRoutine(&rt); msg != "" {
		httpError(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.admin.UpdateRoutine(r.Context(), rt); err != nil {
		httpError(w, http.StatusNotFound, "Routine nicht gefunden")
		return
	}
	s.audit(r.Context(), "routine.update", rt.Name)
	s.reloadScheduler(r.Context())
	writeJSON(w, http.StatusOK, rt)
}

func (s *Server) handleDeleteRoutine(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.admin.DeleteRoutine(r.Context(), id); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r.Context(), "routine.delete", strconv.FormatInt(id, 10))
	s.reloadScheduler(r.Context())
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleRunRoutine triggers a routine immediately, detached from the request
// so long announce sequences survive the HTTP round trip.
func (s *Server) handleRunRoutine(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if s.sched == nil {
		httpError(w, http.StatusServiceUnavailable, "Scheduler nicht aktiv")
		return
	}
	s.audit(r.Context(), "routine.run-now", strconv.FormatInt(id, 10))
	go s.sched.RunNow(context.Background(), id)
	writeJSON(w, http.StatusAccepted, map[string]bool{"started": true})
}

// handleListPlayers returns the restorable players (playerdata scan +
// usercache names) for the restore dropdown.
func (s *Server) handleListPlayers(w http.ResponseWriter, r *http.Request) {
	players, err := backup.ListPlayers(s.mcDataDir)
	if err != nil {
		s.log.Error("player list failed", "err", err)
		httpError(w, http.StatusInternalServerError, "Spielerliste nicht lesbar: "+err.Error())
		return
	}
	if players == nil {
		players = []backup.Player{}
	}
	writeJSON(w, http.StatusOK, players)
}

// handleRestorePlayer restores one player's data file from the newest
// backup snapshot. Synchronous — the browser waits for the result.
func (s *Server) handleRestorePlayer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UUID string `json:"uuid"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid json")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Minute)
	defer cancel()
	msg, err := s.restore.RestorePlayer(ctx, req.UUID)
	if err != nil {
		s.audit(r.Context(), "backup.restore.failed", "uuid="+req.UUID+" err="+err.Error())
		s.bus.Publish(events.Event{
			Type: events.TypeRestoreFailed, Severity: events.SevError,
			Title:   "Spielerdaten-Restore fehlgeschlagen",
			Message: err.Error(),
			Fields:  []events.Field{{Name: "UUID", Value: req.UUID}},
		})
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.audit(r.Context(), "backup.restore", "uuid="+req.UUID)
	s.bus.Publish(events.Event{
		Type: events.TypeRestoreOK, Severity: events.SevSuccess,
		Title:   "Spielerdaten wiederhergestellt",
		Message: msg,
		Fields:  []events.Field{{Name: "UUID", Value: req.UUID}},
	})
	writeJSON(w, http.StatusOK, map[string]string{"message": msg})
}

func (s *Server) handleRecentRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := s.admin.RecentRuns(r.Context(), 50)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if runs == nil {
		runs = []storage.RoutineRun{}
	}
	writeJSON(w, http.StatusOK, runs)
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	entries, err := s.admin.RecentAudit(r.Context(), 100)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if entries == nil {
		entries = []storage.AuditEntry{}
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) reloadScheduler(ctx context.Context) {
	if s.sched == nil {
		return
	}
	if err := s.sched.Reload(ctx); err != nil {
		s.log.Error("scheduler reload failed", "err", err)
	}
}
