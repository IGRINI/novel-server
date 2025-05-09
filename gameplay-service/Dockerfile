# --- Стадия сборки ---
FROM golang:1.24-alpine as builder

# Устанавливаем рабочую директорию в корень модуля
WORKDIR /app

# Копируем ТОЛЬКО корневые go.mod и go.sum
COPY go.mod go.sum ./

# Загружаем ВСЕ зависимости проекта
RUN go mod download

# Копируем ВЕСЬ исходный код проекта
COPY . .

# Собираем приложение gameplay-service
# Команда выполняется из корня /app
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /gameplay-service ./gameplay-service/cmd/server/main.go

# --- Финальная стадия ---
FROM alpine:latest

# Устанавливаем корневые сертификаты (на всякий случай)
RUN apk --no-cache add ca-certificates wget

WORKDIR /app

# Копируем собранный бинарник из стадии сборки
COPY --from=builder /gameplay-service .

# Порт, который будет слушать сервис (для информации)
# EXPOSE 8082 - порт определяется переменной окружения

# Команда для запуска приложения
ENTRYPOINT ["/app/gameplay-service"]
