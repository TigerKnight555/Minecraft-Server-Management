#!/bin/bash
# MSM-Diagnose: sammelt alles Relevante für die Fehlersuche.
# Secrets (Passwörter, Hashes) werden maskiert — Ausgabe kann gefahrlos
# geteilt werden. Aufruf im Repo-Verzeichnis: bash diagnose.sh

section() { echo; echo "===== $1 ====="; }

mask() {
  # maskiert Passwort-/Hash-/Webhook-Werte: leer -> (LEER), sonst (GESETZT, n Zeichen)
  awk -F'=' '{
    if ($1 ~ /(RCON_PASSWORD|PASSWORD_HASH|RESTIC_PASSWORD|DISCORD_WEBHOOK.*|DISCORD_WEBHOOKS)$/) {
      v = substr($0, length($1) + 2);
      if (length(v) == 0) print $1 "=(LEER)";
      else print $1 "=(GESETZT, " length(v) " Zeichen)";
    } else print $0;
  }'
}

section "Git-Stand"
git log --oneline -3 2>&1
git status --short 2>&1

section ".env (Secrets maskiert)"
if [ -f .env ]; then
  grep -v '^\s*#' .env | grep -v '^\s*$' | mask
else
  echo "FEHLT: keine .env im Verzeichnis $(pwd)"
fi

section "Docker-GID Abgleich"
echo "getent: $(getent group docker | cut -d: -f3)"
echo ".env:   $(grep '^DOCKER_GID=' .env 2>/dev/null || echo 'DOCKER_GID nicht gesetzt')"

section "Compose-Status"
docker compose ps 2>&1

section "MSM-Logs (letzte 30 Zeilen)"
docker compose logs --tail 30 msm 2>&1

section "Socket-Proxy-Logs (letzte 10 Zeilen)"
docker compose logs --tail 10 socket-proxy 2>&1

section "mc-fabric: Status + Query/RCON-Env (Passwort maskiert)"
docker inspect mc-fabric --format 'Status: {{.State.Status}}  Restarts: {{.RestartCount}}' 2>&1
docker inspect mc-fabric --format '{{range .Config.Env}}{{println .}}{{end}}' 2>/dev/null \
  | grep -E 'QUERY|RCON|VERSION|TYPE|MEMORY' | mask

section "Netzwerke: wer hängt wo"
for net in $(docker network ls --format '{{.Name}}' | grep -E 'minecraft-server-management|msm'); do
  echo "-- $net:"
  docker network inspect "$net" --format '{{range .Containers}}  {{.Name}}{{println}}{{end}}' 2>&1
done
echo "-- mc-fabric ist in:"
docker inspect mc-fabric --format '{{range $k, $v := .NetworkSettings.Networks}}  {{$k}}{{println}}{{end}}' 2>&1

section "Port-Bindung 8080"
ss -tlnp 2>/dev/null | grep 8080 || echo "nichts lauscht auf 8080!"

section "Healthcheck vom Host"
BIND=$(grep '^MSM_BIND_ADDR=' .env 2>/dev/null | cut -d= -f2)
BIND=${BIND:-127.0.0.1}
echo "curl http://$BIND:8080/healthz →"
curl -s -m 5 "http://$BIND:8080/healthz" || echo "(keine Antwort)"
echo

section "Erreichbarkeit Query/RCON aus MSM-Container"
docker compose exec -T msm /msm -healthcheck 2>&1 && echo "msm healthcheck: OK" || echo "msm healthcheck: FEHLGESCHLAGEN"

section "Phase 4: Backup-/Restore-Container"
for c in mc-backup mc-restore; do
  echo "-- $c:"
  if docker inspect "$c" >/dev/null 2>&1; then
    docker inspect "$c" --format '  Angelegt: {{.Created}}  Status: {{.State.Status}}  ExitCode: {{.State.ExitCode}}' 2>&1
    docker inspect "$c" --format '{{range .Config.Env}}{{println .}}{{end}}' 2>/dev/null \
      | grep -E 'RESTIC' | mask | sed 's/^/  /'
  else
    echo "  FEHLT — anlegen mit: docker compose --profile backup up -d --no-start $c"
  fi
done

section "Phase 4: NAS-Mount + restic-Repo"
echo "-- automount-Einheit:"
mount | grep -E 'mc-backups' || echo "  kein Mount für mc-backups sichtbar"
echo "-- Inhalt (Zugriff löst Automount aus):"
ls /mnt/mc-backups 2>&1 | head -5
echo "-- restic-Repo:"
ls /mnt/mc-backups/restic 2>&1 | head -5
echo "-- Restore-Job-Verzeichnis:"
ls -ld "$(grep '^MC_DATA_PATH=' .env 2>/dev/null | cut -d= -f2)/.msm-restore" 2>&1

section "Phase 4: letzte mc-backup-Logs"
docker logs mc-backup --tail 15 2>&1 || echo "(keine Logs)"

section "Ressourcen"
docker stats --no-stream msm msm-socket-proxy 2>&1

echo
echo "===== Ende Diagnose ====="
