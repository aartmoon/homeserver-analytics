# Включение Telegram в существующем боте

2026-11-06, по явному запросу пользователя. Созданные до этого правила теперь
посылают firing/recovery существующему owner через прежний Bot и session/proxy.

Изменено на ВМ:
- ~/serverbot/bot.py: webhook lifecycle рядом с прежним единственным polling,
  bounded set_my_commands, сохранён fcntl lock и все owner/private handlers.
- ~/serverbot/monitoring_webhooks.py: новый модуль. Исходник в integrations/serverbot/.
- ~/serverbot/.env: добавлены только MONITORING_*; оригинальные TG_* сохранены.
  .env/реальные значения в Git отсутствуют.
- monitoring Compose: Grafana читает private secrets/grafana-webhook.env.
- contact point serverbot.yaml и policy notifications.yaml: Bearer secret через
  environment interpolation; постоянный mute снят, recovery включено.

Перед изменениями protected backup создан в backups/telegram-UTC (700/600),
включая старые bot.py/config.py/.env/Compose/policy. Порт9088 слушает только
127.0.0.1, без Docker ports/Traefik/firewall изменений. Grafana уже в host network.
Маршрут Kuma подготовлен с отдельным секретом, но его контейнерные уведомления
не включены, чтобы не дублировать Grafana; bridge→loopback связи нет.

Бот перезапущен SIGTERM прежнего MainPID; его существующий Restart=always service
сам запустил новую версию. sudo без пароля не требовался. Проверен один процесс,
active/running, правильный loopback listener. Нового бота и второго polling нет.

13 mock tests PASS: owner/private commands/callbacks, durable/retry/dedup queue,
HTTP401/400/413/202, datasource fallback и container collector.
Сквозной тест отдельного alert project=test действительно прошёл:
- Grafana firing, Telegram API подтвердил отправку test problem;
- Grafana recovered, Telegram API подтвердил test recovery;
- inbox delivered записан после успешного send_message;
- test rule/metric/fast route удалены, production не останавливался.
Отдельно доставлен реальный BackupStatusUnknown; backup jobs всё ещё не подтверждены.
Чтение человеком не проверяется. Локальные временные timestamps/evidence исключены
из Git. Секреты, токены, owner ID, БД, data/тома и backup-архивы не коммитятся.

Git repository: aartmoon/homeserver-analytics. Версионируется весь код мониторинга,
provisioning, dashboards, tests, docs, host units и bot source/patch. Secrets/.env/
SQLite/runtime datasets исключены .gitignore, значения отдельно проверяются перед push.

Доставка at-least-once; редкий дубль возможен после аварии между send и commit.
Одинаковый firing инцидент подавляется по fingerprint/status/startsAt в течение7дней,
включая повторные напоминания Grafana. Recovery/new incident принимаются отдельно.
Очередь1000 pending, ошибки Telegram сохраняют событие с retry до1h, size64KiB,
секрет проверяется до приёма. 202 только после durable commit. Инструкция аварийного
mute/disable и развёртывания: integrations/serverbot/README.md.
