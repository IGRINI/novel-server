# Этап 1: Сборка приложения
FROM golang:1.24-alpine AS builder

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

# Собираем приложение auth
# Команда выполняется из корня /app
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /auth-service ./auth/cmd/auth/main.go


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