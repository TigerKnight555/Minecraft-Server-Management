package watchers

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/collector"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func drain(ch <-chan events.Event) []events.Event {
	var out []events.Event
	for {
		select {
		case ev := <-ch:
			out = append(out, ev)
		case <-time.After(30 * time.Millisecond):
			return out
		}
	}
}

func wan(rtt, loss float64, reached bool) collector.WANSample {
	return collector.WANSample{Targets: []collector.PingResult{
		{Target: "1.1.1.1", Reached: reached, RTTMs: rtt, LossPct: loss},
	}}
}

func TestNetHysteresis(t *testing.T) {
	bus := events.New()
	ch, cancel := bus.Subscribe(16)
	defer cancel()
	n := NewNet(nil, bus)
	n.Sustain = 3

	// 2x schlecht reicht nicht
	n.step(wan(200, 0, true))
	n.step(wan(200, 0, true))
	if evs := drain(ch); len(evs) != 0 {
		t.Fatalf("events = %+v, want keine vor Sustain", evs)
	}
	// 3. schlechte Messung kippt
	n.step(wan(200, 0, true))
	evs := drain(ch)
	if len(evs) != 1 || evs[0].Type != events.TypeNetDegraded {
		t.Fatalf("events = %+v, want net.degraded", evs)
	}
	// eine gute Messung entwarnt nicht
	n.step(wan(15, 0, true))
	n.step(wan(15, 0, true))
	if evs := drain(ch); len(evs) != 0 {
		t.Fatalf("events = %+v, want keine Entwarnung vor Sustain", evs)
	}
	n.step(wan(15, 0, true))
	evs = drain(ch)
	if len(evs) != 1 || evs[0].Type != events.TypeNetOK {
		t.Fatalf("events = %+v, want net.ok", evs)
	}
}

func TestNetTotalOutageCounts(t *testing.T) {
	bus := events.New()
	ch, cancel := bus.Subscribe(16)
	defer cancel()
	n := NewNet(nil, bus)
	n.Sustain = 2
	n.step(wan(0, 100, false))
	n.step(wan(0, 100, false))
	evs := drain(ch)
	if len(evs) != 1 || evs[0].Type != events.TypeNetDegraded {
		t.Fatalf("events = %+v, want degraded bei Komplettausfall", evs)
	}
}

func TestResourceThresholdWarnsOnceAndRearms(t *testing.T) {
	bus := events.New()
	ch, cancel := bus.Subscribe(16)
	defer cancel()
	r := NewResource(nil, bus)

	full := collector.HostSample{DiskTotal: 100, DiskUsed: 95, MemTotal: 100, MemUsed: 50}
	r.step(full)
	r.step(full) // zweiter Tick darf nicht erneut warnen
	evs := drain(ch)
	if len(evs) != 1 || evs[0].Type != events.TypeResource || !strings.Contains(evs[0].Title, "Festplatte") {
		t.Fatalf("events = %+v, want genau eine Disk-Warnung", evs)
	}
	// Erholung unter Re-Arm-Marge, dann erneut voll -> neue Warnung
	r.step(collector.HostSample{DiskTotal: 100, DiskUsed: 80, MemTotal: 100, MemUsed: 50})
	r.step(full)
	evs = drain(ch)
	if len(evs) != 1 {
		t.Fatalf("events = %+v, want erneute Warnung nach Re-Arm", evs)
	}
}

func TestCrashSnippetAndNewFileDetection(t *testing.T) {
	dir := t.TempDir()
	crashDir := filepath.Join(dir, "crash-reports")
	os.MkdirAll(crashDir, 0o755)
	os.WriteFile(filepath.Join(crashDir, "crash-alt.txt"), []byte("alt"), 0o644)

	bus := events.New()
	ch, cancel := bus.Subscribe(16)
	defer cancel()
	c := NewCrash(dir, bus, testLogger())
	c.Interval = 5 * time.Millisecond

	ctx, stop := contextWithTimeout(t)
	defer stop()
	go c.Run(ctx)
	time.Sleep(15 * time.Millisecond) // Bestand einlesen lassen

	report := "---- Minecraft Crash Report ----\nDescription: Ticking entity\n\njava.lang.NullPointerException: oops\n\tat net.minecraft.foo(Bar.java:1)\n"
	os.WriteFile(filepath.Join(crashDir, "crash-neu.txt"), []byte(report), 0o644)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		evs := drain(ch)
		if len(evs) > 0 {
			if evs[0].Type != events.TypeCrash || !strings.Contains(evs[0].Message, "NullPointerException") ||
				!strings.Contains(evs[0].Title, "crash-neu.txt") {
				t.Fatalf("event = %+v", evs[0])
			}
			return
		}
	}
	t.Fatal("kein Crash-Event für neue Datei")
}

func contextWithTimeout(t *testing.T) (ctx context.Context, cancel func()) {
	t.Helper()
	return context.WithTimeout(context.Background(), 3*time.Second)
}
