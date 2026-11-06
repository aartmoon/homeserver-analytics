# PostgreSQL — НЕ подключён

Сначала проверить `docker network inspect` и labels существующего postgres.
Из Compose найдено имя по умолчанию ruk_backend, но runtime не проверен.
Не перезапускать и не пересоздавать postgres.

В административном psql (база rukodelie) создать роль:

```sql
CREATE ROLE monitoring_exporter LOGIN CONNECTION LIMIT 3;
GRANT CONNECT ON DATABASE rukodelie TO monitoring_exporter;
GRANT pg_monitor TO monitoring_exporter;
ALTER ROLE monitoring_exporter SET statement_timeout = '5s';
ALTER ROLE monitoring_exporter SET default_transaction_read_only = on;
```

`pg_monitor` — стандартная роль чтения статистики PostgreSQL, без superuser,
создания БД, ролей, репликации и доступа к таблицам заказов через SELECT.
Пароль назначить интерактивной командой psql `\password monitoring_exporter`;
не указывать его в командной строке. Сохранить тот же пароль локально в
secrets/postgres_password (600). Не использовать пароль приложения.

В templates/postgres.compose.yaml заменить PG_NETWORK и PG_HOST после inspect.
Exporter подключается к двум сетям: своей и только существующей сети БД.
Prometheus и Grafana не получают доступа к сети БД. Exporter слушает только в
своей сети (web.listen-address привязать к IP monitoring-сети после проверки,
если требуется исключить чтение метрик из сети backend). Порт хоста не публикуется.
Проверить `pg_up == 1`, имена pg_settings_max_connections и
pg_stat_database_numbackends до активации правил optional-rules.yaml.
Для общей БД метки project=infrastructure; не приписывать все транзакции магазину.

ОБНОВЛЕНИЕ: exporter уже запущен основным compose.yaml, роль создана,
pg_up=1 и max_connections=100 проверены. Шаблон выше — справочный, повторно
CREATE ROLE не выполнять. Runtime network ruk_backend подтверждена Docker inspect.
Prometheus использует host loopback127.0.0.1:9187; пароль600 доступен exporter
uid1000. Пользователь не имеет SELECT на таблицы public (проверено).
