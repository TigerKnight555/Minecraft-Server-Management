package storage

import (
	"context"
	"encoding/json"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/jvmstats"
)

// lagRetention: Lag-Protokoll so lange wie die Minutenwerte der Graphen.
const lagRetention = 30 * 24 * time.Hour

// Alle Felder eines Lags liegen als JSON in detail; start_ts/end_ts (Unix-
// Sekunden) dienen nur Filter, Sortierung und Prune.
func (s *SQLite) migrateLags() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS lag_events (
	id       INTEGER PRIMARY KEY AUTOINCREMENT,
	start_ts INTEGER NOT NULL,
	end_ts   INTEGER NOT NULL,
	detail   TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_lag_events_start ON lag_events(start_ts);`)
	return err
}

// SaveLag legt einen Lag an (ID 0 → neue ID in ev.ID) oder aktualisiert
// ihn. Der laufende Lag wird jeden Takt gespeichert — ein MSM-Neustart
// mitten im Lag verliert so nichts, der Eintrag behält sein bisheriges Ende.
func (s *SQLite) SaveLag(ctx context.Context, ev *jvmstats.LagEvent) error {
	detail, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if ev.ID == 0 {
		res, err := s.db.ExecContext(ctx,
			`INSERT INTO lag_events(start_ts, end_ts, detail) VALUES(?,?,?)`,
			ev.Start.Unix(), ev.End.Unix(), string(detail))
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		ev.ID = id
		return nil
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE lag_events SET start_ts=?, end_ts=?, detail=? WHERE id=?`,
		ev.Start.Unix(), ev.End.Unix(), string(detail), ev.ID)
	return err
}

// ListLags liefert Lags, die nach since endeten — neueste zuerst.
func (s *SQLite) ListLags(ctx context.Context, since time.Time, limit int) ([]jvmstats.LagEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, detail FROM lag_events WHERE end_ts >= ? ORDER BY start_ts DESC, id DESC LIMIT ?`,
		since.Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []jvmstats.LagEvent{}
	for rows.Next() {
		var id int64
		var detail string
		if err := rows.Scan(&id, &detail); err != nil {
			return nil, err
		}
		var ev jvmstats.LagEvent
		if err := json.Unmarshal([]byte(detail), &ev); err != nil {
			continue // kaputter Eintrag soll das Protokoll nicht sperren
		}
		ev.ID = id
		out = append(out, ev)
	}
	return out, rows.Err()
}
