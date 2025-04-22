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

Для доступа к большинству эндпоинтов (кроме регистрации, входа и обновления токена) необходимо передавать JWT access токен, полученный при входе или обновлении.
*   **HTTP запросы:** В заголовке `Authorization: Bearer <ваш_access_token>`.
*   **WebSocket соединение:** Через query-параметр `?token=<ваш_access_token>` при установке соединения.

### Основные эндпоинты

Ниже описаны основные эндпоинты, доступные для взаимодействия с пользователем и между сервисами. Маршрутизация осуществляется через API Gateway (Traefik).

---

#### Сервис Аутентификации (`/auth` и `/api`)

Предоставляет эндпоинты для управления пользователями и токенами.

##### Публичные эндпоинты (`/auth`)

*   **`POST /auth/register`**
    *   Описание: Регистрация нового пользователя.
    *   Аутентификация: **Не требуется.**
    *   Тело запроса (`application/json`):
        ```json
        {
          "username": "string", // 3-30 символов, [a-zA-Z0-9_-]
          "email": "string (valid email format)",
          "password": "string" // 8-100 символов
        }
        ```
    *   Ответ при успехе (`201 Created`):
        ```json
        {
          "id": "uuid-string", // UUID пользователя
          "username": "string",
          "email": "string"
          // Примечание: В коде обработчика возвращается только { "message": "user registered successfully" } ? Нужно уточнить.
        }
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request` (`{"code": 40001, "message": "..."}`): Невалидные данные (формат, длина).
        *   `409 Conflict` (`{"code": 40901, "message": "Username is already taken | Email is already taken"}`): Пользователь/email уже существует.
        *   `500 Internal Server Error` (`{"code": 50001, "message": "..."}`): Внутренняя ошибка.

*   **`POST /auth/login`**
    *   Описание: Вход пользователя.
    *   Аутентификация: **Не требуется.**
    *   Тело запроса (`application/json`):
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
        *   `400 Bad Request` (`{"code": 40001, "message": "..."}`): Невалидные данные.
        *   `401 Unauthorized` (`{"code": 40101, "message": "Invalid credentials or input format"}`): Неверный логин/пароль.
        *   `500 Internal Server Error` (`{"code": 50001, "message": "..."}`): Внутренняя ошибка.

*   **`POST /auth/refresh`**
    *   Описание: Обновление пары access/refresh токенов.
    *   Аутентификация: **Не требуется.**
    *   Тело запроса (`application/json`):
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
        *   `500 Internal Server Error` (`{"code": 50001, "message": "..."}`): Внутренняя ошибка.

*   **`POST /auth/logout`**
    *   Описание: Выход пользователя (отзыв refresh токена и связанных с ним access токенов).
    *   Аутентификация: **Не требуется** (токен передается в теле).
    *   Тело запроса (`application/json`):
        ```json
        {
          "refresh_token": "string (jwt)" // Токен, который нужно отозвать
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
        *   `401 Unauthorized` (`{"code": 40102 | 40103 | 40104, "message": "..."}`): Невалидный, просроченный или уже отозванный refresh токен.
        *   `500 Internal Server Error` (`{"code": 50001, "message": "..."}`): Внутренняя ошибка сервера.

*   **`POST /auth/token/verify`**
    *   Описание: Проверка валидности access токена (без проверки отзыва). Используется, например, другими сервисами для быстрой валидации без обращения к хранилищу отозванных токенов.
    *   Аутентификация: **Не требуется.**
    *   Тело запроса (`application/json`):
        ```json
        {
          "token": "string (jwt)"
        }
        ```
    *   Ответ при успехе (`200 OK`):
        ```json
        {
          "valid": true,
          "user_id": "uuid-string",
          "access_uuid": "uuid-string" // UUID самого токена
          // ... другие клеймы токена при необходимости
        }
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидное тело запроса.
        *   `401 Unauthorized`: Токен невалиден (не парсится, неверная подпись, истек срок). Возвращает `{"valid": false, "error": "reason..."}`.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

##### Защищенные эндпоинты (`/api`)

*   **`GET /api/me`**
    *   Описание: Получение информации о текущем аутентифицированном пользователе.
    *   Аутентификация: **Требуется** (`Authorization: Bearer <access_token>`).
    *   Тело запроса: Нет.
    *   Ответ при успехе (`200 OK`):
        ```json
        {
          "id": "uuid-string", // UUID пользователя
          "username": "string",
          "displayName": "string", // Отображаемое имя, может совпадать с username
          "email": "string",
          "roles": ["user", "..."], // Список ролей
          "isBanned": false
        }
        ```
    *   Ответ при ошибке:
        *   `401 Unauthorized` (`{"code": 40102 | 40103 | 40104, "message": "..."}`): Access токен невалиден, просрочен или отозван.
        *   `404 Not Found` (`{"code": 40402, "message": "User not found"}`): Пользователь, связанный с токеном, не найден в БД.
        *   `500 Internal Server Error` (`{"code": 50001, "message": "..."}`): Внутренняя ошибка сервера.

*   **`POST /api/device-tokens`**
    *   Описание: Регистрирует токен устройства для текущего аутентифицированного пользователя, чтобы получать push-уведомления. Если токен для этого пользователя уже существует, обновляет платформу и время последнего использования.
    *   Аутентификация: **Требуется** (`Authorization: Bearer <access_token>`).
    *   Тело запроса (`application/json`):
        ```json
        {
          "token": "string (device token)",  // Токен, полученный от FCM/APNS
          "platform": "string (ios|android)" // Платформа устройства ('ios' или 'android')
        }
        ```
    *   Ответ при успехе (`200 OK`):
        ```json
        {
          "message": "Device token registered successfully"
        }
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидное тело запроса (отсутствуют поля, неверный формат платформы, пустой токен).
        *   `401 Unauthorized`: Невалидный access токен.
        *   `500 Internal Server Error`: Ошибка базы данных при сохранении токена.

*   **`DELETE /api/device-tokens`**
    *   Описание: Удаляет указанный токен устройства из системы. Пользователь больше не будет получать push-уведомления на это устройство.
    *   Аутентификация: **Требуется** (`Authorization: Bearer <access_token>`).
    *   Тело запроса (`application/json`):
        ```json
        {
          "token": "string (device token)" // Токен, который нужно удалить
        }
        ```
    *   Ответ при успехе (`200 OK`):
        ```json
        {
          "message": "Device token unregistered successfully"
        }
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидное тело запроса (отсутствует или пустой токен).
        *   `401 Unauthorized`: Невалидный access токен.
        *   `500 Internal Server Error`: Ошибка базы данных при удалении токена. (Примечание: если токен не найден, ошибка не возвращается, операция считается успешной).

---

#### Сервис Геймплея (`/api`)

Управляет процессом создания, редактирования, публикации и прохождения историй.

**Важное замечание:** Внутри `gameplay-service` и в его API идентификация пользователей (`user_id`) и сущностей (черновики, опубликованные истории) происходит с использованием **`uuid.UUID`**. Это отличается от старого числового `user_id`, который мог использоваться ранее. Эндпоинт `/api/me` из сервиса `auth` также возвращает `user_id` как UUID.

##### Черновики историй (`/api/stories`)

*   **`POST /api/stories/generate`**
    *   Описание: Запуск генерации **нового** черновика истории на основе промпта.
    *   Аутентификация: **Требуется.**
    *   Тело запроса (`application/json`):
        ```json
        {
          "prompt": "Текст начального запроса пользователя...",
          "language": "string" // Код языка (например, "en", "ru"). Обязательное поле. Поддерживаемые: en, fr, de, es, it, pt, ru, zh, ja.
        }
        ```
    *   Ответ при успехе (`202 Accepted`): Возвращает созданный объект `StoryConfig` со статусом `generating`.
        ```json
        {
          "id": "uuid-string", // ID созданного черновика
          "status": "generating"
        }
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидное тело запроса (например, отсутствует `prompt` или `language`, неподдерживаемый язык).
        *   `401 Unauthorized`: Невалидный токен.
        *   `409 Conflict` (`{"message": "User already has an active generation task"}`): У пользователя уже есть активная задача генерации.
        *   `500 Internal Server Error`: Ошибка при создании записи в БД или постановке задачи в очередь. **Примечание:** Если ошибка произошла *после* создания записи, но *до* отправки задачи, тело ответа может содержать созданный `StoryConfig` со статусом `error`.

*   **`GET /api/stories`**
    *   Описание: Получение списка **своих** черновиков (`StoryConfig`). Поддерживает курсорную пагинацию.
    *   Аутентификация: **Требуется.**
    *   Query параметры:
        *   `limit` (int, опционально, default=10, max=100): Количество записей.
        *   `cursor` (string, опционально): Курсор для следующей страницы.
    *   Ответ при успехе (`200 OK`): Пагинированный список `StoryConfigSummary`.
        ```json
        {
          "data": [
            {
              "id": "uuid-string",
              "title": "string", // Может быть пустым, если генерация еще идет
              "description": "string", // Может быть user_input, если генерация еще идет
              "createdAt": "timestamp",
              "status": "generating | draft | error"
            }
            /* ... */
          ],
          "next_cursor": "string | null"
        }
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный курсор или `limit`.
        *   `401 Unauthorized`: Невалидный токен.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

*   **`GET /api/stories/:id`**
    *   Описание: Получение детальной информации о **своем** черновике по его UUID. Возвращает либо базовую информацию (если генерация не завершена/ошибка), либо распарсенный конфиг.
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:id` - UUID черновика (`StoryConfig`).
    *   Ответ при успехе (`200 OK`):
        *   **Статус `generating` или `error`:** `StoryConfigDetail`
            ```json
            {
              "id": "uuid-string",
              "createdAt": "timestamp",
              "status": "generating | error",
              "config": null // Поле config будет null
            }
            ```
        *   **Статус `draft`:** `StoryConfigParsedDetail` (распарсенные поля из `config`)
            ```json
            {
              "title": "string",
              "shortDescription": "string",
              "franchise": "string | null",
              "genre": "string",
              "language": "string",
              "isAdultContent": false,
              "playerName": "string",
              "playerDescription": "string",
              "worldContext": "string",
              "storySummary": "string",
              "coreStats": { // Словарь статов
                "stat_key_1": {
                  "description": "string",
                  "initialValue": 10,
                  "gameOverConditions": {
                    "min": false, // true, если Game Over при мин. значении
                    "max": false  // true, если Game Over при макс. значении
                  }
                },
                "stat_key_2": { ... }
              }
            }
            ```
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID.
        *   `401 Unauthorized`: Невалидный токен.
        *   `403 Forbidden`: Попытка доступа к чужому черновику.
        *   `404 Not Found`: Черновик не найден.
        *   `500 Internal Server Error`: Ошибка парсинга JSON конфига или другая внутренняя ошибка.

*   **`POST /api/stories/:id/revise`**
    *   Описание: Запуск задачи на **перегенерацию** существующего черновика на основе новой инструкции.
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:id` - UUID черновика (`StoryConfig`).
    *   Тело запроса (`application/json`):
        ```json
        {
          "revision_prompt": "Текст инструкции для изменения..." // Поле называется revision_prompt
        }
        ```
    *   Ответ при успехе (`202 Accepted`): **Пустое тело.** Статус черновика изменится на `generating`.
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID или тело запроса (отсутствует `revision_prompt`).
        *   `401 Unauthorized`: Невалидный токен.
        *   `403 Forbidden`: Попытка доступа к чужому черновику.
        *   `404 Not Found`: Черновик не найден.
        *   `409 Conflict` (`{"message": "Story config is not in draft state" | "User already has an active generation task"}`): Черновик не готов к ревизии или у пользователя уже есть задача.
        *   `500 Internal Server Error`: Ошибка при обновлении БД или постановке задачи.

*   **`POST /api/stories/:id/publish`**
    *   Описание: Публикация готового черновика. Создает запись `PublishedStory` и первую сцену на основе конфига.
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:id` - UUID черновика (`StoryConfig`).
    *   Тело запроса: Нет.
    *   Ответ при успехе (`201 Created`): Объект `PublishedStory` (или его ID?).
        ```json
        {
          "published_story_id": "uuid-string"
        }
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID.
        *   `401 Unauthorized`: Невалидный токен.
        *   `403 Forbidden`: Попытка доступа к чужому черновику.
        *   `404 Not Found`: Черновик не найден.
        *   `409 Conflict` (`{"message": "Story config is not in draft state"}`): Черновик не готов к публикации.
        *   `500 Internal Server Error`: Ошибка при создании `PublishedStory`, сцены или обновлении статуса черновика.

*   **`POST /api/stories/drafts/:draft_id/retry`**
    *   Описание: Повторный запуск задачи генерации для черновика, который завершился с ошибкой (`status: "error"`).
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:draft_id` - UUID черновика (`StoryConfig`).
    *   Тело запроса: Нет.
    *   Ответ при успехе (`202 Accepted`): Обновленный объект `StoryConfig` со статусом `generating`.
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID.
        *   `401 Unauthorized`: Невалидный токен.
        *   `403 Forbidden`: Попытка доступа к чужому черновику.
        *   `404 Not Found`: Черновик не найден.
        *   `409 Conflict` (`{"message": "Story config is not in error state" | "User already has an active generation task"}`): Черновик не в статусе ошибки или у пользователя уже есть задача.
        *   `500 Internal Server Error`: Ошибка при обновлении БД или постановке задачи.

*   **`DELETE /api/stories/drafts/:draft_id`**
    *   Описание: Удаление **своего** черновика истории.
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:draft_id` - UUID черновика (`StoryConfig`).
    *   Тело запроса: Нет.
    *   Ответ при успехе (`204 No Content`): Черновик успешно удален.
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID.
        *   `401 Unauthorized`: Невалидный токен.
        *   `403 Forbidden`: Попытка доступа к чужому черновику.
        *   `404 Not Found`: Черновик не найден.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера при удалении.

##### Опубликованные истории (`/api/published-stories`)

*   **`GET /api/published-stories/me`**
    *   Описание: Получение списка **своих** опубликованных историй. Поддерживает курсорную пагинацию.
    *   Аутентификация: **Требуется.**
    *   Query параметры: `limit`, `cursor` (аналогично `/api/stories`).
    *   Ответ при успехе (`200 OK`): Пагинированный список `sharedModels.PublishedStorySummaryWithProgress`.
        ```json
        {
          "data": [
            {
              "id": "uuid-string",
              "title": "string",
              "short_description": "string", // <-- Обновлено поле
              "author_id": "uuid-string",
              "author_name": "string", // <-- Добавлено имя автора
              "published_at": "timestamp",
              "is_adult_content": false, // <-- Обновлено поле
              "likes_count": 123,
              "is_liked": true,
              "hasPlayerProgress": false // Есть ли прогресс у текущего пользователя
              // "status": "..." // Статус больше не возвращается в Summary
            }
            /* ... */
          ],
          "next_cursor": "string | null"
        }
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный курсор или `limit`.
        *   `401 Unauthorized`: Невалидный токен.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

*   **`GET /api/published-stories/public`**
    *   Описание: Получение списка **публичных** опубликованных историй (доступных всем). Поддерживает курсорную пагинацию.
    *   Аутентификация: **Требуется** (проверяется токен, но доступ не ограничивается автором).
    *   Query параметры: `limit`, `cursor`.
    *   Ответ при успехе (`200 OK`): Пагинированный список `sharedModels.PublishedStorySummaryWithProgress`.
        ```json
        {
          "data": [
            {
              "id": "uuid-string",
              "title": "string",
              "short_description": "string", // <-- Обновлено поле
              "author_id": "uuid-string",
              "author_name": "string", // <-- Добавлено имя автора
              "published_at": "timestamp",
              "is_adult_content": false, // <-- Обновлено поле
              "likes_count": 123,
              "is_liked": false, // Лайкнул ли текущий пользователь
              "hasPlayerProgress": true // Есть ли прогресс у текущего пользователя
              // "status": "..." // Статус больше не возвращается в Summary
            }
            /* ... */
          ],
          "next_cursor": "string | null"
        }
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный курсор или `limit`.
        *   `401 Unauthorized`: Невалидный токен.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

*   **`GET /api/published-stories/:id`**
    *   Описание: Получение детальной информации об **одной** опубликованной истории с распарсенными полями конфига/сетапа.
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:id` - UUID опубликованной истории (`PublishedStory`).
    *   Ответ при успехе (`200 OK`): Объект `PublishedStoryParsedDetailDTO`.
        ```json
        {
          "id": "uuid-string", // ID истории
          "authorId": "uuid-string", // ID Автора
          "authorName": "string", // Имя автора
          "publishedAt": "timestamp-string", // Дата публикации (фактически время создания)
          "likesCount": number, // Количество лайков
          "isLiked": boolean, // Лайкнул ли историю текущий пользователь
          "isAuthor": boolean, // Является ли текущий пользователь автором истории
          "isPublic": boolean, // Является ли история публичной
          "isAdultContent": boolean, // Флаг 18+ (из конфига)
          "status": "ready | completed | error | setup_pending | generating_scene", // Текущий статус истории
          // Распарсенные поля из Config/Setup:
          "title": "string", // Название (из Config)
          "shortDescription": "string", // Краткое описание (из Config)
          // "franchise": "string | null", // Поле пока не извлекается
          "genre": "string", // Жанр (из Config)
          "language": "string", // Язык (из Config)
          "playerName": "string", // Имя игрока (из Config)
          // "playerDescription": "string", // Поле пока не извлекается
          // "worldContext": "string", // Поле пока не извлекается
          // "storySummary": "string", // Поле пока не извлекается
          "coreStats": { // Статы (из Setup)
            "statName1": {
              "description": "string",
              "initialValue": number,
              "min": number, // Минимальное значение (0 - нет)
              "max": number, // Максимальное значение (0 - нет)
              "gameOverMin": boolean, // Game Over при достижении Min?
              "gameOverMax": boolean // Game Over при достижении Max?
            },
            "statName2": { ... }
          },
          "characters": [ // Персонажи (из Setup)
            {
              "name": "string", // Имя персонажа
              "description": "string", // Описание
              "personality": "string | null" // Личность (опционально)
            }
            // ... другие персонажи
          ],
          // Информация о прогрессе:
          "hasPlayerProgress": true,
          "lastPlayedAt": "2024-03-10T15:30:00Z",
          "currentSceneIndex": 3,
          "currentSceneSummary": "You stand before the ancient gates...",
          "currentPlayerStats": {
            "strength": 12,
            "mana": 5
          }
        }
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID.
        *   `401 Unauthorized`: Невалидный токен.
        *   `403 Forbidden`: Нет доступа к приватной истории.
        *   `404 Not Found`: История не найдена.
        *   `500 Internal Server Error`: Внутренняя ошибка (например, ошибка парсинга JSON конфига/сетапа).

*   **`GET /api/published-stories/:id/scene`**
    *   Описание: Получение текущей сцены для **своей** игровой сессии в опубликованной истории. Если прогресса нет, возвращает начальную сцену.
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:id` - UUID опубликованной истории (`PublishedStory`).
    *   Ответ при успехе (`200 OK`): Объект сцены (`GameSceneResponse`).
        ```json
        {
          "id": "uuid-string", // ID текущей сцены
          "publishedStoryId": "uuid-string", // ID опубликованной истории
          "content": { // Содержимое сцены
            "type": "choices | game_over | continuation", // Тип сцены
            // --- Только для type="choices" или "continuation" ---
            "ch": [ // Массив блоков выбора (если есть)
              {
                "sh": 0, // 0 - не перемешивать, 1 - перемешивать
                "desc": "Описание блока/ситуации выбора",
                "opts": [ // Массив опций в блоке
                  {
                    "txt": "Текст опции 1",
                    "cons": { // Последствия выбора (для отображения)
                      "cs_chg": { // Изменения статов (если есть)
                        "stat_key_1": -1,
                        "stat_key_2": 5
                      },
                      "resp_txt": "Текст-реакция на выбор (если есть)"
                    }
                  },
                  { "txt": "Текст опции 2", "cons": { ... }}
                ]
              }
              // ... другие блоки выбора
            ],
            // --- Только для type="game_over" ---
            "et": "Текст концовки игры...",
            // --- Только для type="continuation" ---
            "npd": "Описание нового персонажа...",
            "csr": { // Новые базовые статы
              "stat_key_1": 10, "stat_key_3": 15
            },
            "etp": "Текст концовки для предыдущего персонажа..."
          }
        }
        ```
        *   **Примечание:** Текущие статы игрока не возвращаются в этом ответе. Изменения статов (`cs_chg`) показывают, как изменится значение при выборе опции.
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID.
        *   `401 Unauthorized`: Невалидный токен.
        *   `404 Not Found`: История или прогресс не найдены.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

*   **`POST /api/published-stories/:id/choice`**
    *   Описание: Отправка выбора игрока для текущей сцены. Генерирует следующую сцену (асинхронно) и возвращает ее, либо информацию об ожидании генерации.
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:id` - UUID опубликованной истории (`PublishedStory`).
    *   Тело запроса (`application/json`):
        ```json
        {
          "selected_option_indices": [ 0, 1 ] // Массив индексов выбранных опций (по одному индексу на каждый блок 'ch' в текущей сцене)
        }
        ```
    *   Ответ при успехе (`202 Accepted`): **Пустое тело.** Сервер принял выбор и запустил генерацию следующей сцены (или пересчет состояния). Клиенту нужно будет запросить новую сцену через `GET /api/published-stories/:id/scene`, когда она будет готова (можно использовать WebSocket для уведомления или периодический опрос).
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID, невалидное тело запроса (`selected_option_indices` отсутствует, содержит неверные индексы или неверное количество индексов).
        *   `401 Unauthorized`: Невалидный токен.
        *   `404 Not Found`: История, прогресс или выбор не найдены.
        *   `409 Conflict`: Попытка сделать выбор не для текущей сцены или пока идет генерация.
        *   `500 Internal Server Error`: Ошибка сохранения прогресса или постановки задачи генерации.

*   **`DELETE /api/published-stories/:id/progress`**
    *   Описание: Сброс прогресса прохождения для **своей** игровой сессии в опубликованной истории. Позволяет начать историю заново.
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:id` - UUID опубликованной истории (`PublishedStory`).
    *   Ответ при успехе (`204 No Content`): Прогресс успешно удален.
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID.
        *   `401 Unauthorized`: Невалидный токен.
        *   `404 Not Found`: История или прогресс не найдены.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

*   **`POST /api/published-stories/:id/like`**
    *   Описание: Поставить лайк опубликованной истории.
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:id` - UUID опубликованной истории (`PublishedStory`).
    *   Тело запроса: Нет.
    *   Ответ при успехе (`204 No Content`): Лайк успешно поставлен.
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID.
        *   `401 Unauthorized`: Невалидный токен.
        *   `404 Not Found`: История не найдена.
        *   `409 Conflict` (`{"message": "story already liked by this user"}`): Пользователь уже лайкнул эту историю.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

*   **`DELETE /api/published-stories/:id/like`**
    *   Описание: Убрать лайк с опубликованной истории.
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:id` - UUID опубликованной истории (`PublishedStory`).
    *   Тело запроса: Нет.
    *   Ответ при успехе (`204 No Content`): Лайк успешно убран.
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID.
        *   `401 Unauthorized`: Невалидный токен.
        *   `404 Not Found` (`{"message": "story not liked by this user yet"}`): Пользователь не лайкал эту историю.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

*   **`DELETE /api/published-stories/:id`**
    *   Описание: Удаление **своей** опубликованной истории.
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:id` - UUID опубликованной истории (`PublishedStory`).
    *   Тело запроса: Нет.
    *   Ответ при успехе (`204 No Content`): История успешно удалена.
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID.
        *   `401 Unauthorized`: Невалидный токен.
        *   `403 Forbidden`: Попытка доступа к чужой истории.
        *   `404 Not Found`: История не найдена.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера при удалении.

*   **`POST /api/published-stories/:id/retry`**
    *   Описание: Повторный запуск задачи генерации (Setup или Scene) для опубликованной истории, которая завершилась с ошибкой (`status: "error"`).
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:id` - UUID опубликованной истории (`PublishedStory`).
    *   Тело запроса: Нет.
    *   Ответ при успехе (`202 Accepted`): **Пустое тело.** Статус истории изменится на `setup_pending` или `generating_scene`.
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID.
        *   `401 Unauthorized`: Невалидный токен.
        *   `404 Not Found`: История не найдена.
        *   `409 Conflict` (`{"message": "Story is not in error state"}`): История не в статусе ошибки.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

*   **`GET /api/published-stories/me/progress`**
    *   Описание: Получение списка историй, в которых у текущего пользователя есть прогресс прохождения.
    *   Аутентификация: **Требуется.**
    *   Query параметры: `limit`, `cursor`.
    *   Ответ при успехе (`200 OK`): Пагинированный список `sharedModels.PublishedStorySummaryWithProgress`.
        ```json
        {
          "data": [
            {
              "id": "uuid-string",
              "title": "string",
              "short_description": "string",
              "author_id": "uuid-string",
              "author_name": "string",
              "published_at": "timestamp",
              "is_adult_content": false,
              "likes_count": 42,
              "is_liked": true, // Лайкнул ли эту историю текущий пользователь
              "hasPlayerProgress": true // Всегда true для этого эндпоинта
            }
            /* ... */
          ],
          "next_cursor": "string | null"
        }
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный курсор или `limit`.
        *   `401 Unauthorized`: Невалидный токен.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

*   **`GET /api/published-stories/me/likes`**
    *   Описание: Получение списка историй, которые лайкнул пользователь.
    *   Аутентификация: **Требуется.**
    *   Query параметры: `limit`, `cursor`.
    *   Ответ при успехе (`200 OK`): Пагинированный список `sharedModels.PublishedStorySummaryWithProgress`.
        ```json
        {
          "data": [
            {
              "id": "uuid-string",
              "title": "string",
              "short_description": "string", // <-- Обновлено поле
              "author_id": "uuid-string",
              "author_name": "string", // <-- Добавлено имя автора
              "published_at": "timestamp",
              "is_adult_content": false, // <-- Обновлено поле
              "likes_count": 123,
              "is_liked": true, // Всегда true для этого эндпоинта
              "hasPlayerProgress": false // Есть ли прогресс у текущего пользователя
            }
            /* ... */
          ],
          "next_cursor": "string | null"
        }
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный курсор или `limit`.
        *   `401 Unauthorized`: Невалидный токен.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

---