package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"novel-server/shared/interfaces"
	"novel-server/shared/models"
	"novel-server/shared/utils"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	"go.uber.org/zap"
)

// Compile-time check to ensure implementation satisfies the interface.
var _ interfaces.PlayerProgressRepository = (*pgPlayerProgressRepository)(nil)

type pgPlayerProgressRepository struct {
	db     interfaces.DBTX // Keep DBTX for potential transactions
	logger *zap.Logger
	pool   *pgxpool.Pool // Use pool for regular queries
}

// NewPgPlayerProgressRepository creates a new repository instance.
// It now takes a *pgxpool.Pool for executing queries.
func NewPgPlayerProgressRepository(pool *pgxpool.Pool, logger *zap.Logger) interfaces.PlayerProgressRepository {
	return &pgPlayerProgressRepository{
		db:     pool, // Can assign pool to DBTX if needed for consistency or transactions later
		logger: logger.Named("PgPlayerProgressRepo"),
		pool:   pool,
	}
}

// Get retrieves the current player progress for a specific story.
// func (r *pgPlayerProgressRepository) Get(ctx context.Context, userID uint64, publishedStoryID uuid.UUID) (*models.PlayerProgress, error) {
// ... implementation removed ...
// }

// Upsert creates a new player progress record or updates an existing one.
// func (r *pgPlayerProgressRepository) Upsert(ctx context.Context, progress *models.PlayerProgress) error {
// ... implementation removed ...
// }

const getPlayerProgressQuery = `
SELECT user_id, published_story_id, current_core_stats, current_story_variables, current_global_flags, current_state_hash, scene_index, updated_at, last_story_summary, last_future_direction, last_var_impact_summary
FROM player_progress
WHERE user_id = $1 AND published_story_id = $2`

const upsertPlayerProgressQuery = `
INSERT INTO player_progress (user_id, published_story_id, current_core_stats, current_story_variables, current_global_flags, current_state_hash, scene_index, updated_at, last_story_summary, last_future_direction, last_var_impact_summary)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (user_id, published_story_id) DO UPDATE SET
    current_core_stats = EXCLUDED.current_core_stats,
    current_story_variables = EXCLUDED.current_story_variables,
    current_global_flags = EXCLUDED.current_global_flags,
    current_state_hash = EXCLUDED.current_state_hash,
    scene_index = EXCLUDED.scene_index,
    updated_at = EXCLUDED.updated_at,
    -- created_at не обновляется при конфликте (и столбца нет)
    last_story_summary = EXCLUDED.last_story_summary,
    last_future_direction = EXCLUDED.last_future_direction,
    last_var_impact_summary = EXCLUDED.last_var_impact_summary
`

const deletePlayerProgressQuery = `
DELETE FROM player_progress
WHERE user_id = $1 AND published_story_id = $2`

// GetByUserIDAndStoryID now accepts userID as uuid.UUID
func (r *pgPlayerProgressRepository) GetByUserIDAndStoryID(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) (*models.PlayerProgress, error) {
	progress := &models.PlayerProgress{}
	var coreStatsJSON, storyVarsJSON []byte // Use []byte for scanning jsonb
	// var globalFlags pq.StringArray    // Use direct type, it should handle '{}'
	var globalFlags []string                                                                                            // Use native pgx scanning into Go slice
	logFields := []zap.Field{zap.Stringer("userID", userID), zap.String("publishedStoryID", publishedStoryID.String())} // Log UUID

	err := r.pool.QueryRow(ctx, getPlayerProgressQuery, userID, publishedStoryID).Scan(
		&progress.UserID, // This should now scan a UUID correctly
		&progress.PublishedStoryID,
		&coreStatsJSON,
		&storyVarsJSON,
		&globalFlags, // Scan directly into []string slice
		&progress.CurrentStateHash,
		&progress.SceneIndex,
		&progress.UpdatedAt,
		&progress.LastStorySummary,
		&progress.LastFutureDirection,
		&progress.LastVarImpactSummary,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, models.ErrNotFound // Use specific error for not found
		}
		// r.logger.Error("Failed to get player progress", zap.Error(err), zap.Uint64("userID", userID), zap.String("publishedStoryID", publishedStoryID.String()))
		r.logger.Error("Failed to get player progress", append(logFields, zap.Error(err))...)
		return nil, err // Return generic error after logging
	}

	// Unmarshal JSONB fields
	if err := utils.UnmarshalMap(coreStatsJSON, &progress.CoreStats); err != nil {
		// r.logger.Error("Failed to unmarshal core stats", zap.Error(err), zap.Uint64("userID", userID))
		r.logger.Error("Failed to unmarshal core stats", append(logFields, zap.Error(err))...)
		return nil, err
	}
	if err := utils.UnmarshalMap(storyVarsJSON, &progress.StoryVariables); err != nil {
		// r.logger.Error("Failed to unmarshal story variables", zap.Error(err), zap.Uint64("userID", userID))
		r.logger.Error("Failed to unmarshal story variables", append(logFields, zap.Error(err))...)
		return nil, err
	}

	// Handle potentially nil globalFlags (removed, pq.StringArray should handle '{}')
	// if globalFlags != nil {
	// 	progress.GlobalFlags = []string(*globalFlags) // Dereference pointer if not nil
	// } else {
	// 	progress.GlobalFlags = []string{} // Assign empty slice if DB value was NULL
	// }
	progress.GlobalFlags = globalFlags // Assign the scanned slice directly

	// r.logger.Debug("Retrieved player progress", zap.Uint64("userID", userID), zap.String("publishedStoryID", publishedStoryID.String()))
	r.logger.Debug("Retrieved player progress", logFields...)
	return progress, nil
}

// CreateOrUpdate still accepts *models.PlayerProgress, which now has UserID as uuid.UUID
func (r *pgPlayerProgressRepository) CreateOrUpdate(ctx context.Context, progress *models.PlayerProgress) error {
	now := time.Now()
	// if progress.CreatedAt.IsZero() { // Убираем логику с CreatedAt
	// 	progress.CreatedAt = now
	// }
	progress.UpdatedAt = now
	logFields := []zap.Field{
		zap.Stringer("userID", progress.UserID),
		zap.String("publishedStoryID", progress.PublishedStoryID.String()),
		zap.String("stateHash", progress.CurrentStateHash),
		zap.Int("sceneIndex", progress.SceneIndex), // <<< Логгируем SceneIndex
	}

	// Marshal map fields to JSON
	coreStatsJSON, err := utils.MarshalMap(progress.CoreStats)
	if err != nil {
		r.logger.Error("Failed to marshal core stats for upsert", append(logFields, zap.Error(err))...)
		return err
	}
	storyVarsJSON, err := utils.MarshalMap(progress.StoryVariables)
	if err != nil {
		r.logger.Error("Failed to marshal story variables for upsert", append(logFields, zap.Error(err))...)
		return err
	}

	// Handle empty global flags array - pass explicit '{}' literal to DB
	var globalFlagsValue interface{}
	if len(progress.GlobalFlags) > 0 {
		globalFlagsValue = pq.Array(progress.GlobalFlags)
	} else {
		globalFlagsValue = "{}" // Pass the string literal for an empty array
	}

	_, err = r.pool.Exec(ctx, upsertPlayerProgressQuery,
		progress.UserID,
		progress.PublishedStoryID,
		coreStatsJSON,
		storyVarsJSON,
		globalFlagsValue, // Pass the prepared value ('{}' or pq.Array)
		progress.CurrentStateHash,
		progress.SceneIndex,
		progress.UpdatedAt,
		progress.LastStorySummary,
		progress.LastFutureDirection,
		progress.LastVarImpactSummary,
	)

	if err != nil {
		r.logger.Error("Failed to upsert player progress", append(logFields, zap.Error(err))...)
		return err // Return generic error after logging
	}

	r.logger.Debug("Upserted player progress", logFields...)
	return nil
}

// Delete now accepts userID as uuid.UUID
func (r *pgPlayerProgressRepository) Delete(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) error {
	logFields := []zap.Field{zap.Stringer("userID", userID), zap.String("publishedStoryID", publishedStoryID.String())} // Log UUID
	cmdTag, err := r.pool.Exec(ctx, deletePlayerProgressQuery, userID, publishedStoryID)                                // Pass UUID directly
	if err != nil {
		// r.logger.Error("Failed to delete player progress", zap.Error(err),
		// 	zap.Uint64("userID", userID),
		// 	zap.String("publishedStoryID", publishedStoryID.String()))
		r.logger.Error("Failed to delete player progress", append(logFields, zap.Error(err))...)
		return err // Return generic error after logging
	}

	if cmdTag.RowsAffected() == 0 {
		// r.logger.Warn("Attempted to delete non-existent player progress",
		// 	zap.Uint64("userID", userID),
		// 	zap.String("publishedStoryID", publishedStoryID.String()))
		r.logger.Warn("Attempted to delete non-existent player progress", logFields...)
		// Возвращаем nil, так как цель (отсутствие прогресса) достигнута
	} else {
		// r.logger.Info("Deleted player progress",
		// 	zap.Uint64("userID", userID),
		// 	zap.String("publishedStoryID", publishedStoryID.String()))
		r.logger.Info("Deleted player progress", logFields...)
	}

	return nil
}

// CheckProgressExistsForStories checks if player progress exists for a given user and multiple story IDs.
func (r *pgPlayerProgressRepository) CheckProgressExistsForStories(ctx context.Context, userID uuid.UUID, storyIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	logFields := []zap.Field{zap.Stringer("userID", userID), zap.Int("storyIDCount", len(storyIDs))}
	r.logger.Debug("Checking progress existence for multiple stories", logFields...)

	if len(storyIDs) == 0 {
		return make(map[uuid.UUID]bool), nil // Return empty map if no IDs provided
	}

	query := `
		SELECT DISTINCT published_story_id
		FROM player_progress
		WHERE user_id = $1 AND published_story_id = ANY($2::uuid[])
	`

	rows, err := r.pool.Query(ctx, query, userID, storyIDs)
	if err != nil {
		r.logger.Error("Failed to query progress existence for stories", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("error checking progress existence: %w", err)
	}
	defer rows.Close()

	// Initialize the map with all requested IDs set to false
	progressExistsMap := make(map[uuid.UUID]bool, len(storyIDs))
	for _, id := range storyIDs {
		progressExistsMap[id] = false
	}

	// Iterate through the results and mark existing progress as true
	for rows.Next() {
		var foundStoryID uuid.UUID
		if err := rows.Scan(&foundStoryID); err != nil {
			r.logger.Error("Failed to scan existing progress story ID", append(logFields, zap.Error(err))...)
			// Decide how to handle scan error: return error or just skip this ID?
			// Returning error is safer to signal potential data inconsistency.
			return nil, fmt.Errorf("error scanning progress existence results: %w", err)
		}
		progressExistsMap[foundStoryID] = true
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("Error iterating progress existence results", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("error iterating progress existence results: %w", err)
	}

	r.logger.Debug("Successfully checked progress existence", logFields...)
	return progressExistsMap, nil
}
