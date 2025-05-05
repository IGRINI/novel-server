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

func (s *gameLoopServiceImpl) GetStoryScene(ctx context.Context, userID uuid.UUID, gameStateID uuid.UUID) (*models.StoryScene, error) {
	log := s.logger.With(zap.String("gameStateID", gameStateID.String()), zap.Stringer("userID", userID))
	log.Info("GetStoryScene called")

	gameState, errState := s.playerGameStateRepo.GetByID(ctx, gameStateID)
	if errState != nil {
		if errors.Is(errState, models.ErrNotFound) || errors.Is(errState, pgx.ErrNoRows) {
			log.Warn("Player game state not found by ID")
			return nil, models.ErrPlayerGameStateNotFound
		}
		log.Error("Failed to get player game state by ID", zap.Error(errState))
		return nil, models.ErrInternalServer
	}

	if gameState.PlayerID != userID {
		log.Warn("User attempted to access game state they do not own", zap.Stringer("ownerID", gameState.PlayerID))
		return nil, models.ErrForbidden
	}

	switch gameState.PlayerStatus {
	case models.PlayerStatusGeneratingScene:
		log.Info("Player is waiting for scene generation")
		return nil, models.ErrSceneNeedsGeneration
	case models.PlayerStatusGameOverPending:
		log.Info("Player is waiting for game over generation")
		return nil, models.ErrGameOverPending
	case models.PlayerStatusCompleted:
		log.Info("Player has completed the game")
		return nil, models.ErrGameCompleted
	case models.PlayerStatusError:
		log.Error("Player game state is in error", zap.Stringp("errorDetails", gameState.ErrorDetails))
		return nil, models.ErrPlayerStateInError
	case models.PlayerStatusPlaying:
		log.Debug("Player status is Playing, fetching current scene")
	default:
		log.Error("Unknown player status in game state", zap.String("status", string(gameState.PlayerStatus)))
		return nil, models.ErrInternalServer
	}

	if !gameState.CurrentSceneID.Valid {
		log.Error("CRITICAL: PlayerStatus is Playing, but CurrentSceneID is NULL", zap.String("gameStateID", gameState.ID.String()))
		return nil, models.ErrSceneNotFound
	}

	scene, errScene := s.sceneRepo.GetByID(ctx, gameState.CurrentSceneID.UUID)
	if errScene != nil {
		if errors.Is(errScene, pgx.ErrNoRows) || errors.Is(errScene, models.ErrNotFound) {
			log.Error("CRITICAL: CurrentSceneID from game state not found in scene repository", zap.String("sceneID", gameState.CurrentSceneID.UUID.String()))
			return nil, models.ErrSceneNotFound
		}
		log.Error("Error getting scene by ID from repository", zap.String("sceneID", gameState.CurrentSceneID.UUID.String()), zap.Error(errScene))
		return nil, models.ErrInternalServer
	}

	log.Info("Scene found and returned", zap.String("sceneID", scene.ID.String()))
	return scene, nil
}

func (s *gameLoopServiceImpl) MakeChoice(ctx context.Context, userID uuid.UUID, gameStateID uuid.UUID, selectedOptionIndices []int) error {
	logFields := []zap.Field{
		zap.String("gameStateID", gameStateID.String()),
		zap.Stringer("userID", userID),
		zap.Any("selectedOptionIndices", selectedOptionIndices),
	}
	s.logger.Info("MakeChoice called", logFields...)

	gameState, errState := s.playerGameStateRepo.GetByID(ctx, gameStateID)
	if errState != nil {
		if errors.Is(errState, models.ErrNotFound) || errors.Is(errState, pgx.ErrNoRows) {
			s.logger.Warn("Player game state not found for MakeChoice")
			return models.ErrPlayerGameStateNotFound
		}
		s.logger.Error("Failed to get player game state for MakeChoice", zap.Error(errState))
		return models.ErrInternalServer
	}

	if gameState.PlayerStatus != models.PlayerStatusPlaying {
		s.logger.Warn("Attempt to make choice while not in Playing status", append(logFields, zap.String("playerStatus", string(gameState.PlayerStatus)))...)
		if gameState.PlayerStatus == models.PlayerStatusGeneratingScene {
			return models.ErrSceneNeedsGeneration
		}
		return models.ErrBadRequest
	}

	if gameState.PlayerProgressID == uuid.Nil {
		s.logger.Error("CRITICAL: PlayerStatus is Playing, but PlayerProgressID is Nil", append(logFields, zap.String("gameStateID", gameState.ID.String()))...)
		return models.ErrInternalServer
	}
	if !gameState.CurrentSceneID.Valid {
		s.logger.Error("CRITICAL: PlayerStatus is Playing, but CurrentSceneID is Nil", append(logFields, zap.String("gameStateID", gameState.ID.String()))...)
		return models.ErrSceneNotFound
	}

	currentProgressID := gameState.PlayerProgressID
	currentSceneID := gameState.CurrentSceneID.UUID
	logFields = append(logFields, zap.String("currentProgressID", currentProgressID.String()), zap.String("currentSceneID", currentSceneID.String()))

	currentProgress, errProgress := s.playerProgressRepo.GetByID(ctx, currentProgressID)
	if errProgress != nil {
		s.logger.Error("Failed to get current PlayerProgress node linked in game state", append(logFields, zap.Error(errProgress))...)
		return models.ErrInternalServer
	}

	currentScene, errScene := s.sceneRepo.GetByID(ctx, currentSceneID)
	if errScene != nil {
		s.logger.Error("Failed to get current Scene linked in game state", append(logFields, zap.Error(errScene))...)
		return models.ErrInternalServer
	}

	publishedStory, errStory := s.publishedRepo.GetByID(ctx, gameState.PublishedStoryID)
	if errStory != nil {
		s.logger.Error("Failed to get published story associated with game state", append(logFields, zap.Error(errStory))...)
		return models.ErrInternalServer
	}

	var sceneData sceneContentChoices
	if err := json.Unmarshal(currentScene.Content, &sceneData); err != nil {
		s.logger.Error("Failed to unmarshal current scene content", append(logFields, zap.Error(err))...)
		return models.ErrInternalServer
	}

	if len(selectedOptionIndices) == 0 {
		s.logger.Warn("Player sent empty choice array", logFields...)
		return fmt.Errorf("%w: selected_option_indices cannot be empty", models.ErrBadRequest)
	}
	if len(selectedOptionIndices) > len(sceneData.Choices) {
		s.logger.Warn("Player sent more choices than available in the scene", append(logFields, zap.Int("sceneChoices", len(sceneData.Choices)), zap.Int("playerChoices", len(selectedOptionIndices)))...)
		return fmt.Errorf("%w: received %d choice indices, but scene only has %d choice blocks", models.ErrBadRequest, len(selectedOptionIndices), len(sceneData.Choices))
	}

	if publishedStory.Setup == nil {
		s.logger.Error("CRITICAL: PublishedStory Setup is nil", logFields...)
		return models.ErrInternalServer
	}
	var setupContent models.NovelSetupContent
	if err := json.Unmarshal(publishedStory.Setup, &setupContent); err != nil {
		s.logger.Error("Failed to unmarshal NovelSetup content", append(logFields, zap.Error(err))...)
		return models.ErrInternalServer
	}

	nextProgress := &models.PlayerProgress{
		UserID:                currentProgress.UserID,
		PublishedStoryID:      currentProgress.PublishedStoryID,
		CoreStats:             make(map[string]int),
		StoryVariables:        make(map[string]interface{}),
		GlobalFlags:           make([]string, 0, len(currentProgress.GlobalFlags)),
		SceneIndex:            currentProgress.SceneIndex + 1,
		EncounteredCharacters: make([]string, 0, len(currentProgress.EncounteredCharacters)),
	}
	for k, v := range currentProgress.CoreStats {
		nextProgress.CoreStats[k] = v
	}
	for k, v := range currentProgress.StoryVariables {
		nextProgress.StoryVariables[k] = v
	}
	nextProgress.GlobalFlags = append(nextProgress.GlobalFlags, currentProgress.GlobalFlags...)
	nextProgress.EncounteredCharacters = append(nextProgress.EncounteredCharacters, currentProgress.EncounteredCharacters...)

	var isGameOver bool
	var gameOverStat string
	madeChoicesInfo := make([]models.UserChoiceInfo, 0, len(selectedOptionIndices))
	for i, selectedIndex := range selectedOptionIndices {
		if i >= len(sceneData.Choices) {
			s.logger.Error("Logic error: index out of bounds accessing sceneData.Choices", append(logFields, zap.Int("index", i), zap.Int("sceneChoicesLen", len(sceneData.Choices)))...)
			return models.ErrInternalServer
		}
		choiceBlock := sceneData.Choices[i]

		if selectedIndex < 0 || selectedIndex >= len(choiceBlock.Options) {
			s.logger.Warn("Invalid selected option index for choice block", append(logFields, zap.Int("choiceBlockIndex", i), zap.Int("selectedIndex", selectedIndex), zap.Int("optionsAvailable", len(choiceBlock.Options)))...)
			return fmt.Errorf("%w: invalid index %d for choice block %d (options: %d)", models.ErrInvalidChoice, selectedIndex, i, len(choiceBlock.Options))
		}
		selectedOption := choiceBlock.Options[selectedIndex]
		madeChoicesInfo = append(madeChoicesInfo, models.UserChoiceInfo{Desc: choiceBlock.Description, Text: selectedOption.Text})
		statCausingGameOver, gameOverTriggered := applyConsequences(nextProgress, selectedOption.Consequences, &setupContent)

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

		if gameOverTriggered {
			isGameOver = true
			gameOverStat = statCausingGameOver
			s.logger.Info("Game Over condition met", append(logFields, zap.String("gameOverStat", gameOverStat))...)
			break
		}
	}

	if !isGameOver && len(selectedOptionIndices) < len(sceneData.Choices) {
		s.logger.Warn("Player did not provide choices for all available blocks, and game did not end",
			append(logFields, zap.Int("choicesMade", len(selectedOptionIndices)), zap.Int("choicesAvailable", len(sceneData.Choices)))...)
		return fmt.Errorf("%w: not all choices were made (made %d, available %d) and game over condition not met",
			models.ErrBadRequest, len(selectedOptionIndices), len(sceneData.Choices))
	}

	if isGameOver {
		s.logger.Info("Handling Game Over state update")

		s.logger.Debug("Data before calculateStateHash (Game Over)",
			zap.String("gameStateID", gameStateID.String()),
			zap.String("previousHash", currentProgress.CurrentStateHash),
			zap.Any("coreStats", nextProgress.CoreStats),
			zap.Any("storyVars", nextProgress.StoryVariables),
			zap.Strings("globalFlags", nextProgress.GlobalFlags),
		)

		finalStateHash, hashErr := calculateStateHash(currentProgress.CurrentStateHash, nextProgress.CoreStats, nextProgress.StoryVariables, nextProgress.GlobalFlags)
		if hashErr != nil {
			s.logger.Error("Failed to calculate final state hash before game over", append(logFields, zap.Error(hashErr))...)
			return models.ErrInternalServer
		}
		nextProgress.CurrentStateHash = finalStateHash

		existingFinalNode, errFind := s.playerProgressRepo.GetByStoryIDAndHash(ctx, gameState.PublishedStoryID, finalStateHash)
		var finalProgressNodeID uuid.UUID

		if errFind == nil {
			finalProgressNodeID = existingFinalNode.ID
			s.logger.Debug("Final progress node before game over already exists", append(logFields, zap.String("progressID", finalProgressNodeID.String()))...)
		} else if errors.Is(errFind, models.ErrNotFound) || errors.Is(errFind, pgx.ErrNoRows) {
			nextProgress.StoryVariables = make(map[string]interface{})
			nextProgress.GlobalFlags = clearTransientFlags(nextProgress.GlobalFlags)

			nextProgress.UserID = gameState.PlayerID

			savedID, errSave := s.playerProgressRepo.Save(ctx, nextProgress)
			if errSave != nil {
				s.logger.Error("Failed to save final player progress node before game over", append(logFields, zap.Error(errSave))...)
				return models.ErrInternalServer
			}
			finalProgressNodeID = savedID
			s.logger.Info("Saved new final PlayerProgress node before game over", append(logFields, zap.String("progressID", finalProgressNodeID.String()))...)
		} else {
			s.logger.Error("Error checking for existing final progress node before game over", append(logFields, zap.Error(errFind))...)
			return models.ErrInternalServer
		}

		now := time.Now().UTC()
		gameState.PlayerStatus = models.PlayerStatusGameOverPending
		gameState.PlayerProgressID = finalProgressNodeID
		gameState.CurrentSceneID = uuid.NullUUID{Valid: false}
		gameState.LastActivityAt = now

		if _, err := s.playerGameStateRepo.Save(ctx, gameState); err != nil {
			s.logger.Error("Failed to update PlayerGameState to GameOverPending", append(logFields, zap.Error(err))...)
			return models.ErrInternalServer
		}

		taskID := uuid.New().String()
		reasonCondition := ""
		finalValue := nextProgress.CoreStats[gameOverStat]
		if def, ok := setupContent.CoreStatsDefinition[gameOverStat]; ok {
			minConditionMet := def.Go.Min && finalValue <= 0
			maxConditionMet := def.Go.Max && finalValue >= 100

			if minConditionMet {
				reasonCondition = "min"
			} else if maxConditionMet {
				reasonCondition = "max"
			}
		}
		reason := sharedMessaging.GameOverReason{StatName: gameOverStat, Condition: reasonCondition, Value: finalValue}

		minimalGameOverConfig := models.ToMinimalConfigForGameOver(publishedStory.Config)
		minimalGameOverSetup := models.ToMinimalSetupForGameOver(&setupContent)

		minimalConfigBytes, errMarshalConf := json.Marshal(minimalGameOverConfig)
		if errMarshalConf != nil {
			s.logger.Error("Failed to marshal minimal config for game over task", append(logFields, zap.Error(errMarshalConf))...)
			return models.ErrInternalServer
		}
		minimalSetupBytes, errMarshalSetup := json.Marshal(minimalGameOverSetup)
		if errMarshalSetup != nil {
			s.logger.Error("Failed to marshal minimal setup for game over task", append(logFields, zap.Error(errMarshalSetup))...)
			return models.ErrInternalServer
		}

		lastStateProgress := *nextProgress
		lastStateProgress.ID = finalProgressNodeID

		gameOverPayload := sharedMessaging.GameOverTaskPayload{
			TaskID:           taskID,
			UserID:           gameState.PlayerID.String(),
			PublishedStoryID: gameState.PublishedStoryID.String(),
			GameStateID:      gameState.ID.String(),
			LastState:        lastStateProgress,
			Reason:           reason,
			NovelConfig:      minimalConfigBytes,
			NovelSetup:       minimalSetupBytes,
			Language:         publishedStory.Language,
		}
		if err := s.publisher.PublishGameOverTask(ctx, gameOverPayload); err != nil {
			s.logger.Error("Failed to publish game over generation task", append(logFields, zap.Error(err))...)
			return models.ErrInternalServer
		}
		s.logger.Info("Game over task published", append(logFields, zap.String("taskID", taskID))...)
		return nil
	}

	s.logger.Debug("Data before calculateStateHash (Normal Flow)",
		zap.String("gameStateID", gameStateID.String()),
		zap.String("previousHash", currentProgress.CurrentStateHash),
		zap.Any("coreStats", nextProgress.CoreStats),
		zap.Any("storyVars", nextProgress.StoryVariables),
		zap.Strings("globalFlags", nextProgress.GlobalFlags),
	)
	nextStateHash, hashErr := calculateStateHash(currentProgress.CurrentStateHash, nextProgress.CoreStats, nextProgress.StoryVariables, nextProgress.GlobalFlags)
	if hashErr != nil {
		s.logger.Error("Failed to calculate next state hash", append(logFields, zap.Error(hashErr))...)
		return models.ErrInternalServer
	}
	nextProgress.CurrentStateHash = nextStateHash
	logFields = append(logFields, zap.String("nextStateHash", nextStateHash))
	s.logger.Debug("Calculated next state hash", logFields...)

	var nextNodeProgress *models.PlayerProgress
	var nextNodeProgressID uuid.UUID

	existingNodeByHash, errFindNode := s.playerProgressRepo.GetByStoryIDAndHash(ctx, gameState.PublishedStoryID, nextStateHash)
	if errFindNode == nil {
		nextNodeProgress = existingNodeByHash
		nextNodeProgressID = existingNodeByHash.ID
		s.logger.Info("Found existing PlayerProgress node matching the new state hash", append(logFields, zap.String("progressID", nextNodeProgressID.String()))...)
	} else if errors.Is(errFindNode, models.ErrNotFound) || errors.Is(errFindNode, pgx.ErrNoRows) {
		s.logger.Info("Creating new PlayerProgress node for the new state hash", logFields...)

		nextProgress.CurrentStateHash = nextStateHash
		nextProgress.UserID = gameState.PlayerID
		nextProgress.PublishedStoryID = gameState.PublishedStoryID
		nextProgress.StoryVariables = make(map[string]interface{})
		nextProgress.GlobalFlags = clearTransientFlags(nextProgress.GlobalFlags)

		savedID, errSaveNode := s.playerProgressRepo.Save(ctx, nextProgress)
		if errSaveNode != nil {
			s.logger.Error("Failed to save new PlayerProgress node", append(logFields, zap.Error(errSaveNode))...)
			return models.ErrInternalServer
		}
		nextNodeProgressID = savedID
		nextNodeProgress = nextProgress
		nextNodeProgress.ID = savedID
		s.logger.Info("Saved new PlayerProgress node", append(logFields, zap.String("progressID", nextNodeProgressID.String()))...)
	} else {
		s.logger.Error("Error checking for existing progress node by hash", append(logFields, zap.Error(errFindNode))...)
		return models.ErrInternalServer
	}

	var nextSceneID *uuid.UUID
	nextScene, errScene := s.sceneRepo.FindByStoryAndHash(ctx, gameState.PublishedStoryID, nextStateHash)

	if errScene == nil {
		nextSceneID = &nextScene.ID
		s.logger.Info("Next scene found in DB", append(logFields, zap.String("sceneID", nextSceneID.String()))...)

		gameState.PlayerStatus = models.PlayerStatusPlaying
		gameState.CurrentSceneID = uuid.NullUUID{UUID: *nextSceneID, Valid: true}

		gameState.PlayerProgressID = nextNodeProgressID
		gameState.LastActivityAt = time.Now().UTC()

		if _, err := s.playerGameStateRepo.Save(ctx, gameState); err != nil {
			s.logger.Error("Failed to update PlayerGameState after finding next scene", append(logFields, zap.Error(err))...)
			return models.ErrInternalServer
		}
		s.logger.Info("PlayerGameState updated to Playing, linked to existing scene")
		return nil

	} else if errors.Is(errScene, pgx.ErrNoRows) || errors.Is(errScene, models.ErrNotFound) {
		s.logger.Info("Next scene not found, initiating generation", logFields...)

		gameState.PlayerStatus = models.PlayerStatusGeneratingScene
		gameState.CurrentSceneID = uuid.NullUUID{Valid: false}
		gameState.PlayerProgressID = nextNodeProgressID
		gameState.LastActivityAt = time.Now().UTC()

		if _, err := s.playerGameStateRepo.Save(ctx, gameState); err != nil {
			s.logger.Error("Failed to update PlayerGameState to GeneratingScene", append(logFields, zap.Error(err))...)
			return models.ErrInternalServer
		}
		s.logger.Info("PlayerGameState updated to GeneratingScene")

		generationPayload, errGenPayload := createGenerationPayload(
			gameState.PlayerID,
			publishedStory,
			nextNodeProgress,
			gameState,
			madeChoicesInfo,
			nextStateHash,
		)
		if errGenPayload != nil {
			s.logger.Error("Failed to create generation payload", append(logFields, zap.Error(errGenPayload))...)
			return models.ErrInternalServer
		}
		generationPayload.GameStateID = gameState.ID.String()

		if errPub := s.publisher.PublishGenerationTask(ctx, generationPayload); errPub != nil {
			s.logger.Error("Failed to publish next scene generation task", append(logFields, zap.Error(errPub))...)
			return models.ErrInternalServer
		}
		s.logger.Info("Next scene generation task published", append(logFields, zap.String("taskID", generationPayload.TaskID))...)
		return nil

	} else {
		s.logger.Error("Error searching for next scene", append(logFields, zap.Error(errScene))...)
		return models.ErrInternalServer
	}
}

func (s *gameLoopServiceImpl) GetPlayerProgress(ctx context.Context, userID uuid.UUID, gameStateID uuid.UUID) (*models.PlayerProgress, error) {
	log := s.logger.With(zap.String("gameStateID", gameStateID.String()), zap.Stringer("userID", userID))
	log.Debug("GetPlayerProgress called")

	gameState, errState := s.playerGameStateRepo.GetByID(ctx, gameStateID)
	if errState != nil {
		if errors.Is(errState, models.ErrNotFound) || errors.Is(errState, pgx.ErrNoRows) {
			log.Warn("Player game state not found for GetPlayerProgress")
			return nil, models.ErrPlayerGameStateNotFound
		}
		log.Error("Failed to get player game state for GetPlayerProgress", zap.Error(errState))
		return nil, models.ErrInternalServer
	}

	if gameState.PlayerProgressID == uuid.Nil {
		log.Warn("Player game state exists but has no associated PlayerProgressID", zap.String("gameStateID", gameState.ID.String()))
		return nil, models.ErrNotFound
	}

	progress, errProgress := s.playerProgressRepo.GetByID(ctx, gameState.PlayerProgressID)
	if errProgress != nil {
		log.Error("Failed to get player progress node by ID from repository", zap.String("progressID", gameState.PlayerProgressID.String()), zap.Error(errProgress))
		if errors.Is(errProgress, pgx.ErrNoRows) || errors.Is(errProgress, models.ErrNotFound) {
			return nil, models.ErrNotFound
		}
		return nil, models.ErrInternalServer
	}

	log.Debug("Player progress node retrieved successfully", zap.String("progressID", progress.ID.String()))
	return progress, nil
}
