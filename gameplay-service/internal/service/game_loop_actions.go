package service

import (
	"context"
	"errors"
	"fmt"
	"novel-server/shared/database"
	"novel-server/shared/models"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

func (s *gameLoopServiceImpl) GetStoryScene(ctx context.Context, userID uuid.UUID, gameStateID uuid.UUID) (*models.StoryScene, error) {
	log := s.logger.With(zap.String("gameStateID", gameStateID.String()), zap.Stringer("userID", userID))
	log.Info("GetStoryScene called")

	gameState, err := s.fetchAndAuthorizeGameState(ctx, userID, gameStateID)
	if err != nil {
		if errors.Is(err, models.ErrPlayerGameStateNotFound) {
			log.Warn("Player game state not found by ID", zap.Error(err))
		}
		return nil, err
	}

	switch gameState.PlayerStatus {
	case models.PlayerStatusGeneratingScene:
		log.Info("Player is waiting for scene generation")
		return nil, models.ErrSceneNeedsGeneration
	case models.PlayerStatusGameOverPending:
		log.Info("Player is waiting for game over generation or game is over")
		if !gameState.CurrentSceneID.Valid {
			log.Info("Player is waiting for game over scene generation (CurrentSceneID is nil)")
			return nil, models.ErrGameOverPending
		}
		// Fetch the final scene (ending text)
		finalScene, errScene := s.sceneRepo.GetByID(ctx, s.pool, gameState.CurrentSceneID.UUID)
		if errScene != nil {
			if errors.Is(errScene, models.ErrNotFound) || errors.Is(errScene, pgx.ErrNoRows) {
				log.Error("Could not find final scene for completed game state", zap.Stringer("sceneID", gameState.CurrentSceneID.UUID), zap.Error(errScene))
				return nil, models.ErrInternalServer // Scene should exist
			}
			log.Error("Failed to get final scene by ID for completed game", zap.Stringer("sceneID", gameState.CurrentSceneID.UUID), zap.Error(errScene))
			return nil, models.ErrInternalServer
		}
		log.Info("Returning final scene for completed game")
		return finalScene, nil // Return the final scene containing the ending text
	case models.PlayerStatusCompleted:
		log.Info("Player has finished the game, attempting to fetch final scene")
		if !gameState.CurrentSceneID.Valid {
			log.Error("Player game state is Finished, but CurrentSceneID is nil", zap.Stringer("gameStateID", gameStateID))
			return nil, models.ErrInternalServer // Should not happen if game over was processed correctly
		}
		// Fetch the final scene (ending text)
		finalScene, errScene := s.sceneRepo.GetByID(ctx, s.pool, gameState.CurrentSceneID.UUID)
		if errScene != nil {
			if errors.Is(errScene, models.ErrNotFound) || errors.Is(errScene, pgx.ErrNoRows) {
				log.Error("Could not find final scene for completed game state", zap.Stringer("sceneID", gameState.CurrentSceneID.UUID), zap.Error(errScene))
				return nil, models.ErrInternalServer // Scene should exist
			}
			log.Error("Failed to get final scene by ID for completed game", zap.Stringer("sceneID", gameState.CurrentSceneID.UUID), zap.Error(errScene))
			return nil, models.ErrInternalServer
		}
		log.Info("Returning final scene for completed game")
		return finalScene, nil // Return the final scene containing the ending text
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

	scene, err := s.sceneRepo.GetByID(ctx, s.pool, gameState.CurrentSceneID.UUID)
	if wrapErr := WrapRepoError(s.logger, err, "StoryScene"); wrapErr != nil {
		if errors.Is(wrapErr, models.ErrNotFound) {
			log.Error("CRITICAL: CurrentSceneID from game state not found in scene repository", zap.String("sceneID", gameState.CurrentSceneID.UUID.String()), zap.Error(err))
			return nil, models.ErrSceneNotFound
		}
		return nil, wrapErr
	}

	log.Info("Scene found and returned", zap.String("sceneID", scene.ID.String()))
	return scene, nil
}

func (s *gameLoopServiceImpl) MakeChoice(ctx context.Context, userID uuid.UUID, gameStateID uuid.UUID, selectedOptionIndices []int) error {
	return WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		logFields := []zap.Field{
			zap.String("gameStateID", gameStateID.String()),
			zap.Stringer("userID", userID),
			zap.Any("selectedOptionIndices", selectedOptionIndices),
		}
		s.logger.Info("MakeChoice called", logFields...)

		gs, cp, sc, ps, err := s.makeChoiceInit(ctx, tx, userID, gameStateID, selectedOptionIndices, logFields)
		if err != nil {
			return err
		}

		np, mc, gameOver, goStat, err := s.makeChoiceApplyConsequences(cp, sc, ps, selectedOptionIndices, logFields)
		if err != nil {
			return err
		}

		if gameOver {
			return s.makeChoiceHandleGameOver(ctx, tx, gs, cp, np, mc, goStat, logFields)
		}
		return s.makeChoiceHandleContinue(ctx, tx, gs, cp, np, mc, logFields)
	})
}

func (s *gameLoopServiceImpl) GetPlayerProgress(ctx context.Context, userID uuid.UUID, gameStateID uuid.UUID) (*models.PlayerProgress, error) {
	log := s.logger.With(zap.String("gameStateID", gameStateID.String()), zap.Stringer("userID", userID))
	log.Debug("GetPlayerProgress called")

	gameState, err := s.playerGameStateRepo.GetByID(ctx, s.pool, gameStateID)
	if wrapErr := WrapRepoError(s.logger, err, "PlayerGameState"); wrapErr != nil {
		if errors.Is(wrapErr, models.ErrNotFound) {
			log.Warn("Player game state not found for GetPlayerProgress")
			return nil, models.ErrPlayerGameStateNotFound
		}
		return nil, wrapErr
	}

	if gameState.PlayerID != userID {
		log.Warn("User attempted to access progress from game state they do not own", zap.Stringer("ownerID", gameState.PlayerID))
		return nil, models.ErrForbidden
	}

	if gameState.PlayerProgressID == uuid.Nil {
		log.Warn("Player game state exists but has no associated PlayerProgressID", zap.String("gameStateID", gameState.ID.String()))
		return nil, models.ErrNotFound
	}

	progress, err := s.playerProgressRepo.GetByID(ctx, s.pool, gameState.PlayerProgressID)
	if wrapErr := WrapRepoError(s.logger, err, "PlayerProgress"); wrapErr != nil {
		if errors.Is(wrapErr, models.ErrNotFound) {
			return nil, models.ErrNotFound
		}
		return nil, wrapErr
	}

	log.Debug("Player progress node retrieved successfully", zap.String("progressID", progress.ID.String()))
	return progress, nil
}

func (s *gameLoopServiceImpl) makeChoiceInit(ctx context.Context, tx pgx.Tx, userID, gameStateID uuid.UUID, selectedOptionIndices []int, logFields []zap.Field) (*models.PlayerGameState, *models.PlayerProgress, *models.StoryScene, *models.PublishedStory, error) {
	gameStateRepo := database.NewPgPlayerGameStateRepository(tx, s.logger)
	progressRepo := database.NewPgPlayerProgressRepository(tx, s.logger)
	sceneRepo := database.NewPgStorySceneRepository(tx, s.logger)
	publishedRepo := database.NewPgPublishedStoryRepository(tx, s.logger)

	gs, err := gameStateRepo.GetByID(ctx, tx, gameStateID)
	if wrap := WrapRepoError(s.logger, err, "PlayerGameState"); wrap != nil {
		if errors.Is(wrap, models.ErrNotFound) {
			s.logger.Warn("Player game state not found for MakeChoice")
			return nil, nil, nil, nil, models.ErrPlayerGameStateNotFound
		}
		return nil, nil, nil, nil, wrap
	}
	if gs.PlayerID != userID {
		s.logger.Warn("User attempted to access game state they do not own in MakeChoice", zap.Stringer("ownerID", gs.PlayerID))
		return nil, nil, nil, nil, models.ErrForbidden
	}
	if gs.PlayerStatus != models.PlayerStatusPlaying {
		s.logger.Warn("Attempt to make choice while not in Playing status", append(logFields, zap.String("playerStatus", string(gs.PlayerStatus)))...)
		if gs.PlayerStatus == models.PlayerStatusGeneratingScene {
			return nil, nil, nil, nil, models.ErrSceneNeedsGeneration
		}
		return nil, nil, nil, nil, models.ErrBadRequest
	}
	if gs.PlayerProgressID == uuid.Nil {
		s.logger.Error("CRITICAL: PlayerStatus is Playing, but PlayerProgressID is Nil", append(logFields, zap.String("gameStateID", gs.ID.String()))...)
		return nil, nil, nil, nil, models.ErrInternalServer
	}
	if !gs.CurrentSceneID.Valid {
		s.logger.Error("CRITICAL: PlayerStatus is Playing, but CurrentSceneID is Nil", append(logFields, zap.String("gameStateID", gs.ID.String()))...)
		return nil, nil, nil, nil, models.ErrSceneNotFound
	}
	cp, err := progressRepo.GetByID(ctx, tx, gs.PlayerProgressID)
	if wrap := WrapRepoError(s.logger, err, "PlayerProgress"); wrap != nil {
		return nil, nil, nil, nil, wrap
	}
	sc, err := sceneRepo.GetByID(ctx, tx, gs.CurrentSceneID.UUID)
	if wrap := WrapRepoError(s.logger, err, "StoryScene"); wrap != nil {
		if errors.Is(wrap, models.ErrNotFound) {
			s.logger.Error("Current scene not found for MakeChoice", append(logFields, zap.Error(err))...)
			return nil, nil, nil, nil, models.ErrSceneNotFound
		}
		return nil, nil, nil, nil, wrap
	}
	ps, err := publishedRepo.GetByID(ctx, tx, gs.PublishedStoryID)
	if wrap := WrapRepoError(s.logger, err, "PublishedStory"); wrap != nil {
		return nil, nil, nil, nil, wrap
	}
	if ps.Setup == nil {
		s.logger.Error("CRITICAL: PublishedStory Setup is nil", logFields...)
		return nil, nil, nil, nil, models.ErrInternalServer
	}
	return gs, cp, sc, ps, nil
}

func (s *gameLoopServiceImpl) makeChoiceApplyConsequences(cp *models.PlayerProgress, sc *models.StoryScene, ps *models.PublishedStory, selected []int, logFields []zap.Field) (*models.PlayerProgress, []models.UserChoiceInfo, bool, string, error) {
	var sceneData sceneContentChoices
	if err := DecodeStrictJSON(sc.Content, &sceneData); err != nil {
		s.logger.Error("Failed to decode current scene content", append(logFields, zap.Error(err))...)
		return nil, nil, false, "", models.ErrInternalServer
	}
	if len(selected) == 0 {
		s.logger.Warn("Player sent empty choice array", logFields...)
		return nil, nil, false, "", fmt.Errorf("%w: selected_option_indices cannot be empty", models.ErrBadRequest)
	}
	if len(selected) > len(sceneData.Choices) {
		s.logger.Warn("Player sent more choices than available", append(logFields, zap.Int("sceneChoices", len(sceneData.Choices)), zap.Int("playerChoices", len(selected)))...)
		return nil, nil, false, "", fmt.Errorf("%w: received %d choice indices, but scene only has %d choice blocks", models.ErrBadRequest, len(selected), len(sceneData.Choices))
	}
	next := &models.PlayerProgress{
		UserID:                cp.UserID,
		PublishedStoryID:      cp.PublishedStoryID,
		CoreStats:             make(map[string]int),
		SceneIndex:            cp.SceneIndex + 1,
		EncounteredCharacters: append([]string{}, cp.EncounteredCharacters...),
	}
	for k, v := range cp.CoreStats {
		next.CoreStats[k] = v
	}
	var made []models.UserChoiceInfo
	isOver := false
	var overStat string
	for i, idx := range selected {
		block := sceneData.Choices[i]
		opt := block.Options[idx]
		var rtPtr *string
		if opt.Consequences.ResponseText != "" {
			val := opt.Consequences.ResponseText
			rtPtr = &val
		}
		made = append(made, models.UserChoiceInfo{Desc: block.Description, Text: opt.Text, ResponseText: rtPtr})
		stat, triggered := applyConsequences(next, opt.Consequences, &models.NovelSetupContent{})
		if triggered {
			isOver = true
			overStat = stat
			break
		}
	}
	return next, made, isOver, overStat, nil
}

func (s *gameLoopServiceImpl) makeChoiceHandleGameOver(ctx context.Context, tx pgx.Tx, gs *models.PlayerGameState, cp *models.PlayerProgress, next *models.PlayerProgress, made []models.UserChoiceInfo, stat string, logFields []zap.Field) error {
	gameStateRepoTx := database.NewPgPlayerGameStateRepository(tx, s.logger)
	playerProgressRepoTx := database.NewPgPlayerProgressRepository(tx, s.logger)
	publishedRepoTx := database.NewPgPublishedStoryRepository(tx, s.logger)
	publishedStory, err := publishedRepoTx.GetByID(ctx, tx, gs.PublishedStoryID)
	if wrap := WrapRepoError(s.logger, err, "PublishedStory"); wrap != nil {
		return wrap
	}
	s.logger.Debug("Data before calculateStateHash (Game Over)", zap.String("gameStateID", gs.ID.String()), zap.String("previousHash", cp.CurrentStateHash), zap.Any("coreStats", next.CoreStats))
	finalStateHash, hashErr := calculateStateHash(cp.CurrentStateHash, next.CoreStats)
	if hashErr != nil {
		s.logger.Error("Failed to calculate final state hash before game over", append(logFields, zap.Error(hashErr))...)
		return models.ErrInternalServer
	}
	next.CurrentStateHash = finalStateHash
	existingFinalNode, errFind := playerProgressRepoTx.GetByStoryIDAndHash(ctx, tx, gs.PublishedStoryID, finalStateHash)
	var finalProgressNodeID uuid.UUID
	if errFind == nil {
		finalProgressNodeID = existingFinalNode.ID
		s.logger.Debug("Final progress node before game over already exists", append(logFields, zap.String("progressID", finalProgressNodeID.String()))...)
	} else if errors.Is(errFind, models.ErrNotFound) || errors.Is(errFind, pgx.ErrNoRows) {
		next.UserID = gs.PlayerID
		savedID, errSave := playerProgressRepoTx.Save(ctx, tx, next)
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
	gs.PlayerStatus = models.PlayerStatusGameOverPending
	gs.PlayerProgressID = finalProgressNodeID
	gs.CurrentSceneID = uuid.NullUUID{Valid: false}
	gs.LastActivityAt = now
	if _, err := gameStateRepoTx.Save(ctx, tx, gs); err != nil {
		s.logger.Error("Failed to update PlayerGameState to GameOverPending", append(logFields, zap.Error(err))...)
		return models.ErrInternalServer
	}
	generationPayload, errGenPayload := createGenerationPayload(gs.PlayerID, publishedStory, next, gs, made, finalStateHash, publishedStory.Language, models.PromptTypeNovelGameOverCreator)
	if errGenPayload != nil {
		s.logger.Error("Failed to create generation payload for game over task", append(logFields, zap.Error(errGenPayload))...)
		return models.ErrInternalServer
	}
	generationPayload.GameStateID = gs.ID.String()
	if err := s.publisher.PublishGenerationTask(ctx, generationPayload); err != nil {
		s.logger.Error("Failed to publish game over generation task (as GenerationTask)", append(logFields, zap.Error(err))...)
		return models.ErrInternalServer
	}
	s.logger.Info("Game over generation task published (as GenerationTask)", append(logFields, zap.String("taskID", generationPayload.TaskID))...)
	return nil
}

func (s *gameLoopServiceImpl) makeChoiceHandleContinue(ctx context.Context, tx pgx.Tx, gs *models.PlayerGameState, cp *models.PlayerProgress, next *models.PlayerProgress, made []models.UserChoiceInfo, logFields []zap.Field) error {
	gameStateRepoTx := database.NewPgPlayerGameStateRepository(tx, s.logger)
	playerProgressRepoTx := database.NewPgPlayerProgressRepository(tx, s.logger)
	sceneRepoTx := database.NewPgStorySceneRepository(tx, s.logger)
	publishedRepoTx := database.NewPgPublishedStoryRepository(tx, s.logger)
	s.logger.Debug("Data before calculateStateHash (Normal Flow)", zap.String("gameStateID", gs.ID.String()), zap.String("previousHash", cp.CurrentStateHash), zap.Any("coreStats", next.CoreStats))
	nextStateHash, hashErr := calculateStateHash(cp.CurrentStateHash, next.CoreStats)
	if hashErr != nil {
		s.logger.Error("Failed to calculate next state hash", append(logFields, zap.Error(hashErr))...)
		return models.ErrInternalServer
	}
	next.CurrentStateHash = nextStateHash
	logFields = append(logFields, zap.String("nextStateHash", nextStateHash))
	s.logger.Debug("Calculated next state hash", logFields...)
	progressToUpsert := *next
	progressToUpsert.ID = uuid.Nil
	progressToUpsert.UserID = gs.PlayerID
	progressToUpsert.PublishedStoryID = gs.PublishedStoryID
	progressToUpsert.LastStorySummary = cp.CurrentSceneSummary
	upsertedProgressID, errUpsert := playerProgressRepoTx.UpsertByHash(ctx, tx, &progressToUpsert)
	if errUpsert != nil {
		s.logger.Error("Failed to upsert PlayerProgress node by hash", append(logFields, zap.Error(errUpsert))...)
		return models.ErrInternalServer
	}
	nextNodeProgressID := upsertedProgressID
	s.logger.Info("Upserted/Retrieved PlayerProgress node by hash", append(logFields, zap.String("progressID", nextNodeProgressID.String()))...)
	nextScene, errScene := sceneRepoTx.FindByStoryAndHash(ctx, tx, gs.PublishedStoryID, nextStateHash)
	if errScene == nil {
		gs.PlayerStatus = models.PlayerStatusPlaying
		gs.CurrentSceneID = uuid.NullUUID{UUID: nextScene.ID, Valid: true}
		gs.PlayerProgressID = nextNodeProgressID
		gs.LastActivityAt = time.Now().UTC()
		if _, err := gameStateRepoTx.Save(ctx, tx, gs); err != nil {
			s.logger.Error("Failed to update PlayerGameState after finding next scene", append(logFields, zap.Error(err))...)
			return models.ErrInternalServer
		}
		s.logger.Info("PlayerGameState updated to Playing, linked to existing scene (pending commit)", append(logFields, zap.String("sceneID", nextScene.ID.String()))...)
		return nil
	}
	if errors.Is(errScene, pgx.ErrNoRows) || errors.Is(errScene, models.ErrNotFound) {
		gs.PlayerStatus = models.PlayerStatusGeneratingScene
		gs.CurrentSceneID = uuid.NullUUID{Valid: false}
		gs.PlayerProgressID = nextNodeProgressID
		gs.LastActivityAt = time.Now().UTC()
		if _, err := gameStateRepoTx.Save(ctx, tx, gs); err != nil {
			s.logger.Error("Failed to update PlayerGameState to GeneratingScene", append(logFields, zap.Error(err))...)
			return models.ErrInternalServer
		}
		publishedStory, err := publishedRepoTx.GetByID(ctx, tx, gs.PublishedStoryID)
		if wrap := WrapRepoError(s.logger, err, "PublishedStory"); wrap != nil {
			return wrap
		}
		generationPayload, errGenPayload := createGenerationPayload(gs.PlayerID, publishedStory, next, gs, made, nextStateHash, publishedStory.Language, models.PromptTypeStoryContinuation)
		if errGenPayload != nil {
			s.logger.Error("Failed to create generation payload for next scene", append(logFields, zap.Error(errGenPayload))...)
			return models.ErrInternalServer
		}
		generationPayload.GameStateID = gs.ID.String()
		if errPub := s.publisher.PublishGenerationTask(ctx, generationPayload); errPub != nil {
			s.logger.Error("Failed to publish next scene generation task", append(logFields, zap.Error(errPub))...)
			return models.ErrInternalServer
		}
		s.logger.Info("Next scene generation task published", append(logFields, zap.String("taskID", generationPayload.TaskID))...)
		return nil
	}
	return models.ErrInternalServer
}
