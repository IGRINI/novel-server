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

*   **`POST /api/stories/:id/publish`**
    *   Публикует завершенный черновик истории, делая его доступным для игры.
    *   Эта операция **удаляет** исходный черновик (`StoryConfig`) и создает запись опубликованной истории (`PublishedStory`).
    *   Запускает фоновую генерацию начального игрового состояния (`Setup`).
    *   **Требует заголовок `Authorization`.**
    *   Параметр пути: `:id` - UUID черновика (`StoryConfig`) для публикации.
    *   Тело запроса: **Нет.**
    *   Ответ при успехе (`202 Accepted`):
        *   Тело ответа (JSON): `{ "published_story_id": "uuid-string" }` - ID созданной опубликованной истории.
    *   Ответ при ошибке (`404 Not Found`, `400 Bad Request` - если статус не `draft` или `error`, или нет сгенерированного `config`, `401 Unauthorized`).

*   **`GET /api/stories`**
    *   Получение списка **моих** черновиков (`StoryConfig`) с курсорной пагинацией.
    *   **Требует заголовок `Authorization`.**
    *   Query параметры:
        *   `limit` (int, опционально, default=20, max=100): Количество возвращаемых записей.
        *   `cursor` (string, опционально): Непрозрачный курсор из поля `next_cursor` предыдущего ответа для получения следующей страницы.
    *   Ответ при успехе (`200 OK`):
        *   Тело ответа (JSON): `{\"data\": [StoryConfig, ...], \"next_cursor\": \"string | null\"}`
    *   Ответ при ошибке (`400 Bad Request` - невалидный курсор, `401 Unauthorized`).

#### Сервис Геймплея (`/api/published-stories`)

*   **`GET /api/published-stories/me`**
    *   Получение списка **моих** опубликованных историй (`PublishedStory`) с offset/limit пагинацией.
    *   **Требует заголовок `Authorization`.**
    *   Query параметры:
        *   `limit` (int, опционально, default=20, max=100): Количество возвращаемых записей.
        *   `offset` (int, опционально, default=0): Смещение от начала списка.
    *   Ответ при успехе (`200 OK`):
        *   Тело ответа (JSON): `{\"data\": [PublishedStory, ...], \"next_cursor\": null}`
    *   Ответ при ошибке (`401 Unauthorized`):
        *   `400 Bad Request`: Invalid `limit` or `offset`.
        *   `500 Internal Server Error`: Failed to retrieve stories.

*   **`GET /api/published-stories/public`**
    *   Получение списка **публичных** опубликованных историй (`PublishedStory`) с offset/limit пагинацией.
    *   **Требует заголовок `Authorization`.** (Примечание: возможно, стоит сделать этот эндпоинт публичным, убрав middleware)
    *   Query параметры:
        *   `limit` (int, опционально, default=20, max=100): Количество.
        *   `offset` (int, опционально, default=0): Смещение.
    *   Ответ при успехе (`200 OK`):
        *   Тело ответа (JSON): `{"data": [PublishedStory, ...]}`
    *   Ответ при ошибке (`401 Unauthorized`).

*   **`GET /api/published-stories/:id/scene`**
    *   Получение текущей игровой сцены для указанной опубликованной истории.
    *   **Требует заголовок `Authorization`.**
    *   Параметр пути: `:id` - UUID опубликованной истории (`PublishedStory`).
    *   Ответ при успехе (`200 OK`):
        *   Тело ответа (JSON): Полный объект `StoryScene`, содержащий `id`, `publishedStoryId`, `stateHash` и `content` (JSON сцены).
            ```json
            {
              "id": "scene-uuid-string",
              "publishedStoryId": "story-uuid-string",
              "stateHash": "calculated-hash-string",
              "content": { /* JSON контент сцены от story-generator */ },
              "createdAt": "timestamp"
            }
            ```
    *   Ответ при ошибке:
        *   `401 Unauthorized`: Невалидный токен.
        *   `404 Not Found`: Опубликованная история не найдена.
        *   `409 Conflict` (`sharedModels.ErrStoryNotReadyYet`): История еще не готова к игре (статус `setup_pending` или `first_scene_pending`).
        *   `409 Conflict` (`sharedModels.ErrSceneNeedsGeneration`): Текущая сцена для данного состояния еще не сгенерирована.
        *   `500 Internal Server Error`: Другие ошибки сервера.

*   **`POST /api/published-stories/:id/choice`**
    *   Обработка выбора игрока в текущей сцене.
    *   **Требует заголовок `Authorization`.**
    *   Параметр пути: `:id` - UUID опубликованной истории (`PublishedStory`).
    *   Тело запроса (JSON):
        ```json
        {
          "selected_option_indices": [0] // Массив индексов выбранных опций (0 или 1 для каждого выбора в сцене)
        }
        ```
    *   Ответ при успехе (`200 OK`):
        *   Тело ответа: **Пока не определено.** Возвращает пустой ответ или статус OK. В будущем может возвращать следующую сцену или обновленное состояние.
    *   Ответ при ошибке (`400 Bad Request` - невалидное тело запроса, `401 Unauthorized`, `404 Not Found` - история не найдена, `409 Conflict` - история не в статусе 'ready', `500 Internal Server Error`).

*   **`DELETE /api/published-stories/:id/progress`**
    *   Удаляет прогресс текущего пользователя для указанной опубликованной истории, позволяя начать ее заново.
    *   **Требует заголовок `Authorization`.**
    *   Параметр пути: `:id` - UUID опубликованной истории (`PublishedStory`).
    *   Тело запроса: **Нет.**
    *   Ответ при успехе (`204 No Content`):
        *   **Без тела ответа.**
    *   Ответ при ошибке:
        *   `401 Unauthorized`: Невалидный токен.
        *   `404 Not Found`: Опубликованная история не найдена.
        *   `500 Internal Server Error`: Ошибка при удалении прогресса.

#### WebSocket Уведомления (`/ws`)

*   **URL для подключения:** `ws://localhost:8080/ws`
*   **Аутентификация:** Клиент **должен** передать валидный JWT access токен при установке соединения.
    *   Рекомендуемый способ: через query-параметр `ws://localhost:8080/ws?token=<ваш_access_token>`. Middleware `shared/middleware/auth.go` и `websocket-service/internal/handler/ws_handler.go` были обновлены для поддержки этого метода.
    *   *Старый метод через заголовок `Authorization` больше не поддерживается стандартными WebSocket API браузеров.*
*   **Получаемые сообщения (от сервера клиенту):**
    *   Когда генерация или ревизия **черновика** (`StoryConfig`) завершена (успешно или с ошибкой), сервер отправит JSON-сообщение следующей структуры (`ClientStoryUpdate`):
        ```json
        {
          "id": "uuid-string",             // ID обновленного StoryConfig
          "user_id": "string-user-id",     // ID пользователя
          "status": "draft" | "error",      // Новый статус черновика
          "title": "...",                  // Сгенерированное название
          "description": "...",            // Сгенерированное описание
          "themes": ["..."],               // Из поля "pp.th"
          "world_lore": ["..."],            // Из поля "pp.wl"
          "player_description": "...",     // Из поля "p_desc"
          "error_details": "..."           // Только если status == "error"
        }
        ```
    *   **ВАЖНО:** На данный момент **не отправляются** автоматические WebSocket уведомления об изменении статуса **опубликованной истории** (`PublishedStory`), например, когда завершается генерация `Setup` или готова новая сцена. Клиент должен использовать HTTP эндпоинты (`GET /api/published-stories/:id/scene`) для проверки готовности и получения текущей сцены.

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

*   Сервис `gameplay-service` теперь отправляет задачи в очередь `story_generation_tasks` для:
    *   Начальной генерации (`prompt_type: narrator`)
    *   Ревизии (`prompt_type: narrator` с `input_data.current_config`)
    *   Генерации начального состояния игры (`prompt_type: novel_setup`)
*   `story-generator` получает задачи, выполняет их и отправляет **полные** уведомления (`shared/messaging.NotificationPayload`) в очередь `internal_updates`.

## Поток Уведомлений

1.  `story-generator` -> `internal_updates` (полное `NotificationPayload` с результатом генерации `narrator`)
2.  `gameplay-service` слушает `internal_updates`:
    *   Получает результат генерации `narrator`.
    *   Обновляет `StoryConfig` в БД (статус `draft`, поля `Config`, `Title`, `Description`).
    *   Формирует **отфильтрованное** сообщение `ClientStoryUpdate` (выбирая нужные поля из `Config`).
    *   Отправляет `ClientStoryUpdate` в очередь `client_updates`.
3.  `websocket-service` слушает `client_updates`:
    *   Получает `ClientStoryUpdate`.
    *   Находит WebSocket соединение для нужного `UserID`.
    *   Пересылает `ClientStoryUpdate` клиенту по WebSocket.
*   **Примечание:** Обработка уведомлений от генерации `novel_setup` (после публикации) в `gameplay-service` **пока не реализована**. `gameplay-service` на данный момент обрабатывает только уведомления, связанные с `StoryConfig`.

## Текущая реализованная логика

На данный момент реализованы следующие основные возможности:

*   **Аутентификация пользователей:** Регистрация, вход, выход, обновление токенов (`auth-service`).
*   **Управление черновиками историй (`gameplay-service`):**
    *   **Начальная генерация:** Пользователь отправляет промпт (`/generate`), создается `StoryConfig`, отправляется задача `narrator` в `story-generator`.
    *   **Ревизия:** Пользователь отправляет правку к существующему черновику (`/revise`), `StoryConfig` обновляется, отправляется задача `narrator` (с текущим конфигом) в `story-generator`.

### List Public Stories

*   **`GET /api/published-stories/public`**
    *   Получение списка **публичных** опубликованных историй (`PublishedStory`) с offset/limit пагинацией.
    *   **Требует заголовок `Authorization`.** (Примечание: возможно, стоит сделать этот эндпоинт публичным, убрав middleware)
    *   Query параметры:
        *   `limit` (int, опционально, default=20, max=100): Количество.
        *   `offset` (int, опционально, default=0): Смещение.
    *   Ответ при успехе (`200 OK`):
        *   Тело ответа (JSON): `{"data": [PublishedStory, ...]}`
    *   Ответ при ошибке (`401 Unauthorized`).