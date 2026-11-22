package basepack

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mods"
)

func exists(root, cat, name string) bool {
	_, err := os.Stat(filepath.Join(root, cat, name))
	return err == nil
}

func TestToggleOnAbsorbsOwnCopy(t *testing.T) {
	s, api, files, _, _ := setup(t)
	ctx := context.Background()
	api.addPackVersion(t, "26.3", "6.4.0", map[string]string{"sodium-1.jar": "s1"}, nil)
	if _, err := s.Apply(ctx, "FO", "6.4.0", "26.3", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Toggle(ctx, "P-sodium", false); err != nil {
		t.Fatal(err)
	}
	files.own = []mods.Entry{{Category: "mods", Filename: "sodium-own.jar", ProjectID: "P-sodium", Name: "Sodium"}}
	os.WriteFile(filepath.Join(files.root, "mods", "sodium-own.jar"), []byte("own"), 0o644)
	if err := s.Toggle(ctx, "P-sodium", true); err != nil {
		t.Fatal(err)
	}
	if !exists(files.root, "mods", "sodium-1.jar") || exists(files.root, "mods", "sodium-own.jar") {
		t.Error("nur die Pack-Datei darf liegen")
	}
	st, _ := s.State()
	if !contains(st.Absorbed, "P-sodium") || st.IsDisabled("P-sodium") {
		t.Errorf("state = %+v", st)
	}
}

func TestDisabledPackModKeepsOwnCopy(t *testing.T) {
	s, api, files, _, _ := setup(t)
	ctx := context.Background()
	api.addPackVersion(t, "26.3", "6.4.0", map[string]string{"sodium-1.jar": "s1"}, nil)
	if _, err := s.Apply(ctx, "FO", "6.4.0", "26.3", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Toggle(ctx, "P-sodium", false); err != nil {
		t.Fatal(err)
	}
	files.own = []mods.Entry{{Category: "mods", Filename: "sodium-own.jar", ProjectID: "P-sodium", Name: "Sodium"}}
	os.WriteFile(filepath.Join(files.root, "mods", "sodium-own.jar"), []byte("own"), 0o644)
	pv, err := s.Preview(ctx, "FO", "26.3")
	if err != nil || len(pv.Plan.AbsorbOwn) != 0 {
		t.Errorf("preview absorb = %+v, %v", pv, err)
	}
	if _, err := s.Apply(ctx, "FO", "6.4.0", "26.3", nil); err != nil {
		t.Fatal(err)
	}
	if !exists(files.root, "mods", "sodium-own.jar") {
		t.Error("eigene Kopie muss bleiben")
	}
	st, _ := s.State()
	if contains(st.Absorbed, "P-sodium") {
		t.Errorf("absorbed = %v", st.Absorbed)
	}
	if s.Pinned("sodium-1.jar") {
		t.Error("abgeschaltete Pack-Datei darf nicht gepinnt sein")
	}
}

func TestPinnedIgnoresDisabledSameName(t *testing.T) {
	s, api, files, _, _ := setup(t)
	ctx := context.Background()
	api.addPackVersion(t, "26.3", "6.4.0", map[string]string{"sodium-1.jar": "s1"}, nil)
	if _, err := s.Apply(ctx, "FO", "6.4.0", "26.3", nil); err != nil {
		t.Fatal(err)
	}
	if !s.Pinned("sodium-1.jar") {
		t.Fatal("aktive Pack-Datei muss gepinnt sein")
	}
	if err := s.Toggle(ctx, "P-sodium", false); err != nil {
		t.Fatal(err)
	}
	_ = files
	if s.Pinned("sodium-1.jar") {
		t.Error("abgeschaltete Pack-Datei darf nicht gepinnt sein")
	}
}

func TestDropHintOnSwitchAndRemove(t *testing.T) {
	for _, remove := range []bool{false, true} {
		s, api, files, _, notes := setup(t)
		ctx := context.Background()
		api.addPackVersion(t, "26.3", "6.4.0", map[string]string{"sodium-1.jar": "s1", "lithium-1.jar": "l1"}, nil)
		files.own = []mods.Entry{{Category: "mods", Filename: "sodium-own.jar", ProjectID: "P-sodium", Name: "Sodium"}}
		os.WriteFile(filepath.Join(files.root, "mods", "sodium-own.jar"), []byte("own"), 0o644)
		if _, err := s.Apply(ctx, "FO", "6.4.0", "26.3", nil); err != nil {
			t.Fatal(err)
		}
		files.own = nil
		if remove {
			if err := s.Remove(ctx); err != nil {
				t.Fatal(err)
			}
		} else {
			api.addPackVersion(t, "26.3", "1.0", map[string]string{"lithium-9.jar": "l9"}, nil)
			if _, err := s.Apply(ctx, "BP", "1.0", "26.3", nil); err != nil {
				t.Fatal(err)
			}
		}
		last := (*notes)[len(*notes)-1]
		hints := 0
		for _, c := range last {
			if c.Action == "pack" && strings.Contains(c.Name, "entfällt — war vorher auch deine eigene Mod") {
				hints++
				if !strings.Contains(c.Name, "T-P-sodium") {
					t.Errorf("name = %q", c.Name)
				}
			}
		}
		if hints != 1 {
			t.Errorf("remove=%v: %d Hinweise: %+v", remove, hints, last)
		}
	}
}

func TestSwitchPack(t *testing.T) {
	s, api, files, _, _ := setup(t)
	ctx := context.Background()
	api.addPackVersion(t, "26.3", "A1", map[string]string{"sodium-1.jar": "s1", "lithium-1.jar": "l1", "shared-1.jar": "sa"}, nil)
	if _, err := s.Apply(ctx, "FO", "A1", "26.3", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Toggle(ctx, "P-lithium", false); err != nil {
		t.Fatal(err)
	}
	api.addPackVersion(t, "26.3", "B1", map[string]string{"shared-1.jar": "sb", "extra-1.jar": "e1"}, nil)
	pv, err := s.Preview(ctx, "BP", "26.3")
	if err != nil || len(pv.Plan.Add) != 2 {
		t.Fatalf("preview = %+v, %v", pv, err)
	}
	if _, err := s.Apply(ctx, "BP", "B1", "26.3", nil); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{"sodium-1.jar": false, "lithium-1.jar": false, "shared-1.jar": true, "extra-1.jar": true} {
		if exists(files.root, "mods", name) != want {
			t.Errorf("%s vorhanden = %v, want %v", name, !want, want)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(files.root, "mods", "shared-1.jar")); string(b) != "sb" {
		t.Errorf("shared = %q", b)
	}
	for _, a := range files.last.adds {
		if a.Filename == "shared-1.jar" && a.Replaces != "shared-1.jar" {
			t.Errorf("shared add = %+v", a)
		}
	}
	if len(files.last.removes) != 1 || files.last.removes[0].Filename != "sodium-1.jar" {
		t.Errorf("removes = %+v", files.last.removes)
	}
	st, _ := s.State()
	if st.ProjectID != "BP" || len(st.Disabled) != 0 || len(st.Files) != 2 {
		t.Errorf("state = %+v", st)
	}
}

func TestPreviewOnSwitchFiltersOldPackFiles(t *testing.T) {
	s, api, files, _, _ := setup(t)
	ctx := context.Background()
	api.addPackVersion(t, "26.3", "A1", map[string]string{"sodium-1.jar": "s1"}, nil)
	if _, err := s.Apply(ctx, "FO", "A1", "26.3", nil); err != nil {
		t.Fatal(err)
	}
	// CheckUpdates listet auch die alte Pack-Datei; sie darf nicht als eigene gelten.
	files.own = []mods.Entry{{Category: "mods", Filename: "sodium-1.jar", ProjectID: "P-sodium", Name: "Sodium"}}
	api.addPackVersion(t, "26.3", "B1", map[string]string{"sodium-2.jar": "s2"}, nil)
	pv, err := s.Preview(ctx, "BP", "26.3")
	if err != nil || len(pv.Plan.AbsorbOwn) != 0 {
		t.Errorf("absorbOwn = %+v, %v", pv, err)
	}
}

func TestNoNotifyOnNoopReapply(t *testing.T) {
	s, api, _, _, notes := setup(t)
	ctx := context.Background()
	api.addPackVersion(t, "26.3", "6.4.0", map[string]string{"sodium-1.jar": "s1"}, map[string]string{"config/a.json": "A"})
	if _, err := s.Apply(ctx, "FO", "6.4.0", "26.3", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(ctx, "FO", "6.4.0", "26.3", nil); err != nil {
		t.Fatal(err)
	}
	if len(*notes) != 1 {
		t.Errorf("notes = %+v", *notes)
	}
}

func TestApplyErrorLogsLabel(t *testing.T) {
	s, api, files, _, _ := setup(t)
	var buf strings.Builder
	s.log = slog.New(slog.NewTextHandler(&buf, nil))
	api.addPackVersion(t, "26.3", "6.4.0", map[string]string{"sodium-1.jar": "s1"}, nil)
	files.fail, files.failLbl = errors.New("kaputt"), "LBL-42"
	if _, err := s.Apply(context.Background(), "FO", "6.4.0", "26.3", nil); err == nil {
		t.Fatal("Fehler erwartet")
	}
	if !strings.Contains(buf.String(), "LBL-42") || !strings.Contains(buf.String(), "RollbackLabel") {
		t.Errorf("log = %s", buf.String())
	}
	if _, ok := s.State(); ok {
		t.Error("state darf nicht gespeichert sein")
	}
}
