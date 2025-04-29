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
	PromptsDir     string                                      // Директория с файлами промтов
	aiClient       service.AIClient                            // Клиент для AI API
	resultRepo     sharedInterfaces.GenerationResultRepository // Репозиторий для сохранения результатов
	notifier       service.Notifier                            // Сервис для отправки уведомлений
	maxAttempts    int                                         // Максимальное кол-во попыток вызова AI
	aiTimeout      time.Duration                               // Таймаут для одного вызова AI
	baseRetryDelay time.Duration                               // Базовая задержка перед ретраем
	aiModel        string                                      // <<< ДОБАВЛЕНО: Имя модели для метрик >>>
}

// NewTaskHandler создает новый экземпляр обработчика задач
func NewTaskHandler(cfg *config.Config, aiClient service.AIClient, resultRepo sharedInterfaces.GenerationResultRepository, notifier service.Notifier) *TaskHandler {
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
	// promptTypeLabel := string(payload.PromptType) // <<< УДАЛЕНО
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
	var finalUsageInfo service.UsageInfo // <<< ДОБАВЛЕНО: для хранения UsageInfo успешного вызова >>>

	// <<< ВЫЧИСЛЯЕМ ВРЕМЯ ЗАВЕРШЕНИЯ ЗДЕСЬ >>>
	completedAt = time.Now()

	// <<< ВЫЧИСЛЯЕМ ДЛИТЕЛЬНОСТЬ ОБРАБОТКИ >>>
	processingDuration := completedAt.Sub(taskStartTime)

	// --- Этап 1: Загрузка инструкций из файла промта ---
	promptInstructions, processingErr = h.preparePrompt(payload.TaskID, sharedModels.PromptType(payload.PromptType))
	if processingErr != nil {
		log.Printf("[TaskID: %s] Ошибка подготовки инструкций из промта: %v", payload.TaskID, processingErr)
		MetricsIncrementTaskFailed("prompt_preparation")                              // Записываем метрику ошибки
		err = fmt.Errorf("ошибка подготовки инструкций из промта: %w", processingErr) // Устанавливаем возвращаемую ошибку
		// <<< УДАЛЕНО: return err - даем коду дойти до saveAndNotifyResult >>>
	} else {
		// --- Этап 2: Формирование финального UserInput для AI (только если промпт загружен) ---
		originalUserInput := payload.UserInput
		log.Printf("[TaskID: %s] Original UserInput received (length: %d): %s", payload.TaskID, len(originalUserInput), originalUserInput)

		userInputForAI := strings.Replace(promptInstructions, "{{USER_INPUT}}", originalUserInput, 1)
		if userInputForAI == promptInstructions {
			log.Printf("[TaskID: %s][WARN] Placeholder {{USER_INPUT}} не найден в файле промпта типа '%s'. AI получит только инструкции.", payload.TaskID, payload.PromptType)
		}
		log.Printf("[TaskID: %s] Final UserInput for AI (length: %d).", payload.TaskID, len(userInputForAI))

		// --- Этап 3: Вызов AI API с ретраями (только если промпт загружен) ---
		if originalUserInput == "" && payload.PromptType != "" { // TODO: Уточнить, всегда ли нужен UserInput?
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
					fixedShortSystemPrompt,
					userInputForAI,
					service.GenerationParams{Temperature: float64Ptr(0.2)})
				cancel()

				aiCallDuration := time.Since(aiCallStartTime)
				aiStatusLabel := "success"

				if attemptErr == nil {
					log.Printf("[TaskID: %s] AI API успешно ответил (Попытка %d). Время ответа: %v", payload.TaskID, attempt, aiCallDuration)
					log.Printf("[TaskID: %s] Raw AI Response (length: %d): %s", payload.TaskID, len(aiResponse), aiResponse)
					MetricsRecordAIRequest(h.aiModel, aiStatusLabel, aiCallDuration)
					if attemptUsageInfo.TotalTokens > 0 || attemptUsageInfo.EstimatedCostUSD > 0 {
						MetricsRecordAITokens(h.aiModel, float64(attemptUsageInfo.PromptTokens), float64(attemptUsageInfo.CompletionTokens))
						MetricsAddAICost(h.aiModel, attemptUsageInfo.EstimatedCostUSD)
						log.Printf("[TaskID: %s][Attempt %d Metrics] Tokens: P=%d, C=%d. Cost: %.6f USD",
							payload.TaskID, attempt, attemptUsageInfo.PromptTokens, attemptUsageInfo.CompletionTokens, attemptUsageInfo.EstimatedCostUSD)
					}
					finalUsageInfo = attemptUsageInfo // <<< СОХРАНЯЕМ UsageInfo успешного вызова >>>
					processingErr = nil
					break
				}

				aiStatusLabel = "error"
				processingErr = attemptErr
				log.Printf("[TaskID: %s] Ошибка вызова AI API (Попытка %d/%d, время: %v): %v", payload.TaskID, attempt, h.maxAttempts, aiCallDuration, processingErr)
				MetricsRecordAIRequest(h.aiModel, aiStatusLabel, aiCallDuration)

				if attempt == h.maxAttempts {
					log.Printf("[TaskID: %s] Достигнуто максимальное количество попыток (%d) вызова AI.", payload.TaskID, h.maxAttempts)
					MetricsIncrementTaskFailed("ai_error")
					err = fmt.Errorf("достигнуто макс. попыток AI: %w", processingErr)
					break
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
	}

	// --- Этап 4: Сохранение результата и отправка уведомления --- //
	saveErr := h.saveAndNotifyResult(
		payload.TaskID,
		payload.UserID,
		sharedModels.PromptType(payload.PromptType),
		payload.StoryConfigID,
		payload.PublishedStoryID,
		payload.StateHash,
		aiResponse,
		processingErr,
		taskStartTime,
		completedAt,
		processingDuration,
		finalUsageInfo, // <<< ПЕРЕДАЕМ UsageInfo >>>
	)

	if saveErr != nil {
		log.Printf("[TaskID: %s] Критическая ошибка при сохранении результата или отправке уведомления: %v", payload.TaskID, saveErr)
		// Если была ошибка до этого, сохраняем её.
		// Если ошибок не было, но не смогли сохранить/уведомить, то это новая ошибка.
		if err == nil {
			err = fmt.Errorf("ошибка сохранения/уведомления: %w", saveErr)
			MetricsIncrementTaskFailed("save_notify_error") // <<< НОВАЯ МЕТРИКА >>>
		}
		// Возвращаем ошибку в любом случае, чтобы сообщение попало в Nack
		return err
	}

	// Если была ошибка обработки (processingErr), но сохранение/уведомление прошло успешно,
	// возвращаем исходную ошибку обработки.
	if processingErr != nil && err == nil {
		err = fmt.Errorf("ошибка обработки: %w", processingErr)
	}

	return err // Возвращаем nil, если все успешно, или ошибку обработки
}

// preparePrompt загружает инструкции из файла промта
func (h *TaskHandler) preparePrompt(taskID string, promptType sharedModels.PromptType) (string, error) {
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
