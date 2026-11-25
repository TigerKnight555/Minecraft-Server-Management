package collector

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func pendingValue(c *Collector, series string) (float64, bool) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	for _, s := range c.pending {
		if s.Series == series {
			return s.Value, true
		}
	}
	return 0, false
}

func TestPollMCEnqueuesMSPT(t *testing.T) {
	c := New(Config{}, nil, fixedMC{s: MCStatus{Time: time.Now(), Online: true, TPS: 19, MSPT: 42.5}}, nil, nil, nil, quietLog())
	c.pollMC(context.Background())
	if v, ok := pendingValue(c, "mc.mspt"); !ok || v != 42.5 {
		t.Errorf("mc.mspt = %v (%v), want 42.5", v, ok)
	}
}

func TestPollMCSkipsMSPTWhenUnknown(t *testing.T) {
	c := New(Config{}, nil, fixedMC{s: MCStatus{Time: time.Now(), Online: true}}, nil, nil, nil, quietLog())
	c.pollMC(context.Background())
	if _, ok := pendingValue(c, "mc.mspt"); ok {
		t.Error("mc.mspt ohne Messwert geschrieben (Graph soll Lücke zeigen)")
	}
}

func TestPublishAndEnqueueExported(t *testing.T) {
	c := New(Config{}, nil, nil, nil, nil, nil, quietLog())
	ch, cancel := c.Subscribe()
	defer cancel()
	c.Publish(Event{Type: "perf", Data: 1})
	select {
	case ev := <-ch:
		if ev.Type != "perf" {
			t.Errorf("Type = %s", ev.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("kein Ereignis")
	}
	c.Enqueue(Sample{Series: "jvm.cpu.total", Time: time.Now(), Value: 3})
	if v, ok := pendingValue(c, "jvm.cpu.total"); !ok || v != 3 {
		t.Errorf("Enqueue: %v %v", v, ok)
	}
}

func TestNextInterval(t *testing.T) {
	if got := nextInterval(10*time.Second, true, false); got != 60*time.Second {
		t.Errorf("gestreckt ohne Zuschauer = %v, want 60s", got)
	}
	if got := nextInterval(10*time.Second, true, true); got != 10*time.Second {
		t.Errorf("mit Zuschauer = %v, want 10s", got)
	}
	// mc-Takt: auch ohne Zuschauer nicht strecken (Lag-Erkennung)
	if got := nextInterval(10*time.Second, false, false); got != 10*time.Second {
		t.Errorf("ungestreckt = %v, want 10s", got)
	}
}
