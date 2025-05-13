package service

import (
	"context"
	"encoding/json"
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
	if err != nil {
		if errors.Is(err, models.ErrNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return nil, models.ErrPlayerGameStateNotFound
		}
		return nil, models.ErrInternalServer
	}
	if gs.PlayerID != userID {
		return nil, models.ErrForbidden
	}
	if gs.PlayerStatus != models.PlayerStatusError && gs.PlayerStatus != models.PlayerStatusGeneratingScene {
		return nil, models.ErrCannotRetry
	}

	story, err := publishedRepo.GetByID(ctx, tx, gs.PublishedStoryID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, models.ErrNotFound) {
			return nil, models.ErrStoryNotFound
		}
		return nil, models.ErrInternalServer
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
			publishedRepo.UpdateStatusFlagsAndDetails(ctx, tx, story.ID, models.StatusSetupPending, false, false, nil, nil)
		}
		// Форматируем input
		var cfg models.Config
		_ = json.Unmarshal(story.Config, &cfg)
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
	if err != nil {
		return nil, models.ErrInternalServer
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

	var storyConfig models.Config
	var setupMap map[string]interface{}
	var initialNarrativeText string
	var promptType models.PromptType
	var userInput string
	var payloadToSend interface{}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		log.Error("Failed to begin transaction", zap.Error(err))
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			if rbErr := tx.Rollback(ctx); rbErr != nil {
				log.Error("Failed to rollback transaction", zap.Error(rbErr))
			}
			log.Error("RetryInitialGeneration failed", zap.Error(err))
		} else {
			if cmErr := tx.Commit(ctx); cmErr != nil {
				log.Error("Failed to commit transaction", zap.Error(cmErr))
				err = fmt.Errorf("failed to commit transaction: %w", cmErr)
			}
		}
	}()

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
	if err := json.Unmarshal(story.Config, &storyConfig); err != nil {
		log.Error("Failed to unmarshal story config JSON", zap.Error(err))
		return fmt.Errorf("failed to unmarshal config for story %s: %w", storyID, err)
	}

	stepPtr := story.InternalGenerationStep
	if stepPtr == nil {
		log.Error("InternalGenerationStep became nil unexpectedly")
		return fmt.Errorf("internal generation step is unexpectedly nil for story %s", storyID)
	}
	step := *stepPtr
	log.Info("Starting retry from step", zap.String("step", string(step)))

	// Получаем контент начальной сцены, если он может понадобиться
	var initialSceneContent *models.InitialSceneContent
	if step == models.StepCardImageGeneration || step == models.StepCharacterImageGeneration || step == models.StepInitialSceneJSON {
		sceneRepo := database.NewPgStorySceneRepository(tx, s.logger)
		initialScene, errScene := sceneRepo.FindByStoryAndHash(ctx, tx, storyID, models.InitialStateHash)
		if errScene != nil {
			if errors.Is(errScene, pgx.ErrNoRows) || errors.Is(errScene, models.ErrNotFound) {
				log.Error("Initial scene not found for retry step", zap.String("step", string(step)))
				return fmt.Errorf("initial scene not found for story %s retry", storyID)
			} else {
				log.Error("Failed to get initial scene for retry", zap.Error(errScene))
				return fmt.Errorf("failed to get initial scene for story %s retry: %w", storyID, errScene)
			}
		}
		if initialScene.Content != nil {
			tempContent := models.InitialSceneContent{}
			if errUnmarshal := json.Unmarshal(initialScene.Content, &tempContent); errUnmarshal != nil {
				log.Error("Failed to unmarshal initial scene content for retry", zap.Error(errUnmarshal))
				return fmt.Errorf("failed to unmarshal initial scene content for story %s retry: %w", storyID, errUnmarshal)
			} else {
				initialSceneContent = &tempContent
			}
		} else {
			log.Error("Initial scene content is nil for retry step", zap.String("step", string(step)))
			return fmt.Errorf("initial scene content is nil for story %s retry", storyID)
		}
	}

	if story.Setup != nil {
		if err := json.Unmarshal(story.Setup, &setupMap); err != nil {
			log.Error("Failed to unmarshal story setup JSON", zap.Error(err))
			return fmt.Errorf("failed to unmarshal setup for story %s: %w", storyID, err)
		}
	}
	var ok bool
	initialNarrativeText, ok = setupMap["initial_narrative"].(string)
	if !ok {
		initialNarrativeText = ""
	}
	isAdult := story.IsAdultContent

	switch step {
	case models.StepModeration:
		promptType = models.PromptTypeContentModeration
		var configForModeration models.Config
		if err := json.Unmarshal(story.Config, &configForModeration); err != nil {
			log.Error("Failed to unmarshal config for Moderation retry payload", zap.Error(err))
			return err // Critical unmarshal error
		}
		userInput, err = utils.FormatInputForModeration(configForModeration)
		if err != nil {
			log.Error("Failed to format input for Moderation retry", zap.Error(err))
			return err
		}
		payloadToSend = &sharedMessaging.GenerationTaskPayload{
			TaskID:           uuid.New().String(),
			UserID:           userID.String(),
			PublishedStoryID: storyID.String(),
			PromptType:       promptType,
			UserInput:        userInput,
			Language:         story.Language,
		}
		log.Info("Prepared payload for Moderation retry")

	case models.StepProtagonistGoal:
		promptType = models.PromptTypeProtagonistGoal
		var configForGoal models.Config
		if err := json.Unmarshal(story.Config, &configForGoal); err != nil {
			log.Error("Failed to unmarshal config for Goal retry payload", zap.Error(err))
			return err // Critical unmarshal error
		}
		// Используем новый форматтер
		userInput = utils.FormatConfigForGoalPrompt(configForGoal, story.IsAdultContent)
		log.Info("Prepared payload for ProtagonistGoal retry")
		payloadToSend = &sharedMessaging.GenerationTaskPayload{
			TaskID:           uuid.New().String(),
			UserID:           userID.String(),
			PublishedStoryID: storyID.String(),
			PromptType:       promptType,
			UserInput:        userInput,
			Language:         story.Language,
		}
		log.Info("Prepared payload for ProtagonistGoal retry")

	case models.StepScenePlanner:
		promptType = models.PromptTypeScenePlanner
		goal, ok := setupMap["protagonist_goal"].(string)
		if !ok || goal == "" {
			log.Error("Protagonist goal missing or invalid in setupMap for scene planner retry")
			return fmt.Errorf("protagonist_goal missing/invalid in setup for story %s", storyID)
		}
		userInput, err = utils.FormatConfigAndGoalForScenePlanner(storyConfig, goal, isAdult)
		if err != nil {
			log.Error("Failed to format input for scene planner", zap.Error(err))
			return err
		}
		payloadToSend = &sharedMessaging.GenerationTaskPayload{
			TaskID:           uuid.New().String(),
			UserID:           userID.String(),
			PublishedStoryID: storyID.String(),
			PromptType:       promptType,
			UserInput:        userInput,
			Language:         story.Language,
		}
		log.Info("Prepared payload for ScenePlanner retry")

	case models.StepCharacterGeneration:
		promptType = models.PromptTypeCharacterGeneration
		var configForCharGen models.Config
		if err := json.Unmarshal(story.Config, &configForCharGen); err != nil {
			log.Error("Failed to unmarshal config for CharacterGen retry payload", zap.Error(err))
			return err // Critical unmarshal error
		}
		userInput, err = utils.FormatInputForCharacterGen(configForCharGen, setupMap, isAdult)
		if err != nil {
			log.Error("Failed to format input for CharacterGen retry", zap.Error(err))
			return err
		}
		payloadToSend = &sharedMessaging.GenerationTaskPayload{
			TaskID:           uuid.New().String(),
			UserID:           userID.String(),
			PublishedStoryID: storyID.String(),
			PromptType:       promptType,
			UserInput:        userInput,
			Language:         story.Language,
		}
		log.Info("Prepared payload for CharacterGeneration retry")

	case models.StepSetupGeneration:
		promptType = models.PromptTypeStorySetup
		userInput, err = utils.FormatInputForSetupGen(storyConfig, setupMap, isAdult)
		if err != nil {
			log.Error("Failed to format input for setup generation", zap.Error(err))
			return err
		}
		payloadToSend = &sharedMessaging.GenerationTaskPayload{
			TaskID:           uuid.New().String(),
			UserID:           userID.String(),
			PublishedStoryID: storyID.String(),
			PromptType:       promptType,
			UserInput:        userInput,
			Language:         story.Language,
		}
		log.Info("Prepared payload for SetupGeneration retry")

	case models.StepInitialSceneJSON:
		promptType = models.PromptTypeJsonGeneration
		if initialNarrativeText == "" {
			log.Error("Initial narrative text is missing in setupMap for JSON generation retry")
			return fmt.Errorf("initial_narrative missing in setup for story %s", storyID)
		}
		// Добавляем десериализацию Setup в models.NovelSetupContent для получения списка персонажей
		var deserializedSetup models.NovelSetupContent
		if err := json.Unmarshal(story.Setup, &deserializedSetup); err != nil {
			log.Error("Failed to unmarshal Setup into NovelSetupContent for JSON task retry", zap.Error(err))
			return fmt.Errorf("failed to unmarshal setup into struct for json task retry (story %s): %w", storyID, err)
		}
		userInput, err = utils.FormatInputForJsonGeneration(storyConfig, deserializedSetup, setupMap, initialNarrativeText)
		if err != nil {
			log.Error("Failed to format input for initial JSON generation", zap.Error(err))
			return err
		}
		payloadToSend = &sharedMessaging.GenerationTaskPayload{
			TaskID:           uuid.New().String(),
			UserID:           userID.String(),
			PublishedStoryID: storyID.String(),
			PromptType:       promptType,
			UserInput:        userInput,
			Language:         story.Language,
		}
		log.Info("Prepared payload for InitialJsonGeneration retry")

	case models.StepCoverImageGeneration:
		promptType = models.PromptTypeStoryPreviewImage // Используем этот тип для обложки
		basePrompt, okPrompt := setupMap["story_preview_image_prompt"].(string)
		if !okPrompt || basePrompt == "" {
			log.Error("story_preview_image_prompt missing or empty in setupMap for CoverImage retry")
			return errors.New("cannot retry cover image generation: story_preview_image_prompt missing")
		}
		// TODO: Получить style и suffix, если они используются для обложки
		style := ""  // Пример
		suffix := "" // Пример
		userInput, err = utils.FormatInputForCoverImage(basePrompt, style, suffix)
		if err != nil {
			log.Error("Failed to format input for CoverImage retry", zap.Error(err))
			return err
		}
		payloadToSend = &sharedMessaging.CharacterImageTaskPayload{
			UserID:           userID.String(),
			PublishedStoryID: storyID,
			TaskID:           uuid.New().String(),
			Prompt:           userInput,
		}
		log.Info("Prepared payload for CoverImage retry")

	case models.StepCardImageGeneration:
		promptType = models.PromptTypeImageGeneration
		log.Warn("Retrying CardImages step - this will republish tasks for ALL initial card images")

		// Читаем карточки из InitialSceneContent
		if initialSceneContent == nil || len(initialSceneContent.Cards) == 0 {
			log.Warn("No initial cards found in InitialSceneContent, cannot retry card images")
			return nil // Ничего для ретрая
		}

		var publishErrors []error
		for i, card := range initialSceneContent.Cards { // <<< Используем initialSceneContent.Cards
			if card.ImagePromptDescriptor == "" || card.ImageReferenceName == "" {
				log.Warn("Skipping card image generation due to missing prompt/ref", zap.Int("cardIndex", i), zap.String("cardName", card.Title))
				continue
			}

			// Формируем payload. ID персонажа для карточки обычно Nil.
			cardPayload := sharedMessaging.CharacterImageTaskPayload{
				UserID:           userID.String(),
				PublishedStoryID: storyID,
				TaskID:           uuid.New().String(),
				Prompt:           card.ImagePromptDescriptor,
				CharacterName:    card.Title,
				ImageReference:   card.ImageReferenceName, // <<< Добавлено ImageReference
				CharacterID:      uuid.Nil,                // <<< ID персонажа Nil для карточек
				Ratio:            "3:2",                   // <<< Уточнить Ratio для карточек
			}

			if errPub := s.imagePublisher.PublishCharacterImageTask(ctx, cardPayload); errPub != nil {
				log.Error("Failed to publish card image task during retry", zap.Error(errPub), zap.Int("cardIndex", i), zap.String("cardName", card.Title))
				if publishErrors == nil {
					publishErrors = []error{}
				}
				publishErrors = append(publishErrors, errPub)
			} else {
				log.Info("Published card image task payload for retry", zap.Int("cardIndex", i), zap.String("cardName", card.Title))
			}
		}
		if len(publishErrors) > 0 {
			err = fmt.Errorf("encountered %d errors during card image task publishing retry: %v", len(publishErrors), publishErrors[0])
			return err // Stop processing and rollback if any card image task fails to publish
		}
		log.Info("Finished publishing all initial card image tasks for retry")
		payloadToSend = nil // No single payload, tasks published individually

	case models.StepCharacterImageGeneration:
		log.Warn("Retrying CharacterImages step - this will republish tasks for ALL generated characters")

		// Читаем персонажей из InitialSceneContent
		if initialSceneContent == nil || len(initialSceneContent.Characters) == 0 {
			log.Warn("No characters found in InitialSceneContent, cannot retry character images")
			return nil // Ничего для ретрая
		}

		var publishErrors []error
		for i, char := range initialSceneContent.Characters { // <<< Используем initialSceneContent.Characters
			if char.Prompt == "" || char.ImageRef == "" { // <<< Используем поля CharacterDefinition
				log.Warn("Skipping character image generation due to missing prompt/ref", zap.Int("charIndex", i), zap.String("charName", char.Name))
				continue
			}

			// Формируем payload, используя поля CharacterDefinition
			// ID персонажа не хранится в CharacterDefinition, его нужно будет найти иначе, если он нужен издателю.
			// Пока отправляем Nil UUID.
			charPayload := sharedMessaging.CharacterImageTaskPayload{
				TaskID:           uuid.New().String(),
				UserID:           story.UserID.String(),
				PublishedStoryID: storyID,
				CharacterID:      uuid.Nil, // <<< ID персонажа неизвестен из CharacterDefinition
				CharacterName:    char.Name,
				ImageReference:   char.ImageRef,
				Prompt:           char.Prompt,
				NegativePrompt:   "",    // Add if needed
				Ratio:            "2:3", // Or get from config/setup if variable
			}

			if errPub := s.imagePublisher.PublishCharacterImageTask(ctx, charPayload); errPub != nil {
				log.Error("Failed to publish character image task during retry", zap.Error(errPub), zap.Int("charIndex", i), zap.String("charName", char.Name))
				publishErrors = append(publishErrors, errPub)
			} else {
				log.Info("Published character image task payload for retry", zap.Int("charIndex", i), zap.String("charName", char.Name))
			}
		}
		if len(publishErrors) > 0 {
			// Combine errors or handle them as needed
			err = fmt.Errorf("encountered %d errors during character image task publishing retry: %v", len(publishErrors), publishErrors[0])
			return err // Stop processing and rollback if any character image task fails to publish
		}
		log.Info("Finished publishing all character image tasks for retry")
		payloadToSend = nil // No single payload, tasks published individually

	default:
		log.Error("Unsupported generation step for retry", zap.String("step", string(step)))
		return fmt.Errorf("unsupported step for initial generation retry: %s", string(step))
	}

	if payloadToSend != nil {
		switch p := payloadToSend.(type) {
		case *sharedMessaging.GenerationTaskPayload:
			err = s.publisher.PublishGenerationTask(ctx, *p)
			if err != nil {
				log.Error("Failed to publish generation task", zap.Error(err), zap.String("promptType", string(p.PromptType)))
				return fmt.Errorf("failed to publish generation task (%s): %w", string(p.PromptType), err)
			}
			log.Info("Successfully published generation task for retry", zap.String("promptType", string(p.PromptType)))
		case *sharedMessaging.CharacterImageTaskPayload:
			if errPub := s.imagePublisher.PublishCharacterImageTask(ctx, *p); errPub != nil {
				log.Error("Failed to publish cover image task during retry", zap.Error(errPub))
				return fmt.Errorf("failed to publish cover image task: %w", errPub)
			} else {
				log.Info("Successfully published cover image task for retry")
			}
		default:
			log.Error("Unknown payload type prepared for retry")
			return fmt.Errorf("internal error: unknown payload type for retry")
		}
	}

	if story.Status == models.StatusError {
		err = s.publishedRepo.UpdateStatusAndError(ctx, tx, storyID, models.StatusGenerating, nil)
		if err != nil {
			log.Error("Failed to update story status to Generating", zap.Error(err))
			return fmt.Errorf("failed to update story status to Generating: %w", err)
		}
		log.Info("Updated story status from Error to Generating")
	}

	log.Info("RetryInitialGeneration completed successfully")
	return nil
}
