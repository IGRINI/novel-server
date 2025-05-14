package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"

	"novel-server/gameplay-service/internal/config"
	"novel-server/shared/constants"
	interfaces "novel-server/shared/interfaces"
	sharedMessaging "novel-server/shared/messaging"
	sharedModels "novel-server/shared/models"
	"novel-server/shared/notifications"
	"novel-server/shared/utils"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
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
	IsAdultContent bool                     `json:"ac,omitempty"`
	Genre          string                   `json:"gn,omitempty"`
	PlayerName     string                   `json:"pn,omitempty"`
	PlayerDesc     string                   `json:"pd,omitempty"`
	WorldContext   string                   `json:"wc,omitempty"`
	StorySummary   string                   `json:"ss,omitempty"`
	PlayerPrefs    sharedModels.PlayerPrefs `json:"pp,omitempty"` // Используем существующую структуру
}

// ToMinimalConfigForFirstScene преобразует полный конфиг в минимальный
// IsAdultContent должен передаваться в эту функцию как отдельный параметр, так как его нет в models.Config
func ToMinimalConfigForFirstScene(configBytes []byte, isAdultContentForStory bool) MinimalConfigForFirstScene {
	var fullConfig sharedModels.Config
	_ = json.Unmarshal(configBytes, &fullConfig) // Игнорируем ошибку, если конфиг некорректный

	// Используем структуру PlayerPrefs напрямую
	return MinimalConfigForFirstScene{
		IsAdultContent: isAdultContentForStory, // Используем переданный параметр
		Genre:          fullConfig.Genre,
		PlayerName:     fullConfig.ProtagonistName,
		PlayerDesc:     fullConfig.ProtagonistDescription,
		WorldContext:   fullConfig.WorldContext,
		StorySummary:   fullConfig.StorySummary,
		PlayerPrefs:    fullConfig.PlayerPrefs, // Передаем всю структуру PlayerPrefs
	}
}

// NotificationHandler определяет обработчик уведомлений по типу PromptType
type NotificationHandler interface {
	Handle(ctx context.Context, notification sharedMessaging.NotificationPayload, storyID uuid.UUID) error
}

// NotificationHandlerFunc позволяет использовать функцию как NotificationHandler
type NotificationHandlerFunc func(ctx context.Context, notification sharedMessaging.NotificationPayload, storyID uuid.UUID) error

func (f NotificationHandlerFunc) Handle(ctx context.Context, notification sharedMessaging.NotificationPayload, storyID uuid.UUID) error {
	return f(ctx, notification, storyID)
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
	clientPub                  ClientUpdatePublisher                           // Для отправки обновлений клиенту
	taskPub                    TaskPublisher                                   // !!! ДОБАВЛЕНО: Для отправки новых задач генерации
	pushPub                    PushNotificationPublisher                       // <<< Добавляем издателя push-уведомлений
	characterImageTaskPub      CharacterImageTaskPublisher                     // <<< ДОБАВЛЕНО: Для отправки задач генерации изображений
	characterImageTaskBatchPub CharacterImageTaskBatchPublisher                // <<< ДОБАВЛЕНО: Для отправки батчей задач генерации изображений
	authClient                 interfaces.AuthServiceClient                    // <<< ДОБАВЛЕНО: Клиент для Auth Service >>>
	logger                     *zap.Logger                                     // <<< ДОБАВЛЕНО
	cfg                        *config.Config                                  // <<< ДОБАВЛЕНО: Доступ к конфигурации
	playerProgressRepo         interfaces.PlayerProgressRepository             // <<< ADDED: Dependency for progress updates
	db                         *pgxpool.Pool                                   // <<< На pgxpool.Pool
	handlers                   map[sharedModels.PromptType]NotificationHandler // мапа PromptType->Handler
	gameLoopService            interfaces.GameLoopService                      // <<< ДОБАВЛЕНО
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
	gameLoopService interfaces.GameLoopService, // <<< ДОБАВЛЕНО
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
	if gameLoopService == nil {
		logger.Fatal("gameLoopService cannot be nil for NotificationProcessor")
	}
	p := &NotificationProcessor{
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
		db:                         db,
		handlers:                   make(map[sharedModels.PromptType]NotificationHandler),
		gameLoopService:            gameLoopService,
	}
	// Регистрируем стандартные обработчики
	p.handlers[sharedModels.PromptTypeNarrator] = NotificationHandlerFunc(p.handleNarratorNotification)
	p.handlers[sharedModels.PromptTypeContentModeration] = NotificationHandlerFunc(p.handleContentModerationResult)
	p.handlers[sharedModels.PromptTypeProtagonistGoal] = NotificationHandlerFunc(p.handleProtagonistGoalResult)
	p.handlers[sharedModels.PromptTypeScenePlanner] = NotificationHandlerFunc(p.handleScenePlannerResult)
	p.handlers[sharedModels.PromptTypeStorySetup] = NotificationHandlerFunc(p.handleNovelSetupNotification)
	// StoryContinuation и GameOver
	p.handlers[sharedModels.PromptTypeStoryContinuation] = NotificationHandlerFunc(func(ctx context.Context, notification sharedMessaging.NotificationPayload, storyID uuid.UUID) error {
		if notification.GameStateID == "" && notification.StateHash == sharedModels.InitialStateHash {
			return p.handleInitialSceneGenerated(ctx, notification, storyID)
		}
		if notification.GameStateID != "" {
			return p.handleSceneGenerationNotification(ctx, notification, storyID)
		}
		p.logger.Error("Invalid continuation notification", zap.Any("notification", notification))
		return nil
	})
	p.handlers[sharedModels.PromptTypeNovelGameOverCreator] = p.handlers[sharedModels.PromptTypeStoryContinuation]
	p.handlers[sharedModels.PromptTypeCharacterGeneration] = NotificationHandlerFunc(p.handleCharacterGenerationResult)
	p.handlers[sharedModels.PromptTypeCharacterImage] = NotificationHandlerFunc(p.handleImageNotification)
	p.handlers[sharedModels.PromptTypeStoryPreviewImage] = NotificationHandlerFunc(p.handleImageNotification)
	p.handlers[sharedModels.PromptTypeJsonGeneration] = NotificationHandlerFunc(p.handleJsonGenerationResult)
	return p
}

// Метрики обработки уведомлений
var (
	notificationProcessCounter = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "notification_processed_total",
			Help: "Total processed notifications by prompt type and result",
		}, []string{"prompt_type", "result"},
	)
	notificationProcessDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "notification_process_duration_seconds",
			Help:    "Duration of notification processing",
			Buckets: prometheus.DefBuckets,
		}, []string{"prompt_type"},
	)
)

// Process обрабатывает одно сообщение из очереди (может быть NotificationPayload или CharacterImageResultPayload)
// <<< ИЗМЕНЕНИЕ: Сигнатура теперь принимает amqp.Delivery >>>
func (p *NotificationProcessor) Process(ctx context.Context, delivery amqp.Delivery) error {
	start := time.Now()
	p.logger.Info("Processing delivery", zap.String("correlation_id", delivery.CorrelationId), zap.Uint64("delivery_tag", delivery.DeliveryTag))

	var notification sharedMessaging.NotificationPayload
	errNotification := json.Unmarshal(delivery.Body, &notification)
	if errNotification != nil {
		// метрика ошибки парсинга
		notificationProcessCounter.WithLabelValues("unmarshal", "error").Inc()
		p.logger.Error("Failed to unmarshal message body as NotificationPayload", zap.Error(errNotification), zap.String("correlation_id", delivery.CorrelationId))
		return fmt.Errorf("unknown message format: %w", errNotification)
	}

	p.logger.Info("Processing as NotificationPayload", zap.String("task_id", notification.TaskID), zap.String("prompt_type", string(notification.PromptType)))
	errProcess := p.processNotificationPayloadInternal(ctx, notification)
	duration := time.Since(start).Seconds()
	notificationProcessDuration.WithLabelValues(string(notification.PromptType)).Observe(duration)
	if errProcess != nil {
		notificationProcessCounter.WithLabelValues(string(notification.PromptType), "error").Inc()
	} else {
		notificationProcessCounter.WithLabelValues(string(notification.PromptType), "success").Inc()
	}
	if errProcess != nil {
		p.logger.Error("Error processing NotificationPayload", zap.String("task_id", notification.TaskID), zap.Error(errProcess))
		return errProcess
	}
	p.logger.Info("NotificationPayload processed successfully", zap.String("task_id", notification.TaskID))
	return nil
}

// processNotificationPayloadInternal обрабатывает задачи генерации текста
func (p *NotificationProcessor) processNotificationPayloadInternal(ctx context.Context, notification sharedMessaging.NotificationPayload) error {
	taskID := notification.TaskID
	// Идемпотентность: записываем taskID, чтобы пропустить повторную доставку одного и того же уведомления
	tag, errExec := p.db.Exec(ctx, "INSERT INTO processed_notifications (task_id) VALUES ($1) ON CONFLICT DO NOTHING", taskID)
	if errExec != nil {
		p.logger.Error("Idempotency check failed", zap.Error(errExec), zap.String("task_id", taskID))
		return fmt.Errorf("idempotency check error: %w", errExec)
	}
	if tag.RowsAffected() == 0 {
		p.logger.Info("Duplicate notification detected, skipping processing", zap.String("task_id", taskID))
		return nil
	}

	// Извлекаем PublishedStoryID (если применимо)
	var publishedStoryID uuid.UUID
	var errParseID error
	if notification.PublishedStoryID != "" {
		publishedStoryID, errParseID = uuid.Parse(notification.PublishedStoryID)
		if errParseID != nil {
			p.logger.Error("Invalid PublishedStoryID in notification", zap.Error(errParseID), zap.String("task_id", taskID))
			// Если ID невалиден, мы не можем привязать ошибку к истории, но сообщение обработано
			// TODO: Возможно, отправлять в DLQ?
			return nil // Ack
		}
	} else if notification.StoryConfigID != "" { // Fallback для старых типов
		// Для Narrator - используем StoryConfigID как publishedStoryID (хотя это не совсем верно для PublishedStory)
		// В случае неизвестного типа, попробуем StoryConfigID, если он есть
		storyConfigID, errParseCfgID := uuid.Parse(notification.StoryConfigID)
		if errParseCfgID == nil {
			publishedStoryID = storyConfigID
			p.logger.Warn("Using StoryConfigID as PublishedStoryID for unknown notification type handling", zap.String("task_id", taskID))
		} // else: если и его нет, publishedStoryID останется нулевым
	}

	// Делаем роутинг через зарегистрированные обработчики
	if handler, ok := p.handlers[notification.PromptType]; ok {
		err := handler.Handle(ctx, notification, publishedStoryID)
		if err != nil {
			p.logger.Error("Error handling notification", zap.Error(err), zap.String("task_id", taskID))
			// Обработчик должен сам выставить статус Error, если нужно. NACK может привести к повторной обработке
			// return err // NACK
		}
		return nil // Ack
	}
	p.logger.Warn("Получен неизвестный тип уведомления", zap.String("prompt_type", string(notification.PromptType)), zap.String("task_id", taskID))
	return nil // Ack
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
		err := p.publishedRepo.UpdateStatusFlagsAndDetails(ctx, p.db, publishedStoryID, sharedModels.StatusError, false, false, &notification.ErrorDetails, nil)
		if err != nil {
			log.Error("Failed to update published story status to Error after initial scene generation failure", zap.Error(err))
		}
		// Уведомляем клиента и отправляем Push
		userID, _ := uuid.Parse(notification.UserID)
		go p.publishClientStoryUpdateOnError(publishedStoryID, userID, notification.ErrorDetails)
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
			go p.publishClientGameStateUpdateOnSuccess(gameState, scene.ID)
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

		// Обновляем статус и флаг AreImagesPending в транзакции
		txImg, errTxImg := p.db.Begin(ctx)
		if errTxImg != nil {
			log.Error("Failed to begin transaction for updating AreImagesPending flag", zap.Error(errTxImg))
			return fmt.Errorf("failed to begin transaction for updating AreImagesPending flag: %w", errTxImg)
		}
		if errUpdate := p.publishedRepo.UpdateStatusFlagsAndDetails(ctx, txImg, publishedStoryID, newStatus, storyBeforeUpdate.IsFirstScenePending, false, nil, nil); errUpdate != nil {
			log.Error("Failed to update status and AreImagesPending flag in transaction", zap.Error(errUpdate))
			_ = txImg.Rollback(ctx)
			return fmt.Errorf("failed to update status and AreImagesPending flag: %w", errUpdate)
		}
		if errCommit := txImg.Commit(ctx); errCommit != nil {
			log.Error("Failed to commit transaction for updating AreImagesPending flag", zap.Error(errCommit))
			return fmt.Errorf("failed to commit transaction for updating AreImagesPending flag: %w", errCommit)
		}
		storyBeforeUpdate.AreImagesPending = false
		storyBeforeUpdate.Status = newStatus // Обновляем локально для дальнейшей логики

		// После успешного коммита: отправка задачи первой сцены, если нужно
		if newStatus == sharedModels.StatusFirstScenePending {
			log.Info("Publishing task for first scene generation after images ready...")
			if errPublish := p.publishFirstSceneTaskInternal(ctx, storyBeforeUpdate); errPublish != nil {
				log.Error("Failed to publish first scene task after images became ready", zap.Error(errPublish))
			}
		}

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
				go p.publishClientStoryUpdateOnReady(storyBeforeUpdate) // Используем КОПИЮ storyBeforeUpdate!
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

		dbCtxErr, cancelErr := context.WithTimeout(ctx, 10*time.Second)
		defer cancelErr()

		// Mark the specific image reference as failed by saving an empty URL
		if notification.ImageReference != "" {
			if errSaveRef := p.imageReferenceRepo.SaveOrUpdateImageReference(dbCtxErr, notification.ImageReference, ""); errSaveRef != nil {
				p.logger.Error("Failed to mark image reference as erroneous (saved empty URL)",
					append(logFields, zap.Error(errSaveRef))...,
				)
				// Non-critical, log and continue
			} else {
				p.logger.Info("Successfully marked image reference as erroneous (saved empty URL)",
					logFields...,
				)
			}
		}

		errMsgForStory := fmt.Sprintf("Image generation task %s failed for story %s, reference %s. Details: %s", notification.TaskID, publishedStoryID, notification.ImageReference, notification.ErrorDetails)
		if errUpd := p.publishedRepo.UpdateStatusAndError(dbCtxErr, p.db, publishedStoryID, sharedModels.StatusError, &errMsgForStory); errUpd != nil {
			p.logger.Error("Failed to update story status to Error after image generation failure", append(logFields, zap.Error(errUpd))...)
			return fmt.Errorf("failed to update story %s to error after image gen failure (task %s): %w", publishedStoryID, notification.TaskID, errUpd)
		}
		if uid, errUID := uuid.Parse(notification.UserID); errUID == nil {
			p.publishClientStoryUpdateOnError(publishedStoryID, uid, errMsgForStory)
			p.publishPushOnError(ctx, publishedStoryID, uid, errMsgForStory, constants.PushEventTypeStoryError)
		}
		return nil // Ack, так как статус в БД обновлен (или попытка была сделана)
	}

	// Обработка успешной генерации
	if notification.Status == sharedMessaging.NotificationStatusSuccess {
		// Проверка наличия URL и референса
		if notification.ImageURL == nil || *notification.ImageURL == "" {
			errMsg := fmt.Sprintf("CRITICAL: Image generation for story %s (task %s, ref %s) succeeded but returned empty/nil URL.", publishedStoryID, notification.TaskID, notification.ImageReference)
			p.logger.Error(errMsg, logFields...)
			// Немедленно выставляем Error
			dbCtxErr, cancelErr := context.WithTimeout(ctx, 10*time.Second)
			defer cancelErr()
			if errUpd := p.publishedRepo.UpdateStatusAndError(dbCtxErr, p.db, publishedStoryID, sharedModels.StatusError, &errMsg); errUpd != nil {
				p.logger.Error("Failed to update story status to Error after empty image URL", append(logFields, zap.Error(errUpd))...)
				return fmt.Errorf("failed to update story %s to error (empty URL): %w", publishedStoryID, errUpd)
			}
			// Уведомляем клиента об ошибке
			if uid, errUID := uuid.Parse(notification.UserID); errUID == nil {
				p.publishClientStoryUpdateOnError(publishedStoryID, uid, errMsg)
				p.publishPushOnError(ctx, publishedStoryID, uid, errMsg, constants.PushEventTypeStoryError)
			}
			return nil // Ack, статус обновлен
		}
		if notification.ImageReference == "" {
			errMsg := fmt.Sprintf("CRITICAL: Image generation for story %s (task %s, url %s) succeeded but returned empty ImageReference.", publishedStoryID, notification.TaskID, *notification.ImageURL)
			p.logger.Error(errMsg, logFields...)
			// Немедленно выставляем Error
			dbCtxErr, cancelErr := context.WithTimeout(ctx, 10*time.Second)
			defer cancelErr()
			if errUpd := p.publishedRepo.UpdateStatusAndError(dbCtxErr, p.db, publishedStoryID, sharedModels.StatusError, &errMsg); errUpd != nil {
				p.logger.Error("Failed to update story status to Error after empty image reference", append(logFields, zap.Error(errUpd))...)
				return fmt.Errorf("failed to update story %s to error (empty ref): %w", publishedStoryID, errUpd)
			}
			// Уведомляем клиента об ошибке
			if uid, errUID := uuid.Parse(notification.UserID); errUID == nil {
				p.publishClientStoryUpdateOnError(publishedStoryID, uid, errMsg)
				p.publishPushOnError(ctx, publishedStoryID, uid, errMsg, constants.PushEventTypeStoryError)
			}
			return nil // Ack, статус обновлен
		}

		imageURL := *notification.ImageURL
		imageReference := notification.ImageReference
		p.logger.Info("Image generated successfully, saving URL", append(logFields, zap.String("image_url", imageURL))...)

		dbCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()

		// Версионирование схемы контента сцены
		var contentMap map[string]interface{}
		if errUnmarshal := json.Unmarshal([]byte(imageURL), &contentMap); errUnmarshal != nil {
			// Если не удалось распарсить, сохраняем raw
			contentMap = map[string]interface{}{"content": imageURL}
		}
		// Устанавливаем версию схемы JSON
		contentMap["schema_version"] = "v1"

		// 1. Сохраняем URL для текущего референса
		errSave := p.imageReferenceRepo.SaveOrUpdateImageReference(dbCtx, imageReference, imageURL)
		if errSave != nil {
			p.logger.Error("Failed to save image URL for reference, NACKing message", append(logFields, zap.String("image_url", imageURL), zap.Error(errSave))...)
			return fmt.Errorf("failed to save image URL for reference %s: %w", imageReference, errSave) // Nack
		}
		p.logger.Info("Image URL saved successfully for reference", logFields...)

		// 2. Определяем, какой счетчик декрементировать и проверяем готовность истории
		var opDecCharGen, opIncCharImg, opDecCardImg, opDecCharImg int
		var determinedDecrement bool = false // Флаг, что мы определили, какой счетчик уменьшать

		// Загружаем историю, чтобы проверить счетчики Pending
		publishedStory, errGetStory := p.publishedRepo.GetByID(dbCtx, p.db, publishedStoryID)
		if errGetStory != nil {
			p.logger.Error("Failed to get PublishedStory to determine image type based on counters", append(logFields, zap.Error(errGetStory))...)
			// Не можем определить тип, Nack-аем
			return fmt.Errorf("failed to get story %s to determine image type: %w", publishedStoryID, errGetStory)
		}

		if imageReference == "cover" {
			p.logger.Info("Image identified as cover image.", logFields...)
			opDecCardImg = 1
			logFields = append(logFields, zap.String("decrementing", "PendingCardImgTasks (for cover)"))
			determinedDecrement = true
		} else {
			// Если не обложка, пытаемся определить по счетчикам
			if publishedStory.PendingCharImgTasks > 0 {
				p.logger.Info("Image assumed to be character image based on PendingCharImgTasks > 0.", logFields...)
				opDecCharImg = 1
				logFields = append(logFields, zap.String("decrementing", "PendingCharImgTasks"))
				determinedDecrement = true
			} else if publishedStory.PendingCardImgTasks > 0 {
				p.logger.Info("Image assumed to be card image based on PendingCharImgTasks = 0 and PendingCardImgTasks > 0.", logFields...)
				opDecCardImg = 1
				logFields = append(logFields, zap.String("decrementing", "PendingCardImgTasks (for card)"))
				determinedDecrement = true
			} else {
				// Оба счетчика 0, но результат пришел? Странно.
				p.logger.Warn("Received image result, but both PendingCharImgTasks and PendingCardImgTasks are 0. Assuming it's a late card image.", append(logFields, zap.Int("pending_char_img", publishedStory.PendingCharImgTasks), zap.Int("pending_card_img", publishedStory.PendingCardImgTasks))...)
				opDecCardImg = 1 // По умолчанию декрементируем счетчик карт
				logFields = append(logFields, zap.String("decrementing", "PendingCardImgTasks (assumed late card)"))
				determinedDecrement = true // Считаем, что определили
			}
		}

		// Проверяем, что хотя бы один счетчик был выбран для декремента
		if !determinedDecrement {
			// Этого не должно произойти при текущей логике, но на всякий случай
			p.logger.Error("CRITICAL: Failed to determine which counter to decrement for image result.", logFields...)
			// Не знаем, что делать, Nack
			return fmt.Errorf("failed to determine counter decrement for image %s (story %s)", imageReference, publishedStoryID)
		}

		p.logger.Info("Triggering story readiness check after image notification", logFields...)
		// Вызываем checkStoryReadinessAfterImage с определенными НОВОЙ логикой opDec... значениями
		go p.checkStoryReadinessAfterImage(context.Background(), publishedStoryID, opDecCharGen, opIncCharImg, opDecCardImg, opDecCharImg)

		// Если дошли сюда без ошибок сохранения URL, подтверждаем сообщение
		return nil // Ack
	}

	// Обрабатываем любые другие/неизвестные статусы (хотя ожидаются только Success/Error)
	p.logger.Warn("Received image notification with unexpected status, acking.", append(logFields, zap.String("status", string(notification.Status)))...)
	return nil // Ack
}

// --- Helper methods for publishing updates ---

func (p *NotificationProcessor) publishClientStoryUpdateOnError(storyID uuid.UUID, userID uuid.UUID, errorDetails string) {
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
func (p *NotificationProcessor) publishClientStoryUpdateOnReady(story *sharedModels.PublishedStory) {
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

func (p *NotificationProcessor) publishClientGameStateUpdateOnSuccess(gameState *sharedModels.PlayerGameState, sceneID uuid.UUID) {
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
	fallbackTitle := "Story Generation Error"
	fallbackBody := fmt.Sprintf("An error occurred during story generation: %s", errorDetails)
	if len(fallbackBody) > 150 {
		fallbackBody = fallbackBody[:147] + "..."
	}

	pushPayload := sharedModels.PushNotificationPayload{
		UserID: userID,
		Notification: sharedModels.PushNotification{
			Title: fallbackTitle,
			Body:  fallbackBody,
		},
		Data: map[string]string{
			"eventType":                      eventType,
			"publishedStoryId":               storyID.String(),
			constants.PushLocKey:             constants.PushLocKeyStoryError,
			constants.PushLocArgErrorDetails: errorDetails,
			constants.PushFallbackTitleKey:   fallbackTitle,
			constants.PushFallbackBodyKey:    fallbackBody,
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
func (p *NotificationProcessor) checkStoryReadinessAfterImage(ctx context.Context, publishedStoryID uuid.UUID, opDecCharGen, opIncCharImg, opDecCardImg, opDecCharImg int) {
	log := p.logger.With(
		zap.Stringer("publishedStoryID", publishedStoryID),
		zap.Int("opDecCharGen", opDecCharGen),
		zap.Int("opIncCharImg", opIncCharImg),
		zap.Int("opDecCardImg", opDecCardImg),
		zap.Int("opDecCharImg", opDecCharImg),
	)
	log.Info("Checking story readiness after image task completion...")

	// Используем UpdateCountersAndMaybeStatus для атомарного обновления счетчиков и статуса.
	// Значения opDecCardImg и opDecCharImg передаются из handleImageNotification
	// и уже учитывают тип сгенерированного изображения.

	// Используем временный контекст для обновления счетчиков/статуса
	updateCtx, cancelUpdate := context.WithTimeout(ctx, 15*time.Second)
	defer cancelUpdate()

	// Вызываем метод репозитория, который атомарно обновит счетчики (если передать > 0) и статус
	allTasksComplete, finalStatus, errUpdate := p.publishedRepo.UpdateCountersAndMaybeStatus(
		updateCtx,
		p.db, // Используем основной pool, т.к. метод должен быть атомарным
		publishedStoryID,
		opDecCharGen,             // Используем переданные значения
		opIncCharImg,             // Используем переданные значения
		opDecCardImg,             // Используем переданные значения
		opDecCharImg,             // Используем переданные значения
		sharedModels.StatusReady, // newStatusIfComplete
	)

	if errUpdate != nil {
		if errors.Is(errUpdate, sharedModels.ErrNotFound) {
			log.Warn("PublishedStory not found during counter update check, maybe deleted?")
		} else {
			log.Error("Failed to update counters and potentially status for story", zap.Error(errUpdate))
		}
		return // Ничего не делаем дальше при ошибке
	}

	log.Info("Counter/Status check complete after image generation",
		zap.Bool("allTasksComplete", allTasksComplete),
		zap.String("finalStatus", string(finalStatus)),
	)

	// Если все задачи завершены и статус стал Ready, устанавливаем шаг Complete и отправляем уведомления
	if allTasksComplete && finalStatus == sharedModels.StatusReady {
		log.Info("All initial generation tasks complete and status is Ready. Setting step to Complete and sending notifications.")

		var storyForNotifications *sharedModels.PublishedStory // Объявляем здесь, т.к. используется только в этом блоке
		stepComplete := sharedModels.StepComplete
		stepCtx, cancelStep := context.WithTimeout(ctx, 10*time.Second)
		defer cancelStep()

		// Обновляем только шаг, статус уже Ready. Устанавливаем флаги в false.
		if errUpdateStep := p.publishedRepo.UpdateStatusFlagsAndDetails(stepCtx, p.db, publishedStoryID, sharedModels.StatusReady, false, false, nil, &stepComplete); errUpdateStep != nil {
			log.Error("Failed to update InternalGenerationStep to Complete after all tasks finished", zap.Error(errUpdateStep))
			// Не критично, но логируем
		}

		// Получаем актуальное состояние истории для уведомлений
		notifyCtx, cancelNotify := context.WithTimeout(ctx, 10*time.Second)
		storyForNotifications, errGetNotify := p.publishedRepo.GetByID(notifyCtx, p.db, publishedStoryID)
		cancelNotify()
		if errGetNotify != nil {
			log.Error("Failed to get story for Ready notifications after image completion", zap.Error(errGetNotify))
		} else {
			// Отправляем уведомления
			go p.publishClientStoryUpdateOnReady(storyForNotifications)
			go p.publishPushNotificationForStoryReady(context.Background(), storyForNotifications) // Push может занять больше времени
		}
	} else if !allTasksComplete {
		// Если не все задачи завершены, InternalGenerationStep не меняется здесь.
		// Он будет изменен соответствующим обработчиком, когда запустится следующая фаза (например, handle_setup).
		log.Info("Initial generation tasks not yet complete, InternalGenerationStep remains unchanged by this handler.")
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

	// Распарсиваем Config
	var configData sharedModels.Config
	if errUnmarshalConfig := json.Unmarshal(story.Config, &configData); errUnmarshalConfig != nil {
		log.Error("Cannot publish first scene task: Failed to unmarshal Config JSON", zap.Error(errUnmarshalConfig))
		return fmt.Errorf("failed to unmarshal config JSON: %w", errUnmarshalConfig)
	}

	// Распарсиваем Setup
	var setupContent sharedModels.NovelSetupContent
	if errUnmarshalSetup := json.Unmarshal(story.Setup, &setupContent); errUnmarshalSetup != nil {
		log.Error("Cannot publish first scene task: Failed to unmarshal setup JSON", zap.Error(errUnmarshalSetup))
		return fmt.Errorf("failed to unmarshal setup JSON: %w", errUnmarshalSetup)
	}

	// Используем новый форматтер для UserInput
	combinedInputString := utils.FormatConfigAndSetupToString(configData, setupContent)

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
		PromptType:       sharedModels.PromptTypeStoryContinuation,
		PublishedStoryID: story.ID.String(),
		UserInput:        combinedInputString,
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

// <<< НАЧАЛО: Добавленный метод publishPushNotificationForStoryReady >>>
func (p *NotificationProcessor) publishPushNotificationForStoryReady(ctx context.Context, story *sharedModels.PublishedStory) {
	if story == nil {
		p.logger.Error("Cannot send push for story ready: story is nil")
		return
	}

	getAuthorName := func(userID uuid.UUID) string {
		authorName := "Unknown Author"
		if p.authClient != nil {
			authCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			userInfoMap, errAuth := p.authClient.GetUsersInfo(authCtx, []uuid.UUID{userID})
			cancel()
			if errAuth != nil {
				p.logger.Error("Failed to get author info map for push notification (story ready)", zap.Stringer("userID", userID), zap.Error(errAuth))
			} else if userInfo, ok := userInfoMap[userID]; ok {
				if userInfo.DisplayName != "" {
					authorName = userInfo.DisplayName
				} else if userInfo.Username != "" {
					authorName = userInfo.Username
				}
			} else {
				p.logger.Warn("Author info not found in map from auth service for push notification (story ready)", zap.Stringer("userID", userID))
			}
		} else {
			p.logger.Warn("authClient is nil in NotificationProcessor, cannot fetch author name for push notification")
		}
		return authorName
	}

	// Используем хелпер из shared/notifications
	payload, err := notifications.BuildStoryReadyPushPayload(story, getAuthorName)
	if err != nil {
		p.logger.Error("Failed to build push notification payload for story ready", zap.Error(err))
		return
	}

	if payload == nil {
		p.logger.Error("Push notification payload for story ready is nil, cannot publish", zap.String("publishedStoryID", story.ID.String()))
		return
	}

	pushCtx, pushCancel := context.WithTimeout(context.Background(), 15*time.Second) // Используем context.Background()
	defer pushCancel()
	if err := p.pushPub.PublishPushNotification(pushCtx, *payload); err != nil {
		p.logger.Error("Failed to publish push notification event for story ready",
			zap.String("userID", payload.UserID.String()),
			zap.String("publishedStoryID", story.ID.String()),
			zap.Error(err),
		)
	} else {
		p.logger.Info("Push notification event for story ready published successfully",
			zap.String("userID", payload.UserID.String()),
			zap.String("publishedStoryID", story.ID.String()),
		)
	}
}

// <<< КОНЕЦ: Добавленный метод publishPushNotificationForStoryReady >>>

// Note: Methods handleNarratorNotification, handleNovelSetupNotification, handleSceneGenerationNotification,
// handleImageNotification should be defined in their respective handle_*.go files.
// Removed placeholder/duplicate definitions from here.

// updateStorySetupAndCounters обновляет атомарно Setup, статус, флаги ожидания и счётчики задач для истории.
func (p *NotificationProcessor) updateStorySetupAndCounters(ctx context.Context, tx interfaces.DBTX, story *sharedModels.PublishedStory) error {
	// Сначала обновляем флаги и Setup
	if err := p.publishedRepo.UpdateStatusFlagsAndSetup(ctx, tx,
		story.ID,
		story.Status,
		story.Setup,
		story.IsFirstScenePending,
		story.AreImagesPending,
		nil,
	); err != nil {
		p.logger.Error("Failed to update status flags and setup", zap.String("storyID", story.ID.String()), zap.Error(err))
		return err
	}
	// Затем обновляем счётчики
	// Преобразуем булев флаг PendingCharGenTasks в int
	var pendingCharGenCount int
	if story.PendingCharGenTasks {
		pendingCharGenCount = 1
	}
	if err := p.publishedRepo.UpdateSetupStatusAndCounters(ctx, tx,
		story.ID,
		story.Setup,
		story.Status,
		pendingCharGenCount,
		story.PendingCardImgTasks,
		story.PendingCharImgTasks,
		nil,
	); err != nil {
		p.logger.Error("Failed to update setup status and counters", zap.String("storyID", story.ID.String()), zap.Error(err))
		return err
	}
	return nil
}
