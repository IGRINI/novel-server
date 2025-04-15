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

Все запросы к бэкенду должны отправляться через API Gateway (Traefik). Убедитесь, что порт Traefik (по умолчанию 8080, но может быть изменен в `docker-compose.yml` в секции `ports` для `api-gateway`) доступен.

*   **Базовый URL API (HTTP):** `http://<ваш_хост>:<порт_traefik_web>`
*   **WebSocket URL:** `ws://<ваш_хост>:<порт_traefik_web>/ws`
*   **Traefik Dashboard:** `http://<ваш_хост>:<порт_traefik_dashboard>` (по умолчанию порт 8888)

### Аутентификация

Для доступа к большинству эндпоинтов (кроме регистрации и входа) необходимо передавать JWT access токен.
*   **HTTP запросы:** В заголовке `Authorization: Bearer <ваш_access_token>`.
*   **WebSocket соединение:** Через query-параметр `?token=<ваш_access_token>` при установке соединения.

### Основные эндпоинты

Ниже описаны основные эндпоинты, доступные для взаимодействия с пользователем.

---

#### Сервис Аутентификации (`/api/auth`)

*   **`POST /api/auth/register`**
    *   Описание: Регистрация нового пользователя.
    *   Аутентификация: **Не требуется.**
    *   Тело запроса (JSON):
        ```json
        {
          "username": "string",
          "email": "string (valid email format)",
          "password": "string"
        }
        ```
    *   **Требования к полям:**
        *   `username`: Длина от 3 до 30 символов. Допустимы только латинские буквы (a-z, A-Z), цифры (0-9), знаки подчеркивания (`_`) и дефисы (`-`).
        *   `password`: Длина от 8 до 100 символов. Должен содержать хотя бы одну букву и хотя бы одну цифру.
    *   Ответ при успехе (`201 Created`):
        ```json
        {
          "message": "user registered successfully",
          "user_id": 123,
          "username": "string",
          "email": "string"
        }
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request` (`{"code": 40001, "message": "..."}`): Невалидные данные запроса (включая несоблюдение требований к длине/формату username и password).
        *   `409 Conflict` (`{"code": 40901, "message": "..."}`): Пользователь или email уже существует.
        *   `500 Internal Server Error` (`{"code": 50001, "message": "..."}`): Внутренняя ошибка сервера.

*   **`POST /api/auth/login`**
    *   Описание: Вход пользователя.
    *   Аутентификация: **Не требуется.**
    *   Тело запроса (JSON):
        ```json
        {
          "username": "string",
          "password": "string"
        }
        ```
    *   Ответ при успехе (`200 OK`):
        ```json
        {
          "access_token": "string (jwt)",
          "refresh_token": "string (jwt)"
        }
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request` (`{"code": 40001, "message": "..."}`): Невалидные данные запроса.
        *   `401 Unauthorized` (`{"code": 40101, "message": "..."}`): Неверный логин или пароль.
        *   `500 Internal Server Error` (`{"code": 50001, "message": "..."}`): Внутренняя ошибка сервера.

*   **`POST /api/auth/refresh`**
    *   Описание: Обновление пары access/refresh токенов.
    *   Аутентификация: **Не требуется.**
    *   Тело запроса (JSON):
        ```json
        {
          "refresh_token": "string (jwt)"
        }
        ```
    *   Ответ при успехе (`200 OK`):
        ```json
        {
          "access_token": "string (jwt)",
          "refresh_token": "string (jwt)"
        }
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request` (`{"code": 40001, "message": "..."}`): Невалидное тело запроса.
        *   `401 Unauthorized` (`{"code": 40102 | 40103 | 40104, "message": "..."}`): Невалидный, просроченный или отозванный refresh токен.
        *   `500 Internal Server Error` (`{"code": 50001, "message": "..."}`): Внутренняя ошибка сервера.

*   **`POST /api/auth/logout`**
    *   Описание: Выход пользователя (отзыв токенов).
    *   Аутентификация: **Требуется** (валидный `access_token` в заголовке `Authorization`).
    *   Тело запроса (JSON):
        ```json
        {
          "refresh_token": "string (jwt)" // Токен, который нужно отозвать вместе с текущим access токеном
        }
        ```
    *   Ответ при успехе (`200 OK`):
        ```json
        {
          "message": "Successfully logged out"
        }
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request` (`{"code": 40001, "message": "..."}`): Отсутствует или невалиден refresh токен в теле.
        *   `401 Unauthorized` (`{"code": 40102 | 40103, "message": "..."}`): Access токен невалиден или просрочен.
        *   `500 Internal Server Error` (`{"code": 50001, "message": "..."}`): Внутренняя ошибка сервера.

---

#### Информация о пользователе (`/api`)

*   **`GET /api/me`**
    *   Описание: Получение информации о текущем аутентифицированном пользователе.
    *   Аутентификация: **Требуется.**
    *   Тело запроса: Нет.
    *   Ответ при успехе (`200 OK`):
        ```json
        {
          "id": 123,
          "username": "string",
          "email": "string",
          "roles": ["user", "..."], // Список ролей
          "isBanned": false
        }
        ```
    *   Ответ при ошибке:
        *   `401 Unauthorized` (`{"code": 40102 | 40103, "message": "..."}`): Access токен невалиден или просрочен.
        *   `404 Not Found` (`{"code": 40402, "message": "..."}`): Пользователь, связанный с токеном, не найден в БД.
        *   `500 Internal Server Error` (`{"code": 50001, "message": "..."}`): Внутренняя ошибка сервера.

---

#### Сервис Геймплея: Черновики (`/api/stories`)

*   **`POST /api/stories/generate`**
    *   Описание: Запуск генерации нового черновика истории на основе промпта.
    *   Аутентификация: **Требуется.**
    *   Тело запроса (JSON):
        ```json
        {
          "prompt": "Текст начального запроса пользователя..."
        }
        ```
    *   Ответ при успехе (`202 Accepted`): Объект `StoryConfig` с `status: "generating"`.
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
    *   Ответ при ошибке:
        *   `401 Unauthorized`: Невалидный токен.
        *   `409 Conflict` (`{"message": "User already has an active generation task"}`): У пользователя уже есть активная задача генерации.
        *   `500 Internal Server Error`: Ошибка при постановке задачи в очередь (ответ будет содержать объект `StoryConfig` со `status: "error"`).

*   **`GET /api/stories`**
    *   Описание: Получение списка **своих** черновиков. Поддерживает курсорную пагинацию.
    *   Аутентификация: **Требуется.**
    *   Query параметры:
        *   `limit` (int, опционально, default=10, max=100): Количество записей на странице.
        *   `cursor` (string, опционально): Курсор из поля `next_cursor` предыдущего ответа для получения следующей страницы.
    *   Ответ при успехе (`200 OK`):
        ```json
        {
          "data": [
            {
              "id": "uuid-string",
              "title": "string",
              "description": "string",
              "createdAt": "timestamp",
              "status": "generating | draft | error"
            }
            /* ... другие StoryConfigSummary ... */
          ],
          "next_cursor": "string | null" // Курсор для следующей страницы или null
        }
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный курсор или `limit`.
        *   `401 Unauthorized`: Невалидный токен.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

*   **`GET /api/stories/:id`**
    *   Описание: Получение детальной информации о конкретном черновике истории по его UUID. Возвращаемая структура зависит от наличия сгенерированного конфига.
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:id` - UUID черновика (`StoryConfig`).
    *   Ответ при успехе (`200 OK`):
        *   **Если `config` еще не сгенерирован (или ошибка генерации):**
            ```json
            {
              "id": "uuid-string",
              "createdAt": "timestamp",
              "status": "generating | error",
              "config": null
            }
            ```
        *   **Если `config` успешно сгенерирован:** Возвращается объект с выбранными полями из распарсенного конфига:
            ```json
            {
              "title": "string",
              "shortDescription": "string",
              "franchise": "string",
              "genre": "string",
              "language": "string",
              "isAdultContent": boolean,
              "playerName": "string",
              "playerDescription": "string",
              "worldContext": "string",
              "storySummary": "string",
              "coreStats": {
                "stat_name_1": {
                  "description": "string",
                  "initialValue": 50,
                  "gameOverConditions": { "min": boolean, "max": boolean }
                },
                "stat_name_2": { ... },
                "stat_name_3": { ... },
                "stat_name_4": { ... }
              }
            }
            ```
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный формат UUID.
        *   `401 Unauthorized`: Невалидный токен.
        *   `404 Not Found` (`{"message": "Resource not found or access denied"}`): Черновик не найден или принадлежит другому пользователю.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера (включая ошибку парсинга существующего `config`).

*   **`POST /api/stories/:id/revise`**
    *   Описание: Отправка запроса на ревизию (изменение) существующего черновика.
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:id` - UUID черновика (`StoryConfig`).
    *   Тело запроса (JSON):
        ```json
        {
          "revision_prompt": "Текст правок от пользователя..."
        }
        ```
    *   Ответ при успехе (`202 Accepted`): **Нет тела ответа.** Статус черновика изменится на `revising`. Обновленный черновик будет отправлен по WebSocket после завершения генерации.
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный формат UUID или тело запроса.
        *   `401 Unauthorized`: Невалидный токен.
        *   `404 Not Found`: Черновик не найден или принадлежит другому пользователю.
        *   `409 Conflict` (`{"message": "Cannot revise story with status: ..."}`): Черновик не находится в статусе `draft` или `error`.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

*   **`POST /api/stories/:id/publish`**
    *   Описание: Публикация завершенного черновика. Создает опубликованную историю (`PublishedStory`), удаляет черновик (`StoryConfig`) и запускает генерацию начального игрового состояния (`Setup`).
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:id` - UUID черновика (`StoryConfig`).
    *   Тело запроса: Нет.
    *   Ответ при успехе (`202 Accepted`):
        ```json
        {
          "published_story_id": "uuid-string" // UUID созданной опубликованной истории
        }
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request` (`{"message": "Cannot publish story: config is missing or status is not draft/error"}`): Черновик не готов к публикации (нет `config` или статус не `draft`/`error`).
        *   `401 Unauthorized`: Невалидный токен.
        *   `404 Not Found`: Черновик не найден или принадлежит другому пользователю.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

---

#### Сервис Геймплея: Опубликованные истории (`/api/published-stories`)

*   **`GET /api/published-stories/me`**
    *   Описание: Получение списка **своих** опубликованных историй (`PublishedStory`). Поддерживает offset/limit пагинацию.
    *   Аутентификация: **Требуется.**
    *   Query параметры:
        *   `limit` (int, опционально, default=20, max=100): Количество.
        *   `offset` (int, опционально, default=0): Смещение.
    *   Ответ при успехе (`200 OK`):
        ```json
        {
          "data": [ /* массив объектов PublishedStory */ ]
          // "next_cursor": null // В текущей реализации пагинация offset/limit, курсора нет
        }
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный `limit` или `offset`.
        *   `401 Unauthorized`: Невалидный токен.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

*   **`GET /api/published-stories/public`**
    *   Описание: Получение списка **публичных** опубликованных историй (`PublishedStory`). Поддерживает offset/limit пагинацию.
    *   Аутентификация: **Требуется** (на данный момент).
    *   Query параметры:
        *   `limit` (int, опционально, default=20, max=100): Количество.
        *   `offset` (int, опционально, default=0): Смещение.
    *   Ответ при успехе (`200 OK`):
        ```json
        {
          "data": [ /* массив объектов PublishedStory */ ]
        }
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный `limit` или `offset`.
        *   `401 Unauthorized`: Невалидный токен.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

*   **`GET /api/published-stories/:id/scene`**
    *   Описание: Получение текущей игровой сцены для указанной опубликованной истории. Создает начальный прогресс, если его нет.
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:id` - UUID опубликованной истории (`PublishedStory`).
    *   Ответ при успехе (`200 OK`): Объект `StoryScene`.
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
        *   `404 Not Found` (`{"message": "Resource not found or access denied"}`): История не найдена.
        *   `409 Conflict` (`{"message": "Story setup is pending, check back later"}`): История еще не готова к игре (статус `setup_pending`).
        *   `409 Conflict` (`{"message": "Scene generation is pending, check back later"}`): Текущая сцена для данного состояния еще не сгенерирована.
        *   `500 Internal Server Error`: Другие ошибки сервера.

*   **`POST /api/published-stories/:id/choice`**
    *   Описание: Отправка выбора игрока в текущей сцене. Запускает процесс обновления состояния и генерации следующей сцены.
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:id` - UUID опубликованной истории (`PublishedStory`).
    *   Тело запроса (JSON):
        ```json
        {
          "selected_option_index": 0 // Индекс выбранной опции (0 или 1)
        }
        ```
    *   Ответ при успехе (`204 No Content`): **Нет тела ответа.** Клиент должен будет запросить новую сцену через `GET /api/published-stories/:id/scene` после получения WebSocket уведомления или через некоторое время.
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный индекс выбора или тело запроса.
        *   `401 Unauthorized`: Невалидный токен.
        *   `404 Not Found`: История, прогресс игрока или текущая сцена не найдены.
        *   `409 Conflict`: История не в статусе 'ready'.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

*   **`DELETE /api/published-stories/:id/progress`**
    *   Описание: Удаление прогресса текущего пользователя для указанной опубликованной истории. Позволяет начать историю заново.
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:id` - UUID опубликованной истории (`PublishedStory`).
    *   Тело запроса: Нет.
    *   Ответ при успехе (`204 No Content`): **Нет тела ответа.**
    *   Ответ при ошибке:
        *   `401 Unauthorized`: Невалидный токен.
        *   `404 Not Found`: История не найдена.
        *   `500 Internal Server Error`: Ошибка при удалении прогресса.

---

#### WebSocket Уведомления (`/ws`)

*   **URL для подключения:** `ws://<ваш_хост>:<порт_traefik_web>/ws?token=<ваш_access_token>`
*   **Аутентификация:** Через query-параметр `token`.
*   **Сообщения Сервер -> Клиент:**
    *   При обновлении статуса черновика (`StoryConfig`) или опубликованной истории (`PublishedStory`) сервер отправит JSON-сообщение `ClientStoryUpdate`:
        ```json
        // Пример для обновления черновика
        {
          "id": "uuid-draft-id",        // ID обновленного StoryConfig
          "user_id": "123",             // ID пользователя
          "status": "draft",            // Новый статус: draft или error
          "title": "Новое Название",    // Сгенерированное/обновленное
          "description": "Новое Описание", // Сгенерированное/обновленное
          "themes": ["theme1", "..."],  // Из конфига
          "world_lore": ["lore1", "..."], // Из конфига
          "player_description": "...",  // Из конфига
          "error_details": null         // или "текст ошибки" если status == "error"
        }

        // Пример для уведомления о готовности опубликованной истории
        {
          "id": "uuid-published-id",    // ID обновленной PublishedStory
          "user_id": "123",             // ID пользователя
          "status": "ready",            // Новый статус
          "isCompleted": false,
          "title": null, // Эти поля не передаются для PublishedStory
          "description": null,
          "themes": null,
          "world_lore": null,
          "player_description": null,
          "error_details": null,
          "endingText": null
        }

        // Пример для уведомления о завершении игры
        {
           "id": "uuid-published-id",
           "user_id": "123",
           "status": "completed",
           "isCompleted": true,
           "endingText": "Текст концовки...",
           // ... остальные поля null ...
        }
        ```
    *   Клиент должен использовать `id` и `status` из этого сообщения, чтобы решить, когда запрашивать обновленные данные через соответствующие HTTP эндпоинты (например, `GET /api/stories/:id` или `GET /api/published-stories/:id/scene`).

---

#### Внутренние API (Internal)

Эти эндпоинты используются для взаимодействия между сервисами и **не доступны** через основной API Gateway.

## Задачи для генерации (Story Generator)

*   Сервис `gameplay-service` теперь отправляет задачи в очередь `story_generation_tasks` для:
    *   Начальной генерации (`prompt_type: narrator`)
    *   Ревизии (`prompt_type: narrator` с `input_data.current_config`)
    *   Генерации начального состояния игры (`prompt_type: novel_setup`)
    *   Генерации следующей сцены (`prompt_type: novel_creator`)
    *   Генерации концовки (`prompt_type: novel_game_over_creator`)
*   `story-generator` получает задачи, выполняет их и отправляет **полные** уведомления (`shared/messaging.NotificationPayload`) в очередь `internal_updates`.
*   **Формат `InputData` для `novel_creator`:** Использует сжатые ключи (`cfg`, `stp`, `cs`, `uc`, `pss`, `pfd`, `pvis`, `sv`, `gf`), где `sv` и `gf` содержат данные только последнего выбора. См. `promts/novel_creator.md`.

## Поток Уведомлений

1.  `story-generator` -> `internal_updates` (полное `NotificationPayload` с результатом генерации: `narrator`, `novel_setup`, `novel_creator`, `novel_game_over_creator`)
2.  `gameplay-service` слушает `internal_updates`:
    *   **Для `narrator`:** Обновляет `StoryConfig` (статус `draft`, `Config`, `Title`, `Description`). Формирует `ClientStoryUpdate`. Отправляет в `client_updates`.
    *   **Для `novel_setup`:** Обновляет `PublishedStory` (статус `first_scene_pending`, поле `Setup`). Запускает задачу генерации первой сцены (`novel_first_scene_creator`).
    *   **Для `novel_first_scene_creator` и `novel_creator`:** Создает `StoryScene`. Обновляет статус `PublishedStory` на `ready`. Формирует `ClientStoryUpdate` (только ID, UserID, Status='ready'). Отправляет в `client_updates`.
    *   **Для `novel_game_over_creator`:** Создает `StoryScene` (с текстом концовки). Обновляет статус `PublishedStory` на `completed`. Формирует `ClientStoryUpdate` (ID, UserID, Status='completed', IsCompleted=true, EndingText=...). Отправляет в `client_updates`.
3.  `websocket-service` слушает `client_updates`:
    *   Получает `ClientStoryUpdate`.
    *   Находит WebSocket соединение для нужного `UserID`.
    *   Пересылает `ClientStoryUpdate` клиенту по WebSocket.

## Текущая реализованная логика

На данный момент реализованы следующие основные возможности:

*   **Аутентификация пользователей:** Регистрация, вход, выход, обновление токенов (`auth-service`).
*   **Управление черновиками историй (`gameplay-service`):**
    *   Начальная генерация и ревизия (`narrator`).
    *   Получение списка и деталей черновиков.
    *   Публикация черновика (удаляет черновик, создает `PublishedStory`, запускает генерацию `novel_setup`).
*   **Игровой процесс (`gameplay-service`):**
    *   Получение текущей сцены (`GET .../scene`). Учитывает статус истории и наличие сцены. Создает начальный прогресс игрока, если его нет.
    *   Обработка выбора игрока (`POST .../choice`):
        *   Принимает индекс одного выбора (`selected_option_index`).
        *   Применяет последствия выбора к `CoreStats`, `StoryVariables`, `GlobalFlags`.
        *   Рассчитывает новый хэш состояния по принципу "блокчейна": `hash(previousHash + cs + last_sv + last_gf)`.
        *   Проверяет наличие следующей сцены по новому хэшу.
        *   Если сцена не найдена: запускает задачу генерации `novel_creator`, передавая сводки (`pss`, `pfd`, `pvis`), статы (`cs`), выбор (`uc`) и `sv`/`gf` последнего шага.
        *   Если сцена найдена: извлекает из нее сводки (`sssf`, `fd`, `vis`) для обновления прогресса.
        *   **Очищает `StoryVariables` и `GlobalFlags`** в `PlayerProgress` перед сохранением.
        *   Сохраняет `PlayerProgress` с новым хэшем, обновленными статами, очищенными `sv`/`gf` и (если сцена найдена) новыми сводками.
        *   **Очищает `StoryVariables` и `GlobalFlags`** в `PlayerProgress` перед сохранением.
        *   Сохраняет `PlayerProgress` с новым хэшем, обновленными статами, очищенными `sv`/`gf` и (если сцена найдена) новыми сводками.
    *   Удаление прогресса игрока (`DELETE .../progress`).
*   **Генерация контента (`story-generator`):** Обрабатывает задачи `narrator`, `novel_setup`, `novel_creator`, `novel_game_over_creator`. Генерирует JSON в сжатом формате с использованием сводок (`vis`).
*   **Уведомления (`websocket-service`):** Доставляет `ClientStoryUpdate` об изменениях статуса `StoryConfig` и `PublishedStory`.