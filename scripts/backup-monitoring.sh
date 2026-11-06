#!/usr/bin/env bash
set -euo pipefail
umask 077
cd "$(dirname "$0")/.."
mkdir -p backups
chmod 700 backups
snapshot_dir=$(mktemp -d "$PWD/backups/staging.XXXXXXXX")
trap 'docker compose start >/dev/null; rm -rf -- "$snapshot_dir"' EXIT
docker compose stop
mkdir "$snapshot_dir/prometheus" "$snapshot_dir/kuma"
docker compose cp prometheus:/prometheus/. "$snapshot_dir/prometheus/"
docker compose cp kuma:/app/data/. "$snapshot_dir/kuma/"
cp -a data/grafana "$snapshot_dir/grafana"
mkdir "$snapshot_dir/config"
cp -a compose.yaml prometheus blackbox grafana secrets textfile scripts templates integrations README.md "$snapshot_dir/config/"
cp -a data/container-collector "$snapshot_dir/config/collector-state"
docker compose start
archive_path="$PWD/backups/monitoring-$(date -u +%Y%m%dT%H%M%SZ).tar.gz"
tar -czf "$archive_path" -C "$snapshot_dir" .
chmod 600 "$archive_path"
tar -tzf "$archive_path" >/dev/null
printf '%s\n' "Backup created and archive structure verified. Copy encrypted off the VM; restore still needs testing."
