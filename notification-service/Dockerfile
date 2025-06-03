# Этап сборки
FROM golang:1.24-alpine AS builder

# Игнорируем go.work внутри сборки
ENV GOWORK=off

# Устанавливаем рабочую директорию
WORKDIR /app

# Копируем go.mod и go.sum для notification-service
COPY notification-service/go.mod notification-service/go.sum ./notification-service/

# Копируем go.mod и go.sum для shared модуля
COPY shared/go.mod shared/go.sum ./shared/

# Загружаем зависимости для notification-service
WORKDIR /app/notification-service
RUN go mod download

# Возвращаемся в корень
WORKDIR /app

# Копируем исходный код shared модуля
COPY shared/ ./shared/

# Копируем исходный код notification-service
COPY notification-service/ ./notification-service/

# Собираем приложение
WORKDIR /app/notification-service
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /notification-service ./cmd/server

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