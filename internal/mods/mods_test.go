package mods_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mock"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mods"
)

func setup(t *testing.T) (*mods.Manager, []mods.Profile, string) {
	t.Helper()
	base := t.TempDir()
	profiles, err := mock.CreateFakeProfiles(base)
	if err != nil {
		t.Fatal(err)
	}
	mgr := mods.NewManager(mock.NewModrinth(), "fabric", profiles)
	return mgr, profiles, base
}

func TestCheckUpdatesIdentifiesAndFindsUpdates(t *testing.T) {
	mgr, _, _ := setup(t)
	entries, err := mgr.CheckUpdates(context.Background(), "server", "1.21.11")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Fatalf("entries = %d, want 4", len(entries))
	}
	var managed, updates int
	for _, e := range entries {
		if e.Managed {
			managed++
		}
		if e.UpdateVersion != "" {
			updates++
		}
	}
	if managed != 4 {
		t.Errorf("managed = %d, want 4 (mock kennt alle)", managed)
	}
	if updates == 0 {
		t.Error("keine Updates gefunden, mock liefert für jeden zweiten eins")
	}
}

func TestStageApplyRollback(t *testing.T) {
	mgr, profiles, _ := setup(t)
	ctx := context.Background()
	serverDir := profiles[0].Dirs["mods"]

	if _, err := mgr.CheckUpdates(ctx, "server", "1.21.11"); err != nil {
		t.Fatal(err)
	}
	entries := mgr.Entries("server")
	var updatable []string
	oldContent := map[string][]byte{}
	for _, e := range entries {
		if e.UpdateVersion != "" {
			updatable = append(updatable, e.Filename)
			data, _ := os.ReadFile(filepath.Join(serverDir, e.Filename))
			oldContent[e.Filename] = data
		}
	}
	if len(updatable) == 0 {
		t.Fatal("nichts updatebar")
	}

	// Stage: Live-Verzeichnis bleibt unangetastet
	n, err := mgr.Stage(ctx, "server", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(updatable) {
		t.Errorf("staged = %d, want %d", n, len(updatable))
	}
	for f, want := range oldContent {
		got, _ := os.ReadFile(filepath.Join(serverDir, f))
		if string(got) != string(want) {
			t.Errorf("live-datei %s wurde beim staging verändert!", f)
		}
	}

	// Apply: alte Dateien im Backup, neue im Live-Verzeichnis
	label, applied, err := mgr.ApplyStaged("server")
	if err != nil {
		t.Fatal(err)
	}
	if applied != n {
		t.Errorf("applied = %d, want %d", applied, n)
	}
	for f := range oldContent {
		if _, err := os.Stat(filepath.Join(serverDir, f)); !os.IsNotExist(err) {
			t.Errorf("alte datei %s liegt noch im live-verzeichnis", f)
		}
		backup := filepath.Join(serverDir, ".backup", label, f)
		if _, err := os.Stat(backup); err != nil {
			t.Errorf("backup von %s fehlt: %v", f, err)
		}
	}

	// Rollback: alter Stand zurück
	restored, err := mgr.Rollback("server")
	if err != nil {
		t.Fatal(err)
	}
	if restored != applied {
		t.Errorf("restored = %d, want %d", restored, applied)
	}
	for f, want := range oldContent {
		got, err := os.ReadFile(filepath.Join(serverDir, f))
		if err != nil || string(got) != string(want) {
			t.Errorf("rollback von %s unvollständig: %v", f, err)
		}
	}
}

func TestApplyKeepsRemainingUpdateBadges(t *testing.T) {
	mgr, _, _ := setup(t)
	ctx := context.Background()
	mgr.CheckUpdates(ctx, "server", "1.21.11")

	// nur EIN Update stagen und anwenden
	var first string
	for _, e := range mgr.Entries("server") {
		if e.UpdateVersion != "" {
			first = e.Filename
			break
		}
	}
	if _, err := mgr.Stage(ctx, "server", []string{first}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mgr.ApplyStaged("server"); err != nil {
		t.Fatal(err)
	}

	entries := mgr.Entries("server")
	if len(entries) == 0 {
		t.Fatal("cache wurde geleert — Update-Badges verloren (UX-Regression)")
	}
	var stillUpdatable, appliedCurrent int
	for _, e := range entries {
		if e.UpdateVersion != "" {
			stillUpdatable++
		}
		if e.Version == "1.1.0" && e.UpdateVersion == "" {
			appliedCurrent++
		}
	}
	if stillUpdatable == 0 {
		t.Error("übrige Updates verschwunden — nur der angewendete Eintrag darf 'aktuell' werden")
	}
	if appliedCurrent != 1 {
		t.Errorf("angewendeter Eintrag nicht als aktuell markiert (got %d)", appliedCurrent)
	}
}

func TestWatcherRejectsEmptyVersion(t *testing.T) {
	mgr, _, _ := setup(t)
	w := mods.NewWatcher(mock.NewModrinth(), mgr, "fabric")
	if _, err := w.Check(context.Background(), ""); err == nil {
		t.Error("Check mit leerer Version muss fehlschlagen statt Unsinn zu liefern")
	}
}

func TestApplyWithoutStagingFails(t *testing.T) {
	mgr, _, _ := setup(t)
	if _, _, err := mgr.ApplyStaged("server"); err == nil {
		t.Error("apply ohne staging muss fehlschlagen")
	}
}

func TestUnmanagedFilesUntouched(t *testing.T) {
	mgr, profiles, _ := setup(t)
	ctx := context.Background()
	serverDir := profiles[0].Dirs["mods"]

	mgr.CheckUpdates(ctx, "server", "1.21.11")
	mgr.Stage(ctx, "server", nil)
	mgr.ApplyStaged("server")

	// unknown-custom-mod.jar hat kein Update (mock) — muss unverändert liegen
	if _, err := os.Stat(filepath.Join(serverDir, "unknown-custom-mod.jar")); err != nil {
		t.Errorf("unverwaltete datei wurde angefasst: %v", err)
	}
}

func TestWatcherReadiness(t *testing.T) {
	mgr, _, _ := setup(t)
	ctx := context.Background()
	mgr.CheckUpdates(ctx, "server", "1.21.11")
	mgr.CheckUpdates(ctx, "client", "1.21.11")

	api := mock.NewModrinth()
	w := mods.NewWatcher(api, mgr, "fabric")

	// fake mojang + fabric endpoints
	manifest := `{"latest":{"release":"1.22"}}`
	fabric := `[{"version":"1.22","stable":true}]`
	srv := newJSONServer(t, map[string]string{"/manifest": manifest, "/fabric": fabric})
	w.SetEndpoints(srv+"/manifest", srv+"/fabric")

	status, err := w.Check(ctx, "1.21.11")
	if err != nil {
		t.Fatal(err)
	}
	if !status.NewerAvailable || status.LatestVersion != "1.22" {
		t.Errorf("status = %+v, want newer 1.22", status)
	}
	if !status.LoaderReady {
		t.Error("loader sollte bereit sein")
	}
	if len(status.Profiles) != 2 {
		t.Fatalf("profiles = %d, want 2", len(status.Profiles))
	}
	// proj-0 hinkt laut mock hinterher
	if len(status.Stragglers) == 0 {
		t.Error("erwartet mindestens einen Nachzügler (proj-0)")
	}
	for _, p := range status.Profiles {
		if p.Ready >= p.Total && len(status.Stragglers) > 0 && p.Profile == "server" {
			t.Errorf("server-profil: ready %d/%d trotz Nachzügler", p.Ready, p.Total)
		}
	}
}
