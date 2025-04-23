package database

import (
	"context"
	"errors"
	"fmt"
	"novel-server/shared/models"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

type pgPlayerGameStateRepository struct {
	pool   *pgxpool.Pool
	logger *zap.Logger
}

// NewPgPlayerGameStateRepository создает новый экземпляр репозитория состояния игры игрока.
func NewPgPlayerGameStateRepository(pool *pgxpool.Pool, logger *zap.Logger) *pgPlayerGameStateRepository {
	return &pgPlayerGameStateRepository{
		pool:   pool,
		logger: logger.Named("PlayerGameStateRepository"),
	}
}

// --- Имплементация методов интерфейса --- //

// GetByPlayerAndStory retrieves the current game state for a specific player and story.
func (r *pgPlayerGameStateRepository) GetByPlayerAndStory(ctx context.Context, playerID uuid.UUID, publishedStoryID uuid.UUID) (*models.PlayerGameState, error) {
	query := `SELECT id, player_id, published_story_id, player_progress_id, current_scene_id, player_status, error_details, started_at, last_activity_at, completed_at, ending_text
              FROM player_game_states
              WHERE player_id = $1 AND published_story_id = $2`

	state := &models.PlayerGameState{}
	err := r.pool.QueryRow(ctx, query, playerID, publishedStoryID).Scan(
		&state.ID,
		&state.PlayerID,
		&state.PublishedStoryID,
		&state.PlayerProgressID,
		&state.CurrentSceneID,
		&state.PlayerStatus,
		&state.ErrorDetails,
		&state.StartedAt,
		&state.LastActivityAt,
		&state.CompletedAt,
		&state.EndingText,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			r.logger.Warn("Player game state not found", zap.String("playerID", playerID.String()), zap.String("storyID", publishedStoryID.String()))
			return nil, models.ErrNotFound
		}
		r.logger.Error("Error getting player game state", zap.String("playerID", playerID.String()), zap.String("storyID", publishedStoryID.String()), zap.Error(err))
		return nil, err // Return original error for other DB issues
	}

	return state, nil
}

// GetByID retrieves a player game state by its unique ID.
func (r *pgPlayerGameStateRepository) GetByID(ctx context.Context, gameStateID uuid.UUID) (*models.PlayerGameState, error) {
	query := `SELECT id, player_id, published_story_id, player_progress_id, current_scene_id, player_status, error_details, started_at, last_activity_at, completed_at, ending_text
              FROM player_game_states
              WHERE id = $1`

	state := &models.PlayerGameState{}
	err := r.pool.QueryRow(ctx, query, gameStateID).Scan(
		&state.ID,
		&state.PlayerID,
		&state.PublishedStoryID,
		&state.PlayerProgressID,
		&state.CurrentSceneID,
		&state.PlayerStatus,
		&state.ErrorDetails,
		&state.StartedAt,
		&state.LastActivityAt,
		&state.CompletedAt,
		&state.EndingText,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			r.logger.Warn("Player game state not found by ID", zap.String("gameStateID", gameStateID.String()))
			return nil, models.ErrNotFound
		}
		r.logger.Error("Error getting player game state by ID", zap.String("gameStateID", gameStateID.String()), zap.Error(err))
		return nil, err // Return original error
	}

	return state, nil
}

// Save creates or updates a player game state.
func (r *pgPlayerGameStateRepository) Save(ctx context.Context, state *models.PlayerGameState) (uuid.UUID, error) {
	now := time.Now().UTC()

	if state.ID == uuid.Nil {
		// Create new record
		state.ID = uuid.New()
		state.StartedAt = now
		state.LastActivityAt = now

		query := `INSERT INTO player_game_states
                  (id, player_id, published_story_id, player_progress_id, current_scene_id, player_status, error_details, started_at, last_activity_at, completed_at, ending_text)
                  VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`
		_, err := r.pool.Exec(ctx, query,
			state.ID, state.PlayerID, state.PublishedStoryID, state.PlayerProgressID, state.CurrentSceneID,
			state.PlayerStatus, state.ErrorDetails, state.StartedAt, state.LastActivityAt, state.CompletedAt, state.EndingText,
		)
		if err != nil {
			r.logger.Error("Error creating player game state", zap.Error(err), zap.Any("state", state))
			return uuid.Nil, err
		}
		r.logger.Debug("Player game state created", zap.String("id", state.ID.String()))
		return state.ID, nil
	} else {
		// Update existing record
		state.LastActivityAt = now // Always update last activity on save

		query := `UPDATE player_game_states SET
                  player_progress_id = $2,
                  current_scene_id = $3,
                  player_status = $4,
                  error_details = $5,
                  last_activity_at = $6,
                  completed_at = $7,
                  ending_text = $8
                  WHERE id = $1`
		_, err := r.pool.Exec(ctx, query,
			state.ID, state.PlayerProgressID, state.CurrentSceneID, state.PlayerStatus,
			state.ErrorDetails, state.LastActivityAt, state.CompletedAt, state.EndingText,
		)
		if err != nil {
			r.logger.Error("Error updating player game state", zap.Error(err), zap.Any("state", state))
			return uuid.Nil, err
		}
		r.logger.Debug("Player game state updated", zap.String("id", state.ID.String()))
		return state.ID, nil
	}
}

// DeleteByPlayerAndStory removes the game state record.
func (r *pgPlayerGameStateRepository) DeleteByPlayerAndStory(ctx context.Context, playerID uuid.UUID, publishedStoryID uuid.UUID) error {
	query := `DELETE FROM player_game_states WHERE player_id = $1 AND published_story_id = $2`
	result, err := r.pool.Exec(ctx, query, playerID, publishedStoryID)

	if err != nil {
		r.logger.Error("Error deleting player game state", zap.String("playerID", playerID.String()), zap.String("storyID", publishedStoryID.String()), zap.Error(err))
		return err
	}

	if result.RowsAffected() == 0 {
		// It's okay if the record didn't exist, treat as success
		r.logger.Debug("Player game state not found for deletion, operation considered successful", zap.String("playerID", playerID.String()), zap.String("storyID", publishedStoryID.String()))
	}

	return nil
}

// CheckGameStateExistsForStories checks if active player game states exist for a given player and a list of story IDs.
func (r *pgPlayerGameStateRepository) CheckGameStateExistsForStories(ctx context.Context, playerID uuid.UUID, storyIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	if len(storyIDs) == 0 {
		return make(map[uuid.UUID]bool), nil // Return empty map if no story IDs are provided
	}

	query := `SELECT published_story_id
              FROM player_game_states
              WHERE player_id = $1 AND published_story_id = ANY($2::uuid[])`

	rows, err := r.pool.Query(ctx, query, playerID, storyIDs)
	if err != nil {
		r.logger.Error("Error checking game state existence", zap.String("playerID", playerID.String()), zap.Error(err))
		return nil, err
	}
	defer rows.Close()

	existsMap := make(map[uuid.UUID]bool)
	for rows.Next() {
		var storyID uuid.UUID
		if err := rows.Scan(&storyID); err != nil {
			r.logger.Error("Error scanning story ID during game state existence check", zap.Error(err))
			return nil, err // Return error if scanning fails
		}
		existsMap[storyID] = true
	}

	if rows.Err() != nil {
		r.logger.Error("Error iterating rows during game state existence check", zap.Error(rows.Err()))
		return nil, rows.Err()
	}

	// Ensure all requested story IDs have an entry in the map (defaulting to false)
	for _, id := range storyIDs {
		if _, found := existsMap[id]; !found {
			existsMap[id] = false
		}
	}

	return existsMap, nil
}

// ListByStoryID получает все состояния игры для указанной истории.
func (r *pgPlayerGameStateRepository) ListByStoryID(ctx context.Context, publishedStoryID uuid.UUID) ([]models.PlayerGameState, error) {
	query := `
		SELECT id, player_id, published_story_id, current_scene_id, player_progress_id, 
		       player_status, ending_text, error_details, started_at, last_activity_at, completed_at
		FROM player_game_states
		WHERE published_story_id = $1
		ORDER BY last_activity_at DESC
	`

	rows, err := r.pool.Query(ctx, query, publishedStoryID)
	if err != nil {
		return nil, fmt.Errorf("error querying player game states by story ID %s: %w", publishedStoryID, err)
	}
	defer rows.Close()

	var states []models.PlayerGameState
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
			return nil, fmt.Errorf("error scanning player game state row for story ID %s: %w", publishedStoryID, err)
		}
		states = append(states, state)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating player game state rows for story ID %s: %w", publishedStoryID, err)
	}

	// Возвращаем пустой срез, если ничего не найдено, а не nil
	if states == nil {
		states = make([]models.PlayerGameState, 0)
	}

	return states, nil
}
