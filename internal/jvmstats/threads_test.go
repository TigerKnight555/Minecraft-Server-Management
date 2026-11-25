package jvmstats

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestGroupName(t *testing.T) {
	cases := map[string]string{
		"C2ME worker #3":           "C2ME worker",
		"GC Thread#0":              "GC Thread",
		"Worker-Main-12":           "Worker-Main",
		"Server thread":            "Server thread",
		"Netty Epoll Server IO #2": "Netty Epoll Server IO",
		"G1 Conc#0":                "G1 Conc",
		"Thread-7":                 "Thread",
		"12345":                    "12345",
	}
	for in, want := range cases {
		if got := GroupName(in); got != want {
			t.Errorf("GroupName(%q) = %q, want %q", in, got, want)
		}
	}
}

func pid500(context.Context) (int, error) { return 500, nil }

func TestThreadSamplerCPU(t *testing.T) {
	pf := newProcFixture(t)
	pf.thread(510, 510, "java", 100, 0)
	pf.thread(510, 511, "Server thread", 1000, 0)
	pf.thread(510, 512, "C2ME worker #0", 200, 0)
	pf.thread(510, 513, "C2ME worker #1", 300, 0)
	pf.thread(510, 514, "GC Thread#0", 50, 0)
	s := NewThreadSampler(pf.root, pid500)
	t0 := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)

	if _, ok, err := s.Sample(context.Background(), t0); err != nil || ok {
		t.Fatalf("erster Takt: ok=%v err=%v, want ok=false", ok, err)
	}
	// 5 s später: Server thread +500 Ticks = 5 s CPU in 5 s = 100 %
	pf.thread(510, 511, "Server thread", 1450, 50)
	pf.thread(510, 512, "C2ME worker #0", 300, 0) // +100 → 20 %
	pf.thread(510, 513, "C2ME worker #1", 400, 0) // +100 → 20 %
	pf.thread(510, 514, "GC Thread#0", 60, 0)     // +10 → 2 %
	got, ok, err := s.Sample(context.Background(), t0.Add(5*time.Second))
	if err != nil || !ok {
		t.Fatalf("zweiter Takt: ok=%v err=%v", ok, err)
	}
	if !got.ServerFound || !approx(got.ServerCPU, 100) {
		t.Errorf("ServerCPU = %v (found %v), want 100", got.ServerCPU, got.ServerFound)
	}
	if !approx(got.TotalCPU, 142) {
		t.Errorf("TotalCPU = %v, want 142", got.TotalCPU)
	}
	want := []ThreadGroup{
		{Name: "Server thread", Count: 1, CPU: 100},
		{Name: "C2ME worker", Count: 2, CPU: 40},
		{Name: "GC Thread", Count: 1, CPU: 2},
		{Name: "java", Count: 1, CPU: 0},
	}
	if len(got.Groups) != len(want) {
		t.Fatalf("Groups = %+v", got.Groups)
	}
	for i, w := range want {
		g := got.Groups[i]
		if g.Name != w.Name || g.Count != w.Count || !approx(g.CPU, w.CPU) {
			t.Errorf("Groups[%d] = %+v, want %+v", i, g, w)
		}
	}
}

func TestThreadSamplerNewThreadNoSpike(t *testing.T) {
	pf := newProcFixture(t)
	pf.thread(510, 511, "Server thread", 1000, 0)
	s := NewThreadSampler(pf.root, pid500)
	t0 := time.Now()
	s.Sample(context.Background(), t0)
	// neuer Thread mit vielen Ticks seit seinem Start: kein Vergleichsstand → 0 %
	pf.thread(510, 520, "Worker-Main-3", 90000, 0)
	got, ok, err := s.Sample(context.Background(), t0.Add(5*time.Second))
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if !approx(got.TotalCPU, 0) {
		t.Errorf("TotalCPU = %v, want 0", got.TotalCPU)
	}
}

func TestThreadSamplerPIDReuse(t *testing.T) {
	pf := newProcFixture(t)
	pf.thread(510, 511, "Server thread", 1000, 0)
	s := NewThreadSampler(pf.root, pid500)
	t0 := time.Now()
	s.Sample(context.Background(), t0)
	if s.pid != 510 {
		t.Fatalf("pid = %d, want 510", s.pid)
	}
	// Server neu gestartet: PID 510 gehört jetzt einem anderen Prozess
	// (andere starttime), die neue JVM hat PID 520
	pf.write("510/stat", statLine(510, "sh", 1, 0, 0, 99999))
	pf.proc(520, "java", 500, 20000)
	pf.thread(520, 521, "Server thread", 10, 0)
	if _, ok, err := s.Sample(context.Background(), t0.Add(5*time.Second)); err != nil || ok {
		t.Fatalf("nach Wechsel: ok=%v err=%v, want ok=false (neuer Vergleichsstand)", ok, err)
	}
	if s.pid != 520 || s.start != 20000 {
		t.Errorf("pid/start = %d/%d, want 520/20000", s.pid, s.start)
	}
	pf.thread(520, 521, "Server thread", 260, 0) // +250 in 5 s = 50 %
	got, ok, err := s.Sample(context.Background(), t0.Add(10*time.Second))
	if err != nil || !ok || !approx(got.ServerCPU, 50) {
		t.Errorf("ServerCPU = %v ok=%v err=%v, want 50", got.ServerCPU, ok, err)
	}
}

func TestThreadSamplerNoJava(t *testing.T) {
	pf := newProcFixture(t)
	pf.write("510/stat", statLine(510, "sh", 500, 0, 0, 12345)) // kein java mehr
	s := NewThreadSampler(pf.root, pid500)
	if _, _, err := s.Sample(context.Background(), time.Now()); !errors.Is(err, ErrNoJava) {
		t.Errorf("err = %v, want ErrNoJava", err)
	}
	s = NewThreadSampler(pf.root, func(context.Context) (int, error) {
		return 0, errors.New("Container läuft nicht")
	})
	if _, _, err := s.Sample(context.Background(), time.Now()); !errors.Is(err, ErrNoJava) {
		t.Errorf("err = %v, want ErrNoJava", err)
	}
}
