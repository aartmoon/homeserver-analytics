# Current Docker deployment, 2026-11-06

All nine monitoring services now run in ~/monitoring/compose.yaml, including
serverbot, container-collector and docker-read-proxy. No host bot/collector
service, Python launcher or virtual environment remains.

Removed after successful container delivery test:
- ~/serverbot, including .venv, Python implementation, wrapper and host Go binary;
- root serverbot.service and its autostart link (stopped/disabled, daemon reloaded);
- user collector service/timer and their repository copies;
- legacy/ Python sources/tests/patches, host bin/, host build/install examples;
- old rollback/config snapshots and archives containing the legacy implementation.
Current token/env is secrets/serverbot.env600; current inbox is data/serverbot.
No secret, queue, metric history or project volume was discarded. A new backup
uses only the current Docker deployment. Git history is retained normally.

Images: official Go1.26.8-bookworm builder and Distroless base-debian12 pinned
by digest in Dockerfile. Go/sqlite dependency versions pinned by go.mod/go.sum.
Runtime images contain only the Go binaries/base libraries, no shell/Python/SDK.
All three new services UID1000, read-only rootfs, cap_drop ALL, no-new-privileges,
64MiB budget each, bounded CPU/PIDs, rotating logs and unless-stopped restart.

Bot retains the same account, owner, SOCKS5 proxy and one polling. Host network
is used only to reach localhost proxy/webhook. It mounts just three host proc
files plus an empty directory for host statfs, not the whole host root.
Grafana webhook remains127.0.0.1:9088, no public port/router/firewall changes.
A real Grafana→container bot→Telegram test problem and recovery were accepted;
queue integrity=ok, seven delivered records and no pending at that check.

Collector has no raw Docker socket/group/network. It reads a restricted Unix
socket exposed by the isolated Go proxy. Proxy alone sees raw socket; :ro is NOT
considered protection. Actual controls: GET-only endpoint whitelist, project
scope, forced stats/list/event queries, no Env/Command/binds/secret-label fields
in returned responses. Mutation/exec/archive/log endpoints rejected. OOM history
bounded to24h/Docker's256-event buffer. Proxy itself remains a trusted component
because raw Docker access is powerful; it has no network or published port.

Validation: Go race tests/vet; whitelist/redaction/OOM tests and host-stat fixture;
Docker Compose config/build; all three new Docker healthchecks; real Telegram
problem/recovery; Node Exporter receives 18 container samples; Prometheus13/13 UP
and Grafana datasource queries passed before the backup restart. Current backup
script stops/restarts only monitoring, preserves bot/collector state and checks
archive structure. Full archive restore and external-internet port tests remain
unperformed. Sites/Traefik/DB/MinIO were not restarted during this migration.

A one-off administrative container was used to stop/disable the old root unit,
then removed. Runtime containers do not mount D-Bus/systemd or use unconfined
AppArmor/privileged mode. Other system units, firewall and SSH were unchanged.

Remaining original tasks: manual Kuma administrator/monitors, source-side app
liveness/readiness deadline, confirmed project backup schedule/status/offsite,
full restore test and independent external uptime checking. These are not
represented as complete by this container migration.
