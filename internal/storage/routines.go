package storage

import (
	"context"
	"database/sql"
	"time"
)

// Routine is a scheduled action (scheduler stage 1). Kinds:
//
//	rcon             — run Payload as an RCON command
//	restart          — restart the container named in Payload
//	announce-restart — warn players via RCON over WarnMinutes, then restart
type Routine struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Cron        string `json:"cron"` // standard 5-field cron expression
	Kind        string `json:"kind"`
	Payload     string `json:"payload"`
	WarnMinutes int    `json:"warnMinutes"`
	Enabled     bool   `json:"enabled"`
}

// RoutineRun is one execution record. Routines must never fail silently
// (concept invariant) — every run lands here, success or not.
type RoutineRun struct {
	ID        int64     `json:"id"`
	RoutineID int64     `json:"routineId"`
	Time      time.Time `json:"time"`
	OK        bool      `json:"ok"`
	Message   string    `json:"message"`
}

func (s *SQLite) migrateRoutines() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS routines (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	name    TEXT NOT NULL,
	cron    TEXT NOT NULL,
	kind    TEXT NOT NULL,
	payload TEXT NOT NULL DEFAULT '',
	warn_minutes INTEGER NOT NULL DEFAULT 0,
	enabled INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE IF NOT EXISTS routine_runs (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	routine_id INTEGER NOT NULL,
	ts         INTEGER NOT NULL,
	ok         INTEGER NOT NULL,
	message    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_runs_routine ON routine_runs(routine_id, ts);
`)
	return err
}

func (s *SQLite) ListRoutines(ctx context.Context) ([]Routine, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, cron, kind, payload, warn_minutes, enabled FROM routines ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Routine
	for rows.Next() {
		var r Routine
		if err := rows.Scan(&r.ID, &r.Name, &r.Cron, &r.Kind, &r.Payload, &r.WarnMinutes, &r.Enabled); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *SQLite) GetRoutine(ctx context.Context, id int64) (Routine, error) {
	var r Routine
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, cron, kind, payload, warn_minutes, enabled FROM routines WHERE id = ?`, id).
		Scan(&r.ID, &r.Name, &r.Cron, &r.Kind, &r.Payload, &r.WarnMinutes, &r.Enabled)
	return r, err
}

func (s *SQLite) CreateRoutine(ctx context.Context, r Routine) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO routines(name, cron, kind, payload, warn_minutes, enabled) VALUES(?,?,?,?,?,?)`,
		r.Name, r.Cron, r.Kind, r.Payload, r.WarnMinutes, r.Enabled)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *SQLite) UpdateRoutine(ctx context.Context, r Routine) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE routines SET name=?, cron=?, kind=?, payload=?, warn_minutes=?, enabled=? WHERE id=?`,
		r.Name, r.Cron, r.Kind, r.Payload, r.WarnMinutes, r.Enabled, r.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *SQLite) DeleteRoutine(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM routines WHERE id = ?`, id)
	return err
}

func (s *SQLite) RecordRun(ctx context.Context, run RoutineRun) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO routine_runs(routine_id, ts, ok, message) VALUES(?,?,?,?)`,
		run.RoutineID, run.Time.Unix(), run.OK, run.Message)
	return err
}

func (s *SQLite) RecentRuns(ctx context.Context, limit int) ([]RoutineRun, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, routine_id, ts, ok, message FROM routine_runs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RoutineRun
	for rows.Next() {
		var r RoutineRun
		var ts int64
		if err := rows.Scan(&r.ID, &r.RoutineID, &ts, &r.OK, &r.Message); err != nil {
			return nil, err
		}
		r.Time = time.Unix(ts, 0)
		out = append(out, r)
	}
	return out, rows.Err()
}
