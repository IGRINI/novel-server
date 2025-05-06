package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"novel-server/gameplay-service/internal/messaging"
	interfaces "novel-server/shared/interfaces"
	sharedMessaging "novel-server/shared/messaging"
	"novel-server/shared/models"

	"novel-server/gameplay-service/internal/config"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

type GameLoopService interface {
	// GetStoryScene retrieves the scene associated with a specific game state ID.
	GetStoryScene(ctx context.Context, userID uuid.UUID, gameStateID uuid.UUID) (*models.StoryScene, error)

	// MakeChoice applies player choices to a specific game state.
	MakeChoice(ctx context.Context, userID uuid.UUID, gameStateID uuid.UUID, selectedOptionIndices []int) error

	// ListGameStates lists all active game states (save slots) for a player and a story.
	ListGameStates(ctx context.Context, playerID uuid.UUID, publishedStoryID uuid.UUID) ([]*models.PlayerGameState, error)

	// CreateNewGameState creates a new save slot (game state) for a player and a story.
	// Returns an error if the player exceeds their save slot limit (TODO: implement limit check).
	CreateNewGameState(ctx context.Context, playerID uuid.UUID, publishedStoryID uuid.UUID) (*models.PlayerGameState, error)

	// DeletePlayerGameState deletes a specific game state (save slot) by its ID.
	DeletePlayerGameState(ctx context.Context, userID uuid.UUID, gameStateID uuid.UUID) error

	// RetryGenerationForGameState handles retrying generation for a specific game state.
	// It determines if setup or scene generation failed and triggers the appropriate task.
	RetryGenerationForGameState(ctx context.Context, userID, storyID, gameStateID uuid.UUID) error

	// UpdateSceneInternal updates the content of a specific scene (internal admin func).
	UpdateSceneInternal(ctx context.Context, sceneID uuid.UUID, contentJSON string) error

	// GetPlayerProgress retrieves the progress node linked to a specific game state ID.
	GetPlayerProgress(ctx context.Context, userID uuid.UUID, gameStateID uuid.UUID) (*models.PlayerProgress, error)

	// DeleteSceneInternal deletes a scene (internal admin func).
	DeleteSceneInternal(ctx context.Context, sceneID uuid.UUID) error

	// UpdatePlayerProgressInternal updates a specific progress node (internal func).
	UpdatePlayerProgressInternal(ctx context.Context, progressID uuid.UUID, progressData map[string]interface{}) error

	// RetryInitialGeneration handles retrying generation for a published story's Setup or Initial Scene.
	RetryInitialGeneration(ctx context.Context, userID, storyID uuid.UUID) error
}

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
) GameLoopService {
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
	}
}

// --- Helper Functions ---

// checkAndGenerateSetupImages parses the setup, checks image needs, updates flags, and publishes tasks.
// It needs access to logger and repositories, so it must be a method on gameLoopServiceImpl.
func (s *gameLoopServiceImpl) checkAndGenerateSetupImages(ctx context.Context, story *models.PublishedStory, setupBytes []byte, userID uuid.UUID) (bool, error) {
	log := s.logger.With(zap.String("publishedStoryID", story.ID.String()))

	var setupContent models.NovelSetupContent
	if err := json.Unmarshal(setupBytes, &setupContent); err != nil {
		log.Error("Failed to unmarshal setup JSON in checkAndGenerateSetupImages", zap.Error(err))
		return false, fmt.Errorf("failed to unmarshal setup JSON: %w", err)
	}

	var fullConfig models.Config
	var characterVisualStyle string
	var storyStyle string
	var characterStyleSuffix string = ""
	var previewStyleSuffix string = ""

	charDynConfKey := "prompt.character_style_suffix"
	dynamicConfigChar, errConfChar := s.dynamicConfigRepo.GetByKey(ctx, s.pool, charDynConfKey)
	if errConfChar != nil {
		if !errors.Is(errConfChar, models.ErrNotFound) {
			log.Error("Failed to get dynamic config for character style suffix, using empty default", zap.String("key", charDynConfKey), zap.Error(errConfChar))
		} else {
			log.Info("Dynamic config for character style suffix not found, using empty default", zap.String("key", charDynConfKey))
		}
	} else if dynamicConfigChar != nil && dynamicConfigChar.Value != "" {
		characterStyleSuffix = dynamicConfigChar.Value
		log.Info("Using dynamic config for character style suffix", zap.String("key", charDynConfKey))
	}

	previewDynConfKey := "prompt.story_preview_style_suffix"
	dynamicConfigPreview, errConfPreview := s.dynamicConfigRepo.GetByKey(ctx, s.pool, previewDynConfKey)
	if errConfPreview != nil {
		if !errors.Is(errConfPreview, models.ErrNotFound) {
			log.Error("Failed to get dynamic config for story preview style suffix, using empty default", zap.String("key", previewDynConfKey), zap.Error(errConfPreview))
		} else {
			log.Info("Dynamic config for story preview style suffix not found, using empty default", zap.String("key", previewDynConfKey))
		}
	} else if dynamicConfigPreview != nil && dynamicConfigPreview.Value != "" {
		previewStyleSuffix = dynamicConfigPreview.Value
		log.Info("Using dynamic config for story preview style suffix", zap.String("key", previewDynConfKey))
	}

	if errCfg := json.Unmarshal(story.Config, &fullConfig); errCfg != nil {
		log.Warn("Failed to unmarshal config JSON to get styles in checkAndGenerateSetupImages", zap.Error(errCfg))
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

	needsCharacterImages := false
	needsPreviewImage := false
	imageTasks := make([]sharedMessaging.CharacterImageTaskPayload, 0, len(setupContent.Characters))

	log.Info("Checking which images need generation (from checkAndGenerateSetupImages)")

	characterRefsToCheck := make([]string, 0, len(setupContent.Characters))
	refToCharDataMap := make(map[string]models.CharacterDefinition)
	for _, charData := range setupContent.Characters {
		if charData.ImageRef == "" || charData.Prompt == "" {
			continue
		}
		originalRef := charData.ImageRef
		correctedRef := originalRef
		if !strings.HasPrefix(originalRef, "ch_") {
			if strings.HasPrefix(originalRef, "character_") {
				correctedRef = strings.TrimPrefix(originalRef, "character_")
			} else if strings.HasPrefix(originalRef, "char_") {
				correctedRef = strings.TrimPrefix(originalRef, "char_")
			} else {
				correctedRef = originalRef
			}
			correctedRef = "ch_" + strings.TrimPrefix(correctedRef, "ch_")
		}
		characterRefsToCheck = append(characterRefsToCheck, correctedRef)
		refToCharDataMap[correctedRef] = charData
	}

	existingURLs := make(map[string]string)
	var errCheckBatch error
	if len(characterRefsToCheck) > 0 {
		existingURLs, errCheckBatch = s.imageReferenceRepo.GetImageURLsByReferences(ctx, characterRefsToCheck)
		if errCheckBatch != nil {
			log.Error("Error checking character ImageRefs in DB (batch)", zap.Error(errCheckBatch))
		}
	}

	for _, ref := range characterRefsToCheck {
		if _, exists := existingURLs[ref]; !exists {
			log.Debug("Character image needs generation (checked via batch)", zap.String("image_ref", ref))
			needsCharacterImages = true
			charData := refToCharDataMap[ref]
			characterIDForTask := uuid.New()

			fullCharacterPrompt := charData.Prompt + characterVisualStyle + characterStyleSuffix
			imageTask := sharedMessaging.CharacterImageTaskPayload{
				TaskID:           characterIDForTask.String(),
				UserID:           userID.String(),
				CharacterID:      characterIDForTask,
				Prompt:           fullCharacterPrompt,
				NegativePrompt:   charData.NegPrompt,
				ImageReference:   ref,
				Ratio:            "2:3",
				PublishedStoryID: story.ID,
			}
			imageTasks = append(imageTasks, imageTask)
		} else {
			log.Debug("Character image already exists (checked via batch)", zap.String("image_ref", ref))
		}
	}

	areImagesPending := needsPreviewImage || needsCharacterImages

	if areImagesPending {
		log.Info("Updating story flags: are_images_pending=true")

		if err := s.publishedRepo.UpdateStatusFlagsAndDetails(
			ctx,
			s.pool,
			story.ID,
			story.Status,
			story.IsFirstScenePending,
			areImagesPending,
			nil,
		); err != nil {
			log.Error("CRITICAL ERROR: Failed to update are_images_pending flag for PublishedStory", zap.Error(err))

			return false, fmt.Errorf("failed to update are_images_pending flag for story %s: %w", story.ID, err)
		}

		log.Info("Publishing image generation tasks",
			zap.Bool("preview_needed", needsPreviewImage),
			zap.Int("character_images_needed", len(imageTasks)),
		)
		if len(imageTasks) > 0 {
			batchPayload := sharedMessaging.CharacterImageTaskBatchPayload{BatchID: uuid.New().String(), Tasks: imageTasks}
			if errPub := s.characterImageTaskBatchPub.PublishCharacterImageTaskBatch(ctx, batchPayload); errPub != nil {
				log.Error("Failed to publish character image task batch", zap.Error(errPub), zap.String("batch_id", batchPayload.BatchID))
			} else {
				log.Info("Character image task batch published successfully", zap.String("batch_id", batchPayload.BatchID))
			}
		}
		if needsPreviewImage {
			previewImageRef := fmt.Sprintf("history_preview_%s", story.ID.String())
			basePreviewPrompt := setupContent.StoryPreviewImagePrompt

			fullPreviewPromptWithStyles := basePreviewPrompt + storyStyle + characterVisualStyle + previewStyleSuffix
			previewTask := sharedMessaging.CharacterImageTaskPayload{
				TaskID:           uuid.New().String(),
				UserID:           userID.String(),
				CharacterID:      story.ID,
				Prompt:           fullPreviewPromptWithStyles,
				NegativePrompt:   "",
				ImageReference:   previewImageRef,
				Ratio:            "3:2",
				PublishedStoryID: story.ID,
			}
			previewBatchPayload := sharedMessaging.CharacterImageTaskBatchPayload{BatchID: uuid.New().String(), Tasks: []sharedMessaging.CharacterImageTaskPayload{previewTask}}
			if errPub := s.characterImageTaskBatchPub.PublishCharacterImageTaskBatch(ctx, previewBatchPayload); errPub != nil {
				log.Error("Failed to publish story preview image task", zap.Error(errPub), zap.String("preview_batch_id", previewBatchPayload.BatchID))
			} else {
				log.Info("Story preview image task published successfully", zap.String("preview_batch_id", previewBatchPayload.BatchID))
			}
		}
	} else {
		log.Info("No image generation needed based on Setup and existing references.")
	}

	return areImagesPending, nil
}
