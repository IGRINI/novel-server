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

	"novel-server/gameplay-service/internal/config" // <<< ИЗМЕНЕНО: Правильный путь к конфигу >>>

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5" // Needed for unique constraint error check
	"go.uber.org/zap"
)

// --- Structs specific to Game Loop logic ---

// sceneContentChoices represents the expected structure for scene content of type "choices".
type sceneContentChoices struct {
	Choices []sceneChoice `json:"ch"`
}

// sceneChoice represents a block of choices within a scene.
type sceneChoice struct {
	Description string        `json:"desc"`
	Options     []sceneOption `json:"opts"` // Expecting exactly 2 options
	Char        string        `json:"char"`
}

// sceneOption represents a single option within a choice block.
type sceneOption struct {
	Text         string                    `json:"txt"`
	Consequences sharedModels.Consequences `json:"cons"`
}

// --- GameLoopService Interface and Implementation ---

// GameLoopService defines the interface for core gameplay interactions.
type GameLoopService interface {
	// GetStoryScene retrieves the scene associated with a specific game state ID.
	GetStoryScene(ctx context.Context, userID uuid.UUID, gameStateID uuid.UUID) (*sharedModels.StoryScene, error)

	// MakeChoice applies player choices to a specific game state.
	MakeChoice(ctx context.Context, userID uuid.UUID, gameStateID uuid.UUID, selectedOptionIndices []int) error

	// ListGameStates lists all active game states (save slots) for a player and a story.
	ListGameStates(ctx context.Context, playerID uuid.UUID, publishedStoryID uuid.UUID) ([]*sharedModels.PlayerGameState, error)

	// CreateNewGameState creates a new save slot (game state) for a player and a story.
	// Returns an error if the player exceeds their save slot limit (TODO: implement limit check).
	CreateNewGameState(ctx context.Context, playerID uuid.UUID, publishedStoryID uuid.UUID) (*sharedModels.PlayerGameState, error)

	// DeletePlayerGameState deletes a specific game state (save slot) by its ID.
	DeletePlayerGameState(ctx context.Context, userID uuid.UUID, gameStateID uuid.UUID) error

	// RetryGenerationForGameState handles retrying generation for a specific game state.
	// It determines if setup or scene generation failed and triggers the appropriate task.
	RetryGenerationForGameState(ctx context.Context, userID, storyID, gameStateID uuid.UUID) error // <<< НОВЫЙ МЕТОД >>>

	// UpdateSceneInternal updates the content of a specific scene (internal admin func).
	UpdateSceneInternal(ctx context.Context, sceneID uuid.UUID, contentJSON string) error

	// GetOrCreatePlayerGameState - DEPRECATED, use CreateNewGameState and ListGameStates.
	// GetOrCreatePlayerGameState(ctx context.Context, playerID, storyID uuid.UUID) (*sharedModels.PlayerGameState, error)

	// GetPlayerProgress retrieves the progress node linked to a specific game state ID.
	GetPlayerProgress(ctx context.Context, userID uuid.UUID, gameStateID uuid.UUID) (*sharedModels.PlayerProgress, error)

	// DeleteSceneInternal deletes a scene (internal admin func).
	DeleteSceneInternal(ctx context.Context, sceneID uuid.UUID) error

	// UpdatePlayerProgressInternal updates a specific progress node (internal func).
	UpdatePlayerProgressInternal(ctx context.Context, progressID uuid.UUID, progressData map[string]interface{}) error

	// DeleteAllPlayerGameStatesForStory deletes all game states for a specific player and story.
	// DeleteAllPlayerGameStatesForStory(ctx context.Context, playerID uuid.UUID, publishedStoryID uuid.UUID) error // <<< УЖЕ ЗАКОММЕНТИРОВАНО, УДАЛЯЕМ

	// RetryInitialGeneration handles retrying generation for a published story's Setup or Initial Scene.
	RetryInitialGeneration(ctx context.Context, userID, storyID uuid.UUID) error // <<< НОВЫЙ МЕТОД >>>
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
	dynamicConfigRepo          interfaces.DynamicConfigRepository // <<< ДОБАВЛЕНО: Репозиторий динамических настроек >>>
	logger                     *zap.Logger
	cfg                        *config.Config // <<< ДОБАВЛЕНО: Поле для конфига (тип теперь правильный) >>>
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
	dynamicConfigRepo interfaces.DynamicConfigRepository, // <<< ДОБАВЛЕНО >>>
	logger *zap.Logger,
	cfg *config.Config, // <<< ДОБАВЛЕНО: Принимаем конфиг (тип теперь правильный) >>>
) GameLoopService {
	if cfg == nil {
		// Можно установить значения по умолчанию или запаниковать
		panic("cfg cannot be nil for NewGameLoopService") // Паникуем, т.к. суффиксы важны
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
		dynamicConfigRepo:          dynamicConfigRepo, // <<< ДОБАВЛЕНО >>>
		logger:                     logger.Named("GameLoopService"),
		cfg:                        cfg,
	}
}

// GetStoryScene gets the current scene for the player based on their game state ID.
func (s *gameLoopServiceImpl) GetStoryScene(ctx context.Context, userID uuid.UUID, gameStateID uuid.UUID) (*sharedModels.StoryScene, error) {
	log := s.logger.With(zap.String("gameStateID", gameStateID.String()), zap.Stringer("userID", userID))
	log.Info("GetStoryScene called")

	// 1. Get Player Game State by ID
	gameState, errState := s.playerGameStateRepo.GetByID(ctx, gameStateID)
	if errState != nil {
		if errors.Is(errState, sharedModels.ErrNotFound) || errors.Is(errState, pgx.ErrNoRows) {
			log.Warn("Player game state not found by ID")
			return nil, sharedModels.ErrPlayerGameStateNotFound // Use specific error
		}
		log.Error("Failed to get player game state by ID", zap.Error(errState))
		return nil, sharedModels.ErrInternalServer // Default to internal error
	}

	// <<< ДОБАВЛЕНО: Проверка владельца gameState >>>
	if gameState.PlayerID != userID {
		log.Warn("User attempted to access game state they do not own", zap.Stringer("ownerID", gameState.PlayerID))
		return nil, sharedModels.ErrForbidden // Запрещаем доступ к чужому состоянию
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
	// <<< ИЗМЕНЕНО: Проверяем Valid для NullUUID >>>
	if !gameState.CurrentSceneID.Valid {
		log.Error("CRITICAL: PlayerStatus is Playing, but CurrentSceneID is NULL", zap.String("gameStateID", gameState.ID.String()))
		// This indicates an inconsistent state. Maybe the initial scene wasn't found?
		// Or the scene link wasn't updated after generation.
		return nil, sharedModels.ErrSceneNotFound // Or ErrInternalServer?
	}

	// 4. Fetch the scene by its ID
	// <<< ИЗМЕНЕНО: Используем .UUID из NullUUID >>>
	scene, errScene := s.sceneRepo.GetByID(ctx, gameState.CurrentSceneID.UUID)
	if errScene != nil {
		if errors.Is(errScene, pgx.ErrNoRows) || errors.Is(errScene, sharedModels.ErrNotFound) {
			// <<< ИЗМЕНЕНО: Используем .UUID.String() для логирования >>>
			log.Error("CRITICAL: CurrentSceneID from game state not found in scene repository", zap.String("sceneID", gameState.CurrentSceneID.UUID.String()))
			return nil, sharedModels.ErrSceneNotFound // Scene linked in state doesn't exist
		}
		// <<< ИЗМЕНЕНО: Используем .UUID.String() для логирования >>>
		log.Error("Error getting scene by ID from repository", zap.String("sceneID", gameState.CurrentSceneID.UUID.String()), zap.Error(errScene))
		return nil, sharedModels.ErrInternalServer
	}

	log.Info("Scene found and returned", zap.String("sceneID", scene.ID.String()))
	return scene, nil
}

// MakeChoice handles player choice, updates game state, and triggers next scene/game over generation.
func (s *gameLoopServiceImpl) MakeChoice(ctx context.Context, userID uuid.UUID, gameStateID uuid.UUID, selectedOptionIndices []int) error {
	logFields := []zap.Field{
		zap.String("gameStateID", gameStateID.String()),
		zap.Stringer("userID", userID), // <<< Добавлено userID в логи
		zap.Any("selectedOptionIndices", selectedOptionIndices),
	}
	s.logger.Info("MakeChoice called", logFields...)

	// 1. Get Player Game State by ID
	gameState, errState := s.playerGameStateRepo.GetByID(ctx, gameStateID)
	if errState != nil {
		if errors.Is(errState, sharedModels.ErrNotFound) || errors.Is(errState, pgx.ErrNoRows) {
			s.logger.Warn("Player game state not found for MakeChoice")
			return sharedModels.ErrPlayerGameStateNotFound
		}
		s.logger.Error("Failed to get player game state for MakeChoice", zap.Error(errState))
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
	// <<< ИЗМЕНЕНО: Сравниваем uuid.UUID с uuid.Nil >>>
	if gameState.PlayerProgressID == uuid.Nil {
		s.logger.Error("CRITICAL: PlayerStatus is Playing, but PlayerProgressID is Nil", append(logFields, zap.String("gameStateID", gameState.ID.String()))...)
		return sharedModels.ErrInternalServer
	}
	// <<< ИЗМЕНЕНО: Проверяем Valid для NullUUID >>>
	if !gameState.CurrentSceneID.Valid {
		s.logger.Error("CRITICAL: PlayerStatus is Playing, but CurrentSceneID is Nil", append(logFields, zap.String("gameStateID", gameState.ID.String()))...)
		return sharedModels.ErrSceneNotFound
	}

	currentProgressID := gameState.PlayerProgressID
	currentSceneID := gameState.CurrentSceneID.UUID
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
	publishedStory, errStory := s.publishedRepo.GetByID(ctx, gameState.PublishedStoryID)
	if errStory != nil {
		s.logger.Error("Failed to get published story associated with game state", append(logFields, zap.Error(errStory))...)
		return sharedModels.ErrInternalServer
	}

	// 7. Parse scene content and validate choice
	var sceneData sceneContentChoices
	if err := json.Unmarshal(currentScene.Content, &sceneData); err != nil {
		s.logger.Error("Failed to unmarshal current scene content", append(logFields, zap.Error(err))...)
		return sharedModels.ErrInternalServer
	}
	// <<< УДАЛЕНО: Проверка на точное совпадение количества выборов >>>
	/*
		if len(sceneData.Choices) != len(selectedOptionIndices) {
			s.logger.Warn("Mismatch between number of choices in scene and choices made", append(logFields, zap.Int("sceneChoices", len(sceneData.Choices)), zap.Int("playerChoices", len(selectedOptionIndices)))...)
			return fmt.Errorf("%w: expected %d choice indices, got %d", sharedModels.ErrBadRequest, len(sceneData.Choices), len(selectedOptionIndices))
		}
	*/

	// <<< ДОБАВЛЕНО: Проверка, что выбор игрока не пустой и не превышает кол-во блоков >>>
	if len(selectedOptionIndices) == 0 {
		s.logger.Warn("Player sent empty choice array", logFields...)
		return fmt.Errorf("%w: selected_option_indices cannot be empty", sharedModels.ErrBadRequest)
	}
	if len(selectedOptionIndices) > len(sceneData.Choices) {
		s.logger.Warn("Player sent more choices than available in the scene", append(logFields, zap.Int("sceneChoices", len(sceneData.Choices)), zap.Int("playerChoices", len(selectedOptionIndices)))...)
		return fmt.Errorf("%w: received %d choice indices, but scene only has %d choice blocks", sharedModels.ErrBadRequest, len(selectedOptionIndices), len(sceneData.Choices))
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
		UserID:                currentProgress.UserID,
		PublishedStoryID:      currentProgress.PublishedStoryID,
		CoreStats:             make(map[string]int),
		StoryVariables:        make(map[string]interface{}),
		GlobalFlags:           make([]string, 0, len(currentProgress.GlobalFlags)),
		SceneIndex:            currentProgress.SceneIndex + 1,
		EncounteredCharacters: make([]string, 0, len(currentProgress.EncounteredCharacters)), // <<< ДОБАВЛЕНО: Инициализация для копирования
	}
	for k, v := range currentProgress.CoreStats {
		nextProgress.CoreStats[k] = v
	}
	for k, v := range currentProgress.StoryVariables {
		nextProgress.StoryVariables[k] = v
	}
	nextProgress.GlobalFlags = append(nextProgress.GlobalFlags, currentProgress.GlobalFlags...)
	nextProgress.EncounteredCharacters = append(nextProgress.EncounteredCharacters, currentProgress.EncounteredCharacters...) // <<< ДОБАВЛЕНО: Копирование существующих

	// 10. Apply consequences
	var isGameOver bool
	var gameOverStat string
	madeChoicesInfo := make([]sharedModels.UserChoiceInfo, 0, len(selectedOptionIndices)) // Используем длину выбора игрока
	// <<< ИЗМЕНЕНО: Итерируем по выбору игрока, а не по всем блокам сцены >>>
	for i, selectedIndex := range selectedOptionIndices {
		// Безопасный доступ к блоку выбора
		if i >= len(sceneData.Choices) {
			// Эта проверка уже сделана выше, но для надежности
			s.logger.Error("Logic error: index out of bounds accessing sceneData.Choices", append(logFields, zap.Int("index", i), zap.Int("sceneChoicesLen", len(sceneData.Choices)))...)
			return sharedModels.ErrInternalServer
		}
		choiceBlock := sceneData.Choices[i]

		// <<< ИЗМЕНЕНО: selectedIndex уже есть из цикла range >>>
		// selectedIndex := selectedOptionIndices[i] // УДАЛЕНО

		// Валидация индекса опции внутри блока
		if selectedIndex < 0 || selectedIndex >= len(choiceBlock.Options) {
			s.logger.Warn("Invalid selected option index for choice block", append(logFields, zap.Int("choiceBlockIndex", i), zap.Int("selectedIndex", selectedIndex), zap.Int("optionsAvailable", len(choiceBlock.Options)))...)
			return fmt.Errorf("%w: invalid index %d for choice block %d (options: %d)", sharedModels.ErrInvalidChoice, selectedIndex, i, len(choiceBlock.Options))
		}
		selectedOption := choiceBlock.Options[selectedIndex]
		madeChoicesInfo = append(madeChoicesInfo, sharedModels.UserChoiceInfo{Desc: choiceBlock.Description, Text: selectedOption.Text})
		statCausingGameOver, gameOverTriggered := applyConsequences(nextProgress, selectedOption.Consequences, &setupContent)

		// <<< ДОБАВЛЕНО: Логика добавления встреченных персонажей >>>
		if choiceBlock.Char != "" {
			charFound := false
			for _, encounteredChar := range nextProgress.EncounteredCharacters {
				if encounteredChar == choiceBlock.Char {
					charFound = true
					break
				}
			}
			if !charFound {
				nextProgress.EncounteredCharacters = append(nextProgress.EncounteredCharacters, choiceBlock.Char)
				s.logger.Debug("Added new encountered character", append(logFields, zap.String("character", choiceBlock.Char))...)
			}
		}
		// <<< КОНЕЦ ДОБАВЛЕНИЯ ЛОГИКИ >>>

		if gameOverTriggered {
			isGameOver = true
			gameOverStat = statCausingGameOver
			s.logger.Info("Game Over condition met", append(logFields, zap.String("gameOverStat", gameOverStat))...)
			break
		}
	}

	// <<< ДОБАВЛЕНО: Проверка, что все выборы сделаны, ЕСЛИ игра НЕ закончилась >>>
	if !isGameOver && len(selectedOptionIndices) < len(sceneData.Choices) {
		s.logger.Warn("Player did not provide choices for all available blocks, and game did not end",
			append(logFields, zap.Int("choicesMade", len(selectedOptionIndices)), zap.Int("choicesAvailable", len(sceneData.Choices)))...)
		return fmt.Errorf("%w: not all choices were made (made %d, available %d) and game over condition not met",
			sharedModels.ErrBadRequest, len(selectedOptionIndices), len(sceneData.Choices))
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

		// Create or Find the final progress node based on the hash.
		// Unlike the 'Playing' state, we *create* a new node if the final hash doesn't exist.
		existingFinalNode, errFind := s.playerProgressRepo.GetByStoryIDAndHash(ctx, gameState.PublishedStoryID, finalStateHash)
		var finalProgressNodeID uuid.UUID

		if errFind == nil {
			finalProgressNodeID = existingFinalNode.ID
			s.logger.Debug("Final progress node before game over already exists", append(logFields, zap.String("progressID", finalProgressNodeID.String()))...)
		} else if errors.Is(errFind, sharedModels.ErrNotFound) || errors.Is(errFind, pgx.ErrNoRows) {
			// Clear transient state before saving the final node
			nextProgress.StoryVariables = make(map[string]interface{})
			nextProgress.GlobalFlags = clearTransientFlags(nextProgress.GlobalFlags)

			// Assign the correct player ID before saving
			nextProgress.UserID = gameState.PlayerID

			// Save the new final progress node
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

		// Update PlayerGameState (the specific one we are operating on)
		now := time.Now().UTC()
		gameState.PlayerStatus = sharedModels.PlayerStatusGameOverPending
		// <<< ИЗМЕНЕНО: Прямое присваивание uuid.UUID >>>
		gameState.PlayerProgressID = finalProgressNodeID
		// <<< ИЗМЕНЕНО: Присваивание пустого NullUUID >>>
		gameState.CurrentSceneID = uuid.NullUUID{Valid: false}
		gameState.LastActivityAt = now

		if _, err := s.playerGameStateRepo.Save(ctx, gameState); err != nil {
			s.logger.Error("Failed to update PlayerGameState to GameOverPending", append(logFields, zap.Error(err))...)
			return sharedModels.ErrInternalServer
		}

		// Publish Game Over Task
		taskID := uuid.New().String()
		reasonCondition := "" // <<< ВОЗВРАЩАЕМ ИНИЦИАЛИЗАЦИЮ
		finalValue := nextProgress.CoreStats[gameOverStat]
		// --- НАЧАЛО ИЗМЕНЕНИЯ: Переписываем логику для обхода ошибки линтера ---
		if def, ok := setupContent.CoreStatsDefinition[gameOverStat]; ok {
			minConditionMet := def.GameOverConditions.Min && finalValue <= 0
			maxConditionMet := def.GameOverConditions.Max && finalValue >= 100

			if minConditionMet {
				reasonCondition = "min"
			} else if maxConditionMet { // Используем стандартный else if
				reasonCondition = "max"
			}
		}
		// --- КОНЕЦ ИЗМЕНЕНИЯ ---
		reason := sharedMessaging.GameOverReason{StatName: gameOverStat, Condition: reasonCondition, Value: finalValue}

		minimalGameOverConfig := sharedModels.ToMinimalConfigForGameOver(publishedStory.Config)
		minimalGameOverSetup := sharedModels.ToMinimalSetupForGameOver(&setupContent)

		// <<< НАЧАЛО ИЗМЕНЕНИЯ: Маршалим конфиг и сетап в JSON >>>
		minimalConfigBytes, errMarshalConf := json.Marshal(minimalGameOverConfig)
		if errMarshalConf != nil {
			s.logger.Error("Failed to marshal minimal config for game over task", append(logFields, zap.Error(errMarshalConf))...)
			return sharedModels.ErrInternalServer
		}
		minimalSetupBytes, errMarshalSetup := json.Marshal(minimalGameOverSetup)
		if errMarshalSetup != nil {
			s.logger.Error("Failed to marshal minimal setup for game over task", append(logFields, zap.Error(errMarshalSetup))...)
			return sharedModels.ErrInternalServer
		}
		// <<< КОНЕЦ ИЗМЕНЕНИЯ >>>

		lastStateProgress := *nextProgress         // Create a copy
		lastStateProgress.ID = finalProgressNodeID // Assign the correct ID

		gameOverPayload := sharedMessaging.GameOverTaskPayload{
			TaskID:           taskID,
			UserID:           gameState.PlayerID.String(),         // Use player ID from game state
			PublishedStoryID: gameState.PublishedStoryID.String(), // Use story ID from game state
			GameStateID:      gameState.ID.String(),               // Pass the specific game state ID
			LastState:        lastStateProgress,
			Reason:           reason,
			NovelConfig:      minimalConfigBytes, // <<< Используем байты JSON
			NovelSetup:       minimalSetupBytes,  // <<< Используем байты JSON
			// <<< ДОБАВЛЕНО: Language >>>
			Language: publishedStory.Language,
		}
		if err := s.publisher.PublishGameOverTask(ctx, gameOverPayload); err != nil {
			s.logger.Error("Failed to publish game over generation task", append(logFields, zap.Error(err))...)
			return sharedModels.ErrInternalServer
		}
		s.logger.Info("Game over task published", append(logFields, zap.String("taskID", taskID))...)
		return nil
	}

	// 12. Not Game Over (implies all choices were made): Calculate next state hash
	newStateHash, errHash := calculateStateHash(currentProgress.CurrentStateHash, nextProgress.CoreStats, nextProgress.StoryVariables, nextProgress.GlobalFlags)
	if errHash != nil {
		s.logger.Error("Failed to calculate new state hash", append(logFields, zap.Error(errHash))...)
		return sharedModels.ErrInternalServer
	}
	logFields = append(logFields, zap.String("newStateHash", newStateHash))
	s.logger.Debug("New state hash calculated", logFields...)

	// 13. Find or Create the NEXT PlayerProgress node based on the new hash
	//    This is crucial for branching and reusing progress nodes.
	var nextNodeProgress *sharedModels.PlayerProgress
	var nextNodeProgressID uuid.UUID

	existingNodeByHash, errFindNode := s.playerProgressRepo.GetByStoryIDAndHash(ctx, gameState.PublishedStoryID, newStateHash)
	if errFindNode == nil {
		// Node with this hash already exists
		nextNodeProgress = existingNodeByHash
		nextNodeProgressID = existingNodeByHash.ID
		s.logger.Info("Found existing PlayerProgress node matching the new state hash", append(logFields, zap.String("progressID", nextNodeProgressID.String()))...)
	} else if errors.Is(errFindNode, sharedModels.ErrNotFound) || errors.Is(errFindNode, pgx.ErrNoRows) {
		// Node with this hash does NOT exist, create it.
		s.logger.Info("Creating new PlayerProgress node for the new state hash", logFields...)

		// Prepare the node for saving (clear transient state)
		nextProgress.CurrentStateHash = newStateHash // Set the calculated hash
		nextProgress.UserID = gameState.PlayerID     // Set the correct player ID
		nextProgress.PublishedStoryID = gameState.PublishedStoryID
		nextProgress.StoryVariables = make(map[string]interface{})               // Clear transient vars
		nextProgress.GlobalFlags = clearTransientFlags(nextProgress.GlobalFlags) // Clear transient flags

		savedID, errSaveNode := s.playerProgressRepo.Save(ctx, nextProgress)
		if errSaveNode != nil {
			s.logger.Error("Failed to save new PlayerProgress node", append(logFields, zap.Error(errSaveNode))...)
			return sharedModels.ErrInternalServer
		}
		nextNodeProgressID = savedID
		nextNodeProgress = nextProgress // Use the newly created node
		nextNodeProgress.ID = savedID
		s.logger.Info("Saved new PlayerProgress node", append(logFields, zap.String("progressID", nextNodeProgressID.String()))...)
	} else {
		// Other error finding node by hash
		s.logger.Error("Error checking for existing progress node by hash", append(logFields, zap.Error(errFindNode))...)
		return sharedModels.ErrInternalServer
	}

	// 14. Find the next Scene associated with the new state hash
	var nextSceneID *uuid.UUID
	nextScene, errScene := s.sceneRepo.FindByStoryAndHash(ctx, gameState.PublishedStoryID, newStateHash) // Ищем сцену по новому хэшу

	if errScene == nil {
		// Scene already exists
		nextSceneID = &nextScene.ID
		s.logger.Info("Next scene found in DB", append(logFields, zap.String("sceneID", nextSceneID.String()))...)

		// Update PlayerGameState (the specific one we are operating on)
		gameState.PlayerStatus = sharedModels.PlayerStatusPlaying
		// <<< ИЗМЕНЕНО: Корректное присваивание NullUUID из *uuid.UUID >>>
		if nextSceneID != nil {
			gameState.CurrentSceneID = uuid.NullUUID{UUID: *nextSceneID, Valid: true}
		} else {
			// Эта ветка не должна выполняться, так как nextSceneID здесь не nil
			gameState.CurrentSceneID = uuid.NullUUID{Valid: false}
		}

		// <<< ИЗМЕНЕНО: Прямое присваивание uuid.UUID >>>
		gameState.PlayerProgressID = nextNodeProgressID // Link to the (potentially new) progress node
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

		// Update PlayerGameState (the specific one we are operating on)
		gameState.PlayerStatus = sharedModels.PlayerStatusGeneratingScene
		// <<< ИЗМЕНЕНО: Присваивание пустого NullUUID >>>
		gameState.CurrentSceneID = uuid.NullUUID{Valid: false}
		// <<< ИЗМЕНЕНО: Прямое присваивание uuid.UUID >>>
		gameState.PlayerProgressID = nextNodeProgressID // Link to the (potentially new) progress node
		gameState.LastActivityAt = time.Now().UTC()

		if _, err := s.playerGameStateRepo.Save(ctx, gameState); err != nil {
			s.logger.Error("Failed to update PlayerGameState to GeneratingScene", append(logFields, zap.Error(err))...)
			return sharedModels.ErrInternalServer
		}
		s.logger.Info("PlayerGameState updated to GeneratingScene")

		// Publish Generation Task
		generationPayload, errGenPayload := createGenerationPayload(
			gameState.PlayerID, // Use PlayerID from gameState
			publishedStory,
			nextNodeProgress, // <<< ИСПОЛЬЗУЕМ nextNodeProgress >>>
			gameState,        // <<< ПЕРЕДАЕМ gameState >>>
			madeChoicesInfo,
			newStateHash,
		)
		if errGenPayload != nil {
			s.logger.Error("Failed to create generation payload", append(logFields, zap.Error(errGenPayload))...)
			return sharedModels.ErrInternalServer
		}
		generationPayload.GameStateID = gameState.ID.String() // Add the specific game state ID

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

// DeletePlayerGameState deletes a specific game state (save slot).
func (s *gameLoopServiceImpl) DeletePlayerGameState(ctx context.Context, userID uuid.UUID, gameStateID uuid.UUID) error {
	log := s.logger.With(zap.String("gameStateID", gameStateID.String()), zap.Stringer("userID", userID))
	log.Info("Deleting player game state by ID")

	// 1. Получаем gameState по ID
	gameState, errState := s.playerGameStateRepo.GetByID(ctx, gameStateID)
	if errState != nil {
		if errors.Is(errState, sharedModels.ErrNotFound) || errors.Is(errState, pgx.ErrNoRows) {
			log.Warn("Player game state not found for deletion by ID")
			return sharedModels.ErrPlayerGameStateNotFound // Возвращаем, что не найдено
		}
		log.Error("Failed to get player game state for deletion check", zap.Error(errState))
		return sharedModels.ErrInternalServer
	}

	// 2. Проверяем, принадлежит ли он пользователю
	if gameState.PlayerID != userID {
		log.Warn("Attempt to delete game state belonging to another user", zap.Stringer("ownerUserID", gameState.PlayerID))
		return sharedModels.ErrForbidden
	}

	// 3. Если все ок, удаляем
	err := s.playerGameStateRepo.Delete(ctx, gameStateID) // <<< Use Delete instead of DeleteByID
	if err != nil {
		if errors.Is(err, sharedModels.ErrNotFound) {
			// Эта ветка не должна сработать после GetByID, но оставляем для полноты
			log.Warn("Player game state not found for deletion by ID (unexpected after check)")
			return sharedModels.ErrPlayerGameStateNotFound
		}
		// Log other DB errors
		log.Error("Error deleting player game state by ID from repository", zap.Error(err))
		return sharedModels.ErrInternalServer // Return generic internal error
	}

	log.Info("Player game state deleted successfully by ID")
	return nil
}

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
		if errors.Is(errState, sharedModels.ErrNotFound) || errors.Is(errState, pgx.ErrNoRows) {
			log.Warn("Player game state not found for retry")
			return sharedModels.ErrPlayerGameStateNotFound
		}
		log.Error("Failed to get player game state for retry", zap.Error(errState))
		return sharedModels.ErrInternalServer
	}

	// 2. Verify ownership
	if gameState.PlayerID != userID {
		log.Warn("User attempted to retry game state they do not own", zap.Stringer("ownerID", gameState.PlayerID))
		return sharedModels.ErrForbidden
	}

	// 3. Check current game state status (should be Error, maybe GeneratingScene if worker died)
	if gameState.PlayerStatus != sharedModels.PlayerStatusError && gameState.PlayerStatus != sharedModels.PlayerStatusGeneratingScene {
		log.Warn("Attempt to retry generation for game state not in Error or GeneratingScene status", zap.String("status", string(gameState.PlayerStatus)))
		return sharedModels.ErrCannotRetry // Or a more specific error
	}
	if gameState.PlayerStatus == sharedModels.PlayerStatusGeneratingScene {
		log.Warn("Retrying generation for game state still marked as GeneratingScene. Worker might have failed unexpectedly.")
	}

	// 4. Get the associated Published Story
	// Use gameState.PublishedStoryID which should be the same as the input storyID
	publishedStory, errStory := s.publishedRepo.GetByID(ctx, gameState.PublishedStoryID)
	if errStory != nil {
		if errors.Is(errStory, pgx.ErrNoRows) {
			log.Error("Published story linked to game state not found", zap.String("storyID", gameState.PublishedStoryID.String()), zap.Error(errStory))
			return sharedModels.ErrStoryNotFound // Data inconsistency
		}
		log.Error("Failed to get published story for retry", zap.Error(errStory))
		return sharedModels.ErrInternalServer
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
			gameState.PlayerStatus = sharedModels.PlayerStatusError
			errMsg := "Cannot retry: Story Config is missing"
			gameState.ErrorDetails = &errMsg
			if _, saveErr := s.playerGameStateRepo.Save(ctx, gameState); saveErr != nil {
				log.Error("Failed to update game state to Error after discovering missing config", zap.Error(saveErr))
			}
			return sharedModels.ErrInternalServer // Cannot proceed
		}

		// Update PublishedStory status back to SetupPending (if it was Error)
		// We don't touch the game state status here yet.
		if publishedStory.Status == sharedModels.StatusError {
			if err := s.publishedRepo.UpdateStatusFlagsAndDetails(ctx, publishedStory.ID, sharedModels.StatusSetupPending, false, false, nil); err != nil {
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
			PromptType:       sharedModels.PromptTypeNovelSetup,
			UserInput:        configJSONString,
			PublishedStoryID: publishedStory.ID.String(),
			Language:         publishedStory.Language,
			// GameStateID is not relevant for setup task
		}

		if errPub := s.publisher.PublishGenerationTask(ctx, setupPayload); errPub != nil {
			log.Error("Error publishing retry setup generation task", zap.Error(errPub))
			// If publishing failed, maybe revert story status? Or just return error?
			// Let's return an error, the caller might try again.
			return sharedModels.ErrInternalServer
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
			gameState.PlayerStatus = sharedModels.PlayerStatusError // Ensure it's Error
			errMsg := "Cannot retry: Inconsistent state (missing PlayerProgressID)"
			gameState.ErrorDetails = &errMsg
			if _, saveErr := s.playerGameStateRepo.Save(ctx, gameState); saveErr != nil {
				log.Error("Failed to update game state to Error after discovering missing progress ID", zap.Error(saveErr))
			}
			return sharedModels.ErrInternalServer
		}

		// Get the progress node associated with the game state
		// <<< ИЗМЕНЕНО: Нет индирекции для uuid.UUID >>>
		progress, errProgress := s.playerProgressRepo.GetByID(ctx, gameState.PlayerProgressID)
		if errProgress != nil {
			// <<< ИЗМЕНЕНО: Используем String() для uuid.UUID >>>
			log.Error("Failed to get PlayerProgress node linked to game state for retry", zap.String("progressID", gameState.PlayerProgressID.String()), zap.Error(errProgress))
			// Update game state to Error?
			return sharedModels.ErrInternalServer
		}

		// Update GameState status to GeneratingScene before publishing task
		gameState.PlayerStatus = sharedModels.PlayerStatusGeneratingScene
		gameState.ErrorDetails = nil                // Clear previous error
		gameState.LastActivityAt = time.Now().UTC() // Update activity time
		if _, errSave := s.playerGameStateRepo.Save(ctx, gameState); errSave != nil {
			log.Error("Failed to update game state status to GeneratingScene before retry task publish", zap.Error(errSave))
			return sharedModels.ErrInternalServer
		}
		log.Info("Updated game state status to GeneratingScene")

		if progress.CurrentStateHash == sharedModels.InitialStateHash {
			// --- Retrying Initial Scene ---
			log.Info("Retrying initial scene generation for game state", zap.String("initialHash", sharedModels.InitialStateHash))

			generationPayload, errPayload := createInitialSceneGenerationPayload(userID, publishedStory)
			if errPayload != nil {
				log.Error("Failed to create initial generation payload for scene retry", zap.Error(errPayload))
				// Attempt to roll back game state status?
				gameState.PlayerStatus = sharedModels.PlayerStatusError
				errMsg := fmt.Sprintf("Failed to create initial generation payload: %v", errPayload)
				gameState.ErrorDetails = &errMsg
				if _, saveErr := s.playerGameStateRepo.Save(context.Background(), gameState); saveErr != nil { // Use background context for rollback attempt
					log.Error("Failed to roll back game state status after initial payload creation error", zap.Error(saveErr))
				}
				return sharedModels.ErrInternalServer
			}
			generationPayload.GameStateID = gameStateID.String() // Add the specific game state ID

			if errPub := s.publisher.PublishGenerationTask(ctx, generationPayload); errPub != nil {
				log.Error("Error publishing initial scene retry generation task", zap.Error(errPub))
				// Attempt to roll back game state status?
				gameState.PlayerStatus = sharedModels.PlayerStatusError
				errMsg := fmt.Sprintf("Failed to publish initial generation task: %v", errPub)
				gameState.ErrorDetails = &errMsg
				if _, saveErr := s.playerGameStateRepo.Save(context.Background(), gameState); saveErr != nil {
					log.Error("Failed to roll back game state status after initial scene retry publish error", zap.Error(saveErr))
				}
				return sharedModels.ErrInternalServer
			}
			log.Info("Initial scene retry generation task published successfully", zap.String("taskID", generationPayload.TaskID))
			return nil

		} else {
			// --- Retrying Subsequent Scene ---
			log.Info("Retrying subsequent scene generation for game state", zap.String("stateHash", progress.CurrentStateHash))

			madeChoicesInfo := []sharedModels.UserChoiceInfo{} // No choices info on retry
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
				gameState.PlayerStatus = sharedModels.PlayerStatusError
				errMsg := fmt.Sprintf("Failed to create generation payload: %v", errGenPayload)
				gameState.ErrorDetails = &errMsg
				if _, saveErr := s.playerGameStateRepo.Save(context.Background(), gameState); saveErr != nil {
					log.Error("Failed to roll back game state status after payload creation error", zap.Error(saveErr))
				}
				return sharedModels.ErrInternalServer
			}
			generationPayload.GameStateID = gameStateID.String() // Add the specific game state ID

			if errPub := s.publisher.PublishGenerationTask(ctx, generationPayload); errPub != nil {
				log.Error("Error publishing retry scene generation task", zap.Error(errPub))
				// Attempt to roll back game state status?
				gameState.PlayerStatus = sharedModels.PlayerStatusError
				errMsg := fmt.Sprintf("Failed to publish generation task: %v", errPub)
				gameState.ErrorDetails = &errMsg
				if _, saveErr := s.playerGameStateRepo.Save(context.Background(), gameState); saveErr != nil {
					log.Error("Failed to roll back game state status after scene retry publish error", zap.Error(saveErr))
				}
				return sharedModels.ErrInternalServer
			}

			log.Info("Retry scene generation task published successfully", zap.String("taskID", generationPayload.TaskID))
			return nil
		}
	}
}

// GetPlayerProgress retrieves the player's current progress node based on their game state ID.
func (s *gameLoopServiceImpl) GetPlayerProgress(ctx context.Context, userID uuid.UUID, gameStateID uuid.UUID) (*sharedModels.PlayerProgress, error) {
	log := s.logger.With(zap.String("gameStateID", gameStateID.String()), zap.Stringer("userID", userID))
	log.Debug("GetPlayerProgress called")

	// 1. Get the player's game state by its ID.
	gameState, errState := s.playerGameStateRepo.GetByID(ctx, gameStateID)
	if errState != nil {
		if errors.Is(errState, sharedModels.ErrNotFound) || errors.Is(errState, pgx.ErrNoRows) {
			log.Warn("Player game state not found for GetPlayerProgress")
			return nil, sharedModels.ErrPlayerGameStateNotFound
		}
		log.Error("Failed to get player game state for GetPlayerProgress", zap.Error(errState))
		return nil, sharedModels.ErrInternalServer
	}

	// 2. Check if PlayerProgressID exists in the game state.
	// <<< ИЗМЕНЕНО: Сравниваем uuid.UUID с uuid.Nil >>>
	if gameState.PlayerProgressID == uuid.Nil {
		log.Warn("Player game state exists but has no associated PlayerProgressID", zap.String("gameStateID", gameState.ID.String()))
		return nil, sharedModels.ErrNotFound // Progress node is effectively not found
	}

	// 3. Fetch the PlayerProgress node using the ID from the game state.
	// <<< ИЗМЕНЕНО: Нет индирекции для uuid.UUID >>>
	progress, errProgress := s.playerProgressRepo.GetByID(ctx, gameState.PlayerProgressID)
	if errProgress != nil {
		// <<< ИЗМЕНЕНО: Используем String() для uuid.UUID >>>
		log.Error("Failed to get player progress node by ID from repository", zap.String("progressID", gameState.PlayerProgressID.String()), zap.Error(errProgress))
		if errors.Is(errProgress, pgx.ErrNoRows) || errors.Is(errProgress, sharedModels.ErrNotFound) {
			return nil, sharedModels.ErrNotFound // Indicates inconsistency
		}
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

// ListGameStates lists all game states for a player and story.
func (s *gameLoopServiceImpl) ListGameStates(ctx context.Context, playerID uuid.UUID, publishedStoryID uuid.UUID) ([]*sharedModels.PlayerGameState, error) {
	log := s.logger.With(zap.String("playerID", playerID.String()), zap.String("publishedStoryID", publishedStoryID.String()))
	log.Info("ListGameStates called")

	states, err := s.playerGameStateRepo.ListByPlayerAndStory(ctx, playerID, publishedStoryID)
	if err != nil {
		log.Error("Failed to list player game states from repository", zap.Error(err))
		return nil, sharedModels.ErrInternalServer
	}

	log.Info("Game states listed successfully", zap.Int("count", len(states)))
	return states, nil
}

// CreateNewGameState creates a new game state (save slot).
func (s *gameLoopServiceImpl) CreateNewGameState(ctx context.Context, playerID uuid.UUID, publishedStoryID uuid.UUID) (*sharedModels.PlayerGameState, error) {
	log := s.logger.With(zap.String("playerID", playerID.String()), zap.String("publishedStoryID", publishedStoryID.String()))
	log.Info("CreateNewGameState called")

	// Проверка лимита сохранений (1 слот)
	existingStates, errList := s.playerGameStateRepo.ListByPlayerAndStory(ctx, playerID, publishedStoryID)
	if errList != nil {
		log.Error("Failed to list existing game states before creation", zap.Error(errList))
		return nil, sharedModels.ErrInternalServer // Ошибка при проверке
	}
	if len(existingStates) > 0 {
		log.Warn("Attempted to create new game state when one already exists", zap.Int("existingCount", len(existingStates)))
		return nil, sharedModels.ErrSaveSlotExists // Возвращаем новую ошибку
	}

	// 1. Fetch the published story to ensure it's ready
	publishedStory, storyErr := s.publishedRepo.GetByID(ctx, publishedStoryID)
	if storyErr != nil {
		if errors.Is(storyErr, pgx.ErrNoRows) {
			log.Error("Published story not found for creating new game state")
			return nil, sharedModels.ErrStoryNotFound
		}
		log.Error("Failed to get published story for creating new game state", zap.Error(storyErr))
		return nil, sharedModels.ErrInternalServer
	}

	// 2. Check story status
	if publishedStory.Status != sharedModels.StatusReady {
		log.Warn("Attempt to create game state for story not in Ready status", zap.String("status", string(publishedStory.Status)))
		return nil, sharedModels.ErrStoryNotReady
	}

	// 3. Find or Create the Initial PlayerProgress node (common for all game states of this story)
	var initialProgressID uuid.UUID
	initialProgress, progressErr := s.playerProgressRepo.GetByStoryIDAndHash(ctx, publishedStoryID, sharedModels.InitialStateHash)

	if progressErr == nil {
		initialProgressID = initialProgress.ID
		log.Debug("Found existing initial PlayerProgress node", zap.String("progressID", initialProgressID.String()))
	} else if errors.Is(progressErr, sharedModels.ErrNotFound) || errors.Is(progressErr, pgx.ErrNoRows) {
		log.Info("Initial PlayerProgress node not found, creating it")
		initialStats := make(map[string]int)
		if publishedStory.Setup != nil && string(publishedStory.Setup) != "null" {
			var setupContent sharedModels.NovelSetupContent
			if setupErr := json.Unmarshal(publishedStory.Setup, &setupContent); setupErr == nil && setupContent.CoreStatsDefinition != nil {
				for key, statDef := range setupContent.CoreStatsDefinition {
					initialStats[key] = statDef.Initial
				}
				log.Debug("Initialized initial progress stats from story setup", zap.Any("initialStats", initialStats))
			}
		}

		newInitialProgress := &sharedModels.PlayerProgress{
			UserID:                playerID, // Link to the player creating it
			PublishedStoryID:      publishedStoryID,
			CurrentStateHash:      sharedModels.InitialStateHash,
			CoreStats:             initialStats,
			StoryVariables:        make(map[string]interface{}),
			GlobalFlags:           []string{},
			SceneIndex:            0,
			EncounteredCharacters: []string{},
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

	// 4. Find the Initial Scene ID (if it exists)
	var initialSceneID *uuid.UUID
	initialScene, sceneErr := s.sceneRepo.FindByStoryAndHash(ctx, publishedStoryID, sharedModels.InitialStateHash)
	if sceneErr == nil {
		initialSceneID = &initialScene.ID
		log.Debug("Found initial scene", zap.String("sceneID", initialSceneID.String()))
	} else if !errors.Is(sceneErr, pgx.ErrNoRows) && !errors.Is(sceneErr, sharedModels.ErrNotFound) {
		log.Error("Error fetching initial scene by hash", zap.Error(sceneErr))
		// Continue without initial scene ID, state will be GeneratingScene if scene is missing
	}

	// 5. Create the new PlayerGameState
	now := time.Now().UTC()
	playerStatus := sharedModels.PlayerStatusPlaying // Assume playing if initial scene exists
	if initialSceneID == nil {
		playerStatus = sharedModels.PlayerStatusGeneratingScene // If initial scene missing, need generation
		log.Warn("Initial scene not found for new game state, setting status to GeneratingScene")
	}
	newGameState := &sharedModels.PlayerGameState{
		PlayerID:         playerID,
		PublishedStoryID: publishedStoryID,
		// <<< ИЗМЕНЕНО: Прямое присваивание uuid.UUID >>>
		PlayerProgressID: initialProgressID,
		// <<< ИЗМЕНЕНО: Корректное присваивание NullUUID из *uuid.UUID >>>
		CurrentSceneID: uuid.NullUUID{}, // Инициализируем пустым
		PlayerStatus:   playerStatus,
		StartedAt:      now,
		LastActivityAt: now,
	}
	// <<< ИЗМЕНЕНО: Устанавливаем значение CurrentSceneID после создания структуры >>>
	if initialSceneID != nil {
		newGameState.CurrentSceneID = uuid.NullUUID{UUID: *initialSceneID, Valid: true}
	}

	// 6. Save the new PlayerGameState
	createdStateID, saveErr := s.playerGameStateRepo.Save(ctx, newGameState)
	if saveErr != nil {
		log.Error("Error creating new player game state in repository", zap.Error(saveErr))
		return nil, sharedModels.ErrInternalServer
	}
	newGameState.ID = createdStateID

	log.Info("New player game state created successfully", zap.String("gameStateID", createdStateID.String()))

	// 7. If status is GeneratingScene, publish the initial scene generation task
	if newGameState.PlayerStatus == sharedModels.PlayerStatusGeneratingScene {
		log.Info("Publishing initial scene generation task for the new game state")
		generationPayload, errPayload := createInitialSceneGenerationPayload(playerID, publishedStory)
		if errPayload != nil {
			log.Error("Failed to create initial generation payload after creating game state", zap.Error(errPayload))
			newGameState.PlayerStatus = sharedModels.PlayerStatusError
			newGameState.ErrorDetails = sharedModels.StringPtr("Failed to create initial generation payload")
			if _, updateErr := s.playerGameStateRepo.Save(ctx, newGameState); updateErr != nil {
				log.Error("Failed to update game state to Error after payload creation failure", zap.Error(updateErr))
			}
			return nil, sharedModels.ErrInternalServer // Indicate failure to start generation
		}

		// <<< УДАЛЕНО: Привязка задачи к конкретному GameStateID >>>
		// generationPayload.GameStateID = newGameState.ID.String()

		if errPub := s.publisher.PublishGenerationTask(ctx, generationPayload); errPub != nil {
			log.Error("Error publishing initial scene generation task after creating game state", zap.Error(errPub))
			newGameState.PlayerStatus = sharedModels.PlayerStatusError
			newGameState.ErrorDetails = sharedModels.StringPtr("Failed to publish initial generation task")
			if _, updateErr := s.playerGameStateRepo.Save(ctx, newGameState); updateErr != nil {
				log.Error("Failed to update game state to Error after publish failure", zap.Error(updateErr))
			}
			return nil, sharedModels.ErrInternalServer // Indicate failure to start generation
		}
		log.Info("Initial scene generation task published successfully for new game state", zap.String("taskID", generationPayload.TaskID))
	}

	return newGameState, nil
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
	gameState *sharedModels.PlayerGameState, // <<< ДОБАВЛЕНО: PlayerGameState >>>
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
		return sharedMessaging.GenerationTaskPayload{}, fmt.Errorf("error parsing Config JSON: %w", err)
	}
	var fullSetup sharedModels.NovelSetupContent
	if err := json.Unmarshal(story.Setup, &fullSetup); err != nil {
		log.Printf("WARN: Failed to parse Setup JSON for generation task StoryID %s: %v", story.ID, err)
		return sharedMessaging.GenerationTaskPayload{}, fmt.Errorf("error parsing Setup JSON: %w", err)
	}

	minimalConfig := sharedModels.ToMinimalConfigForScene(&fullConfig)
	minimalSetup := sharedModels.ToMinimalSetupForScene(&fullSetup)
	// --- MODIFICATION END ---

	// --- Язык берем напрямую из структуры PublishedStory ---
	storyLanguage := story.Language
	if storyLanguage == "" {
		log.Printf("WARN: Story language is empty for StoryID %s, defaulting to 'en'", story.ID)
		storyLanguage = "en"
	}

	// --- Определяем тип промпта на основе статуса игры ---
	promptType := sharedModels.PromptTypeNovelCreator
	// Проверяем gameState только если он не nil (т.е. это не retry)
	if gameState != nil && gameState.PlayerStatus == sharedModels.PlayerStatusGameOverPending {
		// Используем стандартный логгер пакета, так как 's' или 'p' здесь недоступны
		log.Printf("WARN: Attempting to create generation payload for StoryID %s while game over is pending. This should not happen.", story.ID)
		// В теории, сюда не должны попадать. Но если попали, возможно, стоит вернуть ошибку.
		// Пока что оставляем PromptTypeNovelCreator, но логируем предупреждение.
		// Или, возможно, использовать специальный тип, если он будет?
	}

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
	compressedInputData["sv"] = nonTransientVars               // Only non-nil, non-transient vars resulting from choice
	compressedInputData["ec"] = progress.EncounteredCharacters // <<< ДОБАВЛЕНО: Передаем список встреченных

	// Include info about the choice(s) the user just made
	userChoiceMap := make(map[string]string) // Convert UserChoiceInfo for AI prompt
	if len(madeChoicesInfo) > 0 {
		// Упрощение: Берем информацию только о последнем сделанном выборе
		lastChoice := madeChoicesInfo[len(madeChoicesInfo)-1]
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
		PromptType:       promptType,        // Используем определенный ранее тип
		UserInput:        userInputJSON,     // Use the marshaled JSON string
		StateHash:        currentStateHash,
		Language:         storyLanguage, // <<< Используем язык из PublishedStory >>>
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

	// --- Язык берем напрямую из структуры PublishedStory --- // <<< ВОССТАНОВЛЕНО
	storyLanguage := story.Language // <<< ВОССТАНОВЛЕНО
	if storyLanguage == "" {        // <<< ВОССТАНОВЛЕНО
		log.Printf("WARN: Story language is empty for StoryID %s, defaulting to 'en'", story.ID) // <<< ВОССТАНОВЛЕНО
		storyLanguage = "en"                                                                     // <<< ВОССТАНОВЛЕНО
	} // <<< ВОССТАНОВЛЕНО

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
	compressedInputData["ec"] = []string{}                   // <<< ДОБАВЛЕНО: Пустой список для первой сцены

	userInputBytes, errMarshal := json.Marshal(compressedInputData)
	if errMarshal != nil {
		log.Printf("ERROR: Failed to marshal compressedInputData for initial scene generation task StoryID %s: %v", story.ID, errMarshal)
		return sharedMessaging.GenerationTaskPayload{}, fmt.Errorf("error marshaling input data: %w", errMarshal)
	}
	userInputJSON := string(userInputBytes)

	payload := sharedMessaging.GenerationTaskPayload{
		TaskID:           uuid.New().String(),
		UserID:           userID.String(),                               // Use string UUID
		PublishedStoryID: story.ID.String(),                             // Use string UUID
		PromptType:       sharedModels.PromptTypeNovelFirstSceneCreator, // <<< ИСПРАВЛЕНО: Правильный тип для первой сцены
		UserInput:        userInputJSON,
		StateHash:        sharedModels.InitialStateHash, // Use the constant for initial state hash
		Language:         storyLanguage,                 // <<< Используем язык из PublishedStory (с fallback на 'en') >>>
		// GameStateID is intentionally omitted for the initial scene task
	}

	return payload, nil
}

// checkAndGenerateSetupImages parses the setup, checks image needs, updates flags, and publishes tasks.
func (s *gameLoopServiceImpl) checkAndGenerateSetupImages(ctx context.Context, story *sharedModels.PublishedStory, setupBytes []byte, userID uuid.UUID) (bool, error) {
	log := s.logger.With(zap.String("publishedStoryID", story.ID.String()))

	var setupContent sharedModels.NovelSetupContent
	if err := json.Unmarshal(setupBytes, &setupContent); err != nil {
		log.Error("Failed to unmarshal setup JSON in checkAndGenerateSetupImages", zap.Error(err))
		return false, fmt.Errorf("failed to unmarshal setup JSON: %w", err)
	}

	var fullConfig sharedModels.Config
	var characterVisualStyle string
	var storyStyle string
	// <<< ВОЗВРАЩАЕМ ПЕРЕМЕННЫЕ ДЛЯ СУФФИКСОВ >>>
	var characterStyleSuffix string = "" // Инициализируем пустыми строками
	var previewStyleSuffix string = ""   // Инициализируем пустыми строками

	// <<< ВОЗВРАЩАЕМ ЛОГИКУ ПОЛУЧЕНИЯ СУФФИКСОВ ИЗ ДИНАМИЧЕСКОГО КОНФИГА >>>
	charDynConfKey := "prompt.character_style_suffix"
	dynamicConfigChar, errConfChar := s.dynamicConfigRepo.GetByKey(ctx, charDynConfKey)
	if errConfChar != nil {
		if !errors.Is(errConfChar, sharedModels.ErrNotFound) {
			log.Error("Failed to get dynamic config for character style suffix, using empty default", zap.String("key", charDynConfKey), zap.Error(errConfChar))
		} else {
			log.Info("Dynamic config for character style suffix not found, using empty default", zap.String("key", charDynConfKey))
		}
	} else if dynamicConfigChar != nil && dynamicConfigChar.Value != "" {
		characterStyleSuffix = dynamicConfigChar.Value
		log.Info("Using dynamic config for character style suffix", zap.String("key", charDynConfKey))
	}

	previewDynConfKey := "prompt.story_preview_style_suffix"
	dynamicConfigPreview, errConfPreview := s.dynamicConfigRepo.GetByKey(ctx, previewDynConfKey)
	if errConfPreview != nil {
		if !errors.Is(errConfPreview, sharedModels.ErrNotFound) {
			log.Error("Failed to get dynamic config for story preview style suffix, using empty default", zap.String("key", previewDynConfKey), zap.Error(errConfPreview))
		} else {
			log.Info("Dynamic config for story preview style suffix not found, using empty default", zap.String("key", previewDynConfKey))
		}
	} else if dynamicConfigPreview != nil && dynamicConfigPreview.Value != "" {
		previewStyleSuffix = dynamicConfigPreview.Value
		log.Info("Using dynamic config for story preview style suffix", zap.String("key", previewDynConfKey))
	}
	// <<< КОНЕЦ ВОЗВРАЩЕННОЙ ЛОГИКИ >>>

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

	// <<< НАЧАЛО ИЗМЕНЕНИЯ: Оптимизация с GetImageURLsByReferences >>>
	// 1. Собрать все референсы персонажей для проверки
	characterRefsToCheck := make([]string, 0, len(setupContent.Characters))
	refToCharDataMap := make(map[string]sharedModels.CharacterDefinition) // <<< ИСПРАВЛЕНО: Правильный тип
	for _, charData := range setupContent.Characters {
		if charData.ImageRef == "" || charData.Prompt == "" {
			continue // Пропускаем, если нет рефа или промпта
		}
		// Корректируем референс (гарантируем префикс ch_)
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
		refToCharDataMap[correctedRef] = charData // Сохраняем данные по исправленному референсу
	}

	// 2. Выполнить один запрос для получения существующих URL
	existingURLs := make(map[string]string)
	var errCheckBatch error
	if len(characterRefsToCheck) > 0 {
		existingURLs, errCheckBatch = s.imageReferenceRepo.GetImageURLsByReferences(ctx, characterRefsToCheck)
		if errCheckBatch != nil {
			log.Error("Error checking character ImageRefs in DB (batch)", zap.Error(errCheckBatch))
			// Не прерываем весь процесс, просто не сможем сгенерировать
			// Можно вернуть ошибку, если это критично
			// return false, fmt.Errorf("failed to batch check image references: %w", errCheckBatch)
		}
	}

	// 3. Определить, какие изображения нужно сгенерировать
	for _, ref := range characterRefsToCheck {
		if _, exists := existingURLs[ref]; !exists {
			// URL не найден, значит нужно генерировать
			log.Debug("Character image needs generation (checked via batch)", zap.String("image_ref", ref))
			needsCharacterImages = true
			charData := refToCharDataMap[ref] // Получаем данные персонажа
			characterIDForTask := uuid.New()
			// <<< ВОЗВРАЩАЕМ ИСПОЛЬЗОВАНИЕ СУФФИКСА >>>
			fullCharacterPrompt := charData.Prompt + characterVisualStyle + characterStyleSuffix
			imageTask := sharedMessaging.CharacterImageTaskPayload{
				TaskID:           characterIDForTask.String(),
				UserID:           userID.String(),
				CharacterID:      characterIDForTask,
				Prompt:           fullCharacterPrompt,
				NegativePrompt:   charData.NegPrompt,
				ImageReference:   ref, // Используем исправленный референс
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
			// <<< ВОЗВРАЩАЕМ ИСПОЛЬЗОВАНИЕ СУФФИКСА >>>
			fullPreviewPromptWithStyles := basePreviewPrompt + storyStyle + characterVisualStyle + previewStyleSuffix
			previewTask := sharedMessaging.CharacterImageTaskPayload{
				TaskID:           uuid.New().String(),
				UserID:           userID.String(), // <<< ИСПРАВЛЕНО: Преобразуем в строку
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

// RetryInitialGeneration handles retrying generation for a published story's Setup or Initial Scene.
func (s *gameLoopServiceImpl) RetryInitialGeneration(ctx context.Context, userID, storyID uuid.UUID) error {
	log := s.logger.With(zap.Stringer("storyID", storyID), zap.Stringer("userID", userID))
	log.Info("RetryInitialGeneration called")

	// 1. Get the Published Story first
	publishedStory, err := s.publishedRepo.GetByID(ctx, storyID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, sharedModels.ErrNotFound) {
			log.Warn("Published story not found for retry")
			return sharedModels.ErrStoryNotFound
		}
		log.Error("Failed to get published story for retry", zap.Error(err))
		return sharedModels.ErrInternalServer
	}

	// 2. Check if the user is the author
	if publishedStory.UserID != userID { // Correct field: UserID
		log.Warn("User is not the author, cannot retry generation")
		return sharedModels.ErrForbidden // Or ErrBadRequest? Forbidden seems more appropriate.
	}

	// 3. Check if NovelSetup exists
	if publishedStory.Setup == nil || len(publishedStory.Setup) == 0 {
		log.Info("NovelSetup is missing, retrying setup generation")

		if publishedStory.Config == nil {
			log.Error("Cannot retry setup: Story config is missing")
			// Update story status to Error? Not strictly necessary, but good practice
			errMsg := "Cannot retry Setup: Story Config is missing"
			if errUpdate := s.publishedRepo.UpdateStatusFlagsAndDetails(ctx, storyID, sharedModels.StatusError, false, false, &errMsg); errUpdate != nil {
				log.Error("Failed to update story status to Error after discovering missing config for setup retry", zap.Error(errUpdate))
			}
			return sharedModels.ErrInternalServer // Cannot proceed without config
		}

		// Mark story as setup pending, clear error
		if errUpdate := s.publishedRepo.UpdateStatusFlagsAndDetails(ctx, storyID, sharedModels.StatusSetupPending, false, false, nil); errUpdate != nil {
			log.Error("Failed to update story status to SetupPending before retry", zap.Error(errUpdate))
			return fmt.Errorf("failed to update story status: %w", errUpdate)
		}

		// Publish setup generation task
		taskID := uuid.New().String()
		setupPayload := sharedMessaging.GenerationTaskPayload{
			TaskID:           taskID,
			UserID:           publishedStory.UserID.String(), // UserID from story
			PublishedStoryID: storyID.String(),
			PromptType:       sharedModels.PromptTypeNovelSetup,
			UserInput:        string(publishedStory.Config), // Use story config as input
			Language:         publishedStory.Language,
			// No StateHash or GameStateID needed for Setup
		}
		if errPub := s.publisher.PublishGenerationTask(ctx, setupPayload); errPub != nil { // Correct publisher method
			log.Error("Failed to publish setup retry task", zap.Error(errPub))
			// Try to revert status? Or leave as pending? Leave as pending for now.
			errMsg := fmt.Sprintf("Failed to publish setup retry task: %v", errPub)
			if errUpdate := s.publishedRepo.UpdateStatusFlagsAndDetails(context.Background(), storyID, sharedModels.StatusError, false, false, &errMsg); errUpdate != nil {
				log.Error("Failed to revert story status to Error after failed setup publish", zap.Error(errUpdate))
			}
			return fmt.Errorf("failed to publish setup retry task: %w", errPub)
		}
		log.Info("Published setup retry task successfully", zap.String("taskID", taskID))
		return nil // Success
	}

	// 4. NovelSetup exists, check if the initial scene text exists
	scene, err := s.sceneRepo.GetByStoryIDAndStateHash(ctx, storyID, sharedModels.InitialStateHash) // Correct constant usage
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, sharedModels.ErrNotFound) {
			log.Info("Initial scene not found, retrying first scene generation")
			// Mark story as first scene pending, clear error
			if errUpdate := s.publishedRepo.UpdateStatusFlagsAndDetails(ctx, storyID, publishedStory.Status, true, publishedStory.AreImagesPending, nil); errUpdate != nil {
				log.Error("Failed to update story IsFirstScenePending flag before retry", zap.Error(errUpdate))
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
		// Other error fetching scene
		log.Error("Failed to check for initial scene", zap.Error(err))
		return sharedModels.ErrInternalServer
	}

	// 5. Both Setup and the Initial Scene TEXT exist. Check for the cover image.
	if publishedStory.CoverImageURL == nil || *publishedStory.CoverImageURL == "" { // Correct check for *string
		log.Info("Initial scene text exists, but cover image is missing. Retrying image generation.", zap.String("sceneID", scene.ID.String()))

		// --- Reconstruct Cover Image Prompt --- START ---
		var setupContent sharedModels.NovelSetupContent
		if errUnmarshalSetup := json.Unmarshal(publishedStory.Setup, &setupContent); errUnmarshalSetup != nil {
			log.Error("Failed to unmarshal setup JSON to reconstruct cover prompt", zap.Error(errUnmarshalSetup))
			// Mark as error? Or just fail retry?
			return fmt.Errorf("failed to parse setup for cover image retry: %w", errUnmarshalSetup)
		}

		if setupContent.StoryPreviewImagePrompt == "" {
			log.Error("Cannot retry cover image generation: StoryPreviewImagePrompt (spi) is empty in setup", zap.String("storyID", storyID.String()))
			return fmt.Errorf("cannot retry cover image: StoryPreviewImagePrompt is missing in setup")
		}

		var fullConfig sharedModels.Config
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
			if !errors.Is(errConfPreview, sharedModels.ErrNotFound) {
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

	// --- ADDED: Check for pending character images if cover exists --- START ---
	// This condition is met if the code reached here (meaning Setup and Scene Text exist) AND the cover image URL is not empty.
	if publishedStory.AreImagesPending {
		log.Info("Cover image exists, but AreImagesPending flag is true. Checking and retrying character images asynchronously.")

		// Check/Generate setup images (async, doesn't block retry)
		go func(bgCtx context.Context, story *sharedModels.PublishedStory, setupBytes []byte, authorID uuid.UUID) {
			_, errImages := s.checkAndGenerateSetupImages(bgCtx, story, setupBytes, authorID)
			if errImages != nil {
				// Log error from background task
				s.logger.Error("Error during background checkAndGenerateSetupImages triggered by initial retry",
					zap.String("publishedStoryID", story.ID.String()),
					zap.Stringer("userID", authorID),
					zap.Error(errImages))
			}
		}(context.WithoutCancel(ctx), publishedStory, publishedStory.Setup, userID) // Pass necessary data to goroutine

		// We initiated the async check/retry for character images.
		return nil // Success
	}
	// --- ADDED: Check for pending character images if cover exists --- END ---

	// 6. Setup, Scene Text, and Cover Image all exist, and AreImagesPending is false. Nothing to retry initially.
	log.Warn("Setup, initial scene text, cover image exist, and no images are pending. Nothing to retry via initial retry endpoint.")
	return ErrCannotRetryInitial // Use a more specific error?
}

var (
	ErrCannotRetryInitial      = errors.New("cannot retry initial generation steps (setup, first scene text, cover image) as they already exist or are pending")
	ErrNoSaveSlotsAvailable    = errors.New("no save slots available")                                   // TODO: Define this properly
	ErrSaveSlotLimitReached    = errors.New("player has reached the maximum number of save slots")       // TODO: Define this properly
	ErrInitialSceneNotReadyYet = errors.New("initial scene for the story is not generated or ready yet") // Added error
)
