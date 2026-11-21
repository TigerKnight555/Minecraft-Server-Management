package mrpack

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
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

func TestParseRoundTripWithOverrides(t *testing.T) {
	data, err := Build([]File{
		{Path: "mods/sodium.jar", SHA1: "s1", SHA512: "s512", URL: "https://cdn.modrinth.com/a/sodium.jar", Size: 10},
	}, Meta{VersionID: "6.4.0", Name: "FO", Minecraft: "26.3", FabricLoader: "0.20.1",
		Overrides: map[string][]byte{"config/a.json": []byte("A"), "options.txt": []byte("O")}})
	if err != nil {
		t.Fatal(err)
	}
	idx, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if idx.Name != "FO" || idx.VersionID != "6.4.0" || idx.Dependencies["minecraft"] != "26.3" {
		t.Errorf("kopf = %+v", idx)
	}
	if len(idx.Files) != 1 || idx.Files[0].Path != "mods/sodium.jar" || idx.Files[0].SHA1 != "s1" ||
		idx.Files[0].SHA512 != "s512" || idx.Files[0].URLs[0] != "https://cdn.modrinth.com/a/sodium.jar" || idx.Files[0].Size != 10 {
		t.Errorf("files = %+v", idx.Files)
	}
	if string(idx.Overrides["config/a.json"]) != "A" || string(idx.Overrides["options.txt"]) != "O" {
		t.Errorf("overrides = %v", idx.Overrides)
	}
}

func TestParseClientOverridesWinServerIgnored(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name, body string) {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	write("modrinth.index.json", `{"formatVersion":1,"game":"minecraft","versionId":"1","name":"P",
		"files":[{"path":"mods/srv.jar","hashes":{"sha1":"a","sha512":"b"},"env":{"client":"unsupported","server":"required"},"downloads":["https://cdn.modrinth.com/x"],"fileSize":1}],
		"dependencies":{"minecraft":"26.3","fabric-loader":"0.20.1"}}`)
	write("overrides/config/x.json", "base")
	write("client-overrides/config/x.json", "client")
	write("server-overrides/config/y.json", "server")
	write("overrides/../evil.txt", "evil")
	zw.Close()

	idx, err := Parse(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if string(idx.Overrides["config/x.json"]) != "client" {
		t.Errorf("client-overrides müssen gewinnen: %q", idx.Overrides["config/x.json"])
	}
	if _, ok := idx.Overrides["config/y.json"]; ok {
		t.Error("server-overrides dürfen nicht übernommen werden")
	}
	if len(idx.Overrides) != 1 {
		t.Errorf("overrides = %v (ungültige Pfade müssen fehlen)", idx.Overrides)
	}
	if idx.Files[0].ClientEnv != "unsupported" {
		t.Errorf("ClientEnv = %q", idx.Files[0].ClientEnv)
	}
}

func TestParseRejectsMissingIndexAndBadPaths(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("readme.txt")
	w.Write([]byte("x"))
	zw.Close()
	if _, err := Parse(buf.Bytes()); err == nil {
		t.Error("ZIP ohne modrinth.index.json akzeptiert")
	}
	var b2 bytes.Buffer
	zw = zip.NewWriter(&b2)
	w, _ = zw.Create("modrinth.index.json")
	w.Write([]byte(`{"formatVersion":1,"game":"minecraft","versionId":"1","name":"P","files":[{"path":"../x.jar","hashes":{"sha1":"a","sha512":"b"},"downloads":["https://cdn.modrinth.com/x"],"fileSize":1}],"dependencies":{}}`))
	zw.Close()
	if _, err := Parse(b2.Bytes()); err == nil {
		t.Error("Pfad mit .. akzeptiert")
	}
}

const minimalIndex = `{"formatVersion":1,"game":"minecraft","versionId":"1","name":"P","files":[],"dependencies":{"minecraft":"26.3"}}`

func TestParseSkipsOversizedOverride(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("modrinth.index.json")
	w.Write([]byte(minimalIndex))
	w, _ = zw.Create("overrides/config/big.bin")
	w.Write(make([]byte, maxOverride+10))
	w, _ = zw.Create("overrides/config/ok.json")
	w.Write([]byte("{}"))
	zw.Close()

	idx, err := Parse(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := idx.Overrides["config/big.bin"]; ok {
		t.Error("übergroßes Override wurde gespeichert")
	}
	if string(idx.Overrides["config/ok.json"]) != "{}" {
		t.Errorf("ok.json fehlt: %v", idx.Overrides)
	}
}

func TestParseRejectsTooManyEntries(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("modrinth.index.json")
	w.Write([]byte(minimalIndex))
	for i := 0; i < maxEntries+1; i++ {
		w, _ = zw.Create(fmt.Sprintf("overrides/config/f%05d.txt", i))
		w.Write([]byte("x"))
	}
	zw.Close()

	if _, err := Parse(buf.Bytes()); err == nil {
		t.Error("Pack mit zu vielen Einträgen akzeptiert")
	}
}

func TestAllowedURL(t *testing.T) {
	for u, want := range map[string]bool{
		"https://cdn.modrinth.com/x": true, "https://github.com/a/b": true,
		"https://raw.githubusercontent.com/a": true, "https://gitlab.com/a": true,
		"http://cdn.modrinth.com/x": false, "https://evil.com/x": false, "https://github.com.evil.com/x": false,
		"http://169.254.169.254/latest": false, "": false,
	} {
		if AllowedURL(u) != want {
			t.Errorf("AllowedURL(%q) != %v", u, want)
		}
	}
}
