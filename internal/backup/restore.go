package backup

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Restore stellt einzelne Spielerdateien aus dem restic-Repo wieder her
// (Phase 4.4) — ersetzt restore_player_inventory.sh. Gleiches Muster wie
// das Backup: vordefinierter mc-restore-Container, MSM übergibt den Job als
// Skript über ein geteiltes Verzeichnis (der Socket-Proxy erlaubt kein exec).
type Restore struct {
	docker     Docker
	containers resolver
	container  string // e.g. "mc-restore"
	jobDir     string // shared dir, mounted at /job in the restore container
	log        *slog.Logger

	Timeout  time.Duration
	PollStep time.Duration
}

func NewRestore(docker Docker, containers resolver, container, jobDir string, log *slog.Logger) *Restore {
	return &Restore{
		docker: docker, containers: containers, container: container,
		jobDir: jobDir, log: log,
		Timeout:  15 * time.Minute,
		PollStep: 3 * time.Second,
	}
}

// dashed Mojang-UUID — exakt das Format der playerdata-Dateinamen. Streng
// validiert, weil der Wert in ein Shell-Skript eingesetzt wird.
// ValidUUID reports whether s is a dashed Mojang UUID.
func ValidUUID(s string) bool { return uuidRe.MatchString(strings.TrimSpace(s)) }

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// restoreScript: restores one player file from the newest snapshot.
// Beibehaltevariante des alten Skripts: die aktuelle Datei wird vor dem
// Überschreiben gesichert (.pre-restore-<ts>), nie gelöscht.
//
// Zwei Ablageorte (siehe PlayerDirs): Ziel ist der Ordner, den die Welt
// JETZT benutzt; im Snapshot wird zuerst dasselbe Layout gesucht, dann das
// andere — der Pflicht-Snapshot direkt vor einem MC-Update hat noch
// world/playerdata, die Welt danach schon world/players/data. Umgekehrt
// (Welt auf altem Stand, Snapshot hat beides) gewinnt das alte Layout.
const restoreScript = `set -eu
echo "` + jobMarker + `"
U="%s"
if [ -d /data/world/players/data ]; then D=/data/world/players/data; O=/data/world/playerdata; else D=/data/world/playerdata; O=/data/world/players/data; fi
F="$D/$U.dat"
restic restore latest --target /restore-out --include "/data/world/players/data/$U.dat" --include "/data/world/playerdata/$U.dat"
S=""
for C in "/restore-out$D/$U.dat" "/restore-out$O/$U.dat"; do
  if [ -f "$C" ]; then S="$C"; break; fi
done
if [ -z "$S" ]; then
  echo "FEHLER: Spielerdatei $U.dat ist im letzten Snapshot nicht enthalten"
  exit 3
fi
if [ -f "$F" ]; then
  cp -p "$F" "$F.pre-restore-$(date +%%Y-%%m-%%d_%%H-%%M-%%S)"
  echo "aktuelle Datei gesichert als $F.pre-restore-*"
fi
# -p erhält Besitzer/Rechte aus dem Snapshot — ohne -p gehörte die Datei
# root und der MC-Server (uid 1000) könnte sie nicht mehr lesen! Zusätzlich
# Besitzer an den Spieler-Ordner angleichen, falls der Snapshot selbst
# schon falsche Ownership enthält (busybox: kein chown --reference).
cp -p "$S" "$F"
chown "$(stat -c '%%u:%%g' "$D")" "$F"
echo "wiederhergestellt: $F"
`

func (r *Restore) resolve() (string, error) {
	if r.containers != nil {
		for _, c := range r.containers.Containers() {
			if c.Name == r.container || c.ID == r.container {
				return c.ID, nil
			}
		}
	}
	if r.container != "" {
		return r.container, nil
	}
	return "", fmt.Errorf("kein Restore-Container konfiguriert")
}

// RestorePlayer restores <uuid>.dat (players/data oder playerdata) from the latest snapshot.
// Der Spieler muss offline sein — sonst überschreibt der Server die Datei
// beim nächsten Speichern wieder (Verantwortung des Aufrufers/UI-Hinweis).
func (r *Restore) RestorePlayer(ctx context.Context, uuid string) (string, error) {
	uuid = strings.TrimSpace(uuid)
	if !uuidRe.MatchString(uuid) {
		return "", fmt.Errorf("ungültige Spieler-UUID %q (erwartet Format mit Bindestrichen, z. B. 069a79f4-44e9-4726-a5be-fca90e38aaf5)", uuid)
	}
	id, err := r.resolve()
	if err != nil {
		return "", err
	}
	if det, err := r.docker.InspectContainer(ctx, id); err == nil && det.Running {
		return "", fmt.Errorf("restore läuft bereits (Container %s aktiv)", r.container)
	}

	script := fmt.Sprintf(restoreScript, uuid)
	if err := os.WriteFile(filepath.Join(r.jobDir, "run.sh"), []byte(script), 0o644); err != nil {
		return "", fmt.Errorf("job-Skript schreiben (%s): %w", r.jobDir, err)
	}

	started := time.Now()
	if err := r.docker.StartContainer(ctx, id); err != nil {
		return "", fmt.Errorf("restore-container starten: %w", err)
	}
	det, err := superviseJob(ctx, r.docker, r.log, id, started, r.Timeout, r.PollStep)
	if err != nil {
		return "", fmt.Errorf("restore %w", err)
	}
	dur := det.FinishedAt.Sub(started).Round(time.Second)
	logs, _ := r.docker.TailLogs(ctx, id, 40)
	logs = currentJob(logs)
	if det.ExitCode != 0 {
		return "", fmt.Errorf("restore fehlgeschlagen (exit %d nach %s): %s",
			det.ExitCode, dur, tailSummary(logs, 400))
	}
	return fmt.Sprintf("Spielerdaten %s in %s wiederhergestellt (Original als .pre-restore-* gesichert)", uuid, dur), nil
}

// jobMarker leitet jeden Restore-Lauf im Container-Log ein. Der Container
// wird wiederverwendet, sein Log enthält also auch frühere Läufe — ohne
// Schnitt zeigte eine Fehlermeldung die Ausgabe des vorigen Laufs mit
// (WSL-Test 2026-11-10).
const jobMarker = "### msm-restore-job"

// currentJob returns the log part after the last job marker.
func currentJob(logs string) string {
	if i := strings.LastIndex(logs, jobMarker); i >= 0 {
		return strings.TrimLeft(logs[i+len(jobMarker):], "\r\n")
	}
	return logs
}
