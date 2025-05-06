package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"novel-server/shared/interfaces"
	"novel-server/shared/models"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/lib/pq"
	"go.uber.org/zap"
)

// Константы для операций со статусом
const (
	baseUpdateStatusDetailsQuery = `UPDATE published_stories SET status = $2, updated_at = $3`
	updateVisibilityQuery        = `UPDATE published_stories SET is_public = $1, updated_at = NOW() WHERE id = $2 AND user_id = $3 AND status = $4`
	checkStoryOwnerStatusQuery   = `SELECT user_id, status FROM published_stories WHERE id = $1`
	updateConfigSetupStatusQuery = `
        UPDATE published_stories
        SET config = $2, setup = $3, status = $4::story_status, updated_at = NOW()
        WHERE id = $1
    `
	countActiveGenerationsQuery = `SELECT COUNT(*) FROM published_stories WHERE user_id = $1 AND (status = $2 OR status = $3 OR status = $4)` // Assuming $2=SetupPending, $3=FirstScenePending, $4=InitialGeneration
	countByStatusQuery          = `SELECT COUNT(*) FROM published_stories WHERE status = $1`
	findAndMarkStaleQuery       = `
        UPDATE published_stories
        SET status = $1, error_details = $2, updated_at = NOW()
        WHERE status = ANY($3::story_status[]) AND updated_at < $4
    `
	checkInitialGenStatusQuery  = `SELECT status FROM published_stories WHERE id = $1`
	updateStatusFlagsSetupQuery = `
        UPDATE published_stories
        SET
            status = $2::story_status,
            setup = $3,
            is_first_scene_pending = $4,
            are_images_pending = $5,
            updated_at = NOW(),
            error_details = NULL -- Reset error on successful setup update
        WHERE id = $1
    `
	updateStatusFlagsDetailsQuery = `
        UPDATE published_stories
        SET
            status = $2::story_status,
            is_first_scene_pending = $3,
            are_images_pending = $4,
            error_details = $5,
            updated_at = NOW()
        WHERE id = $1
    `
)

// UpdateStatusDetails обновляет статус, детали ошибки или setup опубликованной истории.
func (r *pgPublishedStoryRepository) UpdateStatusDetails(ctx context.Context, id uuid.UUID, status models.StoryStatus, setup json.RawMessage, title, description, errorDetails *string) error {
	query := baseUpdateStatusDetailsQuery
	args := []interface{}{id, status, time.Now().UTC()} // Use UTC
	paramIndex := 4                                     // Start after id, status, updated_at

	logFields := []zap.Field{
		zap.String("publishedStoryID", id.String()),
		zap.String("newStatus", string(status)),
	}

	if setup != nil {
		query += fmt.Sprintf(", setup = $%d", paramIndex)
		args = append(args, setup)
		paramIndex++
		logFields = append(logFields, zap.Int("setupSize", len(setup)))
	}
	if title != nil {
		query += fmt.Sprintf(", title = $%d", paramIndex)
		args = append(args, *title)
		paramIndex++
		logFields = append(logFields, zap.Stringp("newTitle", title))
	}
	if description != nil {
		query += fmt.Sprintf(", description = $%d", paramIndex)
		args = append(args, *description)
		paramIndex++
		logFields = append(logFields, zap.Stringp("newDescription", description))
	}
	if errorDetails != nil {
		query += fmt.Sprintf(", error_details = $%d", paramIndex)
		args = append(args, *errorDetails)
		paramIndex++
		logFields = append(logFields, zap.Stringp("errorDetails", errorDetails))
	} else {
		// If errorDetails is explicitly nil, set it to NULL in DB
		query += fmt.Sprintf(", error_details = $%d", paramIndex)
		args = append(args, nil)
		paramIndex++
		logFields = append(logFields, zap.Bool("clearErrorDetails", true))
	}

	query += " WHERE id = $1"

	r.logger.Debug("Updating published story status/details", append(logFields, zap.String("query", query))...)

	tag, err := r.db.Exec(ctx, query, args...)
	if err != nil {
		r.logger.Error("Failed to update published story status/details", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка обновления статуса/деталей опубликованной истории %s: %w", id, err)
	}

	if tag.RowsAffected() == 0 {
		r.logger.Warn("No rows affected when updating published story status/details", logFields...)
		return models.ErrNotFound // Story not found
	}

	r.logger.Info("Published story status and details updated successfully", logFields...)
	return nil
}

// UpdateVisibility обновляет видимость истории.
func (r *pgPublishedStoryRepository) UpdateVisibility(ctx context.Context, storyID, userID uuid.UUID, isPublic bool, requiredStatus models.StoryStatus) error {
	logFields := []zap.Field{
		zap.String("publishedStoryID", storyID.String()),
		zap.String("userID", userID.String()),
		zap.Bool("isPublic", isPublic),
		zap.String("requiredStatus", string(requiredStatus)),
	}
	r.logger.Debug("Updating story visibility with status check", logFields...)

	commandTag, err := r.db.Exec(ctx, updateVisibilityQuery, isPublic, storyID, userID, requiredStatus)
	if err != nil {
		r.logger.Error("Failed to execute visibility update query", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка обновления видимости истории %s: %w", storyID, err)
	}

	if commandTag.RowsAffected() == 0 {
		// Check reason for failure
		var ownerID uuid.UUID
		var currentStatus models.StoryStatus
		checkErr := r.db.QueryRow(ctx, checkStoryOwnerStatusQuery, storyID).Scan(&ownerID, &currentStatus)

		if checkErr != nil {
			if errors.Is(checkErr, pgx.ErrNoRows) {
				r.logger.Warn("Attempted to update visibility for non-existent story", logFields...)
				return models.ErrNotFound
			}
			r.logger.Error("Failed to check story details after visibility update failed", append(logFields, zap.Error(checkErr))...)
			return fmt.Errorf("ошибка проверки истории после неудачного обновления видимости: %w", checkErr)
		}

		if ownerID != userID {
			r.logger.Warn("Attempted to update visibility for story not owned by user", logFields...)
			return models.ErrForbidden
		}
		if currentStatus != requiredStatus {
			r.logger.Warn("Attempted to update visibility for story with incorrect status", append(logFields, zap.String("currentStatus", string(currentStatus)))...)
			return models.ErrStoryNotReadyForPublishing // Use the shared error
		}

		r.logger.Error("Visibility update failed for unknown reason despite passing checks", logFields...)
		return fmt.Errorf("неизвестная ошибка при обновлении видимости истории %s", storyID)
	}

	r.logger.Info("Story visibility updated successfully", logFields...)
	return nil
}

// UpdateConfigAndSetupAndStatus обновляет конфиг, setup и статус опубликованной истории.
func (r *pgPublishedStoryRepository) UpdateConfigAndSetupAndStatus(ctx context.Context, querier interfaces.DBTX, id uuid.UUID, config, setup json.RawMessage, status models.StoryStatus) error {
	logFields := []zap.Field{
		zap.String("publishedStoryID", id.String()),
		zap.String("newStatus", string(status)),
	}
	r.logger.Debug("Updating published story config/setup/status", logFields...)

	commandTag, err := querier.Exec(ctx, updateConfigSetupStatusQuery, id, config, setup, status)
	if err != nil {
		r.logger.Error("Failed to update published story config/setup/status", append(logFields, zap.Error(err))...)
		return fmt.Errorf("failed to update config/setup/status for story %s: %w", id, err)
	}
	if commandTag.RowsAffected() == 0 {
		r.logger.Warn("Attempted to update config/setup/status for non-existent published story", logFields...)
		return models.ErrNotFound
	}
	r.logger.Info("Published story config/setup/status updated successfully", logFields...)
	return nil
}

// CountActiveGenerationsForUser подсчитывает количество опубликованных историй с активными статусами генерации.
func (r *pgPublishedStoryRepository) CountActiveGenerationsForUser(ctx context.Context, querier interfaces.DBTX, userID uuid.UUID) (int, error) {
	activeStatuses := []models.StoryStatus{
		models.StatusSetupPending,
		models.StatusFirstScenePending,
		models.StatusInitialGeneration,
	}

	var count int
	logFields := []zap.Field{zap.String("userID", userID.String()), zap.Any("activeStatuses", activeStatuses)}
	r.logger.Debug("Counting active generations for user in published_stories", logFields...)

	err := querier.QueryRow(ctx, countActiveGenerationsQuery, userID, activeStatuses[0], activeStatuses[1], activeStatuses[2]).Scan(&count)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil // Not an error
		}
		r.logger.Error("Failed to count active generations in published_stories", append(logFields, zap.Error(err))...)
		return 0, fmt.Errorf("ошибка подсчета активных генераций для user %s: %w", userID.String(), err)
	}

	r.logger.Debug("Active generations count retrieved from published_stories", append(logFields, zap.Int("count", count))...)
	return count, nil
}

// CountByStatus подсчитывает количество опубликованных историй по заданному статусу.
func (r *pgPublishedStoryRepository) CountByStatus(ctx context.Context, querier interfaces.DBTX, status models.StoryStatus) (int, error) {
	log := r.logger.With(zap.String("status", string(status)))

	var count int
	err := querier.QueryRow(ctx, countByStatusQuery, status).Scan(&count)
	if err != nil {
		log.Error("Failed to count published stories by status", zap.Error(err))
		return 0, fmt.Errorf("failed to query story count by status: %w", err)
	}

	log.Debug("Successfully counted published stories by status", zap.Int("count", count))
	return count, nil
}

// FindAndMarkStaleGeneratingAsError находит "зависшие" истории в процессе генерации и устанавливает им статус Error.
func (r *pgPublishedStoryRepository) FindAndMarkStaleGeneratingAsError(ctx context.Context, querier interfaces.DBTX, staleThreshold time.Duration) (int64, error) {
	staleStatuses := []models.StoryStatus{
		models.StatusSetupPending,
		models.StatusFirstScenePending,
		models.StatusInitialGeneration,
	}

	logFields := []zap.Field{
		zap.Duration("staleThreshold", staleThreshold),
		zap.Any("staleStatuses", staleStatuses),
	}
	r.logger.Info("Finding and marking stale generating published stories as Error", logFields...)

	errorMessage := "Generation timed out or failed (marked as stale)"
	args := []interface{}{models.StatusError, errorMessage, pq.Array(staleStatuses)}
	query := findAndMarkStaleQuery

	if staleThreshold > 0 {
		args = append(args, time.Now().UTC().Add(-staleThreshold)) // Append threshold time
	} else {
		// If threshold is 0, we still need a time comparison to satisfy the query structure where $4 is expected.
		// Use a very old time to effectively check all records matching the status.
		args = append(args, time.Time{}) // Effectively no time limit
		r.logger.Info("Stale threshold is zero, checking all specified stale statuses regardless of time.", logFields...)
	}

	commandTag, err := querier.Exec(ctx, query, args...)
	if err != nil {
		r.logger.Error("Failed to execute update query for stale published stories", append(logFields, zap.Error(err), zap.String("query", query))...)
		return 0, fmt.Errorf("ошибка обновления статуса зависших опубликованных историй: %w", err)
	}

	updatedCount := commandTag.RowsAffected()
	r.logger.Info("FindAndMarkStaleGeneratingAsError completed", append(logFields, zap.Int64("updated_count", updatedCount))...)
	return updatedCount, nil
}

// CheckInitialGenerationStatus проверяет, готовы ли Setup и Первая сцена.
func (r *pgPublishedStoryRepository) CheckInitialGenerationStatus(ctx context.Context, querier interfaces.DBTX, id uuid.UUID) (bool, error) {
	var status models.StoryStatus
	logFields := []zap.Field{zap.String("publishedStoryID", id.String())}
	r.logger.Debug("Checking initial generation status for published story", logFields...)

	err := querier.QueryRow(ctx, checkInitialGenStatusQuery, id).Scan(&status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			r.logger.Warn("Published story not found for initial generation status check", logFields...)
			return false, models.ErrNotFound
		}
		r.logger.Error("Failed to query status for initial generation check", append(logFields, zap.Error(err))...)
		return false, fmt.Errorf("ошибка получения статуса истории %s: %w", id, err)
	}

	isReady := (status == models.StatusReady)
	r.logger.Debug("Initial generation status check complete", append(logFields, zap.Bool("isReady", isReady))...)
	return isReady, nil
}

// UpdateStatusFlagsAndSetup обновляет статус, Setup и флаги ожидания для истории.
func (r *pgPublishedStoryRepository) UpdateStatusFlagsAndSetup(ctx context.Context, querier interfaces.DBTX, id uuid.UUID, status models.StoryStatus, setup json.RawMessage, isFirstScenePending bool, areImagesPending bool) error {
	logFields := []zap.Field{
		zap.String("publishedStoryID", id.String()),
		zap.String("newStatus", string(status)),
		zap.Int("setupSize", len(setup)),
		zap.Bool("isFirstScenePending", isFirstScenePending),
		zap.Bool("areImagesPending", areImagesPending),
	}
	r.logger.Debug("Updating published story status, flags, and setup", logFields...)

	commandTag, err := querier.Exec(ctx, updateStatusFlagsSetupQuery, id, status, setup, isFirstScenePending, areImagesPending)
	if err != nil {
		r.logger.Error("Failed to update published story status, flags, and setup", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка обновления статуса/флагов/Setup для истории %s: %w", id, err)
	}

	if commandTag.RowsAffected() == 0 {
		r.logger.Warn("No rows affected when updating published story status, flags, and setup (story not found?)", logFields...)
		return models.ErrNotFound
	}

	r.logger.Info("Published story status, flags, and setup updated successfully", logFields...)
	return nil
}

// UpdateStatusFlagsAndDetails обновляет статус, флаги ожидания и детали ошибки.
func (r *pgPublishedStoryRepository) UpdateStatusFlagsAndDetails(ctx context.Context, querier interfaces.DBTX, id uuid.UUID, status models.StoryStatus, isFirstScenePending bool, areImagesPending bool, errorDetails *string) error {
	logFields := []zap.Field{
		zap.String("publishedStoryID", id.String()),
		zap.String("newStatus", string(status)),
		zap.Bool("isFirstScenePending", isFirstScenePending),
		zap.Bool("areImagesPending", areImagesPending),
	}
	if errorDetails != nil {
		logFields = append(logFields, zap.Stringp("errorDetails", errorDetails))
	} else {
		logFields = append(logFields, zap.Bool("clearErrorDetails", true))
	}
	r.logger.Debug("Updating published story status, flags, and error details", logFields...)

	commandTag, err := querier.Exec(ctx, updateStatusFlagsDetailsQuery, id, status, isFirstScenePending, areImagesPending, errorDetails)
	if err != nil {
		r.logger.Error("Failed to update published story status, flags, and error details", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка обновления статуса/флагов/деталей ошибки для истории %s: %w", id, err)
	}

	if commandTag.RowsAffected() == 0 {
		r.logger.Warn("No rows affected when updating published story status, flags, and error details (story not found?)", logFields...)
		return models.ErrNotFound
	}

	r.logger.Info("Published story status, flags, and error details updated successfully", logFields...)
	return nil
}
