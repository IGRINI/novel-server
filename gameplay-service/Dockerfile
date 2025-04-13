# --- Стадия сборки ---
FROM golang:1.24-alpine as builder

# Устанавливаем рабочую директорию
WORKDIR /app

# Копируем go.work и go.work.sum
COPY go.work go.work.sum ./

# Копируем ВСЕ модули, указанные в go.work, чтобы go build мог их найти
# Копируем сначала go.mod/go.sum для кеширования слоя зависимостей
COPY auth/go.mod auth/go.sum ./auth/
COPY story-generator/go.mod story-generator/go.sum ./story-generator/
COPY shared/go.mod shared/go.sum ./shared/
COPY gameplay-service/go.mod gameplay-service/go.sum ./gameplay-service/

# --- Опционально: Загрузка зависимостей ---
# Возможно, этот шаг не нужен, так как go build сам скачает зависимости.
# Если go build будет медленным, можно раскомментировать.
# Сначала синхронизируем go.work с go.mod файлов
# RUN go work sync
# Затем скачиваем зависимости
# RUN go mod download

# Копируем исходный код ВСЕХ модулей
COPY auth/ ./auth/
COPY story-generator/ ./story-generator/
COPY shared/ ./shared/
COPY gameplay-service/ ./gameplay-service/

# Собираем приложение gameplay-service
# Запускаем go build из корневой директории воркспейса (/app),
# указывая путь к main.go сервиса.
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /server ./gameplay-service/cmd/server/main.go

# --- Финальная стадия ---
FROM alpine:latest

# Копируем собранный бинарник из стадии сборки
COPY --from=builder /server /server

# Опционально: Копируем другие нужные файлы (например, конфиги, если они читаются из файлов)
# COPY --from=builder /app/gameplay-service/config/config.yaml /config/config.yaml

# Указываем порт, который слушает приложение (если нужно для информации)
# EXPOSE ${GAMEPLAY_SERVER_PORT:-8082}

# Команда для запуска приложения
CMD ["/server"]
