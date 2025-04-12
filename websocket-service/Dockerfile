# Этап 1: Сборка приложения
ARG GO_VERSION=1.24 # Обновляем версию Go
FROM golang:${GO_VERSION}-alpine AS builder

# Устанавливаем рабочую директорию внутри контейнера
WORKDIR /app

# 1. Копируем mod/sum сервиса
COPY websocket-service/go.mod websocket-service/go.sum ./websocket-service/

# 2. Копируем mod/sum shared модуля (важно для download)
COPY shared/go.mod shared/go.sum ./shared/

# 3. Копируем ВЕСЬ код shared модуля, т.к. replace ссылается на него
COPY shared/ ./shared/

# 4. Переходим в директорию сервиса, чтобы download видел ../shared
WORKDIR /app/websocket-service
# Теперь запускаем download. Он должен найти ../shared/go.mod
RUN go mod download

# 5. Возвращаемся в корень /app и копируем ОСТАЛЬНОЙ код проекта
# (не копируя shared снова, если COPY выше сделал это полностью,
# но для простоты скопируем все, Docker обработает это эффективно)
WORKDIR /app
COPY . .

# 6. Снова переходим в директорию сервиса для сборки
WORKDIR /app/websocket-service

# Создаем папку vendor, отключая workspace mode
RUN GOWORK=off go mod vendor

# Собираем приложение, используя vendor, отключая workspace mode
RUN GOWORK=off CGO_ENABLED=0 GOOS=linux go build -mod=vendor -ldflags="-w -s" -o /app/websocket-server ./cmd/server/main.go

# Этап 2: Создание минимального финального образа
FROM alpine:latest

# Устанавливаем рабочую директорию
WORKDIR /app

# Копируем собранный бинарник из /app/websocket-service (где он был создан)
COPY --from=builder /app/websocket-server .

# Копируем возможные необходимые файлы конфигурации или статики (если они появятся)
# COPY ./config /app/config

# Открываем порт, который будет слушать сервис (берется из переменной окружения)
# EXPOSE 8083 - Не обязательно, т.к. порт задается в docker-compose

# Устанавливаем команду для запуска приложения
CMD ["./websocket-server"] 