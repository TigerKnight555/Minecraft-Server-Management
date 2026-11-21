package mods

import (
	"crypto/sha512"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func hexsum(b []byte) string { s := sha512.Sum512(b); return hex.EncodeToString(s[:]) }

func TestApplyFilesAddsReplacesRemovesAndSeals(t *testing.T) {
	m, _, dir := testProfile(t)
	os.WriteFile(filepath.Join(dir, "old.jar"), []byte("old"), 0o644)
	os.WriteFile(filepath.Join(dir, "gone.jar"), []byte("g"), 0o644)
	var hooked int
	m.SetChangeHook(func(string, []Change) { hooked++ })

	label, err := m.ApplyFiles(ProfileServer, []FileAdd{
		{Category: "mods", Filename: "new.jar", Data: []byte("new"), SHA512: hexsum([]byte("new")), Name: "New"},
		{Category: "mods", Filename: "old-2.jar", Data: []byte("old2"), SHA512: hexsum([]byte("old2")), Replaces: "old.jar"},
	}, []FileRemove{{Category: "mods", Filename: "gone.jar"}}, "Fabulously Optimized 6.5.0")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"new.jar", "old-2.jar"} {
		if !exists(filepath.Join(dir, f)) {
			t.Errorf("%s fehlt", f)
		}
	}
	if exists(filepath.Join(dir, "old.jar")) || exists(filepath.Join(dir, "gone.jar")) {
		t.Error("ersetzte/entfernte Datei liegt noch im Live-Ordner")
	}
	if !exists(filepath.Join(dir, ".backup", label, "old.jar")) {
		t.Error("alte Datei nicht gesichert")
	}
	if hooked != 0 {
		t.Error("ApplyFiles darf den Change-Hook nicht rufen (Aufrufer meldet selbst)")
	}
	_, err = m.Rollback(ProfileServer)
	if err == nil || !strings.Contains(err.Error(), "Basis-Modpack") || !strings.Contains(err.Error(), "6.5.0") {
		t.Fatalf("Rollback err = %v, want Stopp am Basis-Pack-Satz", err)
	}
	if _, err := m.RollbackLabel(ProfileServer, label); err != nil {
		t.Errorf("gezieltes RollbackLabel muss möglich bleiben: %v", err)
	}
}

func TestApplyFilesRejectsBadHashAndPending(t *testing.T) {
	m, p, _ := testProfile(t)
	if _, err := m.ApplyFiles(ProfileServer, []FileAdd{{Category: "mods", Filename: "x.jar", Data: []byte("x"), SHA512: "falsch"}}, nil, ""); err == nil {
		t.Error("falsche Prüfsumme akzeptiert")
	}
	stage(t, p, map[string]string{"y.jar": "y"}, stagingItem{Category: "mods", NewFilename: "y.jar", Name: "Y"})
	if _, err := m.ApplyFiles(ProfileServer, []FileAdd{{Category: "mods", Filename: "z.jar", Data: []byte("z"), SHA512: hexsum([]byte("z"))}}, nil, ""); err == nil {
		t.Error("ApplyFiles trotz vorgemerkter Änderungen")
	}
}

func TestPinnedFilesGetNoUpdates(t *testing.T) {
	m, _, dir := testProfile(t)
	os.WriteFile(filepath.Join(dir, "pinned.jar"), []byte("p"), 0o644)
	m.SetPinned(func(profile, filename string) bool { return filename == "pinned.jar" })
	if !m.pinned(ProfileServer, "pinned.jar") || m.pinned(ProfileServer, "other.jar") {
		t.Error("pinned-Abfrage falsch")
	}
}

func readF(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestApplyFilesPartialFailureMarksAndCleansUp(t *testing.T) {
	root := t.TempDir()
	mods := filepath.Join(root, "mods")
	os.MkdirAll(mods, 0o755)
	cfg := filepath.Join(root, "config")
	os.WriteFile(cfg, []byte("ich bin eine Datei"), 0o644) // Ziel-Ordner unbrauchbar
	p := Profile{Name: ProfileServer, Dirs: map[string]string{"mods": mods, "config": cfg}}
	m := NewManager(nil, "fabric", []Profile{p})

	label, err := m.ApplyFiles(ProfileServer, []FileAdd{
		{Category: "mods", Filename: "a.jar", Data: []byte("a"), SHA512: hexsum([]byte("a"))},
		{Category: "config", Filename: "b.cfg", Data: []byte("b"), SHA512: hexsum([]byte("b"))},
	}, nil, "Pack 1")
	if err == nil || !strings.Contains(err.Error(), "RollbackLabel") {
		t.Fatalf("err = %v, want Teilfehler mit Hinweis", err)
	}
	if !exists(filepath.Join(mods, "a.jar")) {
		t.Error("bereits angewendetes Item fehlt")
	}
	if !exists(filepath.Join(mods, ".backup", label, basepackFile)) {
		t.Error(".basepack-Marker fehlt im Teilsatz")
	}
	if exists(filepath.Join(stagingDir(p), "manifest.json")) || exists(filepath.Join(stagingDir(p), "b.cfg")) {
		t.Error("Staging nicht aufgeräumt")
	}
	if _, err := m.ApplyFiles(ProfileServer, []FileAdd{{Category: "mods", Filename: "c.jar", Data: []byte("c"), SHA512: hexsum([]byte("c"))}}, nil, ""); err != nil {
		t.Fatalf("Wiederholung scheitert: %v", err)
	}
}

func TestApplyFilesBacksUpSameNameFile(t *testing.T) {
	m, _, dir := testProfile(t)
	os.WriteFile(filepath.Join(dir, "x.jar"), []byte("old"), 0o644)
	label, err := m.ApplyFiles(ProfileServer, []FileAdd{{Category: "mods", Filename: "x.jar", Data: []byte("new"), SHA512: hexsum([]byte("new"))}}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := readF(t, filepath.Join(dir, "x.jar")); got != "new" {
		t.Errorf("live x.jar = %q", got)
	}
	if got := readF(t, filepath.Join(dir, ".backup", label, "x.jar")); got != "old" {
		t.Errorf("backup x.jar = %q", got)
	}
}

// Identische vorhandene Datei: nichts zu tun, Label "" (kein Satz).
func TestApplyFilesSkipsIdenticalExisting(t *testing.T) {
	m, p, dir := testProfile(t)
	os.WriteFile(filepath.Join(dir, "y.jar"), []byte("c"), 0o644)
	label, err := m.ApplyFiles(ProfileServer, []FileAdd{{Category: "mods", Filename: "y.jar", Data: []byte("c"), SHA512: hexsum([]byte("c"))}}, nil, "M")
	if err != nil || label != "" {
		t.Fatalf("label=%q err=%v, want leer, nil", label, err)
	}
	if exists(filepath.Join(dir, ".backup")) || exists(stagingDir(p)) {
		t.Error("es darf nichts angelegt werden")
	}
}

func TestApplyFilesRejectsDuplicateNamesAndUnknownRemoveCategory(t *testing.T) {
	m, _, _ := testProfile(t)
	add := func(n, c string) FileAdd {
		return FileAdd{Category: "mods", Filename: n, Data: []byte(c), SHA512: hexsum([]byte(c))}
	}
	if _, err := m.ApplyFiles(ProfileServer, []FileAdd{add("d.jar", "1"), add("d.jar", "2")}, nil, ""); err == nil {
		t.Error("doppelter Dateiname akzeptiert")
	}
	r := add("e.jar", "e")
	r2 := add("f.jar", "f")
	r2.Replaces = "e.jar"
	if _, err := m.ApplyFiles(ProfileServer, []FileAdd{r, r2}, nil, ""); err == nil {
		t.Error("Add-Name = Replaces eines anderen Adds akzeptiert")
	}
	if _, err := m.ApplyFiles(ProfileServer, nil, []FileRemove{{Category: "nix", Filename: "g.jar"}}, ""); err == nil {
		t.Error("unbekannte Remove-Kategorie akzeptiert")
	}
}

func TestApplyFilesCorruptManifestIsNotOverwritten(t *testing.T) {
	m, p, _ := testProfile(t)
	os.MkdirAll(stagingDir(p), 0o755)
	mp := filepath.Join(stagingDir(p), "manifest.json")
	os.WriteFile(mp, []byte("{kaputt"), 0o644)
	if _, err := m.ApplyFiles(ProfileServer, []FileAdd{{Category: "mods", Filename: "z.jar", Data: []byte("z"), SHA512: hexsum([]byte("z"))}}, nil, ""); err == nil {
		t.Error("kaputte Staging-Liste ignoriert")
	}
	if readF(t, mp) != "{kaputt" {
		t.Error("kaputte Staging-Liste überschrieben")
	}
}

// Teilfehler, danach Wiederholung desselben Aufrufs: die bereits eingesetzte
// neue Datei (alte schon in der Sicherung) darf die Wiederholung nicht
// blockieren (Final-Review Important 1).
func TestApplyFilesRetryAfterPartialFailure(t *testing.T) {
	root := t.TempDir()
	modsDir := filepath.Join(root, "mods")
	os.MkdirAll(modsDir, 0o755)
	cfg := filepath.Join(root, "config")
	os.WriteFile(cfg, []byte("ich bin eine Datei"), 0o644) // Ziel-Ordner unbrauchbar
	os.WriteFile(filepath.Join(modsDir, "y.jar"), []byte("y"), 0o644)
	p := Profile{Name: ProfileServer, Dirs: map[string]string{"mods": modsDir, "config": cfg}}
	m := NewManager(nil, "fabric", []Profile{p})
	adds := []FileAdd{
		{Category: "mods", Filename: "n.jar", Data: []byte("n"), SHA512: hexsum([]byte("n")), Replaces: "y.jar"},
		{Category: "config", Filename: "b.cfg", Data: []byte("b"), SHA512: hexsum([]byte("b"))},
	}
	if _, err := m.ApplyFiles(ProfileServer, adds, nil, "Pack 2"); err == nil {
		t.Fatal("Testvoraussetzung: erster Lauf muss teilweise scheitern")
	}
	if !exists(filepath.Join(modsDir, "n.jar")) || exists(filepath.Join(modsDir, "y.jar")) {
		t.Fatal("Testvoraussetzung: n.jar live, y.jar gesichert")
	}
	os.Remove(cfg)
	os.MkdirAll(cfg, 0o755)
	if _, err := m.ApplyFiles(ProfileServer, adds, nil, "Pack 2"); err != nil {
		t.Fatalf("Wiederholung scheitert: %v", err)
	}
	if got := readF(t, filepath.Join(modsDir, "n.jar")); got != "n" {
		t.Errorf("n.jar = %q", got)
	}
	if !exists(filepath.Join(cfg, "b.cfg")) {
		t.Error("b.cfg fehlt nach Wiederholung")
	}
}

// Absturz zwischen Einsetzen und Sichern der alten Datei bzw. beide liegen
// live: neue Datei identisch → nicht neu einsetzen, die alte aber entfernen.
func TestApplyFilesIdenticalNewWithOldStillLive(t *testing.T) {
	m, _, dir := testProfile(t)
	os.WriteFile(filepath.Join(dir, "y.jar"), []byte("y"), 0o644)
	os.WriteFile(filepath.Join(dir, "n.jar"), []byte("n"), 0o644)
	label, err := m.ApplyFiles(ProfileServer, []FileAdd{
		{Category: "mods", Filename: "n.jar", Data: []byte("n"), SHA512: hexsum([]byte("n")), Replaces: "y.jar"},
	}, nil, "Pack 2")
	if err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(dir, "y.jar")) {
		t.Error("alte Datei y.jar liegt noch live")
	}
	if !exists(filepath.Join(dir, ".backup", label, "y.jar")) {
		t.Error("alte Datei nicht gesichert")
	}
	if got := readF(t, filepath.Join(dir, "n.jar")); got != "n" {
		t.Errorf("n.jar = %q", got)
	}
	// abweichender Inhalt unter dem neuen Namen bleibt ein Fehler
	os.WriteFile(filepath.Join(dir, "z.jar"), []byte("fremd"), 0o644)
	os.WriteFile(filepath.Join(dir, "w.jar"), []byte("w"), 0o644)
	if _, err := m.ApplyFiles(ProfileServer, []FileAdd{
		{Category: "mods", Filename: "z.jar", Data: []byte("z"), SHA512: hexsum([]byte("z")), Replaces: "w.jar"},
	}, nil, ""); err == nil {
		t.Error("fremde gleichnamige Datei würde überschrieben")
	}
}
