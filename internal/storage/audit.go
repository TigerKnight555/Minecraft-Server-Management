package storage

import (
	"context"
	"time"
)

// AuditEntry is one logged admin action (concept chapter 9: every action is
// recorded).
type AuditEntry struct {
	ID     int64     `json:"id"`
	Time   time.Time `json:"time"`
	Action string    `json:"action"` // e.g. "container.restart", "rcon", "routine.create"
	Detail string    `json:"detail"`
}

func (s *SQLite) migrateAudit() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS audit_log (
	id     INTEGER PRIMARY KEY AUTOINCREMENT,
	ts     INTEGER NOT NULL,
	action TEXT NOT NULL,
	detail TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_audit_ts ON audit_log(ts);
`)
	return err
}

func (s *SQLite) Audit(ctx context.Context, action, detail string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO audit_log(ts, action, detail) VALUES(?,?,?)`,
		time.Now().Unix(), action, detail)
	return err
}

func (s *SQLite) RecentAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, ts, action, detail FROM audit_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var ts int64
		if err := rows.Scan(&e.ID, &ts, &e.Action, &e.Detail); err != nil {
			return nil, err
		}
		e.Time = time.Unix(ts, 0)
		out = append(out, e)
	}
	return out, rows.Err()
}
