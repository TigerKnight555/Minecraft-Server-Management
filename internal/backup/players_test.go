package backup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListPlayersJoinsUsercache(t *testing.T) {
	dir := t.TempDir()
	pd := filepath.Join(dir, "world", "playerdata")
	if err := os.MkdirAll(pd, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{
		"069a79f4-44e9-4726-a5be-fca90e38aaf5.dat",                        // Steve
		"853c80ef-3c37-49fd-aa49-938b674adae6.dat",                        // nicht im usercache
		"069a79f4-44e9-4726-a5be-fca90e38aaf5.dat.pre-restore-2026-07-18", // Sicherung: ignorieren
		"069a79f4-44e9-4726-a5be-fca90e38aaf5.dat_old",                    // Vanilla-Altdatei: ignorieren
		"kein-uuid.dat", // ignorieren
	} {
		if err := os.WriteFile(filepath.Join(pd, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cache := `[{"name":"Steve","uuid":"069a79f4-44e9-4726-a5be-fca90e38aaf5","expiresOn":"2026-08-01"}]`
	if err := os.WriteFile(filepath.Join(dir, "usercache.json"), []byte(cache), 0o644); err != nil {
		t.Fatal(err)
	}

	players, err := ListPlayers(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(players) != 2 {
		t.Fatalf("players = %+v, want 2 (nur echte <uuid>.dat)", players)
	}
	// benannte zuerst
	if players[0].Name != "Steve" || players[0].UUID != "069a79f4-44e9-4726-a5be-fca90e38aaf5" {
		t.Errorf("players[0] = %+v, want Steve", players[0])
	}
	if players[1].Name != "" || players[1].UUID != "853c80ef-3c37-49fd-aa49-938b674adae6" {
		t.Errorf("players[1] = %+v, want unbenannte UUID", players[1])
	}
	if players[0].LastSaved.IsZero() {
		t.Error("LastSaved fehlt")
	}
}

func TestListPlayersWithoutUsercache(t *testing.T) {
	dir := t.TempDir()
	pd := filepath.Join(dir, "world", "playerdata")
	os.MkdirAll(pd, 0o755)
	os.WriteFile(filepath.Join(pd, "069a79f4-44e9-4726-a5be-fca90e38aaf5.dat"), []byte("x"), 0o644)

	players, err := ListPlayers(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(players) != 1 || players[0].Name != "" {
		t.Errorf("players = %+v, want 1 unbenannter Eintrag", players)
	}
}

func TestListPlayersMissingDirFails(t *testing.T) {
	if _, err := ListPlayers(t.TempDir()); err == nil {
		t.Error("fehlendes playerdata-Verzeichnis muss Fehler geben")
	}
}
