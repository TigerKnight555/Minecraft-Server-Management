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
	keyPackFP = "mods.clientpack.fingerprint"
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
	// fingerprint beschreibt den aktuellen Paket-Inhalt (nil = unbekannt)
	fingerprint func() string
	// pendingServer liefert die Schlüssel (Projekt-ID, sonst Name) der am
	// Server noch vorgemerkten Änderungen
	pendingServer func() map[string]bool

	// Poll: Abstand der Online-Prüfung
	Poll time.Duration

	mu  sync.Mutex
	gen uint64 // zählt Client-Änderungen (nur im Speicher) — siehe Published
}

func New(get func(string) string, set func(string, string), bus *events.Bus, online func() bool, log *slog.Logger) *Announcer {
	return &Announcer{get: get, set: set, bus: bus, online: online, log: log, Poll: 5 * time.Second}
}

// SetBusy: solange f true liefert (Ein-Klick-Update), bleibt der Announcer
// still — das Update verschickt sein Paket selbst.
func (a *Announcer) SetBusy(f func() bool) { a.busy = f }

// SetPublisher wires the client pack upload; publish muss bei Erfolg
// BeginPublish/Published auslösen (in main über publishPack erledigt).
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
		a.gen++
		a.set(keyDirty, "1") // jede Client-Änderung (auch Updates/Rollback) macht das Paket alt
		if len(news) > 0 {
			a.save(keyClient, merge(a.load(keyClient), news))
		}
	case mods.ProfileServer:
		if len(news) > 0 {
			a.save(keyServer, merge(a.load(keyServer), news))
		}
	}
}

// merge appends news to the pending list; eine Gegenänderung an derselben
// Datei (Hinzufügen ↔ Entfernen, z. B. durch Rollback) hebt einen noch
// nicht angekündigten Eintrag auf, statt beide zu melden. Sonst kündigte
// der nächste Neustart längst zurückgerollte Mods als neu an
// (WSL-Test 2026-11-10).
func merge(list, news []mods.Change) []mods.Change {
	for _, n := range news {
		cancelled := false
		for i := len(list) - 1; i >= 0; i-- {
			if n.Filename != "" && list[i].Filename == n.Filename && list[i].Action != n.Action {
				list = append(list[:i], list[i+1:]...)
				cancelled = true
				break
			}
		}
		if !cancelled {
			list = append(list, n)
		}
	}
	return list
}

// SetFingerprint wires a content fingerprint of the client pack. Damit
// erkennt AfterRestart, dass ein „veraltetes" Paket inhaltlich doch dem
// zuletzt verschickten entspricht (z. B. Upload + Rollback) — sonst bekamen
// die Spieler ein „📦 Neues Mod-Paket" ohne jede Änderung (WSL-Test 2026-11-10).
func (a *Announcer) SetFingerprint(f func() string) { a.fingerprint = f }

// SetPendingServer wires the server's still-staged changes. Client-Teile
// eines Mods, dessen Server-Teil noch vorgemerkt ist, werden bis zu dessen
// Neustart zurückgehalten — sonst kam Waystones (beide Seiten) erst als
// „nur bei dir (Client-Paket)" und später als „nur Server, nichts zu tun"
// (WSL-Test 2026-11-10, Neustart durch eine Routine ohne Einspielen).
func (a *Announcer) SetPendingServer(f func() map[string]bool) { a.pendingServer = f }

func changeKey(c mods.Change) string {
	if c.ProjectID != "" {
		return c.ProjectID
	}
	return c.Name
}

// BeginPublish returns the current client-change generation; vor dem
// Paket-Upload aufrufen und das Ergebnis an Published weitergeben.
func (a *Announcer) BeginPublish() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.gen
}

// Published clears the "pack outdated" flag after a successful upload
// (Button, Update, Neustart) — aber nur, wenn seit BeginPublish keine
// Client-Änderung dazukam (sonst fehlt sie im hochgeladenen Paket). Die
// gesammelte Änderungsliste bleibt stehen, bis AfterRestart sie meldet
// (Nutzer-Entscheidung: Button-Versand verbraucht die Liste nicht).
func (a *Announcer) Published(gen uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.gen == gen {
		a.set(keyDirty, "")
		if a.fingerprint != nil {
			a.set(keyPackFP, a.fingerprint())
		}
	}
}

// AfterRestart posts the mod list (if anything was added/removed) and
// publishes the client pack when it is outdated.
func (a *Announcer) AfterRestart(ctx context.Context) {
	var pending map[string]bool
	if a.pendingServer != nil {
		pending = a.pendingServer()
	}
	a.mu.Lock()
	server, all := a.load(keyServer), a.load(keyClient)
	var client, held []mods.Change
	for _, c := range all {
		if pending[changeKey(c)] {
			held = append(held, c)
		} else {
			client = append(client, c)
		}
	}
	dirty := a.get(keyDirty) == "1"
	if dirty && a.fingerprint != nil {
		if fp := a.get(keyPackFP); fp != "" && fp == a.fingerprint() {
			dirty = false // Inhalt wie zuletzt verschickt
			a.set(keyDirty, "")
		}
	}
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
		a.save(keyClient, held)
		a.mu.Unlock()
	}
	if canPublish {
		pctx, cancel := context.WithTimeout(ctx, 45*time.Minute)
		defer cancel()
		if err := a.publish(pctx); err != nil {
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
	clientNeeded   bool // Modrinth: client_side required
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
		if c.ClientSide == "required" {
			r.clientNeeded = true
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
			case r.server && (r.client || r.clientNeeded):
				// auch wenn der Client-Teil schon früher verteilt wurde:
				// die Spieler brauchen den Mod
				hint = both
			case r.client:
				hint = clientOnly
			}
			b.WriteString("• " + r.name + " — " + hint + "\n")
		}
	}
	write("**Neu:**", added, "auch bei dir nötig", "nur Server, nichts zu tun", "nur bei dir (Client-Paket)")
	write("**Entfernt:**", removed, "bitte auch bei dir entfernen", "nichts zu tun", "kann bei dir raus")
	onServer := len(server) > 0
	switch {
	case len(added) > 0 && onServer:
		title = "🧩 Neue Mods auf dem Server"
	case len(added) > 0:
		title = "🧩 Neu im Mod-Paket"
	case onServer:
		title = "🧩 Mods vom Server entfernt"
	default:
		title = "🧩 Aus dem Mod-Paket entfernt"
	}
	return strings.TrimRight(b.String(), "\n"), title, true
}
