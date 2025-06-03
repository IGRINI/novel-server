# Этап 1: Сборка приложения
FROM golang:1.24-alpine AS builder

# Игнорируем go.work внутри сборки
ENV GOWORK=off

# Устанавливаем рабочую директорию
WORKDIR /app

# Копируем go.mod и go.sum для auth сервиса
COPY auth/go.mod auth/go.sum ./auth/

# Копируем go.mod и go.sum для shared модуля
COPY shared/go.mod shared/go.sum ./shared/

# Загружаем зависимости для auth сервиса
WORKDIR /app/auth
RUN go mod download

# Возвращаемся в корень
WORKDIR /app

# Копируем исходный код shared модуля
COPY shared/ ./shared/

# Копируем исходный код auth сервиса
COPY auth/ ./auth/

# Собираем приложение auth
WORKDIR /app/auth
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /auth-service ./cmd/auth/main.go

# Этап 2: Создание минимального образа
FROM alpine:latest

RUN apk --no-cache add ca-certificates wget \
    && addgroup -S app && adduser -S app -G app

WORKDIR /app

# Копируем бинарный файл из этапа сборки
COPY --from=builder --chown=app:app /auth-service .

USER app

# Копируем .env файл (если он не будет монтироваться как volume)
# COPY .env .env
# Копируем папку с миграциями (нужно для migrate сервиса, но auth может не требовать)
# COPY shared/database/migrations ./migrations

# Порт, который будет слушать сервис (информационно)
# EXPOSE 8081 - определяется переменной окружения

# Команда для запуска сервиса
ENTRYPOINT ["/app/auth-service"]