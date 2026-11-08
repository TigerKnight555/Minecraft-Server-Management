package mods

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// MaxUploadBytes limits uploaded files (Spec: 100 MB).
const MaxUploadBytes = 100 << 20

var uploadExt = map[string]string{"mods": ".jar", "shaderpacks": ".zip", "resourcepacks": ".zip"}

var errClientPending = errors.New("Im Client-Paket sind schon Änderungen vorgemerkt — erst anwenden oder verwerfen")

// ProfileFile names a file in a profile.
type ProfileFile struct {
	Profile  string `json:"profile"`
	Filename string `json:"filename"`
}

// RemovalInfo feeds the confirm dialog before removing a mod.
type RemovalInfo struct {
	Name       string        `json:"name"`
	Dependents []string      `json:"dependents"` // installierte Mods, die ihn brauchen
	AlsoIn     []ProfileFile `json:"alsoIn"`     // derselbe Mod in anderen Profilen
}

// findLive returns the category of a live file in the profile.
func findLive(p Profile, filename string) (string, error) {
	if !validName(filename) {
		return "", fmt.Errorf("ungültiger Dateiname %q", filename)
	}
	for cat, dir := range p.Dirs {
		if fi, err := os.Stat(filepath.Join(dir, filename)); err == nil && fi.Mode().IsRegular() {
			return cat, nil
		}
	}
	return "", fmt.Errorf("%s nicht im Profil %s gefunden", filename, p.Name)
}

// RemovalInfo collects warnings before removing filename — best effort:
// ohne Modrinth gibt es nur den Dateinamen-Abgleich.
func (m *Manager) RemovalInfo(ctx context.Context, profile, filename string) (*RemovalInfo, error) {
	p, err := m.profile(profile)
	if err != nil {
		return nil, err
	}
	cat, err := findLive(p, filename)
	if err != nil {
		return nil, err
	}
	info := &RemovalInfo{Name: filename, Dependents: []string{}, AlsoIn: []ProfileFile{}}
	sum, _, err := hashFile(filepath.Join(p.Dirs[cat], filename))
	if err != nil {
		return nil, err
	}
	target := ""
	if vs, err := m.api.VersionsByHash(ctx, []string{sum}); err == nil {
		if v, ok := vs[sum]; ok {
			target, info.Name = v.ProjectID, v.Name
		}
	}
	if target != "" {
		if inv, err := m.inventory(ctx); err == nil {
			for pid, im := range inv {
				if pid == target {
					for prof, file := range im.files {
						if prof != profile {
							info.AlsoIn = append(info.AlsoIn, ProfileFile{prof, file})
						}
					}
					continue
				}
				if !im.profiles[profile] {
					continue
				}
				for _, d := range im.deps {
					if d.DependencyType == "required" && d.ProjectID == target {
						info.Dependents = append(info.Dependents, im.name)
						break
					}
				}
			}
		}
	} else {
		for _, op := range m.profiles {
			if op.Name == profile {
				continue
			}
			if _, err := findLive(op, filename); err == nil {
				info.AlsoIn = append(info.AlsoIn, ProfileFile{op.Name, filename})
			}
		}
	}
	sort.Strings(info.Dependents)
	sort.Slice(info.AlsoIn, func(i, j int) bool { return info.AlsoIn[i].Profile < info.AlsoIn[j].Profile })
	return info, nil
}

// StageRemove marks a live file for removal. Server: vorgemerkt bis zum
// Neustart; Client: sofort (Datei wandert nach .backup, Rollback möglich).
func (m *Manager) StageRemove(profile, filename string) error {
	p, err := m.profile(profile)
	if err != nil {
		return err
	}
	m.stageMu.Lock()
	applied, err := m.stageRemoveLocked(p, filename)
	m.stageMu.Unlock()
	if len(applied) > 0 {
		m.notify(profile, applied)
	}
	return err
}

func (m *Manager) stageRemoveLocked(p Profile, filename string) ([]Change, error) {
	profile := p.Name
	if profile == ProfileClient && m.HasStaged(ProfileClient) {
		return nil, errClientPending
	}
	cat, err := findLive(p, filename)
	if err != nil {
		return nil, err
	}
	sdir := stagingDir(p)
	manPath := filepath.Join(sdir, "manifest.json")
	man, err := loadManifest(manPath)
	if err != nil {
		return nil, err
	}
	for _, it := range man.Items {
		if it.OldFilename == filename {
			return nil, fmt.Errorf("für %s ist schon eine Änderung vorgemerkt — erst verwerfen", filename)
		}
	}
	item := stagingItem{Category: cat, OldFilename: filename}
	m.mu.Lock()
	for _, e := range m.entries[profile] {
		if e.Filename == filename && e.Category == cat {
			item.ProjectID, item.Name = e.ProjectID, e.Name
		}
	}
	m.mu.Unlock()
	if err := os.MkdirAll(sdir, 0o755); err != nil {
		return nil, err
	}
	man.Created = time.Now()
	man.Items = append(man.Items, item)
	if err := writeManifest(manPath, man); err != nil {
		return nil, err
	}
	if profile == ProfileClient {
		_, _, changes, err := m.applyStagedLocked(profile)
		if err != nil {
			return changes, fmt.Errorf("client-paket: %w", err)
		}
		return changes, nil
	}
	return nil, nil
}

// StageUpload stages an uploaded file. Bekannte Modrinth-Dateien (Hash)
// werden verwaltet und bekommen Updates; unbekannte bleiben "unverwaltet".
func (m *Manager) StageUpload(ctx context.Context, profile, category, filename string, data []byte) (Entry, error) {
	p, err := m.profile(profile)
	if err != nil {
		return Entry{}, err
	}
	if profile == ProfileClient && m.HasStaged(ProfileClient) {
		return Entry{}, errClientPending
	}
	live, ok := p.Dirs[category]
	if !ok {
		return Entry{}, fmt.Errorf("Profil %s hat keine Kategorie %q", profile, category)
	}
	if !validName(filename) {
		return Entry{}, fmt.Errorf("ungültiger Dateiname %q", filename)
	}
	ext := uploadExt[category]
	if ext == "" || !strings.EqualFold(filepath.Ext(filename), ext) {
		return Entry{}, fmt.Errorf("für %s sind nur %s-Dateien erlaubt", category, ext)
	}
	if len(data) == 0 || len(data) > MaxUploadBytes {
		return Entry{}, fmt.Errorf("Datei leer oder größer als 100 MB")
	}
	if _, err := os.Stat(filepath.Join(live, filename)); err == nil {
		return Entry{}, fmt.Errorf("%s liegt bereits im Profil %s", filename, profile)
	}
	sdir := stagingDir(p)
	manPath := filepath.Join(sdir, "manifest.json")
	man, err := loadManifest(manPath)
	if err != nil {
		return Entry{}, err
	}
	for _, it := range man.Items {
		if it.NewFilename == filename {
			return Entry{}, fmt.Errorf("%s ist schon vorgemerkt", filename)
		}
	}
	sum := sha512.Sum512(data)
	hash := hex.EncodeToString(sum[:])
	item := stagingItem{Category: category, NewFilename: filename, SHA512: hash, Name: filename}
	if vs, err := m.api.VersionsByHash(ctx, []string{hash}); err == nil {
		if v, ok := vs[hash]; ok {
			item.ProjectID, item.Name, item.Version = v.ProjectID, v.Name, v.VersionNumber
		}
	}
	var applied []Change
	m.stageMu.Lock()
	err = func() error {
		if profile == ProfileClient && m.HasStaged(ProfileClient) {
			return errClientPending
		}
		man, err := loadManifest(manPath) // frisch lesen — VersionsByHash lief ohne Sperre
		if err != nil {
			return err
		}
		for _, it := range man.Items {
			if it.NewFilename == filename {
				return fmt.Errorf("%s ist schon vorgemerkt", filename)
			}
		}
		if err := os.MkdirAll(sdir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(sdir, filename), data, 0o644); err != nil {
			return err
		}
		man.Created = time.Now()
		man.Items = append(man.Items, item)
		if err := writeManifest(manPath, man); err != nil {
			return err
		}
		if profile == ProfileClient {
			_, _, changes, err := m.applyStagedLocked(profile)
			applied = changes
			if err != nil {
				return fmt.Errorf("client-paket: %w", err)
			}
		}
		return nil
	}()
	m.stageMu.Unlock()
	if len(applied) > 0 {
		m.notify(profile, applied)
	}
	if err != nil {
		return Entry{}, err
	}
	return Entry{Profile: profile, Category: category, Filename: filename, SHA512: hash,
		Managed: item.ProjectID != "", ProjectID: item.ProjectID, Name: item.Name, Version: item.Version}, nil
}
