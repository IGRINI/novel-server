# --- Build Stage ---
ARG GO_VERSION=1.24
FROM golang:${GO_VERSION}-alpine AS builder

# Игнорируем go.work внутри сборки
ENV GOWORK=off

# Устанавливаем рабочую директорию
WORKDIR /app

# Копируем go.mod и go.sum для websocket-service
COPY websocket-service/go.mod websocket-service/go.sum ./websocket-service/

# Копируем go.mod и go.sum для shared модуля
COPY shared/go.mod shared/go.sum ./shared/

# Загружаем зависимости для websocket-service
WORKDIR /app/websocket-service
RUN go mod download

# Возвращаемся в корень
WORKDIR /app

# Копируем исходный код shared модуля
COPY shared/ ./shared/

# Копируем исходный код websocket-service
COPY websocket-service/ ./websocket-service/

# Собираем приложение websocket-service
WORKDIR /app/websocket-service
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /websocket-server ./cmd/server/main.go

# --- Final Stage ---
FROM alpine:latest

RUN apk --no-cache add ca-certificates \
    && addgroup -S app && adduser -S app -G app

# Устанавливаем рабочую директорию
WORKDIR /app

# Копируем собранный бинарник из builder stage
COPY --from=builder --chown=app:app /websocket-server .

USER app

# Копируем возможные необходимые файлы конфигурации или статики (если они появятся)
# COPY ./config /app/config

# Открываем порт, который будет слушать сервис (берется из переменной окружения)
# EXPOSE 8083 - Не обязательно, т.к. порт задается в docker-compose

# Устанавливаем команду для запуска приложения
CMD ["/app/websocket-server"]