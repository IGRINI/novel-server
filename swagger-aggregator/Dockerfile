# --- Build Stage ---
ARG GO_VERSION=1.24
FROM golang:${GO_VERSION}-alpine AS builder

# Игнорируем go.work внутри сборки
ENV GOWORK=off

# Устанавливаем рабочую директорию
WORKDIR /app

# Копируем go.mod и go.sum для swagger-aggregator
COPY swagger-aggregator/go.mod swagger-aggregator/go.sum ./swagger-aggregator/

# Копируем go.mod и go.sum для shared модуля
COPY shared/go.mod shared/go.sum ./shared/

# Загружаем зависимости для swagger-aggregator
WORKDIR /app/swagger-aggregator
RUN go mod download

# Возвращаемся в корень
WORKDIR /app

# Копируем исходный код shared модуля
COPY shared/ ./shared/

# Копируем исходный код swagger-aggregator
COPY swagger-aggregator/ ./swagger-aggregator/

# Собираем приложение swagger-aggregator
WORKDIR /app/swagger-aggregator
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /swagger-aggregator ./cmd/server/main.go

# --- Final Stage ---
FROM alpine:latest

# Устанавливаем корневые сертификаты
RUN apk --no-cache add ca-certificates \
    && addgroup -S app && adduser -S app -G app

WORKDIR /app

# Копируем собранный бинарник из builder stage
COPY --from=builder --chown=app:app /swagger-aggregator .

# Копируем шаблоны и статические файлы
COPY --from=builder --chown=app:app /app/swagger-aggregator/templates ./templates
COPY --from=builder --chown=app:app /app/swagger-aggregator/static ./static

USER app

# Команда для запуска сервиса
ENTRYPOINT ["/app/swagger-aggregator"] 