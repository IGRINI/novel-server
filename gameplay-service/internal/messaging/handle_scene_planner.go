package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"novel-server/shared/constants"
	sharedMessaging "novel-server/shared/messaging"
	sharedModels "novel-server/shared/models"
	"novel-server/shared/utils"
)

func (p *NotificationProcessor) handleScenePlannerResult(ctx context.Context, notification sharedMessaging.NotificationPayload, publishedStoryID uuid.UUID) error {
	taskID := notification.TaskID
	log := p.logger.With(zap.String("task_id", taskID), zap.String("published_story_id", publishedStoryID.String()), zap.String("prompt_type", string(notification.PromptType)))

	log.Info("Processing scene planner result")

	dbCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	publishedStory, err := p.publishedRepo.GetByID(dbCtx, p.db, publishedStoryID)
	if err != nil {
		log.Error("Failed to get PublishedStory for scene planner update", zap.Error(err))
		return fmt.Errorf("error getting PublishedStory %s: %w", publishedStoryID, err)
	}

	if publishedStory.Status != sharedModels.StatusScenePlannerPending {
		log.Warn("PublishedStory not in ScenePlannerPending status, update skipped.", zap.String("current_status", string(publishedStory.Status)))
		return nil
	}

	var storyCfg sharedModels.Config
	if errUnmarshalCfg := json.Unmarshal(publishedStory.Config, &storyCfg); errUnmarshalCfg != nil {
		log.Error("Failed to unmarshal story config in scene planner result handling", zap.Error(errUnmarshalCfg))
		errMsg := fmt.Sprintf("critical error: failed to unmarshal story config %s: %v", publishedStoryID, errUnmarshalCfg)
		if errUpdate := p.publishedRepo.UpdateStatusAndError(ctx, p.db, publishedStoryID, sharedModels.StatusError, &errMsg); errUpdate != nil {
			log.Error("Failed to update PublishedStory to Error status after config unmarshal failure", zap.Error(errUpdate))
		}
		if uid, errUID := uuid.Parse(publishedStory.UserID.String()); errUID == nil {
			p.publishClientStoryUpdateOnError(publishedStoryID, uid, errMsg)
			p.publishPushOnError(ctx, publishedStoryID, uid, errMsg, constants.PushEventTypeStoryError)
		}
		return fmt.Errorf(errMsg)
	}

	if notification.Status == sharedMessaging.NotificationStatusError {
		log.Warn("Scene planner task failed", zap.String("error_details", notification.ErrorDetails))
		if errUpdate := p.publishedRepo.UpdateStatusAndError(ctx, p.db, publishedStoryID, sharedModels.StatusError, &notification.ErrorDetails); errUpdate != nil {
			log.Error("Failed to update PublishedStory to Error status after scene planner failure", zap.Error(errUpdate))
			return fmt.Errorf("failed to update story status to Error: %w", errUpdate)
		}
		if uid, errUID := uuid.Parse(publishedStory.UserID.String()); errUID == nil {
			p.publishClientStoryUpdateOnError(publishedStoryID, uid, notification.ErrorDetails)
			p.publishPushOnError(ctx, publishedStoryID, uid, notification.ErrorDetails, constants.PushEventTypeStoryError)
		}
		log.Info("PublishedStory status updated to Error due to scene planner failure.")
		return nil
	}

	log.Info("Scene planner task successful, processing results.")

	genResult, genErr := p.genResultRepo.GetByTaskID(dbCtx, taskID)
	if genErr != nil {
		log.Error("Failed to get GenerationResult by TaskID for scene planner", zap.Error(genErr))
		errMsg := fmt.Sprintf("failed to fetch scene planner result from gen_results: %v", genErr)
		if errUpdate := p.publishedRepo.UpdateStatusAndError(dbCtx, p.db, publishedStoryID, sharedModels.StatusError, &errMsg); errUpdate != nil {
			log.Error("Failed to update PublishedStory to Error status after failing to fetch gen_result for scene planner", zap.Error(errUpdate))
		}
		if uid, errUID := uuid.Parse(publishedStory.UserID.String()); errUID == nil {
			p.publishClientStoryUpdateOnError(publishedStoryID, uid, errMsg)
			p.publishPushOnError(ctx, publishedStoryID, uid, errMsg, constants.PushEventTypeStoryError)
		}
		return nil
	}

	if genResult.Error != "" {
		log.Warn("GenerationResult for scene planner indicates an error", zap.String("gen_error", genResult.Error))
		if errUpdate := p.publishedRepo.UpdateStatusAndError(ctx, p.db, publishedStoryID, sharedModels.StatusError, &genResult.Error); errUpdate != nil {
			log.Error("Failed to update PublishedStory to Error status due to gen_result error for scene planner", zap.Error(errUpdate))
		}
		if uid, errUID := uuid.Parse(publishedStory.UserID.String()); errUID == nil {
			p.publishClientStoryUpdateOnError(publishedStoryID, uid, genResult.Error)
			p.publishPushOnError(ctx, publishedStoryID, uid, genResult.Error, constants.PushEventTypeStoryError)
		}
		return nil
	}
	genResultText := genResult.GeneratedText

	var plannerOutcome sharedModels.InitialScenePlannerOutcome
	if err := utils.DecodeStrict([]byte(genResultText), &plannerOutcome); err != nil {
		log.Error("Failed to unmarshal scene planner outcome", zap.Error(err))
		errMsg := fmt.Sprintf("failed to parse scene planner result: %v", err)
		if errUpdate := p.publishedRepo.UpdateStatusAndError(ctx, p.db, publishedStoryID, sharedModels.StatusError, &errMsg); errUpdate != nil {
			log.Error("Failed to update PublishedStory to Error status after scene planner JSON parse failure", zap.Error(errUpdate))
		}
		if uid, errUID := uuid.Parse(publishedStory.UserID.String()); errUID == nil {
			p.publishClientStoryUpdateOnError(publishedStoryID, uid, errMsg)
			p.publishPushOnError(ctx, publishedStoryID, uid, errMsg, constants.PushEventTypeStoryError)
		}
		return nil
	}

	var sceneCharacters []sharedModels.CharacterDefinition
	if storyCfg.Chars != nil {
		charactersToRemoveSet := make(map[string]struct{})
		for _, charToRemove := range plannerOutcome.CharactersToRemove {
			charactersToRemoveSet[charToRemove.ID] = struct{}{}
		}

		for _, charDef := range storyCfg.Chars {
			if _, remove := charactersToRemoveSet[charDef.Name]; !remove {
				sceneCharacters = append(sceneCharacters, charDef)
			} else {
				log.Info("Removing character from initial scene based on planner outcome", zap.String("character_name", charDef.Name))
			}
		}
	}

	initialSceneContent := sharedModels.InitialSceneContent{
		SceneFocus: plannerOutcome.SceneFocus,
		Cards:      make([]sharedModels.SceneCard, len(plannerOutcome.NewCardSuggestions)),
		Characters: sceneCharacters,
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
		log.Error("Failed to marshal InitialSceneContent JSON", zap.Error(errMarshalScene))
		errMsg := fmt.Sprintf("failed to marshal initial scene content: %v", errMarshalScene)
		if errUpdate := p.publishedRepo.UpdateStatusAndError(dbCtx, p.db, publishedStoryID, sharedModels.StatusError, &errMsg); errUpdate != nil {
			log.Error("Failed to update story to Error after scene content marshal failure", zap.Error(errUpdate))
		}
		if uid, errUID := uuid.Parse(publishedStory.UserID.String()); errUID == nil {
			p.publishClientStoryUpdateOnError(publishedStoryID, uid, errMsg)
			p.publishPushOnError(ctx, publishedStoryID, uid, errMsg, constants.PushEventTypeStoryError)
		}
		return nil
	}

	charTasksToLaunch := len(plannerOutcome.NewCharacterSuggestions)
	cardImgTasksToLaunch := len(plannerOutcome.NewCardSuggestions)

	publishedStory.PendingCharGenTasks = (charTasksToLaunch > 0)
	publishedStory.PendingCardImgTasks = cardImgTasksToLaunch

	if publishedStory.PendingCharGenTasks || publishedStory.PendingCardImgTasks > 0 {
		publishedStory.Status = sharedModels.StatusSubTasksPending
	} else {
		publishedStory.Status = sharedModels.StatusSetupPending
	}
	publishedStory.ErrorDetails = nil
	publishedStory.UpdatedAt = time.Now().UTC()

	tx, errTx := p.db.Begin(dbCtx)
	if errTx != nil {
		log.Error("Failed to begin transaction for story update after scene planner", zap.Error(errTx))
		return fmt.Errorf("failed to begin transaction: %w", errTx)
	}
	defer func() { _ = tx.Rollback(dbCtx) }()

	if _, errLock := tx.Exec(dbCtx, "SELECT 1 FROM published_stories WHERE id=$1 FOR UPDATE", publishedStoryID); errLock != nil {
		log.Error("Failed to lock story row for scene planner update", zap.Error(errLock))
		return fmt.Errorf("failed to lock story row: %w", errLock)
	}

	initialScene := sharedModels.StoryScene{
		ID:               uuid.New(),
		PublishedStoryID: publishedStoryID,
		StateHash:        sharedModels.InitialStateHash,
		Content:          initialSceneContentBytes,
	}
	if errCreateScene := p.sceneRepo.Create(dbCtx, tx, &initialScene); errCreateScene != nil {
		log.Error("Failed to create initial story scene in transaction", zap.Error(errCreateScene))
		return fmt.Errorf("failed to create initial scene: %w", errCreateScene)
	}
	log.Info("Initial story scene created successfully", zap.Stringer("scene_id", initialScene.ID))

	if errUpdateStory := p.publishedRepo.UpdateStory(dbCtx, tx, publishedStory); errUpdateStory != nil {
		log.Error("Failed to update PublishedStory in transaction after scene planner", zap.Error(errUpdateStory))
		return fmt.Errorf("failed to update story (status/counters) after scene planner: %w", errUpdateStory)
	}
	log.Info("PublishedStory status and counters updated successfully (transaction pending commit)",
		zap.String("new_status", string(publishedStory.Status)),
		zap.Bool("pending_char_gen", publishedStory.PendingCharGenTasks),
		zap.Int("pending_card_img", publishedStory.PendingCardImgTasks),
	)

	if errCommit := tx.Commit(dbCtx); errCommit != nil {
		log.Error("Failed to commit transaction for story update after scene planner", zap.Error(errCommit))
		return fmt.Errorf("failed to commit transaction: %w", errCommit)
	}
	log.Info("Transaction committed: PublishedStory updated and initial scene created.", zap.Stringer("initial_scene_id", initialScene.ID))

	var protagonistGoalMap map[string]interface{}
	if len(publishedStory.Setup) > 0 && string(publishedStory.Setup) != "null" && string(publishedStory.Setup) != "{}" {
		if errUnmarshalSetup := json.Unmarshal(publishedStory.Setup, &protagonistGoalMap); errUnmarshalSetup != nil {
			log.Warn("Failed to unmarshal PublishedStory.Setup to get protagonist_goal, char gen might be impaired.",
				zap.Error(errUnmarshalSetup), zap.String("setup_content", string(publishedStory.Setup)))
			protagonistGoalMap = make(map[string]interface{})
		}
	} else {
		log.Warn("PublishedStory.Setup is empty or null, expected protagonist_goal for char gen.",
			zap.String("setup_content", string(publishedStory.Setup)))
		protagonistGoalMap = make(map[string]interface{})
	}

	tempSetupForCharGen := make(map[string]interface{})
	if goal, ok := protagonistGoalMap["protagonist_goal"]; ok {
		tempSetupForCharGen["protagonist_goal"] = goal
	} else {
		log.Warn("protagonist_goal not found in PublishedStory.Setup for char gen input.")
	}
	tempSetupForCharGen["chars"] = sceneCharacters
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
				ImageType:        constants.ImageTypeCard,
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
