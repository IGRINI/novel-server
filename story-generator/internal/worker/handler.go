package worker

import (
	// Для рендеринга шаблона
	"context"
	"fmt"
	"log"
	"math"
	"math/rand"
	"novel-server/shared/messaging"
	"novel-server/story-generator/internal/config"     // Импортируем конфиг для AIMaxAttempts
	"novel-server/story-generator/internal/model"      // Добавляем модель
	"novel-server/story-generator/internal/repository" // Добавляем репозиторий
	"novel-server/story-generator/internal/service"
	"os"
	"path/filepath" // Для шаблонизации промтов
	"strings"       // <<< ДОБАВЛЕНО: для strings.Replace >>>
	"time"
)

// <<< ДОБАВЛЕНО: Короткий системный промпт >>>
const fixedShortSystemPrompt = `You are a strict JSON API responder.
Always answer ONLY with a single valid JSON object.
Do not include any text, comments, or explanations.`

// TaskHandler обрабатывает задачи генерации
type TaskHandler struct {
	PromptsDir     string                      // Директория с файлами промтов
	aiClient       service.AIClient            // Клиент для AI API
	resultRepo     repository.ResultRepository // Репозиторий для сохранения результатов
	notifier       service.Notifier            // Сервис для отправки уведомлений
	maxAttempts    int                         // Максимальное кол-во попыток вызова AI
	aiTimeout      time.Duration               // Таймаут для одного вызова AI
	baseRetryDelay time.Duration               // Базовая задержка перед ретраем
	aiModel        string                      // <<< ДОБАВЛЕНО: Имя модели для метрик >>>
}

// NewTaskHandler создает новый экземпляр обработчика задач
func NewTaskHandler(cfg *config.Config, aiClient service.AIClient, resultRepo repository.ResultRepository, notifier service.Notifier) *TaskHandler {
	return &TaskHandler{
		PromptsDir:     cfg.PromptsDir,
		aiClient:       aiClient,
		resultRepo:     resultRepo,
		notifier:       notifier,
		maxAttempts:    cfg.AIMaxAttempts,
		aiTimeout:      cfg.AITimeout,
		baseRetryDelay: cfg.AIBaseRetryDelay,
		aiModel:        cfg.AIModel, // <<< ДОБАВЛЕНО: Сохраняем имя модели >>>
	}
}

// Handle обрабатывает одну задачу генерации
func (h *TaskHandler) Handle(payload messaging.GenerationTaskPayload) (err error) { // Возвращаем именованную ошибку для удобства defer
	taskStartTime := time.Now() // Замеряем время начала обработки задачи
	log.Printf("[TaskID: %s] Обработка задачи: UserID=%s, PromptType=%s",
		payload.TaskID, payload.UserID, payload.PromptType)

	// Метка для прометеуса
	promptTypeLabel := string(payload.PromptType)
	// Переменная для статуса задачи (для финальной метрики длительности)
	taskStatus := "success" // По умолчанию успех

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

	// Объявляем переменные для результатов и ошибок
	var promptInstructions string // Переименовали finalSystemPrompt
	var aiResponse string
	var processingErr error // Переименовали err в processingErr чтобы не конфликтовать с именованным возвращаемым значением
	var completedAt time.Time
	var totalTaskTokensPrompt, totalTaskTokensCompletion float64
	var totalTaskCost float64

	// --- Этап 1: Загрузка инструкций из файла промта ---
	promptInstructions, processingErr = h.preparePrompt(payload.TaskID, payload.PromptType)
	if processingErr != nil {
		log.Printf("[TaskID: %s] Ошибка подготовки инструкций из промта: %v", payload.TaskID, processingErr)
		MetricsIncrementTaskFailed("prompt_preparation")                              // Записываем метрику ошибки
		err = fmt.Errorf("ошибка подготовки инструкций из промта: %w", processingErr) // Устанавливаем возвращаемую ошибку
		return err
	}

	// --- Этап 2: Формирование финального UserInput для AI ---
	originalUserInput := payload.UserInput
	// <<< ДОБАВЛЕНО: Логирование оригинального UserInput >>>
	log.Printf("[TaskID: %s] Original UserInput received (length: %d): %s", payload.TaskID, len(originalUserInput), originalUserInput)
	// <<< КОНЕЦ ДОБАВЛЕНИЯ >>>

	// Объединяем инструкции из файла и оригинальный UserInput
	// userInputForAI := fmt.Sprintf("--- Instructions ---\n%s\n\n--- User Input ---\n%s", promptInstructions, originalUserInput)
	// <<< ИЗМЕНЕНО: Используем strings.Replace >>>
	userInputForAI := strings.Replace(promptInstructions, "{{USER_INPUT}}", originalUserInput, 1)
	// Проверка, что замена произошла (опционально, для отладки)
	if userInputForAI == promptInstructions {
		log.Printf("[TaskID: %s][WARN] Placeholder {{USER_INPUT}} не найден в файле промпта типа '%s'. AI получит только инструкции.", payload.TaskID, payload.PromptType)
		// Можно решить, как обрабатывать эту ситуацию: вернуть ошибку или продолжить только с инструкциями.
		// Пока продолжаем, но логируем.
	}

	log.Printf("[TaskID: %s] Final UserInput for AI (length: %d).", payload.TaskID, len(userInputForAI))

	// --- Этап 3: Вызов AI API с ретраями ---
	// userInput больше не используется напрямую, используем userInputForAI
	// log.Printf("[TaskID: %s] UserInput для AI API (длина: %d).", payload.TaskID, len(originalUserInput))

	// Проверка может быть нужна для originalUserInput или userInputForAI в зависимости от логики
	if originalUserInput == "" && payload.PromptType != "" { // Пока проверяем оригинальный, т.к. инструкции всегда будут?
		processingErr = fmt.Errorf("ошибка: userInput пуст для PromptType '%s'", payload.PromptType)
		log.Printf("[TaskID: %s] %v", payload.TaskID, processingErr)
		MetricsIncrementTaskFailed("user_input_empty")          // Записываем метрику ошибки
		err = fmt.Errorf("ошибка валидации: %w", processingErr) // Устанавливаем возвращаемую ошибку
		return err
	}

	baseDelay := h.baseRetryDelay
	var lastSuccessfulUsageInfo service.UsageInfo // Храним usage последнего успеха

	for attempt := 1; attempt <= h.maxAttempts; attempt++ {
		aiCallStartTime := time.Now() // Время начала конкретного вызова AI
		log.Printf("[TaskID: %s] Вызов AI API (Попытка %d/%d)...", payload.TaskID, attempt, h.maxAttempts)
		ctx, cancel := context.WithTimeout(context.Background(), h.aiTimeout)

		// Логирование перед вызовом (опционально, может быть слишком многословно)
		// log.Printf(`[TaskID: %s][Attempt: %d] Отправка запроса в AI...`, payload.TaskID, attempt)

		var attemptUsageInfo service.UsageInfo
		var attemptErr error
		aiResponse, attemptUsageInfo, attemptErr = h.aiClient.GenerateText(ctx,
			payload.UserID,
			fixedShortSystemPrompt, // <<< Передаем короткий системный промпт >>>
			userInputForAI,         // <<< Передаем объединенный ввод >>>
			service.GenerationParams{Temperature: float64Ptr(0.2)})
		cancel() // Освобождаем ресурсы контекста

		aiCallDuration := time.Since(aiCallStartTime) // Время выполнения этого вызова AI
		aiStatusLabel := "success"                    // Статус для метрики этого вызова

		if attemptErr == nil {
			log.Printf("[TaskID: %s] AI API успешно ответил (Попытка %d). Время ответа: %v", payload.TaskID, attempt, aiCallDuration)
			lastSuccessfulUsageInfo = attemptUsageInfo // Сохраняем данные последнего успешного вызова
			MetricsRecordAIRequest(h.aiModel, aiStatusLabel, aiCallDuration)
			// Записываем токены и стоимость *этого конкретного* AI вызова
			if attemptUsageInfo.TotalTokens > 0 || attemptUsageInfo.EstimatedCostUSD > 0 {
				MetricsRecordAITokens(h.aiModel, float64(attemptUsageInfo.PromptTokens), float64(attemptUsageInfo.CompletionTokens))
				MetricsAddAICost(h.aiModel, attemptUsageInfo.EstimatedCostUSD)
				log.Printf("[TaskID: %s][Attempt %d Metrics] Tokens: P=%d, C=%d. Cost: %.6f USD",
					payload.TaskID, attempt, attemptUsageInfo.PromptTokens, attemptUsageInfo.CompletionTokens, attemptUsageInfo.EstimatedCostUSD)
			}
			processingErr = nil // Сбрасываем ошибку, т.к. попытка успешна
			break               // Выходим из цикла ретраев
		}

		// Обработка ошибки вызова AI
		aiStatusLabel = "error"    // Меняем статус для метрики
		processingErr = attemptErr // Сохраняем последнюю ошибку
		log.Printf("[TaskID: %s] Ошибка вызова AI API (Попытка %d/%d, время: %v): %v", payload.TaskID, attempt, h.maxAttempts, aiCallDuration, processingErr)
		MetricsRecordAIRequest(h.aiModel, aiStatusLabel, aiCallDuration) // Записываем неудачную попытку

		if attempt == h.maxAttempts {
			log.Printf("[TaskID: %s] Достигнуто максимальное количество попыток (%d) вызова AI.", payload.TaskID, h.maxAttempts)
			MetricsIncrementTaskFailed("ai_error")                             // Регистрируем финальную ошибку AI
			err = fmt.Errorf("достигнуто макс. попыток AI: %w", processingErr) // Устанавливаем возвращаемую ошибку
			// Не выходим из функции здесь, даем коду дойти до сохранения/уведомления
			break // Выходим из цикла ретраев
		}

		// Расчет задержки перед следующей попыткой
		delay := float64(baseDelay) * math.Pow(2, float64(attempt-1))
		jitter := delay * 0.1                    // 10% jitter
		delay += jitter * (rand.Float64()*2 - 1) // Apply jitter (+/- 10%)
		waitDuration := time.Duration(delay)
		if waitDuration < baseDelay { // Ensure minimum delay
			waitDuration = baseDelay
		}

		log.Printf("[TaskID: %s] Ожидание %v перед следующей попыткой...", payload.TaskID, waitDuration)
		time.Sleep(waitDuration)
	}

	// --- Запись итоговых метрик по токенам/стоимости ЗАДАЧИ ---
	// Используем данные последнего *успешного* вызова AI (lastSuccessfulUsageInfo)
	// Если все попытки провалились, токенов/стоимости не будет (значения по умолчанию 0)
	if processingErr == nil { // Только если AI в итоге успешно отработал
		totalTaskTokensPrompt = float64(lastSuccessfulUsageInfo.PromptTokens)
		totalTaskTokensCompletion = float64(lastSuccessfulUsageInfo.CompletionTokens)
		totalTaskCost = lastSuccessfulUsageInfo.EstimatedCostUSD

		MetricsRecordTaskTokens(promptTypeLabel, "prompt", totalTaskTokensPrompt)
		MetricsRecordTaskTokens(promptTypeLabel, "completion", totalTaskTokensCompletion)
		MetricsRecordTaskCost(promptTypeLabel, totalTaskCost)
		log.Printf("[TaskID: %s][Task Final Metrics] Tokens: P=%.0f, C=%.0f. Cost: %.6f USD",
			payload.TaskID, totalTaskTokensPrompt, totalTaskTokensCompletion, totalTaskCost)
	}
	// -------------------------------------------------------------

	completedAt = time.Now() // Время завершения логики обработчика (до сохранения)

	// Логируем финальный ответ AI или ошибку
	if processingErr != nil {
		log.Printf("[TaskID: %s] Финальная ошибка после всех попыток AI: %v", payload.TaskID, processingErr)
		// Метрика tasksFailed("ai_error") уже записана выше, если все попытки неудачны
	} else {
		// Успешный ответ AI
		log.Printf("[TaskID: %s] Финальный ответ от AI получен (длина: %d).", payload.TaskID, len(aiResponse))
		// <<< ДОБАВЛЕНО: Логирование полного ответа AI >>>
		log.Printf("[TaskID: %s] Полный ответ AI: %s", payload.TaskID, aiResponse)
		// <<< КОНЕЦ ДОБАВЛЕНИЯ >>>
		MetricsIncrementTaskSucceeded() // <<< Инкрементируем успех здесь >>>
	}

	// --- Этап 4-6: Сохранение и уведомление ---
	saveErr := h.saveAndNotifyResult(
		payload.TaskID,
		payload.UserID,
		payload.PromptType, // Используем оригинальный promptType
		payload.StoryConfigID,
		payload.PublishedStoryID,
		payload.StateHash,
		aiResponse,
		processingErr, // Передаем ошибку AI/подготовки, если она была
		taskStartTime, // Передаем время начала задачи
		completedAt,   // Передаем время завершения обработки
		// processingTime больше не передаем, т.к. не используется
	)

	if saveErr != nil {
		MetricsIncrementTaskFailed("save_error") // Записываем ошибку сохранения
		// Если была ошибка AI/подготовки, она уже установлена как возвращаемая ошибка `err`
		// Если ошибки AI/подготовки не было, устанавливаем ошибку сохранения как возвращаемую
		if err == nil {
			err = saveErr
		}
		// В любом случае возвращаем ошибку (err), defer обработает запись taskStatus="failed"
		return err
	} else if processingErr != nil {
		// Ошибка была на этапе AI/подготовки (err уже установлено)
		// Сохранение прошло успешно, но задача считается неуспешной из-за ошибки AI/подготовки
		return err // Возвращаем ошибку AI/подготовки
	}

	// Успешная обработка AI и успешное сохранение/уведомление
	// tasksSucceededTotal инкрементирован выше
	log.Printf("[TaskID: %s] Задача успешно обработана, сохранена и уведомление отправлено.", payload.TaskID)
	return nil // Возвращаем nil для ack сообщения
}

// preparePrompt загружает инструкции из файла промта
func (h *TaskHandler) preparePrompt(taskID string, promptType messaging.PromptType) (string, error) {
	log.Printf("[TaskID: %s] Загрузка инструкций из промта типа '%s'...", taskID, promptType)
	if promptType == "" {
		return "", fmt.Errorf("PromptType не может быть пустым")
	}
	promptFileName := string(promptType) + ".md"
	promptFilePath := filepath.Join(h.PromptsDir, promptFileName)

	systemPromptBytes, readErr := os.ReadFile(promptFilePath)
	if readErr != nil {
		return "", fmt.Errorf("ошибка чтения файла промта '%s': %w", promptFilePath, readErr)
	}
	systemPrompt := string(systemPromptBytes)

	// Пока шаблонизация не используется, просто возвращаем содержимое файла
	finalSystemPrompt := systemPrompt
	log.Printf("[TaskID: %s] Инструкции '%s' успешно загружены.", taskID, promptFileName)
	return finalSystemPrompt, nil
}

// saveAndNotifyResult сохраняет результат (или ошибку) в БД и отправляет уведомление.
// Возвращает ошибку, если сохранение или уведомление не удалось.
func (h *TaskHandler) saveAndNotifyResult(
	taskID string,
	userID string,
	promptType messaging.PromptType,
	storyConfigID string,
	publishedStoryID string,
	stateHash string,
	aiResponse string,
	processingErr error, // Ошибка от этапа подготовки или AI
	createdAt time.Time, // Используем время начала задачи из Handle
	completedAt time.Time, // Используем время завершения из Handle
	// processingTime time.Duration, // Убрали, т.к. не используется
) error {
	var errorMsg string
	status := messaging.NotificationStatusSuccess // Статус для уведомления
	if processingErr != nil {
		errorMsg = processingErr.Error()
		status = messaging.NotificationStatusError
	}

	// Заполняем поле GeneratedText даже при ошибке (для записи в БД)
	processedResult := aiResponse
	if processedResult == "" && errorMsg != "" {
		processedResult = fmt.Sprintf("[генерация не удалась: %s]", errorMsg)
	}

	log.Printf("[TaskID: %s] Сохранение результата в БД (статус: %s)...", taskID, status)

	result := &model.GenerationResult{
		ID:             taskID,
		UserID:         userID,
		PromptType:     promptType,
		GeneratedText:  processedResult,            // Сохраняем результат или сообщение об ошибке
		ProcessingTime: completedAt.Sub(createdAt), // Сохраняем *общую* длительность задачи
		CreatedAt:      createdAt,
		CompletedAt:    completedAt,
		Error:          errorMsg, // Сохраняем текст ошибки, если была
	}

	// Сохранение результата
	saveCtx, saveCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer saveCancel()
	saveErr := h.resultRepo.Save(saveCtx, result)
	if saveErr != nil {
		log.Printf("[TaskID: %s] Ошибка сохранения результата в БД: %v", taskID, saveErr)
		// Не возвращаем ошибку немедленно, пытаемся отправить уведомление об ошибке
	} else {
		log.Printf("[TaskID: %s] Результат успешно сохранен в БД.", taskID)
	}

	// Отправка уведомления
	log.Printf("[TaskID: %s] Отправка уведомления (статус: %s)...", taskID, status)
	notificationPayload := messaging.NotificationPayload{
		TaskID:           taskID,
		UserID:           userID,
		PromptType:       promptType,
		StoryConfigID:    storyConfigID,
		PublishedStoryID: publishedStoryID,
		StateHash:        stateHash,
		Status:           status,
		ErrorDetails:     errorMsg, // Передаем текст ошибки
		GeneratedText:    "",       // Не передаем полный текст в уведомлении успеха (если не нужно)
	}
	// Если статус Успех, и нужно передать текст (например, превью), добавить сюда:
	// if status == messaging.NotificationStatusSuccess {
	//     notificationPayload.GeneratedText = processedResult // Или его часть
	// }

	notifyCtx, notifyCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer notifyCancel()
	notifyErr := h.notifier.Notify(notifyCtx, notificationPayload)
	if notifyErr != nil {
		log.Printf("[TaskID: %s][WARN] Не удалось отправить уведомление: %v", taskID, notifyErr)
		// Не считаем это критической ошибкой для ретрая задачи, просто логируем
	} else {
		log.Printf("[TaskID: %s] Уведомление успешно отправлено.", taskID)
	}

	// Возвращаем ошибку сохранения, если она была, чтобы nack-нуть сообщение
	if saveErr != nil {
		return fmt.Errorf("ошибка сохранения результата в БД: %w", saveErr)
	}

	// Если ошибки сохранения не было, возвращаем nil (даже если была ошибка AI/подготовки)
	return nil
}

// <<< ДОБАВЛЕНО: Вспомогательная функция для получения указателя на float64 >>>
func float64Ptr(f float64) *float64 {
	return &f
}

// <<< КОНЕЦ ДОБАВЛЕНИЯ >>>
