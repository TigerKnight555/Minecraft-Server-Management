package upgrade

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/collector"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
)

// Originalauszug aus dem gescheiterten 26.2-Update vom 29.07.2026.
const realCrashLog = `
[init] Image info: buildtime=2025-08-09T18:41:31.220Z,version=java21,revision=0d834dd
[init] Starting the Minecraft server...
[17:18:50] [ERROR] [FabricLoader/]: Uncaught exception in thread "main"
java.lang.RuntimeException: An exception occurred when launching the server!
Caused by: java.lang.RuntimeException: Error invoking MC server bundler: java.lang.UnsupportedClassVersionError: net/minecraft/bundler/Main has been compiled by a more recent version of the Java Runtime (class file version 69.0), this version of the Java Runtime only recognizes class file versions up to 65.0
2026-07-29T17:18:50.699Z WARN mc-server-runner Minecraft server failed. {"exitCode": 1}
`

func TestDiagnoseRecognisesJavaMismatch(t *testing.T) {
	got := Diagnose(realCrashLog)
	if got == "" {
		t.Fatal("Java-Fehler nicht erkannt")
	}
	// 69.0 -> Java 25, 65.0 -> Java 21
	for _, want := range []string{"Java 25", "Java 21", "pull"} {
		if !strings.Contains(got, want) {
			t.Errorf("Diagnose nennt %q nicht: %s", want, got)
		}
	}
}

func TestDiagnoseOtherCases(t *testing.T) {
	cases := []struct {
		name, log, want string
	}{
		{"inkompatible Mods", "Incompatible mods found in the environment", "Mod passt nicht"},
		{"fehlende Abhängigkeit", "Incompatible mods found\n - Mod 'X' requires any version of midnightlib, which is missing!", "midnightlib"},
		{"zu wenig RAM", "java.lang.OutOfMemoryError: Java heap space", "Arbeitsspeicher"},
		{"EULA", "You need to agree to the EULA in order to run the server", "EULA"},
		{"Port belegt", "FAILED TO BIND TO PORT!", "Port"},
		{"nichts Bekanntes", "irgendein harmloses Log", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Diagnose(tc.log)
			if tc.want == "" {
				if got != "" {
					t.Errorf("unerwartete Diagnose: %s", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("Diagnose = %q, sollte %q enthalten", got, tc.want)
			}
		})
	}
}

func TestJavaFromClassFile(t *testing.T) {
	for cf, want := range map[int]int{65: 21, 69: 25, 61: 17, 44: 0} {
		if got := javaFromClassFile(cf); got != want {
			t.Errorf("Class-File %d -> Java %d, want %d", cf, got, want)
		}
	}
}

func TestLastLinesKeepsTail(t *testing.T) {
	got := LastLines("a\n\nb\nc\n\n", 2)
	if got != "b\nc" {
		t.Errorf("LastLines = %q, want \"b\\nc\"", got)
	}
}

// Watchdog bricht bei einer Absturzschleife früh ab — mit Klartext-Ursache
// statt 25 Minuten Warten und „bitte Logs prüfen".
func TestWatchdogAbortsOnCrashLoopWithDiagnosis(t *testing.T) {
	f := &fakes{backupOK: true}
	st := readyFor("26.2", 0) // keine Java-Anforderung -> Guard greift nicht
	bus := events.New()
	ch, cancel := bus.Subscribe(16)
	defer cancel()

	o := New(f, f, f,
		func() collector.MCStatus { return collector.MCStatus{} }, // nie online
		f, f, fixedReadiness{st}, f, bus, "mc-fabric", testLogger())
	o.WarnMinutes = 1
	o.WarnStep = time.Millisecond
	o.OnlineTimeout = 10 * time.Second // groß — der Abbruch muss vorher kommen
	o.PollStep = time.Millisecond
	o.CrashLimit = 3
	// dieser Test prüft die Diagnose, nicht den Rückfall
	o.AutoRollback = false

	restarts := 0
	o.Inspect = func(context.Context) (collector.ContainerDetail, error) {
		restarts++ // jede Abfrage zählt einen weiteren Fehlstart
		return collector.ContainerDetail{RestartCount: restarts, ExitCode: 1}, nil
	}
	o.TailLogs = func(context.Context, int) (string, error) { return realCrashLog, nil }

	if err := o.Start("26.2"); err != nil {
		t.Fatal(err)
	}
	waitDone(t, o)

	var failMsg string
	for _, ev := range drain(ch) {
		if ev.Type == events.TypeUpgradeFailed {
			for _, fl := range ev.Fields {
				failMsg += fl.Value
			}
		}
	}
	if failMsg == "" {
		t.Fatal("keine Fehlermeldung veröffentlicht")
	}
	if !strings.Contains(failMsg, "startet nicht") {
		t.Errorf("Meldung nennt die Absturzschleife nicht: %s", failMsg)
	}
	if !strings.Contains(failMsg, "Java 25") {
		t.Errorf("Meldung enthält keine Diagnose: %s", failMsg)
	}
}
