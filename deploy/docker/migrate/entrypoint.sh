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

# --- Добавляем цикл ожидания ---
MAX_WAIT=30
CURRENT_WAIT=0
DB_HOST_CHECK=${DB_HOST:-postgres}
DB_PORT_CHECK=${DB_PORT:-5432}

echo "Waiting for database host '$DB_HOST_CHECK' on port '$DB_PORT_CHECK' to be available (max $MAX_WAIT seconds)..."
while ! nc -z "$DB_HOST_CHECK" "$DB_PORT_CHECK" > /dev/null 2>&1; do
  CURRENT_WAIT=$((CURRENT_WAIT + 1))
  if [ $CURRENT_WAIT -ge $MAX_WAIT ]; then
    echo "Error: Timed out waiting for database connection."
    exit 1
  fi
  echo "Database unavailable, retrying in 1 second... ($CURRENT_WAIT/$MAX_WAIT)"
  sleep 1
done
echo "Database is available!"
# --- Конец цикла ожидания ---

echo "Running migrations with DSN: postgres://${DB_USER:-postgres}:********@${DB_HOST:-postgres}:${DB_PORT:-5432}/${DB_NAME:-novel_db}?sslmode=${DB_SSL_MODE:-disable}"

# Запускаем миграцию, передавая DSN напрямую
migrate -path /migrations -database "$DSN" up 2>&1

EXIT_CODE=$?
if [ $EXIT_CODE -ne 0 ]; then
  echo "Migration failed with exit code $EXIT_CODE"
fi
exit $EXIT_CODE 