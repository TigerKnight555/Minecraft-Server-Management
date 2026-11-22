package upgrade

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mods"
)

// fixedReadiness liefert einen frei wählbaren Versions-Watch-Stand.
type fixedReadiness struct{ st *mods.WatchStatus }

func (r fixedReadiness) Last() *mods.WatchStatus { return r.st }

func readyFor(version string, requiredJava int) *mods.WatchStatus {
	return &mods.WatchStatus{
		CurrentVersion: "1.21.11", LatestVersion: version,
		NewerAvailable: true, LoaderReady: true,
		Profiles:     []mods.ProfileReady{{Profile: "server", Ready: 3, Total: 3}},
		RequiredJava: requiredJava,
	}
}

func orchWithJava(t *testing.T, st *mods.WatchStatus, probe func(context.Context) int) (*Orchestrator, *fakes) {
	t.Helper()
	f := &fakes{backupOK: true}
	o := New(f, f, f, f.mcStatus, f, f, fixedReadiness{st}, f, events.New(), "mc-fabric", testLogger())
	// gleiche Kurzzeiten wie newOrch, sonst wartet der Test auf echte Minuten
	o.WarnMinutes = 1
	o.WarnStep = time.Millisecond
	o.OnlineTimeout = 2 * time.Second
	o.PollStep = 5 * time.Millisecond
	o.JavaProbe = probe
	return o, f
}

// Der Fall vom 29.07.: MC 26.2 verlangt Java 25, das Image lieferte Java 21.
// Früher blockierte MSM hier. Seit der Host-Watcher beim Sprung selbst
// `docker compose pull mc-fabric` ausführt, kommt das neue Java mit dem
// Update — die Kette läuft, und das Start-Event nennt den Java-Wechsel.
func TestStartUpdatesJavaInsteadOfBlocking(t *testing.T) {
	f := &fakes{backupOK: true}
	bus := events.New()
	ch, cancel := bus.Subscribe(64)
	defer cancel()
	o := New(f, f, f, f.mcStatus, f, f, fixedReadiness{readyFor("26.2", 25)}, f, bus, "mc-fabric", testLogger())
	o.WarnMinutes = 1
	o.WarnStep = time.Millisecond
	o.OnlineTimeout = 2 * time.Second
	o.PollStep = 5 * time.Millisecond
	o.JavaProbe = func(context.Context) int { return 21 }

	if err := o.Start("26.2"); err != nil {
		t.Fatalf("Upgrade blockiert, obwohl das neue Image Java mitbringt: %v", err)
	}
	waitDone(t, o)
	var start *events.Event
	for len(ch) > 0 {
		ev := <-ch
		if ev.Type == events.TypeUpgradeStart {
			start = &ev
			break
		}
	}
	if start == nil {
		t.Fatal("kein Start-Event")
	}
	found := false
	for _, fl := range start.Fields {
		if fl.Name == "Java" && strings.Contains(fl.Value, "21") && strings.Contains(fl.Value, "25") && strings.Contains(fl.Value, "Server-Image") {
			found = true
		}
	}
	if !found {
		t.Errorf("Start-Event nennt den Java-Wechsel nicht: %+v", start.Fields)
	}
}

func TestStartAllowsMatchingJava(t *testing.T) {
	o, _ := orchWithJava(t, readyFor("26.2", 25), func(context.Context) int { return 25 })
	if err := o.Start("26.2"); err != nil {
		t.Fatalf("Upgrade blockiert, obwohl Java 25 vorhanden: %v", err)
	}
	waitDone(t, o)
}

// Neueres Java als gefordert ist in Ordnung.
func TestStartAllowsNewerJava(t *testing.T) {
	o, _ := orchWithJava(t, readyFor("26.2", 21), func(context.Context) int { return 25 })
	if err := o.Start("26.2"); err != nil {
		t.Fatalf("Upgrade blockiert, obwohl Java 25 > gefordert 21: %v", err)
	}
	waitDone(t, o)
}

// Unbekannte Werte dürfen nicht blockieren — lieber durchlassen als den
// Nutzer grundlos aussperren (Watchdog fängt den Rest ab).
func TestStartNotBlockedWhenJavaUnknown(t *testing.T) {
	t.Run("Anforderung unbekannt", func(t *testing.T) {
		o, _ := orchWithJava(t, readyFor("26.2", 0), func(context.Context) int { return 21 })
		if err := o.Start("26.2"); err != nil {
			t.Fatalf("blockiert trotz unbekannter Anforderung: %v", err)
		}
		waitDone(t, o)
	})
	t.Run("Image-Java unbekannt", func(t *testing.T) {
		o, _ := orchWithJava(t, readyFor("26.2", 25), func(context.Context) int { return 0 })
		if err := o.Start("26.2"); err != nil {
			t.Fatalf("blockiert trotz unbekannter Image-Version: %v", err)
		}
		waitDone(t, o)
	})
	t.Run("kein Probe verdrahtet", func(t *testing.T) {
		o, _ := orchWithJava(t, readyFor("26.2", 25), nil)
		if err := o.Start("26.2"); err != nil {
			t.Fatalf("blockiert ohne Probe: %v", err)
		}
		waitDone(t, o)
	})
}

// Passt Java schon, steht im Start-Event kein Java-Hinweis.
func TestStartEventWithoutJavaFieldWhenJavaMatches(t *testing.T) {
	f := &fakes{backupOK: true}
	bus := events.New()
	ch, cancel := bus.Subscribe(64)
	defer cancel()
	o := New(f, f, f, f.mcStatus, f, f, fixedReadiness{readyFor("26.2", 25)}, f, bus, "mc-fabric", testLogger())
	o.WarnMinutes = 1
	o.WarnStep = time.Millisecond
	o.OnlineTimeout = 2 * time.Second
	o.PollStep = 5 * time.Millisecond
	o.JavaProbe = func(context.Context) int { return 25 }
	if err := o.Start("26.2"); err != nil {
		t.Fatal(err)
	}
	waitDone(t, o)
	for len(ch) > 0 {
		ev := <-ch
		if ev.Type == events.TypeUpgradeStart {
			for _, fl := range ev.Fields {
				if fl.Name == "Java" {
					t.Errorf("Java-Hinweis trotz passender Version: %+v", fl)
				}
			}
			return
		}
	}
	t.Fatal("kein Start-Event")
}
