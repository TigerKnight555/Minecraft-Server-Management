// Package packpub publishes the client pack over all configured channels
// (Dropbox-ZIP, Modrinth-Modpack). Je Kanal merkt es sich den Fingerabdruck
// des zuletzt erfolgreich verschickten Inhalts: ein Kanal mit aktuellem
// Stand wird übersprungen, ein gescheiterter beim nächsten Lauf nachgeholt.
// Erfolgreiche Kanäle ergeben EINE gemeinsame 📦-Meldung, Fehler je Kanal
// eine Admin-Meldung.
package packpub

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
)

var (
	// ErrBusy: ein Paket-Versand läuft bereits.
	ErrBusy = errors.New("client-Paket wird gerade schon verschickt")
	// ErrNotReady: kein ausgewählter Kanal ist eingerichtet.
	ErrNotReady = errors.New("kein Versandweg eingerichtet — Dropbox oder Modrinth im Einstellungen-Tab hinterlegen")
)

// Outcome is what a successful channel contributes to the player message.
// Keine Zeilen = Kanal wird nicht erwähnt (z. B. Modrinth-Projekt noch nicht
// öffentlich).
type Outcome struct {
	Lines  []string
	Fields []events.Field
}

// Channel is one way to distribute the client pack.
type Channel interface {
	Name() string  // "dropbox" | "modrinth"
	Label() string // für Meldungen
	Ready() bool
	Publish(ctx context.Context) (Outcome, error)
}

type Publisher struct {
	channels    []Channel
	fingerprint func() string
	get         func(string) string
	set         func(string, string)
	bus         *events.Bus
	log         *slog.Logger
	busy        atomic.Bool
}

func New(fingerprint func() string, get func(string) string, set func(string, string), bus *events.Bus, log *slog.Logger, channels ...Channel) *Publisher {
	return &Publisher{channels: channels, fingerprint: fingerprint, get: get, set: set, bus: bus, log: log}
}

// Busy reports whether a publish is running.
func (p *Publisher) Busy() bool { return p.busy.Load() }

// Ready reports whether the named channel ("" = irgendeiner) is configured.
func (p *Publisher) Ready(name string) bool {
	for _, ch := range p.channels {
		if (name == "" || ch.Name() == name) && ch.Ready() {
			return true
		}
	}
	return false
}

func fpKey(name string) string { return "clientpack.published." + name }

// Fingerprinter is optional for a Channel: hängt sein Stand nicht nur am
// Inhalt des Client-Pakets (Modrinth: MC- und Loader-Version), fließt
// Fingerprint() in den gemerkten Stand dieses Kanals ein.
type Fingerprinter interface {
	Fingerprint() string
}

// stamp is what a channel stores after a successful publish of content fp.
func stamp(ch Channel, fp string) string {
	if f, ok := ch.(Fingerprinter); ok {
		return fp + "|" + f.Fingerprint()
	}
	return fp
}

// Current reports whether every configured channel already has the current
// content. Ohne Fingerabdruck oder ohne eingerichteten Kanal: false.
func (p *Publisher) Current() bool {
	if p.fingerprint == nil {
		return false
	}
	fp := p.fingerprint()
	if fp == "" {
		return false
	}
	ready := 0
	for _, ch := range p.channels {
		if !ch.Ready() {
			continue
		}
		ready++
		if p.get(fpKey(ch.Name())) != stamp(ch, fp) {
			return false
		}
	}
	return ready > 0
}

func selected(names []string, name string) bool {
	if len(names) == 0 {
		return true
	}
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

// Run publishes over the given channels (nil = alle eingerichteten).
// force=false überspringt Kanäle, deren letzter erfolgreicher Versand
// denselben Inhalt hatte. Fehler: je gescheitertem Kanal, gebündelt.
func (p *Publisher) Run(ctx context.Context, names []string, force bool) error {
	_, err := p.RunReport(ctx, names, force)
	return err
}

// RunReport is Run and additionally reports whether a „📦 Neues
// Mod-Paket"-Meldung an die Spieler ging.
func (p *Publisher) RunReport(ctx context.Context, names []string, force bool) (announced bool, err error) {
	if !p.busy.CompareAndSwap(false, true) {
		return false, ErrBusy
	}
	defer p.busy.Store(false)
	fp := ""
	if p.fingerprint != nil {
		fp = p.fingerprint()
	}
	var lines []string
	var fields []events.Field
	var errs []error
	ran := 0
	for _, ch := range p.channels {
		if !selected(names, ch.Name()) || !ch.Ready() {
			continue
		}
		ran++
		want := stamp(ch, fp)
		if !force && fp != "" && p.get(fpKey(ch.Name())) == want {
			continue // dieser Kanal hat den Inhalt schon
		}
		out, err := ch.Publish(ctx)
		if err != nil {
			p.log.Error("client-pack publish failed", "channel", ch.Name(), "err", err)
			p.bus.Publish(events.Event{
				Type: events.TypeClientPack, Severity: events.SevError,
				Title:   "⚠️ Mod-Paket-Upload fehlgeschlagen (" + ch.Label() + ")",
				Message: "Info für den Admin — Details unten.",
				Fields:  []events.Field{{Name: "Details", Value: err.Error()}},
			})
			errs = append(errs, fmt.Errorf("%s: %w", ch.Label(), err))
			continue
		}
		if fp != "" {
			p.set(fpKey(ch.Name()), want)
		}
		lines = append(lines, out.Lines...)
		fields = append(fields, out.Fields...)
	}
	if ran == 0 {
		return false, ErrNotReady
	}
	if len(lines) > 0 {
		p.bus.Publish(events.Event{
			Type: events.TypeClientPack, Severity: events.SevSuccess,
			Title:   "📦 Neues Mod-Paket!",
			Message: strings.Join(lines, "\n\n"),
			Fields:  fields,
		})
	}
	return len(lines) > 0, errors.Join(errs...)
}
