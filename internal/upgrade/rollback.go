package upgrade

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
)

// Nach einem gescheiterten Versionssprung blieb bisher alles stehen: die neue
// MC_VERSION gesetzt, der Container in der Neustart-Schleife, der Server für
// die Spieler tot — obwohl die Welt beim 26.2-Versuch nie angefasst wurde und
// ein Rückweg gefahrlos gewesen wäre (Learning 19).
//
// Automatisch zurück geht es NUR, wenn der letzte Startversuch beweist, dass
// die Welt nicht geöffnet wurde. Ist sie schon konvertiert, wäre ein
// Versionsrücksprung sinnlos (Minecraft lädt keine neuere Welt) — dann bleibt
// nur das Backup, und genau das sagt die Meldung dann auch.

var (
	// itzg schreibt diese Zeile bei jedem Startversuch — daran schneiden wir
	// den letzten Versuch aus dem Gesamtlog heraus.
	reStartBanner = regexp.MustCompile(`\[init\] Starting the Minecraft server`)
	// Belege dafür, dass die Welt tatsächlich geöffnet/konvertiert wurde
	reWorldOpened = regexp.MustCompile(`(?i)Preparing level|Preparing spawn area|Upgrading world|Forcing world upgrade|Done \(`)
)

// lastAttempt schneidet den jüngsten Startversuch aus dem Log.
func lastAttempt(logText string) string {
	locs := reStartBanner.FindAllStringIndex(logText, -1)
	if len(locs) == 0 {
		return logText
	}
	return logText[locs[len(locs)-1][0]:]
}

// WorldOpened meldet, ob der letzte Startversuch die Welt angefasst hat.
func WorldOpened(logText string) bool {
	return reWorldOpened.MatchString(lastAttempt(logText))
}

// recover versucht nach einem gescheiterten Sprung den alten Zustand
// wiederherzustellen. Der Rückgabewert ist immer ein Fehler — er beschreibt,
// was passiert ist; der Aufrufer meldet ihn nach Discord.
func (o *Orchestrator) recover(ctx context.Context, from string, cause error) error {
	if !o.AutoRollback || from == "" || o.signal == nil {
		return cause
	}
	// Ohne Logeinsicht kein automatischer Eingriff — zu riskant.
	if o.TailLogs == nil {
		return fmt.Errorf("%w — automatischer Rückfall übersprungen (keine Logeinsicht)", cause)
	}
	logText, err := o.TailLogs(ctx, 2000)
	if err != nil {
		return fmt.Errorf("%w — automatischer Rückfall übersprungen (Log nicht lesbar: %v)", cause, err)
	}
	if WorldOpened(logText) {
		return fmt.Errorf("%w — KEIN automatischer Rückfall: die Welt wurde bereits geladen und ggf. konvertiert. "+
			"Ein Versionsrücksprung würde sie nicht mehr öffnen. Rückweg ist das Backup von vor dem Update", cause)
	}

	o.setStatus("Rückfall auf " + from)
	o.bus.Publish(events.Event{
		Type: events.TypeUpgradeFailed, Severity: events.SevWarn,
		Title:   "↩️ Update fehlgeschlagen — Server kommt auf " + from + " zurück",
		Message: "Die Welt wurde nicht angefasst. Der alte Stand wird wiederhergestellt, gleich geht es weiter.",
	})

	// Mods gehören zur Version: die Kette hat sie auf die Zielversion
	// gehoben, für den alten Server müssen sie zurück.
	modNote := ""
	if o.ModRollback != nil {
		if n, err := o.ModRollback("server"); err != nil {
			modNote = fmt.Sprintf(" (Mod-Rollback fehlgeschlagen: %v — im Mods-Tab prüfen)", err)
		} else {
			modNote = fmt.Sprintf(" (%d Mod-Datei(en) zurückgerollt)", n)
		}
	}
	if err := o.signal.RequestUpgrade(from); err != nil {
		return fmt.Errorf("%w — Rückfall auf %s ebenfalls fehlgeschlagen: %v", cause, from, err)
	}

	// kurzer Watchdog: kommt der alte Stand wieder hoch?
	deadline := time.Now().Add(o.RollbackTimeout)
	for time.Now().Before(deadline) {
		if st := o.mcStatus(); st.Online && st.Version == from {
			o.bus.Publish(events.Event{
				Type: events.TypeServerUp, Severity: events.SevSuccess,
				Title:   "✅ Server läuft wieder auf " + from,
				Message: "Das Update hat nicht geklappt, der alte Stand ist zurück — die Welt ist unversehrt." + modNote,
			})
			return fmt.Errorf("%w — Server wurde automatisch auf %s zurückgesetzt und läuft wieder%s", cause, from, modNote)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(o.PollStep):
		}
	}
	return fmt.Errorf("%w — Rückfall auf %s angestoßen, der Server meldet sich aber noch nicht%s", cause, from, modNote)
}
