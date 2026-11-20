// Package modrinth is a minimal client for the Modrinth API v2 — only the
// endpoints MSM needs: bulk hash lookup for update checks and per-project
// version queries for the readiness check. A descriptive User-Agent is
// mandatory per API rules; requests are throttled well below the
// 300/min limit.
package modrinth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	baseURL   = "https://api.modrinth.com/v2"
	userAgent = "TigerKnight555/Minecraft-Server-Management (msm dashboard; github.com/TigerKnight555/Minecraft-Server-Management)"
)

type Client struct {
	http     *http.Client
	base     string
	throttle <-chan time.Time
}

func New() *Client {
	return &Client{
		http: &http.Client{Timeout: 30 * time.Second},
		base: baseURL,
		// max ~2 Anfragen/s — weit unter dem Limit von 300/min
		throttle: time.Tick(500 * time.Millisecond),
	}
}

// NewWithBase is used by tests to point at a fake server (no throttle).
func NewWithBase(base string) *Client {
	ch := make(chan time.Time)
	close(ch) // closed channel: receives immediately
	return &Client{http: &http.Client{Timeout: 5 * time.Second}, base: base, throttle: ch}
}

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

// Version is one Modrinth project version (fields MSM needs).
type Version struct {
	ID            string       `json:"id"`
	ProjectID     string       `json:"project_id"`
	Name          string       `json:"name"`
	VersionNumber string       `json:"version_number"`
	Changelog     string       `json:"changelog"`
	GameVersions  []string     `json:"game_versions"`
	Loaders       []string     `json:"loaders"`
	Files         []File       `json:"files"`
	Dependencies  []Dependency `json:"dependencies"`
}

type File struct {
	URL      string `json:"url"`
	Filename string `json:"filename"`
	Primary  bool   `json:"primary"`
	Size     int64  `json:"size"`
	Hashes   struct {
		SHA512 string `json:"sha512"`
		SHA1   string `json:"sha1"`
	} `json:"hashes"`
}

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
	Status      string `json:"status"`
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

// PrimaryFile returns the primary (or first) file of a version.
func (v Version) PrimaryFile() (File, bool) {
	for _, f := range v.Files {
		if f.Primary {
			return f, true
		}
	}
	if len(v.Files) > 0 {
		return v.Files[0], true
	}
	return File{}, false
}

// LatestVersions maps every known file hash to the newest matching version
// for the given loader + MC version — one bulk request for all mods.
// Hashes without a Modrinth match are absent from the result (unmanaged).
func (c *Client) LatestVersions(ctx context.Context, sha512s []string, loader, mcVersion string) (map[string]Version, error) {
	if len(sha512s) == 0 {
		return map[string]Version{}, nil
	}
	req := map[string]any{
		"hashes":        sha512s,
		"algorithm":     "sha512",
		"loaders":       []string{loader},
		"game_versions": []string{mcVersion},
	}
	out := map[string]Version{}
	if err := c.do(ctx, http.MethodPost, "/version_files/update", req, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// VersionsByHash resolves file hashes to their current Modrinth versions
// (identifies project IDs and names for the inventory).
func (c *Client) VersionsByHash(ctx context.Context, sha512s []string) (map[string]Version, error) {
	if len(sha512s) == 0 {
		return map[string]Version{}, nil
	}
	req := map[string]any{"hashes": sha512s, "algorithm": "sha512"}
	out := map[string]Version{}
	if err := c.do(ctx, http.MethodPost, "/version_files", req, &out); err != nil {
		return nil, err
	}
	return out, nil
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

// Projects fetches several projects in one request (/projects?ids=[...]) —
// für Anzeigenamen im Mod-Inventar statt einer Abfrage pro Mod.
func (c *Client) Projects(ctx context.Context, ids []string) ([]Project, error) {
	if len(ids) == 0 {
		return []Project{}, nil
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("ids", string(raw))
	var out []Project
	if err := c.do(ctx, http.MethodGet, "/projects?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ProjectVersions lists a project's versions for loader + MC version,
// newest first (Modrinth-Reihenfolge). Die Filter sind JSON-Arrays im
// Query-String und MÜSSEN URL-encodiert werden — rohe Anführungszeichen/
// Klammern beantwortet Modrinth mit 400 (Learning 14).
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

// ProjectSupports reports whether a project has any version for the given
// loader + MC version (readiness check for the version watch).
func (c *Client) ProjectSupports(ctx context.Context, projectID, loader, mcVersion string) (bool, error) {
	versions, err := c.ProjectVersions(ctx, projectID, loader, mcVersion)
	if err != nil {
		return false, err
	}
	return len(versions) > 0, nil
}

// Download fetches a file and returns its content (staging verifies the
// SHA-512 before anything touches the live mods directory).
func (c *Client) Download(ctx context.Context, url string) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.throttle:
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 256<<20)) // 256 MB Schutzgrenze
}

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
