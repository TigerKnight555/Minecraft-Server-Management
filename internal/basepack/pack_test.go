package basepack

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mrpack"
)

func TestFromIndex(t *testing.T) {
	idx := &mrpack.Index{
		Files: []mrpack.IndexFile{
			{Path: "mods/sodium.jar", SHA1: "a", SHA512: "A", URLs: []string{"https://cdn.modrinth.com/s"}, Size: 1},
			{Path: "resourcepacks/rp.zip", SHA1: "b", SHA512: "B", URLs: []string{"https://github.com/x/rp.zip"}, Size: 2},
			{Path: "mods/server-only.jar", SHA1: "c", SHA512: "C", URLs: []string{"https://cdn.modrinth.com/c"}, ClientEnv: "unsupported"},
			{Path: "kubejs/x.js", SHA1: "d", SHA512: "D", URLs: []string{"https://cdn.modrinth.com/d"}},
			{Path: "mods/sub/x.jar", SHA1: "e", SHA512: "E", URLs: []string{"https://cdn.modrinth.com/e"}},
		},
		Overrides: map[string][]byte{"config/a.json": nil, "options.txt": nil, "mods/bundled.jar": nil},
	}
	files, configs, ignored := FromIndex(idx)
	// mods/bundled.jar aus overrides/ ist jetzt eine Pack-Datei (Override)
	if len(files) != 3 || !files[0].Override || files[0].Filename != "bundled.jar" ||
		files[1].Category != "mods" || files[1].Filename != "sodium.jar" || files[1].Override ||
		files[2].Category != "resourcepacks" || files[2].URL != "https://github.com/x/rp.zip" {
		t.Errorf("files = %+v", files)
	}
	if strings.Join(configs, ",") != "config/a.json,options.txt" {
		t.Errorf("configs = %v", configs)
	}
	if strings.Join(ignored, ",") != "kubejs/x.js,mods/sub/x.jar" {
		t.Errorf("ignored = %v", ignored)
	}
}

func TestDefaultOn(t *testing.T) {
	for p, want := range map[string]bool{
		"config/fabric/indigo-renderer.properties": true,
		"config/sodium-extra.json":                 true,
		"config/sodium-options.json":               false,
		"config/iris.properties":                   false,
		"config/xaerominimap.txt":                  false,
		"config/controlling.json":                  false,
		"config/amecs-keybinds.json":               false,
		"options.txt":                              false,
		"servers.dat":                              false,
		"resourcepacks.txt":                        false,
	} {
		if DefaultOn(p) != want {
			t.Errorf("DefaultOn(%q) != %v", p, want)
		}
	}
}

func pf(key, name, sha string) PackFile {
	return PackFile{Path: "mods/" + name + ".jar", Category: "mods", Filename: name + ".jar", ProjectID: key, Name: name, SHA512: sha}
}

func TestDiff(t *testing.T) {
	prev := State{
		ProjectID: "P-base",
		Files:     []PackFile{pf("P-sod", "sodium", "s1"), pf("P-lith", "lithium", "l1"), pf("P-old", "oldmod", "o1"), pf("P-was-own", "wasown", "w1")},
		Disabled:  []string{"P-lith"},
		Configs:   map[string]bool{"config/a.json": true},
		Absorbed:  []string{"P-was-own"},
	}
	next := []PackFile{pf("P-sod", "sodium", "s2"), pf("P-lith", "lithium", "l2"), pf("P-new", "newmod", "n1"), pf("P-jade", "jade", "j1")}
	own := []OwnFile{{Category: "mods", Filename: "jade-own.jar", Key: "P-jade", Name: "Jade"}, {Category: "mods", Filename: "mine.jar", Key: "P-mine", Name: "Mine"}}
	p := Diff(prev, next, []string{"config/a.json", "config/b.json"}, own)

	if len(p.Add) != 2 || len(p.Update) != 2 || len(p.Remove) != 2 {
		t.Fatalf("plan = %+v", p)
	}
	if len(p.AbsorbOwn) != 1 || p.AbsorbOwn[0].Filename != "jade-own.jar" {
		t.Errorf("absorb = %+v", p.AbsorbOwn)
	}
	if len(p.DropAbsorbed) != 1 || p.DropAbsorbed[0] != "wasown" {
		t.Errorf("dropAbsorbed = %v", p.DropAbsorbed)
	}
	if len(p.NewConfigs) != 1 || p.NewConfigs[0] != "config/b.json" {
		t.Errorf("newConfigs = %v", p.NewConfigs)
	}
	if !prev.IsDisabled("P-lith") || prev.IsDisabled("P-sod") {
		t.Error("IsDisabled falsch")
	}
}

func TestSummary(t *testing.T) {
	prev := State{VersionNumber: "6.4.0"}
	p := Plan{Add: make([]PackFile, 3), Remove: make([]PackFile, 1), Update: make([]FileUpdate, 12)}
	if s := Summary(prev, "Fabulously Optimized", "6.5.0", p, 40); s != "Fabulously Optimized 6.4.0 → 6.5.0 (3 neu, 1 entfernt, 12 aktualisiert)" {
		t.Errorf("summary = %q", s)
	}
	if s := Summary(State{}, "Fabulously Optimized", "6.4.0", Plan{}, 40); s != "Basis-Modpack Fabulously Optimized 6.4.0 übernommen (40 Mods)" {
		t.Errorf("erstes = %q", s)
	}
}

func TestDiffAbsorbsOwnForExistingPackFile(t *testing.T) {
	prev := State{ProjectID: "FO", Files: []PackFile{pf("P-sod", "sodium", "s1")}}
	next := []PackFile{pf("P-sod", "sodium", "s1")}
	own := []OwnFile{{Category: "mods", Filename: "sodium-own.jar", Key: "P-sod", Name: "Sodium"}}
	p := Diff(prev, next, nil, own)

	if len(p.AbsorbOwn) != 1 {
		t.Fatalf("AbsorbOwn = %+v, want 1 entry", p.AbsorbOwn)
	}
	if len(p.Add) != 0 || len(p.Update) != 0 || len(p.Remove) != 0 {
		t.Errorf("plan = %+v, want only AbsorbOwn", p)
	}
}

func TestPlanJSONShape(t *testing.T) {
	p := Plan{
		Update:    []FileUpdate{{Old: PackFile{Filename: "a.jar"}, New: PackFile{Filename: "b.jar"}}},
		AbsorbOwn: []OwnFile{{Category: "mods", Filename: "x.jar", Key: "P-x", Name: "X"}},
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string][]map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["absorbOwn"][0]["name"] != "X" || m["absorbOwn"][0]["filename"] != "x.jar" || m["absorbOwn"][0]["key"] != "P-x" || m["absorbOwn"][0]["category"] != "mods" {
		t.Errorf("absorbOwn = %v", m["absorbOwn"])
	}
	if m["update"][0]["old"] == nil || m["update"][0]["new"] == nil {
		t.Errorf("update = %v", m["update"])
	}
	// Alte (Go-großgeschriebene) Schlüssel dekodieren weiterhin (case-insensitive).
	var o OwnFile
	if err := json.Unmarshal([]byte(`{"Category":"mods","Filename":"y.jar","Key":"P-y","Name":"Y"}`), &o); err != nil || o.Name != "Y" || o.Key != "P-y" {
		t.Errorf("legacy decode = %+v, %v", o, err)
	}
}

// Minor 5: nur erlaubte Download-Quellen (https, Modrinth-Liste); die erste
// erlaubte gewinnt, ohne erlaubte Quelle landet die Datei in ignored.
func TestFromIndexAllowListsURLs(t *testing.T) {
	idx := &mrpack.Index{Files: []mrpack.IndexFile{
		{Path: "mods/a.jar", SHA512: "A", URLs: []string{"http://169.254.169.254/a", "https://cdn.modrinth.com/a"}},
		{Path: "mods/b.jar", SHA512: "B", URLs: []string{"https://evil.com/b"}},
		{Path: "mods/c.jar", SHA512: "C"},
	}}
	files, _, ignored := FromIndex(idx)
	if len(files) != 1 || files[0].URL != "https://cdn.modrinth.com/a" {
		t.Errorf("files = %+v", files)
	}
	if strings.Join(ignored, ",") != "mods/b.jar,mods/c.jar" {
		t.Errorf("ignored = %v", ignored)
	}
}
