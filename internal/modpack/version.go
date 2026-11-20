// Package modpack is the Modrinth channel of the client-pack publish: es
// löst die Dateien des Client-Pakets bei Modrinth auf, zählt die eigene
// Paket-Versionsnummer, baut die .mrpack und lädt sie als neue Version des
// Modpack-Projekts hoch.
package modpack

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Entry is one published file (Projekt-ID, Dateiname, Modrinth-Versionsnummer).
type Entry struct {
	Project string `json:"project"`
	File    string `json:"file"`
	Version string `json:"version"`
}

// State is the content of one published modpack version.
type State struct {
	Version string  `json:"version"`
	MC      string  `json:"mc"`
	Loader  string  `json:"loader"`
	Files   []Entry `json:"files"`
}

// Bump is the version part to increase.
type Bump int

const (
	Patch Bump = iota
	Minor
	Major
)

// Kind decides the bump (Nutzer-Regel): andere MC-Version → MAJOR; anderer
// Loader oder Projekte dazu/weg → MINOR; sonst PATCH.
func Kind(prev, cur State) Bump {
	if prev.MC != cur.MC {
		return Major
	}
	if prev.Loader != cur.Loader || !sameProjects(prev.Files, cur.Files) {
		return Minor
	}
	return Patch
}

func sameProjects(a, b []Entry) bool {
	set := map[string]bool{}
	for _, e := range a {
		set[e.Project] = true
	}
	other := map[string]bool{}
	for _, e := range b {
		other[e.Project] = true
		if !set[e.Project] {
			return false
		}
	}
	return len(set) == len(other)
}

type semver [3]int

func parse(v string) (semver, bool) {
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	var out semver
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || strconv.Itoa(n) != p {
			return semver{}, false
		}
		out[i] = n
	}
	return out, true
}

func (s semver) String() string { return fmt.Sprintf("%d.%d.%d", s[0], s[1], s[2]) }

func (s semver) cmp(o semver) int {
	for i := range s {
		if s[i] != o[i] {
			if s[i] < o[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func (s semver) bump(k Bump) semver {
	switch k {
	case Major:
		return semver{s[0] + 1, 0, 0}
	case Minor:
		return semver{s[0], s[1] + 1, 0}
	default:
		return semver{s[0], s[1], s[2] + 1}
	}
}

// ValidVersion reports whether v is MAJOR.MINOR.PATCH.
func ValidVersion(v string) bool {
	_, ok := parse(v)
	return ok
}

// Highest returns the greatest valid version number ("" = keine gültige).
func Highest(numbers []string) string {
	var best semver
	found := false
	for _, n := range numbers {
		if v, ok := parse(n); ok && (!found || v.cmp(best) > 0) {
			best, found = v, true
		}
	}
	if !found {
		return ""
	}
	return best.String()
}

// Next returns the version number for cur. start ist der Startwert aus den
// Einstellungen ("" = 1.0.0), highest die höchste schon auf Modrinth
// liegende Versionsnummer ("" = keine).
func Next(prev, cur State, start, highest string) string {
	s, ok := parse(start)
	if !ok {
		s = semver{1, 0, 0}
	}
	h, hasH := parse(highest)
	if prev.Version == "" {
		if hasH && h.cmp(s) >= 0 {
			return h.bump(Minor).String()
		}
		return s.String()
	}
	base, ok := parse(prev.Version)
	if !ok {
		base = s
	}
	if hasH && h.cmp(base) > 0 {
		base = h
	}
	return base.bump(Kind(prev, cur)).String()
}

var reServerJar = regexp.MustCompile(`^fabric-server-mc\.(.+)-loader\.(.+)-launcher\.(.+)\.jar$`)

// DetectLoader reads the Fabric loader version from the server launcher JAR
// in the MC data dir (z. B. fabric-server-mc.26.3-loader.0.20.1-launcher.1.1.2.jar)
// — genau der Loader, mit dem der Server läuft. "" = nicht gefunden.
func DetectLoader(dataDir, mc string) string {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if m := reServerJar.FindStringSubmatch(e.Name()); m != nil && m[1] == mc {
			return m[2]
		}
	}
	return ""
}

// Changelog describes cur against prev (Markdown für die Modrinth-Seite).
func Changelog(prev, cur State, titles map[string]string, omitted []string) string {
	name := func(e Entry) string {
		if t := titles[e.Project]; t != "" {
			return t
		}
		return e.File
	}
	var lines []string
	if prev.Version == "" {
		lines = append(lines, fmt.Sprintf("Erste Version: %d Dateien (MC %s · Fabric %s).", len(cur.Files), cur.MC, cur.Loader))
	} else {
		if prev.MC != cur.MC {
			lines = append(lines, fmt.Sprintf("**Minecraft:** %s → %s", prev.MC, cur.MC))
		}
		if prev.Loader != cur.Loader {
			lines = append(lines, fmt.Sprintf("**Fabric Loader:** %s → %s", prev.Loader, cur.Loader))
		}
		old := map[string]Entry{}
		for _, e := range prev.Files {
			old[e.Project] = e
		}
		now := map[string]bool{}
		var added, removed, updated []string
		for _, e := range cur.Files {
			now[e.Project] = true
			o, ok := old[e.Project]
			switch {
			case !ok:
				added = append(added, name(e))
			case o.Version != e.Version:
				updated = append(updated, fmt.Sprintf("%s (%s → %s)", name(e), o.Version, e.Version))
			}
		}
		for _, e := range prev.Files {
			if !now[e.Project] {
				removed = append(removed, name(e))
			}
		}
		for _, part := range []struct {
			label string
			items []string
		}{{"Neu", added}, {"Entfernt", removed}, {"Aktualisiert", updated}} {
			if len(part.items) > 0 {
				sort.Strings(part.items)
				lines = append(lines, "**"+part.label+":** "+strings.Join(part.items, ", "))
			}
		}
		if len(lines) == 0 {
			lines = append(lines, "Keine inhaltlichen Änderungen (Paket neu veröffentlicht).")
		}
	}
	if len(omitted) > 0 {
		o := append([]string(nil), omitted...)
		sort.Strings(o)
		lines = append(lines, "**Nicht im Modpack enthalten** (nicht auf Modrinth): "+strings.Join(o, ", "))
	}
	return strings.Join(lines, "\n")
}
