package storage

import (
	"context"
	"database/sql"
	"time"
)

// MaintenanceWindow is one planned offline period (Phase 4.6). Die
// Fortschritts-Flags sind persistiert, damit ein MSM-Neustart (oder der
// nächtliche Host-Reboot) mitten im Fenster keinen Schritt doppelt oder
// vergisst.
type MaintenanceWindow struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	Start         time.Time `json:"start"`
	End           time.Time `json:"end"`
	Started       bool      `json:"started"`       // Stopp-Sequenz gelaufen
	Ended         bool      `json:"ended"`         // Wiederanlauf gelaufen
	StoppedServer bool      `json:"stoppedServer"` // Fenster hat den Server gestoppt (dann startet es ihn auch wieder)
}

func (s *SQLite) migrateMaintenance() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS maintenance_windows (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	name    TEXT NOT NULL,
	start   INTEGER NOT NULL,
	end     INTEGER NOT NULL,
	started INTEGER NOT NULL DEFAULT 0,
	ended   INTEGER NOT NULL DEFAULT 0,
	stopped_server INTEGER NOT NULL DEFAULT 0
);`)
	return err
}

func (s *SQLite) CreateWindow(ctx context.Context, w MaintenanceWindow) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO maintenance_windows(name, start, end) VALUES(?,?,?)`,
		w.Name, w.Start.Unix(), w.End.Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListWindows returns windows that are not yet fully in the past (plus the
// last few finished ones for the history view).
func (s *SQLite) ListWindows(ctx context.Context) ([]MaintenanceWindow, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, start, end, started, ended, stopped_server
FROM maintenance_windows
ORDER BY start DESC LIMIT 20`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MaintenanceWindow
	for rows.Next() {
		var w MaintenanceWindow
		var start, end int64
		if err := rows.Scan(&w.ID, &w.Name, &start, &end, &w.Started, &w.Ended, &w.StoppedServer); err != nil {
			return nil, err
		}
		w.Start, w.End = time.Unix(start, 0), time.Unix(end, 0)
		out = append(out, w)
	}
	return out, rows.Err()
}

// MarkWindow updates the progress flags.
func (s *SQLite) MarkWindow(ctx context.Context, id int64, started, ended, stoppedServer bool) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE maintenance_windows SET started=?, ended=?, stopped_server=? WHERE id=?`,
		started, ended, stoppedServer, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// EndWindowNow moves the end to now (vorzeitig beenden).
func (s *SQLite) EndWindowNow(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE maintenance_windows SET end=? WHERE id=? AND ended=0`, time.Now().Unix(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *SQLite) DeleteWindow(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM maintenance_windows WHERE id=?`, id)
	return err
}
