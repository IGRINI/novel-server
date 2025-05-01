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

Конкретные URL формируются следующим образом:

*   **Превью опубликованной истории:**
    *   Формат: `[Базовый URL изображений]/history_preview_{publishedStoryID}.jpg`
    *   Пример: `https://crion.space/generated-images/history_preview_a1b2c3d4-e5f6-7890-1234-567890abcdef.jpg`
    *   Где `{publishedStoryID}` - это UUID опубликованной истории.

*   **Изображение персонажа:**
    *   Формат: `[Базовый URL изображений]/{imageReference}.jpg`
    *   Где `{imageReference}` - это уникальный идентификатор, который `gameplay-service` передает в `image-generator` при постановке задачи на генерацию изображения персонажа (например, `character_{characterID}_{taskID}`). Этот же `imageReference` возвращается в поле `characters[].imageReference` эндпоинта `GET /api/published-stories/:id`.
    *   Пример: `https://crion.space/generated-images/character_b2c3d4e5-f6a7-8901-2345-67890abcdef12_task12345.jpg`
    *   **Примечание:** Бэкенд не возвращает готовые URL изображений персонажей в API. Фронтенду необходимо будет получить `imageReference` (вероятно, из данных Setup истории) и самостоятельно конструировать полный URL.

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
          \"access_token\": \"string (jwt)\",
          \"refresh_token\": \"string (jwt)\"
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
          \"refresh_token\": \"string (jwt)\"
        }
        ```
    *   Ответ при успехе (`200 OK`):
        ```json
        {
          \"access_token\": \"string (jwt)\",
          \"refresh_token\": \"string (jwt)\"
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
          \"refresh_token\": \"string (jwt)\" // Токен, который нужно отозвать
        }
        ```
    *   Ответ при успехе (`200 OK`):
        ```json
        {
          \"message\": \"Successfully logged out\"
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
          \"token\": \"string (jwt)\"
        }
        ```
    *   Ответ при успехе (`200 OK`):
        ```json
        {
          \"valid\": true,
          \"user_id\": \"uuid-string\",
          \"access_uuid\": \"uuid-string\" // UUID самого токена
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
          \"id\": \"uuid-string\", // UUID пользователя
          \"username\": \"string\",
          \"displayName\": \"string\", // Отображаемое имя, может совпадать с username
          \"email\": \"string\",
          \"roles\": [\"user\", \"...\"], // Список ролей
          \"isBanned\": false
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
          \"token\": \"string (device token)\",  // Токен, полученный от FCM/APNS
          \"platform\": \"string (ios|android)\" // Платформа устройства ('ios' или 'android')
        }
        ```
    *   Ответ при успехе (`200 OK`):
        ```json
        {
          \"message\": \"Device token registered successfully\"
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
          \"token\": \"string (device token)\" // Токен, который нужно удалить
        }
        ```
    *   Ответ при успехе (`200 OK`):
        ```json
        {
          \"message\": \"Device token unregistered successfully\"
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
          \"prompt\": \"Текст начального запроса пользователя...\",
          \"language\": \"string\" // Код языка (например, \"en\", \"ru\"). Обязательное поле. Поддерживаемые: en, fr, de, es, it, pt, ru, zh, ja.
        }
        ```
    *   Ответ при успехе (`202 Accepted`): Возвращает созданный объект `StoryConfig` со статусом `generating`.
        ```json
        {
          \"id\": \"uuid-string\", // ID созданного черновика
          \"status\": \"generating\"
        }
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидное тело запроса (например, отсутствует `prompt` или `language`, неподдерживаемый язык).
        *   `401 Unauthorized`: Невалидный токен.
        *   `409 Conflict` (`{\"message\": \"User already has an active generation task\"}`): У пользователя уже есть активная задача генерации.
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
          \"data\": [
            {
              \"id\": \"uuid-string\",
              \"title\": \"string\", // Может быть пустым, если генерация еще идет
              \"description\": \"string\", // Может быть user_input, если генерация еще идет
              \"createdAt\": \"timestamp\",
              \"status\": \"generating | draft | error\"
            }
            /* ... */
          ],
          \"next_cursor\": \"string | null\"
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
              \"id\": \"uuid-string\",
              \"createdAt\": \"timestamp\",
              \"status\": \"generating | error\",
              \"config\": null // Поле config будет null
            }
            ```
        *   **Статус `draft`:** `StoryConfigParsedDetail` (распарсенные поля из `config`)
            ```json
            {
              \"title\": \"string\",
              \"shortDescription\": \"string\",
              \"franchise\": \"string | null\",
              \"genre\": \"string\",
              \"language\": \"string\",
              \"isAdultContent\": false,
              \"playerName\": \"string\",
              \"playerDescription\": \"string\",
              \"worldContext\": \"string\",
              \"storySummary\": \"string\",
              \"coreStats\": { // Словарь статов
                \"stat_key_1\": {
                  \"description\": \"string\",
                  \"initialValue\": 10,
                  \"gameOverConditions\": {
                    \"min\": false, // true, если Game Over при мин. значении
                    \"max\": false  // true, если Game Over при макс. значении
                  }
                },
                \"stat_key_2\": { ... }
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
          \"revision_prompt\": \"Текст инструкции для изменения...\" // Поле называется revision_prompt
        }
        ```
    *   Ответ при успехе (`202 Accepted`): **Пустое тело.** Статус черновика изменится на `generating`.
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID или тело запроса (отсутствует `revision_prompt`).
        *   `401 Unauthorized`: Невалидный токен.
        *   `403 Forbidden`: Попытка доступа к чужому черновику.
        *   `404 Not Found`: Черновик не найден.
        *   `409 Conflict` (`{\"message\": \"Story config is not in draft state\" | \"User already has an active generation task\"}`): Черновик не готов к ревизии или у пользователя уже есть задача.
        *   `500 Internal Server Error`: Ошибка при обновлении БД или постановке задачи.

*   **`POST /api/stories/:id/publish`**
    *   Описание: Публикация готового черновика. Создает запись `PublishedStory` и первую сцену на основе конфига.
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:id` - UUID черновика (`StoryConfig`).
    *   Тело запроса: Нет.
    *   Ответ при успехе (`201 Created`): Объект `PublishedStory` (или его ID?).
        ```json
        {
          \"published_story_id\": \"uuid-string\"
        }
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID.
        *   `401 Unauthorized`: Невалидный токен.
        *   `403 Forbidden`: Попытка доступа к чужому черновику.
        *   `404 Not Found`: Черновик не найден.
        *   `409 Conflict` (`{\"message\": \"Story config is not in draft state\"}`): Черновик не готов к публикации.
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
          \"data\": [
            {
              \"id\": \"uuid-string\",
              \"title\": \"string\",
              \"short_description\": \"string\", // <-- Обновлено поле
              \"author_id\": \"uuid-string\",
              \"author_name\": \"string\", // <-- Добавлено имя автора
              \"published_at\": \"timestamp\",
              \"is_adult_content\": false, // <-- Обновлено поле
              \"likes_count\": 123,
              \"is_liked\": true,
              "hasPlayerProgress": false // Есть ли прогресс у текущего пользователя
              "status": "ready | completed | error | ..." // <<< ДОБАВЛЯЕМ СТАТУС НАЗАД
              "isPublic": true // <<< ДОБАВЛЕНО: Является ли история публичной
            }
            /* ... */
          ],
          \"next_cursor\": \"string | null\"
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
          \"data\": [
            {
              \"id\": \"uuid-string\",
              \"title\": \"string\",
              \"short_description\": \"string\", // <-- Обновлено поле
              \"author_id\": \"uuid-string\",
              \"author_name\": \"string\", // <-- Добавлено имя автора
              \"published_at\": \"timestamp\",
              \"is_adult_content\": false, // <-- Обновлено поле
              \"likes_count\": 123,
              \"is_liked\": false, // Лайкнул ли текущий пользователь
              \"hasPlayerProgress\": true // Есть ли прогресс у текущего пользователя
              \"status\": \"ready | completed | error | ...\" // <<< ДОБАВЛЯЕМ СТАТУС НАЗАД
              "isPublic": true // <<< ДОБАВЛЕНО: Является ли история публичной
            }
            /* ... */
          ],
          \"next_cursor\": \"string | null\"
        }
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный курсор или `limit`.
        *   `401 Unauthorized`: Невалидный токен.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

*   **`GET /api/published-stories/:id`**
    *   Описание: Получение детальной информации об **одной** опубликованной истории с распарсенными полями конфига/сетапа и списком сохранений текущего пользователя.
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:id` - UUID опубликованной истории (`PublishedStory`).
    *   Ответ при успехе (`200 OK`): Объект `PublishedStoryParsedDetailDTO`.
        ```json
        {
          "id": "uuid-string",
          "authorId": "uuid-string",
          "authorName": "string",
          "publishedAt": "timestamp-string",
          "likesCount": number,
          "isLiked": boolean,
          "isAuthor": boolean,
          "isPublic": boolean,
          "isAdultContent": boolean,
          "status": "ready | completed | error | setup_pending | generating_scene",
          // Распарсенные поля:
          "title": "string",
          "shortDescription": "string",
          "genre": "string",
          "language": "string",
          "playerName": "string",
          "coreStats": { /* ... как было ... */ },
          "characters": [ /* ... как было ... */ ],
          "previewImageUrl": "string | null",
          // Список сохранений:
          "gameStates": [
            {
              "id": "uuid-string", // ID состояния игры (gameStateID)
              "lastActivityAt": "timestamp-string"
            }
            // ... другие сохранения (отсортированы по lastActivityAt desc)
          ]
        }
        ```
        *   **Примечание:** Поля, относящиеся к *одному* прогрессу (`hasPlayerProgress`, `lastPlayedAt`, `currentSceneIndex`, `currentSceneSummary`, `currentPlayerStats`), больше не возвращаются. Используйте `gameStates`.
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
            "id": "uuid-string", // ID состояния игры (использовать в /scene, /choice, /gamestates/:id)
            "lastActivityAt": "timestamp-string" // Время последней активности
          },
          // ... другие сохранения
        ]
        ```
        *   Примечание: Возвращается пустой массив `[]`, если сохранений нет.
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID `story_id`.
        *   `401 Unauthorized`: Невалидный токен.
        *   `404 Not Found`: Опубликованная история не найдена.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

*   **`GET /api/published-stories/gamestates/:game_state_id/scene`**
    *   Описание: Получение текущей сцены для **конкретного состояния игры (сохранения)**. Если идет генерация следующей сцены для этого состояния, возвращается ошибка `409 Conflict`.
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:game_state_id` - UUID состояния игры (`PlayerGameState`).
    *   Ответ при успехе (`200 OK`): Объект сцены (`GameSceneResponseDTO`).
        ```json
        {
          "id": "uuid-string", // ID текущей сцены
          "publishedStoryId": "uuid-string", // ID опубликованной истории
          "gameStateId": "uuid-string", // ID состояния игры (game_state_id из пути)
          "currentStats": { // Текущие статы игрока в этом сохранении
            "stat_key_1": 50,
            "stat_key_2": 35
          },
          // --- Поля для type="choices" или "continuation" ---
          "choices": [
            {
              "shuffleable": false, // Можно ли перемешивать (sh: 1 = true, 0 = false)
              "characterName": "Advisor Zaltar", // <<< НОВОЕ ПОЛЕ: Имя персонажа из поля 'char'
              "description": "Описание блока/ситуации выбора", // Текст из 'desc'
              "options": [
                {
                  "text": "Текст опции 1", // Текст опции (Используется ключ 'text')
                  "consequences": { // ПОСЛЕДСТВИЯ ОПЦИИ (может быть null)
                    "responseText": "Текст-реакция на выбор (если есть)", // Текст из 'rt', КЛИЕНТУ ОТДАЕТСЯ КАК responseText
                    "statChanges": { "Wealth": -15, "Army": 5 } // Изменения статов (из 'cs'), КЛИЕНТУ ОТДАЕТСЯ КАК statChanges, будет null если нет
                  }
                },
                {
                  "text": "Текст опции 2", // Текст опции (Используется ключ 'text')
                  "consequences": null // БУДЕТ null, если нет ни 'responseText', ни 'statChanges'
                }
              ]
            }
            // ... другие блоки выбора
          ],
          // --- Поле для type="game_over" ---
          "endingText": "Текст концовки игры...", // Текст из 'et' (будет null для других типов)
          // --- Поле для type="continuation" ---
          "continuation": { // Будет null для других типов
            "newPlayerDescription": "Описание нового персонажа...", // Текст из 'npd'
            "endingTextPrevious": "Текст концовки для предыдущего персонажа...", // Текст из 'etp'
            "coreStatsReset": { "stat_key_1": 10, ... } // Новые базовые статы из 'csr'
          }
        }
        ```
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID `game_state_id`.
        *   `401 Unauthorized`: Невалидный токен.
        *   `404 Not Found`: Состояние игры (`PlayerGameState`) не найдено или не принадлежит пользователю.
        *   `409 Conflict` (`{"code": ..., "message": "Scene generation in progress" | "Game over generation in progress" | "Game already completed"}`): Невозможно получить сцену из-за текущего статуса состояния игры.
        *   `500 Internal Server Error`: Внутренняя ошибка сервера.

*   **`POST /api/published-stories/gamestates/:game_state_id/choice`**
    *   Описание: Отправка выбора игрока для текущей сцены в **конкретном состоянии игры (сохранении)**. Запускает процесс обновления состояния и генерации следующей сцены/концовки (асинхронно).
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:game_state_id` - UUID состояния игры (`PlayerGameState`).
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
        *   `409 Conflict` (`{\"message\": \"story already liked by this user\"}`): Пользователь уже лайкнул эту историю.
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
        *   `404 Not Found` (`{\"message\": \"story not liked by this user yet\"}`): Пользователь не лайкал эту историю.
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
    *   Ответ при успехе (`202 Accepted`): **Пустое тело.** Статус истории изменится на `setup_pending` или `generating_scene` (в зависимости от того, что упало).
    *   Ответ при ошибке:
        *   `400 Bad Request`: Невалидный UUID.
        *   `401 Unauthorized`: Невалидный токен.
        *   `404 Not Found`: История не найдена.
        *   `409 Conflict` (`{\"message\": "Story is not in error state"}`): История не в статусе ошибки.
        *   `500 Internal Server Error`: Ошибка при обновлении статуса или постановке задачи.

*   **`PATCH /api/published-stories/:id/visibility`**
    *   Описание: Изменение видимости **своей** опубликованной истории (сделать публичной или приватной).
    *   Аутентификация: **Требуется.**
    *   Параметр пути: `:id` - UUID опубликованной истории (`PublishedStory`).
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
            "scene_id": "uuid-string",
            "state_hash": "string" // Хеш состояния, для которого сгенерирована сцена
          }
        }
        ```
    *   **Ошибка генерации сцены (`scene_error`):**
        ```json
        {
          "event": "scene_error",
          "payload": {
            "published_story_id": "uuid-string",
            "state_hash": "string", // Хеш состояния, для которого произошла ошибка
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

## Локальная разработка

*   **Просмотр логов:** `docker-compose logs -f <имя_сервиса>` (например, `docker-compose logs -f auth-service`).
*   **Пересборка одного сервиса:** `docker-compose build <имя_сервиса>`
*   **Перезапуск одного сервиса:** `docker-compose up -d --no-deps --build <имя_сервиса>`

## Структура папок

*   `auth/`: Сервис аутентификации.
*   `story-generator/`: Сервис генерации историй (воркер).
*   `websocket-service/`: Сервис WebSocket уведомлений.
*   `gameplay-service/`: Сервис управления игровым процессом.
*   `admin-service/`: Сервис администрирования (может быть в будущем).
*   `notification-service/`: Сервис отправки push-уведомлений (может быть в будущем).
*   `shared/`: Общие пакеты Go (модели данных, интерфейсы репозиториев, утилиты и т.д.).
*   `deploy/`: Файлы для деплоя (например, конфигурация Docker, скрипты).
*   `prompts/`: Промпты для AI.
*   `landing-page/`: Статический лендинг (если есть).

## TODO

*   [ ] Добавить эндпоинт для редактирования профиля пользователя.
*   [ ] Реализовать систему ролей и прав доступа.
*   [ ] Добавить возможность загрузки аватара пользователя.
*   [ ] Реализовать административную панель.
*   [ ] Добавить тесты.
*   [ ] Оптимизировать запросы к БД.
*   [ ] Добавить обработку ошибок при публикации в RabbitMQ (retry, dead-letter queue).
*   [ ] Улучшить систему логирования (трассировка запросов).
*   [ ] Рассмотреть использование ORM (например, GORM или sqlx) вместо прямого SQL.
*   [ ] Документировать внутренние API взаимодействия между сервисами.
*   [ ] Добавить `admin-service` для управления пользователями и контентом.
*   [ ] Добавить `notification-service` для отправки push-уведомлений.
*   [ ] **Важно:** Пересмотреть формат `CoreStats` в `StoryConfigParsedDetail`, возможно, вернуть `Min` и `Max` для удобства фронтенда.
*   [ ] **Важно:** Уточнить возвращаемое значение `POST /auth/register`.
*   [ ] **Важно:** Проверить/дополнить обработку `409 Conflict`