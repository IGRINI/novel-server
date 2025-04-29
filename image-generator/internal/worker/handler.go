package worker

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/push"
	"github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"

	"novel-server/image-generator/internal/service"
	"novel-server/shared/messaging"
	models "novel-server/shared/models"
)

// Определяем метрики Prometheus
var (
	tasksProcessed = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "image_generator_tasks_processed_total",
			Help: "Total number of image generation tasks processed.",
		},
		[]string{"status"}, // "success", "error_generation", "error_publish", "error_unmarshal"
	)
	taskDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "image_generator_task_duration_seconds",
		Help:    "Duration of image generation task processing.",
		Buckets: prometheus.LinearBuckets(0.1, 0.1, 10), // Пример: 0.1s, 0.2s, ..., 1s
	})
	sanaApiErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "image_generator_sana_api_errors_total",
		Help: "Total number of errors calling the SANA API.",
	})
	saveErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "image_generator_save_errors_total",
		Help: "Total number of errors saving the generated image.",
	})
	publishResultErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "image_generator_publish_result_errors_total",
		Help: "Total number of errors publishing task results.",
	})
)

// Helper function to get a pointer to a string
func ptrString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Helper function to safely dereference a string pointer for logging
func safeDerefString(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

// Handler обрабатывает входящие сообщения.
type Handler struct {
	logger          *zap.Logger
	imageService    service.ImageGenerationService
	resultPublisher messaging.Publisher // Обязательно для отправки CharacterImageResultPayload
	pusher          *push.Pusher        // Pusher для метрик
}

// NewHandler создает новый экземпляр Handler.
func NewHandler(
	logger *zap.Logger,
	imageService service.ImageGenerationService,
	resultPublisher messaging.Publisher,
	pushGatewayURL string, // URL для Pushgateway
) *Handler {
	if resultPublisher == nil {
		logger.Fatal("Result publisher cannot be nil for image generator handler")
	}

	// Инициализация Pusher
	hostname, _ := os.Hostname() // Используем hostname для instance label
	pusher := push.New(pushGatewayURL, "image-generator").
		Grouping("instance", hostname). // Группируем по instance
		Gatherer(prometheus.DefaultGatherer)

	logger.Info("Prometheus Pusher initialized", zap.String("url", pushGatewayURL), zap.String("instance", hostname))

	return &Handler{
		logger:          logger,
		imageService:    imageService,
		resultPublisher: resultPublisher,
		pusher:          pusher,
	}
}

// HandleDelivery обрабатывает сообщения. Поддерживает как одиночные CharacterImageTaskPayload,
// так и батчи CharacterImageTaskBatchPayload для обратной совместимости и гибкости.
// Возвращает true, если исходное сообщение должно быть подтверждено (ack).
func (h *Handler) HandleDelivery(ctx context.Context, msg amqp091.Delivery) bool {
	defer func() {
		if err := h.pusher.Push(); err != nil {
			h.logger.Error("Failed to push metrics to Pushgateway", zap.Error(err))
		} else {
			h.logger.Debug("Metrics pushed to Pushgateway")
		}
	}()

	// Пытаемся распарсить как батч
	var batchPayload messaging.CharacterImageTaskBatchPayload
	errBatch := json.Unmarshal(msg.Body, &batchPayload)

	if errBatch == nil && len(batchPayload.Tasks) > 0 {
		// Успешно распарсили как непустой батч
		log := h.logger.With(zap.String("batch_id", batchPayload.BatchID), zap.Int("task_count", len(batchPayload.Tasks)), zap.String("correlation_id", msg.CorrelationId))
		log.Info("Received character image generation task batch")

		var wg sync.WaitGroup
		resultsChan := make(chan messaging.NotificationPayload, len(batchPayload.Tasks))

		for _, task := range batchPayload.Tasks {
			wg.Add(1)
			go func(t messaging.CharacterImageTaskPayload) {
				defer wg.Done()
				taskLog := log.With(
					zap.String("task_id", t.TaskID),
					zap.String("character_id", t.CharacterID.String()), // Use .String()
					zap.String("image_reference", t.ImageReference),
				)
				taskLog.Info("Processing task from batch")

				// Замеряем время выполнения подзадачи
				taskStartTime := time.Now()

				// Вызываем сервис для генерации и сохранения изображения
				generationResult := h.imageService.GenerateAndStoreImage(context.Background(), t)
				taskDuration.Observe(time.Since(taskStartTime).Seconds()) // Наблюдаем длительность

				// Создаем result payload
				resultPayload := messaging.CharacterImageResultPayload{
					TaskID:           t.TaskID,
					PublishedStoryID: t.PublishedStoryID,
					ImageReference:   t.ImageReference,
					Success:          false, // Default to false, set to true on success
				}

				if generationResult.Error != nil {
					taskLog.Error("Failed to generate and store image for task", zap.Error(generationResult.Error))
					// Используем ErrorMessage и передаем указатель на строку ошибки
					errMsg := generationResult.Error.Error()
					resultPayload.ErrorMessage = &errMsg

					// Инкрементируем счетчики ошибок
					sanaApiErrors.Inc() // Пример: считаем как ошибку API
					saveErrors.Inc()    // Пример: считаем как ошибку сохранения (уточнить логику в imageService)
					tasksProcessed.WithLabelValues("error_generation").Inc()

				} else {
					resultPayload.Success = true
					// Передаем указатель на URL изображения
					if generationResult.ImageURL != "" {
						resultPayload.ImageURL = &generationResult.ImageURL
					}
					// Инкрементируем счетчик успешных задач
					tasksProcessed.WithLabelValues("success").Inc()
				}

				// Создаем NotificationPayload
				notificationPayload := messaging.NotificationPayload{
					TaskID:           t.TaskID,
					PublishedStoryID: t.PublishedStoryID.String(),
					UserID:           t.UserID,
					PromptType:       determinePromptTypeFromResult(resultPayload),
					ImageReference:   resultPayload.ImageReference,
				}
				if resultPayload.Success {
					notificationPayload.Status = messaging.NotificationStatusSuccess
					notificationPayload.ImageURL = resultPayload.ImageURL
				} else {
					notificationPayload.Status = messaging.NotificationStatusError
					if resultPayload.ErrorMessage != nil {
						notificationPayload.ErrorDetails = *resultPayload.ErrorMessage
					}
				}

				resultsChan <- notificationPayload // Отправляем NotificationPayload в канал
			}(task)
		}

		go func() {
			wg.Wait()
			close(resultsChan)
			log.Info("All tasks in batch processed")
		}()

		var publishErrorsEncountered bool
		for notificationResult := range resultsChan {
			resultLog := log.With(zap.String("task_id", notificationResult.TaskID), zap.String("prompt_type", string(notificationResult.PromptType)))
			if notificationResult.Status == messaging.NotificationStatusError {
				resultLog.Warn("Publishing error notification for task from batch", zap.String("error", notificationResult.ErrorDetails))
			} else {
				resultLog.Info("Publishing success notification for task from batch")
			}

			if pubErr := h.resultPublisher.Publish(ctx, notificationResult, msg.CorrelationId); pubErr != nil {
				resultLog.Error("Failed to publish notification result for task from batch", zap.Error(pubErr))
				publishErrorsEncountered = true
				// Инкрементируем счетчик ошибок публикации
				publishResultErrors.Inc()
				tasksProcessed.WithLabelValues("error_publish").Inc()
			}
		}

		if publishErrorsEncountered {
			log.Warn("Finished processing batch with some result publishing errors.")
		} else {
			log.Info("Finished processing batch, all results published successfully.")
		}
		// Ack батча не зависит от ошибок публикации отдельных результатов
		return true // Ack the original batch message
	}

	// Если не удалось распарсить как батч, пытаемся как одиночную задачу
	var taskPayload messaging.CharacterImageTaskPayload
	if err := json.Unmarshal(msg.Body, &taskPayload); err != nil {
		h.logger.Error("Failed to unmarshal message body as Batch or Single Task",
			zap.Error(errBatch),
			zap.Error(err),
			zap.String("correlation_id", msg.CorrelationId),
			zap.ByteString("body", msg.Body))
		// TODO: Метрика для невалидных сообщений?
		tasksProcessed.WithLabelValues("error_unmarshal").Inc() // Добавим новый статус
		return false                                            // Nack - неизвестный формат
	}

	// Обработка как одиночной задачи
	log := h.logger.With(zap.String("task_id", taskPayload.TaskID), zap.String("correlation_id", msg.CorrelationId))
	log.Info("Received single character image generation task")
	// Используем .String() для CharacterID и PublishedStoryID
	log = log.With(
		zap.String("character_id", taskPayload.CharacterID.String()),
		zap.String("published_story_id", taskPayload.PublishedStoryID.String()),
		zap.String("image_reference", taskPayload.ImageReference),
	)

	// Замеряем время
	taskStartTime := time.Now()

	generationResult := h.imageService.GenerateAndStoreImage(context.Background(), taskPayload)
	taskDuration.Observe(time.Since(taskStartTime).Seconds()) // Наблюдаем длительность

	// Создаем result payload
	resultPayload := messaging.CharacterImageResultPayload{
		TaskID:           taskPayload.TaskID,
		PublishedStoryID: taskPayload.PublishedStoryID,
		ImageReference:   taskPayload.ImageReference,
		Success:          false, // Default to false
	}

	if generationResult.Error != nil {
		log.Error("Failed to generate and store image", zap.Error(generationResult.Error))
		// Используем ErrorMessage и передаем указатель
		errMsg := generationResult.Error.Error()
		resultPayload.ErrorMessage = &errMsg

		// Инкрементируем счетчики ошибок
		sanaApiErrors.Inc() // Пример
		saveErrors.Inc()    // Пример
		tasksProcessed.WithLabelValues("error_generation").Inc()

		// Создаем NotificationPayload
		notificationPayload := messaging.NotificationPayload{
			TaskID:           taskPayload.TaskID,
			PublishedStoryID: taskPayload.PublishedStoryID.String(),
			UserID:           taskPayload.UserID,
			PromptType:       determinePromptTypeFromResult(resultPayload),
			ImageReference:   resultPayload.ImageReference,
		}
		notificationPayload.Status = messaging.NotificationStatusError
		notificationPayload.ErrorDetails = *resultPayload.ErrorMessage

		// Публикуем результат NotificationPayload
		if pubErr := h.resultPublisher.Publish(ctx, notificationPayload, msg.CorrelationId); pubErr != nil {
			log.Error("Failed to publish notification result for single task", zap.Error(pubErr))
			// Инкрементируем метрики
			publishResultErrors.Inc()
			tasksProcessed.WithLabelValues("error_publish").Inc()
			return false // Nack - ошибка публикации
		}

		log.Warn("Successfully processed and published notification for single task")
		return true // Ack
	} else {
		resultPayload.Success = true
		if generationResult.ImageURL != "" {
			resultPayload.ImageURL = &generationResult.ImageURL
		}
		// Инкрементируем метрику успеха
		tasksProcessed.WithLabelValues("success").Inc()

		// Создаем NotificationPayload
		notificationPayload := messaging.NotificationPayload{
			TaskID:           taskPayload.TaskID,
			PublishedStoryID: taskPayload.PublishedStoryID.String(),
			UserID:           taskPayload.UserID,
			PromptType:       determinePromptTypeFromResult(resultPayload),
			ImageReference:   resultPayload.ImageReference,
		}
		notificationPayload.Status = messaging.NotificationStatusSuccess
		notificationPayload.ImageURL = resultPayload.ImageURL

		// Публикуем результат NotificationPayload
		if pubErr := h.resultPublisher.Publish(ctx, notificationPayload, msg.CorrelationId); pubErr != nil {
			log.Error("Failed to publish notification result for single task", zap.Error(pubErr))
			// Инкрементируем метрики
			publishResultErrors.Inc()
			tasksProcessed.WithLabelValues("error_publish").Inc()
			return false // Nack - ошибка публикации
		}

		log.Info("Successfully processed and published notification for single task")
		return true // Ack
	}
}

// determinePromptTypeFromResult определяет PromptType на основе ImageReference.
func determinePromptTypeFromResult(result messaging.CharacterImageResultPayload) models.PromptType {
	// Простое правило: если reference начинается с "ch_", то это персонаж,
	// если с "history_preview_", то это превью. Иначе - неизвестно.
	if len(result.ImageReference) > 3 && result.ImageReference[:3] == "ch_" {
		return models.PromptTypeCharacterImage
	} else if len(result.ImageReference) > 16 && result.ImageReference[:16] == "history_preview_" {
		return models.PromptTypeStoryPreviewImage
	}
	// По умолчанию или если не распознано
	return models.PromptType("unknown_image_type") // Возвращаем кастомный тип, чтобы не падало при валидации
}

// buildFullPrompt создает полный промпт, комбинируя базовый и суффикс стиля.
// <<< ИЗМЕНЕНО: Больше не добавляет суффикс из конфига >>>
func buildFullPrompt(basePrompt string) string {
	// Просто возвращаем базовый промпт
	return basePrompt
}

// handleGenerateImage обрабатывает одну задачу генерации изображения.
