package service

import (
	"context" // Может понадобиться для UserInput
	// <<< ДОБАВЛЕНО
	"errors"
	"fmt"

	// <<< ДОБАВЛЕНО
	// Может понадобиться для payload
	// Убедитесь, что он здесь
	// Убедитесь, что он здесь
	"novel-server/gameplay-service/internal/config"
	"novel-server/gameplay-service/internal/messaging"
	"novel-server/shared/database"
	interfaces "novel-server/shared/interfaces"
	sharedMessaging "novel-server/shared/messaging" // <<< ДОБАВЛЕН АЛИАС
	"novel-server/shared/models"
	"novel-server/shared/utils"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// DispatchNextGenerationTask determines the next AI generation task based on the story's current status
// and publishes it to the message queue. It operates within the provided transaction.
// It accepts optional context (gameState, progress, narrative) which might be needed for certain statuses.
// NOTE: This function is intended to be called AFTER the status has been updated to the relevant 'pending' state.
func (s *gameLoopServiceImpl) DispatchNextGenerationTask(
	ctx context.Context,
	tx interfaces.DBTX,
	storyID uuid.UUID,
	// Optional context, pass nil/empty if not available/relevant for the current status
	optionalGameState *models.PlayerGameState,
	optionalProgress *models.PlayerProgress,
	optionalNarrative string,
) error {
	log := s.logger.With(
		zap.String("storyID", storyID.String()),
		zap.String("operation", "DispatchNextGenerationTask"),
		zap.Bool("hasGameState", optionalGameState != nil),
		zap.Bool("hasProgress", optionalProgress != nil),
		zap.Bool("hasNarrative", optionalNarrative != ""),
	)
	log.Info("Attempting to dispatch next generation task with optional context")

	publishedRepoTx := database.NewPgPublishedStoryRepository(tx, s.logger)
	publishedStory, err := publishedRepoTx.GetByID(ctx, tx, storyID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, models.ErrNotFound) {
			log.Error("Published story not found for dispatching task", zap.Error(err))
			return nil
		}
		log.Error("Failed to get published story for dispatching task", zap.Error(err))
		return fmt.Errorf("failed to get story for dispatch: %w", err) // Internal error
	}

	log.Info("Current story status for dispatch", zap.String("status", string(publishedStory.Status)))

	switch publishedStory.Status {
	case models.StatusSetupPending:
		log.Info("Dispatching Setup task")
		if publishedStory.Config == nil {
			log.Error("Cannot dispatch Setup task: Config is nil")
			return fmt.Errorf("cannot dispatch setup task for story %s: config is nil", storyID)
		}
		var deserializedConfig models.Config
		if errUnmarshal := DecodeStrictJSON(publishedStory.Config, &deserializedConfig); errUnmarshal != nil {
			log.Error("Failed to unmarshal Config for Setup task payload creation", zap.Error(errUnmarshal))
			return fmt.Errorf("failed to unmarshal config for story %s: %w", storyID, errUnmarshal)
		}
		configUserInput := utils.FormatConfigToString(deserializedConfig, publishedStory.IsAdultContent)
		if configUserInput == "" {
			log.Error("Generated empty UserInput from Config for Setup task")
			return fmt.Errorf("generated empty user input for setup task for story %s", storyID)
		}
		taskID := uuid.New().String()
		payload := sharedMessaging.GenerationTaskPayload{
			TaskID:           taskID,
			UserID:           publishedStory.UserID.String(),
			PromptType:       models.PromptTypeStorySetup,
			UserInput:        configUserInput,
			PublishedStoryID: publishedStory.ID.String(),
			Language:         publishedStory.Language,
		}
		if errPub := s.publisher.PublishGenerationTask(ctx, payload); errPub != nil {
			log.Error("Error publishing setup generation task", zap.String("taskID", taskID), zap.Error(errPub))
		} else {
			log.Info("Successfully published setup generation task", zap.String("taskID", taskID))
		}
		return nil

	case models.StatusImageGenerationPending:
		log.Info("Dispatching Image Generation task(s)")
		if publishedStory.Setup == nil || string(publishedStory.Setup) == "null" || string(publishedStory.Setup) == "{}" {
			log.Error("Cannot dispatch Image Generation task: Setup is nil or empty")
			return fmt.Errorf("cannot dispatch image generation task for story %s: setup is missing or empty", storyID)
		}
		var setupContent models.NovelSetupContent
		if errUnmarshal := DecodeStrictJSON(publishedStory.Setup, &setupContent); errUnmarshal != nil {
			log.Error("Failed to unmarshal Setup for Image Generation task payload creation", zap.Error(errUnmarshal))
			return fmt.Errorf("failed to unmarshal setup for story %s: %w", storyID, errUnmarshal)
		}
		taskPublished := false
		if setupContent.StoryPreviewImagePrompt != "" {
			log.Info("Dispatching story preview image generation task")
			taskID := uuid.New().String()
			coverImagePayload := sharedMessaging.CharacterImageTaskPayload{
				TaskID:           taskID,
				UserID:           publishedStory.UserID.String(),
				PublishedStoryID: publishedStory.ID,
				Prompt:           setupContent.StoryPreviewImagePrompt,
				ImageReference:   fmt.Sprintf("history_preview_%s", publishedStory.ID.String()),
				CharacterID:      uuid.Nil,
				CharacterName:    "Story Cover",
				Ratio:            "3:2",
			}
			if errPub := s.imagePublisher.PublishCharacterImageTask(ctx, coverImagePayload); errPub != nil {
				log.Error("Error publishing story preview image generation task using imagePublisher", zap.String("taskID", taskID), zap.Error(errPub))
			} else {
				log.Info("Successfully published story preview image generation task using imagePublisher", zap.String("taskID", taskID))
				taskPublished = true
			}
		}
		characterTasks := []sharedMessaging.CharacterImageTaskPayload{}
		if len(setupContent.Characters) > 0 {
			log.Info("Preparing character image generation tasks", zap.Int("character_count", len(setupContent.Characters)))
			for _, char := range setupContent.Characters {
				if char.ImageReferenceName != "" && char.ImagePromptDescriptor != "" {
					charTaskID := uuid.New().String()
					charRatio := "2:3"
					var charID uuid.UUID
					payload := sharedMessaging.CharacterImageTaskPayload{
						TaskID:           charTaskID,
						UserID:           publishedStory.UserID.String(),
						PublishedStoryID: publishedStory.ID,
						CharacterID:      charID,
						CharacterName:    char.Name,
						ImageReference:   char.ImageReferenceName,
						Prompt:           char.ImagePromptDescriptor,
						Ratio:            charRatio,
					}
					characterTasks = append(characterTasks, payload)
				} else {
					log.Warn("Skipping character image generation due to missing reference name or prompt desc", zap.String("char_name", char.Name))
				}
			}
		}
		if len(characterTasks) > 0 {
			batchID := uuid.New().String()
			batchPayload := sharedMessaging.CharacterImageTaskBatchPayload{
				BatchID:          batchID,
				PublishedStoryID: publishedStory.ID,
				Tasks:            characterTasks,
			}
			if errPub := s.characterImageTaskBatchPub.PublishCharacterImageTaskBatch(ctx, batchPayload); errPub != nil {
				log.Error("Error publishing character image generation task batch", zap.String("batchID", batchID), zap.Error(errPub))
			} else {
				log.Info("Successfully published character image generation task batch", zap.String("batchID", batchID), zap.Int("task_count", len(characterTasks)))
				taskPublished = true
			}
		}
		if !taskPublished {
			log.Warn("No image generation tasks were dispatched (no preview prompt and no valid characters found)")
		}
		return nil

	case models.StatusJsonGenerationPending:
		log.Info("Dispatching JSON Generation task")

		// --- Use optionalNarrative and optionalGameState from context --- <<< CHANGED
		if optionalNarrative == "" {
			log.Error("Cannot dispatch JSON Generation task: Narrative text is missing from context")
			return fmt.Errorf("cannot dispatch json generation task for story %s: narrative text is missing", storyID)
		}
		var gameStateID string
		if optionalGameState != nil {
			gameStateID = optionalGameState.ID.String()
			log.Info("Using GameStateID from context for JSON task", zap.String("gameStateID", gameStateID))
		} else {
			// If GameState is not provided, can we proceed? The result needs linking.
			// Maybe the first scene's JSON doesn't need a GameStateID?
			// Or maybe the caller MUST provide it.
			log.Warn("Cannot dispatch JSON Generation task: GameState context is missing. Result cannot be linked.")
			// Returning error for now, as linking seems crucial.
			return fmt.Errorf("cannot dispatch json generation task for story %s: game state context missing", storyID)
		}
		// --- End Context Usage ---

		if publishedStory.Config == nil || publishedStory.Setup == nil {
			log.Error("Cannot dispatch JSON Generation task: Config or Setup is nil")
			return fmt.Errorf("cannot dispatch json generation task for story %s: config or setup is nil", storyID)
		}

		var deserializedConfig models.Config
		var deserializedSetup models.NovelSetupContent
		if err := DecodeStrictJSON(publishedStory.Config, &deserializedConfig); err != nil {
			log.Error("Failed to unmarshal Config for JSON task payload creation", zap.Error(err))
			return fmt.Errorf("failed to unmarshal config for json task (story %s): %w", storyID, err)
		}
		if err := DecodeStrictJSON(publishedStory.Setup, &deserializedSetup); err != nil {
			log.Error("Failed to unmarshal Setup for JSON task payload creation", zap.Error(err))
			return fmt.Errorf("failed to unmarshal setup for json task (story %s): %w", storyID, err)
		}

		// Добавляем десериализацию Setup в map[string]interface{} для получения цели
		var setupMap map[string]interface{}
		if err := DecodeStrictJSON(publishedStory.Setup, &setupMap); err != nil {
			log.Error("Failed to unmarshal Setup into map[string]interface{} for JSON task payload creation", zap.Error(err))
			// Можно решить, критична ли ошибка. Если цель необязательна, можно продолжить с nil map.
			// Пока считаем критичной, так как форматтер теперь ожидает setupMap.
			return fmt.Errorf("failed to unmarshal setup into map for json task (story %s): %w", storyID, err)
		}

		// Используем новую функцию форматирования
		jsonUserInput, errFormat := utils.FormatInputForJsonGeneration(deserializedConfig, deserializedSetup, setupMap, optionalNarrative)
		if errFormat != nil {
			log.Error("Failed to format UserInput for JSON generation task", zap.Error(errFormat))
			return fmt.Errorf("failed to format input for json task (story %s): %w", storyID, errFormat)
		}
		if jsonUserInput == "" {
			log.Error("Generated empty UserInput for JSON task")
			return fmt.Errorf("generated empty user input for json task for story %s", storyID)
		}

		taskID := uuid.New().String()
		payload := sharedMessaging.GenerationTaskPayload{
			TaskID:           taskID,
			UserID:           publishedStory.UserID.String(),
			PromptType:       models.PromptTypeJsonGeneration,
			UserInput:        jsonUserInput,
			PublishedStoryID: publishedStory.ID.String(),
			Language:         publishedStory.Language,
			GameStateID:      gameStateID, // <<< Use gameStateID from context
		}

		if errPub := s.publisher.PublishGenerationTask(ctx, payload); errPub != nil {
			log.Error("Error publishing json generation task", zap.String("taskID", taskID), zap.Error(errPub))
		} else {
			log.Info("Successfully published json generation task", zap.String("taskID", taskID))
		}
		return nil

	case models.StatusGenerating:
		log.Info("Dispatching Scene Continuation or Game Over task (triggered by status)")

		// --- Use optionalGameState and optionalProgress from context --- <<< CHANGED
		if optionalGameState == nil || optionalProgress == nil {
			log.Error("Cannot dispatch Scene/GameOver task: GameState or Progress context is missing")
			return fmt.Errorf("cannot dispatch scene/gameover task for story %s: context missing", storyID)
		}
		gameState := optionalGameState // Use context directly
		progress := optionalProgress
		// --- End Context Usage ---

		promptTypeToUse := models.PromptTypeStoryContinuation
		if !gameState.CurrentSceneID.Valid {
			promptTypeToUse = models.PromptTypeNovelGameOverCreator
			log.Info("Dispatching Game Over task based on GameState")
		} else {
			log.Info("Dispatching Scene Continuation task based on GameState")
		}

		madeChoicesInfo := []models.UserChoiceInfo{}

		generationPayload, errGenPayload := createGenerationPayload(
			gameState.PlayerID,
			publishedStory,
			progress,
			gameState,
			madeChoicesInfo,
			progress.CurrentStateHash,
			publishedStory.Language,
			promptTypeToUse,
		)

		if errGenPayload != nil {
			log.Error("Failed to create generation payload for scene/gameover dispatch", zap.Error(errGenPayload))
			return fmt.Errorf("failed to create payload for scene/gameover dispatch for story %s: %w", storyID, errGenPayload)
		}
		generationPayload.GameStateID = gameState.ID.String()

		if errPub := s.publisher.PublishGenerationTask(ctx, generationPayload); errPub != nil {
			log.Error("Error publishing scene/gameover generation task", zap.String("taskID", generationPayload.TaskID), zap.Error(errPub))
		} else {
			log.Info("Successfully published scene/gameover generation task", zap.String("taskID", generationPayload.TaskID), zap.String("type", string(promptTypeToUse)))
		}
		return nil

	case models.StatusReady, models.StatusError:
		log.Info("No dispatch needed for terminal or error status", zap.String("status", string(publishedStory.Status)))
		return nil

	default:
		log.Warn("Unhandled status for dispatching task", zap.String("status", string(publishedStory.Status)))
		return nil
	}

	// Placeholder: remove when TODOs are implemented (Should be unreachable now if all cases return)
	// log.Warn("Dispatch logic not fully implemented yet")
	// return nil
}

// Ensure gameLoopServiceImpl implements GameLoopService
var _ interfaces.GameLoopService = (*gameLoopServiceImpl)(nil)

// Восстановление структуры и конструктора GameLoopServiceImpl
type gameLoopServiceImpl struct {
	publishedRepo              interfaces.PublishedStoryRepository
	sceneRepo                  interfaces.StorySceneRepository
	playerProgressRepo         interfaces.PlayerProgressRepository
	playerGameStateRepo        interfaces.PlayerGameStateRepository
	publisher                  messaging.TaskPublisher
	storyConfigRepo            interfaces.StoryConfigRepository
	imageReferenceRepo         interfaces.ImageReferenceRepository
	characterImageTaskBatchPub messaging.CharacterImageTaskBatchPublisher
	dynamicConfigRepo          interfaces.DynamicConfigRepository
	clientPub                  messaging.ClientUpdatePublisher
	logger                     *zap.Logger
	cfg                        *config.Config
	pool                       *pgxpool.Pool
	imagePublisher             messaging.CharacterImageTaskPublisher
}

// NewGameLoopService creates a new instance of GameLoopService.
func NewGameLoopService(
	publishedRepo interfaces.PublishedStoryRepository,
	sceneRepo interfaces.StorySceneRepository,
	playerProgressRepo interfaces.PlayerProgressRepository,
	playerGameStateRepo interfaces.PlayerGameStateRepository,
	publisher messaging.TaskPublisher,
	storyConfigRepo interfaces.StoryConfigRepository,
	imageReferenceRepo interfaces.ImageReferenceRepository,
	characterImageTaskBatchPub messaging.CharacterImageTaskBatchPublisher,
	dynamicConfigRepo interfaces.DynamicConfigRepository,
	clientPub messaging.ClientUpdatePublisher,
	logger *zap.Logger,
	cfg *config.Config,
	pool *pgxpool.Pool,
	imagePublisher messaging.CharacterImageTaskPublisher,
) interfaces.GameLoopService {
	if cfg == nil {
		panic("cfg cannot be nil for NewGameLoopService")
	}
	return &gameLoopServiceImpl{
		publishedRepo:              publishedRepo,
		sceneRepo:                  sceneRepo,
		playerProgressRepo:         playerProgressRepo,
		playerGameStateRepo:        playerGameStateRepo,
		publisher:                  publisher,
		storyConfigRepo:            storyConfigRepo,
		imageReferenceRepo:         imageReferenceRepo,
		characterImageTaskBatchPub: characterImageTaskBatchPub,
		dynamicConfigRepo:          dynamicConfigRepo,
		clientPub:                  clientPub,
		logger:                     logger.Named("GameLoopService"),
		cfg:                        cfg,
		pool:                       pool,
		imagePublisher:             imagePublisher,
	}
}

// --- Helper Functions ---
