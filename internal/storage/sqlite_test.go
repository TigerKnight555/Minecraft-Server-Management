package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/collector"
)

func openTestDB(t *testing.T) *SQLite {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestWriteAndQueryRaw(t *testing.T) {
	s := openTestDB(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	samples := []collector.Sample{
		{Series: "host.cpu", Time: now.Add(-2 * time.Minute), Value: 10},
		{Series: "host.cpu", Time: now.Add(-1 * time.Minute), Value: 20},
		{Series: "host.mem", Time: now, Value: 999},
	}
	if err := s.WriteSamples(ctx, samples); err != nil {
		t.Fatal(err)
	}

	got, err := s.QuerySeries(ctx, "host.cpu", now.Add(-10*time.Minute), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d samples, want 2", len(got))
	}
	if got[0].Value != 10 || got[1].Value != 20 {
		t.Errorf("values = %v, %v; want 10, 20", got[0].Value, got[1].Value)
	}
}

func TestMinuteDownsampling(t *testing.T) {
	s := openTestDB(t)
	ctx := context.Background()
	base := time.Now().Add(-72 * time.Hour).Truncate(time.Minute)

	// three samples inside the same minute → mean 20
	samples := []collector.Sample{
		{Series: "mc.tps", Time: base.Add(5 * time.Second), Value: 10},
		{Series: "mc.tps", Time: base.Add(20 * time.Second), Value: 20},
		{Series: "mc.tps", Time: base.Add(40 * time.Second), Value: 30},
	}
	if err := s.WriteSamples(ctx, samples); err != nil {
		t.Fatal(err)
	}

	// query window older than 48h → served from the minute table
	got, err := s.QuerySeries(ctx, "mc.tps", base.Add(-time.Hour), base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d minute samples, want 1", len(got))
	}
	if got[0].Value != 20 {
		t.Errorf("minute mean = %v, want 20", got[0].Value)
	}
}

func TestPrune(t *testing.T) {
	s := openTestDB(t)
	ctx := context.Background()

	old := time.Now().Add(-50 * time.Hour)
	fresh := time.Now()
	if err := s.WriteSamples(ctx, []collector.Sample{
		{Series: "host.cpu", Time: old, Value: 1},
		{Series: "host.cpu", Time: fresh, Value: 2},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := s.QuerySeries(ctx, "host.cpu", time.Now().Add(-47*time.Hour), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Value != 2 {
		t.Errorf("after prune got %v, want only the fresh sample", got)
	}
}
