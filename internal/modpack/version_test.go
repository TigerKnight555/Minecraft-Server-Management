package modpack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func st(version, mc, loader string, files ...Entry) State {
	return State{Version: version, MC: mc, Loader: loader, Files: files}
}

var (
	sodium1 = Entry{Project: "P-sod", File: "sodium-1.jar", Version: "1"}
	sodium2 = Entry{Project: "P-sod", File: "sodium-2.jar", Version: "2"}
	jade    = Entry{Project: "P-jade", File: "jade.jar", Version: "1"}
)

func TestNext(t *testing.T) {
	prev := st("1.4.2", "26.3", "0.20.1", sodium1)
	cases := []struct {
		name        string
		prev, cur   State
		start, high string
		want        string
	}{
		{"erste Version = Startwert", State{}, st("", "26.3", "0.20.1", sodium1), "", "", "1.0.0"},
		{"erste Version eigener Startwert", State{}, st("", "26.3", "0.20.1", sodium1), "3.0.0", "", "3.0.0"},
		{"erste Version, Modrinth hat schon 2.3.0", State{}, st("", "26.3", "0.20.1", sodium1), "", "2.3.0", "2.4.0"},
		{"nur Mod-Update = PATCH", prev, st("", "26.3", "0.20.1", sodium2), "", "", "1.4.3"},
		{"Mod dazu = MINOR", prev, st("", "26.3", "0.20.1", sodium1, jade), "", "", "1.5.0"},
		{"Mod weg = MINOR", st("1.4.2", "26.3", "0.20.1", sodium1, jade), st("", "26.3", "0.20.1", sodium1), "", "", "1.5.0"},
		{"Loader neu = MINOR", prev, st("", "26.3", "0.21.0", sodium1), "", "", "1.5.0"},
		{"MC neu = MAJOR", prev, st("", "26.4", "0.21.0", sodium2), "", "", "2.0.0"},
		{"Modrinth höher als eigener Stand", prev, st("", "26.3", "0.20.1", sodium2), "", "1.9.0", "1.9.1"},
		{"Modrinth niedriger zählt nicht", prev, st("", "26.3", "0.20.1", sodium2), "", "1.0.0", "1.4.3"},
	}
	for _, c := range cases {
		if got := Next(c.prev, c.cur, c.start, c.high); got != c.want {
			t.Errorf("%s: Next = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestHighestAndValid(t *testing.T) {
	if h := Highest([]string{"1.2.0", "1.10.0", "kaputt", "1.9.9", "0.1"}); h != "1.10.0" {
		t.Errorf("Highest = %q, want 1.10.0", h)
	}
	if h := Highest([]string{"x"}); h != "" {
		t.Errorf("Highest ohne gültige = %q", h)
	}
	for v, want := range map[string]bool{"1.0.0": true, "10.2.33": true, "1.0": false, "v1.0.0": false, "1.0.0-beta": false, "": false} {
		if ValidVersion(v) != want {
			t.Errorf("ValidVersion(%q) != %v", v, want)
		}
	}
}

func TestDetectLoader(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"fabric-server-mc.1.21.11-loader.0.18.0-launcher.1.1.0.jar", "fabric-server-mc.26.3-loader.0.20.1-launcher.1.1.2.jar", "eula.txt"} {
		os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644)
	}
	if l := DetectLoader(dir, "26.3"); l != "0.20.1" {
		t.Errorf("loader = %q, want 0.20.1", l)
	}
	if l := DetectLoader(dir, "26.4"); l != "" {
		t.Errorf("loader für fremde MC-Version = %q, want leer", l)
	}
	if l := DetectLoader(filepath.Join(dir, "fehlt"), "26.3"); l != "" {
		t.Errorf("fehlender Ordner = %q", l)
	}
}

func TestDetectLoaderPrefersNewestJar(t *testing.T) {
	dir := t.TempDir()
	older := filepath.Join(dir, "fabric-server-mc.26.3-loader.0.21.0-launcher.1.1.2.jar")
	newer := filepath.Join(dir, "fabric-server-mc.26.3-loader.0.9.0-launcher.1.1.2.jar")
	for _, p := range []string{older, newer} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	if err := os.Chtimes(older, now.Add(-time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newer, now, now); err != nil {
		t.Fatal(err)
	}
	if l := DetectLoader(dir, "26.3"); l != "0.9.0" {
		t.Errorf("loader = %q, want 0.9.0 (neueste JAR)", l)
	}
	if err := os.Chtimes(older, now.Add(time.Hour), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if l := DetectLoader(dir, "26.3"); l != "0.21.0" {
		t.Errorf("loader = %q, want 0.21.0 (neueste JAR)", l)
	}
}

func TestChangelog(t *testing.T) {
	titles := map[string]string{"P-sod": "Sodium", "P-jade": "Jade"}
	first := Changelog(State{}, st("1.0.0", "26.3", "0.20.1", sodium1), titles, nil)
	if !strings.Contains(first, "Erste Version") {
		t.Errorf("erste = %q", first)
	}
	cl := Changelog(st("1.4.2", "26.3", "0.20.1", sodium1), st("2.0.0", "26.4", "0.21.0", sodium2, jade), titles, []string{"fremd.jar"})
	for _, want := range []string{"**Minecraft:** 26.3 → 26.4", "**Fabric Loader:** 0.20.1 → 0.21.0", "**Neu:** Jade", "**Aktualisiert:** Sodium (1 → 2)", "fremd.jar"} {
		if !strings.Contains(cl, want) {
			t.Errorf("changelog fehlt %q:\n%s", want, cl)
		}
	}
	gone := Changelog(st("1.4.2", "26.3", "0.20.1", sodium1, jade), st("1.5.0", "26.3", "0.20.1", sodium1), titles, nil)
	if !strings.Contains(gone, "**Entfernt:** Jade") {
		t.Errorf("entfernt = %q", gone)
	}
}
