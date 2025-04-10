# Использование Docker для запуска микросервисов

## Подготовка к запуску

1. Убедитесь, что у вас установлены:
   - Docker
   - Docker Compose

2. Скопируйте `.env.example` в `.env` и настройте необходимые переменные:
   ```
   cp .env.example .env
   ```

3. Особое внимание уделите настройкам:
   - `DB_PASSWORD` - сложный пароль для базы данных
   - `JWT_SECRET` - секретный ключ для JWT токенов
   - `PASSWORD_SALT` - соль для хеширования паролей
   - `AUTH_SERVICE_API_KEY` - API ключ для межсервисной аутентификации

## Запуск контейнеров

```bash
docker-compose up -d
```

При первом запуске будет выполнена сборка образов и инициализация базы данных.

## Остановка контейнеров

```bash
docker-compose down
```

Для удаления томов с данными (включая базу данных):

```bash
docker-compose down -v
```

## Проверка работоспособности

### Auth Service
```
curl http://localhost:8081/auth/login -X POST -H "Content-Type: application/json" -d '{"username":"ваш_логин", "password":"ваш_пароль"}'
```

### Main API Server
```
curl http://localhost:8080/api/novels -H "Authorization: Bearer ваш_токен"
```

## Структура микросервисов

1. **Auth Service** (порт 8081):
   - Аутентификация пользователей
   - Регистрация новых пользователей
   - Управление JWT токенами
   - Межсервисная аутентификация

2. **API Server** (порт 8080):
   - Основной API шлюз
   - Работа с романами/новеллами
   - Проксирование запросов к Auth Service
   - Интеграция с API нейросетей

3. **PostgreSQL** (порт 5432):
   - Хранение данных всех сервисов

## Логи контейнеров

Для просмотра логов контейнеров используйте:

```bash
# Все контейнеры
docker-compose logs -f

# Конкретный сервис
docker-compose logs -f auth-service
docker-compose logs -f api-server
``` 