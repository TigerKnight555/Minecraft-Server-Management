package upgrade

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/collector"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mods"
)

// Log eines Servers, der die Welt nie geöffnet hat (der reale 26.2-Fall).
const logWorldUntouched = realCrashLog

// Log eines Starts, der die Welt geladen hat.
const logWorldOpened = `
[init] Starting the Minecraft server...
[17:24:57] [main/INFO]: Loading Minecraft 26.2 with Fabric Loader 0.19.3
[17:25:10] [Server thread/INFO]: Preparing level "world"
[17:25:12] [Server thread/INFO]: Done (1.577s)! For help, type "help"
`

func TestWorldOpenedDetection(t *testing.T) {
	if WorldOpened(logWorldUntouched) {
		t.Error("Welt fälschlich als geöffnet erkannt — der Server kam nie so weit")
	}
	if !WorldOpened(logWorldOpened) {
		t.Error("geöffnete Welt nicht erkannt")
	}
}

// Nur der LETZTE Startversuch zählt: frühere Läufe der alten Version haben
// die Welt natürlich geöffnet, das darf den Rückfall nicht blockieren.
func TestWorldOpenedLooksAtLastAttemptOnly(t *testing.T) {
	combined := logWorldOpened + "\n" + logWorldUntouched
	if WorldOpened(combined) {
		t.Error("alter Lauf blockiert den Rückfall — nur der jüngste Versuch zählt")
	}
}

type rollbackFakes struct {
	*fakes
	signalled  atomic.Value // string: letzte angeforderte Version
	modRolled  atomic.Bool
	comeBackAs string // Version, die der Server nach dem Rückfall meldet
}

func (r *rollbackFakes) RequestUpgrade(version string) error {
	r.signalled.Store(version)
	if version == r.comeBackAs {
		r.fakes.version.Store(version)
		r.fakes.online.Store(true)
	}
	return nil
}

func newRollbackOrch(t *testing.T, logText string, comeBack string) (*Orchestrator, *rollbackFakes, <-chan events.Event) {
	t.Helper()
	base := &fakes{backupOK: true}
	rf := &rollbackFakes{fakes: base, comeBackAs: comeBack}
	bus := events.New()
	ch, cancel := bus.Subscribe(32)
	t.Cleanup(cancel)

	o := New(base, base, base, base.mcStatus, base, base,
		fixedReadiness{readyFor("26.2", 0)}, rf, bus, "mc-fabric", testLogger())
	o.WarnMinutes = 1
	o.WarnStep = time.Millisecond
	o.OnlineTimeout = 60 * time.Millisecond
	o.PollStep = time.Millisecond
	o.RollbackTimeout = time.Second
	o.CrashLimit = 0 // nur der Timeout-Pfad in diesem Test
	o.TailLogs = func(context.Context, int) (string, error) { return logText, nil }
	o.ModRollback = func(_, label string) (int, error) {
		if label != "L" {
			t.Errorf("Mod-Rollback mit Label %q, want L (der Satz vom Sprung)", label)
		}
		rf.modRolled.Store(true)
		return 2, nil
	}
	return o, rf, ch
}

// Welt unangetastet -> automatischer Rückfall, Mods zurück, Server wieder da.
func TestFailedUpgradeRollsBackWhenWorldUntouched(t *testing.T) {
	o, rf, ch := newRollbackOrch(t, logWorldUntouched, "1.21.11")
	// Ausgangsversion, die der Rückfall wiederherstellen soll
	rf.fakes.version.Store("1.21.11")
	rf.fakes.online.Store(true)

	if err := o.Start("26.2"); err != nil {
		t.Fatal(err)
	}
	waitDone(t, o)

	if got, _ := rf.signalled.Load().(string); got != "1.21.11" {
		t.Errorf("Rückfall-Signal = %q, want 1.21.11", got)
	}
	if !rf.modRolled.Load() {
		t.Error("Mods wurden nicht zurückgerollt")
	}
	var sawBack bool
	for _, ev := range drain(ch) {
		if strings.Contains(ev.Title, "läuft wieder auf 1.21.11") {
			sawBack = true
		}
	}
	if !sawBack {
		t.Error("keine Meldung über den geglückten Rückfall")
	}
}

// Welt bereits geladen -> KEIN Rückfall, dafür ein klarer Hinweis aufs Backup.
func TestFailedUpgradeKeepsHandsOffWhenWorldOpened(t *testing.T) {
	o, rf, ch := newRollbackOrch(t, logWorldOpened, "1.21.11")
	rf.fakes.version.Store("1.21.11")
	rf.fakes.online.Store(true)

	if err := o.Start("26.2"); err != nil {
		t.Fatal(err)
	}
	waitDone(t, o)

	// RequestUpgrade darf nur EINMAL gelaufen sein (der Sprung selbst),
	// nicht noch einmal für den Rückfall
	if got, _ := rf.signalled.Load().(string); got != "26.2" {
		t.Errorf("es wurde zurückgerollt, obwohl die Welt schon geladen war (Signal=%q)", got)
	}
	if rf.modRolled.Load() {
		t.Error("Mods zurückgerollt, obwohl die Welt schon konvertiert sein kann")
	}
	var msg string
	for _, ev := range drain(ch) {
		if ev.Type == events.TypeUpgradeFailed {
			for _, f := range ev.Fields {
				msg += f.Value
			}
		}
	}
	if !strings.Contains(msg, "Backup") {
		t.Errorf("Meldung verweist nicht aufs Backup: %s", msg)
	}
}

// Ohne Logeinsicht wird bewusst nichts automatisch verändert.
func TestNoRollbackWithoutLogAccess(t *testing.T) {
	o, rf, _ := newRollbackOrch(t, "", "1.21.11")
	o.TailLogs = nil
	rf.fakes.version.Store("1.21.11")
	rf.fakes.online.Store(true)

	if err := o.Start("26.2"); err != nil {
		t.Fatal(err)
	}
	waitDone(t, o)

	if got, _ := rf.signalled.Load().(string); got != "26.2" {
		t.Errorf("Rückfall ohne Logeinsicht ausgelöst (Signal=%q)", got)
	}
}

// Hat der Sprung keine Mods getauscht, darf der Rückfall auch keine Mods
// anfassen — der neueste Backup-Satz stammt dann von einem älteren Update.
func TestRollbackLeavesModsAloneWhenNothingWasApplied(t *testing.T) {
	o, rf, _ := newRollbackOrch(t, logWorldUntouched, "1.21.11")
	rf.fakes.applyErr = mods.ErrNothingStaged
	rf.fakes.version.Store("1.21.11")
	rf.fakes.online.Store(true)

	if err := o.Start("26.2"); err != nil {
		t.Fatal(err)
	}
	waitDone(t, o)

	if got, _ := rf.signalled.Load().(string); got != "1.21.11" {
		t.Errorf("Rückfall-Signal = %q, want 1.21.11", got)
	}
	if rf.modRolled.Load() {
		t.Error("Mods zurückgerollt, obwohl der Sprung keine getauscht hat")
	}
}

// Scheitert das Einspielen der Server-Mods mittendrin, werden die schon
// getauschten zurückgerollt, bevor der alte Server wieder startet.
func TestPartialModApplyIsRolledBack(t *testing.T) {
	o, rf, _ := newRollbackOrch(t, logWorldUntouched, "1.21.11")
	rf.fakes.applyErr = errors.New("disk full")
	rf.fakes.version.Store("1.21.11")
	rf.fakes.online.Store(true)

	if err := o.Start("26.2"); err != nil {
		t.Fatal(err)
	}
	waitDone(t, o)

	if !rf.modRolled.Load() {
		t.Error("teilweise getauschte Mods nicht zurückgerollt")
	}
	if got, _ := rf.signalled.Load().(string); got != "" {
		t.Errorf("Host-Signal trotz Mod-Fehler: %q", got)
	}
	steps := rf.fakes.list()
	if steps[len(steps)-1] != "start" {
		t.Errorf("alter Server nicht wieder gestartet: %v", steps)
	}
}

// Meldet der Host-Helfer einen gescheiterten Neubau, kommt der Rückfall
// sofort — ohne Timeout und obwohl das Log (des alten Containers) eine
// geöffnete Welt zeigt: die neue Version lief nie.
func TestHostFailureTriggersImmediateRollback(t *testing.T) {
	o, rf, ch := newRollbackOrch(t, logWorldOpened, "1.21.11")
	o.OnlineTimeout = 30 * time.Second // der Rückfall muss lange vorher kommen
	o.HostFailure = func() (string, bool) {
		got, _ := rf.signalled.Load().(string)
		return "compose up gescheitert", got == "26.2"
	}
	rf.fakes.version.Store("1.21.11")
	rf.fakes.online.Store(true)

	start := time.Now()
	if err := o.Start("26.2"); err != nil {
		t.Fatal(err)
	}
	waitDone(t, o)

	if time.Since(start) > 3*time.Second {
		t.Errorf("Rückfall kam erst nach %s — bis zum Timeout gewartet?", time.Since(start))
	}
	if got, _ := rf.signalled.Load().(string); got != "1.21.11" {
		t.Errorf("Rückfall-Signal = %q, want 1.21.11", got)
	}
	if !rf.modRolled.Load() {
		t.Error("Mods für die Zielversion nicht zurückgerollt")
	}
	var back bool
	var details string
	for _, ev := range drain(ch) {
		if strings.Contains(ev.Title, "läuft wieder auf 1.21.11") {
			back = true
		}
		for _, f := range ev.Fields {
			details += f.Value
		}
	}
	if !back {
		t.Error("keine Meldung über den geglückten Rückfall")
	}
	if !strings.Contains(details, "compose up gescheitert") {
		t.Errorf("Ursache vom Host fehlt in den Details: %q", details)
	}
}

// Ein veralteter „online"-Snapshot von vor dem Stopp ist kein Beleg, dass
// der alte Server zurück ist (in der Testumgebung beobachtet: Erfolgsmeldung
// vor dem Container-Neubau).
func TestStaleStatusIsNoRollbackSuccess(t *testing.T) {
	o, rf, ch := newRollbackOrch(t, logWorldOpened, "")
	o.RollbackTimeout = 100 * time.Millisecond
	o.HostFailure = func() (string, bool) {
		got, _ := rf.signalled.Load().(string)
		return "compose up gescheitert", got == "26.2"
	}
	rf.fakes.version.Store("1.21.11")
	rf.fakes.online.Store(true)
	rf.fakes.staleStatus.Store(true)

	if err := o.Start("26.2"); err != nil {
		t.Fatal(err)
	}
	waitDone(t, o)

	for _, ev := range drain(ch) {
		if strings.Contains(ev.Title, "läuft wieder") {
			t.Errorf("Erfolg auf Basis eines veralteten Snapshots gemeldet: %q", ev.Title)
		}
	}
}

var _ = collector.MCStatus{}
