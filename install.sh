#!/bin/bash
# MSM-Installation: fragt alle nötigen Werte interaktiv ab (mit Erklärung,
# woher jeder Wert kommt), erkennt so viel wie möglich automatisch, schreibt
# die .env, baut und startet den Stack. Idempotent — bei erneutem Aufruf
# werden vorhandene Werte als Vorgabe angeboten.
#
# Aufruf als normaler User (NICHT root) im Repo-Verzeichnis:  bash install.sh
# Integrationen (Discord, Dropbox, Selbst-Update) werden NICHT hier gesetzt —
# das macht man nach dem Start bequem im Dashboard unter "Einstellungen".
set -euo pipefail
cd "$(dirname "$0")"

bold() { printf '\033[1m%s\033[0m\n' "$*"; }
note() { printf '  \033[2m%s\033[0m\n' "$*"; }
die()  { printf 'FEHLER: %s\n' "$*" >&2; exit 1; }

# ask VAR "Frage" "Default" "Erklärung..."
ask() {
  local var="$1" prompt="$2" def="$3"; shift 3
  echo
  bold "$prompt"
  local line
  for line in "$@"; do note "$line"; done
  local input
  read -r -p "  [$def] > " input
  printf -v "$var" '%s' "${input:-$def}"
}

# set_env KEY VALUE — ersetzt oder ergänzt in .env
set_env() {
  local key="$1" value="$2"
  if grep -q "^$key=" .env; then
    # | als sed-Trenner; Werte mit | kommen hier nicht vor
    sed -i "s|^$key=.*|$key=$value|" .env
  else
    printf '%s=%s\n' "$key" "$value" >> .env
  fi
}

env_or() { grep -s "^$1=" .env | head -n1 | cut -d= -f2- || true; }

# ---------- Vorbedingungen ----------
bold "== MSM-Installation =="
[ "$(id -u)" -ne 0 ] || die "bitte als normaler User ausführen (nicht root/sudo) — sudo wird nur für den Host-Watcher-Schritt gebraucht"
command -v docker >/dev/null || die "Docker fehlt: https://docs.docker.com/engine/install/"
docker compose version >/dev/null 2>&1 || die "Docker Compose v2 fehlt (Plugin 'docker-compose-plugin' installieren)"
docker info >/dev/null 2>&1 || die "kein Zugriff auf Docker — User in die docker-Gruppe: sudo usermod -aG docker $USER && neu einloggen"
command -v openssl >/dev/null || die "openssl fehlt (für Passwort-Generierung): sudo apt install openssl"

[ -f .env ] || cp .env.example .env
echo "OK: Docker $(docker --version | grep -oE '[0-9]+\.[0-9]+' | head -1), .env vorhanden"

# ---------- Fragen ----------
DEF_TZ="$(cat /etc/timezone 2>/dev/null || timedatectl show -p Timezone --value 2>/dev/null || echo Europe/Berlin)"
ask TZ_VAL "Zeitzone (für Routinen-Zeiten, Wartungsfenster)" "$(env_or TZ || true)" \
  "Format: Region/Stadt. Automatisch erkannt vom System."
: "${TZ_VAL:=$DEF_TZ}"
[ -n "$TZ_VAL" ] || TZ_VAL="$DEF_TZ"

ask MC_DATA "Pfad der Minecraft-Serverdaten (Welt, mods/, server.properties)" \
  "$(env_or MC_DATA_PATH || echo "$HOME/minecraft/fabric_server")" \
  "Bestehender Server: das Verzeichnis mit world/ und mods/." \
  "Neuer Server: Wunschpfad — wird angelegt, itzg/minecraft-server richtet alles ein."
mkdir -p "$MC_DATA"

# MC-Version: aus den Server-Logs erraten, wenn möglich
DEF_VER="$(env_or MC_VERSION)"
if [ -z "$DEF_VER" ] && [ -f "$MC_DATA/logs/latest.log" ]; then
  DEF_VER="$(grep -oE 'Starting minecraft server version [0-9][0-9A-Za-z.]*' "$MC_DATA/logs/latest.log" | head -1 | awk '{print $NF}')"
fi
ask MC_VER "Minecraft-Version — EXAKT die laufende Version!" "${DEF_VER:-1.21.11}" \
  "WARNUNG: eine höhere Version upgradet die Welt beim ersten Start UNUMKEHRBAR." \
  "Nachsehen: im Spiel unten links im Menü, oder in $MC_DATA/logs/latest.log" \
  "('Starting minecraft server version …'). Spätere Sprünge macht MSM per Klick."

ask CLIENT_PACK "Pfad des Client-Pakets (Mods/Shader für die Spieler)" \
  "$(env_or MC_CLIENT_PACK_PATH || echo "$HOME/minecraft/client-pack")" \
  "Wird angelegt, falls nicht vorhanden. Befüllen später per scp (siehe README)."
mkdir -p "$CLIENT_PACK"/{mods,shaderpacks,resourcepacks}

DEF_IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
ask BIND "LAN-IP dieses Servers (Dashboard-Adresse — NIEMALS öffentlich!)" \
  "$(env_or MSM_BIND_ADDR || echo "${DEF_IP:-127.0.0.1}")" \
  "Automatisch erkannt. Das Dashboard ist danach unter http://<IP>:8080 erreichbar." \
  "Keine Port-Weiterleitung im Router einrichten — LAN/VPN only."

RCON_PW="$(env_or MSM_RCON_PASSWORD)"
if [ -z "$RCON_PW" ]; then
  RCON_PW="$(openssl rand -base64 24 | tr -d '/+=' | cut -c1-24)"
  echo; bold "RCON-Passwort: automatisch generiert (intern zwischen MSM und Minecraft-Server)."
else
  echo; bold "RCON-Passwort: vorhandenes wird beibehalten."
fi

ask NAS "NAS-Mountpoint für Backups (leer = Backups später einrichten)" \
  "$(env_or MSM_NAS_PATH)" \
  "Ein per fstab gemounteter Pfad (z. B. /mnt/mc-backups). Anleitung für den" \
  "systemd-Automount steht im README, Abschnitt 'Backups'. Kann leer bleiben."

RESTIC_PW="$(env_or RESTIC_PASSWORD)"
if [ -n "$NAS" ] && [ -z "$RESTIC_PW" ]; then
  RESTIC_PW="$(openssl rand -base64 32)"
  echo
  bold "!!! WICHTIG: restic-Backup-Passwort wurde generiert: !!!"
  echo "    $RESTIC_PW"
  bold "!!! JETZT in den Passwort-Manager kopieren — ohne dieses Passwort sind ALLE Backups unlesbar !!!"
  read -r -p "  Gespeichert? [Enter] " _
fi

echo
bold "Dashboard-Passwort (Login für die Weboberfläche)"
note "Frei wählbar; wird nur als Argon2-Hash gespeichert."
DASH_PW=""
while [ -z "$DASH_PW" ]; do
  read -r -s -p "  Passwort > " DASH_PW; echo
done

# ---------- .env schreiben ----------
set_env TZ "$TZ_VAL"
set_env MC_DATA_PATH "$MC_DATA"
set_env MC_VERSION "$MC_VER"
set_env MC_CLIENT_PACK_PATH "$CLIENT_PACK"
set_env MSM_BIND_ADDR "$BIND"
set_env MSM_RCON_PASSWORD "$RCON_PW"
set_env MSM_NAS_PATH "$NAS"
[ -n "$RESTIC_PW" ] && set_env RESTIC_PASSWORD "$RESTIC_PW"
set_env DOCKER_GID "$(getent group docker | cut -d: -f3)"
set_env MC_GID "$(id -g)"
set_env MSM_HOST_SIGNAL_PATH "$HOME/minecraft/msm-host"
set_env MSM_MANAGED_CONTAINERS "$(env_or MSM_MANAGED_CONTAINERS || echo mc-fabric)"
echo; echo "OK: .env geschrieben"

# ---------- Verzeichnisse + Rechte ----------
mkdir -p "$MC_DATA/mods" "$MC_DATA/.msm-restore"
chmod 775 "$MC_DATA/.msm-restore"
chmod -R g+w "$MC_DATA/mods" "$CLIENT_PACK" 2>/dev/null || true
echo "OK: Verzeichnisse angelegt, Gruppenrechte gesetzt"

# ---------- Bauen + Login-Hash ----------
echo; bold "Baue MSM (erster Build dauert ein paar Minuten)…"
docker compose build msm
HASH="$(docker compose run --rm --no-deps msm -hash-password "$DASH_PW")"
set_env MSM_ADMIN_PASSWORD_HASH "${HASH//$/\$\$}"
echo "OK: Login-Hash gesetzt"

# ---------- Host-Watcher ----------
echo
bold "Host-Watcher installieren (sudo nötig — Reboot/MC-Upgrade/Selbst-Update aus dem Dashboard)"
sudo bash deploy/host-watcher/install.sh

# ---------- Starten ----------
echo; bold "Starte den Stack…"
docker compose up -d
if [ -n "$NAS" ]; then
  docker compose --profile backup up -d --no-start mc-backup mc-restore
  echo "OK: Backup-/Restore-Container angelegt (startet MSM bei Bedarf)"
fi

echo
bold "================= FERTIG ================="
echo "  Dashboard:  http://$BIND:8080  (Login mit deinem Dashboard-Passwort)"
echo
echo "  Nächste Schritte — alles im Dashboard:"
echo "   1. Tab 'Einstellungen': Discord-Webhook + Dropbox einrichten"
echo "      (Schritt-für-Schritt-Anleitungen stehen direkt im Tab)"
echo "   2. Tab 'Routinen': Nachtbackup (z. B. 0 4 * * *, Typ Backup) und"
echo "      Host-Reboot (z. B. 30 3 * * *) anlegen"
echo "   3. Tab 'Mods': 'Updates prüfen'"
if [ -z "$NAS" ]; then
  echo
  echo "  Backups sind noch AUS (kein NAS-Pfad). Einrichtung: README, Abschnitt 'Backups',"
  echo "  danach dieses Skript einfach erneut ausführen."
fi
echo "  Doku: README.md im Repo."
