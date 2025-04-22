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

// userChoiceInfo holds information about a choice made by the user.
type userChoiceInfo struct {
	Desc string `json:"d"` // Description of the choice block
	Text string `json:"t"` // Text of the chosen option
}

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
	GetStoryScene(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) (*sharedModels.StoryScene, error)
	MakeChoice(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID, selectedOptionIndices []int) error
	DeletePlayerProgress(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) error
	RetrySceneGeneration(ctx context.Context, storyID, userID uuid.UUID) error
	UpdateSceneInternal(ctx context.Context, sceneID uuid.UUID, contentJSON string) error
}

type gameLoopServiceImpl struct {
	publishedRepo      interfaces.PublishedStoryRepository
	sceneRepo          interfaces.StorySceneRepository
	playerProgressRepo interfaces.PlayerProgressRepository
	publisher          messaging.TaskPublisher
	storyConfigRepo    interfaces.StoryConfigRepository
	logger             *zap.Logger
}

// NewGameLoopService creates a new instance of GameLoopService.
func NewGameLoopService(
	publishedRepo interfaces.PublishedStoryRepository,
	sceneRepo interfaces.StorySceneRepository,
	playerProgressRepo interfaces.PlayerProgressRepository,
	publisher messaging.TaskPublisher,
	storyConfigRepo interfaces.StoryConfigRepository,
	logger *zap.Logger,
) GameLoopService {
	return &gameLoopServiceImpl{
		publishedRepo:      publishedRepo,
		sceneRepo:          sceneRepo,
		playerProgressRepo: playerProgressRepo,
		publisher:          publisher,
		storyConfigRepo:    storyConfigRepo,
		logger:             logger.Named("GameLoopService"),
	}
}

// GetStoryScene gets the current scene for the player.
func (s *gameLoopServiceImpl) GetStoryScene(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) (*sharedModels.StoryScene, error) {
	log := s.logger.With(zap.String("userID", userID.String()), zap.String("publishedStoryID", publishedStoryID.String()))
	log.Info("GetStoryScene called")

	// 1. Get the published story
	publishedStory, err := s.publishedRepo.GetByID(ctx, publishedStoryID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Warn("Published story not found for GetStoryScene")
			return nil, sharedModels.ErrStoryNotFound // Use shared error
		}
		log.Error("Error getting published story", zap.Error(err))
		return nil, sharedModels.ErrInternalServer // Use shared error
	}

	// 2. Check story status
	if publishedStory.Status == sharedModels.StatusSetupPending || publishedStory.Status == sharedModels.StatusFirstScenePending {
		log.Info("Story is not ready yet", zap.String("status", string(publishedStory.Status)))
		return nil, sharedModels.ErrStoryNotReadyYet // Use shared error
	}
	if publishedStory.Status != sharedModels.StatusReady && publishedStory.Status != sharedModels.StatusGeneratingScene {
		log.Warn("Attempt to get scene for story in invalid state", zap.String("status", string(publishedStory.Status)))
		// Use the more specific sharedModels.ErrStoryNotReady
		return nil, sharedModels.ErrStoryNotReady
	}

	// 3. Get player progress or create initial progress
	playerProgress, err := s.playerProgressRepo.GetByUserIDAndStoryID(ctx, userID, publishedStoryID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Info("Player progress not found, creating initial progress")
			// TODO: Extract initial progress creation logic if it becomes complex
			playerProgress = &sharedModels.PlayerProgress{
				UserID:           userID,
				PublishedStoryID: publishedStoryID,
				CurrentStateHash: sharedModels.InitialStateHash,
				CoreStats:        make(map[string]int),
				StoryVariables:   make(map[string]interface{}),
				GlobalFlags:      []string{},
				CreatedAt:        time.Now().UTC(),
				UpdatedAt:        time.Now().UTC(),
			}
			if errCreate := s.playerProgressRepo.CreateOrUpdate(ctx, playerProgress); errCreate != nil {
				log.Error("Error creating initial player progress", zap.Error(errCreate))
				return nil, sharedModels.ErrInternalServer // Use shared error
			}
		} else {
			log.Error("Error getting player progress", zap.Error(err))
			return nil, sharedModels.ErrInternalServer // Use shared error
		}
	}

	// <<< ДОБАВЛЕНО: Установка SceneIndex в 1 для нового прогресса >>>
	if playerProgress.SceneIndex == 0 { // Проверяем, что это действительно новый прогресс (индекс еще не установлен)
		log.Info("Setting initial scene index to 1")
		playerProgress.SceneIndex = 1
		// Не сохраняем здесь, так как CreateOrUpdate выше уже сохранил (или Get вернул существующий)
		// Если Get вернул существующий, у него уже будет индекс > 0.
	}

	// 4. Get scene by hash
	scene, err := s.sceneRepo.FindByStoryAndHash(ctx, publishedStoryID, playerProgress.CurrentStateHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Info("Scene not found for hash, requires generation", zap.String("stateHash", playerProgress.CurrentStateHash))
			return nil, sharedModels.ErrSceneNeedsGeneration // Use shared error
		}
		log.Error("Error getting scene by hash", zap.String("stateHash", playerProgress.CurrentStateHash), zap.Error(err))
		return nil, sharedModels.ErrInternalServer // Use shared error
	}

	log.Info("Scene found and returned", zap.String("sceneID", scene.ID.String()))
	return scene, nil
}

// MakeChoice handles player choice.
func (s *gameLoopServiceImpl) MakeChoice(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID, selectedOptionIndices []int) error {
	logFields := []zap.Field{
		zap.String("userID", userID.String()),
		zap.String("publishedStoryID", publishedStoryID.String()),
		zap.Any("selectedOptionIndices", selectedOptionIndices),
	}
	s.logger.Info("MakeChoice called", logFields...)

	// 1. Get the progress
	progress, err := s.playerProgressRepo.GetByUserIDAndStoryID(ctx, userID, publishedStoryID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			s.logger.Warn("Player progress not found for MakeChoice", logFields...)
			return sharedModels.ErrPlayerProgressNotFound // Use shared error
		}
		s.logger.Error("Failed to get player progress", append(logFields, zap.Error(err))...)
		return sharedModels.ErrInternalServer // Use shared error
	}

	// 2. Get the story
	publishedStory, err := s.publishedRepo.GetByID(ctx, publishedStoryID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			s.logger.Warn("Published story not found for MakeChoice", logFields...)
			return sharedModels.ErrStoryNotFound // Use shared error
		}
		s.logger.Error("Failed to get published story", append(logFields, zap.Error(err))...)
		return sharedModels.ErrInternalServer // Use shared error
	}

	// 3. Check the status of the published story
	if publishedStory.Status != sharedModels.StatusReady && publishedStory.Status != sharedModels.StatusGeneratingScene {
		s.logger.Warn("Attempt to make choice in non-ready/generating story state", append(logFields, zap.String("status", string(publishedStory.Status)))...)
		return sharedModels.ErrStoryNotReady // Use shared error
	}

	// 4. Get the current scene by hash from progress
	previousHash := progress.CurrentStateHash
	currentScene, err := s.sceneRepo.FindByStoryAndHash(ctx, publishedStoryID, previousHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			s.logger.Error("CRITICAL: Current scene not found for hash in player progress", append(logFields, zap.String("stateHash", previousHash))...)
			return sharedModels.ErrSceneNotFound // Use shared error
		}
		s.logger.Error("Failed to get current scene by hash", append(logFields, zap.String("stateHash", previousHash), zap.Error(err))...)
		return sharedModels.ErrInternalServer // Use shared error
	}

	// 5. Parse the content of the current scene
	var sceneData sceneContentChoices
	if err := json.Unmarshal(currentScene.Content, &sceneData); err != nil {
		s.logger.Error("Failed to unmarshal current scene content", append(logFields, zap.String("sceneID", currentScene.ID.String()), zap.Error(err))...)
		return sharedModels.ErrInternalServer
	}
	if sceneData.Type != "choices" {
		s.logger.Error("Scene content is not of type 'choices'", append(logFields, zap.String("sceneID", currentScene.ID.String()), zap.String("type", sceneData.Type))...)
		return sharedModels.ErrInternalServer // Indicate internal inconsistency
	}
	if len(sceneData.Choices) == 0 {
		s.logger.Error("Scene content has type 'choices' but no choices array", append(logFields, zap.String("sceneID", currentScene.ID.String()))...)
		return sharedModels.ErrNoChoicesAvailable // Use shared error
	}

	// 6. Validate inputs count match
	if len(sceneData.Choices) != len(selectedOptionIndices) {
		s.logger.Error("Mismatch between number of choices in scene and choices made by player",
			append(logFields, zap.Int("sceneChoicesCount", len(sceneData.Choices)), zap.Int("playerChoicesCount", len(selectedOptionIndices)))...)
		return fmt.Errorf("%w: number of choices provided (%d) does not match expected (%d)",
			sharedModels.ErrBadRequest, len(selectedOptionIndices), len(sceneData.Choices)) // Use shared BadRequest
	}

	// 7. Load NovelSetup
	if publishedStory.Setup == nil {
		s.logger.Error("CRITICAL: PublishedStory Setup is nil, but scene exists and status is Ready/Generating", append(logFields, zap.String("status", string(publishedStory.Status)))...)
		return sharedModels.ErrInternalServer
	}
	var setupContent sharedModels.NovelSetupContent
	if err := json.Unmarshal(publishedStory.Setup, &setupContent); err != nil {
		s.logger.Error("Failed to unmarshal NovelSetup content", append(logFields, zap.Error(err))...)
		return sharedModels.ErrInternalServer
	}

	// 8. Apply consequences for EACH choice in the batch
	var isGameOver bool
	var gameOverStat string
	madeChoicesInfo := make([]userChoiceInfo, 0, len(sceneData.Choices))

	for i, choiceBlock := range sceneData.Choices {
		selectedIndex := selectedOptionIndices[i]

		if selectedIndex < 0 || selectedIndex >= len(choiceBlock.Options) {
			s.logger.Warn("Invalid selected option index for choice block",
				append(logFields, zap.Int("choiceBlockIndex", i), zap.Int("selectedIndex", selectedIndex), zap.Int("optionsAvailable", len(choiceBlock.Options)))...)
			return fmt.Errorf("%w: invalid selected_option_index %d for choice block %d",
				sharedModels.ErrInvalidChoice, selectedIndex, i) // Use shared InvalidChoice error
		}

		selectedOption := choiceBlock.Options[selectedIndex]
		madeChoicesInfo = append(madeChoicesInfo, userChoiceInfo{Desc: choiceBlock.Description, Text: selectedOption.Text})

		statCausingGameOver, gameOverTriggered := applyConsequences(progress, selectedOption.Consequences, &setupContent)
		if gameOverTriggered {
			isGameOver = true
			gameOverStat = statCausingGameOver
			s.logger.Info("Game Over condition met during batch processing", append(logFields, zap.Int("choiceBlockIndex", i), zap.String("gameOverStat", gameOverStat))...)
			break
		}
	}

	if isGameOver {
		s.logger.Info("Handling Game Over after batch processing", append(logFields, zap.String("gameOverStat", gameOverStat))...)
		if err := s.publishedRepo.UpdateStatusDetails(ctx, publishedStoryID, sharedModels.StatusGameOverPending, nil, nil, nil, nil); err != nil {
			s.logger.Error("Failed to update published story status to GameOverPending", append(logFields, zap.Error(err))...)
			// Continue to publish task even if status update fails, but log error
		}
		progress.UpdatedAt = time.Now().UTC()
		if err := s.playerProgressRepo.CreateOrUpdate(ctx, progress); err != nil {
			s.logger.Error("Failed to save final player progress before game over", append(logFields, zap.Error(err))...)
			return sharedModels.ErrInternalServer
		}

		taskID := uuid.New().String()
		var reasonCondition string
		finalValue := progress.CoreStats[gameOverStat]
		if def, ok := setupContent.CoreStatsDefinition[gameOverStat]; ok {
			// Determine reason based on which flag is set in GameOverConditions
			if def.GameOverConditions.Min { // Check the Min flag
				// Assume if Min flag is true, game over *could* be due to min value.
				// We don't have the exact boundary here, rely on applyConsequences having triggered isGameOver.
				// If both Min and Max flags are true, this might not be the precise reason.
				reasonCondition = "min"
			} else if def.GameOverConditions.Max { // Check the Max flag if Min wasn't the reason (or wasn't set)
				reasonCondition = "max"
			}
			// If neither flag is set, reasonCondition remains "", which might indicate an issue.
		}
		reason := sharedMessaging.GameOverReason{
			StatName:  gameOverStat,
			Condition: reasonCondition,
			Value:     finalValue,
		}
		var novelConfig sharedModels.Config
		if err := json.Unmarshal(publishedStory.Config, &novelConfig); err != nil {
			s.logger.Error("Failed to unmarshal novel config for game over task", append(logFields, zap.Error(err))...)
			return sharedModels.ErrInternalServer
		}

		gameOverPayload := sharedMessaging.GameOverTaskPayload{
			TaskID:           taskID,
			UserID:           userID.String(),
			PublishedStoryID: publishedStoryID.String(),
			LastState:        *progress,
			Reason:           reason,
			NovelConfig:      novelConfig,
			NovelSetup:       setupContent,
		}
		if err := s.publisher.PublishGameOverTask(ctx, gameOverPayload); err != nil {
			s.logger.Error("Failed to publish game over generation task", append(logFields, zap.Error(err))...)
			return sharedModels.ErrInternalServer
		}
		s.logger.Info("Game over task published", append(logFields, zap.String("taskID", taskID))...)
		return nil // Game over handled
	}

	// 9. Calculate next state hash
	newStateHash, err := calculateStateHash(previousHash, progress.CoreStats, progress.StoryVariables, progress.GlobalFlags)
	if err != nil {
		s.logger.Error("Failed to calculate new state hash after batch processing", append(logFields, zap.Error(err))...)
		return sharedModels.ErrInternalServer
	}
	logFields = append(logFields, zap.String("newStateHash", newStateHash))
	s.logger.Debug("New state hash calculated after batch", logFields...)

	progress.CurrentStateHash = newStateHash
	progress.UpdatedAt = time.Now().UTC()

	// <<< ДОБАВЛЕНО: Увеличиваем индекс сцены >>>
	progress.SceneIndex++
	s.logger.Info("Incremented scene index", append(logFields, zap.Int("newSceneIndex", progress.SceneIndex))...)

	// 10. Check if the next scene already exists
	nextScene, err := s.sceneRepo.FindByStoryAndHash(ctx, publishedStoryID, newStateHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			s.logger.Info("Next scene not found, publishing generation task after batch", logFields...)
			if errStatus := s.publishedRepo.UpdateStatusDetails(ctx, publishedStoryID, sharedModels.StatusGeneratingScene, nil, nil, nil, nil); errStatus != nil {
				s.logger.Error("Failed to update published story status to GeneratingScene", append(logFields, zap.Error(errStatus))...)
				// Continue to publish task even if status update fails
			}

			generationPayload, errGenPayload := createGenerationPayload(
				userID,
				publishedStory,
				progress,
				madeChoicesInfo,
				newStateHash,
			)
			if errGenPayload != nil {
				s.logger.Error("Failed to create generation payload after batch", append(logFields, zap.Error(errGenPayload))...)
				return sharedModels.ErrInternalServer
			}

			if errPub := s.publisher.PublishGenerationTask(ctx, generationPayload); errPub != nil {
				s.logger.Error("Failed to publish next scene generation task after batch", append(logFields, zap.Error(errPub))...)
				return sharedModels.ErrInternalServer
			}
			s.logger.Info("Next scene generation task published after batch", append(logFields, zap.String("taskID", generationPayload.TaskID))...)

			s.logger.Debug("Clearing StoryVariables and GlobalFlags before saving progress after generation task published", logFields...)
			progress.StoryVariables = make(map[string]interface{}) // Clear transient variables
			progress.GlobalFlags = clearTransientFlags(progress.GlobalFlags)

			if err := s.playerProgressRepo.CreateOrUpdate(ctx, progress); err != nil {
				s.logger.Error("Ошибка сохранения обновленного PlayerProgress после запуска генерации (батч)", append(logFields, zap.Error(err))...)
				return sharedModels.ErrInternalServer
			}
			s.logger.Info("PlayerProgress (с очищенными sv/gf) успешно обновлен после запуска генерации (батч)", logFields...)

			return nil // Scene generation initiated
		} else {
			s.logger.Error("Error searching for next scene after batch", append(logFields, zap.Error(err))...)
			return sharedModels.ErrInternalServer
		}
	}

	// 11. Next scene found in DB
	s.logger.Info("Next scene found in DB after batch", append(logFields, zap.String("nextSceneID", nextScene.ID.String()))...)

	// Update progress with summaries from the found scene (if available)
	type SceneOutputFormat struct { // Consider moving this definition if reused
		Sssf string `json:"sssf"`
		Fd   string `json:"fd"`
		Vis  string `json:"vis"`
	}
	var sceneOutput SceneOutputFormat
	if errUnmarshal := json.Unmarshal(nextScene.Content, &sceneOutput); errUnmarshal != nil {
		s.logger.Warn("Failed to unmarshal next scene content to get summaries after batch",
			append(logFields, zap.String("nextSceneID", nextScene.ID.String()), zap.Error(errUnmarshal))...)
		// Proceed without updating summaries if unmarshal fails
	} else {
		progress.LastStorySummary = &sceneOutput.Sssf
		progress.LastFutureDirection = &sceneOutput.Fd
		progress.LastVarImpactSummary = &sceneOutput.Vis
	}

	s.logger.Debug("Clearing StoryVariables and GlobalFlags before saving progress after next scene found", logFields...)
	progress.StoryVariables = make(map[string]interface{}) // Clear transient variables
	progress.GlobalFlags = clearTransientFlags(progress.GlobalFlags)

	if err := s.playerProgressRepo.CreateOrUpdate(ctx, progress); err != nil {
		s.logger.Error("Ошибка сохранения обновленного PlayerProgress после нахождения след. сцены (батч)", append(logFields, zap.Error(err))...)
		return sharedModels.ErrInternalServer
	}
	s.logger.Info("PlayerProgress (с очищенными sv/gf и новыми сводками) успешно обновлен после нахождения след. сцены (батч)", logFields...)

	return nil // Choice processed, next scene exists
}

// DeletePlayerProgress deletes player progress for the specified story.
func (s *gameLoopServiceImpl) DeletePlayerProgress(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) error {
	log := s.logger.With(zap.String("userID", userID.String()), zap.String("publishedStoryID", publishedStoryID.String()))
	log.Info("Deleting player progress")

	// Optional: Check if story exists first? publishedRepo.Exists(ctx, publishedStoryID)
	// Delete operation might fail anyway if story FK is enforced, but checking
	// first could provide a clearer ErrStoryNotFound vs. ErrPlayerProgressNotFound.

	err := s.playerProgressRepo.Delete(ctx, userID, publishedStoryID)
	if err != nil {
		// Check if the error is specifically that progress didn't exist
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, sharedModels.ErrPlayerProgressNotFound) { // Check for repo-specific or shared error
			log.Warn("Player progress not found, nothing to delete")
			return sharedModels.ErrPlayerProgressNotFound // Return consistent error
		}
		// Log other DB errors
		log.Error("Error deleting player progress from repository", zap.Error(err))
		return sharedModels.ErrInternalServer // Return generic internal error
	}

	log.Info("Player progress deleted successfully")
	return nil
}

// RetrySceneGeneration handles the logic for retrying generation for a published story.
// It checks if the error occurred during Setup or Scene generation and restarts the appropriate task.
func (s *gameLoopServiceImpl) RetrySceneGeneration(ctx context.Context, storyID, userID uuid.UUID) error {
	log := s.logger.With(zap.String("publishedStoryID", storyID.String()), zap.String("userID", userID.String()))
	log.Info("RetrySceneGeneration called")

	// <<< ДОБАВЛЕНО: Проверка лимита активных генераций >>>
	activeCount, err := s.storyConfigRepo.CountActiveGenerations(ctx, userID)
	if err != nil {
		log.Error("Error counting active generations before RetrySceneGeneration", zap.Error(err))
		return sharedModels.ErrInternalServer // Внутренняя ошибка
	}
	generationLimit := 1 // TODO: Сделать лимит конфигурируемым
	if activeCount >= generationLimit {
		log.Warn("User reached the active generation limit, RetrySceneGeneration rejected", zap.Int("limit", generationLimit))
		return sharedModels.ErrUserHasActiveGeneration
	}
	// <<< КОНЕЦ ДОБАВЛЕНИЯ >>>

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
	// <<< ИЗМЕНЕНО: Улучшенная проверка на существование Setup >>>
	// Считаем Setup отсутствующим, если он nil ИЛИ содержит JSON 'null'.
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

		// Get player progress to determine which scene to retry
		progress, err := s.playerProgressRepo.GetByUserIDAndStoryID(ctx, userID, storyID)
		if err != nil {
			if errors.Is(err, sharedModels.ErrNotFound) {
				log.Warn("Player progress not found for scene retry. Assuming retry for initial scene.")
				// <<< НАЧАЛО ИЗМЕНЕНИЯ: Вызываем createInitialSceneGenerationPayload >>>
				generationPayload, errPayload := createInitialSceneGenerationPayload(userID, story)
				if errPayload != nil {
					log.Error("Failed to create initial generation payload for scene retry", zap.Error(errPayload))
					// Пытаемся откатить статус обратно в Error
					if rollbackErr := s.publishedRepo.UpdateStatusDetails(context.Background(), storyID, sharedModels.StatusError, nil, nil, nil, nil); rollbackErr != nil {
						log.Error("CRITICAL: Failed to roll back status to Error after initial payload creation error", zap.Error(rollbackErr))
					}
					return sharedModels.ErrInternalServer
				}
				// Статус уже должен быть Error, меняем на GeneratingScene перед публикацией
				if err := s.publishedRepo.UpdateStatusDetails(ctx, storyID, sharedModels.StatusGeneratingScene, nil, nil, nil, nil); err != nil {
					log.Error("Failed to update story status to GeneratingScene before initial retry task publish", zap.Error(err))
					return sharedModels.ErrInternalServer
				}
				// Публикуем задачу для начальной сцены
				if errPub := s.publisher.PublishGenerationTask(ctx, generationPayload); errPub != nil {
					log.Error("Error publishing initial scene retry generation task. Rolling back status...", zap.Error(errPub))
					if rollbackErr := s.publishedRepo.UpdateStatusDetails(context.Background(), storyID, sharedModels.StatusError, nil, nil, nil, nil); rollbackErr != nil {
						log.Error("CRITICAL: Failed to roll back status to Error after initial scene retry publish error", zap.Error(rollbackErr))
					}
					return sharedModels.ErrInternalServer
				}
				log.Info("Initial scene retry generation task published successfully", zap.String("taskID", generationPayload.TaskID), zap.String("stateHash", generationPayload.StateHash))
				return nil // Задача для начальной сцены отправлена
				// <<< КОНЕЦ ИЗМЕНЕНИЯ >>>
			} else {
				// Другая ошибка при получении прогресса - это фатально
				log.Error("Failed to get player progress for scene retry", zap.Error(err))
				return sharedModels.ErrInternalServer
			}
		}

		// Прогресс найден, продолжаем стандартную логику Retry для существующего progress
		// Update status back to GeneratingScene
		if err := s.publishedRepo.UpdateStatusDetails(ctx, storyID, sharedModels.StatusGeneratingScene, nil, nil, nil, nil); err != nil {
			log.Error("Failed to update story status to GeneratingScene before retry task publish", zap.Error(err))
			return sharedModels.ErrInternalServer
		}

		// Create payload for the scene indicated by progress.CurrentStateHash
		madeChoicesInfo := []userChoiceInfo{} // No choice info on a simple retry
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
		return ErrInternal
	}

	log.Info("Scene content updated successfully by internal request")
	return nil
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
			// TODO: This assumes that if isGameOver is true, one of the conditions was met.
			// This might be inaccurate if a stat could theoretically go below min AND above max simultaneously,
			// or if game over is triggered by other means not reflected in GameOverConditions.
			// Also, we no longer have the numeric bounds (Min/Max) here directly.
			// We rely on the fact that applyConsequences correctly determined isGameOver based on those bounds (or other rules).

			// Check Min condition IF it's a game over condition
			if definition.GameOverConditions.Min && currentValue <= 0 { // TODO: Hardcoded 0, need real min bound if logic requires it beyond just the flag
				return statName, true
			}
			// Check Max condition IF it's a game over condition
			if definition.GameOverConditions.Max && currentValue >= 100 { // TODO: Hardcoded 100, need real max bound if logic requires it beyond just the flag
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
	madeChoicesInfo []userChoiceInfo,
	currentStateHash string,
) (sharedMessaging.GenerationTaskPayload, error) {

	var configMap map[string]interface{}
	if len(story.Config) > 0 {
		if err := json.Unmarshal(story.Config, &configMap); err != nil {
			// Log warning but don't necessarily fail, maybe generation can proceed without full config?
			log.Printf("WARN: Failed to parse Config JSON for generation task StoryID %s: %v", story.ID, err)
			// return sharedMessaging.GenerationTaskPayload{}, fmt.Errorf("error parsing Config JSON: %w", err)
			configMap = make(map[string]interface{}) // Provide empty map
		}
	} else {
		log.Printf("WARN: Missing Config in PublishedStory ID %s for generation task", story.ID)
		configMap = make(map[string]interface{}) // Provide empty map
	}

	var setupMap map[string]interface{}
	if len(story.Setup) > 0 {
		if err := json.Unmarshal(story.Setup, &setupMap); err != nil {
			log.Printf("WARN: Failed to parse Setup JSON for generation task StoryID %s: %v", story.ID, err)
			// return sharedMessaging.GenerationTaskPayload{}, fmt.Errorf("error parsing Setup JSON: %w", err)
			setupMap = make(map[string]interface{}) // Provide empty map
		}
	} else {
		log.Printf("WARN: Missing Setup in PublishedStory ID %s for generation task", story.ID)
		setupMap = make(map[string]interface{}) // Provide empty map
	}

	compressedInputData := make(map[string]interface{})

	// --- Essential Data ---
	compressedInputData["cfg"] = configMap // Story config (parsed JSON, БЕЗ core_stats)
	compressedInputData["stp"] = setupMap  // Story setup (parsed JSON)

	// --- Current State (before this choice) ---
	if progress.CoreStats != nil {
		// Filter core stats if needed (e.g., remove zero values?)
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
	compressedInputData["pss"] = progress.LastStorySummary
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
	compressedInputData["uc"] = madeChoicesInfo

	// <<< ИЗМЕНЕНИЕ: Сериализуем всё в UserInput >>>
	userInputBytes, errMarshal := json.Marshal(compressedInputData)
	if errMarshal != nil {
		log.Printf("ERROR: Failed to marshal compressedInputData for generation task StoryID %s: %v", story.ID, errMarshal)
		return sharedMessaging.GenerationTaskPayload{}, fmt.Errorf("error marshaling input data: %w", errMarshal)
	}
	userInputJSON := string(userInputBytes)
	// <<< КОНЕЦ ИЗМЕНЕНИЯ >>>

	payload := sharedMessaging.GenerationTaskPayload{
		TaskID:           uuid.New().String(),
		UserID:           userID.String(),
		PublishedStoryID: story.ID.String(),
		PromptType:       sharedMessaging.PromptTypeNovelCreator,
		UserInput:        userInputJSON,    // <-- Передаем весь JSON сюда
		StateHash:        currentStateHash, // <<< Используем переданный хеш
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

	// Парсинг Config
	var configMap map[string]interface{}
	if len(story.Config) > 0 {
		if err := json.Unmarshal(story.Config, &configMap); err != nil {
			log.Printf("WARN: Failed to parse Config JSON for initial scene generation task StoryID %s: %v", story.ID, err)
			configMap = make(map[string]interface{}) // Пустая карта при ошибке
		}
	} else {
		log.Printf("WARN: Missing Config in PublishedStory ID %s for initial scene generation task", story.ID)
		configMap = make(map[string]interface{}) // Пустая карта
	}

	// Парсинг Setup и извлечение начальных статов
	var setupMap map[string]interface{}
	initialCoreStats := make(map[string]int)
	if len(story.Setup) > 0 {
		var setupContent sharedModels.NovelSetupContent
		if err := json.Unmarshal(story.Setup, &setupContent); err != nil {
			log.Printf("WARN: Failed to parse Setup JSON for initial scene generation task StoryID %s: %v", story.ID, err)
			setupMap = make(map[string]interface{}) // Пустая карта при ошибке
		} else {
			// Успешно распарсили Setup, извлекаем начальные статы
			setupMap = make(map[string]interface{}) // Создаем setupMap для передачи
			errMarshal := json.Unmarshal(story.Setup, &setupMap)
			if errMarshal != nil { // Доп. проверка на маршалинг в map
				log.Printf("WARN: Failed to marshal parsed Setup back to map for initial scene generation task StoryID %s: %v", story.ID, errMarshal)
				setupMap = make(map[string]interface{})
			}

			if setupContent.CoreStatsDefinition != nil {
				for statName, definition := range setupContent.CoreStatsDefinition {
					initialCoreStats[statName] = definition.Initial // Используем начальное значение из Setup
				}
			}
		}
	} else {
		log.Printf("WARN: Missing Setup in PublishedStory ID %s for initial scene generation task", story.ID)
		setupMap = make(map[string]interface{}) // Пустая карта
	}

	compressedInputData := make(map[string]interface{})
	compressedInputData["cfg"] = configMap
	compressedInputData["stp"] = setupMap
	compressedInputData["cs"] = initialCoreStats             // Начальные статы
	compressedInputData["sv"] = make(map[string]interface{}) // Пусто
	compressedInputData["gf"] = []string{}                   // Пусто
	compressedInputData["uc"] = []userChoiceInfo{}           // Пусто
	compressedInputData["pss"] = ""                          // Пусто
	compressedInputData["pfd"] = ""                          // Пусто
	compressedInputData["pvis"] = ""                         // Пусто

	userInputBytes, errMarshal := json.Marshal(compressedInputData)
	if errMarshal != nil {
		log.Printf("ERROR: Failed to marshal compressedInputData for initial scene generation task StoryID %s: %v", story.ID, errMarshal)
		return sharedMessaging.GenerationTaskPayload{}, fmt.Errorf("error marshaling input data: %w", errMarshal)
	}
	userInputJSON := string(userInputBytes)

	payload := sharedMessaging.GenerationTaskPayload{
		TaskID:           uuid.New().String(),
		UserID:           userID.String(), // UserID все еще нужен для идентификации задачи
		PublishedStoryID: story.ID.String(),
		PromptType:       sharedMessaging.PromptTypeNovelCreator, // Первая сцена тоже генерируется им
		UserInput:        userInputJSON,
		StateHash:        sharedModels.InitialStateHash, // Явно указываем хеш начальной сцены
	}

	return payload, nil
}
