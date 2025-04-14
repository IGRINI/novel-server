# Этап 1: Сборка приложения
ARG GO_VERSION=1.24
FROM golang:${GO_VERSION}-alpine AS builder

WORKDIR /app/websocket-service

# 1. Копируем go.mod / go.sum сначала
COPY websocket-service/go.mod websocket-service/go.sum ./
RUN mkdir -p ../shared
COPY shared/go.mod shared/go.sum ../shared/

# 2. Копируем ВЕСЬ исходный код СНАЧАЛА
COPY shared/ ../shared/
COPY websocket-service/ .

# 3. Теперь выполняем tidy, когда весь код на месте
RUN GOWORK=off go mod tidy

# 4. Собираем приложение
RUN GOWORK=off CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /app/websocket-server ./cmd/server

# Этап 2: Создание минимального финального образа
FROM alpine:latest

# Устанавливаем рабочую директорию
WORKDIR /app

# Копируем собранный бинарник
COPY --from=builder /app/websocket-server .

# Копируем возможные необходимые файлы конфигурации или статики (если они появятся)
# COPY ./config /app/config

# Открываем порт, который будет слушать сервис (берется из переменной окружения)
# EXPOSE 8083 - Не обязательно, т.к. порт задается в docker-compose

# Устанавливаем команду для запуска приложения
CMD ["./websocket-server"] 