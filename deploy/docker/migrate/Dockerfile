# Используем образ с migrate CLI
FROM migrate/migrate:v4.17.1

# Копируем только скрипты миграций
COPY ./shared/database/migrations /migrations

WORKDIR /

# Точка входа не нужна, команда будет передаваться через docker-compose 