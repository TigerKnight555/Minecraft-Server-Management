package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mock"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mods"
)

func modsServer(t *testing.T) (*Server, []mods.Profile) {
	t.Helper()
	profiles, err := mock.CreateFakeProfiles(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mgr := mods.NewManager(mock.NewModrinth(), "fabric", profiles)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	return New(Deps{ModManager: mgr, FallbackMCVersion: "26.3", Log: log}), profiles
}

func do(srv *Server, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

func TestModsSearchPlanAddDiscard(t *testing.T) {
	srv, profiles := modsServer(t)

	rec := do(srv, "GET", "/api/mods/search?q=jade", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "jade-mock") {
		t.Fatalf("search = %d %s", rec.Code, rec.Body)
	}
	if rec := do(srv, "GET", "/api/mods/search?q=j", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("1-Zeichen-Suche = %d, want 400", rec.Code)
	}

	rec = do(srv, "GET", "/api/mods/plan?project=jade-mock", "")
	var plan mods.AddPlan
	if err := json.NewDecoder(rec.Body).Decode(&plan); err != nil || !plan.Client || len(plan.Deps) != 1 {
		t.Fatalf("plan = %+v err=%v", plan, err)
	}

	rec = do(srv, "POST", "/api/mods/add", `{"projectId":"jade-mock","profiles":["server","client"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("add = %d %s", rec.Code, rec.Body)
	}
	clientMods := profiles[1].Dirs["mods"]
	for _, f := range []string{"jade-mock-1.0.jar", "balm-mock-1.0.jar"} {
		if _, err := os.Stat(filepath.Join(clientMods, f)); err != nil {
			t.Errorf("client fehlt %s", f)
		}
	}
	rec = do(srv, "GET", "/api/mods?profile=server", "")
	if !strings.Contains(rec.Body.String(), `"pending":"add"`) {
		t.Errorf("Server-Liste ohne Vormerkung: %s", rec.Body)
	}

	if rec := do(srv, "POST", "/api/mods/discard", `{"profile":"server","filename":"jade-mock-1.0.jar"}`); rec.Code != http.StatusOK {
		t.Errorf("discard = %d %s", rec.Code, rec.Body)
	}
	if rec := do(srv, "POST", "/api/mods/discard", `{"profile":"server","filename":"jade-mock-1.0.jar"}`); rec.Code != http.StatusConflict {
		t.Errorf("zweites discard = %d, want 409", rec.Code)
	}
}

func TestModsRemoveFlow(t *testing.T) {
	srv, profiles := modsServer(t)
	rec := do(srv, "GET", "/api/mods/remove-info?profile=server&file=lithium-0.21.2.jar", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("remove-info = %d %s", rec.Code, rec.Body)
	}
	if rec := do(srv, "POST", "/api/mods/remove", `{"profile":"server","filename":"lithium-0.21.2.jar"}`); rec.Code != http.StatusOK {
		t.Fatalf("remove = %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(profiles[0].Dirs["mods"], "lithium-0.21.2.jar")); err != nil {
		t.Error("Server-Mod sofort entfernt — muss vorgemerkt bleiben")
	}
	if rec := do(srv, "POST", "/api/mods/remove", `{"profile":"server","filename":"../x.jar"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("Pfad-Trick = %d, want 400", rec.Code)
	}
}

func TestModsUploadRejectsWrongType(t *testing.T) {
	srv, _ := modsServer(t)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("profile", "server")
	mw.WriteField("category", "mods")
	fw, _ := mw.CreateFormFile("file", "evil.zip")
	fw.Write([]byte("x"))
	mw.Close()
	req := httptest.NewRequest("POST", "/api/mods/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("upload .zip als Mod = %d %s, want 400", rec.Code, rec.Body)
	}
}

func TestApplyInfoWithoutScheduler(t *testing.T) {
	srv, _ := modsServer(t)
	rec := do(srv, "GET", "/api/mods/apply-info", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"routine":""`) {
		t.Errorf("apply-info = %d %s", rec.Code, rec.Body)
	}
}
