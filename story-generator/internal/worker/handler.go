package worker

import (
	// Для рендеринга шаблона
	"context"
	"fmt"
	"log"
	"math"
	"math/rand"
	sharedInterfaces "novel-server/shared/interfaces" // <<< Используем этот импорт
	"novel-server/shared/messaging"
	sharedModels "novel-server/shared/models"      // <<< Добавляем импорт models
	"novel-server/story-generator/internal/config" // Импортируем конфиг для AIMaxAttempts

	// Добавляем модель
	// "novel-server/story-generator/internal/repository" // <<< Удаляем старый импорт
	"novel-server/story-generator/internal/service" // Для шаблонизации промтов
	"strings"                                       // <<< ДОБАВЛЕНО: для strings.Replace >>>
	"time"
	// "novel-server/story-generator/internal/model" // <<< Удаляем импорт model
)

// Ключ для системного промпта
const (
	systemPromptKey = "system_prompt" // Ключ, по которому ищем системный промпт в базе
)

// TaskHandler обрабатывает задачи генерации
type TaskHandler struct {
	cfg        *config.Config
	aiClient   service.AIClient // <<< Используем интерфейс
	resultRepo sharedInterfaces.GenerationResultRepository
	notifier   service.Notifier
	prompts    *service.PromptProvider // <<< Провайдер для ВСЕХ промптов
	// configProvider *service.ConfigProvider // <<< УДАЛЕНО
}

// NewTaskHandler создает новый экземпляр обработчика задач
func NewTaskHandler(
	cfg *config.Config,
	aiClient service.AIClient, // <<< Принимаем интерфейс
	resultRepo sharedInterfaces.GenerationResultRepository,
	notifier service.Notifier,
	promptProvider *service.PromptProvider, // <<< Принимаем PromptProvider
	// configProvider *service.ConfigProvider, // <<< УДАЛЕНО
) *TaskHandler {
	return &TaskHandler{
		cfg:        cfg,
		aiClient:   aiClient,
		resultRepo: resultRepo,
		notifier:   notifier,
		prompts:    promptProvider, // <<< Сохраняем
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
			baseDelay := h.cfg.AIBaseRetryDelay

			for attempt := 1; attempt <= h.cfg.AIMaxAttempts; attempt++ {
				aiCallStartTime := time.Now()
				log.Printf("[TaskID: %s] Вызов AI API (Попытка %d/%d)...", payload.TaskID, attempt, h.cfg.AIMaxAttempts)
				ctx, cancel := context.WithTimeout(context.Background(), h.cfg.AITimeout)

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
					MetricsRecordAIRequest(h.cfg.AIModel, aiStatusLabel, aiCallDuration)
					if attemptUsageInfo.TotalTokens > 0 || attemptUsageInfo.EstimatedCostUSD > 0 {
						MetricsRecordAITokens(h.cfg.AIModel, float64(attemptUsageInfo.PromptTokens), float64(attemptUsageInfo.CompletionTokens))
						MetricsAddAICost(h.cfg.AIModel, attemptUsageInfo.EstimatedCostUSD)
						log.Printf("[TaskID: %s][Attempt %d Metrics] Tokens: P=%d, C=%d. Cost: %.6f USD",
							payload.TaskID, attempt, attemptUsageInfo.PromptTokens, attemptUsageInfo.CompletionTokens, attemptUsageInfo.EstimatedCostUSD)
					}
					finalUsageInfo = attemptUsageInfo
					processingErr = nil
					break
				}

				aiStatusLabel = "error"
				processingErr = attemptErr
				log.Printf("[TaskID: %s] Ошибка вызова AI API (Попытка %d/%d, время: %v): %v", payload.TaskID, attempt, h.cfg.AIMaxAttempts, aiCallDuration, processingErr)
				MetricsRecordAIRequest(h.cfg.AIModel, aiStatusLabel, aiCallDuration)

				if attempt == h.cfg.AIMaxAttempts {
					log.Printf("[TaskID: %s] Достигнуто максимальное количество попыток (%d) вызова AI.", payload.TaskID, h.cfg.AIMaxAttempts)
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

			if processingErr == nil {
				aiResponse = aiResponse
			}
		}
	} // <<< Конец блока else (обработка после успешного получения промпта задачи) >>>

SaveAndNotify: // Метка для перехода при ошибке получения промптов
	// --- Этап N: Сохранение результата и отправка уведомления --- //
	completedAt = time.Now() // Обновляем время завершения перед сохранением
	processingDuration := completedAt.Sub(taskStartTime)
	saveErr := h.saveAndNotifyResult(
		payload.TaskID,
		payload.UserID,
		payload.PromptType,
		payload.StoryConfigID,
		payload.PublishedStoryID,
		payload.StateHash,
		aiResponse,    // Будет пустой строкой, если была ошибка до вызова AI
		processingErr, // Передаем ошибку этапа обработки (prompt/AI)
		taskStartTime,
		completedAt,
		processingDuration,
		finalUsageInfo, // Будет нулевой, если AI не вызывался или все попытки неудачны
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

// saveAndNotifyResult сохраняет результат (или ошибку) в БД и отправляет уведомление
func (h *TaskHandler) saveAndNotifyResult(
	taskID string,
	userID string,
	promptType sharedModels.PromptType,
	storyConfigID string,
	publishedStoryID string,
	stateHash string,
	aiResponse string,
	processingErr error,
	createdAt time.Time,
	completedAt time.Time,
	processingTime time.Duration,
	usageInfo service.UsageInfo, // <<< ДОБАВЛЕНО: Принимаем UsageInfo >>>
) error {
	ctx := context.Background() // Используем фоновый контекст для сохранения/уведомления

	// --- Подготовка данных для сохранения --- //
	errorDetails := ""
	if processingErr != nil {
		errorDetails = processingErr.Error()
	}

	result := &sharedModels.GenerationResult{
		ID:               taskID,
		PromptType:       promptType,
		UserID:           userID,
		GeneratedText:    aiResponse,   // Будет пустым при ошибке
		Error:            errorDetails, // Ошибка изначальной обработки AI
		CreatedAt:        createdAt,
		CompletedAt:      completedAt,
		ProcessingTimeMs: processingTime.Milliseconds(), // Используем milliseconds и правильное имя поля
		PromptTokens:     usageInfo.PromptTokens,        // <<< Используем данные из usageInfo >>>
		CompletionTokens: usageInfo.CompletionTokens,    // <<< Используем данные из usageInfo >>>
		EstimatedCostUSD: usageInfo.EstimatedCostUSD,    // <<< Используем данные из usageInfo >>>
	}

	// --- Сохранение в БД --- //
	saveDbErr := h.resultRepo.Save(ctx, result)
	if saveDbErr != nil {
		log.Printf("[TaskID: %s] Ошибка сохранения результата в БД: %v", taskID, saveDbErr)
		MetricsIncrementTaskFailed("save_error")
		// Дополняем детали ошибки для уведомления
		if errorDetails == "" {
			errorDetails = fmt.Sprintf("ошибка сохранения результата: %v", saveDbErr)
		} else {
			errorDetails = fmt.Sprintf("ошибка обработки: %s; ошибка сохранения: %v", errorDetails, saveDbErr)
		}
	} else {
		log.Printf("[TaskID: %s] Результат успешно сохранен в БД.", taskID)
	}

	// --- Отправка уведомления --- //
	// Статус зависит И от ошибки обработки, И от ошибки сохранения
	notificationStatus := messaging.NotificationStatusSuccess
	finalErrorForNotification := "" // Ошибка, которая пойдет в уведомление

	if processingErr != nil || saveDbErr != nil { // Если была ЛЮБАЯ ошибка (обработка или сохранение)
		notificationStatus = messaging.NotificationStatusError
		finalErrorForNotification = errorDetails // Используем собранные детали ошибки
	}

	// Определяем ID для уведомления (Config или Published)
	var storyConfIDToSend, pubStoryIDToSend *string
	if storyConfigID != "" {
		storyConfIDToSend = &storyConfigID
	} else if publishedStoryID != "" {
		pubStoryIDToSend = &publishedStoryID
	}

	payload := messaging.NotificationPayload{
		TaskID:           taskID,
		UserID:           userID,
		PromptType:       promptType,
		Status:           notificationStatus,        // Используем вычисленный статус
		ErrorDetails:     finalErrorForNotification, // Используем собранные ошибки
		StateHash:        stateHash,
		StoryConfigID:    safeDerefString(storyConfIDToSend),
		PublishedStoryID: safeDerefString(pubStoryIDToSend),
	}

	log.Printf("[TaskID: %s] Отправка уведомления (статус: %s)...", taskID, notificationStatus)
	notifyErr := h.notifier.Notify(ctx, payload)
	if notifyErr != nil {
		log.Printf("[TaskID: %s] Ошибка отправки уведомления: %v", taskID, notifyErr)
		MetricsIncrementTaskFailed("notify_error")
		// Если была ошибка сохранения И ошибка уведомления - возвращаем обе
		if saveDbErr != nil {
			return fmt.Errorf("ошибка сохранения (%w) и отправки уведомления (%w)", saveDbErr, notifyErr)
		}
		// Если была только ошибка уведомления
		return fmt.Errorf("ошибка отправки уведомления: %w", notifyErr)
	}

	log.Printf("[TaskID: %s] Уведомление успешно отправлено.", taskID)

	// Если была ошибка сохранения, но уведомление ушло, возвращаем ошибку сохранения
	if saveDbErr != nil {
		return fmt.Errorf("ошибка сохранения результата в БД: %w", saveDbErr)
	}

	// Если не было ни ошибки обработки, ни ошибки сохранения, записываем успех задачи
	if processingErr == nil && saveDbErr == nil {
		MetricsIncrementTaskSucceeded()
	}

	// Если все прошло успешно (сохранение и уведомление), возвращаем nil
	// Если была ошибка обработки (processingErr != nil), но сохранение и уведомление прошли успешно,
	// эта ошибка уже будет возвращена из главной функции Handle. Здесь возвращаем nil.
	return nil
}

// float64Ptr возвращает указатель на float64
func float64Ptr(f float64) *float64 {
	return &f
}

// safeDerefString разыменовывает указатель на строку или возвращает пустую строку, если указатель nil.
func safeDerefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
