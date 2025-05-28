package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"novel-server/shared/constants"
	"novel-server/shared/interfaces"
	sharedMessaging "novel-server/shared/messaging"
	sharedModels "novel-server/shared/models"
	"novel-server/shared/utils"
)

// protagonistGoalResultPayload - ожидаемая структура JSON ответа от AI для цели протагониста.
// Может содержать как саму цель, так и начальные элементы setup.
// Например: {"goal": "найти артефакт", "initial_setup_elements": {"key_item": "старая карта"}}
// ИСПРАВЛЕННАЯ СТРУКТУРА В СООТВЕТСТВИИ С protagonist_goal_prompt.md
type protagonistGoalResultPayload struct {
	Result string `json:"res"` // Промпт возвращает только это поле
}

func (p *NotificationProcessor) handleProtagonistGoalResult(ctx context.Context, notification sharedMessaging.NotificationPayload, publishedStoryID uuid.UUID) error {
	taskID := notification.TaskID
	log := p.logger.With(
		zap.String("task_id", taskID),
		zap.String("published_story_id", publishedStoryID.String()),
		zap.String("prompt_type", string(notification.PromptType)),
	)
	log.Info("Processing protagonist goal result")

	// Проверяем статус истории
	publishedStory, err := p.ensureStoryStatus(ctx, publishedStoryID, sharedModels.StatusProtagonistGoalPending)
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
		log.Info("Protagonist goal task successful, processing results.")

		// Получаем результат генерации
		genResult, genErr := p.genResultRepo.GetByTaskID(ctx, taskID)
		if genErr != nil {
			userID, _ := uuid.Parse(notification.UserID)
			errMsg := fmt.Sprintf("failed to fetch protagonist goal result: %v", genErr)
			return p.errorHandler.HandleStoryError(ctx, p.db, publishedStoryID, userID, errMsg, constants.WSEventStoryError)
		}

		if genResult.Error != "" {
			log.Warn("GenerationResult for protagonist goal indicates an error", zap.String("gen_error", genResult.Error))
			userID, _ := uuid.Parse(notification.UserID)
			errDetails := fmt.Sprintf("protagonist goal generation error: %s", genResult.Error)
			return p.errorHandler.HandleStoryError(ctx, p.db, publishedStoryID, userID, errDetails, constants.WSEventStoryError)
		}

		// Сохраняем результат цели протагониста в конфиге
		protagonistGoal := genResult.GeneratedText
		log.Info("Protagonist goal generated successfully", zap.String("goal_preview", protagonistGoal[:min(100, len(protagonistGoal))]))

		// Используем атомарную операцию для обновления состояния
		return p.txHelper.WithTransaction(ctx, func(ctx context.Context, tx interfaces.DBTX) error {
			// Обновляем конфиг с целью протагониста
			err := p.updateConfigWithProtagonistGoal(ctx, publishedStory, protagonistGoal)
			if err != nil {
				return fmt.Errorf("failed to update config with protagonist goal: %w", err)
			}

			// Атомарно обновляем шаг и статус
			expectedStep := sharedModels.StepProtagonistGoal
			nextStep := sharedModels.StepScenePlanner
			nextStatus := sharedModels.StatusScenePlannerPending

			updatedStory, err := p.stepManager.AtomicUpdateStepAndStatus(
				ctx, tx, publishedStoryID,
				&expectedStep, // ожидаемый текущий шаг
				&nextStep,     // новый шаг
				nextStatus,    // новый статус
			)
			if err != nil {
				return fmt.Errorf("failed to update story step and status: %w", err)
			}

			// Отправляем задачу планирования сцены
			return p.dispatchScenePlannerTask(ctx, updatedStory)
		})
	}

	log.Warn("Received protagonist goal notification with unexpected status",
		zap.String("status", string(notification.Status)))
	return nil
}

// updateConfigWithProtagonistGoal обновляет конфиг истории с целью протагониста
func (p *NotificationProcessor) updateConfigWithProtagonistGoal(ctx context.Context, story *sharedModels.PublishedStory, protagonistGoal string) error {
	if story.Config == nil {
		return fmt.Errorf("cannot update config: config is nil")
	}

	// Декодируем существующий конфиг
	config, err := decodeStrictJSON[sharedModels.Config](string(story.Config))
	if err != nil {
		return fmt.Errorf("failed to decode existing config: %w", err)
	}

	// Обновляем конфиг с целью протагониста
	// Цель протагониста может быть добавлена в PlayerPrefs или как отдельное поле
	// Пока добавим в WorldLore как дополнительную информацию
	if config.PlayerPrefs.WorldLore == "" {
		config.PlayerPrefs.WorldLore = fmt.Sprintf("Protagonist Goal: %s", protagonistGoal)
	} else {
		config.PlayerPrefs.WorldLore = fmt.Sprintf("%s\n\nProtagonist Goal: %s", config.PlayerPrefs.WorldLore, protagonistGoal)
	}

	// Кодируем обновленный конфиг
	updatedConfigBytes, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal updated config: %w", err)
	}

	// Обновляем конфиг в БД (используем существующий метод)
	if err := p.publishedRepo.UpdateConfigAndSetupAndStatus(ctx, p.db, story.ID, json.RawMessage(updatedConfigBytes), story.Setup, story.Status); err != nil {
		return fmt.Errorf("failed to update config in database: %w", err)
	}

	p.logger.Info("Config updated with protagonist goal",
		zap.String("story_id", story.ID.String()),
		zap.String("protagonist_goal", protagonistGoal))

	return nil
}

// dispatchScenePlannerTask отправляет задачу планирования сцены
func (p *NotificationProcessor) dispatchScenePlannerTask(ctx context.Context, story *sharedModels.PublishedStory) error {
	if story.Config == nil {
		return fmt.Errorf("cannot dispatch scene planner task: config is nil")
	}

	config, err := decodeStrictJSON[sharedModels.Config](string(story.Config))
	if err != nil {
		return fmt.Errorf("failed to decode config for scene planner task: %w", err)
	}

	// Извлекаем цель протагониста из WorldLore
	protagonistGoal := extractProtagonistGoalFromConfig(config)

	// Используем существующий форматтер
	userInput, err := utils.FormatConfigAndGoalForScenePlanner(config, protagonistGoal, story.IsAdultContent)
	if err != nil {
		return fmt.Errorf("failed to format config for scene planner: %w", err)
	}

	taskID := uuid.New().String()
	payload := sharedMessaging.GenerationTaskPayload{
		TaskID:           taskID,
		UserID:           story.UserID.String(),
		PromptType:       sharedModels.PromptTypeScenePlanner,
		UserInput:        userInput,
		PublishedStoryID: story.ID.String(),
		Language:         story.Language,
	}

	if err := p.publishTask(payload); err != nil {
		return fmt.Errorf("failed to publish scene planner task: %w", err)
	}

	p.logger.Info("Scene planner task dispatched successfully",
		zap.String("story_id", story.ID.String()),
		zap.String("task_id", taskID))

	return nil
}

// extractProtagonistGoalFromConfig извлекает цель протагониста из конфига
func extractProtagonistGoalFromConfig(config sharedModels.Config) string {
	// Ищем цель протагониста в WorldLore
	if strings.Contains(config.PlayerPrefs.WorldLore, "Protagonist Goal:") {
		parts := strings.Split(config.PlayerPrefs.WorldLore, "Protagonist Goal:")
		if len(parts) > 1 {
			goal := strings.TrimSpace(parts[1])
			// Если есть еще текст после цели, берем только первую строку
			if idx := strings.Index(goal, "\n"); idx != -1 {
				goal = strings.TrimSpace(goal[:idx])
			}
			return goal
		}
	}
	return ""
}

// min возвращает минимальное из двух чисел
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
