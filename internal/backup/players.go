package backup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Player is one playerdata entry for the restore dropdown: UUID from the
// .dat filename, name resolved via the server's usercache.json (empty if
// the cache no longer knows the UUID).
type Player struct {
	UUID      string    `json:"uuid"`
	Name      string    `json:"name,omitempty"`
	LastSaved time.Time `json:"lastSaved"`
}

// PlayerDirs are the world-relative player-data directories, newest MC
// layout first: ab MC 26.x liegen die Dateien unter world/players/data,
// beim Weltupgrade verschiebt der Server world/playerdata dorthin
// (WSL-Test 2026-11-10: nach 1.21.11 → 26.3 war die Spielerliste leer).
var PlayerDirs = []string{"players/data", "playerdata"}

// PlayerDir returns the existing player-data directory of the world.
func PlayerDir(dataDir string) (string, error) {
	var firstErr error
	for _, rel := range PlayerDirs {
		dir := filepath.Join(dataDir, "world", filepath.FromSlash(rel))
		info, err := os.Stat(dir)
		if err == nil && info.IsDir() {
			return dir, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return "", firstErr
}

// ListPlayers scans the world's player-data directory for restorable
// players. Nur exakte <uuid>.dat-Namen zählen — .pre-restore-Sicherungen
// und sonstige Dateien werden ignoriert.
func ListPlayers(dataDir string) ([]Player, error) {
	dir, err := PlayerDir(dataDir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := readUsercache(filepath.Join(dataDir, "usercache.json"))

	var out []Player
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		uuid, ok := strings.CutSuffix(e.Name(), ".dat")
		if !ok || !uuidRe.MatchString(uuid) {
			continue
		}
		p := Player{UUID: strings.ToLower(uuid), Name: names[strings.ToLower(uuid)]}
		if info, err := e.Info(); err == nil {
			p.LastSaved = info.ModTime()
		}
		out = append(out, p)
	}
	// benannte Spieler zuerst (alphabetisch), unbekannte UUIDs hinten
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Name == "") != (out[j].Name == "") {
			return out[i].Name != ""
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].UUID < out[j].UUID
	})
	return out, nil
}

// readUsercache parses the server's usercache.json into uuid -> name.
// Fehler sind unkritisch (Datei fehlt/kaputt) — dann bleiben Namen leer.
func readUsercache(path string) map[string]string {
	out := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	var entries []struct {
		Name string `json:"name"`
		UUID string `json:"uuid"`
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		return out
	}
	for _, e := range entries {
		if e.UUID != "" && e.Name != "" {
			out[strings.ToLower(e.UUID)] = e.Name
		}
	}
	return out
}
