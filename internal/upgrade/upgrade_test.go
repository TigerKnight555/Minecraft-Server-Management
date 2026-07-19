package upgrade

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/collector"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mods"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

type fakes struct {
	mu       sync.Mutex
	steps    []string
	backupOK bool
	applyErr error
	version  atomic.Value // string: was der "Server" nach dem Upgrade meldet
	online   atomic.Bool
}

func (f *fakes) rec(s string) {
	f.mu.Lock()
	f.steps = append(f.steps, s)
	f.mu.Unlock()
}
func (f *fakes) list() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.steps...)
}

// collector.RCONClient
func (f *fakes) Exec(_ context.Context, cmd string) (string, error) {
	if strings.HasPrefix(cmd, "say") {
		f.rec("say")
	} else {
		f.rec(cmd)
	}
	return "", nil
}

// collector.ContainerController
func (f *fakes) StartContainer(_ context.Context, id string) error { f.rec("start"); return nil }
func (f *fakes) StopContainer(_ context.Context, id string) error  { f.rec("stop"); return nil }
func (f *fakes) RestartContainer(_ context.Context, id string) error {
	f.rec("restart")
	return nil
}

// resolver
func (f *fakes) Containers() []collector.Container {
	return []collector.Container{{ID: "a1", Name: "mc-fabric", State: "running"}}
}

// BackupRunner
func (f *fakes) Run(context.Context) (string, error) {
	f.rec("backup")
	if !f.backupOK {
		return "", errors.New("NAS weg")
	}
	return "Backup ok", nil
}

// ModManager
func (f *fakes) CheckUpdates(_ context.Context, profile, v string) ([]mods.Entry, error) {
	f.rec("check:" + profile + "@" + v)
	return nil, nil
}
func (f *fakes) Stage(_ context.Context, profile string, _ []string) (int, error) {
	f.rec("stage:" + profile)
	return 2, nil
}
func (f *fakes) ApplyStaged(profile string) (string, int, error) {
	f.rec("apply:" + profile)
	return "L", 2, f.applyErr
}

// Readiness
func (f *fakes) Last() *mods.WatchStatus {
	return &mods.WatchStatus{
		CurrentVersion: "1.21.11", LatestVersion: "26.2",
		NewerAvailable: true, LoaderReady: true,
		Profiles: []mods.ProfileReady{{Profile: "server", Ready: 3, Total: 3}},
	}
}

// UpgradeSignaler
func (f *fakes) RequestUpgrade(version string) error {
	f.rec("signal:" + version)
	f.version.Store(version) // Host "erstellt neu" -> Server meldet Zielversion
	f.online.Store(true)
	return nil
}

func (f *fakes) mcStatus() collector.MCStatus {
	v, _ := f.version.Load().(string)
	return collector.MCStatus{Online: f.online.Load(), Version: v}
}

func newOrch(f *fakes, bus *events.Bus) *Orchestrator {
	o := New(f, f, f, f.mcStatus, f, f, f, f, bus, "mc-fabric", testLogger())
	o.WarnMinutes = 1
	o.WarnStep = time.Millisecond
	o.OnlineTimeout = 2 * time.Second
	o.PollStep = 5 * time.Millisecond
	return o
}

func waitDone(t *testing.T, o *Orchestrator) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		o.mu.Lock()
		running := o.running
		o.mu.Unlock()
		if !running {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("upgrade nicht fertig geworden")
}

func drain(ch <-chan events.Event) []events.Event {
	var out []events.Event
	for {
		select {
		case ev := <-ch:
			out = append(out, ev)
		case <-time.After(50 * time.Millisecond):
			return out
		}
	}
}

func TestUpgradeHappyPathOrder(t *testing.T) {
	f := &fakes{backupOK: true}
	f.online.Store(true)
	bus := events.New()
	ch, cancel := bus.Subscribe(16)
	defer cancel()
	o := newOrch(f, bus)

	if err := o.Start("26.2"); err != nil {
		t.Fatal(err)
	}
	waitDone(t, o)

	steps := f.list()
	want := []string{"say", "say", "save-all", "stop", "backup",
		"check:server@26.2", "stage:server", "apply:server", "signal:26.2",
		"check:client@26.2", "stage:client", "apply:client"}
	if fmt.Sprint(steps) != fmt.Sprint(want) {
		t.Fatalf("steps = %v\nwant    %v", steps, want)
	}
	evs := drain(ch)
	if len(evs) != 2 || evs[0].Type != events.TypeUpgradeStart || evs[1].Type != events.TypeUpgradeOK {
		t.Errorf("events = %+v, want start + ok", evs)
	}
	if !strings.Contains(evs[1].Title, "26.2") {
		t.Errorf("ok-event = %+v", evs[1])
	}
}

func TestUpgradeBackupFailureRestartsOldServer(t *testing.T) {
	f := &fakes{backupOK: false}
	f.online.Store(true)
	bus := events.New()
	ch, cancel := bus.Subscribe(16)
	defer cancel()
	o := newOrch(f, bus)

	if err := o.Start("26.2"); err != nil {
		t.Fatal(err)
	}
	waitDone(t, o)

	steps := f.list()
	last := steps[len(steps)-1]
	if last != "start" {
		t.Fatalf("steps = %v, want alten Server wieder starten", steps)
	}
	for _, s := range steps {
		if strings.HasPrefix(s, "signal:") || strings.HasPrefix(s, "apply:") {
			t.Fatalf("steps = %v — nach Backup-Fehler darf nichts mehr passieren", steps)
		}
	}
	evs := drain(ch)
	if len(evs) != 2 || evs[1].Type != events.TypeUpgradeFailed {
		t.Errorf("events = %+v, want start + failed", evs)
	}
}

func TestUpgradeGuards(t *testing.T) {
	f := &fakes{backupOK: true}
	bus := events.New()
	o := newOrch(f, bus)

	if err := o.Start("1.99"); err == nil || !strings.Contains(err.Error(), "Zielversion") {
		t.Errorf("falsche Version akzeptiert: %v", err)
	}
	// Doppelstart
	f.online.Store(true)
	if err := o.Start("26.2"); err != nil {
		t.Fatal(err)
	}
	if err := o.Start("26.2"); err == nil || !strings.Contains(err.Error(), "läuft bereits") {
		t.Errorf("doppelstart akzeptiert: %v", err)
	}
	waitDone(t, o)
}
