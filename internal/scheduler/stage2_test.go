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

// --- backupChain (Phase 4.3, Stop-basiertes Backup) ---

type fakeResolver struct{ state string }

func (f *fakeResolver) Containers() []collector.Container {
	return []collector.Container{{ID: "abc123", Name: "mc-fabric", State: f.state}}
}

type fakeBackup struct {
	calls int
	msg   string
	err   error
}

func (f *fakeBackup) Run(context.Context) (string, error) {
	f.calls++
	return f.msg, f.err
}

func backupRoutine(mut func(*storage.Routine)) storage.Routine {
	r := storage.Routine{
		Name: "nachtbackup", Cron: "30 3 * * *", Kind: "backup",
		Payload: "mc-fabric", WarnMinutes: 0, Enabled: true,
	}
	if mut != nil {
		mut(&r)
	}
	return r
}

func TestBackupChainStopsSnapshotsStarts(t *testing.T) {
	s, store, docker, rcon := setup(t)
	s.containers = &fakeResolver{state: "running"}
	fb := &fakeBackup{msg: "Backup ok in 42s — snapshot ab12 saved"}
	s.SetBackupRunner(fb)

	id, _ := store.CreateRoutine(context.Background(), backupRoutine(nil))
	s.RunNow(context.Background(), id)

	got := docker.ActionLog()
	if len(got) != 2 || !strings.HasPrefix(got[0], "stop:") || !strings.HasPrefix(got[1], "start:") {
		t.Fatalf("actions = %v, want [stop, start]", got)
	}
	if fb.calls != 1 {
		t.Errorf("backup calls = %d, want 1", fb.calls)
	}
	cmds := rcon.Commands()
	if len(cmds) != 1 || cmds[0] != "save-all" {
		t.Errorf("rcon = %v, want [save-all]", cmds)
	}
	runs, _ := store.RecentRuns(context.Background(), 10)
	if len(runs) != 1 || !runs[0].OK ||
		!strings.Contains(runs[0].Message, "Server gestoppt") ||
		!strings.Contains(runs[0].Message, "Backup ok") ||
		!strings.Contains(runs[0].Message, "Server gestartet") {
		t.Errorf("runs = %+v, want vollständige Schrittkette", runs)
	}
}

func TestBackupChainFailureStartsServerAnyway(t *testing.T) {
	s, store, docker, _ := setup(t)
	s.containers = &fakeResolver{state: "running"}
	fb := &fakeBackup{err: errors.New("NAS weg")}
	s.SetBackupRunner(fb)

	id, _ := store.CreateRoutine(context.Background(), backupRoutine(nil))
	s.RunNow(context.Background(), id)

	got := docker.ActionLog()
	if len(got) != 2 || !strings.HasPrefix(got[1], "start:") {
		t.Fatalf("actions = %v, want Start trotz Backup-Fehler", got)
	}
	runs, _ := store.RecentRuns(context.Background(), 10)
	if len(runs) != 1 || runs[0].OK || !strings.Contains(runs[0].Message, "NAS weg") {
		t.Errorf("runs = %+v, want Fehler mit Ursache", runs)
	}
}

func TestBackupChainStoppedServerStaysStopped(t *testing.T) {
	s, store, docker, rcon := setup(t)
	s.containers = &fakeResolver{state: "exited"}
	fb := &fakeBackup{msg: "Backup ok in 30s — snapshot cd34 saved"}
	s.SetBackupRunner(fb)

	id, _ := store.CreateRoutine(context.Background(), backupRoutine(nil))
	s.RunNow(context.Background(), id)

	if got := docker.ActionLog(); len(got) != 0 {
		t.Fatalf("actions = %v, want keine (Server bleibt aus)", got)
	}
	if len(rcon.Commands()) != 0 {
		t.Errorf("rcon = %v, want keine Befehle bei gestopptem Server", rcon.Commands())
	}
	runs, _ := store.RecentRuns(context.Background(), 10)
	if len(runs) != 1 || !runs[0].OK || !strings.Contains(runs[0].Message, "bleibt aus") {
		t.Errorf("runs = %+v, want Hinweis 'bleibt aus'", runs)
	}
	if fb.calls != 1 {
		t.Errorf("backup calls = %d, want 1", fb.calls)
	}
}

func TestBackupChainAppliesStagedAfterBackup(t *testing.T) {
	s, store, docker, _ := setup(t)
	s.containers = &fakeResolver{state: "running"}
	fb := &fakeBackup{msg: "Backup ok"}
	s.SetBackupRunner(fb)
	app := &fakeApplier{label: "L", n: 2}
	s.SetStagedApplier(app)

	id, _ := store.CreateRoutine(context.Background(), backupRoutine(func(r *storage.Routine) {
		r.ApplyStaged = true
	}))
	s.RunNow(context.Background(), id)

	if app.calls != 1 {
		t.Errorf("ApplyStaged calls = %d, want 1 (nach Backup, vor Start)", app.calls)
	}
	runs, _ := store.RecentRuns(context.Background(), 10)
	if len(runs) != 1 || !runs[0].OK || !strings.Contains(runs[0].Message, "2 Updates eingespielt") {
		t.Errorf("runs = %+v", runs)
	}
	if got := docker.ActionLog(); len(got) != 2 {
		t.Errorf("actions = %v, want [stop, start]", got)
	}
}

func TestBackupChainSkipsWhenPlayersOnline(t *testing.T) {
	s, store, docker, _ := setup(t)
	s.containers = &fakeResolver{state: "running"}
	fb := &fakeBackup{msg: "Backup ok"}
	s.SetBackupRunner(fb)
	var players atomic.Int32
	var online atomic.Bool
	players.Store(2)
	s.SetMCStatus(mcState(&players, &online))

	id, _ := store.CreateRoutine(context.Background(), backupRoutine(func(r *storage.Routine) {
		r.SkipIfPlayersOnline = true
	}))
	s.RunNow(context.Background(), id)

	if fb.calls != 0 || len(docker.ActionLog()) != 0 {
		t.Fatalf("backup=%d actions=%v, want alles übersprungen", fb.calls, docker.ActionLog())
	}
	runs, _ := store.RecentRuns(context.Background(), 10)
	if len(runs) != 1 || !runs[0].OK || !strings.Contains(runs[0].Message, "übersprungen") {
		t.Errorf("runs = %+v", runs)
	}
}

// --- hostReboot (Phase 4.5) ---

type fakeSignaler struct {
	calls int
	err   error
}

func (f *fakeSignaler) RequestReboot() error {
	f.calls++
	return f.err
}

func rebootRoutine(mut func(*storage.Routine)) storage.Routine {
	r := storage.Routine{
		Name: "nachtreboot", Cron: "30 3 * * *", Kind: "host-reboot",
		Payload: "mc-fabric", WarnMinutes: 0, Enabled: true,
	}
	if mut != nil {
		mut(&r)
	}
	return r
}

func TestHostRebootStopsThenSignals(t *testing.T) {
	s, store, docker, rcon := setup(t)
	s.containers = &fakeResolver{state: "running"}
	sig := &fakeSignaler{}
	s.SetRebootSignaler(sig)

	id, _ := store.CreateRoutine(context.Background(), rebootRoutine(nil))
	s.RunNow(context.Background(), id)

	if got := docker.ActionLog(); len(got) != 1 || !strings.HasPrefix(got[0], "stop:") {
		t.Fatalf("actions = %v, want nur [stop] (Start macht restart:always nach Boot)", got)
	}
	if sig.calls != 1 {
		t.Errorf("signaler calls = %d, want 1", sig.calls)
	}
	cmds := rcon.Commands()
	if len(cmds) != 1 || cmds[0] != "save-all" {
		t.Errorf("rcon = %v, want [save-all]", cmds)
	}
	runs, _ := store.RecentRuns(context.Background(), 10)
	if len(runs) != 1 || !runs[0].OK || !strings.Contains(runs[0].Message, "Reboot angefordert") {
		t.Errorf("runs = %+v", runs)
	}
}

func TestHostRebootSignalFailureRestartsServer(t *testing.T) {
	s, store, docker, _ := setup(t)
	s.containers = &fakeResolver{state: "running"}
	sig := &fakeSignaler{err: errors.New("verzeichnis fehlt")}
	s.SetRebootSignaler(sig)

	id, _ := store.CreateRoutine(context.Background(), rebootRoutine(nil))
	s.RunNow(context.Background(), id)

	got := docker.ActionLog()
	if len(got) != 2 || !strings.HasPrefix(got[1], "start:") {
		t.Fatalf("actions = %v, want [stop, start] — Server darf nicht liegen bleiben", got)
	}
	runs, _ := store.RecentRuns(context.Background(), 10)
	if len(runs) != 1 || runs[0].OK || !strings.Contains(runs[0].Message, "Server läuft wieder") {
		t.Errorf("runs = %+v", runs)
	}
}

func TestHostRebootStoppedServerJustSignals(t *testing.T) {
	s, store, docker, rcon := setup(t)
	s.containers = &fakeResolver{state: "exited"}
	sig := &fakeSignaler{}
	s.SetRebootSignaler(sig)

	id, _ := store.CreateRoutine(context.Background(), rebootRoutine(nil))
	s.RunNow(context.Background(), id)

	if got := docker.ActionLog(); len(got) != 0 {
		t.Fatalf("actions = %v, want keine", got)
	}
	if len(rcon.Commands()) != 0 {
		t.Errorf("rcon = %v, want keine", rcon.Commands())
	}
	if sig.calls != 1 {
		t.Errorf("signaler calls = %d, want 1", sig.calls)
	}
}

func TestHostRebootSkipsWhenPlayersOnline(t *testing.T) {
	s, store, docker, _ := setup(t)
	s.containers = &fakeResolver{state: "running"}
	sig := &fakeSignaler{}
	s.SetRebootSignaler(sig)
	var players atomic.Int32
	var online atomic.Bool
	players.Store(1)
	s.SetMCStatus(mcState(&players, &online))

	id, _ := store.CreateRoutine(context.Background(), rebootRoutine(func(r *storage.Routine) {
		r.SkipIfPlayersOnline = true
	}))
	s.RunNow(context.Background(), id)

	if sig.calls != 0 || len(docker.ActionLog()) != 0 {
		t.Fatalf("signaler=%d actions=%v, want übersprungen", sig.calls, docker.ActionLog())
	}
}
