// Package dropbox publishes the client pack (Phase 4.8): ZIP des kompletten
// Client-Profils → Dropbox (Upload-Session, gestreamt — das Container-FS ist
// read-only, es gibt kein Temp-File) → Shared Link → Discord-Embed.
// Auth: Scoped App + Refresh-Token; MSM holt sich Access-Tokens selbst.
package dropbox

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Config holds the app credentials (alle aus der .env, nie im Repo).
type Config struct {
	AppKey       string
	AppSecret    string
	RefreshToken string
}

func (c Config) Complete() bool {
	return c.AppKey != "" && c.AppSecret != "" && c.RefreshToken != ""
}

type Client struct {
	cfg  func() Config // Provider: Einstellungen können sich zur Laufzeit ändern
	http *http.Client

	tokenURL   string // overridable in tests
	apiURL     string // api.dropboxapi.com base
	contentURL string // content.dropboxapi.com base

	mu          sync.Mutex
	accessToken string
	tokenUntil  time.Time
	tokenFor    Config // Credentials, für die der Access-Token gilt
}

// New wraps a fixed config (Tests, .env-only-Betrieb).
func New(cfg Config) *Client {
	return NewDynamic(func() Config { return cfg })
}

// NewDynamic reads the credentials via provider on every use — der
// Einstellungen-Tab kann sie ändern, ohne dass MSM neu starten muss.
func NewDynamic(cfg func() Config) *Client {
	return &Client{
		cfg:        cfg,
		http:       &http.Client{Timeout: 5 * time.Minute},
		tokenURL:   "https://api.dropbox.com/oauth2/token",
		apiURL:     "https://api.dropboxapi.com",
		contentURL: "https://content.dropboxapi.com",
	}
}

// Ready reports whether credentials are currently complete.
func (c *Client) Ready() bool { return c.cfg().Complete() }

// SetEndpoints overrides the upstream URLs (tests).
func (c *Client) SetEndpoints(token, api, content string) {
	c.tokenURL, c.apiURL, c.contentURL = token, api, content
}

// token returns a valid access token, refreshing it when needed. Ändern
// sich die Credentials (Einstellungen-Tab), wird der Cache verworfen.
func (c *Client) token(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cfg := c.cfg()
	if !cfg.Complete() {
		return "", fmt.Errorf("dropbox nicht konfiguriert — App-Key/Secret/Refresh-Token im Einstellungen-Tab hinterlegen")
	}
	if c.accessToken != "" && time.Now().Before(c.tokenUntil) && cfg == c.tokenFor {
		return c.accessToken, nil
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {cfg.RefreshToken},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(cfg.AppKey, cfg.AppSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("dropbox token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return "", fmt.Errorf("dropbox token: %s: %s", resp.Status, body)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", err
	}
	c.accessToken = tok.AccessToken
	c.tokenFor = cfg
	c.tokenUntil = time.Now().Add(time.Duration(tok.ExpiresIn-60) * time.Second)
	return c.accessToken, nil
}

const chunkSize = 8 << 20 // 8 MiB pro Session-Append

// Upload streams r to the given Dropbox path via an upload session
// (funktioniert für jede Größe, Pflicht ab 150 MB).
func (c *Client) Upload(ctx context.Context, path string, r io.Reader) error {
	tok, err := c.token(ctx)
	if err != nil {
		return err
	}
	// Session starten
	var start struct {
		SessionID string `json:"session_id"`
	}
	if err := c.content(ctx, tok, "/2/files/upload_session/start", `{"close":false}`, nil, &start); err != nil {
		return fmt.Errorf("upload start: %w", err)
	}
	buf := make([]byte, chunkSize)
	offset := int64(0)
	for {
		n, readErr := io.ReadFull(r, buf)
		if n > 0 {
			arg := fmt.Sprintf(`{"cursor":{"session_id":%q,"offset":%d},"close":false}`, start.SessionID, offset)
			if err := c.content(ctx, tok, "/2/files/upload_session/append_v2", arg, bytes.NewReader(buf[:n]), nil); err != nil {
				return fmt.Errorf("upload append @%d: %w", offset, err)
			}
			offset += int64(n)
		}
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	arg := fmt.Sprintf(`{"cursor":{"session_id":%q,"offset":%d},"commit":{"path":%q,"mode":"overwrite"}}`,
		start.SessionID, offset, path)
	if err := c.content(ctx, tok, "/2/files/upload_session/finish", arg, nil, nil); err != nil {
		return fmt.Errorf("upload finish: %w", err)
	}
	return nil
}

// ShareLink returns a public link for path (existierender Link wird
// wiederverwendet — Dropbox liefert sonst 409 shared_link_already_exists).
func (c *Client) ShareLink(ctx context.Context, path string) (string, error) {
	tok, err := c.token(ctx)
	if err != nil {
		return "", err
	}
	var created struct {
		URL string `json:"url"`
	}
	err = c.api(ctx, tok, "/2/sharing/create_shared_link_with_settings",
		fmt.Sprintf(`{"path":%q}`, path), &created)
	if err == nil {
		return created.URL, nil
	}
	if !strings.Contains(err.Error(), "shared_link_already_exists") {
		return "", err
	}
	var list struct {
		Links []struct {
			URL string `json:"url"`
		} `json:"links"`
	}
	if err := c.api(ctx, tok, "/2/sharing/list_shared_links",
		fmt.Sprintf(`{"path":%q,"direct_only":true}`, path), &list); err != nil {
		return "", err
	}
	if len(list.Links) == 0 {
		return "", fmt.Errorf("kein shared link für %s", path)
	}
	return list.Links[0].URL, nil
}

// api posts a JSON RPC call to api.dropboxapi.com.
func (c *Client) api(ctx context.Context, tok, endpoint, body string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL+endpoint, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("dropbox %s: %s: %s", endpoint, resp.Status, raw)
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// content posts to content.dropboxapi.com with the API arg header.
func (c *Client) content(ctx context.Context, tok, endpoint, apiArg string, body io.Reader, out any) error {
	if body == nil {
		body = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.contentURL+endpoint, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Dropbox-API-Arg", apiArg)
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("dropbox %s: %s: %s", endpoint, resp.Status, raw)
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// ZipDirs streams the given category dirs as one ZIP into w. Pfade im
// Archiv: <category>/<datei> — genau die Struktur, die Spieler in ihren
// .minecraft-Ordner entpacken.
func ZipDirs(w io.Writer, dirs map[string]string) (files int, err error) {
	zw := zip.NewWriter(w)
	// deterministische Reihenfolge
	var cats []string
	for c := range dirs {
		cats = append(cats, c)
	}
	sortStrings(cats)
	for _, cat := range cats {
		entries, err := os.ReadDir(dirs[cat])
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return files, err
		}
		for _, e := range entries {
			if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			f, err := os.Open(filepath.Join(dirs[cat], e.Name()))
			if err != nil {
				return files, err
			}
			// Store statt Deflate: JARs/ZIPs sind schon komprimiert
			hdr := &zip.FileHeader{Name: cat + "/" + e.Name(), Method: zip.Store}
			if info, err := e.Info(); err == nil {
				hdr.Modified = info.ModTime()
			}
			dst, err := zw.CreateHeader(hdr)
			if err == nil {
				_, err = io.Copy(dst, f)
			}
			f.Close()
			if err != nil {
				return files, err
			}
			files++
		}
	}
	return files, zw.Close()
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
