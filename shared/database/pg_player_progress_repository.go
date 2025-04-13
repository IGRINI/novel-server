package database

import (
	"context"
	"errors"
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
SELECT id, user_id, published_story_id, core_stats, story_variables, global_flags, current_state_hash, created_at, updated_at
FROM player_progress
WHERE user_id = $1 AND published_story_id = $2`

const upsertPlayerProgressQuery = `
INSERT INTO player_progress (id, user_id, published_story_id, core_stats, story_variables, global_flags, current_state_hash, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (user_id, published_story_id) DO UPDATE SET
    core_stats = EXCLUDED.core_stats,
    story_variables = EXCLUDED.story_variables,
    global_flags = EXCLUDED.global_flags,
    current_state_hash = EXCLUDED.current_state_hash,
    updated_at = EXCLUDED.updated_at
`

const deletePlayerProgressQuery = `
DELETE FROM player_progress
WHERE user_id = $1 AND published_story_id = $2`

func (r *pgPlayerProgressRepository) GetByUserIDAndStoryID(ctx context.Context, userID, publishedStoryID uuid.UUID) (*models.PlayerProgress, error) {
	progress := &models.PlayerProgress{}
	var coreStatsJSON, storyVarsJSON []byte // Use []byte for scanning jsonb
	var globalFlags pq.StringArray

	err := r.pool.QueryRow(ctx, getPlayerProgressQuery, userID, publishedStoryID).Scan(
		&progress.ID,
		&progress.UserID,           // Correct field
		&progress.PublishedStoryID, // Correct field
		&coreStatsJSON,             // Target for jsonb
		&storyVarsJSON,             // Target for jsonb
		&globalFlags,               // Target for text[]
		&progress.CurrentStateHash, // Correct field
		&progress.CreatedAt,        // Correct field
		&progress.UpdatedAt,        // Correct field
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, models.ErrNotFound // Use specific error for not found
		}
		r.logger.Error("Failed to get player progress", zap.Error(err), zap.String("userID", userID.String()), zap.String("publishedStoryID", publishedStoryID.String()))
		return nil, err // Return generic error after logging
	}

	// Unmarshal JSONB fields
	if err := utils.UnmarshalMap(coreStatsJSON, &progress.CoreStats); err != nil { // Correct field
		r.logger.Error("Failed to unmarshal core stats", zap.Error(err), zap.String("userID", userID.String()))
		return nil, err
	}
	if err := utils.UnmarshalMap(storyVarsJSON, &progress.StoryVariables); err != nil { // Correct field
		r.logger.Error("Failed to unmarshal story variables", zap.Error(err), zap.String("userID", userID.String()))
		return nil, err
	}
	progress.GlobalFlags = []string(globalFlags) // Assign scanned array

	r.logger.Debug("Retrieved player progress", zap.String("userID", userID.String()), zap.String("publishedStoryID", publishedStoryID.String()))
	return progress, nil
}

func (r *pgPlayerProgressRepository) CreateOrUpdate(ctx context.Context, progress *models.PlayerProgress) error {
	if progress.ID == uuid.Nil {
		progress.ID = uuid.New()
	}
	now := time.Now()
	if progress.CreatedAt.IsZero() {
		progress.CreatedAt = now
	}
	progress.UpdatedAt = now

	// Marshal map fields to JSON
	coreStatsJSON, err := utils.MarshalMap(progress.CoreStats) // Correct field
	if err != nil {
		r.logger.Error("Failed to marshal core stats for upsert", zap.Error(err), zap.String("progressID", progress.ID.String()))
		return err
	}
	storyVarsJSON, err := utils.MarshalMap(progress.StoryVariables) // Correct field
	if err != nil {
		r.logger.Error("Failed to marshal story variables for upsert", zap.Error(err), zap.String("progressID", progress.ID.String()))
		return err
	}

	_, err = r.pool.Exec(ctx, upsertPlayerProgressQuery,
		progress.ID,
		progress.UserID,
		progress.PublishedStoryID,
		coreStatsJSON,
		storyVarsJSON,
		pq.Array(progress.GlobalFlags), // Correct field
		progress.CurrentStateHash,      // Correct field
		progress.CreatedAt,
		progress.UpdatedAt,
	)

	if err != nil {
		r.logger.Error("Failed to upsert player progress", zap.Error(err),
			zap.String("userID", progress.UserID.String()), // Log UserID as string
			zap.String("publishedStoryID", progress.PublishedStoryID.String()))
		return err // Return generic error after logging
	}

	r.logger.Debug("Upserted player progress", zap.String("userID", progress.UserID.String()), zap.String("stateHash", progress.CurrentStateHash)) // Log CurrentStateHash
	return nil
}

func (r *pgPlayerProgressRepository) Delete(ctx context.Context, userID, publishedStoryID uuid.UUID) error {
	cmdTag, err := r.pool.Exec(ctx, deletePlayerProgressQuery, userID, publishedStoryID)
	if err != nil {
		r.logger.Error("Failed to delete player progress", zap.Error(err),
			zap.String("userID", userID.String()),
			zap.String("publishedStoryID", publishedStoryID.String()))
		return err // Return generic error after logging
	}

	if cmdTag.RowsAffected() == 0 {
		r.logger.Warn("Attempted to delete non-existent player progress",
			zap.String("userID", userID.String()),
			zap.String("publishedStoryID", publishedStoryID.String()))
		// Возвращаем nil, так как цель (отсутствие прогресса) достигнута
	} else {
		r.logger.Info("Deleted player progress",
			zap.String("userID", userID.String()),
			zap.String("publishedStoryID", publishedStoryID.String()))
	}

	return nil
}
