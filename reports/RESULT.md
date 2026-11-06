# Установка мониторинга 2026-11-06 UTC

Исторический отчёт до включения Telegram. Обновление: Telegram теперь включён
в существующем боте; актуальные изменения и проверки — TELEGRAM.md.

## 1. Создано и изменено

Создан независимый /home/sysadmin/monitoring:
- compose.yaml: 6 сервисов, официальные закреплённые tags+digests, persistent data,
  resource limits, restart и ротация логов.
- prometheus/: 13 targets, правила-эталоны и promtool unit tests;
  blackbox/: реальные HTTP/TLS/text/image модули.
- grafana/: 5 dashboards, datasource и 17 активных правил, policy с receiver empty
  и непрерывным mute (доставка выключена).
- scripts/: config/runtime проверки, безопасный runtime alert test, restart/persistence
  test, backup monitoring, status publisher реального backup, host container collector.
- integrations/serverbot/: готовый отключённый модуль, точный будущий bot.py.patch,
  env/HTTP/firewall инструкция; mock tests. Работающий бот не менялся.
- templates/: добавление проекта, PG/Traefik и будущий backup-age alert.
- README, reports и защищённые snapshots конфигураций/архивы в backups.
- secrets/: локально сгенерированные пароль Grafana и отдельный пароль PG роли,
  файлы600/каталог700, не выводились в ответ и не добавлялись в git.

Существующий ~/ruk/docker-compose.yml изменён минимально: добавлены 6 Traefik
metrics flags и порт127.0.0.1:8082:8082. Diff: reports/ruk-compose.patch.
До изменения сохранены исходные оба Compose-файла и точный runtime rollback
Traefik в backups/traefik-UTC (700/600). Git repositories для ruk/talk/serverbot
отсутствуют: git status сообщил not a git repository; пользовательские остальные
строки сохранены. ~/talk и ~/serverbot не изменены, git push не выполнялся.

Созданы user systemd units в ~/.config/systemd/user/:
monitoring-container-collector.service и .timer. Timer enabled, linger sysadmin
включён для работы без SSH-сессии. Создана PostgreSQL role monitoring_exporter
(pg_monitor, CONNECT, connection limit3, read-only/timeout defaults), без superuser,
создания ролей/БД/репликации и SELECT на public tables. Существующие роли не менялись.
Firewall/SSH/Telegram не изменялись; Docker API/socket наружу не опубликованы
и не монтируются в monitoring containers.

## 2. Реально запущено и проверено

Работают Prometheus3.16.0, Node Exporter1.12.1, Blackbox0.29.1, Grafana13.3.0,
Kuma2.5.9, postgres_exporter0.20.1. Все images реально pulled, tags закреплены digest.
12 исходных targets UP, затем подключён Traefik — итого13 UP. 7 HTTP проб успешны.
Пять dashboards получены через Grafana API; реальные datasource queries CPU,
HTTP availability, PG, UP и proxy requests rate вернули данные без ошибок.
Все17 правил Grafana имеют health=ok.

Проверки:
- docker compose config -q, включая merged production Traefik config: PASS.
- promtool config +14 reference rules + unit tests: PASS. Тестируются HTTP down/
  recovery, <15%/<5% disk и отсутствие двойного warning, RAM for15m, low-traffic
  guards 5xx/p95 и firing при достаточном числе запросов.
- Grafana datasource/dashboard/alert provisioning: API проверен, без query errors.
- Временное правило: inactive→pending→firing→inactive; два успешных прогона,
  последний в финальной loopback-сетевой схеме. Production не останавливался
  для alert test; Telegram не отправлялся. Evidence runtime-alert-test.json.
- Restart только monitoring: историческая TSDB-выборка, созданный через API
  writable dashboard и marker в Kuma volume сохранились. Тестовые объекты удалены.
  Сохранность ещё не созданных Kuma monitor settings не утверждается.
- Backup monitoring: stop/copy/start только monitoring; архив600 создан,
  tar listing проверен. Полное восстановление не тестировалось.
- 10 Python tests: PASS, mock Bot, durable/reopened inbox, retry/dedup/bounds,
  реальные локальные aiohttp HTTP400/401/413/202, OOM event dedup/restart delta.
  Синтаксис Python/Shell и будущего patched bot.py проверен.
- ss и Docker bindings: только127.0.0.1 на3001/3002/9090/9100/9115/9187/8082.
  Из отдельного Docker network namespace к LAN-IP ВМ закрыты также9088/5432/
  2375/2376. Это не внешняя проверка с другого физического устройства/интернета.

Traefik пересоздан только после явного ответа пользователя «Пересоздать Traefik
сейчас». Предварительно image pulled и сравнение показало сохранение всех старых
command flags/80/443. Проверены healthy и HTTPS200 магазина, каталога, readiness,
Talk после пересоздания. App/DB/MinIO/воркеры/Talk не перезапускались; production
тома не удалялись и не пересоздавались.

## 3. Сайты и метрики

cottontoys.ru: главная200+ожидаемый текст; /catalog200+ожидаемый текст;
/api/health200+database=ok; реальная публичная photo200 image/webp; TLS expiry.
Собранный health handler прочитан без изменений: реальный SELECT1, 503 с общим
ответом при ошибке. Явный server-side timeout и отдельная liveness отсутствуют.
Root app sources на ВМ нет; код внутри работающего контейнера не редактировался.

rp-6.ru (Talk): homepage, /healthz и /readyz — реальные маршруты из Compose,
HTTP200. Все HTTP interval60s, после3 ошибок, восстановление на следующем успехе.

Ubuntu: CPU/RAM/swap/FS/inode, настоящая сеть wlp4s0, disk IO.
PostgreSQL: pg_up1, max_connections100, per-database connections, transactions/
rollback и стандартные stats exporter. Общая БД маркирована infrastructure.
15 долгоживущих Compose containers: availability, working-set RAM, sampled CPU,
automatic restart counters и OOM events с оговорённым ограниченным event buffer.
Traefik: реальные requests_total и duration buckets; project mapping проверен:
shop/media→cottontoys, talk-api/talk-web→talk. Метрики именно reverse proxy.

Kuma работает и healthy, но администратор/7 monitors требуют ручного добавления.
Сейчас реальные автоматические HTTP проверки выполняют Blackbox+Grafana.

## 4. Фактические ресурсы

Три ранних docker stats замера 6 monitoring containers:
421.6–427.8MiB суммарной RAM, 0.39–1.00% CPU (100%=1 ядро).
Это короткое наблюдение, не steady-state нагрузочный тест; samples в
runtime-resources.json. Collector oneshot: peak≈30MiB, CPU≈154ms на один запуск/мин.
RAM limits2432MiB — лимиты, не наблюдаемое потребление.
Ранняя data usage: Prometheus968KiB, Kuma16KiB, Grafana≈545MiB (plugins≈543MiB).
Один проверенный monitoring archive≈176.6MiB, mode600; images/новые архивы отдельно.
На завершающей проверке диск /≈192GB свободно (до pull≈198GB), RAM запас большой.
TSDB retention15days/5GB, WAL/compaction и прочие данные сверх этого лимита.

## 5. Что осталось

- Создать администратора и семь мониторов Kuma по точной таблице README;
  notifications оставить off, retention30days. БД Kuma не редактировалась.
- В исходном репозитории магазина: отдельная liveness и явный короткий deadline/
  cancellation readiness, source-level tests. Текущий SELECT1 readiness уже мониторится;
  отказ PostgreSQL на production ради теста не вызывался.
- Рабочие регулярные backup jobs магазина/MinIO не подтверждены. User crontab
  отсутствует, доступные cron/timers не содержат проектных backup; root crontab
  полностью не проверен без sudo. Архивы миграции не доказывают расписание.
  Настроить реальное резервирование/offsite/restore и только после успеха
  подключить backup-success.py. Сейчас намеренно нет метрики последнего успеха;
  активен BackupStatusUnknown, age rule пока шаблон.
- Полностью испытать restore monitoring archive и организовать offsite/расписание.
- Проверить порты с другого устройства/из интернета; текущая проверка была с ВМ
  и из другого Docker namespace. Внешняя независимая проверка не создана.
- Оценить ресурсы/пороги по более долгому наблюдению.

## 6. Telegram позже

Полностью отключён: нет webhook listener/contact point/нового бота, SMTP off,
receiver empty + mute delivery-disabled. Существующий polling/lock/handlers
serverbot не менялись. Использовать integrations/serverbot/README.md и bot.py.patch:
существующий Bot/OWNER_ID/session, ровно один polling, bounded durable SQLite queue,
разные routes, bearer secrets, validation, size/time limits, retries и shutdown.
Привязка будущего webhook172.20.0.1:9088; согласовать firewall INPUT только с
monitoring subnet/bridge, сейчас правила не добавлялись. Сначала mock tests,
затем явное включение env flag и снятие continuous mute. Дедупликация at-least-once:
краш между Telegram send и DB commit может дать повтор; это документировано.
