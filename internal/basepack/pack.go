// Package basepack lets the client pack build on a Modrinth modpack (z. B.
// Fabulously Optimized): Pack-Mods als getestete Kombination übernehmen und
// als Ganzes aktualisieren, einzelne abschalten, eigene Mods ergänzen, nur
// freigegebene Configs mitliefern.
package basepack

import (
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

// AssignKeys gives every file a key unique within files: das Modrinth-Projekt;
// liefert ein Projekt mehrere Dateien, bekommt die erste (nach Pfad) die
// Projekt-ID, jede weitere "<Projekt>#2", "#3" … — über Versionen stabil,
// solange die Dateien des Projekts gleich bleiben. Ohne Projekt: der Pfad.
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
		count[f.ProjectID]++
		if n := count[f.ProjectID]; n > 1 {
			f.Key = fmt.Sprintf("%s#%d", f.ProjectID, n)
		} else {
			f.Key = f.ProjectID
		}
	}
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
// unterhalb der Mod-Ordner). Nur-Server-Dateien fallen still weg.
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
		if len(f.URLs) > 0 {
			url = f.URLs[0]
		}
		files = append(files, PackFile{Path: f.Path, Category: dir, Filename: name,
			SHA1: f.SHA1, SHA512: f.SHA512, URL: url, Size: f.Size})
	}
	for p := range idx.Overrides {
		first := strings.SplitN(p, "/", 2)[0]
		if categories[first] {
			ignored = append(ignored, p)
			continue
		}
		configs = append(configs, p)
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
	// eigene Dateien je Modrinth-Projekt (OwnFile.Key = Projekt-ID)
	ownByProject := map[string][]OwnFile{}
	for _, o := range own {
		if o.Key != "" {
			ownByProject[o.Key] = append(ownByProject[o.Key], o)
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
		// Übernahme durch die erste angeschaltete Pack-Datei des Projekts;
		// in AbsorbOwn steht dann deren Pack-Schlüssel.
		if n.ProjectID != "" && !prev.IsDisabled(k) {
			for _, of := range ownByProject[n.ProjectID] {
				of.Key = k
				p.AbsorbOwn = append(p.AbsorbOwn, of)
			}
			delete(ownByProject, n.ProjectID)
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

// Summary is the one-line Discord/changelog description of a pack change.
func Summary(prev State, title, version string, p Plan, total int) string {
	if prev.VersionNumber == "" {
		return fmt.Sprintf("Basis-Modpack %s %s übernommen (%d Mods)", title, version, total)
	}
	return fmt.Sprintf("%s %s → %s (%d neu, %d entfernt, %d aktualisiert)",
		title, prev.VersionNumber, version, len(p.Add), len(p.Remove), len(p.Update))
}
