package database

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"novel-server/shared/interfaces"
	"novel-server/shared/models"
	"novel-server/shared/utils"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// Compile-time check to ensure implementation satisfies the interface.
var _ interfaces.PlayerProgressRepository = (*pgPlayerProgressRepository)(nil)

type pgPlayerProgressRepository struct {
	db     interfaces.DBTX // Changed field name for clarity
	logger *zap.Logger
	// pool *pgxpool.Pool // Removed
}

// NewPgPlayerProgressRepository creates a new repository instance.
// <<< ИЗМЕНЕНО: Принимает interfaces.DBTX >>>
func NewPgPlayerProgressRepository(querier interfaces.DBTX, logger *zap.Logger) interfaces.PlayerProgressRepository {
	return &pgPlayerProgressRepository{
		db:     querier,
		logger: logger.Named("PgPlayerProgressRepo"),
		// pool:   pool, // Removed
	}
}

// --- Constants for SQL Queries ---

const getPlayerProgressBaseFields = `
	id, user_id, published_story_id, current_core_stats, current_story_variables, 
	current_state_hash, scene_index, created_at, updated_at, 
	last_story_summary, last_future_direction, last_var_impact_summary,
	current_scene_summary
`

const getPlayerProgressByIDQuery = `SELECT ` + getPlayerProgressBaseFields + ` FROM player_progress WHERE id = $1`

const getPlayerProgressByStoryAndHashQuery = `SELECT ` + getPlayerProgressBaseFields + ` FROM player_progress WHERE published_story_id = $1 AND current_state_hash = $2`

// Note: GetByUserIDAndStoryID might become less relevant if PlayerGameState is the primary entry point.
const getPlayerProgressByUserAndStoryQuery = `SELECT ` + getPlayerProgressBaseFields + ` FROM player_progress WHERE user_id = $1 AND published_story_id = $2`

const insertPlayerProgressQuery = `
INSERT INTO player_progress (user_id, published_story_id, current_core_stats, current_story_variables, current_state_hash, scene_index, created_at, updated_at, last_story_summary, last_future_direction, last_var_impact_summary, current_scene_summary)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING id, created_at` // Return ID and CreatedAt

const updatePlayerProgressQuery = `
UPDATE player_progress SET
    user_id = $2,
    published_story_id = $3,
    current_core_stats = $4,
    current_story_variables = $5,
    current_state_hash = $6,
    scene_index = $7,
    updated_at = $8,
    last_story_summary = $9,
    last_future_direction = $10,
    last_var_impact_summary = $11,
    current_scene_summary = $12
WHERE id = $1
RETURNING updated_at` // Return UpdatedAt

const deletePlayerProgressByIDQuery = `DELETE FROM player_progress WHERE id = $1`

// Old query for DeleteByUserIDAndStoryID (kept separate if needed)
const deletePlayerProgressByUserAndStoryQuery = `DELETE FROM player_progress WHERE user_id = $1 AND published_story_id = $2`

// --- Helper Function for Scanning ---

// scanPlayerProgress scans a row into a PlayerProgress struct.
func scanPlayerProgress(row pgx.Row) (*models.PlayerProgress, error) {
	progress := &models.PlayerProgress{}
	var coreStatsJSON, storyVarsJSON []byte
	// var globalFlags []string // Удалено, так как поле удалено из БД и модели
	var userID, storyID *uuid.UUID // Nullable fields need pointers
	var lastSummary, lastDirection, lastImpact, currentSummary *string

	err := row.Scan(
		&progress.ID,
		&userID,  // Scan into pointer
		&storyID, // Scan into pointer
		&coreStatsJSON,
		&storyVarsJSON,
		// &globalFlags, // Удалено
		&progress.CurrentStateHash,
		&progress.SceneIndex,
		&progress.CreatedAt,
		&progress.UpdatedAt,
		&lastSummary,
		&lastDirection,
		&lastImpact,
		&currentSummary,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, models.ErrNotFound
		}
		return nil, fmt.Errorf("error scanning player progress: %w", err)
	}

	// Assign values from pointers if they are not nil
	if userID != nil {
		progress.UserID = *userID
	}
	if storyID != nil {
		progress.PublishedStoryID = *storyID
	}
	progress.LastStorySummary = lastSummary
	progress.LastFutureDirection = lastDirection
	progress.LastVarImpactSummary = lastImpact
	progress.CurrentSceneSummary = currentSummary

	// Unmarshal JSONB fields
	if err := utils.UnmarshalMap(coreStatsJSON, &progress.CoreStats); err != nil {
		return nil, fmt.Errorf("failed to unmarshal core stats: %w", err)
	}

	return progress, nil
}

// --- Interface Method Implementations ---

// GetByID retrieves a specific progress node by its unique ID.
func (r *pgPlayerProgressRepository) GetByID(ctx context.Context, querier interfaces.DBTX, progressID uuid.UUID) (*models.PlayerProgress, error) {
	logFields := []zap.Field{zap.String("progressID", progressID.String())}
	row := querier.QueryRow(ctx, getPlayerProgressByIDQuery, progressID)
	progress, err := scanPlayerProgress(row)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			r.logger.Warn("Player progress not found by ID", logFields...)
		} else {
			r.logger.Error("Failed to get player progress by ID", append(logFields, zap.Error(err))...)
		}
		return nil, err // Return ErrNotFound or wrapped error
	}
	r.logger.Debug("Retrieved player progress by ID", logFields...)
	return progress, nil
}

// GetByStoryIDAndHash retrieves a specific progress node by story ID and state hash.
func (r *pgPlayerProgressRepository) GetByStoryIDAndHash(ctx context.Context, querier interfaces.DBTX, publishedStoryID uuid.UUID, stateHash string) (*models.PlayerProgress, error) {
	logFields := []zap.Field{zap.String("publishedStoryID", publishedStoryID.String()), zap.String("stateHash", stateHash)}
	row := querier.QueryRow(ctx, getPlayerProgressByStoryAndHashQuery, publishedStoryID, stateHash)
	progress, err := scanPlayerProgress(row)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			r.logger.Warn("Player progress not found by story and hash", logFields...)
		} else {
			r.logger.Error("Failed to get player progress by story and hash", append(logFields, zap.Error(err))...)
		}
		return nil, err // Return ErrNotFound or wrapped error
	}
	r.logger.Debug("Retrieved player progress by story and hash", logFields...)
	return progress, nil
}

// GetByUserIDAndStoryID retrieves the player's progress for a specific story.
// Note: This might be less used now, potentially replaced by lookups via PlayerGameState.
// Since user_id and published_story_id might be null in the DB now, this might return ErrNotFound
// even if a progress node exists but isn't directly linked to a user/story via these columns.
func (r *pgPlayerProgressRepository) GetByUserIDAndStoryID(ctx context.Context, querier interfaces.DBTX, userID uuid.UUID, publishedStoryID uuid.UUID) (*models.PlayerProgress, error) {
	logFields := []zap.Field{zap.Stringer("userID", userID), zap.String("publishedStoryID", publishedStoryID.String())}
	row := querier.QueryRow(ctx, getPlayerProgressByUserAndStoryQuery, userID, publishedStoryID)
	progress, err := scanPlayerProgress(row)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			r.logger.Warn("Player progress not found by user and story", logFields...)
		} else {
			r.logger.Error("Failed to get player progress by user and story", append(logFields, zap.Error(err))...)
		}
		return nil, err // Return ErrNotFound or wrapped error
	}
	r.logger.Debug("Retrieved player progress by user and story", logFields...)
	return progress, nil
}

// Save creates a new player progress node if progress.ID is zero UUID,
// or updates an existing one based on progress.ID.
// Returns the ID of the created/updated record.
func (r *pgPlayerProgressRepository) Save(ctx context.Context, querier interfaces.DBTX, progress *models.PlayerProgress) (uuid.UUID, error) {
	now := time.Now().UTC() // Use UTC
	progress.UpdatedAt = now

	logFields := []zap.Field{
		// UserID and StoryID might be zero if creating a generic node
		zap.Stringer("userID", progress.UserID),
		zap.String("publishedStoryID", progress.PublishedStoryID.String()),
		zap.String("stateHash", progress.CurrentStateHash),
		zap.Int("sceneIndex", progress.SceneIndex),
	}
	if progress.ID != uuid.Nil {
		logFields = append(logFields, zap.String("progressID", progress.ID.String()))
	}

	// Marshal map fields to JSON
	coreStatsJSON, err := utils.MarshalMap(progress.CoreStats)
	if err != nil {
		r.logger.Error("Failed to marshal core stats for save", append(logFields, zap.Error(err))...)
		return uuid.Nil, err
	}

	// Handle potentially nil nullable fields (UserID, StoryID)
	var userIDArg, storyIDArg interface{}
	if progress.UserID != uuid.Nil {
		userIDArg = progress.UserID
	} else {
		userIDArg = nil // Pass nil to DB if UserID is zero UUID
	}
	if progress.PublishedStoryID != uuid.Nil {
		storyIDArg = progress.PublishedStoryID
	} else {
		storyIDArg = nil // Pass nil to DB if StoryID is zero UUID
	}

	if progress.ID == uuid.Nil {
		// --- INSERT new progress node ---
		r.logger.Debug("Inserting new player progress node", logFields...)
		progress.CreatedAt = now // Set CreatedAt only on insert

		err = querier.QueryRow(ctx, insertPlayerProgressQuery,
			userIDArg,                     // $1 (nullable)
			storyIDArg,                    // $2 (nullable)
			coreStatsJSON,                 // $3
			progress.CurrentStateHash,     // $5
			progress.SceneIndex,           // $6
			progress.CreatedAt,            // $7 (Use the value set above)
			progress.UpdatedAt,            // $8 (Use the value set above)
			progress.LastStorySummary,     // $9
			progress.LastFutureDirection,  // $10
			progress.LastVarImpactSummary, // $11
			progress.CurrentSceneSummary,  // $12
		).Scan(&progress.ID, &progress.CreatedAt) // Scan the returned ID and CreatedAt

		if err != nil {
			r.logger.Error("Failed to insert player progress node", append(logFields, zap.Error(err))...)
			// TODO: Check for unique constraint violation errors (e.g., unique_story_state_hash)?
			return uuid.Nil, err
		}
		r.logger.Info("Inserted new player progress node", append(logFields, zap.String("newProgressID", progress.ID.String()))...)
		return progress.ID, nil

	} else {
		// --- UPDATE existing progress node ---
		r.logger.Debug("Updating existing player progress node", logFields...)

		err = querier.QueryRow(ctx, updatePlayerProgressQuery,
			progress.ID,                   // $1 for WHERE clause
			userIDArg,                     // $2 (nullable)
			storyIDArg,                    // $3 (nullable)
			coreStatsJSON,                 // $4
			progress.CurrentStateHash,     // $6
			progress.SceneIndex,           // $7
			progress.UpdatedAt,            // $8 (Use the value set above)
			progress.LastStorySummary,     // $9
			progress.LastFutureDirection,  // $10
			progress.LastVarImpactSummary, // $11
			progress.CurrentSceneSummary,  // $12
		).Scan(&progress.UpdatedAt) // Scan the returned UpdatedAt timestamp

		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				r.logger.Warn("Attempted to update non-existent player progress node", logFields...)
				return uuid.Nil, models.ErrNotFound
			}
			r.logger.Error("Failed to update player progress node", append(logFields, zap.Error(err))...)
			return uuid.Nil, err
		}
		r.logger.Info("Updated existing player progress node", logFields...)
		return progress.ID, nil
	}
}

// Delete deletes a specific progress node by its ID.
func (r *pgPlayerProgressRepository) Delete(ctx context.Context, querier interfaces.DBTX, progressID uuid.UUID) error {
	logFields := []zap.Field{zap.String("progressID", progressID.String())}
	cmdTag, err := querier.Exec(ctx, deletePlayerProgressByIDQuery, progressID)
	if err != nil {
		r.logger.Error("Failed to delete player progress node by ID", append(logFields, zap.Error(err))...)
		return err
	}
	if cmdTag.RowsAffected() == 0 {
		r.logger.Warn("Attempted to delete non-existent player progress node by ID", logFields...)
		return models.ErrNotFound
	}
	r.logger.Info("Deleted player progress node by ID", logFields...)
	return nil
}

// --- Deprecated / Internal Methods ---

// DeleteByUserIDAndStoryID - Keeping the old delete logic under a new name for potential use cases
// (like resetting progress) but it's NOT part of the PlayerProgressRepository interface anymore.
func (r *pgPlayerProgressRepository) DeleteByUserIDAndStoryID(ctx context.Context, querier interfaces.DBTX, userID uuid.UUID, publishedStoryID uuid.UUID) error {
	logFields := []zap.Field{zap.Stringer("userID", userID), zap.String("publishedStoryID", publishedStoryID.String())}
	cmdTag, err := querier.Exec(ctx, deletePlayerProgressByUserAndStoryQuery, userID, publishedStoryID)
	if err != nil {
		r.logger.Error("Failed to delete player progress by user and story", append(logFields, zap.Error(err))...)
		return err
	}
	if cmdTag.RowsAffected() == 0 {
		r.logger.Warn("Attempted to delete non-existent player progress by user and story", logFields...)
		// Return nil as the state (no progress for user/story) is achieved.
		return nil
	}
	r.logger.Info("Deleted player progress by user and story", logFields...)
	return nil
}

// CheckProgressExistsForStories checks if player progress exists for a given user and multiple story IDs.
// Note: This implementation still relies on user_id/story_id lookup.
// Its relevance might decrease depending on how player progress is managed.
func (r *pgPlayerProgressRepository) CheckProgressExistsForStories(ctx context.Context, querier interfaces.DBTX, userID uuid.UUID, storyIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	logFields := []zap.Field{zap.Stringer("userID", userID), zap.Int("storyIDCount", len(storyIDs))}
	r.logger.Debug("Checking progress existence for multiple stories", logFields...)

	if len(storyIDs) == 0 {
		return make(map[uuid.UUID]bool), nil // Return empty map if no IDs provided
	}

	// This query might need adjustment depending on the exact definition of "progress exists"
	// Currently checks if *any* progress node exists for the user/story combination.
	query := `
		SELECT DISTINCT published_story_id
		FROM player_progress
		WHERE user_id = $1 AND published_story_id = ANY($2::uuid[])
	`

	rows, err := querier.Query(ctx, query, userID, storyIDs)
	if err != nil {
		r.logger.Error("Failed to query progress existence for stories", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("error checking progress existence: %w", err)
	}
	defer rows.Close()

	progressExistsMap := make(map[uuid.UUID]bool, len(storyIDs))
	for _, id := range storyIDs {
		progressExistsMap[id] = false
	}

	for rows.Next() {
		var foundStoryID uuid.UUID
		if err := rows.Scan(&foundStoryID); err != nil {
			r.logger.Error("Failed to scan existing progress story ID", append(logFields, zap.Error(err))...)
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

// UpdateFields updates specific fields for a player progress node.
func (r *pgPlayerProgressRepository) UpdateFields(ctx context.Context, querier interfaces.DBTX, progressID uuid.UUID, updates map[string]interface{}) error {
	logFields := []zap.Field{zap.String("progressID", progressID.String()), zap.Any("updates", updates)}
	r.logger.Debug("Updating specific fields for player progress", logFields...)

	if len(updates) == 0 {
		r.logger.Warn("UpdateFields called with empty updates map", logFields...)
		return nil // No fields to update
	}

	// --- Построение динамического SQL запроса --- //
	query := "UPDATE player_progress SET "
	args := make([]interface{}, 0, len(updates)+1) // +1 для progressID в WHERE
	argCounter := 1

	// Отслеживаем порядок аргументов
	updateClauses := make([]string, 0, len(updates))

	for field, value := range updates {
		// Валидация имени поля (простая проверка, можно улучшить)
		// TODO: Добавить более строгую валидацию разрешенных полей?
		if !isValidPlayerProgressField(field) {
			r.logger.Error("UpdateFields called with invalid field name", append(logFields, zap.String("invalidField", field))...)
			return fmt.Errorf("%w: invalid field name '%s' for player progress update", models.ErrBadRequest, field)
		}

		// Добавляем поле и placeholder в запрос
		updateClauses = append(updateClauses, fmt.Sprintf("%s = $%d", field, argCounter))
		args = append(args, value)
		argCounter++
	}

	// Добавляем updatedAt автоматически, если его не передали явно
	if _, ok := updates["updated_at"]; !ok {
		updateClauses = append(updateClauses, fmt.Sprintf("updated_at = $%d", argCounter))
		args = append(args, time.Now().UTC())
		argCounter++
	}

	// Собираем SET клаузу
	query += fmt.Sprintf("%s", strings.Join(updateClauses, ", "))

	// Добавляем WHERE клаузу
	query += fmt.Sprintf(" WHERE id = $%d", argCounter)
	args = append(args, progressID)

	r.logger.Debug("Executing dynamic update query", append(logFields, zap.String("query", query), zap.Any("args", args))...)

	// --- Выполнение запроса --- //
	cmdTag, err := querier.Exec(ctx, query, args...)
	if err != nil {
		// TODO: Обработать специфичные ошибки БД, если нужно (e.g., неверный тип данных)
		r.logger.Error("Failed to execute dynamic update for player progress", append(logFields, zap.Error(err))...)
		return fmt.Errorf("database error during player progress update: %w", err)
	}

	if cmdTag.RowsAffected() == 0 {
		r.logger.Warn("Attempted to update non-existent player progress node with UpdateFields", logFields...)
		return models.ErrNotFound
	}

	r.logger.Info("Successfully updated player progress fields", logFields...)
	return nil
}

// isValidPlayerProgressField - вспомогательная функция для валидации имен полей.
// TODO: Сделать эту проверку более надежной (например, используя reflect или список разрешенных полей).
func isValidPlayerProgressField(field string) bool {
	// Простая проверка на разрешенные поля (можно расширить)
	switch field {
	case "user_id",
		"published_story_id",
		"current_core_stats",      // JSONB
		"current_story_variables", // JSONB
		"current_state_hash",
		"scene_index",
		"updated_at", // Обычно обновляется автоматически
		"last_story_summary",
		"last_future_direction",
		"last_var_impact_summary",
		"current_scene_summary",
		"progress_data_json": // Добавлено поле для примера в game_loop_service
		return true
	default:
		return false
	}
}

// UpsertInitial attempts to insert an initial player progress record using INSERT ... ON CONFLICT.
// It returns the ID of the existing or newly inserted record.
func (r *pgPlayerProgressRepository) UpsertInitial(ctx context.Context, querier interfaces.DBTX, progress *models.PlayerProgress) (uuid.UUID, error) {
	const query = `
        INSERT INTO player_progress (
            id, user_id, published_story_id, current_state_hash, current_core_stats,
            current_story_variables, scene_index
        )
        VALUES ($1, $2, $3, $4, $5, $6, $7)
        ON CONFLICT (published_story_id, current_state_hash)
        DO UPDATE SET
            -- No actual update needed, just need to retrieve the ID.
            updated_at = NOW()
        RETURNING id;
    `
	log := r.logger.With(
		zap.String("method", "UpsertInitial"),
		zap.Stringer("userID", progress.UserID),
		zap.Stringer("storyID", progress.PublishedStoryID),
		zap.String("hash", progress.CurrentStateHash),
	)

	if progress.CurrentStateHash != models.InitialStateHash {
		log.Error("UpsertInitial called with non-initial state hash", zap.String("providedHash", progress.CurrentStateHash))
		return uuid.Nil, fmt.Errorf("UpsertInitial must be used only with InitialStateHash")
	}

	// Ensure ID is set if it's nil
	if progress.ID == uuid.Nil {
		progress.ID = uuid.New()
	}

	// Marshal maps to JSON
	coreStatsJSON, errM1 := utils.MarshalMap(progress.CoreStats)
	if errM1 != nil {
		log.Error("Failed to marshal progress data for UpsertInitial", zap.Error(errM1))
		return uuid.Nil, fmt.Errorf("failed to marshal progress data: %v", errM1)
	}

	var returnedID uuid.UUID
	err := querier.QueryRow(ctx, query,
		progress.ID, progress.UserID, progress.PublishedStoryID, progress.CurrentStateHash,
		coreStatsJSON,
		progress.SceneIndex,
	).Scan(&returnedID)

	if err != nil {
		log.Error("Failed to execute UpsertInitial query", zap.Error(err))
		return uuid.Nil, fmt.Errorf("database error during UpsertInitial: %w", err)
	}

	log.Info("Initial progress node upserted/retrieved successfully", zap.Stringer("progressID", returnedID))
	return returnedID, nil
}

// Update updates an existing player progress record.
func (r *pgPlayerProgressRepository) Update(ctx context.Context, querier interfaces.DBTX, progress *models.PlayerProgress) error {
	const query = `
        UPDATE player_progress SET
            current_state_hash = $2,
            current_core_stats = $3,
            current_story_variables = $4,
            scene_index = $5,
            updated_at = NOW()
        WHERE id = $1 AND user_id = $6;
    `
	log := r.logger.With(
		zap.String("method", "Update"),
		zap.Stringer("progressID", progress.ID),
		zap.Stringer("userID", progress.UserID),
	)

	// Marshal maps to JSON
	coreStatsJSON, errM1 := utils.MarshalMap(progress.CoreStats)
	if errM1 != nil {
		log.Error("Failed to marshal progress data for Update", zap.Error(errM1))
		return fmt.Errorf("failed to marshal progress data: %v", errM1)
	}

	cmdTag, err := querier.Exec(ctx, query,
		progress.ID, progress.CurrentStateHash,
		coreStatsJSON,
		progress.SceneIndex,
		progress.UserID, // For WHERE clause
	)

	if err != nil {
		log.Error("Failed to execute update query for player progress", zap.Error(err))
		return fmt.Errorf("database error during player progress update: %w", err)
	}

	if cmdTag.RowsAffected() == 0 {
		log.Warn("No player progress found to update, or user ID mismatch", zap.Stringer("progressID", progress.ID), zap.Stringer("userID", progress.UserID))
		// Consider checking if the record exists to return ErrNotFound vs potentially ErrForbidden
		return models.ErrNotFound // Or a more specific error
	}

	log.Info("Player progress updated successfully")
	return nil
}

// UpsertByHash attempts to insert a player progress record based on its state hash.
// Returns the ID of the existing or newly inserted record.
func (r *pgPlayerProgressRepository) UpsertByHash(ctx context.Context, querier interfaces.DBTX, progress *models.PlayerProgress) (uuid.UUID, error) {
	// Note: This query assumes a unique constraint exists on (published_story_id, current_state_hash)
	const query = `
        INSERT INTO player_progress (
            id, user_id, published_story_id, current_state_hash, current_core_stats,
            current_story_variables, scene_index,
            last_story_summary, last_future_direction, last_var_impact_summary, current_scene_summary,
            created_at, updated_at
        )
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
        ON CONFLICT (published_story_id, current_state_hash)
        DO UPDATE SET
            -- Update updated_at just to ensure RETURNING id works correctly and potentially track reuse
            updated_at = NOW()
        RETURNING id;
    `
	log := r.logger.With(
		zap.String("method", "UpsertByHash"),
		zap.Stringer("userID", progress.UserID), // Log UserID even if not in constraint
		zap.Stringer("storyID", progress.PublishedStoryID),
		zap.String("hash", progress.CurrentStateHash),
	)

	// Ensure ID is set for the potential insert
	if progress.ID == uuid.Nil {
		progress.ID = uuid.New()
	}
	now := time.Now().UTC()
	progress.CreatedAt = now // Set in case it's an insert
	progress.UpdatedAt = now // Set in case it's an insert

	// Marshal maps to JSON
	coreStatsJSON, errM1 := utils.MarshalMap(progress.CoreStats)
	if errM1 != nil {
		log.Error("Failed to marshal progress data for UpsertByHash", zap.Error(errM1))
		return uuid.Nil, fmt.Errorf("failed to marshal progress data: %v", errM1)
	}

	var returnedID uuid.UUID
	err := querier.QueryRow(ctx, query,
		progress.ID, progress.UserID, progress.PublishedStoryID, progress.CurrentStateHash,
		coreStatsJSON, progress.SceneIndex,
		progress.LastStorySummary, progress.LastFutureDirection, progress.LastVarImpactSummary, progress.CurrentSceneSummary,
		progress.CreatedAt, progress.UpdatedAt,
	).Scan(&returnedID)

	if err != nil {
		log.Error("Failed to execute UpsertByHash query", zap.Error(err))
		// TODO: Check for specific constraint errors if needed
		return uuid.Nil, fmt.Errorf("database error during UpsertByHash: %w", err)
	}

	log.Info("Progress node upserted/retrieved successfully by hash", zap.Stringer("progressID", returnedID))
	return returnedID, nil
}
