package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPackFingerprintExtra(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "config", "d"), 0o755)
	f := filepath.Join(root, "config", "a.cfg")
	os.WriteFile(f, []byte("aaaa"), 0o644)
	extra := []string{"config/a.cfg"}
	fp1 := packFingerprint(nil, root, extra, nil)
	os.WriteFile(f, []byte("bbbb"), 0o644) // gleiche Größe, anderer Inhalt
	if fp2 := packFingerprint(nil, root, extra, nil); fp1 == fp2 {
		t.Error("Fingerprint ändert sich nicht bei gleich großer Config-Änderung")
	}
	base := packFingerprint(nil, root, extra, nil)
	if got := packFingerprint(nil, root, append([]string{"config/d"}, extra...), nil); got != base {
		t.Error("Verzeichnis darf den Fingerprint nicht beeinflussen")
	}
}

func TestPackFingerprintOverrideContent(t *testing.T) {
	root := t.TempDir()
	rp := filepath.Join(root, "resourcepacks")
	os.MkdirAll(rp, 0o755)
	f := filepath.Join(rp, "helper.zip")
	os.WriteFile(f, []byte("aaaa"), 0o644)
	dirs := map[string]string{"resourcepacks": rp}
	content := map[string]bool{"resourcepacks/helper.zip": true}
	fp1 := packFingerprint(dirs, root, nil, content)
	plain1 := packFingerprint(dirs, root, nil, nil)
	os.WriteFile(f, []byte("bbbb"), 0o644) // gleiche Größe, anderer Inhalt
	if fp2 := packFingerprint(dirs, root, nil, content); fp1 == fp2 {
		t.Error("Fingerprint ändert sich nicht bei gleich großer Änderung einer mitgelieferten Pack-Datei")
	}
	if plain2 := packFingerprint(dirs, root, nil, nil); plain1 != plain2 {
		t.Error("ohne Inhalts-Liste muss der Fingerprint wie bisher nur Name + Größe nutzen")
	}
}

func TestPackFingerprintOverrideCached(t *testing.T) {
	root := t.TempDir()
	rp := filepath.Join(root, "resourcepacks")
	os.MkdirAll(rp, 0o755)
	f := filepath.Join(rp, "helper.zip")
	os.WriteFile(f, []byte("aaaa"), 0o644)
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	os.Chtimes(f, stamp, stamp)
	dirs := map[string]string{"resourcepacks": rp}
	content := map[string]bool{"resourcepacks/helper.zip": true}
	fp1 := packFingerprint(dirs, root, nil, content)
	// gleiche Größe + Änderungszeit: Cache-Treffer, Datei wird nicht neu gelesen
	os.WriteFile(f, []byte("bbbb"), 0o644)
	os.Chtimes(f, stamp, stamp)
	if fp2 := packFingerprint(dirs, root, nil, content); fp2 != fp1 {
		t.Error("Inhalts-Hash nicht aus dem Cache (Datei neu gelesen)")
	}
	later := stamp.Add(time.Minute)
	os.Chtimes(f, later, later)
	if fp3 := packFingerprint(dirs, root, nil, content); fp3 == fp1 {
		t.Error("geänderte Änderungszeit muss neu hashen")
	}
}
