# Образ для нагрузочного тестирования: grafana/k6 + postgresql-client.
# postgresql-client нужен для засева данных и разрешения UUID прямо
# внутри Docker-сети перед запуском тестов.
#
# Точка входа переопределена на /tests/stress/run-all.sh,
# который монтируется как том из ./tests/stress.
FROM grafana/k6:latest

USER root
RUN apk add --no-cache postgresql-client

# Точка входа — оркестратор тестов (монтируется через volume).
# Явно вызываем /bin/sh, поэтому execute-бит файла не нужен.
ENTRYPOINT ["/bin/sh", "/tests/stress/run-all.sh"]
