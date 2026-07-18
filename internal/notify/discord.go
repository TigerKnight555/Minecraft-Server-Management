// Package notify contains the notifiers that subscribe to the event bus.
// First (and per concept decision the primary) notifier: Discord webhooks —
// no bot, just HTTP POSTs with embeds. Webhook URLs are secrets and only
// ever enter the process via environment, never the repo or the database.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
)

// Webhook is one Discord target with an event-type filter (see events.Matches).
type Webhook struct {
	Name   string   `json:"name"`
	URL    string   `json:"url"`
	Events []string `json:"events"`
}

// ParseWebhooks builds the webhook list from the two supported env formats:
// jsonList  — MSM_DISCORD_WEBHOOKS: `[{"name":"admin","url":"...","events":["*"]}]`
// singleURL — MSM_DISCORD_WEBHOOK_URL: one URL that receives every event.
// jsonList wins when both are set.
func ParseWebhooks(jsonList, singleURL string) ([]Webhook, error) {
	if strings.TrimSpace(jsonList) != "" {
		var hooks []Webhook
		if err := json.Unmarshal([]byte(jsonList), &hooks); err != nil {
			return nil, fmt.Errorf("MSM_DISCORD_WEBHOOKS: %w", err)
		}
		for i, h := range hooks {
			if h.URL == "" {
				return nil, fmt.Errorf("MSM_DISCORD_WEBHOOKS[%d]: url fehlt", i)
			}
			if len(h.Events) == 0 {
				hooks[i].Events = []string{"*"}
			}
			if h.Name == "" {
				hooks[i].Name = fmt.Sprintf("webhook-%d", i+1)
			}
		}
		return hooks, nil
	}
	if strings.TrimSpace(singleURL) != "" {
		return []Webhook{{Name: "default", URL: strings.TrimSpace(singleURL), Events: []string{"*"}}}, nil
	}
	return nil, nil
}

// Discord posts events as webhook embeds.
type Discord struct {
	hooks []Webhook
	http  *http.Client
	log   *slog.Logger
}

func NewDiscord(hooks []Webhook, log *slog.Logger) *Discord {
	return &Discord{
		hooks: hooks,
		http:  &http.Client{Timeout: 15 * time.Second},
		log:   log,
	}
}

// Run consumes the event channel until it closes or ctx is done. Meant to be
// started as a goroutine; delivery failures are logged, never fatal.
func (d *Discord) Run(ctx context.Context, ch <-chan events.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			for _, h := range d.hooks {
				if !events.Matches(h.Events, ev.Type) {
					continue
				}
				if err := d.send(ctx, h.URL, ev); err != nil {
					d.log.Error("discord delivery failed", "webhook", h.Name, "event", ev.Type, "err", err)
				}
			}
		}
	}
}

// Discord embed limits (documented API constraints).
const (
	maxTitle  = 256
	maxDesc   = 4096
	maxField  = 1024
	maxFields = 25
)

var colors = map[events.Severity]int{
	events.SevSuccess: 0x2ECC71, // green
	events.SevInfo:    0x3498DB, // blue
	events.SevWarn:    0xE67E22, // orange
	events.SevError:   0xE74C3C, // red
}

type embedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

type embed struct {
	Title       string       `json:"title"`
	Description string       `json:"description,omitempty"`
	Color       int          `json:"color"`
	Timestamp   string       `json:"timestamp"`
	Fields      []embedField `json:"fields,omitempty"`
}

type webhookPayload struct {
	Username string  `json:"username"`
	Embeds   []embed `json:"embeds"`
}

func buildPayload(ev events.Event) webhookPayload {
	e := embed{
		Title:       clip(ev.Title, maxTitle),
		Description: clip(ev.Message, maxDesc),
		Color:       colors[ev.Severity],
		Timestamp:   ev.Time.UTC().Format(time.RFC3339),
	}
	if e.Color == 0 {
		e.Color = colors[events.SevInfo]
	}
	for i, f := range ev.Fields {
		if i == maxFields {
			break
		}
		v := f.Value
		if v == "" {
			v = "—" // Discord rejects empty field values
		}
		e.Fields = append(e.Fields, embedField{
			Name: clip(f.Name, maxTitle), Value: clip(v, maxField), Inline: true,
		})
	}
	return webhookPayload{Username: "MSM", Embeds: []embed{e}}
}

// send delivers one event with up to 3 attempts; 429 waits for Retry-After.
func (d *Discord) send(ctx context.Context, url string, ev events.Event) error {
	body, err := json.Marshal(buildPayload(ev))
	if err != nil {
		return err
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := d.http.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		func() {
			defer resp.Body.Close()
			switch {
			case resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK:
				lastErr = nil
			case resp.StatusCode == http.StatusTooManyRequests:
				if s, err := strconv.ParseFloat(resp.Header.Get("Retry-After"), 64); err == nil && s > 0 && s <= 60 {
					select {
					case <-ctx.Done():
					case <-time.After(time.Duration(s * float64(time.Second))):
					}
				}
				lastErr = fmt.Errorf("rate limited (429)")
			default:
				snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
				lastErr = fmt.Errorf("discord antwortete %s: %s", resp.Status, snippet)
			}
		}()
		if lastErr == nil {
			return nil
		}
	}
	return lastErr
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// clip on a rune boundary so we never produce invalid UTF-8; the
	// ellipsis itself is 3 bytes and must fit within n
	cut := n - len("…")
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut] + "…"
}
