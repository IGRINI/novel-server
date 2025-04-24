package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"

	"novel-server/gameplay-service/internal/config"
	interfaces "novel-server/shared/interfaces"
	sharedMessaging "novel-server/shared/messaging"
)

// Типы обновлений для ClientStoryUpdate
const (
	UpdateTypeDraft = "draft_update"
	UpdateTypeStory = "story_update"
)

// --- NotificationProcessor ---

// NotificationProcessor обрабатывает логику уведомлений.
// Вынесен в отдельную структуру для тестируемости.
type NotificationProcessor struct {
	repo                       interfaces.StoryConfigRepository     // Используем shared интерфейс
	publishedRepo              interfaces.PublishedStoryRepository  // !!! ДОБАВЛЕНО: Для PublishedStory
	sceneRepo                  interfaces.StorySceneRepository      // !!! ДОБАВЛЕНО: Для StoryScene
	playerGameStateRepo        interfaces.PlayerGameStateRepository // <<< ДОБАВЛЕНО: Для PlayerGameState
	imageReferenceRepo         interfaces.ImageReferenceRepository  // <<< ИСПОЛЬЗУЕМ ИНТЕРФЕЙС
	clientPub                  ClientUpdatePublisher                // Для отправки обновлений клиенту
	taskPub                    TaskPublisher                        // !!! ДОБАВЛЕНО: Для отправки новых задач генерации
	pushPub                    PushNotificationPublisher            // <<< Добавляем издателя push-уведомлений
	characterImageTaskPub      CharacterImageTaskPublisher          // <<< ДОБАВЛЕНО: Для отправки задач генерации изображений
	characterImageTaskBatchPub CharacterImageTaskBatchPublisher     // <<< ДОБАВЛЕНО: Для отправки батчей задач генерации изображений
	logger                     *zap.Logger                          // <<< ДОБАВЛЕНО
	cfg                        *config.Config                       // <<< ДОБАВЛЕНО: Доступ к конфигурации
}

// NewNotificationProcessor создает новый экземпляр NotificationProcessor.
func NewNotificationProcessor(
	repo interfaces.StoryConfigRepository, // Используем shared интерфейс
	publishedRepo interfaces.PublishedStoryRepository,
	sceneRepo interfaces.StorySceneRepository, // !!! Добавлено sceneRepo
	playerGameStateRepo interfaces.PlayerGameStateRepository, // <<< ДОБАВЛЕНО
	imageReferenceRepo interfaces.ImageReferenceRepository, // <<< ИСПОЛЬЗУЕМ ИНТЕРФЕЙС
	clientPub ClientUpdatePublisher,
	taskPub TaskPublisher,
	pushPub PushNotificationPublisher,
	characterImageTaskPub CharacterImageTaskPublisher, // <<< ДОБАВЛЕНО
	characterImageTaskBatchPub CharacterImageTaskBatchPublisher, // <<< ДОБАВЛЕНО
	logger *zap.Logger, // <<< ДОБАВЛЕНО
	cfg *config.Config, // <<< ДОБАВЛЕНО: Принимаем конфиг
) *NotificationProcessor {
	// <<< ДОБАВЛЕНО: Проверка на nil >>>
	if imageReferenceRepo == nil {
		logger.Fatal("imageReferenceRepo cannot be nil for NotificationProcessor")
	}
	if characterImageTaskPub == nil {
		logger.Fatal("characterImageTaskPub cannot be nil for NotificationProcessor")
	}
	if logger == nil {
		// Provide a default logger if nil is passed, although ideally it should always be provided.
		logger = zap.NewNop() // Or initialize a default production logger
		logger.Warn("Nil logger passed to NewNotificationProcessor, using No-op logger.")
	}
	if cfg == nil { // <<< ДОБАВЛЕНО: Проверка cfg
		logger.Fatal("cfg cannot be nil for NotificationProcessor")
	}
	return &NotificationProcessor{
		repo:                       repo,
		publishedRepo:              publishedRepo,
		sceneRepo:                  sceneRepo,
		playerGameStateRepo:        playerGameStateRepo,
		imageReferenceRepo:         imageReferenceRepo,
		clientPub:                  clientPub,
		taskPub:                    taskPub,
		pushPub:                    pushPub,
		characterImageTaskPub:      characterImageTaskPub,
		characterImageTaskBatchPub: characterImageTaskBatchPub,
		logger:                     logger,
		cfg:                        cfg, // <<< ДОБАВЛЕНО
	}
}

// Process обрабатывает одно сообщение из очереди (может быть NotificationPayload или CharacterImageResultPayload)
// <<< ИЗМЕНЕНИЕ: Сигнатура теперь принимает amqp.Delivery >>>
func (p *NotificationProcessor) Process(ctx context.Context, delivery amqp.Delivery) error {
	// <<< ИЗМЕНЕНИЕ: Логируем начало обработки Delivery >>>
	p.logger.Info("Processing delivery", zap.String("correlation_id", delivery.CorrelationId), zap.Uint64("delivery_tag", delivery.DeliveryTag))

	// Попытка 1: Обработать как NotificationPayload
	var notification sharedMessaging.NotificationPayload
	errNotification := json.Unmarshal(delivery.Body, &notification)

	if errNotification == nil {
		// Успешно распарсили как NotificationPayload
		p.logger.Info("Processing as NotificationPayload",
			zap.String("task_id", notification.TaskID),
			zap.String("prompt_type", string(notification.PromptType)),
			zap.String("correlation_id", delivery.CorrelationId),
		)
		// Вызываем внутренний метод для обработки NotificationPayload
		errProcess := p.processNotificationPayloadInternal(ctx, notification)
		if errProcess != nil {
			p.logger.Error("Error processing NotificationPayload",
				zap.String("task_id", notification.TaskID),
				zap.Error(errProcess),
				zap.String("correlation_id", delivery.CorrelationId),
			)
			// Nack(false, false) будет вызван в StartConsuming
			return errProcess // Возвращаем ошибку, чтобы вызвать Nack
		}
		p.logger.Info("NotificationPayload processed successfully",
			zap.String("task_id", notification.TaskID),
			zap.String("correlation_id", delivery.CorrelationId),
		)
		// Ack будет вызван в StartConsuming
		return nil // Успех
	}

	// Попытка 2: Обработать как CharacterImageResultPayload
	var imageResult sharedMessaging.CharacterImageResultPayload
	errImageResult := json.Unmarshal(delivery.Body, &imageResult)

	if errImageResult == nil {
		// Успешно распарсили как CharacterImageResultPayload
		p.logger.Info("Processing as CharacterImageResultPayload",
			zap.String("task_id", imageResult.TaskID), // Используем TaskID из imageResult
			zap.String("image_reference", imageResult.ImageReference),
			zap.String("correlation_id", delivery.CorrelationId),
		)
		// Вызываем внутренний метод для обработки CharacterImageResultPayload
		errProcess := p.processImageResult(ctx, imageResult)
		if errProcess != nil {
			p.logger.Error("Error processing CharacterImageResultPayload",
				zap.String("task_id", imageResult.TaskID),
				zap.Error(errProcess),
				zap.String("correlation_id", delivery.CorrelationId),
			)
			// Nack(false, false) будет вызван в StartConsuming
			return errProcess // Возвращаем ошибку, чтобы вызвать Nack
		}
		p.logger.Info("CharacterImageResultPayload processed successfully",
			zap.String("task_id", imageResult.TaskID),
			zap.String("correlation_id", delivery.CorrelationId),
		)
		// Ack будет вызван в StartConsuming
		return nil // Успех
	}

	// Если не удалось распарсить ни как NotificationPayload, ни как CharacterImageResultPayload
	p.logger.Error("Failed to unmarshal message body into known payload types",
		zap.Error(errNotification),
		zap.Error(errImageResult),
		zap.String("correlation_id", delivery.CorrelationId),
		zap.ByteString("body", delivery.Body), // Логируем тело сообщения для отладки
	)
	// Nack(false, false) будет вызван в StartConsuming
	return fmt.Errorf("unknown message format: failed to parse as NotificationPayload (%v) or CharacterImageResultPayload (%v)", errNotification, errImageResult)
}

// processNotificationPayloadInternal обрабатывает задачи генерации текста
func (p *NotificationProcessor) processNotificationPayloadInternal(ctx context.Context, notification sharedMessaging.NotificationPayload) error {
	taskID := notification.TaskID
	// Определяем ID и тип задачи
	var isStoryConfigTask bool
	var storyConfigID uuid.UUID
	var publishedStoryID uuid.UUID
	var parseIDErr error

	// <<< ИСПРАВЛЕНИЕ: Проверка строк >>>
	if notification.StoryConfigID != "" {
		storyConfigID, parseIDErr = uuid.Parse(notification.StoryConfigID)
		if parseIDErr == nil {
			isStoryConfigTask = true
		}
	}
	if !isStoryConfigTask && notification.PublishedStoryID != "" {
		publishedStoryID, parseIDErr = uuid.Parse(notification.PublishedStoryID) // <<< ИСПРАВЛЕНИЕ: Парсим ID здесь >>>
	}

	// <<< ИСПРАВЛЕНИЕ: Проверка ошибки парсинга ID >>>
	if parseIDErr != nil || (storyConfigID == uuid.Nil && publishedStoryID == uuid.Nil) {
		p.logger.Error("Invalid or missing ID in NotificationPayload", zap.String("task_id", taskID), zap.String("story_config_id", notification.StoryConfigID), zap.String("published_story_id", notification.PublishedStoryID), zap.Error(parseIDErr)) // <<< Добавляем parseIDErr в лог
		return fmt.Errorf("invalid or missing ID in notification payload: %w", parseIDErr)
	}

	switch notification.PromptType {
	case sharedMessaging.PromptTypeNarrator:
		if !isStoryConfigTask {
			p.logger.Error("Narrator received without StoryConfigID", zap.String("task_id", taskID), zap.String("published_story_id", notification.PublishedStoryID)) // Используем строку из уведомления для лога
			return fmt.Errorf("invalid notification: Narrator without StoryConfigID")
		}
		// <<< ИЗМЕНЕНИЕ: Передаем распарсенный storyConfigID >>>
		return p.handleNarratorNotification(ctx, notification, storyConfigID) // Вызов функции из handle_narrator.go

	case sharedMessaging.PromptTypeNovelSetup:
		if isStoryConfigTask {
			p.logger.Error("NovelSetup received with StoryConfigID", zap.String("task_id", taskID), zap.String("story_config_id", storyConfigID.String()))
			return fmt.Errorf("invalid notification: Setup with StoryConfigID")
		}
		// <<< ИЗМЕНЕНИЕ: Передаем распарсенный publishedStoryID >>>
		return p.handleNovelSetupNotification(ctx, notification, publishedStoryID) // Вызов функции из handle_setup.go

	case sharedMessaging.PromptTypeNovelFirstSceneCreator, sharedMessaging.PromptTypeNovelCreator, sharedMessaging.PromptTypeNovelGameOverCreator:
		if isStoryConfigTask {
			p.logger.Error("Scene/GameOver received with StoryConfigID", zap.String("task_id", taskID), zap.String("prompt_type", string(notification.PromptType)), zap.String("story_config_id", storyConfigID.String()))
			return fmt.Errorf("invalid notification: %s with StoryConfigID", notification.PromptType)
		}
		if notification.StateHash == "" {
			p.logger.Error("Scene/GameOver received without StateHash", zap.String("task_id", taskID), zap.String("prompt_type", string(notification.PromptType)), zap.String("published_story_id", publishedStoryID.String()))
			return fmt.Errorf("invalid notification: %s without StateHash", notification.PromptType)
		}
		// <<< ИЗМЕНЕНИЕ: Передаем распарсенный publishedStoryID >>>
		return p.handleSceneGenerationNotification(ctx, notification, publishedStoryID) // Вызов функции из handle_scene.go

	default:
		p.logger.Error("Unknown PromptType in NotificationPayload. Nacking message.", zap.String("task_id", taskID), zap.String("prompt_type", string(notification.PromptType)))
		return fmt.Errorf("unknown PromptType: %s", notification.PromptType)
	}
}

// processImageResult обрабатывает результат генерации изображения
func (p *NotificationProcessor) processImageResult(ctx context.Context, result sharedMessaging.CharacterImageResultPayload) error {
	logFields := []zap.Field{
		zap.String("task_id", result.TaskID),
		zap.String("image_reference", result.ImageReference),
	}
	p.logger.Info("Processing CharacterImageResultPayload", logFields...)

	if !result.Success { // Check the Success field
		errMsg := "Unknown error"
		if result.ErrorMessage != nil {
			errMsg = *result.ErrorMessage
		}
		p.logger.Error("Image generation failed for reference", append(logFields, zap.String("error", errMsg))...)
		// Ошибку обработали (залогировали), но сообщение подтверждаем, т.к. повторная обработка не поможет.
		return nil // Ack
	}

	if result.ImageURL == nil || *result.ImageURL == "" { // Check pointer and value
		p.logger.Error("Image generation succeeded but returned empty or nil URL", logFields...)
		// Это странная ситуация, логируем как ошибку, но подтверждаем.
		return nil // Ack
	}

	imageURL := *result.ImageURL // Dereference the pointer
	p.logger.Info("Image generated successfully, saving URL", append(logFields, zap.String("image_url", imageURL))...)

	dbCtx, cancel := context.WithTimeout(ctx, 10*time.Second) // Короткий таймаут для сохранения URL
	defer cancel()

	// Используем imageReferenceRepo для сохранения URL
	err := p.imageReferenceRepo.SaveOrUpdateImageReference(dbCtx, result.ImageReference, imageURL)
	if err != nil {
		p.logger.Error("Failed to save image URL for reference", append(logFields, zap.String("image_url", imageURL), zap.Error(err))...)
		// Не смогли сохранить URL. Возвращаем ошибку, чтобы сообщение было обработано повторно (nack).
		return fmt.Errorf("failed to save image URL for reference %s: %w", result.ImageReference, err)
	}

	p.logger.Info("Image URL saved successfully for reference", logFields...)
	return nil // Ack
}
