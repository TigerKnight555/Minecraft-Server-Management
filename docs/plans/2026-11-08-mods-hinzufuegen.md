# Mods hinzufügen & entfernen – Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Mods im MSM-Dashboard per Modrinth-Suche oder Datei-Upload zum Server- und/oder Client-Profil hinzufügen und wieder entfernen — sicher über den bestehenden Staging-/Backup-/Rollback-Weg, mit Discord-Info an die Spieler nach dem Neustart.

**Architecture:** Der vorhandene Staging-Mechanismus (`internal/mods`) lernt zwei neue Fälle: Item ohne `OldFilename` = Hinzufügen, ohne `NewFilename` = Entfernen. Server-Änderungen bleiben vorgemerkt bis zum nächsten Neustart (Routine mit „Vorgemerkte Updates anwenden" oder Button „Anwenden + Neustart", jetzt mit 5-min-Vorwarnung über den Scheduler); Client-Änderungen werden sofort angewendet. Ein neues Paket `internal/modnews` sammelt angewendete Änderungen über einen Hook am `mods.Manager`, erkennt den nächsten Neustart (offline → online) und postet dann die Mod-Liste und ggf. das neue Client-Paket.

**Tech Stack:** Go 1.26 (stdlib, `robfig/cron/v3`), Svelte 5 (Runes), Modrinth API v2, SQLite app_state (über `kvGet`/`kvSet`).

**Spec:** `docs/specs/2026-11-08-mods-hinzufuegen-design.md` (Vault-Original: „MSM – Mods hinzufügen & entfernen").

## Global Constraints

- Gitflow: Arbeit auf `feature/mods-add-remove`, abgezweigt von `feature/upgrade-fixes` (enthält die Dropbox-/Settings-Fixes, auf denen dieses Feature aufsetzt). Commit-Messages englisch im Stil `feat:`/`fix:`/`test:`/`docs:`, Endzeile `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Commits nur nach Freigabe durch den Nutzer (Projektregel). Die Commit-Schritte unten sind die geplanten Schnitte.
- Nutzer-sichtbare Texte (UI, Discord, Fehlermeldungen) auf Deutsch, spielertauglich; Code-Bezeichner englisch, Kommentare wie im Bestand deutsch/englisch gemischt.
- Datenintegritäts-Invarianten aus `internal/mods/mods.go` gelten weiter: Live-Ordner nur beim expliziten Anwenden anfassen; ersetzte/entfernte Dateien wandern nach `.backup/<label>/`, nie löschen; heruntergeladene Dateien SHA-512-geprüft.
- Upload: nur `.jar` (Kategorie `mods`) bzw. `.zip` (`shaderpacks`, `resourcepacks`), max. 100 MB (`100 << 20` Bytes), Dateiname ohne Pfad, ohne `..`, ohne führenden Punkt.
- Abhängigkeiten: rekursiv höchstens 3 Ebenen.
- „Anwenden + Neustart": 5 min Vorwarnung, `save-all`, Watchdog 10 min.
- Modrinth-Query-Parameter mit JSON-Arrays immer über `url.Values` encodieren (Learning 14).
- Go-Tests: `go test ./...` muss am Ende jeder Task grün sein. Web: `cd web && npm run build` muss bauen.
- Doku-Pflicht: jeder abgeschlossene Schritt wird im Obsidian-Vault (`10-Projekte/Software/Minecraft-Server-Manager/`) nachgezogen (Task 11).

## File Structure

| Datei | Aktion | Verantwortung |
|---|---|---|
| `internal/modrinth/client.go` | ändern | `Dependency`, `Project`, `SearchHit`; `Search`, `Project`, `ProjectVersions`; `ProjectSupports` nutzt `ProjectVersions` |
| `internal/modrinth/client_add_test.go` | neu | Tests der neuen Abfragen gegen httptest-Server |
| `internal/mock/modrinth.go` | ändern | Mock-Katalog (`jade-mock` → braucht `balm-mock`) für Mock-Modus und API-Tests |
| `internal/mods/mods.go` | ändern | Interface-Erweiterung, `Entry.Pending`, `Change` + Hook, Staging-Semantik Hinzufügen/Entfernen in `ApplyStaged`/`restoreSet`, `Entries` mit vorgemerkten Zeilen, `Discard`, `HasStaged` |
| `internal/mods/staging_test.go` | neu | interne Tests der neuen Staging-Semantik |
| `internal/mods/add.go` | neu | `Search`, `Plan`, `AddMods`, `inventory` |
| `internal/mods/add_test.go` | neu | Fake-API + Tests für Plan/AddMods |
| `internal/mods/remove.go` | neu | `RemovalInfo`, `StageRemove`, `StageUpload` |
| `internal/mods/remove_test.go` | neu | Tests Entfernen/Upload |
| `internal/events/events.go` | ändern | `TypeModsChanged = "mods.changed"` |
| `internal/modnews/modnews.go` | neu | Änderungen sammeln, Neustart erkennen, Discord-Meldung, Paket-Versand |
| `internal/modnews/modnews_test.go` | neu | Tests |
| `internal/scheduler/scheduler.go` | ändern | `run` aus `RunNow` herauslösen, `RunAdhoc`, `NextApply` |
| `internal/scheduler/adhoc_test.go` | neu | Tests |
| `internal/upgrade/upgrade.go` | ändern | `PendingChanges`-Sperre |
| `internal/upgrade/upgrade_test.go` | ändern | Test der Sperre |
| `internal/api/modsadd.go` | neu | Handler search/plan/add/upload/remove-info/remove/discard/apply-info |
| `internal/api/mods.go` | ändern | Apply mit angekündigtem Neustart, Publish über `publishPack` |
| `internal/api/api.go` | ändern | Routen, `Deps.PublishPack`, `mcVersion` nil-sicher |
| `internal/api/modsadd_test.go` | neu | API-Tests |
| `cmd/msm/main.go` | ändern | Verdrahtung modnews/publishPack/PendingChanges |
| `.env.example` | ändern | Event-Filter-Beispiel um `mods.changed`, `mods.clientpack` |
| `web/src/lib/stream.js` | ändern | neue API-Aufrufe |
| `web/src/lib/AddMod.svelte` | neu | Suche, Dialog, Upload |
| `web/src/lib/Mods.svelte` | ändern | AddMod einbinden, Badges, Entfernen/Verwerfen, neuer Apply-Text |

---

### Task 0: Ausgangsbasis herstellen

**Files:** keine Code-Änderung.

- [ ] **Step 1: Offene Dropbox-/Settings-Fixes auf `feature/upgrade-fixes` committen** (nach Nutzer-Freigabe)

```bash
cd /c/Entwicklung/GIT/Minecraft-Server-Management
git status -s   # erwartet: internal/api/settings.go, internal/api/settings_test.go, internal/dropbox/*, web/src/lib/Settings.svelte, docs/
git add internal/api/settings.go internal/api/settings_test.go internal/dropbox/dropbox.go internal/dropbox/dropbox_test.go internal/dropbox/publish.go web/src/lib/Settings.svelte
git commit -m "fix: settings save no longer blocked by unchanged .env webhook; clear Dropbox missing_scope hint

- only changed fields are saved (no .env values copied into the DB)
- accept discordapp.com / ptb / canary webhook URLs
- explain missing_scope with the steps to fix it
- guide: sharing.read, scope check, curl.exe hint
- close pipe reader on upload error (zip goroutine leak)

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

- [ ] **Step 2: Feature-Branch anlegen**

```bash
git checkout -b feature/mods-add-remove
git add docs/specs/2026-11-08-mods-hinzufuegen-design.md docs/plans/2026-11-08-mods-hinzufuegen.md
git commit -m "docs: spec and plan for adding/removing mods

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
go test ./...
```
Expected: alle Pakete `ok`.

---

### Task 1: Modrinth-Client – Suche, Projekt, Versionen, Abhängigkeiten

**Files:**
- Modify: `internal/modrinth/client.go`
- Create: `internal/modrinth/client_add_test.go`
- Modify: `internal/mods/mods.go:34-41` (Interface `ModrinthAPI`)
- Modify: `internal/mock/modrinth.go`

**Interfaces:**
- Produces:
  - `type modrinth.Dependency struct { VersionID, ProjectID, DependencyType string }` (JSON `version_id`, `project_id`, `dependency_type`)
  - `modrinth.Version.Dependencies []Dependency`
  - `type modrinth.Project struct { ID, Slug, Title, Description, IconURL, ClientSide, ServerSide, ProjectType string }`
  - `type modrinth.SearchHit struct { ProjectID, Slug, Title, Description, IconURL string; Downloads int64; ClientSide, ServerSide string }` (JSON snake_case wie Modrinth)
  - `(*modrinth.Client).Search(ctx, query, loader, mcVersion string, limit int) ([]SearchHit, error)`
  - `(*modrinth.Client).Project(ctx, id string) (Project, error)`
  - `(*modrinth.Client).ProjectVersions(ctx, projectID, loader, mcVersion string) ([]Version, error)` — neueste zuerst (Modrinth-Reihenfolge)
  - `mods.ModrinthAPI` bekommt dieselben drei Methoden.

- [ ] **Step 1: Failing Tests schreiben** — `internal/modrinth/client_add_test.go`

```go
package modrinth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSearchSendsEncodedFacets(t *testing.T) {
	var gotQuery, gotFacets string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			http.Error(w, "path "+r.URL.Path, http.StatusNotFound)
			return
		}
		gotQuery = r.URL.Query().Get("query")
		gotFacets = r.URL.Query().Get("facets")
		w.Write([]byte(`{"hits":[{"project_id":"P1","title":"Jade","client_side":"required","server_side":"optional","downloads":5}]}`))
	}))
	defer srv.Close()

	hits, err := NewWithBase(srv.URL).Search(context.Background(), "jade", "fabric", "26.3", 20)
	if err != nil {
		t.Fatal(err)
	}
	if gotQuery != "jade" {
		t.Errorf("query = %q", gotQuery)
	}
	if want := `[["project_type:mod"],["categories:fabric"],["versions:26.3"]]`; gotFacets != want {
		t.Errorf("facets = %q, want %q", gotFacets, want)
	}
	if len(hits) != 1 || hits[0].ProjectID != "P1" || hits[0].ClientSide != "required" || hits[0].Downloads != 5 {
		t.Errorf("hits = %+v", hits)
	}
}

func TestProjectAndVersionsWithDependencies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/project/P1":
			w.Write([]byte(`{"id":"P1","title":"Waystones","client_side":"required","server_side":"required","project_type":"mod"}`))
		case "/project/P1/version":
			if r.URL.Query().Get("loaders") != `["fabric"]` || r.URL.Query().Get("game_versions") != `["26.3"]` {
				http.Error(w, "bad filter", http.StatusBadRequest)
				return
			}
			w.Write([]byte(`[{"id":"V1","project_id":"P1","version_number":"21.1","files":[{"url":"u","filename":"waystones.jar","primary":true,"hashes":{"sha512":"abc"}}],"dependencies":[{"project_id":"BALM","dependency_type":"required"}]}]`))
		default:
			http.Error(w, "path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := NewWithBase(srv.URL)

	p, err := c.Project(context.Background(), "P1")
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "Waystones" || p.ServerSide != "required" {
		t.Errorf("project = %+v", p)
	}
	vs, err := c.ProjectVersions(context.Background(), "P1", "fabric", "26.3")
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || len(vs[0].Dependencies) != 1 || vs[0].Dependencies[0].ProjectID != "BALM" || vs[0].Dependencies[0].DependencyType != "required" {
		t.Errorf("versions = %+v", vs)
	}
	ok, err := c.ProjectSupports(context.Background(), "P1", "fabric", "26.3")
	if err != nil || !ok {
		t.Errorf("ProjectSupports = %v, %v", ok, err)
	}
}
```

- [ ] **Step 2: Test laufen lassen, muss scheitern**

Run: `go test ./internal/modrinth/ -run 'Search|ProjectAndVersions'`
Expected: Compile-Fehler `c.Search undefined` / `Dependencies undefined`.

- [ ] **Step 3: Implementierung in `internal/modrinth/client.go`**

Import `strconv` ergänzen. In `Version` nach `Files []File ...` einfügen:

```go
	Dependencies  []Dependency `json:"dependencies"`
```

Nach dem `File`-Typ einfügen:

```go
// Dependency is one dependency of a version. DependencyType: required |
// optional | incompatible | embedded.
type Dependency struct {
	VersionID      string `json:"version_id"`
	ProjectID      string `json:"project_id"`
	DependencyType string `json:"dependency_type"`
}

// Project is the subset of /project/{id} MSM needs. ClientSide/ServerSide:
// required | optional | unsupported | unknown.
type Project struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description"`
	IconURL     string `json:"icon_url"`
	ClientSide  string `json:"client_side"`
	ServerSide  string `json:"server_side"`
	ProjectType string `json:"project_type"`
}

// SearchHit is one result of /search.
type SearchHit struct {
	ProjectID   string `json:"project_id"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description"`
	IconURL     string `json:"icon_url"`
	Downloads   int64  `json:"downloads"`
	ClientSide  string `json:"client_side"`
	ServerSide  string `json:"server_side"`
}

// Search finds mods with a build for loader + MC version. Facetten sind
// JSON im Query-String und müssen encodiert werden (Learning 14).
func (c *Client) Search(ctx context.Context, query, loader, mcVersion string, limit int) ([]SearchHit, error) {
	q := url.Values{}
	q.Set("query", query)
	q.Set("facets", fmt.Sprintf(`[["project_type:mod"],["categories:%s"],["versions:%s"]]`, loader, mcVersion))
	q.Set("limit", strconv.Itoa(limit))
	var out struct {
		Hits []SearchHit `json:"hits"`
	}
	if err := c.do(ctx, http.MethodGet, "/search?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return out.Hits, nil
}

// Project fetches one project (ID oder Slug).
func (c *Client) Project(ctx context.Context, id string) (Project, error) {
	var p Project
	err := c.do(ctx, http.MethodGet, "/project/"+url.PathEscape(id), nil, &p)
	return p, err
}

// ProjectVersions lists a project's versions for loader + MC version,
// newest first (Modrinth-Reihenfolge).
func (c *Client) ProjectVersions(ctx context.Context, projectID, loader, mcVersion string) ([]Version, error) {
	q := url.Values{}
	q.Set("loaders", fmt.Sprintf(`["%s"]`, loader))
	q.Set("game_versions", fmt.Sprintf(`["%s"]`, mcVersion))
	path := "/project/" + url.PathEscape(projectID) + "/version?" + q.Encode()
	var versions []Version
	if err := c.do(ctx, http.MethodGet, path, nil, &versions); err != nil {
		return nil, err
	}
	return versions, nil
}
```

`ProjectSupports` ersetzen durch:

```go
// ProjectSupports reports whether a project has any version for the given
// loader + MC version (readiness check for the version watch).
func (c *Client) ProjectSupports(ctx context.Context, projectID, loader, mcVersion string) (bool, error) {
	versions, err := c.ProjectVersions(ctx, projectID, loader, mcVersion)
	if err != nil {
		return false, err
	}
	return len(versions) > 0, nil
}
```

- [ ] **Step 4: Interface erweitern** — `internal/mods/mods.go`, `ModrinthAPI`:

```go
type ModrinthAPI interface {
	LatestVersions(ctx context.Context, sha512s []string, loader, mcVersion string) (map[string]modrinth.Version, error)
	VersionsByHash(ctx context.Context, sha512s []string) (map[string]modrinth.Version, error)
	ProjectSupports(ctx context.Context, projectID, loader, mcVersion string) (bool, error)
	Download(ctx context.Context, url string) ([]byte, error)
	Search(ctx context.Context, query, loader, mcVersion string, limit int) ([]modrinth.SearchHit, error)
	Project(ctx context.Context, id string) (modrinth.Project, error)
	ProjectVersions(ctx context.Context, projectID, loader, mcVersion string) ([]modrinth.Version, error)
}
```

- [ ] **Step 5: Mock-Katalog** — `internal/mock/modrinth.go`, Import `strings` ergänzen, ans Dateiende vor `CreateFakeProfiles` einfügen:

```go
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
```

- [ ] **Step 6: Tests laufen lassen**

Run: `go test ./internal/modrinth/ ./internal/mods/ ./internal/mock/ && go vet ./...`
Expected: `ok` (alle bisherigen mods-Tests weiter grün, `supportsRecorder` bettet `*mock.Modrinth` ein und erfüllt das Interface).

- [ ] **Step 7: Commit**

```bash
git add internal/modrinth internal/mods/mods.go internal/mock/modrinth.go
git commit -m "feat: Modrinth search, project and version queries with dependencies

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Staging-Semantik Hinzufügen/Entfernen, Change-Hook, Verwerfen

**Files:**
- Modify: `internal/mods/mods.go` (Entry, stagingItem, Entries, ApplyStaged, Rollback, RollbackLabel, restoreSet)
- Create: `internal/mods/staging_test.go`

**Interfaces:**
- Consumes: nichts aus Task 1 außer dem Paket.
- Produces:
  - `Entry.Pending string` (JSON `pending`, Werte `"add" | "remove" | "update"`, leer = nichts vorgemerkt)
  - `stagingItem` neue Felder `ProjectID`, `Name`, `ClientSide` (JSON `projectId`, `name`, `clientSide`)
  - `func (it stagingItem) action() string` → `"add" | "remove" | "update"`
  - `type Change struct { Action, Profile, Filename, ProjectID, Name, ClientSide string }` (JSON `action`, `profile`, `filename`, `projectId`, `name`, `clientSide`)
  - `func (m *Manager) SetChangeHook(f func(profile string, changes []Change))` — aufgerufen nach jedem erfolgreichen `ApplyStaged` (mit allen angewendeten Items) und nach jedem erfolgreichen Rollback (mit `nil`)
  - `func (m *Manager) Discard(profile, filename string) error`
  - `func (m *Manager) HasStaged(profile string) bool`
  - `func (m *Manager) stagedItems(p Profile) []stagingItem` (intern, für Task 3/4)
  - Konstanten `ProfileServer = "server"`, `ProfileClient = "client"`

- [ ] **Step 1: Failing Tests** — `internal/mods/staging_test.go`

```go
package mods

import (
	"os"
	"path/filepath"
	"testing"
)

func testProfile(t *testing.T) (*Manager, Profile, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "mods")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := Profile{Name: ProfileServer, Dirs: map[string]string{"mods": dir}}
	return NewManager(nil, "fabric", []Profile{p}), p, dir
}

func stage(t *testing.T, p Profile, files map[string]string, items ...stagingItem) {
	t.Helper()
	sdir := stagingDir(p)
	if err := os.MkdirAll(sdir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(sdir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeManifest(filepath.Join(sdir, "manifest.json"), stagingManifest{Items: items}); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

func TestApplyAndRollbackAddItem(t *testing.T) {
	m, p, dir := testProfile(t)
	stage(t, p, map[string]string{"jade-1.0.jar": "jade"},
		stagingItem{Category: "mods", NewFilename: "jade-1.0.jar", ProjectID: "P1", Name: "Jade", ClientSide: "required"})
	var got []Change
	m.SetChangeHook(func(profile string, c []Change) { got = append(got, c...) })

	label, n, err := m.ApplyStaged(ProfileServer)
	if err != nil || n != 1 {
		t.Fatalf("apply = %d, %v", n, err)
	}
	if !exists(filepath.Join(dir, "jade-1.0.jar")) {
		t.Fatal("hinzugefügte Datei fehlt im Live-Ordner")
	}
	if len(got) != 1 || got[0].Action != "add" || got[0].Name != "Jade" || got[0].Profile != ProfileServer || got[0].ClientSide != "required" {
		t.Errorf("hook = %+v", got)
	}

	restored, err := m.RollbackLabel(ProfileServer, label)
	if err != nil || restored != 1 {
		t.Fatalf("rollback = %d, %v", restored, err)
	}
	if exists(filepath.Join(dir, "jade-1.0.jar")) {
		t.Error("Datei nach Rollback noch im Live-Ordner")
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatal("Live-Ordner beim Rollback beschädigt (leerer Dateiname!)")
	}
}

func TestApplyAndRollbackRemoveItem(t *testing.T) {
	m, p, dir := testProfile(t)
	if err := os.WriteFile(filepath.Join(dir, "old.jar"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	stage(t, p, nil, stagingItem{Category: "mods", OldFilename: "old.jar", Name: "Old Mod"})
	var got []Change
	m.SetChangeHook(func(profile string, c []Change) { got = append(got, c...) })

	label, n, err := m.ApplyStaged(ProfileServer)
	if err != nil || n != 1 {
		t.Fatalf("apply = %d, %v", n, err)
	}
	if exists(filepath.Join(dir, "old.jar")) {
		t.Fatal("entfernte Datei noch im Live-Ordner")
	}
	if !exists(filepath.Join(dir, ".backup", label, "old.jar")) {
		t.Fatal("entfernte Datei nicht im Backup-Satz")
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatal("Live-Ordner beschädigt (leerer Dateiname!)")
	}
	if len(got) != 1 || got[0].Action != "remove" || got[0].Filename != "old.jar" || got[0].Name != "Old Mod" {
		t.Errorf("hook = %+v", got)
	}

	if _, err := m.RollbackLabel(ProfileServer, label); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "old.jar")); err != nil || string(b) != "old" {
		t.Error("entfernte Datei nach Rollback nicht zurück")
	}
}

func TestRollbackNotifiesHook(t *testing.T) {
	m, p, _ := testProfile(t)
	stage(t, p, map[string]string{"a.jar": "a"}, stagingItem{Category: "mods", NewFilename: "a.jar"})
	if _, _, err := m.ApplyStaged(ProfileServer); err != nil {
		t.Fatal(err)
	}
	calls := 0
	m.SetChangeHook(func(string, []Change) { calls++ })
	if _, err := m.Rollback(ProfileServer); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("hook calls nach Rollback = %d, want 1", calls)
	}
}

func TestEntriesShowPendingAddAndRemove(t *testing.T) {
	m, p, dir := testProfile(t)
	os.WriteFile(filepath.Join(dir, "keep.jar"), []byte("k"), 0o644)
	os.WriteFile(filepath.Join(dir, "gone.jar"), []byte("g"), 0o644)
	m.entries[ProfileServer] = []Entry{
		{Profile: ProfileServer, Category: "mods", Filename: "gone.jar", Name: "Gone"},
		{Profile: ProfileServer, Category: "mods", Filename: "keep.jar", Name: "Keep"},
	}
	stage(t, p, map[string]string{"new.jar": "n"},
		stagingItem{Category: "mods", NewFilename: "new.jar", ProjectID: "P9", Name: "Neu", Version: "2.0"},
		stagingItem{Category: "mods", OldFilename: "gone.jar", Name: "Gone"})

	byFile := map[string]Entry{}
	for _, e := range m.Entries(ProfileServer) {
		byFile[e.Filename] = e
	}
	if e := byFile["new.jar"]; e.Pending != "add" || !e.Staged || e.Name != "Neu" || !e.Managed {
		t.Errorf("new.jar = %+v", e)
	}
	if e := byFile["gone.jar"]; e.Pending != "remove" || !e.Staged {
		t.Errorf("gone.jar = %+v", e)
	}
	if e := byFile["keep.jar"]; e.Pending != "" || e.Staged {
		t.Errorf("keep.jar = %+v", e)
	}

	// nach dem Anwenden: Cache gepflegt, nichts mehr vorgemerkt
	if _, _, err := m.ApplyStaged(ProfileServer); err != nil {
		t.Fatal(err)
	}
	byFile = map[string]Entry{}
	for _, e := range m.Entries(ProfileServer) {
		byFile[e.Filename] = e
	}
	if _, ok := byFile["gone.jar"]; ok {
		t.Error("entfernter Mod steht noch in der Liste")
	}
	if e, ok := byFile["new.jar"]; !ok || e.Pending != "" || e.Name != "Neu" {
		t.Errorf("neuer Mod nach Anwenden = %+v (ok=%v)", e, ok)
	}
}

func TestDiscardAndHasStaged(t *testing.T) {
	m, p, dir := testProfile(t)
	os.WriteFile(filepath.Join(dir, "gone.jar"), []byte("g"), 0o644)
	stage(t, p, map[string]string{"new.jar": "n"},
		stagingItem{Category: "mods", NewFilename: "new.jar"},
		stagingItem{Category: "mods", OldFilename: "gone.jar"})
	if !m.HasStaged(ProfileServer) {
		t.Fatal("HasStaged = false")
	}
	if err := m.Discard(ProfileServer, "new.jar"); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(stagingDir(p), "new.jar")) {
		t.Error("verworfene Datei liegt noch im Staging")
	}
	if err := m.Discard(ProfileServer, "gone.jar"); err != nil {
		t.Fatal(err)
	}
	if m.HasStaged(ProfileServer) {
		t.Error("HasStaged nach dem Verwerfen aller Einträge = true")
	}
	if !exists(filepath.Join(dir, "gone.jar")) {
		t.Error("Verwerfen einer Entfernung darf den Live-Ordner nicht anfassen")
	}
	if err := m.Discard(ProfileServer, "nix.jar"); err == nil {
		t.Error("Verwerfen ohne Vormerkung muss fehlschlagen")
	}
}
```

- [ ] **Step 2: Laufen lassen, muss scheitern**

Run: `go test ./internal/mods/ -run 'AddItem|RemoveItem|NotifiesHook|Pending|Discard'`
Expected: Compile-Fehler (`ProfileServer`, `Change`, `SetChangeHook`, `Pending`, `Discard`, `HasStaged` undefined).

- [ ] **Step 3: Implementierung** in `internal/mods/mods.go`

Nach `var ErrNothingStaged ...` einfügen:

```go
// Profilnamen — Server = Live-Mods des Minecraft-Servers, Client = Paket
// für die Spieler.
const (
	ProfileServer = "server"
	ProfileClient = "client"
)
```

In `Entry` nach `Staged bool ...` einfügen:

```go
	// Pending: was vorgemerkt ist — "add" (neu), "remove" (wird entfernt),
	// "update" (gestagtes Update); leer = nichts vorgemerkt
	Pending string `json:"pending,omitempty"`
```

In `Manager` nach `entries map[string][]Entry ...` einfügen:

```go
	// onChange meldet angewendete Änderungen (Discord-Neuigkeiten, Paket
	// veraltet); nil = aus
	onChange func(profile string, changes []Change)
```

Nach `func (m *Manager) Profiles()` einfügen:

```go
// Change describes one applied staging item — Grundlage für die
// Discord-Meldung nach dem Neustart.
type Change struct {
	Action     string `json:"action"` // "add" | "remove" | "update"
	Profile    string `json:"profile"`
	Filename   string `json:"filename"`
	ProjectID  string `json:"projectId,omitempty"`
	Name       string `json:"name"`
	ClientSide string `json:"clientSide,omitempty"`
}

// SetChangeHook registers f; called after every successful ApplyStaged
// (alle angewendeten Items) and every successful rollback (changes = nil).
func (m *Manager) SetChangeHook(f func(profile string, changes []Change)) { m.onChange = f }

func (m *Manager) notify(profile string, changes []Change) {
	if m.onChange != nil {
		m.onChange(profile, changes)
	}
}
```

`Entries` komplett ersetzen:

```go
// Entries returns the cached result of the last update check plus pending
// staging state: Vormerkungen werden bei jedem Aufruf aus dem Manifest
// gelesen (kein Modrinth-Aufruf nötig), vorgemerkte Neuzugänge erscheinen
// als eigene Zeilen.
func (m *Manager) Entries(profile string) []Entry {
	m.mu.Lock()
	out := append([]Entry(nil), m.entries[profile]...)
	m.mu.Unlock()
	p, err := m.profile(profile)
	if err != nil {
		return out
	}
	items := m.stagedItems(p)
	byOld := map[string]stagingItem{}
	for _, it := range items {
		if it.OldFilename != "" {
			byOld[it.OldFilename] = it
		}
	}
	for i := range out {
		it, ok := byOld[out[i].Filename]
		out[i].Staged = ok
		out[i].Pending = ""
		if ok {
			out[i].Pending = it.action()
		}
	}
	for _, it := range items {
		if it.action() == "add" {
			out = append(out, Entry{
				Profile: profile, Category: it.Category, Filename: it.NewFilename,
				SHA512: it.SHA512, Managed: it.ProjectID != "", ProjectID: it.ProjectID,
				Name: it.Name, Version: it.Version, Staged: true, Pending: "add",
			})
		}
	}
	return out
}
```

`stagingItem` ersetzen und Hilfsmethoden ergänzen:

```go
// stagingItem: OldFilename leer = Hinzufügen, NewFilename leer = Entfernen,
// beide gesetzt = Update.
type stagingItem struct {
	Category    string `json:"category"`
	OldFilename string `json:"oldFilename"`
	NewFilename string `json:"newFilename"`
	SHA512      string `json:"sha512"`
	Version     string `json:"version"`
	ProjectID   string `json:"projectId,omitempty"`
	Name        string `json:"name,omitempty"`
	ClientSide  string `json:"clientSide,omitempty"`
}

func (it stagingItem) action() string {
	switch {
	case it.OldFilename == "":
		return "add"
	case it.NewFilename == "":
		return "remove"
	}
	return "update"
}

func (it stagingItem) change(profile string) Change {
	file := it.NewFilename
	if it.action() == "remove" {
		file = it.OldFilename
	}
	name := it.Name
	if name == "" {
		name = file
	}
	return Change{Action: it.action(), Profile: profile, Filename: file,
		ProjectID: it.ProjectID, Name: name, ClientSide: it.ClientSide}
}
```

`stagedFilenames` ersetzen durch:

```go
func (m *Manager) stagedItems(p Profile) []stagingItem {
	man, err := readManifest(filepath.Join(stagingDir(p), "manifest.json"))
	if err != nil {
		return nil
	}
	return man.Items
}

func (m *Manager) stagedFilenames(p Profile) map[string]bool {
	out := map[string]bool{}
	for _, it := range m.stagedItems(p) {
		if it.OldFilename != "" {
			out[it.OldFilename] = true
		}
	}
	return out
}

// HasStaged reports whether anything is pending for the profile.
func (m *Manager) HasStaged(profile string) bool {
	p, err := m.profile(profile)
	if err != nil {
		return false
	}
	return len(m.stagedItems(p)) > 0
}

// Discard drops the pending change for filename (vorgemerktes Hinzufügen,
// Entfernen oder Update). Der Live-Ordner bleibt unangetastet.
func (m *Manager) Discard(profile, filename string) error {
	p, err := m.profile(profile)
	if err != nil {
		return err
	}
	sdir := stagingDir(p)
	manPath := filepath.Join(sdir, "manifest.json")
	man, err := readManifest(manPath)
	if err != nil {
		return fmt.Errorf("für %s ist nichts vorgemerkt", filename)
	}
	kept := man.Items[:0]
	found := false
	for _, it := range man.Items {
		if filename != "" && (it.OldFilename == filename || (it.OldFilename == "" && it.NewFilename == filename)) {
			found = true
			if it.NewFilename != "" {
				os.Remove(filepath.Join(sdir, it.NewFilename))
			}
			continue
		}
		kept = append(kept, it)
	}
	if !found {
		return fmt.Errorf("für %s ist nichts vorgemerkt", filename)
	}
	if len(kept) == 0 {
		return os.Remove(manPath)
	}
	man.Items = kept
	return writeManifest(manPath, man)
}
```

`ApplyStaged` komplett ersetzen:

```go
// ApplyStaged swaps staged files into the live directory: old/removed files
// move to .backup/<timestamp>/ (never deleted), new files move in. Returns
// the backup label for rollback. Call while the server is stopped or right
// before a scheduled restart.
func (m *Manager) ApplyStaged(profileName string) (string, int, error) {
	p, err := m.profile(profileName)
	if err != nil {
		return "", 0, err
	}
	sdir := stagingDir(p)
	manPath := filepath.Join(sdir, "manifest.json")
	man, err := readManifest(manPath)
	if err != nil || len(man.Items) == 0 {
		return "", 0, fmt.Errorf("%w für Profil %s", ErrNothingStaged, profileName)
	}

	label := time.Now().Format("2006-01-02_15-04-05")
	applied := 0
	backupMan := stagingManifest{Created: time.Now()}
	var changes []Change
	for _, it := range man.Items {
		liveDir, ok := p.Dirs[it.Category]
		if !ok {
			continue
		}
		backupDir := filepath.Join(liveDir, ".backup", label)
		if err := os.MkdirAll(backupDir, 0o755); err != nil {
			return label, applied, err
		}
		// Leerer Name: filepath.Join(liveDir, "") wäre liveDir selbst —
		// jede Seite nur anfassen, wenn sie gesetzt ist.
		if it.OldFilename != "" {
			oldPath := filepath.Join(liveDir, it.OldFilename)
			if _, err := os.Stat(oldPath); err == nil {
				if err := os.Rename(oldPath, filepath.Join(backupDir, it.OldFilename)); err != nil {
					return label, applied, fmt.Errorf("backup von %s: %w", it.OldFilename, err)
				}
			}
		}
		if it.NewFilename != "" {
			if err := os.Rename(filepath.Join(sdir, it.NewFilename), filepath.Join(liveDir, it.NewFilename)); err != nil {
				return label, applied, fmt.Errorf("einsetzen von %s: %w", it.NewFilename, err)
			}
		}
		backupMan.Items = append(backupMan.Items, it)
		applied++
		// rollback needs to know where the backup lives
		writeManifest(filepath.Join(backupDir, "manifest.json"), backupMan)
		changes = append(changes, it.change(profileName))
	}
	os.Remove(manPath)
	m.updateCache(profileName, man.Items)
	m.notify(profileName, changes)
	return label, applied, nil
}

// updateCache pflegt den Check-Cache nach dem Anwenden, statt ihn zu
// verwerfen — übrige Update-Badges bleiben stehen (UX-Feedback vom ersten
// echten Update).
func (m *Manager) updateCache(profileName string, items []stagingItem) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entries, ok := m.entries[profileName]
	if !ok {
		return // noch kein Check gelaufen
	}
	for _, it := range items {
		switch it.action() {
		case "add":
			entries = append(entries, Entry{
				Profile: profileName, Category: it.Category, Filename: it.NewFilename,
				SHA512: it.SHA512, Managed: it.ProjectID != "", ProjectID: it.ProjectID,
				Name: it.Name, Version: it.Version,
			})
		case "remove":
			kept := entries[:0]
			for _, e := range entries {
				if !(e.Filename == it.OldFilename && e.Category == it.Category) {
					kept = append(kept, e)
				}
			}
			entries = kept
		default:
			for i := range entries {
				if entries[i].Filename == it.OldFilename && entries[i].Category == it.Category {
					entries[i].Filename = it.NewFilename
					entries[i].SHA512 = it.SHA512
					entries[i].Version = it.Version
					entries[i].UpdateVersion = ""
					entries[i].UpdateFilename = ""
					entries[i].UpdateURL = ""
					entries[i].UpdateSHA512 = ""
					entries[i].Changelog = ""
					entries[i].Staged = false
				}
			}
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Filename < entries[j].Filename })
	m.entries[profileName] = entries
}
```

In `Rollback` und `RollbackLabel` jeweils `m.invalidate(profileName)` ersetzen durch:

```go
	m.invalidate(profileName)
	m.notify(profileName, nil)
```

`restoreSet` ersetzen:

```go
// restoreSet moves the new files out of liveDir (into the set, suffixed)
// and the backed-up old files back in. Leere Namen (Hinzufügen/Entfernen)
// werden übersprungen — filepath.Join(dir, "") wäre der Ordner selbst.
func restoreSet(liveDir, set string) (int, error) {
	man, err := readManifest(filepath.Join(set, "manifest.json"))
	if err != nil {
		return 0, nil
	}
	restored := 0
	for _, it := range man.Items {
		if it.NewFilename != "" {
			newPath := filepath.Join(liveDir, it.NewFilename)
			if _, err := os.Stat(newPath); err == nil {
				os.Rename(newPath, filepath.Join(set, it.NewFilename+".rolledback"))
				if it.OldFilename == "" {
					restored++ // Hinzufügen rückgängig gemacht
				}
			}
		}
		if it.OldFilename != "" {
			oldBackup := filepath.Join(set, it.OldFilename)
			if _, err := os.Stat(oldBackup); err == nil {
				if err := os.Rename(oldBackup, filepath.Join(liveDir, it.OldFilename)); err != nil {
					return restored, fmt.Errorf("rollback von %s: %w", it.OldFilename, err)
				}
				restored++
			}
		}
	}
	return restored, nil
}
```

- [ ] **Step 4: Tests laufen lassen**

Run: `go test ./internal/mods/ ./internal/upgrade/ ./internal/scheduler/`
Expected: `ok` — neue Tests grün, `TestApplyKeepsRemainingUpdateBadges`, `TestRollbackLabelRestoresOnlyThatSet` weiter grün.

- [ ] **Step 5: Gegenprobe Leerer-Name-Schutz**

Temporär in `restoreSet` die Bedingung `if it.OldFilename != ""` entfernen → `go test ./internal/mods/ -run AddItem` muss mit „Live-Ordner beim Rollback beschädigt" scheitern. Danach zurücksetzen und erneut grün laufen lassen.

- [ ] **Step 6: Commit**

```bash
git add internal/mods/mods.go internal/mods/staging_test.go
git commit -m "feat: staging supports adding and removing mods, change hook, discard

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Suche, Plan mit Abhängigkeiten, Hinzufügen

**Files:**
- Create: `internal/mods/add.go`
- Create: `internal/mods/add_test.go`

**Interfaces:**
- Consumes: Task 1 (`modrinth.Project`, `ProjectVersions`, `Dependency`), Task 2 (`stagingItem` mit `ProjectID/Name/ClientSide`, `stagedItems`, `ApplyStaged`, `ProfileServer/ProfileClient`).
- Produces:
  - `func (m *Manager) Search(ctx, query, mcVersion string) ([]modrinth.SearchHit, error)` (Limit 20, nie nil)
  - `type PlannedFile struct { ProjectID, Name, VersionNumber, Filename, ClientSide, ServerSide string; Present map[string]bool; URL, SHA512 string /* json:"-" */ }` — JSON `projectId`, `name`, `versionNumber`, `filename`, `clientSide`, `serverSide`, `present`
  - `type AddPlan struct { Main PlannedFile; Deps []PlannedFile; Server, Client bool; Warnings []string; Blocked string }` — JSON `main`, `deps`, `server`, `client`, `warnings`, `blocked`
  - `func (m *Manager) Plan(ctx, projectID, mcVersion string) (*AddPlan, error)`
  - `type AddResult struct { Added map[string][]string }` — JSON `added` (Profil → Namen)
  - `func (m *Manager) AddMods(ctx, projectID, mcVersion string, profiles []string) (*AddResult, error)`
  - intern: `type installedMod struct { profiles map[string]bool; files map[string]string; name string; deps []modrinth.Dependency }`, `func (m *Manager) inventory(ctx) (map[string]*installedMod, error)` (für Task 4)

- [ ] **Step 1: Failing Tests** — `internal/mods/add_test.go`

```go
package mods_test

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
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
func (f *fakeAPI) ProjectSupports(context.Context, string, string, string) (bool, error) { return true, nil }
func (f *fakeAPI) Download(_ context.Context, url string) ([]byte, error) {
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
```

- [ ] **Step 2: Laufen lassen, muss scheitern**

Run: `go test ./internal/mods/ -run 'Plan|AddMods|SearchNever'`
Expected: Compile-Fehler (`mgr.Plan`, `mgr.AddMods`, `mgr.Search` undefined).

- [ ] **Step 3: Implementierung** — `internal/mods/add.go`

```go
package mods

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/modrinth"
)

const maxDepDepth = 3

// PlannedFile is one mod the add dialog shows (Haupt-Mod oder Abhängigkeit).
type PlannedFile struct {
	ProjectID     string          `json:"projectId"`
	Name          string          `json:"name"`
	VersionNumber string          `json:"versionNumber"`
	Filename      string          `json:"filename"`
	ClientSide    string          `json:"clientSide"`
	ServerSide    string          `json:"serverSide"`
	Present       map[string]bool `json:"present"` // Profil -> schon installiert/vorgemerkt
	URL           string          `json:"-"`
	SHA512        string          `json:"-"`
}

// AddPlan is everything the add dialog needs. Blocked != "" = Hinzufügen
// nicht möglich (Grund steht drin).
type AddPlan struct {
	Main     PlannedFile   `json:"main"`
	Deps     []PlannedFile `json:"deps"`
	Server   bool          `json:"server"` // Vorschlag aus Modrinth
	Client   bool          `json:"client"`
	Warnings []string      `json:"warnings"`
	Blocked  string        `json:"blocked,omitempty"`
}

// AddResult lists the added mod names per profile.
type AddResult struct {
	Added map[string][]string `json:"added"`
}

type installedMod struct {
	profiles map[string]bool
	files    map[string]string // Profil -> Dateiname
	name     string
	deps     []modrinth.Dependency
}

// Search finds mods with a build for the current MC version + loader.
func (m *Manager) Search(ctx context.Context, query, mcVersion string) ([]modrinth.SearchHit, error) {
	hits, err := m.api.Search(ctx, query, m.loader, mcVersion, 20)
	if err != nil {
		return nil, fmt.Errorf("modrinth-suche: %w", err)
	}
	if hits == nil {
		hits = []modrinth.SearchHit{}
	}
	return hits, nil
}

// inventory maps project IDs of all installed mods (Kategorie "mods", alle
// Profile, ein Modrinth-Aufruf) plus vorgemerkter Neuzugänge.
func (m *Manager) inventory(ctx context.Context) (map[string]*installedMod, error) {
	type ref struct{ profile, file, hash string }
	var refs []ref
	var hashes []string
	for _, p := range m.profiles {
		entries, err := scanFiles(p)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.Category != "mods" {
				continue
			}
			refs = append(refs, ref{p.Name, e.Filename, e.SHA512})
			hashes = append(hashes, e.SHA512)
		}
	}
	versions, err := m.api.VersionsByHash(ctx, hashes)
	if err != nil {
		return nil, fmt.Errorf("modrinth identify: %w", err)
	}
	inv := map[string]*installedMod{}
	add := func(pid, profile, file, name string, deps []modrinth.Dependency) {
		im, ok := inv[pid]
		if !ok {
			im = &installedMod{profiles: map[string]bool{}, files: map[string]string{}, name: name}
			inv[pid] = im
		}
		im.profiles[profile] = true
		im.files[profile] = file
		im.deps = append(im.deps, deps...)
	}
	for _, r := range refs {
		if v, ok := versions[r.hash]; ok && v.ProjectID != "" {
			add(v.ProjectID, r.profile, r.file, v.Name, v.Dependencies)
		}
	}
	for _, p := range m.profiles {
		for _, it := range m.stagedItems(p) {
			if it.action() == "add" && it.ProjectID != "" {
				add(it.ProjectID, p.Name, it.NewFilename, it.Name, nil)
			}
		}
	}
	return inv, nil
}

func sideWanted(s string) bool { return s == "required" || s == "optional" }

// planFile resolves one project to its newest build; ver == nil = kein Build.
func (m *Manager) planFile(ctx context.Context, projectID, mcVersion string, inv map[string]*installedMod) (PlannedFile, *modrinth.Version, error) {
	proj, err := m.api.Project(ctx, projectID)
	if err != nil {
		return PlannedFile{}, nil, fmt.Errorf("modrinth-projekt %s: %w", projectID, err)
	}
	pf := PlannedFile{ProjectID: proj.ID, Name: proj.Title, ClientSide: proj.ClientSide,
		ServerSide: proj.ServerSide, Present: map[string]bool{}}
	if pf.ProjectID == "" {
		pf.ProjectID = projectID
	}
	if im, ok := inv[pf.ProjectID]; ok {
		for p := range im.profiles {
			pf.Present[p] = true
		}
	}
	vs, err := m.api.ProjectVersions(ctx, pf.ProjectID, m.loader, mcVersion)
	if err != nil {
		return pf, nil, fmt.Errorf("modrinth-versionen %s: %w", pf.Name, err)
	}
	if len(vs) == 0 {
		return pf, nil, nil
	}
	v := vs[0]
	f, ok := v.PrimaryFile()
	if !ok {
		return pf, nil, nil
	}
	pf.VersionNumber, pf.Filename, pf.URL, pf.SHA512 = v.VersionNumber, f.Filename, f.URL, f.Hashes.SHA512
	return pf, &v, nil
}

// Plan builds the add dialog for one Modrinth project.
func (m *Manager) Plan(ctx context.Context, projectID, mcVersion string) (*AddPlan, error) {
	if mcVersion == "" {
		return nil, fmt.Errorf("Minecraft-Version unbekannt (Server startet noch?)")
	}
	inv, err := m.inventory(ctx)
	if err != nil {
		return nil, err
	}
	main, ver, err := m.planFile(ctx, projectID, mcVersion, inv)
	if err != nil {
		return nil, err
	}
	plan := &AddPlan{Main: main, Deps: []PlannedFile{}, Warnings: []string{},
		Server: sideWanted(main.ServerSide), Client: sideWanted(main.ClientSide)}
	if !plan.Server && !plan.Client {
		plan.Server = true // Modrinth weiß es nicht — Server ist der sichere Vorschlag
	}
	if ver == nil {
		plan.Blocked = fmt.Sprintf("%s hat noch keinen Build für Minecraft %s (%s)", main.Name, mcVersion, m.loader)
		return plan, nil
	}

	type queued struct {
		v     *modrinth.Version
		depth int
	}
	seen := map[string]bool{main.ProjectID: true}
	queue := []queued{{ver, 1}}
	truncated := false
	for len(queue) > 0 {
		q := queue[0]
		queue = queue[1:]
		for _, d := range q.v.Dependencies {
			switch d.DependencyType {
			case "incompatible":
				if im, ok := inv[d.ProjectID]; ok && d.ProjectID != "" {
					plan.Warnings = append(plan.Warnings,
						fmt.Sprintf("Laut Modrinth unverträglich mit dem installierten Mod %s", im.name))
				}
			case "required":
				if d.ProjectID == "" {
					plan.Warnings = append(plan.Warnings, "Eine Pflicht-Abhängigkeit nennt kein Projekt — bitte auf Modrinth prüfen")
					continue
				}
				if seen[d.ProjectID] {
					continue
				}
				seen[d.ProjectID] = true
				if q.depth > maxDepDepth {
					truncated = true
					continue
				}
				dep, dv, err := m.planFile(ctx, d.ProjectID, mcVersion, inv)
				if err != nil {
					return nil, err
				}
				if dv == nil {
					plan.Blocked = fmt.Sprintf("Abhängigkeit %s hat noch keinen Build für Minecraft %s", dep.Name, mcVersion)
					continue
				}
				if len(dep.Present) < len(m.profiles) {
					plan.Deps = append(plan.Deps, dep)
				}
				queue = append(queue, queued{dv, q.depth + 1})
			}
		}
	}
	if truncated {
		plan.Warnings = append(plan.Warnings, "Abhängigkeiten tiefer als 3 Ebenen wurden nicht geprüft — nach dem Neustart das Server-Log ansehen")
	}
	return plan, nil
}

// AddMods stages the mod plus missing dependencies per profile. Server:
// vorgemerkt bis zum Neustart; Client: sofort angewendet (gleiche Sicherung).
func (m *Manager) AddMods(ctx context.Context, projectID, mcVersion string, profiles []string) (*AddResult, error) {
	if len(profiles) == 0 {
		return nil, fmt.Errorf("mindestens ein Profil wählen")
	}
	for _, name := range profiles {
		if _, err := m.profile(name); err != nil {
			return nil, err
		}
	}
	plan, err := m.Plan(ctx, projectID, mcVersion)
	if err != nil {
		return nil, err
	}
	if plan.Blocked != "" {
		return nil, errors.New(plan.Blocked)
	}
	files := append([]PlannedFile{plan.Main}, plan.Deps...)
	res := &AddResult{Added: map[string][]string{}}
	data := map[string][]byte{} // sha512 -> Inhalt (einmal laden für beide Profile)
	for _, name := range profiles {
		p, _ := m.profile(name)
		live, ok := p.Dirs["mods"]
		if !ok {
			return res, fmt.Errorf("Profil %s hat keinen mods-Ordner", name)
		}
		sdir := stagingDir(p)
		if err := os.MkdirAll(sdir, 0o755); err != nil {
			return res, err
		}
		manPath := filepath.Join(sdir, "manifest.json")
		man, _ := readManifest(manPath)
		man.Created = time.Now()
		for _, f := range files {
			if f.Present[name] {
				continue
			}
			if _, err := os.Stat(filepath.Join(live, f.Filename)); err == nil {
				return res, fmt.Errorf("%s liegt bereits im Profil %s", f.Filename, name)
			}
			b, ok := data[f.SHA512]
			if !ok {
				b, err = m.api.Download(ctx, f.URL)
				if err != nil {
					return res, fmt.Errorf("download %s: %w", f.Filename, err)
				}
				sum := sha512.Sum512(b)
				if hex.EncodeToString(sum[:]) != f.SHA512 {
					return res, fmt.Errorf("hash-verifikation fehlgeschlagen für %s — abgebrochen", f.Filename)
				}
				data[f.SHA512] = b
			}
			if err := os.WriteFile(filepath.Join(sdir, f.Filename), b, 0o644); err != nil {
				return res, err
			}
			man.Items = append(man.Items, stagingItem{
				Category: "mods", NewFilename: f.Filename, SHA512: f.SHA512, Version: f.VersionNumber,
				ProjectID: f.ProjectID, Name: f.Name, ClientSide: f.ClientSide,
			})
			res.Added[name] = append(res.Added[name], f.Name)
		}
		if len(res.Added[name]) == 0 {
			continue
		}
		if err := writeManifest(manPath, man); err != nil {
			return res, err
		}
		if name == ProfileClient {
			if _, _, err := m.ApplyStaged(name); err != nil {
				return res, fmt.Errorf("client-paket: %w", err)
			}
		}
	}
	if len(res.Added) == 0 {
		return res, fmt.Errorf("%s ist in den gewählten Profilen schon vorhanden oder vorgemerkt", plan.Main.Name)
	}
	return res, nil
}
```

- [ ] **Step 4: Tests laufen lassen**

Run: `go test ./internal/mods/`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/mods/add.go internal/mods/add_test.go
git commit -m "feat: plan and add mods from Modrinth incl. required dependencies

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Entfernen und Datei-Upload

**Files:**
- Create: `internal/mods/remove.go`
- Create: `internal/mods/remove_test.go`

**Interfaces:**
- Consumes: Task 2 (`stagingItem`, `stagedItems`, `ApplyStaged`, `ProfileClient`), Task 3 (`inventory`, `installedMod.files`, `addSetup`/`fakeAPI` aus `add_test.go`).
- Produces:
  - `type ProfileFile struct { Profile, Filename string }` (JSON `profile`, `filename`)
  - `type RemovalInfo struct { Name string; Dependents []string; AlsoIn []ProfileFile }` (JSON `name`, `dependents`, `alsoIn`)
  - `func (m *Manager) RemovalInfo(ctx, profile, filename string) (*RemovalInfo, error)`
  - `func (m *Manager) StageRemove(profile, filename string) error`
  - `const MaxUploadBytes = 100 << 20`
  - `func (m *Manager) StageUpload(ctx, profile, category, filename string, data []byte) (Entry, error)`

- [ ] **Step 1: Failing Tests** — `internal/mods/remove_test.go`

```go
package mods_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mods"
)

func TestRemovalInfoFindsDependentsAndOtherProfile(t *testing.T) {
	mgr, api, _, _ := addSetup(t)
	api.addVersion("way", "Waystones", "waystones.jar", "required", "required", req("fapi"))
	ctx := context.Background()
	if _, err := mgr.AddMods(ctx, "way", "26.3", []string{mods.ProfileServer, mods.ProfileClient}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mgr.ApplyStaged(mods.ProfileServer); err != nil {
		t.Fatal(err)
	}
	info, err := mgr.RemovalInfo(ctx, mods.ProfileServer, "fabric-api.jar")
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Dependents) != 1 || !strings.Contains(info.Dependents[0], "Waystones") {
		t.Errorf("dependents = %v", info.Dependents)
	}
	if len(info.AlsoIn) != 1 || info.AlsoIn[0].Profile != mods.ProfileClient || info.AlsoIn[0].Filename != "fabric-api.jar" {
		t.Errorf("alsoIn = %+v", info.AlsoIn)
	}
}

func TestStageRemoveServerPendingUntilApply(t *testing.T) {
	mgr, _, serverDir, _ := addSetup(t)
	if err := mgr.StageRemove(mods.ProfileServer, "fabric-api.jar"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(serverDir, "fabric-api.jar")); err != nil {
		t.Fatal("Server-Mod schon weg — muss bis zum Neustart bleiben")
	}
	found := false
	for _, e := range mgr.Entries(mods.ProfileServer) {
		if e.Filename == "fabric-api.jar" && e.Pending == "remove" {
			found = true
		}
	}
	if !found {
		t.Error("Entfernung nicht als vorgemerkt sichtbar")
	}
	if err := mgr.StageRemove(mods.ProfileServer, "fabric-api.jar"); err == nil {
		t.Error("doppeltes Vormerken muss abgelehnt werden")
	}
	if _, _, err := mgr.ApplyStaged(mods.ProfileServer); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(serverDir, "fabric-api.jar")); err == nil {
		t.Error("nach dem Anwenden noch da")
	}
}

func TestStageRemoveClientImmediate(t *testing.T) {
	mgr, _, _, clientDir := addSetup(t)
	if err := os.WriteFile(filepath.Join(clientDir, "sodium.jar"), []byte("s"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := mgr.StageRemove(mods.ProfileClient, "sodium.jar"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(clientDir, "sodium.jar")); err == nil {
		t.Error("Client-Mod muss sofort entfernt sein")
	}
}

func TestStageRemoveRejectsBadNames(t *testing.T) {
	mgr, _, _, _ := addSetup(t)
	for _, name := range []string{"", "../fabric-api.jar", ".msm-staging", "fehlt.jar"} {
		if err := mgr.StageRemove(mods.ProfileServer, name); err == nil {
			t.Errorf("%q: kein Fehler", name)
		}
	}
}

func TestStageUploadValidation(t *testing.T) {
	mgr, _, _, _ := addSetup(t)
	ctx := context.Background()
	cases := []struct {
		profile, category, name string
		data                    []byte
	}{
		{mods.ProfileServer, "mods", "../evil.jar", []byte("x")},
		{mods.ProfileServer, "mods", ".hidden.jar", []byte("x")},
		{mods.ProfileServer, "mods", "pack.zip", []byte("x")},
		{mods.ProfileClient, "shaderpacks", "shader.jar", []byte("x")},
		{mods.ProfileServer, "shaderpacks", "shader.zip", []byte("x")}, // Server hat keine Shader
		{mods.ProfileServer, "mods", "leer.jar", nil},
		{mods.ProfileServer, "mods", "fabric-api.jar", []byte("x")}, // existiert schon
	}
	for _, c := range cases {
		if _, err := mgr.StageUpload(ctx, c.profile, c.category, c.name, c.data); err == nil {
			t.Errorf("%s/%s/%s: kein Fehler", c.profile, c.category, c.name)
		}
	}
	big := make([]byte, mods.MaxUploadBytes+1)
	if _, err := mgr.StageUpload(ctx, mods.ProfileServer, "mods", "big.jar", big); err == nil {
		t.Error("Datei > 100 MB angenommen")
	}
}

func TestStageUploadRecognisesModrinthFile(t *testing.T) {
	mgr, api, _, clientDir := addSetup(t)
	ctx := context.Background()
	content := api.files["fake://fabric-api.jar"]
	e, err := mgr.StageUpload(ctx, mods.ProfileClient, "mods", "fabric-api-renamed.jar", content)
	if err != nil {
		t.Fatal(err)
	}
	if !e.Managed || e.ProjectID != "fapi" {
		t.Errorf("entry = %+v, want verwaltet (fapi)", e)
	}
	if _, err := os.Stat(filepath.Join(clientDir, "fabric-api-renamed.jar")); err != nil {
		t.Error("Client-Upload nicht sofort im Paket")
	}

	e, err = mgr.StageUpload(ctx, mods.ProfileServer, "mods", "custom.jar", []byte("eigener mod"))
	if err != nil {
		t.Fatal(err)
	}
	if e.Managed {
		t.Error("unbekannte Datei als verwaltet markiert")
	}
	if !mgr.HasStaged(mods.ProfileServer) {
		t.Error("Server-Upload nicht vorgemerkt")
	}
	if _, err := mgr.StageUpload(ctx, mods.ProfileServer, "mods", "custom.jar", []byte("nochmal")); err == nil {
		t.Error("doppelter Upload angenommen")
	}
}
```

- [ ] **Step 2: Laufen lassen, muss scheitern**

Run: `go test ./internal/mods/ -run 'Removal|StageRemove|StageUpload'`
Expected: Compile-Fehler.

- [ ] **Step 3: Implementierung** — `internal/mods/remove.go`

```go
package mods

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// MaxUploadBytes limits uploaded files (Spec: 100 MB).
const MaxUploadBytes = 100 << 20

var uploadExt = map[string]string{"mods": ".jar", "shaderpacks": ".zip", "resourcepacks": ".zip"}

// ProfileFile names a file in a profile.
type ProfileFile struct {
	Profile  string `json:"profile"`
	Filename string `json:"filename"`
}

// RemovalInfo feeds the confirm dialog before removing a mod.
type RemovalInfo struct {
	Name       string        `json:"name"`
	Dependents []string      `json:"dependents"` // installierte Mods, die ihn brauchen
	AlsoIn     []ProfileFile `json:"alsoIn"`     // derselbe Mod in anderen Profilen
}

func validName(name string) bool {
	return name != "" && name == filepath.Base(name) && !strings.ContainsAny(name, `/\`) &&
		!strings.HasPrefix(name, ".") && !strings.Contains(name, "..")
}

// findLive returns the category of a live file in the profile.
func findLive(p Profile, filename string) (string, error) {
	if !validName(filename) {
		return "", fmt.Errorf("ungültiger Dateiname %q", filename)
	}
	for cat, dir := range p.Dirs {
		if fi, err := os.Stat(filepath.Join(dir, filename)); err == nil && fi.Mode().IsRegular() {
			return cat, nil
		}
	}
	return "", fmt.Errorf("%s nicht im Profil %s gefunden", filename, p.Name)
}

// RemovalInfo collects warnings before removing filename — best effort:
// ohne Modrinth gibt es nur den Dateinamen-Abgleich.
func (m *Manager) RemovalInfo(ctx context.Context, profile, filename string) (*RemovalInfo, error) {
	p, err := m.profile(profile)
	if err != nil {
		return nil, err
	}
	cat, err := findLive(p, filename)
	if err != nil {
		return nil, err
	}
	info := &RemovalInfo{Name: filename, Dependents: []string{}, AlsoIn: []ProfileFile{}}
	sum, _, err := hashFile(filepath.Join(p.Dirs[cat], filename))
	if err != nil {
		return nil, err
	}
	target := ""
	if vs, err := m.api.VersionsByHash(ctx, []string{sum}); err == nil {
		if v, ok := vs[sum]; ok {
			target, info.Name = v.ProjectID, v.Name
		}
	}
	if target != "" {
		if inv, err := m.inventory(ctx); err == nil {
			for pid, im := range inv {
				if pid == target {
					for prof, file := range im.files {
						if prof != profile {
							info.AlsoIn = append(info.AlsoIn, ProfileFile{prof, file})
						}
					}
					continue
				}
				if !im.profiles[profile] {
					continue
				}
				for _, d := range im.deps {
					if d.DependencyType == "required" && d.ProjectID == target {
						info.Dependents = append(info.Dependents, im.name)
						break
					}
				}
			}
		}
	} else {
		for _, op := range m.profiles {
			if op.Name == profile {
				continue
			}
			if _, err := findLive(op, filename); err == nil {
				info.AlsoIn = append(info.AlsoIn, ProfileFile{op.Name, filename})
			}
		}
	}
	sort.Strings(info.Dependents)
	sort.Slice(info.AlsoIn, func(i, j int) bool { return info.AlsoIn[i].Profile < info.AlsoIn[j].Profile })
	return info, nil
}

// StageRemove marks a live file for removal. Server: vorgemerkt bis zum
// Neustart; Client: sofort (Datei wandert nach .backup, Rollback möglich).
func (m *Manager) StageRemove(profile, filename string) error {
	p, err := m.profile(profile)
	if err != nil {
		return err
	}
	cat, err := findLive(p, filename)
	if err != nil {
		return err
	}
	sdir := stagingDir(p)
	if err := os.MkdirAll(sdir, 0o755); err != nil {
		return err
	}
	manPath := filepath.Join(sdir, "manifest.json")
	man, _ := readManifest(manPath)
	for _, it := range man.Items {
		if it.OldFilename == filename {
			return fmt.Errorf("für %s ist schon eine Änderung vorgemerkt — erst verwerfen", filename)
		}
	}
	item := stagingItem{Category: cat, OldFilename: filename}
	m.mu.Lock()
	for _, e := range m.entries[profile] {
		if e.Filename == filename && e.Category == cat {
			item.ProjectID, item.Name = e.ProjectID, e.Name
		}
	}
	m.mu.Unlock()
	man.Created = time.Now()
	man.Items = append(man.Items, item)
	if err := writeManifest(manPath, man); err != nil {
		return err
	}
	if profile == ProfileClient {
		if _, _, err := m.ApplyStaged(profile); err != nil {
			return fmt.Errorf("client-paket: %w", err)
		}
	}
	return nil
}

// StageUpload stages an uploaded file. Bekannte Modrinth-Dateien (Hash)
// werden verwaltet und bekommen Updates; unbekannte bleiben "unverwaltet".
func (m *Manager) StageUpload(ctx context.Context, profile, category, filename string, data []byte) (Entry, error) {
	p, err := m.profile(profile)
	if err != nil {
		return Entry{}, err
	}
	live, ok := p.Dirs[category]
	if !ok {
		return Entry{}, fmt.Errorf("Profil %s hat keine Kategorie %q", profile, category)
	}
	if !validName(filename) {
		return Entry{}, fmt.Errorf("ungültiger Dateiname %q", filename)
	}
	ext := uploadExt[category]
	if ext == "" || !strings.EqualFold(filepath.Ext(filename), ext) {
		return Entry{}, fmt.Errorf("für %s sind nur %s-Dateien erlaubt", category, ext)
	}
	if len(data) == 0 || len(data) > MaxUploadBytes {
		return Entry{}, fmt.Errorf("Datei leer oder größer als 100 MB")
	}
	if _, err := os.Stat(filepath.Join(live, filename)); err == nil {
		return Entry{}, fmt.Errorf("%s liegt bereits im Profil %s", filename, profile)
	}
	sdir := stagingDir(p)
	if err := os.MkdirAll(sdir, 0o755); err != nil {
		return Entry{}, err
	}
	manPath := filepath.Join(sdir, "manifest.json")
	man, _ := readManifest(manPath)
	for _, it := range man.Items {
		if it.NewFilename == filename {
			return Entry{}, fmt.Errorf("%s ist schon vorgemerkt", filename)
		}
	}
	sum := sha512.Sum512(data)
	hash := hex.EncodeToString(sum[:])
	item := stagingItem{Category: category, NewFilename: filename, SHA512: hash, Name: filename}
	if vs, err := m.api.VersionsByHash(ctx, []string{hash}); err == nil {
		if v, ok := vs[hash]; ok {
			item.ProjectID, item.Name, item.Version = v.ProjectID, v.Name, v.VersionNumber
		}
	}
	if err := os.WriteFile(filepath.Join(sdir, filename), data, 0o644); err != nil {
		return Entry{}, err
	}
	man.Created = time.Now()
	man.Items = append(man.Items, item)
	if err := writeManifest(manPath, man); err != nil {
		return Entry{}, err
	}
	if profile == ProfileClient {
		if _, _, err := m.ApplyStaged(profile); err != nil {
			return Entry{}, fmt.Errorf("client-paket: %w", err)
		}
	}
	return Entry{Profile: profile, Category: category, Filename: filename, SHA512: hash,
		Managed: item.ProjectID != "", ProjectID: item.ProjectID, Name: item.Name, Version: item.Version}, nil
}
```

Hinweis: Der Stagingordner liegt in `mods/.msm-staging`; Shader/Resourcepacks werden von dort per `os.Rename` in ihren Ordner verschoben. Das funktioniert, weil das Client-Paket als **ein** Mount (`/mc/client-pack`) im Container hängt (docker-compose.yml) — nicht in getrennte Mounts aufteilen.

- [ ] **Step 4: Tests laufen lassen**

Run: `go test ./internal/mods/`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/mods/remove.go internal/mods/remove_test.go
git commit -m "feat: remove mods and upload files with validation

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Mod-Neuigkeiten nach dem Neustart (`internal/modnews`)

**Files:**
- Modify: `internal/events/events.go` (Konstante)
- Create: `internal/modnews/modnews.go`
- Create: `internal/modnews/modnews_test.go`

**Interfaces:**
- Consumes: Task 2 (`mods.Change`, `mods.ProfileServer/ProfileClient`).
- Produces:
  - `events.TypeModsChanged = "mods.changed"`
  - `func modnews.New(get func(string) string, set func(string, string), bus *events.Bus, online func() bool, log *slog.Logger) *Announcer`
  - `(*Announcer).SetBusy(f func() bool)` — true = schweigen (Ein-Klick-Update läuft)
  - `(*Announcer).SetPublisher(ready func() bool, publish func(ctx context.Context) error)`
  - `(*Announcer).OnApply(profile string, changes []mods.Change)` — Hook für `mods.Manager.SetChangeHook`
  - `(*Announcer).Published()` — nach jedem erfolgreichen Paket-Versand aufrufen
  - `(*Announcer).AfterRestart(ctx context.Context)`
  - `(*Announcer).Run(ctx context.Context)`; Feld `Poll time.Duration` (Default 5 s)
  - app_state-Schlüssel: `mods.news.server`, `mods.news.client` (JSON `[]mods.Change`), `mods.clientpack.dirty` (`"1"` oder leer)

- [ ] **Step 1: Event-Typ** — `internal/events/events.go`, nach `TypeClientPack ...` einfügen:

```go
	TypeModsChanged     = "mods.changed"    // Mods hinzugefügt/entfernt (nach Neustart)
```

- [ ] **Step 2: Failing Tests** — `internal/modnews/modnews_test.go`

```go
package modnews

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mods"
)

type kv struct {
	mu sync.Mutex
	m  map[string]string
}

func (k *kv) get(key string) string { k.mu.Lock(); defer k.mu.Unlock(); return k.m[key] }
func (k *kv) set(key, v string)     { k.mu.Lock(); defer k.mu.Unlock(); k.m[key] = v }

func setup(t *testing.T, ready bool) (*Announcer, *kv, <-chan events.Event, *atomic.Int32, *atomic.Bool) {
	t.Helper()
	store := &kv{m: map[string]string{}}
	bus := events.New()
	ch, cancel := bus.Subscribe(16)
	t.Cleanup(cancel)
	online := &atomic.Bool{}
	online.Store(true)
	published := &atomic.Int32{}
	a := New(store.get, store.set, bus, online.Load, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var self *Announcer = a
	a.SetPublisher(func() bool { return ready }, func(context.Context) error {
		published.Add(1)
		self.Published()
		return nil
	})
	return a, store, ch, published, online
}

func next(t *testing.T, ch <-chan events.Event) (events.Event, bool) {
	t.Helper()
	select {
	case ev := <-ch:
		return ev, true
	case <-time.After(200 * time.Millisecond):
		return events.Event{}, false
	}
}

func add(profile, name, client string) mods.Change {
	return mods.Change{Action: "add", Profile: profile, Name: name, ProjectID: "p-" + name, ClientSide: client}
}

func TestAfterRestartAnnouncesAndPublishes(t *testing.T) {
	a, store, ch, published, _ := setup(t, true)
	a.OnApply(mods.ProfileServer, []mods.Change{add(mods.ProfileServer, "Waystones", "required"), add(mods.ProfileServer, "Lithium", "optional")})
	a.OnApply(mods.ProfileClient, []mods.Change{add(mods.ProfileClient, "Waystones", "required"), add(mods.ProfileClient, "Sodium", "required")})
	a.OnApply(mods.ProfileServer, []mods.Change{{Action: "remove", Profile: mods.ProfileServer, Name: "Altmod", Filename: "alt.jar"}})

	a.AfterRestart(context.Background())

	ev, ok := next(t, ch)
	if !ok || ev.Type != events.TypeModsChanged {
		t.Fatalf("event = %+v (ok=%v)", ev, ok)
	}
	for _, want := range []string{"Waystones — auch bei dir nötig", "Lithium — nur Server, nichts zu tun", "Sodium — nur bei dir (Client-Mod)", "Altmod — nichts zu tun"} {
		if !strings.Contains(ev.Message, want) {
			t.Errorf("Meldung fehlt %q:\n%s", want, ev.Message)
		}
	}
	if published.Load() != 1 {
		t.Errorf("published = %d, want 1", published.Load())
	}
	if store.get(keyDirty) != "" || store.get(keyServer) != "" || store.get(keyClient) != "" {
		t.Errorf("Zustand nicht geleert: %v", store.m)
	}
	// zweiter Neustart ohne Änderungen: still
	a.AfterRestart(context.Background())
	if ev, ok := next(t, ch); ok {
		t.Errorf("unerwartete Meldung: %+v", ev)
	}
	if published.Load() != 1 {
		t.Error("Paket ohne Änderung erneut verschickt")
	}
}

func TestUpdatesOnlyPublishPackWithoutModMessage(t *testing.T) {
	a, _, ch, published, _ := setup(t, true)
	a.OnApply(mods.ProfileClient, []mods.Change{{Action: "update", Profile: mods.ProfileClient, Name: "Sodium"}})
	a.OnApply(mods.ProfileServer, []mods.Change{{Action: "update", Profile: mods.ProfileServer, Name: "Lithium"}})
	a.AfterRestart(context.Background())
	if ev, ok := next(t, ch); ok {
		t.Errorf("Mod-Meldung bei reinen Updates: %+v", ev)
	}
	if published.Load() != 1 {
		t.Errorf("published = %d, want 1", published.Load())
	}
}

func TestWithoutDropboxHintAndDirtyStays(t *testing.T) {
	a, store, ch, published, _ := setup(t, false)
	a.OnApply(mods.ProfileClient, []mods.Change{add(mods.ProfileClient, "Sodium", "required")})
	a.AfterRestart(context.Background())
	ev, ok := next(t, ch)
	if !ok || !strings.Contains(ev.Message, "verteilt der Admin") {
		t.Fatalf("event = %+v", ev)
	}
	if published.Load() != 0 {
		t.Error("ohne Dropbox veröffentlicht")
	}
	if store.get(keyDirty) != "1" {
		t.Error("Markierung 'Paket veraltet' verloren")
	}
}

func TestPublishedByButtonConsumesClientList(t *testing.T) {
	a, _, ch, _, _ := setup(t, true)
	a.OnApply(mods.ProfileClient, []mods.Change{add(mods.ProfileClient, "Sodium", "required")})
	a.Published()
	a.AfterRestart(context.Background())
	if ev, ok := next(t, ch); ok {
		t.Errorf("schon verschickte Client-Änderung erneut gemeldet: %+v", ev)
	}
}

func TestRunTriggersOnlyAfterOfflineOnlineAndNotWhileBusy(t *testing.T) {
	a, _, ch, _, online := setup(t, true)
	a.Poll = 5 * time.Millisecond
	busy := &atomic.Bool{}
	a.SetBusy(busy.Load)
	a.OnApply(mods.ProfileServer, []mods.Change{add(mods.ProfileServer, "Lithium", "optional")})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx)

	if _, ok := next(t, ch); ok {
		t.Fatal("Meldung ohne Neustart")
	}
	busy.Store(true)
	online.Store(false)
	time.Sleep(30 * time.Millisecond)
	online.Store(true)
	if _, ok := next(t, ch); ok {
		t.Fatal("Meldung während eines laufenden Ein-Klick-Updates")
	}
	busy.Store(false)
	online.Store(false)
	time.Sleep(30 * time.Millisecond)
	online.Store(true)
	if ev, ok := next(t, ch); !ok || ev.Type != events.TypeModsChanged {
		t.Fatalf("keine Meldung nach Neustart: %+v", ev)
	}
}
```

- [ ] **Step 3: Laufen lassen, muss scheitern**

Run: `go test ./internal/modnews/`
Expected: Compile-Fehler (Paket leer).

- [ ] **Step 4: Implementierung** — `internal/modnews/modnews.go`

```go
// Package modnews tells players about mod changes after the restart that
// activated them: Liste der neuen/entfernten Mods + ggf. neues Client-Paket.
// Änderungen kommen über den Hook am mods.Manager, der Neustart wird am
// Übergang offline → online erkannt. Zustand liegt in app_state und
// überlebt so auch Host-Reboots.
package modnews

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mods"
)

const (
	keyServer = "mods.news.server"
	keyClient = "mods.news.client"
	keyDirty  = "mods.clientpack.dirty"
	maxItems  = 50
)

type Announcer struct {
	get    func(string) string
	set    func(string, string)
	bus    *events.Bus
	online func() bool
	log    *slog.Logger

	busy    func() bool
	ready   func() bool
	publish func(ctx context.Context) error

	// Poll: Abstand der Online-Prüfung
	Poll time.Duration

	mu sync.Mutex
}

func New(get func(string) string, set func(string, string), bus *events.Bus, online func() bool, log *slog.Logger) *Announcer {
	return &Announcer{get: get, set: set, bus: bus, online: online, log: log, Poll: 5 * time.Second}
}

// SetBusy: solange f true liefert (Ein-Klick-Update), bleibt der Announcer
// still — das Update verschickt sein Paket selbst.
func (a *Announcer) SetBusy(f func() bool) { a.busy = f }

// SetPublisher wires the client pack upload; publish muss bei Erfolg
// Published() auslösen (in main über publishPack erledigt).
func (a *Announcer) SetPublisher(ready func() bool, publish func(ctx context.Context) error) {
	a.ready, a.publish = ready, publish
}

func (a *Announcer) load(key string) []mods.Change {
	var out []mods.Change
	if raw := a.get(key); raw != "" {
		json.Unmarshal([]byte(raw), &out)
	}
	return out
}

func (a *Announcer) save(key string, list []mods.Change) {
	if len(list) == 0 {
		a.set(key, "")
		return
	}
	if len(list) > maxItems {
		list = list[len(list)-maxItems:]
	}
	b, _ := json.Marshal(list)
	a.set(key, string(b))
}

// OnApply is the mods.Manager change hook.
func (a *Announcer) OnApply(profile string, changes []mods.Change) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var news []mods.Change
	for _, c := range changes {
		if c.Action == "add" || c.Action == "remove" {
			news = append(news, c)
		}
	}
	switch profile {
	case mods.ProfileClient:
		a.set(keyDirty, "1") // jede Client-Änderung (auch Updates/Rollback) macht das Paket alt
		if len(news) > 0 {
			a.save(keyClient, append(a.load(keyClient), news...))
		}
	case mods.ProfileServer:
		if len(news) > 0 {
			a.save(keyServer, append(a.load(keyServer), news...))
		}
	}
}

// Published clears the "pack outdated" flag and the collected client
// changes — nach jedem erfolgreichen Versand (Button, Update, Neustart).
func (a *Announcer) Published() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.set(keyDirty, "")
	a.save(keyClient, nil)
}

// AfterRestart posts the mod list (if anything was added/removed) and
// publishes the client pack when it is outdated.
func (a *Announcer) AfterRestart(ctx context.Context) {
	a.mu.Lock()
	server, client := a.load(keyServer), a.load(keyClient)
	dirty := a.get(keyDirty) == "1"
	a.mu.Unlock()

	canPublish := dirty && a.publish != nil && a.ready != nil && a.ready()
	if text, title, ok := compose(server, client); ok {
		if dirty && !canPublish {
			text += "\nDas neue Mod-Paket verteilt der Admin."
		} else if canPublish {
			text += "\nDen Download-Link findet ihr gleich in der Meldung „📦 Neues Mod-Paket“."
		}
		a.bus.Publish(events.Event{Type: events.TypeModsChanged, Severity: events.SevSuccess, Title: title, Message: text})
		a.mu.Lock()
		a.save(keyServer, nil)
		a.save(keyClient, nil)
		a.mu.Unlock()
	}
	if canPublish {
		if err := a.publish(ctx); err != nil {
			a.log.Error("client-paket nach neustart fehlgeschlagen", "err", err)
		}
	}
}

// Run watches for offline → online transitions and calls AfterRestart.
func (a *Announcer) Run(ctx context.Context) {
	t := time.NewTicker(a.Poll)
	defer t.Stop()
	sawOffline := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if a.busy != nil && a.busy() {
			sawOffline = false
			continue
		}
		if !a.online() {
			sawOffline = true
			continue
		}
		if sawOffline {
			sawOffline = false
			a.AfterRestart(ctx)
		}
	}
}

type row struct {
	name           string
	server, client bool
}

// compose builds the Discord text; ok=false wenn nichts hinzugefügt oder
// entfernt wurde.
func compose(server, client []mods.Change) (text, title string, ok bool) {
	added, removed := map[string]*row{}, map[string]*row{}
	put := func(c mods.Change, isClient bool) {
		set := added
		if c.Action == "remove" {
			set = removed
		}
		key := c.ProjectID
		if key == "" {
			key = c.Name
		}
		r, ok := set[key]
		if !ok {
			r = &row{name: c.Name}
			set[key] = r
		}
		if isClient {
			r.client = true
		} else {
			r.server = true
		}
	}
	for _, c := range server {
		put(c, false)
	}
	for _, c := range client {
		put(c, true)
	}
	if len(added)+len(removed) == 0 {
		return "", "", false
	}
	var b strings.Builder
	write := func(head string, set map[string]*row, both, serverOnly, clientOnly string) {
		if len(set) == 0 {
			return
		}
		rows := make([]*row, 0, len(set))
		for _, r := range set {
			rows = append(rows, r)
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
		b.WriteString(head + "\n")
		for _, r := range rows {
			hint := serverOnly
			switch {
			case r.server && r.client:
				hint = both
			case r.client:
				hint = clientOnly
			}
			b.WriteString("• " + r.name + " — " + hint + "\n")
		}
	}
	write("**Neu:**", added, "auch bei dir nötig", "nur Server, nichts zu tun", "nur bei dir (Client-Mod)")
	write("**Entfernt:**", removed, "bitte auch bei dir entfernen", "nichts zu tun", "kann bei dir raus")
	title = "🧩 Neue Mods auf dem Server"
	if len(added) == 0 {
		title = "🧩 Mods vom Server entfernt"
	}
	return strings.TrimRight(b.String(), "\n"), title, true
}
```

- [ ] **Step 5: Tests laufen lassen**

Run: `go test -race ./internal/modnews/ ./internal/events/`
Expected: `ok`.

- [ ] **Step 6: Commit**

```bash
git add internal/events/events.go internal/modnews
git commit -m "feat: announce mod changes and publish the client pack after restart

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Scheduler – Ad-hoc-Lauf und nächste Übernahme

**Files:**
- Modify: `internal/scheduler/scheduler.go` (`RunNow` aufteilen, `RunAdhoc`, `NextApply`)
- Create: `internal/scheduler/adhoc_test.go`

**Interfaces:**
- Produces:
  - `func (s *Scheduler) RunAdhoc(ctx context.Context, r storage.Routine)` — führt eine nicht gespeicherte Routine synchron aus (gleiche Discord-/Wartungsfenster-Logik wie `RunNow`), schreibt **keinen** Historien-Eintrag (`r.ID == 0`)
  - `func (s *Scheduler) NextApply(ctx context.Context, now time.Time) (name string, at time.Time, ok bool, err error)` — früheste aktive Routine mit `ApplyStaged` und Typ `announce-restart` oder `backup`

- [ ] **Step 1: Failing Tests** — `internal/scheduler/adhoc_test.go`

```go
package scheduler

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/storage"
)

type adhocStore struct {
	routines []storage.Routine
	runs     int
}

func (s *adhocStore) ListRoutines(context.Context) ([]storage.Routine, error) { return s.routines, nil }
func (s *adhocStore) GetRoutine(_ context.Context, id int64) (storage.Routine, error) {
	for _, r := range s.routines {
		if r.ID == id {
			return r, nil
		}
	}
	return storage.Routine{}, fmt.Errorf("not found")
}
func (s *adhocStore) RecordRun(context.Context, storage.RoutineRun) error { s.runs++; return nil }

type adhocRCON struct{ cmds []string }

func (r *adhocRCON) Exec(_ context.Context, c string) (string, error) {
	r.cmds = append(r.cmds, c)
	return "ok", nil
}

func adhocLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestRunAdhocExecutesWithoutHistory(t *testing.T) {
	store := &adhocStore{}
	rcon := &adhocRCON{}
	s := New(store, rcon, nil, nil, adhocLog())
	s.RunAdhoc(context.Background(), storage.Routine{Name: "Test", Kind: "rcon", Payload: "say hi"})
	if len(rcon.cmds) != 1 || rcon.cmds[0] != "say hi" {
		t.Errorf("cmds = %v", rcon.cmds)
	}
	if store.runs != 0 {
		t.Errorf("Ad-hoc-Lauf in der Historie gespeichert (%d)", store.runs)
	}
}

func TestNextApplyPicksEarliestApplyingRoutine(t *testing.T) {
	store := &adhocStore{routines: []storage.Routine{
		{ID: 1, Name: "Nacht-Backup", Kind: "backup", Cron: "0 4 * * *", Enabled: true, ApplyStaged: true},
		{ID: 2, Name: "Neustart ohne Updates", Kind: "announce-restart", Cron: "0 20 * * *", Enabled: true},
		{ID: 3, Name: "Aus", Kind: "announce-restart", Cron: "0 18 * * *", Enabled: false, ApplyStaged: true},
		{ID: 4, Name: "RCON", Kind: "rcon", Cron: "0 18 * * *", Enabled: true, ApplyStaged: true},
	}}
	s := New(store, nil, nil, nil, adhocLog())
	now := time.Date(2026, 11, 8, 17, 0, 0, 0, time.Local)
	name, at, ok, err := s.NextApply(context.Background(), now)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if name != "Nacht-Backup" || !at.Equal(time.Date(2026, 11, 9, 4, 0, 0, 0, time.Local)) {
		t.Errorf("next = %s @ %v", name, at)
	}

	store.routines = store.routines[1:]
	if _, _, ok, _ := s.NextApply(context.Background(), now); ok {
		t.Error("ok ohne passende Routine")
	}
}
```

- [ ] **Step 2: Laufen lassen, muss scheitern**

Run: `go test ./internal/scheduler/ -run 'Adhoc|NextApply'`
Expected: Compile-Fehler (`RunAdhoc`, `NextApply` undefined).

- [ ] **Step 3: Implementierung** — `internal/scheduler/scheduler.go`

`RunNow` aufteilen: Der bisherige Rumpf ab `s.log.Info("routine started", ...)` wandert in `run`. Ergebnis:

```go
// RunNow executes one routine immediately (used by cron and the "jetzt
// ausführen" button). Always records the outcome.
func (s *Scheduler) RunNow(ctx context.Context, routineID int64) {
	if ctx == nil {
		ctx = context.Background()
	}
	r, err := s.store.GetRoutine(ctx, routineID)
	if err != nil {
		s.log.Error("routine vanished", "id", routineID, "err", err)
		return
	}
	s.run(ctx, r)
}

// RunAdhoc executes a routine definition that is not stored (z. B.
// „Anwenden + Neustart" im Mods-Tab) — gleiche Kette, gleiche Meldungen,
// aber kein Historien-Eintrag (ID 0). Läuft synchron.
func (s *Scheduler) RunAdhoc(ctx context.Context, r storage.Routine) {
	r.ID = 0
	s.run(ctx, r)
}

func (s *Scheduler) run(ctx context.Context, r storage.Routine) {
	var err error
	s.log.Info("routine started", "name", r.Name, "kind", r.Kind)
	// … unveränderter bisheriger Rumpf von RunNow ab hier (msg/affected,
	// Wartungsfenster-Prüfung, execute, switch mit Discord-Meldungen) …
	// Am Ende statt des bisherigen RecordRun-Blocks:
	if r.ID == 0 {
		return // Ad-hoc-Lauf: keine Historie
	}
	if err := s.store.RecordRun(ctx, run); err != nil {
		s.log.Error("record run failed", "routine", r.Name, "err", err)
	}
}
```

Konkret: In der bestehenden Funktion `RunNow` die Zeilen `r, err := s.store.GetRoutine(...)` bis `return }` stehen lassen, danach `s.run(ctx, r)` und schließende Klammer; den Rest (ab `s.log.Info("routine started"...)` bis zum Ende) in die neue Funktion `run` verschieben, `var err error` an deren Anfang, und den `RecordRun`-Block durch die oben gezeigte `r.ID == 0`-Variante ersetzen.

`NextApply` am Dateiende ergänzen:

```go
// NextApply returns the earliest enabled routine that applies staged mod
// changes (announce-restart/backup mit ApplyStaged) — für den Hinweis
// „Wird aktiv: …" im Mods-Tab.
func (s *Scheduler) NextApply(ctx context.Context, now time.Time) (string, time.Time, bool, error) {
	routines, err := s.store.ListRoutines(ctx)
	if err != nil {
		return "", time.Time{}, false, err
	}
	var (
		name string
		at   time.Time
		ok   bool
	)
	for _, r := range routines {
		if !r.Enabled || !r.ApplyStaged || (r.Kind != "announce-restart" && r.Kind != "backup") {
			continue
		}
		sched, err := cron.ParseStandard(r.Cron)
		if err != nil {
			continue
		}
		next := sched.Next(now)
		if !ok || next.Before(at) {
			name, at, ok = r.Name, next, true
		}
	}
	return name, at, ok, nil
}
```

- [ ] **Step 4: Tests laufen lassen**

Run: `go test ./internal/scheduler/`
Expected: `ok` (alle bisherigen Scheduler-Tests weiter grün).

- [ ] **Step 5: Commit**

```bash
git add internal/scheduler
git commit -m "feat: scheduler runs ad-hoc routines and reports the next staged apply

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: Ein-Klick-Update sperrt bei Vorgemerktem

**Files:**
- Modify: `internal/upgrade/upgrade.go` (Feld + Prüfung in `Start`)
- Modify: `internal/upgrade/upgrade_test.go` (neuer Test)

**Interfaces:**
- Produces: `Orchestrator.PendingChanges func() bool` (optional; true = Start verweigern)

- [ ] **Step 1: Failing Test** — ans Ende von `internal/upgrade/upgrade_test.go`:

```go
func TestUpgradeRefusesWithPendingModChanges(t *testing.T) {
	f := &fakes{backupOK: true}
	f.online.Store(true)
	o := newOrch(f, events.New())
	o.PendingChanges = func() bool { return true }
	err := o.Start("26.2")
	if err == nil || !strings.Contains(err.Error(), "vorgemerkt") {
		t.Fatalf("err = %v, want Hinweis auf vorgemerkte Änderungen", err)
	}
	if steps := f.list(); len(steps) != 0 {
		t.Errorf("Kette lief trotzdem an: %v", steps)
	}
}
```

(`strings` ist in der Testdatei bereits importiert; falls nicht, Import ergänzen.)

- [ ] **Step 2: Laufen lassen, muss scheitern**

Run: `go test ./internal/upgrade/ -run PendingModChanges`
Expected: Compile-Fehler `o.PendingChanges undefined`.

- [ ] **Step 3: Implementierung** — `internal/upgrade/upgrade.go`

Im `Orchestrator` nach `HostFailure func() (string, bool)` einfügen:

```go
	// PendingChanges meldet manuell vorgemerkte Mod-Änderungen (Mods-Tab).
	// Sie gehören zur ALTEN Version — ein Sprung würde sie mit einspielen.
	// Optional; true = Start verweigern.
	PendingChanges func() bool
```

In `Start` direkt nach dem `if o.running { … }`-Block einfügen:

```go
	if o.PendingChanges != nil && o.PendingChanges() {
		o.mu.Unlock()
		return fmt.Errorf("im Mods-Tab sind Änderungen vorgemerkt — erst anwenden oder verwerfen, dann das Update starten")
	}
```

- [ ] **Step 4: Tests laufen lassen**

Run: `go test ./internal/upgrade/`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/upgrade
git commit -m "feat: one-click upgrade refuses to start while mod changes are pending

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: API-Endpunkte

**Files:**
- Create: `internal/api/modsadd.go`
- Create: `internal/api/modsadd_test.go`
- Modify: `internal/api/api.go` (Deps/Server-Felder, Routen, `mcVersion` nil-sicher)
- Modify: `internal/api/mods.go` (`handleModsApply`, `handlePublishClientPack`)

**Interfaces:**
- Consumes: Task 3/4 (`Search`, `Plan`, `AddMods`, `RemovalInfo`, `StageRemove`, `StageUpload`, `MaxUploadBytes`), Task 2 (`Discard`, `HasStaged`), Task 6 (`RunAdhoc`, `NextApply`).
- Produces (HTTP, alle JSON):
  - `GET /api/mods/search?q=` → `{"mcVersion": "26.3", "hits": [SearchHit…]}`
  - `GET /api/mods/plan?project=` → `AddPlan`
  - `POST /api/mods/add` `{"projectId","profiles":[…]}` → `AddResult`
  - `POST /api/mods/upload` multipart (`file`, `profile` mehrfach, `category`) → `Entry` (des letzten Profils)
  - `GET /api/mods/remove-info?profile=&file=` → `RemovalInfo`
  - `POST /api/mods/remove` `{"profile","filename"}` → `{"ok":true}`
  - `POST /api/mods/discard` `{"profile","filename"}` → `{"ok":true}`
  - `GET /api/mods/apply-info` → `{"routine": "Nacht-Backup", "next": "<RFC3339>"}` bzw. `{"routine": ""}`
  - `POST /api/mods/apply` mit `restart:true` (Server) → `202 {"scheduled":true,"message":…}`
  - `api.Deps.PublishPack func(ctx context.Context) error`

- [ ] **Step 1: Failing Tests** — `internal/api/modsadd_test.go`

```go
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
```

- [ ] **Step 2: Laufen lassen, muss scheitern**

Run: `go test ./internal/api/ -run 'ModsSearch|ModsRemove|ModsUpload|ApplyInfo'`
Expected: 404/Panic (Routen fehlen; `mcVersion` greift auf nil-`collector` zu).

- [ ] **Step 3: `internal/api/api.go` anpassen**

`Server`-Struct: nach `upgrader *upgrade.Orchestrator` einfügen:

```go
	publishPack       func(ctx context.Context) error // Client-Paket-Versand inkl. Zustands-Pflege
	applyBusy         atomic.Bool                     // „Anwenden + Neustart" läuft
```

`Deps`: nach `Upgrader *upgrade.Orchestrator` einfügen:

```go
	PublishPack       func(ctx context.Context) error // optional; nil = dropbox.Publish direkt
```

In `New(...)` im Rückgabe-Literal nach `upgrader: d.Upgrader,` einfügen: `publishPack: d.PublishPack,`. Imports `context` und `sync/atomic` ergänzen (falls nicht vorhanden).

Im Routen-Block `if s.modmgr != nil {` nach der Rollback-Route einfügen:

```go
		mux.HandleFunc("GET /api/mods/search", s.handleModsSearch)
		mux.HandleFunc("GET /api/mods/plan", s.handleModsPlan)
		mux.HandleFunc("POST /api/mods/add", s.handleModsAdd)
		mux.HandleFunc("POST /api/mods/upload", s.handleModsUpload)
		mux.HandleFunc("GET /api/mods/remove-info", s.handleModsRemoveInfo)
		mux.HandleFunc("POST /api/mods/remove", s.handleModsRemove)
		mux.HandleFunc("POST /api/mods/discard", s.handleModsDiscard)
		mux.HandleFunc("GET /api/mods/apply-info", s.handleModsApplyInfo)
```

In `internal/api/mods.go` `mcVersion` ersetzen:

```go
func (s *Server) mcVersion() string {
	if s.collector != nil {
		if v := s.collector.MCVersion(); v != "" {
			return v
		}
	}
	return s.fallbackMCVersion
}
```

- [ ] **Step 4: Handler** — `internal/api/modsadd.go`

```go
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mods"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/storage"
)

// needVersion writes 409 when the MC version is unknown (Server startet).
func (s *Server) needVersion(w http.ResponseWriter) (string, bool) {
	v := s.mcVersion()
	if v == "" {
		httpError(w, http.StatusConflict, "Minecraft-Version unbekannt (Server startet noch?) — in 1–2 Minuten erneut versuchen")
		return "", false
	}
	return v, true
}

func (s *Server) handleModsSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if n := len([]rune(q)); n < 2 || n > 100 {
		httpError(w, http.StatusBadRequest, "Suchbegriff: 2–100 Zeichen")
		return
	}
	version, ok := s.needVersion(w)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	hits, err := s.modmgr.Search(ctx, q, version)
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"mcVersion": version, "hits": hits})
}

func (s *Server) handleModsPlan(w http.ResponseWriter, r *http.Request) {
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	if project == "" || len(project) > 64 {
		httpError(w, http.StatusBadRequest, "project fehlt")
		return
	}
	version, ok := s.needVersion(w)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	plan, err := s.modmgr.Plan(ctx, project, version)
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (s *Server) handleModsAdd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProjectID string   `json:"projectId"`
		Profiles  []string `json:"profiles"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.ProjectID == "" {
		httpError(w, http.StatusBadRequest, "projectId und profiles angeben")
		return
	}
	version, ok := s.needVersion(w)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	res, err := s.modmgr.AddMods(ctx, req.ProjectID, version, req.Profiles)
	if err != nil {
		s.audit(r.Context(), "mods.add.failed", fmt.Sprintf("project=%s err=%s", req.ProjectID, err))
		httpError(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r.Context(), "mods.add", fmt.Sprintf("project=%s added=%v", req.ProjectID, res.Added))
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleModsUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, mods.MaxUploadBytes+(1<<20))
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		httpError(w, http.StatusBadRequest, "Upload zu groß oder ungültig (max. 100 MB)")
		return
	}
	profiles := r.MultipartForm.Value["profile"]
	category := r.FormValue("category")
	if category == "" {
		category = "mods"
	}
	file, hdr, err := r.FormFile("file")
	if err != nil || len(profiles) == 0 {
		httpError(w, http.StatusBadRequest, "Datei und mindestens ein Profil angeben")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, mods.MaxUploadBytes+1))
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	var last mods.Entry
	for _, p := range profiles {
		e, err := s.modmgr.StageUpload(r.Context(), p, category, hdr.Filename, data)
		if err != nil {
			s.audit(r.Context(), "mods.upload.failed", fmt.Sprintf("profile=%s file=%s err=%s", p, hdr.Filename, err))
			httpError(w, http.StatusBadRequest, err.Error())
			return
		}
		last = e
		s.audit(r.Context(), "mods.upload", fmt.Sprintf("profile=%s file=%s managed=%v", p, hdr.Filename, e.Managed))
	}
	writeJSON(w, http.StatusOK, last)
}

func (s *Server) handleModsRemoveInfo(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	info, err := s.modmgr.RemovalInfo(ctx, r.URL.Query().Get("profile"), r.URL.Query().Get("file"))
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

type profileFileReq struct {
	Profile  string `json:"profile"`
	Filename string `json:"filename"`
}

func decodeProfileFile(w http.ResponseWriter, r *http.Request) (profileFileReq, bool) {
	var req profileFileReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid json")
		return req, false
	}
	return req, true
}

func (s *Server) handleModsRemove(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeProfileFile(w, r)
	if !ok {
		return
	}
	if err := s.modmgr.StageRemove(req.Profile, req.Filename); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r.Context(), "mods.remove", fmt.Sprintf("profile=%s file=%s", req.Profile, req.Filename))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleModsDiscard(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeProfileFile(w, r)
	if !ok {
		return
	}
	if err := s.modmgr.Discard(req.Profile, req.Filename); err != nil {
		httpError(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r.Context(), "mods.discard", fmt.Sprintf("profile=%s file=%s", req.Profile, req.Filename))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleModsApplyInfo(w http.ResponseWriter, r *http.Request) {
	if s.sched == nil {
		writeJSON(w, http.StatusOK, map[string]any{"routine": ""})
		return
	}
	name, at, ok, err := s.sched.NextApply(r.Context(), time.Now())
	if err != nil || !ok {
		writeJSON(w, http.StatusOK, map[string]any{"routine": ""})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"routine": name, "next": at})
}

// startAnnouncedApply runs „Anwenden + Neustart" über den Scheduler: 5 min
// Vorwarnung, save-all, Stop → Tausch → Start, Watchdog. Vorher startete
// der Button den Server sofort ohne Warnung neu.
func (s *Server) startAnnouncedApply(w http.ResponseWriter, r *http.Request) {
	if !s.modmgr.HasStaged(mods.ProfileServer) {
		httpError(w, http.StatusConflict, "für den Server ist nichts vorgemerkt")
		return
	}
	if !s.applyBusy.CompareAndSwap(false, true) {
		httpError(w, http.StatusConflict, "Einspielen läuft bereits")
		return
	}
	s.audit(r.Context(), "mods.apply", "profile=server (angekündigter Neustart, 5 min)")
	go func() {
		defer s.applyBusy.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		s.sched.RunAdhoc(ctx, storage.Routine{
			Name: "Mod-Änderungen einspielen", Kind: "announce-restart", Payload: s.mcContainer,
			WarnMinutes: 5, ApplyStaged: true, WatchdogMinutes: 10,
		})
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{
		"scheduled": true,
		"message":   "Server startet in 5 Minuten neu (Spieler werden im Spiel gewarnt) — danach sind die Änderungen aktiv.",
	})
}
```

- [ ] **Step 5: `handleModsApply` und Publish anpassen** — `internal/api/mods.go`

In `handleModsApply` direkt nach `if req.Profile == "" { req.Profile = "server" }` einfügen:

```go
	if req.Restart && req.Profile == mods.ProfileServer && s.sched != nil {
		s.startAnnouncedApply(w, r)
		return
	}
```

In `handlePublishClientPack` die Zeile `if err := dropbox.Publish(ctx, s.dropbox, clientDirs, s.bus, s.log); err != nil {` ersetzen durch:

```go
			publish := func(ctx context.Context) error { return dropbox.Publish(ctx, s.dropbox, clientDirs, s.bus, s.log) }
			if s.publishPack != nil {
				publish = s.publishPack
			}
			if err := publish(ctx); err != nil {
```

- [ ] **Step 6: Tests laufen lassen**

Run: `go vet ./internal/api/ && go test ./internal/api/`
Expected: `ok`.

- [ ] **Step 7: Commit**

```bash
git add internal/api
git commit -m "feat: API for searching, adding, uploading, removing and discarding mods; announced apply restart

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: Verdrahtung in `main.go`

**Files:**
- Modify: `cmd/msm/main.go`
- Modify: `.env.example`

**Interfaces:**
- Consumes: Task 5 (`modnews.New`, `OnApply`, `Published`, `SetPublisher`, `SetBusy`, `Run`), Task 7 (`PendingChanges`), Task 8 (`Deps.PublishPack`).

- [ ] **Step 1: modnews nach dem Mod-Manager anlegen** — nach `sched.SetStagedApplier(modmgr)`:

```go
	// Mod-Neuigkeiten (Hinzufügen/Entfernen): sammelt angewendete
	// Änderungen und meldet sie nach dem nächsten Neustart in Discord
	news := modnews.New(kvGet, kvSet, bus, func() bool { return mcState().Online }, log)
	modmgr.SetChangeHook(news.OnApply)
```

Import `"github.com/TigerKnight555/Minecraft-Server-Management/internal/modnews"` ergänzen.

- [ ] **Step 2: PendingChanges am Upgrader** — nach `upgrader.ModRollback = modmgr.RollbackLabel`:

```go
		upgrader.PendingChanges = func() bool {
			return modmgr.HasStaged(mods.ProfileServer) || modmgr.HasStaged(mods.ProfileClient)
		}
```

- [ ] **Step 3: Dropbox-Block ersetzen** — den Block ab `dbx := dropbox.NewDynamic(cfg.DropboxConfig)` bis zur schließenden `}` des anschließenden `{ … }`-Blocks ersetzen durch:

```go
	dbx := dropbox.NewDynamic(cfg.DropboxConfig)
	var clientDirs map[string]string
	for _, p := range modProfiles {
		if p.Name == mods.ProfileClient {
			clientDirs = p.Dirs
		}
	}
	// publishPack ist der einzige Weg zum Paket-Versand (Button, MC-Update,
	// Mod-Neuigkeiten) — jeder Erfolg löscht „Paket veraltet".
	publishPack := func(ctx context.Context) error {
		if clientDirs == nil {
			return fmt.Errorf("kein Client-Profil konfiguriert")
		}
		if err := dropbox.Publish(ctx, dbx, clientDirs, bus, log); err != nil {
			return err
		}
		news.Published()
		return nil
	}
	if upgrader != nil && clientDirs != nil {
		// nach dem Ein-Klick-MC-Update das frische Client-Paket
		// automatisch verteilen — nur wenn Dropbox konfiguriert ist
		upgrader.Publish = func(ctx context.Context) (bool, error) {
			if !dbx.Ready() {
				return false, nil // Feature aus — kein Fehler
			}
			return true, publishPack(ctx)
		}
	}
	news.SetPublisher(dbx.Ready, publishPack)
	news.SetBusy(upgradeActive)
	go news.Run(ctx)
```

(`upgradeActive` ist oberhalb bereits definiert; `fmt` ist importiert — sonst ergänzen.)

- [ ] **Step 4: API-Deps** — in `api.Deps{…}` nach `Dropbox: dbx,` einfügen: `PublishPack: publishPack,`

- [ ] **Step 5: `.env.example`** — im Kommentar-Beispiel `MSM_DISCORD_WEBHOOKS` den Spieler-Filter erweitern:

```
#   MSM_DISCORD_WEBHOOKS=[{"name":"spieler","url":"https://discord.com/api/webhooks/...","events":["version.","mods.applied","mods.changed","mods.clientpack"]},{"name":"admin","url":"https://discord.com/api/webhooks/...","events":["*"]}]
```

- [ ] **Step 6: Bauen + Tests**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: alles `ok`.

- [ ] **Step 7: Mock-Modus starten und API grob prüfen**

Run (Git Bash, Hintergrund): `go run ./cmd/msm -mock -addr 127.0.0.1:8099` — dann `curl -s "localhost:8099/api/mods/search?q=jade"`
Expected: JSON mit `jade-mock`. Prozess danach beenden.

- [ ] **Step 8: Commit**

```bash
git add cmd/msm/main.go .env.example
git commit -m "feat: wire mod news, pack publisher and pending-change guard

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10: Oberfläche

**Files:**
- Modify: `web/src/lib/stream.js`
- Create: `web/src/lib/AddMod.svelte`
- Modify: `web/src/lib/Mods.svelte`

**Interfaces:**
- Consumes: HTTP-Endpunkte aus Task 8.

- [ ] **Step 1: `stream.js`** — nach `publishClientPack` einfügen:

```js
// --- Mods hinzufügen/entfernen ---
export const searchMods = (q) => apiFetch(`/api/mods/search?q=${encodeURIComponent(q)}`)
export const planMod = (project) => apiFetch(`/api/mods/plan?project=${encodeURIComponent(project)}`)
export const addMod = (projectId, profiles) =>
  apiFetch('/api/mods/add', { method: 'POST', body: JSON.stringify({ projectId, profiles }) })
export const removeInfo = (profile, file) =>
  apiFetch(`/api/mods/remove-info?profile=${encodeURIComponent(profile)}&file=${encodeURIComponent(file)}`)
export const removeMod = (profile, filename) =>
  apiFetch('/api/mods/remove', { method: 'POST', body: JSON.stringify({ profile, filename }) })
export const discardMod = (profile, filename) =>
  apiFetch('/api/mods/discard', { method: 'POST', body: JSON.stringify({ profile, filename }) })
export const applyInfo = () => apiFetch('/api/mods/apply-info')

// Upload: multipart, deshalb ohne apiFetch (das setzt JSON-Content-Type)
export async function uploadMod(file, profiles, category) {
  const fd = new FormData()
  fd.append('file', file)
  profiles.forEach((p) => fd.append('profile', p))
  fd.append('category', category)
  const resp = await fetch('/api/mods/upload', { method: 'POST', body: fd })
  if (resp.status === 401) {
    authenticated.set(false)
    throw new Error('nicht angemeldet')
  }
  const data = await resp.json().catch(() => ({}))
  if (!resp.ok) throw new Error(data.error ?? resp.statusText)
  return data
}
```

- [ ] **Step 2: `AddMod.svelte`** — neue Datei `web/src/lib/AddMod.svelte`

```svelte
<script>
  import { searchMods, planMod, addMod, uploadMod, applyInfo } from './stream.js'

  let { onchange } = $props()

  let query = $state('')
  let hits = $state([])
  let mcVersion = $state('')
  let searched = $state(false)
  let busy = $state(false)
  let error = $state('')
  let info = $state('')

  let plan = $state(null)
  let wantServer = $state(false)
  let wantClient = $state(false)
  let apply = $state(null)

  let upFile = $state(null)
  let upServer = $state(true)
  let upClient = $state(false)
  let upCategory = $state('mods')
  $effect(() => { if (upServer) upCategory = 'mods' })

  const sideLabel = { required: 'nötig', optional: 'optional', unsupported: 'nicht nötig', unknown: 'unbekannt' }

  const missing = (profile) => (plan?.deps ?? []).filter((d) => !d.present?.[profile])
  let depNames = $derived.by(() => {
    if (!plan) return []
    const names = new Set()
    if (wantServer) missing('server').forEach((d) => names.add(d.name))
    if (wantClient) missing('client').forEach((d) => names.add(d.name))
    return [...names]
  })
  let canAdd = $derived(plan && !plan.blocked && (wantServer || wantClient) && !busy)

  async function guard(fn) {
    busy = true
    error = ''
    info = ''
    try {
      await fn()
    } catch (err) {
      error = err.message
    } finally {
      busy = false
    }
  }

  function doSearch(ev) {
    ev.preventDefault()
    if (query.trim().length < 2) return
    guard(async () => {
      const res = await searchMods(query.trim())
      hits = res.hits
      mcVersion = res.mcVersion
      searched = true
    })
  }

  const openPlan = (hit) => guard(async () => {
    plan = await planMod(hit.project_id)
    wantServer = plan.server && !plan.main.present?.server
    wantClient = plan.client && !plan.main.present?.client
    apply = await applyInfo().catch(() => null)
  })

  const confirmAdd = () => guard(async () => {
    const profiles = []
    if (wantServer) profiles.push('server')
    if (wantClient) profiles.push('client')
    const res = await addMod(plan.main.projectId, profiles)
    info = 'Hinzugefügt — ' + Object.entries(res.added)
      .map(([p, names]) => `${p === 'server' ? 'Server (vorgemerkt)' : 'Client-Paket'}: ${names.join(', ')}`)
      .join(' · ')
    plan = null
    onchange?.()
  })

  const doUpload = () => guard(async () => {
    const profiles = []
    if (upServer) profiles.push('server')
    if (upClient) profiles.push('client')
    const res = await uploadMod(upFile, profiles, upCategory)
    info = `Hochgeladen: ${res.name}` + (res.managed
      ? ' (auf Modrinth erkannt — bekommt Updates)'
      : ' (unverwaltet — keine automatischen Updates)')
    upFile = null
    onchange?.()
  })

  function applyText() {
    if (!apply?.routine) {
      return '⚠️ Keine Routine übernimmt Vorgemerktes — „Anwenden + Neustart" nutzen oder in einer Routine „Vorgemerkte Updates anwenden" aktivieren.'
    }
    return `Wird aktiv: ${new Date(apply.next).toLocaleString('de-DE')} (Routine „${apply.routine}") — oder sofort per „Anwenden + Neustart".`
  }
</script>

<div class="panel wide">
  <h2>Mod hinzufügen</h2>
  {#if error}<div class="err-msg">{error}</div>{/if}
  {#if info}<div class="info-msg">{info}</div>{/if}

  <form class="row" onsubmit={doSearch}>
    <input type="text" bind:value={query} placeholder="Auf Modrinth suchen, z. B. Jade" />
    <button class="btn" type="submit" disabled={busy || query.trim().length < 2}>Suchen</button>
  </form>

  {#if plan}
    <div class="dialog">
      <h3>{plan.main.name} <span class="dim-sm">{plan.main.versionNumber}</span></h3>
      {#if plan.blocked}
        <div class="err-msg">{plan.blocked}</div>
      {:else}
        <div class="row">
          <label>
            <input type="checkbox" bind:checked={wantServer} disabled={plan.main.present?.server} />
            Server
            <span class="dim-sm">({plan.main.present?.server ? 'schon vorhanden' : 'Modrinth: ' + (sideLabel[plan.main.serverSide] ?? plan.main.serverSide)})</span>
          </label>
          <label>
            <input type="checkbox" bind:checked={wantClient} disabled={plan.main.present?.client} />
            Client-Paket
            <span class="dim-sm">({plan.main.present?.client ? 'schon vorhanden' : 'Modrinth: ' + (sideLabel[plan.main.clientSide] ?? plan.main.clientSide)})</span>
          </label>
        </div>
        {#if depNames.length}
          <p>Wird mit installiert: <strong>{depNames.join(', ')}</strong></p>
        {/if}
        {#each plan.warnings as w}
          <div class="warn-msg">⚠️ {w}</div>
        {/each}
        {#if wantServer}<p class="dim-sm">{applyText()}</p>{/if}
        {#if wantClient}<p class="dim-sm">Client-Paket: sofort drin, wird beim nächsten Neustart automatisch an die Spieler verteilt.</p>{/if}
      {/if}
      <div class="row">
        <button class="btn accent" onclick={confirmAdd} disabled={!canAdd}>Hinzufügen</button>
        <button class="btn" onclick={() => (plan = null)} disabled={busy}>Abbrechen</button>
      </div>
    </div>
  {/if}

  {#if hits.length}
    <ul class="hits">
      {#each hits as h (h.project_id)}
        <li>
          {#if h.icon_url}<img src={h.icon_url} alt="" width="32" height="32" />{:else}<span class="noicon"></span>{/if}
          <div class="hit-text">
            <strong>{h.title}</strong>
            <span class="dim-sm">{(h.downloads ?? 0).toLocaleString('de-DE')} Downloads</span>
            <div class="dim-sm">{h.description}</div>
          </div>
          <button class="btn sm" onclick={() => openPlan(h)} disabled={busy}>Auswählen</button>
        </li>
      {/each}
    </ul>
  {:else if searched}
    <div class="empty">Keine Treffer mit Build für Minecraft {mcVersion}.</div>
  {/if}

  <h3>Datei hochladen <span class="dim-sm">(Notlösung, z. B. Mods von CurseForge)</span></h3>
  <div class="row">
    <input type="file" accept=".jar,.zip" onchange={(e) => (upFile = e.currentTarget.files?.[0] ?? null)} />
    <label><input type="checkbox" bind:checked={upServer} /> Server</label>
    <label><input type="checkbox" bind:checked={upClient} /> Client-Paket</label>
    {#if upClient && !upServer}
      <select bind:value={upCategory}>
        <option value="mods">Mod (.jar)</option>
        <option value="shaderpacks">Shader (.zip)</option>
        <option value="resourcepacks">Resourcepack (.zip)</option>
      </select>
    {/if}
    <button class="btn" onclick={doUpload} disabled={busy || !upFile || (!upServer && !upClient)}>Hochladen</button>
  </div>
</div>

<style>
  .row { display: flex; gap: 0.6rem; align-items: center; flex-wrap: wrap; margin-bottom: 0.5rem; }
  input[type='text'] { min-width: min(22rem, 100%); }
  .btn {
    background: var(--panel-border); border: none; border-radius: 5px;
    color: var(--text); padding: 0.4rem 0.9rem; cursor: pointer; font-size: 0.85rem;
  }
  .btn:hover { background: #3a4552; }
  .btn:disabled { opacity: 0.4; cursor: default; }
  .btn.accent { background: var(--accent); color: #0f1419; font-weight: 600; }
  .btn.sm { padding: 0.2rem 0.6rem; font-size: 0.75rem; }
  .dialog { border: 1px solid var(--accent); border-radius: 6px; padding: 0.6rem 0.8rem; margin: 0.6rem 0; }
  .dialog h3 { margin: 0 0 0.5rem; }
  .hits { list-style: none; padding: 0; margin: 0.4rem 0 0.8rem; }
  .hits li { display: flex; gap: 0.6rem; align-items: center; padding: 0.4rem 0; border-bottom: 1px solid var(--panel-border); }
  .hits img, .noicon { width: 32px; height: 32px; border-radius: 4px; flex: none; background: var(--panel-border); }
  .hit-text { flex: 1; min-width: 0; }
  .dim-sm { font-size: 0.75rem; color: var(--text-dim); }
  .err-msg { color: var(--err); font-size: 0.85rem; margin-bottom: 0.6rem; }
  .info-msg { color: var(--accent); font-size: 0.85rem; margin-bottom: 0.6rem; }
  .warn-msg { color: var(--warn); font-size: 0.85rem; margin-bottom: 0.3rem; }
</style>
```

- [ ] **Step 3: `Mods.svelte` anpassen**

Imports ergänzen:

```js
  import AddMod from './AddMod.svelte'
  import {
    listMods, checkMods, stageMods, applyMods, rollbackMods,
    versionWatch, versionWatchCheck, publishClientPack,
    removeInfo, removeMod, discardMod,
  } from './stream.js'
```

`stagedCount` ersetzen (zählt jetzt alle Vormerkungen):

```js
  let stagedCount = $derived(entries.filter((e) => e.pending).length)
```

Nach `doWatchCheck` einfügen:

```js
  const profileLabel = (p) => (p === 'server' ? 'Server' : 'Client-Paket')

  async function doRemove(e) {
    let info
    try {
      info = await removeInfo(profile, e.filename)
    } catch (err) {
      error = err.message
      return
    }
    let msg = `${info.name} aus dem ${profileLabel(profile)} entfernen?`
    if (profile === 'server') msg += '\nWird beim nächsten Neustart wirksam (Datei wandert ins Backup, Rollback möglich).'
    if (info.dependents.length) msg += `\n\n⚠️ Wird gebraucht von: ${info.dependents.join(', ')}`
    if (!confirm(msg)) return
    const also = info.alsoIn.filter((a) => confirm(`${info.name} auch aus dem ${profileLabel(a.profile)} entfernen?`))
    run(async () => {
      await removeMod(profile, e.filename)
      for (const a of also) await removeMod(a.profile, a.filename)
    }, () => (profile === 'server' ? 'Entfernen vorgemerkt' : 'Entfernt') + (also.length ? ' (auch im anderen Profil)' : ''))
  }

  const doDiscard = (e) => run(() => discardMod(profile, e.filename), () => `Vormerkung für ${e.name || e.filename} verworfen`)
```

`doApply` ersetzen:

```js
  const doApply = () => {
    const restart = profile === 'server'
    const msg = restart
      ? 'Vorgemerkte Änderungen einspielen? Der Server startet mit 5 Minuten Vorwarnung neu.'
      : 'Gestagte Updates ins Client-Paket einsetzen?'
    if (!confirm(msg)) return
    run(() => applyMods(profile, restart),
      (r) => r.message ?? `${r.applied} Update(s) eingesetzt (Backup ${r.backup})`)
  }
```

Direkt nach dem ersten Panel (`Minecraft-Version`) einfügen:

```svelte
<AddMod onchange={load} />
```

Im Tabellenkörper die Badge-Zelle ersetzen:

```svelte
            <td>
              {#if e.pending === 'add'}
                <span class="badge staged">neu · vorgemerkt</span>
              {:else if e.pending === 'remove'}
                <span class="badge remove">wird entfernt</span>
              {:else if e.staged}
                <span class="badge staged">gestaged</span>
              {:else if e.updateVersion}
                <span class="badge update">→ {e.updateVersion}</span>
              {:else if e.managed}
                <span class="badge ok">aktuell</span>
              {:else}
                <span class="badge unmanaged" title="Nicht auf Modrinth gefunden — wird nie angefasst">unverwaltet</span>
              {/if}
            </td>
            <td class="row-actions">
              {#if e.pending}
                <button class="btn sm" onclick={() => doDiscard(e)} disabled={busy}>Verwerfen</button>
              {:else}
                {#if e.updateVersion}
                  <button class="btn sm" onclick={() => doStageOne(e.filename)} disabled={busy}>Stagen</button>
                {/if}
                <button class="btn sm danger" onclick={() => doRemove(e)} disabled={busy}>Entfernen</button>
              {/if}
            </td>
```

Die leere-Liste-Bedingung `{#if entries.length === 0}` bleibt; vorgemerkte Neuzugänge erscheinen auch ohne Check, weil `Entries` sie anhängt.

Im `<style>` ergänzen:

```css
  .badge.remove { background: #3a1c1c; color: var(--err); }
  .row-actions { white-space: nowrap; }
  .row-actions .btn { margin-right: 0.3rem; }
```

- [ ] **Step 4: Bauen**

Run: `cd web && npm run build`
Expected: `✓ built in …`, keine Svelte-Warnungen zu den neuen Dateien (Warnungen lesen und beheben).

- [ ] **Step 5: Im Mock-Modus durchklicken**

`go run ./cmd/msm -mock …` starten (Flags wie in Task 9 Step 7), im Browser-Pane öffnen: Suche „jade" → Auswählen → Dialog zeigt „Wird mit installiert: Balm (Mock)", Haken Client gesetzt → Hinzufügen → Erfolgsmeldung; Server-Profil zeigt „neu · vorgemerkt" + Verwerfen; Entfernen-Dialog erscheint; Upload einer `.zip` als Mod wird mit Fehlermeldung abgelehnt. Screenshot ansehen, Layout bei schmaler Breite prüfen.

- [ ] **Step 6: Commit**

```bash
git add web/src/lib/stream.js web/src/lib/AddMod.svelte web/src/lib/Mods.svelte
git commit -m "feat: UI for searching, adding, uploading, removing and discarding mods

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 11: Echter Lauf in der WSL-Testumgebung + Doku

**Files:**
- Vault: `MSM – Mods hinzufügen & entfernen.md` (Status), `MSM – Mod-Verwaltung.md`, `MSM – Nutzungshandbuch.md`, `MSM – Architektur & Code-Struktur.md`
- Scratchpad-Skripte (nicht im Repo)

- [ ] **Step 1: Stand in WSL bringen** — in WSL `cd ~/Minecraft-Server-Management && git fetch origin && git checkout feature/mods-add-remove && git reset --hard origin/feature/mods-add-remove` (origin = Windows-Repo; vorher prüfen, dass dort nur die bekannten lokalen Kopien liegen: `git status -s` → nur `docker-compose.override.yml` untracked; sonst nachfragen). Danach `docker compose build -q msm && docker compose up -d --no-deps msm`. Server auf 26.3 online lassen.

- [ ] **Step 2: Hinzufügen über die Oberfläche** (Browser-Pane, `localhost:8080`):
  1. Server-only-Mod suchen und hinzufügen (z. B. „Spark" oder ein anderer Mod mit `server_side` required/optional, `client_side` unsupported/optional) → Server-Zeile „neu · vorgemerkt", Hinweis „Wird aktiv: …".
  2. Client-only-Mod (z. B. „Mod Menu") → sofort in `~/minecraft/client-pack/mods`.
  3. Mod für beide Seiten mit Abhängigkeit (z. B. „Waystones" → Balm) → Dialog zeigt die Abhängigkeit, Server vorgemerkt, Client sofort.
  4. Einen vorhandenen Server-Mod entfernen (vorgemerkt) und eine Vormerkung verwerfen.

- [ ] **Step 3: Anwenden + Neustart** → Vorwarnungen im MC-Log (`docker logs mc-fabric | grep -i neustart`), Server läuft danach mit den neuen Mods (`docker logs mc-fabric | grep -iE "Loading [0-9]+ mods|waystones"`), Discord-Mitschnitt `~/minecraft/discord.log` enthält „🧩 Neue Mods auf dem Server" mit korrekten Kennzeichnungen und danach „📦 Neues Mod-Paket" mit Link (Dropbox ist eingerichtet). Link-ZIP-Inhalt vom Nutzer prüfen lassen.

- [ ] **Step 4: Rollback** im Mods-Tab (Server) → hinzugefügte Mods wieder raus, entfernter Mod wieder da; Server neu starten, startet sauber.

- [ ] **Step 5: Nacht-Routine-Pfad** — erneut einen Mod hinzufügen, dann die Routine mit „Vorgemerkte Updates anwenden" im Routinen-Tab per „Jetzt ausführen" starten → gleiche Meldungen wie in Step 3.

- [ ] **Step 6: Sperre beim MC-Update** — mit einer Vormerkung den Ein-Klick-Update-Button drücken (Welt vorher per `reset-to.sh 1.21.11` zurücksetzen) → Fehlermeldung „im Mods-Tab sind Änderungen vorgemerkt …".

- [ ] **Step 7: Doku im Vault** — Ergebnis jedes Laufs als Nachtrag in „MSM – Mods hinzufügen & entfernen" (Status → umgesetzt/getestet), Nutzungshandbuch-Abschnitt „Mods hinzufügen/entfernen" (Bedienung), „MSM – Mod-Verwaltung" verlinken, Architektur-Notiz um `internal/modnews`, neue Routen, app_state-Schlüssel, Event `mods.changed` ergänzen. Gefundene Fehler mit Learning-Nummer festhalten.

- [ ] **Step 8: Abschluss-Commit (falls Fixes aus dem Testlauf)** und Übergabe an den Nutzer: Branch-Stand, offene Punkte, nächster Schritt (Merge nach develop + Release nach Gitflow).
