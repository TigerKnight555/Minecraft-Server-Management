package modpack

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/basepack"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/modrinth"
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

// overrideEnv: Client-Paket mit resourcepacks/ und den gegebenen
// mitgelieferten Basis-Pack-Dateien (Name → Inhalt).
func overrideEnv(t *testing.T, files map[string]string) (*env, string) {
	t.Helper()
	e := newEnv(t, "approved")
	root := filepath.Dir(e.mods)
	rpDir := filepath.Join(root, "resourcepacks")
	os.MkdirAll(rpDir, 0o755)
	e.ch.dirs["resourcepacks"] = rpDir
	e.addMod("sodium.jar", "P-sod", "1", true)
	idx := map[string]basepack.PackFile{}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(rpDir, name), []byte(body), 0o644); err != nil {
			t.Skipf("Dateiname %q auf diesem System nicht möglich: %v", name, err)
		}
		idx[sha([]byte(body))] = basepack.PackFile{Path: "resourcepacks/" + name, Category: "resourcepacks",
			Filename: name, SHA1: "x", SHA512: sha([]byte(body)), Size: int64(len(body)), Override: true}
	}
	e.ch.SetBasepack(root, func() []string { return nil }, func() map[string]basepack.PackFile { return idx })
	return e, rpDir
}

func packEntries(t *testing.T, pack []byte) map[string]bool {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(pack), int64(len(pack)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, f := range zr.File {
		out[f.Name] = true
	}
	return out
}

func TestOverrideInvalidPathOmitted(t *testing.T) {
	e, rpDir := overrideEnv(t, map[string]string{"ok.zip": "OK"})
	// Ordner unter einem Kategorie-Namen, der keinen gültigen .mrpack-Pfad
	// ergibt (Dateinamen mit „\" oder „:" sind nicht auf jedem System möglich)
	bad := filepath.Join(filepath.Dir(rpDir), "bad")
	os.MkdirAll(bad, 0o755)
	os.WriteFile(filepath.Join(bad, "x.zip"), []byte("X"), 0o644)
	e.ch.dirs["re:s"] = bad
	idx := e.ch.bpIndex()
	idx[sha([]byte("X"))] = basepack.PackFile{Path: "re:s/x.zip", Category: "re:s", Filename: "x.zip",
		SHA1: "x", SHA512: sha([]byte("X")), Size: 1, Override: true}
	if _, err := e.ch.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := packEntries(t, e.api.packs[0])
	if !got["overrides/resourcepacks/ok.zip"] {
		t.Error("ok.zip fehlt")
	}
	if st := e.ch.Status(); strings.Join(st.Omitted, ",") != "x.zip" {
		t.Errorf("omitted = %v", st.Omitted)
	}
}

func TestOverrideReadErrorOmittedNotFatal(t *testing.T) {
	old := maxLiveOverride
	maxLiveOverride = 3
	defer func() { maxLiveOverride = old }()
	e, _ := overrideEnv(t, map[string]string{"big.zip": "zu groß", "ok.zip": "OK"})
	var buf bytes.Buffer
	e.ch.log = slog.New(slog.NewTextHandler(&buf, nil))
	if _, err := e.ch.Publish(context.Background()); err != nil {
		t.Fatalf("Publish abgebrochen: %v", err)
	}
	got := packEntries(t, e.api.packs[0])
	if got["overrides/resourcepacks/big.zip"] || !got["overrides/resourcepacks/ok.zip"] {
		t.Errorf("pack = %v", got)
	}
	if st := e.ch.Status(); strings.Join(st.Omitted, ",") != "big.zip" {
		t.Errorf("omitted = %v", st.Omitted)
	}
	if !strings.Contains(buf.String(), "big.zip") {
		t.Errorf("log = %q, want Zeile zu big.zip", buf.String())
	}
}

func TestOverrideTotalCap(t *testing.T) {
	old := maxOverrideBytes
	maxOverrideBytes = 5
	defer func() { maxOverrideBytes = old }()
	e, _ := overrideEnv(t, map[string]string{"a.zip": "AAA", "b.zip": "BBB"})
	if _, err := e.ch.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := packEntries(t, e.api.packs[0])
	if !got["overrides/resourcepacks/a.zip"] || got["overrides/resourcepacks/b.zip"] {
		t.Errorf("pack = %v", got)
	}
	if st := e.ch.Status(); strings.Join(st.Omitted, ",") != "b.zip" {
		t.Errorf("omitted = %v", st.Omitted)
	}
}

func TestOverrideKnownByModrinthGoesToIndex(t *testing.T) {
	e, _ := overrideEnv(t, map[string]string{"helper.zip": "HELP"})
	v := modrinth.Version{ProjectID: "P-help", VersionNumber: "2"}
	f := modrinth.File{URL: "https://cdn.modrinth.com/data/P-help/helper.zip", Filename: "helper.zip", Size: 4}
	f.Hashes.SHA512 = sha([]byte("HELP"))
	f.Hashes.SHA1 = "sha1-help"
	v.Files = []modrinth.File{f}
	e.api.known[sha([]byte("HELP"))] = v
	if _, err := e.ch.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	pack := e.api.packs[0]
	if packEntries(t, pack)["overrides/resourcepacks/helper.zip"] {
		t.Error("von Modrinth bekannte Datei als Override gepackt")
	}
	found := false
	for _, x := range indexOf(t, pack)["files"].([]any) {
		m := x.(map[string]any)
		if m["path"] == "resourcepacks/helper.zip" {
			found = m["downloads"].([]any)[0] == "https://cdn.modrinth.com/data/P-help/helper.zip"
		}
	}
	if !found {
		t.Error("helper.zip fehlt im Index mit CDN-URL")
	}
}
