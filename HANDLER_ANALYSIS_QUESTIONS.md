# Анализ логики хендлеров в gameplay-service

## Обзор архитектуры

### Найденные файлы хендлеров:
1. `handle_narrator.go` - обработка генерации конфигурации истории (draft → generating → draft)
2. `handle_moderation.go` - модерация контента (moderation_pending → protagonist_goal_pending)
3. `handle_protagonist_goal.go` - определение цели протагониста (protagonist_goal_pending → scene_planner_pending)
4. `handle_scene_planner.go` - планирование сцены (scene_planner_pending → sub_tasks_pending/image_generation_pending/setup_pending)
5. `handle_character_generation.go` - генерация персонажей (sub_tasks_pending → image_generation_pending)
6. `handle_setup.go` - генерация setup и первой сцены (setup_pending → image_generation_pending/json_generation_pending)
7. `handle_json_generation.go` - структурирование сцен в JSON (generating_scene → playing для игрока или Ready для истории)
8. `handle_scene.go` - генерация сцен (обрабатывает StoryContinuation и GameOver)
9. `processor.go` - обработка изображений (handleImageNotification)

### Последовательность генерации:

```
1. handleNarratorNotification: draft → generating → draft
2. handleContentModerationResult: moderation_pending → protagonist_goal_pending  
3. handleProtagonistGoalResult: protagonist_goal_pending → scene_planner_pending
4. handleScenePlannerResult: scene_planner_pending → sub_tasks_pending/image_generation_pending/setup_pending
5. handleCharacterGenerationResult: sub_tasks_pending → image_generation_pending
6. handleNovelSetupNotification: setup_pending → image_generation_pending/json_generation_pending
7. handleImageNotification: декрементирует счетчики изображений
8. handleJsonGenerationResult: generating_scene → playing (для игрока) или Ready (для истории)
```

### Две ветки логики:

**Первая сцена (StateHash = "initial"):**
- Обрабатывается в контексте PublishedStory
- Переводит историю в статус Ready
- Обновляет все PlayerGameState, ожидающие начальную сцену

**Последующие сцены (StateHash != "initial"):**
- Обрабатывается в контексте PlayerGameState
- Переводит игрока в статус Playing
- Работает с конкретным GameStateID

## Критические проблемы

### ПРОБЛЕМА 1: Race condition в handleCharacterGenerationResult
**Файл:** `handle_character_generation.go`, строки 280-295

```go
// Получаем актуальное состояние счетчиков и статуса (могло измениться)
postCommitStory, errGetPost := p.publishedRepo.GetByID(context.Background(), p.db, publishedStoryID)
if errGetPost != nil {
    log.Error("Failed to get story state after character generation commit for step update", zap.Error(errGetPost))
    // Не фатально, но следующий шаг не будет установлен
} else {
    // Определяем следующий шаг на основе СВЕЖИХ данных
    var finalNextStep *sharedModels.InternalGenerationStep
    if postCommitStory.PendingCardImgTasks > 0 {
        step := sharedModels.StepCardImageGeneration
        finalNextStep = &step
    } else if postCommitStory.PendingCharImgTasks > 0 {
        step := sharedModels.StepCharacterImageGeneration
        finalNextStep = &step
    }
    
    // ПРОБЛЕМА: Между получением свежего состояния и обновлением шага
    // может произойти другое обновление, делающее данные устаревшими
    if errUpdateStep := p.publishedRepo.UpdateStatusFlagsAndDetails(updateStepCtx, p.db,
        publishedStoryID,
        postCommitStory.Status, // Используем устаревшие данные
        // ...
    ); errUpdateStep != nil {
        // ...
    }
}
```

**Вопрос:** Нужно ли использовать SELECT FOR UPDATE или атомарные операции для обновления шага?

### ПРОБЛЕМА 2: Inconsistent transaction usage
**Различные паттерны в разных хендлерах:**

1. **handle_narrator.go** - `tx.Begin()` + defer с ручным rollback/commit
2. **handle_setup.go** - `tx.Begin()` + defer с ручным rollback/commit  
3. **handle_character_generation.go** - `withTransaction()` helper
4. **handle_moderation.go** - прямые вызовы без транзакций

**Вопрос:** Какой паттерн транзакций должен быть стандартным?

### ПРОБЛЕМА 3: Дублирование логики определения InternalGenerationStep

**В handle_scene_planner.go:**
```go
var nextInternalStep sharedModels.InternalGenerationStep
var areImagesPendingFlag bool
if pendingCharGenTasksFlag {
    nextInternalStep = sharedModels.StepCharacterGeneration
    areImagesPendingFlag = (pendingCardImgTasksCount > 0)
} else if pendingCardImgTasksCount > 0 {
    nextInternalStep = sharedModels.StepCardImageGeneration
    areImagesPendingFlag = true
} else {
    nextInternalStep = sharedModels.StepSetupGeneration
    areImagesPendingFlag = false
}
```

**В handle_character_generation.go:**
```go
var finalNextStep *sharedModels.InternalGenerationStep
if postCommitStory.PendingCardImgTasks > 0 {
    step := sharedModels.StepCardImageGeneration
    finalNextStep = &step
} else if postCommitStory.PendingCharImgTasks > 0 {
    step := sharedModels.StepCharacterImageGeneration
    finalNextStep = &step
} else if postCommitStory.PendingCharGenTasks == 0 {
    step := sharedModels.StepSetupGeneration
    finalNextStep = &step
}
```

**Вопрос:** Стоит ли вынести эту логику в отдельную функцию?

### ПРОБЛЕМА 4: Неконсистентная обработка ошибок

**handle_json_generation.go** использует разные функции для разных типов ошибок:
```go
if notification.StateHash == sharedModels.InitialStateHash {
    p.handleStoryError(ctx, publishedStoryID, notification.UserID, errDetails, constants.WSEventStoryError)
} else {
    p.handleGameStateError(ctx, gameStateID, notification.UserID, errDetails)
}
```

**Но в других местах используется только handleStoryError**

**Вопрос:** Какая логика должна определять выбор между handleStoryError и handleGameStateError?

### ПРОБЛЕМА 5: Потенциальная утечка данных в handleNovelSetupNotification

**Файл:** `handle_setup.go`, строки 408-444

```go
func (p *NotificationProcessor) dispatchTasksAfterSetupCommit(storyID uuid.UUID, setupResult SetupPromptResult) {
    // Создаем новый контекст для вызова сервиса, т.к. транзакционный контекст завершен
    dispatchCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    // После setup создаём задачу на генерацию JSON для первой сцены
    freshStory, errGet := p.publishedRepo.GetByID(dispatchCtx, p.db, storyID)
    if errGet != nil {
        log.Error("Failed to get story for JSON generation task after setup", zap.Error(errGet))
    } else {
        // ПРОБЛЕМА: setupResult содержит данные из транзакции, 
        // но freshStory получена после коммита - могут быть несоответствия
        jsonPayload := sharedMessaging.GenerationTaskPayload{
            UserInput:        setupResult.Result, // Данные из транзакции
            PublishedStoryID: storyID.String(),
            // ... другие поля из freshStory
        }
    }
}
```

**Вопрос:** Безопасно ли использовать setupResult.Result после коммита транзакции?

### ПРОБЛЕМА 6: Неправильное использование newStatus в handleScenePlannerResult

**Файл:** `handle_scene_planner.go`, строки 190-200

```go
if errCommit := tx.Commit(ctx); errCommit != nil {
    // ... обработка ошибки
}

// После коммита, если статус SetupPending, публикуем задачу генерации Setup
if newStatus == sharedModels.StatusSetupPending {
    // ПРОБЛЕМА: используем newStatus вместо актуального статуса из БД
    // после коммита статус мог измениться
}
```

**Вопрос:** Нужно ли перечитывать статус из БД после коммита?

### ПРОБЛЕМА 7: Система флагов и счетчиков

**Найденные флаги:**
- `PendingCharGenTasks` (int) - количество задач генерации персонажей
- `PendingCardImgTasks` (int) - количество задач генерации изображений карт  
- `PendingCharImgTasks` (int) - количество задач генерации изображений персонажей
- `AreImagesPending` (bool) - общий флаг ожидания изображений
- `IsFirstScenePending` (bool) - флаг ожидания первой сцены

**В handleImageNotification (processor.go, строки 500-600):**
```go
switch notification.PromptType {
case sharedModels.PromptTypeCardImage:
    opDecCardImg = 1
case sharedModels.PromptTypeCharacterImage:
    opDecCharImg = 1
case sharedModels.PromptTypeStoryPreviewImage:
    opDecCardImg = 1 // Превью обрабатывается как "card-like"
}

go p.checkStoryReadinessAfterImage(context.Background(), publishedStoryID, opDecCardImg, opDecCharImg)
```

**Вопрос:** Корректно ли обрабатываются все типы изображений и правильно ли декрементируются счетчики?

### ПРОБЛЕМА 8: Локальная переменная vs актуальное состояние

**В handle_character_generation.go, строки 214-215:**
```go
// Обновляем флаг задачи генерации персонажей
publishedStory.PendingCharGenTasks = 0
publishedStory.PendingCharImgTasks += len(chars)
```

**Затем в транзакции используются эти локальные значения, но потом:**
```go
// Получаем актуальное состояние счетчиков и статуса (могло измениться)
postCommitStory, errGetPost := p.publishedRepo.GetByID(context.Background(), p.db, publishedStoryID)
```

**Вопрос:** Не создает ли это race condition между локальными изменениями и актуальным состоянием БД?

## Вопросы для уточнения

### 1. Архитектурные вопросы:
- Должны ли все хендлеры использовать одинаковый паттерн транзакций?
- Нужна ли централизованная логика определения следующего шага генерации?
- Стоит ли вынести обработку ошибок в отдельный сервис?

### 2. Безопасность данных:
- Как обеспечить консистентность при параллельной обработке уведомлений?
- Нужны ли дополнительные блокировки для критических секций?
- Как избежать race conditions при обновлении счетчиков?

### 3. Обработка ошибок:
- Какая стратегия retry должна использоваться при ошибках БД?
- Как обрабатывать частичные сбои (например, когда задача опубликована, но статус не обновлен)?
- Нужен ли механизм компенсации для откатов?

### 4. Производительность:
- Оптимальны ли текущие таймауты для операций БД?
- Нужно ли кэширование для часто читаемых данных?
- Стоит ли использовать batch операции для обновления множественных записей?

### 5. Мониторинг:
- Какие метрики нужны для отслеживания состояния генерации?
- Как отслеживать "зависшие" истории в промежуточных статусах?
- Нужны ли алерты на критические ошибки в хендлерах?

## Рекомендации по улучшению

### 1. Унификация транзакций
Использовать единый паттерн `withTransaction()` во всех хендлерах.

### 2. Централизация логики шагов
Создать функцию `determineNextGenerationStep(story *PublishedStory) InternalGenerationStep`.

### 3. Атомарные операции
Использовать SELECT FOR UPDATE для критических обновлений счетчиков.

### 4. Валидация состояний
Добавить проверки валидности переходов между статусами.

### 5. Улучшенное логирование
Добавить структурированные логи с correlation ID для трассировки. 