package basepack

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/modrinth"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mods"
)

// realFiles: echter mods.Manager (Staging, Sicherung), eigene Mods vorgegeben.
type realFiles struct {
	*mods.Manager
	own []mods.Entry
	err error
}

func (r *realFiles) CheckUpdates(context.Context, string, string) ([]mods.Entry, error) {
	return r.own, r.err
}

func setupReal(t *testing.T) (*Service, *fakeAPI, *realFiles, string) {
	t.Helper()
	root := t.TempDir()
	dirs := map[string]string{}
	for _, d := range []string{"mods", "resourcepacks", "shaderpacks"} {
		dirs[d] = filepath.Join(root, d)
		os.MkdirAll(dirs[d], 0o755)
	}
	mgr := mods.NewManager(nil, "fabric", []mods.Profile{{Name: mods.ProfileClient, Dirs: dirs}})
	api := &fakeAPI{versions: map[string][]modrinth.Version{}, packs: map[string][]byte{}, blobs: map[string][]byte{}, byHash: map[string]modrinth.Version{}}
	files := &realFiles{Manager: mgr}
	kv := map[string]string{}
	s := New(api, files, root, func(k string) string { return kv[k] }, func(k, v string) { kv[k] = v },
		nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	mgr.SetPinned(func(profile, name string) bool { return profile == mods.ProfileClient && s.Pinned(name) })
	return s, api, files, root
}

// Important 1: Pack-Update brach nach dem Einsetzen ab (neue Datei live, alte
// in der Sicherung, Stand noch alt) — die Wiederholung muss gelingen.
func TestApplyRetryAfterInterruptedUpdate(t *testing.T) {
	s, api, _, root := setupReal(t)
	ctx := context.Background()
	api.addPackVersion(t, "26.3", "6.4.0", map[string]string{"sodium-1.jar": "s1", "lithium-1.jar": "l1"}, nil)
	if _, err := s.Apply(ctx, "FO", "6.4.0", "26.3", nil); err != nil {
		t.Fatal(err)
	}
	api.addPackVersion(t, "26.3", "6.5.0", map[string]string{"sodium-2.jar": "s2", "lithium-1.jar": "l1"}, nil)
	// Zustand nach Abbruch: sodium-2 live, sodium-1 weg (gesichert), Stand 6.4.0
	os.Remove(filepath.Join(root, "mods", "sodium-1.jar"))
	os.WriteFile(filepath.Join(root, "mods", "sodium-2.jar"), []byte("s2"), 0o644)
	if _, err := s.Apply(ctx, "FO", "6.5.0", "26.3", nil); err != nil {
		t.Fatalf("Wiederholung scheitert: %v", err)
	}
	st, _ := s.State()
	if st.VersionNumber != "6.5.0" {
		t.Errorf("version = %s", st.VersionNumber)
	}
	if !exists(root, "mods", "sodium-2.jar") || exists(root, "mods", "sodium-1.jar") {
		t.Error("Live-Ordner falsch")
	}
	// Variante: beide liegen noch live → alte Datei muss raus
	api.addPackVersion(t, "26.3", "6.6.0", map[string]string{"sodium-3.jar": "s3", "lithium-1.jar": "l1"}, nil)
	os.WriteFile(filepath.Join(root, "mods", "sodium-3.jar"), []byte("s3"), 0o644)
	if _, err := s.Apply(ctx, "FO", "6.6.0", "26.3", nil); err != nil {
		t.Fatalf("Wiederholung (alte noch live) scheitert: %v", err)
	}
	if exists(root, "mods", "sodium-2.jar") || !exists(root, "mods", "sodium-3.jar") {
		t.Error("alte Datei liegt noch live")
	}
}
