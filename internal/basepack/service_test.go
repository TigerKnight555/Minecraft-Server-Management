package basepack

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/modrinth"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mods"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mrpack"
)

func sum(b []byte) string { s := sha512.Sum512(b); return hex.EncodeToString(s[:]) }

// fakeAPI liefert Pack-Versionen als echte .mrpack-Bytes.
type fakeAPI struct {
	versions map[string][]modrinth.Version // mc → neueste zuerst
	packs    map[string][]byte             // version id → .mrpack
	blobs    map[string][]byte             // url → Datei
	byHash   map[string]modrinth.Version
	versionsErr error // lässt ProjectVersions scheitern
}

func (f *fakeAPI) SearchModpacks(context.Context, string, string, string, int) ([]modrinth.SearchHit, error) {
	return []modrinth.SearchHit{{ProjectID: "FO", Title: "Fabulously Optimized"}}, nil
}
func (f *fakeAPI) Project(_ context.Context, id string) (modrinth.Project, error) {
	if id == "FO" {
		return modrinth.Project{ID: "FO", Slug: "fabulously-optimized", Title: "Fabulously Optimized", ProjectType: "modpack"}, nil
	}
	return modrinth.Project{ID: id, Slug: strings.ToLower(id), Title: "Pack " + id, ProjectType: "modpack"}, nil
}
func (f *fakeAPI) ProjectVersions(_ context.Context, _, _, mc string) ([]modrinth.Version, error) {
	if f.versionsErr != nil {
		return nil, f.versionsErr
	}
	return f.versions[mc], nil
}
func (f *fakeAPI) VersionsByHash(_ context.Context, hs []string) (map[string]modrinth.Version, error) {
	out := map[string]modrinth.Version{}
	for _, h := range hs {
		if v, ok := f.byHash[h]; ok {
			out[h] = v
		}
	}
	return out, nil
}
func (f *fakeAPI) Projects(_ context.Context, ids []string) ([]modrinth.Project, error) {
	var out []modrinth.Project
	for _, id := range ids {
		out = append(out, modrinth.Project{ID: id, Title: "T-" + id})
	}
	return out, nil
}
func (f *fakeAPI) Download(_ context.Context, url string) ([]byte, error) {
	if b, ok := f.packs[url]; ok {
		return b, nil
	}
	return f.blobs[url], nil
}

// addPackVersion baut eine .mrpack mit mods (name → inhalt) und configs.
func (f *fakeAPI) addPackVersion(t *testing.T, mc, id string, mods map[string]string, configs map[string]string) {
	t.Helper()
	var files []mrpack.File
	for name, body := range mods {
		url := "https://cdn.modrinth.com/" + name
		f.blobs[url] = []byte(body)
		files = append(files, mrpack.File{Path: "mods/" + name, SHA1: "x", SHA512: sum([]byte(body)), URL: url, Size: int64(len(body))})
		f.byHash[sum([]byte(body))] = modrinth.Version{ProjectID: "P-" + strings.Split(name, "-")[0], VersionNumber: body}
	}
	ov := map[string][]byte{}
	for p, b := range configs {
		ov[p] = []byte(b)
	}
	data, err := mrpack.Build(files, mrpack.Meta{VersionID: id, Name: "FO", Minecraft: mc, FabricLoader: "0.20.1", Overrides: ov})
	if err != nil {
		t.Fatal(err)
	}
	packURL := "https://cdn.modrinth.com/pack-" + id + ".mrpack"
	f.packs[packURL] = data
	v := modrinth.Version{ID: id, ProjectID: "FO", VersionNumber: id}
	file := modrinth.File{URL: packURL, Filename: "fo-" + id + ".mrpack", Primary: true}
	v.Files = []modrinth.File{file}
	f.versions[mc] = append([]modrinth.Version{v}, f.versions[mc]...)
}

type fakeFiles struct {
	root    string
	calls   int
	markers []string
	own     []mods.Entry
	fail    error
	failLbl string
	last    struct {
		adds    []mods.FileAdd
		removes []mods.FileRemove
	}
}

func (f *fakeFiles) ApplyFiles(_ string, adds []mods.FileAdd, removes []mods.FileRemove, marker string) (string, error) {
	f.calls++
	f.markers = append(f.markers, marker)
	f.last.adds, f.last.removes = adds, removes
	if f.fail != nil {
		return f.failLbl, f.fail
	}
	if len(adds) == 0 && len(removes) == 0 {
		return "", nil
	}
	for _, r := range removes {
		os.Remove(filepath.Join(f.root, r.Category, r.Filename))
	}
	for _, a := range adds {
		if a.Replaces != "" {
			os.Remove(filepath.Join(f.root, a.Category, a.Replaces))
		}
		os.WriteFile(filepath.Join(f.root, a.Category, a.Filename), a.Data, 0o644)
	}
	return "L", nil
}
func (f *fakeFiles) CheckUpdates(context.Context, string, string) ([]mods.Entry, error) {
	return f.own, nil
}

func setup(t *testing.T) (*Service, *fakeAPI, *fakeFiles, map[string]string, *[][]mods.Change) {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"mods", "resourcepacks", "shaderpacks"} {
		os.MkdirAll(filepath.Join(root, d), 0o755)
	}
	api := &fakeAPI{versions: map[string][]modrinth.Version{}, packs: map[string][]byte{}, blobs: map[string][]byte{}, byHash: map[string]modrinth.Version{}}
	files := &fakeFiles{root: root}
	kv := map[string]string{}
	var notes [][]mods.Change
	s := New(api, files, root, func(k string) string { return kv[k] }, func(k, v string) { kv[k] = v },
		func(c []mods.Change) { notes = append(notes, c) }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return s, api, files, kv, &notes
}

func TestApplyFirstVersionWithConfigsAndAbsorb(t *testing.T) {
	s, api, files, _, notes := setup(t)
	api.addPackVersion(t, "26.3", "6.4.0", map[string]string{"sodium-1.jar": "s1", "lithium-1.jar": "l1"},
		map[string]string{"config/a.json": "A", "config/sodium-options.json": "S", "options.txt": "O"})
	files.own = []mods.Entry{{Category: "mods", Filename: "sodium-own.jar", Managed: true, ProjectID: "P-sodium", Name: "Sodium"}}
	os.WriteFile(filepath.Join(files.root, "mods", "sodium-own.jar"), []byte("own"), 0o644)

	pv, err := s.Preview(context.Background(), "FO", "26.3")
	if err != nil {
		t.Fatal(err)
	}
	if pv.VersionID != "6.4.0" || len(pv.Plan.Add) != 2 || len(pv.Plan.AbsorbOwn) != 1 {
		t.Fatalf("preview = %+v", pv)
	}
	on := map[string]bool{}
	for _, c := range pv.Configs {
		on[c.Path] = c.On
	}
	if !on["config/a.json"] || on["config/sodium-options.json"] || on["options.txt"] {
		t.Errorf("vorauswahl = %v", on)
	}
	summary, err := s.Apply(context.Background(), "FO", "6.4.0", "26.3", on)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "6.4.0") || files.markers[0] == "" {
		t.Errorf("summary = %q markers = %v", summary, files.markers)
	}
	if b, _ := os.ReadFile(filepath.Join(files.root, "config", "a.json")); string(b) != "A" {
		t.Error("freigegebene Config nicht geschrieben")
	}
	if _, err := os.Stat(filepath.Join(files.root, "options.txt")); err == nil {
		t.Error("nicht freigegebene Config geschrieben")
	}
	if _, err := os.Stat(filepath.Join(files.root, "mods", "sodium-own.jar")); err == nil {
		t.Error("eigene Sodium-Kopie hätte wegfallen müssen")
	}
	st, ok := s.State()
	if !ok || st.VersionNumber != "6.4.0" || len(st.Absorbed) != 1 || !s.Pinned("sodium-1.jar") || s.Pinned("fremd.jar") {
		t.Errorf("state = %+v", st)
	}
	if len(*notes) != 1 || (*notes)[0][0].Action != "pack" {
		t.Errorf("notes = %+v", *notes)
	}
}

func TestUpdateKeepsDisabledAndFollowsPack(t *testing.T) {
	s, api, files, _, _ := setup(t)
	api.addPackVersion(t, "26.3", "6.4.0", map[string]string{"sodium-1.jar": "s1", "lithium-1.jar": "l1", "old-1.jar": "o1"}, nil)
	if _, err := s.Apply(context.Background(), "FO", "6.4.0", "26.3", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Toggle(context.Background(), "P-lithium", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(files.root, "mods", "lithium-1.jar")); err == nil {
		t.Fatal("abgeschaltete Mod liegt noch im Paket")
	}
	api.addPackVersion(t, "26.3", "6.5.0", map[string]string{"sodium-2.jar": "s2", "lithium-2.jar": "l2", "new-1.jar": "n1"}, nil)
	av, err := s.Available(context.Background(), "26.3")
	if err != nil || av == nil || av.ID != "6.5.0" {
		t.Fatalf("available = %+v, %v", av, err)
	}
	if _, err := s.Apply(context.Background(), "FO", "6.5.0", "26.3", nil); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{"sodium-2.jar": true, "new-1.jar": true, "lithium-2.jar": false, "old-1.jar": false, "sodium-1.jar": false} {
		_, err := os.Stat(filepath.Join(files.root, "mods", name))
		if (err == nil) != want {
			t.Errorf("%s vorhanden = %v, want %v", name, err == nil, want)
		}
	}
	st, _ := s.State()
	if !st.IsDisabled("P-lithium") {
		t.Error("Abschaltung nach Update verloren")
	}
	if err := s.Toggle(context.Background(), "P-lithium", true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(files.root, "mods", "lithium-2.jar")); string(b) != "l2" {
		t.Error("Anschalten hat die aktuelle Pack-Datei nicht geladen")
	}
}

func TestReadySwitchAndRemove(t *testing.T) {
	s, api, files, _, _ := setup(t)
	if _, _, set, _ := s.Ready(context.Background(), "26.4"); set {
		t.Error("ohne Basis-Pack darf Ready nicht gesetzt melden")
	}
	api.addPackVersion(t, "26.3", "6.4.0", map[string]string{"sodium-1.jar": "s1"}, map[string]string{"config/a.json": "A"})
	if _, err := s.Apply(context.Background(), "FO", "6.4.0", "26.3", map[string]bool{"config/a.json": true}); err != nil {
		t.Fatal(err)
	}
	if _, ready, set, _ := s.Ready(context.Background(), "26.4"); !set || ready {
		t.Error("26.4 gibt es noch nicht — ready muss false sein")
	}
	api.addPackVersion(t, "26.4", "7.0.0", map[string]string{"sodium-3.jar": "s3"}, map[string]string{"config/a.json": "A2"})
	if err := s.SwitchMC(context.Background(), "26.4"); err != nil {
		t.Fatal(err)
	}
	st, _ := s.State()
	if st.MC != "26.4" || st.VersionNumber != "7.0.0" {
		t.Errorf("state nach Umstellung = %+v", st)
	}
	if b, _ := os.ReadFile(filepath.Join(files.root, "config", "a.json")); string(b) != "A2" {
		t.Error("Config-Auswahl nicht übernommen")
	}
	if err := s.Remove(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.State(); ok {
		t.Error("Stand nach Entfernen noch da")
	}
	if _, err := os.Stat(filepath.Join(files.root, "config", "a.json")); err == nil {
		t.Error("Config nach Entfernen noch da")
	}
	if _, err := os.Stat(filepath.Join(files.root, "mods", "sodium-3.jar")); err == nil {
		t.Error("Pack-Mod nach Entfernen noch da")
	}
}

func TestReapplyAbsorbsNewOwnCopy(t *testing.T) {
	s, api, files, _, _ := setup(t)
	api.addPackVersion(t, "26.3", "6.4.0", map[string]string{"sodium-1.jar": "s1"}, nil)
	if _, err := s.Apply(context.Background(), "FO", "6.4.0", "26.3", nil); err != nil {
		t.Fatal(err)
	}
	files.own = []mods.Entry{{Category: "mods", Filename: "sodium-own.jar", Managed: true, ProjectID: "P-sodium", Name: "Sodium"}}
	os.WriteFile(filepath.Join(files.root, "mods", "sodium-own.jar"), []byte("own"), 0o644)
	if _, err := s.Apply(context.Background(), "FO", "6.4.0", "26.3", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(files.root, "mods", "sodium-own.jar")); err == nil {
		t.Error("eigene Kopie hätte wegfallen müssen")
	}
	if _, err := os.Stat(filepath.Join(files.root, "mods", "sodium-1.jar")); err != nil {
		t.Error("Pack-Datei muss bleiben")
	}
	st, _ := s.State()
	if len(st.Absorbed) != 1 || st.Absorbed[0] != "P-sodium" {
		t.Errorf("absorbed = %v", st.Absorbed)
	}
}

func TestAbsorbOwnOnUpdateRemovesOnce(t *testing.T) {
	s, api, files, _, _ := setup(t)
	api.addPackVersion(t, "26.3", "6.4.0", map[string]string{"sodium-1.jar": "s1"}, nil)
	if _, err := s.Apply(context.Background(), "FO", "6.4.0", "26.3", nil); err != nil {
		t.Fatal(err)
	}
	files.own = []mods.Entry{{Category: "mods", Filename: "sodium-own.jar", ProjectID: "P-sodium", Name: "Sodium"}}
	os.WriteFile(filepath.Join(files.root, "mods", "sodium-own.jar"), []byte("own"), 0o644)
	api.addPackVersion(t, "26.3", "6.5.0", map[string]string{"sodium-2.jar": "s2"}, nil)
	if _, err := s.Apply(context.Background(), "FO", "6.5.0", "26.3", nil); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range files.last.removes {
		if r.Filename == "sodium-own.jar" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("eigene Kopie %d-mal entfernt: %+v", n, files.last.removes)
	}
	for name, want := range map[string]bool{"sodium-own.jar": false, "sodium-1.jar": false, "sodium-2.jar": true} {
		if _, err := os.Stat(filepath.Join(files.root, "mods", name)); (err == nil) != want {
			t.Errorf("%s vorhanden = %v, want %v", name, err == nil, want)
		}
	}
}

func TestAbsorbOwnWithSameFilenameUsesReplaces(t *testing.T) {
	s, api, files, _, _ := setup(t)
	api.addPackVersion(t, "26.3", "6.4.0", map[string]string{"sodium-1.jar": "s1"}, nil)
	files.own = []mods.Entry{{Category: "mods", Filename: "sodium-1.jar", ProjectID: "P-sodium", Name: "Sodium"}}
	os.WriteFile(filepath.Join(files.root, "mods", "sodium-1.jar"), []byte("own"), 0o644)
	if _, err := s.Apply(context.Background(), "FO", "6.4.0", "26.3", nil); err != nil {
		t.Fatal(err)
	}
	if len(files.last.adds) != 1 || files.last.adds[0].Replaces != "sodium-1.jar" || len(files.last.removes) != 0 {
		t.Errorf("adds=%+v removes=%+v", files.last.adds, files.last.removes)
	}
}

func TestApplyErrorKeepsState(t *testing.T) {
	s, api, files, _, _ := setup(t)
	api.addPackVersion(t, "26.3", "6.4.0", map[string]string{"sodium-1.jar": "s1"}, map[string]string{"config/a.json": "A"})
	if _, err := s.Apply(context.Background(), "FO", "6.4.0", "26.3", map[string]bool{"config/a.json": true}); err != nil {
		t.Fatal(err)
	}
	api.addPackVersion(t, "26.3", "6.5.0", map[string]string{"sodium-2.jar": "s2"}, map[string]string{"config/a.json": "A2"})
	files.fail = errors.New("kaputt")
	if _, err := s.Apply(context.Background(), "FO", "6.5.0", "26.3", nil); err == nil {
		t.Fatal("Fehler erwartet")
	}
	st, _ := s.State()
	if st.VersionNumber != "6.4.0" {
		t.Errorf("state = %+v", st)
	}
	if b, _ := os.ReadFile(filepath.Join(files.root, "config", "a.json")); string(b) != "A" {
		t.Error("Config bei Fehler verändert")
	}
}

func TestEmptyLabelIsNotAnError(t *testing.T) {
	s, api, _, _, _ := setup(t)
	api.addPackVersion(t, "26.3", "6.4.0", map[string]string{"sodium-1.jar": "s1"}, nil)
	if _, err := s.Apply(context.Background(), "FO", "6.4.0", "26.3", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(context.Background(), "FO", "6.4.0", "26.3", nil); err != nil {
		t.Fatalf("zweites Übernehmen ohne Änderung: %v", err)
	}
}

func TestDropAbsorbedHint(t *testing.T) {
	s, api, files, _, notes := setup(t)
	api.addPackVersion(t, "26.3", "6.4.0", map[string]string{"sodium-1.jar": "s1", "lithium-1.jar": "l1"}, nil)
	files.own = []mods.Entry{{Category: "mods", Filename: "sodium-own.jar", ProjectID: "P-sodium", Name: "Sodium"}}
	os.WriteFile(filepath.Join(files.root, "mods", "sodium-own.jar"), []byte("own"), 0o644)
	if _, err := s.Apply(context.Background(), "FO", "6.4.0", "26.3", nil); err != nil {
		t.Fatal(err)
	}
	files.own = nil
	api.addPackVersion(t, "26.3", "6.5.0", map[string]string{"lithium-2.jar": "l2"}, nil)
	if _, err := s.Apply(context.Background(), "FO", "6.5.0", "26.3", nil); err != nil {
		t.Fatal(err)
	}
	last := (*notes)[len(*notes)-1]
	if len(last) != 2 || !strings.Contains(last[1].Name, "entfällt — war vorher auch deine eigene Mod") {
		t.Errorf("notes = %+v", last)
	}
}

func TestReadyReturnsModrinthError(t *testing.T) {
	s, api, _, _, _ := setup(t)
	api.addPackVersion(t, "26.3", "6.4.0", map[string]string{"sodium-1.jar": "s1"}, map[string]string{"config/a.json": "A"})
	if _, err := s.Apply(context.Background(), "FO", "6.4.0", "26.3", map[string]bool{"config/a.json": true}); err != nil {
		t.Fatal(err)
	}
	api.versionsErr = errors.New("modrinth 503")
	name, ready, set, err := s.Ready(context.Background(), "26.4")
	if err == nil || ready || !set || name == "" {
		t.Errorf("Ready = %q %v %v %v, want Fehler mit set=true, ready=false", name, ready, set, err)
	}
}
