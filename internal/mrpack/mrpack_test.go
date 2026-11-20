package mrpack

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"testing"
)

func readIndex(t *testing.T, data []byte) map[string]any {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 1 || zr.File[0].Name != "modrinth.index.json" {
		t.Fatalf("zip entries = %v, want nur modrinth.index.json", zr.File)
	}
	rc, _ := zr.File[0].Open()
	raw, _ := io.ReadAll(rc)
	var idx map[string]any
	if err := json.Unmarshal(raw, &idx); err != nil {
		t.Fatal(err)
	}
	return idx
}

func TestBuildIndex(t *testing.T) {
	data, err := Build([]File{
		{Path: "mods/sodium.jar", SHA1: "s1", SHA512: "s512", URL: "https://cdn.modrinth.com/data/a/sodium.jar", Size: 10},
		{Path: "resourcepacks/faithful.zip", SHA1: "f1", SHA512: "f512", URL: "https://cdn.modrinth.com/data/b/faithful.zip", Size: 20},
	}, Meta{VersionID: "1.5.0", Name: "Mein Pack", Minecraft: "26.3", FabricLoader: "0.20.1"})
	if err != nil {
		t.Fatal(err)
	}
	idx := readIndex(t, data)
	if idx["formatVersion"].(float64) != 1 || idx["game"] != "minecraft" || idx["versionId"] != "1.5.0" || idx["name"] != "Mein Pack" {
		t.Errorf("kopf = %v", idx)
	}
	deps := idx["dependencies"].(map[string]any)
	if deps["minecraft"] != "26.3" || deps["fabric-loader"] != "0.20.1" || len(deps) != 2 {
		t.Errorf("dependencies = %v", deps)
	}
	files := idx["files"].([]any)
	if len(files) != 2 {
		t.Fatalf("files = %v", files)
	}
	first := files[0].(map[string]any) // sortiert nach Pfad: mods/ vor resourcepacks/
	hashes := first["hashes"].(map[string]any)
	if first["path"] != "mods/sodium.jar" || hashes["sha1"] != "s1" || hashes["sha512"] != "s512" ||
		first["fileSize"].(float64) != 10 || first["downloads"].([]any)[0] != "https://cdn.modrinth.com/data/a/sodium.jar" {
		t.Errorf("file = %v", first)
	}
	if _, ok := first["env"]; ok {
		t.Error("env darf fehlen (alles ist Client-Inhalt)")
	}
}

func TestBuildRejectsBadInput(t *testing.T) {
	meta := Meta{VersionID: "1.0.0", Name: "P", Minecraft: "26.3", FabricLoader: "0.20.1"}
	for _, f := range []File{
		{Path: "../evil.jar", SHA1: "a", SHA512: "b", URL: "https://cdn.modrinth.com/x"},
		{Path: "/abs.jar", SHA1: "a", SHA512: "b", URL: "https://cdn.modrinth.com/x"},
		{Path: "C:/x.jar", SHA1: "a", SHA512: "b", URL: "https://cdn.modrinth.com/x"},
		{Path: "mods\\x.jar", SHA1: "a", SHA512: "b", URL: "https://cdn.modrinth.com/x"},
		{Path: "mods/x.jar", SHA1: "", SHA512: "b", URL: "https://cdn.modrinth.com/x"},
	} {
		if _, err := Build([]File{f}, meta); err == nil {
			t.Errorf("Build(%+v) ohne Fehler", f)
		}
	}
	if _, err := Build(nil, Meta{Name: "P"}); err == nil {
		t.Error("fehlende Pflicht-Metadaten nicht erkannt")
	}
}

func TestValidPath(t *testing.T) {
	for p, want := range map[string]bool{
		"mods/a.jar": true, "shaderpacks/x y.zip": true,
		"": false, "a/../b": false, "./a": false, "a//b": false,
	} {
		if got := ValidPath(p); got != want {
			t.Errorf("ValidPath(%q) = %v, want %v", p, got, want)
		}
	}
}
