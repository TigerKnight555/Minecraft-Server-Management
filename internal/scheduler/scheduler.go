// Package scheduler runs routines (stage 1 of the concept's Routinen &
// Wartungsfenster): cron-triggered RCON commands, container restarts and
// announced restarts with player warnings. Invariant: routines never fail
// silently — every run is recorded, failures land in the log and the run
// history.
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/collector"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
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

type Scheduler struct {
	store      RoutineStore
	rcon       collector.RCONClient
	controller collector.ContainerController
	containers resolver
	log        *slog.Logger
	bus        *events.Bus // optional; nil bus is a safe no-op

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
	if err != nil {
		run.Message = err.Error()
		s.log.Error("routine failed", "name", r.Name, "err", err)
		s.bus.Publish(events.Event{
			Type: events.TypeRoutineFailed, Severity: events.SevError,
			Title:   "Routine fehlgeschlagen: " + r.Name,
			Message: err.Error(),
			Fields:  []events.Field{{Name: "Typ", Value: r.Kind}},
		})
	} else {
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

// announceRestart warns players each minute, then restarts the container.
func (s *Scheduler) announceRestart(ctx context.Context, r storage.Routine) (string, error) {
	id, err := s.resolve(r.Payload)
	if err != nil {
		return "", err
	}
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
	if err := s.controller.RestartContainer(ctx, id); err != nil {
		return "", fmt.Errorf("restart %s: %w", r.Payload, err)
	}
	return fmt.Sprintf("Neustart nach %d min Vorwarnung", r.WarnMinutes), nil
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
