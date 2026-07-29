package watchers

import (
	"context"
	"testing"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/collector"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
)

// fakeContainers liefert einen steuerbaren Container-Zustand.
type fakeContainers struct{ state string }

func (f *fakeContainers) Containers() []collector.Container {
	return []collector.Container{{ID: "abc", Name: "mc-fabric", State: f.state}}
}

// downFixture verdrahtet den Wächter mit kurzen Intervallen.
func downFixture(t *testing.T, online *bool, expected func() bool) (*Down, *fakeContainers, <-chan events.Event) {
	t.Helper()
	bus := events.New()
	ch, cancel := bus.Subscribe(32)
	t.Cleanup(cancel)
	cts := &fakeContainers{state: "running"}
	d := NewDown(cts, "mc-fabric", expected, nil, bus).
		WithMCOnline(func() bool { return *online })
	d.Interval = 5 * time.Millisecond
	d.Grace = 2
	d.StartGrace = 4
	return d, cts, ch
}

// Kernfall des gescheiterten 26.2-Updates: der Container flackert zwischen
// running und exited, Minecraft antwortet nie. Es darf KEINE Entwarnung geben.
func TestDownNoFalseRecoveryDuringCrashLoop(t *testing.T) {
	online := false
	d, cts, ch := downFixture(t, &online, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go d.Run(ctx)

	// Absturzschleife nachstellen: exited -> running -> exited -> running …
	for i := 0; i < 6; i++ {
		cts.state = "exited"
		time.Sleep(20 * time.Millisecond)
		cts.state = "running" // JVM startet, stirbt gleich wieder
		time.Sleep(20 * time.Millisecond)
	}
	cancel()

	evs := drain(ch)
	var down, up int
	for _, e := range evs {
		switch e.Type {
		case events.TypeServerDown:
			down++
		case events.TypeServerUp:
			up++
		}
	}
	if up != 0 {
		t.Errorf("Entwarnung trotz Absturzschleife gemeldet (%d×) — Minecraft war nie erreichbar", up)
	}
	if down == 0 {
		t.Error("gar kein Alarm — der Ausfall muss gemeldet werden")
	}
	if down > 1 {
		t.Errorf("Alarm %d× statt einmalig", down)
	}
}

// Echte Erholung: Container läuft UND Minecraft antwortet -> genau eine
// Entwarnung.
func TestDownRecoveryOnlyWhenMinecraftAnswers(t *testing.T) {
	online := false
	d, cts, ch := downFixture(t, &online, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go d.Run(ctx)

	cts.state = "exited"
	time.Sleep(40 * time.Millisecond) // Alarm
	cts.state = "running"
	time.Sleep(30 * time.Millisecond) // läuft, aber stumm -> keine Entwarnung
	online = true
	time.Sleep(30 * time.Millisecond) // jetzt erst Entwarnung
	cancel()

	evs := drain(ch)
	var up int
	for _, e := range evs {
		if e.Type == events.TypeServerUp {
			up++
		}
	}
	if up != 1 {
		t.Errorf("Entwarnungen = %d, want genau 1", up)
	}
}

// Während eines geplanten Updates schweigt der Wächter komplett.
func TestDownSilentWhileUpgradeActive(t *testing.T) {
	online := false
	upgrading := true
	d, cts, ch := downFixture(t, &online, func() bool { return upgrading })

	ctx, cancel := context.WithCancel(context.Background())
	go d.Run(ctx)

	cts.state = "exited"
	time.Sleep(60 * time.Millisecond)
	cancel()

	for _, e := range drain(ch) {
		if e.Type == events.TypeServerDown || e.Type == events.TypeServerUp {
			t.Errorf("Meldung %q während eines laufenden Updates", e.Title)
		}
	}
}

// Normaler Start darf keinen Fehlalarm auslösen: Container läuft, Minecraft
// braucht kurz — innerhalb von StartGrace bleibt es still.
func TestDownStartGraceCoversSlowBoot(t *testing.T) {
	online := false
	d, cts, ch := downFixture(t, &online, nil)
	cts.state = "running"

	ctx, cancel := context.WithCancel(context.Background())
	go d.Run(ctx)
	time.Sleep(15 * time.Millisecond) // < StartGrace(4) * 5ms … knapp darunter
	online = true
	time.Sleep(20 * time.Millisecond)
	cancel()

	for _, e := range drain(ch) {
		if e.Type == events.TypeServerDown {
			t.Error("Fehlalarm während des normalen Hochfahrens")
		}
	}
}
