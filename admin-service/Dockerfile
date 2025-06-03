# --- Стадия сборки ---
FROM golang:1.24-alpine AS builder

# Игнорируем go.work внутри сборки
ENV GOWORK=off

# Устанавливаем рабочую директорию
WORKDIR /app

# Копируем go.mod и go.sum для admin-service
COPY admin-service/go.mod admin-service/go.sum ./admin-service/

# Копируем go.mod и go.sum для shared модуля
COPY shared/go.mod shared/go.sum ./shared/

# Загружаем зависимости для admin-service
WORKDIR /app/admin-service
RUN go mod download

# Возвращаемся в корень
WORKDIR /app

# Копируем исходный код shared модуля
COPY shared/ ./shared/

# Копируем исходный код admin-service
COPY admin-service/ ./admin-service/

# Собираем приложение admin-service
WORKDIR /app/admin-service
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /admin-server ./cmd/server/main.go

# --- Этап выполнения ---
FROM alpine:latest

RUN apk --no-cache add ca-certificates wget \
    && addgroup -S app && adduser -S app -G app

WORKDIR /app

# Копируем скомпилированный бинарник
COPY --from=builder --chown=app:app /admin-server .

# Копируем веб-файлы (шаблоны И статику) из admin-service/web
COPY --from=builder --chown=app:app /app/admin-service/web ./web

# --- ДОБАВЛЕНО: Копируем ОБЩИЙ 404.html --- 
# Убедимся, что директория для статики существует
RUN mkdir -p /app/web/static
# Копируем общий 404 из shared/static в /app/web/static/
COPY --from=builder --chown=app:app /app/shared/static/404.html /app/web/static/404.html

# Порт для метрик Prometheus
EXPOSE 9094

# Команда для запуска приложения
USER app
ENTRYPOINT ["/app/admin-server"]