package mock

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/modrinth"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mods"
)

// Modrinth fakes the API: every known file resolves to a project, half of
// them have an update available. Downloads return deterministic content.
type Modrinth struct {
	// hash -> version info, filled lazily from VersionsByHash calls
	known map[string]modrinth.Version
}

func NewModrinth() *Modrinth { return &Modrinth{known: map[string]modrinth.Version{}} }

func fakeUpdateContent(name string) []byte { return []byte("updated-jar-content: " + name) }

func (m *Modrinth) VersionsByHash(_ context.Context, sha512s []string) (map[string]modrinth.Version, error) {
	out := map[string]modrinth.Version{}
	for i, h := range sha512s {
		v := modrinth.Version{
			ID: fmt.Sprintf("ver-%d", i), ProjectID: fmt.Sprintf("proj-%d", i),
			Name: fmt.Sprintf("Mock Mod %d", i+1), VersionNumber: "1.0.0",
		}
		m.known[h] = v
		out[h] = v
	}
	return out, nil
}

func (m *Modrinth) LatestVersions(_ context.Context, sha512s []string, _, _ string) (map[string]modrinth.Version, error) {
	out := map[string]modrinth.Version{}
	for i, h := range sha512s {
		if i%2 != 0 {
			continue // jeder zweite Mod ist aktuell
		}
		filename := fmt.Sprintf("mock-mod-%d-1.1.0.jar", i+1)
		content := fakeUpdateContent(filename)
		sum := sha512.Sum512(content)
		v := modrinth.Version{
			ID: fmt.Sprintf("ver-%d-new", i), ProjectID: fmt.Sprintf("proj-%d", i),
			Name: fmt.Sprintf("Mock Mod %d", i+1), VersionNumber: "1.1.0",
			Changelog: "Bugfixes und Verbesserungen (Mock)",
			Files: []modrinth.File{{
				URL: "mock://" + filename, Filename: filename, Primary: true,
				Hashes: struct {
					SHA512 string `json:"sha512"`
				}{SHA512: hex.EncodeToString(sum[:])},
			}},
		}
		out[h] = v
	}
	return out, nil
}

func (m *Modrinth) ProjectSupports(_ context.Context, projectID, _, _ string) (bool, error) {
	// proj-0 hinkt hinterher, Rest ist bereit
	return projectID != "proj-0", nil
}

func (m *Modrinth) Download(_ context.Context, url string) ([]byte, error) {
	return fakeUpdateContent(filepath.Base(url)), nil
}

// CreateFakeProfiles writes a few dummy jars under baseDir and returns
// profiles pointing at them (mock mode + tests).
func CreateFakeProfiles(baseDir string) ([]mods.Profile, error) {
	serverMods := filepath.Join(baseDir, "server", "mods")
	clientMods := filepath.Join(baseDir, "client-pack", "mods")
	clientShaders := filepath.Join(baseDir, "client-pack", "shaderpacks")
	for _, d := range []string{serverMods, clientMods, clientShaders} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	files := map[string]string{
		filepath.Join(serverMods, "fabric-api-0.140.2.jar"):    "fabric-api-content",
		filepath.Join(serverMods, "lithium-0.21.2.jar"):        "lithium-content",
		filepath.Join(serverMods, "spark-1.10.170.jar"):        "spark-content",
		filepath.Join(serverMods, "unknown-custom-mod.jar"):    "custom-content",
		filepath.Join(clientMods, "fabric-api-0.140.2.jar"):    "fabric-api-content",
		filepath.Join(clientMods, "sodium-0.6.0.jar"):          "sodium-content",
		filepath.Join(clientShaders, "complementary-r5.4.zip"): "shader-content",
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return nil, err
		}
	}
	return []mods.Profile{
		{Name: "server", Dirs: map[string]string{"mods": serverMods}},
		{Name: "client", Dirs: map[string]string{"mods": clientMods, "shaderpacks": clientShaders}},
	}, nil
}
