package backup

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
)

// FreshnessStore is the storage subset the watcher needs (storage.SQLite
// satisfies it).
type FreshnessStore interface {
	LastOKRunForKind(ctx context.Context, kind string) (time.Time, bool, error)
	HasEnabledRoutineKind(ctx context.Context, kind string) (bool, error)
}

// WatchFreshness warns (via bus) when backups go stale: an enabled backup
// routine exists, but no successful run within maxAge. Checks hourly, warns
// at most once per day — ein Dauerticker wäre nur Alarm-Müdigkeit.
func WatchFreshness(ctx context.Context, store FreshnessStore, bus *events.Bus, maxAge time.Duration, log *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	var lastWarned time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		enabled, err := store.HasEnabledRoutineKind(ctx, "backup")
		if err != nil || !enabled {
			continue // keine Backup-Routine eingerichtet -> nichts zu überwachen
		}
		last, found, err := store.LastOKRunForKind(ctx, "backup")
		if err != nil {
			log.Error("freshness check failed", "err", err)
			continue
		}
		fresh := found && time.Since(last) <= maxAge
		if fresh || time.Since(lastWarned) < 24*time.Hour {
			continue
		}
		lastWarned = time.Now()
		msg := "Es gibt noch keinen erfolgreichen Backup-Lauf."
		if found {
			msg = fmt.Sprintf("Letztes erfolgreiches Backup: %s (vor %s).",
				last.Format("2006-01-02 15:04"), time.Since(last).Round(time.Hour))
		}
		bus.Publish(events.Event{
			Type: events.TypeBackupStale, Severity: events.SevWarn,
			Title:   "⏰ Backup überfällig",
			Message: msg + " Info für den Admin: Routine und NAS prüfen.",
		})
	}
}
