package storage

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// Routine is a scheduled action. Kinds:
//
//	rcon             — run Payload as an RCON command
//	restart          — restart the container named in Payload
//	announce-restart — warn players via RCON over WarnMinutes, then restart
//
// The stage-2 fields (conditions, staged updates, watchdog) only apply to
// announce-restart; everything defaults to off so stage-1 routines behave
// exactly as before.
type Routine struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Cron        string `json:"cron"` // standard 5-field cron expression
	Kind        string `json:"kind"`
	Payload     string `json:"payload"`
	WarnMinutes int    `json:"warnMinutes"`
	Enabled     bool   `json:"enabled"`

	// Stufe-2-Bedingungen (Konzept: Routinen & Wartungsfenster)
	SkipIfPlayersOnline bool   `json:"skipIfPlayersOnline"` // ganz überspringen, wenn Spieler online
	WaitForEmpty        bool   `json:"waitForEmpty"`        // auf leeren Server warten …
	WaitDeadline        string `json:"waitDeadline"`        // … höchstens bis "HH:MM" (leer = 60 min)
	ApplyStaged         bool   `json:"applyStaged"`         // gestagte Mod-Updates beim Neustart einspielen
	WatchdogMinutes     int    `json:"watchdogMinutes"`     // nach Start auf Online warten (0 = aus)
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
	if err != nil {
		return err
	}
	// Phase-4.2-Spalten: ALTER TABLE schlägt fehl, wenn die Spalte schon
	// existiert — das ist der "Migration schon gelaufen"-Normalfall.
	for _, col := range []string{
		`ALTER TABLE routines ADD COLUMN skip_if_players INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE routines ADD COLUMN wait_for_empty INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE routines ADD COLUMN wait_deadline TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE routines ADD COLUMN apply_staged INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE routines ADD COLUMN watchdog_minutes INTEGER NOT NULL DEFAULT 0`,
	} {
		if _, err := s.db.Exec(col); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}
	return nil
}

const routineCols = `id, name, cron, kind, payload, warn_minutes, enabled,
	skip_if_players, wait_for_empty, wait_deadline, apply_staged, watchdog_minutes`

func scanRoutine(scan func(...any) error) (Routine, error) {
	var r Routine
	err := scan(&r.ID, &r.Name, &r.Cron, &r.Kind, &r.Payload, &r.WarnMinutes, &r.Enabled,
		&r.SkipIfPlayersOnline, &r.WaitForEmpty, &r.WaitDeadline, &r.ApplyStaged, &r.WatchdogMinutes)
	return r, err
}

func (s *SQLite) ListRoutines(ctx context.Context) ([]Routine, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+routineCols+` FROM routines ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Routine
	for rows.Next() {
		r, err := scanRoutine(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *SQLite) GetRoutine(ctx context.Context, id int64) (Routine, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+routineCols+` FROM routines WHERE id = ?`, id)
	return scanRoutine(row.Scan)
}

func (s *SQLite) CreateRoutine(ctx context.Context, r Routine) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO routines(name, cron, kind, payload, warn_minutes, enabled,
			skip_if_players, wait_for_empty, wait_deadline, apply_staged, watchdog_minutes)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		r.Name, r.Cron, r.Kind, r.Payload, r.WarnMinutes, r.Enabled,
		r.SkipIfPlayersOnline, r.WaitForEmpty, r.WaitDeadline, r.ApplyStaged, r.WatchdogMinutes)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *SQLite) UpdateRoutine(ctx context.Context, r Routine) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE routines SET name=?, cron=?, kind=?, payload=?, warn_minutes=?, enabled=?,
			skip_if_players=?, wait_for_empty=?, wait_deadline=?, apply_staged=?, watchdog_minutes=?
		 WHERE id=?`,
		r.Name, r.Cron, r.Kind, r.Payload, r.WarnMinutes, r.Enabled,
		r.SkipIfPlayersOnline, r.WaitForEmpty, r.WaitDeadline, r.ApplyStaged, r.WatchdogMinutes, r.ID)
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

// LastOKRunForKind returns when a routine of the given kind last succeeded
// (found=false if never). Skipped runs count as OK in the history but not
// as a real execution here.
func (s *SQLite) LastOKRunForKind(ctx context.Context, kind string) (time.Time, bool, error) {
	var ts sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
SELECT MAX(rr.ts) FROM routine_runs rr
JOIN routines r ON r.id = rr.routine_id
WHERE r.kind = ? AND rr.ok = 1 AND rr.message NOT LIKE 'übersprungen%'`, kind).Scan(&ts)
	if err != nil {
		return time.Time{}, false, err
	}
	if !ts.Valid {
		return time.Time{}, false, nil
	}
	return time.Unix(ts.Int64, 0), true, nil
}

// HasEnabledRoutineKind reports whether any enabled routine of the kind exists.
func (s *SQLite) HasEnabledRoutineKind(ctx context.Context, kind string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM routines WHERE kind = ? AND enabled = 1`, kind).Scan(&n)
	return n > 0, err
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
