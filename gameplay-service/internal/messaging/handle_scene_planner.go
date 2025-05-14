package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"novel-server/shared/constants"
	sharedMessaging "novel-server/shared/messaging"
	sharedModels "novel-server/shared/models"
	"novel-server/shared/utils"
)

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (p *NotificationProcessor) handleScenePlannerResult(ctx context.Context, notification sharedMessaging.NotificationPayload, publishedStoryID uuid.UUID) error {
	taskID := notification.TaskID
	log := p.logger.With(zap.String("task_id", taskID), zap.String("published_story_id", publishedStoryID.String()), zap.String("prompt_type", string(notification.PromptType)))

	log.Info("Processing scene planner result")

	publishedStory, err := p.ensureStoryStatus(ctx, publishedStoryID, sharedModels.StatusScenePlannerPending)
	if err != nil {
		return err
	}
	if publishedStory == nil {
		return nil
	}

	var storyCfg sharedModels.Config
	if err := json.Unmarshal(publishedStory.Config, &storyCfg); err != nil {
		errMsg := fmt.Sprintf("critical error: failed to unmarshal story config %s: %v", publishedStoryID, err)
		p.handleStoryError(ctx, publishedStoryID, publishedStory.UserID.String(), errMsg, constants.WSEventStoryError)
		return errors.New(errMsg)
	}

	if notification.Status == sharedMessaging.NotificationStatusError {
		// Ошибка планирования сцены — используем обёртку
		p.handleStoryError(ctx, publishedStoryID, publishedStory.UserID.String(), notification.ErrorDetails, constants.WSEventStoryError)
		return nil
	}

	log.Info("Scene planner task successful, processing results.")

	genResult, genErr := p.genResultRepo.GetByTaskID(ctx, taskID)
	if genErr != nil {
		errMsg := fmt.Sprintf("failed to fetch scene planner result: %v", genErr)
		p.handleStoryError(ctx, publishedStoryID, publishedStory.UserID.String(), errMsg, constants.WSEventStoryError)
		return nil
	}

	if genResult.Error != "" {
		errMsg := fmt.Sprintf("scene planner generation error: %s", genResult.Error)
		p.handleStoryError(ctx, publishedStoryID, publishedStory.UserID.String(), errMsg, constants.WSEventStoryError)
		return nil
	}
	genResultText := genResult.GeneratedText

	plannerOutcome, err := decodeStrict[sharedModels.InitialScenePlannerOutcome](genResultText)
	if err != nil {
		errMsg := fmt.Sprintf("failed to parse scene planner result: %v", err)
		p.handleStoryError(ctx, publishedStoryID, publishedStory.UserID.String(), errMsg, constants.WSEventStoryError)
		return nil
	}

	initialSceneContent := sharedModels.InitialSceneContent{
		SceneFocus: plannerOutcome.SceneFocus,
		Cards:      make([]sharedModels.SceneCard, len(plannerOutcome.NewCardSuggestions)),
		Characters: []sharedModels.CharacterDefinition{},
	}
	for i, sug := range plannerOutcome.NewCardSuggestions {
		initialSceneContent.Cards[i] = sharedModels.SceneCard{
			ImagePromptDescriptor: sug.ImagePromptDescriptor,
			ImageReferenceName:    sug.ImageReferenceName,
			Title:                 sug.Title,
		}
	}

	initialSceneContentBytes, errMarshalScene := json.Marshal(initialSceneContent)
	if errMarshalScene != nil {
		errMsg := fmt.Sprintf("failed to marshal initial scene content: %v", errMarshalScene)
		p.handleStoryError(ctx, publishedStoryID, publishedStory.UserID.String(), errMsg, constants.WSEventStoryError)
		return nil
	}

	charTasksToLaunch := len(plannerOutcome.NewCharacterSuggestions)
	cardImgTasksToLaunch := len(plannerOutcome.NewCardSuggestions)

	newStatus := publishedStory.Status
	pendingCharGenTasksFlag := (charTasksToLaunch > 0)
	pendingCardImgTasksCount := cardImgTasksToLaunch

	if pendingCharGenTasksFlag || pendingCardImgTasksCount > 0 {
		newStatus = sharedModels.StatusSubTasksPending
	} else {
		newStatus = sharedModels.StatusSetupPending
	}

	tx, errTx := p.db.Begin(ctx)
	if errTx != nil {
		log.Error("Failed to begin transaction for story update after scene planner", zap.Error(errTx))
		return fmt.Errorf("failed to begin transaction: %w", errTx)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, errLock := tx.Exec(ctx, "SELECT 1 FROM published_stories WHERE id=$1 FOR UPDATE", publishedStoryID); errLock != nil {
		log.Error("Failed to lock story row for scene planner update", zap.Error(errLock))
		return fmt.Errorf("failed to lock story row: %w", errLock)
	}

	initialScene := sharedModels.StoryScene{
		ID:               uuid.New(),
		PublishedStoryID: publishedStoryID,
		StateHash:        sharedModels.InitialStateHash,
		Content:          initialSceneContentBytes,
	}
	if errCreateScene := p.sceneRepo.Create(ctx, tx, &initialScene); errCreateScene != nil {
		log.Error("Failed to create initial story scene in transaction", zap.Error(errCreateScene))
		return fmt.Errorf("failed to create initial scene: %w", errCreateScene)
	}
	log.Info("Initial story scene created successfully", zap.Stringer("scene_id", initialScene.ID))

	// Определяем следующий шаг и флаг ожидания изображений
	var nextInternalStep sharedModels.InternalGenerationStep
	var areImagesPendingFlag bool

	if pendingCharGenTasksFlag {
		nextInternalStep = sharedModels.StepCharacterGeneration
		// Флаг areImagesPending будет true, если нужны ЕЩЕ и изображения карт
		areImagesPendingFlag = (pendingCardImgTasksCount > 0)
	} else if pendingCardImgTasksCount > 0 {
		nextInternalStep = sharedModels.StepCardImageGeneration
		areImagesPendingFlag = true // Нужны только изображения карт
	} else {
		// Если не нужны ни персонажи, ни изображения карт, переходим к генерации Setup
		// (Хотя статус newStatus должен был стать StatusSetupPending в этом случае)
		nextInternalStep = sharedModels.StepSetupGeneration
		areImagesPendingFlag = false
	}

	// Обновляем статус, флаг ожидания изображений (только карт!) и ШАГ
	if errUpdateStory := p.publishedRepo.UpdateStatusFlagsAndDetails(ctx, tx,
		publishedStoryID,
		newStatus,                          // Рассчитанный статус
		publishedStory.IsFirstScenePending, // Сохраняем существующий флаг
		areImagesPendingFlag,               // True, если запускаем генерацию изображений КАРТ
		nil,                                // Ошибок нет
		&nextInternalStep,                  // Передаем указатель на рассчитанный шаг
	); errUpdateStory != nil {
		log.Error("Failed to update PublishedStory (status/flags/step) in transaction after scene planner", zap.Error(errUpdateStory))
		return fmt.Errorf("failed to update story (status/flags/step) after scene planner: %w", errUpdateStory)
	}
	log.Info("PublishedStory status, flags, and step updated (transaction pending commit)",
		zap.String("new_status", string(newStatus)),
		zap.Bool("pending_char_gen_flag_implied_by_step", pendingCharGenTasksFlag),
		zap.Int("pending_card_img_count", pendingCardImgTasksCount),
		zap.Bool("are_images_pending_flag_set", areImagesPendingFlag), // Отражает только карты, запущенные здесь
		zap.Any("internal_step_set", nextInternalStep),
	)

	if errCommit := tx.Commit(ctx); errCommit != nil {
		log.Error("Failed to commit transaction for story update after scene planner", zap.Error(errCommit))
		return fmt.Errorf("failed to commit transaction: %w", errCommit)
	}
	log.Info("Transaction committed: PublishedStory updated and initial scene created.", zap.Stringer("initial_scene_id", initialScene.ID))

	var protagonistGoalMap map[string]interface{}
	if len(publishedStory.Setup) > 0 && string(publishedStory.Setup) != "null" && string(publishedStory.Setup) != "{}" {
		if errUnmarshalSetup := json.Unmarshal(publishedStory.Setup, &protagonistGoalMap); errUnmarshalSetup != nil {
			log.Warn("Failed to unmarshal PublishedStory.Setup to get protagonist_goal, char gen input might be impaired.",
				zap.Error(errUnmarshalSetup), zap.String("setup_content", string(publishedStory.Setup)))
			protagonistGoalMap = make(map[string]interface{})
		}
	} else {
		log.Warn("PublishedStory.Setup is empty or null, expected protagonist_goal for char gen.", zap.String("setup_content", string(publishedStory.Setup)))
		protagonistGoalMap = make(map[string]interface{})
	}

	tempSetupForCharGen := make(map[string]interface{})
	if goal, ok := protagonistGoalMap["protagonist_goal"]; ok {
		tempSetupForCharGen["protagonist_goal"] = goal
	} else {
		log.Warn("protagonist_goal not found in PublishedStory.Setup for char gen input.")
	}
	tempSetupForCharGen["chars"] = nil
	if len(plannerOutcome.NewCharacterSuggestions) > 0 {
		tempSetupForCharGen["characters_to_generate_list"] = plannerOutcome.NewCharacterSuggestions
	}

	if charTasksToLaunch > 0 {
		log.Info("Dispatching character generation task.", zap.Int("count", charTasksToLaunch))

		charGenInput, formatErr := utils.FormatInputForCharacterGen(
			storyCfg,
			tempSetupForCharGen,
			publishedStory.IsAdultContent,
		)
		if formatErr != nil {
			log.Error("Failed to format input for character generation", zap.Error(formatErr), zap.String("published_story_id", publishedStoryID.String()))
			charGenInput = ""
		}

		charGenTaskID := uuid.New().String()
		charGenPayload := sharedMessaging.GenerationTaskPayload{
			TaskID:           charGenTaskID,
			UserID:           publishedStory.UserID.String(),
			PublishedStoryID: publishedStoryID.String(),
			PromptType:       sharedModels.PromptTypeCharacterGeneration,
			UserInput:        charGenInput,
			Language:         publishedStory.Language,
		}
		if errPub := p.publishTask(charGenPayload); errPub != nil {
			log.Error("Failed to publish character generation task", zap.Error(errPub))
		} else {
			log.Info("Character generation task published successfully", zap.String("task_id", charGenTaskID))
		}
	}

	if cardImgTasksToLaunch > 0 {
		log.Info("Dispatching image generation tasks for cards.", zap.Int("count", cardImgTasksToLaunch))
		for _, cardSuggestion := range plannerOutcome.NewCardSuggestions {
			imgGenTaskID := uuid.New().String()
			cardPayload := sharedMessaging.CharacterImageTaskPayload{
				TaskID:           imgGenTaskID,
				PublishedStoryID: publishedStoryID,
				UserID:           publishedStory.UserID.String(),
				Prompt:           cardSuggestion.ImagePromptDescriptor,
				ImageReference:   cardSuggestion.ImageReferenceName,
				CharacterID:      uuid.Nil,
				CharacterName:    cardSuggestion.Title,
				NegativePrompt:   "",
				Ratio:            "2:3",
			}
			go func(payload sharedMessaging.CharacterImageTaskPayload) {
				taskCtx, cancelTask := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancelTask()
				if errPub := p.characterImageTaskPub.PublishCharacterImageTask(taskCtx, payload); errPub != nil {
					log.Error("Failed to publish card image generation task", zap.Error(errPub), zap.String("task_id", payload.TaskID))
				} else {
					log.Info("Card image task published successfully", zap.String("task_id", payload.TaskID))
				}
			}(cardPayload)
		}
	}

	p.publishClientStoryUpdateOnReady(publishedStory)
	return nil
}
