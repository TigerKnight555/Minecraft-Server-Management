// Package selfupdate lets MSM update ITSELF from the dashboard (Konzept
// Entscheidung #15): der Checker fragt GitHub nach dem neuesten Tag (Releases
// werden auf master getaggt), die UI zeigt „Update verfügbar", der Klick
// schreibt eine Signaldatei — der Host-Helfer macht `git checkout <tag>` +
// `docker compose up -d --build msm`. MSM stirbt beim Rebuild und kommt als
// neue Version wieder; schlägt der Build fehl, läuft der alte Container
// weiter (compose baut zuerst).
package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// tagRe: was als Release-Tag akzeptiert wird — der Wert wandert in eine
// Signaldatei und von dort in git-Kommandos, deshalb streng.
var tagRe = regexp.MustCompile(`^v?[0-9A-Za-z][0-9A-Za-z._-]{0,63}$`)

// Status is what the dashboard shows.
type Status struct {
	Current   string    `json:"current"`         // laufende Version (git describe beim Build)
	Latest    string    `json:"latest"`          // neuester Tag auf GitHub
	Newer     bool      `json:"newer"`           // Update verfügbar?
	CheckedAt time.Time `json:"checkedAt"`       // letzter erfolgreicher Check
	Error     string    `json:"error,omitempty"` // letzter Check-Fehler (z. B. Repo privat)
}

// Signaler writes the host request (hostctl.Signaler satisfies this).
type Signaler interface {
	RequestSelfUpdate(tag string) error
}

type Checker struct {
	repo    string // "owner/name"
	token   string // optional (privates Repo)
	current string
	signal  Signaler
	http    *http.Client
	apiBase string // overridable in tests

	mu     sync.Mutex
	status Status
}

func New(repo, token, current string, signal Signaler) *Checker {
	return &Checker{
		repo: repo, token: token, current: current, signal: signal,
		http:    &http.Client{Timeout: 30 * time.Second},
		apiBase: "https://api.github.com",
	}
}

// SetAPIBase overrides the GitHub endpoint (tests).
func (c *Checker) SetAPIBase(base string) { c.apiBase = base }

// Status returns the last check result (Current ist immer gefüllt).
func (c *Checker) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.status
	s.Current = c.current
	return s
}

// Check fetches the tags and updates the status.
func (c *Checker) Check(ctx context.Context) (Status, error) {
	latest, err := c.latestTag(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.status.Error = err.Error()
		s := c.status
		s.Current = c.current
		return s, err
	}
	c.status = Status{
		Latest:    latest,
		Newer:     latest != "" && !sameVersion(c.current, latest) && !newerOrEqual(c.current, latest),
		CheckedAt: time.Now(),
	}
	s := c.status
	s.Current = c.current
	return s, nil
}

// Run checks daily (Ergebnis landet im Status; die UI pollt).
func (c *Checker) Run(ctx context.Context) {
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		c.Check(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Apply validates the tag against the last check and signals the host.
func (c *Checker) Apply(tag string) error {
	if !tagRe.MatchString(tag) {
		return fmt.Errorf("ungültiger Tag %q", tag)
	}
	s := c.Status()
	if s.Latest == "" {
		return fmt.Errorf("noch kein Versions-Check gelaufen")
	}
	if tag != s.Latest {
		return fmt.Errorf("tag %q ist nicht der geprüfte neueste Stand %q", tag, s.Latest)
	}
	if !s.Newer {
		return fmt.Errorf("version %s läuft bereits", s.Current)
	}
	return c.signal.RequestSelfUpdate(tag)
}

// latestTag returns the highest semver tag (vX.Y.Z); non-semver tags are
// ignored — Releases MÜSSEN semver-getaggt sein, sonst ist "neuester" nicht
// entscheidbar (die GitHub-API sortiert Tags nicht chronologisch).
func (c *Checker) latestTag(ctx context.Context) (string, error) {
	url := fmt.Sprintf("%s/repos/%s/tags?per_page=100", c.apiBase, c.repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("repo %s nicht erreichbar (privat? MSM_GITHUB_TOKEN setzen)", c.repo)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return "", fmt.Errorf("github tags: %s: %s", resp.Status, b)
	}
	var tags []struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return "", err
	}
	best := ""
	for _, t := range tags {
		if _, ok := parseSemver(t.Name); !ok {
			continue
		}
		if best == "" || newerOrEqual(t.Name, best) {
			best = t.Name
		}
	}
	if best == "" && len(tags) > 0 {
		return "", fmt.Errorf("keine semver-Tags (vX.Y.Z) gefunden — Releases bitte so taggen")
	}
	return best, nil
}

// parseSemver akzeptiert vX, vX.Y, vX.Y.Z (mit oder ohne v).
func parseSemver(tag string) ([3]int, bool) {
	tag = strings.TrimPrefix(tag, "v")
	// Suffixe wie -rc1 abschneiden — zählt als Vorabversion derselben Nummer
	if i := strings.IndexAny(tag, "-+"); i >= 0 {
		tag = tag[:i]
	}
	parts := strings.Split(tag, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

// sameVersion: exakter Vergleich, tolerant gegenüber v-Präfix.
func sameVersion(a, b string) bool {
	return strings.TrimPrefix(a, "v") == strings.TrimPrefix(b, "v")
}

// newerOrEqual reports whether a >= b (semver). Nicht-semver a (dev-Builds,
// git-Hashes) gilt als älter — ein Update auf einen echten Tag ist dann
// immer erlaubt.
func newerOrEqual(a, b string) bool {
	av, aok := parseSemver(a)
	bv, bok := parseSemver(b)
	if !aok {
		return false
	}
	if !bok {
		return true
	}
	for i := 0; i < 3; i++ {
		if av[i] != bv[i] {
			return av[i] > bv[i]
		}
	}
	return true
}
