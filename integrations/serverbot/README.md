# Serverbot и сборщик переведены на Go

Активный код: cmd/serverbot, internal/serverbot, cmd/container-collector,
internal/collector. Prometheus/Grafana/Kuma/exporters сохранены. Старый Python
код находится в legacy/ для отката и compatibility tests; он не исполняется
в работающем боте. Подробный результат: reports/GO-MIGRATION.md.

## Что работает сейчас

Существующий serverbot.service запускает тот же Telegram-бот с прежними
TG_BOT_TOKEN/TG_ALLOWED_ID/TG_PROXY, data и bot.lock. Один polling getUpdates,
кнопка «📊 Состояние сервера», ответы на сообщения владельца в private chat,
owner-only callback handlers. Русский текст метрик сохранён. Один HTTP client/
transport разделяют polling, ответы и alert delivery; существующий SOCKS5 proxy
поддерживается стандартным net/http. Raw HTTP/SDK errors с token URL не логируются.

Webhook только127.0.0.1:9088, /grafana и /kuma с разными Bearer secrets.
Grafana уже в host network, отправляет на http://127.0.0.1:9088/grafana.
Kuma notifications выключены для предотвращения дублей; bridge-контейнер Kuma
не имеет доступа к host loopback. Публичного webhook/Traefik router нет,
firewall не менялся. Для будущих Kuma-only alerts отдельно согласовать приватную
связь, не открывать webhook в интернет.

## Почему systemctl ExecStart по-прежнему показывает Python

Корневая служба управляется администратором; sudo без пароля недоступен.
В ~/serverbot/bot.py установлена короткая exec-go.py: она немедленно делает
os.execv ~/serverbot/serverbot-go. PID сохраняется, Python интерпретатор полностью
заменяется Go executable. Постоянного Python-процесса/второго polling нет.
Проверка: /proc/$(systemctl show serverbot -p MainPID --value)/exe.
.venv сохранена для совместимости запуска службы и старых тестов/отката.
Не удалять её, пока администратор не сменит ExecStart.

Для прямого запуска без обёртки подготовлен deploy/systemd/serverbot-go.service.example.
Применять к той же serverbot.service с daemon-reload, НЕ создавать вторую службу.
Это необязательный административный шаг: экономия RAM уже получена через exec.

## Сборка и обновление

Нужны Go≥1.24 и gcc (SQLite встроен в CGO driver, отдельный sqlite dev package
не нужен). Сборка проверена Go1.26.8 на Ubuntu amd64. Версия go-sqlite3 закреплена
в go.mod/go.sum: v1.14.53. Telegram SDK не добавлялся.

```bash
cd ~/monitoring
go test -race ./...
go vet ./...
./scripts/build-go.sh
./bin/serverbot-go --root ~/serverbot --check-config
```

Если системный HTTP_PROXY мешает скачиванию Go modules, выполнить сборку в
обычном окружении с работающей сетью; на этой ВМ для первичного скачивания
использовалось HTTP_PROXY= HTTPS_PROXY= ALL_PROXY=. Не менять proxy бота.

Перед установкой сохранить bot.py, .env и consistent SQLite backup в каталоге700,
файлы600. Установить bin/serverbot-go как ~/serverbot/serverbot-go через временный
файл+rename, права755; exec-go.py как ~/serverbot/bot.py. Если bot.py отличается
от известной legacy-версии, сначала объединить собственные handlers, не заменять
их вслепую. Здесь исходный обработчик перенесён полностью и проверен mock API.
Go-launcher.patch показывает точное изменение Python entrypoint.

Перезапустить только существующий bot service. На этой ВМ: SIGTERM прежнему
MainPID пользователя sysadmin; уже существующая Restart=always-служба запускает
новую версию через5s. Не выполнять второй `python bot.py`/Go polling параллельно.
После обновления проверить exe, один MainPID, loopback listener и Telegram test.

Для collector установить bin/container-collector в ~/monitoring/bin/;
user unit из deploy/systemd/user/ вызывает Go каждые60s. Он обращается к Docker
по локальному Unix socket, только GET /version,/containers/json,/json,/stats,/events,
без subprocess docker/Python и без socket mounts в контейнеры. Состояние и metric
names прежние. CPU теперь среднее число занятых ядер между двумя успешными
сборами, первая выборка без baseline0; memory — usage минус inactive file cache.
При смене container ID CPU baseline сбрасывается, restart/OOM totals сохраняются.

## Env и секреты

Настоящий ~/serverbot/.env600 сохранён, в Git только .env.example.
MONITORING_WEBHOOKS_ENABLED=1, MONITORING_WEBHOOK_HOST=127.0.0.1,
MONITORING_WEBHOOK_PORT=9088. Файлы MONITORING_GRAFANA_SECRET_FILE и
MONITORING_KUMA_SECRET_FILE указывают на secrets/grafana_webhook и kuma_webhook.
Секреты минимум32 символа, разные, mode600; прежние значения не менялись.
Service environment имеет приоритет над .env, значения из файла не исполняются.
TG_PROXY отсутствует→socks5://127.0.0.1:1080, явное пустое значение→без proxy.

Grafana env_file secrets/grafana-webhook.env600; tracked serverbot.yaml использует
$SERVERBOT_GRAFANA_WEBHOOK_SECRET. Notifications policy receiver serverbot,
recovery включено, group30s/5m/repeat4h. delivery-disabled — запасной mute timing,
сейчас не применяется. Для экстренного mute добавить catch-all child route
receiver serverbot с mute_time_intervals=[delivery-disabled], reload provisioning.
Отключать listener через env flag только после mute sender.

## Очередь и совместимость

Старый ~/serverbot/data/monitoring-inbox.sqlite3 используется на месте. events
schema, тексты и SHA256 ключи совместимы с Python json.dumps(list), включая
пробелы после запятых. SQLite WAL + synchronous FULL, file600, data700.
Добавлена только таблица bot_state для durable Telegram update offset;
старый Python её игнорирует. После миграции integrity_check=ok, старые deliveries
сохранены; секреты/owner/.env не менялись.

202 только ПОСЛЕ commit. До1000 pending, до10000 строк. Secret401, media415,
JSON400, >64KiB413, capacity/storage error503 Retry-After60. Чтение5s, header8KiB,
DB timeout5s. Только private/loopback binding, wildcard/public IP отвергаются.
Worker send timeout30s; ошибка оставляет pending с exponential retry до1h,
Telegram retry_after учитывается, даже если больше базовой задержки.
Shutdown закрывает HTTP, отменяет единственный polling/worker и закрывает SQLite.
Незавершённая отправка остаётся pending. Новый poll offset фиксируется после
обработки update; unauthorized messages не вызывают отправку метрик.

Дедупликация7дней: Grafana fingerprint/status/startsAt/endsAt; Kuma project/
service/status/time. Повторные firing напоминания того же инцидента тоже подавлены.
Новая проблема/recovery — отдельный ключ. Между успешным Telegram send и commit
возможен дубль после аварии: at-least-once, sendMessage не имеет idempotency key.
Это ограничение сохранено, exactly-once не заявляется.

## Проверки и откат

Go tests -race: 13 тестов очереди/legacy ID/schema, reopen/offset, bounded/retry/
cancel, HTTP/auth/size, owner/private polling/callbacks, lock/status, config/
private bind, Docker API/counter dedup/recreation/CPU и отказ сохранения метрик.
Go vet прошёл. Пробный collector с реальным Docker проверен в отдельном temp root.
Сквозной Grafana→Go→Telegram test действительно отправил test problem и recovery;
это подтверждение Telegram API, не прочтения человеком. Runtime script отправляет
реальные сообщения только при явном запуске, не включать его в CI.

Для rollback: сохранить текущее состояние, восстановить прежний ~/serverbot/bot.py
из protected backups/go-UTC (legacy module/config и .venv остались на месте),
SIGTERM текущему MainPID. Для collector вернуть сохранённую user unit на Python,
daemon-reload и запуск timer. Очередь и collector state совместимы: не откатывать
их к старой копии без причины, иначе возможно повторное получение сообщений/
счётчиков. Текущие файлы/тома сайтов при откате не трогать.
