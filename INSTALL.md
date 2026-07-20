# MSM installieren & betreiben

Zurück zur Projekt-Übersicht: [README](README.md)

## Voraussetzungen

- Linux-Host mit **Docker + Docker Compose v2**, dein User in der `docker`-Gruppe
  (`sudo usermod -aG docker $USER`, danach neu einloggen)
- Ein Minecraft-Server auf Basis von [itzg/minecraft-server](https://github.com/itzg/docker-minecraft-server)
  — oder noch keiner: MSM legt ihn beim ersten Start mit an
- Für TPS/MSPT-Anzeige: den Mod [spark](https://spark.lucko.me/) in den mods-Ordner
- Optional: NAS-Freigabe für Backups, Discord-Server, Dropbox-Konto

Im Minecraft-Container müssen `ENABLE_QUERY=true` und `ENABLE_RCON=true` gesetzt
sein — die mitgelieferte Compose-Datei erledigt das. Query-/RCON-Ports niemals
nach außen freigeben.

## Installation (empfohlen: das Skript)

```sh
git clone https://github.com/TigerKnight555/Minecraft-Server-Management.git ~/Minecraft-Server-Management
cd ~/Minecraft-Server-Management
bash install.sh
```

Das Skript fragt alle Werte interaktiv ab (mit Erklärung, woher jeder Wert
kommt), erkennt so viel wie möglich automatisch, generiert Passwörter, baut
den Stack, installiert die Host-Watcher (einziger sudo-Schritt) und startet
alles. Es ist **idempotent**: einfach erneut ausführen, um Werte zu ändern —
vorhandene Einträge sind die Vorgabe.

Danach: Dashboard unter `http://<LAN-IP>:8080` öffnen, mit dem gewählten
Passwort einloggen. **Integrationen (Discord, Dropbox) richtest du im
Dashboard-Tab „Einstellungen" ein — die Schritt-für-Schritt-Anleitungen
stehen direkt dort.**

### Woher kommt welcher Wert?

| Wert | Abgefragt von | Woher nehmen |
|---|---|---|
| Zeitzone (`TZ`) | install.sh | automatisch erkannt (`/etc/timezone`) |
| MC-Datenpfad (`MC_DATA_PATH`) | install.sh | Verzeichnis mit `world/` + `mods/`; bei Neuinstallation Wunschpfad |
| MC-Version (`MC_VERSION`) | install.sh | automatisch aus `logs/latest.log` erraten; sonst im Spiel-Menü unten links. **Exakt angeben — eine höhere Version upgradet die Welt unumkehrbar!** |
| Client-Paket-Pfad | install.sh | Wunschpfad; Ordnerstruktur wird angelegt |
| LAN-IP (`MSM_BIND_ADDR`) | install.sh | automatisch erkannt (`hostname -I`) |
| RCON-Passwort | install.sh | automatisch generiert (nur intern MSM ↔ Minecraft) |
| Dashboard-Passwort | install.sh | frei wählen — der Web-Login |
| NAS-Pfad (`MSM_NAS_PATH`) | install.sh, optional | fstab-Automount-Mountpoint, siehe „Backups"; leer = Backups später |
| restic-Passwort | install.sh (generiert) | **in den Passwort-Manager!** Ohne = Backups unlesbar |
| `DOCKER_GID`, `MC_GID`, Signal-Pfad | install.sh | vollautomatisch |
| Discord-Webhook | Dashboard → Einstellungen | Discord-Channel → ⚙️ → Integrationen → Webhooks |
| Dropbox App-Key/Secret/Refresh-Token | Dashboard → Einstellungen | Anleitung aufklappbar direkt im Tab |
| GitHub-Token (`MSM_GITHUB_TOKEN`) | `.env`, nur bei privatem Fork | github.com → Settings → Developer settings → Tokens |

Manuelle Installation ohne Skript: `.env.example` nach `.env` kopieren und
ausfüllen (jede Variable ist dort kommentiert), dann `docker compose build msm`,
Login-Hash setzen (`docker compose run --rm --no-deps msm -hash-password '…'`,
`$` als `$$` escapen), `sudo bash deploy/host-watcher/install.sh`,
`docker compose up -d`.

## Sicherheit

- Ohne `MSM_ADMIN_PASSWORD_HASH` läuft das Dashboard **ohne Login** — nur für
  Entwicklung; im Log steht eine Warnung.
- Nur an die LAN-Adresse binden, **keine Port-Weiterleitung im Router**.
- MSM hat bewusst keine Host-Rechte: Docker-Zugriff nur über einen
  Socket-Proxy (lesen + start/stop/restart, sonst nichts), privilegierte
  Aktionen (Host-Reboot, MC-Versionswechsel, Selbst-Update) laufen über
  winzige systemd-Watcher mit je genau einer Fähigkeit.
- Steuerbar sind nur Container aus der Allowlist `MSM_MANAGED_CONTAINERS`.
- Für TLS den auskommentierten Caddy-Service in der Compose-Datei aktivieren.
- Jede Aktion landet im Audit-Log.

## Backups einrichten (restic → NAS)

```sh
# 1. NAS-Automount auf dem Host (fstab-Zeile anpassen: IP, Share, Credentials):
#    //NAS-IP/share /mnt/mc-backups cifs credentials=/etc/nas_credentials,uid=1000,gid=1000,vers=3.1.1,noauto,x-systemd.automount,x-systemd.idle-timeout=300,_netdev 0 0
sudo systemctl daemon-reload && sudo systemctl restart remote-fs.target
ls /mnt/mc-backups   # Zugriff löst den Mount aus — muss den Share zeigen

# 2. install.sh erneut ausführen und den NAS-Pfad angeben — es setzt
#    RESTIC_PASSWORD (Passwort-Manager!), MSM_NAS_PATH und legt die
#    Backup-/Restore-Container an.
bash install.sh
```

Dann im Dashboard eine Routine vom Typ **„Backup (restic)"** anlegen
(z. B. `0 4 * * *`, Container `mc-fabric`, Watchdog 10, „auf leeren Server
warten"). Das Backup läuft bei **gestopptem Server** (garantiert konsistent);
der integrierte Neustart ersetzt eine separate Restart-Routine. restic
dedupliziert: nach dem ersten Voll-Backup dauern Nächte nur Sekunden.
Aufbewahrung: 7 täglich / 4 wöchentlich / 6 monatlich, Aufräumen nur nach
erfolgreichem Integritäts-Check.

**Einzeldatei-Restore:** Tab „Routinen" → „Spielerdaten wiederherstellen" →
Spieler im Dropdown wählen → `playerdata/<uuid>.dat` kommt aus dem letzten
Snapshot zurück (Original wird als `.pre-restore-*` gesichert, nie gelöscht).

## Nächtlicher Host-Reboot

Routine vom Typ **„Host-Reboot"** (z. B. `30 3 * * *`). MSM warnt die Spieler,
speichert, stoppt den Server und schreibt eine Signaldatei; der
systemd-Watcher rebootet den Host. Nach dem Boot startet Docker alles, MSM
gleicht den Soll-Zustand ab (bewusst gestoppte Container bleiben aus) und
meldet „wieder online" — nur wenn Spieler betroffen waren.

## MC-Version aktualisieren (Ein-Klick)

Die Dashboard-Kachel „MC-Update" wird grün, sobald der Fabric-Loader und
**alle** Server-Mods die neue Version unterstützen → „Jetzt updaten" macht
den kompletten Sprung: Warnung → Pflicht-Backup (Rollback-Punkt!) →
Server-Mods → Server-Neuerstellung mit neuer Version + frischem Loader →
Client-Mods → neues Mod-Paket für die Spieler (wenn Dropbox eingerichtet).

## MSM aktualisieren

Einstellungen-Tab → „MSM-Version" → „Auf vX.Y.Z aktualisieren", sobald ein
neuer Release-Tag existiert. Das Dashboard baut sich selbst neu (~1–2 min
weg), Minecraft läuft ungestört weiter. Manuell geht immer:
`git pull && docker compose up -d --build msm` — **niemals `docker compose
down`, das stoppt auch den Minecraft-Server!**

## Client-Mods einspielen/aktualisieren

Einzelne Datei: per scp nach `<client-pack>/mods/`. Größere Änderungen als
ZIP hochladen und entpacken; wenn Mods entfernt wurden, den mods-Ordner
vorher beiseitelegen (`mv mods mods.alt-$(date +%F)` — nie löschen). Danach
immer: `chmod -R g+w <client-pack>` und im Mods-Tab „Updates prüfen".
Verteilen an die Spieler: Button „Client-Paket veröffentlichen" (Dropbox-Link
landet in Discord).

## Fehlersuche

```sh
bash diagnose.sh
```

Sammelt alles Relevante (Git-Stand, Konfiguration mit **maskierten**
Secrets, Container-Status, Logs, Backup-Status) — die Ausgabe ist gefahrlos
teilbar, z. B. in einem GitHub-Issue.

Häufige Stolperfallen:

| Symptom | Ursache → Lösung |
|---|---|
| Backup: „empty password is not allowed" | Env wird beim **Anlegen** des Containers eingebacken → `.env` füllen, dann `docker compose --profile backup up -d --no-start --force-recreate mc-backup mc-restore` |
| Routinen feuern zur falschen Stunde | `TZ` fehlt in `.env` (Container rechnet sonst UTC) |
| Login schlägt fehl, Hash verdächtig kurz | `$` im Argon2-Hash muss als `$$` escaped sein (install.sh macht das) |
| `exec read: EOF` in der Historie | RCON-Flakiness, MSM wiederholt automatisch — einzelner Fehler mit folgendem OK ist harmlos |
| Skripte: „bad interpreter" | Repo mit CRLF ausgecheckt — `.gitattributes` erzwingt LF, `git checkout -- '*.sh'` |

## Entwicklung

```sh
cd web && npm install && npm run build && cd ..   # Frontend (go:embed)
go build ./... && go test ./...                   # Backend + ~90 Tests
go run ./cmd/msm -mock -addr 127.0.0.1:8080       # komplett ohne Docker/Minecraft (Fake-Daten)
```

Frontend-Hot-Reload: `cd web && npm run dev` (proxied `/api` auf das
Mock-Backend). Branching: Gitflow — `feature/*` von `develop`, Releases über
`release/*` nach `main` mit semver-Tag (`vX.Y.Z`), Notfälle über `hotfix/*`.
