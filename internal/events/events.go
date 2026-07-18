// Package events is the central event bus from the concept (Benachrichtigungen
// & Integrationen): every notable occurrence is published here, notifiers
// (Discord first) subscribe. Invariants:
//   - publishing never blocks — a slow or dead subscriber drops events for
//     itself, never for others and never for the publisher
//   - a nil *Bus is safe to publish to (no-op), so emitters need no nil checks
package events

import (
	"sync"
	"time"
)

// Severity drives the embed color and urgency of a notification.
type Severity string

const (
	SevInfo    Severity = "info"
	SevSuccess Severity = "success"
	SevWarn    Severity = "warn"
	SevError   Severity = "error"
)

// Event types use dotted names; subscribers can filter by exact type or
// "prefix." (trailing dot) — see Matches.
const (
	TypeRoutineOK       = "routine.ok"
	TypeRoutineFailed   = "routine.failed"
	TypeRoutineSkipped  = "routine.skipped"
	TypeModsApplied     = "mods.applied"
	TypeModsRollback    = "mods.rollback"
	TypeVersionNew      = "version.new"
	TypeVersionReady    = "version.ready"
	TypeContainerAction = "container.action"
	TypeBackupOK        = "backup.ok"
	TypeBackupFailed    = "backup.failed"
	TypeBackupStale     = "backup.stale"
	TypeRestoreOK       = "backup.restore.ok"
	TypeRestoreFailed   = "backup.restore.failed"
	TypeSystemReboot    = "system.reboot"   // Reboot angefordert
	TypeSystemOnline    = "system.online"   // nach Boot: alles wieder da
	TypeSystemDegraded  = "system.degraded" // nach Boot: Server kam nicht hoch

	// Wächter (Phase 4.7)
	TypeCrash       = "watch.crash"        // Crash-Report aufgetaucht
	TypeServerDown  = "watch.server.down"  // MC-Container unerwartet aus
	TypeServerUp    = "watch.server.up"    // Entwarnung nach server.down
	TypeNetDegraded = "watch.net.degraded" // Internet anhaltend schlecht
	TypeNetOK       = "watch.net.ok"       // Entwarnung nach net.degraded
	TypeResource    = "watch.resource"     // Disk/RAM-Schwellwert gerissen

	// Wartungsfenster (Phase 4.6)
	TypeMaintAnnounce = "maintenance.announce" // Fenster angekündigt
	TypeMaintStart    = "maintenance.start"    // Fenster beginnt
	TypeMaintEnd      = "maintenance.end"      // Fenster beendet, alles wieder da
)

// Field is one key/value pair shown in a notification (Discord embed field).
type Field struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Event is one occurrence worth telling someone about.
type Event struct {
	Type     string    `json:"type"`
	Severity Severity  `json:"severity"`
	Title    string    `json:"title"`
	Message  string    `json:"message,omitempty"`
	Fields   []Field   `json:"fields,omitempty"`
	Time     time.Time `json:"time"`
}

// Matches reports whether an event type passes a filter list. Filters:
// "*" matches everything, "mods." (trailing dot) matches the whole prefix,
// anything else must match exactly. An empty list matches nothing.
func Matches(filters []string, eventType string) bool {
	for _, f := range filters {
		switch {
		case f == "*":
			return true
		case len(f) > 0 && f[len(f)-1] == '.':
			if len(eventType) >= len(f) && eventType[:len(f)] == f {
				return true
			}
		case f == eventType:
			return true
		}
	}
	return false
}

// Bus fans events out to subscribers. Zero value is not usable — use New.
type Bus struct {
	mu      sync.Mutex
	subs    map[int]chan Event
	nextID  int
	dropped int
}

func New() *Bus {
	return &Bus{subs: map[int]chan Event{}}
}

// Publish delivers the event to every subscriber without ever blocking.
// Missing timestamps are filled in. Safe on a nil bus.
func (b *Bus) Publish(ev Event) {
	if b == nil {
		return
	}
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subs {
		select {
		case ch <- ev:
		default:
			b.dropped++ // full buffer: subscriber loses this event, publisher never waits
		}
	}
}

// Subscribe returns a buffered event channel and a cancel func that closes it.
func (b *Bus) Subscribe(buffer int) (<-chan Event, func()) {
	if buffer < 1 {
		buffer = 16
	}
	ch := make(chan Event, buffer)
	b.mu.Lock()
	id := b.nextID
	b.nextID++
	b.subs[id] = ch
	b.mu.Unlock()
	cancel := func() {
		b.mu.Lock()
		if _, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(ch)
		}
		b.mu.Unlock()
	}
	return ch, cancel
}

// Dropped returns how many events were lost to full subscriber buffers.
func (b *Bus) Dropped() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dropped
}
