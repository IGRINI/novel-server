package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"

	"novel-server/gameplay-service/internal/config"
	interfaces "novel-server/shared/interfaces"
	sharedMessaging "novel-server/shared/messaging"
	sharedModels "novel-server/shared/models"
	"novel-server/shared/schemas"
)

// Типы обновлений для ClientStoryUpdate
const (
	UpdateTypeDraft = "draft_update"
	UpdateTypeStory = "story_update"
)

// Регулярное выражение для извлечения UUID из ImageReference - БОЛЬШЕ НЕ НУЖНО
// var storyIDRegex = regexp.MustCompile(`[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}`)

// <<< ПЕРЕМЕЩЕНО ИЗ handle_setup.go >>>
// MinimalConfigForFirstScene - структура для минимального конфига, отправляемого для PromptTypeNovelFirstSceneCreator
// Содержит только поля, необходимые этому промпту
type MinimalConfigForFirstScene struct {
	Language       string                   `json:"ln,omitempty"`
	IsAdultContent bool                     `json:"ac,omitempty"`
	Genre          string                   `json:"gn,omitempty"`
	PlayerName     string                   `json:"pn,omitempty"`
	PlayerGender   string                   `json:"pg,omitempty"`
	PlayerDesc     string                   `json:"p_desc,omitempty"`
	WorldContext   string                   `json:"wc,omitempty"`
	StorySummary   string                   `json:"ss,omitempty"`
	PlayerPrefs    sharedModels.PlayerPrefs `json:"pp,omitempty"` // Используем существующую структуру
}

// ToMinimalConfigForFirstScene преобразует полный конфиг в минимальный
func ToMinimalConfigForFirstScene(configBytes []byte) MinimalConfigForFirstScene {
	var fullConfig sharedModels.Config
	_ = json.Unmarshal(configBytes, &fullConfig) // Игнорируем ошибку, если конфиг некорректный

	// Используем структуру PlayerPrefs напрямую
	return MinimalConfigForFirstScene{
		Language:       fullConfig.Language,
		IsAdultContent: fullConfig.IsAdultContent,
		Genre:          fullConfig.Genre,
		PlayerName:     fullConfig.PlayerName,
		PlayerGender:   fullConfig.PlayerGender,
		PlayerDesc:     fullConfig.PlayerDesc,
		WorldContext:   fullConfig.WorldContext,
		StorySummary:   fullConfig.StorySummary,
		PlayerPrefs:    fullConfig.PlayerPrefs, // Передаем всю структуру PlayerPrefs
	}
}

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
	authClient                 interfaces.AuthServiceClient        // <<< ДОБАВЛЕНО: Клиент для Auth Service >>>
	logger                     *zap.Logger                         // <<< ДОБАВЛЕНО
	cfg                        *config.Config                      // <<< ДОБАВЛЕНО: Доступ к конфигурации
	playerProgressRepo         interfaces.PlayerProgressRepository // <<< ADDED: Dependency for progress updates
	db                         *pgxpool.Pool                       // <<< На pgxpool.Pool
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
	authClient interfaces.AuthServiceClient, // <<< ДОБАВЛЕНО >>>
	logger *zap.Logger, // <<< ДОБАВЛЕНО
	cfg *config.Config, // <<< ДОБАВЛЕНО: Принимаем конфиг
	playerProgressRepo interfaces.PlayerProgressRepository, // <<< ADDED
	db *pgxpool.Pool, // <<< На pgxpool.Pool
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
	if authClient == nil {
		logger.Fatal("authClient cannot be nil for NotificationProcessor")
	}
	if logger == nil {
		logger = zap.NewNop()
		logger.Warn("Nil logger passed to NewNotificationProcessor, using No-op logger.")
	}
	if cfg == nil {
		logger.Fatal("cfg cannot be nil for NotificationProcessor")
	}
	if db == nil { // <<< Проверяем pgxpool.Pool
		logger.Fatal("db pool cannot be nil for NotificationProcessor")
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
		authClient:                 authClient,
		logger:                     logger,
		cfg:                        cfg,
		playerProgressRepo:         playerProgressRepo,
		db:                         db, // <<< Присваиваем pgxpool.Pool
	}
}

// Process обрабатывает одно сообщение из очереди (может быть NotificationPayload или CharacterImageResultPayload)
// <<< ИЗМЕНЕНИЕ: Сигнатура теперь принимает amqp.Delivery >>>
func (p *NotificationProcessor) Process(ctx context.Context, delivery amqp.Delivery) error {
	// <<< ИЗМЕНЕНИЕ: Логируем начало обработки Delivery >>>
	p.logger.Info("Processing delivery", zap.String("correlation_id", delivery.CorrelationId), zap.Uint64("delivery_tag", delivery.DeliveryTag))

	// Попытка 1: Обработать как NotificationPayload
	var notification sharedMessaging.NotificationPayload

	// <<< ЛОГИРОВАНИЕ: Залогировать тело сообщения перед анмаршалингом >>>
	p.logger.Debug("Raw message body received",
		zap.ByteString("body", delivery.Body),
		zap.String("correlation_id", delivery.CorrelationId),
		zap.Uint64("delivery_tag", delivery.DeliveryTag),
	)

	errNotification := json.Unmarshal(delivery.Body, &notification)

	// <<< ЛОГИРОВАНИЕ: Залогировать ImageURL после анмаршалинга >>>
	if notification.ImageURL != nil {
		p.logger.Debug("ImageURL after unmarshal",
			zap.Stringp("image_url_ptr", notification.ImageURL),
			zap.String("task_id", notification.TaskID),
			zap.String("correlation_id", delivery.CorrelationId),
		)
	} else {
		p.logger.Debug("ImageURL after unmarshal is nil",
			zap.String("task_id", notification.TaskID),
			zap.String("correlation_id", delivery.CorrelationId),
		)
	}

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

	if notification.StoryConfigID != "" {
		storyConfigID, parseIDErr = uuid.Parse(notification.StoryConfigID)
		if parseIDErr == nil {
			isStoryConfigTask = true
		}
	}
	if !isStoryConfigTask && notification.PublishedStoryID != "" {
		publishedStoryID, parseIDErr = uuid.Parse(notification.PublishedStoryID)
	}

	if parseIDErr != nil || (storyConfigID == uuid.Nil && publishedStoryID == uuid.Nil) {
		p.logger.Error("Invalid or missing ID in NotificationPayload", zap.String("task_id", taskID), zap.String("story_config_id", notification.StoryConfigID), zap.String("published_story_id", notification.PublishedStoryID), zap.Error(parseIDErr))
		return fmt.Errorf("invalid or missing ID in notification payload: %w", parseIDErr)
	}

	switch notification.PromptType {
	case sharedModels.PromptTypeNarrator, sharedModels.PromptTypeNarratorReviser:
		if !isStoryConfigTask {
			p.logger.Error("Narrator/Reviser received without StoryConfigID", zap.String("task_id", taskID), zap.String("prompt_type", string(notification.PromptType)), zap.String("published_story_id", notification.PublishedStoryID))
			return fmt.Errorf("invalid notification: %s without StoryConfigID", notification.PromptType)
		}
		// Вызываем метод из handle_narrator.go
		return p.handleNarratorNotification(ctx, notification, storyConfigID)

	case sharedModels.PromptTypeNovelSetup:
		if isStoryConfigTask {
			p.logger.Error("NovelSetup received with StoryConfigID", zap.String("task_id", taskID), zap.String("story_config_id", storyConfigID.String()))
			return fmt.Errorf("invalid notification: Setup with StoryConfigID")
		}
		// Вызываем метод из handle_setup.go
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

		// Разделение логики для начальной и последующих сцен
		if notification.GameStateID == "" && notification.StateHash == sharedModels.InitialStateHash {
			p.logger.Info("Routing to handleInitialSceneGenerated", zap.String("task_id", taskID), zap.Stringer("publishedStoryID", publishedStoryID))
			return p.handleInitialSceneGenerated(ctx, notification, publishedStoryID)
		} else if notification.GameStateID != "" {
			p.logger.Info("Routing to handleSceneGenerationNotification", zap.String("task_id", taskID), zap.Stringer("publishedStoryID", publishedStoryID), zap.String("gameStateID", notification.GameStateID))
			// Вызываем метод из handle_scene.go
			return p.handleSceneGenerationNotification(ctx, notification, publishedStoryID)
		} else {
			p.logger.Error("Scene/GameOver notification received without GameStateID for non-initial hash",
				zap.String("task_id", taskID),
				zap.String("prompt_type", string(notification.PromptType)),
				zap.Stringer("publishedStoryID", publishedStoryID),
				zap.String("stateHash", notification.StateHash),
			)
			return fmt.Errorf("invalid notification: missing GameStateID for non-initial hash %s", notification.StateHash)
		}

	case sharedModels.PromptTypeCharacterImage, sharedModels.PromptTypeStoryPreviewImage:
		p.logger.Info("Processing image generation result notification",
			zap.String("task_id", taskID),
			zap.String("prompt_type", string(notification.PromptType)),
			zap.String("published_story_id_str", notification.PublishedStoryID),
			zap.String("image_reference", notification.ImageReference),
			zap.String("status", string(notification.Status)),
		)
		publishedStoryIDForImage, err := uuid.Parse(notification.PublishedStoryID)
		if err != nil || publishedStoryIDForImage == uuid.Nil {
			p.logger.Error("Invalid or missing PublishedStoryID in image notification. Acking.",
				zap.String("task_id", taskID),
				zap.String("published_story_id_str", notification.PublishedStoryID),
				zap.Error(err),
			)
			return nil // Ack
		}
		// Вызываем метод из handle_image.go (предполагая его существование)
		return p.handleImageNotification(ctx, notification, publishedStoryIDForImage)

	default:
		p.logger.Warn("Получен неизвестный тип уведомления", zap.String("prompt_type", string(notification.PromptType)), zap.String("task_id", taskID))
		return nil // Ack
	}
}

// handleInitialSceneGenerated обрабатывает уведомление об успешной/неудачной генерации НАЧАЛЬНОЙ сцены.
func (p *NotificationProcessor) handleInitialSceneGenerated(ctx context.Context, notification sharedMessaging.NotificationPayload, publishedStoryID uuid.UUID) error {
	log := p.logger.With(
		zap.String("task_id", notification.TaskID),
		zap.Stringer("publishedStoryID", publishedStoryID),
		zap.String("stateHash", notification.StateHash),
		zap.String("notification_status", string(notification.Status)),
	)
	log.Info("Handling initial scene generated notification")

	if notification.StateHash != sharedModels.InitialStateHash {
		log.Error("handleInitialSceneGenerated called with non-initial StateHash", zap.String("stateHash", notification.StateHash))
		return fmt.Errorf("internal logic error: handleInitialSceneGenerated called with hash %s", notification.StateHash)
	}

	// 1. Обработка ошибки
	if notification.Status == sharedMessaging.NotificationStatusError {
		log.Error("Initial scene generation failed", zap.String("error_details", notification.ErrorDetails))
		err := p.publishedRepo.UpdateStatusFlagsAndDetails(ctx, p.db, publishedStoryID, sharedModels.StatusError, false, false, &notification.ErrorDetails)
		if err != nil {
			log.Error("Failed to update published story status to Error after initial scene generation failure", zap.Error(err))
		}
		// Уведомляем клиента и отправляем Push
		userID, _ := uuid.Parse(notification.UserID)
		go p.publishClientStoryUpdateOnError(ctx, publishedStoryID, userID, notification.ErrorDetails)
		go p.publishPushOnError(ctx, publishedStoryID, userID, notification.ErrorDetails, "story_error")
		return nil // Ack
	}

	// 2. Обработка успеха
	if notification.Status == sharedMessaging.NotificationStatusSuccess {
		log.Info("Initial scene generated successfully")
		// 2.1 Находим сцену
		scene, errScene := p.sceneRepo.FindByStoryAndHash(ctx, p.db, publishedStoryID, sharedModels.InitialStateHash)
		if errScene != nil {
			log.Error("Failed to find the generated initial scene by hash", zap.Error(errScene))
			return fmt.Errorf("failed to find generated initial scene: %w", errScene) // Nack
		}
		log.Info("Found generated initial scene", zap.Stringer("sceneID", scene.ID))

		// 2.3 Находим ВСЕ PlayerGameState, ожидающие начальную сцену
		// Используем ListByPlayerAndStory + фильтрация
		userID, _ := uuid.Parse(notification.UserID)
		allStatesForUser, errList := p.playerGameStateRepo.ListByPlayerAndStory(ctx, p.db, userID, publishedStoryID)
		if errList != nil {
			log.Error("Failed to list game states by player and story for initial scene", zap.Error(errList))
			return fmt.Errorf("failed to list waiting game states: %w", errList) // Nack
		}
		statesToUpdate := make([]*sharedModels.PlayerGameState, 0)
		for _, gs := range allStatesForUser {
			// <<< ИСПРАВЛЕНО: Проверяем только статус GeneratingScene, т.к. PlayerProgressID для начальной сцены еще нет >>>
			if gs.PlayerStatus == sharedModels.PlayerStatusGeneratingScene {
				statesToUpdate = append(statesToUpdate, gs)
			}
		}

		log.Info("Found game states waiting for initial scene", zap.Int("count", len(statesToUpdate)))

		// 2.4 Обновляем каждое найденное состояние
		updatedCount := 0
		now := time.Now().UTC()
		for _, gameState := range statesToUpdate {
			gameStateLog := log.With(zap.Stringer("gameStateID", gameState.ID))
			gameState.PlayerStatus = sharedModels.PlayerStatusPlaying
			gameState.CurrentSceneID = uuid.NullUUID{UUID: scene.ID, Valid: true}
			gameState.LastActivityAt = now
			gameState.ErrorDetails = nil
			_, errSave := p.playerGameStateRepo.Save(ctx, p.db, gameState)
			if errSave != nil {
				gameStateLog.Error("Failed to update player game state with initial scene", zap.Error(errSave))
				continue
			}
			updatedCount++
			gameStateLog.Info("Player game state updated with initial scene")
			// Уведомляем клиента
			go p.publishClientGameStateUpdateOnSuccess(ctx, gameState, scene.ID)
		}
		log.Info("Finished updating game states", zap.Int("updatedCount", updatedCount))

		// 2.5 Обновляем статус истории и отправляем уведомление, если нужно
		// Получаем актуальное состояние ДО обновления статуса
		storyBeforeUpdate, errStory := p.publishedRepo.GetByID(ctx, p.db, publishedStoryID)
		if errStory != nil {
			log.Error("Failed to get published story before final status update", zap.Error(errStory))
			// Не критично для обновления статуса, но уведомление может не уйти
		}

		// Определяем новый статус (Ready, если не было ошибки) и обновляем флаги
		newStatus := sharedModels.StatusReady
		if storyBeforeUpdate != nil && storyBeforeUpdate.Status == sharedModels.StatusError {
			newStatus = sharedModels.StatusError // Сохраняем Error, если он уже был
		}
		wasErrorBefore := storyBeforeUpdate != nil && storyBeforeUpdate.Status == sharedModels.StatusError
		wasReadyBefore := storyBeforeUpdate != nil && storyBeforeUpdate.Status == sharedModels.StatusReady
		wereImagesPendingBefore := storyBeforeUpdate != nil && storyBeforeUpdate.AreImagesPending

		errUpdate := p.publishedRepo.UpdateStatusFlagsAndDetails(ctx, p.db, publishedStoryID, newStatus,
			false,                   // isFirstScenePending = false
			wereImagesPendingBefore, // Сохраняем флаг AreImagesPending (он мог быть false, если картинки не требовались)
			nil)                     // Сбрасываем ошибку
		if errUpdate != nil {
			log.Error("Failed to update published story flags/status after initial scene generated", zap.Error(errUpdate))
			// Ошибка обновления статуса, уведомление не отправляем
		} else {
			log.Info("Successfully updated published story status/flags",
				zap.String("new_status", string(newStatus)),
				zap.Bool("is_first_scene_pending", false),
				zap.Bool("are_images_pending", wereImagesPendingBefore),
			)

			// Проверяем, НУЖНО ЛИ отправлять уведомление о готовности
			// Отправляем, если:
			// 1. Новый статус - Ready
			// 2. Статус ДО этого НЕ был Ready и НЕ был Error
			// 3. Флаг AreImagesPending (сохраненный) - false (т.е. картинки готовы ИЛИ не требовались)
			shouldNotifyReady := newStatus == sharedModels.StatusReady && !wasReadyBefore && !wasErrorBefore && !wereImagesPendingBefore

			if shouldNotifyReady {
				log.Info("Story became Ready, triggering notifications")
				// Используем storyBeforeUpdate, т.к. он содержит UserID
				if storyBeforeUpdate != nil {
					go p.publishClientStoryUpdateOnReady(ctx, storyBeforeUpdate) // Используем КОПИЮ storyBeforeUpdate!
					go p.publishPushNotificationForStoryReady(ctx, storyBeforeUpdate)
				} else {
					log.Warn("Cannot send Ready notifications because storyBeforeUpdate was nil")
				}
			} else {
				log.Info("No 'Ready' notification needed",
					zap.String("final_status", string(newStatus)),
					zap.Bool("wasReadyBefore", wasReadyBefore),
					zap.Bool("wasErrorBefore", wasErrorBefore),
					zap.Bool("wereImagesPendingBefore", wereImagesPendingBefore),
				)
			}
		}
	}

	return nil // Ack
}

// handleImageNotification обрабатывает уведомление о генерации изображения.
func (p *NotificationProcessor) handleImageNotification(ctx context.Context, notification sharedMessaging.NotificationPayload, publishedStoryID uuid.UUID) error {
	// <<< НАЧАЛО: Логика из старого процессора >>>
	logFields := []zap.Field{
		zap.String("task_id", notification.TaskID),
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
		// Вызываем отдельную функцию для проверки и обновления статуса
		go p.checkStoryReadinessAfterImage(dbCtx, publishedStoryID)

		// Если дошли сюда без ошибок сохранения URL, подтверждаем сообщение
		return nil // Ack

	} else {
		// Неизвестный статус
		p.logger.Error("Unknown status in image notification. Acking.", append(logFields, zap.String("status", string(notification.Status)))...)
		return nil // Ack
	}
	// <<< КОНЕЦ: Логика из старого процессора >>>
}

// --- Helper methods for publishing updates ---

func (p *NotificationProcessor) publishClientStoryUpdateOnError(ctx context.Context, storyID uuid.UUID, userID uuid.UUID, errorDetails string) {
	if userID == uuid.Nil {
		p.logger.Warn("Cannot publish client story update on error, UserID is nil", zap.Stringer("storyID", storyID))
		return
	}
	payload := sharedModels.ClientStoryUpdate{
		ID:           storyID.String(),
		UserID:       userID.String(),
		UpdateType:   sharedModels.UpdateTypeStory,
		Status:       string(sharedModels.StatusError),
		ErrorDetails: &errorDetails,
	}
	if err := p.clientPub.PublishClientUpdate(context.Background(), payload); err != nil {
		p.logger.Error("Failed to publish client story update on error", zap.Error(err), zap.Stringer("storyID", storyID), zap.Stringer("userID", userID))
	}
}

// <<< НАЧАЛО: Новый хелпер для WS при готовности >>>
func (p *NotificationProcessor) publishClientStoryUpdateOnReady(ctx context.Context, story *sharedModels.PublishedStory) {
	if story == nil {
		return
	}
	payload := sharedModels.ClientStoryUpdate{
		ID:         story.ID.String(),
		UserID:     story.UserID.String(),
		UpdateType: sharedModels.UpdateTypeStory,
		Status:     string(sharedModels.StatusReady),
		// Другие поля (SceneID, StateHash, ErrorDetails) не нужны для этого типа обновления
	}
	if err := p.clientPub.PublishClientUpdate(context.Background(), payload); err != nil {
		p.logger.Error("Failed to publish client story update on ready", zap.Error(err), zap.Stringer("storyID", story.ID), zap.Stringer("userID", story.UserID))
	}
}

// <<< КОНЕЦ: Новый хелпер для WS при готовности >>>

func (p *NotificationProcessor) publishClientGameStateUpdateOnSuccess(ctx context.Context, gameState *sharedModels.PlayerGameState, sceneID uuid.UUID) {
	if gameState == nil {
		return
	}
	sceneIDStr := sceneID.String()
	payload := sharedModels.ClientStoryUpdate{
		ID:         gameState.ID.String(),
		UserID:     gameState.PlayerID.String(),
		UpdateType: sharedModels.UpdateTypeGameState,
		Status:     string(gameState.PlayerStatus),
		SceneID:    &sceneIDStr,
	}
	if err := p.clientPub.PublishClientUpdate(context.Background(), payload); err != nil {
		p.logger.Error("Failed to publish client game state update on success", zap.Error(err), zap.Stringer("gameStateID", gameState.ID))
	}
}

func (p *NotificationProcessor) publishPushOnError(ctx context.Context, storyID uuid.UUID, userID uuid.UUID, errorDetails, eventType string) {
	if userID == uuid.Nil {
		return
	}
	// TODO: Get localized title/body
	title := "Story Generation Error"
	body := fmt.Sprintf("An error occurred during story generation: %s", errorDetails)
	if len(body) > 150 {
		body = body[:147] + "..."
	}

	pushPayload := sharedModels.PushNotificationPayload{
		UserID: userID,
		Notification: sharedModels.PushNotification{
			Title: title,
			Body:  body,
		},
		Data: map[string]string{
			"eventType":        eventType,
			"publishedStoryId": storyID.String(),
		},
	}
	if err := p.pushPub.PublishPushNotification(ctx, pushPayload); err != nil {
		p.logger.Error("Failed to publish push notification on error", zap.Error(err), zap.String("eventType", eventType), zap.Stringer("storyID", storyID), zap.Stringer("userID", userID))
	}
}

// <<< КОНЕЦ: Функция проверки готовности >>>

// <<< НАЧАЛО: Функция проверки готовности после генерации изображения >>>
// checkStoryReadinessAfterImage проверяет, все ли необходимые изображения и первая сцена для истории готовы.
// Если да, обновляет статус истории на Ready и отправляет Push-уведомление.
func (p *NotificationProcessor) checkStoryReadinessAfterImage(ctx context.Context, publishedStoryID uuid.UUID) {
	log := p.logger.With(zap.Stringer("publishedStoryID", publishedStoryID))
	log.Info("Checking story readiness after image generated")

	dbCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. Получаем текущее состояние истории
	story, errGetStory := p.publishedRepo.GetByID(dbCtx, p.db, publishedStoryID)
	if errGetStory != nil {
		if errors.Is(errGetStory, sharedModels.ErrNotFound) {
			log.Warn("PublishedStory not found while checking image completion, maybe deleted?")
		} else {
			log.Error("Failed to get PublishedStory while checking image completion.", zap.Error(errGetStory))
		}
		return // Не можем продолжить без истории
	}

	// 2. Если флаги уже сняты, ничего не делаем
	if !story.AreImagesPending && !story.IsFirstScenePending {
		log.Info("Story already marked as ready (images and first scene not pending). Skipping further checks.")
		return
	}
	if !story.AreImagesPending {
		log.Info("Story already marked as images not pending. Skipping image checks.")
		// Проверяем только флаг первой сцены ниже
	} else {
		// 3. Проверяем, все ли изображения готовы (если флаг AreImagesPending еще true)
		var setupContent sharedModels.NovelSetupContent
		if len(story.Setup) == 0 {
			log.Error("Setup is missing or empty for PublishedStory, cannot verify all images.")
			// Возможно, стоит сбросить флаг AreImagesPending в Error? Пока нет.
			return
		}
		if errUnmarshalSetup := json.Unmarshal(story.Setup, &setupContent); errUnmarshalSetup != nil {
			log.Error("Cannot publish first scene task: Failed to unmarshal setup JSON", zap.Error(errUnmarshalSetup))
			return
		}

		// Собираем все необходимые референсы
		requiredRefs := make([]string, 0)
		if setupContent.StoryPreviewImagePrompt != "" {
			requiredRefs = append(requiredRefs, fmt.Sprintf("history_preview_%s", publishedStoryID.String()))
		}
		for _, char := range setupContent.Characters {
			if char.ImageRef != "" {
				// <<< ИСПРАВЛЕНИЕ: Применяем ту же логику префикса, что и в старом коде >>>
				correctedRef := char.ImageRef
				if !strings.HasPrefix(correctedRef, "ch_") {
					// log.Warn("Character ImageRef in Setup does not start with 'ch_'. Attempting correction for check.", zap.String("original_ref", correctedRef)) // <<< УБИРАЕМ ЛОГ
					if strings.HasPrefix(correctedRef, "character_") {
						correctedRef = strings.TrimPrefix(correctedRef, "character_")
					} else if strings.HasPrefix(correctedRef, "char_") {
						correctedRef = strings.TrimPrefix(correctedRef, "char_")
					}
					correctedRef = "ch_" + strings.TrimPrefix(correctedRef, "ch_")
				}
				requiredRefs = append(requiredRefs, correctedRef)
			}
		}

		// Проверяем URL для каждого референса
		allImagesReady := true
		if len(requiredRefs) == 0 {
			log.Info("No images were required for this story according to Setup.")
		} else {
			log.Debug("Checking URLs for required image references", zap.Strings("refs", requiredRefs))
			for _, ref := range requiredRefs {
				imageURL, errCheck := p.imageReferenceRepo.GetImageURLByReference(dbCtx, ref)
				if errCheck != nil {
					if errors.Is(errCheck, sharedModels.ErrNotFound) {
						log.Info("Required image reference still missing URL, not all images are ready yet.", zap.String("missing_ref", ref))
						allImagesReady = false
						break // Нашли недостающий
					} else {
						log.Error("Error checking image reference URL.", zap.String("checked_ref", ref), zap.Error(errCheck))
						// Техническая ошибка при проверке, выходим
						return
					}
				} else {
					if imageURL == "" || strings.HasPrefix(imageURL, "https://https:/") { // Проверяем на пустоту и некорректный префикс
						log.Warn("Required image reference found, but URL is invalid or empty.", zap.String("ref", ref), zap.String("invalid_url", imageURL))
						allImagesReady = false
						break // Нашли некорректный URL
					}
				}
			}
		}

		// Если не все изображения готовы, выходим
		if !allImagesReady {
			log.Info("Not all required image URLs are ready yet.")
			return
		}

		// Если все изображения готовы, обновляем флаг
		log.Info("All required image URLs found or none were required. Marking images as not pending.")

		// <<< НАЧАЛО ИЗМЕНЕНИЯ: Определяем правильный статус >>>
		var newStatus sharedModels.StoryStatus
		if story.IsFirstScenePending {
			// Если первая сцена еще ожидается, ставим статус first_scene_pending
			newStatus = sharedModels.StatusFirstScenePending
			log.Info("All images ready, setting status to trigger first scene generation.", zap.String("new_status", string(newStatus)))
		} else {
			// Если первая сцена уже не ожидается (что маловероятно на этом этапе, но для полноты),
			// то сохраняем текущий статус (который может быть 'error' или другой)
			newStatus = story.Status
			log.Info("All images ready, but first scene is not pending. Keeping current status.", zap.String("current_status", string(newStatus)))
		}

		// Обновляем статус и флаг AreImagesPending
		errUpdatePending := p.publishedRepo.UpdateStatusFlagsAndDetails(dbCtx, p.db, publishedStoryID, newStatus, story.IsFirstScenePending, false, nil)
		if errUpdatePending != nil {
			log.Error("Failed to update status and AreImagesPending flag.", zap.Error(errUpdatePending))
			// Выходим, так как не смогли обновить важную информацию
			return
		}
		// Обновляем локальную переменную для дальнейшей проверки
		story.AreImagesPending = false
		story.Status = newStatus // Обновляем и локальный статус

		// <<< НАЧАЛО БЛОКА ОТПРАВКИ ЗАДАЧИ >>>
		// Если статус изменился на first_scene_pending, отправляем задачу
		if newStatus == sharedModels.StatusFirstScenePending {
			log.Info("Publishing task for first scene generation...")
			if errPublish := p.publishFirstSceneTaskInternal(ctx, story); errPublish != nil {
				log.Error("Failed to publish first scene task after images became ready", zap.Error(errPublish))
				// Не выходим, но логируем критическую ошибку
			}
		}
		// <<< КОНЕЦ БЛОКА ОТПРАВКИ ЗАДАЧИ >>>
	}

	// 4. Проверяем, готова ли первая сцена и нужно ли установить статус Ready
	if !story.IsFirstScenePending && !story.AreImagesPending && story.Status != sharedModels.StatusReady {
		log.Info("Both first scene and images are now ready. Setting story status to Ready.")
		errUpdateReady := p.publishedRepo.UpdateStatusFlagsAndDetails(dbCtx, p.db, publishedStoryID, sharedModels.StatusReady, false, false, nil)
		if errUpdateReady != nil {
			log.Error("Failed to set story status to Ready after all checks passed.", zap.Error(errUpdateReady))
			return // Не смогли установить статус Ready
		}
		log.Info("Story status successfully set to Ready.")

		// Отправляем WS обновление и Push
		go p.publishClientStoryUpdateOnReady(ctx, story)      // Используем оригинальный ctx
		go p.publishPushNotificationForStoryReady(ctx, story) // Используем оригинальный ctx
	} else {
		log.Info("Story is not yet fully ready (or already Ready).", zap.Bool("firstScenePending", story.IsFirstScenePending), zap.Bool("imagesPending", story.AreImagesPending), zap.String("currentStatus", string(story.Status)))
	}
}

// <<< КОНЕЦ: Функция проверки готовности >>>

// --- Helper for Publishing First Scene Task ---

// <<< НАЧАЛО: Логика отправки задачи первой сцены >>>
// publishFirstSceneTaskInternal отправляет задачу на генерацию первой сцены
// (логика скопирована и адаптирована из handleNovelSetupNotification)
func (p *NotificationProcessor) publishFirstSceneTaskInternal(ctx context.Context, story *sharedModels.PublishedStory) error {
	log := p.logger.With(zap.Stringer("publishedStoryID", story.ID))
	log.Info("Preparing task for first scene generation (Internal)")

	if len(story.Config) == 0 {
		log.Error("Cannot publish first scene task: Config is nil or empty")
		return errors.New("config is nil or empty")
	}
	if len(story.Setup) == 0 {
		log.Error("Cannot publish first scene task: Setup is nil or empty")
		return errors.New("setup is nil or empty")
	}

	// Распарсиваем Setup для получения структурированных данных
	var setupContent sharedModels.NovelSetupContent
	if errUnmarshalSetup := json.Unmarshal(story.Setup, &setupContent); errUnmarshalSetup != nil {
		log.Error("Cannot publish first scene task: Failed to unmarshal setup JSON", zap.Error(errUnmarshalSetup))
		return fmt.Errorf("failed to unmarshal setup JSON: %w", errUnmarshalSetup)
	}

	// Генерируем plain-текст для первой сцены: комбинируем конфиг и setup
	var fullConfig sharedModels.Config
	_ = json.Unmarshal(story.Config, &fullConfig)
	configPlain := schemas.FormatNarratorPlain(&fullConfig)
	setupPlain := schemas.FormatNovelSetupForScene(&fullConfig, &setupContent)
	userInput := configPlain + "\n" + setupPlain

	// Используем язык из истории
	storyLanguage := story.Language
	if storyLanguage == "" {
		log.Warn("Language field is empty in published story, defaulting to 'en' for first scene task")
		storyLanguage = "en"
	}

	// Формируем payload задачи
	nextTaskPayload := sharedMessaging.GenerationTaskPayload{
		TaskID:           uuid.New().String(),
		UserID:           story.UserID.String(),
		PromptType:       sharedModels.PromptTypeNovelFirstSceneCreator,
		PublishedStoryID: story.ID.String(),
		UserInput:        userInput,
		StateHash:        sharedModels.InitialStateHash,
		Language:         storyLanguage,
	}

	// Отправляем задачу
	if errPub := p.taskPub.PublishGenerationTask(ctx, nextTaskPayload); errPub != nil {
		log.Error("Failed to send first scene generation task", zap.Error(errPub), zap.String("next_task_id", nextTaskPayload.TaskID))
		return fmt.Errorf("failed to publish task for first scene: %w", errPub)
	}

	log.Info("First scene generation task sent successfully (Internal)", zap.String("next_task_id", nextTaskPayload.TaskID))
	return nil
}

// <<< КОНЕЦ: Логика отправки задачи первой сцены >>>

// Note: Methods handleNarratorNotification, handleNovelSetupNotification, handleSceneGenerationNotification,
// handleImageNotification should be defined in their respective handle_*.go files.
// Removed placeholder/duplicate definitions from here.
