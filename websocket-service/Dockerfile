# --- Build Stage ---
ARG GO_VERSION=1.24
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

# Собираем приложение websocket-service
# Команда выполняется из корня /app
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /websocket-server ./websocket-service/cmd/server/main.go

# --- Final Stage ---
FROM alpine:latest

# Устанавливаем рабочую директорию
WORKDIR /app

# Копируем собранный бинарник из builder stage
COPY --from=builder /websocket-server .

# Копируем возможные необходимые файлы конфигурации или статики (если они появятся)
# COPY ./config /app/config

# Открываем порт, который будет слушать сервис (берется из переменной окружения)
# EXPOSE 8083 - Не обязательно, т.к. порт задается в docker-compose

# Устанавливаем команду для запуска приложения
CMD ["/app/websocket-server"]