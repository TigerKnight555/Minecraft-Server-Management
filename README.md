# MSM — Minecraft Server Management

Ein leichtgewichtiges Web-Dashboard, das einen selbst gehosteten
Minecraft-Server (Docker, [itzg/minecraft-server](https://github.com/itzg/docker-minecraft-server))
komplett verwaltet: Monitoring, Backups, Mod-Updates, Versions-Sprünge,
Discord-Meldungen — alles per Klick statt per SSH.

> Dieses Projekt wurde gemeinsam mit **Claude** (Anthropic) entwickelt —
> Konzept vom Menschen, Code größtenteils vom Modell, getestet im echten
> Betrieb auf einem Homeserver.

☕ Wenn dir MSM weiterhilft: [buymeacoffee.com/knvt](https://buymeacoffee.com/knvt)

## Warum gibt es das?

Bei mir lief ein Minecraft-Server für Freunde auf dem Homeserver — und die
Verwaltung war über die Zeit ein Zoo aus Bash-Skripten geworden: ein
Cron-Job rebootet nachts den Host, ein Skript rsynct Backups aufs NAS, ein
drittes fummelt Mod-Updates von Modrinth zusammen, und wenn jemand im
Discord fragt „ist der Server down?", heißt es erstmal SSH aufmachen.

Das Grundproblem: **alles funktionierte irgendwie, aber nichts war
sichtbar, nichts war sicher, und alles brauchte mich.** Das Backup-Skript
löschte z. B. das alte Voll-Backup, *bevor* das neue fertig war — ein
Stromausfall zur falschen Zeit und die Sicherung wäre weg gewesen.
Mod-Updates hießen: zehn Modrinth-Seiten durchklicken, JARs runterladen,
per scp rüberschieben, hoffen dass nichts crasht.

Fertige Panels habe ich mir angeschaut (Pterodactyl, Crafty & Co.) — alle
zu schwer für den kleinen Server, keins passte zum bestehenden
Docker-Setup, und die Minecraft-Spezialitäten (Modrinth-Abgleich,
Welt-konsistente Backups, Client-Mod-Verteilung an die Spieler) kann
sowieso keins. Also selbst gebaut: ein einzelnes Go-Binary mit eingebautem
Web-Frontend, unter 150 MB RAM für den ganzen Stack.

Heute läuft die komplette Nacht-Automatik ohne mich: 03:30 Reboot, 04:00
Backup mit Mod-Updates, Meldung in Discord nur, wenn's jemanden betrifft
oder etwas schiefgeht. Und wenn eine neue Minecraft-Version rauskommt,
sagt mir das Dashboard, sobald alle Mods bereit sind — dann ist der
Versions-Sprung ein Klick.

## Was kann es?

**Sehen:**
- Live-Dashboard: Server-Status, Spieler, TPS, RAM/CPU, Internet-Qualität,
  NAS, Container — plus Verlaufs-Charts und Live-Logs
- RCON-Konsole im Browser
- Audit-Log über jede Aktion

**Automatisieren:**
- Routinen per Cron: Backups, Host-Reboots, angekündigte Neustarts mit
  Spieler-Countdown, beliebige RCON-Befehle — mit Bedingungen wie
  „überspringen, wenn Spieler online" oder „auf leeren Server warten"
- **Backups mit restic** aufs NAS: laufen bei gestopptem Server (garantiert
  konsistent), dedupliziert (Nächte dauern Sekunden), verschlüsselt, mit
  Integritäts-Check vor jedem Aufräumen — und Einzeldatei-Restore für
  Spielerstände per Dropdown
- Nächtlicher Host-Reboot mit automatischem Wiederanlauf und
  Soll-Zustand-Abgleich
- Wartungsfenster: ankündigen (Discord: 1 h + 5 min vorher), automatisch
  stoppen/starten, Alarme solange stumm

**Mods & Updates:**
- Mod-Verwaltung über Modrinth: Update-Check per Datei-Hash, Staging mit
  Verifikation, Ein-Klick-Rollback — getrennte Profile für Server und
  Client-Paket
- **MC-Versions-Update per Klick**: Button erscheint, sobald Loader + alle
  Server-Mods die neue Version unterstützen; Ablauf komplett automatisch
  inkl. Pflicht-Backup als Rollback-Punkt
- Client-Mod-Paket als ZIP zu Dropbox, Download-Link automatisch in den
  Discord-Channel
- **MSM aktualisiert sich selbst** aus dem Dashboard, sobald hier ein neuer
  Release-Tag erscheint

**Bescheid wissen:**
- Discord-Meldungen, bewusst spielertauglich formuliert („✅ Server ist
  wieder online!") — Technik-Details stecken im Detail-Feld
- Stille-Regel: Erfolgs-Meldungen nur, wenn Spieler betroffen waren; Fehler
  und Alarme immer
- Wächter für Crashes (mit Report-Ausschnitt), unerwartete Ausfälle,
  Internet-Störungen (entprellt) und volle Platte/RAM

**Sicherheit als Prinzip:** MSM selbst hat keine Host-Rechte. Docker läuft
über einen Socket-Proxy mit minimaler Allowlist, und die drei privilegierten
Aktionen (Host-Reboot, MC-Versionswechsel, Selbst-Update) erledigen winzige
systemd-Watcher mit je genau einer Fähigkeit. Dashboard ist LAN-only mit
Login, ersetzte Dateien werden nie gelöscht.

## Architektur (Kurzfassung)

```
Browser ──REST/SSE──▶ msm (Go + Svelte, ein Binary, :8080 LAN-only)
                       ├──▶ socket-proxy ──▶ docker.sock (nur lesen + start/stop/restart)
                       ├──▶ Minecraft (Query/RCON)  ├──▶ /proc (Host-Metriken)
                       ├──▶ Modrinth / Mojang / Discord / Dropbox / GitHub
                       └──▶ Signaldateien ──▶ systemd-Watcher (Reboot/Upgrade/Selbst-Update)
                       + vordefinierte restic-Container für Backup & Restore
```

Go-Backend, eingebettetes Svelte-Frontend, SQLite — Deployment per Docker
Compose. Konfiguration übers Dashboard (Tab „Einstellungen"), Setup-Werte
über ein interaktives Installationsskript.

## Installation

```sh
git clone https://github.com/TigerKnight555/Minecraft-Server-Management.git ~/Minecraft-Server-Management
cd ~/Minecraft-Server-Management
bash install.sh
```

Das Skript fragt alles Nötige ab und erklärt zu jedem Wert, woher er kommt.
Ausführliche Anleitung, Backups-Einrichtung, Sicherheit, Fehlersuche:
**[INSTALL.md](INSTALL.md)**

## Mitmachen / Entwicklung

Gitflow: `feature/*`-Branches von `develop`, Releases über `release/*` nach
`main` mit semver-Tag (`vX.Y.Z`), Notfall-Fixes über `hotfix/*`.
Entwicklungs-Setup (inkl. Mock-Modus ohne Docker/Minecraft) steht in
[INSTALL.md](INSTALL.md#entwicklung).

## Lizenz

**MIT mit Commons Clause** — siehe [LICENSE](LICENSE). Auf Deutsch:

- ✅ frei nutzen, verändern, weitergeben — privat **und** kommerziell
  (z. B. für den Server deiner Firma oder Community)
- ❌ nicht erlaubt ist, MSM selbst zu **verkaufen** — also die Software oder
  einen Dienst, dessen Wert im Wesentlichen aus MSM besteht, gegen Geld
  anzubieten (verkauftes Hosting-Panel, bezahltes „MSM as a Service" o. ä.)

Keine Gewährleistung — Betrieb auf eigene Verantwortung. Backups testen!
