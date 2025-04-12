# Novel Server

Интерактивный генератор текстовых новелл на основе AI.

## Архитектура

Проект использует микросервисную архитектуру, управляемую через Docker Compose. Включает следующие сервисы:

*   **api-gateway (Traefik):** Единая точка входа для всех HTTP и WebSocket запросов. Маршрутизирует запросы к соответствующим внутренним сервисам.
*   **auth:** Сервис аутентификации и авторизации пользователей (регистрация, вход, управление токенами).
*   **story-generator:** Воркер, отвечающий за генерацию контента новеллы с помощью AI по запросам из очереди.
*   **websocket-service:** Управляет WebSocket соединениями для отправки уведомлений пользователям в реальном времени (например, о завершении генерации).
*   **postgres:** База данных PostgreSQL для хранения данных пользователей, результатов генерации и т.д.
*   **redis:** Кэш Redis (используется сервисом `auth` для хранения сессий/отозванных токенов).
*   **rabbitmq:** Брокер сообщений RabbitMQ для асинхронного взаимодействия между сервисами (постановка задач генерации, отправка уведомлений).
*   **migrate:** Сервис для применения миграций базы данных.

## Запуск проекта

1.  **Установите Docker и Docker Compose.**
2.  **Создайте файл `.env`** в корне проекта на основе примера `.env.example` (если он есть) или скопируйте существующий `.env`.
    *   **Важно:** Убедитесь, что установлены `JWT_SECRET`, `PASSWORD_SALT`, `DB_PASSWORD`, `AI_API_KEY` и другие необходимые секреты.
3.  **Запустите все сервисы:**
    ```bash
    docker-compose up --build -d
    ```
    *   Флаг `--build` пересобирает образы при необходимости.
    *   Флаг `-d` запускает контейнеры в фоновом режиме.

4.  **Остановка сервисов:**
    ```bash
    docker-compose down
    ```

## Доступ к API

Все запросы к бэкенду должны отправляться через API Gateway (Traefik):

*   **Базовый URL API:** `http://localhost:8080` (Стандартный порт).
*   **WebSocket URL:** `ws://localhost:8080/ws`
*   **Traefik Dashboard:** `http://localhost:8888`

### Аутентификация

Для доступа к защищенным эндпоинтам (включая `/api/stories` и `/ws`) необходимо передавать JWT access токен.
*   Для HTTP запросов: заголовок `Authorization: Bearer <ваш_access_token>`.
*   Для WebSocket: См. раздел WebSocket Уведомления.

### Основные эндпоинты

#### Сервис Аутентификации (`/api/auth`)

*   **`POST /api/auth/register`**: Регистрация.
*   **`POST /api/auth/login`**: Вход (возвращает `access_token`, `refresh_token`).
*   **`POST /api/auth/refresh`**: Обновление токена.
*   **`POST /api/auth/logout`**: Выход.
*   **`POST /api/auth/token/verify`**: Проверка токена (для сервисов).

#### Пользовательские API (`/api`)

*   **`GET /api/me`**: Информация о текущем пользователе.

#### Сервис Геймплея (`/api/stories`)

*   **`POST /api/stories/generate`**
    *   Запускает генерацию новой истории на основе промпта пользователя.
    *   **Требует заголовок `Authorization`.**
    *   Тело запроса (JSON):
        ```json
        {
          "prompt": "Текст начального запроса пользователя..."
        }
        ```
    *   Ответ при успехе (`202 Accepted`):
        *   Тело ответа (JSON): Полный объект `StoryConfig` с `id` и статусом `generating`.
            ```json
            {
              "id": "uuid-string",
              "user_id": 123,
              "title": "",
              "description": "Текст начального запроса пользователя...",
              "user_input": ["Текст начального запроса пользователя..."],
              "config": null,
              "status": "generating",
              "created_at": "timestamp",
              "updated_at": "timestamp"
            }
            ```
    *   Ответ при ошибке публикации задачи (`500 Internal Server Error`):
        *   Тело ответа (JSON): Тот же объект `StoryConfig`, но со статусом `error`.
    *   Ответ при конфликте (например, пользователь уже имеет активную генерацию) (`409 Conflict`):
        *   Тело ответа (JSON): `{ "message": "User already has an active generation task" }`

*   **`GET /api/stories/:id`**
    *   Получение информации о конкретной конфигурации истории (драфте) по ее `id`.
    *   **Требует заголовок `Authorization`.**
    *   Параметр пути: `:id` - UUID конфигурации истории.
    *   Ответ при успехе (`200 OK`):
        *   Тело ответа (JSON): Полный объект `StoryConfig` (включая поле `config`, если оно сгенерировано).
    *   Ответ при ошибке (`404 Not Found`, `401 Unauthorized`).

*   **`POST /api/stories/:id/revise`**
    *   Запускает процесс ревизии существующего драфта истории.
    *   **Требует заголовок `Authorization`.**
    *   Параметр пути: `:id` - UUID конфигурации истории.
    *   Тело запроса (JSON):
        ```json
        {
          "revision_prompt": "Текст правок от пользователя..."
        }
        ```
    *   Ответ при успехе (`202 Accepted`):
        *   **Без тела ответа.** Результат (обновленный `StoryConfig`) будет отправлен по WebSocket.
    *   Ответ при ошибке (`404 Not Found`, `409 Conflict` - если статус не `draft` или `error`).

#### WebSocket Уведомления (`/ws`)

*   **URL для подключения:** `ws://localhost:8080/ws`
*   **Аутентификация:** Клиент **должен** передать валидный JWT access токен при установке соединения. 
    *   Рекомендуемый способ: через query-параметр `ws://localhost:8080/ws?token=<ваш_access_token>`. Middleware `shared/middleware/auth.go` и `websocket-service/internal/handler/ws_handler.go` были обновлены для поддержки этого метода.
    *   *Старый метод через заголовок `Authorization` больше не поддерживается стандартными WebSocket API браузеров.* 
*   **Получаемые сообщения (от сервера клиенту):**
    *   Когда генерация или ревизия `StoryConfig` завершена (успешно или с ошибкой), сервер отправит JSON-сообщение следующей структуры (`ClientStoryUpdate`):
        ```json
        {
          "id": "uuid-string",             // ID обновленного StoryConfig
          "user_id": "string-user-id",     // ID пользователя
          "status": "draft" | "error",      // Новый статус
          "title": "Сгенерированное название", // Из поля "t" JSON-конфига
          "description": "Сгенерированное описание", // Из поля "sd" JSON-конфига
          "themes": ["theme1", "theme2"],  // Из поля "pp.th"
          "world_lore": ["lore1", "lore2"], // Из поля "pp.wl"
          "player_description": "Описание игрока", // Из поля "p_desc"
          "error_details": "Текст ошибки" // Только если status == "error"
        }
        ```

#### Внутренние API (Internal)

Эти эндпоинты предназначены для прямого взаимодействия между сервисами и **не должны** быть доступны через основной API Gateway (`/api/auth`). Доступ к ним должен быть ограничен на уровне сети или через отдельный защищенный роутер Traefik (не настроено).

*   **`POST /internal/auth/token/generate`**
    *   Генерация токена для межсервисного взаимодействия.
    *   Тело запроса (JSON):
        ```json
        {
          "service_name": "string"
        }
        ```
    *   Ответ (JSON):
        ```json
        {
          "inter_service_token": "string"
        }
        ```
*   **`POST /internal/auth/token/verify`**
    *   Проверка токена межсервисного взаимодействия.
    *   Тело запроса (JSON):
        ```json
        {
          "token": "string" // Inter-service токен
        }
        ```
    *   Ответ (JSON при успехе):
        ```json
        {
          "service_name": "string",
          "valid": true
        }
        ```

## Задачи для генерации (Story Generator)

*   Сервис `gameplay-service` теперь отправляет задачи в очередь `story_generation_tasks`.
*   `story-generator` получает задачи, выполняет их и отправляет **полные** уведомления (`shared/messaging.NotificationPayload`) в очередь `internal_updates`.

## Поток Уведомлений

1.  `story-generator` -> `internal_updates` (полное `NotificationPayload`)
2.  `gameplay-service` слушает `internal_updates`:
    *   Обновляет `StoryConfig` в БД (включая поля `Config`, `Title`, `Description`).
    *   Формирует **отфильтрованное** сообщение `ClientStoryUpdate`.
    *   Отправляет `ClientStoryUpdate` в очередь `client_updates`.
3.  `websocket-service` слушает `client_updates`:
    *   Получает `ClientStoryUpdate`.
    *   Находит соединение нужного `UserID`.
    *   Пересылает `ClientStoryUpdate` клиенту по WebSocket.

# ... (Остальная часть README) ... 