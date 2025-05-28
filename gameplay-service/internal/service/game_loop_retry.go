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

	// Валидация состояния игры для retry
	if err := s.validateBasicGameStateForRetry(ctx, tx, gs, story, log); err != nil {
		log.Error("Game state validation failed for retry", zap.Error(err))
		return nil, err
	}

	// Восстановление состояния игры для retry
	if err := s.prepareBasicGameStateForRetry(ctx, tx, gs, story, log); err != nil {
		log.Error("Failed to prepare game state for retry", zap.Error(err))
		return nil, err
	}

	// Валидация и восстановление порядка шагов для retry игровых состояний
	if err := s.validateGameStateRetryStep(ctx, tx, gs, story, log); err != nil {
		log.Error("Game state retry step validation failed", zap.Error(err))
		return nil, err
	}

	// Восстановление правильного порядка шагов для retry игровых состояний
	if err := s.prepareGameStateRetrySteps(ctx, tx, gs, story, log); err != nil {
		log.Error("Failed to prepare game state retry steps", zap.Error(err))
		return nil, err
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
func (s *gameLoopServiceImpl) RetryGenerationForGameState(ctx context.Context, userID, storyID, gameStateID uuid.UUID) error {
	return WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		payload, err := s.buildRetryGenerationForGameStatePayload(ctx, tx, userID, storyID, gameStateID)
		if err != nil {
			return err
		}

		// Отправляем WebSocket уведомление о начале retry
		if s.clientPub != nil {
			retryStartNotification := models.ClientStoryUpdate{
				ID:           gameStateID.String(),
				UserID:       userID.String(),
				UpdateType:   models.UpdateTypeGameState,
				Status:       "retry_started",
				ErrorDetails: nil,
			}
			if pubErr := s.clientPub.PublishClientUpdate(ctx, retryStartNotification); pubErr != nil {
				s.logger.Warn("Failed to send retry start WebSocket notification",
					zap.String("gameStateID", gameStateID.String()),
					zap.Error(pubErr))
			}
		}

		if err := s.publisher.PublishGenerationTask(ctx, *payload); err != nil {
			// При ошибке отправки задачи, отправляем WebSocket уведомление об ошибке
			if s.clientPub != nil {
				errorMsg := "Failed to publish retry task"
				errorNotification := models.ClientStoryUpdate{
					ID:           gameStateID.String(),
					UserID:       userID.String(),
					UpdateType:   models.UpdateTypeGameState,
					Status:       "retry_error",
					ErrorDetails: &errorMsg,
				}
				if pubErr := s.clientPub.PublishClientUpdate(ctx, errorNotification); pubErr != nil {
					s.logger.Warn("Failed to send retry error WebSocket notification",
						zap.String("gameStateID", gameStateID.String()),
						zap.Error(pubErr))
				}
			}
			return err
		}

		return nil
	})
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

		// Отправляем WebSocket уведомление о начале retry
		if s.clientPub != nil {
			retryStartNotification := models.ClientStoryUpdate{
				ID:           storyID.String(),
				UserID:       userID.String(),
				UpdateType:   models.UpdateTypeStory,
				Status:       "retry_started",
				ErrorDetails: nil,
			}
			if pubErr := s.clientPub.PublishClientUpdate(ctx, retryStartNotification); pubErr != nil {
				s.logger.Warn("Failed to send retry start WebSocket notification",
					zap.String("storyID", storyID.String()),
					zap.Error(pubErr))
			}
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

		// Проверяем что шаг соответствует текущему состоянию истории
		if err := s.validateRetryStep(ctx, tx, story, step, log); err != nil {
			log.Error("Retry step validation failed", zap.Error(err))
			return err
		}

		// Восстанавливаем состояние для retry если необходимо
		if err := s.prepareStoryStateForRetry(ctx, tx, story, step, log); err != nil {
			log.Error("Failed to prepare story state for retry", zap.Error(err))
			return err
		}

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
			// При ошибке отправляем WebSocket уведомление об ошибке
			if s.clientPub != nil {
				errorMsg := fmt.Sprintf("Failed to prepare retry payload: %v", err)
				errorNotification := models.ClientStoryUpdate{
					ID:           storyID.String(),
					UserID:       userID.String(),
					UpdateType:   models.UpdateTypeStory,
					Status:       "retry_error",
					ErrorDetails: &errorMsg,
				}
				if pubErr := s.clientPub.PublishClientUpdate(ctx, errorNotification); pubErr != nil {
					s.logger.Warn("Failed to send retry error WebSocket notification",
						zap.String("storyID", storyID.String()),
						zap.Error(pubErr))
				}
			}
			// Error already logged by helper or a check before switch
			return err // Return the error from helper or pre-switch check
		}

		if payloadToSend != nil {
			switch p := payloadToSend.(type) {
			case *sharedMessaging.GenerationTaskPayload:
				if pubErr := s.publisher.PublishGenerationTask(ctx, *p); pubErr != nil {
					log.Error("Failed to publish generation task", zap.Error(pubErr), zap.String("promptType", string(p.PromptType)))

					// При ошибке публикации отправляем WebSocket уведомление об ошибке
					if s.clientPub != nil {
						errorMsg := fmt.Sprintf("Failed to publish generation task: %v", pubErr)
						errorNotification := models.ClientStoryUpdate{
							ID:           storyID.String(),
							UserID:       userID.String(),
							UpdateType:   models.UpdateTypeStory,
							Status:       "retry_error",
							ErrorDetails: &errorMsg,
						}
						if pubErr2 := s.clientPub.PublishClientUpdate(ctx, errorNotification); pubErr2 != nil {
							s.logger.Warn("Failed to send retry error WebSocket notification",
								zap.String("storyID", storyID.String()),
								zap.Error(pubErr2))
						}
					}

					return fmt.Errorf("failed to publish generation task (%s): %w", string(p.PromptType), pubErr)
				}
				log.Info("Successfully published generation task for retry", zap.String("promptType", string(p.PromptType)))
			case *sharedMessaging.CharacterImageTaskPayload:
				if pubErr := s.imagePublisher.PublishCharacterImageTask(ctx, *p); pubErr != nil {
					log.Error("Failed to publish cover/character image task during retry", zap.Error(pubErr))

					// При ошибке публикации отправляем WebSocket уведомление об ошибке
					if s.clientPub != nil {
						errorMsg := fmt.Sprintf("Failed to publish image task: %v", pubErr)
						errorNotification := models.ClientStoryUpdate{
							ID:           storyID.String(),
							UserID:       userID.String(),
							UpdateType:   models.UpdateTypeStory,
							Status:       "retry_error",
							ErrorDetails: &errorMsg,
						}
						if pubErr2 := s.clientPub.PublishClientUpdate(ctx, errorNotification); pubErr2 != nil {
							s.logger.Warn("Failed to send retry error WebSocket notification",
								zap.String("storyID", storyID.String()),
								zap.Error(pubErr2))
						}
					}

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

// validateRetryStep проверяет что шаг retry соответствует текущему состоянию истории
func (s *gameLoopServiceImpl) validateRetryStep(ctx context.Context, tx pgx.Tx, story *models.PublishedStory, step models.InternalGenerationStep, log *zap.Logger) error {
	// Проверяем что история находится в подходящем статусе для retry
	if story.Status != models.StatusError && story.Status != models.StatusGenerating {
		log.Warn("Story is not in Error or Generating status, cannot retry", zap.String("status", string(story.Status)))
		return fmt.Errorf("story status %s does not allow retry", string(story.Status))
	}

	// Проверяем зависимости для каждого шага
	switch step {
	case models.StepModeration:
		// Moderation всегда можно повторить если есть Config
		if story.Config == nil || len(story.Config) == 0 {
			return fmt.Errorf("cannot retry moderation: story config is missing")
		}
	case models.StepProtagonistGoal:
		// ProtagonistGoal требует успешной модерации
		if story.Config == nil || len(story.Config) == 0 {
			return fmt.Errorf("cannot retry protagonist goal: story config is missing")
		}
	case models.StepScenePlanner:
		// ScenePlanner требует Setup
		if story.Setup == nil || string(story.Setup) == "null" {
			return fmt.Errorf("cannot retry scene planner: story setup is missing")
		}
	case models.StepCharacterGeneration:
		// CharacterGeneration требует Setup и начальную сцену
		if story.Setup == nil || string(story.Setup) == "null" {
			return fmt.Errorf("cannot retry character generation: story setup is missing")
		}
		sceneRepo := database.NewPgStorySceneRepository(tx, s.logger)
		_, errScene := sceneRepo.FindByStoryAndHash(ctx, tx, story.ID, models.InitialStateHash)
		if errScene != nil {
			return fmt.Errorf("cannot retry character generation: initial scene is missing")
		}
	case models.StepSetupGeneration:
		// SetupGeneration требует Config
		if story.Config == nil || len(story.Config) == 0 {
			return fmt.Errorf("cannot retry setup generation: story config is missing")
		}
	case models.StepInitialSceneJSON:
		// InitialSceneJSON требует Setup и начальную сцену с контентом
		if story.Setup == nil || string(story.Setup) == "null" {
			return fmt.Errorf("cannot retry initial scene JSON: story setup is missing")
		}
		sceneRepo := database.NewPgStorySceneRepository(tx, s.logger)
		initialScene, errScene := sceneRepo.FindByStoryAndHash(ctx, tx, story.ID, models.InitialStateHash)
		if errScene != nil {
			return fmt.Errorf("cannot retry initial scene JSON: initial scene is missing")
		}
		if initialScene.Content == nil {
			return fmt.Errorf("cannot retry initial scene JSON: initial scene content is missing")
		}
	case models.StepCoverImageGeneration:
		// CoverImageGeneration требует Setup
		if story.Setup == nil || string(story.Setup) == "null" {
			return fmt.Errorf("cannot retry cover image generation: story setup is missing")
		}
	case models.StepCardImageGeneration, models.StepCharacterImageGeneration:
		// Image generation требует начальную сцену с контентом
		sceneRepo := database.NewPgStorySceneRepository(tx, s.logger)
		initialScene, errScene := sceneRepo.FindByStoryAndHash(ctx, tx, story.ID, models.InitialStateHash)
		if errScene != nil {
			return fmt.Errorf("cannot retry %s: initial scene is missing", string(step))
		}
		if initialScene.Content == nil {
			return fmt.Errorf("cannot retry %s: initial scene content is missing", string(step))
		}
	}

	log.Info("Retry step validation passed", zap.String("step", string(step)))
	return nil
}

// prepareStoryStateForRetry восстанавливает состояние истории для корректного retry
func (s *gameLoopServiceImpl) prepareStoryStateForRetry(ctx context.Context, tx pgx.Tx, story *models.PublishedStory, step models.InternalGenerationStep, log *zap.Logger) error {
	publishedRepo := database.NewPgPublishedStoryRepository(tx, s.logger)

	// Сбрасываем флаги генерации в зависимости от шага
	var resetFlags bool
	var setupGenerated, scenePlanGenerated, characterGenerated, initialSceneGenerated, coverImageGenerated bool

	switch step {
	case models.StepModeration:
		// При retry модерации сбрасываем все флаги
		resetFlags = true
		setupGenerated = false
		scenePlanGenerated = false
		characterGenerated = false
		initialSceneGenerated = false
		coverImageGenerated = false
	case models.StepProtagonistGoal:
		// При retry protagonist goal сбрасываем флаги после этого шага
		resetFlags = true
		setupGenerated = false
		scenePlanGenerated = false
		characterGenerated = false
		initialSceneGenerated = false
		coverImageGenerated = false
	case models.StepScenePlanner:
		// При retry scene planner сбрасываем флаги после этого шага
		resetFlags = true
		setupGenerated = true
		scenePlanGenerated = false
		characterGenerated = false
		initialSceneGenerated = false
		coverImageGenerated = false
	case models.StepCharacterGeneration:
		// При retry character generation сбрасываем флаги после этого шага
		resetFlags = true
		setupGenerated = true
		scenePlanGenerated = true
		characterGenerated = false
		initialSceneGenerated = false
		coverImageGenerated = false
	case models.StepSetupGeneration:
		// При retry setup generation сбрасываем флаги после этого шага
		resetFlags = true
		setupGenerated = false
		scenePlanGenerated = false
		characterGenerated = false
		initialSceneGenerated = false
		coverImageGenerated = false
	case models.StepInitialSceneJSON:
		// При retry initial scene JSON сбрасываем флаги после этого шага
		resetFlags = true
		setupGenerated = true
		scenePlanGenerated = true
		characterGenerated = true
		initialSceneGenerated = false
		coverImageGenerated = false
	case models.StepCoverImageGeneration:
		// При retry cover image generation сбрасываем только этот флаг
		resetFlags = true
		setupGenerated = true
		scenePlanGenerated = true
		characterGenerated = true
		initialSceneGenerated = true
		coverImageGenerated = false
	case models.StepCardImageGeneration, models.StepCharacterImageGeneration:
		// При retry image generation не сбрасываем основные флаги
		resetFlags = false
	}

	if resetFlags {
		err := publishedRepo.UpdateStatusFlagsAndDetails(
			ctx, tx, story.ID,
			models.StatusGenerating, // Устанавливаем статус Generating
			setupGenerated,
			scenePlanGenerated,
			0,     // pendingCharGenTasks
			0,     // pendingCardImgTasks
			0,     // pendingCharImgTasks
			nil,   // errorDetails
			&step, // currentStep
		)
		if err != nil {
			log.Error("Failed to reset story flags for retry", zap.Error(err))
			return fmt.Errorf("failed to reset story flags for retry: %w", err)
		}
		log.Info("Reset story generation flags for retry",
			zap.String("step", string(step)),
			zap.Bool("setupGenerated", setupGenerated),
			zap.Bool("scenePlanGenerated", scenePlanGenerated),
			zap.Bool("characterGenerated", characterGenerated),
			zap.Bool("initialSceneGenerated", initialSceneGenerated),
			zap.Bool("coverImageGenerated", coverImageGenerated),
		)
	}

	return nil
}

// validateBasicGameStateForRetry проверяет базовые требования для retry игрового состояния
func (s *gameLoopServiceImpl) validateBasicGameStateForRetry(ctx context.Context, tx pgx.Tx, gs *models.PlayerGameState, story *models.PublishedStory, log *zap.Logger) error {
	// Проверяем что история готова для игры
	if story.Status != models.StatusReady && story.Status != models.StatusError {
		log.Warn("Story is not in Ready or Error status, cannot retry game state", zap.String("status", string(story.Status)))
		return fmt.Errorf("story status %s does not allow game state retry", string(story.Status))
	}

	// Проверяем что у истории есть Setup
	if story.Setup == nil || string(story.Setup) == "null" {
		log.Warn("Story setup is missing, cannot retry game state")
		return fmt.Errorf("cannot retry game state: story setup is missing")
	}

	// Проверяем что у игрового состояния есть прогресс
	if gs.PlayerProgressID == uuid.Nil {
		log.Warn("Game state has no progress ID, cannot retry")
		return fmt.Errorf("cannot retry game state: progress ID is missing")
	}

	// Проверяем что прогресс существует
	progressRepo := database.NewPgPlayerProgressRepository(tx, s.logger)
	_, err := progressRepo.GetByID(ctx, tx, gs.PlayerProgressID)
	if err != nil {
		log.Warn("Game state progress not found, cannot retry", zap.Error(err))
		return fmt.Errorf("cannot retry game state: progress not found")
	}

	log.Info("Basic game state validation passed for retry")
	return nil
}

// prepareBasicGameStateForRetry выполняет базовую подготовку игрового состояния для retry
func (s *gameLoopServiceImpl) prepareBasicGameStateForRetry(ctx context.Context, tx pgx.Tx, gs *models.PlayerGameState, story *models.PublishedStory, log *zap.Logger) error {
	// Сбрасываем ошибки в игровом состоянии
	gs.ErrorDetails = nil
	gs.LastActivityAt = time.Now().UTC()

	// Если это retry сцены, проверяем что сцена действительно отсутствует
	if gs.CurrentSceneID.Valid {
		sceneRepo := database.NewPgStorySceneRepository(tx, s.logger)
		progressRepo := database.NewPgPlayerProgressRepository(tx, s.logger)

		// Получаем прогресс для проверки хеша состояния
		progress, err := progressRepo.GetByID(ctx, tx, gs.PlayerProgressID)
		if err != nil {
			log.Error("Failed to get progress for scene validation", zap.Error(err))
			return fmt.Errorf("failed to get progress for retry preparation: %w", err)
		}

		// Проверяем что сцена для текущего хеша состояния действительно отсутствует
		_, sceneErr := sceneRepo.FindByStoryAndHash(ctx, tx, story.ID, progress.CurrentStateHash)
		if sceneErr == nil {
			// Сцена существует, это не должно быть retry
			log.Warn("Scene exists for current state hash, retry not needed")
			gs.PlayerStatus = models.PlayerStatusPlaying
			return nil
		}

		// Сцена отсутствует, сбрасываем CurrentSceneID для корректной генерации
		gs.CurrentSceneID = uuid.NullUUID{Valid: false}
		log.Info("Reset CurrentSceneID for scene retry")
	}

	// Если история была в ошибке, восстанавливаем её статус для retry
	if story.Status == models.StatusError {
		publishedRepo := database.NewPgPublishedStoryRepository(tx, s.logger)
		err := publishedRepo.UpdateStatusFlagsAndDetails(
			ctx, tx, story.ID,
			models.StatusReady, // Восстанавливаем статус Ready
			false, false,       // Сбрасываем флаги ожидания
			0, 0, 0, // Сбрасываем счетчики задач
			nil, // Убираем детали ошибки
			nil, // Не меняем шаг генерации
		)
		if err != nil {
			log.Error("Failed to restore story status for retry", zap.Error(err))
			return fmt.Errorf("failed to restore story status for retry: %w", err)
		}
		log.Info("Restored story status to Ready for game state retry")
	}

	log.Info("Basic game state prepared for retry")
	return nil
}

// validateGameStateRetryStep проверяет что retry игрового состояния соответствует правильному порядку шагов
func (s *gameLoopServiceImpl) validateGameStateRetryStep(ctx context.Context, tx pgx.Tx, gs *models.PlayerGameState, story *models.PublishedStory, log *zap.Logger) error {
	// Проверяем что история готова для игры
	if story.Status != models.StatusReady && story.Status != models.StatusError {
		log.Warn("Story is not in Ready or Error status for game state retry", zap.String("status", string(story.Status)))
		return fmt.Errorf("story status %s does not allow game state retry", string(story.Status))
	}

	// Проверяем что все необходимые компоненты истории готовы
	if story.Setup == nil || string(story.Setup) == "null" {
		log.Warn("Story setup is missing for game state retry")
		return fmt.Errorf("cannot retry game state: story setup is missing")
	}

	// Проверяем что начальная сцена существует
	sceneRepo := database.NewPgStorySceneRepository(tx, s.logger)
	_, errInitialScene := sceneRepo.FindByStoryAndHash(ctx, tx, story.ID, models.InitialStateHash)
	if errInitialScene != nil {
		log.Warn("Initial scene is missing for game state retry", zap.Error(errInitialScene))
		return fmt.Errorf("cannot retry game state: initial scene is missing")
	}

	// Проверяем что прогресс игрока корректен
	if gs.PlayerProgressID == uuid.Nil {
		log.Warn("Game state has no progress ID for retry")
		return fmt.Errorf("cannot retry game state: progress ID is missing")
	}

	progressRepo := database.NewPgPlayerProgressRepository(tx, s.logger)
	progress, err := progressRepo.GetByID(ctx, tx, gs.PlayerProgressID)
	if err != nil {
		log.Warn("Game state progress not found for retry", zap.Error(err))
		return fmt.Errorf("cannot retry game state: progress not found")
	}

	// Проверяем что хеш состояния корректен
	if progress.CurrentStateHash == "" {
		log.Warn("Game state progress has empty state hash for retry")
		return fmt.Errorf("cannot retry game state: progress state hash is empty")
	}

	// Проверяем что индекс сцены корректен
	if progress.SceneIndex < 0 {
		log.Warn("Game state progress has invalid scene index for retry", zap.Int("sceneIndex", progress.SceneIndex))
		return fmt.Errorf("cannot retry game state: progress scene index is invalid")
	}

	log.Info("Game state retry step validation passed")
	return nil
}

// prepareGameStateRetrySteps восстанавливает правильный порядок шагов для retry игровых состояний
func (s *gameLoopServiceImpl) prepareGameStateRetrySteps(ctx context.Context, tx pgx.Tx, gs *models.PlayerGameState, story *models.PublishedStory, log *zap.Logger) error {
	progressRepo := database.NewPgPlayerProgressRepository(tx, s.logger)
	sceneRepo := database.NewPgStorySceneRepository(tx, s.logger)

	// Получаем прогресс для анализа состояния
	progress, err := progressRepo.GetByID(ctx, tx, gs.PlayerProgressID)
	if err != nil {
		log.Error("Failed to get progress for retry steps preparation", zap.Error(err))
		return fmt.Errorf("failed to get progress for retry steps: %w", err)
	}

	// Проверяем что сцена для текущего хеша состояния действительно отсутствует
	_, sceneErr := sceneRepo.FindByStoryAndHash(ctx, tx, story.ID, progress.CurrentStateHash)
	if sceneErr == nil {
		// Сцена существует, проверяем что она корректно связана с игровым состоянием
		log.Info("Scene exists for current state hash, updating game state to Playing")
		gs.PlayerStatus = models.PlayerStatusPlaying
		gs.ErrorDetails = nil
		gs.LastActivityAt = time.Now().UTC()

		// Находим сцену и связываем её с игровым состоянием
		scene, _ := sceneRepo.FindByStoryAndHash(ctx, tx, story.ID, progress.CurrentStateHash)
		if scene != nil {
			gs.CurrentSceneID = uuid.NullUUID{UUID: scene.ID, Valid: true}
		}

		gameStateRepo := database.NewPgPlayerGameStateRepository(tx, s.logger)
		if _, saveErr := gameStateRepo.Save(ctx, tx, gs); saveErr != nil {
			log.Error("Failed to update game state to Playing after finding existing scene", zap.Error(saveErr))
			return fmt.Errorf("failed to update game state: %w", saveErr)
		}

		log.Info("Game state updated to Playing, retry not needed")
		return nil
	}

	// Сцена отсутствует, проверяем что это корректное состояние для генерации
	if progress.CurrentStateHash == models.InitialStateHash {
		// Это начальная сцена, проверяем что история готова
		if story.Status != models.StatusReady {
			log.Warn("Story is not ready for initial scene generation retry", zap.String("status", string(story.Status)))
			return fmt.Errorf("story status %s does not allow initial scene retry", string(story.Status))
		}
	} else {
		// Это продолжение истории, проверяем что предыдущие сцены существуют
		if progress.SceneIndex > 0 {
			// Проверяем что есть предыдущий прогресс
			prevProgress, errPrev := progressRepo.GetByStoryIDAndHash(ctx, tx, story.ID, progress.CurrentStateHash)
			if errPrev != nil && !errors.Is(errPrev, models.ErrNotFound) {
				log.Error("Failed to check previous progress for retry", zap.Error(errPrev))
				return fmt.Errorf("failed to validate progress chain for retry: %w", errPrev)
			}

			if prevProgress == nil {
				log.Warn("Previous progress not found for scene retry", zap.Int("sceneIndex", progress.SceneIndex))
				return fmt.Errorf("cannot retry scene: previous progress not found")
			}
		}
	}

	// Сбрасываем CurrentSceneID для корректной генерации
	gs.CurrentSceneID = uuid.NullUUID{Valid: false}
	gs.PlayerStatus = models.PlayerStatusGeneratingScene
	gs.ErrorDetails = nil
	gs.LastActivityAt = time.Now().UTC()

	log.Info("Game state retry steps prepared successfully")
	return nil
}

// centralizedStatusUpdate централизованно обновляет статус истории с WebSocket уведомлением
func (s *gameLoopServiceImpl) centralizedStatusUpdate(
	ctx context.Context,
	tx pgx.Tx,
	storyID uuid.UUID,
	userID uuid.UUID,
	status models.StoryStatus,
	isFirstScenePending bool,
	areImagesPending bool,
	pendingCharGenTasks int,
	pendingCardImgTasks int,
	pendingCoverImgTasks int,
	errorDetails *string,
	internalStep *models.InternalGenerationStep,
	log *zap.Logger,
) error {
	publishedRepo := database.NewPgPublishedStoryRepository(tx, s.logger)

	// Обновляем статус в базе данных
	err := publishedRepo.UpdateStatusFlagsAndDetails(
		ctx, tx, storyID, status,
		isFirstScenePending, areImagesPending,
		pendingCharGenTasks, pendingCardImgTasks, pendingCoverImgTasks,
		errorDetails, internalStep,
	)
	if err != nil {
		log.Error("Failed to update story status in database",
			zap.String("storyID", storyID.String()),
			zap.String("status", string(status)),
			zap.Error(err))
		return fmt.Errorf("failed to update story status: %w", err)
	}

	// Отправляем WebSocket уведомление
	if s.clientPub != nil {
		statusStr := string(status)
		clientUpdate := models.ClientStoryUpdate{
			ID:           storyID.String(),
			UserID:       userID.String(),
			UpdateType:   models.UpdateTypeStory,
			Status:       statusStr,
			ErrorDetails: errorDetails,
		}

		if pubErr := s.clientPub.PublishClientUpdate(ctx, clientUpdate); pubErr != nil {
			log.Warn("Failed to send status update WebSocket notification",
				zap.String("storyID", storyID.String()),
				zap.String("status", statusStr),
				zap.Error(pubErr))
		} else {
			log.Info("Successfully sent status update WebSocket notification",
				zap.String("storyID", storyID.String()),
				zap.String("status", statusStr))
		}
	}

	log.Info("Centralized status update completed successfully",
		zap.String("storyID", storyID.String()),
		zap.String("status", string(status)))

	return nil
}

// centralizedGameStateStatusUpdate централизованно обновляет статус игрового состояния с WebSocket уведомлением
func (s *gameLoopServiceImpl) centralizedGameStateStatusUpdate(
	ctx context.Context,
	tx pgx.Tx,
	gameStateID uuid.UUID,
	userID uuid.UUID,
	status models.PlayerStatus,
	errorDetails *string,
	currentSceneID *uuid.UUID,
	log *zap.Logger,
) error {
	gameStateRepo := database.NewPgPlayerGameStateRepository(tx, s.logger)

	// Получаем текущее состояние
	gs, err := gameStateRepo.GetByID(ctx, tx, gameStateID)
	if err != nil {
		log.Error("Failed to get game state for status update",
			zap.String("gameStateID", gameStateID.String()),
			zap.Error(err))
		return fmt.Errorf("failed to get game state: %w", err)
	}

	// Обновляем статус
	gs.PlayerStatus = status
	gs.ErrorDetails = errorDetails
	gs.LastActivityAt = time.Now().UTC()

	if currentSceneID != nil {
		gs.CurrentSceneID = uuid.NullUUID{UUID: *currentSceneID, Valid: true}
	}

	// Сохраняем в базе данных
	if _, saveErr := gameStateRepo.Save(ctx, tx, gs); saveErr != nil {
		log.Error("Failed to save game state status update",
			zap.String("gameStateID", gameStateID.String()),
			zap.String("status", string(status)),
			zap.Error(saveErr))
		return fmt.Errorf("failed to save game state status: %w", saveErr)
	}

	// Отправляем WebSocket уведомление
	if s.clientPub != nil {
		statusStr := string(status)
		clientUpdate := models.ClientStoryUpdate{
			ID:           gameStateID.String(),
			UserID:       userID.String(),
			UpdateType:   models.UpdateTypeGameState,
			Status:       statusStr,
			ErrorDetails: errorDetails,
		}

		if currentSceneID != nil {
			sceneIDStr := currentSceneID.String()
			clientUpdate.SceneID = &sceneIDStr
		}

		if pubErr := s.clientPub.PublishClientUpdate(ctx, clientUpdate); pubErr != nil {
			log.Warn("Failed to send game state status update WebSocket notification",
				zap.String("gameStateID", gameStateID.String()),
				zap.String("status", statusStr),
				zap.Error(pubErr))
		} else {
			log.Info("Successfully sent game state status update WebSocket notification",
				zap.String("gameStateID", gameStateID.String()),
				zap.String("status", statusStr))
		}
	}

	log.Info("Centralized game state status update completed successfully",
		zap.String("gameStateID", gameStateID.String()),
		zap.String("status", string(status)))

	return nil
}
