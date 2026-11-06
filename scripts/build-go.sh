#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p bin
# go.sum pins dependencies. serverbot bundles SQLite via CGO; gcc is required.
CGO_ENABLED=1 go build -trimpath -ldflags='-s -w' -o bin/serverbot-go.new ./cmd/serverbot
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/container-collector.new ./cmd/container-collector
chmod 755 bin/serverbot-go.new bin/container-collector.new
mv bin/serverbot-go.new bin/serverbot-go
mv bin/container-collector.new bin/container-collector
