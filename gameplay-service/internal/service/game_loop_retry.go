package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	sharedMessaging "novel-server/shared/messaging"
	"novel-server/shared/models"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// RetryGenerationForGameState handles retrying generation for a specific game state.
func (s *gameLoopServiceImpl) RetryGenerationForGameState(ctx context.Context, userID, storyID, gameStateID uuid.UUID) error {
	log := s.logger.With(
		zap.String("gameStateID", gameStateID.String()),
		zap.String("publishedStoryID", storyID.String()),
		zap.Stringer("userID", userID),
	)
	log.Info("RetryGenerationForGameState called")

	// 1. Get the target game state
	gameState, errState := s.playerGameStateRepo.GetByID(ctx, gameStateID)
	if errState != nil {
		if errors.Is(errState, models.ErrNotFound) || errors.Is(errState, pgx.ErrNoRows) {
			log.Warn("Player game state not found for retry")
			return models.ErrPlayerGameStateNotFound
		}
		log.Error("Failed to get player game state for retry", zap.Error(errState))
		return models.ErrInternalServer
	}

	// 2. Verify ownership
	if gameState.PlayerID != userID {
		log.Warn("User attempted to retry game state they do not own", zap.Stringer("ownerID", gameState.PlayerID))
		return models.ErrForbidden
	}

	// 3. Check current game state status (should be Error, maybe GeneratingScene if worker died)
	if gameState.PlayerStatus != models.PlayerStatusError && gameState.PlayerStatus != models.PlayerStatusGeneratingScene {
		log.Warn("Attempt to retry generation for game state not in Error or GeneratingScene status", zap.String("status", string(gameState.PlayerStatus)))
		return models.ErrCannotRetry // Or a more specific error
	}
	if gameState.PlayerStatus == models.PlayerStatusGeneratingScene {
		log.Warn("Retrying generation for game state still marked as GeneratingScene. Worker might have failed unexpectedly.")
	}

	// 4. Get the associated Published Story
	// Use gameState.PublishedStoryID which should be the same as the input storyID
	publishedStory, errStory := s.publishedRepo.GetByID(ctx, gameState.PublishedStoryID)
	if errStory != nil {
		if errors.Is(errStory, pgx.ErrNoRows) {
			log.Error("Published story linked to game state not found", zap.String("storyID", gameState.PublishedStoryID.String()), zap.Error(errStory))
			return models.ErrStoryNotFound // Data inconsistency
		}
		log.Error("Failed to get published story for retry", zap.Error(errStory))
		return models.ErrInternalServer
	}

	// 5. Check if Setup exists (essential for scene generation)
	setupExists := publishedStory.Setup != nil && string(publishedStory.Setup) != "null"

	if !setupExists {
		// --- Setup generation failed or was never completed ---
		// This is unusual if a game state already exists, but handle it defensively.
		log.Warn("Retrying generation, but published story setup is missing. Attempting setup retry.", zap.String("storyStatus", string(publishedStory.Status)))

		if publishedStory.Config == nil {
			log.Error("CRITICAL: Story Config is nil, cannot retry Setup generation.")
			// Update game state to Error to prevent further retries?
			gameState.PlayerStatus = models.PlayerStatusError
			errMsg := "Cannot retry: Story Config is missing"
			gameState.ErrorDetails = &errMsg
			if _, saveErr := s.playerGameStateRepo.Save(ctx, gameState); saveErr != nil {
				log.Error("Failed to update game state to Error after discovering missing config", zap.Error(saveErr))
			}
			return models.ErrInternalServer // Cannot proceed
		}

		// Update PublishedStory status back to SetupPending (if it was Error)
		// We don't touch the game state status here yet.
		if publishedStory.Status == models.StatusError {
			if err := s.publishedRepo.UpdateStatusFlagsAndDetails(ctx, publishedStory.ID, models.StatusSetupPending, false, false, nil); err != nil {
				log.Error("Failed to update story status to SetupPending before setup retry task publish", zap.Error(err))
				// Don't necessarily fail the game state retry yet, maybe setup task publish will succeed.
			} else {
				log.Info("Updated PublishedStory status to SetupPending for setup retry")
			}
		}

		// Create and publish Setup task payload
		taskID := uuid.New().String()
		configJSONString := string(publishedStory.Config)
		setupPayload := sharedMessaging.GenerationTaskPayload{
			TaskID:           taskID,
			UserID:           publishedStory.UserID.String(), // Use UserID from the story
			PromptType:       models.PromptTypeNovelSetup,
			UserInput:        configJSONString,
			PublishedStoryID: publishedStory.ID.String(),
			Language:         publishedStory.Language,
			// GameStateID is not relevant for setup task
		}

		if errPub := s.publisher.PublishGenerationTask(ctx, setupPayload); errPub != nil {
			log.Error("Error publishing retry setup generation task", zap.Error(errPub))
			// If publishing failed, maybe revert story status? Or just return error?
			// Let's return an error, the caller might try again.
			return models.ErrInternalServer
		}

		log.Info("Retry setup generation task published successfully", zap.String("taskID", taskID))
		// Even though we retried setup, the game state remains (maybe in Error).
		// The user needs to wait for setup and then potentially retry the game state again if the initial scene fails later.
		return nil // Indicate setup retry was initiated.

	} else {
		// --- Setup exists, proceed with Scene generation retry ---
		log.Info("Setup exists, proceeding with scene generation retry for the game state")

		// Check/Generate setup images (async, doesn't block retry)
		// TODO: Consider potential race conditions if multiple retries happen quickly.
		go func(bgCtx context.Context) {
			_, errImages := s.checkAndGenerateSetupImages(bgCtx, publishedStory, publishedStory.Setup, userID)
			if errImages != nil {
				s.logger.Error("Error during background checkAndGenerateSetupImages in scene retry",
					zap.String("publishedStoryID", publishedStory.ID.String()),
					zap.String("gameStateID", gameStateID.String()),
					zap.Error(errImages))
			}
		}(context.WithoutCancel(ctx)) // Run in background with independent context

		// 6. Determine if it's the initial scene or a subsequent scene
		// <<< ИЗМЕНЕНО: Сравниваем uuid.UUID с uuid.Nil >>>
		if gameState.PlayerProgressID == uuid.Nil {
			// This shouldn't happen if status is Error/GeneratingScene and setup exists, indicates inconsistency.
			log.Error("CRITICAL: GameState is in Error/Generating but PlayerProgressID is Nil. Cannot determine scene type.")
			gameState.PlayerStatus = models.PlayerStatusError // Ensure it's Error
			errMsg := "Cannot retry: Inconsistent state (missing PlayerProgressID)"
			gameState.ErrorDetails = &errMsg
			if _, saveErr := s.playerGameStateRepo.Save(ctx, gameState); saveErr != nil {
				log.Error("Failed to update game state to Error after discovering missing progress ID", zap.Error(saveErr))
			}
			return models.ErrInternalServer
		}

		// Get the progress node associated with the game state
		// <<< ИЗМЕНЕНО: Нет индирекции для uuid.UUID >>>
		progress, errProgress := s.playerProgressRepo.GetByID(ctx, gameState.PlayerProgressID)
		if errProgress != nil {
			// <<< ИЗМЕНЕНО: Используем String() для uuid.UUID >>>
			log.Error("Failed to get PlayerProgress node linked to game state for retry", zap.String("progressID", gameState.PlayerProgressID.String()), zap.Error(errProgress))
			// Update game state to Error?
			return models.ErrInternalServer
		}

		// Update GameState status to GeneratingScene before publishing task
		gameState.PlayerStatus = models.PlayerStatusGeneratingScene
		gameState.ErrorDetails = nil                // Clear previous error
		gameState.LastActivityAt = time.Now().UTC() // Update activity time
		if _, errSave := s.playerGameStateRepo.Save(ctx, gameState); errSave != nil {
			log.Error("Failed to update game state status to GeneratingScene before retry task publish", zap.Error(errSave))
			return models.ErrInternalServer
		}
		log.Info("Updated game state status to GeneratingScene")

		if progress.CurrentStateHash == models.InitialStateHash {
			// --- Retrying Initial Scene ---
			log.Info("Retrying initial scene generation for game state", zap.String("initialHash", models.InitialStateHash))

			generationPayload, errPayload := createInitialSceneGenerationPayload(userID, publishedStory)
			if errPayload != nil {
				log.Error("Failed to create initial generation payload for scene retry", zap.Error(errPayload))
				// Attempt to roll back game state status?
				gameState.PlayerStatus = models.PlayerStatusError
				errMsg := fmt.Sprintf("Failed to create initial generation payload: %v", errPayload)
				gameState.ErrorDetails = &errMsg
				if _, saveErr := s.playerGameStateRepo.Save(context.Background(), gameState); saveErr != nil { // Use background context for rollback attempt
					log.Error("Failed to roll back game state status after initial payload creation error", zap.Error(saveErr))
				}
				return models.ErrInternalServer
			}
			generationPayload.GameStateID = gameStateID.String() // Add the specific game state ID

			if errPub := s.publisher.PublishGenerationTask(ctx, generationPayload); errPub != nil {
				log.Error("Error publishing initial scene retry generation task", zap.Error(errPub))
				// Attempt to roll back game state status?
				gameState.PlayerStatus = models.PlayerStatusError
				errMsg := fmt.Sprintf("Failed to publish initial generation task: %v", errPub)
				gameState.ErrorDetails = &errMsg
				if _, saveErr := s.playerGameStateRepo.Save(context.Background(), gameState); saveErr != nil {
					log.Error("Failed to roll back game state status after initial scene retry publish error", zap.Error(saveErr))
				}
				return models.ErrInternalServer
			}
			log.Info("Initial scene retry generation task published successfully", zap.String("taskID", generationPayload.TaskID))
			return nil

		} else {
			// --- Retrying Subsequent Scene ---
			log.Info("Retrying subsequent scene generation for game state", zap.String("stateHash", progress.CurrentStateHash))

			madeChoicesInfo := []models.UserChoiceInfo{} // No choices info on retry
			generationPayload, errGenPayload := createGenerationPayload(
				userID,
				publishedStory,
				progress,
				gameState, // Pass the actual game state object
				madeChoicesInfo,
				progress.CurrentStateHash, // Retry for the hash in the progress node
			)
			if errGenPayload != nil {
				log.Error("Failed to create generation payload for scene retry", zap.Error(errGenPayload))
				// Attempt to roll back game state status?
				gameState.PlayerStatus = models.PlayerStatusError
				errMsg := fmt.Sprintf("Failed to create generation payload: %v", errGenPayload)
				gameState.ErrorDetails = &errMsg
				if _, saveErr := s.playerGameStateRepo.Save(context.Background(), gameState); saveErr != nil {
					log.Error("Failed to roll back game state status after payload creation error", zap.Error(saveErr))
				}
				return models.ErrInternalServer
			}
			generationPayload.GameStateID = gameStateID.String() // Add the specific game state ID

			if errPub := s.publisher.PublishGenerationTask(ctx, generationPayload); errPub != nil {
				log.Error("Error publishing retry scene generation task", zap.Error(errPub))
				// Attempt to roll back game state status?
				gameState.PlayerStatus = models.PlayerStatusError
				errMsg := fmt.Sprintf("Failed to publish generation task: %v", errPub)
				gameState.ErrorDetails = &errMsg
				if _, saveErr := s.playerGameStateRepo.Save(context.Background(), gameState); saveErr != nil {
					log.Error("Failed to roll back game state status after scene retry publish error", zap.Error(saveErr))
				}
				return models.ErrInternalServer
			}

			log.Info("Retry scene generation task published successfully", zap.String("taskID", generationPayload.TaskID))
			return nil
		}
	}
}

// RetryInitialGeneration handles retrying generation for a published story's Setup or Initial Scene.
func (s *gameLoopServiceImpl) RetryInitialGeneration(ctx context.Context, userID, storyID uuid.UUID) error {
	log := s.logger.With(zap.String("storyID", storyID.String()), zap.Stringer("userID", userID))
	log.Info("RetryInitialGeneration called")

	// 1. Получить опубликованную историю
	publishedStory, err := s.publishedRepo.GetByID(ctx, storyID)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			log.Warn("Published story not found for retry")
			return models.ErrNotFound
		}
		log.Error("Failed to get published story for retry", zap.Error(err))
		return models.ErrInternalServer
	}

	// 2. Проверить владельца (на всякий случай)
	if publishedStory.UserID != userID {
		log.Warn("User attempted to retry generation for story they do not own")
		return models.ErrForbidden
	}

	// 3. Проверить текущий статус и выполнить специальную логику для статуса Error
	sceneExists := false
	initialSceneIsValid := false
	var initialSceneID uuid.UUID // Понадобится для удаления

	if publishedStory.Status == models.StatusError {
		log.Info("Story status is Error, performing validation before retry")

		// 3a. Валидация Setup
		setupIsValid := false // Флаг для отслеживания валидности Setup
		if len(publishedStory.Setup) == 0 {
			log.Warn("Setup is nil or empty for story in Error status, proceeding to setup generation logic")
			// setupIsValid остается false
		} else {
			var setupContent models.NovelSetupContent
			if errUnmarshalSetup := json.Unmarshal(publishedStory.Setup, &setupContent); errUnmarshalSetup != nil {
				log.Error("Invalid Setup JSON for story in Error status, will attempt setup regeneration.", zap.Error(errUnmarshalSetup))
				// --- ИЗМЕНЕНИЕ: НЕ возвращаем ошибку, setupIsValid остается false ---
				//errMsg := fmt.Sprintf("Invalid setup data, cannot retry automatically: %v", errUnmarshalSetup)
				//if errUpdate := s.publishedRepo.UpdateStatusFlagsAndDetails(context.Background(), storyID, publishedStory.Status, publishedStory.IsFirstScenePending, publishedStory.AreImagesPending, &errMsg); errUpdate != nil {
				//	log.Error("Failed to update story error details about invalid setup", zap.Error(errUpdate))
				//}
				// return fmt.Errorf("%w: invalid setup data", models.ErrCannotRetry)
			} else {
				log.Info("Setup JSON is valid for story in Error status")
				setupIsValid = true // Setup валиден
			}
		}

		// 3b. Валидация начальной сцены (только если Setup валиден)
		if setupIsValid {
			// <<< ИЗМЕНЕНО: Используем FindByStoryAndHash как обход линтера >>>
			scene, errScene := s.sceneRepo.FindByStoryAndHash(ctx, storyID, models.InitialStateHash)
			if errScene == nil && scene != nil {
				// Сцена найдена
				initialSceneID = scene.ID // Сохраняем ID
				log.Info("Initial scene found for story in Error status.", zap.String("sceneID", initialSceneID.String()))
				sceneExists = true

				// Проверяем валидность контента сцены
				if len(scene.Content) == 0 || !json.Valid(scene.Content) {
					log.Warn("Initial scene content is empty or invalid, will retry scene generation.", zap.String("sceneID", initialSceneID.String()))
					initialSceneIsValid = false
					// Потенциально можно удалить невалидную сцену здесь, чтобы запустить регенерацию
					// dbCtxDel, cancelDel := context.WithTimeout(ctx, 5*time.Second)
					// if errDel := s.sceneRepo.DeleteByID(dbCtxDel, initialSceneID); errDel != nil {
					// 	log.Error("Failed to delete invalid initial scene during retry check", zap.Error(errDel))
					// } else {
					// 	log.Info("Deleted invalid initial scene to trigger regeneration", zap.String("sceneID", initialSceneID.String()))
					// 	sceneExists = false // Считаем, что сцены нет, раз удалили
					// }
					// cancelDel()
				} else {
					initialSceneIsValid = true
					log.Info("Initial scene content is valid.")
				}
			} else if !errors.Is(errScene, pgx.ErrNoRows) && !errors.Is(errScene, models.ErrNotFound) {
				// Ошибка при получении сцены из БД
				log.Error("Failed to check for initial scene existence during retry", zap.Error(errScene))
				return models.ErrInternalServer
			} else {
				// Сцена не найдена (ErrNoRows или ErrNotFound)
				log.Info("Initial scene not found for story in Error status.")
				sceneExists = false
			}
		}
	} // Конец блока if publishedStory.Status == models.StatusError

	// 4. Определяем, что нужно регенерировать, на основе проверок (включая проверки для статуса Error)

	// --- ИЗМЕНЕНИЕ: Явно проверяем флаг setupIsValid и len(publishedStory.Setup) == 0 ---
	// Объявляем setupIsValid здесь, чтобы он был доступен ниже
	setupIsValid := false
	if len(publishedStory.Setup) > 0 {
		var setupContent models.NovelSetupContent
		if errUnmarshalSetup := json.Unmarshal(publishedStory.Setup, &setupContent); errUnmarshalSetup == nil {
			setupIsValid = true
		}
	}

	// Определяем initialSceneIsValid, sceneExists и initialSceneID здесь, чтобы они были доступны во всех ветках ниже
	// --- ИЗМЕНЕНИЕ: Используем = вместо := для присваивания уже объявленным переменным ---
	initialSceneIsValid = false
	sceneExists = false
	// --- УДАЛЯЕМ ЭТУ СТРОКУ (повторное объявление) ---
	// var initialSceneID uuid.UUID

	if setupIsValid { // Проверяем сцену только если Setup валиден
		scene, errScene := s.sceneRepo.FindByStoryAndHash(ctx, storyID, models.InitialStateHash)
		if errScene == nil && scene != nil {
			sceneExists = true
			initialSceneID = scene.ID
			if len(scene.Content) > 0 && json.Valid(scene.Content) {
				initialSceneIsValid = true
			}
		} else if !errors.Is(errScene, pgx.ErrNoRows) && !errors.Is(errScene, models.ErrNotFound) {
			log.Error("Failed to check for initial scene existence during retry", zap.Error(errScene))
			return models.ErrInternalServer // Критическая ошибка БД
		}
	}

	// Случай 1: Setup отсутствует ИЛИ был невалиден в статусе Error
	// (len(publishedStory.Setup) == 0 уже проверено выше при установке setupIsValid)
	if !setupIsValid {
		log.Info("Setup is missing or was invalid, retrying setup generation")

		// Проверяем наличие Config (необходим для генерации Setup)
		if len(publishedStory.Config) == 0 {
			log.Error("CRITICAL: Story Config is nil or empty, cannot retry Setup generation.")
			// Обновляем статус в Error с деталями
			errMsg := "Cannot retry Setup: Story Config is missing"
			if errUpdate := s.publishedRepo.UpdateStatusFlagsAndDetails(context.Background(), storyID, models.StatusError, false, false, &errMsg); errUpdate != nil {
				log.Error("Failed to update story status to Error after discovering missing config for setup retry", zap.Error(errUpdate))
			}
			return models.ErrInternalServer // Не можем продолжить
		}

		// Обновляем статус истории на SetupPending, сбрасываем флаги и детали ошибки
		log.Info("Updating story status to SetupPending before setup retry task publish")
		if errUpdate := s.publishedRepo.UpdateStatusFlagsAndDetails(ctx, storyID, models.StatusSetupPending, false, false, nil); errUpdate != nil {
			log.Error("Failed to update story status to SetupPending before setup retry task publish", zap.Error(errUpdate))
			return fmt.Errorf("failed to update story status for setup retry: %w", errUpdate)
		}

		// Создаем и публикуем задачу генерации Setup (PromptTypeNovelSetup)
		taskID := uuid.New().String()
		configJSONString := string(publishedStory.Config) // Используем полный конфиг
		setupPayload := sharedMessaging.GenerationTaskPayload{
			TaskID:           taskID,
			UserID:           publishedStory.UserID.String(), // Используем UserID из истории
			PromptType:       models.PromptTypeNovelSetup,
			UserInput:        configJSONString, // Отправляем Config как UserInput для Setup
			PublishedStoryID: publishedStory.ID.String(),
			Language:         publishedStory.Language,
			// GameStateID не нужен для Setup
		}

		log.Info("Publishing setup generation task (retry)", zap.String("taskID", taskID))
		if errPub := s.publisher.PublishGenerationTask(ctx, setupPayload); errPub != nil {
			log.Error("Error publishing retry setup generation task", zap.Error(errPub))
			// Пытаемся откатить статус обратно в Error?
			errMsg := fmt.Sprintf("Failed to publish setup retry task: %v", errPub)
			if errUpdate := s.publishedRepo.UpdateStatusFlagsAndDetails(context.Background(), storyID, models.StatusError, false, false, &errMsg); errUpdate != nil {
				log.Error("Failed to revert story status to Error after setup retry publish failure", zap.Error(errUpdate))
			}
			return fmt.Errorf("failed to publish setup generation task: %w", errPub)
		}

		log.Info("Retry setup generation task published successfully", zap.String("taskID", taskID))
		return nil // Успешно инициировали регенерацию setup

		// Старый код для случая len(publishedStory.Setup) == 0 (удален, т.к. логика объединена выше)
		/*
			log.Info("Setup is missing, retrying setup generation (narrator prompt)")
			log.Error("Cannot retry setup generation from published story context.")
			return fmt.Errorf("%w: setup data is missing, cannot regenerate from here", models.ErrCannotRetry)
		*/
	}

	// Случай 2: Setup есть и валиден, но начальная сцена отсутствует (или была невалидна/удалена)
	// --- ИЗМЕНЕНИЕ: Логика проверки сцены уже выполнена выше,
	// просто используем ранее определенные sceneExists и initialSceneIsValid ---

	if !sceneExists || !initialSceneIsValid {
		log.Info("Initial scene is missing or was invalid, retrying first scene generation")
		// Отмечаем историю как ожидающую первую сцену, сбрасываем ошибку
		// Убеждаемся, что AreImagesPending сохраняется
		if errUpdate := s.publishedRepo.UpdateStatusFlagsAndDetails(ctx, storyID, models.StatusFirstScenePending, true, publishedStory.AreImagesPending, nil); errUpdate != nil {
			log.Error("Failed to update story status to FirstScenePending before retry", zap.Error(errUpdate))
			return fmt.Errorf("failed to update story status: %w", errUpdate)
		}

		// Create and publish first scene generation task (without GameStateID)
		payload, errPayload := createInitialSceneGenerationPayload(userID, publishedStory)
		if errPayload != nil {
			log.Error("Failed to create initial scene generation payload for retry", zap.Error(errPayload))
			// Revert status?
			errMsg := fmt.Sprintf("Failed to create initial scene payload: %v", errPayload)
			if errUpdate := s.publishedRepo.UpdateStatusFlagsAndDetails(context.Background(), storyID, publishedStory.Status, false, publishedStory.AreImagesPending, &errMsg); errUpdate != nil {
				log.Error("Failed to revert IsFirstScenePending after payload creation error", zap.Error(errUpdate))
			}
			return fmt.Errorf("failed to create generation payload: %w", errPayload)
		}
		// Note: createInitialSceneGenerationPayload sets PromptTypeNovelCreator
		if errPub := s.publisher.PublishGenerationTask(ctx, payload); errPub != nil { // Correct publisher method
			log.Error("Failed to publish initial scene retry task", zap.Error(errPub))
			// Revert status?
			errMsg := fmt.Sprintf("Failed to publish initial scene task: %v", errPub)
			if errUpdate := s.publishedRepo.UpdateStatusFlagsAndDetails(context.Background(), storyID, publishedStory.Status, false, publishedStory.AreImagesPending, &errMsg); errUpdate != nil {
				log.Error("Failed to revert IsFirstScenePending after publish error", zap.Error(errUpdate))
			}
			return fmt.Errorf("failed to publish generation task: %w", errPub)
		}
		log.Info("Published initial scene retry task successfully", zap.String("taskID", payload.TaskID))
		return nil // Success
	}

	// 5. Both Setup and the Initial Scene TEXT exist. Check if images are pending.
	if publishedStory.AreImagesPending {
		log.Info("Initial scene text exists, but AreImagesPending is true. Retrying image generation (cover and/or characters).", zap.String("sceneID", initialSceneID.String()))

		// --- Reconstruct Cover Image Prompt --- START ---
		var setupContent models.NovelSetupContent

		if errUnmarshalSetup := json.Unmarshal(publishedStory.Setup, &setupContent); errUnmarshalSetup != nil {
			log.Error("Failed to unmarshal setup JSON to reconstruct cover prompt", zap.Error(errUnmarshalSetup))
			// Mark as error? Or just fail retry?
			return fmt.Errorf("failed to parse setup for cover image retry: %w", errUnmarshalSetup)
		}

		if setupContent.StoryPreviewImagePrompt == "" {
			log.Error("Cannot retry cover image generation: StoryPreviewImagePrompt (spi) is empty in setup", zap.String("storyID", storyID.String()))
			return fmt.Errorf("cannot retry cover image: StoryPreviewImagePrompt is missing in setup")
		}

		var fullConfig models.Config
		var characterVisualStyle string
		var storyStyle string
		var previewStyleSuffix string = "" // Default empty

		// Get styles from Config
		if errUnmarshalConfig := json.Unmarshal(publishedStory.Config, &fullConfig); errUnmarshalConfig != nil {
			log.Warn("Failed to unmarshal config JSON to get styles for cover prompt reconstruction", zap.Error(errUnmarshalConfig))
			// Proceed without styles if config parsing fails
		} else {
			characterVisualStyle = fullConfig.PlayerPrefs.CharacterVisualStyle
			storyStyle = fullConfig.PlayerPrefs.Style
			if characterVisualStyle != "" {
				characterVisualStyle = ", " + characterVisualStyle
			}
			if storyStyle != "" {
				storyStyle = ", " + storyStyle
			}
		}

		// Get suffix from Dynamic Config (similar to checkAndGenerateSetupImages)
		previewDynConfKey := "prompt.story_preview_style_suffix"
		dynamicConfigPreview, errConfPreview := s.dynamicConfigRepo.GetByKey(ctx, previewDynConfKey)
		if errConfPreview != nil {
			if !errors.Is(errConfPreview, models.ErrNotFound) {
				log.Error("Failed to get dynamic config for story preview style suffix, using empty default", zap.String("key", previewDynConfKey), zap.Error(errConfPreview))
			} // else: NotFound is fine, use default empty string
		} else if dynamicConfigPreview != nil && dynamicConfigPreview.Value != "" {
			previewStyleSuffix = dynamicConfigPreview.Value
			log.Info("Using dynamic config suffix for cover image retry prompt", zap.String("key", previewDynConfKey))
		}

		// Combine the prompt parts
		// TODO: Confirm if characterVisualStyle should be included for cover/preview images.
		reconstructedCoverPrompt := setupContent.StoryPreviewImagePrompt + storyStyle + characterVisualStyle + previewStyleSuffix
		log.Debug("Reconstructed cover image prompt", zap.String("prompt", reconstructedCoverPrompt))
		// --- Reconstruct Cover Image Prompt --- END ---

		// Mark story as images pending, clear error
		if errUpdate := s.publishedRepo.UpdateStatusFlagsAndDetails(ctx, storyID, publishedStory.Status, publishedStory.IsFirstScenePending, true, nil); errUpdate != nil { // Use AreImagesPending flag
			log.Error("Failed to update story AreImagesPending flag before image retry", zap.Error(errUpdate))
			return fmt.Errorf("failed to update story status for image retry: %w", errUpdate)
		}

		// Publish image generation task for the initial scene's cover image
		previewImageRef := fmt.Sprintf("history_preview_%s", storyID.String()) // Use the same reference as in checkAndGenerateSetupImages
		coverTaskPayload := sharedMessaging.CharacterImageTaskPayload{         // Use CharacterImageTaskPayload
			TaskID:           uuid.New().String(),
			UserID:           userID.String(),          // UserID needs to be string
			CharacterID:      storyID,                  // Use StoryID as CharacterID for preview?
			Prompt:           reconstructedCoverPrompt, // Use the reconstructed prompt
			NegativePrompt:   "",                       // TODO: Add negative prompt if available in setup/config?
			ImageReference:   previewImageRef,          // Use specific reference for cover
			Ratio:            "3:2",                    // Standard cover ratio?
			PublishedStoryID: storyID,
		}

		// Publish using the character image batch publisher
		coverBatchPayload := sharedMessaging.CharacterImageTaskBatchPayload{BatchID: uuid.New().String(), Tasks: []sharedMessaging.CharacterImageTaskPayload{coverTaskPayload}}
		if errPub := s.characterImageTaskBatchPub.PublishCharacterImageTaskBatch(ctx, coverBatchPayload); errPub != nil {
			log.Error("Failed to publish cover image retry task", zap.Error(errPub))
			// Attempt to revert AreImagesPending flag?
			errMsg := fmt.Sprintf("Failed to publish cover image task: %v", errPub)
			if errUpdate := s.publishedRepo.UpdateStatusFlagsAndDetails(context.Background(), storyID, publishedStory.Status, publishedStory.IsFirstScenePending, false, &errMsg); errUpdate != nil {
				log.Error("Failed to revert AreImagesPending flag after image publish error", zap.Error(errUpdate))
			}
			return fmt.Errorf("failed to publish image generation task: %w", errPub)
		}

		log.Info("Published cover image retry task successfully", zap.String("taskID", coverTaskPayload.TaskID))
		return nil // Success
	}

	// 6. Setup, Scene Text, and Cover Image all exist, and AreImagesPending is false. Nothing to retry initially.
	log.Warn("Setup, initial scene text, cover image exist, and no images are pending. Nothing to retry via initial retry endpoint.")
	return ErrCannotRetryInitial // Use a more specific error?
}
