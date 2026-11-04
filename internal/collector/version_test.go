package collector

import (
	"context"
	"log/slog"
	"os"
	"testing"
)

type fixedMC struct{ s MCStatus }

func (f fixedMC) Status(context.Context) (MCStatus, error) { return f.s, nil }

// Nach einem Ein-Klick-Update steht in MC_VERSION noch die alte Version.
// Die zuletzt gesehene Version muss deshalb den Neustart überleben und
// sofort nach dem Start verfügbar sein, bevor Minecraft antwortet.
func TestVersionStoreSeedsAndPersists(t *testing.T) {
	stored := "26.2"
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	c := New(Config{}, nil, fixedMC{MCStatus{Online: true, Version: "26.3"}}, nil, nil, nil, log)
	c.SetVersionStore(func() string { return stored }, func(v string) { stored = v })
	if got := c.MCVersion(); got != "26.2" {
		t.Fatalf("MCVersion nach Start = %q, want 26.2 aus dem Store", got)
	}

	c.pollMC(context.Background())
	if got := c.MCVersion(); got != "26.3" {
		t.Errorf("MCVersion nach Poll = %q, want 26.3", got)
	}
	if stored != "26.3" {
		t.Errorf("Store = %q, want 26.3", stored)
	}
}
