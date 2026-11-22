// Package upgrade orchestrates the one-click Minecraft version bump: der
// Nutzer klickt im Dashboard, MSM erledigt die komplette Kette. Der einzige
// privilegierte Schritt (MC_VERSION setzen + Container neu erstellen) läuft
// über den Host-Helfer (Signaldatei, deploy/host-watcher/) — MSM darf keine
// Container erstellen (Socket-Proxy-Vertrauensschnitt).
//
// Kette: Guards (bereit? nichts anderes läuft?) → Spieler-Warnungen →
// save-all → Stop → Pflicht-Backup (Rollback-Punkt — das Welt-Upgrade ist
// UNUMKEHRBAR) → Server-Mods für die Zielversion stagen+einspielen →
// upgrade.request → Host erstellt mc-fabric neu (itzg lädt Server-JAR +
// frischen Fabric-Loader) → Watchdog bis „online mit Zielversion" →
// Client-Mods nachziehen → Erfolgsmeldung.
package upgrade

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/collector"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mods"
)

type resolver interface {
	Containers() []collector.Container
}

// BackupRunner runs one restic snapshot (backup.Runner satisfies this).
type BackupRunner interface {
	Run(ctx context.Context) (string, error)
}

// UpgradeSignaler writes the host request (hostctl.Signaler satisfies this).
type UpgradeSignaler interface {
	RequestUpgrade(version string) error
}

// ModManager is the mods surface the chain needs (mods.Manager satisfies it).
type ModManager interface {
	CheckUpdates(ctx context.Context, profile, mcVersion string) ([]mods.Entry, error)
	Stage(ctx context.Context, profile string, filenames []string) (int, error)
	ApplyStaged(profile string) (string, int, error)
}

// Readiness liefert den letzten Versions-Watch (mods.Watcher satisfies it).
type Readiness interface {
	Last() *mods.WatchStatus
}

type Orchestrator struct {
	rcon       collector.RCONClient
	controller collector.ContainerController
	containers resolver
	mcStatus   func() collector.MCStatus
	backup     BackupRunner
	modmgr     ModManager
	watch      Readiness
	signal     UpgradeSignaler
	bus        *events.Bus
	mcName     string
	log        *slog.Logger

	WarnMinutes   int
	WarnStep      time.Duration
	OnlineTimeout time.Duration // Welt-Upgrade beim ersten Start kann dauern
	PollStep      time.Duration

	// Publish veröffentlicht das Client-Paket (Dropbox, Modrinth) —
	// optional; läuft nach jedem erfolgreichen Sprung (außer bei
	// gescheitertem Client-Weg). published=true nur, wenn eine
	// „📦 Neues Mod-Paket"-Meldung an die Spieler ging; false ohne
	// eingerichteten Versandweg oder wenn kein Kanal etwas verschickt hat.
	Publish func(ctx context.Context) (published bool, err error)

	// JavaProbe meldet die Java-Hauptversion des laufenden Container-Images
	// (0 = unbekannt). Neuere Minecraft-Versionen verlangen neueres Java
	// (Learning 15, 26.2 braucht Java 25). Das neue Image holt der
	// Host-Watcher beim Sprung; hier nur für den Hinweis im Start-Event.
	JavaProbe func(ctx context.Context) int

	// Inspect/Tail sind optional und dienen dem Watchdog: Absturzschleifen
	// früh erkennen (RestartCount) und die Ursache aus dem Log lesen, statt
	// 25 Minuten stumm zu warten und dann „bitte Logs prüfen" zu melden
	// (Learning 17/18).
	Inspect  func(ctx context.Context) (collector.ContainerDetail, error)
	TailLogs func(ctx context.Context, lines int) (string, error)
	// CrashLimit: so viele Neustarts während des Watchdogs gelten als
	// Absturzschleife -> sofortiger Abbruch mit Diagnose.
	CrashLimit int

	// AutoRollback setzt den Server nach einem gescheiterten Sprung selbst
	// auf die alte Version zurück — aber nur, wenn die Welt nachweislich
	// nicht geöffnet wurde (Learning 19). ModRollback holt genau den beim
	// Sprung ersetzten Mod-Satz zurück (mods.Manager.RollbackLabel).
	AutoRollback    bool
	RollbackTimeout time.Duration
	ModRollback     func(profile, label string) (int, error)
	// ModSeal markiert die Mod-Sätze eines gelungenen Sprungs: ein späterer
	// normaler Rollback darf nicht dahinter zurück (er setzte sonst Mods der
	// alten MC-Version ein — Server startet nicht; WSL-Test 2026-11-12).
	ModSeal func(profile, label, version string) error

	// HostFailure liest die Rückmeldung des Host-Helfers (upgrade.failed):
	// scheitert dort der Container-Neubau, steht die .env schon wieder auf
	// der alten Version und der neue Server lief nie. Optional —
	// hostctl.Signaler.UpgradeFailure erfüllt das.
	HostFailure func() (string, bool)

	// PendingChanges meldet manuell vorgemerkte Mod-Änderungen (Mods-Tab).
	// Sie gehören zur ALTEN Version — ein Sprung würde sie mit einspielen.
	// Optional; true = Start verweigern.
	PendingChanges func() bool

	// RoutineBusy nennt die gerade laufende Server-Routine ("" = keine) —
	// ein Versionssprung mitten in einer Backup-/Neustart-Kette würde sich
	// mit ihr um den Server streiten.
	RoutineBusy func() string
	// BasepackReady: gibt es das Basis-Modpack für die Zielversion?
	// set=false = kein Basis-Pack eingerichtet; err = Modrinth nicht erreichbar.
	BasepackReady func(ctx context.Context, mc string) (name string, ready, set bool, err error)
	// BasepackSwitch stellt das Basis-Modpack nach dem Sprung um.
	BasepackSwitch func(ctx context.Context, mc string) error
	// Pinned: Basis-Pack-Datei? Solche Mods sind nie Nachzügler (wechseln
	// mit dem Pack); nil = keine.
	Pinned func(profile, filename string) bool

	// OnSuccess läuft nach einem erfolgreichen Sprung (z. B. Versions-Watch
	// und Mod-Inventar sofort neu prüfen — sonst zeigt die Kachel bis zum
	// nächsten Tageslauf den alten Stand und bietet dasselbe Update erneut an).
	// Optional; muss selbst entscheiden, ob es asynchron läuft.
	OnSuccess func(ctx context.Context, version string)

	mu      sync.Mutex
	running bool
	status  string
}

func New(rcon collector.RCONClient, controller collector.ContainerController, containers resolver,
	mcStatus func() collector.MCStatus, backup BackupRunner, modmgr ModManager,
	watch Readiness, signal UpgradeSignaler, bus *events.Bus, mcName string, log *slog.Logger) *Orchestrator {
	return &Orchestrator{
		rcon: rcon, controller: controller, containers: containers,
		mcStatus: mcStatus, backup: backup, modmgr: modmgr,
		watch: watch, signal: signal, bus: bus, mcName: mcName, log: log,
		WarnMinutes:   5,
		WarnStep:      time.Minute,
		OnlineTimeout: 25 * time.Minute,
		PollStep:      10 * time.Second,
		CrashLimit:    3,

		AutoRollback:    true,
		RollbackTimeout: 10 * time.Minute,
	}
}

// Status returns the current progress line ("" = kein Upgrade aktiv).
func (o *Orchestrator) Status() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.status
}

// Active meldet, ob gerade ein Versionssprung läuft. Der Down-Wächter muss
// währenddessen schweigen — ein geplantes Update ist nie „unerwartet offline"
// (Learning 15: das gescheiterte 26.2-Update erzeugte 6 Fehlalarme).
func (o *Orchestrator) Active() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.running
}

func (o *Orchestrator) setStatus(s string) {
	o.mu.Lock()
	o.status = s
	o.mu.Unlock()
	if s != "" {
		o.log.Info("upgrade", "status", s)
	}
}

// checkLocked führt die billigen Vorab-Prüfungen aus. o.mu muss gehalten sein.
func (o *Orchestrator) checkLocked(version string) error {
	if o.running {
		return fmt.Errorf("es läuft bereits ein Upgrade (%s)", o.status)
	}
	if o.RoutineBusy != nil {
		if busy := o.RoutineBusy(); busy != "" {
			return fmt.Errorf("gerade läuft „%s“ — das Update danach starten", busy)
		}
	}
	if o.PendingChanges != nil && o.PendingChanges() {
		return fmt.Errorf("im Mods-Tab sind Änderungen vorgemerkt — erst anwenden oder verwerfen, dann das Update starten")
	}
	// veralteter Versions-Watch darf keinen zweiten Sprung auf dieselbe
	// Version auslösen (Backup + Neustart + Neubau für nichts)
	if o.mcStatus != nil {
		if cur := o.mcStatus().Version; cur != "" && cur == version {
			return fmt.Errorf("der Server läuft bereits auf Minecraft %s — Versions-Check mit „Jetzt prüfen“ aktualisieren", version)
		}
	}
	// Guards VOR dem Start — der Button darf nur Grünes auslösen
	last := o.watch.Last()
	if last == nil || !last.NewerAvailable {
		return fmt.Errorf("kein Update verfügbar (Versions-Watch lief noch nicht?)")
	}
	if version != last.LatestVersion {
		return fmt.Errorf("angefragte Version %q ist nicht die geprüfte Zielversion %q", version, last.LatestVersion)
	}
	if !last.LoaderReady {
		return fmt.Errorf("fabric-Loader unterstützt %s noch nicht", version)
	}
	for _, p := range last.Profiles {
		if p.Profile == "server" && (p.Total == 0 || p.Ready < p.Total) {
			return fmt.Errorf("noch nicht alle Server-Mods für %s bereit (%d/%d)", version, p.Ready, p.Total)
		}
	}
	return nil
}

// Start validates the request and launches the chain in the background.
func (o *Orchestrator) Start(version string) error {
	o.mu.Lock()
	err := o.checkLocked(version)
	o.mu.Unlock()
	if err != nil {
		return err
	}
	// Basis-Pack-Abfrage = Netzwerk (bis 30 s): ohne o.mu, sonst hängen
	// Status()/Active() und damit die UI-Abfrage so lange.
	if o.BasepackReady != nil {
		bctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		name, ready, set, berr := o.BasepackReady(bctx, version)
		cancel()
		switch {
		case berr != nil:
			name = nameOr(name, "Basis-Modpack")
			return fmt.Errorf("Basis-Modpack „%s“ konnte bei Modrinth nicht geprüft werden (%v) — später noch einmal versuchen", name, berr)
		case set && !ready:
			return fmt.Errorf("Basis-Modpack „%s“ gibt es noch nicht für Minecraft %s — abwarten oder Basis-Pack im Mods-Tab entfernen", name, version)
		}
	}
	// Zustand kann sich währenddessen geändert haben (zweiter Start, neue
	// Vormerkung) — unter Sperre erneut prüfen, dann erst running setzen.
	o.mu.Lock()
	if err := o.checkLocked(version); err != nil {
		o.mu.Unlock()
		return err
	}
	o.running = true
	o.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Minute)
		defer cancel()
		defer func() {
			o.mu.Lock()
			o.running = false
			o.status = ""
			o.mu.Unlock()
		}()
		if err := o.run(ctx, version); err != nil {
			o.log.Error("upgrade fehlgeschlagen", "version", version, "err", err)
			o.bus.Publish(events.Event{
				Type: events.TypeUpgradeFailed, Severity: events.SevError,
				Title:   "⚠️ Minecraft-Update auf " + version + " fehlgeschlagen",
				Message: "Der Admin kümmert sich — Details unten.",
				Fields:  []events.Field{{Name: "Details", Value: err.Error()}},
			})
		}
	}()
	return nil
}

func nameOr(name, fallback string) string {
	if name == "" {
		return fallback
	}
	return name
}

// javaFields nennt im Start-Event einen anstehenden Java-Wechsel. Früher
// blockierte MSM hier (Learning 15: MC 26.2 braucht Java 25, das Image
// lieferte Java 21). Seit der Host-Watcher beim Sprung selbst
// `docker compose pull mc-fabric` ausführt, kommt das neue Java mit dem
// Update; klappt das nicht, erkennt der Watchdog den Java-Fehler und der
// Rücksprung greift.
func (o *Orchestrator) javaFields() []events.Field {
	last := o.watch.Last()
	if last == nil || last.RequiredJava <= 0 || o.JavaProbe == nil {
		return nil
	}
	have := o.JavaProbe(context.Background())
	if have <= 0 || have >= last.RequiredJava {
		return nil
	}
	return []events.Field{{Name: "Java", Value: fmt.Sprintf("%d → %d (neues Server-Image wird geholt)", have, last.RequiredJava)}}
}

func (o *Orchestrator) run(ctx context.Context, version string) error {
	o.bus.Publish(events.Event{
		Type: events.TypeUpgradeStart, Severity: events.SevWarn,
		Title: "⬆️ Minecraft-Update auf " + version + " startet",
		// Ehrliche Zeitangabe: die „ca. 5 min" waren nur die Vorwarnung —
		// beim 26.2-Versuch warteten die Spieler dann über eine halbe Stunde
		// (Learning 20). Backup, Mod-Update und das erste Hochfahren mit
		// Welt-Konvertierung brauchen ein Vielfaches davon.
		Message: fmt.Sprintf("In %d Minuten geht der Server offline. Das Update selbst dauert erfahrungsgemäß 15–30 Minuten (Backup, Mods, Welt-Umstellung) — Meldung folgt, sobald er wieder da ist.", o.WarnMinutes),
		Fields:  o.javaFields(),
	})

	// Ausgangsversion merken — Rückfallziel, falls der Sprung scheitert
	prevVersion := o.mcStatus().Version
	if prevVersion == "" {
		if last := o.watch.Last(); last != nil {
			prevVersion = last.CurrentVersion
		}
	}

	// 1. Spieler warnen (nur wenn der Server läuft)
	id, running := o.resolve()
	if running && o.rcon != nil {
		o.setStatus("warne Spieler")
		for m := o.WarnMinutes; m >= 1; m-- {
			o.rcon.Exec(ctx, fmt.Sprintf("say §cServer-Update auf %s in %d Minute(n)!", version, m))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(o.WarnStep):
			}
		}
		o.rcon.Exec(ctx, "say §cUpdate beginnt JETZT — bis gleich!")
		if _, err := o.rcon.Exec(ctx, "save-all"); err != nil {
			o.log.Warn("save-all vor Upgrade fehlgeschlagen", "err", err)
		}
	}

	// 2. Stoppen
	if running {
		o.setStatus("stoppe Server")
		if err := o.controller.StopContainer(ctx, id); err != nil {
			return fmt.Errorf("stop: %w", err)
		}
	}
	restartOld := func(reason error) error {
		if startErr := o.controller.StartContainer(ctx, id); startErr != nil {
			return fmt.Errorf("%v; Start des alten Servers AUCH fehlgeschlagen: %w", reason, startErr)
		}
		return fmt.Errorf("%w (Server läuft wieder auf der alten Version)", reason)
	}

	// 3. Pflicht-Backup — der Rollback-Punkt, das Welt-Upgrade ist unumkehrbar
	o.setStatus("erstelle Backup (Rollback-Punkt)")
	if o.backup == nil {
		return restartOld(fmt.Errorf("kein Backup-Runner verdrahtet — Upgrade ohne Backup ist tabu"))
	}
	if _, err := o.backup.Run(ctx); err != nil {
		return restartOld(fmt.Errorf("backup fehlgeschlagen — Upgrade abgebrochen: %w", err))
	}

	// 4. Server-Mods für die Zielversion einspielen (Server ist gestoppt)
	o.setStatus("aktualisiere Server-Mods für " + version)
	if _, err := o.modmgr.CheckUpdates(ctx, "server", version); err != nil {
		return restartOld(fmt.Errorf("mod-Check für %s: %w", version, err))
	}
	if _, err := o.modmgr.Stage(ctx, "server", nil); err != nil {
		return restartOld(fmt.Errorf("mods stagen: %w", err))
	}
	// modLabel: genau dieser Backup-Satz gehört zum Sprung — nur er darf
	// beim Rückfall zurückgerollt werden, nie ein älteres Mod-Update
	modLabel := ""
	if label, n, err := o.modmgr.ApplyStaged("server"); err != nil && !errors.Is(err, mods.ErrNothingStaged) {
		// Teil-Tausch: was schon getauscht ist, muss zurück — sonst startet
		// der alte Server mit einem Mix aus alten und neuen Mods
		cause := fmt.Errorf("mods einspielen: %w", err)
		if label != "" && o.ModRollback != nil {
			if _, rbErr := o.ModRollback("server", label); rbErr != nil {
				cause = fmt.Errorf("%w; Zurückrollen der teilweise getauschten Mods ebenfalls fehlgeschlagen: %v", cause, rbErr)
			}
		}
		return restartOld(cause)
	} else if err == nil {
		modLabel = label
		o.log.Info("server-mods für zielversion eingespielt", "count", n, "backup", label)
	}

	// 5. Host-Helfer: MC_VERSION setzen + Container neu erstellen
	o.setStatus("erstelle Server mit " + version + " neu (Host-Helfer)")
	signalled := time.Now()
	if err := o.signal.RequestUpgrade(version); err != nil {
		return restartOld(err)
	}

	// 6. Watchdog: online UND Zielversion — das erste Hochfahren macht das
	// Welt-Upgrade und darf dauern
	o.setStatus("warte auf Server mit " + version + " (Welt-Upgrade kann dauern)")
	deadline := time.Now().Add(o.OnlineTimeout)
	baseRestarts := -1 // Startwert erst beim ersten erfolgreichen Inspect
	for time.Now().Before(deadline) {
		if st := o.mcStatus(); freshOnline(st, version, signalled) {
			o.seal("server", modLabel, version)
			return o.finish(ctx, version)
		}
		// Host konnte den Container nicht neu erstellen: nicht bis zum
		// Timeout warten — die neue Version lief nie, die Welt ist sicher
		// unangetastet, der Rückfall braucht keine Logprüfung
		if o.HostFailure != nil {
			if msg, failed := o.HostFailure(); failed {
				return o.recover(ctx, prevVersion, modLabel,
					fmt.Errorf("host-helfer konnte den Server nicht mit %s neu erstellen: %s", version, msg), true)
			}
		}
		// Absturzschleife? Dann nicht bis zum Timeout warten — der Server
		// startet reproduzierbar nicht (Learning 18).
		if o.Inspect != nil && o.CrashLimit > 0 {
			if det, err := o.Inspect(ctx); err == nil {
				if baseRestarts < 0 {
					baseRestarts = det.RestartCount
				} else if det.RestartCount-baseRestarts >= o.CrashLimit {
					return o.recover(ctx, prevVersion, modLabel, fmt.Errorf("server startet nicht (%d Fehlversuche in Folge): %s",
						det.RestartCount-baseRestarts, o.explain(ctx)), false)
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(o.PollStep):
		}
	}
	return o.recover(ctx, prevVersion, modLabel, fmt.Errorf("server meldete sich nach %s nicht mit Version %s: %s",
		o.OnlineTimeout, version, o.explain(ctx)), false)
}

// freshOnline: der Server meldet sich mit der erwarteten Version — und zwar
// in einer Abfrage NACH since. Ältere Werte zählen nicht: ist der Container
// weg, hängt die Abfrage in der Namensauflösung (bis ~1 min), und bis dahin
// steht noch „online" von vor dem Stopp im Snapshot. In der Testumgebung
// meldete der Rückfall deshalb Erfolg, bevor der Container überhaupt
// neu erstellt war.
func freshOnline(st collector.MCStatus, version string, since time.Time) bool {
	return st.Online && st.Version == version && !st.Time.Before(since)
}

// explain liest das Container-Log und übersetzt bekannte Startfehler in
// Klartext. Ohne Log-Zugriff bleibt es beim bisherigen Hinweis.
func (o *Orchestrator) explain(ctx context.Context) string {
	fallback := "bitte Logs prüfen (bei großen Welten kann das Welt-Upgrade länger dauern; der Server startet ggf. trotzdem fertig)"
	if o.TailLogs == nil {
		return fallback
	}
	logText, err := o.TailLogs(ctx, 400)
	if err != nil || strings.TrimSpace(logText) == "" {
		return fallback
	}
	if d := Diagnose(logText); d != "" {
		return d
	}
	return fallback + "\nLetzte Logzeilen:\n" + LastLines(logText, 8)
}

// finish updates the client profile and celebrates.
func (o *Orchestrator) finish(ctx context.Context, version string) error {
	var clientErrPre error
	if o.BasepackSwitch != nil {
		o.setStatus("stelle Basis-Modpack um")
		if err := o.BasepackSwitch(ctx, version); err != nil {
			o.log.Error("basis-modpack umstellen", "err", err)
			clientErrPre = fmt.Errorf("basis-modpack umstellen: %w", err)
		}
	}
	o.setStatus("aktualisiere Client-Mods")
	clientNote := ""
	clientUpdated := false
	// Jeder Fehler auf dem Client-Weg wird sichtbar — bisher endete ein
	// gescheiterter Check/Download stumm: kein Paket, kein Hinweis.
	var clientErr error
	// Mods, für die der Check gerade einen Build zur Zielversion gefunden
	// hat, sind keine Nachzügler mehr — auch wenn der (bis zu 24 h alte)
	// Versions-Watch das noch behauptet
	nowAvailable := map[string]bool{}
	entries, err := o.modmgr.CheckUpdates(ctx, "client", version)
	for _, e := range entries {
		if e.UpdateVersion != "" && e.Name != "" {
			nowAvailable[e.Name] = true
		}
		// Basis-Pack-Mods: Bereitschaft prüft der Pack-Check, nie entfernen lassen
		if e.Name != "" && o.Pinned != nil && o.Pinned("client", e.Filename) {
			nowAvailable[e.Name] = true
		}
	}
	switch {
	case err != nil:
		clientErr = fmt.Errorf("client-mod-check: %w", err)
	case len(entries) == 0:
		// leeres Client-Paket: nichts zu tun — Stage würde sonst mit
		// „erst Update-Check ausführen" einen Fehlalarm auslösen
	default:
		if _, err := o.modmgr.Stage(ctx, "client", nil); err != nil {
			clientErr = fmt.Errorf("client-mods herunterladen: %w", err)
		} else if label, n, err := o.modmgr.ApplyStaged("client"); err == nil {
			o.seal("client", label, version)
			clientUpdated = true
			clientNote = fmt.Sprintf(" %d Client-Mods wurden aktualisiert.", n)
		} else if !errors.Is(err, mods.ErrNothingStaged) {
			clientErr = fmt.Errorf("client-mods einspielen: %w", err)
		}
	}
	clientErr = errors.Join(clientErrPre, clientErr)
	var fields []events.Field
	if clientErr != nil {
		o.log.Error("client-mods nach upgrade", "err", clientErr)
		clientNote = " (Client-Mods konnten nicht automatisch aktualisiert werden — der Admin kümmert sich.)"
		fields = append(fields, events.Field{Name: "Details", Value: clientErr.Error()})
	}
	// Paket nach jedem erfolgreichen Sprung verteilen — auch ohne geänderte
	// Client-Mods: das Modrinth-Modpack muss die neue MC-/Loader-Version
	// deklarieren. Kanäle mit passendem Stand (Dropbox bei gleichem Inhalt)
	// überspringt packpub. Bei gescheitertem Client-Weg nicht: das Paket
	// hätte alte Mods unter neuer MC-Version, der Admin ist informiert.
	// Den Link nur ankündigen, wenn die 📦-Meldung wirklich kam.
	if clientErr == nil {
		published := false
		var pubErr error
		if o.Publish != nil {
			o.setStatus("veröffentliche Client-Paket")
			published, pubErr = o.Publish(ctx)
		}
		switch {
		case pubErr != nil:
			clientNote += " (Paket-Upload fehlgeschlagen — im Mods-Tab manuell veröffentlichen.)"
		case published:
			clientNote += " Den Download-Link findet ihr in der Meldung „📦 Neues Mod-Paket“."
		case clientUpdated:
			clientNote += " Das neue Mod-Paket verteilt der Admin."
		}
	}
	// Client-Mods ohne Build für die neue Version bleiben im Paket auf dem
	// alten Stand — die Spieler müssen wissen, welche sie vorerst entfernen
	if lagging := o.clientStragglers(version, nowAvailable); len(lagging) > 0 {
		clientNote += fmt.Sprintf(" Noch nicht für %s verfügbar (vorerst aus dem mods-Ordner nehmen): %s.",
			version, strings.Join(lagging, ", "))
	}
	o.bus.Publish(events.Event{
		Type: events.TypeUpgradeOK, Severity: events.SevSuccess,
		Title:   "🎉 Server läuft jetzt auf Minecraft " + version + "!",
		Message: "Update fertig, es kann weitergehen." + clientNote,
		Fields:  fields,
	})
	if o.OnSuccess != nil {
		o.OnSuccess(ctx, version)
	}
	return nil
}

// clientStragglers listet die Client-Mods, für die der Versions-Watch keine
// passende Version zur Zielversion gefunden hat (sortiert).
//
// Nur ein Watch-Ergebnis für genau diese Zielversion zählt: läuft der Watch
// zwischendurch erneut, ist die Liste leer bzw. bezieht sich auf etwas
// anderes.
func (o *Orchestrator) clientStragglers(version string, nowAvailable map[string]bool) []string {
	last := o.watch.Last()
	if last == nil || last.LatestVersion != version {
		return nil
	}
	var out []string
	for name, profile := range last.Stragglers {
		if profile == "client" && !nowAvailable[name] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func (o *Orchestrator) resolve() (id string, running bool) {
	for _, c := range o.containers.Containers() {
		if c.Name == o.mcName {
			return c.ID, c.State == "running"
		}
	}
	return o.mcName, false
}

func (o *Orchestrator) seal(profile, label, version string) {
	if o.ModSeal == nil || label == "" {
		return
	}
	if err := o.ModSeal(profile, label, version); err != nil {
		o.log.Warn("mod-satz versiegeln fehlgeschlagen", "profile", profile, "label", label, "err", err)
	}
}
