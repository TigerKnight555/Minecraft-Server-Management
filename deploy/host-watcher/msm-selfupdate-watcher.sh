#!/bin/sh
# MSM Host-Watcher: aktualisiert MSM selbst auf einen Release-Tag.
# Fähigkeiten: git fetch/checkout im Repo + docker compose up --build msm.
# Schlägt der Build fehl, läuft der alte Container unverändert weiter.
SIGNAL_FILE="${MSM_SIGNAL_FILE:?nicht gesetzt — kommt aus dem systemd-Unit (deploy/host-watcher/install.sh)}"
REPO_DIR="${MSM_REPO_DIR:?nicht gesetzt — kommt aus dem systemd-Unit (deploy/host-watcher/install.sh)}"

[ -f "$SIGNAL_FILE" ] || exit 0

if [ -z "$(find "$SIGNAL_FILE" -mmin -10 2>/dev/null)" ]; then
  rm -f "$SIGNAL_FILE"
  logger -t msm-host-watcher "Veraltete selfupdate.request ignoriert und entfernt"
  exit 0
fi

TAG=$(head -n1 "$SIGNAL_FILE" | tr -d '[:space:]')
rm -f "$SIGNAL_FILE"

case "$TAG" in
  ''|*[!0-9A-Za-z._-]*)
    logger -t msm-host-watcher "Selbst-Update abgelehnt: ungültiger Tag '$TAG'"
    exit 1
    ;;
esac

cd "$REPO_DIR" || { logger -t msm-host-watcher "Repo-Verzeichnis fehlt: $REPO_DIR"; exit 1; }

# als Repo-Besitzer fetchen/checkouten, damit Ownership + Credentials stimmen
OWNER=$(stat -c '%U' .git)
runuser -u "$OWNER" -- git fetch --tags origin >>/var/log/msm-selfupdate.log 2>&1
if ! runuser -u "$OWNER" -- git rev-parse -q --verify "refs/tags/$TAG" >/dev/null 2>&1; then
  logger -t msm-host-watcher "Selbst-Update abgelehnt: Tag '$TAG' existiert nicht"
  exit 1
fi
runuser -u "$OWNER" -- git checkout -f "$TAG" >>/var/log/msm-selfupdate.log 2>&1
logger -t msm-host-watcher "MSM-Selbst-Update: Stand $TAG ausgecheckt, baue neu"

docker compose up -d --build msm >>/var/log/msm-selfupdate.log 2>&1
logger -t msm-host-watcher "MSM-Selbst-Update auf $TAG abgeschlossen (exit $?)"
