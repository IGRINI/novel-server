package messaging

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"novel-server/shared/interfaces"
	sharedMessaging "novel-server/shared/messaging"
	sharedModels "novel-server/shared/models"
	"novel-server/shared/utils"
	// "novel-server/shared/utils" // Может понадобиться позже
)

// publishTask публикует задачу генерации и возвращает ошибку при неудаче
func (p *NotificationProcessor) publishTask(payload sharedMessaging.GenerationTaskPayload) error {
	if payload.UserInput == "" {
		p.logger.Error("CRITICAL: Cannot publish task because UserInput is empty after formatting",
			zap.String("publishedStoryID", payload.PublishedStoryID),
			zap.String("taskID", payload.TaskID),
			zap.String("promptType", string(payload.PromptType)))
		return fmt.Errorf("empty UserInput for TaskID %s", payload.TaskID)
	}
	taskCtx, taskCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer taskCancel()
	if errPub := p.taskPub.PublishGenerationTask(taskCtx, payload); errPub != nil {
		p.logger.Error("CRITICAL: Failed to publish task", zap.Error(errPub),
			zap.String("task_id", payload.TaskID),
			zap.String("promptType", string(payload.PromptType)))
		return fmt.Errorf("failed to publish task %s: %w", payload.TaskID, errPub)
	}
	p.logger.Info("Task published successfully", zap.String("task_id", payload.TaskID), zap.String("promptType", string(payload.PromptType)))
	return nil
}

// handleCharacterGenerationResult обрабатывает результат задачи генерации персонажей
func (p *NotificationProcessor) handleCharacterGenerationResult(ctx context.Context, notification sharedMessaging.NotificationPayload, publishedStoryID uuid.UUID) error {
	p.logger.Info("Processing character generation result",
		zap.String("story_id", publishedStoryID.String()),
		zap.String("status", string(notification.Status)))

	// Обработка ошибки
	if notification.Status == sharedMessaging.NotificationStatusError {
		userID, _ := uuid.Parse(notification.UserID)
		return p.errorHandler.HandleStoryError(ctx, p.db, publishedStoryID, userID, notification.ErrorDetails, "character_generation_error")
	}

	// Используем атомарную операцию для обновления состояния
	return p.txHelper.WithTransaction(ctx, func(ctx context.Context, tx interfaces.DBTX) error {
		// Получаем текущее состояние с блокировкой
		publishedStory, err := p.stepManager.getStoryForUpdate(ctx, tx, publishedStoryID)
		if err != nil {
			return fmt.Errorf("failed to get story for update: %w", err)
		}

		// Проверяем, что мы находимся в правильном состоянии
		if publishedStory.InternalGenerationStep == nil ||
			*publishedStory.InternalGenerationStep != sharedModels.StepCharacterGeneration {
			p.logger.Warn("Story not in expected step for character generation",
				zap.String("story_id", publishedStoryID.String()),
				zap.Any("current_step", publishedStory.InternalGenerationStep))
			return nil // Игнорируем, если не в нужном состоянии
		}

		// Декрементируем счетчик задач генерации персонажей
		if publishedStory.PendingCharGenTasks > 0 {
			publishedStory.PendingCharGenTasks--
		}

		// Определяем следующий шаг
		nextStep := p.stepManager.DetermineNextStep(publishedStory)
		nextStatus := p.stepManager.DetermineStatusFromStep(nextStep)

		// Атомарно обновляем шаг и статус
		expectedStep := sharedModels.StepCharacterGeneration
		updatedStory, err := p.stepManager.AtomicUpdateStepAndStatus(
			ctx, tx, publishedStoryID,
			&expectedStep, // ожидаемый текущий шаг
			nextStep,      // новый шаг
			nextStatus,    // новый статус
		)
		if err != nil {
			return fmt.Errorf("failed to update story step and status: %w", err)
		}

		// Отправляем задачи после успешного коммита
		return p.dispatchTasksAfterCharacterGeneration(ctx, updatedStory)
	})
}

// dispatchTasksAfterCharacterGeneration отправляет задачи после генерации персонажей
func (p *NotificationProcessor) dispatchTasksAfterCharacterGeneration(
	ctx context.Context,
	story *sharedModels.PublishedStory,
) error {
	// Если есть ожидающие задачи генерации изображений персонажей, отправляем их
	if story.PendingCharImgTasks > 0 {
		return p.dispatchCharacterImageTasks(ctx, story)
	}

	// Если есть ожидающие задачи генерации изображений карт, отправляем их
	if story.PendingCardImgTasks > 0 {
		return p.dispatchCardImageTasks(ctx, story)
	}

	// Если нет ожидающих задач, переходим к следующему этапу
	if story.InternalGenerationStep != nil {
		switch *story.InternalGenerationStep {
		case sharedModels.StepCardImageGeneration, sharedModels.StepCharacterImageGeneration:
			// Ждем завершения генерации изображений
			return nil
		case sharedModels.StepSetupGeneration:
			// Отправляем задачу генерации setup
			return p.dispatchSetupTask(ctx, story)
		case sharedModels.StepInitialSceneJSON:
			// Отправляем задачу JSON генерации
			return p.dispatchJSONGenerationTask(ctx, story)
		}
	}

	return nil
}

// dispatchCharacterImageTasks отправляет задачи генерации изображений персонажей
func (p *NotificationProcessor) dispatchCharacterImageTasks(ctx context.Context, story *sharedModels.PublishedStory) error {
	if story.Setup == nil {
		return fmt.Errorf("cannot dispatch character image tasks: setup is nil")
	}

	setupContent, err := decodeStrictJSON[sharedModels.NovelSetupContent](string(story.Setup))
	if err != nil {
		return fmt.Errorf("failed to decode setup for character image tasks: %w", err)
	}

	if len(setupContent.Characters) == 0 {
		p.logger.Info("No characters found for image generation", zap.String("story_id", story.ID.String()))
		return nil
	}

	// Создаем задачи для каждого персонажа
	var characterTasks []sharedMessaging.CharacterImageTaskPayload
	for _, char := range setupContent.Characters {
		if char.ImageReferenceName != "" && char.ImagePromptDescriptor != "" {
			charTaskID := uuid.New().String()
			payload := sharedMessaging.CharacterImageTaskPayload{
				TaskID:           charTaskID,
				UserID:           story.UserID.String(),
				PublishedStoryID: story.ID,
				CharacterID:      uuid.Nil, // Пока не используем конкретный ID персонажа
				CharacterName:    char.Name,
				ImageReference:   fmt.Sprintf("ch_%s", char.ImageReferenceName),
				Prompt:           char.ImagePromptDescriptor,
				Ratio:            "2:3", // Стандартное соотношение для персонажей
			}
			characterTasks = append(characterTasks, payload)
		} else {
			p.logger.Warn("Skipping character image generation due to missing data",
				zap.String("char_name", char.Name),
				zap.String("story_id", story.ID.String()))
		}
	}

	if len(characterTasks) == 0 {
		p.logger.Info("No valid character image tasks to dispatch", zap.String("story_id", story.ID.String()))
		return nil
	}

	// Отправляем батч задач
	batchID := uuid.New().String()
	batchPayload := sharedMessaging.CharacterImageTaskBatchPayload{
		BatchID:          batchID,
		PublishedStoryID: story.ID,
		Tasks:            characterTasks,
	}

	if err := p.characterImageTaskBatchPub.PublishCharacterImageTaskBatch(ctx, batchPayload); err != nil {
		return fmt.Errorf("failed to publish character image task batch: %w", err)
	}

	p.logger.Info("Character image tasks dispatched successfully",
		zap.String("story_id", story.ID.String()),
		zap.String("batch_id", batchID),
		zap.Int("task_count", len(characterTasks)))

	return nil
}

// dispatchCardImageTasks отправляет задачи генерации изображений карт
func (p *NotificationProcessor) dispatchCardImageTasks(ctx context.Context, story *sharedModels.PublishedStory) error {
	// Проверяем есть ли карты в setup
	if story.Setup == nil {
		p.logger.Info("No setup found for card image generation", zap.String("story_id", story.ID.String()))
		return nil
	}

	setupContent, err := decodeStrictJSON[sharedModels.NovelSetupContent](string(story.Setup))
	if err != nil {
		p.logger.Warn("Failed to decode setup for card image tasks, skipping",
			zap.String("story_id", story.ID.String()),
			zap.Error(err))
		return nil
	}

	// Проверяем есть ли карты в setup (пока карты не реализованы в NovelSetupContent)
	// Когда карты будут добавлены в модель, здесь будет реальная логика
	if len(setupContent.Characters) == 0 {
		p.logger.Info("No cards found in setup for image generation", zap.String("story_id", story.ID.String()))
		return nil
	}

	// Пока карты не реализованы в системе, просто логируем и возвращаем nil
	// Это не ошибка, а ожидаемое поведение до реализации карт
	p.logger.Info("Card image generation not yet implemented, skipping",
		zap.String("story_id", story.ID.String()),
		zap.Int("pending_card_tasks", story.PendingCardImgTasks))

	return nil
}

// dispatchSetupTask отправляет задачу генерации setup
func (p *NotificationProcessor) dispatchSetupTask(ctx context.Context, story *sharedModels.PublishedStory) error {
	if story.Config == nil {
		return fmt.Errorf("cannot dispatch setup task: config is nil")
	}

	config, err := decodeStrictJSON[sharedModels.Config](string(story.Config))
	if err != nil {
		return fmt.Errorf("failed to decode config for setup task: %w", err)
	}

	// Используем существующий форматтер
	userInput := utils.FormatConfigToString(config, story.IsAdultContent)
	if userInput == "" {
		return fmt.Errorf("generated empty user input for setup task")
	}

	taskID := uuid.New().String()
	payload := sharedMessaging.GenerationTaskPayload{
		TaskID:           taskID,
		UserID:           story.UserID.String(),
		PromptType:       sharedModels.PromptTypeStorySetup,
		UserInput:        userInput,
		PublishedStoryID: story.ID.String(),
		Language:         story.Language,
	}

	if err := p.publishTask(payload); err != nil {
		return fmt.Errorf("failed to publish setup task: %w", err)
	}

	p.logger.Info("Setup task dispatched successfully",
		zap.String("story_id", story.ID.String()),
		zap.String("task_id", taskID))

	return nil
}

// dispatchJSONGenerationTask отправляет задачу JSON генерации
func (p *NotificationProcessor) dispatchJSONGenerationTask(ctx context.Context, story *sharedModels.PublishedStory) error {
	if story.Setup == nil {
		return fmt.Errorf("cannot dispatch JSON generation task: setup is nil")
	}

	// Получаем setup для формирования входных данных
	setupText := string(story.Setup)
	if setupText == "" || setupText == "null" || setupText == "{}" {
		return fmt.Errorf("setup is empty for JSON generation task")
	}

	taskID := uuid.New().String()
	payload := sharedMessaging.GenerationTaskPayload{
		TaskID:           taskID,
		UserID:           story.UserID.String(),
		PromptType:       sharedModels.PromptTypeJsonGeneration,
		UserInput:        setupText,
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

// Вспомогательная функция для разыменования указателя на строку для логгирования
func PtrToString(s *sharedModels.InternalGenerationStep) string {
	if s == nil {
		return "<nil>"
	}
	return string(*s)
}
