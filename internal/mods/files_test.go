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
