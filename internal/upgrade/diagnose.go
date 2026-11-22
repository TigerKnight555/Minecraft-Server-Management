package upgrade

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Beim gescheiterten 26.2-Update stand die Ursache die ganze Zeit im
// Container-Log, MSM meldete aber nur „bitte Logs prüfen" (Learning 17).
// Diagnose() übersetzt die bekannten Startfehler in Klartext.

var (
	// java.lang.UnsupportedClassVersionError: … class file version 69.0 …
	// only recognizes class file versions up to 65.0
	reClassVersion = regexp.MustCompile(`class file version (\d+)\.\d+.*?up to (\d+)\.\d+`)
	reIncompatible = regexp.MustCompile(`(?i)incompatible mod set|Incompatible mods found`)
	reMissingDep   = regexp.MustCompile(`(?i)requires (?:any version of )?([\w.-]+), which is missing`)
	reOOM          = regexp.MustCompile(`OutOfMemoryError`)
	reEULA         = regexp.MustCompile(`(?i)You need to agree to the EULA`)
	rePortBusy     = regexp.MustCompile(`(?i)Address already in use|FAILED TO BIND TO PORT`)
	reDownload     = regexp.MustCompile(`(?i)(failed to download|could not resolve|connection refused).*(server|jar|manifest)`)
)

// javaFromClassFile: Class-File 65 = Java 21, 69 = Java 25 (Offset 44).
func javaFromClassFile(cf int) int {
	if cf < 45 {
		return 0
	}
	return cf - 44
}

// Diagnose liefert eine Klartext-Ursache aus dem Container-Log,
// oder "" wenn nichts Bekanntes gefunden wurde.
func Diagnose(logText string) string {
	if m := reClassVersion.FindStringSubmatch(logText); m != nil {
		need, _ := strconv.Atoi(m[1])
		have, _ := strconv.Atoi(m[2])
		return fmt.Sprintf(
			"Das Container-Image bringt zu altes Java mit: die Zielversion braucht Java %d, das Image liefert Java %d. Lösung: »docker compose pull mc-fabric«, dann Container neu erstellen.",
			javaFromClassFile(need), javaFromClassFile(have))
	}
	if reIncompatible.MatchString(logText) {
		msg := "Fabric hat den Start abgelehnt: mindestens ein Mod passt nicht zur Zielversion."
		if m := reMissingDep.FindStringSubmatch(logText); m != nil {
			msg += fmt.Sprintf(" Fehlende Abhängigkeit: %s.", m[1])
		}
		return msg + " Lösung: Mods im Mods-Tab prüfen oder per Rollback zurück."
	}
	if reOOM.MatchString(logText) {
		return "Der Server ist an zu wenig Arbeitsspeicher gescheitert (OutOfMemoryError) — MEMORY bzw. mem_limit prüfen."
	}
	if reEULA.MatchString(logText) {
		return "Die EULA wurde nicht akzeptiert (EULA=TRUE fehlt in der Container-Umgebung)."
	}
	if rePortBusy.MatchString(logText) {
		return "Der Minecraft-Port ist belegt — läuft noch ein alter Container?"
	}
	if reDownload.MatchString(logText) {
		return "Der Server-Download ist fehlgeschlagen (Netz oder Mojang nicht erreichbar)."
	}
	return ""
}

// LastLines liefert die letzten n nicht-leeren Zeilen — als Anhang für die
// Fehlermeldung, wenn Diagnose() nichts Bekanntes erkennt.
func LastLines(logText string, n int) string {
	var kept []string
	for _, l := range strings.Split(logText, "\n") {
		if s := strings.TrimSpace(l); s != "" {
			kept = append(kept, s)
		}
	}
	if len(kept) > n {
		kept = kept[len(kept)-n:]
	}
	return strings.Join(kept, "\n")
}
