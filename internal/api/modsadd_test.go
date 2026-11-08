package api

import (
	"bytes"
	"encoding/json"
	"io"
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

func uploadReq(t *testing.T, fields [][2]string, fileFirst bool, name string, content []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	writeFile := func() {
		fw, _ := mw.CreateFormFile("file", name)
		fw.Write(content)
	}
	if fileFirst {
		writeFile()
	}
	for _, f := range fields {
		mw.WriteField(f[0], f[1])
	}
	if !fileFirst {
		writeFile()
	}
	mw.Close()
	req := httptest.NewRequest("POST", "/api/mods/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func TestModsUploadStreamsFieldsBeforeFile(t *testing.T) {
	srv, profiles := modsServer(t)
	req := uploadReq(t, [][2]string{{"profile", "server"}, {"profile", "client"}, {"category", "mods"}}, false, "custom.jar", []byte("eigener mod"))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload = %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(profiles[1].Dirs["mods"], "custom.jar")); err != nil {
		t.Error("Client-Upload nicht sofort im Paket")
	}
	if !srv.modmgr.HasStaged(mods.ProfileServer) {
		t.Error("Server-Upload nicht vorgemerkt")
	}
	assertNoTempFiles(t, profiles[0].Dirs["mods"])

	// Datei vor dem Profil: Profil ist beim Lesen der Datei unbekannt
	req = uploadReq(t, [][2]string{{"profile", "server"}}, true, "other.jar", []byte("x"))
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("Datei vor Profil = %d %s, want 400", rec.Code, rec.Body)
	}
}

// Mehrere Profile: scheitert der Client, bleibt auch der Server unverändert.
func TestModsUploadMultiProfileAllOrNothing(t *testing.T) {
	srv, profiles := modsServer(t)
	sdir := filepath.Join(profiles[1].Dirs["mods"], ".msm-staging")
	if err := os.MkdirAll(sdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdir, "manifest.json"), []byte(`{"items":[{"category":"mods","oldFilename":"x.jar"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	req := uploadReq(t, [][2]string{{"profile", "server"}, {"profile", "client"}, {"category", "mods"}}, false, "custom.jar", []byte("eigener mod"))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("upload = %d %s, want 400", rec.Code, rec.Body)
	}
	if srv.modmgr.HasStaged(mods.ProfileServer) {
		t.Error("Server trotz Fehler beim Client vorgemerkt")
	}
	assertNoTempFiles(t, profiles[0].Dirs["mods"])
}

func TestModsUploadTooLarge(t *testing.T) {
	srv, profiles := modsServer(t)
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		mw.WriteField("profile", "server")
		mw.WriteField("category", "mods")
		fw, _ := mw.CreateFormFile("file", "big.jar")
		chunk := make([]byte, 1<<20)
		for i := 0; i < 101; i++ {
			if _, err := fw.Write(chunk); err != nil {
				pw.CloseWithError(err)
				return
			}
		}
		mw.Close()
		pw.Close()
	}()
	req := httptest.NewRequest("POST", "/api/mods/upload", pr)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	pr.CloseWithError(io.ErrClosedPipe)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "max. 100 MB") {
		t.Errorf("upload = %d %s, want 400 max. 100 MB", rec.Code, rec.Body)
	}
	assertNoTempFiles(t, profiles[0].Dirs["mods"])
}

// assertNoTempFiles: im Staging-Ordner liegen nur vorgemerkte Dateien und
// die Liste, keine Upload-Reste (versteckte Temp-Dateien).
func assertNoTempFiles(t *testing.T, modsDir string) {
	t.Helper()
	items, _ := os.ReadDir(filepath.Join(modsDir, ".msm-staging"))
	for _, it := range items {
		if strings.HasPrefix(it.Name(), ".") {
			t.Errorf("Temp-Datei übrig: %s", it.Name())
		}
	}
}
