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

// ListPlayers scans <dataDir>/world/playerdata for restorable players.
// Nur exakte <uuid>.dat-Namen zählen — .pre-restore-Sicherungen und
// sonstige Dateien werden ignoriert.
func ListPlayers(dataDir string) ([]Player, error) {
	entries, err := os.ReadDir(filepath.Join(dataDir, "world", "playerdata"))
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
