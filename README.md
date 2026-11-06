# Homeserver monitoring — Docker Compose

Все рабочие компоненты находятся в одном независимом ~/monitoring Compose project:
9 контейнеров, включая Go Telegram-бота и Go-сборщик. Старые Python-бот/collector,
.venv, exec-обёртки, host binaries, systemd units и legacy rollback-копии удалены.
Очередь, секреты и метрики сохранены. Сайты/БД/MinIO не входят в этот Compose.

## Компоненты и доступ

| Сервис | Назначение | Доступ |
|---|---|---|
| Grafana13.3.0 | 5 дашбордов, 17 правил | 127.0.0.1:3001 |
| Kuma2.5.9 | UI HTTP-мониторов, ручная настройка | 127.0.0.1:3002 |
| Prometheus3.16.0 | Метрики/история | 127.0.0.1:9090 |
| Node Exporter1.12.1 | Ubuntu | 127.0.0.1:9100 |
| Blackbox0.29.1 | HTTPS/text/image/TLS probes | 127.0.0.1:9115 |
| postgres_exporter0.20.1 | Общая PostgreSQL | 127.0.0.1:9187 |
| serverbot (Go) | Прежний Telegram account, polling+alerts | webhook127.0.0.1:9088 |
| container-collector (Go) | Контейнеры/OOM/restarts, каждые60s | network none |
| docker-read-proxy (Go) | Ограниченный read API для collector | только Unix socket |

Официальные образы и builder/runtime в Dockerfile закреплены digest. Три Go
сервиса собираются из этого repository через multi-stage Dockerfile; runtime
Distroless без shell/Python/Go toolchain. CGO SQLite собран в Go builder Debian12,
совместим с runtime base-debian12. Зависимости закреплены go.mod/go.sum.

С компьютера в домашней сети:

```bash
ssh -N -L 3001:127.0.0.1:3001 -L 3002:127.0.0.1:3002 -L 9090:127.0.0.1:9090 sysadmin@192.168.1.105
```

Снаружи использовать существующий SSH-адрес ВМ. Интерфейсы localhost:3001,
localhost:3002, localhost:9090. Grafana login admin, первоначальный пароль
читать локально: `less ~/monitoring/secrets/grafana_admin_password`.
Не публиковать значения файлов secrets/ и Docker inspect Env.
В Kuma сначала создать администратора через туннель; мониторы ещё нужно добавить.

## Запуск и эксплуатация

```bash
cd ~/monitoring
# Новый clone: создаёт только отсутствующие private файлы/каталоги.
python3 scripts/init-local.py
# На новом хосте заполнить secrets/serverbot.env прежним token/owner,
# создать PG monitoring role и согласовать её пароль; существующие секреты не менять.
docker network inspect monitoring_private >/dev/null 2>&1 || docker network create monitoring_private
docker compose config -q
docker compose build serverbot container-collector docker-read-proxy
docker compose up -d
./scripts/check-config.sh
./scripts/verify-runtime.py
```

В secrets/serverbot.env600 находятся прежние TG_BOT_TOKEN/TG_ALLOWED_ID/TG_PROXY,
MONITORING_*; это не новый бот. Шаблон integrations/serverbot/.env.example не
содержит настоящих значений. Go container env переопределяет secret paths на
/run/secrets/...; Bot token напрямую в Grafana не передаётся.
Root .env600 содержит только локальный DOCKER_SOCKET_GID, установленный init script
по /var/run/docker.sock. В текущей ВМ уже всё заполнено и запущено.

Управление только мониторингом:
`docker compose ps`, `docker compose stop`, `docker compose start`,
`docker compose restart serverbot`, `docker compose logs --tail100 serverbot`.
Не публиковать логи без проверки. Restart unless-stopped, json logs10MB×3.
Перед config changes — защищённая копия dirs700/files600. Для update сохранить
backup, сменить проверенные версии/digests, build/config tests, up и runtime tests.
Rollback — предыдущий Git revision И совместимый data backup: миграции Grafana/
Kuma могут не поддерживать downgrade. Не использовать down -v и не трогать тома
ruk/talk. Старые Python установки для отката больше не хранятся.

## Безопасность контейнеров

Три новых сервиса: UID1000, cap_drop ALL, no-new-privileges, read-only rootfs,
ограниченные CPU/RAM/PIDs, private data mounts. Bot host network необходим для
существующего SOCKS5 localhost1080 и Grafana localhost9088; публикуемых ports нет.
Только три файла /proc/uptime,meminfo,loadavg смонтированы read-only для host status.
Пустой data/host-filesystem bind даёт statfs того же диска без доступа ко всему /.
Старого root systemd serverbot.service и user timer больше нет.

Collector не имеет raw Docker socket и Docker group. Он видит только restricted
Unix socket из data/docker-proxy, network none. Proxy один имеет raw host socket
и supplemental Docker GID, также network none. Read-only socket mount НЕ является
защитой API; защиту для collector обеспечивает Go handler proxy:
- только GET /version, scoped container list/inspect/stats, bounded OOM events;
- только проекты ruk/talk/monitoring, принудительные query filters;
- ответы projected: без Env, Command, binds, secret labels, лишних event attrs;
- create/exec/archive/logs/images/прочие endpoints и любые mutations запрещены.
Proxy — доверенная компонента: компрометация процесса с raw socket всё ещё опасна.
Сокет защищён700-каталогом/660-inode, TCP port отсутствует. Не отдавать его другим
проектам. Тесты проверяют запрет writes/endpoints и redaction.

Prometheus/Grafana/Node используют host network с явными127.0.0.1 bindings;
существующий firewall мешал bridge→host, его правила не менялись. Exporters
публикуются толькоloopback. PostgreSQL exporter соединён со своей сетью и только
существующей ruk_backend. Сети всех проектов не объединялись.
Node Exporter имеет read-only host root/proc/sys и host PID для метрик;
остальные новые Go сервисы не получают весь root или Docker API.

## Реальные сайты и метрики

13 targets Prometheus UP. HTTP interval60s; timeout10s (магазин readiness5s),
проверка TLS включена, HTTPS обязателен, GET/status200. Семь probes:

| project/service | URL / проверка |
|---|---|
| cottontoys/homepage | https://cottontoys.ru/ — «Ватные игрушки» |
| cottontoys/catalog | https://cottontoys.ru/catalog — «Каталог игрушек» |
| cottontoys/readiness | https://cottontoys.ru/api/health — database=ok |
| cottontoys/photo | Реальная image/webp ниже |
| talk/homepage | https://rp-6.ru/ |
| talk/liveness | https://rp-6.ru/healthz |
| talk/readiness | https://rp-6.ru/readyz |

https://cottontoys.ru/rukodelie-media/products/2026-08-01/60c7b406-5174-4a72-81f2-ba2057e3ae59.webp
найдена в существующей карточке товара, реально загружена200 image/webp.
Сохранить контрольный объект либо заранее заменить URL. URL/UUID не попадают
в metric labels: HTTP instance — стабильное имя проверки.

Labels project/service/environment/instance: cottontoys, talk, infrastructure,
production и стабильные service/container names. Без email/order IDs/URL labels.
Общая PostgreSQL/Traefik относятся к infrastructure. Proxy HTTP service labels
shop/media→cottontoys, talk-api/talk-web/talk-media→talk. Считаются запросы reverse
proxy, не внутренние app spans; duration buckets0.1/0.3/1/2.5/5s.
Существующий ~/ruk/docker-compose.yml содержит metrics flags и127.0.0.1:8082.
Traefik был пересоздан с отдельным разрешением ранее; в этой контейнеризации
production не перезапускался. Diff сохранён reports/ruk-compose.patch.

Магазин /api/health реально делает SELECT1, при ошибке503 с общим сообщением.
Собранный код прочитан без изменения; исходников на ВМ нет. Отдельную liveness
без БД и server-side deadline/cancellation readiness (budget2s, query≤1s) ещё
нужно добавить в исходном репозитории и покрыть тестами dependency failure/timeout.
БД production ради теста не останавливали. Сейчас существующая readiness подключена.

Ubuntu: CPU/RAM/swap/FS/inode/network/disk IO. PG: pg_up, connections по DB,
max_connections100, commits/rollbacks и стандартные exporter stats. Роль
monitoring_exporter: CONNECT rukodelie+pg_monitor, connection limit3, statement
 timeout5s, default read-only; без superuser/CREATEDB/CREATEROLE/replication/SELECT
на public tables. Пароль отдельный в secrets/postgres_password600. На новом хосте
создать роль администратором, пароль назначать интерактивно, не CLI аргументом.

Collector: 18 долгоживущих Compose containers, up, working-set RAM, CPU cores
(среднее между успешными samples), automatic restart totals, OOM events.
Смена ID не сбрасывает totals; первая CPU baseline0. Docker events имеют буфер256,
proxy читает не более24h истории: после долгого простоя возможна потеря исторических
OOM. Это не lossless аудит. Freshness контролируется. State700/600, textfile644
через fsync+atomic replace. Удалённый намеренно сервис остаётся up0: после окна
обслуживания убрать именно его запись из state, не обнулять все counters.

## Дашборды и алерты

Папка Monitoring: Host, Projects, Project selector, PostgreSQL, Monitoring itself.
Проверены реальные queries через Grafana datasource. p95 скрыт при низком числе
запросов, backup timestamp пока No data — это отсутствие статуса, не успешный ноль.

Grafana17 rules, health=ok. Prometheus reference rules только для promtool tests.

| Условие | Pending / уровень |
|---|---|
| up=0 | 3m critical |
| 3 последовательные ошибки HTTP60s | на третьей, critical; recovery на успехе |
| Диск <15%, но≥5% | 10m warning |
| Диск <5% | 5m critical |
| inode free<10% | 10m warning |
| MemAvailable<10% | 15m warning |
| CPU busy>90% | 15m warning |
| TLS осталось<14дней | 1h warning |
| Нет статуса успешного backup cottontoys | 15m warning |
| pg_up=0 | 2m critical |
| PG connections>80% limit | 10m warning |
| container_up=0 | 3m critical |
| ≥3 automatic restarts/15m | 2m warning |
| Новый OOM/15m | critical |
| Collector stale>180s | 2m critical |
| ≥5 HTTP5xx/15m, >5%, ≥50 requests | 5m warning |
| HTTPp95>2.5s, ≥100 requests/15m | 10m warning |

Grouping folder/alertname/project, wait30s, interval5m, repeat4h; recovery включено.
Bot dedup suppresses identical firing7days, включая повторные reminders, новый
incident/recovery — новый ключ. NoData OK у threshold rules, lost collection
проверяется отдельно; datasource error — Error. Уведомляет только Grafana,
Kuma notifications остаются off. Maintenance: Kuma pause/schedule; Grafana pause
provisioned rule через isPaused+reload или silence/mute. Для общего mute применить
существующий delivery-disabled к catch-all child route serverbot. Сейчас mute off.

## Telegram

Один прежний account/polling, owner-only private messages/callbacks, прежняя кнопка
статуса. SQLite data/serverbot/monitoring-inbox.sqlite3 сохранена с прежними ID.
Новый Go процесс и версия в Docker прошли реальную отправку test problem/recovery.
Webhook /grafana и reserved /kuma имеют разные Bearer secrets. Приём JSON64KiB,
read5s, до100 alerts/request; 401/400/413/415/503, queue1000 pending/10000 rows.
202 после WAL/FULL commit. Send timeout30s, retry до1h с учётом Telegram retry_after,
shutdown сохраняет pending. Редкий дубль после crash send→commit возможен:
at-least-once, не exactly-once. Подробнее integrations/serverbot/README.md.

## Kuma и новый сайт

Kuma healthy, но ещё нужно создать администратора и вручную добавить семь probes
из таблицы. HTTP(s) Keyword для текста главной/каталога, JSON query для readiness
либо keyword `"database":"ok"`. Status200, interval60s, retries2, retry60s,
timeout10s, TLS verification, certificate warning14days. Notifications OFF,
retention30days; tags project/service/environment/instance. БД Kuma не правилась:
поддерживаемого стороннего API provisioning monitors нет.

Новый сайт: реальный URL по templates/project.json добавить в file_sd
prometheus/targets/http.json; file_sd перечитается. Для текста создать blackbox
module и перезапустить только blackbox. Проверить probe_success/labels и добавить
Kuma monitor вручную. Health URL сначала найти, не угадывать. Для контейнеров
нового Compose project расширить whitelist collector И docker proxy и tests.

## Бэкапы

Регулярные backups магазина/MinIO не подтверждены: user crontab отсутствует,
доступные timers не показали project backups; root crontab полностью не проверен.
Миграционные архивы не подтверждают расписание/успех/offsite. BackupStatusUnknown
сообщает именно это. Не публиковать фиктивный success timestamp.
scripts/backup-success.py вызывать ТОЛЬКО после успешного dump/upload/verification:
`./scripts/backup-success.py cottontoys database` (media отдельный backup_job).
После появления реального статуса убрать deleteRules monitor-9 из active rules,
включить templates/grafana-backup-expired.yaml: >36h for15m critical для daily job.
Согласовать график и expected jobs database/media; success script сам не бэкапит.

Backup текущего мониторинга: scripts/backup-monitoring.sh останавливает только
все9 monitoring containers, делает согласованные копии Prometheus/Kuma/Grafana/
serverbot/collector state, config/source/secrets, затем запускает их обратно.
Архив600/backups700 содержит Telegram token/пароли: offsite только зашифрованно.
Старые legacy rollback-копии удалены по запросу; новые backups содержат текущую
Docker-версию. Расписание/offsite не включались. Проверка tar listing не заменяет
полный restore test, который ещё не выполнен.

Restore: остановить monitoring, сохранить текущее состояние, распаковать в700-dir.
TSDB/Kuma восстанавливать в новые тома с владельцами65534 и1000, переключить через
volume override; старые тома не удалять. Bind data/grafana, data/serverbot,
data/container-collector вернуть UID1000. Secret files600; nonsecret provisioning
644/dirs755. data/docker-proxy/socket не бэкапить, создаётся заново; empty
host-filesystem dir создать init script. Пересобрать Go images, config/start,
проверить историю/очередь/targets. Не копировать поверх работающей SQLite/TSDB.

## Проверки и ограничения

`go test -race ./...`, `go vet ./...`; scripts/check-config.sh запускает promtool
config/rules/unit tests HTTP recovery, disk/RAM и low-traffic 5xx/p95 guards.
verify-runtime.py проверяет targets/samples/dashboard objects и реальные queries.
test-alert-runtime.py временно глушит project=test, production не останавливает.
test-telegram-runtime.py явно отправляет реальные test problem/recovery, затем
удаляет временные rule/gauge/fast route. Не запускать его в CI.

Порты loopback подтверждены ss/bindings и недоступностью LAN-IP из другого
Docker namespace. Проверка с отдельного физического устройства/интернета остаётся
отдельной задачей. При отключении ВМ/электричества/домашнего интернета мониторинг
и доставка тоже исчезнут. Домен с этой ВМ не заменяет внешний probe (DNS/hairpin/
путь провайдера могут отличаться). Внешний независимый монитор здесь не создан.

Текущий отчёт и перечень удаления: reports/CONTAINERS.md. Бинарники/секреты/.env/
data/тома/backups не коммитятся; Go source, pinned Dockerfile, config/tests/docs в Git.
