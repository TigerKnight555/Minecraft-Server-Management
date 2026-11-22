package modpack

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/basepack"
)

func TestBasepackOverrideFilesAsOverrides(t *testing.T) {
	e := newEnv(t, "approved")
	root := filepath.Dir(e.mods)
	rpDir := filepath.Join(root, "resourcepacks")
	os.MkdirAll(rpDir, 0o755)
	e.ch.dirs["resourcepacks"] = rpDir
	e.addMod("sodium.jar", "P-sod", "1", true)
	os.WriteFile(filepath.Join(rpDir, "Mod Menu Helper.zip"), []byte("RP1"), 0o644)
	idx := map[string]basepack.PackFile{sha([]byte("RP1")): {Path: "resourcepacks/Mod Menu Helper.zip", Category: "resourcepacks",
		Filename: "Mod Menu Helper.zip", SHA1: "x", SHA512: sha([]byte("RP1")), Size: 3, Override: true, Name: "Mod Menu Helper"}}
	e.ch.SetBasepack(root, func() []string { return nil }, func() map[string]basepack.PackFile { return idx })
	if _, err := e.ch.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	pack := e.api.packs[0]
	for _, f := range indexOf(t, pack)["files"].([]any) {
		if p := f.(map[string]any)["path"].(string); strings.Contains(p, "Mod Menu") {
			t.Errorf("Override-Datei im Index: %s", p)
		}
	}
	zr, _ := zip.NewReader(bytes.NewReader(pack), int64(len(pack)))
	found := false
	for _, f := range zr.File {
		if f.Name == "overrides/resourcepacks/Mod Menu Helper.zip" {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			found = string(b) == "RP1"
		}
	}
	if !found {
		t.Error("overrides/resourcepacks/Mod Menu Helper.zip fehlt oder falscher Inhalt")
	}
	if st := e.ch.Status(); len(st.Omitted) != 0 {
		t.Errorf("omitted = %v", st.Omitted)
	}
	// geänderter Inhalt → Changelog nennt das Resourcepack als aktualisiert
	os.WriteFile(filepath.Join(rpDir, "Mod Menu Helper.zip"), []byte("RP2"), 0o644)
	idx = map[string]basepack.PackFile{sha([]byte("RP2")): {Path: "resourcepacks/Mod Menu Helper.zip", Category: "resourcepacks",
		Filename: "Mod Menu Helper.zip", SHA1: "y", SHA512: sha([]byte("RP2")), Size: 3, Override: true}}
	if _, err := e.ch.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	cl := e.api.created[len(e.api.created)-1].Changelog
	if !strings.Contains(cl, "Aktualisiert") || !strings.Contains(cl, "Mod Menu Helper.zip") {
		t.Errorf("changelog = %q", cl)
	}
}
