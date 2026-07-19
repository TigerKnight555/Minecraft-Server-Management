// Package upgrade orchestrates the one-click Minecraft version bump: der
// Nutzer klickt im Dashboard, MSM erledigt die komplette Kette. Der einzige
// privilegierte Schritt (MC_VERSION setzen + Container neu erstellen) läuft
// über den Host-Helfer (Signaldatei, deploy/host-watcher/) — MSM darf keine
// Container erstellen (Socket-Proxy-Vertrauensschnitt).
//
// Kette: Guards (bereit? nichts anderes läuft?) → Spieler-Warnungen →
// save-all → Stop → Pflicht-Backup (Rollback-Punkt — das Welt-Upgrade ist
// UNUMKEHRBAR) → Server-Mods für die Zielversion stagen+einspielen →
// upgrade.request → Host erstellt mc-fabric neu (itzg lädt Server-JAR +
// frischen Fabric-Loader) → Watchdog bis „online mit Zielversion" →
// Client-Mods nachziehen → Erfolgsmeldung.
package upgrade

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/collector"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mods"
)

type resolver interface {
	Containers() []collector.Container
}

// BackupRunner runs one restic snapshot (backup.Runner satisfies this).
type BackupRunner interface {
	Run(ctx context.Context) (string, error)
}

// UpgradeSignaler writes the host request (hostctl.Signaler satisfies this).
type UpgradeSignaler interface {
	RequestUpgrade(version string) error
}

// ModManager is the mods surface the chain needs (mods.Manager satisfies it).
type ModManager interface {
	CheckUpdates(ctx context.Context, profile, mcVersion string) ([]mods.Entry, error)
	Stage(ctx context.Context, profile string, filenames []string) (int, error)
	ApplyStaged(profile string) (string, int, error)
}

// Readiness liefert den letzten Versions-Watch (mods.Watcher satisfies it).
type Readiness interface {
	Last() *mods.WatchStatus
}

type Orchestrator struct {
	rcon       collector.RCONClient
	controller collector.ContainerController
	containers resolver
	mcStatus   func() collector.MCStatus
	backup     BackupRunner
	modmgr     ModManager
	watch      Readiness
	signal     UpgradeSignaler
	bus        *events.Bus
	mcName     string
	log        *slog.Logger

	WarnMinutes   int
	WarnStep      time.Duration
	OnlineTimeout time.Duration // Welt-Upgrade beim ersten Start kann dauern
	PollStep      time.Duration

	mu      sync.Mutex
	running bool
	status  string
}

func New(rcon collector.RCONClient, controller collector.ContainerController, containers resolver,
	mcStatus func() collector.MCStatus, backup BackupRunner, modmgr ModManager,
	watch Readiness, signal UpgradeSignaler, bus *events.Bus, mcName string, log *slog.Logger) *Orchestrator {
	return &Orchestrator{
		rcon: rcon, controller: controller, containers: containers,
		mcStatus: mcStatus, backup: backup, modmgr: modmgr,
		watch: watch, signal: signal, bus: bus, mcName: mcName, log: log,
		WarnMinutes:   5,
		WarnStep:      time.Minute,
		OnlineTimeout: 25 * time.Minute,
		PollStep:      10 * time.Second,
	}
}

// Status returns the current progress line ("" = kein Upgrade aktiv).
func (o *Orchestrator) Status() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.status
}

func (o *Orchestrator) setStatus(s string) {
	o.mu.Lock()
	o.status = s
	o.mu.Unlock()
	if s != "" {
		o.log.Info("upgrade", "status", s)
	}
}

// Start validates the request and launches the chain in the background.
func (o *Orchestrator) Start(version string) error {
	o.mu.Lock()
	if o.running {
		o.mu.Unlock()
		return fmt.Errorf("es läuft bereits ein Upgrade (%s)", o.status)
	}
	// Guards VOR dem Start — der Button darf nur Grünes auslösen
	last := o.watch.Last()
	if last == nil || !last.NewerAvailable {
		o.mu.Unlock()
		return fmt.Errorf("kein Update verfügbar (Versions-Watch lief noch nicht?)")
	}
	if version != last.LatestVersion {
		o.mu.Unlock()
		return fmt.Errorf("angefragte Version %q ist nicht die geprüfte Zielversion %q", version, last.LatestVersion)
	}
	if !last.LoaderReady {
		o.mu.Unlock()
		return fmt.Errorf("fabric-Loader unterstützt %s noch nicht", version)
	}
	for _, p := range last.Profiles {
		if p.Profile == "server" && (p.Total == 0 || p.Ready < p.Total) {
			o.mu.Unlock()
			return fmt.Errorf("noch nicht alle Server-Mods für %s bereit (%d/%d)", version, p.Ready, p.Total)
		}
	}
	o.running = true
	o.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Minute)
		defer cancel()
		defer func() {
			o.mu.Lock()
			o.running = false
			o.status = ""
			o.mu.Unlock()
		}()
		if err := o.run(ctx, version); err != nil {
			o.log.Error("upgrade fehlgeschlagen", "version", version, "err", err)
			o.bus.Publish(events.Event{
				Type: events.TypeUpgradeFailed, Severity: events.SevError,
				Title:   "⚠️ Minecraft-Update auf " + version + " fehlgeschlagen",
				Message: "Der Admin kümmert sich — Details unten.",
				Fields:  []events.Field{{Name: "Details", Value: err.Error()}},
			})
		}
	}()
	return nil
}

func (o *Orchestrator) run(ctx context.Context, version string) error {
	o.bus.Publish(events.Event{
		Type: events.TypeUpgradeStart, Severity: events.SevWarn,
		Title:   "⬆️ Minecraft-Update auf " + version + " startet",
		Message: fmt.Sprintf("Der Server geht gleich für das Update offline (Warnung läuft, ca. %d min). Meldung folgt, sobald alles fertig ist.", o.WarnMinutes),
	})

	// 1. Spieler warnen (nur wenn der Server läuft)
	id, running := o.resolve()
	if running && o.rcon != nil {
		o.setStatus("warne Spieler")
		for m := o.WarnMinutes; m >= 1; m-- {
			o.rcon.Exec(ctx, fmt.Sprintf("say §cServer-Update auf %s in %d Minute(n)!", version, m))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(o.WarnStep):
			}
		}
		o.rcon.Exec(ctx, "say §cUpdate beginnt JETZT — bis gleich!")
		if _, err := o.rcon.Exec(ctx, "save-all"); err != nil {
			o.log.Warn("save-all vor Upgrade fehlgeschlagen", "err", err)
		}
	}

	// 2. Stoppen
	if running {
		o.setStatus("stoppe Server")
		if err := o.controller.StopContainer(ctx, id); err != nil {
			return fmt.Errorf("stop: %w", err)
		}
	}
	restartOld := func(reason error) error {
		if startErr := o.controller.StartContainer(ctx, id); startErr != nil {
			return fmt.Errorf("%v; Start des alten Servers AUCH fehlgeschlagen: %w", reason, startErr)
		}
		return fmt.Errorf("%w (Server läuft wieder auf der alten Version)", reason)
	}

	// 3. Pflicht-Backup — der Rollback-Punkt, das Welt-Upgrade ist unumkehrbar
	o.setStatus("erstelle Backup (Rollback-Punkt)")
	if o.backup == nil {
		return restartOld(fmt.Errorf("kein Backup-Runner verdrahtet — Upgrade ohne Backup ist tabu"))
	}
	if _, err := o.backup.Run(ctx); err != nil {
		return restartOld(fmt.Errorf("backup fehlgeschlagen — Upgrade abgebrochen: %w", err))
	}

	// 4. Server-Mods für die Zielversion einspielen (Server ist gestoppt)
	o.setStatus("aktualisiere Server-Mods für " + version)
	if _, err := o.modmgr.CheckUpdates(ctx, "server", version); err != nil {
		return restartOld(fmt.Errorf("mod-Check für %s: %w", version, err))
	}
	if _, err := o.modmgr.Stage(ctx, "server", nil); err != nil {
		return restartOld(fmt.Errorf("mods stagen: %w", err))
	}
	if _, n, err := o.modmgr.ApplyStaged("server"); err != nil && !errors.Is(err, mods.ErrNothingStaged) {
		return restartOld(fmt.Errorf("mods einspielen: %w", err))
	} else if err == nil {
		o.log.Info("server-mods für zielversion eingespielt", "count", n)
	}

	// 5. Host-Helfer: MC_VERSION setzen + Container neu erstellen
	o.setStatus("erstelle Server mit " + version + " neu (Host-Helfer)")
	if err := o.signal.RequestUpgrade(version); err != nil {
		return restartOld(err)
	}

	// 6. Watchdog: online UND Zielversion — das erste Hochfahren macht das
	// Welt-Upgrade und darf dauern
	o.setStatus("warte auf Server mit " + version + " (Welt-Upgrade kann dauern)")
	deadline := time.Now().Add(o.OnlineTimeout)
	for time.Now().Before(deadline) {
		if st := o.mcStatus(); st.Online && st.Version == version {
			return o.finish(ctx, version)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(o.PollStep):
		}
	}
	return fmt.Errorf("server meldete sich nach %s nicht mit Version %s — bitte Logs prüfen (Welt-Upgrade kann bei großen Welten länger dauern; der Watchdog gibt nur die Meldung auf, der Server startet ggf. trotzdem fertig)", o.OnlineTimeout, version)
}

// finish updates the client profile and celebrates.
func (o *Orchestrator) finish(ctx context.Context, version string) error {
	o.setStatus("aktualisiere Client-Mods")
	clientNote := ""
	if _, err := o.modmgr.CheckUpdates(ctx, "client", version); err == nil {
		if _, err := o.modmgr.Stage(ctx, "client", nil); err == nil {
			if _, n, err := o.modmgr.ApplyStaged("client"); err == nil {
				clientNote = fmt.Sprintf(" %d Client-Mods wurden aktualisiert — neues Mod-Paket folgt.", n)
			} else if !errors.Is(err, mods.ErrNothingStaged) {
				clientNote = " (Client-Mods konnten nicht automatisch aktualisiert werden — siehe Mods-Tab.)"
			}
		}
	}
	o.bus.Publish(events.Event{
		Type: events.TypeUpgradeOK, Severity: events.SevSuccess,
		Title:   "🎉 Server läuft jetzt auf Minecraft " + version + "!",
		Message: "Update fertig, es kann weitergehen." + clientNote,
	})
	return nil
}

func (o *Orchestrator) resolve() (id string, running bool) {
	for _, c := range o.containers.Containers() {
		if c.Name == o.mcName {
			return c.ID, c.State == "running"
		}
	}
	return o.mcName, false
}
