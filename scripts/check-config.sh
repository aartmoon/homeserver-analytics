#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
docker compose config -q
docker compose run --rm --no-deps --entrypoint promtool prometheus check config /etc/prometheus/prometheus.yml
docker compose run --rm --no-deps --entrypoint promtool prometheus check rules /etc/prometheus/tests/reference-rules.yml
docker compose run --rm --no-deps --entrypoint promtool prometheus test rules /etc/prometheus/tests/rules.test.yml
