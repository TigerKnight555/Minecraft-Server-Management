package scheduler

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mock"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/storage"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func setup(t *testing.T) (*Scheduler, *mock.Store, *mock.Docker, *mock.RCON) {
	t.Helper()
	store := mock.NewStore()
	docker := mock.NewDocker()
	rcon := mock.NewRCON()
	// nil resolver: container names resolve to themselves (docker accepts names)
	s := New(store, rcon, docker, nil, testLogger())
	s.warnStep = 5 * time.Millisecond // shrink announce countdown for tests
	return s, store, docker, rcon
}

func TestRunRconRoutine(t *testing.T) {
	s, store, _, rcon := setup(t)
	id, _ := store.CreateRoutine(context.Background(), storage.Routine{
		Name: "save", Cron: "0 4 * * *", Kind: "rcon", Payload: "save-all", Enabled: true,
	})
	s.RunNow(context.Background(), id)

	cmds := rcon.Commands()
	if len(cmds) != 1 || cmds[0] != "save-all" {
		t.Errorf("rcon commands = %v, want [save-all]", cmds)
	}
	runs, _ := store.RecentRuns(context.Background(), 10)
	if len(runs) != 1 || !runs[0].OK {
		t.Errorf("runs = %+v, want one successful run", runs)
	}
}

func TestRunRestartRoutine(t *testing.T) {
	s, store, docker, _ := setup(t)
	id, _ := store.CreateRoutine(context.Background(), storage.Routine{
		Name: "neustart", Cron: "30 4 * * *", Kind: "restart", Payload: "mc-fabric", Enabled: true,
	})
	s.RunNow(context.Background(), id)

	actions := docker.ActionLog()
	if len(actions) != 1 || !strings.HasPrefix(actions[0], "restart:") {
		t.Errorf("actions = %v, want one restart", actions)
	}
}

func TestAnnounceRestartWarnsThenRestarts(t *testing.T) {
	s, store, docker, rcon := setup(t)
	id, _ := store.CreateRoutine(context.Background(), storage.Routine{
		Name: "angekündigt", Cron: "0 5 * * *", Kind: "announce-restart",
		Payload: "mc-fabric", WarnMinutes: 3, Enabled: true,
	})
	s.RunNow(context.Background(), id)

	cmds := rcon.Commands()
	// 3 countdown warnings + final "jetzt" + save-all vor dem Stopp (Stufe 2)
	if len(cmds) != 5 {
		t.Fatalf("rcon commands = %v, want 4 announcements + save-all", cmds)
	}
	if !strings.Contains(cmds[0], "3 Minute") {
		t.Errorf("first warning = %q, want countdown at 3", cmds[0])
	}
	if cmds[4] != "save-all" {
		t.Errorf("last command = %q, want save-all before stop", cmds[4])
	}
	if len(docker.ActionLog()) != 1 {
		t.Errorf("actions = %v, want one restart after warnings", docker.ActionLog())
	}
	runs, _ := store.RecentRuns(context.Background(), 10)
	if len(runs) != 1 || !runs[0].OK {
		t.Errorf("runs = %+v, want one successful run", runs)
	}
}

func TestUnknownKindRecordsFailure(t *testing.T) {
	s, store, _, _ := setup(t)
	id, _ := store.CreateRoutine(context.Background(), storage.Routine{
		Name: "kaputt", Cron: "* * * * *", Kind: "unfug", Enabled: true,
	})
	s.RunNow(context.Background(), id)
	runs, _ := store.RecentRuns(context.Background(), 10)
	if len(runs) != 1 || runs[0].OK {
		t.Fatalf("runs = %+v, want one failed run (never silent)", runs)
	}
	if !strings.Contains(runs[0].Message, "unfug") {
		t.Errorf("failure message %q should name the bad kind", runs[0].Message)
	}
}

func TestValidateCron(t *testing.T) {
	if err := ValidateCron("0 4 * * *"); err != nil {
		t.Errorf("valid cron rejected: %v", err)
	}
	if err := ValidateCron("kein cron"); err == nil {
		t.Error("invalid cron accepted")
	}
}

func TestReloadSkipsInvalidCronVisibly(t *testing.T) {
	s, store, _, _ := setup(t)
	store.CreateRoutine(context.Background(), storage.Routine{
		Name: "ok", Cron: "0 4 * * *", Kind: "rcon", Payload: "list", Enabled: true,
	})
	badID, _ := store.CreateRoutine(context.Background(), storage.Routine{
		Name: "kaputt", Cron: "invalid", Kind: "rcon", Payload: "list", Enabled: true,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	runs, _ := store.RecentRuns(context.Background(), 10)
	found := false
	for _, r := range runs {
		if r.RoutineID == badID && !r.OK {
			found = true
		}
	}
	if !found {
		t.Error("invalid cron produced no visible failure record")
	}
}

func TestRunPublishesOutcomeEvents(t *testing.T) {
	s, store, _, _ := setup(t)
	bus := events.New()
	ch, cancel := bus.Subscribe(8)
	defer cancel()
	s.SetBus(bus)

	okID, _ := store.CreateRoutine(context.Background(), storage.Routine{
		Name: "gut", Cron: "0 4 * * *", Kind: "rcon", Payload: "list", Enabled: true,
	})
	badID, _ := store.CreateRoutine(context.Background(), storage.Routine{
		Name: "kaputt", Cron: "0 4 * * *", Kind: "unfug", Enabled: true,
	})
	s.RunNow(context.Background(), okID)
	s.RunNow(context.Background(), badID)

	var got []events.Event
	for i := 0; i < 2; i++ {
		select {
		case ev := <-ch:
			got = append(got, ev)
		case <-time.After(time.Second):
			t.Fatalf("nur %d events erhalten, want 2", len(got))
		}
	}
	if got[0].Type != events.TypeRoutineOK || !strings.Contains(got[0].Title, "gut") {
		t.Errorf("event 0 = %+v, want routine.ok für 'gut'", got[0])
	}
	if got[1].Type != events.TypeRoutineFailed || got[1].Severity != events.SevError {
		t.Errorf("event 1 = %+v, want routine.failed mit severity error", got[1])
	}
}
