# Этап 1: Сборка приложения
FROM golang:1.24-alpine AS builder

# Игнорируем go.work внутри сборки
ENV GOWORK=off

WORKDIR /app

# Копируем go.mod и go.sum для auth и shared
# COPY go.work go.work # go.work не нужен при GOWORK=off
COPY auth/go.mod auth/go.sum ./auth/
COPY shared/go.mod shared/go.sum ./shared/

# Копируем ВЕСЬ исходный код auth и shared
COPY auth/ ./auth/
COPY shared/ ./shared/

# Переходим в директорию auth
WORKDIR /app/auth

# Загружаем зависимости (go.mod должен содержать replace ../shared)
# Эта команда может быть опциональной, но оставим для кэширования слоев
RUN go mod download

# Собираем приложение auth
# Используем -ldflags="-s -w" для уменьшения размера
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /auth-service ./cmd/auth/main.go


# Этап 2: Создание минимального образа
FROM alpine:latest

# Устанавливаем корневые сертификаты для HTTPS/TLS (если потребуются)
RUN apk --no-cache add ca-certificates

WORKDIR /app

# Копируем бинарный файл из этапа сборки
COPY --from=builder /auth-service .

# Копируем .env файл (если он не будет монтироваться как volume)
# COPY .env .env
# Копируем папку с миграциями (нужно для migrate сервиса, но auth может не требовать)
# COPY shared/database/migrations ./migrations

# Порт, который будет слушать сервис
EXPOSE 8081
# Используем порт по умолчанию из config.go, если не переопределен в .env

# Команда для запуска сервиса
ENTRYPOINT ["/app/auth-service"] 