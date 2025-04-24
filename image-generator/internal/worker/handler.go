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
)

// Определяем метрики Prometheus
var (
	tasksProcessed = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "image_generator_tasks_processed_total",
			Help: "Total number of image generation tasks processed.",
		},
		[]string{"status"}, // "success", "error_generation", "error_publish"
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
		resultsChan := make(chan messaging.CharacterImageResultPayload, len(batchPayload.Tasks))

		for _, task := range batchPayload.Tasks {
			wg.Add(1)
			go func(t messaging.CharacterImageTaskPayload) {
				defer wg.Done()
				taskLog := log.With(
					zap.String("task_id", t.TaskID),
					zap.String("character_id", t.CharacterID),
					zap.String("image_reference", t.ImageReference),
				)
				taskLog.Info("Processing task from batch")

				// Замеряем время выполнения подзадачи
				taskStartTime := time.Now()

				// Вызываем сервис для генерации и сохранения изображения
				generationResult := h.imageService.GenerateAndStoreImage(context.Background(), t)
				taskDuration.Observe(time.Since(taskStartTime).Seconds()) // Наблюдаем длительность

				resultPayload := messaging.CharacterImageResultPayload{
					TaskID:         t.TaskID,
					UserID:         t.UserID,
					CharacterID:    t.CharacterID,
					ImageReference: t.ImageReference,
					ImageURL:       generationResult.ImageURL,
				}
				if generationResult.Error != nil {
					taskLog.Error("Failed to generate and store image for task", zap.Error(generationResult.Error))
					resultPayload.Error = generationResult.Error.Error()
					// Инкрементируем счетчики ошибок
					sanaApiErrors.Inc() // Пример: считаем как ошибку API
					saveErrors.Inc()    // Пример: считаем как ошибку сохранения (уточнить логику в imageService)
					tasksProcessed.WithLabelValues("error_generation").Inc()

				} else {
					// Инкрементируем счетчик успешных задач
					tasksProcessed.WithLabelValues("success").Inc()
				}
				resultsChan <- resultPayload
			}(task)
		}

		go func() {
			wg.Wait()
			close(resultsChan)
			log.Info("All tasks in batch processed")
		}()

		var publishErrorsEncountered bool
		for result := range resultsChan {
			resultLog := log.With(zap.String("task_id", result.TaskID), zap.String("character_id", result.CharacterID))
			if result.Error != "" {
				resultLog.Warn("Publishing error result for task from batch", zap.String("error", result.Error))
			} else {
				resultLog.Info("Publishing success result for task from batch", zap.String("image_url", result.ImageURL))
			}

			if pubErr := h.resultPublisher.Publish(ctx, result, msg.CorrelationId); pubErr != nil {
				resultLog.Error("Failed to publish result for task from batch", zap.Error(pubErr))
				publishErrorsEncountered = true
				// Инкрементируем счетчик ошибок публикации
				publishResultErrors.Inc()
			}
		}

		if publishErrorsEncountered {
			log.Warn("Finished processing batch with some result publishing errors.")
		} else {
			log.Info("Finished processing batch, all results published successfully.")
		}
		// Ack батча не зависит от метрик
		return true // Ack the batch message
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
	log = log.With(
		zap.String("user_id", taskPayload.UserID),
		zap.String("character_id", taskPayload.CharacterID),
		zap.String("image_reference", taskPayload.ImageReference),
	)

	// Замеряем время
	taskStartTime := time.Now()

	generationResult := h.imageService.GenerateAndStoreImage(context.Background(), taskPayload)
	taskDuration.Observe(time.Since(taskStartTime).Seconds()) // Наблюдаем длительность

	resultPayload := messaging.CharacterImageResultPayload{
		TaskID:         taskPayload.TaskID,
		UserID:         taskPayload.UserID,
		CharacterID:    taskPayload.CharacterID,
		ImageReference: taskPayload.ImageReference,
		ImageURL:       generationResult.ImageURL,
	}

	if generationResult.Error != nil {
		log.Error("Failed to generate and store image", zap.Error(generationResult.Error))
		resultPayload.Error = generationResult.Error.Error()
		// Инкрементируем счетчики ошибок
		sanaApiErrors.Inc() // Пример
		saveErrors.Inc()    // Пример
		tasksProcessed.WithLabelValues("error_generation").Inc()

		if pubErr := h.resultPublisher.Publish(ctx, resultPayload, msg.CorrelationId); pubErr != nil {
			log.Error("Failed to publish error result", zap.Error(pubErr), zap.Any("payload", resultPayload))
			// Инкрементируем счетчик ошибок публикации
			publishResultErrors.Inc()
			return false // Nack - не смогли опубликовать ошибку
		}
		log.Warn("Published error result for image generation task")
		return false // Nack - задача не выполнена, но ошибка опубликована
	}

	log.Info("Image generated and stored successfully", zap.String("image_url", generationResult.ImageURL))
	// Инкрементируем счетчик успешных задач
	tasksProcessed.WithLabelValues("success").Inc()

	if pubErr := h.resultPublisher.Publish(ctx, resultPayload, msg.CorrelationId); pubErr != nil {
		log.Error("Failed to publish success result", zap.Error(pubErr), zap.Any("payload", resultPayload))
		// Инкрементируем счетчик ошибок публикации
		publishResultErrors.Inc()
		return false // Nack - задача выполнена, но результат не опубликован
	}
	log.Info("Published success result for image generation task")

	return true // Ack - все успешно для одиночной задачи
}
