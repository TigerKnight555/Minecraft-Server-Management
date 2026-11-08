package mods

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/modrinth"
)

const maxDepDepth = 3

// PlannedFile is one mod the add dialog shows (Haupt-Mod oder Abhängigkeit).
type PlannedFile struct {
	ProjectID     string          `json:"projectId"`
	Name          string          `json:"name"`
	VersionNumber string          `json:"versionNumber"`
	Filename      string          `json:"filename"`
	ClientSide    string          `json:"clientSide"`
	ServerSide    string          `json:"serverSide"`
	Present       map[string]bool `json:"present"` // Profil -> schon installiert/vorgemerkt
	URL           string          `json:"-"`
	SHA512        string          `json:"-"`
}

// AddPlan is everything the add dialog needs. Blocked != "" = Hinzufügen
// nicht möglich (Grund steht drin).
type AddPlan struct {
	Main     PlannedFile   `json:"main"`
	Deps     []PlannedFile `json:"deps"`
	Server   bool          `json:"server"` // Vorschlag aus Modrinth
	Client   bool          `json:"client"`
	Warnings []string      `json:"warnings"`
	Blocked  string        `json:"blocked,omitempty"`
}

// AddResult lists the added mod names per profile.
type AddResult struct {
	Added map[string][]string `json:"added"`
}

type installedMod struct {
	profiles map[string]bool
	files    map[string]string // Profil -> Dateiname
	name     string
	deps     []modrinth.Dependency
}

// Search finds mods with a build for the current MC version + loader.
func (m *Manager) Search(ctx context.Context, query, mcVersion string) ([]modrinth.SearchHit, error) {
	hits, err := m.api.Search(ctx, query, m.loader, mcVersion, 20)
	if err != nil {
		return nil, fmt.Errorf("modrinth-suche: %w", err)
	}
	if hits == nil {
		hits = []modrinth.SearchHit{}
	}
	return hits, nil
}

// inventory maps project IDs of all installed mods (Kategorie "mods", alle
// Profile, ein Modrinth-Aufruf) plus vorgemerkter Neuzugänge.
func (m *Manager) inventory(ctx context.Context) (map[string]*installedMod, error) {
	type ref struct{ profile, file, hash string }
	var refs []ref
	var hashes []string
	for _, p := range m.profiles {
		entries, err := scanFiles(p)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.Category != "mods" {
				continue
			}
			refs = append(refs, ref{p.Name, e.Filename, e.SHA512})
			hashes = append(hashes, e.SHA512)
		}
	}
	versions, err := m.api.VersionsByHash(ctx, hashes)
	if err != nil {
		return nil, fmt.Errorf("modrinth identify: %w", err)
	}
	inv := map[string]*installedMod{}
	add := func(pid, profile, file, name string, deps []modrinth.Dependency) {
		im, ok := inv[pid]
		if !ok {
			im = &installedMod{profiles: map[string]bool{}, files: map[string]string{}, name: name}
			inv[pid] = im
		}
		im.profiles[profile] = true
		im.files[profile] = file
		im.deps = append(im.deps, deps...)
	}
	for _, r := range refs {
		if v, ok := versions[r.hash]; ok && v.ProjectID != "" {
			add(v.ProjectID, r.profile, r.file, v.Name, v.Dependencies)
		}
	}
	for _, p := range m.profiles {
		for _, it := range m.stagedItems(p) {
			if it.action() == "add" && it.ProjectID != "" {
				add(it.ProjectID, p.Name, it.NewFilename, it.Name, nil)
			}
		}
	}
	return inv, nil
}

func sideWanted(s string) bool { return s == "required" || s == "optional" }

// planFile resolves one project to its newest build; ver == nil = kein Build.
func (m *Manager) planFile(ctx context.Context, projectID, mcVersion string, inv map[string]*installedMod) (PlannedFile, *modrinth.Version, error) {
	proj, err := m.api.Project(ctx, projectID)
	if err != nil {
		return PlannedFile{}, nil, fmt.Errorf("modrinth-projekt %s: %w", projectID, err)
	}
	pf := PlannedFile{ProjectID: proj.ID, Name: proj.Title, ClientSide: proj.ClientSide,
		ServerSide: proj.ServerSide, Present: map[string]bool{}}
	if pf.ProjectID == "" {
		pf.ProjectID = projectID
	}
	if im, ok := inv[pf.ProjectID]; ok {
		for p := range im.profiles {
			pf.Present[p] = true
		}
	}
	vs, err := m.api.ProjectVersions(ctx, pf.ProjectID, m.loader, mcVersion)
	if err != nil {
		return pf, nil, fmt.Errorf("modrinth-versionen %s: %w", pf.Name, err)
	}
	if len(vs) == 0 {
		return pf, nil, nil
	}
	v := vs[0]
	f, ok := v.PrimaryFile()
	if !ok {
		return pf, nil, nil
	}
	pf.VersionNumber, pf.Filename, pf.URL, pf.SHA512 = v.VersionNumber, f.Filename, f.URL, f.Hashes.SHA512
	return pf, &v, nil
}

// Plan builds the add dialog for one Modrinth project.
func (m *Manager) Plan(ctx context.Context, projectID, mcVersion string) (*AddPlan, error) {
	if mcVersion == "" {
		return nil, fmt.Errorf("Minecraft-Version unbekannt (Server startet noch?)")
	}
	inv, err := m.inventory(ctx)
	if err != nil {
		return nil, err
	}
	main, ver, err := m.planFile(ctx, projectID, mcVersion, inv)
	if err != nil {
		return nil, err
	}
	plan := &AddPlan{Main: main, Deps: []PlannedFile{}, Warnings: []string{},
		Server: sideWanted(main.ServerSide), Client: sideWanted(main.ClientSide)}
	if !plan.Server && !plan.Client {
		plan.Server = true // Modrinth weiß es nicht — Server ist der sichere Vorschlag
	}
	if ver == nil {
		plan.Blocked = fmt.Sprintf("%s hat noch keinen Build für Minecraft %s (%s)", main.Name, mcVersion, m.loader)
		return plan, nil
	}

	type queued struct {
		v     *modrinth.Version
		depth int
	}
	seen := map[string]bool{main.ProjectID: true}
	queue := []queued{{ver, 1}}
	truncated := false
	for len(queue) > 0 {
		q := queue[0]
		queue = queue[1:]
		for _, d := range q.v.Dependencies {
			switch d.DependencyType {
			case "incompatible":
				if im, ok := inv[d.ProjectID]; ok && d.ProjectID != "" {
					plan.Warnings = append(plan.Warnings,
						fmt.Sprintf("Laut Modrinth unverträglich mit dem installierten Mod %s", im.name))
				}
			case "required":
				if d.ProjectID == "" {
					plan.Warnings = append(plan.Warnings, "Eine Pflicht-Abhängigkeit nennt kein Projekt — bitte auf Modrinth prüfen")
					continue
				}
				if seen[d.ProjectID] {
					continue
				}
				seen[d.ProjectID] = true
				if q.depth > maxDepDepth {
					truncated = true
					continue
				}
				dep, dv, err := m.planFile(ctx, d.ProjectID, mcVersion, inv)
				if err != nil {
					return nil, err
				}
				if dv == nil {
					plan.Blocked = fmt.Sprintf("Abhängigkeit %s hat noch keinen Build für Minecraft %s", dep.Name, mcVersion)
					continue
				}
				if len(dep.Present) < len(m.profiles) {
					plan.Deps = append(plan.Deps, dep)
				}
				queue = append(queue, queued{dv, q.depth + 1})
			}
		}
	}
	if truncated {
		plan.Warnings = append(plan.Warnings, "Abhängigkeiten tiefer als 3 Ebenen wurden nicht geprüft — nach dem Neustart das Server-Log ansehen")
	}
	return plan, nil
}

// AddMods stages the mod plus missing dependencies per profile. Server:
// vorgemerkt bis zum Neustart; Client: sofort angewendet (gleiche Sicherung).
func (m *Manager) AddMods(ctx context.Context, projectID, mcVersion string, profiles []string) (*AddResult, error) {
	if len(profiles) == 0 {
		return nil, fmt.Errorf("mindestens ein Profil wählen")
	}
	for _, name := range profiles {
		if _, err := m.profile(name); err != nil {
			return nil, err
		}
	}
	plan, err := m.Plan(ctx, projectID, mcVersion)
	if err != nil {
		return nil, err
	}
	if plan.Blocked != "" {
		return nil, errors.New(plan.Blocked)
	}
	files := append([]PlannedFile{plan.Main}, plan.Deps...)
	res := &AddResult{Added: map[string][]string{}}
	data := map[string][]byte{} // sha512 -> Inhalt (einmal laden für beide Profile)
	for _, name := range profiles {
		p, _ := m.profile(name)
		live, ok := p.Dirs["mods"]
		if !ok {
			return res, fmt.Errorf("Profil %s hat keinen mods-Ordner", name)
		}
		sdir := stagingDir(p)
		if err := os.MkdirAll(sdir, 0o755); err != nil {
			return res, err
		}
		manPath := filepath.Join(sdir, "manifest.json")
		man, _ := readManifest(manPath)
		man.Created = time.Now()
		for _, f := range files {
			if f.Present[name] {
				continue
			}
			if _, err := os.Stat(filepath.Join(live, f.Filename)); err == nil {
				return res, fmt.Errorf("%s liegt bereits im Profil %s", f.Filename, name)
			}
			b, ok := data[f.SHA512]
			if !ok {
				b, err = m.api.Download(ctx, f.URL)
				if err != nil {
					return res, fmt.Errorf("download %s: %w", f.Filename, err)
				}
				sum := sha512.Sum512(b)
				if hex.EncodeToString(sum[:]) != f.SHA512 {
					return res, fmt.Errorf("hash-verifikation fehlgeschlagen für %s — abgebrochen", f.Filename)
				}
				data[f.SHA512] = b
			}
			if err := os.WriteFile(filepath.Join(sdir, f.Filename), b, 0o644); err != nil {
				return res, err
			}
			man.Items = append(man.Items, stagingItem{
				Category: "mods", NewFilename: f.Filename, SHA512: f.SHA512, Version: f.VersionNumber,
				ProjectID: f.ProjectID, Name: f.Name, ClientSide: f.ClientSide,
			})
			res.Added[name] = append(res.Added[name], f.Name)
		}
		if len(res.Added[name]) == 0 {
			continue
		}
		if err := writeManifest(manPath, man); err != nil {
			return res, err
		}
		if name == ProfileClient {
			if _, _, err := m.ApplyStaged(name); err != nil {
				return res, fmt.Errorf("client-paket: %w", err)
			}
		}
	}
	if len(res.Added) == 0 {
		return res, fmt.Errorf("%s ist in den gewählten Profilen schon vorhanden oder vorgemerkt", plan.Main.Name)
	}
	return res, nil
}
