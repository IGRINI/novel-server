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

### Генерация и доступ к изображениям

Сгенерированные изображения (превью историй и портреты персонажей) сохраняются локально и доступны через API Gateway по следующему базовому URL:

*   **Базовый URL изображений:** `https://crion.space/generated-images`

Конкретные URL формируются и используются следующим образом:

*   **Превью опубликованной истории:**
    *   **Получение URL:** Полный URL превью возвращается API в поле `coverImageUrl` (или аналогичном) в ответах эндпоинтов, возвращающих информацию об опубликованных историях (например, `GET /api/published-stories/me`, `GET /api/published-stories/public`, `GET /api/published-stories/:story_id`). Если превью еще не сгенерировано, значение поля будет `null`.
    *   Формат URL: `[Базовый URL изображений]/history_preview_{publishedStoryID}.jpg`
    *   Пример: `https://crion.space/generated-images/history_preview_a1b2c3d4-e5f6-7890-1234-567890abcdef.jpg`
    *   Где `{publishedStoryID}` - это UUID опубликованной истории.

*   **Изображение персонажа:**
    *   **Получение URL:** Бэкенд **не** возвращает готовые URL изображений персонажей. Клиент должен:
        1.  Получить поле `imageReference` из данных Setup истории (например, через `GET /api/published-stories/:story_id` в поле `characters[].imageReference`).
        2.  Самостоятельно сконструировать полный URL, используя базовый URL изображений и полученный `imageReference`.
    *   Формат URL: `[Базовый URL изображений]/{imageReference}.jpg`
    *   Где `{imageReference}` - это уникальный идентификатор (например, `ch_male_adult_wizard_...`).
    *   Пример: `https://crion.space/generated-images/ch_male_adult_wizard_abc123.jpg`

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
        }
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request` (`{\"code\": 40001, \"message\": \"...\"}`): Невалидные данные (формат, длина).
        *   `409 Conflict` (`{\"code\": 40901, \"message\": \"Username is already taken | Email is already taken\"}`): Пользователь/email уже существует.
        *   `500 Internal Server Error` (`{\"code\": 50001, \"message\": \"...\"}`): Внутренняя ошибка.

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
        *   `400 Bad Request` (`{\"code\": 40001, \"message\": \"...\"}`): Невалидные данные.
        *   `401 Unauthorized` (`{\"code\": 40101, \"message\": \"Invalid credentials or input format\"}`): Неверный логин/пароль.
        *   `500 Internal Server Error` (`{\"code\": 50001, \"message\": \"...\"}`): Внутренняя ошибка.

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
        *   `400 Bad Request` (`{\"code\": 40001, \"message\": \"...\"}`): Невалидное тело запроса.
        *   `401 Unauthorized` (`{\"code\": 40102 | 40103 | 40104, \"message\": \"...\"}`): Невалидный, просроченный или отозванный refresh токен.
        *   `500 Internal Server Error` (`{\"code\": 50001, \"message\": \"...\"}`): Внутренняя ошибка.

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
        *   `400 Bad Request` (`{\"code\": 40001, \"message\": \"...\"}`): Отсутствует или невалиден refresh токен в теле.
        *   `401 Unauthorized` (`{\"code\": 40102 | 40103 | 40104, \"message\": \"...\"}`): Невалидный, просроченный или уже отозванный refresh токен.
        *   `500 Internal Server Error` (`{\"code\": 50001, \"message\": \"...\"}`): Внутренняя ошибка сервера.

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
        *   `401 Unauthorized`: Токен невалиден (не парсится, неверная подпись, истек срок). Возвращает `{\"valid\": false, \"error\": \"reason...\"}`.
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
          "display_name": "string", // Отображаемое имя, может совпадать с username
          "email": "string",
          "roles": ["user", "..."], // Список ролей
          "is_banned": false
        }
        ```
    *   Ответ при ошибке:
        *   `401 Unauthorized` (`{\"code\": 40102 | 40103 | 40104, \"message\": \"...\"}`): Access токен невалиден, просрочен или отозван.
        *   `404 Not Found` (`{\"code\": 40402, \"message\": \"User not found\"}`): Пользователь, связанный с токеном, не найден в БД.
        *   `500 Internal Server Error` (`{\"code\": 50001, \"message\": \"...\"}`): Внутренняя ошибка сервера.

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
              "created_at": "timestamp",
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
              "created_at": "timestamp",
              "status": "generating | error",
              "config": null // Поле config будет null
            }
            ```
        *   **Статус `draft`:** `StoryConfigParsedDetail` (распарсенные поля из `config`)
            ```json
            {
              "title": "string",
              "short_description": "string",
              "franchise": "string | null",
              "genre": "string",
              "language": "string",
              "is_adult_content": false,
              "player_name": "string",
              "player_description": "string",
              "world_context": "string",
              "story_summary": "string",
              "core_stats": { // Словарь статов
                "stat_key_1": {
                  "description": "string",
                  "initial_value": 10,
                  "game_over_conditions": {
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

*   **`DELETE /api/stories/:id`**
    *   Описание: Удаление **своего** черновика истории.
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:id` - UUID черновика (`StoryConfig`).
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
              "short_description": "string | null", // (snake_case)
              "author_id": "uuid-string", // (snake_case)
              "author_name": "string", // (snake_case)
              "published_at": "timestamp-string", // (snake_case)
              "is_adult_content": false, // (snake_case)
              "likes_count": 123, // (snake_case)
              "is_liked": true, // (snake_case)
              "has_player_progress": false, // (snake_case)
              "status": "ready | error | ...",
              "is_public": true, // (snake_case)
              "cover_image_url": "https://crion.space/generated-images/history_preview_...jpg | null" // (snake_case, omitempty)
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
              "short_description": "string | null", // (snake_case)
              "author_id": "uuid-string", // (snake_case)
              "author_name": "string", // (snake_case)
              "published_at": "timestamp-string", // (snake_case)
              "is_adult_content": false, // (snake_case)
              "likes_count": 123, // (snake_case)
              "is_liked": false, // (snake_case)
              "has_player_progress": true, // (snake_case)
              "status": "ready | error | ...",
              "is_public": true, // (snake_case)
              "cover_image_url": "https://crion.space/generated-images/history_preview_...jpg | null" // (snake_case, omitempty)
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

*   **`GET /api/published-stories/:story_id`**
    *   Описание: Получение детальной информации об **одной** опубликованной истории с распарсенными полями конфига/сетапа и списком сохранений текущего пользователя.
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:story_id` - UUID опубликованной истории (`PublishedStory`).
    *   Ответ при успехе (`200 OK`): Объект `PublishedStoryParsedDetailDTO`.
        ```json
        {
          "id": "uuid-string",
          "author_id": "uuid-string", // camelCase -> snake_case
          "author_name": "string", // camelCase -> snake_case
          "published_at": "timestamp-string", // camelCase -> snake_case
          "likes_count": 15, // camelCase -> snake_case
          "is_liked": true, // camelCase -> snake_case
          "is_author": false, // camelCase -> snake_case
          "is_public": true, // camelCase -> snake_case
          "is_adult_content": false, // camelCase -> snake_case
          "status": "published",
          "title": "Загадочный Особняк",
          "short_description": "Исследуйте тайны старого поместья...", // camelCase -> snake_case
          "genre": "детектив",
          "language": "ru",
          "player_name": "Сыщик", // camelCase -> snake_case
          "core_stats": { // camelCase -> snake_case
            "sanity": { "description": "Рассудок", "initial_value": 10, "game_over_min": true, "game_over_max": false, "icon": "brain" },
            "clues": { "description": "Улики", "initial_value": 0, "game_over_min": false, "game_over_max": false, "icon": "magnifying-glass" }
          },
          "characters": [
            { "name": "Дворецкий", "description": "Верный слуга... или нет?", "personality": "Загадочный", "image_reference": "ch_butler_ref_123" }
          ],
          "cover_image_url": "https://crion.space/generated-images/history_preview_...jpg | null", // Имя поля и snake_case
          "game_states": [ // camelCase -> snake_case
            {
              "id": "game-state-uuid-1",
              "last_activity_at": "2024-04-20T10:30:00Z",
              "scene_index": 5,
              "current_scene_summary": "Вы стоите перед туманными воротами..."
            },
            {
              "id": "game-state-uuid-2",
              "last_activity_at": "2024-04-19T15:00:00Z",
              "scene_index": 2,
              "current_scene_summary": "Внутри таверны пахнет элем и..."
            }
          ]
        }
        ```
        *   **Примечание:** Поля, относящиеся к *одному* прогрессу (`hasPlayerProgress`, `lastPlayedAt`, `currentPlayerStats`), больше не возвращаются. Используйте `gameStates`.
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID.
        *   `401 Unauthorized`: Невалидный токен.
        *   `403 Forbidden`: Нет доступа к приватной истории.
        *   `404 Not Found`: История не найдена.
        *   `500 Internal Server Error`: Внутренняя ошибка (например, ошибка парсинга JSON или получения данных).

*   **`GET /api/published-stories/:story_id/gamestates`**
    *   Описание: Получение списка состояний игры (сохранений) для **текущего пользователя** в указанной опубликованной истории.
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:story_id` - UUID опубликованной истории (`PublishedStory`).
    *   Ответ при успехе (`200 OK`): Массив объектов `GameStateSummaryDTO`.
        ```json
        [
          {
            "id": "game-state-uuid-1",
            "last_activity_at": "2024-04-20T10:30:00Z", // camelCase -> snake_case
            "scene_index": 5, // camelCase -> snake_case
            "current_scene_summary": "Краткое описание сцены 10..." // camelCase -> snake_case
          },
          {
            "id": "game-state-uuid-2",
            "last_activity_at": "2024-04-19T15:00:00Z", // camelCase -> snake_case
            "scene_index": 2, // camelCase -> snake_case
            "current_scene_summary": "Внутри таверны пахнет элем и..." // camelCase -> snake_case
          }
        ]
        ```
        *   Примечание: Возвращается пустой массив `[]`, если сохранений нет.
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID `story_id`.
        *   `401 Unauthorized`: Невалидный токен.
        *   `404 Not Found`: Опубликованная история не найдена.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

*   **`GET /api/published-stories/:story_id/gamestates/:game_state_id/scene`**
    *   Описание: Получение текущей сцены для **конкретного состояния игры (сохранения)**. Если идет генерация следующей сцены для этого состояния, возвращается ошибка `409 Conflict`.
    *   Аутентификация: **Требуется.**
    *   Параметры пути:
        *   `:story_id` - UUID опубликованной истории (`PublishedStory`).
        *   `:game_state_id` - UUID состояния игры (`PlayerGameState`).
    *   Ответ при успехе (`200 OK`): Объект сцены (`GameSceneResponseDTO`).
        ```json
        {
          "id": "uuid-string", // ID текущей сцены
          "published_story_id": "uuid-string", // ID опубликованной истории (snake_case)
          "game_state_id": "uuid-string", // ID состояния игры (snake_case)
          "current_stats": { // Текущие статы игрока в этом сохранении (snake_case)
            "stat_key_1": 50,
            "stat_key_2": 35
          },
          // --- Поля для type="choices" или "continuation" ---
          "choices": [
            {
              "shuffleable": false, // Можно ли перемешивать (sh: 1 = true, 0 = false)
              "character_name": "Advisor Zaltar", // Имя персонажа (snake_case)
              "description": "Описание блока/ситуации выбора", // Текст из 'desc'
              "options": [
                {
                  "text": "Текст опции 1", // Текст опции (Используется ключ 'text')
                  "consequences": { // ПОСЛЕДСТВИЯ ОПЦИИ (может быть null)
                    "response_text": "Текст-реакция на выбор (если есть)", // (snake_case)
                    "stat_changes": { "Wealth": -15, "Army": 5 } // (snake_case), будет null если нет
                  }
                },
                {
                  "text": "Текст опции 2", // Текст опции (Используется ключ 'text')
                  "consequences": null // БУДЕТ null, если нет ни 'response_text', ни 'stat_changes'
                }
              ]
            }
            // ... другие блоки выбора
          ],
          // --- Поле для type="game_over" ---
          "ending_text": "Текст концовки игры...", // (snake_case, будет null для других типов)
          // --- Поле для type="continuation" ---
          "continuation": { // Будет null для других типов
            "new_player_description": "Описание нового персонажа...", // (snake_case)
            "ending_text_previous": "Текст концовки для предыдущего персонажа...", // (snake_case)
            "core_stats_reset": { "stat_key_1": 10, ... } // Новые базовые статы (snake_case)
          }
        }
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID `game_state_id`.
        *   `401 Unauthorized`: Невалидный токен.
        *   `404 Not Found`: Состояние игры (`PlayerGameState`) не найдено или не принадлежит пользователю.
        *   `409 Conflict` (`{"code": ..., "message": "Scene generation in progress" | "Game over generation in progress" | "Game already completed"}`): Невозможно получить сцену из-за текущего статуса состояния игры.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

*   **`POST /api/published-stories/:story_id/gamestates/:game_state_id/choice`**
    *   Описание: Отправка выбора игрока для текущей сцены в **конкретном состоянии игры (сохранении)**. Запускает процесс обновления состояния и генерации следующей сцены/концовки (асинхронно).
    *   Аутентификация: **Требуется.**
    *   Параметры пути:
        *   `:story_id` - UUID опубликованной истории (`PublishedStory`).
        *   `:game_state_id` - UUID состояния игры (`PlayerGameState`).
    *   Тело запроса (`application/json`):
        ```json
        {
          "selected_option_indices": [ 0, 1 ]
        }
        ```
    *   Ответ при успехе (`200 OK`): **Пустое тело.**
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID `game_state_id`, невалидное тело запроса.
        *   `401 Unauthorized`: Невалидный токен.
        *   `404 Not Found`: Состояние игры (`PlayerGameState`) не найдено или не принадлежит пользователю.
        *   `409 Conflict` (`{"code": ..., "message": "Player not in 'Playing' status" | "Scene generation in progress"}`): Невозможно сделать выбор из-за текущего статуса состояния игры.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

*   **`DELETE /api/published-stories/:story_id/gamestates/:game_state_id`**
    *   Описание: Удаление конкретного состояния игры (слота сохранения) для **своей** игровой сессии в опубликованной истории.
    *   Аутентификация: **Требуется.**
    *   Параметры пути:
        *   `:story_id` - UUID опубликованной истории (`PublishedStory`).
        *   `:game_state_id` - UUID удаляемого состояния игры (`PlayerGameState`).
    *   Ответ при успехе (`204 No Content`): Состояние игры успешно удалено.
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID (для `story_id` или `game_state_id`).
        *   `401 Unauthorized`: Невалидный токен.
        *   `404 Not Found`: Опубликованная история или указанное состояние игры не найдены (или состояние игры не принадлежит пользователю).
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

*   **`POST /api/published-stories/:story_id/like`**
    *   Описание: Поставить лайк опубликованной истории.
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:story_id` - UUID опубликованной истории (`PublishedStory`).
    *   Тело запроса: Нет.
    *   Ответ при успехе (`204 No Content`): Лайк успешно поставлен.
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID.
        *   `401 Unauthorized`: Невалидный токен.
        *   `404 Not Found`: История не найдена.
        *   `409 Conflict` (`{"message": "story already liked by this user"}`): Пользователь уже лайкнул эту историю.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

*   **`DELETE /api/published-stories/:story_id/like`**
    *   Описание: Убрать лайк с опубликованной истории.
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:story_id` - UUID опубликованной истории (`PublishedStory`).
    *   Тело запроса: Нет.
    *   Ответ при успехе (`204 No Content`): Лайк успешно убран.
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID.
        *   `401 Unauthorized`: Невалидный токен.
        *   `404 Not Found` (`{"message": "story not liked by this user yet"}`): Пользователь не лайкал эту историю.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

*   **`DELETE /api/published-stories/:story_id`**
    *   Описание: Удаление **своей** опубликованной истории.
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:story_id` - UUID опубликованной истории (`PublishedStory`).
    *   Тело запроса: Нет.
    *   Ответ при успехе (`204 No Content`): История успешно удалена.
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID.
        *   `401 Unauthorized`: Невалидный токен.
        *   `403 Forbidden`: Попытка доступа к чужой истории.
        *   `404 Not Found`: История не найдена.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера при удалении.

*   **`POST /api/published-stories/:story_id/retry`**
    *   Описание: Повторный запуск **начальных** шагов генерации для опубликованной истории, которая имеет статус ошибки или у которой отсутствуют необходимые компоненты (Setup, текст первой сцены, изображение обложки, изображения персонажей). Эндпоинт последовательно проверяет и запускает перегенерацию необходимого шага: сначала Setup, затем текст первой сцены, затем изображение обложки, и, наконец, проверяет и запускает (асинхронно) генерацию недостающих изображений персонажей, если флаг `are_images_pending` установлен. Этот эндпоинт **не** ретраит генерацию сцен *после* первой.
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:story_id` - UUID опубликованной истории (`PublishedStory`).
    *   Тело запроса: Нет.
    *   Ответ при успехе (`202 Accepted`): **Пустое тело.** Статус истории или связанного с ней начального состояния игры изменится на соответствующий (`setup_pending` или `generating_scene`).
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID.
        *   `401 Unauthorized`: Невалидный токен.
        *   `404 Not Found`: История не найдена.
        *   `409 Conflict` (`{"message": "Story/initial state is not in error state"}`): История или начальное состояние игры не в статусе ошибки.
        *   `500 Internal Server Error`: Ошибка при обновлении статуса или постановке задачи.

*   **`POST /api/published-stories/:story_id/gamestates/:game_state_id/retry`**
    *   Описание: Повторный запуск задачи генерации **конкретной** сцены для указанного состояния игры (`PlayerGameState`), которое завершилось с ошибкой (`playerStatus: "error"`).
    *   Аутентификация: **Требуется.**
    *   Параметры пути:
        *   `:story_id` - UUID опубликованной истории.
        *   `:game_state_id` - UUID состояния игры (`PlayerGameState`), для которого нужно повторить генерацию.
    *   Тело запроса: Нет.
    *   Ответ при успехе (`202 Accepted`): **Пустое тело.** Статус состояния игры изменится на `generating_scene`.
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID `story_id` или `game_state_id`.
        *   `401 Unauthorized`: Невалидный токен.
        *   `403 Forbidden`: Попытка ретрая чужого состояния игры.
        *   `404 Not Found`: История или состояние игры не найдены.
        *   `409 Conflict` (`{"message": "Game state is not in error state"}`): Состояние игры не в статусе ошибки.
        *   `500 Internal Server Error`: Ошибка при обновлении статуса или постановке задачи.

*   **`PATCH /api/published-stories/:story_id/visibility`**
    *   Описание: Изменение видимости **своей** опубликованной истории (сделать публичной или приватной).
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:story_id` - UUID опубликованной истории (`PublishedStory`).
    *   Тело запроса (`application/json`):
        ```json
        {
          "is_public": true // или false
        }
        ```
    *   Ответ при успехе (`204 No Content`): Видимость успешно изменена.
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID или тело запроса (например, отсутствует `is_public`).
        *   `401 Unauthorized`: Невалидный токен.
        *   `403 Forbidden`: Попытка изменить видимость чужой истории.
        *   `404 Not Found`: История не найдена.
        *   `409 Conflict` (`{"message": "Story is not ready for publishing" | "Adult content cannot be made public"}`): История не готова к публикации или контент 18+ не может быть сделан публичным.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

*   **`POST /api/published-stories/:story_id/gamestates`**
    *   Описание: Создание нового (и единственного) состояния игры (сохранения) для **текущего пользователя** в указанной опубликованной истории. Вызывается, когда игрок нажимает "Начать игру".
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:story_id` - UUID опубликованной истории (`PublishedStory`).
    *   Тело запроса: Нет.
    *   Ответ при успехе (`201 Created`): Объект `PlayerGameState` созданного сохранения.
        ```json
        {
          "id": "uuid-string", // ID созданного состояния игры (gameStateID)
          "player_id": "uuid-string", // camelCase -> snake_case
          "published_story_id": "uuid-string", // camelCase -> snake_case
          "player_progress_id": "uuid-string", // ID начального узла прогресса (camelCase -> snake_case)
          "current_scene_id": "uuid-string | null", // ID начальной сцены (camelCase -> snake_case)
          "player_status": "generating_scene | playing", // Статус (camelCase -> snake_case)
          "started_at": "timestamp-string", // camelCase -> snake_case
          "last_activity_at": "timestamp-string", // camelCase -> snake_case
          "error_details": null, // (camelCase -> snake_case)
          "completed_at": null // (camelCase -> snake_case)
        }
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID `story_id`.
        *   `401 Unauthorized`: Невалидный токен.
        *   `404 Not Found`: Опубликованная история не найдена.
        *   `409 Conflict` (`{"code": "SAVE_SLOT_EXISTS", ...}` | `{"code": "STORY_NOT_READY", ...}`): Слот сохранения уже существует, или история не готова к игре.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

---

#### Сервис WebSocket (`/ws`)

Предоставляет WebSocket соединение для получения уведомлений в реальном времени.

*   **URL:** `ws://<ваш_хост>:<порт_traefik_web>/ws?token=<ваш_access_token>`
*   **Аутентификация:** Через query-параметр `token`.
*   **Сообщения от сервера:**
    *   **Успешная генерация черновика (`draft_generated`):**
        ```json
        {
          "event": "draft_generated",
          "payload": {
            "id": "uuid-string", // ID черновика
            "status": "draft"
          }
        }
        ```
    *   **Ошибка генерации черновика (`draft_error`):**
        ```json
        {
          "event": "draft_error",
          "payload": {
            "id": "uuid-string", // ID черновика
            "status": "error",
            "error": "Сообщение об ошибке"
          }
        }
        ```
    *   **Успешная генерация сцены (`scene_generated`):**
        ```json
        {
          "event": "scene_generated",
          "payload": {
            "published_story_id": "uuid-string",
            "game_state_id": "uuid-string", // <<< ДОБАВЛЕНО (вместо state_hash)
            "scene_id": "uuid-string"
            // "state_hash": "string" // <<< УДАЛЕНО
          }
        }
        ```
    *   **Ошибка генерации сцены (`scene_error`):**
        ```json
        {
          "event": "scene_error",
          "payload": {
            "published_story_id": "uuid-string",
            "game_state_id": "uuid-string", // <<< ДОБАВЛЕНО (вместо state_hash)
            // "state_hash": "string", // <<< УДАЛЕНО
            "error": "Сообщение об ошибке"
          }
        }
        ```
    *   **Успешная генерация концовки (`game_over_generated`):**
        ```json
        {
          "event": "game_over_generated",
          "payload": {
            "published_story_id": "uuid-string",
            "scene_id": "uuid-string" // ID сгенерированной сцены с концовкой
          }
        }
        ```
    *   **Ошибка генерации концовки (`game_over_error`):**
        ```json
        {
          "event": "game_over_error",
          "payload": {
            "published_story_id": "uuid-string",
            "error": "Сообщение об ошибке"
          }
        }
        ```
    *   **Успешная генерация Setup (`setup_generated`):**
        ```json
        {
          "event": "setup_generated",
          "payload": {
            "published_story_id": "uuid-string"
          }
        }
        ```
    *   **Ошибка генерации Setup (`setup_error`):**
        ```json
        {
          "event": "setup_error",
          "payload": {
            "published_story_id": "uuid-string",
            "error": "Сообщение об ошибке"
          }
        }
        ```
*   **Сообщения от клиента:** Не предусмотрены (только установка соединения).

---

#### Сервис Push-уведомлений (`notification-service`)

Этот сервис отвечает за доставку push-уведомлений пользователям через Firebase Cloud Messaging (FCM) и Apple Push Notification service (APNS).

**Локализация на клиенте (Data-Only Notifications):**

Чтобы уведомления отображались на языке пользователя, **клиентское приложение (iOS/Android) отвечает за их локализацию**.

Бэкенд (`notification-service`) теперь отправляет **только data-only** push-уведомления. Это значит, что стандартные поля `notification.title` и `notification.body` **не отправляются**. Вместо этого весь необходимый контент передается в специальном `data` payload, который включает:

*   `loc_key`: Ключ строки локализации (например, `notification_scene_ready`). Используйте константу `constants.PushLocKey` для самого ключа (`"loc_key"`).
*   `loc_arg_*`: Аргументы, которые нужно подставить в строку локализации (например, `loc_arg_storyTitle`). Используйте константы `constants.PushLocArg*`.
*   `fallback_title`: Запасной заголовок на случай, если локализация не удалась.
*   `fallback_body`: Запасное тело сообщения.
*   Другие необходимые данные (`storyConfigId`, `publishedStoryId` и т.д.).

**Задача клиента:**

1.  **Получить data payload:** Обработать получение data-only уведомления (даже в background/terminated). Это особенно важно для iOS.
    *   **Android:** Использовать `FirebaseMessagingService`.
    *   **iOS:** **Обязательно** использовать **Notification Service Extension** для перехвата и модификации уведомления *до* его отображения, или Background Push с показом *локального* уведомления.
2.  **Извлечь данные:** Получить `loc_key`, все `loc_arg_*`, `fallback_title`, `fallback_body` из **`data` payload** уведомления.
3.  **Выполнить локализацию:** Попробовать найти строку перевода по `loc_key` и подставить аргументы `loc_arg_*`.
4.  **Определить текст:** Если локализация удалась, использовать переведенные строки. Если нет (или ключа `loc_key` не было), использовать `fallback_title` и `fallback_body`.
5.  **Отобразить уведомление:** Создать и отобразить *локальное* уведомление (или модифицировать входящее через Extension на iOS) с полученным заголовком и телом.

**Типы уведомлений и их данные:**

Ниже перечислены основные события, по которым отправляются push-уведомления, и данные, которые они содержат в `data` payload.

*   **Черновик готов:**
    *   `loc_key`: `notification_draft_ready` (константа `constants.PushLocKeyDraftReady`)
    *   `data`:
        ```json
        {
          "story_config_id": "uuid-string", // camelCase -> snake_case
          "event_type": "draft", // camelCase -> snake_case
          "loc_key": "notification_draft_ready",
          "loc_arg_story_title": "Название Черновика", // Изменено с storyTitle
          "fallback_title": "Черновик готов!",
          "fallback_body": "Ваш черновик \"Название Черновика\" готов к настройке."
        }
        ```
*   **История готова к игре:** (После генерации Setup, первой сцены и всех изображений)
    *   `loc_key`: `notification_story_ready` (константа `constants.PushLocKeyStoryReady`)
    *   `data`:
        ```json
        {
          "published_story_id": "uuid-string", // camelCase -> snake_case
          "event_type": "ready", // camelCase -> snake_case
          "loc_key": "notification_story_ready",
          "loc_arg_story_title": "Название Истории", // Изменено с storyTitle
          "fallback_title": "История готова!",
          "fallback_body": "Ваша история \"Название Истории\" готова к игре!",
          "title": "Название Истории",
          "author_name": "Имя Автора" // camelCase -> snake_case
        }
        ```
*   **Новая сцена готова:**
    *   `loc_key`: `notification_scene_ready` (константа `constants.PushLocKeySceneReady`)
    *   `data`:
        ```json
        {
          "published_story_id": "uuid-string", // camelCase -> snake_case
          "game_state_id": "uuid-string", // camelCase -> snake_case
          "scene_id": "uuid-string", // camelCase -> snake_case
          "event_type": "scene_ready", // Уточнено значение
          "loc_key": "notification_scene_ready",
          "loc_arg_story_title": "Название Истории", // Изменено с storyTitle
          "fallback_title": "Новая сцена готова!", // Изменено для ясности
          "fallback_body": "Новая сцена в истории \"Название Истории\" готова!", // Изменено для ясности
          "title": "Название Истории",
          "author_name": "Имя Автора" // camelCase -> snake_case
        }
        ```
*   **Игра завершена (Game Over):**
    *   `loc_key`: `notification_game_over` (константа `constants.PushLocKeyGameOver`)
    *   `data`:
        ```json
        {
          "published_story_id": "uuid-string", // camelCase -> snake_case
          "game_state_id": "uuid-string", // camelCase -> snake_case
          "scene_id": "uuid-string", // camelCase -> snake_case
          "event_type": "completed", // camelCase -> snake_case
          "loc_key": "notification_game_over",
          "loc_arg_story_title": "Название Истории", // Изменено с storyTitle
          "loc_arg_ending_text": "Текст концовки...", // Изменено с endingText
          "fallback_title": "Игра завершена!",
          "fallback_body": "История \"Название Истории\" завершена.",
          "title": "Название Истории",
          "author_name": "Имя Автора" // camelCase -> snake_case
        }
        ```

**Примечание:** Ключи аргументов (`loc_arg_story_title`, `loc_arg_ending_text`), ключи fallback (`fallback_title`, `fallback_body`) и основной ключ `loc_key` теперь должны быть консистентными.

---

## Игровой процесс: Взаимодействие Клиент-Сервер

Этот раздел описывает типичный цикл взаимодействия между клиентским приложением (например, мобильным) и сервером во время прохождения опубликованной истории.

1.  **Начало игры (Создание Сохранения):**
    *   Пользователь выбирает историю и нажимает "Начать игру" (или "Продолжить", если есть сохранения).
    *   Если сохранений нет или пользователь хочет начать заново, клиент отправляет запрос:
        *   `POST /api/published-stories/:story_id/gamestates`
    *   Сервер создает новое состояние игры (`PlayerGameState`) и возвращает его (`201 Created`):
        ```json
        {
          "id": "new-game-state-uuid", // ID созданного состояния
          // ... другие поля PlayerGameState ...
          "player_status": "generating_scene | playing" // Статус
        }
        ```
    *   Клиент **сохраняет** полученный `id` (это `game_state_id` для дальнейших запросов).
    *   **Важно:** Если `player_status` равен `generating_scene`, клиент должен подождать WebSocket-события `scene_generated`, прежде чем запрашивать сцену. Если статус `playing`, можно сразу переходить к шагу 2.

2.  **Запрос текущей сцены:**
    *   Клиент использует сохраненный `game_state_id` и отправляет запрос:
        *   `GET /api/published-stories/:story_id/gamestates/:game_state_id/scene`
    *   Сервер возвращает текущую сцену (`200 OK`, см. описание DTO `GameSceneResponseDTO` выше, теперь полностью в `snake_case`) или ошибку:
        *   `409 Conflict`: Если сцена или концовка еще генерируется (`generating_scene` или `game_over_pending`). Клиент должен подождать WebSocket-события.
        *   `404 Not Found`: Состояние игры не найдено (маловероятно, если ID был только что получен).
        *   `5xx`: Внутренняя ошибка.

3.  **Отображение сцены и выбор игрока:**
    *   Клиент парсит полученный `GameSceneResponseDTO`.
    *   Если есть поле `choices`, отображает текст (`description`) и варианты (`options[].text`).
    *   Если есть поле `ending_text`, отображает текст концовки.
    *   Если есть поле `continuation`, обрабатывает переход к новому персонажу.
    *   Пользователь делает выбор (нажимает на один из вариантов в блоке `choices`). Клиент запоминает индекс(ы) выбранного варианта (например, `[0]` или `[1]`, если выбор был во втором блоке `choices`).

4.  **Отправка выбора:**
    *   Клиент отправляет сделанный выбор на сервер:
        *   `POST /api/published-stories/:story_id/gamestates/:game_state_id/choice`
        *   Тело запроса: `{"selected_option_indices": [ index1, index2, ...]}` (поле уже в `snake_case`)
    *   Сервер принимает выбор (`200 OK`), обновляет внутреннее состояние прогресса и **асинхронно** запускает задачу на генерацию следующей сцены или концовки.

5.  **Ожидание следующей сцены/концовки (WebSocket):**
    *   Клиент **обязательно** должен слушать WebSocket-события после отправки выбора.
    *   **Успешная генерация сцены:**
        *   Событие: `scene_generated`
        *   Payload: `{"published_story_id": "...", "game_state_id": "...", "scene_id": "..."}` (поля в `snake_case`)
        *   Действие клиента: Генерация завершена. **Перейти к шагу 2** (Запрос текущей сцены с тем же `game_state_id`).
    *   **Успешная генерация концовки:**
        *   Событие: `game_over_generated`
        *   Payload: `{"published_story_id": "...", "scene_id": "..."}` (поля в `snake_case`)
        *   Действие клиента: Генерация концовки завершена. **Перейти к шагу 2** (Запрос текущей сцены с тем же `game_state_id`, которая теперь будет содержать `ending_text`).
    *   **Ошибка генерации сцены/концовки:**
        *   Событие: `scene_error` или `game_over_error`
        *   Payload: `{"published_story_id": "...", "game_state_id": "...", "error": "..."}` (для `scene_error`) или `{"published_story_id": "...", "error": "..."}` (для `game_over_error`) (поля в `snake_case`)
        *   Действие клиента: Показать пользователю сообщение об ошибке. Можно предложить кнопку "Попробовать снова", которая вызовет:
            *   `POST /api/published-stories/:story_id/gamestates/:game_state_id/retry`
            После успешного ответа `202 Accepted` на ретрай, клиент снова **переходит к шагу 5** (Ожидание WebSocket).

6.  **Цикл:** Шаги 2-5 повторяются, пока не будет получена сцена с `ending_text`.