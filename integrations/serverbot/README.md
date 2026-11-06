# Telegram-бот в Docker

Активный Go source: cmd/serverbot + internal/serverbot. Запуск — service serverbot
в корневом compose.yaml; отдельной systemd-службы, Python launcher и .venv нет.
Старый ~/serverbot удалён, очередь перенесена в ~/monitoring/data/serverbot.
Один прежний account и polling, token/owner/proxy сохранены. Docker restart
возобновляет тот же inbox; bot.lock защищает от параллельного экземпляра при
общем data mount. Не запускать другой polling с тем же Telegram token.

Private secrets/serverbot.env600 содержит TG_BOT_TOKEN/TG_ALLOWED_ID/TG_PROXY,
MONITORING_*; .env.example — пустой шаблон. Compose передаёт env_file и secret
files /run/secrets/grafana_webhook,/run/secrets/kuma_webhook. Секреты не в Git.
Host network даёт доступ к старому SOCKS5 localhost1080 и webhook127.0.0.1:9088.
Наружу port/router не открываются, firewall не менялся. Bot UID1000, cap_drop ALL,
no-new-privileges, readonly rootfs,64MiB RAM budget. Для host status — только
3 /proc-файла и statfs пустого каталога того же диска, без всего root хоста.

Grafana receiver serverbot: POST http://127.0.0.1:9088/grafana, Bearer credential
через env interpolation $SERVERBOT_GRAFANA_WEBHOOK_SECRET. Recovery включено,
policy wait30s/interval5m/repeat4h. delivery-disabled существует, сейчас не активен;
для экстренного mute добавить его к catch-all child route, reload provisioning.
Для отключения bot сначала заглушить sender, затем docker compose stop serverbot.
Kuma notifications off во избежание дублей. Reserved /kuma имеет отдельный secret,
но bridge-контейнер Kuma не достигает host loopback: не включать вслепую.

Сообщения/кнопки статуса по-прежнему только владельцу в private chat; unauthorized
callback получает отказ без метрик. Один HTTP transport обслуживает polling,
ответы и alerts. Durable getUpdates offset хранится в SQLite bot_state.

Webhook: JSON64KiB, headers8KiB, read5s, DB5s. Secret401, JSON400, oversized413,
media415, storage/capacity503 Retry-After60. До1000 pending и10000 rows.
202 только после SQLite WAL/synchronous FULL commit; data700/DB600. Worker30s
send timeout, retry сохраняет pending, exponential до1h и Telegram retry_after.
Shutdown прекращает HTTP/polling/worker, незавершённое остаётся в БД.
Healthcheck GET /healthz проверяет inbox; не считает доступность Telegram условием
готовности приёма, потому что события должны сохраняться и во время его отказа.

Старые события/ID совместимы. Dedup7дней fingerprint/status/startsAt/endsAt,
включая firing reminders одного incident. Recovery/new incident принимаются
отдельно. Между Telegram send и DB commit crash может дать дубль: at-least-once.
Raw error URLs/тела запросов/credentials не логируются.

Обновление: docker compose build serverbot; docker compose up -d serverbot.
Тесты Go/mock API не посылают настоящих сообщений. Явный сквозной тест
scripts/test-telegram-runtime.py отправляет problem/recovery через Grafana и
проверяет delivered после Telegram API success, не прочтение человеком.
Он уже прошёл для контейнерной версии; временные объекты удалены.
При restore остановить container, вернуть согласованный inbox, права1000/600,
затем запуск. Не обнулять delivered/offset ради зелёного статуса.
