package mods

import (
	"os"
	"path/filepath"
	"testing"
	"strings"
	"time"
)

func testProfile(t *testing.T) (*Manager, Profile, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "mods")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := Profile{Name: ProfileServer, Dirs: map[string]string{"mods": dir}}
	return NewManager(nil, "fabric", []Profile{p}), p, dir
}

func stage(t *testing.T, p Profile, files map[string]string, items ...stagingItem) {
	t.Helper()
	sdir := stagingDir(p)
	if err := os.MkdirAll(sdir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(sdir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeManifest(filepath.Join(sdir, "manifest.json"), stagingManifest{Items: items}); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

func TestApplyAndRollbackAddItem(t *testing.T) {
	m, p, dir := testProfile(t)
	stage(t, p, map[string]string{"jade-1.0.jar": "jade"},
		stagingItem{Category: "mods", NewFilename: "jade-1.0.jar", ProjectID: "P1", Name: "Jade", ClientSide: "required"})
	var got []Change
	m.SetChangeHook(func(profile string, c []Change) { got = append(got, c...) })

	label, n, err := m.ApplyStaged(ProfileServer)
	if err != nil || n != 1 {
		t.Fatalf("apply = %d, %v", n, err)
	}
	if !exists(filepath.Join(dir, "jade-1.0.jar")) {
		t.Fatal("hinzugefügte Datei fehlt im Live-Ordner")
	}
	if len(got) != 1 || got[0].Action != "add" || got[0].Name != "Jade" || got[0].Profile != ProfileServer || got[0].ClientSide != "required" {
		t.Errorf("hook = %+v", got)
	}

	restored, err := m.RollbackLabel(ProfileServer, label)
	if err != nil || restored != 1 {
		t.Fatalf("rollback = %d, %v", restored, err)
	}
	if exists(filepath.Join(dir, "jade-1.0.jar")) {
		t.Error("Datei nach Rollback noch im Live-Ordner")
	}
	// Rollback meldet die Umkehrung, damit die Mod-Neuigkeiten sie streichen
	if len(got) != 2 || got[1].Action != "remove" || got[1].Filename != "jade-1.0.jar" || got[1].Name != "Jade" {
		t.Errorf("hook nach Rollback = %+v, want remove jade-1.0.jar", got)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatal("Live-Ordner beim Rollback beschädigt (leerer Dateiname!)")
	}
}

func TestApplyAndRollbackRemoveItem(t *testing.T) {
	m, p, dir := testProfile(t)
	if err := os.WriteFile(filepath.Join(dir, "old.jar"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	stage(t, p, nil, stagingItem{Category: "mods", OldFilename: "old.jar", Name: "Old Mod"})
	var got []Change
	m.SetChangeHook(func(profile string, c []Change) { got = append(got, c...) })

	label, n, err := m.ApplyStaged(ProfileServer)
	if err != nil || n != 1 {
		t.Fatalf("apply = %d, %v", n, err)
	}
	if exists(filepath.Join(dir, "old.jar")) {
		t.Fatal("entfernte Datei noch im Live-Ordner")
	}
	if !exists(filepath.Join(dir, ".backup", label, "old.jar")) {
		t.Fatal("entfernte Datei nicht im Backup-Satz")
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatal("Live-Ordner beschädigt (leerer Dateiname!)")
	}
	if len(got) != 1 || got[0].Action != "remove" || got[0].Filename != "old.jar" || got[0].Name != "Old Mod" {
		t.Errorf("hook = %+v", got)
	}

	if _, err := m.RollbackLabel(ProfileServer, label); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "old.jar")); err != nil || string(b) != "old" {
		t.Error("entfernte Datei nach Rollback nicht zurück")
	}
}

func TestRollbackNotifiesHook(t *testing.T) {
	m, p, _ := testProfile(t)
	stage(t, p, map[string]string{"a.jar": "a"}, stagingItem{Category: "mods", NewFilename: "a.jar"})
	if _, _, err := m.ApplyStaged(ProfileServer); err != nil {
		t.Fatal(err)
	}
	calls := 0
	m.SetChangeHook(func(string, []Change) { calls++ })
	if _, err := m.Rollback(ProfileServer); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("hook calls nach Rollback = %d, want 1", calls)
	}
}

func TestEntriesShowPendingAddAndRemove(t *testing.T) {
	m, p, dir := testProfile(t)
	os.WriteFile(filepath.Join(dir, "keep.jar"), []byte("k"), 0o644)
	os.WriteFile(filepath.Join(dir, "gone.jar"), []byte("g"), 0o644)
	m.entries[ProfileServer] = []Entry{
		{Profile: ProfileServer, Category: "mods", Filename: "gone.jar", Name: "Gone"},
		{Profile: ProfileServer, Category: "mods", Filename: "keep.jar", Name: "Keep"},
	}
	stage(t, p, map[string]string{"new.jar": "n"},
		stagingItem{Category: "mods", NewFilename: "new.jar", ProjectID: "P9", Name: "Neu", Version: "2.0"},
		stagingItem{Category: "mods", OldFilename: "gone.jar", Name: "Gone"})

	byFile := map[string]Entry{}
	for _, e := range m.Entries(ProfileServer) {
		byFile[e.Filename] = e
	}
	if e := byFile["new.jar"]; e.Pending != "add" || !e.Staged || e.Name != "Neu" || !e.Managed {
		t.Errorf("new.jar = %+v", e)
	}
	if e := byFile["gone.jar"]; e.Pending != "remove" || !e.Staged {
		t.Errorf("gone.jar = %+v", e)
	}
	if e := byFile["keep.jar"]; e.Pending != "" || e.Staged {
		t.Errorf("keep.jar = %+v", e)
	}

	// nach dem Anwenden: Cache gepflegt, nichts mehr vorgemerkt
	if _, _, err := m.ApplyStaged(ProfileServer); err != nil {
		t.Fatal(err)
	}
	byFile = map[string]Entry{}
	for _, e := range m.Entries(ProfileServer) {
		byFile[e.Filename] = e
	}
	if _, ok := byFile["gone.jar"]; ok {
		t.Error("entfernter Mod steht noch in der Liste")
	}
	if e, ok := byFile["new.jar"]; !ok || e.Pending != "" || e.Name != "Neu" {
		t.Errorf("neuer Mod nach Anwenden = %+v (ok=%v)", e, ok)
	}
}

func TestDiscardAndHasStaged(t *testing.T) {
	m, p, dir := testProfile(t)
	os.WriteFile(filepath.Join(dir, "gone.jar"), []byte("g"), 0o644)
	stage(t, p, map[string]string{"new.jar": "n"},
		stagingItem{Category: "mods", NewFilename: "new.jar"},
		stagingItem{Category: "mods", OldFilename: "gone.jar"})
	if !m.HasStaged(ProfileServer) {
		t.Fatal("HasStaged = false")
	}
	if err := m.Discard(ProfileServer, "new.jar"); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(stagingDir(p), "new.jar")) {
		t.Error("verworfene Datei liegt noch im Staging")
	}
	if err := m.Discard(ProfileServer, "gone.jar"); err != nil {
		t.Fatal(err)
	}
	if m.HasStaged(ProfileServer) {
		t.Error("HasStaged nach dem Verwerfen aller Einträge = true")
	}
	if !exists(filepath.Join(dir, "gone.jar")) {
		t.Error("Verwerfen einer Entfernung darf den Live-Ordner nicht anfassen")
	}
	if err := m.Discard(ProfileServer, "nix.jar"); err == nil {
		t.Error("Verwerfen ohne Vormerkung muss fehlschlagen")
	}
}

// Fehler mitten im Anwenden: Angewendetes wird gemeldet, die Staging-Liste
// enthält nur noch den Rest — ein zweiter Versuch funktioniert.
func TestApplyStagedPartialFailureKeepsRest(t *testing.T) {
	m, p, dir := testProfile(t)
	stage(t, p, map[string]string{"a-1.0.jar": "a"},
		stagingItem{Category: "mods", NewFilename: "a-1.0.jar", ProjectID: "PA", Name: "A"},
		stagingItem{Category: "mods", NewFilename: "b-1.0.jar", ProjectID: "PB", Name: "B"}) // b fehlt im Staging
	var got []Change
	m.SetChangeHook(func(profile string, c []Change) { got = append(got, c...) })

	if _, n, err := m.ApplyStaged(ProfileServer); err == nil || n != 1 {
		t.Fatalf("apply = %d, %v — want 1 + Fehler", n, err)
	}
	if !exists(filepath.Join(dir, "a-1.0.jar")) {
		t.Error("a nicht angewendet")
	}
	if len(got) != 1 || got[0].Name != "A" {
		t.Errorf("hook = %+v, want nur A", got)
	}
	man, err := readManifest(filepath.Join(stagingDir(p), "manifest.json"))
	if err != nil || len(man.Items) != 1 || man.Items[0].NewFilename != "b-1.0.jar" {
		t.Fatalf("rest-manifest = %+v, %v — want nur b", man.Items, err)
	}

	// Datei nachliefern → zweiter Versuch spielt nur b ein
	if err := os.WriteFile(filepath.Join(stagingDir(p), "b-1.0.jar"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, n, err := m.ApplyStaged(ProfileServer); err != nil || n != 1 {
		t.Fatalf("retry = %d, %v", n, err)
	}
	if !exists(filepath.Join(dir, "b-1.0.jar")) {
		t.Error("b nach Retry nicht da")
	}
}

// Update, dessen neue Datei fehlt: die alte Datei kommt zurück in den
// Live-Ordner statt im Backup zu verschwinden.
func TestApplyStagedFailedUpdateRestoresOld(t *testing.T) {
	m, p, dir := testProfile(t)
	if err := os.WriteFile(filepath.Join(dir, "c-1.0.jar"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	stage(t, p, nil, stagingItem{Category: "mods", OldFilename: "c-1.0.jar", NewFilename: "c-2.0.jar"})
	if _, _, err := m.ApplyStaged(ProfileServer); err == nil {
		t.Fatal("kein Fehler")
	}
	if !exists(filepath.Join(dir, "c-1.0.jar")) {
		t.Error("alte Datei nach gescheitertem Update nicht mehr im Live-Ordner")
	}
}

// Nach einem angewendeten Update zeigte die Liste den Namen der ALTEN
// Version ("[1.21.11] Fabric API 0.141.4" neben fabric-api-0.166.0+26.3.jar,
// WSL-Test 2026-11-09).
func TestApplyUpdateRefreshesName(t *testing.T) {
	m, p, dir := testProfile(t)
	os.WriteFile(filepath.Join(dir, "api-1.jar"), []byte("old"), 0o644)
	m.entries[ProfileServer] = []Entry{{Profile: ProfileServer, Category: "mods", Filename: "api-1.jar", Name: "[1.21.11] API 1"}}
	stage(t, p, map[string]string{"api-2.jar": "new"},
		stagingItem{Category: "mods", OldFilename: "api-1.jar", NewFilename: "api-2.jar", Version: "2", Name: "[26.3] API 2"})
	if _, _, err := m.ApplyStaged(ProfileServer); err != nil {
		t.Fatal(err)
	}
	e := m.Entries(ProfileServer)
	if len(e) != 1 || e[0].Filename != "api-2.jar" || e[0].Name != "[26.3] API 2" {
		t.Errorf("entries = %+v, want Name der neuen Version", e)
	}
}

// Rollback nimmt nur die zuletzt angewendete Änderung zurück — nicht je
// Kategorie-Ordner den jeweils neuesten Satz (WSL-Test 2026-11-09: ein
// Client-Rollback entfernte nebenbei ein älteres Resourcepack-Upload).
// Ein zweiter Rollback geht einen Schritt weiter zurück.
func TestRollbackOnlyNewestChangeAcrossCategories(t *testing.T) {
	base := t.TempDir()
	modsDir, rpDir := filepath.Join(base, "mods"), filepath.Join(base, "resourcepacks")
	os.MkdirAll(modsDir, 0o755)
	os.MkdirAll(rpDir, 0o755)
	p := Profile{Name: ProfileClient, Dirs: map[string]string{"mods": modsDir, "resourcepacks": rpDir}}
	m := NewManager(nil, "fabric", []Profile{p})

	// 1) älter: Resourcepack hinzugefügt
	stage(t, p, map[string]string{"pack.zip": "p"}, stagingItem{Category: "resourcepacks", NewFilename: "pack.zip"})
	if _, _, err := m.ApplyStaged(ProfileClient); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond) // Labels haben Sekunden-Auflösung
	// 2) neuer: Mod entfernt
	os.WriteFile(filepath.Join(modsDir, "way.jar"), []byte("w"), 0o644)
	stage(t, p, nil, stagingItem{Category: "mods", OldFilename: "way.jar"})
	if _, _, err := m.ApplyStaged(ProfileClient); err != nil {
		t.Fatal(err)
	}

	if _, err := m.Rollback(ProfileClient); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(modsDir, "way.jar")) {
		t.Error("entfernter Mod nicht zurück")
	}
	if !exists(filepath.Join(rpDir, "pack.zip")) {
		t.Fatal("Rollback hat die ältere, unbeteiligte Resourcepack-Änderung mit zurückgenommen")
	}

	// zweiter Rollback: jetzt die ältere Änderung
	if _, err := m.Rollback(ProfileClient); err != nil {
		t.Fatalf("zweiter Rollback: %v", err)
	}
	if exists(filepath.Join(rpDir, "pack.zip")) {
		t.Error("zweiter Rollback hat das Resourcepack-Upload nicht zurückgenommen")
	}
	if !exists(filepath.Join(modsDir, "way.jar")) {
		t.Error("zweiter Rollback hat den ersten wieder umgedreht")
	}
}

// Zwei Aktionen direkt nacheinander (gleiche Sekunde) bekommen eigene
// Sätze — vorher überschrieb die zweite das Manifest der ersten, die
// danach nicht mehr zurückrollbar war (WSL-Test 2026-11-10).
func TestBackToBackAppliesGetOwnSets(t *testing.T) {
	m, p, dir := testProfile(t)
	stage(t, p, map[string]string{"a.jar": "a"}, stagingItem{Category: "mods", NewFilename: "a.jar", Name: "A"})
	l1, _, err := m.ApplyStaged(ProfileServer)
	if err != nil {
		t.Fatal(err)
	}
	stage(t, p, map[string]string{"b.jar": "b"}, stagingItem{Category: "mods", NewFilename: "b.jar", Name: "B"})
	l2, _, err := m.ApplyStaged(ProfileServer)
	if err != nil {
		t.Fatal(err)
	}
	if l1 == l2 {
		t.Fatalf("beide Aktionen im selben Satz %q", l1)
	}
	if _, err := m.Rollback(ProfileServer); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(dir, "a.jar")) || exists(filepath.Join(dir, "b.jar")) {
		t.Fatal("erster Rollback muss nur b.jar zurücknehmen")
	}
	if _, err := m.Rollback(ProfileServer); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(dir, "a.jar")) {
		t.Fatal("zweiter Rollback muss a.jar zurücknehmen")
	}
}

func TestNewLabelAvoidsTakenNames(t *testing.T) {
	_, p, dir := testProfile(t)
	now := time.Date(2026, 11, 10, 21, 10, 40, 123e6, time.Local)
	first := newLabel(p, now)
	os.MkdirAll(filepath.Join(dir, ".backup", first), 0o755)
	second := newLabel(p, now)
	os.MkdirAll(filepath.Join(dir, ".backup", second+rolledBackSuffix), 0o755)
	third := newLabel(p, now)
	if first != "2026-11-10_21-10-40.123" || second == first || third == second || third == first {
		t.Fatalf("labels = %q, %q, %q", first, second, third)
	}
	if !(first < second && second < third) {
		t.Errorf("Reihenfolge nicht zeitlich sortierbar: %q %q %q", first, second, third)
	}
}

// Rollback darf nicht hinter ein MC-Update zurück (setzte Mods der alten
// Version ein); Änderungen danach bleiben normal zurückrollbar.
func TestRollbackStopsAtSealedUpgradeSet(t *testing.T) {
	m, p, dir := testProfile(t)
	os.WriteFile(filepath.Join(dir, "api-1.21.jar"), []byte("alt"), 0o644)
	stage(t, p, map[string]string{"api-26.3.jar": "neu"},
		stagingItem{Category: "mods", OldFilename: "api-1.21.jar", NewFilename: "api-26.3.jar", Name: "API"})
	up, _, err := m.ApplyStaged(ProfileServer)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SealLabel(ProfileServer, up, "26.3"); err != nil {
		t.Fatal(err)
	}
	stage(t, p, map[string]string{"jade.jar": "j"}, stagingItem{Category: "mods", NewFilename: "jade.jar", Name: "Jade"})
	if _, _, err := m.ApplyStaged(ProfileServer); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Rollback(ProfileServer); err != nil {
		t.Fatalf("Rollback der Änderung nach dem Update: %v", err)
	}
	_, err = m.Rollback(ProfileServer)
	if err == nil || !strings.Contains(err.Error(), "26.3") {
		t.Fatalf("err = %v, want Sperre mit Versionshinweis", err)
	}
	if !exists(filepath.Join(dir, "api-26.3.jar")) || exists(filepath.Join(dir, "api-1.21.jar")) {
		t.Error("Mods des Updates wurden trotzdem zurückgerollt")
	}
	// gezielter Rückfall des Updates selbst bleibt möglich
	if _, err := m.RollbackLabel(ProfileServer, up); err != nil {
		t.Errorf("RollbackLabel: %v", err)
	}
}
