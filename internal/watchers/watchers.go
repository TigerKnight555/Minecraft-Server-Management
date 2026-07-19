// Package watchers implements the Phase-4.7 guards: crash reports, an
// unexpected-down detector, internet quality with hysteresis and resource
// thresholds. Alle melden über den Event-Bus; keiner greift selbst ein
// (eingreifen tun Routinen/Reconciler — Wächter machen nur sichtbar).
package watchers

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/collector"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
)

// ---- Crash-Wächter --------------------------------------------------------

// Crash watches <dataDir>/crash-reports for new files and posts a snippet.
type Crash struct {
	dataDir  string
	bus      *events.Bus
	log      *slog.Logger
	Interval time.Duration
}

func NewCrash(dataDir string, bus *events.Bus, log *slog.Logger) *Crash {
	return &Crash{dataDir: dataDir, bus: bus, log: log, Interval: 30 * time.Second}
}

func (c *Crash) Run(ctx context.Context) {
	// Bestand beim Start merken — nur NEUE Reports melden
	seen := map[string]bool{}
	for _, f := range c.list() {
		seen[f] = true
	}
	t := time.NewTicker(c.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		for _, f := range c.list() {
			if seen[f] {
				continue
			}
			seen[f] = true
			c.bus.Publish(events.Event{
				Type: events.TypeCrash, Severity: events.SevError,
				Title:   "💥 Der Server ist abgestürzt",
				Message: "Er sollte gleich automatisch neu starten. Falls nicht: der Admin wurde informiert.",
				Fields:  []events.Field{{Name: "Report", Value: f}, {Name: "Details", Value: c.snippet(f)}},
			})
		}
	}
}

func (c *Crash) list() []string {
	entries, err := os.ReadDir(filepath.Join(c.dataDir, "crash-reports"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".txt") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// snippet returns the informative head of a crash report (description +
// stack trace start), truncated for the Discord embed.
func (c *Crash) snippet(name string) string {
	data, err := os.ReadFile(filepath.Join(c.dataDir, "crash-reports", name))
	if err != nil {
		return "(Report nicht lesbar: " + err.Error() + ")"
	}
	lines := strings.Split(string(data), "\n")
	var keep []string
	for _, l := range lines {
		l = strings.TrimRight(l, "\r")
		if strings.HasPrefix(l, "---- Minecraft Crash Report") || l == "" {
			continue
		}
		keep = append(keep, l)
		if len(keep) >= 14 {
			break
		}
	}
	s := strings.Join(keep, "\n")
	if len(s) > 900 {
		s = s[:900] + "…"
	}
	return s
}

// ---- Unerwartet-Down-Wächter ----------------------------------------------

type resolver interface {
	Containers() []collector.Container
}

// Down alerts when the MC container is exited although nobody intended that:
// keine laufende Routine (expectedDown) und Soll-Zustand nicht "stopped".
type Down struct {
	containers   resolver
	mcName       string
	expectedDown func() bool // scheduler: gerade absichtlich gestoppt?
	desiredStop  func() bool // soll-zustand: bewusst aus?
	bus          *events.Bus
	Interval     time.Duration
	Grace        int // aufeinanderfolgende Down-Messungen bis Alarm

	downAlerted bool
}

func NewDown(containers resolver, mcName string, expectedDown, desiredStop func() bool, bus *events.Bus) *Down {
	return &Down{
		containers: containers, mcName: mcName,
		expectedDown: expectedDown, desiredStop: desiredStop, bus: bus,
		Interval: 30 * time.Second, Grace: 2,
	}
}

func (d *Down) Run(ctx context.Context) {
	t := time.NewTicker(d.Interval)
	defer t.Stop()
	misses := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		running := false
		known := false
		for _, c := range d.containers.Containers() {
			if c.Name == d.mcName {
				known = true
				running = c.State == "running"
			}
		}
		if !known || (d.expectedDown != nil && d.expectedDown()) || (d.desiredStop != nil && d.desiredStop()) {
			misses = 0
			continue
		}
		if running {
			misses = 0
			if d.downAlerted {
				d.downAlerted = false
				d.bus.Publish(events.Event{
					Type: events.TypeServerUp, Severity: events.SevSuccess,
					Title:   "✅ Server läuft wieder",
					Message: "Alles wieder normal — viel Spaß!",
				})
			}
			continue
		}
		misses++
		if misses >= d.Grace && !d.downAlerted {
			d.downAlerted = true
			d.bus.Publish(events.Event{
				Type: events.TypeServerDown, Severity: events.SevError,
				Title:   "❌ Server ist unerwartet offline",
				Message: "Er sollte gleich automatisch neu starten — Meldung folgt. Falls nicht, ist der Admin dran.",
			})
		}
	}
}

// ---- Internet-Wächter mit Hysterese ----------------------------------------

// Net debounces WAN quality: Alarm erst nach anhaltender Störung, Entwarnung
// erst nach stabiler Erholung (Konzept: Benachrichtigungen & Integrationen).
type Net struct {
	sample func() collector.WANSample
	bus    *events.Bus

	Interval   time.Duration
	Sustain    int     // aufeinanderfolgende Messungen für Kippen
	MaxRTTMs   float64 // Median-RTT-Schwelle
	MaxLossPct float64

	degraded   bool
	badStreak  int
	goodStreak int
	since      time.Time
}

func NewNet(sample func() collector.WANSample, bus *events.Bus) *Net {
	return &Net{
		sample: sample, bus: bus,
		Interval: 30 * time.Second, Sustain: 6, // ~3 min
		MaxRTTMs: 80, MaxLossPct: 2,
	}
}

// bad classifies one WAN sample: Median über alle erreichten Ziele.
func (n *Net) bad(s collector.WANSample) bool {
	var rtts []float64
	var loss float64
	reached := 0
	for _, t := range s.Targets {
		if t.Reached {
			reached++
			rtts = append(rtts, t.RTTMs)
		}
		loss += t.LossPct
	}
	if len(s.Targets) == 0 {
		return false // keine Daten = kein Urteil
	}
	if reached == 0 {
		return true // komplett offline
	}
	loss /= float64(len(s.Targets))
	sort.Float64s(rtts)
	median := rtts[len(rtts)/2]
	return median > n.MaxRTTMs || loss > n.MaxLossPct
}

func (n *Net) Run(ctx context.Context) {
	t := time.NewTicker(n.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		n.step(n.sample())
	}
}

// step advances the hysteresis state machine (exported logic for tests).
func (n *Net) step(s collector.WANSample) {
	if n.bad(s) {
		n.badStreak++
		n.goodStreak = 0
	} else {
		n.goodStreak++
		n.badStreak = 0
	}
	switch {
	case !n.degraded && n.badStreak >= n.Sustain:
		n.degraded = true
		n.since = time.Now()
		n.bus.Publish(events.Event{
			Type: events.TypeNetDegraded, Severity: events.SevWarn,
			Title:   "🌐 Internet am Server gerade instabil",
			Message: "Es kann gerade zu Lags oder Verbindungsabbrüchen kommen — liegt nicht an euch.",
		})
	case n.degraded && n.goodStreak >= n.Sustain:
		n.degraded = false
		n.bus.Publish(events.Event{
			Type: events.TypeNetOK, Severity: events.SevSuccess,
			Title:   "🌐 Internet wieder stabil",
			Message: fmt.Sprintf("Die Störung dauerte %s.", time.Since(n.since).Round(time.Minute)),
		})
	}
}

// ---- Ressourcen-Schwellwerte ------------------------------------------------

// Resource warns once when disk or RAM cross their thresholds and arms
// itself again after recovery (einfache Hysterese über Re-Arm-Marge).
type Resource struct {
	host func() collector.HostSample
	bus  *events.Bus

	Interval    time.Duration
	DiskMaxPct  float64
	MemMaxPct   float64
	rearmMargin float64

	diskAlerted, memAlerted bool
}

func NewResource(host func() collector.HostSample, bus *events.Bus) *Resource {
	return &Resource{
		host: host, bus: bus,
		Interval: time.Minute, DiskMaxPct: 90, MemMaxPct: 95, rearmMargin: 5,
	}
}

func (r *Resource) Run(ctx context.Context) {
	t := time.NewTicker(r.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		r.step(r.host())
	}
}

func (r *Resource) step(h collector.HostSample) {
	if h.DiskTotal > 0 {
		pct := float64(h.DiskUsed) / float64(h.DiskTotal) * 100
		switch {
		case !r.diskAlerted && pct >= r.DiskMaxPct:
			r.diskAlerted = true
			r.bus.Publish(events.Event{
				Type: events.TypeResource, Severity: events.SevWarn,
				Title:   fmt.Sprintf("⚠️ Festplatte des Servers zu %.0f %% voll", pct),
				Message: "Info für den Admin: bitte Platz schaffen, sonst sind Welt-Speicherung und Backups in Gefahr.",
			})
		case r.diskAlerted && pct < r.DiskMaxPct-r.rearmMargin:
			r.diskAlerted = false
		}
	}
	if h.MemTotal > 0 {
		pct := float64(h.MemUsed) / float64(h.MemTotal) * 100
		switch {
		case !r.memAlerted && pct >= r.MemMaxPct:
			r.memAlerted = true
			r.bus.Publish(events.Event{
				Type: events.TypeResource, Severity: events.SevWarn,
				Title:   fmt.Sprintf("⚠️ Arbeitsspeicher zu %.0f %% belegt", pct),
				Message: "Info für den Admin: anhaltend voller Speicher kann den Server abstürzen lassen.",
			})
		case r.memAlerted && pct < r.MemMaxPct-r.rearmMargin:
			r.memAlerted = false
		}
	}
}
