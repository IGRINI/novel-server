package database

import (
	"context"
	"errors"
	"fmt"
	"novel-server/shared/interfaces"
	"novel-server/shared/models"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

const (
	playerGameStateFields = `id, player_id, published_story_id, current_scene_id, player_progress_id, player_status, ending_text, error_details, started_at, last_activity_at, completed_at`

	insertPlayerGameStateQuery = `
            INSERT INTO player_game_states
                (id, player_id, published_story_id, current_scene_id, player_progress_id, player_status, ending_text, error_details, started_at, last_activity_at, completed_at, created_at, updated_at)
            VALUES
                ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW(), NOW())
            RETURNING id
        `
	updatePlayerGameStateQuery = `
            UPDATE player_game_states SET
                current_scene_id = $2,
                player_progress_id = $3,
                player_status = $4,
                ending_text = $5,
                error_details = $6,
                last_activity_at = $7,
                completed_at = $8,
                updated_at = NOW()
            WHERE id = $1
            RETURNING id
        `
	getPlayerGameStateByPlayerAndStoryQuery = `
        SELECT ` + playerGameStateFields + `
        FROM player_game_states
        WHERE player_id = $1 AND published_story_id = $2
    `
	getPlayerGameStateByIDQuery = `
        SELECT ` + playerGameStateFields + `
        FROM player_game_states
        WHERE id = $1
    `
	deletePlayerGameStateByIDQuery             = `DELETE FROM player_game_states WHERE id = $1`
	deletePlayerGameStateByPlayerAndStoryQuery = `DELETE FROM player_game_states WHERE player_id = $1 AND published_story_id = $2`
	listPlayerGameStateByStoryIDQuery          = `
        SELECT ` + playerGameStateFields + `
        FROM player_game_states
        WHERE published_story_id = $1
        ORDER BY last_activity_at DESC -- Or started_at?
    `
	findAndMarkStalePlayerGeneratingQueryBase = `
UPDATE player_game_states
SET player_status = $1, -- PlayerStatusError
    error_details = $2, -- Сообщение об ошибке
    last_activity_at = NOW() -- Обновляем время активности
WHERE (player_status = $3 OR player_status = $4) -- PlayerStatusGeneratingScene или PlayerStatusGameOverPending
`
	checkGameStateExistsForStoriesQuery = `
        SELECT published_story_id
        FROM player_game_states
        WHERE player_id = $1 AND published_story_id = ANY($2::uuid[])
    `
	listPlayerGameStateByPlayerAndStoryQuery = `
        SELECT ` + playerGameStateFields + `
        FROM player_game_states
        WHERE player_id = $1 AND published_story_id = $2
        ORDER BY last_activity_at DESC -- Сортируем по последней активности (или started_at?)
    `
	listGameStateSummariesByPlayerAndStoryQuery = `
		SELECT
		    pgs.id,              -- Game State ID
		    pgs.last_activity_at,
		    pp.scene_index,
		    pp.current_scene_summary, -- <<< ADDED: Select from player_progress
		    pgs.player_status        -- <<< ДОБАВЛЕНО: Выбираем статус >>>
		FROM
		    player_game_states pgs
		JOIN
		    player_progress pp ON pgs.player_progress_id = pp.id
		WHERE
		    pgs.player_id = $1 AND pgs.published_story_id = $2
		ORDER BY
		    pgs.last_activity_at DESC -- Most recent first
	`
)

// Compile-time check to ensure pgPlayerGameStateRepository implements the interface
var _ interfaces.PlayerGameStateRepository = (*pgPlayerGameStateRepository)(nil)

// pgPlayerGameStateRepository is the PostgreSQL implementation of PlayerGameStateRepository
type pgPlayerGameStateRepository struct {
	db     interfaces.DBTX // Can be *pgxpool.Pool or pgx.Tx
	logger *zap.Logger
}

// NewPgPlayerGameStateRepository creates a new repository instance.
func NewPgPlayerGameStateRepository(db interfaces.DBTX, logger *zap.Logger) interfaces.PlayerGameStateRepository {
	return &pgPlayerGameStateRepository{
		db:     db,
		logger: logger.Named("PgPlayerGameStateRepo"),
	}
}

// Save создает новую запись состояния игры или обновляет существующую по ID.
// Ответственность за проверку лимитов слотов лежит на сервисном слое.
func (r *pgPlayerGameStateRepository) Save(ctx context.Context, querier interfaces.DBTX, state *models.PlayerGameState) (uuid.UUID, error) {
	now := time.Now().UTC()
	state.LastActivityAt = now // Always update last activity time

	logFields := []zap.Field{
		zap.String("playerID", state.PlayerID.String()),
		zap.String("publishedStoryID", state.PublishedStoryID.String()),
		zap.String("playerStatus", string(state.PlayerStatus)),
	}
	if state.ID != uuid.Nil {
		logFields = append(logFields, zap.String("gameStateID", state.ID.String()))
	}

	var returnedID uuid.UUID
	var err error

	if state.ID == uuid.Nil {
		// --- INSERT ---
		state.ID = uuid.New() // Generate new ID for insert
		state.StartedAt = now // Restore setting started time
		logFields = append(logFields, zap.String("newGameStateID", state.ID.String()))
		r.logger.Debug("Inserting new player game state", logFields...)

		err = querier.QueryRow(ctx, insertPlayerGameStateQuery,
			state.ID,               // $1
			state.PlayerID,         // $2
			state.PublishedStoryID, // $3
			state.CurrentSceneID,   // $4
			state.PlayerProgressID, // $5
			state.PlayerStatus,     // $6
			state.EndingText,       // $7 (Restored)
			state.ErrorDetails,     // $8
			state.StartedAt,        // $9 (Restored)
			state.LastActivityAt,   // $10
			state.CompletedAt,      // $11 (Restored)
		).Scan(&returnedID)

	} else {
		// --- UPDATE ---
		r.logger.Debug("Updating existing player game state", logFields...)
		err = querier.QueryRow(ctx, updatePlayerGameStateQuery,
			state.ID,               // $1
			state.CurrentSceneID,   // $2
			state.PlayerProgressID, // $3
			state.PlayerStatus,     // $4
			state.EndingText,       // $5 (Restored)
			state.ErrorDetails,     // $6
			state.LastActivityAt,   // $7
			state.CompletedAt,      // $8 (Restored)
		).Scan(&returnedID)

		// Handle potential ErrNoRows on update, although it shouldn't happen if ID is correct
		if errors.Is(err, pgx.ErrNoRows) {
			r.logger.Error("Failed to update player game state: record not found", append(logFields, zap.Error(err))...)
			return uuid.Nil, models.ErrNotFound // Return specific error if ID not found for update
		}
	}

	if err != nil {
		logAction := "inserting"
		if state.ID != uuid.Nil {
			logAction = "updating"
		}
		r.logger.Error(fmt.Sprintf("Failed during %s player game state", logAction), append(logFields, zap.Error(err))...)
		return uuid.Nil, fmt.Errorf("ошибка при сохранении состояния игры (%s): %w", logAction, err)
	}

	r.logger.Info("Player game state saved successfully", append(logFields, zap.String("returnedID", returnedID.String()))...)
	return returnedID, nil
}

// Get retrieves the player's game state for a specific story.
// ПРИМЕЧАНИЕ: Этот метод был переименован/заменен на GetByPlayerAndStory в интерфейсе,
// но его логика может быть полезна или идентична.
func (r *pgPlayerGameStateRepository) Get(ctx context.Context, playerID, publishedStoryID uuid.UUID) (*models.PlayerGameState, error) {
	logFields := []zap.Field{
		zap.String("playerID", playerID.String()),
		zap.String("publishedStoryID", publishedStoryID.String()),
	}
	r.logger.Debug("Getting player game state (using old Get method name)", logFields...)

	row := r.db.QueryRow(ctx, getPlayerGameStateByPlayerAndStoryQuery, playerID, publishedStoryID)
	state, err := scanPlayerGameState(row)

	if err != nil {
		if err == models.ErrNotFound { // Check specific error from helper
			r.logger.Warn("Player game state not found", logFields...)
			return nil, models.ErrNotFound
		}
		r.logger.Error("Failed to get player game state", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("ошибка получения состояния игры: %w", err)
	}
	r.logger.Debug("Player game state retrieved successfully", logFields...)
	return state, nil
}

// GetByPlayerAndStory retrieves the active game state for a player and story.
func (r *pgPlayerGameStateRepository) GetByPlayerAndStory(ctx context.Context, querier interfaces.DBTX, playerID, publishedStoryID uuid.UUID) (*models.PlayerGameState, error) {
	const query = `
        SELECT id, player_id, published_story_id, player_progress_id, current_scene_id, player_status, started_at, last_activity_at, created_at, updated_at
        FROM player_game_states
        WHERE player_id = $1 AND published_story_id = $2
        -- Add ORDER BY if multiple states per player/story are possible and we need the 'latest'
        LIMIT 1;
    `
	log := r.logger.With(
		zap.String("method", "GetByPlayerAndStory"),
		zap.Stringer("playerID", playerID),
		zap.Stringer("storyID", publishedStoryID),
	)
	log.Debug("Getting game state by player and story")

	row := querier.QueryRow(ctx, query, playerID, publishedStoryID)
	state, err := scanPlayerGameState(row) // Assuming a scan helper exists

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Info("No game state found for player and story")
			return nil, models.ErrNotFound // Consistent error
		}
		log.Error("Failed to scan player game state", zap.Error(err))
		return nil, fmt.Errorf("database scan error: %w", err)
	}

	log.Debug("Game state retrieved successfully")
	return state, nil
}

// GetByID retrieves the player's game state by its unique ID.
func (r *pgPlayerGameStateRepository) GetByID(ctx context.Context, querier interfaces.DBTX, id uuid.UUID) (*models.PlayerGameState, error) {
	logFields := []zap.Field{zap.String("gameStateID", id.String())}
	r.logger.Debug("Getting player game state by ID", logFields...)

	row := querier.QueryRow(ctx, getPlayerGameStateByIDQuery, id)
	state, err := scanPlayerGameState(row)

	if err != nil {
		if err == models.ErrNotFound { // Check specific error from helper
			r.logger.Warn("Player game state not found by ID", logFields...)
			return nil, models.ErrNotFound
		}
		r.logger.Error("Failed to get player game state by ID", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("ошибка получения состояния игры по ID %s: %w", id, err)
	}
	r.logger.Debug("Player game state retrieved by ID successfully", logFields...)
	return state, nil
}

// Delete removes a game state record by its unique ID.
// Implements the interface method.
func (r *pgPlayerGameStateRepository) Delete(ctx context.Context, querier interfaces.DBTX, gameStateID uuid.UUID) error {
	logFields := []zap.Field{zap.String("gameStateID", gameStateID.String())}
	r.logger.Debug("Deleting player game state by ID", logFields...)

	cmdTag, err := querier.Exec(ctx, deletePlayerGameStateByIDQuery, gameStateID)
	if err != nil {
		r.logger.Error("Failed to delete player game state by ID", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка удаления состояния игры по ID %s: %w", gameStateID, err)
	}

	if cmdTag.RowsAffected() == 0 {
		r.logger.Warn("Player game state not found for deletion by ID", logFields...)
		return models.ErrNotFound
	}

	r.logger.Info("Player game state deleted successfully by ID", logFields...)
	return nil
}

// Удаляем старый DeleteByPlayerAndStory, так как он больше не в интерфейсе - <<< ОШИБКА, МЕТОД НУЖЕН >>>
func (r *pgPlayerGameStateRepository) DeleteByPlayerAndStory(ctx context.Context, querier interfaces.DBTX, playerID, publishedStoryID uuid.UUID) error {
	logFields := []zap.Field{
		zap.String("playerID", playerID.String()),
		zap.String("publishedStoryID", publishedStoryID.String()),
	}
	r.logger.Debug("Deleting player game state by player and story", logFields...)

	commandTag, err := querier.Exec(ctx, deletePlayerGameStateByPlayerAndStoryQuery, playerID, publishedStoryID)
	if err != nil {
		r.logger.Error("Failed to delete player game state by player and story", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка удаления состояния игры по игроку и истории: %w", err)
	}

	if commandTag.RowsAffected() == 0 {
		r.logger.Warn("Player game state not found for deletion by player and story", logFields...)
		// Не возвращаем ошибку, если запись не найдена, как указано в комментарии интерфейса (DEPRECATED)
		// Вернем ErrNotFound для консистентности с Delete
		return models.ErrNotFound
	}

	r.logger.Info("Player game state deleted successfully by player and story", logFields...)
	return nil
}

// ListByStoryID retrieves all game states associated with a specific story ID.
// Primarily for internal use (e.g., deleting related states when a story is deleted).
func (r *pgPlayerGameStateRepository) ListByStoryID(ctx context.Context, querier interfaces.DBTX, publishedStoryID uuid.UUID) ([]models.PlayerGameState, error) {
	logFields := []zap.Field{zap.String("publishedStoryID", publishedStoryID.String())}
	r.logger.Debug("Listing player game states by story ID", logFields...)

	rows, err := querier.Query(ctx, listPlayerGameStateByStoryIDQuery, publishedStoryID)
	if err != nil {
		r.logger.Error("Failed to query player game states by story ID", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("ошибка получения списка состояний игры для истории %s: %w", publishedStoryID, err)
	}
	defer rows.Close()

	states := make([]models.PlayerGameState, 0)
	for rows.Next() {
		state, err := scanPlayerGameState(rows) // Use helper for scanning rows
		if err != nil {
			// scanPlayerGameState doesn't return ErrNotFound for Rows
			r.logger.Error("Failed to scan player game state row in ListByStoryID", append(logFields, zap.Error(err))...)
			// Decide: return error or skip row? Returning error for now.
			return nil, fmt.Errorf("ошибка сканирования строки состояния игры: %w", err)
		}
		states = append(states, *state) // Append the scanned state (dereferenced)
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("Error iterating player game state rows", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("ошибка итерации по результатам состояний игры: %w", err)
	}

	r.logger.Debug("Player game states listed successfully", append(logFields, zap.Int("count", len(states)))...)
	return states, nil
}

// FindAndMarkStaleGeneratingAsError находит состояния игры игрока, которые 'зависли'
// в статусе генерации сцены или концовки (или все такие, если порог 0), и обновляет их статус на Error.
func (r *pgPlayerGameStateRepository) FindAndMarkStaleGeneratingAsError(ctx context.Context, querier interfaces.DBTX, staleThreshold time.Duration) (int64, error) {
	staleStatus1 := models.PlayerStatusGeneratingScene
	staleStatus2 := models.PlayerStatusGameOverPending // Restored
	errorMessage := "Player state generation process timed out or got stuck."
	args := []interface{}{
		models.PlayerStatusError, // $1: Новый статус
		errorMessage,             // $2: Сообщение об ошибке
		staleStatus1,             // $3: Зависший статус 1
		staleStatus2,             // $4: Зависший статус 2 (Restored)
	}
	query := findAndMarkStalePlayerGeneratingQueryBase
	thresholdTime := time.Now().UTC().Add(-staleThreshold)

	logFields := []zap.Field{
		zap.Any("staleStatuses", []models.PlayerStatus{staleStatus1, staleStatus2}),
		zap.Duration("staleThreshold", staleThreshold),
	}

	// Добавляем условие времени только если staleThreshold > 0
	if staleThreshold > 0 {
		query += " AND last_activity_at < $5" // $5 будет thresholdTime
		args = append(args, thresholdTime)
		logFields = append(logFields, zap.Time("thresholdTime", thresholdTime))
	} else {
		r.logger.Info("Stale threshold is zero, checking all generating/game_over_pending states regardless of time.", logFields...) // Restored pending
	}

	r.logger.Info("Finding and marking stale generating player game states as Error", logFields...)

	// Передаем собранные аргументы
	commandTag, err := querier.Exec(ctx, query, args...)

	if err != nil {
		r.logger.Error("Failed to execute update query for stale player game states", append(logFields, zap.Error(err))...)
		return 0, fmt.Errorf("ошибка обновления статуса зависших состояний игры: %w", err)
	}

	affectedRows := commandTag.RowsAffected()
	r.logger.Info("Finished marking stale player game states", append(logFields, zap.Int64("updatedCount", affectedRows))...)

	return affectedRows, nil
}

// CheckGameStateExistsForStories checks if active player game states exist for a given player and a list of story IDs.
func (r *pgPlayerGameStateRepository) CheckGameStateExistsForStories(ctx context.Context, querier interfaces.DBTX, playerID uuid.UUID, storyIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	if len(storyIDs) == 0 {
		return make(map[uuid.UUID]bool), nil // Return empty map if no IDs provided
	}

	logFields := []zap.Field{
		zap.String("playerID", playerID.String()),
		zap.Int("storyIDCount", len(storyIDs)),
	}
	r.logger.Debug("Checking game state existence for stories", logFields...)

	rows, err := querier.Query(ctx, checkGameStateExistsForStoriesQuery, playerID, storyIDs)
	if err != nil {
		r.logger.Error("Failed to query game state existence", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("ошибка проверки существования состояния игры: %w", err)
	}
	defer rows.Close()

	// Initialize the result map with false for all input story IDs
	existenceMap := make(map[uuid.UUID]bool, len(storyIDs))
	for _, id := range storyIDs {
		existenceMap[id] = false
	}

	// Iterate through the found story IDs and mark them as true
	for rows.Next() {
		var foundStoryID uuid.UUID
		if err := rows.Scan(&foundStoryID); err != nil {
			r.logger.Error("Failed to scan existing game state story ID", append(logFields, zap.Error(err))...)
			// Continue scanning other rows, but log the error
			continue
		}
		existenceMap[foundStoryID] = true
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("Error iterating game state existence rows", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("ошибка итерации результатов проверки существования состояния игры: %w", err)
	}

	r.logger.Debug("Game state existence check completed", logFields...)
	return existenceMap, nil
}

// ListByPlayerAndStory retrieves all game states for a specific player and story.
// Returns an empty slice if no game states exist.
func (r *pgPlayerGameStateRepository) ListByPlayerAndStory(ctx context.Context, querier interfaces.DBTX, playerID, publishedStoryID uuid.UUID) ([]*models.PlayerGameState, error) {
	logFields := []zap.Field{
		zap.String("playerID", playerID.String()),
		zap.String("publishedStoryID", publishedStoryID.String()),
	}
	r.logger.Debug("Listing player game states by player and story", logFields...)

	rows, err := querier.Query(ctx, listPlayerGameStateByPlayerAndStoryQuery, playerID, publishedStoryID)
	if err != nil {
		r.logger.Error("Failed to query player game states", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("ошибка получения списка состояний игры: %w", err)
	}
	defer rows.Close()

	states := make([]*models.PlayerGameState, 0)
	for rows.Next() {
		state, err := scanPlayerGameState(rows) // Use helper
		if err != nil {
			r.logger.Error("Failed to scan player game state row in ListByPlayerAndStory", append(logFields, zap.Error(err))...)
			return nil, fmt.Errorf("ошибка сканирования данных состояния игры: %w", err)
		}
		states = append(states, state)
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("Error iterating over player game state rows", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("ошибка при итерации результатов состояний игры: %w", err)
	}

	r.logger.Debug("Player game states listed successfully", append(logFields, zap.Int("count", len(states)))...)
	return states, nil
}

// ListSummariesByPlayerAndStory retrieves a list of game state summaries (ID, LastActivityAt, SceneIndex)
// for a specific player and story, joined with player_progress.
// Returns an empty slice if no game states are found.
func (r *pgPlayerGameStateRepository) ListSummariesByPlayerAndStory(ctx context.Context, querier interfaces.DBTX, userID, publishedStoryID uuid.UUID) ([]*models.GameStateSummaryDTO, error) {
	logFields := []zap.Field{
		zap.Stringer("userID", userID),
		zap.Stringer("publishedStoryID", publishedStoryID),
	}
	r.logger.Debug("Listing game state summaries by player and story", logFields...)

	rows, err := querier.Query(ctx, listGameStateSummariesByPlayerAndStoryQuery, userID, publishedStoryID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			r.logger.Debug("No game state summaries found for player and story", logFields...)
			return []*models.GameStateSummaryDTO{}, nil
		}
		r.logger.Error("Failed to query game state summaries", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("database error listing game state summaries: %w", err)
	}
	defer rows.Close()

	summaries := make([]*models.GameStateSummaryDTO, 0)
	for rows.Next() {
		summary := &models.GameStateSummaryDTO{}
		var sceneSummary *string   // <<< ADDED: Variable to scan into
		var playerStatusStr string // <<< НОВОЕ: Временная строка для статуса
		if err := rows.Scan(
			&summary.ID,
			&summary.LastActivityAt,
			&summary.SceneIndex,
			&sceneSummary,    // <<< ADDED: Scan the summary
			&playerStatusStr, // <<< ИЗМЕНЕНО: Сканируем в строку
		); err != nil {
			r.logger.Error("Failed to scan game state summary row", append(logFields, zap.Error(err))...)
			return nil, fmt.Errorf("error scanning game state summary: %w", err)
		}
		summary.CurrentSceneSummary = sceneSummary                  // <<< ADDED: Assign to DTO field
		summary.PlayerStatus = models.PlayerStatus(playerStatusStr) // <<< ИЗМЕНЕНО: Присваиваем с приведением типа
		summaries = append(summaries, summary)
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("Error during iteration over game state summary rows", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("database error iterating game state summaries: %w", err)
	}

	r.logger.Debug("Successfully listed game state summaries", append(logFields, zap.Int("count", len(summaries)))...)
	return summaries, nil
}

// DeleteForUser deletes the game state only if the userID matches the owner.
// Returns ErrNotFound if the state doesn't exist or the user doesn't own it.
func (r *pgPlayerGameStateRepository) DeleteForUser(ctx context.Context, querier interfaces.DBTX, gameStateID, userID uuid.UUID) error {
	const query = `DELETE FROM player_game_states WHERE id = $1 AND player_id = $2`
	log := r.logger.With(
		zap.String("method", "DeleteForUser"),
		zap.Stringer("gameStateID", gameStateID),
		zap.Stringer("userID", userID),
	)
	log.Debug("Attempting to delete game state for user")

	cmdTag, err := querier.Exec(ctx, query, gameStateID, userID)
	if err != nil {
		log.Error("Failed to execute delete query for game state", zap.Error(err))
		return fmt.Errorf("database error during game state deletion: %w", err)
	}

	if cmdTag.RowsAffected() == 0 {
		log.Warn("Game state not found or user forbidden to delete")
		// We don't distinguish between NotFound and Forbidden here, return NotFound
		return models.ErrNotFound
	}

	log.Info("Game state deleted successfully for user")
	return nil
}

// UpdateProgressAndScene updates the progress and current scene ID for a game state.
func (r *pgPlayerGameStateRepository) UpdateProgressAndScene(ctx context.Context, querier interfaces.DBTX, gameStateID, progressID uuid.UUID, sceneID uuid.UUID) error {
	const query = `
        UPDATE player_game_states SET
            player_progress_id = $2,
            current_scene_id = $3,
            player_status = $4,
            last_activity_at = NOW(),
            updated_at = NOW()
        WHERE id = $1;
    `
	log := r.logger.With(
		zap.String("method", "UpdateProgressAndScene"),
		zap.Stringer("gameStateID", gameStateID),
		zap.Stringer("progressID", progressID),
		zap.Stringer("sceneID", sceneID),
	)
	log.Debug("Updating game state progress and scene")

	// Determine new status based on whether scene generation is needed
	newStatus := models.PlayerStatusPlaying // Assume playing if scene exists
	sceneNullUUID := uuid.NullUUID{UUID: sceneID, Valid: sceneID != uuid.Nil}
	if !sceneNullUUID.Valid {
		newStatus = models.PlayerStatusGeneratingScene
		log.Info("Scene ID is nil, setting player status to GeneratingScene")
	}

	cmdTag, err := querier.Exec(ctx, query, gameStateID, progressID, sceneNullUUID, newStatus)
	if err != nil {
		log.Error("Failed to execute update query for progress and scene", zap.Error(err))
		return fmt.Errorf("database error updating game state progress/scene: %w", err)
	}

	if cmdTag.RowsAffected() == 0 {
		log.Warn("No game state found to update progress and scene")
		return models.ErrNotFound // Or a more specific error
	}

	log.Info("Game state progress and scene updated successfully")
	return nil
}

// --- Helper methods (internal) ---

// scanPlayerGameState scans a single row into a PlayerGameState struct.
// It handles potential ErrNoRows from QueryRow and returns models.ErrNotFound.
// For Rows iteration, ErrNoRows is not expected during Scan.
func scanPlayerGameState(row pgx.Row) (*models.PlayerGameState, error) {
	state := &models.PlayerGameState{}
	err := row.Scan(
		&state.ID,
		&state.PlayerID,
		&state.PublishedStoryID,
		&state.CurrentSceneID,
		&state.PlayerProgressID,
		&state.PlayerStatus,
		&state.EndingText, // Restored
		&state.ErrorDetails,
		&state.StartedAt, // Restored
		&state.LastActivityAt,
		&state.CompletedAt, // Restored
		// Missing CreatedAt, UpdatedAt in original fields/scan?
		// Let's assume they are handled by DB defaults or not selected everywhere.
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, models.ErrNotFound // Specific error for QueryRow case
		}
		// Don't log here, let the caller log with more context
		return nil, fmt.Errorf("ошибка сканирования строки состояния игры: %w", err)
	}
	return state, nil
}
