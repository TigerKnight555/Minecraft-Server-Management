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

	deadline := time.Now().Add(r.Timeout)
	for {
		if time.Now().After(deadline) {
			return "", fmt.Errorf("backup nach %s nicht fertig — Container %s läuft noch, bitte prüfen", r.Timeout, r.container)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(r.PollStep):
		}
		det, err := r.docker.InspectContainer(ctx, id)
		if err != nil {
			r.log.Warn("backup inspect failed, retrying", "err", err)
			continue
		}
		if det.Running {
			continue
		}
		// finished — der Container könnte von einem früheren Lauf übrig
		// sein; nur Ergebnisse NACH unserem Start zählen
		if det.FinishedAt.Before(started) {
			continue
		}
		dur := det.FinishedAt.Sub(started).Round(time.Second)
		logs, _ := r.docker.TailLogs(ctx, id, 40)
		if det.ExitCode != 0 {
			return "", fmt.Errorf("backup fehlgeschlagen (exit %d nach %s): %s",
				det.ExitCode, dur, tailSummary(logs, 400))
		}
		return fmt.Sprintf("Backup ok in %s — %s", dur, resticSummary(logs)), nil
	}
}

// resticSummary extracts the informative lines from restic output.
func resticSummary(logs string) string {
	var keep []string
	for _, line := range strings.Split(logs, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "processed "),
			strings.HasPrefix(line, "Added to the repo"),
			strings.HasPrefix(line, "snapshot ") && strings.HasSuffix(line, "saved"),
			strings.Contains(line, "packs deleted"),
			strings.HasPrefix(line, "keep "):
			keep = append(keep, line)
		}
	}
	if len(keep) == 0 {
		return "keine restic-Zusammenfassung im Log gefunden"
	}
	return strings.Join(keep, "; ")
}

func tailSummary(logs string, n int) string {
	logs = strings.TrimSpace(logs)
	if len(logs) <= n {
		return logs
	}
	return "…" + logs[len(logs)-n:]
}
