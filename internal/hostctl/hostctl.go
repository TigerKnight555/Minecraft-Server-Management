// Package hostctl implements the host-reboot mechanism (Phase 4.5). MSM
// selbst hat KEINE Host-Rechte — es schreibt nur eine Signaldatei auf ein
// gemeinsames Verzeichnis. Ein winziger systemd-Watcher auf dem Host (siehe
// deploy/host-watcher/) reagiert darauf; seine einzige Fähigkeit ist der
// Reboot. Nach dem Boot stellt der Reconciler den persistierten Soll-Zustand
// der Container her und meldet „wieder online" — bleibt die Meldung aus, IST
// das der Alarm (Konzept: Routinen & Wartungsfenster).
package hostctl

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/collector"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/storage"
)

// Signaler writes request files for the host watcher.
type Signaler struct {
	dir string
}

func NewSignaler(dir string) *Signaler { return &Signaler{dir: dir} }

// RequestReboot signals the host watcher. Der Dateiinhalt ist nur Doku —
// der Watcher reagiert auf die Existenz (und ignoriert veraltete Dateien).
func (s *Signaler) RequestReboot() error {
	if s.dir == "" {
		return fmt.Errorf("kein Signal-Verzeichnis konfiguriert (MSM_HOST_SIGNAL_DIR)")
	}
	path := filepath.Join(s.dir, "reboot.request")
	content := "MSM Reboot-Anforderung " + time.Now().Format(time.RFC3339) + "\n"
	if err := os.WriteFile(path, []byte(content), 0o664); err != nil {
		return fmt.Errorf("signaldatei schreiben (%s): %w — Host-Watcher installiert und Verzeichnis g+w?", path, err)
	}
	return nil
}

// DesiredStore is the persistence subset the reconciler needs
// (storage.SQLite and mock.Store satisfy it).
type DesiredStore interface {
	ListDesiredStates(ctx context.Context) ([]storage.DesiredState, error)
}

type resolver interface {
	Containers() []collector.Container
}

// Reconciler enforces the desired state after MSM starts and, following a
// fresh host boot, reports "wieder online" once the Minecraft server
// answers — oder schlägt hörbar Alarm, wenn nicht.
type Reconciler struct {
	store      DesiredStore
	controller collector.ContainerController
	containers resolver
	mcStatus   func() collector.MCStatus
	host       func() collector.HostSample
	bus        *events.Bus
	mcName     string
	log        *slog.Logger

	// tunables (tests shrink them)
	Warmup         time.Duration // wait for collector/docker after start
	FreshBootMax   time.Duration // uptime below this = "gerade gebootet"
	OnlineTimeout  time.Duration // watchdog for the online message
	OnlinePollStep time.Duration
}

func NewReconciler(store DesiredStore, controller collector.ContainerController, containers resolver,
	mcStatus func() collector.MCStatus, host func() collector.HostSample,
	bus *events.Bus, mcName string, log *slog.Logger) *Reconciler {
	return &Reconciler{
		store: store, controller: controller, containers: containers,
		mcStatus: mcStatus, host: host, bus: bus, mcName: mcName, log: log,
		Warmup:         45 * time.Second,
		FreshBootMax:   15 * time.Minute,
		OnlineTimeout:  10 * time.Minute,
		OnlinePollStep: 10 * time.Second,
	}
}

// Run executes one reconciliation pass (call as goroutine at MSM start).
func (r *Reconciler) Run(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(r.Warmup):
	}
	freshBoot := r.host != nil && r.host().UptimeSec > 0 &&
		time.Duration(r.host().UptimeSec)*time.Second < r.FreshBootMax

	states, err := r.store.ListDesiredStates(ctx)
	if err != nil {
		r.log.Error("soll-zustand laden fehlgeschlagen", "err", err)
		return
	}
	byName := map[string]collector.Container{}
	for _, c := range r.containers.Containers() {
		byName[c.Name] = c
	}

	mcDesiredRunning := true // ohne expliziten Eintrag gilt: soll laufen
	for _, want := range states {
		if want.Container == r.mcName && want.State == "stopped" {
			mcDesiredRunning = false
		}
		actual, known := byName[want.Container]
		if !known {
			continue
		}
		switch {
		case want.State == "running" && actual.State != "running":
			r.log.Info("soll-zustand: starte container", "container", want.Container)
			if err := r.controller.StartContainer(ctx, actual.ID); err != nil {
				r.log.Error("soll-zustand start fehlgeschlagen", "container", want.Container, "err", err)
			}
		case want.State == "stopped" && actual.State == "running":
			// bewusst gestoppt bleibt gestoppt — auch wenn Dockers
			// restart-Policy ihn nach dem Boot hochgezogen hat
			r.log.Info("soll-zustand: stoppe container", "container", want.Container)
			if err := r.controller.StopContainer(ctx, actual.ID); err != nil {
				r.log.Error("soll-zustand stopp fehlgeschlagen", "container", want.Container, "err", err)
			}
		}
	}

	if !freshBoot {
		return // MSM-Redeploy o. Ä. — Abgleich ja, Boot-Meldung nein
	}
	if !mcDesiredRunning {
		r.bus.Publish(events.Event{
			Type: events.TypeSystemOnline, Severity: events.SevInfo,
			Title:   "🔄 System neu gestartet",
			Message: "Der Minecraft-Server bleibt gestoppt (war vorher bewusst ausgeschaltet).",
		})
		return
	}
	// Watchdog: die Online-Meldung kommt erst, wenn Minecraft antwortet
	deadline := time.Now().Add(r.OnlineTimeout)
	for time.Now().Before(deadline) {
		if st := r.mcStatus(); st.Online {
			r.bus.Publish(events.Event{
				Type: events.TypeSystemOnline, Severity: events.SevSuccess,
				Title:   "✅ Server ist wieder online!",
				Message: "Neustart abgeschlossen — es kann weitergehen.",
			})
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(r.OnlinePollStep):
		}
	}
	r.bus.Publish(events.Event{
		Type: events.TypeSystemDegraded, Severity: events.SevError,
		Title:   "❌ Server nach Neustart nicht erreichbar",
		Message: fmt.Sprintf("Der Rechner läuft wieder, aber Minecraft antwortet seit %s nicht. Der Admin kümmert sich — bitte etwas Geduld.", r.OnlineTimeout),
	})
}
