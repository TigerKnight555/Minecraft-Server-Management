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

// ModrinthAPI is the subset of the Modrinth client the manager needs
// (interface for tests/mock mode).
type ModrinthAPI interface {
	LatestVersions(ctx context.Context, sha512s []string, loader, mcVersion string) (map[string]modrinth.Version, error)
	VersionsByHash(ctx context.Context, sha512s []string) (map[string]modrinth.Version, error)
	ProjectSupports(ctx context.Context, projectID, loader, mcVersion string) (bool, error)
	Download(ctx context.Context, url string) ([]byte, error)
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
}

// Manager coordinates scans, update checks, staging and rollback.
type Manager struct {
	api      ModrinthAPI
	loader   string
	profiles []Profile

	mu      sync.Mutex
	entries map[string][]Entry // profile name -> last check result
}

func NewManager(api ModrinthAPI, loader string, profiles []Profile) *Manager {
	return &Manager{api: api, loader: loader, profiles: profiles, entries: map[string][]Entry{}}
}

func (m *Manager) Profiles() []Profile { return m.profiles }

func (m *Manager) profile(name string) (Profile, error) {
	for _, p := range m.profiles {
		if p.Name == name {
			return p, nil
		}
	}
	return Profile{}, fmt.Errorf("unbekanntes Profil %q", name)
}

// Entries returns the cached result of the last update check; the staged
// flag is re-read from the staging manifest so it stays current after
// Stage/Apply without a new Modrinth round trip.
func (m *Manager) Entries(profile string) []Entry {
	m.mu.Lock()
	out := append([]Entry(nil), m.entries[profile]...)
	m.mu.Unlock()
	if p, err := m.profile(profile); err == nil {
		staged := m.stagedFilenames(p)
		for i := range out {
			out[i].Staged = staged[out[i].Filename]
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

type stagingItem struct {
	Category    string `json:"category"`
	OldFilename string `json:"oldFilename"`
	NewFilename string `json:"newFilename"`
	SHA512      string `json:"sha512"`
	Version     string `json:"version"`
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

func (m *Manager) stagedFilenames(p Profile) map[string]bool {
	out := map[string]bool{}
	man, err := readManifest(filepath.Join(stagingDir(p), "manifest.json"))
	if err != nil {
		return out
	}
	for _, it := range man.Items {
		out[it.OldFilename] = true
	}
	return out
}

func readManifest(path string) (stagingManifest, error) {
	var man stagingManifest
	data, err := os.ReadFile(path)
	if err != nil {
		return man, err
	}
	return man, json.Unmarshal(data, &man)
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
	if err := os.MkdirAll(sdir, 0o755); err != nil {
		return 0, err
	}
	manPath := filepath.Join(sdir, "manifest.json")
	man, _ := readManifest(manPath)
	man.Created = time.Now()

	staged := 0
	for _, e := range entries {
		if e.UpdateURL == "" {
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
		// replace an existing staging entry for the same old file
		kept := man.Items[:0]
		for _, it := range man.Items {
			if it.OldFilename != e.Filename {
				kept = append(kept, it)
			}
		}
		man.Items = append(kept, stagingItem{
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

// ApplyStaged swaps staged files into the live directory: old files move to
// .backup/<timestamp>/ (never deleted), staged files move in. Returns the
// backup label for rollback. Call while the server is stopped or right
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
	for _, it := range man.Items {
		liveDir, ok := p.Dirs[it.Category]
		if !ok {
			continue
		}
		backupDir := filepath.Join(liveDir, ".backup", label)
		if err := os.MkdirAll(backupDir, 0o755); err != nil {
			return label, applied, err
		}
		oldPath := filepath.Join(liveDir, it.OldFilename)
		if _, err := os.Stat(oldPath); err == nil {
			if err := os.Rename(oldPath, filepath.Join(backupDir, it.OldFilename)); err != nil {
				return label, applied, fmt.Errorf("backup von %s: %w", it.OldFilename, err)
			}
		}
		if err := os.Rename(filepath.Join(sdir, it.NewFilename), filepath.Join(liveDir, it.NewFilename)); err != nil {
			return label, applied, fmt.Errorf("einsetzen von %s: %w", it.NewFilename, err)
		}
		backupMan.Items = append(backupMan.Items, it)
		applied++
		// rollback needs to know where the backup lives
		writeManifest(filepath.Join(backupDir, "manifest.json"), backupMan)
	}
	os.Remove(manPath)
	// den Cache NICHT komplett verwerfen — nur die angewendeten Einträge auf
	// den neuen Stand setzen. Die übrigen Update-Badges bleiben stehen, die
	// Oberfläche "vergisst" nichts (UX-Feedback vom ersten echten Update).
	m.mu.Lock()
	entries := m.entries[profileName]
	for _, it := range man.Items {
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
	m.mu.Unlock()
	return label, applied, nil
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
		newest := filepath.Join(backupRoot, labels[0].Name())
		man, err := readManifest(filepath.Join(newest, "manifest.json"))
		if err != nil {
			continue
		}
		for _, it := range man.Items {
			newPath := filepath.Join(liveDir, it.NewFilename)
			if _, err := os.Stat(newPath); err == nil {
				os.Rename(newPath, filepath.Join(newest, it.NewFilename+".rolledback"))
			}
			oldBackup := filepath.Join(newest, it.OldFilename)
			if _, err := os.Stat(oldBackup); err == nil {
				if err := os.Rename(oldBackup, filepath.Join(liveDir, it.OldFilename)); err != nil {
					return restored, fmt.Errorf("rollback von %s: %w", it.OldFilename, err)
				}
				restored++
			}
		}
	}
	if restored == 0 {
		return 0, fmt.Errorf("kein Backup zum Zurückrollen gefunden")
	}
	m.invalidate(profileName)
	return restored, nil
}

func (m *Manager) invalidate(profileName string) {
	m.mu.Lock()
	delete(m.entries, profileName)
	m.mu.Unlock()
}
