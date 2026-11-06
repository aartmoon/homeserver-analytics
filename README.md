# Мониторинг homeserver

Стек запущен 6 ноября 2026. Шесть контейнеров работают; 13 targets Prometheus UP,
семь HTTPS-проб успешны. Проверены provisioning пяти дашбордов, реальные запросы
через Grafana datasource, тестовый Firing→Normal и сохранение данных после restart.
Незавершённые части: ручная настройка Kuma, отдельная liveness и ограничение времени readiness магазина в исходниках,
регулярные бэкапы проектов. Полный отчёт: [reports/RESULT.md](reports/RESULT.md).

## Архитектура и безопасность

Независимый Compose project `monitoring` в ~/monitoring. Обновление/остановка
ruk/talk не останавливает его; зависимость PostgreSQL может стать DOWN, но сам
мониторинг продолжит работать. Второго Traefik, Loki, Tempo, Kubernetes нет.

| Компонент | Назначение | Привязка |
|---|---|---|
| Grafana 13.3.0 | Пять дашбордов, вычисление алертов | 127.0.0.1:3001 |
| Uptime Kuma 2.5.9 | UI и история HTTP-мониторов, требуют ручного добавления | 127.0.0.1:3002 |
| Prometheus 3.16.0 | Сбор/TSDB | 127.0.0.1:9090 |
| Node Exporter 1.12.1 | Ubuntu CPU/RAM/swap/FS/inode/network/IO + textfile | 127.0.0.1:9100 |
| Blackbox Exporter 0.29.1 | Подготовленные HTTPS/text/TLS/image пробы Grafana | 127.0.0.1:9115 |
| postgres_exporter 0.20.1 | Общий PostgreSQL | 127.0.0.1:9187 |

Образы официальных проектов, закреплены tags И digest. Версии проверены по
GitHub releases и реально загружены. Blackbox нужен для автоматических
HTTP/TLS-метрик Grafana, пока Kuma настраивается вручную поддерживаемым UI.
Внутреннюю БД Kuma не редактируем.

Prometheus, Grafana и Node Exporter используют host network с ЯВНЫМИ loopback
listen-address. Так доступны настоящие сетевые счётчики Ubuntu, без изменения
существующего firewall, который блокировал Docker bridge→host. Kuma/Blackbox
остаются в monitoring_private. PostgreSQL exporter подключён только к этой сети
и существующей ruk_backend; его порт опубликован исключительно на loopback.
Prometheus/Grafana не добавлялись в сети проектов. monitoring_private помечена
external для сохранения после `compose down`; уже создана. При переносе на новую
ВМ сначала `docker network create monitoring_private`.

Node Exporter читает /proc, /sys и / хоста read-only и использует host PID;
это чувствительное чтение, ограниченное exporter. Привилегии всех контейнеров:
cap_drop ALL, no-new-privileges; privileged отсутствует. Docker socket/API в
контейнеры не монтируется. Read-only socket не считался бы защитой API.
Новые секреты в secrets/ (каталог700, файлы600), исключены из git.

## Доступ

С компьютера в домашней сети:

```bash
ssh -N -L 3001:127.0.0.1:3001 -L 3002:127.0.0.1:3002 -L 9090:127.0.0.1:9090 sysadmin@192.168.1.105
```

При доступе извне заменить IP существующим SSH-адресом ВМ. Не открывать порты
мониторинга на роутере. Интерфейсы: Grafana http://localhost:3001,
Kuma http://localhost:3002, Prometheus http://localhost:9090.
Grafana login `admin`; пароль читать локально в доверенном терминале:
`less ~/monitoring/secrets/grafana_admin_password`. Не публиковать его в чатах/логах.
Grafana работает uid1000; bind data/grafana700 и пароль600 доступны ей без
расширения прав. В Kuma ещё нужно создать первого администратора через туннель.

Проверены ss, Docker bindings и недоступность 3001/3002/9090/9100/9115/9187,
8082/9088/5432/2375/2376 по LAN-IP ВМ из отдельного контейнера. Это проверка из
другого network namespace, НЕ проверка из внешнего интернета или другого
физического устройства. Firewall и правила SSH не изменялись. На ВМ уже был
существующий listener19999 на внешних интерфейсах; он не создан этим стеком
и в рамках этой задачи не изменялся.

## Какие данные собираются

Основные метрики раз в30s; HTTP каждые60s. labels project/service/environment/
instance стабильны: cottontoys, talk, infrastructure. instance HTTP — имя пробы,
а не URL; у контейнеров — стабильное Compose-имя без Docker ID. URL изображения
с UUID передаётся как target, но не сохраняется в metric labels. Не добавлять
labels с email/заказами/произвольными URL. Общие PostgreSQL/Traefik относятся
к infrastructure; БД общая для rukodelie и talk.

| project | service | URL и условие |
|---|---|---|
| cottontoys | homepage | https://cottontoys.ru/ — 200, «Ватные игрушки» |
| cottontoys | catalog | https://cottontoys.ru/catalog — 200, «Каталог игрушек» |
| cottontoys | readiness | https://cottontoys.ru/api/health — 200, JSON database=ok |
| cottontoys | photo | Реальная фотография ниже — 200, Content-Type image/* |
| talk | homepage | https://rp-6.ru/ — 200 |
| talk | liveness | https://rp-6.ru/healthz — 200 |
| talk | readiness | https://rp-6.ru/readyz — 200 |

Фотография из ссылки Image существующей карточки
/catalog/ozon-4512-033581b097:
https://cottontoys.ru/rukodelie-media/products/2026-08-01/60c7b406-5174-4a72-81f2-ba2057e3ae59.webp
Её реальная загрузка с ВМ проверена: HTTP200, image/webp. Не удалять объект при
архивировании товара либо заранее заменить контрольный URL.
Все семь проб: TLS verification включена, HTTPS обязателен, статус строго200,
GET, timeout10s (readiness5s). Проверка срока сертификата собирается Blackbox.

Для магазина прочитан собранный health handler из образа БЕЗ его изменения:
`SELECT 1`, 200 с database=ok; при ошибке503 с database=unavailable без exception.
Сейчас endpoint работоспособен. Явного server-side timeout в handler нет,
отдельная liveness не найдена. Исходного репозитория на ВМ нет. В исходниках
нужно добавить liveness без зависимостей, readiness с общим budget2s, DB query
≤1s с корректной отменой/освобождением соединения, no-store, без stacktrace/DSN.
Проверить успешную/недоступную/медленную БД и отсутствие вызова БД в liveness.
Эти новые endpoints и тесты НЕ созданы внутри контейнера. Текущую БД ради
проверки отказа не останавливали; failure path проверен чтением кода, не сбоем БД.
Talk host/health routes найдены в существующем Compose и проверены HTTP-запросами.

PostgreSQL exporter: pg_up=1, max_connections=100, подключения по БД,
commit/rollback counters и стандартные статистики exporter. Отдельная роль
monitoring_exporter: CONNECT rukodelie, pg_monitor, connection limit3,
statement_timeout5s, default_transaction_read_only. Нет superuser/CREATEDB/
CREATEROLE/replication; SELECT на таблицы public не выдан (проверено).
Пароль приложения не использован. Подробнее templates/postgres.md.

Контейнеры: 15 длительно работающих Compose containers; up, working-set RAM,
CPU cores (среднее между сборами Docker stats), накопленные автоматические рестарты,
OOM events. Сборщик bin/container-collector (Go) работает НА ХОСТЕ от sysadmin
по user systemd timer каждую минуту, сохраняет состояние атомарно в
 data/container-collector (700), textfile читает Node Exporter. Он не логирует
полный inspect/env и не передаёт Docker API контейнерам. Включён linger sysadmin
для работы user timer без SSH-сессии. Временные setup/minio-init/promtool
контейнеры исключаются по restart policy.

Restart счётчик накапливает приращения Docker RestartCount с учётом смены
container ID; ручная перезагрузка приложения сама по себе не объявляется OOM.
OOM события дедуплицируются по timeNano/ID. Docker хранит ограниченный буфер
событий; при долгой остановке collector/daemon или >256 Docker events за паузу
часть исторических OOM может потеряться. Это не аудит с гарантией без потерь;
свежесть collector контролируется отдельным алертом. После намеренного удаления
сервиса удалить его запись из state только после защищённой копии и обслуживания,
иначе container_up=0 справедливо укажет на отсутствие ранее известного сервиса.

## Дашборды

Папка Monitoring: Домашний сервер; Обзор проектов; Отдельный проект с выбором
project; PostgreSQL; Состояние мониторинга. Есть реальные CPU/RAM/swap/FS/inode,
сеть wlp4s0, disk IO, HTTP status/длительность/TLS, PostgreSQL, targets и
контейнеры. Реальные запросы через Grafana datasource проверены API.
Панели requests/5xx/p95 используют включённые метрики существующего Traefik.
При недостаточном числе запросов p95 намеренно No data. Backup timestamp No data,
пока реальная работающая задача не передаёт статус. Не трактовать это как ноль.

## Kuma — ручной шаг остаётся

В UI создать первого администратора, затем семь мониторов из таблицы.
Type HTTP(s) Keyword: главная, каталог, readiness магазина. Keywords:
«Ватные игрушки», «Каталог игрушек», `"database":"ok"` соответственно;
при изменении JSON whitespace использовать подходящий JSON Query monitor.
Остальные HTTP(s); для фотографии проверить именно полный публичный URL.
Accepted status200, interval60s, retries2 (первый сбой +2 повтора =3 ошибки),
retry interval60s, timeout10s, HTTPS verification включена.
Добавить tags project/service/environment/instance. Notifications отключены.
Для главной включить срок сертификата, warning14дней. Восстановление видно
в истории после успешной пробы. Kuma retention установить30дней.

Внутренний API управления Kuma не является поддерживаемым сторонним API;
он не использован, внутренняя БД не редактировалась. До ручного добавления
Kuma не показывает историю сайтов; provisioned Blackbox/Grafana уже проверяют их.

## Алерты и обслуживание

Grafana вычисляет 17 правил. Prometheus reference rules служат promtool тестам,
не создают второй активный источник тех же алертов. Telegram-доставка включена через существующий serverbot и localhost webhook.
Receiver serverbot, recovery включено; SMTP off. Вычисление и история состояний
продолжаются. delivery-disabled — доступный mute timing, сейчас ни к одному
маршруту не применён. Кумa notifications выключены для предотвращения дублей.
Группировка по grafana_folder/alertname/project, group_wait30s, interval5m,
repeat4h. Проверена фактическая notification policy через API.

| Условие | Задержка | Уровень |
|---|---|---|
| Потеря scrape (up=0) | 3m | critical |
| Три неуспешные минутные HTTP-пробы | до третьей ошибки; max/count over3m | critical |
| Диск <15%, но ≥5% | 10m | warning |
| Диск <5% | 5m | critical |
| Свободных inode <10% | 10m | warning |
| MemAvailable <10% | 15m | warning |
| CPU busy >90% | 15m | warning |
| TLS осталось <14дней | 1h | warning |
| Нет отметки успешного backup cottontoys | 15m | warning |
| pg_up=0 | 2m | critical |
| Подключения >80% max_connections | 10m | warning |
| container_up=0 | 3m | critical |
| ≥3 автоматических рестарта /15m | 2m | warning |
| Новый OOM /15m | без дополнительного pending | critical |
| Collector не обновлялся >180s | 2m | critical |

Recovery HTTP — следующая успешная проба; остальные — выход из условия.
NoDataState=OK у порогов; отсутствие сбора отдельно обнаруживают up и свежесть
collector, ошибка datasource — Grafana Error. Низкий трафик учтён в активных
Traefik-правилах: ≥5 ошибок5xx/15m, >5%, ≥50 запросов, for5m; p95>2.5s при
≥100 запросах/15m, for10m. Это запросы reverse proxy, а не latency внутри app.
При малом трафике основная проверка — синтетическая доступность.

На обслуживание: Pause мониторов/maintenance schedule Kuma; для Grafana —
Pause evaluation (у provisioned rules через isPaused и reload) либо silences/
mute timings, если нужно только подавить будущую доставку. По окончании вернуть.
HTTP/TLS/backup уведомляет Grafana; Kuma оставлена локальной для тех же проверок,
чтобы не дублировать сообщения. Для экстренного отключения доставки применить
delivery-disabled к catch-all child route, затем reload provisioning.

## Traefik: подключён после согласования перезапуска

По явному разрешению пользователя пересоздан ТОЛЬКО ruk-traefik-1.
В ~/ruk/docker-compose.yml добавлены шесть metrics flags и loopback binding
127.0.0.1:8082:8082. Прочие команды/порты80/443 сохранены, конфигурации до
изменения и точный runtime rollback сохранены в backups/traefik-UTC (700/600).
Это сохранено в основном Compose, поэтому обычный deploy не выключит метрики.
Traefik healthy, магазин и Talk после изменения ответили200.

Job traefik UP; реально собраны traefik_service_requests_total и duration buckets.
Service labels shop/media → cottontoys, talk-api/talk-web/talk-media → talk;
общие метрики → infrastructure. Это запросы reverse proxy, не внутренних
обработчиков. Панели 5xx возвращают0 только на основе существующего request
counter, если ошибок нет; p95 скрыт при <100 запросов/15m.
Правила 5xx/p95 включены; promtool тестирует low-traffic guard и срабатывание
при достаточной выборке. templates/ruk.traefik.override.yaml — справочный
эквивалент уже применённых дополнений, повторно подключать его не требуется.
Дальнейшее изменение static config также требует отдельного окна.

## Бэкапы проектов

У sysadmin crontab отсутствует. В доступных cron и systemd timers не найдены
задания резервирования ruk/MinIO; dpkg-db-backup — системный, не backup сайта.
Root crontab полностью не проверен из-за отсутствия sudo без пароля.
Миграционные архивы в ruk не подтверждают расписание/успех/внешнюю копию.
Регулярное резервное копирование магазина НЕ настроено этой работой.

scripts/backup-success.py — только передача статуса. Реальная задача вызывает
`~/monitoring/scripts/backup-success.py cottontoys database` СТРОГО после успешных
dump/upload/verification. Для MinIO отдельный backup_job. Скрипт не выполнялся
для создания зелёной отметки: backup_last_success_timestamp_seconds сейчас нет.
Он пишет через fsync+atomic replace; ошибка задачи не должна вызывать скрипт.

После появления реального статуса удалить deleteRules monitor-9 из активного
rules.yaml и включить templates/grafana-backup-expired.yaml: >36h, for15m,
critical. 36h рассчитано на ежедневный backup; согласовать с фактическим графиком.
Добавить проверку полноты ожидаемых jobs database/media, когда определено
расписание. Сейчас активен только BackupStatusUnknown, честно показывающий
отсутствие подтверждения, а не фиктивную давность последнего успеха.

## Запуск, остановка, проверки

```bash
cd ~/monitoring
docker compose config -q
./scripts/check-config.sh
docker compose up -d
./scripts/verify-runtime.py
docker compose ps
# Только мониторинг:
docker compose stop
docker compose start
```

scripts/check-config.sh проверяет Prometheus config/rules и promtool unit tests
HTTP recovery, порогов диска и длительного дефицита RAM. Скрипт runtime проверяет
UP targets, реальные метрики, пять dashboards и запросы через Grafana datasource.
После запуска ждать полный интервал HTTP60s: unknown сразу после старта ожидаем.

Безопасный тест: scripts/test-alert-runtime.py — отдельный project=test gauge,
временное правило Grafana, pending30s, Firing→Normal, затем удаление только
тестовых объектов. Проверен дважды, последний после финальной смены networking.
Никакого production outage и отправки Telegram. test-persistence.py перезапускает
ТОЛЬКО monitoring, проверяет сохранение исторической TSDB-выборки, временного
НЕ provisioned дашборда и файла тома Kuma, затем удаляет тестовые объекты.
Сохранение настроек вручную созданных Kuma-мониторов пока не проверялось:
их ещё нет. Тома существующих проектов не удалялись/не пересоздавались.

Go tests: `go test -race ./...`, статическая проверка `go vet ./...`.
Legacy compatibility tests: `~/serverbot/.venv/bin/python -m unittest discover -s ~/monitoring/tests -v`.
13 legacy Python тестов прошли, включая реальные HTTP413/401/400/202 на локальном aiohttp
TestServer, дедупликацию, bounded/durable queue и OOM/restart collector.
Новых health handlers нет, поэтому их source-level тестов на ВМ нет.

User timer: `systemctl --user status monitoring-container-collector.timer`;
ручной сбор: `systemctl --user start monitoring-container-collector.service`.
Остановка всего мониторинга при длительном обслуживании также требует остановки
этого user timer; обычный Compose stop не выключает хостовой сборщик.

## Хранение и ресурсы

TSDB: 15дней ИЛИ5GB (первое достигнутое ограничение); WAL/compaction требуют
дополнительного места. Это не общая квота всех томов. Docker json logs10MB×3
на контейнер, restart unless-stopped. RAM limits: Prometheus768MiB,
Grafana/Kuma по512MiB, node/blackbox по256MiB, postgres_exporter128MiB —
всего2432MiB лимитов. CPU limits0.25–1 ядра на контейнер.

Реальные три коротких замера: 421.6–427.8MiB RAM суммарно, 0.39–1.00% CPU
(100%=одно ядро). Это раннее наблюдение, не длительный нагрузочный профиль.
Collector oneshot: пик около30MiB, CPU154ms при одном запуске в минуту.
На ранней проверке data: Prometheus≈968KiB, Kuma≈16KiB, Grafana≈545MiB
(из них≈543MiB установленные plugins). Images и локальные backup-архивы отдельно.
Исходно на диске было≈198GB свободно; контролировать дальнейший рост.
Подробные samples в reports/runtime-resources.json. Проверять docker stats и
`docker system df` периодически; не выполнять prune для очистки без анализа.

## Бэкап мониторинга, обновление и откат

scripts/backup-monitoring.sh проверен: остановил только monitoring, скопировал
TSDB/Kuma/Grafana и config/secrets/collector state, снова запустил стек, создал
архив600 и проверил tar listing. Архив в backups/, каталог700. Он содержит
секреты: хранить зашифрованную копию вне ВМ. Автоматическое расписание и
offsite upload не включены. Полное восстановление архива ещё не тестировалось.

Архив: prometheus/, kuma/, grafana/ — данные; config/ — Compose/provisioning/
secrets/scripts/templates/integrations/README и collector-state. Восстанавливать
при остановленном monitoring, предварительно сохранив текущее состояние.
Распаковать в отдельный каталог700. Для TSDB/Kuma создавать НОВЫЕ тома,
копировать туда данные временным контейнером, владельцы65534:65534 (Prometheus),
1000:1000 (Kuma). Через Compose volume override подключить восстановленные тома;
старые не удалять. Grafana вернуть в data/grafana uid1000; collector-state в
 data/container-collector с файлами600. Восстановить secrets600, nonsecret
provisioning files644/dirs755. После checks/start проверить историю и все targets.
Не копировать поверх работающей SQLite/TSDB и не использовать `down -v`.

Перед изменением config: защищённая копия в backups/config-UTC (dirs700,
files600); такие копии создавались перед изменениями текущей сессии.
Перед upgrade — согласованный backup данных, сохранить прежние tags/digests,
изучить миграции, заменить на проверенные версии, config/promtool checks,
`docker compose pull && docker compose up -d`, runtime checks.
Rollback — старые config И совместимые данные: миграция Kuma/Grafana может
не поддерживать простой возврат старого образа к новой БД. Production-тома
ruk/talk не трогать. Git remote: git@github.com:aartmoon/homeserver-analytics.git.
В Git только source/config/docs/tests, без secrets/.env/data/backups.

После Go-переноса backup script также включает Go cmd/internal/go.mod/go.sum,
user units и serverbot/ с бинарником, launcher, .env и online SQLite snapshot
очереди. Архив содержит Telegram token, нужен тот же режим600 и offsite encryption.
Перед восстановлением inbox остановить единственную службу бота; не копировать
SQLite поверх работающего Go. Новый snapshot-фрагмент проверен отдельно через
integrity_check; полный archive restore по-прежнему не заявляется проверенным.
На новом хосте бинарники можно пересобрать scripts/build-go.sh из включённых
исходников (Go≥1.24+gcc), не создавать второй polling process.

## Новый сайт и Telegram

Добавить реальные HTTPS targets по templates/project.json в
prometheus/targets/http.json; file_sd перечитает автоматически. Для текста
добавить blackbox module и перезапустить только blackbox. Проверить probe_success,
status/labels, затем добавить Kuma monitor вручную. Endpoint сначала найти в
исходниках/конфигурации, не угадывать. Общие ресурсы оставлять infrastructure.

Telegram-бот перенесён на Go (cmd/serverbot и internal/serverbot). Сохранены
один polling, прежний token/owner/proxy, bot.lock и owner-only/private handlers.
Существующий ~/serverbot/bot.py теперь немедленно exec-заменяется Go binary;
постоянного Python процесса нет, корневая systemd-служба сохранена. Webhook только
127.0.0.1:9088, доступен Grafana в host network, firewall не менялся. Кумa не
уведомляет для тех же проблем. Детали и инструкции отключения:
[integrations/serverbot/README.md](integrations/serverbot/README.md).

В Git находятся Go source, go.mod/go.sum, exec launcher, Go tests и env examples
без токенов. Старый Python source сохранён в integrations/serverbot/legacy/
для совместимости/отката. Сборка: scripts/build-go.sh (Go≥1.24 + gcc). В ~/serverbot/.env включён флаг,
но этот настоящий файл и секреты в Git не попадают. Подтверждена реальная цепочка
Grafana→webhook→старый Bot→Telegram: тестовая проблема и восстановление,
а также настоящее BackupStatusUnknown. Доставлено Telegram, прочтение человеком
не проверяется. scripts/test-telegram-runtime.py отправляет реальные сообщения
только при явном запуске; обычный test-alert-runtime.py временно глушит test project.

После нового clone сначала `python3 scripts/init-local.py`: создаёт только
отсутствующие локальные секреты и каталоги, не печатает значения и не меняет
existing пароли. Затем отдельно применить PG роль/пароль и интеграцию бота по
инструкции; скрипт не развёртывает БД/бота. Для обновления текущей ВМ сохранять
существующие secrets/data/backups/serverbot inbox и не запускать второй polling.
Хостовые user systemd units сохранены в deploy/systemd/user/.

## Экономия после Go-переноса

13 Go tests с race detector и go vet прошли. Реальный collector API trial,
экспорт метрик через promtool и 13/13 UP проверены. Go-бот повторно прошёл
реальный Grafana→webhook→Telegram test problem/recovery с прежним inbox.

| Измерение | Python | Go |
|---|---:|---:|
| Bot process RSS | 178.86MiB | 15.48MiB |
| Bot service memory.current | 164.64MiB | 6.24–6.25MiB |
| Collector oneshot peak | 30.91MiB | около5.53MiB |
| Collector CPU/запуск | 136.53ms | около18.51ms |

RSS бота снизился на91.3%. Это три короткие пробы после настоящей отправки,
не длительный stress test; RSS и cgroup memory считаются по-разному, сравнивать
нужно одинаковые строки. Grafana/Kuma/Prometheus/exporters сохранены и продолжают
потреблять свои ресурсы. Go soft memory limits: bot64MiB, collector32MiB;
это не жёсткий RSS лимит. Bin-файлы не коммитятся, исходники и зависимости в Git.
Подробности, сборка, особенности exec launcher и откат:
reports/GO-MIGRATION.md и integrations/serverbot/README.md.

## Ограничения

При отключении ВМ/электричества/домашнего интернета мониторинг тоже исчезнет.
Проверка домена с домашней ВМ не заменяет внешний probe: DNS/hairpin NAT/путь
провайдера могут отличаться. Независимая внешняя проверка здесь не создавалась.
Состояние BackupUnknown и отсутствие истории Kuma — известные незавершённые части,
не скрытые успешными значениями. Дополнительные проекты в домашнем каталоге
с работающими Compose-приложениями кроме ruk/talk/serverbot не обнаружены.

Документация: https://prometheus.io/docs/guides/node-exporter/ ;
https://prometheus.io/docs/guides/multi-target-exporter/ ;
https://github.com/prometheus/blackbox_exporter/blob/master/CONFIGURATION.md ;
https://grafana.com/docs/grafana/latest/administration/provisioning/ ;
https://grafana.com/docs/grafana/latest/alerting/set-up/provision-alerting-resources/file-provisioning/ ;
https://doc.traefik.io/traefik/reference/install-configuration/observability/metrics/ ;
https://github.com/prometheus-community/postgres_exporter ;
https://github.com/louislam/uptime-kuma/wiki/Internal-API .
