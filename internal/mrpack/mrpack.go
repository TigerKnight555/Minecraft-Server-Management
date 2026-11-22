// Package mrpack builds and reads Modrinth modpack files (.mrpack): ein ZIP mit
// modrinth.index.json an der Wurzel, das jede Datei per Download-Link und
// Hashes beschreibt (Format-Version 1, Spiel „minecraft"). Zusätzlich können
// Overrides unter overrides/ liegen; Parse liest overrides/ und
// client-overrides/ (client gewinnt), server-overrides/ wird ignoriert.
package mrpack

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// MediaType is the MIME type Modrinth expects for .mrpack uploads.
const MediaType = "application/x-modrinth-modpack+zip"

// File is one entry of the index.
type File struct {
	Path   string // relativ zum Instanzordner, z. B. "mods/sodium.jar"
	SHA1   string
	SHA512 string
	URL    string
	Size   int64
}

// Meta is the pack header.
type Meta struct {
	VersionID    string
	Name         string
	Summary      string
	Minecraft    string
	FabricLoader string
	// Overrides: Dateien, die ins Instanzverzeichnis kopiert werden
	// (Pfad relativ zur Instanz, z. B. "config/sodium.json")
	Overrides map[string][]byte
}

type index struct {
	FormatVersion int               `json:"formatVersion"`
	Game          string            `json:"game"`
	VersionID     string            `json:"versionId"`
	Name          string            `json:"name"`
	Summary       string            `json:"summary,omitempty"`
	Files         []indexFile       `json:"files"`
	Dependencies  map[string]string `json:"dependencies"`
}

type indexFile struct {
	Path      string            `json:"path"`
	Hashes    map[string]string `json:"hashes"`
	Downloads []string          `json:"downloads"`
	FileSize  int64             `json:"fileSize"`
}

// AllowedURL: Download-Quellen, die Modrinth in Modpacks erlaubt (nur https).
// Gilt beim Bauen des eigenen Packs wie beim Laden eines fremden — URLs aus
// einem .mrpack sind Fremdeingaben.
func AllowedURL(u string) bool {
	for _, p := range []string{"https://cdn.modrinth.com/", "https://github.com/", "https://raw.githubusercontent.com/", "https://gitlab.com/"} {
		if strings.HasPrefix(u, p) {
			return true
		}
	}
	return false
}

// ValidPath enforces the spec: relativ, ohne „..", ohne Laufwerk/Backslash.
func ValidPath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsAny(p, `\:`) {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// Build returns the .mrpack bytes for files and meta.
func Build(files []File, meta Meta) ([]byte, error) {
	if meta.VersionID == "" || meta.Name == "" || meta.Minecraft == "" || meta.FabricLoader == "" {
		return nil, fmt.Errorf("mrpack: versionId, name, minecraft und fabric-loader sind Pflicht")
	}
	idx := index{
		FormatVersion: 1, Game: "minecraft",
		VersionID: meta.VersionID, Name: meta.Name, Summary: meta.Summary,
		Files:        make([]indexFile, 0, len(files)),
		Dependencies: map[string]string{"minecraft": meta.Minecraft, "fabric-loader": meta.FabricLoader},
	}
	for _, f := range files {
		if !ValidPath(f.Path) {
			return nil, fmt.Errorf("mrpack: ungültiger Pfad %q", f.Path)
		}
		if f.SHA1 == "" || f.SHA512 == "" || f.URL == "" {
			return nil, fmt.Errorf("mrpack: %s: sha1, sha512 und URL sind Pflicht", f.Path)
		}
		idx.Files = append(idx.Files, indexFile{
			Path:      f.Path,
			Hashes:    map[string]string{"sha1": f.SHA1, "sha512": f.SHA512},
			Downloads: []string{f.URL},
			FileSize:  f.Size,
		})
	}
	sort.Slice(idx.Files, func(i, j int) bool { return idx.Files[i].Path < idx.Files[j].Path })
	raw, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("modrinth.index.json")
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(raw); err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(meta.Overrides))
	for p := range meta.Overrides {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if !ValidPath(p) {
			return nil, fmt.Errorf("mrpack: ungültiger Override-Pfad %q", p)
		}
		ow, err := zw.Create("overrides/" + p)
		if err != nil {
			return nil, err
		}
		if _, err := ow.Write(meta.Overrides[p]); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// IndexFile is one file entry of a parsed pack.
type IndexFile struct {
	Path      string
	SHA1      string
	SHA512    string
	URLs      []string
	Size      int64
	ClientEnv string // "required" | "optional" | "unsupported" | "" (fehlt = nötig)
}

// Index is a parsed .mrpack.
type Index struct {
	Name         string
	VersionID    string
	Summary      string
	Dependencies map[string]string
	Files        []IndexFile
	// Overrides: overrides/ + client-overrides/ (gewinnt); server-overrides/
	// wird ignoriert. Schlüssel = Pfad relativ zur Instanz.
	Overrides map[string][]byte
	// Skipped: Override-Pfade, die wegen Einzel- oder Gesamtlimit nicht
	// übernommen wurden (sortiert, für den Hinweis in der Vorschau)
	Skipped []string
}

// maxOverride limits a single override file (Configs sind klein).
const maxOverride = 8 << 20

// maxCategoryOverride limits an override directly in mods/, resourcepacks/
// or shaderpacks/ (mitgelieferte Pack-Dateien, z. B. Shaderpacks).
const maxCategoryOverride = 32 << 20

// overrideLimit returns the size limit for an override path.
func overrideLimit(rel string) int64 {
	if seg := strings.Split(rel, "/"); len(seg) == 2 {
		switch seg[0] {
		case "mods", "resourcepacks", "shaderpacks":
			return maxCategoryOverride
		}
	}
	return maxOverride
}

// Aggregatgrenzen gegen Zip-Bomben.
const (
	maxOverrideTotal = 64 << 20 // Summe aller gespeicherten Override-Bytes
	maxEntries       = 5000     // betrachtete ZIP-Einträge
)

// Parse liest ein .mrpack (ZIP). Ungültige Override-Pfade und zu große Overrides
// werden übersprungen; ungültige Index-Pfade sind ein Fehler.
func Parse(data []byte) (*Index, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("mrpack: kein gültiges ZIP: %w", err)
	}
	var raw struct {
		VersionID string `json:"versionId"`
		Name      string `json:"name"`
		Summary   string `json:"summary"`
		Files     []struct {
			Path      string            `json:"path"`
			Hashes    map[string]string `json:"hashes"`
			Env       map[string]string `json:"env"`
			Downloads []string          `json:"downloads"`
			FileSize  int64             `json:"fileSize"`
		} `json:"files"`
		Dependencies map[string]string `json:"dependencies"`
	}
	found := false
	base := map[string][]byte{}
	client := map[string][]byte{}
	skipped := map[string]bool{}       // Pfad → zu groß
	clientSkipped := map[string]bool{} // client-overrides zu groß: überdeckt auch overrides/
	entries := 0
	var total int64
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		entries++
		if entries > maxEntries {
			return nil, fmt.Errorf("mrpack: Pack zu groß oder zu viele Dateien")
		}
		if f.Name == "modrinth.index.json" {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			err = json.NewDecoder(io.LimitReader(rc, 16<<20)).Decode(&raw)
			rc.Close()
			if err != nil {
				return nil, fmt.Errorf("mrpack: index unlesbar: %w", err)
			}
			found = true
			continue
		}
		var target map[string][]byte
		var rel string
		switch {
		case strings.HasPrefix(f.Name, "client-overrides/"):
			target, rel = client, strings.TrimPrefix(f.Name, "client-overrides/")
		case strings.HasPrefix(f.Name, "overrides/"):
			target, rel = base, strings.TrimPrefix(f.Name, "overrides/")
		default:
			continue // server-overrides und Sonstiges
		}
		if !ValidPath(rel) {
			continue
		}
		skip := func() {
			skipped[rel] = true
			if strings.HasPrefix(f.Name, "client-overrides/") {
				clientSkipped[rel] = true
			}
		}
		limit := overrideLimit(rel)
		if f.UncompressedSize64 > uint64(limit) || total+int64(f.UncompressedSize64) > maxOverrideTotal {
			skip()
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(io.LimitReader(rc, limit+1))
		rc.Close()
		if err != nil {
			return nil, err
		}
		if int64(len(b)) > limit || total+int64(len(b)) > maxOverrideTotal {
			skip() // Größenangabe im ZIP gelogen
			continue
		}
		total += int64(len(b))
		target[rel] = b
	}
	if !found {
		return nil, fmt.Errorf("mrpack: modrinth.index.json fehlt")
	}
	idx := &Index{Name: raw.Name, VersionID: raw.VersionID, Summary: raw.Summary,
		Dependencies: raw.Dependencies, Overrides: base}
	for p, b := range client {
		idx.Overrides[p] = b
	}
	for p := range clientSkipped {
		delete(idx.Overrides, p) // client-Datei gewinnt, auch wenn sie fehlt
	}
	for p := range skipped {
		if _, ok := idx.Overrides[p]; !ok {
			idx.Skipped = append(idx.Skipped, p)
		}
	}
	sort.Strings(idx.Skipped)
	for _, f := range raw.Files {
		if !ValidPath(f.Path) {
			return nil, fmt.Errorf("mrpack: ungültiger Pfad %q im Index", f.Path)
		}
		idx.Files = append(idx.Files, IndexFile{Path: f.Path, SHA1: f.Hashes["sha1"], SHA512: f.Hashes["sha512"],
			URLs: f.Downloads, Size: f.FileSize, ClientEnv: f.Env["client"]})
	}
	return idx, nil
}
