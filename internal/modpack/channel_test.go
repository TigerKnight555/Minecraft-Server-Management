package modpack

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/modrinth"
)

type fakeAPI struct {
	known       map[string]modrinth.Version // sha512 → Version
	project     modrinth.Project
	versions    []modrinth.Version
	created     []modrinth.NewVersion
	packs       [][]byte
	failNext    int   // so viele CreateVersion-Aufrufe mit 400 beantworten
	userErr     error // CurrentUser scheitert (z. B. Token ohne „Read user data")
	versionsErr error // AuthProjectVersions scheitert
}

func (f *fakeAPI) VersionsByHash(_ context.Context, hs []string) (map[string]modrinth.Version, error) {
	out := map[string]modrinth.Version{}
	for _, h := range hs {
		if v, ok := f.known[h]; ok {
			out[h] = v
		}
	}
	return out, nil
}
func (f *fakeAPI) Projects(_ context.Context, ids []string) ([]modrinth.Project, error) {
	var out []modrinth.Project
	for _, id := range ids {
		out = append(out, modrinth.Project{ID: id, Title: "Titel-" + id})
	}
	return out, nil
}
func (f *fakeAPI) AuthProject(context.Context, string, string) (modrinth.Project, error) {
	return f.project, nil
}
func (f *fakeAPI) AuthProjectVersions(context.Context, string, string) ([]modrinth.Version, error) {
	if f.versionsErr != nil {
		return nil, f.versionsErr
	}
	return f.versions, nil
}
func (f *fakeAPI) CreateVersion(_ context.Context, _ string, v modrinth.NewVersion, _ string, data []byte) (modrinth.Version, error) {
	if f.failNext > 0 {
		f.failNext--
		f.versions = append(f.versions, modrinth.Version{VersionNumber: v.VersionNumber})
		return modrinth.Version{}, &modrinth.APIError{Path: "/version", Status: http.StatusBadRequest, Body: "duplicate"}
	}
	f.created = append(f.created, v)
	f.packs = append(f.packs, data)
	f.versions = append(f.versions, modrinth.Version{VersionNumber: v.VersionNumber})
	return modrinth.Version{ID: "V", VersionNumber: v.VersionNumber}, nil
}
func (f *fakeAPI) CurrentUser(context.Context, string) (modrinth.User, error) {
	if f.userErr != nil {
		return modrinth.User{}, f.userErr
	}
	return modrinth.User{Username: "knvt"}, nil
}

type env struct {
	api   *fakeAPI
	ch    *Channel
	mods  string
	state map[string]string
}

func sha(b []byte) string { s := sha512.Sum512(b); return hex.EncodeToString(s[:]) }

func newEnv(t *testing.T, status string) *env {
	t.Helper()
	root := t.TempDir()
	modsDir := filepath.Join(root, "mods")
	data := filepath.Join(root, "data")
	os.MkdirAll(modsDir, 0o755)
	os.MkdirAll(data, 0o755)
	os.WriteFile(filepath.Join(data, "fabric-server-mc.26.3-loader.0.20.1-launcher.1.1.2.jar"), []byte("x"), 0o644)
	e := &env{api: &fakeAPI{known: map[string]modrinth.Version{},
		project: modrinth.Project{ID: "PACK", Slug: "mein-pack", Title: "Mein Pack", ProjectType: "modpack", Status: status}},
		mods: modsDir, state: map[string]string{}}
	e.ch = New(e.api, func() Config { return Config{Project: "mein-pack", Token: "mrp_x"} },
		map[string]string{"mods": modsDir}, data, func() string { return "26.3" },
		func(k string) string { return e.state[k] }, func(k, v string) { e.state[k] = v },
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	return e
}

// addMod legt eine Datei an und macht sie (optional) bei Modrinth bekannt.
func (e *env) addMod(name, project, version string, known bool) {
	content := []byte(name + version)
	os.WriteFile(filepath.Join(e.mods, name), content, 0o644)
	if known {
		v := modrinth.Version{ProjectID: project, VersionNumber: version}
		f := modrinth.File{URL: "https://cdn.modrinth.com/data/" + project + "/" + name, Filename: name, Size: int64(len(content))}
		f.Hashes.SHA512 = sha(content)
		f.Hashes.SHA1 = "sha1-" + name
		v.Files = []modrinth.File{f}
		e.api.known[sha(content)] = v
	}
}

func indexOf(t *testing.T, data []byte) map[string]any {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	rc, _ := zr.File[0].Open()
	raw, _ := io.ReadAll(rc)
	var idx map[string]any
	json.Unmarshal(raw, &idx)
	return idx
}

func TestPublishFirstAndMinor(t *testing.T) {
	e := newEnv(t, "approved")
	e.addMod("sodium.jar", "P-sod", "1", true)
	out, err := e.ch.Publish(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	v := e.api.created[0]
	if v.VersionNumber != "1.0.0" || v.Name != "1.0.0 (MC 26.3 · Fabric 0.20.1)" || v.ProjectID != "PACK" ||
		v.GameVersions[0] != "26.3" || v.Loaders[0] != "fabric" || v.VersionType != "release" || !v.Featured {
		t.Errorf("version = %+v", v)
	}
	idx := indexOf(t, e.api.packs[0])
	files := idx["files"].([]any)
	if len(files) != 1 || files[0].(map[string]any)["path"] != "mods/sodium.jar" {
		t.Errorf("files = %v", files)
	}
	if len(out.Lines) == 0 || !strings.Contains(strings.Join(out.Lines, "\n"), "modrinth.com/modpack/mein-pack") {
		t.Errorf("outcome = %+v, want Spieler-Zeile mit Projekt-Link", out)
	}

	e.addMod("jade.jar", "P-jade", "1", true)
	if _, err := e.ch.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := e.api.created[1].VersionNumber; got != "1.1.0" {
		t.Errorf("zweite Version = %q, want 1.1.0 (Mod dazu)", got)
	}
	if !strings.Contains(e.api.created[1].Changelog, "**Neu:** Titel-P-jade") {
		t.Errorf("changelog = %q", e.api.created[1].Changelog)
	}
	if st := e.ch.Status(); st.Version != "1.1.0" || st.Loader != "0.20.1" || !st.Ready {
		t.Errorf("status = %+v", st)
	}
}

func TestDraftProjectUploadsButStaysQuiet(t *testing.T) {
	e := newEnv(t, "draft")
	e.addMod("sodium.jar", "P-sod", "1", true)
	out, err := e.ch.Publish(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(e.api.created) != 1 || len(out.Lines) != 0 {
		t.Errorf("created=%d lines=%v, want Upload ohne Spieler-Zeile", len(e.api.created), out.Lines)
	}
}

func TestUnknownFilesAreOmitted(t *testing.T) {
	e := newEnv(t, "approved")
	e.addMod("sodium.jar", "P-sod", "1", true)
	e.addMod("fremd.jar", "", "", false)
	if _, err := e.ch.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := e.ch.Status(); len(st.Omitted) != 1 || st.Omitted[0] != "fremd.jar" {
		t.Errorf("omitted = %v", st.Omitted)
	}
	if !strings.Contains(e.api.created[0].Changelog, "fremd.jar") {
		t.Errorf("changelog nennt weggelassene Datei nicht: %q", e.api.created[0].Changelog)
	}

	only := newEnv(t, "approved")
	only.addMod("fremd.jar", "", "", false)
	if _, err := only.ch.Publish(context.Background()); err == nil || len(only.api.created) != 0 {
		t.Errorf("nur unbekannte Dateien: err=%v created=%d, want Fehler ohne Upload", err, len(only.api.created))
	}
}

func TestDuplicateVersionRetriesOnce(t *testing.T) {
	e := newEnv(t, "approved")
	e.addMod("sodium.jar", "P-sod", "1", true)
	e.api.failNext = 1 // 1.0.0 gilt plötzlich als vergeben
	if _, err := e.ch.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(e.api.created) != 1 || e.api.created[0].VersionNumber != "1.0.1" {
		t.Errorf("created = %+v, want genau ein Upload mit 1.0.1", e.api.created)
	}
}

func TestDuplicateVersionKeepsMinorBump(t *testing.T) {
	e := newEnv(t, "approved")
	e.addMod("sodium.jar", "P-sod", "1", true)
	if _, err := e.ch.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.addMod("jade.jar", "P-jade", "1", true)
	e.api.failNext = 1 // 1.1.0 gilt plötzlich als vergeben
	if _, err := e.ch.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(e.api.created) != 2 || e.api.created[1].VersionNumber != "1.2.0" {
		t.Errorf("created = %+v, want zweiter Upload mit 1.2.0 (Minor bleibt Minor)", e.api.created)
	}
}

func TestExplainForbidden(t *testing.T) {
	err := explain(&modrinth.APIError{Path: "/version", Status: 403}, "p")
	if err == nil || !strings.Contains(err.Error(), "Token") {
		t.Errorf("err = %v, want Hinweis auf Token-Rechte", err)
	}
}

func TestHigherVersionOnModrinthWins(t *testing.T) {
	e := newEnv(t, "approved")
	e.api.versions = []modrinth.Version{{VersionNumber: "2.3.0"}}
	e.addMod("sodium.jar", "P-sod", "1", true)
	if _, err := e.ch.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := e.api.created[0].VersionNumber; got != "2.4.0" {
		t.Errorf("version = %q, want 2.4.0", got)
	}
}

func TestWrongProjectTypeAndCheck(t *testing.T) {
	e := newEnv(t, "approved")
	e.api.project.ProjectType = "mod"
	e.addMod("sodium.jar", "P-sod", "1", true)
	if _, err := e.ch.Publish(context.Background()); err == nil || !strings.Contains(err.Error(), "kein Modpack") {
		t.Errorf("err = %v, want Hinweis kein Modpack", err)
	}
	if _, err := e.ch.Check(context.Background()); err == nil {
		t.Error("Check akzeptiert Mod-Projekt")
	}
	e.api.project.ProjectType = "modpack"
	msg, err := e.ch.Check(context.Background())
	if err != nil || !strings.Contains(msg, "knvt") || !strings.Contains(msg, "Mein Pack") {
		t.Errorf("Check = %q, %v", msg, err)
	}
}

// Ohne „Read user data" scheitert schon GET /user — der Hinweis muss die
// nötigen Rechte vollständig nennen.
func TestCheckNamesAllTokenScopes(t *testing.T) {
	e := newEnv(t, "approved")
	e.api.userErr = &modrinth.APIError{Path: "/user", Status: http.StatusUnauthorized}
	_, err := e.ch.Check(context.Background())
	if err == nil {
		t.Fatal("Check ohne Fehler trotz 401")
	}
	for _, scope := range []string{"Create versions", "Read projects", "Read versions", "Read user data"} {
		if !strings.Contains(err.Error(), scope) {
			t.Errorf("err = %v, want Recht %q", err, scope)
		}
	}
	if strings.Contains(err.Error(), "mrp_x") {
		t.Error("Fehlertext enthält den Token")
	}
}

// Der Kanal-Fingerabdruck folgt MC- und Loader-Version.
func TestFingerprintFollowsMCAndLoader(t *testing.T) {
	e := newEnv(t, "approved")
	if got := e.ch.Fingerprint(); got != "26.3|0.20.1" {
		t.Errorf("Fingerprint = %q, want 26.3|0.20.1", got)
	}
	mc := ""
	e.ch.mcVersion = func() string { return mc }
	e.state[stateKey] = `{"version":"1.0.0","mc":"26.2","loader":"0.19.0"}`
	if got := e.ch.Fingerprint(); got != "26.2|0.19.0" {
		t.Errorf("Server offline: Fingerprint = %q, want zuletzt veröffentlichter Stand 26.2|0.19.0", got)
	}
}

// Weggelassene Dateien und Projekt-Link überstehen einen Neustart.
func TestStatusSurvivesRestart(t *testing.T) {
	e := newEnv(t, "approved")
	e.addMod("sodium.jar", "P-sod", "1", true)
	e.addMod("fremd.jar", "", "", false)
	if _, err := e.ch.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted := New(e.api, func() Config { return Config{Project: "mein-pack", Token: "mrp_x"} },
		map[string]string{"mods": e.mods}, "", func() string { return "26.3" },
		func(k string) string { return e.state[k] }, func(k, v string) { e.state[k] = v },
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	st := restarted.Status()
	if len(st.Omitted) != 1 || st.Omitted[0] != "fremd.jar" || st.ProjectURL != "https://modrinth.com/modpack/mein-pack" {
		t.Errorf("status nach Neustart = %+v", st)
	}
}

// Ein Fehler beim Lesen der Versionsliste wird geloggt statt verschluckt.
func TestHighestErrorIsLogged(t *testing.T) {
	e := newEnv(t, "approved")
	var buf bytes.Buffer
	e.ch.log = slog.New(slog.NewTextHandler(&buf, nil))
	e.api.versionsErr = errors.New("versions kaputt")
	e.addMod("sodium.jar", "P-sod", "1", true)
	if _, err := e.ch.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "level=WARN") || !strings.Contains(buf.String(), "versions kaputt") {
		t.Errorf("log = %q, want Warnung mit Fehler", buf.String())
	}
	if strings.Contains(buf.String(), "mrp_x") {
		t.Error("Log enthält den Token")
	}
}
