package jvmstats

import (
	"bytes"
	"errors"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// GCPause ist eine Stop-the-World-Pause aus dem GC-Log (-Xlog:gc).
type GCPause struct {
	Kind       string // Young, Mixed, Full, Remark, Cleanup, ...
	Before     uint64 // Heap vor der Pause, Bytes
	After      uint64 // Heap nach der Pause, Bytes
	Total      uint64 // Gesamt-Heap, Bytes
	DurationMs float64
}

// z. B. "GC(42) Pause Young (Normal) (G1 Evacuation Pause) 5000M->1200M(12288M) 12.345ms"
var pauseRe = regexp.MustCompile(`GC\(\d+\) Pause (\w+)(.*?) (\d+(?:\.\d+)?)([KMG])->(\d+(?:\.\d+)?)([KMG])\((\d+(?:\.\d+)?)([KMG])\) (\d+(?:\.\d+)?)ms`)

// ParseGCLine erkennt Pausen-Zeilen; alles andere (Concurrent-Phasen,
// Start-Infos, kaputte Zeilen) liefert ok=false.
func ParseGCLine(line string) (GCPause, bool) {
	m := pauseRe.FindStringSubmatch(line)
	if m == nil {
		return GCPause{}, false
	}
	dur, err := strconv.ParseFloat(m[9], 64)
	if err != nil {
		return GCPause{}, false
	}
	kind := m[1]
	if kind == "Young" && strings.Contains(m[2], "(Mixed)") {
		kind = "Mixed"
	}
	return GCPause{
		Kind:       kind,
		Before:     toBytes(m[3], m[4]),
		After:      toBytes(m[5], m[6]),
		Total:      toBytes(m[7], m[8]),
		DurationMs: dur,
	}, true
}

func toBytes(num, unit string) uint64 {
	v, _ := strconv.ParseFloat(num, 64)
	switch unit {
	case "K":
		v *= 1 << 10
	case "M":
		v *= 1 << 20
	case "G":
		v *= 1 << 30
	}
	return uint64(v)
}

const (
	maxReadPerPoll = 1 << 20  // höchstens 1 MB pro Takt
	primeWindow    = 64 << 10 // Vorlauf beim Start: nur das Dateiende
)

// GCTailer liest das GC-Log fortlaufend: Offset merken, bei gekürzter oder
// neuer Datei (Rotation) von vorn, abgeschnittene letzte Zeile bis zum
// nächsten Takt aufheben.
type GCTailer struct {
	path    string
	info    os.FileInfo // zuletzt gelesene Datei (Vergleich per os.SameFile)
	offset  int64
	partial []byte
	primed  bool
	last    GCPause
	hasLast bool
}

func NewGCTailer(path string) *GCTailer { return &GCTailer{path: path} }

// Last liefert die zuletzt gesehene Pause (auch aus dem Vorlauf).
func (t *GCTailer) Last() (GCPause, bool) { return t.last, t.hasLast }

// Poll liest neue vollständige Zeilen und liefert die gefundenen Pausen.
// Der erste erfolgreiche Aufruf liest nur die letzten 64 KB für den
// aktuellen Heap-Stand und liefert keine Pausen — alte Pausen dürfen
// keinen Lag auslösen.
func (t *GCTailer) Poll() ([]GCPause, error) {
	f, err := os.Open(t.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	priming := !t.primed
	switch {
	case priming:
		t.offset = max(0, info.Size()-primeWindow)
		t.partial = nil
	case t.info == nil || !os.SameFile(t.info, info) || info.Size() < t.offset:
		t.offset = 0 // rotiert, neu angelegt oder gekürzt → von vorn
		t.partial = nil
	}
	t.info = info
	t.primed = true
	startedMid := t.offset > 0
	n := min(info.Size()-t.offset, maxReadPerPoll)
	if n <= 0 {
		return nil, nil
	}
	buf := make([]byte, n)
	read, err := f.ReadAt(buf, t.offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	t.offset += int64(read)
	data := append(t.partial, buf[:read]...)
	lines := bytes.Split(data, []byte("\n"))
	t.partial = append([]byte(nil), lines[len(lines)-1]...)
	if len(t.partial) > maxReadPerPoll {
		t.partial = nil // Riesenzeile ohne Umbruch verwerfen
	}
	lines = lines[:len(lines)-1]
	if priming && startedMid && len(lines) > 0 {
		lines = lines[1:] // mitten in der Datei begonnen: erste Zeile angeschnitten
	}
	var out []GCPause
	for _, l := range lines {
		p, ok := ParseGCLine(strings.TrimRight(string(l), "\r"))
		if !ok {
			continue
		}
		t.last, t.hasLast = p, true
		out = append(out, p)
	}
	if priming {
		return nil, nil
	}
	return out, nil
}
