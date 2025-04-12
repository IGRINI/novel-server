# --- Build Stage --- 
ARG GO_VERSION=1.24 # Обновляем версию Go
FROM golang:${GO_VERSION}-alpine AS builder

# Игнорируем go.work внутри сборки
ENV GOWORK=off

WORKDIR /src

# Копируем go.mod и go.sum из директории story-generator
COPY story-generator/go.mod story-generator/go.sum ./story-generator/
# Копируем go.mod и go.sum для shared (нужны из-за replace)
COPY shared/go.mod shared/go.sum ./shared/
# Копируем исходный код shared
COPY shared/ ./shared/

# Переходим в директорию воркера и загружаем зависимости
WORKDIR /src/story-generator
RUN go mod download

# Возвращаемся в /src и копируем остальной исходный код воркера
WORKDIR /src
COPY story-generator/ ./story-generator/

# Переходим в директорию модуля ПЕРЕД tidy
WORKDIR /src/story-generator
# Синхронизируем зависимости перед сборкой, отключая workspace mode
RUN GOWORK=off go mod tidy

# Собираем приложение (уже находимся в /src/story-generator)
# CGO_ENABLED=0 для статичной сборки без C зависимостей
# -ldflags="-s -w" для уменьшения размера бинарника
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/worker ./cmd/worker/main.go

# --- Final Stage --- 
FROM alpine:latest

WORKDIR /app

# Копируем собранный бинарник из builder stage
COPY --from=builder /app/worker /app/worker

# Копируем .env файл, если используется godotenv (не рекомендуется для production)
# COPY .env .env 

# Указываем команду для запуска
# Бинарник будет читать конфигурацию из переменных окружения, переданных docker-compose
CMD ["/app/worker"] 