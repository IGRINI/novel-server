# Базовый сборочный образ
FROM golang:1.22-alpine AS builder

# Установка необходимых утилит
RUN apk update && apk add --no-cache git ca-certificates tzdata && update-ca-certificates

# Создаем рабочую директорию
WORKDIR /app

# Копируем файлы зависимостей
COPY go.mod go.sum ./

# Загружаем все зависимости
RUN go mod download

# Копируем исходный код
COPY . .

# Сборка главного сервера
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o server ./cmd/server/main.go

# Сборка auth сервиса
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o auth-service ./cmd/auth/main.go

# Финальный образ для main server
FROM alpine:latest AS server

# Устанавливаем часовой пояс и сертификаты
RUN apk --no-cache add ca-certificates tzdata

# Создаем непривилегированного пользователя
RUN adduser -D -g '' appuser

WORKDIR /app/

# Копируем бинарные файлы из сборочного образа
COPY --from=builder /app/server .
COPY --from=builder /app/internal/database/migrations ./internal/database/migrations/

# Переходим на непривилегированного пользователя
USER appuser

# Устанавливаем точку входа
ENTRYPOINT ["./server"]

# Финальный образ для auth service
FROM alpine:latest AS auth-service

# Устанавливаем часовой пояс и сертификаты
RUN apk --no-cache add ca-certificates tzdata

# Создаем непривилегированного пользователя
RUN adduser -D -g '' appuser

WORKDIR /app/

# Копируем бинарные файлы из сборочного образа
COPY --from=builder /app/auth-service .
COPY --from=builder /app/internal/database/migrations ./internal/database/migrations/

# Переходим на непривилегированного пользователя
USER appuser

# Устанавливаем точку входа
ENTRYPOINT ["./auth-service"] 