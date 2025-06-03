# --- Стадия сборки ---
FROM golang:1.24-alpine AS builder

# Игнорируем go.work внутри сборки
ENV GOWORK=off

# Устанавливаем рабочую директорию
WORKDIR /app

# Копируем go.mod и go.sum для gameplay-service
COPY gameplay-service/go.mod gameplay-service/go.sum ./gameplay-service/

# Копируем go.mod и go.sum для shared модуля
COPY shared/go.mod shared/go.sum ./shared/

# Загружаем зависимости для gameplay-service
WORKDIR /app/gameplay-service
RUN go mod download

# Возвращаемся в корень
WORKDIR /app

# Копируем исходный код shared модуля
COPY shared/ ./shared/

# Копируем исходный код gameplay-service
COPY gameplay-service/ ./gameplay-service/

# Собираем приложение gameplay-service
WORKDIR /app/gameplay-service
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /gameplay-service ./cmd/server/main.go

# --- Финальная стадия ---
FROM alpine:latest

# Устанавливаем корневые сертификаты (на всякий случай)
RUN apk --no-cache add ca-certificates wget \
    && addgroup -S app && adduser -S app -G app

WORKDIR /app

# Копируем собранный бинарник из стадии сборки
COPY --from=builder --chown=app:app /gameplay-service .

USER app

# Порт, который будет слушать сервис (для информации)
# EXPOSE 8082 - порт определяется переменной окружения

# Команда для запуска приложения
ENTRYPOINT ["/app/gameplay-service"]
