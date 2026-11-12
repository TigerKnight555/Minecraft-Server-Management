package mock

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

// mockFile builds a file whose hash matches what Download returns.
func mockFile(name string) modrinth.File {
	sum := sha512.Sum512(fakeUpdateContent(name))
	f := modrinth.File{URL: "mock://" + name, Filename: name, Primary: true}
	f.Hashes.SHA512 = hex.EncodeToString(sum[:])
	return f
}

// mockCatalog: Suche/Hinzufügen im Mock-Modus. jade-mock braucht balm-mock.
var mockCatalog = []struct {
	project modrinth.Project
	version modrinth.Version
}{
	{
		modrinth.Project{ID: "jade-mock", Slug: "jade", Title: "Jade (Mock)", Description: "Zeigt an, worauf du schaust", ClientSide: "required", ServerSide: "optional", ProjectType: "mod"},
		modrinth.Version{ID: "jade-v1", ProjectID: "jade-mock", Name: "Jade 1.0", VersionNumber: "1.0",
			Files:        []modrinth.File{mockFile("jade-mock-1.0.jar")},
			Dependencies: []modrinth.Dependency{{ProjectID: "balm-mock", DependencyType: "required"}}},
	},
	{
		modrinth.Project{ID: "balm-mock", Slug: "balm", Title: "Balm (Mock)", Description: "Bibliothek", ClientSide: "required", ServerSide: "required", ProjectType: "mod"},
		modrinth.Version{ID: "balm-v1", ProjectID: "balm-mock", Name: "Balm 1.0", VersionNumber: "1.0",
			Files: []modrinth.File{mockFile("balm-mock-1.0.jar")}},
	},
}

func (m *Modrinth) Search(_ context.Context, query, _, _ string, _ int) ([]modrinth.SearchHit, error) {
	out := []modrinth.SearchHit{}
	q := strings.ToLower(query)
	for _, c := range mockCatalog {
		if strings.Contains(strings.ToLower(c.project.Title), q) || strings.Contains(c.project.ID, q) {
			out = append(out, modrinth.SearchHit{
				ProjectID: c.project.ID, Slug: c.project.Slug, Title: c.project.Title,
				Description: c.project.Description, Downloads: 1234,
				ClientSide: c.project.ClientSide, ServerSide: c.project.ServerSide,
			})
		}
	}
	return out, nil
}

func (m *Modrinth) Project(_ context.Context, id string) (modrinth.Project, error) {
	for _, c := range mockCatalog {
		if c.project.ID == id || c.project.Slug == id {
			return c.project, nil
		}
	}
	return modrinth.Project{}, fmt.Errorf("modrinth /project/%s: 404 Not Found", id)
}

func (m *Modrinth) ProjectVersions(_ context.Context, id, _, _ string) ([]modrinth.Version, error) {
	for _, c := range mockCatalog {
		if c.project.ID == id {
			return []modrinth.Version{c.version}, nil
		}
	}
	return []modrinth.Version{}, nil
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

// Projects returns catalog projects; unknown IDs are omitted (Anzeige fällt
// dann auf den Versionsnamen zurück).
func (m *Modrinth) Projects(_ context.Context, ids []string) ([]modrinth.Project, error) {
	out := []modrinth.Project{}
	for _, id := range ids {
		for _, c := range mockCatalog {
			if c.project.ID == id {
				out = append(out, c.project)
			}
		}
	}
	return out, nil
}
