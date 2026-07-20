package events

import (
	"testing"
	"time"
)

func TestPublishReachesAllSubscribers(t *testing.T) {
	b := New()
	ch1, cancel1 := b.Subscribe(4)
	ch2, cancel2 := b.Subscribe(4)
	defer cancel1()
	defer cancel2()

	b.Publish(Event{Type: TypeRoutineOK, Title: "t"})

	for i, ch := range []<-chan Event{ch1, ch2} {
		select {
		case ev := <-ch:
			if ev.Type != TypeRoutineOK {
				t.Errorf("sub %d: type = %q", i, ev.Type)
			}
			if ev.Time.IsZero() {
				t.Errorf("sub %d: timestamp not filled in", i)
			}
		case <-time.After(time.Second):
			t.Fatalf("sub %d: no event received", i)
		}
	}
}

func TestPublishNeverBlocksOnFullSubscriber(t *testing.T) {
	b := New()
	_, cancel := b.Subscribe(1) // nobody reads
	defer cancel()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 10; i++ {
			b.Publish(Event{Type: TypeRoutineOK})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on full subscriber buffer")
	}
	if b.Dropped() != 9 {
		t.Errorf("dropped = %d, want 9", b.Dropped())
	}
}

func TestNilBusIsSafe(t *testing.T) {
	var b *Bus
	b.Publish(Event{Type: TypeRoutineOK}) // must not panic
	if b.Dropped() != 0 {
		t.Error("nil bus dropped != 0")
	}
}

func TestCancelClosesChannelOnce(t *testing.T) {
	b := New()
	ch, cancel := b.Subscribe(1)
	cancel()
	cancel() // second cancel must not panic
	if _, open := <-ch; open {
		t.Error("channel still open after cancel")
	}
	b.Publish(Event{Type: TypeRoutineOK}) // must not panic on removed sub
}

func TestMatches(t *testing.T) {
	cases := []struct {
		filters []string
		typ     string
		want    bool
	}{
		{[]string{"*"}, "mods.applied", true},
		{[]string{"mods."}, "mods.applied", true},
		{[]string{"mods."}, "routine.ok", false},
		{[]string{"routine.ok"}, "routine.ok", true},
		{[]string{"routine.ok"}, "routine.failed", false},
		{nil, "routine.ok", false},
		{[]string{""}, "routine.ok", false},
	}
	for _, c := range cases {
		if got := Matches(c.filters, c.typ); got != c.want {
			t.Errorf("Matches(%v, %q) = %v, want %v", c.filters, c.typ, got, c.want)
		}
	}
}
