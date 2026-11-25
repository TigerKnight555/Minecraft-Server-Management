# Leistung: Threads, echter RAM, Lag-Protokoll — Design

Stand: 2026-11-25 · Branch `feature/performance` (von `feature/chart-history`) · Vault: „MSM – Leistung"

## Ziel

Der Admin soll sehen, **warum** der Server laggt (TPS-Einbruch, Ghost-Blöcke bei 5 Spielern): wie ausgelastet die Java-Threads sind — vor allem der Server-Thread, der die Spielwelt allein auf einem Kern rechnet —, wie viel Heap wirklich belegt ist, wie lange Garbage-Collection-Pausen den Server anhalten. Weil Lags meist auftreten, wenn niemand aufs Dashboard schaut, hält MSM jeden Lag mit seiner wahrscheinlichen Ursache in einem Protokoll fest.

## Nutzer-Entscheidungen

- Echter RAM über das **GC-Log der JVM** (kein Mod, kein Agent; einmal `mc-fabric` neu erstellen).
- **Graphen + Lag-Protokoll + Live-Thread-Tabelle**; keine Discord-Meldung.
- Eigener Tab **„Leistung"**.

## Datenquellen (neues Paket `internal/jvmstats`, Takt 5 s)

### Threads aus `/host/proc`

- `ContainerDetail` bekommt `Pid int` (Docker-Inspect `State.Pid`, Host-PID des Container-Init).
- Java-Prozess = Prozess mit `comm` `java` in der Nachfahren-Kette dieser PID (Scan von `/host/proc/*/stat`, Feld ppid). Die PID wird nie fest gespeichert; jeder Takt prüft, ob sie noch passt (gleiche `starttime`), sonst neu suchen.
- Pro Thread `/host/proc/<pid>/task/<tid>/comm` (Name) und `.../stat` (utime + stime). Auslastung = Δ(utime+stime) / Δt / `CLK_TCK` (100) in **% eines Kerns**.
- Gruppen: Name ohne laufende Nummer/Index (`C2ME worker #3` → `C2ME worker`, `GC Thread#0` → `GC Thread`, `Worker-Main-12` → `Worker-Main`), Anzeige `Name (Anzahl)`, Auslastung = Summe.
- Kennzahlen: **Server-Thread-CPU** (Thread-Name exakt `Server thread`), **Java gesamt**, **Kerne des Hosts** (aus `/host/proc/stat`).

### Heap und GC aus dem GC-Log

- `docker-compose.yml`, `mc-fabric`: `JVM_XX_OPTS: "-Xlog:gc:file=/data/logs/gc.log:time,uptime:filecount=5,filesize=10m"`.
- MSM liest `<MSM_MC_DATA_DIR>/logs/gc.log` fortlaufend (Offset merken; Datei kleiner oder neue Inode → von vorn). Pro Takt höchstens 1 MB lesen; abgeschnittene letzte Zeile bis zum nächsten Takt aufheben.
- Ausgewertet: Zeilen mit `Pause` und `<vor>M-><nach>M(<gesamt>M) <dauer>ms` (G1: Young, Mixed, Full, Remark, Cleanup). Andere Zeilen werden ignoriert.
- Kennzahlen pro Takt: **Heap nach letzter GC**, **Gesamt-Heap**, **längste GC-Pause** im Takt, Anzahl Pausen.

### Speicherung

- Zeitreihen (bestehende Tabellen, 48 h roh / 30 Tage Minutenwerte): `jvm.thread.server.cpu`, `jvm.cpu.total`, `jvm.heap.after_gc` (Bytes), `jvm.heap.total` (Bytes), `jvm.gc.pause_max` (ms).
- Neue Tabelle `lag_events` (siehe unten), Einträge älter als 30 Tage löscht der bestehende Prune-Lauf.

## Lag-Erkennung

Ein Lag **beginnt**, sobald eins zutrifft:

- MSPT (bestehende TPS-Abfrage, `mc`-Takt) **> 50 ms**,
- eine GC-Pause **> 100 ms**,
- Server-Thread **≥ 95 %** in **zwei** Messungen hintereinander.

Er **endet**, wenn 30 s lang keins mehr zutrifft. Ein Lag = **ein** Eintrag:

| Feld | Inhalt |
|---|---|
| start, end | Zeit |
| trigger | `mspt` / `gc` / `cpu` (erster Auslöser; weitere als Liste) |
| worstMspt, worstTps | schlechtester Wert im Lag |
| maxGcPauseMs | längste Pause |
| players | höchste Spielerzahl im Lag |
| heapAfterGc, heapTotal | im schlimmsten Moment |
| topThreads | Top 5 Thread-Gruppen (Name, % Kern) im Takt mit der höchsten Server-Thread-CPU |
| diagnosis | Kurztext nach Regeln (unten) |

**Diagnose-Regeln** (alle zutreffenden, in dieser Reihenfolge):

1. Server-Thread ≥ 95 % → „Spiellogik überlastet (Server-Thread am Anschlag)".
2. GC-Pause > 100 ms → „Speicherbereinigung hat den Server angehalten", zusätzlich „— Heap fast voll" wenn nach GC > 85 % belegt.
3. MSPT > 50 ms und Server-Thread < 80 % → „Server wartet auf etwas (z. B. Festplatte, Chunk-Laden)".

## UI: Tab „Leistung"

- **Kacheln:** Server-Thread (% Kern), MSPT (grün < 40, gelb < 50, rot ≥ 50 ms), Heap nach GC („5,2 von 12 GiB"), letzte GC-Pause, Java-CPU gesamt („140 % von 8 Kernen").
- **Graphen** (Verlauf vorladen und Lücken wie im Dashboard; Zeitraum 1 h / 6 h / 24 h):
  1. Server-Thread-CPU (%) + MSPT (ms, zweite Achse), Linie bei 50 ms;
  2. Heap nach GC vs. Gesamt-Heap (GiB);
  3. GC-Pausen (ms), Linie bei 100 ms.
  Lag-Zeiträume rot hinterlegt.
- **Live-Tabelle „Java-Threads":** Top 15 Gruppen mit Balken; kurze Erklärung der wichtigen Threads (Server thread = Spielwelt, Netty = Netzwerk, C2ME/Worker = Chunks, GC = Speicher aufräumen).
- **Lag-Protokoll:** neueste zuerst; Uhrzeit, Dauer, Auslöser, schlechteste MSPT, Spieler, Heap, Top-Threads, Diagnose. Klick → Graphen springen auf diesen Zeitraum.

## API

- `GET /api/perf` — aktueller Stand: Kacheln + Thread-Gruppen (Top 15) + Verfügbarkeit (`threads`, `gcLog`: ok/Grund).
- `GET /api/perf/lags?days=7` — Lag-Protokoll (max. 500 Einträge).
- Verlauf über das bestehende `GET /api/history` (neue Serien-Namen); `hours` 1 / 6 / 24.
- Hinter derselben Anmeldung wie die übrigen Routen.

## Fehlerfälle

| Fall | Verhalten |
|---|---|
| Java-Prozess nicht gefunden (Server aus/Neustart) | Kacheln „—", Graph-Lücke; nächster Takt sucht neu |
| `/host/proc` nicht lesbar | Thread-Bereich mit Hinweis aus, Rest läuft |
| GC-Log fehlt | Heap-Bereich zeigt „GC-Log nicht eingeschaltet" + die Compose-Zeile |
| unbekannte / kaputte Log-Zeilen | ignoriert |
| MSM-Neustart während eines Lags | offener Lag wird mit bisherigem Ende gespeichert, kein Eintrag geht verloren |

Ressourcen: pro Takt nur `stat`/`comm` der Java-Threads (Fabric ~100–200), GC-Log höchstens 1 MB pro Takt; MSM bleibt unter 200 MB / 0,5 CPU.

## Tests

- Unit (Testdateien unter `testdata/`): Thread-Auslastung aus zwei `stat`-Ständen; Gruppierung; Java-Prozess über die PID-Kette (inkl. PID-Wiederverwendung über `starttime`); GC-Parser (Young/Mixed/Full/Remark, Rotation, abgeschnittene Zeile); Lag-Erkennung (Start je Auslöser, Zusammenfassen, Ende nach 30 s, Diagnose-Texte); Speicherung + Löschen nach 30 Tagen; API.
- Live in WSL: GC-Log einschalten, Lag erzeugen (z. B. `/tick sprint`, viele Chunks laden), Protokoll/Graphen/Threads prüfen.

## Release-Hinweis

Nach dem Update `mc-fabric` einmal neu erstellen, damit das GC-Log angeht: `docker compose up -d mc-fabric` (ein Neustart des Minecraft-Servers).
