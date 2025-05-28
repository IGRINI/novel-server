package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"novel-server/shared/constants"
	interfaces "novel-server/shared/interfaces"
	sharedMessaging "novel-server/shared/messaging"
	sharedModels "novel-server/shared/models"
	"novel-server/shared/notifications"
	"novel-server/shared/utils"
)

// SetupPromptResult - структура для разбора JSON результата от PromptTypeStorySetup
type SetupPromptResult struct {
	Result        string `json:"res"` // Текст первой сцены (повествование)
	PreviewPrompt string `json:"prv"` // Промпт для генерации превью-изображения
}

// handleNovelSetupNotification обрабатывает результат задачи генерации setup
func (p *NotificationProcessor) handleNovelSetupNotification(ctx context.Context, notification sharedMessaging.NotificationPayload, publishedStoryID uuid.UUID) error {
	taskID := notification.TaskID
	log := p.logger.With(
		zap.String("task_id", taskID),
		zap.String("published_story_id", publishedStoryID.String()),
		zap.String("prompt_type", string(notification.PromptType)),
	)
	log.Info("Processing novel setup result")

	// Проверяем статус истории
	publishedStory, err := p.ensureStoryStatus(ctx, publishedStoryID, sharedModels.StatusSetupPending)
	if err != nil {
		return err
	}
	if publishedStory == nil {
		return nil // История не в нужном статусе
	}

	// Обработка ошибки
	if notification.Status == sharedMessaging.NotificationStatusError {
		userID, _ := uuid.Parse(notification.UserID)
		return p.errorHandler.HandleStoryError(ctx, p.db, publishedStoryID, userID, notification.ErrorDetails, constants.WSEventStoryError)
	}

	// Обработка успешного результата
	if notification.Status == sharedMessaging.NotificationStatusSuccess {
		log.Info("Novel setup task successful, processing results.")

		// Получаем результат генерации
		genResult, genErr := p.genResultRepo.GetByTaskID(ctx, taskID)
		if genErr != nil {
			userID, _ := uuid.Parse(notification.UserID)
			errMsg := fmt.Sprintf("failed to fetch setup result: %v", genErr)
			return p.errorHandler.HandleStoryError(ctx, p.db, publishedStoryID, userID, errMsg, constants.WSEventStoryError)
		}

		if genResult.Error != "" {
			log.Warn("GenerationResult for setup indicates an error", zap.String("gen_error", genResult.Error))
			userID, _ := uuid.Parse(notification.UserID)
			errDetails := fmt.Sprintf("setup generation error: %s", genResult.Error)
			return p.errorHandler.HandleStoryError(ctx, p.db, publishedStoryID, userID, errDetails, constants.WSEventStoryError)
		}

		// Сохраняем результат setup
		setupText := genResult.GeneratedText
		log.Info("Setup generated successfully", zap.String("setup_preview", setupText[:min(100, len(setupText))]))

		// Используем атомарную операцию для обновления состояния
		return p.txHelper.WithTransaction(ctx, func(ctx context.Context, tx interfaces.DBTX) error {
			// Сохраняем setup в initial_narrative первой сцены
			err := p.saveSetupToInitialScene(ctx, tx, publishedStoryID, setupText)
			if err != nil {
				return fmt.Errorf("failed to save setup to initial scene: %w", err)
			}

			// Определяем следующий шаг
			nextStep := p.stepManager.DetermineNextStep(publishedStory)
			nextStatus := p.stepManager.DetermineStatusFromStep(nextStep)

			// Атомарно обновляем шаг и статус
			expectedStep := sharedModels.StepSetupGeneration
			updatedStory, err := p.stepManager.AtomicUpdateStepAndStatus(
				ctx, tx, publishedStoryID,
				&expectedStep, // ожидаемый текущий шаг
				nextStep,      // новый шаг
				nextStatus,    // новый статус
			)
			if err != nil {
				return fmt.Errorf("failed to update story step and status: %w", err)
			}

			// Отправляем соответствующие задачи после коммита
			return p.dispatchTasksAfterSetup(ctx, updatedStory)
		})
	}

	log.Warn("Received setup notification with unexpected status",
		zap.String("status", string(notification.Status)))
	return nil
}

// saveSetupToInitialScene сохраняет setup в initial_narrative первой сцены
func (p *NotificationProcessor) saveSetupToInitialScene(
	ctx context.Context,
	tx interfaces.DBTX,
	storyID uuid.UUID,
	setupText string,
) error {
	// Декодируем setup content
	setupData, err := decodeStrictJSON[sharedModels.NovelSetupContent](setupText)
	if err != nil {
		return fmt.Errorf("failed to decode setup content: %w", err)
	}

	// Кодируем setup для сохранения
	setupBytes, err := json.Marshal(setupData)
	if err != nil {
		return fmt.Errorf("failed to marshal setup: %w", err)
	}

	// Получаем текущую историю
	story, err := p.publishedRepo.GetByID(ctx, tx, storyID)
	if err != nil {
		return fmt.Errorf("failed to get story: %w", err)
	}

	// Обновляем setup в истории
	if err := p.publishedRepo.UpdateConfigAndSetupAndStatus(ctx, tx, storyID, story.Config, json.RawMessage(setupBytes), story.Status); err != nil {
		return fmt.Errorf("failed to update setup in database: %w", err)
	}

	// Создаем начальную сцену если её нет
	existingScenes, err := p.sceneRepo.ListByStoryID(ctx, tx, storyID)
	if err != nil {
		return fmt.Errorf("failed to check existing scenes: %w", err)
	}

	if len(existingScenes) == 0 {
		initialScene := &sharedModels.StoryScene{
			ID:               uuid.New(),
			PublishedStoryID: storyID,
			StateHash:        sharedModels.InitialStateHash,
			Content:          nil, // Будет заполнено при генерации JSON
			CreatedAt:        time.Now().UTC(),
		}

		// Сохраняем начальную сцену
		if err := p.sceneRepo.Create(ctx, tx, initialScene); err != nil {
			return fmt.Errorf("failed to create initial scene: %w", err)
		}

		p.logger.Info("Initial scene created",
			zap.String("story_id", storyID.String()),
			zap.String("scene_id", initialScene.ID.String()))
	}

	p.logger.Info("Setup saved successfully",
		zap.String("story_id", storyID.String()),
		zap.String("setup_preview", setupText[:min(50, len(setupText))]))

	return nil
}

// dispatchTasksAfterSetup отправляет задачи после генерации setup
func (p *NotificationProcessor) dispatchTasksAfterSetup(ctx context.Context, story *sharedModels.PublishedStory) error {
	// Определяем следующий шаг
	nextStep := p.stepManager.DetermineNextStep(story)

	if nextStep == nil {
		p.logger.Info("No next step after setup, story is complete",
			zap.String("story_id", story.ID.String()))
		return nil
	}

	switch *nextStep {
	case sharedModels.StepInitialSceneJSON:
		// Отправляем задачу генерации JSON для начальной сцены
		return p.dispatchInitialSceneJSONTask(ctx, story)
	case sharedModels.StepCoverImageGeneration:
		// Отправляем задачу генерации обложки
		return p.dispatchCoverImageTask(ctx, story)
	default:
		p.logger.Warn("Unexpected next step after setup",
			zap.String("story_id", story.ID.String()),
			zap.String("next_step", string(*nextStep)))
		return fmt.Errorf("unexpected next step after setup: %s", *nextStep)
	}
}

// dispatchInitialSceneJSONTask отправляет задачу JSON генерации для первой сцены
func (p *NotificationProcessor) dispatchInitialSceneJSONTask(ctx context.Context, story *sharedModels.PublishedStory) error {
	if story.Config == nil {
		return fmt.Errorf("cannot dispatch JSON generation task: config is nil")
	}
	if story.Setup == nil {
		return fmt.Errorf("cannot dispatch JSON generation task: setup is nil")
	}

	config, err := decodeStrictJSON[sharedModels.Config](string(story.Config))
	if err != nil {
		return fmt.Errorf("failed to decode config for JSON generation task: %w", err)
	}

	setupContent, err := decodeStrictJSON[sharedModels.NovelSetupContent](string(story.Setup))
	if err != nil {
		return fmt.Errorf("failed to decode setup for JSON generation task: %w", err)
	}

	// Получаем текст повествования из StorySummarySoFar (это и есть текст первой сцены)
	narrativeText := setupContent.StorySummarySoFar
	if narrativeText == "" {
		return fmt.Errorf("no narrative text found for JSON generation")
	}

	// Создаем setupMap для форматтера
	setupMap := make(map[string]interface{})
	// Добавляем цель протагониста если есть
	if strings.Contains(config.PlayerPrefs.WorldLore, "Protagonist Goal:") {
		parts := strings.Split(config.PlayerPrefs.WorldLore, "Protagonist Goal:")
		if len(parts) > 1 {
			goal := strings.TrimSpace(parts[1])
			if idx := strings.Index(goal, "\n"); idx != -1 {
				goal = strings.TrimSpace(goal[:idx])
			}
			setupMap["protagonist_goal"] = goal
		}
	}

	// Используем существующий форматтер
	userInput, err := utils.FormatInputForJsonGeneration(config, setupContent, setupMap, narrativeText)
	if err != nil {
		return fmt.Errorf("failed to format input for JSON generation: %w", err)
	}

	taskID := uuid.New().String()
	payload := sharedMessaging.GenerationTaskPayload{
		TaskID:           taskID,
		UserID:           story.UserID.String(),
		PromptType:       sharedModels.PromptTypeJsonGeneration,
		UserInput:        userInput,
		PublishedStoryID: story.ID.String(),
		StateHash:        sharedModels.InitialStateHash,
		Language:         story.Language,
	}

	if err := p.publishTask(payload); err != nil {
		return fmt.Errorf("failed to publish JSON generation task: %w", err)
	}

	p.logger.Info("JSON generation task dispatched successfully",
		zap.String("story_id", story.ID.String()),
		zap.String("task_id", taskID))

	return nil
}

// dispatchCoverImageTask отправляет задачу генерации обложки
func (p *NotificationProcessor) dispatchCoverImageTask(ctx context.Context, story *sharedModels.PublishedStory) error {
	if story.Setup == nil {
		return fmt.Errorf("cannot dispatch cover image task: setup is nil")
	}

	setup, err := decodeStrictJSON[sharedModels.NovelSetupContent](string(story.Setup))
	if err != nil {
		return fmt.Errorf("failed to decode setup for cover image task: %w", err)
	}

	// Используем StoryPreviewImagePrompt из setup
	imagePrompt := setup.StoryPreviewImagePrompt
	if imagePrompt == "" {
		p.logger.Warn("No story preview image prompt in setup, skipping cover image generation",
			zap.String("story_id", story.ID.String()))
		return nil
	}

	taskID := uuid.New().String()
	payload := sharedMessaging.CharacterImageTaskPayload{
		TaskID:           taskID,
		UserID:           story.UserID.String(),
		PublishedStoryID: story.ID,
		CharacterID:      uuid.Nil, // Не персонаж, а обложка
		CharacterName:    "Cover",
		ImageReference:   fmt.Sprintf("cover_%s", story.ID.String()),
		Prompt:           imagePrompt,
		Ratio:            "3:2", // Стандартное соотношение для обложек
	}

	if err := p.characterImageTaskBatchPub.PublishCharacterImageTaskBatch(ctx, sharedMessaging.CharacterImageTaskBatchPayload{
		BatchID:          taskID,
		PublishedStoryID: story.ID,
		Tasks:            []sharedMessaging.CharacterImageTaskPayload{payload},
	}); err != nil {
		return fmt.Errorf("failed to publish cover image task: %w", err)
	}

	p.logger.Info("Cover image task dispatched successfully",
		zap.String("story_id", story.ID.String()),
		zap.String("task_id", taskID))

	return nil
}

// handleNovelSetupErrorTx - версия handleNovelSetupError, работающая внутри транзакции
func (p *NotificationProcessor) handleNovelSetupErrorTx(ctx context.Context, tx interfaces.DBTX, story *sharedModels.PublishedStory, errorDetails string, logger *zap.Logger) error {
	_ = logger
	return p.handleStoryError(ctx, story.ID, story.UserID.String(), errorDetails, constants.WSEventSetupError)
}

// publishPushNotificationForSetupPending использует notifications.BuildSetupPendingPushPayload
func (p *NotificationProcessor) publishPushNotificationForSetupPending(ctx context.Context, story *sharedModels.PublishedStory) {
	payload, err := notifications.BuildSetupPendingPushPayload(story)
	if err != nil {
		p.logger.Error("Failed to build push notification payload for setup pending", zap.Error(err))
		return
	}

	if err := p.pushPub.PublishPushNotification(ctx, *payload); err != nil {
		p.logger.Error("Failed to publish push notification event for setup pending",
			zap.String("userID", payload.UserID.String()),
			zap.String("publishedStoryID", story.ID.String()),
			zap.Error(err),
		)
	} else {
		p.logger.Info("Push notification event for setup pending published successfully",
			zap.String("userID", payload.UserID.String()),
			zap.String("publishedStoryID", story.ID.String()),
		)
	}
}

// <<< НОВОЕ: Функция для запуска задач ПОСЛЕ коммита >>>
func (p *NotificationProcessor) dispatchTasksAfterSetupCommit(storyID uuid.UUID, setupResult SetupPromptResult) {
	log := p.logger.With(zap.String("published_story_id", storyID.String()))
	log.Info("Dispatching next task via GameLoopService (after commit)")

	// Создаем новый контекст для вызова сервиса, т.к. транзакционный контекст завершен
	dispatchCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// После setup создаём задачу на генерацию JSON для первой сцены
	freshStory, errGet := p.publishedRepo.GetByID(dispatchCtx, p.db, storyID)
	if errGet != nil {
		log.Error("Failed to get story for JSON generation task after setup", zap.Error(errGet))
	} else {
		jsonTaskID := uuid.New().String()
		jsonPayload := sharedMessaging.GenerationTaskPayload{
			TaskID:           jsonTaskID,
			UserID:           freshStory.UserID.String(),
			PromptType:       sharedModels.PromptTypeJsonGeneration,
			UserInput:        setupResult.Result,
			PublishedStoryID: storyID.String(),
			StateHash:        sharedModels.InitialStateHash,
			Language:         freshStory.Language,
		}
		if errPub := p.publishTask(jsonPayload); errPub != nil {
			log.Error("Failed to publish JsonGeneration task after setup", zap.Error(errPub), zap.String("task_id", jsonTaskID))
		} else {
			log.Info("JsonGeneration task published after setup", zap.String("task_id", jsonTaskID))
		}
	}

	// Уведомления клиенту
	if freshStory != nil {
		go p.publishStoryUpdateViaRabbitMQ(context.Background(), freshStory, constants.WSEventSetupGenerated, nil)
		go p.publishPushNotificationForSetupPending(context.Background(), freshStory)
	}
}
