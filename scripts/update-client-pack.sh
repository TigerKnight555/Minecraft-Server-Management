#!/bin/bash
# Ersetzt den Inhalt des Client-Pakets durch ein neues ZIP.
#
#   bash update-client-pack.sh ~/client_upload.zip
#
# Eigenschaften:
#   - erkennt Wrapper-Ordner im ZIP automatisch (mods/ kann beliebig tief liegen)
#   - sichert den alten Stand vorher als tar.gz
#   - löscht NIE das Verzeichnis client-pack selbst (es ist in MSM
#     bind-gemountet; ein neu angelegter Ordner hätte eine neue Inode und
#     der Container würde weiter auf den alten, gelöschten zeigen)
#   - behält vorhandene .backup-Ordner, verwirft veraltetes .msm-staging
#   - setzt die Gruppen-Schreibrechte, die MSM zum Aktualisieren braucht
set -euo pipefail

# Pfad aus der .env des Repos übernehmen, falls dort konfiguriert
# (Umgebungsvariable hat Vorrang, damit Tests/Sonderfälle möglich bleiben).
REPO_ENV="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/.env"
if [ -z "${MC_CLIENT_PACK_PATH:-}" ] && [ -f "$REPO_ENV" ]; then
  MC_CLIENT_PACK_PATH="$(grep -E '^MC_CLIENT_PACK_PATH=' "$REPO_ENV" | tail -1 | cut -d= -f2- | tr -d '"'"'"'')"
fi

PACK_DIR="${MC_CLIENT_PACK_PATH:-$HOME/minecraft/client-pack}"
BACKUP_DIR="${CLIENT_PACK_BACKUP_DIR:-${PACK_DIR}-backups}"
CATEGORIES=(mods shaderpacks resourcepacks config)

ZIP="${1:-}"
if [ -z "$ZIP" ]; then
  echo "Nutzung: bash $(basename "$0") <pfad/zum/client_pack.zip>" >&2
  exit 1
fi
if [ ! -f "$ZIP" ]; then
  echo "ZIP nicht gefunden: $ZIP" >&2
  exit 1
fi
if [ ! -d "$PACK_DIR" ]; then
  echo "Client-Paket-Verzeichnis fehlt: $PACK_DIR" >&2
  echo "Anlegen mit: mkdir -p $PACK_DIR/{mods,shaderpacks,resourcepacks,config}" >&2
  exit 1
fi
command -v unzip >/dev/null || { echo "unzip ist nicht installiert" >&2; exit 1; }

echo "======================================"
echo " Client-Paket aktualisieren"
echo " Ziel:  $PACK_DIR"
echo " Quelle: $ZIP"
echo "======================================"

# ---------- 1. Entpacken (Temp-Ordner im selben Dateisystem) ----------
TMP="$(dirname "$PACK_DIR")/.client-pack-tmp.$$"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP"

echo "[1/5] Entpacke ZIP..."
unzip -q "$ZIP" -d "$TMP"

# Wrapper-Ebene finden: das Verzeichnis, das mods/ (oder ersatzweise
# shaderpacks/ bzw. resourcepacks/) enthält.
SRC=""
for cat in mods shaderpacks resourcepacks; do
  found="$(find "$TMP" -maxdepth 3 -type d -name "$cat" -print -quit || true)"
  if [ -n "$found" ]; then
    SRC="$(dirname "$found")"
    break
  fi
done
if [ -z "$SRC" ]; then
  echo "Im ZIP wurde kein mods/-, shaderpacks/- oder resourcepacks/-Ordner gefunden." >&2
  echo "Inhalt der obersten Ebene:" >&2
  ls -1 "$TMP" >&2
  exit 1
fi
if [ "$SRC" = "$TMP" ]; then
  echo "      Quellordner im ZIP: (Wurzel)"
else
  echo "      Quellordner im ZIP: ${SRC#"$TMP"/}"
fi

NEW_MODS=$(find "$SRC/mods" -maxdepth 1 -type f 2>/dev/null | wc -l)
if [ "$NEW_MODS" -eq 0 ]; then
  echo "Warnung: das neue Paket enthält keine Mod-Dateien — Abbruch zur Sicherheit." >&2
  exit 1
fi

# ---------- 2. Alten Stand sichern ----------
echo "[2/5] Sichere bisherigen Stand..."
mkdir -p "$BACKUP_DIR"
STAMP="$(date +%Y-%m-%d_%H-%M-%S)"
ARCHIVE="$BACKUP_DIR/client-pack_$STAMP.tar.gz"
tar -czf "$ARCHIVE" -C "$PACK_DIR" .
echo "      $ARCHIVE ($(du -h "$ARCHIVE" | cut -f1))"

# ---------- 3. Inhalt leeren (Verzeichnis selbst bleibt bestehen!) ----------
echo "[3/5] Leere bisherigen Inhalt..."
for cat in "${CATEGORIES[@]}"; do
  dir="$PACK_DIR/$cat"
  [ -d "$dir" ] || continue
  # nur sichtbare Einträge löschen -> .backup bleibt erhalten
  find "$dir" -mindepth 1 -maxdepth 1 ! -name '.*' -exec rm -rf {} +
  # veraltetes Staging verwerfen (bezieht sich auf die alten Dateien)
  rm -rf "$dir/.msm-staging"
done

# ---------- 4. Neuen Stand einsetzen ----------
echo "[4/5] Setze neues Paket ein..."
for cat in "${CATEGORIES[@]}"; do
  [ -d "$SRC/$cat" ] || continue
  mkdir -p "$PACK_DIR/$cat"
  cp -r "$SRC/$cat/." "$PACK_DIR/$cat/"
done

# ---------- 5. Rechte + Zusammenfassung ----------
echo "[5/5] Setze Schreibrechte für MSM..."
chmod -R g+w "$PACK_DIR"

echo
echo "Fertig:"
for cat in "${CATEGORIES[@]}"; do
  dir="$PACK_DIR/$cat"
  [ -d "$dir" ] || continue
  n=$(find "$dir" -mindepth 1 -maxdepth 1 ! -name '.*' | wc -l)
  printf "  %-14s %s Einträge\n" "$cat" "$n"
done
echo
echo "Backup des alten Stands: $ARCHIVE"
echo "Im Dashboard: Tab Mods -> Client-Paket -> \"Updates prüfen\""
