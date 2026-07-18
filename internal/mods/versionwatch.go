package mods

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// WatchStatus answers "lohnt sich der Umstieg schon?" (concept chapter on
// Versions-Watch): newest MC release, Fabric loader support and per-mod
// Modrinth readiness for both profiles.
type WatchStatus struct {
	Checked        time.Time         `json:"checked"`
	CurrentVersion string            `json:"currentVersion"`
	LatestVersion  string            `json:"latestVersion"`
	NewerAvailable bool              `json:"newerAvailable"`
	LoaderReady    bool              `json:"loaderReady"`
	Profiles       []ProfileReady    `json:"profiles"`
	Stragglers     map[string]string `json:"stragglers"` // mod name -> profile
}

type ProfileReady struct {
	Profile string `json:"profile"`
	Ready   int    `json:"ready"`
	Total   int    `json:"total"` // managed entries only
}

// Watcher performs the daily readiness check.
type Watcher struct {
	api      ModrinthAPI
	mgr      *Manager
	loader   string
	http     *http.Client
	manifest string // mojang piston-meta URL (overridable in tests)
	fabric   string // fabric meta URL

	mu   sync.Mutex
	last *WatchStatus
}

func NewWatcher(api ModrinthAPI, mgr *Manager, loader string) *Watcher {
	return &Watcher{
		api: api, mgr: mgr, loader: loader,
		http:     &http.Client{Timeout: 30 * time.Second},
		manifest: "https://piston-meta.mojang.com/mc/game/version_manifest_v2.json",
		fabric:   "https://meta.fabricmc.net/v2/versions/game",
	}
}

// SetEndpoints overrides the upstream URLs (tests).
func (w *Watcher) SetEndpoints(manifest, fabric string) {
	w.manifest, w.fabric = manifest, fabric
}

// Last returns the most recent check result (nil before the first run).
func (w *Watcher) Last() *WatchStatus {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.last
}

// SetLast stores a manually triggered check result.
func (w *Watcher) SetLast(s *WatchStatus) {
	w.mu.Lock()
	w.last = s
	w.mu.Unlock()
}

// Run performs checks daily until ctx is done; one check runs at start.
func (w *Watcher) Run(ctx context.Context, currentVersion func() string) {
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		if status, err := w.Check(ctx, currentVersion()); err == nil {
			w.mu.Lock()
			w.last = status
			w.mu.Unlock()
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (w *Watcher) getJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := w.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Check runs one readiness evaluation against the given current version.
func (w *Watcher) Check(ctx context.Context, currentVersion string) (*WatchStatus, error) {
	status := &WatchStatus{
		Checked:        time.Now(),
		CurrentVersion: currentVersion,
		Stragglers:     map[string]string{},
	}

	// 1. newest Mojang release
	var manifest struct {
		Latest struct {
			Release string `json:"release"`
		} `json:"latest"`
	}
	if err := w.getJSON(ctx, w.manifest, &manifest); err != nil {
		return nil, fmt.Errorf("mojang manifest: %w", err)
	}
	status.LatestVersion = manifest.Latest.Release
	status.NewerAvailable = manifest.Latest.Release != "" && manifest.Latest.Release != currentVersion
	if !status.NewerAvailable {
		return status, nil
	}

	// 2. fabric loader support
	var games []struct {
		Version string `json:"version"`
		Stable  bool   `json:"stable"`
	}
	if err := w.getJSON(ctx, w.fabric, &games); err != nil {
		return nil, fmt.Errorf("fabric meta: %w", err)
	}
	for _, g := range games {
		if g.Version == status.LatestVersion {
			status.LoaderReady = true
			break
		}
	}

	// 3. per-mod readiness for both profiles (throttled by the API client)
	for _, p := range w.mgr.Profiles() {
		entries := w.mgr.Entries(p.Name)
		pr := ProfileReady{Profile: p.Name}
		for _, e := range entries {
			if !e.Managed {
				continue
			}
			pr.Total++
			ok, err := w.api.ProjectSupports(ctx, e.ProjectID, w.loader, status.LatestVersion)
			if err != nil {
				continue // einzelner Fehler darf den Check nicht kippen
			}
			if ok {
				pr.Ready++
			} else {
				name := e.Name
				if name == "" {
					name = e.Filename
				}
				status.Stragglers[name] = p.Name
			}
		}
		status.Profiles = append(status.Profiles, pr)
	}
	return status, nil
}
