package backup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newRestoreRunner(t *testing.T, d *fakeDocker) (*Restore, string) {
	t.Helper()
	dir := t.TempDir()
	r := NewRestore(d, nil, "mc-restore", dir, testLogger())
	r.Timeout = 2 * time.Second
	r.PollStep = 5 * time.Millisecond
	return r, dir
}

func TestRestoreWritesScriptAndSupervises(t *testing.T) {
	d := &fakeDocker{polls: 1, logs: "wiederhergestellt: /data/world/playerdata/069a79f4-44e9-4726-a5be-fca90e38aaf5.dat\n"}
	r, dir := newRestoreRunner(t, d)

	msg, err := r.RestorePlayer(context.Background(), "069a79f4-44e9-4726-a5be-fca90e38aaf5")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "069a79f4-44e9-4726-a5be-fca90e38aaf5") || !strings.Contains(msg, "wiederhergestellt") {
		t.Errorf("msg = %q", msg)
	}
	script, err := os.ReadFile(filepath.Join(dir, "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`U="069a79f4-44e9-4726-a5be-fca90e38aaf5"`,
		"restic restore latest",
		// beide Layouts im Snapshot suchen, neues zuerst
		`--include "/data/world/players/data/$U.dat" --include "/data/world/playerdata/$U.dat"`,
		`if [ -d /data/world/players/data ]; then D=/data/world/players/data; else D=/data/world/playerdata; fi`,
		".pre-restore-",
		"set -eu",
		`cp -p "$S" "$F"`, // Ownership erhalten (MC-Server muss lesen können)
		`chown "$(stat -c '%u:%g' "$D")" "$F"`, // Gürtel + Hosenträger
	} {
		if !strings.Contains(string(script), want) {
			t.Errorf("script fehlt %q:\n%s", want, script)
		}
	}
	if !d.started {
		t.Error("restore-Container nicht gestartet")
	}
}

func TestRestoreRejectsInvalidUUID(t *testing.T) {
	d := &fakeDocker{}
	r, dir := newRestoreRunner(t, d)
	for _, bad := range []string{
		"", "kein-uuid", "069a79f444e94726a5befca90e38aaf5", // ohne Bindestriche
		`x"; rm -rf / #`, "069a79f4-44e9-4726-a5be-fca90e38aaf5.dat",
	} {
		if _, err := r.RestorePlayer(context.Background(), bad); err == nil {
			t.Errorf("UUID %q akzeptiert, want Fehler", bad)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "run.sh")); !os.IsNotExist(err) {
		t.Error("Skript geschrieben trotz ungültiger UUID")
	}
	if d.started {
		t.Error("Container gestartet trotz ungültiger UUID")
	}
}

func TestRestoreFileNotInSnapshotFails(t *testing.T) {
	d := &fakeDocker{exitCode: 3, logs: "FEHLER: /data/world/playerdata/069a79f4-44e9-4726-a5be-fca90e38aaf5.dat ist im letzten Snapshot nicht enthalten\n"}
	r, _ := newRestoreRunner(t, d)
	_, err := r.RestorePlayer(context.Background(), "069a79f4-44e9-4726-a5be-fca90e38aaf5")
	if err == nil || !strings.Contains(err.Error(), "nicht enthalten") {
		t.Fatalf("err = %v, want 'nicht enthalten'", err)
	}
}

func TestRestoreRefusesDoubleRun(t *testing.T) {
	d := &fakeDocker{running: true}
	r, _ := newRestoreRunner(t, d)
	_, err := r.RestorePlayer(context.Background(), "069a79f4-44e9-4726-a5be-fca90e38aaf5")
	if err == nil || !strings.Contains(err.Error(), "läuft bereits") {
		t.Fatalf("err = %v, want Doppelstart-Schutz", err)
	}
}

func TestRestoreErrorShowsOnlyCurrentRun(t *testing.T) {
	d := &fakeDocker{exitCode: 3, logs: jobMarker + "\nFEHLER: alt.dat fehlt\n" + jobMarker + "\nFEHLER: Spielerdatei neu.dat fehlt\n"}
	r, _ := newRestoreRunner(t, d)
	_, err := r.RestorePlayer(context.Background(), "069a79f4-44e9-4726-a5be-fca90e38aaf5")
	if err == nil || strings.Contains(err.Error(), "alt.dat") || !strings.Contains(err.Error(), "neu.dat") {
		t.Fatalf("err = %v, want nur Ausgabe des aktuellen Laufs", err)
	}
}
