package modnews

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mods"
)

type kv struct {
	mu sync.Mutex
	m  map[string]string
}

func (k *kv) get(key string) string { k.mu.Lock(); defer k.mu.Unlock(); return k.m[key] }
func (k *kv) set(key, v string)     { k.mu.Lock(); defer k.mu.Unlock(); k.m[key] = v }

func setup(t *testing.T, ready bool) (*Announcer, *kv, <-chan events.Event, *atomic.Int32, *atomic.Bool) {
	t.Helper()
	store := &kv{m: map[string]string{}}
	bus := events.New()
	ch, cancel := bus.Subscribe(16)
	t.Cleanup(cancel)
	online := &atomic.Bool{}
	online.Store(true)
	published := &atomic.Int32{}
	a := New(store.get, store.set, bus, online.Load, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var self *Announcer = a
	a.SetPublisher(func() bool { return ready }, func(context.Context) error {
		published.Add(1)
		self.Published()
		return nil
	})
	return a, store, ch, published, online
}

func next(t *testing.T, ch <-chan events.Event) (events.Event, bool) {
	t.Helper()
	select {
	case ev := <-ch:
		return ev, true
	case <-time.After(200 * time.Millisecond):
		return events.Event{}, false
	}
}

func add(profile, name, client string) mods.Change {
	return mods.Change{Action: "add", Profile: profile, Name: name, ProjectID: "p-" + name, ClientSide: client}
}

func TestAfterRestartAnnouncesAndPublishes(t *testing.T) {
	a, store, ch, published, _ := setup(t, true)
	a.OnApply(mods.ProfileServer, []mods.Change{add(mods.ProfileServer, "Waystones", "required"), add(mods.ProfileServer, "Lithium", "optional")})
	a.OnApply(mods.ProfileClient, []mods.Change{add(mods.ProfileClient, "Waystones", "required"), add(mods.ProfileClient, "Sodium", "required")})
	a.OnApply(mods.ProfileServer, []mods.Change{{Action: "remove", Profile: mods.ProfileServer, Name: "Altmod", Filename: "alt.jar"}})

	a.AfterRestart(context.Background())

	ev, ok := next(t, ch)
	if !ok || ev.Type != events.TypeModsChanged {
		t.Fatalf("event = %+v (ok=%v)", ev, ok)
	}
	for _, want := range []string{"Waystones — auch bei dir nötig", "Lithium — nur Server, nichts zu tun", "Sodium — nur bei dir (Client-Mod)", "Altmod — nichts zu tun"} {
		if !strings.Contains(ev.Message, want) {
			t.Errorf("Meldung fehlt %q:\n%s", want, ev.Message)
		}
	}
	if published.Load() != 1 {
		t.Errorf("published = %d, want 1", published.Load())
	}
	if store.get(keyDirty) != "" || store.get(keyServer) != "" || store.get(keyClient) != "" {
		t.Errorf("Zustand nicht geleert: %v", store.m)
	}
	// zweiter Neustart ohne Änderungen: still
	a.AfterRestart(context.Background())
	if ev, ok := next(t, ch); ok {
		t.Errorf("unerwartete Meldung: %+v", ev)
	}
	if published.Load() != 1 {
		t.Error("Paket ohne Änderung erneut verschickt")
	}
}

func TestUpdatesOnlyPublishPackWithoutModMessage(t *testing.T) {
	a, _, ch, published, _ := setup(t, true)
	a.OnApply(mods.ProfileClient, []mods.Change{{Action: "update", Profile: mods.ProfileClient, Name: "Sodium"}})
	a.OnApply(mods.ProfileServer, []mods.Change{{Action: "update", Profile: mods.ProfileServer, Name: "Lithium"}})
	a.AfterRestart(context.Background())
	if ev, ok := next(t, ch); ok {
		t.Errorf("Mod-Meldung bei reinen Updates: %+v", ev)
	}
	if published.Load() != 1 {
		t.Errorf("published = %d, want 1", published.Load())
	}
}

func TestWithoutDropboxHintAndDirtyStays(t *testing.T) {
	a, store, ch, published, _ := setup(t, false)
	a.OnApply(mods.ProfileClient, []mods.Change{add(mods.ProfileClient, "Sodium", "required")})
	a.AfterRestart(context.Background())
	ev, ok := next(t, ch)
	if !ok || !strings.Contains(ev.Message, "verteilt der Admin") {
		t.Fatalf("event = %+v", ev)
	}
	if published.Load() != 0 {
		t.Error("ohne Dropbox veröffentlicht")
	}
	if store.get(keyDirty) != "1" {
		t.Error("Markierung 'Paket veraltet' verloren")
	}
}

func TestPublishedByButtonConsumesClientList(t *testing.T) {
	a, _, ch, _, _ := setup(t, true)
	a.OnApply(mods.ProfileClient, []mods.Change{add(mods.ProfileClient, "Sodium", "required")})
	a.Published()
	a.AfterRestart(context.Background())
	if ev, ok := next(t, ch); ok {
		t.Errorf("schon verschickte Client-Änderung erneut gemeldet: %+v", ev)
	}
}

func TestRunTriggersOnlyAfterOfflineOnlineAndNotWhileBusy(t *testing.T) {
	a, _, ch, _, online := setup(t, true)
	a.Poll = 5 * time.Millisecond
	busy := &atomic.Bool{}
	a.SetBusy(busy.Load)
	a.OnApply(mods.ProfileServer, []mods.Change{add(mods.ProfileServer, "Lithium", "optional")})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx)

	if _, ok := next(t, ch); ok {
		t.Fatal("Meldung ohne Neustart")
	}
	busy.Store(true)
	online.Store(false)
	time.Sleep(30 * time.Millisecond)
	online.Store(true)
	if _, ok := next(t, ch); ok {
		t.Fatal("Meldung während eines laufenden Ein-Klick-Updates")
	}
	busy.Store(false)
	online.Store(false)
	time.Sleep(30 * time.Millisecond)
	online.Store(true)
	if ev, ok := next(t, ch); !ok || ev.Type != events.TypeModsChanged {
		t.Fatalf("keine Meldung nach Neustart: %+v", ev)
	}
}
