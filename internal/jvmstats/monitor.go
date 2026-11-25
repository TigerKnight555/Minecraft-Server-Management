package jvmstats

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/collector"
)

// TopGroups: so viele Thread-Gruppen zeigt die Live-Tabelle.
const TopGroups = 15

// Zeitreihen des Leistung-Tabs (bestehende Sample-Tabellen).
const (
	SeriesServerCPU = "jvm.thread.server.cpu" // % eines Kerns
	SeriesJavaCPU   = "jvm.cpu.total"         // % eines Kerns
	SeriesHeapAfter = "jvm.heap.after_gc"     // Bytes
	SeriesHeapTotal = "jvm.heap.total"        // Bytes
	SeriesGCPause   = "jvm.gc.pause_max"      // ms, längste Pause im Takt
)

// Availability sagt, ob eine Datenquelle liefert, und sonst warum nicht.
type Availability struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
}

// Status ist der aktuelle Stand für GET /api/perf und das SSE-Ereignis
// "perf". nil = unbekannt (Kachel zeigt „—").
type Status struct {
	Time            time.Time     `json:"time"`
	Threads         Availability  `json:"threads"`
	GCLog           Availability  `json:"gcLog"`
	ServerThreadCPU *float64      `json:"serverThreadCpu"`
	JavaCPU         *float64      `json:"javaCpu"`
	HostCores       int           `json:"hostCores"`
	MSPT            *float64      `json:"mspt"`
	HeapAfterGC     *uint64       `json:"heapAfterGc"`
	HeapTotal       *uint64       `json:"heapTotal"`
	LastGCPauseMs   *float64      `json:"lastGcPauseMs"`
	GCPauseMax      *float64      `json:"gcPauseMax"` // längste Pause in diesem Takt
	Groups          []ThreadGroup `json:"groups"`     // Top 15
	OpenLag         *LagEvent     `json:"openLag"`    // laufender Lag, nil = keiner
}

// Sink nimmt Messpunkte und SSE-Ereignisse an (collector.Collector).
type Sink interface {
	Publish(collector.Event)
	Enqueue(...collector.Sample)
}

// LagStore speichert das Lag-Protokoll (storage.SQLite).
type LagStore interface {
	SaveLag(ctx context.Context, ev *LagEvent) error
	ListLags(ctx context.Context, since time.Time, limit int) ([]LagEvent, error)
}

type Config struct {
	ProcRoot  string                             // Host-procfs, z. B. /host/proc
	GCLogPath string                             // z. B. /mc/data/logs/gc.log
	Interval  time.Duration                      // 0 → 5 s (fest, auch ohne Zuschauer)
	InitPID   func(context.Context) (int, error) // Host-PID des MC-Container-Init
	MC        func() collector.MCStatus          // letzter MC-Status (MSPT, Spieler)
}

// Monitor misst Threads und GC im festen Takt, erkennt Lags und verteilt
// den Stand.
type Monitor struct {
	cfg     Config
	threads *ThreadSampler
	gc      *GCTailer
	det     *Detector
	sink    Sink
	store   LagStore // nil = Lags werden nicht gespeichert (Mock-Modus)
	log     *slog.Logger
	lastMC  time.Time

	mu     sync.RWMutex
	status Status
}

func NewMonitor(cfg Config, sink Sink, store LagStore, log *slog.Logger) *Monitor {
	if cfg.Interval <= 0 {
		cfg.Interval = 5 * time.Second
	}
	return &Monitor{
		cfg:     cfg,
		threads: NewThreadSampler(cfg.ProcRoot, cfg.InitPID),
		gc:      NewGCTailer(cfg.GCLogPath),
		det:     NewDetector(),
		sink:    sink,
		store:   store,
		log:     log,
		status:  Status{Groups: []ThreadGroup{}},
	}
}

// Run misst sofort und dann im festen Takt, bis ctx endet. Der laufende Lag
// ist zu diesem Zeitpunkt schon mit seinem bisherigen Ende gespeichert.
func (m *Monitor) Run(ctx context.Context) {
	m.Tick(ctx, time.Now())
	t := time.NewTicker(m.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			m.flush()
			return
		case now := <-t.C:
			m.Tick(ctx, now)
		}
	}
}

// Tick ist ein Messtakt (für Tests exportiert).
func (m *Monitor) Tick(ctx context.Context, now time.Time) {
	st := Status{Time: now, Groups: []ThreadGroup{}}
	tick := Tick{Time: now}
	var samples []collector.Sample
	add := func(series string, v float64) {
		samples = append(samples, collector.Sample{Series: series, Time: now, Value: v})
	}

	if cores, err := HostCores(m.cfg.ProcRoot); err == nil {
		st.HostCores = cores
	}

	ts, ok, terr := m.threads.Sample(ctx, now)
	if terr != nil {
		st.Threads = Availability{Reason: threadReason(terr)}
	} else {
		st.Threads = Availability{OK: true}
		if ok {
			java := ts.TotalCPU
			st.JavaCPU = &java
			add(SeriesJavaCPU, java)
			if ts.ServerFound {
				srv := ts.ServerCPU
				st.ServerThreadCPU = &srv
				tick.ServerCPU, tick.HasCPU = srv, true
				add(SeriesServerCPU, srv)
			}
			tick.Groups = ts.Groups
			st.Groups = slices.Clone(ts.Groups[:min(TopGroups, len(ts.Groups))])
		}
	}

	pauses, err := m.gc.Poll()
	if err != nil {
		st.GCLog = Availability{Reason: gcReason(err)}
	} else {
		st.GCLog = Availability{OK: true}
		for _, p := range pauses {
			tick.GCPauseMax = max(tick.GCPauseMax, p.DurationMs)
		}
		gmax := tick.GCPauseMax
		st.GCPauseMax = &gmax
		add(SeriesGCPause, gmax)
		if last, ok := m.gc.Last(); ok {
			after, total, dur := last.After, last.Total, last.DurationMs
			st.HeapAfterGC, st.HeapTotal, st.LastGCPauseMs = &after, &total, &dur
			tick.HeapAfterGC, tick.HeapTotal = after, total
			add(SeriesHeapAfter, float64(after))
			add(SeriesHeapTotal, float64(total))
		}
	}

	if m.cfg.MC != nil {
		mc := m.cfg.MC()
		if mc.Online && mc.MSPT > 0 {
			v := mc.MSPT
			st.MSPT = &v
			// nur neue Messwerte zählen (mc-Takt 10 s, dieser Takt 5 s)
			if mc.Time.After(m.lastMC) {
				tick.MSPT, tick.TPS, tick.HasMSPT = mc.MSPT, mc.TPS, true
				m.lastMC = mc.Time
			}
		}
		tick.Players = mc.PlayersOnline
	}

	if errors.Is(terr, ErrNoJava) {
		// Server gestoppt: offenen Lag beenden und speichern.
		if ev, ok := m.det.Flush(); ok {
			m.saveLag(ctx, ev)
		}
	} else {
		ev, _ := m.det.Observe(tick)
		if ev != nil {
			m.saveLag(ctx, ev)
		}
		// Nach einer Pause kann der neue offene Lag nur über Open() erreichbar sein.
		if open := m.det.Open(); open != nil {
			if open != ev {
				m.saveLag(ctx, open)
			}
			c := open.clone()
			st.OpenLag = &c
		}
	}

	m.mu.Lock()
	m.status = st
	m.mu.Unlock()
	if len(samples) > 0 {
		m.sink.Enqueue(samples...)
	}
	m.sink.Publish(collector.Event{Type: "perf", Data: st})
}

// saveLag speichert einen Lag (ohne Store: nichts).
func (m *Monitor) saveLag(ctx context.Context, ev *LagEvent) {
	if m.store == nil {
		return
	}
	if err := m.store.SaveLag(ctx, ev); err != nil {
		m.log.Warn("save lag failed", "err", err)
	}
}

// flush beendet beim Herunterfahren einen offenen Lag und speichert ihn.
func (m *Monitor) flush() {
	if ev, ok := m.det.Flush(); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		m.saveLag(ctx, ev)
	}
}

// Current liefert den Stand des letzten Takts.
func (m *Monitor) Current() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	st := m.status
	st.Groups = slices.Clone(st.Groups)
	if st.OpenLag != nil {
		c := st.OpenLag.clone()
		st.OpenLag = &c
	}
	return st
}

// Lags liefert das Lag-Protokoll (ohne Store: leer).
func (m *Monitor) Lags(ctx context.Context, since time.Time, limit int) ([]LagEvent, error) {
	if m.store == nil {
		return []LagEvent{}, nil
	}
	return m.store.ListLags(ctx, since, limit)
}

func threadReason(err error) string {
	switch {
	case errors.Is(err, ErrProcUnavailable):
		return "/host/proc nicht lesbar"
	case errors.Is(err, ErrNoJava):
		return "Java-Prozess nicht gefunden (Server aus oder startet neu)"
	default:
		return err.Error()
	}
}

func gcReason(err error) string {
	if errors.Is(err, fs.ErrNotExist) {
		return "GC-Log nicht eingeschaltet"
	}
	return "GC-Log nicht lesbar: " + err.Error()
}
