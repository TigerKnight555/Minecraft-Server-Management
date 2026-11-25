package jvmstats

import (
	"slices"
	"strings"
	"time"
)

// Grenzwerte der Lag-Erkennung (Spec „Leistung").
const (
	MSPTLimit      = 50.0             // ms pro Tick
	GCPauseLimit   = 100.0            // ms
	CPULimit       = 95.0             // % eines Kerns, zwei Messungen in Folge
	CPUWaitLimit   = 80.0             // darunter „wartet auf etwas"
	HeapFullRatio  = 0.85             // Heap nach GC
	LagQuiet       = 30 * time.Second // so lange ohne Auslöser → Lag vorbei
	topThreadCount = 5
)

// LagEvent ist ein Eintrag im Lag-Protokoll.
type LagEvent struct {
	ID           int64         `json:"id"`
	Start        time.Time     `json:"start"`
	End          time.Time     `json:"end"`      // letzter Takt mit Auslöser
	Trigger      string        `json:"trigger"`  // mspt | gc | cpu (erster Auslöser)
	Triggers     []string      `json:"triggers"` // alle, Reihenfolge des ersten Auftretens
	WorstMSPT    float64       `json:"worstMspt"`
	WorstTPS     float64       `json:"worstTps"` // 0 = keine TPS-Messung im Lag
	MaxGCPauseMs float64       `json:"maxGcPauseMs"`
	Players      int           `json:"players"`
	HeapAfterGC  uint64        `json:"heapAfterGc"` // im Moment der höchsten Belegung
	HeapTotal    uint64        `json:"heapTotal"`
	MaxServerCPU float64       `json:"maxServerCpu"`
	TopThreads   []ThreadGroup `json:"topThreads"` // Top 5 im Takt mit der höchsten Server-Thread-CPU
	Diagnosis    string        `json:"diagnosis"`
}

// clone kopiert inklusive Slices (der Detector ändert den laufenden Lag weiter).
func (e LagEvent) clone() LagEvent {
	e.Triggers = slices.Clone(e.Triggers)
	e.TopThreads = slices.Clone(e.TopThreads)
	return e
}

// Tick ist ein Messtakt (5 s) mit allem, was die Lag-Erkennung braucht.
type Tick struct {
	Time        time.Time
	ServerCPU   float64 // % eines Kerns
	HasCPU      bool    // Thread-Messung in diesem Takt vorhanden
	MSPT, TPS   float64
	HasMSPT     bool    // NEUER MC-Messwert in diesem Takt
	Players     int
	GCPauseMax  float64 // längste GC-Pause in diesem Takt, ms
	HeapAfterGC uint64  // 0 = unbekannt
	HeapTotal   uint64
	Groups      []ThreadGroup // absteigend nach CPU
}

// Findings fasst zusammen, was die Diagnose-Regeln brauchen.
type Findings struct {
	MaxServerCPU   float64
	MaxGCPauseMs   float64
	HeapRatio      float64 // Heap nach GC / gesamt im schlimmsten Moment
	WorstMSPT      float64
	CPUAtWorstMSPT float64 // Server-Thread-CPU im Takt der schlechtesten MSPT
	HasCPUAtWorst  bool
}

// Diagnose wendet die Regeln in fester Reihenfolge an; alle zutreffenden
// Texte, mit „; " verbunden. Leer = keine eindeutige Ursache.
func Diagnose(f Findings) string {
	var parts []string
	if f.MaxServerCPU >= CPULimit {
		parts = append(parts, "Spiellogik überlastet (Server-Thread am Anschlag)")
	}
	if f.MaxGCPauseMs > GCPauseLimit {
		s := "Speicherbereinigung hat den Server angehalten"
		if f.HeapRatio > HeapFullRatio {
			s += " — Heap fast voll"
		}
		parts = append(parts, s)
	}
	if f.WorstMSPT > MSPTLimit && f.HasCPUAtWorst && f.CPUAtWorstMSPT < CPUWaitLimit {
		parts = append(parts, "Server wartet auf etwas (z. B. Festplatte, Chunk-Laden)")
	}
	return strings.Join(parts, "; ")
}

// Detector fasst Auslöser zu Lags zusammen. Nicht nebenläufig benutzen.
type Detector struct {
	open          *LagEvent
	lastTrigger   time.Time
	cpuStreak     int
	cpuStart      time.Time
	cpuAtWorst    float64
	hasCPUAtWorst bool
}

func NewDetector() *Detector { return &Detector{} }

// Observe verarbeitet einen Takt. Liefert den laufenden oder gerade
// beendeten Lag (nil = kein Lag) und ob er mit diesem Takt beendet wurde.
// Der Zeiger bleibt über alle Takte eines Lags derselbe — eine beim
// Speichern vergebene ID bleibt so erhalten.
func (d *Detector) Observe(t Tick) (*LagEvent, bool) {
	if t.HasCPU && t.ServerCPU >= CPULimit {
		if d.cpuStreak == 0 {
			d.cpuStart = t.Time
		}
		d.cpuStreak++
	} else {
		d.cpuStreak = 0
	}
	var trig []string
	if t.HasMSPT && t.MSPT > MSPTLimit {
		trig = append(trig, "mspt")
	}
	if t.GCPauseMax > GCPauseLimit {
		trig = append(trig, "gc")
	}
	if d.cpuStreak >= 2 {
		trig = append(trig, "cpu")
	}

	if len(trig) == 0 {
		if d.open != nil && t.Time.Sub(d.lastTrigger) >= LagQuiet {
			ev := d.open
			d.open = nil
			return ev, true
		}
		return d.open, false
	}
	if d.open == nil {
		start := t.Time
		if slices.Contains(trig, "cpu") && d.cpuStart.Before(start) {
			start = d.cpuStart
		}
		d.open = &LagEvent{Start: start, Trigger: trig[0]}
		d.cpuAtWorst, d.hasCPUAtWorst = 0, false
	}
	e := d.open
	d.lastTrigger = t.Time
	e.End = t.Time
	for _, tr := range trig {
		if !slices.Contains(e.Triggers, tr) {
			e.Triggers = append(e.Triggers, tr)
		}
	}
	d.update(t)
	e.Diagnosis = Diagnose(Findings{
		MaxServerCPU:   e.MaxServerCPU,
		MaxGCPauseMs:   e.MaxGCPauseMs,
		HeapRatio:      heapRatio(e.HeapAfterGC, e.HeapTotal),
		WorstMSPT:      e.WorstMSPT,
		CPUAtWorstMSPT: d.cpuAtWorst,
		HasCPUAtWorst:  d.hasCPUAtWorst,
	})
	return e, false
}

func (d *Detector) update(t Tick) {
	e := d.open
	if t.HasMSPT {
		if t.MSPT > e.WorstMSPT {
			e.WorstMSPT = t.MSPT
			d.cpuAtWorst, d.hasCPUAtWorst = t.ServerCPU, t.HasCPU
		}
		if t.TPS > 0 && (e.WorstTPS == 0 || t.TPS < e.WorstTPS) {
			e.WorstTPS = t.TPS
		}
	}
	e.MaxGCPauseMs = max(e.MaxGCPauseMs, t.GCPauseMax)
	e.Players = max(e.Players, t.Players)
	if t.HeapTotal > 0 && (e.HeapTotal == 0 ||
		heapRatio(t.HeapAfterGC, t.HeapTotal) > heapRatio(e.HeapAfterGC, e.HeapTotal)) {
		e.HeapAfterGC, e.HeapTotal = t.HeapAfterGC, t.HeapTotal
	}
	if t.HasCPU && (e.TopThreads == nil || t.ServerCPU > e.MaxServerCPU) {
		e.MaxServerCPU = t.ServerCPU
		e.TopThreads = slices.Clone(t.Groups[:min(topThreadCount, len(t.Groups))])
	}
}

func heapRatio(after, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(after) / float64(total)
}
