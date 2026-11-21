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
//
// Namensgleichheit: Zwei Adds mit gleichem Dateinamen (oder ein Add, dessen
// Name ein anderer Add ersetzt) werden abgelehnt. Existiert die Datei eines
// Adds ohne Replaces schon im Live-Ordner (und wird nicht im selben Lauf
// entfernt), wird sie bei gleichem SHA512 übersprungen, sonst als Replaces
// in die Sicherung gelegt. Liegt die Datei eines Adds MIT Replaces schon
// identisch live (abgebrochener Lauf), wird nur die noch vorhandene alte
// Datei entfernt. Bleibt danach nichts zu tun, ist das Label "".
//
// Bricht das Anwenden mittendrin ab, bleiben bereits angewendete Items
// angewendet und gesichert; der Satz wird trotzdem markiert, das übrige
// Staging aufgeräumt (Wiederholung möglich) und der Fehler zurückgegeben.
func (m *Manager) ApplyFiles(profile string, adds []FileAdd, removes []FileRemove, marker string) (string, error) {
	p, err := m.profile(profile)
	if err != nil {
		return "", err
	}
	seen := map[string]bool{}
	replaced := map[string]bool{}
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
		// Staging ist flach: gleicher Name in zwei Adds überschriebe sich dort.
		if seen[a.Filename] {
			return "", fmt.Errorf("doppelter Dateiname %q in der Änderungsliste", a.Filename)
		}
		seen[a.Filename] = true
		if a.Replaces != "" {
			replaced[a.Category+"/"+a.Replaces] = true
		}
	}
	removed := map[string]bool{}
	for _, r := range removes {
		if !validName(r.Filename) {
			return "", fmt.Errorf("ungültiger Dateiname %q", r.Filename)
		}
		if _, ok := p.Dirs[r.Category]; !ok {
			return "", fmt.Errorf("unbekannte Kategorie %q", r.Category)
		}
		removed[r.Category+"/"+r.Filename] = true
	}
	for _, a := range adds {
		if replaced[a.Category+"/"+a.Filename] && a.Replaces != a.Filename {
			return "", fmt.Errorf("dateiname %q wird von einem anderen Eintrag ersetzt — Kollision", a.Filename)
		}
	}
	m.stageMu.Lock()
	defer m.stageMu.Unlock()
	sdir := stagingDir(p)
	manPath := filepath.Join(sdir, "manifest.json")
	pending, err := loadManifest(manPath)
	if err != nil {
		return "", err
	}
	if len(pending.Items) > 0 {
		return "", fmt.Errorf("im Profil %s sind noch Änderungen vorgemerkt — erst anwenden oder verwerfen", profile)
	}
	// Gleichnamige Dateien im Live-Ordner: identisch = schon da, sonst sichern.
	// Identisch trotz Replaces: Rest eines abgebrochenen Laufs — die neue
	// Datei bleibt, eine noch vorhandene alte wird entfernt (Wiederholung).
	removes = append([]FileRemove(nil), removes...)
	var todo []FileAdd
	for _, a := range adds {
		key := a.Category + "/" + a.Filename
		livePath := filepath.Join(p.Dirs[a.Category], a.Filename)
		if a.Replaces != a.Filename && !removed[key] && fileExists(livePath) {
			if cur, rerr := os.ReadFile(livePath); rerr == nil {
				sum := sha512.Sum512(cur)
				if hex.EncodeToString(sum[:]) == a.SHA512 {
					oldKey := a.Category + "/" + a.Replaces
					if a.Replaces != "" && !removed[oldKey] && fileExists(filepath.Join(p.Dirs[a.Category], a.Replaces)) {
						removed[oldKey] = true
						removes = append(removes, FileRemove{Category: a.Category, Filename: a.Replaces})
					}
					continue // schon vorhanden
				}
			}
			if a.Replaces != "" {
				return "", fmt.Errorf("datei %q existiert bereits im Ordner %s und würde überschrieben", a.Filename, a.Category)
			}
			a.Replaces = a.Filename
		}
		todo = append(todo, a)
	}
	if len(todo) == 0 && len(removes) == 0 {
		return "", nil
	}
	if err := os.MkdirAll(sdir, 0o755); err != nil {
		return "", err
	}
	man := stagingManifest{Created: time.Now()}
	for _, r := range removes {
		man.Items = append(man.Items, stagingItem{Category: r.Category, OldFilename: r.Filename})
	}
	for _, a := range todo {
		if err := os.WriteFile(filepath.Join(sdir, a.Filename), a.Data, 0o644); err != nil {
			cleanupStaging(sdir, man, a.Filename)
			return "", err
		}
		man.Items = append(man.Items, stagingItem{Category: a.Category, OldFilename: a.Replaces, NewFilename: a.Filename,
			SHA512: a.SHA512, Version: a.Version, ProjectID: a.ProjectID, Name: a.Name})
	}
	if err := writeManifest(manPath, man); err != nil {
		cleanupStaging(sdir, man)
		return "", err
	}
	label, _, _, applyErr := m.applyStagedLocked(profile)
	if applyErr != nil {
		// Reste des Stagings aufräumen, damit ein erneuter Aufruf möglich ist.
		cleanupStaging(sdir, man)
	}
	var markErr error
	if marker != "" && label != "" {
		for _, liveDir := range p.Dirs {
			set := filepath.Join(liveDir, ".backup", label)
			if _, err := os.Stat(set); err == nil {
				if werr := os.WriteFile(filepath.Join(set, basepackFile), []byte(marker+"\n"), 0o644); werr != nil && markErr == nil {
					markErr = fmt.Errorf("basis-pack-markierung in %s: %w", set, werr)
				}
			}
		}
	}
	if applyErr != nil {
		applyErr = fmt.Errorf("%w (teilweise angewendet — Rollback über RollbackLabel möglich)", applyErr)
		if markErr != nil {
			applyErr = fmt.Errorf("%w; %v", applyErr, markErr)
		}
		return label, applyErr
	}
	return label, markErr
}

// cleanupStaging removes the manifest and every staged file of man (plus
// extra names); files already moved live are simply not there any more.
func cleanupStaging(sdir string, man stagingManifest, extra ...string) {
	for _, it := range man.Items {
		if it.NewFilename != "" {
			os.Remove(filepath.Join(sdir, it.NewFilename))
		}
	}
	for _, n := range extra {
		os.Remove(filepath.Join(sdir, n))
	}
	os.Remove(filepath.Join(sdir, "manifest.json"))
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
