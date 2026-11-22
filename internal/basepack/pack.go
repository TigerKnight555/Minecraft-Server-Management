// Package basepack lets the client pack build on a Modrinth modpack (z. B.
// Fabulously Optimized): Pack-Mods als getestete Kombination übernehmen und
// als Ganzes aktualisieren, einzelne abschalten, eigene Mods ergänzen, nur
// freigegebene Configs mitliefern.
package basepack

import (
	"crypto/sha1"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mrpack"
)

// PackFile is one client file of the base pack.
type PackFile struct {
	Path      string `json:"path"`
	Category  string `json:"category"`
	Filename  string `json:"filename"`
	ProjectID string `json:"projectId,omitempty"`
	Version   string `json:"version,omitempty"`
	Name      string `json:"name,omitempty"`
	SHA1      string `json:"sha1"`
	SHA512    string `json:"sha512"`
	URL       string `json:"url"`
	Size      int64  `json:"size"`
	// Override: Datei aus overrides/ bzw. client-overrides/ des Packs (keine
	// URL) — die Bytes kommen aus der geladenen .mrpack.
	Override bool `json:"override,omitempty"`
	// Key: eindeutiger Schlüssel im Pack (AssignKeys); leer = alter Stand
	Key string `json:"key,omitempty"`
}

// State is the persisted base-pack setup.
type State struct {
	ProjectID     string          `json:"projectId"`
	Slug          string          `json:"slug"`
	Title         string          `json:"title"`
	VersionID     string          `json:"versionId"`
	VersionNumber string          `json:"versionNumber"`
	MC            string          `json:"mc"`
	Files         []PackFile      `json:"files"`
	Disabled      []string        `json:"disabled,omitempty"`
	Configs       map[string]bool `json:"configs,omitempty"`
	// Absorbed: Schlüssel früher eigener Mods, die das Pack übernommen hat
	Absorbed []string `json:"absorbed,omitempty"`
	// WrittenConfigs: von MSM ins Client-Paket geschriebene Config-Pfade
	WrittenConfigs []string `json:"writtenConfigs,omitempty"`
}

// Key identifies a pack file across versions: der von AssignKeys vergebene
// Schlüssel, sonst (alter Stand) Modrinth-Projekt bzw. Pfad.
func Key(f PackFile) string {
	if f.Key != "" {
		return f.Key
	}
	if f.ProjectID != "" {
		return f.ProjectID
	}
	return f.Path
}

// AssignKeys gives every file a key unique within files: Datei in mods/ →
// Projekt-ID, in einem anderen Ordner → "<Projekt>@<Ordner>". Liefert ein
// Projekt im SELBEN Ordner mehrere Dateien, bekommen die weiteren (nach Pfad)
// "#2", "#3" … angehängt. So verschiebt eine neue Datei in einem Ordner nie
// die Schlüssel (und Abschaltungen) im anderen. Ohne Projekt: der Pfad.
func AssignKeys(files []PackFile) {
	order := make([]int, len(files))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return files[order[a]].Path < files[order[b]].Path })
	count := map[string]int{}
	for _, i := range order {
		f := &files[i]
		if f.ProjectID == "" {
			f.Key = f.Path
			continue
		}
		base := f.ProjectID
		if f.Category != "mods" {
			base += "@" + f.Category
		}
		count[base]++
		if n := count[base]; n > 1 {
			f.Key = fmt.Sprintf("%s#%d", base, n)
		} else {
			f.Key = base
		}
	}
}

// firstKeys maps each project to the key of its first file (nach Pfad).
func firstKeys(files []PackFile) map[string]string {
	out := map[string]string{}
	first := map[string]string{}
	for _, f := range files {
		if f.ProjectID == "" {
			continue
		}
		if p, ok := first[f.ProjectID]; !ok || f.Path < p {
			first[f.ProjectID] = f.Path
			out[f.ProjectID] = Key(f)
		}
	}
	return out
}

// normalizeKeys vergibt die Schlüssel neu und biegt nackte Projekt-IDs
// aus älteren Ständen (Disabled/Absorbed), die keiner Datei mehr gehören,
// auf die erste Datei des Projekts um.
func (s *State) normalizeKeys() {
	AssignKeys(s.Files)
	keys := map[string]bool{}
	for _, f := range s.Files {
		keys[Key(f)] = true
	}
	first := firstKeys(s.Files)
	fix := func(list []string) []string {
		var out []string
		for _, k := range list {
			if !keys[k] {
				if fk, ok := first[k]; ok {
					k = fk
				}
			}
			if !contains(out, k) {
				out = append(out, k)
			}
		}
		return out
	}
	s.Disabled = fix(s.Disabled)
	s.Absorbed = fix(s.Absorbed)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// IsDisabled reports whether the pack file with key is switched off.
func (s State) IsDisabled(key string) bool { return contains(s.Disabled, key) }

var categories = map[string]bool{"mods": true, "resourcepacks": true, "shaderpacks": true}

// FromIndex splits a parsed pack into client files, config paths and
// ignored paths (unbekannte Ordner, Unterordner, Dateien in overrides/
// unterhalb der Mod-Ordner, Dateien ohne erlaubte Download-Quelle).
// Nur-Server-Dateien fallen still weg.
func FromIndex(idx *mrpack.Index) (files []PackFile, configs []string, ignored []string) {
	for _, f := range idx.Files {
		if f.ClientEnv == "unsupported" {
			continue
		}
		dir, name := path.Split(f.Path)
		dir = strings.TrimSuffix(dir, "/")
		if !categories[dir] || name == "" {
			ignored = append(ignored, f.Path)
			continue
		}
		url := ""
		for _, u := range f.URLs {
			if mrpack.AllowedURL(u) {
				url = u
				break
			}
		}
		if url == "" { // keine erlaubte Quelle (Schutz vor beliebigen Abrufen)
			ignored = append(ignored, f.Path)
			continue
		}
		files = append(files, PackFile{Path: f.Path, Category: dir, Filename: name,
			SHA1: f.SHA1, SHA512: f.SHA512, URL: url, Size: f.Size})
	}
	// Overrides direkt in mods/, resourcepacks/, shaderpacks/ (z. B. FOs
	// „Mod Menu Helper.zip") werden Pack-Dateien wie Index-Dateien; die Bytes
	// liegen in der .mrpack. Gleicher Pfad wie eine Index-Datei: der Override
	// gewinnt (Launcher kopieren Overrides nach den Downloads).
	for p, data := range idx.Overrides {
		first := strings.SplitN(p, "/", 2)[0]
		if !categories[first] {
			configs = append(configs, p)
			continue
		}
		dir, name := path.Split(p)
		dir = strings.TrimSuffix(dir, "/")
		if !categories[dir] || name == "" {
			ignored = append(ignored, p) // Unterordner
			continue
		}
		s1 := sha1.Sum(data)
		s512 := sha512.Sum512(data)
		f := PackFile{Path: p, Category: dir, Filename: name, SHA1: hex.EncodeToString(s1[:]),
			SHA512: hex.EncodeToString(s512[:]), Size: int64(len(data)), Override: true}
		replaced := false
		for i := range files {
			if files[i].Path == p {
				files[i], replaced = f, true
			}
		}
		if !replaced {
			files = append(files, f)
		}
	}
	for _, p := range idx.Skipped { // über dem Größenlimit: Admin soll den Verlust sehen
		ignored = append(ignored, p+" (zu groß)")
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	sort.Strings(configs)
	sort.Strings(ignored)
	return files, configs, ignored
}

// DefaultOn is the preselection for a config file: Spieler-Einstellungen
// (Grafik, Tasten, Shader, Server-Liste, Minimap) und alles außerhalb von
// config/ sind aus — sie würden bei jedem Paket-Update überschrieben.
func DefaultOn(p string) bool {
	if !strings.HasPrefix(p, "config/") {
		return false
	}
	lower := strings.ToLower(p)
	switch {
	case lower == "config/sodium-options.json", lower == "config/iris.properties",
		strings.HasPrefix(lower, "config/xaero"), strings.HasPrefix(lower, "config/controlling"),
		strings.Contains(path.Base(lower), "keybind"):
		return false
	}
	return true
}

// OwnFile is a file the admin added himself (nicht aus dem Pack). Key ist
// beim Aufruf von Diff die Modrinth-Projekt-ID, in Plan.AbsorbOwn der
// Schlüssel der übernehmenden Pack-Datei.
type OwnFile struct {
	Category string `json:"category"`
	Filename string `json:"filename"`
	Key      string `json:"key"`
	Name     string `json:"name"`
}

// FileUpdate is a pack file whose content changed between versions.
type FileUpdate struct {
	Old PackFile `json:"old"`
	New PackFile `json:"new"`
}

// Plan is the difference between the current and a new pack version.
type Plan struct {
	Add    []PackFile   `json:"add"`
	Update []FileUpdate `json:"update"`
	Remove []PackFile   `json:"remove"`
	// AbsorbOwn: eigene Dateien, deren Projekt jetzt Teil des Packs ist
	// (neu hinzugefügt, aktualisiert oder unverändert).
	AbsorbOwn    []OwnFile `json:"absorbOwn"`
	DropAbsorbed []string  `json:"dropAbsorbed"`
	NewConfigs   []string  `json:"newConfigs"`
}

// Diff compares the current state with the files and configs of a new
// pack version. own sind die eigenen Dateien des Client-Pakets.
func Diff(prev State, next []PackFile, nextConfigs []string, own []OwnFile) Plan {
	var p Plan
	old := map[string]PackFile{}
	for _, f := range prev.Files {
		old[Key(f)] = f
	}
	// eigene Dateien je Modrinth-Projekt + Ordner (OwnFile.Key = Projekt-ID)
	ownBy := map[string][]OwnFile{}
	for _, o := range own {
		if o.Key != "" {
			ownBy[projCat(o.Key, o.Category)] = append(ownBy[projCat(o.Key, o.Category)], o)
		}
	}
	// Ist eine Pack-Datei des Projekts im selben Ordner abgeschaltet, ist die
	// eigene Kopie der bewusste Ersatz — keine Übernahme.
	off := map[string]bool{}
	for _, n := range next {
		if n.ProjectID != "" && prev.IsDisabled(Key(n)) {
			off[projCat(n.ProjectID, n.Category)] = true
		}
	}
	seen := map[string]bool{}
	for _, n := range next {
		k := Key(n)
		seen[k] = true
		o, ok := old[k]
		switch {
		case !ok:
			p.Add = append(p.Add, n)
		case o.SHA512 != n.SHA512:
			p.Update = append(p.Update, FileUpdate{Old: o, New: n})
		}
		// Übernahme nur durch eine Pack-Datei desselben Projekts UND Ordners
		// (die erste); in AbsorbOwn steht dann deren Pack-Schlüssel.
		if pc := projCat(n.ProjectID, n.Category); n.ProjectID != "" && !off[pc] {
			for _, of := range ownBy[pc] {
				of.Key = k
				p.AbsorbOwn = append(p.AbsorbOwn, of)
			}
			delete(ownBy, pc)
		}
	}
	for _, o := range prev.Files {
		if seen[Key(o)] {
			continue
		}
		p.Remove = append(p.Remove, o)
		if contains(prev.Absorbed, Key(o)) {
			name := o.Name
			if name == "" {
				name = o.Filename
			}
			p.DropAbsorbed = append(p.DropAbsorbed, name)
		}
	}
	for _, c := range nextConfigs {
		if _, known := prev.Configs[c]; !known && prev.ProjectID != "" {
			p.NewConfigs = append(p.NewConfigs, c)
		}
	}
	return p
}

func projCat(project, category string) string { return project + "/" + category }

// Summary is the one-line Discord/changelog description of a pack change.
func Summary(prev State, title, version string, p Plan, total int) string {
	if prev.VersionNumber == "" {
		return fmt.Sprintf("Basis-Modpack %s %s übernommen (%d Mods)", title, version, total)
	}
	return fmt.Sprintf("%s %s → %s (%d neu, %d entfernt, %d aktualisiert)",
		title, prev.VersionNumber, version, len(p.Add), len(p.Remove), len(p.Update))
}
