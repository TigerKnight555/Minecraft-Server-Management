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
// Die Kette darf gar nicht erst loslaufen.
func TestStartBlocksWhenImageJavaTooOld(t *testing.T) {
	o, f := orchWithJava(t, readyFor("26.2", 25), func(context.Context) int { return 21 })

	err := o.Start("26.2")
	if err == nil {
		t.Fatal("Upgrade gestartet, obwohl das Image nur Java 21 hat")
	}
	for _, want := range []string{"Java 25", "Java 21", "pull"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Meldung nennt %q nicht: %v", want, err)
		}
	}
	if steps := f.list(); len(steps) != 0 {
		t.Errorf("Kette hat trotz Guard gearbeitet: %v", steps)
	}
	if o.Active() {
		t.Error("Orchestrator meldet sich als aktiv, obwohl der Guard griff")
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
