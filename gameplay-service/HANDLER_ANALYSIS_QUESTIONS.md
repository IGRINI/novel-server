# Анализ логики хендлеров - Вопросы и ответы

## Общая архитектура

### Вопрос 1: Какая общая последовательность генерации истории?
**Ответ:** Судя по коду:
1. `handleNarratorNotification` - генерация конфигурации истории (draft -> generating -> draft)
2. `handleContentModerationResult` - модерация контента (moderation_pending -> protagonist_goal_pending)
3. `handleProtagonistGoalResult` - определение цели протагониста (protagonist_goal_pending -> scene_planner_pending)
4. `handleScenePlannerResult` - планирование сцены (scene_planner_pending -> sub_tasks_pending/image_generation_pending/setup_pending)
5. `handleCharacterGenerationResult` - генерация персонажей (sub_tasks_pending -> image_generation_pending)
6. `handleNovelSetupNotification` - генерация setup и первой сцены (setup_pending -> image_generation_pending/json_generation_pending)
7. `handleJsonGenerationResult` - структурирование сцены в JSON (generating_scene -> playing для игрока, или готовность истории)

### Вопрос 2: Какие есть две ветки логики - для первой сцены и последующих?
**Ответ:** 
- **Первая сцена (initial scene)**: StateHash = "initial", обрабатывается в контексте PublishedStory, переводит историю в статус Ready
- **Последующие сцены**: StateHash != "initial", обрабатывается в контексте PlayerGameState, переводит игрока в статус Playing

### Вопрос 3: Как работает система флагов PendingCharGenTasks, PendingCardImgTasks, PendingCharImgTasks?
**Ответ:** Судя по коду:
- `PendingCharGenTasks` - количество ожидающих задач генерации персонажей (0/1 как bool)
- `PendingCardImgTasks` - количество ожидающих задач генерации изображений карт
- `PendingCharImgTasks` - количество ожидающих задач генерации изображений персонажей

## Специфические вопросы по хендлерам

### Вопрос 4: В handleCharacterGenerationResult - почему проверяется PendingCharGenTasks == 0 как ошибка?
**Ответ:** Потому что если мы получили результат генерации персонажей, то флаг должен быть > 0, иначе это неожиданное состояние.

### Вопрос 5: В handleJsonGenerationResult - как определяется что делать с результатом?
**Ответ:** По StateHash:
- Если StateHash == "initial" -> обновляется PublishedStory, статус -> Ready
- Если StateHash != "initial" -> обновляется PlayerGameState, статус -> Playing

### Вопрос 6: В handleNovelSetupNotification - почему initial_narrative сохраняется в StoryScene, а не в Setup?
**Ответ:** Потому что initial_narrative - это контент сцены, который потом будет структурирован в JSON через handleJsonGenerationResult.

### Вопрос 7: Как работает система InternalGenerationStep?
**Ответ:** Это внутренний шаг генерации, который показывает на каком этапе находится процесс:
- StepProtagonistGoal
- StepScenePlanner  
- StepCharacterGeneration
- StepCardImageGeneration
- StepCharacterImageGeneration
- StepSetupGeneration
- StepCoverImageGeneration
- StepInitialSceneJSON
- StepComplete

### Вопрос 8: В handleSceneGenerationNotification - почему запускается JsonGeneration?
**Ответ:** Потому что сначала генерируется текст сцены, а потом он структурируется в JSON формат для клиента.

### Вопрос 9: Что происходит если задача image generation fails?
**Ответ:** Судя по коду, обработчики image generation отсутствуют в предоставленных файлах. Это потенциальная проблема.

### Вопрос 10: Как обрабатываются retry scenarios?
**Ответ:** В `ensureStoryStatus` есть логика для retry - если статус "generating" и InternalGenerationStep соответствует ожидаемому шагу, то обработка продолжается.

## НАЙДЕННЫЕ ПРОБЛЕМЫ

### КРИТИЧЕСКАЯ ПРОБЛЕМА 1: Отсутствуют обработчики image generation
В коде нет обработчиков для результатов генерации изображений (character images, card images, cover images). Это означает что:
- Флаги `PendingCardImgTasks`, `PendingCharImgTasks` никогда не сбрасываются
- Статус `StatusImageGenerationPending` никогда не меняется
- Процесс генерации зависает на этапе изображений

### КРИТИЧЕСКАЯ ПРОБЛЕМА 2: Нарушение последовательности в handleCharacterGenerationResult
В строке 214-215 файла `handle_character_generation.go`:
```go
publishedStory.PendingCharGenTasks = 0
publishedStory.PendingCharImgTasks += len(chars)
```
Но потом в строке 235 используется `publishedStory.PendingCharImgTasks` для обновления БД, хотя эта переменная может быть устаревшей.

### ПРОБЛЕМА 3: Race condition в handleCharacterGenerationResult
В строках 280-295 получается свежее состояние истории, но обновление шага происходит отдельным запросом. Между этими операциями состояние может измениться.

### ПРОБЛЕМА 4: Inconsistent transaction usage
- `handleCharacterGenerationResult` использует `withTransaction`
- `handleScenePlannerResult` использует `tx.Begin()` и defer
- `handleNovelSetupNotification` использует `tx.Begin()` и defer
- Остальные хендлеры не используют транзакции

### ПРОБЛЕМА 5: Дублирование логики определения следующего шага
В нескольких хендлерах есть похожая логика определения `InternalGenerationStep`, но она не централизована.

### ПРОБЛЕМА 6: Неконсистентная обработка ошибок
- `handleStoryError` vs `handleGameStateError` используются непоследовательно
- Некоторые хендлеры возвращают ошибки для NACK, другие всегда возвращают nil

### ПРОБЛЕМА 7: Потенциальная проблема с Setup в handleNovelSetupNotification
В строке 380+ сохраняется `initial_narrative` в StoryScene, но это происходит ВНУТРИ транзакции, которая может откатиться. Если транзакция откатится, то `dispatchTasksAfterSetupCommit` все равно выполнится с устаревшими данными.

### ПРОБЛЕМА 8: Неправильная логика в handleScenePlannerResult
В строке 297 вызывается `p.notifyClient`, но используется `newStatus` вместо актуального статуса из БД после коммита.

## ПРЕДЛОЖЕНИЯ ПО УЛУЧШЕНИЮ

### 1. Создать централизованную функцию для определения следующего шага
```go
func determineNextStep(story *models.PublishedStory) *models.InternalGenerationStep {
    // Централизованная логика
}
```

### 2. Унифицировать использование транзакций
Все хендлеры должны использовать одинаковый паттерн транзакций.

### 3. Создать обработчики для image generation
Необходимо добавить:
- `handleCharacterImageResult`
- `handleCardImageResult` 
- `handleCoverImageResult`

### 4. Централизовать обработку ошибок
Создать единую функцию для обработки ошибок с правильным выбором между `handleStoryError` и `handleGameStateError`.

### 5. Добавить валидацию состояний
Перед каждым обновлением проверять что текущее состояние позволяет выполнить операцию.

## Вопросы требующие уточнения

### Вопрос 11: Что происходит если PublishedStory удаляется во время генерации?
**Требует уточнения** я сам не знаю. Думаю нужно запретить удалять историю во время генерации. Но тогда будет проблема если другие игроки в данный момент играют в эту историю. Думаю нужно поменять логику удаления истории. Если игрок удаляет историю, то она просто помечается как удаленная и перестает отображаться в любых списках. А если какие то игроки уже имеют прогресс этой истории, то они смогут его продолжить или перезапустить.

### Вопрос 12: Как работает система приоритетов задач?
**Требует уточнения** Что именно под приоритетом подразумевается? Там вроде все задачи друг за другом идут, кроме генерации изображений. Они параллельно идут

### Вопрос 13: Должны ли image generation задачи обрабатываться параллельно или последовательно?
**Требует уточнения** Параллельно. Их обрабатывают воркеры и могут параллельно генерировать изображения

### Вопрос 14: Что делать если часть image generation задач fails, а часть succeeds?
**Требует уточнения** История должна падать с ошибкой, а при ретрае задачи добавляются только fails, succeeds заново не отправляются