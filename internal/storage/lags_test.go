package storage

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/jvmstats"
)

func TestSaveAndListLags(t *testing.T) {
	s := openTestDB(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	older := &jvmstats.LagEvent{Start: now.Add(-2 * time.Hour), End: now.Add(-2*time.Hour + 20*time.Second),
		Trigger: "gc", Triggers: []string{"gc"}, MaxGCPauseMs: 812}
	if err := s.SaveLag(ctx, older); err != nil {
		t.Fatal(err)
	}
	if older.ID == 0 {
		t.Fatal("SaveLag hat keine ID vergeben")
	}
	newer := &jvmstats.LagEvent{Start: now.Add(-time.Hour), End: now.Add(-time.Hour),
		Trigger: "mspt", Triggers: []string{"mspt"}, WorstMSPT: 61,
		TopThreads: []jvmstats.ThreadGroup{{Name: "Server thread", Count: 1, CPU: 99}}}
	if err := s.SaveLag(ctx, newer); err != nil {
		t.Fatal(err)
	}
	id := newer.ID
	// laufender Lag wird jeden Takt aktualisiert, nicht doppelt angelegt
	newer.End = now.Add(-time.Hour + 30*time.Second)
	newer.WorstMSPT = 75
	newer.Triggers = append(newer.Triggers, "cpu")
	if err := s.SaveLag(ctx, newer); err != nil {
		t.Fatal(err)
	}
	if newer.ID != id {
		t.Fatalf("ID geändert: %d → %d", id, newer.ID)
	}

	got, err := s.ListLags(ctx, now.Add(-24*time.Hour), 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d lags, want 2", len(got))
	}
	if got[0].ID != id || got[0].WorstMSPT != 75 || strings.Join(got[0].Triggers, ",") != "mspt,cpu" ||
		!got[0].End.Equal(newer.End) || got[0].TopThreads[0].Name != "Server thread" {
		t.Errorf("neuester Lag = %+v", got[0])
	}
	if got[1].ID != older.ID || got[1].MaxGCPauseMs != 812 {
		t.Errorf("älterer Lag = %+v", got[1])
	}
	if got, _ := s.ListLags(ctx, now.Add(-24*time.Hour), 1); len(got) != 1 {
		t.Errorf("limit 1: %d Einträge", len(got))
	}
	if got, _ := s.ListLags(ctx, now.Add(-90*time.Minute), 500); len(got) != 1 || got[0].ID != id {
		t.Errorf("since: %+v", got)
	}
}

func TestListLagsEmptyIsNotNil(t *testing.T) {
	s := openTestDB(t)
	got, err := s.ListLags(context.Background(), time.Now().Add(-time.Hour), 500)
	if err != nil || got == nil {
		t.Errorf("got %v, %v; want leere Liste", got, err)
	}
}

func TestPruneLags(t *testing.T) {
	s := openTestDB(t)
	ctx := context.Background()
	now := time.Now()
	old := &jvmstats.LagEvent{Start: now.Add(-31 * 24 * time.Hour), End: now.Add(-31 * 24 * time.Hour), Trigger: "gc"}
	recent := &jvmstats.LagEvent{Start: now.Add(-29 * 24 * time.Hour), End: now.Add(-29 * 24 * time.Hour), Trigger: "gc"}
	for _, ev := range []*jvmstats.LagEvent{old, recent} {
		if err := s.SaveLag(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListLags(ctx, now.Add(-40*24*time.Hour), 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != recent.ID {
		t.Errorf("nach Prune: %+v, want nur den 29 Tage alten Lag", got)
	}
}
