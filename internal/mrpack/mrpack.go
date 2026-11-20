// Package mrpack builds Modrinth modpack files (.mrpack): ein ZIP mit
// modrinth.index.json an der Wurzel, das jede Datei per Download-Link und
// Hashes beschreibt (Format-Version 1, Spiel „minecraft"). MSM braucht keine
// overrides — das Client-Paket besteht nur aus Modrinth-Inhalten.
package mrpack

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
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
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
