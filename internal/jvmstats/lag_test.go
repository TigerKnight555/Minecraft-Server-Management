package jvmstats

import (
	"strings"
	"testing"
	"time"
)

var lagBase = time.Date(2026, 11, 25, 20, 0, 0, 0, time.UTC)

func at(sec int) Tick { return Tick{Time: lagBase.Add(time.Duration(sec) * time.Second)} }

func TestLagStartsOnMSPT(t *testing.T) {
	d := NewDetector()
	tk := at(0)
	tk.HasMSPT, tk.MSPT, tk.TPS = true, 60, 16
	ev, ended := d.Observe(tk)
	if ev == nil || ended || ev.Trigger != "mspt" || ev.WorstMSPT != 60 || ev.WorstTPS != 16 {
		t.Fatalf("ev = %+v ended=%v", ev, ended)
	}
}

func TestLagIgnoresStaleMSPT(t *testing.T) {
	d := NewDetector()
	tk := at(0)
	tk.MSPT = 60 // kein neuer Messwert in diesem Takt
	if ev, _ := d.Observe(tk); ev != nil {
		t.Errorf("veralteter MSPT-Wert hat Lag ausgelöst: %+v", ev)
	}
}

func TestLagStartsOnGC(t *testing.T) {
	d := NewDetector()
	tk := at(0)
	tk.GCPauseMax = 812
	ev, _ := d.Observe(tk)
	if ev == nil || ev.Trigger != "gc" || ev.MaxGCPauseMs != 812 {
		t.Fatalf("ev = %+v", ev)
	}
	d2 := NewDetector()
	tk.GCPauseMax = 100 // genau an der Grenze: kein Lag
	if ev, _ := d2.Observe(tk); ev != nil {
		t.Errorf("100 ms hat Lag ausgelöst")
	}
}

func TestLagCPUNeedsTwoInARow(t *testing.T) {
	d := NewDetector()
	cpu := func(sec int, v float64) Tick {
		tk := at(sec)
		tk.HasCPU, tk.ServerCPU = true, v
		return tk
	}
	if ev, _ := d.Observe(cpu(0, 97)); ev != nil {
		t.Fatal("eine Messung reicht nicht")
	}
	if ev, _ := d.Observe(cpu(5, 50)); ev != nil {
		t.Fatal("Serie unterbrochen")
	}
	if ev, _ := d.Observe(cpu(10, 96)); ev != nil {
		t.Fatal("eine Messung reicht nicht")
	}
	ev, _ := d.Observe(cpu(15, 99))
	if ev == nil || ev.Trigger != "cpu" {
		t.Fatalf("ev = %+v", ev)
	}
	if !ev.Start.Equal(lagBase.Add(10 * time.Second)) {
		t.Errorf("Start = %v, want erste der beiden Messungen", ev.Start)
	}
}

func TestLagMergesAndEndsAfterQuiet(t *testing.T) {
	d := NewDetector()
	t0 := at(0)
	t0.HasMSPT, t0.MSPT = true, 70
	first, _ := d.Observe(t0)
	t1 := at(10)
	t1.GCPauseMax = 300
	second, _ := d.Observe(t1)
	if second != first {
		t.Fatal("zweiter Auslöser hat neuen Lag angelegt statt zusammenzufassen")
	}
	if first.Trigger != "mspt" || strings.Join(first.Triggers, ",") != "mspt,gc" {
		t.Errorf("Trigger = %s, Triggers = %v", first.Trigger, first.Triggers)
	}
	for sec := 15; sec < 40; sec += 5 {
		ev, ended := d.Observe(at(sec))
		if ev != first || ended {
			t.Fatalf("t=%d: ev=%p ended=%v, want offen", sec, ev, ended)
		}
	}
	ev, ended := d.Observe(at(40)) // 30 s nach dem letzten Auslöser (t=10)
	if ev != first || !ended {
		t.Fatalf("t=40: ended=%v, want beendet", ended)
	}
	if !ev.End.Equal(lagBase.Add(10 * time.Second)) {
		t.Errorf("End = %v, want letzter Auslöse-Takt", ev.End)
	}
	if ev, _ := d.Observe(at(45)); ev != nil {
		t.Error("nach dem Ende noch ein Lag")
	}
	t2 := at(50)
	t2.GCPauseMax = 200
	next, _ := d.Observe(t2)
	if next == nil || next == first || next.ID != 0 {
		t.Error("neuer Lag muss ein neuer Eintrag sein")
	}
}

func groupsN(n int, server float64) []ThreadGroup {
	g := []ThreadGroup{{Name: "Server thread", Count: 1, CPU: server}}
	for i := 1; i < n; i++ {
		g = append(g, ThreadGroup{Name: "G" + string(rune('A'+i)), Count: 1, CPU: float64(10 - i)})
	}
	return g
}

func TestLagAggregates(t *testing.T) {
	d := NewDetector()
	t1 := at(0)
	t1.HasMSPT, t1.MSPT, t1.TPS, t1.Players = true, 60, 16, 3
	t1.HasCPU, t1.ServerCPU, t1.Groups = true, 70, groupsN(2, 70)
	t1.HeapAfterGC, t1.HeapTotal = 6<<30, 12<<30
	d.Observe(t1)
	t2 := at(10)
	t2.HasMSPT, t2.MSPT, t2.TPS, t2.Players = true, 90, 11, 5
	t2.HasCPU, t2.ServerCPU, t2.Groups = true, 60, groupsN(7, 60)
	t2.HeapAfterGC, t2.HeapTotal = 11<<30, 12<<30
	t2.GCPauseMax = 150
	ev, _ := d.Observe(t2)
	if ev.WorstMSPT != 90 || ev.WorstTPS != 11 || ev.Players != 5 || ev.MaxGCPauseMs != 150 {
		t.Errorf("ev = %+v", ev)
	}
	if ev.HeapAfterGC != 11<<30 || ev.HeapTotal != 12<<30 {
		t.Errorf("Heap = %d/%d, want höchste Belegung", ev.HeapAfterGC, ev.HeapTotal)
	}
	// Top-Threads aus dem Takt mit der höchsten Server-Thread-CPU (t1: 70 %)
	if ev.MaxServerCPU != 70 || len(ev.TopThreads) != 2 || ev.TopThreads[0].CPU != 70 {
		t.Errorf("TopThreads = %+v (max %v)", ev.TopThreads, ev.MaxServerCPU)
	}
	want := "Speicherbereinigung hat den Server angehalten — Heap fast voll; Server wartet auf etwas (z. B. Festplatte, Chunk-Laden)"
	if ev.Diagnosis != want {
		t.Errorf("Diagnosis = %q", ev.Diagnosis)
	}
	t3 := at(15)
	t3.HasCPU, t3.ServerCPU, t3.Groups = true, 75, groupsN(7, 75)
	t3.GCPauseMax = 120
	ev, _ = d.Observe(t3)
	if len(ev.TopThreads) != 5 || ev.TopThreads[0].CPU != 75 {
		t.Errorf("TopThreads = %+v, want Top 5 aus t3", ev.TopThreads)
	}
}

func TestDiagnose(t *testing.T) {
	const (
		cpu  = "Spiellogik überlastet (Server-Thread am Anschlag)"
		gc   = "Speicherbereinigung hat den Server angehalten"
		wait = "Server wartet auf etwas (z. B. Festplatte, Chunk-Laden)"
	)
	cases := []struct {
		f    Findings
		want string
	}{
		{Findings{MaxServerCPU: 99}, cpu},
		{Findings{MaxGCPauseMs: 812, HeapRatio: 0.5}, gc},
		{Findings{MaxGCPauseMs: 812, HeapRatio: 0.9}, gc + " — Heap fast voll"},
		{Findings{WorstMSPT: 70, CPUAtWorstMSPT: 30, HasCPUAtWorst: true}, wait},
		{Findings{WorstMSPT: 70, CPUAtWorstMSPT: 85, HasCPUAtWorst: true}, ""},
		{Findings{WorstMSPT: 70}, ""},
		{Findings{MaxGCPauseMs: 100}, ""},
		{Findings{MaxServerCPU: 97, MaxGCPauseMs: 150, HeapRatio: 0.2, WorstMSPT: 80, CPUAtWorstMSPT: 50, HasCPUAtWorst: true},
			cpu + "; " + gc + "; " + wait},
	}
	for i, c := range cases {
		if got := Diagnose(c.f); got != c.want {
			t.Errorf("%d: Diagnose(%+v) = %q, want %q", i, c.f, got, c.want)
		}
	}
}

func TestLagEventCloneIsDeep(t *testing.T) {
	e := LagEvent{Triggers: []string{"gc"}, TopThreads: []ThreadGroup{{Name: "a"}}}
	c := e.clone()
	c.Triggers[0] = "x"
	c.TopThreads[0].Name = "y"
	if e.Triggers[0] != "gc" || e.TopThreads[0].Name != "a" {
		t.Error("clone teilt Slices mit dem Original")
	}
}
