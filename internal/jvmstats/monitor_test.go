package jvmstats

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/collector"
)

type fakeSink struct {
	mu      sync.Mutex
	events  []collector.Event
	samples []collector.Sample
}

func (s *fakeSink) Publish(ev collector.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
}

func (s *fakeSink) Enqueue(smp ...collector.Sample) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.samples = append(s.samples, smp...)
}

// last liefert den jüngsten Wert einer Serie.
func (s *fakeSink) last(series string) (float64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.samples) - 1; i >= 0; i-- {
		if s.samples[i].Series == series {
			return s.samples[i].Value, true
		}
	}
	return 0, false
}

type fakeLagStore struct{ saved []LagEvent }

func (f *fakeLagStore) SaveLag(_ context.Context, ev *LagEvent) error {
	if ev.ID == 0 {
		ev.ID = int64(len(f.saved) + 1)
	}
	f.saved = append(f.saved, ev.clone())
	return nil
}

func (f *fakeLagStore) ListLags(context.Context, time.Time, int) ([]LagEvent, error) {
	return f.saved, nil
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestMonitorTick(t *testing.T) {
	pf := newProcFixture(t)
	pf.thread(510, 511, "Server thread", 1000, 0)
	pf.thread(510, 512, "C2ME worker #0", 100, 0)
	gcPath := filepath.Join(t.TempDir(), "gc.log")
	appendTo(t, gcPath, lineYoung) // 5000M->1200M(12288M)
	t0 := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	mc := collector.MCStatus{Time: t0, Online: true, TPS: 20, MSPT: 8, PlayersOnline: 3}
	sink, store := &fakeSink{}, &fakeLagStore{}
	m := NewMonitor(Config{
		ProcRoot:  pf.root,
		GCLogPath: gcPath,
		InitPID:   pid500,
		MC:        func() collector.MCStatus { return mc },
	}, sink, store, quiet())
	ctx := context.Background()

	m.Tick(ctx, t0)
	st := m.Current()
	if !st.Threads.OK || st.ServerThreadCPU != nil {
		t.Errorf("Takt 1: Threads=%+v ServerThreadCPU=%v, want ok ohne Wert", st.Threads, st.ServerThreadCPU)
	}
	if !st.GCLog.OK || st.HeapAfterGC == nil || *st.HeapAfterGC != 1200*mib || *st.HeapTotal != 12288*mib {
		t.Errorf("Takt 1: Heap aus dem Vorlauf fehlt: %+v", st)
	}
	if st.HostCores != 2 || st.MSPT == nil || *st.MSPT != 8 || st.OpenLag != nil {
		t.Errorf("Takt 1: %+v", st)
	}

	// Takt 2: Server-Thread 100 %, Full-GC 812 ms → Lag (gc)
	pf.thread(510, 511, "Server thread", 1500, 0)
	pf.thread(510, 512, "C2ME worker #0", 150, 0)
	appendTo(t, gcPath, lineFull)
	m.Tick(ctx, t0.Add(5*time.Second))
	st = m.Current()
	if st.ServerThreadCPU == nil || !approx(*st.ServerThreadCPU, 100) || !approx(*st.JavaCPU, 110) {
		t.Errorf("Takt 2: CPU = %v / %v", st.ServerThreadCPU, st.JavaCPU)
	}
	if len(st.Groups) != 2 || st.Groups[0].Name != "Server thread" {
		t.Errorf("Takt 2: Groups = %+v", st.Groups)
	}
	if *st.HeapAfterGC != 6000*mib || *st.LastGCPauseMs != 812.004 || *st.GCPauseMax != 812.004 {
		t.Errorf("Takt 2: Heap/GC = %+v", st)
	}
	if st.OpenLag == nil || st.OpenLag.Trigger != "gc" || st.OpenLag.ID != 1 || len(store.saved) != 1 {
		t.Fatalf("Takt 2: OpenLag = %+v, saved = %d", st.OpenLag, len(store.saved))
	}
	if v, ok := sink.last(SeriesGCPause); !ok || v != 812.004 {
		t.Errorf("%s = %v", SeriesGCPause, v)
	}
	if v, ok := sink.last(SeriesServerCPU); !ok || !approx(v, 100) {
		t.Errorf("%s = %v", SeriesServerCPU, v)
	}
	if v, ok := sink.last(SeriesHeapAfter); !ok || v != 6000*mib {
		t.Errorf("%s = %v", SeriesHeapAfter, v)
	}
	if len(sink.events) != 2 || sink.events[1].Type != "perf" {
		t.Errorf("events = %+v", sink.events)
	}
	lags, err := m.Lags(ctx, t0.Add(-time.Hour), 500)
	if err != nil || len(lags) != 1 {
		t.Errorf("Lags = %v, %v", lags, err)
	}
}

func TestMonitorUnavailable(t *testing.T) {
	sink := &fakeSink{}
	m := NewMonitor(Config{
		ProcRoot:  filepath.Join(t.TempDir(), "kein-proc"),
		GCLogPath: filepath.Join(t.TempDir(), "gc.log"),
		InitPID:   pid500,
	}, sink, nil, quiet())
	m.Tick(context.Background(), time.Now())
	st := m.Current()
	if st.Threads.OK || st.Threads.Reason != "/host/proc nicht lesbar" {
		t.Errorf("Threads = %+v", st.Threads)
	}
	if st.GCLog.OK || st.GCLog.Reason != "GC-Log nicht eingeschaltet" {
		t.Errorf("GCLog = %+v", st.GCLog)
	}
	if st.ServerThreadCPU != nil || st.HeapAfterGC != nil || st.Groups == nil {
		t.Errorf("Status = %+v, want leere Werte und Groups []", st)
	}
	if len(sink.samples) != 0 || len(sink.events) != 1 {
		t.Errorf("samples = %v, events = %d", sink.samples, len(sink.events))
	}
	if lags, err := m.Lags(context.Background(), time.Now(), 500); err != nil || lags == nil || len(lags) != 0 {
		t.Errorf("Lags ohne Store = %v, %v; want []", lags, err)
	}
}

func TestMonitorServerOff(t *testing.T) {
	pf := newProcFixture(t)
	pf.write("510/stat", statLine(510, "sh", 500, 0, 0, 12345))
	gcPath := filepath.Join(t.TempDir(), "gc.log")
	if err := os.WriteFile(gcPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewMonitor(Config{ProcRoot: pf.root, GCLogPath: gcPath, InitPID: pid500}, &fakeSink{}, nil, quiet())
	m.Tick(context.Background(), time.Now())
	st := m.Current()
	if st.Threads.OK || st.Threads.Reason != "Java-Prozess nicht gefunden (Server aus oder startet neu)" {
		t.Errorf("Threads = %+v", st.Threads)
	}
	if !st.GCLog.OK {
		t.Errorf("GCLog = %+v, want ok (leere Datei)", st.GCLog)
	}
}

// Server stoppt mitten im Lag: der offene Lag wird beendet und gespeichert.
func TestMonitorFlushOnServerStop(t *testing.T) {
	pf := newProcFixture(t)
	pf.thread(510, 511, "Server thread", 1000, 0)
	gcPath := filepath.Join(t.TempDir(), "gc.log")
	appendTo(t, gcPath, lineYoung)
	t0 := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	store := &fakeLagStore{}
	m := NewMonitor(Config{ProcRoot: pf.root, GCLogPath: gcPath, InitPID: pid500}, &fakeSink{}, store, quiet())
	ctx := context.Background()
	m.Tick(ctx, t0)
	appendTo(t, gcPath, lineFull)
	m.Tick(ctx, t0.Add(5*time.Second))
	if m.Current().OpenLag == nil || len(store.saved) != 1 {
		t.Fatalf("Lag nicht offen: %+v, saved = %d", m.Current().OpenLag, len(store.saved))
	}
	pf.write("510/stat", statLine(510, "sh", 500, 0, 0, 12345))
	m.Tick(ctx, t0.Add(10*time.Second))
	if m.Current().OpenLag != nil || len(store.saved) != 2 {
		t.Errorf("nach Stopp: OpenLag = %+v, saved = %d, want nil und 2", m.Current().OpenLag, len(store.saved))
	}
}
