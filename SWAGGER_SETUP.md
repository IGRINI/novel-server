# Swagger Setup для Novel Server

## Обзор

Все сервисы Novel Server теперь интегрированы с Swagger для автоматической генерации и агрегации API документации.

## Архитектура

### Swagger Aggregator
- **Порт**: 8090
- **Функция**: Собирает OpenAPI спецификации от всех сервисов и предоставляет единый интерфейс
- **URL**: http://localhost:8090/

### Сервисы с Swagger
Все сервисы предоставляют OpenAPI спецификации:

| Сервис | Порт | OpenAPI JSON | OpenAPI YAML | Swagger UI |
|--------|------|--------------|--------------|------------|
| Auth Service | 8081 | `/api/openapi.json` | `/api/openapi.yaml` | `/swagger/index.html` |
| Admin Service | 8084 | `/api/openapi.json` | `/api/openapi.yaml` | `/swagger/index.html` |
| Gameplay Service | 8082 | `/api/openapi.json` | `/api/openapi.yaml` | `/swagger/index.html` |
| WebSocket Service | 8083 | `/api/openapi.json` | `/api/openapi.yaml` | `/swagger/index.html` |

## Выполненные исправления

### 1. Auth Service
- ✅ Добавлены Swagger зависимости в `go.mod`
- ✅ Добавлены Swagger аннотации в `cmd/auth/main.go`
- ✅ Добавлены эндпоинты `/api/openapi.json` и `/api/openapi.yaml`
- ✅ Добавлен Swagger UI на `/swagger/*` (только в development режиме)
- ✅ Исправлен импорт `swaggerFiles`
- ✅ Сгенерирована документация

### 2. Admin Service
- ✅ Добавлены Swagger зависимости в `go.mod`
- ✅ Добавлены Swagger аннотации в `cmd/server/main.go`
- ✅ Добавлены эндпоинты `/api/openapi.json` и `/api/openapi.yaml`
- ✅ Добавлен Swagger UI на `/swagger/*` (только в development режиме)
- ✅ Исправлен импорт `swaggerFiles`
- ✅ Сгенерирована документация

### 3. WebSocket Service
- ✅ Добавлены Swagger зависимости в `go.mod`
- ✅ Переписан с стандартного HTTP mux на Gin для поддержки Swagger
- ✅ Добавлены Swagger аннотации в `cmd/server/main.go`
- ✅ Добавлены эндпоинты `/api/openapi.json` и `/api/openapi.yaml`
- ✅ Добавлен Swagger UI на `/swagger/*`
- ✅ Исправлен импорт `swaggerFiles`
- ✅ Упрощена архитектура (убран отдельный metrics server)
- ✅ Сгенерирована документация

### 4. Gameplay Service
- ✅ Уже был настроен корректно
- ✅ Документация обновлена

### 5. Swagger Aggregator
- ✅ Уже был настроен корректно
- ✅ Документация обновлена

## Автоматизация

### PowerShell скрипт для обновления
Создан скрипт `update_swagger.ps1` для автоматического обновления всех сервисов:

```powershell
.\update_swagger.ps1
```

Скрипт выполняет:
1. `go mod tidy` для всех сервисов
2. `swag init` для генерации документации
3. Отчет о результатах

## Запуск и проверка

### 1. Обновление документации
```bash
.\update_swagger.ps1
```

### 2. Запуск сервисов
```bash
.\deploy.ps1
```

### 3. Проверка доступности

#### Swagger Aggregator (Единый интерфейс)
- http://localhost:8090/

#### Отдельные сервисы
- Auth Service: http://localhost:8081/swagger/index.html
- Admin Service: http://localhost:8084/swagger/index.html  
- Gameplay Service: http://localhost:8082/swagger/index.html
- WebSocket Service: http://localhost:8083/swagger/index.html

#### OpenAPI спецификации
- Auth: http://localhost:8081/api/openapi.json
- Admin: http://localhost:8084/api/openapi.json
- Gameplay: http://localhost:8082/api/openapi.json
- WebSocket: http://localhost:8083/api/openapi.json

### 4. Через Traefik (если настроен)
- Swagger Aggregator: http://localhost:28960/docs/
- Отдельные сервисы: http://localhost:28960/swagger/

## Разработка

### Добавление новых эндпоинтов

1. Добавьте Swagger аннотации к функции-обработчику:
```go
// @Summary Описание эндпоинта
// @Description Подробное описание
// @Tags tag-name
// @Accept json
// @Produce json
// @Param id path int true "ID записи"
// @Success 200 {object} ResponseModel
// @Failure 400 {object} ErrorResponse
// @Router /api/endpoint/{id} [get]
func HandlerFunction(c *gin.Context) {
    // код обработчика
}
```

2. Обновите документацию:
```bash
cd service-directory
swag init -g cmd/server/main.go -o docs
```

3. Или используйте автоматический скрипт:
```bash
.\update_swagger.ps1
```

### Структура аннотаций

#### Основная информация (в main.go)
```go
// @title Service Name API
// @version 1.0
// @description Описание API
// @host localhost:8081
// @BasePath /api/v1
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
```

#### Аннотации эндпоинтов
- `@Summary` - краткое описание
- `@Description` - подробное описание  
- `@Tags` - группировка эндпоинтов
- `@Accept` - принимаемый Content-Type
- `@Produce` - возвращаемый Content-Type
- `@Param` - параметры запроса
- `@Success` - успешные ответы
- `@Failure` - ошибки
- `@Router` - путь и HTTP метод
- `@Security` - требования аутентификации

## Troubleshooting

### Проблема: Swagger UI не отображается
**Решение**: Проверьте, что:
1. Импортирован пакет `swaggerFiles`
2. Добавлен маршрут для Swagger UI
3. Сгенерирована документация в папке `docs/`

### Проблема: OpenAPI JSON недоступен
**Решение**: Проверьте, что:
1. Добавлены маршруты `/api/openapi.json` и `/api/openapi.yaml`
2. Файлы `swagger.json` и `swagger.yaml` существуют в папке `docs/`

### Проблема: Swagger Aggregator не может получить спецификации
**Решение**: Проверьте, что:
1. Все сервисы запущены
2. Эндпоинты `/api/openapi.json` доступны
3. Нет проблем с сетью между сервисами

### Проблема: Ошибки при генерации документации
**Решение**: 
1. Проверьте синтаксис Swagger аннотаций
2. Убедитесь, что все импорты корректны
3. Запустите `go mod tidy`

## Мониторинг

### Проверка статуса сервисов
```bash
# Проверка доступности OpenAPI эндпоинтов
curl http://localhost:8081/api/openapi.json
curl http://localhost:8082/api/openapi.json  
curl http://localhost:8083/api/openapi.json
curl http://localhost:8084/api/openapi.json

# Проверка Swagger Aggregator
curl http://localhost:8090/health
```

### Логи
Проверьте логи сервисов для диагностики проблем:
```bash
docker service logs novel_stack_auth-service
docker service logs novel_stack_swagger-aggregator
```

## Заключение

Все сервисы Novel Server теперь полностью интегрированы со Swagger. Swagger Aggregator предоставляет единый интерфейс для всей API документации, что упрощает разработку и тестирование. 