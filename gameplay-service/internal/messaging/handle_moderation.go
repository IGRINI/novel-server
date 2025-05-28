package messaging

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"novel-server/shared/constants"
	"novel-server/shared/interfaces"
	sharedMessaging "novel-server/shared/messaging"
	sharedModels "novel-server/shared/models"
	"novel-server/shared/utils"
	// "novel-server/shared/utils" // Пока не используется, но может понадобиться
)

// BoolFromInt для кастомного анмаршалинга bool из int (0 или 1)
// или из стандартного bool (true/false)
// или из строки ("0", "1", "true", "false")
type BoolFromInt bool

// UnmarshalJSON реализует кастомную логику для BoolFromInt
func (b *BoolFromInt) UnmarshalJSON(data []byte) error {
	// Попробуем как стандартный bool
	var standardBool bool
	if err := json.Unmarshal(data, &standardBool); err == nil {
		*b = BoolFromInt(standardBool)
		return nil
	}

	// Попробуем как число (int)
	var num_i int
	if err_i := json.Unmarshal(data, &num_i); err_i == nil {
		*b = BoolFromInt(num_i != 0)
		return nil
	}

	// Попробуем как число (float64, так как json.Unmarshal по умолчанию читает числа в float64)
	var num_f float64
	if err_f := json.Unmarshal(data, &num_f); err_f == nil {
		*b = BoolFromInt(num_f != 0)
		return nil
	}

	// Попробуем как строку и сконвертировать
	var str string
	if err_str := json.Unmarshal(data, &str); err_str == nil {
		switch str {
		case "1", "true", "TRUE":
			*b = true
			return nil
		case "0", "false", "FALSE":
			*b = false
			return nil
		}
	}

	return fmt.Errorf("cannot unmarshal %s into BoolFromInt (tried bool, number, and string representations '0','1','true','false')", string(data))
}

// moderationResultPayload представляет результат модерации контента
type moderationResultPayload struct {
	IsAppropriate bool   `json:"is_appropriate"`
	Reason        string `json:"reason,omitempty"`
}

func (p *NotificationProcessor) handleContentModerationResult(ctx context.Context, notification sharedMessaging.NotificationPayload, publishedStoryID uuid.UUID) error {
	taskID := notification.TaskID
	log := p.logger.With(
		zap.String("task_id", taskID),
		zap.String("published_story_id", publishedStoryID.String()),
		zap.String("prompt_type", string(notification.PromptType)),
	)
	log.Info("Processing content moderation result")

	// Проверяем статус истории
	publishedStory, err := p.ensureStoryStatus(ctx, publishedStoryID, sharedModels.StatusModerationPending)
	if err != nil {
		return err
	}
	if publishedStory == nil {
		return nil // История не в нужном статусе
	}

	// Обработка ошибки модерации
	if notification.Status == sharedMessaging.NotificationStatusError {
		userID, _ := uuid.Parse(notification.UserID)
		return p.errorHandler.HandleStoryError(ctx, p.db, publishedStoryID, userID, notification.ErrorDetails, constants.WSEventStoryError)
	}

	// Обработка успешной модерации
	if notification.Status == sharedMessaging.NotificationStatusSuccess {
		log.Info("Content moderation task successful, processing results.")

		// Получаем результат генерации
		genResult, genErr := p.genResultRepo.GetByTaskID(ctx, taskID)
		if genErr != nil {
			log.Error("Failed to get GenerationResult by TaskID for moderation", zap.Error(genErr))
			userID, _ := uuid.Parse(notification.UserID)
			errMsg := fmt.Sprintf("failed to fetch moderation result: %v", genErr)
			return p.errorHandler.HandleStoryError(ctx, p.db, publishedStoryID, userID, errMsg, constants.WSEventStoryError)
		}

		if genResult.Error != "" {
			log.Warn("GenerationResult for moderation indicates an error", zap.String("gen_error", genResult.Error))
			userID, _ := uuid.Parse(notification.UserID)
			errDetails := fmt.Sprintf("moderation result error: %s", genResult.Error)
			return p.errorHandler.HandleStoryError(ctx, p.db, publishedStoryID, userID, errDetails, constants.WSEventStoryError)
		}

		// Парсим результат модерации
		moderationOutcome, err := decodeStrictJSON[moderationResultPayload](genResult.GeneratedText)
		if err != nil {
			log.Error("Failed to unmarshal moderation result JSON", zap.Error(err), zap.String("json_text", genResult.GeneratedText))
			userID, _ := uuid.Parse(notification.UserID)
			errMsg := fmt.Sprintf("failed to parse moderation result: %v", err)
			return p.errorHandler.HandleStoryError(ctx, p.db, publishedStoryID, userID, errMsg, constants.WSEventStoryError)
		}

		// Проверяем результат модерации
		if !moderationOutcome.IsAppropriate {
			log.Warn("Content moderation failed - content deemed inappropriate",
				zap.String("reason", moderationOutcome.Reason))
			userID, _ := uuid.Parse(notification.UserID)
			errMsg := fmt.Sprintf("content moderation failed: %s", moderationOutcome.Reason)
			return p.errorHandler.HandleStoryError(ctx, p.db, publishedStoryID, userID, errMsg, constants.WSEventStoryError)
		}

		log.Info("Content moderation passed, proceeding to protagonist goal generation")

		// Используем атомарную операцию для обновления состояния
		return p.txHelper.WithTransaction(ctx, func(ctx context.Context, tx interfaces.DBTX) error {
			// Атомарно обновляем шаг и статус
			expectedStep := sharedModels.StepModeration
			nextStep := sharedModels.StepProtagonistGoal
			nextStatus := sharedModels.StatusProtagonistGoalPending

			updatedStory, err := p.stepManager.AtomicUpdateStepAndStatus(
				ctx, tx, publishedStoryID,
				&expectedStep, // ожидаемый текущий шаг
				&nextStep,     // новый шаг
				nextStatus,    // новый статус
			)
			if err != nil {
				return fmt.Errorf("failed to update story step and status: %w", err)
			}

			// Отправляем задачу генерации цели протагониста
			return p.dispatchProtagonistGoalTask(ctx, updatedStory)
		})
	}

	log.Warn("Received moderation notification with unexpected status",
		zap.String("status", string(notification.Status)))
	return nil
}

// dispatchProtagonistGoalTask отправляет задачу генерации цели протагониста
func (p *NotificationProcessor) dispatchProtagonistGoalTask(ctx context.Context, story *sharedModels.PublishedStory) error {
	if story.Config == nil {
		return fmt.Errorf("cannot dispatch protagonist goal task: config is nil")
	}

	config, err := decodeStrictJSON[sharedModels.Config](string(story.Config))
	if err != nil {
		return fmt.Errorf("failed to decode config for protagonist goal task: %w", err)
	}

	// Используем существующий форматтер
	userInput := utils.FormatConfigForGoalPrompt(config, story.IsAdultContent)
	if userInput == "" {
		return fmt.Errorf("generated empty user input for protagonist goal task")
	}

	taskID := uuid.New().String()
	payload := sharedMessaging.GenerationTaskPayload{
		TaskID:           taskID,
		UserID:           story.UserID.String(),
		PromptType:       sharedModels.PromptTypeProtagonistGoal,
		UserInput:        userInput,
		PublishedStoryID: story.ID.String(),
		Language:         story.Language,
	}

	if err := p.publishTask(payload); err != nil {
		return fmt.Errorf("failed to publish protagonist goal task: %w", err)
	}

	p.logger.Info("Protagonist goal task dispatched successfully",
		zap.String("story_id", story.ID.String()),
		zap.String("task_id", taskID))

	return nil
}
