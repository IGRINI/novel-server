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
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// Счетчик полученных задач
	tasksReceivedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "story_generator_tasks_received_total",
		Help: "Total number of tasks received by the worker.",
	})
	// Счетчик успешно обработанных задач
	tasksSucceededTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "story_generator_tasks_succeeded_total",
		Help: "Total number of tasks successfully processed.",
	})
	// Счетчик задач с ошибкой
	tasksFailedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "story_generator_tasks_failed_total",
		Help: "Total number of tasks failed during processing, labeled by error type.",
	}, []string{"error_type"}) // Добавляем label для типа ошибки

	// Гистограмма общей длительности обработки задачи
	taskProcessingDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "story_generator_task_processing_duration_seconds",
		Help:    "Total duration of task processing.",
		Buckets: prometheus.ExponentialBuckets(0.5, 2, 10), // Базовые бакеты: 0.5s, 1s, 2s ... ~4 min
	})
)

// IncrementTasksReceived инкрементирует счетчик полученных задач.
func IncrementTasksReceived() {
	tasksReceivedTotal.Inc()
}

// IncrementTaskFailed инкрементирует счетчик задач с ошибкой, указывая тип ошибки.
func IncrementTaskFailed(errorType string) {
	tasksFailedTotal.WithLabelValues(errorType).Inc()
}

// TaskHandler обрабатывает задачи генерации
type TaskHandler struct {
	PromptsDir     string                      // Директория с файлами промтов
	aiClient       service.AIClient            // Клиент для AI API
	resultRepo     repository.ResultRepository // Репозиторий для сохранения результатов
	notifier       service.Notifier            // Сервис для отправки уведомлений
	maxAttempts    int                         // Максимальное кол-во попыток вызова AI
	aiTimeout      time.Duration               // Таймаут для одного вызова AI
	baseRetryDelay time.Duration               // Добавляем поле
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
	}
}

// Handle обрабатывает одну задачу генерации
func (h *TaskHandler) Handle(payload messaging.GenerationTaskPayload) error {
	log.Printf("[TaskID: %s] Обработка задачи: UserID=%s, PromptType=%s",
		payload.TaskID, payload.UserID, payload.PromptType)

	fullStartTime := time.Now()
	createdAt := fullStartTime

	// Объявляем переменные для результатов и ошибок
	var finalSystemPrompt string
	var aiResponse string
	var err error
	var processingTime time.Duration
	var completedAt time.Time

	// --- Этап 1-2: Загрузка и рендеринг промта ---
	finalSystemPrompt, err = h.preparePrompt(payload.TaskID, payload.PromptType)
	if err != nil {
		log.Printf("[TaskID: %s] Ошибка подготовки промта: %v", payload.TaskID, err)
		completedAt = time.Now()
		processingTime = completedAt.Sub(fullStartTime)
		tasksFailedTotal.WithLabelValues("prompt_preparation").Inc()
		taskProcessingDuration.Observe(processingTime.Seconds())
		return h.saveAndNotifyResult(payload.TaskID, payload.UserID, payload.PromptType, payload.StoryConfigID, payload.PublishedStoryID, payload.StateHash, "", err, createdAt, completedAt, processingTime)
	}

	// --- Этап 3: Вызов AI API с ретраями ---
	userInput := payload.UserInput
	log.Printf("[TaskID: %s] UserInput для AI API (длина: %d).", payload.TaskID, len(userInput))

	if userInput == "" && payload.PromptType != "" {
		err = fmt.Errorf("ошибка: userInput пуст для PromptType '%s'", payload.PromptType)
		log.Printf("[TaskID: %s] %v", payload.TaskID, err)
		completedAt = time.Now()
		processingTime = completedAt.Sub(fullStartTime)
		tasksFailedTotal.WithLabelValues("user_input_empty").Inc()
		taskProcessingDuration.Observe(processingTime.Seconds())
		return h.saveAndNotifyResult(payload.TaskID, payload.UserID, payload.PromptType, payload.StoryConfigID, payload.PublishedStoryID, payload.StateHash, "", err, createdAt, completedAt, processingTime)
	}

	baseDelay := h.baseRetryDelay

	for attempt := 1; attempt <= h.maxAttempts; attempt++ {
		log.Printf("[TaskID: %s] Вызов AI API (Попытка %d/%d)...", payload.TaskID, attempt, h.maxAttempts)
		aiStartTime := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), h.aiTimeout)

		aiResponse, err = h.aiClient.GenerateText(ctx, payload.UserID, finalSystemPrompt, userInput, service.GenerationParams{})
		cancel()

		processingTime = time.Since(aiStartTime)

		if err == nil {
			log.Printf("[TaskID: %s] AI API успешно ответил (Попытка %d).", payload.TaskID, attempt)
			break
		}

		log.Printf("[TaskID: %s] Ошибка вызова AI API (Попытка %d/%d): %v", payload.TaskID, attempt, h.maxAttempts, err)

		if attempt == h.maxAttempts {
			log.Printf("[TaskID: %s] Достигнуто максимальное количество попыток (%d) вызова AI.", payload.TaskID, h.maxAttempts)
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
	completedAt = time.Now()
	totalDuration := completedAt.Sub(fullStartTime)
	taskProcessingDuration.Observe(totalDuration.Seconds())

	// Логируем финальный ответ AI или ошибку
	if err != nil {
		log.Printf("[TaskID: %s] Финальная ошибка после всех попыток AI: %v", payload.TaskID, err)
		tasksFailedTotal.WithLabelValues("ai_error").Inc() // Регистрируем ошибку AI
	} else {
		// Вот сюда добавим лог
		log.Printf("[TaskID: %s] Финальный ответ от AI (длина: %d): %s", payload.TaskID, len(aiResponse), aiResponse)
	}

	// --- Этап 4-6: Сохранение и уведомление ---
	saveErr := h.saveAndNotifyResult(payload.TaskID, payload.UserID, payload.PromptType, payload.StoryConfigID, payload.PublishedStoryID, payload.StateHash, aiResponse, err, createdAt, completedAt, processingTime)

	if saveErr != nil {
		tasksFailedTotal.WithLabelValues("save_error").Inc()
		return saveErr
	} else if err != nil {
		return err
	} else {
		tasksSucceededTotal.Inc()
		return nil
	}
}

// preparePrompt загружает и рендерит шаблон промта
func (h *TaskHandler) preparePrompt(taskID string, promptType messaging.PromptType) (string, error) {
	log.Printf("[TaskID: %s] Загрузка и рендеринг промта...", taskID)
	promptFilePath := filepath.Join(h.PromptsDir, string(promptType)+".md")
	systemPromptBytes, readErr := os.ReadFile(promptFilePath)
	if readErr != nil {
		return "", fmt.Errorf("ошибка чтения файла промта '%s': %w", promptFilePath, readErr)
	}
	systemPrompt := string(systemPromptBytes)

	finalSystemPrompt := systemPrompt
	log.Printf("[TaskID: %s] Промт успешно подготовлен (без шаблонизации).", taskID)
	return finalSystemPrompt, nil
}

// saveAndNotifyResult сохраняет результат (или ошибку) в БД и отправляет уведомление.
// Возвращает ошибку, если задача должна быть nack-нута (ошибка AI/подготовки/сохранения).
func (h *TaskHandler) saveAndNotifyResult(
	taskID string,
	userID string,
	promptType messaging.PromptType,
	storyConfigID string,
	publishedStoryID string,
	stateHash string,
	aiResponse string,
	processingErr error, // Ошибка от этапа подготовки или AI
	createdAt time.Time,
	completedAt time.Time,
	processingTime time.Duration, // Время последнего вызова AI или подготовки
) error {
	var errorMsg string
	if processingErr != nil {
		errorMsg = processingErr.Error()
	}

	if aiResponse == "" && errorMsg != "" {
		aiResponse = "[генерация не удалась из-за ошибки]"
	}
	processedResult := aiResponse

	log.Printf("[TaskID: %s] Сохранение результата в БД...", taskID)

	result := &model.GenerationResult{
		ID:             taskID,
		UserID:         userID,
		PromptType:     promptType,
		GeneratedText:  processedResult,
		ProcessingTime: processingTime,
		CreatedAt:      createdAt,
		CompletedAt:    completedAt,
		Error:          errorMsg,
	}

	saveCtx, saveCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer saveCancel()
	saveErr := h.resultRepo.Save(saveCtx, result)
	if saveErr != nil {
		log.Printf("[TaskID: %s] Ошибка сохранения результата в БД: %v", taskID, saveErr)
		return fmt.Errorf("ошибка сохранения результата в БД: %w", saveErr)
	}

	log.Printf("[TaskID: %s] Отправка уведомления...", taskID)
	notificationPayload := messaging.NotificationPayload{
		TaskID:           taskID,
		UserID:           userID,
		PromptType:       promptType,
		StoryConfigID:    storyConfigID,
		PublishedStoryID: publishedStoryID,
		StateHash:        stateHash,
	}
	if errorMsg != "" {
		notificationPayload.Status = messaging.NotificationStatusError
		notificationPayload.ErrorDetails = errorMsg
	} else {
		notificationPayload.Status = messaging.NotificationStatusSuccess
		notificationPayload.GeneratedText = processedResult
	}

	notifyCtx, notifyCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer notifyCancel()
	notifyErr := h.notifier.Notify(notifyCtx, notificationPayload)
	if notifyErr != nil {
		log.Printf("[TaskID: %s] ВНИМАНИЕ: Не удалось отправить уведомление: %v", taskID, notifyErr)
	}

	if processingErr != nil {
		log.Printf("[TaskID: %s] Ошибка обработки (processingErr), сырой ответ AI перед отправкой уведомления: '%s'", taskID, aiResponse)
		return fmt.Errorf("задача завершилась с ошибкой подготовки/AI (сохранено, уведомление отправлено/ошибка логирована): %w", processingErr)
	}

	log.Printf("[TaskID: %s] Задача успешно обработана, сохранена и уведомление отправлено за %v.", taskID, completedAt.Sub(createdAt))
	return nil
}
