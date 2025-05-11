package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"novel-server/shared/database"
	"novel-server/shared/models"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

func (s *gameLoopServiceImpl) GetStoryScene(ctx context.Context, userID uuid.UUID, gameStateID uuid.UUID) (*models.StoryScene, error) {
	log := s.logger.With(zap.String("gameStateID", gameStateID.String()), zap.Stringer("userID", userID))
	log.Info("GetStoryScene called")

	gameState, errState := s.playerGameStateRepo.GetByID(ctx, s.pool, gameStateID)
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
		log.Info("Player has completed the game, attempting to fetch final scene")
		if !gameState.CurrentSceneID.Valid {
			log.Error("Player game state is Completed, but CurrentSceneID is nil", zap.Stringer("gameStateID", gameStateID))
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

	scene, errScene := s.sceneRepo.GetByID(ctx, s.pool, gameState.CurrentSceneID.UUID)
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

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.logger.Error("Failed to begin transaction for MakeChoice", zap.Error(err))
		return models.ErrInternalServer
	}
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("Panic recovered during MakeChoice, rolling back transaction", zap.Any("panic", r))
			_ = tx.Rollback(context.Background())
			err = fmt.Errorf("panic during MakeChoice: %v", r)
		} else if err != nil {
			s.logger.Warn("Rolling back transaction due to error during MakeChoice", zap.Error(err))
			if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
				s.logger.Error("Failed to rollback MakeChoice transaction", zap.Error(rollbackErr))
			}
		} else {
			s.logger.Info("Attempting to commit MakeChoice transaction")
			if commitErr := tx.Commit(ctx); commitErr != nil {
				s.logger.Error("Failed to commit MakeChoice transaction", zap.Error(commitErr))
				err = fmt.Errorf("error committing MakeChoice transaction: %w", commitErr)
			} else {
				s.logger.Info("MakeChoice transaction committed successfully")
			}
		}
	}()

	gameStateRepoTx := database.NewPgPlayerGameStateRepository(tx, s.logger)
	playerProgressRepoTx := database.NewPgPlayerProgressRepository(tx, s.logger)
	sceneRepoTx := database.NewPgStorySceneRepository(tx, s.logger)
	publishedRepoTx := database.NewPgPublishedStoryRepository(tx, s.logger)

	gameState, errState := gameStateRepoTx.GetByID(ctx, tx, gameStateID)
	if errState != nil {
		if errors.Is(errState, models.ErrNotFound) || errors.Is(errState, pgx.ErrNoRows) {
			s.logger.Warn("Player game state not found for MakeChoice")
			err = models.ErrPlayerGameStateNotFound
		} else {
			s.logger.Error("Failed to get player game state for MakeChoice", zap.Error(errState))
			err = models.ErrInternalServer
		}
		return err
	}

	if gameState.PlayerID != userID {
		s.logger.Warn("User attempted to access game state they do not own in MakeChoice", zap.Stringer("ownerID", gameState.PlayerID))
		err = models.ErrForbidden
		return err
	}

	if gameState.PlayerStatus != models.PlayerStatusPlaying {
		s.logger.Warn("Attempt to make choice while not in Playing status", append(logFields, zap.String("playerStatus", string(gameState.PlayerStatus)))...)
		if gameState.PlayerStatus == models.PlayerStatusGeneratingScene {
			err = models.ErrSceneNeedsGeneration
		} else {
			err = models.ErrBadRequest
		}
		return err
	}

	if gameState.PlayerProgressID == uuid.Nil {
		s.logger.Error("CRITICAL: PlayerStatus is Playing, but PlayerProgressID is Nil", append(logFields, zap.String("gameStateID", gameState.ID.String()))...)
		err = models.ErrInternalServer
		return err
	}
	if !gameState.CurrentSceneID.Valid {
		s.logger.Error("CRITICAL: PlayerStatus is Playing, but CurrentSceneID is Nil", append(logFields, zap.String("gameStateID", gameState.ID.String()))...)
		err = models.ErrSceneNotFound
		return err
	}

	currentProgressID := gameState.PlayerProgressID
	currentSceneID := gameState.CurrentSceneID.UUID
	logFields = append(logFields, zap.String("currentProgressID", currentProgressID.String()), zap.String("currentSceneID", currentSceneID.String()))

	currentProgress, errProgress := playerProgressRepoTx.GetByID(ctx, tx, currentProgressID)
	if errProgress != nil {
		s.logger.Error("Failed to get current PlayerProgress node linked in game state", append(logFields, zap.Error(errProgress))...)
		err = models.ErrInternalServer
		return err
	}

	currentScene, errScene := sceneRepoTx.GetByID(ctx, tx, currentSceneID)
	if errScene != nil {
		s.logger.Error("Failed to get current Scene linked in game state", append(logFields, zap.Error(errScene))...)
		err = models.ErrInternalServer
		return err
	}

	publishedStory, errStory := publishedRepoTx.GetByID(ctx, tx, gameState.PublishedStoryID)
	if errStory != nil {
		s.logger.Error("Failed to get published story associated with game state", append(logFields, zap.Error(errStory))...)
		err = models.ErrInternalServer
		return err
	}

	var sceneData sceneContentChoices
	if err := json.Unmarshal(currentScene.Content, &sceneData); err != nil {
		s.logger.Error("Failed to unmarshal current scene content", append(logFields, zap.Error(err))...)
		err = models.ErrInternalServer
		return err
	}

	if len(selectedOptionIndices) == 0 {
		s.logger.Warn("Player sent empty choice array", logFields...)
		err = fmt.Errorf("%w: selected_option_indices cannot be empty", models.ErrBadRequest)
		return err
	}
	if len(selectedOptionIndices) > len(sceneData.Choices) {
		s.logger.Warn("Player sent more choices than available in the scene", append(logFields, zap.Int("sceneChoices", len(sceneData.Choices)), zap.Int("playerChoices", len(selectedOptionIndices)))...)
		err = fmt.Errorf("%w: received %d choice indices, but scene only has %d choice blocks", models.ErrBadRequest, len(selectedOptionIndices), len(sceneData.Choices))
		return err
	}

	if publishedStory.Setup == nil {
		s.logger.Error("CRITICAL: PublishedStory Setup is nil", logFields...)
		err = models.ErrInternalServer
		return err
	}
	var setupContent models.NovelSetupContent
	if err := json.Unmarshal(publishedStory.Setup, &setupContent); err != nil {
		s.logger.Error("Failed to unmarshal NovelSetup content", append(logFields, zap.Error(err))...)
		err = models.ErrInternalServer
		return err
	}

	nextProgress := &models.PlayerProgress{
		UserID:                currentProgress.UserID,
		PublishedStoryID:      currentProgress.PublishedStoryID,
		CoreStats:             make(map[string]int),
		SceneIndex:            currentProgress.SceneIndex + 1,
		EncounteredCharacters: make([]string, 0, len(currentProgress.EncounteredCharacters)),
	}
	for k, v := range currentProgress.CoreStats {
		nextProgress.CoreStats[k] = v
	}
	nextProgress.EncounteredCharacters = append(nextProgress.EncounteredCharacters, currentProgress.EncounteredCharacters...)

	var isGameOver bool
	var gameOverStat string
	madeChoicesInfo := make([]models.UserChoiceInfo, 0, len(selectedOptionIndices))
	for i, selectedIndex := range selectedOptionIndices {
		if i >= len(sceneData.Choices) {
			s.logger.Error("Logic error: index out of bounds accessing sceneData.Choices", append(logFields, zap.Int("index", i), zap.Int("sceneChoicesLen", len(sceneData.Choices)))...)
			err = models.ErrInternalServer
			return err
		}
		choiceBlock := sceneData.Choices[i]

		if selectedIndex < 0 || selectedIndex >= len(choiceBlock.Options) {
			s.logger.Warn("Invalid selected option index for choice block", append(logFields, zap.Int("choiceBlockIndex", i), zap.Int("selectedIndex", selectedIndex), zap.Int("optionsAvailable", len(choiceBlock.Options)))...)
			err = fmt.Errorf("%w: invalid index %d for choice block %d (options: %d)", models.ErrInvalidChoice, selectedIndex, i, len(choiceBlock.Options))
			return err
		}
		selectedOption := choiceBlock.Options[selectedIndex]
		var rtPtr *string
		if selectedOption.Consequences.ResponseText != "" {
			rtVal := selectedOption.Consequences.ResponseText
			rtPtr = &rtVal
		}
		madeChoicesInfo = append(madeChoicesInfo, models.UserChoiceInfo{
			Desc:         choiceBlock.Description,
			Text:         selectedOption.Text,
			ResponseText: rtPtr,
		})
		statCausingGameOver, gameOverTriggered := applyConsequences(nextProgress, selectedOption.Consequences, &setupContent)

		charStr := strconv.Itoa(choiceBlock.Char)
		charFound := false
		for _, encounteredChar := range nextProgress.EncounteredCharacters {
			if encounteredChar == charStr {
				charFound = true
				break
			}
		}
		if !charFound {
			nextProgress.EncounteredCharacters = append(nextProgress.EncounteredCharacters, charStr)
			s.logger.Debug("Added new encountered character", append(logFields, zap.String("character", charStr))...)
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
		err = fmt.Errorf("%w: not all choices were made (made %d, available %d) and game over condition not met",
			models.ErrBadRequest, len(selectedOptionIndices), len(sceneData.Choices))
		return err
	}

	if isGameOver {
		s.logger.Info("Handling Game Over state update")

		s.logger.Debug("Data before calculateStateHash (Game Over)",
			zap.String("gameStateID", gameStateID.String()),
			zap.String("previousHash", currentProgress.CurrentStateHash),
			zap.Any("coreStats", nextProgress.CoreStats),
		)

		finalStateHash, hashErr := calculateStateHash(currentProgress.CurrentStateHash, nextProgress.CoreStats)
		if hashErr != nil {
			s.logger.Error("Failed to calculate final state hash before game over", append(logFields, zap.Error(hashErr))...)
			err = models.ErrInternalServer
			return err
		}
		nextProgress.CurrentStateHash = finalStateHash

		existingFinalNode, errFind := playerProgressRepoTx.GetByStoryIDAndHash(ctx, tx, gameState.PublishedStoryID, finalStateHash)
		var finalProgressNodeID uuid.UUID

		if errFind == nil {
			finalProgressNodeID = existingFinalNode.ID
			s.logger.Debug("Final progress node before game over already exists", append(logFields, zap.String("progressID", finalProgressNodeID.String()))...)
		} else if errors.Is(errFind, models.ErrNotFound) || errors.Is(errFind, pgx.ErrNoRows) {

			nextProgress.UserID = gameState.PlayerID

			savedID, errSave := playerProgressRepoTx.Save(ctx, tx, nextProgress)
			if errSave != nil {
				s.logger.Error("Failed to save final player progress node before game over", append(logFields, zap.Error(errSave))...)
				err = models.ErrInternalServer
				return err
			}
			finalProgressNodeID = savedID
			s.logger.Info("Saved new final PlayerProgress node before game over", append(logFields, zap.String("progressID", finalProgressNodeID.String()))...)
		} else {
			s.logger.Error("Error checking for existing final progress node before game over", append(logFields, zap.Error(errFind))...)
			err = models.ErrInternalServer
			return err
		}

		now := time.Now().UTC()
		gameState.PlayerStatus = models.PlayerStatusGameOverPending
		gameState.PlayerProgressID = finalProgressNodeID
		gameState.CurrentSceneID = uuid.NullUUID{Valid: false}
		gameState.LastActivityAt = now

		if _, err := gameStateRepoTx.Save(ctx, tx, gameState); err != nil {
			s.logger.Error("Failed to update PlayerGameState to GameOverPending", append(logFields, zap.Error(err))...)
			err = models.ErrInternalServer
			return err
		}

		generationPayload, errGenPayload := createGenerationPayload(
			gameState.PlayerID,
			publishedStory,
			nextProgress,
			gameState,
			madeChoicesInfo,
			finalStateHash,
			publishedStory.Language,
			models.PromptTypeNovelGameOverCreator,
		)
		if errGenPayload != nil {
			s.logger.Error("Failed to create generation payload for game over task", append(logFields, zap.Error(errGenPayload))...)
			err = models.ErrInternalServer
			return err
		}
		generationPayload.GameStateID = gameState.ID.String()

		if err := s.publisher.PublishGenerationTask(ctx, generationPayload); err != nil {
			s.logger.Error("Failed to publish game over generation task (as GenerationTask)", append(logFields, zap.Error(err))...)
			err = models.ErrInternalServer
			return err
		}
		s.logger.Info("Game over generation task published (as GenerationTask)", append(logFields, zap.String("taskID", generationPayload.TaskID))...)
		return nil
	}

	s.logger.Debug("Data before calculateStateHash (Normal Flow)",
		zap.String("gameStateID", gameStateID.String()),
		zap.String("previousHash", currentProgress.CurrentStateHash),
		zap.Any("coreStats", nextProgress.CoreStats),
	)
	nextStateHash, hashErr := calculateStateHash(currentProgress.CurrentStateHash, nextProgress.CoreStats)
	if hashErr != nil {
		s.logger.Error("Failed to calculate next state hash", append(logFields, zap.Error(hashErr))...)
		err = models.ErrInternalServer
		return err
	}
	nextProgress.CurrentStateHash = nextStateHash
	logFields = append(logFields, zap.String("nextStateHash", nextStateHash))
	s.logger.Debug("Calculated next state hash", logFields...)

	var nextNodeProgressID uuid.UUID

	// --- UPSERT next PlayerProgress node using the new state hash ---
	// Prepare the progress node to be upserted
	progressToUpsert := *nextProgress            // Make a copy to avoid modifying nextProgress directly here
	progressToUpsert.ID = uuid.Nil               // Let Upsert handle ID generation if needed
	progressToUpsert.UserID = gameState.PlayerID // Ensure UserID is set
	progressToUpsert.PublishedStoryID = gameState.PublishedStoryID
	// Clear transient fields before potentially saving a new node
	progressToUpsert.LastStorySummary = currentProgress.CurrentSceneSummary // Use previous scene summary as last summary
	// TODO: Fill LastFutureDirection and LastVarImpactSummary if available from generation result

	upsertedProgressID, errUpsert := playerProgressRepoTx.UpsertByHash(ctx, tx, &progressToUpsert)
	if errUpsert != nil {
		s.logger.Error("Failed to upsert PlayerProgress node by hash", append(logFields, zap.Error(errUpsert))...)
		err = models.ErrInternalServer
		return err
	}
	nextNodeProgressID = upsertedProgressID
	s.logger.Info("Upserted/Retrieved PlayerProgress node by hash", append(logFields, zap.String("progressID", nextNodeProgressID.String()))...)

	// --- Check for existing next scene using the nextNodeProgressID (obtained from upsert) ---
	var nextSceneID *uuid.UUID
	// We still need to check if a scene exists for the target state hash
	nextScene, errScene := sceneRepoTx.FindByStoryAndHash(ctx, tx, gameState.PublishedStoryID, nextStateHash)

	if errScene == nil {
		nextSceneID = &nextScene.ID
		s.logger.Info("Next scene found in DB", append(logFields, zap.String("sceneID", nextSceneID.String()))...)

		gameState.PlayerStatus = models.PlayerStatusPlaying
		gameState.CurrentSceneID = uuid.NullUUID{UUID: *nextSceneID, Valid: true}

		gameState.PlayerProgressID = nextNodeProgressID
		gameState.LastActivityAt = time.Now().UTC()

		if _, err := gameStateRepoTx.Save(ctx, tx, gameState); err != nil {
			s.logger.Error("Failed to update PlayerGameState after finding next scene", append(logFields, zap.Error(err))...)
			err = models.ErrInternalServer
			return err
		}
		s.logger.Info("PlayerGameState updated to Playing, linked to existing scene (pending commit)")
		return nil

	} else if errors.Is(errScene, pgx.ErrNoRows) || errors.Is(errScene, models.ErrNotFound) {
		s.logger.Info("Next scene not found, initiating generation", logFields...)

		gameState.PlayerStatus = models.PlayerStatusGeneratingScene
		gameState.CurrentSceneID = uuid.NullUUID{Valid: false}
		gameState.PlayerProgressID = nextNodeProgressID
		gameState.LastActivityAt = time.Now().UTC()

		if _, err := gameStateRepoTx.Save(ctx, tx, gameState); err != nil {
			s.logger.Error("Failed to update PlayerGameState to GeneratingScene", append(logFields, zap.Error(err))...)
			err = models.ErrInternalServer
			return err
		}
		s.logger.Info("PlayerGameState updated to GeneratingScene (pending commit), task should be published after commit")

		generationPayload, errGenPayload := createGenerationPayload(
			gameState.PlayerID,
			publishedStory,
			nextProgress,
			gameState,
			madeChoicesInfo,
			nextStateHash,
			publishedStory.Language,
			models.PromptTypeNovelCreator,
		)
		if errGenPayload != nil {
			s.logger.Error("Failed to create generation payload", append(logFields, zap.Error(errGenPayload))...)
			err = models.ErrInternalServer
			return err
		}
		generationPayload.GameStateID = gameState.ID.String()

		if errPub := s.publisher.PublishGenerationTask(ctx, generationPayload); errPub != nil {
			s.logger.Error("Failed to publish next scene generation task", append(logFields, zap.Error(errPub))...)
			err = models.ErrInternalServer
			return err
		}
		s.logger.Info("Next scene generation task published", append(logFields, zap.String("taskID", generationPayload.TaskID))...)
		return nil

	} else {
		s.logger.Error("Error searching for next scene", append(logFields, zap.Error(errScene))...)
		err = models.ErrInternalServer
		return err
	}
}

func (s *gameLoopServiceImpl) GetPlayerProgress(ctx context.Context, userID uuid.UUID, gameStateID uuid.UUID) (*models.PlayerProgress, error) {
	log := s.logger.With(zap.String("gameStateID", gameStateID.String()), zap.Stringer("userID", userID))
	log.Debug("GetPlayerProgress called")

	gameState, errState := s.playerGameStateRepo.GetByID(ctx, s.pool, gameStateID)
	if errState != nil {
		if errors.Is(errState, models.ErrNotFound) || errors.Is(errState, pgx.ErrNoRows) {
			log.Warn("Player game state not found for GetPlayerProgress")
			return nil, models.ErrPlayerGameStateNotFound
		}
		log.Error("Failed to get player game state for GetPlayerProgress", zap.Error(errState))
		return nil, models.ErrInternalServer
	}

	if gameState.PlayerID != userID {
		log.Warn("User attempted to access progress from game state they do not own", zap.Stringer("ownerID", gameState.PlayerID))
		return nil, models.ErrForbidden
	}

	if gameState.PlayerProgressID == uuid.Nil {
		log.Warn("Player game state exists but has no associated PlayerProgressID", zap.String("gameStateID", gameState.ID.String()))
		return nil, models.ErrNotFound
	}

	progress, errProgress := s.playerProgressRepo.GetByID(ctx, s.pool, gameState.PlayerProgressID)
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
