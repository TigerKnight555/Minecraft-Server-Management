# MSM — Minecraft Server Management

Web-Dashboard zur Verwaltung eines selbst gehosteten Minecraft-Servers auf
Docker-Basis ([itzg/minecraft-server](https://github.com/itzg/docker-minecraft-server)).
Monitoring, Backups, Mod-Updates, Minecraft-Versionswechsel und
Discord-Benachrichtigungen laufen über eine Weboberfläche statt über SSH
und Einzelskripte.

## Hintergrund

Ich betreibe einen Minecraft-Server für Freunde auf einem Homeserver. Die
Verwaltung bestand ursprünglich aus mehreren Bash-Skripten und Cron-Jobs:
ein nächtlicher Host-Reboot, rsync-Backups aufs NAS, Mod-Updates von Hand
über die Modrinth-Webseite. Das hatte drei Probleme:

1. **Keine Sichtbarkeit.** Ob der Server läuft, wie viel RAM er braucht oder
   warum er nachts weg war, ließ sich nur per SSH herausfinden.
2. **Riskante Backups.** Das Skript löschte das alte Vollbackup, bevor das
   neue geschrieben war. Ein Ausfall zum falschen Zeitpunkt hätte die
   Sicherung gekostet.
3. **Handarbeit.** Mod-Updates bedeuteten: jede Mod einzeln prüfen,
   herunterladen, per scp auf den Server kopieren, neu starten, testen.

Vorhandene Panels wie Pterodactyl oder Crafty waren für den Zweck zu
schwergewichtig, passten nicht zum bestehenden Docker-Setup und decken die
Minecraft-spezifischen Abläufe (Modrinth-Abgleich, konsistente Backups der
Welt, Verteilung der Client-Mods an die Spieler) nicht ab. MSM ersetzt die
Skripte durch ein einzelnes Go-Binary mit eingebauter Weboberfläche; der
gesamte Stack benötigt unter 150 MB RAM.

## Entwicklung mit Claude

Dieses Projekt wurde zusammen mit **Claude** (Anthropic) entwickelt.
Konzept, Anforderungen und Entscheidungen stammen von mir; der Großteil des
Codes wurde von Claude geschrieben und von mir im laufenden Betrieb auf dem
Homeserver getestet.

## Funktionen

- **Dashboard:** Live-Status von Server, Spielern, TPS, RAM/CPU,
  Internetqualität, NAS und Containern; Verlaufs-Charts, Live-Logs,
  RCON-Konsole, Audit-Log
- **Routinen:** zeitgesteuerte Abläufe (Cron) für Backups, Host-Reboots,
  angekündigte Neustarts mit Spieler-Countdown und RCON-Befehle; mit
  Bedingungen wie „überspringen, wenn Spieler online" oder „auf leeren
  Server warten"
- **Backups:** restic aufs NAS, ausgeführt bei gestopptem Server (dadurch
  garantiert konsistent), dedupliziert und verschlüsselt; Aufräumen nur
  nach bestandenem Integritäts-Check; Wiederherstellung einzelner
  Spielerstände über ein Dropdown
- **Mod-Verwaltung:** Update-Prüfung gegen Modrinth per Datei-Hash, Staging
  mit Verifikation, Rollback; getrennte Profile für Server-Mods und das
  Client-Paket
- **Minecraft-Versionswechsel:** das Dashboard zeigt an, sobald der
  Fabric-Loader und alle Server-Mods eine neue Version unterstützen; der
  Wechsel selbst ist ein Klick und läuft automatisch ab, inklusive
  vorherigem Backup als Rollback-Punkt
- **Client-Paket:** die Mods für die Spieler werden als ZIP zu Dropbox
  hochgeladen, der Download-Link wird in den Discord-Channel gepostet
- **Discord-Benachrichtigungen:** verständlich formuliert; Erfolgsmeldungen
  nur, wenn Spieler betroffen waren, Fehler und Ausfälle immer
- **Wartungsfenster:** geplante Auszeiten mit Ankündigung (1 h und 5 min
  vorher), automatischem Stopp/Start und stummgeschalteten Alarmen
- **Wächter:** Crash-Erkennung mit Report-Ausschnitt, Alarm bei
  unerwartetem Ausfall, Internet-Überwachung mit Entprellung,
  Schwellwerte für Festplatte und RAM
- **Selbst-Update:** MSM prüft auf neue Release-Tags und aktualisiert sich
  auf Klick aus dem Dashboard

Sicherheitsmodell: MSM selbst hat keine Rechte auf dem Host. Der
Docker-Zugriff läuft über einen Socket-Proxy, der nur Lesen sowie
Start/Stopp/Neustart erlaubt. Die drei privilegierten Aktionen
(Host-Reboot, Versionswechsel, Selbst-Update) übernehmen kleine
systemd-Watcher mit jeweils genau einer Fähigkeit. Das Dashboard ist nur im
LAN erreichbar und durch einen Login geschützt.

## Architektur

```
Browser ──REST/SSE──▶ msm (Go + Svelte, ein Binary, :8080 LAN-only)
                       ├──▶ socket-proxy ──▶ docker.sock (lesen + start/stop/restart)
                       ├──▶ Minecraft (Query/RCON)     ├──▶ /proc (Host-Metriken)
                       ├──▶ Modrinth / Mojang / Discord / Dropbox / GitHub
                       └──▶ Signaldateien ──▶ systemd-Watcher (Reboot/Upgrade/Selbst-Update)
                       + vordefinierte restic-Container für Backup und Restore
```

Go-Backend mit eingebettetem Svelte-Frontend und SQLite, Deployment per
Docker Compose. Integrationen werden im Dashboard konfiguriert
(Tab „Einstellungen"), die Grundeinrichtung übernimmt ein interaktives
Installationsskript.

## Installation

```sh
git clone https://github.com/TigerKnight555/Minecraft-Server-Management.git ~/Minecraft-Server-Management
cd ~/Minecraft-Server-Management
bash install.sh
```

Das Skript fragt alle nötigen Werte ab und erklärt jeweils, woher sie
kommen. Die vollständige Anleitung — inklusive Backup-Einrichtung,
Sicherheitshinweisen und Fehlersuche — steht in **[INSTALL.md](INSTALL.md)**.

## Entwicklung

Branching nach Gitflow: `feature/*`-Branches gehen von `develop` ab,
Releases laufen über `release/*` nach `main` und werden dort mit
Semver-Tags (`vX.Y.Z`) versehen, dringende Korrekturen über `hotfix/*`.
Details zum Entwicklungs-Setup (inklusive Mock-Modus ohne Docker und
Minecraft) in [INSTALL.md](INSTALL.md#entwicklung).

## Unterstützung

Wer das Projekt unterstützen möchte:
[buymeacoffee.com/knvt](https://buymeacoffee.com/knvt)

## Lizenz

MIT mit Commons Clause — siehe [LICENSE](LICENSE). Kurz zusammengefasst:

- Erlaubt: nutzen, verändern, weitergeben — privat und kommerziell (etwa
  für den Server eines Vereins oder Unternehmens)
- Nicht erlaubt: MSM selbst zu verkaufen, also die Software oder einen
  Dienst, dessen Wert im Wesentlichen aus MSM besteht, gegen Entgelt
  anzubieten

Keine Gewährleistung; Betrieb auf eigene Verantwortung.
