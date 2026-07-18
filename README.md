# MSM — Minecraft Server Management

Leichtgewichtiges, Docker-basiertes Server-Management-Dashboard für den
Homeserver.

**Phase 1 (Sichtbarkeit):** Livedaten aller Container, Host-Metriken,
Minecraft-Status, WAN-Monitoring, Log-Streaming, RCON-Konsole.
**Phase 2 (Kontrolle):** Login (Argon2 + Sessions), Container
Start/Stopp/Restart (Allowlist), Routinen-Scheduler (Cron: RCON-Befehle,
Neustarts mit Spieler-Vorwarnung), Audit-Log jeder Aktion.
**Phase 3 (Mod-Verwaltung):** Modrinth-Update-Checks (SHA-512), Staging mit
Hash-Verifikation, Ein-Klick-Rollback, MC-Versions-Watch.
**Phase 4 (laufend):** Event-Bus mit Discord-Benachrichtigungen.

Go-Backend (ein Binary, eingebettetes Svelte-Frontend, SQLite) + Socket-Proxy,
Deployment per Docker Compose. Ressourcenbudget: < 150 MB RAM gesamt.

## Architektur

```
Browser ──SSE/REST──▶ msm (Go, :8080, LAN-only)
                       │
                       ├──▶ socket-proxy ──▶ /var/run/docker.sock (ro)
                       ├──▶ mc-fabric:25565 (Query, UDP)
                       ├──▶ mc-fabric:25575 (RCON, TCP)
                       ├──▶ /host/proc (ro)  Host-Metriken
                       └──▶ 1.1.1.1 / 9.9.9.9 / Gateway (ICMP)
```

## Voraussetzungen auf dem Minecraft-Server (itzg/minecraft-server)

In der Container-Umgebung von `mc-fabric` müssen gesetzt sein:

| Env-Var | Wert | Zweck |
|---|---|---|
| `ENABLE_QUERY` | `true` | Spielerzahl/-liste, Version, MOTD |
| `ENABLE_RCON` | `true` (Default beim itzg-Image) | Konsole, TPS |
| `RCON_PASSWORD` | starkes Passwort | gleiches Passwort in MSM `.env` |

Für TPS/MSPT muss der Mod [spark](https://spark.lucko.me/) installiert sein
(`spark tps` via RCON). Query- und RCON-Ports **nicht** nach außen freigeben —
nur im Docker-Netz bzw. LAN erreichbar machen.

## Setup

```sh
cp .env.example .env   # Werte anpassen, insbesondere MSM_RCON_PASSWORD

# Login-Hash erzeugen und in .env eintragen ($ muss für Compose als $$
# escaped werden — das erledigt diese Zeile automatisch):
HASH=$(docker compose run --rm --no-deps msm -hash-password 'DEIN_PASSWORT')
sed -i "s|^MSM_ADMIN_PASSWORD_HASH=.*|MSM_ADMIN_PASSWORD_HASH=${HASH//$/\$\$}|" .env

docker compose up -d --build
```

Dashboard: `http://<host-lan-ip>:8080` (Bind-Adresse über `MSM_BIND_ADDR`).

**Sicherheit:**
- Ohne `MSM_ADMIN_PASSWORD_HASH` läuft das Dashboard **ohne Login** — nur für
  Entwicklung akzeptabel, im Log erscheint eine Warnung.
- Nur an die LAN-Adresse binden, keine Port-Weiterleitung im Router.
- Steuerbar sind ausschließlich Container aus `MSM_MANAGED_CONTAINERS`.
- Für TLS den auskommentierten Caddy-Service in der Compose-Datei aktivieren
  (interne CA, `https://<host>:8443`).
- Jede Aktion (RCON, Start/Stopp, Routinen-Änderung) landet im Audit-Log.

### Routinen

Tab „Routinen" im Dashboard. Typen: `rcon` (beliebiger Konsolenbefehl),
`restart` (Container-Neustart), `announce-restart` (Countdown-Warnungen an
die Spieler per `say`, dann Neustart). Cron-Syntax, 5 Felder — Beispiel
täglich 04:30: `30 4 * * *`. Jede Ausführung wird protokolliert, Fehler sind
im UI sichtbar — Routinen scheitern nie still.

Der angekündigte Neustart ist eine Schrittkette (Bedingungen → Warnungen →
`save-all` → Stop → Start → Watchdog) mit Optionen:
- **Überspringen, wenn Spieler online** — Lauf wird sichtbar als
  „übersprungen" protokolliert
- **Auf leeren Server warten (max. bis HH:MM)** — bei Fristablauf wird
  trotzdem neugestartet
- **Gestagte Mod-Updates einspielen** — Stop → Tausch → Start; schlägt der
  Tausch fehl, startet der Server trotzdem wieder (alter Stand)
- **Watchdog (Min.)** — Routine gilt erst als erfolgreich, wenn der Server
  wieder online meldet; Timeout ergibt einen sichtbaren Fehler

### Backups (restic → NAS)

Routine vom Typ „Backup (restic)", Payload = Minecraft-Container. Das
Backup läuft bei **gestopptem Server** — so finden während des Snapshots
garantiert keine Dateiänderungen statt, und der integrierte Start ersetzt
zugleich den nächtlichen Neustart (keine separate Restart-Routine nötig).

Kette bei laufendem Server: Bedingungen (Spieler online?) → Warnungen →
`save-all` → Stop → Snapshot (restic: backup → check → forget, `forget`
nur nach erfolgreichem check) → optional gestagte Mod-Updates → Start →
Watchdog. Der Server wird **immer** wieder gestartet, auch wenn Backup
oder Update-Tausch fehlschlagen. Ist der Server beim Start der Routine
bereits (bewusst) gestoppt, läuft nur der Snapshot — MSM startet nichts,
was jemand absichtlich ausgeschaltet hat.

Ergebnis inkl. restic-Zusammenfassung landet in Historie + Discord
(`backup.ok`/`backup.failed`); bleibt ein erfolgreiches Backup > 26 h aus,
warnt `backup.stale`.

Einmalige Einrichtung:

```sh
# 1. NAS-Automount auf dem Host (fstab, mountet nur bei Zugriff):
#    //NAS-IP/share /mnt/mc-backups cifs credentials=/etc/nas_credentials,uid=1000,gid=1000,vers=3.1.1,noauto,x-systemd.automount,x-systemd.idle-timeout=300,_netdev 0 0
sudo systemctl daemon-reload && sudo systemctl restart remote-fs.target

# 2. RESTIC_PASSWORD und MSM_NAS_PATH in .env setzen (Passwort zusätzlich
#    im Passwort-Manager sichern — ohne Passwort sind Backups unlesbar!)

# 3. Job-Verzeichnis fürs Einzeldatei-Restore anlegen (MSM schreibt hier
#    die Restore-Skripte hinein; Gruppe = MC_GID aus .env):
mkdir -m 775 -p ~/minecraft/fabric_server/.msm-restore

# 4. Backup- und Restore-Container anlegen (NICHT starten — das macht MSM):
docker compose --profile backup up -d --no-start mc-backup mc-restore
```

**Einzeldatei-Restore (Spielerdaten):** Tab „Routinen" →
„Spielerdaten wiederherstellen": Spieler-UUID eingeben →
`playerdata/<UUID>.dat` wird aus dem letzten Snapshot wiederhergestellt.
Der Spieler muss offline sein; die aktuelle Datei wird vorher als
`.pre-restore-<Zeitstempel>` gesichert (nie gelöscht). Ersetzt
`restore_player_inventory.sh`.

Das restic-Repo legt der erste Lauf automatisch an. Das alte
Nachtbackup-Skript läuft parallel weiter, bis der erste Restore-Test aus
dem restic-Repo gelungen ist (Migrationsregel: nie weniger Sicherung als
vorher).

### Discord-Benachrichtigungen

Ereignisse (Routine ok/fehlgeschlagen, Mod-Updates eingespielt, Rollback,
neue MC-Version/Umstiegsbereitschaft) laufen über einen internen Event-Bus
und werden als Discord-Embeds zugestellt. Einrichtung: im Discord-Channel
unter Einstellungen → Integrationen → Webhooks eine URL erzeugen und als
`MSM_DISCORD_WEBHOOK_URL` in die `.env` eintragen (URL geheim halten!).
Mehrere Webhooks mit Event-Filtern: `MSM_DISCORD_WEBHOOKS` als JSON-Liste,
Details in [.env.example](.env.example). Zustellfehler werden geloggt und
mit Backoff bis zu 3× versucht; ohne konfigurierten Webhook ist der
Notifier komplett inaktiv.

### mc-fabric ist Teil des Stacks

Der Minecraft-Server ist als Service `mc-fabric` in der Compose-Datei
definiert (Version über `MC_VERSION` in `.env` gepinnt, Daten-Pfad über
`MC_DATA_PATH`). Damit entfällt jedes manuelle `docker network connect`.

**Wichtig:** `docker compose down` stoppt auch den Minecraft-Server!
Für MSM-Updates gezielt `docker compose up -d --build msm` verwenden.

Migration von einem bestehenden `docker run`-Container: alten Container
stoppen und entfernen (`docker stop mc-fabric && docker rm mc-fabric`),
dann `docker compose up -d` — die Welt liegt im Bind-Mount und bleibt
unangetastet. Ein eventuell vorhandenes Boot-/Autostart-Skript danach
deaktivieren (`restart: unless-stopped` übernimmt).

### ICMP-Hinweis

Das WAN-Monitoring pingt per ICMP über unprivilegierte Ping-Sockets — die
Compose-Datei setzt dafür das namespaced Sysctl `net.ipv4.ping_group_range`
im Container. Keine Capability nötig (`CAP_NET_RAW` kollidiert auf manchen
Hosts mit AppArmor).

### Socket-Proxy

`wollomatic/socket-proxy` lässt nur das Nötigste durch: lesend
Container-Liste/Stats/Logs, schreibend ausschließlich
`start|stop|restart`. `DOCKER_GID` in `.env` muss der docker-Gruppe des
Hosts entsprechen: `getent group docker | cut -d: -f3`.

## Entwicklung

```sh
# Frontend bauen (wird per go:embed ins Binary eingebettet)
cd web && npm install && npm run build && cd ..

# Backend bauen und testen
go build ./...
go test ./...

# Lokal ohne Docker/Minecraft starten (Fake-Daten):
go run ./cmd/msm -mock -addr 127.0.0.1:8080
```

Frontend-Entwicklung mit Hot-Reload: `cd web && npm run dev` (proxied `/api`
auf `localhost:8080`, dort das Backend im Mock-Modus laufen lassen).

## API

| Endpoint | Beschreibung |
|---|---|
| `GET /api/snapshot` | Momentaufnahme: Container, Stats, Host, MC, WAN |
| `GET /api/containers` | Container-Liste |
| `GET /api/history?series=host.cpu&hours=24` | Zeitreihe (roh ≤ 48 h, Minutenmittel ≤ 30 d) |
| `GET /api/stream/stats` | SSE: Events `snapshot`, `container`, `host`, `mc`, `wan` |
| `GET /api/stream/logs?container=mc-fabric&tail=200` | SSE-Log-Stream |
| `POST /api/rcon` | `{"command":"list"}` → `{"response":"..."}` |
| `GET /healthz` | Health-Check |

## Konfiguration

Alle Optionen als Env-Vars, siehe [.env.example](.env.example) und den
Kopfkommentar in [cmd/msm/main.go](cmd/msm/main.go).

## Roadmap

Konzept und Fahrplan (Phasen 1–6) liegen in der privaten Knowledgebase.
Phase 2: Start/Stopp/Restart, Scheduler, Login. Phase 3: Mod-Updates via
Modrinth. Phase 4: Backups (restic), Discord-Benachrichtigungen.
