#!/bin/sh
# MSM Host-Watcher: führt einen von MSM angeforderten MC-Versionssprung aus.
# Einzige Fähigkeiten: MC_VERSION in der .env setzen + mc-fabric neu
# erstellen. MSM hat die Kette vorher abgesichert (Backup, Mods, Stopp).
SIGNAL_FILE="${MSM_SIGNAL_FILE:?nicht gesetzt — kommt aus dem systemd-Unit (deploy/host-watcher/install.sh)}"
REPO_DIR="${MSM_REPO_DIR:?nicht gesetzt — kommt aus dem systemd-Unit (deploy/host-watcher/install.sh)}"

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

# Rückmeldung an MSM: scheitert der Neubau, liest MSM diese Datei und holt
# den alten Stand zurück (Mods + Version), statt bis zum Timeout zu warten
FAILED_FILE="$(dirname "$SIGNAL_FILE")/upgrade.failed"
fail() {
  logger -t msm-host-watcher "Upgrade fehlgeschlagen: $1"
  printf '%s
' "$1" > "$FAILED_FILE"
  chmod 644 "$FAILED_FILE"
  exit 1
}

cd "$REPO_DIR" || fail "Repo-Verzeichnis fehlt: $REPO_DIR"

OWNER=$(stat -c '%U:%G' .env)
OLD_LINE=$(grep -m1 '^MC_VERSION=' .env) || fail "kein MC_VERSION-Eintrag in $REPO_DIR/.env"
sed -i "s|^MC_VERSION=.*|MC_VERSION=$VERSION|" .env
chown "$OWNER" .env
logger -t msm-host-watcher "MC_VERSION=$VERSION gesetzt, erstelle mc-fabric neu"

# Image vorher aktualisieren: neuere Minecraft-Versionen verlangen oft ein
# neueres Java (26.2 braucht Java 25). Ein veraltetes Image startet dann gar
# nicht — "UnsupportedClassVersionError" in einer Neustart-Schleife.
# Fehlschlag ist nicht fatal (z. B. kein Netz): dann läuft der Versuch mit
# dem vorhandenen Image weiter, MSM erkennt das Problem im Watchdog.
# timeout: ein hängender Pull blockierte sonst auch den Rücksprung (der
# Watcher arbeitet Anforderungen nacheinander ab)
timeout 600 docker compose pull mc-fabric >>/var/log/msm-upgrade.log 2>&1 \
  && logger -t msm-host-watcher "Image für mc-fabric aktualisiert" \
  || logger -t msm-host-watcher "WARNUNG: docker compose pull fehlgeschlagen — fahre mit vorhandenem Image fort"

# nur mc-fabric — MSM und der Rest des Stacks bleiben unangetastet
if ! docker compose up -d mc-fabric >>/var/log/msm-upgrade.log 2>&1; then
  # .env zurück auf den alten Stand: sonst stünde dort eine Version, die nie
  # lief, und das nächste compose up würde die Welt ungewollt anheben
  sed -i "s|^MC_VERSION=.*|$OLD_LINE|" .env
  chown "$OWNER" .env
  fail "docker compose up -d mc-fabric mit $VERSION gescheitert (Details: /var/log/msm-upgrade.log) — .env zurückgesetzt auf $OLD_LINE"
fi
logger -t msm-host-watcher "mc-fabric mit Version $VERSION neu erstellt"
