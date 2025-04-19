# --- Стадия сборки ---
FROM golang:1.24-alpine AS builder

# Устанавливаем рабочую директорию в корень модуля
WORKDIR /app

# Копируем ТОЛЬКО корневые go.mod и go.sum
COPY go.mod go.sum ./

# Загружаем ВСЕ зависимости проекта
RUN go mod download

# Копируем ВЕСЬ исходный код проекта
COPY . .

# Собираем приложение admin-service
# Команда выполняется из корня /app
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /admin-server ./admin-service/cmd/server/main.go

# --- Этап выполнения ---
FROM alpine:latest

# Устанавливаем корневые сертификаты и wget для healthcheck
RUN apk --no-cache add ca-certificates wget

WORKDIR /app

# Копируем скомпилированный бинарник
COPY --from=builder /admin-server .

# Копируем веб-файлы (шаблоны И статику) из admin-service/web
COPY --from=builder /app/admin-service/web ./web

# --- ДОБАВЛЕНО: Копируем ОБЩИЙ 404.html --- 
# Убедимся, что директория для статики существует
RUN mkdir -p /app/web/static
# Копируем общий 404 из shared/static в /app/web/static/
COPY --from=builder /app/shared/static/404.html /app/web/static/404.html

# Порт, который будет слушать приложение (для информации)
# EXPOSE 8084 - порт определяется переменной окружения
# Порт для метрик Prometheus
EXPOSE 9094

# Команда для запуска приложения
ENTRYPOINT ["/app/admin-server"]

# Healthcheck (используем wget, который установили)
HEALTHCHECK --interval=10s --timeout=5s --start-period=30s --retries=5 CMD wget -q -O /dev/null http://localhost:${ADMIN_SERVER_PORT:-8084}/health || exit 1 