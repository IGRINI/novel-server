package service

import (
	"context"
	"encoding/json"
	"errors"
	"novel-server/shared/models"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// ListGameStates lists all game states for a player and story.
func (s *gameLoopServiceImpl) ListGameStates(ctx context.Context, playerID uuid.UUID, publishedStoryID uuid.UUID) ([]*models.PlayerGameState, error) {
	log := s.logger.With(zap.String("playerID", playerID.String()), zap.String("publishedStoryID", publishedStoryID.String()))
	log.Info("ListGameStates called")

	states, err := s.playerGameStateRepo.ListByPlayerAndStory(ctx, playerID, publishedStoryID)
	if err != nil {
		log.Error("Failed to list player game states from repository", zap.Error(err))
		return nil, models.ErrInternalServer
	}

	log.Info("Game states listed successfully", zap.Int("count", len(states)))
	return states, nil
}

// CreateNewGameState creates a new game state (save slot).
func (s *gameLoopServiceImpl) CreateNewGameState(ctx context.Context, playerID uuid.UUID, publishedStoryID uuid.UUID) (*models.PlayerGameState, error) {
	log := s.logger.With(zap.String("playerID", playerID.String()), zap.String("publishedStoryID", publishedStoryID.String()))
	log.Info("CreateNewGameState called")

	existingStates, errList := s.playerGameStateRepo.ListByPlayerAndStory(ctx, playerID, publishedStoryID)
	if errList != nil {
		log.Error("Failed to list existing game states before creation", zap.Error(errList))
		return nil, models.ErrInternalServer
	}
	if len(existingStates) > 0 {
		log.Warn("Attempted to create new game state when one already exists", zap.Int("existingCount", len(existingStates)))
		return nil, models.ErrSaveSlotExists
	}

	publishedStory, storyErr := s.publishedRepo.GetByID(ctx, publishedStoryID)
	if storyErr != nil {
		if errors.Is(storyErr, pgx.ErrNoRows) {
			log.Error("Published story not found for creating new game state")
			return nil, models.ErrStoryNotFound
		}
		log.Error("Failed to get published story for creating new game state", zap.Error(storyErr))
		return nil, models.ErrInternalServer
	}

	if publishedStory.Status != models.StatusReady {
		log.Warn("Attempt to create game state for story not in Ready status", zap.String("status", string(publishedStory.Status)))
		return nil, models.ErrStoryNotReady
	}

	var initialProgressID uuid.UUID
	initialProgress, progressErr := s.playerProgressRepo.GetByStoryIDAndHash(ctx, publishedStoryID, models.InitialStateHash)

	if progressErr == nil {
		initialProgressID = initialProgress.ID
		log.Debug("Found existing initial PlayerProgress node", zap.String("progressID", initialProgressID.String()))
	} else if errors.Is(progressErr, models.ErrNotFound) || errors.Is(progressErr, pgx.ErrNoRows) {
		log.Info("Initial PlayerProgress node not found, creating it")
		initialStats := make(map[string]int)
		if publishedStory.Setup != nil && string(publishedStory.Setup) != "null" {
			var setupContent models.NovelSetupContent
			if setupErr := json.Unmarshal(publishedStory.Setup, &setupContent); setupErr == nil && setupContent.CoreStatsDefinition != nil {
				for key, statDef := range setupContent.CoreStatsDefinition {
					initialStats[key] = statDef.Initial
				}
				log.Debug("Initialized initial progress stats from story setup", zap.Any("initialStats", initialStats))
			}
		}

		newInitialProgress := &models.PlayerProgress{
			UserID:                playerID,
			PublishedStoryID:      publishedStoryID,
			CurrentStateHash:      models.InitialStateHash,
			CoreStats:             initialStats,
			StoryVariables:        make(map[string]interface{}),
			GlobalFlags:           []string{},
			SceneIndex:            0,
			EncounteredCharacters: []string{},
		}
		savedID, createErr := s.playerProgressRepo.Save(ctx, newInitialProgress)
		if createErr != nil {
			log.Error("Error creating initial player progress node in repository", zap.Error(createErr))
			return nil, models.ErrInternalServer
		}
		initialProgressID = savedID
		log.Info("Initial PlayerProgress node created successfully", zap.String("progressID", initialProgressID.String()))
	} else {
		log.Error("Unexpected error getting initial player progress node", zap.Error(progressErr))
		return nil, models.ErrInternalServer
	}

	var initialSceneID *uuid.UUID
	initialScene, sceneErr := s.sceneRepo.FindByStoryAndHash(ctx, publishedStoryID, models.InitialStateHash)
	if sceneErr == nil {
		initialSceneID = &initialScene.ID
		log.Debug("Found initial scene", zap.String("sceneID", initialSceneID.String()))
	} else if !errors.Is(sceneErr, pgx.ErrNoRows) && !errors.Is(sceneErr, models.ErrNotFound) {
		log.Error("Error fetching initial scene by hash", zap.Error(sceneErr))

	}

	now := time.Now().UTC()
	playerStatus := models.PlayerStatusPlaying
	if initialSceneID == nil {
		playerStatus = models.PlayerStatusGeneratingScene
		log.Warn("Initial scene not found for new game state, setting status to GeneratingScene")
	}
	newGameState := &models.PlayerGameState{
		PlayerID:         playerID,
		PublishedStoryID: publishedStoryID,

		PlayerProgressID: initialProgressID,

		CurrentSceneID: uuid.NullUUID{},
		PlayerStatus:   playerStatus,
		StartedAt:      now,
		LastActivityAt: now,
	}

	if initialSceneID != nil {
		newGameState.CurrentSceneID = uuid.NullUUID{UUID: *initialSceneID, Valid: true}
	}

	createdStateID, saveErr := s.playerGameStateRepo.Save(ctx, newGameState)
	if saveErr != nil {
		log.Error("Error creating new player game state in repository", zap.Error(saveErr))
		return nil, models.ErrInternalServer
	}
	newGameState.ID = createdStateID

	log.Info("New player game state created successfully", zap.String("gameStateID", createdStateID.String()))

	if newGameState.PlayerStatus == models.PlayerStatusGeneratingScene {
		log.Info("Publishing initial scene generation task for the new game state")
		generationPayload, errPayload := createInitialSceneGenerationPayload(playerID, publishedStory)
		if errPayload != nil {
			log.Error("Failed to create initial generation payload after creating game state", zap.Error(errPayload))
			newGameState.PlayerStatus = models.PlayerStatusError
			newGameState.ErrorDetails = models.StringPtr("Failed to create initial generation payload")
			if _, updateErr := s.playerGameStateRepo.Save(ctx, newGameState); updateErr != nil {
				log.Error("Failed to update game state to Error after payload creation failure", zap.Error(updateErr))
			}
			return nil, models.ErrInternalServer
		}

		if errPub := s.publisher.PublishGenerationTask(ctx, generationPayload); errPub != nil {
			log.Error("Error publishing initial scene generation task after creating game state", zap.Error(errPub))
			newGameState.PlayerStatus = models.PlayerStatusError
			newGameState.ErrorDetails = models.StringPtr("Failed to publish initial generation task")
			if _, updateErr := s.playerGameStateRepo.Save(ctx, newGameState); updateErr != nil {
				log.Error("Failed to update game state to Error after publish failure", zap.Error(updateErr))
			}
			return nil, models.ErrInternalServer
		}
		log.Info("Initial scene generation task published successfully for new game state", zap.String("taskID", generationPayload.TaskID))
	}

	return newGameState, nil
}

// DeletePlayerGameState deletes a specific game state (save slot).
func (s *gameLoopServiceImpl) DeletePlayerGameState(ctx context.Context, userID uuid.UUID, gameStateID uuid.UUID) error {
	log := s.logger.With(zap.String("gameStateID", gameStateID.String()), zap.Stringer("userID", userID))
	log.Info("Deleting player game state by ID")

	gameState, errState := s.playerGameStateRepo.GetByID(ctx, gameStateID)
	if errState != nil {
		if errors.Is(errState, models.ErrNotFound) || errors.Is(errState, pgx.ErrNoRows) {
			log.Warn("Player game state not found for deletion by ID")
			return models.ErrPlayerGameStateNotFound
		}
		log.Error("Failed to get player game state for deletion check", zap.Error(errState))
		return models.ErrInternalServer
	}

	if gameState.PlayerID != userID {
		log.Warn("Attempt to delete game state belonging to another user", zap.Stringer("ownerUserID", gameState.PlayerID))
		return models.ErrForbidden
	}

	err := s.playerGameStateRepo.Delete(ctx, gameStateID)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {

			log.Warn("Player game state not found for deletion by ID (unexpected after check)")
			return models.ErrPlayerGameStateNotFound
		}

		log.Error("Error deleting player game state by ID from repository", zap.Error(err))
		return models.ErrInternalServer
	}

	log.Info("Player game state deleted successfully by ID")
	return nil
}
