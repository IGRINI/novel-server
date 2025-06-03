# --- Build Stage ---
ARG GO_VERSION=1.24
FROM golang:${GO_VERSION}-alpine AS builder

# Игнорируем go.work внутри сборки
ENV GOWORK=off

# Устанавливаем рабочую директорию
WORKDIR /app

# Копируем go.mod и go.sum для story-generator
COPY story-generator/go.mod story-generator/go.sum ./story-generator/

# Копируем go.mod и go.sum для shared модуля
COPY shared/go.mod shared/go.sum ./shared/

# Загружаем зависимости для story-generator
WORKDIR /app/story-generator
RUN go mod download

# Возвращаемся в корень
WORKDIR /app

# Копируем исходный код shared модуля
COPY shared/ ./shared/

# Копируем исходный код story-generator
COPY story-generator/ ./story-generator/

# Собираем приложение story-generator (worker)
WORKDIR /app/story-generator
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /story-worker ./cmd/worker/main.go

# --- Final Stage ---
FROM alpine:latest

# Устанавливаем корневые сертификаты (могут понадобиться для внешних API)
RUN apk --no-cache add ca-certificates \
    && addgroup -S app && adduser -S app -G app

WORKDIR /app

# Копируем собранный бинарник из builder stage
COPY --from=builder --chown=app:app /story-worker .

USER app
# ----------------------------------------------------

# Копируем .env файл, если используется godotenv (не рекомендуется для production)
# COPY .env .env

# Команда для запуска воркера
ENTRYPOINT ["/app/story-worker"]