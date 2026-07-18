package hostctl

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/collector"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/storage"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestSignalerWritesRequestFile(t *testing.T) {
	dir := t.TempDir()
	if err := NewSignaler(dir).RequestReboot(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "reboot.request"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "MSM Reboot-Anforderung") {
		t.Errorf("inhalt = %q", data)
	}
}

func TestSignalerFailsLoudlyWithoutDir(t *testing.T) {
	if err := NewSignaler("").RequestReboot(); err == nil {
		t.Error("leeres Verzeichnis akzeptiert")
	}
	if err := NewSignaler(filepath.Join(t.TempDir(), "gibtsnicht")).RequestReboot(); err == nil {
		t.Error("fehlendes Verzeichnis akzeptiert")
	}
}

// --- Reconciler ---

type fakeStore struct{ states []storage.DesiredState }

func (f *fakeStore) ListDesiredStates(context.Context) ([]storage.DesiredState, error) {
	return f.states, nil
}

type fakeController struct {
	mu      sync.Mutex
	actions []string
}

func (f *fakeController) act(a, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actions = append(f.actions, a+":"+id)
	return nil
}
func (f *fakeController) StartContainer(_ context.Context, id string) error {
	return f.act("start", id)
}
func (f *fakeController) StopContainer(_ context.Context, id string) error { return f.act("stop", id) }
func (f *fakeController) RestartContainer(_ context.Context, id string) error {
	return f.act("restart", id)
}
func (f *fakeController) log() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.actions...)
}

type fakeContainers struct{ list []collector.Container }

func (f *fakeContainers) Containers() []collector.Container { return f.list }

func newReconciler(store DesiredStore, ctrl collector.ContainerController, containers resolver,
	online bool, uptime uint64, bus *events.Bus) *Reconciler {
	r := NewReconciler(store, ctrl, containers,
		func() collector.MCStatus { return collector.MCStatus{Online: online} },
		func() collector.HostSample { return collector.HostSample{UptimeSec: uptime} },
		bus, "mc-fabric", testLogger())
	r.Warmup = time.Millisecond
	r.OnlineTimeout = 100 * time.Millisecond
	r.OnlinePollStep = 5 * time.Millisecond
	return r
}

func collect(ch <-chan events.Event) []events.Event {
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

func TestReconcilerEnforcesDesiredState(t *testing.T) {
	ctrl := &fakeController{}
	store := &fakeStore{states: []storage.DesiredState{
		{Container: "mc-fabric", State: "running"},
		{Container: "webseite", State: "stopped"},
	}}
	containers := &fakeContainers{list: []collector.Container{
		{ID: "a1", Name: "mc-fabric", State: "exited"}, // soll laufen -> start
		{ID: "b2", Name: "webseite", State: "running"}, // bewusst gestoppt -> stop
	}}
	bus := events.New()
	ch, cancel := bus.Subscribe(8)
	defer cancel()

	rec := newReconciler(store, ctrl, containers, true, 60, bus) // frischer Boot
	rec.Run(context.Background())

	got := ctrl.log()
	if len(got) != 2 || got[0] != "start:a1" || got[1] != "stop:b2" {
		t.Fatalf("actions = %v, want [start:a1 stop:b2]", got)
	}
	evs := collect(ch)
	if len(evs) != 1 || evs[0].Type != events.TypeSystemOnline || evs[0].Severity != events.SevSuccess {
		t.Errorf("events = %+v, want system.online (success)", evs)
	}
}

func TestReconcilerAlarmsWhenServerStaysDown(t *testing.T) {
	ctrl := &fakeController{}
	store := &fakeStore{}
	containers := &fakeContainers{list: []collector.Container{{ID: "a1", Name: "mc-fabric", State: "running"}}}
	bus := events.New()
	ch, cancel := bus.Subscribe(8)
	defer cancel()

	rec := newReconciler(store, ctrl, containers, false /*nie online*/, 60, bus)
	rec.Run(context.Background())

	evs := collect(ch)
	if len(evs) != 1 || evs[0].Type != events.TypeSystemDegraded {
		t.Fatalf("events = %+v, want system.degraded", evs)
	}
}

func TestReconcilerNoBootMessageOnRedeploy(t *testing.T) {
	ctrl := &fakeController{}
	store := &fakeStore{states: []storage.DesiredState{{Container: "mc-fabric", State: "running"}}}
	containers := &fakeContainers{list: []collector.Container{{ID: "a1", Name: "mc-fabric", State: "exited"}}}
	bus := events.New()
	ch, cancel := bus.Subscribe(8)
	defer cancel()

	// Uptime 3 Tage = kein frischer Boot -> Abgleich ja, Meldung nein
	rec := newReconciler(store, ctrl, containers, true, 3*24*3600, bus)
	rec.Run(context.Background())

	if got := ctrl.log(); len(got) != 1 || got[0] != "start:a1" {
		t.Fatalf("actions = %v, want Abgleich trotzdem", got)
	}
	if evs := collect(ch); len(evs) != 0 {
		t.Errorf("events = %+v, want keine Boot-Meldung bei Redeploy", evs)
	}
}

func TestReconcilerStoppedDesiredSkipsWatchdog(t *testing.T) {
	ctrl := &fakeController{}
	store := &fakeStore{states: []storage.DesiredState{{Container: "mc-fabric", State: "stopped"}}}
	containers := &fakeContainers{list: []collector.Container{{ID: "a1", Name: "mc-fabric", State: "exited"}}}
	bus := events.New()
	ch, cancel := bus.Subscribe(8)
	defer cancel()

	rec := newReconciler(store, ctrl, containers, false, 60, bus)
	rec.Run(context.Background())

	evs := collect(ch)
	if len(evs) != 1 || evs[0].Type != events.TypeSystemOnline || !strings.Contains(evs[0].Message, "bleibt gestoppt") {
		t.Fatalf("events = %+v, want Info 'bleibt gestoppt' ohne Alarm", evs)
	}
}
