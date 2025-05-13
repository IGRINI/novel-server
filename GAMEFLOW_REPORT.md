# Отчет о геймфлоу

## 1. Генерация черновика (Draft)
- Пользователь вызывает `DraftService.GenerateInitialStory(initialPrompt, language)`.
- Создаётся запись `StoryConfig` со статусом `StatusGenerating`.
- Публикация задачи `PromptTypeNarrator` через `TaskPublisher`.
- В `handleNarratorNotification`:
  - Валидация JSON в `sharedModels.Config`.
  - Сохранение полей `Config`, `Title`, `Description` в `DraftService`.
  - Смена статуса на `StatusDraft`.
  - Отправка клиентского обновления (WebSocket/RabbitMQ) и push-уведомления.
  - При ошибке — установка `StatusError` и уведомление.

## 2. Публикация и модерация
- В `PublishingService.PublishDraft`:
  - Создание `PublishedStory` со статусом `StatusModerationPending`.
  - Публикация задачи `PromptTypeContentModeration`.
- В `handleContentModerationResult`:
  - Проверка статуса `StatusModerationPending`.
  - При неуспехе — `StatusError`, детали ошибки сохраняются, уведомления.
  - При успехе — установка флага `IsAdultContent`, смена на `StatusProtagonistGoalPending`, публикация `PromptTypeProtagonistGoal`.

## 3. Установка цели протагониста
- В `handleProtagonistGoalResult`:
  - Проверка статуса `StatusProtagonistGoalPending`.
  - Unmarshal JSON в `protagonistGoalResultPayload`.
  - Добавление поля `protagonist_goal` в начальный `setup` истории.
  - Смена статуса на `StatusScenePlannerPending`.
  - Публикация задачи `PromptTypeScenePlanner`.
  - При ошибке разбора — fallback-парсинг или обновление в `StatusError`.

## 4. Планировщик сцены (ScenePlanner)
- В `handleScenePlannerResult`:
  - Проверка `StatusScenePlannerPending`.
  - Unmarshal JSON в `InitialScenePlannerOutcome`:
    - `need_new_character`, `new_character_suggestions`
    - `new_card_suggestions`
    - `character_updates`
    - `characters_to_remove`
    - `cards_to_remove`
    - `scene_focus`
  - Применение удаления NPC и карточек к текущему `setup`.
  - Сохранение полного плана (`full_initial_scene_plan`) и счетчиков подзадач (`PendingCharGenTasks`, `PendingCardImgTasks`) одной транзакцией.
  - Если подзадач нет — публикация `PromptTypeStorySetup`, иначе — статус `StatusSubTasksPending`.

## 5. Генерация персонажей и изображений
- В `handleCharacterGenerationResult`:
  - Получение `GeneratedCharacter`, Unmarshal.
  - Idempotent-обновление списка `characters` в `scene_content`.
  - Обновление счетчиков: декремент `PendingCharGenTasks`, инкремент `PendingCharImgTasks`.
  - Публикация задач `PromptTypeImageGeneration`.
- В `handleImageNotification`:
  - Сохранение URL через `imageReferenceRepo.SaveOrUpdateImageReference`.
  - Проверка готовности всех изображений, снятие флага `AreImagesPending`.
  - При готовности — публикация первой сцены.

## 6. Генерация настроек (StorySetup)
- Задача `PromptTypeStorySetup` отправляется сразу из ScenePlanner или после подзадач.
- В `handleNovelSetupNotification`:
  - Проверка `StatusSetupPending`.
  - Unmarshal в `NovelSetupContent`, валидация.
  - Определение потребности в изображениях (персонажи, превью).
  - Установка `StatusFirstScenePending` и флага `AreImagesPending`.
  - Публикация клиентского обновления и push.

## 7. Первая сцена (Initial Scene)
- `publishFirstSceneTaskInternal` формирует `PromptTypeStoryContinuation` для `InitialStateHash`.
- В `handleInitialSceneGenerated`:
  - Проверка `InitialStateHash`.
  - Upsert начальной `StoryScene`.
  - Обновление всех `PlayerGameState` (статус `GeneratingScene` → `Playing`, `CurrentSceneID`).
  - Транзакционное обновление `PublishedStory` (status, flags).
  - При отсутствии флагов ожидания — смена на `StatusReady`.
  - Отправка WS и push-уведомлений.

## 8. Продолжение истории (Game Loop)
- При выборе в `GameLoopService.MakeChoice` — публикация `PromptTypeStoryContinuation` с новым `stateHash`.
- В `handleSceneGenerationNotification`:
  - Валидация JSON (SceneValidation/GameOverValidation).
  - Upsert новой `StoryScene` с включением полей: `config`, `setup`, `scene_planner_output`, `characters`, `cards_to_remove`, `content`.
  - Транзакция: Upsert сцены, обновление `PublishedStory` (status, flags, counters), сохранение `PlayerGameState`.
  - Публикация WS/Push.
  - Публикация `PromptTypeJsonGeneration`.
- В `handleJsonGenerationResult`:
  - Сохранение `structured_scene_content` в `player_progress`.
  - Обновление `PlayerGameState`, WS-уведомление.

## 9. Идемпотентность и надёжность
- Таблица `processed_notifications` хранит `task_id` для фильтрации повторных уведомлений.
- В начале `processNotificationPayloadInternal` — вставка `task_id ON CONFLICT DO NOTHING`.
- При повторе — выход без побочных эффектов (Ack).
- Встроенный retry в `publishMessage` (3 попытки), Nack при ошибках публикации/транзакций.

## 10. Хранение данных
- `story_scenes.scene_content` (JSONB) хранит все метаданные и контент сцен.
- `image_references` хранит URL сгенерированных изображений.
- `published_stories.setup` используется только при старте; далее читается, но не изменяется.

---

# Основные ошибки и риски

1. Несогласованность схем JSON и структур моделей (Unmarshal может падать).
2. Отсутствие централизованных fallback-механизмов при изменении API промптов.
3. Глубокая вложенность логики усложняет поддержку и юнит-тесты.
4. Риск дедлоков из-за advisory locks + транзакция + параллельная обработка.
5. Возможные гонки при обновлении флагов в разных хендлерах.
6. Переполнение таблицы `processed_notifications` без политики очистки.
7. Отсутствие версионирования JSON схемы `scene_content` при эволюции данных.
8. Ограниченный мониторинг и метрики — затрудняют диагностику.
9. Нет хранения истории удалённых NPC/карточек вне одной сцены.
10. Сложность миграций при переносе части логики из `setup` в `scene_content`.
11. Потеря контекста при частичных ошибках обработки — требуется транзакционная компенсация.
12. Отсутствие SLA и таймаутов на уровне сервисов — возможны зависания.
