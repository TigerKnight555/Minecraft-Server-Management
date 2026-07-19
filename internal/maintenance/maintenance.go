// Package maintenance drives planned offline windows (Phase 4.6):
// Spieler-Warnungen (30/15/5/1 min) → save-all → Server-Stopp → während des
// Fensters sind Alarme stumm (der Notifier fragt Active()) → zum Ende Start
// + Watchdog + grüne Meldung. Fortschritt ist persistiert — ein MSM-Neustart
// mitten im Fenster macht keinen Schritt doppelt.
//
// Bewusste v1-Vereinfachungen (siehe Vault-Notiz Phase-4-Plan): einmalige
// Fenster (keine Wiederkehr), Routinen im Fenster werden sichtbar
// übersprungen statt vorgezogen/nachgeholt, kein Host-Shutdown-Modus.
package maintenance

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/collector"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/storage"
)

// Store is the persistence subset the manager needs.
type Store interface {
	ListWindows(ctx context.Context) ([]storage.MaintenanceWindow, error)
	MarkWindow(ctx context.Context, id int64, started, ended, stoppedServer bool) error
	MarkWindowNotified(ctx context.Context, id int64, stage string) error
}

type resolver interface {
	Containers() []collector.Container
}

type Manager struct {
	store      Store
	controller collector.ContainerController
	containers resolver
	rcon       collector.RCONClient
	mcStatus   func() collector.MCStatus
	bus        *events.Bus
	mcName     string
	log        *slog.Logger

	Interval      time.Duration
	OnlineTimeout time.Duration

	active atomic.Bool
	warned map[int64]map[int]bool // window id -> minute mark -> done
}

func New(store Store, controller collector.ContainerController, containers resolver,
	rcon collector.RCONClient, mcStatus func() collector.MCStatus,
	bus *events.Bus, mcName string, log *slog.Logger) *Manager {
	return &Manager{
		store: store, controller: controller, containers: containers,
		rcon: rcon, mcStatus: mcStatus, bus: bus, mcName: mcName, log: log,
		Interval:      20 * time.Second,
		OnlineTimeout: 10 * time.Minute,
		warned:        map[int64]map[int]bool{},
	}
}

// Active reports whether a maintenance window is running right now — der
// Discord-Notifier schaltet damit Alarme stumm, das Dashboard zeigt den
// Banner, der Scheduler überspringt Routinen.
func (m *Manager) Active() bool { return m.active.Load() }

// Run drives all windows until ctx is done.
func (m *Manager) Run(ctx context.Context) {
	t := time.NewTicker(m.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		m.tick(ctx)
	}
}

// tick advances every window one step (exported logic for tests via Tick).
func (m *Manager) Tick(ctx context.Context) { m.tick(ctx) }

func (m *Manager) tick(ctx context.Context) {
	windows, err := m.store.ListWindows(ctx)
	if err != nil {
		m.log.Error("wartungsfenster laden fehlgeschlagen", "err", err)
		return
	}
	now := time.Now()
	anyActive := false
	for _, w := range windows {
		if w.Ended {
			continue
		}
		switch {
		case now.Before(w.Start):
			m.announcePhase(ctx, w, now)
			m.warnPhase(ctx, w, now)
		case now.Before(w.End):
			anyActive = true
			if !w.Started {
				m.begin(ctx, w)
			}
		default: // Fenster vorbei
			if w.Started {
				m.finish(ctx, w)
			} else {
				// nie begonnen (MSM war aus o. Ä.) — nur abhaken
				m.store.MarkWindow(ctx, w.ID, false, true, false)
			}
		}
	}
	m.active.Store(anyActive)
}

// announcePhase posts the Discord reminders before a planned downtime:
// 1 Stunde vorher und nochmal 5 Minuten vorher (Nutzerwunsch). Persistiert —
// ein MSM-Neustart dazwischen (z. B. Nacht-Reboot) wiederholt nichts.
func (m *Manager) announcePhase(ctx context.Context, w storage.MaintenanceWindow, now time.Time) {
	remaining := w.Start.Sub(now)
	fmtSpan := func() string {
		return fmt.Sprintf("%s bis ca. %s Uhr", w.Start.Format("15:04"), w.End.Format("15:04"))
	}
	// kurzfristig angelegte Fenster (< 5 min Vorlauf) überspringen die
	// 1h-Stufe — sonst käme „in einer Stunde" 3 Minuten vor dem Start
	if remaining <= time.Hour && remaining > 5*time.Minute && !w.Notified1h {
		if err := m.store.MarkWindowNotified(ctx, w.ID, "1h"); err != nil {
			m.log.Error("wartung: 1h-marke speichern fehlgeschlagen", "err", err)
			return // lieber nächster Tick als Doppel-Post riskieren
		}
		m.bus.Publish(events.Event{
			Type: events.TypeMaintAnnounce, Severity: events.SevInfo,
			Title:   "🔧 In einer Stunde: Wartung „" + w.Name + "“",
			Message: "Der Server geht heute von " + fmtSpan() + " offline. Meldung folgt, wenn er wieder da ist.",
		})
		return // pro Tick höchstens eine Stufe
	}
	if remaining <= 5*time.Minute && !w.Notified5m {
		if err := m.store.MarkWindowNotified(ctx, w.ID, "5m"); err != nil {
			m.log.Error("wartung: 5m-marke speichern fehlgeschlagen", "err", err)
			return
		}
		m.bus.Publish(events.Event{
			Type: events.TypeMaintAnnounce, Severity: events.SevWarn,
			Title:   "🔧 Gleich geht's los: Wartung „" + w.Name + "“",
			Message: "Der Server geht in etwa 5 Minuten offline (" + fmtSpan() + "). Bitte jetzt einen sicheren Ort suchen und ausloggen.",
		})
	}
}

// warnPhase sends the 30/15/5/1-minute player warnings before the start.
func (m *Manager) warnPhase(ctx context.Context, w storage.MaintenanceWindow, now time.Time) {
	if m.rcon == nil || !m.serverRunning() {
		return
	}
	remaining := int(w.Start.Sub(now).Minutes())
	if remaining < 0 {
		return
	}
	// kleinste passende Warnstufe wählen (Liste absteigend, letzter Treffer
	// gewinnt): 4 min vor Start -> 5er-Marke, nicht die 30er
	target := 0
	for _, mark := range []int{30, 15, 5, 1} {
		if remaining <= mark {
			target = mark
		}
	}
	if target == 0 {
		return // noch mehr als 30 min hin
	}
	if m.warned[w.ID] == nil {
		m.warned[w.ID] = map[int]bool{}
	}
	if m.warned[w.ID][target] {
		return
	}
	m.warned[w.ID][target] = true
	msg := fmt.Sprintf("say §eWartung \"%s\" beginnt in ca. %d Minute(n) — Server geht offline!", w.Name, remaining+1)
	if _, err := m.rcon.Exec(ctx, msg); err != nil {
		m.log.Warn("wartungs-warnung fehlgeschlagen", "err", err)
	}
}

// begin runs the stop sequence at window start.
func (m *Manager) begin(ctx context.Context, w storage.MaintenanceWindow) {
	stopped := false
	if m.serverRunning() {
		if m.rcon != nil {
			m.rcon.Exec(ctx, "say §cWartung beginnt JETZT — Server geht offline.")
			if _, err := m.rcon.Exec(ctx, "save-all"); err != nil {
				m.log.Warn("save-all vor Wartung fehlgeschlagen", "err", err)
			}
		}
		if id, ok := m.resolve(); ok {
			if err := m.controller.StopContainer(ctx, id); err != nil {
				m.log.Error("wartung: stopp fehlgeschlagen", "err", err)
			} else {
				stopped = true
			}
		}
	}
	if err := m.store.MarkWindow(ctx, w.ID, true, false, stopped); err != nil {
		m.log.Error("wartung: fortschritt speichern fehlgeschlagen", "err", err)
	}
	m.bus.Publish(events.Event{
		Type: events.TypeMaintStart, Severity: events.SevWarn,
		Title:   "🔧 Wartung läuft: " + w.Name,
		Message: fmt.Sprintf("Der Server ist offline — voraussichtlich bis %s Uhr. Meldung folgt, sobald er wieder da ist.", w.End.Format("15:04")),
	})
}

// finish restarts the server (if the window stopped it) and closes the window.
func (m *Manager) finish(ctx context.Context, w storage.MaintenanceWindow) {
	msg := "Der Server war während der Wartung nicht gestoppt — alles läuft normal weiter."
	sev := events.SevInfo
	if w.StoppedServer {
		if id, ok := m.resolve(); ok {
			if err := m.controller.StartContainer(ctx, id); err != nil {
				m.log.Error("wartung: start fehlgeschlagen", "err", err)
				m.bus.Publish(events.Event{
					Type: events.TypeSystemDegraded, Severity: events.SevError,
					Title:   "❌ Server-Start nach Wartung fehlgeschlagen",
					Message: "Der Admin kümmert sich — bitte etwas Geduld.",
					Fields:  []events.Field{{Name: "Details", Value: err.Error()}},
				})
				return // Fenster offen lassen -> nächster Tick versucht es wieder
			}
		}
		// Watchdog: grüne Meldung erst, wenn Minecraft antwortet
		deadline := time.Now().Add(m.OnlineTimeout)
		online := false
		for time.Now().Before(deadline) {
			if m.mcStatus != nil && m.mcStatus().Online {
				online = true
				break
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Second):
			}
		}
		if online {
			msg = "Der Server ist wieder online — viel Spaß!"
			sev = events.SevSuccess
		} else {
			msg = "Der Server wurde gestartet, antwortet aber noch nicht. Der Admin schaut drauf."
			sev = events.SevError
		}
	}
	if err := m.store.MarkWindow(ctx, w.ID, true, true, w.StoppedServer); err != nil {
		m.log.Error("wartung: fortschritt speichern fehlgeschlagen", "err", err)
	}
	delete(m.warned, w.ID)
	m.bus.Publish(events.Event{
		Type: events.TypeMaintEnd, Severity: sev,
		Title:   "✅ Wartung beendet: " + w.Name,
		Message: msg,
	})
}

func (m *Manager) serverRunning() bool {
	for _, c := range m.containers.Containers() {
		if c.Name == m.mcName {
			return c.State == "running"
		}
	}
	return false
}

func (m *Manager) resolve() (string, bool) {
	for _, c := range m.containers.Containers() {
		if c.Name == m.mcName {
			return c.ID, true
		}
	}
	return "", false
}
