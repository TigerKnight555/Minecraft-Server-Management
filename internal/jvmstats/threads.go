package jvmstats

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ThreadGroup fasst gleichnamige Threads zusammen (ohne laufende Nummer).
type ThreadGroup struct {
	Name  string  `json:"name"`
	Count int     `json:"count"`
	CPU   float64 `json:"cpu"` // Summe, % eines Kerns
}

// trailingIndex: laufende Nummer am Namensende samt Trenner (" #3", "#0", "-12").
var trailingIndex = regexp.MustCompile(`[\s#_-]*\d+$`)

// GroupName entfernt die laufende Nummer: "C2ME worker #3" → "C2ME worker",
// "GC Thread#0" → "GC Thread", "Worker-Main-12" → "Worker-Main".
func GroupName(comm string) string {
	name := strings.TrimSpace(trailingIndex.ReplaceAllString(comm, ""))
	if name == "" {
		return comm
	}
	return name
}

// ThreadSample ist die Auslastung eines Takts.
type ThreadSample struct {
	ServerCPU   float64       // Thread "Server thread", % eines Kerns
	ServerFound bool          // Thread "Server thread" existiert
	TotalCPU    float64       // alle Java-Threads, % eines Kerns
	Groups      []ThreadGroup // absteigend nach CPU
}

// ThreadSampler misst die Java-Threads Takt für Takt. Die Java-PID wird nie
// fest gespeichert: jeder Takt prüft per starttime, ob sie noch derselbe
// Prozess ist, sonst wird über die Container-Init-PID neu gesucht.
type ThreadSampler struct {
	root    string
	initPID func(context.Context) (int, error)
	pid     int
	start   uint64
	prev    map[int]thread
	prevAt  time.Time
}

func NewThreadSampler(root string, initPID func(context.Context) (int, error)) *ThreadSampler {
	return &ThreadSampler{root: root, initPID: initPID}
}

// Sample misst einen Takt. ok=false: erster Takt nach (Neu-)Fund des
// Prozesses — es gibt noch keinen Vergleichsstand.
func (s *ThreadSampler) Sample(ctx context.Context, now time.Time) (ThreadSample, bool, error) {
	if !s.stillValid() {
		s.pid, s.prev = 0, nil
		if s.initPID == nil {
			return ThreadSample{}, false, ErrNoJava
		}
		init, err := s.initPID(ctx)
		if err != nil {
			return ThreadSample{}, false, fmt.Errorf("%w: %v", ErrNoJava, err)
		}
		pid, start, err := FindJava(s.root, init)
		if err != nil {
			return ThreadSample{}, false, err
		}
		s.pid, s.start = pid, start
	}
	cur, err := readThreads(s.root, s.pid)
	if err != nil {
		s.pid, s.prev = 0, nil
		return ThreadSample{}, false, ErrNoJava
	}
	prev, prevAt := s.prev, s.prevAt
	s.prev, s.prevAt = cur, now
	dt := now.Sub(prevAt).Seconds()
	if prev == nil || dt <= 0 {
		return ThreadSample{}, false, nil
	}
	return compute(prev, cur, dt), true, nil
}

func (s *ThreadSampler) stillValid() bool {
	if s.pid == 0 {
		return false
	}
	st, err := readStat(filepath.Join(s.root, strconv.Itoa(s.pid), "stat"))
	return err == nil && st.StartTime == s.start && st.Comm == "java"
}

// compute: Auslastung = Δ(utime+stime) / Δt / CLK_TCK in % eines Kerns.
// Threads ohne Vorgängerstand (gerade gestartet) zählen mit 0 %.
func compute(prev, cur map[int]thread, dt float64) ThreadSample {
	var out ThreadSample
	groups := map[string]*ThreadGroup{}
	for tid, t := range cur {
		cpu := 0.0
		if p, ok := prev[tid]; ok && t.ticks >= p.ticks {
			cpu = float64(t.ticks-p.ticks) / ClkTck / dt * 100
		}
		out.TotalCPU += cpu
		if t.comm == "Server thread" {
			out.ServerCPU += cpu
			out.ServerFound = true
		}
		name := GroupName(t.comm)
		g := groups[name]
		if g == nil {
			g = &ThreadGroup{Name: name}
			groups[name] = g
		}
		g.Count++
		g.CPU += cpu
	}
	out.Groups = make([]ThreadGroup, 0, len(groups))
	for _, g := range groups {
		out.Groups = append(out.Groups, *g)
	}
	sort.Slice(out.Groups, func(i, j int) bool {
		if out.Groups[i].CPU != out.Groups[j].CPU {
			return out.Groups[i].CPU > out.Groups[j].CPU
		}
		return out.Groups[i].Name < out.Groups[j].Name
	})
	return out
}
