#!/bin/bash
# Installiert die MSM Host-Watcher (systemd-Path-Units). MSM selbst hat keine
# Host-Rechte — diese drei winzigen Watcher führen je genau eine Aktion aus,
# sobald MSM die passende Signaldatei schreibt:
#   reboot.request     -> systemctl reboot
#   upgrade.request    -> MC_VERSION in .env setzen + compose up -d mc-fabric
#   selfupdate.request -> git checkout <tag> + compose up -d --build msm
# Pfade werden automatisch abgeleitet (kein Hardcoding) und in die Units
# geschrieben. Idempotent — bei Updates einfach erneut ausführen.
# Aufruf: sudo bash install.sh   (im Verzeichnis deploy/host-watcher)
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
  echo "Bitte mit sudo ausführen: sudo bash install.sh" >&2
  exit 1
fi

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="$(cd "$HERE/../.." && pwd)"
OWNER="${SUDO_USER:-$(stat -c '%U' "$REPO_DIR")}"
OWNER_HOME="$(eval echo "~$OWNER")"
SIGNAL_DIR="${MSM_HOST_SIGNAL_PATH:-$OWNER_HOME/minecraft/msm-host}"

echo "Repo:              $REPO_DIR"
echo "Besitzer:          $OWNER"
echo "Signal-Verzeichnis: $SIGNAL_DIR"

echo "[1/4] Signal-Verzeichnis (Gruppe schreibberechtigt für MSM)"
mkdir -p "$SIGNAL_DIR"
chown "$OWNER:$OWNER" "$SIGNAL_DIR"
chmod 775 "$SIGNAL_DIR"

echo "[2/4] Watcher-Skripte nach /usr/local/bin"
install -m 755 "$HERE/msm-reboot-watcher.sh" /usr/local/bin/msm-reboot-watcher.sh
install -m 755 "$HERE/msm-upgrade-watcher.sh" /usr/local/bin/msm-upgrade-watcher.sh
install -m 755 "$HERE/msm-selfupdate-watcher.sh" /usr/local/bin/msm-selfupdate-watcher.sh

echo "[3/4] systemd-Units generieren (mit lokalen Pfaden)"
unit() { # $1=name  $2=beschreibung  $3=signaldatei
  cat > "/etc/systemd/system/msm-$1.path" <<EOF
[Unit]
Description=MSM Host-Watcher: wartet auf $3

[Path]
PathExists=$SIGNAL_DIR/$3
Unit=msm-$1.service

[Install]
WantedBy=multi-user.target
EOF
  cat > "/etc/systemd/system/msm-$1.service" <<EOF
[Unit]
Description=MSM Host-Watcher: $2

[Service]
Type=oneshot
Environment=MSM_SIGNAL_FILE=$SIGNAL_DIR/$3
Environment=MSM_REPO_DIR=$REPO_DIR
ExecStart=/usr/local/bin/msm-$1-watcher.sh
EOF
}
unit reboot "führt angeforderten Reboot aus" reboot.request
unit upgrade "führt angeforderten MC-Versionssprung aus" upgrade.request
unit selfupdate "führt angefordertes MSM-Selbst-Update aus" selfupdate.request

systemctl daemon-reload
systemctl enable --now msm-reboot.path msm-upgrade.path msm-selfupdate.path

echo "[4/4] Status"
systemctl is-active msm-reboot.path msm-upgrade.path msm-selfupdate.path

echo
echo "Fertig. In der .env muss stehen: MSM_HOST_SIGNAL_PATH=$SIGNAL_DIR"
