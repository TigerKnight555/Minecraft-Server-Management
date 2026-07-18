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
	"net/http"
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
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.throttle:
	}
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("modrinth rate limit erreicht (429)")
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("modrinth %s: %s: %s", path, resp.Status, strings.TrimSpace(string(b)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Version is one Modrinth project version (fields MSM needs).
type Version struct {
	ID            string   `json:"id"`
	ProjectID     string   `json:"project_id"`
	Name          string   `json:"name"`
	VersionNumber string   `json:"version_number"`
	Changelog     string   `json:"changelog"`
	GameVersions  []string `json:"game_versions"`
	Loaders       []string `json:"loaders"`
	Files         []File   `json:"files"`
}

type File struct {
	URL      string `json:"url"`
	Filename string `json:"filename"`
	Primary  bool   `json:"primary"`
	Size     int64  `json:"size"`
	Hashes   struct {
		SHA512 string `json:"sha512"`
	} `json:"hashes"`
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

// ProjectSupports reports whether a project has any version for the given
// loader + MC version (readiness check for the version watch).
func (c *Client) ProjectSupports(ctx context.Context, projectID, loader, mcVersion string) (bool, error) {
	path := fmt.Sprintf("/project/%s/version?loaders=[%q]&game_versions=[%q]",
		projectID, loader, mcVersion)
	var versions []Version
	if err := c.do(ctx, http.MethodGet, path, nil, &versions); err != nil {
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
