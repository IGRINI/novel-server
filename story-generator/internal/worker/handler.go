package worker

import (
	"bytes" // Для рендеринга шаблона
	"context"
	"encoding/json"
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
	"path/filepath"
	"text/template" // Для шаблонизации промтов
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
	// Счетчик вызовов AI API
	aiCallsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "story_generator_ai_calls_total",
		Help: "Total number of AI API calls made.",
	})
	// Счетчик ошибок AI API
	aiErrorsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "story_generator_ai_errors_total",
		Help: "Total number of errors encountered during AI API calls.",
	})
	// Гистограмма длительности вызовов AI API
	aiCallDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "story_generator_ai_call_duration_seconds",
		Help:    "Duration of AI API calls.",
		Buckets: prometheus.ExponentialBuckets(0.1, 2, 10), // Базовые бакеты: 0.1s, 0.2s, 0.4s ... ~8.5min
	})
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
	// <<< Метрика: Счетчик полученных задач >>>
	// (Перенесено в main.go, где задача фактически получается из очереди)

	log.Printf("[TaskID: %s] Обработка задачи: UserID=%s, PromptType=%s, InputData=%v",
		payload.TaskID, payload.UserID, payload.PromptType, payload.InputData)

	fullStartTime := time.Now()
	createdAt := fullStartTime

	// Объявляем переменные для результатов и ошибок
	var finalSystemPrompt string
	var aiResponse string
	var err error
	var processingTime time.Duration
	var completedAt time.Time

	// --- Этап 1-2: Загрузка и рендеринг промта ---
	finalSystemPrompt, err = h.preparePrompt(payload)
	if err != nil {
		log.Printf("[TaskID: %s] Ошибка подготовки промта: %v", payload.TaskID, err)
		completedAt = time.Now()
		processingTime = completedAt.Sub(fullStartTime)
		// <<< Метрика: Задача с ошибкой (подготовка промта) >>>
		tasksFailedTotal.WithLabelValues("prompt_preparation").Inc()
		// <<< Метрика: Время обработки задачи (ошибка) >>>
		taskProcessingDuration.Observe(processingTime.Seconds())
		// Сразу вызываем сохранение и уведомление с ошибкой
		return h.saveAndNotifyResult(payload, "", err, createdAt, completedAt, processingTime)
	}

	// --- Этап 3: Вызов AI API с ретраями ---
	userInput := payload.UserInput
	baseDelay := h.baseRetryDelay

	for attempt := 1; attempt <= h.maxAttempts; attempt++ {
		log.Printf("[TaskID: %s] Вызов AI API (Попытка %d/%d)...", payload.TaskID, attempt, h.maxAttempts)
		aiStartTime := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), h.aiTimeout)

		// <<< Метрика: Счетчик вызовов AI >>>
		aiCallsTotal.Inc()

		aiResponse, err = h.aiClient.GenerateText(ctx, finalSystemPrompt, userInput)
		cancel()

		// <<< Метрика: Время вызова AI >>>
		aiDuration := time.Since(aiStartTime)
		aiCallDuration.Observe(aiDuration.Seconds())
		processingTime = aiDuration // Обновляем время обработки последним вызовом

		if err == nil {
			log.Printf("[TaskID: %s] AI API успешно ответил (Попытка %d).", payload.TaskID, attempt)
			// <<< Метрика: Успешная задача >>>
			// (Инкрементируется в конце, после сохранения)
			break
		}

		log.Printf("[TaskID: %s] Ошибка вызова AI API (Попытка %d/%d): %v", payload.TaskID, attempt, h.maxAttempts, err)

		if attempt == h.maxAttempts {
			log.Printf("[TaskID: %s] Достигнуто максимальное количество попыток (%d) вызова AI.", payload.TaskID, h.maxAttempts)
			// <<< Метрика: Задача с ошибкой (ошибка AI после ретраев) >>>
			// (Инкрементируется ниже, после цикла)
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
	totalDuration := completedAt.Sub(fullStartTime) // Общее время от начала до конца

	// <<< Метрика: Время обработки задачи (общее) >>>
	taskProcessingDuration.Observe(totalDuration.Seconds())

	// Проверяем, была ли ошибка AI после всех попыток
	if err != nil {
		// <<< Метрика: Задача с ошибкой (ошибка AI) >>>
		tasksFailedTotal.WithLabelValues("ai_error").Inc()
	}

	// --- Этап 4-6: Сохранение и уведомление ---
	saveErr := h.saveAndNotifyResult(payload, aiResponse, err, createdAt, completedAt, processingTime) // 'err' здесь - это ошибка AI или nil

	// Определяем финальный статус задачи для метрик
	if saveErr != nil {
		// Ошибка сохранения - это отдельная категория ошибки задачи
		tasksFailedTotal.WithLabelValues("save_error").Inc()
		// Возвращаем ошибку сохранения, чтобы сообщение было nack-нуто
		return saveErr
	} else if err != nil {
		// Ошибка была на этапе AI (уже посчитана выше), но сохранение прошло успешно.
		// Возвращаем исходную ошибку AI, чтобы сообщение было nack-нуто.
		return err
	} else {
		// Все прошло успешно (и AI, и сохранение)
		tasksSucceededTotal.Inc()
		return nil // Возвращаем nil для ack
	}
}

// preparePrompt загружает и рендерит шаблон промта
func (h *TaskHandler) preparePrompt(payload messaging.GenerationTaskPayload) (string, error) {
	log.Printf("[TaskID: %s] Загрузка и рендеринг промта...", payload.TaskID)
	promptFilePath := filepath.Join(h.PromptsDir, string(payload.PromptType)+".md")
	systemPromptBytes, readErr := os.ReadFile(promptFilePath)
	if readErr != nil {
		return "", fmt.Errorf("ошибка чтения файла промта '%s': %w", promptFilePath, readErr)
	}
	systemPrompt := string(systemPromptBytes)

	tmpl, parseErr := template.New("prompt").Parse(systemPrompt)
	if parseErr != nil {
		return "", fmt.Errorf("ошибка парсинга шаблона промта '%s': %w", payload.PromptType, parseErr)
	}
	var renderedPrompt bytes.Buffer
	execErr := tmpl.Execute(&renderedPrompt, payload.InputData)
	if execErr != nil {
		return "", fmt.Errorf("ошибка выполнения шаблона промта '%s': %w", payload.PromptType, execErr)
	}
	finalSystemPrompt := renderedPrompt.String()
	log.Printf("[TaskID: %s] Промт успешно подготовлен.", payload.TaskID)
	return finalSystemPrompt, nil
}

// saveAndNotifyResult сохраняет результат (или ошибку) в БД и отправляет уведомление.
// Возвращает ошибку, если задача должна быть nack-нута (ошибка AI/подготовки/сохранения).
func (h *TaskHandler) saveAndNotifyResult(
	payload messaging.GenerationTaskPayload,
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

	log.Printf("[TaskID: %s] Сохранение результата в БД...", payload.TaskID)
	inputDataJSON, jsonErr := json.Marshal(payload.InputData)
	if jsonErr != nil {
		log.Printf("[TaskID: %s] КРИТИЧЕСКАЯ ОШИБКА: Не удалось сериализовать InputData в JSON: %v", payload.TaskID, jsonErr)
		// <<< Метрика: Задача с ошибкой (ошибка JSON) >>>
		tasksFailedTotal.WithLabelValues("json_marshal_error").Inc()
		if errorMsg != "" {
			errorMsg = fmt.Sprintf("%s; КРИТИЧЕСКАЯ ОШИБКА: %v", errorMsg, jsonErr)
		} else {
			errorMsg = fmt.Sprintf("КРИТИЧЕСКАЯ ОШИБКА: %v", jsonErr)
		}
		inputDataJSON = []byte("null")
	}

	result := &model.GenerationResult{
		ID:             payload.TaskID,
		UserID:         payload.UserID,
		PromptType:     payload.PromptType,
		InputData:      inputDataJSON,
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
		log.Printf("[TaskID: %s] Ошибка сохранения результата в БД: %v", payload.TaskID, saveErr)
		// <<< Метрика: Задача с ошибкой (ошибка сохранения) >>>
		// (Дублируется? Нет, здесь ошибка именно в *момент* сохранения)
		// Мы уже инкрементировали save_error в Handle, если saveErr не nil.
		// Поэтому здесь не инкрементируем, чтобы не задвоить.
		if errorMsg != "" {
			errorMsg = fmt.Sprintf("%s; Ошибка сохранения: %v", errorMsg, saveErr)
		} else {
			errorMsg = fmt.Sprintf("Ошибка сохранения: %v", saveErr)
		}
		// Возвращаем ошибку сохранения, т.к. она критична
		// (Уведомление ниже не будет отправлено в этом случае)
		return fmt.Errorf("ошибка сохранения результата в БД: %w", saveErr)
	}

	log.Printf("[TaskID: %s] Отправка уведомления...", payload.TaskID)
	notificationPayload := messaging.NotificationPayload{
		TaskID:     payload.TaskID,
		UserID:     payload.UserID,
		PromptType: payload.PromptType,
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
		log.Printf("[TaskID: %s] ВНИМАНИЕ: Не удалось отправить уведомление: %v", payload.TaskID, notifyErr)
	}

	// Если была ошибка AI, подготовки или JSON, но сохранение прошло успешно,
	// возвращаем исходную ошибку, чтобы инициировать Nack.
	// Метрики для этих ошибок уже инкрементированы в Handle.
	if processingErr != nil {
		return fmt.Errorf("задача завершилась с ошибкой подготовки/AI (сохранено, уведомление отправлено/ошибка логирована): %w", processingErr)
	}
	if jsonErr != nil { // Ошибка была только при сериализации JSON
		// Метрика json_marshal_error уже инкрементирована выше
		return fmt.Errorf("задача завершилась с критической ошибкой JSON (сохранено как null, уведомление отправлено/ошибка логирована): %w", jsonErr)
	}

	log.Printf("[TaskID: %s] Задача успешно обработана, сохранена и уведомление отправлено за %v.", payload.TaskID, completedAt.Sub(createdAt))
	return nil // Успешное завершение
}
