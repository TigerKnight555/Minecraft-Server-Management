// Package storage persists metric samples in SQLite. Retention follows the
// concept: raw samples for 48 h, per-minute means for 30 days.
package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/collector"
)

const (
	rawRetention    = 48 * time.Hour
	minuteRetention = 30 * 24 * time.Hour
)

type SQLite struct {
	db *sql.DB
}

func Open(path string) (*SQLite, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	// modernc sqlite is not safe for concurrent writes on one connection pool >1
	db.SetMaxOpenConns(1)
	s := &SQLite{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLite) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS samples_raw (
	series TEXT NOT NULL,
	ts     INTEGER NOT NULL, -- unix seconds
	value  REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_raw_series_ts ON samples_raw(series, ts);

CREATE TABLE IF NOT EXISTS samples_minute (
	series TEXT NOT NULL,
	ts     INTEGER NOT NULL, -- unix seconds, truncated to the minute
	value  REAL NOT NULL,    -- mean over the minute
	n      INTEGER NOT NULL, -- sample count backing the mean
	PRIMARY KEY (series, ts)
);
`)
	if err != nil {
		return err
	}
	if err := s.migrateAudit(); err != nil {
		return err
	}
	if err := s.migrateStates(); err != nil {
		return err
	}
	if err := s.migrateMaintenance(); err != nil {
		return err
	}
	return s.migrateRoutines()
}

func (s *SQLite) WriteSamples(ctx context.Context, samples []collector.Sample) error {
	if len(samples) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	insRaw, err := tx.PrepareContext(ctx, `INSERT INTO samples_raw(series, ts, value) VALUES(?,?,?)`)
	if err != nil {
		return err
	}
	defer insRaw.Close()
	// running mean update keeps the minute table correct without a batch job
	insMin, err := tx.PrepareContext(ctx, `
INSERT INTO samples_minute(series, ts, value, n) VALUES(?,?,?,1)
ON CONFLICT(series, ts) DO UPDATE SET
	value = (value * n + excluded.value) / (n + 1),
	n = n + 1`)
	if err != nil {
		return err
	}
	defer insMin.Close()

	for _, smp := range samples {
		ts := smp.Time.Unix()
		if _, err := insRaw.ExecContext(ctx, smp.Series, ts, smp.Value); err != nil {
			return err
		}
		minuteTS := smp.Time.Truncate(time.Minute).Unix()
		if _, err := insMin.ExecContext(ctx, smp.Series, minuteTS, smp.Value); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// QuerySeries picks the resolution by range: raw inside 48 h, minute means
// beyond that.
func (s *SQLite) QuerySeries(ctx context.Context, series string, from, to time.Time) ([]collector.Sample, error) {
	table := "samples_raw"
	if time.Since(from) > rawRetention {
		table = "samples_minute"
	}
	rows, err := s.db.QueryContext(ctx,
		fmt.Sprintf(`SELECT ts, value FROM %s WHERE series = ? AND ts BETWEEN ? AND ? ORDER BY ts`, table),
		series, from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []collector.Sample
	for rows.Next() {
		var ts int64
		var v float64
		if err := rows.Scan(&ts, &v); err != nil {
			return nil, err
		}
		out = append(out, collector.Sample{Series: series, Time: time.Unix(ts, 0), Value: v})
	}
	return out, rows.Err()
}

// Prune deletes data past retention; call it periodically (e.g. hourly).
func (s *SQLite) Prune(ctx context.Context) error {
	now := time.Now()
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM samples_raw WHERE ts < ?`, now.Add(-rawRetention).Unix()); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM samples_minute WHERE ts < ?`, now.Add(-minuteRetention).Unix())
	return err
}

func (s *SQLite) Close() error { return s.db.Close() }
