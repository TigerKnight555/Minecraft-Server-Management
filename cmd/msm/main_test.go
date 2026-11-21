package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPackFingerprintExtra(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "config", "d"), 0o755)
	f := filepath.Join(root, "config", "a.cfg")
	os.WriteFile(f, []byte("aaaa"), 0o644)
	extra := []string{"config/a.cfg"}
	fp1 := packFingerprint(nil, root, extra)
	os.WriteFile(f, []byte("bbbb"), 0o644) // gleiche Größe, anderer Inhalt
	if fp2 := packFingerprint(nil, root, extra); fp1 == fp2 {
		t.Error("Fingerprint ändert sich nicht bei gleich großer Config-Änderung")
	}
	base := packFingerprint(nil, root, extra)
	if got := packFingerprint(nil, root, append([]string{"config/d"}, extra...)); got != base {
		t.Error("Verzeichnis darf den Fingerprint nicht beeinflussen")
	}
}
