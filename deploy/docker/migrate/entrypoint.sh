#!/bin/sh
set -e

# Проверяем наличие файла секрета
if [ -f /run/secrets/db_password ]; then
  # Читаем пароль из секрета
  DB_PASSWORD=$(cat /run/secrets/db_password)
else
  echo "Error: Secret file /run/secrets/db_password not found!"
  exit 1
fi

# Формируем DSN (пароль теперь простой, кодирование не нужно)
DSN="postgres://${DB_USER:-postgres}:${DB_PASSWORD}@${DB_HOST:-postgres}:${DB_PORT:-5432}/${DB_NAME:-novel_db}?sslmode=${DB_SSL_MODE:-disable}"

echo "Running migrations with DSN: postgres://${DB_USER:-postgres}:********@${DB_HOST:-postgres}:${DB_PORT:-5432}/${DB_NAME:-novel_db}?sslmode=${DB_SSL_MODE:-disable}"

# Запускаем миграцию, передавая DSN напрямую
migrate -path /migrations -database "$DSN" up 2>&1

EXIT_CODE=$?
if [ $EXIT_CODE -ne 0 ]; then
  echo "Migration failed with exit code $EXIT_CODE"
fi
exit $EXIT_CODE 