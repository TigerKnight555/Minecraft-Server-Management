#!/bin/bash
# Installiert den MSM Host-Watcher (Phase 4.5): ein systemd-Path-Trigger,
# der auf die Signaldatei von MSM reagiert und das System neu startet.
# Aufruf: sudo bash install.sh   (im Verzeichnis deploy/host-watcher)
set -euo pipefail

SIGNAL_DIR="/home/knvt/minecraft/msm-host"
HERE="$(cd "$(dirname "$0")" && pwd)"

echo "[1/4] Signal-Verzeichnis $SIGNAL_DIR (Gruppe schreibberechtigt für MSM)"
mkdir -p "$SIGNAL_DIR"
chown knvt:knvt "$SIGNAL_DIR"
chmod 775 "$SIGNAL_DIR"

echo "[2/4] Watcher-Skript nach /usr/local/bin"
install -m 755 "$HERE/msm-reboot-watcher.sh" /usr/local/bin/msm-reboot-watcher.sh

echo "[3/4] systemd-Units"
install -m 644 "$HERE/msm-reboot.path" /etc/systemd/system/msm-reboot.path
install -m 644 "$HERE/msm-reboot.service" /etc/systemd/system/msm-reboot.service
systemctl daemon-reload
systemctl enable --now msm-reboot.path

echo "[4/4] Status"
systemctl status msm-reboot.path --no-pager | head -5

echo
echo "Fertig. Test (löst NACH 10 s einen echten Reboot aus — nur wenn gewollt!):"
echo "  touch $SIGNAL_DIR/reboot.request"
