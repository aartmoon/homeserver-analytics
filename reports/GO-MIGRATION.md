# Go migration: bot + host collector, 2026-11-06

По выбранному пользователем варианту сохранены Grafana/Kuma/Prometheus и все
exporters/дашборды/правила. Перенесены два своих runtime компонента: Telegram-бот
и минутный сборщик Docker. Сайты, Traefik, PostgreSQL, MinIO, Talk и контейнеры
мониторинга в этой миграции не перезапускались.

## Реально запущено

Существующая serverbot.service имеет один MainPID, executable теперь
/home/sysadmin/serverbot/serverbot-go. Token/owner/proxy/.env/data/bot.lock сохранены.
Старый корневой unit не менялся: маленький bot.py немедленно exec-заменяет Python
на Go с тем же PID. Поэтому systemctl ExecStart всё ещё ссылается на прежнюю
.venv/bin/python, но в памяти работает Go executable. .venv сохранена для запуска
обёртки и отката; отдельного Python процесса/polling нет. Подготовлен необязательный
пример прямого Go ExecStart для администратора, не установлен как второй unit.

User timer collector теперь запускает ~/monitoring/bin/container-collector.
Прямой локальный Docker HTTP Unix API, только GET; subprocess Docker/Python
удалены из активного пути. Сокет не передавался в контейнеры. Прежние JSON state,
labels, metric names и restart/OOM totals сохранены. CPU gauge теперь усредняется
по разнице накопленного CPU usage между сборами (первый baseline0).

## Ресурсы до и после

| Измерение | Python | Go |
|---|---:|---:|
| Bot RSS (/proc/MainPID/status) | 178.86MiB | 15.48MiB |
| Bot service memory.current | 164.64MiB | 6.24–6.25MiB |
| Collector peak/запуск | 30.91MiB | около5.53MiB |
| Collector CPU/запуск | 136.53ms | около18.51ms |

RSS бота ниже на91.3%, около163MiB освобождено по этой метрике.
RSS и cgroup memory учитывают shared/file pages по-разному: сравниваются только
одинаковые строки. После — три короткие пробы ПОСЛЕ реальной Telegram отправки,
а не долгий stress test. Сборщик — одно наблюдаемое oneshot выполнение.
Исходные runtime samples в локальных ignored reports/runtime-go-before/after.json.
Grafana/Kuma/Prometheus продолжают потреблять собственную RAM; их перенос или
упрощение не выполнялись. GOMEMLIMIT bot64MiB/collector32MiB — soft GC пределы,
а не жёсткие ограничения RSS.

## Совместимость и проверки

- 13 Go tests с -race: PASS. go vet: PASS.
- Legacy Python ID fixture совпадает побайтно: SHA256 Python json.dumps list,
  включая spaces. Existing events schema не конвертировалась и не удалялась.
  Добавлена только bot_state для durable getUpdates offset; старый Python её игнорирует.
- Durable commit before202, capacity/dedup/retry/reopen/cancel, HTTP401/400/413/202,
  owner/private message+callback handling, polling offset и single-instance lock
  проверены с mock Telegram API, без реальных сообщений в unit tests.
- Пробный Go collector с реальным Docker API запускался в отдельном temp root:
  все15 контейнеров UP, restart/OOM counters из прежнего state сохранены.
- После переключения Go textfile прошёл promtool metrics check; Node Exporter
  читает новые данные, Prometheus13/13 UP, dashboards возвращают реальные samples.
- Сквозной тест Grafana→Go webhook→тот же Telegram account: test problem и
  recovery действительно приняты Telegram API; inbox delivered установлен после
  успешного send. Test rule/gauge/fast route удалены. Прочтение человеком не проверяется.
- SQLite integrity_check=ok; 3 старые delivered строки сохранены, две новые
  тестовые доставки добавлены, pending0 на проверке.
- Webhook остался только127.0.0.1:9088, bearer secrets прежние, firewall не менялся.

Read-only сравнение текущего Alertmanager API с retained legacy deliveries не
нашло точного active payload для live replay. Поэтому live replay старого event
не заявляется: совместимость ID проверена fixture+сохранённой SQLite схемой/строками.

## Изменения и защита данных

До замены созданы backups/go-UTC (700, файлы600): старый bot.py/module/config/.env,
consistent SQLite backup, collector JSON state и прежний user unit.
После проверки бинарников установлен только launcher+serverbot-go на месте
существующего бота и заменён ExecStart пользовательского collector unit.
Корневой бот перезапущен штатным SIGTERM MainPID + Restart=always, без второй
polling службы. Обновлённые user units/source/build docs сохранены в Git.

Новый код: cmd/, internal/, go.mod/go.sum, scripts/build-go.sh.
Зависимость: pinned github.com/mattn/go-sqlite3 v1.14.53 (CGO, gcc; SQLite bundled).
Telegram/HTTP/SOCKS5/locking/collector используют стандартную Go библиотеку.
Сборка проверена Go1.26.8 linux/amd64. Бинарники bin/ и установленный исполняемый
файл, секреты, .env, inbox/тома, runtime samples, backups в Git не добавляются.

Legacy Python source перенесён в integrations/serverbot/legacy/ для отката.
Операционные provisioning/test/backup scripts частично остаются на Python —
они запускаются вручную и не являются постоянным bot/collector runtime.
Backup script дополнен Go source/build files и consistent online snapshot inbox
с текущим launcher/бинарником/.env; такой архив содержит Telegram token и должен
оставаться600, offsite только зашифрованно. Новая SQLite snapshot часть проверяется
отдельно, полный восстановительный прогон архива остаётся незавершённым.

## Ограничения и откат

At-least-once и подавление одинаковых firing в течение7дней сохранены; редкий
дубль после crash между Telegram send и commit возможен. Docker event buffer
ограничен: длительная остановка collector/daemon может потерять исторические OOM.
Существующие незавершённые Kuma manual setup, app health deadline/liveness и
подтверждённые регулярные backup jobs сайтов остаются задачами из README.

Rollback без трогания сайтов: вернуть protected старый bot.py в ~/serverbot,
SIGTERM текущему MainPID (legacy module/config/.venv остались); вернуть прежний
collector user unit и daemon-reload/start timer. SQLite/collector state совместимы,
поэтому обычно не восстанавливать устаревшие данные поверх текущих — это может
повторно доставить уже отправленные события или откатить счётчики.
