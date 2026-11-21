package basepack

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/modrinth"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mods"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mrpack"
)

const (
	stateKey = "basepack.state"
	loader   = "fabric"
	maxPack  = 64 << 20
)

// API is the Modrinth surface the service needs (*modrinth.Client).
type API interface {
	SearchModpacks(ctx context.Context, query, loader, mcVersion string, limit int) ([]modrinth.SearchHit, error)
	Project(ctx context.Context, id string) (modrinth.Project, error)
	ProjectVersions(ctx context.Context, projectID, loader, mcVersion string) ([]modrinth.Version, error)
	VersionsByHash(ctx context.Context, sha512s []string) (map[string]modrinth.Version, error)
	Projects(ctx context.Context, ids []string) ([]modrinth.Project, error)
	Download(ctx context.Context, url string) ([]byte, error)
}

// Files is the mod-manager surface (*mods.Manager).
type Files interface {
	ApplyFiles(profile string, adds []mods.FileAdd, removes []mods.FileRemove, marker string) (string, error)
	CheckUpdates(ctx context.Context, profile, mcVersion string) ([]mods.Entry, error)
}

// ConfigChoice is one config file in the preview.
type ConfigChoice struct {
	Path string `json:"path"`
	On   bool   `json:"on"`
	New  bool   `json:"new"`
}

// Preview describes what applying a pack version would do.
type Preview struct {
	ProjectID     string         `json:"projectId"`
	Slug          string         `json:"slug"`
	Title         string         `json:"title"`
	VersionID     string         `json:"versionId"`
	VersionNumber string         `json:"versionNumber"`
	MC            string         `json:"mc"`
	Total         int            `json:"total"`
	Plan          Plan           `json:"plan"`
	Configs       []ConfigChoice `json:"configs"`
	Ignored       []string       `json:"ignored"`
}

type Service struct {
	api    API
	files  Files
	root   string
	get    func(string) string
	set    func(string, string)
	notify func([]mods.Change)
	log    *slog.Logger
	mu     sync.Mutex // serialisiert alle Änderungen
}

func New(api API, files Files, root string, get func(string) string, set func(string, string), notify func([]mods.Change), log *slog.Logger) *Service {
	return &Service{api: api, files: files, root: root, get: get, set: set, notify: notify, log: log}
}

// Root is the client pack root dir (für Verteilung der Configs).
func (s *Service) Root() string { return s.root }

// State returns the persisted state; ok=false = kein Basis-Pack gesetzt.
func (s *Service) State() (State, bool) {
	var st State
	raw := s.get(stateKey)
	if raw == "" || json.Unmarshal([]byte(raw), &st) != nil || st.ProjectID == "" {
		return State{}, false
	}
	return st, true
}

func (s *Service) save(st *State) {
	if st == nil {
		s.set(stateKey, "")
		return
	}
	b, _ := json.Marshal(st)
	s.set(stateKey, string(b))
}

func (s *Service) Search(ctx context.Context, query, mc string) ([]modrinth.SearchHit, error) {
	return s.api.SearchModpacks(ctx, query, loader, mc, 15)
}

// loaded is a downloaded + resolved pack version.
type loaded struct {
	project modrinth.Project
	version modrinth.Version
	files   []PackFile
	configs []string
	ignored []string
	over    map[string][]byte
}

func (s *Service) versionFor(ctx context.Context, projectID, versionID, mc string) (modrinth.Version, error) {
	vs, err := s.api.ProjectVersions(ctx, projectID, loader, mc)
	if err != nil {
		return modrinth.Version{}, err
	}
	if len(vs) == 0 {
		return modrinth.Version{}, fmt.Errorf("dieses Modpack hat keine Version für Minecraft %s", mc)
	}
	if versionID == "" {
		return vs[0], nil
	}
	for _, v := range vs {
		if v.ID == versionID {
			return v, nil
		}
	}
	return modrinth.Version{}, fmt.Errorf("Pack-Version %s passt nicht zu Minecraft %s", versionID, mc)
}

func (s *Service) load(ctx context.Context, projectID, versionID, mc string) (*loaded, error) {
	proj, err := s.api.Project(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if proj.ProjectType != "modpack" {
		return nil, fmt.Errorf("„%s“ ist kein Modpack", proj.Title)
	}
	v, err := s.versionFor(ctx, projectID, versionID, mc)
	if err != nil {
		return nil, err
	}
	file, ok := v.PrimaryFile()
	if !ok || !strings.HasSuffix(file.Filename, ".mrpack") {
		return nil, fmt.Errorf("Pack-Version %s enthält keine .mrpack-Datei", v.VersionNumber)
	}
	if file.Size > maxPack {
		return nil, fmt.Errorf("Modpack-Datei zu groß (%d MB, höchstens 64 MB)", file.Size>>20)
	}
	data, err := s.api.Download(ctx, file.URL)
	if err != nil {
		return nil, fmt.Errorf("modpack laden: %w", err)
	}
	if len(data) > maxPack {
		return nil, fmt.Errorf("Modpack-Datei zu groß (höchstens 64 MB)")
	}
	idx, err := mrpack.Parse(data)
	if err != nil {
		return nil, err
	}
	files, configs, ignored := FromIndex(idx)
	hashes := make([]string, 0, len(files))
	for _, f := range files {
		hashes = append(hashes, f.SHA512)
	}
	if known, err := s.api.VersionsByHash(ctx, hashes); err == nil {
		var ids []string
		for i := range files {
			if kv, ok := known[files[i].SHA512]; ok {
				files[i].ProjectID = kv.ProjectID
				files[i].Version = kv.VersionNumber
				ids = append(ids, kv.ProjectID)
			}
		}
		if ps, err := s.api.Projects(ctx, ids); err == nil {
			titles := map[string]string{}
			for _, p := range ps {
				titles[p.ID] = p.Title
			}
			for i := range files {
				files[i].Name = titles[files[i].ProjectID]
			}
		}
	}
	for i := range files {
		if files[i].Name == "" {
			files[i].Name = files[i].Filename
		}
	}
	return &loaded{project: proj, version: v, files: files, configs: configs, ignored: ignored, over: idx.Overrides}, nil
}

// own lists the admin's own files (ohne Pack-Dateien).
func (s *Service) own(ctx context.Context, mc string, prev State) []OwnFile {
	entries, err := s.files.CheckUpdates(ctx, mods.ProfileClient, mc)
	if err != nil {
		return nil
	}
	pack := map[string]bool{}
	for _, f := range prev.Files {
		pack[f.Filename] = true
	}
	var out []OwnFile
	for _, e := range entries {
		if pack[e.Filename] || e.ProjectID == "" {
			continue
		}
		out = append(out, OwnFile{Category: e.Category, Filename: e.Filename, Key: e.ProjectID, Name: e.Name})
	}
	return out
}

// filterAbsorb drops own files whose pack mod the admin switched off: the own
// copy is then the deliberate replacement and stays.
func filterAbsorb(own []OwnFile, base State) []OwnFile {
	var out []OwnFile
	for _, o := range own {
		if !base.IsDisabled(o.Key) {
			out = append(out, o)
		}
	}
	return out
}

// droppedNames lists the names of absorbed keys that are not kept any more.
func droppedNames(prev State, keep map[string]bool) []string {
	var out []string
	for _, k := range prev.Absorbed {
		if keep[k] {
			continue
		}
		name := k
		for _, f := range prev.Files {
			if Key(f) == k {
				name = f.Name
				if name == "" {
					name = f.Filename
				}
				break
			}
		}
		out = append(out, name)
	}
	return out
}

const dropHint = " entfällt — war vorher auch deine eigene Mod, bei Bedarf neu hinzufügen"

func sameConfigs(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

func choices(prev State, configs []string, plan Plan) []ConfigChoice {
	isNew := map[string]bool{}
	for _, c := range plan.NewConfigs {
		isNew[c] = true
	}
	out := make([]ConfigChoice, 0, len(configs))
	for _, c := range configs {
		on, known := prev.Configs[c]
		switch {
		case isNew[c]:
			on = false
		case !known:
			on = DefaultOn(c)
		}
		out = append(out, ConfigChoice{Path: c, On: on, New: isNew[c]})
	}
	return out
}

// Preview loads the newest pack version for mc and describes the change.
func (s *Service) Preview(ctx context.Context, projectID, mc string) (*Preview, error) {
	prev, _ := s.State()
	l, err := s.load(ctx, projectID, "", mc)
	if err != nil {
		return nil, err
	}
	own := s.own(ctx, mc, prev) // mit dem alten Stand: alte Pack-Dateien sind keine eigenen
	if prev.ProjectID != "" && prev.ProjectID != l.project.ID {
		prev = State{} // anderes Pack: alles neu, die alten Pack-Dateien gehen raus (Apply)
	}
	plan := Diff(prev, l.files, l.configs, own)
	plan.AbsorbOwn = filterAbsorb(plan.AbsorbOwn, prev)
	return &Preview{ProjectID: l.project.ID, Slug: l.project.Slug, Title: l.project.Title,
		VersionID: l.version.ID, VersionNumber: l.version.VersionNumber, MC: mc, Total: len(l.files),
		Plan: plan, Configs: choices(prev, l.configs, plan), Ignored: l.ignored}, nil
}

func (s *Service) download(ctx context.Context, f PackFile) ([]byte, error) {
	b, err := s.api.Download(ctx, f.URL)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", f.Filename, err)
	}
	sum := sha512.Sum512(b)
	if hex.EncodeToString(sum[:]) != f.SHA512 {
		return nil, fmt.Errorf("hash-verifikation fehlgeschlagen für %s — abgebrochen", f.Filename)
	}
	return b, nil
}

func addOf(f PackFile, data []byte, replaces string) mods.FileAdd {
	return mods.FileAdd{Category: f.Category, Filename: f.Filename, SHA512: f.SHA512, ProjectID: f.ProjectID,
		Name: f.Name, Version: f.Version, Replaces: replaces, Data: data}
}

// Apply switches to the given pack version (oder übernimmt es erstmals).
// configs: Auswahl je Pfad (nil = bisherige Auswahl + Vorauswahl).
func (s *Service) Apply(ctx context.Context, projectID, versionID, mc string, configs map[string]bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyLocked(ctx, projectID, versionID, mc, configs)
}

func (s *Service) applyLocked(ctx context.Context, projectID, versionID, mc string, configs map[string]bool) (string, error) {
	prev, _ := s.State()
	l, err := s.load(ctx, projectID, versionID, mc)
	if err != nil {
		return "", err
	}
	switching := prev.ProjectID != "" && prev.ProjectID != l.project.ID
	base := prev
	if switching {
		base = State{}
	}
	plan := Diff(base, l.files, l.configs, s.own(ctx, mc, prev))
	plan.AbsorbOwn = filterAbsorb(plan.AbsorbOwn, base)

	var adds []mods.FileAdd
	var removes []mods.FileRemove
	removed := map[string]bool{}
	removeFile := func(cat, name string) {
		k := cat + "/" + name
		if !removed[k] {
			removed[k] = true
			removes = append(removes, mods.FileRemove{Category: cat, Filename: name})
		}
	}
	addIdx := map[string]int{} // Key → Index in adds (nur neu hinzugefügte Pack-Dateien)
	for _, f := range plan.Add {
		if base.IsDisabled(Key(f)) {
			continue
		}
		data, err := s.download(ctx, f)
		if err != nil {
			return "", err
		}
		addIdx[Key(f)] = len(adds)
		adds = append(adds, addOf(f, data, ""))
	}
	for _, u := range plan.Update {
		if base.IsDisabled(Key(u.New)) {
			continue // abgeschaltet: nichts live, Abschaltung bleibt
		}
		data, err := s.download(ctx, u.New)
		if err != nil {
			return "", err
		}
		if u.Old.Category == u.New.Category {
			adds = append(adds, addOf(u.New, data, u.Old.Filename))
		} else {
			removeFile(u.Old.Category, u.Old.Filename)
			adds = append(adds, addOf(u.New, data, ""))
		}
	}
	removeList := plan.Remove
	if switching {
		removeList = prev.Files // anderes Pack: alle alten Pack-Dateien raus
	}
	for _, f := range removeList {
		if !prev.IsDisabled(Key(f)) {
			removeFile(f.Category, f.Filename)
		}
	}
	// Eigene Dateien, deren Projekt das Pack liefert: Pack übernimmt.
	for _, o := range plan.AbsorbOwn {
		if i, ok := addIdx[o.Key]; ok && adds[i].Category == o.Category && adds[i].Replaces == "" {
			adds[i].Replaces = o.Filename // ersetzt die eigene Kopie (auch bei gleichem Dateinamen)
			continue
		}
		removeFile(o.Category, o.Filename) // Update/unverändert/andere Kategorie
	}
	// Entfernen + Hinzufügen derselben Datei: stattdessen ersetzen.
	var finalRemoves []mods.FileRemove
	for _, r := range removes {
		keep := true
		for i := range adds {
			if adds[i].Category == r.Category && adds[i].Filename == r.Filename && (adds[i].Replaces == "" || adds[i].Replaces == r.Filename) {
				adds[i].Replaces = r.Filename
				keep = false
				break
			}
		}
		if keep {
			finalRemoves = append(finalRemoves, r)
		}
	}
	removes = finalRemoves

	summary := Summary(prev, l.project.Title, l.version.VersionNumber, plan, len(l.files))
	if switching {
		summary = fmt.Sprintf("Basis-Modpack gewechselt: %s → %s %s", prev.Title, l.project.Title, l.version.VersionNumber)
	}
	// Label "" = nichts zu tun (z. B. alles identisch) — kein Fehler. Bei Fehler
	// bleibt der Stand auf der bisherigen Version.
	if len(adds) > 0 || len(removes) > 0 {
		if label, err := s.files.ApplyFiles(mods.ProfileClient, adds, removes, l.project.Title+" "+l.version.VersionNumber); err != nil {
			if label != "" {
				s.log.Warn("basis-modpack teilweise angewendet — RollbackLabel kann die Änderung rückgängig machen", "label", label, "err", err)
			}
			return "", err
		}
	}

	next := State{ProjectID: l.project.ID, Slug: l.project.Slug, Title: l.project.Title,
		VersionID: l.version.ID, VersionNumber: l.version.VersionNumber, MC: mc, Files: l.files,
		Configs: map[string]bool{}}
	keys := map[string]bool{}
	for _, f := range l.files {
		keys[Key(f)] = true
	}
	for _, d := range base.Disabled {
		if keys[d] {
			next.Disabled = append(next.Disabled, d)
		}
	}
	for _, a := range base.Absorbed {
		if keys[a] && !contains(next.Absorbed, a) {
			next.Absorbed = append(next.Absorbed, a)
		}
	}
	for _, o := range plan.AbsorbOwn {
		if !contains(next.Absorbed, o.Key) {
			next.Absorbed = append(next.Absorbed, o.Key)
		}
	}
	for _, c := range choices(base, l.configs, plan) {
		on := c.On
		if v, ok := configs[c.Path]; ok {
			on = v
		}
		next.Configs[c.Path] = on
	}
	next.WrittenConfigs = s.writeConfigs(prev.WrittenConfigs, next.Configs, l.over)
	s.save(&next)

	lines := []mods.Change{{Action: "pack", Profile: mods.ProfileClient, Name: summary}}
	drops := plan.DropAbsorbed
	if switching {
		drops = droppedNames(prev, keys)
	}
	for _, name := range drops {
		lines = append(lines, mods.Change{Action: "pack", Profile: mods.ProfileClient, Name: name + dropHint})
	}
	noop := !switching && prev.VersionID == l.version.ID && prev.MC == mc &&
		len(plan.Add) == 0 && len(plan.Update) == 0 && len(plan.Remove) == 0 &&
		len(plan.AbsorbOwn) == 0 && len(plan.DropAbsorbed) == 0 && sameConfigs(prev.Configs, next.Configs)
	if s.notify != nil && !noop {
		s.notify(lines)
	}
	s.log.Info("basis-modpack übernommen", "pack", l.project.Title, "version", l.version.VersionNumber, "mc", mc)
	return summary, nil
}

// writeConfigs writes the selected configs and removes previously written
// ones that are no longer selected or no longer in the pack.
func (s *Service) writeConfigs(old []string, sel map[string]bool, over map[string][]byte) []string {
	var written []string
	keep := map[string]bool{}
	paths := make([]string, 0, len(sel))
	for p := range sel {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		data, ok := over[p]
		if !sel[p] || !ok || !mrpack.ValidPath(p) {
			continue
		}
		dst := filepath.Join(s.root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			s.log.Warn("config schreiben", "path", p, "err", err)
			continue
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			s.log.Warn("config schreiben", "path", p, "err", err)
			continue
		}
		keep[p] = true
		written = append(written, p)
	}
	for _, p := range old {
		if !keep[p] && mrpack.ValidPath(p) {
			os.Remove(filepath.Join(s.root, filepath.FromSlash(p)))
		}
	}
	return written
}

func (s *Service) find(st State, key string) (PackFile, bool) {
	for _, f := range st.Files {
		if Key(f) == key {
			return f, true
		}
	}
	return PackFile{}, false
}

// Toggle switches one pack file on or off.
func (s *Service) Toggle(ctx context.Context, key string, on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.State()
	if !ok {
		return fmt.Errorf("kein Basis-Modpack eingerichtet")
	}
	f, ok := s.find(st, key)
	if !ok {
		return fmt.Errorf("unbekannte Pack-Mod %q", key)
	}
	if st.IsDisabled(key) == !on {
		return nil
	}
	marker := st.Title + " " + st.VersionNumber
	change := mods.Change{Profile: mods.ProfileClient, Filename: f.Filename, ProjectID: f.ProjectID, Name: f.Name}
	if on {
		data, err := s.download(ctx, f)
		if err != nil {
			return err
		}
		add := addOf(f, data, "")
		var removes []mods.FileRemove
		absorbed := false
		for _, o := range s.own(ctx, st.MC, st) {
			if o.Key != key {
				continue
			}
			absorbed = true
			if o.Category == f.Category && o.Filename == f.Filename {
				add.Replaces = o.Filename
			} else {
				removes = append(removes, mods.FileRemove{Category: o.Category, Filename: o.Filename})
			}
		}
		if _, err := s.files.ApplyFiles(mods.ProfileClient, []mods.FileAdd{add}, removes, marker); err != nil {
			return err
		}
		if absorbed && !contains(st.Absorbed, key) {
			st.Absorbed = append(st.Absorbed, key)
		}
		var rest []string
		for _, d := range st.Disabled {
			if d != key {
				rest = append(rest, d)
			}
		}
		st.Disabled = rest
		change.Action = "add"
	} else {
		if _, err := s.files.ApplyFiles(mods.ProfileClient, nil, []mods.FileRemove{{Category: f.Category, Filename: f.Filename}}, marker); err != nil {
			return err
		}
		st.Disabled = append(st.Disabled, key)
		change.Action = "remove"
	}
	s.save(&st)
	if s.notify != nil {
		s.notify([]mods.Change{change})
	}
	return nil
}

// SetConfigs changes the config selection (lädt das Pack für die Inhalte neu).
func (s *Service) SetConfigs(ctx context.Context, configs map[string]bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.State()
	if !ok {
		return fmt.Errorf("kein Basis-Modpack eingerichtet")
	}
	l, err := s.load(ctx, st.ProjectID, st.VersionID, st.MC)
	if err != nil {
		return err
	}
	for p, on := range configs {
		if _, known := st.Configs[p]; known {
			st.Configs[p] = on
		}
	}
	st.WrittenConfigs = s.writeConfigs(st.WrittenConfigs, st.Configs, l.over)
	s.save(&st)
	if s.notify != nil {
		s.notify([]mods.Change{{Action: "pack", Profile: mods.ProfileClient, Name: "Einstellungen des Basis-Modpacks " + st.Title + " geändert"}})
	}
	return nil
}

// Remove takes the base pack out of the client pack (eigene Mods bleiben).
func (s *Service) Remove(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.State()
	if !ok {
		return nil
	}
	var removes []mods.FileRemove
	for _, f := range st.Files {
		if !st.IsDisabled(Key(f)) {
			removes = append(removes, mods.FileRemove{Category: f.Category, Filename: f.Filename})
		}
	}
	if _, err := s.files.ApplyFiles(mods.ProfileClient, nil, removes, st.Title+" entfernt"); err != nil {
		return err
	}
	s.writeConfigs(st.WrittenConfigs, nil, nil)
	s.save(nil)
	if s.notify != nil {
		lines := []mods.Change{{Action: "pack", Profile: mods.ProfileClient, Name: "Basis-Modpack " + st.Title + " entfernt"}}
		for _, name := range droppedNames(st, nil) {
			lines = append(lines, mods.Change{Action: "pack", Profile: mods.ProfileClient, Name: name + dropHint})
		}
		s.notify(lines)
	}
	return nil
}

// Available returns a newer pack version for mc (nil = aktuell / kein Pack).
func (s *Service) Available(ctx context.Context, mc string) (*modrinth.Version, error) {
	st, ok := s.State()
	if !ok {
		return nil, nil
	}
	vs, err := s.api.ProjectVersions(ctx, st.ProjectID, loader, mc)
	if err != nil {
		return nil, err
	}
	if len(vs) == 0 || vs[0].ID == st.VersionID {
		return nil, nil
	}
	return &vs[0], nil
}

// Ready reports whether the base pack has a version for mc.
// set=false: kein Basis-Pack eingerichtet.
func (s *Service) Ready(ctx context.Context, mc string) (string, bool, bool) {
	st, ok := s.State()
	if !ok {
		return "", true, false
	}
	vs, err := s.api.ProjectVersions(ctx, st.ProjectID, loader, mc)
	return st.Title, err == nil && len(vs) > 0, true
}

// SwitchMC moves the pack to its newest version for mc (MC-Update).
func (s *Service) SwitchMC(ctx context.Context, mc string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.State()
	if !ok || st.MC == mc {
		return nil
	}
	_, err := s.applyLocked(ctx, st.ProjectID, "", mc, st.Configs)
	return err
}

// Pinned reports whether filename is an active pack file.
func (s *Service) Pinned(filename string) bool {
	st, ok := s.State()
	if !ok {
		return false
	}
	for _, f := range st.Files {
		if f.Filename == filename && !st.IsDisabled(Key(f)) {
			return true
		}
	}
	return false
}

// IndexFiles maps sha512 → pack file (für das eigene Modrinth-Modpack).
func (s *Service) IndexFiles() map[string]PackFile {
	out := map[string]PackFile{}
	if st, ok := s.State(); ok {
		for _, f := range st.Files {
			out[f.SHA512] = f
		}
	}
	return out
}

// WrittenConfigs returns the config paths MSM wrote into the client pack.
func (s *Service) WrittenConfigs() []string {
	st, _ := s.State()
	return append([]string(nil), st.WrittenConfigs...)
}
