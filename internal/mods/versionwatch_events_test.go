package mods_test

import (
	"testing"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mock"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mods"
)

// drain collects everything currently in the channel (bus delivery is
// synchronous, so after SetLast returns the events are already buffered).
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

func watchStatus(latest string, loaderReady bool, ready, total int) *mods.WatchStatus {
	return &mods.WatchStatus{
		Checked: time.Now(), CurrentVersion: "1.21.11",
		LatestVersion: latest, NewerAvailable: true, LoaderReady: loaderReady,
		Profiles: []mods.ProfileReady{{Profile: "server", Ready: ready, Total: total}},
	}
}

func TestVersionTransitionsPublishOnlyChanges(t *testing.T) {
	mgr, _, _ := setup(t)
	w := mods.NewWatcher(mock.NewModrinth(), mgr, "fabric")
	bus := events.New()
	ch, cancel := bus.Subscribe(16)
	defer cancel()
	w.SetBus(bus)

	// 1. new release, mods not ready yet -> only version.new
	w.SetLast(watchStatus("1.22", true, 1, 3))
	got := drain(ch)
	if len(got) != 1 || got[0].Type != events.TypeVersionNew {
		t.Fatalf("erster Check: events = %+v, want genau version.new", got)
	}

	// 2. same release, unchanged -> nothing (daily check must not repeat)
	w.SetLast(watchStatus("1.22", true, 1, 3))
	if got := drain(ch); len(got) != 0 {
		t.Fatalf("unveränderter Check: events = %+v, want keine", got)
	}

	// 3. same release becomes fully ready -> only version.ready
	w.SetLast(watchStatus("1.22", true, 3, 3))
	got = drain(ch)
	if len(got) != 1 || got[0].Type != events.TypeVersionReady {
		t.Fatalf("bereit-Übergang: events = %+v, want genau version.ready", got)
	}

	// 4. next release appears and is instantly ready -> both events
	w.SetLast(watchStatus("1.23", true, 3, 3))
	got = drain(ch)
	if len(got) != 2 || got[0].Type != events.TypeVersionNew || got[1].Type != events.TypeVersionReady {
		t.Fatalf("neue+bereite Version: events = %+v, want version.new + version.ready", got)
	}
}

func TestEmptyInventoryIsNotReady(t *testing.T) {
	mgr, _, _ := setup(t)
	w := mods.NewWatcher(mock.NewModrinth(), mgr, "fabric")
	bus := events.New()
	ch, cancel := bus.Subscribe(16)
	defer cancel()
	w.SetBus(bus)

	// 0/0 mods = inventory never scanned -> version.new ja, version.ready nein
	w.SetLast(watchStatus("1.22", true, 0, 0))
	got := drain(ch)
	if len(got) != 1 || got[0].Type != events.TypeVersionNew {
		t.Fatalf("leeres Inventar: events = %+v, want nur version.new", got)
	}
}

func TestAnnounceStoreSurvivesRestart(t *testing.T) {
	mgr, _, _ := setup(t)
	bus := events.New()
	ch, cancel := bus.Subscribe(16)
	defer cancel()

	// persistenter Store, den zwei Watcher-Instanzen teilen (simuliert
	// MSM-Neustart durch den Nacht-Reboot)
	kv := map[string]string{}
	get := func(k string) string { return kv[k] }
	set := func(k, v string) { kv[k] = v }

	w1 := mods.NewWatcher(mock.NewModrinth(), mgr, "fabric")
	w1.SetBus(bus)
	w1.SetAnnounceStore(get, set)
	w1.SetLast(watchStatus("1.22", true, 3, 3))
	if got := drain(ch); len(got) != 2 {
		t.Fatalf("erste Instanz: events = %+v, want version.new + version.ready", got)
	}

	// "Neustart": frische Instanz, gleicher Store, gleiche Version
	w2 := mods.NewWatcher(mock.NewModrinth(), mgr, "fabric")
	w2.SetBus(bus)
	w2.SetAnnounceStore(get, set)
	w2.SetLast(watchStatus("1.22", true, 3, 3))
	if got := drain(ch); len(got) != 0 {
		t.Fatalf("nach Neustart: events = %+v, want keine Wiederholung", got)
	}

	// neue Version -> wieder Meldungen
	w2.SetLast(watchStatus("1.23", true, 3, 3))
	if got := drain(ch); len(got) != 2 {
		t.Fatalf("neue Version: events = %+v, want beide Meldungen", got)
	}
}
