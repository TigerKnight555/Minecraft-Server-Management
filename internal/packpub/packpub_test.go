package packpub

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
)

type fakeChannel struct {
	name  string
	ready bool
	err   error
	calls int
	lines []string
	block chan struct{}
}

func (f *fakeChannel) Name() string  { return f.name }
func (f *fakeChannel) Label() string { return strings.ToUpper(f.name) }
func (f *fakeChannel) Ready() bool   { return f.ready }
func (f *fakeChannel) Publish(ctx context.Context) (Outcome, error) {
	f.calls++
	if f.block != nil {
		<-f.block
	}
	if f.err != nil {
		return Outcome{}, f.err
	}
	return Outcome{Lines: f.lines, Fields: []events.Field{{Name: f.name, Value: "ok"}}}, nil
}

type kv struct {
	mu sync.Mutex
	m  map[string]string
}

func (k *kv) get(key string) string { k.mu.Lock(); defer k.mu.Unlock(); return k.m[key] }
func (k *kv) set(key, v string)     { k.mu.Lock(); defer k.mu.Unlock(); k.m[key] = v }

func setup(fp *string, chans ...Channel) (*Publisher, <-chan events.Event, *kv) {
	store := &kv{m: map[string]string{}}
	bus := events.New()
	ch, _ := bus.Subscribe(16)
	p := New(func() string { return *fp }, store.get, store.set, bus, slog.New(slog.NewTextHandler(io.Discard, nil)), chans...)
	return p, ch, store
}

func drain(ch <-chan events.Event) []events.Event {
	var out []events.Event
	for {
		select {
		case ev := <-ch:
			out = append(out, ev)
		case <-time.After(50 * time.Millisecond):
			return out
		}
	}
}

func TestRunPostsOneCombinedMessage(t *testing.T) {
	fp := "A"
	dbx := &fakeChannel{name: "dropbox", ready: true, lines: []string{"Download: x"}}
	mr := &fakeChannel{name: "modrinth", ready: true, lines: []string{"Modrinth App: Update"}}
	p, ch, _ := setup(&fp, dbx, mr)
	if err := p.Run(context.Background(), nil, false); err != nil {
		t.Fatal(err)
	}
	evs := drain(ch)
	if len(evs) != 1 || evs[0].Type != events.TypeClientPack || !strings.Contains(evs[0].Message, "Download: x") || !strings.Contains(evs[0].Message, "Modrinth App") {
		t.Fatalf("events = %+v, want eine gemeinsame Meldung", evs)
	}
}

func TestFailedChannelIsRetriedOthersSkipped(t *testing.T) {
	fp := "A"
	dbx := &fakeChannel{name: "dropbox", ready: true, lines: []string{"Download"}}
	mr := &fakeChannel{name: "modrinth", ready: true, lines: []string{"Modrinth"}, err: errors.New("offline")}
	p, ch, _ := setup(&fp, dbx, mr)
	err := p.Run(context.Background(), nil, false)
	if err == nil || !strings.Contains(err.Error(), "MODRINTH") {
		t.Fatalf("err = %v, want Fehler des Modrinth-Kanals", err)
	}
	evs := drain(ch)
	if len(evs) != 2 { // Admin-Fehler + Erfolgsmeldung (nur Dropbox)
		t.Fatalf("events = %+v", evs)
	}
	// nächster Lauf, gleicher Inhalt: Dropbox schon aktuell, nur Modrinth erneut
	mr.err = nil
	if err := p.Run(context.Background(), nil, false); err != nil {
		t.Fatal(err)
	}
	if dbx.calls != 1 || mr.calls != 2 {
		t.Errorf("calls dropbox=%d modrinth=%d, want 1/2", dbx.calls, mr.calls)
	}
	// force (Knopf) verschickt trotz gleichem Inhalt
	if err := p.Run(context.Background(), []string{"dropbox"}, true); err != nil {
		t.Fatal(err)
	}
	if dbx.calls != 2 || mr.calls != 2 {
		t.Errorf("force: calls dropbox=%d modrinth=%d, want 2/2", dbx.calls, mr.calls)
	}
}

func TestNotReadyAndBusy(t *testing.T) {
	fp := "A"
	off := &fakeChannel{name: "dropbox", ready: false}
	p, _, _ := setup(&fp, off)
	if err := p.Run(context.Background(), nil, false); !errors.Is(err, ErrNotReady) {
		t.Fatalf("err = %v, want ErrNotReady", err)
	}
	if p.Ready("") || p.Ready("dropbox") {
		t.Error("Ready ohne eingerichteten Kanal")
	}

	slow := &fakeChannel{name: "modrinth", ready: true, block: make(chan struct{})}
	p2, _, _ := setup(&fp, slow)
	done := make(chan error)
	go func() { done <- p2.Run(context.Background(), nil, true) }()
	deadline := time.Now().Add(2 * time.Second)
	for !p2.Busy() {
		if time.Now().After(deadline) {
			t.Fatal("Run wurde nie busy")
		}
		time.Sleep(time.Millisecond)
	}
	if err := p2.Run(context.Background(), nil, true); !errors.Is(err, ErrBusy) {
		t.Errorf("zweiter Lauf: err = %v, want ErrBusy", err)
	}
	close(slow.block)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestChannelWithoutLinesStaysQuiet(t *testing.T) {
	fp := "A"
	draft := &fakeChannel{name: "modrinth", ready: true} // Projekt nicht öffentlich: keine Zeilen
	p, ch, _ := setup(&fp, draft)
	if err := p.Run(context.Background(), nil, false); err != nil {
		t.Fatal(err)
	}
	if evs := drain(ch); len(evs) != 0 {
		t.Errorf("events = %+v, want keine Spieler-Meldung", evs)
	}
}

// Ein Lauf nur für einen Kanal (Knopf) macht das Paket noch nicht „aktuell",
// solange der andere Kanal den Stand nicht hat.
func TestCurrentNeedsEveryReadyChannel(t *testing.T) {
	fp := "A"
	dbx := &fakeChannel{name: "dropbox", ready: true, lines: []string{"Download"}}
	mr := &fakeChannel{name: "modrinth", ready: true, lines: []string{"Modrinth"}}
	p, _, _ := setup(&fp, dbx, mr)
	if p.Current() {
		t.Fatal("Current vor jedem Versand")
	}
	if err := p.Run(context.Background(), []string{"dropbox"}, true); err != nil {
		t.Fatal(err)
	}
	if p.Current() {
		t.Error("Current nach Einzelkanal-Lauf, obwohl Modrinth fehlt")
	}
	if err := p.Run(context.Background(), []string{"modrinth"}, false); err != nil {
		t.Fatal(err)
	}
	if !p.Current() {
		t.Error("Current = false, obwohl beide Kanäle den Stand haben")
	}
	fp = "B" // Inhalt geändert
	if p.Current() {
		t.Error("Current trotz geändertem Inhalt")
	}
	fp = ""
	if p.Current() {
		t.Error("Current ohne Fingerabdruck")
	}
}

func TestCurrentFalseAfterFailedChannel(t *testing.T) {
	fp := "A"
	dbx := &fakeChannel{name: "dropbox", ready: true, lines: []string{"Download"}}
	mr := &fakeChannel{name: "modrinth", ready: true, err: errors.New("offline")}
	p, _, _ := setup(&fp, dbx, mr)
	if err := p.Run(context.Background(), nil, false); err == nil {
		t.Fatal("Fehler des Modrinth-Kanals fehlt")
	}
	if p.Current() {
		t.Error("Current trotz gescheitertem Kanal")
	}
}

// fpChannel hat einen eigenen Fingerabdruck (z. B. MC-/Loader-Version).
type fpChannel struct {
	fakeChannel
	own string
}

func (f *fpChannel) Fingerprint() string { return f.own }

func TestFingerprinterRerunsOnOwnChange(t *testing.T) {
	fp := "A"
	dbx := &fakeChannel{name: "dropbox", ready: true, lines: []string{"Download"}}
	mr := &fpChannel{fakeChannel: fakeChannel{name: "modrinth", ready: true, lines: []string{"Modrinth"}}, own: "26.2|0.19"}
	p, ch, _ := setup(&fp, dbx, mr)
	if err := p.Run(context.Background(), nil, false); err != nil {
		t.Fatal(err)
	}
	drain(ch)
	// gleiche Dateien, aber neue MC-Version: nur Modrinth läuft erneut
	mr.own = "26.3|0.20"
	if !p.Ready("") || p.Current() {
		t.Error("Current trotz geändertem Kanal-Fingerabdruck")
	}
	announced, err := p.RunReport(context.Background(), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if dbx.calls != 1 || mr.calls != 2 {
		t.Errorf("calls dropbox=%d modrinth=%d, want 1/2", dbx.calls, mr.calls)
	}
	if !announced {
		t.Error("announced = false, obwohl Modrinth eine Meldung beigetragen hat")
	}
	if !p.Current() {
		t.Error("Current = false nach erfolgreichem Lauf")
	}
	// nichts geändert: kein Kanal läuft, keine Meldung
	announced, err = p.RunReport(context.Background(), nil, false)
	if err != nil || announced {
		t.Errorf("RunReport = %v, %v; want false, nil (alles aktuell)", announced, err)
	}
	if evs := drain(ch); len(evs) != 1 {
		t.Errorf("events = %+v, want genau eine 📦-Meldung", evs)
	}
}
