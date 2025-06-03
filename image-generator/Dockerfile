# --- Build Stage ---
ARG GO_VERSION=1.24
FROM golang:${GO_VERSION}-alpine AS builder

# Игнорируем go.work внутри сборки
ENV GOWORK=off

# Устанавливаем рабочую директорию
WORKDIR /app

# Копируем go.mod и go.sum для image-generator
COPY image-generator/go.mod image-generator/go.sum ./image-generator/

# Копируем go.mod и go.sum для shared модуля
COPY shared/go.mod shared/go.sum ./shared/

# Загружаем зависимости для image-generator
WORKDIR /app/image-generator
RUN go mod download

# Возвращаемся в корень
WORKDIR /app

# Копируем исходный код shared модуля
COPY shared/ ./shared/

# Копируем исходный код image-generator
COPY image-generator/ ./image-generator/

# Собираем приложение image-generator (worker)
WORKDIR /app/image-generator
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /image-worker ./cmd/worker/main.go

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