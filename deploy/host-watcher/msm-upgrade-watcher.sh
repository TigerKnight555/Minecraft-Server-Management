#!/bin/sh
# MSM Host-Watcher: führt einen von MSM angeforderten MC-Versionssprung aus.
# Einzige Fähigkeiten: MC_VERSION in der .env setzen + mc-fabric neu
# erstellen. MSM hat die Kette vorher abgesichert (Backup, Mods, Stopp).
SIGNAL_FILE="${MSM_SIGNAL_FILE:-/home/knvt/minecraft/msm-host/upgrade.request}"
REPO_DIR="${MSM_REPO_DIR:-/home/knvt/Minecraft-Server-Management}"

[ -f "$SIGNAL_FILE" ] || exit 0

# Nur frische Anforderungen (< 10 min) — Schutz vor Wiederholung nach Boot
if [ -z "$(find "$SIGNAL_FILE" -mmin -10 2>/dev/null)" ]; then
  rm -f "$SIGNAL_FILE"
  logger -t msm-host-watcher "Veraltete upgrade.request ignoriert und entfernt"
  exit 0
fi

VERSION=$(head -n1 "$SIGNAL_FILE" | tr -d '[:space:]')
rm -f "$SIGNAL_FILE"

# strenge Validierung — der Wert landet in der .env
case "$VERSION" in
  ''|*[!0-9A-Za-z._-]*)
    logger -t msm-host-watcher "Upgrade abgelehnt: ungültige Version '$VERSION'"
    exit 1
    ;;
esac

cd "$REPO_DIR" || { logger -t msm-host-watcher "Repo-Verzeichnis fehlt: $REPO_DIR"; exit 1; }

OWNER=$(stat -c '%U:%G' .env)
sed -i "s|^MC_VERSION=.*|MC_VERSION=$VERSION|" .env
chown "$OWNER" .env
logger -t msm-host-watcher "MC_VERSION=$VERSION gesetzt, erstelle mc-fabric neu"

# nur mc-fabric — MSM und der Rest des Stacks bleiben unangetastet
docker compose up -d mc-fabric >>/var/log/msm-upgrade.log 2>&1
logger -t msm-host-watcher "mc-fabric mit Version $VERSION neu erstellt (exit $?)"
