package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"novel-server/gameplay-service/internal/messaging"
	interfaces "novel-server/shared/interfaces"
	sharedMessaging "novel-server/shared/messaging"
	sharedModels "novel-server/shared/models"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// --- Structs specific to Game Loop logic ---

// sceneContentChoices represents the expected structure for scene content of type "choices".
type sceneContentChoices struct {
	Type    string        `json:"type"` // Should be "choices"
	Choices []sceneChoice `json:"ch"`
}

// sceneChoice represents a block of choices within a scene.
type sceneChoice struct {
	Shuffleable int           `json:"sh"` // 0 or 1
	Description string        `json:"desc"`
	Options     []sceneOption `json:"opts"` // Expecting exactly 2 options
}

// sceneOption represents a single option within a choice block.
type sceneOption struct {
	Text         string                    `json:"txt"`
	Consequences sharedModels.Consequences `json:"cons"`
}

// --- GameLoopService Interface and Implementation ---

// GameLoopService defines the interface for core gameplay interactions.
type GameLoopService interface {
	GetStoryScene(ctx context.Context, playerID uuid.UUID, publishedStoryID uuid.UUID) (*sharedModels.StoryScene, error)
	MakeChoice(ctx context.Context, playerID uuid.UUID, publishedStoryID uuid.UUID, selectedOptionIndices []int) error
	DeletePlayerGameState(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) error
	RetrySceneGeneration(ctx context.Context, storyID, userID uuid.UUID) error
	UpdateSceneInternal(ctx context.Context, sceneID uuid.UUID, contentJSON string) error
	GetOrCreatePlayerGameState(ctx context.Context, playerID, storyID uuid.UUID) (*sharedModels.PlayerGameState, error)
	RetryStoryGeneration(ctx context.Context, storyID, userID uuid.UUID) error
	// GetPlayerProgress retrieves the current progress node associated with the player's game state.
	GetPlayerProgress(ctx context.Context, userID, storyID uuid.UUID) (*sharedModels.PlayerProgress, error)

	// <<< ДОБАВЛЕНО: Метод для удаления сцены админкой >>>
	DeleteSceneInternal(ctx context.Context, sceneID uuid.UUID) error
	// <<< ДОБАВЛЕНО: Сигнатура для обновления прогресса игрока (внутренний метод) >>>
	UpdatePlayerProgressInternal(ctx context.Context, progressID uuid.UUID, progressData map[string]interface{}) error
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
	logger                     *zap.Logger
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
	logger *zap.Logger,
) GameLoopService {
	return &gameLoopServiceImpl{
		publishedRepo:              publishedRepo,
		sceneRepo:                  sceneRepo,
		playerProgressRepo:         playerProgressRepo,
		playerGameStateRepo:        playerGameStateRepo,
		publisher:                  publisher,
		storyConfigRepo:            storyConfigRepo,
		imageReferenceRepo:         imageReferenceRepo,
		characterImageTaskBatchPub: characterImageTaskBatchPub,
		logger:                     logger.Named("GameLoopService"),
	}
}

// GetStoryScene gets the current scene for the player based on their game state.
func (s *gameLoopServiceImpl) GetStoryScene(ctx context.Context, playerID uuid.UUID, publishedStoryID uuid.UUID) (*sharedModels.StoryScene, error) {
	log := s.logger.With(zap.String("playerID", playerID.String()), zap.String("publishedStoryID", publishedStoryID.String()))
	log.Info("GetStoryScene called")

	// 1. Get or Create Player Game State
	gameState, errState := s.GetOrCreatePlayerGameState(ctx, playerID, publishedStoryID)
	if errState != nil {
		// Errors already logged by GetOrCreatePlayerGameState
		// Map known errors to shared errors
		if errors.Is(errState, sharedModels.ErrStoryNotFound) || errors.Is(errState, sharedModels.ErrStoryNotReady) {
			return nil, errState // Return specific error
		}
		return nil, sharedModels.ErrInternalServer // Default to internal error
	}

	// 2. Check Player Status from Game State
	switch gameState.PlayerStatus {
	case sharedModels.PlayerStatusGeneratingScene:
		log.Info("Player is waiting for scene generation")
		return nil, sharedModels.ErrSceneNeedsGeneration
	case sharedModels.PlayerStatusGameOverPending:
		log.Info("Player is waiting for game over generation")
		return nil, sharedModels.ErrGameOverPending
	case sharedModels.PlayerStatusCompleted:
		log.Info("Player has completed the game")
		return nil, sharedModels.ErrGameCompleted
	case sharedModels.PlayerStatusError:
		log.Error("Player game state is in error", zap.Stringp("errorDetails", gameState.ErrorDetails))
		return nil, sharedModels.ErrPlayerStateInError
	case sharedModels.PlayerStatusPlaying:
		// Continue to fetch the scene
		log.Debug("Player status is Playing, fetching current scene")
	default:
		log.Error("Unknown player status in game state", zap.String("status", string(gameState.PlayerStatus)))
		return nil, sharedModels.ErrInternalServer
	}

	// 3. Status is Playing, get the Current Scene ID
	if gameState.CurrentSceneID == nil {
		log.Error("CRITICAL: PlayerStatus is Playing, but CurrentSceneID is nil", zap.String("gameStateID", gameState.ID.String()))
		// This indicates an inconsistent state. Maybe the initial scene wasn't found?
		// Or the scene link wasn't updated after generation.
		return nil, sharedModels.ErrSceneNotFound // Or ErrInternalServer?
	}

	// 4. Fetch the scene by its ID
	scene, errScene := s.sceneRepo.GetByID(ctx, *gameState.CurrentSceneID)
	if errScene != nil {
		if errors.Is(errScene, pgx.ErrNoRows) || errors.Is(errScene, sharedModels.ErrNotFound) {
			log.Error("CRITICAL: CurrentSceneID from game state not found in scene repository", zap.String("sceneID", gameState.CurrentSceneID.String()))
			return nil, sharedModels.ErrSceneNotFound // Scene linked in state doesn't exist
		}
		log.Error("Error getting scene by ID from repository", zap.String("sceneID", gameState.CurrentSceneID.String()), zap.Error(errScene))
		return nil, sharedModels.ErrInternalServer
	}

	log.Info("Scene found and returned", zap.String("sceneID", scene.ID.String()))
	return scene, nil
}

// MakeChoice handles player choice, updates game state, and triggers next scene/game over generation.
func (s *gameLoopServiceImpl) MakeChoice(ctx context.Context, playerID uuid.UUID, publishedStoryID uuid.UUID, selectedOptionIndices []int) error {
	logFields := []zap.Field{
		zap.String("playerID", playerID.String()),
		zap.String("publishedStoryID", publishedStoryID.String()),
		zap.Any("selectedOptionIndices", selectedOptionIndices),
	}
	s.logger.Info("MakeChoice called", logFields...)

	// 1. Get or Create Player Game State
	gameState, errState := s.GetOrCreatePlayerGameState(ctx, playerID, publishedStoryID)
	if errState != nil {
		if errors.Is(errState, sharedModels.ErrStoryNotFound) || errors.Is(errState, sharedModels.ErrStoryNotReady) {
			return errState
		}
		return sharedModels.ErrInternalServer
	}

	// 2. Check Player Status
	if gameState.PlayerStatus != sharedModels.PlayerStatusPlaying {
		s.logger.Warn("Attempt to make choice while not in Playing status", append(logFields, zap.String("playerStatus", string(gameState.PlayerStatus)))...)
		if gameState.PlayerStatus == sharedModels.PlayerStatusGeneratingScene {
			return sharedModels.ErrSceneNeedsGeneration
		}
		return sharedModels.ErrBadRequest
	}

	// 3. Check necessary IDs in GameState
	if gameState.PlayerProgressID == nil {
		s.logger.Error("CRITICAL: PlayerStatus is Playing, but PlayerProgressID is nil", append(logFields, zap.String("gameStateID", gameState.ID.String()))...)
		return sharedModels.ErrInternalServer
	}
	if gameState.CurrentSceneID == nil {
		s.logger.Error("CRITICAL: PlayerStatus is Playing, but CurrentSceneID is nil", append(logFields, zap.String("gameStateID", gameState.ID.String()))...)
		return sharedModels.ErrSceneNotFound
	}

	currentProgressID := *gameState.PlayerProgressID
	currentSceneID := *gameState.CurrentSceneID
	logFields = append(logFields, zap.String("currentProgressID", currentProgressID.String()), zap.String("currentSceneID", currentSceneID.String()))

	// 4. Get current PlayerProgress node
	currentProgress, errProgress := s.playerProgressRepo.GetByID(ctx, currentProgressID)
	if errProgress != nil {
		s.logger.Error("Failed to get current PlayerProgress node linked in game state", append(logFields, zap.Error(errProgress))...)
		return sharedModels.ErrInternalServer
	}

	// 5. Get the current Scene
	currentScene, errScene := s.sceneRepo.GetByID(ctx, currentSceneID)
	if errScene != nil {
		s.logger.Error("Failed to get current Scene linked in game state", append(logFields, zap.Error(errScene))...)
		return sharedModels.ErrInternalServer
	}

	// 6. Get the Published Story (needed for Setup)
	publishedStory, errStory := s.publishedRepo.GetByID(ctx, publishedStoryID)
	if errStory != nil {
		s.logger.Error("Failed to get published story associated with game state", append(logFields, zap.Error(errStory))...)
		return sharedModels.ErrInternalServer
	}

	// 7. Parse scene content and validate choice
	var sceneData sceneContentChoices
	if err := json.Unmarshal(currentScene.Content, &sceneData); err != nil || sceneData.Type != "choices" || len(sceneData.Choices) == 0 {
		s.logger.Error("Failed to unmarshal current scene content or invalid type/choices array", append(logFields, zap.Error(err))...)
		return sharedModels.ErrInternalServer
	}
	if len(sceneData.Choices) != len(selectedOptionIndices) {
		s.logger.Warn("Mismatch between number of choices in scene and choices made", append(logFields, zap.Int("sceneChoices", len(sceneData.Choices)), zap.Int("playerChoices", len(selectedOptionIndices)))...)
		return fmt.Errorf("%w: expected %d choice indices, got %d", sharedModels.ErrBadRequest, len(sceneData.Choices), len(selectedOptionIndices))
	}

	// 8. Load NovelSetup from PublishedStory
	if publishedStory.Setup == nil {
		s.logger.Error("CRITICAL: PublishedStory Setup is nil", logFields...)
		return sharedModels.ErrInternalServer
	}
	var setupContent sharedModels.NovelSetupContent
	if err := json.Unmarshal(publishedStory.Setup, &setupContent); err != nil {
		s.logger.Error("Failed to unmarshal NovelSetup content", append(logFields, zap.Error(err))...)
		return sharedModels.ErrInternalServer
	}

	// 9. Create a *copy* of the current progress to calculate the next state
	nextProgress := &sharedModels.PlayerProgress{
		UserID:           currentProgress.UserID,
		PublishedStoryID: currentProgress.PublishedStoryID,
		CoreStats:        make(map[string]int),
		StoryVariables:   make(map[string]interface{}),
		GlobalFlags:      make([]string, 0, len(currentProgress.GlobalFlags)),
		SceneIndex:       currentProgress.SceneIndex + 1,
	}
	for k, v := range currentProgress.CoreStats {
		nextProgress.CoreStats[k] = v
	}
	for k, v := range currentProgress.StoryVariables {
		nextProgress.StoryVariables[k] = v
	}
	nextProgress.GlobalFlags = append(nextProgress.GlobalFlags, currentProgress.GlobalFlags...)

	// 10. Apply consequences
	var isGameOver bool
	var gameOverStat string
	madeChoicesInfo := make([]sharedModels.UserChoiceInfo, 0, len(sceneData.Choices))
	for i, choiceBlock := range sceneData.Choices {
		selectedIndex := selectedOptionIndices[i]
		if selectedIndex < 0 || selectedIndex >= len(choiceBlock.Options) {
			s.logger.Warn("Invalid selected option index", append(logFields, zap.Int("choiceIndex", i), zap.Int("selectedIndex", selectedIndex))...)
			return fmt.Errorf("%w: invalid index %d for choice block %d", sharedModels.ErrInvalidChoice, selectedIndex, i)
		}
		selectedOption := choiceBlock.Options[selectedIndex]
		madeChoicesInfo = append(madeChoicesInfo, sharedModels.UserChoiceInfo{Desc: choiceBlock.Description, Text: selectedOption.Text})
		statCausingGameOver, gameOverTriggered := applyConsequences(nextProgress, selectedOption.Consequences, &setupContent)
		if gameOverTriggered {
			isGameOver = true
			gameOverStat = statCausingGameOver
			s.logger.Info("Game Over condition met", append(logFields, zap.String("gameOverStat", gameOverStat))...)
			break
		}
	}

	// 11. Handle Game Over
	if isGameOver {
		s.logger.Info("Handling Game Over state update")
		finalStateHash, hashErr := calculateStateHash(currentProgress.CurrentStateHash, nextProgress.CoreStats, nextProgress.StoryVariables, nextProgress.GlobalFlags)
		if hashErr != nil {
			s.logger.Error("Failed to calculate final state hash before game over", append(logFields, zap.Error(hashErr))...)
			return sharedModels.ErrInternalServer
		}
		nextProgress.CurrentStateHash = finalStateHash

		existingFinalNode, errFind := s.playerProgressRepo.GetByStoryIDAndHash(ctx, publishedStoryID, finalStateHash)
		var finalProgressNodeID uuid.UUID

		if errFind == nil {
			finalProgressNodeID = existingFinalNode.ID
			s.logger.Debug("Final progress node before game over already exists", append(logFields, zap.String("progressID", finalProgressNodeID.String()))...)
		} else if errors.Is(errFind, sharedModels.ErrNotFound) || errors.Is(errFind, pgx.ErrNoRows) {
			nextProgress.StoryVariables = make(map[string]interface{})
			nextProgress.GlobalFlags = clearTransientFlags(nextProgress.GlobalFlags)
			savedID, errSave := s.playerProgressRepo.Save(ctx, nextProgress)
			if errSave != nil {
				s.logger.Error("Failed to save final player progress node before game over", append(logFields, zap.Error(errSave))...)
				return sharedModels.ErrInternalServer
			}
			finalProgressNodeID = savedID
			s.logger.Info("Saved new final PlayerProgress node before game over", append(logFields, zap.String("progressID", finalProgressNodeID.String()))...)
		} else {
			s.logger.Error("Error checking for existing final progress node before game over", append(logFields, zap.Error(errFind))...)
			return sharedModels.ErrInternalServer
		}

		// Update PlayerGameState
		now := time.Now().UTC()
		gameState.PlayerStatus = sharedModels.PlayerStatusGameOverPending
		gameState.PlayerProgressID = &finalProgressNodeID
		gameState.CurrentSceneID = nil
		gameState.LastActivityAt = now

		if _, err := s.playerGameStateRepo.Save(ctx, gameState); err != nil {
			s.logger.Error("Failed to update PlayerGameState to GameOverPending", append(logFields, zap.Error(err))...)
			return sharedModels.ErrInternalServer
		}

		// Publish Game Over Task
		taskID := uuid.New().String()
		reasonCondition := ""
		finalValue := nextProgress.CoreStats[gameOverStat]
		if def, ok := setupContent.CoreStatsDefinition[gameOverStat]; ok {
			// Determine condition based on which limit was hit
			if def.GameOverConditions.Min && finalValue <= 0 { // Assuming 0 is the min boundary
				reasonCondition = "min"
			} else if def.GameOverConditions.Max && finalValue >= 100 { // Assuming 100 is the max boundary
				reasonCondition = "max"
			}
		}
		reason := sharedMessaging.GameOverReason{StatName: gameOverStat, Condition: reasonCondition, Value: finalValue}

		// --- MODIFICATION START: Use Minimal Config/Setup ---
		minimalGameOverConfig := sharedModels.ToMinimalConfigForGameOver(publishedStory.Config)
		minimalGameOverSetup := sharedModels.ToMinimalSetupForGameOver(&setupContent) // Pass parsed setup
		// --- MODIFICATION END ---

		lastStateProgress := *nextProgress         // Create a copy
		lastStateProgress.ID = finalProgressNodeID // Assign the correct ID

		gameOverPayload := sharedMessaging.GameOverTaskPayload{
			TaskID:           taskID,
			UserID:           playerID.String(),         // Use string UUID
			PublishedStoryID: publishedStoryID.String(), // Use string UUID
			GameStateID:      gameState.ID.String(),     // Use string UUID
			LastState:        lastStateProgress,         // Pass the final PlayerProgress node
			Reason:           reason,
			NovelConfig:      minimalGameOverConfig, // Use minimal config
			NovelSetup:       minimalGameOverSetup,  // Use minimal setup
		}
		if err := s.publisher.PublishGameOverTask(ctx, gameOverPayload); err != nil {
			s.logger.Error("Failed to publish game over generation task", append(logFields, zap.Error(err))...)
			return sharedModels.ErrInternalServer
		}
		s.logger.Info("Game over task published", append(logFields, zap.String("taskID", taskID))...)
		return nil
	}

	// 12. Not Game Over: Calculate next state hash
	newStateHash, errHash := calculateStateHash(currentProgress.CurrentStateHash, nextProgress.CoreStats, nextProgress.StoryVariables, nextProgress.GlobalFlags)
	if errHash != nil {
		s.logger.Error("Failed to calculate new state hash", append(logFields, zap.Error(errHash))...)
		return sharedModels.ErrInternalServer
	}
	logFields = append(logFields, zap.String("newStateHash", newStateHash))
	s.logger.Debug("New state hash calculated", logFields...)

	// 13. Find or Save the next PlayerProgress node
	var nextNodeProgressID uuid.UUID
	var nextNodeProgress *sharedModels.PlayerProgress
	existingNode, errFind := s.playerProgressRepo.GetByStoryIDAndHash(ctx, publishedStoryID, newStateHash)

	if errFind == nil {
		nextNodeProgressID = existingNode.ID
		nextNodeProgress = existingNode
		s.logger.Info("Next PlayerProgress node already exists", append(logFields, zap.String("progressID", nextNodeProgressID.String()))...)
	} else if errors.Is(errFind, sharedModels.ErrNotFound) || errors.Is(errFind, pgx.ErrNoRows) {
		nextProgress.StoryVariables = make(map[string]interface{})
		nextProgress.GlobalFlags = clearTransientFlags(nextProgress.GlobalFlags)
		nextProgress.CurrentStateHash = newStateHash

		savedID, errSave := s.playerProgressRepo.Save(ctx, nextProgress)
		if errSave != nil {
			s.logger.Error("Failed to save new PlayerProgress node", append(logFields, zap.Error(errSave))...)
			return sharedModels.ErrInternalServer
		}
		nextNodeProgressID = savedID
		nextNodeProgress = nextProgress
		s.logger.Info("Saved new PlayerProgress node", append(logFields, zap.String("progressID", nextNodeProgressID.String()))...)
	} else {
		s.logger.Error("Error finding/saving next PlayerProgress node", append(logFields, zap.Error(errFind))...)
		return sharedModels.ErrInternalServer
	}

	// 14. Find the next Scene associated with the new state hash
	var nextSceneID *uuid.UUID
	nextScene, errScene := s.sceneRepo.FindByStoryAndHash(ctx, publishedStoryID, newStateHash)
	if errScene == nil {
		// Scene already exists
		nextSceneID = &nextScene.ID
		s.logger.Info("Next scene found in DB", append(logFields, zap.String("sceneID", nextSceneID.String()))...)

		// Update PlayerGameState
		gameState.PlayerStatus = sharedModels.PlayerStatusPlaying
		gameState.CurrentSceneID = nextSceneID
		gameState.PlayerProgressID = &nextNodeProgressID
		gameState.LastActivityAt = time.Now().UTC()

		if _, err := s.playerGameStateRepo.Save(ctx, gameState); err != nil {
			s.logger.Error("Failed to update PlayerGameState after finding next scene", append(logFields, zap.Error(err))...)
			return sharedModels.ErrInternalServer
		}
		s.logger.Info("PlayerGameState updated to Playing, linked to existing scene")
		return nil

	} else if errors.Is(errScene, pgx.ErrNoRows) || errors.Is(errScene, sharedModels.ErrNotFound) {
		// Scene does not exist, need to generate it
		s.logger.Info("Next scene not found, initiating generation", logFields...)

		// Update PlayerGameState
		gameState.PlayerStatus = sharedModels.PlayerStatusGeneratingScene
		gameState.CurrentSceneID = nil
		gameState.PlayerProgressID = &nextNodeProgressID
		gameState.LastActivityAt = time.Now().UTC()

		if _, err := s.playerGameStateRepo.Save(ctx, gameState); err != nil {
			s.logger.Error("Failed to update PlayerGameState to GeneratingScene", append(logFields, zap.Error(err))...)
			return sharedModels.ErrInternalServer
		}
		s.logger.Info("PlayerGameState updated to GeneratingScene")

		// Publish Generation Task
		generationPayload, errGenPayload := createGenerationPayload(
			playerID,
			publishedStory,
			nextNodeProgress,
			madeChoicesInfo,
			newStateHash,
		)
		if errGenPayload != nil {
			s.logger.Error("Failed to create generation payload", append(logFields, zap.Error(errGenPayload))...)
			return sharedModels.ErrInternalServer
		}
		generationPayload.GameStateID = gameState.ID.String()

		if errPub := s.publisher.PublishGenerationTask(ctx, generationPayload); errPub != nil {
			s.logger.Error("Failed to publish next scene generation task", append(logFields, zap.Error(errPub))...)
			return sharedModels.ErrInternalServer
		}
		s.logger.Info("Next scene generation task published", append(logFields, zap.String("taskID", generationPayload.TaskID))...)
		return nil

	} else {
		// Other error finding scene
		s.logger.Error("Error searching for next scene", append(logFields, zap.Error(errScene))...)
		return sharedModels.ErrInternalServer
	}
}

// DeletePlayerGameState deletes player game state for the specified story.
// Renamed from DeletePlayerProgress
func (s *gameLoopServiceImpl) DeletePlayerGameState(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) error {
	log := s.logger.With(zap.String("userID", userID.String()), zap.String("publishedStoryID", publishedStoryID.String()))
	log.Info("Deleting player game state")

	// Use PlayerGameStateRepository
	err := s.playerGameStateRepo.DeleteByPlayerAndStory(ctx, userID, publishedStoryID)
	if err != nil {
		// Check if the error is specifically that state didn't exist
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, sharedModels.ErrNotFound) {
			log.Warn("Player game state not found, nothing to delete")
			return sharedModels.ErrPlayerGameStateNotFound
		}
		// Log other DB errors
		log.Error("Error deleting player game state from repository", zap.Error(err))
		return sharedModels.ErrInternalServer // Return generic internal error
	}

	log.Info("Player game state deleted successfully")
	return nil
}

// RetrySceneGeneration handles the logic for retrying generation for a published story.
// It checks if the error occurred during Setup or Scene generation and restarts the appropriate task.
func (s *gameLoopServiceImpl) RetrySceneGeneration(ctx context.Context, storyID, userID uuid.UUID) error {
	log := s.logger.With(zap.String("publishedStoryID", storyID.String()), zap.String("userID", userID.String()))
	log.Info("RetrySceneGeneration called")

	// 1. Get the story
	story, err := s.publishedRepo.GetByID(ctx, storyID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Warn("Published story not found for retry")
			return sharedModels.ErrStoryNotFound
		}
		log.Error("Failed to get published story for retry", zap.Error(err))
		return sharedModels.ErrInternalServer
	}

	// 2. Check status (must be Error)
	if story.Status != sharedModels.StatusError {
		log.Warn("Attempt to retry generation for story not in Error status", zap.String("status", string(story.Status)))
		return sharedModels.ErrCannotRetry
	}

	// 3. Check if Setup generation failed or Scene generation failed
	setupExists := story.Setup != nil && string(story.Setup) != "null"

	if !setupExists {
		// --- Error occurred during Setup generation (Setup is nil or JSON null) ---
		log.Info("Setup is nil or JSON null, retrying Setup generation")

		if story.Config == nil {
			log.Error("CRITICAL: Story is in Error, Setup is nil/null, and Config is also nil. Cannot retry Setup.")
			return sharedModels.ErrInternalServer // Cannot proceed
		}

		// Update status back to SetupPending
		if err := s.publishedRepo.UpdateStatusDetails(ctx, storyID, sharedModels.StatusSetupPending, nil, nil, nil, nil); err != nil {
			log.Error("Failed to update story status to SetupPending before retry task publish", zap.Error(err))
			return sharedModels.ErrInternalServer
		}

		// Create and publish Setup task payload
		taskID := uuid.New().String()
		configJSONString := string(story.Config) // Config is needed for Setup
		setupPayload := sharedMessaging.GenerationTaskPayload{
			TaskID:           taskID,
			UserID:           story.UserID.String(), // Use UserID from the story
			PromptType:       sharedMessaging.PromptTypeNovelSetup,
			UserInput:        configJSONString,
			PublishedStoryID: storyID.String(),
		}

		if err := s.publisher.PublishGenerationTask(ctx, setupPayload); err != nil {
			log.Error("Error publishing retry setup generation task. Rolling back status...", zap.Error(err))
			if rollbackErr := s.publishedRepo.UpdateStatusDetails(context.Background(), storyID, sharedModels.StatusError, nil, nil, nil, nil); rollbackErr != nil {
				log.Error("CRITICAL: Failed to roll back status to Error after setup retry publish error", zap.Error(rollbackErr))
			}
			return sharedModels.ErrInternalServer
		}

		log.Info("Retry setup generation task published successfully", zap.String("taskID", taskID))
		return nil

	} else {
		// --- Error occurred during Scene generation (Setup exists and is not JSON null) ---
		log.Info("Setup exists, retrying Scene generation")

		// <<< ДОБАВЛЕНО: Проверка и генерация картинок Setup >>>
		var setupContent sharedModels.NovelSetupContent
		if errUnmarshal := json.Unmarshal(story.Setup, &setupContent); errUnmarshal != nil {
			log.Error("Failed to unmarshal setup JSON during scene retry, cannot check images", zap.Error(errUnmarshal))
			// Продолжаем без проверки картинок, но логируем ошибку.
		} else {
			// Вызываем хелпер для проверки/генерации картинок
			if _, errImages := s.checkAndGenerateSetupImages(ctx, userID.String(), story, &setupContent); errImages != nil {
				// Логируем ошибку, но не прерываем Retry сцены
				log.Error("Error during checkAndGenerateSetupImages in scene retry", zap.Error(errImages))
			}
		}
		// <<< КОНЕЦ: Проверка и генерация картинок Setup >>>

		// Get player progress to determine which scene to retry
		progress, err := s.playerProgressRepo.GetByUserIDAndStoryID(ctx, userID, storyID)
		if err != nil {
			if errors.Is(err, sharedModels.ErrNotFound) {
				log.Warn("Player progress not found for scene retry. Assuming retry for initial scene.")
				generationPayload, errPayload := createInitialSceneGenerationPayload(userID, story)
				if errPayload != nil {
					log.Error("Failed to create initial generation payload for scene retry", zap.Error(errPayload))
					if rollbackErr := s.publishedRepo.UpdateStatusDetails(context.Background(), storyID, sharedModels.StatusError, nil, nil, nil, nil); rollbackErr != nil {
						log.Error("CRITICAL: Failed to roll back status to Error after initial payload creation error", zap.Error(rollbackErr))
					}
					return sharedModels.ErrInternalServer
				}
				if errPub := s.publisher.PublishGenerationTask(ctx, generationPayload); errPub != nil {
					log.Error("Error publishing initial scene retry generation task. Rolling back status...", zap.Error(errPub))
					if rollbackErr := s.publishedRepo.UpdateStatusDetails(context.Background(), storyID, sharedModels.StatusError, nil, nil, nil, nil); rollbackErr != nil {
						log.Error("CRITICAL: Failed to roll back status to Error after initial scene retry publish error", zap.Error(rollbackErr))
					}
					return sharedModels.ErrInternalServer
				}
				log.Info("Initial scene retry generation task published successfully", zap.String("taskID", generationPayload.TaskID), zap.String("stateHash", generationPayload.StateHash))
				return nil // Задача для начальной сцены отправлена
			} else {
				log.Error("Failed to get player progress for scene retry", zap.Error(err))
				return sharedModels.ErrInternalServer
			}
		}

		// Прогресс найден, продолжаем стандартную логику Retry для существующего progress
		madeChoicesInfo := []sharedModels.UserChoiceInfo{} // No choice info on a simple retry
		generationPayload, err := createGenerationPayload(
			userID,
			story,
			progress,
			madeChoicesInfo,
			progress.CurrentStateHash, // Retry for the current hash in progress
		)
		if err != nil {
			log.Error("Failed to create generation payload for scene retry", zap.Error(err))
			if rollbackErr := s.publishedRepo.UpdateStatusDetails(context.Background(), storyID, sharedModels.StatusError, nil, nil, nil, nil); rollbackErr != nil {
				log.Error("CRITICAL: Failed to roll back status to Error after payload creation error", zap.Error(rollbackErr))
			}
			return sharedModels.ErrInternalServer
		}
		generationPayload.PromptType = sharedMessaging.PromptTypeNovelCreator // Ensure correct type

		if err := s.publisher.PublishGenerationTask(ctx, generationPayload); err != nil {
			log.Error("Error publishing retry scene generation task. Rolling back status...", zap.Error(err))
			if rollbackErr := s.publishedRepo.UpdateStatusDetails(context.Background(), storyID, sharedModels.StatusError, nil, nil, nil, nil); rollbackErr != nil {
				log.Error("CRITICAL: Failed to roll back status to Error after scene retry publish error", zap.Error(rollbackErr))
			}
			return sharedModels.ErrInternalServer
		}

		log.Info("Retry scene generation task published successfully", zap.String("taskID", generationPayload.TaskID), zap.String("stateHash", progress.CurrentStateHash))
		return nil
	}
}

// RetryStoryGeneration handles the logic for retrying generation for a published story.
// It checks if the error occurred during Setup or Scene generation and restarts the appropriate task.
// <<< ВАЖНО: Нужно также обновить этот дублирующий метод или удалить его >>>
func (s *gameLoopServiceImpl) RetryStoryGeneration(ctx context.Context, storyID, userID uuid.UUID) error {
	log := s.logger.With(zap.String("publishedStoryID", storyID.String()), zap.String("userID", userID.String()))
	log.Info("RetryStoryGeneration called")

	// 1. Get the story
	story, err := s.publishedRepo.GetByID(ctx, storyID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Warn("Published story not found for retry")
			return sharedModels.ErrStoryNotFound
		}
		log.Error("Failed to get published story for retry", zap.Error(err))
		return sharedModels.ErrInternalServer
	}

	// 2. Check status (must be Error)
	if story.Status != sharedModels.StatusError {
		log.Warn("Attempt to retry generation for story not in Error status", zap.String("status", string(story.Status)))
		return sharedModels.ErrCannotRetry
	}

	// 3. Check if Setup generation failed or Scene generation failed
	setupExists := story.Setup != nil && string(story.Setup) != "null"

	if !setupExists {
		// --- Error occurred during Setup generation (Setup is nil or JSON null) ---
		log.Info("Setup is nil or JSON null, retrying Setup generation")

		if story.Config == nil {
			log.Error("CRITICAL: Story is in Error, Setup is nil/null, and Config is also nil. Cannot retry Setup.")
			return sharedModels.ErrInternalServer // Cannot proceed
		}

		// Update status back to SetupPending
		if err := s.publishedRepo.UpdateStatusDetails(ctx, storyID, sharedModels.StatusSetupPending, nil, nil, nil, nil); err != nil {
			log.Error("Failed to update story status to SetupPending before retry task publish", zap.Error(err))
			return sharedModels.ErrInternalServer
		}

		// Create and publish Setup task payload
		taskID := uuid.New().String()
		configJSONString := string(story.Config) // Config is needed for Setup
		setupPayload := sharedMessaging.GenerationTaskPayload{
			TaskID:           taskID,
			UserID:           story.UserID.String(), // Use UserID from the story
			PromptType:       sharedMessaging.PromptTypeNovelSetup,
			UserInput:        configJSONString,
			PublishedStoryID: storyID.String(),
		}

		if err := s.publisher.PublishGenerationTask(ctx, setupPayload); err != nil {
			log.Error("Error publishing retry setup generation task. Rolling back status...", zap.Error(err))
			if rollbackErr := s.publishedRepo.UpdateStatusDetails(context.Background(), storyID, sharedModels.StatusError, nil, nil, nil, nil); rollbackErr != nil {
				log.Error("CRITICAL: Failed to roll back status to Error after setup retry publish error", zap.Error(rollbackErr))
			}
			return sharedModels.ErrInternalServer
		}

		log.Info("Retry setup generation task published successfully", zap.String("taskID", taskID))
		return nil

	} else {
		// --- Error occurred during Scene generation (Setup exists and is not JSON null) ---
		log.Info("Setup exists, retrying Scene generation")

		// <<< ДОБАВЛЕНО: Проверка и генерация картинок Setup >>>
		var setupContent sharedModels.NovelSetupContent
		if errUnmarshal := json.Unmarshal(story.Setup, &setupContent); errUnmarshal != nil {
			log.Error("Failed to unmarshal setup JSON during scene retry, cannot check images", zap.Error(errUnmarshal))
			// Продолжаем без проверки картинок, но логируем ошибку.
		} else {
			// Вызываем хелпер для проверки/генерации картинок
			if _, errImages := s.checkAndGenerateSetupImages(ctx, userID.String(), story, &setupContent); errImages != nil {
				// Логируем ошибку, но не прерываем Retry сцены
				log.Error("Error during checkAndGenerateSetupImages in scene retry", zap.Error(errImages))
			}
		}
		// <<< КОНЕЦ: Проверка и генерация картинок Setup >>>

		// Get player progress to determine which scene to retry
		progress, err := s.playerProgressRepo.GetByUserIDAndStoryID(ctx, userID, storyID)
		if err != nil {
			if errors.Is(err, sharedModels.ErrNotFound) {
				log.Warn("Player progress not found for scene retry. Assuming retry for initial scene.")
				generationPayload, errPayload := createInitialSceneGenerationPayload(userID, story)
				if errPayload != nil {
					log.Error("Failed to create initial generation payload for scene retry", zap.Error(errPayload))
					if rollbackErr := s.publishedRepo.UpdateStatusDetails(context.Background(), storyID, sharedModels.StatusError, nil, nil, nil, nil); rollbackErr != nil {
						log.Error("CRITICAL: Failed to roll back status to Error after initial payload creation error", zap.Error(rollbackErr))
					}
					return sharedModels.ErrInternalServer
				}
				if errPub := s.publisher.PublishGenerationTask(ctx, generationPayload); errPub != nil {
					log.Error("Error publishing initial scene retry generation task. Rolling back status...", zap.Error(errPub))
					if rollbackErr := s.publishedRepo.UpdateStatusDetails(context.Background(), storyID, sharedModels.StatusError, nil, nil, nil, nil); rollbackErr != nil {
						log.Error("CRITICAL: Failed to roll back status to Error after initial scene retry publish error", zap.Error(rollbackErr))
					}
					return sharedModels.ErrInternalServer
				}
				log.Info("Initial scene retry generation task published successfully", zap.String("taskID", generationPayload.TaskID), zap.String("stateHash", generationPayload.StateHash))
				return nil // Задача для начальной сцены отправлена
			} else {
				log.Error("Failed to get player progress for scene retry", zap.Error(err))
				return sharedModels.ErrInternalServer
			}
		}

		// Прогресс найден, продолжаем стандартную логику Retry для существующего progress
		madeChoicesInfo := []sharedModels.UserChoiceInfo{} // No choice info on a simple retry
		generationPayload, err := createGenerationPayload(
			userID,
			story,
			progress,
			madeChoicesInfo,
			progress.CurrentStateHash, // Retry for the current hash in progress
		)
		if err != nil {
			log.Error("Failed to create generation payload for scene retry", zap.Error(err))
			if rollbackErr := s.publishedRepo.UpdateStatusDetails(context.Background(), storyID, sharedModels.StatusError, nil, nil, nil, nil); rollbackErr != nil {
				log.Error("CRITICAL: Failed to roll back status to Error after payload creation error", zap.Error(rollbackErr))
			}
			return sharedModels.ErrInternalServer
		}
		generationPayload.PromptType = sharedMessaging.PromptTypeNovelCreator // Ensure correct type

		if err := s.publisher.PublishGenerationTask(ctx, generationPayload); err != nil {
			log.Error("Error publishing retry scene generation task. Rolling back status...", zap.Error(err))
			if rollbackErr := s.publishedRepo.UpdateStatusDetails(context.Background(), storyID, sharedModels.StatusError, nil, nil, nil, nil); rollbackErr != nil {
				log.Error("CRITICAL: Failed to roll back status to Error after scene retry publish error", zap.Error(rollbackErr))
			}
			return sharedModels.ErrInternalServer
		}

		log.Info("Retry scene generation task published successfully", zap.String("taskID", generationPayload.TaskID), zap.String("stateHash", progress.CurrentStateHash))
		return nil
	}
}

// GetPlayerProgress retrieves the player's current progress node based on their game state.
func (s *gameLoopServiceImpl) GetPlayerProgress(ctx context.Context, userID, storyID uuid.UUID) (*sharedModels.PlayerProgress, error) {
	log := s.logger.With(zap.String("userID", userID.String()), zap.String("storyID", storyID.String()))
	log.Debug("GetPlayerProgress called")

	// 1. Get the player's game state.
	gameState, errState := s.GetOrCreatePlayerGameState(ctx, userID, storyID)
	if errState != nil {
		// Errors are logged within GetOrCreatePlayerGameState
		if errors.Is(errState, sharedModels.ErrStoryNotFound) || errors.Is(errState, sharedModels.ErrStoryNotReady) {
			return nil, errState // Return specific errors
		}
		return nil, sharedModels.ErrInternalServer // Default to internal error
	}

	// 2. Check if PlayerProgressID exists in the game state.
	if gameState.PlayerProgressID == nil {
		log.Warn("Player game state exists but has no associated PlayerProgressID", zap.String("gameStateID", gameState.ID.String()))
		// This might happen if the initial progress node wasn't created/linked yet.
		// Consider if ErrNotFound or a different error is more appropriate.
		return nil, sharedModels.ErrNotFound
	}

	// 3. Fetch the PlayerProgress node using the ID from the game state.
	progress, errProgress := s.playerProgressRepo.GetByID(ctx, *gameState.PlayerProgressID)
	if errProgress != nil {
		// Log the specific error from the repository
		log.Error("Failed to get player progress node by ID from repository", zap.String("progressID", gameState.PlayerProgressID.String()), zap.Error(errProgress))
		// Map common errors
		if errors.Is(errProgress, pgx.ErrNoRows) || errors.Is(errProgress, sharedModels.ErrNotFound) {
			// This indicates inconsistency: gameState points to a non-existent progress node.
			return nil, sharedModels.ErrNotFound
		}
		// For other errors, return a generic internal server error
		return nil, sharedModels.ErrInternalServer
	}

	log.Debug("Player progress node retrieved successfully", zap.String("progressID", progress.ID.String()))
	return progress, nil
}

// DeleteSceneInternal deletes a scene.
func (s *gameLoopServiceImpl) DeleteSceneInternal(ctx context.Context, sceneID uuid.UUID) error {
	log := s.logger.With(zap.String("sceneID", sceneID.String()))
	log.Info("DeleteSceneInternal called")

	// Вызов репозитория
	err := s.sceneRepo.Delete(ctx, sceneID)
	if err != nil {
		if errors.Is(err, sharedModels.ErrNotFound) {
			log.Warn("Scene not found for deletion")
			return sharedModels.ErrNotFound
		}
		log.Error("Failed to delete scene from repository", zap.Error(err))
		return sharedModels.ErrInternalServer
	}

	log.Info("Scene deleted successfully")
	return nil
}

// UpdatePlayerProgressInternal updates the player's progress internally.
func (s *gameLoopServiceImpl) UpdatePlayerProgressInternal(ctx context.Context, progressID uuid.UUID, progressData map[string]interface{}) error {
	log := s.logger.With(zap.String("progressID", progressID.String()))
	log.Info("Attempting to update player progress internally")

	// 1. Получить текущий прогресс, чтобы убедиться, что он существует
	// Используем GetByID, так как userID нам не важен для внутреннего обновления
	currentProgress, err := s.playerProgressRepo.GetByID(ctx, progressID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Warn("Player progress not found for internal update")
			// Используем стандартную ошибку NotFound
			return fmt.Errorf("%w: player progress with ID %s not found", sharedModels.ErrNotFound, progressID)
		}
		log.Error("Failed to get player progress by ID for internal update", zap.Error(err))
		return fmt.Errorf("failed to retrieve player progress: %w", err)
	}

	// 2. Обновить данные прогресса
	// Предполагаем, что progressData содержит поля, которые нужно обновить.
	// PlayerProgressRepository должен иметь метод для частичного обновления (например, UpdateFields или аналогичный)
	// Если такого метода нет, нужно будет его добавить в репозиторий.
	// Пока что предполагаем, что Update обновляет все переданные поля.

	// UpdateFields метод существует в репозитории, используем его.
	// Если нет, нужно либо добавить его, либо использовать Update,
	// но тогда нужно быть осторожным, чтобы не затереть нужные поля.
	// Пока используем гипотетический UpdateFields, если он есть, или Update.

	// Пример: Обновляем только поле ProgressDataJSON
	progressDataBytes, err := json.Marshal(progressData)
	if err != nil {
		log.Error("Failed to marshal progress data for internal update", zap.Error(err))
		return fmt.Errorf("%w: failed to marshal progress data", sharedModels.ErrBadRequest)
	}

	// Обновляем только нужные поля. Если репозиторий поддерживает map[string]interface{}
	// то можно передать progressData напрямую. Иначе, нужно обновить конкретные поля.
	// Пример обновления JSON поля:
	updates := map[string]interface{}{
		"progress_data_json": json.RawMessage(progressDataBytes), // Поле из базы данных
		"updated_at":         time.Now().UTC(),                   // Обновляем время изменения
	}

	err = s.playerProgressRepo.UpdateFields(ctx, currentProgress.ID, updates) // Используем UpdateFields
	if err != nil {
		log.Error("Failed to update player progress internally in repository", zap.Error(err))
		// Можно добавить обработку специфичных ошибок репозитория, если нужно
		return fmt.Errorf("failed to update player progress in repository: %w", err)
	}

	log.Info("Player progress updated successfully internally")
	return nil
}

// GetOrCreatePlayerGameState retrieves the player's game state for a story, creating it if it doesn't exist.
func (s *gameLoopServiceImpl) GetOrCreatePlayerGameState(ctx context.Context, playerID, storyID uuid.UUID) (*sharedModels.PlayerGameState, error) {
	log := s.logger.With(zap.String("playerID", playerID.String()), zap.String("storyID", storyID.String()))
	log.Debug("GetOrCreatePlayerGameState called")

	// 1. Try to get existing game state
	gameState, err := s.playerGameStateRepo.GetByPlayerAndStory(ctx, playerID, storyID)
	if err == nil {
		log.Debug("Existing player game state found")
		return gameState, nil
	}

	// 2. If not found, create a new one
	if errors.Is(err, sharedModels.ErrNotFound) || errors.Is(err, pgx.ErrNoRows) {
		log.Info("Player game state not found, creating initial state")

		// 2a. Fetch the published story
		publishedStory, storyErr := s.publishedRepo.GetByID(ctx, storyID)
		if storyErr != nil {
			if errors.Is(storyErr, pgx.ErrNoRows) {
				log.Error("Published story not found while trying to create initial game state")
				return nil, sharedModels.ErrStoryNotFound
			}
			log.Error("Failed to get published story while creating initial game state", zap.Error(storyErr))
			return nil, sharedModels.ErrInternalServer
		}

		// 2b. Check story status
		if publishedStory.Status != sharedModels.StatusReady {
			log.Warn("Attempt to create game state for story not in Ready status", zap.String("status", string(publishedStory.Status)))
			return nil, sharedModels.ErrStoryNotReady
		}

		// 2c. Find or Create the Initial PlayerProgress node
		var initialProgressID uuid.UUID
		initialProgress, progressErr := s.playerProgressRepo.GetByStoryIDAndHash(ctx, storyID, sharedModels.InitialStateHash)

		if progressErr == nil {
			initialProgressID = initialProgress.ID
			log.Debug("Found existing initial PlayerProgress node", zap.String("progressID", initialProgressID.String()))
		} else if errors.Is(progressErr, sharedModels.ErrNotFound) || errors.Is(progressErr, pgx.ErrNoRows) {
			log.Info("Initial PlayerProgress node not found, creating it")
			initialStats := make(map[string]int)
			if publishedStory.Setup != nil && string(publishedStory.Setup) != "null" {
				var setupContent sharedModels.NovelSetupContent
				if setupErr := json.Unmarshal(publishedStory.Setup, &setupContent); setupErr != nil {
					log.Error("Failed to unmarshal story Setup JSON for initial progress stats", zap.Error(setupErr))
				} else if setupContent.CoreStatsDefinition != nil {
					for key, statDef := range setupContent.CoreStatsDefinition {
						initialStats[key] = statDef.Initial
					}
					log.Debug("Initialized initial progress stats from story setup", zap.Any("initialStats", initialStats))
				}
			}

			newInitialProgress := &sharedModels.PlayerProgress{
				UserID:           playerID,
				PublishedStoryID: storyID,
				CurrentStateHash: sharedModels.InitialStateHash,
				CoreStats:        initialStats,
				StoryVariables:   make(map[string]interface{}),
				GlobalFlags:      []string{},
				SceneIndex:       0,
			}
			savedID, createErr := s.playerProgressRepo.Save(ctx, newInitialProgress)
			if createErr != nil {
				log.Error("Error creating initial player progress node in repository", zap.Error(createErr))
				return nil, sharedModels.ErrInternalServer
			}
			initialProgressID = savedID
			log.Info("Initial PlayerProgress node created successfully", zap.String("progressID", initialProgressID.String()))
		} else {
			log.Error("Unexpected error getting initial player progress node", zap.Error(progressErr))
			return nil, sharedModels.ErrInternalServer
		}

		// 2d. Find the Initial Scene ID (if it exists)
		var initialSceneID *uuid.UUID
		initialScene, sceneErr := s.sceneRepo.FindByStoryAndHash(ctx, storyID, sharedModels.InitialStateHash)
		if sceneErr == nil {
			initialSceneID = &initialScene.ID
			log.Debug("Found initial scene", zap.String("sceneID", initialSceneID.String()))
		} else if !errors.Is(sceneErr, pgx.ErrNoRows) {
			log.Error("Error fetching initial scene by hash", zap.Error(sceneErr))
		}

		// 2e. Create the new PlayerGameState
		now := time.Now().UTC()
		newGameState := &sharedModels.PlayerGameState{
			PlayerID:         playerID,
			PublishedStoryID: storyID,
			PlayerProgressID: &initialProgressID,
			CurrentSceneID:   initialSceneID,
			PlayerStatus:     sharedModels.PlayerStatusPlaying,
			StartedAt:        now,
			LastActivityAt:   now,
		}

		// 2f. Save the new PlayerGameState
		createdStateID, saveErr := s.playerGameStateRepo.Save(ctx, newGameState)
		if saveErr != nil {
			log.Error("Error creating initial player game state in repository", zap.Error(saveErr))
			return nil, sharedModels.ErrInternalServer
		}
		newGameState.ID = createdStateID

		log.Info("Initial player game state created successfully", zap.String("gameStateID", createdStateID.String()))
		return newGameState, nil

	} else {
		log.Error("Unexpected error getting player game state", zap.Error(err))
		return nil, sharedModels.ErrInternalServer
	}
}

// --- Helper Functions ---

// calculateStateHash calculates a deterministic state hash, including the previous state hash.
func calculateStateHash(previousHash string, coreStats map[string]int, storyVars map[string]interface{}, globalFlags []string) (string, error) {
	// 1. Prepare data
	stateMap := make(map[string]interface{})

	stateMap["_ph"] = previousHash // Include previous hash

	// Add core stats (prefix to avoid collisions)
	for k, v := range coreStats {
		stateMap["cs_"+k] = v
	}

	// Add story variables (prefix)
	// Only include non-nil and non-transient variables (e.g., those not starting with '_')
	for k, v := range storyVars {
		if v != nil && !strings.HasPrefix(k, "_") {
			stateMap["sv_"+k] = v
		}
	}

	// Add sorted global flags (prefix)
	// Filter out transient flags (starting with '_') before sorting and hashing
	nonTransientFlags := make([]string, 0, len(globalFlags))
	for _, flag := range globalFlags {
		if !strings.HasPrefix(flag, "_") {
			nonTransientFlags = append(nonTransientFlags, flag)
		}
	}
	sort.Strings(nonTransientFlags)
	stateMap["gf"] = nonTransientFlags

	// 2. Sort keys for deterministic serialization
	keys := make([]string, 0, len(stateMap))
	for k := range stateMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// 3. Build canonical JSON string
	var sb strings.Builder
	sb.WriteString("{")
	for i, k := range keys {
		valueBytes, err := json.Marshal(stateMap[k])
		if err != nil {
			log.Printf("ERROR calculating state hash: failed to marshal value for key '%s': %v", k, err) // Use standard log for util func
			return "", fmt.Errorf("error serializing value for key '%s': %w", k, err)
		}
		sb.WriteString(fmt.Sprintf("\"%s\":%s", k, string(valueBytes)))
		if i < len(keys)-1 {
			sb.WriteString(",")
		}
	}
	sb.WriteString("}")
	canonicalJSON := sb.String()

	// 4. Calculate SHA256 hash
	hasher := sha256.New()
	hasher.Write([]byte(canonicalJSON))
	hashBytes := hasher.Sum(nil)

	return hex.EncodeToString(hashBytes), nil
}

// applyConsequences applies consequences of choice to player progress
// and checks Game Over conditions.
// Returns stat name causing Game Over and Game Over flag.
func applyConsequences(progress *sharedModels.PlayerProgress, cons sharedModels.Consequences, setup *sharedModels.NovelSetupContent) (gameOverStat string, isGameOver bool) {
	if progress == nil || setup == nil {
		log.Println("ERROR: applyConsequences called with nil progress or setup")
		return "", false // Should not happen in normal flow
	}

	// Ensure maps/slices exist
	if progress.CoreStats == nil {
		progress.CoreStats = make(map[string]int)
	}
	if progress.StoryVariables == nil {
		progress.StoryVariables = make(map[string]interface{})
	}
	if progress.GlobalFlags == nil {
		progress.GlobalFlags = []string{}
	}

	// Apply core stat changes
	if cons.CoreStatsChange != nil {
		for statName, change := range cons.CoreStatsChange {
			progress.CoreStats[statName] += change
		}
	}

	// Apply story variable changes (set or unset)
	if cons.StoryVariables != nil {
		for varName, value := range cons.StoryVariables {
			if value == nil {
				delete(progress.StoryVariables, varName)
			} else {
				progress.StoryVariables[varName] = value
			}
		}
	}

	// Remove specified global flags
	if len(cons.GlobalFlagsRemove) > 0 {
		flagsToRemove := make(map[string]struct{}, len(cons.GlobalFlagsRemove))
		for _, flag := range cons.GlobalFlagsRemove {
			flagsToRemove[flag] = struct{}{}
		}
		newFlags := make([]string, 0, len(progress.GlobalFlags))
		for _, flag := range progress.GlobalFlags {
			if _, found := flagsToRemove[flag]; !found {
				newFlags = append(newFlags, flag)
			}
		}
		progress.GlobalFlags = newFlags
	}

	// Add new global flags (if not already present)
	if len(cons.GlobalFlags) > 0 {
		existingFlags := make(map[string]struct{}, len(progress.GlobalFlags))
		for _, flag := range progress.GlobalFlags {
			existingFlags[flag] = struct{}{}
		}
		for _, flagToAdd := range cons.GlobalFlags {
			if _, found := existingFlags[flagToAdd]; !found {
				progress.GlobalFlags = append(progress.GlobalFlags, flagToAdd)
				existingFlags[flagToAdd] = struct{}{}
			}
		}
	}

	// Check game over conditions based on the *updated* core stats
	if setup.CoreStatsDefinition != nil {
		for statName, definition := range setup.CoreStatsDefinition {
			currentValue, exists := progress.CoreStats[statName]
			if !exists { // If stat doesn't exist, assume it's 0 for comparison
				currentValue = 0
			}
			// Check game over conditions based on StatDefinition flags
			// Assuming the 0-100 range is fixed game mechanic.

			// Check Min condition IF it's a game over condition
			if definition.GameOverConditions.Min && currentValue <= 0 {
				return statName, true
			}
			// Check Max condition IF it's a game over condition
			if definition.GameOverConditions.Max && currentValue >= 100 {
				return statName, true
			}
		}
	}

	return "", false // No game over condition met
}

// createGenerationPayload creates the payload for the next scene generation task,
// using compressed keys and summaries from the previous step.
func createGenerationPayload(
	userID uuid.UUID,
	story *sharedModels.PublishedStory,
	progress *sharedModels.PlayerProgress,
	madeChoicesInfo []sharedModels.UserChoiceInfo,
	currentStateHash string,
) (sharedMessaging.GenerationTaskPayload, error) {

	// --- MODIFICATION START: Parse full config/setup and create minimal versions ---
	if story.Config == nil || story.Setup == nil {
		log.Printf("ERROR: Story Config or Setup is nil for StoryID %s", story.ID)
		return sharedMessaging.GenerationTaskPayload{}, fmt.Errorf("story config or setup is nil")
	}
	var fullConfig sharedModels.Config
	if err := json.Unmarshal(story.Config, &fullConfig); err != nil {
		log.Printf("WARN: Failed to parse Config JSON for generation task StoryID %s: %v", story.ID, err)
		// Continue with empty minimal config? Or return error?
		// Return error for now, as config is likely important.
		return sharedMessaging.GenerationTaskPayload{}, fmt.Errorf("error parsing Config JSON: %w", err)
	}
	var fullSetup sharedModels.NovelSetupContent
	if err := json.Unmarshal(story.Setup, &fullSetup); err != nil {
		log.Printf("WARN: Failed to parse Setup JSON for generation task StoryID %s: %v", story.ID, err)
		// Continue with empty minimal setup? Or return error?
		// Return error for now.
		return sharedMessaging.GenerationTaskPayload{}, fmt.Errorf("error parsing Setup JSON: %w", err)
	}

	minimalConfig := sharedModels.ToMinimalConfigForScene(&fullConfig)
	minimalSetup := sharedModels.ToMinimalSetupForScene(&fullSetup)
	// --- MODIFICATION END ---

	compressedInputData := make(map[string]interface{})

	// --- Essential Data: Use MINIMAL structs here ---
	compressedInputData["cfg"] = minimalConfig // Minimal Config
	compressedInputData["stp"] = minimalSetup  // Minimal Setup

	// --- Current State (before this choice) ---
	if progress.CoreStats != nil {
		compressedInputData["cs"] = progress.CoreStats
	}
	// Filter non-transient global flags before sending
	nonTransientFlags := make([]string, 0, len(progress.GlobalFlags))
	for _, flag := range progress.GlobalFlags {
		if !strings.HasPrefix(flag, "_") {
			nonTransientFlags = append(nonTransientFlags, flag)
		}
	}
	sort.Strings(nonTransientFlags)
	compressedInputData["gf"] = nonTransientFlags

	// Include summaries from the *previous* step (the scene the player just saw)
	compressedInputData["pss"] = progress.LastStorySummary // Use correct field names
	compressedInputData["pfd"] = progress.LastFutureDirection
	compressedInputData["pvis"] = progress.LastVarImpactSummary

	// --- Player Action & Transient State ---
	// Filter non-transient story variables before sending
	nonTransientVars := make(map[string]interface{})
	if progress.StoryVariables != nil {
		for k, v := range progress.StoryVariables {
			if v != nil && !strings.HasPrefix(k, "_") {
				nonTransientVars[k] = v
			}
		}
	}
	compressedInputData["sv"] = nonTransientVars // Only non-nil, non-transient vars resulting from choice

	// Include info about the choice(s) the user just made
	userChoiceMap := make(map[string]string) // Convert UserChoiceInfo for AI prompt
	if len(madeChoicesInfo) > 0 {
		lastChoice := madeChoicesInfo[len(madeChoicesInfo)-1] // Simplified: use last choice
		userChoiceMap["d"] = lastChoice.Desc
		userChoiceMap["t"] = lastChoice.Text
	}
	compressedInputData["uc"] = userChoiceMap

	// Marshal the compressed data into UserInput JSON string
	userInputBytes, errMarshal := json.Marshal(compressedInputData)
	if errMarshal != nil {
		log.Printf("ERROR: Failed to marshal compressedInputData for generation task StoryID %s: %v", story.ID, errMarshal)
		return sharedMessaging.GenerationTaskPayload{}, fmt.Errorf("error marshaling input data: %w", errMarshal)
	}
	userInputJSON := string(userInputBytes)

	payload := sharedMessaging.GenerationTaskPayload{
		TaskID:           uuid.New().String(),
		UserID:           userID.String(),   // Use string UUID
		PublishedStoryID: story.ID.String(), // Use string UUID
		PromptType:       sharedMessaging.PromptTypeNovelCreator,
		UserInput:        userInputJSON, // Use the marshaled JSON string
		StateHash:        currentStateHash,
		// GameStateID is added later before publishing
	}

	return payload, nil
}

// clearTransientFlags removes flags starting with "_" from the slice.
func clearTransientFlags(flags []string) []string {
	if flags == nil {
		return nil
	}
	newFlags := make([]string, 0, len(flags))
	for _, flag := range flags {
		if !strings.HasPrefix(flag, "_") {
			newFlags = append(newFlags, flag)
		}
	}
	return newFlags
}

// createInitialSceneGenerationPayload создает payload для генерации *первой* сцены истории.
// Она использует только Config и Setup истории, без PlayerProgress.
func createInitialSceneGenerationPayload(
	userID uuid.UUID,
	story *sharedModels.PublishedStory,
) (sharedMessaging.GenerationTaskPayload, error) {

	// --- MODIFICATION START: Parse full config/setup and create minimal versions ---
	if story.Config == nil || story.Setup == nil {
		log.Printf("ERROR: Story Config or Setup is nil for initial scene generation, StoryID %s", story.ID)
		return sharedMessaging.GenerationTaskPayload{}, fmt.Errorf("story config or setup is nil")
	}
	var fullConfig sharedModels.Config
	if err := json.Unmarshal(story.Config, &fullConfig); err != nil {
		log.Printf("WARN: Failed to parse Config JSON for initial scene task StoryID %s: %v", story.ID, err)
		return sharedMessaging.GenerationTaskPayload{}, fmt.Errorf("error parsing Config JSON: %w", err)
	}
	var fullSetup sharedModels.NovelSetupContent
	if err := json.Unmarshal(story.Setup, &fullSetup); err != nil {
		log.Printf("WARN: Failed to parse Setup JSON for initial scene task StoryID %s: %v", story.ID, err)
		return sharedMessaging.GenerationTaskPayload{}, fmt.Errorf("error parsing Setup JSON: %w", err)
	}

	minimalConfig := sharedModels.ToMinimalConfigForScene(&fullConfig)
	minimalSetup := sharedModels.ToMinimalSetupForScene(&fullSetup)
	// --- MODIFICATION END ---

	// Extract initial stats from the full setup
	initialCoreStats := make(map[string]int)
	if fullSetup.CoreStatsDefinition != nil {
		for statName, definition := range fullSetup.CoreStatsDefinition {
			initialCoreStats[statName] = definition.Initial
		}
	}

	compressedInputData := make(map[string]interface{})
	compressedInputData["cfg"] = minimalConfig               // Minimal Config
	compressedInputData["stp"] = minimalSetup                // Minimal Setup
	compressedInputData["cs"] = initialCoreStats             // Initial stats
	compressedInputData["sv"] = make(map[string]interface{}) // Empty for first scene
	compressedInputData["gf"] = []string{}                   // Empty for first scene
	compressedInputData["uc"] = make(map[string]string)      // Empty user choice map for first scene
	compressedInputData["pss"] = ""                          // Empty summary for first scene
	compressedInputData["pfd"] = ""                          // Empty direction for first scene
	compressedInputData["pvis"] = ""                         // Empty var impact for first scene

	userInputBytes, errMarshal := json.Marshal(compressedInputData)
	if errMarshal != nil {
		log.Printf("ERROR: Failed to marshal compressedInputData for initial scene generation task StoryID %s: %v", story.ID, errMarshal)
		return sharedMessaging.GenerationTaskPayload{}, fmt.Errorf("error marshaling input data: %w", errMarshal)
	}
	userInputJSON := string(userInputBytes)

	payload := sharedMessaging.GenerationTaskPayload{
		TaskID:           uuid.New().String(),
		UserID:           userID.String(),                        // Use string UUID
		PublishedStoryID: story.ID.String(),                      // Use string UUID
		PromptType:       sharedMessaging.PromptTypeNovelCreator, // First scene uses NovelCreator prompt type now
		UserInput:        userInputJSON,
		StateHash:        sharedModels.InitialStateHash, // Use the constant for initial state hash
		// GameStateID is added later before publishing
	}

	return payload, nil
}

// <<< НАЧАЛО: Перемещенная вспомогательная функция >>>
// checkAndGenerateSetupImages parses the setup, checks image needs, updates flags, and publishes tasks.
func (s *gameLoopServiceImpl) checkAndGenerateSetupImages(ctx context.Context, userID string, story *sharedModels.PublishedStory, setupContent *sharedModels.NovelSetupContent) (bool, error) {
	log := s.logger.With(zap.String("publishedStoryID", story.ID.String()), zap.String("userID", userID))
	log.Info("Checking and generating setup images if needed")

	var ( // Initialize variables
		needsCharacterImages bool
		needsPreviewImage    bool
		imageTasks           = make([]sharedMessaging.CharacterImageTaskPayload, 0, len(setupContent.Characters))
		characterVisualStyle string
		storyStyle           string
		fullConfig           sharedModels.Config
	)

	if story.Config != nil {
		if errCfg := json.Unmarshal(story.Config, &fullConfig); errCfg != nil {
			log.Warn("Failed to unmarshal config JSON to get CharacterVisualStyle/Style for image generation", zap.Error(errCfg))
			// Continue without style information, prompts might be less specific
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
	} else {
		log.Warn("Story config is nil, cannot extract visual style for image generation")
	}

	log.Info("Checking which character images need generation")
	for _, charData := range setupContent.Characters {
		if charData.ImageRef == "" || charData.Prompt == "" {
			log.Debug("Skipping character image check: missing ImageRef or Prompt", zap.String("char_name", charData.Name))
			continue
		}
		imageRef := charData.ImageRef
		_, errCheck := s.imageReferenceRepo.GetImageURLByReference(ctx, imageRef)

		// <<< ИСПРАВЛЕНО: Используем sharedModels.ErrNotFound и убираем проверку по строке >>>
		if errors.Is(errCheck, sharedModels.ErrNotFound) || errors.Is(errCheck, pgx.ErrNoRows) {
			log.Debug("Character image needs generation", zap.String("image_ref", imageRef))
			needsCharacterImages = true
			characterIDForTask := uuid.New()
			fullCharacterPrompt := charData.Prompt + characterVisualStyle
			imageTask := sharedMessaging.CharacterImageTaskPayload{
				TaskID:           characterIDForTask.String(),
				UserID:           userID, // <<< ДОБАВЛЕНО: Передаем userID
				CharacterID:      characterIDForTask,
				Prompt:           fullCharacterPrompt,
				NegativePrompt:   charData.NegPrompt,
				ImageReference:   imageRef,
				Ratio:            "2:3",
				PublishedStoryID: story.ID,
			}
			imageTasks = append(imageTasks, imageTask)
		} else if errCheck != nil { // Ловим другие ошибки
			log.Error("Error checking Character ImageRef in DB", zap.String("image_ref", imageRef), zap.Error(errCheck))
		} else { // Ошибки нет
			log.Debug("Character image already exists", zap.String("image_ref", imageRef))
		}
	}

	log.Info("Checking if preview image needs generation")
	if setupContent.StoryPreviewImagePrompt != "" {
		previewImageRef := fmt.Sprintf("history_preview_%s", story.ID.String())
		_, errCheck := s.imageReferenceRepo.GetImageURLByReference(ctx, previewImageRef)

		// <<< ИСПРАВЛЕНО: Используем sharedModels.ErrNotFound и убираем проверку по строке >>>
		if errors.Is(errCheck, sharedModels.ErrNotFound) || errors.Is(errCheck, pgx.ErrNoRows) {
			log.Debug("Preview image needs generation", zap.String("image_ref", previewImageRef))
			needsPreviewImage = true
		} else if errCheck != nil { // Ловим другие ошибки
			log.Error("Error checking Preview ImageRef in DB", zap.String("image_ref", previewImageRef), zap.Error(errCheck))
		} else { // Ошибки нет
			log.Debug("Preview image already exists", zap.String("image_ref", previewImageRef))
		}
	} else {
		log.Info("StoryPreviewImagePrompt (spi) is empty in setup, no preview generation needed.")
	}

	areImagesPending := needsPreviewImage || needsCharacterImages

	if areImagesPending {
		log.Info("Updating story flags: are_images_pending=true")
		// Update only the flag, don't change status or other fields here.
		// <<< ИСПРАВЛЕНО: Передаем bool значения напрямую >>>
		if err := s.publishedRepo.UpdateStatusFlagsAndDetails(
			ctx,                       // context
			story.ID,                  // storyID
			story.Status,              // status (не меняем)
			story.IsFirstScenePending, // is_first_scene_pending (передаем текущее значение)
			areImagesPending,          // are_images_pending (передаем новое значение)
			nil,                       // error_details
		); err != nil {
			log.Error("CRITICAL ERROR: Failed to update are_images_pending flag for PublishedStory", zap.Error(err))
			// Return the error, as subsequent steps depend on this flag being correct.
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
				// Do not return error here, allow scene retry to proceed. Logged the error.
			} else {
				log.Info("Character image task batch published successfully", zap.String("batch_id", batchPayload.BatchID))
			}
		}
		if needsPreviewImage {
			previewImageRef := fmt.Sprintf("history_preview_%s", story.ID.String())
			basePreviewPrompt := setupContent.StoryPreviewImagePrompt
			fullPreviewPromptWithStyles := basePreviewPrompt + storyStyle + characterVisualStyle
			previewTask := sharedMessaging.CharacterImageTaskPayload{
				TaskID:           uuid.New().String(),
				UserID:           userID,   // <<< ДОБАВЛЕНО: Передаем userID
				CharacterID:      story.ID, // Use story ID as character ID for preview
				Prompt:           fullPreviewPromptWithStyles,
				NegativePrompt:   "",
				ImageReference:   previewImageRef,
				Ratio:            "3:2",
				PublishedStoryID: story.ID,
			}
			previewBatchPayload := sharedMessaging.CharacterImageTaskBatchPayload{BatchID: uuid.New().String(), Tasks: []sharedMessaging.CharacterImageTaskPayload{previewTask}}
			if errPub := s.characterImageTaskBatchPub.PublishCharacterImageTaskBatch(ctx, previewBatchPayload); errPub != nil {
				log.Error("Failed to publish story preview image task", zap.Error(errPub), zap.String("preview_batch_id", previewBatchPayload.BatchID))
				// Do not return error here, allow scene retry to proceed. Logged the error.
			} else {
				log.Info("Story preview image task published successfully", zap.String("preview_batch_id", previewBatchPayload.BatchID))
			}
		}
	} else {
		log.Info("No image generation needed based on Setup and existing references.")
	}

	return areImagesPending, nil // Return whether images were pending and nil error
}

// <<< КОНЕЦ: Перемещенная вспомогательная функция >>>

// UpdateSceneInternal updates the content of a scene.
func (s *gameLoopServiceImpl) UpdateSceneInternal(ctx context.Context, sceneID uuid.UUID, contentJSON string) error {
	log := s.logger.With(zap.String("sceneID", sceneID.String()))
	log.Info("UpdateSceneInternal called")

	// Валидация JSON
	var contentBytes []byte
	if contentJSON != "" {
		if !json.Valid([]byte(contentJSON)) {
			log.Warn("Invalid JSON received for scene content")
			return fmt.Errorf("%w: invalid scene content JSON format", sharedModels.ErrBadRequest)
		}
		contentBytes = []byte(contentJSON)
	} else {
		// Запрещаем делать контент пустым? Или разрешаем?
		// Пока запретим, так как пустая сцена бессмысленна.
		log.Warn("Attempted to set empty content for scene")
		return fmt.Errorf("%w: scene content cannot be empty", sharedModels.ErrBadRequest)
		// Если нужно разрешить, использовать: contentBytes = nil
	}

	// Вызов репозитория
	err := s.sceneRepo.UpdateContent(ctx, sceneID, contentBytes)
	if err != nil {
		if errors.Is(err, sharedModels.ErrNotFound) {
			log.Warn("Scene not found for update")
			return sharedModels.ErrNotFound
		}
		log.Error("Failed to update scene content in repository", zap.Error(err))
		return sharedModels.ErrInternalServer // Use shared error
	}

	log.Info("Scene content updated successfully by internal request")
	return nil
}
