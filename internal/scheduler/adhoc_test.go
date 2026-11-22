package scheduler

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/storage"
)

type adhocStore struct {
	routines []storage.Routine
	runs     int
}

func (s *adhocStore) ListRoutines(context.Context) ([]storage.Routine, error) { return s.routines, nil }
func (s *adhocStore) GetRoutine(_ context.Context, id int64) (storage.Routine, error) {
	for _, r := range s.routines {
		if r.ID == id {
			return r, nil
		}
	}
	return storage.Routine{}, fmt.Errorf("not found")
}
func (s *adhocStore) RecordRun(context.Context, storage.RoutineRun) error { s.runs++; return nil }

type adhocRCON struct{ cmds []string }

func (r *adhocRCON) Exec(_ context.Context, c string) (string, error) {
	r.cmds = append(r.cmds, c)
	return "ok", nil
}

func adhocLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestRunAdhocExecutesWithoutHistory(t *testing.T) {
	store := &adhocStore{}
	rcon := &adhocRCON{}
	s := New(store, rcon, nil, nil, adhocLog())
	s.RunAdhoc(context.Background(), storage.Routine{Name: "Test", Kind: "rcon", Payload: "say hi"})
	if len(rcon.cmds) != 1 || rcon.cmds[0] != "say hi" {
		t.Errorf("cmds = %v", rcon.cmds)
	}
	if store.runs != 0 {
		t.Errorf("Ad-hoc-Lauf in der Historie gespeichert (%d)", store.runs)
	}
}

func TestNextApplyPicksEarliestApplyingRoutine(t *testing.T) {
	store := &adhocStore{routines: []storage.Routine{
		{ID: 1, Name: "Nacht-Backup", Kind: "backup", Cron: "0 4 * * *", Enabled: true, ApplyStaged: true},
		{ID: 2, Name: "Neustart ohne Updates", Kind: "announce-restart", Cron: "0 20 * * *", Enabled: true},
		{ID: 3, Name: "Aus", Kind: "announce-restart", Cron: "0 18 * * *", Enabled: false, ApplyStaged: true},
		{ID: 4, Name: "RCON", Kind: "rcon", Cron: "0 18 * * *", Enabled: true, ApplyStaged: true},
	}}
	s := New(store, nil, nil, nil, adhocLog())
	now := time.Date(2026, 11, 8, 17, 0, 0, 0, time.Local)
	name, at, ok, err := s.NextApply(context.Background(), now)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if name != "Nacht-Backup" || !at.Equal(time.Date(2026, 11, 9, 4, 0, 0, 0, time.Local)) {
		t.Errorf("next = %s @ %v", name, at)
	}

	store.routines = store.routines[1:]
	if _, _, ok, _ := s.NextApply(context.Background(), now); ok {
		t.Error("ok ohne passende Routine")
	}
}
