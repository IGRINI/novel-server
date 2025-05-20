# Этап сборки
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Копируем файлы зависимостей и скачиваем их
COPY go.mod go.sum ./
RUN go mod download

# Копируем исходный код
COPY . .

# Собираем приложение
# CGO_ENABLED=0 для статичной сборки без C зависимостей
# -ldflags="-s -w" для уменьшения размера бинарника
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /notification-service ./notification-service/cmd/server

# Этап выполнения
FROM alpine:latest

# Устанавливаем корневые сертификаты (могут понадобиться для внешних API)
RUN apk --no-cache add ca-certificates wget \
    && addgroup -S app && adduser -S app -G app

WORKDIR /app

# Копируем бинарник из этапа сборки
COPY --from=builder --chown=app:app /notification-service .

USER app

# Копируем конфигурацию (если используется файл config.yml)
# COPY config.yml .

# Указываем порт для health check (значение по умолчанию или из ENV)
ARG HEALTH_CHECK_PORT=8088
EXPOSE ${HEALTH_CHECK_PORT}

# Команда для запуска сервиса
CMD ["./notification-service"] 