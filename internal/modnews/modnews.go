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
			a.save(keyClient, append(a.load(keyClient), news...))
		}
	case mods.ProfileServer:
		if len(news) > 0 {
			a.save(keyServer, append(a.load(keyServer), news...))
		}
	}
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
	}
}

// AfterRestart posts the mod list (if anything was added/removed) and
// publishes the client pack when it is outdated.
func (a *Announcer) AfterRestart(ctx context.Context) {
	a.mu.Lock()
	server, client := a.load(keyServer), a.load(keyClient)
	dirty := a.get(keyDirty) == "1"
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
		a.save(keyClient, nil)
		a.mu.Unlock()
	}
	if canPublish {
		if err := a.publish(ctx); err != nil {
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
			case r.server && r.client:
				hint = both
			case r.client:
				hint = clientOnly
			}
			b.WriteString("• " + r.name + " — " + hint + "\n")
		}
	}
	write("**Neu:**", added, "auch bei dir nötig", "nur Server, nichts zu tun", "nur bei dir (Client-Mod)")
	write("**Entfernt:**", removed, "bitte auch bei dir entfernen", "nichts zu tun", "kann bei dir raus")
	title = "🧩 Neue Mods auf dem Server"
	if len(added) == 0 {
		title = "🧩 Mods vom Server entfernt"
	}
	return strings.TrimRight(b.String(), "\n"), title, true
}
