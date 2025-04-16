# --- Build Stage --- 
ARG GO_VERSION=1.24 # Обновляем версию Go
FROM golang:${GO_VERSION}-alpine AS builder

# Игнорируем go.work внутри сборки
ENV GOWORK=off

WORKDIR /src

# <<< Шаг 1: Копируем файлы модулей для кеширования зависимостей >>>
COPY shared/go.mod shared/go.sum ./shared/
COPY story-generator/go.mod story-generator/go.sum ./story-generator/

# <<< Шаг 2: Копируем ВЕСЬ исходный код для обоих модулей >>>
COPY shared/ ./shared/
COPY story-generator/ ./story-generator/

# <<< Шаг 3: Переходим в директорию собираемого модуля >>>
WORKDIR /src/story-generator

# <<< Шаг 4: Запускаем tidy и download ПОСЛЕ копирования всего кода >>>
# Это позволит Go найти локальный модуль shared через replace
RUN go mod tidy
RUN go mod download

# <<< Шаг 5: Собираем приложение >>>
# CGO_ENABLED=0 для статичной сборки без C зависимостей
# -ldflags="-s -w" для уменьшения размера бинарника
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/worker ./cmd/worker/main.go

# --- Final Stage --- 
FROM alpine:latest

WORKDIR /app

# Копируем собранный бинарник из builder stage
COPY --from=builder /app/worker /app/worker

# --- Добавляем права на выполнение --- 
RUN chmod +x /app/worker
# -------------------------------------

# --- ВАЖНО: Копирование директории с промптами ---
# Если воркер читает файлы из директории promts, её нужно скопировать
# Путь назначения должен совпадать с тем, что ожидается в конфигурации (PROMPTS_DIR)
# Пример:
COPY promts/ /app/promts/
# ----------------------------------------------------

# Копируем .env файл, если используется godotenv (не рекомендуется для production)
# COPY .env .env 

# Возвращаем запуск основного приложения
CMD ["/app/worker"]
HEALTHCHECK --interval=10s --timeout=5s --start-period=30s --retries=5 CMD wget --spider -q http://localhost:9091/health || exit 1 