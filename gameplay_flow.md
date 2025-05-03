# Описание игрового процесса (Gameplay Flow)

Данный документ описывает основные сценарии взаимодействия пользователя с системой через `gameplay-service`.

## 1. Создание и публикация истории

1.  **Инициация генерации (Черновик):**
    *   Пользователь отправляет начальный промпт и выбирает язык (`POST /api/v1/stories/generate`).
    *   `GameplayHandler.generateInitialStory` валидирует запрос (включая язык).
    *   `DraftService.GenerateInitialStory` создает запись `StoryConfig` в БД со статусом `generating` и отправляет задачу генерации (с промптом, userID, draftID, языком) в очередь RabbitMQ (`generation_tasks`).
    *   Пользователь получает ответ `202 Accepted` с ID и начальным статусом черновика.
    *   `story-generator` получает задачу, генерирует полный JSON-конфиг истории (описание, персонажи, статы, мир и т.д.).
    *   `story-generator` отправляет результат (JSON-конфиг и draftID) в очередь `internal_updates`.
    *   `NotificationConsumer` в `gameplay-service` получает результат, обновляет `StoryConfig` в БД (записывает JSON в поле `config`, меняет статус на `complete`).
    *   (Опционально) `NotificationConsumer` отправляет уведомление клиенту через `client_updates`.
2.  **Просмотр и доработка черновика:**
    *   Пользователь запрашивает список своих черновиков (`GET /api/v1/stories`).
    *   Пользователь запрашивает детали конкретного черновика (`GET /api/v1/stories/:id`).
    *   Если статус `complete`, `GameplayHandler.getStoryConfig` парсит JSON из поля `config` и возвращает детальную информацию.
    *   (Опционально) Пользователь инициирует ревизию черновика (`POST /api/v1/stories/:id/revise` с новым промптом). Процесс аналогичен шагу 1 (статус меняется на `revising`, отправляется задача, результат обновляет `config`, статус -> `complete`).
3.  **Публикация:**
    *   Пользователь нажимает "Опубликовать" для готового черновика (`POST /api/v1/stories/:id/publish`).
    *   `GameplayHandler.publishStoryDraft` вызывает `PublishingService.PublishDraft`.
    *   `PublishingService.PublishDraft` в транзакции:
        *   Проверяет статус черновика (`complete`).
        *   Создает запись `PublishedStory` на основе данных из `StoryConfig` (статус `generating`).
        *   Отправляет задачу на генерацию *начальной сцены* истории в очередь `generation_tasks` (с publishedStoryID, setup_json из черновика).
    *   Пользователь получает `202 Accepted` с ID опубликованной истории (`published_story_id`).
    *   `story-generator` генерирует первую сцену.
    *   `story-generator` отправляет результат (первую сцену) в `internal_updates`.
    *   `NotificationConsumer` получает сцену, сохраняет ее в `story_scenes`, обновляет статус `PublishedStory` на `ready`.
    *   (Опционально) Отправляется уведомление клиенту.

## 2. Прохождение опубликованной истории

1.  **Поиск и выбор истории:**
    *   Пользователь просматривает списки историй (`GET /api/v1/published-stories/me`, `GET /api/v1/published-stories/public`).
    *   Пользователь выбирает историю и просматривает ее детали (`GET /api/v1/published-stories/:story_id`).
2.  **Начало игры (Создание сохранения):**
    *   Пользователь нажимает "Начать игру" (`POST /api/v1/published-stories/:story_id/gamestates`).
    *   `GameplayHandler.createPlayerGameState` вызывает `GameLoopService.CreateNewGameState`.
    *   Сервис создает запись `PlayerGameState` (со ссылкой на `published_story_id` и `user_id`, статус `generating_scene`) и запись `PlayerProgress` (с начальными статами из конфига истории).
    *   Сервис проверяет, существует ли *уже* сгенерированная первая сцена для этой истории.
        *   **Если да:** Статус `PlayerGameState` меняется на `ready`.
        *   **Если нет (или статус истории еще не `ready`):** Генерация первой сцены была инициирована при публикации (см. шаг 1.3). Пользователь может получить ошибку `ErrStoryNotReadyYet` или просто будет ждать.
    *   Пользователь получает ответ `201 Created` (или `202 Accepted`) с ID нового `game_state_id`.
3.  **Получение сцены:**
    *   Пользователь (клиент) запрашивает текущую сцену для своего сохранения (`GET /api/v1/published-stories/:story_id/gamestates/:game_state_id/scene`).
    *   `GameplayHandler.getPublishedStoryScene` вызывает `GameLoopService.GetStoryScene`.
    *   `GameLoopService.GetStoryScene`:
        *   Находит `PlayerGameState` по `game_state_id`.
        *   Определяет ID текущей сцены (`current_scene_id`) из `PlayerGameState`.
        *   Пытается загрузить сцену из `story_scenes` по `current_scene_id`.
        *   **Если сцена найдена и готова:**
            *   Загружает `PlayerProgress`.
            *   Парсит JSON контент сцены (`scene.Content`).
            *   Формирует ответ `GameSceneResponseDTO` (с текстом, картинками, вариантами выбора, текущими статами из `PlayerProgress`).
            *   Возвращает ответ пользователю (`200 OK`).
        *   **Если сцена не найдена или еще генерируется (статус `generating`):**
            *   Возвращает ошибку `ErrSceneNeedsGeneration` или статус, указывающий на ожидание (`202 Accepted` или кастомный код).
            *   *(Неявно)* Если `current_scene_id` в `PlayerGameState` еще не задан (самое начало) или сцена не найдена, сервис *должен был* инициировать ее генерацию ранее (при создании `GameState` или после выбора).
4.  **Выбор варианта:**
    *   Пользователь выбирает один из вариантов ответа (`POST /api/v1/published-stories/:story_id/gamestates/:game_state_id/choice`).
    *   `GameplayHandler.makeChoice` вызывает `GameLoopService.MakeChoice`.
    *   `GameLoopService.MakeChoice` в транзакции:
        *   Находит `PlayerGameState` и `PlayerProgress`.
        *   Находит текущую сцену (`current_scene_id`).
        *   Валидирует выбор пользователя.
        *   Применяет последствия выбора к `PlayerProgress` (обновляет статы).
        *   Определяет ID следующей сцены (`next_scene_id`) на основе выбора.
        *   Обновляет `PlayerGameState`, устанавливая `current_scene_id = next_scene_id` и статус `generating_scene`.
        *   Отправляет задачу на генерацию *следующей* сцены (`next_scene_id`) в `generation_tasks` (передавая историю прогресса, ID истории, ID пользователя, ID новой сцены).
    *   Пользователь получает `202 Accepted`.
    *   `story-generator` генерирует новую сцену.
    *   `NotificationConsumer` сохраняет сцену и обновляет статус `PlayerGameState` на `ready`.
    *   Клиент снова запрашивает сцену (шаг 2.3), и теперь получает новую сгенерированную сцену.
5.  **Завершение игры:** Игра продолжается (шаги 2.3 - 2.4), пока не будет достигнута сцена без вариантов выбора (конец) или статы игрока не достигнут условия Game Over (проверяется при применении последствий выбора).

## 3. Другие взаимодействия

*   **Лайки:** Пользователь может лайкать/дизлайкать истории (`POST/DELETE /api/v1/published-stories/:story_id/like`).
*   **Удаление:** Пользователь может удалять свои черновики (`DELETE /api/v1/stories/:id`), опубликованные истории (`DELETE /api/v1/published-stories/:story_id`) и сохранения (`DELETE /api/v1/published-stories/:story_id/gamestates/:game_state_id`).
*   **Видимость:** Автор может менять видимость своей опубликованной истории (`PATCH /api/v1/published-stories/:story_id/visibility`).
*   **Повторная генерация:** Пользователь (или система) может инициировать повторную генерацию для черновика (`POST /api/v1/stories/drafts/:draft_id/retry`), опубликованной истории (`POST /api/v1/published-stories/:story_id/retry`) или конкретного состояния игры (`POST /api/v1/published-stories/:story_id/gamestates/:game_state_id/retry`), если предыдущая попытка завершилась ошибкой. 