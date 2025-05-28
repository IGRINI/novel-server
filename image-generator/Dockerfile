# --- Build Stage ---
ARG GO_VERSION=1.24
FROM golang:${GO_VERSION}-alpine AS builder

# Игнорируем go.work внутри сборки
ENV GOWORK=off

# Устанавливаем рабочую директорию в корень модуля
WORKDIR /app

# Копируем ТОЛЬКО корневые go.mod и go.sum
COPY go.mod go.sum ./

# Загружаем ВСЕ зависимости проекта
RUN go mod download

# Копируем ВЕСЬ исходный код проекта
# Это не самый оптимальный вариант с точки зрения кеширования Docker,
# но для монорепозитория это часто самый простой подход.
COPY . .

# Собираем приложение image-generator (worker)
# Команда выполняется из корня /app
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /image-worker ./image-generator/cmd/worker/main.go

# --- Final Stage ---
FROM alpine:latest

# Устанавливаем корневые сертификаты (могут понадобиться для HTTPS к SANA серверу или S3)
RUN apk --no-cache add ca-certificates \
    && addgroup -S app && adduser -S app -G app

WORKDIR /app

# Копируем собранный бинарник из builder stage
COPY --from=builder --chown=app:app /image-worker .

RUN mkdir -p /app/generated_images && chown app:app /app/generated_images

USER app

# Копируем .env файл, если используется godotenv (не рекомендуется для production)
# COPY .env .env

# Команда для запуска воркера
ENTRYPOINT ["/app/image-worker"] 