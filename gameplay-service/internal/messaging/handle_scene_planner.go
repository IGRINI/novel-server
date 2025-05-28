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
)

// scenePlannerResultPayload представляет результат планирования сцены
type scenePlannerResultPayload struct {
	Characters []characterPlan `json:"characters"`
	Cards      []cardPlan      `json:"cards"`
}

type characterPlan struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type cardPlan struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (p *NotificationProcessor) handleScenePlannerResult(ctx context.Context, notification sharedMessaging.NotificationPayload, publishedStoryID uuid.UUID) error {
	taskID := notification.TaskID
	log := p.logger.With(
		zap.String("task_id", taskID),
		zap.String("published_story_id", publishedStoryID.String()),
		zap.String("prompt_type", string(notification.PromptType)),
	)
	log.Info("Processing scene planner result")

	// Проверяем статус истории
	publishedStory, err := p.ensureStoryStatus(ctx, publishedStoryID, sharedModels.StatusScenePlannerPending)
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
		log.Info("Scene planner task successful, processing results.")

		// Получаем результат генерации
		genResult, genErr := p.genResultRepo.GetByTaskID(ctx, taskID)
		if genErr != nil {
			userID, _ := uuid.Parse(notification.UserID)
			errMsg := fmt.Sprintf("failed to fetch scene planner result: %v", genErr)
			return p.errorHandler.HandleStoryError(ctx, p.db, publishedStoryID, userID, errMsg, constants.WSEventStoryError)
		}

		if genResult.Error != "" {
			log.Warn("GenerationResult for scene planner indicates an error", zap.String("gen_error", genResult.Error))
			userID, _ := uuid.Parse(notification.UserID)
			errDetails := fmt.Sprintf("scene planner generation error: %s", genResult.Error)
			return p.errorHandler.HandleStoryError(ctx, p.db, publishedStoryID, userID, errDetails, constants.WSEventStoryError)
		}

		// Парсим результат планирования сцены
		scenePlan, err := decodeStrictJSON[scenePlannerResultPayload](genResult.GeneratedText)
		if err != nil {
			log.Error("Failed to unmarshal scene planner result JSON", zap.Error(err), zap.String("json_text", genResult.GeneratedText))
			userID, _ := uuid.Parse(notification.UserID)
			errMsg := fmt.Sprintf("failed to parse scene planner result: %v", err)
			return p.errorHandler.HandleStoryError(ctx, p.db, publishedStoryID, userID, errMsg, constants.WSEventStoryError)
		}

		log.Info("Scene plan generated successfully",
			zap.Int("characters_count", len(scenePlan.Characters)),
			zap.Int("cards_count", len(scenePlan.Cards)))

		// Используем атомарную операцию для обновления состояния
		return p.txHelper.WithTransaction(ctx, func(ctx context.Context, tx interfaces.DBTX) error {
			// Сохраняем план сцены
			err := p.saveScenePlan(ctx, publishedStory, scenePlan)
			if err != nil {
				return fmt.Errorf("failed to save scene plan: %w", err)
			}

			// Обновляем счетчики задач в истории
			publishedStory.PendingCharGenTasks = len(scenePlan.Characters)
			publishedStory.PendingCardImgTasks = len(scenePlan.Cards)

			// Определяем следующий шаг на основе плана
			nextStep := p.stepManager.DetermineNextStep(publishedStory)
			nextStatus := p.stepManager.DetermineStatusFromStep(nextStep)

			// Атомарно обновляем шаг и статус
			expectedStep := sharedModels.StepScenePlanner
			updatedStory, err := p.stepManager.AtomicUpdateStepAndStatus(
				ctx, tx, publishedStoryID,
				&expectedStep, // ожидаемый текущий шаг
				nextStep,      // новый шаг
				nextStatus,    // новый статус
			)
			if err != nil {
				return fmt.Errorf("failed to update story step and status: %w", err)
			}

			// Отправляем соответствующие задачи
			return p.dispatchTasksAfterScenePlanning(ctx, updatedStory)
		})
	}

	log.Warn("Received scene planner notification with unexpected status",
		zap.String("status", string(notification.Status)))
	return nil
}

// saveScenePlan сохраняет план сцены в setup истории
func (p *NotificationProcessor) saveScenePlan(ctx context.Context, story *sharedModels.PublishedStory, scenePlan scenePlannerResultPayload) error {
	// Декодируем существующий setup или создаем новый
	var setupContent sharedModels.NovelSetupContent
	if story.Setup != nil {
		var err error
		setupContent, err = decodeStrictJSON[sharedModels.NovelSetupContent](string(story.Setup))
		if err != nil {
			return fmt.Errorf("failed to decode existing setup: %w", err)
		}
	}

	// Конвертируем characterPlan в GeneratedCharacter
	if len(scenePlan.Characters) > 0 {
		generatedChars := make([]sharedModels.GeneratedCharacter, len(scenePlan.Characters))
		for i, charPlan := range scenePlan.Characters {
			generatedChars[i] = sharedModels.GeneratedCharacter{
				ID:                    uuid.New().String(),
				Name:                  charPlan.Name,
				Role:                  charPlan.Description, // Используем description как role
				Traits:                "",                   // Пока пустое, будет заполнено при генерации
				Relationship:          make(map[string]string),
				ImageReferenceName:    "",
				ImagePromptDescriptor: "",
				Memories:              "",
				PlotHook:              "",
			}
		}
		setupContent.Characters = generatedChars
	}

	// Кодируем обновленный setup
	updatedSetupBytes, err := json.Marshal(setupContent)
	if err != nil {
		return fmt.Errorf("failed to marshal updated setup: %w", err)
	}

	// Обновляем setup в БД
	if err := p.publishedRepo.UpdateConfigAndSetupAndStatus(ctx, p.db, story.ID, story.Config, json.RawMessage(updatedSetupBytes), story.Status); err != nil {
		return fmt.Errorf("failed to update setup in database: %w", err)
	}

	p.logger.Info("Scene plan saved to setup",
		zap.String("story_id", story.ID.String()),
		zap.Int("characters_count", len(scenePlan.Characters)))

	return nil
}

// dispatchTasksAfterScenePlanning отправляет задачи после планирования сцены
func (p *NotificationProcessor) dispatchTasksAfterScenePlanning(ctx context.Context, story *sharedModels.PublishedStory) error {
	// Определяем следующий шаг
	nextStep := p.stepManager.DetermineNextStep(story)

	if nextStep == nil {
		p.logger.Info("No next step after scene planning, story is complete",
			zap.String("story_id", story.ID.String()))
		return nil
	}

	switch *nextStep {
	case sharedModels.StepCharacterGeneration:
		// Отправляем задачу генерации персонажей
		return p.dispatchCharacterGenerationTask(ctx, story)
	case sharedModels.StepSetupGeneration:
		// Отправляем задачу генерации setup
		return p.dispatchSetupTaskFromScenePlanner(ctx, story)
	default:
		p.logger.Warn("Unexpected next step after scene planning",
			zap.String("story_id", story.ID.String()),
			zap.String("next_step", string(*nextStep)))
		return fmt.Errorf("unexpected next step after scene planning: %s", *nextStep)
	}
}

// dispatchCharacterGenerationTask отправляет задачу генерации персонажей
func (p *NotificationProcessor) dispatchCharacterGenerationTask(ctx context.Context, story *sharedModels.PublishedStory) error {
	if story.Config == nil {
		return fmt.Errorf("cannot dispatch character generation task: config is nil")
	}

	config, err := decodeStrictJSON[sharedModels.Config](string(story.Config))
	if err != nil {
		return fmt.Errorf("failed to decode config for character generation task: %w", err)
	}

	// Получаем setup для формирования входных данных
	if story.Setup == nil {
		return fmt.Errorf("cannot dispatch character generation task: setup is nil")
	}

	setupContent, err := decodeStrictJSON[sharedModels.NovelSetupContent](string(story.Setup))
	if err != nil {
		return fmt.Errorf("failed to decode setup for character generation task: %w", err)
	}

	// Создаем setupMap для форматтера
	setupMap := make(map[string]interface{})
	setupMap["characters_to_generate_list"] = convertCharactersToGenerationList(setupContent.Characters)

	// Используем существующий форматтер
	userInput, err := utils.FormatInputForCharacterGen(config, setupMap, story.IsAdultContent)
	if err != nil {
		return fmt.Errorf("failed to format input for character generation: %w", err)
	}

	taskID := uuid.New().String()
	payload := sharedMessaging.GenerationTaskPayload{
		TaskID:           taskID,
		UserID:           story.UserID.String(),
		PromptType:       sharedModels.PromptTypeCharacterGeneration,
		UserInput:        userInput,
		PublishedStoryID: story.ID.String(),
		Language:         story.Language,
	}

	if err := p.publishTask(payload); err != nil {
		return fmt.Errorf("failed to publish character generation task: %w", err)
	}

	p.logger.Info("Character generation task dispatched successfully",
		zap.String("story_id", story.ID.String()),
		zap.String("task_id", taskID))

	return nil
}

// convertCharactersToGenerationList конвертирует персонажей в формат для генерации
func convertCharactersToGenerationList(characters []sharedModels.GeneratedCharacter) []interface{} {
	var result []interface{}
	for _, char := range characters {
		charMap := map[string]interface{}{
			"role":   char.Name,
			"reason": char.Role, // Используем Role как reason
		}
		result = append(result, charMap)
	}
	return result
}

// dispatchSetupTaskFromScenePlanner отправляет задачу генерации setup (переименован чтобы избежать дублирования)
func (p *NotificationProcessor) dispatchSetupTaskFromScenePlanner(ctx context.Context, story *sharedModels.PublishedStory) error {
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
