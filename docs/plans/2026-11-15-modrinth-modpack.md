# Modrinth-Modpack automatisch veröffentlichen – Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** MSM veröffentlicht das Client-Paket bei jeder Änderung zusätzlich als neue Version eines eigenen Modrinth-Modpacks (`.mrpack`), damit Spieler in der Modrinth App nur noch „Update" klicken — parallel zum bestehenden Dropbox-ZIP.

**Architecture:** Ein neues Paket `internal/packpub` macht den Paket-Versand kanalfähig: jeder Kanal (Dropbox, Modrinth) implementiert ein kleines Interface, `packpub.Publisher` führt die eingerichteten Kanäle nacheinander aus, merkt sich je Kanal den Fingerabdruck des zuletzt verschickten Inhalts (überspringt bereits aktuelle Kanäle, holt gescheiterte nach) und postet EINE gemeinsame 📦-Meldung. `internal/mrpack` baut die `.mrpack`-Datei, `internal/modpack` ist der Modrinth-Kanal (Dateien auflösen, Versionsnummer zählen, hochladen). `publishPack` in `cmd/msm/main.go` bleibt der einzige Versandweg und meldet `modnews` nur dann „aktuell", wenn alle Kanäle erfolgreich waren.

**Tech Stack:** Go 1.26 (stdlib: `archive/zip`, `mime/multipart`, `crypto/sha512`), Svelte 5 (Runes), Modrinth API v2, SQLite app_state (über `kvGet`/`kvSet`).

**Spec:** `docs/specs/2026-11-15-modrinth-modpack-design.md` (Vault-Original: „MSM – Modrinth-Modpack").

**Abweichung von der Spec (bewusst, gleiche Wirkung):** Der Zustand „Paket aktuell je Kanal" liegt nicht als zusätzliche Schlüssel in `modnews`, sondern im neuen `packpub` (`clientpack.published.<kanal>` = Fingerabdruck). `modnews` bleibt unverändert: sein „veraltet"-Flag wird nur gelöscht, wenn alle Kanäle erfolgreich waren; beim nächsten Neustart überspringt `packpub` die schon aktuellen Kanäle und holt nur den gescheiterten nach. Der Changelog wird aus dem Vergleich mit der zuletzt veröffentlichten Modpack-Version gebaut (genauer als die Neuigkeiten-Liste, die Neustarts ohne Modpack-Versand nicht kennt).

## Global Constraints

- Gitflow: Arbeit auf `feature/modrinth-modpack` (von `develop`). Commit-Messages im Stil `feat:`/`fix:`/`test:`/`docs:`, Endzeile `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Nutzer-sichtbare Texte (UI, Discord, Fehlermeldungen) auf Deutsch, spielertauglich; Code-Bezeichner englisch.
- Versionsnummer `MAJOR.MINOR.PATCH`: andere MC-Version → MAJOR; andere Loader-Version oder Projekte dazu/weg → MINOR; nur andere Dateiversionen → PATCH. Startwert Standard `1.0.0`, erste Veröffentlichung nutzt ihn unverändert; liegt auf Modrinth eine höhere Version, wird von dieser gezählt.
- Versionsname: `<version> (MC <mc> · Fabric <loader>)`, z. B. `1.5.0 (MC 26.3 · Fabric 0.20.1)`.
- `.mrpack`: ZIP mit `modrinth.index.json` an der Wurzel, `formatVersion: 1`, `game: "minecraft"`, `dependencies` = `minecraft` + `fabric-loader`, je Datei `sha1` + `sha512`, `fileSize`, `downloads` nur `https://cdn.modrinth.com/…`; Pfade ohne `..`, ohne führenden `/`; keine `overrides`. MIME `application/x-modrinth-modpack+zip`.
- Modrinth-Upload: `POST /version`, `multipart/form-data`, Teil `data` (JSON) + Teil `file`, Header `Authorization: <token>` (ohne „Bearer"), `loaders: ["fabric"]`, `version_type: "release"`, `featured: true`.
- Modrinth wird in der Spieler-Meldung nur genannt, wenn das Projekt öffentlich ist (Status `approved`, `unlisted` oder `archived`) und der Upload gelang.
- Nur ein Paket-Versand gleichzeitig (alle Kanäle zusammen).
- Einstellungen: Schlüssel `settings.modrinth.project`, `settings.modrinth.token`, `settings.modrinth.startversion`; `.env`-Rückfall `MSM_MODRINTH_PROJECT`, `MSM_MODRINTH_TOKEN`. Werte im Einstellungen-Tab im Klartext (Nutzer-Entscheidung), nie im Audit-Log.
- Modrinth-Query-Parameter mit JSON-Arrays immer über `url.Values` encodieren (Learning 14).
- Go-Tests: `go test ./...` und `go vet ./...` müssen am Ende jeder Task grün sein. Web: `cd web && npm run build` muss bauen.
- Doku-Pflicht: Vault (`10-Projekte/Software/Minecraft-Server-Manager/`) wird in Task 9 nachgezogen.

## File Structure

| Datei | Aktion | Verantwortung |
|---|---|---|
| `internal/modrinth/client.go` | ändern | `send` mit Token + beliebigem Body, `APIError`, `File.Hashes.SHA1`, `Project.Status`, `User`, `NewVersion`; `AuthProject`, `AuthProjectVersions`, `CurrentUser`, `CreateVersion` |
| `internal/modrinth/client_test.go` | ändern | Tests für Upload, Auth-Header, `APIError` |
| `internal/mrpack/mrpack.go` (+`_test`) | neu | `.mrpack` bauen, Pfad-Validierung |
| `internal/modpack/version.go` (+`_test`) | neu | `State`, `Next`, `Highest`, `ValidVersion`, `DetectLoader`, `Changelog` (rein, ohne I/O außer `DetectLoader`) |
| `internal/packpub/packpub.go` (+`_test`) | neu | Kanal-Interface, `Publisher` (Single-flight, Fingerabdruck je Kanal, gemeinsame Meldung) |
| `internal/dropbox/publish.go` | ändern | `Publish` → `Pack` (ohne Events) + `Channel` (Dropbox-Kanal für packpub) |
| `internal/dropbox/dropbox_test.go` | ändern | Single-flight-Test entfällt (wandert nach packpub) |
| `internal/modpack/channel.go` (+`_test`) | neu | Modrinth-Kanal: auflösen, zählen, bauen, hochladen; `Check`, `Status` |
| `internal/settings/settings.go` (+`_test`) | ändern | Modrinth-Schlüssel, `ModrinthConfig`, View/Values |
| `internal/api/api.go`, `mods.go`, `settings.go` (+Tests) | ändern | Kanal-Parameter am Versand, `GET /api/modpack`, `POST /api/settings/modrinth/test`, Einstellungen speichern |
| `cmd/msm/main.go` | ändern | Kanäle verdrahten, `publishPack` mit Kanälen |
| `web/src/lib/stream.js`, `Settings.svelte`, `Mods.svelte` | ändern | Modrinth-Felder + Anleitung + „Verbindung prüfen", Knopf + Hinweis im Mods-Tab |
| `docs/…`, README, Vault | ändern | Doku |

---

### Task 1: Modrinth-Client — Token, Upload, Projekt- und Nutzer-Abfrage

**Files:**
- Modify: `internal/modrinth/client.go`
- Test: `internal/modrinth/client_test.go`

**Interfaces:**
- Produces (für Task 5):
  - `type APIError struct { Path string; Status int; Body string }` mit `Error() string`
  - `File.Hashes.SHA1 string` (`json:"sha1"`), `Project.Status string` (`json:"status"`)
  - `type User struct { ID, Username string }`
  - `type NewVersion struct { Name, VersionNumber, Changelog string; Dependencies []Dependency; GameVersions []string; VersionType string; Loaders []string; Featured bool; ProjectID string; FileParts []string; PrimaryFile string }`
  - `func (c *Client) AuthProject(ctx context.Context, token, project string) (Project, error)`
  - `func (c *Client) AuthProjectVersions(ctx context.Context, token, project string) ([]Version, error)`
  - `func (c *Client) CurrentUser(ctx context.Context, token string) (User, error)`
  - `func (c *Client) CreateVersion(ctx context.Context, token string, v NewVersion, fileName string, data []byte) (Version, error)`

- [ ] **Step 1: Failing tests schreiben** — an `internal/modrinth/client_test.go` anhängen (Imports ergänzen: `errors`, `io`, `strings`):

```go
func TestCreateVersionMultipart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/version" {
			http.Error(w, "wrong route", http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "mrp_test" {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var meta NewVersion
		if err := json.Unmarshal([]byte(r.FormValue("data")), &meta); err != nil {
			http.Error(w, "data: "+err.Error(), http.StatusBadRequest)
			return
		}
		if meta.ProjectID != "P1" || meta.VersionNumber != "1.0.0" || len(meta.FileParts) != 1 ||
			meta.FileParts[0] != "file" || meta.PrimaryFile != "file" || meta.Dependencies == nil {
			http.Error(w, "bad meta", http.StatusBadRequest)
			return
		}
		f, hdr, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "file: "+err.Error(), http.StatusBadRequest)
			return
		}
		body, _ := io.ReadAll(f)
		if hdr.Filename != "pack-1.0.0.mrpack" || hdr.Header.Get("Content-Type") != "application/x-modrinth-modpack+zip" || string(body) != "ZIPDATA" {
			http.Error(w, "bad file part", http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(Version{ID: "V1", VersionNumber: "1.0.0"})
	}))
	defer srv.Close()

	c := NewWithBase(srv.URL)
	v, err := c.CreateVersion(context.Background(), "mrp_test", NewVersion{
		Name: "1.0.0 (MC 26.3 · Fabric 0.20.1)", VersionNumber: "1.0.0", GameVersions: []string{"26.3"},
		VersionType: "release", Loaders: []string{"fabric"}, Featured: true, ProjectID: "P1",
	}, "pack-1.0.0.mrpack", []byte("ZIPDATA"))
	if err != nil {
		t.Fatal(err)
	}
	if v.ID != "V1" {
		t.Errorf("version = %+v", v)
	}
}

func TestAPIErrorCarriesStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	_, err := NewWithBase(srv.URL).CurrentUser(context.Background(), "bad")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnauthorized {
		t.Fatalf("err = %v, want *APIError 401", err)
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("Error() = %q", err.Error())
	}
}

func TestAuthProjectSendsTokenAndStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/project/mein-pack" || r.Header.Get("Authorization") != "mrp_x" {
			http.Error(w, "nope", http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(Project{ID: "P1", Slug: "mein-pack", Title: "Mein Pack", ProjectType: "modpack", Status: "draft"})
	}))
	defer srv.Close()
	p, err := NewWithBase(srv.URL).AuthProject(context.Background(), "mrp_x", "mein-pack")
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "P1" || p.Status != "draft" || p.ProjectType != "modpack" {
		t.Errorf("project = %+v", p)
	}
}

func TestVersionFileSHA1Decoded(t *testing.T) {
	var v Version
	json.Unmarshal([]byte(`{"files":[{"url":"https://cdn.modrinth.com/x.jar","hashes":{"sha1":"aa","sha512":"bb"},"size":3}]}`), &v)
	if v.Files[0].Hashes.SHA1 != "aa" || v.Files[0].Hashes.SHA512 != "bb" {
		t.Errorf("hashes = %+v", v.Files[0].Hashes)
	}
}
```

- [ ] **Step 2: Tests laufen lassen, Fehlschlag prüfen**

Run: `go test ./internal/modrinth/`
Expected: FAIL (`undefined: NewVersion`, `CreateVersion`, `APIError`, …)

- [ ] **Step 3: Implementierung** in `internal/modrinth/client.go`:

Imports ergänzen: `"mime/multipart"`, `"net/textproto"`.

`File.Hashes` erweitern:

```go
	Hashes   struct {
		SHA512 string `json:"sha512"`
		SHA1   string `json:"sha1"`
	} `json:"hashes"`
```

`Project` um `Status string \`json:"status"\`` ergänzen (nach `ProjectType`).

Die bestehende `do`-Funktion ersetzen durch:

```go
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	ctype := ""
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(buf)
		ctype = "application/json"
	}
	return c.send(ctx, method, path, "", ctype, rdr, out)
}

// APIError is a non-2xx answer from Modrinth — Aufrufer prüfen den Status
// (401 = Token ungültig/ohne Recht, 400 = z. B. Versionsnummer vergeben).
type APIError struct {
	Path   string
	Status int
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("modrinth %s: %d %s: %s", e.Path, e.Status, http.StatusText(e.Status), e.Body)
}

// send performs one throttled request. token (optional) geht unverändert in
// den Authorization-Header — Modrinth erwartet den Personal Access Token
// ohne „Bearer"-Präfix.
func (c *Client) send(ctx context.Context, method, path, token, ctype string, body io.Reader, out any) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.throttle:
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("modrinth rate limit erreicht (429)")
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return &APIError{Path: path, Status: resp.StatusCode, Body: strings.TrimSpace(string(b))}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
```

Neue Typen und Methoden am Dateiende:

```go
// User is the token owner (GET /user).
type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// NewVersion is the `data` part of POST /version.
type NewVersion struct {
	Name          string       `json:"name"`
	VersionNumber string       `json:"version_number"`
	Changelog     string       `json:"changelog"`
	Dependencies  []Dependency `json:"dependencies"`
	GameVersions  []string     `json:"game_versions"`
	VersionType   string       `json:"version_type"`
	Loaders       []string     `json:"loaders"`
	Featured      bool         `json:"featured"`
	ProjectID     string       `json:"project_id"`
	FileParts     []string     `json:"file_parts"`
	PrimaryFile   string       `json:"primary_file"`
}

// AuthProject fetches a project with the owner's token — so sind auch noch
// nicht freigegebene Projekte (Entwurf) sichtbar.
func (c *Client) AuthProject(ctx context.Context, token, project string) (Project, error) {
	var p Project
	err := c.send(ctx, http.MethodGet, "/project/"+url.PathEscape(project), token, "", nil, &p)
	return p, err
}

// AuthProjectVersions lists all versions of a project (mit Token, auch Entwürfe).
func (c *Client) AuthProjectVersions(ctx context.Context, token, project string) ([]Version, error) {
	var out []Version
	err := c.send(ctx, http.MethodGet, "/project/"+url.PathEscape(project)+"/version", token, "", nil, &out)
	return out, err
}

// CurrentUser returns the token owner — Token-Prüfung für „Verbindung prüfen".
func (c *Client) CurrentUser(ctx context.Context, token string) (User, error) {
	var u User
	err := c.send(ctx, http.MethodGet, "/user", token, "", nil, &u)
	return u, err
}

// CreateVersion uploads one .mrpack as a new project version (POST /version).
func (c *Client) CreateVersion(ctx context.Context, token string, v NewVersion, fileName string, data []byte) (Version, error) {
	if v.Dependencies == nil {
		v.Dependencies = []Dependency{} // Modrinth will ein Array, kein null
	}
	v.FileParts = []string{"file"}
	v.PrimaryFile = "file"
	meta, err := json.Marshal(v)
	if err != nil {
		return Version{}, err
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("data", string(meta)); err != nil {
		return Version{}, err
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, fileName))
	h.Set("Content-Type", "application/x-modrinth-modpack+zip")
	part, err := mw.CreatePart(h)
	if err != nil {
		return Version{}, err
	}
	if _, err := part.Write(data); err != nil {
		return Version{}, err
	}
	if err := mw.Close(); err != nil {
		return Version{}, err
	}
	var out Version
	err = c.send(ctx, http.MethodPost, "/version", token, mw.FormDataContentType(), &buf, &out)
	return out, err
}
```

- [ ] **Step 4: Tests laufen lassen**

Run: `go test ./internal/modrinth/ ./internal/mods/ && go vet ./...`
Expected: PASS (bestehende Tests, die Fehlertexte wie „404 Not Found" prüfen, bleiben grün — `APIError.Error()` liefert dasselbe Format).

- [ ] **Step 5: Commit**

```bash
git add internal/modrinth
git commit -m "feat(modrinth): Token-Requests, Versions-Upload, Projekt-/Nutzerabfrage

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: `.mrpack` bauen

**Files:**
- Create: `internal/mrpack/mrpack.go`
- Test: `internal/mrpack/mrpack_test.go`

**Interfaces:**
- Produces (für Task 5):
  - `type File struct { Path, SHA1, SHA512, URL string; Size int64 }`
  - `type Meta struct { VersionID, Name, Summary, Minecraft, FabricLoader string }`
  - `const MediaType = "application/x-modrinth-modpack+zip"`
  - `func ValidPath(p string) bool`
  - `func Build(files []File, meta Meta) ([]byte, error)`

- [ ] **Step 1: Failing test schreiben** — `internal/mrpack/mrpack_test.go`:

```go
package mrpack

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"testing"
)

func readIndex(t *testing.T, data []byte) map[string]any {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 1 || zr.File[0].Name != "modrinth.index.json" {
		t.Fatalf("zip entries = %v, want nur modrinth.index.json", zr.File)
	}
	rc, _ := zr.File[0].Open()
	raw, _ := io.ReadAll(rc)
	var idx map[string]any
	if err := json.Unmarshal(raw, &idx); err != nil {
		t.Fatal(err)
	}
	return idx
}

func TestBuildIndex(t *testing.T) {
	data, err := Build([]File{
		{Path: "mods/sodium.jar", SHA1: "s1", SHA512: "s512", URL: "https://cdn.modrinth.com/data/a/sodium.jar", Size: 10},
		{Path: "resourcepacks/faithful.zip", SHA1: "f1", SHA512: "f512", URL: "https://cdn.modrinth.com/data/b/faithful.zip", Size: 20},
	}, Meta{VersionID: "1.5.0", Name: "Mein Pack", Minecraft: "26.3", FabricLoader: "0.20.1"})
	if err != nil {
		t.Fatal(err)
	}
	idx := readIndex(t, data)
	if idx["formatVersion"].(float64) != 1 || idx["game"] != "minecraft" || idx["versionId"] != "1.5.0" || idx["name"] != "Mein Pack" {
		t.Errorf("kopf = %v", idx)
	}
	deps := idx["dependencies"].(map[string]any)
	if deps["minecraft"] != "26.3" || deps["fabric-loader"] != "0.20.1" || len(deps) != 2 {
		t.Errorf("dependencies = %v", deps)
	}
	files := idx["files"].([]any)
	if len(files) != 2 {
		t.Fatalf("files = %v", files)
	}
	first := files[0].(map[string]any) // sortiert nach Pfad: mods/ vor resourcepacks/
	hashes := first["hashes"].(map[string]any)
	if first["path"] != "mods/sodium.jar" || hashes["sha1"] != "s1" || hashes["sha512"] != "s512" ||
		first["fileSize"].(float64) != 10 || first["downloads"].([]any)[0] != "https://cdn.modrinth.com/data/a/sodium.jar" {
		t.Errorf("file = %v", first)
	}
	if _, ok := first["env"]; ok {
		t.Error("env darf fehlen (alles ist Client-Inhalt)")
	}
}

func TestBuildRejectsBadInput(t *testing.T) {
	meta := Meta{VersionID: "1.0.0", Name: "P", Minecraft: "26.3", FabricLoader: "0.20.1"}
	for _, f := range []File{
		{Path: "../evil.jar", SHA1: "a", SHA512: "b", URL: "https://cdn.modrinth.com/x"},
		{Path: "/abs.jar", SHA1: "a", SHA512: "b", URL: "https://cdn.modrinth.com/x"},
		{Path: "C:/x.jar", SHA1: "a", SHA512: "b", URL: "https://cdn.modrinth.com/x"},
		{Path: "mods\\x.jar", SHA1: "a", SHA512: "b", URL: "https://cdn.modrinth.com/x"},
		{Path: "mods/x.jar", SHA1: "", SHA512: "b", URL: "https://cdn.modrinth.com/x"},
	} {
		if _, err := Build([]File{f}, meta); err == nil {
			t.Errorf("Build(%+v) ohne Fehler", f)
		}
	}
	if _, err := Build(nil, Meta{Name: "P"}); err == nil {
		t.Error("fehlende Pflicht-Metadaten nicht erkannt")
	}
}

func TestValidPath(t *testing.T) {
	for p, want := range map[string]bool{
		"mods/a.jar": true, "shaderpacks/x y.zip": true,
		"": false, "a/../b": false, "./a": false, "a//b": false,
	} {
		if got := ValidPath(p); got != want {
			t.Errorf("ValidPath(%q) = %v, want %v", p, got, want)
		}
	}
}
```

- [ ] **Step 2: Test laufen lassen, Fehlschlag prüfen**

Run: `go test ./internal/mrpack/`
Expected: FAIL (Paket existiert nicht / `undefined: Build`)

- [ ] **Step 3: Implementierung** — `internal/mrpack/mrpack.go`:

```go
// Package mrpack builds Modrinth modpack files (.mrpack): ein ZIP mit
// modrinth.index.json an der Wurzel, das jede Datei per Download-Link und
// Hashes beschreibt (Format-Version 1, Spiel „minecraft"). MSM braucht keine
// overrides — das Client-Paket besteht nur aus Modrinth-Inhalten.
package mrpack

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// MediaType is the MIME type Modrinth expects for .mrpack uploads.
const MediaType = "application/x-modrinth-modpack+zip"

// File is one entry of the index.
type File struct {
	Path   string // relativ zum Instanzordner, z. B. "mods/sodium.jar"
	SHA1   string
	SHA512 string
	URL    string
	Size   int64
}

// Meta is the pack header.
type Meta struct {
	VersionID    string
	Name         string
	Summary      string
	Minecraft    string
	FabricLoader string
}

type index struct {
	FormatVersion int               `json:"formatVersion"`
	Game          string            `json:"game"`
	VersionID     string            `json:"versionId"`
	Name          string            `json:"name"`
	Summary       string            `json:"summary,omitempty"`
	Files         []indexFile       `json:"files"`
	Dependencies  map[string]string `json:"dependencies"`
}

type indexFile struct {
	Path      string            `json:"path"`
	Hashes    map[string]string `json:"hashes"`
	Downloads []string          `json:"downloads"`
	FileSize  int64             `json:"fileSize"`
}

// ValidPath enforces the spec: relativ, ohne „..", ohne Laufwerk/Backslash.
func ValidPath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsAny(p, `\:`) {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// Build returns the .mrpack bytes for files and meta.
func Build(files []File, meta Meta) ([]byte, error) {
	if meta.VersionID == "" || meta.Name == "" || meta.Minecraft == "" || meta.FabricLoader == "" {
		return nil, fmt.Errorf("mrpack: versionId, name, minecraft und fabric-loader sind Pflicht")
	}
	idx := index{
		FormatVersion: 1, Game: "minecraft",
		VersionID: meta.VersionID, Name: meta.Name, Summary: meta.Summary,
		Files:        make([]indexFile, 0, len(files)),
		Dependencies: map[string]string{"minecraft": meta.Minecraft, "fabric-loader": meta.FabricLoader},
	}
	for _, f := range files {
		if !ValidPath(f.Path) {
			return nil, fmt.Errorf("mrpack: ungültiger Pfad %q", f.Path)
		}
		if f.SHA1 == "" || f.SHA512 == "" || f.URL == "" {
			return nil, fmt.Errorf("mrpack: %s: sha1, sha512 und URL sind Pflicht", f.Path)
		}
		idx.Files = append(idx.Files, indexFile{
			Path:      f.Path,
			Hashes:    map[string]string{"sha1": f.SHA1, "sha512": f.SHA512},
			Downloads: []string{f.URL},
			FileSize:  f.Size,
		})
	}
	sort.Slice(idx.Files, func(i, j int) bool { return idx.Files[i].Path < idx.Files[j].Path })
	raw, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("modrinth.index.json")
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(raw); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
```

- [ ] **Step 4: Tests laufen lassen**

Run: `go test ./internal/mrpack/ && go vet ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/mrpack
git commit -m "feat(mrpack): Modrinth-Modpack-Datei (.mrpack) bauen

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Versionsnummer, Loader-Erkennung, Changelog

**Files:**
- Create: `internal/modpack/version.go`
- Test: `internal/modpack/version_test.go`

**Interfaces:**
- Produces (für Task 5 + 6):
  - `type Entry struct { Project, File, Version string }` (JSON `project`, `file`, `version`)
  - `type State struct { Version, MC, Loader string; Files []Entry }` (JSON `version`, `mc`, `loader`, `files`)
  - `type Bump int` mit `Patch`, `Minor`, `Major`
  - `func Kind(prev, cur State) Bump`
  - `func Next(prev, cur State, start, highest string) string`
  - `func Highest(numbers []string) string`
  - `func ValidVersion(v string) bool`
  - `func DetectLoader(dataDir, mc string) string`
  - `func Changelog(prev, cur State, titles map[string]string, omitted []string) string`

- [ ] **Step 1: Failing tests schreiben** — `internal/modpack/version_test.go`:

```go
package modpack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func st(version, mc, loader string, files ...Entry) State {
	return State{Version: version, MC: mc, Loader: loader, Files: files}
}

var (
	sodium1 = Entry{Project: "P-sod", File: "sodium-1.jar", Version: "1"}
	sodium2 = Entry{Project: "P-sod", File: "sodium-2.jar", Version: "2"}
	jade    = Entry{Project: "P-jade", File: "jade.jar", Version: "1"}
)

func TestNext(t *testing.T) {
	prev := st("1.4.2", "26.3", "0.20.1", sodium1)
	cases := []struct {
		name          string
		prev, cur     State
		start, high   string
		want          string
	}{
		{"erste Version = Startwert", State{}, st("", "26.3", "0.20.1", sodium1), "", "", "1.0.0"},
		{"erste Version eigener Startwert", State{}, st("", "26.3", "0.20.1", sodium1), "3.0.0", "", "3.0.0"},
		{"erste Version, Modrinth hat schon 2.3.0", State{}, st("", "26.3", "0.20.1", sodium1), "", "2.3.0", "2.4.0"},
		{"nur Mod-Update = PATCH", prev, st("", "26.3", "0.20.1", sodium2), "", "", "1.4.3"},
		{"Mod dazu = MINOR", prev, st("", "26.3", "0.20.1", sodium1, jade), "", "", "1.5.0"},
		{"Mod weg = MINOR", st("1.4.2", "26.3", "0.20.1", sodium1, jade), st("", "26.3", "0.20.1", sodium1), "", "", "1.5.0"},
		{"Loader neu = MINOR", prev, st("", "26.3", "0.21.0", sodium1), "", "", "1.5.0"},
		{"MC neu = MAJOR", prev, st("", "26.4", "0.21.0", sodium2), "", "", "2.0.0"},
		{"Modrinth höher als eigener Stand", prev, st("", "26.3", "0.20.1", sodium2), "", "1.9.0", "1.9.1"},
		{"Modrinth niedriger zählt nicht", prev, st("", "26.3", "0.20.1", sodium2), "", "1.0.0", "1.4.3"},
	}
	for _, c := range cases {
		if got := Next(c.prev, c.cur, c.start, c.high); got != c.want {
			t.Errorf("%s: Next = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestHighestAndValid(t *testing.T) {
	if h := Highest([]string{"1.2.0", "1.10.0", "kaputt", "1.9.9", "0.1"}); h != "1.10.0" {
		t.Errorf("Highest = %q, want 1.10.0", h)
	}
	if h := Highest([]string{"x"}); h != "" {
		t.Errorf("Highest ohne gültige = %q", h)
	}
	for v, want := range map[string]bool{"1.0.0": true, "10.2.33": true, "1.0": false, "v1.0.0": false, "1.0.0-beta": false, "": false} {
		if ValidVersion(v) != want {
			t.Errorf("ValidVersion(%q) != %v", v, want)
		}
	}
}

func TestDetectLoader(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"fabric-server-mc.1.21.11-loader.0.18.0-launcher.1.1.0.jar", "fabric-server-mc.26.3-loader.0.20.1-launcher.1.1.2.jar", "eula.txt"} {
		os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644)
	}
	if l := DetectLoader(dir, "26.3"); l != "0.20.1" {
		t.Errorf("loader = %q, want 0.20.1", l)
	}
	if l := DetectLoader(dir, "26.4"); l != "" {
		t.Errorf("loader für fremde MC-Version = %q, want leer", l)
	}
	if l := DetectLoader(filepath.Join(dir, "fehlt"), "26.3"); l != "" {
		t.Errorf("fehlender Ordner = %q", l)
	}
}

func TestChangelog(t *testing.T) {
	titles := map[string]string{"P-sod": "Sodium", "P-jade": "Jade"}
	first := Changelog(State{}, st("1.0.0", "26.3", "0.20.1", sodium1), titles, nil)
	if !strings.Contains(first, "Erste Version") {
		t.Errorf("erste = %q", first)
	}
	cl := Changelog(st("1.4.2", "26.3", "0.20.1", sodium1), st("2.0.0", "26.4", "0.21.0", sodium2, jade), titles, []string{"fremd.jar"})
	for _, want := range []string{"**Minecraft:** 26.3 → 26.4", "**Fabric Loader:** 0.20.1 → 0.21.0", "**Neu:** Jade", "**Aktualisiert:** Sodium (1 → 2)", "fremd.jar"} {
		if !strings.Contains(cl, want) {
			t.Errorf("changelog fehlt %q:\n%s", want, cl)
		}
	}
	gone := Changelog(st("1.4.2", "26.3", "0.20.1", sodium1, jade), st("1.5.0", "26.3", "0.20.1", sodium1), titles, nil)
	if !strings.Contains(gone, "**Entfernt:** Jade") {
		t.Errorf("entfernt = %q", gone)
	}
}
```

- [ ] **Step 2: Test laufen lassen, Fehlschlag prüfen**

Run: `go test ./internal/modpack/`
Expected: FAIL (Paket/Funktionen fehlen)

- [ ] **Step 3: Implementierung** — `internal/modpack/version.go`:

```go
// Package modpack is the Modrinth channel of the client-pack publish: es
// löst die Dateien des Client-Pakets bei Modrinth auf, zählt die eigene
// Paket-Versionsnummer, baut die .mrpack und lädt sie als neue Version des
// Modpack-Projekts hoch.
package modpack

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Entry is one published file (Projekt-ID, Dateiname, Modrinth-Versionsnummer).
type Entry struct {
	Project string `json:"project"`
	File    string `json:"file"`
	Version string `json:"version"`
}

// State is the content of one published modpack version.
type State struct {
	Version string  `json:"version"`
	MC      string  `json:"mc"`
	Loader  string  `json:"loader"`
	Files   []Entry `json:"files"`
}

// Bump is the version part to increase.
type Bump int

const (
	Patch Bump = iota
	Minor
	Major
)

// Kind decides the bump (Nutzer-Regel): andere MC-Version → MAJOR; anderer
// Loader oder Projekte dazu/weg → MINOR; sonst PATCH.
func Kind(prev, cur State) Bump {
	if prev.MC != cur.MC {
		return Major
	}
	if prev.Loader != cur.Loader || !sameProjects(prev.Files, cur.Files) {
		return Minor
	}
	return Patch
}

func sameProjects(a, b []Entry) bool {
	set := map[string]bool{}
	for _, e := range a {
		set[e.Project] = true
	}
	other := map[string]bool{}
	for _, e := range b {
		other[e.Project] = true
		if !set[e.Project] {
			return false
		}
	}
	return len(set) == len(other)
}

type semver [3]int

func parse(v string) (semver, bool) {
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	var out semver
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || strconv.Itoa(n) != p {
			return semver{}, false
		}
		out[i] = n
	}
	return out, true
}

func (s semver) String() string { return fmt.Sprintf("%d.%d.%d", s[0], s[1], s[2]) }

func (s semver) cmp(o semver) int {
	for i := range s {
		if s[i] != o[i] {
			if s[i] < o[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func (s semver) bump(k Bump) semver {
	switch k {
	case Major:
		return semver{s[0] + 1, 0, 0}
	case Minor:
		return semver{s[0], s[1] + 1, 0}
	default:
		return semver{s[0], s[1], s[2] + 1}
	}
}

// ValidVersion reports whether v is MAJOR.MINOR.PATCH.
func ValidVersion(v string) bool {
	_, ok := parse(v)
	return ok
}

// Highest returns the greatest valid version number ("" = keine gültige).
func Highest(numbers []string) string {
	var best semver
	found := false
	for _, n := range numbers {
		if v, ok := parse(n); ok && (!found || v.cmp(best) > 0) {
			best, found = v, true
		}
	}
	if !found {
		return ""
	}
	return best.String()
}

// Next returns the version number for cur. start ist der Startwert aus den
// Einstellungen ("" = 1.0.0), highest die höchste schon auf Modrinth
// liegende Versionsnummer ("" = keine).
func Next(prev, cur State, start, highest string) string {
	s, ok := parse(start)
	if !ok {
		s = semver{1, 0, 0}
	}
	h, hasH := parse(highest)
	if prev.Version == "" {
		if hasH && h.cmp(s) >= 0 {
			return h.bump(Minor).String()
		}
		return s.String()
	}
	base, ok := parse(prev.Version)
	if !ok {
		base = s
	}
	if hasH && h.cmp(base) > 0 {
		base = h
	}
	return base.bump(Kind(prev, cur)).String()
}

var reServerJar = regexp.MustCompile(`^fabric-server-mc\.(.+)-loader\.(.+)-launcher\.(.+)\.jar$`)

// DetectLoader reads the Fabric loader version from the server launcher JAR
// in the MC data dir (z. B. fabric-server-mc.26.3-loader.0.20.1-launcher.1.1.2.jar)
// — genau der Loader, mit dem der Server läuft. "" = nicht gefunden.
func DetectLoader(dataDir, mc string) string {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if m := reServerJar.FindStringSubmatch(e.Name()); m != nil && m[1] == mc {
			return m[2]
		}
	}
	return ""
}

// Changelog describes cur against prev (Markdown für die Modrinth-Seite).
func Changelog(prev, cur State, titles map[string]string, omitted []string) string {
	name := func(e Entry) string {
		if t := titles[e.Project]; t != "" {
			return t
		}
		return e.File
	}
	var lines []string
	if prev.Version == "" {
		lines = append(lines, fmt.Sprintf("Erste Version: %d Dateien (MC %s · Fabric %s).", len(cur.Files), cur.MC, cur.Loader))
	} else {
		if prev.MC != cur.MC {
			lines = append(lines, fmt.Sprintf("**Minecraft:** %s → %s", prev.MC, cur.MC))
		}
		if prev.Loader != cur.Loader {
			lines = append(lines, fmt.Sprintf("**Fabric Loader:** %s → %s", prev.Loader, cur.Loader))
		}
		old := map[string]Entry{}
		for _, e := range prev.Files {
			old[e.Project] = e
		}
		now := map[string]bool{}
		var added, removed, updated []string
		for _, e := range cur.Files {
			now[e.Project] = true
			o, ok := old[e.Project]
			switch {
			case !ok:
				added = append(added, name(e))
			case o.Version != e.Version:
				updated = append(updated, fmt.Sprintf("%s (%s → %s)", name(e), o.Version, e.Version))
			}
		}
		for _, e := range prev.Files {
			if !now[e.Project] {
				removed = append(removed, name(e))
			}
		}
		for _, part := range []struct {
			label string
			items []string
		}{{"Neu", added}, {"Entfernt", removed}, {"Aktualisiert", updated}} {
			if len(part.items) > 0 {
				sort.Strings(part.items)
				lines = append(lines, "**"+part.label+":** "+strings.Join(part.items, ", "))
			}
		}
		if len(lines) == 0 {
			lines = append(lines, "Keine inhaltlichen Änderungen (Paket neu veröffentlicht).")
		}
	}
	if len(omitted) > 0 {
		o := append([]string(nil), omitted...)
		sort.Strings(o)
		lines = append(lines, "**Nicht im Modpack enthalten** (nicht auf Modrinth): "+strings.Join(o, ", "))
	}
	return strings.Join(lines, "\n")
}
```

- [ ] **Step 4: Tests laufen lassen**

Run: `go test ./internal/modpack/ && go vet ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/modpack
git commit -m "feat(modpack): Versionsnummer, Loader-Erkennung, Changelog

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Kanalfähiger Paket-Versand (`packpub`) + Dropbox als erster Kanal

Verhalten danach wie heute (nur Dropbox), aber über Kanäle: gemeinsame 📦-Meldung, Fingerabdruck je Kanal, Single-flight in `packpub`.

**Files:**
- Create: `internal/packpub/packpub.go`
- Test: `internal/packpub/packpub_test.go`
- Modify: `internal/dropbox/publish.go`, `internal/dropbox/dropbox_test.go`
- Modify: `internal/api/api.go`, `internal/api/mods.go`
- Modify: `cmd/msm/main.go`

**Interfaces:**
- Produces:
  - `packpub.ErrBusy`, `packpub.ErrNotReady`
  - `type Outcome struct { Lines []string; Fields []events.Field }`
  - `type Channel interface { Name() string; Label() string; Ready() bool; Publish(ctx context.Context) (Outcome, error) }`
  - `func New(fingerprint func() string, get func(string) string, set func(string, string), bus *events.Bus, log *slog.Logger, channels ...Channel) *Publisher`
  - `func (p *Publisher) Busy() bool`, `func (p *Publisher) Ready(name string) bool` (`""` = irgendein Kanal), `func (p *Publisher) Run(ctx context.Context, names []string, force bool) error`
  - `dropbox.Pack(ctx context.Context, c *Client, dirs map[string]string) (link string, files int, err error)`
  - `dropbox.Channel{C *Client; Dirs map[string]string}` (implementiert `packpub.Channel`, Name `"dropbox"`)
  - `api.Deps.Packs *packpub.Publisher`, `api.Deps.PublishPack func(ctx context.Context, channels []string, force bool) error`

- [ ] **Step 1: Failing tests schreiben** — `internal/packpub/packpub_test.go`:

```go
package packpub

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
)

type fakeChannel struct {
	name  string
	ready bool
	err   error
	calls int
	lines []string
	block chan struct{}
}

func (f *fakeChannel) Name() string  { return f.name }
func (f *fakeChannel) Label() string { return strings.ToUpper(f.name) }
func (f *fakeChannel) Ready() bool   { return f.ready }
func (f *fakeChannel) Publish(ctx context.Context) (Outcome, error) {
	f.calls++
	if f.block != nil {
		<-f.block
	}
	if f.err != nil {
		return Outcome{}, f.err
	}
	return Outcome{Lines: f.lines, Fields: []events.Field{{Name: f.name, Value: "ok"}}}, nil
}

type kv struct {
	mu sync.Mutex
	m  map[string]string
}

func (k *kv) get(key string) string { k.mu.Lock(); defer k.mu.Unlock(); return k.m[key] }
func (k *kv) set(key, v string)     { k.mu.Lock(); defer k.mu.Unlock(); k.m[key] = v }

func setup(fp *string, chans ...Channel) (*Publisher, <-chan events.Event, *kv) {
	store := &kv{m: map[string]string{}}
	bus := events.New()
	ch, _ := bus.Subscribe(16)
	p := New(func() string { return *fp }, store.get, store.set, bus, slog.New(slog.NewTextHandler(io.Discard, nil)), chans...)
	return p, ch, store
}

func drain(ch <-chan events.Event) []events.Event {
	var out []events.Event
	for {
		select {
		case ev := <-ch:
			out = append(out, ev)
		case <-time.After(50 * time.Millisecond):
			return out
		}
	}
}

func TestRunPostsOneCombinedMessage(t *testing.T) {
	fp := "A"
	dbx := &fakeChannel{name: "dropbox", ready: true, lines: []string{"Download: x"}}
	mr := &fakeChannel{name: "modrinth", ready: true, lines: []string{"Modrinth App: Update"}}
	p, ch, _ := setup(&fp, dbx, mr)
	if err := p.Run(context.Background(), nil, false); err != nil {
		t.Fatal(err)
	}
	evs := drain(ch)
	if len(evs) != 1 || evs[0].Type != events.TypeClientPack || !strings.Contains(evs[0].Message, "Download: x") || !strings.Contains(evs[0].Message, "Modrinth App") {
		t.Fatalf("events = %+v, want eine gemeinsame Meldung", evs)
	}
}

func TestFailedChannelIsRetriedOthersSkipped(t *testing.T) {
	fp := "A"
	dbx := &fakeChannel{name: "dropbox", ready: true, lines: []string{"Download"}}
	mr := &fakeChannel{name: "modrinth", ready: true, lines: []string{"Modrinth"}, err: errors.New("offline")}
	p, ch, _ := setup(&fp, dbx, mr)
	err := p.Run(context.Background(), nil, false)
	if err == nil || !strings.Contains(err.Error(), "MODRINTH") {
		t.Fatalf("err = %v, want Fehler des Modrinth-Kanals", err)
	}
	evs := drain(ch)
	if len(evs) != 2 { // Admin-Fehler + Erfolgsmeldung (nur Dropbox)
		t.Fatalf("events = %+v", evs)
	}
	// nächster Lauf, gleicher Inhalt: Dropbox schon aktuell, nur Modrinth erneut
	mr.err = nil
	if err := p.Run(context.Background(), nil, false); err != nil {
		t.Fatal(err)
	}
	if dbx.calls != 1 || mr.calls != 2 {
		t.Errorf("calls dropbox=%d modrinth=%d, want 1/2", dbx.calls, mr.calls)
	}
	// force (Knopf) verschickt trotz gleichem Inhalt
	if err := p.Run(context.Background(), []string{"dropbox"}, true); err != nil {
		t.Fatal(err)
	}
	if dbx.calls != 2 || mr.calls != 2 {
		t.Errorf("force: calls dropbox=%d modrinth=%d, want 2/2", dbx.calls, mr.calls)
	}
}

func TestNotReadyAndBusy(t *testing.T) {
	fp := "A"
	off := &fakeChannel{name: "dropbox", ready: false}
	p, _, _ := setup(&fp, off)
	if err := p.Run(context.Background(), nil, false); !errors.Is(err, ErrNotReady) {
		t.Fatalf("err = %v, want ErrNotReady", err)
	}
	if p.Ready("") || p.Ready("dropbox") {
		t.Error("Ready ohne eingerichteten Kanal")
	}

	slow := &fakeChannel{name: "modrinth", ready: true, block: make(chan struct{})}
	p2, _, _ := setup(&fp, slow)
	done := make(chan error)
	go func() { done <- p2.Run(context.Background(), nil, true) }()
	for !p2.Busy() {
		time.Sleep(time.Millisecond)
	}
	if err := p2.Run(context.Background(), nil, true); !errors.Is(err, ErrBusy) {
		t.Errorf("zweiter Lauf: err = %v, want ErrBusy", err)
	}
	close(slow.block)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestChannelWithoutLinesStaysQuiet(t *testing.T) {
	fp := "A"
	draft := &fakeChannel{name: "modrinth", ready: true} // Projekt nicht öffentlich: keine Zeilen
	p, ch, _ := setup(&fp, draft)
	if err := p.Run(context.Background(), nil, false); err != nil {
		t.Fatal(err)
	}
	if evs := drain(ch); len(evs) != 0 {
		t.Errorf("events = %+v, want keine Spieler-Meldung", evs)
	}
}
```

- [ ] **Step 2: Test laufen lassen, Fehlschlag prüfen**

Run: `go test ./internal/packpub/`
Expected: FAIL (Paket fehlt)

- [ ] **Step 3: `packpub` implementieren** — `internal/packpub/packpub.go`:

```go
// Package packpub publishes the client pack over all configured channels
// (Dropbox-ZIP, Modrinth-Modpack). Je Kanal merkt es sich den Fingerabdruck
// des zuletzt erfolgreich verschickten Inhalts: ein Kanal mit aktuellem
// Stand wird übersprungen, ein gescheiterter beim nächsten Lauf nachgeholt.
// Erfolgreiche Kanäle ergeben EINE gemeinsame 📦-Meldung, Fehler je Kanal
// eine Admin-Meldung.
package packpub

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
)

var (
	// ErrBusy: ein Paket-Versand läuft bereits.
	ErrBusy = errors.New("client-Paket wird gerade schon verschickt")
	// ErrNotReady: kein ausgewählter Kanal ist eingerichtet.
	ErrNotReady = errors.New("kein Versandweg eingerichtet — Dropbox oder Modrinth im Einstellungen-Tab hinterlegen")
)

// Outcome is what a successful channel contributes to the player message.
// Keine Zeilen = Kanal wird nicht erwähnt (z. B. Modrinth-Projekt noch nicht
// öffentlich).
type Outcome struct {
	Lines  []string
	Fields []events.Field
}

// Channel is one way to distribute the client pack.
type Channel interface {
	Name() string  // "dropbox" | "modrinth"
	Label() string // für Meldungen
	Ready() bool
	Publish(ctx context.Context) (Outcome, error)
}

type Publisher struct {
	channels    []Channel
	fingerprint func() string
	get         func(string) string
	set         func(string, string)
	bus         *events.Bus
	log         *slog.Logger
	busy        atomic.Bool
}

func New(fingerprint func() string, get func(string) string, set func(string, string), bus *events.Bus, log *slog.Logger, channels ...Channel) *Publisher {
	return &Publisher{channels: channels, fingerprint: fingerprint, get: get, set: set, bus: bus, log: log}
}

// Busy reports whether a publish is running.
func (p *Publisher) Busy() bool { return p.busy.Load() }

// Ready reports whether the named channel ("" = irgendeiner) is configured.
func (p *Publisher) Ready(name string) bool {
	for _, ch := range p.channels {
		if (name == "" || ch.Name() == name) && ch.Ready() {
			return true
		}
	}
	return false
}

func fpKey(name string) string { return "clientpack.published." + name }

func selected(names []string, name string) bool {
	if len(names) == 0 {
		return true
	}
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

// Run publishes over the given channels (nil = alle eingerichteten).
// force=false überspringt Kanäle, deren letzter erfolgreicher Versand
// denselben Inhalt hatte. Fehler: je gescheitertem Kanal, gebündelt.
func (p *Publisher) Run(ctx context.Context, names []string, force bool) error {
	if !p.busy.CompareAndSwap(false, true) {
		return ErrBusy
	}
	defer p.busy.Store(false)
	fp := ""
	if p.fingerprint != nil {
		fp = p.fingerprint()
	}
	var lines []string
	var fields []events.Field
	var errs []error
	ran := 0
	for _, ch := range p.channels {
		if !selected(names, ch.Name()) || !ch.Ready() {
			continue
		}
		ran++
		if !force && fp != "" && p.get(fpKey(ch.Name())) == fp {
			continue // dieser Kanal hat den Inhalt schon
		}
		out, err := ch.Publish(ctx)
		if err != nil {
			p.log.Error("client-pack publish failed", "channel", ch.Name(), "err", err)
			p.bus.Publish(events.Event{
				Type: events.TypeClientPack, Severity: events.SevError,
				Title:   "⚠️ Mod-Paket-Upload fehlgeschlagen (" + ch.Label() + ")",
				Message: "Info für den Admin — Details unten.",
				Fields:  []events.Field{{Name: "Details", Value: err.Error()}},
			})
			errs = append(errs, fmt.Errorf("%s: %w", ch.Label(), err))
			continue
		}
		if fp != "" {
			p.set(fpKey(ch.Name()), fp)
		}
		lines = append(lines, out.Lines...)
		fields = append(fields, out.Fields...)
	}
	if ran == 0 {
		return ErrNotReady
	}
	if len(lines) > 0 {
		p.bus.Publish(events.Event{
			Type: events.TypeClientPack, Severity: events.SevSuccess,
			Title:   "📦 Neues Mod-Paket!",
			Message: strings.Join(lines, "\n\n"),
			Fields:  fields,
		})
	}
	return errors.Join(errs...)
}
```

- [ ] **Step 4: Dropbox umbauen** — in `internal/dropbox/publish.go` den Block ab `// Publish zips …` bis Dateiende (inkl. `ErrBusy`, `publishing`, `Publishing`, `Publish`) ersetzen durch:

```go
// Pack zips the client profile dirs, uploads them and returns the shared
// link — ohne Discord-Meldung (die baut packpub gemeinsam für alle Kanäle).
func Pack(ctx context.Context, c *Client, dirs map[string]string) (string, int, error) {
	name := "/MSM/mc-clientpack-" + time.Now().Format("2006-01-02") + ".zip"
	pr, pw := io.Pipe()
	files := 0
	done := make(chan struct{})
	go func() {
		defer close(done)
		n, err := ZipDirs(pw, dirs)
		files = n
		pw.CloseWithError(err)
	}()
	if err := c.Upload(ctx, name, pr); err != nil {
		// Leseseite schließen, sonst blockiert die ZIP-Goroutine für immer
		// im Schreiben (Goroutine + offene Dateien leaken je Fehlversuch)
		pr.CloseWithError(err)
		<-done
		return "", 0, err
	}
	<-done
	link, err := c.ShareLink(ctx, name)
	if err != nil {
		return "", 0, err
	}
	return link, files, nil
}

// Channel is the Dropbox distribution channel for packpub.
type Channel struct {
	C    *Client
	Dirs map[string]string
}

func (ch Channel) Name() string  { return "dropbox" }
func (ch Channel) Label() string { return "Dropbox" }
func (ch Channel) Ready() bool   { return ch.C != nil && ch.Dirs != nil && ch.C.Ready() }

func (ch Channel) Publish(ctx context.Context) (packpub.Outcome, error) {
	link, files, err := Pack(ctx, ch.C, ch.Dirs)
	if err != nil {
		return packpub.Outcome{}, err
	}
	return packpub.Outcome{
		Lines: []string{"Download (ZIP): " + link + "\nIn den bestehenden .minecraft-Ordner entpacken — Karten, Wegpunkte und Configs bleiben erhalten."},
		Fields: []events.Field{
			{Name: "Dateien", Value: fmt.Sprint(files)},
			{Name: "Stand", Value: time.Now().Format("02.01.2006")},
		},
	}, nil
}
```

Imports von `publish.go` danach: `context`, `fmt`, `io`, `time`, `events`, `packpub` (`log/slog`, `errors`, `sync/atomic` entfallen).

In `internal/dropbox/dropbox_test.go` die Funktion `TestPublishRefusesConcurrentRun` löschen (Single-flight testet jetzt `packpub`); nicht mehr genutzte Imports (`errors`) entfernen.

- [ ] **Step 5: API umstellen**

`internal/api/api.go`:
- Import `dropbox` entfernen, `packpub` hinzufügen.
- Feld `dropbox *dropbox.Client` durch `packs *packpub.Publisher` ersetzen; Feld `publishPack` bekommt den Typ `func(ctx context.Context, channels []string, force bool) error`.
- In `Deps`: `Dropbox *dropbox.Client` durch `Packs *packpub.Publisher` ersetzen; `PublishPack func(ctx context.Context, channels []string, force bool) error // Paket-Versand inkl. Zustands-Pflege`.
- In `New`: `dropbox: d.Dropbox,` → `packs: d.Packs,`.
- Routen: `if s.dropbox != nil {` → `if s.packs != nil && s.publishPack != nil {`.

`internal/api/mods.go`: Import `dropbox` entfernen, `packpub` hinzufügen; `handlePublishClientPack` vollständig ersetzen:

```go
// handlePublishClientPack verschickt das Client-Paket sofort (Knopf im
// Mods-Tab). ?channel=dropbox|modrinth wählt einen Kanal, ohne Parameter
// alle eingerichteten. Der Knopf erzwingt den Versand auch bei
// unverändertem Inhalt.
func (s *Server) handlePublishClientPack(w http.ResponseWriter, r *http.Request) {
	channel := r.URL.Query().Get("channel")
	switch channel {
	case "", "dropbox", "modrinth":
	default:
		httpError(w, http.StatusBadRequest, "unbekannter Versandweg "+channel)
		return
	}
	if !s.packs.Ready(channel) {
		msg := packpub.ErrNotReady.Error()
		switch channel {
		case "dropbox":
			msg = "Dropbox nicht konfiguriert — App-Key/Secret/Refresh-Token im Einstellungen-Tab hinterlegen"
		case "modrinth":
			msg = "Modrinth-Modpack nicht eingerichtet — Projekt und Token im Einstellungen-Tab hinterlegen"
		}
		httpError(w, http.StatusConflict, msg)
		return
	}
	if s.packs.Busy() {
		httpError(w, http.StatusConflict, "das Client-Paket wird gerade schon verschickt — die Meldung kommt gleich in Discord")
		return
	}
	var names []string
	label := "alle"
	if channel != "" {
		names = []string{channel}
		label = channel
	}
	s.audit(r.Context(), "mods.publish", "client-pack versand gestartet ("+label+")")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer cancel()
		if err := s.publishPack(ctx, names, true); err != nil {
			s.audit(ctx, "mods.publish.failed", err.Error())
		} else {
			s.audit(ctx, "mods.publish.ok", "client-pack veröffentlicht ("+label+")")
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{
		"message": "Versand gestartet — das Ergebnis kommt als Discord-Meldung (und ins Audit-Log).",
	})
}
```

Test dazu an `internal/api/modsadd_test.go` anhängen (nutzt dessen Helper nicht; eigener Aufbau):

```go
type stubChannel struct{ ready bool }

func (c stubChannel) Name() string  { return "dropbox" }
func (c stubChannel) Label() string { return "Dropbox" }
func (c stubChannel) Ready() bool   { return c.ready }
func (c stubChannel) Publish(context.Context) (packpub.Outcome, error) {
	return packpub.Outcome{}, nil
}

func TestPublishChannelValidation(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := mods.NewManager(nil, "fabric", nil)
	packs := packpub.New(nil, func(string) string { return "" }, func(string, string) {}, events.New(), log, stubChannel{ready: true})
	called := make(chan []string, 1)
	srv := New(Deps{ModManager: mgr, Packs: packs, Log: log,
		PublishPack: func(_ context.Context, ch []string, force bool) error { called <- ch; return nil }})

	post := func(q string) int {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/mods/publish"+q, nil))
		return rec.Code
	}
	if c := post("?channel=ftp"); c != http.StatusBadRequest {
		t.Errorf("unbekannter Kanal: %d", c)
	}
	if c := post("?channel=modrinth"); c != http.StatusConflict {
		t.Errorf("nicht eingerichteter Kanal: %d", c)
	}
	if c := post("?channel=dropbox"); c != http.StatusAccepted {
		t.Fatalf("dropbox: %d", c)
	}
	if ch := <-called; len(ch) != 1 || ch[0] != "dropbox" {
		t.Errorf("channels = %v", ch)
	}
}
```

(Imports in `modsadd_test.go` ggf. ergänzen: `context`, `io`, `log/slog`, `net/http`, `net/http/httptest`, `events`, `mods`, `packpub`.)

- [ ] **Step 6: `main.go` umstellen** — den Block ab `// publishPack ist der einzige Weg …` bis einschließlich `news.SetPublisher(dbx.Ready, publishPack)` ersetzen durch:

```go
	// Versandwege für das Client-Paket (packpub): heute Dropbox; jeder Kanal
	// merkt sich seinen zuletzt verschickten Stand selbst
	packs := packpub.New(func() string { return packFingerprint(clientDirs) }, kvGet, kvSet, bus, log,
		dropbox.Channel{C: dbx, Dirs: clientDirs})
	// publishPack ist der einzige Weg zum Paket-Versand (Button, MC-Update,
	// Mod-Neuigkeiten). „Paket veraltet" wird nur gelöscht, wenn ALLE
	// Kanäle erfolgreich waren — ein gescheiterter wird beim nächsten
	// Neustart nachgeholt, aktuelle überspringt packpub.
	publishPack := func(ctx context.Context, channels []string, force bool) error {
		if clientDirs == nil {
			return fmt.Errorf("kein Client-Profil konfiguriert")
		}
		gen := news.BeginPublish()
		if err := packs.Run(ctx, channels, force); err != nil {
			return err
		}
		news.Published(gen)
		return nil
	}
	if upgrader != nil && clientDirs != nil {
		// nach dem Ein-Klick-MC-Update das frische Client-Paket
		// automatisch verteilen — nur wenn ein Versandweg eingerichtet ist
		upgrader.Publish = func(ctx context.Context) (bool, error) {
			if !packs.Ready("") {
				return false, nil // Feature aus — kein Fehler
			}
			return true, publishPack(ctx, nil, false)
		}
	}
	news.SetPublisher(func() bool { return packs.Ready("") }, func(ctx context.Context) error {
		return publishPack(ctx, nil, false)
	})
```

Import `packpub` ergänzen. In `api.New(api.Deps{…})`: `Dropbox: dbx,` → `Packs: packs,`; `PublishPack: publishPack,` bleibt.

- [ ] **Step 7: Tests + Build**

Run: `go vet ./... && go test ./...`
Expected: PASS (alle Pakete; `modnews`-Tests unverändert grün)

- [ ] **Step 8: Commit**

```bash
git add internal/packpub internal/dropbox internal/api cmd/msm/main.go
git commit -m "feat(packpub): Paket-Versand über Kanäle, Dropbox als erster Kanal

Gemeinsame 📦-Meldung, Stand je Kanal (gescheiterte werden nachgeholt,
aktuelle übersprungen), Single-flight für alle Kanäle zusammen.
Knopf-Versand wählbar per ?channel=.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Modrinth-Kanal (`modpack.Channel`)

**Files:**
- Create: `internal/modpack/channel.go`
- Test: `internal/modpack/channel_test.go`

**Interfaces:**
- Consumes: Task 1 (`modrinth.*`), Task 2 (`mrpack.Build`, `mrpack.File`, `mrpack.Meta`), Task 3 (`State`, `Entry`, `Next`, `Highest`, `DetectLoader`, `Changelog`, `Kind`, `Major`), Task 4 (`packpub.Outcome`).
- Produces (für Task 6 + 7):
  - `type API interface { VersionsByHash; Projects; AuthProject; AuthProjectVersions; CreateVersion; CurrentUser }` (Signaturen wie `*modrinth.Client`)
  - `type Config struct { Project, Token, StartVersion string }` mit `Complete() bool`
  - `type Status struct { Ready bool; Version, MC, Loader string; Omitted []string; LastError, ProjectURL string }` (JSON `ready`, `version`, `mc`, `loader`, `omitted`, `lastError`, `projectUrl`)
  - `func New(api API, cfg func() Config, dirs map[string]string, dataDir string, mcVersion func() string, get func(string) string, set func(string, string), log *slog.Logger) *Channel`
  - `(*Channel) Name() string` = `"modrinth"`, `Label()` = `"Modrinth"`, `Ready() bool`, `Publish(ctx) (packpub.Outcome, error)`, `Check(ctx) (string, error)`, `Status() Status`

- [ ] **Step 1: Failing tests schreiben** — `internal/modpack/channel_test.go`:

```go
package modpack

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
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
	known    map[string]modrinth.Version // sha512 → Version
	project  modrinth.Project
	versions []modrinth.Version
	created  []modrinth.NewVersion
	packs    [][]byte
	failNext int // so viele CreateVersion-Aufrufe mit 400 beantworten
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
```

- [ ] **Step 2: Test laufen lassen, Fehlschlag prüfen**

Run: `go test ./internal/modpack/`
Expected: FAIL (`undefined: New`, `Channel`, …)

- [ ] **Step 3: Implementierung** — `internal/modpack/channel.go`:

```go
package modpack

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/modrinth"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mrpack"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/packpub"
)

// API is the Modrinth surface the channel needs (*modrinth.Client).
type API interface {
	VersionsByHash(ctx context.Context, sha512s []string) (map[string]modrinth.Version, error)
	Projects(ctx context.Context, ids []string) ([]modrinth.Project, error)
	AuthProject(ctx context.Context, token, project string) (modrinth.Project, error)
	AuthProjectVersions(ctx context.Context, token, project string) ([]modrinth.Version, error)
	CreateVersion(ctx context.Context, token string, v modrinth.NewVersion, fileName string, data []byte) (modrinth.Version, error)
	CurrentUser(ctx context.Context, token string) (modrinth.User, error)
}

// Config comes from the settings store (Dashboard vor .env).
type Config struct {
	Project      string
	Token        string
	StartVersion string
}

func (c Config) Complete() bool { return c.Project != "" && c.Token != "" }

// Status is the dashboard view (GET /api/modpack).
type Status struct {
	Ready      bool     `json:"ready"`
	Version    string   `json:"version,omitempty"`
	MC         string   `json:"mc,omitempty"`
	Loader     string   `json:"loader,omitempty"`
	Omitted    []string `json:"omitted,omitempty"`
	LastError  string   `json:"lastError,omitempty"`
	ProjectURL string   `json:"projectUrl,omitempty"`
}

const stateKey = "modpack.state"

type Channel struct {
	api       API
	cfg       func() Config
	dirs      map[string]string
	dataDir   string
	mcVersion func() string
	get       func(string) string
	set       func(string, string)
	log       *slog.Logger

	mu         sync.Mutex
	omitted    []string
	lastErr    string
	projectURL string
}

func New(api API, cfg func() Config, dirs map[string]string, dataDir string, mcVersion func() string,
	get func(string) string, set func(string, string), log *slog.Logger) *Channel {
	return &Channel{api: api, cfg: cfg, dirs: dirs, dataDir: dataDir, mcVersion: mcVersion, get: get, set: set, log: log}
}

func (c *Channel) Name() string  { return "modrinth" }
func (c *Channel) Label() string { return "Modrinth" }
func (c *Channel) Ready() bool   { return c.api != nil && c.dirs != nil && c.cfg().Complete() }

func (c *Channel) loadState() State {
	var s State
	if raw := c.get(stateKey); raw != "" {
		json.Unmarshal([]byte(raw), &s)
	}
	return s
}

// Status returns the last published state for the dashboard.
func (c *Channel) Status() Status {
	s := c.loadState()
	c.mu.Lock()
	defer c.mu.Unlock()
	return Status{Ready: c.Ready(), Version: s.Version, MC: s.MC, Loader: s.Loader,
		Omitted: append([]string(nil), c.omitted...), LastError: c.lastErr, ProjectURL: c.projectURL}
}

func public(status string) bool {
	return status == "approved" || status == "unlisted" || status == "archived"
}

func projectURL(p modrinth.Project) string {
	id := p.Slug
	if id == "" {
		id = p.ID
	}
	return "https://modrinth.com/modpack/" + id
}

// explain turns Modrinth API errors into admin-friendly text.
func explain(err error, project string) error {
	var apiErr *modrinth.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Status {
		case http.StatusUnauthorized:
			return fmt.Errorf("Modrinth lehnt den Token ab (401) — Token gültig und mit den Rechten „Create versions“ und „Read projects“? (%w)", err)
		case http.StatusNotFound:
			return fmt.Errorf("Modrinth-Projekt „%s“ nicht gefunden — Slug/ID prüfen (%w)", project, err)
		}
	}
	return err
}

// Check verifies token and project for „Verbindung prüfen".
func (c *Channel) Check(ctx context.Context) (string, error) {
	cfg := c.cfg()
	if !cfg.Complete() {
		return "", fmt.Errorf("Projekt und Token eintragen und speichern")
	}
	u, err := c.api.CurrentUser(ctx, cfg.Token)
	if err != nil {
		return "", explain(err, cfg.Project)
	}
	p, err := c.api.AuthProject(ctx, cfg.Token, cfg.Project)
	if err != nil {
		return "", explain(err, cfg.Project)
	}
	if p.ProjectType != "modpack" {
		return "", fmt.Errorf("„%s“ ist kein Modpack-Projekt (Typ: %s) — auf Modrinth ein Projekt vom Typ Modpack anlegen", p.Title, p.ProjectType)
	}
	state := "öffentlich"
	if !public(p.Status) {
		state = "noch nicht freigegeben (Status: " + p.Status + ") — Versionen werden schon hochgeladen, die Spieler-Meldung nennt Modrinth erst nach der Freigabe"
	}
	return fmt.Sprintf("✔ Verbunden als %s · Projekt „%s“: %s", u.Username, p.Title, state), nil
}

type localFile struct {
	category, name, sha512 string
}

func scan(dirs map[string]string) ([]localFile, error) {
	var out []localFile
	cats := make([]string, 0, len(dirs))
	for cat := range dirs {
		cats = append(cats, cat)
	}
	sort.Strings(cats)
	for _, cat := range cats {
		items, err := os.ReadDir(dirs[cat])
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("client-paket lesen (%s): %w", cat, err)
		}
		for _, it := range items {
			if it.IsDir() || strings.HasPrefix(it.Name(), ".") {
				continue
			}
			f, err := os.Open(filepath.Join(dirs[cat], it.Name()))
			if err != nil {
				return nil, err
			}
			h := sha512.New()
			_, err = io.Copy(h, f)
			f.Close()
			if err != nil {
				return nil, err
			}
			out = append(out, localFile{category: cat, name: it.Name(), sha512: hex.EncodeToString(h.Sum(nil))})
		}
	}
	return out, nil
}

func (c *Channel) fail(err error) (packpub.Outcome, error) {
	c.mu.Lock()
	c.lastErr = err.Error()
	c.mu.Unlock()
	return packpub.Outcome{}, err
}

// Publish uploads the current client pack as a new modpack version.
func (c *Channel) Publish(ctx context.Context) (packpub.Outcome, error) {
	cfg := c.cfg()
	mc := c.mcVersion()
	if mc == "" {
		return c.fail(fmt.Errorf("Minecraft-Version unbekannt (Server offline?) — Modpack wird beim nächsten Versand nachgeholt"))
	}
	prev := c.loadState()

	locals, err := scan(c.dirs)
	if err != nil {
		return c.fail(err)
	}
	hashes := make([]string, 0, len(locals))
	for _, l := range locals {
		hashes = append(hashes, l.sha512)
	}
	known, err := c.api.VersionsByHash(ctx, hashes)
	if err != nil {
		return c.fail(fmt.Errorf("dateien bei Modrinth auflösen: %w", err))
	}
	var files []mrpack.File
	var entries []Entry
	var omitted []string
	for _, l := range locals {
		v, ok := known[l.sha512]
		var file *modrinth.File
		if ok {
			for i := range v.Files {
				f := &v.Files[i]
				if f.Hashes.SHA512 == l.sha512 && strings.HasPrefix(f.URL, "https://cdn.modrinth.com/") && f.Hashes.SHA1 != "" {
					file = f
					break
				}
			}
		}
		if file == nil {
			omitted = append(omitted, l.name)
			continue
		}
		files = append(files, mrpack.File{Path: l.category + "/" + l.name, SHA1: file.Hashes.SHA1, SHA512: l.sha512, URL: file.URL, Size: file.Size})
		entries = append(entries, Entry{Project: v.ProjectID, File: l.name, Version: v.VersionNumber})
	}
	if len(files) == 0 {
		return c.fail(fmt.Errorf("keine Datei des Client-Pakets ist auf Modrinth bekannt — kein Modpack-Upload"))
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Project < entries[j].Project })

	loader := DetectLoader(c.dataDir, mc)
	if loader == "" && prev.MC == mc {
		loader = prev.Loader
	}
	if loader == "" {
		return c.fail(fmt.Errorf("Fabric-Loader-Version nicht gefunden (Server-JAR fabric-server-mc.%s-loader.*.jar fehlt im MC-Ordner)", mc))
	}

	proj, err := c.api.AuthProject(ctx, cfg.Token, cfg.Project)
	if err != nil {
		return c.fail(explain(err, cfg.Project))
	}
	if proj.ProjectType != "modpack" {
		return c.fail(fmt.Errorf("„%s“ ist kein Modpack-Projekt (Typ: %s)", proj.Title, proj.ProjectType))
	}
	highest := func() string {
		vs, err := c.api.AuthProjectVersions(ctx, cfg.Token, cfg.Project)
		if err != nil {
			return ""
		}
		nums := make([]string, 0, len(vs))
		for _, v := range vs {
			nums = append(nums, v.VersionNumber)
		}
		return Highest(nums)
	}

	cur := State{MC: mc, Loader: loader, Files: entries}
	cur.Version = Next(prev, cur, cfg.StartVersion, highest())

	ids := map[string]bool{}
	for _, e := range append(append([]Entry(nil), prev.Files...), entries...) {
		ids[e.Project] = true
	}
	idList := make([]string, 0, len(ids))
	for id := range ids {
		idList = append(idList, id)
	}
	titles := map[string]string{}
	if ps, err := c.api.Projects(ctx, idList); err == nil {
		for _, p := range ps {
			titles[p.ID] = p.Title
		}
	}
	changelog := Changelog(prev, cur, titles, omitted)

	upload := func(version string) error {
		data, err := mrpack.Build(files, mrpack.Meta{VersionID: version, Name: proj.Title, Minecraft: mc, FabricLoader: loader})
		if err != nil {
			return err
		}
		_, err = c.api.CreateVersion(ctx, cfg.Token, modrinth.NewVersion{
			Name:          fmt.Sprintf("%s (MC %s · Fabric %s)", version, mc, loader),
			VersionNumber: version,
			Changelog:     changelog,
			GameVersions:  []string{mc},
			VersionType:   "release",
			Loaders:       []string{"fabric"},
			Featured:      true,
			ProjectID:     proj.ID,
		}, fmt.Sprintf("%s-%s.mrpack", proj.Slug, version), data)
		return err
	}
	err = upload(cur.Version)
	var apiErr *modrinth.APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusBadRequest {
		// Versionsnummer inzwischen vergeben (z. B. von Hand hochgeladen):
		// höchste neu lesen und einmal weiterzählen
		if h := Highest([]string{highest(), cur.Version}); h != "" {
			cur.Version = Next(State{Version: h, MC: mc, Loader: loader, Files: entries}, cur, cfg.StartVersion, "")
			err = upload(cur.Version)
		}
	}
	if err != nil {
		return c.fail(explain(err, cfg.Project))
	}

	raw, _ := json.Marshal(cur)
	c.set(stateKey, string(raw))
	c.mu.Lock()
	c.omitted, c.lastErr, c.projectURL = omitted, "", projectURL(proj)
	c.mu.Unlock()
	c.log.Info("modpack veröffentlicht", "version", cur.Version, "mc", mc, "loader", loader, "dateien", len(files), "weggelassen", len(omitted))

	if !public(proj.Status) {
		return packpub.Outcome{}, nil // Projekt noch nicht freigegeben: Spieler nicht hinschicken
	}
	line := fmt.Sprintf("Modrinth App: beim Modpack „%s“ auf *Update* klicken (neu: Version %s).\n%s", proj.Title, cur.Version, projectURL(proj))
	if prev.Version != "" && Kind(prev, cur) == Major {
		line += "\nNeue Minecraft-Version: falls kein Update angeboten wird, in der Modrinth App beim Modpack „Version ändern“ → neueste wählen."
	}
	return packpub.Outcome{
		Lines:  []string{line},
		Fields: []events.Field{{Name: "Modpack-Version", Value: cur.Version}},
	}, nil
}
```

- [ ] **Step 4: Tests laufen lassen**

Run: `go test ./internal/modpack/ && go vet ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/modpack
git commit -m "feat(modpack): Modrinth-Kanal — Client-Paket als Modpack-Version hochladen

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Einstellungen + API für Modrinth

**Files:**
- Modify: `internal/settings/settings.go`, Test `internal/settings/settings_test.go`
- Modify: `internal/api/api.go`, `internal/api/settings.go`, `internal/api/mods.go`
- Test: `internal/api/settings_test.go`

**Interfaces:**
- Consumes: Task 5 (`modpack.Config`, `modpack.Channel` mit `Check`, `Status`; `modpack.ValidVersion`).
- Produces (für Task 7 + 8):
  - `settings.KeyModrinthProject`, `KeyModrinthToken`, `KeyModrinthStart`; `func (s *Store) ModrinthConfig() modpack.Config`
  - `View.ModrinthProject`, `View.ModrinthToken` (`Masked`), `View.ModrinthReady bool` (JSON `modrinthProject`, `modrinthToken`, `modrinthReady`)
  - `Values()` liefert zusätzlich `modrinthProject`, `modrinthToken`, `modrinthStartVersion`
  - `api.Deps.Modpack *modpack.Channel`
  - Routen: `GET /api/modpack` → `modpack.Status`; `POST /api/settings/modrinth/test` → `{"message": …}` bzw. 502 `{"error": …}`
  - `PUT /api/settings` akzeptiert `modrinthProject`, `modrinthToken`, `modrinthStartVersion`

- [ ] **Step 1: Failing tests schreiben**

An `internal/settings/settings_test.go` anhängen (Import `testing` ist vorhanden; ggf. `os` nicht nötig):

```go
func TestModrinthConfigDBBeforeEnv(t *testing.T) {
	t.Setenv("MSM_MODRINTH_PROJECT", "env-pack")
	t.Setenv("MSM_MODRINTH_TOKEN", "env-token")
	db := map[string]string{}
	s := New(func(k string) string { return db[k] }, func(k, v string) { db[k] = v })
	if c := s.ModrinthConfig(); c.Project != "env-pack" || c.Token != "env-token" || c.StartVersion != "" {
		t.Errorf("env-fallback = %+v", c)
	}
	s.Set(KeyModrinthProject, "mein-pack")
	s.Set(KeyModrinthStart, "2.0.0")
	c := s.ModrinthConfig()
	if c.Project != "mein-pack" || c.Token != "env-token" || c.StartVersion != "2.0.0" {
		t.Errorf("config = %+v", c)
	}
	v := s.View()
	if !v.ModrinthReady || v.ModrinthProject.Source != "dashboard" || v.ModrinthToken.Source != "env" {
		t.Errorf("view = %+v", v)
	}
	vals := s.Values()
	if vals["modrinthProject"] != "mein-pack" || vals["modrinthToken"] != "env-token" || vals["modrinthStartVersion"] != "2.0.0" {
		t.Errorf("values = %v", vals)
	}
}
```

An `internal/api/settings_test.go` anhängen:

```go
func TestSaveModrinthSettings(t *testing.T) {
	srv, db := settingsServer(t)
	rec := putSettings(srv, `{"modrinthProject":" mein-pack ","modrinthToken":"mrp_x","modrinthStartVersion":"1.2.0"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if db[settings.KeyModrinthProject] != "mein-pack" || db[settings.KeyModrinthToken] != "mrp_x" || db[settings.KeyModrinthStart] != "1.2.0" {
		t.Errorf("db = %v", db)
	}
	if rec := putSettings(srv, `{"modrinthStartVersion":"v1"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("ungültige Startversion: status = %d", rec.Code)
	}
}
```

- [ ] **Step 2: Tests laufen lassen, Fehlschlag prüfen**

Run: `go test ./internal/settings/ ./internal/api/`
Expected: FAIL (`undefined: KeyModrinthProject`, `ModrinthConfig`, …)

- [ ] **Step 3: Settings implementieren** — `internal/settings/settings.go`:

Import `modpack` ergänzen. Konstanten erweitern:

```go
	KeyModrinthProject = "settings.modrinth.project"
	KeyModrinthToken   = "settings.modrinth.token"
	KeyModrinthStart   = "settings.modrinth.startversion"
```

Nach `DropboxConfig` einfügen:

```go
// ModrinthConfig returns the effective Modrinth-Modpack settings (DB vor
// .env; der Startwert existiert nur im Dashboard).
func (s *Store) ModrinthConfig() modpack.Config {
	pick := func(key, env string) string {
		if v := s.value(key); v != "" {
			return v
		}
		return strings.TrimSpace(os.Getenv(env))
	}
	return modpack.Config{
		Project:      pick(KeyModrinthProject, "MSM_MODRINTH_PROJECT"),
		Token:        pick(KeyModrinthToken, "MSM_MODRINTH_TOKEN"),
		StartVersion: s.value(KeyModrinthStart),
	}
}
```

`View` erweitern um

```go
	ModrinthProject Masked `json:"modrinthProject"`
	ModrinthToken   Masked `json:"modrinthToken"`
	ModrinthReady   bool   `json:"modrinthReady"`
```

und in `View()` füllen:

```go
		ModrinthProject: s.masked(KeyModrinthProject, "MSM_MODRINTH_PROJECT"),
		ModrinthToken:   s.masked(KeyModrinthToken, "MSM_MODRINTH_TOKEN"),
		ModrinthReady:   s.ModrinthConfig().Complete(),
```

In `Values()` vor `return` `mr := s.ModrinthConfig()` und in die Map:

```go
		"modrinthProject":      mr.Project,
		"modrinthToken":        mr.Token,
		"modrinthStartVersion": mr.StartVersion,
```

- [ ] **Step 4: API implementieren**

`internal/api/settings.go` — im Request-Struct von `handleSaveSettings` ergänzen:

```go
		ModrinthProject      *string `json:"modrinthProject"`
		ModrinthToken        *string `json:"modrinthToken"`
		ModrinthStartVersion *string `json:"modrinthStartVersion"`
```

Vor dem abschließenden `writeJSON` einfügen (Import `modpack` ergänzen):

```go
	if v, ok := changed(req.ModrinthStartVersion, "modrinthStartVersion"); ok {
		if v != "" && !modpack.ValidVersion(v) {
			httpError(w, http.StatusBadRequest, "Startversion muss die Form 1.0.0 haben")
			return
		}
		s.settings.Set(settings.KeyModrinthStart, v)
		s.audit(r.Context(), "settings.modrinth", "startversion="+v)
	}
	setMR := func(ptr *string, field, key, name string) {
		if v, ok := changed(ptr, field); ok {
			s.settings.Set(key, v)
			s.audit(r.Context(), "settings.modrinth", name+" geändert (Wert nicht protokolliert)")
		}
	}
	setMR(req.ModrinthProject, "modrinthProject", settings.KeyModrinthProject, "projekt")
	setMR(req.ModrinthToken, "modrinthToken", settings.KeyModrinthToken, "token")
```

Hinweis: Die Startversion wird VOR den anderen Feldern geprüft, damit eine ungültige Eingabe nichts halb speichert — den Block deshalb direkt nach dem Discord-Webhook-Block einfügen (vor `set := func…`), die beiden `setMR`-Aufrufe ans Ende.

Neuer Handler in `internal/api/settings.go`:

```go
// handleModrinthTest prüft Token + Projekt („Verbindung prüfen").
func (s *Server) handleModrinthTest(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	msg, err := s.modpack.Check(ctx)
	if err != nil {
		s.audit(r.Context(), "settings.modrinth.test", "fehlgeschlagen: "+err.Error())
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.audit(r.Context(), "settings.modrinth.test", "ok")
	writeJSON(w, http.StatusOK, map[string]string{"message": msg})
}
```

`internal/api/mods.go` — neuer Handler:

```go
// handleModpackStatus liefert den letzten Modrinth-Modpack-Stand fürs Dashboard.
func (s *Server) handleModpackStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.modpack.Status())
}
```

`internal/api/api.go`: Import `modpack`; Feld `modpack *modpack.Channel`; `Deps.Modpack *modpack.Channel`; in `New` `modpack: d.Modpack,`. Routen (nach dem Einstellungen-Block):

```go
	if s.modpack != nil {
		mux.HandleFunc("GET /api/modpack", s.handleModpackStatus)
		if s.settings != nil {
			mux.HandleFunc("POST /api/settings/modrinth/test", s.handleModrinthTest)
		}
	}
```

- [ ] **Step 5: Tests laufen lassen**

Run: `go vet ./... && go test ./...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/settings internal/api
git commit -m "feat(api): Modrinth-Einstellungen, Verbindungsprüfung, Modpack-Status

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: Modrinth-Kanal in `main.go` verdrahten

**Files:**
- Modify: `cmd/msm/main.go`

**Interfaces:**
- Consumes: Task 4 (`packpub.New`), Task 5 (`modpack.New`), Task 6 (`cfg.ModrinthConfig`, `api.Deps.Modpack`).

- [ ] **Step 1: Modrinth-Client beschaffen** — direkt vor `packs := packpub.New(…)` einfügen (Import `modpack` ergänzen):

```go
	// Modrinth-Modpack (zweiter Versandweg): nur mit echtem Modrinth-Client,
	// im Mock-Modus bleibt der Kanal aus
	var mpChannel *modpack.Channel
	if mrc, ok := modAPI.(*modrinth.Client); ok && clientDirs != nil {
		mpChannel = modpack.New(mrc, cfg.ModrinthConfig, clientDirs, mcDataDir,
			func() string { return mcState().Version }, kvGet, kvSet, log)
	}
	channels := []packpub.Channel{dropbox.Channel{C: dbx, Dirs: clientDirs}}
	if mpChannel != nil {
		channels = append(channels, mpChannel)
	}
```

und `packs := packpub.New(…)` ändern zu:

```go
	packs := packpub.New(func() string { return packFingerprint(clientDirs) }, kvGet, kvSet, bus, log, channels...)
```

Prüfen, dass `mcDataDir` an dieser Stelle definiert ist (wird schon für den Crash-Wächter genutzt) und `modrinth` importiert ist (Zeile `modAPI = modrinth.New()`).

- [ ] **Step 2: API-Deps ergänzen** — in `api.New(api.Deps{…})` nach `Packs: packs,` einfügen:

```go
			Modpack:           mpChannel,
```

Achtung: `api.Deps.Modpack` ist ein `*modpack.Channel`; bei `mpChannel == nil` registriert die API die Modpack-Routen nicht (Prüfung `s.modpack != nil` in Task 6).

- [ ] **Step 3: Build + Tests + Mock-Start**

Run: `go vet ./... && go test ./... && go build ./cmd/msm`
Expected: PASS, Binary baut. Kurzer Mock-Start: `go run ./cmd/msm -mock -addr 127.0.0.1:8090` startet ohne Panic (Strg+C beenden).

- [ ] **Step 4: Commit**

```bash
git add cmd/msm/main.go
git commit -m "feat: Modrinth-Modpack als zweiten Versandweg verdrahten

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Oberfläche — Einstellungen und Mods-Tab

**Files:**
- Modify: `web/src/lib/stream.js`, `web/src/lib/Settings.svelte`, `web/src/lib/Mods.svelte`

**Interfaces:**
- Consumes: Task 6 (Routen und Felder).

- [ ] **Step 1: API-Helfer** — in `web/src/lib/stream.js` die Zeile `export const publishClientPack = …` ersetzen und ergänzen:

```js
export const publishClientPack = (channel) =>
  apiFetch(`/api/mods/publish${channel ? `?channel=${encodeURIComponent(channel)}` : ''}`, { method: 'POST' })
export const modpackStatus = () => apiFetch('/api/modpack')
export const testModrinth = () => apiFetch('/api/settings/modrinth/test', { method: 'POST' })
```

- [ ] **Step 2: Einstellungen** — `web/src/lib/Settings.svelte`:

Import erweitern um `testModrinth`. `fields` initialisieren mit zusätzlich `modrinthProject: '', modrinthToken: '', modrinthStartVersion: ''`. In `save()` das gesendete Objekt ergänzen:

```js
        modrinthProject: fields.modrinthProject.trim(),
        modrinthToken: fields.modrinthToken.trim(),
        modrinthStartVersion: fields.modrinthStartVersion.trim(),
```

Neue Funktion neben `doTest`:

```js
  async function doTestModrinth() {
    error = ''
    info = ''
    try {
      const res = await testModrinth()
      info = res.message
    } catch (err) {
      error = err.message
    }
  }
```

Im Markup direkt vor `<button class="btn accent" onclick={save}>Speichern</button>` einfügen:

```svelte
    <h3>Modrinth-Modpack (Updates in der Modrinth App)</h3>
    <p class="dim">Status: <strong>{view.modrinthReady ? '✔ eingerichtet — mit „Verbindung prüfen" testen' : 'nicht eingerichtet — Anleitung unten'}</strong></p>
    <div class="grid">
      <label>Projekt (Slug oder ID) <span class="dim">({status(view.modrinthProject)})</span>
        <input type="text" bind:value={fields.modrinthProject} placeholder="mein-server-pack" /></label>
      <label>Personal Access Token <span class="dim">({status(view.modrinthToken)})</span>
        <input type="text" bind:value={fields.modrinthToken} placeholder="mrp_…" /></label>
      <label>Startversion <span class="dim">(Standard 1.0.0 — gilt nur für die erste Veröffentlichung)</span>
        <input type="text" bind:value={fields.modrinthStartVersion} placeholder="1.0.0" /></label>
    </div>
    {#if view.modrinthReady}
      <div class="row"><button class="btn" onclick={doTestModrinth}>Verbindung prüfen</button></div>
    {/if}
```

Nach dem bestehenden `</details>` der Dropbox-Anleitung einfügen:

```svelte
    <details class="howto">
      <summary>📖 Anleitung: Modrinth-Modpack einrichten (einmalig, ~5 Minuten)</summary>
      <ol>
        <li><a href="https://modrinth.com" target="_blank" rel="noreferrer">modrinth.com</a> → anmelden → <strong>„Create a project"</strong> → Typ <strong>Modpack</strong> → Name, kurze Beschreibung, Lizenz und Icon eintragen. Den <strong>Slug</strong> (Teil der Adresse, z. B. <code>mein-server-pack</code>) oben als Projekt eintragen.</li>
        <li>Profilbild → <strong>Settings → Personal access tokens</strong> → „Create a PAT" → Name z. B. <code>MSM</code>, Rechte <strong>Create versions</strong> und <strong>Read projects</strong> → Token (beginnt mit <code>mrp_</code>) oben eintragen. <em>Der Token wird nur einmal angezeigt.</em></li>
        <li><strong>Speichern</strong> → <strong>„Verbindung prüfen"</strong> muss „✔ Verbunden …" melden.</li>
        <li>Mods-Tab → Profil „Client-Paket" → <strong>„Modpack veröffentlichen (Modrinth)"</strong> → die erste Version wird hochgeladen.</li>
        <li>Auf Modrinth beim Projekt <strong>„Submit for review"</strong> — erst nach der Freigabe durch Modrinth ist das Modpack öffentlich. Ab dann lädt MSM jede Änderung automatisch hoch, und die Spieler bekommen in der Modrinth App einen „Update"-Knopf.</li>
      </ol>
      <p class="dim">Spieler installieren das Modpack einmal über die Modrinth App (Modpack suchen → Installieren). Danach genügt bei jeder neuen Version ein Klick auf „Update"; Welten und Einstellungen bleiben erhalten.</p>
    </details>
```

- [ ] **Step 3: Mods-Tab** — `web/src/lib/Mods.svelte`:

Import erweitern um `modpackStatus`. `doPublish` ersetzen durch:

```js
  let modpack = $state(null)
  async function loadModpack() {
    try {
      modpack = await modpackStatus()
    } catch {
      modpack = null // Feature aus (Route fehlt) oder nicht erreichbar
    }
  }
  onMount(loadModpack)

  async function doPublish(channel) {
    const text = channel === 'modrinth'
      ? 'Client-Paket jetzt als neue Version des Modrinth-Modpacks hochladen?'
      : 'Client-Paket als ZIP zu Dropbox hochladen und den Link in Discord posten?\nVorher einmal Mod-Lizenzen geprüft?'
    if (!confirm(text)) return
    try {
      const res = await publishClientPack(channel)
      info = res.message
      error = ''
      if (channel === 'modrinth') setTimeout(loadModpack, 15000)
    } catch (err) {
      error = err.message
    }
  }
```

(`onMount` ist bereits importiert; `info`/`error` sind unterhalb deklariert — die Funktionen werden erst nach dem Mount aufgerufen, das ist in Svelte 5 unkritisch.)

Den Dropbox-Knopf ersetzen durch:

```svelte
    {#if profile === 'client'}
      <button class="btn" onclick={() => doPublish('dropbox')} disabled={busy}>Client-Paket veröffentlichen (Dropbox)</button>
      {#if modpack?.ready}
        <button class="btn" onclick={() => doPublish('modrinth')} disabled={busy}>Modpack veröffentlichen (Modrinth)</button>
      {/if}
    {/if}
```

Direkt nach dem schließenden `</div>` der `mod-actions` einfügen:

```svelte
  {#if profile === 'client' && modpack?.ready}
    <div class="dim-sm">
      Modrinth-Modpack: {modpack.version ? `zuletzt ${modpack.version} (MC ${modpack.mc} · Fabric ${modpack.loader})` : 'noch nicht veröffentlicht'}
      {#if modpack.projectUrl} · <a href={modpack.projectUrl} target="_blank" rel="noreferrer">Projektseite</a>{/if}
    </div>
    {#if modpack.omitted?.length}
      <div class="warn-msg">⚠️ Nicht im Modpack (nicht auf Modrinth): {modpack.omitted.join(', ')}</div>
    {/if}
    {#if modpack.lastError}
      <div class="err-msg">Letzter Modpack-Versand fehlgeschlagen: {modpack.lastError}</div>
    {/if}
  {/if}
```

Falls `.warn-msg`/`.dim-sm` im `<style>` von `Mods.svelte` fehlen, ergänzen:

```css
  .warn-msg { color: var(--warn); font-size: 0.85rem; margin: 0.3rem 0; }
  .dim-sm { font-size: 0.75rem; color: var(--text-dim); }
```

- [ ] **Step 4: Build**

Run: `cd web && npm run build && cd .. && go build ./cmd/msm`
Expected: Build ok ohne Svelte-Warnungen zu unbenutzten Variablen.

- [ ] **Step 5: Commit**

```bash
git add web/src/lib
git commit -m "feat(web): Modrinth-Modpack in Einstellungen und Mods-Tab

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: Doku

**Files:**
- Modify: `.env.example` (Variablen `MSM_MODRINTH_PROJECT`, `MSM_MODRINTH_TOKEN` mit Kommentar „optional, auch im Einstellungen-Tab")
- Modify: `README.md` (Abschnitt Client-Paket: Modrinth-Modpack als zweiter Weg, Verweis auf die Anleitung im Einstellungen-Tab)
- Vault: „MSM – Modrinth-Modpack" (Status → umgesetzt, Umsetzungsbericht), „MSM – Nutzungshandbuch" (Abschnitt Modpack für Admin + Spieler), „MSM – Architektur & Code-Struktur" (Pakete `packpub`, `mrpack`, `modpack`; Routen `GET /api/modpack`, `POST /api/settings/modrinth/test`, `?channel=` am Versand; app_state-Schlüssel `modpack.state`, `clientpack.published.<kanal>`; Env-Variablen)

- [ ] **Step 1:** Dateien wie oben ergänzen (Texte deutsch, sachlich).
- [ ] **Step 2:** `go test ./... && cd web && npm run build` — nichts kaputt.
- [ ] **Step 3: Commit**

```bash
git add .env.example README.md
git commit -m "docs: Modrinth-Modpack (Einrichtung, Variablen)

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Live-Test (Controller, nach Task 9)

In WSL (`deploy-branch.sh` auf `feature/modrinth-modpack` umstellen), mit dem echten, noch nicht freigegebenen Projekt des Nutzers (Token trägt der Nutzer im Einstellungen-Tab ein):
1. „Verbindung prüfen" → „✔ Verbunden … noch nicht freigegeben".
2. Knopf „Modpack veröffentlichen" → Version `1.0.0` auf Modrinth (Entwurf), Changelog „Erste Version", Discord ohne Modrinth-Zeile (Projekt nicht öffentlich).
3. Client-Mod hinzufügen + Neustart → `1.1.0`, Dropbox + Modrinth beide verschickt; nur Mod-Update → PATCH.
4. Falsches Token → Admin-Fehler „Modrinth lehnt den Token ab (401)", Dropbox läuft trotzdem; Token korrigieren → nächster Neustart holt nur Modrinth nach.
5. MC-Update 1.21.11 → 26.3 → MAJOR-Version mit neuer MC-/Loader-Version.
6. Nutzer prüft am PC in der Modrinth App: Installation des Modpacks, „Update"-Knopf bei neuer Version, Verhalten beim MC-Versionswechsel → Ergebnis in Spec/Vault nachtragen (ggf. Text der MAJOR-Hinweiszeile anpassen).
