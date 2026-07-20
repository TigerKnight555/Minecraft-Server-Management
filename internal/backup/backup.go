// Package backup supervises the restic snapshot container (Phase 4.3). The
// socket proxy only permits start/stop/restart — no create, no exec. The
// restic container is therefore a pre-created compose service (restart:
// "no"); MSM merely starts it and watches the exit code.
//
// Konsistenz-Entscheidung (Nutzer, 2026-07-18): das Backup läuft bei
// GESTOPPTEM Minecraft-Server — kein save-off/flush-Verfahren. Stop →
// Snapshot → Start übernimmt der Scheduler als Schrittkette (Routine-Typ
// "backup"); der integrierte Start ersetzt zugleich den nächtlichen
// Neustart. Dieses Paket kennt daher weder RCON noch den MC-Server.
package backup

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/collector"
)

// Docker is the container access the runner needs (dockerclient satisfies it).
type Docker interface {
	StartContainer(ctx context.Context, id string) error
	InspectContainer(ctx context.Context, id string) (collector.ContainerDetail, error)
	TailLogs(ctx context.Context, id string, tail int) (string, error)
}

// resolver maps container names to IDs (the collector already knows them).
type resolver interface {
	Containers() []collector.Container
}

type Runner struct {
	docker     Docker
	containers resolver
	container  string // backup container name, e.g. "mc-backup"
	log        *slog.Logger

	// Timeout/PollStep are exported for tests; defaults set in New.
	Timeout  time.Duration
	PollStep time.Duration
}

func New(docker Docker, containers resolver, container string, log *slog.Logger) *Runner {
	return &Runner{
		docker: docker, containers: containers, container: container, log: log,
		Timeout:  60 * time.Minute,
		PollStep: 5 * time.Second,
	}
}

func (r *Runner) resolve() (string, error) {
	if r.containers != nil {
		for _, c := range r.containers.Containers() {
			if c.Name == r.container || c.ID == r.container {
				return c.ID, nil
			}
		}
	}
	if r.container != "" {
		return r.container, nil // docker accepts names too
	}
	return "", fmt.Errorf("kein Backup-Container konfiguriert")
}

// Run starts the snapshot container, waits for it to finish and returns a
// human-readable summary. The caller is responsible for stopping/starting
// the Minecraft server around it.
func (r *Runner) Run(ctx context.Context) (string, error) {
	id, err := r.resolve()
	if err != nil {
		return "", err
	}

	// never start a second run while one is in flight
	if det, err := r.docker.InspectContainer(ctx, id); err == nil && det.Running {
		return "", fmt.Errorf("backup läuft bereits (Container %s aktiv)", r.container)
	}

	started := time.Now()
	if err := r.docker.StartContainer(ctx, id); err != nil {
		return "", fmt.Errorf("backup-container starten: %w", err)
	}
	det, err := superviseJob(ctx, r.docker, r.log, id, started, r.Timeout, r.PollStep)
	if err != nil {
		return "", fmt.Errorf("backup %w", err)
	}
	dur := det.FinishedAt.Sub(started).Round(time.Second)
	logs, _ := r.docker.TailLogs(ctx, id, 40)
	if det.ExitCode != 0 {
		return "", fmt.Errorf("backup fehlgeschlagen (exit %d nach %s): %s",
			det.ExitCode, dur, tailSummary(logs, 400))
	}
	return fmt.Sprintf("Backup ok in %s — %s", dur, resticSummary(logs)), nil
}

// superviseJob waits until a started job container exits and returns its
// final state. Only results finishing AFTER `started` count — the container
// could be a leftover from an earlier run.
func superviseJob(ctx context.Context, docker Docker, log *slog.Logger,
	id string, started time.Time, timeout, poll time.Duration) (collector.ContainerDetail, error) {
	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return collector.ContainerDetail{}, fmt.Errorf("nach %s nicht fertig — Container läuft noch, bitte prüfen", timeout)
		}
		select {
		case <-ctx.Done():
			return collector.ContainerDetail{}, ctx.Err()
		case <-time.After(poll):
		}
		det, err := docker.InspectContainer(ctx, id)
		if err != nil {
			log.Warn("job inspect failed, retrying", "err", err)
			continue
		}
		if det.Running || det.FinishedAt.Before(started) {
			continue
		}
		return det, nil
	}
}

// resticSummary translates restic's output into one plain German line —
// die rohen Zeilen ("processed 7626 files…", "keep 2 snapshots:") waren im
// Discord-Embed für Spieler unverständlich.
var (
	reProcessed = regexp.MustCompile(`processed (\d+) files, ([\d.]+ \w+) in ([\d:]+)`)
	reAdded     = regexp.MustCompile(`Added to the repo\w*: ([\d.]+ \w+)`)
	reSnapshot  = regexp.MustCompile(`snapshot (\w+) saved`)
)

func resticSummary(logs string) string {
	var parts []string
	if m := reProcessed.FindStringSubmatch(logs); m != nil {
		parts = append(parts, fmt.Sprintf("%s geprüft (%s Dateien, %s min)", m[2], m[1], m[3]))
	}
	if m := reAdded.FindStringSubmatch(logs); m != nil {
		parts = append(parts, m[1]+" neu gesichert")
	}
	if m := reSnapshot.FindStringSubmatch(logs); m != nil {
		parts = append(parts, "Stand "+m[1])
	}
	if len(parts) == 0 {
		return "keine restic-Zusammenfassung im Log gefunden"
	}
	return strings.Join(parts, ", ")
}

func tailSummary(logs string, n int) string {
	logs = strings.TrimSpace(logs)
	if len(logs) <= n {
		return logs
	}
	return "…" + logs[len(logs)-n:]
}
