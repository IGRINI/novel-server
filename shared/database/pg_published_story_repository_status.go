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
	findAndMarkStaleQueryBase = `
        UPDATE published_stories
        SET status = $1, error_details = $2, updated_at = NOW()
        WHERE status = ANY($3::story_status[])
    `
	checkInitialGenStatusQuery  = `SELECT status FROM published_stories WHERE id = $1`
	updateStatusFlagsSetupQuery = `
        UPDATE published_stories
        SET
            status = $2::story_status,
            setup = $3,
            is_first_scene_pending = $4,
            are_images_pending = $5,
            internal_generation_step = $6::internal_generation_step,
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
            pending_char_gen_tasks = $5,
            pending_card_img_tasks = $6,
            pending_char_img_tasks = $7,
            error_details = $8,
            internal_generation_step = $9::internal_generation_step,
            updated_at = NOW()
        WHERE id = $1
    `
)

// UpdateStatusDetails обновляет статус, детали ошибки или setup опубликованной истории.
func (r *pgPublishedStoryRepository) UpdateStatusDetails(ctx context.Context, querier interfaces.DBTX, id uuid.UUID, status models.StoryStatus, setup json.RawMessage, title, description, errorDetails *string) error {
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

	tag, err := querier.Exec(ctx, query, args...)
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
func (r *pgPublishedStoryRepository) UpdateVisibility(ctx context.Context, querier interfaces.DBTX, storyID, userID uuid.UUID, isPublic bool, requiredStatus models.StoryStatus) error {
	logFields := []zap.Field{
		zap.String("publishedStoryID", storyID.String()),
		zap.String("userID", userID.String()),
		zap.Bool("isPublic", isPublic),
		zap.String("requiredStatus", string(requiredStatus)),
	}
	r.logger.Debug("Updating story visibility with status check", logFields...)

	commandTag, err := querier.Exec(ctx, updateVisibilityQuery, isPublic, storyID, userID, requiredStatus)
	if err != nil {
		r.logger.Error("Failed to execute visibility update query", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка обновления видимости истории %s: %w", storyID, err)
	}

	if commandTag.RowsAffected() == 0 {
		// Check reason for failure - use querier for the check query as well
		var ownerID uuid.UUID
		var currentStatus models.StoryStatus
		checkErr := querier.QueryRow(ctx, checkStoryOwnerStatusQuery, storyID).Scan(&ownerID, &currentStatus)

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
		models.StatusModerationPending,
		models.StatusProtagonistGoalPending,
		models.StatusScenePlannerPending,
		models.StatusSubTasksPending,
		models.StatusSetupPending,
		models.StatusFirstScenePending,
		models.StatusImageGenerationPending,
		models.StatusJsonGenerationPending,
		models.StatusInitialGeneration,
	}

	logFields := []zap.Field{
		zap.Duration("staleThreshold", staleThreshold),
		zap.Any("staleStatuses", staleStatuses),
	}
	r.logger.Info("Finding and marking stale generating published stories as Error", logFields...)

	errorMessage := "Generation timed out or failed (marked as stale)"
	args := []interface{}{models.StatusError, errorMessage, pq.Array(staleStatuses)}
	var queryToUse string
	if staleThreshold > 0 {
		queryToUse = findAndMarkStaleQuery
		args = append(args, time.Now().UTC().Add(-staleThreshold))
	} else {
		queryToUse = findAndMarkStaleQueryBase
		r.logger.Info("Stale threshold is zero, checking all specified stale statuses regardless of time.", logFields...)
	}

	commandTag, err := querier.Exec(ctx, queryToUse, args...)
	if err != nil {
		r.logger.Error("Failed to execute update query for stale published stories", append(logFields, zap.Error(err), zap.String("query", queryToUse))...)
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

// UpdateStatusFlagsAndSetup обновляет статус, Setup, флаги ожидания и внутренний шаг генерации.
func (r *pgPublishedStoryRepository) UpdateStatusFlagsAndSetup(ctx context.Context, querier interfaces.DBTX, id uuid.UUID, status models.StoryStatus, setup json.RawMessage, isFirstScenePending bool, areImagesPending bool, internalStep *models.InternalGenerationStep) error {
	logFields := []zap.Field{
		zap.String("publishedStoryID", id.String()),
		zap.String("newStatus", string(status)),
		zap.Bool("isFirstScenePending", isFirstScenePending),
		zap.Bool("areImagesPending", areImagesPending),
		zap.Int("setupSize", len(setup)),
		zap.Any("internalStep", internalStep),
	}
	r.logger.Debug("Updating published story status, flags, setup, and internal step", logFields...)

	commandTag, err := querier.Exec(ctx, updateStatusFlagsSetupQuery, id, status, setup, isFirstScenePending, areImagesPending, internalStep)
	if err != nil {
		r.logger.Error("Failed to update published story status, flags, setup, and internal step", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка обновления статуса/флагов/setup/шага истории %s: %w", id, err)
	}

	if commandTag.RowsAffected() == 0 {
		r.logger.Warn("No rows affected when updating published story status, flags, setup, and internal step", logFields...)
		return models.ErrNotFound
	}

	r.logger.Info("Published story status, flags, setup, and internal step updated successfully", logFields...)
	return nil
}

// UpdateStatusFlagsAndDetails обновляет статус, флаги ожидания, счётчики задач, детали ошибки и внутренний шаг генерации.
func (r *pgPublishedStoryRepository) UpdateStatusFlagsAndDetails(ctx context.Context, querier interfaces.DBTX, id uuid.UUID, status models.StoryStatus, isFirstScenePending bool, areImagesPending bool, pendingCharGenTasks int, pendingCardImgTasks int, pendingCharImgTasks int, errorDetails *string, internalStep *models.InternalGenerationStep) error {
	logFields := []zap.Field{
		zap.String("publishedStoryID", id.String()),
		zap.String("newStatus", string(status)),
		zap.Bool("isFirstScenePending", isFirstScenePending),
		zap.Bool("areImagesPending", areImagesPending),
		zap.Int("pendingCharGenTasks", pendingCharGenTasks),
		zap.Int("pendingCardImgTasks", pendingCardImgTasks),
		zap.Int("pendingCharImgTasks", pendingCharImgTasks),
		zap.Stringp("errorDetails", errorDetails),
		zap.Any("internalStep", internalStep),
	}
	r.logger.Debug("Updating published story status, flags, details, and internal step", logFields...)

	commandTag, err := querier.Exec(ctx, updateStatusFlagsDetailsQuery, id, status, isFirstScenePending, areImagesPending, pendingCharGenTasks, pendingCardImgTasks, pendingCharImgTasks, errorDetails, internalStep)
	if err != nil {
		r.logger.Error("Failed to update published story status, flags, details, and internal step", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка обновления статуса/флагов/деталей/шага истории %s: %w", id, err)
	}

	if commandTag.RowsAffected() == 0 {
		r.logger.Warn("No rows affected when updating published story status, flags, details, and internal step", logFields...)
		return models.ErrNotFound
	}

	r.logger.Info("Published story status, flags, details, and internal step updated successfully", logFields...)
	return nil
}

// UpdateAfterModeration обновляет историю после завершения задачи модерации.
// Устанавливает IsAdultContent, новый статус, опционально детали ошибки и внутренний шаг генерации.
func (r *pgPublishedStoryRepository) UpdateAfterModeration(ctx context.Context, querier interfaces.DBTX, id uuid.UUID, status models.StoryStatus, isAdultContent bool, errorDetails *string, internalStep *models.InternalGenerationStep) error {
	query := `
        UPDATE published_stories
        SET
            status = $2::story_status,
            is_adult_content = $3,
            error_details = $4, -- Передаем errorDetails как есть (может быть NULL)
            internal_generation_step = $5::internal_generation_step,
            updated_at = NOW()
        WHERE id = $1
    `
	logFields := []zap.Field{
		zap.String("publishedStoryID", id.String()),
		zap.String("newStatus", string(status)),
		zap.Bool("isAdultContent", isAdultContent),
		zap.Stringp("errorDetails", errorDetails),
		zap.Any("internalStep", internalStep),
	}
	r.logger.Debug("Updating published story after moderation", logFields...)

	commandTag, err := querier.Exec(ctx, query, id, status, isAdultContent, errorDetails, internalStep)
	if err != nil {
		r.logger.Error("Failed to update published story after moderation", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка обновления истории %s после модерации: %w", id, err)
	}

	if commandTag.RowsAffected() == 0 {
		r.logger.Warn("No rows affected when updating published story after moderation (story not found?)", logFields...)
		return models.ErrNotFound
	}

	r.logger.Info("Published story updated successfully after moderation", logFields...)
	return nil
}

// UpdateStatusAndError обновляет статус и детали ошибки для опубликованной истории.
func (r *pgPublishedStoryRepository) UpdateStatusAndError(ctx context.Context, querier interfaces.DBTX, id uuid.UUID, status models.StoryStatus, errorDetails *string) error {
	logFields := []zap.Field{
		zap.String("publishedStoryID", id.String()),
		zap.String("newStatus", string(status)),
	}
	if errorDetails != nil {
		logFields = append(logFields, zap.Stringp("errorDetails", errorDetails))
	} else {
		logFields = append(logFields, zap.Bool("clearErrorDetails", true))
	}
	r.logger.Debug("Updating published story status and error", logFields...)

	query := `
		UPDATE published_stories
		SET
			status = $2::story_status,
			error_details = $3,
			updated_at = NOW()
		WHERE id = $1
	`
	commandTag, err := querier.Exec(ctx, query, id, status, errorDetails)
	if err != nil {
		r.logger.Error("Failed to update published story status and error", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка обновления статуса и ошибки истории %s: %w", id, err)
	}

	if commandTag.RowsAffected() == 0 {
		r.logger.Warn("No rows affected when updating published story status and error (story not found?)", logFields...)
		return models.ErrNotFound
	}

	r.logger.Info("Published story status and error updated successfully", logFields...)
	return nil
}

// UpdateSetup обновляет поле setup для опубликованной истории.
func (r *pgPublishedStoryRepository) UpdateSetup(ctx context.Context, querier interfaces.DBTX, id uuid.UUID, setup json.RawMessage) error {
	logFields := []zap.Field{
		zap.String("publishedStoryID", id.String()),
		zap.Int("setupSize", len(setup)),
	}
	r.logger.Debug("Updating published story setup", logFields...)

	query := `
		UPDATE published_stories
		SET
			setup = $2,
			updated_at = NOW()
		WHERE id = $1
	`
	commandTag, err := querier.Exec(ctx, query, id, setup)
	if err != nil {
		r.logger.Error("Failed to update published story setup", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка обновления setup для истории %s: %w", id, err)
	}

	if commandTag.RowsAffected() == 0 {
		r.logger.Warn("No rows affected when updating published story setup (story not found?)", logFields...)
		return models.ErrNotFound
	}

	r.logger.Info("Published story setup updated successfully", logFields...)
	return nil
}

// UpdateSetupStatusAndCounters обновляет setup, статус, счетчики ожидающих задач и внутренний шаг генерации.
func (r *pgPublishedStoryRepository) UpdateSetupStatusAndCounters(ctx context.Context, querier interfaces.DBTX, id uuid.UUID, setup json.RawMessage, status models.StoryStatus, pendingCharGen, pendingCardImg, pendingCharImg int, internalStep *models.InternalGenerationStep) error {
	query := `
        UPDATE published_stories
        SET
            setup = $2,
            status = $3::story_status,
            pending_char_gen_tasks = $4, -- Убрано преобразование в bool, т.к. поле теперь int
            pending_card_img_tasks = $5,
            pending_char_img_tasks = $6,
            internal_generation_step = $7::internal_generation_step, -- Добавлен шаг
            updated_at = NOW(),
            error_details = NULL -- Сбрасываем ошибку при этом обновлении
        WHERE id = $1
    `
	logFields := []zap.Field{
		zap.String("publishedStoryID", id.String()),
		zap.Int("setupSize", len(setup)),
		zap.String("newStatus", string(status)),
		zap.Int("pendingCharGen", pendingCharGen),
		zap.Int("pendingCardImg", pendingCardImg),
		zap.Int("pendingCharImg", pendingCharImg),
		zap.Any("internalStep", internalStep),
	}
	r.logger.Debug("Updating published story setup, status, counters, and internal step", logFields...)

	commandTag, err := querier.Exec(ctx, query, id, setup, status, pendingCharGen, pendingCardImg, pendingCharImg, internalStep)
	if err != nil {
		r.logger.Error("Failed to update published story setup, status, counters, and internal step", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка обновления setup/статуса/счетчиков/шага истории %s: %w", id, err)
	}

	if commandTag.RowsAffected() == 0 {
		r.logger.Warn("No rows affected when updating published story setup, status, counters, and internal step", logFields...)
		return models.ErrNotFound
	}

	r.logger.Info("Published story setup, status, counters, and internal step updated successfully", logFields...)
	return nil
}

// UpdateCountersAndMaybeStatus атомарно обновляет счетчики задач и, если все счетчики <= 0,
// обновляет статус истории. Возвращает true, если все задачи завершены, и финальный статус.
func (r *pgPublishedStoryRepository) UpdateCountersAndMaybeStatus(ctx context.Context, querier interfaces.DBTX, id uuid.UUID, decrementCharGen int, incrementCharImg int, decrementCardImg int, decrementCharImg int, newStatusIfComplete models.StoryStatus) (allTasksComplete bool, finalStatus models.StoryStatus, err error) {
	logFields := []zap.Field{
		zap.String("publishedStoryID", id.String()),
		zap.Int("decrementCharGen", decrementCharGen),
		zap.Int("incrementCharImg", incrementCharImg),
		zap.Int("decrementCardImg", decrementCardImg),
		zap.Int("decrementCharImg", decrementCharImg),
		zap.String("newStatusIfComplete", string(newStatusIfComplete)),
	}
	r.logger.Debug("Updating counters and maybe status for published story", logFields...)

	query := `
		UPDATE published_stories
		SET
			pending_char_gen_tasks = GREATEST(0, pending_char_gen_tasks - $2),
			pending_card_img_tasks = GREATEST(0, pending_card_img_tasks - $3),
			pending_char_img_tasks = GREATEST(0, pending_char_img_tasks + $4 - $5),
			status = CASE
				WHEN (GREATEST(0, pending_char_gen_tasks - $2) <= 0 AND
					  GREATEST(0, pending_card_img_tasks - $3) <= 0 AND
					  GREATEST(0, pending_char_img_tasks + $4 - $5) <= 0)
				THEN $6::story_status
				ELSE status
			END,
			updated_at = NOW()
		WHERE id = $1
		RETURNING status, pending_char_gen_tasks, pending_card_img_tasks, pending_char_img_tasks
	`

	var newCharGenTasks, newCardImgTasks, newCharImgTasks int
	err = querier.QueryRow(ctx, query, id, decrementCharGen, decrementCardImg, incrementCharImg, decrementCharImg, newStatusIfComplete).
		Scan(&finalStatus, &newCharGenTasks, &newCardImgTasks, &newCharImgTasks)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			r.logger.Warn("No rows affected when updating counters and maybe status (story not found?)", logFields...)
			return false, "", models.ErrNotFound
		}
		r.logger.Error("Failed to update counters and maybe status for published story", append(logFields, zap.Error(err))...)
		return false, "", fmt.Errorf("ошибка обновления счетчиков и статуса истории %s: %w", id, err)
	}

	allTasksComplete = newCharGenTasks <= 0 && newCardImgTasks <= 0 && newCharImgTasks <= 0

	logFields = append(logFields,
		zap.Bool("allTasksComplete", allTasksComplete),
		zap.String("finalStatus", string(finalStatus)),
		zap.Int("newCharGenTasks", newCharGenTasks),
		zap.Int("newCardImgTasks", newCardImgTasks),
		zap.Int("newCharImgTasks", newCharImgTasks),
	)
	r.logger.Info("Counters and maybe status updated successfully for published story", logFields...)
	return allTasksComplete, finalStatus, nil
}
