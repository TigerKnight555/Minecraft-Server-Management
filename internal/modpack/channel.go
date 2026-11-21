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

const (
	stateKey  = "modpack.state"
	statusKey = "modpack.status" // weggelassene Dateien + Projekt-Link des letzten Uploads
)

// savedStatus keeps the dashboard view of the last upload across restarts.
type savedStatus struct {
	Omitted    []string `json:"omitted,omitempty"`
	ProjectURL string   `json:"projectUrl,omitempty"`
}

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

// Fingerprint implements packpub.Fingerprinter: der Modrinth-Kanal ist erst
// aktuell, wenn auch MC- und Loader-Version zum letzten Upload passen. Ist
// der Server offline (MC-Version unbekannt), gilt der zuletzt veröffentlichte
// Stand — sonst liefe jeder Versand in den Fehler „Minecraft-Version
// unbekannt".
func (c *Channel) Fingerprint() string {
	prev := c.loadState()
	mc := c.mcVersion()
	if mc == "" {
		return prev.MC + "|" + prev.Loader
	}
	loader := DetectLoader(c.dataDir, mc)
	if loader == "" && prev.MC == mc {
		loader = prev.Loader
	}
	return mc + "|" + loader
}

// Status returns the last published state for the dashboard.
func (c *Channel) Status() Status {
	s := c.loadState()
	c.mu.Lock()
	omitted, url := append([]string(nil), c.omitted...), c.projectURL
	lastErr := c.lastErr
	c.mu.Unlock()
	if url == "" { // nach einem Neustart: gespeicherten Stand zeigen
		var saved savedStatus
		if raw := c.get(statusKey); raw != "" && json.Unmarshal([]byte(raw), &saved) == nil {
			omitted, url = saved.Omitted, saved.ProjectURL
		}
	}
	return Status{Ready: c.Ready(), Version: s.Version, MC: s.MC, Loader: s.Loader,
		Omitted: omitted, LastError: lastErr, ProjectURL: url}
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
		case http.StatusUnauthorized, http.StatusForbidden:
			return fmt.Errorf("Modrinth lehnt den Token ab (%d) — Token gültig und mit den Rechten „Create versions“, „Read projects“, „Read versions“ und „Read user data“? (%w)", apiErr.Status, err)
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
			// ohne Liste zählt nur der eigene Stand weiter — Recht „Read versions"?
			c.log.Warn("modrinth-versionsliste nicht lesbar", "project", cfg.Project, "err", explain(err, cfg.Project))
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
		// Modrinth prüft Versionsnummern nicht auf Eindeutigkeit — ein 400
		// hat also meist andere Gründe. Der eine Wiederholungsversuch mit
		// neu gelesener höchster Nummer hilft nur, falls die Ablehnung doch
		// an der Nummer lag; sonst scheitert er genauso und der Fehler wird
		// gemeldet.
		if h := Highest([]string{highest(), cur.Version}); h != "" {
			base := prev
			if prev.Version == "" {
				// Erstveröffentlichung: keine Sprungart vorhanden, also Patch-Schritt auf h
				base = State{Version: h, MC: mc, Loader: loader, Files: entries}
			}
			cur.Version = Next(base, cur, cfg.StartVersion, h)
			err = upload(cur.Version)
		}
	}
	if err != nil {
		return c.fail(explain(err, cfg.Project))
	}

	raw, _ := json.Marshal(cur)
	c.set(stateKey, string(raw))
	rawStatus, _ := json.Marshal(savedStatus{Omitted: omitted, ProjectURL: projectURL(proj)})
	c.set(statusKey, string(rawStatus))
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
