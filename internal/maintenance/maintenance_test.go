package maintenance

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/collector"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mock"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/storage"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

type fakeContainers struct{ state string }

func (f *fakeContainers) Containers() []collector.Container {
	return []collector.Container{{ID: "a1", Name: "mc-fabric", State: f.state}}
}

func setup(t *testing.T, containerState string, online bool) (*Manager, *mock.Store, *mock.Docker, *mock.RCON, <-chan events.Event) {
	t.Helper()
	store := mock.NewStore()
	docker := mock.NewDocker()
	rcon := mock.NewRCON()
	bus := events.New()
	ch, cancel := bus.Subscribe(16)
	t.Cleanup(cancel)
	m := New(store, docker, &fakeContainers{state: containerState}, rcon,
		func() collector.MCStatus { return collector.MCStatus{Online: online} },
		bus, "mc-fabric", testLogger())
	m.OnlineTimeout = 50 * time.Millisecond
	return m, store, docker, rcon, ch
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

func TestWindowStartStopsServerAndMutes(t *testing.T) {
	m, store, docker, rcon, ch := setup(t, "running", true)
	store.CreateWindow(context.Background(), storage.MaintenanceWindow{
		Name: "Strom", Start: time.Now().Add(-time.Minute), End: time.Now().Add(time.Hour),
	})
	m.Tick(context.Background())

	if !m.Active() {
		t.Fatal("Active() = false, want true während des Fensters")
	}
	if got := docker.ActionLog(); len(got) != 1 || !strings.HasPrefix(got[0], "stop:") {
		t.Errorf("actions = %v, want [stop]", got)
	}
	cmds := rcon.Commands()
	if len(cmds) != 2 || !strings.Contains(cmds[0], "JETZT") || cmds[1] != "save-all" {
		t.Errorf("rcon = %v, want Ansage + save-all", cmds)
	}
	evs := drain(ch)
	if len(evs) != 1 || evs[0].Type != events.TypeMaintStart {
		t.Errorf("events = %+v, want maintenance.start", evs)
	}
	ws, _ := store.ListWindows(context.Background())
	if !ws[0].Started || !ws[0].StoppedServer {
		t.Errorf("window = %+v, want started + stoppedServer persistiert", ws[0])
	}
}

func TestWindowEndRestartsAndReports(t *testing.T) {
	m, store, docker, _, ch := setup(t, "exited", true)
	id, _ := store.CreateWindow(context.Background(), storage.MaintenanceWindow{
		Name: "Strom", Start: time.Now().Add(-2 * time.Hour), End: time.Now().Add(-time.Minute),
	})
	store.MarkWindow(context.Background(), id, true, false, true) // Fenster lief, Server von uns gestoppt

	m.Tick(context.Background())

	if m.Active() {
		t.Error("Active() = true nach Fensterende")
	}
	if got := docker.ActionLog(); len(got) != 1 || !strings.HasPrefix(got[0], "start:") {
		t.Errorf("actions = %v, want [start]", got)
	}
	evs := drain(ch)
	if len(evs) != 1 || evs[0].Type != events.TypeMaintEnd || evs[0].Severity != events.SevSuccess {
		t.Errorf("events = %+v, want maintenance.end (success)", evs)
	}
	ws, _ := store.ListWindows(context.Background())
	if !ws[0].Ended {
		t.Error("Fenster nicht als beendet markiert")
	}
}

func TestWindowLeavesForeignStoppedServerAlone(t *testing.T) {
	// Server war schon vor dem Fenster aus -> Fenster startet ihn NICHT
	m, store, docker, _, ch := setup(t, "exited", false)
	id, _ := store.CreateWindow(context.Background(), storage.MaintenanceWindow{
		Name: "w", Start: time.Now().Add(-2 * time.Hour), End: time.Now().Add(-time.Minute),
	})
	store.MarkWindow(context.Background(), id, true, false, false) // stoppedServer=false

	m.Tick(context.Background())

	if got := docker.ActionLog(); len(got) != 0 {
		t.Errorf("actions = %v, want keine (Server war fremd-gestoppt)", got)
	}
	evs := drain(ch)
	if len(evs) != 1 || evs[0].Type != events.TypeMaintEnd {
		t.Errorf("events = %+v", evs)
	}
}

func TestWarningsBeforeStart(t *testing.T) {
	m, store, _, rcon, _ := setup(t, "running", true)
	store.CreateWindow(context.Background(), storage.MaintenanceWindow{
		Name: "w", Start: time.Now().Add(4 * time.Minute), End: time.Now().Add(time.Hour),
	})
	m.Tick(context.Background())
	m.Tick(context.Background()) // gleiche Stufe darf nicht doppelt warnen

	cmds := rcon.Commands()
	if len(cmds) != 1 || !strings.Contains(cmds[0], "Minute") {
		t.Errorf("rcon = %v, want genau eine 5-min-Warnung", cmds)
	}
	if m.Active() {
		t.Error("Active() = true vor Fensterbeginn")
	}
}

// Discord-Ankündigungen vor geplanter Downtime: 1 h vorher + 5 min vorher,
// persistiert (kein Doppel-Post nach MSM-Neustart), Kurzfrist-Fenster
// überspringen die 1h-Stufe.
func TestAnnouncePhaseStages(t *testing.T) {
	m, store, _, _, ch := setup(t, "running", true)
	id, _ := store.CreateWindow(context.Background(), storage.MaintenanceWindow{
		Name: "Stromkasten", Start: time.Now().Add(50 * time.Minute), End: time.Now().Add(3 * time.Hour),
	})

	m.Tick(context.Background()) // 50 min vorher -> 1h-Stufe
	m.Tick(context.Background()) // darf nicht doppeln
	evs := drain(ch)
	if len(evs) != 1 || evs[0].Type != events.TypeMaintAnnounce || !strings.Contains(evs[0].Title, "In einer Stunde") {
		t.Fatalf("events = %+v, want genau eine 1h-Ankündigung", evs)
	}

	// Fenster rückt auf 4 min heran -> 5m-Stufe
	store.Windows[0].Start = time.Now().Add(4 * time.Minute)
	m.Tick(context.Background())
	m.Tick(context.Background())
	evs = drain(ch)
	if len(evs) != 1 || !strings.Contains(evs[0].Title, "Gleich geht's los") {
		t.Fatalf("events = %+v, want genau eine 5m-Ankündigung", evs)
	}
	ws, _ := store.ListWindows(context.Background())
	if !ws[0].Notified1h || !ws[0].Notified5m {
		t.Errorf("window = %+v, want beide Marken persistiert", ws[0])
	}
	_ = id
}

func TestAnnouncePhaseShortNoticeSkips1h(t *testing.T) {
	m, store, _, _, ch := setup(t, "running", true)
	store.CreateWindow(context.Background(), storage.MaintenanceWindow{
		Name: "kurzfristig", Start: time.Now().Add(3 * time.Minute), End: time.Now().Add(time.Hour),
	})
	m.Tick(context.Background())
	evs := drain(ch)
	if len(evs) != 1 || !strings.Contains(evs[0].Title, "Gleich geht's los") {
		t.Fatalf("events = %+v, want nur die 5m-Ankündigung (keine falsche 1h)", evs)
	}
}
