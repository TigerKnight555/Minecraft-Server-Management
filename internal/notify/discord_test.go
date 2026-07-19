package notify

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// capture is a fake Discord endpoint recording every payload.
type capture struct {
	mu       sync.Mutex
	payloads []webhookPayload
	fails    int // number of requests to reject before succeeding
}

func (c *capture) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.fails > 0 {
			c.fails--
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var p webhookPayload
		json.Unmarshal(body, &p)
		c.payloads = append(c.payloads, p)
		w.WriteHeader(http.StatusNoContent)
	}
}

func (c *capture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.payloads)
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not reached in time")
}

func TestDeliversMatchingEventsAsEmbeds(t *testing.T) {
	cap := &capture{}
	srv := httptest.NewServer(cap.handler())
	defer srv.Close()

	bus := events.New()
	ch, cancel := bus.Subscribe(16)
	defer cancel()
	d := NewDiscordStatic([]Webhook{{Name: "t", URL: srv.URL, Events: []string{"mods."}}}, testLogger())
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go d.Run(ctx, ch)

	bus.Publish(events.Event{Type: events.TypeRoutineOK, Title: "ignored"})
	bus.Publish(events.Event{
		Type: events.TypeModsApplied, Severity: events.SevSuccess,
		Title: "Mods eingespielt", Message: "2 Updates",
		Fields: []events.Field{{Name: "Profil", Value: "server"}},
	})

	waitFor(t, func() bool { return cap.count() == 1 })
	p := cap.payloads[0]
	if len(p.Embeds) != 1 {
		t.Fatalf("embeds = %d, want 1", len(p.Embeds))
	}
	e := p.Embeds[0]
	if e.Title != "Mods eingespielt" || e.Color != colors[events.SevSuccess] {
		t.Errorf("embed = %+v", e)
	}
	if len(e.Fields) != 1 || e.Fields[0].Value != "server" {
		t.Errorf("fields = %+v", e.Fields)
	}
	if e.Timestamp == "" {
		t.Error("timestamp missing")
	}
}

func TestRetriesOnServerError(t *testing.T) {
	cap := &capture{fails: 2}
	srv := httptest.NewServer(cap.handler())
	defer srv.Close()

	d := NewDiscordStatic(nil, testLogger())
	err := d.send(context.Background(), srv.URL, events.Event{
		Type: events.TypeRoutineFailed, Title: "x", Time: time.Now(),
	})
	if err != nil {
		t.Fatalf("send after retries failed: %v", err)
	}
	if cap.count() != 1 {
		t.Errorf("delivered = %d, want 1", cap.count())
	}
}

func TestGivesUpAfterThreeAttempts(t *testing.T) {
	cap := &capture{fails: 99}
	srv := httptest.NewServer(cap.handler())
	defer srv.Close()

	d := NewDiscordStatic(nil, testLogger())
	err := d.send(context.Background(), srv.URL, events.Event{Title: "x", Time: time.Now()})
	if err == nil {
		t.Fatal("send succeeded, want error")
	}
}

func TestClipRespectsUTF8(t *testing.T) {
	long := strings.Repeat("ü", 300)
	got := clip(long, maxTitle)
	if len(got) > maxTitle {
		t.Errorf("len = %d, want <= %d", len(got), maxTitle)
	}
	if !strings.HasSuffix(got, "…") {
		t.Error("no ellipsis")
	}
	for _, r := range got {
		if r == 0xFFFD {
			t.Fatal("invalid UTF-8 after clip")
		}
	}
}

func TestParseWebhooks(t *testing.T) {
	hooks, err := ParseWebhooks(`[{"name":"admin","url":"https://x/1","events":["routine."]},{"url":"https://x/2"}]`, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(hooks) != 2 || hooks[1].Name != "webhook-2" || hooks[1].Events[0] != "*" {
		t.Errorf("hooks = %+v", hooks)
	}

	hooks, err = ParseWebhooks("", "https://x/single")
	if err != nil || len(hooks) != 1 || hooks[0].Events[0] != "*" {
		t.Errorf("single: hooks=%+v err=%v", hooks, err)
	}

	if _, err := ParseWebhooks(`[{"name":"kaputt"}]`, ""); err == nil {
		t.Error("missing url accepted")
	}
	if _, err := ParseWebhooks(`kein json`, ""); err == nil {
		t.Error("invalid json accepted")
	}
	if hooks, err := ParseWebhooks("", ""); err != nil || hooks != nil {
		t.Errorf("empty: hooks=%+v err=%v", hooks, err)
	}
}
