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

func watchStatus(latest string, loaderReady bool, srvReady, srvTotal int) *mods.WatchStatus {
	return &mods.WatchStatus{
		Checked: time.Now(), CurrentVersion: "1.21.11",
		LatestVersion: latest, NewerAvailable: true, LoaderReady: loaderReady,
		Profiles: []mods.ProfileReady{
			{Profile: "server", Ready: srvReady, Total: srvTotal},
			{Profile: "client", Ready: 0, Total: 5}, // Client-Nachzügler blockieren nie
		},
	}
}

// Nutzerwunsch: der Channel ist für Spieler — es gibt genau EINE Meldung pro
// Release, und zwar erst wenn Loader + ALLE Server-Mods bereit sind.
func TestVersionMessageOnlyWhenServerModsReady(t *testing.T) {
	mgr, _, _ := setup(t)
	w := mods.NewWatcher(mock.NewModrinth(), mgr, "fabric")
	bus := events.New()
	ch, cancel := bus.Subscribe(16)
	defer cancel()
	w.SetBus(bus)

	// neue Version, Server-Mods noch nicht durch -> KEINE Meldung
	w.SetLast(watchStatus("1.22", true, 1, 3))
	if got := drain(ch); len(got) != 0 {
		t.Fatalf("unfertig: events = %+v, want keine", got)
	}
	// Loader fehlt -> ebenfalls keine
	w.SetLast(watchStatus("1.22", false, 3, 3))
	if got := drain(ch); len(got) != 0 {
		t.Fatalf("ohne Loader: events = %+v, want keine", got)
	}
	// jetzt alles bereit -> genau eine version.ready
	w.SetLast(watchStatus("1.22", true, 3, 3))
	got := drain(ch)
	if len(got) != 1 || got[0].Type != events.TypeVersionReady {
		t.Fatalf("bereit: events = %+v, want genau version.ready", got)
	}
	// unverändert bereit -> keine Wiederholung
	w.SetLast(watchStatus("1.22", true, 3, 3))
	if got := drain(ch); len(got) != 0 {
		t.Fatalf("wiederholt: events = %+v, want keine", got)
	}
	// nächste Version sofort bereit -> wieder eine Meldung
	w.SetLast(watchStatus("1.23", true, 3, 3))
	if got := drain(ch); len(got) != 1 {
		t.Fatalf("neue Version: events = %+v, want eine", got)
	}
}

func TestEmptyInventoryIsNotReady(t *testing.T) {
	mgr, _, _ := setup(t)
	w := mods.NewWatcher(mock.NewModrinth(), mgr, "fabric")
	bus := events.New()
	ch, cancel := bus.Subscribe(16)
	defer cancel()
	w.SetBus(bus)

	// 0/0 Server-Mods = Inventar nie gescannt -> keine Meldung
	w.SetLast(watchStatus("1.22", true, 0, 0))
	if got := drain(ch); len(got) != 0 {
		t.Fatalf("leeres Inventar: events = %+v, want keine", got)
	}
}

func TestAnnounceStoreSurvivesRestart(t *testing.T) {
	mgr, _, _ := setup(t)
	bus := events.New()
	ch, cancel := bus.Subscribe(16)
	defer cancel()

	kv := map[string]string{}
	get := func(k string) string { return kv[k] }
	set := func(k, v string) { kv[k] = v }

	w1 := mods.NewWatcher(mock.NewModrinth(), mgr, "fabric")
	w1.SetBus(bus)
	w1.SetAnnounceStore(get, set)
	w1.SetLast(watchStatus("1.22", true, 3, 3))
	if got := drain(ch); len(got) != 1 {
		t.Fatalf("erste Instanz: events = %+v, want version.ready", got)
	}

	// "Neustart": frische Instanz, gleicher Store -> keine Wiederholung
	w2 := mods.NewWatcher(mock.NewModrinth(), mgr, "fabric")
	w2.SetBus(bus)
	w2.SetAnnounceStore(get, set)
	w2.SetLast(watchStatus("1.22", true, 3, 3))
	if got := drain(ch); len(got) != 0 {
		t.Fatalf("nach Neustart: events = %+v, want keine Wiederholung", got)
	}

	w2.SetLast(watchStatus("1.23", true, 3, 3))
	if got := drain(ch); len(got) != 1 {
		t.Fatalf("neue Version: events = %+v, want eine Meldung", got)
	}
}
