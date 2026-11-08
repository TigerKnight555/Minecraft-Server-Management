// Package mods implements the mod management from the concept: two profiles
// (server = live mods volume, client = client-pack superset), Modrinth
// update checks via SHA-512, staged downloads and manifest-based rollback.
//
// Safety invariants (Datenintegrität):
//   - the live directory is only touched during an explicit apply
//   - every replaced file is moved to .backup/<timestamp>/, never deleted
//   - staged files are hash-verified before they can be applied
//   - files unknown to Modrinth are "unmanaged" and never modified
package mods

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/modrinth"
)

// ErrNothingStaged signals an apply without staged files — callers like the
// scheduler treat this as "nothing to do", not as a failure.
var ErrNothingStaged = errors.New("nichts gestaged")

// Profilnamen — Server = Live-Mods des Minecraft-Servers, Client = Paket
// für die Spieler.
const (
	ProfileServer = "server"
	ProfileClient = "client"
)

// ModrinthAPI is the subset of the Modrinth client the manager needs
// (interface for tests/mock mode).
type ModrinthAPI interface {
	LatestVersions(ctx context.Context, sha512s []string, loader, mcVersion string) (map[string]modrinth.Version, error)
	VersionsByHash(ctx context.Context, sha512s []string) (map[string]modrinth.Version, error)
	ProjectSupports(ctx context.Context, projectID, loader, mcVersion string) (bool, error)
	Download(ctx context.Context, url string) ([]byte, error)
	Search(ctx context.Context, query, loader, mcVersion string, limit int) ([]modrinth.SearchHit, error)
	Project(ctx context.Context, id string) (modrinth.Project, error)
	ProjectVersions(ctx context.Context, projectID, loader, mcVersion string) ([]modrinth.Version, error)
}

// Profile is one managed directory tree.
type Profile struct {
	Name string            `json:"name"` // "server" | "client"
	Dirs map[string]string `json:"-"`    // category ("mods", "shaderpacks", ...) -> absolute dir
}

// Entry is one file of a profile with its update state.
type Entry struct {
	Profile        string `json:"profile"`
	Category       string `json:"category"`
	Filename       string `json:"filename"`
	SHA512         string `json:"sha512"`
	Size           int64  `json:"size"`
	Managed        bool   `json:"managed"`
	ProjectID      string `json:"projectId,omitempty"`
	Name           string `json:"name,omitempty"`
	Version        string `json:"version,omitempty"`
	UpdateVersion  string `json:"updateVersion,omitempty"`
	UpdateFilename string `json:"updateFilename,omitempty"`
	UpdateURL      string `json:"-"`
	UpdateSHA512   string `json:"-"`
	Changelog      string `json:"changelog,omitempty"`
	Staged         bool   `json:"staged"`
	// Pending: was vorgemerkt ist — "add" (neu), "remove" (wird entfernt),
	// "update" (gestagtes Update); leer = nichts vorgemerkt
	Pending string `json:"pending,omitempty"`
}

// Manager coordinates scans, update checks, staging and rollback.
type Manager struct {
	api      ModrinthAPI
	loader   string
	profiles []Profile

	mu      sync.Mutex
	entries map[string][]Entry // profile name -> last check result
	// onChange meldet angewendete Änderungen (Discord-Neuigkeiten, Paket
	// veraltet); nil = aus
	onChange func(profile string, changes []Change)
}

func NewManager(api ModrinthAPI, loader string, profiles []Profile) *Manager {
	return &Manager{api: api, loader: loader, profiles: profiles, entries: map[string][]Entry{}}
}

func (m *Manager) Profiles() []Profile { return m.profiles }

// Change describes one applied staging item — Grundlage für die
// Discord-Meldung nach dem Neustart.
type Change struct {
	Action     string `json:"action"` // "add" | "remove" | "update"
	Profile    string `json:"profile"`
	Filename   string `json:"filename"`
	ProjectID  string `json:"projectId,omitempty"`
	Name       string `json:"name"`
	ClientSide string `json:"clientSide,omitempty"`
}

// SetChangeHook registers f; called after every successful ApplyStaged
// (alle angewendeten Items) and every successful rollback (changes = nil).
func (m *Manager) SetChangeHook(f func(profile string, changes []Change)) { m.onChange = f }

func (m *Manager) notify(profile string, changes []Change) {
	if m.onChange != nil {
		m.onChange(profile, changes)
	}
}

func (m *Manager) profile(name string) (Profile, error) {
	for _, p := range m.profiles {
		if p.Name == name {
			return p, nil
		}
	}
	return Profile{}, fmt.Errorf("unbekanntes Profil %q", name)
}

// Entries returns the cached result of the last update check plus pending
// staging state: Vormerkungen werden bei jedem Aufruf aus dem Manifest
// gelesen (kein Modrinth-Aufruf nötig), vorgemerkte Neuzugänge erscheinen
// als eigene Zeilen.
func (m *Manager) Entries(profile string) []Entry {
	m.mu.Lock()
	out := append([]Entry(nil), m.entries[profile]...)
	m.mu.Unlock()
	p, err := m.profile(profile)
	if err != nil {
		return out
	}
	items := m.stagedItems(p)
	byOld := map[string]stagingItem{}
	for _, it := range items {
		if it.OldFilename != "" {
			byOld[it.OldFilename] = it
		}
	}
	for i := range out {
		it, ok := byOld[out[i].Filename]
		out[i].Staged = ok
		out[i].Pending = ""
		if ok {
			out[i].Pending = it.action()
		}
	}
	for _, it := range items {
		if it.action() == "add" {
			out = append(out, Entry{
				Profile: profile, Category: it.Category, Filename: it.NewFilename,
				SHA512: it.SHA512, Managed: it.ProjectID != "", ProjectID: it.ProjectID,
				Name: it.Name, Version: it.Version, Staged: true, Pending: "add",
			})
		}
	}
	return out
}

// scanFiles hashes every regular file in the profile's category dirs.
func scanFiles(p Profile) ([]Entry, error) {
	var out []Entry
	for category, dir := range p.Dirs {
		items, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("scan %s: %w", dir, err)
		}
		for _, it := range items {
			if it.IsDir() || strings.HasPrefix(it.Name(), ".") {
				continue
			}
			path := filepath.Join(dir, it.Name())
			sum, size, err := hashFile(path)
			if err != nil {
				return nil, err
			}
			out = append(out, Entry{
				Profile: p.Name, Category: category,
				Filename: it.Name(), SHA512: sum, Size: size,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Filename < out[j].Filename })
	return out, nil
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha512.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// CheckUpdates scans a profile, resolves identities and available updates
// via Modrinth and caches the result.
func (m *Manager) CheckUpdates(ctx context.Context, profileName, mcVersion string) ([]Entry, error) {
	p, err := m.profile(profileName)
	if err != nil {
		return nil, err
	}
	entries, err := scanFiles(p)
	if err != nil {
		return nil, err
	}
	hashes := make([]string, len(entries))
	for i, e := range entries {
		hashes[i] = e.SHA512
	}
	current, err := m.api.VersionsByHash(ctx, hashes)
	if err != nil {
		return nil, fmt.Errorf("modrinth identify: %w", err)
	}
	latest, err := m.api.LatestVersions(ctx, hashes, m.loader, mcVersion)
	if err != nil {
		return nil, fmt.Errorf("modrinth update check: %w", err)
	}
	staged := m.stagedFilenames(p)
	for i := range entries {
		e := &entries[i]
		if cur, ok := current[e.SHA512]; ok {
			e.Managed = true
			e.ProjectID = cur.ProjectID
			e.Name = cur.Name
			e.Version = cur.VersionNumber
		}
		if up, ok := latest[e.SHA512]; ok && e.Managed {
			if f, ok := up.PrimaryFile(); ok && f.Hashes.SHA512 != e.SHA512 {
				e.UpdateVersion = up.VersionNumber
				e.UpdateFilename = f.Filename
				e.UpdateURL = f.URL
				e.UpdateSHA512 = f.Hashes.SHA512
				e.Changelog = up.Changelog
			}
		}
		e.Staged = staged[e.Filename]
	}
	m.mu.Lock()
	m.entries[profileName] = entries
	m.mu.Unlock()
	return append([]Entry(nil), entries...), nil
}

// --- Staging ---

// stagingItem: OldFilename leer = Hinzufügen, NewFilename leer = Entfernen,
// beide gesetzt = Update.
type stagingItem struct {
	Category    string `json:"category"`
	OldFilename string `json:"oldFilename"`
	NewFilename string `json:"newFilename"`
	SHA512      string `json:"sha512"`
	Version     string `json:"version"`
	ProjectID   string `json:"projectId,omitempty"`
	Name        string `json:"name,omitempty"`
	ClientSide  string `json:"clientSide,omitempty"`
}

func (it stagingItem) action() string {
	switch {
	case it.OldFilename == "":
		return "add"
	case it.NewFilename == "":
		return "remove"
	}
	return "update"
}

func (it stagingItem) change(profile string) Change {
	file := it.NewFilename
	if it.action() == "remove" {
		file = it.OldFilename
	}
	name := it.Name
	if name == "" {
		name = file
	}
	return Change{Action: it.action(), Profile: profile, Filename: file,
		ProjectID: it.ProjectID, Name: name, ClientSide: it.ClientSide}
}

type stagingManifest struct {
	Created time.Time     `json:"created"`
	Items   []stagingItem `json:"items"`
}

// stagingDir lives INSIDE a mounted category dir (the container root-fs is
// read-only, only the mounts are writable). Deterministic choice: "mods" if
// present, otherwise the lexicographically first category. The scanner
// ignores dot-prefixed entries, so staging never shows up as profile content.
func stagingDir(p Profile) string {
	if dir, ok := p.Dirs["mods"]; ok {
		return filepath.Join(dir, ".msm-staging")
	}
	keys := make([]string, 0, len(p.Dirs))
	for k := range p.Dirs {
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	return filepath.Join(p.Dirs[keys[0]], ".msm-staging")
}

func (m *Manager) stagedItems(p Profile) []stagingItem {
	man, err := readManifest(filepath.Join(stagingDir(p), "manifest.json"))
	if err != nil {
		return nil
	}
	return man.Items
}

func (m *Manager) stagedFilenames(p Profile) map[string]bool {
	out := map[string]bool{}
	for _, it := range m.stagedItems(p) {
		if it.OldFilename != "" {
			out[it.OldFilename] = true
		}
	}
	return out
}

// HasStaged reports whether anything is pending for the profile.
func (m *Manager) HasStaged(profile string) bool {
	p, err := m.profile(profile)
	if err != nil {
		return false
	}
	return len(m.stagedItems(p)) > 0
}

// Discard drops the pending change for filename (vorgemerktes Hinzufügen,
// Entfernen oder Update). Der Live-Ordner bleibt unangetastet.
func (m *Manager) Discard(profile, filename string) error {
	p, err := m.profile(profile)
	if err != nil {
		return err
	}
	sdir := stagingDir(p)
	manPath := filepath.Join(sdir, "manifest.json")
	man, err := readManifest(manPath)
	if err != nil {
		return fmt.Errorf("für %s ist nichts vorgemerkt", filename)
	}
	kept := man.Items[:0]
	found := false
	for _, it := range man.Items {
		if filename != "" && (it.OldFilename == filename || (it.OldFilename == "" && it.NewFilename == filename)) {
			found = true
			if it.NewFilename != "" {
				os.Remove(filepath.Join(sdir, it.NewFilename))
			}
			continue
		}
		kept = append(kept, it)
	}
	if !found {
		return fmt.Errorf("für %s ist nichts vorgemerkt", filename)
	}
	if len(kept) == 0 {
		return os.Remove(manPath)
	}
	man.Items = kept
	return writeManifest(manPath, man)
}

func readManifest(path string) (stagingManifest, error) {
	var man stagingManifest
	data, err := os.ReadFile(path)
	if err != nil {
		return man, err
	}
	return man, json.Unmarshal(data, &man)
}

// loadManifest is readManifest, but a missing file is an empty manifest and
// a corrupt one is an error (never silently overwritten).
func loadManifest(path string) (stagingManifest, error) {
	man, err := readManifest(path)
	if err == nil {
		return man, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return stagingManifest{}, nil
	}
	return stagingManifest{}, fmt.Errorf("staging-liste %s unlesbar: %w", path, err)
}

func writeManifest(path string, man stagingManifest) error {
	data, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// Stage downloads the available updates for the given filenames (empty =
// all with updates) into the staging dir, verifying every hash. Nothing in
// the live directory changes.
func (m *Manager) Stage(ctx context.Context, profileName string, filenames []string) (int, error) {
	p, err := m.profile(profileName)
	if err != nil {
		return 0, err
	}
	m.mu.Lock()
	entries := append([]Entry(nil), m.entries[profileName]...)
	m.mu.Unlock()
	if len(entries) == 0 {
		return 0, fmt.Errorf("erst Update-Check ausführen")
	}
	want := map[string]bool{}
	for _, f := range filenames {
		want[f] = true
	}

	sdir := stagingDir(p)
	manPath := filepath.Join(sdir, "manifest.json")
	man, err := loadManifest(manPath)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(sdir, 0o755); err != nil {
		return 0, err
	}
	man.Created = time.Now()
	// Dateien mit einer Vormerkung (Entfernen, Update) bleiben unberührt —
	// sonst würde „Alle stagen" eine vorgemerkte Entfernung zum Update machen
	pending := map[string]bool{}
	for _, it := range man.Items {
		if it.OldFilename != "" {
			pending[it.OldFilename] = true
		}
	}

	staged := 0
	for _, e := range entries {
		if e.UpdateURL == "" || pending[e.Filename] {
			continue
		}
		if len(want) > 0 && !want[e.Filename] {
			continue
		}
		data, err := m.api.Download(ctx, e.UpdateURL)
		if err != nil {
			return staged, fmt.Errorf("download %s: %w", e.UpdateFilename, err)
		}
		sum := sha512.Sum512(data)
		if hex.EncodeToString(sum[:]) != e.UpdateSHA512 {
			return staged, fmt.Errorf("hash-verifikation fehlgeschlagen für %s — abgebrochen", e.UpdateFilename)
		}
		if err := os.WriteFile(filepath.Join(sdir, e.UpdateFilename), data, 0o644); err != nil {
			return staged, err
		}
		pending[e.Filename] = true
		man.Items = append(man.Items, stagingItem{
			Category: e.Category, OldFilename: e.Filename,
			NewFilename: e.UpdateFilename, SHA512: e.UpdateSHA512, Version: e.UpdateVersion,
		})
		staged++
	}
	if staged > 0 {
		if err := writeManifest(manPath, man); err != nil {
			return staged, err
		}
	}
	return staged, nil
}

// ApplyStaged swaps staged files into the live directory: old/removed files
// move to .backup/<timestamp>/ (never deleted), new files move in. Returns
// the backup label for rollback. Call while the server is stopped or right
// before a scheduled restart.
func (m *Manager) ApplyStaged(profileName string) (string, int, error) {
	p, err := m.profile(profileName)
	if err != nil {
		return "", 0, err
	}
	sdir := stagingDir(p)
	manPath := filepath.Join(sdir, "manifest.json")
	man, err := readManifest(manPath)
	if err != nil || len(man.Items) == 0 {
		return "", 0, fmt.Errorf("%w für Profil %s", ErrNothingStaged, profileName)
	}

	label := time.Now().Format("2006-01-02_15-04-05")
	applied := 0
	backupMan := stagingManifest{Created: time.Now()}
	var changes []Change
	for _, it := range man.Items {
		liveDir, ok := p.Dirs[it.Category]
		if !ok {
			continue
		}
		backupDir := filepath.Join(liveDir, ".backup", label)
		if err := os.MkdirAll(backupDir, 0o755); err != nil {
			return label, applied, err
		}
		// Leerer Name: filepath.Join(liveDir, "") wäre liveDir selbst —
		// jede Seite nur anfassen, wenn sie gesetzt ist.
		if it.OldFilename != "" {
			oldPath := filepath.Join(liveDir, it.OldFilename)
			if _, err := os.Stat(oldPath); err == nil {
				if err := os.Rename(oldPath, filepath.Join(backupDir, it.OldFilename)); err != nil {
					return label, applied, fmt.Errorf("backup von %s: %w", it.OldFilename, err)
				}
			}
		}
		if it.NewFilename != "" {
			if err := os.Rename(filepath.Join(sdir, it.NewFilename), filepath.Join(liveDir, it.NewFilename)); err != nil {
				return label, applied, fmt.Errorf("einsetzen von %s: %w", it.NewFilename, err)
			}
		}
		backupMan.Items = append(backupMan.Items, it)
		applied++
		// rollback needs to know where the backup lives
		writeManifest(filepath.Join(backupDir, "manifest.json"), backupMan)
		changes = append(changes, it.change(profileName))
	}
	os.Remove(manPath)
	m.updateCache(profileName, man.Items)
	m.notify(profileName, changes)
	return label, applied, nil
}

// updateCache pflegt den Check-Cache nach dem Anwenden, statt ihn zu
// verwerfen — übrige Update-Badges bleiben stehen (UX-Feedback vom ersten
// echten Update).
func (m *Manager) updateCache(profileName string, items []stagingItem) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entries, ok := m.entries[profileName]
	if !ok {
		return // noch kein Check gelaufen
	}
	for _, it := range items {
		switch it.action() {
		case "add":
			entries = append(entries, Entry{
				Profile: profileName, Category: it.Category, Filename: it.NewFilename,
				SHA512: it.SHA512, Managed: it.ProjectID != "", ProjectID: it.ProjectID,
				Name: it.Name, Version: it.Version,
			})
		case "remove":
			kept := entries[:0]
			for _, e := range entries {
				if !(e.Filename == it.OldFilename && e.Category == it.Category) {
					kept = append(kept, e)
				}
			}
			entries = kept
		default:
			for i := range entries {
				if entries[i].Filename == it.OldFilename && entries[i].Category == it.Category {
					entries[i].Filename = it.NewFilename
					entries[i].SHA512 = it.SHA512
					entries[i].Version = it.Version
					entries[i].UpdateVersion = ""
					entries[i].UpdateFilename = ""
					entries[i].UpdateURL = ""
					entries[i].UpdateSHA512 = ""
					entries[i].Changelog = ""
					entries[i].Staged = false
				}
			}
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Filename < entries[j].Filename })
	m.entries[profileName] = entries
}

// Rollback restores the newest backup set: new files move out of the live
// dir (into the backup dir, suffixed), old files move back.
func (m *Manager) Rollback(profileName string) (int, error) {
	p, err := m.profile(profileName)
	if err != nil {
		return 0, err
	}
	restored := 0
	for _, liveDir := range p.Dirs {
		backupRoot := filepath.Join(liveDir, ".backup")
		labels, err := os.ReadDir(backupRoot)
		if err != nil || len(labels) == 0 {
			continue
		}
		sort.Slice(labels, func(i, j int) bool { return labels[i].Name() > labels[j].Name() })
		n, err := restoreSet(liveDir, filepath.Join(backupRoot, labels[0].Name()))
		restored += n
		if err != nil {
			return restored, err
		}
	}
	if restored == 0 {
		return 0, fmt.Errorf("kein Backup zum Zurückrollen gefunden")
	}
	m.invalidate(profileName)
	m.notify(profileName, nil)
	return restored, nil
}

// RollbackLabel restores exactly the backup set with the given label (the
// one ApplyStaged returned). Anders als Rollback greift es nie auf einen
// älteren Satz zurück — wichtig für den automatischen Rückfall nach einem
// gescheiterten Versionssprung.
func (m *Manager) RollbackLabel(profileName, label string) (int, error) {
	p, err := m.profile(profileName)
	if err != nil {
		return 0, err
	}
	if label == "" || label != filepath.Base(label) || strings.HasPrefix(label, ".") {
		return 0, fmt.Errorf("ungültiges Backup-Label %q", label)
	}
	restored := 0
	for _, liveDir := range p.Dirs {
		set := filepath.Join(liveDir, ".backup", label)
		if _, err := os.Stat(set); err != nil {
			continue
		}
		n, err := restoreSet(liveDir, set)
		restored += n
		if err != nil {
			return restored, err
		}
	}
	if restored == 0 {
		return 0, fmt.Errorf("backup-satz %s nicht gefunden oder bereits zurückgerollt", label)
	}
	m.invalidate(profileName)
	m.notify(profileName, nil)
	return restored, nil
}

// restoreSet moves the new files out of liveDir (into the set, suffixed)
// and the backed-up old files back in. Leere Namen (Hinzufügen/Entfernen)
// werden übersprungen — filepath.Join(dir, "") wäre der Ordner selbst.
func restoreSet(liveDir, set string) (int, error) {
	man, err := readManifest(filepath.Join(set, "manifest.json"))
	if err != nil {
		return 0, nil
	}
	restored := 0
	for _, it := range man.Items {
		if it.NewFilename != "" {
			newPath := filepath.Join(liveDir, it.NewFilename)
			if _, err := os.Stat(newPath); err == nil {
				os.Rename(newPath, filepath.Join(set, it.NewFilename+".rolledback"))
				if it.OldFilename == "" {
					restored++ // Hinzufügen rückgängig gemacht
				}
			}
		}
		if it.OldFilename != "" {
			oldBackup := filepath.Join(set, it.OldFilename)
			if _, err := os.Stat(oldBackup); err == nil {
				if err := os.Rename(oldBackup, filepath.Join(liveDir, it.OldFilename)); err != nil {
					return restored, fmt.Errorf("rollback von %s: %w", it.OldFilename, err)
				}
				restored++
			}
		}
	}
	return restored, nil
}

func (m *Manager) invalidate(profileName string) {
	m.mu.Lock()
	delete(m.entries, profileName)
	m.mu.Unlock()
}
