#!/bin/sh
# MSM Host-Watcher: führt einen von MSM angeforderten Reboot aus.
# Einzige Berechtigung dieses Mechanismus: systemctl reboot.
# Wird von msm-reboot.path getriggert, sobald die Signaldatei erscheint.
SIGNAL_FILE="${MSM_SIGNAL_FILE:?nicht gesetzt — kommt aus dem systemd-Unit (deploy/host-watcher/install.sh)}"

[ -f "$SIGNAL_FILE" ] || exit 0

# Nur frische Anforderungen honorieren (< 10 min alt) — schützt vor einer
# Reboot-Schleife, falls die Datei einen Boot überlebt hat.
if [ -n "$(find "$SIGNAL_FILE" -mmin -10 2>/dev/null)" ]; then
  rm -f "$SIGNAL_FILE"
  logger -t msm-host-watcher "Reboot von MSM angefordert — starte System neu"
  sync
  systemctl reboot
else
  rm -f "$SIGNAL_FILE"
  logger -t msm-host-watcher "Veraltete reboot.request ignoriert und entfernt"
fi
