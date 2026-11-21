# Basis-Modpack im Client-Paket — Design

Stand: 2026-11-21 · Branch `feature/base-modpack` (von `feature/modrinth-modpack`) · Vault-Original: „MSM – Basis-Modpack"

## Ziel

Das Client-Paket kann auf einem fertigen Modrinth-Modpack aufbauen (z. B. Fabulously Optimized, „FO"). Dessen Mods werden als getestete Kombination übernommen und als Ganzes aktualisiert; der Admin ergänzt eigene Mods und schaltet einzelne Pack-Mods ab. Dropbox-ZIP und eigenes Modrinth-Modpack enthalten alles zusammen — für Spieler ändert sich nichts.

## Nutzer-Entscheidungen

- Nur für das Client-Paket (Server-Mods bleiben einzeln verwaltet).
- Genau ein Basis-Pack gleichzeitig.
- Pack-Updates: anzeigen + Klick „Übernehmen" (nicht automatisch). Einzige Ausnahme: das Ein-Klick-MC-Update stellt das Pack auf die passende Version um.
- Configs: nur ausdrücklich freigegebene; bekannte Spieler-Einstellungen standardmäßig aus, damit Spieler-Configs nicht bei jedem Update überschrieben werden.
- Kommt eine Mod im Pack dazu oder fällt weg, folgt das Client-Paket genau so.

## Ablauf

1. Mods-Tab, Profil „Client-Paket", neuer Bereich „Basis-Modpack": Modrinth nach Modpacks durchsuchen (Projekttyp `modpack`). MSM wählt die neueste Pack-Version für die laufende MC-Version und Loader `fabric`.
2. Vorschau vor dem Übernehmen: Mods, Configs (mit Vorauswahl), was neu dazukommt, welche eigenen Mods das Pack übernimmt, ignorierte Ordner.
3. „Übernehmen": Pack-Mods und freigegebene Configs ins Client-Paket (Prüfsumme; Mods mit Sicherung und Rollback wie bei allen Client-Änderungen, Configs ohne — siehe Abschnitt Configs).
4. Mod-Liste: Pack-Mods mit Herkunft „Basis-Pack" und Schalter an/aus.
5. Neue Pack-Version → Hinweis „FO 6.5.0 verfügbar" mit Änderungsliste → „Übernehmen".
6. Verteilung wie gewohnt (Neustart → 🧩/📦, Dropbox-ZIP, Modrinth-Modpack).

## Regeln beim Übernehmen / Pack-Update

Vergleich alte ↔ neue Pack-Version (Schlüssel: Modrinth-Projekt-ID der Datei, sonst Pfad):

| Fall | Ergebnis |
|---|---|
| Mod neu im Pack | kommt dazu, **an** |
| Mod aus dem Pack entfernt | fliegt raus (auch wenn abgeschaltet) |
| Mod im Pack aktualisiert | wird ersetzt; Abschaltung bleibt |
| eigene Mod ist auch im Pack | Pack übernimmt, eigene Kopie fällt weg (Hinweis in der Vorschau) |
| vom Pack übernommene frühere eigene Mod wird aus dem Pack entfernt | fällt weg; Änderungsliste: „X entfällt — war vorher auch deine eigene Mod, bei Bedarf neu hinzufügen" |
| neue Config-Datei im Pack | aus, markiert „neu" |
| freigegebene Config geändert | wird ersetzt |
| freigegebene Config entfällt im Pack | fliegt raus |

Pack-Mods werden nie einzeln aktualisiert (die Update-Prüfung überspringt sie); nur das Pack als Ganzes.

## Configs

- Quelle: `overrides/` (und `client-overrides/`) der `.mrpack`; `server-overrides/` wird ignoriert.
- Standardmäßig AUS (Spieler-Einstellungen): `options.txt`, `optionsof.txt`, `optionsshaders.txt`, `servers.dat`, `config/sodium-options.json`, `config/iris.properties`, `config/xaero*` , `config/controlling*`, Keybind-Dateien (Dateiname enthält `keybind`), sowie alles außerhalb von `config/`. Alles übrige unter `config/` ist standardmäßig AN.
- Auswahl wird gespeichert (je Pfad an/aus); neue Dateien aus Updates starten AUS.
- Freigegebene Configs liegen im neuen Client-Paket-Ordner `config/`; Dropbox-ZIP nimmt ihn mit, das Modrinth-Modpack als `overrides/config/…`.
- Hinweis im UI: mitgelieferte Configs überschreiben bei jedem Paket-Update die Spieler-Datei (Format-Grenze von `.mrpack`/ZIP).
- Pack-Configs werden ohne Sicherung geschrieben; Quelle ist immer das Pack (Neu laden/Übernehmen stellt sie wieder her). Rollback setzt Configs nicht zurück.

## Bausteine

- **`internal/basepack` (neu):** Modpack-Suche, passende Version finden, `.mrpack` laden (Größenlimit 64 MB), Index lesen (Dateien mit Pfad, Hashes, Download-URLs, `env`; `overrides`), Vergleich alt ↔ neu (Änderungsliste), Config-Vorauswahl. Rein und testbar; Downloads über den Modrinth-Client.
- **Gespeicherter Stand** (`app_state`, JSON unter `basepack.state`): Projekt-ID, Slug, Titel, Version (ID + Nummer), MC-Version, Liste der Pack-Dateien (Pfad, Projekt-ID, Version, sha512), abgeschaltete Projekte, Config-Auswahl, vom Pack übernommene frühere eigene Mods.
- **Anwenden** über `mods.Manager` (Client-Profil): Staging + Sicherung + Rollback; Herkunft je Datei im Inventar (`Entry.Source`: `""` = eigene, `"basepack"`). Abschalten = Datei in die Sicherung, Eintrag bleibt mit `Disabled`; Anschalten = aus der Sicherung zurück, sonst neu laden.
- **Configs** liegen im Ordner `config/` (bzw. am jeweiligen Pfad) des Client-Paket-Verzeichnisses und werden vom Basis-Pack-Dienst direkt geschrieben — nicht als Kategorie des Client-Profils (Sicherung/Staging des Mod-Managers sind flach, Pack-Configs liegen in Unterordnern); die Mod-Liste zeigt sie nicht.
- **API:** `GET /api/basepack` (Status, verfügbares Update), `GET /api/basepack/search?q=`, `GET /api/basepack/preview?project=&version=`, `POST /api/basepack/apply` (Projekt, Version, Config-Auswahl), `POST /api/basepack/toggle` (Projekt, an/aus), `POST /api/basepack/configs` (Auswahl ändern), `DELETE /api/basepack` (entfernen). Gesperrt während eines MC-Updates (409), wie alle Mod-Änderungen.
- **Update-Erkennung:** täglicher Versions-Lauf und „Jetzt prüfen" fragen die neueste Pack-Version für MC + fabric ab; Kachel/Mods-Tab zeigen „verfügbar".
- **MC-Update:** Versions-Watch nennt das Pack („bereit für 26.4" / „noch nicht"); `upgrade.Start` verweigert, wenn ein Basis-Pack gesetzt ist und keine Version für die Zielversion existiert; nach erfolgreichem Sprung stellt der Client-Mod-Schritt das Pack auf die passende Version um (gleiche Regeln, Configs-Auswahl bleibt).
- **Modrinth-Modpack (Kanal):** erlaubte Download-Quellen wie Modrinth-Whitelist: `cdn.modrinth.com`, `github.com`, `raw.githubusercontent.com`, `gitlab.com`; freigegebene Configs als `overrides/`. Dateien ohne Modrinth-Version (z. B. GitHub-Datei aus dem Pack) werden mit den Daten aus dem Pack-Index übernommen.
- **modnews:** ein Pack-Wechsel ergibt EINE Zeile („Fabulously Optimized 6.4.0 → 6.5.0 (3 neu, 1 entfernt, 12 aktualisiert)") statt Einzelzeilen; An/Aus einzelner Pack-Mods wie normale Client-Änderungen.

## Fehlerfälle

- Keine Pack-Version für die laufende MC-Version: Übernehmen nicht möglich, Hinweis.
- Download-/Prüfsummenfehler: Abbruch, nichts verändert.
- Nur-Server-Dateien (`env.client = unsupported`): übersprungen.
- Unbekannte Ordner: ignoriert, in der Vorschau genannt.
- Basis-Pack entfernen: Pack-Dateien (in die Sicherung) und Pack-Configs (ohne Sicherung) raus, eigene Mods bleiben.
- Während MC-Update: alle Basis-Pack-Aktionen 409.
- MC-Update ohne passende Pack-Version: Update verweigert mit Hinweis (abwarten oder Basis-Pack entfernen).

## Tests

- Unit: Index lesen (Ordner, env, overrides), Vergleich (alle Tabellenfälle inkl. Übernahme/Wegfall eigener Mods), Config-Vorauswahl, An/Aus + Wiederherstellen, Upgrade-Sperre, erlaubte Download-Quellen, Modnews-Zeile.
- Live in WSL mit echtem FO: einbinden, Mod abschalten, eigene Mod ergänzen, ältere FO-Version → Update (Änderungsliste, Abschaltungen), MC-Update 1.21.11 → 26.3 mit Pack-Umstellung, ZIP- und Modpack-Inhalt prüfen.

## Nicht enthalten

- Mehrere Basis-Packs, Server-Packs.
- Packs anderer Plattformen (CurseForge).
- „Configs nur beim ersten Installieren" (vom Format nicht unterstützt).
