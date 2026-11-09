package mods

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteManifestAtomicNoTempLeft(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	man := stagingManifest{Items: []stagingItem{{OldFilename: "a.jar", NewFilename: "b.jar"}}}
	if err := writeManifest(path, man); err != nil {
		t.Fatal(err)
	}
	tmps, _ := filepath.Glob(filepath.Join(dir, ".manifest-*.tmp"))
	if len(tmps) != 0 {
		t.Errorf("Temp-Dateien übrig: %v", tmps)
	}
	got, err := readManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 || got.Items[0].NewFilename != "b.jar" {
		t.Errorf("round-trip: %+v", got)
	}
	if _, err := os.Stat(path); err != nil {
		t.Error(err)
	}
}
