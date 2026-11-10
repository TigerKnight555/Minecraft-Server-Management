package backup

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/collector"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// fakeDocker simulates a job container: after start, it "finishes" with the
// configured exit code once the poll counter runs out.
type fakeDocker struct {
	mu       sync.Mutex
	started  bool
	exitCode int
	running  bool // pre-existing running container (double-run guard)
	logs     string
	polls    int
}

func (f *fakeDocker) StartContainer(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = true
	return nil
}

func (f *fakeDocker) InspectContainer(_ context.Context, id string) (collector.ContainerDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.running {
		return collector.ContainerDetail{Running: true}, nil
	}
	if !f.started {
		return collector.ContainerDetail{Running: false, ExitCode: 0}, nil
	}
	if f.polls > 0 {
		f.polls--
		return collector.ContainerDetail{Running: true}, nil
	}
	return collector.ContainerDetail{
		Running: false, ExitCode: f.exitCode, FinishedAt: time.Now(),
	}, nil
}

func (f *fakeDocker) TailLogs(context.Context, string, int) (string, error) {
	return f.logs, nil
}

func newRunner(d *fakeDocker) *Runner {
	run := New(d, nil, "mc-backup", testLogger())
	run.Timeout = 2 * time.Second
	run.PollStep = 5 * time.Millisecond
	return run
}

func TestBackupHappyPath(t *testing.T) {
	d := &fakeDocker{polls: 2, logs: "processed 100 files, 2 GiB in 0:30\nsnapshot ab12cd34 saved\n"}
	msg, err := newRunner(d).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "Backup ok") || !strings.Contains(msg, "Stand ab12cd34") {
		t.Errorf("msg = %q", msg)
	}
}

func TestBackupNonZeroExitFailsWithLogs(t *testing.T) {
	d := &fakeDocker{exitCode: 1, logs: "Fatal: unable to open repository\n"}
	_, err := newRunner(d).Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "exit 1") || !strings.Contains(err.Error(), "unable to open repository") {
		t.Fatalf("err = %v, want exit-1 mit Log-Ausschnitt", err)
	}
}

func TestBackupRefusesDoubleRun(t *testing.T) {
	d := &fakeDocker{running: true}
	_, err := newRunner(d).Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "läuft bereits") {
		t.Fatalf("err = %v, want Doppelstart-Schutz", err)
	}
	if d.started {
		t.Error("Container trotz laufendem Backup gestartet")
	}
}

func TestBackupTimeout(t *testing.T) {
	d := &fakeDocker{polls: 100000}
	run := newRunner(d)
	run.Timeout = 30 * time.Millisecond
	_, err := run.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "nicht fertig") {
		t.Fatalf("err = %v, want Timeout", err)
	}
}

func TestResticSummary(t *testing.T) {
	logs := "irrelevant\nprocessed 100 files, 2 GiB in 0:30\nAdded to the repository: 55 MiB\nsnapshot ab12cd34 saved\n"
	got := resticSummary(logs)
	for _, want := range []string{"2 GiB geprüft", "100 Dateien", "55 MiB neu gesichert", "Stand ab12cd34"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary %q fehlt %q", got, want)
		}
	}
	if s := resticSummary("nur rauschen"); !strings.Contains(s, "keine restic-Zusammenfassung") {
		t.Errorf("leere summary = %q", s)
	}
}

// Vorlauf im Log (früherer Lauf) + lange forget/prune-Ausgabe danach: die
// Zahlen des aktuellen Laufs müssen gefunden werden.
func TestResticSummaryUsesLastRunDespitePrune(t *testing.T) {
	logs := "processed 1 files, 1 MiB in 0:01\nAdded to the repository: 1 MiB\nsnapshot 00000000 saved\n" +
		"processed 215 files, 251.773 MiB in 0:01\nAdded to the repository: 9.5 MiB\nsnapshot e4fbc24f saved\n" +
		strings.Repeat("[0:00] 100.00%  1 / 1 packs repacked\n", 60) + "done\n"
	got := resticSummary(logs)
	for _, want := range []string{"251.773 MiB geprüft", "215 Dateien", "9.5 MiB neu gesichert", "Stand e4fbc24f"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary %q fehlt %q", got, want)
		}
	}
}
