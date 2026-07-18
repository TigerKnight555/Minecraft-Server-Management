package scheduler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/collector"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mods"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/storage"
)

// fakeApplier records ApplyStaged calls and returns a configurable result.
type fakeApplier struct {
	calls int
	label string
	n     int
	err   error
}

func (f *fakeApplier) ApplyStaged(profile string) (string, int, error) {
	f.calls++
	return f.label, f.n, f.err
}

func mcState(players *atomic.Int32, online *atomic.Bool) func() collector.MCStatus {
	return func() collector.MCStatus {
		return collector.MCStatus{
			Time:          time.Now(),
			Online:        online.Load(),
			PlayersOnline: int(players.Load()),
		}
	}
}

func announceRoutine(mut func(*storage.Routine)) storage.Routine {
	r := storage.Routine{
		Name: "nacht", Cron: "0 4 * * *", Kind: "announce-restart",
		Payload: "mc-fabric", WarnMinutes: 1, Enabled: true,
	}
	mut(&r)
	return r
}

func TestSkipIfPlayersOnline(t *testing.T) {
	s, store, docker, _ := setup(t)
	var players atomic.Int32
	var online atomic.Bool
	players.Store(3)
	s.SetMCStatus(mcState(&players, &online))

	id, _ := store.CreateRoutine(context.Background(), announceRoutine(func(r *storage.Routine) {
		r.SkipIfPlayersOnline = true
	}))
	s.RunNow(context.Background(), id)

	if len(docker.ActionLog()) != 0 {
		t.Errorf("actions = %v, want none (skipped)", docker.ActionLog())
	}
	runs, _ := store.RecentRuns(context.Background(), 10)
	if len(runs) != 1 || !runs[0].OK || !strings.Contains(runs[0].Message, "übersprungen") {
		t.Errorf("runs = %+v, want OK 'übersprungen'", runs)
	}
}

func TestWaitForEmptyProceedsWhenServerEmpties(t *testing.T) {
	s, store, docker, _ := setup(t)
	var players atomic.Int32
	var online atomic.Bool
	players.Store(2)
	s.SetMCStatus(mcState(&players, &online))

	id, _ := store.CreateRoutine(context.Background(), announceRoutine(func(r *storage.Routine) {
		r.WaitForEmpty = true
		r.WarnMinutes = 0
	}))
	go func() {
		time.Sleep(15 * time.Millisecond)
		players.Store(0)
	}()
	s.RunNow(context.Background(), id)

	if got := docker.ActionLog(); len(got) != 1 || !strings.HasPrefix(got[0], "restart:") {
		t.Errorf("actions = %v, want one restart after server emptied", got)
	}
}

func TestApplyStagedStopsSwapsStarts(t *testing.T) {
	s, store, docker, _ := setup(t)
	app := &fakeApplier{label: "2026-07-18_20-00-00", n: 3}
	s.SetStagedApplier(app)

	id, _ := store.CreateRoutine(context.Background(), announceRoutine(func(r *storage.Routine) {
		r.ApplyStaged = true
		r.WarnMinutes = 0
	}))
	s.RunNow(context.Background(), id)

	got := docker.ActionLog()
	if len(got) != 2 || !strings.HasPrefix(got[0], "stop:") || !strings.HasPrefix(got[1], "start:") {
		t.Fatalf("actions = %v, want [stop, start]", got)
	}
	if app.calls != 1 {
		t.Errorf("ApplyStaged calls = %d, want 1", app.calls)
	}
	runs, _ := store.RecentRuns(context.Background(), 10)
	if len(runs) != 1 || !runs[0].OK || !strings.Contains(runs[0].Message, "3 Updates eingespielt") {
		t.Errorf("runs = %+v, want OK mit Update-Zahl", runs)
	}
}

func TestApplyStagedNothingStagedIsNoFailure(t *testing.T) {
	s, store, docker, _ := setup(t)
	app := &fakeApplier{err: fmt.Errorf("%w für Profil server", mods.ErrNothingStaged)}
	s.SetStagedApplier(app)

	id, _ := store.CreateRoutine(context.Background(), announceRoutine(func(r *storage.Routine) {
		r.ApplyStaged = true
		r.WarnMinutes = 0
	}))
	s.RunNow(context.Background(), id)

	runs, _ := store.RecentRuns(context.Background(), 10)
	if len(runs) != 1 || !runs[0].OK || !strings.Contains(runs[0].Message, "keine gestagten Updates") {
		t.Errorf("runs = %+v, want OK 'keine gestagten Updates'", runs)
	}
	if got := docker.ActionLog(); len(got) != 2 {
		t.Errorf("actions = %v, want stop+start trotzdem", got)
	}
}

func TestApplyStagedFailureRestartsServerAnyway(t *testing.T) {
	s, store, docker, _ := setup(t)
	app := &fakeApplier{err: errors.New("hash kaputt")}
	s.SetStagedApplier(app)

	id, _ := store.CreateRoutine(context.Background(), announceRoutine(func(r *storage.Routine) {
		r.ApplyStaged = true
		r.WarnMinutes = 0
	}))
	s.RunNow(context.Background(), id)

	// Fehlschlag ja — aber der Server muss wieder gestartet worden sein
	got := docker.ActionLog()
	if len(got) != 2 || !strings.HasPrefix(got[1], "start:") {
		t.Fatalf("actions = %v, want [stop, start] trotz Fehler", got)
	}
	runs, _ := store.RecentRuns(context.Background(), 10)
	if len(runs) != 1 || runs[0].OK || !strings.Contains(runs[0].Message, "läuft wieder mit altem Stand") {
		t.Errorf("runs = %+v, want Fehler mit 'läuft wieder'", runs)
	}
}

func TestWatchdogSucceedsWhenServerComesBack(t *testing.T) {
	s, store, _, _ := setup(t)
	var players atomic.Int32
	var online atomic.Bool
	s.SetMCStatus(mcState(&players, &online))

	id, _ := store.CreateRoutine(context.Background(), announceRoutine(func(r *storage.Routine) {
		r.WatchdogMinutes = 1 // warnStep ist im Test winzig, Timeout großzügig
		r.WarnMinutes = 0
	}))
	go func() {
		time.Sleep(10 * time.Millisecond)
		online.Store(true)
	}()
	s.RunNow(context.Background(), id)

	runs, _ := store.RecentRuns(context.Background(), 10)
	if len(runs) != 1 || !runs[0].OK || !strings.Contains(runs[0].Message, "Watchdog") {
		t.Errorf("runs = %+v, want OK mit Watchdog-Schritt", runs)
	}
}

func TestWatchdogFailsWhenServerStaysDown(t *testing.T) {
	s, store, _, _ := setup(t)
	var players atomic.Int32
	var online atomic.Bool // bleibt false
	s.SetMCStatus(mcState(&players, &online))
	s.warnStep = 5 * time.Millisecond

	id, _ := store.CreateRoutine(context.Background(), announceRoutine(func(r *storage.Routine) {
		r.WatchdogMinutes = 1
		r.WarnMinutes = 0
	}))

	// Timeout künstlich klein halten: watchdog direkt testen
	err := s.watchdog(context.Background(), 30*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "nicht wieder online") {
		t.Errorf("watchdog err = %v, want 'nicht wieder online'", err)
	}
	_ = id
	_ = store
}

func TestConditionsWithoutWiringFailLoudly(t *testing.T) {
	s, store, _, _ := setup(t)
	// kein SetMCStatus, kein SetStagedApplier
	id, _ := store.CreateRoutine(context.Background(), announceRoutine(func(r *storage.Routine) {
		r.SkipIfPlayersOnline = true
		r.WarnMinutes = 0
	}))
	s.RunNow(context.Background(), id)
	runs, _ := store.RecentRuns(context.Background(), 10)
	if len(runs) != 1 || runs[0].OK || !strings.Contains(runs[0].Message, "nicht verdrahtet") {
		t.Errorf("runs = %+v, want lauter Fehler statt stillem Degradieren", runs)
	}
}
