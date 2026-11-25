package jvmstats

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const mib = 1 << 20

func TestParseGCLineKinds(t *testing.T) {
	data, err := os.ReadFile("testdata/gc/gc.log")
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, line := range strings.Split(string(data), "\n") {
		if p, ok := ParseGCLine(line); ok {
			kinds = append(kinds, p.Kind)
		}
	}
	want := "Young,Mixed,Full,Remark,Cleanup"
	if got := strings.Join(kinds, ","); got != want {
		t.Errorf("kinds = %s, want %s", got, want)
	}
}

func TestParseGCLineValues(t *testing.T) {
	p, ok := ParseGCLine("[2026-11-25T10:00:05.000+0100][128.333s] GC(43) Pause Full (G1 Compaction Pause) 11800M->6000M(12288M) 812.004ms")
	if !ok {
		t.Fatal("nicht erkannt")
	}
	if p.Kind != "Full" || p.Before != 11800*mib || p.After != 6000*mib || p.Total != 12288*mib || p.DurationMs != 812.004 {
		t.Errorf("p = %+v", p)
	}
}

func TestParseGCLineUnits(t *testing.T) {
	p, ok := ParseGCLine("[x][1.0s] GC(1) Pause Young (Normal) (G1 Evacuation Pause) 512K->256K(2G) 0.500ms\r")
	if !ok {
		t.Fatal("nicht erkannt")
	}
	if p.Before != 512<<10 || p.After != 256<<10 || p.Total != 2<<30 || p.DurationMs != 0.5 {
		t.Errorf("p = %+v", p)
	}
}

func TestParseGCLineIgnoresOthers(t *testing.T) {
	for _, l := range []string{
		"[2026-11-25T10:00:06.500+0100][129.500s] GC(44) Concurrent Mark Cycle 210.441ms",
		"[2026-11-25T09:59:58.000+0100][0.010s] Using G1",
		"kaputte Zeile ohne alles",
		"",
	} {
		if _, ok := ParseGCLine(l); ok {
			t.Errorf("%q fälschlich als Pause erkannt", l)
		}
	}
}

const (
	lineYoung  = "[2026-11-25T10:00:00.123+0100][123.456s] GC(42) Pause Young (Normal) (G1 Evacuation Pause) 5000M->1200M(12288M) 12.345ms\n"
	lineFull   = "[2026-11-25T10:00:05.000+0100][128.333s] GC(43) Pause Full (G1 Compaction Pause) 11800M->6000M(12288M) 812.004ms\n"
	lineRemark = "[2026-11-25T10:00:06.000+0100][129.000s] GC(44) Pause Remark 6100M->6050M(12288M) 3.210ms\n"
)

func appendTo(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

func TestTailerPrimesWithoutPauses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gc.log")
	appendTo(t, path, lineYoung+lineFull)
	tl := NewGCTailer(path)
	pauses, err := tl.Poll()
	if err != nil || len(pauses) != 0 {
		t.Fatalf("Vorlauf: %v, %v — alte Pausen dürfen nicht gemeldet werden", pauses, err)
	}
	last, ok := tl.Last()
	if !ok || last.After != 6000*mib {
		t.Errorf("Last = %+v, want Heap-Stand der letzten Zeile", last)
	}
	appendTo(t, path, lineRemark)
	pauses, err = tl.Poll()
	if err != nil || len(pauses) != 1 || pauses[0].Kind != "Remark" {
		t.Errorf("neue Zeile: %+v, %v", pauses, err)
	}
}

func TestTailerTruncatedLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gc.log")
	appendTo(t, path, "")
	tl := NewGCTailer(path)
	tl.Poll() // Vorlauf auf leerer Datei
	appendTo(t, path, lineFull[:40])
	if pauses, _ := tl.Poll(); len(pauses) != 0 {
		t.Fatalf("halbe Zeile ausgewertet: %+v", pauses)
	}
	appendTo(t, path, lineFull[40:])
	pauses, err := tl.Poll()
	if err != nil || len(pauses) != 1 || pauses[0].DurationMs != 812.004 {
		t.Errorf("zusammengesetzte Zeile: %+v, %v", pauses, err)
	}
}

func TestTailerTruncatedFileRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gc.log")
	appendTo(t, path, "")
	tl := NewGCTailer(path)
	tl.Poll()
	appendTo(t, path, lineYoung+lineFull+lineYoung)
	tl.Poll()
	// Datei geleert und neu begonnen (kleiner als der Offset) → von vorn
	if err := os.WriteFile(path, []byte(lineRemark), 0o644); err != nil {
		t.Fatal(err)
	}
	pauses, err := tl.Poll()
	if err != nil || len(pauses) != 1 || pauses[0].Kind != "Remark" {
		t.Errorf("nach Kürzen: %+v, %v", pauses, err)
	}
}

func TestTailerRotationNewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gc.log")
	appendTo(t, path, "")
	tl := NewGCTailer(path)
	tl.Poll()
	appendTo(t, path, lineYoung)
	tl.Poll()
	// JVM rotiert: gc.log → gc.log.0, neue (größere) gc.log
	if err := os.Rename(path, path+".0"); err != nil {
		t.Fatal(err)
	}
	appendTo(t, path, lineFull+lineRemark+lineYoung)
	pauses, err := tl.Poll()
	if err != nil || len(pauses) != 3 || pauses[0].Kind != "Full" {
		t.Errorf("nach Rotation: %+v, %v", pauses, err)
	}
}

func TestTailerReadsAtMost1MB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gc.log")
	appendTo(t, path, "")
	tl := NewGCTailer(path)
	tl.Poll()
	filler := strings.Repeat(strings.Repeat("x", 99)+"\n", 12000) // 1,2 MB
	appendTo(t, path, filler+lineFull)
	if pauses, _ := tl.Poll(); len(pauses) != 0 {
		t.Fatalf("erster Takt hat mehr als 1 MB gelesen: %+v", pauses)
	}
	pauses, err := tl.Poll()
	if err != nil || len(pauses) != 1 {
		t.Errorf("zweiter Takt: %+v, %v", pauses, err)
	}
}

func TestTailerMissingFile(t *testing.T) {
	tl := NewGCTailer(filepath.Join(t.TempDir(), "gc.log"))
	if _, err := tl.Poll(); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v, want fs.ErrNotExist", err)
	}
}
