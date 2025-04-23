package worker

import (
	"context"
	"encoding/json"

	"github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"

	"novel-server/image-generator/internal/service"
	"novel-server/shared/messaging"
)

// Handler обрабатывает входящие сообщения CharacterImageTaskPayload.
type Handler struct {
	logger          *zap.Logger
	imageService    service.ImageGenerationService
	resultPublisher messaging.Publisher // Обязательно для отправки CharacterImageResultPayload
}

// NewHandler создает новый экземпляр Handler.
func NewHandler(
	logger *zap.Logger,
	imageService service.ImageGenerationService,
	resultPublisher messaging.Publisher, // Должен быть инициализирован
) *Handler {
	if resultPublisher == nil {
		// В этой архитектуре отправка результата обязательна
		logger.Fatal("Result publisher cannot be nil for image generator handler")
	}
	return &Handler{
		logger:          logger,
		imageService:    imageService,
		resultPublisher: resultPublisher,
	}
}

// HandleDelivery обрабатывает CharacterImageTaskPayload.
// Возвращает true, если сообщение должно быть подтверждено (ack).
func (h *Handler) HandleDelivery(ctx context.Context, msg amqp091.Delivery) bool {
	log := h.logger.With(zap.String("correlation_id", msg.CorrelationId))
	log.Info("Received character image generation task")

	var taskPayload messaging.CharacterImageTaskPayload
	if err := json.Unmarshal(msg.Body, &taskPayload); err != nil {
		log.Error("Failed to unmarshal CharacterImageTaskPayload", zap.Error(err), zap.ByteString("body", msg.Body))
		// Сообщение невалидно, подтверждать не нужно (nack/reject)
		// В идеале - настроить DLQ
		return false
	}

	log = log.With(
		zap.String("user_id", taskPayload.UserID),
		zap.String("character_id", taskPayload.CharacterID),
		zap.String("image_reference", taskPayload.ImageReference),
		zap.String("task_id", taskPayload.TaskID),
	)

	// Вызываем сервис для генерации и сохранения изображения
	generationResult := h.imageService.GenerateAndStoreImage(
		context.Background(), // Или ctx, если нужно управление таймаутом извне
		taskPayload,          // Передаем всю структуру задачи
	)

	// Готовим сообщение с результатом
	resultPayload := messaging.CharacterImageResultPayload{
		TaskID:         taskPayload.TaskID, // Возвращаем ID исходной задачи
		UserID:         taskPayload.UserID,
		CharacterID:    taskPayload.CharacterID,
		ImageReference: taskPayload.ImageReference,
		ImageURL:       generationResult.ImageURL, // Будет пустым при ошибке
	}
	if generationResult.Error != nil {
		log.Error("Failed to generate and store image", zap.Error(generationResult.Error))
		resultPayload.Error = generationResult.Error.Error() // Записываем ошибку в результат
		// Не подтверждаем исходное сообщение, чтобы оно было обработано повторно (или ушло в DLQ)
		// Отправляем сообщение об ошибке
		if pubErr := h.resultPublisher.Publish(ctx, resultPayload, msg.CorrelationId); pubErr != nil {
			log.Error("Failed to publish error result", zap.Error(pubErr), zap.Any("payload", resultPayload))
			// Двойная ошибка - и генерация, и публикация. Не подтверждаем.
			return false
		}
		log.Warn("Published error result for image generation task")
		// Сообщение об ошибке отправлено, теперь исходное сообщение можно отбросить (nack/reject)
		// Но чтобы избежать бесконечных повторов, пока просто вернем false (ожидая DLQ или ручного вмешательства)
		return false // nack
	}

	// Успех: отправляем результат
	log.Info("Image generated and stored successfully", zap.String("image_url", generationResult.ImageURL))
	if pubErr := h.resultPublisher.Publish(ctx, resultPayload, msg.CorrelationId); pubErr != nil {
		log.Error("Failed to publish success result", zap.Error(pubErr), zap.Any("payload", resultPayload))
		// Ошибка публикации результата - серьезная проблема.
		// Основная задача (генерация) выполнена, но gameplay-service не узнает.
		// Не подтверждаем исходное сообщение, чтобы попытаться отправить результат снова.
		return false // nack
	}
	log.Info("Published success result for image generation task")

	// Все успешно, подтверждаем исходное сообщение
	return true // ack
}
