package backup

import (
	"context"
	"errors"
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
// configured exit code on the next inspect.
type fakeDocker struct {
	mu       sync.Mutex
	started  bool
	startErr error
	exitCode int
	running  bool // pre-existing running container (double-run guard)
	logs     string
	polls    int // inspects until the job counts as finished
}

func (f *fakeDocker) StartContainer(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return f.startErr
	}
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

// fakeRCON records commands; individual commands can be made to fail.
type fakeRCON struct {
	mu    sync.Mutex
	cmds  []string
	fails map[string]bool
}

func (f *fakeRCON) Exec(_ context.Context, cmd string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cmds = append(f.cmds, cmd)
	if f.fails[cmd] {
		return "", errors.New("rcon kaputt")
	}
	return "", nil
}

func (f *fakeRCON) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.cmds...)
}

func newRunner(d *fakeDocker, r collector.RCONClient, online bool) *Runner {
	run := New(d, r, func() collector.MCStatus {
		return collector.MCStatus{Online: online}
	}, nil, "mc-backup", testLogger())
	run.Timeout = 2 * time.Second
	run.PollStep = 5 * time.Millisecond
	return run
}

func TestBackupHappyPathOnline(t *testing.T) {
	d := &fakeDocker{polls: 2, logs: "processed 100 files, 2 GiB in 0:30\nsnapshot ab12cd34 saved\n"}
	r := &fakeRCON{}
	msg, err := newRunner(d, r, true).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"save-off", "save-all flush", "save-on"}
	got := r.commands()
	if len(got) != 3 {
		t.Fatalf("rcon = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("rcon[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if !strings.Contains(msg, "Backup ok") || !strings.Contains(msg, "snapshot ab12cd34 saved") {
		t.Errorf("msg = %q", msg)
	}
}

func TestBackupOfflineServerSkipsRCON(t *testing.T) {
	d := &fakeDocker{}
	r := &fakeRCON{}
	_, err := newRunner(d, r, false).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.commands()) != 0 {
		t.Errorf("rcon = %v, want keine Befehle bei offline-Server", r.commands())
	}
}

func TestBackupAbortsWhenSaveOffFails(t *testing.T) {
	d := &fakeDocker{}
	r := &fakeRCON{fails: map[string]bool{"save-off": true}}
	_, err := newRunner(d, r, true).Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "save-off") {
		t.Fatalf("err = %v, want save-off-Abbruch", err)
	}
	if d.started {
		t.Error("Backup-Container gestartet trotz save-off-Fehler")
	}
}

func TestBackupFlushFailureRestoresAutoSave(t *testing.T) {
	d := &fakeDocker{}
	r := &fakeRCON{fails: map[string]bool{"save-all flush": true}}
	_, err := newRunner(d, r, true).Run(context.Background())
	if err == nil {
		t.Fatal("want error")
	}
	got := r.commands()
	if got[len(got)-1] != "save-on" {
		t.Errorf("rcon = %v, save-on muss auch im Fehlerfall laufen", got)
	}
}

func TestBackupNonZeroExitFailsWithLogsAndSaveOn(t *testing.T) {
	d := &fakeDocker{exitCode: 1, logs: "Fatal: unable to open repository\n"}
	r := &fakeRCON{}
	_, err := newRunner(d, r, true).Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "exit 1") || !strings.Contains(err.Error(), "unable to open repository") {
		t.Fatalf("err = %v, want exit-1 mit Log-Ausschnitt", err)
	}
	got := r.commands()
	if got[len(got)-1] != "save-on" {
		t.Errorf("rcon = %v, save-on muss auch bei Backup-Fehler laufen", got)
	}
}

func TestBackupRefusesDoubleRun(t *testing.T) {
	d := &fakeDocker{running: true}
	_, err := newRunner(d, &fakeRCON{}, false).Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "läuft bereits") {
		t.Fatalf("err = %v, want Doppelstart-Schutz", err)
	}
}

func TestBackupOnlineWithoutRCONRefuses(t *testing.T) {
	d := &fakeDocker{}
	run := New(d, nil, func() collector.MCStatus { return collector.MCStatus{Online: true} },
		nil, "mc-backup", testLogger())
	_, err := run.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "RCON nicht konfiguriert") {
		t.Fatalf("err = %v, want Abbruch ohne RCON bei laufendem Server", err)
	}
}

func TestBackupTimeout(t *testing.T) {
	d := &fakeDocker{polls: 100000}
	r := &fakeRCON{}
	run := newRunner(d, r, false)
	run.Timeout = 30 * time.Millisecond
	_, err := run.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "nicht fertig") {
		t.Fatalf("err = %v, want Timeout", err)
	}
}

func TestResticSummary(t *testing.T) {
	logs := "irrelevant\nprocessed 100 files, 2 GiB in 0:30\nAdded to the repository: 55 MiB\nsnapshot ab12cd34 saved\n"
	got := resticSummary(logs)
	for _, want := range []string{"processed 100 files", "Added to the repo", "snapshot ab12cd34 saved"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary %q fehlt %q", got, want)
		}
	}
	if s := resticSummary("nur rauschen"); !strings.Contains(s, "keine restic-Zusammenfassung") {
		t.Errorf("leere summary = %q", s)
	}
}
