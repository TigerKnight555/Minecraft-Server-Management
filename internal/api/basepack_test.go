package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/basepack"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/modrinth"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mods"
)

func bpServer(t *testing.T, api basepack.API, kv map[string]string) *Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	bp := basepack.New(api, stubBPFiles{}, t.TempDir(), func(k string) string { return kv[k] }, func(k, v string) { kv[k] = v }, nil, log)
	return New(Deps{ModManager: mods.NewManager(nil, "fabric", nil), Basepack: bp, FallbackMCVersion: "26.3", Log: log})
}

func bpDo(srv *Server, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

func TestBasepackRoutes(t *testing.T) {
	srv := bpServer(t, stubBPAPI{}, map[string]string{})
	if rec := bpDo(srv, "GET", "/api/basepack", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"state":null`) {
		t.Errorf("GET = %d %s", rec.Code, rec.Body)
	}
	if rec := bpDo(srv, "POST", "/api/basepack/toggle", `{"key":"x","on":false}`); rec.Code != 400 {
		t.Errorf("toggle ohne Pack = %d", rec.Code)
	}
	if rec := bpDo(srv, "GET", "/api/basepack/search?q=a", ""); rec.Code != 400 {
		t.Errorf("search kurz = %d", rec.Code)
	}
	if rec := bpDo(srv, "GET", "/api/basepack/preview", ""); rec.Code != 400 {
		t.Errorf("preview ohne project = %d", rec.Code)
	}
	srv.upgradeActive = func() bool { return true }
	for _, p := range []string{"apply", "toggle", "configs"} {
		if rec := bpDo(srv, "POST", "/api/basepack/"+p, `{"projectId":"FO","versionId":"1","key":"k"}`); rec.Code != 409 {
			t.Errorf("%s während Update = %d", p, rec.Code)
		}
	}
	if rec := bpDo(srv, "DELETE", "/api/basepack", ""); rec.Code != 409 {
		t.Errorf("remove während Update = %d", rec.Code)
	}
	// Vorschau würde den Client-Update-Cache mit der alten MC-Version füllen
	if rec := bpDo(srv, "GET", "/api/basepack/preview?project=FO", ""); rec.Code != 409 {
		t.Errorf("preview während Update = %d", rec.Code)
	}
}

func TestBasepackDisabledWithoutService(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(Deps{ModManager: mods.NewManager(nil, "fabric", nil), Log: log})
	if rec := bpDo(srv, "GET", "/api/basepack", ""); rec.Code != 404 {
		t.Errorf("ohne Service = %d", rec.Code)
	}
}

type errBPAPI struct{ stubBPAPI }

func (errBPAPI) ProjectVersions(context.Context, string, string, string) ([]modrinth.Version, error) {
	return nil, errors.New("dial tcp: secret-internal-host timeout")
}

func TestBasepackStatusCheckError(t *testing.T) {
	st := basepack.State{ProjectID: "FO", Title: "Fabulously Optimized", VersionID: "1", MC: "26.3"}
	b, _ := json.Marshal(st)
	srv := bpServer(t, errBPAPI{}, map[string]string{"basepack.state": string(b)})
	rec := bpDo(srv, "GET", "/api/basepack", "")
	if rec.Code != 200 {
		t.Fatalf("code %d", rec.Code)
	}
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out["state"] == nil || out["available"] != nil {
		t.Errorf("state/available: %v", out)
	}
	if e, _ := out["checkError"].(string); e == "" || strings.Contains(e, "secret") {
		t.Errorf("checkError = %q", e)
	}
}

func TestDecorateClient(t *testing.T) {
	st := basepack.State{ProjectID: "FO", Title: "FO", Disabled: []string{"B"}, Files: []basepack.PackFile{
		{Path: "mods/a.jar", Category: "mods", Filename: "a.jar", ProjectID: "A", Name: "A"},
		{Path: "mods/b.jar", Category: "mods", Filename: "b.jar", ProjectID: "B", Name: "B"},
	}}
	b, _ := json.Marshal(st)
	srv := bpServer(t, stubBPAPI{}, map[string]string{"basepack.state": string(b)})
	out := srv.decorateClient([]mods.Entry{{Category: "mods", Filename: "a.jar"}, {Category: "mods", Filename: "own.jar"}})
	if len(out) != 3 || out[0].Source != "basepack" || out[1].Source != "" || !out[2].Disabled || out[2].Source != "basepack" {
		t.Errorf("decorate = %+v", out)
	}
	if out[0].PackKey != "A" || out[1].PackKey != "" || out[2].PackKey != "B" {
		t.Errorf("packKey = %q / %q / %q", out[0].PackKey, out[1].PackKey, out[2].PackKey)
	}
}

type stubBPAPI struct{}

func (stubBPAPI) SearchModpacks(context.Context, string, string, string, int) ([]modrinth.SearchHit, error) {
	return nil, nil
}
func (stubBPAPI) Project(context.Context, string) (modrinth.Project, error) {
	return modrinth.Project{}, nil
}
func (stubBPAPI) ProjectVersions(context.Context, string, string, string) ([]modrinth.Version, error) {
	return nil, nil
}
func (stubBPAPI) VersionsByHash(context.Context, []string) (map[string]modrinth.Version, error) {
	return nil, nil
}
func (stubBPAPI) Projects(context.Context, []string) ([]modrinth.Project, error) { return nil, nil }
func (stubBPAPI) Download(context.Context, string) ([]byte, error)               { return nil, nil }

type stubBPFiles struct{}

func (stubBPFiles) ApplyFiles(string, []mods.FileAdd, []mods.FileRemove, string) (string, error) {
	return "", nil
}
func (stubBPFiles) CheckUpdates(context.Context, string, string) ([]mods.Entry, error) {
	return nil, nil
}
