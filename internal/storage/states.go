package storage

import (
	"context"
	"time"
)

// DesiredState is the persisted intent for one container: "running" or
// "stopped". Nur explizite Nutzer-Aktionen (Start/Stopp im Dashboard)
// setzen ihn — temporäre Stopps durch Routinen (Backup, Neustart) nicht.
// Der Boot-Abgleich stellt nach jedem Host-Reboot genau diesen Zustand her.
type DesiredState struct {
	Container string    `json:"container"`
	State     string    `json:"state"` // "running" | "stopped"
	Updated   time.Time `json:"updated"`
}

func (s *SQLite) migrateStates() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS desired_state (
	container TEXT PRIMARY KEY,
	state     TEXT NOT NULL,
	updated   INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS app_state (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);`)
	return err
}

// GetAppState returns a small persisted key/value ("" if unset) — z. B.
// welche MC-Version schon angekündigt wurde (übersteht den Nacht-Reboot).
func (s *SQLite) GetAppState(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM app_state WHERE key=?`, key).Scan(&v)
	if err != nil {
		return "", nil // fehlender Key ist kein Fehler
	}
	return v, nil
}

func (s *SQLite) SetAppState(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO app_state(key, value) VALUES(?,?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

func (s *SQLite) SetDesiredState(ctx context.Context, container, state string) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO desired_state(container, state, updated) VALUES(?,?,?)
ON CONFLICT(container) DO UPDATE SET state=excluded.state, updated=excluded.updated`,
		container, state, time.Now().Unix())
	return err
}

func (s *SQLite) ListDesiredStates(ctx context.Context) ([]DesiredState, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT container, state, updated FROM desired_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DesiredState
	for rows.Next() {
		var d DesiredState
		var ts int64
		if err := rows.Scan(&d.Container, &d.State, &ts); err != nil {
			return nil, err
		}
		d.Updated = time.Unix(ts, 0)
		out = append(out, d)
	}
	return out, rows.Err()
}
