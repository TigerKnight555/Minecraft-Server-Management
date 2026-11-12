package mods_test

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/modrinth"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mods"
)

// fakeAPI is a tiny in-memory Modrinth for add/remove tests.
type fakeAPI struct {
	projects map[string]modrinth.Project
	versions map[string][]modrinth.Version
	byHash   map[string]modrinth.Version
	files    map[string][]byte
	// onDownload läuft vor jedem Download (Tests für gleichzeitige Änderungen)
	onDownload func()
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{projects: map[string]modrinth.Project{}, versions: map[string][]modrinth.Version{},
		byHash: map[string]modrinth.Version{}, files: map[string][]byte{}}
}

func req(pid string) modrinth.Dependency {
	return modrinth.Dependency{ProjectID: pid, DependencyType: "required"}
}

// addVersion registers a project with one version and one file.
func (f *fakeAPI) addVersion(pid, title, filename, client, server string, deps ...modrinth.Dependency) modrinth.Version {
	f.projects[pid] = modrinth.Project{ID: pid, Slug: pid, Title: title, ClientSide: client, ServerSide: server, ProjectType: "mod"}
	content := []byte("jar:" + filename)
	sum := sha512.Sum512(content)
	h := hex.EncodeToString(sum[:])
	file := modrinth.File{URL: "fake://" + filename, Filename: filename, Primary: true}
	file.Hashes.SHA512 = h
	v := modrinth.Version{ID: "v-" + pid, ProjectID: pid, Name: title + " 1.0", VersionNumber: "1.0",
		Files: []modrinth.File{file}, Dependencies: deps}
	f.versions[pid] = append([]modrinth.Version{v}, f.versions[pid]...)
	f.byHash[h] = v
	f.files[file.URL] = content
	return v
}

func (f *fakeAPI) VersionsByHash(_ context.Context, hs []string) (map[string]modrinth.Version, error) {
	out := map[string]modrinth.Version{}
	for _, h := range hs {
		if v, ok := f.byHash[h]; ok {
			out[h] = v
		}
	}
	return out, nil
}
func (f *fakeAPI) LatestVersions(context.Context, []string, string, string) (map[string]modrinth.Version, error) {
	return map[string]modrinth.Version{}, nil
}
func (f *fakeAPI) ProjectSupports(context.Context, string, string, string) (bool, error) {
	return true, nil
}
func (f *fakeAPI) Download(_ context.Context, url string) ([]byte, error) {
	if f.onDownload != nil {
		f.onDownload()
	}
	b, ok := f.files[url]
	if !ok {
		return nil, fmt.Errorf("404 %s", url)
	}
	return b, nil
}
func (f *fakeAPI) Search(_ context.Context, q, _, _ string, _ int) ([]modrinth.SearchHit, error) {
	var out []modrinth.SearchHit
	for _, p := range f.projects {
		if strings.Contains(strings.ToLower(p.Title), strings.ToLower(q)) {
			out = append(out, modrinth.SearchHit{ProjectID: p.ID, Title: p.Title})
		}
	}
	return out, nil
}
func (f *fakeAPI) Project(_ context.Context, id string) (modrinth.Project, error) {
	p, ok := f.projects[id]
	if !ok {
		return p, fmt.Errorf("404 project %s", id)
	}
	return p, nil
}
func (f *fakeAPI) Projects(_ context.Context, ids []string) ([]modrinth.Project, error) {
	var out []modrinth.Project
	for _, id := range ids {
		if p, ok := f.projects[id]; ok {
			out = append(out, p)
		}
	}
	return out, nil
}
func (f *fakeAPI) ProjectVersions(_ context.Context, id, _, _ string) ([]modrinth.Version, error) {
	return f.versions[id], nil
}

// addSetup: Server- und Client-Profil; Fabric API liegt nur im Server.
func addSetup(t *testing.T) (*mods.Manager, *fakeAPI, string, string) {
	t.Helper()
	base := t.TempDir()
	serverDir := filepath.Join(base, "server", "mods")
	clientDir := filepath.Join(base, "client", "mods")
	shaderDir := filepath.Join(base, "client", "shaderpacks")
	for _, d := range []string{serverDir, clientDir, shaderDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	api := newFakeAPI()
	fapi := api.addVersion("fapi", "Fabric API", "fabric-api.jar", "required", "required")
	if err := os.WriteFile(filepath.Join(serverDir, "fabric-api.jar"), api.files[fapi.Files[0].URL], 0o644); err != nil {
		t.Fatal(err)
	}
	mgr := mods.NewManager(api, "fabric", []mods.Profile{
		{Name: mods.ProfileServer, Dirs: map[string]string{"mods": serverDir}},
		{Name: mods.ProfileClient, Dirs: map[string]string{"mods": clientDir, "shaderpacks": shaderDir}},
	})
	return mgr, api, serverDir, clientDir
}

func depNames(p *mods.AddPlan) []string {
	var out []string
	for _, d := range p.Deps {
		out = append(out, d.Name)
	}
	return out
}

func TestPlanSuggestsSidesAndListsDeps(t *testing.T) {
	mgr, api, _, _ := addSetup(t)
	api.addVersion("balm", "Balm", "balm.jar", "required", "required")
	api.addVersion("way", "Waystones", "waystones.jar", "required", "required", req("balm"), req("fapi"))

	plan, err := mgr.Plan(context.Background(), "way", "26.3")
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Server || !plan.Client || plan.Blocked != "" {
		t.Fatalf("plan = %+v", plan)
	}
	if got := strings.Join(depNames(plan), ","); got != "Balm,Fabric API" {
		t.Errorf("deps = %s, want Balm,Fabric API (Fabric API fehlt im Client)", got)
	}
	for _, d := range plan.Deps {
		if d.Name == "Fabric API" && (!d.Present[mods.ProfileServer] || d.Present[mods.ProfileClient]) {
			t.Errorf("Fabric API present = %v, want nur server", d.Present)
		}
	}
}

func TestPlanSuggestsClientOnlyMod(t *testing.T) {
	mgr, api, _, _ := addSetup(t)
	api.addVersion("sodium", "Sodium", "sodium.jar", "required", "unsupported")
	plan, err := mgr.Plan(context.Background(), "sodium", "26.3")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Server || !plan.Client {
		t.Errorf("server=%v client=%v, want nur client", plan.Server, plan.Client)
	}
}

func TestPlanBlockedWhenDependencyHasNoBuild(t *testing.T) {
	mgr, api, _, _ := addSetup(t)
	api.projects["balm"] = modrinth.Project{ID: "balm", Title: "Balm"} // keine Version für 26.3
	api.addVersion("way", "Waystones", "waystones.jar", "required", "required", req("balm"))
	plan, err := mgr.Plan(context.Background(), "way", "26.3")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Blocked, "Balm") || !strings.Contains(plan.Blocked, "26.3") {
		t.Errorf("blocked = %q", plan.Blocked)
	}
}

func TestPlanBlockedWhenModHasNoBuild(t *testing.T) {
	mgr, api, _, _ := addSetup(t)
	api.projects["old"] = modrinth.Project{ID: "old", Title: "Altmod"}
	plan, err := mgr.Plan(context.Background(), "old", "26.3")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Blocked, "Altmod") {
		t.Errorf("blocked = %q", plan.Blocked)
	}
}

func TestPlanStopsDeepDependencyChains(t *testing.T) {
	mgr, api, _, _ := addSetup(t)
	api.addVersion("e", "E", "e.jar", "required", "required")
	api.addVersion("d", "D", "d.jar", "required", "required", req("e"))
	api.addVersion("c", "C", "c.jar", "required", "required", req("d"))
	api.addVersion("b", "B", "b.jar", "required", "required", req("c"))
	api.addVersion("a", "A", "a.jar", "required", "required", req("b"))
	plan, err := mgr.Plan(context.Background(), "a", "26.3")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(depNames(plan), ","); got != "B,C,D" {
		t.Errorf("deps = %s, want B,C,D (3 Ebenen)", got)
	}
	if len(plan.Warnings) == 0 {
		t.Error("keine Warnung zur abgeschnittenen Kette")
	}
}

func TestPlanWarnsIncompatibleInstalledMod(t *testing.T) {
	mgr, api, _, _ := addSetup(t)
	api.addVersion("opt", "OptiStuff", "opt.jar", "required", "unsupported",
		modrinth.Dependency{ProjectID: "fapi", DependencyType: "incompatible"})
	plan, err := mgr.Plan(context.Background(), "opt", "26.3")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Warnings) != 1 || !strings.Contains(plan.Warnings[0], "unverträglich") {
		t.Errorf("warnings = %v", plan.Warnings)
	}
}

func TestPlanMarksAlreadyInstalledMain(t *testing.T) {
	mgr, _, _, _ := addSetup(t)
	plan, err := mgr.Plan(context.Background(), "fapi", "26.3")
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Main.Present[mods.ProfileServer] || plan.Main.Present[mods.ProfileClient] {
		t.Errorf("present = %v", plan.Main.Present)
	}
}

func TestAddModsStagesServerAndAppliesClient(t *testing.T) {
	mgr, api, serverDir, clientDir := addSetup(t)
	api.addVersion("balm", "Balm", "balm.jar", "required", "required")
	api.addVersion("way", "Waystones", "waystones.jar", "required", "required", req("balm"), req("fapi"))

	res, err := mgr.AddMods(context.Background(), "way", "26.3", []string{mods.ProfileServer, mods.ProfileClient})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(res.Added[mods.ProfileServer], ","); got != "Waystones,Balm" {
		t.Errorf("server added = %s (Fabric API ist dort schon)", got)
	}
	if got := strings.Join(res.Added[mods.ProfileClient], ","); got != "Waystones,Balm,Fabric API" {
		t.Errorf("client added = %s", got)
	}
	// Server: nur vorgemerkt, Live-Ordner unverändert
	if _, err := os.Stat(filepath.Join(serverDir, "waystones.jar")); err == nil {
		t.Error("Server-Mod liegt schon live — muss bis zum Neustart vorgemerkt bleiben")
	}
	pending := 0
	for _, e := range mgr.Entries(mods.ProfileServer) {
		if e.Pending == "add" {
			pending++
		}
	}
	if pending != 2 {
		t.Errorf("vorgemerkte Neuzugänge Server = %d, want 2", pending)
	}
	// Client: sofort im Paket
	for _, f := range []string{"waystones.jar", "balm.jar", "fabric-api.jar"} {
		if _, err := os.Stat(filepath.Join(clientDir, f)); err != nil {
			t.Errorf("client fehlt %s", f)
		}
	}
	if mgr.HasStaged(mods.ProfileClient) {
		t.Error("Client-Profil hat noch Vormerkungen — muss sofort angewendet sein")
	}
	// zweites Hinzufügen: alles schon da/vorgemerkt
	if _, err := mgr.AddMods(context.Background(), "way", "26.3", []string{mods.ProfileServer, mods.ProfileClient}); err == nil {
		t.Error("doppeltes Hinzufügen muss abgelehnt werden")
	}
}

func TestAddModsRejectsHashMismatch(t *testing.T) {
	mgr, api, _, clientDir := addSetup(t)
	v := api.addVersion("sodium", "Sodium", "sodium.jar", "required", "unsupported")
	api.files[v.Files[0].URL] = []byte("manipuliert")
	if _, err := mgr.AddMods(context.Background(), "sodium", "26.3", []string{mods.ProfileClient}); err == nil || !strings.Contains(err.Error(), "hash") {
		t.Fatalf("err = %v, want hash-Fehler", err)
	}
	if _, err := os.Stat(filepath.Join(clientDir, "sodium.jar")); err == nil {
		t.Error("manipulierte Datei im Live-Ordner")
	}
}

func TestSearchNeverNil(t *testing.T) {
	mgr, _, _, _ := addSetup(t)
	hits, err := mgr.Search(context.Background(), "gibtsnicht", "26.3")
	if err != nil || hits == nil {
		t.Errorf("hits = %v, err = %v", hits, err)
	}
}

func TestAddModsAtomicAcrossProfiles(t *testing.T) {
	mgr, api, serverDir, clientDir := addSetup(t)
	api.addVersion("balm", "Balm", "balm.jar", "required", "required")
	api.addVersion("way", "Waystones", "waystones.jar", "required", "required", req("balm"), req("fapi"))
	delete(api.files, "fake://fabric-api.jar") // nur der Client braucht Fabric API
	if _, err := mgr.AddMods(context.Background(), "way", "26.3", []string{mods.ProfileServer, mods.ProfileClient}); err == nil {
		t.Fatal("want download error")
	}
	if mgr.HasStaged(mods.ProfileServer) {
		t.Error("Server hat Vormerkungen trotz Fehler")
	}
	for _, d := range []string{serverDir, clientDir} {
		if _, err := os.Stat(filepath.Join(d, "waystones.jar")); err == nil {
			t.Errorf("waystones.jar liegt in %s", d)
		}
	}
}

func TestAddModsRejectsCorruptManifest(t *testing.T) {
	mgr, api, serverDir, _ := addSetup(t)
	api.addVersion("balm", "Balm", "balm.jar", "required", "required")
	sdir := filepath.Join(serverDir, ".msm-staging")
	if err := os.MkdirAll(sdir, 0o755); err != nil {
		t.Fatal(err)
	}
	mp := filepath.Join(sdir, "manifest.json")
	if err := os.WriteFile(mp, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.AddMods(context.Background(), "balm", "26.3", []string{mods.ProfileServer}); err == nil {
		t.Fatal("want error")
	}
	got, _ := os.ReadFile(mp)
	if string(got) != "{not json" {
		t.Errorf("manifest überschrieben: %q", got)
	}
}

func TestAddModsRejectsBadRemoteFilename(t *testing.T) {
	mgr, api, serverDir, _ := addSetup(t)
	api.addVersion("evil", "Evil", "../evil.jar", "required", "required")
	if _, err := mgr.AddMods(context.Background(), "evil", "26.3", []string{mods.ProfileServer}); err == nil {
		t.Fatal("want error")
	}
	for _, p := range []string{filepath.Join(serverDir, "..", "evil.jar"), filepath.Join(serverDir, ".msm-staging", "..", "evil.jar")} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s geschrieben", p)
		}
	}
}

func TestAddModsClientRefusesWhenOtherChangesPending(t *testing.T) {
	mgr, api, _, clientDir := addSetup(t)
	api.addVersion("sodium", "Sodium", "sodium.jar", "required", "unsupported")
	old := filepath.Join(clientDir, "old.jar")
	if err := os.WriteFile(old, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	sdir := filepath.Join(clientDir, ".msm-staging")
	if err := os.MkdirAll(sdir, 0o755); err != nil {
		t.Fatal(err)
	}
	man := `{"created":"2026-01-01T00:00:00Z","items":[{"category":"mods","oldFilename":"old.jar","newFilename":"","sha512":"","version":""}]}`
	if err := os.WriteFile(filepath.Join(sdir, "manifest.json"), []byte(man), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.AddMods(context.Background(), "sodium", "26.3", []string{mods.ProfileClient}); err == nil {
		t.Fatal("want error")
	}
	if _, err := os.Stat(old); err != nil {
		t.Error("old.jar wurde entfernt")
	}
	if _, err := os.Stat(filepath.Join(clientDir, "sodium.jar")); err == nil {
		t.Error("sodium.jar angewendet")
	}
}

type manifestFile struct {
	Items []struct {
		OldFilename string `json:"oldFilename"`
		NewFilename string `json:"newFilename"`
	} `json:"items"`
}

func readStaged(t *testing.T, modsDir string) manifestFile {
	t.Helper()
	var man manifestFile
	b, err := os.ReadFile(filepath.Join(modsDir, ".msm-staging", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &man); err != nil {
		t.Fatal(err)
	}
	return man
}

// Während AddMods lädt, wird die Staging-Liste anderweitig geändert
// (Anwenden, weitere Vormerkung) — AddMods darf nichts davon überschreiben.
func TestAddModsDoesNotClobberConcurrentStagingChanges(t *testing.T) {
	mgr, api, serverDir, _ := addSetup(t)
	api.addVersion("way", "Waystones", "waystones.jar", "required", "required")
	ctx := context.Background()
	if err := mgr.StageRemove(mods.ProfileServer, "fabric-api.jar"); err != nil {
		t.Fatal(err)
	}
	ran := false
	api.onDownload = func() {
		if ran {
			return
		}
		ran = true
		// angewendet während des Downloads → darf nicht wiederauferstehen
		if _, _, err := mgr.ApplyStaged(mods.ProfileServer); err != nil {
			t.Error(err)
		}
		// neu vorgemerkt während des Downloads → darf nicht verloren gehen
		if _, err := mgr.StageUpload(ctx, mods.ProfileServer, "mods", "custom.jar", []byte("eigen")); err != nil {
			t.Error(err)
		}
	}
	if _, err := mgr.AddMods(ctx, "way", "26.3", []string{mods.ProfileServer}); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, it := range readStaged(t, serverDir).Items {
		if it.OldFilename == "fabric-api.jar" {
			t.Error("angewendete Entfernung wieder in der Staging-Liste")
		}
		got[it.NewFilename] = true
	}
	if !got["waystones.jar"] || !got["custom.jar"] {
		t.Errorf("staging-liste = %v, want waystones.jar + custom.jar", got)
	}
}

// Dieselbe Datei schon vorgemerkt (z. B. per Upload): AddMods lehnt ab.
func TestAddModsRefusesStagedFilenameCollision(t *testing.T) {
	mgr, api, _, _ := addSetup(t)
	api.addVersion("way", "Waystones", "waystones.jar", "required", "required")
	ctx := context.Background()
	if _, err := mgr.StageUpload(ctx, mods.ProfileServer, "mods", "waystones.jar", []byte("eigener build")); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.AddMods(ctx, "way", "26.3", []string{mods.ProfileServer}); err == nil {
		t.Error("gleicher Dateiname schon vorgemerkt — kein Fehler")
	}
}

// Die Mod-Liste zeigt den Projekttitel, nicht den Versionsnamen (bei
// manchen Mods nur eine Nummer, WSL-Test 2026-11-09).
func TestCheckUpdatesUsesProjectTitles(t *testing.T) {
	mgr, _, _, _ := addSetup(t)
	entries, err := mgr.CheckUpdates(context.Background(), mods.ProfileServer, "26.3")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "Fabric API" {
		t.Errorf("entries = %+v, want Name \"Fabric API\" (Projekttitel statt \"Fabric API 1.0\")", entries)
	}
}
