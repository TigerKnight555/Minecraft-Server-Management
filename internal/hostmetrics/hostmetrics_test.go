package hostmetrics

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// writeProcFixtures builds a fake /proc directory.
func writeProcFixtures(t *testing.T, stat string) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"meminfo": "MemTotal:       16000000 kB\nMemFree:         1000000 kB\nMemAvailable:    4000000 kB\n",
		"stat":    stat,
		"loadavg": "1.25 0.90 0.60 2/345 6789\n",
		"uptime":  "260000.55 100000.00\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestSample(t *testing.T) {
	dir := writeProcFixtures(t, "cpu  100 0 100 700 100 0 0 0 0 0\n")
	s := New(dir, "", "")

	got, err := s.Sample(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.MemTotal != 16000000*1024 {
		t.Errorf("MemTotal = %d, want %d", got.MemTotal, 16000000*1024)
	}
	if want := uint64((16000000 - 4000000) * 1024); got.MemUsed != want {
		t.Errorf("MemUsed = %d, want %d", got.MemUsed, want)
	}
	if got.Load1 != 1.25 || got.Load5 != 0.90 || got.Load15 != 0.60 {
		t.Errorf("load = %v/%v/%v, want 1.25/0.90/0.60", got.Load1, got.Load5, got.Load15)
	}
	if got.UptimeSec != 260000 {
		t.Errorf("UptimeSec = %d, want 260000", got.UptimeSec)
	}
	// first CPU sample has no delta
	if got.CPUPercent != 0 {
		t.Errorf("first CPUPercent = %v, want 0", got.CPUPercent)
	}
	if got.NASOnline {
		t.Error("NASOnline = true without configured nas path")
	}
}

func TestCPUDelta(t *testing.T) {
	dir := writeProcFixtures(t, "cpu  100 0 100 700 100 0 0 0 0 0\n")
	s := New(dir, "", "")
	if _, err := s.Sample(context.Background()); err != nil {
		t.Fatal(err)
	}
	// busy delta 200, idle delta 200 → 50%
	err := os.WriteFile(filepath.Join(dir, "stat"), []byte("cpu  200 0 200 800 200 0 0 0 0 0\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Sample(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.CPUPercent != 50 {
		t.Errorf("CPUPercent = %v, want 50", got.CPUPercent)
	}
}

func TestNASCheck(t *testing.T) {
	dir := writeProcFixtures(t, "cpu  1 0 1 1 1 0 0 0 0 0\n")
	nas := t.TempDir()
	s := New(dir, "", nas)
	got, err := s.Sample(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got.NASOnline {
		t.Error("NASOnline = false for existing directory")
	}
}
