// Package scheduler runs routines (stage 1 of the concept's Routinen &
// Wartungsfenster): cron-triggered RCON commands, container restarts and
// announced restarts with player warnings. Invariant: routines never fail
// silently — every run is recorded, failures land in the log and the run
// history.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/collector"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mods"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/storage"
)

// RoutineStore is the persistence the scheduler needs.
type RoutineStore interface {
	ListRoutines(ctx context.Context) ([]storage.Routine, error)
	GetRoutine(ctx context.Context, id int64) (storage.Routine, error)
	RecordRun(ctx context.Context, run storage.RoutineRun) error
}

// resolver maps container names to IDs (the collector already knows them).
type resolver interface {
	Containers() []collector.Container
}

// StagedApplier swaps staged mod updates into the live directory
// (mods.Manager satisfies this).
type StagedApplier interface {
	ApplyStaged(profile string) (string, int, error)
}

// errSkipped marks a run that was intentionally not executed because a
// condition held (e.g. players online). Not a failure — the run history
// records it as OK with the reason.
type errSkipped struct{ reason string }

func (e errSkipped) Error() string { return e.reason }

type Scheduler struct {
	store      RoutineStore
	rcon       collector.RCONClient
	controller collector.ContainerController
	containers resolver
	log        *slog.Logger
	bus        *events.Bus // optional; nil bus is a safe no-op

	// stage-2 deps, both optional (setters); routines that need them fail
	// with a clear message instead of silently degrading
	mcStatus func() collector.MCStatus
	applier  StagedApplier

	// warnStep is 1 minute in production; tests shrink it.
	warnStep time.Duration

	mu      sync.Mutex
	cron    *cron.Cron
	entries map[int64]cron.EntryID
	ctx     context.Context
}

func New(store RoutineStore, rcon collector.RCONClient, controller collector.ContainerController, containers resolver, log *slog.Logger) *Scheduler {
	return &Scheduler{
		store:      store,
		rcon:       rcon,
		controller: controller,
		containers: containers,
		log:        log,
		warnStep:   time.Minute,
		entries:    make(map[int64]cron.EntryID),
	}
}

// SetBus wires the event bus; routine outcomes are published there.
func (s *Scheduler) SetBus(b *events.Bus) { s.bus = b }

// SetMCStatus wires the Minecraft state source (player conditions, watchdog).
func (s *Scheduler) SetMCStatus(f func() collector.MCStatus) { s.mcStatus = f }

// SetStagedApplier wires the mod manager for "apply staged on restart".
func (s *Scheduler) SetStagedApplier(a StagedApplier) { s.applier = a }

// Start loads all enabled routines and begins scheduling; Reload picks up
// changes after CRUD operations.
func (s *Scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
	if err := s.Reload(ctx); err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		s.mu.Lock()
		if s.cron != nil {
			s.cron.Stop()
		}
		s.mu.Unlock()
	}()
	return nil
}

// Reload rebuilds the cron table from the store.
func (s *Scheduler) Reload(ctx context.Context) error {
	routines, err := s.store.ListRoutines(ctx)
	if err != nil {
		return fmt.Errorf("list routines: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cron != nil {
		s.cron.Stop()
	}
	s.cron = cron.New()
	s.entries = make(map[int64]cron.EntryID)
	for _, r := range routines {
		if !r.Enabled {
			continue
		}
		id, err := s.cron.AddFunc(r.Cron, func() { s.RunNow(s.ctx, r.ID) })
		if err != nil {
			// invalid cron must be visible, not silent
			s.log.Error("routine has invalid cron, skipped", "routine", r.Name, "cron", r.Cron, "err", err)
			s.store.RecordRun(context.Background(), storage.RoutineRun{
				RoutineID: r.ID, Time: time.Now(), OK: false,
				Message: "ungültiger Cron-Ausdruck: " + r.Cron,
			})
			continue
		}
		s.entries[r.ID] = id
	}
	s.cron.Start()
	return nil
}

// ValidateCron checks an expression without scheduling it.
func ValidateCron(expr string) error {
	_, err := cron.ParseStandard(expr)
	return err
}

// RunNow executes one routine immediately (used by cron and the "jetzt
// ausführen" button). Always records the outcome.
func (s *Scheduler) RunNow(ctx context.Context, routineID int64) {
	if ctx == nil {
		ctx = context.Background()
	}
	r, err := s.store.GetRoutine(ctx, routineID)
	if err != nil {
		s.log.Error("routine vanished", "id", routineID, "err", err)
		return
	}
	s.log.Info("routine started", "name", r.Name, "kind", r.Kind)
	msg, err := s.execute(ctx, r)
	run := storage.RoutineRun{RoutineID: r.ID, Time: time.Now(), OK: err == nil, Message: msg}
	var skipped errSkipped
	switch {
	case errors.As(err, &skipped):
		// Bedingung griff — kein Fehler, aber sichtbar (nie stilles Ausfallen)
		run.OK = true
		run.Message = "übersprungen: " + skipped.reason
		s.log.Info("routine skipped", "name", r.Name, "reason", skipped.reason)
		s.bus.Publish(events.Event{
			Type: events.TypeRoutineSkipped, Severity: events.SevInfo,
			Title:   "Routine übersprungen: " + r.Name,
			Message: skipped.reason,
			Fields:  []events.Field{{Name: "Typ", Value: r.Kind}},
		})
	case err != nil:
		run.Message = err.Error()
		s.log.Error("routine failed", "name", r.Name, "err", err)
		s.bus.Publish(events.Event{
			Type: events.TypeRoutineFailed, Severity: events.SevError,
			Title:   "Routine fehlgeschlagen: " + r.Name,
			Message: err.Error(),
			Fields:  []events.Field{{Name: "Typ", Value: r.Kind}},
		})
	default:
		s.log.Info("routine finished", "name", r.Name)
		s.bus.Publish(events.Event{
			Type: events.TypeRoutineOK, Severity: events.SevSuccess,
			Title:   "Routine erfolgreich: " + r.Name,
			Message: msg,
			Fields:  []events.Field{{Name: "Typ", Value: r.Kind}},
		})
	}
	if err := s.store.RecordRun(ctx, run); err != nil {
		s.log.Error("record run failed", "routine", r.Name, "err", err)
	}
}

func (s *Scheduler) execute(ctx context.Context, r storage.Routine) (string, error) {
	switch r.Kind {
	case "rcon":
		if s.rcon == nil {
			return "", fmt.Errorf("rcon nicht konfiguriert")
		}
		out, err := s.rcon.Exec(ctx, r.Payload)
		if err != nil {
			return "", fmt.Errorf("rcon %q: %w", r.Payload, err)
		}
		return truncate(out, 200), nil

	case "restart":
		id, err := s.resolve(r.Payload)
		if err != nil {
			return "", err
		}
		if err := s.controller.RestartContainer(ctx, id); err != nil {
			return "", fmt.Errorf("restart %s: %w", r.Payload, err)
		}
		return "Container neugestartet", nil

	case "announce-restart":
		return s.announceRestart(ctx, r)

	default:
		return "", fmt.Errorf("unbekannter Routinentyp %q", r.Kind)
	}
}

// announceRestart is the stage-2 step chain (Konzept: Routinen &
// Wartungsfenster): Bedingungen → Warnungen → save-all → Stop →
// optional gestagte Updates → Start → Watchdog. Ohne gesetzte
// Stufe-2-Felder verhält es sich exakt wie Stufe 1 (warnen → restart).
func (s *Scheduler) announceRestart(ctx context.Context, r storage.Routine) (string, error) {
	id, err := s.resolve(r.Payload)
	if err != nil {
		return "", err
	}

	// --- Bedingungen ---
	if r.SkipIfPlayersOnline {
		if s.mcStatus == nil {
			return "", fmt.Errorf("bedingung 'Spieler online' braucht den MC-Status (nicht verdrahtet)")
		}
		if n := s.mcStatus().PlayersOnline; n > 0 {
			return "", errSkipped{reason: fmt.Sprintf("%d Spieler online", n)}
		}
	}
	if r.WaitForEmpty {
		if err := s.waitForEmpty(ctx, r); err != nil {
			return "", err
		}
	}

	// --- Warnungen ---
	if s.rcon != nil && r.WarnMinutes > 0 {
		for m := r.WarnMinutes; m >= 1; m-- {
			warn := fmt.Sprintf("say §cServer-Neustart in %d Minute(n)!", m)
			if _, err := s.rcon.Exec(ctx, warn); err != nil {
				// server may be down; keep going, the restart still matters
				s.log.Warn("warn announce failed", "err", err)
			}
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(s.warnStep):
			}
		}
		s.rcon.Exec(ctx, "say §cServer startet jetzt neu!")
	}

	// --- Welt sichern, bevor irgendetwas stoppt ---
	if s.rcon != nil {
		if _, err := s.rcon.Exec(ctx, "save-all"); err != nil {
			s.log.Warn("save-all before restart failed", "err", err)
		}
	}

	var steps []string
	if r.ApplyStaged {
		// expliziter Stop → Tausch → Start, damit die neuen Mods sicher
		// erst nach vollständigem Herunterfahren im Verzeichnis liegen
		if s.applier == nil {
			return "", fmt.Errorf("'Updates einspielen' braucht die Mod-Verwaltung (nicht verdrahtet)")
		}
		if err := s.controller.StopContainer(ctx, id); err != nil {
			return "", fmt.Errorf("stop %s: %w", r.Payload, err)
		}
		label, n, err := s.applier.ApplyStaged("server")
		switch {
		case errors.Is(err, mods.ErrNothingStaged):
			steps = append(steps, "keine gestagten Updates")
		case err != nil:
			// Server nicht liegen lassen: trotz Fehlschlag wieder starten
			if startErr := s.controller.StartContainer(ctx, id); startErr != nil {
				return "", fmt.Errorf("updates einspielen: %v; start danach AUCH fehlgeschlagen: %w", err, startErr)
			}
			return "", fmt.Errorf("updates einspielen (Server läuft wieder mit altem Stand): %w", err)
		default:
			steps = append(steps, fmt.Sprintf("%d Updates eingespielt (Backup %s)", n, label))
		}
		if err := s.controller.StartContainer(ctx, id); err != nil {
			return "", fmt.Errorf("start %s: %w", r.Payload, err)
		}
	} else {
		if err := s.controller.RestartContainer(ctx, id); err != nil {
			return "", fmt.Errorf("restart %s: %w", r.Payload, err)
		}
	}
	steps = append([]string{fmt.Sprintf("Neustart nach %d min Vorwarnung", r.WarnMinutes)}, steps...)

	// --- Watchdog: erst fertig, wenn der Server wieder antwortet ---
	if r.WatchdogMinutes > 0 {
		if err := s.watchdog(ctx, time.Duration(r.WatchdogMinutes)*time.Minute); err != nil {
			return "", err
		}
		steps = append(steps, "Watchdog: Server wieder online")
	}
	return strings.Join(steps, "; "), nil
}

// waitForEmpty polls until no players are online, at most until the
// routine's deadline ("HH:MM", empty = 60 min from now). Reaching the
// deadline does NOT abort — the restart then proceeds (concept: "auf leeren
// Server warten, max. bis 06:00").
func (s *Scheduler) waitForEmpty(ctx context.Context, r storage.Routine) error {
	if s.mcStatus == nil {
		return fmt.Errorf("bedingung 'auf leeren Server warten' braucht den MC-Status (nicht verdrahtet)")
	}
	deadline := time.Now().Add(time.Hour)
	if r.WaitDeadline != "" {
		t, err := time.ParseInLocation("15:04", r.WaitDeadline, time.Local)
		if err != nil {
			return fmt.Errorf("ungültige Warte-Frist %q (erwartet HH:MM)", r.WaitDeadline)
		}
		now := time.Now()
		deadline = time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, time.Local)
		if deadline.Before(now) {
			deadline = deadline.Add(24 * time.Hour) // Frist ist heute vorbei → morgen
		}
	}
	for time.Now().Before(deadline) {
		if s.mcStatus().PlayersOnline == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.warnStep):
		}
	}
	s.log.Info("wait-for-empty deadline reached, proceeding anyway")
	return nil
}

// watchdog waits until the Minecraft server reports online again. Failure is
// a routine failure — the concept's "kein stilles Ausfallen" applies to the
// restart's outcome, not just its start.
func (s *Scheduler) watchdog(ctx context.Context, timeout time.Duration) error {
	if s.mcStatus == nil {
		return fmt.Errorf("watchdog braucht den MC-Status (nicht verdrahtet)")
	}
	start := time.Now()
	deadline := start.Add(timeout)
	for time.Now().Before(deadline) {
		// nur Messungen zählen, die NACH dem Neustart erhoben wurden — der
		// Collector-Cache kann sonst noch den alten "online"-Stand zeigen
		if st := s.mcStatus(); st.Online && st.Time.After(start) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.warnStep / 4):
		}
	}
	return fmt.Errorf("watchdog: Server nach %s nicht wieder online — bitte prüfen!", timeout)
}

func (s *Scheduler) resolve(name string) (string, error) {
	if s.containers != nil {
		for _, c := range s.containers.Containers() {
			if c.Name == name || c.ID == name {
				return c.ID, nil
			}
		}
	}
	if name != "" {
		// fall back to the raw name — docker accepts names too
		return name, nil
	}
	return "", fmt.Errorf("kein Container angegeben")
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
