package basepack

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mrpack"
)

// rawPack baut ein .mrpack mit leerem Index und beliebigen ZIP-Einträgen.
func rawPack(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("modrinth.index.json")
	w.Write([]byte(`{"formatVersion":1,"game":"minecraft","versionId":"1","name":"FO","files":[],"dependencies":{"minecraft":"26.3"}}`))
	for name, body := range entries {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestFromIndexOverridePackFiles(t *testing.T) {
	idx, err := mrpack.Parse(rawPack(t, map[string]string{
		"overrides/resourcepacks/Mod Menu Helper.zip": "RP",
		"overrides/shaderpacks/y.zip":                 "alt",
		"client-overrides/shaderpacks/y.zip":          "client",
		"overrides/mods/bundled.jar":                  "JAR",
		"overrides/resourcepacks/sub/nested.zip":      "N",
		"server-overrides/mods/server.jar":            "S",
		"overrides/config/a.json":                     "A",
	}))
	if err != nil {
		t.Fatal(err)
	}
	files, configs, ignored := FromIndex(idx)
	got := map[string]PackFile{}
	for _, f := range files {
		got[f.Path] = f
	}
	if len(files) != 3 {
		t.Fatalf("files = %+v, want 3 Override-Dateien", files)
	}
	rp := got["resourcepacks/Mod Menu Helper.zip"]
	if !rp.Override || rp.Category != "resourcepacks" || rp.Filename != "Mod Menu Helper.zip" ||
		rp.SHA512 != sum([]byte("RP")) || rp.SHA1 == "" || rp.Size != 2 || rp.URL != "" {
		t.Errorf("resourcepack = %+v", rp)
	}
	if sp := got["shaderpacks/y.zip"]; !sp.Override || sp.SHA512 != sum([]byte("client")) {
		t.Errorf("shaderpack = %+v, want client-overrides-Inhalt", sp)
	}
	if m := got["mods/bundled.jar"]; !m.Override || m.Category != "mods" {
		t.Errorf("mod = %+v", m)
	}
	if _, ok := got["mods/server.jar"]; ok {
		t.Error("server-overrides übernommen")
	}
	if strings.Join(configs, ",") != "config/a.json" {
		t.Errorf("configs = %v", configs)
	}
	if strings.Join(ignored, ",") != "resourcepacks/sub/nested.zip" {
		t.Errorf("ignored = %v", ignored)
	}
}

func TestApplyOverrideFileUpdateRemoveToggle(t *testing.T) {
	s, api, files, _, _ := setup(t)
	ctx := context.Background()
	rp := filepath.Join(files.root, "resourcepacks", "Mod Menu Helper.zip")
	api.addPackVersion(t, "26.3", "6.4.0", map[string]string{"sodium-1.jar": "s1"},
		map[string]string{"resourcepacks/Mod Menu Helper.zip": "RP1", "shaderpacks/sh.zip": "SH1", "config/a.json": "A"})

	pv, err := s.Preview(ctx, "FO", "26.3")
	if err != nil {
		t.Fatal(err)
	}
	if len(pv.Plan.Add) != 3 || len(pv.Ignored) != 0 {
		t.Fatalf("preview add = %+v ignored = %v", pv.Plan.Add, pv.Ignored)
	}
	if _, err := s.Apply(ctx, "FO", "6.4.0", "26.3", nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(rp); string(b) != "RP1" {
		t.Fatalf("resourcepack = %q, want RP1", b)
	}
	st, _ := s.State()
	var key string
	for _, f := range st.Files {
		if f.Filename == "Mod Menu Helper.zip" {
			key = Key(f)
			if !f.Override || f.Name != "Mod Menu Helper" || f.SHA512 != sum([]byte("RP1")) {
				t.Errorf("state file = %+v", f)
			}
		}
	}
	if key != "resourcepacks/Mod Menu Helper.zip" || !s.Pinned("Mod Menu Helper.zip") {
		t.Fatalf("key = %q pinned = %v", key, s.Pinned("Mod Menu Helper.zip"))
	}

	// Ab- und wieder anschalten: Bytes kommen aus dem Pack, kein Download.
	if err := s.Toggle(ctx, key, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(rp); err == nil {
		t.Fatal("abgeschaltetes Resourcepack liegt noch im Paket")
	}
	if err := s.Toggle(ctx, key, true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(rp); string(b) != "RP1" {
		t.Fatalf("nach Anschalten = %q, want RP1", b)
	}

	// Neue Pack-Version: Resourcepack geändert, Shaderpack weg.
	api.addPackVersion(t, "26.3", "6.5.0", map[string]string{"sodium-1.jar": "s1"},
		map[string]string{"resourcepacks/Mod Menu Helper.zip": "RP2"})
	pv, err = s.Preview(ctx, "FO", "26.3")
	if err != nil {
		t.Fatal(err)
	}
	if len(pv.Plan.Update) != 1 || pv.Plan.Update[0].New.Filename != "Mod Menu Helper.zip" ||
		len(pv.Plan.Remove) != 1 || pv.Plan.Remove[0].Filename != "sh.zip" || len(pv.Plan.Add) != 0 {
		t.Fatalf("plan = %+v", pv.Plan)
	}
	if _, err := s.Apply(ctx, "FO", "6.5.0", "26.3", nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(rp); string(b) != "RP2" {
		t.Errorf("resourcepack nach Update = %q, want RP2", b)
	}
	if _, err := os.Stat(filepath.Join(files.root, "shaderpacks", "sh.zip")); err == nil {
		t.Error("entferntes Shaderpack liegt noch im Paket")
	}
	if m := s.IndexFiles()[sum([]byte("RP2"))]; !m.Override {
		t.Errorf("IndexFiles = %+v, want Override", m)
	}
	if of := s.OverrideFiles(); len(of) != 1 || !of["resourcepacks/Mod Menu Helper.zip"] {
		t.Errorf("OverrideFiles = %v", of)
	}
}

func TestToggleOnOverrideVerifiesPackBytes(t *testing.T) {
	s, api, files, kv, _ := setup(t)
	ctx := context.Background()
	api.addPackVersion(t, "26.3", "6.4.0", nil, map[string]string{"resourcepacks/r.zip": "R1"})
	if _, err := s.Apply(ctx, "FO", "6.4.0", "26.3", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Toggle(ctx, "resourcepacks/r.zip", false); err != nil {
		t.Fatal(err)
	}
	// gespeicherter Hash passt nicht mehr zu den Pack-Bytes → Abbruch
	kv[stateKey] = strings.Replace(kv[stateKey], sum([]byte("R1")), sum([]byte("anders")), 1)
	if err := s.Toggle(ctx, "resourcepacks/r.zip", true); err == nil {
		t.Error("Toggle mit falschem Hash ohne Fehler")
	}
	if _, err := os.Stat(filepath.Join(files.root, "resourcepacks", "r.zip")); err == nil {
		t.Error("Datei trotz Hash-Fehler geschrieben")
	}
}
