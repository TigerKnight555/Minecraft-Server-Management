package mods

import (
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// basepackFile marks a backup set created for the base modpack; der normale
// Rollback stoppt dort (Basis-Pack-Stand und Dateien blieben sonst nicht
// deckungsgleich).
const basepackFile = ".basepack"

// FileAdd is one verified file to place into a profile.
type FileAdd struct {
	Category  string
	Filename  string
	SHA512    string
	ProjectID string
	Name      string
	Version   string
	// Replaces: Datei derselben Kategorie, die dabei ersetzt wird ("" = neu)
	Replaces string
	Data     []byte
}

// FileRemove is one file to take out of a profile (wandert in die Sicherung).
type FileRemove struct {
	Category string
	Filename string
}

// SetPinned registers a predicate for files that must not get individual
// updates (Basis-Pack-Dateien werden nur mit dem Pack aktualisiert).
func (m *Manager) SetPinned(f func(profile, filename string) bool) { m.pinnedFn = f }

func (m *Manager) pinned(profile, filename string) bool {
	return m.pinnedFn != nil && m.pinnedFn(profile, filename)
}

// ApplyFiles places and removes files as ONE backup set (über die
// Staging-Mechanik, Sicherung + RollbackLabel möglich) — ohne Change-Hook:
// der Aufrufer meldet selbst (Basis-Pack: eine Zeile statt vieler).
// marker != "" markiert den Satz als Basis-Pack-Satz.
func (m *Manager) ApplyFiles(profile string, adds []FileAdd, removes []FileRemove, marker string) (string, error) {
	p, err := m.profile(profile)
	if err != nil {
		return "", err
	}
	for _, a := range adds {
		if !validName(a.Filename) || (a.Replaces != "" && !validName(a.Replaces)) {
			return "", fmt.Errorf("ungültiger Dateiname %q", a.Filename)
		}
		if _, ok := p.Dirs[a.Category]; !ok {
			return "", fmt.Errorf("unbekannte Kategorie %q", a.Category)
		}
		sum := sha512.Sum512(a.Data)
		if hex.EncodeToString(sum[:]) != a.SHA512 {
			return "", fmt.Errorf("hash-verifikation fehlgeschlagen für %s — abgebrochen", a.Filename)
		}
	}
	for _, r := range removes {
		if !validName(r.Filename) {
			return "", fmt.Errorf("ungültiger Dateiname %q", r.Filename)
		}
	}
	m.stageMu.Lock()
	defer m.stageMu.Unlock()
	if len(m.stagedItems(p)) > 0 {
		return "", fmt.Errorf("im Profil %s sind noch Änderungen vorgemerkt — erst anwenden oder verwerfen", profile)
	}
	sdir := stagingDir(p)
	if err := os.MkdirAll(sdir, 0o755); err != nil {
		return "", err
	}
	man := stagingManifest{Created: time.Now()}
	for _, r := range removes {
		man.Items = append(man.Items, stagingItem{Category: r.Category, OldFilename: r.Filename})
	}
	for _, a := range adds {
		if err := os.WriteFile(filepath.Join(sdir, a.Filename), a.Data, 0o644); err != nil {
			return "", err
		}
		man.Items = append(man.Items, stagingItem{Category: a.Category, OldFilename: a.Replaces, NewFilename: a.Filename,
			SHA512: a.SHA512, Version: a.Version, ProjectID: a.ProjectID, Name: a.Name})
	}
	if len(man.Items) == 0 {
		return "", nil
	}
	if err := writeManifest(filepath.Join(sdir, "manifest.json"), man); err != nil {
		return "", err
	}
	label, _, _, err := m.applyStagedLocked(profile)
	if err != nil {
		return label, err
	}
	if marker != "" {
		for _, liveDir := range p.Dirs {
			set := filepath.Join(liveDir, ".backup", label)
			if _, err := os.Stat(set); err == nil {
				os.WriteFile(filepath.Join(set, basepackFile), []byte(marker+"\n"), 0o644)
			}
		}
	}
	return label, nil
}
