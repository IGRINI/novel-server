package service

import (
	"context"
	"errors"
	"fmt"
	"novel-server/shared/database"
	sharedMessaging "novel-server/shared/messaging"
	"novel-server/shared/models"
	"novel-server/shared/utils"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// buildRetryGenerationForGameStatePayload содержит бизнес-логику подготовки таска для RetryGenerationForGameState
func (s *gameLoopServiceImpl) buildRetryGenerationForGameStatePayload(
	ctx context.Context,
	tx pgx.Tx,
	userID, storyID, gameStateID uuid.UUID,
) (*sharedMessaging.GenerationTaskPayload, error) {
	log := s.logger.With(
		zap.String("gameStateID", gameStateID.String()),
		zap.String("publishedStoryID", storyID.String()),
		zap.Stringer("userID", userID),
	)
	log.Info("RetryGenerationForGameState called")

	gameStateRepo := database.NewPgPlayerGameStateRepository(tx, s.logger)
	publishedRepo := database.NewPgPublishedStoryRepository(tx, s.logger)

	gs, err := gameStateRepo.GetByID(ctx, tx, gameStateID)
	if wrapErr := WrapRepoError(s.logger, err, "PlayerGameState"); wrapErr != nil {
		if errors.Is(wrapErr, models.ErrNotFound) {
			return nil, models.ErrPlayerGameStateNotFound
		}
		return nil, wrapErr
	}
	if gs.PlayerID != userID {
		return nil, models.ErrForbidden
	}
	if gs.PlayerStatus != models.PlayerStatusError && gs.PlayerStatus != models.PlayerStatusGeneratingScene {
		return nil, models.ErrCannotRetry
	}

	story, err := publishedRepo.GetByID(ctx, tx, gs.PublishedStoryID)
	if wrapErr := WrapRepoError(s.logger, err, "PublishedStory"); wrapErr != nil {
		if errors.Is(wrapErr, models.ErrNotFound) {
			return nil, models.ErrStoryNotFound
		}
		return nil, wrapErr
	}

	// Retry setup if missing
	if story.Setup == nil || string(story.Setup) == "null" {
		if story.Config == nil {
			// пометить gs как ошибку
			gs.PlayerStatus = models.PlayerStatusError
			msg := "Cannot retry: Story Config is missing"
			gs.ErrorDetails = &msg
			gameStateRepo.Save(ctx, tx, gs)
			return nil, models.ErrInternalServer
		}
		// Обновить флаги статуса для setup retry
		if story.Status == models.StatusError {
			publishedRepo.UpdateStatusFlagsAndDetails(ctx, tx, story.ID, models.StatusSetupPending, false, false, 0, 0, 0, nil, nil)
		}
		// Форматируем input
		var cfg models.Config
		_ = DecodeStrictJSON(story.Config, &cfg)
		input := utils.FormatConfigToString(cfg, story.IsAdultContent)
		payload := sharedMessaging.GenerationTaskPayload{
			TaskID:           uuid.New().String(),
			UserID:           story.UserID.String(),
			PromptType:       models.PromptTypeStorySetup,
			UserInput:        input,
			PublishedStoryID: story.ID.String(),
			Language:         story.Language,
		}
		return &payload, nil
	}

	// Retry scene/game over
	if gs.PlayerProgressID == uuid.Nil {
		gs.PlayerStatus = models.PlayerStatusError
		msg := "Cannot retry: Inconsistent state (missing PlayerProgressID)"
		gs.ErrorDetails = &msg
		gameStateRepo.Save(ctx, tx, gs)
		return nil, models.ErrInternalServer
	}
	progressRepo := database.NewPgPlayerProgressRepository(tx, s.logger)
	prog, err := progressRepo.GetByID(ctx, tx, gs.PlayerProgressID)
	if wrapErr := WrapRepoError(s.logger, err, "PlayerProgress"); wrapErr != nil {
		return nil, wrapErr
	}
	// Определяем тип задачи: сцена или game over
	promptType := models.PromptTypeStoryContinuation
	if !gs.CurrentSceneID.Valid {
		log.Info("Identified as Game Over retry.")
		promptType = models.PromptTypeNovelGameOverCreator
	} else {
		log.Info("Identified as Scene retry.")
	}
	gs.PlayerStatus = models.PlayerStatusGeneratingScene
	gs.ErrorDetails = nil
	gs.LastActivityAt = time.Now().UTC()
	gameStateRepo.Save(ctx, tx, gs)
	// Создаём payload
	genPayload, err := createGenerationPayload(
		userID,
		story,
		prog,
		gs,
		[]models.UserChoiceInfo{},
		prog.CurrentStateHash,
		story.Language,
		promptType,
	)
	if err != nil {
		log.Error("Failed to create generation payload for scene/gameover retry", zap.Error(err))
		msg := fmt.Sprintf("Failed to create generation payload: %v", err)
		gs.PlayerStatus = models.PlayerStatusError
		gs.ErrorDetails = &msg
		gameStateRepo.Save(ctx, tx, gs)
		return nil, models.ErrInternalServer
	}
	genPayload.GameStateID = gameStateID.String()
	return &genPayload, nil
}

// RetryGenerationForGameState handles retrying generation for a specific game state.
func (s *gameLoopServiceImpl) RetryGenerationForGameState(
	ctx context.Context,
	userID, storyID, gameStateID uuid.UUID,
) error {
	return s.doRetryWithTx(
		ctx,
		userID,
		storyID,
		func(ctx context.Context, tx pgx.Tx) (*sharedMessaging.GenerationTaskPayload, error) {
			return s.buildRetryGenerationForGameStatePayload(ctx, tx, userID, storyID, gameStateID)
		},
		func(ctx context.Context, tx pgx.Tx, payload *sharedMessaging.GenerationTaskPayload) error {
			if payload != nil {
				return s.publisher.PublishGenerationTask(ctx, *payload)
			}
			return nil
		},
	)
}

// RetryInitialGeneration handles retrying generation for a published story based on its InternalGenerationStep.
func (s *gameLoopServiceImpl) RetryInitialGeneration(ctx context.Context, userID, storyID uuid.UUID) (err error) {
	log := s.logger.With(zap.String("storyID", storyID.String()), zap.Stringer("userID", userID))
	log.Info("RetryInitialGeneration called")

	err = WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		story, err := s.publishedRepo.GetByID(ctx, tx, storyID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				log.Warn("Story not found")
				return models.ErrStoryNotFound
			}
			log.Error("Failed to get story by ID", zap.Error(err))
			return fmt.Errorf("failed to get story by ID: %w", err)
		}

		if story.UserID != userID {
			log.Warn("User ID mismatch", zap.Stringer("storyUserID", story.UserID))
			return models.ErrForbidden
		}

		if story.InternalGenerationStep == nil {
			log.Warn("InternalGenerationStep is nil, cannot retry")
			return fmt.Errorf("cannot retry story %s: InternalGenerationStep is nil", storyID)
		}

		if len(story.Config) == 0 {
			log.Error("Config JSON is missing for the story")
			return fmt.Errorf("story %s has missing config JSON", storyID)
		}
		var storyConfig models.Config
		if err := DecodeStrictJSON(story.Config, &storyConfig); err != nil {
			log.Error("Failed to unmarshal story config JSON", zap.Error(err))
			return fmt.Errorf("failed to unmarshal config for story %s: %w", storyID, err)
		}

		var setupMap map[string]interface{}
		if story.Setup != nil {
			if err := DecodeStrictJSON(story.Setup, &setupMap); err != nil {
				log.Error("Failed to unmarshal story setup JSON", zap.Error(err))
				return fmt.Errorf("failed to unmarshal setup for story %s: %w", storyID, err)
			}
		}

		stepPtr := story.InternalGenerationStep
		if stepPtr == nil { // Should be caught by earlier check, but for safety
			log.Error("InternalGenerationStep became nil unexpectedly after initial check")
			return fmt.Errorf("internal generation step is unexpectedly nil for story %s", storyID)
		}
		step := *stepPtr
		log.Info("Starting retry from step", zap.String("step", string(step)))

		var initialSceneContent *models.InitialSceneContent
		if step == models.StepCardImageGeneration || step == models.StepCharacterImageGeneration || step == models.StepCharacterGeneration || step == models.StepInitialSceneJSON {
			sceneRepo := database.NewPgStorySceneRepository(tx, s.logger) // Use s.logger for repo
			initialScene, errScene := sceneRepo.FindByStoryAndHash(ctx, tx, storyID, models.InitialStateHash)
			if errScene != nil {
				if errors.Is(errScene, pgx.ErrNoRows) || errors.Is(errScene, models.ErrNotFound) {
					log.Error("Initial scene not found for retry step", zap.String("step", string(step)))
					return fmt.Errorf("initial scene not found for story %s retry", storyID)
				}
				log.Error("Failed to get initial scene for retry", zap.Error(errScene))
				return fmt.Errorf("failed to get initial scene for story %s retry: %w", storyID, errScene)
			}
			if initialScene.Content != nil {
				tempContent := models.InitialSceneContent{}
				if errUnmarshal := DecodeStrictJSON(initialScene.Content, &tempContent); errUnmarshal != nil {
					log.Error("Failed to unmarshal initial scene content for retry", zap.Error(errUnmarshal))
					return fmt.Errorf("failed to unmarshal initial scene content for story %s retry: %w", storyID, errUnmarshal)
				}
				initialSceneContent = &tempContent
			} else {
				log.Error("Initial scene content is nil for retry step", zap.String("step", string(step)))
				return fmt.Errorf("initial scene content is nil for story %s retry", storyID)
			}
		}

		var deserializedSetup models.NovelSetupContent
		if step == models.StepInitialSceneJSON {
			if err := DecodeStrictJSON(story.Setup, &deserializedSetup); err != nil {
				log.Error("Failed to decode Setup into NovelSetupContent for JSON task retry", zap.Error(err))
				return fmt.Errorf("failed to decode setup into struct for json task retry (story %s): %w", storyID, err)
			}
		}

		var payloadToSend interface{}
		var directPublishError error
		var promptTypeForLog models.PromptType

		switch step {
		case models.StepModeration:
			payloadToSend, err = s.prepareModerationRetryPayload(log, story, storyConfig, userID)
			promptTypeForLog = models.PromptTypeContentModeration
		case models.StepProtagonistGoal:
			payloadToSend, err = s.prepareProtagonistGoalRetryPayload(log, story, storyConfig, userID)
			promptTypeForLog = models.PromptTypeProtagonistGoal
		case models.StepScenePlanner:
			payloadToSend, err = s.prepareScenePlannerRetryPayload(log, story, storyConfig, setupMap, userID)
			promptTypeForLog = models.PromptTypeScenePlanner
		case models.StepCharacterGeneration:
			if initialSceneContent == nil && (step == models.StepCharacterGeneration) { // Ensure it's loaded if needed by this step specifically
				log.Error("Initial scene content is unexpectedly nil for CharacterGeneration step")
				return fmt.Errorf("initial scene content nil for CharacterGeneration retry for story %s", storyID)
			}
			payloadToSend, err = s.prepareCharacterGenerationRetryPayload(log, story, storyConfig, setupMap, initialSceneContent, userID)
			promptTypeForLog = models.PromptTypeCharacterGeneration
		case models.StepSetupGeneration:
			payloadToSend, err = s.prepareSetupGenerationRetryPayload(log, story, storyConfig, setupMap, userID)
			promptTypeForLog = models.PromptTypeStorySetup
		case models.StepInitialSceneJSON:
			if initialSceneContent == nil || initialSceneContent.SceneFocus == "" {
				log.Error("Initial scene content or its SceneFocus is missing for JSON generation retry", zap.String("storyID", story.ID.String()))
				err = fmt.Errorf("SceneFocus missing in InitialSceneContent for story %s retry", story.ID.String())
			} else {
				payloadToSend, err = s.prepareInitialSceneJSONRetryPayload(log, story, storyConfig, setupMap, deserializedSetup, initialSceneContent, userID)
			}
			promptTypeForLog = models.PromptTypeJsonGeneration
		case models.StepCoverImageGeneration:
			payloadToSend, err = s.prepareCoverImageRetryPayload(log, story, setupMap, userID)
			promptTypeForLog = models.PromptTypeStoryPreviewImage
		case models.StepCardImageGeneration:
			if initialSceneContent == nil {
				log.Error("Initial scene content is unexpectedly nil for CardImageGeneration step")
				return fmt.Errorf("initial scene content nil for CardImageGeneration retry for story %s", storyID)
			}
			directPublishError = s.publishRetryCardImageTasks(ctx, log, story, initialSceneContent, userID)
			err = directPublishError // Assign to err to handle it below
		case models.StepCharacterImageGeneration:
			if initialSceneContent == nil {
				log.Error("Initial scene content is unexpectedly nil for CharacterImageGeneration step")
				return fmt.Errorf("initial scene content nil for CharacterImageGeneration retry for story %s", storyID)
			}
			directPublishError = s.publishRetryCharacterImageTasks(ctx, log, story, initialSceneContent)
			err = directPublishError // Assign to err to handle it below
		default:
			log.Error("Unsupported generation step for retry", zap.String("step", string(step)))
			return fmt.Errorf("unsupported step for initial generation retry: %s", string(step))
		}

		if err != nil {
			// Error already logged by helper or a check before switch
			return err // Return the error from helper or pre-switch check
		}

		if payloadToSend != nil {
			switch p := payloadToSend.(type) {
			case *sharedMessaging.GenerationTaskPayload:
				if pubErr := s.publisher.PublishGenerationTask(ctx, *p); pubErr != nil {
					log.Error("Failed to publish generation task", zap.Error(pubErr), zap.String("promptType", string(p.PromptType)))
					return fmt.Errorf("failed to publish generation task (%s): %w", string(p.PromptType), pubErr)
				}
				log.Info("Successfully published generation task for retry", zap.String("promptType", string(p.PromptType)))
			case *sharedMessaging.CharacterImageTaskPayload:
				if pubErr := s.imagePublisher.PublishCharacterImageTask(ctx, *p); pubErr != nil {
					log.Error("Failed to publish cover/character image task during retry", zap.Error(pubErr))
					return fmt.Errorf("failed to publish cover/character image task: %w", pubErr)
				}
				log.Info("Successfully published cover/character image task for retry", zap.String("taskType", string(promptTypeForLog)))
			default:
				log.Error("Unknown payload type prepared for retry", zap.Any("payload", payloadToSend))
				return fmt.Errorf("internal error: unknown payload type for retry")
			}
		}

		if story.Status == models.StatusError {
			updateErr := s.publishedRepo.UpdateStatusAndError(ctx, tx, storyID, models.StatusGenerating, nil)
			if updateErr != nil {
				log.Error("Failed to update story status to Generating", zap.Error(updateErr))
				return fmt.Errorf("failed to update story status to Generating: %w", updateErr)
			}
			log.Info("Updated story status from Error to Generating")
		}

		log.Info("RetryInitialGeneration completed successfully for step", zap.String("step", string(step)))
		return nil
	})
	return err
}

func (s *gameLoopServiceImpl) prepareModerationRetryPayload(log *zap.Logger, story *models.PublishedStory, storyConfig models.Config, userID uuid.UUID) (*sharedMessaging.GenerationTaskPayload, error) {
	userInput, err := utils.FormatInputForModeration(storyConfig)
	if err != nil {
		log.Error("Failed to format input for Moderation retry", zap.Error(err))
		return nil, err
	}
	payload := &sharedMessaging.GenerationTaskPayload{
		TaskID:           uuid.New().String(),
		UserID:           userID.String(),
		PublishedStoryID: story.ID.String(),
		PromptType:       models.PromptTypeContentModeration,
		UserInput:        userInput,
		Language:         story.Language,
	}
	log.Info("Prepared payload for Moderation retry")
	return payload, nil
}

func (s *gameLoopServiceImpl) prepareProtagonistGoalRetryPayload(log *zap.Logger, story *models.PublishedStory, storyConfig models.Config, userID uuid.UUID) (*sharedMessaging.GenerationTaskPayload, error) {
	userInput := utils.FormatConfigForGoalPrompt(storyConfig, story.IsAdultContent)
	payload := &sharedMessaging.GenerationTaskPayload{
		TaskID:           uuid.New().String(),
		UserID:           userID.String(),
		PublishedStoryID: story.ID.String(),
		PromptType:       models.PromptTypeProtagonistGoal,
		UserInput:        userInput,
		Language:         story.Language,
	}
	log.Info("Prepared payload for ProtagonistGoal retry")
	return payload, nil
}

func (s *gameLoopServiceImpl) prepareScenePlannerRetryPayload(log *zap.Logger, story *models.PublishedStory, storyConfig models.Config, setupMap map[string]interface{}, userID uuid.UUID) (*sharedMessaging.GenerationTaskPayload, error) {
	goal, ok := setupMap["protagonist_goal"].(string)
	if !ok || goal == "" {
		log.Error("Protagonist goal missing or invalid in setupMap for scene planner retry")
		return nil, fmt.Errorf("protagonist_goal missing/invalid in setup for story %s", story.ID.String())
	}
	userInput, err := utils.FormatConfigAndGoalForScenePlanner(storyConfig, goal, story.IsAdultContent)
	if err != nil {
		log.Error("Failed to format input for scene planner", zap.Error(err))
		return nil, err
	}
	payload := &sharedMessaging.GenerationTaskPayload{
		TaskID:           uuid.New().String(),
		UserID:           userID.String(),
		PublishedStoryID: story.ID.String(),
		PromptType:       models.PromptTypeScenePlanner,
		UserInput:        userInput,
		Language:         story.Language,
	}
	log.Info("Prepared payload for ScenePlanner retry")
	return payload, nil
}

func (s *gameLoopServiceImpl) prepareCharacterGenerationRetryPayload(log *zap.Logger, story *models.PublishedStory, storyConfig models.Config, setupMap map[string]interface{}, initialSceneContent *models.InitialSceneContent, userID uuid.UUID) (*sharedMessaging.GenerationTaskPayload, error) {
	if initialSceneContent == nil {
		log.Error("InitialSceneContent is nil for CharacterGeneration retry")
		return nil, fmt.Errorf("initialSceneContent is nil for CharacterGeneration retry, story %s", story.ID.String())
	}

	charSetupMap := make(map[string]interface{})
	if goal, ok := setupMap["protagonist_goal"]; ok {
		charSetupMap["protagonist_goal"] = goal
	}
	var suggestions []interface{}
	for _, charDef := range initialSceneContent.Characters {
		m := map[string]interface{}{"role": charDef.Name, "reason": charDef.Description}
		suggestions = append(suggestions, m)
	}
	charSetupMap["characters_to_generate_list"] = suggestions

	userInput, err := utils.FormatInputForCharacterGen(storyConfig, charSetupMap, story.IsAdultContent)
	if err != nil {
		log.Error("Failed to format input for CharacterGen retry", zap.Error(err))
		return nil, err
	}
	payload := &sharedMessaging.GenerationTaskPayload{
		TaskID:           uuid.New().String(),
		UserID:           userID.String(),
		PublishedStoryID: story.ID.String(),
		PromptType:       models.PromptTypeCharacterGeneration,
		UserInput:        userInput,
		Language:         story.Language,
	}
	log.Info("Prepared payload for CharacterGeneration retry")
	return payload, nil
}

func (s *gameLoopServiceImpl) prepareSetupGenerationRetryPayload(log *zap.Logger, story *models.PublishedStory, storyConfig models.Config, setupMap map[string]interface{}, userID uuid.UUID) (*sharedMessaging.GenerationTaskPayload, error) {
	userInput, err := utils.FormatInputForSetupGen(storyConfig, setupMap, story.IsAdultContent)
	if err != nil {
		log.Error("Failed to format input for setup generation", zap.Error(err))
		return nil, err
	}
	payload := &sharedMessaging.GenerationTaskPayload{
		TaskID:           uuid.New().String(),
		UserID:           userID.String(),
		PublishedStoryID: story.ID.String(),
		PromptType:       models.PromptTypeStorySetup,
		UserInput:        userInput,
		Language:         story.Language,
	}
	log.Info("Prepared payload for SetupGeneration retry")
	return payload, nil
}

func (s *gameLoopServiceImpl) prepareInitialSceneJSONRetryPayload(log *zap.Logger, story *models.PublishedStory, storyConfig models.Config, setupMap map[string]interface{}, deserializedSetup models.NovelSetupContent, initialSceneContent *models.InitialSceneContent, userID uuid.UUID) (*sharedMessaging.GenerationTaskPayload, error) {
	if initialSceneContent == nil || initialSceneContent.SceneFocus == "" {
		log.Error("Initial scene content or its SceneFocus is missing in prepareInitialSceneJSONRetryPayload")
		return nil, fmt.Errorf("SceneFocus missing or initialSceneContent is nil for story %s", story.ID.String())
	}
	initialNarrativeText := initialSceneContent.SceneFocus

	userInput, err := utils.FormatInputForJsonGeneration(storyConfig, deserializedSetup, setupMap, initialNarrativeText)
	if err != nil {
		log.Error("Failed to format input for initial JSON generation", zap.Error(err))
		return nil, err
	}
	payload := &sharedMessaging.GenerationTaskPayload{
		TaskID:           uuid.New().String(),
		UserID:           userID.String(),
		PublishedStoryID: story.ID.String(),
		PromptType:       models.PromptTypeJsonGeneration,
		UserInput:        userInput,
		StateHash:        models.InitialStateHash,
		Language:         story.Language,
	}
	log.Info("Prepared payload for InitialJsonGeneration retry")
	return payload, nil
}

func (s *gameLoopServiceImpl) prepareCoverImageRetryPayload(log *zap.Logger, story *models.PublishedStory, setupMap map[string]interface{}, userID uuid.UUID) (*sharedMessaging.CharacterImageTaskPayload, error) {
	basePrompt, okPrompt := setupMap["story_preview_image_prompt"].(string)
	if !okPrompt || basePrompt == "" {
		log.Error("story_preview_image_prompt missing or empty in setupMap for CoverImage retry")
		return nil, errors.New("cannot retry cover image generation: story_preview_image_prompt missing")
	}
	userInput, err := utils.FormatInputForCoverImage(basePrompt, "", "")
	if err != nil {
		log.Error("Failed to format input for CoverImage retry", zap.Error(err))
		return nil, err
	}
	payload := &sharedMessaging.CharacterImageTaskPayload{
		TaskID:           uuid.New().String(),
		UserID:           userID.String(),
		PublishedStoryID: story.ID,
		Prompt:           userInput,
		ImageReference:   fmt.Sprintf("history_preview_%s", story.ID.String()),
		CharacterID:      uuid.Nil,
		CharacterName:    "Story Cover",
		Ratio:            "3:2",
	}
	log.Info("Prepared payload for CoverImage retry")
	return payload, nil
}

func (s *gameLoopServiceImpl) publishRetryCardImageTasks(ctx context.Context, log *zap.Logger, story *models.PublishedStory, initialSceneContent *models.InitialSceneContent, userID uuid.UUID) error {
	log.Warn("Retrying CardImages step - this will republish tasks for ALL initial card images")
	if initialSceneContent == nil || len(initialSceneContent.Cards) == 0 {
		log.Warn("No initial cards found in InitialSceneContent, cannot retry card images")
		return nil // Nothing to retry
	}

	var publishErrors []error
	for i, card := range initialSceneContent.Cards {
		if card.Pr == "" || card.Ir == "" {
			log.Warn("Skipping card image generation due to missing prompt/ref", zap.Int("cardIndex", i), zap.String("cardName", card.Title))
			continue
		}
		cardPayload := sharedMessaging.CharacterImageTaskPayload{
			UserID:           userID.String(),
			PublishedStoryID: story.ID,
			TaskID:           uuid.New().String(),
			Prompt:           card.Pr,
			CharacterName:    card.Title,
			ImageReference:   fmt.Sprintf("card_%s", card.Ir),
			CharacterID:      uuid.Nil,
			Ratio:            "2:3",
		}
		if errPub := s.imagePublisher.PublishCharacterImageTask(ctx, cardPayload); errPub != nil {
			log.Error("Failed to publish card image task during retry", zap.Error(errPub), zap.Int("cardIndex", i), zap.String("cardName", card.Title))
			publishErrors = append(publishErrors, errPub)
		} else {
			log.Info("Published card image task payload for retry", zap.Int("cardIndex", i), zap.String("cardName", card.Title))
		}
	}
	if len(publishErrors) > 0 {
		// Return the first error, or a combined error
		return fmt.Errorf("encountered %d errors during card image task publishing retry: %w", len(publishErrors), publishErrors[0])
	}
	log.Info("Finished publishing all initial card image tasks for retry")
	return nil
}

func (s *gameLoopServiceImpl) publishRetryCharacterImageTasks(ctx context.Context, log *zap.Logger, story *models.PublishedStory, initialSceneContent *models.InitialSceneContent) error {
	log.Warn("Retrying CharacterImages step - this will republish tasks for ALL generated characters")
	if initialSceneContent == nil || len(initialSceneContent.Characters) == 0 {
		log.Warn("No characters found in InitialSceneContent, cannot retry character images")
		return nil // Nothing to retry
	}

	var publishErrors []error
	for i, char := range initialSceneContent.Characters {
		if char.Prompt == "" || char.ImageRef == "" {
			log.Warn("Skipping character image generation due to missing prompt/ref", zap.Int("charIndex", i), zap.String("charName", char.Name))
			continue
		}
		charPayload := sharedMessaging.CharacterImageTaskPayload{
			TaskID:           uuid.New().String(),
			UserID:           story.UserID.String(),
			PublishedStoryID: story.ID,
			CharacterID:      uuid.Nil, // Assuming CharacterID is not readily available here or needed by publisher as Nil
			CharacterName:    char.Name,
			ImageReference:   fmt.Sprintf("ch_%s", char.ImageRef),
			Prompt:           char.Prompt,
			NegativePrompt:   "", // Add if needed
			Ratio:            "2:3",
		}
		if errPub := s.imagePublisher.PublishCharacterImageTask(ctx, charPayload); errPub != nil {
			log.Error("Failed to publish character image task during retry", zap.Error(errPub), zap.Int("charIndex", i), zap.String("charName", char.Name))
			publishErrors = append(publishErrors, errPub)
		} else {
			log.Info("Published character image task payload for retry", zap.Int("charIndex", i), zap.String("charName", char.Name))
		}
	}
	if len(publishErrors) > 0 {
		return fmt.Errorf("encountered %d errors during character image task publishing retry: %w", len(publishErrors), publishErrors[0])
	}
	log.Info("Finished publishing all character image tasks for retry")
	return nil
}
