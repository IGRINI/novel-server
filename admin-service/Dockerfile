# --- Стадия сборки ---
FROM golang:1.24-alpine AS builder

# Устанавливаем рабочую директорию СРАЗУ в модуль
WORKDIR /app/admin-service

# Копируем go.mod / go.sum для admin-service В ТЕКУЩУЮ директорию (/app/admin-service)
# Контекст сборки = . (корень проекта), поэтому указываем полный путь
COPY admin-service/go.mod admin-service/go.sum ./

# Копируем go.mod / go.sum для shared В ОТДЕЛЬНУЮ директорию /app/shared
# Используем абсолютный путь для ясности
RUN mkdir -p /app/shared
COPY shared/ /app/shared/

# Загружаем зависимости ТОЛЬКО для admin-service (мы в /app/admin-service)
RUN go mod download

# Копируем исходный код shared в /app/shared
# Контекст сборки = ., поэтому путь к shared начинается с shared/

# Копируем исходный код admin-service в текущую директорию (/app/admin-service)
# Контекст сборки = ., поэтому путь к admin-service начинается с admin-service/
COPY admin-service/ .

# <<< Шаг отладки: Проверяем наличие файлов перед сборкой
RUN echo "--- Listing current directory (/app/admin-service) ---" && ls -l . && \
    echo "--- Listing /app/shared directory ---" && ls -l /app/shared && \
    echo "--- Listing /app/shared/logger directory ---" && ls -l /app/shared/logger

# Собираем приложение (мы уже в /app/admin-service)
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /admin-server ./cmd/server/main.go

# --- Этап выполнения ---
FROM alpine:latest

WORKDIR / 
# Устанавливаем рабочую директорию в корень

# Копируем скомпилированный бинарник
COPY --from=builder /admin-server /admin-server

# Копируем шаблоны HTML
COPY --from=builder /app/admin-service/web /web 
# ^^^ Добавляем эту строку, чтобы шаблоны были доступны по пути /web/templates

# Копируем .env файл (если нужен внутри контейнера)
# COPY .env .env

# Указываем порт, который будет слушать приложение
# Замените 8084 на ваш ADMIN_SERVER_PORT, если он другой
# EXPOSE 8084 # Закомментировано, т.к. порт мапится в docker-compose

# Команда для запуска приложения
ENTRYPOINT ["/admin-server"] 