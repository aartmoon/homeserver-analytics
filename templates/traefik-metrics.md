# Traefik — применено 2026-11-06 с явным разрешением пользователя

Существующий ~/ruk/docker-compose.yml дополнен:

```yaml
      - --entrypoints.metrics.address=:8082
      - --metrics.prometheus=true
      - --metrics.prometheus.entrypoint=metrics
      - --metrics.prometheus.addServicesLabels=true
      - --metrics.prometheus.addRoutersLabels=false
      - --metrics.prometheus.buckets=0.1,0.3,1,2.5,5
```

И портом127.0.0.1:8082:8082. До изменения проверено, что старые флаги и80/443
сохраняются. Выполнен config-q, образ загружен ДО пересоздания.
Только `docker compose up -d --no-deps traefik`; app/DB/MinIO/Talk не перезапускались.
После изменения проверены HTTPS200 магазина, каталога, readiness и Talk.
Traefik healthy, Prometheus job UP; metrics реальные. Новые сети/proxy не созданы.

Protected backup: ../backups/traefik-UTC содержит исходные Compose и
runtime-rollback.yaml с точным старым image ID/command (600). Последний содержит
приватные runtime значения: не выводить в чат/логи. Для отката выбрать snapshot,
восстановить исходный docker-compose.yml после копии текущего, проверить config;
пересоздать только Traefik в согласованное окно. Тома не удалять. При изменении
image/config учитывать runtime-rollback.yaml как последний override, не теряя
порты80/443. Убрать scrape/rules Traefik, если метрики выключаются намеренно.

ruk.traefik.override.yaml — сохранённый пример, теперь не нужен для обычного
deploy: изменения находятся в основном файле. Не добавлять повторно в команды.
HTTP метрики reverse proxy: requests_total и request_duration_seconds_bucket.
Никаких URL, order ID или email labels. Project mapping проверен в Prometheus.
