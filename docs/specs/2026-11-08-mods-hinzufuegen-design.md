# MSM – Mods hinzufügen & entfernen (Design)

Erweiterung der [[MSM – Mod-Verwaltung]]: Mods im Dashboard zum Server- und/oder Client-Profil hinzufügen und wieder entfernen. Design mit dem Nutzer abgestimmt am 2026-11-08 (alle Abschnitte freigegeben). Repo-Kopie: `docs/specs/2026-11-08-mods-hinzufuegen-design.md`.

## Ziel

Heute kann der Mods-Tab nur vorhandene Mods aktualisieren (prüfen → stagen → anwenden → Rollback). Neue Mods müssen von Hand ins Dateisystem. Ziel: Hinzufügen und Entfernen komplett im Dashboard, sicher (Hash-Prüfung, Sicherung, Rollback) und mit Discord-Info an die Spieler.

## Entscheidungen

| # | Frage | Entscheidung |
|---|---|---|
| 1 | Quelle | Modrinth-Suche als Standard **und** Datei-Upload als Notlösung |
| 2 | Zielprofil | Vorschlag aus Modrinth (`server_side`/`client_side`), Nutzer bestätigt per Haken; Mod für beide Seiten mit einem Klick in beide Profile |
| 3 | Wann aktiv (Server) | Vormerken (Staging); aktiv beim nächsten Neustart durch Routine mit „Vorgemerkte Updates anwenden" oder per Button „Anwenden + Neustart" (mit 5-min-Vorwarnung) |
| 3b | Client-Profil | Änderung sofort im Paket (vormerken + direkt anwenden, gleiche Sicherung) |
| 4 | Entfernen | Ja, mit Sicherheitsnetz: vorgemerkt (Server) bzw. sofort (Client), Datei wandert nach `.backup`, Rollback möglich, Warnung bei Abhängigkeiten |
| 5 | Abhängigkeiten | Pflicht-Abhängigkeiten werden im Dialog angezeigt und automatisch mit hinzugefügt |
| 6 | Discord | Nach dem Neustart mit Mod-Änderungen: Meldung mit Liste der neuen/entfernten Mods + automatisch neues Client-Paket (wenn Client-Profil geändert) |

## Abläufe

**Hinzufügen per Suche**
1. Mods-Tab → Bereich „Mod hinzufügen" → Suchfeld. Treffer nur mit Build für die aktuelle MC-Version + Fabric (Icon, Kurzbeschreibung, Downloads).
2. Klick → Dialog: Haken „Server"/„Client" nach Modrinth vorgesetzt, änderbar. „Wird mit installiert: …" (fehlende Pflicht-Abhängigkeiten). Warnung bei von Modrinth gemeldeter Unverträglichkeit mit installiertem Mod.
3. Hinweis, **wann der Mod aktiv wird**: „Wird aktiv: heute 04:00 (Routine ‚…')". Hat keine Routine die Option „Vorgemerkte Updates anwenden" an: deutliche Warnung „Keine Routine übernimmt Vorgemerktes — ‚Anwenden + Neustart' nutzen oder Option in einer Routine aktivieren."
4. „Hinzufügen" → Download, SHA-512-Prüfung → Server: vorgemerkt (Badge „neu · vorgemerkt"); Client: sofort im Paket.

**Hinzufügen per Upload**
Datei + Haken Server/Client (+ Kategorie beim Client: `mods`, `shaderpacks`, `resourcepacks`). MSM prüft den Hash bei Modrinth: bekannt → normal verwaltet (bekommt Updates); unbekannt → „unverwaltet".

**Entfernen**
Button pro Zeile. Liegt der Mod in beiden Profilen, wird beidseitiges Entfernen angeboten. Warnung mit Namen, wenn ein anderer installierter Mod ihn braucht (keine Sperre). Server: vorgemerkt („wird entfernt"); Client: sofort.

**Verwerfen**
Button pro vorgemerkter Zeile — macht ein vorgemerktes Hinzufügen/Entfernen rückgängig, bevor es wirkt.

**Anwenden + Neustart (geändert)**
Heute startet der Button den Server sofort ohne Warnung neu. Neu: angekündigter Neustart über die Scheduler-Logik — 5 min Vorwarnung im Spiel, `save-all`, anwenden, Neustart; läuft im Hintergrund.

**Nach dem Neustart mit Mod-Änderungen** (Routine oder Button)
1. Warten, bis der Server wirklich online ist (nur ein nach dem Auftrag erhobener Status zählt — gleiche Regel wie beim MC-Update).
2. Discord-Meldung „🧩 Neue Mods auf dem Server": Neu / Entfernt, pro Mod „auch bei dir nötig" (Client-Mod) oder „nur Server, nichts zu tun".
3. Wenn die Markierung „Client-Paket veraltet" gesetzt ist: automatischer Dropbox-Versand → vorhandene Meldung „📦 Neues Mod-Paket zum Download!". Ohne Dropbox: Zusatz „Das neue Mod-Paket verteilt der Admin."
4. Server kommt nicht hoch → keine Erfolgsmeldung; der Wächter meldet den Fehler. Kein automatischer Rückfall (eigenes Feature).

**Markierung „Client-Paket veraltet"** (app_state): gesetzt bei jeder Änderung am Client-Profil (Hinzufügen, Entfernen, Upload, Anwenden), gelöscht nach erfolgreichem Versand. Reine Client-Änderungen sammeln sich so bis zum nächsten Neustart und gehen in **einem** Paket raus. Button „Client-Paket veröffentlichen" bleibt für „sofort".

**Präzisierungen (Spec-Review):**
- Client-Hinzufügungen/-Entfernungen werden zusätzlich als Liste in app_state gesammelt (`mods.client.changes`, gelöscht mit der Markierung). Meldung 1 nennt **Server-Änderungen dieses Neustarts + gesammelte Client-Änderungen** — Spieler erfahren also auch von reinen Client-Mods.
- Meldung 1 kommt nur, wenn mindestens ein Hinzufügen/Entfernen dabei ist. Reine **Updates** vorhandener Mods lösen sie nicht aus (bisheriges Verhalten bleibt). Ein veraltetes Paket wird trotzdem nach dem Neustart verschickt (Meldung 2).
- Wird das Paket vorher per Button verschickt, ist die Client-Liste damit „verbraucht" und taucht in der nächsten Meldung 1 nicht mehr auf.

**Schutz beim MC-Update**
Das Ein-Klick-Update startet nicht, solange manuell Vorgemerktes existiert („erst anwenden oder verwerfen") — sonst landet ein Mod für die alte Version auf dem neuen Server.

## Technischer Ansatz

Bestehenden Staging-Mechanismus erweitern (verworfen: eigene Änderungsliste in SQLite — doppelte Logik; direkt in den Mod-Ordner — kein Rollback).

- **Staging-Manifest:** `stagingItem` ohne `OldFilename` = Hinzufügen, ohne `NewFilename` = Entfernen. `ApplyStaged` und `restoreSet` (Rollback) behandeln beide Fälle: Entfernen → Datei nach `.backup/<label>/`, Rollback holt sie zurück; Hinzufügen → Rollback verschiebt die neue Datei als `.rolledback` in den Satz. Zusätzlich `ProjectID`, `Name`, `Side` im Item (für Discord-Text und Duplikatprüfung).
- **`internal/modrinth`:** `Search(query, loader, mcVersion)` (`/v2/search`, Facetten `project_type:mod`, `categories:fabric`, `versions:<mc>`), `Project(id)` (`/v2/project/{id}`: Name, Icon, `client_side`/`server_side`), `ProjectVersions(id, loader, mcVersion)` (`/v2/project/{id}/version`). `Version` bekommt `Dependencies` (`project_id`, `version_id`, `dependency_type`: required/incompatible/…).
- **`internal/mods/add.go`:** `Plan(ctx, projectID, mcVersion)` → neuester passender Build, vorgeschlagene Seiten, fehlende Pflicht-Abhängigkeiten (rekursiv, max. 3 Ebenen, installierte/vorgemerkte übersprungen — erkannt per Projekt-ID über `VersionsByHash`), Unverträglichkeiten. `StageAdd`, `StageRemove`, `StageUpload`, `Discard`. Client: vormerken + sofort `ApplyStaged`. `Entries` zeigt vorgemerkte Neuzugänge als eigene Zeilen und Entfernungen als „wird entfernt".
- **`internal/api`:** `GET /api/mods/search?q=`, `GET /api/mods/plan?project=`, `POST /api/mods/add` (`projectId`, `versionId`, `profiles[]`), `POST /api/mods/upload` (multipart, max. 100 MB), `POST /api/mods/remove`, `POST /api/mods/discard`, `GET /api/mods/apply-info`. `POST /api/mods/apply` mit Neustart → angekündigter Neustart (Scheduler) statt Sofort-Neustart.
- **`internal/scheduler`:** angekündigten Neustart für Ad-hoc-Aufruf freigeben; nach Neustart mit angewendeten Mod-Änderungen: Online-Abwarten → Discord-Meldung → ggf. Paket-Versand. Gilt für Routinen-Pfad (`announce-restart` und Backup-Routine) und Button gleichermaßen.
- **`internal/upgrade`:** Vorab-Prüfung „manuell Vorgemerktes vorhanden → Abbruch mit Hinweis".
- **`web/src/lib/Mods.svelte`:** Bereich „Mod hinzufügen" (Suche, Treffer, Upload), Dialog (Haken, Abhängigkeiten, Warnungen, Aktiv-Hinweis), Zeilen-Buttons „Entfernen"/„Verwerfen", Badges „neu · vorgemerkt"/„wird entfernt".
- **Audit:** jede Aktion (`mods.add`, `mods.remove`, `mods.upload`, `mods.discard`) im Audit-Log.

## Fehlerfälle

- Modrinth nicht erreichbar → Meldung im Dialog, nichts vorgemerkt.
- Hash-Abweichung → Abbruch, Datei verworfen.
- Abhängigkeit ohne Build für die aktuelle MC-Version → Hinzufügen gesperrt mit Begründung.
- Mod schon installiert/vorgemerkt (gleiche Projekt-ID) → Hinweis statt Duplikat.
- Upload: nur `.jar` (mods) bzw. `.zip` (shader-/resourcepacks), max. 100 MB, Dateiname bereinigt (kein Pfad, kein `..`, kein führender Punkt), gleichnamige Datei → abgelehnt.
- Entfernen einer Abhängigkeit → Warnung mit Namen, keine Sperre.

## Nicht enthalten

- Shader/Resourcepacks über die Suche (andere Loader-Regeln bei Modrinth) — Upload deckt sie ab.
- Automatischer Rückfall, wenn der Server nach Mod-Änderungen nicht hochkommt (manueller Rollback-Button reicht vorerst).
- Rechte-Problem `.backup`/`.msm-staging` (uid 65532, `0755`) — eigener offener Punkt im [[MSM – Phase-4-Plan]].

## Tests

- **Go-Unit-Tests** (Modrinth-Fake): Suche/Plan inkl. Abhängigkeiten und Rekursions-Stopp; Hinzufügen/Entfernen/Verwerfen → Anwenden → Rollback (Datei wirklich wieder da bzw. weg); Upload-Validierung; Sperre beim MC-Update; Markierung „Paket veraltet" (gesetzt/gelöscht); Discord-Text (Server-/Client-Kennzeichnung).
- **Echter Lauf in der WSL-Testumgebung:** Server-Mod und Client-Mod per Suche; Mod für beide Seiten mit Abhängigkeit; Entfernen; „Anwenden + Neustart" → Vorwarnung, Server läuft mit neuen Mods, beide Discord-Meldungen, Dropbox-Link ok; Rollback; dieselbe Kette über die nächtliche Routine (manuell ausgelöst).

## Verwandt

- [[MSM – Mod-Verwaltung]] · [[MSM – Phase-3-Plan]] · [[MSM – Phase-4-Plan]] · [[MSM – Architektur & Code-Struktur]] · [[MSM – Routinen & Wartungsfenster]]
