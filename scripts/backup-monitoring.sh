#!/usr/bin/env bash
set -euo pipefail
umask 077
cd "$(dirname "$0")/.."
mkdir -p backups
chmod 700 backups
snapshot_dir=$(mktemp -d "$PWD/backups/staging.XXXXXXXX")
trap 'docker compose start >/dev/null; rm -rf -- "$snapshot_dir"' EXIT
# All monitoring services, including the bot, stop together; no project service is touched.
docker compose stop
mkdir "$snapshot_dir/prometheus" "$snapshot_dir/kuma" "$snapshot_dir/config"
docker compose cp prometheus:/prometheus/. "$snapshot_dir/prometheus/"
docker compose cp kuma:/app/data/. "$snapshot_dir/kuma/"
cp -a data/grafana "$snapshot_dir/grafana"
cp -a data/serverbot "$snapshot_dir/serverbot"
cp -a data/container-collector "$snapshot_dir/collector-state"
cp -a compose.yaml Dockerfile .dockerignore prometheus blackbox grafana secrets scripts templates integrations cmd internal go.mod go.sum README.md "$snapshot_dir/config/"
docker compose start
archive_path="$PWD/backups/monitoring-$(date -u +%Y%m%dT%H%M%SZ).tar.gz"
tar -czf "$archive_path" -C "$snapshot_dir" .
chmod 600 "$archive_path"
tar -tzf "$archive_path" >/dev/null
printf '%s\n' 'Current Docker monitoring backup created and archive structure verified; full restore is not yet tested.'
