# --- Стадия сборки ---
FROM golang:1.24-alpine as builder

# Устанавливаем рабочую директорию СРАЗУ в модуль
WORKDIR /app/gameplay-service

# Копируем go.mod / go.sum для gameplay-service В ТЕКУЩУЮ директорию (.)
COPY gameplay-service/go.mod gameplay-service/go.sum ./

# Копируем go.mod / go.sum для shared В ОТДЕЛЬНУЮ директорию ../shared
# чтобы директива replace в go.mod сработала
# Создаем директорию, если ее нет (на всякий случай)
RUN mkdir -p ../shared
COPY shared/go.mod shared/go.sum ../shared/

# Загружаем зависимости ТОЛЬКО для gameplay-service
# go mod download поймет replace ../shared, но пока не найдет там код (это нормально на этом шаге)
# Этот шаг опционален, т.к. go build тоже скачает зависимости, но может ускорить сборку
RUN go mod download

# Копируем исходный код shared в ../shared
COPY shared/ ../shared/

# Копируем исходный код gameplay-service в текущую директорию (.)
# Копируем всё из локальной директории gameplay-service в /app/gameplay-service контейнера
COPY gameplay-service/ .

# Собираем приложение (мы уже в /app/gameplay-service)
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /server ./cmd/server/main.go

# --- Финальная стадия ---
FROM alpine:latest

# Копируем собранный бинарник из стадии сборки
WORKDIR / 
# Устанавливаем рабочую директорию в корень для финального образа
COPY --from=builder /server /server

# Опционально: Копируем другие нужные файлы (например, конфиги, если они читаются из файлов)
# COPY --from=builder /app/gameplay-service/config/config.yaml /config/config.yaml

# Указываем порт, который слушает приложение (если нужно для информации)
# EXPOSE ${GAMEPLAY_SERVER_PORT:-8082}

# Команда для запуска приложения
CMD ["/server"]
HEALTHCHECK --interval=10s --timeout=5s --start-period=30s --retries=5 CMD wget --spider -q http://localhost:8082/health || exit 1
