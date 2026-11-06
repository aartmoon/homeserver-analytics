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
cp -a compose.yaml prometheus blackbox grafana secrets textfile scripts templates integrations deploy cmd internal go.mod go.sum README.md "$snapshot_dir/config/"
cp -a data/container-collector "$snapshot_dir/config/collector-state"
# The bot stays running; SQLite's online backup takes a consistent queue snapshot.
if [[ -f ../serverbot/data/monitoring-inbox.sqlite3 ]]; then
    python3 - "$snapshot_dir/serverbot" "$PWD/../serverbot" <<'PYBOT'
from pathlib import Path
import shutil
import sqlite3
import sys
dest, source = map(Path, sys.argv[1:])
dest.mkdir(mode=0o700)
for name in ['bot.py', 'config.py', '.env', 'serverbot-go']:
    if (source / name).is_file():
        shutil.copy2(source / name, dest / name)
        (dest / name).chmod(0o700 if name == 'serverbot-go' else 0o600)
with sqlite3.connect('file:' + str(source / 'data/monitoring-inbox.sqlite3') + '?mode=ro', uri=True) as db:
    with sqlite3.connect(dest / 'monitoring-inbox.sqlite3') as backup:
        db.backup(backup)
(dest / 'monitoring-inbox.sqlite3').chmod(0o600)
PYBOT
fi
docker compose start
archive_path="$PWD/backups/monitoring-$(date -u +%Y%m%dT%H%M%SZ).tar.gz"
tar -czf "$archive_path" -C "$snapshot_dir" .
chmod 600 "$archive_path"
tar -tzf "$archive_path" >/dev/null
printf '%s\n' "Backup created and archive structure verified. Copy encrypted off the VM; restore still needs testing."
