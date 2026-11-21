package basepack

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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

func keyOf(t *testing.T, st State, filename string) string {
	t.Helper()
	for _, f := range st.Files {
		if f.Filename == filename {
			return Key(f)
		}
	}
	t.Fatalf("%s nicht im Stand", filename)
	return ""
}

// Important 2: zwei Pack-Dateien desselben Modrinth-Projekts brauchen
// eigene Schlüssel — Übernehmen, erneutes Übernehmen, einzeln schalten.
func TestTwoFilesSameProject(t *testing.T) {
	s, api, _, root := setupReal(t)
	ctx := context.Background()
	api.addPackVersion(t, "26.3", "6.4.0", map[string]string{"fabric-api-1.jar": "a1", "fabric-lib-1.jar": "b1", "sodium-1.jar": "s1"}, nil)
	if _, err := s.Apply(ctx, "FO", "6.4.0", "26.3", nil); err != nil {
		t.Fatal(err)
	}
	st, _ := s.State()
	kA, kB := keyOf(t, st, "fabric-api-1.jar"), keyOf(t, st, "fabric-lib-1.jar")
	if kA == kB {
		t.Fatalf("Schlüssel nicht eindeutig: %q", kA)
	}
	api.addPackVersion(t, "26.3", "6.5.0", map[string]string{"fabric-api-2.jar": "a2", "fabric-lib-2.jar": "b2", "sodium-1.jar": "s1"}, nil)
	if _, err := s.Apply(ctx, "FO", "6.5.0", "26.3", nil); err != nil {
		t.Fatalf("erneutes Übernehmen: %v", err)
	}
	for name, want := range map[string]bool{"fabric-api-2.jar": true, "fabric-lib-2.jar": true, "fabric-api-1.jar": false, "fabric-lib-1.jar": false} {
		if exists(root, "mods", name) != want {
			t.Errorf("%s live = %v, want %v", name, !want, want)
		}
	}
	st, _ = s.State()
	kA, kB = keyOf(t, st, "fabric-api-2.jar"), keyOf(t, st, "fabric-lib-2.jar")
	if err := s.Toggle(ctx, kB, false); err != nil {
		t.Fatal(err)
	}
	if exists(root, "mods", "fabric-lib-2.jar") || !exists(root, "mods", "fabric-api-2.jar") {
		t.Error("Abschalten von lib trifft die falsche Datei")
	}
	if !s.Pinned("fabric-api-2.jar") || s.Pinned("fabric-lib-2.jar") {
		t.Error("Pinned falsch nach Abschalten von lib")
	}
	if err := s.Toggle(ctx, kA, false); err != nil {
		t.Fatal(err)
	}
	if err := s.Toggle(ctx, kB, true); err != nil {
		t.Fatal(err)
	}
	if !exists(root, "mods", "fabric-lib-2.jar") || exists(root, "mods", "fabric-api-2.jar") {
		t.Error("Anschalten von lib trifft die falsche Datei")
	}
	st, _ = s.State()
	if !st.IsDisabled(kA) || st.IsDisabled(kB) {
		t.Errorf("disabled = %v", st.Disabled)
	}
	// Pack-Update mit abgeschalteter api: bleibt aus, lib wird aktualisiert
	api.addPackVersion(t, "26.3", "6.6.0", map[string]string{"fabric-api-3.jar": "a3", "fabric-lib-3.jar": "b3", "sodium-1.jar": "s1"}, nil)
	if _, err := s.Apply(ctx, "FO", "6.6.0", "26.3", nil); err != nil {
		t.Fatal(err)
	}
	if exists(root, "mods", "fabric-api-3.jar") || !exists(root, "mods", "fabric-lib-3.jar") || exists(root, "mods", "fabric-lib-2.jar") {
		t.Error("Update mit abgeschalteter api falsch")
	}
}

// Alter Stand ohne gespeicherte Schlüssel: doppelte Projekte werden beim
// Lesen eindeutig gemacht.
func TestLegacyStateGetsUniqueKeys(t *testing.T) {
	s, _, _, _ := setupReal(t)
	s.save(&State{ProjectID: "FO", Title: "FO", Files: []PackFile{
		{Path: "mods/b.jar", Category: "mods", Filename: "b.jar", ProjectID: "P"},
		{Path: "mods/a.jar", Category: "mods", Filename: "a.jar", ProjectID: "P"},
	}})
	st, _ := s.State()
	if Key(st.Files[0]) == Key(st.Files[1]) {
		t.Fatalf("Schlüssel gleich: %q", Key(st.Files[0]))
	}
	if keyOf(t, st, "a.jar") != "P" {
		t.Errorf("erste Datei (nach Pfad) soll den Projekt-Schlüssel behalten, got %q", keyOf(t, st, "a.jar"))
	}
}

// Important 3: scheitert die Liste der eigenen Mods, darf nichts angewendet
// werden — sonst lägen Pack- und eigene Kopie derselben Mod nebeneinander.
func TestOwnErrorAborts(t *testing.T) {
	s, api, files, root := setupReal(t)
	ctx := context.Background()
	api.addPackVersion(t, "26.3", "6.4.0", map[string]string{"sodium-1.jar": "s1"}, nil)
	files.err = errors.New("modrinth down")
	if _, err := s.Preview(ctx, "FO", "26.3"); err == nil {
		t.Error("Preview ohne Fehler trotz gescheiterter Mod-Liste")
	}
	if _, err := s.Apply(ctx, "FO", "6.4.0", "26.3", nil); err == nil {
		t.Error("Apply ohne Fehler trotz gescheiterter Mod-Liste")
	}
	if _, ok := s.State(); ok || exists(root, "mods", "sodium-1.jar") {
		t.Error("Apply hat trotz Fehler angewendet")
	}
	files.err = nil
	if _, err := s.Apply(ctx, "FO", "6.4.0", "26.3", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Toggle(ctx, "P-sodium", false); err != nil {
		t.Fatal(err)
	}
	files.err = errors.New("modrinth down")
	if err := s.Toggle(ctx, "P-sodium", true); err == nil {
		t.Error("Anschalten ohne Fehler trotz gescheiterter Mod-Liste")
	}
	if st, _ := s.State(); !st.IsDisabled("P-sodium") || exists(root, "mods", "sodium-1.jar") {
		t.Error("Anschalten hat trotz Fehler angewendet")
	}
	// Abschalten braucht die Liste nicht
	files.err = nil
	if err := s.Toggle(ctx, "P-sodium", true); err != nil {
		t.Fatal(err)
	}
	files.err = errors.New("modrinth down")
	if err := s.Toggle(ctx, "P-sodium", false); err != nil {
		t.Errorf("Abschalten: %v", err)
	}
}

// Minor 5: auch ein gespeicherter Stand mit fremder URL (Anschalten nutzt
// die gespeicherte URL) wird nicht geladen.
func TestDownloadRefusesForeignURL(t *testing.T) {
	s, api, _, _ := setupReal(t)
	ctx := context.Background()
	api.addPackVersion(t, "26.3", "6.4.0", map[string]string{"sodium-1.jar": "s1"}, nil)
	if _, err := s.Apply(ctx, "FO", "6.4.0", "26.3", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Toggle(ctx, "P-sodium", false); err != nil {
		t.Fatal(err)
	}
	st, _ := s.State()
	st.Files[0].URL = "http://169.254.169.254/latest/meta-data"
	api.blobs[st.Files[0].URL] = []byte("s1")
	s.save(&st)
	if err := s.Toggle(ctx, "P-sodium", true); err == nil || !strings.Contains(err.Error(), "Quelle") {
		t.Errorf("err = %v, want Ablehnung der Quelle", err)
	}
}

// Minor 6: die .mrpack-Datei wird gegen den SHA512 der Version geprüft.
func TestPackHashVerified(t *testing.T) {
	s, api, _, _ := setupReal(t)
	api.addPackVersion(t, "26.3", "6.4.0", map[string]string{"sodium-1.jar": "s1"}, nil)
	v := &api.versions["26.3"][0]
	v.Files[0].Hashes.SHA512 = sum(api.packs[v.Files[0].URL])
	if _, err := s.Preview(context.Background(), "FO", "26.3"); err != nil {
		t.Fatalf("korrekte Prüfsumme abgelehnt: %v", err)
	}
	v.Files[0].Hashes.SHA512 = sum([]byte("anders"))
	if _, err := s.Preview(context.Background(), "FO", "26.3"); err == nil || !strings.Contains(err.Error(), "hash") {
		t.Errorf("err = %v, want Prüfsummenfehler", err)
	}
}

// Minor 8: unveränderte Config-Auswahl meldet keine „geändert“-Zeile.
func TestSetConfigsNoopNoNotify(t *testing.T) {
	s, api, _, _, notes := setup(t)
	ctx := context.Background()
	api.addPackVersion(t, "26.3", "6.4.0", map[string]string{"sodium-1.jar": "s1"}, map[string]string{"config/a.json": "A"})
	if _, err := s.Apply(ctx, "FO", "6.4.0", "26.3", nil); err != nil {
		t.Fatal(err)
	}
	n := len(*notes)
	st, _ := s.State()
	if err := s.SetConfigs(ctx, map[string]bool{"config/a.json": st.Configs["config/a.json"]}); err != nil {
		t.Fatal(err)
	}
	if len(*notes) != n {
		t.Errorf("no-op meldet: %+v", (*notes)[n:])
	}
	if err := s.SetConfigs(ctx, map[string]bool{"config/a.json": !st.Configs["config/a.json"]}); err != nil {
		t.Fatal(err)
	}
	if len(*notes) != n+1 {
		t.Error("echte Änderung nicht gemeldet")
	}
}

// Minor 9: Pinned wird je Mod-Eintrag gerufen — der Stand darf nicht jedes
// Mal neu geparst werden, muss aber Änderungen sofort sehen.
func TestPinnedUsesCachedState(t *testing.T) {
	s, api, _, _ := setupReal(t)
	ctx := context.Background()
	api.addPackVersion(t, "26.3", "6.4.0", map[string]string{"sodium-1.jar": "s1", "lithium-1.jar": "l1"}, nil)
	if _, err := s.Apply(ctx, "FO", "6.4.0", "26.3", nil); err != nil {
		t.Fatal(err)
	}
	if !s.Pinned("sodium-1.jar") {
		t.Fatal("sodium nicht angeheftet")
	}
	if allocs := testing.AllocsPerRun(100, func() { s.Pinned("sodium-1.jar") }); allocs > 0 {
		t.Errorf("Pinned alloziert %.0f× je Aufruf — Stand wird neu geparst", allocs)
	}
	if err := s.Toggle(ctx, "P-sodium", false); err != nil {
		t.Fatal(err)
	}
	if s.Pinned("sodium-1.jar") {
		t.Error("Cache sieht die Abschaltung nicht")
	}
}
