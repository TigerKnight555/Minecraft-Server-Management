# Modrinth-Modpack automatisch veröffentlichen — Design

Stand: 2026-11-15 · Branch `feature/modrinth-modpack` · Vault-Original: „MSM – Modrinth-Modpack"

## Ziel

Spieler bekommen Client-Mod-Änderungen ohne Neu-Import: MSM veröffentlicht das Client-Paket bei jeder Änderung als neue Version eines eigenen Modrinth-Modpacks. In der Modrinth App (von den Spielern genutzt) genügt ein Klick auf „Update". Das Dropbox-ZIP bleibt parallel bestehen.

## Nutzer-Entscheidungen

- Launcher der Spieler: Modrinth App.
- Das Modpack-Projekt legt der Nutzer einmal selbst auf modrinth.com an (gibt es noch nicht); MSM lädt danach nur Versionen hoch.
- Dropbox und Modrinth laufen parallel, jeder Kanal einzeln abschaltbar.
- Keine Mods von außerhalb Modrinth im Client-Paket (trotzdem abgesichert, siehe Fehlerfälle).
- Eigene Paket-Versionsnummer, unabhängig von MC- und Loader-Version; MC- und Loader-Version stehen im Versionsnamen.

## Ablauf

Auslöser sind dieselben wie für das Dropbox-ZIP — `publishPack` in `cmd/msm/main.go` bleibt der einzige Versandweg:
1. Neustart nach einer Client-Änderung (`internal/modnews.AfterRestart`)
2. Ende eines Ein-Klick-MC-Updates (`upgrade.Orchestrator.Publish`)
3. Knopf im Mods-Tab

`publishPack` versendet nacheinander über jeden eingerichteten Kanal (Dropbox, Modrinth). Kanäle sind unabhängig: Ein Fehler des einen verhindert den anderen nicht.

## Bausteine

### `internal/mrpack` (neu)
- `Build(files []File, meta Meta) ([]byte, error)` — erzeugt die `.mrpack` (ZIP, Wurzel `modrinth.index.json`, `formatVersion: 1`, `game: minecraft`).
- `File`: Pfad im Instanzordner (`mods/…`, `resourcepacks/…`, `shaderpacks/…`), `sha1`, `sha512`, `fileSize`, Download-URL. Nur Dateien mit Modrinth-URL (`https://cdn.modrinth.com/…`) kommen in `files`.
- `Meta`: `versionId` (Paketversion), `name`, `summary`, `dependencies` = `{"minecraft": <MC>, "fabric-loader": <Loader>}`.
- Keine `overrides` (es gibt keine Fremddateien und keine Configs im Client-Paket).
- Pfade ohne `..`, ohne führenden `/` (Spec-Pflicht).

### `internal/modrinth` (erweitert)
- `VersionsByHash` liefert bereits die Datei-Infos (URL, `sha1`, `sha512`, Größe) — `File.Hashes` um `SHA1` ergänzen.
- `CreateVersion(ctx, token, req, filename, data)` — `POST /version` als `multipart/form-data`: Teil `data` (JSON: `name`, `version_number`, `changelog`, `dependencies: []`, `game_versions: [MC]`, `version_type: release`, `loaders: ["fabric"]`, `featured: true`, `project_id`, `file_parts: ["file"]`, `primary_file: "file"`) + Teil `file` (`application/x-modrinth-modpack+zip`). Header `Authorization: <token>`.
- `ProjectVersions(ctx, token, project)` — alle Versionen des Projekts (auch eines nicht freigegebenen, daher mit Token) für die höchste vorhandene Versionsnummer.
- `ProjectWithToken(ctx, token, project)` — Typ, Status (`approved`/`draft`/`processing`/…), ID.
- `CurrentUser(ctx, token)` — Token-Prüfung für „Verbindung prüfen".

### `internal/modpack` (neu) — Kanal Modrinth
- `Publisher.Publish(ctx) (Result, error)`:
  1. Client-Dateien hashen (wie das Mod-Inventar), Modrinth per `VersionsByHash` auflösen.
  2. Loader-Version aus dem Dateinamen der Server-JAR im MC-Datenordner lesen (`fabric-server-mc.<MC>-loader.<L>-launcher.<X>.jar`); MC-Version vom Server-Status.
  3. Nächste Paketversion bestimmen (siehe Versionsnummer), mit der höchsten vorhandenen Version auf Modrinth abgleichen.
  4. `.mrpack` bauen, `CreateVersion` hochladen.
  5. Ergebnis: Versionsnummer, Projekt-URL, ob Projekt öffentlich ist, weggelassene Dateien.
- Single-flight wie `dropbox.Publish`.

### Versionsnummer `MAJOR.MINOR.PATCH`
Zuletzt veröffentlichter Stand in `app_state` (`modpack.version`, `modpack.mc`, `modpack.loader`, `modpack.files` = sortierte Liste der Projekt-IDs + Dateinamen).

| Änderung gegenüber letzter Veröffentlichung | Sprung |
|---|---|
| andere MC-Version | MAJOR (`1.5.0` → `2.0.0`) |
| andere Loader-Version, oder Projekte dazu/weg | MINOR (`1.4.3` → `1.5.0`) |
| nur andere Dateiversionen derselben Projekte | PATCH (`1.4.2` → `1.4.3`) |

- Startwert im Einstellungen-Tab (Standard `1.0.0`; die erste Veröffentlichung nutzt ihn unverändert).
- Ist auf Modrinth eine höhere Version vorhanden (z. B. von Hand hochgeladen), wird von dieser aus gezählt.
- Versionsname: `1.5.0 (MC 26.3 · Fabric 0.20.1)`.
- Changelog: aus der Mod-Neuigkeiten-Liste (neu / entfernt / aktualisiert), Loader-Wechsel ausdrücklich („Fabric Loader 0.20.1 → 0.21.0"), weggelassene Dateien als „nicht im Modpack enthalten".

### Einstellungen (`internal/settings`, Einstellungen-Tab)
- Neue Schlüssel `settings.modrinth.project`, `settings.modrinth.token`, `settings.modrinth.startversion`; `.env`-Rückfall `MSM_MODRINTH_PROJECT`, `MSM_MODRINTH_TOKEN`.
- Anzeige wie Dropbox (Klartext, Quelle Dashboard/.env), Status „einsatzbereit" wenn Projekt + Token gesetzt.
- Knopf „Verbindung prüfen": Token gültig (`CurrentUser`), Projekt existiert, Typ `modpack`, Status (öffentlich / wartet auf Freigabe / Entwurf).
- Aufklappbare Anleitung: Projekt anlegen (Typ Modpack) → Personal Access Token mit *Create versions* + *Read projects* → in MSM eintragen + prüfen → „Modpack jetzt veröffentlichen" → auf Modrinth „Submit for review".

### Mods-Tab
- Knopf „Modpack veröffentlichen (Modrinth)" neben dem Dropbox-Knopf, nur sichtbar wenn eingerichtet.
- Hinweis auf weggelassene Dateien, falls die letzte Veröffentlichung welche hatte.

### `internal/modnews` (angepasst)
- „Paket veraltet" und Fingerabdruck je Kanal (`mods.clientpack.dirty.<kanal>`, `mods.clientpack.fingerprint.<kanal>`); bestehende Schlüssel gelten als Dropbox-Stand (Migration ohne Neu-Versand).
- `publishPack` meldet je Kanal Erfolg; nur erfolgreiche Kanäle werden als aktuell markiert.

### Discord
- Eine gemeinsame 📦-Meldung: „In der Modrinth App beim Modpack auf *Update* klicken" + Dropbox-Link als Ausweich. Modrinth wird nur erwähnt, wenn das Projekt öffentlich ist und der Upload gelang.
- Nach einem MAJOR-Sprung zusätzlich der Hinweis, falls die Modrinth App den Versionswechsel nicht selbst anbietet (Verhalten wird im Live-Test geklärt): „In der Modrinth App: Version ändern → neueste wählen".
- Fehler eines Kanals: eigene Admin-Meldung mit Klartext-Ursache.

## Fehlerfälle

- Modrinth nicht erreichbar / 5xx: Kanal bleibt „veraltet", Nachholen beim nächsten Neustart oder Knopf; Admin-Meldung; Spieler-Meldung nennt nur Dropbox.
- Token ungültig / fehlendes Recht: Klartext („Token fehlt *Create versions*"), Einstellungen zeigen ✗.
- Projekt nicht freigegeben: Version wird hochgeladen, Spieler-Meldung erwähnt Modrinth nicht.
- Versionsnummer vergeben (400): höchste vorhandene Version neu lesen, einmal weiterzählen und erneut versuchen.
- Datei nicht auf Modrinth: weglassen, im Changelog und Mods-Tab nennen. Sind alle Dateien unbekannt: kein Upload, Admin-Meldung.
- Gleichzeitige Auslöser: nur ein Versand gleichzeitig (409 für den Knopf).
- Während eines MC-Updates: kein Versand über modnews (wie bisher); das Update verschickt am Ende selbst.
- Server-JAR nicht gefunden: Loader-Version aus dem letzten veröffentlichten Stand; ohne diesen kein Upload, Admin-Meldung.

## Tests

- `internal/mrpack`: Index-JSON (Felder, Pfade, Hashes, Abhängigkeiten), ZIP-Aufbau, Pfad-Validierung.
- Versionsnummer: alle Tabellenfälle, Startwert, höhere Version auf Modrinth.
- Loader-Erkennung aus JAR-Dateinamen (inkl. kein Treffer).
- `internal/modrinth`: `CreateVersion` gegen httptest (Multipart-Felder, `Authorization`-Header, Fehlercodes 400/401).
- `publishPack`: Kanäle unabhängig, Stand je Kanal, Single-flight.
- Live in WSL mit dem echten, noch nicht freigegebenen Projekt: Upload, Nummern, Changelog, Fehlerfälle (falsches Token, Modrinth-Kanal aus). Nutzer prüft in der Modrinth App Installation, Update-Knopf und Verhalten beim MC-Versionswechsel.

## Nicht enthalten

- Automatisches Anlegen des Modrinth-Projekts oder „Submit for review".
- Configs/Overrides im Modpack.
- Andere Launcher-Formate (CurseForge, packwiz).
