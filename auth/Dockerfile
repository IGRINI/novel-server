# Этап 1: Сборка приложения
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Копируем go.mod и go.sum для кэширования зависимостей
# Копируем корневой go.work, чтобы workspace работал
COPY go.work go.work
COPY auth/go.mod auth/go.mod
COPY auth/go.sum auth/go.sum
COPY shared/go.mod shared/go.mod
COPY shared/go.sum shared/go.sum

# Загружаем зависимости заранее (используя workspace)
# Сначала auth
WORKDIR /app/auth
RUN go mod download
# Затем shared
WORKDIR /app/shared
RUN go mod download

# Возвращаемся в корень и копируем исходный код
WORKDIR /app
COPY auth/ ./auth/
COPY shared/ ./shared/

# Собираем приложение auth
WORKDIR /app/auth
RUN CGO_ENABLED=0 GOOS=linux go build -o /auth-service ./cmd/auth/main.go


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
# Запускаем бинарник напрямую
ENTRYPOINT ["/app/auth-service"] 