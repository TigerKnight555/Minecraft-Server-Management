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
		gen := self.BeginPublish()
		published.Add(1)
		self.Published(gen)
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
	for _, want := range []string{"Waystones — auch bei dir nötig", "Lithium — nur Server, nichts zu tun", "Sodium — nur bei dir (Client-Paket)", "Altmod — nichts zu tun"} {
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

// Nutzer-Entscheidung: der Button-Versand löscht nur „Paket veraltet" —
// die Client-Änderungen werden trotzdem beim nächsten Neustart gemeldet.
func TestPublishedByButtonKeepsClientListForRestart(t *testing.T) {
	a, store, ch, published, _ := setup(t, true)
	a.OnApply(mods.ProfileClient, []mods.Change{add(mods.ProfileClient, "Sodium", "required")})
	a.Published(a.BeginPublish())
	if store.get(keyDirty) != "" {
		t.Error("Markierung 'Paket veraltet' nach Versand nicht gelöscht")
	}
	a.AfterRestart(context.Background())
	ev, ok := next(t, ch)
	if !ok || !strings.Contains(ev.Message, "Sodium") {
		t.Fatalf("Client-Änderung nach Button-Versand nicht gemeldet: %+v (ok=%v)", ev, ok)
	}
	if published.Load() != 0 {
		t.Error("Paket ist aktuell — kein erneuter Versand nötig")
	}
	if store.get(keyClient) != "" {
		t.Error("Liste nach der Meldung nicht geleert")
	}
}

// Kommt während des Versands eine Client-Änderung, bleibt das Paket veraltet.
func TestPublishedKeepsDirtyWhenChangedDuringUpload(t *testing.T) {
	a, store, _, _, _ := setup(t, true)
	a.OnApply(mods.ProfileClient, []mods.Change{add(mods.ProfileClient, "Sodium", "required")})
	gen := a.BeginPublish()
	a.OnApply(mods.ProfileClient, []mods.Change{add(mods.ProfileClient, "Iris", "required")})
	a.Published(gen)
	if store.get(keyDirty) != "1" {
		t.Error("Änderung während des Uploads: Paket muss veraltet bleiben")
	}
	a.Published(a.BeginPublish())
	if store.get(keyDirty) != "" {
		t.Error("Versand ohne neue Änderung muss die Markierung löschen")
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

// Der Paket-Versand nach dem Neustart hat ein Zeitlimit.
func TestAfterRestartPublishHasTimeout(t *testing.T) {
	store := &kv{m: map[string]string{}}
	a := New(store.get, store.set, events.New(), func() bool { return true }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var deadline time.Time
	a.SetPublisher(func() bool { return true }, func(ctx context.Context) error {
		deadline, _ = ctx.Deadline()
		return nil
	})
	a.OnApply(mods.ProfileClient, []mods.Change{{Action: "update", Profile: mods.ProfileClient, Name: "Sodium"}})
	a.AfterRestart(context.Background())
	if deadline.IsZero() || time.Until(deadline) > 45*time.Minute {
		t.Errorf("deadline = %v, want ≤ 45 min", deadline)
	}
}

// Zurückgerollte, noch nicht angekündigte Änderungen dürfen beim nächsten
// Neustart nicht als neu gemeldet werden (WSL-Test 2026-11-10: „Neu:
// rb.zip … Entfernt: Sodium", obwohl beides zurückgerollt war).
func TestRollbackCancelsPendingNews(t *testing.T) {
	a, store, ch, _, _ := setup(t, true)
	up := mods.Change{Action: "add", Profile: mods.ProfileClient, Name: "rb.zip", Filename: "rb.zip"}
	so := mods.Change{Action: "remove", Profile: mods.ProfileClient, Name: "Sodium", Filename: "sodium.jar"}
	keep := mods.Change{Action: "add", Profile: mods.ProfileClient, Name: "Jade", Filename: "jade.jar"}
	a.OnApply(mods.ProfileClient, []mods.Change{up, keep})
	a.OnApply(mods.ProfileClient, []mods.Change{so})
	// Rollback meldet die Umkehrung
	a.OnApply(mods.ProfileClient, []mods.Change{{Action: "add", Profile: mods.ProfileClient, Name: "Sodium", Filename: "sodium.jar"}})
	a.OnApply(mods.ProfileClient, []mods.Change{{Action: "remove", Profile: mods.ProfileClient, Name: "rb.zip", Filename: "rb.zip"}})

	a.AfterRestart(context.Background())
	ev, ok := next(t, ch)
	if !ok {
		t.Fatal("keine Meldung — Jade ist weiterhin neu")
	}
	if !strings.Contains(ev.Message, "Jade") || strings.Contains(ev.Message, "rb.zip") || strings.Contains(ev.Message, "Sodium") {
		t.Errorf("Meldung = %q, want nur Jade", ev.Message)
	}
	_ = store
}

// Upload + Rollback: Paket gilt als veraltet, ist inhaltlich aber gleich
// dem zuletzt verschickten — kein erneuter Versand.
func TestNoRepublishWhenPackContentUnchanged(t *testing.T) {
	a, store, _, published, _ := setup(t, true)
	content := "A"
	a.SetFingerprint(func() string { return content })
	a.Published(a.BeginPublish()) // Stand A verschickt

	a.OnApply(mods.ProfileClient, nil) // Upload …
	content = "B"
	a.OnApply(mods.ProfileClient, nil) // … und Rollback
	content = "A"
	a.AfterRestart(context.Background())
	if published.Load() != 0 {
		t.Errorf("published = %d, Paket unverändert — kein Versand erwartet", published.Load())
	}
	if store.get(keyDirty) != "" {
		t.Error("dirty-Flag bleibt stehen")
	}

	a.OnApply(mods.ProfileClient, nil)
	content = "C" // echte Änderung
	a.AfterRestart(context.Background())
	if published.Load() != 1 {
		t.Errorf("published = %d, geänderter Inhalt muss verschickt werden", published.Load())
	}
}

// Waystones für beide Seiten: Client sofort drin, Server noch vorgemerkt.
// Ein Neustart ohne Einspielen darf den Client-Teil nicht allein melden;
// nach dem Einspielen kommt eine gemeinsame Zeile „auch bei dir nötig".
func TestClientPartHeldWhileServerPartPending(t *testing.T) {
	a, _, ch, _, _ := setup(t, true)
	pending := map[string]bool{"p-Waystones": true}
	a.SetPendingServer(func() map[string]bool { return pending })
	a.OnApply(mods.ProfileClient, []mods.Change{add(mods.ProfileClient, "Waystones", "required"), add(mods.ProfileClient, "Mod Menu", "required")})

	a.AfterRestart(context.Background())
	ev, ok := next(t, ch)
	if !ok || strings.Contains(ev.Message, "Waystones") || !strings.Contains(ev.Message, "Mod Menu") {
		t.Fatalf("1. Neustart: %+v, want nur Mod Menu", ev)
	}
	if ev.Title != "🧩 Neu im Mod-Paket" {
		t.Errorf("Titel = %q (nur Client-Änderungen)", ev.Title)
	}

	pending = map[string]bool{}
	a.OnApply(mods.ProfileServer, []mods.Change{add(mods.ProfileServer, "Waystones", "required")})
	a.AfterRestart(context.Background())
	ev, ok = next(t, ch)
	if !ok || !strings.Contains(ev.Message, "Waystones — auch bei dir nötig") {
		t.Fatalf("2. Neustart: %+v, want Waystones — auch bei dir nötig", ev)
	}
}

// Server-Teil allein, Modrinth sagt client required (Client-Teil früher
// verteilt oder bewusst nicht ins Paket): nicht „nichts zu tun".
func TestServerOnlyButClientRequiredSaysNeeded(t *testing.T) {
	a, _, ch, _, _ := setup(t, true)
	a.OnApply(mods.ProfileServer, []mods.Change{add(mods.ProfileServer, "Balm", "required"), add(mods.ProfileServer, "Lithium", "optional")})
	a.AfterRestart(context.Background())
	ev, _ := next(t, ch)
	if !strings.Contains(ev.Message, "Balm — auch bei dir nötig") || !strings.Contains(ev.Message, "Lithium — nur Server, nichts zu tun") {
		t.Errorf("Meldung = %q", ev.Message)
	}
}

// Server-Rollback eines Mods, der im Client-Paket bleibt: kein „bitte
// auch bei dir entfernen" (WSL-Test 2026-11-12).
func TestServerRemovalOfClientRequiredSaysNothingToDo(t *testing.T) {
	a, _, ch, _, _ := setup(t, true)
	a.OnApply(mods.ProfileServer, []mods.Change{{Action: "remove", Profile: mods.ProfileServer, Name: "Waystones", Filename: "w.jar", ProjectID: "p-w", ClientSide: "required"}})
	a.AfterRestart(context.Background())
	ev, _ := next(t, ch)
	if !strings.Contains(ev.Message, "Waystones — nichts zu tun") {
		t.Errorf("Meldung = %q", ev.Message)
	}
}

func TestPackChangeIsOneLine(t *testing.T) {
	a, _, ch, _, _ := setup(t, true)
	a.OnApply(mods.ProfileClient, []mods.Change{{Action: "pack", Profile: mods.ProfileClient, Name: "Fabulously Optimized 6.4.0 → 6.5.0 (3 neu, 1 entfernt, 12 aktualisiert)"}})
	a.AfterRestart(context.Background())
	ev, ok := next(t, ch)
	if !ok || !strings.Contains(ev.Message, "**Basis-Modpack:**") || !strings.Contains(ev.Message, "• Fabulously Optimized 6.4.0 → 6.5.0") {
		t.Fatalf("event = %+v", ev)
	}
	if ev.Title != "🧩 Mod-Paket aktualisiert" {
		t.Errorf("Titel = %q", ev.Title)
	}
}

// WSL-Live-Test 2026-11-22: Pack-Update + abgeschaltete Pack-Mod lief als
// „Aus dem Mod-Paket entfernt", und „Basis-Modpack" stand doppelt da.
func TestPackLineTitleAndNoDoublePrefix(t *testing.T) {
	a, _, ch, _, _ := setup(t, true)
	a.OnApply(mods.ProfileClient, []mods.Change{
		{Action: "pack", Profile: mods.ProfileClient, Name: "Basis-Modpack Fabulously Optimized 6.4.0 übernommen (40 Mods)"},
		{Action: "remove", Profile: mods.ProfileClient, Name: "BetterGrassify", Filename: "bg.jar", ProjectID: "p-bg"},
	})
	a.AfterRestart(context.Background())
	ev, ok := next(t, ch)
	if !ok {
		t.Fatal("keine Meldung")
	}
	if ev.Title != "🧩 Mod-Paket aktualisiert" {
		t.Errorf("Titel = %q", ev.Title)
	}
	if strings.Contains(ev.Message, "• Basis-Modpack ") || !strings.Contains(ev.Message, "• Fabulously Optimized 6.4.0 übernommen (40 Mods)") {
		t.Errorf("Meldung = %q", ev.Message)
	}
}
