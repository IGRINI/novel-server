# Novel Server

Интерактивный генератор текстовых новелл на основе AI.

## Архитектура

Проект использует микросервисную архитектуру, управляемую через Docker Compose. Включает следующие сервисы:

*   **api-gateway (Traefik):** Единая точка входа для всех HTTP и WebSocket запросов. Маршрутирует запросы к соответствующим внутренним сервисам. Осуществляет терминирование SSL.
*   **auth:** Сервис аутентификации и авторизации пользователей (регистрация, вход, управление токенами, получение информации о пользователе).
*   **gameplay-service:** Основной сервис, управляющий логикой создания черновиков, публикации историй, прохождения игры (сцены, выборы), лайками и списком историй. Взаимодействует с другими сервисами.
*   **story-generator:** Воркер, отвечающий за генерацию контента новеллы (начальный конфиг, setup, сцены, концовки) с помощью AI по запросам из очереди RabbitMQ.
*   **image-generator:** Воркер, отвечающий за генерацию изображений (обложки историй, портреты персонажей) по запросам из очереди RabbitMQ.
*   **websocket-service:** Управляет WebSocket соединениями для отправки уведомлений пользователям в реальном времени о статусе генерации и других событиях. Получает события из RabbitMQ.
*   **notification-service:** Отвечает за отправку Push-уведомлений пользователям через FCM/APNS. Получает события из RabbitMQ.
*   **admin-service:** Веб-интерфейс и API для администрирования (управление пользователями, историями, просмотр логов и т.д.). (Может быть опциональным в зависимости от развертывания).
*   **postgres:** База данных PostgreSQL для хранения данных пользователей, черновиков, опубликованных историй, прогресса игры, лайков, ссылок на изображения и т.д.
*   **redis:** Кэш Redis (используется сервисом `auth` для хранения сессий/отозванных токенов и, возможно, другими сервисами для кэширования).
*   **rabbitmq:** Брокер сообщений RabbitMQ для асинхронного взаимодействия между сервисами (постановка задач генерации контента/изображений, отправка событий для WebSocket и Push).
*   **migrate:** Сервис для применения миграций базы данных при старте.
*   **prometheus:** Сбор метрик сервисов.
*   **grafana:** Визуализация метрик (дашборды).

## Запуск проекта

1.  **Установите Docker и Docker Compose.**
2.  **Создайте файл `.env`** в корне проекта на основе примера `.env.example` (если он есть) или скопируйте существующий `.env`.
    *   **Важно:** Убедитесь, что установлены `JWT_SECRET`, `PASSWORD_SALT`, `DB_PASSWORD`, `AI_API_KEY` и другие необходимые секреты. Обратите внимание на конфигурацию AI провайдера (`AI_CLIENT_TYPE`, `AI_BASE_URL`, `AI_MODEL`).
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

Все запросы к бэкенду должны отправляться через API Gateway (Traefik). Убедитесь, что порт Traefik (по умолчанию 8080 для HTTP и 443 для HTTPS, если настроен) доступен.

*   **Базовый URL API (HTTPS):** `https://crion.space` (пример, используйте ваш домен/IP)
*   **WebSocket URL:** `wss://crion.space/ws` (пример, используйте ваш домен/IP)
*   **Traefik Dashboard:** `http://<ваш_хост>:<порт_traefik_dashboard>` (по умолчанию порт 8888, доступен локально или через VPN)

### Генерация и доступ к изображениям

Сгенерированные изображения (превью историй и портреты персонажей) сохраняются локально в volume, который пробрасывается в контейнер `api-gateway`. Доступ к ним осуществляется через API Gateway.

*   **Базовый URL изображений:** `https://crion.space/generated-images` (пример, используйте ваш домен/IP)

Конкретные URL формируются и используются следующим образом:

*   **Превью опубликованной истории:**
    *   Формат URL: `[Базовый URL изображений]/history_preview_{publishedStoryID}.jpg`
    *   Пример: `https://crion.space/generated-images/history_preview_a1b2c3d4-e5f6-7890-1234-567890abcdef.jpg`
    *   Где `{publishedStoryID}` - это UUID опубликованной истории.
    *   URL возвращается в API ответах, где это применимо (например, в списках историй).

*   **Изображение персонажа:**
    *   **Получение URL:** Бэкенд **не** возвращает готовые URL изображений персонажей в списках или деталях истории. Клиент должен:
        1.  Получить поле `imageReference` из данных **Setup** истории (например, через `GET /api/v1/published-stories/:story_id` в поле `characters[].imageReference`). Это поле содержит уникальный идентификатор изображения персонажа (например, `ch_male_adult_wizard_...`).
        2.  Самостоятельно сконструировать полный URL, используя базовый URL изображений и полученный `imageReference`.
    *   Формат URL: `[Базовый URL изображений]/{imageReference}.jpg`
    *   Пример: `https://crion.space/generated-images/ch_male_adult_wizard_abc123.jpg`

### Аутентификация

Для доступа к большинству эндпоинтов (кроме регистрации, входа и обновления токена) необходимо передавать JWT access токен, полученный при входе или обновлении.
*   **HTTP запросы:** В заголовке `Authorization: Bearer <ваш_access_token>`.
*   **WebSocket соединение:** Через query-параметр `?token=<ваш_access_token>` при установке соединения.

### Основные эндпоинты

Ниже описаны основные эндпоинты, доступные для взаимодействия с пользователем и между сервисами. Маршрутизация осуществляется через API Gateway (Traefik). Все ключи в JSON запросах и ответах используют **snake_case**.

---

#### Сервис Аутентификации (`/auth` и `/api/v1`)

Предоставляет эндпоинты для управления пользователями и токенами.

##### Публичные эндпоинты (`/auth`)

*   **`POST /auth/register`**
    *   Описание: Регистрация нового пользователя.
    *   Аутентификация: **Не требуется.**
    *   **Rate Limit:** 10 запросов в минуту с одного IP-адреса.
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
        *   `400 Bad Request` (`{"code": 40001, "message": "..."}`): Невалидные данные (формат, длина).
        *   `409 Conflict` (`{"code": 40901, "message": "Username is already taken | Email is already taken"}`): Пользователь/email уже существует.
        *   `429 Too Many Requests`: Превышен лимит запросов.
        *   `500 Internal Server Error` (`{"code": 50001, "message": "..."}`): Внутренняя ошибка.

*   **`POST /auth/login`**
    *   Описание: Вход пользователя.
    *   Аутентификация: **Не требуется.**
    *   **Rate Limit:** 10 запросов в минуту с одного IP-адреса.
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
        *   `429 Too Many Requests`: Превышен лимит запросов.
        *   `500 Internal Server Error` (`{"code": 50001, "message": "..."}`): Внутренняя ошибка.

*   **`POST /auth/refresh`**
    *   Описание: Обновление пары access/refresh токенов.
    *   Аутентификация: **Не требуется.**
    *   **Rate Limit:** (Общий лимит API Gameplay Service)
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
        *   `429 Too Many Requests`: (Если применен общий лимит)
        *   `500 Internal Server Error` (`{"code": 50001, "message": "..."}`): Внутренняя ошибка.

*   **`POST /auth/logout`**
    *   Описание: Выход пользователя (отзыв refresh токена и связанных с ним access токенов).
    *   Аутентификация: **Не требуется** (токен передается в теле).
    *   **Rate Limit:** (Общий лимит API Gameplay Service)
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
        *   `429 Too Many Requests`: Превышен лимит запросов.
        *   `500 Internal Server Error` (`{"code": 50001, "message": "..."}`): Внутренняя ошибка сервера.

*   **`POST /auth/token/verify`**
    *   Описание: Проверка валидности access токена (без проверки отзыва). Используется, например, другими сервисами для быстрой валидации без обращения к хранилищу отозванных токенов.
    *   Аутентификация: **Не требуется.**
    *   **Rate Limit:** (Общий лимит API Gameplay Service)
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
        *   `429 Too Many Requests`: Превышен лимит запросов.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

##### Защищенные эндпоинты (`/api/v1`)

*   **`GET /api/v1/me`**
    *   Описание: Получение информации о текущем аутентифицированном пользователе.
    *   Аутентификация: **Требуется** (`Authorization: Bearer <access_token>`).
    *   **Rate Limit:** 100 запросов в минуту с одного IP-адреса (общий лимит Gameplay Service).
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
        *   `401 Unauthorized` (`{"code": 40102 | 40103 | 40104, "message": "..."}`): Access токен невалиден, просрочен или отозван.
        *   `404 Not Found` (`{"code": 40402, "message": "User not found"}`): Пользователь, связанный с токеном, не найден в БД.
        *   `429 Too Many Requests`: Превышен лимит запросов.
        *   `500 Internal Server Error` (`{"code": 50001, "message": "..."}`): Внутренняя ошибка сервера.

*   **`POST /api/v1/device-tokens`**
    *   Описание: Регистрирует токен устройства для текущего аутентифицированного пользователя, чтобы получать push-уведомления. Если токен для этого пользователя уже существует, обновляет платформу и время последнего использования.
    *   Аутентификация: **Требуется** (`Authorization: Bearer <access_token>`).
    *   **Rate Limit:** 100 запросов в минуту с одного IP-адреса (общий лимит Gameplay Service).
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
        *   `429 Too Many Requests`: Превышен лимит запросов.
        *   `500 Internal Server Error`: Ошибка базы данных при сохранении токена.

*   **`DELETE /api/v1/device-tokens`**
    *   Описание: Удаляет указанный токен устройства из системы. Пользователь больше не будет получать push-уведомления на это устройство.
    *   Аутентификация: **Требуется** (`Authorization: Bearer <access_token>`).
    *   **Rate Limit:** 100 запросов в минуту с одного IP-адреса (общий лимит Gameplay Service).
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
        *   `429 Too Many Requests`: Превышен лимит запросов.
        *   `500 Internal Server Error`: Ошибка базы данных при удалении токена. (Примечание: если токен не найден, ошибка не возвращается, операция считается успешной).

---

#### Сервис Геймплея (`/api/v1`)

Управляет процессом создания, редактирования, публикации и прохождения историй.

**Важное замечание:** Внутри `gameplay-service` и в его API идентификация пользователей (`user_id`) и сущностей (черновики, опубликованные истории) происходит с использованием **`uuid.UUID`**. Это отличается от старого числового `user_id`, который мог использоваться ранее. Эндпоинт `/api/v1/me` из сервиса `auth` также возвращает `user_id` как UUID.

**Общие лимиты:**
*   **IP-лимит:** 100 запросов в минуту с одного IP-адреса на все эндпоинты `/api/v1`.
*   **User-лимит (генерация):** 20 запросов в минуту **для одного пользователя** на эндпоинты, запускающие генерацию (отмечены отдельно).

##### Черновики историй (`/api/v1/stories`)

*   **`POST /api/v1/stories/generate`**
    *   Описание: Запуск генерации **нового** черновика истории на основе промпта. Возвращает ID черновика. Генерация происходит асинхронно. Клиент должен слушать WebSocket для получения результата.
    *   Аутентификация: **Требуется.**
    *   **Rate Limit:** User-лимит (20/мин).
    *   Тело запроса (`application/json`):
        ```json
        {
          "prompt": "Текст начального запроса пользователя...", // (обязательно, string)
          "language": "string" // Код языка (например, "en", "ru"). (обязательно, string, из списка поддерживаемых)
        }
        ```
    *   Ответ при успехе (`202 Accepted`): Возвращает созданный объект `StoryConfig` со статусом `generating`.
        ```json
        {
          "id": "uuid-string", // (обязательно, string, UUID) ID созданного черновика
          "user_id": "uuid-string", // UUID пользователя
          "title": "", // Изначально пустые
          "description": "",
          "user_input": ["..."], // JSON массив с начальным prompt
          "config": null, // null до завершения генерации
          "status": "generating", // (обязательно, string)
          "language": "string",
          "created_at": "timestamp",
          "updated_at": "timestamp"
        }
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидное тело запроса (например, отсутствует `prompt` или `language`, неподдерживаемый язык).
        *   `401 Unauthorized`: Невалидный токен.
        *   `409 Conflict` (`{"message": "User already has an active generation task"}`): У пользователя уже есть активная задача генерации.
        *   `500 Internal Server Error`: Ошибка при создании записи в БД или постановке задачи в очередь. **Примечание:** Если ошибка произошла *после* создания записи, но *до* отправки задачи, тело ответа может содержать созданный `StoryConfig` со статусом `error`.

*   **`GET /api/v1/stories`**
    *   Описание: Получение списка **своих** черновиков (`StoryConfig`). Поддерживает курсорную пагинацию.
    *   Аутентификация: **Требуется.**
    *   Query параметры:
        *   `limit` (опционально, int, default=10, max=100): Количество записей.
        *   `cursor` (опционально, string): Курсор для следующей страницы.
    *   Ответ при успехе (`200 OK`): Пагинированный список `StoryConfigSummary`.
        ```json
        {
          "data": [ // (обязательно, array) Массив может быть пустым []
            {
              "id": "uuid-string", // (обязательно, string, UUID)
              "title": "string", // (обязательно, string, может быть пустым "")
              "description": "string", // (обязательно, string, может быть user_input)
              "created_at": "timestamp-string", // (обязательно, string, timestamp)
              "status": "generating | draft | error" // (обязательно, string)
            }
            /* ... */
          ],
          "next_cursor": "string | null" // (обязательно, string или null)
        }
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный курсор или `limit`.
        *   `401 Unauthorized`: Невалидный токен.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

*   **`GET /api/v1/stories/:id`**
    *   Описание: Получение детальной информации о **своем** черновике по его UUID. Возвращает распарсенный конфиг, если он готов (`status: "draft"`).
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:id` (обязательно, string, UUID) - UUID черновика (`StoryConfig`).
    *   Ответ при успехе (`200 OK`): Объект `StoryConfigParsedDetail`.
        ```json
        {
          "title": "string", // (обязательно, string)
          "short_description": "string", // (обязательно, string)
          "franchise": "string | null", // (опционально, string или null, зависит от генерации)
          "genre": "string", // (обязательно, string)
          "language": "string", // (обязательно, string)
          "is_adult_content": false, // (обязательно, boolean)
          "player_name": "string", // (обязательно, string) Protagonist Name
          "player_description": "string", // (обязательно, string) Protagonist Description
          "world_context": "string", // (обязательно, string)
          "story_summary": "string", // (обязательно, string)
          "core_stats": { // (обязательно, object) Словарь статов: имя -> описание
            "stat_key_1": { // (обязательно, object)
              "description": "string" // (обязательно, string) Описание из вывода Narrator
              // InitialValue и GameOverConditions здесь отсутствуют, т.к. это черновик
            },
            "stat_key_2": { ... }
          },
          "player_preferences": { // (обязательно, object) Protagonist Preferences
            "th": ["string"], // (опционально, array) Themes
            "st": "string",   // (опционально, string) Style
            "wl": ["string"], // (опционально, array) World Lore
            "dt": "string",   // (опционально, string) Protagonist Details
            "dl": "string",   // (опционально, string) Desired Locations (comma-separated)
            "dc": "string"    // (опционально, string) Desired Characters (comma-separated)
          },
          "status": "draft | generating | error" // (обязательно, string) Текущий статус черновика
        }
        ```
        *   **Примечание:** Если `status` черновика не `draft` (например, `generating` или `error`), поля, зависящие от сгенерированного `config` (такие как `title`, `core_stats`, `player_preferences` и т.д.) могут отсутствовать или быть `null`/пустыми. Клиент должен проверять статус.
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID.
        *   `401 Unauthorized`: Невалидный токен.
        *   `403 Forbidden`: Попытка доступа к чужому черновику.
        *   `404 Not Found`: Черновик не найден.
        *   `500 Internal Server Error`: Ошибка парсинга JSON конфига или другая внутренняя ошибка.

*   **`POST /api/v1/stories/:id/revise`**
    *   Описание: Запуск задачи на **перегенерацию** существующего черновика на основе новой инструкции. Черновик должен быть в статусе `draft` или `error`.
    *   Аутентификация: **Требуется.**
    *   **Rate Limit:** User-лимит (20/мин).
    *   Параметр пути: `:id` (обязательно, string, UUID) - UUID черновика (`StoryConfig`).
    *   Тело запроса (`application/json`):
        ```json
        {
          "revision_prompt": "Текст инструкции для изменения..." // (обязательно, string)
        }
        ```
    *   Ответ при успехе (`202 Accepted`): **Пустое тело.** Статус черновика асинхронно изменится на `generating`.
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID или тело запроса (отсутствует `revision_prompt`).
        *   `401 Unauthorized`: Невалидный токен.
        *   `403 Forbidden`: Попытка доступа к чужому черновику.
        *   `404 Not Found`: Черновик не найден.
        *   `409 Conflict` (`{"message": "Story config is not in a state that allows revision" | "User already has an active generation task"}`): Черновик не готов к ревизии или у пользователя уже есть задача.
        *   `500 Internal Server Error`: Ошибка при обновлении БД или постановке задачи.

*   **`POST /api/v1/stories/:id/publish`**
    *   Описание: Публикация готового черновика (`status: "draft"`). Создает запись `PublishedStory` и запускает асинхронную генерацию `NovelSetup` и первой сцены.
    *   Аутентификация: **Требуется.**
    *   **Rate Limit:** User-лимит (20/мин).
    *   Параметр пути: `:id` (обязательно, string, UUID) - UUID черновика (`StoryConfig`).
    *   Тело запроса: Нет.
    *   Ответ при успехе (`202 Accepted`): Возвращает ID созданной опубликованной истории. Статус опубликованной истории будет `setup_pending` или `initial_generation`.
        ```json
        {
          "published_story_id": "uuid-string" // (обязательно, string, UUID)
        }
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID.
        *   `401 Unauthorized`: Невалидный токен.
        *   `403 Forbidden`: Попытка доступа к чужому черновику.
        *   `404 Not Found`: Черновик не найден.
        *   `409 Conflict` (`{"message": "Story config is not in draft state"}`): Черновик не готов к публикации.
        *   `500 Internal Server Error`: Ошибка при создании `PublishedStory`, сцены или обновлении статуса черновика.

*   **`POST /api/v1/stories/drafts/:draft_id/retry`**
    *   Описание: Повторный запуск задачи генерации для черновика, который завершился с ошибкой (`status: "error"`).
    *   Аутентификация: **Требуется.**
    *   **Rate Limit:** User-лимит (20/мин).
    *   Параметр пути: `:draft_id` (обязательно, string, UUID) - UUID черновика (`StoryConfig`).
    *   Тело запроса: Нет.
    *   Ответ при успехе (`202 Accepted`): **Пустое тело.** Статус черновика асинхронно изменится на `generating`.
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID.
        *   `401 Unauthorized`: Невалидный токен.
        *   `403 Forbidden`: Попытка доступа к чужому черновику.
        *   `404 Not Found`: Черновик не найден.
        *   `409 Conflict` (`{"message": "Story config is not in error state" | "User already has an active generation task"}`): Черновик не в статусе ошибки или у пользователя уже есть задача.
        *   `500 Internal Server Error`: Ошибка при обновлении БД или постановке задачи.

*   **`DELETE /api/v1/stories/:id`**
    *   Описание: Удаление **своего** черновика истории.
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:id` (обязательно, string, UUID) - UUID черновика (`StoryConfig`).
    *   Тело запроса: Нет.
    *   Ответ при успехе (`204 No Content`): Черновик успешно удален.
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID.
        *   `401 Unauthorized`: Невалидный токен.
        *   `403 Forbidden`: Попытка доступа к чужому черновику.
        *   `404 Not Found`: Черновик не найден.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера при удалении.

##### Опубликованные истории (`/api/v1/published-stories`)

*   **`GET /api/v1/published-stories/me`**
    *   Описание: Получение списка **своих** опубликованных историй. Поддерживает курсорную пагинацию.
    *   Аутентификация: **Требуется.**
    *   Query параметры:
        *   `limit` (опционально, int, default=10, max=100).
        *   `cursor` (опционально, string).
        *   `filter_adult` (опционально, boolean, default=false): Если true, исключает истории с контентом 18+.
    *   Ответ при успехе (`200 OK`): Пагинированный список `PublishedStorySummaryWithProgress`.
        ```json
        {
          "data": [ // (обязательно, array)
            {
              "id": "uuid-string", // (обязательно, string, UUID)
              "title": "string", // (обязательно, string)
              "short_description": "string | null", // (опционально, string или null)
              "author_id": "uuid-string", // (обязательно, string, UUID)
              "author_name": "string", // (обязательно, string)
              "published_at": "timestamp-string", // (обязательно, string, timestamp)
              "is_adult_content": false, // (обязательно, boolean)
              "likes_count": 123, // (обязательно, integer >= 0)
              "is_liked": true, // (обязательно, boolean) Лайкнул ли *текущий* пользователь
              "has_player_progress": true, // (обязательно, boolean) Есть ли у *текущего* пользователя сохранения
              "status": "ready | error | setup_pending | ...", // (обязательно, string) Статус истории
              "is_public": true, // (обязательно, boolean)
              "cover_image_url": "https://.../history_preview_...jpg | null", // (опционально, string URL или null)
              "player_game_status": "playing | completed | error | null", // (опционально, string или null) Статус последнего не-error сохранения игрока
              "player_game_state_id": "uuid-string | null" // (опционально, string UUID или null) ID сохранения игрока
            }
            /* ... */
          ],
          "next_cursor": "string | null" // (обязательно, string или null)
        }
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный курсор или `limit`.
        *   `401 Unauthorized`: Невалидный токен.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

*   **`GET /api/v1/published-stories/public`**
    *   Описание: Получение списка **публичных** опубликованных историй. Поддерживает курсорную пагинацию.
    *   Аутентификация: **Требуется.**
    *   Query параметры:
        *   `limit` (опционально, int, default=10, max=100).
        *   `cursor` (опционально, string).
        *   `filter_adult` (опционально, boolean, default=false): Если true, исключает истории с контентом 18+.
    *   Ответ при успехе (`200 OK`): Пагинированный список `PublishedStorySummaryWithProgress`. Структура ответа **аналогична** `GET /api/v1/published-stories/me`.
        ```json
        {
          "data": [ /* ... */ ],
          "next_cursor": "string | null"
        }
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный курсор или `limit`.
        *   `401 Unauthorized`: Невалидный токен.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

*   **`GET /api/v1/published-stories/:story_id`**
    *   Описание: Получение детальной информации об **одной** опубликованной истории с распарсенными полями конфига/сетапа и списком сохранений текущего пользователя.
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:story_id` (обязательно, string, UUID) - UUID опубликованной истории.
    *   Ответ при успехе (`200 OK`): Объект `PublishedStoryParsedDetailDTO`.
        ```json
        {
          "id": "uuid-string", // (обязательно, string, UUID)
          "author_id": "uuid-string", // (обязательно, string, UUID)
          "author_name": "string", // (обязательно, string)
          "published_at": "timestamp-string", // (обязательно, string, timestamp)
          "likes_count": 15, // (обязательно, integer >= 0)
          "is_liked": true, // (обязательно, boolean) Лайкнул ли *текущий* пользователь
          "is_author": false, // (обязательно, boolean) Является ли *текущий* пользователь автором
          "is_public": true, // (обязательно, boolean)
          "is_adult_content": false, // (обязательно, boolean)
          "status": "ready | error | setup_pending | ...", // (обязательно, string) Статус истории
          "title": "Загадочный Особняк", // (обязательно, string)
          "short_description": "Исследуйте тайны старого поместья...", // (обязательно, string)
          "genre": "детектив", // (обязательно, string)
          "language": "ru", // (обязательно, string)
          "protagonist_name": "Сыщик", // (обязательно, string) Имя ГГ (Protagonist Name)
          "protagonist_description": "Описание ГГ", // (обязательно, string)
          "world_context": "Контекст мира", // (обязательно, string)
          "story_summary": "Общее саммари", // (обязательно, string)
          "core_stats": { // (обязательно, object) Статы с деталями из NovelSetup
            "sanity": { // (обязательно, object)
                "description": "Рассудок (детальное описание из setup)", // (обязательно, string)
                "initial_value": 10, // (обязательно, integer)
                "game_over_min": true, // (обязательно, boolean)
                "game_over_max": false, // (обязательно, boolean)
                "icon": "brain" // (опционально, string)
            },
            "clues": { /* ... */ }
          },
          "characters": [ // (обязательно, array) Массив персонажей из NovelSetup, может быть пустым []
            {
              "name": "Дворецкий", // (обязательно, string)
              "description": "Верный слуга... или нет?", // (обязательно, string)
              "personality": "Загадочный", // (опционально, string)
              "image_reference": "ch_butler_ref_123" // (опционально, string) Идентификатор для построения URL клиентом
            }
            /* ... */
          ],
          "cover_image_url": "https://.../history_preview_...jpg | null", // (опционально, string URL или null)
          "game_states": [ // (обязательно, array) Массив сохранений *текущего* пользователя, может быть пустым []
            {
              "id": "game-state-uuid-1", // (обязательно, string, UUID) ID сохранения
              "last_activity_at": "timestamp-string", // (обязательно, string, timestamp)
              "scene_index": 5, // (обязательно, integer) Индекс последней *завершенной* сцены
              "current_scene_summary": "Краткое описание сцены 10...", // (обязательно, string)
              "player_status": "playing | completed | error" // (обязательно, string) Статус этого сохранения
            }
            /* ... */
          ]
        }
        ```
        *   **Примечание:** Поля `core_stats` и `characters` будут доступны только если статус истории `ready` или `error` (т.е. генерация `NovelSetup` завершена, успешно или нет). Если статус `setup_pending` или подобный, эти поля могут быть `null` или пустыми. Клиент должен проверять статус истории.
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID.
        *   `401 Unauthorized`: Невалидный токен.
        *   `403 Forbidden`: Нет доступа к приватной истории.
        *   `404 Not Found`: История не найдена.
        *   `500 Internal Server Error`: Внутренняя ошибка (например, ошибка парсинга JSON или получения данных).

*   **`GET /api/v1/published-stories/:story_id/gamestates`**
    *   Описание: Получение списка состояний игры (сохранений) для **текущего пользователя** в указанной опубликованной истории.
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:story_id` (обязательно, string, UUID) - UUID опубликованной истории.
    *   Ответ при успехе (`200 OK`): Массив объектов `GameStateSummaryDTO`.
        ```json
        [ // (обязательно, array) Массив может быть пустым []
          {
            "id": "game-state-uuid-1", // (обязательно, string, UUID)
            "last_activity_at": "timestamp-string", // (обязательно, string, timestamp)
            "scene_index": 5, // (обязательно, integer)
            "current_scene_summary": "Краткое описание сцены 10...", // (обязательно, string)
            "player_status": "playing | completed | error" // (обязательно, string)
          }
          /* ... */
        ]
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID `story_id`.
        *   `401 Unauthorized`: Невалидный токен.
        *   `404 Not Found`: Опубликованная история не найдена.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

*   **`GET /api/v1/published-stories/:story_id/gamestates/:game_state_id/scene`**
    *   Описание: Получение текущей сцены для **конкретного состояния игры (сохранения)**.
    *   Аутентификация: **Требуется.**
    *   Параметры пути:
        *   `:story_id` (обязательно, string, UUID) - UUID опубликованной истории.
        *   `:game_state_id` (обязательно, string, UUID) - UUID состояния игры.
    *   Ответ при успехе (`200 OK`): Объект сцены (`GameSceneResponseDTO`).
        ```json
        {
          "id": "uuid-string", // (обязательно, string, UUID) ID текущей сцены
          "published_story_id": "uuid-string", // (обязательно, string, UUID)
          "game_state_id": "uuid-string", // (обязательно, string, UUID)
          "current_stats": { // (обязательно, object) Текущие статы игрока в этом сохранении
            "stat_key_1": 50, // (обязательно, integer)
            "stat_key_2": 35 // (обязательно, integer)
             // ... все статы из core_stats истории
          },
          // --- Поля, определяющие тип сцены (взаимоисключающие, кроме choices+continuation) ---
          "choices": [ // (опционально, array, может быть null/отсутствовать) Блоки выбора. Если есть, тип сцены "choices" или "continuation"
            {
              "shuffleable": false, // (обязательно, boolean)
              "character_name": "Advisor Zaltar | null", // (опционально, string или null/отсутствует)
              "description": "Описание блока/ситуации выбора", // (обязательно, string)
              "options": [ // (обязательно, array, минимум 1 элемент)
                {
                  "text": "Текст опции 1", // (обязательно, string)
                  "consequences": { // (опционально, object, может быть null/отсутствовать)
                    "response_text": "Текст-реакция на выбор | null", // (опционально, string или null/отсутствует)
                    "stat_changes": { "sanity": -15, "clues": 5 } // (опционально, object, может быть null/отсутствовать) Ключи - ID статов, значения - изменения
                  }
                },
                {
                  "text": "Текст опции 2", // (обязательно, string)
                  "consequences": null // (опционально, object или null)
                }
                /* ... */
              ]
            }
            /* ... другие блоки выбора */
          ],
          "ending_text": "Текст концовки игры... | null", // (опционально, string, может быть null/отсутствовать) Если есть, тип сцены "game_over"
          "continuation": { // (опционально, object, может быть null/отсутствовать) Если есть, тип сцены "continuation" (может сочетаться с choices)
            "new_player_description": "Описание нового персонажа...", // (обязательно, string, если continuation не null)
            "ending_text_previous": "Текст концовки для предыдущего персонажа...", // (обязательно, string, если continuation не null)
            "core_stats_reset": { // (обязательно, object, если continuation не null) Новые начальные значения статов
                 "stat_key_1": 10,
                 /* ... */
            }
          }
        }
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID `game_state_id`.
        *   `401 Unauthorized`: Невалидный токен.
        *   `404 Not Found`: Состояние игры не найдено или не принадлежит пользователю.
        *   `409 Conflict` (`{"code": ..., "message": "..."}`): Невозможно получить сцену из-за текущего статуса состояния игры (генерация, завершено и т.д.).
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

*   **`POST /api/v1/published-stories/:story_id/gamestates/:game_state_id/choice`**
    *   Описание: Отправка выбора игрока для текущей сцены в **конкретном состоянии игры**. Запускает асинхронную генерацию следующей сцены/концовки.
    *   Аутентификация: **Требуется.**
    *   **Rate Limit:** User-лимит (20/мин).
    *   Параметры пути:
        *   `:story_id` (обязательно, string, UUID) - UUID опубликованной истории.
        *   `:game_state_id` (обязательно, string, UUID) - UUID состояния игры.
    *   Тело запроса (`application/json`):
        ```json
        {
          // (обязательно, array of integers, >= 0)
          // Индексы выбранных опций. По одному индексу на каждый блок "choices" в текущей сцене.
          "selected_option_indices": [ 0, 1 ]
        }
        ```
    *   Ответ при успехе (`200 OK`): **Пустое тело.** Статус `PlayerGameState` изменится на `generating_scene` или `game_over_pending`. Клиент должен ждать WebSocket уведомления.
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID `game_state_id`, невалидное тело запроса (неверное кол-во индексов, индексы вне диапазона).
        *   `401 Unauthorized`: Невалидный токен.
        *   `404 Not Found`: Состояние игры не найдено или не принадлежит пользователю.
        *   `409 Conflict` (`{"code": ..., "message": "..."}`): Невозможно сделать выбор из-за текущего статуса состояния игры (не 'playing', уже идет генерация).
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

*   **`DELETE /api/v1/published-stories/:story_id/gamestates/:game_state_id`**
    *   Описание: Удаление конкретного состояния игры (слота сохранения).
    *   Аутентификация: **Требуется.**
    *   Параметры пути:
        *   `:story_id` (обязательно, string, UUID) - UUID опубликованной истории.
        *   `:game_state_id` (обязательно, string, UUID) - UUID удаляемого состояния игры.
    *   Ответ при успехе (`204 No Content`): Состояние игры успешно удалено.
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID.
        *   `401 Unauthorized`: Невалидный токен.
        *   `404 Not Found`: Опубликованная история или указанное состояние игры не найдены (или не принадлежат пользователю).
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

*   **`POST /api/v1/published-stories/:story_id/like`**
    *   Описание: Поставить лайк опубликованной истории.
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:story_id` (обязательно, string, UUID) - UUID опубликованной истории.
    *   Тело запроса: Нет.
    *   Ответ при успехе (`204 No Content`): Лайк успешно поставлен.
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID.
        *   `401 Unauthorized`: Невалидный токен.
        *   `404 Not Found`: История не найдена.
        *   `409 Conflict` (`{"message": "story already liked by this user"}`): Пользователь уже лайкнул эту историю.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

*   **`DELETE /api/v1/published-stories/:story_id/like`**
    *   Описание: Убрать лайк с опубликованной истории.
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:story_id` (обязательно, string, UUID) - UUID опубликованной истории.
    *   Тело запроса: Нет.
    *   Ответ при успехе (`204 No Content`): Лайк успешно убран.
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID.
        *   `401 Unauthorized`: Невалидный токен.
        *   `404 Not Found` (`{"message": "story not liked by this user yet"}`): Пользователь не лайкал эту историю или история не найдена.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

*   **`DELETE /api/v1/published-stories/:story_id`**
    *   Описание: Удаление **своей** опубликованной истории.
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:story_id` (обязательно, string, UUID) - UUID опубликованной истории.
    *   Тело запроса: Нет.
    *   Ответ при успехе (`204 No Content`): История успешно удалена.
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID.
        *   `401 Unauthorized`: Невалидный токен.
        *   `403 Forbidden`: Попытка удаления чужой истории.
        *   `404 Not Found`: История не найдена.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера при удалении.

*   **`POST /api/v1/published-stories/:story_id/retry`**
    *   Описание: Повторный запуск **начальных** шагов генерации для опубликованной истории с ошибкой (`status: "error"` или похожий) или отсутствующими компонентами (setup, первая сцена).
    *   Аутентификация: **Требуется.**
    *   **Rate Limit:** User-лимит (20/мин).
    *   Параметр пути: `:story_id` (обязательно, string, UUID) - UUID опубликованной истории.
    *   Тело запроса: Нет.
    *   Ответ при успехе (`202 Accepted`): **Пустое тело.** Статус истории асинхронно изменится на соответствующий (например, `setup_pending` или `first_scene_pending`).
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID.
        *   `401 Unauthorized`: Невалидный токен.
        *   `403 Forbidden`: Попытка ретрая чужой истории (если проверка добавлена).
        *   `404 Not Found`: История не найдена.
        *   `409 Conflict` (`{"message": "..."}`): История не в статусе, допускающем ретрай.
        *   `500 Internal Server Error`: Ошибка при обновлении статуса или постановке задачи.

*   **`POST /api/v1/published-stories/:story_id/gamestates/:game_state_id/retry`**
    *   Описание: Повторный запуск генерации **конкретной** сцены для состояния игры с ошибкой (`playerStatus: "error"`).
    *   Аутентификация: **Требуется.**
    *   **Rate Limit:** User-лимит (20/мин).
    *   Параметры пути:
        *   `:story_id` (обязательно, string, UUID) - UUID опубликованной истории.
        *   `:game_state_id` (обязательно, string, UUID) - UUID состояния игры.
    *   Тело запроса: Нет.
    *   Ответ при успехе (`202 Accepted`): **Пустое тело.** Статус состояния игры асинхронно изменится на `generating_scene`.
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID.
        *   `401 Unauthorized`: Невалидный токен.
        *   `403 Forbidden`: Попытка ретрая чужого состояния игры.
        *   `404 Not Found`: История или состояние игры не найдены.
        *   `409 Conflict` (`{"message": "Game state is not in error state"}`): Состояние игры не в статусе ошибки.
        *   `500 Internal Server Error`: Ошибка при обновлении статуса или постановке задачи.

*   **`PATCH /api/v1/published-stories/:story_id/visibility`**
    *   Описание: Изменение видимости **своей** опубликованной истории. История должна быть в статусе `ready`. Истории с контентом 18+ не могут быть сделаны публичными.
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:story_id` (обязательно, string, UUID) - UUID опубликованной истории.
    *   Тело запроса (`application/json`):
        ```json
        {
          "is_public": true // (обязательно, boolean) Новое значение видимости
        }
        ```
    *   Ответ при успехе (`204 No Content`): Видимость успешно изменена.
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID или тело запроса.
        *   `401 Unauthorized`: Невалидный токен.
        *   `403 Forbidden`: Попытка изменить видимость чужой истории.
        *   `404 Not Found`: История не найдена.
        *   `409 Conflict` (`{"message": "Story is not ready yet" | "Cannot make adult content public"}`): История не готова к публикации или контент 18+.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

*   **`POST /api/v1/published-stories/:story_id/gamestates`**
    *   Описание: Создание нового состояния игры (сохранения) для **текущего пользователя** в указанной опубликованной истории (при нажатии "Начать игру"). Может быть создан только один слот сохранения на пользователя на историю. История должна быть в статусе `ready`.
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:story_id` (обязательно, string, UUID) - UUID опубликованной истории.
    *   Тело запроса: Нет.
    *   Ответ при успехе (`201 Created`): Объект `PlayerGameState` созданного сохранения.
        ```json
        {
          "id": "uuid-string", // (обязательно, string, UUID) ID созданного состояния игры
          "player_id": "uuid-string", // (обязательно, string, UUID) ID пользователя
          "published_story_id": "uuid-string", // (обязательно, string, UUID) ID истории
          "player_progress_id": "uuid-string", // (обязательно, string, UUID) ID связанного узла прогресса
          "current_scene_id": "uuid-string | null", // (опционально, string UUID или null) ID текущей сцены (первой сцены истории)
          "player_status": "playing", // (обязательно, string) Статус сохранения
          "started_at": "timestamp-string", // (обязательно, string, timestamp)
          "last_activity_at": "timestamp-string", // (обязательно, string, timestamp)
          "error_details": null, // (опционально, string или null)
          "completed_at": null, // (опционально, string timestamp или null)
          "ending_text": null // (опционально, string или null)
        }
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID `story_id`.
        *   `401 Unauthorized`: Невалидный токен.
        *   `404 Not Found`: Опубликованная история не найдена.
        *   `409 Conflict` (`{"code": "SAVE_SLOT_EXISTS", ...}` | `{"code": "STORY_NOT_READY", ...}`): Слот сохранения уже существует для этого пользователя/истории, или история еще не готова к игре (статус не `ready`).
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

*   **`GET /api/v1/published-stories/me/progress`**
    *   Описание: Получение списка **своих** опубликованных историй, **только тех, в которых есть прогресс** (т.е. существует хотя бы одно сохранение).
    *   Аутентификация: **Требуется.**
    *   Query параметры:
        *   `limit` (опционально, int, default=10, max=100).
        *   `cursor` (опционально, string).
        *   `filter_adult` (опционально, boolean, default=false): Если true, исключает истории с контентом 18+.
    *   Ответ при успехе (`200 OK`): Пагинированный список `PublishedStorySummaryWithProgress`. Структура ответа **аналогична** `GET /api/v1/published-stories/me`.
        ```json
        {
          "data": [ /* ... */ ],
          "next_cursor": "string | null"
        }
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный курсор или `limit`.
        *   `401 Unauthorized`: Невалидный токен.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

*   **`GET /api/v1/published-stories/liked`**
    *   Описание: Получение списка опубликованных историй, которые **лайкнул текущий пользователь**. Поддерживает курсорную пагинацию. Истории отсортированы по времени добавления лайка (сначала самые недавние).
    *   Аутентификация: **Требуется.**
    *   Query параметры:
        *   `limit` (опционально, int, default=10, max=100).
        *   `cursor` (опционально, string).
    *   Ответ при успехе (`200 OK`): Пагинированный список `PublishedStorySummaryWithProgress`. Структура ответа **аналогична** `GET /api/v1/published-stories/me`.
        ```json
        {
          "data": [ /* ... */ ],
          "next_cursor": "string | null"
        }
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный курсор или `limit`.
        *   `401 Unauthorized`: Невалидный токен.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

--- Конец секции Gameplay Service ---

---

#### Сервис WebSocket (`/ws`)

Предоставляет WebSocket соединение для получения уведомлений в реальном времени.

*   **URL:** `wss://crion.space/ws?token=<ваш_access_token>` (пример, используйте ваш настроенный URL)
*   **Аутентификация:** Через query-параметр `token`.
*   **Сообщения от сервера:** Сервер отправляет JSON-сообщения. Основное поле `update_type` определяет тип события. Все ключи в JSON - **snake_case**.
    *   **Обновление Черновика (`draft`):**
        *   Описание: Отправляется после завершения генерации или ревизии черновика (успешно или с ошибкой).
        *   Структура (`sharedModels.ClientStoryUpdate`):
        ```json
        {
          "id": "uuid-string", // ID черновика (StoryConfig)
          "user_id": "uuid-string", // ID пользователя (Игнорируется клиентом)
          "update_type": "draft", // Тип обновления
          "status": "draft | error", // Итоговый статус черновика
          "error_details": "Сообщение об ошибке | null" // Опционально, только при status = error
        }
        ```
    *   **Общее обновление Опубликованной Истории (`story`):**
        *   Описание: Отправляется при изменении статуса PublishedStory (например, после генерации Setup, изображений, или при ошибке).
        *   Структура (`sharedModels.ClientStoryUpdate`):
        ```json
        {
          "id": "uuid-string", // ID опубликованной истории (PublishedStoryID)
          "user_id": "uuid-string", // ID пользователя (Игнорируется клиентом)
          "update_type": "story", // Тип обновления
          "status": "string", // Новый статус PublishedStory (ready, error, setup_pending, first_scene_pending, images_pending, etc.)
          // --- Опциональные поля (зависят от причины обновления) ---
          "error_details": "Сообщение об ошибке | null",
          "story_title": "Заголовок Истории | null", // Может отправляться для удобства клиента
          "is_public": true | false | null, // Текущая публичность
          "cover_image_url": "URL | null" // URL обложки, если готова
          // Поля scene_id, state_hash, ending_text здесь НЕ ожидаются.
        }
        ```
    *   **Обновление Состояния Игры (`game_state`):**
        *   Описание: Отправляется при изменении статуса PlayerGameState, обычно после успешной генерации сцены/концовки или при ошибке генерации сцены.
        *   Структура (`sharedModels.ClientStoryUpdate`):
        ```json
        {
          "id": "uuid-string", // ID Состояния Игры (GameStateID)!
          "user_id": "uuid-string", // ID пользователя (Игнорируется клиентом)
          "update_type": "game_state", // Тип обновления
          "status": "playing | completed | error | generating_scene | game_over_pending", // Новый статус PlayerGameState
          // --- Поля, определяющие результат ---
          "scene_id": "uuid-string | null", // ID сцены, которая стала текущей (при успехе: status='playing' или 'completed')
          "state_hash": "string | null", // Хэш состояния, связанный с новой сценой (при успехе)
          "ending_text": "Текст концовки | null", // Если игра завершена (status='completed')
          "error_details": "Сообщение об ошибке | null" // Опционально, при status = error
          // Поле story_title здесь не ожидается.
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
*   `loc_arg_*`: Аргументы, которые нужно подставить в строку локализации (например, `loc_arg_story_title`). Используйте константы `constants.PushLocArg*` (например, `constants.PushLocArgStoryTitle`). Все ключи аргументов - **snake_case**.
*   `fallback_title`: Запасной заголовок на случай, если локализация не удалась.
*   `fallback_body`: Запасное тело сообщения.
*   Другие необходимые данные (`story_config_id`, `published_story_id` и т.д.). Все ключи - **snake_case**.

**Задача клиента:**

1.  **Получить data payload:** Обработать получение data-only уведомления (даже в background/terminated). Это особенно важно для iOS.
    *   **Android:** Использовать `FirebaseMessagingService`.
    *   **iOS:** **Обязательно** использовать **Notification Service Extension** для перехвата и модификации уведомления *до* его отображения, или Background Push с показом *локального* уведомления.
2.  **Извлечь данные:** Получить `loc_key`, все `loc_arg_*`, `fallback_title`, `fallback_body` из **`data` payload** уведомления.
3.  **Выполнить локализацию:** Попробовать найти строку перевода по `loc_key` и подставить аргументы `loc_arg_*`.
4.  **Определить текст:** Если локализация удалась, использовать переведенные строки. Если нет (или ключа `loc_key` не было), использовать `fallback_title` и `fallback_body`.
5.  **Отобразить уведомление:** Создать и отобразить *локальное* уведомление (или модифицировать входящее через Extension на iOS) с полученным заголовком и телом.

**Типы уведомлений и их данные:**

Ниже перечислены основные события, по которым отправляются push-уведомления, и данные, которые они содержат в `data` payload. Все ключи - **snake_case**.

*   **Черновик готов:**
    *   `loc_key`: `notification_draft_ready` (`constants.PushLocKeyDraftReady`)
    *   `data`:
        ```json
        {
          "story_config_id": "uuid-string",
          "event_type": "draft_ready",
          "loc_key": "notification_draft_ready",
          "loc_arg_story_title": "Название Черновика",
          "fallback_title": "Черновик готов!",
          "fallback_body": "Ваш черновик "Название Черновика" готов к настройке.",
          "title": "Название Черновика"
        }
        ```
*   **Setup готов (ожидание первой сцены):**
    *   `loc_key`: `notification_setup_ready` (`constants.PushLocKeySetupReady`)
    *   `data`:
        ```json
        {
          "published_story_id": "uuid-string",
          "event_type": "setup_pending",
          "loc_key": "notification_setup_ready",
          "loc_arg_story_title": "Название Истории",
          "fallback_title": "История "Название Истории" почти готова...",
          "fallback_body": "Скоро можно будет начать играть!",
          "title": "Название Истории"
        }
        ```
*   **История готова к игре:** (После генерации Setup, первой сцены и всех изображений)
    *   `loc_key`: `notification_story_ready` (`constants.PushLocKeyStoryReady`)
    *   `data`:
        ```json
        {
          "published_story_id": "uuid-string",
          "event_type": "story_ready",
          "loc_key": "notification_story_ready",
          "loc_arg_story_title": "Название Истории",
          "fallback_title": "История готова!",
          "fallback_body": "Ваша история "Название Истории" готова к игре!",
          "title": "Название Истории",
          "author_name": "Имя Автора"
        }
        ```
*   **Новая сцена готова:**
    *   `loc_key`: `notification_scene_ready` (`constants.PushLocKeySceneReady`)
    *   `data`:
        ```json
        {
          "published_story_id": "uuid-string",
          "game_state_id": "uuid-string",
          "scene_id": "uuid-string",
          "event_type": "scene_ready",
          "loc_key": "notification_scene_ready",
          "loc_arg_story_title": "Название Истории",
          "fallback_title": "Новая сцена готова!",
          "fallback_body": "Новая сцена в истории "Название Истории" готова!",
          "title": "Название Истории",
          "author_name": "Имя Автора" // Добавлено
        }
        ```
*   **Игра завершена (Game Over):**
    *   `loc_key`: `notification_game_over` (`constants.PushLocKeyGameOver`)
    *   `data`:
        ```json
        {
          "published_story_id": "uuid-string",
          "game_state_id": "uuid-string",
          "scene_id": "uuid-string", // ID сцены с концовкой
          "event_type": "game_over",
          "loc_key": "notification_game_over",
          "loc_arg_story_title": "Название Истории",
          "loc_arg_ending_text": "Текст концовки...",
          "fallback_title": "Игра завершена!",
          "fallback_body": "История "Название Истории" завершена.",
          "title": "Название Истории",
          "author_name": "Имя Автора" // Добавлено
        }
        ```

---

## Игровой процесс: Взаимодействие Клиент-Сервер

Этот раздел описывает типичный цикл взаимодействия между клиентским приложением (например, мобильным) и сервером во время прохождения опубликованной истории.

1.  **Начало игры (Создание Сохранения):**
    *   Пользователь выбирает историю и нажимает "Начать игру" (или "Продолжить", если есть сохранения).
    *   Если сохранений нет или пользователь хочет начать заново, клиент отправляет запрос:
        *   `POST /api/v1/published-stories/:story_id/gamestates`
    *   Сервер создает новое состояние игры (`PlayerGameState`) и возвращает его (`201 Created`):
        ```json
        {
          "id": "new-game-state-uuid", // ID созданного состояния
          // ... другие поля PlayerGameState, включая player_status='playing'
        }
        ```
    *   Клиент **сохраняет** полученный `id` (это `game_state_id` для дальнейших запросов).
    *   Теперь можно сразу переходить к шагу 2 (запрос первой сцены), так как статус `playing`.

2.  **Запрос текущей сцены:**
    *   Клиент использует сохраненный `game_state_id` и отправляет запрос:
        *   `GET /api/v1/published-stories/:story_id/gamestates/:game_state_id/scene`
    *   Сервер возвращает текущую сцену (`200 OK`, см. описание DTO `GameSceneResponseDTO` выше) или ошибку:
        *   `409 Conflict`: Если сцена еще генерируется (статус `generating_scene` или `game_over_pending`). Клиент должен подождать WebSocket-события.
        *   `404 Not Found`: Состояние игры не найдено.
        *   `5xx`: Внутренняя ошибка.

3.  **Отображение сцены и выбор игрока:**
    *   Клиент парсит полученный `GameSceneResponseDTO`.
    *   Если есть поле `choices`, отображает текст (`description`) и варианты (`options[].text`).
    *   Если есть поле `ending_text`, отображает текст концовки.
    *   Если есть поле `continuation`, обрабатывает переход к новому персонажу.
    *   Пользователь делает выбор (нажимает на один из вариантов в блоке `choices`). Клиент запоминает индекс(ы) выбранного варианта (например, `[0]` или `[1]`).

4.  **Отправка выбора:**
    *   Клиент отправляет сделанный выбор на сервер:
        *   `POST /api/v1/published-stories/:story_id/gamestates/:game_state_id/choice`
        *   Тело запроса: `{"selected_option_indices": [ index1, index2, ...]}`
    *   Сервер принимает выбор (`200 OK`), обновляет статус `PlayerGameState` на `generating_scene` (или `game_over_pending`) и **асинхронно** запускает задачу на генерацию следующей сцены или концовки.

5.  **Ожидание следующей сцены/концовки (WebSocket):**
    *   Клиент **обязательно** должен слушать WebSocket-события после отправки выбора. Сервер отправит сообщение типа `game_state`.
    *   **Тип События:** `game_state` (`sharedModels.UpdateTypeGameState`)
    *   **Payload (`sharedModels.ClientStoryUpdate`):**
        ```json
        {
          "id": "game-state-uuid", // ID Состояния Игры
          "user_id": "uuid-string", // ID пользователя (Игнорируется клиентом)
          "update_type": "game_state",
          "status": "playing | completed | error", // Новый статус PlayerGameState
          // --- Поля, определяющие результат ---
          "scene_id": "uuid-string | null", // ID новой сцены/концовки (если успех)
          "state_hash": "string | null", // Хэш состояния, связанный с новой сценой (если успех)
          "ending_text": "Текст концовки | null", // Если игра завершена (status='completed')
          "error_details": "Сообщение об ошибке | null" // Если произошла ошибка генерации (status='error')
        }
        ```
    *   **Действия клиента:**
        *   **При Успехе (Сцена/Концовка Готова):** Если `status` равен `playing` или `completed`, значит генерация завершена. Клиент должен **перейти к шагу 2** (Запрос текущей сцены с тем же `game_state_id`). Если `status` = `completed`, игра завершена.
        *   **При Ошибке:** Если `status` равен `error` и присутствует `error_details`, произошла ошибка генерации. Клиент должен показать пользователю сообщение об ошибке. Можно предложить кнопку "Попробовать снова", которая вызовет:
            *   `POST /api/v1/published-stories/:story_id/gamestates/:game_state_id/retry`
            После успешного ответа `202 Accepted` на ретрай, клиент снова **переходит к шагу 5** (Ожидание WebSocket).

6.  **Цикл:** Шаги 2-5 повторяются, пока `status` в WebSocket-сообщении не станет `completed`.