package worker

import (
	// Для рендеринга шаблона
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/rand"
	sharedInterfaces "novel-server/shared/interfaces" // <<< Используем этот импорт
	"novel-server/shared/messaging"
	sharedModels "novel-server/shared/models" // <<< Добавляем импорт models

	// Импортируем конфиг для AIMaxAttempts
	// Добавляем модель
	// "novel-server/story-generator/internal/repository" // <<< Удаляем старый импорт
	"novel-server/story-generator/internal/service" // Для шаблонизации промтов
	"strings"                                       // <<< ДОБАВЛЕНО: для strings.Replace >>>
	"time"

	// "novel-server/story-generator/internal/model" // <<< Удаляем импорт model
	"github.com/google/uuid"
)

// Ключ для системного промпта
const (
	systemPromptKey = "system_prompt" // Ключ, по которому ищем системный промпт в базе
)

// TaskHandler обрабатывает задачи генерации
type TaskHandler struct {
	// <<< УДАЛЕНО: cfg *config.Config >>>
	maxAttempts    int           // <<< ДОБАВЛЕНО
	baseRetryDelay time.Duration // <<< ДОБАВЛЕНО
	aiTimeout      time.Duration // <<< ДОБАВЛЕНО
	aiClient       service.AIClient
	resultRepo     sharedInterfaces.GenerationResultRepository
	sceneRepo      sharedInterfaces.StorySceneRepository // <<< ДОБАВЛЕНО: Репозиторий для сцен >>>
	notifier       service.Notifier
	prompts        *service.PromptProvider
	// configProvider *service.ConfigProvider // <<< УДАЛЕНО
}

// NewTaskHandler создает новый экземпляр обработчика задач
func NewTaskHandler(
	// <<< ПРИНИМАЕМ ПАРАМЕТРЫ ВМЕСТО КОНФИГА >>>
	maxAttempts int,
	baseRetryDelay time.Duration,
	aiTimeout time.Duration,
	aiClient service.AIClient,
	resultRepo sharedInterfaces.GenerationResultRepository,
	sceneRepo sharedInterfaces.StorySceneRepository, // <<< ДОБАВЛЕНО: Принимаем репозиторий сцен >>>
	notifier service.Notifier,
	promptProvider *service.PromptProvider,
	// configProvider *service.ConfigProvider, // <<< УДАЛЕНО
) *TaskHandler {
	return &TaskHandler{
		// <<< СОХРАНЯЕМ ПАРАМЕТРЫ >>>
		maxAttempts:    maxAttempts,
		baseRetryDelay: baseRetryDelay,
		aiTimeout:      aiTimeout,
		aiClient:       aiClient,
		resultRepo:     resultRepo,
		sceneRepo:      sceneRepo, // <<< ДОБАВЛЕНО: Сохраняем репозиторий сцен >>>
		notifier:       notifier,
		prompts:        promptProvider,
		// configProvider: configProvider, // <<< УДАЛЕНО
	}
}

// Handle обрабатывает одну задачу генерации
func (h *TaskHandler) Handle(payload messaging.GenerationTaskPayload) (err error) {
	MetricsIncrementTasksReceived() // Увеличиваем счетчик полученных задач
	taskStartTime := time.Now()     // Замеряем время начала обработки задачи
	log.Printf("[TaskID: %s] Обработка задачи: UserID=%s, PromptType=%s, Language=%s",
		payload.TaskID, payload.UserID, payload.PromptType, payload.Language)

	// Переменная для статуса задачи (для финальной метрики длительности)
	taskStatus := "success" // По умолчанию успех

	// Объявляем переменные для результатов и ошибок ЗАРАНЕЕ
	var aiResponse string
	var processingErr error
	var completedAt time.Time
	var finalUsageInfo service.UsageInfo
	var systemPrompt string
	var language string       // <<< Перенесено сюда
	var promptKey string      // <<< Перенесено сюда
	var promptText string     // <<< Перенесено сюда
	var userInputForAI string // <<< Перенесено сюда

	// Defer для записи общей длительности задачи и отправки метрик
	defer func() {
		duration := time.Since(taskStartTime)
		MetricsRecordTaskProcessingDuration(duration) // Записываем общую длительность
		if err != nil {
			// Если была ошибка на любом этапе, записываем метрику ошибки
			// Тип ошибки уже должен быть записан ранее (prompt_preparation, user_input_empty, ai_error, save_error)
			taskStatus = "failed" // Меняем статус для лога
		}
		// Принудительно отправляем все собранные метрики для этой задачи
		if pushErr := PushMetricsNow(); pushErr != nil {
			log.Printf("[TaskID: %s][WARN] Не удалось принудительно отправить метрики в конце задачи: %v", payload.TaskID, pushErr)
		}
		log.Printf("[TaskID: %s] Завершение обработки задачи. Статус: %s. Общее время: %v.", payload.TaskID, taskStatus, duration)
	}()

	// --- Этап 1: Получение системного промпта (с учетом языка из payload) ---
	systemPromptLang := payload.Language // Используем язык из payload
	if systemPromptLang == "" {
		systemPromptLang = "en" // Запасной язык, если в payload пусто (НУЖНО ЛИ?)
		log.Printf("[TaskID: %s][WARN] Язык в payload пуст, используется запасной язык '%s' для системного промпта.", payload.TaskID, systemPromptLang)
	}
	systemPrompt, err = h.prompts.GetPrompt(context.Background(), systemPromptKey, systemPromptLang)
	if err != nil {
		log.Printf("[TaskID: %s] Критическая ошибка: не удалось получить системный промпт (%s/%s): %v",
			payload.TaskID, systemPromptLang, systemPromptKey, err)
		MetricsIncrementTaskFailed("system_prompt_missing")
		processingErr = fmt.Errorf("failed to get system prompt '%s/%s': %w", systemPromptLang, systemPromptKey, err)
		goto SaveAndNotify // Используем goto для перехода к сохранению ошибки
	}
	if systemPrompt == "" {
		log.Printf("[TaskID: %s] Критическая ошибка: системный промпт (%s/%s) пуст.",
			payload.TaskID, systemPromptLang, systemPromptKey)
		MetricsIncrementTaskFailed("system_prompt_empty")
		processingErr = fmt.Errorf("system prompt '%s/%s' is empty", systemPromptLang, systemPromptKey)
		goto SaveAndNotify // Используем goto для перехода к сохранению ошибки
	}
	log.Printf("[TaskID: %s] Системный промпт '%s/%s' успешно получен.", payload.TaskID, systemPromptLang, systemPromptKey)

	// --- Этап 2: Получение промпта задачи ---
	language = payload.Language // <<< Используем язык из payload >>>
	if language == "" {         // <<< Добавляем fallback и здесь >>>
		language = "en"
		log.Printf("[TaskID: %s][WARN] Язык в payload пуст, используется запасной язык '%s' для промпта задачи.", payload.TaskID, language)
	}
	promptKey = string(payload.PromptType) // <<< Используем PromptType как ключ
	promptText, err = h.prompts.GetPrompt(context.Background(), promptKey, language)
	if err != nil {
		log.Printf("[TaskID: %s] Ошибка получения промпта из кэша: %v. key='%s', lang='%s'", payload.TaskID, err, promptKey, language)
		MetricsIncrementTaskFailed("prompt_cache_miss")
		processingErr = fmt.Errorf("failed to get prompt '%s/%s': %w", language, promptKey, err)
		// Ошибка получения промпта - дальше не идем, сразу сохраняем/уведомляем
	} else {
		// <<< Промпт получен, продолжаем обработку >>>

		// <<< Заменяем плейсхолдер {{USER_INPUT}} >>>
		userInputForAI = strings.Replace(promptText, "{{USER_INPUT}}", payload.UserInput, 1)
		if userInputForAI == promptText && strings.Contains(promptText, "{{USER_INPUT}}") {
			// Если плейсхолдер был, но замена не произошла (например, UserInput пуст)
			log.Printf("[TaskID: %s][WARN] Placeholder {{USER_INPUT}} не был заменен в промпте типа '%s'. Возможно, UserInput пуст.", payload.TaskID, payload.PromptType)
			// Решаем, что делать дальше: либо ошибка, либо использовать промпт как есть
			// Пока оставим как есть, AI получит промпт с незамененным плейсхолдером или без него.
		}
		log.Printf("[TaskID: %s] Final UserInput for AI (length: %d).", payload.TaskID, len(userInputForAI))

		// --- Этап 3: Вызов AI API с ретраями (только если промпт загружен) ---
		if payload.UserInput == "" && payload.PromptType != "" { // TODO: Уточнить, всегда ли нужен UserInput?
			processingErr = fmt.Errorf("ошибка: userInput пуст для PromptType '%s'", payload.PromptType)
			log.Printf("[TaskID: %s] %v", payload.TaskID, processingErr)
			MetricsIncrementTaskFailed("user_input_empty")
			err = fmt.Errorf("ошибка валидации: %w", processingErr)
			// <<< УДАЛЕНО: return err >>>
		} else {
			baseDelay := h.baseRetryDelay

			for attempt := 1; attempt <= h.maxAttempts; attempt++ {
				aiCallStartTime := time.Now()
				log.Printf("[TaskID: %s] Вызов AI API (Попытка %d/%d)...", payload.TaskID, attempt, h.maxAttempts)
				ctx, cancel := context.WithTimeout(context.Background(), h.aiTimeout)

				var attemptUsageInfo service.UsageInfo
				var attemptErr error
				aiResponse, attemptUsageInfo, attemptErr = h.aiClient.GenerateText(ctx,
					payload.UserID,
					systemPrompt, // <<< ИСПОЛЬЗУЕМ ПОЛУЧЕННЫЙ СИСТЕМНЫЙ ПРОМПТ
					userInputForAI,
					service.GenerationParams{Temperature: float64Ptr(0.2)})
				cancel()

				aiCallDuration := time.Since(aiCallStartTime)
				aiStatusLabel := "success"

				if attemptErr == nil {
					log.Printf("[TaskID: %s] AI API успешно ответил (Попытка %d). Время ответа: %v", payload.TaskID, attempt, aiCallDuration)
					log.Printf("[TaskID: %s] Raw AI Response (length: %d): %s", payload.TaskID, len(aiResponse), aiResponse)
					MetricsRecordAIRequest("unknown", aiStatusLabel, aiCallDuration)
					if attemptUsageInfo.TotalTokens > 0 || attemptUsageInfo.EstimatedCostUSD > 0 {
						MetricsRecordAITokens("unknown", float64(attemptUsageInfo.PromptTokens), float64(attemptUsageInfo.CompletionTokens))
						MetricsAddAICost("unknown", attemptUsageInfo.EstimatedCostUSD)
						log.Printf("[TaskID: %s][Attempt %d Metrics] Tokens: P=%d, C=%d. Cost: %.6f USD",
							payload.TaskID, attempt, attemptUsageInfo.PromptTokens, attemptUsageInfo.CompletionTokens, attemptUsageInfo.EstimatedCostUSD)
					}
					finalUsageInfo = attemptUsageInfo
					processingErr = nil
					break
				}

				aiStatusLabel = "error"
				processingErr = attemptErr
				log.Printf("[TaskID: %s] Ошибка вызова AI API (Попытка %d/%d, время: %v): %v", payload.TaskID, attempt, h.maxAttempts, aiCallDuration, processingErr)
				MetricsRecordAIRequest("unknown", aiStatusLabel, aiCallDuration)

				if attempt == h.maxAttempts {
					log.Printf("[TaskID: %s] Достигнуто максимальное количество попыток (%d) вызова AI.", payload.TaskID, h.maxAttempts)
					MetricsIncrementTaskFailed("ai_error")
					// Не устанавливаем err здесь, processingErr уже содержит ошибку
					break // Выходим из цикла ретраев после последней неудачной попытки
				}

				delay := float64(baseDelay) * math.Pow(2, float64(attempt-1))
				jitter := delay * 0.1
				delay += jitter * (rand.Float64()*2 - 1)
				waitDuration := time.Duration(delay)
				if waitDuration < baseDelay {
					waitDuration = baseDelay
				}
				log.Printf("[TaskID: %s] Ожидание %v перед следующей попыткой...", payload.TaskID, waitDuration)
				time.Sleep(waitDuration)
			}
		}
	} // <<< Конец блока else (обработка после успешного получения промпта задачи) >>>

SaveAndNotify: // Метка для перехода при ошибке получения промптов
	// --- Этап N: Сохранение результата и отправка уведомления --- //
	completedAt = time.Now() // Обновляем время завершения перед сохранением
	processingDuration := completedAt.Sub(taskStartTime)
	saveErr := h.saveResultAndNotify(
		context.Background(),
		payload,    // Передаем весь payload
		aiResponse, // Результат AI (может быть пустой при ошибке)
		processingDuration,
		processingErr, // Ошибка обработки (может быть nil)
		finalUsageInfo,
	)

	if saveErr != nil {
		log.Printf("[TaskID: %s] Критическая ошибка при сохранении результата или отправке уведомления: %v", payload.TaskID, saveErr)
		if processingErr == nil { // Если до этого не было ошибки AI/промпта
			// Это новая ошибка, связанная с сохранением/уведомлением
			err = fmt.Errorf("ошибка сохранения/уведомления: %w", saveErr)
			MetricsIncrementTaskFailed("save_notify_error")
		} else {
			// Если уже была ошибка AI/промпта, она приоритетнее
			err = processingErr
		}
		return err // Возвращаем ошибку (либо AI/промпта, либо save/notify)
	}

	// Если сохранение/уведомление прошло успешно, возвращаем ошибку обработки (если она была)
	if processingErr != nil {
		return processingErr
	}

	// Если ошибок не было ни при обработке, ни при сохранении/уведомлении
	return nil
}

// preparePrompt - ЭТА ФУНКЦИЯ БОЛЬШЕ НЕ НУЖНА, ТАК КАК ПРОМПТ БЕРЕТСЯ ИЗ ПРОВАЙДЕРА
/*
func (h *TaskHandler) preparePrompt(taskID string, promptType sharedModels.PromptType) (string, error) {
	promptFilename := model.GetPromptFilename(promptType)
	promptFilePath := filepath.Join(h.cfg.PromptsDir, promptFilename)

	log.Printf("[TaskID: %s] Загрузка инструкций из файла промпта: %s", taskID, promptFilePath)
	promptBytes, err := os.ReadFile(promptFilePath)
	if err != nil {
		return "", fmt.Errorf("ошибка чтения файла промпта %s: %w", promptFilePath, err)
	}
	return string(promptBytes), nil
}
*/

// saveResultAndNotify сохраняет результат генерации и отправляет уведомление
func (h *TaskHandler) saveResultAndNotify(
	ctx context.Context,
	payload messaging.GenerationTaskPayload,
	generatedText string,
	duration time.Duration,
	execErr error,
	usage service.UsageInfo,
) error {
	result := &sharedModels.GenerationResult{
		ID:               payload.TaskID,
		UserID:           payload.UserID,
		PromptType:       payload.PromptType,
		GeneratedText:    generatedText, // Сохраняем полный текст ответа AI в generation_results для отладки
		ProcessingTimeMs: duration.Milliseconds(),
		CreatedAt:        time.Now().UTC().Add(-duration),
		CompletedAt:      time.Now().UTC(),
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		EstimatedCostUSD: usage.EstimatedCostUSD,
	}
	if execErr != nil {
		result.Error = execErr.Error()
	}

	// Всегда сохраняем результат в generation_results (для аудита и статистики)
	saveDbErr := h.resultRepo.Save(ctx, result)
	if saveDbErr != nil {
		log.Printf("[TaskID: %s] КРИТИЧЕСКАЯ ОШИБКА: Не удалось сохранить результат генерации в generation_results: %v; Result: %+v", payload.TaskID, saveDbErr, result)
		// Не прерываем выполнение, попробуем отправить уведомление об ошибке
		if execErr == nil { // Если исходной ошибки не было, то эта ошибка становится основной
			execErr = fmt.Errorf("ошибка сохранения результата генерации: %w", saveDbErr)
		}
	}

	log.Printf("[TaskID: %s] Результат генерации сохранен в generation_results.", payload.TaskID)

	// --- СОХРАНЕНИЕ СЦЕНЫ (только при успехе и для нужных типов) --- START
	notifyStatus := messaging.NotificationStatusSuccess
	errorDetails := ""
	if execErr != nil {
		notifyStatus = messaging.NotificationStatusError
		errorDetails = execErr.Error()
	} else {
		// Ошибки не было, проверяем тип промпта
		isScenePrompt := payload.PromptType == sharedModels.PromptTypeNovelFirstSceneCreator ||
			payload.PromptType == sharedModels.PromptTypeNovelCreator ||
			payload.PromptType == sharedModels.PromptTypeNovelGameOverCreator

		if isScenePrompt {
			log.Printf("[TaskID: %s] Попытка сохранения сгенерированной сцены (PromptType: %s)...", payload.TaskID, payload.PromptType)
			// Парсим PublishedStoryID
			storyUUID, storyParseErr := uuid.Parse(payload.PublishedStoryID)
			if storyParseErr != nil {
				log.Printf("[TaskID: %s] КРИТИЧЕСКАЯ ОШИБКА: Не удалось распарсить PublishedStoryID '%s' для сохранения сцены: %v", payload.TaskID, payload.PublishedStoryID, storyParseErr)
				execErr = fmt.Errorf("invalid PublishedStoryID '%s' for scene saving: %w", payload.PublishedStoryID, storyParseErr)
				notifyStatus = messaging.NotificationStatusError
				errorDetails = execErr.Error()
			} else {
				// Создаем объект сцены
				scene := &sharedModels.StoryScene{
					ID:               uuid.New(),
					PublishedStoryID: storyUUID,
					StateHash:        payload.StateHash,
					Content:          json.RawMessage(generatedText),
					CreatedAt:        time.Now().UTC(),
				}

				// Используем Upsert, т.к. задача может быть перезапущена
				upsertErr := h.sceneRepo.Upsert(ctx, scene)
				if upsertErr != nil {
					log.Printf("[TaskID: %s] КРИТИЧЕСКАЯ ОШИБКА: Не удалось сохранить/обновить сцену (StoryID: %s, StateHash: %s): %v", payload.TaskID, payload.PublishedStoryID, payload.StateHash, upsertErr)
					execErr = fmt.Errorf("ошибка сохранения сцены: %w", upsertErr)
					notifyStatus = messaging.NotificationStatusError
					errorDetails = execErr.Error()
				} else {
					log.Printf("[TaskID: %s] Сцена успешно сохранена/обновлена (StoryID: %s, StateHash: %s).", payload.TaskID, payload.PublishedStoryID, payload.StateHash)
				}
			}
		}
	}
	// --- СОХРАНЕНИЕ СЦЕНЫ --- END

	// Отправка уведомления (статус и ошибка могли измениться при сохранении сцены)
	notifyErr := h.notify(ctx, payload, notifyStatus, errorDetails)
	if notifyErr != nil {
		if execErr == nil && saveDbErr == nil { // Если до этого не было ошибок
			return fmt.Errorf("ошибка отправки уведомления: %w", notifyErr) // Основная ошибка - отправка уведомления
		} else {
			// Если были ошибки до этого (AI, save result, save scene), они важнее.
			// Просто логируем, что уведомление тоже не ушло.
			log.Printf("[TaskID: %s] Ошибка отправки уведомления '%s' после предыдущей ошибки '%s': %v",
				payload.TaskID, notifyStatus, errorDetails, notifyErr)
			// Возвращаем исходную ошибку (execErr приоритетнее saveDbErr)
			if execErr != nil {
				return execErr
			}
			return saveDbErr // execErr был nil, но была ошибка сохранения результата
		}
	}

	if execErr == nil && saveDbErr == nil && notifyStatus == messaging.NotificationStatusSuccess {
		// Инкрементируем успех только если ВСЕ прошло хорошо (включая сохранение сцены)
		MetricsIncrementTaskSucceeded()
		log.Printf("[TaskID: %s] Успешное завершение и уведомление отправлено.", payload.TaskID)
	}

	// Возвращаем ошибку, если она была на каком-либо этапе (AI, save result, save scene)
	if execErr != nil {
		return execErr
	}
	if saveDbErr != nil {
		return saveDbErr // Ошибка сохранения результата (менее приоритетная)
	}

	return nil // Все прошло успешно
}

// notify отправляет уведомление
func (h *TaskHandler) notify(ctx context.Context, payload messaging.GenerationTaskPayload, status messaging.NotificationStatus, errorDetails string) error {
	notification := messaging.NotificationPayload{
		TaskID:           payload.TaskID,
		UserID:           payload.UserID,
		PromptType:       payload.PromptType,
		Status:           status,
		ErrorDetails:     errorDetails,
		StoryConfigID:    payload.StoryConfigID,
		PublishedStoryID: payload.PublishedStoryID,
		StateHash:        payload.StateHash,
		// GameStateID устанавливается в зависимости от типа сцены и наличия его в payload
	}

	// --- Логика установки GameStateID в уведомлении --- START
	// Определяем, относится ли задача к генерации сцены
	isSceneGenerationTask := payload.PromptType == sharedModels.PromptTypeNovelFirstSceneCreator ||
		payload.PromptType == sharedModels.PromptTypeNovelCreator ||
		payload.PromptType == sharedModels.PromptTypeNovelGameOverCreator

	// GameStateID передается в уведомлении, ЕСЛИ:
	// 1. Это НЕ генерация сцены ВОВСЕ.
	// ИЛИ
	// 2. Это генерация сцены, но это НЕ начальная сцена (StateHash != initial) ИЛИ GameStateID БЫЛ в исходном payload.
	if !isSceneGenerationTask || (payload.StateHash != sharedModels.InitialStateHash || payload.GameStateID != "") {
		notification.GameStateID = payload.GameStateID
		log.Printf("[TaskID: %s] GameStateID '%s' будет передан в уведомлении (isScene: %t, isInitialHash: %t, payloadHasGid: %t).",
			payload.TaskID, payload.GameStateID, isSceneGenerationTask, payload.StateHash == sharedModels.InitialStateHash, payload.GameStateID != "")
	} else {
		// Это генерация НАЧАЛЬНОЙ сцены, и GameStateID в payload был пустой.
		// Не передаем GameStateID в уведомлении.
		notification.GameStateID = ""
		log.Printf("[TaskID: %s] Генерация начальной сцены без исходного GameStateID, GameStateID в уведомлении будет пустой.", payload.TaskID)
	}
	// --- Логика установки GameStateID в уведомлении --- END

	if err := h.notifier.Notify(ctx, notification); err != nil {
		log.Printf("[TaskID: %s] Не удалось отправить уведомление (Status: %s, Error: '%s', GID: '%s'): %v",
			payload.TaskID, status, errorDetails, notification.GameStateID, err)
		return err
	}

	if status == messaging.NotificationStatusSuccess {
		log.Printf("[TaskID: %s] Уведомление об успехе отправлено (GID: '%s').", payload.TaskID, notification.GameStateID)
	} else {
		log.Printf("[TaskID: %s] Уведомление об ошибке отправлено (Error: '%s', GID: '%s').", payload.TaskID, errorDetails, notification.GameStateID)
	}
	return nil
}

// float64Ptr возвращает указатель на float64
func float64Ptr(f float64) *float64 {
	return &f
}
