# --- Build Stage ---
ARG GO_VERSION=1.24 # Обновляем версию Go
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
COPY . .

# Собираем приложение story-generator (worker)
# Команда выполняется из корня /app
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /story-worker ./story-generator/cmd/worker/main.go

# --- Final Stage ---
FROM alpine:latest

# Устанавливаем корневые сертификаты (могут понадобиться для внешних API)
RUN apk --no-cache add ca-certificates

WORKDIR /app

# Копируем собранный бинарник из builder stage
COPY --from=builder /story-worker .
# ----------------------------------------------------

# Копируем .env файл, если используется godotenv (не рекомендуется для production)
# COPY .env .env

# Команда для запуска воркера
ENTRYPOINT ["/app/story-worker"]