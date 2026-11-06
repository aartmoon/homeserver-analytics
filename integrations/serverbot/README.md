# Алерты через существующий serverbot — включено

На этой ВМ работает один прежний serverbot.service. Его bot.py изменён, рядом
добавлен monitoring_webhooks.py; существующие Config, proxy, Bot, owner,
fcntl lock, команды и handlers сохранены. Ровно один start_polling.
Код bot.py/config.py/requirements.txt в этом каталоге — версия для развёртывания,
не второй автоматически запускаемый бот. bot.py.patch показывает точные изменения
к исходному bot.py. Токен/owner/.env/data в Git отсутствуют.

## Текущая связь

Grafana работает в host network. Webhook бота слушает только127.0.0.1:9088;
URL http://127.0.0.1:9088/grafana. Bearer secret читается из локального файла.
Порт не опубликован в Docker/Traefik и не слушает LAN. Firewall не менялся.
Kuma уведомления выключены, чтобы не повторять те же HTTP/TLS alerts.
Маршрут /kuma реализован и имеет отдельный секрет, но Kuma в bridge network
сейчас не может обратиться к host loopback. Не включать его вслепую.
Если понадобятся отдельные Kuma-only проверки: либо согласовать private bridge
bind+INPUT firewall, либо отдельно спроектировать локальную связь. Не публиковать
webhook в интернет. Текущая Grafana-доставка не требует Docker bridge→host доступа.

## Локальные переменные

В ~/serverbot/.env (600), реальные значения не публикуются:

```dotenv
MONITORING_WEBHOOKS_ENABLED=1
MONITORING_WEBHOOK_HOST=127.0.0.1
MONITORING_WEBHOOK_PORT=9088
MONITORING_GRAFANA_SECRET_FILE=/home/sysadmin/monitoring/secrets/grafana_webhook
MONITORING_KUMA_SECRET_FILE=/home/sysadmin/monitoring/secrets/kuma_webhook
```

Существующие TG_BOT_TOKEN, TG_ALLOWED_ID, TG_PROXY сохранены.
Шаблон .env.example безопасно отключает feature для нового развёртывания.
Новые разные секреты созданы локально, файлы600/каталог700. Grafana получает
только webhook secret через secrets/grafana-webhook.env (600), env_file Compose.
В tracked provisioning используется `$SERVERBOT_GRAFANA_WEBHOOK_SECRET`, а не
значение секрета; Grafana шифрует credential в своей БД.

## Повторное применение на этом/другом хосте

Сначала защищённый backup старых bot.py/config.py/.env/data. Не перезаписывать
существующие команды/handlers чужой версии: проверить bot.py.patch через
`patch -p1 --dry-run` в каталоге serverbot. Затем применить патч и скопировать
monitoring_webhooks.py рядом с bot.py. Полный bot.py здесь — эталон именно этой
версии. Config и requirements не менялись; aiogram3/aiohttp уже установлены.
Добавить только MONITORING_* env, проверить py_compile и mock tests.

Перезапустить существующий serverbot.service. Здесь sudo без пароля недоступен:
был отправлен SIGTERM именно прежнему MainPID пользователя sysadmin, и его
уже существующая Restart=always-служба сама запустила новую версию через5s.
Вручную второй `python bot.py` параллельно не запускать. После restart проверить
один MainPID, lock и listener127.0.0.1:9088. AiohttpSession и Config.proxy остаются
прежними. Webhook стартует до set_my_commands; обновление команд ограничено10s,
так что ошибка Telegram при старте не блокирует очередь навсегда.
`close_bot_session=False` у polling сохраняет общую session до bridge.close();
внешний async with Bot закрывает её последней.

## Grafana

Provisioning: serverbot.yaml и notifications.yaml. Receiver serverbot,
POST http://127.0.0.1:9088/grafana, Authorization Bearer, стандартный JSON,
recovery включено. Group by grafana_folder/alertname/project; wait30s,
interval5m, repeat4h. Постоянный mute снят; delivery-disabled остаётся только
неиспользуемым mute timing для экстренного отключения.

Экстренно выключить все отправки, сохранив вычисление: в policy добавить
catch-all child route receiver=serverbot с mute_time_intervals=[delivery-disabled],
reload provisioning (или применить silence в UI). Для полного отключения
listener выставить MONITORING_WEBHOOKS_ENABLED=0 и перезапустить только бота;
сначала приглушить receiver, иначе Grafana будет повторять недоставленный webhook.
Не удалять SQLite inbox. Для включения вернуть флаг и снять mute.
Не тестировать contact point кнопкой без project: такой TestAlert будет400.
Использовать scripts/test-telegram-runtime.py с корректными labels.

## Доставка и ограничения

Принимаются только JSON POST с правильным секретом. Ограничение64KiB,
чтение тела5s, до100 alerts/request. Неверный secret401, JSON400, media type415,
слишком большой запрос413. Invalid arbitrary labels не попадают в сообщения.
Grafana-generated DatasourceError/NoData без project относятся к infrastructure;
для прочих alerts project обязателен. Сообщение: проблема/восстановление,
проект, сервис, экземпляр, имя проверки и уровень. Только существующему owner;
пользовательские команды по-прежнему owner-only/private-chat.

202 возвращается только после commit SQLite WAL/synchronous FULL в существующем
~/serverbot/data/monitoring-inbox.sqlite3 (600, data700). До1000 pending,
до10000 строк суммарно. Заполненная/недоступная БД→503 Retry-After60.
Telegram недоступен: запись остаётся pending, timeout отправки30s, exponential
retry до1h. Ошибки SDK/request body/secret не логируются. SQLite transient error
не убивает worker, повтор через5s. При shutdown вход закрывается, worker получает
до5s, незавершённое остаётся в БД. Второй Bot/session/polling не создаётся.

Дедупликация7дней: Grafana fingerprint+status+startsAt (+endsAt для recovery);
Kuma project/service/status/time. Повтор одной и той же доставки не дублирует
сообщение. Повторные firing напоминания Grafana с тем же startsAt тоже будут
подавлены в течение retention; новая проблема после recovery имеет новый startsAt.
При аварии между успешным Telegram send и фиксацией delivered возможен дубль:
это at-least-once, у Telegram sendMessage нет idempotency key. После7дней
гарантии дедупликации нет. Не удалять inbox при обновлениях.

## Проверки

13 mock tests прошли: очереди/retry/dedup/reopen, HTTP400/401/413/202,
owner/private handlers и callbacks, collector. Реальных Telegram calls в unit
тестах нет. Отдельный scripts/test-telegram-runtime.py действительно отправил
тестовую проблему и восстановление через Grafana→webhook→тот же Bot; delivered
зафиксирован после успешного ответа Telegram. Был также доставлен настоящий
BackupStatusUnknown — регулярные backup пока не подтверждены.
Это проверяет приём Telegram, а не прочтение владельцем.

Тестовый runtime script отправляет реальные сообщения: запускать явно,
не добавлять в CI. Скрипт создаёт отдельное правило/fast test route, проверяет
firing/recovery и durable delivered, затем удаляет временное правило/gauge/route.
Production не останавливается. Локальный отчёт reports/runtime-telegram-test.json
игнорируется Git. Для чтения статуса очереди использовать sqlite read-only,
не изменять delivered вручную.
