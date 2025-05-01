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
func (r *pgPlayerGameStateRepository) Save(ctx context.Context, state *models.PlayerGameState) (uuid.UUID, error) {
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
		state.StartedAt = now // Set started time only on insert
		logFields = append(logFields, zap.String("newGameStateID", state.ID.String()))
		r.logger.Debug("Inserting new player game state", logFields...)

		query := `
            INSERT INTO player_game_states
                (id, player_id, published_story_id, current_scene_id, player_progress_id, player_status, ending_text, error_details, started_at, last_activity_at, completed_at)
            VALUES
                ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
            RETURNING id
        `
		err = r.db.QueryRow(ctx, query,
			state.ID,
			state.PlayerID,
			state.PublishedStoryID,
			state.CurrentSceneID,
			state.PlayerProgressID,
			state.PlayerStatus,
			state.EndingText,
			state.ErrorDetails,
			state.StartedAt,
			state.LastActivityAt,
			state.CompletedAt,
		).Scan(&returnedID)

	} else {
		// --- UPDATE ---
		r.logger.Debug("Updating existing player game state", logFields...)
		query := `
            UPDATE player_game_states SET
                current_scene_id = $2,
                player_progress_id = $3,
                player_status = $4,
                ending_text = $5,
                error_details = $6,
                last_activity_at = $7,
                completed_at = $8
                -- player_id and published_story_id should not change
                -- started_at should not change on update
            WHERE id = $1
            RETURNING id
        `
		err = r.db.QueryRow(ctx, query,
			state.ID,               // $1
			state.CurrentSceneID,   // $2
			state.PlayerProgressID, // $3
			state.PlayerStatus,     // $4
			state.EndingText,       // $5
			state.ErrorDetails,     // $6
			state.LastActivityAt,   // $7
			state.CompletedAt,      // $8
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
	query := `
        SELECT id, player_id, published_story_id, current_scene_id, player_progress_id, player_status, ending_text, error_details, started_at, last_activity_at, completed_at
        FROM player_game_states
        WHERE player_id = $1 AND published_story_id = $2
    `
	state := &models.PlayerGameState{}
	logFields := []zap.Field{
		zap.String("playerID", playerID.String()),
		zap.String("publishedStoryID", publishedStoryID.String()),
	}
	r.logger.Debug("Getting player game state (using old Get method name)", logFields...)

	err := r.db.QueryRow(ctx, query, playerID, publishedStoryID).Scan(
		&state.ID,
		&state.PlayerID,
		&state.PublishedStoryID,
		&state.CurrentSceneID,
		&state.PlayerProgressID,
		&state.PlayerStatus,
		&state.EndingText,
		&state.ErrorDetails,
		&state.StartedAt,
		&state.LastActivityAt,
		&state.CompletedAt,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			r.logger.Warn("Player game state not found", logFields...)
			return nil, models.ErrNotFound
		}
		r.logger.Error("Failed to get player game state", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("ошибка получения состояния игры: %w", err)
	}
	r.logger.Debug("Player game state retrieved successfully", logFields...)
	return state, nil
}

// GetByPlayerAndStory retrieves the current game state for a specific player and story.
// Returns models.ErrNotFound if no active game state exists.
func (r *pgPlayerGameStateRepository) GetByPlayerAndStory(ctx context.Context, playerID, publishedStoryID uuid.UUID) (*models.PlayerGameState, error) {
	// Используем ту же логику, что и в Get
	query := `
        SELECT id, player_id, published_story_id, current_scene_id, player_progress_id, player_status, ending_text, error_details, started_at, last_activity_at, completed_at
        FROM player_game_states
        WHERE player_id = $1 AND published_story_id = $2
    `
	state := &models.PlayerGameState{}
	logFields := []zap.Field{
		zap.String("playerID", playerID.String()),
		zap.String("publishedStoryID", publishedStoryID.String()),
	}
	r.logger.Debug("Getting player game state by player and story", logFields...)

	err := r.db.QueryRow(ctx, query, playerID, publishedStoryID).Scan(
		&state.ID,
		&state.PlayerID,
		&state.PublishedStoryID,
		&state.CurrentSceneID,
		&state.PlayerProgressID,
		&state.PlayerStatus,
		&state.EndingText,
		&state.ErrorDetails,
		&state.StartedAt,
		&state.LastActivityAt,
		&state.CompletedAt,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			r.logger.Warn("Player game state not found by player and story", logFields...)
			return nil, models.ErrNotFound
		}
		r.logger.Error("Failed to get player game state by player and story", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("ошибка получения состояния игры по игроку и истории: %w", err)
	}
	r.logger.Debug("Player game state retrieved successfully by player and story", logFields...)
	return state, nil
}

// GetByID retrieves the player's game state by its unique ID.
func (r *pgPlayerGameStateRepository) GetByID(ctx context.Context, id uuid.UUID) (*models.PlayerGameState, error) {
	query := `
        SELECT id, player_id, published_story_id, current_scene_id, player_progress_id, player_status, ending_text, error_details, started_at, last_activity_at, completed_at
        FROM player_game_states
        WHERE id = $1
    `
	state := &models.PlayerGameState{}
	logFields := []zap.Field{zap.String("gameStateID", id.String())}
	r.logger.Debug("Getting player game state by ID", logFields...)

	err := r.db.QueryRow(ctx, query, id).Scan(
		&state.ID,
		&state.PlayerID,
		&state.PublishedStoryID,
		&state.CurrentSceneID,
		&state.PlayerProgressID,
		&state.PlayerStatus,
		&state.EndingText,
		&state.ErrorDetails,
		&state.StartedAt,
		&state.LastActivityAt,
		&state.CompletedAt,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			r.logger.Warn("Player game state not found by ID", logFields...)
			return nil, models.ErrNotFound
		}
		r.logger.Error("Failed to get player game state by ID", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("ошибка получения состояния игры по ID %s: %w", id, err)
	}
	r.logger.Debug("Player game state retrieved by ID successfully", logFields...)
	return state, nil
}

// Delete removes the player's game state for a specific story.
// ПРИМЕЧАНИЕ: Этот метод был переименован/заменен на DeleteByPlayerAndStory в интерфейсе,
// но его логика может быть полезна или идентична.
func (r *pgPlayerGameStateRepository) Delete(ctx context.Context, playerID, publishedStoryID uuid.UUID) error {
	query := `DELETE FROM player_game_states WHERE player_id = $1 AND published_story_id = $2`
	logFields := []zap.Field{
		zap.String("playerID", playerID.String()),
		zap.String("publishedStoryID", publishedStoryID.String()),
	}
	r.logger.Debug("Deleting player game state (using old Delete method name)", logFields...)

	commandTag, err := r.db.Exec(ctx, query, playerID, publishedStoryID)
	if err != nil {
		r.logger.Error("Failed to delete player game state", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка удаления состояния игры: %w", err)
	}

	if commandTag.RowsAffected() == 0 {
		r.logger.Warn("Player game state not found for deletion", logFields...)
		return models.ErrNotFound
	}

	r.logger.Info("Player game state deleted successfully", logFields...)
	return nil
}

// DeleteByPlayerAndStory removes the game state record for a specific player and story.
// This is typically used when a player explicitly "resets" their progress for a story.
// Returns nil if the record was deleted or did not exist.
func (r *pgPlayerGameStateRepository) DeleteByPlayerAndStory(ctx context.Context, playerID, publishedStoryID uuid.UUID) error {
	query := `DELETE FROM player_game_states WHERE player_id = $1 AND published_story_id = $2`
	logFields := []zap.Field{
		zap.String("playerID", playerID.String()),
		zap.String("publishedStoryID", publishedStoryID.String()),
	}
	r.logger.Debug("Deleting player game state by player and story", logFields...)

	commandTag, err := r.db.Exec(ctx, query, playerID, publishedStoryID)
	if err != nil {
		r.logger.Error("Failed to delete player game state by player and story", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка удаления состояния игры по игроку и истории: %w", err)
	}

	if commandTag.RowsAffected() == 0 {
		r.logger.Warn("Player game state not found for deletion by player and story", logFields...)
		// Не возвращаем ошибку, если запись не найдена, как указано в комментарии интерфейса
		return nil
	}

	r.logger.Info("Player game state deleted successfully by player and story", logFields...)
	return nil
}

// ListByStoryID retrieves all game states for a specific published story.
func (r *pgPlayerGameStateRepository) ListByStoryID(ctx context.Context, publishedStoryID uuid.UUID) ([]models.PlayerGameState, error) {
	query := `
        SELECT id, player_id, published_story_id, current_scene_id, player_progress_id, player_status, ending_text, error_details, started_at, last_activity_at, completed_at
        FROM player_game_states
        WHERE published_story_id = $1
        ORDER BY last_activity_at DESC -- Or started_at?
    `
	logFields := []zap.Field{zap.String("publishedStoryID", publishedStoryID.String())}
	r.logger.Debug("Listing player game states by story ID", logFields...)

	rows, err := r.db.Query(ctx, query, publishedStoryID)
	if err != nil {
		r.logger.Error("Failed to query player game states by story ID", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("ошибка получения списка состояний игры для истории %s: %w", publishedStoryID, err)
	}
	defer rows.Close()

	states := make([]models.PlayerGameState, 0)
	for rows.Next() {
		var state models.PlayerGameState
		if err := rows.Scan(
			&state.ID,
			&state.PlayerID,
			&state.PublishedStoryID,
			&state.CurrentSceneID,
			&state.PlayerProgressID,
			&state.PlayerStatus,
			&state.EndingText,
			&state.ErrorDetails,
			&state.StartedAt,
			&state.LastActivityAt,
			&state.CompletedAt,
		); err != nil {
			r.logger.Error("Failed to scan player game state row", append(logFields, zap.Error(err))...)
			// Decide: return error or skip row? Returning error for now.
			return nil, fmt.Errorf("ошибка сканирования строки состояния игры: %w", err)
		}
		states = append(states, state)
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("Error iterating player game state rows", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("ошибка итерации по результатам состояний игры: %w", err)
	}

	r.logger.Debug("Player game states listed successfully", append(logFields, zap.Int("count", len(states)))...)
	return states, nil
}

const findAndMarkStalePlayerGeneratingQueryBase = `
UPDATE player_game_states
SET player_status = $1, -- PlayerStatusError
    error_details = $2, -- Сообщение об ошибке
    last_activity_at = NOW() -- Обновляем время активности
WHERE (player_status = $3 OR player_status = $4) -- PlayerStatusGeneratingScene или PlayerStatusGameOverPending
`

// FindAndMarkStaleGeneratingAsError находит состояния игры игрока, которые 'зависли'
// в статусе генерации сцены или концовки (или все такие, если порог 0), и обновляет их статус на Error.
func (r *pgPlayerGameStateRepository) FindAndMarkStaleGeneratingAsError(ctx context.Context, staleThreshold time.Duration) (int64, error) {
	staleStatus1 := models.PlayerStatusGeneratingScene
	staleStatus2 := models.PlayerStatusGameOverPending
	errorMessage := "Player state generation process timed out or got stuck."
	args := []interface{}{
		models.PlayerStatusError, // $1: Новый статус
		errorMessage,             // $2: Сообщение об ошибке
		staleStatus1,             // $3: Зависший статус 1
		staleStatus2,             // $4: Зависший статус 2
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
		r.logger.Info("Stale threshold is zero, checking all generating/game_over_pending states regardless of time.", logFields...)
	}

	r.logger.Info("Finding and marking stale generating player game states as Error", logFields...)

	// Передаем собранные аргументы
	commandTag, err := r.db.Exec(ctx, query, args...)

	if err != nil {
		r.logger.Error("Failed to execute update query for stale player game states", append(logFields, zap.Error(err))...)
		return 0, fmt.Errorf("ошибка обновления статуса зависших состояний игры: %w", err)
	}

	affectedRows := commandTag.RowsAffected()
	r.logger.Info("Finished marking stale player game states", append(logFields, zap.Int64("updatedCount", affectedRows))...)

	return affectedRows, nil
}

// CheckGameStateExistsForStories checks if active player game states exist for a given player and a list of story IDs.
func (r *pgPlayerGameStateRepository) CheckGameStateExistsForStories(ctx context.Context, playerID uuid.UUID, storyIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	if len(storyIDs) == 0 {
		return make(map[uuid.UUID]bool), nil // Return empty map if no IDs provided
	}

	query := `
        SELECT published_story_id
        FROM player_game_states
        WHERE player_id = $1 AND published_story_id = ANY($2::uuid[])
    `
	logFields := []zap.Field{
		zap.String("playerID", playerID.String()),
		zap.Int("storyIDCount", len(storyIDs)),
	}
	r.logger.Debug("Checking game state existence for stories", logFields...)

	rows, err := r.db.Query(ctx, query, playerID, storyIDs)
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
func (r *pgPlayerGameStateRepository) ListByPlayerAndStory(ctx context.Context, playerID, publishedStoryID uuid.UUID) ([]*models.PlayerGameState, error) {
	query := `
        SELECT id, player_id, published_story_id, current_scene_id, player_progress_id, player_status, ending_text, error_details, started_at, last_activity_at, completed_at
        FROM player_game_states
        WHERE player_id = $1 AND published_story_id = $2
        ORDER BY last_activity_at DESC -- Сортируем по последней активности (или started_at?)
    `
	states := make([]*models.PlayerGameState, 0)
	logFields := []zap.Field{
		zap.String("playerID", playerID.String()),
		zap.String("publishedStoryID", publishedStoryID.String()),
	}
	r.logger.Debug("Listing player game states by player and story", logFields...)

	rows, err := r.db.Query(ctx, query, playerID, publishedStoryID)
	if err != nil {
		r.logger.Error("Failed to query player game states", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("ошибка получения списка состояний игры: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		state := &models.PlayerGameState{}
		err := rows.Scan(
			&state.ID,
			&state.PlayerID,
			&state.PublishedStoryID,
			&state.CurrentSceneID,
			&state.PlayerProgressID,
			&state.PlayerStatus,
			&state.EndingText,
			&state.ErrorDetails,
			&state.StartedAt,
			&state.LastActivityAt,
			&state.CompletedAt,
		)
		if err != nil {
			r.logger.Error("Failed to scan player game state row", append(logFields, zap.Error(err))...)
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

// DeleteByID removes a specific game state record by its ID.
func (r *pgPlayerGameStateRepository) DeleteByID(ctx context.Context, gameStateID uuid.UUID) error {
	query := `DELETE FROM player_game_states WHERE id = $1`
	logFields := []zap.Field{zap.String("gameStateID", gameStateID.String())}
	r.logger.Debug("Deleting player game state by ID", logFields...)

	commandTag, err := r.db.Exec(ctx, query, gameStateID)
	if err != nil {
		r.logger.Error("Failed to delete player game state by ID", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка удаления состояния игры по ID: %w", err)
	}

	if commandTag.RowsAffected() == 0 {
		r.logger.Warn("No player game state found to delete by ID", logFields...)
		// Если запись не найдена по ID, это явная ошибка NotFound
		return models.ErrNotFound
	}

	r.logger.Info("Player game state deleted successfully by ID", logFields...)
	return nil
}
