# Basis-Modpack im Client-Paket – Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Das Client-Paket kann auf einem Modrinth-Modpack (z. B. Fabulously Optimized) aufbauen: Pack-Mods als Ganzes übernehmen und aktualisieren, einzelne abschalten, eigene Mods ergänzen, nur freigegebene Configs mitliefern.

**Architecture:** `internal/mrpack` lernt `.mrpack` lesen (inkl. `overrides`). Neues Paket `internal/basepack`: reine Logik (Index → Pack-Dateien, Config-Vorauswahl, Vergleich alt ↔ neu, Zusammenfassung) plus `Service` mit gespeichertem Stand (`app_state` „basepack.state"), der Dateiänderungen über die neue Methode `mods.Manager.ApplyFiles` (Staging + Sicherung, als `.basepack` markiert) ausführt und Configs selbst ins Client-Paket schreibt. Dropbox-ZIP, Modrinth-Modpack (Kanal) und Fingerabdruck nehmen die geschriebenen Configs mit; Pack-Dateien aus dem Index (auch GitHub/GitLab) werden im Modrinth-Modpack direkt verlinkt. Versions-Watch und Ein-Klick-Update kennen das Pack.

**Tech Stack:** Go 1.26 (stdlib `archive/zip`), Svelte 5 (Runes), Modrinth API v2, SQLite app_state.

**Spec:** `docs/specs/2026-11-21-basis-modpack-design.md` (Vault: „MSM – Basis-Modpack").

**Entscheidungen über die Spec hinaus (Spec-Absicht unverändert):**
- Configs liegen nicht im Mod-Profil (dessen Kategorien sind flache Ordner), sondern werden von `basepack` direkt unter dem Client-Paket-Wurzelordner geschrieben (Pfad aus der `.mrpack`, z. B. `config/x.json`); `basepack` führt die geschriebenen Pfade im Stand. Dropbox, Modrinth-Kanal und Fingerabdruck lesen diese Liste.
- Jede Basis-Pack-Dateiänderung ist ein eigener Sicherungssatz mit Markierung `.basepack`. Der normale Rollback stoppt dort (wie beim MC-Update-Satz) — sonst liefen Dateien und gespeicherter Pack-Stand auseinander. Zurück geht es im Bereich „Basis-Modpack" (andere Version übernehmen, Mod wieder anschalten).
- Wieder Anschalten lädt die Datei neu von der Pack-Quelle (Prüfsumme aus dem Pack-Index), statt sie aus der Sicherung zu holen.

## Global Constraints

- Gitflow: Branch `feature/base-modpack` (von `feature/modrinth-modpack`). Commits `feat:`/`fix:`/`test:`/`docs:`, Endzeile `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Nur Client-Paket; genau ein Basis-Pack; Loader `fabric`.
- Pack-Update: Vergleich per Schlüssel = Modrinth-Projekt-ID der Datei, sonst Pfad. Neu → dazu und an; im Pack entfernt → raus (auch wenn abgeschaltet); aktualisiert → ersetzt, Abschaltung bleibt; eigene Mod auch im Pack → Pack übernimmt, eigene Kopie fällt weg; vom Pack übernommene frühere eigene Mod entfällt mit dem Pack → Hinweis „X entfällt — war vorher auch deine eigene Mod, bei Bedarf neu hinzufügen"; neue Config → aus, markiert „neu"; freigegebene Config geändert → ersetzt; freigegebene Config entfällt → raus.
- Pack-Mods werden nie einzeln aktualisiert (Update-Prüfung überspringt sie).
- Config-Vorauswahl AUS: `options.txt`, `optionsof.txt`, `optionsshaders.txt`, `servers.dat`, `config/sodium-options.json`, `config/iris.properties`, `config/xaero*`, `config/controlling*`, Dateinamen mit `keybind`, alles außerhalb von `config/`. Übriges unter `config/` AN. Neue Configs aus Updates starten AUS.
- Quellen der Configs: `overrides/` und `client-overrides/` (letztere gewinnen); `server-overrides/` ignorieren. Pack-Dateien mit `env.client = unsupported` überspringen. Unbekannte Ordner ignorieren und nennen. Bekannte Ordner: `mods`, `resourcepacks`, `shaderpacks`, `config` (bzw. Config-Dateien).
- `.mrpack`-Download höchstens 64 MB.
- Modrinth-Modpack: erlaubte Download-Quellen `cdn.modrinth.com`, `github.com`, `raw.githubusercontent.com`, `gitlab.com`; freigegebene Configs als `overrides/<pfad>`.
- MC-Update: verweigert, wenn ein Basis-Pack gesetzt ist und keine Version für die Zielversion existiert („Basis-Modpack „X“ gibt es noch nicht für Minecraft Y — abwarten oder Basis-Pack im Mods-Tab entfernen"); nach erfolgreichem Sprung Pack automatisch auf die passende Version umstellen (gleiche Config-Auswahl, gleiche Abschaltungen).
- Alle Basis-Pack-Aktionen während eines MC-Updates → 409.
- Discord: ein Pack-Wechsel = EINE Zeile, z. B. „Fabulously Optimized 6.4.0 → 6.5.0 (3 neu, 1 entfernt, 12 aktualisiert)".
- Nutzer-sichtbare Texte deutsch. `go vet ./...`, `go test ./...` grün; `cd web && npm run build` baut.

## File Structure

| Datei | Aktion | Verantwortung |
|---|---|---|
| `internal/mrpack/mrpack.go` (+test) | ändern | `Parse` (Index + Overrides), `Build` mit `Meta.Overrides` |
| `internal/modrinth/client.go` (+test) | ändern | `SearchModpacks` |
| `internal/basepack/pack.go` (+test) | neu | `PackFile`, `State`, `Key`, `FromIndex`, `DefaultOn`, `Diff`/`Plan`, `Summary` |
| `internal/mods/files.go` (+test) | neu | `FileAdd`, `FileRemove`, `ApplyFiles`, `SetPinned`, `.basepack`-Marke |
| `internal/mods/mods.go` | ändern | `Entry.Source/Disabled`, Pinned in `CheckUpdates`, Rollback-Stopp `.basepack` |
| `internal/basepack/service.go` (+test) | neu | Stand, Suche, Vorschau, Übernehmen, An/Aus, Configs, Entfernen, Update-Erkennung, MC-Umstellung, Abfragen |
| `internal/modnews/modnews.go` (+test) | ändern | Aktion `pack` als eigene Zeile |
| `internal/dropbox/publish.go` (+test), `internal/modpack/channel.go` (+test), `cmd/msm/main.go` (`packFingerprint`) | ändern | Configs und Pack-Index-Dateien verteilen |
| `internal/mods/versionwatch.go`, `internal/upgrade/upgrade.go` (+tests) | ändern | Pack in Kachel, Update-Sperre, Umstellung |
| `internal/api/basepack.go` (neu), `api.go`, `mods.go` (+test) | ändern | Routen, Mod-Liste mit Herkunft |
| `cmd/msm/main.go` | ändern | Verdrahtung |
| `web/src/lib/BasePack.svelte` (neu), `Mods.svelte`, `stream.js` | ändern | Oberfläche |
| Doku, Vault | ändern | |

---

### Task 1: `.mrpack` lesen und Overrides schreiben

**Files:**
- Modify: `internal/mrpack/mrpack.go`
- Test: `internal/mrpack/mrpack_test.go`

**Interfaces:**
- Produces:
  - `type IndexFile struct { Path, SHA1, SHA512 string; URLs []string; Size int64; ClientEnv string }`
  - `type Index struct { Name, VersionID, Summary string; Dependencies map[string]string; Files []IndexFile; Overrides map[string][]byte }`
  - `func Parse(data []byte) (*Index, error)`
  - `Meta.Overrides map[string][]byte` (Pfad relativ zur Instanz → Inhalt), in `Build` als `overrides/<pfad>`

- [ ] **Step 1: Failing tests** — an `internal/mrpack/mrpack_test.go` anhängen:

```go
func TestParseRoundTripWithOverrides(t *testing.T) {
	data, err := Build([]File{
		{Path: "mods/sodium.jar", SHA1: "s1", SHA512: "s512", URL: "https://cdn.modrinth.com/a/sodium.jar", Size: 10},
	}, Meta{VersionID: "6.4.0", Name: "FO", Minecraft: "26.3", FabricLoader: "0.20.1",
		Overrides: map[string][]byte{"config/a.json": []byte("A"), "options.txt": []byte("O")}})
	if err != nil {
		t.Fatal(err)
	}
	idx, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if idx.Name != "FO" || idx.VersionID != "6.4.0" || idx.Dependencies["minecraft"] != "26.3" {
		t.Errorf("kopf = %+v", idx)
	}
	if len(idx.Files) != 1 || idx.Files[0].Path != "mods/sodium.jar" || idx.Files[0].SHA1 != "s1" ||
		idx.Files[0].SHA512 != "s512" || idx.Files[0].URLs[0] != "https://cdn.modrinth.com/a/sodium.jar" || idx.Files[0].Size != 10 {
		t.Errorf("files = %+v", idx.Files)
	}
	if string(idx.Overrides["config/a.json"]) != "A" || string(idx.Overrides["options.txt"]) != "O" {
		t.Errorf("overrides = %v", idx.Overrides)
	}
}

func TestParseClientOverridesWinServerIgnored(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name, body string) {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	write("modrinth.index.json", `{"formatVersion":1,"game":"minecraft","versionId":"1","name":"P",
		"files":[{"path":"mods/srv.jar","hashes":{"sha1":"a","sha512":"b"},"env":{"client":"unsupported","server":"required"},"downloads":["https://cdn.modrinth.com/x"],"fileSize":1}],
		"dependencies":{"minecraft":"26.3","fabric-loader":"0.20.1"}}`)
	write("overrides/config/x.json", "base")
	write("client-overrides/config/x.json", "client")
	write("server-overrides/config/y.json", "server")
	write("overrides/../evil.txt", "evil")
	zw.Close()

	idx, err := Parse(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if string(idx.Overrides["config/x.json"]) != "client" {
		t.Errorf("client-overrides müssen gewinnen: %q", idx.Overrides["config/x.json"])
	}
	if _, ok := idx.Overrides["config/y.json"]; ok {
		t.Error("server-overrides dürfen nicht übernommen werden")
	}
	if len(idx.Overrides) != 1 {
		t.Errorf("overrides = %v (ungültige Pfade müssen fehlen)", idx.Overrides)
	}
	if idx.Files[0].ClientEnv != "unsupported" {
		t.Errorf("ClientEnv = %q", idx.Files[0].ClientEnv)
	}
}

func TestParseRejectsMissingIndexAndBadPaths(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("readme.txt")
	w.Write([]byte("x"))
	zw.Close()
	if _, err := Parse(buf.Bytes()); err == nil {
		t.Error("ZIP ohne modrinth.index.json akzeptiert")
	}
	var b2 bytes.Buffer
	zw = zip.NewWriter(&b2)
	w, _ = zw.Create("modrinth.index.json")
	w.Write([]byte(`{"formatVersion":1,"game":"minecraft","versionId":"1","name":"P","files":[{"path":"../x.jar","hashes":{"sha1":"a","sha512":"b"},"downloads":["https://cdn.modrinth.com/x"],"fileSize":1}],"dependencies":{}}`))
	zw.Close()
	if _, err := Parse(b2.Bytes()); err == nil {
		t.Error("Pfad mit .. akzeptiert")
	}
}
```

- [ ] **Step 2:** `cd /c/Entwicklung/GIT/Minecraft-Server-Management && go test ./internal/mrpack/` → FAIL (`undefined: Parse`, unbekanntes Feld `Overrides`).

- [ ] **Step 3: Implementierung** in `internal/mrpack/mrpack.go`. Import `io` ergänzen. `Meta` erweitern:

```go
	// Overrides: Dateien, die ins Instanzverzeichnis kopiert werden
	// (Pfad relativ zur Instanz, z. B. "config/sodium.json")
	Overrides map[string][]byte
```

In `Build` direkt vor `if err := zw.Close(); err != nil {` einfügen:

```go
	paths := make([]string, 0, len(meta.Overrides))
	for p := range meta.Overrides {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if !ValidPath(p) {
			return nil, fmt.Errorf("mrpack: ungültiger Override-Pfad %q", p)
		}
		ow, err := zw.Create("overrides/" + p)
		if err != nil {
			return nil, err
		}
		if _, err := ow.Write(meta.Overrides[p]); err != nil {
			return nil, err
		}
	}
```

Am Dateiende ergänzen:

```go
// IndexFile is one file entry of a parsed pack.
type IndexFile struct {
	Path      string
	SHA1      string
	SHA512    string
	URLs      []string
	Size      int64
	ClientEnv string // "required" | "optional" | "unsupported" | "" (fehlt = nötig)
}

// Index is a parsed .mrpack.
type Index struct {
	Name         string
	VersionID    string
	Summary      string
	Dependencies map[string]string
	Files        []IndexFile
	// Overrides: overrides/ + client-overrides/ (gewinnt); server-overrides/
	// wird ignoriert. Schlüssel = Pfad relativ zur Instanz.
	Overrides map[string][]byte
}

// maxOverride limits a single override file (Configs sind klein).
const maxOverride = 8 << 20

// Parse reads a .mrpack (ZIP).
func Parse(data []byte) (*Index, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("mrpack: kein gültiges ZIP: %w", err)
	}
	var raw struct {
		VersionID string `json:"versionId"`
		Name      string `json:"name"`
		Summary   string `json:"summary"`
		Files     []struct {
			Path      string            `json:"path"`
			Hashes    map[string]string `json:"hashes"`
			Env       map[string]string `json:"env"`
			Downloads []string          `json:"downloads"`
			FileSize  int64             `json:"fileSize"`
		} `json:"files"`
		Dependencies map[string]string `json:"dependencies"`
	}
	found := false
	base := map[string][]byte{}
	client := map[string][]byte{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if f.Name == "modrinth.index.json" {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			err = json.NewDecoder(rc).Decode(&raw)
			rc.Close()
			if err != nil {
				return nil, fmt.Errorf("mrpack: index unlesbar: %w", err)
			}
			found = true
			continue
		}
		var target map[string][]byte
		var rel string
		switch {
		case strings.HasPrefix(f.Name, "client-overrides/"):
			target, rel = client, strings.TrimPrefix(f.Name, "client-overrides/")
		case strings.HasPrefix(f.Name, "overrides/"):
			target, rel = base, strings.TrimPrefix(f.Name, "overrides/")
		default:
			continue // server-overrides und Sonstiges
		}
		if !ValidPath(rel) || f.UncompressedSize64 > maxOverride {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(io.LimitReader(rc, maxOverride+1))
		rc.Close()
		if err != nil {
			return nil, err
		}
		target[rel] = b
	}
	if !found {
		return nil, fmt.Errorf("mrpack: modrinth.index.json fehlt")
	}
	idx := &Index{Name: raw.Name, VersionID: raw.VersionID, Summary: raw.Summary,
		Dependencies: raw.Dependencies, Overrides: base}
	for p, b := range client {
		idx.Overrides[p] = b
	}
	for _, f := range raw.Files {
		if !ValidPath(f.Path) {
			return nil, fmt.Errorf("mrpack: ungültiger Pfad %q im Index", f.Path)
		}
		idx.Files = append(idx.Files, IndexFile{Path: f.Path, SHA1: f.Hashes["sha1"], SHA512: f.Hashes["sha512"],
			URLs: f.Downloads, Size: f.FileSize, ClientEnv: f.Env["client"]})
	}
	return idx, nil
}
```

- [ ] **Step 4:** `go test ./internal/mrpack/ && go vet ./...` → PASS.
- [ ] **Step 5: Commit** `feat(mrpack): .mrpack lesen, Overrides schreiben`.

---

### Task 2: Modpacks auf Modrinth suchen

**Files:** Modify `internal/modrinth/client.go`; Test `internal/modrinth/client_test.go`

**Interfaces:** Produces `func (c *Client) SearchModpacks(ctx context.Context, query, loader, mcVersion string, limit int) ([]SearchHit, error)`

- [ ] **Step 1: Failing test:**

```go
func TestSearchModpacksFacets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f := r.URL.Query().Get("facets")
		if r.URL.Path != "/search" || f != `[["project_type:modpack"],["categories:fabric"],["versions:26.3"]]` {
			http.Error(w, "bad facets: "+f, http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"hits": []SearchHit{{ProjectID: "FO", Title: "Fabulously Optimized"}}})
	}))
	defer srv.Close()
	hits, err := NewWithBase(srv.URL).SearchModpacks(context.Background(), "fabulously", "fabric", "26.3", 10)
	if err != nil || len(hits) != 1 || hits[0].ProjectID != "FO" {
		t.Fatalf("hits = %+v, err = %v", hits, err)
	}
}
```

- [ ] **Step 2:** `go test ./internal/modrinth/` → FAIL.
- [ ] **Step 3:** `Search` auf gemeinsamen Helfer umstellen:

```go
func (c *Client) Search(ctx context.Context, query, loader, mcVersion string, limit int) ([]SearchHit, error) {
	return c.search(ctx, "mod", query, loader, mcVersion, limit)
}

// SearchModpacks finds modpacks with a version for loader + MC version.
func (c *Client) SearchModpacks(ctx context.Context, query, loader, mcVersion string, limit int) ([]SearchHit, error) {
	return c.search(ctx, "modpack", query, loader, mcVersion, limit)
}

func (c *Client) search(ctx context.Context, ptype, query, loader, mcVersion string, limit int) ([]SearchHit, error) {
	q := url.Values{}
	q.Set("query", query)
	q.Set("facets", fmt.Sprintf(`[["project_type:%s"],["categories:%s"],["versions:%s"]]`, ptype, loader, mcVersion))
	q.Set("limit", strconv.Itoa(limit))
	var out struct {
		Hits []SearchHit `json:"hits"`
	}
	if err := c.do(ctx, http.MethodGet, "/search?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return out.Hits, nil
}
```

- [ ] **Step 4:** `go test ./internal/modrinth/ && go vet ./...` → PASS.
- [ ] **Step 5: Commit** `feat(modrinth): Modpacks suchen`.

---

### Task 3: Basis-Pack-Logik (rein)

**Files:** Create `internal/basepack/pack.go`, `internal/basepack/pack_test.go`

**Interfaces:**
- Consumes: `mrpack.Index`, `mrpack.IndexFile` (Task 1)
- Produces:
  - `type PackFile struct { Path, Category, Filename, ProjectID, Version, Name, SHA1, SHA512, URL string; Size int64 }` (JSON: `path, category, filename, projectId, version, name, sha1, sha512, url, size`)
  - `type State struct { ProjectID, Slug, Title, VersionID, VersionNumber, MC string; Files []PackFile; Disabled []string; Configs map[string]bool; Absorbed []string; WrittenConfigs []string }` (JSON camelCase wie Feldnamen klein)
  - `func Key(f PackFile) string`
  - `func FromIndex(idx *mrpack.Index) (files []PackFile, configs []string, ignored []string)`
  - `func DefaultOn(path string) bool`
  - `type OwnFile struct { Category, Filename, Key, Name string }`
  - `type FileUpdate struct { Old, New PackFile }`
  - `type Plan struct { Add []PackFile; Update []FileUpdate; Remove []PackFile; AbsorbOwn []OwnFile; DropAbsorbed []string; NewConfigs []string }`
  - `func Diff(prev State, next []PackFile, nextConfigs []string, own []OwnFile) Plan`
  - `func Summary(prev State, title, version string, p Plan, total int) string`
  - `func (s State) IsDisabled(key string) bool`

- [ ] **Step 1: Failing tests** — `internal/basepack/pack_test.go`:

```go
package basepack

import (
	"strings"
	"testing"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mrpack"
)

func TestFromIndex(t *testing.T) {
	idx := &mrpack.Index{
		Files: []mrpack.IndexFile{
			{Path: "mods/sodium.jar", SHA1: "a", SHA512: "A", URLs: []string{"https://cdn.modrinth.com/s"}, Size: 1},
			{Path: "resourcepacks/rp.zip", SHA1: "b", SHA512: "B", URLs: []string{"https://github.com/x/rp.zip"}, Size: 2},
			{Path: "mods/server-only.jar", SHA1: "c", SHA512: "C", URLs: []string{"https://cdn.modrinth.com/c"}, ClientEnv: "unsupported"},
			{Path: "kubejs/x.js", SHA1: "d", SHA512: "D", URLs: []string{"https://cdn.modrinth.com/d"}},
			{Path: "mods/sub/x.jar", SHA1: "e", SHA512: "E", URLs: []string{"https://cdn.modrinth.com/e"}},
		},
		Overrides: map[string][]byte{"config/a.json": nil, "options.txt": nil, "mods/bundled.jar": nil},
	}
	files, configs, ignored := FromIndex(idx)
	if len(files) != 2 || files[0].Category != "mods" || files[0].Filename != "sodium.jar" || files[1].Category != "resourcepacks" || files[1].URL != "https://github.com/x/rp.zip" {
		t.Errorf("files = %+v", files)
	}
	if strings.Join(configs, ",") != "config/a.json,options.txt" {
		t.Errorf("configs = %v", configs)
	}
	if strings.Join(ignored, ",") != "kubejs/x.js,mods/bundled.jar,mods/sub/x.jar" {
		t.Errorf("ignored = %v", ignored)
	}
}

func TestDefaultOn(t *testing.T) {
	for p, want := range map[string]bool{
		"config/fabric/indigo-renderer.properties": true,
		"config/sodium-extra.json":                 true,
		"config/sodium-options.json":               false,
		"config/iris.properties":                   false,
		"config/xaerominimap.txt":                  false,
		"config/controlling.json":                  false,
		"config/amecs-keybinds.json":               false,
		"options.txt":                              false,
		"servers.dat":                              false,
		"resourcepacks.txt":                        false,
	} {
		if DefaultOn(p) != want {
			t.Errorf("DefaultOn(%q) != %v", p, want)
		}
	}
}

func pf(key, name, sha string) PackFile {
	return PackFile{Path: "mods/" + name + ".jar", Category: "mods", Filename: name + ".jar", ProjectID: key, Name: name, SHA512: sha}
}

func TestDiff(t *testing.T) {
	prev := State{
		Files:    []PackFile{pf("P-sod", "sodium", "s1"), pf("P-lith", "lithium", "l1"), pf("P-old", "oldmod", "o1"), pf("P-was-own", "wasown", "w1")},
		Disabled: []string{"P-lith"},
		Configs:  map[string]bool{"config/a.json": true},
		Absorbed: []string{"P-was-own"},
	}
	next := []PackFile{pf("P-sod", "sodium", "s2"), pf("P-lith", "lithium", "l2"), pf("P-new", "newmod", "n1"), pf("P-jade", "jade", "j1")}
	own := []OwnFile{{Category: "mods", Filename: "jade-own.jar", Key: "P-jade", Name: "Jade"}, {Category: "mods", Filename: "mine.jar", Key: "P-mine", Name: "Mine"}}
	p := Diff(prev, next, []string{"config/a.json", "config/b.json"}, own)

	if len(p.Add) != 2 || len(p.Update) != 2 || len(p.Remove) != 2 {
		t.Fatalf("plan = %+v", p)
	}
	if len(p.AbsorbOwn) != 1 || p.AbsorbOwn[0].Filename != "jade-own.jar" {
		t.Errorf("absorb = %+v", p.AbsorbOwn)
	}
	if len(p.DropAbsorbed) != 1 || p.DropAbsorbed[0] != "wasown" {
		t.Errorf("dropAbsorbed = %v", p.DropAbsorbed)
	}
	if len(p.NewConfigs) != 1 || p.NewConfigs[0] != "config/b.json" {
		t.Errorf("newConfigs = %v", p.NewConfigs)
	}
	if !prev.IsDisabled("P-lith") || prev.IsDisabled("P-sod") {
		t.Error("IsDisabled falsch")
	}
}

func TestSummary(t *testing.T) {
	prev := State{VersionNumber: "6.4.0"}
	p := Plan{Add: make([]PackFile, 3), Remove: make([]PackFile, 1), Update: make([]FileUpdate, 12)}
	if s := Summary(prev, "Fabulously Optimized", "6.5.0", p, 40); s != "Fabulously Optimized 6.4.0 → 6.5.0 (3 neu, 1 entfernt, 12 aktualisiert)" {
		t.Errorf("summary = %q", s)
	}
	if s := Summary(State{}, "Fabulously Optimized", "6.4.0", Plan{}, 40); s != "Basis-Modpack Fabulously Optimized 6.4.0 übernommen (40 Mods)" {
		t.Errorf("erstes = %q", s)
	}
}
```

- [ ] **Step 2:** `go test ./internal/basepack/` → FAIL.

- [ ] **Step 3: Implementierung** — `internal/basepack/pack.go`:

```go
// Package basepack lets the client pack build on a Modrinth modpack (z. B.
// Fabulously Optimized): Pack-Mods als getestete Kombination übernehmen und
// als Ganzes aktualisieren, einzelne abschalten, eigene Mods ergänzen, nur
// freigegebene Configs mitliefern.
package basepack

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mrpack"
)

// PackFile is one client file of the base pack.
type PackFile struct {
	Path      string `json:"path"`
	Category  string `json:"category"`
	Filename  string `json:"filename"`
	ProjectID string `json:"projectId,omitempty"`
	Version   string `json:"version,omitempty"`
	Name      string `json:"name,omitempty"`
	SHA1      string `json:"sha1"`
	SHA512    string `json:"sha512"`
	URL       string `json:"url"`
	Size      int64  `json:"size"`
}

// State is the persisted base-pack setup.
type State struct {
	ProjectID     string          `json:"projectId"`
	Slug          string          `json:"slug"`
	Title         string          `json:"title"`
	VersionID     string          `json:"versionId"`
	VersionNumber string          `json:"versionNumber"`
	MC            string          `json:"mc"`
	Files         []PackFile      `json:"files"`
	Disabled      []string        `json:"disabled,omitempty"`
	Configs       map[string]bool `json:"configs,omitempty"`
	// Absorbed: Schlüssel früher eigener Mods, die das Pack übernommen hat
	Absorbed []string `json:"absorbed,omitempty"`
	// WrittenConfigs: von MSM ins Client-Paket geschriebene Config-Pfade
	WrittenConfigs []string `json:"writtenConfigs,omitempty"`
}

// Key identifies a pack file across versions: Modrinth-Projekt, sonst Pfad.
func Key(f PackFile) string {
	if f.ProjectID != "" {
		return f.ProjectID
	}
	return f.Path
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// IsDisabled reports whether the pack file with key is switched off.
func (s State) IsDisabled(key string) bool { return contains(s.Disabled, key) }

var categories = map[string]bool{"mods": true, "resourcepacks": true, "shaderpacks": true}

// FromIndex splits a parsed pack into client files, config paths and
// ignored paths (unbekannte Ordner, Unterordner, Dateien in overrides/
// unterhalb der Mod-Ordner). Nur-Server-Dateien fallen still weg.
func FromIndex(idx *mrpack.Index) (files []PackFile, configs []string, ignored []string) {
	for _, f := range idx.Files {
		if f.ClientEnv == "unsupported" {
			continue
		}
		dir, name := path.Split(f.Path)
		dir = strings.TrimSuffix(dir, "/")
		if !categories[dir] || name == "" {
			ignored = append(ignored, f.Path)
			continue
		}
		url := ""
		if len(f.URLs) > 0 {
			url = f.URLs[0]
		}
		files = append(files, PackFile{Path: f.Path, Category: dir, Filename: name,
			SHA1: f.SHA1, SHA512: f.SHA512, URL: url, Size: f.Size})
	}
	for p := range idx.Overrides {
		first := strings.SplitN(p, "/", 2)[0]
		if categories[first] {
			ignored = append(ignored, p)
			continue
		}
		configs = append(configs, p)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	sort.Strings(configs)
	sort.Strings(ignored)
	return files, configs, ignored
}

// DefaultOn is the preselection for a config file: Spieler-Einstellungen
// (Grafik, Tasten, Shader, Server-Liste, Minimap) und alles außerhalb von
// config/ sind aus — sie würden bei jedem Paket-Update überschrieben.
func DefaultOn(p string) bool {
	if !strings.HasPrefix(p, "config/") {
		return false
	}
	lower := strings.ToLower(p)
	switch {
	case lower == "config/sodium-options.json", lower == "config/iris.properties",
		strings.HasPrefix(lower, "config/xaero"), strings.HasPrefix(lower, "config/controlling"),
		strings.Contains(path.Base(lower), "keybind"):
		return false
	}
	return true
}

// OwnFile is a file the admin added himself (nicht aus dem Pack).
type OwnFile struct {
	Category string
	Filename string
	Key      string
	Name     string
}

// FileUpdate is a pack file whose content changed between versions.
type FileUpdate struct {
	Old PackFile
	New PackFile
}

// Plan is the difference between the current and a new pack version.
type Plan struct {
	Add          []PackFile   `json:"add"`
	Update       []FileUpdate `json:"update"`
	Remove       []PackFile   `json:"remove"`
	AbsorbOwn    []OwnFile    `json:"absorbOwn"`
	DropAbsorbed []string     `json:"dropAbsorbed"`
	NewConfigs   []string     `json:"newConfigs"`
}

// Diff compares the current state with the files and configs of a new
// pack version. own sind die eigenen Dateien des Client-Pakets.
func Diff(prev State, next []PackFile, nextConfigs []string, own []OwnFile) Plan {
	var p Plan
	old := map[string]PackFile{}
	for _, f := range prev.Files {
		old[Key(f)] = f
	}
	ownByKey := map[string]OwnFile{}
	for _, o := range own {
		if o.Key != "" {
			ownByKey[o.Key] = o
		}
	}
	seen := map[string]bool{}
	for _, n := range next {
		k := Key(n)
		seen[k] = true
		o, ok := old[k]
		switch {
		case !ok:
			p.Add = append(p.Add, n)
			if own, isOwn := ownByKey[k]; isOwn {
				p.AbsorbOwn = append(p.AbsorbOwn, own)
			}
		case o.SHA512 != n.SHA512:
			p.Update = append(p.Update, FileUpdate{Old: o, New: n})
		}
	}
	for _, o := range prev.Files {
		if seen[Key(o)] {
			continue
		}
		p.Remove = append(p.Remove, o)
		if contains(prev.Absorbed, Key(o)) {
			name := o.Name
			if name == "" {
				name = o.Filename
			}
			p.DropAbsorbed = append(p.DropAbsorbed, name)
		}
	}
	for _, c := range nextConfigs {
		if _, known := prev.Configs[c]; !known && prev.ProjectID != "" {
			p.NewConfigs = append(p.NewConfigs, c)
		}
	}
	return p
}

// Summary is the one-line Discord/changelog description of a pack change.
func Summary(prev State, title, version string, p Plan, total int) string {
	if prev.VersionNumber == "" {
		return fmt.Sprintf("Basis-Modpack %s %s übernommen (%d Mods)", title, version, total)
	}
	return fmt.Sprintf("%s %s → %s (%d neu, %d entfernt, %d aktualisiert)",
		title, prev.VersionNumber, version, len(p.Add), len(p.Remove), len(p.Update))
}
```

Hinweis zu `NewConfigs`: beim allerersten Übernehmen (`prev.ProjectID == ""`) gibt es keine „neuen" Configs — dort gilt die Vorauswahl `DefaultOn`.

- [ ] **Step 4:** `go test ./internal/basepack/ && go vet ./...` → PASS.
- [ ] **Step 5: Commit** `feat(basepack): Pack-Logik (Index, Vergleich, Config-Vorauswahl)`.

---

### Task 4: Mod-Manager — Dateien gesammelt anwenden, Pack-Dateien anheften

**Files:** Create `internal/mods/files.go`, `internal/mods/files_test.go`; Modify `internal/mods/mods.go`

**Interfaces:**
- Produces:
  - `Entry.Source string` (JSON `source,omitempty`, `"basepack"` für Pack-Dateien), `Entry.Disabled bool` (JSON `disabled,omitempty`) — gesetzt von der API (Task 9).
  - `type FileAdd struct { Category, Filename, SHA512, ProjectID, Name, Version, Replaces string; Data []byte }`
  - `type FileRemove struct { Category, Filename string }`
  - `func (m *Manager) ApplyFiles(profile string, adds []FileAdd, removes []FileRemove, marker string) (string, error)`
  - `func (m *Manager) SetPinned(f func(profile, filename string) bool)`
  - Rollback stoppt an Sätzen mit `.basepack`

- [ ] **Step 1: Failing tests** — `internal/mods/files_test.go`:

```go
package mods

import (
	"crypto/sha512"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func hexsum(b []byte) string { s := sha512.Sum512(b); return hex.EncodeToString(s[:]) }

func TestApplyFilesAddsReplacesRemovesAndSeals(t *testing.T) {
	m, _, dir := testProfile(t)
	os.WriteFile(filepath.Join(dir, "old.jar"), []byte("old"), 0o644)
	os.WriteFile(filepath.Join(dir, "gone.jar"), []byte("g"), 0o644)
	var hooked int
	m.SetChangeHook(func(string, []Change) { hooked++ })

	label, err := m.ApplyFiles(ProfileServer, []FileAdd{
		{Category: "mods", Filename: "new.jar", Data: []byte("new"), SHA512: hexsum([]byte("new")), Name: "New"},
		{Category: "mods", Filename: "old-2.jar", Data: []byte("old2"), SHA512: hexsum([]byte("old2")), Replaces: "old.jar"},
	}, []FileRemove{{Category: "mods", Filename: "gone.jar"}}, "Fabulously Optimized 6.5.0")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"new.jar", "old-2.jar"} {
		if !exists(filepath.Join(dir, f)) {
			t.Errorf("%s fehlt", f)
		}
	}
	if exists(filepath.Join(dir, "old.jar")) || exists(filepath.Join(dir, "gone.jar")) {
		t.Error("ersetzte/entfernte Datei liegt noch im Live-Ordner")
	}
	if !exists(filepath.Join(dir, ".backup", label, "old.jar")) {
		t.Error("alte Datei nicht gesichert")
	}
	if hooked != 0 {
		t.Error("ApplyFiles darf den Change-Hook nicht rufen (Aufrufer meldet selbst)")
	}
	_, err = m.Rollback(ProfileServer)
	if err == nil || !strings.Contains(err.Error(), "Basis-Modpack") || !strings.Contains(err.Error(), "6.5.0") {
		t.Fatalf("Rollback err = %v, want Stopp am Basis-Pack-Satz", err)
	}
	if _, err := m.RollbackLabel(ProfileServer, label); err != nil {
		t.Errorf("gezieltes RollbackLabel muss möglich bleiben: %v", err)
	}
}

func TestApplyFilesRejectsBadHashAndPending(t *testing.T) {
	m, p, _ := testProfile(t)
	if _, err := m.ApplyFiles(ProfileServer, []FileAdd{{Category: "mods", Filename: "x.jar", Data: []byte("x"), SHA512: "falsch"}}, nil, ""); err == nil {
		t.Error("falsche Prüfsumme akzeptiert")
	}
	stage(t, p, map[string]string{"y.jar": "y"}, stagingItem{Category: "mods", NewFilename: "y.jar", Name: "Y"})
	if _, err := m.ApplyFiles(ProfileServer, []FileAdd{{Category: "mods", Filename: "z.jar", Data: []byte("z"), SHA512: hexsum([]byte("z"))}}, nil, ""); err == nil {
		t.Error("ApplyFiles trotz vorgemerkter Änderungen")
	}
}

func TestPinnedFilesGetNoUpdates(t *testing.T) {
	m, _, dir := testProfile(t)
	os.WriteFile(filepath.Join(dir, "pinned.jar"), []byte("p"), 0o644)
	m.SetPinned(func(profile, filename string) bool { return filename == "pinned.jar" })
	if !m.pinned(ProfileServer, "pinned.jar") || m.pinned(ProfileServer, "other.jar") {
		t.Error("pinned-Abfrage falsch")
	}
}
```

(`testProfile`, `stage`, `exists` sind Helfer aus `staging_test.go`.)

- [ ] **Step 2:** `go test ./internal/mods/` → FAIL.

- [ ] **Step 3: Implementierung.**

In `internal/mods/mods.go`:
- `Entry` am Ende ergänzen:

```go
	// Source: "basepack" für Dateien aus dem Basis-Modpack (setzt die API)
	Source string `json:"source,omitempty"`
	// Disabled: abgeschaltete Basis-Pack-Mod (liegt nicht im Paket)
	Disabled bool `json:"disabled,omitempty"`
```

- `Manager` um `pinnedFn func(profile, filename string) bool` ergänzen.
- In `CheckUpdates` die Bedingung `if up, ok := latest[e.SHA512]; ok && e.Managed {` ersetzen durch `if up, ok := latest[e.SHA512]; ok && e.Managed && !m.pinned(profileName, e.Filename) {` (Pack-Mods werden nur mit dem Pack aktualisiert).
- In `Rollback` direkt nach der bestehenden `sealFile`-Prüfschleife (MC-Update) einfügen:

```go
	for _, liveDir := range p.Dirs {
		if v, err := os.ReadFile(filepath.Join(liveDir, ".backup", label, basepackFile)); err == nil {
			return 0, fmt.Errorf("der letzte Satz gehört zum Basis-Modpack (%s) — Änderungen am Basis-Pack bitte im Bereich „Basis-Modpack“ vornehmen (andere Version übernehmen oder Mod wieder anschalten)", strings.TrimSpace(string(v)))
		}
	}
```

`internal/mods/files.go`:

```go
package mods

import (
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// basepackFile marks a backup set created for the base modpack; der normale
// Rollback stoppt dort (Basis-Pack-Stand und Dateien blieben sonst nicht
// deckungsgleich).
const basepackFile = ".basepack"

// FileAdd is one verified file to place into a profile.
type FileAdd struct {
	Category  string
	Filename  string
	SHA512    string
	ProjectID string
	Name      string
	Version   string
	// Replaces: Datei derselben Kategorie, die dabei ersetzt wird ("" = neu)
	Replaces string
	Data     []byte
}

// FileRemove is one file to take out of a profile (wandert in die Sicherung).
type FileRemove struct {
	Category string
	Filename string
}

// SetPinned registers a predicate for files that must not get individual
// updates (Basis-Pack-Dateien werden nur mit dem Pack aktualisiert).
func (m *Manager) SetPinned(f func(profile, filename string) bool) { m.pinnedFn = f }

func (m *Manager) pinned(profile, filename string) bool {
	return m.pinnedFn != nil && m.pinnedFn(profile, filename)
}

// ApplyFiles places and removes files as ONE backup set (über die
// Staging-Mechanik, Sicherung + RollbackLabel möglich) — ohne Change-Hook:
// der Aufrufer meldet selbst (Basis-Pack: eine Zeile statt vieler).
// marker != "" markiert den Satz als Basis-Pack-Satz.
func (m *Manager) ApplyFiles(profile string, adds []FileAdd, removes []FileRemove, marker string) (string, error) {
	p, err := m.profile(profile)
	if err != nil {
		return "", err
	}
	for _, a := range adds {
		if !validName(a.Filename) || (a.Replaces != "" && !validName(a.Replaces)) {
			return "", fmt.Errorf("ungültiger Dateiname %q", a.Filename)
		}
		if _, ok := p.Dirs[a.Category]; !ok {
			return "", fmt.Errorf("unbekannte Kategorie %q", a.Category)
		}
		sum := sha512.Sum512(a.Data)
		if hex.EncodeToString(sum[:]) != a.SHA512 {
			return "", fmt.Errorf("hash-verifikation fehlgeschlagen für %s — abgebrochen", a.Filename)
		}
	}
	for _, r := range removes {
		if !validName(r.Filename) {
			return "", fmt.Errorf("ungültiger Dateiname %q", r.Filename)
		}
	}
	m.stageMu.Lock()
	defer m.stageMu.Unlock()
	if len(m.stagedItems(p)) > 0 {
		return "", fmt.Errorf("im Profil %s sind noch Änderungen vorgemerkt — erst anwenden oder verwerfen", profile)
	}
	sdir := stagingDir(p)
	if err := os.MkdirAll(sdir, 0o755); err != nil {
		return "", err
	}
	man := stagingManifest{Created: time.Now()}
	for _, r := range removes {
		man.Items = append(man.Items, stagingItem{Category: r.Category, OldFilename: r.Filename})
	}
	for _, a := range adds {
		if err := os.WriteFile(filepath.Join(sdir, a.Filename), a.Data, 0o644); err != nil {
			return "", err
		}
		man.Items = append(man.Items, stagingItem{Category: a.Category, OldFilename: a.Replaces, NewFilename: a.Filename,
			SHA512: a.SHA512, Version: a.Version, ProjectID: a.ProjectID, Name: a.Name})
	}
	if len(man.Items) == 0 {
		return "", nil
	}
	if err := writeManifest(filepath.Join(sdir, "manifest.json"), man); err != nil {
		return "", err
	}
	label, _, _, err := m.applyStagedLocked(profile)
	if err != nil {
		return label, err
	}
	if marker != "" {
		for _, liveDir := range p.Dirs {
			set := filepath.Join(liveDir, ".backup", label)
			if _, err := os.Stat(set); err == nil {
				os.WriteFile(filepath.Join(set, basepackFile), []byte(marker+"\n"), 0o644)
			}
		}
	}
	return label, nil
}
```

`validName` existiert in `add.go`.

- [ ] **Step 4:** `go test ./internal/mods/ && go vet ./...` → PASS.
- [ ] **Step 5: Commit** `feat(mods): Dateien gesammelt anwenden, Basis-Pack-Dateien anheften`.

---

### Task 5: Basis-Pack-Service

**Files:** Create `internal/basepack/service.go`, `internal/basepack/service_test.go`

**Interfaces:**
- Consumes: Task 1 (`mrpack.Parse`), Task 3 (alles aus `pack.go`), Task 4 (`mods.FileAdd`, `mods.FileRemove`, `mods.Entry.Source`), `modrinth.Version`, `modrinth.Project`, `modrinth.SearchHit`.
- Produces:
  - `type API interface { SearchModpacks(ctx, query, loader, mcVersion string, limit int) ([]modrinth.SearchHit, error); Project(ctx, id string) (modrinth.Project, error); ProjectVersions(ctx, projectID, loader, mcVersion string) ([]modrinth.Version, error); VersionsByHash(ctx, sha512s []string) (map[string]modrinth.Version, error); Projects(ctx, ids []string) ([]modrinth.Project, error); Download(ctx, url string) ([]byte, error) }`
  - `type Files interface { ApplyFiles(profile string, adds []mods.FileAdd, removes []mods.FileRemove, marker string) (string, error); CheckUpdates(ctx context.Context, profile, mcVersion string) ([]mods.Entry, error) }`
  - `func New(api API, files Files, root string, get func(string) string, set func(string, string), notify func([]mods.Change), log *slog.Logger) *Service` — `root` = Client-Paket-Wurzel (Ordner mit `mods/`, `config/` …)
  - `type ConfigChoice struct { Path string; On, New bool }` (JSON `path, on, new`)
  - `type Preview struct { ProjectID, Slug, Title, VersionID, VersionNumber, MC string; Total int; Plan Plan; Configs []ConfigChoice; Ignored []string }`
  - Methoden: `State() (State, bool)`, `Search(ctx, query, mc string) ([]modrinth.SearchHit, error)`, `Preview(ctx, projectID, mc string) (*Preview, error)`, `Apply(ctx, projectID, versionID, mc string, configs map[string]bool) (string, error)`, `Toggle(ctx, key string, on bool) error`, `SetConfigs(ctx, configs map[string]bool) error`, `Remove(ctx) error`, `Available(ctx, mc string) (*modrinth.Version, error)`, `Ready(ctx, mc string) (title string, ready, set bool)`, `SwitchMC(ctx, mc string) error`, `Pinned(filename string) bool`, `IndexFiles() map[string]PackFile` (sha512 → Datei), `WrittenConfigs() []string`, `Root() string`

- [ ] **Step 1: Failing tests** — `internal/basepack/service_test.go`:

```go
package basepack

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/modrinth"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mods"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mrpack"
)

func sum(b []byte) string { s := sha512.Sum512(b); return hex.EncodeToString(s[:]) }

// fakeAPI liefert Pack-Versionen als echte .mrpack-Bytes.
type fakeAPI struct {
	versions map[string][]modrinth.Version // mc → neueste zuerst
	packs    map[string][]byte             // version id → .mrpack
	blobs    map[string][]byte             // url → Datei
	byHash   map[string]modrinth.Version
}

func (f *fakeAPI) SearchModpacks(context.Context, string, string, string, int) ([]modrinth.SearchHit, error) {
	return []modrinth.SearchHit{{ProjectID: "FO", Title: "Fabulously Optimized"}}, nil
}
func (f *fakeAPI) Project(context.Context, string) (modrinth.Project, error) {
	return modrinth.Project{ID: "FO", Slug: "fabulously-optimized", Title: "Fabulously Optimized", ProjectType: "modpack"}, nil
}
func (f *fakeAPI) ProjectVersions(_ context.Context, _, _, mc string) ([]modrinth.Version, error) {
	return f.versions[mc], nil
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
func (f *fakeAPI) Projects(_ context.Context, ids []string) ([]modrinth.Project, error) {
	var out []modrinth.Project
	for _, id := range ids {
		out = append(out, modrinth.Project{ID: id, Title: "T-" + id})
	}
	return out, nil
}
func (f *fakeAPI) Download(_ context.Context, url string) ([]byte, error) {
	if b, ok := f.packs[url]; ok {
		return b, nil
	}
	return f.blobs[url], nil
}

// addPackVersion baut eine .mrpack mit mods (name → inhalt) und configs.
func (f *fakeAPI) addPackVersion(t *testing.T, mc, id string, mods map[string]string, configs map[string]string) {
	t.Helper()
	var files []mrpack.File
	for name, body := range mods {
		url := "https://cdn.modrinth.com/" + name
		f.blobs[url] = []byte(body)
		files = append(files, mrpack.File{Path: "mods/" + name, SHA1: "x", SHA512: sum([]byte(body)), URL: url, Size: int64(len(body))})
		f.byHash[sum([]byte(body))] = modrinth.Version{ProjectID: "P-" + strings.Split(name, "-")[0], VersionNumber: body}
	}
	ov := map[string][]byte{}
	for p, b := range configs {
		ov[p] = []byte(b)
	}
	data, err := mrpack.Build(files, mrpack.Meta{VersionID: id, Name: "FO", Minecraft: mc, FabricLoader: "0.20.1", Overrides: ov})
	if err != nil {
		t.Fatal(err)
	}
	packURL := "https://cdn.modrinth.com/pack-" + id + ".mrpack"
	f.packs[packURL] = data
	v := modrinth.Version{ID: id, ProjectID: "FO", VersionNumber: id}
	file := modrinth.File{URL: packURL, Filename: "fo-" + id + ".mrpack", Primary: true}
	v.Files = []modrinth.File{file}
	f.versions[mc] = append([]modrinth.Version{v}, f.versions[mc]...)
}

type fakeFiles struct {
	root    string
	calls   int
	markers []string
	own     []mods.Entry
}

func (f *fakeFiles) ApplyFiles(_ string, adds []mods.FileAdd, removes []mods.FileRemove, marker string) (string, error) {
	f.calls++
	f.markers = append(f.markers, marker)
	for _, r := range removes {
		os.Remove(filepath.Join(f.root, r.Category, r.Filename))
	}
	for _, a := range adds {
		if a.Replaces != "" {
			os.Remove(filepath.Join(f.root, a.Category, a.Replaces))
		}
		os.WriteFile(filepath.Join(f.root, a.Category, a.Filename), a.Data, 0o644)
	}
	return "L", nil
}
func (f *fakeFiles) CheckUpdates(context.Context, string, string) ([]mods.Entry, error) { return f.own, nil }

func setup(t *testing.T) (*Service, *fakeAPI, *fakeFiles, map[string]string, *[][]mods.Change) {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"mods", "resourcepacks", "shaderpacks"} {
		os.MkdirAll(filepath.Join(root, d), 0o755)
	}
	api := &fakeAPI{versions: map[string][]modrinth.Version{}, packs: map[string][]byte{}, blobs: map[string][]byte{}, byHash: map[string]modrinth.Version{}}
	files := &fakeFiles{root: root}
	kv := map[string]string{}
	var notes [][]mods.Change
	s := New(api, files, root, func(k string) string { return kv[k] }, func(k, v string) { kv[k] = v },
		func(c []mods.Change) { notes = append(notes, c) }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return s, api, files, kv, &notes
}

func TestApplyFirstVersionWithConfigsAndAbsorb(t *testing.T) {
	s, api, files, _, notes := setup(t)
	api.addPackVersion(t, "26.3", "6.4.0", map[string]string{"sodium-1.jar": "s1", "lithium-1.jar": "l1"},
		map[string]string{"config/a.json": "A", "config/sodium-options.json": "S", "options.txt": "O"})
	files.own = []mods.Entry{{Category: "mods", Filename: "sodium-own.jar", Managed: true, ProjectID: "P-sodium", Name: "Sodium"}}
	os.WriteFile(filepath.Join(files.root, "mods", "sodium-own.jar"), []byte("own"), 0o644)

	pv, err := s.Preview(context.Background(), "FO", "26.3")
	if err != nil {
		t.Fatal(err)
	}
	if pv.VersionID != "6.4.0" || len(pv.Plan.Add) != 2 || len(pv.Plan.AbsorbOwn) != 1 {
		t.Fatalf("preview = %+v", pv)
	}
	on := map[string]bool{}
	for _, c := range pv.Configs {
		on[c.Path] = c.On
	}
	if !on["config/a.json"] || on["config/sodium-options.json"] || on["options.txt"] {
		t.Errorf("vorauswahl = %v", on)
	}
	summary, err := s.Apply(context.Background(), "FO", "6.4.0", "26.3", on)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "6.4.0") || files.markers[0] == "" {
		t.Errorf("summary = %q markers = %v", summary, files.markers)
	}
	if b, _ := os.ReadFile(filepath.Join(files.root, "config", "a.json")); string(b) != "A" {
		t.Error("freigegebene Config nicht geschrieben")
	}
	if _, err := os.Stat(filepath.Join(files.root, "options.txt")); err == nil {
		t.Error("nicht freigegebene Config geschrieben")
	}
	if _, err := os.Stat(filepath.Join(files.root, "mods", "sodium-own.jar")); err == nil {
		t.Error("eigene Sodium-Kopie hätte wegfallen müssen")
	}
	st, ok := s.State()
	if !ok || st.VersionNumber != "6.4.0" || len(st.Absorbed) != 1 || !s.Pinned("sodium-1.jar") || s.Pinned("fremd.jar") {
		t.Errorf("state = %+v", st)
	}
	if len(*notes) != 1 || (*notes)[0][0].Action != "pack" {
		t.Errorf("notes = %+v", *notes)
	}
}

func TestUpdateKeepsDisabledAndFollowsPack(t *testing.T) {
	s, api, files, _, _ := setup(t)
	api.addPackVersion(t, "26.3", "6.4.0", map[string]string{"sodium-1.jar": "s1", "lithium-1.jar": "l1", "old-1.jar": "o1"}, nil)
	if _, err := s.Apply(context.Background(), "FO", "6.4.0", "26.3", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Toggle(context.Background(), "P-lithium", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(files.root, "mods", "lithium-1.jar")); err == nil {
		t.Fatal("abgeschaltete Mod liegt noch im Paket")
	}
	api.addPackVersion(t, "26.3", "6.5.0", map[string]string{"sodium-2.jar": "s2", "lithium-2.jar": "l2", "new-1.jar": "n1"}, nil)
	av, err := s.Available(context.Background(), "26.3")
	if err != nil || av == nil || av.ID != "6.5.0" {
		t.Fatalf("available = %+v, %v", av, err)
	}
	if _, err := s.Apply(context.Background(), "FO", "6.5.0", "26.3", nil); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{"sodium-2.jar": true, "new-1.jar": true, "lithium-2.jar": false, "old-1.jar": false, "sodium-1.jar": false} {
		_, err := os.Stat(filepath.Join(files.root, "mods", name))
		if (err == nil) != want {
			t.Errorf("%s vorhanden = %v, want %v", name, err == nil, want)
		}
	}
	st, _ := s.State()
	if !st.IsDisabled("P-lithium") {
		t.Error("Abschaltung nach Update verloren")
	}
	if err := s.Toggle(context.Background(), "P-lithium", true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(files.root, "mods", "lithium-2.jar")); string(b) != "l2" {
		t.Error("Anschalten hat die aktuelle Pack-Datei nicht geladen")
	}
}

func TestReadySwitchAndRemove(t *testing.T) {
	s, api, files, _, _ := setup(t)
	if _, _, set := s.Ready(context.Background(), "26.4"); set {
		t.Error("ohne Basis-Pack darf Ready nicht gesetzt melden")
	}
	api.addPackVersion(t, "26.3", "6.4.0", map[string]string{"sodium-1.jar": "s1"}, map[string]string{"config/a.json": "A"})
	if _, err := s.Apply(context.Background(), "FO", "6.4.0", "26.3", map[string]bool{"config/a.json": true}); err != nil {
		t.Fatal(err)
	}
	if _, ready, set := s.Ready(context.Background(), "26.4"); !set || ready {
		t.Error("26.4 gibt es noch nicht — ready muss false sein")
	}
	api.addPackVersion(t, "26.4", "7.0.0", map[string]string{"sodium-3.jar": "s3"}, map[string]string{"config/a.json": "A2"})
	if err := s.SwitchMC(context.Background(), "26.4"); err != nil {
		t.Fatal(err)
	}
	st, _ := s.State()
	if st.MC != "26.4" || st.VersionNumber != "7.0.0" {
		t.Errorf("state nach Umstellung = %+v", st)
	}
	if b, _ := os.ReadFile(filepath.Join(files.root, "config", "a.json")); string(b) != "A2" {
		t.Error("Config-Auswahl nicht übernommen")
	}
	if err := s.Remove(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.State(); ok {
		t.Error("Stand nach Entfernen noch da")
	}
	if _, err := os.Stat(filepath.Join(files.root, "config", "a.json")); err == nil {
		t.Error("Config nach Entfernen noch da")
	}
	if _, err := os.Stat(filepath.Join(files.root, "mods", "sodium-3.jar")); err == nil {
		t.Error("Pack-Mod nach Entfernen noch da")
	}
}
```

- [ ] **Step 2:** `go test ./internal/basepack/` → FAIL.

- [ ] **Step 3: Implementierung** — `internal/basepack/service.go`:

```go
package basepack

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/modrinth"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mods"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mrpack"
)

const (
	stateKey = "basepack.state"
	loader   = "fabric"
	maxPack  = 64 << 20
)

// API is the Modrinth surface the service needs (*modrinth.Client).
type API interface {
	SearchModpacks(ctx context.Context, query, loader, mcVersion string, limit int) ([]modrinth.SearchHit, error)
	Project(ctx context.Context, id string) (modrinth.Project, error)
	ProjectVersions(ctx context.Context, projectID, loader, mcVersion string) ([]modrinth.Version, error)
	VersionsByHash(ctx context.Context, sha512s []string) (map[string]modrinth.Version, error)
	Projects(ctx context.Context, ids []string) ([]modrinth.Project, error)
	Download(ctx context.Context, url string) ([]byte, error)
}

// Files is the mod-manager surface (*mods.Manager).
type Files interface {
	ApplyFiles(profile string, adds []mods.FileAdd, removes []mods.FileRemove, marker string) (string, error)
	CheckUpdates(ctx context.Context, profile, mcVersion string) ([]mods.Entry, error)
}

// ConfigChoice is one config file in the preview.
type ConfigChoice struct {
	Path string `json:"path"`
	On   bool   `json:"on"`
	New  bool   `json:"new"`
}

// Preview describes what applying a pack version would do.
type Preview struct {
	ProjectID     string         `json:"projectId"`
	Slug          string         `json:"slug"`
	Title         string         `json:"title"`
	VersionID     string         `json:"versionId"`
	VersionNumber string         `json:"versionNumber"`
	MC            string         `json:"mc"`
	Total         int            `json:"total"`
	Plan          Plan           `json:"plan"`
	Configs       []ConfigChoice `json:"configs"`
	Ignored       []string       `json:"ignored"`
}

type Service struct {
	api    API
	files  Files
	root   string
	get    func(string) string
	set    func(string, string)
	notify func([]mods.Change)
	log    *slog.Logger
	mu     sync.Mutex // serialisiert alle Änderungen
}

func New(api API, files Files, root string, get func(string) string, set func(string, string), notify func([]mods.Change), log *slog.Logger) *Service {
	return &Service{api: api, files: files, root: root, get: get, set: set, notify: notify, log: log}
}

// Root is the client pack root dir (für Verteilung der Configs).
func (s *Service) Root() string { return s.root }

// State returns the persisted state; ok=false = kein Basis-Pack gesetzt.
func (s *Service) State() (State, bool) {
	var st State
	raw := s.get(stateKey)
	if raw == "" || json.Unmarshal([]byte(raw), &st) != nil || st.ProjectID == "" {
		return State{}, false
	}
	return st, true
}

func (s *Service) save(st *State) {
	if st == nil {
		s.set(stateKey, "")
		return
	}
	b, _ := json.Marshal(st)
	s.set(stateKey, string(b))
}

func (s *Service) Search(ctx context.Context, query, mc string) ([]modrinth.SearchHit, error) {
	return s.api.SearchModpacks(ctx, query, loader, mc, 15)
}

// loaded is a downloaded + resolved pack version.
type loaded struct {
	project modrinth.Project
	version modrinth.Version
	files   []PackFile
	configs []string
	ignored []string
	over    map[string][]byte
}

func (s *Service) versionFor(ctx context.Context, projectID, versionID, mc string) (modrinth.Version, error) {
	vs, err := s.api.ProjectVersions(ctx, projectID, loader, mc)
	if err != nil {
		return modrinth.Version{}, err
	}
	if len(vs) == 0 {
		return modrinth.Version{}, fmt.Errorf("dieses Modpack hat keine Version für Minecraft %s", mc)
	}
	if versionID == "" {
		return vs[0], nil
	}
	for _, v := range vs {
		if v.ID == versionID {
			return v, nil
		}
	}
	return modrinth.Version{}, fmt.Errorf("Pack-Version %s passt nicht zu Minecraft %s", versionID, mc)
}

func (s *Service) load(ctx context.Context, projectID, versionID, mc string) (*loaded, error) {
	proj, err := s.api.Project(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if proj.ProjectType != "modpack" {
		return nil, fmt.Errorf("„%s“ ist kein Modpack", proj.Title)
	}
	v, err := s.versionFor(ctx, projectID, versionID, mc)
	if err != nil {
		return nil, err
	}
	file, ok := v.PrimaryFile()
	if !ok || !strings.HasSuffix(file.Filename, ".mrpack") {
		return nil, fmt.Errorf("Pack-Version %s enthält keine .mrpack-Datei", v.VersionNumber)
	}
	if file.Size > maxPack {
		return nil, fmt.Errorf("Modpack-Datei zu groß (%d MB, höchstens 64 MB)", file.Size>>20)
	}
	data, err := s.api.Download(ctx, file.URL)
	if err != nil {
		return nil, fmt.Errorf("modpack laden: %w", err)
	}
	if len(data) > maxPack {
		return nil, fmt.Errorf("Modpack-Datei zu groß (höchstens 64 MB)")
	}
	idx, err := mrpack.Parse(data)
	if err != nil {
		return nil, err
	}
	files, configs, ignored := FromIndex(idx)
	hashes := make([]string, 0, len(files))
	for _, f := range files {
		hashes = append(hashes, f.SHA512)
	}
	if known, err := s.api.VersionsByHash(ctx, hashes); err == nil {
		var ids []string
		for i := range files {
			if kv, ok := known[files[i].SHA512]; ok {
				files[i].ProjectID = kv.ProjectID
				files[i].Version = kv.VersionNumber
				ids = append(ids, kv.ProjectID)
			}
		}
		if ps, err := s.api.Projects(ctx, ids); err == nil {
			titles := map[string]string{}
			for _, p := range ps {
				titles[p.ID] = p.Title
			}
			for i := range files {
				files[i].Name = titles[files[i].ProjectID]
			}
		}
	}
	for i := range files {
		if files[i].Name == "" {
			files[i].Name = files[i].Filename
		}
	}
	return &loaded{project: proj, version: v, files: files, configs: configs, ignored: ignored, over: idx.Overrides}, nil
}

// own lists the admin's own files (ohne Pack-Dateien).
func (s *Service) own(ctx context.Context, mc string, prev State) []OwnFile {
	entries, err := s.files.CheckUpdates(ctx, mods.ProfileClient, mc)
	if err != nil {
		return nil
	}
	pack := map[string]bool{}
	for _, f := range prev.Files {
		pack[f.Filename] = true
	}
	var out []OwnFile
	for _, e := range entries {
		if pack[e.Filename] || e.ProjectID == "" {
			continue
		}
		out = append(out, OwnFile{Category: e.Category, Filename: e.Filename, Key: e.ProjectID, Name: e.Name})
	}
	return out
}

func choices(prev State, configs []string, plan Plan) []ConfigChoice {
	isNew := map[string]bool{}
	for _, c := range plan.NewConfigs {
		isNew[c] = true
	}
	out := make([]ConfigChoice, 0, len(configs))
	for _, c := range configs {
		on, known := prev.Configs[c]
		switch {
		case isNew[c]:
			on = false
		case !known:
			on = DefaultOn(c)
		}
		out = append(out, ConfigChoice{Path: c, On: on, New: isNew[c]})
	}
	return out
}

// Preview loads the newest pack version for mc and describes the change.
func (s *Service) Preview(ctx context.Context, projectID, mc string) (*Preview, error) {
	prev, _ := s.State()
	l, err := s.load(ctx, projectID, "", mc)
	if err != nil {
		return nil, err
	}
	if prev.ProjectID != "" && prev.ProjectID != l.project.ID {
		prev = State{} // anderes Pack: alles neu, die alten Pack-Dateien gehen raus (Apply)
	}
	plan := Diff(prev, l.files, l.configs, s.own(ctx, mc, prev))
	return &Preview{ProjectID: l.project.ID, Slug: l.project.Slug, Title: l.project.Title,
		VersionID: l.version.ID, VersionNumber: l.version.VersionNumber, MC: mc, Total: len(l.files),
		Plan: plan, Configs: choices(prev, l.configs, plan), Ignored: l.ignored}, nil
}

func (s *Service) download(ctx context.Context, f PackFile) ([]byte, error) {
	b, err := s.api.Download(ctx, f.URL)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", f.Filename, err)
	}
	sum := sha512.Sum512(b)
	if hex.EncodeToString(sum[:]) != f.SHA512 {
		return nil, fmt.Errorf("hash-verifikation fehlgeschlagen für %s — abgebrochen", f.Filename)
	}
	return b, nil
}

func addOf(f PackFile, data []byte, replaces string) mods.FileAdd {
	return mods.FileAdd{Category: f.Category, Filename: f.Filename, SHA512: f.SHA512, ProjectID: f.ProjectID,
		Name: f.Name, Version: f.Version, Replaces: replaces, Data: data}
}

// Apply switches to the given pack version (oder übernimmt es erstmals).
// configs: Auswahl je Pfad (nil = bisherige Auswahl + Vorauswahl).
func (s *Service) Apply(ctx context.Context, projectID, versionID, mc string, configs map[string]bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyLocked(ctx, projectID, versionID, mc, configs)
}

func (s *Service) applyLocked(ctx context.Context, projectID, versionID, mc string, configs map[string]bool) (string, error) {
	prev, _ := s.State()
	l, err := s.load(ctx, projectID, versionID, mc)
	if err != nil {
		return "", err
	}
	switching := prev.ProjectID != "" && prev.ProjectID != l.project.ID
	base := prev
	if switching {
		base = State{}
	}
	plan := Diff(base, l.files, l.configs, s.own(ctx, mc, prev))

	var adds []mods.FileAdd
	var removes []mods.FileRemove
	absorbByCat := map[string]OwnFile{}
	for _, o := range plan.AbsorbOwn {
		absorbByCat[o.Key] = o
	}
	for _, f := range plan.Add {
		if base.IsDisabled(Key(f)) {
			continue
		}
		data, err := s.download(ctx, f)
		if err != nil {
			return "", err
		}
		replaces := ""
		if o, ok := absorbByCat[Key(f)]; ok {
			if o.Category == f.Category {
				replaces = o.Filename
			} else {
				removes = append(removes, mods.FileRemove{Category: o.Category, Filename: o.Filename})
			}
		}
		adds = append(adds, addOf(f, data, replaces))
	}
	for _, u := range plan.Update {
		if base.IsDisabled(Key(u.New)) {
			continue
		}
		data, err := s.download(ctx, u.New)
		if err != nil {
			return "", err
		}
		if u.Old.Category == u.New.Category {
			adds = append(adds, addOf(u.New, data, u.Old.Filename))
		} else {
			removes = append(removes, mods.FileRemove{Category: u.Old.Category, Filename: u.Old.Filename})
			adds = append(adds, addOf(u.New, data, ""))
		}
	}
	removeList := plan.Remove
	if switching {
		removeList = prev.Files // anderes Pack: alle alten Pack-Dateien raus
	}
	for _, f := range removeList {
		if !prev.IsDisabled(Key(f)) {
			removes = append(removes, mods.FileRemove{Category: f.Category, Filename: f.Filename})
		}
	}

	summary := Summary(prev, l.project.Title, l.version.VersionNumber, plan, len(l.files))
	if switching {
		summary = fmt.Sprintf("Basis-Modpack gewechselt: %s → %s %s", prev.Title, l.project.Title, l.version.VersionNumber)
	}
	if _, err := s.files.ApplyFiles(mods.ProfileClient, adds, removes, l.project.Title+" "+l.version.VersionNumber); err != nil {
		return "", err
	}

	next := State{ProjectID: l.project.ID, Slug: l.project.Slug, Title: l.project.Title,
		VersionID: l.version.ID, VersionNumber: l.version.VersionNumber, MC: mc, Files: l.files,
		Configs: map[string]bool{}}
	keys := map[string]bool{}
	for _, f := range l.files {
		keys[Key(f)] = true
	}
	for _, d := range base.Disabled {
		if keys[d] {
			next.Disabled = append(next.Disabled, d)
		}
	}
	for _, a := range base.Absorbed {
		if keys[a] {
			next.Absorbed = append(next.Absorbed, a)
		}
	}
	for _, o := range plan.AbsorbOwn {
		next.Absorbed = append(next.Absorbed, o.Key)
	}
	for _, c := range choices(base, l.configs, plan) {
		on := c.On
		if v, ok := configs[c.Path]; ok {
			on = v
		}
		next.Configs[c.Path] = on
	}
	next.WrittenConfigs = s.writeConfigs(prev.WrittenConfigs, next.Configs, l.over)
	s.save(&next)

	lines := []mods.Change{{Action: "pack", Profile: mods.ProfileClient, Name: summary}}
	for _, name := range plan.DropAbsorbed {
		lines = append(lines, mods.Change{Action: "pack", Profile: mods.ProfileClient,
			Name: name + " entfällt — war vorher auch deine eigene Mod, bei Bedarf neu hinzufügen"})
	}
	if s.notify != nil {
		s.notify(lines)
	}
	s.log.Info("basis-modpack übernommen", "pack", l.project.Title, "version", l.version.VersionNumber, "mc", mc)
	return summary, nil
}

// writeConfigs writes the selected configs and removes previously written
// ones that are no longer selected or no longer in the pack.
func (s *Service) writeConfigs(old []string, sel map[string]bool, over map[string][]byte) []string {
	var written []string
	keep := map[string]bool{}
	paths := make([]string, 0, len(sel))
	for p := range sel {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		data, ok := over[p]
		if !sel[p] || !ok || !mrpack.ValidPath(p) {
			continue
		}
		dst := filepath.Join(s.root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			s.log.Warn("config schreiben", "path", p, "err", err)
			continue
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			s.log.Warn("config schreiben", "path", p, "err", err)
			continue
		}
		keep[p] = true
		written = append(written, p)
	}
	for _, p := range old {
		if !keep[p] && mrpack.ValidPath(p) {
			os.Remove(filepath.Join(s.root, filepath.FromSlash(p)))
		}
	}
	return written
}

func (s *Service) find(st State, key string) (PackFile, bool) {
	for _, f := range st.Files {
		if Key(f) == key {
			return f, true
		}
	}
	return PackFile{}, false
}

// Toggle switches one pack file on or off.
func (s *Service) Toggle(ctx context.Context, key string, on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.State()
	if !ok {
		return fmt.Errorf("kein Basis-Modpack eingerichtet")
	}
	f, ok := s.find(st, key)
	if !ok {
		return fmt.Errorf("unbekannte Pack-Mod %q", key)
	}
	if st.IsDisabled(key) == !on {
		return nil
	}
	marker := st.Title + " " + st.VersionNumber
	change := mods.Change{Profile: mods.ProfileClient, Filename: f.Filename, ProjectID: f.ProjectID, Name: f.Name}
	if on {
		data, err := s.download(ctx, f)
		if err != nil {
			return err
		}
		if _, err := s.files.ApplyFiles(mods.ProfileClient, []mods.FileAdd{addOf(f, data, "")}, nil, marker); err != nil {
			return err
		}
		var rest []string
		for _, d := range st.Disabled {
			if d != key {
				rest = append(rest, d)
			}
		}
		st.Disabled = rest
		change.Action = "add"
	} else {
		if _, err := s.files.ApplyFiles(mods.ProfileClient, nil, []mods.FileRemove{{Category: f.Category, Filename: f.Filename}}, marker); err != nil {
			return err
		}
		st.Disabled = append(st.Disabled, key)
		change.Action = "remove"
	}
	s.save(&st)
	if s.notify != nil {
		s.notify([]mods.Change{change})
	}
	return nil
}

// SetConfigs changes the config selection (lädt das Pack für die Inhalte neu).
func (s *Service) SetConfigs(ctx context.Context, configs map[string]bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.State()
	if !ok {
		return fmt.Errorf("kein Basis-Modpack eingerichtet")
	}
	l, err := s.load(ctx, st.ProjectID, st.VersionID, st.MC)
	if err != nil {
		return err
	}
	for p, on := range configs {
		if _, known := st.Configs[p]; known {
			st.Configs[p] = on
		}
	}
	st.WrittenConfigs = s.writeConfigs(st.WrittenConfigs, st.Configs, l.over)
	s.save(&st)
	if s.notify != nil {
		s.notify([]mods.Change{{Action: "pack", Profile: mods.ProfileClient, Name: "Einstellungen des Basis-Modpacks " + st.Title + " geändert"}})
	}
	return nil
}

// Remove takes the base pack out of the client pack (eigene Mods bleiben).
func (s *Service) Remove(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.State()
	if !ok {
		return nil
	}
	var removes []mods.FileRemove
	for _, f := range st.Files {
		if !st.IsDisabled(Key(f)) {
			removes = append(removes, mods.FileRemove{Category: f.Category, Filename: f.Filename})
		}
	}
	if _, err := s.files.ApplyFiles(mods.ProfileClient, nil, removes, st.Title+" entfernt"); err != nil {
		return err
	}
	s.writeConfigs(st.WrittenConfigs, nil, nil)
	s.save(nil)
	if s.notify != nil {
		s.notify([]mods.Change{{Action: "pack", Profile: mods.ProfileClient, Name: "Basis-Modpack " + st.Title + " entfernt"}})
	}
	return nil
}

// Available returns a newer pack version for mc (nil = aktuell / kein Pack).
func (s *Service) Available(ctx context.Context, mc string) (*modrinth.Version, error) {
	st, ok := s.State()
	if !ok {
		return nil, nil
	}
	vs, err := s.api.ProjectVersions(ctx, st.ProjectID, loader, mc)
	if err != nil {
		return nil, err
	}
	if len(vs) == 0 || vs[0].ID == st.VersionID {
		return nil, nil
	}
	return &vs[0], nil
}

// Ready reports whether the base pack has a version for mc.
// set=false: kein Basis-Pack eingerichtet.
func (s *Service) Ready(ctx context.Context, mc string) (string, bool, bool) {
	st, ok := s.State()
	if !ok {
		return "", true, false
	}
	vs, err := s.api.ProjectVersions(ctx, st.ProjectID, loader, mc)
	return st.Title, err == nil && len(vs) > 0, true
}

// SwitchMC moves the pack to its newest version for mc (MC-Update).
func (s *Service) SwitchMC(ctx context.Context, mc string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.State()
	if !ok || st.MC == mc {
		return nil
	}
	_, err := s.applyLocked(ctx, st.ProjectID, "", mc, st.Configs)
	return err
}

// Pinned reports whether filename is an active pack file.
func (s *Service) Pinned(filename string) bool {
	st, ok := s.State()
	if !ok {
		return false
	}
	for _, f := range st.Files {
		if f.Filename == filename {
			return true
		}
	}
	return false
}

// IndexFiles maps sha512 → pack file (für das eigene Modrinth-Modpack).
func (s *Service) IndexFiles() map[string]PackFile {
	out := map[string]PackFile{}
	if st, ok := s.State(); ok {
		for _, f := range st.Files {
			out[f.SHA512] = f
		}
	}
	return out
}

// WrittenConfigs returns the config paths MSM wrote into the client pack.
func (s *Service) WrittenConfigs() []string {
	st, _ := s.State()
	return append([]string(nil), st.WrittenConfigs...)
}
```

Hinweis: In `Apply` über `applyLocked` beim Wechsel `prev → nil Configs` greift die Vorauswahl über `choices`. `Toggle` lädt beim Anschalten die Datei aus dem gespeicherten Stand (URL + Prüfsumme der aktuellen Pack-Version).

- [ ] **Step 4:** `go test ./internal/basepack/ && go vet ./...` → PASS.
- [ ] **Step 5: Commit** `feat(basepack): Service (Übernehmen, An/Aus, Configs, Updates, MC-Umstellung)`.

---

### Task 6: Mod-Neuigkeiten — Pack-Zeile

**Files:** Modify `internal/modnews/modnews.go`; Test `internal/modnews/modnews_test.go`

- [ ] **Step 1: Failing test:**

```go
func TestPackChangeIsOneLine(t *testing.T) {
	a, _, ch, _, _ := setup(t, true)
	a.OnApply(mods.ProfileClient, []mods.Change{{Action: "pack", Profile: mods.ProfileClient, Name: "Fabulously Optimized 6.4.0 → 6.5.0 (3 neu, 1 entfernt, 12 aktualisiert)"}})
	a.AfterRestart(context.Background())
	ev, ok := next(t, ch)
	if !ok || !strings.Contains(ev.Message, "**Basis-Modpack:**") || !strings.Contains(ev.Message, "• Fabulously Optimized 6.4.0 → 6.5.0") {
		t.Fatalf("event = %+v", ev)
	}
	if ev.Title != "🧩 Neu im Mod-Paket" {
		t.Errorf("Titel = %q", ev.Title)
	}
}
```

- [ ] **Step 2:** `go test ./internal/modnews/` → FAIL.
- [ ] **Step 3:** In `OnApply` die Bedingung `if c.Action == "add" || c.Action == "remove" {` ersetzen durch `if c.Action == "add" || c.Action == "remove" || c.Action == "pack" {`.

In `compose` ganz am Anfang (vor `added, removed := …`):

```go
	var packLines []string
	keep := func(list []mods.Change) []mods.Change {
		var out []mods.Change
		for _, c := range list {
			if c.Action == "pack" {
				packLines = append(packLines, c.Name)
				continue
			}
			out = append(out, c)
		}
		return out
	}
	server, client = keep(server), keep(client)
```

Die Prüfung `if len(added)+len(removed) == 0 {` ersetzen durch `if len(added)+len(removed)+len(packLines) == 0 {`. Direkt nach `var b strings.Builder` einfügen:

```go
	if len(packLines) > 0 {
		b.WriteString("**Basis-Modpack:**\n")
		for _, l := range packLines {
			b.WriteString("• " + l + "\n")
		}
	}
```

und in der Titel-Auswahl den ersten Fall ergänzen: vor `case len(added) > 0 && onServer:` einfügen

```go
	case len(added) == 0 && len(removed) == 0:
		title = "🧩 Neu im Mod-Paket"
```

- [ ] **Step 4:** `go test ./internal/modnews/ && go vet ./...` → PASS.
- [ ] **Step 5: Commit** `feat(modnews): Basis-Pack-Wechsel als eine Zeile`.

---

### Task 7: Verteilung — Configs und Pack-Dateien

**Files:** Modify `internal/dropbox/publish.go`, `internal/dropbox/dropbox_test.go`, `internal/modpack/channel.go`, `internal/modpack/channel_test.go`, `cmd/msm/main.go` (`packFingerprint`)

**Interfaces:**
- Consumes: `basepack.PackFile` (Task 3)
- Produces:
  - `dropbox.Channel` bekommt Felder `Root string` und `Extra func() []string` (relative Pfade unter Root, die zusätzlich ins ZIP kommen); `dropbox.Pack(ctx, c, dirs, root string, extra []string)`.
  - `func (c *modpack.Channel) SetBasepack(root string, configs func() []string, index func() map[string]basepack.PackFile)`
  - `packFingerprint(dirs map[string]string, root string, extra []string) string` in `main.go`

- [ ] **Step 1: Failing tests.**

`internal/dropbox/dropbox_test.go` anhängen:

```go
func TestZipExtraFiles(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "config", "sub"), 0o755)
	os.WriteFile(filepath.Join(root, "config", "sub", "a.json"), []byte("A"), 0o644)
	var buf bytes.Buffer
	n, err := zipAll(&buf, map[string]string{}, root, []string{"config/sub/a.json", "../evil"})
	if err != nil || n != 1 {
		t.Fatalf("n = %d, err = %v", n, err)
	}
	zr, _ := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if len(zr.File) != 1 || zr.File[0].Name != "config/sub/a.json" {
		t.Errorf("zip = %v", zr.File)
	}
}
```

`internal/modpack/channel_test.go` anhängen:

```go
func TestBasepackFilesAndConfigs(t *testing.T) {
	e := newEnv(t, "approved")
	e.addMod("sodium.jar", "P-sod", "1", true)
	// Pack-Datei von GitHub, Modrinth kennt sie nicht
	os.WriteFile(filepath.Join(e.mods, "gh.jar"), []byte("gh"), 0o644)
	root := filepath.Dir(e.mods)
	os.MkdirAll(filepath.Join(root, "config"), 0o755)
	os.WriteFile(filepath.Join(root, "config", "a.json"), []byte("A"), 0o644)
	e.ch.SetBasepack(root, func() []string { return []string{"config/a.json"} },
		func() map[string]basepack.PackFile {
			return map[string]basepack.PackFile{sha([]byte("gh")): {Path: "mods/gh.jar", Category: "mods", Filename: "gh.jar",
				SHA1: "g1", SHA512: sha([]byte("gh")), URL: "https://github.com/x/gh.jar", Size: 2, ProjectID: "P-gh", Version: "1"}}
		})
	if _, err := e.ch.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	idx := indexOf(t, e.api.packs[0])
	if n := len(idx["files"].([]any)); n != 2 {
		t.Errorf("files = %d, want 2 (sodium + GitHub-Datei aus dem Pack)", n)
	}
	zr, _ := zip.NewReader(bytes.NewReader(e.api.packs[0]), int64(len(e.api.packs[0])))
	found := false
	for _, f := range zr.File {
		if f.Name == "overrides/config/a.json" {
			found = true
		}
	}
	if !found {
		t.Error("freigegebene Config fehlt als overrides/config/a.json")
	}
	if st := e.ch.Status(); len(st.Omitted) != 0 {
		t.Errorf("omitted = %v", st.Omitted)
	}
}

func TestAllowedURL(t *testing.T) {
	for u, want := range map[string]bool{
		"https://cdn.modrinth.com/x": true, "https://github.com/a/b": true,
		"https://raw.githubusercontent.com/a": true, "https://gitlab.com/a": true,
		"http://cdn.modrinth.com/x": false, "https://evil.com/x": false, "https://github.com.evil.com/x": false,
	} {
		if allowedURL(u) != want {
			t.Errorf("allowedURL(%q) != %v", u, want)
		}
	}
}
```

(Imports in `channel_test.go` ergänzen: `basepack`.)

- [ ] **Step 2:** `go test ./internal/dropbox/ ./internal/modpack/` → FAIL.

- [ ] **Step 3: Implementierung.**

`internal/dropbox/publish.go`: `Pack` bekommt `root string, extra []string` und ruft statt `ZipDirs(pw, dirs)` die neue Funktion `zipAll(pw, dirs, root, extra)`. In `internal/dropbox/dropbox.go` ergänzen (ZipDirs bleibt für bestehende Aufrufer/Tests):

```go
// zipAll zips the category dirs plus extra files (Pfade relativ zu root,
// z. B. freigegebene Configs des Basis-Modpacks).
func zipAll(w io.Writer, dirs map[string]string, root string, extra []string) (int, error) {
	zw := zip.NewWriter(w)
	files, err := zipDirsInto(zw, dirs)
	if err != nil {
		return files, err
	}
	for _, rel := range extra {
		if rel == "" || strings.Contains(rel, "..") || strings.HasPrefix(rel, "/") || strings.ContainsAny(rel, `\:`) {
			continue
		}
		f, err := os.Open(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			continue // Datei inzwischen weg — nicht fatal
		}
		dst, err := zw.Create(rel)
		if err == nil {
			_, err = io.Copy(dst, f)
		}
		f.Close()
		if err != nil {
			return files, err
		}
		files++
	}
	return files, zw.Close()
}
```

und `ZipDirs` intern aufteilen: Schleifenkörper in `func zipDirsInto(zw *zip.Writer, dirs map[string]string) (int, error)` verschieben; `ZipDirs` wird zu

```go
func ZipDirs(w io.Writer, dirs map[string]string) (int, error) {
	zw := zip.NewWriter(w)
	n, err := zipDirsInto(zw, dirs)
	if err != nil {
		return n, err
	}
	return n, zw.Close()
}
```

`dropbox.Channel`:

```go
type Channel struct {
	C     *Client
	Dirs  map[string]string
	Root  string          // Client-Paket-Wurzel (für Extra)
	Extra func() []string // zusätzliche Dateien relativ zu Root (Configs)
}
```

`Publish` ruft `Pack(ctx, ch.C, ch.Dirs, ch.Root, extra)` mit `extra := []string(nil); if ch.Extra != nil { extra = ch.Extra() }`.

`internal/modpack/channel.go`:

```go
// allowedURL: Download-Quellen, die Modrinth in Modpacks erlaubt.
func allowedURL(u string) bool {
	for _, p := range []string{"https://cdn.modrinth.com/", "https://github.com/", "https://raw.githubusercontent.com/", "https://gitlab.com/"} {
		if strings.HasPrefix(u, p) {
			return true
		}
	}
	return false
}
```

Felder am `Channel`: `bpRoot string; bpConfigs func() []string; bpIndex func() map[string]basepack.PackFile`, Setter:

```go
// SetBasepack lets the channel ship base-pack files Modrinth does not know
// (z. B. GitHub-Links aus dem Pack) and the selected configs as overrides.
func (c *Channel) SetBasepack(root string, configs func() []string, index func() map[string]basepack.PackFile) {
	c.bpRoot, c.bpConfigs, c.bpIndex = root, configs, index
}
```

In `Publish` in der Dateischleife: die Bedingung `strings.HasPrefix(f.URL, "https://cdn.modrinth.com/")` durch `allowedURL(f.URL)` ersetzen. Wenn danach `file == nil`, vor `omitted = append(…)` den Pack-Index prüfen:

```go
		if file == nil && c.bpIndex != nil {
			if pf, ok := c.bpIndex()[l.sha512]; ok && allowedURL(pf.URL) && pf.SHA1 != "" {
				files = append(files, mrpack.File{Path: l.category + "/" + l.name, SHA1: pf.SHA1, SHA512: l.sha512, URL: pf.URL, Size: pf.Size})
				entries = append(entries, Entry{Project: pf.ProjectID, File: l.name, Version: pf.Version})
				continue
			}
		}
```

Overrides: vor dem Bau (`upload := func…`) die Configs einsammeln:

```go
	overrides := map[string][]byte{}
	if c.bpConfigs != nil {
		for _, rel := range c.bpConfigs() {
			if !mrpack.ValidPath(rel) {
				continue
			}
			if b, err := os.ReadFile(filepath.Join(c.bpRoot, filepath.FromSlash(rel))); err == nil {
				overrides[rel] = b
			}
		}
	}
```

und in `mrpack.Build(...)` die Meta um `Overrides: overrides` ergänzen.

`cmd/msm/main.go` — `packFingerprint(dirs map[string]string, root string, extra []string)`: nach der bestehenden Schleife für jede `rel` in `extra` die Größe von `filepath.Join(root, filepath.FromSlash(rel))` als Zeile `"extra/"+rel+"/"+size` anhängen. Aufrufstelle vorerst `packFingerprint(clientDirs, "", nil)` (Task 10 verdrahtet Root/Extra).

- [ ] **Step 4:** `go test ./internal/dropbox/ ./internal/modpack/ && go vet ./... && go build ./cmd/msm` → PASS.
- [ ] **Step 5: Commit** `feat: Basis-Pack-Dateien und Configs verteilen (ZIP, Modrinth-Modpack)`.

---

### Task 8: Versions-Watch und MC-Update

**Files:** Modify `internal/mods/versionwatch.go`, `internal/upgrade/upgrade.go`; Tests `internal/mods/versionwatch_events_test.go` (oder neue Datei `versionwatch_basepack_test.go`), `internal/upgrade/upgrade_test.go`

**Interfaces:**
- Produces:
  - `type BasepackReady struct { Name string; Ready bool }` (JSON `name, ready`); `WatchStatus.Basepack *BasepackReady` (JSON `basepack,omitempty`)
  - `func (w *Watcher) SetBasepack(f func(ctx context.Context, mc string) (name string, ready, set bool))`
  - `upgrade.Orchestrator.BasepackReady func(ctx context.Context, mc string) (name string, ready, set bool)`, `upgrade.Orchestrator.BasepackSwitch func(ctx context.Context, mc string) error`

- [ ] **Step 1: Failing tests.**

`internal/mods/versionwatch_basepack_test.go` — mit dem Watcher-Testaufbau aus `versionwatch_events_test.go` (Fake-Endpunkte für Manifest/Fabric, `mock.NewModrinth()`): nach `w.SetBasepack(func(context.Context, string) (string, bool, bool) { return "Fabulously Optimized", false, true })` liefert `Check` ein `Basepack` mit `Name == "Fabulously Optimized"` und `Ready == false`; ohne Setter ist `Basepack == nil`. (Den Fake-Aufbau aus der bestehenden Testdatei übernehmen; Funktion `TestCheckReportsBasepack`.)

`internal/upgrade/upgrade_test.go`:

```go
func TestUpgradeRefusesWithoutBasepackVersion(t *testing.T) {
	f := &fakes{backupOK: true}
	f.online.Store(true)
	o := newOrch(f, events.New())
	o.BasepackReady = func(context.Context, string) (string, bool, bool) { return "Fabulously Optimized", false, true }
	err := o.Start("26.2")
	if err == nil || !strings.Contains(err.Error(), "Fabulously Optimized") || !strings.Contains(err.Error(), "26.2") {
		t.Fatalf("err = %v", err)
	}
	if steps := f.list(); len(steps) != 0 {
		t.Errorf("Kette lief trotzdem an: %v", steps)
	}
}

func TestUpgradeSwitchesBasepack(t *testing.T) {
	f := &fakes{backupOK: true}
	f.online.Store(true)
	o := newOrch(f, events.New())
	var switched atomic.Value
	o.BasepackReady = func(context.Context, string) (string, bool, bool) { return "FO", true, true }
	o.BasepackSwitch = func(_ context.Context, mc string) error { switched.Store(mc); return nil }
	if err := o.Start("26.2"); err != nil {
		t.Fatal(err)
	}
	waitDone(t, o)
	if v, _ := switched.Load().(string); v != "26.2" {
		t.Errorf("Pack-Umstellung auf %q, want 26.2", v)
	}
}
```

- [ ] **Step 2:** `go test ./internal/mods/ ./internal/upgrade/` → FAIL.

- [ ] **Step 3: Implementierung.**

`versionwatch.go`: Typ + Feld wie oben; `Watcher` bekommt `basepack func(ctx context.Context, mc string) (string, bool, bool)` und

```go
// SetBasepack lets the watch report whether the base modpack exists for the
// newest MC version (Kachel; Client-Paket ohne passendes Pack wäre kaputt).
func (w *Watcher) SetBasepack(f func(ctx context.Context, mc string) (string, bool, bool)) { w.basepack = f }
```

In `Check` direkt vor `return status, nil`:

```go
	if w.basepack != nil && status.LatestVersion != "" {
		if name, ready, set := w.basepack(ctx, status.LatestVersion); set {
			status.Basepack = &BasepackReady{Name: name, Ready: ready}
		}
	}
```

`upgrade.go`: Felder am `Orchestrator`

```go
	// BasepackReady: gibt es das Basis-Modpack für die Zielversion?
	// set=false = kein Basis-Pack eingerichtet.
	BasepackReady func(ctx context.Context, mc string) (name string, ready, set bool)
	// BasepackSwitch stellt das Basis-Modpack nach dem Sprung um.
	BasepackSwitch func(ctx context.Context, mc string) error
```

In `Start` nach der `RoutineBusy`-Prüfung:

```go
	if o.BasepackReady != nil {
		bctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		name, ready, set := o.BasepackReady(bctx, version)
		cancel()
		if set && !ready {
			o.mu.Unlock()
			return fmt.Errorf("Basis-Modpack „%s“ gibt es noch nicht für Minecraft %s — abwarten oder Basis-Pack im Mods-Tab entfernen", name, version)
		}
	}
```

In `finish` ganz am Anfang (vor `o.setStatus("aktualisiere Client-Mods")`):

```go
	if o.BasepackSwitch != nil {
		o.setStatus("stelle Basis-Modpack um")
		if err := o.BasepackSwitch(ctx, version); err != nil {
			o.log.Error("basis-modpack umstellen", "err", err)
			clientErrPre = fmt.Errorf("basis-modpack umstellen: %w", err)
		}
	}
```

und `clientErrPre` in den bestehenden Client-Fehlerweg übernehmen: direkt nach der Deklaration `var clientErr error` die Zeile `clientErr = clientErrPre` einfügen (Deklaration `var clientErrPre error` vor dem neuen Block).

- [ ] **Step 4:** `go test ./internal/mods/ ./internal/upgrade/ && go vet ./...` → PASS.
- [ ] **Step 5: Commit** `feat: Basis-Modpack in Versions-Watch und MC-Update`.

---

### Task 9: API

**Files:** Create `internal/api/basepack.go`, `internal/api/basepack_test.go`; Modify `internal/api/api.go`, `internal/api/mods.go`

**Interfaces:**
- Consumes: `*basepack.Service` (Task 5)
- Produces: `api.Deps.Basepack *basepack.Service`; Routen
  - `GET /api/basepack` → `{"state": State|null, "available": {"versionId","versionNumber"}|null}`
  - `GET /api/basepack/search?q=` → `{"hits": [...], "mcVersion": "…"}`
  - `GET /api/basepack/preview?project=` → `Preview`
  - `POST /api/basepack/apply` `{"projectId","versionId","configs":{path:bool}}` → `{"message": summary}`
  - `POST /api/basepack/toggle` `{"key","on"}` → `{"ok":true}`
  - `POST /api/basepack/configs` `{"configs":{path:bool}}` → `{"ok":true}`
  - `DELETE /api/basepack` → `{"ok":true}`
  - `GET /api/mods?profile=client`: Einträge aus dem Pack mit `source: "basepack"`, abgeschaltete Pack-Mods zusätzlich mit `disabled: true`

- [ ] **Step 1: Failing test** — `internal/api/basepack_test.go`: Server mit `Deps{ModManager: mods.NewManager(nil, "fabric", nil), Basepack: nil, …}` → `GET /api/basepack` 404 (Route nicht registriert); mit einem `basepack.New(...)` aus Fakes (kopiere `fakeAPI`/`fakeFiles` nicht — nutze einen minimalen Fake in der Testdatei, der `SearchModpacks` usw. erfüllt) → `GET /api/basepack` liefert 200 mit `"state":null`; `POST /api/basepack/toggle` mit unbekanntem Schlüssel → 400; bei aktivem MC-Update (`Deps.Upgrader` nicht nötig — über das interne Feld `upgradeActive` im Test setzen: `srv.upgradeActive = func() bool { return true }`) → `POST /api/basepack/apply` 409.

Konkreter Test:

```go
func TestBasepackRoutes(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	kv := map[string]string{}
	bp := basepack.New(stubBPAPI{}, stubBPFiles{}, t.TempDir(), func(k string) string { return kv[k] }, func(k, v string) { kv[k] = v }, nil, log)
	srv := New(Deps{ModManager: mods.NewManager(nil, "fabric", nil), Basepack: bp, FallbackMCVersion: "26.3", Log: log})
	do := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rec
	}
	if rec := do("GET", "/api/basepack", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"state":null`) {
		t.Errorf("GET = %d %s", rec.Code, rec.Body)
	}
	if rec := do("POST", "/api/basepack/toggle", `{"key":"x","on":false}`); rec.Code != 400 {
		t.Errorf("toggle ohne Pack = %d", rec.Code)
	}
	srv.upgradeActive = func() bool { return true }
	if rec := do("POST", "/api/basepack/apply", `{"projectId":"FO","versionId":"1"}`); rec.Code != 409 {
		t.Errorf("apply während Update = %d", rec.Code)
	}
}

type stubBPAPI struct{}

func (stubBPAPI) SearchModpacks(context.Context, string, string, string, int) ([]modrinth.SearchHit, error) { return nil, nil }
func (stubBPAPI) Project(context.Context, string) (modrinth.Project, error)                           { return modrinth.Project{}, nil }
func (stubBPAPI) ProjectVersions(context.Context, string, string, string) ([]modrinth.Version, error) { return nil, nil }
func (stubBPAPI) VersionsByHash(context.Context, []string) (map[string]modrinth.Version, error)       { return nil, nil }
func (stubBPAPI) Projects(context.Context, []string) ([]modrinth.Project, error)                      { return nil, nil }
func (stubBPAPI) Download(context.Context, string) ([]byte, error)                                    { return nil, nil }

type stubBPFiles struct{}

func (stubBPFiles) ApplyFiles(string, []mods.FileAdd, []mods.FileRemove, string) (string, error) { return "", nil }
func (stubBPFiles) CheckUpdates(context.Context, string, string) ([]mods.Entry, error)           { return nil, nil }
```

- [ ] **Step 2:** `go test ./internal/api/` → FAIL.

- [ ] **Step 3: Implementierung.** `api.go`: Feld `basepack *basepack.Service`, `Deps.Basepack`, in `New` zuweisen; Routen in einem Block `if s.basepack != nil { … }` registrieren (die sieben oben). `internal/api/basepack.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mods"
)

func (s *Server) basepackBlocked(w http.ResponseWriter) bool {
	if s.upgradeActive != nil && s.upgradeActive() {
		httpError(w, http.StatusConflict, "Ein Minecraft-Update läuft gerade — Basis-Modpack danach ändern")
		return true
	}
	return false
}

func (s *Server) handleBasepackStatus(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"state": nil, "available": nil}
	if st, ok := s.basepack.State(); ok {
		out["state"] = st
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		if v, err := s.basepack.Available(ctx, s.mcVersion()); err == nil && v != nil {
			out["available"] = map[string]string{"versionId": v.ID, "versionNumber": v.VersionNumber}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleBasepackSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < 2 {
		httpError(w, http.StatusBadRequest, "Suchbegriff zu kurz")
		return
	}
	mc := s.mcVersion()
	hits, err := s.basepack.Search(r.Context(), q, mc)
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hits": hits, "mcVersion": mc})
}

func (s *Server) handleBasepackPreview(w http.ResponseWriter, r *http.Request) {
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	if project == "" {
		httpError(w, http.StatusBadRequest, "project fehlt")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	pv, err := s.basepack.Preview(ctx, project, s.mcVersion())
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, pv)
}

func (s *Server) handleBasepackApply(w http.ResponseWriter, r *http.Request) {
	if s.basepackBlocked(w) {
		return
	}
	var req struct {
		ProjectID string          `json:"projectId"`
		VersionID string          `json:"versionId"`
		Configs   map[string]bool `json:"configs"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil || req.ProjectID == "" {
		httpError(w, http.StatusBadRequest, "projectId fehlt")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	summary, err := s.basepack.Apply(ctx, req.ProjectID, req.VersionID, s.mcVersion(), req.Configs)
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.audit(r.Context(), "basepack.apply", summary)
	writeJSON(w, http.StatusOK, map[string]string{"message": summary + " — wird beim nächsten Neustart an die Spieler verteilt."})
}

func (s *Server) handleBasepackToggle(w http.ResponseWriter, r *http.Request) {
	if s.basepackBlocked(w) {
		return
	}
	var req struct {
		Key string `json:"key"`
		On  bool   `json:"on"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.Key == "" {
		httpError(w, http.StatusBadRequest, "key fehlt")
		return
	}
	if _, ok := s.basepack.State(); !ok {
		httpError(w, http.StatusBadRequest, "kein Basis-Modpack eingerichtet")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := s.basepack.Toggle(ctx, req.Key, req.On); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r.Context(), "basepack.toggle", req.Key)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleBasepackConfigs(w http.ResponseWriter, r *http.Request) {
	if s.basepackBlocked(w) {
		return
	}
	var req struct {
		Configs map[string]bool `json:"configs"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid json")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := s.basepack.SetConfigs(ctx, req.Configs); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r.Context(), "basepack.configs", "auswahl geändert")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleBasepackRemove(w http.ResponseWriter, r *http.Request) {
	if s.basepackBlocked(w) {
		return
	}
	if err := s.basepack.Remove(r.Context()); err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.audit(r.Context(), "basepack.remove", "")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// decorateClient marks pack files and appends disabled pack mods.
func (s *Server) decorateClient(entries []mods.Entry) []mods.Entry {
	st, ok := s.basepack.State()
	if !ok {
		return entries
	}
	for i := range entries {
		if s.basepack.Pinned(entries[i].Filename) {
			entries[i].Source = "basepack"
		}
	}
	for _, f := range st.Files {
		key := f.ProjectID
		if key == "" {
			key = f.Path
		}
		if st.IsDisabled(key) {
			entries = append(entries, mods.Entry{Profile: mods.ProfileClient, Category: f.Category, Filename: f.Filename,
				Name: f.Name, ProjectID: f.ProjectID, Version: f.Version, Managed: f.ProjectID != "", Source: "basepack", Disabled: true})
		}
	}
	return entries
}
```

In `handleModsList` vor `writeJSON`: `if profile == mods.ProfileClient && s.basepack != nil { entries = s.decorateClient(entries) }`.

Die Toggle-Schlüssel im UI: `projectId` der Datei, sonst `path` — für Einträge ohne `projectId` liefert die Liste keinen Pfad; das UI nutzt deshalb `projectId || category + "/" + filename` (= `Path` aus dem Pack).

- [ ] **Step 4:** `go test ./internal/api/ && go vet ./...` → PASS.
- [ ] **Step 5: Commit** `feat(api): Basis-Modpack-Routen, Herkunft in der Mod-Liste`.

---

### Task 10: Verdrahtung in `main.go`

**Files:** Modify `cmd/msm/main.go`

**Interfaces:** Consumes Tasks 4–9.

- [ ] **Step 1:** Client-Paket-Wurzel verfügbar machen: die Variable `clientPack := envOr("MSM_CLIENT_PACK_DIR", "/mc/client-pack")` aus dem `else`-Zweig herausziehen (vor `if *mockMode {` deklarieren: `var clientPackRoot string`; im echten Zweig `clientPackRoot = envOr(...)` und `clientPack` durch `clientPackRoot` ersetzen; im Mock-Zweig leer lassen).

- [ ] **Step 2:** Direkt nach `modmgr.SetChangeHook(news.OnApply)` (bzw. nach dem `news.SetPendingServer`-Block):

```go
	// Basis-Modpack (nur mit echtem Modrinth-Client)
	var bp *basepack.Service
	if mrc, ok := modAPI.(*modrinth.Client); ok && clientPackRoot != "" {
		bp = basepack.New(mrc, modmgr, clientPackRoot, kvGet, kvSet,
			func(c []mods.Change) { news.OnApply(mods.ProfileClient, c) }, log)
		modmgr.SetPinned(func(profile, filename string) bool {
			return profile == mods.ProfileClient && bp.Pinned(filename)
		})
	}
```

- [ ] **Step 3:** Watcher: direkt nach `watcher := mods.NewWatcher(...)`: `if bp != nil { watcher.SetBasepack(bp.Ready) }`.

- [ ] **Step 4:** Upgrade: im `upgrader`-Block (neben `ModSeal`): `if bp != nil { upgrader.BasepackReady = bp.Ready; upgrader.BasepackSwitch = bp.SwitchMC }`.

- [ ] **Step 5:** Verteilung: beim Bau der Kanäle

```go
	var bpRoot string
	bpConfigs := func() []string { return nil }
	if bp != nil {
		bpRoot = bp.Root()
		bpConfigs = bp.WrittenConfigs
	}
	channels := []packpub.Channel{dropbox.Channel{C: dbx, Dirs: clientDirs, Root: bpRoot, Extra: bpConfigs}}
```

nach `mpChannel = modpack.New(...)`: `if bp != nil { mpChannel.SetBasepack(bpRoot, bp.WrittenConfigs, bp.IndexFiles) }`; Fingerabdruck: `packFingerprint(clientDirs, bpRoot, bpConfigs())`.

- [ ] **Step 6:** API: `Basepack: bp,` in `api.Deps`.
- [ ] **Step 7:** `go vet ./... && go test ./... && go build ./cmd/msm`; Mock-Start (`go run ./cmd/msm -mock -addr 127.0.0.1:8093`): `/healthz` 200, `/api/basepack` 404 (Mock hat kein Basis-Pack).
- [ ] **Step 8: Commit** `feat: Basis-Modpack verdrahten`.

---

### Task 11: Oberfläche

**Files:** Create `web/src/lib/BasePack.svelte`; Modify `web/src/lib/Mods.svelte`, `web/src/lib/stream.js`

- [ ] **Step 1: `stream.js`** ergänzen:

```js
export const basepackStatus = () => apiFetch('/api/basepack')
export const basepackSearch = (q) => apiFetch(`/api/basepack/search?q=${encodeURIComponent(q)}`)
export const basepackPreview = (project) => apiFetch(`/api/basepack/preview?project=${encodeURIComponent(project)}`)
export const basepackApply = (projectId, versionId, configs) =>
  apiFetch('/api/basepack/apply', { method: 'POST', body: JSON.stringify({ projectId, versionId, configs }) })
export const basepackToggle = (key, on) => apiFetch('/api/basepack/toggle', { method: 'POST', body: JSON.stringify({ key, on }) })
export const basepackConfigs = (configs) => apiFetch('/api/basepack/configs', { method: 'POST', body: JSON.stringify({ configs }) })
export const basepackRemove = () => apiFetch('/api/basepack', { method: 'DELETE' })
```

- [ ] **Step 2: `BasePack.svelte`:**

```svelte
<script>
  import { onMount } from 'svelte'
  import { basepackStatus, basepackSearch, basepackPreview, basepackApply, basepackRemove } from './stream.js'

  let { onchange } = $props()
  let enabled = $state(true)
  let status = $state(null)
  let query = $state('')
  let hits = $state([])
  let preview = $state(null)
  let configs = $state({})
  let busy = $state(false)
  let error = $state('')
  let info = $state('')

  async function load() {
    try {
      status = await basepackStatus()
    } catch {
      enabled = false // Feature aus (Mock / kein Modrinth)
    }
  }
  onMount(load)

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

  const doSearch = (ev) => {
    ev.preventDefault()
    guard(async () => { hits = (await basepackSearch(query.trim())).hits ?? [] })
  }
  const openPreview = (projectId) => guard(async () => {
    preview = await basepackPreview(projectId)
    configs = Object.fromEntries(preview.configs.map((c) => [c.path, c.on]))
  })
  const doApply = () => guard(async () => {
    const res = await basepackApply(preview.projectId, preview.versionId, configs)
    info = res.message
    preview = null
    hits = []
    await load()
    onchange?.()
  })
  const doRemove = () => {
    if (!confirm(`Basis-Modpack „${status.state.title}" entfernen? Pack-Mods und Pack-Configs gehen raus, eigene Mods bleiben.`)) return
    guard(async () => { await basepackRemove(); info = 'Basis-Modpack entfernt.'; await load(); onchange?.() })
  }
</script>

{#if enabled}
<div class="panel wide">
  <h2>Basis-Modpack</h2>
  {#if error}<div class="err-msg">{error}</div>{/if}
  {#if info}<div class="info-msg">{info}</div>{/if}

  {#if status?.state}
    <p>
      <strong>{status.state.title}</strong> {status.state.versionNumber}
      <span class="dim-sm">(MC {status.state.mc} · {status.state.files.length} Dateien · {status.state.disabled?.length ?? 0} abgeschaltet)</span>
    </p>
    <div class="row">
      {#if status.available}
        <button class="btn accent" onclick={() => openPreview(status.state.projectId)} disabled={busy}>
          {status.available.versionNumber} verfügbar — Vorschau
        </button>
      {:else}
        <span class="dim-sm">✔ aktuell</span>
      {/if}
      <button class="btn" onclick={() => openPreview(status.state.projectId)} disabled={busy}>Configs / Neu laden</button>
      <button class="btn danger" onclick={doRemove} disabled={busy}>Entfernen</button>
    </div>
  {:else}
    <p class="dim-sm">Ein fertiges Modrinth-Modpack (z. B. Fabulously Optimized) als Grundlage des Client-Pakets: Mods kommen als getestete Kombination, eigene Mods bleiben, einzelne Pack-Mods lassen sich abschalten.</p>
    <form class="row" onsubmit={doSearch}>
      <input type="text" bind:value={query} placeholder="Modpack auf Modrinth suchen, z. B. Fabulously Optimized" />
      <button class="btn" type="submit" disabled={busy || query.trim().length < 2}>Suchen</button>
    </form>
    {#each hits as h (h.project_id)}
      <div class="row">
        <strong>{h.title}</strong> <span class="dim-sm">{(h.downloads ?? 0).toLocaleString('de-DE')} Downloads</span>
        <button class="btn sm" onclick={() => openPreview(h.project_id)} disabled={busy}>Vorschau</button>
      </div>
    {/each}
  {/if}

  {#if busy && !preview}<p class="dim-sm">Lade Modpack und prüfe Dateien …</p>{/if}

  {#if preview}
    <div class="dialog">
      <h3>{preview.title} {preview.versionNumber} <span class="dim-sm">(MC {preview.mc}, {preview.total} Dateien)</span></h3>
      {#if preview.plan.add?.length}<p><strong>Neu:</strong> {preview.plan.add.map((f) => f.name).join(', ')}</p>{/if}
      {#if preview.plan.remove?.length}<p><strong>Entfernt:</strong> {preview.plan.remove.map((f) => f.name).join(', ')}</p>{/if}
      {#if preview.plan.update?.length}<p><strong>Aktualisiert:</strong> {preview.plan.update.length} Dateien</p>{/if}
      {#if preview.plan.absorbOwn?.length}<p class="warn-msg">Übernimmt deine eigenen: {preview.plan.absorbOwn.map((o) => o.Name || o.Filename).join(', ')} (eigene Kopie fällt weg)</p>{/if}
      {#if preview.plan.dropAbsorbed?.length}<p class="warn-msg">Entfällt (war vorher auch deine eigene Mod, bei Bedarf neu hinzufügen): {preview.plan.dropAbsorbed.join(', ')}</p>{/if}
      {#if preview.ignored?.length}<p class="dim-sm">Ignoriert (unbekannte Ordner): {preview.ignored.join(', ')}</p>{/if}
      {#if preview.configs?.length}
        <h4>Configs</h4>
        <p class="dim-sm">Mitgelieferte Configs überschreiben bei jedem Paket-Update die Datei beim Spieler. Spieler-Einstellungen (Grafik, Tasten, Shader, Minimap) sind deshalb vorab aus.</p>
        {#each preview.configs as c (c.path)}
          <label class="cfg"><input type="checkbox" bind:checked={configs[c.path]} /> <code>{c.path}</code>{#if c.new} <span class="badge">neu</span>{/if}</label>
        {/each}
      {/if}
      <div class="row">
        <button class="btn accent" onclick={doApply} disabled={busy}>Übernehmen</button>
        <button class="btn" onclick={() => (preview = null)} disabled={busy}>Abbrechen</button>
      </div>
    </div>
  {/if}
</div>
{/if}

<style>
  .row { display: flex; gap: 0.6rem; align-items: center; flex-wrap: wrap; margin-bottom: 0.5rem; }
  input[type='text'] { min-width: min(24rem, 100%); }
  .btn { background: var(--panel-border); border: none; border-radius: 5px; color: var(--text); padding: 0.4rem 0.9rem; cursor: pointer; font-size: 0.85rem; }
  .btn:disabled { opacity: 0.4; cursor: default; }
  .btn.accent { background: var(--accent); color: #0f1419; font-weight: 600; }
  .btn.danger { background: #5a2a2a; }
  .btn.sm { padding: 0.2rem 0.6rem; font-size: 0.75rem; }
  .dialog { border: 1px solid var(--accent); border-radius: 6px; padding: 0.6rem 0.8rem; margin: 0.6rem 0; }
  .cfg { display: block; font-size: 0.8rem; margin: 0.15rem 0; }
  .badge { font-size: 0.7rem; background: var(--warn); color: #0f1419; border-radius: 3px; padding: 0 0.3rem; }
  .dim-sm { font-size: 0.75rem; color: var(--text-dim); }
  .err-msg { color: var(--err); font-size: 0.85rem; margin-bottom: 0.6rem; }
  .info-msg { color: var(--accent); font-size: 0.85rem; margin-bottom: 0.6rem; }
  .warn-msg { color: var(--warn); font-size: 0.85rem; }
</style>
```

- [ ] **Step 3: `Mods.svelte`:** `import BasePack from './BasePack.svelte'` und `basepackToggle` aus `stream.js`. Direkt nach `<AddMod onchange={load} />` einfügen: `{#if profile === 'client'}<BasePack onchange={load} />{/if}` (Hinweis: `profile` ist unterhalb deklariert — falls Svelte meckert, den Block hinter die Profil-Auswahl verschieben).

In der Tabellenzeile:
- Zeilen-Klasse: `class={e.disabled ? 'unmanaged' : e.managed ? '' : 'unmanaged'}`.
- Spalte „Mod": `{e.name || '—'}{#if e.source === 'basepack'} <span class="badge staged">Basis-Pack</span>{/if}`.
- Spalte „Update": vor den bestehenden Fällen `{#if e.disabled}<span class="badge unmanaged">aus</span>{:else if e.pending === 'add'}…` (den bestehenden ersten `{#if e.pending === 'add'}` zu `{:else if …}` machen).
- Aktionen: für `e.source === 'basepack'` statt Stagen/Entfernen einen Knopf

```svelte
<button class="btn sm" onclick={() => doTogglePack(e)} disabled={busy}>{e.disabled ? 'Anschalten' : 'Abschalten'}</button>
```

und die bestehenden Knöpfe in `{:else}` setzen. Funktion im Script:

```js
  const doTogglePack = (e) => run(() => basepackToggle(e.projectId || `${e.category}/${e.filename}`, !!e.disabled),
    () => (e.disabled ? `${e.name || e.filename} angeschaltet` : `${e.name || e.filename} abgeschaltet`))
```

- [ ] **Step 4:** `cd web && npm run build` (ohne Warnungen), `cd .. && go build ./cmd/msm`.
- [ ] **Step 5: Commit** `feat(web): Basis-Modpack im Mods-Tab`.

---

### Task 12: Doku

- [ ] README (Abschnitt Client-Paket: Basis-Modpack in zwei Sätzen), Vault: „MSM – Basis-Modpack" (Status umgesetzt, Umsetzungsbericht), „MSM – Nutzungshandbuch" (Abschnitt Basis-Modpack unter „Tab: Mods"), „MSM – Architektur & Code-Struktur" (Paket `basepack`, `mods.ApplyFiles`/`SetPinned`/`.basepack`, Routen, app_state `basepack.state`).
- [ ] `go test ./...` grün; Commit `docs: Basis-Modpack`.

---

## Live-Test (Controller, nach Task 12)

WSL, Branch `feature/base-modpack`, mit dem echten Fabulously Optimized:
1. Suche „Fabulously Optimized" → Vorschau (Mods, Configs mit Vorauswahl, eigene Sodium wird übernommen) → Übernehmen → Mod-Liste zeigt „Basis-Pack"-Einträge, Configs liegen unter `client-pack/config/`.
2. Eine Pack-Mod abschalten → Datei weg, Liste „aus"; wieder anschalten.
3. Eigene Mod hinzufügen → bleibt eigene; Update-Prüfung bietet für Pack-Mods nichts an.
4. Rollback-Knopf → Stopp-Hinweis am Basis-Pack-Satz.
5. Ältere FO-Version per API übernehmen (`POST /api/basepack/apply` mit älterer versionId), dann „verfügbar → Vorschau → Übernehmen": Änderungsliste, Abschaltung bleibt.
6. Neustart → 🧩 mit Zeile „Fabulously Optimized … → …" und 📦; ZIP enthält `config/…`; Modrinth-Modpack (falls eingerichtet) enthält `overrides/config/…` und keine weggelassenen Dateien.
7. MC-Update 1.21.11 → 26.3 mit gesetztem FO: Kachel zeigt FO bereit, Umstellung auf FO-Version für 26.3.
