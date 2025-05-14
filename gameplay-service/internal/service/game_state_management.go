package service

import (
	"context"
	"errors"
	"novel-server/shared/database"
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

	states, err := s.playerGameStateRepo.ListByPlayerAndStory(ctx, s.pool, playerID, publishedStoryID)
	if err != nil {
		log.Error("Failed to list player game states from repository", zap.Error(err))
		return nil, models.ErrInternalServer
	}

	log.Info("Game states listed successfully", zap.Int("count", len(states)))
	return states, nil
}

// CreateNewGameState creates a new game state (save slot).
func (s *gameLoopServiceImpl) CreateNewGameState(ctx context.Context, playerID uuid.UUID, publishedStoryID uuid.UUID) (createdState *models.PlayerGameState, err error) {
	log := s.logger.With(zap.String("playerID", playerID.String()), zap.String("publishedStoryID", publishedStoryID.String()))
	log.Info("CreateNewGameState called")

	err = WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		gameStateRepoTx := database.NewPgPlayerGameStateRepository(tx, s.logger)
		publishedRepoTx := database.NewPgPublishedStoryRepository(tx, s.logger)
		progressRepoTx := database.NewPgPlayerProgressRepository(tx, s.logger)
		sceneRepoTx := database.NewPgStorySceneRepository(tx, s.logger)

		existingStates, errList := gameStateRepoTx.ListByPlayerAndStory(ctx, tx, playerID, publishedStoryID)
		if errList != nil {
			log.Error("Failed to list existing game states before creation", zap.Error(errList))
			err = models.ErrInternalServer
			return err
		}
		if len(existingStates) > 0 {
			log.Warn("Attempted to create new game state when one already exists", zap.Int("existingCount", len(existingStates)))
			err = models.ErrSaveSlotExists
			return err
		}

		publishedStory, storyErr := publishedRepoTx.GetByID(ctx, tx, publishedStoryID)
		if wrapErr := WrapRepoError(s.logger, storyErr, "PublishedStory"); wrapErr != nil {
			if errors.Is(wrapErr, models.ErrNotFound) {
				log.Error("Published story not found for creating new game state")
				return models.ErrStoryNotFound
			}
			return wrapErr
		}

		if publishedStory.Status != models.StatusReady {
			log.Warn("Attempt to create game state for story not in Ready status", zap.String("status", string(publishedStory.Status)))
			err = models.ErrStoryNotReady
			return err
		}

		// --- Upsert Initial Player Progress ---
		var initialProgressID uuid.UUID
		// Prepare the initial progress data
		initialStats := make(map[string]int)
		if publishedStory.Setup != nil && string(publishedStory.Setup) != "null" {
			var setupContent models.NovelSetupContent
			if err := DecodeStrictJSON(publishedStory.Setup, &setupContent); err == nil && setupContent.CoreStatsDefinition != nil {
				for key, statDef := range setupContent.CoreStatsDefinition {
					initialStats[key] = statDef.Initial
				}
				log.Debug("Initialized initial progress stats from story setup", zap.Any("initialStats", initialStats))
			}
		}
		newInitialProgress := &models.PlayerProgress{
			// ID will be set by repository if needed
			UserID:                playerID,
			PublishedStoryID:      publishedStoryID,
			CurrentStateHash:      models.InitialStateHash,
			CoreStats:             initialStats,
			SceneIndex:            0,
			EncounteredCharacters: []string{},
		}
		// Call UpsertInitial
		upsertedID, upsertErr := progressRepoTx.UpsertInitial(ctx, tx, newInitialProgress)
		if upsertErr != nil {
			log.Error("Failed to upsert initial player progress node", zap.Error(upsertErr))
			err = models.ErrInternalServer // Assign error to be handled by defer
			return err
		}
		initialProgressID = upsertedID
		log.Info("Initial PlayerProgress upserted/retrieved", zap.String("progressID", initialProgressID.String()))

		// --- Check initial scene ---
		var initialSceneID *uuid.UUID
		initialScene, sceneErr := sceneRepoTx.FindByStoryAndHash(ctx, tx, publishedStoryID, models.InitialStateHash)
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
			CurrentSceneID:   uuid.NullUUID{},
			PlayerStatus:     playerStatus,
			LastActivityAt:   now,
		}

		if initialSceneID != nil {
			newGameState.CurrentSceneID = uuid.NullUUID{UUID: *initialSceneID, Valid: true}
		}

		createdStateID, saveErr := gameStateRepoTx.Save(ctx, tx, newGameState)
		if saveErr != nil {
			log.Error("Error creating new player game state in repository", zap.Error(saveErr))
			err = models.ErrInternalServer
			return err
		}
		newGameState.ID = createdStateID

		log.Info("New player game state created successfully (transaction pending commit)", zap.String("gameStateID", createdStateID.String()))

		if newGameState.PlayerStatus == models.PlayerStatusGeneratingScene {
			log.Info("Initial scene generation task needed for the new game state (task should be published AFTER commit)")
			_, errPayload := createInitialSceneGenerationPayload(playerID, publishedStory, publishedStory.Language)
			if errPayload != nil {
				log.Error("Failed to create initial generation payload object (potential issue for task sending later)", zap.Error(errPayload))
			}
		}

		createdState = newGameState
		return nil
	})
	return createdState, err
}

// DeletePlayerGameState deletes a specific game state (save slot).
func (s *gameLoopServiceImpl) DeletePlayerGameState(ctx context.Context, userID uuid.UUID, gameStateID uuid.UUID) (err error) {
	log := s.logger.With(zap.String("gameStateID", gameStateID.String()), zap.Stringer("userID", userID))
	log.Info("Deleting player game state by ID")
	return WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		repo := database.NewPgPlayerGameStateRepository(tx, s.logger)
		err := repo.DeleteForUser(ctx, tx, gameStateID, userID)
		if wrapErr := WrapRepoError(s.logger, err, "PlayerGameState"); wrapErr != nil {
			if errors.Is(wrapErr, models.ErrNotFound) {
				log.Warn("Player game state not found or user forbidden to delete", zap.Error(err))
				return models.ErrPlayerGameStateNotFound
			}
			return wrapErr
		}
		log.Info("Player game state deleted successfully")
		return nil
	})
}
