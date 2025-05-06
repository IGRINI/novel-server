package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"novel-server/shared/database"
	sharedMessaging "novel-server/shared/messaging"
	"novel-server/shared/models"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// RetryGenerationForGameState handles retrying generation for a specific game state.
func (s *gameLoopServiceImpl) RetryGenerationForGameState(ctx context.Context, userID, storyID, gameStateID uuid.UUID) (err error) {
	log := s.logger.With(
		zap.String("gameStateID", gameStateID.String()),
		zap.String("publishedStoryID", storyID.String()),
		zap.Stringer("userID", userID),
	)
	log.Info("RetryGenerationForGameState called")

	var generationPayload *sharedMessaging.GenerationTaskPayload
	var setupPayload *sharedMessaging.GenerationTaskPayload

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		log.Error("Failed to begin transaction for RetryGenerationForGameState", zap.Error(err))
		return models.ErrInternalServer
	}
	defer func() {
		if r := recover(); r != nil {
			log.Error("Panic recovered during RetryGenerationForGameState, rolling back transaction", zap.Any("panic", r))
			_ = tx.Rollback(context.Background())
			err = fmt.Errorf("panic during RetryGenerationForGameState: %v", r)
		} else if err != nil {
			log.Warn("Rolling back transaction due to error during RetryGenerationForGameState", zap.Error(err))
			if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
				log.Error("Failed to rollback RetryGenerationForGameState transaction", zap.Error(rollbackErr))
			}
		} else {
			log.Info("Attempting to commit RetryGenerationForGameState transaction")
			if commitErr := tx.Commit(ctx); commitErr != nil {
				log.Error("Failed to commit RetryGenerationForGameState transaction", zap.Error(commitErr))
				err = fmt.Errorf("error committing RetryGenerationForGameState transaction: %w", commitErr)
			} else {
				log.Info("RetryGenerationForGameState transaction committed successfully")
				if setupPayload != nil {
					log.Info("Publishing setup generation task (retry) after commit", zap.String("taskID", setupPayload.TaskID))
					if errPub := s.publisher.PublishGenerationTask(context.Background(), *setupPayload); errPub != nil {
						log.Error("Error publishing retry setup generation task POST-COMMIT", zap.Error(errPub))
					}
				} else if generationPayload != nil {
					log.Info("Publishing scene/gameover generation task (retry) after commit", zap.String("taskID", generationPayload.TaskID), zap.String("promptType", string(generationPayload.PromptType)))
					if errPub := s.publisher.PublishGenerationTask(context.Background(), *generationPayload); errPub != nil {
						log.Error("Error publishing retry scene/gameover generation task POST-COMMIT", zap.Error(errPub))
					}
				}
			}
		}
	}()

	gameStateRepoTx := database.NewPgPlayerGameStateRepository(tx, s.logger)
	publishedRepoTx := database.NewPgPublishedStoryRepository(tx, s.logger)
	playerProgressRepoTx := database.NewPgPlayerProgressRepository(tx, s.logger)

	gameState, errState := gameStateRepoTx.GetByID(ctx, tx, gameStateID)
	if errState != nil {
		if errors.Is(errState, models.ErrNotFound) || errors.Is(errState, pgx.ErrNoRows) {
			log.Warn("Player game state not found for retry")
			err = models.ErrPlayerGameStateNotFound
		} else {
			log.Error("Failed to get player game state for retry", zap.Error(errState))
			err = models.ErrInternalServer
		}
		return err
	}

	if gameState.PlayerID != userID {
		log.Warn("User attempted to retry game state they do not own", zap.Stringer("ownerID", gameState.PlayerID))
		err = models.ErrForbidden
		return err
	}

	if gameState.PlayerStatus != models.PlayerStatusError && gameState.PlayerStatus != models.PlayerStatusGeneratingScene {
		log.Warn("Attempt to retry generation for game state not in Error or GeneratingScene status", zap.String("status", string(gameState.PlayerStatus)))
		err = models.ErrCannotRetry
		return err
	}
	if gameState.PlayerStatus == models.PlayerStatusGeneratingScene {
		log.Warn("Retrying generation for game state still marked as GeneratingScene. Worker might have failed unexpectedly.")
	}

	publishedStory, errStory := publishedRepoTx.GetByID(ctx, tx, gameState.PublishedStoryID)
	if errStory != nil {
		if errors.Is(errStory, pgx.ErrNoRows) || errors.Is(errStory, models.ErrNotFound) {
			log.Error("Published story linked to game state not found", zap.String("storyID", gameState.PublishedStoryID.String()), zap.Error(errStory))
			err = models.ErrStoryNotFound
		} else {
			log.Error("Failed to get published story for retry", zap.Error(errStory))
			err = models.ErrInternalServer
		}
		return err
	}

	setupExists := publishedStory.Setup != nil && string(publishedStory.Setup) != "null"

	if !setupExists {
		log.Warn("Retrying generation, but published story setup is missing. Attempting setup retry.", zap.String("storyStatus", string(publishedStory.Status)))

		if publishedStory.Config == nil {
			log.Error("CRITICAL: Story Config is nil, cannot retry Setup generation.")
			gameState.PlayerStatus = models.PlayerStatusError
			errMsg := "Cannot retry: Story Config is missing"
			gameState.ErrorDetails = &errMsg
			if _, saveErr := gameStateRepoTx.Save(ctx, tx, gameState); saveErr != nil {
				log.Error("Failed to update game state to Error after discovering missing config", zap.Error(saveErr))
			}
			err = models.ErrInternalServer
			return err
		}

		if publishedStory.Status == models.StatusError {
			if errUpdate := publishedRepoTx.UpdateStatusFlagsAndDetails(ctx, tx, publishedStory.ID, models.StatusSetupPending, false, false, nil); errUpdate != nil {
				log.Error("Failed to update story status to SetupPending before setup retry task publish", zap.Error(errUpdate))
			} else {
				log.Info("Updated PublishedStory status to SetupPending for setup retry")
			}
		}

		taskID := uuid.New().String()
		configJSONString := string(publishedStory.Config)
		setupPayloadLocal := sharedMessaging.GenerationTaskPayload{
			TaskID:           taskID,
			UserID:           publishedStory.UserID.String(),
			PromptType:       models.PromptTypeNovelSetup,
			UserInput:        configJSONString,
			PublishedStoryID: publishedStory.ID.String(),
			Language:         publishedStory.Language,
		}
		setupPayload = &setupPayloadLocal

		log.Info("Setup generation task payload created for post-commit publish", zap.String("taskID", taskID))
		return nil
	} else {
		log.Info("Proceeding with scene/game_over retry for the game state")

		if gameState.PlayerProgressID == uuid.Nil {
			log.Error("CRITICAL: GameState is in Error/Generating but PlayerProgressID is Nil.")
			gameState.PlayerStatus = models.PlayerStatusError
			errMsg := "Cannot retry: Inconsistent state (missing PlayerProgressID)"
			gameState.ErrorDetails = &errMsg
			if _, saveErr := gameStateRepoTx.Save(ctx, tx, gameState); saveErr != nil {
				log.Error("Failed to update game state to Error after discovering missing progress ID", zap.Error(saveErr))
			}
			err = models.ErrInternalServer
			return err
		}

		progress, errProgress := playerProgressRepoTx.GetByID(ctx, tx, gameState.PlayerProgressID)
		if errProgress != nil {
			log.Error("Failed to get PlayerProgress node linked to game state for retry", zap.String("progressID", gameState.PlayerProgressID.String()), zap.Error(errProgress))
			err = models.ErrInternalServer
			return err
		}

		promptTypeToUse := models.PromptTypeNovelCreator
		if !gameState.CurrentSceneID.Valid {
			promptTypeToUse = models.PromptTypeNovelGameOverCreator
			log.Info("Identified as Game Over retry.")
			gameState.PlayerStatus = models.PlayerStatusGeneratingScene
		} else {
			log.Info("Identified as Scene retry.")
		}

		gameState.PlayerStatus = models.PlayerStatusGeneratingScene
		gameState.ErrorDetails = nil
		gameState.LastActivityAt = time.Now().UTC()
		if _, errSave := gameStateRepoTx.Save(ctx, tx, gameState); errSave != nil {
			log.Error("Failed to update game state status before retry task publish", zap.String("targetStatus", string(models.PlayerStatusGeneratingScene)), zap.Error(errSave))
			err = models.ErrInternalServer
			return err
		}
		log.Info("Updated game state status before retry task publish", zap.String("newStatus", string(gameState.PlayerStatus)))

		madeChoicesInfo := []models.UserChoiceInfo{}
		generationPayloadLocal, errGenPayload := createGenerationPayload(
			userID,
			publishedStory,
			progress,
			gameState,
			madeChoicesInfo,
			progress.CurrentStateHash,
			publishedStory.Language,
			promptTypeToUse,
		)
		if errGenPayload != nil {
			log.Error("Failed to create generation payload for scene/gameover retry", zap.Error(errGenPayload))
			errMsg := fmt.Sprintf("Failed to create generation payload: %v", errGenPayload)
			gameState.PlayerStatus = models.PlayerStatusError
			gameState.ErrorDetails = &errMsg
			if _, saveErr := gameStateRepoTx.Save(context.Background(), tx, gameState); saveErr != nil {
				log.Error("Failed to roll back game state status to Error after payload creation error", zap.Error(saveErr))
			}
			err = models.ErrInternalServer
			return err
		}
		generationPayloadLocal.GameStateID = gameStateID.String()
		generationPayload = &generationPayloadLocal

		log.Info("Scene/Game Over generation task payload created for post-commit publish", zap.String("taskID", generationPayload.TaskID), zap.String("promptType", string(generationPayload.PromptType)))
		return nil
	}
}

// RetryInitialGeneration handles retrying generation for a published story's Setup or Initial Scene.
func (s *gameLoopServiceImpl) RetryInitialGeneration(ctx context.Context, userID, storyID uuid.UUID) (err error) {
	log := s.logger.With(zap.String("storyID", storyID.String()), zap.Stringer("userID", userID))
	log.Info("RetryInitialGeneration called")

	var setupPayload *sharedMessaging.GenerationTaskPayload
	var initialScenePayload *sharedMessaging.GenerationTaskPayload
	var coverImageBatchPayload *sharedMessaging.CharacterImageTaskBatchPayload
	var clientUpdatePayload *models.ClientStoryUpdate

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		log.Error("Failed to begin transaction for RetryInitialGeneration", zap.Error(err))
		return models.ErrInternalServer
	}
	defer func() {
		if r := recover(); r != nil {
			log.Error("Panic recovered during RetryInitialGeneration, rolling back transaction", zap.Any("panic", r))
			_ = tx.Rollback(context.Background())
			err = fmt.Errorf("panic during RetryInitialGeneration: %v", r)
		} else if err != nil {
			log.Warn("Rolling back transaction due to error during RetryInitialGeneration", zap.Error(err))
			if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
				log.Error("Failed to rollback RetryInitialGeneration transaction", zap.Error(rollbackErr))
			}
		} else {
			log.Info("Attempting to commit RetryInitialGeneration transaction")
			if commitErr := tx.Commit(ctx); commitErr != nil {
				log.Error("Failed to commit RetryInitialGeneration transaction", zap.Error(commitErr))
				err = fmt.Errorf("error committing RetryInitialGeneration transaction: %w", commitErr)
			} else {
				log.Info("RetryInitialGeneration transaction committed successfully")
				bgCtx := context.Background()
				if setupPayload != nil {
					log.Info("Publishing setup generation task (retry) after commit", zap.String("taskID", setupPayload.TaskID))
					if errPub := s.publisher.PublishGenerationTask(bgCtx, *setupPayload); errPub != nil {
						log.Error("Error publishing retry setup generation task POST-COMMIT", zap.Error(errPub))
					}
				} else if initialScenePayload != nil {
					log.Info("Publishing initial scene task (retry) after commit", zap.String("taskID", initialScenePayload.TaskID))
					if errPub := s.publisher.PublishGenerationTask(bgCtx, *initialScenePayload); errPub != nil {
						log.Error("Failed to publish initial scene retry task POST-COMMIT", zap.Error(errPub))
					}
				} else if coverImageBatchPayload != nil {
					log.Info("Publishing cover image task (retry) after commit", zap.String("batch_id", coverImageBatchPayload.BatchID))
					if errPub := s.characterImageTaskBatchPub.PublishCharacterImageTaskBatch(bgCtx, *coverImageBatchPayload); errPub != nil {
						log.Error("Failed to publish cover image retry task POST-COMMIT", zap.Error(errPub))
					}
				} else if clientUpdatePayload != nil {
					log.Info("Sending ClientStoryUpdate (Story Status Corrected) after commit", zap.String("storyID", clientUpdatePayload.ID))
					if s.clientPub != nil {
						wsCtx, wsCancel := context.WithTimeout(bgCtx, 10*time.Second)
						if errWs := s.clientPub.PublishClientUpdate(wsCtx, *clientUpdatePayload); errWs != nil {
							log.Error("Error sending ClientStoryUpdate (Story Status Corrected) POST-COMMIT", zap.Error(errWs))
						}
						wsCancel()
					} else {
						log.Warn("ClientUpdatePublisher (clientPub) is nil in gameLoopServiceImpl. Cannot send WebSocket update for status correction POST-COMMIT.")
					}
				}
			}
		}
	}()

	publishedRepoTx := database.NewPgPublishedStoryRepository(tx, s.logger)
	sceneRepoTx := database.NewPgStorySceneRepository(tx, s.logger)
	dynamicConfigRepoTx := database.NewPgDynamicConfigRepository(tx, s.logger)

	publishedStory, errStory := publishedRepoTx.GetByID(ctx, tx, storyID)
	if errStory != nil {
		if errors.Is(errStory, models.ErrNotFound) || errors.Is(errStory, pgx.ErrNoRows) {
			log.Warn("Published story not found for retry")
			err = models.ErrNotFound
		} else {
			log.Error("Failed to get published story for retry", zap.Error(errStory))
			err = models.ErrInternalServer
		}
		return err
	}

	if publishedStory.UserID != userID {
		log.Warn("User attempted to retry generation for story they do not own")
		err = models.ErrForbidden
		return err
	}

	setupIsValid := false
	initialSceneIsValid := false
	sceneExists := false
	var initialSceneID uuid.UUID

	if len(publishedStory.Setup) > 0 {
		var setupContent models.NovelSetupContent
		if errUnmarshalSetup := json.Unmarshal(publishedStory.Setup, &setupContent); errUnmarshalSetup == nil {
			setupIsValid = true
		} else if publishedStory.Status == models.StatusError {
			log.Error("Invalid Setup JSON for story in Error status, will attempt setup regeneration.", zap.Error(errUnmarshalSetup))
		}
	}

	if setupIsValid {
		scene, errScene := sceneRepoTx.FindByStoryAndHash(ctx, tx, storyID, models.InitialStateHash)
		if errScene == nil && scene != nil {
			sceneExists = true
			initialSceneID = scene.ID
			if len(scene.Content) > 0 && json.Valid(scene.Content) {
				initialSceneIsValid = true
			} else if publishedStory.Status == models.StatusError {
				log.Warn("Initial scene content is empty or invalid for story in Error status, will retry scene generation.", zap.String("sceneID", initialSceneID.String()))
			}
		} else if !errors.Is(errScene, pgx.ErrNoRows) && !errors.Is(errScene, models.ErrNotFound) {
			log.Error("Failed to check for initial scene existence during retry", zap.Error(errScene))
			err = models.ErrInternalServer
			return err
		}
	}

	if !setupIsValid {
		log.Info("Setup is missing or was invalid, retrying setup generation")
		if len(publishedStory.Config) == 0 {
			log.Error("CRITICAL: Story Config is nil or empty, cannot retry Setup generation.")
			errMsg := "Cannot retry Setup: Story Config is missing"
			if errUpdate := publishedRepoTx.UpdateStatusFlagsAndDetails(context.Background(), tx, storyID, models.StatusError, false, false, &errMsg); errUpdate != nil {
				log.Error("Failed to update story status to Error after discovering missing config for setup retry", zap.Error(errUpdate))
			}
			err = models.ErrInternalServer
			return err
		}

		log.Info("Updating story status to SetupPending before setup retry task publish")
		if errUpdate := publishedRepoTx.UpdateStatusFlagsAndDetails(ctx, tx, storyID, models.StatusSetupPending, false, false, nil); errUpdate != nil {
			log.Error("Failed to update story status to SetupPending before setup retry task publish", zap.Error(errUpdate))
			err = fmt.Errorf("failed to update story status for setup retry: %w", errUpdate)
			return err
		}

		taskID := uuid.New().String()
		configJSONString := string(publishedStory.Config)
		setupPayloadLocal := sharedMessaging.GenerationTaskPayload{
			TaskID:           taskID,
			UserID:           publishedStory.UserID.String(),
			PromptType:       models.PromptTypeNovelSetup,
			UserInput:        configJSONString,
			PublishedStoryID: publishedStory.ID.String(),
			Language:         publishedStory.Language,
		}
		setupPayload = &setupPayloadLocal

		log.Info("Setup generation task payload created for post-commit publish", zap.String("taskID", taskID))
		return nil
	}

	if !sceneExists || !initialSceneIsValid {
		log.Info("Initial scene is missing or was invalid, retrying first scene generation")
		if errUpdate := publishedRepoTx.UpdateStatusFlagsAndDetails(ctx, tx, storyID, models.StatusFirstScenePending, true, publishedStory.AreImagesPending, nil); errUpdate != nil {
			log.Error("Failed to update story status to FirstScenePending before retry", zap.Error(errUpdate))
			err = fmt.Errorf("failed to update story status: %w", errUpdate)
			return err
		}

		payloadLocal, errPayload := createInitialSceneGenerationPayload(userID, publishedStory, publishedStory.Language)
		if errPayload != nil {
			log.Error("Failed to create initial scene generation payload for retry", zap.Error(errPayload))
			errMsg := fmt.Sprintf("Failed to create initial scene payload: %v", errPayload)
			if errUpdate := publishedRepoTx.UpdateStatusFlagsAndDetails(context.Background(), tx, storyID, publishedStory.Status, false, publishedStory.AreImagesPending, &errMsg); errUpdate != nil {
				log.Error("Failed to revert IsFirstScenePending after payload creation error", zap.Error(errUpdate))
			}
			err = fmt.Errorf("failed to create generation payload: %w", errPayload)
			return err
		}
		initialScenePayload = &payloadLocal

		log.Info("Initial scene task payload created for post-commit publish", zap.String("taskID", initialScenePayload.TaskID))
		return nil
	}

	if publishedStory.AreImagesPending {
		log.Info("Initial scene text exists, but AreImagesPending is true. Retrying image generation.", zap.String("sceneID", initialSceneID.String()))

		var setupContent models.NovelSetupContent
		if errUnmarshalSetup := json.Unmarshal(publishedStory.Setup, &setupContent); errUnmarshalSetup != nil {
			log.Error("Failed to unmarshal setup JSON to reconstruct cover prompt", zap.Error(errUnmarshalSetup))
			err = fmt.Errorf("failed to parse setup for cover image retry: %w", errUnmarshalSetup)
			return err
		}
		if setupContent.StoryPreviewImagePrompt == "" {
			log.Error("Cannot retry cover image generation: StoryPreviewImagePrompt is empty in setup")
			err = fmt.Errorf("cannot retry cover image: StoryPreviewImagePrompt is missing")
			return err
		}

		var previewStyleSuffix string
		previewDynConfKey := "prompt.story_preview_style_suffix"
		dynamicConfigPreview, errConfPreview := dynamicConfigRepoTx.GetByKey(ctx, tx, previewDynConfKey)
		if errConfPreview != nil && !errors.Is(errConfPreview, models.ErrNotFound) {
			log.Error("Failed to get dynamic config for story preview style suffix", zap.String("key", previewDynConfKey), zap.Error(errConfPreview))
		} else if dynamicConfigPreview != nil {
			previewStyleSuffix = dynamicConfigPreview.Value
		}

		var fullConfig models.Config
		var characterVisualStyle, storyStyle string
		if errUnmarshalConfig := json.Unmarshal(publishedStory.Config, &fullConfig); errUnmarshalConfig == nil {
			characterVisualStyle = fullConfig.PlayerPrefs.CharacterVisualStyle
			storyStyle = fullConfig.PlayerPrefs.Style
			if characterVisualStyle != "" {
				characterVisualStyle = ", " + characterVisualStyle
			}
			if storyStyle != "" {
				storyStyle = ", " + storyStyle
			}
		}
		reconstructedCoverPrompt := setupContent.StoryPreviewImagePrompt + storyStyle + characterVisualStyle + previewStyleSuffix

		if !publishedStory.AreImagesPending {
			if errUpdate := publishedRepoTx.UpdateStatusFlagsAndDetails(ctx, tx, storyID, publishedStory.Status, publishedStory.IsFirstScenePending, true, nil); errUpdate != nil {
				log.Error("Failed to update story AreImagesPending flag before image retry", zap.Error(errUpdate))
				err = fmt.Errorf("failed to update story status for image retry: %w", errUpdate)
				return err
			}
		}

		previewImageRef := fmt.Sprintf("history_preview_%s", storyID.String())
		coverTaskPayloadLocal := sharedMessaging.CharacterImageTaskPayload{
			TaskID:           uuid.New().String(),
			UserID:           userID.String(),
			CharacterID:      storyID,
			Prompt:           reconstructedCoverPrompt,
			NegativePrompt:   "",
			ImageReference:   previewImageRef,
			Ratio:            "3:2",
			PublishedStoryID: storyID,
		}
		coverBatchPayloadLocal := sharedMessaging.CharacterImageTaskBatchPayload{BatchID: uuid.New().String(), Tasks: []sharedMessaging.CharacterImageTaskPayload{coverTaskPayloadLocal}}
		coverImageBatchPayload = &coverBatchPayloadLocal

		log.Info("Cover image task payload created for post-commit publish", zap.String("taskID", coverTaskPayloadLocal.TaskID))
		return nil
	}

	log.Info("Initial generation steps (setup, scene text, cover) validated.")
	if publishedStory.Status == models.StatusReady {
		log.Warn("Story status is already Ready. Nothing to retry.")
		return nil
	} else {
		log.Info("Correcting story status to Ready.", zap.String("current_status", string(publishedStory.Status)))
		errUpdate := publishedRepoTx.UpdateStatusFlagsAndDetails(ctx, tx, storyID, models.StatusReady, false, false, nil)
		if errUpdate != nil {
			log.Error("Failed to correct story status to Ready after validation.", zap.Error(errUpdate))
			err = fmt.Errorf("failed to correct story status: %w", errUpdate)
			return err
		}
		log.Info("Successfully corrected story status to Ready.")

		clientUpdatePayload = &models.ClientStoryUpdate{
			ID:           publishedStory.ID.String(),
			UserID:       publishedStory.UserID.String(),
			UpdateType:   models.UpdateTypeStory,
			Status:       string(models.StatusReady),
			ErrorDetails: nil,
			StoryTitle:   publishedStory.Title,
		}
		log.Info("Client update payload created for post-commit send")
		return nil
	}
}
