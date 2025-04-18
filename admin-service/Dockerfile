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

# Копируем шаблоны HTML из исходников в директорию web внутри /app
COPY --from=builder /app/admin-service/web ./web

# Порт, который будет слушать приложение (для информации)
# EXPOSE 8084 - порт определяется переменной окружения

# Команда для запуска приложения
ENTRYPOINT ["/app/admin-server"]

# Healthcheck (используем wget, который установили)
HEALTHCHECK --interval=10s --timeout=5s --start-period=30s --retries=5 CMD wget -q -O /dev/null http://localhost:${ADMIN_SERVER_PORT:-8084}/health || exit 1 