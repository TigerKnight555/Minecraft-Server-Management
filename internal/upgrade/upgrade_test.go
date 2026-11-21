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
	// clientCheckErr lässt den Client-Mod-Check nach dem Sprung scheitern
	clientCheckErr error
	emptyClient    bool         // Client-Paket ohne Dateien
	version        atomic.Value // string: was der "Server" nach dem Upgrade meldet
	online         atomic.Bool
	staleStatus    atomic.Bool // mcStatus liefert einen veralteten Snapshot
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
	if profile == "client" {
		if f.clientCheckErr != nil {
			return nil, f.clientCheckErr
		}
		if f.emptyClient {
			return nil, nil
		}
		return []mods.Entry{{Profile: "client", Filename: "sodium.jar"}}, nil
	}
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
	at := time.Now()
	if f.staleStatus.Load() {
		at = at.Add(-time.Hour) // Snapshot von vor dem Stopp
	}
	return collector.MCStatus{Online: f.online.Load(), Version: v, Time: at}
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

// Scheitert der Client-Weg, darf das nicht mehr stumm bleiben: kein
// Paket-Upload, aber ein sichtbarer Hinweis samt Details in der Meldung.
func TestClientModFailureIsReported(t *testing.T) {
	f := &fakes{backupOK: true, clientCheckErr: errors.New("modrinth 503")}
	f.online.Store(true)
	bus := events.New()
	ch, cancel := bus.Subscribe(16)
	defer cancel()
	o := newOrch(f, bus)
	published := false
	o.Publish = func(context.Context) (bool, error) { published = true; return true, nil }

	if err := o.Start("26.2"); err != nil {
		t.Fatal(err)
	}
	waitDone(t, o)

	if published {
		t.Error("Paket veröffentlicht, obwohl die Client-Mods nicht aktualisiert wurden")
	}
	var ok *events.Event
	for _, ev := range drain(ch) {
		if ev.Type == events.TypeUpgradeOK {
			ev := ev
			ok = &ev
		}
	}
	if ok == nil {
		t.Fatal("keine Erfolgsmeldung — der Server-Sprung selbst hat geklappt")
	}
	if !strings.Contains(ok.Message, "Client-Mods konnten nicht") {
		t.Errorf("kein Hinweis für die Spieler: %q", ok.Message)
	}
	if len(ok.Fields) == 0 || !strings.Contains(ok.Fields[0].Value, "modrinth 503") {
		t.Errorf("Fehlerdetails fehlen: %+v", ok.Fields)
	}
}

// Client-Nachzügler aus dem Versions-Watch werden in der Erfolgsmeldung
// genannt — sonst starten Spieler mit inkompatiblen Mods.
func TestClientStragglersAreNamed(t *testing.T) {
	f := &fakes{backupOK: true}
	f.online.Store(true)
	bus := events.New()
	ch, cancel := bus.Subscribe(16)
	defer cancel()
	st := readyFor("26.2", 0)
	st.Stragglers = map[string]string{"Xaero's Minimap": "client", "Lithium": "server"}
	o := New(f, f, f, f.mcStatus, f, f, fixedReadiness{st}, f, bus, "mc-fabric", testLogger())
	o.WarnMinutes = 1
	o.WarnStep = time.Millisecond
	o.PollStep = time.Millisecond

	if err := o.Start("26.2"); err != nil {
		t.Fatal(err)
	}
	waitDone(t, o)

	for _, ev := range drain(ch) {
		if ev.Type != events.TypeUpgradeOK {
			continue
		}
		if !strings.Contains(ev.Message, "Xaero's Minimap") {
			t.Errorf("Client-Nachzügler nicht genannt: %q", ev.Message)
		}
		if strings.Contains(ev.Message, "Lithium") {
			t.Errorf("Server-Mod als Client-Nachzügler genannt: %q", ev.Message)
		}
		return
	}
	t.Fatal("keine Erfolgsmeldung")
}

// Ein leeres Client-Paket ist kein Fehler — kein Stage, kein Fehlalarm.
func TestEmptyClientPackIsNoError(t *testing.T) {
	f := &fakes{backupOK: true, emptyClient: true}
	f.online.Store(true)
	bus := events.New()
	ch, cancel := bus.Subscribe(16)
	defer cancel()
	o := newOrch(f, bus)

	if err := o.Start("26.2"); err != nil {
		t.Fatal(err)
	}
	waitDone(t, o)

	for _, s := range f.list() {
		if s == "stage:client" {
			t.Error("Stage für leeres Client-Paket aufgerufen")
		}
	}
	for _, ev := range drain(ch) {
		if ev.Type == events.TypeUpgradeOK && (strings.Contains(ev.Message, "konnten nicht") || len(ev.Fields) > 0) {
			t.Errorf("Fehlalarm bei leerem Client-Paket: %q %+v", ev.Message, ev.Fields)
		}
	}
}

// Nachzügler aus einem Watch-Lauf für eine andere Version werden ignoriert.
func TestStaleStragglersIgnored(t *testing.T) {
	f := &fakes{backupOK: true}
	st := readyFor("26.2", 0)
	st.Stragglers = map[string]string{"Xaero's Minimap": "client"}
	o := New(f, f, f, f.mcStatus, f, f, fixedReadiness{st}, f, events.New(), "mc-fabric", testLogger())
	if got := o.clientStragglers("26.3", nil); len(got) != 0 {
		t.Errorf("Nachzügler einer anderen Zielversion übernommen: %v", got)
	}
	if got := o.clientStragglers("26.2", nil); len(got) != 1 {
		t.Errorf("Nachzügler der Zielversion fehlen: %v", got)
	}
}

// Hat der Client-Check gerade einen Build zur Zielversion gefunden, ist der
// Mod kein Nachzügler mehr — auch wenn der ältere Watch-Lauf das behauptet.
func TestFreshlyUpdatedModIsNoStraggler(t *testing.T) {
	f := &fakes{backupOK: true}
	st := readyFor("26.2", 0)
	st.Stragglers = map[string]string{"Xaero's Minimap": "client", "Iris": "client"}
	o := New(f, f, f, f.mcStatus, f, f, fixedReadiness{st}, f, events.New(), "mc-fabric", testLogger())
	got := o.clientStragglers("26.2", map[string]bool{"Iris": true})
	if fmt.Sprint(got) != "[Xaero's Minimap]" {
		t.Errorf("Nachzügler = %v, want nur Xaero's Minimap", got)
	}
}

// Der Download-Link wird nur angekündigt, wenn das Paket wirklich
// veröffentlicht wurde.
func TestDownloadLinkOnlyAnnouncedWhenPublished(t *testing.T) {
	for _, tc := range []struct {
		name      string
		publish   func(context.Context) (bool, error)
		want, not string
	}{
		{"veröffentlicht", func(context.Context) (bool, error) { return true, nil }, "Download-Link", "verteilt der Admin"},
		{"nicht eingerichtet", func(context.Context) (bool, error) { return false, nil }, "verteilt der Admin", "Download-Link"},
		{"ohne Publish", nil, "verteilt der Admin", "Download-Link"},
		{"Upload-Fehler", func(context.Context) (bool, error) { return true, errors.New("dropbox weg") }, "Paket-Upload fehlgeschlagen", "Download-Link findet"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakes{backupOK: true}
			f.online.Store(true)
			bus := events.New()
			ch, cancel := bus.Subscribe(16)
			defer cancel()
			o := newOrch(f, bus)
			o.Publish = tc.publish
			if err := o.Start("26.2"); err != nil {
				t.Fatal(err)
			}
			waitDone(t, o)
			for _, ev := range drain(ch) {
				if ev.Type != events.TypeUpgradeOK {
					continue
				}
				if !strings.Contains(ev.Message, tc.want) || strings.Contains(ev.Message, tc.not) {
					t.Errorf("Meldung = %q (want %q, nicht %q)", ev.Message, tc.want, tc.not)
				}
				return
			}
			t.Fatal("keine Erfolgsmeldung")
		})
	}
}

func TestUpgradeRefusesWithPendingModChanges(t *testing.T) {
	f := &fakes{backupOK: true}
	f.online.Store(true)
	o := newOrch(f, events.New())
	o.PendingChanges = func() bool { return true }
	err := o.Start("26.2")
	if err == nil || !strings.Contains(err.Error(), "vorgemerkt") {
		t.Fatalf("err = %v, want Hinweis auf vorgemerkte Änderungen", err)
	}
	if steps := f.list(); len(steps) != 0 {
		t.Errorf("Kette lief trotzdem an: %v", steps)
	}
}

func TestUpgradeRefusesWhileRoutineRuns(t *testing.T) {
	f := &fakes{backupOK: true}
	f.online.Store(true)
	o := newOrch(f, events.New())
	o.RoutineBusy = func() string { return "Nacht-Backup" }
	err := o.Start("26.2")
	if err == nil || !strings.Contains(err.Error(), "Nacht-Backup") {
		t.Fatalf("err = %v, want Hinweis auf laufende Routine", err)
	}
	if steps := f.list(); len(steps) != 0 {
		t.Errorf("Kette lief trotzdem an: %v", steps)
	}
}

// Nach einem erfolgreichen Sprung zeigte der Versions-Watch bis zu 24 h den
// alten Stand ("26.3 verfügbar, aktuell 1.21.11") — der Knopf bot dasselbe
// Update erneut an (WSL-Test 2026-11-09). OnSuccess stößt die Neuprüfung an.
func TestUpgradeCallsOnSuccess(t *testing.T) {
	f := &fakes{backupOK: true}
	f.online.Store(true)
	o := newOrch(f, events.New())
	var got atomic.Value
	o.OnSuccess = func(_ context.Context, v string) { got.Store(v) }
	if err := o.Start("26.2"); err != nil {
		t.Fatal(err)
	}
	waitDone(t, o)
	if v, _ := got.Load().(string); v != "26.2" {
		t.Errorf("OnSuccess version = %q, want 26.2", v)
	}
}

// Läuft der Server schon auf der Zielversion (veralteter Versions-Watch),
// darf die Kette nicht noch einmal starten.
func TestUpgradeRefusesWhenAlreadyOnTarget(t *testing.T) {
	f := &fakes{backupOK: true}
	f.online.Store(true)
	f.version.Store("26.2")
	o := newOrch(f, events.New())
	err := o.Start("26.2")
	if err == nil || !strings.Contains(err.Error(), "läuft bereits auf") {
		t.Fatalf("err = %v, want Hinweis 'läuft bereits auf'", err)
	}
	if steps := f.list(); len(steps) != 0 {
		t.Errorf("Kette lief trotzdem an: %v", steps)
	}
}

// Ein gelungener Sprung versiegelt seine Mod-Sätze — ein späterer
// Rollback-Klick darf nicht Mods der alten Version einsetzen.
func TestUpgradeSealsModSets(t *testing.T) {
	f := &fakes{backupOK: true}
	f.online.Store(true)
	o := newOrch(f, events.New())
	var mu sync.Mutex
	var sealed []string
	o.ModSeal = func(profile, label, version string) error {
		mu.Lock()
		defer mu.Unlock()
		sealed = append(sealed, profile+"@"+version)
		return nil
	}
	if err := o.Start("26.2"); err != nil {
		t.Fatal(err)
	}
	waitDone(t, o)
	mu.Lock()
	defer mu.Unlock()
	if len(sealed) == 0 || sealed[0] != "server@26.2" {
		t.Errorf("sealed = %v, want server@26.2 (und ggf. client)", sealed)
	}
}

// Auch ohne geänderte Client-Mods wird das Paket verteilt: das Modpack muss
// die neue MC-Version deklarieren. Der Spieler-Hinweis folgt dem, was
// Publish meldet (📦-Meldung ja/nein).
func TestPublishWithoutClientChange(t *testing.T) {
	for _, tc := range []struct {
		name      string
		announced bool
		want, not string
	}{
		{"Modpack verschickt", true, "Meldung „📦 Neues Mod-Paket“", "verteilt der Admin"},
		{"nichts verschickt", false, "", "Mod-Paket"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakes{backupOK: true, applyErr: mods.ErrNothingStaged}
			f.online.Store(true)
			bus := events.New()
			ch, cancel := bus.Subscribe(16)
			defer cancel()
			o := newOrch(f, bus)
			calls := 0
			o.Publish = func(context.Context) (bool, error) { calls++; return tc.announced, nil }
			if err := o.Start("26.2"); err != nil {
				t.Fatal(err)
			}
			waitDone(t, o)
			if calls != 1 {
				t.Fatalf("Publish %d× aufgerufen, want 1 (auch ohne Client-Änderung)", calls)
			}
			for _, ev := range drain(ch) {
				if ev.Type != events.TypeUpgradeOK {
					continue
				}
				if !strings.Contains(ev.Message, tc.want) || strings.Contains(ev.Message, tc.not) {
					t.Errorf("Meldung = %q (want %q, nicht %q)", ev.Message, tc.want, tc.not)
				}
				return
			}
			t.Fatal("keine Erfolgsmeldung")
		})
	}
}
