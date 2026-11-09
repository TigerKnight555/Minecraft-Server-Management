package mods

import (
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
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
// projectTitle returns the Modrinth project title ("Waystones") — der
// Versionsname aus dem Hash-Abgleich ist bei manchen Mods nur eine Nummer
// („26.3.0.2+fabric-26.3", im WSL-Test 2026-11-08 aufgefallen).
func (m *Manager) projectTitle(ctx context.Context, projectID, fallback string) string {
	if p, err := m.api.Project(ctx, projectID); err == nil && p.Title != "" {
		return p.Title
	}
	return fallback
}

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
			target, info.Name = v.ProjectID, m.projectTitle(ctx, v.ProjectID, v.Name)
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
						info.Dependents = append(info.Dependents, m.projectTitle(ctx, pid, im.name))
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

// UploadFile is an uploaded file spooled to disk by SpoolUpload.
type UploadFile struct {
	Path   string // Temp-Datei im Staging-Ordner (Aufrufer löscht sie)
	Size   int64
	SHA512 string
}

var errUploadSize = errors.New("Datei leer oder zu groß (max. 100 MB)")

// SpoolUpload streams r into a hidden temp file inside the profile's staging
// dir (beschreibbarer Mount — das Root-FS des Containers ist read-only),
// hashing while copying and enforcing MaxUploadBytes. Bei jedem Fehler ist
// die Temp-Datei wieder weg; sonst löscht der Aufrufer f.Path.
func (m *Manager) SpoolUpload(profile string, r io.Reader) (UploadFile, error) {
	p, err := m.profile(profile)
	if err != nil {
		return UploadFile{}, err
	}
	sdir := stagingDir(p)
	if sdir == "" {
		return UploadFile{}, fmt.Errorf("Profil %s hat keinen Ordner", profile)
	}
	if err := os.MkdirAll(sdir, 0o755); err != nil {
		return UploadFile{}, err
	}
	tmp, err := os.CreateTemp(sdir, ".upload-*.tmp")
	if err != nil {
		return UploadFile{}, err
	}
	h := sha512.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r, MaxUploadBytes+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
		os.Remove(tmp.Name())
		return UploadFile{}, fmt.Errorf("Upload abgebrochen oder zu groß (max. 100 MB): %w", err)
	case n == 0 || n > MaxUploadBytes:
		os.Remove(tmp.Name())
		return UploadFile{}, errUploadSize
	}
	return UploadFile{Path: tmp.Name(), Size: n, SHA512: hex.EncodeToString(h.Sum(nil))}, nil
}

// StageUpload stages uploaded bytes for one profile (Komfort für Tests und
// kleine Dateien; der HTTP-Upload nutzt SpoolUpload + StageUploads).
func (m *Manager) StageUpload(ctx context.Context, profile, category, filename string, data []byte) (Entry, error) {
	if len(data) == 0 || len(data) > MaxUploadBytes {
		return Entry{}, errUploadSize
	}
	f, err := m.SpoolUpload(profile, bytes.NewReader(data))
	if err != nil {
		return Entry{}, err
	}
	defer os.Remove(f.Path)
	return m.StageUploads(ctx, []string{profile}, category, filename, f)
}

// StageUploads stages an uploaded file in every given profile — alles oder
// nichts: Phase A prüft alle Profile, Phase B schreibt erst danach. Bekannte
// Modrinth-Dateien (Hash) werden verwaltet und bekommen Updates; unbekannte
// bleiben "unverwaltet". Server: vorgemerkt; Client: sofort angewendet.
func (m *Manager) StageUploads(ctx context.Context, profiles []string, category, filename string, f UploadFile) (Entry, error) {
	var ps []Profile
	seen := map[string]bool{}
	for _, name := range profiles {
		if seen[name] {
			continue
		}
		seen[name] = true
		p, err := m.profile(name)
		if err != nil {
			return Entry{}, err
		}
		ps = append(ps, p)
	}
	if len(ps) == 0 {
		return Entry{}, fmt.Errorf("mindestens ein Profil wählen")
	}
	if !validName(filename) {
		return Entry{}, fmt.Errorf("ungültiger Dateiname %q", filename)
	}
	ext := uploadExt[category]
	if ext == "" || !strings.EqualFold(filepath.Ext(filename), ext) {
		return Entry{}, fmt.Errorf("für %s sind nur %s-Dateien erlaubt", category, ext)
	}
	if f.Size <= 0 || f.Size > MaxUploadBytes {
		return Entry{}, errUploadSize
	}

	// Phase A: alle Profile prüfen, ohne zu schreiben.
	m.stageMu.Lock()
	err := m.checkUpload(ps, category, filename)
	m.stageMu.Unlock()
	if err != nil {
		return Entry{}, err
	}
	item := stagingItem{Category: category, NewFilename: filename, SHA512: f.SHA512, Name: filename}
	if vs, err := m.api.VersionsByHash(ctx, []string{f.SHA512}); err == nil {
		if v, ok := vs[f.SHA512]; ok {
			item.ProjectID, item.Name, item.Version = v.ProjectID, v.Name, v.VersionNumber
		}
	}

	// Phase B: unter stageMu erneut prüfen (VersionsByHash lief ohne
	// Sperre), dann Dateien ablegen, dann die Listen schreiben.
	var applied []Change
	m.stageMu.Lock()
	err = func() error {
		if err := m.checkUpload(ps, category, filename); err != nil {
			return err
		}
		var written []string
		for _, p := range ps {
			sdir := stagingDir(p)
			dst := filepath.Join(sdir, filename)
			if err := os.MkdirAll(sdir, 0o755); err == nil {
				err = linkOrCopy(f.Path, dst)
			}
			if err != nil {
				for _, w := range written {
					os.Remove(w)
				}
				return err
			}
			written = append(written, dst)
		}
		for _, p := range ps {
			manPath := filepath.Join(stagingDir(p), "manifest.json")
			man, err := loadManifest(manPath)
			if err != nil {
				return err
			}
			man.Created = time.Now()
			man.Items = append(man.Items, item)
			if err := writeManifest(manPath, man); err != nil {
				return err
			}
			if p.Name == ProfileClient {
				_, _, changes, err := m.applyStagedLocked(p.Name)
				applied = changes
				if err != nil {
					return fmt.Errorf("client-paket: %w", err)
				}
			}
		}
		return nil
	}()
	m.stageMu.Unlock()
	if len(applied) > 0 {
		m.notify(ProfileClient, applied)
	}
	if err != nil {
		return Entry{}, err
	}
	last := ps[len(ps)-1]
	return Entry{Profile: last.Name, Category: category, Filename: filename, SHA512: f.SHA512, Size: f.Size,
		Managed: item.ProjectID != "", ProjectID: item.ProjectID, Name: item.Name, Version: item.Version}, nil
}

// checkUpload validates filename against every profile. Caller holds stageMu.
func (m *Manager) checkUpload(ps []Profile, category, filename string) error {
	for _, p := range ps {
		if p.Name == ProfileClient && m.HasStaged(ProfileClient) {
			return errClientPending
		}
		live, ok := p.Dirs[category]
		if !ok {
			return fmt.Errorf("Profil %s hat keine Kategorie %q", p.Name, category)
		}
		if _, err := os.Stat(filepath.Join(live, filename)); err == nil {
			return fmt.Errorf("%s liegt bereits im Profil %s", filename, p.Name)
		}
		man, err := loadManifest(filepath.Join(stagingDir(p), "manifest.json"))
		if err != nil {
			return err
		}
		for _, it := range man.Items {
			if it.NewFilename == filename {
				return fmt.Errorf("%s ist im Profil %s schon vorgemerkt", filename, p.Name)
			}
		}
	}
	return nil
}

// linkOrCopy puts src at dst: Hardlink auf demselben Mount, sonst Kopie
// (Client-Paket und Server-Mods sind getrennte Mounts).
func linkOrCopy(src, dst string) error {
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dst)
	}
	return err
}
