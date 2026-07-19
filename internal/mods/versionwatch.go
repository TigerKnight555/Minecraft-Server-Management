package mods

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
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
	bus  *events.Bus // optional; nil bus is a safe no-op

	// optionaler Ankündigungs-Speicher: ohne ihn vergisst der Watcher bei
	// jedem MSM-Neustart (= jedem Nacht-Reboot), was er schon gemeldet hat,
	// und wiederholt version.new/version.ready täglich (Learning 13)
	annGet func(key string) string
	annSet func(key, value string)
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

// SetBus wires the event bus; version transitions are published there.
func (w *Watcher) SetBus(b *events.Bus) { w.bus = b }

// SetAnnounceStore wires persistence for "schon angekündigt"-marks.
func (w *Watcher) SetAnnounceStore(get func(key string) string, set func(key, value string)) {
	w.annGet, w.annSet = get, set
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
	prev := w.last
	w.last = s
	w.mu.Unlock()
	w.notifyTransitions(prev, s)
}

// Run performs checks daily until ctx is done; one check runs at start.
func (w *Watcher) Run(ctx context.Context, currentVersion func() string) {
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		if status, err := w.Check(ctx, currentVersion()); err == nil {
			w.SetLast(status)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// allReady means: loader supports the release and every managed mod of every
// profile has a matching Modrinth version. Zero known mods everywhere means
// the inventory was never scanned (empty cache) — that is "unknown", not
// "ready", so no premature all-clear right after startup.
func allReady(s *WatchStatus) bool {
	if s == nil || !s.NewerAvailable || !s.LoaderReady {
		return false
	}
	total := 0
	for _, p := range s.Profiles {
		if p.Ready < p.Total {
			return false
		}
		total += p.Total
	}
	return total > 0
}

// notifyTransitions publishes only state CHANGES, so the daily check does not
// repeat the same news: a release seen for the first time, and the moment a
// known release becomes fully ready. Mit Announce-Store überleben die Marken
// den MSM-Neustart; ohne (Tests) gilt der In-Memory-Vergleich.
func (w *Watcher) notifyTransitions(prev, cur *WatchStatus) {
	if cur == nil || !cur.NewerAvailable {
		return
	}
	newRelease := prev == nil || prev.LatestVersion != cur.LatestVersion
	if w.annGet != nil {
		newRelease = w.annGet("version.announced") != cur.LatestVersion
	}
	if newRelease {
		if w.annSet != nil {
			w.annSet("version.announced", cur.LatestVersion)
		}
		ready := "nein"
		if cur.LoaderReady {
			ready = "ja"
		}
		fields := []events.Field{{Name: "Fabric-Loader bereit", Value: ready}}
		for _, p := range cur.Profiles {
			fields = append(fields, events.Field{
				Name: "Mods " + p.Profile, Value: fmt.Sprintf("%d/%d bereit", p.Ready, p.Total),
			})
		}
		w.bus.Publish(events.Event{
			Type: events.TypeVersionNew, Severity: events.SevInfo,
			Title:   "Neue Minecraft-Version: " + cur.LatestVersion,
			Message: "Aktuell installiert: " + cur.CurrentVersion,
			Fields:  fields,
		})
	}
	readyNew := allReady(cur) && (newRelease || !allReady(prev))
	if w.annGet != nil {
		readyNew = allReady(cur) && w.annGet("version.ready.announced") != cur.LatestVersion
	}
	if readyNew {
		if w.annSet != nil {
			w.annSet("version.ready.announced", cur.LatestVersion)
		}
		w.bus.Publish(events.Event{
			Type: events.TypeVersionReady, Severity: events.SevSuccess,
			Title:   "Minecraft " + cur.LatestVersion + " — alles bereit für den Umstieg",
			Message: "Fabric-Loader und alle verwalteten Mods beider Profile unterstützen die neue Version.",
		})
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
	if currentVersion == "" {
		// ohne bekannte Version wäre jedes Ergebnis irreführend
		return nil, fmt.Errorf("aktuelle Minecraft-Version unbekannt — Check übersprungen")
	}
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
