package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"

	"novel-server/gameplay-service/internal/config"
	interfaces "novel-server/shared/interfaces"
	sharedMessaging "novel-server/shared/messaging"
	sharedModels "novel-server/shared/models"
)

// Типы обновлений для ClientStoryUpdate
const (
	UpdateTypeDraft = "draft_update"
	UpdateTypeStory = "story_update"
)

// Регулярное выражение для извлечения UUID из ImageReference - БОЛЬШЕ НЕ НУЖНО
// var storyIDRegex = regexp.MustCompile(`[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}`)

// --- NotificationProcessor ---

// NotificationProcessor обрабатывает логику уведомлений.
// Вынесен в отдельную структуру для тестируемости.
type NotificationProcessor struct {
	repo                       interfaces.StoryConfigRepository     // Используем shared интерфейс
	publishedRepo              interfaces.PublishedStoryRepository  // !!! ДОБАВЛЕНО: Для PublishedStory
	sceneRepo                  interfaces.StorySceneRepository      // !!! ДОБАВЛЕНО: Для StoryScene
	playerGameStateRepo        interfaces.PlayerGameStateRepository // <<< ДОБАВЛЕНО: Для PlayerGameState
	imageReferenceRepo         interfaces.ImageReferenceRepository  // <<< ИСПОЛЬЗУЕМ ИНТЕРФЕЙС
	genResultRepo              interfaces.GenerationResultRepository
	clientPub                  ClientUpdatePublisher               // Для отправки обновлений клиенту
	taskPub                    TaskPublisher                       // !!! ДОБАВЛЕНО: Для отправки новых задач генерации
	pushPub                    PushNotificationPublisher           // <<< Добавляем издателя push-уведомлений
	characterImageTaskPub      CharacterImageTaskPublisher         // <<< ДОБАВЛЕНО: Для отправки задач генерации изображений
	characterImageTaskBatchPub CharacterImageTaskBatchPublisher    // <<< ДОБАВЛЕНО: Для отправки батчей задач генерации изображений
	logger                     *zap.Logger                         // <<< ДОБАВЛЕНО
	cfg                        *config.Config                      // <<< ДОБАВЛЕНО: Доступ к конфигурации
	playerProgressRepo         interfaces.PlayerProgressRepository // <<< ADDED: Dependency for progress updates
}

// NewNotificationProcessor создает новый экземпляр NotificationProcessor.
func NewNotificationProcessor(
	repo interfaces.StoryConfigRepository, // Используем shared интерфейс
	publishedRepo interfaces.PublishedStoryRepository,
	sceneRepo interfaces.StorySceneRepository, // !!! Добавлено sceneRepo
	playerGameStateRepo interfaces.PlayerGameStateRepository, // <<< ДОБАВЛЕНО
	imageReferenceRepo interfaces.ImageReferenceRepository, // <<< ИСПОЛЬЗУЕМ ИНТЕРФЕЙС
	genResultRepo interfaces.GenerationResultRepository,
	clientPub ClientUpdatePublisher,
	taskPub TaskPublisher,
	pushPub PushNotificationPublisher,
	characterImageTaskPub CharacterImageTaskPublisher, // <<< ДОБАВЛЕНО
	characterImageTaskBatchPub CharacterImageTaskBatchPublisher, // <<< ДОБАВЛЕНО
	logger *zap.Logger, // <<< ДОБАВЛЕНО
	cfg *config.Config, // <<< ДОБАВЛЕНО: Принимаем конфиг
	playerProgressRepo interfaces.PlayerProgressRepository, // <<< ADDED
) *NotificationProcessor {
	if genResultRepo == nil {
		logger.Fatal("genResultRepo cannot be nil for NotificationProcessor")
	}
	if imageReferenceRepo == nil {
		logger.Fatal("imageReferenceRepo cannot be nil for NotificationProcessor")
	}
	if characterImageTaskPub == nil {
		logger.Fatal("characterImageTaskPub cannot be nil for NotificationProcessor")
	}
	if logger == nil {
		logger = zap.NewNop()
		logger.Warn("Nil logger passed to NewNotificationProcessor, using No-op logger.")
	}
	if cfg == nil {
		logger.Fatal("cfg cannot be nil for NotificationProcessor")
	}
	return &NotificationProcessor{
		repo:                       repo,
		publishedRepo:              publishedRepo,
		sceneRepo:                  sceneRepo,
		playerGameStateRepo:        playerGameStateRepo,
		imageReferenceRepo:         imageReferenceRepo,
		genResultRepo:              genResultRepo,
		clientPub:                  clientPub,
		taskPub:                    taskPub,
		pushPub:                    pushPub,
		characterImageTaskPub:      characterImageTaskPub,
		characterImageTaskBatchPub: characterImageTaskBatchPub,
		logger:                     logger,
		cfg:                        cfg,
		playerProgressRepo:         playerProgressRepo,
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

	if errNotification != nil {
		// Если не удалось распарсить как NotificationPayload
		p.logger.Error("Failed to unmarshal message body as NotificationPayload",
			zap.Error(errNotification),
			zap.String("correlation_id", delivery.CorrelationId),
			zap.ByteString("body", delivery.Body), // Логируем тело сообщения для отладки
		)
		// Nack(false, false) будет вызван в StartConsuming
		return fmt.Errorf("unknown message format: failed to parse as NotificationPayload: %w", errNotification)
	}

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

	// <<< ИЗМЕНЕНО: Используем константы из sharedModels >>>
	switch notification.PromptType {
	case sharedModels.PromptTypeNarrator, sharedModels.PromptTypeNarratorReviser:
		if !isStoryConfigTask {
			p.logger.Error("Narrator/Reviser received without StoryConfigID", zap.String("task_id", taskID), zap.String("prompt_type", string(notification.PromptType)), zap.String("published_story_id", notification.PublishedStoryID))
			return fmt.Errorf("invalid notification: %s without StoryConfigID", notification.PromptType)
		}
		return p.handleNarratorNotification(ctx, notification, storyConfigID)

	case sharedModels.PromptTypeNovelSetup:
		if isStoryConfigTask {
			p.logger.Error("NovelSetup received with StoryConfigID", zap.String("task_id", taskID), zap.String("story_config_id", storyConfigID.String()))
			return fmt.Errorf("invalid notification: Setup with StoryConfigID")
		}
		return p.handleNovelSetupNotification(ctx, notification, publishedStoryID)

	case sharedModels.PromptTypeNovelFirstSceneCreator, sharedModels.PromptTypeNovelCreator, sharedModels.PromptTypeNovelGameOverCreator:
		if isStoryConfigTask {
			p.logger.Error("Scene/GameOver received with StoryConfigID", zap.String("task_id", taskID), zap.String("prompt_type", string(notification.PromptType)), zap.String("story_config_id", storyConfigID.String()))
			return fmt.Errorf("invalid notification: %s with StoryConfigID", notification.PromptType)
		}
		if notification.StateHash == "" {
			p.logger.Error("Scene/GameOver received without StateHash", zap.String("task_id", taskID), zap.String("prompt_type", string(notification.PromptType)), zap.String("published_story_id", publishedStoryID.String()))
			return fmt.Errorf("invalid notification: %s without StateHash", notification.PromptType)
		}
		return p.handleSceneGenerationNotification(ctx, notification, publishedStoryID)

	case sharedModels.PromptTypeCharacterImage, sharedModels.PromptTypeStoryPreviewImage:
		p.logger.Info("Processing image generation result notification",
			zap.String("task_id", taskID),
			zap.String("prompt_type", string(notification.PromptType)),
			zap.String("published_story_id_str", notification.PublishedStoryID),
			zap.String("image_reference", notification.ImageReference),
			zap.String("status", string(notification.Status)),
		)

		// Валидация ID истории
		publishedStoryID, err := uuid.Parse(notification.PublishedStoryID)
		if err != nil || publishedStoryID == uuid.Nil {
			p.logger.Error("Invalid or missing PublishedStoryID in image notification. Acking.",
				zap.String("task_id", taskID),
				zap.String("published_story_id_str", notification.PublishedStoryID),
				zap.Error(err),
			)
			return nil // Ack, так как повторная обработка не поможет
		}

		logFields := []zap.Field{
			zap.String("task_id", taskID),
			zap.String("image_reference", notification.ImageReference),
			zap.Stringer("publishedStoryID", publishedStoryID),
		}

		// Обработка ошибки генерации
		if notification.Status == sharedMessaging.NotificationStatusError {
			p.logger.Error("Image generation failed", append(logFields, zap.String("error_details", notification.ErrorDetails))...)
			// TODO: Возможно, стоит как-то помечать историю или reference как ошибочный?
			// Сейчас просто подтверждаем сообщение (Ack), так как ошибка уже произошла.
			return nil // Ack
		}

		// Обработка успешной генерации
		if notification.Status == sharedMessaging.NotificationStatusSuccess {
			// Проверка наличия URL и референса
			if notification.ImageURL == nil || *notification.ImageURL == "" {
				p.logger.Error("Image generation succeeded but returned empty or nil URL. Acking.", logFields...)
				return nil // Ack
			}
			if notification.ImageReference == "" {
				p.logger.Error("Image generation succeeded but returned empty ImageReference. Acking.", logFields...)
				return nil // Ack
			}

			imageURL := *notification.ImageURL
			imageReference := notification.ImageReference
			p.logger.Info("Image generated successfully, saving URL", append(logFields, zap.String("image_url", imageURL))...)

			dbCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()

			// 1. Сохраняем URL для текущего референса
			errSave := p.imageReferenceRepo.SaveOrUpdateImageReference(dbCtx, imageReference, imageURL)
			if errSave != nil {
				p.logger.Error("Failed to save image URL for reference, NACKing message", append(logFields, zap.String("image_url", imageURL), zap.Error(errSave))...)
				return fmt.Errorf("failed to save image URL for reference %s: %w", imageReference, errSave) // Nack
			}
			p.logger.Info("Image URL saved successfully for reference", logFields...)

			// 2. Проверяем, все ли изображения для этой истории готовы
			p.logger.Info("Checking if all images are ready for story", logFields...)

			story, errGetStory := p.publishedRepo.GetByID(dbCtx, publishedStoryID)
			if errGetStory != nil {
				if errors.Is(errGetStory, sharedModels.ErrNotFound) {
					p.logger.Warn("PublishedStory not found while checking image completion, maybe deleted? Acking.", logFields...)
				} else {
					p.logger.Error("Failed to get PublishedStory while checking image completion. Acking.", append(logFields, zap.Error(errGetStory))...)
				}
				// Не можем проверить статус, подтверждаем сообщение.
				return nil // Ack
			}

			// Если флаг are_images_pending уже false, ничего не делаем.
			if !story.AreImagesPending {
				p.logger.Info("Story already marked as images not pending. Skipping further checks.", logFields...)
				return nil // Ack
			}

			var setupContent sharedModels.NovelSetupContent
			if story.Setup == nil || len(story.Setup) == 0 {
				p.logger.Error("Setup is missing or empty for PublishedStory, cannot verify all images. Acking.", logFields...)
				return nil // Ack
			}
			if errUnmarshal := json.Unmarshal(story.Setup, &setupContent); errUnmarshal != nil {
				p.logger.Error("Failed to unmarshal Setup JSON for PublishedStory, cannot verify all images. Acking.", append(logFields, zap.Error(errUnmarshal))...)
				return nil // Ack
			}

			// Собираем все необходимые референсы
			requiredRefs := make([]string, 0)
			if setupContent.StoryPreviewImagePrompt != "" {
				requiredRefs = append(requiredRefs, fmt.Sprintf("history_preview_%s", publishedStoryID.String()))
			}
			for _, char := range setupContent.Characters {
				if char.ImageRef != "" {
					requiredRefs = append(requiredRefs, char.ImageRef)
				}
			}

			// Определяем, были ли все изображения готовы к концу проверок
			var allImagesReady bool
			if len(requiredRefs) == 0 {
				p.logger.Info("No images were required for this story according to Setup.", logFields...)
				allImagesReady = true
			} else {
				allImagesReady = true // Предполагаем, что готовы
				p.logger.Debug("Checking URLs for required image references", append(logFields, zap.Strings("refs", requiredRefs))...)
				for _, ref := range requiredRefs {
					// <<< НАЧАЛО ИЗМЕНЕНИЯ: Исправляем префикс перед проверкой >>>
					correctedCheckRef := ref
					// Применяем ту же логику, что и при отправке задач
					if strings.HasPrefix(ref, "history_preview_") {
						// Префикс превью не трогаем
					} else if !strings.HasPrefix(ref, "ch_") {
						p.logger.Warn("ImageRef in requiredRefs check does not start with 'ch_'. Attempting correction.", zap.String("original_ref", ref))
						if strings.HasPrefix(ref, "character_") {
							correctedCheckRef = strings.TrimPrefix(ref, "character_")
						} else if strings.HasPrefix(ref, "char_") {
							correctedCheckRef = strings.TrimPrefix(ref, "char_")
						} else {
							correctedCheckRef = ref
						}
						correctedCheckRef = "ch_" + strings.TrimPrefix(correctedCheckRef, "ch_")
						p.logger.Info("Ensured ImageRef prefix is 'ch_' for check.", zap.String("original_ref", ref), zap.String("new_ref", correctedCheckRef))
					}
					// <<< КОНЕЦ ИЗМЕНЕНИЯ >>>

					_, errCheck := p.imageReferenceRepo.GetImageURLByReference(dbCtx, correctedCheckRef) // <<< Проверяем исправленный correctedCheckRef >>>
					if errCheck != nil {
						if errors.Is(errCheck, sharedModels.ErrNotFound) {
							p.logger.Info("Required image reference still missing URL, not all images are ready yet.", append(logFields, zap.String("missing_ref", correctedCheckRef))...) // Логируем исправленный реф
							allImagesReady = false
							break // Нашли недостающий
						} else {
							p.logger.Error("Error checking image reference URL, assuming not all images are ready. Acking.", append(logFields, zap.String("checked_ref", correctedCheckRef), zap.Error(errCheck))...)
							// Техническая ошибка при проверке, лучше подтвердить сообщение, чтобы не зациклиться
							return nil // Ack
						}
					}
				}
			}

			// 4. Если все изображения готовы, обновляем флаг и проверяем общий статус
			if allImagesReady {
				p.logger.Info("All required image URLs found or none were required. Marking images as not pending.", logFields...)
				// Обновляем флаг are_images_pending = false
				errUpdatePending := p.publishedRepo.UpdateStatusFlagsAndDetails(dbCtx, publishedStoryID, story.Status, story.IsFirstScenePending, false, nil)
				if errUpdatePending != nil {
					p.logger.Error("Failed to update AreImagesPending flag to false. Acking.", append(logFields, zap.Error(errUpdatePending))...)
					// Ошибка обновления флага, но URL сохранен. Ack.
					return nil // Ack
				}

				// Проверяем, нужно ли установить статус Ready (если первая сцена тоже готова)
				// Используем значение story.IsFirstScenePending *до* обновления флага
				if !story.IsFirstScenePending {
					p.logger.Info("Both first scene and images are now ready. Setting story status to Ready.", logFields...)
					// Устанавливаем статус Ready
					errUpdateReady := p.publishedRepo.UpdateStatusFlagsAndDetails(dbCtx, publishedStoryID, sharedModels.StatusReady, false, false, nil)
					if errUpdateReady != nil {
						p.logger.Error("Failed to set story status to Ready after all checks passed. Acking.", append(logFields, zap.Error(errUpdateReady))...)
						// Ошибка установки Ready, но флаги обновлены. Ack.
						return nil // Ack
					}
					p.logger.Info("Story status successfully set to Ready.", logFields...)

					// <<< НАЧАЛО: Отправка WS обновления при StatusReady >>>
					clientUpdateReady := ClientStoryUpdate{
						ID:         publishedStoryID.String(),
						UserID:     story.UserID.String(), // Используем UserID из загруженной истории
						UpdateType: UpdateTypeStory,
						Status:     string(sharedModels.StatusReady),
						// SceneID, StateHash, EndingText не релевантны для этого обновления
					}
					wsCtx, wsCancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer wsCancel()
					if errWs := p.clientPub.PublishClientUpdate(wsCtx, clientUpdateReady); errWs != nil {
						p.logger.Error("Error sending ClientStoryUpdate (Images Ready -> Status Ready)", append(logFields, zap.Error(errWs))...)
					} else {
						p.logger.Info("ClientStoryUpdate sent (Images Ready -> Status Ready)", append(logFields, zap.String("status", clientUpdateReady.Status))...)
					}
					// <<< КОНЕЦ: Отправка WS обновления при StatusReady >>>

				} else {
					p.logger.Info("Images are ready, but first scene is still pending. Status Ready not set yet.", logFields...)
				}
			} else {
				p.logger.Info("Not all required image URLs are ready yet.", logFields...)
			}

			// Если дошли сюда без ошибок сохранения URL, подтверждаем сообщение
			return nil // Ack

		} else {
			// Неизвестный статус
			p.logger.Error("Unknown status in image notification. Acking.", append(logFields, zap.String("status", string(notification.Status)))...)
			return nil // Ack
		}

	default:
		p.logger.Error("Unknown PromptType in NotificationPayload. Nacking message.", zap.String("task_id", taskID), zap.String("prompt_type", string(notification.PromptType)))
		return fmt.Errorf("unknown PromptType: %s", notification.PromptType)
	}
}

// extractStoryIDFromImageReference - БОЛЬШЕ НЕ НУЖНА
/*
func extractStoryIDFromImageReference(ref string) (uuid.UUID, error) {
	matches := storyIDRegex.FindStringSubmatch(ref)
	if len(matches) == 0 {
		return uuid.Nil, fmt.Errorf("UUID not found in image reference: %s", ref)
	}
	id, err := uuid.Parse(matches[0])
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to parse extracted UUID '%s': %w", matches[0], err)
	}
	return id, nil
}
*/
